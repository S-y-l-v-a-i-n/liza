package ops

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/git"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/paths"
	"github.com/liza-mas/liza/internal/testhelpers"
)

const orphanHealReviewer = "code-reviewer-1"

type orphanHealFixture struct {
	root, taskID, orphan, head, wt string
	bb                             *db.Blackboard
	authority                      models.AgentAuthority
}

// setupOrphanHeal builds a submitted task whose `commits` commits sit on a
// commit integration then rolls back. With conflict, integration then adds
// the task's first file with other content.
func setupOrphanHeal(t *testing.T, commits int, conflict bool) *orphanHealFixture {
	t.Helper()
	root := t.TempDir()
	testhelpers.SetupTestGitRepo(t, root)
	statePath, _ := testhelpers.SetupLizaDir(t, root)
	write := func(dir, name, contents string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
		testhelpers.MustGit(t, dir, "add", name)
	}
	testhelpers.MustGit(t, root, "checkout", "integration")
	before := testhelpers.MustGit(t, root, "rev-parse", "integration")
	write(root, "transient.txt", "transient merge\n")
	testhelpers.MustGit(t, root, "commit", "-m", "transient merge")
	orphan := testhelpers.MustGit(t, root, "rev-parse", "HEAD")

	const taskID = "task-orphan"
	g := git.New(root)
	if _, err := g.CreateWorktree(taskID, "integration"); err != nil {
		t.Fatal(err)
	}
	wt := g.GetWorktreePath(taskID)
	for i := 0; i < commits; i++ {
		write(wt, fmt.Sprintf("work-%d.txt", i), fmt.Sprintf("task work %d\n", i))
		testhelpers.MustGit(t, wt, "commit", "-m", fmt.Sprintf("task commit %d", i))
	}
	head := testhelpers.MustGit(t, wt, "rev-parse", "HEAD")

	testhelpers.MustGit(t, root, "reset", "--hard", before)
	if conflict {
		write(root, "work-0.txt", "integration owns this file\n")
	} else {
		write(root, "unrelated.txt", "unrelated\n")
	}
	testhelpers.MustGit(t, root, "commit", "-m", "integration moves on")

	state := testhelpers.CreateValidState()
	registerClaimReviewerTaskTestAgents(state)
	worktree := g.GetWorktreeRelPath(taskID)
	task := testhelpers.BuildTaskByStatus(taskID, models.TaskStatusReadyForReview, time.Now().UTC())
	task.RolePair = "coding-pair"
	task.SpecRef = ""
	task.Worktree = &worktree
	task.BaseCommit = &orphan
	task.ReviewCommit = &head
	state.Tasks = append(state.Tasks, task)
	bb := testhelpers.WriteInitialState(t, statePath, state)
	return &orphanHealFixture{
		root: root, taskID: taskID, orphan: orphan, head: head, wt: wt, bb: bb,
		authority: models.AgentAuthority{ID: orphanHealReviewer, Generation: testhelpers.TestAgentGeneration},
	}
}

func (fx *orphanHealFixture) heal(t *testing.T) (OrphanedBaseHealOutcome, error) {
	t.Helper()
	authority := fx.authority
	return HealOrphanedReviewBase(OrphanedBaseHealInput{
		ProjectRoot: fx.root, AgentID: orphanHealReviewer, Role: models.RoleCodeReviewer, Authority: &authority, TaskID: fx.taskID,
	})
}

// restart registers a new generation for the reviewer, as a restarted
// supervisor would, and makes it the caller's authority.
func (fx *orphanHealFixture) restart(t *testing.T) {
	t.Helper()
	const generation = "restarted-generation"
	if err := fx.bb.Modify(func(state *models.State) error {
		agent := state.Agents[orphanHealReviewer]
		agent.Generation = generation
		state.Agents[orphanHealReviewer] = agent
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	fx.authority.Generation = generation
}

func (fx *orphanHealFixture) task(t *testing.T) *models.Task {
	t.Helper()
	state, err := fx.bb.Read()
	if err != nil {
		t.Fatal(err)
	}
	return state.FindTask(fx.taskID)
}

func (fx *orphanHealFixture) worktreeHead(t *testing.T) string {
	t.Helper()
	return testhelpers.MustGit(t, fx.wt, "rev-parse", "HEAD")
}

func (fx *orphanHealFixture) reflogLength(t *testing.T) int {
	t.Helper()
	return len(strings.Split(testhelpers.MustGit(t, fx.wt, "reflog", "--format=%H"), "\n"))
}

func healEntries(task *models.Task) int {
	count := 0
	for _, entry := range task.History {
		if entry.Event == models.TaskEventReviewCommitUpdated && entry.Extra["orphaned_base_heal"] == true {
			count++
		}
	}
	return count
}

func assertHealPublished(t *testing.T, fx *orphanHealFixture, commits int) string {
	t.Helper()
	task := fx.task(t)
	integration := testhelpers.MustGit(t, fx.root, "rev-parse", "integration")
	head := fx.worktreeHead(t)
	if optionalValue(task.BaseCommit) != integration || optionalValue(task.ReviewCommit) != head || head == fx.head {
		t.Fatalf("boundary = (%s, %s), worktree HEAD %s; want (integration %s, rebased HEAD)", optionalValue(task.BaseCommit), optionalValue(task.ReviewCommit), head, integration)
	}
	g := git.New(fx.root)
	oldCommits, _, err := g.RangeCommits(fx.orphan, fx.head)
	if err != nil {
		t.Fatal(err)
	}
	newCommits, merges, err := g.RangeCommits(integration, head)
	if err != nil || merges || len(newCommits) != commits || len(oldCommits) != commits {
		t.Fatalf("ranges = %d old, %d new (merges %v, %v), want %d each", len(oldCommits), len(newCommits), merges, err, commits)
	}
	for i := range oldCommits {
		oldID, _ := g.PatchID(oldCommits[i])
		newID, _ := g.PatchID(newCommits[i])
		if oldID == "" || oldID != newID {
			t.Errorf("commit %d patch id %q -> %q, want preserved", i, oldID, newID)
		}
	}
	if n := healEntries(task); n != 1 {
		t.Errorf("heal history entries = %d, want exactly 1", n)
	}
	if orphanedBaseHealPending(task) {
		t.Error("heal preparation still pending after publication")
	}
	return head
}

func TestHealOrphanedReviewBasePreservesEveryCommit(t *testing.T) {
	t.Parallel()
	fx := setupOrphanHeal(t, 3, false)
	outcome, err := fx.heal(t)
	if err != nil || outcome != OrphanedBaseHealed {
		t.Fatalf("heal = %s, %v; want healed", outcome, err)
	}
	assertHealPublished(t, fx, 3)
}

func TestHealOrphanedReviewBaseIgnoresReachableBase(t *testing.T) {
	t.Parallel()
	fx := setupOrphanHeal(t, 1, false)
	reachable := testhelpers.MustGit(t, fx.root, "rev-parse", "integration~1")
	if err := fx.bb.Modify(func(state *models.State) error {
		state.FindTask(fx.taskID).BaseCommit = &reachable
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before := fx.reflogLength(t)
	outcome, err := fx.heal(t)
	if err != nil || outcome != OrphanedBaseHealNotApplicable {
		t.Fatalf("heal = %s, %v; want not_applicable", outcome, err)
	}
	if fx.worktreeHead(t) != fx.head || fx.reflogLength(t) != before {
		t.Error("a non-orphaned task's worktree was written")
	}
}

func TestHealOrphanedReviewBaseStaleAuthorityHasNoEffects(t *testing.T) {
	t.Parallel()
	fx := setupOrphanHeal(t, 1, false)
	fx.authority.Generation = "replaced-generation"
	before, reflog := fx.task(t), fx.reflogLength(t)
	_, err := fx.heal(t)
	if !IsAgentAuthorityError(err) {
		t.Fatalf("heal error = %v, want an agent authority error", err)
	}
	if fx.worktreeHead(t) != fx.head || fx.reflogLength(t) != reflog || !reflect.DeepEqual(before, fx.task(t)) {
		t.Error("a stale generation changed Git or task state")
	}
}

func TestHealOrphanedReviewBasePausedHasNoEffects(t *testing.T) {
	t.Parallel()
	fx := setupOrphanHeal(t, 1, false)
	if err := fx.bb.Modify(func(state *models.State) error {
		state.Config.Mode = models.SystemModePaused
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before, reflog := fx.task(t), fx.reflogLength(t)
	_, err := fx.heal(t)
	if !errors.Is(err, ErrSystemHalted) {
		t.Fatalf("heal error = %v, want a system-halted refusal", err)
	}
	if fx.worktreeHead(t) != fx.head || fx.reflogLength(t) != reflog || !reflect.DeepEqual(before, fx.task(t)) {
		t.Error("a paused system admitted Git or task changes")
	}
}

// The tests below install package hooks, so they do not run in parallel.

func abandonAfterRebase(t *testing.T) {
	t.Helper()
	orphanHealAfterRebaseTestHook = func() bool { return true }
	t.Cleanup(func() { orphanHealAfterRebaseTestHook = nil })
}

func TestHealOrphanedReviewBaseSettlesEffectLeftBeforePublication(t *testing.T) {
	fx := setupOrphanHeal(t, 2, false)
	abandonAfterRebase(t)
	if _, err := fx.heal(t); !errors.Is(err, errOrphanHealAbandonedForTest) {
		t.Fatalf("first heal error = %v, want abandonment", err)
	}
	orphanHealAfterRebaseTestHook = nil
	rebased := fx.worktreeHead(t)
	if rebased == fx.head || !orphanedBaseHealPending(fx.task(t)) {
		t.Fatalf("setup: HEAD %s, pending %v; want a rebased worktree with its preparation left", rebased, orphanedBaseHealPending(fx.task(t)))
	}

	fx.restart(t)
	outcome, err := fx.heal(t)
	if err != nil || outcome != OrphanedBaseHealed {
		t.Fatalf("settling heal = %s, %v; want healed", outcome, err)
	}
	if head := assertHealPublished(t, fx, 2); head != rebased {
		t.Errorf("worktree HEAD %s, want the left effect %s adopted, not redone", head, rebased)
	}
}

// The heal's own untouched stop is fenced as well: settlement cannot tell it
// from partial operator work. Once the operator aborts it, the next heal
// proves the effect absent and routes the conflict to integration recovery.
func TestHealOrphanedReviewBaseFencesInterruptedHealRebase(t *testing.T) {
	fx := setupOrphanHeal(t, 1, true)
	abandonAfterRebase(t)
	if _, err := fx.heal(t); !errors.Is(err, errOrphanHealAbandonedForTest) {
		t.Fatalf("first heal error = %v, want abandonment", err)
	}
	orphanHealAfterRebaseTestHook = nil
	before, err := git.New(fx.root).GetInterruptedRebase(fx.wt)
	if err != nil || before == nil {
		t.Fatalf("setup: interrupted rebase = %+v, %v; want one in progress", before, err)
	}

	fx.restart(t)
	_, err = fx.heal(t)
	var fenced *OrphanedBaseHealFencedError
	if !errors.As(err, &fenced) {
		t.Fatalf("settling heal error = %v, want a fenced settlement", err)
	}
	if after, err := git.New(fx.root).GetInterruptedRebase(fx.wt); err != nil || after == nil || *after != *before || !orphanedBaseHealPending(fx.task(t)) {
		t.Fatalf("after fencing: rebase %+v (%v), pending %v; want both kept", after, err, orphanedBaseHealPending(fx.task(t)))
	}

	testhelpers.MustGit(t, fx.wt, "rebase", "--abort")
	outcome, err := fx.heal(t)
	if err != nil || outcome != OrphanedBaseHealIntegrationFailed {
		t.Fatalf("heal after abort = %s, %v; want integration_failed (the replay conflicts)", outcome, err)
	}
	if operation, _ := git.New(fx.root).InterruptedOperation(fx.wt); operation != "" || fx.worktreeHead(t) != fx.head {
		t.Errorf("worktree = HEAD %s, interrupted %q; want the submitted HEAD at rest", fx.worktreeHead(t), operation)
	}
	task := fx.task(t)
	if task.Status != models.TaskStatusIntegrationFailed || orphanedBaseHealPending(task) {
		t.Errorf("status %s, pending %v; want INTEGRATION_FAILED with the preparation settled", task.Status, orphanedBaseHealPending(task))
	}
}

// resolveRebaseConflict stages an operator's resolution of work-0.txt in the
// interrupted rebase and returns that rebase.
func resolveRebaseConflict(t *testing.T, fx *orphanHealFixture, resolution string) *git.InterruptedRebase {
	t.Helper()
	if err := os.WriteFile(filepath.Join(fx.wt, "work-0.txt"), []byte(resolution), 0o644); err != nil {
		t.Fatal(err)
	}
	testhelpers.MustGit(t, fx.wt, "add", "work-0.txt")
	rebase, err := git.New(fx.root).GetInterruptedRebase(fx.wt)
	if err != nil || rebase == nil {
		t.Fatalf("setup: interrupted rebase = %+v, %v; want one in progress", rebase, err)
	}
	return rebase
}

// assertForeignRebaseKept restarts the reviewer and requires its heal to fence
// the operator's rebase, leaving it, its staged resolution and the
// preparation as they were.
func assertForeignRebaseKept(t *testing.T, fx *orphanHealFixture, before *git.InterruptedRebase, resolution string) {
	t.Helper()
	fx.restart(t)
	_, err := fx.heal(t)
	var fenced *OrphanedBaseHealFencedError
	if !errors.As(err, &fenced) {
		t.Fatalf("heal error = %v, want a fenced settlement", err)
	}
	after, err := git.New(fx.root).GetInterruptedRebase(fx.wt)
	if err != nil || after == nil || *after != *before {
		t.Errorf("interrupted rebase after heal = %+v, %v; want %+v untouched", after, err, before)
	}
	if staged := testhelpers.MustGit(t, fx.wt, "show", ":work-0.txt"); staged+"\n" != resolution {
		t.Errorf("staged work-0.txt = %q, want the operator's resolution %q", staged, resolution)
	}
	if !orphanedBaseHealPending(fx.task(t)) {
		t.Error("the heal preparation was retired although its effect is unattributed")
	}
}

// B1: the operator's documented manual repair, started after an abandoned
// heal and after integration moved again, is not the prepared heal.
func TestHealOrphanedReviewBaseFencesLaterManualRebase(t *testing.T) {
	fx := setupOrphanHeal(t, 1, false)
	abandonAfterRebase(t)
	if _, err := fx.heal(t); !errors.Is(err, errOrphanHealAbandonedForTest) {
		t.Fatalf("first heal error = %v, want abandonment", err)
	}
	orphanHealAfterRebaseTestHook = nil
	if err := os.WriteFile(filepath.Join(fx.root, "work-0.txt"), []byte("integration owns this file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testhelpers.MustGit(t, fx.root, "add", "work-0.txt")
	testhelpers.MustGit(t, fx.root, "commit", "-m", "integration moves again")
	var conflict *git.RebaseConflictError
	if err := git.New(fx.root).RebaseOnto(fx.wt, "integration"); !errors.As(err, &conflict) {
		t.Fatalf("setup: manual rebase error = %v, want a conflict", err)
	}
	const resolution = "human conflict resolution\n"
	assertForeignRebaseKept(t, fx, resolveRebaseConflict(t, fx, resolution), resolution)
}

// B1: an operator partway through the heal's stopped rebase — one conflict
// staged, another edited but still unmerged — keeps all of that work.
func TestHealOrphanedReviewBaseFencesPartlyResolvedHealRebase(t *testing.T) {
	fx := setupOrphanHeal(t, 1, true)
	commitSecond := func(dir, contents string, git ...string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "second.txt"), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
		testhelpers.MustGit(t, dir, "add", "second.txt")
		testhelpers.MustGit(t, dir, git...)
	}
	// The submitted commit also changes second.txt, which integration changes too.
	commitSecond(fx.wt, "task second\n", "commit", "--amend", "--no-edit")
	fx.head = fx.worktreeHead(t)
	if err := fx.bb.Modify(func(state *models.State) error {
		state.FindTask(fx.taskID).ReviewCommit = &fx.head
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	commitSecond(fx.root, "integration second\n", "commit", "-m", "integration changes second.txt")
	abandonAfterRebase(t)
	if _, err := fx.heal(t); !errors.Is(err, errOrphanHealAbandonedForTest) {
		t.Fatalf("first heal error = %v, want abandonment", err)
	}
	orphanHealAfterRebaseTestHook = nil

	const resolution = "human conflict resolution\n"
	before := resolveRebaseConflict(t, fx, resolution)
	const edit = "unstaged human edit\n"
	if err := os.WriteFile(filepath.Join(fx.wt, "second.txt"), []byte(edit), 0o644); err != nil {
		t.Fatal(err)
	}
	if unmerged := testhelpers.MustGit(t, fx.wt, "ls-files", "--unmerged"); !strings.Contains(unmerged, "second.txt") {
		t.Fatalf("setup: unmerged entries = %q, want second.txt still conflicted", unmerged)
	}
	assertForeignRebaseKept(t, fx, before, resolution)
	if data, err := os.ReadFile(filepath.Join(fx.wt, "second.txt")); err != nil || string(data) != edit {
		t.Errorf("second.txt = %q (%v), want the operator's unstaged edit", data, err)
	}
}

func TestHealOrphanedReviewBaseFencesUnprovenEffect(t *testing.T) {
	fx := setupOrphanHeal(t, 1, false)
	abandonAfterRebase(t)
	if _, err := fx.heal(t); !errors.Is(err, errOrphanHealAbandonedForTest) {
		t.Fatalf("first heal error = %v, want abandonment", err)
	}
	orphanHealAfterRebaseTestHook = nil
	if err := os.WriteFile(filepath.Join(fx.wt, "foreign.txt"), []byte("foreign\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testhelpers.MustGit(t, fx.wt, "add", "foreign.txt")
	testhelpers.MustGit(t, fx.wt, "commit", "-m", "foreign commit")
	foreign := fx.worktreeHead(t)

	fx.restart(t)
	_, err := fx.heal(t)
	var fenced *OrphanedBaseHealFencedError
	if !errors.As(err, &fenced) {
		t.Fatalf("heal error = %v, want a fenced settlement", err)
	}
	if fx.worktreeHead(t) != foreign || !orphanedBaseHealPending(fx.task(t)) {
		t.Error("an unproven effect was rewritten or its preparation retired")
	}
}

func TestHealOrphanedReviewBaseRestoresWhenBoundaryChangesBeforePublication(t *testing.T) {
	fx := setupOrphanHeal(t, 1, false)
	orphanHealPublishTestHook = func(bb *db.Blackboard, publish func() error) error {
		if err := bb.Modify(func(state *models.State) error {
			task := state.FindTask(fx.taskID)
			task.Approvals = append(task.Approvals, models.Approval{Agent: "code-reviewer-2", Timestamp: time.Now().UTC()})
			return nil
		}); err != nil {
			return err
		}
		return publish()
	}
	t.Cleanup(func() { orphanHealPublishTestHook = nil })
	outcome, err := fx.heal(t)
	if err == nil || outcome != OrphanedBaseHealNotApplicable {
		t.Fatalf("heal = %s, %v; want a refused publication", outcome, err)
	}
	task := fx.task(t)
	if fx.worktreeHead(t) != fx.head || optionalValue(task.ReviewCommit) != fx.head || orphanedBaseHealPending(task) {
		t.Errorf("HEAD %s, review %s, pending %v; want the submitted HEAD restored and the preparation retired", fx.worktreeHead(t), optionalValue(task.ReviewCommit), orphanedBaseHealPending(task))
	}
}

func TestHealOrphanedReviewBaseKeepsEffectOnLostAcknowledgement(t *testing.T) {
	fx := setupOrphanHeal(t, 1, false)
	orphanHealPublishTestHook = func(_ *db.Blackboard, publish func() error) error {
		if err := publish(); err != nil {
			return err
		}
		return errors.New("lock released after commit; acknowledgement lost")
	}
	t.Cleanup(func() { orphanHealPublishTestHook = nil })
	outcome, err := fx.heal(t)
	if err != nil || outcome != OrphanedBaseHealed {
		t.Fatalf("heal = %s, %v; want healed from the committed write", outcome, err)
	}
	assertHealPublished(t, fx, 1)
}

// The replayed boundary needs fresh evidence. A deterministic refusal routes
// the task to integration recovery and leaves the submitted commit in place.
func TestHealOrphanedReviewBaseRoutesAcceptanceRefusal(t *testing.T) {
	t.Parallel()
	root, taskID, orphan := setupSubmittedAcceptanceTask(t, true)
	state, err := db.For(paths.New(root).StatePath()).Read()
	if err != nil {
		t.Fatal(err)
	}
	head := optionalValue(state.FindTask(taskID).ReviewCommit)
	outcome, err := HealOrphanedReviewBase(OrphanedBaseHealInput{
		ProjectRoot: root, AgentID: orphanHealReviewer, Role: models.RoleCodeReviewer, TaskID: taskID,
		Authority: &models.AgentAuthority{ID: orphanHealReviewer, Generation: testhelpers.TestAgentGeneration},
	})
	if err != nil || outcome != OrphanedBaseHealIntegrationFailed {
		t.Fatalf("heal = %s, %v; want integration_failed on the acceptance refusal", outcome, err)
	}
	wt := git.New(root).GetWorktreePath(taskID)
	if got := testhelpers.MustGit(t, wt, "rev-parse", "HEAD"); got != head {
		t.Errorf("worktree HEAD %s, want the submitted %s restored", got, head)
	}
	after, err := db.For(paths.New(root).StatePath()).Read()
	if err != nil {
		t.Fatal(err)
	}
	task := after.FindTask(taskID)
	detail, _ := task.IntegrationFailure["detail"].(string)
	if task.Status != models.TaskStatusIntegrationFailed || !strings.Contains(detail, "acceptance") || !strings.Contains(detail, orphan) {
		t.Errorf("status %s, detail %q; want INTEGRATION_FAILED naming the acceptance refusal and orphan %s", task.Status, detail, orphan)
	}
}

// update-review-commit takes the per-task claim lock a heal holds for its
// whole invocation, so the two never interleave on one worktree. The lock's
// owner record names the last holder.
func TestUpdateReviewCommitTakesTheHealTaskLock(t *testing.T) {
	t.Parallel()
	fx := setupOrphanHeal(t, 1, false)
	if _, err := UpdateReviewCommit(fx.root, fx.taskID, "human"); err != nil {
		t.Fatalf("UpdateReviewCommit() error = %v", err)
	}
	data, err := os.ReadFile(claimTaskWorktreeLockPath(paths.New(fx.root).StatePath(), fx.taskID) + ".lock.owner.json")
	if err != nil {
		t.Fatalf("task lock owner record: %v", err)
	}
	var owner struct {
		Operation string `json:"operation"`
	}
	if err := json.Unmarshal(data, &owner); err != nil || owner.Operation != "update-review-commit" {
		t.Errorf("task lock owner operation = %q (%v), want update-review-commit", owner.Operation, err)
	}
}

// orphanStrictTask orphans the base of a strict runtime-input task and records
// it as submitted for review.
func orphanStrictTask(t *testing.T, f *runtimeInputFixture) (string, string) {
	t.Helper()
	orphan, head := orphanWorktreeBase(t, f.root, git.New(f.root).GetWorktreePath(f.taskID))
	if err := f.bb.Modify(func(state *models.State) error {
		registerClaimReviewerTaskTestAgents(state)
		task := state.FindTask(f.taskID)
		task.Status = models.TaskStatusReadyForReview
		task.LeaseExpires = nil
		task.ReviewCommit = &head
		task.BaseCommit = &orphan
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return orphan, head
}

func healStrictTask(f *runtimeInputFixture) (OrphanedBaseHealOutcome, error) {
	return HealOrphanedReviewBase(OrphanedBaseHealInput{
		ProjectRoot: f.root, AgentID: orphanHealReviewer, Role: models.RoleCodeReviewer, TaskID: f.taskID,
		Authority: &models.AgentAuthority{ID: orphanHealReviewer, Generation: testhelpers.TestAgentGeneration},
	})
}

// A strict task is healed only with evidence executed on the replayed HEAD,
// through the runtime-input gate. The runtime-input fixture redirects the key
// path, so these tests do not run in parallel.
func TestHealOrphanedReviewBaseRunsStrictAcceptanceOnTheReplay(t *testing.T) {
	f := setupRuntimeInputScenario(t, 0)
	_, head := orphanStrictTask(t, f)
	if outcome, err := healStrictTask(f); err != nil || outcome != OrphanedBaseHealed {
		t.Fatalf("heal = %s, %v; want healed", outcome, err)
	}
	newHead := testhelpers.MustGit(t, git.New(f.root).GetWorktreePath(f.taskID), "rev-parse", "HEAD")
	task := f.state(t).FindTask(f.taskID)
	receipt := task.AcceptanceReceipt
	if newHead == head || optionalValue(task.ReviewCommit) != newHead || receipt == nil || receipt.ReviewCommit != newHead {
		t.Fatalf("review_commit %s, receipt %+v; want a fresh receipt bound to the replayed HEAD %s", optionalValue(task.ReviewCommit), receipt, newHead)
	}
	if len(receipt.Commands) != 1 || receipt.Commands[0].ExitCode != 0 || !strings.Contains(receipt.Commands[0].Output, "PASS replay assertion") {
		t.Errorf("receipt commands = %+v, want the canonical command's passing output", receipt.Commands)
	}
	if runs := f.runs(t); len(runs) != 1 || runs[0] != runtimeInputFixtureValue+" 1" {
		t.Errorf("command runs = %q, want one run that received the input, spent before launch", runs)
	}
	if instance := f.instance(t, "w03"); instance.State != models.RuntimeInputConsumed || instance.Consumed == nil || instance.Consumed.Agent != orphanHealReviewer {
		t.Errorf("w03 instance = %+v, want consumed by the heal", instance)
	}
}

// Submission spent the single-use input; the heal must not run the command
// again on it and routes the task to its doer instead.
func TestHealOrphanedReviewBaseRoutesSpentSingleUseInput(t *testing.T) {
	f := setupRuntimeInputScenario(t, 0)
	if _, err := SubmitForReview(f.root, f.taskID, f.commit, f.agentID); err != nil {
		t.Fatalf("SubmitForReview: %v", err)
	}
	runs := len(f.runs(t))
	orphan, head := orphanStrictTask(t, f)
	if outcome, err := healStrictTask(f); err != nil || outcome != OrphanedBaseHealIntegrationFailed {
		t.Fatalf("heal = %s, %v; want integration_failed on the spent input", outcome, err)
	}
	if got := len(f.runs(t)); got != runs {
		t.Errorf("command runs = %d, want %d: nothing launched on a spent single-use input", got, runs)
	}
	if got := testhelpers.MustGit(t, git.New(f.root).GetWorktreePath(f.taskID), "rev-parse", "HEAD"); got != head {
		t.Errorf("worktree HEAD %s, want the submitted %s restored", got, head)
	}
	task := f.state(t).FindTask(f.taskID)
	detail, _ := task.IntegrationFailure["detail"].(string)
	if task.Status != models.TaskStatusIntegrationFailed || !strings.Contains(detail, "w03") || !strings.Contains(detail, orphan) {
		t.Errorf("status %s, detail %q; want INTEGRATION_FAILED naming input w03 and orphan %s", task.Status, detail, orphan)
	}
}

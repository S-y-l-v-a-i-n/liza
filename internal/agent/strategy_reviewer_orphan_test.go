package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	lizagit "github.com/liza-mas/liza/internal/git"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/testhelpers"
)

// D-26: a reviewable task whose base_commit left the integration branch (a
// merge rolled back or redone around a lock timeout, or a history rewrite)
// can never pass review-boundary validation again. These tests drive the real
// reviewer path — ops.ClaimReviewerTask, then the strategy's observation of
// the failure — the way the supervisor loop does.

type orphanVariant struct {
	relanded       bool // the orphaned commit's content returns to integration as a new commit
	conflict       bool // integration adds the task's own file with other content
	merge          bool // the task range contains a merge commit
	partially      bool // the task is partially approved by another reviewer
	reachableStale bool // control: base is stale but still on integration (not an orphan)
}

type orphanFixture struct {
	taskID   string
	orphan   string // the commit that left the integration branch
	head     string // the submitted worktree HEAD
	worktree string // absolute worktree path
}

// addOrphanedReviewableTask builds a submitted task whose commits sit on a
// commit that integration then rolls back, exactly the I-677 shape.
func (p *reviewClaimProject) addOrphanedReviewableTask(t *testing.T, taskID string, v orphanVariant) orphanFixture {
	t.Helper()
	write := func(dir, name, contents string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	testhelpers.MustGit(t, p.root, "checkout", "integration")
	before := testhelpers.MustGit(t, p.root, "rev-parse", "integration")

	// The transient merge: on integration long enough for the task to build on it.
	write(p.root, "transient-"+taskID+".txt", "transient merge content\n")
	testhelpers.MustGit(t, p.root, "add", "transient-"+taskID+".txt")
	testhelpers.MustGit(t, p.root, "commit", "-m", "transient merge for "+taskID)
	orphan := testhelpers.MustGit(t, p.root, "rev-parse", "HEAD")

	g := lizagit.New(p.root)
	if _, err := g.CreateWorktree(taskID, "integration"); err != nil {
		t.Fatalf("CreateWorktree(%s) error = %v", taskID, err)
	}
	wtPath := g.GetWorktreePath(taskID)
	write(wtPath, taskID+".txt", taskID+" work\n")
	testhelpers.MustGit(t, wtPath, "add", taskID+".txt")
	testhelpers.MustGit(t, wtPath, "commit", "-m", "Add "+taskID)
	if v.merge {
		testhelpers.MustGit(t, wtPath, "checkout", "-b", "side-"+taskID, orphan)
		write(wtPath, "side-"+taskID+".txt", "side work\n")
		testhelpers.MustGit(t, wtPath, "add", "side-"+taskID+".txt")
		testhelpers.MustGit(t, wtPath, "commit", "-m", "side work for "+taskID)
		testhelpers.MustGit(t, wtPath, "checkout", "task/"+taskID)
		testhelpers.MustGit(t, wtPath, "merge", "--no-ff", "-m", "merge side work", "side-"+taskID)
	}
	head := testhelpers.MustGit(t, wtPath, "rev-parse", "HEAD")

	base := orphan
	if v.reachableStale {
		// Control: integration keeps the commit; the recorded base is merely
		// an older ancestor — ordinary ADR-0078 metadata drift.
		base = before
	} else {
		// The rollback: the ref moves back past the commit the task built on.
		testhelpers.MustGit(t, p.root, "reset", "--hard", before)
		if v.relanded {
			write(p.root, "transient-"+taskID+".txt", "transient merge content\n")
			testhelpers.MustGit(t, p.root, "add", "transient-"+taskID+".txt")
			testhelpers.MustGit(t, p.root, "commit", "-m", "merge redone for "+taskID)
		}
	}
	if v.conflict {
		write(p.root, taskID+".txt", "integration owns this file now\n")
		testhelpers.MustGit(t, p.root, "add", taskID+".txt")
	} else {
		write(p.root, "unrelated-"+taskID+".txt", "unrelated integration work\n")
		testhelpers.MustGit(t, p.root, "add", "unrelated-"+taskID+".txt")
	}
	testhelpers.MustGit(t, p.root, "commit", "-m", "integration moves on past "+taskID)

	worktree := g.GetWorktreeRelPath(taskID)
	status := models.TaskStatusReadyForReview
	if v.partially {
		status = models.TaskStatusPartiallyApproved
	}
	if err := p.bb.Modify(func(state *models.State) error {
		task := testhelpers.BuildTaskByStatus(taskID, status, time.Now().UTC())
		task.RolePair = "coding-pair"
		task.Worktree = &worktree
		task.BaseCommit = &base
		task.ReviewCommit = &head
		if v.partially {
			task.Approvals = []models.Approval{{Agent: "code-reviewer-2", Provider: "other", Timestamp: time.Now().UTC()}}
		}
		state.Tasks = append(state.Tasks, task)
		return nil
	}); err != nil {
		t.Fatalf("add task %s: %v", taskID, err)
	}
	return orphanFixture{taskID: taskID, orphan: orphan, head: head, worktree: wtPath}
}

// driveReviewerClaims runs the supervisor's claim-then-observe step until a
// claim succeeds or attempts run out, and returns the claimed task ID.
func driveReviewerClaims(t *testing.T, reviewer *reviewerStrategy, project *reviewClaimProject, attempts int) (string, error) {
	t.Helper()
	config := project.supervisorConfig(t)
	var lastErr error
	for i := 0; i < attempts; i++ {
		taskID, _, err := reviewer.ClaimTask(config, project.bb)
		if err == nil {
			return taskID, nil
		}
		lastErr = err
		if decision := reviewer.ObserveClaimFailure(err); decision.Stop {
			return "", fmt.Errorf("supervisor stopped: %w", err)
		}
	}
	return "", lastErr
}

func (p *reviewClaimProject) task(t *testing.T, taskID string) *models.Task {
	t.Helper()
	state, err := p.bb.Read()
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	task := state.FindTask(taskID)
	if task == nil {
		t.Fatalf("task %s not found", taskID)
	}
	return task
}

func assertWorktreeUntouched(t *testing.T, fx orphanFixture) {
	t.Helper()
	if head := testhelpers.MustGit(t, fx.worktree, "rev-parse", "HEAD"); head != fx.head {
		t.Errorf("worktree HEAD = %s, want the submitted %s untouched", head, fx.head)
	}
	if status := testhelpers.MustGit(t, fx.worktree, "status", "--porcelain"); status != "" {
		t.Errorf("worktree status = %q, want clean", status)
	}
	gitDir := testhelpers.MustGit(t, fx.worktree, "rev-parse", "--git-dir")
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(fx.worktree, gitDir)
	}
	for _, dir := range []string{"rebase-merge", "rebase-apply"} {
		if _, err := os.Stat(filepath.Join(gitDir, dir)); err == nil {
			t.Errorf("worktree has an interrupted rebase (%s)", dir)
		}
	}
}

func assertHealedAndClaimed(t *testing.T, project *reviewClaimProject, fx orphanFixture, claimed string, claimErr error) {
	t.Helper()
	if claimed != fx.taskID {
		t.Fatalf("claimed %q (last error: %v), want %s claimed within one quarantine cycle", claimed, claimErr, fx.taskID)
	}
	task := project.task(t, fx.taskID)
	integration := testhelpers.MustGit(t, project.root, "rev-parse", "integration")
	newHead := testhelpers.MustGit(t, fx.worktree, "rev-parse", "HEAD")
	if task.BaseCommit == nil || *task.BaseCommit != integration {
		t.Errorf("base_commit = %v, want integration %s", task.BaseCommit, integration)
	}
	if task.ReviewCommit == nil || *task.ReviewCommit != newHead || newHead == fx.head {
		t.Errorf("review_commit = %v, worktree HEAD %s, old HEAD %s; want the rebased HEAD recorded", task.ReviewCommit, newHead, fx.head)
	}
	if task.ReviewingBy == nil || *task.ReviewingBy != project.reviewerID {
		t.Errorf("reviewing_by = %v, want %s", task.ReviewingBy, project.reviewerID)
	}
	g := lizagit.New(project.root)
	oldID, err := g.PatchID(fx.head)
	if err != nil || oldID == "" {
		t.Fatalf("PatchID(old) = %q, %v", oldID, err)
	}
	newID, err := g.PatchID(newHead)
	if err != nil || newID != oldID {
		t.Errorf("PatchID(new) = %q, %v; want the task's own change preserved (%q)", newID, err, oldID)
	}
	if absorbed, err := g.IsAncestor(fx.orphan, newHead); err != nil || absorbed {
		t.Errorf("IsAncestor(orphan %s, healed HEAD) = %v, %v; the review range must exclude the orphan", fx.orphan, absorbed, err)
	}
	healed := false
	for _, entry := range task.History {
		if entry.Event == models.TaskEventReviewCommitUpdated && entry.Extra["orphaned_base_heal"] == true {
			healed = true
		}
	}
	if !healed {
		t.Error("history has no review_commit_updated entry marked orphaned_base_heal")
	}
}

func assertRoutedToIntegrationFailed(t *testing.T, project *reviewClaimProject, fx orphanFixture) {
	t.Helper()
	task := project.task(t, fx.taskID)
	if task.Status != models.TaskStatusIntegrationFailed {
		t.Fatalf("status = %s, want %s (a visible engine route, not a quarantine retried forever)", task.Status, models.TaskStatusIntegrationFailed)
	}
	detail, _ := task.IntegrationFailure["detail"].(string)
	if !strings.Contains(detail, "rebase --onto") || !strings.Contains(detail, fx.orphan) {
		t.Errorf("integration_failure.detail = %q, want the rebase --onto repair naming orphan %s", detail, fx.orphan)
	}
	assertWorktreeUntouched(t, fx)
}

func TestReviewerOrphanedBaseHealsWhenContentRelanded(t *testing.T) {
	project := setupReviewClaimProject(t)
	fx := project.addOrphanedReviewableTask(t, "task-orphan", orphanVariant{relanded: true})
	claimed, err := driveReviewerClaims(t, newReviewerStrategyForTest(t), project, claimBreakerThreshold+1)
	assertHealedAndClaimed(t, project, fx, claimed, err)
}

func TestReviewerOrphanedBaseHealsWhenContentNotRelanded(t *testing.T) {
	project := setupReviewClaimProject(t)
	fx := project.addOrphanedReviewableTask(t, "task-orphan", orphanVariant{})
	claimed, err := driveReviewerClaims(t, newReviewerStrategyForTest(t), project, claimBreakerThreshold+1)
	assertHealedAndClaimed(t, project, fx, claimed, err)
}

func TestReviewerOrphanedBaseConflictRoutesToIntegrationFailed(t *testing.T) {
	project := setupReviewClaimProject(t)
	fx := project.addOrphanedReviewableTask(t, "task-orphan", orphanVariant{conflict: true})
	_, _ = driveReviewerClaims(t, newReviewerStrategyForTest(t), project, claimBreakerThreshold+1)
	assertRoutedToIntegrationFailed(t, project, fx)
}

func TestReviewerOrphanedBasePartiallyApprovedRoutesToIntegrationFailed(t *testing.T) {
	project := setupReviewClaimProject(t)
	fx := project.addOrphanedReviewableTask(t, "task-orphan", orphanVariant{partially: true})
	_, _ = driveReviewerClaims(t, newReviewerStrategyForTest(t), project, claimBreakerThreshold+1)
	assertRoutedToIntegrationFailed(t, project, fx)
}

func TestReviewerOrphanedBaseMergeInRangeRoutesToIntegrationFailed(t *testing.T) {
	project := setupReviewClaimProject(t)
	fx := project.addOrphanedReviewableTask(t, "task-orphan", orphanVariant{merge: true})
	_, _ = driveReviewerClaims(t, newReviewerStrategyForTest(t), project, claimBreakerThreshold+1)
	assertRoutedToIntegrationFailed(t, project, fx)
}

// Guard: a stale base that is still on integration is ordinary ADR-0078
// metadata drift. It keeps the documented repair path: no Git write, no
// route change, quarantine as before.
func TestReviewerReachableStaleBaseKeepsRepairQuarantine(t *testing.T) {
	project := setupReviewClaimProject(t)
	fx := project.addOrphanedReviewableTask(t, "task-stale", orphanVariant{reachableStale: true})
	recorded := *project.task(t, fx.taskID).BaseCommit
	claimed, _ := driveReviewerClaims(t, newReviewerStrategyForTest(t), project, claimBreakerThreshold+1)
	if claimed != "" {
		t.Fatalf("claimed %s, want the stale boundary to keep failing until repaired", claimed)
	}
	task := project.task(t, fx.taskID)
	if task.Status != models.TaskStatusReadyForReview {
		t.Errorf("status = %s, want %s", task.Status, models.TaskStatusReadyForReview)
	}
	if task.BaseCommit == nil || *task.BaseCommit != recorded || task.ReviewCommit == nil || *task.ReviewCommit != fx.head {
		t.Errorf("boundary = (%v, %v), want the recorded (%s, %s) unchanged", task.BaseCommit, task.ReviewCommit, recorded, fx.head)
	}
	assertWorktreeUntouched(t, fx)
}

// A heal left unsettled by a reviewer that died and never restarted stays
// "current" (absence is not retirement). It must reserve only its own task:
// the claim names it for the heal and claims the healthy task, and the heal
// then settles it — the D-26 (d) failure must not return in a new form.
func TestReviewerLeftoverOrphanHealDoesNotStallQueue(t *testing.T) {
	project := setupReviewClaimProject(t)
	fx := project.addOrphanedReviewableTask(t, "task-orphan", orphanVariant{})
	project.addReviewableTask(t, "task-healthy", false)
	digest := strings.Repeat("a", 64)
	if err := project.bb.Modify(func(state *models.State) error {
		for i := range state.Tasks {
			if state.Tasks[i].ID == "task-healthy" {
				state.Tasks[i].Priority = 2
			}
		}
		task := state.FindTask(fx.taskID)
		task.Lifecycle = &models.TaskLifecycle{Revision: 1}
		boundary := models.TaskTransitionID(task)
		task.Lifecycle.Preparation = &models.LifecyclePreparation{
			LifecycleIdentity: models.LifecycleIdentity{
				Operation: "heal-orphaned-review-base", Actor: "code-reviewer-2",
				ExpectedTransition: boundary, PayloadDigest: digest,
			},
			Boundary: boundary,
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	claimed, err := driveReviewerClaims(t, newReviewerStrategyForTest(t), project, 1)
	if claimed != "task-healthy" {
		t.Fatalf("claimed %q (%v), want the healthy task despite the leftover heal", claimed, err)
	}
	task := project.task(t, fx.taskID)
	if task.BaseCommit == nil || *task.BaseCommit != testhelpers.MustGit(t, project.root, "rev-parse", "integration") {
		t.Errorf("orphan base_commit = %v, want the leftover heal settled and the task healed", task.BaseCommit)
	}
	if healed := healEntryCount(task); healed != 1 {
		t.Errorf("heal history entries = %d, want 1", healed)
	}
}

// C1: a fenced heal leaves an unattributed effect in the worktree, so the
// quarantine anomaly must carry the heal's inspection guidance, not the plain
// rebase recipe the claim attached.
func TestReviewerFencedOrphanHealReachesQuarantineAnomaly(t *testing.T) {
	project := setupReviewClaimProject(t)
	fx := project.addOrphanedReviewableTask(t, "task-orphan", orphanVariant{})
	if err := project.bb.Modify(func(state *models.State) error {
		task := state.FindTask(fx.taskID)
		task.Lifecycle = &models.TaskLifecycle{Revision: 1}
		boundary := models.TaskTransitionID(task)
		task.Lifecycle.Preparation = &models.LifecyclePreparation{
			LifecycleIdentity: models.LifecycleIdentity{
				Operation: "heal-orphaned-review-base", Actor: "code-reviewer-2",
				ExpectedTransition: boundary, PayloadDigest: strings.Repeat("a", 64),
			},
			Boundary: boundary,
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fx.worktree, "foreign.txt"), []byte("foreign\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testhelpers.MustGit(t, fx.worktree, "add", "foreign.txt")
	testhelpers.MustGit(t, fx.worktree, "commit", "-m", "foreign commit")
	foreign := testhelpers.MustGit(t, fx.worktree, "rev-parse", "HEAD")

	if claimed, _ := driveReviewerClaims(t, newReviewerStrategyForTest(t), project, claimBreakerThreshold); claimed != "" {
		t.Fatalf("claimed %s, want the fenced task to stay unclaimed", claimed)
	}
	anomalies := project.anomaliesOfType(t, models.AnomalyTypeReviewerClaimCircuitOpen)
	if len(anomalies) != 1 || anomalies[0].Task != fx.taskID {
		t.Fatalf("circuit-open anomalies = %+v, want one for %s", anomalies, fx.taskID)
	}
	if recovery, _ := anomalies[0].Details["recovery"].(string); !strings.Contains(recovery, "heal is fenced") || !strings.Contains(recovery, "inspect") {
		t.Errorf("anomaly recovery = %q, want the fenced heal's inspection guidance", recovery)
	}
	if head := testhelpers.MustGit(t, fx.worktree, "rev-parse", "HEAD"); head != foreign {
		t.Errorf("worktree HEAD = %s, want the unattributed %s untouched", head, foreign)
	}
}

func healEntryCount(task *models.Task) int {
	count := 0
	for _, entry := range task.History {
		if entry.Event == models.TaskEventReviewCommitUpdated && entry.Extra["orphaned_base_heal"] == true {
			count++
		}
	}
	return count
}

package ops

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/filelock"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/testhelpers"
)

// dvFixture is an integration branch whose task merges carry mutation
// receipts, with a goal ledger the deferred-validation runner reads.
type dvFixture struct {
	t    *testing.T
	root string
	bb   *db.Blackboard
	base string
}

// waitForFile polls for a marker a shell command creates; the command blocks
// on a release file, so no timing assumption decides the outcome.
func waitForFile(t *testing.T, path string, limit time.Duration) {
	t.Helper()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.After(limit)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline:
			t.Fatalf("%s did not appear within %s", path, limit)
		}
	}
}

func requireShellRunner(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("deferred-validation fixtures use POSIX shell commands")
	}
}

func newDVFixture(t *testing.T, files map[string]string) *dvFixture {
	t.Helper()
	requireShellRunner(t)
	root := t.TempDir()
	testhelpers.SetupTestGitRepo(t, root)
	statePath, _ := testhelpers.SetupLizaDir(t, root)
	testhelpers.MustGit(t, root, "checkout", "integration")
	f := &dvFixture{t: t, root: root}
	files[".gitignore"] = ".liza/\n.deps/\n"
	f.write(files)
	testhelpers.MustGit(t, root, "add", "-A", ".")
	testhelpers.MustGit(t, root, "commit", "-m", "test: base")
	f.base = testhelpers.MustGit(t, root, "rev-parse", "HEAD")
	f.bb = testhelpers.WriteInitialState(t, statePath, testhelpers.CreateValidState())
	return f
}

func (f *dvFixture) write(files map[string]string) {
	f.t.Helper()
	for name, contents := range files {
		path := filepath.Join(f.root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			f.t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			f.t.Fatal(err)
		}
	}
}

func dvCommand(command string, timeout int) models.DeferredValidationCommand {
	return models.DeferredValidationCommand{
		Command: command, CommandSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(command))),
		TimeoutSeconds: timeout, Authority: models.AcceptanceDeferralContract,
	}
}

// merge commits files on integration as taskID's merge and records its
// mutation receipt, plus a deferral when commands are given.
func (f *dvFixture) merge(taskID string, files map[string]string, commands ...models.DeferredValidationCommand) string {
	f.t.Helper()
	before := testhelpers.MustGit(f.t, f.root, "rev-parse", "HEAD")
	f.write(files)
	testhelpers.MustGit(f.t, f.root, "add", "-A", ".")
	testhelpers.MustGit(f.t, f.root, "commit", "-m", "Merge "+taskID)
	after := testhelpers.MustGit(f.t, f.root, "rev-parse", "HEAD")
	f.modify(func(state *models.State) {
		if state.Goal.Integration == nil {
			state.Goal.Integration = &models.IntegrationLifecycle{}
		}
		lifecycle := state.Goal.Integration
		lifecycle.MutationReceipts = append(lifecycle.MutationReceipts, models.IntegrationMutationReceipt{TaskID: taskID, BeforeCommit: before, AfterCommit: after})
		if len(commands) > 0 {
			if lifecycle.DeferredValidation == nil {
				lifecycle.DeferredValidation = &models.DeferredValidationLedger{}
			}
			lifecycle.DeferredValidation.Deferrals = append(lifecycle.DeferredValidation.Deferrals, models.DeferredValidationDeferral{
				TaskID: taskID, MergeCommit: after, BeforeCommit: before, RecordedAt: time.Now().UTC(), Commands: commands,
			})
		}
	})
	return after
}

func (f *dvFixture) modify(fn func(*models.State)) {
	f.t.Helper()
	if err := f.bb.Modify(func(state *models.State) error { fn(state); return nil }); err != nil {
		f.t.Fatal(err)
	}
}

func (f *dvFixture) ledger() *models.DeferredValidationLedger {
	f.t.Helper()
	state, err := f.bb.Read()
	if err != nil {
		f.t.Fatal(err)
	}
	return deferredValidationLedger(state)
}

func (f *dvFixture) run(force bool) *RunDeferredValidationResult {
	f.t.Helper()
	result, err := RunDeferredValidation(context.Background(), f.root, RunDeferredValidationOptions{Force: force})
	if err != nil {
		f.t.Fatal(err)
	}
	return result
}

func onlyBatch(t *testing.T, result *RunDeferredValidationResult) models.DeferredValidationBatch {
	t.Helper()
	if result.Outcome != "ran" || len(result.Batches) != 1 {
		t.Fatalf("want exactly one batch, got %#v", result)
	}
	return result.Batches[0]
}

const dvCheckValue = `test "$(cat value.txt)" = ok`

func TestDeferredValidationBatchPassesAndCoversHEAD(t *testing.T) {
	t.Parallel()
	f := newDVFixture(t, map[string]string{"value.txt": "ok\n"})
	check := dvCommand(dvCheckValue, 60)
	head := f.merge("task-1", map[string]string{"a.txt": "a\n"}, check)

	batch := onlyBatch(t, f.run(false))
	if batch.Status != models.DeferredValidationBatchPassed || batch.Commit != head || len(batch.Results) != 1 || batch.Results[0].Status != models.DeferredValidationResultPassed {
		t.Fatalf("batch = %#v", batch)
	}
	state, _ := f.bb.Read()
	if waiting, blocked := deferredValidationGate(state, head); waiting != nil || blocked != nil {
		t.Fatalf("gate after passed batch: waiting=%v blocked=%v", waiting, blocked)
	}
	if again := f.run(false); again.Outcome != "skipped" || again.Reason != "covered" {
		t.Fatalf("covered HEAD must be a no-op, got %#v", again)
	}
	if f.ledger().Runner != nil {
		t.Fatal("runner record must be cleared after publication")
	}
}

func TestDeferredValidationBisectNamesCulpritAndRecordsDiscovery(t *testing.T) {
	t.Parallel()
	f := newDVFixture(t, map[string]string{"value.txt": "ok\n"})
	check := dvCommand(dvCheckValue, 60)
	f.merge("task-1", map[string]string{"a.txt": "a\n"}, check)
	culpritCommit := f.merge("task-2", map[string]string{"value.txt": "broken\n"})
	head := f.merge("task-3", map[string]string{"c.txt": "c\n"})

	batch := onlyBatch(t, f.run(false))
	if batch.Status != models.DeferredValidationBatchFailed || batch.Commit != head || len(batch.Culprits) != 1 {
		t.Fatalf("batch = %#v", batch)
	}
	culprit := batch.Culprits[0]
	if culprit.TaskID != "task-2" || culprit.MergeCommit != culpritCommit || culprit.Unattributed {
		t.Fatalf("culprit = %#v", culprit)
	}
	passed, failed := false, false
	for _, step := range culprit.Steps {
		passed = passed || step.Status == models.DeferredValidationResultPassed
		failed = failed || (step.Commit == culpritCommit && step.Status == models.DeferredValidationResultFailed)
	}
	if !passed || !failed {
		t.Fatalf("culprit lacks an executed passing predecessor and failing successor: %#v", culprit.Steps)
	}
	state, _ := f.bb.Read()
	if len(state.Discovered) != 1 || state.Discovered[0].Urgency != "immediate" || state.Discovered[0].During != "task-2" || !strings.Contains(state.Discovered[0].Recommendation, "task-2") {
		t.Fatalf("discovery = %#v", state.Discovered)
	}
	if _, blocked := deferredValidationGate(state, head); blocked == nil || blocked.Code != integrationProgressBlockedDeferredFailed || len(blocked.TaskIDs) != 1 || blocked.TaskIDs[0] != "task-2" {
		t.Fatalf("gate must block on the culprit, got %#v", blocked)
	}
	if again := f.run(false); again.Outcome != "skipped" || again.Reason != "failed_at_head" {
		t.Fatalf("a failed batch at an unchanged HEAD must not auto-retry, got %#v", again)
	}
	forced := onlyBatch(t, f.run(true))
	if !forced.Forced || forced.Status != models.DeferredValidationBatchFailed {
		t.Fatalf("forced retry = %#v", forced)
	}
	state, _ = f.bb.Read()
	if len(state.Discovered) != 1 {
		t.Fatalf("a repeated culprit must not record a second discovery: %#v", state.Discovered)
	}
}

func TestDeferredValidationBisectAttributesFirstDeferringMerge(t *testing.T) {
	t.Parallel()
	f := newDVFixture(t, map[string]string{"value.txt": "ok\n"})
	check := dvCommand(dvCheckValue, 60)
	culpritCommit := f.merge("task-1", map[string]string{"value.txt": "broken\n"}, check)
	f.merge("task-2", map[string]string{"b.txt": "b\n"})

	batch := onlyBatch(t, f.run(false))
	if len(batch.Culprits) != 1 || batch.Culprits[0].TaskID != "task-1" || batch.Culprits[0].MergeCommit != culpritCommit {
		t.Fatalf("baseline must be the first deferring merge's Before, got %#v", batch.Culprits)
	}
}

func TestDeferredValidationBisectUnattributedWhenBaselineFails(t *testing.T) {
	t.Parallel()
	f := newDVFixture(t, map[string]string{"value.txt": "broken\n"})
	check := dvCommand(dvCheckValue, 60)
	f.merge("task-1", map[string]string{"a.txt": "a\n"}, check)

	batch := onlyBatch(t, f.run(false))
	if len(batch.Culprits) != 1 || !batch.Culprits[0].Unattributed || batch.Culprits[0].Reason != "fails_at_baseline" {
		t.Fatalf("culprits = %#v", batch.Culprits)
	}
	state, _ := f.bb.Read()
	if len(state.Discovered) != 1 || state.Discovered[0].During != "unattributed" {
		t.Fatalf("discovery = %#v", state.Discovered)
	}
}

func TestDeferredValidationBisectOrdersByAncestryNotReceiptOrder(t *testing.T) {
	t.Parallel()
	f := newDVFixture(t, map[string]string{"value.txt": "ok\n"})
	check := dvCommand(dvCheckValue, 60)
	f.merge("task-1", map[string]string{"a.txt": "a\n"}, check)
	culpritCommit := f.merge("task-2", map[string]string{"value.txt": "broken\n"})
	f.merge("task-3", map[string]string{"c.txt": "c\n"})
	f.merge("task-4", map[string]string{"d.txt": "d\n"})
	// Receipts are persisted after the ref moves, outside the mutation lock;
	// shuffle their append order so only ancestry can order the search.
	f.modify(func(state *models.State) {
		receipts := state.Goal.Integration.MutationReceipts
		receipts[1], receipts[3] = receipts[3], receipts[1]
	})

	batch := onlyBatch(t, f.run(false))
	if len(batch.Culprits) != 1 || batch.Culprits[0].TaskID != "task-2" || batch.Culprits[0].MergeCommit != culpritCommit {
		t.Fatalf("culprits = %#v", batch.Culprits)
	}
}

func TestDeferredValidationProbeReprovisionsEachRevision(t *testing.T) {
	t.Parallel()
	// Setup installs the revision's declared dependency into an ignored
	// directory. The command passes only with the v1 dependency, so a probe
	// that reused HEAD's install would blame the wrong merge.
	f := newDVFixture(t, map[string]string{"deps.txt": "v1\n"})
	setup := "mkdir -p .deps && cp deps.txt .deps/installed"
	f.modify(func(state *models.State) { state.Config.PostWorktreeCmd = &setup })
	check := dvCommand(`test "$(cat .deps/installed)" = v1`, 60)
	f.merge("task-1", map[string]string{"a.txt": "a\n"}, check)
	culpritCommit := f.merge("task-2", map[string]string{"deps.txt": "v2\n"})
	f.merge("task-3", map[string]string{"c.txt": "c\n"})

	batch := onlyBatch(t, f.run(false))
	if len(batch.Culprits) != 1 || batch.Culprits[0].TaskID != "task-2" || batch.Culprits[0].MergeCommit != culpritCommit {
		t.Fatalf("culprits = %#v", batch.Culprits)
	}
}

func TestDeferredValidationContaminatingCommandIsNeverPassed(t *testing.T) {
	t.Parallel()
	f := newDVFixture(t, map[string]string{"value.txt": "ok\n"})
	dirty := dvCommand("echo changed >> value.txt", 60)
	check := dvCommand(dvCheckValue, 60)
	head := f.merge("task-1", map[string]string{"a.txt": "a\n"}, dirty, check)

	batch := onlyBatch(t, f.run(false))
	if batch.Status != models.DeferredValidationBatchInfraFailed || len(batch.Results) != 2 {
		t.Fatalf("batch = %#v", batch)
	}
	if batch.Results[0].Status != models.DeferredValidationResultContaminated || batch.Results[1].Status != models.DeferredValidationResultPassed {
		t.Fatalf("the next command must run on a re-prepared clean probe: %#v", batch.Results)
	}
	state, _ := f.bb.Read()
	if waiting, blocked := deferredValidationGate(state, head); waiting == nil || blocked != nil {
		t.Fatalf("an infra-failed batch must keep the gate waiting, got waiting=%v blocked=%v", waiting, blocked)
	}
}

func TestDeferredValidationSetupTimeoutIsInfraFailureAndReleasesLock(t *testing.T) {
	t.Parallel()
	f := newDVFixture(t, map[string]string{"value.txt": "ok\n"})
	setup := "sleep 30"
	f.modify(func(state *models.State) {
		state.Config.PostWorktreeCmd = &setup
		state.Config.DeferredValidation = &models.DeferredValidationConfig{SetupTimeoutSeconds: 1}
	})
	f.merge("task-1", map[string]string{"a.txt": "a\n"}, dvCommand(dvCheckValue, 60))

	batch := onlyBatch(t, f.run(false))
	if batch.Status != models.DeferredValidationBatchInfraFailed || !strings.Contains(batch.Reason, "setup") || batch.Results[0].Status != models.DeferredValidationResultNotRun {
		t.Fatalf("batch = %#v", batch)
	}
	held, acquired, err := filelock.New(filepath.Join(deferredValidationDir(f.root), "runner")).TryHold("test")
	if err != nil || !acquired {
		t.Fatalf("runner lock must be released after a setup timeout: acquired=%v err=%v", acquired, err)
	}
	_ = held.Release()
}

func TestDeferredValidationLiveRunnerMakesSecondInvocationNoOp(t *testing.T) {
	t.Parallel()
	f := newDVFixture(t, map[string]string{"value.txt": "ok\n"})
	f.merge("task-1", map[string]string{"a.txt": "a\n"}, dvCommand(dvCheckValue, 60))
	if err := os.MkdirAll(deferredValidationDir(f.root), 0o755); err != nil {
		t.Fatal(err)
	}
	held, acquired, err := filelock.New(filepath.Join(deferredValidationDir(f.root), "runner")).TryHold("live-runner")
	if err != nil || !acquired {
		t.Fatalf("take lock: %v %v", acquired, err)
	}
	defer func() { _ = held.Release() }()

	if result := f.run(false); result.Outcome != "skipped" || result.Reason != "runner_active" {
		t.Fatalf("result = %#v", result)
	}
	if _, err := os.Stat(filepath.Join(deferredValidationDir(f.root), "worktree")); !os.IsNotExist(err) {
		t.Fatal("a second runner must not touch the batch worktree")
	}
}

func TestDeferredValidationInheritedLockOutlivesDeadRunner(t *testing.T) {
	t.Parallel()
	requireShellRunner(t)
	dir := t.TempDir()
	lock := filelock.New(filepath.Join(dir, "runner"))
	held, acquired, err := lock.TryHold("old-runner")
	if err != nil || !acquired || held.File() == nil {
		t.Skip("lock descriptors do not pass to children on this platform")
	}
	release := filepath.Join(dir, "release")
	child, err := shellCommandContext(context.Background(), fmt.Sprintf("while [ ! -f %q ]; do sleep 0.05; done", release), dir)
	if err != nil {
		t.Fatal(err)
	}
	child.ExtraFiles = []*os.File{held.File()}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	// A dying runner's descriptor is closed without an explicit unlock; the
	// child's inherited descriptor must keep the successor out.
	if err := held.File().Close(); err != nil {
		t.Fatal(err)
	}
	if _, acquired, err := lock.TryHold("successor"); err != nil || acquired {
		t.Fatalf("successor acquired the lock while the old child lives: acquired=%v err=%v", acquired, err)
	}
	if err := os.WriteFile(release, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	successor, acquired, err := lock.TryHold("successor")
	if err != nil || !acquired {
		t.Fatalf("successor must acquire after the child exits: acquired=%v err=%v", acquired, err)
	}
	_ = successor.Release()
}

func TestDeferredValidationDeadPredecessorIsRecordedAndCleared(t *testing.T) {
	t.Parallel()
	f := newDVFixture(t, map[string]string{"value.txt": "ok\n"})
	head := f.merge("task-1", map[string]string{"a.txt": "a\n"}, dvCommand(dvCheckValue, 60))
	f.modify(func(state *models.State) {
		state.Goal.Integration.DeferredValidation.Runner = &models.DeferredValidationRunner{Token: "dead", PID: 1, Commit: head, StartedAt: time.Now().UTC()}
	})

	batch := onlyBatch(t, f.run(false))
	if batch.Status != models.DeferredValidationBatchPassed {
		t.Fatalf("batch = %#v", batch)
	}
	ledger := f.ledger()
	if len(ledger.Batches) != 2 || ledger.Batches[0].Reason != "runner_died" || ledger.Batches[0].Status != models.DeferredValidationBatchAborted || ledger.Runner != nil {
		t.Fatalf("ledger = %#v", ledger)
	}
}

func TestDeferredValidationUnfencedPredecessorRefusesUntilSettled(t *testing.T) {
	f := newDVFixture(t, map[string]string{"value.txt": "ok\n"})
	head := f.merge("task-1", map[string]string{"a.txt": "a\n"}, dvCommand(dvCheckValue, 60))
	f.modify(func(state *models.State) {
		state.Goal.Integration.DeferredValidation.Runner = &models.DeferredValidationRunner{Token: "unsettled", PID: 4242, Commit: head, StartedAt: time.Now().UTC()}
	})
	previous := deferredValidationLockFollowsChildren
	deferredValidationLockFollowsChildren = func(*filelock.Held) bool { return false }
	t.Cleanup(func() { deferredValidationLockFollowsChildren = previous })

	_, err := RunDeferredValidation(context.Background(), f.root, RunDeferredValidationOptions{})
	if err == nil || !strings.Contains(err.Error(), "settle") {
		t.Fatalf("want an unsettled-predecessor refusal, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(deferredValidationDir(f.root), "worktree")); !os.IsNotExist(statErr) {
		t.Fatal("refused recovery must not create, reset or remove the batch worktree")
	}
	if ledger := f.ledger(); len(ledger.Batches) != 0 || ledger.Runner == nil || ledger.Runner.Token != "unsettled" {
		t.Fatalf("refused recovery changed the ledger: %#v", ledger)
	}

	if _, err := SettleDeferredValidation(f.root, "operator confirmed pid 4242 is gone"); err != nil {
		t.Fatal(err)
	}
	batch := onlyBatch(t, f.run(false))
	if batch.Status != models.DeferredValidationBatchPassed {
		t.Fatalf("batch after settlement = %#v", batch)
	}
	if ledger := f.ledger(); ledger.Batches[0].Reason != "runner_settled_by_operator" {
		t.Fatalf("settlement not recorded: %#v", ledger.Batches)
	}
}

func TestDeferredValidationStaleTokenCannotPublish(t *testing.T) {
	t.Parallel()
	f := newDVFixture(t, map[string]string{"value.txt": "ok\n"})
	head := f.merge("task-1", map[string]string{"a.txt": "a\n"}, dvCommand(dvCheckValue, 60))
	f.modify(func(state *models.State) {
		state.Goal.Integration.DeferredValidation.Runner = &models.DeferredValidationRunner{Token: "successor", PID: 1, Commit: head, StartedAt: time.Now().UTC()}
	})
	r := &deferredValidationRunner{root: f.root, bb: f.bb, mask: func(s string) string { return s }}
	now := time.Now().UTC()
	if _, err := r.publish("stale", models.DeferredValidationBatch{Commit: head, StartedAt: now, FinishedAt: now, Status: models.DeferredValidationBatchPassed}); err == nil {
		t.Fatal("a stale token must not publish")
	}
	if ledger := f.ledger(); len(ledger.Batches) != 0 || ledger.Runner.Token != "successor" {
		t.Fatalf("stale publication changed the ledger: %#v", ledger)
	}
}

func TestDeferredValidationRefusesWhenHaltedOrStopped(t *testing.T) {
	t.Parallel()
	for _, mode := range []models.SystemMode{models.SystemModePaused, models.SystemModeStopped} {
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()
			f := newDVFixture(t, map[string]string{"value.txt": "ok\n"})
			f.merge("task-1", map[string]string{"a.txt": "a\n"}, dvCommand(dvCheckValue, 60))
			f.modify(func(state *models.State) { state.Config.Mode = mode })

			if result := f.run(false); result.Outcome != "skipped" || result.Reason != "halted" {
				t.Fatalf("result = %#v", result)
			}
			if _, err := os.Stat(filepath.Join(deferredValidationDir(f.root), "worktree")); !os.IsNotExist(err) {
				t.Fatal("a refused run must not touch the batch worktree")
			}
			if ledger := f.ledger(); len(ledger.Batches) != 0 || ledger.Runner != nil {
				t.Fatalf("a refused run changed the ledger: %#v", ledger)
			}
		})
	}
}

func TestDeferredValidationHaltDuringCommandStopsLaterStarts(t *testing.T) {
	t.Parallel()
	for _, mode := range []models.SystemMode{models.SystemModePaused, models.SystemModeStopped} {
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()
			f := newDVFixture(t, map[string]string{"value.txt": "ok\n"})
			signal := t.TempDir()
			started, release, second := filepath.Join(signal, "started"), filepath.Join(signal, "release"), filepath.Join(signal, "second")
			first := dvCommand(fmt.Sprintf("touch %q; while [ ! -f %q ]; do sleep 0.05; done", started, release), 60)
			next := dvCommand(fmt.Sprintf("touch %q", second), 60)
			f.merge("task-1", map[string]string{"a.txt": "a\n"}, first, next)

			done := make(chan *RunDeferredValidationResult, 1)
			go func() {
				result, _ := RunDeferredValidation(context.Background(), f.root, RunDeferredValidationOptions{})
				done <- result
			}()
			waitForFile(t, started, time.Minute)
			f.modify(func(state *models.State) { state.Config.Mode = mode })
			// The halt's barrier returns while the admitted command still runs:
			// the runner holds the admission lock only across start.
			if err := AwaitWorkAdmissionBarrier(f.root, "pause"); err != nil {
				t.Fatalf("halt barrier blocked on a running batch command: %v", err)
			}
			if err := os.WriteFile(release, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			result := <-done
			batch := onlyBatch(t, result)
			if batch.Status != models.DeferredValidationBatchAborted || batch.Reason != "halted" {
				t.Fatalf("batch = %#v", batch)
			}
			if _, err := os.Stat(second); !os.IsNotExist(err) {
				t.Fatal("no command may start after a committed halt or stop")
			}
		})
	}
}

func TestDeferredValidationDueAfterEveryNMergesWithoutNewDeferrals(t *testing.T) {
	t.Parallel()
	f := newDVFixture(t, map[string]string{"value.txt": "ok\n"})
	f.modify(func(state *models.State) {
		state.Config.DeferredValidation = &models.DeferredValidationConfig{BatchEveryMerges: 2}
		// Coding still in progress: the final integration gate does not
		// require a batch, so only periodic due-ness can start one.
		task := testhelpers.BuildTaskByStatus("task-pending", models.TaskStatusReady, time.Now().UTC())
		state.Tasks = append(state.Tasks, task)
		state.Sprint.Scope.Planned = append(state.Sprint.Scope.Planned, task.ID)
	})
	spawned := 0
	spawn := func(string) error { spawned++; return nil }
	f.merge("task-1", map[string]string{"a.txt": "a\n"}, dvCommand(dvCheckValue, 60))
	onlyBatch(t, f.run(false))

	for window := 1; window <= 3; window++ {
		f.merge(fmt.Sprintf("task-%d-a", window), map[string]string{fmt.Sprintf("w%d-a.txt", window): "x\n"})
		state, _ := f.bb.Read()
		head := testhelpers.MustGit(t, f.root, "rev-parse", "HEAD")
		if wanted, reason, err := deferredValidationWanted(f.root, state, head, false, false); err != nil || wanted {
			t.Fatalf("window %d: one merge must not be due (wanted=%v reason=%s err=%v)", window, wanted, reason, err)
		}
		f.merge(fmt.Sprintf("task-%d-b", window), map[string]string{fmt.Sprintf("w%d-b.txt", window): "x\n"})
		if started, err := startDeferredValidationIfDueWith(f.root, spawn); err != nil || !started {
			t.Fatalf("window %d: two merges with no new deferral must be due (started=%v err=%v)", window, started, err)
		}
		onlyBatch(t, f.run(false))
	}
	if spawned != 3 {
		t.Fatalf("spawned = %d, want 3", spawned)
	}
}

func TestDeferredValidationRunnerCatchesUpBehindHEAD(t *testing.T) {
	t.Parallel()
	f := newDVFixture(t, map[string]string{"value.txt": "ok\n"})
	f.modify(func(state *models.State) {
		state.Config.DeferredValidation = &models.DeferredValidationConfig{BatchEveryMerges: 1}
	})
	signal := t.TempDir()
	started, release := filepath.Join(signal, "started"), filepath.Join(signal, "release")
	// The first run's command blocks until the test has merged behind it.
	gate := dvCommand(fmt.Sprintf("test -f %q || { touch %q; while [ ! -f %q ]; do sleep 0.05; done; }", release, started, release), 60)
	first := f.merge("task-1", map[string]string{"a.txt": "a\n"}, gate)

	done := make(chan *RunDeferredValidationResult, 1)
	go func() {
		result, _ := RunDeferredValidation(context.Background(), f.root, RunDeferredValidationOptions{})
		done <- result
	}()
	waitForFile(t, started, time.Minute)
	second := f.merge("task-2", map[string]string{"b.txt": "b\n"})
	if err := os.WriteFile(release, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	result := <-done
	if len(result.Batches) != 2 || result.Batches[0].Commit != first || result.Batches[1].Commit != second {
		t.Fatalf("runner must catch up to the HEAD that moved during its batch: %#v", result)
	}
}

func TestDeferredValidationTriggerRespectsFailedHEADAndLiveRunner(t *testing.T) {
	t.Parallel()
	f := newDVFixture(t, map[string]string{"value.txt": "broken\n"})
	f.modify(func(state *models.State) {
		state.Config.DeferredValidation = &models.DeferredValidationConfig{BatchEveryMerges: 1}
	})
	f.merge("task-1", map[string]string{"a.txt": "a\n"}, dvCommand(dvCheckValue, 60))
	spawn := func(string) error { return nil }
	if started, err := startDeferredValidationIfDueWith(f.root, spawn); err != nil || !started {
		t.Fatalf("first trigger: started=%v err=%v", started, err)
	}
	onlyBatch(t, f.run(false))
	if started, err := startDeferredValidationIfDueWith(f.root, spawn); err != nil || started {
		t.Fatalf("a failed batch at HEAD must not respawn: started=%v err=%v", started, err)
	}
	f.merge("task-2", map[string]string{"value.txt": "ok\n"})
	if err := os.MkdirAll(deferredValidationDir(f.root), 0o755); err != nil {
		t.Fatal(err)
	}
	held, acquired, err := filelock.New(filepath.Join(deferredValidationDir(f.root), "runner")).TryHold("live")
	if err != nil || !acquired {
		t.Fatal(err)
	}
	if started, err := startDeferredValidationIfDueWith(f.root, spawn); err != nil || started {
		t.Fatalf("a live runner must suppress spawning: started=%v err=%v", started, err)
	}
	_ = held.Release()
	if started, err := startDeferredValidationIfDueWith(f.root, spawn); err != nil || !started {
		t.Fatalf("a correction merge re-arms the trigger: started=%v err=%v", started, err)
	}
}

func TestDeferredValidationRetirementLifecycle(t *testing.T) {
	t.Parallel()
	f := newDVFixture(t, map[string]string{"old.txt": "x\n"})
	oldCheck := dvCommand("test -f old.txt", 60)
	f.merge("task-1", map[string]string{"a.txt": "a\n"}, oldCheck)
	if err := os.Remove(filepath.Join(f.root, "old.txt")); err != nil {
		t.Fatal(err)
	}
	head := f.merge("task-2", map[string]string{"new.txt": "x\n"})

	batch := onlyBatch(t, f.run(false))
	if len(batch.Culprits) != 1 || batch.Culprits[0].TaskID != "task-2" {
		t.Fatalf("rename culprit = %#v", batch.Culprits)
	}
	if _, err := RetireDeferredValidation(f.root, oldCheck.Command, "D-97 ruling: test renamed by task-2"); err != nil {
		t.Fatal(err)
	}
	state, _ := f.bb.Read()
	if waiting, blocked := deferredValidationGate(state, head); waiting != nil || blocked != nil {
		t.Fatalf("a retired command must leave the gate: waiting=%v blocked=%v", waiting, blocked)
	}
	newCheck := dvCommand("test -f new.txt", 60)
	f.merge("task-3", map[string]string{"b.txt": "b\n"}, newCheck)
	effective := f.ledger().EffectiveCommands()
	if len(effective) != 1 || effective[0].CommandSHA256 != newCheck.CommandSHA256 {
		t.Fatalf("effective = %#v", effective)
	}
	f.merge("task-4", map[string]string{"c.txt": "c\n"}, oldCheck)
	if effective := f.ledger().EffectiveCommands(); len(effective) != 2 {
		t.Fatalf("a later deferral re-enters a retired command: %#v", effective)
	}
	if _, err := RetireDeferredValidation(f.root, "not-deferred", "ruling"); err == nil {
		t.Fatal("retiring a command outside the effective set must be refused")
	}
}

func TestDeferredValidationGateWaitsAndBlocksOnAttempts(t *testing.T) {
	t.Parallel()
	check := dvCommand("true", 60)
	head := strings.Repeat("a", 40)
	state := testhelpers.CreateValidState()
	if waiting, blocked := deferredValidationGate(state, head); waiting != nil || blocked != nil {
		t.Fatal("no deferrals must not gate")
	}
	ledger := &models.DeferredValidationLedger{Deferrals: []models.DeferredValidationDeferral{{TaskID: "t", MergeCommit: head, Commands: []models.DeferredValidationCommand{check}}}}
	state.Goal.Integration = &models.IntegrationLifecycle{DeferredValidation: ledger}
	if waiting, _ := deferredValidationGate(state, head); waiting == nil || waiting.Code != IntegrationProgressWaitingDeferredValidation {
		t.Fatalf("missing batch must wait, got %v", waiting)
	}
	for i := 1; i <= models.DeferredValidationMaxAttemptsPerCommit; i++ {
		ledger.Batches = append(ledger.Batches, models.DeferredValidationBatch{ID: i, Commit: head, Status: models.DeferredValidationBatchAborted})
	}
	if _, blocked := deferredValidationGate(state, head); blocked == nil || blocked.Code != integrationProgressBlockedDeferredInfra {
		t.Fatalf("exhausted attempts must block, got %v", blocked)
	}
	ledger.Batches = append(ledger.Batches, models.DeferredValidationBatch{ID: 9, Commit: head, Status: models.DeferredValidationBatchPassed, Results: []models.DeferredValidationResult{{CommandSHA256: check.CommandSHA256, Status: models.DeferredValidationResultPassed}}})
	if waiting, blocked := deferredValidationGate(state, head); waiting != nil || blocked != nil {
		t.Fatal("a passed covering batch must open the gate")
	}
}

func TestRecordMergedDeferralsUsesExactCommandAndMergeBase(t *testing.T) {
	t.Parallel()
	before, after := strings.Repeat("1", 40), strings.Repeat("2", 40)
	state := testhelpers.CreateValidState()
	state.Goal.Integration = &models.IntegrationLifecycle{MutationReceipts: []models.IntegrationMutationReceipt{{TaskID: "task-1", BeforeCommit: before, AfterCommit: after}}}
	secretCommand := "SECRET_TOKEN=hunter2 run-suite"
	task := &models.Task{ID: "task-1", Validation: []string{"unit", secretCommand}, AcceptanceReceipt: &models.AcceptanceReceipt{
		Deferred: []models.AcceptanceDeferredCommand{{Index: 1, Command: "***", CommandSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(secretCommand))), TimeoutSeconds: 90, Authority: models.AcceptanceDeferralOperator}},
	}}
	recordMergedDeferrals(state, task, after, time.Now().UTC())
	deferrals := state.Goal.Integration.DeferredValidation.Deferrals
	if len(deferrals) != 1 || deferrals[0].BeforeCommit != before || deferrals[0].Commands[0].Command != secretCommand || deferrals[0].Commands[0].TimeoutSeconds != 90 {
		t.Fatalf("deferrals = %#v", deferrals)
	}
	recordMergedDeferrals(state, &models.Task{ID: "task-2"}, after, time.Now().UTC())
	if len(state.Goal.Integration.DeferredValidation.Deferrals) != 1 {
		t.Fatal("a task without deferrals must not be recorded")
	}
}

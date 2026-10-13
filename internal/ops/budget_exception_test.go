package ops

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/referencecontract"
	"github.com/liza-mas/liza/internal/testhelpers"
)

func validBudgetException() *BudgetException {
	return &BudgetException{TimeoutSeconds: 3600, Ruling: "D122"}
}

func TestBudgetExceptionValidateBounds(t *testing.T) {
	t.Parallel()
	for _, valid := range []BudgetException{{1, "D1"}, {referencecontract.AcceptanceMaxTimeoutSeconds, "D122"}} {
		if err := valid.Validate(); err != nil {
			t.Errorf("%+v refused: %v", valid, err)
		}
	}
	for name, invalid := range map[string]BudgetException{
		"zero":          {0, "D122"},
		"negative":      {-1, "D122"},
		"above cap":     {referencecontract.AcceptanceMaxTimeoutSeconds + 1, "D122"},
		"blank ruling":  {3600, "  "},
		"empty ruling":  {3600, ""},
		"padded ruling": {3600, " D122"},
		"multiline":     {3600, "D122\nD123"},
		"oversize":      {3600, strings.Repeat("x", MaxBudgetRulingBytes+1)},
		"invalid UTF-8": {3600, "D\xff"},
	} {
		if err := invalid.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestAdmissionTimeoutSecondsRaisesButNeverLowers(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	unblocked := func(extra any) models.TaskHistoryEntry {
		return models.TaskHistoryEntry{Time: now, Event: models.TaskEventUnblocked, Extra: map[string]any{historyExtraBudgetException: extra}}
	}
	history := []models.TaskHistoryEntry{
		unblocked(BudgetException{3600, "D117"}.historyValue()),
		unblocked(BudgetException{2400, "D122"}.historyValue()),
		// Not an unblock, invalid value and above-cap value are all ignored.
		{Time: now, Event: models.TaskEventBlocked, Extra: map[string]any{historyExtraBudgetException: BudgetException{7200, "D1"}.historyValue()}},
		unblocked(map[string]any{"timeout_seconds": 7200}),
		unblocked(map[string]any{"timeout_seconds": referencecontract.AcceptanceMaxTimeoutSeconds + 1, "ruling": "D2"}),
	}
	if got := RecordedBudgetExceptions(history); len(got) != 2 || got[0].Ruling != "D117" || got[1].Ruling != "D122" {
		t.Fatalf("recorded = %+v", got)
	}
	for reviewed, want := range map[int]int{1800: 3600, 3600: 3600, 5400: 5400} {
		if got := admissionTimeoutSeconds(reviewed, history); got != want {
			t.Errorf("admissionTimeoutSeconds(%d) = %d, want %d", reviewed, got, want)
		}
	}
	if got := admissionTimeoutSeconds(1800, nil); got != 1800 {
		t.Errorf("no exception: %d, want the reviewed 1800", got)
	}
}

func setupBlockedTask(t *testing.T, rolePair string) (root, stateFile string) {
	t.Helper()
	root = t.TempDir()
	testhelpers.SetupTestGitRepo(t, root)
	testhelpers.SetupPipelineConfig(t, root)
	stateFile, _ = testhelpers.SetupLizaDir(t, root)
	state := testhelpers.CreateValidState()
	task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusBlocked, time.Now().UTC())
	if rolePair != "" {
		task.RolePair = rolePair
	}
	task.Worktree, task.BaseCommit = nil, nil
	state.Tasks = []models.Task{task}
	testhelpers.WriteInitialState(t, stateFile, state)
	return root, stateFile
}

func TestUnblockTaskRecordsBudgetExceptionWithScopeException(t *testing.T) {
	t.Parallel()
	root, stateFile := setupBlockedTask(t, "")

	opts := UnblockTaskOptions{BudgetException: validBudgetException(), ScopeException: validScopeException()}
	if _, err := UnblockTaskWithOptions(root, "task-1", "human ruled budget", "orchestrator-1", opts); err != nil {
		t.Fatalf("unblock with budget and scope exceptions: %v", err)
	}
	readState, err := db.New(stateFile).Read()
	if err != nil {
		t.Fatal(err)
	}
	history := readState.FindTask("task-1").History
	if got := RecordedBudgetExceptions(history); len(got) != 1 || got[0] != *validBudgetException() {
		t.Fatalf("recorded budget exceptions after YAML round trip = %+v", got)
	}
	if got := RecordedScopeExceptions(history); len(got) != 1 {
		t.Fatalf("recorded scope exceptions = %+v", got)
	}
}

func TestUnblockTaskOperatorRecordsBudgetException(t *testing.T) {
	t.Parallel()
	root, stateFile := setupBlockedTask(t, "")

	if _, err := UnblockTaskAsOperator(root, "task-1", "human ruling", "operator", UnblockTaskOptions{BudgetException: validBudgetException()}); err != nil {
		t.Fatalf("operator unblock with budget exception: %v", err)
	}
	readState, err := db.New(stateFile).Read()
	if err != nil {
		t.Fatal(err)
	}
	if got := RecordedBudgetExceptions(readState.FindTask("task-1").History); len(got) != 1 {
		t.Fatalf("operator budget exception = %+v", got)
	}
}

func TestUnblockTaskWithoutBudgetExceptionRecordsNone(t *testing.T) {
	t.Parallel()
	root, stateFile := setupBlockedTask(t, "")

	if _, err := UnblockTaskWithOptions(root, "task-1", "inputs corrected", "orchestrator-1", UnblockTaskOptions{}); err != nil {
		t.Fatal(err)
	}
	readState, err := db.New(stateFile).Read()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range readState.FindTask("task-1").History {
		if _, ok := entry.Extra[historyExtraBudgetException]; ok {
			t.Fatalf("plain unblock recorded a budget exception: %+v", entry)
		}
	}
}

func TestUnblockTaskRefusesInvalidOrNonCodingBudgetException(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		rolePair  string
		exception *BudgetException
		want      string
	}{
		"non-coding": {"code-planning-pair", validBudgetException(), "coding tasks only"},
		"above cap":  {"", &BudgetException{referencecontract.AcceptanceMaxTimeoutSeconds + 1, "D122"}, "timeout_seconds must be"},
		"no ruling":  {"", &BudgetException{3600, ""}, "ruling"},
	} {
		root, stateFile := setupBlockedTask(t, tc.rolePair)
		before := ownershipStateBytes(t, stateFile)

		_, err := UnblockTaskWithOptions(root, "task-1", "budget", "orchestrator-1", UnblockTaskOptions{BudgetException: tc.exception})
		var invalid *PreconditionError
		if !errors.As(err, &invalid) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %v, want precondition containing %q", name, err, tc.want)
		}
		if !bytes.Equal(before, ownershipStateBytes(t, stateFile)) {
			t.Errorf("%s: refused budget exception changed state", name)
		}
	}
}

func TestUnblockTaskBudgetExceptionReplayAndIdentity(t *testing.T) {
	t.Parallel()
	root, _, bb, _ := setupOwnershipLifecycleClaim(t)
	testhelpers.SetupPipelineConfig(t, root)
	authority := models.AgentAuthority{ID: "orchestrator-1", Generation: "unblock-current"}
	if err := bb.Modify(func(state *models.State) error {
		task := state.FindTask("task-1")
		*task = testhelpers.BuildTaskByStatus(task.ID, models.TaskStatusBlocked, task.Created)
		task.Worktree = nil
		agent := testhelpers.RegisteredTestAgent("orchestrator")
		agent.Generation = authority.Generation
		state.Agents[authority.ID] = agent
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	opts := UnblockTaskOptions{Request: ownershipRequestOptions(t, bb, "budget-exception-unblock"), BudgetException: validBudgetException()}
	if result, err := UnblockTaskWithAuthority(root, "task-1", "budget", authority, opts); err != nil || result.Outcome != models.LifecycleCompleted {
		t.Fatalf("unblock: %+v, %v", result, err)
	}
	replayed, err := UnblockTaskWithAuthority(root, "task-1", "budget", authority, opts)
	if err != nil || replayed.Outcome != models.LifecycleAlreadyCompleted {
		t.Fatalf("exact replay: %+v, %v", replayed, err)
	}
	changed := opts
	changed.BudgetException = &BudgetException{TimeoutSeconds: 7200, Ruling: "D122"}
	if _, err := UnblockTaskWithAuthority(root, "task-1", "budget", authority, changed); err == nil {
		t.Fatal("same request ID with a different budget exception was accepted")
	}
	task, err := bb.GetTask("task-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := RecordedBudgetExceptions(task.History); len(got) != 1 {
		t.Fatalf("replay recorded %d budget exceptions, want 1", len(got))
	}
}

// D-145: a recorded budget exception turns a canonical admission timeout into
// a pass on resubmission, and the in-flight marker the doer session honours
// carries the raised budget.
func TestSubmitForReviewAdmissionUsesRecordedBudgetException(t *testing.T) {
	t.Parallel()
	requirePosixShell(t)
	// GIVEN a reviewed 1 s budget and a canonical command that needs 2 s
	scenario := testhelpers.SetupAcceptanceSubmitScenario(t, "sleep 2\n", 1)
	_, err := SubmitForReviewWithAuthority(scenario.Root, scenario.TaskID, scenario.Commit, scenario.Authority)
	if err == nil || !strings.Contains(err.Error(), "command batch timed out") {
		t.Fatalf("reviewed budget: error = %v, want a batch timeout", err)
	}

	// WHEN a human-ruled 120 s budget exception is recorded
	const raised = 120
	if err := scenario.BB.Modify(func(state *models.State) error {
		task := state.FindTask(scenario.TaskID)
		task.History = append(task.History, models.TaskHistoryEntry{
			Time: time.Now().UTC(), Event: models.TaskEventUnblocked, Agent: testhelpers.StringPtr("operator"),
			Extra: map[string]any{historyExtraBudgetException: BudgetException{raised, "D122"}.historyValue()},
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var (
		mu            sync.Mutex
		markerHorizon time.Duration
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		timeout := time.After(10 * time.Second)
		for {
			observed := time.Now()
			if until, ok := InflightSubmitDeadline(scenario.Root, scenario.Authority, scenario.TaskID, observed); ok {
				mu.Lock()
				markerHorizon = until.Sub(observed)
				mu.Unlock()
				return
			}
			select {
			case <-ticker.C:
			case <-timeout:
				return
			}
		}
	}()
	result, err := SubmitForReviewWithAuthority(scenario.Root, scenario.TaskID, scenario.Commit, scenario.Authority)
	<-done

	// THEN admission passes within the raised budget and the marker carries it
	if err != nil {
		t.Fatalf("raised budget: submit failed: %v", err)
	}
	if result == nil {
		t.Fatal("raised budget: nil result")
	}
	mu.Lock()
	horizon := markerHorizon
	mu.Unlock()
	if floor := (raised-5)*time.Second + submitGraceMargin; horizon < floor {
		t.Fatalf("in-flight marker horizon = %v, want at least %v (raised budget plus grace)", horizon, floor)
	}
	task, err := scenario.BB.GetTask(scenario.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.AcceptanceReceipt == nil || len(task.AcceptanceReceipt.Commands) != 1 || task.AcceptanceReceipt.Commands[0].ExitCode != 0 {
		t.Fatalf("receipt = %+v, want one successful canonical execution", task.AcceptanceReceipt)
	}
}

package ops

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/testhelpers"
)

func validScopeException() *ScopeException {
	return &ScopeException{
		Files:     []string{"tests/support/conftest.py", "tests/unit/test_seed.py"},
		Change:    "add a read-only lock observer fixture",
		Unchanged: "production SQL, the provider contract and acceptance",
	}
}

func TestParseScopeExceptionRefusesUnboundedInput(t *testing.T) {
	t.Parallel()
	if _, err := ParseScopeException([]byte(`{"files":["a.py"],"change":"c","unchanged":"u"}`)); err != nil {
		t.Fatalf("valid exception refused: %v", err)
	}
	nine := `"f1","f2","f3","f4","f5","f6","f7","f8","f9"`
	for name, input := range map[string]string{
		"unknown field":   `{"files":["a.py"],"change":"c","unchanged":"u","scope":"all"}`,
		"no files":        `{"files":[],"change":"c","unchanged":"u"}`,
		"too many files":  `{"files":[` + nine + `],"change":"c","unchanged":"u"}`,
		"duplicate file":  `{"files":["a.py","a.py"],"change":"c","unchanged":"u"}`,
		"absolute file":   `{"files":["/etc/passwd"],"change":"c","unchanged":"u"}`,
		"parent escape":   `{"files":["../other/a.py"],"change":"c","unchanged":"u"}`,
		"unclean path":    `{"files":["tests/./a.py"],"change":"c","unchanged":"u"}`,
		"blank change":    `{"files":["a.py"],"change":"  ","unchanged":"u"}`,
		"blank unchanged": `{"files":["a.py"],"change":"c","unchanged":""}`,
		"two objects":     `{"files":["a.py"],"change":"c","unchanged":"u"}{}`,
		"oversize":        `{"files":["a.py"],"change":"` + strings.Repeat("x", MaxScopeExceptionBytes) + `","unchanged":"u"}`,
	} {
		if _, err := ParseScopeException([]byte(input)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestUnblockTaskRecordsScopeExceptionOnCodingTask(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	testhelpers.SetupPipelineConfig(t, tmpDir)
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)
	state := testhelpers.CreateValidState()
	task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusBlocked, time.Now().UTC())
	task.Worktree, task.BaseCommit = nil, nil
	state.Tasks = []models.Task{task}
	testhelpers.WriteInitialState(t, stateFile, state)

	exception := validScopeException()
	if _, err := UnblockTaskWithOptions(tmpDir, "task-1", "in-place fix", "orchestrator-1", UnblockTaskOptions{ScopeException: exception}); err != nil {
		t.Fatalf("unblock with scope exception: %v", err)
	}
	readState, err := db.New(stateFile).Read()
	if err != nil {
		t.Fatal(err)
	}
	recorded := RecordedScopeExceptions(readState.FindTask("task-1").History)
	if len(recorded) != 1 || recorded[0].Agent != "orchestrator-1" ||
		strings.Join(recorded[0].Files, ",") != strings.Join(exception.Files, ",") ||
		recorded[0].Change != exception.Change || recorded[0].Unchanged != exception.Unchanged {
		t.Fatalf("recorded exceptions after YAML round trip = %+v", recorded)
	}
}

func TestUnblockTaskOperatorRecordsScopeException(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	testhelpers.SetupPipelineConfig(t, tmpDir)
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)
	state := testhelpers.CreateValidState()
	task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusBlocked, time.Now().UTC())
	task.Worktree, task.BaseCommit = nil, nil
	state.Tasks = []models.Task{task}
	testhelpers.WriteInitialState(t, stateFile, state)

	if _, err := UnblockTaskAsOperator(tmpDir, "task-1", "human ruling", "operator", UnblockTaskOptions{ScopeException: validScopeException()}); err != nil {
		t.Fatalf("operator unblock with scope exception: %v", err)
	}
	readState, err := db.New(stateFile).Read()
	if err != nil {
		t.Fatal(err)
	}
	if recorded := RecordedScopeExceptions(readState.FindTask("task-1").History); len(recorded) != 1 || recorded[0].Agent != "operator" {
		t.Fatalf("operator exception = %+v", recorded)
	}
}

func TestUnblockTaskRefusesScopeExceptionOnNonCodingTask(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	testhelpers.SetupTestGitRepo(t, tmpDir)
	testhelpers.SetupPipelineConfig(t, tmpDir)
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)
	state := testhelpers.CreateValidState()
	task := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusBlocked, time.Now().UTC())
	task.RolePair = "code-planning-pair"
	task.Worktree, task.BaseCommit = nil, nil
	state.Tasks = []models.Task{task}
	testhelpers.WriteInitialState(t, stateFile, state)
	before := ownershipStateBytes(t, stateFile)

	_, err := UnblockTaskWithOptions(tmpDir, "task-1", "in-place fix", "orchestrator-1", UnblockTaskOptions{ScopeException: validScopeException()})
	var invalid *PreconditionError
	if !errors.As(err, &invalid) || !strings.Contains(err.Error(), "coding tasks only") {
		t.Fatalf("non-coding scope exception error = %v", err)
	}
	if !bytes.Equal(before, ownershipStateBytes(t, stateFile)) {
		t.Fatal("refused scope exception changed state")
	}
}

func TestUnblockTaskScopeExceptionReplayAndIdentity(t *testing.T) {
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
	opts := UnblockTaskOptions{Request: ownershipRequestOptions(t, bb, "scope-exception-unblock"), ScopeException: validScopeException()}
	if result, err := UnblockTaskWithAuthority(root, "task-1", "in-place fix", authority, opts); err != nil || result.Outcome != models.LifecycleCompleted {
		t.Fatalf("unblock: %+v, %v", result, err)
	}
	replayed, err := UnblockTaskWithAuthority(root, "task-1", "in-place fix", authority, opts)
	if err != nil || replayed.Outcome != models.LifecycleAlreadyCompleted {
		t.Fatalf("exact replay: %+v, %v", replayed, err)
	}
	changed := opts
	changed.ScopeException = validScopeException()
	changed.ScopeException.Change = "a different change"
	if _, err := UnblockTaskWithAuthority(root, "task-1", "in-place fix", authority, changed); err == nil {
		t.Fatal("same request ID with a different scope exception was accepted")
	}
	task, err := bb.GetTask("task-1")
	if err != nil {
		t.Fatal(err)
	}
	if recorded := RecordedScopeExceptions(task.History); len(recorded) != 1 {
		t.Fatalf("replay recorded %d exceptions, want 1", len(recorded))
	}
}

func TestRecordedScopeExceptionsAccumulateAcrossUnblocks(t *testing.T) {
	t.Parallel()
	first, second := validScopeException(), validScopeException()
	second.Files = []string{"tests/fixtures/seed.py"}
	orchestrator, operator := "orchestrator-1", "operator"
	now := time.Now().UTC()
	history := []models.TaskHistoryEntry{
		{Time: now, Event: models.TaskEventUnblocked, Agent: &orchestrator, Extra: map[string]any{historyExtraScopeException: first.historyValue()}},
		{Time: now.Add(time.Minute), Event: models.TaskEventBlocked},
		{Time: now.Add(2 * time.Minute), Event: models.TaskEventUnblocked, Agent: &operator},
		{Time: now.Add(3 * time.Minute), Event: models.TaskEventUnblocked, Agent: &operator, Extra: map[string]any{historyExtraScopeException: map[string]any{
			"files": []any{"tests/fixtures/seed.py"}, "change": second.Change, "unchanged": second.Unchanged,
		}}},
		{Time: now.Add(4 * time.Minute), Event: models.TaskEventUnblocked, Extra: map[string]any{historyExtraScopeException: map[string]any{"files": []any{}}}},
	}
	recorded := RecordedScopeExceptions(history)
	if len(recorded) != 2 || recorded[0].Agent != orchestrator || recorded[1].Files[0] != "tests/fixtures/seed.py" {
		t.Fatalf("accumulated exceptions = %+v", recorded)
	}
}

package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/brand"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/testhelpers"
)

// setupOperatorProject runs the command as an operator shell: no agent identity.
// The test generation executeRootCommand exports stays set, so a recorded
// operator actor proves the operator path ran rather than the orchestrator's.
func setupOperatorProject(t *testing.T, tasks ...models.Task) (string, string) {
	t.Helper()
	t.Setenv(brand.EnvName("AGENT_ID"), "")
	t.Setenv(brand.LegacyEnvName("AGENT_ID"), "")
	return setupMutationTestProject(t, func(state *models.State) {
		state.Tasks = tasks
	})
}

func lastHistoryAgent(t *testing.T, task *models.Task) string {
	t.Helper()
	if len(task.History) == 0 || task.History[len(task.History)-1].Agent == nil {
		t.Fatalf("task %s has no attributed history: %+v", task.ID, task.History)
	}
	return *task.History[len(task.History)-1].Agent
}

func TestOperatorCancelTaskRecordsHumanActor(t *testing.T) {
	// GIVEN a blocked task and an operator shell
	now := time.Now().UTC()
	root, statePath := setupOperatorProject(t, testhelpers.BuildTaskByStatus("task-op-cancel", models.TaskStatusBlocked, now))

	// WHEN the operator cancels it without --agent-id
	if err := executeRootCommand(t, root, "cancel-task", "task-op-cancel", "requirements withdrawn"); err != nil {
		t.Fatalf("operator cancel-task failed: %v", err)
	}

	// THEN the task is ABANDONED and attributed to the default operator actor
	task := mustFindTask(t, readState(t, statePath), "task-op-cancel")
	if task.Status != models.TaskStatusAbandoned {
		t.Fatalf("status = %s, want ABANDONED", task.Status)
	}
	if agent := lastHistoryAgent(t, task); agent != "human" {
		t.Fatalf("history actor = %q, want human", agent)
	}
}

func TestOperatorUnblockTaskRecordsChangedBy(t *testing.T) {
	// GIVEN a repaired blocked task with no worktree
	now := time.Now().UTC()
	blocked := testhelpers.BuildTaskByStatus("task-op-unblock", models.TaskStatusBlocked, now)
	blocked.RolePair = "code-planning-pair"
	blocked.Worktree = nil
	blocked.BaseCommit = nil
	root, statePath := setupOperatorProject(t, blocked)

	// WHEN the operator unblocks it under its own audit name
	if err := executeRootCommand(t, root, "unblock-task", "task-op-unblock", "--reason", "blocker cleared by human", "--changed-by", "run-operator"); err != nil {
		t.Fatalf("operator unblock-task failed: %v", err)
	}

	// THEN it returns to its role-pair initial status, attributed to that name
	task := mustFindTask(t, readState(t, statePath), "task-op-unblock")
	if task.Status != models.TaskStatusDraftCodingPlan {
		t.Fatalf("status = %s, want %s", task.Status, models.TaskStatusDraftCodingPlan)
	}
	if agent := lastHistoryAgent(t, task); agent != "run-operator" {
		t.Fatalf("history actor = %q, want run-operator", agent)
	}
}

func TestOperatorUnblockTaskKeepsStatusGate(t *testing.T) {
	// GIVEN a task that is not BLOCKED
	now := time.Now().UTC()
	root, statePath := setupOperatorProject(t, testhelpers.BuildTaskByStatus("task-op-ready", models.TaskStatusDraftCodingPlan, now))
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}

	// WHEN the operator tries to unblock it
	err = executeRootCommand(t, root, "unblock-task", "task-op-ready", "--reason", "nothing to unblock")

	// THEN the ordinary status gate refuses and state is untouched
	if err == nil || !strings.Contains(err.Error(), "task must be BLOCKED to unblock") {
		t.Fatalf("err = %v, want BLOCKED status refusal", err)
	}
	after, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("refused operator unblock changed state")
	}
}

func TestAgentIdentityDisablesOperatorPath(t *testing.T) {
	// GIVEN a coder session identity in the environment
	now := time.Now().UTC()
	root, statePath := setupOperatorProject(t, testhelpers.BuildTaskByStatus("task-op-agent", models.TaskStatusBlocked, now))
	t.Setenv(brand.EnvName("AGENT_ID"), "coder-1")

	// WHEN it calls cancel-task
	err := executeRootCommand(t, root, "cancel-task", "task-op-agent", "not mine to cancel")

	// THEN role permission still refuses it and the task stays BLOCKED
	if err == nil || !strings.Contains(err.Error(), `operation "cancel-task" not allowed for role "coder"`) {
		t.Fatalf("err = %v, want coder RBAC refusal", err)
	}
	if task := mustFindTask(t, readState(t, statePath), "task-op-agent"); task.Status != models.TaskStatusBlocked {
		t.Fatalf("status = %s, want BLOCKED", task.Status)
	}
}

func TestOperatorRetargetDependencyRecordsHumanActor(t *testing.T) {
	// GIVEN a blocked consumer depending on a stale edge
	now := time.Now().UTC()
	consumer := testhelpers.BuildTaskByStatus("task-op-consumer", models.TaskStatusBlocked, now)
	consumer.DependsOn = []string{"old-dep"}
	root, statePath := setupOperatorProject(t, consumer, testhelpers.BuildTaskByStatus("new-dep", models.TaskStatusMerged, now))

	// WHEN the operator retargets the edge
	if err := executeRootCommand(t, root, "retarget-dependency", "task-op-consumer", "old-dep", "new-dep", "--reason", "diagnosed stale edge"); err != nil {
		t.Fatalf("operator retarget-dependency failed: %v", err)
	}

	// THEN the edge moves and the rewrite is attributed to the operator
	task := mustFindTask(t, readState(t, statePath), "task-op-consumer")
	if len(task.DependsOn) != 1 || task.DependsOn[0] != "new-dep" {
		t.Fatalf("depends_on = %v, want [new-dep]", task.DependsOn)
	}
	if agent := lastHistoryAgent(t, task); agent != "human" {
		t.Fatalf("history actor = %q, want human", agent)
	}
}

// operatorJSONResult runs a command as the operator with --json and returns the
// success envelope's result object.
func operatorJSONResult(t *testing.T, root string, args ...string) map[string]any {
	t.Helper()
	stdout, err := executeRootCommandCapture(t, root, append(args, "--json")...)
	if err != nil {
		t.Fatalf("%v: %v\n%s", args, err, stdout)
	}
	env := parseEnvelope(t, stdout)
	result, ok := env["result"].(map[string]any)
	if env["ok"] != true || !ok {
		t.Fatalf("envelope = %v, want ok with result object", env)
	}
	return result
}

func TestOperatorApplyDependencyRepairJSON(t *testing.T) {
	// GIVEN a blocked task carrying a stored dependency repair request
	now := time.Now().UTC()
	source := testhelpers.BuildTaskByStatus("task-op-source", models.TaskStatusBlocked, now)
	source.DependsOn = []string{"old-dep"}
	source.RepairRequest = &models.RepairRequest{
		Operation:         models.RepairOperationApplyDependencyRepair,
		Target:            "task-op-source",
		DependencyUpdates: []models.DependencyUpdate{{TaskID: "task-op-source", ExpectedDependsOn: []string{"old-dep"}, DesiredDependsOn: []string{"new-dep"}}},
		Evidence:          []string{"command=claim-task exit_code=1 stderr=invalid dependency old-dep"},
		Validation:        []string{"validate repaired dependency graph"},
	}
	root, statePath := setupOperatorProject(t, source,
		testhelpers.BuildTaskByStatus("old-dep", models.TaskStatusMerged, now),
		testhelpers.BuildTaskByStatus("new-dep", models.TaskStatusMerged, now))

	// WHEN the operator applies it with JSON output
	result := operatorJSONResult(t, root, "apply-dependency-repair", "task-op-source", "--reason", "apply diagnosed repair")

	// THEN the envelope reports the canonical list and the rewrite is the operator's
	updates, _ := result["updates"].([]any)
	if result["source_task_id"] != "task-op-source" || len(updates) != 1 {
		t.Fatalf("result = %v, want one update from task-op-source", result)
	}
	if deps, _ := updates[0].(map[string]any)["canonical_dependencies"].([]any); len(deps) != 1 || deps[0] != "new-dep" {
		t.Fatalf("canonical_dependencies = %v, want [new-dep]", updates[0])
	}
	task := mustFindTask(t, readState(t, statePath), "task-op-source")
	if len(task.DependsOn) != 1 || task.DependsOn[0] != "new-dep" {
		t.Fatalf("depends_on = %v, want [new-dep]", task.DependsOn)
	}
	if agent := lastHistoryAgent(t, task); agent != "human" {
		t.Fatalf("history actor = %q, want human", agent)
	}
}

func TestOperatorRepairSupersededDependenciesJSON(t *testing.T) {
	// GIVEN a superseded plan still depending on a downstream coding task
	now := time.Now().UTC()
	target := testhelpers.BuildTaskByStatus("plan-op-old", models.TaskStatusSuperseded, now)
	target.RolePair = "code-planning-pair"
	target.DependsOn = []string{"coding-op", "legal-plan"}
	target.SupersededBy = []string{"plan-op-new"}
	target.RescopeReason = testhelpers.StringPtr("Replaced invalid plan")
	root, statePath := setupOperatorProject(t, target,
		testhelpers.BuildTaskByStatus("coding-op", models.TaskStatusReady, now),
		testhelpers.BuildTaskByStatus("legal-plan", models.TaskStatusDraftCodingPlan, now),
		testhelpers.BuildTaskByStatus("plan-op-new", models.TaskStatusDraftCodingPlan, now))

	// WHEN the operator repairs it with JSON output
	result := operatorJSONResult(t, root, "repair-superseded-dependencies", "plan-op-old", "--reason", "drop illegal edge")

	// THEN only the illegal edge is removed and the repair is the operator's
	removed, _ := result["removed_dependencies"].([]any)
	retained, _ := result["retained_dependencies"].([]any)
	if len(removed) != 1 || removed[0] != "coding-op" || len(retained) != 1 || retained[0] != "legal-plan" {
		t.Fatalf("result = %v, want removed [coding-op] retained [legal-plan]", result)
	}
	task := mustFindTask(t, readState(t, statePath), "plan-op-old")
	if len(task.DependsOn) != 1 || task.DependsOn[0] != "legal-plan" {
		t.Fatalf("depends_on = %v, want [legal-plan]", task.DependsOn)
	}
	if agent := lastHistoryAgent(t, task); agent != "human" {
		t.Fatalf("history actor = %q, want human", agent)
	}
}

func TestOperatorDependencyRepairsKeepTheirGates(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"repair-superseded-dependencies", []string{"repair-superseded-dependencies", "task-op-gate", "--reason", "repair"}, "must be SUPERSEDED"},
		{"apply-dependency-repair", []string{"apply-dependency-repair", "task-op-gate", "--reason", "repair"}, "has no repair request"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// GIVEN a blocked task with no superseded edges and no stored request
			root, statePath := setupOperatorProject(t, testhelpers.BuildTaskByStatus("task-op-gate", models.TaskStatusBlocked, now))
			before, err := os.ReadFile(statePath)
			if err != nil {
				t.Fatal(err)
			}

			// WHEN the operator runs the repair
			err = executeRootCommand(t, root, tc.args...)

			// THEN the operation's own precondition, not authority, refuses it
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			after, err := os.ReadFile(statePath)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("refused operator repair changed state")
			}
		})
	}
}

func TestOperatorReconcileMergedRecordsHumanActor(t *testing.T) {
	// GIVEN an integration failure whose work was merged outside the system
	now := time.Now().UTC()
	failed := testhelpers.BuildTaskByStatus("task-op-reconcile", models.TaskStatusIntegrationFailed, now)
	failed.Worktree = nil
	root, statePath := setupOperatorProject(t, failed)
	head := testhelpers.MustGit(t, root, "rev-parse", "HEAD")

	// WHEN the operator reconciles it
	if err := executeRootCommand(t, root, "reconcile-merged", "task-op-reconcile", "--merge-commit", head, "--reason", "PR merged by hand"); err != nil {
		t.Fatalf("operator reconcile-merged failed: %v", err)
	}

	// THEN it is MERGED at that commit and attributed to the operator
	task := mustFindTask(t, readState(t, statePath), "task-op-reconcile")
	if task.Status != models.TaskStatusMerged || task.MergeCommit == nil || *task.MergeCommit != head {
		t.Fatalf("status = %s, merge_commit = %v, want MERGED at %s", task.Status, task.MergeCommit, head)
	}
	if agent := lastHistoryAgent(t, task); agent != "human" {
		t.Fatalf("history actor = %q, want human", agent)
	}
}

func TestOperatorUnblockTaskRecordsScopeExceptionFile(t *testing.T) {
	// GIVEN a blocked coding task with no worktree
	now := time.Now().UTC()
	blocked := testhelpers.BuildTaskByStatus("task-op-scope", models.TaskStatusBlocked, now)
	blocked.Worktree = nil
	blocked.BaseCommit = nil
	root, statePath := setupOperatorProject(t, blocked)
	dir := t.TempDir()
	write := func(name, content string) string {
		path := dir + "/" + name
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	oversize := write("oversize.json", `{"files":["a.py"],"change":"`+strings.Repeat("x", 5000)+`","unchanged":"u"}`)
	malformed := write("unknown.json", `{"files":["a.py"],"change":"c","unchanged":"u","acceptance":"waived"}`)
	valid := write("valid.json", `{"files":["tests/fixtures/seed.py"],"change":"move the seed origin to the past","unchanged":"every relative schedule"}`)

	// WHEN the file is missing, oversize or carries an unknown field
	for _, path := range []string{dir + "/missing.json", oversize, malformed} {
		err := executeRootCommand(t, root, "unblock-task", "task-op-scope", "--reason", "in place", "--scope-exception-file", path)
		// THEN the command refuses and the task stays BLOCKED
		if err == nil || !strings.Contains(err.Error(), "--scope-exception-file") {
			t.Fatalf("%s: err = %v, want scope-exception-file refusal", path, err)
		}
		if task := mustFindTask(t, readState(t, statePath), "task-op-scope"); task.Status != models.TaskStatusBlocked {
			t.Fatalf("%s: status = %s, want BLOCKED", path, task.Status)
		}
	}

	// WHEN the file is valid
	if err := executeRootCommand(t, root, "unblock-task", "task-op-scope", "--reason", "in place", "--scope-exception-file", valid); err != nil {
		t.Fatalf("operator unblock-task with scope exception failed: %v", err)
	}
	// THEN the unblocked entry carries the exception
	task := mustFindTask(t, readState(t, statePath), "task-op-scope")
	exception, ok := task.History[len(task.History)-1].Extra["scope_exception"].(map[string]any)
	if task.Status == models.TaskStatusBlocked || !ok || exception["change"] != "move the seed origin to the past" {
		t.Fatalf("status %s, exception %+v", task.Status, task.History[len(task.History)-1].Extra)
	}
}

func TestOperatorUnblockTaskRecordsBudgetException(t *testing.T) {
	// GIVEN a blocked coding task with no worktree
	now := time.Now().UTC()
	blocked := testhelpers.BuildTaskByStatus("task-op-budget", models.TaskStatusBlocked, now)
	blocked.Worktree = nil
	blocked.BaseCommit = nil
	root, statePath := setupOperatorProject(t, blocked)

	// WHEN either flag is given alone, or the budget exceeds the engine cap
	for name, args := range map[string][]string{
		"seconds only": {"--budget-exception", "3600"},
		"ruling only":  {"--budget-ruling", "D122"},
		"above cap":    {"--budget-exception", "14401", "--budget-ruling", "D122"},
	} {
		err := executeRootCommand(t, root, append([]string{"unblock-task", "task-op-budget", "--reason", "budget"}, args...)...)
		// THEN the command refuses and the task stays BLOCKED
		if err == nil || !strings.Contains(err.Error(), "--budget-exception") {
			t.Fatalf("%s: err = %v, want budget-exception refusal", name, err)
		}
		if task := mustFindTask(t, readState(t, statePath), "task-op-budget"); task.Status != models.TaskStatusBlocked {
			t.Fatalf("%s: status = %s, want BLOCKED", name, task.Status)
		}
	}

	// WHEN both flags are valid
	if err := executeRootCommand(t, root, "unblock-task", "task-op-budget", "--reason", "budget", "--budget-exception", "3600", "--budget-ruling", "D122"); err != nil {
		t.Fatalf("operator unblock-task with budget exception failed: %v", err)
	}
	// THEN the unblocked entry carries the exception
	task := mustFindTask(t, readState(t, statePath), "task-op-budget")
	exception, ok := task.History[len(task.History)-1].Extra["budget_exception"].(map[string]any)
	if task.Status == models.TaskStatusBlocked || !ok || exception["timeout_seconds"] != 3600 || exception["ruling"] != "D122" {
		t.Fatalf("status %s, exception %+v", task.Status, task.History[len(task.History)-1].Extra)
	}
}

package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/liza-mas/liza/internal/embedded"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/paths"
	"github.com/liza-mas/liza/internal/testhelpers"
	"gopkg.in/yaml.v3"
)

// providerHeldState holds consumer only through provider_dependencies: its
// depends_on is empty, the selected child waits on head, which can start.
func providerHeldState(t *testing.T) *models.State {
	t.Helper()
	var state models.State
	err := yaml.Unmarshal([]byte(`tasks:
  - id: plan
    role_pair: code-planning-pair
    status: MERGED
    transitions_executed: {code-plan-to-coding: true}
    output:
      - {desc: first unit, done_when: done, scope: first, spec_ref: specs/a.md}
  - id: plan-code-0
    role_pair: coding-pair
    status: DRAFT_CODE
    parent_tasks: [plan]
    depends_on: [head]
  - id: head
    role_pair: coding-pair
    status: DRAFT_CODE
  - id: consumer
    role_pair: coding-pair
    status: DRAFT_CODE
    provider_dependencies: [{provider_task: plan, transition: code-plan-to-coding, outputs: [0]}]
`), &state)
	if err != nil {
		t.Fatal(err)
	}
	return &state
}

// workspaceWithPipeline writes the default pipeline where the frozen loader
// reads it, so the wait resolver loads it from the project root.
func workspaceWithPipeline(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := paths.New(root).LizaDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pipeline.yaml"), embedded.PipelineConfigContent(), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

type waitingOnJSON struct {
	ID        string `json:"id"`
	WaitingOn *struct {
		Unmet []struct {
			Dependency string   `json:"dependency"`
			Kind       string   `json:"kind"`
			Blocking   []string `json:"blocking"`
			Reason     string   `json:"reason"`
		} `json:"unmet"`
		Heads []struct {
			ID        string `json:"id"`
			Status    string `json:"status"`
			Claimable bool   `json:"claimable"`
			Reason    string `json:"reason"`
		} `json:"heads"`
	} `json:"waiting_on"`
}

func assertConsumerWait(t *testing.T, got waitingOnJSON) {
	t.Helper()
	wait := got.WaitingOn
	if wait == nil {
		t.Fatalf("%s has no waiting_on although provider_dependencies hold it", got.ID)
	}
	if len(wait.Unmet) != 1 || wait.Unmet[0].Dependency != "plan" || wait.Unmet[0].Kind != string(models.DependencyUnsatisfiedPending) ||
		len(wait.Unmet[0].Blocking) != 1 || wait.Unmet[0].Blocking[0] != "plan-code-0" || wait.Unmet[0].Reason != "selected provider child is not MERGED" {
		t.Fatalf("unmet = %+v, want plan declaration blocked by plan-code-0", wait.Unmet)
	}
	if len(wait.Heads) != 1 || wait.Heads[0].ID != "head" || wait.Heads[0].Status != "DRAFT_CODE" || !wait.Heads[0].Claimable {
		t.Fatalf("heads = %+v, want claimable DRAFT_CODE head", wait.Heads)
	}
}

func TestInspectTaskReportsProviderDependencyWait(t *testing.T) {
	result, err := inspectTask(providerHeldState(t), "consumer", inspectTasksOptions{Format: "json", ProjectRoot: workspaceWithPipeline(t)})
	if err != nil {
		t.Fatalf("inspectTask() error = %v", err)
	}
	var got waitingOnJSON
	if err := json.Unmarshal([]byte(result.(string)), &got); err != nil {
		t.Fatalf("task JSON invalid: %v", err)
	}
	assertConsumerWait(t, got)
}

func TestInspectTasksSummaryReportsWaitsOnlyForHeldTasks(t *testing.T) {
	result, err := inspectTasks(providerHeldState(t), inspectTasksOptions{Format: "json", Summary: true, Active: true, ProjectRoot: workspaceWithPipeline(t)})
	if err != nil {
		t.Fatalf("inspectTasks() error = %v", err)
	}
	var rows []waitingOnJSON
	if err := json.Unmarshal([]byte(result.(string)), &rows); err != nil {
		t.Fatalf("summary JSON invalid: %v", err)
	}
	waits := map[string]bool{}
	for _, row := range rows {
		waits[row.ID] = row.WaitingOn != nil
		if row.ID == "consumer" {
			assertConsumerWait(t, row)
		}
	}
	want := map[string]bool{"plan-code-0": true, "head": false, "consumer": true}
	for id, held := range want {
		if got, listed := waits[id]; !listed || got != held {
			t.Errorf("%s waiting_on present = %t (listed %t), want %t", id, got, listed, held)
		}
	}
}

func TestInspectTaskOmitsWaitWithoutWorkspacePipeline(t *testing.T) {
	result, err := inspectTask(providerHeldState(t), "consumer", inspectTasksOptions{Format: "json", ProjectRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("inspectTask() error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(result.(string)), &got); err != nil {
		t.Fatalf("task JSON invalid: %v", err)
	}
	if _, present := got["waiting_on"]; present {
		t.Fatalf("waiting_on = %v, want omitted when no pipeline can resolve it", got["waiting_on"])
	}
}

// A plain task-ID query reaches the task view through ID routing, which hands
// over the pipeline it loaded for agent-ID detection.
func TestPlainTaskIDQueryReportsWait(t *testing.T) {
	projectRoot := t.TempDir()
	statePath, _ := testhelpers.SetupLizaDir(t, projectRoot)
	testhelpers.SetupPipelineConfig(t, projectRoot)
	state := testhelpers.CreateValidState()
	state.Tasks = providerHeldState(t).Tasks
	testhelpers.WriteInitialState(t, statePath, state)

	output, err := InspectCommand([]string{"consumer"}, InspectOptions{Format: "json", ProjectRoot: projectRoot})
	if err != nil {
		t.Fatal(err)
	}
	var got waitingOnJSON
	if err := json.Unmarshal([]byte(output), &got); err != nil {
		t.Fatalf("task JSON invalid: %v", err)
	}
	assertConsumerWait(t, got)
}

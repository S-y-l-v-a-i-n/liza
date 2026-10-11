package models_test

import (
	"reflect"
	"testing"

	"github.com/liza-mas/liza/internal/embedded"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/pipeline"
	"gopkg.in/yaml.v3"
)

// waitPlanTasks is a merged code plan whose output[0] child may exist; consumer
// waits on that child only through provider_dependencies, never depends_on.
const waitPlanTasks = `
  - id: plan
    role_pair: code-planning-pair
    status: MERGED
    transitions_executed: {code-plan-to-coding: true}
    output:
      - {desc: first unit, done_when: done, scope: first, spec_ref: specs/a.md}
      - {desc: second unit, done_when: done, scope: second, spec_ref: specs/a.md}
  - id: consumer
    role_pair: coding-pair
    status: DRAFT_CODE
    provider_dependencies: [{provider_task: plan, transition: code-plan-to-coding, outputs: [0]}]
`

func waitState(t *testing.T, tasks string) *models.State {
	t.Helper()
	var state models.State
	if err := yaml.Unmarshal([]byte("tasks:"+tasks), &state); err != nil {
		t.Fatal(err)
	}
	return &state
}

func waitResolver(t *testing.T) *pipeline.Resolver {
	t.Helper()
	cfg, err := pipeline.LoadFromBytes(embedded.PipelineConfigContent())
	if err != nil {
		t.Fatal(err)
	}
	return pipeline.NewResolver(cfg)
}

func waitHeads(t *testing.T, state *models.State, taskID string) []models.DependencyWaitHead {
	t.Helper()
	wait := models.NewDependencyWaitResolver(state, waitResolver(t)).Wait(state.FindTask(taskID))
	if wait == nil {
		t.Fatalf("%s has no dependency wait", taskID)
	}
	if len(wait.Unmet) == 0 {
		t.Fatalf("%s wait has no unmet dependencies", taskID)
	}
	return wait.Heads
}

func assertHeads(t *testing.T, got, want []models.DependencyWaitHead) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("heads = %+v\nwant  %+v", got, want)
	}
}

func TestDependencyWaitFollowsProviderDependenciesToChainHead(t *testing.T) {
	state := waitState(t, waitPlanTasks+`
  - id: plan-code-0
    role_pair: coding-pair
    status: DRAFT_CODE
    parent_tasks: [plan]
    depends_on: [head]
  - id: head
    role_pair: coding-pair
    status: DRAFT_CODE
`)
	if deps := state.FindTask("consumer").DependsOn; len(deps) != 0 {
		t.Fatalf("fixture consumer depends_on = %v, want empty", deps)
	}
	wait := models.NewDependencyWaitResolver(state, waitResolver(t)).Wait(state.FindTask("consumer"))
	if wait == nil || len(wait.Unmet) != 1 || wait.Unmet[0].DependencyID != "plan" || !reflect.DeepEqual(wait.Unmet[0].BlockingIDs, []string{"plan-code-0"}) {
		t.Fatalf("unmet = %+v, want the plan declaration blocked by plan-code-0", wait)
	}
	assertHeads(t, wait.Heads, []models.DependencyWaitHead{
		{ID: "head", Status: "DRAFT_CODE", Claimable: true, Reason: string(models.DependencyUnsatisfiedPending)},
	})
}

func TestDependencyWaitHeadsNeverInventRunnableEndpoints(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tasks string
		want  []models.DependencyWaitHead
	}{
		{
			name:  "unborn selected child",
			tasks: waitPlanTasks,
			want:  []models.DependencyWaitHead{{ID: "plan-code-0", Reason: "selected provider child does not exist yet"}},
		},
		{
			name: "provider transition not executed",
			tasks: `
  - id: plan
    role_pair: code-planning-pair
    status: MERGED
    output:
      - {desc: first unit, done_when: done, scope: first, spec_ref: specs/a.md}
  - id: consumer
    role_pair: coding-pair
    status: DRAFT_CODE
    provider_dependencies: [{provider_task: plan, transition: code-plan-to-coding, outputs: [0]}]
`,
			want: []models.DependencyWaitHead{{ID: "plan", Status: models.TaskStatusMerged, Reason: "provider transition has not executed"}},
		},
		{
			name: "invalid declaration",
			tasks: `
  - id: plan
    role_pair: code-planning-pair
    status: MERGED
    transitions_executed: {code-plan-to-coding: true}
    output:
      - {desc: first unit, done_when: done, scope: first, spec_ref: specs/a.md}
  - id: consumer
    role_pair: coding-pair
    status: DRAFT_CODE
    provider_dependencies: [{provider_task: plan, transition: code-plan-to-coding, outputs: [5]}]
`,
			want: []models.DependencyWaitHead{{ID: "plan", Status: models.TaskStatusMerged, Reason: "invalid_provider: provider output index 5 is out of range"}},
		},
		{
			name: "blocked endpoint",
			tasks: `
  - id: blocker
    role_pair: coding-pair
    status: BLOCKED
  - id: consumer
    role_pair: coding-pair
    status: DRAFT_CODE
    depends_on: [blocker]
`,
			want: []models.DependencyWaitHead{{ID: "blocker", Status: models.TaskStatusBlocked, Reason: string(models.DependencyUnsatisfiedPending)}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := waitState(t, tc.tasks)
			assertHeads(t, waitHeads(t, state, "consumer"), tc.want)
		})
	}
}

func TestDependencyWaitReportsCycles(t *testing.T) {
	t.Run("cycle only", func(t *testing.T) {
		state := waitState(t, `
  - id: a
    role_pair: coding-pair
    status: DRAFT_CODE
    depends_on: [b]
  - id: b
    role_pair: coding-pair
    status: DRAFT_CODE
    depends_on: [a]
`)
		assertHeads(t, waitHeads(t, state, "a"), []models.DependencyWaitHead{
			{ID: "a", Status: "DRAFT_CODE", Reason: "dependency cycle: a -> b -> a"},
		})
	})
	t.Run("cycle beside an acyclic branch", func(t *testing.T) {
		state := waitState(t, `
  - id: a
    role_pair: coding-pair
    status: DRAFT_CODE
    depends_on: [b, q]
  - id: b
    role_pair: coding-pair
    status: DRAFT_CODE
    depends_on: [a]
  - id: q
    role_pair: coding-pair
    status: DRAFT_CODE
`)
		assertHeads(t, waitHeads(t, state, "a"), []models.DependencyWaitHead{
			{ID: "a", Status: "DRAFT_CODE", Reason: "dependency cycle: a -> b -> a"},
			{ID: "q", Status: "DRAFT_CODE", Claimable: true, Reason: string(models.DependencyUnsatisfiedPending)},
		})
	})
}

func TestDependencyWaitAbsentWithoutHold(t *testing.T) {
	state := waitState(t, waitPlanTasks+`
  - id: plan-code-0
    role_pair: coding-pair
    status: MERGED
    parent_tasks: [plan]
  - id: done
    role_pair: coding-pair
    status: MERGED
    depends_on: [missing]
`)
	resolver := models.NewDependencyWaitResolver(state, waitResolver(t))
	for _, id := range []string{"consumer", "done", "plan"} {
		if wait := resolver.Wait(state.FindTask(id)); wait != nil {
			t.Fatalf("%s wait = %+v, want nil", id, wait)
		}
	}
}

func TestUnmetClaimDependenciesKeepsDirectMergedSemantics(t *testing.T) {
	state := waitState(t, `
  - id: old
    role_pair: coding-pair
    status: SUPERSEDED
    superseded_by: [new]
  - id: new
    role_pair: coding-pair
    status: MERGED
  - id: consumer
    role_pair: coding-pair
    status: DRAFT_CODE
    depends_on: [old, gone]
`)
	got := models.UnmetClaimDependencies(state.FindTask("consumer"), state, waitResolver(t))
	want := []models.DependencySatisfaction{
		{DependencyID: "old", Kind: models.DependencyUnsatisfiedSuperseded, Path: []string{"old"}, BlockingIDs: []string{"old"}},
		{DependencyID: "gone", Kind: models.DependencyInvalidMissing, Path: []string{"gone"}, BlockingIDs: []string{"gone"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unmet = %+v\nwant  %+v", got, want)
	}
}

package prompts

import (
	"regexp"
	"strings"
	"testing"

	"github.com/liza-mas/liza/internal/brand"
	"github.com/liza-mas/liza/internal/pipeline"
)

// The prevent-blocked-task-chains instruction release: every planning role
// receives its handoff, priority, and commitment duties, and (D51) the
// critical-path duties that make a superfluous dependency reviewable, rendered
// under a non-default brand with no raw default-brand literal.
func TestBlockedTaskChainsPlannerAndReviewerDuties(t *testing.T) {
	withPromptBrandValues(t, func() {
		brand.NameTitle = "Acme"
		brand.BinaryName = "acme-cli"
		brand.GlobalDirName = ".acme"
		brand.ProjectDirName = ".acme-run"
	})
	cfg, err := pipeline.LoadEmbeddedReference()
	if err != nil {
		t.Fatal(err)
	}
	resolver := pipeline.NewResolver(cfg)

	const findingRule = "A finding cannot create a commitment"
	rawDefaultBrand := regexp.MustCompile(`(?i)(^|[^A-Za-z])liza($|[^A-Za-z0-9])|\{\{binaryName\}\}`)
	// D78: the feasibility and known-consumer rows keep their refusal semantics,
	// not just their labels — an unchecked existing-source premise and a gap note
	// on a frozen contract are not admissible.
	handoffRows := []string{"| Handoff usable |", "| Priority propagation |", "| Consequential commitment |", "| Proof stage |",
		"| Child feasibility |", "only an unfinished provider's delivery is deferrable",
		"| Known consumers |", "no allocated correction or investigation holds the affected scope", "a gap note alone does not suffice"}
	const childFeasibility = "Child feasibility"
	const knownConsumers = "never freeze the incompatible contract with only a gap note"
	const knownConsumersHold = "hold the affected scope on the corrected contract"
	const verificationHold = "every consumer of the client's behavior depends on"
	architectCriticalPath := []string{"earliest artifact that suffices", verificationHold, "split a consumed fixture from a later proof",
		"weigh an additive seam", "against every expected writer"}
	const edgeMeaning = "earliest artifact that supplies the input"
	const masterEdge = "contract or its implementation"
	// D-96: a shared-file edge is required unless the plan states the edits
	// commute (whole-definition replacements never do), and every writer joining
	// an ordered chain re-weighs the seam; both render without direct coding.
	commuting := []string{"they claim no common key, slot, name or position", "unstated composition keeps the edge", "whole-definition replacements never commute"}
	const seamReweigh = "re-weighs its seam against the chain's writers"
	const seamReweighRow = "a chain joiner skips re-weighing its seam"
	const masterOwner = "Exactly one entry writes each shared file"
	// D-99: a linear run of units building one deliverable in one owner's files
	// is one task unless a split reason is named, and a Must output reaching
	// optional work applies the shared-prerequisite rule along local paths.
	fixedCost := []string{"each coding task pays a fixed cost whatever its size", "each the sole dependent of the one before",
		"builds one deliverable in the same owned files or module", "separately required deliverables (e2e, docs) stay separate tasks",
		"a named regression-attribution risk"}
	const fixedCostRow = "| Per-task fixed cost |"
	const bundleLimits = "or a bundle exceeds the rule's limits"
	optionalPrereq := []string{"names the Must consumer and the shared prerequisite it needs", "inherit_inputs, transitively",
		"needing the optional capability itself is a source conflict for the human", "disclosed as unchecked"}
	d99 := map[string][]string{
		"epic-planner":   optionalPrereq,
		"architect":      optionalPrereq,
		"architect-root": optionalPrereq,
		"code-planner":   append(append([]string{"or the one deliverable of a fixed-cost bundle", "SEPARATE tasks for:"}, fixedCost...), optionalPrereq...),
		// Root cases append master sections to the specialized list; the real
		// root composition (no fixed-cost rule) is tested in internal/agent.
		"code-planner-root":     optionalPrereq,
		"epic-plan-reviewer":    optionalPrereq,
		"architecture-reviewer": optionalPrereq,
		"code-plan-reviewer":    append(append([]string{fixedCostRow, bundleLimits, "a fixed-cost bundle within its limits"}, fixedCost...), optionalPrereq...),
	}
	roleData := func(role, roleType string) *RoleContextData {
		return &RoleContextData{
			Role: role, AgentID: role + "-1", RoleType: roleType,
			TaskID: "task-1", Description: "d", DoneWhen: "w", SpecRef: "specs/goal.md",
			Worktree: "/repo/.worktrees/task-1", IntegrationBranch: "main",
			BaseCommit: strings.Repeat("a", 40), ReviewCommit: strings.Repeat("b", 40),
			ArchRef: "specs/arch.md", PlanRef: "specs/plan.md", EpicRef: "specs/epic.md",
			GoalSlug: "goal", EpicSlug: "epic",
		}
	}

	cases := []struct {
		role     string
		roleType string
		root     bool
		required []string
	}{
		{"epic-planner", "doer", false, []string{"keep their priority at item level", "expose a missing product policy"}},
		{"us-writer", "doer", false, []string{"Carry each story's inherited priority", "cannot fail a Must story"}},
		{"architect", "doer", false, append(append([]string{"minimum mechanism Must scope", "targeted probe", "never a pre-coding gate without a concrete dependency reason",
			childFeasibility, "acceptance manifest", "checked before it restricts a child", knownConsumers, knownConsumersHold, seamReweigh}, architectCriticalPath...), commuting...)},
		{"code-planner", "doer", false, append([]string{"effective priority", edgeMeaning, "must not inherit its whole barrier",
			childFeasibility, "acceptance manifest", "only an unfinished provider's delivery is deferred", knownConsumers, knownConsumersHold, seamReweigh}, commuting...)},
		{"architect", "doer", true, append(append([]string{"group a shared uncertainty", masterEdge, masterOwner, seamReweigh}, architectCriticalPath...), commuting...)},
		{"code-planner", "doer", true, append([]string{"group a shared uncertainty", masterEdge, seamReweigh}, commuting...)},
		{"epic-plan-reviewer", "reviewer", false, append([]string{findingRule}, handoffRows...)},
		{"us-reviewer", "reviewer", false, []string{findingRule, "inherited priority", "without inventing policy"}},
		{"architecture-reviewer", "reviewer", false, append(append([]string{findingRule, "| Minimal Must mechanism |",
			"circular, missing, or superfluous dependencies", "| Critical path |", verificationHold,
			"bundled with a later proof", "without an additive seam weighed against every expected writer", seamReweighRow}, handoffRows...), commuting...)},
		{"code-plan-reviewer", "reviewer", false, append(append([]string{findingRule, "| Dependency meaning |", edgeMeaning,
			"an edge's only reason is overlap of edits stated to commute", seamReweighRow}, handoffRows...), commuting...)},
		{"architecture-reviewer", "reviewer", true, []string{"7. Priority and uncertainty.", masterEdge, masterOwner}},
		{"code-plan-reviewer", "reviewer", true, []string{"7. Priority and uncertainty.", masterEdge}},
		{"code-reviewer", "reviewer", false, []string{findingRule, "label the finding a contract defect naming the owning artifact and role"}},
	}
	for _, tc := range cases {
		name := tc.role
		if tc.root {
			name += "-root"
		}
		t.Run(name, func(t *testing.T) {
			sections, err := resolver.ContextSections(tc.role)
			if err != nil {
				t.Fatal(err)
			}
			data := roleData(tc.role, tc.roleType)
			if tc.root {
				data.DecompositionRoot = true
				data.MasterOutputRefField = "plan_ref"
				sections = append(sections, map[string]string{"doer": "master-decomposition-mandate", "reviewer": "master-decomposition-review"}[tc.roleType])
			}
			rendered, err := BuildRoleContext(tc.role, sections, data)
			if err != nil {
				t.Fatal(err)
			}
			for _, required := range append(append([]string{}, tc.required...), d99[name]...) {
				if !strings.Contains(rendered, required) {
					t.Errorf("missing %q", required)
				}
			}
			// D-99: without direct coding allocation the architect emits plans,
			// not coding units, so the fixed-cost rule does not render there.
			if tc.role == "architect" || tc.role == "architecture-reviewer" {
				for _, gated := range []string{fixedCost[0], fixedCostRow} {
					if strings.Contains(rendered, gated) {
						t.Errorf("renders direct-allocation-only %q without direct coding", gated)
					}
				}
			}
			if strings.Contains(rendered, "splitting cohesive work adds overhead") {
				t.Error("still renders the cost-free cohesion clause replaced by the fixed-cost rule")
			}
			// D-96: no role keeps the unconditional same-file edge.
			for _, stale := range []string{"they MUST have a depends_on", "may touch the same files, add depends_on",
				"may modify the same files without depends_on", "parallel coders will hit merge conflicts", "per-scope split"} {
				if strings.Contains(rendered, stale) {
					t.Errorf("still renders unconditional shared-file rule %q", stale)
				}
			}
			if m := rawDefaultBrand.FindString(rendered); m != "" {
				t.Errorf("non-default rendering leaks %q", m)
			}
		})
	}

	// D-99: a direct coding allocation emits coding units, so the architect gets
	// the fixed-cost rule and the architecture reviewer its row.
	for _, tc := range []struct {
		role, roleType string
		required       []string
	}{
		{"architect", "doer", fixedCost},
		{"architecture-reviewer", "reviewer", append([]string{fixedCostRow, bundleLimits}, fixedCost...)},
	} {
		t.Run(tc.role+"-direct", func(t *testing.T) {
			sections, err := resolver.ContextSections(tc.role)
			if err != nil {
				t.Fatal(err)
			}
			data := roleData(tc.role, tc.roleType)
			data.DirectCodingAllocationAvailable = true
			rendered, err := BuildRoleContext(tc.role, sections, data)
			if err != nil {
				t.Fatal(err)
			}
			for _, required := range tc.required {
				if !strings.Contains(rendered, required) {
					t.Errorf("missing %q", required)
				}
			}
			if m := rawDefaultBrand.FindString(rendered); m != "" {
				t.Errorf("non-default rendering leaks %q", m)
			}
		})
	}
}

// D-99: the priority owner applies the shared-prerequisite rule along
// dependency paths, so planners and reviewers cite one source.
func TestPriorityOwnerAppliesAlongDependencyPaths(t *testing.T) {
	t.Parallel()

	reference := readContractFixture(t, "../../skills/shared/references/reference-first-authoring.md")
	for _, required := range []string{"This holds along dependency paths", "names the shared prerequisite it needs", "a wait that needs nothing is removed"} {
		if !strings.Contains(reference, required) {
			t.Errorf("reference-first authoring missing %q", required)
		}
	}
}

// D51: the architecture skill names false dependence as an anti-pattern, not
// only false independence, and keeps the real-provider verification hold.
// D-96: shared-file edges yield only to stated commutation (an interface
// contract is no bypass), and the seam is weighed against every expected writer.
func TestArchitecturePlanningFalseDependence(t *testing.T) {
	t.Parallel()

	skill := readContractFixture(t, "../../skills/architecture-planning/SKILL.md")
	for _, required := range []string{
		"**False Dependence**", "earliest one that suffices",
		"every consumer of the client's behavior depends on", "additive seam",
		"including later supplements and corrections", "unless stated to commute",
		"not a writer edge", "each replace one whole definition still share it",
	} {
		if !strings.Contains(skill, required) {
			t.Errorf("architecture-planning skill missing %q", required)
		}
	}
	if strings.Contains(skill, "`depends_on` or explicit interface contracts") {
		t.Error("architecture-planning skill still lets an interface contract replace a writer edge")
	}
}

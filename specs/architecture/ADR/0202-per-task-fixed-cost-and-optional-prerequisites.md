# 202 - Per-Task Fixed Cost and Optional Prerequisites in Planning

## Status

ACCEPTED. Amends [ADR-0150](0150-shared-authoring-vocabulary.md) (priority
rules applied along dependency paths; decomposition guidance).

## Context

In run DEV-766 (D-99) a code plan cut one capability's work into a 9-unit
serial chain of Should units. Each unit paid the full fixed cost of a coding
task: claim and context load, submit admission running its validation list
(long, per D-97), review and merge. 25 tasks waited transitively behind the
first unit. A later critical-path computation found the chain had slack (6 of
31 links), and its Must dependents needed three of its units semantically.
So the run strongly shows the fixed-cost half of the defect and only weakly
the Must-waits-on-optional half.

Planning guidance pushed one way only. The code planner was told "one
observable behavior change" per task, with two too-broad smells; its
counterweight ("splitting cohesive work adds overhead") named no cost and no
test. The code-plan reviewer had two reject gates for too-big units and none
for too-small. Splitting a chain buys no parallelism, only smaller reviews and
sharper attribution, and pays the fixed cost once per link. That the gap caused
this plan is inferred: the run kept no planner rationale.

The priority owner already said a shared prerequisite a Must needs stays
necessary and a Must needing an optional capability is a source conflict for
the human. No planner or reviewer was asked to apply it to the dependency
graph.

## Decision

1. **Per-task fixed cost** (`fixed-cost-bundle`). Every coding task pays a fixed
   cost whatever its size, so a linear run of units — each the sole dependent of
   the one before, no other task or plan needing an intermediate output — that
   builds one deliverable in the same owned files or module is one task whose
   single intent is that deliverable. Independent concerns and separately
   required deliverables (e2e, docs) stay separate tasks; the plan may still
   split for a diff too large to review as one or a named regression-attribution
   risk. A linear run loses no parallelism by construction. The code planner
   carries the rule and its opening single-intent sentence admits the bundle;
   under direct coding allocation the architect carries it too.
2. **Symmetric review** (`fixed-cost-row`). The code-plan reviewer, and the
   architecture reviewer under direct coding allocation, reject a run the rule
   bundles that is split with no named reason, and a bundle that exceeds the
   rule's limits. The single-intent and scope-smell gates exempt only a bundle
   within those limits.
3. **Optional prerequisites** (`optional-prerequisite`). A Must output that
   reaches a Should/Could output by any local path (`depends_on`,
   `task_depends_on`, `provider_dependencies`, `inherit_inputs`, transitively)
   names the Must consumer and the shared prerequisite it needs, which stays
   necessary while the optional feature stays deferrable. Needing the optional
   capability itself is a source conflict for the human; a wait needing nothing
   is removed; a target whose priority the plan cannot see is disclosed as
   unchecked. Epic planner, architect and code planner carry it; the shared
   Priority propagation row applies it for epic-plan, architecture and code-plan
   reviewers. Code-planning decomposition roots, whose prompts drop the
   decomposition and review-checklist sections, carry it in the master mandate
   and master review; their outputs are plans, so the fixed-cost rule does not
   apply to them. The priority owner (`reference-first-authoring.md`) states that the
   rule holds along dependency paths.

## Consequences

- An over-split linear chain and an unstated optional prerequisite on a Must
  path become reviewable defects. Guidance mitigation, not enforcement: no
  engine measure of unit size or priority position exists, and a planner can
  still split, or a reviewer approve, an inefficient chain. Rendered-template
  tests prove delivery to each role, not planner judgement in a run.
- Bundled tasks have larger reviews, costlier rejection rework and coarser
  regression attribution; the split reasons and the existing too-broad smells
  bound this.
- A plan checks only its own graph and declared waits, not the run's global
  critical path.
- The role-budget baseline was re-anchored within the 5 % ceiling: about
  +1.3 KB on the code-plan reviewer prompt (+4.8 %), +1.0 KB code planner,
  +0.5 KB architect, epic planner, their reviewers and the code-planning root
  doer and reviewer, +0.2 KB reads of the priority owner.
- Bundling consecutive unstarted children of a merged plan in flight is
  deferred in `TECH_DEBT.md`.

## Alternatives Considered

1. A numeric minimum unit size (lines or minutes). Rejected: not
   stack-agnostic and unknowable at planning time; the relative fixed-cost test
   needs no number.
2. Engine bundling of consecutive unstarted children. Deferred: provider
   selectors follow only a unique same-slot successor and a task carries one
   acceptance source, so it needs many-to-one slot lineage, multi-section
   acceptance and a reviewed bundle allocation. The measured case gave no
   critical-path benefit; reviewed `replan` remains the in-flight path.
3. Let a plan accept a Should unit on a Must path by declaration. Rejected: it
   would relabel scope, which the priority owner reserves to the human.

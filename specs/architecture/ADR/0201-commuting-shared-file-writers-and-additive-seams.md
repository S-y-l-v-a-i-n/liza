# 201 - Commuting Shared-File Writers and Additive Seams

## Status

ACCEPTED. Amends [ADR-0150](0150-shared-authoring-vocabulary.md) (2026-09-26
critical-path amendment: shared-file writers) and extends
[ADR-0199](0199-human-decisions-before-freeze-and-reviewed-correction-placement.md)
(writers joining an ordered chain).

## Context

Every planning layer required a dependency edge between any two tasks editing
the same file, with no commuting exception: code planner, architect self-check,
and both planning reviewers. The ADR-0150 amendment added only "weigh a per-scope
split" for shared-file writer chains, scoped to the current `output[]`.

In run DEV-766 (D-96) one capability's architecture made every feature extend
two dispatcher functions by redefining each whole function in its own migration.
Per-writer files satisfied "per-scope split" in letter, yet two parallel writers
would merge cleanly and silently drop the earlier change. The architect weighed
an additive seam against the seven store stories it knew and rejected it; later
supplements and corrections grew the chain to 33+ writers. Its ordering rule,
following the liza rule, admitted no commuting exception, so insertion-only edits
to the shared contract, store, package index and README joined the same total
order. The critical path equalled the writer count.

The engine already serializes commuting same-file writers at merge:
submit-time rebase and the merge-tree check route a textual conflict, and the
optional `scripts/integration-test.sh` a failing post-merge test, to
INTEGRATION_FAILED for repair by the coder. That proves routing, not semantic
composition.

## Decision

1. **Commuting is a stated, reviewable claim.** One criterion
   (`commuting-edits`) replaces the unconditional edge for code planners,
   architects and both planning reviewers: two edits to one shared file or
   definition commute only when each preserves the other's behavior, they claim
   no common key, slot, name or position, and neither relies on the other's
   initialization or state. Unstated composition keeps the edge; files that each
   replace one whole definition never commute. Producer/consumer edges stay
   regardless of file overlap. An interface contract removes a consumer's wait on
   implementation, not a writer edge. Code-plan review also rejects an edge whose
   only reason is overlap of edits stated to commute.
2. **Additive seam against every expected writer.** Before routing writers
   through one shared definition, the architect weighs an additive seam (each
   writer adds its own file, function or registered entry) against every expected
   writer — stories and the Provider Ask Inventory, not only its `output[]` — and
   records the count weighed. The architecture Critical path row and
   `architecture-planning` False Dependence carry the same duty.
3. **Re-weigh at every join.** A writer joining an ordered chain on a
   non-commuting definition (`seam-reassessment`) counts the chain's writers
   against the count its seam decision weighed; once outgrown it raises an
   additive-seam correction to the definition's owner — scheduled through the
   ADR-0199 Placement and reservation machinery — or records why the chain still
   wins. It renders for code planners and architects, root or not, independent of
   direct coding allocation; both planning reviewers reject a join that skips it.

## Consequences

- Insertion-only and other commuting writers can code in parallel; the existing
  merge routing serializes their integration.
- A design whose writers replace one whole definition must survive a full-count
  weighing at design time and again at each join, so a rejection against an
  early count no longer authorizes indefinite growth.
- Guidance-only mitigation: no engine predicate changes. Rendered-template tests
  prove delivery to each role, not agent judgement or concurrent writers in a run.
  Live runs keep their reviewed rules until their owners amend them.
- The role-budget baseline was re-anchored (human decision, ceiling unchanged):
  main already sat at the 5 % ceiling with unanchored ADR-0198/0199 growth; this
  change adds about 0.8 KB to architect prompts and 0.5 KB to its skill reads,
  0.55 KB to the code-planner and 0.35 KB to planning-reviewer prompts.
- Non-commuting writers still code serially; parallel coding with ordering-only
  edges, merge-time re-derivation and slot renumbering is deferred in
  `TECH_DEBT.md`.

## Alternatives Considered

1. Keep the unconditional edge and add only seam guidance. Rejected: liza would
   still mandate the total order over commuting writers that D-96 observed.
2. Ordering-only dependency edges so non-commuting writers code in parallel and
   re-derive at merge. Deferred: a new dependency kind across claim, validation,
   plan-check, reservation and prompts, whose gain is bounded by the re-derivation
   pass and re-review each writer still needs; unmeasured.
3. Treat insertion-only edits as commuting by definition. Rejected: two
   insertions can claim one key or slot, or one can need the other's
   initialization.

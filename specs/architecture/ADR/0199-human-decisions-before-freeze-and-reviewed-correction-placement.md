# 199 - Human Decisions Before Freeze and Reviewed Correction Placement

## Status

ACCEPTED. Extends [ADR-0172](0172-human-owned-blocks.md),
[ADR-0195](0195-provider-reservations-for-inserted-writers.md) and
[ADR-0198](0198-frozen-interface-corrections-and-direct-coding-allocation.md).

## Context

ADR-0198 settled provider interface asks before freeze. Another class of
decision still surfaced only after freeze (D-94 root cause 4). Behavior, NFR and
policy questions were recorded as optional Open Questions designed to travel to
code-planners, or left as silent defaults. They reached the human only when a
downstream doer blocked. Reviewers had no closure gate. Only the coder and the
integration analyst were told about `--human-action`, so a planner's block
raised no `AWAITING HUMAN` until the orchestrator reclassified it.

Correction writers in ordered shared-file chains hit a related gap (D-95). The
admission rules governing those chains are project-authored policy. They defined
only a late-admission default, which contradicts a correction that must precede
unstarted writers. The blocked-task guidance also withheld ordering from the
planner. So every correction became a human placement ruling, even when the
planner had already computed a legal slot. The engine mechanisms of ADR-0195
were not the gap.

## Decision

1. **Human-owned commitments gate their outputs.** In the shared authoring
   contract, a human-owned commitment is a Design prerequisite of each output it
   affects: behavior, NFR, policy, or a missing case of an inherited rule. An
   output freezes only on a recorded human decision, or on a declared wait on an
   allocated investigation, correction or decision task. Asking the human
   neither settles nor holds it. Delegated design choices, implementation
   prerequisites and final acceptance evidence keep their Proof Stage treatment.
   Architecture Open Questions name an owner, the affected outputs and how they
   settle.
2. **Reviewers check closure.** The shared hand-off rows (code-plan, epic-plan
   and architecture review) reject an output that relies on an unsettled
   human-owned decision without a recorded ruling or a declared wait on an
   allocated investigation, correction or decision. The
   architecture reviewer also rejects a rule ordering shared-file writers that
   gives a later correction writer no position and names no human ruling.
   Reviewers gain no new authority; rejection routes the decision back to the
   author.
3. **Planning doers can ask the human directly.** The architect, code-planner,
   epic-planner and us-writer tool guidance documents `mark-blocked
   --human-action`, which raises `AWAITING HUMAN` under ADR-0172.
4. **Correction placement is authored, reviewed, then applied.** A correction
   writer joins an ordered chain in one of two ways: a code plan, or a direct
   coding allocation (ADR-0198). Its author records `## Placement` under the
   governing rule's correction case. Placement holds:
   - the rule;
   - its predecessors: every started or merged writer, as declared waits;
   - its successors: the unstarted writers its done_when names;
   - the earliest slot the rule allows between those predecessors and successors,
     clear of fixed and merged slots; reviewers reject a later one.

   The plan or allocation reviewer checks it. At hand-off, the orchestrator
   rechecks that each successor was never claimed and is unstarted. It then
   applies `reserve-provider` for each successor and passes only once all
   reservations hold, so generation stays gated.

   On failure the orchestrator keeps the plan unpassed and keeps its successful
   reservations. A stale caller stops. Lock contention gets a bounded retry. A
   bad transition, cap or cycle returns to a reviewed amendment or replan. A
   started successor, no legal slot, or a rule without a correction case is
   held for the human. Ordinary later admissions keep the rule's default. The
   ADR-0195 commissioning reservation is unchanged.

## Consequences

- Human-owned questions known at architecture time are settled or explicitly
  awaited before dependent work freezes, rather than first reaching a coder.
- A legacy ordering rule without a correction case still needs its owner's
  ruling, for example a standing human decision. The engine does not infer one.
- Reservation eligibility (`reservationPlaceable`) is unchanged. "Never
  claimed" is a policy recheck at hand-off, not an engine predicate.
- These are rendered author, reviewer and orchestrator duties. Tests prove that
  they render and how they route, not agent compliance. The effect on
  correction and replan rates needs measuring on a later run.

## Alternatives Considered

- Compute the earliest slot in the engine. That would hard-code project policy
  such as migration filenames and fixed slots (GUARDRAILS G1.1), and the engine
  has no typed shared-file ownership on coding tasks.
- Add a typed `reserve_successors` field on plan outputs or a `RepairRequest`
  reservation operation. That adds schema and a trust boundary while the
  hand-off already gates generation; revisit if placement errors recur.
- Leave placement to the later writer's planner. ADR-0195 rejected this because
  that planner discovers the order at claim time. Here the correction's own
  author proposes the placement, a reviewer checks it before generation, and
  the orchestrator applies it through ADR-0195's reservations.
- Let reviewers raise `AWAITING HUMAN`. That expands reviewer authority; a
  rejection already routes the decision to the author, who holds the channel.

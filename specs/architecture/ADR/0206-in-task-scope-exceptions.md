# ADR-0206: In-Task Scope Exceptions

## Status

ACCEPTED — 2026-10-13. Amends the task corrective context boundary
(prompt delivery of unblock reasons and notes, D-10) and the orchestrator
recovery routes of [ADR-0170](0170-continuation-is-the-default-unblock.md).
Related: [ADR-0197](0197-reviewed-plan-amendments-and-validation-notes.md),
[ADR-0199](0199-human-decisions-before-freeze-and-reviewed-correction-placement.md).

## Context

A coding task often blocks on a small gap outside its reviewed scope: a fixture
reads the wrong field, a test transport lacks one read, a sibling test asserts
too much, a seed constant is refused by a provider. The fix is a few lines of
test support. The blocked task cannot make it: coder-declared
`scope_extensions` justify touching extra files but cannot override an
explicit reviewed plan rule or another task's file. The orchestrator cannot
grant it either: the unblock reason reaches the doer and reviewer as evidence
that leaves scope unchanged, and the blocked-task recovery routes stop at
"corrected inputs → unblock" or a reviewed corrective plan or replacement.

So every such gap became a correction plan: plan, review, hand-off and a new
coding task, often superseding the blocked one. In one run, seven of these were
commissioned in a day and each was cancelled after a human ruled the repair "in
place, as a named exception"; delivered in place, they merged in about 25
minutes (D-111).

## Decision

1. **Carrier.** `unblock-task <id> --scope-exception-file <json>` (orchestrator
   or operator) records one exception on the `unblocked` history entry as
   `scope_exception: {files, change, unchanged}`. The file is one strict JSON
   object of at most 4096 bytes: 1-8 distinct clean repository-relative files,
   a nonblank statement of the permitted change and a nonblank statement of
   what must stay unchanged. Only tasks whose doer role is `coder` accept it.
   It joins the lifecycle request identity, so exact replay is a no-op and a
   different exception under the same request ID is refused. Every other
   unblock gate applies unchanged.
2. **Authority, file scope only.** Every later doer and reviewer prompt for the
   task renders all recorded exceptions, oldest first, in the task corrective
   context. Outside the task's original scope an exception permits only its
   files and only its stated change; what it keeps unchanged must stay
   unchanged. Canonical acceptance, provider dependencies, review boundary and
   lifecycle gates remain unchanged without qualification. The doer declares
   each such file as a `scope_extension` citing the exception; the reviewer
   treats changes within it as in scope and any other out-of-scope change as a
   scope finding. Exceptions accumulate for the task's life.
3. **Route.** The blocked-task wake prefers the exception to a correction plan
   or replacement when the blocked coding task's fix touches only test support,
   fixtures or its own constants; production code, merged contracts,
   acceptance and provider obligations are unchanged; no other unmerged task
   writes those files; and no human-owned decision is unsettled. Correction
   plans and replacements stay for everything else. A coder that blocks names
   the in-place candidate (files, change, unchanged) when one exists.

Eligibility is judgment, not an engine predicate: the engine cannot classify
"test support" in an arbitrary stack, and file ownership is enforced at review,
not at submission. The engine bounds the grant and makes it visible; the
reviewer enforces it.

## Consequences

- An eligible gap costs one orchestrator turn and the original coding cycle
  instead of a plan, a review and a new coding task, with no human ruling.
- Production, contract and product changes still need reviewed correction or a
  human decision; an exception that tries to carry one is visible to the
  reviewer as a scope finding.
- Scope exceptions are auditable from task history; an older binary ignores
  the extra field.
- Not proven by tests: that orchestrators choose the route for eligible gaps
  and reviewers accept the deliveries. The run-level check is that eligible
  gaps close without a new plan task and merge within one coding cycle.

## Alternatives

- **Exception stated in the unblock reason.** Rejected: the corrective context
  makes that text evidence, deliberately not authority.
- **Doer self-authorizes through `scope_extensions`.** Rejected: the doer
  cannot see other live writers or contract status, and the reviewer would be
  the only gate on overriding a reviewed plan rule.
- **Reviewed micro-amendment (`amend-plan`).** Rejected: it keeps a planner and
  review cycle, the cost this decision removes.

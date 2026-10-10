# 78 - Repairable Review Boundary Metadata

## Status

ACCEPTED. Amended by [ADR-0200](0200-orphaned-review-base-heal.md): a `base_commit` that is no longer on the integration branch has no metadata-only repair and is healed or routed by the engine.

## Context

ADR-0064 treated stale review boundary metadata as a hard lifecycle boundary failure that moved the task to `INTEGRATION_FAILED`. That prevented reviewers from inspecting the wrong commit, but it also pushed repairable local metadata drift into integration recovery.

A second review issue clarified the boundary model further: code review has two distinct ranges:
- `base_commit..review_commit` is the scope and workmanship authority
- `integration_branch..review_commit` is a late drift check against current integration

If `review_commit` or `base_commit` is stale relative to the task worktree, reviewer assignment should stop, but the task does not necessarily need integration failure. The metadata can be repaired from the worktree and merge-base.

## Decision

Treat stale review boundary metadata as repairable drift while preserving exact review-boundary validation.

Liza now:
- keeps reviewers scoped to `base_commit..review_commit`
- checks current integration drift separately with `integration_branch..review_commit`
- blocks reviewer assignment when `review_commit` or `base_commit` is stale
- provides `liza update-review-commit` to refresh both `review_commit` and `base_commit` from the worktree and configured integration branch merge-base
- documents stale review metadata as repairable rather than automatically integration-failed

Reviewers still must inspect exactly the commit Liza records. The change is the recovery path for stale metadata, not a relaxation of review boundary integrity.

## Consequences

Positive:
- Reviewers do not inspect unrecorded commits.
- Repairable review metadata drift stays in review repair flow instead of integration failure.
- Scope deviations are judged from the task's actual scope diff, not from unrelated sibling integration drift.
- `base_commit` becomes an explicit part of the review boundary, not just background metadata.

Trade-offs:
- Operators and agents need a dedicated repair command before review can proceed.
- Review logic now distinguishes scope/workmanship drift from current-integration drift.
- Incorrect repair could still change what reviewers inspect, so the command must recompute from the worktree and integration merge-base.

## Alternatives Considered

1. Keep ADR-0064's `INTEGRATION_FAILED` recovery for stale review metadata.

Rejected because the failure can be local metadata drift that is repairable without integration analysis.

2. Auto-update review metadata during reviewer claim.

Rejected because reviewer assignment should not silently change the submitted review boundary.

3. Judge review scope from current integration to `review_commit`.

Rejected because sibling integration drift can be misclassified as the task's scope deviation.

## Relationship to Prior Decisions

Supersedes ADR-0064's recovery outcome for stale review metadata. Extends ADR-0054 (Blocking Await Primitives) by preserving review wait/reclaim safety while adding a repair path. Complements ADR-0037 (Rebase Conflict Detection) by keeping review-boundary repair separate from rebase/integration failure.

---
*Reconstructed from commits b1e7cd1f and 1692f388 (2026-05-21 to 2026-05-28)*

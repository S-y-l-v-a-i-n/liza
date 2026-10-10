# 200 - Engine Repair of Orphaned Review Bases

## Status

ACCEPTED. Amends [ADR-0078](0078-repairable-review-boundary-metadata.md)
(Alternative 2) for one case; complements
[ADR-0140](0140-reviewer-claim-circuit-breaker.md).

## Context

A merge can be published on the integration branch and then rolled back or
redone (integration-test failure, a receipt-persist lock timeout), and humans
can rewrite the branch. A task that captured such a commit as `base_commit` —
most often at submission, which rebases onto the integration HEAD it reads —
keeps a base that is no longer on the integration branch. ADR-0078 treats a
mismatched base as repairable metadata, but here no metadata-only repair
exists: `update-review-commit` would set the base to the last surviving
ancestor and pull the orphan's content into `base_commit..review_commit`.
The ADR-0140 breaker made the stall visible and kept the queue moving, but the
task was re-probed forever until someone ran `rebase --onto` by hand
(operator register D-26: five instances, up to 7 h each).

## Decision

1. **Classify first.** A reviewable task whose `base_commit` resolves, is an
   ancestor of `review_commit` and is not an ancestor of the integration ref is
   *orphaned*. Review assignment reports it before acceptance evidence, as a
   `review_boundary_repair` candidate marked orphaned. Every other mismatch keeps
   ADR-0078's `update-review-commit` repair.
2. **Heal in place only when provably safe.** The reviewer supervisor runs the
   `heal-orphaned-review-base` lifecycle operation for an orphaned candidate.
   It heals only a submitted task with no reviewer claim and no approvals,
   whose clean worktree HEAD is `review_commit` and whose
   `base_commit..review_commit` has no merge commit. It replays exactly that
   range with `git rebase --onto <integration> <base_commit>`, and requires the
   same commit count and equal per-commit patch identities. It then reruns
   submission's post-rebase checks, executes fresh acceptance evidence, and
   records `review_commit`/`base_commit` with a `review_commit_updated`
   history entry marked `orphaned_base_heal`. The task stays submitted; the
   next claim is a fresh review of exactly the recorded commit. The orphan's
   own content is outside the new range by construction; whether the task still
   works without it is judged by that review and by merge-time integration.
3. **Everything else goes to integration recovery.** A partially approved
   task, a conflict, a content mismatch or a deterministic validation or
   acceptance refusal leaves the worktree at its submitted HEAD and moves the
   task to `INTEGRATION_FAILED`, with the manual `rebase --onto` repair in
   `integration_failure.detail`. The doer's integration-fix claim clears the
   old approvals.
4. **Lifecycle discipline.** The heal holds project lifecycle (shared), then
   the per-task claim lock for the whole invocation. It admits the caller
   (current generation, registered reviewer of the task's role pair, work
   admission) and reserves a preparation whose payload digest binds
   `{old review, old base, target}` before any Git write. A heal preparation
   found later under the task lock belongs to an ended invocation and is
   settled from Git: an interrupted rebase is never aborted but fenced — raw
   Git takes no task lock, and an operator's partial resolution cannot be told
   from the heal's own stop; HEAD at the submitted commit is proven absent; a HEAD whose range matches the
   digest and patch identities is proven and finished once; anything else
   stays fenced for inspection. A claim that meets an unsettled heal treats it
   as a candidate-local repair, never a whole-claim failure.
   `update-review-commit` takes the same task lock.

## Consequences

Positive:
- The observed rollback and rewrite instances self-heal on the next reviewer
  claim, with no raw Git command for a human or an operator.
- Unsafe cases become visible, engine-staffed `INTEGRATION_FAILED` work instead
  of a quarantine retried forever.

Trade-offs:
- A reviewer supervisor writes to a doer's worktree and runs acceptance
  commands while holding the task lock.
- Patch identity includes context lines: integration edits near a task's hunks
  make the heal refuse, routing to the doer instead.
- A heal that ends while its rebase is stopped on a conflict stays fenced
  until an operator aborts or finishes that rebase; the quarantine anomaly
  carries the inspection steps.
- The trigger is unchanged. Merge publication before tests and receipt
  persistence still lets readers capture a commit that later leaves the branch.

## Alternatives Considered

1. **Never move the integration ref backward.** Removes new orphans, but not
   human rewrites or existing ones, and redesigns merge → test → rollback.
2. **`update-review-commit` alone.** Absorbs the orphan's content into the
   review range.
3. **Heal inside the claim transaction machine.** Claims in one call but adds
   another re-entry phase to the claim's lock sequence.

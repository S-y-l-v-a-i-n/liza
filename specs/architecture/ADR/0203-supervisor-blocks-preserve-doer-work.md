# 203 - Supervisor Blocks Preserve Doer Work

## Status

ACCEPTED. Amends [ADR-0060](0060-agent-execution-progress-watchdog.md) step 4
(blocked-task worktree cleanup).

## Context

In run DEV-766 (D-101) a coder's provider session ended on a quota error
(429) while a background test job kept its CLI alive. Thirty minutes without
output later the execution progress watchdog cancelled the CLI and blocked the
task. Every supervisor block then deleted the task worktree and branch, as
ADR-0060 step 4 prescribed, and took about 75 minutes of uncommitted work with
it. The orchestrator unblocked the task minutes later to a fresh worktree.

The same deletion ran on every supervisor block: spinning, prompt-context
failure (on the reviewer path, the branch of a task under review), crash
loop, tool-failure loop and abandoned-background-job loop. Agent-initiated
`mark-blocked` and the exit-42 block never deleted anything. Since ADR-0060,
BLOCKED tasks with a preserved worktree became a supported continuation:
`unblock-task` with `--assign-to` or `--rebase-on` needs the worktree, and a
claim adopts uncommitted work left in it as one WIP commit (INVARIANTS §7).
The cleanup no longer protected an invariant; it destroyed the state the
repair paths expect.

## Decision

1. **Supervisor blocks keep the worktree and branch.** Deletion stays with
   explicit cleanup: `wt-delete`, cancel, supersede, merge.
2. **An owned doer block saves the work first.** Before a block of a task this
   agent executes as its doer, the supervisor commits the worktree's tracked,
   staged and untracked changes as one hook-skipping WIP commit on the task
   branch, through the same helper as the quota stop
   (`CommitTerminatedDoerWIP`). A refusal or failure only logs: the block
   proceeds, nothing is deleted, and the next claim adopts what remains
   (a failed commit may leave the changes staged). A reviewer-path block
   never commits on the doer's behalf.
3. **The block is fenced to the lifecycle it observed.** The save runs outside
   the state lock, so the BLOCKED transaction re-checks the owner slot (doer
   or reviewer) and the task's lifecycle token (`TaskTransitionID`) observed
   before the save; a task reassigned, submitted or otherwise moved meanwhile
   is left to its new owner. The crash and abandoned-job loops decide after
   the exit released the claim: they may block only the exact continuation
   that release produced, identified by the token captured inside the release
   transaction. Those blocks make no WIP commit (the task no longer executes);
   the kept worktree holds the work for the next claim.
4. **A provider-ended session is a provider failure, not a stall.** When the
   watchdog fires and the provider output carries a classified quota or
   provider-unavailable error, the supervisor takes the existing
   provider-failure exit instead of blocking: the quota path commits WIP, the
   claim is released with the worktree kept, the provider signal stops spawns.
   An unavailable ending gets no supervisor WIP commit; the next claim adopts
   the dirty worktree. Sessions with no classified error still block. The
   D-38 exemption for the session's own in-flight submit keeps its fixed
   marker deadline.

## Consequences

- No supervisor block deletes work. BLOCKED tasks keep their worktrees until
  explicit cleanup, as agent-initiated blocks already did; disk use grows
  accordingly.
- A worktree that caused a spinning or crash block survives to the next
  attempt; the claim validates path, branch, HEAD and base and blocks for
  repair instead of deleting it.
- Doer "commit before long runs" guidance is unnecessary for preservation and
  was not added.
- Out of scope: the abandoned-job alert still says "claim preserved" although
  the exit released the claim; admission still depends on the doer's provider
  session staying alive (D-101 (e) second half).

## Alternatives Considered

1. Keep the worktree without a block-time commit. Smaller, but
   `unblock-task --rebase-on` refuses tracked-dirty worktrees and supersede
   successors cannot `git show` uncommitted work.
2. Count a live background job as watchdog progress. Rejected: it masks hung
   jobs and is provider-specific; the provider already bounds background jobs.
3. Reorder the exit flow so the loop blocks run before the claim release.
   Rejected: it moves provider-crash handling ahead of the release and widens
   the change; carrying the released token is exact and local.

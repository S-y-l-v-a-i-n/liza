# ADR-0208: Operator Release of a Capped Blocked Lineage

## Status

ACCEPTED — 2026-10-13. Amends decision 5 and the rejected "dedicated human
release command" alternative of
[ADR-0171](0171-cap-agent-blocked-recovery-replacements.md).

## Context

ADR-0171 caps agent blocked-recovery replacements and makes `resume` the only
release: a human runs `analyze`, which reports the capped task as a
`blocked_replacement_chain` HALT and trips the circuit breaker, then `resume`
resolves that HALT for the task's blocked episode. ADR-0171 rejected a
dedicated release command because `resume` already existed.

In a live run (D-146) that route cost one run-wide pause per release. Every
agent stopped so that a human could permit one replacement of one task, and
independent capped chains are reported one at a time, so two capped lineages
cost two pauses. An operator watching the run cannot use `analyze` in routine
rounds, because it pauses the run.

Recording the release as a resolved circuit-breaker entry outside `analyze`
would not help. Any resolved HALT is a provider-audit acknowledgement boundary,
so the release would silently acknowledge unrelated provider evidence.

## Decision

1. **A second release route.** The operator-only command
   `release-blocked-chain <task-id> --reason <r>` releases the cap for a
   `BLOCKED` task's current blocked episode. It refuses a task that is not
   `BLOCKED`, has no recorded episode, is below the cap, or whose episode is
   already released, and an identified agent session.
2. **The release lives in the task's history.** It appends a
   `blocked_recovery_released` entry with the masked reason, the episode
   (`blocked_at`), the lineage and its recoveries, and writes an activity-log
   line. The event is not a status transition, so the episode does not move.
   No status, mode, sprint or circuit-breaker state changes.
3. **One rule still decides.** `BlockedRecoveryReleased` also returns true when
   a `blocked_recovery_released` entry follows the task's latest
   status-transition event. Episode scope comes from history order, so the next
   transition (a re-block, an unblock, a failed fresh recovery) ends the
   release. A replacement retires the task, so the successor's next block is a
   new episode and is capped again. A task without a recorded episode is
   never released. Supersession, plan-output admission and detection still all
   call `BlockedRecoveryCapped`.
4. **The analyze/resume route is unchanged.** A released task is no longer
   reported by `blocked_replacement_chain`; other capped tasks still are.

## Consequences

- A human can permit one more replacement without pausing unrelated work; the
  orchestrator is woken for the task because its history changed.
- The human decision is still required, recorded with its reason, and bounded
  to one replacement of one episode.
- Like `resume`, the command trusts the absence of an agent identity in the
  environment; it is not stronger proof of a human.
- The release does not clear the task's other holds (`awaiting_human` ask,
  repair gate, dependencies).

## Alternatives Considered

- **A resolved circuit-breaker history entry:** rejected. It reuses the existing
  check unchanged, but it moves the provider-audit watermark.
- **`analyze` without pausing:** rejected. It would change HALT semantics for
  every pattern.
- **A per-lineage cap increase:** rejected. One release per episode keeps
  ADR-0171's one release, one replacement.

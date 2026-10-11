# 60 - Agent Execution Progress Watchdog

## Context and Problem Statement

Liza could show an executing task assigned to an agent while no useful work was progressing. In the observed failures, Codex-backed agents claimed tasks, created worktrees, read bootstrap context, and then stopped producing artifacts. Status showed stale or contradictory process state, while operators had to inspect worktrees, output files, and OS processes manually.

Heartbeat was not reliable enough as the progress signal. The root cause was understood later: agent process identity could drift. A CLI process could continue without a supervisor, and a supervisor could remain registered without a live CLI. Separately, a provider process could be alive but stuck, for example waiting for a permission approval while running headless.

Process liveness and heartbeat freshness therefore did not prove task progress.

## Considered Options

1. **Rely on heartbeat/process liveness** - simple, but it misses headless permission stalls and split supervisor/provider process failures.
2. **Add an execution-progress watchdog** - treat observable task progress as the signal, and block/recover stale assigned work when progress stops.

## Decision Outcome

Chose **Option 2**: add supervisor-side execution-progress detection for assigned executing tasks.

### Architecture

The watchdog treats the following as progress:

- task state changes
- task worktree `HEAD` changes
- task worktree status changes, including untracked files
- provider stdout/stderr activity

When an assigned executing task becomes stale:

1. The supervisor cancels the provider process.
2. It waits for the process to return.
3. If the task is still owned by the same agent, it transitions the task to `BLOCKED`.
4. It runs blocked-task worktree cleanup so worktree/branch invariants are preserved. *Amended by [ADR-0203](0203-supervisor-blocks-preserve-doer-work.md): the block keeps the worktree and branch and first saves the doer's work.*

Status output also includes process status source/detail fields to make stale assignment diagnostics less ambiguous.

### Rationale

The useful question is not "is something alive?" but "is the assigned task producing externally observable progress?" A live process can be stuck, and a fresh heartbeat can be disconnected from useful task movement.

Using worktree, task-state, and provider-output signals gives the supervisor evidence closer to the actual work. The tradeoff is that a long quiet task may be blocked conservatively. That is acceptable because the alternative is silent indefinite assignment with no artifact progress.

### Consequences

**Positive:**
- Stalled task executions become recoverable instead of lingering indefinitely.
- Operators get clearer diagnostics about why an assignment is considered stale.
- Worktree cleanup happens through the same recovery path instead of manual branch/worktree repair.

**Limitations accepted:**
- Progress detection is heuristic.
- Legitimate long-running silent work may need to emit output or accept being blocked for review.
- This does not by itself solve ghost agent identity drift; ADR-0062 addresses that root cause.

**Extends:** ADR-0053 (Supervisor Resilience) - adds progress-based execution recovery. ADR-0023 (Crash Recovery Commands) - automates a common recovery case before manual repair is needed.

---
*Reconstructed from commit 2bb71dad, GitHub issue #67, and liza-run-issues.md (2026-05-16)*

# 88. Cancelling an instance reconciles its open human tasks

- Status: Accepted
- Date: 2026-07-03

## Context

A `ProcessDefinition` may park a token at a `UserTask`, producing a
`humantask.HumanTask` record (state `Unclaimed`) in the `TaskStore`. That store is
the queryable projection the API/authz layer reads for an operator's inbox
(`ClaimableBy`, `AssignedTo`).

When an operator cancels a running instance via `ProcessDriver.CancelInstance`,
`handleCancelRequested` (engine) clears all live tokens and drives the instance to
`StatusTerminated`. Prior to this ADR it did **not** touch the human-task
projection: the parked task record stayed `Unclaimed` in the store. A terminated
instance therefore left an orphaned "open" task that still surfaced in inbox
queries — a candidate could claim work for a process that no longer exists.

The engine already had the mechanism for the fix: the deadline-breach path
(`handleDeadlineFired`, ADR-0064-era) marks a guarded task `humantask.Cancelled`
and emits an `UpdateTask` command, which the runtime performs against the
`TaskStore`. Cancellation simply never applied the same reconciliation. The
in-flight tasks are available on `InstanceState.Tasks`, and `HumanTask.IsOpen()`
already distinguishes open (`Unclaimed`/`Claimed`) from resolved
(`Completed`/`Cancelled`) states.

Terminal paths that clear tokens: `CancelRequested` (→ `StatusTerminated`), and
unhandled error (→ `StatusFailed`). A `KindTerminateEndEvent` is presently inert
(the token is parked, not consumed), so it is not a live orphaning path. This ADR
scopes the fix to **cancellation** (the reported bug); the `StatusFailed` path is
noted as a follow-up below.

## Decision

Add an unexported helper on `InstanceState`:

```go
func (s *InstanceState) cancelOpenTasks() []Command
```

It walks `s.Tasks` in slice order, and for every task where `IsOpen()` is true it
sets `State = humantask.Cancelled` and appends an `UpdateTask{Task: …}` command.
Already-resolved tasks are left untouched; order is deterministic.

`handleCancelRequested` calls it in both terminal branches:

1. **Immediate termination** (no compensation records): the `UpdateTask` commands
   are appended after the def-level/per-node cancel actions and before
   `FailInstance`, alongside the timer/arm cancellations.
2. **Cancel-with-compensation** (`beginCompensation` path): the `UpdateTask`
   commands are prepended before the compensation walk's commands, so a cancelled
   instance closes its parked tasks even though termination is deferred to the
   walk's end.

No public API changes. `UpdateTask`, `HumanTask.IsOpen`, and `humantask.Cancelled`
already exist; the runtime already performs `UpdateTask` against the `TaskStore`.

## Consequences

- **Positive.** A cancelled instance no longer leaves orphaned open tasks in an
  inbox query. Cancellation is now consistent with the deadline-breach path, which
  already produced `Cancelled` tasks. Behaviour is deterministic and covered by
  engine unit tests (single task, parallel tasks, no-task negative,
  cancel-with-compensation) plus a runtime end-to-end test asserting the store
  transition and inbox exclusion.
- **Neutral.** One extra `UpdateTask` command per open task at cancel time. For the
  common case (zero or one parked task) this is negligible; the command is only
  emitted for genuinely open tasks.
- **Follow-up (out of scope).** An instance that reaches `StatusFailed` via an
  unhandled error can likewise orphan a parked task on a parallel branch. The same
  `cancelOpenTasks` helper can be wired at that terminal point, but the reported
  bug and this ADR concern cancellation (`StatusTerminated`) only. Tracked as a
  future task rather than silently expanded here.

# 89. Failing an instance reconciles its open human tasks

- Status: Accepted
- Date: 2026-07-04

## Context

[ADR-0088](0088-cancel-reconciles-human-tasks.md) fixed orphaned human tasks on
**cancellation** (`StatusTerminated`) by adding `InstanceState.cancelOpenTasks`,
which marks every open task `Cancelled` and emits `UpdateTask`. It explicitly
deferred the sibling case: an instance reaching `StatusFailed` can orphan a task
the same way.

Concretely, a parallel fork can park one branch at a `UserTask` while another
branch faults the whole instance. Before this ADR the task record stayed
`Unclaimed` in the `TaskStore` and kept surfacing in inbox queries
(`ClaimableBy`/`AssignedTo`) for a process that had already failed.

There are three engine paths that transition an instance to `StatusFailed` while a
sibling task may still be open:

1. **Unhandled action error, no compensation** — `propagateError` in
   `engine/step_errors.go` sets `StatusFailed` and emits `FailInstance` directly.
2. **Unhandled error with compensation records** — `propagateError` starts a
   compensation walk (`beginCompensation`, `FinalStatus=StatusFailed`) that
   terminates later in `stepCompensationFinish` (`engine/step_compensation.go`).
   This same function is the terminal for the cancel-with-compensation walk
   (`FinalStatus=StatusTerminated`).
3. **Child failure fails the parent** — `handleSubInstanceFailed` in
   `engine/step_triggers.go` sets `StatusFailed` on a `SubInstanceFailed` trigger.

## Decision

Reuse the existing `InstanceState.cancelOpenTasks` helper (no new API) at each of
the three `StatusFailed` terminals:

1. `propagateError` immediate-failure branch — emit `cancelOpenTasks()` before
   `FailInstance`.
2. `stepCompensationFinish` — emit `cancelOpenTasks()` before `FailInstance`. This
   single choke point covers every compensation-terminated walk: error-with-comp
   (`Failed`) and, idempotently, cancel-with-comp (`Terminated`, whose tasks were
   already cancelled at trigger time by ADR-0088, so `cancelOpenTasks` is a
   no-op there).
3. `handleSubInstanceFailed` — emit `cancelOpenTasks()` after `FailInstance` (so
   `FailInstance` remains the first command, preserving existing assertions).

`cancelOpenTasks` only touches tasks for which `IsOpen()` is true, so all three
call sites are idempotent and safe to combine.

## Consequences

- **Positive.** An instance that fails — by unhandled error (with or without
  compensation) or by child-failure propagation — now closes its open human tasks
  instead of orphaning them. Failure is consistent with cancellation (ADR-0088)
  and with the deadline-breach path. Covered by engine unit tests for all three
  paths plus a runtime end-to-end test asserting the store transition and inbox
  exclusion.
- **Neutral.** One extra `UpdateTask` per open task at the failure terminal; only
  emitted for genuinely open tasks. The compensation-finish reconciliation runs on
  both the cancel and error walks, but is a no-op on the cancel walk.
- **Scope.** Together with ADR-0088 this closes human-task reconciliation for all
  current terminal transitions that clear tokens (`StatusTerminated`,
  `StatusFailed`). `StatusCompleted` never has open tasks (the instance only
  completes once all tokens are consumed). `KindTerminateEndEvent` remains inert
  (parks its token rather than terminating) and is not a live orphaning path.

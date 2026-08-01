# 161. A step drops commands whose awaiter it cancelled

- Status: Accepted
- Date: 2026-07-31

> ADR numbers 0155–0158 are reserved by parked branches and do not exist on
> `main`, so this ADR takes 0161, continuing from ADR-0160.
>
> Delivery 1 of 3. Deliveries 2 (ADR-0162, scope-lifecycle correctness) and 3
> (ADR-0158, broadcast signal fan-out) follow, in that order, and are drafted on
> `parked/scope-and-fanout-design`. Design:
> `docs/specs/2026-07-31-stale-command-filter.md`.

## Context

`drive` (`engine/step.go:157-224`) advances tokens in one pass, accumulating
every token's commands into a single slice. A token that parks records the
command it waits on by setting `tok.AwaitCommand`. A **later** token in the same
pass can then destroy that parked token, while its command stays in the
accumulator.

At least five destroyers exist:

- `forceTerminate` (`engine/step_nodes.go:478-504`) sets `c.s.Tokens = nil` at
  `:486` and returns `halt = true`; `drive` then returns the
  **already-accumulated** commands (`engine/step.go:199-204`).
- an interrupting boundary arm consumes its host and cancels the host's sibling
  arms (`cancelTokenWaits`, `engine/step_cancel.go:12-30`).
- an interrupting event-sub-process arm cancels its enclosing scope's tokens
  (`engine/step_eventsubprocess.go:189-207`).
- `propagateError`'s enclosing-scope teardown, called from the error-end-event
  strategy **inside** `drive` (`engine/step_nodes.go:169`), cancels every token
  in the erroring scope (`engine/step_errors.go:377-390`).
- `beginCompensation` cancels **all** in-flight tokens
  (`engine/step_compensation.go:218-226`), reached from the unhandled-error path
  (`engine/step_errors.go:236-242`) and the cancel path.

The count is not load-bearing — the filter reads the **surviving token set**, not
the destroyer, so a sixth destroyer is covered by construction. It is recorded
because the first draft claimed there were three, which understated how ordinary
this is.

The runtime performs the orphaned command post-commit: the action really runs and
its `ActionCompleted` resolves to `ErrTokenNotFound`; an `AwaitHuman` mints an
inbox task nothing can complete; a `StartSubInstance` starts an orphan child.

**This is not specific to any trigger kind and needs no arms at all.** A parallel
fork whose first branch parks on a `ServiceTask` and whose second branch reaches
a force-termination `EndEvent` produces the leak from a bare `StartInstance`.
Every handler that ends in `drive` — timer, action-completed, human-completed,
message, signal, incident-resolution, compensation resume — can produce it.

## Decision

The filter runs **once in `Step`**, on the returned result, after `dispatch` and
the id-error check and before the transient-seam scrub
(`engine/step.go:96-101`):

```go
res.Commands = dropStaleTokenCommands(&res.State, res.Commands)
```

It operates on `&res.State`, not on the working clone. Every handler builds its
own `StepResult{State: *s, …}` literal, which shallow-copies the struct; a filter
mutating the clone would work only by backing-array sharing, and every analogous
mutator in this package (`removeToken`, `removeTimer`, `cancelTimersWhere`,
`removeArmsWhere`) is written as rebuild-and-reassign, so an implementation in
house style would silently stop being visible in the returned state.

### The live-awaiter set

The set is **empty when `s.Status.IsTerminal()`**. Otherwise it is:

- `tok.AwaitCommand` for every token remaining in `s.Tokens`, ignoring empty
  values;
- `s.Compensating.ActiveCmdID` when non-empty.

**The terminal exclusion covers the whole set, not just the cursor.** An earlier
draft scoped it to the cursor alone, which left the filter a **no-op on the most
common failure path**: `handleUnhandledError`'s immediate-fail branch
(`engine/step_errors.go:244-255`) sets `StatusFailed`, stamps `EndedAt` and
reconciles tasks and timers — but never clears `s.Tokens`. Every sibling
therefore survived with its `AwaitCommand` intact, was re-admitted to the live
set, and kept its command. All three symptoms above reproduce verbatim from a
bare `StartInstance` on a fork whose second branch reaches an unhandled error end
event; `TestStaleCommandFilterDropsOnUnhandledError` is the regression test.

The justification is the one the cursor exclusion already rested on: a terminal
instance can never consume a resumption trigger, so nothing it still holds is a
live awaiter. It cannot over-drop, because a plain `EndEvent` does not terminalise
the instance while a sibling is still parked, so no live command is ever emitted
alongside a terminal status. The only non-fire-and-forget `InvokeAction`
reachable on a terminal step is the compensation invoke, which the same rule
excludes deliberately.

The compensation cursor is not optional. `startCompensationWalk`
(`engine/step_nodes.go:982-993`) consumes the throw token at `:983`, mints a
command id, sets `cursor.ActiveCmdID` at `:989` and appends `compensationInvoke`
at `:991` — a **non**-fire-and-forget `InvokeAction` whose token has just been
destroyed by design. `compensationInvoke`
(`engine/step_compensation.go:324-330`) is also reached from
`stepCompensationAdvance`. The walk correlates through
`s.Compensating.ActiveCmdID` (`engine/state_compensation.go:118`, consumed at
`engine/step_triggers.go:84`). A token-only set would drop that command and hang
every compensation walk.

Every `beginCompensation` call site sets `s.Status = StatusCompensating`
immediately before calling it (`engine/step_compensation.go:137,709`,
`engine/step_errors.go:237`, `engine/step_triggers.go:203`). That is the single
assumption which, if a future caller violates it, silently hangs the walk — so it
is written down here rather than left implicit.

The terminal exclusion closes the mirror-image hole: a step that starts a
compensation walk and then force-terminates would otherwise keep the compensation
`InvokeAction` alive for a terminated instance, whose `ActionCompleted` would land
on a non-`StatusCompensating` state and fail.

That the cursor **survives** a terminal transition at all is a **separate,
pre-existing defect**, not a property this ADR endorses. `forceTerminate`
(`engine/step_nodes.go:478-504`), `handleUnhandledError`
(`engine/step_errors.go:246`), `handleSubInstanceFailed`
(`engine/step_triggers.go:830`) and `handleCancelRequested`'s immediate tail all
set a terminal status without clearing `s.Compensating` — which contradicts the
invariant asserted in `beginCompensation`'s own comment
(`engine/step_compensation.go:300-303`) and lets a later plain
`CompensateRequested` **resurrect a force-terminated instance** at a stale
`ResumeNode`, because the terminal guard at `:131` is scoped strictly to
`t.ReverseNode != ""`. That defect is queued in `docs/plans/HANDOVER.md`. The
exclusion here is defensive and stays correct after it is fixed — at which point
the predicate becomes redundant rather than wrong.

### What is filtered

| command | dropped when |
|---|---|
| `InvokeAction` with `FireAndForget == false` | `CommandID` is **non-empty** and not in the live-awaiter set |
| `AwaitHuman` | `TaskID` is **non-empty** and not in the live-awaiter set |
| `StartSubInstance` | `CommandID` is **non-empty** and not in the live-awaiter set |

The non-empty condition matters. `IDGenerator` is a consumer-supplied interface
(`engine/idgen.go:24-26`) and `("", nil)` is a legal return, so a misbehaving
generator can produce a command with an empty id whose token is genuinely parked
on `AwaitCommand == ""`. Today that fails loudly — the action runs and
`ActionCompleted{""}` returns `ErrTokenNotFound`, because `tokenAwaiting` guards
the empty key (`engine/step_state.go:81`, ADR-0152). A filter that dropped it
would replace a loud failure with a **permanently parked instance and no error at
all**. A malformed id is not this filter's problem.

Every other command kind passes through untouched. Two exclusions are deliberate:

- `InvokeAction` with `FireAndForget == true` has no awaiter *by design*
  (`engine/command.go:97-104`) — filtering it would delete every boundary and
  deadline action.
- **`ScheduleTimer` is left alone because no timer id is an awaiter key at all.**
  The four record-backed kinds park their host on a task id or command id, never
  on the timer id: the `UserTask` deadline (`engine/step_nodes.go:643`) and
  reminder (`:570`), `armBoundaries` (`engine/step_boundaries.go:54`) and
  `armEventTriggeredSubprocesses` (`engine/step_eventsubprocess.go:97`) all emit a
  `ScheduleTimer` whose `TimerID` is **never** any token's `AwaitCommand`. A
  `TimerID ∈ liveSet` rule would therefore drop every deadline, reminder,
  boundary and event-sub-process timer on every step. An event-gateway arm's
  timer is the same shape — it correlates through `s.ArmedEvents`, not through
  the gateway token's `evtgw:` sentinel — so even a narrower
  `Kind == TimerIntermediate` rule would mass-drop gateway arms.

  A consequence, recorded so it is not mistaken for an oversight: a token parked
  at an intermediate timer catch does have `tok.AwaitCommand = timerID`
  (`engine/step_nodes.go:700-709`) but **no** `timerRecord`, so when it is
  cancelled `cancelTokenWaits` emits no `CancelTimer` and the durable job is
  orphaned until it fires into `handleTimerFired`'s stale-timer no-op
  (`engine/step_triggers.go:487-496`). The fix is to register intermediate-catch
  timers in `s.Timers` so the existing sweeps find them — **not** to filter the
  command. Out of scope here.

Three further sites set `tok.AwaitCommand` to a value that is **not** a command
id: `engine/step_nodes.go:709` and `engine/step_triggers.go:304` (a
`ScheduleTimer` id) and `engine/step_nodes.go:838` (the `evtgw:` sentinel). They
enter the live set harmlessly. The fallback generator prefixes ids per kind
(`engine/step_state.go:154-162`) and an injected `IDGenerator` returns globally
unique ids verbatim without a prefix (`engine/idgen.go:50-58`), so a collision is
impossible in either configuration — and a collision could only cause
**under**-filtering, never a dropped live command.

### A dropped `AwaitHuman` cancels its task record

When the record is still open, it is marked `humantask.Cancelled` through the
existing `TaskByID` accessor (`engine/state.go:279`) **and an `UpdateTask` is
emitted for it**. The `IsOpen()` guard is required: an already-`Completed` or
already-`Cancelled` record must not be overwritten, and a record cancelled moments
earlier by `cancelOpenTasks` on a terminal path must not produce a second
`UpdateTask`.

Emitting `UpdateTask` is what every other cancel site in the engine does —
`handleDeadlineFired` (`engine/step_timers.go:88-91`) and `cancelOpenTasks`
(`engine/state.go:297-306`) both mark `Cancelled` and emit one. An earlier draft
of this ADR omitted it, reasoning that the store was never told the task existed.
That would have made this the **first** site in the codebase where an engine-side
`Cancelled` record has no store row: `service.ProcessInstance` would render a
`tasks[]` entry, and `history[].task_id` would link to it (ADR-0145), for a task
id `TaskService.Get` answers with `ErrTaskNotFound`. `performUpdateTask` is an
idempotent upsert (`runtime/processdriver_action.go:473-481`) over a record the
engine already stamped with `InstanceID`, `NodeID`, `CreatedAt` and `Vars`
(`engine/step_nodes.go:599-615`), so the row it writes is complete.

The record is **not** removed. `userTaskStrategy` calls `s.setVisitTask`
(`engine/step_nodes.go:619`) and the JSON projection emits `history[].task_id`
documented as linking a user-task visit to its tasks entry
(`service/instance.go:154-156`, ADR-0145), so deleting it would dangle that link
inside the same document. Cancelling is also sufficient: both open-task
projections filter on `IsOpen()` (`service/instance.go:75`,
`runtime/view/instance_actionable.go:65`), which was the motivation (ADR-0142).

The record is **deep-copied** into the command (`HumanTask.Clone`) rather than
shallow-copied. The command escapes into a consumer-supplied `TaskStore` while the
record it was built from is committed as instance state, so a shallow copy would
share the `Claim`/`Completion` pointees, the `Vars` map and the candidate/eligibility
slices across that boundary. Both in-repo stores happen to copy on ingest
(`humantask/memory.go:35`, and the SQL store marshals to JSON), so the hazard is
latent — but `TaskStore` is public API, and `performAwaitHuman` already guards the
same seam on the entry path (`runtime/processdriver_action.go:449-461`). The
identical shallow copy in `cancelOpenTasks` (`engine/state.go:302`) predates this
ADR and is queued rather than changed here.

This makes the delivery state-mutating rather than a pure command filter, and it
covers only the **same-step** half of the invariant. `cancelTokenWaits` never
touches `s.Tasks`, so a token parked on a `UserTask` in an *earlier* step and
cancelled later still leaves an open record. Until ADR-0162 closes that,
`s.Tasks` correctness depends on which step cancelled the token.

### Every drop emits a Warn record

A dropped command leaves **no other trace anywhere**: no error is returned, no
event is published, no history entry is written, and the runtime never learns the
command existed. Without a log, an operator asking "why was this customer never
refunded?" has nothing to work from, and a live-awaiter-set regression (the
failure mode named under Consequences) would be invisible in production.

Each drop therefore emits one `slog.WarnContext` record — `instance_id`,
`command_kind`, `correlation_id` — through the `ctx` that `Step`'s doc comment
already reserves for exactly this class of site (ADR-0129); `drive` uses the same
convention for its missing-node park (`engine/step.go:187`). Warn, not Debug: a
drop means a step both emitted a parking command and destroyed its awaiter, which
is anomalous even though the filter's response to it is correct. The pre-scan
keeps the logger untouched on deliveries that drop nothing.

## Consequences

**Positive.**

- No step runs actions, mints human tasks, or starts child instances on behalf of
  tokens it cancelled in the same step — for every trigger kind.
- One filter, one call site: a new handler or a new destroyer inherits the
  behaviour rather than having to re-establish it.
- State and commands agree: a dropped `AwaitHuman` leaves a `Cancelled` record,
  visible in history, absent from `ActiveTasks`.

**Negative / accepted costs.**

- **Breaking.** `StepResult.Commands` can contain fewer entries than before for
  the same input, and `InstanceState.Tasks` can hold a `Cancelled` record where
  it previously held an open one. No exported signature changes, but a consumer
  asserting on the command stream or on task state will see the difference.
- One extra pass over the command slice and a map of live awaiters on every
  `Step`. The slice is typically 0–3 entries; the map is proportional to token
  count.
- **The filter is only as correct as the live-awaiter set.** Any future command
  that parks an awaiter through a mechanism other than `tok.AwaitCommand` or the
  compensation cursor must be added, or it will be silently dropped. The set is
  built in one function with both sources named explicitly, a test asserts a
  compensation `InvokeAction` survives, and every drop is logged — so such a
  regression shows up as a flood of Warn records rather than as silence.
- Not fixed here, and recorded as known gaps: the orphaned intermediate-catch
  `ScheduleTimer` (above); and an `AwaitHuman` emitted in an **earlier** `Step`
  whose host is later cancelled, which stays open because `cancelTokenWaits` never
  touches `s.Tasks`. Delivery 2 (ADR-0162) touches `cancelTokenWaits` and is the
  right home for the latter.
- **The task fix is therefore asymmetric across step boundaries, and that is not
  intentional design — it is a scope line.** A scope teardown that cancels a
  `UserTask` host parked in the *same* step now leaves a `Cancelled` record and an
  `UpdateTask`; the identical topology spread over *two* steps leaves the record
  `Unclaimed` and open on a completed instance, with no `UpdateTask` at all,
  because `propagateError`'s `consume` closure (`engine/step_errors.go:377-395`)
  has no task reconciliation. Queued in `docs/plans/HANDOVER.md` with the
  reproduction; the fix belongs where the token dies, not where the command is
  filtered.

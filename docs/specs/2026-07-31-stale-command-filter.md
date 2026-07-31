# Stale-command filtering

- Date: 2026-07-31
- Status: design agreed; ADR-0161
- Scope: `engine/` only. No new port, no storage, no transport, no public API
  **signature** change.

Delivery **1 of 3** in the sequence that came out of auditing the parked
signal/message bundle's engine-pure half. The three are ordered so each lands on
a clean base:

1. **this delivery — ADR-0161**, stale-command filtering;
2. ADR-0162, scope-lifecycle correctness (subtree teardown, the sub-process drain
   check, zombie scopes, orphaned tasks and incidents);
3. ADR-0158, a broadcast signal fires every matching arm per family.

The ordering is deliberate and is the opposite of how the work was first
packaged. ADR-0158 is an **amplifier** of the defects the first two fix, so
shipping it last makes "does not ship an amplified defect" true by construction
rather than by co-commit. Drafts for 2 and 3 are parked on
`parked/scope-and-fanout-design`.

## 1. Problem

`drive` (`engine/step.go:157-224`) advances tokens in one pass, accumulating
every token's commands into a single slice. A token that parks records what it
waits on by setting `tok.AwaitCommand`. A **later** token in the same pass can
then destroy that parked token while its command stays in the accumulator.

Three destroyers exist today:

- `forceTerminate` (`engine/step_nodes.go:478-504`) sets `c.s.Tokens = nil` at
  `:486` and returns `halt = true`; `drive` returns the **already-accumulated**
  commands (`engine/step.go:199-204`).
- an interrupting boundary arm consumes its host and cancels the host's sibling
  arms (`cancelTokenWaits`, `engine/step_cancel.go:12-30`).
- an interrupting event-sub-process arm cancels the tokens of its enclosing
  scope (`engine/step_eventsubprocess.go:189-207`).

The runtime then performs the orphaned command post-commit:

| command | consequence |
|---|---|
| `InvokeAction` (not fire-and-forget) | the action really runs; its `ActionCompleted` resolves to `ErrTokenNotFound` |
| `AwaitHuman` | an inbox task is minted that nothing can ever complete |
| `StartSubInstance` | an orphan child instance starts, whose completion has no parent token to resume |

**The minimal reproduction involves no arms and no signal.** A parallel fork
whose first branch parks on a `ServiceTask` and whose second branch reaches a
force-termination `EndEvent` produces the leak from a bare `StartInstance`.
Every handler that ends in `drive` can produce it.

An earlier draft scoped the fix to `handleSignalReceived`, reasoning that no
other handler fires more than one arm. Three independent adversarial audits
disproved it: the staleness comes from `drive` advancing multiple **tokens**, not
from dispatching multiple **arms**.

## 2. Options considered

| option | verdict |
|---|---|
| **A. Accept and document** | Rejected. The actions genuinely run; this is not cosmetic. |
| **B. Filter the accumulated commands against the surviving awaiters, once in `Step`** | **Chosen.** One pass over a slice that is typically 0–3 entries, on a path every trigger already takes. |
| **C. Filter inside each handler** | Rejected. Roughly sixty `StepResult{State: *s, …}` construction sites; the invariant would have to be re-established at each and would rot at the first new handler. |
| **D. Have each destroyer retract its own commands** | Rejected. `forceTerminate` and `cancelTokenWaits` do not have the accumulator in scope — `drive` owns it — so this needs threading a retraction channel through every strategy. |

## 3. Design

### 3.1 Where

Once in `Step`, on the returned result, after `dispatch` and the id-error check
and before the seam scrub (`engine/step.go:96-101`):

```go
res.Commands = dropStaleTokenCommands(&res.State, res.Commands)
```

Operating on `res` rather than on the working clone `sp` matters. Every handler
builds its own `StepResult{State: *s, …}` literal, which **shallow-copies** the
struct — so a filter that mutated `sp` would rely on `res.State.Tasks` still
sharing `sp.Tasks`' backing array. Every analogous mutator in this package
(`removeToken`, `removeTimer`, `cancelTimersWhere`, `removeArmsWhere`) is written
as rebuild-and-reassign, so an implementation following house style would
silently stop being visible in the returned state. Taking `&res.State` removes
the hazard entirely.

### 3.2 The live-awaiter set

- `tok.AwaitCommand` for every token remaining in `s.Tokens`, ignoring empty
  values;
- `s.Compensating.ActiveCmdID`, when non-empty **and the instance is not
  terminal**.

The compensation cursor is load-bearing and non-obvious. `startCompensationWalk`
(`engine/step_nodes.go:982-993`) consumes the throw token at `:983`, then sets
`cursor.ActiveCmdID` at `:989` and appends `compensationInvoke(...)` at `:991` —
a **non**-fire-and-forget `InvokeAction` whose token has just been destroyed by
design. `compensationInvoke` (`engine/step_compensation.go:323-329`) is also
reached from `stepCompensationAdvance`. The walk correlates through
`s.Compensating.ActiveCmdID` (`engine/state_compensation.go:118`, consumed at
`engine/step_triggers.go:84`). A filter built on the token set alone would drop
that command and hang every compensation walk. **This is the single
highest-risk detail in the delivery.**

The terminal exclusion closes the mirror-image hole: nothing clears the cursor on
a terminal transition, so a step that both starts a compensation walk and then
force-terminates would otherwise keep a compensation `InvokeAction` alive for a
terminated instance, whose `ActionCompleted` lands on a non-`StatusCompensating`
state and fails.

### 3.3 What is filtered

| command | dropped when |
|---|---|
| `InvokeAction` with `FireAndForget == false` | `CommandID` not in the live set |
| `AwaitHuman` | `TaskID` not in the live set |
| `StartSubInstance` | `CommandID` not in the live set |

All other kinds pass through. Two exclusions are deliberate:

- `InvokeAction` with `FireAndForget == true` has no awaiter *by design*
  (`engine/command.go:97-104`); filtering it would delete every boundary and
  deadline action.
- `ScheduleTimer` is left alone. For the four **record-backed** timer kinds
  (deadline `engine/step_nodes.go:649`, reminder `:576`, retry
  `engine/step_triggers.go:292`, boundary arms) `cancelTokenWaits` emits the
  matching `CancelTimer` and the runtime's cancel is idempotent. It is **not**
  self-correcting for a token parked at an intermediate timer catch:
  `intermediateCatchEventStrategy` (`engine/step_nodes.go:700-709`) emits
  `ScheduleTimer` and sets `tok.AwaitCommand = timerID` but appends **no**
  `timerRecord`, so no `CancelTimer` is produced and the job survives until it
  fires into `handleTimerFired`'s stale-timer no-op
  (`engine/step_triggers.go:487-496`). Dropping the `ScheduleTimer` would not fix
  that and would emit a `CancelTimer` for a job that was never created.

Seven sites assign `tok.AwaitCommand`, not the four that park a filtered
command. The other three store a non-command value —
`engine/step_nodes.go:709` and `engine/step_triggers.go:304` (a `ScheduleTimer`
id) and `engine/step_nodes.go:838` (the `evtgw:` sentinel). They enter the live
set harmlessly: the fallback generator prefixes ids per kind
(`engine/step_state.go:154-162`) and an injected `IDGenerator` returns globally
unique ids verbatim (`engine/idgen.go:50-58`), so a collision is impossible in
either configuration — and a collision could only ever cause **under**-filtering,
never a dropped live command.

### 3.4 A dropped `AwaitHuman` cancels its task record

The record in `s.Tasks` is marked `humantask.Cancelled` using the existing
`TaskByID` idiom (`engine/state.go:279`, used this way at
`engine/step_timers.go:38,88-89`). It is **not** removed: `userTaskStrategy`
calls `s.setVisitTask` (`engine/step_nodes.go:619`) and the JSON projection emits
`history[].task_id` as a documented link to the tasks entry
(`service/instance.go:154-156`, ADR-0145), so deletion would dangle that link.
Cancelling is also sufficient — both open-task projections filter on `IsOpen()`
(`service/instance.go:75`, `runtime/view/instance_actionable.go:65`), which was
the motivation (ADR-0142).

The filter itself emits no `UpdateTask`. A sibling terminal path may still emit
one for the same record — `cancelOpenTasks` (`engine/state.go:297-306`) does,
and this ADR never filters `UpdateTask` — and that is consistent, because both
write `Cancelled` and `performUpdateTask` is an idempotent upsert.

## 4. What this delivery does NOT do

- **No fix for an `AwaitHuman` emitted in an earlier `Step`** whose host is later
  cancelled. `cancelTokenWaits` never touches `s.Tasks`, so that record stays
  open. Delivery 2 touches `cancelTokenWaits` and is the right home for it.
- **No fix for the orphaned intermediate-catch `ScheduleTimer`** (§3.3).
- No change to arm dispatch, scope teardown, or any trigger's semantics.
- No change to which commands the runtime performs, beyond not performing ones
  whose awaiter is gone.

## 5. Consequences

**Breaking.** `StepResult.Commands` can now contain fewer entries than before for
the same input, and `InstanceState.Tasks` can contain a `Cancelled` record where
it previously held an open one. No exported signature changes.

The filter is only as correct as the live-awaiter set: a future command that
parks an awaiter through a mechanism other than `tok.AwaitCommand` or the
compensation cursor would be silently dropped. The set is therefore built in one
function with both sources named, and a test asserts a compensation
`InvokeAction` survives.

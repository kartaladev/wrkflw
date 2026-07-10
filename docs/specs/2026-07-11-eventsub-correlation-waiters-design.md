# Event Sub-process Correlation Waiters — Design

**Date:** 2026-07-11
**Status:** Approved (autonomous SDD run)
**Related ADRs:** ADR-0121 (event-based start), ADR-0122 (remove EventSubProcess kind)
**New ADR:** ADR-0123 (this change)

## Context

An *event sub-process* (ADR-0122) is an `activity.SubProcess` whose inner start is
event-triggered (signal / timer / message). While a scope is open, the engine records an
`eventTriggeredSubprocessArm` per event sub-process (`engine/step_eventsubprocess.go`,
`armEventTriggeredSubprocesses`). When the matching trigger arrives, the engine already
dispatches correctly:

- `handleMessageReceived` → `eventTriggeredSubprocessArmByMessage` → `fireEventTriggeredSubprocessArm`
- `handleSignalReceived`  → `eventTriggeredSubprocessArmBySignal`  → `fireEventTriggeredSubprocessArm`
- `handleTimerFired`      → `eventTriggeredSubprocessArmByTimer`   → `fireEventTriggeredSubprocessArm`

The engine fire path is complete and tested. The defect is entirely in the **runtime**, in how
it decides *which delivered message/signal reaches which parked instance*.

### The systemic gap

The runtime reconciles "what is this instance waiting for?" after every `deliverLoop` save, in
`runtime/processdriver_waiters.go`:

- `syncMsgWaiters(st)` populates the `msgWaiters` map from three sources:
  1. token-parked awaits (`tok.AwaitMessage`),
  2. `st.MessageBoundaryWaiters()` (armed message boundaries),
  3. `st.MessageArmedEventWaiters()` (armed message arms of event-based gateways).
- `syncSignalBus(st)` subscribes the instance in the `SignalBus` from **one** source:
  token-parked awaits (`tok.AwaitSignal`).

Event sub-process arms are **non-token-parked** (the arm lives in
`s.EventTriggeredSubprocesses`, no token carries `AwaitMessage`/`AwaitSignal`), just like
boundaries and gateway arms. But unlike those two, event-sub arms were **never added to either
reconciliation**. The consequence is two silent no-ops:

| Event-sub trigger | Delivery entry point | Reconciliation source | Result |
|---|---|---|---|
| **Message** | `ProcessDriver.DeliverMessage` | `syncMsgWaiters` (omits arm) | 🔴 silent no-op |
| **Signal**  | `ProcessDriver.BroadcastSignal` → `SignalBus.Publish` | `syncSignalBus` (omits arm) | 🔴 silent no-op |
| **Timer**   | `ScheduleTimer` command → scheduler → `TimerFired` | not correlation-based | 🟢 works |

The message case was documented as a follow-up during ADR-0122. The **signal case is the exact
same bug** and was not previously identified: `SignalBus.Publish` only delivers to instances in
`b.waiters[name]`, and that set is populated solely from `tok.AwaitSignal`. Timer arms are
unaffected because they flow through the scheduler/command mechanism, not the correlation tables.

### Root cause (why this recurred)

"What is this instance waiting for?" is knowledge that lives in the engine's state but is
**re-derived construct-by-construct inside the runtime**. Each new non-token construct
(boundary → gateway arm → event-sub arm) requires the runtime author to remember to add another
enumeration line to `syncMsgWaiters`/`syncSignalBus`. Event-sub arms were forgotten. This is a
cohesion/coupling defect, not just a missing line.

## Decision

Fix the correctness bug **and** remove the class of defect, by making the engine the single
authority on what an instance awaits.

### 1. Engine — granular event-sub accessors (mirror the existing granular accessors)

On `*engine.InstanceState`, add, next to `MessageBoundaryWaiters`/`MessageArmedEventWaiters`:

```go
// MessageEventSubprocessWaiters returns the (name, key) pairs for every armed
// MESSAGE-triggered event sub-process arm. Deterministic slice order; nil when none.
func (s *InstanceState) MessageEventSubprocessWaiters() []MessageWaiter

// SignalEventSubprocessNames returns the signal names of every armed
// SIGNAL-triggered event sub-process arm. Deterministic slice order; nil when none.
func (s *InstanceState) SignalEventSubprocessNames() []string
```

Both iterate `s.EventTriggeredSubprocesses` in slice order (already deterministic), selecting
entries whose `Message` / `Signal` field is non-empty. Timer arms contribute nothing.

### 2. Engine — unified authority accessors (the simplification)

On `*engine.InstanceState`, add the two queries the runtime actually needs:

```go
// MessageWaiters returns EVERY (name, key) pair the instance can be woken by a
// message: token AwaitMessage, message boundaries, event-based-gateway message
// arms, and message-triggered event sub-processes. This is the single authority
// a runtime mirrors; adding a future message construct extends only this method.
func (s *InstanceState) MessageWaiters() []MessageWaiter

// SignalWaiters returns EVERY signal name the instance can be woken by: token
// AwaitSignal and signal-triggered event sub-processes. Single authority for a
// runtime's SignalBus subscription set.
func (s *InstanceState) SignalWaiters() []string
```

`MessageWaiters` composes the token scan + the three existing granular accessors + the new
`MessageEventSubprocessWaiters`, in a fixed, documented order (tokens, boundaries, gateway arms,
event-subs). `SignalWaiters` composes the token `AwaitSignal` scan + `SignalEventSubprocessNames`.
The granular accessors are retained (public, individually tested building blocks); the union is
the one call a runtime makes.

Rationale: the token scan for `AwaitMessage`/`AwaitSignal` currently lives in the *runtime*.
Moving it into these union methods puts all await-derivation in the engine, where the token state
lives — the runtime no longer reaches into `st.Tokens` or knows any construct type.

### 3. Runtime — mirror the authority

`runtime/processdriver_waiters.go`:

- `syncMsgWaiters` iterates a single source, `st.MessageWaiters()`, to populate `msgWaiters`.
- `syncSignalBus` builds its `awaiting` list from `st.SignalWaiters()`.

The runtime keeps ownership of the *mechanism* (the `msgWaiters` map + `msgMu`; the `SignalBus`
subscription lifecycle), but no longer owns the *policy* of which constructs count. Coupling to
engine internals (boundaries, gateway arms, event-subs, token fields) is eliminated.

No engine fire-path change is needed — `handleMessageReceived`/`handleSignalReceived` already
dispatch to `fireEventTriggeredSubprocessArm`.

### 4. Example — remove the workaround

`examples/scenarios/event_subprocess/main.go` currently fires the "cancel" event-sub via
`driver.ApplyTrigger` with an explanatory note that `DeliverMessage` doesn't reach event-sub
arms. Replace it with `driver.DeliverMessage(...)` and update the note. This proves the fix at the
library-consumer level and keeps the example honest.

## Rejected alternatives

These were considered under the "maximum effect on correctness, consistency, performance"
directive and rejected because each *violates* at least one of those criteria.

### Rejected — make non-interrupting event-subs re-armable (repeat firing)

Non-interrupting event-subs are one-shot today: `fireEventTriggeredSubprocessArm` removes the arm
and never re-adds it. Making them repeatable would **break consistency**: non-interrupting
*boundary* events are deliberately, documentedly one-shot as well
(`engine/step_boundaries.go`: *"fired once; do not re-arm — repeating out of scope"*). Diverging
event-subs from boundaries would reintroduce exactly the kind of inconsistency this project keeps
paying down. Repeatable non-interrupting events, if ever wanted, are a **project-wide** decision
covering boundaries *and* event-subs together — its own ADR. Filed as follow-up (A).

### Rejected — multi-instance message fan-out

Changing `msgWaiters` from `map[(name,key)]→instanceID` to a multi-value map so one message wakes
many instances would **break correctness**: BPMN messages are point-to-point (correlated to a
single instance); broadcast-to-many is the *signal* model, which the `SignalBus` already
implements (`b.waiters[name]` is a set). Once signal event-sub arms are registered (this change),
signal fan-out to multiple instances works correctly and for free. Forcing messages to fan out
would contradict BPMN and blur message-vs-signal semantics, at a cross-cutting cost to every
message construct. The narrow real edge — two instances colliding on an identical non-empty
`(name, key)` in the single-value map — is a **pre-existing, all-message-constructs** limitation,
appropriately handled by validation/docs rather than a delivery-model rewrite. Filed as
follow-up (B).

## Quality attributes

- **Correctness:** closes two silent-no-op delivery bugs (message + signal event-sub).
- **Consistency:** event-subs are now reconciled identically to every other non-token construct;
  message stays point-to-point, signal stays broadcast, in line with BPMN.
- **Modifiability / cohesion:** await-derivation is centralized in the engine; a future message
  construct extends one method (`MessageWaiters`) instead of every runtime sync site.
- **Coupling:** the runtime no longer enumerates construct types or reads `st.Tokens`.
- **Performance:** one bounded slice scan per `deliverLoop` sync (same order as today's scans);
  no new allocations on the hot delivery path. Negligible.

## Testing strategy

TDD, observable RED before each GREEN.

- **Engine unit (`engine/state_test.go` or peer):** table tests for
  `MessageEventSubprocessWaiters`, `SignalEventSubprocessNames`, `MessageWaiters`,
  `SignalWaiters` — empty, single-construct, and mixed-construct states; assert deterministic
  order and that timer arms contribute nothing.
- **Runtime e2e (`runtime/..._test.go`, black-box `runtime_test`):**
  - `DeliverMessage` wakes a **message-triggered** event-sub arm (interrupting: main scope
    cancelled + event-sub drains; non-interrupting: spawns alongside).
  - `BroadcastSignal` wakes a **signal-triggered** event-sub arm (interrupting + non-interrupting).
  - Regression: existing message-boundary / event-gateway correlation e2e tests stay green
    through the refactored single-source `syncMsgWaiters`.
- **Example:** `examples/scenarios/event_subprocess` builds and runs via `DeliverMessage`.

Coverage target ≥ 85% on touched packages; `go test ./...` clean; `golangci-lint run ./...` clean.

## Follow-ups (filed, out of scope)

- **(A)** Repeatable non-interrupting events — project-wide ADR spanning boundaries + event-subs.
- **(B)** `msgWaiters` identical-`(name,key)` collision across instances — validation/doc
  hardening for all message constructs (not event-sub specific).

## ADR

Record as ADR-0123 (Nygard template): Context = the systemic non-token reconciliation gap;
Decision = engine-authority unified `MessageWaiters`/`SignalWaiters` + granular event-sub
accessors + runtime mirrors them; Consequences = two bugs closed, class of defect removed,
follow-ups (A)/(B) deferred with rationale.

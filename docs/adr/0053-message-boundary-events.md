# 53. Arm and fire message boundary events

- Status: Accepted
- Date: 2026-06-25

## Context

The model has supported message boundary events for some time: `model.BoundaryEvent`
carries `MessageName` and `CorrelationKey`, `model.WithBoundaryMessage(msg, key)`
constructs one, and `model.Validate` accepts a message boundary attached to any
activity host. But the engine never honoured them — a production-readiness audit
found that a message boundary **validates clean yet silently does nothing**:

- `engine.boundaryArm` had fields for timer and signal arms but **no Message
  field** (unlike `eventSubprocessArm`, which already carried `Message` /
  `MessageKey`).
- `armBoundaries` handled `TimerDuration` (→ `ScheduleTimer`) and `SignalName`
  (→ signal arm) but let `MessageName` **fall through silently**: the arm was
  appended with no message trigger recorded.
- `handleMessageReceived` dispatched a delivered message to event-gateway arms,
  event sub-process arms, and standalone parked-message tokens — but **never to a
  boundary arm**, so even a correctly-recorded message boundary could not fire.

A process author who attached a message boundary therefore got a no-op: the host
activity could never be interrupted (or augmented, for the non-interrupting case)
by an incoming message.

## Decision

Wire message boundary events end-to-end, mirroring the existing **signal**
boundary path exactly and reusing the shared boundary-fire machinery
(`fireBoundaryArm`). The change is additive arming plus one new fire-match branch;
`engine.Step` keeps the same `(state, commands)` shape and stays pure and
deterministic.

- **`engine.boundaryArm` gains `Message` and `MessageKey` fields** (`state.go`),
  matching the `eventSubprocessArm` pattern. The arm is a flat value struct, so
  `cloneState` (which copies `Boundaries` via `append([]boundaryArm(nil), …)`)
  needs no change.
- **`armBoundaries` arms message boundaries** (`step_boundaries.go`): when a
  boundary node has a non-empty `MessageName` (and no timer/signal), it resolves
  the correlation key with `conditions.EvalString(n.CorrelationKey, s.Variables)`
  — the same evaluator the receive-task / event-sub-process message paths use —
  and records `arm.Message` / `arm.MessageKey`. A bad correlation expression is
  returned as a wrapped `workflow-engine:` error, consistent with the timer path.
  No `ScheduleTimer` is emitted for a message boundary.
- **`handleMessageReceived` fires message boundaries** (`step_triggers.go`): a new
  dispatch branch (gateway → **boundary** → event-sub-process → standalone, the
  same order as the signal handler) matches an armed boundary by message name and
  correlation key via the new `boundaryArmByMessage` lookup, then calls
  `fireBoundaryArm`. Interrupting boundaries cancel the host and route the
  boundary's outgoing flow; non-interrupting boundaries spawn an additional token
  while leaving the host parked. Message delivery is point-to-point, so the branch
  returns on first match (unlike the broadcast signal handler).
- **Correlation key**: message boundaries support a correlation key, evaluated at
  arm time from `CorrelationKey`. An empty key matches on message name alone. A
  delivered message fires the boundary only when both name and resolved key match.
- **Non-interrupting** is supported (it is modelled on `BoundaryEvent` via
  `NonInterrupting` / `model.BoundaryNonInterrupting()`) and reuses the existing
  `fireBoundaryArm` non-interrupting path with zero new firing code.
- **Runtime correlation** (`runtime/runner.go`): `syncMsgWaiters` registered only
  `Token.AwaitMessage` waiters, but a message-boundary host parks on a
  task/command, not on the message — so `DeliverMessage` could never correlate a
  boundary message to the parked instance. A new exported engine accessor,
  `InstanceState.MessageBoundaryWaiters() []engine.MessageWaiter`, returns the
  `(name, key)` pairs for every armed message boundary; `syncMsgWaiters` registers
  them alongside the token waiters so `DeliverMessage` wakes the instance.

## Consequences

- A process author can now attach a working message boundary (interrupting or
  non-interrupting, with or without a correlation key). The behaviour matches the
  signal boundary in every respect except message-vs-signal delivery semantics
  (point-to-point vs broadcast).
- No new fire machinery: interrupting/non-interrupting routing, host-token
  consumption, sibling-arm cancellation, and scope resolution are all the existing
  `fireBoundaryArm` code. The only new engine surface is two `boundaryArm` fields,
  the `boundaryArmByMessage` / `MessageBoundaryWaiters` helpers, and the
  `MessageWaiter` type.
- `engine.Step` remains pure and deterministic: arms are appended in
  definition-scan order; the fire branch matches in slice order; no `time.Now`,
  randomness, or map-order-dependent output is introduced.
- This is a deliberate, user-authorised engine change (the boundary firing has to
  live in the core token state machine). Timer and signal boundary behaviour is
  unchanged; their existing tests pass.
- **Known limitation (unchanged, out of scope here)**: `armBoundaries` is wired
  into the ServiceTask, UserTask, and sub-process/call-activity park points, but
  not the ReceiveTask park point — so a message boundary on a *ReceiveTask* host
  is still not armed. That pre-existing gap affects timer and signal boundaries on
  ReceiveTask hosts equally and is left as a follow-up.

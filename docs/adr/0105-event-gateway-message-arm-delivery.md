# 0105. Runtime message delivery to event-based-gateway message arms

Status: **Accepted — 2026-07-08.**
Extends the message-waiter registration seam introduced for message boundaries
([ADR-0053](0053-message-boundary-events.md)).

## Context

An event-based gateway arms several competing catch events; whichever fires first wins,
and the losing arms are cancelled. The engine core already supports a **message** arm on an
event-based gateway: `handleMessageReceived` has a first-event-wins dispatch branch, and
`resolveGatewayWin` cancels the sibling timer arm. `TestEventGatewayFirstMessageWins`
exercises this at the `engine.Step` layer.

However, the **runtime delivery path** could not reach such an arm. `ProcessDriver.DeliverMessage`
correlates a delivered message to a parked instance via `findMessageWaiter`, which reads the
`msgWaiters` table populated by `syncMsgWaiters`. That reconciler registered only:

1. tokens carrying `Token.AwaitMessage` (standalone message-catch intermediate events and
   ReceiveTasks), and
2. armed **message boundary** events via `InstanceState.MessageBoundaryWaiters()`.

An event-based-gateway message arm is tracked as an `armedEvent`, not as a token carrying
`AwaitMessage`, so it was registered by neither path. `DeliverMessage` therefore returned a
clean no-op for it, the message never reached the arm, and the gateway could only ever be
resolved by its timer arm.

This gap made per-entity correlation impossible on an event-based gateway. The
`examples/scenarios/event_based_gateway` example worked around it by modelling the per-order
payment wait as a **broadcast signal** (`WithCatchSignal`) plus a manual `SignalBus.Subscribe`
of a single instance. That is semantically wrong: a signal broadcasts by name to *every*
instance parked on that name, so in a real deployment with concurrent orders a single
`Publish("payment-confirmed", …)` would resume every unpaid order, not the one that paid.
Signals are for genuine fan-out; per-entity waits require a correlated message.

## Decision

Register event-based-gateway message arms as message waiters, symmetric to message boundaries.

1. Add `InstanceState.MessageArmedEventWaiters() []MessageWaiter` (in `engine/state.go`),
   mirroring `MessageBoundaryWaiters()`. It iterates `s.ArmedEvents` and returns a
   `{Name, CorrelationKey}` pair for each arm whose `Message` is non-empty (using the already
   **resolved** `MessageKey` value). Timer and signal arms contribute nothing.

2. In `runtime.ProcessDriver.syncMsgWaiters` (in `runtime/processdriver_waiters.go`), register
   each `MessageArmedEventWaiters()` entry into `msgWaiters` alongside the existing token and
   boundary registrations.

With this, `driver.DeliverMessage(ctx, def, name, correlationKey, payload)` correlates to an
event-gateway message arm exactly as it does for a message boundary — no manual subscription,
no bus. The example is updated to a correlated message arm
(`WithCatchMessage("payment-confirmed", "order")`) delivered via `DeliverMessage`, and the
`SignalBus` wiring is removed.

This closes the signal/message asymmetry for event gateways: message arms auto-register for
delivery (strictly better than the signal arm, which still needs a manual `SignalBus.Subscribe`
because signal arms are broadcast by design).

## Consequences

- **Positive.** Per-entity correlation now works on an event-based gateway through the public
  runtime API. The engine-core support that already existed is finally reachable end-to-end.
  The example teaches the correct primitive (correlated message) for a per-order wait.
- **Positive.** The change is a pure addition mirroring an established pattern; no existing
  behaviour changes. Signal and timer arms are unaffected.
- **Neutral.** `msgWaiters` keys are `{name, correlationKey}`. As with all message waiters,
  two live instances that arm the same name **and** the same resolved correlation key collide
  on one map slot; correlation keys must be unique per in-flight instance (the same constraint
  that already applies to ReceiveTasks and message boundaries).
- **Neutral.** Signal arms still require a manual `SignalBus.Subscribe` in the event-gateway
  case; unifying that is out of scope here (signals are broadcast, so auto-registration would
  need different semantics).
- **Tested.** `TestMessageArmedEventWaitersExposesGatewayMessageArms` (engine, table-driven)
  covers the accessor for correlated, name-only, and non-message arms;
  `TestDeliverMessageFiresEventGatewayArm` (runtime e2e) covers delivery correlating to the arm
  and winning the gateway race.
- **Related example sweep.** The `catch_event_reminder` example was corrected in the same effort
  from a broadcast signal to a correlated message. It did not need this ADR's change (a
  standalone message catch already registers via `Token.AwaitMessage`); it shared the same
  semantic bug — a per-request approval modelled as a broadcast signal.

# 0106. BroadcastSignal: a ProcessDriver facade for signal publishing

Status: **Accepted — 2026-07-08.**
Symmetric to [ADR-0105](0105-event-gateway-message-arm-delivery.md) (message delivery through
the driver) and the message facade `ProcessDriver.DeliverMessage`.

## Context

`ProcessDriver` is the object a consumer holds to drive process instances. For correlated
messages it exposes a clean facade: `driver.DeliverMessage(ctx, def, name, key, payload)`. For
signals there was no equivalent — a consumer wanting to broadcast an *external* signal had to
construct a `signal.SignalBus`, retain the reference, and call `bus.Publish(...)` directly,
reaching around the driver.

This is an ergonomic asymmetry, not a capability gap: the driver already **owns** a
`sigbus *signal.SignalBus` field (configured via `WithSignalBus`) and already publishes to it
internally when a process node throws a signal (`processdriver_action.go` handles the
`ThrowSignal` command via `driver.sigbus.Publish`). The publishing capability existed; it just
was not exposed on the public facade for the external-broadcast case.

The project value at stake: **minimise the API surface a consumer must touch.** Even though
`signal.SignalBus` is a public type, a consumer should be able to prefer the `ProcessDriver`
facade for the common operations.

## Decision

Add `ProcessDriver.BroadcastSignal(ctx context.Context, name string, payload map[string]any) error`
(in a new `runtime/processdriver_signal.go`, mirroring `processdriver_message.go`). It is a thin
delegation to the owned bus:

- If no bus is configured it returns a descriptive error (`workflow-runtime: BroadcastSignal
  %q: no SignalBus configured (use WithSignalBus)`), matching the existing `ThrowSignal`
  no-bus error style.
- Otherwise it calls `driver.sigbus.Publish(ctx, name, payload)`.

The signature is deliberately **def-less**, unlike `DeliverMessage`. `DeliverMessage` needs
`def` because it calls `driver.Deliver(ctx, def, …)` directly; `BroadcastSignal` delegates to
`SignalBus.Publish`, which resumes waiters through the bus's own `DeliverFunc` closure (already
bound to `def` at construction). Adding a redundant `def` parameter would misrepresent how the
bus works today.

The `signal_broadcast` example now broadcasts via `driver.BroadcastSignal` instead of
`bus.Publish`, so the reference wiring demonstrates the facade-first path.

## Consequences

- **Positive.** Signal broadcast and message delivery are now symmetric on the driver facade;
  a consumer holds one object for both and need not retain the bus after startup.
- **Positive.** Pure addition; `SignalBus.Publish` remains public and unchanged, so existing
  code that calls it keeps working.
- **Neutral / deferred.** This does **not** remove the forward-reference bus *construction*
  (the consumer still builds the bus with a `driver.Deliver` closure and passes `WithSignalBus`,
  because the bus needs the driver and the driver needs the bus). Eliminating that — e.g. the
  driver lazily creating a default internal bus, à la the sensible-default constructors of
  ADR-0096/0097 — was considered and deferred: the bus's `DeliverFunc` binds a single `def`,
  which the driver does not have at construction time, so a default internal bus would need a
  different delivery mechanism (a `def`-carrying `BroadcastSignal`, or per-call def routing).
  That is a larger design change and a potential breaking signature; it is out of scope here.
  This ADR closes the call-site asymmetry only.
- **Tested.** `TestBroadcastSignalResumesParkedInstances` (facade resumes all parked waiters)
  and `TestBroadcastSignalWithoutBusErrors` (descriptive error with no bus) in
  `runtime/processdriver_signal_test.go`.

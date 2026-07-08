# 0107. Rename ProcessDriver.Deliver → ApplyTrigger

Status: **Accepted — 2026-07-08.**
Renames the primitive that [ADR-0105](0105-event-gateway-message-arm-delivery.md) and
[ADR-0106](0106-broadcast-signal-driver-facade.md) build their facades on.

## Context

`ProcessDriver.Deliver(ctx, def, instanceID, trg engine.Trigger)` is the low-level primitive that
applies **one** trigger to **one** existing instance (Load → `engine.Step` → Save → journal →
perform). `Drive` (start/advance a new instance with variables) is its sibling entry point;
`DeliverMessage` and `BroadcastSignal` are ergonomic facades that build a concrete `Trigger` and
call this primitive.

The name `Deliver` is confusing on two counts:

1. **Collides with `DeliverMessage`.** The two read as siblings, but `Deliver` is the generic
   primitive that `DeliverMessage`/`BroadcastSignal` are built on — the name hides that hierarchy.
2. **Misdescribes the operation.** It does not deliver to a mailbox; it *applies a trigger to the
   instance's state machine*. The verb should name that effect.

## Decision

Hard-rename the method to **`ApplyTrigger`**, with an identical signature and identical behavior.
No deprecated alias — consistent with this repo's prior hard renames (`Run`→`Drive`,
`NewCatch`→`NewIntermediateCatch`); the module is pre-1.0. The tracer span
`"wrkflw.runner.Deliver"` is renamed to `"wrkflw.runner.ApplyTrigger"` to stay consistent.

`ApplyTrigger` was chosen over the runner-up `Advance` because it explicitly names that the call
takes a `Trigger`; `Advance` conveys the effect but not the input. Resulting API shape:

```
Drive(ctx, def, id, vars)                     // start/advance a NEW instance with variables
ApplyTrigger(ctx, def, id, trg)               // advance an EXISTING instance with a raw trigger  ← primitives
DeliverMessage(ctx, def, name, key, payload)  // facade → builds MessageReceived → ApplyTrigger
BroadcastSignal(ctx, name, payload)           // facade → Publish → ApplyTrigger per waiter
```

## Consequences

- **Positive.** The two low-level entry points (`Drive` + `ApplyTrigger`) and the two facades
  (`DeliverMessage`, `BroadcastSignal`) form a legible hierarchy; the primitive no longer looks
  like a message-specific method.
- **Neutral.** Breaking change for consumers calling `driver.Deliver(...)` — they update to
  `ApplyTrigger`. Pre-1.0, and the mechanical rename is trivial for consumers.
- **Neutral.** Trace dashboards or alerts keyed on the span name `wrkflw.runner.Deliver` must be
  updated to `wrkflw.runner.ApplyTrigger`.
- **Deliberately untouched.** `processtest.Deliver` — an unrelated `ParkHandler` `Decision`
  constructor in the test-harness package — keeps its name; it is a different symbol. The rename
  used receiver-scoped patterns specifically to avoid corrupting it.
- **No behavior change**, so no new tests; the existing suite is the regression net and stays
  green before and after.

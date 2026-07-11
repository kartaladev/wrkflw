# Message-correlation collision WARN — design

- Status: Accepted
- Date: 2026-07-11
- ADR: 0125

## Context

The runtime's message-correlation table is a single-value map:

```go
// runtime/processdriver.go
msgWaiters map[msgKey]string // (messageName, correlationKey) -> instance ID
```

Its own doc states the design contract: *"Message catch events are 1:1 (each
correlation key routes to exactly one instance), so a simple map suffices."*
`syncMsgWaiters` (`runtime/processdriver_waiters.go`) reconciles this table after
every committed save: it deletes the instance's stale entries, returns early for a
terminal instance (the ADR-0124 guard), then re-registers every `(name, key)` the
instance can be woken by from the engine's authoritative `st.MessageWaiters()`
union (token message-catch awaits, armed message boundaries, event-based-gateway
message arms, message-triggered event-sub arms — ADR-0123).

**The gap.** If two *running* instances await the same non-empty (or empty)
`(name, correlationKey)`, the second re-registration silently overwrites the first
(last-writer-wins). This violates the documented 1:1 invariant and silently drops
one instance's waiter: a later `DeliverMessage` correlates to whichever instance
won the map slot, and the other never receives its message. Because every message
construct funnels through `MessageWaiters()`/`msgWaiters`, the gap affects catch
tokens, message boundaries, event-gateway arms, and event-sub arms uniformly.

Two running instances awaiting the same `(name, key)` is **ambiguous message
correlation**: for a keyed await it is a modeling error (correlation keys are meant
to be unique per in-flight conversation); for a keyless await it is inherently
ambiguous. Messages in BPMN are **point-to-point** — exactly one receiver. Fan-out
to many receivers is the **signal** model, already provided by `BroadcastSignal` /
`SignalBus`. So the ambiguity is a real defect in the *model or the caller*, not a
missing engine capability.

Today the collision is invisible: no error, no log. An operator has no signal that
a message was silently dropped.

## Decision

**Surface the invariant violation via a WARN log; keep point-to-point single-value
delivery unchanged.**

In `syncMsgWaiters`, in the re-register loop (after the ADR-0124 terminal guard),
before writing key `k` into `msgWaiters`, check whether `k` is already present and
owned by a **different** instance ID than `st.InstanceID`. If so, emit a WARN via
`driver.obs.tel.Logger.LogAttrs(context.Background(), slog.LevelWarn, ...)` naming
the message, correlation key, the incumbent instance, and the joining instance.
Then proceed with the existing overwrite — **behavior is unchanged**; the WARN only
surfaces the ambiguity.

The WARN matches the project's existing register-/reconcile-time WARN idiom
(`LogAttrs` + `slog.String` attrs, message prefixed `"runtime:"`; see
`runtime/processdriver_cancel.go`, `runtime/jobstore.go`,
`runtime/definition_registry.go`) and the project's WARN-on-ambiguous-config
philosophy (ADR-0119, ADR-0121). Delivery stays 1:1; no multi-value map, no
fan-out, no new public API.

The `msgWaiters` field doc is updated to note that a violation of the 1:1 contract
is WARN-logged.

**Frequency is low.** `syncMsgWaiters` runs only after a `deliverLoop` save, and a
parked instance is not re-saved, so the WARN fires roughly when an instance parks
on an already-awaited key — not on every delivery.

### Rejected alternatives

- **Multi-value delivery (`map[msgKey][]string` + fan-out).** Rejected: it rewrites
  the documented 1:1 point-to-point contract and invents non-BPMN message
  semantics. Fan-out is the signal model and already exists (`BroadcastSignal` /
  `SignalBus`). A consumer wanting many receivers models a signal, not a message.
- **Doc-only (document the last-writer-wins hazard, change nothing).** Rejected: it
  leaves the silent drop silent. An operator still gets no runtime signal that a
  message was lost, so a production misconfiguration stays undiagnosable.
- **Hard error / reject the second registration.** Rejected: `syncMsgWaiters` runs
  inside the commit reconciliation after state is already durably saved; failing it
  cannot un-commit the parked instance and would leave the driver in an inconsistent
  state. WARN-and-continue is the only consistent choice at this seam, and matches
  the project's register-time WARN precedent.

## Quality attributes

- **Backward compatible.** No public API change; delivery semantics identical. Only
  a log line is added.
- **Observability.** The ambiguity is now diagnosable from logs with both instance
  IDs and the offending `(name, key)`.
- **Consistency.** Reuses the existing `LogAttrs` + `slog.String` `"runtime:"` WARN
  idiom and the driver's always-non-nil logger accessor `driver.obs.tel.Logger`.
- **Performance.** One map read already implied by the map write; negligible, and
  only on the low-frequency park-and-save path.

## Testing strategy

White-box (`package runtime`) test that injects a custom `*slog.Logger` writing to
a `bytes.Buffer` (JSON handler) via the existing `WithLogger` option, drives **two**
instances that both park awaiting the **same** message name + correlation key, and
asserts:

1. the buffer contains the WARN record with both instance IDs and the `(name, key)`;
2. delivery still reaches **exactly one** instance (behavior unchanged).

White-box tests cannot import `runtime/internal/runtimetest` (import cycle), so the
driver is constructed directly via `NewProcessDriver(...)` with
`kernel.NewMemInstanceStore()` — the pattern already used by
`runtime/terminal_waiter_test.go`.

# 125. WARN on ambiguous message correlation (cross-instance collision)

- Status: Accepted
- Date: 2026-07-11

## Context

The runtime correlates an inbound message to a parked instance through a
single-value table:

```go
// runtime/processdriver.go
msgWaiters map[msgKey]string // (messageName, correlationKey) -> instance ID
```

Its doc states the design contract: *"Message catch events are 1:1 (each
correlation key routes to exactly one instance), so a simple map suffices."*
`syncMsgWaiters` (`runtime/processdriver_waiters.go`) reconciles this table after
every committed save: it deletes the instance's stale entries, returns early for a
terminal instance (the ADR-0124 guard), then re-registers every `(name, key)` the
instance can be woken by from the engine's authoritative `st.MessageWaiters()`
union — token message-catch awaits, armed message boundaries, event-based-gateway
message arms, and message-triggered event-sub arms (ADR-0123).

If **two running instances** await the same `(name, correlationKey)`, the second
re-registration silently overwrote the first (last-writer-wins). This violates the
documented 1:1 invariant and silently drops one instance's waiter: a later
`DeliverMessage` correlates to whichever instance won the map slot, and the other
never receives its message. Because every message construct funnels through
`MessageWaiters()`/`msgWaiters`, the gap affected catch tokens, message boundaries,
event-gateway arms, and event-sub arms uniformly, with no error and no log.

Two running instances awaiting the same `(name, key)` is **ambiguous message
correlation** — a modeling error for a keyed await (correlation keys are meant to
be unique per in-flight conversation) and inherently ambiguous for a keyless one.
BPMN messages are **point-to-point** (exactly one receiver); fan-out to many
receivers is the **signal** model, already provided by `BroadcastSignal` /
`SignalBus`. The ambiguity is therefore a defect in the model or the caller, not a
missing engine capability.

## Decision

**Surface the invariant violation via a WARN log; keep point-to-point single-value
delivery unchanged.** No multi-value map, no fan-out, no new public API.

1. In `syncMsgWaiters`, in the re-register loop (after the ADR-0124 terminal
   guard), read the current owner of each key before writing it. If the key is
   already present and owned by a **different** instance than `st.InstanceID`, emit
   a WARN via `driver.obs.tel.Logger.LogAttrs(context.Background(),
   slog.LevelWarn, ...)` naming the message, correlation key, incumbent instance,
   and joining instance. Then proceed with the existing overwrite — **behavior is
   unchanged**; the WARN only surfaces the ambiguity.
2. The WARN reuses the project's register-/reconcile-time idiom (`LogAttrs` +
   `slog.String` attrs, message prefixed `"runtime:"`; see
   `runtime/processdriver_cancel.go`, `runtime/jobstore.go`,
   `runtime/definition_registry.go`) and the always-non-nil logger accessor.
3. The `msgWaiters` field doc records that a 1:1-contract violation is WARN-logged.

Frequency is low: `syncMsgWaiters` runs only after a `deliverLoop` save, and a
parked instance is not re-saved, so the WARN fires roughly when an instance parks
on an already-awaited key — not on every delivery.

### Rejected alternative — multi-value delivery

A `map[msgKey][]string` with fan-out was rejected: it rewrites the documented 1:1
point-to-point contract and invents non-BPMN message semantics. Fan-out is the
signal model and already exists (`BroadcastSignal` / `SignalBus`); a consumer
wanting many receivers models a signal, not a message. This matches the project's
direction of converging on the general BPMN form rather than adding bespoke modes.

### Rejected alternative — doc-only

Documenting the last-writer-wins hazard without a runtime signal was rejected: it
leaves the silent drop silent, so a production misconfiguration stays
undiagnosable.

### Rejected alternative — hard error

Failing the re-registration was rejected: `syncMsgWaiters` runs inside commit
reconciliation after state is already durably saved, so an error there cannot
un-commit the parked instance and would leave the driver inconsistent.
WARN-and-continue is the only consistent choice at this seam, matching the
project's register-time WARN precedent (ADR-0119, ADR-0121).

## Consequences

- An ambiguous cross-instance message correlation is now diagnosable from logs,
  with both instance IDs and the offending `(name, key)`.
- Message delivery semantics are unchanged: still 1:1, still last-writer-wins on
  the map slot. No public API change; no wire/definition change.
- The change is one map read plus one conditional WARN in `syncMsgWaiters`, on the
  low-frequency park-and-save path. The ADR-0124 terminal guard and the ADR-0123
  `MessageWaiters()` authority are untouched.
- A consumer needing many receivers for one event uses a signal, not a message.
- Spec: `docs/specs/2026-07-11-message-correlation-collision-warn-design.md`.
- Plan: `docs/plans/2026-07-11-message-correlation-collision-warn.md`.

# 46. Status-accurate terminal outbox events

- Status: Accepted
- Date: 2026-06-24

## Context

Terminal outbox events are the integration seam process-instance chaining
(ADR-0045) subscribes to in order to route a successor by the predecessor's
terminal outcome. For that to work, each terminal outcome must map to a distinct,
accurate event. Today it does not.

Terminal events are derived **command-driven** in `runtime.outboxEventsFor`:
`engine.CompleteInstance → "instance.completed"`, `engine.FailInstance →
"instance.failed"`. But the engine's terminal *command* does not always match the
terminal *status*:

- The cancel path (`engine/step_triggers.go`) sets `StatusTerminated` yet emits
  `FailInstance{Err:"cancelled"}` → a cancelled instance wrongly publishes
  `instance.failed`.
- The admin full-rollback path (`engine/step_compensation.go`) reaches
  `StatusTerminated` with **no terminal command at all** → it publishes
  **nothing**.

So a consumer cannot distinguish *failed* from *terminated*, and misses one
terminal path entirely. The instance *status* (`StatusCompleted` /
`StatusFailed` / `StatusTerminated`) is the accurate signal, and `deliverLoop`
already computes the terminal **edge** (`isTerminal(st.Status) &&
!isTerminal(prevStatus)`) where it derives `CallOutcome` — the ideal place to
derive the event too.

## Decision

Move terminal-event derivation from **command-driven** to **status-driven**,
computed at the `deliverLoop` terminal edge, with the engine core untouched:

| `st.Status` at the terminal edge | Topic | Payload |
|---|---|---|
| `StatusCompleted` | `instance.completed` | `st.Variables` (unchanged) |
| `StatusFailed` | `instance.failed` | `{"error": terminalErr(st)}` |
| `StatusTerminated` | `instance.terminated` (**new**) | `{"error": terminalErr(st)}` |

A new `terminalOutboxEvent(prevStatus, st)` in `runtime/outbox.go` returns the
single event at a terminal edge (or none otherwise), reusing the existing
`terminalErr(st)` helper (first incident error, else a status-keyed message).
`deliverLoop` calls it in place of `outboxEventsFor(res.Commands)`. Because the
command-driven mapping only ever handled the two terminal commands, this fully
replaces it; `outboxEventsFor` is removed. Each terminal status now emits
**exactly one** status-accurate event, carrying the instance id so an in-process
subscriber can route it.

## Consequences

- **Behavioural change (intended):** a cancelled instance now emits
  `instance.terminated` (was `instance.failed`); a full-rollback termination now
  emits `instance.terminated` (was *nothing*). `instance.completed` and
  `instance.failed` (genuine unhandled failure) are **unchanged** in topic and
  payload shape.
- **Migration note:** any consumer that relied on `instance.failed` firing for a
  *cancelled* instance must also subscribe `instance.terminated`. No in-repo
  consumer does. The `{"error": …}` payload string for a cancel changes from the
  command's literal `"cancelled"` to `terminalErr`'s value (the first incident
  error, else `"instance terminated"`); consumers should treat the error string
  as human-readable, not a stable enum (use the topic for routing).
- Determinism/`Step` purity (ADR-0002) are unaffected — derivation reads only
  `prevStatus`/`st.Status` in `runtime`; engine/model production diff is **ZERO**.
- Unblocks ADR-0045: all three terminal outcomes are now routable by topic.

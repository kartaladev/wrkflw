# 123. Correlate message/signal delivery to event sub-process arms

- Status: Accepted
- Date: 2026-07-11

## Context

An event sub-process (ADR-0122) is an `activity.SubProcess` whose inner start is
event-triggered. While a scope is open the engine records an
`eventTriggeredSubprocessArm` per event sub-process and, when the matching
trigger arrives, dispatches to `fireEventTriggeredSubprocessArm` from the
`MessageReceived`, `SignalReceived`, and `TimerFired` handlers. The engine fire
path is complete and tested.

The defect was entirely in the **runtime**, in how it decides which delivered
message/signal reaches which parked instance. After each `deliverLoop` save the
runtime reconciles "what is this instance waiting for?" in
`runtime/processdriver_waiters.go`:

- `syncMsgWaiters` populated the `msgWaiters` correlation map from three sources:
  token `AwaitMessage`, `MessageBoundaryWaiters()`, and `MessageArmedEventWaiters()`.
- `syncSignalBus` subscribed the instance in the `SignalBus` from one source:
  token `AwaitSignal`.

Event sub-process arms are **non-token-parked** — the arm lives in
`s.EventTriggeredSubprocesses`, no token carries `AwaitMessage`/`AwaitSignal` —
exactly like message boundaries and event-gateway arms. But unlike those two,
event-sub arms were never added to either reconciliation. The result was two
silent no-ops:

- `ProcessDriver.DeliverMessage` against a message-triggered event-sub → no-op
  (documented as a follow-up during ADR-0122; example used an `ApplyTrigger`
  workaround).
- `ProcessDriver.BroadcastSignal` against a signal-triggered event-sub → the same
  bug, previously unidentified: `SignalBus.Publish` only delivers to instances in
  `b.waiters[name]`, populated solely from token `AwaitSignal`.

Timer-triggered event-subs were unaffected — they flow through the
scheduler/`ScheduleTimer` command mechanism, not the correlation tables.

Root cause: "what is this instance awaiting?" is engine state, but it was being
re-derived construct-by-construct inside the runtime. Each new non-token construct
(boundary → gateway arm → event-sub arm) required remembering to add another
enumeration line. Event-sub arms were forgotten. This is a cohesion/coupling
defect, not merely a missing line.

## Decision

Make the engine the single authority on what an instance awaits, and have the
runtime mirror exactly that.

1. **Engine granular accessors** on `*engine.InstanceState`, next to the existing
   `MessageBoundaryWaiters`/`MessageArmedEventWaiters`:
   - `MessageEventSubprocessWaiters() []MessageWaiter` — message arms of
     `EventTriggeredSubprocesses`.
   - `SignalEventSubprocessNames() []string` — signal arms of
     `EventTriggeredSubprocesses`.
2. **Engine unified authority** on `*engine.InstanceState`:
   - `MessageWaiters() []MessageWaiter` — the union of token `AwaitMessage`,
     message boundaries, event-gateway message arms, and message event-sub arms,
     in a fixed deterministic order.
   - `SignalWaiters() []string` — the union of token `AwaitSignal` and signal
     event-sub arms.
   The token scan (previously in the runtime) moves into these methods so all
   await-derivation lives where the token state lives.
3. **Runtime mirrors one source.** `syncMsgWaiters` iterates `st.MessageWaiters()`;
   `syncSignalBus` calls `sigbus.Sync(id, st.SignalWaiters())`. The runtime keeps
   the mechanism (the `msgWaiters` map + `msgMu`; the `SignalBus` lifecycle) but no
   longer enumerates construct types or reads `st.Tokens`.
4. **Example** `examples/scenarios/event_subprocess` fires "cancel" via
   `DeliverMessage` instead of the `ApplyTrigger` workaround.

No engine fire-path change was needed — the handlers already dispatch to
`fireEventTriggeredSubprocessArm`.

### Rejected alternatives

- **Repeatable non-interrupting event-subs (re-arm).** Rejected: it would break
  consistency with non-interrupting *boundary* events, which are deliberately,
  documentedly one-shot (`engine/step_boundaries.go`: "fired once; do not re-arm").
  Repeatable non-interrupting events, if wanted, are a project-wide decision
  covering boundaries and event-subs together — its own ADR. **Follow-up (A).**
- **Multi-instance message fan-out.** Rejected: BPMN messages are point-to-point
  (correlated to one instance); broadcast-to-many is the *signal* model, which the
  `SignalBus` already implements (`b.waiters[name]` is a set) and which this change
  makes work for signal event-subs for free. Forcing messages to fan out would
  contradict BPMN and blur message-vs-signal semantics. The narrow real edge — two
  instances colliding on an identical non-empty `(name, key)` in the single-value
  `msgWaiters` map — is a pre-existing, all-message-constructs limitation for
  validation/docs, not a delivery-model rewrite. **Follow-up (B).**

## Consequences

- Two silent-no-op delivery bugs closed: `DeliverMessage` and `BroadcastSignal`
  now wake message- and signal-triggered event sub-process arms (interrupting and
  non-interrupting), verified by runtime e2e tests.
- The class of defect is removed: a future message/signal construct extends one
  engine method (`MessageWaiters`/`SignalWaiters`) instead of every runtime sync
  site. The runtime no longer couples to engine construct internals.
- Message stays point-to-point; signal stays broadcast — in line with BPMN.
- Performance: one bounded slice scan per `deliverLoop` sync (same order as the
  scans it replaces); no new hot-path allocations.
- Follow-ups (A) repeatable non-interrupting events and (B) `(name,key)` collision
  hardening are deferred with rationale above.
- Spec: `docs/specs/2026-07-11-eventsub-correlation-waiters-design.md`.

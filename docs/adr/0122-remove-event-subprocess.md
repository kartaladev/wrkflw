# 122. Remove EventSubProcess; model it as an event-triggered SubProcess

- Status: Accepted
- Date: 2026-07-11

## Context

The engine shipped two node kinds that model the same BPMN concept two ways:

- `activity.SubProcess` — a nested definition executed as a scope, entered by a
  token flowing to it, driving its *none* start inline.
- `event.EventSubProcess` — a nested definition rooted at an **event-triggered**
  start (message/signal/timer), not on any sequence flow, latent until its event
  fires, then interrupting (cancels the enclosing scope) or non-interrupting
  (runs alongside).

ADR-0121 (event-based start) gave the engine first-class event-triggered start
events and generalized the event-sub arm/fire machinery to select the inner
start via `eventTriggeredStart(def)` rather than `StartNodes()[0]`. That made
`EventSubProcess` redundant: **an event sub-process is exactly a `SubProcess`
whose inner start event is event-triggered.** Removing it is the final fold in
the BPMN2-alignment effort (ADRs 0117–0122) of retiring bespoke kinds in favour
of their general form.

This is the highest-regression-risk item in that effort — it retires machinery
hardened very recently (event-sub-arm timer sweeps, interrupting/non-interrupting
scope-drain edge cases, reverse-instance handling). The library is unreleased, so
a clean break is acceptable (no wire aliases, no migrators — ADR-0004).

## Decision

**Delete the `EventSubProcess` node kind and its entire public surface**: the
struct, `model.KindEventSubProcess`, `event.NewEventSubProcess`,
`WithEventSubProcessNonInterrupting`, `EventSubProcessOption`,
`Builder.AddEventSubProcess`, and the `"eventSubProcess"` wire discriminator
name. An event sub-process is now authored as `activity.NewSubProcess(id, sub)`
where `sub` has an event-triggered inner start.

1. **Interrupting marker on the StartEvent (BPMN-faithful).** Add
   `StartEvent.NonInterrupting`, set via `event.WithNonInterrupting()`. BPMN2
   places `isInterrupting` on the event sub-process's start event, not on the
   sub-process; the flag sits next to the trigger fields
   (`SignalName`/`MessageName`/`Timer`) that are already only meaningful for an
   event-triggered start, and the engine reads it off the inner start the
   `eventTriggeredStart` selector already returns. A plain none-start SubProcess
   carries no interrupting field at all.

2. **Engine keys off a single discriminator.** `eventSubprocessNested(node)`
   returns `(nestedDef, innerStart, nonInterrupting, ok)` for an
   `activity.SubProcess` whose inner start is event-triggered (`ok=false` for a
   none-start SubProcess, which stays token-driven inline). The arm scan, the
   fire path, and the scope-drain detection all route through it. The internal
   identifiers were renamed `eventSubprocess*` → `eventTriggeredSubprocess*`
   (e.g. `InstanceState.EventTriggeredSubprocesses`) so nothing carries the
   retired kind's name; `eventTriggeredStart` kept its (already generic) name.

3. **Validation.** A `SubProcess` whose inner start is event-triggered is a
   reachability root (like the old kind) and is exempt from the outgoing-flow
   requirement — it runs its nested definition to its own end and never hands a
   token back. Detected via the wire projection (`definition/model` cannot import
   `definition/event`). A new `ErrEventSubprocessOnFlow` rejects such a node that
   carries an **incoming** sequence flow: it is latent until its trigger fires
   and is never entered by a flowing token, so an incoming flow is ambiguous
   between "embedded" and "event sub-process" semantics. A `SubProcess` with
   *any* event-triggered start is classified as an event sub-process (matching
   the engine's `eventTriggeredStart` arm behaviour), so a mixed none+event start
   nested definition is treated as an event sub-process and may not sit on a flow.

4. **Clean break on the wire.** The `"eventSubProcess"` discriminator is deleted;
   old JSON/YAML carrying it no longer unmarshals (acceptable, unreleased). There
   is **no** wire-format-version constant to bump — the only version is the
   business `ProcessDefinition.Version`. Removing `KindEventSubProcess` from the
   middle of the `NodeKind` iota renumbers later kinds, which is safe because the
   wire keys on the registered string `Name`, not the int.

5. **Parity-first migration.** The generalization and validation changes landed
   while the legacy kind was still alive; a full parity test suite in the
   `SubProcess`-with-event-start form
   (`engine/step_subprocess_eventstart_test.go`) was proven green **alongside**
   the legacy ESP tests before the kind was deleted. The legacy tests then mapped
   1:1 to the parity suite, with unique-coverage cases (ADR-0121 multi-start,
   the reverse-instance terminate-timer-cancel cases) converted and kept.

## Consequences

**Positive.** One fewer node kind; the "event sub-process" concept is a natural
composition (`SubProcess` + event-triggered start) rather than a bespoke type;
the interrupting marker sits where BPMN puts it; internal names no longer
reference a retired kind. The freshly-hardened scope-drain and reverse-instance
behaviour is preserved (parity-tested).

**Negative / risk.** Retires recently-hardened machinery; the
interrupting/non-interrupting scope-drain is the subtlest surface, mitigated by
the parity-first order and adversarial review. Old serialized definitions with
`"kind":"eventSubProcess"` stop loading — acceptable for an unreleased library.

**Known limitation (pre-existing, not introduced here).**
`runtime.ProcessDriver.DeliverMessage` correlates a delivered message only to
parked `AwaitMessage` tokens, message boundaries, and event-gateway arms — not to
a message-triggered event sub-process's own arm (`syncMsgWaiters` omits
`InstanceState.EventTriggeredSubprocesses`). A message meant to fire such an
event-sub therefore silently no-ops through `DeliverMessage`; deliver it via
`ProcessDriver.ApplyTrigger` with the known instance id instead (see
`examples/scenarios/event_subprocess`). This gap predates ADR-0122 (a legacy
message-triggered `EventSubProcess` had the identical behaviour — `runtime/` was
untouched by this change) and is tracked as a follow-up. Signal-triggered
event-subs fire via `BroadcastSignal`; timer-triggered via the scheduler.

**Mooted ticket.** The pre-existing question of whether normal success
completion sweeps outstanding root event-sub arms + their timers is moot for the
ESP-specific machinery now that it is retired; the general question ("does
`TimerFired` no-op on a terminal-status instance?") remains tracked separately.

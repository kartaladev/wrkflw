# 124. Repeatable non-interrupting boundary events and event sub-processes

- Status: Accepted
- Date: 2026-07-11

## Context

Non-interrupting boundary events and non-interrupting event sub-processes were **one-shot**:
they fired once, then the engine deleted the arm, so a second delivery of the same
message/signal (or the next tick of a recurring timer) was a silent no-op. This was an
incomplete implementation, documented in-code as a stopgap (`engine/step_boundaries.go`:
*"fired once; do not re-arm — repeating out of scope"*).

BPMN 2.0 has no "fire once, alongside" concept: a **non-interrupting** boundary event or event
sub-process is repeatable by definition — it may be triggered multiple times while its host
activity / enclosing scope is active, spawning a fresh parallel path each time (periodic timer
escalations, repeated reminder messages, repeated signals).

Research established that one-shot was caused **entirely** by two single-arm deletions
(`removeBoundaryArm` in `fireBoundaryArm`; `removeEventTriggeredSubprocessArm` in
`fireEventTriggeredSubprocessArm`), and that every other mechanism already supports repeatability:
recurring timers are representable and re-delivered by the scheduler (same `TimerID`);
message/signal deliveries re-run the by-name lookup; spawned token IDs (`-t{seq}`) and child
scope IDs (`-s{seq}`) are counter-based (no collision across N fires); arm keys are stable across
fires; and end-of-life sweeps (`removeBoundaryArmsForHost`,
`removeEventTriggeredSubprocessArmsForScope`, `removeAllEventTriggeredSubprocessArms`,
`cancelAllArmsAndBoundaries`) already retire arms at host/scope/instance end.

Two consequences of the arm surviving longer had to be handled:

- **Terminal-instance waiter leak.** `runtime` reconciles correlation waiters after *every*
  committed save, including the terminal one, with no terminal special-case. A repeatable **root**
  event-sub arm (`EnclosingScopeID == ""`) can be present in the `InstanceState` snapshot when the
  instance reaches a terminal status, so its message/signal waiter would be re-registered against a
  dead instance and misroute a later delivery (e.g. swallow a message that should have started a
  fresh message-start instance). This latent gap already existed post-ADR-0123 for a *never-fired*
  root arm on an instance completing via the non-sweeping completion paths.
- **Recurring-timer leak (fixed as a side effect).** A non-interrupting recurring-timer boundary
  previously deleted its arm — and its `TimerID` — on first fire, so host-completion could no
  longer cancel the gocron job; it ran forever. Keeping the arm lets the host-end sweep cancel it.

## Decision

**Non-interrupting ⟹ repeatable, by default, with no new API.** Remove the two single-arm
deletions from the non-interrupting fire branches; leave everything else (spawn token / open child
scope / drive) unchanged.

1. `fireBoundaryArm` non-interrupting branch: drop `removeBoundaryArm(...)`. The arm survives; the
   host stays parked; a fresh `TokenActive` is placed at the boundary target. (The now-unused
   `removeBoundaryArm` helper is deleted.)
2. `fireEventTriggeredSubprocessArm` non-interrupting branch: drop
   `removeEventTriggeredSubprocessArm(...)`. The arm survives; each fire opens its own child scope.
   (The now-unused single-arm remover is deleted.)
3. **Per-delivery-once preserved:** each trigger delivery fires each matching arm at most once
   (handlers call the fire function once per delivery; by-name lookup returns the first match), so
   a single delivery still spawns exactly one parallel path — `TestNonInterruptingBoundarySignalNoSelfCascade`
   holds. Repeatability is *across* deliveries.
4. **Runtime terminal guard:** `syncMsgWaiters` returns without re-registering, and `syncSignalBus`
   syncs an empty set, when `isTerminal(st.Status)`. A terminal instance holds no correlation
   waiter regardless of what arms linger in its snapshot. `isTerminal` excludes the transient
   `Compensating` state (which legitimately keeps its waiters). The engine is left untouched — a
   lingering arm in a terminal snapshot is harmless (`fireEventTriggeredSubprocessArm` is
   status-guarded to no-op on a non-`Running` instance); the runtime guard is the correctness
   boundary.
5. **No wire/definition change.** Repeatability is a runtime firing property of the existing
   `NonInterrupting` flag — no new node field, builder option, or wire key. Existing definitions
   gain the correct BPMN behavior for free.

### Rejected alternative — opt-in repeatable flag

Adding a `WithRepeatable()` option so one-shot stays the default was rejected: BPMN models no
"fire-once non-interrupting" construct, so a flag would invent a non-BPMN mode and split behavior.
The project's direction (ADRs 0117–0123) is to converge on the general BPMN form and retire
bespoke variants. A consumer who wants fire-once-alongside expresses it by modeling (a guard on the
spawned path, or correlation), not an engine flag — YAGNI.

## Consequences

- Non-interrupting boundary events and event sub-processes now fire repeatedly (once per delivery)
  across message, signal, and recurring-timer triggers, matching BPMN 2.0.
- A latent recurring-timer boundary gocron-job leak is fixed (arm retained → host-end sweep cancels).
- A terminal instance never holds a correlation waiter, closing both the new repeatable-arm case
  and the pre-existing ADR-0123 never-fired-arm case.
- Interrupting paths, reverse/terminate/compensation sweeps, arm-time, and the wire format are
  untouched; the change is two deletions plus one runtime guard.
- One-shot test assertions were flipped to repeatable (arm survives + second delivery re-fires);
  two now-dead single-arm removers were deleted.
- Spec: `docs/specs/2026-07-11-repeatable-noninterrupting-design.md`.

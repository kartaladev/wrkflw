# Repeatable Non-Interrupting Events — Design

**Date:** 2026-07-11
**Status:** Approved (autonomous SDD run; follow-up A of ADR-0123)
**Related ADRs:** ADR-0122 (event-sub = SubProcess + event-triggered start), ADR-0123 (event-sub correlation waiters)
**New ADR:** ADR-0124 (this change)

## Context

Non-interrupting boundary events and non-interrupting event sub-processes are today
**one-shot**: they fire once, then the engine deletes the arm, so a second delivery of
the same message/signal (or the next tick of a recurring timer) is a silent no-op.

BPMN 2.0 has no "fire once, alongside" concept: a **non-interrupting** boundary event or
event sub-process is repeatable by definition — it may be triggered multiple times while its
host activity / enclosing scope is active, spawning a fresh parallel path each time (periodic
timer escalations, repeated reminder messages, repeated signals). The current one-shot behavior
is an incomplete implementation, documented in-code as a stopgap
(`engine/step_boundaries.go`: *"fired once; do not re-arm — repeating out of scope"*).

Research into the current mechanics (see the two research passes summarized below) shows the
one-shot behavior is caused **entirely** by two single-arm deletions on the fire path, and that
every other mechanism already supports repeatability:

- **Recurring timers are representable and physically re-delivered.** `schedule.TriggerSpec`
  supports recurring kinds (`Every`, `Cron`, `Daily`, …); the gocron adapter only sets
  `WithLimitedRuns(1)` for `KindOneTime`; and `runtime/timerops.go` already keeps a recurring
  timer armed across fires, re-delivering `TimerFired` with the same `TimerID`. Today the second
  `TimerFired` no-ops only because the arm was deleted on the first.
- **Message/signal re-delivery** re-runs the by-name arm lookup on each external delivery.
- **Spawned IDs never collide:** `placeTokenInScope` (`-t{seq}`) and `openScope` (`-s{seq}`) are
  counter-based, so N fires of the same arm produce N distinct tokens/child-scopes.
- **Arm keys are stable across fires:** boundary `(HostToken, BoundaryNode)`, event-sub
  `(EnclosingScopeID, EventSubprocessNode)`.
- **End-of-life removal already exists:** `removeBoundaryArmsForHost` (host completes/advances),
  `removeEventTriggeredSubprocessArmsForScope` (enclosing scope closes), and
  `removeAllEventTriggeredSubprocessArms` / `cancelAllArmsAndBoundaries` (terminal sweeps) remove
  arms at host/scope/instance end.

### Latent bug this also fixes

A non-interrupting **recurring-timer** boundary today deletes its arm — and with it the arm's
`TimerID` — on the first fire. When the host later completes, `removeBoundaryArmsForHost` can no
longer find that arm to emit `CancelTimer`, so the underlying gocron recurring job leaks (keeps
delivering `TimerFired` that no-op forever). Keeping the arm until host end lets the existing
host-completion sweep cancel the recurring timer.

## Decision

**Non-interrupting ⟹ repeatable, by default, with no new API.** Remove the single-arm deletion
from the two non-interrupting fire branches; leave everything else (spawn token / open child
scope / drive) unchanged. The arm now survives each fire and is removed only by the existing
host-end / scope-end / instance-end sweeps.

### 1. Boundary — `fireBoundaryArm` (non-interrupting branch, `engine/step_boundaries.go`)

Delete the `s.removeBoundaryArm(ba.HostToken, ba.BoundaryNode)` call. The arm stays armed; the
host token stays parked; a fresh `TokenActive` is placed at the boundary's outgoing target (as
today). Interrupting branch unchanged (still consumes host + `removeBoundaryArmsForHost`).

### 2. Event-sub — `fireEventTriggeredSubprocessArm` (non-interrupting branch, `engine/step_eventsubprocess.go`)

Delete the `s.removeEventTriggeredSubprocessArm(ea.EnclosingScopeID, ea.EventSubprocessNode)`
call. The arm stays armed; a fresh child scope is opened and seeded (as today). Interrupting
branch unchanged (still cancels the enclosing scope + `removeEventTriggeredSubprocessArmsForScope`).

### 3. Runtime — terminal instances hold no waiters (required correctness guard)

Because a repeatable **root** event sub-process arm (`EnclosingScopeID == ""`) now survives each
fire and is retired only when the instance ends, it can be live in the `InstanceState` snapshot at
the moment the instance reaches a terminal status. `runtime` reconciles waiters after *every*
committed save, including the terminal one (`processdriver.go` calls `syncWaiters(st)` with the
terminal state), and has no terminal special-case — so a lingering arm would register a **stale
message/signal waiter against a dead instance**, misrouting a later delivery (e.g. swallowing a
message that should have started a fresh message-start instance).

Guard it at the single reconciliation choke point:

- `syncMsgWaiters`: after deleting the instance's existing entries, **return without
  re-registering when `isTerminal(st.Status)`** (Completed / Failed / Terminated).
- `syncSignalBus`: when `isTerminal(st.Status)`, call `sigbus.Sync(id, nil)` to unsubscribe all.

`isTerminal` (already in `runtime/observability.go`) excludes the transient `Compensating` state,
which legitimately keeps its waiters. This is defensive — the runtime authoritatively refuses to
hold correlation entries for terminal instances regardless of what the engine leaves in state —
and it **also retroactively fixes a latent ADR-0123 gap**: a *never-fired* root event-sub arm on
an instance that completes via the non-sweeping completion paths (`step_nodes.go` root-completion
sites) already leaks a stale waiter today. The engine is left untouched (a lingering arm in a
terminal snapshot is harmless — `fireEventTriggeredSubprocessArm` is status-guarded to no-op on a
non-`Running` instance); the runtime guard is the correctness boundary.

### 4. Per-delivery semantics preserved

Each trigger delivery fires each matching arm **at most once** — the signal/message/timer
handlers call the fire function once per delivery, and the by-name lookup returns the first
match. Repeatability is *across deliveries*, not *within one delivery*, so a single delivery
still spawns exactly one parallel path (the `TestNonInterruptingBoundarySignalNoSelfCascade`
invariant holds).

### 5. No wire/definition change

Repeatability is a runtime firing property of the existing `NonInterrupting` flag. No new node
field, builder option, or wire key. Existing definitions gain the correct BPMN behavior for free.

## Scope of trigger types

All three become repeatable uniformly, gated only by arm existence:

- **Message / signal:** every external delivery re-fires (BPMN reminders/escalations).
- **Recurring timer** (`Every`/`Cron`/`Daily`/…): each scheduler tick re-fires until host/scope end.
- **One-time timer** (`AfterDuration`/`At`): fires once physically; keeping the arm is harmless
  (no further `TimerFired` arrives), and the arm is swept at host/scope end.

## Rejected alternative — opt-in repeatable flag

Adding a `WithRepeatable()` option (or a `Repeatable bool` field) so one-shot stays the default
was considered and rejected: BPMN models no "fire-once non-interrupting" construct, so a flag
would invent a non-BPMN mode and split behavior. The project's direction (ADRs 0117–0123) is to
converge on the general BPMN form and retire bespoke variants. Repeatable-by-default is that
form. A consumer who genuinely wants fire-once-alongside expresses it by modeling (a guard on the
spawned path, or correlation), not an engine flag — YAGNI.

## Quality attributes

- **Correctness:** aligns non-interrupting firing with BPMN 2.0; fixes a latent recurring-timer
  gocron-job leak.
- **Consistency:** boundary and event-sub non-interrupting firing behave identically (both
  repeatable), and all three trigger types are gated uniformly by arm existence.
- **Simplicity / altitude:** the change is two deletions — it *removes* the special-case that
  truncated the general mechanism, rather than adding machinery.
- **Performance:** no new work on any path; strictly removes two slice-rebuild calls per fire.
- **Blast radius:** interrupting paths, reverse/terminate/compensation sweeps, arm-time, and the
  runtime correlation tables (ADR-0123) are all untouched. The arm simply lives longer.

## Testing strategy

TDD, observable RED before each GREEN.

- **Engine (`engine/…_test.go`):** for a non-interrupting boundary (signal + message) and a
  non-interrupting event-sub (signal), deliver the trigger **twice** and assert the arm is
  **still present** after each fire and that a **second parallel token / child scope** is
  spawned (repeat-fire). Convert the existing arm-gone assertions
  (`step_events_test.go:878`, `step_boundaries_test.go:299`,
  `step_subprocess_eventstart_test.go:226`) to arm-still-present.
- **Recurring-timer repeat:** a non-interrupting recurring-timer boundary fires on two
  successive `TimerFired` (same `TimerID`) and spawns two tokens; assert the arm survives.
- **Self-cascade regression:** `TestNonInterruptingBoundarySignalNoSelfCascade` must stay green
  (single delivery → one spawn).
- **Runtime e2e (`runtime/eventsub_correlation_e2e_test.go`):** update the non-interrupting
  message/signal cases to assert the arm **survives** the first delivery, and add a **second
  delivery** that fires the event-sub again (two child scopes drained).
- **Leak fix:** assert a non-interrupting recurring-timer boundary emits `CancelTimer` for its
  timer when the host completes (arm retained → sweep can cancel).
- **Terminal-guard (runtime):** an instance that completes with a still-armed root event-sub
  registers **no** message/signal waiter for the dead instance — after completion, a delivery of
  that message name is not swallowed by the terminal instance (assert via `findMessageWaiter`
  absence, or that a subsequent same-name message-start creates a fresh instance). Covers both a
  *fired-then-completed* repeatable arm and a *never-fired* arm (the retroactive ADR-0123 fix).

Coverage ≥ 85% on touched packages; `go test ./...` clean; `golangci-lint run ./...` clean.

## ADR

Record as ADR-0124 (Nygard): Context = one-shot non-interrupting is an incomplete BPMN
implementation caused by two arm deletions; Decision = non-interrupting ⟹ repeatable by default
(remove the two deletions), no new API, per-delivery-once preserved; Consequences = BPMN-correct
repeatable firing, latent recurring-timer leak fixed, one-shot tests flipped to repeatable,
opt-in flag rejected (non-BPMN).

# Engine Core — Events & Event-Based Gateway (Plan 6 of 8) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development (or executing-plans). Checkbox steps.
>
> **Handover note:** Targets the design spec (§3/§5/§6) and assumes Plans 1–5 merged. Contracts are fixed by the spec; ground exact edits against current code. SDD review loop is the safety net. Boundary events here reuse Plan-5 `ScheduleTimer` (timer) and this plan's signal/message awaits; **boundary *error* events are deferred to Plan 8** (errors).

**Goal:** Add Signal and Message catch/throw events, the Event-Based gateway (race several catch events; first wins, rest cancel), and Boundary events (timer + signal; interrupting + non-interrupting) attached to activities.

**Architecture:** Catch events park a token awaiting a named correlation; the runtime delivers `SignalReceived{Name}` (broadcast — fan out to every waiting instance) or `MessageReceived{Name,CorrelationKey}` (to one correlated instance) as triggers. The event-based gateway arms all of its downstream catch events at once and, on the first to fire, takes that branch and cancels the siblings. Boundary events are armed while their host activity is parked.

**Tech Stack:** Go 1.25, `expr-lang/expr` (correlation keys may be expr), `clockwork` (boundary timers, via the `clock` port), `testify`.

## Global Constraints

- Go **1.25**; root packages; engine pure (no transport/storage/bus/time-vendor; no `time.Now()` — boundary-timer `FireAt` from `OccurredAt + dur`). `Step` deterministic + pure; signature unchanged. Black-box tests, `assert`-closure tables, `t.Context()`, fake clock for boundary timers. Coverage ≥ 85% touched; `-race` green; lint clean. Conventional Commits; commit per green step.

## Prerequisite & contracts

- Trigger (sealed): `SignalReceived{Name string; Payload map[string]any}`, `MessageReceived{Name string; CorrelationKey string; Payload map[string]any}` (both spec §5).
- Command (sealed): `ThrowSignal{Name string; Payload map[string]any}` (relayed by the runtime as a broadcast) — modeled as a specialized `EmitEvent`, or a distinct command; pick a distinct `ThrowSignal` for clarity. (Message *send* maps to a `ServiceTask`/`SendTask` via the catalog — not a new command.)
- model `Node`: catch events carry `SignalName` / `MessageName` / `CorrelationKey` (expr). Boundary events carry `AttachedTo string` (host activity node id), `Interrupting bool`, and the event kind (`TimerDuration` | `SignalName`). `KindEventBasedGateway` outgoing flows must each target a catch event (validated).
- `Token` awaiting a signal/message parks with a correlation stored in `AwaitCommand` using a namespaced key (e.g. `sig:<name>` / `msg:<name>`), or add explicit `AwaitSignal`/`AwaitMessage` fields to `Token` for clarity (preferred — avoids overloading `AwaitCommand`).

---

## File Structure

```
model/definition.go        # MODIFY: signal/message/correlation + boundary attachment fields; validate event-based gateway + boundary attachment
model/validate.go          # MODIFY: event-based gateway flows → catch events; boundary AttachedTo resolves
engine/trigger.go          # MODIFY: SignalReceived, MessageReceived (+ constructors)
engine/command.go          # MODIFY: ThrowSignal
engine/state.go            # MODIFY: Token await fields for signal/message; armed-boundary bookkeeping
engine/step.go             # MODIFY: catch events; throw; event-based gateway; boundary arming + firing; signal/message handlers
engine/step_events_test.go
runtime/runner.go          # MODIFY: perform ThrowSignal (broadcast), deliver Signal/Message
runtime/broadcast.go       # SignalBus: in-memory broadcast to waiting instances
runtime/events_example_test.go
```

---

### Task 1: model fields + validation for events and boundaries

- [ ] **RED:** `model` tests — a catch signal node with `SignalName`; a boundary event with `AttachedTo` + `Interrupting`; an event-based gateway whose outgoing flows target catch events. Invalid: boundary `AttachedTo` referencing a missing/non-activity node (`ErrBoundaryAttachment`); event-based gateway flow targeting a non-catch node (`ErrEventGatewayTarget`). Run → fails.
- [ ] **GREEN:** add `Node` fields (`SignalName`, `MessageName`, `CorrelationKey`, `AttachedTo`, `Interrupting`); add `Validate` rules + sentinels. Run → pass.
- [ ] Commit `feat(model): event/boundary node fields and validation`.

---

### Task 2: Signal & Message intermediate catch + throw

**Behavior:** `drive` `KindIntermediateCatchEvent` (signal/message variant): park the token awaiting the named signal/message (set `Token.AwaitSignal`/`AwaitMessage`; evaluate `CorrelationKey` for messages). `Step` `SignalReceived{Name}`: resume **every** token awaiting that signal name (broadcast semantics within the instance), advancing each; `MessageReceived{Name,CorrelationKey}`: resume the single token whose awaited name+key match. Intermediate **throw** signal (`KindIntermediateThrowEvent` with `SignalName`): emit `ThrowSignal{Name,Payload}` and continue along the single outgoing flow.

- [ ] **RED:** `step_events_test.go` — `TestSignalCatchResumesOnSignal` (park, then `SignalReceived` resumes), `TestMessageCatchCorrelates` (only the matching key resumes), `TestSignalThrowEmitsCommand`. Run → fails.
- [ ] **GREEN:** implement catch parking, the two trigger handlers, and throw. Add `Token.AwaitSignal`/`AwaitMessage` + `cloneState` coverage. Run → pass.
- [ ] Commit `feat(engine): signal/message catch and signal throw`.

---

### Task 3: Event-Based gateway (first-event-wins)

**Behavior:** `drive` `KindEventBasedGateway`: for each outgoing flow's target catch event, **arm** it — timer targets emit `ScheduleTimer`; signal/message targets park an await keyed to this gateway instance (so siblings can be cancelled). Park the gateway token (or model the armed set as token bookkeeping). When the **first** armed event fires (`TimerFired`/`SignalReceived`/`MessageReceived` correlated to this gateway), take that branch (advance the token to/through the chosen catch event's outgoing) and **cancel the siblings** (`CancelTimer` for timer arms; drop signal/message awaits). Exactly one branch proceeds.

- [ ] **RED:** `TestEventGatewayFirstTimerWins` and `TestEventGatewayFirstSignalWins`: a gateway racing a timer vs a signal; firing one cancels the other (assert a `CancelTimer` for the loser timer and that a late trigger for the cancelled sibling is a no-op). Run → fails.
- [ ] **GREEN:** implement arming, first-wins selection, and sibling cancellation (track armed event IDs per gateway in state). Run → pass.
- [ ] Commit `feat(engine): event-based gateway with first-event-wins and sibling cancellation`.

---

### Task 4: Boundary events (timer + signal; interrupting + non-interrupting)

**Behavior:** when a host activity token parks (ServiceTask awaiting action, UserTask awaiting human, or a wait state), **arm** its boundary events: timer boundaries emit `ScheduleTimer{FireAt:at+dur}`; signal boundaries register an await. On a boundary event firing while the host is still parked:
- **Interrupting:** cancel the host activity (consume its parked token + `CancelTimer`/drop the host's own awaits and any sibling boundaries), then route a token along the boundary event's outgoing flow.
- **Non-interrupting:** leave the host parked; spawn an **additional** Active token along the boundary's outgoing flow (re-arm the boundary if it is repeating — out of scope unless trivial).
- If the host completes first, cancel its still-armed boundary events.

- [ ] **RED:** `TestInterruptingBoundaryTimerCancelsHost` (host user task; boundary timer fires → host token gone, escalation path runs, a late `HumanCompleted` is a no-op) and `TestNonInterruptingBoundarySpawnsParallelToken` (boundary fires → extra token on boundary path; host still completable). Run → fails.
- [ ] **GREEN:** implement boundary arming on park, firing semantics for both modes, and cancellation on host completion. Run `go test -race ./...`, coverage, lint. Run → pass/clean.
- [ ] Commit `feat(engine): boundary timer/signal events (interrupting + non-interrupting)`.

---

### Task 5: runtime broadcast + correlation + e2e

**Behavior:** `runtime/broadcast.go` `SignalBus` — `Publish(name, payload)` fans out `SignalReceived` to every instance with a token awaiting that signal (the runtime tracks waiting instances, or re-delivers per instance on demand). `runner.perform` handles `ThrowSignal` by publishing to the `SignalBus`. Message delivery is a runtime API `DeliverMessage(name, correlationKey, payload)` that targets the correlated instance. e2e: two instances waiting on the same signal both advance on one publish; an event-based gateway race resolves under a fake clock.

- [ ] **RED:** `events_example_test.go`. Run → fails.
- [ ] **GREEN:** implement `SignalBus`, `perform ThrowSignal`, message delivery, wiring. `-race`, coverage, lint green.
- [ ] Commit `feat(runtime): signal broadcast bus, message correlation, events e2e`.

---

## Verification Checklist (Plan 6)

- [ ] Signal catch resumes on `SignalReceived`; message catch resumes only on matching name+correlation key.
- [ ] Signal throw emits `ThrowSignal`; the runtime broadcasts to all waiting instances.
- [ ] Event-based gateway takes the first event to fire and cancels siblings (`CancelTimer` for timer arms; late sibling triggers are no-ops).
- [ ] Interrupting boundary cancels the host and routes the boundary path; non-interrupting boundary spawns a parallel token and leaves the host running; host completion cancels armed boundaries.
- [ ] `Step` deterministic + pure; engine no `time.Now()`/`clockwork`; `-race` green; coverage ≥ 85%; lint clean.

## Self-Review Notes

- **Spec coverage:** §3 intermediate catch/throw + boundary + event-based gateway; §5 SignalReceived/MessageReceived. Boundary *error* events → Plan 8 (need error throw). Message *send* uses the action catalog (SendTask), not a new command.
- **Determinism:** signal resume order over tokens follows slice order; armed event IDs from counters; boundary timer `FireAt` from `OccurredAt`. Correlation keys evaluated via `expreval`.
- **Cancellation correctness:** first-wins and interrupting-boundary both rely on dropping sibling awaits + `CancelTimer`; tests assert late triggers for cancelled arms are no-ops (the parked token/await is already gone).
- **Grounding required:** read merged `engine/step.go` (timer + human-task parking from Plans 4–5), `runtime/scheduler.go`, `runtime/runner.go` before editing. Several `NewRunner`/wiring changes — update call sites/tests per task.
- **Scope caution:** boundary events interacting with sub-process scopes are fully realized in Plan 7 (scopes); here boundaries attach to flat activities. Repeating/cycle timers are out of scope.

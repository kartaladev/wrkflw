# BPMN2 Alignment — Design (umbrella spec)

- **Status:** Approved (design), 2026-07-10
- **Branch:** `feat/bpmn2-alignment` (off `main == 67df128`)
- **ADRs:** 0117–0122 (ADR-0117 = authz-API relaxation; 0118–0122 = one per feature)
- **Author:** brainstorming session 2026-07-10

## Purpose

Fold five BPMN2 concepts into the engine's **existing** node kinds, retiring bespoke
machinery rather than adding new node types. The goal is a model that is more faithful to
the BPMN2 metamodel (where "event sub-process", "terminate end", and "manual task" are
*modes/flags* on existing elements, not distinct kinds) while **reducing** the surface area
the engine must maintain.

Grounding note: the engine already carries much of the raw material. Start-event trigger
fields (`SignalName`/`MessageName`/`CorrelationKey`/`Timer`) exist and round-trip
(`definition/event/event.go`); the compensate-throw is already modeled as an
`IntermediateThrowEvent` variant with a documented but unimplemented "empty = scope-wide"
case (`engine/step_nodes.go:874`, doc at `event.go:81`); `TerminateEndEvent` exists as a
kind but has **no terminate implementation** — it parks as an unhandled kind
(`engine/step_nodes.go:58`, `engine/step.go:156-161`). So several features are "finish what
is stubbed" more than "build from scratch."

**The library is unreleased.** Therefore every wire change in this effort is a **clean
break** — no name aliases, no `Migrator` passes, no back-compat shims. Old stored
definitions using retired wire names (`terminateEndEvent`, `eventSubProcess`) are not
supported; the definition wire version is bumped.

## Scope

A prerequisite authz-API relaxation (#0) plus five features, delivered on one branch, each
with its own ADR and `examples/scenarios/*`:

0. **#0 Optional co-equal UserTask eligibility** — make roles/privileges/attribute all
   optional, co-equal options; `NewUserTask(id, opts...)` + `WithCandidateRoles`. Prerequisite
   for the clean manual-task story.
1. **#5 Manual user-task mode** — `WithManual()` on `UserTask` (a no-eligibility UserTask).
2. **#2 Unified end event** — fold `TerminateEndEvent` into `EndEvent` via `WithForceTermination`.
3. **#3 Scope-wide compensation throw** — implement the empty-ref compensate throw.
4. **#1 Event-based start events** — start an instance from a message/signal/timer; allow
   multiple start events per definition.
5. **#4 Remove `EventSubProcess`** — make `activity.SubProcess` event-triggered-capable via #1.

### Build order

`#0 (eligibility relaxation) → #5 → #2 → #3 → #1 → #4`.

Rationale: #0 is a prerequisite for #5 (manual = a no-eligibility UserTask, which reads
cleanly only once roles are an optional option) and both touch the same UserTask file surface,
so they ship in one plan. #5 and #2 are small and wire-additive (build momentum). #3 is
engine-contained. #1 is the architecturally deepest (pre-instance subscription ownership) and
must land before #4. #4 deletes the most code and retires freshly-hardened machinery, so it
goes last, behind a full test-parity port.

### Out of scope

- BPMN2 XML loader (YAML + Go authoring only, per CLAUDE.md).
- Correlation across message *and* signal on the same start (a start is one trigger type).
- Non-interrupting semantics beyond what `NonInterrupting` already expresses.

## Dependency graph

```
#1 event-based START ──unlocks──▶ #4 EventSubProcess removal  (ESP ≡ SubProcess w/ event start)
#2 terminate end      ── independent
#3 compensate throw   ── independent (finishes existing empty-ref path)
#5 manual task        ── independent (smallest)
```

Only `#1 → #4` is a hard dependency. #2, #3, #5 are independent of everything.

## The one shared seam — trigger routing (introduced by #1, reused by #4)

Today message/signal/timer delivery routes **only to already-running instances** via the
runtime waiter tables (`runtime/processdriver_waiters.go`: `msgWaiters`, `sigbus.Sync`).
`DeliverMessage`/`BroadcastSignal` (`runtime/processdriver_message.go`,
`processdriver_signal.go`) never create instances. Feature #1 adds a consumer that has **no
instance yet**.

Introduce a **trigger-routing layer** in `ProcessDriver`:

1. On inbound message/signal/timer, **first** correlate to a running instance (existing
   waiter lookup — matches BPMN: a message with an active correlation key targets the
   running instance, it does not spawn a duplicate).
2. **On miss**, consult a new **start-subscription registry**: does any registered
   definition have an event-start that matches? If so, mint and drive a new instance.

**Start-subscription registry** (fork F1a, decided):

- Lives in `ProcessDriver`, in-memory, populated at `RegisterDefinition`, consulted after
  the running-instance lookup.
- Behind an **interface** so a durable consumer can back it with a store; the default is
  in-memory.
- **Rehydrated on boot** analogous to `RehydrateTimers` (`runtime/timerops.go:206`) — in
  particular timer-starts must re-register their schedule with the scheduler on restart.

#4's event-sub-process arming becomes just another subscriber to this same delivery path,
rather than the bespoke `armEventSubprocesses` scan it is today.

---

## ADR-0117 — Optional co-equal UserTask eligibility (#0)

**Context.** Authorization must facilitate role-based (RBAC), resource-privilege-based (a
privilege held by a role *or* a user), and attribute-based (data/process-variable) evaluation
— all co-equal. Today the model already carries all three (`CandidateRoles`,
`EligibilityPrivileges`, `EligibilityExpr`) and the runtime already treats an empty
`authz.AuthzSpec` as "open" (permits) so a consumer can authorize at the transport layer (e.g.
HTTP security middleware). But the **authoring API** privileges RBAC: `NewUserTask(id, roles
[]string, opts...)` takes roles as a *mandatory positional* argument while privileges and
attribute are optional options (`WithEligibilityPrivileges`, `WithEligibilityExpr`). This
makes RBAC feel required and the other dimensions second-class.

**Decision.**
- Change the constructor to `NewUserTask(id string, opts ...UserTaskOption)` — **drop the
  positional `roles`**. Add `WithCandidateRoles(roles ...string) UserTaskOption`. All three
  eligibility dimensions are now co-equal, optional options. Breaking signature change; the
  library is unreleased, so it is a clean break (update all call sites, examples, tests).
- An empty eligibility spec (no roles, no privileges, no attribute) means **no engine-level
  gate** — authorization is deferred to the consumer's transport layer. This is already the
  runtime behavior (`task.Complete`/`Claim` authorize against the task's `Eligibility`; an
  empty spec is permitted); this ADR only aligns the authoring API with it.
- Out of scope: the "privilege held by role *or* user" resolution is a casbin-adapter
  capability, not a definition-model change — noted, not modified here.

**Consequences.** RBAC is no longer privileged in the API. Manual tasks (#5/ADR-0118) become
naturally expressible as a UserTask with no eligibility. Every existing `NewUserTask(id, roles,
...)` call site changes. No wire change (fields unchanged; only the constructor moves roles
from positional to option).

**Example.** Covered by the retrofit of existing user-task scenarios plus the manual-task
example (ADR-0118).

---

## ADR-0118 — Manual user-task mode (#5)

**Model.** Add `Manual bool` to `activity.UserTask`; add `WithManual() UserTaskOption`. Builds
on ADR-0117's optional constructor — a manual task is a UserTask with no eligibility and
`WithManual()`. Wire: additive `manual` bool in `NodeWire`, round-tripped in the `UserTask`
spec.

**Behavior.** A manual task **parks and waits for a bare completion trigger** — the
completion carries no payload/form-data, and `CompletionValidation` is skipped (there is no
form to validate). It still creates a durable human-task record so there is a "someone
confirmed this happened" checkpoint. This is the useful behavior, and it **diverges from
strict BPMN Manual Task** (which has no execution semantics / auto-completes); the divergence
is documented in the ADR.

**Decided micro-decisions.**
- Option `WithManual()`, not a separate `NewManualTask` kind (matches the repo's
  functional-options convention; no new wire kind).
- Authz: handled by ADR-0117 — a manual task simply carries no eligibility, so the engine
  gate is open and authorization defers to the transport layer. If roles/privileges/attribute
  are set (via the ADR-0117 options), the existing claim/authz gate applies unchanged.
- Build-time **error** if `WithManual` is combined with `WithCompletionValidation`
  (contradictory — a manual task has no form to validate).

**Example.** `examples/scenarios/manual_task` — an onboarding "hand over badge" step: no
form, an operator triggers completion to advance.

**Engine touch-points.** `handleHumanCompleted` (`engine/step_triggers.go:476-524`) already
tolerates empty `Output`; the flag mainly documents intent, gates the validation
combination, and bypasses authz when roleless.

---

## ADR-0119 — Unified end event with force-termination (#2)

**Model.**
- Delete `TerminateEndEvent`, `KindTerminateEndEvent`, `NewTerminateEnd`, and the
  `terminateEndEvent` wire name (clean break — unreleased).
- Add to `EndEvent`: `ForceTermination bool`, `TerminationReason string`, and a terminal
  outcome selector `Outcome TerminationOutcome`.
- API: `WithForceTermination(reason string, outcome TerminationOutcome) EndOption`, where
  `TerminationOutcome ∈ {OutcomeComplete, OutcomeAbort}`. `OutcomeComplete` → the instance
  reaches `StatusCompleted` (a successful business halt that cancels remaining parallel
  work); `OutcomeAbort` → `StatusTerminated` (an abort). This satisfies the requirement that
  **both** terminal outcomes be selectable at authoring time.
- `NewEnd` gains an `EndOption` interface (like the other event kinds), keeping a name-only
  shim for the common case.
- Wire: additive `forceTermination` bool, `terminationReason` string, `terminationOutcome`
  on `NodeWire`, round-tripped in the `EndEvent` spec.

**Engine.** This is the **first real terminate implementation**. `endEventStrategy.enter`
(`engine/step_nodes.go:206-233`) checks `ForceTermination`; when set, it runs the
cancel-cleanup already written for `handleCancelRequested` (`engine/step_triggers.go`): cancel
all tokens, timers, arms/boundaries, ESP arms, reconcile open tasks — then ends at the
selected status (`StatusCompleted` or `StatusTerminated`) carrying `TerminationReason`.
Reuses `cancelAllTimers`, `cancelAllArmsAndBoundaries`, `removeAllEventSubprocessArms`,
`cancelOpenTasks`.

**Decided micro-decision.** The "only meaningful when the definition has multiple end events"
rule is a **Build-time WARN (lint)**, not a hard error — a single-end def with
force-termination is merely redundant.

**Example.** `examples/scenarios/terminate_end` — a parallel fork where one branch, on a
business condition, hits `NewEnd("halt", WithForceTermination("fraud detected", OutcomeAbort))`
and cancels the sibling branch's in-flight task; a companion path shows `OutcomeComplete`.

---

## ADR-0120 — Scope-wide compensation throw (#3)

**Model.** Implement the `CompensateRef == ""` (scope-wide) branch on
`IntermediateThrowEvent` — the field doc already anticipates "empty = scope-wide"
(`definition/event/event.go:81`). Add a `NewCompensateThrow(id) model.Node` convenience
constructor that delegates to an `IntermediateThrowEvent` with an empty `CompensateRef`. **No
new wire kind** — `CompensateRef` already round-trips.

**Behavior (fork F3b, decided — throwing-scope, BPMN-faithful).** When a token reaches the
throw:
- Collect the **throwing scope's** completed compensable activities (those that recorded a
  `CompensationRecord` because they carried a `CompensateAction`). At root scope this is the
  whole instance; inside a sub-process it is that scope only.
- Fire their compensate-actions in **reverse instantiation order** (the existing single-cursor
  design already gives deterministic order).
- **Do not propagate** compensation to enclosing scopes.
- **Do not terminate** — after the walk drains, **resume forward** past the throw node
  (BPMN throw-then-continue), unlike `beginCompensation` which is terminating.

**Concurrency.** Reuse the existing `DeferredCompensationThrows` serialization
(`engine/step_nodes.go:897-909`): a scope-wide throw while another walk is in flight defers,
it does not overwrite the cursor.

**Example.** `examples/scenarios/compensation_throw` (distinct from `compensation_saga` /
`reverse_rollback`) — reserve-hotel + reserve-car each carry `WithCompensateAction`; a
downstream validation failure routes to a `NewCompensateThrow` that rolls both back in reverse
order, then continues to a "notify customer" task rather than terminating.

---

## ADR-0121 — Event-based start events (#1)

**Model.** No new fields — the trigger fields already exist on `StartEvent`. Relax validation
(`definition/model/validate`): a definition has **≥1 start event**; **at most one "none"
(plain, trigger-less) start**; every event-start must carry a coherent message/signal/timer
trigger (message-start needs `MessageName`, optional `CorrelationKey`; signal-start needs
`SignalName`; timer-start needs a parseable `Timer` schedule).

**Multiple start events (fork F1c, decided — allow).** Lift the current single-start invariant
(`engine/step_triggers.go:22-25`, `validate` `len(starts)!=1`). A definition may have N start
events, each event-based start registering its **own** subscription.

**Runtime facade** (siblings of `DeliverMessage`/`BroadcastSignal`):
- `StartByMessage(ctx, def, message, correlationKey, payload) (InstanceState, error)` —
  correlated, point-to-point. Per the shared seam, running-instance correlation is tried
  first; only a miss starts a new instance seeded with `payload`, initial token on the
  matching start node.
- `StartBySignal(ctx, def, name, payload)` — broadcast, **no correlation, may start multiple
  definitions** (fan-out) and still resume running waiters.
- **Timer start** — the start node's `Timer` schedule is registered with the scheduler port at
  `RegisterDefinition` and re-registered on boot (rehydration); each fire starts one instance.

**Plain-`Drive` resolution (decided).** Plain `Drive`/`StartInstance` with caller-supplied
vars uses the **none-start** if one is present. If a definition has **only** event-starts,
plain `Drive` **errors** ("use an event entry point: StartByMessage/StartBySignal/timer").

**Engine.** `handleStartInstance` (`engine/step_triggers.go:15-38`) is generalized to place
the initial token on the *resolved* start node (by which trigger fired, or the none-start for
plain drive) rather than assuming the sole start. The start node's trigger fields become the
documented entry contract and, optionally, an input-validation gate (reuse the existing
`InputValidation` slot at `event.go:28`).

**Persistence.** Zero wire change to the node. New durable concern: the start-subscription
registry (see the shared seam) — in-memory by default, interface-backed for durable
consumers, rehydrated on boot (timer-starts especially).

**BPMN alignment.** Message start = correlated/point-to-point (correlate-to-running first,
then create). Signal start = broadcast/no-correlation/fan-out. Timer start = scheduler-driven,
one instance per fire.

**Example.** `examples/scenarios/event_start_message` — an order-fulfilment definition whose
start event carries `WithMessageCorrelator("order.created", "orderId")`; the example calls
`driver.StartByMessage(...)` and shows a second delivery with the same key correlating to the
*running* instance rather than starting a duplicate.

---

## ADR-0122 — Event sub-process as event-triggered sub-process (#4)

**Model.** Delete `EventSubProcess`, `KindEventSubProcess`, `NewEventSubProcess`,
`WithEventSubProcessNonInterrupting`, and the `eventSubProcess` wire name (clean break —
unreleased; definition wire version bumped). Add `WithSubProcessNonInterrupting()` to
`activity.SubProcess`. An event-sub-process ≡ a `SubProcess` whose inner start event is
event-triggered (message/signal/timer) via #1; a plain `SubProcess` with a *none* start keeps
today's inline-drive behavior.

**Engine.** Generalize the arming machinery to key off `activity.SubProcess` + a predicate
`isEventTriggeredStart(sub.Subprocess.StartNodes()...)` instead of the `event.EventSubProcess`
type:
- `armEventSubprocesses` (`engine/step_eventsubprocess.go:26-78`) scans `activity.SubProcess`
  nodes whose inner start is event-triggered.
- `fireEventSubprocessArm` and the scope-drain detection in `endEventStrategy`
  (`engine/step_nodes.go:246-317`, incl. the root-ESP "Fix 1/Fix 2" edge cases) switch their
  type assertions from `event.EventSubProcess` to `activity.SubProcess`.

**Risk & mitigation.** This retires machinery hardened very recently (ESP-arm timer sweeps,
scope-drain edge cases). Highest regression risk in the effort. Mitigation: **port the full
existing ESP test suite** (`state_esp_test.go`, the ESP cases in `reverse_instance_test.go`)
to the `SubProcess`-with-event-start form and assert behavior parity **before** deleting the
`EventSubProcess` kind.

**Related pre-existing ticket (mooted by this feature).** Normal success completion
(`engine/step_nodes.go` end-event → `StatusCompleted`) does not sweep outstanding root-ESP arms
+ their timers (only the terminate paths do, via the reverse-instance FU#2 work). Once ESP
machinery is retired here, the ESP-specific leak is moot; the general question ("does
`TimerFired` no-op on a terminal-status instance?") is tracked separately.

**Example.** Convert `examples/scenarios/subprocess_embedded` (or add
`examples/scenarios/event_subprocess`) to show a `SubProcess` with a message-triggered inner
start acting as a non-interrupting event sub-process.

---

## Cross-cutting concerns

- **Migration:** none. Unreleased library ⇒ clean breaks for `terminateEndEvent` and
  `eventSubProcess`; definition wire version bumped once for the branch.
- **Observability:** new facade methods (`StartByMessage/StartBySignal`, timer-start) get spans
  + metrics consistent with `DeliverMessage`/`BroadcastSignal`; force-termination and
  compensate-throw log via `slog` with the reason/scope.
- **Testing:** TDD per CLAUDE.md (visible RED before GREEN). Black-box `_test.go` per file.
  #4 gated on the ESP test-parity port. Testable examples for the new public facade methods.
- **Risks (ranked):** (1) #1 subscription ownership — genuinely new architecture (pre-instance
  state + rehydration). (2) #4 retiring freshly-hardened ESP machinery — mitigate with the
  parity port. (3) #3 scope containment — wrong scope silently over-compensates (decided:
  throwing-scope).

## Verification checklist (umbrella)

- [ ] Each decision has its own ADR (0117 authz relaxation + 0118–0122 features, Nygard
  template) under `docs/adr/`.
- [ ] Each feature has an `examples/scenarios/*`.
- [ ] `go test -race ./...` green from repo root; touched packages ≥ 85% line coverage.
- [ ] `golangci-lint run ./...` clean.
- [ ] #4 lands only after the ESP test-parity port passes on the `SubProcess` form.
- [ ] `#1 → #4` order respected; #5/#2/#3 independently green before #1/#4.

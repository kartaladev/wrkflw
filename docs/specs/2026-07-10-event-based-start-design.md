# Event-based Start Events — Design (ADR-0121)

- **Status:** Approved (design), 2026-07-10
- **Branch:** `feat/bpmn2-event-start` (off `main == 1f28e64`)
- **ADR:** 0121 (event-based start events; feature #1 of the BPMN2-alignment set)
- **Supersedes:** the ADR-0121 section of the umbrella spec
  `docs/specs/2026-07-10-bpmn2-alignment-design.md` (which proposed a durable
  `StartSubscriptionStore` and new `StartByMessage`/`StartBySignal` facade methods —
  both dropped here after re-brainstorming against the current source).

## Context

A process instance can be created today only by `ProcessDriver.Drive` /
`engine.StartInstance` (a caller-driven "none" start). BPMN2 also lets an instance be
**started by an event** — a message, a signal, or a timer. The `StartEvent` model already
carries the trigger fields (`SignalName`, `MessageName`, `CorrelationKey`, `Timer`,
`InputValidation`) with their options and full wire round-trip (`definition/event/event.go`,
`definition/event/options.go`), but **instance-creation-from-event is 0% built**. This feature
builds it.

Two invariants block it today and must be lifted:

- `engine/step_triggers.go:23` — `handleStartInstance` errors unless `len(starts) == 1` and
  hard-codes `starts[0]`.
- `definition/model/validate.go:196` — `ErrMultipleStartEvents` for `len(starts) > 1`;
  reachability (`validate.go:286`) only runs with exactly one start.

### BPMN2 semantics (researched, 2026-07-10)

Grounded in the BPMN2 spec via the Camunda reference implementation
([Message Events](https://docs.camunda.org/manual/latest/reference/bpmn20/events/message-events/),
[Signal Events](https://docs.camunda.org/manual/7.5/reference/bpmn20/events/signal-events/),
[Start Events](https://docs.camunda.org/manual/7.5/reference/bpmn20/events/start-events/)):

- **Signal start = broadcast, 1:N fan-out.** A published signal starts **one instance per
  registered definition** that has a matching signal-start. The signal name **need not be
  unique** across definitions. **No correlation** is performed or possible — it is a "signal
  flare."
- **Message start = addressed, correlation-controlled.** A message name is **unique** (one
  subscription per message-start). The **correlation key controls instantiation**: if a
  running instance for the same key exists, the message **correlates to it** (no new
  instance); otherwise a new instance is **created**. I.e. *correlate-to-running-first, then
  create*.
- **Timer start = scheduler-driven.** The start node's timer schedule fires autonomously; each
  fire creates one instance (recurring schedules keep firing while the definition is
  registered).

The publisher **never names the target definition** — it emits `name` (+ `key` for messages)
and a payload; the engine resolves who correlates or starts.

## Decision

### 1. Model & validation (`definition/`)

No model changes. Relax `definition/model/validate.go`:

- Lift the single-start invariant. New structural rules:
  - **≥1 start event** (`ErrNoStartEvent` unchanged).
  - **≤1 "none" start** — a start with no trigger fields set. `ErrMultipleNoneStarts`
    (new) when two or more.
  - **Each event-start carries a coherent trigger**: message-start requires `MessageName`;
    signal-start requires `SignalName`; timer-start requires a non-nil parseable `Timer`. A
    start with more than one trigger family set is rejected (`ErrAmbiguousStartTrigger`, new).
- **Reachability** (`validate.go:286`) runs from the **union of all start nodes** rather than
  the sole start. A node reachable from *any* start is reachable.
- Message-start **name uniqueness is cross-definition** and therefore cannot be a structural
  (single-definition) check — it is enforced at registration (§4).

A "none" start is detected by the absence of all of `MessageName`, `SignalName`, and `Timer`.

### 2. Engine seam — multiple starts (`engine/`)

- Lift the `len(starts) != 1` guard at `step_triggers.go:23`.
- Add `StartNodeID string` to the `StartInstance` trigger (`engine/trigger.go:25`):
  - **empty ⇒ resolve the none-start** (plain drive). If there is no none-start, error
    (`ErrNoNoneStart`, surfaced through the runtime as the plain-`Drive` error in §6).
  - **non-empty ⇒ place the initial token on that node.**
- The **driver decides which start node** fired (by correlating the trigger to a start node);
  the **engine only places the token**. This mirrors the ADR-0115 "runtime decides / engine
  executes" seam and keeps the engine free of message/signal/registry concerns.
- `handleStartInstance` generalizes: resolve the node from `t.StartNodeID` (or the none-start),
  place the token, arm event sub-processes, drive.

### 3. Runtime — internal event-start unit + reused facades (`runtime/`)

Event-start orchestration lives in an **`internal`-to-the-`runtime`-package `eventStart`
collaborator composed into `ProcessDriver`** (not a separate consumer-wired component, and not
scattered across the driver). It holds the correlate-then-create / fan-out / node-resolution
logic and calls driver primitives (correlate, create-at-node). **No new public facade
methods** — event-start rides the existing entry points, which become **def-less publish**
operations:

- **`DeliverMessage(ctx, name, key, payload)`** — **drops the `def` parameter.**
  1. Correlate to a running waiter by `(name, key)` via the existing `msgWaiters` table
     (`findMessageWaiter`). On a hit, resolve *that instance's* def from
     `InstanceState.DefID`/`DefVersion` via `defsReg.Lookup` and `ApplyTrigger` (resume) — the
     caller no longer supplies it.
  2. On a miss, find the **unique** message-start def for `name` (via §4 enumeration) and
     **create** a new instance, seeded with `key` as the correlation value and `payload` as
     start vars, initial token on the matching start node. Steps 1–2 run inside the
     per-`(name,key)` guarded critical section of §8 (correlate-check-then-create is atomic).
  3. No running waiter and no message-start match ⇒ clean no-op (today's behavior preserved
     for genuinely-unmatched messages).
- **`BroadcastSignal(ctx, name, payload)`** — signature unchanged (already def-less).
  1. Resume all parked waiters via the `sigbus` (today's behavior).
  2. **Additionally create one instance per registered def** with a matching signal-start
     (fan-out). The nil-`sigbus` guard is relaxed: with no bus but ≥1 signal-start match,
     proceed to create; error only when there is neither a bus nor any signal-start.

Blast radius of dropping `def` from `DeliverMessage` (clean break — unreleased):
`service.DeliverMessageRequest` loses its `DefRef`; the four transport layers
(`transport/http/{httpcore,stdlib,gin,fiber}`) and `service/service.go:362` simplify; the 7
example call sites update. All mechanical.

### 4. Registry enumeration capability + message-name uniqueness (`runtime/kernel`, `runtime/`)

- New **opt-in capability** interface (sibling in spirit to the store's `Notifier`/`Locker`,
  ADR-0081):

  ```go
  // DefinitionLister is an optional capability a DefinitionRegistry may implement so
  // the event-start subsystem can find definitions subscribing to a message/signal/timer
  // start. Registries that do not implement it disable event-based *start* (correlate-to-
  // running still works).
  type DefinitionLister interface {
      ListDefinitions(ctx context.Context) []*model.ProcessDefinition
  }
  ```

  `MemDefinitionRegistry` and `MapDefinitionRegistry` implement it trivially (both already hold
  every definition in a map). `CachingDefinitionRegistry` passes through to its delegate.
- **No durable subscription store, no persistent index.** Signal- and message-start matches are
  resolved by a **stateless scan** of `ListDefinitions` filtered by start-node triggers
  (broadcasts and correlation-misses are not hot paths; definition counts are modest). This
  makes dynamic registration "just work" — nothing to keep in sync. (A registry that needs
  scale may later back the capability with an internal index without any public API change.)
- **Message-start name uniqueness** enforced in `RegisterDefinition` / `MustRegisterDefinition`
  (`runtime/definition_registry.go`): the scan-existing-then-register sequence runs under a
  package-level registration mutex so two concurrent registrations cannot both pass the
  uniqueness check (TOCTOU-free). Reject a registration whose message-start name collides with
  an already-registered message-start (`ErrDuplicateMessageStart`, new). Defense-in-depth: the
  `DeliverMessage` create path errors (`ErrAmbiguousMessageStart`) if a scan ever finds >1
  message-start for a name (covers custom-registry consumers that bypass `RegisterDefinition`).

### 5. Timer-start lifecycle + rehydration (`runtime/`)

- Timer-starts are armed at boot by enumerating registered defs — a new explicit
  **`RehydrateStartTimers(ctx) error`** step, a sibling of the existing explicit
  `RehydrateTimers` (`runtime/timerops.go:206`). Kept separate (not folded into `Start`) to
  match the existing "consumer calls the rehydrate step it needs" idiom and to let a consumer
  register all definitions before arming.
- Each fire creates one instance (initial token on the timer-start node); recurring schedules
  keep firing while the definition is registered.
- **No durable store** — start-timers are a pure function of the (re-registered) definitions,
  unlike instance-scoped `RehydrateTimers`, which needs the timer store because those timers
  are tied to running-instance state. `RehydrateStartTimers` re-derives arms from
  `ListDefinitions`.

### 6. Plain `Drive` resolution (`runtime/`)

- `Drive` / `StartInstance` with caller-supplied vars uses the **none-start** if one is
  present (passes empty `StartNodeID`; the engine resolves it).
- A definition with **only** event-starts (no none-start) makes plain `Drive` **error**:
  `"workflow-runtime: definition <id> has no plain start; use an event entry point
  (DeliverMessage / BroadcastSignal / timer start)"`.

### 7. Observability, testing, example

- **Observability:** the create paths get spans + metrics consistent with the existing
  `DeliverMessage`/`BroadcastSignal` instrumentation; timer-start fires log via `slog`.
- **Testing:** TDD per CLAUDE.md (visible RED before GREEN); black-box `_test.go` per file;
  `table-test` (assert-closure, `t.Context()`), `use-mockgen`, `use-testcontainers` as
  applicable. Cover: multi-start validation, `StartNodeID` node resolution, message
  correlate-then-create, signal fan-out across two defs, timer-start fire + rehydrate,
  message-name-uniqueness rejection, plain-`Drive`-errors-on-only-event-starts.
- **Example:** `examples/scenarios/event_start` — an order signal `order.completed` fanning out
  to start **payment** and **shipment**; shipment then parks on a `payment.completed` **message**
  that correlates-then-completes (illustrating both fan-out signal-start and keyed message
  correlation).

### 8. Concurrency & safety

Event-start runs concurrently with all other instance execution; each trigger type has a
distinct hazard, handled with the machinery the repo already provides.

- **Timer-start — multi-replica safe for free.** Timer-starts ride the same scheduler as
  instance timers, so they inherit `scheduling.Elector` (`scheduling/elector.go`): exactly one
  replica is leader and runs all fires. No duplicate instances across replicas; single-node is
  trivially safe. The fire callback creates through the normal (per-instance-safe) create path.
- **Signal-start — safe by design.** No correlation; each `BroadcastSignal` is one fan-out
  round of independent creates, each an isolated instance via the existing concurrency-safe
  create path. Concurrent identical broadcasts starting multiple instances is *correct* signal
  semantics, not a race. The fan-out loop only reads the enumerated definition set.
- **Registration uniqueness — TOCTOU-free.** The message-name scan-then-register in
  `RegisterDefinition` runs under a package-level registration mutex (§4), so concurrent
  registrations cannot both pass.
- **Message-start correlate-then-create — the real hazard (decided: advisory-lock, no new
  schema).** Two identical `(name,key)` messages must not both create.
  - **Single node / single replica (authoritative):** an in-process **active-correlation map**
    `map[msgKey]instanceID` on the `eventStart` unit, guarded by its own mutex, records each
    message-start instance on create and **evicts it on terminal status** (hooked into
    `deliverLoop`'s terminal path). The correlate-check-then-create critical section is
    serialized per `(name,key)` through this mutex, so concurrent identical messages create at
    most one instance and the second correlates to it. SQLite (single-writer, single-node)
    relies solely on this and is fully safe.
  - **Multi-replica (best-effort):** when the store implements the `dialect.Locker` capability
    (`internal/persistence/dialect/dialect.go:182` — Postgres/MySQL), the critical section is
    additionally wrapped in an advisory lock on `hash(name,key)` so replicas serialize. Because
    there is **no shared durable correlation record** (the no-new-table decision), a replica
    that did not create holds no map entry, so a **narrow cross-replica / post-restart window
    can still produce a duplicate**. This is an accepted, documented limitation.
  - **Deferred upgrade path:** full durable, cross-replica, restart-safe dedup is a later ADR —
    a durable correlation subscription keyed by `(messageName, key)` with a UNIQUE constraint,
    checked inside the create transaction. The `eventStart` seam and the advisory-lock wrapper
    are designed so this can be added behind them without changing the public API.
- **Shared in-memory state.** Every shared map — `msgWaiters` (`msgMu`), the `sigbus`
  subscriptions, the new active-correlation map, and any enumeration snapshot — is mutex-guarded
  or copied before iteration; `go test -race` covers the new paths.

### 9. Deliberately dropped (YAGNI)

- No durable `StartSubscriptionStore` / persistent subscription index (umbrella spec).
- No new `StartByMessage` / `StartBySignal` public methods (umbrella spec) — reuse
  `DeliverMessage` / `BroadcastSignal`.
- No cross-definition message *routing* beyond start (correlate-then-create only).

## Consequences

**Positive**

- BPMN2-faithful event starts (message/signal/timer) with the publisher decoupled from the
  target definition.
- Minimal new machinery: one opt-in registry capability, one trigger field, one rehydrate
  step, two relaxed validations — and **no new durable state** (the definition is the source of
  truth).
- Transparent to consumers: register a definition with an event-start trigger and the existing
  delivery paths start instances.

**Negative / risks**

- `DeliverMessage` loses its `def` parameter — a breaking signature change across the service +
  transport layers + examples (mechanical; unreleased ⇒ acceptable).
- `DeliverMessage`'s miss-branch changes from "always no-op" to "create when a message-start
  matches"; `BroadcastSignal` gains instance-creation. Behavior change, documented.
- Stateless enumeration scan is O(N definitions) per broadcast / correlation-miss — fine for
  the target scale, revisitable behind the capability interface without API change.
- Message-name uniqueness enforced at `RegisterDefinition` is best-effort for consumers that
  register directly into a custom registry; the delivery-time `ErrAmbiguousMessageStart` guard
  is the authoritative backstop.
- Message-start dedup is **authoritative single-node, best-effort multi-replica** (advisory
  `dialect.Locker`, no new schema): a narrow cross-replica / post-restart window can still
  duplicate an instance for one correlation key. Documented limitation; durable dedup is a
  deferred follow-up ADR (see §8). Timer-start (Elector) and signal-start (fan-out by design)
  are fully multi-replica safe.
- Unblocks ADR-0122 (EventSubProcess ≡ SubProcess with an event-triggered inner start), which
  depends on this feature.

## Verification checklist

- [ ] `definition/model/validate.go`: ≥1 start, ≤1 none-start, coherent event-start trigger,
      union-of-starts reachability; new sentinels `ErrMultipleNoneStarts`,
      `ErrAmbiguousStartTrigger`.
- [ ] `engine/trigger.go`: `StartInstance.StartNodeID`; `engine/step_triggers.go` resolves node
      (or none-start), token placement generalized; `ErrNoNoneStart`.
- [ ] `runtime/kernel`: `DefinitionLister` capability; Mem/Map implement it; caching passes
      through.
- [ ] `runtime`: `DeliverMessage(ctx, name, key, payload)` (def dropped) correlate-then-create;
      `BroadcastSignal` fan-out create + relaxed nil-bus guard; `eventStart` internal unit.
- [ ] `runtime/definition_registry.go`: message-name uniqueness at register (under a
      registration mutex, `ErrDuplicateMessageStart`); delivery-time `ErrAmbiguousMessageStart`
      backstop.
- [ ] `runtime`: message-start `eventStart` active-correlation map (mutex-guarded, evicted on
      terminal via `deliverLoop`) + `dialect.Locker`-wrapped critical section; `go test -race`
      on concurrent identical-`(name,key)` delivery and concurrent signal fan-out.
- [ ] `runtime`: `RehydrateStartTimers(ctx)`; timer-start fire creates instance.
- [ ] `runtime`: plain `Drive` uses none-start / errors on only-event-starts.
- [ ] `service` + `transport/http/{httpcore,stdlib,gin,fiber}`: `DeliverMessage` def-drop
      propagated; `DeliverMessageRequest.DefRef` removed.
- [ ] `examples/scenarios/event_start` added; the 7 existing `DeliverMessage` example call sites
      updated.
- [ ] ADR `docs/adr/0121-event-based-start.md` (Nygard template).
- [ ] `go test ./...` green, touched packages ≥85% coverage, `golangci-lint run ./...` clean.

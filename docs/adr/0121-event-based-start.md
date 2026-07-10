# 121. Event-based start events

- Status: Accepted
- Date: 2026-07-10

Part of the BPMN2-alignment effort (umbrella spec
`docs/specs/2026-07-10-bpmn2-alignment-design.md`), which folds bespoke node
kinds into flags/modes on existing kinds to be more faithful to the BPMN2
metamodel. See also the design doc,
`docs/specs/2026-07-10-event-based-start-design.md`, which this ADR
distills, and the umbrella spec's own ADR-0121 section, **superseded** here:
that section proposed a durable `StartSubscriptionStore` and new
`StartByMessage`/`StartBySignal` facade methods, both dropped after
re-brainstorming against the current source (see "Deliberately dropped"
below).

## Context

A process instance could be created only by `ProcessDriver.Drive` /
`engine.StartInstance` — a caller-driven "none" start. BPMN2 also lets an
instance be **started by an event**: a message, a signal, or a timer. The
`StartEvent` model already carried the trigger fields (`SignalName`,
`MessageName`, `CorrelationKey`, `Timer`, `InputValidation`) with their
options and full wire round-trip, but instance-creation-from-event was
entirely unbuilt.

Two invariants blocked it and had to be lifted:

- `engine/step_triggers.go` — `handleStartInstance` errored unless a
  definition had exactly one start event, and hard-coded that single start.
- `definition/model/validate.go` — `ErrMultipleStartEvents` rejected more
  than one start event outright, and reachability validation only ever
  walked from that single start.

BPMN2 semantics for the three trigger kinds differ enough to need distinct
handling, not one generic "event start":

- **Signal start is a broadcast, 1:N fan-out.** A published signal starts
  one instance per registered definition with a matching signal-start. The
  signal name need not be unique across definitions, and no correlation is
  performed — it is a "signal flare."
- **Message start is addressed and correlation-controlled.** A message name
  is unique (one subscription per message-start). The correlation key
  controls instantiation: a running instance for the same key correlates
  the message to itself; otherwise a new instance is created
  (correlate-to-running-first, then create).
- **Timer start is scheduler-driven.** The start node's timer schedule
  fires autonomously; each fire creates one instance.

In all three cases the publisher never names the target definition — it
emits a name (plus a key, for messages) and a payload; the engine resolves
who correlates or starts.

## Decision

### Placement: internal to `runtime`, no new public methods

Event-start orchestration is an `internal`-to-the-`runtime`-package
collaborator composed into `ProcessDriver`, not a separate consumer-wired
component. It holds the correlate-then-create / fan-out / node-resolution
logic and calls existing driver primitives. There are **no new public
facade methods** — event-start rides the existing entry points, which
become **def-less publish** operations: the publisher never names a
definition.

- **`DeliverMessage(ctx, name, key, payload)` drops its `def` parameter.**
  It first tries to correlate to a running waiter by `(name, key)`
  (existing `msgWaiters` behaviour, resuming that instance); on a miss it
  looks for a unique message-start definition for `name` and creates a new
  instance seeded with `payload` as start vars. Two or more matching
  message-start definitions is `ErrAmbiguousMessageStart`. No waiter and no
  match is a no-op, preserving today's behaviour for genuinely-unmatched
  messages.
- **`BroadcastSignal(ctx, name, payload)`** keeps its signature (it was
  already def-less) but now, in addition to resuming parked waiters,
  creates one instance per registered definition with a matching
  signal-start (fan-out). The nil-`sigbus` guard is relaxed: with no bus
  but at least one signal-start match, creation still proceeds; it errors
  only when there is neither a bus nor any signal-start.

Dropping `def` from `DeliverMessage` is a clean break (the library is
unreleased): `service.DeliverMessageRequest` loses its `DefRef`, the four
transport layers (`transport/http/{httpcore,stdlib,gin,fiber}`) and
`service/service.go` simplify, and example call sites update.

### BPMN semantics implemented as designed

Signal-start fan-out, message-start correlate-then-create, and
scheduler-driven timer-start are implemented exactly as scoped in the
Context section above — see the design doc for the full mechanics.

### "Manual start" terminology

The trigger-less, caller-driven start (BPMN's "none start event") is named
a **manual start** throughout the API (`ErrMultipleManualStarts`,
`ErrNoManualStart`, "resolves the manual-start"). This reuses the term
introduced for user tasks in ADR-0118 (`WithManual`), for the same reason:
it names the human-facing behaviour ("someone/something triggers this
directly, with no declared event") rather than leaking the BPMN element
name ("none") into the day-to-day vocabulary.

### No durable subscription store — an opt-in enumeration capability

There is no durable `StartSubscriptionStore` and no persistent subscription
index. Signal- and message-start matches are resolved by a **stateless
scan** of registered definitions, exposed as an opt-in capability interface
(sibling in spirit to the store's `Notifier`/`Locker`, ADR-0081):

```go
// DefinitionLister is an optional capability a DefinitionRegistry may
// implement so the event-start subsystem can find definitions subscribing
// to a message/signal/timer start. Registries that do not implement it
// disable event-based *start* (correlate-to-running still works).
type DefinitionLister interface {
	ListDefinitions(ctx context.Context) []*model.ProcessDefinition
}
```

`MemDefinitionRegistry` and `MapDefinitionRegistry` implement it trivially
(both already hold every definition in a map); `CachingDefinitionRegistry`
passes through to its delegate. The definition is the source of truth:
nothing needs to be kept in sync, and dynamic registration "just works."
Message-start name uniqueness is enforced at `RegisterDefinition` (under a
registration mutex, TOCTOU-free) with `ErrDuplicateMessageStart`, backstopped
by a delivery-time `ErrAmbiguousMessageStart` check for registries that
bypass `RegisterDefinition`.

### Engine seam: `StartInstance.StartNodeID`

`engine.StartInstance` gains a `StartNodeID string` field. Empty resolves
the definition's manual start (`ErrNoManualStart` if there is none);
non-empty places the initial token on that node directly. The **driver**
decides which start node fired (by correlating the trigger to a start
node); the **engine** only places the token — the same "runtime decides,
engine executes" seam as ADR-0115, keeping the engine free of
message/signal/registry concerns. `handleStartInstance` no longer requires
exactly one start event; a definition may now declare multiple start
events, subject to the ≤ 1 manual-start invariant.

### Concurrency and safety

Each trigger type inherits or reuses machinery the repo already provides,
rather than adding new shared state:

- **Timer-start rides `scheduling.Elector`.** Timer-starts use the same
  scheduler as instance timers, so exactly one replica is leader and runs
  all fires — multi-replica safe for free, no duplicate instances across
  replicas.
- **Signal-start fan-out is safe by design.** No correlation is performed;
  each `BroadcastSignal` is one fan-out round of independent creates
  through the existing concurrency-safe create path. Concurrent identical
  broadcasts starting multiple instances is correct signal semantics, not
  a race.
- **Message-start dedup uses a deterministic instance id, not an advisory
  lock — the `runtime/chain.Chainer` pattern.** The message-start
  instance's id is a deterministic function of `(messageName,
  correlationKey)`, and `Store.Create` returning `kernel.ErrInstanceExists`
  is the authoritative dedup: two concurrent identical messages compute the
  same id, exactly one `Create` wins, the other is a clean no-op. This is
  fully multi-replica and restart safe, with no advisory lock, no
  in-process correlation map, and no new schema — the database row is the
  shared state. An earlier advisory-lock design was considered and
  dropped: without shared state to consult, a lock only blocks
  exactly-simultaneous creation (a replica acquiring the lock after
  another released it still cannot see the just-created instance, since a
  message-start instance is not itself a waiter for its own start
  message), so it was strictly weaker than the deterministic-id approach
  for no benefit.

### Deliberately dropped (YAGNI)

- No durable `StartSubscriptionStore` / persistent subscription index (the
  umbrella spec's original proposal).
- No new `StartByMessage` / `StartBySignal` public methods (the umbrella
  spec's original proposal) — event-start reuses `DeliverMessage` /
  `BroadcastSignal`.
- No cross-definition message *routing* beyond start (correlate-then-create
  only).

### Relationship to ADR-0045 (Chainer)

Event-based start overlaps conceptually with the existing
`runtime/chain.Chainer` (predecessor terminal outbox event → a
`SuccessorPolicy` Go callback → start a fresh root instance, with lineage
and exactly-once-durable idempotency, [ADR-0045](0045-process-instance-chaining.md)).
They are complementary, not redundant, on different axes:

- **Event-start** is triggered *externally* (message/signal/timer),
  *declarative* (the definition declares its own start event),
  publisher-decoupled, fans out, and carries **no lineage**.
- **Chainer** is triggered by a *predecessor's terminal outcome*,
  *imperative* (a Go `SuccessorPolicy` in consumer code), routes on
  terminal **outcome**, records **lineage** (`ChainLink`), and is
  **exactly-once over the durable outbox**.

We retain `Chainer` as-is and position event-start — specifically
signal-throw-at-an-end-event → signal-start-elsewhere — as the preferred,
BPMN-native path for process-to-process **choreography**. `Chainer` remains
the right tool for cases event-start does not cover: predecessor→successor
**lineage**, outcome-based routing without editing the predecessor
definition, and exactly-once **durable** chaining. There is no code reuse
between them beyond the shared "start a root instance" primitive (`Chainer`
uses `Drive`; event-start uses the node-targeted create path); event-start
does not reinvent `Chainer`. Chainer's deterministic-id idempotency is the
natural upgrade path if durable message-start dedup is ever needed beyond
the single-use-per-lifetime trade-off accepted below.

## Consequences

**Positive**

- BPMN2-faithful event starts (message/signal/timer) with the publisher
  decoupled from the target definition.
- Minimal new machinery: one opt-in registry capability, one trigger field,
  one rehydrate step (`RehydrateStartTimers`), and two relaxed
  validations — no new durable state; the definition is the source of
  truth.
- Transparent to consumers: register a definition with an event-start
  trigger and the existing delivery paths (`DeliverMessage`,
  `BroadcastSignal`, the scheduler) start instances without any new call.

**Breaking (pre-v0.1.0 — no stability promise)**

- **`DeliverMessage` loses its `def` parameter.** Every caller —
  `service.Engine.DeliverMessage`, the four transport layers, and example
  call sites — updates to the def-less signature.
- **`service.DeliverMessageRequest.DefRef` is removed.** `StartInstance`'s
  `DefRef` is unaffected; only the message-delivery request shrinks.
- **Contract change for consumers who correlate messages to a running
  instance:** the correlate path now resolves the parked instance's
  definition via the registry's `Lookup`, not from a caller-supplied `def`.
  **Receiver definitions must be registered** with the driver's definition
  registry, or correlation fails with `kernel.ErrDefinitionNotFound`. This
  was implicit before (the caller supplied the definition directly) and is
  now an explicit registration requirement — call this out to embedding
  consumers upgrading past this ADR.
- **`BroadcastSignal`'s and `DeliverMessage`'s miss-branch behaviour
  changes.** Previously a signal with no waiters, or a message with no
  waiter, was always a no-op. Now a miss additionally checks for a
  matching signal-/message-start and creates an instance when one exists.
  Definitions with no event-starts see no behaviour change.

**Trade-offs / risks**

- Message-start correlation key is **single-use per instance lifetime**:
  once an instance exists for a `(name, key)` — running or completed, its
  row still present — a later message with the same key correlates to it
  or no-ops rather than starting a fresh instance. This is correct for
  once-ever keys (e.g. an order id) but requires pruning the terminal
  instance before a key can be reused for a new start.
- The stateless enumeration scan is O(N definitions) per broadcast or
  correlation-miss; acceptable at target scale and revisitable behind the
  `DefinitionLister` capability without any public API change.
- Message-start name uniqueness enforced at `RegisterDefinition` is
  best-effort for consumers that register directly into a custom registry
  bypassing that entry point; the delivery-time `ErrAmbiguousMessageStart`
  guard is the authoritative backstop for that case.
- Unblocks ADR-0122 (folding `EventSubProcess` into `SubProcess` with an
  event-triggered inner start), which depends on this feature.

Example: `examples/scenarios/event_start` shows an order signal
(`order.completed`) fanning out to start **payment** and **shipment**
instances; shipment then parks on a `payment.completed` message that
correlates-then-completes it — illustrating both fan-out signal-start and
keyed message correlation in one scenario.

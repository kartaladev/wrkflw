# 12. watermill-backed Publisher behind the runtime.Publisher port

- Status: Accepted
- Date: 2026-06-21

## Context

The Persistence sub-project (ADR-0006/0007/0008) shipped the transactional
outbox: every terminal step writes domain events (`instance.completed`,
`instance.failed`) into `wrkflw_outbox` in the same transaction as the state
snapshot, and a broker-agnostic `Relay` drains the table (`FOR UPDATE SKIP
LOCKED`, at-least-once) by calling a `runtime.Publisher` once per claimed row.
That `Publisher` port is the eventing abstraction; no broker is wired yet.

The locked tech stack (CLAUDE.md) mandates
[`ThreeDotsLabs/watermill`](https://github.com/ThreeDotsLabs/watermill) with the
**outbox publishing pattern**, behind an in-repo abstraction so the broker stays
swappable — watermill must **never** be imported from engine/workflow code. Two
facts shape how it is introduced:

- **The outbox already exists.** Watermill ships its own outbox solution (the
  Forwarder over an intermediate SQL Pub/Sub). Adopting it would duplicate our
  `Relay` and add a second outbox table. The relay is the outbox; watermill is
  only needed as the broker-publishing transport behind `runtime.Publisher`.
- **The port is broker-shaped but identity-poor.** The `Publisher` godoc promises
  that "the outbox `dedup_key` supports deduplication," yet the `OutboxEvent`
  passed to `Publish` carries only `{Topic, Payload}` — neither the dedup key nor
  the instance id. A watermill adapter therefore cannot set a stable message UUID
  (at-least-once redeliveries look like distinct messages to a deduplicating
  consumer) nor a per-instance partition/ordering key.

ADR-0008 established the **façade-over-internal** layout (`persistence/` over
`internal/persistence/postgres/`) as the template for the watermill, gocron, and
casbin sub-projects (ADR-0009, ADR-0010 already reused it). Each adapter façade
may import the infrastructure vendor it adapts — `persistence.OpenPostgres` takes
a `*pgxpool.Pool`, `scheduling.NewScheduler` takes a `clockwork.Clock` — provided
the engine/runtime/model stay vendor-free.

## Decision

Implement a **watermill-backed `Publisher`** behind the existing
`runtime.Publisher` port, following the ADR-0008 façade/internal split, and make
one small extension to `runtime.OutboxEvent` so the adapter can publish
idempotent, partition-keyed messages.

- **`internal/eventing/watermill/`** holds the concrete `*Publisher` wrapping a
  watermill `message.Publisher`. It owns all watermill, OTel, and JSON-encoding
  imports. `Publish` maps one `OutboxEvent` → one `*message.Message`: watermill
  topic = `ev.Topic`; payload = `json.Marshal(ev.Payload)`; message UUID =
  `ev.DedupKey` (or a fresh `watermill.NewUUID()` when empty); metadata
  `instance_id`/`topic`; `msg.SetContext(ctx)` to propagate the trace. A
  `slogLogger` adapts `*slog.Logger` to `watermill.LoggerAdapter` so watermill's
  internal logs are unified with ours.
- **`eventing/`** (module root) is the consumer-facing façade:
  `NewPublisher(pub message.Publisher, ...Option) runtime.Publisher` wraps any
  watermill publisher (Kafka/NATS/SQL/…), and `NewGoChannelPublisher(...Option)`
  returns an in-process publisher + subscriber + `io.Closer` for
  examples/tests/in-process consumers (GoChannel is in watermill core, so no
  external broker dependency is added). The façade imports `watermill/message`
  as the consumer's bring-your-own broker handle — the same vendor-at-edge stance
  as `OpenPostgres(*pgxpool.Pool)` — and re-exports nothing internal-concrete; the
  `var _ runtime.Publisher = (*watermillpub.Publisher)(nil)` guard lives here.
- **`runtime.OutboxEvent` gains `DedupKey` and `InstanceID`** (additive,
  optional). They are populated only when an event is read back from the outbox
  row: the Postgres `Relay` adds `instance_id, dedup_key` to its `SELECT` and the
  row→event mapping. The write-time `runtime.outboxEventsFor` is untouched, and no
  `engine` symbol changes — `OutboxEvent` lives in `runtime`, outside
  `engine.InstanceState`, so `cloneState` and the deterministic-`Step` contract
  are unaffected.
- **Observability on the publish path:** structured `slog` (debug per publish,
  error on failure), one OTel span `eventing.publish` per call, and a
  `wrkflw_eventing_published_total{status}` counter. Defaults use `slog.Default()`
  and the OTel global providers; `WithLogger`/`WithTracerProvider`/
  `WithMeterProvider` override them. The library imports the OTel **API only** —
  the consumer wires the SDK.
- **Deliberately not built:** watermill's Forwarder/SQL Pub-Sub outbox (the relay
  already is the outbox), a subscriber-side framework (consumers subscribe in
  their own app), broker-specific constructors beyond GoChannel, and a
  richer event envelope / topic-mapping (YAGNI until a consumer needs it).

## Consequences

**Easier:** the broker is now reachable from the library with a single wrap —
`persistence.NewRelay(pool, eventing.NewPublisher(brokerPub))` — and watermill
stays swappable behind `runtime.Publisher`, honouring the vendor-isolation rule
and reusing the ADR-0008 template verbatim. Setting the message UUID to the
outbox `dedup_key` makes the at-least-once outbox safely idempotent for
deduplicating consumers, and the `instance_id` metadata gives partitioned brokers
a per-instance ordering key — the godoc promise is now real. GoChannel keeps the
adapter testable end-to-end in-process with no external broker and no extra
dependency. The publish path is observable (slog + trace + metric) by default.

**Harder / trade-offs:** `runtime.OutboxEvent` — already-merged, reviewed code —
gains two fields, so the Postgres relay's projection and any future `Publisher`
impl must be aware of them (mitigated: additive/optional, empty until populated,
in-memory path keeps compiling). The façade and adapter now depend on watermill
and the OTel API, enlarging `go.mod`; a grep guard in the verification gate keeps
those imports out of `engine`/`model`/`runtime`. Because we keep our own relay
rather than watermill's forwarder, we do not get watermill's forwarder features
for free (e.g. its envelope, its poison handling) — acceptable, since the relay
already owns batching, locking, and at-least-once, and retry/DLQ is the separate
Resilience sub-project. Finally, topic names and the JSON envelope are fixed in
v1; a consumer needing prefixing or a structured envelope must wrap their own
`message.Publisher` until a mapping knob is added.

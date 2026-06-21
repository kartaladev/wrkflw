# Eventing (watermill) — design spec

- Status: Accepted
- Date: 2026-06-21
- Related: ADR-0006 (snapshot+outbox storage), ADR-0008 (consumer façade over
  internal impl), ADR-0012 (watermill-backed `Publisher`)
- Plan: `docs/plans/2026-06-21-eventing-watermill.md`

## 1. Goal & scope

Ship a **production event publisher** for the wrkflw engine, backed by
[`ThreeDotsLabs/watermill`](https://github.com/ThreeDotsLabs/watermill) (the
locked eventing library in CLAUDE.md), using the **outbox publishing pattern**.
It implements the *existing* `runtime.Publisher` port that the Persistence
sub-project already defined and that the transactional outbox `Relay` already
drains. The engine core, the runtime, and the Postgres store are unchanged
**except** for one small, deliberate extension to `runtime.OutboxEvent` (§3) that
gives the publisher a stable idempotency key and a per-instance partition key.

The deliverable is **library-first**: the consumer brings their own watermill
`message.Publisher` (Kafka / NATS / SQL / …), wraps it with our adapter, and
hands the result to the relay they already build:

```go
pub   := eventing.NewPublisher(myKafkaPublisher)      // runtime.Publisher
relay := persistence.NewRelay(pool, pub)              // existing outbox relay
go relay.Run(ctx)                                     // drains outbox → watermill → broker
```

In scope (v1):

- A concrete `*Publisher` (watermill adapter) in `internal/eventing/watermill/`,
  the only place watermill is imported besides the façade.
- A consumer-facing root façade `eventing.NewPublisher(...)` returning
  `runtime.Publisher`, plus a `NewGoChannelPublisher(...)` convenience for
  in-process / testing / examples (GoChannel ships inside watermill core — no
  external broker dependency is added).
- The `runtime.OutboxEvent` extension (`DedupKey`, `InstanceID`) and the Postgres
  relay change that populates them from the outbox row.
- Observability on the publish path: structured `slog` logging (incl. bridging
  watermill's internal logs), one OpenTelemetry span per publish, and a
  published/failed counter metric.
- A capstone e2e (testable Example) proving an `OutboxEvent` published through the
  adapter is received by a watermill subscriber with the correct payload, UUID,
  and metadata.

Explicitly **out of scope** (see §9): a subscriber-side framework (consumers
subscribe in their own application), broker-specific constructors beyond
GoChannel, and watermill's own Forwarder / SQL Pub-Sub outbox — our Postgres
`Relay` already *is* the transactional outbox drain (ADR-0006), so layering
watermill's forwarder on top would duplicate it.

Non-goals carried over from the project contract:

- The engine core stays pure of the eventing vendor. **watermill is never
  imported from `engine`/`model`/`runtime`/workflow code** — only from
  `eventing/` and `internal/eventing/` (and `_test.go` files). This is the same
  vendor-isolation rule ADR-0003/0008/0009/0010 enforce for clockwork, pgx,
  gocron, and casbin. The `runtime.Publisher` port is the abstraction.

## 2. The port being implemented (extended in §3, otherwise unchanged)

`runtime/publisher.go` already defines:

```go
// Publisher relays one outbox event to the eventing backend. Implementations
// must be idempotent downstream (delivery is at-least-once; the outbox
// dedup_key supports deduplication). The persistence relay calls Publish for
// each claimed unpublished row.
type Publisher interface {
    Publish(ctx context.Context, ev OutboxEvent) error
}
```

re-exported from the persistence façade as `persistence.Publisher =
runtime.Publisher`. The relay calls `Publish` once per claimed unpublished row,
in row order, inside a `FOR UPDATE SKIP LOCKED` batch transaction; a `Publish`
error rolls the batch back, so the row stays unpublished and is retried on the
next poll (at-least-once). The watermill adapter is a drop-in for this port —
nothing in the relay's drive loop changes.

## 3. The one merged-code change: `OutboxEvent` idempotency/partition keys

The `Publisher` godoc promises *"the outbox dedup_key supports deduplication,"*
yet the event handed to `Publish` today carries neither the dedup key nor the
instance id:

```go
// runtime/ports.go — BEFORE
type OutboxEvent struct {
    Topic   string
    Payload map[string]any
}
```

Without those, a watermill adapter cannot set a stable message UUID (so
at-least-once redeliveries look like distinct messages to a deduplicating
consumer) nor a per-instance partition/ordering key (so Kafka-style brokers
cannot keep one instance's events in order). We close the gap:

```go
// runtime/ports.go — AFTER
type OutboxEvent struct {
    Topic      string
    Payload    map[string]any
    DedupKey   string // stable idempotency key (== outbox dedup_key); empty when not sourced from a persisted row
    InstanceID string // process-instance id; per-instance partition/ordering key
}
```

- **Where they are populated.** These fields are only meaningful when an event is
  *read back* from the outbox table. The Postgres `Relay`
  (`internal/persistence/postgres/relay.go`) already `SELECT`s each claimed row;
  its projection gains `instance_id, dedup_key`, and the row→`OutboxEvent`
  mapping sets the two new fields. The write-time helper
  `runtime.outboxEventsFor` is **untouched** (it derives `Topic`/`Payload` from
  terminal commands and has no row context).
- **Purity unaffected.** `OutboxEvent` lives in `runtime`, not in
  `engine.InstanceState`; `cloneState` and the deterministic-`Step` contract are
  not involved. No new `engine` symbols, no trigger/command changes.
- **Backward compatible.** Both fields are additive and optional; the in-memory
  `MemStore` path and any existing `Publisher` impl keep compiling (they simply
  see empty strings until populated). The adapter treats an empty `DedupKey` as
  "generate a fresh UUID."

## 4. Layout (ADR-0008 template)

```
internal/eventing/watermill/        # concrete adapter — owns ALL watermill imports
  publisher.go      Publisher{pub message.Publisher; logger *slog.Logger; tracer; counter}
                    NewPublisher(pub message.Publisher, opts ...Option) *Publisher
                    (*Publisher).Publish(ctx, runtime.OutboxEvent) error
  logger.go         slogLogger — a watermill.LoggerAdapter backed by *slog.Logger
  options.go        Option, withLogger/withTracerProvider/withMeterProvider
  *_test.go
eventing/                           # public façade — vendor-at-edge (imports watermill/message, like persistence imports pgx)
  eventing.go       NewPublisher(pub message.Publisher, opts ...Option) runtime.Publisher
                    NewGoChannelPublisher(opts ...Option) (runtime.Publisher, message.Subscriber, io.Closer)
                    Option + WithLogger / WithTracerProvider / WithMeterProvider
                    var _ runtime.Publisher = (*watermillpub.Publisher)(nil)
  eventing_example_test.go          # e2e GoChannel round-trip; doubles as godoc
```

`message.Publisher` (watermill's pub interface) is the consumer's
"bring-your-own broker handle", exactly analogous to `persistence.OpenPostgres`
taking a `*pgxpool.Pool`: the façade may import the infrastructure vendor it
adapts; the rule it must honour is that **engine/runtime/model never import
watermill**, which they do not. `GoChannel` lives in watermill core
(`pubsub/gochannel`), so the convenience constructor adds no broker dependency.

## 5. `Publish` design (the core behaviour)

```go
func (p *Publisher) Publish(ctx context.Context, ev runtime.OutboxEvent) error {
    ctx, span := p.tracer.Start(ctx, "eventing.publish",
        trace.WithAttributes(
            attribute.String("messaging.destination", ev.Topic),
            attribute.String("wrkflw.instance_id", ev.InstanceID),
        ))
    defer span.End()

    payload, err := json.Marshal(ev.Payload)
    if err != nil {
        // record error on span + counter, log, return wrapped
        return fmt.Errorf("eventing: marshal payload: %w", err)
    }

    uuid := ev.DedupKey
    if uuid == "" {
        uuid = watermill.NewUUID()
    }
    msg := message.NewMessage(uuid, payload)
    msg.Metadata.Set("topic", ev.Topic)
    msg.Metadata.Set("instance_id", ev.InstanceID)
    msg.SetContext(ctx) // carries OTel trace context to the broker layer

    if err := p.pub.Publish(ev.Topic, msg); err != nil {
        // span.RecordError + status=error counter + slog error
        return fmt.Errorf("eventing: publish topic=%q: %w", ev.Topic, err)
    }
    // status=ok counter + slog debug
    return nil
}
```

Design points:

- **Watermill topic == `ev.Topic`.** No topic rewriting in v1; the engine's topic
  names (`instance.completed`, `instance.failed`) flow straight through. A
  consumer who needs prefixing wraps their own `message.Publisher` — we do not
  add a topic-mapper knob until a real need exists (YAGNI).
- **Message UUID = `DedupKey`** when present → a redelivered outbox row produces a
  byte-identical UUID, so watermill dedup middleware / idempotent consumers
  collapse it. Empty `DedupKey` ⇒ a fresh `watermill.NewUUID()`.
- **`instance_id` metadata** is the partition/ordering key a broker adapter
  (e.g. Kafka via a `GeneratePartitionKey`) can use to keep one instance's events
  ordered. We set it as metadata rather than prescribing a broker — the consumer's
  watermill publisher decides how to use it.
- **One event per call.** The relay drives `Publish` per row; we do not buffer or
  batch across calls (the relay owns batching + the transactional commit).
- **Error → wrapped, non-nil.** Returning an error makes the relay roll the batch
  back, preserving at-least-once. We never swallow a publish failure.

## 6. Root façade (`eventing/`)

```go
// NewPublisher wraps any watermill message.Publisher as a runtime.Publisher.
func NewPublisher(pub message.Publisher, opts ...Option) runtime.Publisher

// NewGoChannelPublisher builds an in-process GoChannel pub/sub and returns a
// Publisher over it, the Subscriber side (for in-process consumers / tests), and
// an io.Closer to release it. No external broker required.
func NewGoChannelPublisher(opts ...Option) (runtime.Publisher, message.Subscriber, io.Closer)

type Option func(*config)
func WithLogger(l *slog.Logger) Option
func WithTracerProvider(tp trace.TracerProvider) Option
func WithMeterProvider(mp metric.MeterProvider) Option
```

The façade re-exports nothing internal-concrete; the compile-time guard
`var _ runtime.Publisher = (*watermillpub.Publisher)(nil)` lives here. Options
default to `slog.Default()` and the OTel global tracer/meter providers, so the
zero-config call `eventing.NewPublisher(pub)` works out of the box.

## 7. Observability

- **slog.** Inject `*slog.Logger` (default `slog.Default()`). One debug line per
  successful publish and one error line per failure, with attrs `topic`,
  `instance_id`, `dedup_key`. Watermill's own internal logs are bridged through
  the same `*slog.Logger` via the `slogLogger` `watermill.LoggerAdapter`
  (`logger.go`), so a consumer gets unified logs and the GoChannel constructor is
  not noisy on stderr.
- **OpenTelemetry tracing.** One span `eventing.publish` per call, attrs
  `messaging.destination`=topic and `wrkflw.instance_id`; `span.RecordError` +
  `codes.Error` on failure. Default tracer from the OTel global provider,
  overridable via `WithTracerProvider`. `msg.SetContext(ctx)` propagates the span
  context into watermill so broker middleware can continue the trace.
- **Metric.** A counter `wrkflw_eventing_published_total` with a `status` attr
  (`ok` | `error`), from the OTel global meter provider, overridable via
  `WithMeterProvider`.

OTel and slog are imported only in the adapter/façade (never engine/runtime),
consistent with the vendor-isolation rule.

## 8. Testing

- **Unit (mapping).** Black-box table tests (`package watermill_test`) with the
  project `assert`-closure form: a hand-written fake `message.Publisher` captures
  `(topic, []*message.Message)`; cases assert watermill topic = `ev.Topic`, JSON
  payload, `msg.UUID == ev.DedupKey` (and a non-empty generated UUID when
  `DedupKey` is empty), and `instance_id`/`topic` metadata. No mockgen — watermill
  `message.Publisher` is a two-method interface, trivially hand-faked.
- **Unit (failure).** A failing fake publisher ⇒ `Publish` returns a wrapped,
  non-nil error; assert the error wraps and the error-status path runs.
- **e2e (testable Example).** `eventing.NewGoChannelPublisher` → subscribe to the
  topic → `Publish` an `OutboxEvent` → assert the received `*message.Message` has
  the expected payload, UUID (= DedupKey), and metadata. Lives in
  `eventing_example_test.go`, doubles as godoc for library consumers.
- **Relay change.** Extend the existing Postgres testcontainers relay test
  (`internal/persistence/postgres/relay_test.go`): the `recordingPub` records full
  `OutboxEvent`s (not just topics) and asserts `DedupKey`/`InstanceID` are
  populated from the seeded row. Uses `database.RunTestDatabase` (testcontainers),
  never mocked.
- **Compile-time.** `var _ runtime.Publisher = (*watermillpub.Publisher)(nil)` in
  both the internal package and the façade.

TDD strict: every new symbol gets a failing test first with a visible RED before
the impl, per CLAUDE.md's TDD Operational Discipline.

## 9. Out of scope (deliberate, with rationale)

1. **Subscriber-side framework.** The engine only *publishes* (outbox → broker).
   Consumers subscribe inside their own application with their own watermill
   router/handlers. We ship only the Publisher side; the GoChannel `Subscriber`
   returned by `NewGoChannelPublisher` is a convenience for in-process consumers
   and tests, not a framework.
2. **Broker-specific constructors** (Kafka/NATS/SQL helpers). The consumer
   constructs their watermill publisher with their own config (brokers, TLS,
   partitioning) and passes it in. Re-wrapping every broker's constructor would
   pull broker SDKs into our `go.mod` for no library-ergonomics gain.
3. **Watermill Forwarder / SQL Pub-Sub outbox.** Our Postgres `Relay` already
   implements the transactional outbox drain (ADR-0006: `FOR UPDATE SKIP LOCKED`,
   at-least-once, dedup_key). Watermill's forwarder is an *alternative* outbox
   mechanism; using it would duplicate the relay and re-introduce a second outbox
   table. We keep our relay and use watermill purely as the broker-publishing
   transport.
4. **Topic mapping / event-envelope schema.** v1 passes `Topic` through verbatim
   and serializes `Payload` as JSON. A richer envelope (CloudEvents headers,
   schema registry) is a follow-up gated on a real consumer need.
5. **Retry / DLQ / poison isolation.** Inherited from the Persistence backlog: a
   persistent poison event head-of-line-blocks its batch (at-least-once intact).
   The retry-backoff/DLQ executor is the Resilience sub-project, not eventing.

## 10. Dependencies & version pins

- Add `github.com/ThreeDotsLabs/watermill` (core module; provides `message` and
  `pubsub/gochannel`). No broker-specific watermill module is added in v1.
- OpenTelemetry API (`go.opentelemetry.io/otel`, `.../trace`, `.../metric`) — API
  only, no SDK/exporter pulled into the library (the consumer wires the SDK).
- Imported **only** from `eventing/` and `internal/eventing/watermill/` (+
  `_test.go`). A grep guard in the verification gate asserts no watermill import
  leaks into `engine`/`model`/`runtime`.

## 11. Verification gate

- `go test -race ./...` green from the repo root (no regressions).
- ≥ 85% line coverage on touched packages (`internal/eventing/watermill`,
  `eventing`; the `eventing_example_test.go` + unit tests carry the adapter; the
  relay change is covered by the extended Postgres test).
- `golangci-lint run ./...` clean (v2 config).
- `grep -R "ThreeDotsLabs/watermill"` finds matches only under
  `internal/eventing/watermill/`, `eventing/`, and `go.mod`/`go.sum` — never in
  `engine`/`model`/`runtime` production code.

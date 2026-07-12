# Publishing wrkflw events to a message broker

wrkflw emits domain events through a **transactional outbox**: state changes and
their events commit in the same transaction, and a **relay** drains the outbox and
publishes each event. The publish port is `kernel.OutboxPublisher`; the eventing package
adapts **any** [watermill](https://github.com/ThreeDotsLabs/watermill) publisher to
it, so reaching Kafka, NATS, Redis Streams, or a SQL-backed queue is a **one-line
swap** — with watermill confined to the `eventing`/`internal` packages
(`engine`/`runtime` never import it).

The module ships **no broker dependency** and no per-broker constructors
(ADR-0093): you construct the watermill publisher for your broker (adding only that
dependency on your side) and hand it to `eventing.NewPublisher`.

A runnable, dependency-free reference lives in
[`examples/broker_wiring`](../examples/broker_wiring/main.go); the godoc
`ExampleNewPublisher` in the `eventing` package shows the mapping in isolation.

## The seam

```go
import (
    "github.com/kartaladev/wrkflw/eventing"
    "github.com/kartaladev/wrkflw/persistence"
)

// brokerPub is your broker's watermill message.Publisher (see below).
pub := eventing.NewPublisher(brokerPub)          // kernel.OutboxPublisher
relay, err := persistence.NewRelay(pool, pub)    // Postgres; or NewSQLiteRelay / NewMySQLRelay
// ...
go relay.Run(ctx)                                // or relay.DrainOnce(ctx) synchronously
```

`eventing.NewPublisher(pub message.Publisher, opts ...eventing.Option)` accepts
`WithLogger`, `WithTracerProvider`, and `WithMeterProvider`.

## What's on the wire

For each outbox row the relay drains, `NewPublisher` produces one watermill
`message.Message`:

| Message field | Value | Use |
|---|---|---|
| `msg.UUID` | the outbox **dedup key** — `"<instanceID>:<seq>:<eventIndex>"` | consumer-side **deduplication** (at-least-once, see below) |
| `msg.Payload` | JSON of the event payload | your event body |
| `msg.Metadata["topic"]` | the event topic (also the publish topic) | routing |
| `msg.Metadata["instance_id"]` | the process-instance id | **partition / ordering key** |
| `msg.Metadata["definition_ref"]` | `"<defID>:<version>"` | routing / filtering |

The publish topic is the event's topic (below). **The instance id is already on
every message** — no configuration needed to expose it.

## Topics

One topic per event kind:

- `instance.completed`, `instance.failed`, `instance.terminated` — status-accurate
  terminal events (ADR-0046). These drive process-instance chaining.
- `message.<Name>` — SendTask outbound messages (ADR-0067), consumed for
  SendTask → ReceiveTask correlation.

## At-least-once & deduplication

The relay is **at-least-once**: a crash between publish and marking the row
`published` re-publishes on the next drain. Every redelivery reuses the **same
`msg.UUID`** (the outbox dedup key), so **consumers must dedupe on `msg.UUID`**.
Most watermill router setups add a dedup/idempotency middleware keyed on the UUID.

## Ordering (read this before you rely on it)

The relay claims outbox rows `ORDER BY id` with `FOR UPDATE SKIP LOCKED`
(`internal/persistence/store/relay.go`). Therefore:

- **Single relay** → per-instance publish order is preserved: an instance's events
  are inserted in `(seq, eventIndex)` order and `id` is monotonic, so they are
  drained and published in that order.
- **Multiple relay replicas** → strict cross-row per-instance order is **not**
  guaranteed: two replicas may claim and publish two rows of the same instance
  concurrently (`SKIP LOCKED`).

Guidance: if you need strict per-instance ordering, **run a single relay**.
Partitioning the broker by `instance_id` (below) gives per-partition ordering only
as strong as the relay's publish order — perfect under a single relay, best-effort
under multiple.

## Broker wiring snippets

These are **illustrative** — add the adapter dependency on your side and check the
API against your watermill adapter version (the core targets watermill `v1.5.x`).
All four wrap the same way: build the broker publisher, then
`eventing.NewPublisher(brokerPub)`.

### Kafka — partition by instance_id (per-instance ordering)

The one broker where you configure partitioning explicitly. A partitioning
marshaler routes every event of an instance to one partition (→ per-partition
ordering):

```go
import (
    "github.com/ThreeDotsLabs/watermill"
    "github.com/ThreeDotsLabs/watermill-kafka/v3/pkg/kafka"
    "github.com/ThreeDotsLabs/watermill/message"
)

marshaler := kafka.NewWithPartitioningMarshaler(
    func(_ string, msg *message.Message) (string, error) {
        return msg.Metadata.Get("instance_id"), nil // partition key
    },
)

brokerPub, err := kafka.NewPublisher(kafka.PublisherConfig{
    Brokers:   []string{"localhost:9092"},
    Marshaler: marshaler,
}, watermill.NewSlogLogger(nil))
if err != nil { /* ... */ }

pub := eventing.NewPublisher(brokerPub)
```

### NATS JetStream

```go
import (
    "github.com/ThreeDotsLabs/watermill"
    "github.com/ThreeDotsLabs/watermill-nats/v2/pkg/nats"
)

brokerPub, err := nats.NewPublisher(nats.PublisherConfig{
    URL:       "nats://localhost:4222",
    Marshaler: &nats.NATSMarshaler{},
    JetStream: nats.JetStreamConfig{Disabled: false},
}, watermill.NewSlogLogger(nil))
if err != nil { /* ... */ }

pub := eventing.NewPublisher(brokerPub)
```

The topic becomes the JetStream subject. JetStream ordering is per-stream/subject;
for per-instance affinity, encode `instance_id` into the subject via a subject
calculator, or consume with a single ordered consumer.

### Redis Streams

```go
import (
    "github.com/ThreeDotsLabs/watermill"
    "github.com/ThreeDotsLabs/watermill-redisstream/pkg/redisstream"
    "github.com/redis/go-redis/v9"
)

rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
brokerPub, err := redisstream.NewPublisher(redisstream.PublisherConfig{
    Client:     rdb,
    Marshaller: redisstream.DefaultMarshallerUnmarshaller{},
}, watermill.NewSlogLogger(nil))
if err != nil { /* ... */ }

pub := eventing.NewPublisher(brokerPub)
```

One stream per topic. Redis Streams preserves order within a single stream;
`instance_id` is available in metadata for application-level routing/filtering.

### SQL (watermill-sql) — a durable, broker-free queue

`watermill-sql` is watermill's **own** SQL pub/sub — a separate messaging table in
a database you control. This is **not** wrkflw's internal persistence store; it is
an independent transport for consumers who want durable eventing without running a
broker.

```go
import (
    "github.com/ThreeDotsLabs/watermill"
    wmsql "github.com/ThreeDotsLabs/watermill-sql/v3/pkg/sql"
)

// yourDB is a *sql.DB you own for messaging (may be the same server, its own tables).
brokerPub, err := wmsql.NewPublisher(yourDB, wmsql.PublisherConfig{
    SchemaAdapter:        wmsql.DefaultPostgreSQLSchema{},
    AutoInitializeSchema: true,
}, watermill.NewSlogLogger(nil))
if err != nil { /* ... */ }

pub := eventing.NewPublisher(brokerPub)
```

## Consuming events

The subscriber side stays watermill-free in your workflow code via two adapters
you mount on your own watermill `message.Router`:

- `eventing.NewChainHandler(chainer)` / `eventing.NewChainerRunner(...)` — drives
  process-instance chaining by subscribing the three terminal topics
  (`instance.completed|failed|terminated`).
- `eventing.NewMessageHandler(deliver)` — routes a `message.<Name>` event back into
  a waiting ReceiveTask (SendTask → ReceiveTask correlation).

Wire your broker's watermill `message.Subscriber` to a `message.Router`, add a
dedup middleware keyed on `msg.UUID`, and register these handlers.

## See also

- Reference wiring: [`examples/broker_wiring`](../examples/broker_wiring/main.go)
- godoc: `eventing.NewPublisher`, `eventing.ExampleNewPublisher`
- ADR-0093 (this decision), ADR-0012 (watermill publisher), ADR-0046 (terminal
  events), ADR-0067 (SendTask outbox).

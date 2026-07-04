# Eventing broker integration (P0-3) — design

- **Status:** Approved (brainstorming, 2026-07-04)
- **Slug:** `eventing-broker-integration`
- **ADR:** 0093 (records the consumer-wired, broker-dependency-free stance)

## Problem

The 2026-06-30 production-readiness audit flagged P0-3: "no concrete broker
adapters — eventing is in-process only," treating cross-process eventing as
unusable out of the box and a release-blocker.

Investigation shows the capability already exists but is **undiscoverable**:

- `eventing.NewPublisher(pub message.Publisher, opts...) kernel.Publisher`
  (`eventing/eventing.go:52`) already wraps **any** watermill publisher, with
  watermill confined to `eventing/` and `internal/eventing/watermill/`. A consumer
  can reach Kafka/NATS/Redis/SQL today by passing the corresponding watermill
  publisher.
- Every published message **already carries the process-instance id**:
  `internal/eventing/watermill/publisher.go:101-103` sets
  `msg.Metadata["topic"|"instance_id"|"definition_ref"]`, sourced end-to-end from
  the `wrkflw_outbox.instance_id` column. The message UUID **is** the outbox
  `dedup_key` (`<instanceID>:<seq>:<eventIndex>`).

What is missing is **documentation, a worked reference example, and honest
ordering/dedup guidance** — not code. No `go.mod` broker dependency exists, and
all four `examples/*_wiring` use `NewGoChannelPublisher`.

## Goal

Close P0-3 by making the existing seam usable and discoverable, **without adding a
broker dependency to the module** and **without changing engine/eventing code**.

## Non-goals

- **No named broker constructors** (`NewKafkaPublisher`, …). They would pull
  broker packages into the core module's dependency graph and re-introduce the
  vendor coupling the abstraction exists to avoid (recorded in ADR-0093).
- No change to `engine`, `definition/model`, `runtime`, `eventing`, or
  `internal/eventing` behaviour. Zero-diff there.
- No new `go.mod` dependency.

## Deliverables

1. A broker-integration **docs guide** — `docs/eventing-brokers.md`.
2. A **runnable, dependency-free reference example** — `examples/broker_wiring/main.go`.
3. A testable **`ExampleNewPublisher`** godoc example in the `eventing` package.
4. **ADR-0093** recording the stance.

## 1. Reference example (`examples/broker_wiring/main.go`)

Wires the real publish path end-to-end with zero new dependencies, so the reader
sees the exact message a broker would receive:

- A small in-repo `demoPublisher` implementing watermill's `message.Publisher`
  (`Publish(topic string, messages ...*message.Message) error`, `Close() error`).
  For each message it prints: **topic**, **UUID (= dedup_key)**, the
  **`instance_id` / `definition_ref` metadata**, and the JSON payload.
- Wiring, mirroring `examples/sqlite_wiring` (SQLite → no container): open a
  SQLite store, build a `runtime.ProcessDriver`, run one instance to a terminal
  status (emitting `instance.completed`), then
  `persistence.NewRelay(eventing.NewPublisher(&demoPublisher{}), store, …)` and
  `DrainOnce(ctx)`.
- Heavily commented: the `demoPublisher` line is annotated "in production replace
  with `kafka.NewPublisher(...)` / `nats.NewPublisher(...)` / … — see
  `docs/eventing-brokers.md`," and the metadata print calls out that
  `instance_id` is the Kafka partition key.

The example is compiled by `go build ./...`. It uses only `watermill` core (for
the `message.Publisher` interface and `message.Message`) plus the existing SQLite
store — no broker dependency.

## 2. Docs guide (`docs/eventing-brokers.md`)

Sections:

- **The seam** — `eventing.NewPublisher(anyWatermillPublisher)` →
  `persistence.NewRelay`; watermill stays confined to `eventing`/`internal`;
  engine/runtime never import it.
- **What's on the wire** — message UUID = `dedup_key`
  (`<instanceID>:<seq>:<eventIndex>`); payload = JSON of the event payload;
  metadata keys `topic`, `instance_id`, `definition_ref`.
- **Topic taxonomy** — `instance.completed`, `instance.failed`,
  `instance.terminated` (status-accurate terminal events, ADR-0046);
  `message.<Name>` (SendTask outbound messages, ADR-0067).
- **Full wiring snippets** (copy-paste; NOT compiled — the consumer adds the dep):
  - **Kafka** — `kafka.NewPublisher` with
    `kafka.NewWithPartitioningMarshaler(func(_ string, m *message.Message) (string, error) { return m.Metadata.Get("instance_id"), nil })`, so events for one instance land on one partition → per-partition ordering.
  - **NATS JetStream** — `nats.NewPublisher` with subject = topic; note that
    JetStream ordering is per-subject/stream and how `instance_id` can drive a
    subject token or consumer filter if per-instance affinity is needed.
  - **Redis Streams** — `redisstream.NewPublisher`; one stream per topic; note
    Redis Streams' single-stream ordering and consumer-group semantics, and using
    `instance_id` for application-level routing.
  - **SQL (watermill-sql)** — `sql.NewPublisher` over the consumer's own DB
    (explicitly *watermill's* SQL pub/sub, **not** wrkflw's internal persistence
    store); a durable, broker-free option; note the offset/ordering model.
- **At-least-once & dedup** — the outbox relay is at-least-once; a redelivery
  reuses the same message UUID (= `dedup_key`), so consumers **must dedupe on the
  message UUID**.
- **Ordering guidance (honest caveat)** — the relay claims outbox rows
  `ORDER BY id` with `FOR UPDATE SKIP LOCKED`
  (`internal/persistence/store/relay.go`). A **single relay** preserves
  per-instance publish order (an instance's events are inserted in
  `(seq, eventIndex)` order and `id` is monotonic). A **multi-replica relay** does
  **not** guarantee strict cross-row per-instance order — two replicas may claim
  and publish two rows of the same instance concurrently. For strict per-instance
  ordering, run a single relay; otherwise partition-by-`instance_id` gives
  per-partition ordering only as strong as the relay's publish order.
- **Subscriber side** — consuming events with `eventing.NewChainHandler` (process
  chaining) and `eventing.NewMessageHandler` (SendTask → ReceiveTask correlation);
  mount on the consumer's own `message.Router`.
- **Pointer** to `examples/broker_wiring`.

## 3. Godoc `ExampleNewPublisher` (`eventing`)

A testable example in `eventing` showing `NewPublisher` wrapping a trivial
in-example `message.Publisher` stub and publishing one `kernel.OutboxEvent`, with
an `// Output:` block asserting the topic + `instance_id` metadata the stub
observed. Runs under `go test ./eventing/...` with no broker dependency and
documents the seam right next to the API.

## 4. ADR-0093

Records: broker integration is **consumer-wired** through
`eventing.NewPublisher(message.Publisher)`; the module ships **no named broker
constructors** and **no broker dependency**; the published message already carries
`instance_id` metadata for partitioning and its UUID (= `dedup_key`) for dedup.
Consequence: consumers add exactly the broker dep they use; the core stays
dependency-light and vendor-neutral. Supersedes the P0-3 "no broker adapters"
finding by clarifying it as a docs gap, now closed.

## Testing / verification

- `go build ./...` compiles `examples/broker_wiring`.
- `go test ./eventing/...` passes, including the new `ExampleNewPublisher`
  (`// Output:` matches).
- `go test ./...` — no regressions.
- `golangci-lint run ./...` clean.
- **Zero-diff** on `engine`, `definition/model`, `runtime`, `eventing/*.go`
  (non-test), and `internal/eventing` — the only `eventing` change is the new
  `example_*_test.go`.
- `go.mod` / `go.sum` unchanged (no new dependency) — verified with `git diff`.

## Risks / notes

- The real-broker snippets are not compiled, so they can drift from watermill API
  changes. Mitigation: pin the watermill adapter versions referenced in the docs
  and note the watermill version they target.
- Multi-replica strict ordering is a genuine limitation, documented rather than
  engineered here; a future ADR could add per-instance-partitioned outbox draining
  if strict multi-replica ordering becomes a requirement.

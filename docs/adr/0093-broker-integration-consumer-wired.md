# 0093 — Broker integration is consumer-wired; the module ships no broker dependency

- **Status:** Accepted
- **Date:** 2026-07-04

## Context

`wrkflw` publishes domain events (status-accurate terminal events and SendTask
outbound messages) through a transactional outbox. The outbox relay drains rows
and calls a `kernel.Publisher`; `eventing.NewPublisher(pub message.Publisher)`
adapts **any** watermill `message.Publisher` to that port, with watermill confined
to `eventing/` and `internal/eventing/watermill/` (ADR-0012). Only
`eventing.NewGoChannelPublisher` (in-process, watermill core) ships.

The 2026-06-30 production-readiness audit filed this as P0-3 — "no concrete broker
adapters, eventing is in-process only" — implying cross-process eventing is
unusable out of the box. Investigation showed that is not accurate:

- `eventing.NewPublisher(anyWatermillPublisher)` already exists and works for
  Kafka/NATS/Redis/SQL today.
- Every published message already carries the process-instance id:
  `internal/eventing/watermill/publisher.go` sets
  `msg.Metadata["topic"|"instance_id"|"definition_ref"]` from the
  `wrkflw_outbox.instance_id` column, and the message UUID is the outbox
  `dedup_key` (`<instanceID>:<seq>:<eventIndex>`).

So the gap is **discoverability and guidance**, not missing code. The open design
question was whether to additionally ship named convenience constructors
(`NewKafkaPublisher`, `NewNATSPublisher`, `NewSNSPublisher`, …).

## Decision

Broker integration is **consumer-wired** through the existing generic seam
`eventing.NewPublisher(message.Publisher)`. The module ships **no named broker
constructors and no broker dependency** in `go.mod`. Consumers construct the
watermill publisher for their broker (adding only that dependency on their side)
and hand it to `NewPublisher`.

To close the discoverability gap we ship documentation and reference wiring only:

- `docs/eventing-brokers.md` — the seam, the on-the-wire message shape
  (UUID = `dedup_key`, `topic`/`instance_id`/`definition_ref` metadata), topic
  taxonomy, full copy-paste wiring snippets for Kafka (with a partitioning
  marshaler keyed on `instance_id`), NATS JetStream, Redis Streams, and
  watermill-SQL, at-least-once/dedup semantics, and the single-vs-multi-replica
  ordering caveat.
- `examples/broker_wiring/main.go` — a runnable, dependency-free reference that
  prints the exact message a broker would receive.
- `eventing.ExampleNewPublisher` — a testable godoc example of the seam.

No change to `engine`, `definition/model`, `runtime`, `eventing` (non-test), or
`internal/eventing`.

## Consequences

- The core module stays dependency-light and vendor-neutral: a consumer pulls in
  exactly the broker package they use, and nothing else. This preserves the
  "no watermill lock-in" goal — swapping brokers touches only the consumer's
  `NewPublisher` argument.
- Named-constructor convenience is traded away. A consumer must write a few lines
  of watermill wiring, now fully documented with per-broker snippets.
- Partition-by-`instance_id` is available immediately (the metadata is already on
  the message); for Kafka it requires configuring a partitioning marshaler that
  reads that key — documented, consumer-side.
- Strict per-instance ordering under a multi-replica relay is **not** guaranteed
  (the relay claims `ORDER BY id` with `FOR UPDATE SKIP LOCKED`). This is
  documented as a limitation; a single relay preserves per-instance publish order.
  A future ADR may add per-instance-partitioned outbox draining if strict
  multi-replica ordering becomes a requirement.
- Supersedes the P0-3 audit finding by reclassifying it from "missing adapters"
  to "documentation gap," now closed.

## References

- Spec: `docs/specs/2026-07-04-eventing-broker-integration.md`
- Builds on ADR-0012 (watermill publisher), ADR-0046 (status-accurate terminal
  events), ADR-0067 (transactional SendTask outbox).
- Backlog: `docs/plans/2026-06-30-production-readiness-backlog.md` (P0-3).

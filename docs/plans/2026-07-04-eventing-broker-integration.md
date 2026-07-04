# Eventing broker integration (P0-3) — implementation plan

Spec: `docs/specs/2026-07-04-eventing-broker-integration.md` · ADR: `docs/adr/0093-...`
Branch: `docs/eventing-broker-integration`

Docs + reference wiring only — no new production symbols, no new `go.mod`
dependency. `engine`/`model`/`runtime`/`eventing` (non-test)/`internal` stay
zero-diff.

## Facts (verified)

- `eventing.NewPublisher(pub message.Publisher, opts...) kernel.Publisher` wraps any
  watermill publisher (`eventing/eventing.go:52`).
- `kernel.Publisher.Publish(ctx, ev OutboxEvent)` — `OutboxEvent{Topic, Payload,
  DedupKey, InstanceID, DefinitionRef}` (`runtime/kernel/{publisher,ports}.go`).
- Internal mapping sets `msg.Metadata["topic"|"instance_id"|"definition_ref"]`,
  UUID = `DedupKey` (`internal/eventing/watermill/publisher.go:96-104`).
- `persistence.NewSQLiteRelay(db, pub, opts...) (Relay, error)`; `Relay.DrainOnce(ctx)
  (int, error)`; `persistence.{OpenSQLite,MigrateSQLite}`. watermill core only in go.mod.

## Tasks

1. **ADR-0093** — `docs/adr/0093-broker-integration-consumer-wired.md`. Stance:
   broker integration is consumer-wired via `NewPublisher(message.Publisher)`; no
   named broker constructors; no broker dep in the module; `instance_id` metadata +
   `dedup_key` UUID already on the wire. Supersedes P0-3 "no adapters" as a docs gap.

2. **Godoc `ExampleNewPublisher`** — `eventing/example_newpublisher_test.go`
   (`package eventing_test`). Wrap a trivial in-example `message.Publisher` stub,
   `NewPublisher(stub)`, publish one `kernel.OutboxEvent`, print topic +
   `instance_id` metadata the stub observed; `// Output:` asserts them. No broker dep.
   Verify: `go test ./eventing/ -run ExampleNewPublisher`.

3. **Reference example** — `examples/broker_wiring/main.go`. In-repo `demoPublisher`
   (implements `message.Publisher`) printing topic + UUID + metadata + payload.
   Wire SQLite (`MigrateSQLite`→`OpenSQLite`) + `runtime.ProcessDriver`, run one
   instance to `instance.completed`, `persistence.NewSQLiteRelay(db,
   eventing.NewPublisher(&demoPublisher{}))`, `DrainOnce`. Heavily commented
   swap-in pointers to the docs. Verify: `go build ./...` + `go run` hermetic.

4. **Docs guide** — `docs/eventing-brokers.md`: seam, on-the-wire message shape,
   topic taxonomy, full copy-paste snippets (Kafka w/ partitioning marshaler on
   `instance_id`, NATS JetStream, Redis Streams, watermill-SQL), at-least-once +
   dedup-on-UUID, single-vs-multi-replica ordering caveat, subscriber side
   (`NewChainHandler`/`NewMessageHandler`), pointer to the example.

5. **Cross-links** — add a short "Brokers" pointer to the eventing package doc
   comment and/or `action`/top-level README if one references eventing; update the
   backlog memory. (Keep eventing non-test zero-diff: doc-comment-only edits are on
   `eventing/eventing.go`'s package comment — acceptable, or skip to preserve strict
   zero-diff. Decide during impl; prefer a docs/ pointer over touching eventing.go.)

## Verification checklist

- [ ] `go build ./...` compiles `examples/broker_wiring`.
- [ ] `go test ./eventing/...` green incl. `ExampleNewPublisher` (`// Output:` matches).
- [ ] `go test ./...` — no regressions.
- [ ] `golangci-lint run ./...` clean.
- [ ] `git diff go.mod go.sum` empty (no new dependency).
- [ ] Zero-diff on `engine`, `definition/model`, `runtime`, `internal/eventing`,
      and `eventing/*.go` non-test (only `eventing/example_newpublisher_test.go` added).
- [ ] `go run ./examples/broker_wiring` prints the demo messages with `instance_id`.

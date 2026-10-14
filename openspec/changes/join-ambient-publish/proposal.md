# Proposal

## Why

A consumer cannot publish a definition Version and write its own rows atomically: `PublishDefinition` always runs on the connection pool and ignores any transaction its context carries (`internal/persistence/store/definitions.go:207`, measured on `eebe55ba`). kartaladev/worktrack#27 asks for that capability. #112 built it once and split it out, because joining changes what a definition *read* means: a read no longer implies a committed row, and `CachingDefinitionRegistry` would serve a rolled-back publish process-wide for its full TTL. That is fail-open, so this change fixes it rather than documenting it. The seam it joins is `add-lent-transaction-seam`'s.

## What Changes

Absorbs kartaladev/wrkflw#151 (folded in whole: the join, the cache invariant, the concurrency contract, and its review findings F0b, S2, G5/S3 and G6). kartaladev/wrkflw#112 (closed) is where item 5 was split out; kartaladev/wrkflw#22 (decided) fixes the lent-transaction shape this change joins; and kartaladev/wrkflw#109 is owned by `add-lent-transaction-seam` and blocks this change, per the #20 register.

- **BREAKING (behavioral).** `PublishDefinition` joins the transaction its context carries: a unit opened by an instance store's `RunInTx`, or a consumer transaction lent through `persistence.WithTx`. Its insert and its read-back run on that one handle, so the F0b split cannot come back. The publish commits or rolls back with the unit. Today it commits on its own, so a definition published inside a unit that later rolls back survives. With no transaction in the context, behavior is unchanged.
- **Definition reads join too.** `GetDefinition` and `Lookup` (pinned and Latest), called with a transaction-carrying context, read through that transaction. They see the unit's own uncommitted publish, and nothing outside the unit sees it before commit. So a consumer can publish a Version and start an Instance of it in one lent transaction. Without a transaction they read the pool as today.
- **The caching registry never serves an uncommitted definition** (G5/S3). A lookup through `CachingDefinitionRegistry` with a transaction-carrying context does not populate the cache. It shares no in-flight result with a lookup outside that transaction, in either direction. After a rollback, a lookup returns `kernel.ErrDefinitionNotFound`. The pinned-key safety argument in its godoc ("a read implies a committed row") is rewritten. Whether such a lookup may still *serve* a committed cached entry is design Open Question 1.
- **A refusal does not poison the unit.** A publish refused with `ErrDefinitionExists` or `ErrInvalidDefinition` writes nothing and leaves the unit committable. The documented remedy for `ErrDefinitionExists` is to publish a new version, and that remedy must work inside the same unit. A failed statement undoes only the publish's own write: under a lent transaction through `add-lent-transaction-seam`'s savepoint, and under an engine-owned unit the way every other joined writer does.
- **SQLite no longer deadlocks.** On a single-connection SQLite pool, a publish or lookup inside an open unit waits on the pool for the only connection, which the unit holds. #151 records a hang killed at 9m54s, and 0.00s once joined. Re-measured mechanism on `eebe55ba`: `SetMaxOpenConns(1)` at `internal/dbtest/sqlite.go:33` and in three examples, with the pool querier at `definitions.go:115-119`. The hang itself is a hypothesis until task 1.1's red shows it.
- Godoc: the "does NOT join … issue #151" paragraphs on `PublishDefinition` (`definitions.go:181-184`, `persistence/persistence.go:122-125`) and the `querier` note (`definitions.go:101-114`) are replaced by the joined contract. They cite the `definition-store` spec, not an issue.

Stale or unproven premises in kartaladev/wrkflw#151:
- The body's citation `definitions.go:97-100` is stale. The pool-querier justification is at `definitions.go:101-114` on `eebe55ba`, as the thread's own correction says.
- "None of #151's defects reproduce today": partly stale. G5/S3, S2 and F0b need the join. The SQLite pool deadlock does not need the join and should reproduce today with a context deadline (task 1.1). So it gets a real red, not only the before/after timing the thread asked for.
- G6 (a REPEATABLE READ read-view problem) was never reproduced and stays a hypothesis. Task 1.3 measures it.

Impact flags. Persisted formats: none (no column, migration or encoding change). Default security posture: unchanged. It closes a fail-open path that only the join would open, and nothing is loosened. Consumer `go.mod`: unchanged.

Out of scope:
- The lent-transaction seam itself (`persistence.WithTx`, `Tx`, `Continuation`, savepoints, `Store.Load` joining): `add-lent-transaction-seam` (kartaladev/wrkflw#109).
- MySQL `def_id` collation, body storable domain (#154), the pgx `ErrNoRows` contract under `ErrConcurrentPublish` (#157), fixture coverage: `harden-definition-store-parity` (kartaladev/wrkflw#152, #154, #155, #157).
- Identical in-memory re-registration and the shared content normalisation: `make-definition-registration-idempotent`.
- `MemDefinitionRegistry` and `MapDefinitionRegistry`, which have no transactions.
- A transaction lent from a different database than the definition store's: it cannot be detected, so it is documented, not refused (same stance as `add-lent-transaction-seam`).
- Recovering an unresumed `Continuation`: `persist-pending-commands`.

Decisions this change cannot be fully specified without (design, Open Questions). Specs cover only what does not depend on them:
1. What a lookup through `CachingDefinitionRegistry` with a transaction-carrying context may serve. Recommended: bypass the cache entirely.
2. The concurrency contract on a joined definition read (S2). #151's thread requires a `-race` red, so "document only" is excluded. Refuse with a sentinel, or serialize? Recommended: refuse.
3. What a joined publish returns when its snapshot cannot see a conflicting committed row (G6). This waits on the task 1.3 measurement.

**Coordination**
- Size: L
- Depends on: add-lent-transaction-seam
- Blocks: nothing
- Readiness: human

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `definition-store`: new requirements. Publish and reads join a transaction-carrying context. A refused publish leaves the unit committable. The caching registry never serves or shares an uncommitted definition. Existing requirements are unchanged.

## Impact

- `internal/persistence/store/definitions.go`: `PublishDefinition`, `readPublished`, `GetDefinition` and `Lookup` obtain their querier from the context's transaction when one is present. Godoc is rewritten.
- `runtime/kernel/caching_definition_registry.go`: the transaction-aware lookup path and the rewritten safety argument. It needs the kernel-visible "context carries a transaction" marker that `add-lent-transaction-seam` adds for lent transactions, extended to engine-owned `RunInTx` units.
- `internal/persistence/store/txrunner.go`: `RunInTx` sets that marker.
- `persistence/persistence.go`: `DefinitionStore` interface godoc and the `NewCachingDefinitionRegistry` example.
- File overlap, not dependency: `definitions.go` is also edited by `harden-definition-store-parity` (godoc, `ErrConcurrentPublish` note) and `make-definition-registration-idempotent` (normalisation move). The same `PublishDefinition` interface comment block in `persistence/persistence.go` is rewritten by `harden-definition-store-parity`. Whichever lands second rebases.

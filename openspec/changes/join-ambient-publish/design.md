# Design

## Context

See proposal.md, Why. Measured on main (`eebe55ba`) before relying on any premise from kartaladev/wrkflw#151 or its thread:

- `PublishDefinition` (`internal/persistence/store/definitions.go:195`) obtains one querier at `:207`, and `readPublished` (`:251`) takes that same querier. **Holds**, so F0b is fixed today and must stay fixed when both move to the joined path.
- `DefinitionStore.querier` (`:115-119`) discards `ctx` and returns a pool querier. `GetDefinition` (`:381`) and `Lookup` (`:414`) use it. **Holds.**
- The "issue #151" deferral comments are at `definitions.go:114`, `:184` and `persistence/persistence.go:125`. The body's `:97-100` citation is **stale**.
- `CachingDefinitionRegistry` argues pinned keys are safe because a published Version is immutable (`runtime/kernel/caching_definition_registry.go:42-43`). It says TTL is the only eviction (`:51`) and populates from any successful backing lookup inside a `singleflight` flight keyed on the qualifier alone (`:115-138`). **Holds.** The argument assumes a read implies a committed row, which a joined read breaks.
- The ambient handle lives under one context key in `internal/database/transaction` (`transaction.go:21-32`). It is not keyed by connection, so a `DefinitionStore` joins whatever unit any store opened. `JoinOrBegin` joins it (`begin.go:44-50`). Every joined writer defers `q.Rollback` on any error return, which marks the unit rollback-only (`begin.go:64-67`; `store_core.go:75-80`). **Holds.**
- SQLite pools are built with `SetMaxOpenConns(1)` (`internal/dbtest/sqlite.go:33`, `examples/sqlite_wiring/main.go:111`, `examples/chaining_wiring/main.go:120`, `examples/scenarios/timer_durability/main.go:74`). A pool read or write issued while a unit holds that connection waits for it. That is a mechanism read off the code, not evidence. Task 1.1 measures it.
- `service.StartInstance` resolves the definition with the caller's context (`service/service.go:340`). Under a lent SQLite transaction with a durable definition store, that lookup is a pool read. This change makes it join. **Hypothesis for `add-lent-transaction-seam`:** until this change lands, its SQLite rows wired to a durable definition store would wait on the pool. Its specs do not say which registry they use. Recorded as coupling below.
- `DurableProvider` wires a `DefinitionStore` with no caching registry (`persistence/durableprovider.go:45`, `:87`). The caching registry is opt-in, and the `NewCachingDefinitionRegistry` godoc example at `persistence/persistence.go:318` shows the opt-in. **Holds.**

## Goals / Non-Goals

**Goals:** every scenario in `specs/definition-store/spec.md` holds on Postgres, MySQL and SQLite. The joined path, the pool path and the caching registry share one rule for what "a read implies" (Decision 3).

**Non-Goals:** making the in-memory registries transactional. Detecting a transaction lent from a different database. Any change to the content comparison (owned by `make-definition-registration-idempotent`).

## Decisions

1. **Join through the same path as every other writer** (kartaladev/wrkflw#151 body: "use the same `JoinOrBegin` path as the other writers"). The publish's insert and its read-back use one joined querier when the context carries a unit, and the pool when it does not. A lent transaction is joined through the external handle kind `add-lent-transaction-seam` adds, so it gets that change's savepoint (its Decision 2). Rejected: `JoinOrBegin` with no ambient unit, which would begin a transaction for a single statement plus one read. The statement's conflict clause is already atomic, and the immutability argument at `definitions.go:101-114` still holds on the pool path, so the pool stays the no-transaction path.

2. **Reads join as well** (kartaladev/wrkflw#151, F0b, and the worktrack#27 capability). Publish-then-start in one lent transaction needs `Lookup` to see the unit's own row. On a single-connection SQLite pool, a pool read inside a unit cannot complete at all. Rejected: joining the publish only. That reintroduces F0b's shape one level up (publish, then a read in the same unit that hangs on SQLite and misses on Postgres and MySQL), which #112 already paid for.

3. **A refusal is not a failed write** (derived from the published contract, not a new policy). `ErrDefinitionExists`'s documented remedy is "publish a new version instead". `ErrInvalidDefinition` is raised before any I/O. Neither writes anything, so neither marks the unit for rollback. Only a failed statement does: rollback-only under an engine-owned unit, and a savepoint rollback under a lent transaction. Rejected: the joined writers' `defer q.Rollback` pattern taken as is. It would mark the unit on every error return, so a caller who handles `ErrDefinitionExists` by publishing v+1 would then fail at commit, and the documented remedy would not work inside a unit.

4. **The cache's safety argument is restated as "a cached entry was read outside any transaction"** (G5/S3; fix, not document, per the #151 thread: a limit that fails open must be fixed). That holds under every option of Open Question 1. A lookup carrying a transaction never writes an entry, and it never joins or leads a `singleflight` flight shared with lookups outside that transaction. Rejected options:
   - An invalidation hook on commit or rollback. The cache lives in `runtime/kernel` and cannot observe a lent transaction's outcome, because the consumer commits it.
   - Documenting the window as a limit. It serves a definition that does not exist, which is fail-open.
   - Keying the flight on the transaction identity. That still lets an uncommitted read populate the shared map.

5. **A kernel-visible "context carries a transaction" marker** is needed for Decision 4, because `runtime/kernel` cannot import `persistence`. `add-lent-transaction-seam` (its Decision 4) adds a lent marker in `runtime/kernel`. This change extends it so `Store.RunInTx` also sets a marker on the context it hands to the callback. One query answers both cases. Rejected: the kernel importing `internal/database/transaction` to read the ambient handle, which couples the port package to one store implementation.

## Cross-change coupling

| Change | Coupling | Owner |
|---|---|---|
| `add-lent-transaction-seam` | Provides `WithTx`, the external handle kind with savepoints, and the kernel lent marker. This change extends the marker to engine-owned units. Landing order: that change first (Depends on). Its SQLite engine scenarios wired to a durable definition store depend on Decision 2 (hypothesis above). Either its tests use an in-memory registry, or it notes the dependency. | `add-lent-transaction-seam` |
| `harden-definition-store-parity` | Same file (`definitions.go`) and the same `PublishDefinition` interface comment in `persistence/persistence.go`. If its Q2 is "translate `ErrNoRows` at the adapter", the translation must cover the pgx transaction querier this change starts using for reads. Its design notes the shared `pgxRow`. No order required; the second to land rebases. | each for its own edits |
| `make-definition-registration-idempotent` | Moves `sameDefinitionContent` to a shared package. This change does not touch the comparison. File overlap only. | `make-definition-registration-idempotent` |
| `persist-pending-commands` | None directly. A definition published in a lent transaction commits with the consumer, independent of `Resume`. | n/a |

## Risks / Trade-offs

- [Lookups inside a unit bypass or partly bypass the cache, so every engine call in a lent transaction reads its definition from the database] → bounded to transaction-carrying contexts. Plain driver traffic is unaffected. Documented on `CachingDefinitionRegistry`.
- [A transaction lent from a different database than the definition store's is joined silently] → cannot be detected without connection identity, so it is documented on `PublishDefinition`, as `add-lent-transaction-seam` documents it for instance stores.
- [Joined reads run on a handle that is not safe for concurrent use (S2)] → Open Question 2, plus the `-race` red in task 4.1.
- [A long unit holds SQLite's write lock while the consumer's publish and start run] → inherent in joining. Documented, not mitigated.

## Migration Plan

No persisted-format change, no migration, no `go.mod` change. A consumer that publishes with a context carrying a unit sees the publish follow the unit's outcome (BREAKING, behavioral). A consumer that never puts a transaction in the context sees no change. Rollback is reverting the change.

## Open Questions

None of these is decided on kartaladev/wrkflw#151, #112 or #22. The specs avoid depending on them. Readiness is human until they are answered.

1. **What may a transaction-carrying lookup through `CachingDefinitionRegistry` serve?** Options:
   - (a) Bypass the cache entirely: no read, no write, no flight.
   - (b) Serve a committed cached hit, and on a miss go to the backing store without populating.
   - (c) Serve hits for pinned keys only.

   Under (b), `Lookup(Latest(id))` inside a unit that published a new highest Version can return the older cached Version. That is read-your-writes broken inside one unit, and it is observable. **Recommendation: (a)**, the simplest rule that keeps read-your-writes, at the cost of one database read per lookup inside a unit. On answer, add a Latest-inside-the-unit row to "A rolled-back publish is never served".
2. **Concurrency contract on a joined definition read or publish (S2).** `DefinitionStore` is documented safe for concurrent use. A `pgx.Tx` and a `*sql.Tx` are not. The #151 thread requires a `-race` red ("Red 2"), which excludes documenting it only. Options:
   - (a) Detect overlapping use of one joined handle and refuse with a new exported sentinel.
   - (b) Serialize statements on the joined handle.

   **Recommendation: (a)**, since silence is never correct. Two goroutines in one transaction have no defined order, and serializing them hides that. `add-lent-transaction-seam` lists the same hazard as a non-goal for lent contexts, so the answer should be applied to both changes. On answer, add an ADDED requirement with a negative control (sequential calls on one context succeed).
3. **G6: a joined publish whose snapshot predates a concurrent commit of the same Version.** Hypothesis, never reproduced. On MySQL REPEATABLE READ (the default), a declined `ON DUPLICATE KEY UPDATE def_id = def_id` may leave the read-back unable to see the conflicting row, which would surface as `ErrConcurrentPublish`. Retrying inside the same transaction cannot help, contrary to that sentinel's "retry" guidance. Postgres under REPEATABLE READ may instead raise a serialization failure. Task 1.3 measures this first. Options once measured:
   - (a) Keep `ErrConcurrentPublish` and amend its godoc to "retry in a new transaction".
   - (b) A new exported sentinel.

   **Recommendation:** decide on the measurement, and if it shows a wrong-content success, stop and treat it as a defect with that test as its red.

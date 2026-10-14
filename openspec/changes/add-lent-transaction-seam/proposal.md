# Proposal

## Why

A consumer cannot today make an engine write and its own row commit or roll back together: there is no exported transaction seam. `Store.RunInTx` always begins its own transaction, `Store.Load` reads through the pool and ignores the context, and `examples/real_db_transaction` imports `internal/database` to fake it. kartaladev/worktrack#26 blocks on this. The shape was decided on kartaladev/wrkflw#22 (decided): a consumer **lends** its open transaction, the engine couples exactly one step to it, performs nothing, and hands back a `Continuation` to run after the consumer commits.

## What Changes

Absorbs kartaladev/wrkflw#109 (folded in whole, items 1–7 and its test list; item 8 stays out of scope as the issue says). Builds kartaladev/wrkflw#22 (decided), calls 2, 3, 6, 7 and 9 plus its re-audit corrections; kartaladev/wrkflw#31 (decided) fixes how `add-gorm-adapter` will consume the seam.

- **New public seam in `persistence`**: a `persistence.Tx` handle built from a consumer's `pgx.Tx` or `*sql.Tx` (the latter with an explicit `persistence.Dialect`), accepted only by `persistence.WithTx(ctx, Tx) (context.Context, *Continuation)`; `Continuation.Resume(ctx) error` and `Continuation.Discard()`. Several engine calls inside one lent transaction accumulate into one `Continuation`, resumed in order.
- **New public `persistence.Dialect`**: a distinct type with `Postgres`, `MySQL`, `SQLite`, never an alias of the internal dialect type (an alias would put an internal type into every exported signature that names it).
- **One coupled step per engine call** inside a lent transaction: the step's state, journal, outbox and timer writes join the consumer's transaction under a savepoint; perform, timer activation, waiter sync and follow-up triggers are deferred to `Resume`, which re-loads and applies them through the optimistic-concurrency retry path.
- **Reads join the lent transaction**: an instance created earlier in the same lent transaction is visible to the next engine call on it.
- **A failed engine write leaves the consumer's transaction as it was** (savepoint rollback; no rollback-only mark on a handle the engine did not open), on all three dialects.
- **`CachingInstanceStore` writes around its cache** under a lent transaction, so a rolled-back consumer transaction is never served.
- **`RunInTx` joins a lent transaction** instead of beginning one; with no lent transaction its owner behavior is unchanged. `kernel.TxRunner` stays for engine-owned use.
- **`examples/real_db_transaction` is rewritten** on exported API only.
- Not BREAKING: additive API. Persisted formats: no change (no column, migration, snapshot or journal shape). Default security posture: unchanged. Consumer go.mod: unchanged (single module; no new dependency).

**Out of scope**
- Recovering a committed step whose `Continuation` is never resumed or is discarded after commit: `persist-pending-commands` (kartaladev/wrkflw#110, #167, #182). Until it lands that state is reachable on purpose and is recovered by nothing; see design Open Question 1.
- `PublishDefinition` joining a lent or ambient transaction, and `CachingDefinitionRegistry` under it: `join-ambient-publish` (kartaladev/wrkflw#151).
- `persistence.Conn`, the exported querier seam, the module split, and the 52 driver-typed signatures: `split-persistence-modules` (kartaladev/wrkflw#39). This change lands on the single module; the split later moves the finished seam into `persistence/pgx` and `persistence/sql`.
- The GORM `Transaction` wrapper that owns lend, commit and resume: `add-gorm-adapter` (kartaladev/wrkflw#42, decided on #31).

**Decisions not yet made** (design Open Questions; specs avoid depending on them): landing order against `persist-pending-commands`; misuse of a `Continuation` (resume twice, resume after discard, nested `WithTx`); an engine call under a lent transaction on a store that cannot join it; the single-module names of the two `Tx` constructors.

**Coordination**
- Size: L
- Depends on: establish-baseline-specs
- Blocks: join-ambient-publish, add-gorm-adapter, cut-v0-release, `split-persistence-modules`
- Readiness: human

## Capabilities

### New Capabilities

### Modified Capabilities
- `transaction-seam`: `RunInTx` owns the unit only when no transaction is lent; new requirements for the lent transaction, the one coupled step, the `Continuation`, savepoint isolation, reads through the lent transaction, and the caching store under it.

## Impact

- `persistence`: new `Tx`, `Dialect`, `WithTx`, `Continuation`, two `Tx` constructors; `CachingInstanceStore` write path under a lent handle.
- `runtime` / `runtime/kernel`: a lent-transaction marker and `Continuation` accumulator the driver can see without importing `persistence`; `deliverLoop` stops after the first joined commit.
- `internal/persistence/store`: `RunInTx` joins; `Load` and the coupled-path human-task reads go through the ambient handle; savepoint per joined step (pgx nested `Begin`, `database/sql` statements).
- `internal/database/transaction`: an external (non-owned) handle kind with no rollback-only marking.
- `examples/real_db_transaction`: rewritten.
- Guard: `runtime/monitor/internal_leak_test.go` must stay green over every new exported signature.

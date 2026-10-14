# Design

## Context

See proposal.md, Why. The shape is decided on kartaladev/wrkflw#22 (calls 2, 3, 6, 7, 9 and the re-audit corrections) and is not reopened here. Measured on main (`9c6dd2c1`) before relying on any premise from kartaladev/wrkflw#109 or its comments:

- `Store.querier(ctx)` discards `ctx` and reads through the pool: `internal/persistence/store/store.go:174-178`. **Holds.**
- `Store.RunInTx` always calls `transaction.Begin`: `internal/persistence/store/txrunner.go:22`. **Holds.**
- A joined participant's `Rollback` sets a rollback-only mark the owner honours at commit: `internal/database/transaction/begin.go` (`joinedQuerier.Rollback`). **Holds**; a lent handle has no owner in the engine, so the mark would be unobservable.
- `deliverLoop` (`runtime/processdriver.go:638`) builds `armed` and `cancelKeys` inside `commitFn`, commits at `:833`, then activates and deactivates timers, calls `syncWaiters`, and `perform`s each command, appending follow-up triggers to the queue. **Holds** (line numbers unchanged).
- The CAS-retrying re-delivery exists: `casRetryAttempts = 5` at `runtime/processdriver.go:592`. **Holds.**
- No public dialect type exists in `persistence`; the only one is `internal/database/timeutil.go:14` (`type Dialect int`). `persistence` names it only inside function bodies (`persistence.go:278`, `mysql.go:55`, `sqlite.go:79`). **Holds.**
- `runtime/monitor/internal_leak_test.go` fails on any exported signature naming an `internal/` type. **Holds.**
- `examples/real_db_transaction/main.go:32-33` imports `internal/database` and `internal/database/transaction`. **Holds.**
- `persistence` imports `runtime/kernel`; `runtime` does not import `persistence`. **Holds**, so the driver can only see a marker that lives in `runtime/kernel` (or `runtime`).
- The lister and the timer store's `ArmedTimer`/`ListArmed` read through the pool on purpose (`internal/persistence/store/timerstore.go:116-118`). **Holds**; they stay as they are.
- Hypothesis, not a finding: G6 (a REPEATABLE READ read-view problem on joined reads, kartaladev/wrkflw#151) was reported and never reproduced. It is not treated as a hazard here; task 1.4 measures it for `Store.Load` before relying on either outcome.

## Goals / Non-Goals

**Goals:** a consumer reproduces the lent-transaction contract with exported API only; every behavior in `specs/transaction-seam/spec.md` holds on Postgres, MySQL and SQLite.

**Non-Goals:** concurrent engine calls on one lent context (a `pgx.Tx` and a `*sql.Tx` are not safe for concurrent use; the godoc says so and nothing enforces it); recovery of an unresumed `Continuation` (`persist-pending-commands`); `PublishDefinition` under a lent transaction (`join-ambient-publish`).

## Decisions

1. **One coupled step per engine call** (decided, #22 call 6). The step's writes join the consumer's transaction; the loop then stops and moves the remaining queue, `armed` jobs, `cancelKeys`, waiter sync and the step's commands into the `Continuation`. Rejected: whole-drive-atomic, because it silently changes the durability unit from one step to the whole drive (#22 review). Consequence: every later step keeps per-step durability and incident recording, which the spec pins by comparison with an unlent call.

2. **Savepoint per joined step, no rollback-only mark on a lent handle** (decided, #22 call 9). pgx brackets with a nested `Begin` (which issues `SAVEPOINT`); `database/sql` issues `SAVEPOINT` / `RELEASE SAVEPOINT` / `ROLLBACK TO SAVEPOINT` itself. Rejected: relying on the database to abort the transaction (Postgres does, MySQL and SQLite do not, so a consumer who commits anyway would persist partial engine state); rejected: the rollback-only mark, which a consumer holding the raw transaction cannot observe. Savepoint names are engine-unique per call so they nest inside a consumer's own savepoints, including GORM's nested-transaction savepoints (`add-gorm-adapter`).

3. **A distinct public `persistence.Dialect`, never an alias** (constraint measured above; type name decided on #22 call 3 and kartaladev/wrkflw#39). An alias `type Dialect = database.Dialect` puts an internal type into every exported signature that names it and opens the alias-re-export hole the leak test cannot see. The public type converts to the internal one at the boundary; an unknown value is refused (Open Question 4 names the sentinel).

4. **Lent-transaction marker and `Continuation` accumulator in `runtime/kernel`** (decided, #109 item 2). `persistence.WithTx` stores the database handle under the store's own key and the marker under the kernel's, because `runtime` cannot import `persistence`. Rejected: detecting the handle in the driver through `persistence`, which inverts the import graph.

5. **Reads on the coupled path join the lent handle** (decided, #22 re-audit). `Store.Load` and the human-task reads the driver makes on the coupled path read through the ambient handle when one is present; without one they read through the pool exactly as today. Rejected: leaving reads on the pool, which makes a second engine call on an instance created in the same lent transaction fail its CAS (`kernel.ErrConcurrentUpdate`) or not find it.

6. **`Resume` re-loads and goes through the CAS-retry path** (decided, #22 re-audit). Another actor may move the instance between the consumer's commit and `Resume`; replaying the captured state would lose that update. Captured commands of the coupled step are performed once, follow-up triggers re-applied through the retrying entry point.

7. **`CachingInstanceStore` evicts instead of `put` under a lent handle** (decided, #22 re-audit). Its touched-id tracker exists only under its own `RunInTx`; with a lent handle it cannot learn the consumer's outcome, so it caches nothing written in that window.

8. **Candidate resolution stays before the joined commit, read-only, inside the consumer's window** (decided, #22 re-audit). It performs no write and no action, so it does not break "nothing external runs before Resume"; resolver latency lengthens the consumer's transaction, documented on `WithTx`.

9. **Land on the single module, before the split** (kartaladev/wrkflw#39, "Order with respect to #109"). The `Tx` constructors live in `persistence` for now; `split-persistence-modules` moves them to `persistence/pgx` and `persistence/sql` as `FromTx` and adds `Conn`. Not introduced here: `Conn`, the exported querier seam, any change to the 52 driver-typed signatures.

## Cross-change coupling

| Change | Coupling | Owner |
|---|---|---|
| `join-ambient-publish` | Reuses the lent/ambient handle kind and the joined-read path; must design `CachingDefinitionRegistry` against it. Blocked by this change (#20 register: #109 blocks #151). | `join-ambient-publish` |
| `add-gorm-adapter` | Its `Transaction(ctx, db, fc)` wrapper calls `WithTx`, `Resume`, `Discard` (decided, kartaladev/wrkflw#31); its nested GORM savepoints must coexist with Decision 2. | `add-gorm-adapter` |
| `persist-pending-commands` | A committed step whose `Continuation` is never resumed is #110's window. If commands become durable rows written in the step transaction (#167), they ride the lent transaction too and `Resume` becomes a fast path. Landing order: Open Question 1. | `persist-pending-commands` |
| `split-persistence-modules` | Moves the `Tx` constructors and keeps `persistence.Dialect`, `WithTx`, `Continuation` in `persistence`. Shares no file this change must edit first; prefers this change to land first. | `split-persistence-modules` |
| `cut-v0-release` | kartaladev/wrkflw#109 is a release blocker. | `cut-v0-release` |

## Risks / Trade-offs

- [A consumer commits and never calls `Resume`, or calls `Discard` after commit] → the instance stays parked at the coupled step with its commands unperformed, recovered by nothing until `persist-pending-commands`. Timers armed by the coupled step are durable and rehydrate on boot (spec scenario). Mitigation: godoc on `WithTx` shows the sequence; `add-gorm-adapter`'s wrapper makes it unforgettable for GORM users; Open Question 1.
- [Joined reads on a non-concurrency-safe handle; kartaladev/wrkflw#151 measured a real race (S2) on that shape for definitions] → task 3.3 runs the coupled path under `-race` and asserts the driver issues no concurrent statement on the lent handle.
- [Long consumer transactions] → resolver I/O and the step's writes run inside the consumer's window; SQLite holds its write lock throughout. Documented, not mitigated.
- [Savepoint names colliding with a consumer's] → engine-generated unique names; spec scenario with a consumer savepoint around the call.

## Migration Plan

Additive. No persisted-format change, no migration, no go.mod change. Rollback is reverting the change; consumers who never call `WithTx` see no behavior change (the MODIFIED `RunInTx` requirement keeps its owner scenarios verbatim).

## Open Questions

These are not decided by #22, #31 or #39; the specs avoid depending on them. Readiness is human until they are answered.

1. **Landing order against `persist-pending-commands`.** The group-16 plan put #110 first so `Resume` would be a fast path over a safety net, and the comment on kartaladev/wrkflw#109 says to re-decide it deliberately because #110 (PR #162) failed and was replaced by #167. (a) Land this first, shipping the unresumed-`Continuation` window knowingly, as #22 call 8 and #31 call 5 already accept it and #39 prefers; (b) depend on `persist-pending-commands`. **Recommendation: (a)**; it unblocks kartaladev/worktrack#26, `add-gorm-adapter` and `join-ambient-publish` now, and `persist-pending-commands` is itself an unstarted redesign. Choosing (b) adds `persist-pending-commands` to Depends on.
2. **`Continuation` misuse.** A second `Resume`, `Resume` after `Discard`, `Discard` after `Resume`, a `WithTx` on a context already carrying a lent transaction, and an engine call with the lent context after `Resume`. **Recommendation:** `Resume` runs at most once and a second `Resume` or one after `Discard` returns a new exported sentinel; `Discard` after `Resume` is a no-op; an engine call on a doubly-lent or already-resumed context is refused with a sentinel before any write.
3. **An engine call under a lent transaction on a store that cannot join it** (the in-memory store, a custom store without the capability). Silently committing per step would decouple the call from the consumer without telling it. **Recommendation:** refuse before any write with a new exported sentinel. A `Tx` from a different database than the store's cannot be detected and is documented.
4. **Single-module names and refusals for the `Tx` constructors.** **Recommendation:** `persistence.FromPgxTx(pgx.Tx) persistence.Tx` and `persistence.FromSQLTx(*sql.Tx, persistence.Dialect) (persistence.Tx, error)`, refusing a nil transaction and an out-of-vocabulary `Dialect` with exported sentinels; `split-persistence-modules` renames them to `pgx.FromTx` and `sql.FromTx`.
5. **`Resume` after a failing follow-up.** When the deferred work of one accumulated call fails, does `Resume` stop or run the remaining calls? **Recommendation:** run every accumulated call (each is an independent instance's per-step continuation) and return `errors.Join` of the failures in call order.

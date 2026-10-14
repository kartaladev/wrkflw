# Design

## Context

See proposal.md, *Why*, for the defect and the re-measured premises. The constraints below decide the approach.

- **The live path has one authority.** After each committed save, `syncWaiters` mirrors `InstanceState.MessageWaiters()` into the driver's message table and `InstanceState.SignalWaiters()` into the configured `signal.SignalBus` (`runtime/processdriver_waiters.go`). Both methods read only the snapshot (`engine/state_waiters.go:104,134`). Both skip a terminal instance, because its snapshot can still hold a repeatable event-sub-process arm.
- **There is no instance-snapshot enumeration that bypasses ownership.** `kernel.InstanceStore` offers `Create`, `Load` and `Commit` (`runtime/kernel/ports.go:69`). `kernel.InstanceLister.List` returns summaries, not snapshots, in pages of at most 200 (`runtime/kernel/lister.go:117-129`). A reader that lists and then `Load`s each instance through a `persistence.CachingInstanceStore` acquires every instance, because `CachingInstanceStore.Load` first calls `owner.Acquire`. kartaladev/wrkflw#167 measured this on #110's branch as `acquires=3 releases=0 held=3`.
- **Status is indexed on every dialect** (`internal/persistence/store/migrations/*/0001_init.sql`). Postgres has a partial index `WHERE ended_at IS NULL`. MySQL and SQLite index `status` in full.
- **The timer precedent is explicit and opt-in.** `RehydrateTimers` is a method the consumer calls once at start-up, and `scheduler.WithJobStore` self-rehydrates on `Start`.

## Goals / Non-Goals

**Goals:**
- Re-establish waiters from what is already durable, through the same authority the live path uses.
- Make the ownership defect class impossible to reintroduce unnoticed, by testing it in a shape that can express it.

**Non-Goals:**
- Changing how a waiter matches a delivery. Matching stays exact on the name and the correlation key, with last-writer-wins plus a WARN for an ambiguous key.
- Recovering anything other than waiters.

## Decisions

### D1. Specs state only behavior that every mechanism option satisfies
The requirements in `specs/restart-recovery/spec.md` hold under Q1 a, b and c alike. Where a scenario needs a verb, it says "recovers waiters" and leaves the entry point (Q2) unnamed. The maintainer's ruling on the open questions therefore changes tasks, not specs. The exceptions are Q3 and Q4 under their non-recommended options, each of which would ADD a requirement.
*Rejected:* **Specify the recommended mechanism.** A ruling for Q1 b would then require rewriting the specs.

### D2. Recovery reuses the live reconciliation and adds no enumeration of its own
Recovery feeds each recovered state through the same reconciliation `syncWaiters` performs. The terminal guard, the ambiguity WARN and the signal-bus sync are therefore shared code, not copies.
*Evidence:* the comments on `syncSignalBus` and `syncMsgWaiters` record that a stale per-construct list at the runtime layer is "exactly what hid the missing boundary and gateway arms". A second enumeration would bring that class back.
*Rejected:* **A recovery-specific waiter list.**

### D3. The ownership guard is tested with a real caching store over the counted ownership value
The ownership scenarios build a real `persistence.CachingInstanceStore` and pass it as the driver's instance store. Its `InstanceOwnership` is the instrumented value the assertion reads.
*Evidence:* kartaladev/wrkflw#167 (R3-F6) found that #110's test wired a `MemInstanceStore` and a separate mock ownership. Those are two unrelated objects, so the defect could not be expressed, and it survived three review rounds. The issue makes this test shape binding on any redesign.
*Rejected:* **A mock store or a mock ownership in that slot.** Either one excludes the defect by construction.

### D4. Live syncs win over recovery
Recovery can read a snapshot older than the one the live path has just synced. The driver therefore records, per instance, the version it last reconciled. A recovered state is applied only when no newer version has been reconciled, and that check runs under the same lock that guards the message table. The scenarios that pin this are "A wait left during recovery is not re-registered" and "A wait entered during recovery is reachable afterwards".
*Rejected:* **Lock the whole driver for the duration of recovery.** Live deliveries would stall for as long as the scan runs.

### D5. The requirements go in `restart-recovery`
Archived baseline design D2 (`establish-baseline-specs`) named this capability as the home for #181. The capability's *Purpose* still says waiter re-registration is "not yet promised". Task 5.2 edits that clause at archive.

## Open Questions

These are maintainer decisions, and Readiness is `human` until they are ruled. No thread records a decision on any of them; #181 poses Q1 and does not answer it.

**Q1. Where do recovered waiters come from?** *(changes tasks, not specs)*
- **(a) Derived from snapshots (recommended).** An optional store read capability, type-asserted the way `TimerWriter` is, streams the snapshots of non-terminal instances over the status index. `persistence.CachingInstanceStore` implements it by delegating to its backing store, never through `Load`. It adds no table, no migration and no backfill. Its cost is bounded by the live instance set, not by retention. A scan of the whole table is what made #110 unbounded, because no instance pruner exists (#167). The scan reads snapshots, not marks. That is acceptable here because nearly every non-terminal instance is parked on something, so marks would rarely prune the set.
- **(b) Durable `wrkflw_waiters` rows**, written in the step's transaction and re-read at recovery. This follows the `wrkflw_timers` precedent and #167's "query marks, not instances". The cost is a persisted-format change: a goose migration on each of the three dialects, plus a backfill that must decode every existing non-terminal snapshot in Go. Without the backfill, instances parked before the upgrade are never recovered. The rows would also be a second copy of state the snapshot already holds, and the two can drift.
- **(c) Durable rows consulted at delivery time**, replacing the in-memory tables. This is (b) plus a redesign of `signal.SignalBus`, whose public `Subscribe`, `Sync` and `Publish` hold the subscription state in memory. It is the only option that also answers Q3.

**Q2. What is the entry point?** *(changes tasks, not specs)*
- **(a) An explicit `ProcessDriver.RehydrateWaiters(ctx) error` (recommended)**, called once at start-up beside `RehydrateTimers`, mirroring that method's contract.
- **(b) Run it inside `ProcessDriver.Start`.** `Start` is documented as optional today, so a consumer who skips it would still lose waiters.
- **(c) One umbrella `Recover(ctx)`** covering timers, waiters and pending commands. This couples the change to `persist-pending-commands`. The recommendation is that `persist-pending-commands` decides whether to introduce the umbrella and folds `RehydrateWaiters` into it.

**Q3. Is cross-replica delivery in scope?** Each replica's waiter table is per-process, so a message delivered to replica B for an instance parked by replica A plausibly misses it today. This is a hypothesis, not reproduced.
- **Out of scope here; file it as intake (recommended).**
- **In scope.** This forces Q1 c and adds a requirement.

**Q4. What happens to a delivery that arrives before recovery has run?** It falls through to message-start or to a silent no-op. This is the "silence is never correct" concern.
- **(i) Document the ordering, and log a one-time WARN on the first `DeliverMessage` or `BroadcastSignal` of a driver with a durable store that has not recovered (recommended).**
- **(ii) Refuse such deliveries with a new sentinel**, for example `runtime.ErrWaitersNotRecovered`. This is fail-loud, but BREAKING for every consumer who never calls recovery, and it adds a requirement.
- **(iii) Recover inside `NewProcessDriver`.** The constructor would then perform I/O without a caller-supplied context deadline.

## Cross-change coupling

| Change | Coupling | Owner, landing order |
|---|---|---|
| `persist-pending-commands` (#110, #167, #182) | Both ADD requirements to `restart-recovery`, under distinct names and with no MODIFIED overlap. Both edit the capability's *Purpose* clause. Under Q2 c they share an entry point. Under Q1 b both add a table to the migration files; that change's Q3 decides between an in-place `0001_init.sql` edit and a `0002` migration, and this change follows the same ruling. **Wording conflict:** its requirement "Recovery is driven by pending-command rows, not by an instance scan" says an instance with no row "MUST NOT be loaded or acquired by recovery". Read unscoped, it forbids Q1 a's snapshot read. That requirement should say *pending-command* recovery, or the ruling on Q1 here must be (b). | Each owns its own requirements and table. `persist-pending-commands` owns the fix to its requirement's scope. Whichever archives second rebases its delta. Neither depends on the other. |
| `add-lent-transaction-seam` (#109) | It defers the waiter sync to `Continuation.Resume`. A continuation that is never resumed leaves waiters unsynced until this change's recovery runs at the next restart. Both call the same live reconciliation (D2). | `add-lent-transaction-seam` owns `Resume`. Landing order is free. |
| `split-persistence-modules` (#39, #207, #220) | Under Q1 a, the new read capability is implemented by the SQL stores. Under Q1 b, the new migrations live in the files that change moves. | Whichever lands second rebases. |
| `encode-persisted-enums-by-name` (#44) | Applies only under Q1 b: a waiter-kind column is stored by name from its first migration. | `encode-persisted-enums-by-name` owns the rule. |

## Risks / Trade-offs

- [Q1 a scans the snapshot of every non-terminal instance at start-up] → The scan is bounded by the live set, uses the status index and streams instead of materializing. A scenario covers 0 instances, and the ownership scenarios cover mixed sets.
- [A consumer forgets to call recovery (Q2 a)] → The production wiring examples and the README call it beside `RehydrateTimers`, and Q4 (i) logs a WARN.
- [Recovery order differs from live order for an ambiguous key] → Delivery already picks one instance and logs a WARN. The spec pins only that exactly one instance resumes and that the WARN names both.

## Migration Plan

Under Q1 a there is nothing to migrate: the change adds an entry point and an optional store capability. Rollback means removing the call. Under Q1 b, the migration and its backfill must land before any binary that relies on the rows, and rolling back leaves an unused table behind.

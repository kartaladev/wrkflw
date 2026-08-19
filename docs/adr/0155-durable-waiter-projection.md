# 155. Durable waiter projection replaces per-replica in-memory waiter caches

- Status: ⚠ Proposed — AUDIT FAILED, revision required (see docs/plans/2026-07-29-audit-findings.md)
- Date: 2026-07-28

> ⚠⚠ **NOT IMPLEMENTED, NOT ACCEPTED.** This ADR is on `main` to reserve its number and to
> preserve the design — it lives nowhere else. It **failed its audit** and needs revision before
> anyone builds on it; the findings are in
> [`docs/plans/2026-07-29-audit-findings.md`](../plans/2026-07-29-audit-findings.md).
> Do **not** treat it as a decision of record. Imported 2026-08-19 from the parked branch
> `feat/durable-waiters-delivery-correctness`, which was deleted afterwards.


## Context

"Which instances can this signal or message wake?" was answered by two
per-process in-memory maps, each written **only** as a side-effect of the
deliverLoop:

| | storage | sole writer |
|---|---|---|
| signal | `SignalBus.waiters map[string]map[string]struct{}` (`runtime/signal/signalbus.go`) | `syncSignalBus` |
| message | `ProcessDriver.msgWaiters map[msgKey]string` (`runtime/processdriver.go`) | `syncMsgWaiters` |

Both are reached from one call site — `syncWaiters`, inside the deliverLoop,
*after* the commit. `ProcessDriver.Start` starts the owned scheduler and nothing
else, and there is no `RehydrateWaiters`; the only rehydration methods are
`RehydrateTimers` and `RehydrateStartTimers` (`runtime/timerops.go`).

Two consequences followed, neither documented.

**A restart empties both registries.** A parked instance is then deaf to
`BroadcastSignal` and uncorrelatable by `DeliverMessage` until something else
happens to step it. For signals this is a liveness bug — `Publish` finds no
waiter and returns nil. For messages it is worse, because `DeliverMessage` does
not stop at "no waiter found": it falls through to a message-start create. With a
keyless non-singleton start, every redelivery mints a **fresh duplicate instance**
while the real one stays parked; with a keyed or singleton start, the
deterministic id dedups only if an instance with that exact id already exists, so
a parked instance started any other way still yields a duplicate.

**A second replica is invisible to the first.** The maps are per-process, so a
broadcast issued on replica A cannot reach an instance parked by replica B. This
was never a stated limitation; it was an unexamined consequence of the storage
choice.

`map[msgKey]string` additionally **cannot represent** two instances awaiting one
`(name, correlationKey)`. `syncMsgWaiters` overwrites, so the losing waiter is
destroyed at registration time, before delivery is attempted (ADR-0125 records
this as a WARN plus last-writer-wins).

The requirement set by the project owner is stricter than "fix the restart bug":
delivery must be **restart-safe and multi-replica-safe**, and nothing may be
silently dropped.

## Decision

Replace both in-memory maps with a **durable projection of the committed
snapshot**, written inside the state-commit transaction and read authoritatively
at delivery time.

A new port in `runtime/kernel`:

- `Waiter{Kind, Name, CorrelationKey}` — one "this instance can be woken by this
  named event" row. `WaiterKind` is `WaiterSignal` or `WaiterMessage`.
- `WaiterStore` — the read side: `SignalWaiters(ctx, name, WaiterFilter)` and
  `MessageWaiters(ctx, name, correlationKey, WaiterFilter)`, both returning a
  `WaiterPage` of instance IDs ascending so fan-out order is deterministic.
  Multiplicity is visible to the caller rather than hidden by the signature, and
  the reads are **paged** — mirroring `kernel.InstanceFilter`, the repo's only
  other cursor-paged read port — so one `Publish` never materialises an unbounded
  recipient set. (`ListArmed`, `ListDefinitions` and `ListDeadLettered` are
  unpaged; paging here is a deliberate addition, not a convention.)
- `WaiterWriter` — the write side, joining the ambient ctx-transaction
  (`JoinOrBegin`) so the projection commits atomically with the snapshot it
  derives from. `ReplaceWaiters(ctx, instanceID, ws)` makes `ws` the complete set;
  an empty `ws` deletes every row for the instance.

One SQL table, `wrkflw_waiters`, keyed **`(instance_id, kind, key_hash)`** where
`key_hash = sha256(kind ‖ 0x00 ‖ name ‖ 0x00 ‖ correlation_key)` as a fixed 64-char
column, with `name` and `correlation_key` retained as plain payload columns and a
`(kind, key_hash)` lookup index.

The hash is not an optimisation. `name` and `correlation_key` are
**expression-derived and unbounded** — `arm.MessageKey` is
`eval.EvalString(n.CorrelationKey, s.Variables)` and no length validation exists
anywhere in `definition/`. Putting them directly in a composite primary key means
an over-long correlation key fails the `INSERT` **inside `commitFn`**, rolling back
the entire state commit and wedging the instance permanently — on MySQL only,
because InnoDB caps an index key at 3072 bytes. Hashing gives identical SQL on all
three dialects with no ceiling and no cross-dialect divergence. The in-repo
precedent is `mysqlHashKey`, which SHA-256s the advisory-lock name for MySQL's
64-character limit. Because ADR-0132
mandates exactly one migration file per dialect (enforced by
`TestMigrations_OneFilePerDialect`), the table is folded into the consolidated
`0001_init.sql` of all three dialects. This is legitimate only pre-release; v0.1.0
is untagged.

The projection has exactly one producer, preserving the ADR-0123/0154 property.
It **deduplicates**: `SignalWaiters()`/`MessageWaiters()` explicitly return
duplicates when two constructs await the same name (an instance with a signal
boundary *and* a signal catch on `"escalate"` yields two identical entries), and
their documented contract is that a set-based sink collapses them. A SQL primary
key does not collapse a duplicate — it rejects it — so an undeduplicated projection
would fail the commit of any such instance. `waitersOf` sorts and compacts, which
fixes the in-memory and SQL backends with one change:

```go
func waitersOf(st engine.InstanceState) []kernel.Waiter {
	if st.Status.IsTerminal() {
		return nil
	}
	// ... st.SignalWaiters() ... st.MessageWaiters() ...
}
```

`WithWaiterStore` takes `kernel.WaiterProjection` — **both** the read and write
halves — and construction fails with `ErrNilDependency` otherwise. Type-asserting
for the writer and nil-guarding the commit hook would let a read-only
implementation (a cache decorator, a metrics wrapper, a generated mock) write no
projection at all, so every lookup returns empty and **all** delivery silently
stops. A load-bearing capability is never nil-guarded into a no-op.

`MemWaiterStore` remains the zero-config default, mirroring `MemTimerStore`, so
`processtest` and every `examples/` scenario keep working. It carries a secondary
`map[Waiter]map[string]struct{}` index so a lookup stays O(1) — the map it replaces
(`SignalBus.waiters`) was a direct hit, and this is the default backend for every
test and example on the delivery hot path. For the degraded
configuration — a durable `InstanceStore` with an in-memory `WaiterStore` —
`(*ProcessDriver).RehydrateWaiters(ctx)` pages non-terminal instances through a
`kernel.InstanceLister`, loads each, and rebuilds the set. That requires a new
`WithInstanceLister` option: the SQL `store.Store` does **not** implement
`kernel.InstanceLister` (`store.Lister` is a separate type), so a bare type-assert
would silently no-op on every SQL backend while passing all `MemInstanceStore`
tests — the precise failure shape this ADR exists to remove. `MemInstanceStore`
does implement it and is auto-detected.

### Alternatives rejected

- **Boot-time rehydrate into the in-memory maps as the primary mechanism.** Stale
  by construction for every instance parked by another replica after this one
  booted. Satisfies restart-safety, fails multi-replica safety. Retained only as
  the recovery path for the in-memory backend.
- **In-memory cache with cross-replica invalidation via `Notifier`.** MySQL and
  SQLite do not implement `dialect.Notifier` and none is injected for them
  (`internal/persistence/dialect/dialect.go`). Postgres-only; MySQL is a supported
  production backend.
- **Distributed fan-out over the eventing abstraction.** Workable for signal
  fan-out, but message correlation becomes a scatter-gather with no clean "no
  waiter exists" answer, and a down replica silently loses its instances'
  messages. It would also make `BroadcastSignal` newly depend on eventing being
  wired.
- **Derive at delivery time by scanning instances.** O(all non-terminal instances)
  full-snapshot read per delivery. Pushing the predicate into SQL would
  reimplement `SignalWaiters()`/`MessageWaiters()` as a JSON expression across
  three dialects with three different JSON capabilities — abandoning the single
  authority.

## Consequences

**Positive.**

- Restart-safe and multi-replica-safe by construction with the SQL backend: there
  is no volatile state to lose and no per-replica view to go stale.
- Drift is structurally impossible for committed states. The table is a
  *projection* of the snapshot, written in the same transaction by the same single
  authority — not a second source of truth maintained by hand. The precedent is the
  ADR-0134 timer direct-save, deliberately not the human-task table, which
  `perform()` writes after the commit and which is therefore not crash-safe in the
  same way.
- Ambiguous message correlation becomes *representable* rather than destroyed at
  registration. What to do with it is ADR-0156's decision.
- Per-step reconciliation stops being a process-global scan under a global lock
  and becomes a PK-scoped write.

**Negative / accepted costs.**

- One delete-then-insert per instance per committed step, including steps that do
  not change the waiter set. Measured optimisation (diffing against the prior
  state) is deliberately **not** done here — it would add an invariant to save an
  unbenchmarked cost, which is the trade ADR-0153 recorded as a lesson.
- The consolidated migration is edited in three dialects rather than a new file
  being added. Any consumer who has already migrated gets nothing. Acceptable
  only while v0.1.0 is untagged; this window closes on tag.
- **The zero-config default has no real transaction.** `MemInstanceStore.RunInTx`
  is `return fn(ctx)`, documented as SQL-only for rollback parity, so the
  "written in the commit tx ⇒ drift structurally impossible" property above is
  vacuous in the configuration `processtest` and every example use. Atomicity is
  asserted only in the SQL conformance suite.
- **Editing the consolidated migration has no upgrade path.** An existing
  development database must be dropped and re-migrated; ADR-0132 permits this only
  pre-tag, and the window closes at v0.1.0.
- A store without `kernel.TxRunner` self-commits each write, so a crash between
  the snapshot and the waiter write leaves a stale projection. Identical to the
  existing timer caveat, documented rather than fixed.
- `RehydrateWaiters` costs O(non-terminal instances) loads at boot for the
  in-memory backend, and remains skippable — mitigated by a construction WARN when
  a durable `InstanceStore` is paired with an in-memory `WaiterStore`.
- Orphan rows are possible only via the degraded no-`TxRunner` path or a bug
  (`Pruner` has no `PruneInstances`, so instances are never deleted). Delivery
  treats `ErrInstanceNotFound` as a self-healing skip — it deletes the row and
  continues — rather than an error. `PruneWaiters` is added as hygiene, sibling to
  `PruneTimers`.

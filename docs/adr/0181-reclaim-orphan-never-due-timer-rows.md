# 181. Reclaim orphan never-due timer rows

- Status: Proposed (audited 2026-08-14; audit findings adjudicated and folded)
- Date: 2026-08-13, revised 2026-08-14

> Design and every measurement:
> [`docs/specs/2026-08-13-never-due-gate-and-orphan-reclamation.md`](../specs/2026-08-13-never-due-gate-and-orphan-reclamation.md) §2.
> Premise evidence: `docs/specs/2026-08-13-adr-0181-0182-premise-evidence.md`.
> Audit adjudication: `docs/specs/2026-08-13-adr-0181-0182-audit-adjudication.md`.
>
> Closes backlog **23**.

## Context

ADR-0176 stopped the engine from *producing* zero-`next_run` timer rows. It did not reclaim the
ones already stored, and recorded that under *Costs accepted*: "the rehydration guard stops them
wedging boot; nothing deletes them, and `PruneTimers` provably cannot."

⚠ The figure that entered `HANDOVER.md` — "`PruneTimers` was measured deleting **1 of 5**" — is
true but misleading and should not be repeated. Executed on SQLite (pure-Go, no container started):

```
  PROBE before prune: 5 armed rows        (4 orphans + 1 healthy expired one-shot)
  PROBE PruneTimers(cutoff=…) deleted=1
  PROBE ORPHANS (zero next_run) RECLAIMED: 0 of 4; still present: 4
```

The one row deleted was the deliberate **control** — a healthy expired one-shot, not an orphan.

The predicate is `next_run < cutoff AND trigger_kind IN (Unset, OneTime, Expr)`, and
`nonRecurringTriggerKinds` is exactly the complement of `TriggerSpec.Recurring()`. A zero
`next_run` **satisfies** the cutoff clause, so the zero literal is not what excludes an orphan —
the `trigger_kind` clause is, and **every never-due kind is recurring**. The reachable set and the
orphan set are **disjoint by construction, not by cutoff choice**. "1 of 5" invites "tune the
cutoff"; nothing about tuning helps.

There is **one** implementation, not one per dialect — ADR-0081 unified them behind
`dialect.Rebind`.

### What the audit changed here

⚠ **MySQL cannot hold a zero `next_run` at all.** The original `ASSUMPTION (unverified — needs
Docker)` that "the same 0-of-4 result holds on Postgres and MySQL" was **refuted by a measurement
already in this repo** — ADR-0176's measurements §4 recorded MySQL rejecting the insert with
`Error 1292 (22007): Incorrect datetime value: '0000-00-00' for column 'next_run'` (`next_run` is
`DATETIME(6) NOT NULL`, and MySQL's `DATETIME` range starts at 1000-01-01). So **MySQL has no
orphan population**, the sweep is a structural no-op there, and the plan's prescribed Docker
checklist item was unrunnable as written: the seeding step itself raises 1292. The remaining
assumption is narrowed to **Postgres only**.

⚠ A MySQL deployment **without** `STRICT_TRANS_TABLES` / `NO_ZERO_DATE` stores `'0000-00-00
00:00:00'` instead of erroring — a value that is *not* `0001-01-01`.

## Decision

Add a **separate sweep with its own predicate** to the store's pruner:

```sql
DELETE FROM wrkflw_timers
 WHERE next_run < <Unix epoch>
   AND trigger_kind IN (<the seven recurring kinds>)
```

⚠ **Not by widening `PruneTimers`' `trigger_kind` IN-list.** Widening it would delete still-armed
recurring rows — exactly the bug ADR-0134 fixed, and the reason the IN-list exists. A second,
disjoint predicate; never a wider one.

**A threshold, not `next_run = <zero>`.** SQLite stores `next_run` as TEXT and compares it
lexicographically; ADR-0151 changed the encoding to a fixed nine-digit fraction, and `parseTimeText`
still *reads* the older trimmed form — its doc comment asserts in-repo that such rows exist. An
equality does not tolerate what a parser does, and orphans are by construction **old** rows.
Measured on SQLite with both encodings seeded plus three controls:

```
equality(zero)     seeded=8 deleted=4 survivors=[armed-future-recurring armed-past-recurring
                                                 healthy-expired-oneshot orphan-legacy-trimmed]
threshold(epoch)   seeded=8 deleted=5 survivors=[armed-future-recurring armed-past-recurring
                                                 healthy-expired-oneshot]
```

The equality form silently misses the legacy-encoded orphan and reports success. The **Unix epoch**
is the sentinel — inside MySQL's `DATETIME` range, above a non-strict MySQL's coerced
`'0000-00-00'`, lexicographically correct on SQLite under both encodings, and unreachable by a
legitimately armed **recurring** row, whose `next_run` is always computed strictly forward from the
arming instant.

⚠ **Corrected during implementation.** This ADR previously named the `armed-past-recurring` control
(a recurring row at 2020) as "the measured proof that this is not ADR-0134's hazard in disguise".
That is **false**, and the plan inherited the same error: at 2020 the row is not sub-epoch, so the
**threshold** clause rejects it before the `trigger_kind` clause is ever consulted. Measured — the
IN-list widened to all ten kinds, twice, independently:

```
survivors expected [control-expired-oneshot control-future-recurring control-past-recurring control-suboneshot]
survivors actual   [control-expired-oneshot control-future-recurring control-past-recurring]
```

`control-past-recurring` survives **every** IN-list mutation; it cannot fail on that axis at all.
As originally prescribed, **no seeded row observed the `trigger_kind` clause**, so the load-bearing
half of the predicate would have shipped untested behind a test named for guarding it.

The two controls therefore guard **different clauses**, and the fixture needs both:

- **`control-past-recurring`** (recurring, `next_run` 2020) guards the **epoch threshold** — it dies
  if the threshold is moved to `time.Now()`.
- **`control-suboneshot`** (`KindOneTime`, sub-epoch `next_run`) guards the **`trigger_kind`
  clause** — it is the only seeded row satisfying one half of the predicate and not the other, and
  it is what dies when the IN-list is widened. It must survive: a sub-epoch one-shot is already
  reachable by `PruneTimers`, and the two sweeps are correct only while they stay disjoint.

**The sweep is a single-statement `DELETE`.** A single statement re-evaluates the predicate
atomically, so a row concurrently re-armed by `upsertTimer` is safe. A `SELECT`-then-`DELETE`-by-PK
variant would open a TOCTOU window and destroy it.

**Reachability — an optional-capability interface.** All three public constructors
(`persistence.NewPruner`, `NewMySQLPruner`, `NewSQLitePruner`) return the `Pruner` **interface**,
and `internal/persistence/store` is unimportable by consumers, so a method on the concrete type
alone would be **unreachable from any consumer wiring** — dead code. `persistence.Pruner` is
**not** widened (source-breaking for implementors). Instead the public `persistence` package gains:

```go
type NeverDueTimerReclaimer interface {
    ReclaimNeverDueTimers(ctx context.Context) (int64, error)
}
```

documented with the type assertion, and pinned by a compile-time assertion that `*store.Pruner`
satisfies it.

Unlike the deploy-time gate of ADR-0182, this decision is **immune to the anchor-dependence
problem** (ADR-0176 measurements §9): it deletes on an *observed* sub-epoch `next_run` already
written to the row, not on a prediction about whether a trigger will ever be due.

## Consequences

**Positive.** The only backlog item with a measured zero-mitigation gets a real one. An operator
can reclaim rows that today require manual SQL — reachably, via the capability interface.
`Stats.NextFireAt`, measured pinned at `0001-01-01` by an orphan heading the keyset index, is
freed, and a prescribed test observes it before and after so this is not another promise nobody
builds.

**Negative / accepted.**

- ⚠ **Reclaiming the row does not unpark the instance.** The orphan is the artefact of an instance
  parked forever; deleting it removes the timer-side diagnostic while the instance stays stuck.
  Operators wanting the identities should read `ListArmed`/`Stats` **before** sweeping. The sweep
  reports only a count: per-row identities would need either a pre-`SELECT` (reintroducing the
  TOCTOU the single statement avoids) or `DELETE … RETURNING`, which MySQL lacks.
- On MySQL running under its **default strict mode** the sweep is a no-op — no orphan population was
  ever written. ⚠ **Not unconditional**: a MySQL without `STRICT_TRANS_TABLES` / `NO_ZERO_DATE`
  coerces the value to `'0000-00-00 00:00:00'` rather than erroring, and that *is* sub-epoch and
  *is* reclaimed. The public doc comment says so too; an operator who reads "no-op on MySQL" as
  unconditional would skip reviewing a destructive call.
- ✅ **The Postgres assumption is DISCHARGED, not deferred.** ⚠ It was originally recorded as
  `ASSUMPTION (unverified — needs Docker)`, and `/code-review` then measured that Postgres accepts a
  zero `next_run` (`next_run` is `TIMESTAMPTZ` with no `CHECK`, and timestamptz reaches back to
  4713 BC) — so the claim that SQLite was the only backend able to hold the fixture was **false**,
  and it had been used to argue against covering the primary production backend at all.
  `TestPrunerReclaimNeverDueTimersPostgres` now exercises the destructive `DELETE` on a real
  Postgres container, with both clause guards, and is mutation-verified to discriminate there.
- A capability interface is reachable but not discoverable: a consumer must know to assert for it.
  The alternative — widening `persistence.Pruner` — is source-breaking, which the owner declined.
- Deleting a row is irreversible. The predicate is deliberately narrow — a sub-epoch `next_run` on
  a recurring kind is unambiguously an orphan, because ADR-0176 now refuses to write one.

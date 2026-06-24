# Data retention runbook

`wrkflw` is a library, not a daemon — it does **not** run a cron and will never
prune your database on its own. Several tables grow **unbounded** under normal
operation; left unattended they eventually degrade or take down Postgres (TOAST
bloat, autovacuum stalls, full scans behind the partial indexes).

**Pruning is the consumer's scheduled responsibility.** The library gives you a
safe, ergonomic pruning surface — `persistence.Pruner` (ADR-0052) — that you call
from your own scheduled job (gocron, k8s CronJob, a ticker, whatever you already
run).

## The pruner

```go
pruner := persistence.NewPruner(pool) // pool already has persistence.Migrate applied

// In a job you schedule (you own the cadence):
now := time.Now()
_, _ = pruner.PruneOutbox(ctx, now.Add(-7*24*time.Hour))
_, _ = pruner.PruneProcessedMessages(ctx, now.Add(-7*24*time.Hour))
_, _ = pruner.PruneCallLinks(ctx, now.Add(-30*24*time.Hour))
_, _ = pruner.PruneChainLinks(ctx, now.Add(-90*24*time.Hour))
```

Every method deletes only **safely-eligible rows older than the cutoff you pass**
and returns the number of rows deleted (`int64`). All cutoffs are *strictly less
than* comparisons.

## What grows, and the recommended policy

| Table | Pruner | Eligibility | Recommended cadence | Recommended cutoff |
|---|---|---|---|---|
| `wrkflw_outbox` | `PruneOutbox` | `status='published' AND published_at < cutoff` | hourly | published **> 7 days** ago |
| `wrkflw_processed_message` | `PruneProcessedMessages` | `processed_at < cutoff` | hourly–daily | processed **> (relay max-delivery × backoff window) + large margin** (e.g. 7 days) |
| `wrkflw_call_links` | `PruneCallLinks` | `status='notified' AND notified_at < cutoff` | daily | notified **> 30 days** ago |
| `wrkflw_chain_links` | `PruneChainLinks` | `created_at < cutoff` | weekly | created **> 90 days** ago (see caveat) |

The cadences/cutoffs are starting points. Tune them to your event volume,
audit-retention requirements, and how long you keep ancestry around.

### `wrkflw_outbox`

Only rows the relay has **published** are dropped. `pending` rows (not yet
drained) and `dead` rows (quarantined after exhausting `MaxDeliveryAttempts`,
awaiting `Relay.Redrive` — ADR-0017) are **never** touched. Keep the cutoff well
past your relay poll/backoff window so a row published moments ago is never
reclaimed before any late `LISTEN/NOTIFY` or peer replica has finished with it.

### `wrkflw_processed_message`

The idempotent-consumer dedup table. A record is only safe to drop once the
message it guards can no longer be redelivered. Set the cutoff **past the relay's
`MaxDeliveryAttempts` × backoff window**, plus a generous margin — if you prune a
dedup record while its message could still arrive again, you reopen a
double-processing window. `PruneProcessedMessages` is equivalent to
`Deduper.Prune`.

### `wrkflw_call_links`

Only rows already **delivered to their parent** (`status='notified'`) are
eligible. The pruner is deliberately conservative:

- `running` children (still executing) survive.
- `completed`/`failed` children that are terminal but **not yet notified**
  (`notified_at IS NULL`) survive — the call notifier may still have to resume the
  parent from them.

So a row a parent might still need is never deleted. 30 days gives ample slack for
slow parents and operator inspection.

### `wrkflw_chain_links` (caveat: lineage loss)

Chain links are process-chaining **lineage** (which predecessor produced which
successor, ADR-0045) **and** the exactly-once chaining backstop. Pruning a link:

- loses the ancestry record for that hop, and
- removes the backstop, so if a predecessor's terminal event were somehow
  **redelivered** after its link was pruned, a successor could be re-chained.

Choose a cutoff **far beyond** any plausible terminal-event redelivery window
(≫ relay max-delivery × backoff). If you rely on long-term ancestry queries,
prune this table rarely or not at all — it is the smallest-growing of the four.

## Snapshot bloat: set `WithHistoryCap` in production

Separate from the pruners: each `wrkflw_instances` row stores the instance's
execution **snapshot** as JSONB, including its inline visit `History`. Without a
cap, that history grows with every node visit and bloats the snapshot for
long-running or loop-heavy processes.

```go
store, _ := persistence.OpenPostgres(ctx, pool, persistence.WithHistoryCap(50))
```

`WithHistoryCap(n)` retains every **open** visit plus at most the `n` most-recent
**closed** visits in the inline snapshot. This is **lossless for execution**
(open visits — the only ones the engine reads on reload — are never dropped) and
**lossless for audit**, because the `wrkflw_journal` table remains the complete
record. The default keeps full inline history for backward compatibility, so
**you should set a cap explicitly in production.** Pick `n` from your deepest
expected open-visit fan-out plus headroom.

## Operating notes

- Run pruners **off the hot path**, on their own schedule — they are plain
  `DELETE ... WHERE` statements over indexed predicates, but high-volume deletes
  still generate WAL and dead tuples.
- Let Postgres autovacuum reclaim space after large deletes; for one-off bulk
  cleanups of a long-neglected table, consider a manual `VACUUM` afterward.
- The returned delete counts are useful to log/meter so you can watch retention
  keep up with ingestion.

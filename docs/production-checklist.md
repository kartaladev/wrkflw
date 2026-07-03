# Production checklist

> See also: [`docs/retention.md`](retention.md) — data-retention runbook (pruning cadence,
> `WithHistoryCap`, table eligibility rules).

This checklist covers what you **must** configure before a wrkflw embedding goes to
production. Each item states the **concrete failure mode** if you skip it.

`persistence.WarnUnsafeConfig` is the code-level reminder. Call it once at startup with
your actual deployment profile and it logs one `WARN` per forgotten item:

```go
persistence.WarnUnsafeConfig(slog.Default(), persistence.DeploymentProfile{
    MultiReplica:       true,  // more than one replica runs concurrently
    CallLinksEnabled:   true,  // call-activity or sub-process wiring is in use
    CallLinkLeaseWired: true,  // persistence.NewAdvisoryLockOwnership is wired
    HistoryCapSet:      true,  // persistence.WithHistoryCap applied to the store
    PruningScheduled:   true,  // a consumer-owned pruning job runs regularly
})
```

`WarnUnsafeConfig` never inspects the live system — it warns based on what **you tell it**.
It is silent for a fully safe profile and never panics on a nil logger.

---

## 1. Connection pool sizing

### PostgreSQL (pgx pool)

```go
cfg, _ := pgxpool.ParseConfig(dsn)
cfg.MaxConns = 20              // cap to Postgres max_connections budget across replicas
cfg.MinConns = 2               // warm floor: avoids cold-start latency on relay/hot paths
cfg.MaxConnLifetime = time.Hour
cfg.MaxConnIdleTime = 30 * time.Minute
cfg.HealthCheckPeriod = time.Minute
pool, _ := pgxpool.NewWithConfig(ctx, cfg)
```

**Sizing rules:**

- `MaxConns × replicas + headroom` must stay under the Postgres server's `max_connections`.
  Start with 10–25 per replica and tune from `pgxpool` stats and DB connection metrics.
- The relay's listen/poll loop, the timer scheduler, and request handlers all share this
  pool — undersizing serialises the hot path; oversizing exhausts Postgres.
- If you front Postgres with **PgBouncer in transaction-pooling mode**, `LISTEN/NOTIFY`
  (the relay's low-latency wake path) needs a session-pooled or direct connection.
  The relay degrades gracefully to poll-interval fallback if notifications are lost.

**Failure mode if skipped:** pool exhaustion under load; relay drain stalls; timer
callbacks queue up; request latency spikes as goroutines block waiting for connections.

### MySQL (database/sql pool)

```go
db, _ := sql.Open("mysql", dsn)
db.SetMaxOpenConns(20)                // cap to MySQL max_connections budget
db.SetMaxIdleConns(5)                 // reuse idle connections; set ≤ MaxOpenConns
db.SetConnMaxLifetime(time.Hour)      // close long-lived connections to survive failover
db.SetConnMaxIdleTime(30 * time.Minute)
```

**Failure mode if skipped:** same as Postgres — pool exhaustion, relay stalls, timer
latency.

### SQLite

```go
db.SetMaxOpenConns(1) // REQUIRED — SQLite is single-writer; > 1 causes SQLITE_BUSY errors
```

**Failure mode if skipped:** concurrent writers get `SQLITE_BUSY` / `database is locked`
errors; data corruption is possible in WAL mode under concurrent writes without proper
serialisation.

---

## 2. Statement timeout and isolation level

### PostgreSQL

Set a **statement timeout** to bound runaway queries (e.g. a migration or prune that
runs longer than expected):

```sql
-- In the connection DSN or pool AfterConnect hook:
SET statement_timeout = '30s';
```

Or in `pgxpool.Config.AfterConnect`:

```go
cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
    _, err := conn.Exec(ctx, "SET statement_timeout = '30s'")
    return err
}
```

**Isolation level:** the store relies on `READ COMMITTED` (Postgres default) with optimistic
CAS updates. Do **not** raise isolation to `REPEATABLE READ` or `SERIALIZABLE` (unnecessary
and adds retry overhead) and do not lower it below `READ COMMITTED` (unsafe — you will
miss concurrent updates).

**Failure mode if skipped (no timeout):** a slow prune or migration holds locks; the relay
blocks; engine steps queue behind it.

### MySQL

Set `max_execution_time` per session (milliseconds):

```sql
SET SESSION max_execution_time = 30000;  -- 30 s
```

Or in the DSN: `?timeout=30s` (affects read queries). For write operations, wire it via a
`BeforeConnect` hook on the `*sql.DB` driver.

**Isolation level:** MySQL defaults to `REPEATABLE READ`. The store is designed for
`READ COMMITTED` — switch the session on connect:

```sql
SET SESSION TRANSACTION ISOLATION LEVEL READ COMMITTED;
```

**Failure mode if skipped:** under `REPEATABLE READ`, long-running transactions can cause
gap locks and deadlocks on the outbox or token tables; the relay's CAS loop may loop more
than necessary.

### SQLite

SQLite does not have session-level statement timeouts. Use `context.WithTimeout` on every
call into the store; the `database/sql` driver cancels the statement when the context
expires.

Isolation is not configurable in SQLite — the WAL journal serialises writes at the
file-system level. No action needed.

---

## 3. Production MUST-DOs (opt-in-but-unsafe-if-forgotten)

These items have safe defaults for development and testing but **must be explicitly
configured for production**. `persistence.WarnUnsafeConfig` flags each one that is missing.

### 3a. Multi-replica call-link lease (exactly-once child notification)

**What:** When you run more than one engine replica AND use call activities (child
processes notifying a parent), you must wire
`persistence.NewAdvisoryLockOwnership` and pass the resulting `Ownership` to
`runtime.NewCachingStore` so that only one replica acts on each completed child.
Without it, every replica races to notify the parent.

```go
ownership, closer, err := persistence.NewAdvisoryLockOwnership(ctx, pool)
// ... handle err; defer closer.Close()
store, _ := persistence.OpenPostgres(ctx, pool)
cachingStore, _ := runtime.NewCachingStore(store, ownership)
// use cachingStore for multi-replica exclusivity
```

**Failure mode if skipped:** when two replicas both see the child's `completed` event,
both attempt to resume the parent. The parent receives the child notification **more than
once**, causing duplicate tokens — the parent process may fork into an invalid state, run
compensation actions twice, or advance to the end event multiple times. Under at-least-once
delivery this is a correctness bug, not just a performance issue.

**Set `DeploymentProfile.CallLinkLeaseWired = true`** once this is wired.

### 3b. `WithHistoryCap` — bound snapshot history growth

**What:** Each `wrkflw_instances` row stores the instance's execution snapshot as JSONB,
including an inline visit `History`. Without a cap, the history field grows with every
node visit.

```go
store, _ := persistence.OpenPostgres(ctx, pool, persistence.WithHistoryCap(50))
// or MySQL:
store, _ := persistence.OpenMySQL(ctx, db, persistence.MySQLWithHistoryCap(50))
```

Pick `n` from your deepest expected open-visit fan-out plus headroom. `WithHistoryCap(n)`
retains every **open** visit (never dropped — these are what the engine reads on reload)
plus at most the `n` most-recent **closed** visits in the inline snapshot. The complete
audit record is always in `wrkflw_journal`.

**Failure mode if skipped:** for loop-heavy or long-running processes, the snapshot JSONB
column grows without bound. At a few hundred visits the row becomes multi-kilobyte; at
thousands it causes TOAST bloat, autovacuum stalls, full scans where index scans are
expected, and increased serialisation overhead on every state commit.

**Set `DeploymentProfile.HistoryCapSet = true`** once this is configured.

### 3c. Consumer-owned pruning cron

**What:** The library never deletes data on its own. Four tables grow unbounded without a
pruning job you schedule:

| Table | Prune with | Typical cutoff |
|---|---|---|
| `wrkflw_outbox` | `pruner.PruneOutbox(ctx, cutoff)` | published > 7 days ago |
| `wrkflw_processed_message` | `pruner.PruneProcessedMessages(ctx, cutoff)` | processed > 7 days ago |
| `wrkflw_call_links` | `pruner.PruneCallLinks(ctx, cutoff)` | notified > 30 days ago |
| `wrkflw_chain_links` | `pruner.PruneChainLinks(ctx, cutoff)` | created > 90 days ago |
| `wrkflw_timers` | `pruner.PruneTimers(ctx, cutoff)` (on public `persistence.Pruner`) | expired > 30 days ago |

```go
pruner := persistence.NewPruner(pool) // Postgres
// MySQL: persistence.NewMySQLPruner(db); SQLite: persistence.NewSQLitePruner(db)

// In your scheduled job (gocron, k8s CronJob, a ticker, …):
now := time.Now()
_, _ = pruner.PruneOutbox(ctx, now.Add(-7*24*time.Hour))
_, _ = pruner.PruneProcessedMessages(ctx, now.Add(-7*24*time.Hour))
_, _ = pruner.PruneCallLinks(ctx, now.Add(-30*24*time.Hour))
_, _ = pruner.PruneChainLinks(ctx, now.Add(-90*24*time.Hour))
```

See [`docs/retention.md`](retention.md) for eligibility rules, caveats (especially on
`wrkflw_chain_links` lineage loss), and tuning guidance.

**Failure mode if skipped:** tables grow without bound. The outbox table is the fastest
to grow (one row per domain event emitted); at high event volume it fills your disk,
causes index bloat, and makes the relay's partial-index scan progressively slower.
The dedup table (`wrkflw_processed_message`) grows with every relay delivery — without
pruning it eventually becomes a full-table-scan bottleneck for deduplication lookups.

**Set `DeploymentProfile.PruningScheduled = true`** once a pruning job is running.

---

## Quick-reference: safe startup template

```go
// At application startup — logs one WARN per unsafe configuration item.
persistence.WarnUnsafeConfig(logger, persistence.DeploymentProfile{
    MultiReplica:       cfg.Replicas > 1,
    CallLinksEnabled:   cfg.CallActivitiesEnabled,
    CallLinkLeaseWired: ownership != nil,           // set after wiring advisory-lock ownership
    HistoryCapSet:      cfg.HistoryCap > 0,
    PruningScheduled:   cfg.PruningCronEnabled,
})
```

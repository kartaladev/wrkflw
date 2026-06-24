# 52. Data-lifecycle pruners for unbounded tables

- Status: Accepted
- Date: 2026-06-25

## Context

The production-readiness audit found that four Postgres tables grow **unbounded**
with no library-provided cleanup. Left unattended they eventually take the
database down — TOAST bloat, autovacuum stalls, and full-scan regressions on the
partial indexes:

- **`wrkflw_outbox`** — the relay marks rows `status='published'` and stamps
  `published_at`, but published rows are never deleted. Every event the engine
  ever emitted accumulates forever.
- **`wrkflw_processed_message`** — the idempotent-consumer dedup table. A
  `Deduper.Prune(ctx, before)` already existed in `internal/persistence/postgres/dedup.go`
  and was already surfaced via the public `persistence.NewDeduper`. So this table
  was already prunable; the gap was discoverability alongside the others.
- **`wrkflw_call_links`** — one row per child instance, doubling as the durable
  parent-notification queue. After the parent is resumed the row reaches
  `status='notified'` (`notified_at` stamped) and is never read again, yet never
  deleted.
- **`wrkflw_chain_links`** — process-chaining lineage (ADR-0045). One row per
  predecessor→successor hop; also the exactly-once chaining backstop. Pure
  ancestry once a chain has settled, but never deleted.

`wrkflw` is a **library**, not a daemon — we do not own a cron. So the engine
cannot prune on its own schedule; it must hand the consumer an **ergonomic,
safe** pruning surface they drive from their own scheduled job.

The schema already carries the timestamps a time-cutoff pruner needs
(`wrkflw_outbox.published_at`, `wrkflw_call_links.notified_at`,
`wrkflw_chain_links.created_at`, `wrkflw_processed_message.processed_at`), so
**no migration is required**.

## Decision

Add a single aggregating **`persistence.Pruner`** (interface in the public
`persistence` façade, concrete `*postgres.Pruner` in `internal`), constructed via
`persistence.NewPruner(pool)`. Every method deletes only **safely-eligible rows
older than a caller-supplied cutoff** and returns the count deleted:

| Method | Table | Eligibility predicate |
|---|---|---|
| `PruneOutbox(ctx, cutoff)` | `wrkflw_outbox` | `status='published' AND published_at < cutoff` |
| `PruneCallLinks(ctx, cutoff)` | `wrkflw_call_links` | `status='notified' AND notified_at < cutoff` |
| `PruneChainLinks(ctx, cutoff)` | `wrkflw_chain_links` | `created_at < cutoff` |
| `PruneProcessedMessages(ctx, cutoff)` | `wrkflw_processed_message` | `processed_at < cutoff` (delegates to `Deduper.Prune`) |

Predicate safety reasoning:

- **Outbox**: only `published` rows are dropped. `pending` rows (not yet drained)
  and `dead` rows (quarantined, awaiting operator redrive — ADR-0017) are never
  touched, so pruning never loses an undelivered or recoverable event.
- **Call links**: only rows the parent has **already been notified from**
  (`status='notified'`) are eligible. `running` children and terminal-but-undelivered
  children (`completed`/`failed` with `notified_at IS NULL`) survive — a row a
  parent might still be resumed from is **never** deleted. This is deliberately
  conservative.
- **Chain links**: keyed off `created_at`. This is lineage, and pruning loses
  ancestry plus the exactly-once backstop for the affected hops (see Consequences).
- **Processed messages**: re-exposes the pre-existing `Deduper.Prune` for one-stop
  ergonomics; behaviour is unchanged.

Pruning cadence and cutoffs are documented in `docs/retention.md`. The runbook
also calls out `persistence.WithHistoryCap(n)` — the per-instance snapshot JSONB
bloats without it, and it should be set in production (the journal table remains
the full audit record, so capping inline history is lossless for execution).

## Consequences

- A consumer can now bound every unbounded table from their own scheduled job —
  the library imposes no cron and no policy, matching the library-first stance.
  The signatures are plain `(ctx, cutoff) (int64, error)` so they drop into any
  scheduler.
- **No migration and no schema change** — the pruners key off existing columns.
  Existing rows are immediately prunable.
- **Chain-links trade-off**: `PruneChainLinks` removes ancestry and the
  exactly-once chaining backstop for hops older than the cutoff. If a
  predecessor's terminal event is somehow redelivered after its link was pruned,
  a successor could be re-chained. The runbook therefore recommends a cutoff far
  beyond any plausible terminal-event redelivery window (e.g. ≫ relay
  max-delivery × backoff). The pruner exists because lineage cannot grow forever;
  the semantics are made explicit rather than hidden.
- **Engine/model/runtime diff is ZERO** — the work is confined to the persistence
  layer (`internal/persistence/postgres`, `persistence`) and docs. Pruners read
  and delete via the existing `pgxpool.Pool`; no engine code is touched.
- Error paths follow the repo convention (`workflow-postgres: pruner: ...`,
  wrapped with `%w`); callers inspect via `errors.Is` if needed.

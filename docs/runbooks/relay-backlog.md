# Runbook: relay backlog / outbox staleness

## Symptom

One or more of the following:

- `wrkflw_outbox_pending` is growing without bound.
- `wrkflw_outbox_oldest_pending_age_seconds` exceeds the warning threshold (5 min) or
  critical threshold (30 min).
- `WrkflwOutboxOldestPendingHigh` or `WrkflwOutboxOldestPendingCritical` alert fires.
- `WrkflwNoInstanceCompletions` fires alongside a non-zero `wrkflw_instances_active` —
  the engine is running instances but nothing is completing (downstream handlers are not
  receiving their events).

The outbox relay is the bridge between the engine's transactional outbox table and the
event broker.  A stalled or slow relay means process instances are waiting for events that
never arrive.

## Checks

**1. Current backlog size and oldest event age:**

```promql
wrkflw_outbox_pending
wrkflw_outbox_oldest_pending_age_seconds
```

**2. Relay status:**

```
GET /admin/relay-stats
```

Key fields to inspect:

| Field | What to look for |
|---|---|
| `pendingCount` | Should trend downward or hold steady |
| `lastPollAt` | Stale (not updated recently) means the relay goroutine has stopped |
| `pollErrors` | Repeated errors point to a DB or broker connectivity problem |
| `publishErrors` | Errors here mean the broker is rejecting events |

**3. Is the relay goroutine alive?**

If `lastPollAt` is stale relative to the configured poll interval, the relay may have
panicked or been shut down without restarting.  Check application logs for
`relay` or `wrkflw` log lines at error or fatal level.

**4. Is the broker reachable?**

Attempt to produce a test message to the broker from the same network context as the
engine.  A network partition, credential rotation, or broker restart can cut off the relay.

**5. Is the database reachable and responsive?**

The relay reads from `wrkflw_outbox` in the same database as the engine.  Check:
- DB connection pool saturation (application metrics / pg_stat_activity)
- Long-running queries holding locks on `wrkflw_outbox`
- Replication lag if the relay reads from a replica

**6. Is there a dead-letter spike causing confusion?**

```promql
wrkflw_outbox_dead
```

A high dead-letter count reduces effective `pending` but does not mean delivery is
healthy.  See `docs/runbooks/high-dlq-depth.md` if `wrkflw_outbox_dead > 0`.

## Remediation

**Relay goroutine stopped — restart the application.**

`wrkflw` is a library: the relay is started by the consumer's application.  If the relay
loop has stopped, restart the application process (or the relevant replica).  The relay is
stateless across restarts — it resumes from the `wrkflw_outbox` table.

**Broker unavailable — restore broker connectivity.**

Once the broker is reachable, the relay resumes automatically on its next poll.  No manual
action is needed for rows that are still `pending`; they will be delivered in order.

**Database contention — reduce lock pressure.**

If heavy insert load is holding locks:
- Temporarily reduce the relay poll interval to let it drain the backlog faster once
  contention eases.
- Ensure no long-running analytics queries are running against `wrkflw_outbox` during
  peak load.

**Backlog too large for the relay to drain — scale horizontally.**

The relay poll batch size and concurrency are configurable.  Increasing `BatchSize` or
running additional relay replicas (each will poll independently and race to claim rows)
drains a large backlog faster.  Note: relay replicas use optimistic locking per row; the
extra replicas do not duplicate delivery.

**Verify recovery:**

```promql
# Pending should trend toward zero
wrkflw_outbox_pending

# Published rate should pick up
rate(wrkflw_relay_events_published_total[5m])

# Oldest pending age should drop
wrkflw_outbox_oldest_pending_age_seconds
```

**Prevent future staleness — check pruning cadence.**

A relay that is technically healthy but slow may be scanning a large table due to deferred
pruning of delivered rows.  Ensure `PruneOutbox` (see `docs/retention.md`) runs on
schedule and that published rows are being dropped within 7 days of delivery.

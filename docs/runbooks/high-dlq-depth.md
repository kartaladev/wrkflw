# Runbook: high DLQ depth (`wrkflw_outbox_dead > 0`)

## Symptom

`wrkflw_outbox_dead` is greater than zero.  The `WrkflwOutboxDeadLetters` alert fires
immediately; `WrkflwOutboxDeadLettersSustained` fires if the condition persists for 10 minutes.

Dead-letter rows are outbox events that exhausted all delivery attempts
(`MaxDeliveryAttempts`).  They are quarantined in `wrkflw_outbox` with `status = 'dead'`
and are **not retried** automatically.  Until they are redriven or discarded, the downstream
subscribers that depended on those events have not received them — process instances waiting
on those events may be stuck.

## Checks

**1. How many dead rows, and which categories?**

```
GET /admin/dead-letters
GET /admin/dead-letters?category=outbox
```

The `category` query parameter filters by event type (e.g. `outbox`, `message`).  The
response lists event IDs, subjects, attempt counts, and the last error.

**2. Is the relay still running and processing?**

```
GET /admin/relay-stats
```

Look at `lastPollAt` and `errorRate`.  A relay that has stopped polling entirely (stale
`lastPollAt`) is a separate problem — see `docs/runbooks/relay-backlog.md`.

**3. Is the broker healthy?**

Check the broker (watermill backend — Kafka, SNS, RabbitMQ, etc.) for connectivity and
consumer-lag metrics.  A broker outage causes deliveries to fail, eventually exhausting
retries and creating dead letters.

**4. Are affected process instances stuck?**

For each dead-letter event that carries a process-instance correlation (visible in the
event payload or from `GET /admin/dead-letters`):

```
GET /admin/instances/{id}/lineage
```

This shows the instance's token state, open incidents, and execution history.  If the
instance raised an incident as a result of the failure, note the incident ID.

**5. Prometheus queries to scope the problem:**

```promql
# Current dead-letter count
wrkflw_outbox_dead

# Rate at which new dead letters are accumulating
increase(wrkflw_outbox_dead[10m])

# Cross-check action failures that may have caused stuck instances
sum by (action) (rate(wrkflw_action_failures_total{retryable="false"}[5m]))
```

## Remediation

**Step 1 — Fix the root cause first.**

Do not redrive until the underlying delivery failure is resolved.  Redriving into a broken
broker or a failing consumer re-exhausts retries immediately and re-creates the dead letter.

- If the broker was unavailable: confirm it is healthy before proceeding.
- If the consumer (event handler) was throwing errors: deploy the fix first.
- If the event payload was malformed: correct the root process and discard the dead letter
  rather than redriving.

**Step 2 — Redrive.**

```
POST /admin/dead-letters/redrive
Content-Type: application/json

{ "ids": ["<event-id-1>", "<event-id-2>"] }
```

Omit `ids` to redrive all dead letters (use with caution in high-volume situations).  The
relay picks up redriven rows on its next poll cycle and reattempts delivery.

**Step 3 — Resolve affected incidents.**

For any process instance that raised an incident due to the delivery failure, resolve it
via `POST /admin/instances/{id}/incidents/{incidentID}/resolve` (or
`service.Service.ResolveIncident` directly) once the underlying issue is fixed and the
event has been successfully delivered.

**Step 4 — Check `wrkflw_outbox_dead` drops to zero.**

```promql
wrkflw_outbox_dead
```

Confirm the gauge reaches zero within a few relay poll cycles.  If it stays elevated,
repeat the investigation from step 1.

**Step 5 — Consider tuning `MaxDeliveryAttempts` and backoff.**

If transient broker unavailability routinely exhausts retries, increase
`MaxDeliveryAttempts` or the backoff window in your relay configuration so the relay rides
out brief outages without creating dead letters.

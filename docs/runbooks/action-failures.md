# Runbook: action failures

## Symptom

One or more of the following:

- `wrkflw_action_failures_total` counter is increasing.
- `WrkflwActionFailureRateHigh` alert fires (> 0.5/s sustained 5 min).
- `WrkflwActionFailureRateCritical` alert fires (> 2/s sustained 5 min).
- `WrkflwActionNonRetryableFailures` alert fires (any non-retryable failures detected).
- Process instances are stalled; incidents visible on `GET /admin/instances/{id}/lineage`.

Actions in the `wrkflw` engine are implementations of `action.Action` (HTTP calls, email
sends, transforms, log actions, or consumer-custom implementations).  A failure may be
retryable (the engine re-queues automatically up to the configured retry limit) or
non-retryable (the engine immediately raises an incident and halts the instance's current
path).

## Checks

**1. Which actions are failing and are the failures retryable?**

```promql
sum by (action, retryable) (rate(wrkflw_action_failures_total[5m]))
```

The `action` label is the catalog name of the action (e.g. `httpcall`, `email`,
`transform`, or a consumer-registered name).  The `retryable` label is `"true"` or
`"false"`.

**2. What is the current retry pressure?**

```promql
rate(wrkflw_action_retries_total[5m])
```

A high retry rate without eventual recovery means the underlying error is persistent, not
transient.

**3. Are instances raising incidents?**

For each stuck instance identified from monitoring:

```
GET /admin/instances/{id}/lineage
```

The lineage response includes open incidents with their error messages, the node and action
that failed, and the full token history up to the failure point.

**4. Are there dead letters in the outbox caused by action-side failures?**

```
GET /admin/dead-letters
```

Some action implementations (e.g. `httpcall`) may have triggered follow-up events that
also failed.  Cross-check with `docs/runbooks/high-dlq-depth.md`.

**5. Action-specific diagnostics:**

| Action type | What to check |
|---|---|
| `httpcall` | Upstream HTTP service status, TLS certificates, firewall rules, request timeout |
| `email` | SMTP/API-gateway connectivity, authentication credentials, recipient validity |
| `transform` | Expression syntax errors (check the expression in the process definition), input variable presence |
| Custom action | Application logs for the action's own error messages |

## Remediation

**Retryable failures — wait or fix the upstream.**

If `retryable="true"`, the engine retries automatically using the configured backoff.  If
retries are also failing:
- Confirm the upstream service is healthy (HTTP 5xx, timeout, network).
- If a deployment is in progress, wait for it to complete.
- If the issue is persistent (bad credentials, misconfigured endpoint), fix the definition
  or the environment variable and redeploy.

Once the upstream is healthy, in-progress retries complete on the next attempt.  No manual
engine intervention is required for retryable failures that ultimately succeed.

**Non-retryable failures — resolve the incident.**

Non-retryable failures (`retryable="false"`) represent logic errors, bad input, or
permanent upstream rejections.  The engine raises an incident and parks the instance.  To
recover:

1. Identify the root cause from the lineage response (`GET /admin/instances/{id}/lineage`).
2. Fix the root cause (correct the process definition expression, fix the upstream, supply
   the missing variable, etc.).
3. Resolve the incident via `POST /admin/instances/{id}/incidents/{incidentID}/resolve`
   (or `service.Service.ResolveIncident`) to allow the engine to retry or continue from
   the failed node.

**High failure rate across many actions — check the engine's shared dependencies.**

If multiple action types are failing simultaneously, the fault may be upstream of the
actions:
- Database connectivity (the engine reads instance state before invoking each action).
- Relay backlog (events from prior steps never arrived to trigger the current step).
- OOM or CPU throttling on the application pod/process.

Check `wrkflw_store_duration_seconds` for slow or failing persistence operations, and
`wrkflw_outbox_oldest_pending_age_seconds` for relay lag.

**Scale out if the retry queue is overwhelming a single replica.**

Under sustained high failure + retry load, the in-memory retry scheduler can back up.
Adding replicas distributes the retry load.  Ensure your instance store and timer store
are shared (both backed by the same database) so retries are not lost on replica restart.

**Verify recovery:**

```promql
# Failure rate should drop
sum(rate(wrkflw_action_failures_total[5m]))

# Retry rate should also normalise
rate(wrkflw_action_retries_total[5m])

# No new incidents (cross-check action failures with incidents raised)
rate(wrkflw_incidents_raised_total[5m])
```

# 17. Outbox relay poison isolation and dead-letter quarantine

- Status: Accepted
- Date: 2026-06-21

## Context

The transactional-outbox `Relay` (`internal/persistence/postgres`, ADR-0006) polls
`wrkflw_outbox` and publishes each event through a `runtime.Publisher`. Today it claims a batch
`FOR UPDATE SKIP LOCKED`, publishes each row inside one transaction, and **rolls back the entire
batch on any publish error** (Persistence deferred #5; Eventing deferred #3). The schema has only
`published_at` (NULL = unpublished) — no retry counter, no dead-letter state.

The failure mode: one persistently-failing ("poison") event rolls the whole batch back on every
poll cycle. Healthy events claimed alongside it are withheld until the poison event is resolved —
classic head-of-line blocking. At-least-once delivery is intact, but throughput collapses behind a
single bad message.

The prevailing fix across SQS (`maxReceiveCount` → `deadLetterTargetArn`), Azure Service Bus
(`MaxDeliveryCount` → `$deadletterqueue`), and outbox-pattern write-ups is a per-message
**delivery-count counter vs a ceiling, overflow to a dead destination**, combined with a
**claim predicate that excludes dead/not-yet-due rows** so the batch advances past poison.

## Decision

Add poison isolation and a logical dead-letter queue **in the outbox table itself** (no separate
broker, no separate table):

- New additive columns: `status TEXT NOT NULL DEFAULT 'pending'` (`pending`|`published`|`dead`),
  `retry_count INT NOT NULL DEFAULT 0`, `next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now()`,
  `last_error TEXT`. A goose migration backfills `status='published'` where `published_at IS NOT
  NULL`, replaces the unpublished partial index with a `(next_attempt_at) WHERE status='pending'`
  claim index, and adds a `WHERE status='dead'` index.
- **Claim predicate:** `WHERE status='pending' AND next_attempt_at <= now() ORDER BY id FOR UPDATE
  SKIP LOCKED LIMIT $batch`. `dead` and not-yet-due rows are skipped — a poison event no longer
  blocks the batch.
- **Per-row isolation:** publish each claimed row, then update *that row*. Success →
  `status='published', published_at=now()`. Failure → `retry_count++`,
  `next_attempt_at = now() + relayBackoff(retry_count)` (pure capped-exponential helper, driven by
  the injected `clock.Clock`), `last_error = err`. When `retry_count >= MaxDeliveryAttempts`
  (configurable, default 10) → `status='dead'`. One row's failure does not roll back its peers.
- **DLQ admin API** on the `persistence` façade (interface/value types only, ADR-0008):
  `ListDeadLettered(ctx, limit)` and `Redrive(ctx, ids...)` (reset to `pending`, `retry_count=0`,
  `next_attempt_at=now()`).

## Consequences

**Easier:** a single poison event is retried with backoff and, after a ceiling, quarantined to
`dead` while every healthy event flows — head-of-line blocking is gone. Operators get a queryable
DLQ (`last_error`, `retry_count`) and a one-call `Redrive` once the downstream is fixed.
At-least-once is preserved (a row is only marked `published` after a successful publish). The whole
mechanism is one table + a pure backoff helper; the broker stays unaware (watermill is never
imported here).

**Harder / trade-offs:** quarantining a `dead` row **breaks strict ordering** — but only within
the affected `instance_id` lane, and `SKIP LOCKED` never promised global order anyway (documented).
Per-row updates replace one batch commit with N row updates, a modest write-amplification increase
on the hot relay path. A consumer that has not adopted idempotent consumption (ADR-0018) can now
observe more duplicates, since per-row retry re-publishes only the failed row rather than the whole
batch — net *fewer* duplicates than the old full-batch rollback, but the at-least-once contract is
unchanged and idempotent consumption remains the consumer's responsibility. `MaxDeliveryAttempts`
is a global relay setting in v1, not per-topic.

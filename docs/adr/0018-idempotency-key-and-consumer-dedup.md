# 18. Idempotency: stable action key + consumer dedup table

- Status: Accepted
- Date: 2026-06-21

## Context

The retry executor (ADR-0015) and the relay (ADR-0017) both deliver **at-least-once**: a
`ServiceAction` may run more than once (the action succeeds but the runtime crashes before
recording the result, so the retry re-invokes it), and an outbox event may be published more than
once (the relay publishes but crashes before marking the row `published`). "Exactly-once" in
practice means **at-least-once delivery + idempotent processing** = exactly-once *effect*.

Without help, both sides risk duplicate side effects: a retried payment action charges twice; a
redelivered event double-applies. The prevailing patterns are (a) a **stable idempotency key**
passed to the side-effecting system so *it* dedups (Temporal derives `WorkflowRunID + ActivityID`;
Stripe takes a client `Idempotency-Key`), and (b) an **idempotent-consumer dedup table** keyed on
message id, written in the same transaction as the business effect (Chris Richardson's
`PROCESSED_MESSAGE` pattern), so a duplicate insert fails and the handler skips.

## Decision

Provide both halves:

- **Producer / action side — stable key.** When `Step` emits an `InvokeAction` for a node, it
  stamps `Input["_idempotencyKey"] = instanceID + ":" + nodeID`. The key is
  **attempt-independent** — identical across every retry of the same action on the same instance —
  so a `ServiceAction` author can dedup an external side effect across retries. The engine already
  holds `instanceID` and `nodeID`, so this stays pure and deterministic. Documented in the
  `action.ServiceAction` godoc and the engine spec as part of the at-least-once contract.
- **Consumer side — dedup table.** A new `wrkflw_processed_message (subscriber TEXT, message_id
  TEXT, processed_at TIMESTAMPTZ NOT NULL DEFAULT now(), PRIMARY KEY (subscriber, message_id))`
  table and a `Deduper` port on the `persistence` façade:
  `Seen(ctx, tx, subscriber, messageID) (firstTime bool, err error)` implemented as `INSERT ... ON
  CONFLICT DO NOTHING`. `message_id` is the outbox `dedup_key`. The consumer calls `Seen` **inside
  its own business transaction**, so the dedup record and the side effect commit atomically;
  `firstTime == false` ⇒ a duplicate, skip the effect. Interface + value types only (ADR-0008); the
  pgx wiring stays in `internal/persistence/postgres`.

## Consequences

**Easier:** action authors get a ready-made stable key for external dedup with zero configuration;
consumers get a drop-in `Deduper` that turns our at-least-once delivery into exactly-once effect,
committed atomically with their work. The end-to-end loop closes: outbox (producer at-least-once) →
relay retry/DLQ (ADR-0017) → broker at-least-once → idempotent consumer dedup. Both pieces are
small and sit behind the existing façade.

**Harder / trade-offs:** the `_idempotencyKey` occupies a reserved key in the action `Input` map —
a (documented, underscore-prefixed) namespace a consumer must not overwrite. The key dedups across
retries of the *same node instance* only; it deliberately does **not** dedup across distinct
instances or re-runs (different `instanceID`), which is correct but means a consumer wanting
cross-instance dedup needs its own key. The `wrkflw_processed_message` table grows unbounded and
needs a retention/pruning job (a duplicate can only arrive within the relay/broker redelivery
window, so a TTL well past `MaxDeliveryAttempts × max backoff` is safe to prune — left to the
operator). Using the `Deduper` is opt-in: a consumer that ignores it still sees duplicates.

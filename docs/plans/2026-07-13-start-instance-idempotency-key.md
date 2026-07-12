# Ticket: idempotency key for StartInstance

- Filed: 2026-07-13
- Status: OPEN — needs design (ADR + brainstorming before implementing)
- Area: `runtime` (ProcessDriver) / `engine` StartInstance
- Priority: P2 (developer experience)

## Summary

There is no first-class way to start a process instance idempotently on a
caller-supplied **business key**. A consumer that wants "start this order's
workflow exactly once" must either mint a deterministic instance id themselves
or build their own key→instance bookkeeping.

## Current behavior

`ProcessDriver.Drive(ctx, def, instanceID, vars)` (and the message/signal start
paths) dedup on the **instance id**:

- If the caller passes a fixed `instanceID`, `Store.Create` returns
  `kernel.ErrInstanceExists` on a second call, so the caller can treat it as a
  duplicate no-op (this is how message-start dedup works, ADR-0121).
- If the caller passes an empty `instanceID`, the driver mints a fresh id via
  the id generator — so two calls create two independent instances.

So idempotency is only achievable today by making the caller responsible for
choosing a stable, collision-free instance id derived from their business key.

## Problem

Callers frequently have a natural idempotency key (order id, request id, an
inbound message id) but do not want to own instance-id assignment. Without a
key seam they must reimplement deterministic-id derivation and the
ErrInstanceExists dance at every call site.

## Proposed directions (to evaluate in an ADR)

1. **Deterministic id derivation.** Add `WithIdempotencyKey(key string)` to the
   start path; the driver derives a stable instance id from the key (e.g. a
   namespaced hash) and follows the existing ErrInstanceExists dedup. Cheapest;
   no schema change; the instance id becomes opaque-but-stable.
2. **Key→instance mapping table.** Persist `(idempotency_key → instance_id)` in
   a small dedup table (mirrors the outbox dedup pattern) and look it up before
   Create. Keeps the instance id free (still minted) but adds a table + a lookup
   on the hot start path.
3. **Return the existing instance on collision.** Regardless of 1 or 2, decide
   the ergonomics: return `(existing state, nil)` vs a typed
   `ErrDuplicateInstance` the caller unwraps. Message-start already treats a
   duplicate as a no-op; a general start likely wants the same.

## Open questions

- Scope of the key: global, or per-definition-id?
- TTL / retention of the mapping (does an idempotency key expire)?
- Interaction with the message-start deterministic-id dedup (ADR-0121) — reuse
  the same mechanism or keep separate?

## Non-goals

- Not a correctness bug; today's id-based dedup works. This is an ergonomics
  seam.

## Note

Originally could not be filed as a GitHub issue (the `gh` CLI is
unauthenticated in this environment); tracked here as a repo doc-ticket instead.

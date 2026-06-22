# 33. Ops-hardening trio: dedup pruning, MarkNotified clock, advisory-lock close guard

- Status: Accepted
- Date: 2026-06-23

## Context

Three small, independent production-hardening items accumulated as deferred follow-ups, all in the
persistence layer (no engine/model impact):

1. The idempotent-consumer dedup table `wrkflw_processed_message` (ADR-0018) grows unbounded with no
   supported pruning path — an operator burden flagged as the "`wrkflw_processed_message`
   retention/pruning job" backlog item.
2. `internal/persistence/postgres` `CallLinkStore.MarkNotified` stamps `notified_at` with
   `time.Now().UTC()` rather than the store's injected `c.clk` (added in ADR-0031), so a fake clock
   can't drive it deterministically — the "MarkNotified clock injection" backlog item.
3. `AdvisoryLockOwnership` (ADR-0020) documents post-`Close` `Acquire`/`Release` as "undefined
   behaviour": they call `Exec`/`QueryRow` on a `*pgxpool.Conn` already returned to the pool — the
   "AdvisoryLockOwnership use-after-close guard" backlog item.

## Decision

Bundle the three into one non-engine track:

1. **`Deduper.Prune(ctx, before time.Time) (int64, error)`** (internal + `persistence.Deduper`
   interface): `DELETE FROM wrkflw_processed_message WHERE processed_at < $1`, returning the deleted
   count. The cutoff is an absolute time supplied by the caller (who owns the clock and retention
   policy), keeping `Deduper` clock-free and the method trivially testable. No new migration (DML on
   an existing table).
2. **`MarkNotified` uses `c.clk.Now().UTC()`** instead of wall-clock. `c.clk` defaults to
   `clock.System()`, so existing callers are unaffected; fake-clock tests become deterministic. No
   signature change.
3. **`AdvisoryLockOwnership` close guard**: a `closed bool` (under the existing `mu`); `Close` sets
   it and is now idempotent; `Acquire`/`Release` return a new sentinel `ErrOwnershipClosed`
   (`workflow-postgres: ownership: closed`) when closed, instead of touching the released conn.

## Consequences

**Positive**
- Operators get a supported, testable dedup-retention primitive; the dedup table no longer grows
  forever.
- `MarkNotified` is now fully clock-injectable — consistent with the rest of the call-link store and
  enabling deterministic tests of `notified_at`.
- Use-after-close on the advisory-lock ownership is a clear error, not a latent panic/undefined
  behaviour; `Close` is idempotent.
- Engine/model untouched; all changes confined to persistence.

**Negative / trade-offs**
- `Prune` is a manual/operator-scheduled DML, not an automatic background job — the consumer must
  call it on a cron with a safe `before` (well past the relay's max-delivery × backoff window) to
  avoid pruning ids still relevant to an in-flight redelivery. Documented on the method.
- `ErrOwnershipClosed` is a new exported sentinel (small public-surface addition).

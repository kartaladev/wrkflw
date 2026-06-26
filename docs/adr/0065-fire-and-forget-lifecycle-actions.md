# 65. Fire-and-forget lifecycle actions (deadline-breach and reminder)

- Status: Accepted
- Date: 2026-06-26

## Context

Two timer-driven lifecycle actions run for their side effect only, with no token
awaiting their result:

- the **deadline-breach action** (`handleDeadlineFired` in `engine/step_timers.go`),
  emitted while the engine simultaneously routes the parked token down the
  deadline (alternative) flow; and
- the **reminder action** (`handleReminderFired`), emitted while the token stays
  parked on its task token.

Both were emitted as ordinary `engine.InvokeAction{CommandID, Name, ...}`. The
runtime ran the action and fed the resulting `ActionCompleted` back into the
engine via `Step`. There `handleActionCompleted` called `tokenAwaiting(cmdID)`,
found no token parked on that command id (the breach moved the token elsewhere;
the reminder never parked one), and returned `ErrTokenNotFound` ("no token
awaiting command"). The timer-fire path in the runner logged this at
`LevelError` as `runtime: timer fire: Deliver failed` and swallowed it. The
instance still completed correctly, but every breach/reminder produced a
spurious error log — operational noise that masks real failures.

The obvious "fix" — making `handleActionCompleted`/`handleActionFailed` no-op on
an unmatched command — is wrong: `ErrTokenNotFound` for a genuinely unmatched
command is an intentional, tested contract. `service` maps it to `ErrConflict`
(HTTP 409) for late/duplicate triggers; `internal/persistence/postgres`'s
call-notifier relies on it to detect a completed parent; and `engine`'s
sub-process tests assert that an unrecognised command id returns it. Weakening
the handler would erode all three.

## Decision

We add a boolean `FireAndForget` field to `engine.InvokeAction`. The engine sets
`FireAndForget: true` on exactly the two fire-once emissions — the
deadline-breach action and the reminder action — and on no other `InvokeAction`
(main-action invocation, retry re-invocation, and compensation are unchanged).
`CommandID` is still populated, so the action remains traceable in spans and
metrics even though no result is fed back.

In `runtime`'s `perform`, the `InvokeAction` branch, when `FireAndForget` is
true, still opens the tracing span and records the action-duration metric, runs
the action through the same `resolveInvokeAction` resolution path (so scoped and
inline catalogs keep working), but returns `(nil, nil)` — no trigger — in every
outcome: success, action error (logged at `Warn`), and resolution miss (logged
at `Warn`). When `FireAndForget` is false, behaviour is byte-for-byte unchanged:
resolve → run → return `ActionCompleted`/`ActionFailed`.

The `ErrTokenNotFound` contract in `handleActionCompleted`/`handleActionFailed`
is left exactly as-is. The fix prevents the fire-once completion from ever being
produced, rather than tolerating an unmatched one.

## Consequences

- The spurious `LevelError "Deliver failed"` log for breach and reminder actions
  is eliminated; error logs again indicate genuine failures.
- Failures of breach/reminder actions are now **logged at `Warn`, not fed back**
  into the engine. This is the correct behaviour: those results were never
  actionable (no token could receive them), so feeding them back only ever
  produced the noise this change removes.
- Observability for breach/reminder actions is retained — the tracing span and
  the action-duration metric are still emitted on the fire-and-forget path.
- The `ErrTokenNotFound` → 409 / completed-parent-detection / sub-process
  contract is preserved unchanged for genuine late or duplicate triggers.
- `InvokeAction` gains one field; consumers constructing it directly are
  unaffected (the zero value `false` preserves prior behaviour). `FireAndForget`
  remains distinct from `InvokeCancelAction`, which is cancel-specific and
  carries no `CommandID`.

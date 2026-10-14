# Proposal

## Why

A process that restarts forgets every signal and message waiter its parked instances registered, so a later `DeliverMessage` or `BroadcastSignal` never reaches them and nothing reports the loss. Timers already survive a restart (`RehydrateTimers`, scheduler self-rehydration); waiters are the same durability gap one field over, filed as kartaladev/wrkflw#181 and left unowned when #110 was deferred.

Re-measured on `main` at `9c6dd2c1` by reading the code; no probe was run, so the behavioral consequences below stay hypotheses until the first task's failing test reproduces them.

| Premise | Measurement on main |
|---|---|
| #181: `syncWaiters` is in-memory, at `runtime/processdriver_waiters.go:20-23`, with a single call site at `processdriver.go:898` | **Holds**, line numbers unchanged. The message table is the driver's `msgWaiters` map; the signal side is `signal.SignalBus`'s in-memory subscription map, filled only through `Sync`/`Subscribe`. Neither is written anywhere else, and no start-up path fills them. |
| #181: "nothing reports that they are missing" | **Holds.** `DeliverMessage` (`runtime/processdriver_message.go:65`) falls through to message-start creation when no waiter matches, and returns nil when no definition starts on the message. `BroadcastSignal` reaches only subscribed instances. |
| #181: "whether re-establishing them is derivable from instance state at boot … nobody has checked" | **Checked: derivable.** `engine.InstanceState.MessageWaiters` and `SignalWaiters` (`engine/state_waiters.go:104,134`) are pure functions of the persisted snapshot, and they are exactly what the live path mirrors. |
| #181: #167's command rows do not cover waiters | **Holds.** A pending command and a waiter have different lifetimes; a waiter lives as long as the wait. |
| Hypothesis (not reproduced): after a restart, a keyed message for a parked instance whose definition also starts on that message creates a **second** instance instead of resuming the first. | Read off `DeliverMessage`'s fall-through; reproduced by task 1.1. |

## What Changes

kartaladev/wrkflw#181 is **absorbed** whole. Nothing in it was found stale.

- A fresh `ProcessDriver` over the same stores can re-establish every signal and message waiter that the non-terminal instances it reads were parked on, so a delivery after a restart reaches the same instance it would have reached before.
- Recovery registers nothing for a terminal instance, including one whose snapshot still holds a repeatable event-sub-process arm, matching the live path's guard.
- Recovery takes ownership of no instance. A store wrapped in `persistence.NewCachingInstanceStore` must not be driven into acquiring every instance it lists. That is the defect #167 records for #110's instance-scanning sweep, and this change does not repeat it.
- Recovery is idempotent and safe beside live traffic: running it twice, or while instances advance, never routes a delivery to a wait the instance has already left.
- The mechanism, the entry point and the cross-replica question are **undecided** (design Open Questions 1 to 4). The specs state only behavior that holds under every option. Where a scenario needs a verb, it says "recovers waiters" and does not name the entry point.
- **Not BREAKING** under the recommended options: the change adds an entry point and a read capability.
- **Persisted format: none under the recommended option (Q1 a).** Option Q1 b adds a `wrkflw_waiters` table, a goose migration on each of the three dialects and a backfill, and it would make this a persisted-format change. **Default security posture: unchanged. Consumer go.mod: unchanged.**

**Out of scope**
- Re-driving committed steps whose commands were never performed, plus attempt counts, backoff and dead-lettering: `persist-pending-commands` (kartaladev/wrkflw#110, #167, #182).
- Re-syncing waiters after a lent transaction's `Continuation` is never resumed: `add-lent-transaction-seam` defers the waiter sync to `Resume`. This change's recovery covers that state at the next restart, and nothing covers it earlier.
- Delivering to an instance parked by a **different live replica** (Open Question 3). No change owns it. The recommendation is to leave it out here and file it as intake.
- Timer rehydration is already specified and is not changed.

**Coordination**
- Size: M
- Depends on: nothing
- Blocks: nothing
- Readiness: human

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `restart-recovery`: ADDS waiter recovery. Parked signal and message waits resume after a restart, terminal instances are excluded, no instance ownership is acquired, and recovery is idempotent beside live traffic. Archived baseline design D2 placed #181 here.

## Impact

- `runtime`: a waiter-recovery entry point on `ProcessDriver` (name pending, Q2). It refills the message-waiter table and the configured `signal.SignalBus` through the same reconciliation the live path uses.
- `runtime/kernel` and the stores: under Q1 a, an optional read capability enumerating non-terminal instance snapshots without acquiring ownership. It is implemented by the in-memory store, the Postgres, MySQL and SQLite stores, and `persistence.CachingInstanceStore`, which must delegate to its backing store without calling `Acquire`.
- `examples/production_wiring`, `sqlite_wiring` and `mysql_wiring` call recovery beside `RehydrateTimers`. `runtime/README.md` documents it.
- Persisted formats: none under Q1 a. A new table and migrations on all three dialects under Q1 b.

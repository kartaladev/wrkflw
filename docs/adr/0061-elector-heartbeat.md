# 61. Leader-elector heartbeat to narrow the timer split-brain window

- Status: Accepted
- Date: 2026-06-25

## Context

ADR-0059 added `PostgresElector`, a `gocron.Elector` that gives single-leader timer
firing across replicas via one session-level advisory lock held on a dedicated
pooled connection. To satisfy gocron's per-job-run hot path, `IsLeader` is
**sticky**: once leadership is won it returns `nil` from an in-memory, mutex-guarded
bool with **no DB round-trip**.

ADR-0059 explicitly documented a residual **split-brain window** that stickiness
opens: if the leader's dedicated connection is severed *server-side* (for example
the backend is killed by `pg_terminate_backend`, an admin restart, an idle-session
timeout, or a network partition that drops the TCP session), Postgres
**auto-releases the advisory lock**, yet the process keeps running and its sticky
`IsLeader` keeps returning `nil`. Meanwhile a follower can now win the freed lock on
its next attempt — a transient **two-leader** state. ADR-0059 downgraded this to
"redundant fires, not double-execution" thanks to the ADR-0027 version-CAS plus
in-tx timer-row deletion, and noted that "a lease/heartbeat that re-checks the lock
would close the window" but deferred it to avoid distributed-scheduler machinery.

The window is unbounded in time: a stuck two-leader state persists for as long as
the severed-but-running leader stays up, producing a steady stream of redundant
`Deliver` attempts and CAS-conflict log noise — exactly the storm the Elector exists
to remove. We want to bound it without rebuilding a distributed scheduler.

## Decision

We add a **bounded background heartbeat** to `PostgresElector` that periodically
re-validates leadership and steps down on silent loss.

- **Lazy lifecycle.** The heartbeat goroutine starts **on first leadership
  acquisition** (inside `IsLeader`, guarded by a one-shot `started` flag) — not at
  construction — so followers that never win leadership spawn no goroutine. It runs
  for the elector's lifetime and is **stopped by the existing `Close()`**: `Close`
  cancels the goroutine's background context, closes its `done` channel, and
  `wg.Wait()`s for it to exit before releasing the dedicated connection. There is no
  goroutine leak (enforced by the package's `goleak.VerifyTestMain`).
- **Configurable interval.** `WithHeartbeatInterval(d)` sets the tick period
  (default **5s**). The façade exposes it as
  `scheduling.WithElectorHeartbeatInterval(d)`.
- **Clock-driven, testable.** The heartbeat ticker is created from a
  `clockwork.Clock` injected via `WithElectorClock` (default: a real clock). The
  façade threads the scheduler's own clock into the elector, so under test one fake
  clock advances both timer firing and heartbeat ticks (ADR-0003).
- **Silent-loss detection.** Each tick calls `conn.Ping(ctx)` on the dedicated
  connection. A failed ping means the backend was severed — and with it the
  advisory lock that Postgres auto-released — so the heartbeat sets `isLeader =
  false` under the mutex. The **next** `IsLeader` then re-attempts
  `pg_try_advisory_lock` (re-winning if the lock is free, or returning
  `ErrNotLeader` to step aside for a follower).
- **Connection-concurrency guarding.** The dedicated `*pgxpool.Conn` is shared by
  `IsLeader`, `Close`, and the heartbeat. **All** conn access is serialized under
  the existing `sync.Mutex`, so there is no pgx data race; `Close` flips `closed`
  under the lock, then stops the goroutine and waits before releasing the conn, so a
  tick can never `Ping` a released connection.
- **Hot path unchanged.** `IsLeader`'s sticky fast path still returns `nil` with no
  round-trip; the heartbeat, not the hot path, is what catches silent loss.

This **narrows the split-brain window to at most one heartbeat interval** (≤ 5s by
default, operator-tunable). It supersedes only the "no heartbeat / unbounded window"
caveat of ADR-0059; the Elector's shape, key handling, failover, and
mutual-exclusion-with-Locker semantics are otherwise unchanged.

## Consequences

- The two-leader window is bounded to ≤ one heartbeat interval instead of "until the
  severed process is restarted." Operators trade a tighter window for one extra
  cheap `Ping` round-trip per interval per leader, and one long-lived goroutine on
  the leader only.
- **The ADR-0027 version-CAS remains the exactly-once backstop**, unchanged. The
  heartbeat reduces redundancy further; it is not a correctness mechanism on its own.
  Even within the residual ≤-interval window, double-firing is downgraded to
  redundant (idempotently-rejected) fires, exactly as before.
- The "no lease/heartbeat loop" property ADR-0059 advertised is now qualified: there
  *is* a heartbeat, but it is a single bounded goroutine doing a `Ping` — not the
  lease-renewal / timer-reassignment machinery of a full distributed scheduler,
  which remains deliberately un-built (see ADR-0059's closing note on
  claim-on-rehydrate).
- The interval is a tunable: shorter narrows the window at the cost of more pings;
  longer is cheaper but widens the window. The 5s default is a balance; consumers
  who care set `WithElectorHeartbeatInterval`.
- Engine/model/runtime diff is **ZERO**: the change lives entirely in the scheduling
  adapter (`internal/scheduling/gocron`) and the `scheduling` façade option. Without
  the Elector, behaviour is unchanged.
- **Re-entrant lock release on `Close`.** Because a transient heartbeat ping failure can
  falsely step the elector down while the advisory lock is still held, the *next*
  `IsLeader` re-runs `pg_try_advisory_lock` on the **same** dedicated connection, stacking
  the session-level re-entrant counter. `Close` therefore issues
  `SELECT pg_advisory_unlock_all()` (not a single targeted `pg_advisory_unlock`) before
  `conn.Release()`: a single unlock would only decrement the counter and leave the lock
  held, and `Release()` returns the connection to the pool **without** dropping the session
  or resetting its locks, so a re-entrant lock would otherwise linger on a pooled backend.
  `unlock_all` clears the whole stack regardless of depth; a fresh session then acquires
  the key immediately after `Close`.

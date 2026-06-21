# 22. Optional LISTEN/NOTIFY relay trigger over the poll fallback

- Status: Accepted
- Date: 2026-06-22

## Context

The transactional-outbox `Relay` (`internal/persistence/postgres/relay.go`) drains
`wrkflw_outbox` on a fixed poll interval (`WithPollInterval`, default 1s). Polling is
simple and robust but trades latency for idleness: a committed event waits up to one
interval before relay, and an idle system still queries every tick. Both the
Persistence (#3) and Eventing (#4) sub-projects flagged a Postgres `LISTEN`/`NOTIFY`
push as the latency optimization to layer on the poll fallback.

`NOTIFY` is transactional in Postgres — a notification is delivered only when the
issuing transaction commits — which fits the outbox write exactly: the same tx that
inserts outbox rows can announce them, and a rolled-back step announces nothing. The
constraint is that polling must remain as a fallback: notifications can be missed
across listener reconnects/restarts, and in a multi-worker relay only the worker
holding the listen connection receives pushes.

## Decision

Add an opt-in `LISTEN`/`NOTIFY` wakeup that accelerates the existing poll loop without
replacing it.

- **Write side** — `Store.Commit`/`Create` emit `NOTIFY wrkflw_outbox` **inside the
  same transaction** when (and only when) the step inserted outbox rows, so the
  hottest write path (steps with no events, the common case) is untouched. Opt-in via
  `persistence.OpenPostgres(..., WithOutboxNotify())`. The notification carries no
  payload (bare channel wakeup); the relay still claims via `FOR UPDATE SKIP LOCKED`,
  so the NOTIFY payload-size limit is irrelevant.
- **Read side** — `Relay.Run` (opt-in `WithListenNotify()`) acquires a dedicated pool
  connection, runs `LISTEN wrkflw_outbox`, and in a goroutine loops
  `conn.WaitForNotification(ctx)`, feeding a wakeup channel into the existing
  `select`. The poll `ticker` **stays as the fallback** case, unchanged. On any
  wakeup (tick or notify) the relay drains until `DrainOnce` returns 0, coalescing a
  burst of notifications into one drain sweep. The per-row poison isolation / DLQ
  (ADR-0017) is unchanged. On a dropped listen connection the relay logs,
  re-acquires, and re-`LISTEN`s; the poll covers the gap.

The two sides are **independently safe**: NOTIFY without a listener is ignored; a
listener without NOTIFY falls back to polling; enabling both yields the latency win.
Neither weakens at-least-once delivery or the DLQ semantics.

## Consequences

**Easier:** committed events relay with sub-interval latency and an idle system stops
spinning the poll query needlessly, while the poll fallback guarantees liveness under
listener loss, restart, and multi-worker fan-out. The change is additive and fully
opt-in; a consumer who enables neither option sees today's pure-polling behavior.
`pgx`'s native `LISTEN`/`WaitForNotification` (chosen in ADR-0006 over `database/sql`)
makes the listener a few lines with no new dependency.

**Harder / trade-offs:** the listener pins one connection per relay worker that opts
in. In a multi-worker deployment only the connection-holding worker(s) get pushes; the
rest rely on the poll — strict per-worker push fairness is out of scope. A flapping
listen connection adds reconnect churn (bounded by the poll as a safety net). Because
NOTIFY is best-effort relative to the durable outbox row, the system's correctness
never depends on it: a lost notification only delays an event to the next poll tick,
never drops it.

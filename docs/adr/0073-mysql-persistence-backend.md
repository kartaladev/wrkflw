# 0073. MySQL 8.0+ as an alternative SQL persistence backend

Status: **Accepted — 2026-06-28.**
Design doc: `docs/specs/2026-06-28-mysql-persistence-backend-design.md`.
Plan: `docs/plans/mysql-persistence-backend.md`.
Relates to: ADR-0004 (no `pkg/` prefix), ADR-0017/0025 (transactional outbox + atomic write),
ADR-0020 (advisory-lock ownership), ADR-0027 (timer rehydration), ADR-0031 (call-link lease),
ADR-0059/0061 (elector + heartbeat), ADR-0072 (Option A leadership re-arm).

## Context

The tech stack locks "PostgreSQL 17, SQL-based" as the only database; changing it requires an ADR
(this one). A consumer has asked to run the engine on **MySQL**. The engine core already depends only
on `runtime.*` persistence ports (`Store`, `TimerStore`, `CallLinkStore`, `ChainLinkStore`,
`InstanceLister`, `Ownership`, `DefinitionRegistry`, the relay `Publisher`), so a second backend is an
adapter swap, not an engine change. The existing implementation in `internal/persistence/postgres/`
is, however, deeply Postgres-specific: pgx driver and pgx-typed `DBTX`, `LISTEN/NOTIFY` relay wake,
`pg_*advisory_lock` ownership/elector, `RETURNING`, `ON CONFLICT`, `JSONB`, `TIMESTAMPTZ`,
`BIGSERIAL`, and SQLSTATE-based conflict classification.

Two structural options were weighed: a **parallel `mysql` tree** (duplicate orchestration, isolate
risk) versus a **dialect-abstracted core** (refactor the working Postgres code behind a dialect/driver
seam). The pervasiveness of the dialect differences — and especially the pgx-vs-`database/sql` type
split and the absence of `LISTEN/NOTIFY`/`RETURNING` in MySQL — makes a shared abstraction leaky and
puts battle-tested Postgres paths at regression risk.

This decision is also consistent with ADR-0072: choosing the timer-elector Option A (advisory-lock
based) over a `SKIP LOCKED` claim scheduler kept the cross-dialect story cheap (`pg_try_advisory_lock`
→ `GET_LOCK`).

## Decision

Adopt **MySQL 8.0+ as an alternative SQL backend**, implemented as a **parallel, non-exported
package** `internal/persistence/mysql/` that mirrors `internal/persistence/postgres/` and satisfies
the same `runtime.*` ports. PostgreSQL 17 remains the **primary/default** backend and its code is **not
modified**.

- **Driver:** stdlib `database/sql` + `github.com/go-sql-driver/mysql` (new dependency). DSN sets
  `parseTime=true&loc=UTC`.
- **Floor:** MySQL **8.0+** (requires `FOR UPDATE SKIP LOCKED`, native `JSON`, CTEs,
  `RELEASE_ALL_LOCKS()`). MySQL 5.7 is not supported (EOL, lacks `SKIP LOCKED`).
- **Dialect mapping:** `?` placeholders; `ON DUPLICATE KEY UPDATE`/`INSERT IGNORE` for upsert;
  `SELECT … FOR UPDATE SKIP LOCKED` + follow-up `UPDATE` in place of `RETURNING`; `JSON`/`DATETIME(6)`/
  `BIGINT AUTO_INCREMENT`; `GET_LOCK`/`RELEASE_ALL_LOCKS` for advisory locking (instance keys hashed to
  the 64-char limit); CAS via `RowsAffected()==0` with deadlock `1213`/lock-wait `1205` wrapped to
  `runtime.ErrConcurrentUpdate`.
- **Relay & notifier are poll-only** on MySQL (no `LISTEN/NOTIFY`); timer/event latency is bounded by
  the poll interval.
- **Public surface:** parallel `*MySQL*` constructors on the root `persistence` package (taking
  `*sql.DB`, returning the same interface types) and `scheduling.WithMySQLTimerElector`. The tech-stack
  table in `CLAUDE.md`/`REQUIREMENTS.md` is updated to read "PostgreSQL 17 (primary) or MySQL 8.0+".
- **Delivery:** 5 TDD phases (Foundation → Relay → Correlation → Definitions/ops → Scheduling), each
  branched and merged green, tested against a MySQL 8.0 testcontainer via a new
  `database.RunTestMySQL` helper.

## Consequences

- The engine runs on either Postgres or MySQL with identical semantics; the choice is a consumer
  wiring decision. No `engine/`, `model/`, or `runtime/` code changes.
- Two parallel persistence trees must be kept behaviourally in sync; the shared port-level test
  expectations are the guardrail. A future shared conformance suite is noted but not built here.
- MySQL deployments accept poll-interval latency for outbox relay and call-notify (no push wake), and a
  64-char advisory-lock key space (mitigated by hashing).
- New third-party dependency `github.com/go-sql-driver/mysql` enters `go.mod`.
- Postgres remains the reference for behaviour and the default in examples; MySQL is additive.

# 0080. UTC time discipline for database connections

Status: **Accepted — 2026-07-02.**
Plan: `docs/plans/2026-07-02-database-toolkit-transactions-time-correctness.md`.
Relates to: ADR-0073 (MySQL persistence backend), ADR-0079 (database transaction toolkit).

## Context

`wrkflw` stores all timestamps — token fire times, instance created/updated, deadline
expirations — in UTC. On **Postgres**, `TIMESTAMPTZ` columns store instants in UTC
internally and the driver (`pgx/v5`) returns `time.Time` values in the session's
configured time zone. If the session time zone is not `UTC`, the `time.Time.Location()`
field may name a non-UTC zone even though the instant is correct.

On **MySQL**, `DATETIME(6)` columns have no embedded zone. `DEFAULT CURRENT_TIMESTAMP(6)`
uses the MySQL server's session `time_zone`. If the session or server zone is not UTC,
inserted timestamps silently record a local time while the application treats them as UTC
instants. `go-sql-driver/mysql` further requires `parseTime=true` to return `time.Time`
rather than byte slices, and its `loc` parameter controls the zone used when
*constructing* `time.Time` from the raw bytes. `FormatDSN` with a UTC location omits the
`loc` key from the DSN string entirely (UTC is the driver default), making the guarantee
invisible to a reader of the DSN but relying on an undocumented default.

Before this work, time-zone configuration was ad-hoc: each call site constructed its own
DSN, relying on database-server defaults. A misconfigured `time_zone` on the MySQL server
could cause timer fire times to silently drift relative to the engine's expectations,
leading to timers firing at the wrong instant or being missed. The problem only appeared
in environments where the MySQL server or the application host ran in a non-UTC zone —
the kind of failure that is invisible in development and surfaces in production.

## Decision

We adopt a three-layer UTC discipline applied at `Open` time for each driver.

### 1. Connection-level pinning

**Postgres (`OpenPostgres`)** accepts an already-constructed `*pgxpool.Pool` from the
consumer. It does not rewrite the DSN or issue any server-side `SET` statement. Its
sole time-correctness action at open is to run `database.ProbeUTC` (see §2) as a
fail-fast check. Postgres `TIMESTAMPTZ` columns preserve the instant independently of
session time zone; the probe catches any driver misconfiguration, and `.UTC()`
normalization on the read path (§3) corrects the `time.Time.Location()` field.

**MySQL (`OpenMySQL`)** likewise accepts an already-constructed `*sql.DB` from the
consumer and runs `database.ProbeUTC` as a fail-fast check. The UTC guarantees for MySQL
connections live entirely in **`persistence.MySQLDSN(base string) (string, error)`**,
which the consumer must use when building the DSN. `MySQLDSN` unconditionally sets:

- `parseTime=true` — `go-sql-driver/mysql` returns `time.Time` values instead of byte
  slices.
- `cfg.Loc = time.UTC` — the driver constructs `time.Time` values in UTC when parsing
  `DATETIME(6)` byte strings. Note: `FormatDSN` omits the `loc` key from the serialized
  DSN because UTC is the driver's default, but the guarantee is enforced in-process
  via the config struct.
- `time_zone='+00:00'` as a DSN connection parameter — `go-sql-driver/mysql` executes
  `SET time_zone = '+00:00'` as a session statement on every new connection, pinning the
  server-side session zone for `DEFAULT CURRENT_TIMESTAMP(6)` and `NOW(6)` to UTC
  regardless of the server's global `time_zone` setting.

`OpenMySQL` does **not** itself issue any `SET time_zone` statement; all MySQL UTC
pinning is the responsibility of the DSN produced by `MySQLDSN`.

### 2. Fail-fast ProbeUTC at Open

`database.ProbeUTC(ctx, querier, dialect)` is called by both `OpenPostgres` and
`OpenMySQL` immediately after the connection pool is received. It reads a known literal
UTC instant from the database using a dialect-appropriate SQL expression and asserts:

```
stored.Equal(probeTime)   // instant equality, not location equality
```

We compare instants (`.Equal`) rather than zone names (`.Location().String() == "UTC"`)
because:

- `pgx/v5` may legitimately return a `TIMESTAMPTZ` in `time.Local` even when the instant
  is correct (the driver maps zones by offset, not by IANA name, and the result varies by
  platform).
- A zone-name check would produce false failures on Postgres while missing a genuine
  MySQL misconfiguration where the instant itself is wrong.

If the instant does not survive the round-trip, `ProbeUTC` returns an error and `Open`
fails, preventing a misconfigured instance from entering the main execution path.

### 3. Normalize-on-read at scan sites

Scan sites that read a `time.Time` column call `.UTC()` on the scanned value before
storing it. This is the defence-in-depth layer: even if a future driver version or
connection-pool configuration change shifts the returned `time.Time.Location()`, the
value written into the engine's data structures is a UTC-located instant.

In Phase 1, normalization is applied to the **timer `fire_at` read path** in both
`internal/persistence/postgres/timerstore.go` (Postgres `ListArmed` and `Stats`) and
`internal/persistence/mysql/timerstore.go` (MySQL `ListArmed` and `Stats`), as these
are the most critical sites (a wrong fire instant causes timers to fire at the wrong
moment or be missed entirely). Other scanned timestamp columns — instance `started_at`
/ `ended_at` in the lister, dead-letter `created_at` in the relay, chain-link
`created_at` — are **not yet normalized**; applying `.UTC()` uniformly across all
remaining scan sites is a documented Phase-1.x follow-up.

## Consequences

- **Consumers must build MySQL DSNs via `MySQLDSN`**. A raw DSN string without
  `parseTime=true` and `loc=UTC` bypasses the toolkit's guarantee. Consumers who
  construct their own DSN and pass it directly to `sql.Open` will not have the session
  `time_zone` pinned and will not receive a `time.Time` from scans. The `MySQLDSN`
  helper is the supported entry point.
- **Misconfiguration fails at startup.** If a MySQL DSN is constructed without
  `MySQLDSN` (missing `parseTime=true` / `loc=UTC` / `time_zone='+00:00'`), the
  `ProbeUTC` call inside `OpenMySQL` will detect that the round-trip instant drifted and
  return an error, preventing the misconfigured instance from entering the main execution
  path.
- **Instant equality probe is intentionally strict.** A one-second tolerance was
  considered and rejected: if the round-trip instant differs by any amount, the
  configuration is wrong, not the clock. The probe uses a fixed past instant, not
  `time.Now()`, to avoid any clock-skew ambiguity.
- **MySQL `time_zone` DSN parameter requires session-variable permission.** The
  `time_zone='+00:00'` connection parameter that `MySQLDSN` injects causes
  `go-sql-driver/mysql` to issue `SET time_zone = '+00:00'` on every new connection. In
  environments where the application database user lacks the right to set session
  variables, these connections will fail. In practice this is not restricted on standard
  managed MySQL (RDS, Cloud SQL), but operators on hardened environments should ensure
  the application user has session-variable permission.
- **Postgres normalize-on-read is defence-in-depth.** Postgres `TIMESTAMPTZ` already
  preserves the instant regardless of session time zone; the `.UTC()` call at scan sites
  corrects the `time.Time.Location()` field and documents the intent, guarding against
  future driver or configuration changes that might return a non-UTC-located `time.Time`.
- **Phase-1.x follow-up: complete normalize-on-read coverage.** Currently only the timer
  `fire_at` read path normalizes to UTC. Remaining timestamp columns (instance
  `started_at`/`ended_at`, dead-letter `created_at`, chain-link `created_at`) are a
  documented follow-up; they are not on the engine's hot execution path.
- **MySQL `DATETIME(6)` vs Postgres `TIMESTAMPTZ` difference is managed, not
  eliminated.** The two column types carry different semantics (zoneless vs. zone-aware).
  The three-layer discipline bridges the gap at the driver boundary; the application code
  above the seam sees only UTC `time.Time` instants.

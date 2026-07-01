# 0080. UTC time discipline for database connections

Status: **Accepted — 2026-07-02.**
Plan: `docs/plans/2026-07-02-database-transaction-toolkit.md`.
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

**Postgres (`OpenPostgres`)** passes `TimeZone=UTC` in the connection string. This pins
the session time zone server-side for all connections from the pool, ensuring that
`pgx/v5` returns `time.Time` values in UTC.

**MySQL (`OpenMySQL` via `persistence.MySQLDSN`)** requires consumers to build their DSN
through `MySQLDSN(host, port, user, password, dbname, extraParams)`. The helper
unconditionally sets:

- `parseTime=true` — `go-sql-driver/mysql` returns `time.Time` values instead of byte
  slices.
- `cfg.Loc = time.UTC` — the driver constructs `time.Time` values in UTC. Note: this
  sets the `loc` field on the driver config struct; `FormatDSN` omits it from the
  serialized DSN string because UTC is the driver default, but the guarantee is enforced
  in-process via the config object, not the string.
- After obtaining a connection, `OpenMySQL` issues `SET time_zone = '+00:00'` as a
  session statement, pinning the server-side session zone for `DEFAULT
  CURRENT_TIMESTAMP(6)` and `NOW(6)` to UTC regardless of the server's global
  `time_zone` setting.

### 2. Fail-fast ProbeUTC at Open

`database.ProbeUTC(ctx, querier)` is called by both `OpenPostgres` and `OpenMySQL`
immediately after the connection pool is created. It inserts a known UTC `time.Time`
instant, reads it back, and asserts:

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

All scan sites that read a `time.Time` column call `.UTC()` on the scanned value before
storing it. This is the defence-in-depth layer: even if a future driver version or
connection-pool configuration change shifts the returned zone, the value written into the
engine's data structures is always a UTC instant. Timer `fire_at` columns are the most
critical site; all others follow the same pattern.

## Consequences

- **Consumers must build MySQL DSNs via `MySQLDSN`**. A raw DSN string without
  `parseTime=true` and `loc=UTC` bypasses the toolkit's guarantee. Consumers who
  construct their own DSN and pass it directly to `sql.Open` will not have the session
  `time_zone` pinned and will not receive a `time.Time` from scans. The `MySQLDSN`
  helper is the supported entry point.
- **Misconfiguration fails at startup.** If the MySQL server's global `time_zone` cannot
  be overridden via `SET time_zone` (e.g., due to a permissions restriction), or if the
  Postgres session zone is misconfigured, `ProbeUTC` surfaces the failure immediately at
  `Open` rather than silently drifting at runtime.
- **Instant equality probe is intentionally strict.** A one-second tolerance was
  considered and rejected: if the round-trip instant differs by any amount, the session
  zone is wrong, not the clock. The probe uses a fixed past instant, not `time.Now()`,
  to avoid any clock-skew ambiguity.
- **The `SET time_zone` session pin requires execute permission.** Environments where the
  application database user lacks `SET` session-variable rights will fail `OpenMySQL`. In
  practice this is not a restriction on standard managed MySQL (RDS, Cloud SQL), but
  operators on hardened environments should ensure the application user can set session
  variables.
- **Postgres normalize-on-read is redundant but harmless.** `TimeZone=UTC` on the
  connection already returns UTC `time.Time` values; the `.UTC()` call at scan sites is a
  no-op in the normal case. It guards against future configuration drift and documents the
  intent at the scan site.
- **MySQL `DATETIME(6)` vs Postgres `TIMESTAMPTZ` difference is managed, not
  eliminated.** The two column types carry different semantics (zoneless vs. zone-aware).
  The three-layer discipline bridges the gap at the driver boundary; the application code
  above the seam sees only UTC `time.Time` instants.

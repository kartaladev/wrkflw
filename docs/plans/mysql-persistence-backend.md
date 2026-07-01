# MySQL Persistence Backend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add MySQL 8.0+ as an alternative SQL persistence backend behind the existing `runtime.*` ports, leaving the PostgreSQL implementation untouched.

**Architecture:** A new non-exported `internal/persistence/mysql/` package mirrors `internal/persistence/postgres/`, using `database/sql` + `go-sql-driver/mysql`, translating Postgres dialect to MySQL 8.0. Public access is via parallel `*MySQL*` constructors on the root `persistence` package returning the same interface types.

**Tech Stack:** Go 1.25, MySQL 8.0+, `database/sql`, `github.com/go-sql-driver/mysql`, goose migrations, testcontainers-go, uber-go/mock, testify.

## Global Constraints

- **Mirror, don't refactor:** `internal/persistence/postgres/` is NOT modified. Each MySQL file mirrors its Postgres analog's behaviour.
- **MySQL 8.0+ only** (requires `FOR UPDATE SKIP LOCKED`, `JSON`, CTEs, `RELEASE_ALL_LOCKS()`).
- **DSN:** `parseTime=true&loc=UTC` always; timestamps stored/returned as UTC.
- **Ports are the contract:** every MySQL type satisfies the same `runtime.*` / `persistence.*` interface as its Postgres analog (compile-time `var _ Iface = (*T)(nil)`).
- **TDD strict** (CLAUDE.md): failing test + observed red BEFORE implementation, per symbol. Black-box tests (`mysql_test` / `persistence_test` / `scheduling_test`). Use `database.RunTestMySQL(t)` for DB tests (use-testcontainers skill). `<file>.go` ↔ `<file>_test.go` pairing.
- **Error sentinels** use the `workflow-` prefix (e.g. `workflow-persistence-mysql: ...`).
- **Verification per package:** `go test -race` green, `goleak` where goroutines exist, ≥85% line coverage, `golangci-lint run` clean.
- **Naming map (use verbatim):** dialect translations per the design doc table — `?` placeholders, `ON DUPLICATE KEY UPDATE`/`INSERT IGNORE`, `SELECT … FOR UPDATE SKIP LOCKED` + `UPDATE` for `RETURNING`, `JSON`/`DATETIME(6)`/`BIGINT AUTO_INCREMENT`, `GET_LOCK`/`RELEASE_ALL_LOCKS`, CAS via `RowsAffected()==0`, retry deadlock `1213`/lock-wait `1205`, dup `1062`.

---

## Phase 1 — Foundation (branch `feat/mysql-phase1-foundation`)

### Task 1.1: Dependency + tech-stack docs

**Files:**
- Modify: `go.mod`, `go.sum`
- Modify: `CLAUDE.md` (Tech Stack table Database row)

**Interfaces:** Produces: the `github.com/go-sql-driver/mysql` import availability.

- [ ] **Step 1:** `go get github.com/go-sql-driver/mysql@latest`
- [ ] **Step 2:** Update the Database row of the Tech Stack table to: `**PostgreSQL 17** (primary) or **MySQL 8.0+**, SQL-based` with a note pointing to ADR-0073.
- [ ] **Step 3:** `go build ./...` — expect success.
- [ ] **Step 4:** Commit `chore(mysql): add go-sql-driver/mysql dep; tech-stack table → Postgres|MySQL (ADR-0073)`.

### Task 1.2: `RunTestMySQL` testcontainers helper

**Files:**
- Find the file defining `RunTestDatabase` (grep `func RunTestDatabase`); add `RunTestMySQL` beside it (same package, likely `internal/database/testutils.go`).
- Test: `<that package>_test.go` — a smoke test that the helper returns a pingable `*sql.DB`.

**Interfaces:**
- Produces: `func RunTestMySQL(t *testing.T, opts ...TestOption) *sql.DB` — boots a MySQL 8.0 testcontainer, builds a DSN with `parseTime=true&loc=UTC`, opens `*sql.DB`, runs `MigrateMySQL`, registers `t.Cleanup` to close + terminate. (Reuse the existing `TestOption` type if present; otherwise no opts param.)

- [ ] **Step 1:** Read `RunTestDatabase` to copy its structure (container lifecycle, snapshot/reuse, cleanup, ryuk handling).
- [ ] **Step 2:** Write failing test `TestRunTestMySQL_PingsAndMigrates` asserting `db.PingContext(ctx)` succeeds and a known migrated table (e.g. `wrkflw_instances`) exists.
- [ ] **Step 3:** Run it → red (helper undefined). NOTE: `MigrateMySQL` does not exist yet — for this task stub migration as a no-op or gate the table assertion behind Task 1.3. Prefer: implement helper now WITHOUT migrate, assert only Ping; add the table assertion in Task 1.3.
- [ ] **Step 4:** Implement `RunTestMySQL` using `testcontainers-go` MySQL module (`mysql.Run(ctx, "mysql:8.0", mysql.WithDatabase, mysql.WithUsername, mysql.WithPassword)`), `cont.ConnectionString(ctx, "parseTime=true", "loc=UTC")`, `sql.Open("mysql", dsn)`, `t.Cleanup`.
- [ ] **Step 5:** Run → green. Commit `test(mysql): RunTestMySQL testcontainers helper`.

### Task 1.3: MySQL migrations

**Files:**
- Create: `internal/persistence/mysql/migrations/0001_init.sql` … mirroring every Postgres migration's logical schema (read `internal/persistence/postgres/migrations/*.sql` first). Consolidation into fewer files is fine as long as the final schema matches.
- Create: `internal/persistence/mysql/migrate.go` — embedded FS + `Migrate(ctx, *sql.DB) error` using goose `mysql` dialect.
- Test: `internal/persistence/mysql/migrate_test.go`.

**Interfaces:**
- Produces: `mysql.Migrate(ctx context.Context, db *sql.DB) error`; tables `wrkflw_instances`, `wrkflw_journal`, `wrkflw_outbox`, `wrkflw_definitions`, `wrkflw_processed_message`, `wrkflw_call_links`, `wrkflw_timers`, `wrkflw_chain_links`.

DDL dialect rules: `JSONB→JSON`, `TIMESTAMPTZ→DATETIME(6)`, `BIGSERIAL→BIGINT AUTO_INCREMENT`, `text→VARCHAR(n)` for indexed columns (MySQL can't index unbounded TEXT without prefix length — use `VARCHAR(255)` for ids/keys), keep composite PKs/unique keys identical, recreate every index. Goose annotations: `-- +goose Up` / `-- +goose Down`.

- [ ] **Step 1:** Write failing `TestMigrate_CreatesAllTables` — after `Migrate`, query `information_schema.tables` for each expected table; assert all present.
- [ ] **Step 2:** Run → red.
- [ ] **Step 3:** Author the DDL + `migrate.go` (mirror `postgres/migrate.go`, swap goose dialect to `mysql`, `goose.SetBaseFS(embedFS)`).
- [ ] **Step 4:** Run → green. Wire `RunTestMySQL` to call `Migrate` (complete Task 1.2's deferred table assertion).
- [ ] **Step 5:** Commit `feat(mysql): schema migrations (goose, MySQL 8.0 dialect)`.

### Task 1.4: `DBTX` driver seam

**Files:**
- Create: `internal/persistence/mysql/dbtx.go`
- Test: covered indirectly by Store tests (no standalone test needed — pure interface).

**Interfaces:**
- Produces: `type DBTX interface { ExecContext(ctx, query string, args ...any) (sql.Result, error); QueryContext(...) (*sql.Rows, error); QueryRowContext(...) *sql.Row }` satisfied by `*sql.DB`, `*sql.Tx`, `*sql.Conn`. Plus a helper `txWith(ctx, db *sql.DB, fn func(*sql.Tx) error) error` that begins, defers rollback, runs `fn`, commits.

- [ ] **Step 1:** No new behaviour → no standalone test (pure type alias + tx helper exercised by Task 1.5). Write the file.
- [ ] **Step 2:** `go build ./internal/persistence/mysql/...` → success. Commit folded into Task 1.5.

### Task 1.5: `Store` (Create/Load/Commit/Entries) with CAS + error mapping

**Files:**
- Create: `internal/persistence/mysql/errors.go` (MySQL error classification), `internal/persistence/mysql/store.go`, `internal/persistence/mysql/trigger_codec.go` (or reuse postgres's by extracting — but per "mirror, don't refactor" copy a small codec here; the codec is plain `encoding/json`, no dialect).
- Test: `internal/persistence/mysql/store_test.go`, `errors_test.go`.

**Interfaces:**
- Consumes: `runtime.AppliedStep`, `runtime.Token`, `runtime.InstanceState`, `engine.Trigger`, `runtime.ErrConcurrentUpdate`, `DBTX`.
- Produces: `func NewStore(db *sql.DB, opts ...StoreOption) *Store` satisfying `runtime.Store` + `runtime.JournalReader`. `StoreOption` mirrors postgres (`WithHistoryCap`). `func isConcurrencyError(err error) bool` + `func isUniqueViolation(err error) bool` in errors.go using `*mysqldriver.MySQLError` `.Number` (1213, 1205 → concurrency; 1062 → unique).

- [ ] **Step 1 (errors, red):** `TestIsConcurrencyError` table: `1213`→true, `1205`→true, `1062`→false, nil→false (use `assert` closure form per table-test skill). Run → red.
- [ ] **Step 2 (errors, green):** Implement `errors.go` with `errors.As(err, &me *mysqldriver.MySQLError)` switch. Run → green.
- [ ] **Step 3 (Commit CAS, red):** `TestStore_Commit_ConflictReturnsErrConcurrentUpdate` — Create an instance, Load token v, then two Commits with the same expected token; second must return `runtime.ErrConcurrentUpdate`. Run → red.
- [ ] **Step 4 (Store, green):** Implement `store.go` mirroring `postgres/store.go`: `Create` inserts instance+journal+outbox in a tx; `Load` selects snapshot+version → `(InstanceState, Token)`; `Commit` runs `UPDATE wrkflw_instances SET version=version+1, snapshot=?, updated_at=? WHERE instance_id=? AND version=?`, `RowsAffected()==0` → `ErrConcurrentUpdate`, then writes journal+outbox+timer rows (timer upsert `INSERT … ON DUPLICATE KEY UPDATE`); `Entries` selects journal triggers and decodes via the codec. Wrap deadlock/lock-wait via `isConcurrencyError`. Run → green.
- [ ] **Step 5:** Add `TestStore_CreateLoadEntries_RoundTrip` (mirror postgres). Run → green.
- [ ] **Step 6:** Commit `feat(mysql): Store with optimistic CAS, error classification, dbtx seam`.

### Task 1.6: `TimerStore`

**Files:** Create `internal/persistence/mysql/timerstore.go`; Test `timerstore_test.go`.

**Interfaces:** Produces `func NewTimerStore(db *sql.DB) *TimerStore` satisfying `runtime.TimerStore` (`ListArmed(ctx) ([]runtime.ArmedTimer, error)`).

- [ ] **Step 1:** `TestTimerStore_ListArmed` — arm timers via a Store Commit (or direct insert), assert `ListArmed` returns them. Run → red.
- [ ] **Step 2:** Implement `SELECT instance_id, timer_id, def_id, def_version, fire_at FROM wrkflw_timers`. Run → green.
- [ ] **Step 3:** Commit `feat(mysql): TimerStore.ListArmed`.

### Task 1.7: Public facade — `OpenMySQL` / `MigrateMySQL` / `NewMySQLTimerStore`

**Files:** Modify `persistence/persistence.go`; Test `persistence/facade_mysql_test.go`.

**Interfaces:**
- Produces: `func OpenMySQL(_ context.Context, db *sql.DB, opts ...Option) (Store, error)`; `func MigrateMySQL(ctx context.Context, db *sql.DB) error`; `func NewMySQLTimerStore(db *sql.DB) runtime.TimerStore`. Return the SAME `Store`/interface types the Postgres constructors return; map facade `Option`s to `mysql.StoreOption`s exactly as `OpenPostgres` maps to `postgres.StoreOption`.

- [ ] **Step 1:** `TestOpenMySQL_RoundTrip` via the facade (Create→Load through `Store`). Run → red.
- [ ] **Step 2:** Implement the three constructors delegating to `internal/persistence/mysql`. Run → green.
- [ ] **Step 3:** `go test ./persistence/... ./internal/persistence/mysql/...`, lint, coverage ≥85%. Commit `feat(persistence): MySQL facade — OpenMySQL/MigrateMySQL/NewMySQLTimerStore`.

**Phase 1 gate:** all above green, `go test ./...` no regressions, then merge to main + HANDOVER note.

---

## Phase 2 — Relay (branch `feat/mysql-phase2-relay`)

### Task 2.1: `Relay` (poll-based outbox drain + DLQ + redrive)

**Files:** Create `internal/persistence/mysql/relay.go`, `relay_backoff.go` (copy the pure backoff fn); Test `relay_test.go`.

**Interfaces:**
- Consumes: `runtime.Publisher`, `runtime.OutboxEvent`, `runtime.DeadLetter`.
- Produces: `func NewRelay(db *sql.DB, pub runtime.Publisher, opts ...RelayOption) *Relay` with `Run(ctx) error`, `DrainOnce(ctx) (int, error)`, `ListDeadLettered(ctx, limit) ([]runtime.DeadLetter, error)`, `Redrive(ctx, ids ...int64) (int, error)`. `RelayOption` mirrors postgres (`WithPollInterval`, `WithBatchSize`, `WithMaxDeliveryAttempts`, `WithRelayBackoff`) — but NO `WithListenNotify` (poll-only).

Claim SQL (in a tx): `SELECT id, payload, ... FROM wrkflw_outbox WHERE status='pending' AND next_attempt_at<=? ORDER BY id FOR UPDATE SKIP LOCKED LIMIT ?`; publish each; on success `UPDATE ... SET status='published', published_at=?`; on failure increment `retry_count`, set `next_attempt_at` via backoff, and at `max_delivery_attempts` set `status='dead'`. `Redrive`: `UPDATE ... SET status='pending', retry_count=0, next_attempt_at=? WHERE id IN (?,...)`.

- [ ] **Step 1:** `TestRelay_DrainOnce_PublishesAndMarks` — seed outbox via Store, fake Publisher, assert claimed+published. Run → red.
- [ ] **Step 2:** Implement `DrainOnce` + claim/mark. Run → green.
- [ ] **Step 3:** `TestRelay_Retry_Backoff_DeadLetter` — Publisher returns error; assert retry_count climbs, then dead at cap. Run → red→green.
- [ ] **Step 4:** `TestRelay_ListDeadLettered_And_Redrive`. Run → red→green.
- [ ] **Step 5:** `TestRelay_Run_DrainsUntilCancelled` with `goleak` (Run loop exits on ctx cancel). Run → red→green.
- [ ] **Step 6:** Commit `feat(mysql): poll-based outbox relay with DLQ + redrive`.

### Task 2.2: `Deduper`

**Files:** Create `internal/persistence/mysql/dedup.go`; Test `dedup_test.go`.

**Interfaces:** Produces `func NewDeduper(db *sql.DB) *Deduper` matching the postgres `Deduper` public method set (read it). Use `INSERT IGNORE INTO wrkflw_processed_message ...` → `RowsAffected()` tells first-vs-duplicate.

- [ ] **Step 1:** `TestDeduper_FirstSeenThenDuplicate`. Run → red.
- [ ] **Step 2:** Implement. Run → green. Commit `feat(mysql): Deduper (INSERT IGNORE)`.

### Task 2.3: Facade `NewMySQLRelay` / `NewMySQLDeduper`

**Files:** Modify `persistence/persistence.go`; Test `persistence/facade_mysql_test.go`.

**Interfaces:** Produces `func NewMySQLRelay(db *sql.DB, pub runtime.Publisher, opts ...RelayOption) Relay`; `func NewMySQLDeduper(db *sql.DB) Deduper`. Same return interface types as postgres facade.

- [ ] **Step 1:** `TestNewMySQLRelay_DrainsViaFacade`. Run → red→green.
- [ ] **Step 2:** lint+coverage. Commit `feat(persistence): MySQL relay + deduper facade`.

**Phase 2 gate:** green + `go test ./...` + merge + HANDOVER.

---

## Phase 3 — Correlation (branch `feat/mysql-phase3-correlation`)

### Task 3.1: `CallLinkStore` (plain + lease)

**Files:** Create `internal/persistence/mysql/call_links.go`; Test `call_links_test.go`, `call_links_lease_test.go`.

**Interfaces:** Produces `func NewCallLinkStore(db *sql.DB, opts ...CallLinkOption) *CallLinkStore` satisfying `runtime.CallLinkStore` (`ClaimPending`, `MarkNotified`, `LookupChild`, `ListRunningChildren`). `CallLinkOption` mirrors postgres (`WithCallLinkLease(owner, ttl)`).

Lease `ClaimPending` (no RETURNING): tx → `SELECT child_instance_id, ... WHERE notified_at IS NULL AND (claimed_at IS NULL OR claimed_at<=?) ORDER BY ... FOR UPDATE SKIP LOCKED LIMIT ?`, collect ids, `UPDATE ... SET claimed_at=?, claimed_by=? WHERE child_instance_id IN (?,...)`, return rows. Plain mode: same select without lease columns.

- [ ] **Step 1:** `TestCallLinkStore_ClaimPending_Plain` + `MarkNotified` + `LookupChild` + `ListRunningChildren` (table where shared). Run → red.
- [ ] **Step 2:** Implement plain mode. Run → green.
- [ ] **Step 3:** `TestCallLinkStore_Lease_HidesClaimedRows` (two stores, distinct owners, SKIP LOCKED exclusivity). Run → red→green.
- [ ] **Step 4:** Commit `feat(mysql): CallLinkStore (plain + SKIP LOCKED lease)`.

### Task 3.2: `Ownership` (GET_LOCK)

**Files:** Create `internal/persistence/mysql/ownership.go`; Test `ownership_test.go`.

**Interfaces:** Produces `func NewAdvisoryLockOwnership(ctx context.Context, db *sql.DB) (*AdvisoryLockOwnership, error)` satisfying `runtime.Ownership` (`Acquire(ctx, instanceID) (bool, error)`, `Release(ctx, instanceID) error`) + `Close() error`. Holds a dedicated `*sql.Conn`. Key = `hashKey(instanceID)` → SHA-256 hex truncated to ≤64 chars (GET_LOCK limit). `Acquire`: `SELECT GET_LOCK(?, 0)` → scan int, 1=acquired (sticky in-memory set, mutex-guarded). `Release`: `SELECT RELEASE_LOCK(?)`. `Close`: `SELECT RELEASE_ALL_LOCKS()` + conn.Close (goleak-clean, idempotent).

- [ ] **Step 1:** `TestOwnership_AcquireExclusiveThenRelease` — owner A acquires id; owner B (separate conn) fails to acquire same id; after A.Release, B succeeds. Run → red.
- [ ] **Step 2:** Implement. Run → green.
- [ ] **Step 3:** `TestOwnership_CloseIdempotentReleasesAll` + goleak. Run → red→green.
- [ ] **Step 4:** `TestHashKey_StableAndWithin64Chars`. Run → red→green.
- [ ] **Step 5:** Commit `feat(mysql): GET_LOCK-based Ownership`.

### Task 3.3: `ChainLinkStore`

**Files:** Create `internal/persistence/mysql/chainlink.go`; Test `chainlink_test.go`.

**Interfaces:** Produces `func NewChainLinkStore(db *sql.DB) *ChainLinkStore` satisfying `runtime.ChainLinkStore` (`Record`, `LookupBySuccessor`, `ListByPredecessor`).

- [ ] **Step 1:** `TestChainLinkStore_RecordLookupList`. Run → red.
- [ ] **Step 2:** Implement (plain SQL). Run → green. Commit `feat(mysql): ChainLinkStore`.

### Task 3.4: `Lister`

**Files:** Create `internal/persistence/mysql/lister.go`; Test `lister_test.go`.

**Interfaces:** Produces `func NewLister(db *sql.DB) *Lister` satisfying `runtime.InstanceLister` (`List(ctx, runtime.InstanceFilter) (runtime.InstancePage, error)`). Keyset cursor `(started_at DESC, instance_id DESC)`. Incident count: `JSON_LENGTH(JSON_EXTRACT(snapshot, '$.Incidents'))` guarded by `JSON_TYPE(JSON_EXTRACT(snapshot,'$.Incidents'))='ARRAY'` (else 0).

- [ ] **Step 1:** `TestLister_KeysetPagination` + `TestLister_IncidentCount` + filter cases (mirror postgres lister_test). Run → red.
- [ ] **Step 2:** Implement. Run → green. Commit `feat(mysql): Lister (keyset + JSON incident count)`.

### Task 3.5: Facades + CallNotifier verification

**Files:** Modify `persistence/persistence.go`; Test `persistence/facade_mysql_test.go`, and a `persistence/callnotifier_mysql_test.go` proving `NewCallNotifier` drives the MySQL call-link store.

**Interfaces:** Produces `NewMySQLCallLinkStore`, `NewMySQLAdvisoryLockOwnership` (returns `(runtime.Ownership, io.Closer, error)` like postgres), `NewMySQLChainLinkStore`, `NewMySQLLister`. `NewCallNotifier` is dialect-agnostic — just pass the MySQL call-link store; verify with a test.

- [ ] **Step 1:** facade round-trip tests per constructor. Run → red→green.
- [ ] **Step 2:** `TestCallNotifier_WithMySQLStore_Delivers`. Run → red→green.
- [ ] **Step 3:** lint+coverage. Commit `feat(persistence): MySQL correlation facade (call-links, ownership, chain-links, lister)`.

**Phase 3 gate:** green + `go test ./...` + merge + HANDOVER.

---

## Phase 4 — Definitions / ops (branch `feat/mysql-phase4-defs-ops`)

### Task 4.1: `DefinitionStore`

**Files:** Create `internal/persistence/mysql/definitions.go`; Test `definitions_test.go`.

**Interfaces:** Produces `func NewDefinitionStore(db *sql.DB) *DefinitionStore` satisfying `runtime.DefinitionRegistry` (`Lookup`) + the postgres `DefinitionStore` public methods (`PutDefinition`, `GetDefinition`). Upsert `INSERT … ON DUPLICATE KEY UPDATE` keyed `(def_id, version)`; definitions stored as `JSON`.

- [ ] **Step 1:** `TestDefinitionStore_PutLookupGet` + upsert-overwrite case. Run → red.
- [ ] **Step 2:** Implement. Run → green. Commit `feat(mysql): DefinitionStore`.

### Task 4.2: `Pruner`

**Files:** Create `internal/persistence/mysql/pruner.go`; Test `pruner_test.go`.

**Interfaces:** Produces `func NewPruner(db *sql.DB) *Pruner` with the postgres Pruner method set (`PruneOutbox`, `PruneCallLinks`, `PruneChainLinks`, `PruneTimers`, `PruneProcessedMessages`), each `DELETE … WHERE <ts_col> < ?` returning rows affected.

- [ ] **Step 1:** `TestPruner_DeletesOlderThanCutoff` table over each table. Run → red.
- [ ] **Step 2:** Implement. Run → green. Commit `feat(mysql): Pruner`.

### Task 4.3: MySQL health `PingCheck`

**Files:** Modify `persistence/health.go` (add a MySQL ctor) OR create alongside; Test `persistence/health_test.go`.

**Interfaces:** Produces `func NewMySQLPingCheck(db *sql.DB, opts ...PingOption) PingCheck` using `db.PingContext`. (If the existing `PingCheck` is pgx-typed, add a parallel MySQL one.)

- [ ] **Step 1:** `TestMySQLPingCheck_Healthy`. Run → red→green. Commit `feat(persistence): MySQL health PingCheck`.

### Task 4.4: Facades `NewMySQLDefinitionStore` / `NewMySQLPruner`

- [ ] **Step 1:** facade tests → red→green; lint+coverage. Commit `feat(persistence): MySQL definitions + pruner facade`.

**Phase 4 gate:** green + `go test ./...` + merge + HANDOVER.

---

## Phase 5 — Scheduling (branch `feat/mysql-phase5-scheduling`)

### Task 5.1: `MySQLElector` (GET_LOCK + heartbeat)

**Files:** Create `internal/scheduling/gocron/mysql_elector.go`; Test `mysql_elector_test.go`.

**Interfaces:** Produces `func NewMySQLElector(ctx context.Context, db *sql.DB, opts ...ElectorOption) (*MySQLElector, error)` satisfying `gocron.Elector` (`IsLeader(ctx) error`). REUSE the existing `ElectorOption` set incl. `WithOnLeadershipAcquired` (Option A) and `WithElectorKey`/`WithHeartbeatInterval`/`WithElectorClock` — extract the option type so both electors share it, OR define parallel options. Mirror `PostgresElector`: dedicated `*sql.Conn`, `SELECT GET_LOCK(?,0)` for leadership (sticky), heartbeat `conn.PingContext` step-down (ADR-0061), `Close` → `RELEASE_ALL_LOCKS()`. Fire `onAcquire` on transition (reuse `fireOnAcquireLocked` logic).

- [ ] **Step 1:** `TestMySQLElectorLeadership` (elect→contend→failover, mirror PostgresElector test). Run → red.
- [ ] **Step 2:** Implement. Run → green.
- [ ] **Step 3:** `TestMySQLElectorInvokesOnLeadershipAcquired` (Option A parity). Run → red→green.
- [ ] **Step 4:** `TestMySQLElectorCloseIdempotent` + goleak. Run → red→green.
- [ ] **Step 5:** Commit `feat(scheduling): MySQL leader elector (GET_LOCK) with Option-A hook`.

### Task 5.2: Facade `WithMySQLTimerElector`

**Files:** Modify `scheduling/scheduler.go`; Test `scheduling/mysql_elector_test.go`.

**Interfaces:** Produces `func WithMySQLTimerElector(db *sql.DB, opts ...ElectorOption) Option`, mirroring `WithTimerElector` but constructing a `MySQLElector`. Reuse the façade `ElectorOption` incl. `WithOnLeadershipAcquired`. Keep `ErrTimerLockElectorConflict` semantics.

- [ ] **Step 1:** `TestSchedulerWithMySQLTimerElector` (leader fires / follower skipped) + `TestSchedulerMySQLElectorOnLeadershipAcquired`. Run → red→green.
- [ ] **Step 2:** lint+coverage. Commit `feat(scheduling): WithMySQLTimerElector façade`.

### Task 5.3: `examples/` MySQL reference wiring

**Files:** Create `examples/mysql_wiring/main.go` (mirror `examples/production_wiring` but MySQL ctors; thin, illustrative — not a shipped binary). Build-only (no test).

- [ ] **Step 1:** Write wiring: `sql.Open("mysql", dsn)`, `persistence.MigrateMySQL`, `persistence.OpenMySQL`, `NewMySQLRelay`, call-links, ownership, timers, lister, chain, `scheduling.NewScheduler(scheduling.WithMySQLTimerElector(db, scheduling.WithOnLeadershipAcquired(func(ctx){ _ = runner.RehydrateTimers(ctx) })))`.
- [ ] **Step 2:** `go build ./examples/...` → success. Commit `docs(examples): MySQL reference wiring`.

**Phase 5 gate:** green + `go test ./...` + `golangci-lint run ./...` clean + merge + final HANDOVER + memory update.

---

## Self-review notes

- Spec coverage: every component in the design doc (Store, TimerStore, Relay, Deduper, CallLinkStore, Ownership, ChainLinkStore, Lister, DefinitionStore, Pruner, health, MySQLElector, facades, migrations, RunTestMySQL) maps to a task. ✓
- Type consistency: facade constructors return the same interface types as their Postgres analogs (`Store`, `runtime.*`); elector reuses `gocron.Elector` + shared `ElectorOption`. ✓
- Open implementation detail for executors: confirm whether `RunTestDatabase` takes a `TestOption` param and whether `PingCheck`/`Relay`/`Deduper`/`StoreOption`/`RelayOption`/`CallLinkOption` facade types are dialect-neutral (reusable) or Postgres-typed (need MySQL parallels). Resolve by reading the analog before each task — do NOT modify postgres types.

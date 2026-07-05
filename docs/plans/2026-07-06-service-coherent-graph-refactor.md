# Service Coherent-Graph Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refactor the module-root `service` package to a fail-fast coherent-graph constructor (`NewEngine`), segregated role interfaces, a self-serializing `ProcessInstance` return type, and a vendor-free `WithDurableStore` — plus a new durable SQL-backed `humantask.TaskStore` (D6) and a `persistence.DurableProvider`.

**Architecture:** `service.NewEngine(opts ...Option) (*Engine, error)` builds in-memory leaves (instance store, definition registry, task store, allow-all authz), then builds the `runtime.ProcessDriver` *from those same leaves* so driver and service can never diverge. Single-instance methods return a `ProcessInstance` interface that marshals itself to the frontend-ready JSON projection (logic moved out of `runtime/view`). Durability flips the whole graph via one interface-typed option, keeping DB drivers out of `service`'s compile graph. The durable graph's task store is a new neutral SQL store over `database.Querier` + `dialect` (Postgres/MySQL/SQLite).

**Tech Stack:** Go 1.25, `expr-lang/expr`, `pgx/v5` (Postgres), `database/sql` (MySQL/SQLite), `modernc.org/sqlite`, goose migrations, testcontainers-go, `samber/do` (examples only), `log/slog`.

## Global Constraints

- **Module path:** `github.com/zakyalvan/krtlwrkflw`.
- **Language:** Go 1.25 (hard requirement).
- **TDD strict:** No production code before a failing test. Every new exported symbol and every behavioural change follows Red → Red-verify (`go test` shows failure/compile error) → Green → Green-verify → Refactor. The red state MUST be observable in the transcript (a separate `Bash` `go test` call between writing the test and writing the impl). See CLAUDE.md "TDD Operational Discipline".
- **Table tests:** Use the project `table-test` skill form — `assert func(t, ...)` closures (NOT `want`/`wantErr` fields), a `ctx` modifier where context matters, and `t.Context()` over `context.Background()`.
- **Black-box tests:** Prefer `package <pkg>_test`.
- **Testcontainers:** For Postgres use `dbtest.RunTestDatabase(t)`; MySQL `dbtest.RunTestMySQL(t)`; SQLite `dbtest.RunTestSQLite(t)`. Never mock a DB.
- **Mocks:** `use-mockgen` skill; `--typed`; mocks live beside the interface in the producer package.
- **Vendor-free invariant:** `go list -deps ./service` must contain no `pgx`, no `go-sql-driver/mysql`, no `modernc.org/sqlite`, no `database/sql`, no `…/persistence`. Enforced by a test (Task 12).
- **Error sentinels:** message prefix `workflow-<pkg>:` (e.g. `workflow-service:`).
- **Timestamps:** store as UTC; route through the dialect time codec (`TimestampsAsText()` branch) — never compare `dialect.Name()` to `"sqlite"` (ADR-0080/0081).
- **Coverage:** touched packages ≥ 85% line coverage. `go test ./...` green. `golangci-lint run ./...` clean.
- **Target ADR:** 0098 (Nygard template).
- **Breaking changes are acceptable** (pre-v0.1.0); no deprecated shims. The old `service.New` and `GetInstanceWithDefinition` are removed outright.

## Build-state note for reviewers

This is a breaking API refactor. After **Task 6** the `service` package compiles and its tests pass, but dependent packages (`transport/http/*`, `examples/*`, `internal/transporttest`) will NOT compile until their migration tasks (Tasks 9–11). This is expected. `go test ./...` is only required green at **Task 15**. Each task keeps its *own* package green.

## Task dependency order

```
Track B (durable persistence — independent, green throughout):
  B1 (Task 1) Dialect.UpsertTask
  B2 (Task 2) wrkflw_human_task migrations
  B3 (Task 3) neutral humantask store  ← needs B1,B2
  B4 (Task 4) persistence facade task-store ctors  ← needs B3

Track A (service API — additive first, then the breaking flip):
  A1 (Task 5) ProcessInstance + projection (additive)
  A2 (Task 6) NewEngine + options + role interfaces + flip return types + migrate service tests  ← needs A1
  A3 (Task 7) segregation compile asserts
  A4 (Task 8) DurableProvider interface + WithDurableStore (additive on A2)

Bridge:
  C1 (Task 12) persistence.DurableProvider  ← needs A4 (interface) + B4 (task store)

Migration:
  M1 (Task 9)  httpcore + admin_endpoints + gin fake
  M2 (Task 10) retire view.InstanceSnapshot  ← needs M1
  M3 (Task 11) examples + transporttest harness

Guards & finish:
  Task 12 vendor-free test  (can run any time after A2; grouped with bridge below as Task 13/14 order)
  Task 15 ADR-0098 + full verification
```

Execute in this numeric order: 1 → 15.

---

### Task 1: `Dialect.UpsertTask()` — per-dialect upsert clause for `wrkflw_human_task`

**Files:**
- Modify: `internal/persistence/dialect/dialect.go` (add method to `Dialect` interface)
- Modify: `internal/persistence/dialect/postgres.go`
- Modify: `internal/persistence/dialect/mysql.go`
- Modify: `internal/persistence/dialect/sqlite.go`
- Test: `internal/persistence/dialect/dialect_test.go` (extend existing conformance/assertion test)

**Interfaces:**
- Produces: `Dialect.UpsertTask() string` — the `ON CONFLICT (task_token) DO UPDATE …` (PG/SQLite) / `ON DUPLICATE KEY UPDATE …` (MySQL) clause, appended after the `INSERT INTO wrkflw_human_task (...) VALUES (...)` statement. Consumed by Task 3.

- [ ] **Step 1: Write the failing test.** Add to `internal/persistence/dialect/dialect_test.go` a table test that asserts each dialect returns a non-empty `UpsertTask()` containing the expected conflict keyword. Follow the `table-test` skill (assert-closure form). Example:

```go
func TestUpsertTaskClause(t *testing.T) {
	tests := []struct {
		name   string
		d      dialect.Dialect
		assert func(t *testing.T, clause string)
	}{
		{
			name: "postgres on-conflict",
			d:    dialect.NewPostgres(),
			assert: func(t *testing.T, clause string) {
				assert.Contains(t, clause, "ON CONFLICT (task_token)")
				assert.Contains(t, clause, "EXCLUDED.state")
			},
		},
		{
			name: "mysql on-duplicate-key",
			d:    dialect.NewMySQL(),
			assert: func(t *testing.T, clause string) {
				assert.Contains(t, clause, "ON DUPLICATE KEY UPDATE")
				assert.Contains(t, clause, "VALUES(state)")
			},
		},
		{
			name: "sqlite on-conflict-excluded",
			d:    dialect.NewSQLite(),
			assert: func(t *testing.T, clause string) {
				assert.Contains(t, clause, "ON CONFLICT (task_token)")
				assert.Contains(t, clause, "excluded.state")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.assert(t, tt.d.UpsertTask())
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails.**

Run: `go test ./internal/persistence/dialect/...`
Expected: FAIL — compile error `tt.d.UpsertTask undefined (type dialect.Dialect has no field or method UpsertTask)`.

- [ ] **Step 3: Add the method to the interface.** In `internal/persistence/dialect/dialect.go`, add `UpsertTask() string` to the `Dialect` interface, placed next to `UpsertDefinition() string`. Add a doc comment matching the style of the neighbours:

```go
	// UpsertTask returns the dialect-specific conflict clause appended to an
	// INSERT INTO wrkflw_human_task ... VALUES(...) so the write is an
	// idempotent insert-or-replace keyed on (task_token).
	UpsertTask() string
```

- [ ] **Step 4: Implement in all three dialects.** Match the exact style of each file's existing `UpsertTimer`/`UpsertDefinition`.

`postgres.go`:
```go
func (postgres) UpsertTask() string {
	return " ON CONFLICT (task_token) DO UPDATE SET" +
		" instance_id = EXCLUDED.instance_id, node_id = EXCLUDED.node_id," +
		" state = EXCLUDED.state, claimed_by = EXCLUDED.claimed_by," +
		" eligibility = EXCLUDED.eligibility, candidates = EXCLUDED.candidates," +
		" vars = EXCLUDED.vars, created_at = EXCLUDED.created_at, due_at = EXCLUDED.due_at"
}
```

`mysql.go`:
```go
func (mysql) UpsertTask() string {
	return " ON DUPLICATE KEY UPDATE" +
		" instance_id=VALUES(instance_id), node_id=VALUES(node_id)," +
		" state=VALUES(state), claimed_by=VALUES(claimed_by)," +
		" eligibility=VALUES(eligibility), candidates=VALUES(candidates)," +
		" vars=VALUES(vars), created_at=VALUES(created_at), due_at=VALUES(due_at)"
}
```

`sqlite.go`:
```go
func (sqliteDialect) UpsertTask() string {
	return " ON CONFLICT (task_token) DO UPDATE SET" +
		" instance_id = excluded.instance_id, node_id = excluded.node_id," +
		" state = excluded.state, claimed_by = excluded.claimed_by," +
		" eligibility = excluded.eligibility, candidates = excluded.candidates," +
		" vars = excluded.vars, created_at = excluded.created_at, due_at = excluded.due_at"
}
```

- [ ] **Step 5: Run tests to verify pass.**

Run: `go test ./internal/persistence/dialect/...`
Expected: PASS. If a separate interface-conformance test in `dialect_test.go` iterates method-by-method, it already passes because the interface + impls are consistent.

- [ ] **Step 6: Commit.**

```bash
git add internal/persistence/dialect/
git commit -m "feat(dialect): add UpsertTask conflict clause for wrkflw_human_task"
```

---

### Task 2: `wrkflw_human_task` migrations (Postgres / MySQL / SQLite)

**Files:**
- Create: `internal/persistence/store/migrations/postgres/0010_human_task.sql`
- Create: `internal/persistence/store/migrations/mysql/0003_human_task.sql`
- Create: `internal/persistence/store/migrations/sqlite/0002_human_task.sql`
- Test: `internal/persistence/store/migration_parity_test.go` (already exists — it auto-covers the new table; this task verifies convergence)

**Interfaces:**
- Produces: table `wrkflw_human_task` with columns `task_token` (PK), `instance_id`, `node_id`, `state`, `claimed_by`, `eligibility`, `candidates`, `vars`, `created_at`, `due_at` (nullable). Indexed on `instance_id`, `state`, `claimed_by`. Consumed by Task 3 (the store) and Task 3's conformance test.

**Before writing:** open `internal/persistence/store/migrations/postgres/0009_*.sql`, `.../mysql/0002_*.sql`, and `.../sqlite/0001_init.sql` and copy their exact goose annotation style (`-- +goose Up` / `-- +goose Down`, and any `-- +goose StatementBegin/End` wrappers they use). The DDL bodies below MUST be wrapped in that same annotation style.

- [ ] **Step 1: Write the failing test.** The parity guardrail `TestMigrationParity_LogicalSchemaConverges` in `internal/persistence/store/migration_parity_test.go` already asserts that every table's logical schema (column names + nullability + PK membership) converges across all three dialects. It currently passes with 8 tables. Confirm it still passes now (baseline), then the new migrations must keep it passing.

Run: `go test -run TestMigrationParity_LogicalSchemaConverges ./internal/persistence/store/...`
Expected: PASS (baseline, before adding migrations). This is the guardrail that will FAIL if the three new DDLs disagree.

- [ ] **Step 2: Create the Postgres migration** `internal/persistence/store/migrations/postgres/0010_human_task.sql` (wrap in the file's goose annotation style):

```sql
CREATE TABLE wrkflw_human_task (
    task_token  TEXT        NOT NULL,
    instance_id TEXT        NOT NULL,
    node_id     TEXT        NOT NULL,
    state       TEXT        NOT NULL,
    claimed_by  TEXT        NOT NULL DEFAULT '',
    eligibility JSONB       NOT NULL,
    candidates  JSONB       NOT NULL,
    vars        JSONB       NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL,
    due_at      TIMESTAMPTZ,
    PRIMARY KEY (task_token)
);
CREATE INDEX idx_wrkflw_human_task_instance ON wrkflw_human_task (instance_id);
CREATE INDEX idx_wrkflw_human_task_state ON wrkflw_human_task (state);
CREATE INDEX idx_wrkflw_human_task_claimed_by ON wrkflw_human_task (claimed_by);
```
Down: `DROP TABLE wrkflw_human_task;`

- [ ] **Step 3: Create the MySQL migration** `internal/persistence/store/migrations/mysql/0003_human_task.sql`:

```sql
CREATE TABLE wrkflw_human_task (
    task_token  VARCHAR(255) NOT NULL,
    instance_id VARCHAR(255) NOT NULL,
    node_id     VARCHAR(255) NOT NULL,
    state       VARCHAR(64)  NOT NULL,
    claimed_by  VARCHAR(255) NOT NULL DEFAULT '',
    eligibility JSON         NOT NULL,
    candidates  JSON         NOT NULL,
    vars        JSON         NOT NULL,
    created_at  DATETIME(6)  NOT NULL,
    due_at      DATETIME(6)  NULL,
    PRIMARY KEY (task_token),
    INDEX idx_wrkflw_human_task_instance (instance_id),
    INDEX idx_wrkflw_human_task_state (state),
    INDEX idx_wrkflw_human_task_claimed_by (claimed_by)
);
```
Down: `DROP TABLE wrkflw_human_task;`

- [ ] **Step 4: Create the SQLite migration** `internal/persistence/store/migrations/sqlite/0002_human_task.sql`:

```sql
CREATE TABLE wrkflw_human_task (
    task_token  TEXT NOT NULL PRIMARY KEY,
    instance_id TEXT NOT NULL,
    node_id     TEXT NOT NULL,
    state       TEXT NOT NULL,
    claimed_by  TEXT NOT NULL DEFAULT '',
    eligibility TEXT NOT NULL,
    candidates  TEXT NOT NULL,
    vars        TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    due_at      TEXT
);
CREATE INDEX idx_wrkflw_human_task_instance ON wrkflw_human_task (instance_id);
CREATE INDEX idx_wrkflw_human_task_state ON wrkflw_human_task (state);
CREATE INDEX idx_wrkflw_human_task_claimed_by ON wrkflw_human_task (claimed_by);
```
Down: `DROP TABLE wrkflw_human_task;`

> Note: the SQLite migrations dir currently holds a single consolidated `0001_init.sql`. Adding `0002_human_task.sql` as a new numbered file is correct (goose applies it after `0001`). If the SQLite embed uses an explicit file list rather than a glob, add the new filename to that list — check `migrate_sqlite.go`.

- [ ] **Step 5: Run the parity + a smoke migration test to verify pass.**

Run: `go test -run 'TestMigrationParity_LogicalSchemaConverges' ./internal/persistence/store/...`
Expected: PASS — the new table converges (task_token PK forced NOT NULL; `due_at` nullable in all three; every other column NOT NULL in all three). Requires Docker for the PG/MySQL legs.

If parity fails, the mismatch is almost always a nullability disagreement — re-check that `claimed_by` is `NOT NULL DEFAULT ''` and `due_at` is nullable in all three files.

- [ ] **Step 6: Commit.**

```bash
git add internal/persistence/store/migrations/
git commit -m "feat(store): add wrkflw_human_task table (PG/MySQL/SQLite migrations)"
```

---

### Task 3: Neutral SQL-backed `humantask.TaskStore` in `internal/persistence/store`

**Files:**
- Create: `internal/persistence/store/humantask_store.go`
- Test: `internal/persistence/store/humantask_store_conformance_test.go` (black-box `store_test`, 3-dialect via `forEachDialect`)

**Interfaces:**
- Consumes: `dialect.Dialect.UpsertTask()` (Task 1); `wrkflw_human_task` table (Task 2); `database.Querier`, `database.From`; time codec helpers `timeArg`/`timeArgP`/`parseTimeText` (already in `internal/persistence/store/time_codec.go` / `store_core.go`); `store.ErrNilDependency`, `store.isNilDep`.
- Produces: `func NewHumanTaskStore(conn any, d dialect.Dialect) (*HumanTaskStore, error)` returning a `*HumanTaskStore` that satisfies `humantask.TaskStore`. Consumed by Task 4 (facade) and Task 12 (durable provider).

**Reference implementations to mirror exactly** (read them first): `internal/persistence/store/definitions.go` (`PutDefinition` upsert, `GetDefinition` not-found→sentinel, JSON `[]byte` marshal/scan), `internal/persistence/store/timerstore.go` (struct+ctor shape, timestamp scan across dialects), `humantask/memory.go` (`ClaimableBy` eligibility rule to replicate in Go).

- [ ] **Step 1: Write the failing conformance test.** Create `internal/persistence/store/humantask_store_conformance_test.go` (package `store_test`). Use the existing `forEachDialect` harness (in `conformance_test.go`) and the `table-test` assert-closure form. Cover: Upsert→Get round-trip (all fields incl. `Eligibility`/`Candidates`/`Vars` JSON and `DueAt`), `Get` miss → `humantask.ErrTaskNotFound`, `AssignedTo` filters by `claimed_by` + sorts by `task_token`, `ClaimableBy` matches by candidate ID, matches by shared role, and excludes non-`Unclaimed` tasks. Start with a compile-time guard and one round-trip case:

```go
package store_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/store"
)

// compile-time guard: the neutral store satisfies the public interface.
var _ humantask.TaskStore = (*store.HumanTaskStore)(nil)

func TestHumanTaskStoreConformance(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		ts, err := store.NewHumanTaskStore(b.conn, b.dialect)
		require.NoError(t, err)

		due := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
		seed := humantask.HumanTask{
			TaskToken:   "tok-1",
			InstanceID:  "inst-1",
			NodeID:      "approve",
			State:       humantask.Unclaimed,
			Eligibility: authz.AuthzSpec{Roles: []string{"manager"}},
			Candidates:  []string{"alice"},
			Vars:        map[string]any{"amount": float64(100)},
			CreatedAt:   time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC),
			DueAt:       &due,
		}
		require.NoError(t, ts.Upsert(t.Context(), seed), "%s: Upsert", b.name)

		got, err := ts.Get(t.Context(), "tok-1")
		require.NoError(t, err, "%s: Get", b.name)
		assert.Equal(t, "inst-1", got.InstanceID, "%s: InstanceID", b.name)
		assert.Equal(t, humantask.Unclaimed, got.State, "%s: State", b.name)
		assert.Equal(t, []string{"manager"}, got.Eligibility.Roles, "%s: Eligibility.Roles", b.name)
		assert.Equal(t, []string{"alice"}, got.Candidates, "%s: Candidates", b.name)
		require.NotNil(t, got.DueAt, "%s: DueAt", b.name)
		assert.True(t, got.DueAt.Equal(due), "%s: DueAt value", b.name)
	})
}
```

- [ ] **Step 2: Run test to verify it fails.**

Run: `go test -run TestHumanTaskStoreConformance ./internal/persistence/store/...`
Expected: FAIL — compile error `undefined: store.HumanTaskStore` / `store.NewHumanTaskStore`.

- [ ] **Step 3: Implement the store.** Create `internal/persistence/store/humantask_store.go`. Mirror `definitions.go` exactly for struct/ctor/querier/JSON, and `timerstore.go` for cross-dialect timestamp scan. Full implementation:

```go
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/internal/database"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
)

// HumanTaskStore is the neutral, dialect-parametrised SQL implementation of
// humantask.TaskStore over the wrkflw_human_task table. It works on
// PostgreSQL, MySQL, and SQLite via the dialect abstraction.
type HumanTaskStore struct {
	conn    any
	dialect dialect.Dialect
}

var _ humantask.TaskStore = (*HumanTaskStore)(nil)

// NewHumanTaskStore constructs a durable task store over conn (a *pgxpool.Pool
// or *sql.DB) using the supplied dialect. It returns ErrNilDependency if conn
// or d is nil.
func NewHumanTaskStore(conn any, d dialect.Dialect) (*HumanTaskStore, error) {
	if isNilDep(conn) {
		return nil, fmt.Errorf("%w: conn", ErrNilDependency)
	}
	if isNilDep(d) {
		return nil, fmt.Errorf("%w: dialect", ErrNilDependency)
	}
	return &HumanTaskStore{conn: conn, dialect: d}, nil
}

func (s *HumanTaskStore) querier() database.Querier {
	q, _ := database.From(s.conn)
	return q
}

const humanTaskColumns = `task_token, instance_id, node_id, state, claimed_by,
	eligibility, candidates, vars, created_at, due_at`

func (s *HumanTaskStore) Upsert(ctx context.Context, t humantask.HumanTask) error {
	eligibility, err := json.Marshal(t.Eligibility)
	if err != nil {
		return fmt.Errorf("workflow-store: upsert task %s: marshal eligibility: %w", t.TaskToken, err)
	}
	candidates, err := json.Marshal(t.Candidates)
	if err != nil {
		return fmt.Errorf("workflow-store: upsert task %s: marshal candidates: %w", t.TaskToken, err)
	}
	vars, err := json.Marshal(t.Vars)
	if err != nil {
		return fmt.Errorf("workflow-store: upsert task %s: marshal vars: %w", t.TaskToken, err)
	}

	q := s.querier()
	_, err = q.Exec(ctx, s.dialect.Rebind(
		`INSERT INTO wrkflw_human_task (`+humanTaskColumns+`)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`+s.dialect.UpsertTask()),
		t.TaskToken, t.InstanceID, t.NodeID, t.State.String(), t.ClaimedBy,
		eligibility, candidates, vars,
		timeArg(s.dialect, t.CreatedAt), s.dueArg(t.DueAt),
	)
	if err != nil {
		return fmt.Errorf("workflow-store: upsert task %s: %w", t.TaskToken, err)
	}
	return nil
}

func (s *HumanTaskStore) Get(ctx context.Context, taskToken string) (humantask.HumanTask, error) {
	q := s.querier()
	row := q.QueryRow(ctx, s.dialect.Rebind(
		`SELECT `+humanTaskColumns+` FROM wrkflw_human_task WHERE task_token = ?`), taskToken)
	t, err := s.scanTask(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return humantask.HumanTask{}, humantask.ErrTaskNotFound
	}
	if err != nil {
		return humantask.HumanTask{}, fmt.Errorf("workflow-store: get task %s: %w", taskToken, err)
	}
	return t, nil
}

func (s *HumanTaskStore) AssignedTo(ctx context.Context, actorID string) ([]humantask.HumanTask, error) {
	return s.query(ctx, "assigned-to",
		`SELECT `+humanTaskColumns+` FROM wrkflw_human_task WHERE claimed_by = ? ORDER BY task_token`,
		actorID)
}

func (s *HumanTaskStore) ClaimableBy(ctx context.Context, actor authz.Actor) ([]humantask.HumanTask, error) {
	candidates, err := s.query(ctx, "claimable-by",
		`SELECT `+humanTaskColumns+` FROM wrkflw_human_task WHERE state = ? ORDER BY task_token`,
		humantask.Unclaimed.String())
	if err != nil {
		return nil, err
	}
	actorRoles := roleSet(actor.Roles)
	var result []humantask.HumanTask
	for _, t := range candidates {
		if candidateContains(t.Candidates, actor.ID) || hasRoleOverlap(actorRoles, t.Eligibility.Roles) {
			result = append(result, t)
		}
	}
	return result, nil
}

func (s *HumanTaskStore) query(ctx context.Context, op, sqlText string, args ...any) ([]humantask.HumanTask, error) {
	q := s.querier()
	rows, err := q.Query(ctx, s.dialect.Rebind(sqlText), args...)
	if err != nil {
		return nil, fmt.Errorf("workflow-store: human task %s: %w", op, err)
	}
	defer func() { _ = rows.Close() }()

	var result []humantask.HumanTask
	for rows.Next() {
		t, err := s.scanTask(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("workflow-store: human task %s: scan: %w", op, err)
		}
		result = append(result, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("workflow-store: human task %s: rows: %w", op, err)
	}
	return result, nil
}

// scanTask decodes one row via the supplied Scan function (works for both
// database.Row and database.Rows).
func (s *HumanTaskStore) scanTask(scan func(dest ...any) error) (humantask.HumanTask, error) {
	var (
		t           humantask.HumanTask
		stateStr    string
		eligibility []byte
		candidates  []byte
		vars        []byte
	)
	created, createdDest := newTimeScan(s.dialect)
	due, dueDest := newNullTimeScan(s.dialect)

	if err := scan(
		&t.TaskToken, &t.InstanceID, &t.NodeID, &stateStr, &t.ClaimedBy,
		&eligibility, &candidates, &vars, createdDest, dueDest,
	); err != nil {
		return humantask.HumanTask{}, err
	}

	t.State = parseTaskState(stateStr)
	if len(eligibility) > 0 {
		if err := json.Unmarshal(eligibility, &t.Eligibility); err != nil {
			return humantask.HumanTask{}, fmt.Errorf("unmarshal eligibility: %w", err)
		}
	}
	if len(candidates) > 0 {
		if err := json.Unmarshal(candidates, &t.Candidates); err != nil {
			return humantask.HumanTask{}, fmt.Errorf("unmarshal candidates: %w", err)
		}
	}
	if len(vars) > 0 {
		if err := json.Unmarshal(vars, &t.Vars); err != nil {
			return humantask.HumanTask{}, fmt.Errorf("unmarshal vars: %w", err)
		}
	}
	ct, err := created.value()
	if err != nil {
		return humantask.HumanTask{}, err
	}
	t.CreatedAt = ct
	dt, ok, err := due.value()
	if err != nil {
		return humantask.HumanTask{}, err
	}
	if ok {
		t.DueAt = &dt
	}
	return t, nil
}

func (s *HumanTaskStore) dueArg(t *time.Time) any {
	if t == nil {
		return nil
	}
	return timeArg(s.dialect, *t)
}

func parseTaskState(s string) humantask.TaskState {
	switch s {
	case humantask.Claimed.String():
		return humantask.Claimed
	case humantask.Completed.String():
		return humantask.Completed
	case humantask.Cancelled.String():
		return humantask.Cancelled
	default:
		return humantask.Unclaimed
	}
}
```

> **Implementation guidance for the timestamp scan helpers and the Go-side eligibility helpers:**
> - `timeArg` already exists in the store package (`time_codec.go`/`store_core.go`) — reuse it. Do NOT re-declare it. `dueArg` above needs `time` imported; if a `timeArgP`-style method already exists on another store struct, prefer a free helper here to avoid coupling.
> - The `newTimeScan`/`newNullTimeScan` + `.value()` helpers in the code above are a *sketch*. **Replace them with the exact cross-dialect timestamp-scan pattern already used in `timerstore.go`** (which branches on `s.dialect.TimestampsAsText()`: scan into a `string`/`sql.NullString` then `parseTimeText` for SQLite, or `time.Time`/`sql.NullTime` for PG/MySQL). If `timerstore.go` exposes a reusable scan helper, call it; otherwise inline the branch directly in `scanTask` (scan `created_at`/`due_at` into dialect-appropriate destinations, convert, assign). Keep the not-found and JSON logic exactly as written above.
> - `roleSet`, `candidateContains`, `hasRoleOverlap` are unexported helpers in `humantask/memory.go` — they are NOT exported, so re-declare equivalent private helpers in this file (copy the 3 tiny functions verbatim from `humantask/memory.go`), or inline the logic. Replicate the `MemTaskStore.ClaimableBy` rule exactly: only `Unclaimed` rows (enforced by the SQL `WHERE state = 'unclaimed'`), then `actor.ID ∈ Candidates OR actor.Roles ∩ Eligibility.Roles ≠ ∅`.
> - Add `"time"` to the import block.

- [ ] **Step 4: Run the conformance test to verify pass.**

Run: `go test -run TestHumanTaskStoreConformance ./internal/persistence/store/...`
Expected: PASS across postgres/mysql/sqlite subtests (Docker required for PG+MySQL).

- [ ] **Step 5: Extend the conformance test** with the remaining cases (miss→`ErrTaskNotFound`, `AssignedTo` filter+sort, `ClaimableBy` by-candidate / by-role / excludes-claimed) using the assert-closure table form. Run again:

Run: `go test -run TestHumanTaskStoreConformance ./internal/persistence/store/...`
Expected: PASS.

- [ ] **Step 6: Commit.**

```bash
git add internal/persistence/store/humantask_store.go internal/persistence/store/humantask_store_conformance_test.go
git commit -m "feat(store): durable SQL humantask.TaskStore over dialect abstraction"
```

---

### Task 4: `persistence` facade constructors for the durable task store

**Files:**
- Create: `persistence/humantask.go`
- Test: `persistence/humantask_test.go`

**Interfaces:**
- Consumes: `store.NewHumanTaskStore` (Task 3), `dialect.NewPostgres/NewMySQL/NewSQLite`.
- Produces:
  - `func NewTaskStore(pool *pgxpool.Pool) (humantask.TaskStore, error)` (Postgres)
  - `func NewMySQLTaskStore(db *sql.DB) (humantask.TaskStore, error)`
  - `func NewSQLiteTaskStore(db *sql.DB) (humantask.TaskStore, error)`
  Consumed by Task 12 (durable provider).

- [ ] **Step 1: Write the failing test.** Create `persistence/humantask_test.go` (package `persistence_test`). Assert each constructor returns a non-nil `humantask.TaskStore` and that a nil conn yields `persistence.ErrNilDependency`. For the DB-backed happy path, use `dbtest.RunTestSQLite(t)` (no Docker) for a fast round-trip; leave PG/MySQL round-trips to the store conformance test (Task 3):

```go
func TestNewSQLiteTaskStore(t *testing.T) {
	db := dbtest.RunTestSQLite(t)
	ts, err := persistence.NewSQLiteTaskStore(db)
	require.NoError(t, err)
	require.NotNil(t, ts)

	err = ts.Upsert(t.Context(), humantask.HumanTask{
		TaskToken: "tok", InstanceID: "i", NodeID: "n",
		State: humantask.Unclaimed, CreatedAt: time.Now().UTC(),
	})
	require.NoError(t, err)
	got, err := ts.Get(t.Context(), "tok")
	require.NoError(t, err)
	assert.Equal(t, "i", got.InstanceID)
}

func TestNewSQLiteTaskStoreNilConn(t *testing.T) {
	_, err := persistence.NewSQLiteTaskStore(nil)
	require.ErrorIs(t, err, persistence.ErrNilDependency)
}
```

- [ ] **Step 2: Run test to verify it fails.**

Run: `go test -run 'TestNewSQLiteTaskStore' ./persistence/...`
Expected: FAIL — `undefined: persistence.NewSQLiteTaskStore`.

- [ ] **Step 3: Implement the facade.** Create `persistence/humantask.go`, mirroring the thin delegations in `persistence/sqlite.go`/`mysql.go`/`persistence.go`:

```go
package persistence

import (
	"database/sql"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/store"
)

// NewTaskStore returns a durable PostgreSQL-backed humantask.TaskStore over the
// wrkflw_human_task table. Run Migrate(ctx, pool) first to create the schema.
func NewTaskStore(pool *pgxpool.Pool) (humantask.TaskStore, error) {
	return store.NewHumanTaskStore(pool, dialect.NewPostgres())
}

// NewMySQLTaskStore returns a durable MySQL-backed humantask.TaskStore.
func NewMySQLTaskStore(db *sql.DB) (humantask.TaskStore, error) {
	return store.NewHumanTaskStore(db, dialect.NewMySQL())
}

// NewSQLiteTaskStore returns a durable SQLite-backed humantask.TaskStore.
func NewSQLiteTaskStore(db *sql.DB) (humantask.TaskStore, error) {
	return store.NewHumanTaskStore(db, dialect.NewSQLite())
}
```

- [ ] **Step 4: Run tests to verify pass.**

Run: `go test -run 'TestNewSQLiteTaskStore' ./persistence/...`
Expected: PASS.

- [ ] **Step 5: Commit.**

```bash
git add persistence/humantask.go persistence/humantask_test.go
git commit -m "feat(persistence): NewTaskStore facade ctors (PG/MySQL/SQLite)"
```

---

### Task 5: `ProcessInstance` interface + self-serializing JSON projection (additive)

**Files:**
- Create: `service/instance.go`
- Test: `service/instance_test.go`

**Interfaces:**
- Consumes: `engine.InstanceState`, `definition/model.ProcessDefinition` (+ `model.ActionOf`, `model.InlineActionOf`, `model.KindServiceTask`, `model.KindBusinessRuleTask`).
- Produces:
  - `type ProcessInstance interface { Definition() *model.ProcessDefinition; State() engine.InstanceState; json.Marshaler }`
  - `func NewProcessInstance(def *model.ProcessDefinition, st engine.InstanceState) ProcessInstance`
  Consumed by Task 6 (method return types) and Task 9 (transports).

- [ ] **Step 1: Write the failing test.** Create `service/instance_test.go` (package `service_test`). Assert: `State()`/`Definition()` return the raw inputs; `json.Marshal(pi)` produces the projection with the expected top-level keys (`instance_id`, `status`, `scoped_actions`, `action_bindings`); nil-definition marshals without panic and omits def-derived fields; embedding `ProcessInstance` in a consumer struct marshals via the promoted `MarshalJSON`. Example skeleton:

```go
func TestProcessInstanceStateAndDefinition(t *testing.T) {
	def := &model.ProcessDefinition{ID: "greeting", Version: 1}
	st := engine.InstanceState{InstanceID: "i-1", DefID: "greeting", DefVersion: 1, Status: engine.StatusRunning}
	pi := service.NewProcessInstance(def, st)
	assert.Equal(t, def, pi.Definition())
	assert.Equal(t, st, pi.State())
}

func TestProcessInstanceMarshalJSON(t *testing.T) {
	def := &model.ProcessDefinition{ID: "greeting", Version: 1}
	st := engine.InstanceState{InstanceID: "i-1", DefID: "greeting", DefVersion: 1, Status: engine.StatusRunning}
	data, err := json.Marshal(service.NewProcessInstance(def, st))
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))
	assert.Equal(t, "i-1", m["instance_id"])
	assert.Equal(t, "running", m["status"])
}

func TestProcessInstanceMarshalNilDefinition(t *testing.T) {
	st := engine.InstanceState{InstanceID: "i-1", Status: engine.StatusRunning}
	data, err := json.Marshal(service.NewProcessInstance(nil, st))
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))
	_, hasBindings := m["action_bindings"]
	assert.False(t, hasBindings, "nil def omits action_bindings")
}
```

(Confirm the exact `engine.Status` string for `StatusRunning` by reading `engine.Status.String()`; adjust `"running"` if the impl differs.)

- [ ] **Step 2: Run test to verify it fails.**

Run: `go test -run TestProcessInstance ./service/...`
Expected: FAIL — `undefined: service.NewProcessInstance` / `service.ProcessInstance`.

- [ ] **Step 3: Implement `service/instance.go`.** Move the projection logic out of `runtime/view.NewInstanceSnapshot` into UNEXPORTED types here. The public surface is only the interface + `NewProcessInstance`.

```go
package service

import (
	"encoding/json"
	"sort"
	"time"

	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
)

// ProcessInstance is the read-only, fused view of a running instance: its
// definition and state. It serializes directly to a stable, frontend-ready JSON
// document via MarshalJSON; the serialized shape is an internal detail (no
// exported DTO fields), so a consumer can embed it in its own domain/DTO type
// and marshal with no transformation.
type ProcessInstance interface {
	Definition() *model.ProcessDefinition // raw template (nil if unresolved)
	State() engine.InstanceState          // raw running state
	json.Marshaler                        // MarshalJSON() ([]byte, error)
}

// NewProcessInstance fuses a definition (may be nil) and instance state into a
// ProcessInstance. Exported so consumers and tests can fabricate one.
func NewProcessInstance(def *model.ProcessDefinition, st engine.InstanceState) ProcessInstance {
	return processInstance{def: def, st: st}
}

type processInstance struct {
	def *model.ProcessDefinition
	st  engine.InstanceState
}

func (p processInstance) Definition() *model.ProcessDefinition { return p.def }
func (p processInstance) State() engine.InstanceState          { return p.st }

func (p processInstance) MarshalJSON() ([]byte, error) {
	return json.Marshal(newInstanceJSON(p.def, p.st))
}

// instanceJSON is the UNEXPORTED serialized projection. Field names/tags match
// the retired runtime/view.InstanceSnapshot for wire compatibility.
type instanceJSON struct {
	InstanceID     string              `json:"instance_id"`
	DefID          string              `json:"def_id"`
	DefVersion     int                 `json:"def_version"`
	Status         string              `json:"status"`
	Variables      map[string]any      `json:"variables,omitempty"`
	Tokens         []tokenJSON         `json:"tokens,omitempty"`
	History        []nodeVisitJSON     `json:"history,omitempty"`
	Tasks          []taskJSON          `json:"tasks,omitempty"`
	Incidents      []incidentJSON      `json:"incidents,omitempty"`
	StartedAt      time.Time           `json:"started_at"`
	EndedAt        *time.Time          `json:"ended_at,omitempty"`
	ScopedActions  []string            `json:"scoped_actions,omitempty"`
	ActionBindings []actionBindingJSON `json:"action_bindings,omitempty"`
}

type tokenJSON struct {
	ID            string         `json:"id"`
	NodeID        string         `json:"node_id"`
	ScopeID       string         `json:"scope_id,omitempty"`
	State         string         `json:"state"`
	Payload       map[string]any `json:"payload,omitempty"`
	EnteredAt     time.Time      `json:"entered_at"`
	RetryAttempts int            `json:"retry_attempts,omitempty"`
}

type nodeVisitJSON struct {
	NodeID    string     `json:"node_id"`
	TokenID   string     `json:"token_id"`
	EnteredAt time.Time  `json:"entered_at"`
	LeftAt    *time.Time `json:"left_at,omitempty"`
	ActorID   string     `json:"actor_id,omitempty"`
}

type incidentJSON struct {
	ID        string    `json:"id"`
	TokenID   string    `json:"token_id"`
	NodeID    string    `json:"node_id"`
	ScopeID   string    `json:"scope_id,omitempty"`
	Error     string    `json:"error"`
	Attempts  int       `json:"attempts"`
	CreatedAt time.Time `json:"created_at"`
}

type taskJSON struct {
	TaskToken  string     `json:"task_token"`
	NodeID     string     `json:"node_id"`
	State      string     `json:"state"`
	ClaimedBy  string     `json:"claimed_by,omitempty"`
	Candidates []string   `json:"candidates,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	DueAt      *time.Time `json:"due_at,omitempty"`
}

type actionBindingJSON struct {
	NodeID   string `json:"node_id"`
	NodeKind string `json:"node_kind"`
	Action   string `json:"action,omitempty"`
	Inline   bool   `json:"inline"`
}

func newInstanceJSON(def *model.ProcessDefinition, st engine.InstanceState) instanceJSON {
	tokens := make([]tokenJSON, 0, len(st.Tokens))
	for _, t := range st.Tokens {
		tokens = append(tokens, tokenJSON{
			ID: t.ID, NodeID: t.NodeID, ScopeID: t.ScopeID,
			State: tokenStateString(t.State), Payload: t.Payload,
			EnteredAt: t.EnteredAt, RetryAttempts: t.RetryAttempts,
		})
	}
	history := make([]nodeVisitJSON, 0, len(st.History))
	for _, v := range st.History {
		history = append(history, nodeVisitJSON{
			NodeID: v.NodeID, TokenID: v.TokenID,
			EnteredAt: v.EnteredAt, LeftAt: v.LeftAt, ActorID: v.ActorID,
		})
	}
	tasks := make([]taskJSON, 0, len(st.Tasks))
	for _, t := range st.Tasks {
		tasks = append(tasks, taskJSON{
			TaskToken: t.TaskToken, NodeID: t.NodeID, State: t.State.String(),
			ClaimedBy: t.ClaimedBy, Candidates: t.Candidates,
			CreatedAt: t.CreatedAt, DueAt: t.DueAt,
		})
	}
	incidents := make([]incidentJSON, 0, len(st.Incidents))
	for _, i := range st.Incidents {
		incidents = append(incidents, incidentJSON{
			ID: i.ID, TokenID: i.TokenID, NodeID: i.NodeID, ScopeID: i.ScopeID,
			Error: i.Error, Attempts: i.Attempts, CreatedAt: i.CreatedAt,
		})
	}

	out := instanceJSON{
		InstanceID: st.InstanceID, DefID: st.DefID, DefVersion: st.DefVersion,
		Status: st.Status.String(), Variables: st.Variables,
		Tokens: tokens, History: history, Tasks: tasks, Incidents: incidents,
		StartedAt: st.StartedAt, EndedAt: st.EndedAt,
	}

	if def != nil {
		out.ScopedActions = def.ScopedActionNames()
		var bindings []actionBindingJSON
		for _, n := range def.Nodes {
			switch n.Kind() {
			case model.KindServiceTask:
				bindings = append(bindings, actionBindingJSON{
					NodeID: n.ID(), NodeKind: "serviceTask",
					Action: model.ActionOf(n), Inline: model.InlineActionOf(n) != nil,
				})
			case model.KindBusinessRuleTask:
				bindings = append(bindings, actionBindingJSON{
					NodeID: n.ID(), NodeKind: "businessRuleTask",
					Action: model.ActionOf(n), Inline: model.InlineActionOf(n) != nil,
				})
			}
		}
		if len(bindings) > 0 {
			sort.Slice(bindings, func(i, j int) bool { return bindings[i].NodeID < bindings[j].NodeID })
			out.ActionBindings = bindings
		}
	}
	return out
}
```

> **Copy `tokenStateString` verbatim** from `runtime/view/instance_snapshot.go` into this file (it is the unexported `func tokenStateString(s engine.TokenState) string` mapping token states to `"active"/"waitingCommand"/"atJoin"/"incident"/"unknown"`). `st.Status.String()` replaces `view.StatusString(st.Status)` (they are equivalent — `StatusString` just delegates to `engine.Status.String`).

- [ ] **Step 4: Run tests to verify pass.**

Run: `go test -run TestProcessInstance ./service/...`
Expected: PASS.

- [ ] **Step 5: Add a golden-parity test** asserting `newInstanceJSON` produces byte-identical JSON to the current `view.NewInstanceSnapshot` for a representative populated state (guards the move). In `instance_test.go`:

```go
func TestInstanceJSONMatchesLegacyViewSnapshot(t *testing.T) {
	def := buildPopulatedDef(t) // service task + business-rule task
	st := buildPopulatedState(t)
	got, err := json.Marshal(service.NewProcessInstance(def, st))
	require.NoError(t, err)
	want, err := json.Marshal(view.NewInstanceSnapshot(st, def))
	require.NoError(t, err)
	assert.JSONEq(t, string(want), string(got))
}
```

(Import `runtime/view` in the test only. This test is deleted in Task 10 when `view.NewInstanceSnapshot` is retired — note that in Task 10.)

Run: `go test ./service/...`
Expected: PASS.

- [ ] **Step 6: Commit.**

```bash
git add service/instance.go service/instance_test.go
git commit -m "feat(service): ProcessInstance interface + self-serializing JSON projection"
```

---

### Task 6: `NewEngine` coherent-graph constructor, options, role interfaces, and return-type flip

**Files:**
- Create: `service/options.go`
- Create: `service/options_test.go`
- Modify: `service/errors.go` (add `ErrNilDependency`)
- Modify: `service/service.go` (Engine struct unchanged fields; remove `New`; add `NewEngine`; role interfaces; `Service` recomposed; method return types → `ProcessInstance`; remove `GetInstanceWithDefinition`; construction summary)
- Modify: `service/service_test.go`, `service/cancel_instance_test.go`, `service/resolve_incident_test.go`, `service/errors_test.go`, `service/coverage_gaps_test.go` (migrate all `service.New(...)` → `NewEngine(...)`; drop `GetInstanceWithDefinition` test; adapt return reads)

**Interfaces:**
- Consumes: `ProcessInstance`, `NewProcessInstance` (Task 5); `runtime.NewProcessDriver`, `runtime.WithInstanceStore/WithDefinitions/WithClock/WithTimerStore/WithCallLinkStore`, `runtime.DefaultDefinitionRegistry`; `kernel.NewMemInstanceStore`, `kernel.InstanceStore/InstanceLister/DefinitionRegistry/TimerStore/CallLinkStore`; `humantask.NewMemTaskStore`, `humantask.TaskStore`; `task.NewTaskService`, `task.WithClock`; `authz.AllowAll`, `authz.Authorizer`; `clock.System`.
- Produces:
  - `func NewEngine(opts ...Option) (*Engine, error)`
  - `type Option func(*engineConfig)` and options `WithProcessDriver`, `WithInstanceStore`, `WithDefinitions`, `WithLister`, `WithHumanTasks`, `WithClock` (WithDurableStore added in Task 8)
  - `var ErrNilDependency error`
  - role interfaces `InstanceStarter`, `InstanceReader`, `TaskManager`, `Messaging`, `InstanceOps`; recomposed `Service`
  - all single-instance methods now return `(ProcessInstance, error)`; `GetInstanceWithDefinition` removed
  Consumed by Tasks 7, 8, 9, 11.

- [ ] **Step 1: Write the failing tests (options + NewEngine defaults + nil-guard).** Create `service/options_test.go` (package `service_test`). Cover: zero-config `NewEngine()` succeeds and round-trips a start→get in-memory (same store observed by driver and reader); a `WithInstanceStore(nil)` is ignored (defaults still apply); explicit nil leaves surfaced through a fake option produce `ErrNilDependency`. Table form:

```go
func TestNewEngineZeroConfig(t *testing.T) {
	e, err := service.NewEngine()
	require.NoError(t, err)
	require.NotNil(t, e)
}

func TestNewEngineDefaultGraphRoundTrips(t *testing.T) {
	e, err := service.NewEngine(service.WithDefinitions(regWith(t, linearDef())))
	require.NoError(t, err)
	pi, err := e.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: "linear:1", InstanceID: "i-1",
	})
	require.NoError(t, err)
	got, err := e.GetInstance(t.Context(), "i-1")
	require.NoError(t, err)
	assert.Equal(t, pi.State().InstanceID, got.State().InstanceID)
}
```

(Use the existing `service_test.go` helpers — `linearDef`, `defRefFor` — and a small `regWith` helper that registers a def into a `kernel.MemDefinitionRegistry`.)

- [ ] **Step 2: Run to verify it fails.**

Run: `go test ./service/...`
Expected: FAIL — `undefined: service.NewEngine` / `service.Option` / `service.WithDefinitions`.

- [ ] **Step 3: Add the sentinel.** In `service/errors.go` add:

```go
// ErrNilDependency is returned by NewEngine when a required dependency resolves
// to nil (an explicitly-nil leaf, or a DurableProvider returning a nil leaf).
var ErrNilDependency = errors.New("workflow-service: nil required dependency")
```

- [ ] **Step 4: Create `service/options.go`.**

```go
package service

import (
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// Option configures NewEngine. Options that receive nil are ignored (the
// coherent in-memory default is kept), except WithDurableStore leaves, which
// are set as-is so a nil leaf surfaces as ErrNilDependency during validation.
type Option func(*engineConfig)

type engineConfig struct {
	driver        *runtime.ProcessDriver
	store         kernel.InstanceStore
	reg           kernel.DefinitionRegistry
	lister        kernel.InstanceLister
	taskStore     humantask.TaskStore
	authz         authz.Authorizer
	timerStore    kernel.TimerStore
	callLinkStore kernel.CallLinkStore
	clk           clock.Clock
	durable       bool
}

// WithProcessDriver supplies a pre-built driver (escape hatch for tests /
// advanced wiring). When set, NewEngine does not build a driver from the leaves.
func WithProcessDriver(d *runtime.ProcessDriver) Option {
	return func(c *engineConfig) {
		if d != nil {
			c.driver = d
		}
	}
}

// WithInstanceStore overrides the in-memory instance store.
func WithInstanceStore(s kernel.InstanceStore) Option {
	return func(c *engineConfig) {
		if s != nil {
			c.store = s
		}
	}
}

// WithDefinitions overrides the default process-global definition registry.
func WithDefinitions(reg kernel.DefinitionRegistry) Option {
	return func(c *engineConfig) {
		if reg != nil {
			c.reg = reg
		}
	}
}

// WithLister overrides the instance lister (defaults to the instance store when
// it satisfies kernel.InstanceLister).
func WithLister(l kernel.InstanceLister) Option {
	return func(c *engineConfig) {
		if l != nil {
			c.lister = l
		}
	}
}

// WithHumanTasks overrides the human-task store and authorizer used to build
// the internal task service.
func WithHumanTasks(taskStore humantask.TaskStore, az authz.Authorizer) Option {
	return func(c *engineConfig) {
		if taskStore != nil {
			c.taskStore = taskStore
		}
		if az != nil {
			c.authz = az
		}
	}
}

// WithClock overrides the clock used by the engine and the internal task
// service (and the default driver).
func WithClock(clk clock.Clock) Option {
	return func(c *engineConfig) {
		if clk != nil {
			c.clk = clk
		}
	}
}
```

- [ ] **Step 5: Rewrite `service/service.go`.** Replace `New` with `NewEngine`, add role interfaces, recompose `Service`, flip return types, remove `GetInstanceWithDefinition`, add the construction summary. Key pieces:

Role interfaces + `Service` (replace the existing `Service` interface block):
```go
type InstanceStarter interface {
	StartInstance(ctx context.Context, req StartInstanceRequest) (ProcessInstance, error)
}
type InstanceReader interface {
	GetInstance(ctx context.Context, instanceID string) (ProcessInstance, error)
	ListInstances(ctx context.Context, filter kernel.InstanceFilter) (kernel.InstancePage, error)
}
type TaskManager interface {
	ClaimTask(ctx context.Context, req ClaimTaskRequest) (ProcessInstance, error)
	CompleteTask(ctx context.Context, req CompleteTaskRequest) (ProcessInstance, error)
	ReassignTask(ctx context.Context, req ReassignTaskRequest) (ProcessInstance, error)
}
type Messaging interface {
	DeliverSignal(ctx context.Context, req DeliverSignalRequest) (ProcessInstance, error)
	DeliverMessage(ctx context.Context, req DeliverMessageRequest) error
}
type InstanceOps interface {
	ResolveIncident(ctx context.Context, req ResolveIncidentRequest) (ProcessInstance, error)
	CancelInstance(ctx context.Context, req CancelInstanceRequest) (ProcessInstance, error)
}

type Service interface {
	InstanceStarter
	InstanceReader
	TaskManager
	Messaging
	InstanceOps
}

var _ Service = (*Engine)(nil)
```

`NewEngine`:
```go
func NewEngine(opts ...Option) (*Engine, error) {
	c := &engineConfig{}
	for _, o := range opts {
		if o != nil {
			o(c)
		}
	}

	// In-memory defaults are applied only in the non-durable path so that a
	// DurableProvider returning a nil required leaf surfaces via validation
	// rather than being silently replaced.
	if !c.durable {
		if c.store == nil {
			ms, err := kernel.NewMemInstanceStore()
			if err != nil {
				return nil, fmt.Errorf("workflow-service: default instance store: %w", err)
			}
			c.store = ms
		}
		if c.reg == nil {
			c.reg = runtime.DefaultDefinitionRegistry()
		}
		if c.taskStore == nil {
			c.taskStore = humantask.NewMemTaskStore()
		}
	}
	if c.clk == nil {
		c.clk = clock.System()
	}
	if c.authz == nil {
		c.authz = authz.AllowAll{}
	}
	if c.lister == nil {
		if l, ok := c.store.(kernel.InstanceLister); ok {
			c.lister = l
		}
	}

	tasks, err := task.NewTaskService(c.taskStore, c.authz, task.WithClock(c.clk))
	if err != nil {
		return nil, fmt.Errorf("workflow-service: task service: %w", err)
	}

	driver := c.driver
	if driver == nil {
		dopts := []runtime.Option{
			runtime.WithInstanceStore(c.store),
			runtime.WithDefinitions(c.reg),
			runtime.WithClock(c.clk),
		}
		if c.timerStore != nil {
			dopts = append(dopts, runtime.WithTimerStore(c.timerStore))
		}
		if c.callLinkStore != nil {
			dopts = append(dopts, runtime.WithCallLinkStore(c.callLinkStore))
		}
		d, derr := runtime.NewProcessDriver(dopts...)
		if derr != nil {
			return nil, fmt.Errorf("workflow-service: default driver: %w", derr)
		}
		driver = d
	}

	if err := validateEngineDeps(driver, c); err != nil {
		return nil, err
	}

	e := &Engine{
		runner:    driver,
		tasks:     tasks,
		reg:       c.reg,
		store:     c.store,
		lister:    c.lister,
		taskStore: c.taskStore,
		clk:       c.clk,
	}
	e.logConstructionSummary(c)
	return e, nil
}

func validateEngineDeps(driver *runtime.ProcessDriver, c *engineConfig) error {
	switch {
	case driver == nil:
		return fmt.Errorf("%w: process driver", ErrNilDependency)
	case c.store == nil:
		return fmt.Errorf("%w: instance store", ErrNilDependency)
	case c.reg == nil:
		return fmt.Errorf("%w: definition registry", ErrNilDependency)
	case c.lister == nil:
		return fmt.Errorf("%w: instance lister", ErrNilDependency)
	case c.taskStore == nil:
		return fmt.Errorf("%w: task store", ErrNilDependency)
	}
	return nil
}
```

Construction summary (mirror `runtime.logConstructionSummary`; use `slog.Default()`):
```go
func (e *Engine) logConstructionSummary(c *engineConfig) {
	storeLabel := "in-memory(non-durable)"
	if c.durable {
		storeLabel = "durable"
	}
	authzLabel := "custom"
	if _, ok := c.authz.(authz.AllowAll); ok {
		authzLabel = "allow-all"
	}
	defLabel := "custom"
	if c.reg == runtime.DefaultDefinitionRegistry() {
		defLabel = "default-global"
	}
	slog.Default().LogAttrs(context.Background(), slog.LevelDebug,
		"service.Engine constructed",
		slog.String("store", storeLabel),
		slog.String("definitions", defLabel),
		slog.String("taskStore", storeLabel),
		slog.String("authz", authzLabel),
		slog.String("hint", "in-memory graph is not durable; wire service.WithDurableStore(persistence.NewDurableProvider(...)) for production"),
	)
}
```
(Add `"log/slog"` to the import block.)

Method changes — flip return types and wrap with `NewProcessInstance`. Examples:
```go
func (e *Engine) StartInstance(ctx context.Context, req StartInstanceRequest) (ProcessInstance, error) {
	def, err := e.reg.Lookup(ctx, req.DefRef)
	if err != nil {
		return nil, err
	}
	st, err := e.runner.Run(ctx, def, req.InstanceID, req.Vars)
	if err != nil {
		return nil, err
	}
	return NewProcessInstance(def, st), nil
}

// GetInstance folds in the definition (nil if unresolved) — replaces the
// removed GetInstanceWithDefinition. State is returned even when the definition
// cannot be resolved.
func (e *Engine) GetInstance(ctx context.Context, instanceID string) (ProcessInstance, error) {
	st, _, err := e.store.Load(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	def, _ := e.reg.Lookup(ctx, fmt.Sprintf("%s:%d", st.DefID, st.DefVersion))
	return NewProcessInstance(def, st), nil
}
```
Apply the same wrap to `DeliverSignal`, `ClaimTask`, `CompleteTask`, `ReassignTask`, `ResolveIncident`, `CancelInstance` (each already resolves `def` via `resolveDefinition` or a `reg.Lookup`; return `NewProcessInstance(def, st)` on success, `nil` on error). Change the private helper `deliverTaskTrigger` to return `(ProcessInstance, error)` — it already has `def` from `resolveDefinition`; wrap its final `st`. Keep `DeliverMessage` (error only) and `ListInstances` (`kernel.InstancePage`) unchanged. **Delete** the `GetInstanceWithDefinition` method and its interface declaration entirely.

- [ ] **Step 6: Migrate all service tests.** In `service_test.go`, `cancel_instance_test.go`, `resolve_incident_test.go`, `errors_test.go`, `coverage_gaps_test.go`, replace every `service.New(runner, taskSvc, reg, store, lister, taskStore, opts...)` with:
```go
svc, err := service.NewEngine(
	service.WithProcessDriver(runner),
	service.WithInstanceStore(store),
	service.WithDefinitions(reg),
	service.WithLister(lister),          // pass `store` where the old call passed store as lister
	service.WithHumanTasks(taskStore, az),
	service.WithClock(fc),               // where a fake clock was used
)
require.NoError(t, err)
```
Adjust each callsite's return reads: methods that returned `engine.InstanceState` now return `ProcessInstance` — read `.State().Status`, `.State().InstanceID`, `.State().Tokens`, etc. **Delete** `TestGetInstanceWithDefinition` from `coverage_gaps_test.go` (the method is gone); if a def-missing-on-get case is still valuable, replace it with a `TestGetInstanceNilDefinitionWhenUnresolved` that asserts `pi.Definition() == nil` and no error. Where a test needs the authorizer (e.g. `TestClaimTaskAuthorizationFailure`), pass a real `authz.RoleAuthorizer{}` via `WithHumanTasks`. Where a test injects `&errLister{}` as the lister, pass it via `WithLister`.

- [ ] **Step 7: Run to verify pass.**

Run: `go test ./service/...`
Expected: PASS with the whole `service` package green.

- [ ] **Step 8: Coverage check.**

Run: `go test -race -coverprofile=cover.out ./service/... && go tool cover -func=cover.out | tail -1`
Expected: total ≥ 85%.

- [ ] **Step 9: Commit.**

```bash
git add service/
git commit -m "feat(service)!: NewEngine coherent-graph ctor, role interfaces, ProcessInstance returns

BREAKING: service.New removed (use NewEngine(opts...)); GetInstanceWithDefinition
removed (folded into GetInstance); single-instance methods return ProcessInstance."
```

---

### Task 7: Role-interface compile-time satisfaction asserts

**Files:**
- Create: `service/segregation_test.go`

**Interfaces:**
- Consumes: role interfaces + `Service` (Task 6).

- [ ] **Step 1: Write the test (compile-time asserts + a behavioural smoke).**

```go
package service_test

import (
	"testing"

	"github.com/zakyalvan/krtlwrkflw/service"
)

var (
	_ service.InstanceStarter = (*service.Engine)(nil)
	_ service.InstanceReader  = (*service.Engine)(nil)
	_ service.TaskManager     = (*service.Engine)(nil)
	_ service.Messaging       = (*service.Engine)(nil)
	_ service.InstanceOps     = (*service.Engine)(nil)
	_ service.Service         = (*service.Engine)(nil)
)

func TestEngineSatisfiesRoleInterfaces(t *testing.T) {
	e, err := service.NewEngine()
	if err != nil {
		t.Fatal(err)
	}
	var _ service.Service = e
}
```

- [ ] **Step 2: Run to verify pass** (this compiles only if Task 6 landed correctly — a pure guard, no red needed beyond confirming it builds).

Run: `go test -run TestEngineSatisfiesRoleInterfaces ./service/...`
Expected: PASS.

- [ ] **Step 3: Commit.**

```bash
git add service/segregation_test.go
git commit -m "test(service): compile-time role-interface satisfaction asserts"
```

---

### Task 8: `DurableProvider` interface + `WithDurableStore` (additive on Task 6)

**Files:**
- Create: `service/durable.go`
- Test: `service/durable_test.go`
- Modify: `service/options.go` (add `WithDurableStore`)

**Interfaces:**
- Consumes: `kernel.InstanceStore/DefinitionRegistry/InstanceLister/TimerStore/CallLinkStore`, `humantask.TaskStore`.
- Produces:
  - `type DurableProvider interface { InstanceStore() kernel.InstanceStore; Definitions() kernel.DefinitionRegistry; Lister() kernel.InstanceLister; TaskStore() humantask.TaskStore; TimerStore() kernel.TimerStore; CallLinkStore() kernel.CallLinkStore }`
  - `func WithDurableStore(p DurableProvider) Option`
  Consumed by Task 12 (persistence provider satisfies this interface).

- [ ] **Step 1: Write the failing test.** Create `service/durable_test.go` with a fake `DurableProvider` wiring all leaves; assert the engine uses them (driver rebuilt from them) and precedence: a later `WithInstanceStore` overrides the provider's store; a provider returning a nil required leaf yields `ErrNilDependency`.

```go
type fakeProvider struct {
	store  kernel.InstanceStore
	reg    kernel.DefinitionRegistry
	lister kernel.InstanceLister
	tasks  humantask.TaskStore
}

func (f fakeProvider) InstanceStore() kernel.InstanceStore    { return f.store }
func (f fakeProvider) Definitions() kernel.DefinitionRegistry { return f.reg }
func (f fakeProvider) Lister() kernel.InstanceLister          { return f.lister }
func (f fakeProvider) TaskStore() humantask.TaskStore         { return f.tasks }
func (f fakeProvider) TimerStore() kernel.TimerStore          { return nil }
func (f fakeProvider) CallLinkStore() kernel.CallLinkStore    { return nil }

func TestWithDurableStore(t *testing.T) {
	ms, _ := kernel.NewMemInstanceStore()
	reg := kernel.NewMemDefinitionRegistry()
	p := fakeProvider{store: ms, reg: reg, lister: ms, tasks: humantask.NewMemTaskStore()}
	e, err := service.NewEngine(service.WithDurableStore(p))
	require.NoError(t, err)
	require.NotNil(t, e)
}

func TestWithDurableStoreNilLeafFails(t *testing.T) {
	p := fakeProvider{store: nil, reg: kernel.NewMemDefinitionRegistry(),
		lister: nil, tasks: humantask.NewMemTaskStore()}
	_, err := service.NewEngine(service.WithDurableStore(p))
	require.ErrorIs(t, err, service.ErrNilDependency)
}

func TestWithDurableStorePrecedenceLaterOverride(t *testing.T) {
	ms1, _ := kernel.NewMemInstanceStore()
	ms2, _ := kernel.NewMemInstanceStore()
	reg := kernel.NewMemDefinitionRegistry()
	p := fakeProvider{store: ms1, reg: reg, lister: ms1, tasks: humantask.NewMemTaskStore()}
	e, err := service.NewEngine(service.WithDurableStore(p), service.WithInstanceStore(ms2))
	require.NoError(t, err)
	require.NotNil(t, e) // later WithInstanceStore(ms2) wins over provider's ms1
}
```

- [ ] **Step 2: Run to verify it fails.**

Run: `go test -run TestWithDurableStore ./service/...`
Expected: FAIL — `undefined: service.WithDurableStore` / `service.DurableProvider`.

- [ ] **Step 3: Create `service/durable.go`.**

```go
package service

import (
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// DurableProvider supplies a coherent set of durable graph leaves. The
// driver-backed implementation lives in the persistence package (which may
// import DB drivers); service depends only on this interface so DB drivers
// never enter service's compile graph.
type DurableProvider interface {
	InstanceStore() kernel.InstanceStore
	Definitions() kernel.DefinitionRegistry
	Lister() kernel.InstanceLister
	TaskStore() humantask.TaskStore
	TimerStore() kernel.TimerStore
	CallLinkStore() kernel.CallLinkStore
}
```

- [ ] **Step 4: Add `WithDurableStore` to `service/options.go`.**

```go
// WithDurableStore flips the whole graph durable in one call, setting every
// leaf from the provider and rebuilding the driver from those leaves. Later
// per-leaf overrides in the option list still win (last-writer-wins). A nil
// provider is ignored; a provider that returns a nil required leaf surfaces as
// ErrNilDependency during NewEngine validation.
func WithDurableStore(p DurableProvider) Option {
	return func(c *engineConfig) {
		if p == nil {
			return
		}
		c.durable = true
		c.store = p.InstanceStore()
		c.reg = p.Definitions()
		c.lister = p.Lister()
		c.taskStore = p.TaskStore()
		c.timerStore = p.TimerStore()
		c.callLinkStore = p.CallLinkStore()
	}
}
```

> Precedence note: because `WithInstanceStore`/etc. ignore nils but overwrite non-nils, an override *after* `WithDurableStore` replaces that leaf; an override *before* is itself overwritten by the provider. This is the documented last-writer-wins in option order.

- [ ] **Step 5: Run to verify pass.**

Run: `go test ./service/...`
Expected: PASS.

- [ ] **Step 6: Commit.**

```bash
git add service/durable.go service/options.go service/durable_test.go
git commit -m "feat(service): WithDurableStore + DurableProvider interface (vendor-free)"
```

---

### Task 9: Migrate `httpcore` + admin endpoints + gin fake to the new service API

**Files:**
- Modify: `transport/http/httpcore/endpoints.go`
- Modify: `transport/http/httpcore/admin_endpoints.go`
- Modify: `transport/http/gin/gin_coverage_test.go` (remove `GetInstanceWithDefinition` from the hand-rolled fake)
- Test: existing `transport/http/**` tests must pass

**Interfaces:**
- Consumes: `service.Service`, `service.ProcessInstance`, `service.GetInstance` (now def-carrying), `view.NewActionableView` (kept).

**Key design:** the `mapper func(engine.InstanceState) any` seam is preserved by feeding it `pi.State()`. Only `GetInstanceSnapshot` returns the self-serializing `ProcessInstance` directly. `GetActionableView` keeps using `view.NewActionableView` but sources def+state from the returned `ProcessInstance`.

- [ ] **Step 1: Confirm the baseline red** by building the transport packages (they will fail to compile because `svc.GetInstanceWithDefinition` is gone and single-instance methods changed return type).

Run: `go build ./transport/...`
Expected: FAIL — `svc.GetInstanceWithDefinition undefined`, and assignment mismatches on `st, err := svc.StartInstance(...)`.

- [ ] **Step 2: Update `endpoints.go`.** For each single-instance endpoint, capture the `ProcessInstance` and feed `pi.State()` to the existing mapper. Replace the two snapshot/actionable functions:

```go
func GetInstanceSnapshot(ctx context.Context, svc service.Service, id string) (int, any, error) {
	pi, err := svc.GetInstance(ctx, id)
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, pi, nil // ProcessInstance self-serializes to the snapshot projection
}

func GetActionableView(ctx context.Context, svc service.Service, id string) (int, any, error) {
	pi, err := svc.GetInstance(ctx, id)
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, view.NewActionableView(pi.State(), pi.Definition()), nil
}
```

For `GetInstance`, `StartInstance`, `DeliverSignal`, `ClaimTask`, `CompleteTask`, `ReassignTask` — change `st, err := svc.X(...)` to `pi, err := svc.X(...)` and `mapInstance(mapper, st)` to `mapInstance(mapper, pi.State())`. The `mapInstance` helper and the `mapper func(engine.InstanceState) any` seam are UNCHANGED.

- [ ] **Step 3: Update `admin_endpoints.go`.** `ResolveIncident`/`CancelInstance` now return `ProcessInstance`: change `st, err := svc.ResolveIncident(...)` to `pi, err := ...` and `NewInstanceView(st)` to `NewInstanceView(pi.State())`. Any admin `GetInstance` handler similarly uses `pi.State()`.

- [ ] **Step 4: Fix the gin coverage fake.** In `transport/http/gin/gin_coverage_test.go`, the hand-rolled `errInstanceSvc` implements `service.Service`. Remove its `GetInstanceWithDefinition` method (line ~32) and change the signatures of the single-instance methods it stubs to return `(service.ProcessInstance, error)` — return `nil, sentinelErr` (or `service.NewProcessInstance(nil, engine.InstanceState{}), nil` for the success stub). Update the comment at line ~78.

- [ ] **Step 5: Run the transport tests to verify pass.**

Run: `go test ./transport/...`
Expected: PASS. If a test asserted the exact JSON body of the snapshot endpoint, it should still match (the projection is byte-identical to the old `view.NewInstanceSnapshot` — guarded by Task 5's parity test).

- [ ] **Step 6: Commit.**

```bash
git add transport/http/
git commit -m "refactor(transport)!: migrate httpcore to ProcessInstance + folded GetInstance

Snapshot endpoint returns the self-serializing service.ProcessInstance; the
engine.InstanceState mapper seam is preserved via pi.State()."
```

---

### Task 10: Retire the `runtime/view` full-snapshot path (keep `ActionableView`)

**Files:**
- Modify: `runtime/view/instance_snapshot.go` (delete `InstanceSnapshot`, `NewInstanceSnapshot`, `TokenView`, `NodeVisitView`, `IncidentView`, `TaskView`, `ActionBindingView`, `tokenStateString`; **keep** `StatusString` — used by `ActionableView`)
- Modify: `runtime/view/instance_snapshot_test.go` (delete tests for the removed symbols) or delete the file if it only tested the snapshot
- Modify: `service/instance_test.go` (delete `TestInstanceJSONMatchesLegacyViewSnapshot` — its `view.NewInstanceSnapshot` reference is now gone)
- Modify: `engine/state.go` (update the doc comment at ~line 492 that mentions `view.InstanceSnapshot` → `service.ProcessInstance`)

**Interfaces:**
- `runtime/view` retains only: `ActionableView`, `NewActionableView`, `ActionableTask`, `NextAction`, `StatusString`. Everything snapshot-related now lives (unexported) in `service`.

- [ ] **Step 1: Confirm nothing else references the snapshot symbols.**

Run: `grep -rn "view.NewInstanceSnapshot\|view.InstanceSnapshot\|view.TokenView\|view.TaskView\|view.NodeVisitView\|view.IncidentView\|view.ActionBindingView" --include=*.go .`
Expected: only `service/instance_test.go` (the parity test) and `runtime/view/instance_snapshot_test.go`. If any production file outside those appears, migrate it first (it should not, per the map).

- [ ] **Step 2: Delete the parity test** `TestInstanceJSONMatchesLegacyViewSnapshot` from `service/instance_test.go` and remove the now-unused `runtime/view` import from that test file.

- [ ] **Step 3: Delete the snapshot symbols** from `runtime/view/instance_snapshot.go`, keeping `StatusString` (move it to a small `runtime/view/status.go` if the file would otherwise be empty). Delete the corresponding tests from `runtime/view/instance_snapshot_test.go` (keep any `StatusString`/`ActionableView` tests; if the file is left empty, delete it).

- [ ] **Step 4: Update the `engine/state.go` doc comment** referencing `view.InstanceSnapshot` to point at `service.ProcessInstance`.

- [ ] **Step 5: Run to verify pass.**

Run: `go test ./runtime/view/... ./service/... ./engine/...`
Expected: PASS. Then confirm the whole tree still builds where already migrated:

Run: `go build ./runtime/... ./service/... ./transport/...`
Expected: builds clean.

- [ ] **Step 6: Commit.**

```bash
git add runtime/view/ service/instance_test.go engine/state.go
git commit -m "refactor(view): retire InstanceSnapshot (moved into service); keep ActionableView"
```

---

### Task 11: Migrate `internal/transporttest` harness + `examples/*` wiring

**Files:**
- Modify: `internal/transporttest/harness.go:94`
- Modify: `examples/production_wiring/main.go:179`
- Modify: `examples/sqlite_wiring/main.go:268`
- Modify: `examples/mysql_wiring/main.go:242`

**Interfaces:**
- Consumes: `service.NewEngine` + options (Task 6). For the durable examples, optionally `service.WithDurableStore(persistence.NewDurableProvider(...))` once Task 12 lands — but the minimal migration keeps the existing explicit leaves.

- [ ] **Step 1: Confirm the baseline red.**

Run: `go build ./internal/transporttest/... ./examples/...`
Expected: FAIL — `svc := service.New(...)` no longer exists.

- [ ] **Step 2: Migrate `internal/transporttest/harness.go`.** Replace:
```go
svc := service.New(runner, taskSvc, reg, store, store, taskStore, service.WithEngineClock(fc))
```
with:
```go
svc, err := service.NewEngine(
	service.WithProcessDriver(runner),
	service.WithInstanceStore(store),
	service.WithDefinitions(reg),
	service.WithLister(store),
	service.WithHumanTasks(taskStore, az),
	service.WithClock(fc),
)
if err != nil {
	panic(err) // test harness: fail loudly
}
```
(`az` is the authorizer already built in `NewHarness`; if the harness didn't retain it, reuse the same authorizer passed to `task.NewTaskService`. Since `NewEngine` builds the task service internally, the pre-built `taskSvc` is no longer passed — remove it if now unused, or keep constructing it only if other harness code references it.) The `NewHarness` return type `(*Harness, service.Service)` is unchanged.

- [ ] **Step 3: Migrate each example `main.go`.** Pattern (production_wiring shown; sqlite/mysql analogous, using their `cachingStore`/`lister`/`taskStore`):
```go
svc, err := service.NewEngine(
	service.WithProcessDriver(runner),
	service.WithInstanceStore(store),   // cachingStore in sqlite/mysql
	service.WithDefinitions(reg),
	service.WithLister(lister),
	service.WithHumanTasks(taskStore, az),
)
if err != nil {
	log.Fatalf("build engine: %v", err)
}
```
Keep the subsequent `stdlib.Mount(mux, svc)` unchanged.

> Optional (nice-to-have, not required): in `sqlite_wiring`/`mysql_wiring`, demonstrate the new one-call durable path by replacing the explicit leaves with `service.WithDurableStore(provider)` once Task 12 exists. Only do this if it keeps the example compiling and the README accurate; otherwise leave the explicit form.

- [ ] **Step 4: Run to verify pass.**

Run: `go build ./internal/transporttest/... ./examples/... && go test ./internal/transporttest/...`
Expected: builds clean; transporttest tests (and anything depending on the harness) pass.

- [ ] **Step 5: Full-tree build + transport tests.**

Run: `go build ./... && go test ./transport/...`
Expected: entire module builds; transport tests pass.

- [ ] **Step 6: Commit.**

```bash
git add internal/transporttest/ examples/
git commit -m "refactor(examples,transporttest)!: migrate to service.NewEngine"
```

---

### Task 12: `persistence.DurableProvider` (bridges the durable task store into `service`)

**Files:**
- Create: `persistence/durableprovider.go`
- Test: `persistence/durableprovider_test.go`

**Interfaces:**
- Consumes: `OpenPostgres`/`OpenMySQL`/`OpenSQLite`, `NewDefinitionStore`/`NewMySQLDefinitionStore`/`NewSQLiteDefinitionStore`, `NewLister`/`NewMySQLLister`/`NewSQLiteLister`, `NewTaskStore`/`NewMySQLTaskStore`/`NewSQLiteTaskStore` (Task 4), `NewTimerStore`/`NewMySQLTimerStore`/`NewSQLiteTimerStore`, `NewCallLinkStore`/`NewMySQLCallLinkStore`/`NewSQLiteCallLinkStore`.
- Produces:
  - `type DurableProvider struct { ... }` with methods `InstanceStore()/Definitions()/Lister()/TaskStore()/TimerStore()/CallLinkStore()` — satisfies `service.DurableProvider`.
  - `func NewDurableProvider(ctx context.Context, pool *pgxpool.Pool) (*DurableProvider, error)`
  - `func NewMySQLDurableProvider(ctx context.Context, db *sql.DB) (*DurableProvider, error)`
  - `func NewSQLiteDurableProvider(ctx context.Context, db *sql.DB) (*DurableProvider, error)`

- [ ] **Step 1: Write the failing test.** Create `persistence/durableprovider_test.go` (package `persistence_test`). Compile-time assert it satisfies `service.DurableProvider`; smoke-test the SQLite provider end-to-end (no Docker): build it, hand it to `service.NewEngine(service.WithDurableStore(p))`, and round-trip a start→get.

```go
package persistence_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	"github.com/zakyalvan/krtlwrkflw/persistence"
	"github.com/zakyalvan/krtlwrkflw/service"
)

var _ service.DurableProvider = (*persistence.DurableProvider)(nil)

func TestSQLiteDurableProviderPowersEngine(t *testing.T) {
	db := dbtest.RunTestSQLite(t)
	p, err := persistence.NewSQLiteDurableProvider(t.Context(), db)
	require.NoError(t, err)

	e, err := service.NewEngine(service.WithDurableStore(p))
	require.NoError(t, err)
	require.NotNil(t, e)
}
```

- [ ] **Step 2: Run to verify it fails.**

Run: `go test -run TestSQLiteDurableProvider ./persistence/...`
Expected: FAIL — `undefined: persistence.NewSQLiteDurableProvider` / `persistence.DurableProvider`.

- [ ] **Step 3: Implement `persistence/durableprovider.go`.**

```go
package persistence

import (
	"context"
	"database/sql"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// DurableProvider is a coherent set of durable graph leaves for one backend,
// suitable for service.WithDurableStore. Construct it with NewDurableProvider
// (Postgres), NewMySQLDurableProvider, or NewSQLiteDurableProvider.
type DurableProvider struct {
	instanceStore kernel.InstanceStore
	definitions   kernel.DefinitionRegistry
	lister        kernel.InstanceLister
	taskStore     humantask.TaskStore
	timerStore    kernel.TimerStore
	callLinkStore kernel.CallLinkStore
}

func (p *DurableProvider) InstanceStore() kernel.InstanceStore    { return p.instanceStore }
func (p *DurableProvider) Definitions() kernel.DefinitionRegistry { return p.definitions }
func (p *DurableProvider) Lister() kernel.InstanceLister          { return p.lister }
func (p *DurableProvider) TaskStore() humantask.TaskStore         { return p.taskStore }
func (p *DurableProvider) TimerStore() kernel.TimerStore          { return p.timerStore }
func (p *DurableProvider) CallLinkStore() kernel.CallLinkStore    { return p.callLinkStore }

// NewDurableProvider builds a PostgreSQL-backed provider. The schema must
// already be migrated (persistence.Migrate).
func NewDurableProvider(ctx context.Context, pool *pgxpool.Pool) (*DurableProvider, error) {
	is, err := OpenPostgres(ctx, pool)
	if err != nil {
		return nil, err
	}
	defs, err := NewDefinitionStore(pool)
	if err != nil {
		return nil, err
	}
	lister, err := NewLister(pool)
	if err != nil {
		return nil, err
	}
	tasks, err := NewTaskStore(pool)
	if err != nil {
		return nil, err
	}
	timers, err := NewTimerStore(pool)
	if err != nil {
		return nil, err
	}
	links, err := NewCallLinkStore(pool)
	if err != nil {
		return nil, err
	}
	return &DurableProvider{
		instanceStore: is, definitions: defs, lister: lister,
		taskStore: tasks, timerStore: timers, callLinkStore: links,
	}, nil
}

// NewMySQLDurableProvider builds a MySQL-backed provider.
func NewMySQLDurableProvider(ctx context.Context, db *sql.DB) (*DurableProvider, error) {
	is, err := OpenMySQL(ctx, db)
	if err != nil {
		return nil, err
	}
	defs, err := NewMySQLDefinitionStore(db)
	if err != nil {
		return nil, err
	}
	lister, err := NewMySQLLister(db)
	if err != nil {
		return nil, err
	}
	tasks, err := NewMySQLTaskStore(db)
	if err != nil {
		return nil, err
	}
	timers, err := NewMySQLTimerStore(db)
	if err != nil {
		return nil, err
	}
	links, err := NewMySQLCallLinkStore(db)
	if err != nil {
		return nil, err
	}
	return &DurableProvider{
		instanceStore: is, definitions: defs, lister: lister,
		taskStore: tasks, timerStore: timers, callLinkStore: links,
	}, nil
}

// NewSQLiteDurableProvider builds a SQLite-backed provider.
func NewSQLiteDurableProvider(ctx context.Context, db *sql.DB) (*DurableProvider, error) {
	is, err := OpenSQLite(ctx, db)
	if err != nil {
		return nil, err
	}
	defs, err := NewSQLiteDefinitionStore(db)
	if err != nil {
		return nil, err
	}
	lister, err := NewSQLiteLister(db)
	if err != nil {
		return nil, err
	}
	tasks, err := NewSQLiteTaskStore(db)
	if err != nil {
		return nil, err
	}
	timers, err := NewSQLiteTimerStore(db)
	if err != nil {
		return nil, err
	}
	links, err := NewSQLiteCallLinkStore(db)
	if err != nil {
		return nil, err
	}
	return &DurableProvider{
		instanceStore: is, definitions: defs, lister: lister,
		taskStore: tasks, timerStore: timers, callLinkStore: links,
	}, nil
}
```

> Confirm the exact constructor names against `persistence/*.go` (Task-4 map): PG definition store is `NewDefinitionStore`, MySQL `NewMySQLDefinitionStore`, SQLite `NewSQLiteDefinitionStore`; listers `NewLister`/`NewMySQLLister`/`NewSQLiteLister`; timers `NewTimerStore`/`NewMySQLTimerStore`/`NewSQLiteTimerStore`; call links `NewCallLinkStore`/`NewMySQLCallLinkStore`/`NewSQLiteCallLinkStore`. Adjust if any differ.

- [ ] **Step 4: Run to verify pass.**

Run: `go test -run TestSQLiteDurableProvider ./persistence/...`
Expected: PASS (the compile-time assert also confirms `service.DurableProvider` is satisfied). Note this introduces a `persistence → service` import; verify no cycle:

Run: `go build ./...`
Expected: builds (service does not import persistence, so no cycle).

- [ ] **Step 5: Commit.**

```bash
git add persistence/durableprovider.go persistence/durableprovider_test.go
git commit -m "feat(persistence): DurableProvider (PG/MySQL/SQLite) for service.WithDurableStore"
```

---

### Task 13: Vendor-free invariant test for `service`

**Files:**
- Create: `service/vendorfree_test.go`

**Interfaces:** none (pure guard).

- [ ] **Step 1: Write the test.** Assert `go list -deps ./service` pulls no DB driver, no `database/sql`, no `persistence`.

```go
package service_test

import (
	"os/exec"
	"strings"
	"testing"
)

func TestServiceDependencyGraphIsVendorFree(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps",
		"github.com/zakyalvan/krtlwrkflw/service").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}
	deps := string(out)
	banned := []string{
		"github.com/jackc/pgx",
		"github.com/go-sql-driver/mysql",
		"modernc.org/sqlite",
		"database/sql",
		"github.com/zakyalvan/krtlwrkflw/persistence",
		"github.com/zakyalvan/krtlwrkflw/internal/persistence",
	}
	for _, b := range banned {
		for _, line := range strings.Split(deps, "\n") {
			if strings.TrimSpace(line) == b {
				t.Errorf("service must be vendor-free but depends on %q", b)
			}
		}
	}
}
```

(Match exact package paths per line — comparing full lines avoids false hits like `database/sql/driver` appearing via an unrelated dep; if `database/sql` legitimately must be tolerated because a *transitive std* dep pulls it, tighten to the module-owned `persistence` paths + the three drivers. Verify by running `go list -deps ./service | grep -E 'pgx|sql|persistence'` first.)

- [ ] **Step 2: Run to verify it passes now** (the graph is already clean; this locks it).

Run: `go test -run TestServiceDependencyGraphIsVendorFree ./service/...`
Expected: PASS. If it FAILS, a prior task accidentally imported a driver into `service` — fix that import (the durable path must go through the `DurableProvider` interface, never a concrete persistence type in `service`).

- [ ] **Step 3: Commit.**

```bash
git add service/vendorfree_test.go
git commit -m "test(service): assert service dependency graph stays DB-vendor-free"
```

---

### Task 14: ADR-0098 (Nygard template)

**Files:**
- Create: `docs/adr/0098-service-coherent-graph-refactor.md`

- [ ] **Step 1: Write the ADR** using the Nygard template (Status/Date, Context, Decision, Consequences), modeled on `docs/adr/0001-record-architecture-decisions.md`. Cover all six decisions (D1 coherent-graph `NewEngine`; D2 role-interface segregation; D3 self-serializing `ProcessInstance` with hidden DTO; D4 vendor-free `WithDurableStore`/`DurableProvider`; D5 allow-all default authorizer; D6 durable SQL `humantask.TaskStore`), the vendor-free invariant, and the breaking-change consequences (removal of `service.New` and `GetInstanceWithDefinition`, retirement of `view.InstanceSnapshot`).

- [ ] **Step 2: Commit.**

```bash
git add docs/adr/0098-service-coherent-graph-refactor.md
git commit -m "docs(adr): 0098 service coherent-graph refactor"
```

---

### Task 15: Full verification pass

**Files:** none (verification + any straggler fixes).

- [ ] **Step 1: Full build.**

Run: `go build ./...`
Expected: clean.

- [ ] **Step 2: Full test suite with race.**

Run: `go test -race ./...`
Expected: all green (Docker up for testcontainers legs). Fix any straggler — likely spots: a missed `service.New` callsite, a test reading a `ProcessInstance` return as `engine.InstanceState`, or a stale `view.InstanceSnapshot` reference.

- [ ] **Step 3: Coverage on touched packages.**

Run: `go test -race -coverprofile=cover.out ./service/... ./persistence/... ./internal/persistence/store/... ./internal/persistence/dialect/... && go tool cover -func=cover.out | tail -1`
Expected: each ≥ 85% (run per-package if the combined tail is ambiguous).

- [ ] **Step 4: Lint.**

Run: `golangci-lint run ./...`
Expected: clean. Address any findings (unused imports from the migration, etc.).

- [ ] **Step 5: Extraction guard still green** (ensure the new store code didn't leak into `internal/database`).

Run: `scripts/check-extraction.sh`
Expected: `OK: internal/database imports only ...`.

- [ ] **Step 6: Self-audit the TDD trail.** Confirm every new symbol had an observable red state in the transcript (per CLAUDE.md self-audit). Then final commit if any straggler fixes were made:

```bash
git add -A
git commit -m "chore(service): final verification fixes for coherent-graph refactor"
```

---

## Self-Review

**Spec coverage (checklist ↔ task):**
- `NewEngine(opts...) (*Engine, error)` + in-mem coherent defaults + fail-fast → **Task 6**.
- `service.ErrNilDependency` sentinel → **Task 6**.
- DEBUG construction summary → **Task 6** (`logConstructionSummary`).
- 5 role interfaces + composed `Service` + `*Engine` satisfies all → **Task 6** (defs) + **Task 7** (asserts).
- `ProcessInstance` interface + `json.Marshaler` + unexported DTO + `NewProcessInstance` → **Task 5**.
- `GetInstanceWithDefinition` removed; single-instance methods return `ProcessInstance` → **Task 6**.
- `WithDurableStore(DurableProvider)` + leaf-override options → **Task 8** (+ options in Task 6).
- Durable `humantask.TaskStore` (D6): table + per-dialect DDL + parity + neutral store + facade + 3-dialect conformance → **Tasks 1, 2, 3, 4**.
- `persistence.NewDurableProvider` (PG/MySQL/SQLite) wiring the durable task store → **Task 12**.
- Vendor-free `go list -deps ./service` test → **Task 13**.
- All impacted call sites migrated (transports, examples, transporttest, tests) → **Tasks 6, 9, 11**.
- Retire the snapshot path in `runtime/view` (keep `ActionableView`) → **Task 10**.
- ADR-0098 → **Task 14**.
- `go test ./...` green, coverage ≥ 85%, lint clean → **Task 15**.

**Type consistency:** `ProcessInstance`/`NewProcessInstance` (Task 5) are used unchanged in Tasks 6, 8, 9, 12. `DurableProvider` interface (Task 8) matches the concrete `persistence.DurableProvider` methods (Task 12) exactly. `NewHumanTaskStore(conn any, d dialect.Dialect)` (Task 3) matches the facade calls (Task 4). `UpsertTask()` (Task 1) is consumed by the store's `Upsert` SQL (Task 3).

**Open items deferred (per spec):** ActionableView remains a transport/view concern (not folded into service); optional hot-path cache for `TaskStore.ClaimableBy` deferred (ADR-0073); `runtime/view` is trimmed, not deleted (ActionableView keeps it alive).

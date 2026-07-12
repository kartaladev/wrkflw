# DB-backed casbin policy adapter + watcher — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Load/save casbin policy from a PostgreSQL `casbin_rule` table via a pgx-native `persist.Adapter`, and propagate policy changes across nodes via a LISTEN/NOTIFY `persist.Watcher`, behind the `casbinauthz` façade.

**Architecture:** A hand-rolled `pgAdapter` and `pgWatcher` live in the casbin-confined `internal/authz/casbin/` package (they may import casbin `persist`/`model` + pgx). An opt-in goose migration on a separate version table creates `casbin_rule`. The façade `casbinauthz.NewCasbinAuthorizerFromDB` builds a `SyncedEnforcer` over the adapter, wires the watcher, and returns the stable `authz.Authorizer` + an `io.Closer`.

**Tech Stack:** Go 1.25, casbin v2.135.0 (pinned, no adapter dep), pgx v5 + pgxpool, goose v3 (Provider API, `WithTableName`), embed.FS, testcontainers-go Postgres 17.

## Global Constraints

- **Go 1.25**; module `github.com/kartaladev/wrkflw`.
- **casbin confinement:** casbin (`github.com/casbin/casbin/v2` + `.../model`, `.../persist`) is imported ONLY in `casbinauthz/` and `internal/authz/casbin/`. Do NOT import casbin from `engine`/`model`/`runtime`/`internal/persistence/*`.
- **No new ORM / driver:** do NOT add `gorm`, `go-pg`, `sqlx`, `ent`, or any `casbin-*-adapter` to `go.mod`. The adapter is hand-rolled over `*pgxpool.Pool`. casbin stays pinned at **v2.135.0**.
- **Façade discipline (ADR-0008/0010):** the public constructor returns `authz.Authorizer` + `io.Closer`, never the internal concrete types.
- **TDD STRICT:** every new symbol gets a failing test with a visible RED (`go test`) before implementation. Capture RED/GREEN in reports.
- **Tests:** black-box (`package <pkg>_test`); table-driven with an **`assert` closure per case** (project `table-test` skill — NOT want/wantErr); `t.Context()` not `context.Background()`; pair each `foo.go` with `foo_test.go`. Postgres tests use `database.RunTestDatabase(t)` and run with `-p 1` (`go test -p 1 ./...`); Docker required.
- **Lint:** `golangci-lint` v2; `golangci-lint run ./...` clean.
- **Coverage:** ≥85% line coverage on `casbinauthz` and `internal/authz/casbin`.
- **Commits:** Conventional Commits scoped `authz`, ending with `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`. Commit per task.

## Confirmed casbin v2.135.0 API (from the module cache — use exactly these)

- `persist.Adapter`: `LoadPolicy(model model.Model) error`, `SavePolicy(model model.Model) error`, `AddPolicy(sec, ptype string, rule []string) error`, `RemovePolicy(sec, ptype string, rule []string) error`, `RemoveFilteredPolicy(sec, ptype string, fieldIndex int, fieldValues ...string) error`.
- Load helper: `persist.LoadPolicyArray(rule []string, m model.Model) error` (feeds a `[ptype, v0, v1, …]` slice into the model).
- `persist.Watcher`: `SetUpdateCallback(func(string)) error`, `Update() error`, `Close()`.
- `casbin.NewSyncedEnforcer(params ...interface{}) (*SyncedEnforcer, error)` — pass `(model, adapter)`. `(*SyncedEnforcer).SetWatcher(persist.Watcher) error`.
- `model.NewModelFromString(text string) (model.Model, error)`.
- The model is `model.Model = map[string]model.AssertionMap`; `model["p"]`/`model["g"]` map a ptype → `*model.Assertion` whose `.Policy` is `[][]string` (the stored rules). **Confirm-point:** verify `Assertion.Policy` field name against v2.135.0 before writing `SavePolicy`.

---

### Task 1: `casbin_rule` migration + `MigrateCasbin`

**Files:**
- Create: `internal/authz/casbin/migrations/0001_casbin_rule.sql`
- Create: `internal/authz/casbin/migrate.go`
- Modify: `casbinauthz/casbinauthz.go` (add `MigrateCasbin`)
- Test: `internal/authz/casbin/migrate_test.go`

**Interfaces:**
- Produces: `casbin.MigrateCasbin(ctx context.Context, pool *pgxpool.Pool) error` (internal, package `casbin`); `casbinauthz.MigrateCasbin(ctx context.Context, pool *pgxpool.Pool) error` (façade). Creates table `casbin_rule`, tracked in `casbin_goose_db_version`.

- [ ] **Step 1: Write the migration SQL**

Create `internal/authz/casbin/migrations/0001_casbin_rule.sql`:

```sql
-- +goose Up
CREATE TABLE casbin_rule (
    id    BIGSERIAL PRIMARY KEY,
    ptype TEXT NOT NULL,
    v0    TEXT NOT NULL DEFAULT '',
    v1    TEXT NOT NULL DEFAULT '',
    v2    TEXT NOT NULL DEFAULT '',
    v3    TEXT NOT NULL DEFAULT '',
    v4    TEXT NOT NULL DEFAULT '',
    v5    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX casbin_rule_ptype_idx ON casbin_rule (ptype);

-- +goose Down
DROP TABLE casbin_rule;
```

- [ ] **Step 2: Write the failing test**

Create `internal/authz/casbin/migrate_test.go`:

```go
package casbin_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/database"
	authzcasbin "github.com/kartaladev/wrkflw/internal/authz/casbin"
)

func TestMigrateCasbinCreatesRuleTable(t *testing.T) {
	pool := database.RunTestDatabase(t)

	require.NoError(t, authzcasbin.MigrateCasbin(t.Context(), pool))
	// Idempotent: a second run is a no-op.
	require.NoError(t, authzcasbin.MigrateCasbin(t.Context(), pool))

	var exists bool
	require.NoError(t, pool.QueryRow(t.Context(),
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'casbin_rule')`,
	).Scan(&exists))
	assert.True(t, exists, "casbin_rule table must exist after MigrateCasbin")
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test -p 1 ./internal/authz/casbin/... -run TestMigrateCasbin`
Expected: FAIL — `undefined: authzcasbin.MigrateCasbin`.

- [ ] **Step 4: Write the migrate helper**

Create `internal/authz/casbin/migrate.go` (mirrors `internal/persistence/postgres/migrate.go`, but with a SEPARATE goose version table so it never collides with the persistence migration set):

```go
package casbin

import (
	"context"
	"embed"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// casbinVersionTable keeps the casbin migration bookkeeping independent of the
// persistence migration set (which uses the default goose_db_version table), so
// the two migration sets can run against the same database without interfering.
const casbinVersionTable = "casbin_goose_db_version"

// MigrateCasbin applies the embedded casbin_rule migration to the database
// reachable through pool. It is idempotent and tracked in casbin_goose_db_version.
// It must be called explicitly before NewCasbinAuthorizerFromDB; it is never
// auto-run on import.
func MigrateCasbin(ctx context.Context, pool *pgxpool.Pool) error {
	db := stdlib.OpenDBFromPool(pool)
	defer func() { _ = db.Close() }()

	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("casbin: migrate: sub fs: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, sub,
		goose.WithTableName(casbinVersionTable))
	if err != nil {
		return fmt.Errorf("casbin: migrate: new provider: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("casbin: migrate: up: %w", err)
	}
	return nil
}
```

- [ ] **Step 5: Add the façade passthrough**

In `casbinauthz/casbinauthz.go`, add the import `internalcasbin "github.com/kartaladev/wrkflw/internal/authz/casbin"` (if not already imported under that or another alias — match the existing alias) and `"github.com/jackc/pgx/v5/pgxpool"`, then:

```go
// MigrateCasbin applies the casbin_rule schema to pool (tracked in its own
// casbin_goose_db_version table, independent of persistence.Migrate). Call it
// before NewCasbinAuthorizerFromDB. Never auto-run on import.
func MigrateCasbin(ctx context.Context, pool *pgxpool.Pool) error {
	return internalcasbin.MigrateCasbin(ctx, pool)
}
```

- [ ] **Step 6: Run the test to verify it passes**

Run: `go test -p 1 ./internal/authz/casbin/... -run TestMigrateCasbin`
Run: `go test ./casbinauthz/...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/authz/casbin/migrations/0001_casbin_rule.sql internal/authz/casbin/migrate.go casbinauthz/casbinauthz.go internal/authz/casbin/migrate_test.go
git commit -m "$(printf 'feat(authz): casbin_rule migration + MigrateCasbin (separate version table) (ADR-0023)\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 2: `pgAdapter` — LoadPolicy + SavePolicy

**Files:**
- Create: `internal/authz/casbin/pg_adapter.go`
- Test: `internal/authz/casbin/pg_adapter_test.go`

**Interfaces:**
- Consumes: `MigrateCasbin` (Task 1); casbin `persist`/`model`.
- Produces: `casbin.newPGAdapter(pool *pgxpool.Pool) *pgAdapter` (unexported); `pgAdapter` implements `persist.Adapter`. Exposed to tests via an `export_test.go` seam `var NewPGAdapter = newPGAdapter` (returns `persist.Adapter`).

- [ ] **Step 1: Add the test seam**

Create `internal/authz/casbin/export_test.go`:

```go
package casbin

import (
	"github.com/casbin/casbin/v2/persist"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPGAdapter exposes the unexported pgAdapter constructor for black-box tests.
func NewPGAdapter(pool *pgxpool.Pool) persist.Adapter { return newPGAdapter(pool) }
```

- [ ] **Step 2: Write the failing test**

Create `internal/authz/casbin/pg_adapter_test.go`:

```go
package casbin_test

import (
	"testing"

	"github.com/casbin/casbin/v2/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/database"
	authzcasbin "github.com/kartaladev/wrkflw/internal/authz/casbin"
)

// rbacModel is a minimal model for adapter round-trip tests.
const rbacModel = `
[request_definition]
r = sub, obj, act
[policy_definition]
p = sub, obj, act
[role_definition]
g = _, _
[policy_effect]
e = some(where (p.eft == allow))
[matchers]
m = g(r.sub, p.sub) && r.obj == p.obj && r.act == p.act
`

func TestPGAdapterSaveLoadRoundTrip(t *testing.T) {
	pool := database.RunTestDatabase(t)
	require.NoError(t, authzcasbin.MigrateCasbin(t.Context(), pool))

	a := authzcasbin.NewPGAdapter(pool)

	// Build a model holding two p rules and one g rule, then SavePolicy.
	m, err := model.NewModelFromString(rbacModel)
	require.NoError(t, err)
	m.AddPolicy("p", "p", []string{"admin", "data1", "read"})
	m.AddPolicy("p", "p", []string{"admin", "data1", "write"})
	m.AddPolicy("g", "g", []string{"alice", "admin"})
	require.NoError(t, a.SavePolicy(m))

	// Load into a FRESH model and assert the rules round-tripped.
	m2, err := model.NewModelFromString(rbacModel)
	require.NoError(t, err)
	require.NoError(t, a.LoadPolicy(m2))

	assert.True(t, m2.HasPolicy("p", "p", []string{"admin", "data1", "read"}))
	assert.True(t, m2.HasPolicy("p", "p", []string{"admin", "data1", "write"}))
	assert.True(t, m2.HasPolicy("g", "g", []string{"alice", "admin"}))
}
```

> Confirm-point: `model.Model.AddPolicy(sec, ptype, rule)` / `HasPolicy(sec, ptype, rule)` exist in v2.135.0 (they do — used widely). If the exact names differ, use the enforcer-level API in the test instead (build a `casbin.NewEnforcer(m, a)` and assert `GetPolicy()`), but keep the adapter SUT.

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test -p 1 ./internal/authz/casbin/... -run TestPGAdapterSaveLoad`
Expected: FAIL — `undefined: newPGAdapter`.

- [ ] **Step 4: Write the adapter (LoadPolicy + SavePolicy + helpers)**

Create `internal/authz/casbin/pg_adapter.go`:

```go
package casbin

import (
	"context"
	"fmt"

	"github.com/casbin/casbin/v2/model"
	"github.com/casbin/casbin/v2/persist"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Compile-time assertion: pgAdapter satisfies the casbin persist.Adapter.
var _ persist.Adapter = (*pgAdapter)(nil)

// pgAdapter is a pgx-native casbin persist.Adapter over the casbin_rule table.
// It holds the policy rows as (ptype, v0..v5); unused rule fields are stored as
// empty strings. It supports casbin's Auto-Save feature (AddPolicy/RemovePolicy/
// RemoveFilteredPolicy).
type pgAdapter struct {
	pool *pgxpool.Pool
}

func newPGAdapter(pool *pgxpool.Pool) *pgAdapter { return &pgAdapter{pool: pool} }

// padRule maps a casbin rule slice to the six fixed v0..v5 columns, padding
// missing trailing fields with empty strings. A rule longer than 6 fields is
// truncated to 6 (casbin_rule has v0..v5); this matches the conventional schema.
func padRule(rule []string) [6]string {
	var v [6]string
	for i := 0; i < len(rule) && i < 6; i++ {
		v[i] = rule[i]
	}
	return v
}

// ruleFromCols rebuilds the casbin rule slice from ptype + v0..v5, trimming
// trailing empty fields (so a 3-field rule round-trips as 3 fields, not 6).
func ruleFromCols(v [6]string) []string {
	n := 6
	for n > 0 && v[n-1] == "" {
		n--
	}
	return append([]string(nil), v[:n]...)
}

// LoadPolicy loads every rule from casbin_rule into the model.
func (a *pgAdapter) LoadPolicy(m model.Model) error {
	ctx := context.Background()
	rows, err := a.pool.Query(ctx,
		`SELECT ptype, v0, v1, v2, v3, v4, v5 FROM casbin_rule ORDER BY id`)
	if err != nil {
		return fmt.Errorf("casbin pgadapter: load: query: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var ptype string
		var v [6]string
		if err := rows.Scan(&ptype, &v[0], &v[1], &v[2], &v[3], &v[4], &v[5]); err != nil {
			return fmt.Errorf("casbin pgadapter: load: scan: %w", err)
		}
		rule := append([]string{ptype}, ruleFromCols(v)...)
		if err := persist.LoadPolicyArray(rule, m); err != nil {
			return fmt.Errorf("casbin pgadapter: load: apply: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("casbin pgadapter: load: rows: %w", err)
	}
	return nil
}

// SavePolicy replaces all stored rules with the model's current p and g lines
// in one transaction (DELETE-all then bulk insert).
func (a *pgAdapter) SavePolicy(m model.Model) error {
	ctx := context.Background()
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("casbin pgadapter: save: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `DELETE FROM casbin_rule`); err != nil {
		return fmt.Errorf("casbin pgadapter: save: delete: %w", err)
	}

	batch := &pgx.Batch{}
	queued := 0
	for _, sec := range []string{"p", "g"} {
		ast := m[sec]
		for ptype, assertion := range ast {
			for _, rule := range assertion.Policy {
				v := padRule(rule)
				batch.Queue(
					`INSERT INTO casbin_rule (ptype, v0, v1, v2, v3, v4, v5) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
					ptype, v[0], v[1], v[2], v[3], v[4], v[5])
				queued++
			}
		}
	}
	if queued > 0 {
		br := tx.SendBatch(ctx, batch)
		for i := 0; i < queued; i++ {
			if _, err := br.Exec(); err != nil {
				_ = br.Close()
				return fmt.Errorf("casbin pgadapter: save: insert: %w", err)
			}
		}
		if err := br.Close(); err != nil {
			return fmt.Errorf("casbin pgadapter: save: batch close: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("casbin pgadapter: save: commit: %w", err)
	}
	return nil
}

// AddPolicy, RemovePolicy, RemoveFilteredPolicy are added in Task 3 (auto-save).
```

> Confirm-point: `m[sec]` is `model.AssertionMap`; `assertion.Policy` is `[][]string`. Verify the field name (`Policy`) against v2.135.0 `model/assertion.go`; if it differs (e.g. `PolicyMap`), adjust the iteration accordingly — the SUT logic is unchanged.

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test -p 1 ./internal/authz/casbin/... -run TestPGAdapterSaveLoad`
Expected: PASS — all three rules round-trip.

> Note: this leaves `pgAdapter` not yet satisfying `persist.Adapter` (Add/Remove/RemoveFiltered missing) — the compile-time assertion `var _ persist.Adapter = (*pgAdapter)(nil)` will FAIL TO COMPILE until Task 3. To keep Task 2 self-contained and green, TEMPORARILY comment out the three missing methods' absence by adding stub methods returning `errors.New("not implemented (Task 3)")` now, OR move the `var _ persist.Adapter` assertion to Task 3. **Chosen: add the three methods as stubs returning a not-implemented error in Task 2 so the assertion holds**, then replace the stubs with real implementations in Task 3. Add to `pg_adapter.go`:

```go
import "errors"

var errNotImpl = errors.New("casbin pgadapter: auto-save method implemented in Task 3")

func (a *pgAdapter) AddPolicy(string, string, []string) error              { return errNotImpl }
func (a *pgAdapter) RemovePolicy(string, string, []string) error           { return errNotImpl }
func (a *pgAdapter) RemoveFilteredPolicy(string, string, int, ...string) error { return errNotImpl }
```

Re-run Step 5 after adding the stubs; PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/authz/casbin/pg_adapter.go internal/authz/casbin/pg_adapter_test.go internal/authz/casbin/export_test.go
git commit -m "$(printf 'feat(authz): pgAdapter LoadPolicy/SavePolicy over casbin_rule (ADR-0023)\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 3: `pgAdapter` auto-save — AddPolicy / RemovePolicy / RemoveFilteredPolicy

**Files:**
- Modify: `internal/authz/casbin/pg_adapter.go` (replace the Task-2 stubs)
- Test: `internal/authz/casbin/pg_adapter_test.go` (extend)

**Interfaces:**
- Consumes: `pgAdapter`, `padRule` (Task 2).
- Produces: working `AddPolicy`/`RemovePolicy`/`RemoveFilteredPolicy`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/authz/casbin/pg_adapter_test.go`:

```go
func TestPGAdapterAutoSaveMutations(t *testing.T) {
	pool := database.RunTestDatabase(t)
	require.NoError(t, authzcasbin.MigrateCasbin(t.Context(), pool))
	a := authzcasbin.NewPGAdapter(pool)

	// AddPolicy persists.
	require.NoError(t, a.AddPolicy("p", "p", []string{"admin", "data1", "read"}))
	require.NoError(t, a.AddPolicy("p", "p", []string{"admin", "data2", "read"}))
	require.NoError(t, a.AddPolicy("p", "p", []string{"viewer", "data1", "read"}))

	load := func() *model.Model {
		m, err := model.NewModelFromString(rbacModel)
		require.NoError(t, err)
		require.NoError(t, a.LoadPolicy(m))
		return &m
	}
	m := *load()
	assert.True(t, m.HasPolicy("p", "p", []string{"admin", "data1", "read"}))
	assert.True(t, m.HasPolicy("p", "p", []string{"viewer", "data1", "read"}))

	// RemovePolicy deletes exactly that rule.
	require.NoError(t, a.RemovePolicy("p", "p", []string{"admin", "data1", "read"}))
	m = *load()
	assert.False(t, m.HasPolicy("p", "p", []string{"admin", "data1", "read"}))
	assert.True(t, m.HasPolicy("p", "p", []string{"admin", "data2", "read"}))

	// RemoveFilteredPolicy(fieldIndex=0, "viewer") removes all rules whose v0=viewer.
	require.NoError(t, a.RemoveFilteredPolicy("p", "p", 0, "viewer"))
	m = *load()
	assert.False(t, m.HasPolicy("p", "p", []string{"viewer", "data1", "read"}))
	assert.True(t, m.HasPolicy("p", "p", []string{"admin", "data2", "read"}))
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -p 1 ./internal/authz/casbin/... -run TestPGAdapterAutoSave`
Expected: FAIL — the stub methods return `errNotImpl`, so the first `AddPolicy` errors.

- [ ] **Step 3: Replace the stubs with real implementations**

In `internal/authz/casbin/pg_adapter.go`, remove `errNotImpl` and the three stub methods, and add:

```go
// AddPolicy inserts one rule (Auto-Save).
func (a *pgAdapter) AddPolicy(_ string, ptype string, rule []string) error {
	v := padRule(rule)
	if _, err := a.pool.Exec(context.Background(),
		`INSERT INTO casbin_rule (ptype, v0, v1, v2, v3, v4, v5) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		ptype, v[0], v[1], v[2], v[3], v[4], v[5]); err != nil {
		return fmt.Errorf("casbin pgadapter: add: %w", err)
	}
	return nil
}

// RemovePolicy deletes rows matching the exact rule (Auto-Save).
func (a *pgAdapter) RemovePolicy(_ string, ptype string, rule []string) error {
	v := padRule(rule)
	if _, err := a.pool.Exec(context.Background(),
		`DELETE FROM casbin_rule
		  WHERE ptype=$1 AND v0=$2 AND v1=$3 AND v2=$4 AND v3=$5 AND v4=$6 AND v5=$7`,
		ptype, v[0], v[1], v[2], v[3], v[4], v[5]); err != nil {
		return fmt.Errorf("casbin pgadapter: remove: %w", err)
	}
	return nil
}

// RemoveFilteredPolicy deletes rows matching ptype plus the provided non-empty
// filter fields starting at fieldIndex (Auto-Save). Empty filter values are
// treated as "don't care" (casbin semantics).
func (a *pgAdapter) RemoveFilteredPolicy(_ string, ptype string, fieldIndex int, fieldValues ...string) error {
	args := []any{ptype}
	where := "ptype = $1"
	col := 2
	for i, val := range fieldValues {
		if val == "" {
			continue // skip don't-care slots
		}
		idx := fieldIndex + i
		if idx < 0 || idx > 5 {
			continue
		}
		where += fmt.Sprintf(" AND v%d = $%d", idx, col)
		args = append(args, val)
		col++
	}
	if _, err := a.pool.Exec(context.Background(),
		`DELETE FROM casbin_rule WHERE `+where, args...); err != nil {
		return fmt.Errorf("casbin pgadapter: remove filtered: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -p 1 ./internal/authz/casbin/... -run TestPGAdapter`
Expected: PASS (both round-trip and auto-save tests).

- [ ] **Step 5: Commit**

```bash
git add internal/authz/casbin/pg_adapter.go internal/authz/casbin/pg_adapter_test.go
git commit -m "$(printf 'feat(authz): pgAdapter auto-save Add/Remove/RemoveFiltered policy (ADR-0023)\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 4: `pgWatcher` — LISTEN/NOTIFY policy-change watcher

**Files:**
- Create: `internal/authz/casbin/pg_watcher.go`
- Test: `internal/authz/casbin/pg_watcher_test.go`
- Modify: `internal/authz/casbin/export_test.go` (add a watcher seam)

**Interfaces:**
- Consumes: casbin `persist.Watcher`; the LISTEN/NOTIFY mechanics pattern from `internal/persistence/postgres/relay.go`.
- Produces: `casbin.newPGWatcher(pool *pgxpool.Pool, channel, nodeID string) *pgWatcher` (unexported); implements `persist.Watcher`. Test seam `var NewPGWatcher = func(pool, channel, nodeID) persist.Watcher { return newPGWatcher(...) }`.

- [ ] **Step 1: Add the watcher test seam**

Append to `internal/authz/casbin/export_test.go`:

```go
// NewPGWatcher exposes the unexported pgWatcher constructor for black-box tests.
func NewPGWatcher(pool *pgxpool.Pool, channel, nodeID string) persist.Watcher {
	return newPGWatcher(pool, channel, nodeID)
}
```

- [ ] **Step 2: Write the failing test**

Create `internal/authz/casbin/pg_watcher_test.go`:

```go
package casbin_test

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/database"
	authzcasbin "github.com/kartaladev/wrkflw/internal/authz/casbin"
)

func TestPGWatcherNotifiesOtherNodesNotSelf(t *testing.T) {
	pool := database.RunTestDatabase(t)
	const channel = "wrkflw_casbin_policy_test"

	// Node A (the writer) and Node B (the observer), each with a watcher.
	wa := authzcasbin.NewPGWatcher(pool, channel, "node-A")
	defer wa.Close()
	wb := authzcasbin.NewPGWatcher(pool, channel, "node-B")
	defer wb.Close()

	var aCb, bCb atomic.Int64
	require.NoError(t, wa.SetUpdateCallback(func(string) { aCb.Add(1) }))
	require.NoError(t, wb.SetUpdateCallback(func(string) { bCb.Add(1) }))

	// Let both listeners establish their LISTEN before A notifies.
	time.Sleep(300 * time.Millisecond)

	// A signals a policy change (payload = "node-A").
	require.NoError(t, wa.Update())

	// B must observe it (payload "node-A" != "node-B"); A must NOT (self-filter).
	require.Eventually(t, func() bool { return bCb.Load() == 1 }, 5*time.Second, 25*time.Millisecond,
		"node B must reload on node A's policy change")
	assert.Equal(t, int64(0), aCb.Load(), "node A must not reload on its own change")
}
```

> Flakiness note: the 300ms establish-sleep mirrors the relay LISTEN test; if it proves racy on slow CI, poll for listener readiness instead. Run this test 3× before committing.

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test -p 1 ./internal/authz/casbin/... -run TestPGWatcher`
Expected: FAIL — `undefined: newPGWatcher`.

- [ ] **Step 4: Write the watcher**

Create `internal/authz/casbin/pg_watcher.go`:

```go
package casbin

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/casbin/casbin/v2/persist"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Compile-time assertion: pgWatcher satisfies casbin persist.Watcher.
var _ persist.Watcher = (*pgWatcher)(nil)

const watcherReconnectDelay = time.Second

// pgWatcher is a casbin persist.Watcher backed by Postgres LISTEN/NOTIFY
// (ADR-0023, reusing the ADR-0022 mechanics). Update() emits a NOTIFY carrying
// this node's id; a listener goroutine invokes the update callback for every
// notification whose payload differs from this node's id (so a node ignores the
// echo of its own write).
type pgWatcher struct {
	pool    *pgxpool.Pool
	channel string
	nodeID  string

	mu       sync.Mutex
	callback func(string)

	cancel context.CancelFunc
	done   chan struct{}
}

func newPGWatcher(pool *pgxpool.Pool, channel, nodeID string) *pgWatcher {
	ctx, cancel := context.WithCancel(context.Background())
	w := &pgWatcher{
		pool:    pool,
		channel: channel,
		nodeID:  nodeID,
		cancel:  cancel,
		done:    make(chan struct{}),
	}
	go w.listen(ctx)
	return w
}

// SetUpdateCallback stores the callback invoked when another node changes policy.
func (w *pgWatcher) SetUpdateCallback(cb func(string)) error {
	w.mu.Lock()
	w.callback = cb
	w.mu.Unlock()
	return nil
}

// Update notifies other nodes that this node changed the policy. The payload is
// this node's id so peers can ignore their own echo.
func (w *pgWatcher) Update() error {
	if _, err := w.pool.Exec(context.Background(),
		`SELECT pg_notify($1, $2)`, w.channel, w.nodeID); err != nil {
		return fmt.Errorf("casbin pgwatcher: notify: %w", err)
	}
	return nil
}

// Close stops the listener goroutine and waits for it to exit.
func (w *pgWatcher) Close() {
	w.cancel()
	<-w.done
}

// listen holds a dedicated connection, LISTENs on the channel, and invokes the
// callback for notifications from other nodes. It reconnects on transient
// failure with a cancellable backoff and exits on ctx cancel.
func (w *pgWatcher) listen(ctx context.Context) {
	defer close(w.done)
	for ctx.Err() == nil {
		conn, err := w.pool.Acquire(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			w.backoff(ctx)
			continue
		}
		if _, err := conn.Exec(ctx, "LISTEN "+w.channel); err != nil {
			conn.Release()
			if ctx.Err() != nil {
				return
			}
			w.backoff(ctx)
			continue
		}
		for ctx.Err() == nil {
			n, err := conn.Conn().WaitForNotification(ctx)
			if err != nil {
				break // conn lost or ctx done; outer loop reconnects/exits
			}
			if n.Payload == w.nodeID {
				continue // ignore our own echo
			}
			w.mu.Lock()
			cb := w.callback
			w.mu.Unlock()
			if cb != nil {
				cb(n.Payload)
			}
		}
		conn.Release()
	}
}

func (w *pgWatcher) backoff(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-time.After(watcherReconnectDelay):
	}
}
```

- [ ] **Step 5: Run the test to verify it passes (3×)**

Run: `go test -p 1 ./internal/authz/casbin/... -run TestPGWatcher -count=1`
Repeat 3× to confirm non-flaky.
Expected: PASS — B observes, A does not; no goroutine leak (Close joins).

- [ ] **Step 6: Commit**

```bash
git add internal/authz/casbin/pg_watcher.go internal/authz/casbin/pg_watcher_test.go internal/authz/casbin/export_test.go
git commit -m "$(printf 'feat(authz): LISTEN/NOTIFY pgWatcher for multi-node policy reload (ADR-0023)\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 5: Façade `NewCasbinAuthorizerFromDB` + options

**Files:**
- Modify: `casbinauthz/casbinauthz.go` (add the constructor + `DBOption`s; read the existing file first to match its model/enforcer/wrapper idioms and import aliases)
- Test: `casbinauthz/casbinauthz_db_test.go`

**Interfaces:**
- Consumes: `casbin.newPGAdapter`/`newPGWatcher` (Tasks 2–4, via the internal package), `internalcasbin.New(enforcer)` (existing hybrid evaluator), `DefaultModel`.
- Produces: `casbinauthz.NewCasbinAuthorizerFromDB(ctx context.Context, pool *pgxpool.Pool, opts ...DBOption) (authz.Authorizer, io.Closer, error)`; `DBOption` + `WithModel(string)`, `WithoutWatcher()`, `WithWatcherChannel(string)`, `WithNodeID(string)`.

- [ ] **Step 1: Add an internal builder seam**

The façade must construct the adapter+watcher+enforcer, but those constructors are unexported in `internal/authz/casbin`. Add an EXPORTED builder in the internal package (not a test seam — this is real API the façade uses). Create `internal/authz/casbin/db.go`:

```go
package casbin

import (
	"context"
	"fmt"
	"io"

	casbinv2 "github.com/casbin/casbin/v2"
	"github.com/casbin/casbin/v2/model"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DBConfig configures NewDBAuthorizer.
type DBConfig struct {
	ModelText      string // required (the façade passes DefaultModel by default)
	WatcherEnabled bool
	WatcherChannel string
	NodeID         string
}

// NewDBAuthorizer builds a hybrid *Authorizer whose policy is loaded from pool
// via pgAdapter, optionally wiring a pgWatcher for multi-node reload. It returns
// the *Authorizer (which holds the enforcer) and an io.Closer that stops the
// watcher (a no-op when the watcher is disabled). On any error the partially
// built watcher is closed before returning.
func NewDBAuthorizer(ctx context.Context, pool *pgxpool.Pool, cfg DBConfig) (*Authorizer, io.Closer, error) {
	m, err := model.NewModelFromString(cfg.ModelText)
	if err != nil {
		return nil, nil, fmt.Errorf("casbin: db authorizer: model: %w", err)
	}
	adapter := newPGAdapter(pool)
	enforcer, err := casbinv2.NewSyncedEnforcer(m, adapter)
	if err != nil {
		return nil, nil, fmt.Errorf("casbin: db authorizer: enforcer: %w", err)
	}

	closer := io.Closer(noopCloser{})
	if cfg.WatcherEnabled {
		w := newPGWatcher(pool, cfg.WatcherChannel, cfg.NodeID)
		if err := w.SetUpdateCallback(func(string) { _ = enforcer.LoadPolicy() }); err != nil {
			w.Close()
			return nil, nil, fmt.Errorf("casbin: db authorizer: watcher callback: %w", err)
		}
		if err := enforcer.SetWatcher(w); err != nil {
			w.Close()
			return nil, nil, fmt.Errorf("casbin: db authorizer: set watcher: %w", err)
		}
		closer = watcherCloser{w}
	}
	return New(enforcer), closer, nil
}

type noopCloser struct{}

func (noopCloser) Close() error { return nil }

type watcherCloser struct{ w *pgWatcher }

func (c watcherCloser) Close() error { c.w.Close(); return nil }
```

> Confirm-point: whether `enforcer.SetWatcher` already calls `SetUpdateCallback(enforcer.LoadPolicy)` internally in v2.135.0. Setting it explicitly first (as above) is safe either way — casbin's default callback would just be overwritten by ours (which is the same `LoadPolicy`). If `SetWatcher` errors when a callback is pre-set, set the watcher first then the callback; verify against the source.

- [ ] **Step 2: Write the failing façade test**

Create `casbinauthz/casbinauthz_db_test.go`:

```go
package casbinauthz_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/casbinauthz"
	"github.com/kartaladev/wrkflw/database"
)

func TestNewCasbinAuthorizerFromDB_MultiNodeReload(t *testing.T) {
	pool := database.RunTestDatabase(t)
	require.NoError(t, casbinauthz.MigrateCasbin(t.Context(), pool))

	// Two authorizers over the same DB, distinct node ids, watcher on.
	_, closerA, err := casbinauthz.NewCasbinAuthorizerFromDB(t.Context(), pool,
		casbinauthz.WithNodeID("A"), casbinauthz.WithWatcherChannel("wrkflw_casbin_policy_db_test"))
	require.NoError(t, err)
	defer closerA.Close()

	authB, closerB, err := casbinauthz.NewCasbinAuthorizerFromDB(t.Context(), pool,
		casbinauthz.WithNodeID("B"), casbinauthz.WithWatcherChannel("wrkflw_casbin_policy_db_test"))
	require.NoError(t, err)
	defer closerB.Close()

	// Seed a role + privilege policy DIRECTLY in the table, then have A change it.
	// Use the enforcer through A by adding a policy via the adapter-backed table:
	_, err = pool.Exec(t.Context(),
		`INSERT INTO casbin_rule (ptype, v0, v1, v2) VALUES ('p','admin','process:42','approve')`)
	require.NoError(t, err)
	_, err = pool.Exec(t.Context(),
		`INSERT INTO casbin_rule (ptype, v0, v1) VALUES ('g','alice','admin')`)
	require.NoError(t, err)
	// Notify via A so B reloads (NOTIFY through the same channel).
	_, err = pool.Exec(t.Context(), `SELECT pg_notify('wrkflw_casbin_policy_db_test','A')`)
	require.NoError(t, err)

	// B must, after reload, authorize alice's privilege on process:42.
	// (AuthzSpec/Actor shapes per the existing authz tests — adjust to match.)
	require.Eventually(t, func() bool {
		return authorizeOK(t, authB, "alice", "process:42", "approve")
	}, 5*time.Second, 50*time.Millisecond, "node B must see policy after reload")

	assert.NotNil(t, authB)
}
```

> The `authorizeOK` helper builds an `authz.AuthzSpec` with the right `Privileges` (`"process:42 approve"`) + an `authz.Actor{ID:"alice"}` and calls `authB.Authorize(ctx, spec, actor, nil)`, returning `err == nil`. Model it on the EXISTING `casbinauthz` / `internal/authz/casbin` tests (read them to copy the exact `AuthzSpec`/`Actor`/privilege-format and the `DefaultModel`'s `p = sub, obj, act` shape). The seeded rule columns (`v0=sub, v1=obj, v2=act`) must match `DefaultModel`'s `p` definition.

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test -p 1 ./casbinauthz/... -run TestNewCasbinAuthorizerFromDB`
Expected: FAIL — `undefined: casbinauthz.NewCasbinAuthorizerFromDB`.

- [ ] **Step 4: Write the façade constructor + options**

In `casbinauthz/casbinauthz.go` (read it first; reuse `DefaultModel` and the existing `Authorizer` wrapper that wraps `*internalcasbin.Authorizer` + enforcer), add:

```go
import (
	"context"
	"io"

	"github.com/jackc/pgx/v5/pgxpool"
	internalcasbin "github.com/kartaladev/wrkflw/internal/authz/casbin"
	"github.com/kartaladev/wrkflw/authz"
)

// DBOption configures NewCasbinAuthorizerFromDB.
type DBOption func(*internalcasbin.DBConfig)

// WithModel overrides the casbin model text (default: DefaultModel).
func WithModel(text string) DBOption {
	return func(c *internalcasbin.DBConfig) { c.ModelText = text }
}

// WithoutWatcher disables the LISTEN/NOTIFY policy-reload watcher (single-node).
func WithoutWatcher() DBOption {
	return func(c *internalcasbin.DBConfig) { c.WatcherEnabled = false }
}

// WithWatcherChannel sets the Postgres NOTIFY channel (default wrkflw_casbin_policy).
func WithWatcherChannel(name string) DBOption {
	return func(c *internalcasbin.DBConfig) { c.WatcherChannel = name }
}

// WithNodeID sets this process's id used to filter self-notifications. Default:
// a value derived at construction (see NewCasbinAuthorizerFromDB).
func WithNodeID(id string) DBOption {
	return func(c *internalcasbin.DBConfig) { c.NodeID = id }
}

// NewCasbinAuthorizerFromDB builds a hybrid casbin Authorizer whose policy is
// loaded from (and saved to) the casbin_rule table in pool, with a LISTEN/NOTIFY
// watcher that reloads policy when another node changes it. Call MigrateCasbin
// first. Close the returned io.Closer at shutdown to stop the watcher.
func NewCasbinAuthorizerFromDB(ctx context.Context, pool *pgxpool.Pool, opts ...DBOption) (authz.Authorizer, io.Closer, error) {
	cfg := internalcasbin.DBConfig{
		ModelText:      DefaultModel,
		WatcherEnabled: true,
		WatcherChannel: "wrkflw_casbin_policy",
		NodeID:         defaultNodeID(),
	}
	for _, o := range opts {
		o(&cfg)
	}
	inner, closer, err := internalcasbin.NewDBAuthorizer(ctx, pool, cfg)
	if err != nil {
		return nil, nil, err
	}
	// Wrap the internal *Authorizer in the façade's existing public Authorizer
	// shape so the return type is the stable authz.Authorizer interface.
	return wrapInternal(inner), closer, nil
}
```

`defaultNodeID()` returns a process-unique string. Implement it simply (this is edge infrastructure, not the pure core, so a random/uuid value is fine):

```go
import (
	"crypto/rand"
	"encoding/hex"
)

func defaultNodeID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "node-" + hex.EncodeToString(b[:])
}
```

> `wrapInternal(inner *internalcasbin.Authorizer) authz.Authorizer`: if the façade's existing `Authorizer` type already wraps `*internalcasbin.Authorizer`, construct it directly. If the existing `NewCasbinAuthorizer` path builds the wrapper from an enforcer, expose a small internal accessor or have `NewDBAuthorizer` ALSO return the `authz.Authorizer` directly (cleaner). **Preferred:** change `internalcasbin.NewDBAuthorizer` to return `(authz.Authorizer, io.Closer, error)` by wrapping with whatever the existing façade uses — read `casbinauthz.go` to see how `NewCasbinAuthorizer` produces its `authz.Authorizer`, and mirror it so there is ONE wrapping path. Eliminate `wrapInternal` if `NewDBAuthorizer` can return the interface directly.

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test -p 1 ./casbinauthz/... -run TestNewCasbinAuthorizerFromDB`
Run: `go test -p 1 ./internal/authz/casbin/... ./casbinauthz/...` (no regressions)
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add casbinauthz/casbinauthz.go casbinauthz/casbinauthz_db_test.go internal/authz/casbin/db.go
git commit -m "$(printf 'feat(authz): NewCasbinAuthorizerFromDB facade + watcher wiring (ADR-0023)\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 6: Verification gate + confinement guard + HANDOVER

**Files:**
- Create/extend: a casbin-confinement guard test (if one does not already exist) asserting casbin is not imported outside `casbinauthz/` + `internal/authz/casbin/`
- Modify: `docs/plans/HANDOVER.md`

- [ ] **Step 1: Confinement guard**

Check whether a guard already exists: `grep -rn "casbin" --include=*_test.go | grep -i 'import\|Deps\|purity'`. If none asserts confinement, add `internal/authz/casbin/confinement_test.go` modeled on `engine/purity_test.go` (`TestCorePurityNoOTel`): use `go list -f '{{.Deps}}'` over `./engine/... ./model/... ./runtime/... ./internal/persistence/...` and assert none transitively import `github.com/casbin/casbin`. Run it: it must pass (casbin is confined).

- [ ] **Step 2: Run the full verification gate (capture real output)**

```bash
go test -race $(go list ./... | grep -v 'internal/persistence/postgres' | grep -v 'internal/authz/casbin' | grep -v 'casbinauthz')
go test -race -p 1 ./internal/authz/casbin/... ./casbinauthz/... ./internal/persistence/postgres/...
go test -coverprofile=cover.out ./casbinauthz/... ./internal/authz/casbin/... && go tool cover -func=cover.out | tail -1
golangci-lint run ./...
go list -f '{{.Deps}}' ./engine/... ./model/... ./runtime/... ./internal/persistence/... | tr ' ' '\n' | grep -E 'casbin|gorm|go-pg|sqlx' || echo "CLEAN: no casbin/ORM leak outside authz packages"
grep -E 'gorm|go-pg|jmoiron/sqlx|/ent' go.mod || echo "NO NEW ORM in go.mod"
```

Expected: all green; coverage ≥85% on `casbinauthz` + `internal/authz/casbin`; lint 0; no casbin/ORM leak; casbin still `v2.135.0`. Do NOT commit `cover.out`.

- [ ] **Step 3: Update HANDOVER.md**

Add a "## DB casbin policy adapter — ✅ COMPLETE" section mirroring prior tracks (what shipped by layer: migration+MigrateCasbin, pgAdapter, pgWatcher, façade; ADR-0023; gate result; deferred follow-ups: filtered loading / `WatcherEx` incremental updates, policy-admin transport surface, the separate version table note). Mark the "DB casbin policy adapter" item resolved in the resume-point "Next focus" list (leaving async call activity as the remaining outstanding item).

- [ ] **Step 4: Commit**

```bash
git add docs/plans/HANDOVER.md internal/authz/casbin/confinement_test.go
git commit -m "$(printf 'docs(authz): mark DB casbin policy adapter complete + confinement guard\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

## Verification checklist (whole track)

- [ ] Task 1 — `MigrateCasbin` creates `casbin_rule`, idempotent, on a separate `casbin_goose_db_version` table.
- [ ] Task 2 — `pgAdapter` SavePolicy/LoadPolicy round-trip p and g rules; rule padding/trimming correct.
- [ ] Task 3 — AddPolicy/RemovePolicy/RemoveFilteredPolicy persist and are observable on reload.
- [ ] Task 4 — `pgWatcher` notifies other nodes (payload≠self) and not itself; Close stops the listener (no leak).
- [ ] Task 5 — `NewCasbinAuthorizerFromDB` returns `authz.Authorizer` + `io.Closer`; two-node reload works; hybrid Authorize over DB policy.
- [ ] Task 6 — full gate green (race, ≥85% on authz pkgs, lint 0); casbin confined; no new ORM; casbin pinned v2.135.0; HANDOVER updated.

## Self-review notes (plan author)

- **Spec coverage:** §3 adapter → Tasks 2–3; §4 watcher → Task 4; §5 migration → Task 1; §6 façade → Task 5; §9 testing → each task + Task 6; §2 invariants (confinement, no ORM, façade) → Task 6 guard + Global Constraints. Covered.
- **Type consistency:** `MigrateCasbin`, `newPGAdapter`/`NewPGAdapter`, `padRule`, `newPGWatcher`/`NewPGWatcher`, `NewDBAuthorizer`/`DBConfig`, `NewCasbinAuthorizerFromDB`/`DBOption`/`WithModel`/`WithoutWatcher`/`WithWatcherChannel`/`WithNodeID` are named identically across producing/consuming tasks.
- **Confirm-points flagged for the implementer (verify against casbin v2.135.0 / read current code):** `model.Assertion.Policy` field name in `SavePolicy`; `model.Model.AddPolicy`/`HasPolicy` test helpers (else use enforcer-level `GetPolicy`); whether `SetWatcher` auto-sets the callback; the existing `casbinauthz.Authorizer` wrapper shape so `NewDBAuthorizer` returns the interface through ONE wrapping path; the existing authz tests' `AuthzSpec`/`Actor`/privilege-format for the Task-5 `authorizeOK` helper and the seeded `casbin_rule` columns.

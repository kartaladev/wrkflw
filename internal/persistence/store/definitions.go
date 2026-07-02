package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/zakyalvan/krtlwrkflw/internal/database"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// DefinitionStore is the vendor-neutral, dialect-parametrised durable
// process-definition store. It satisfies [runtime.DefinitionRegistry] via
// [DefinitionStore.Lookup] and also exposes [DefinitionStore.PutDefinition]
// and [DefinitionStore.GetDefinition] for admin / write paths.
//
// Definitions are serialised as JSON into wrkflw_definitions and deserialised
// by [GetDefinition] and [Lookup]. All fields of [model.ProcessDefinition] and
// its nested types must survive the round-trip — the rich-definition conformance
// test validates this exhaustively against all three dialects.
//
// SQL is written once with ? placeholders and run through
// [dialect.Dialect.Rebind] for the backend's native placeholder style. The
// definition UPSERT conflict clause comes from [dialect.Dialect.UpsertDefinition],
// keyed on (def_id, version). No inline dialect-name comparisons are used.
//
// DefinitionStore is safe for concurrent use: it carries no mutable state.
type DefinitionStore struct {
	conn    any // *pgxpool.Pool or *sql.DB
	dialect dialect.Dialect
}

// Compile-time checks that *DefinitionStore satisfies both ports.
var (
	_ runtime.DefinitionRegistry = (*DefinitionStore)(nil)
)

// NewDefinitionStore constructs a DefinitionStore over conn using dialect d.
// conn must be either a *pgxpool.Pool (Postgres) or a *sql.DB (MySQL, SQLite);
// any other type causes [database.From] to return an error when the first query
// is issued.
// Returns [ErrNilDependency] when conn is nil or d is nil.
//
// Example (Postgres):
//
//	pool, _ := pgxpool.New(ctx, dsn)
//	ds, err := store.NewDefinitionStore(pool, dialect.NewPostgres())
//
// Example (SQLite, tests):
//
//	db := dbtest.RunTestSQLite(t)
//	ds, err := store.NewDefinitionStore(db, dialect.NewSQLite())
func NewDefinitionStore(conn any, d dialect.Dialect) (*DefinitionStore, error) {
	if isNilDep(conn) {
		return nil, fmt.Errorf("%w: conn", ErrNilDependency)
	}
	if isNilDep(d) {
		return nil, fmt.Errorf("%w: dialect", ErrNilDependency)
	}
	return &DefinitionStore{conn: conn, dialect: d}, nil
}

// querier returns a pool-backed [database.Querier] over ds.conn. DefinitionStore
// uses only read-only SELECT queries through this path; PutDefinition issues a
// single idempotent INSERT that does not need an explicit transaction because the
// conflict clause makes it atomic.
func (ds *DefinitionStore) querier(ctx context.Context) database.Querier {
	_ = ctx
	q, _ := database.From(ds.conn)
	return q
}

// PutDefinition upserts a process definition into wrkflw_definitions, keyed by
// (def_id, version). The operation is idempotent: re-inserting the same
// (defID, version) pair overwrites the stored JSON with the new value via the
// dialect-specific conflict clause ([dialect.Dialect.UpsertDefinition]).
//
// def.ID and def.Version must be non-empty / non-zero; the database schema
// enforces uniqueness on (def_id, version).
//
// created_at is written as time.Now().UTC(). The column is set on first insert
// only — the conflict-update clause touches only the definition column — so
// re-inserts preserve the original creation timestamp.
func (ds *DefinitionStore) PutDefinition(ctx context.Context, def *model.ProcessDefinition) error {
	data, err := json.Marshal(def)
	if err != nil {
		return fmt.Errorf("workflow-store: put definition %s:%d: marshal: %w", def.ID, def.Version, err)
	}

	createdAt := timeArg(ds.dialect, time.Now().UTC())

	q := ds.querier(ctx)
	_, err = q.Exec(ctx, ds.dialect.Rebind(
		`INSERT INTO wrkflw_definitions (def_id, version, definition, created_at)
		 VALUES (?,?,?,?)`+ds.dialect.UpsertDefinition()),
		def.ID, def.Version, data, createdAt,
	)
	if err != nil {
		return fmt.Errorf("workflow-store: put definition %s:%d: %w", def.ID, def.Version, err)
	}
	return nil
}

// GetDefinition fetches the definition identified by (defID, version).
// Returns [runtime.ErrDefinitionNotFound] when no row matches.
func (ds *DefinitionStore) GetDefinition(ctx context.Context, defID string, version int) (*model.ProcessDefinition, error) {
	q := ds.querier(ctx)

	var data []byte
	err := q.QueryRow(ctx, ds.dialect.Rebind(
		`SELECT definition FROM wrkflw_definitions WHERE def_id = ? AND version = ?`),
		defID, version,
	).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s:%d", runtime.ErrDefinitionNotFound, defID, version)
	}
	if err != nil {
		return nil, fmt.Errorf("workflow-store: get definition %s:%d: %w", defID, version, err)
	}

	var def model.ProcessDefinition
	if err := json.Unmarshal(data, &def); err != nil {
		return nil, fmt.Errorf("workflow-store: get definition %s:%d: unmarshal: %w", defID, version, err)
	}
	return &def, nil
}

// Lookup satisfies [runtime.DefinitionRegistry]. defRef is interpreted as:
//   - "defID:version" — exact (defID, version) lookup via [GetDefinition].
//   - "defID"         — the definition with the highest version for defID.
//
// Returns [runtime.ErrDefinitionNotFound] when no matching row exists.
// ctx is propagated to the underlying SQL query for cancellation support.
func (ds *DefinitionStore) Lookup(ctx context.Context, defRef string) (*model.ProcessDefinition, error) {
	if id, ver, ok := strings.Cut(defRef, ":"); ok {
		n, err := strconv.Atoi(ver)
		if err != nil {
			return nil, fmt.Errorf("workflow-store: lookup %q: bad version segment: %w", defRef, err)
		}
		return ds.GetDefinition(ctx, id, n)
	}

	// No colon: return the definition with the highest version.
	q := ds.querier(ctx)

	var data []byte
	err := q.QueryRow(ctx, ds.dialect.Rebind(
		`SELECT definition FROM wrkflw_definitions
		 WHERE def_id = ?
		 ORDER BY version DESC
		 LIMIT 1`),
		defRef,
	).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", runtime.ErrDefinitionNotFound, defRef)
	}
	if err != nil {
		return nil, fmt.Errorf("workflow-store: lookup %q: %w", defRef, err)
	}

	var def model.ProcessDefinition
	if err := json.Unmarshal(data, &def); err != nil {
		return nil, fmt.Errorf("workflow-store: lookup %q: unmarshal: %w", defRef, err)
	}
	return &def, nil
}

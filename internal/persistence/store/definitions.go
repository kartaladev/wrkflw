package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jonboulle/clockwork"

	// kinds registers every node kind so definitions read back from the store
	// deserialize into their concrete types (see definition/kinds).
	_ "github.com/kartaladev/wrkflw/definition/kinds"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/internal/database"
	"github.com/kartaladev/wrkflw/internal/database/transaction"
	"github.com/kartaladev/wrkflw/internal/persistence/dialect"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// DefinitionStore is the vendor-neutral, dialect-parametrised durable
// process-definition store. It satisfies [kernel.DefinitionRegistry] via
// [DefinitionStore.Lookup] and also exposes [DefinitionStore.PublishDefinition]
// and [DefinitionStore.GetDefinition] for admin / write paths.
//
// Definitions are serialised as JSON into wrkflw_definitions and deserialised
// by [GetDefinition] and [Lookup]. All fields of [model.ProcessDefinition] and
// its nested types must survive the round-trip — the rich-definition conformance
// test validates this exhaustively against all three dialects.
//
// SQL is written once with ? placeholders and run through
// [dialect.Dialect.Rebind] for the backend's native placeholder style. The
// publish is an insert-if-absent built from
// [dialect.Dialect.InsertIgnorePrefix] and [dialect.Dialect.InsertIgnoreDedup],
// declined by the (def_id, version) primary key. No inline dialect-name
// comparisons are used.
//
// DefinitionStore is safe for concurrent use: it carries no mutable state.
type DefinitionStore struct {
	conn    any // *pgxpool.Pool or *sql.DB
	dialect dialect.Dialect
	// clk is the time source for the created_at stamp written by
	// [DefinitionStore.PublishDefinition].
	clk clockwork.Clock
}

// DefinitionOption is a functional option that configures a [DefinitionStore]
// built by [NewDefinitionStore].
type DefinitionOption func(*DefinitionStore)

// WithDefinitionClock overrides the clock used for the created_at stamp written
// by [DefinitionStore.PublishDefinition]. The default is
// [clockwork.NewRealClock]. A nil clock is ignored (the default is kept).
//
// Reachable off-module only through its facade forwarder,
// persistence.WithDefinitionClock; see the note on [WithStoreClock].
func WithDefinitionClock(clk clockwork.Clock) DefinitionOption {
	return func(ds *DefinitionStore) {
		if clk != nil {
			ds.clk = clk
		}
	}
}

// Compile-time checks that *DefinitionStore satisfies both ports.
var (
	_ kernel.DefinitionRegistry = (*DefinitionStore)(nil)
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
func NewDefinitionStore(conn any, d dialect.Dialect, opts ...DefinitionOption) (*DefinitionStore, error) {
	if isNilDep(conn) {
		return nil, fmt.Errorf("%w: conn", ErrNilDependency)
	}
	if isNilDep(d) {
		return nil, fmt.Errorf("%w: dialect", ErrNilDependency)
	}
	ds := &DefinitionStore{conn: conn, dialect: d, clk: clockwork.NewRealClock()}
	for _, o := range opts {
		o(ds)
	}
	return ds, nil
}

// querier returns a pool-backed [database.Querier] over ds.conn. It is the
// READ path only: [DefinitionStore.GetDefinition] and [DefinitionStore.Lookup]
// issue SELECTs through it. The write path does not use it —
// [DefinitionStore.PublishDefinition] goes through [transaction.JoinOrBegin] so
// it can join a caller's ambient transaction.
func (ds *DefinitionStore) querier(ctx context.Context) database.Querier {
	_ = ctx
	q, _ := database.From(ds.conn)
	return q
}

// ErrConcurrentPublish is returned when the insert was declined but no stored
// row can be read back inside the same unit of work. That means another
// transaction holds an uncommitted insert for the same (def_id, version):
// "ON CONFLICT DO NOTHING" reports zero rows immediately rather than blocking
// on the in-flight write, so under READ COMMITTED the read-back sees nothing.
//
// It is deliberately neither success nor a content conflict. Reporting success
// would claim a publish that never happened; reporting
// [kernel.ErrDefinitionExists] would assert a content difference that was never
// observed. The caller should retry — by then the other transaction has either
// committed (making this an idempotent no-op or a genuine conflict) or rolled
// back (making the insert succeed).
var ErrConcurrentPublish = errors.New("workflow-store: definition publish raced a concurrent publish")

// PublishDefinition publishes def as the content of (def.ID, def.Version).
//
// A published version is IMMUTABLE. The write is an insert-if-absent, never an
// upsert, so an existing version is never overwritten:
//
//   - the row did not exist  → it is inserted. Returns nil.
//   - the row exists and its content is identical → nothing is written.
//     Returns nil, so republishing is idempotent and safe to retry.
//   - the row exists and its content differs → returns
//     [kernel.ErrDefinitionExists], wrapped with "id:version". Publish a new
//     version instead of editing a published one.
//   - the row is being inserted right now by someone else → returns
//     [ErrConcurrentPublish]. See that sentinel.
//
// def is checked by [kernel.ValidateDefinition] — the same gate
// [kernel.MemDefinitionRegistry.Register] uses — before any I/O, so an
// in-memory registration and a durable publish accept exactly the same
// definitions. A Version of 0 is rejected there as [model.ErrInvalidVersion];
// versions are never auto-assigned.
//
// # Transaction
//
// PublishDefinition joins the caller's ambient transaction when ctx carries one
// (see [transaction.JoinOrBegin]), so a consumer can publish a version and
// write its own rows as one atomic unit — and a rollback takes the publish with
// it. With no ambient transaction it commits its own leaf. The read-back
// happens on the same querier as the insert, which is what makes the rollback
// total.
//
// created_at is read from the store's [clockwork.Clock] (override it with
// [WithDefinitionClock]) and is set by the inserting publish only; an
// idempotent republish writes nothing and so preserves the original stamp.
func (ds *DefinitionStore) PublishDefinition(ctx context.Context, def *model.ProcessDefinition) error {
	// The shared authoring gate, before any I/O. It also covers def == nil, so
	// the dereferences below are safe.
	if err := kernel.ValidateDefinition(def); err != nil {
		return err
	}

	data, err := json.Marshal(def)
	if err != nil {
		return fmt.Errorf("workflow-store: publish definition %s:%d: marshal: %w", def.ID, def.Version, err)
	}

	q, err := transaction.JoinOrBegin(ctx, ds.conn)
	if err != nil {
		return fmt.Errorf("workflow-store: publish definition %s:%d: begin: %w", def.ID, def.Version, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = q.Rollback(ctx)
		}
	}()

	// Insert-if-absent, assembled from the dialect's prefix and suffix so no
	// inline dialect checks are needed: "INSERT ... ON CONFLICT DO NOTHING" on
	// Postgres and SQLite, "INSERT IGNORE ..." on MySQL.
	res, err := q.Exec(ctx, ds.dialect.Rebind(
		ds.dialect.InsertIgnorePrefix()+
			` INTO wrkflw_definitions (def_id, version, definition, created_at)
			 VALUES (?,?,?,?)`+
			ds.dialect.InsertIgnoreDedup()),
		def.ID, def.Version, data, timeArg(ds.dialect, ds.clk.Now().UTC()),
	)
	if err != nil {
		return fmt.Errorf("workflow-store: publish definition %s:%d: exec: %w", def.ID, def.Version, err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("workflow-store: publish definition %s:%d: rows affected: %w", def.ID, def.Version, err)
	}

	// RowsAffected == 0 means the insert was declined. It does NOT on its own
	// mean a conflicting row exists: MySQL's INSERT IGNORE downgrades every
	// error to a warning — truncation, a bad value, an over-long def_id — and
	// all of them also land here. The stored row has to be read back before
	// anything can be concluded.
	if n == 0 {
		if err := ds.assertPublishedIsIdentical(ctx, q, def, data); err != nil {
			return err
		}
	}

	if err := q.Commit(ctx); err != nil {
		return fmt.Errorf("workflow-store: publish definition %s:%d: commit: %w", def.ID, def.Version, err)
	}
	committed = true
	return nil
}

// assertPublishedIsIdentical reads the stored definition back through q — the
// same querier the declined insert ran on, so it sees that unit's own writes
// and rolls back with it — and reports whether the already-published content
// matches incoming.
func (ds *DefinitionStore) assertPublishedIsIdentical(
	ctx context.Context,
	q database.Querier,
	def *model.ProcessDefinition,
	incoming []byte,
) error {
	var stored []byte
	err := q.QueryRow(ctx, ds.dialect.Rebind(
		`SELECT definition FROM wrkflw_definitions WHERE def_id = ? AND version = ?`),
		def.ID, def.Version,
	).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %s:%d", ErrConcurrentPublish, def.ID, def.Version)
	}
	if err != nil {
		return fmt.Errorf("workflow-store: publish definition %s:%d: read back: %w", def.ID, def.Version, err)
	}

	same, err := sameDefinitionContent(stored, incoming)
	if err != nil {
		return fmt.Errorf("workflow-store: publish definition %s:%d: %w", def.ID, def.Version, err)
	}
	if !same {
		return fmt.Errorf("%w: %s:%d: content differs from the published version",
			kernel.ErrDefinitionExists, def.ID, def.Version)
	}
	return nil
}

// sameDefinitionContent reports whether the stored JSON and the freshly
// marshalled incoming JSON describe the same definition.
//
// The comparison is NORMALISED: stored is decoded into a
// [model.ProcessDefinition] and re-encoded with the CURRENT marshaller before
// the bytes are compared. Do not "simplify" this into a direct
// bytes.Equal(stored, incoming) — the round-trip is load-bearing twice over:
//
//  1. The database rewrites the bytes. definition is JSONB on Postgres and JSON
//     on MySQL, and both re-serialise on write (JSONB orders keys by length
//     then bytes). What is read back is therefore almost never byte-equal to
//     what was written, so a direct comparison would report a content conflict
//     on EVERY republish on those two dialects and idempotency would be lost
//     on the primary backend.
//  2. It absorbs serialisation drift. A library or struct-tag change that
//     alters how an UNCHANGED definition encodes — a reordered field, a newly
//     omitted empty value — would otherwise read as a conflict on rows written
//     before the change.
//
// # Documented limit
//
// [model.ProcessDefinition]'s scoped action catalog (its unexported scoped and
// scopedNames fields) is never serialised. Two definitions that differ ONLY in
// their scoped catalog therefore compare equal here and the second publish is a
// silent no-op. That is correct with respect to what is stored — the catalog is
// not part of the persisted definition — and it fails closed, since nothing is
// overwritten either way.
func sameDefinitionContent(stored, incoming []byte) (bool, error) {
	var def model.ProcessDefinition
	if err := json.Unmarshal(stored, &def); err != nil {
		return false, fmt.Errorf("unmarshal stored definition: %w", err)
	}
	normalised, err := json.Marshal(&def)
	if err != nil {
		return false, fmt.Errorf("re-marshal stored definition: %w", err)
	}
	return bytes.Equal(normalised, incoming), nil
}

// GetDefinition fetches the definition identified by (defID, version).
// Returns [kernel.ErrDefinitionNotFound] when no row matches.
func (ds *DefinitionStore) GetDefinition(ctx context.Context, defID string, version int) (*model.ProcessDefinition, error) {
	q := ds.querier(ctx)

	var data []byte
	err := q.QueryRow(ctx, ds.dialect.Rebind(
		`SELECT definition FROM wrkflw_definitions WHERE def_id = ? AND version = ?`),
		defID, version,
	).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s:%d", kernel.ErrDefinitionNotFound, defID, version)
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

// Lookup satisfies [kernel.DefinitionRegistry]. q is interpreted as:
//   - q.IsLatest() (Version == 0) — the definition with the highest version for q.ID.
//   - otherwise                   — exact (q.ID, q.Version) lookup via [GetDefinition].
//
// Returns [kernel.ErrDefinitionNotFound] when no matching row exists.
// ctx is propagated to the underlying SQL query for cancellation support.
func (ds *DefinitionStore) Lookup(ctx context.Context, q model.Qualifier) (*model.ProcessDefinition, error) {
	if !q.IsLatest() {
		return ds.GetDefinition(ctx, q.ID, q.Version)
	}

	// Latest: return the definition with the highest version.
	dbq := ds.querier(ctx)

	var data []byte
	err := dbq.QueryRow(ctx, ds.dialect.Rebind(
		`SELECT definition FROM wrkflw_definitions
		 WHERE def_id = ?
		 ORDER BY version DESC
		 LIMIT 1`),
		q.ID,
	).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", kernel.ErrDefinitionNotFound, q)
	}
	if err != nil {
		return nil, fmt.Errorf("workflow-store: lookup %q: %w", q, err)
	}

	var def model.ProcessDefinition
	if err := json.Unmarshal(data, &def); err != nil {
		return nil, fmt.Errorf("workflow-store: lookup %q: unmarshal: %w", q, err)
	}
	return &def, nil
}

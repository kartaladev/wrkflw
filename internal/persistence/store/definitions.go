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

// querier returns the [database.Querier] the READ path should use:
// [DefinitionStore.GetDefinition] and [DefinitionStore.Lookup] issue their
// SELECTs through it.
//
// It joins the caller's ambient transaction when ctx carries one, and falls
// back to the pool otherwise. Both halves matter:
//
//   - Joining is required for correctness. The write path joins the ambient
//     transaction, so a read that went to the pool instead would be looking at
//     a different connection than the one holding the caller's uncommitted
//     writes. A publish-then-read inside a single unit would then miss the row
//     it had just written — and on SQLite, whose write transaction holds the
//     one connection, the read would block on it and hang. That is the same
//     deadlock class moving the WRITE onto the transaction was meant to
//     remove; leaving the read behind merely relocated it.
//   - Falling back to the pool, rather than beginning a transaction, is why
//     this uses [transaction.Join] and not [transaction.JoinOrBegin]. Lookup is
//     a hot path; wrapping every uncontextualised read in its own transaction
//     would be a real cost for no benefit.
func (ds *DefinitionStore) querier(ctx context.Context) database.Querier {
	if q, ok := transaction.Join(ctx); ok {
		return q
	}
	q, _ := database.From(ds.conn)
	return q
}

// ErrConcurrentPublish is returned when the insert was declined but no stored
// row can be read back inside the same unit of work.
//
// It is deliberately neither success nor a content conflict. Reporting success
// would claim a publish that never happened; reporting
// [kernel.ErrDefinitionExists] would assert a content difference that was never
// observed. It is the honest "cannot yet tell" answer.
//
// # How reachable this actually is — measured, not assumed
//
// This branch is defensive, and rarer than the obvious story suggests. The
// obvious story is that "ON CONFLICT DO NOTHING" returns zero rows immediately
// rather than waiting on an in-flight conflicting insert, leaving the read-back
// with nothing to find. That was measured on Postgres 16 and is FALSE: the
// insert BLOCKS until the other transaction commits or rolls back. If it rolls
// back, this insert then succeeds; if it commits, the read-back sees the
// committed row and the outcome is a normal no-op or [kernel.ErrDefinitionExists].
// Neither path reaches here.
//
// What does reach here is a row that was visible to the insert and gone by the
// read-back — a definition deleted by an operator or a pruner in between. There
// is no such deletion path in this package today, which is why this branch has
// no test; see the note on the publish conformance test.
//
// # Retry, and when to stop
//
// Retrying is the right response, but BOUND IT — do not retry indefinitely. On
// MySQL the insert-if-absent form is INSERT IGNORE, which downgrades any error
// it meets to a warning, so a write MySQL declined for some reason other than
// the primary key could present this same signature and would not clear on
// retry. The one such cause that was reachable through this API — an over-long
// def_id — is now refused up front by [kernel.MaxDefinitionIDRunes]. A retry
// budget that expires is a bug report, not a reason to keep waiting.
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
// A REFUSED publish does not poison the caller's unit. [kernel.ErrDefinitionExists]
// and [ErrConcurrentPublish] both mean nothing was written, so the unit is
// committed rather than rolled back and the caller may handle the error and
// carry on with its other writes in the same transaction. That is deliberate:
// republishing an existing version is an expected, recoverable flow, and a
// participant that marked the whole unit rollback-only on an expected outcome
// would make joining useless.
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
	// mean a conflicting row exists, so the stored row is read back before
	// anything is concluded.
	//
	// A note on MySQL, because the obvious guess is wrong and was measured:
	// INSERT IGNORE downgrades errors to warnings, but an over-long def_id does
	// NOT arrive here. MySQL truncates it and reports RowsAffected == 1 — the
	// success branch — which is why that hazard is prevented at the gate by
	// [kernel.MaxDefinitionIDRunes] rather than detected here. A guard on this
	// branch would never have fired for it.
	if n == 0 {
		outcome := ds.assertPublishedIsIdentical(ctx, q, def, data)

		// ErrDefinitionExists and ErrConcurrentPublish are SEMANTIC outcomes,
		// not failures of this unit of work: the insert affected zero rows, so
		// nothing was written and there is nothing to undo. Commit rather than
		// let the deferred rollback run.
		//
		// This matters because of what Rollback means to a JOINED participant.
		// It does not roll back a nested unit — there is no such thing — it
		// marks the caller's ENTIRE transaction rollback-only. Rolling back on
		// an expected conflict would therefore destroy the caller's unrelated
		// writes in the same unit, which would defeat the whole point of
		// joining an ambient transaction. Commit is a no-op when joined and
		// correctly closes an owned leaf, so this is right both ways.
		//
		// A read-back that failed for an infrastructure reason is NOT in that
		// set and deliberately falls through to the rollback: that unit's
		// health is in doubt.
		if outcome == nil || isSemanticPublishOutcome(outcome) {
			if err := q.Commit(ctx); err != nil {
				return fmt.Errorf("workflow-store: publish definition %s:%d: commit: %w", def.ID, def.Version, err)
			}
			committed = true
		}
		return outcome
	}

	if err := q.Commit(ctx); err != nil {
		return fmt.Errorf("workflow-store: publish definition %s:%d: commit: %w", def.ID, def.Version, err)
	}
	committed = true
	return nil
}

// isSemanticPublishOutcome reports whether err is one of the two expected,
// non-failure results of a declined insert — the publish was refused, but the
// unit of work is intact and wrote nothing.
func isSemanticPublishOutcome(err error) bool {
	return errors.Is(err, kernel.ErrDefinitionExists) || errors.Is(err, ErrConcurrentPublish)
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
		// Deliberately drops err. A decode failure here carries fragments of
		// the STORED definition (an unknown node kind name, a malformed field)
		// and this error is returned to whoever attempted the publish, who is
		// not necessarily entitled to read the stored definition. The caller
		// gets the key it already supplied and nothing it did not. The detail
		// stays server-side; the condition means version skew or a row written
		// outside this API, which is an operator problem, not a caller one.
		return fmt.Errorf("workflow-store: publish definition %s:%d: cannot decode the stored definition",
			def.ID, def.Version)
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
// BOTH sides go through the same decode -> re-encode transform before the bytes
// are compared. The symmetry is the whole point and must not be removed; see
// below. Nor may this be "simplified" into a direct bytes.Equal(stored,
// incoming), for two further reasons:
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
// # Why both sides, and not just the stored one
//
// [model.ProcessDefinition] has marshal-only state.
// [model.ProcessDefinition.MarshalJSON] emits "scoped_actions" from
// ScopedActionNames(), and UnmarshalJSON deliberately accepts and DROPS it,
// because a scoped catalog holds live action implementations with no
// serialisable form. So decode -> re-encode is not an identity for any
// definition carrying scoped actions: it strips that key.
//
// Normalising only the stored side would therefore compare a stripped
// definition against an unstripped one and find them different — including when
// they are the very same object. Republishing an identical definition would be
// refused with [kernel.ErrDefinitionExists], which is precisely the case this
// method exists to make a no-op. Putting both sides through the same transform
// makes it a true fixed point: the marshal-only key drops out on both sides and
// equal definitions compare equal.
//
// The extra decode costs nothing on the happy path — this runs only when an
// insert was declined.
//
// # Documented limit
//
// The consequence of the above is that two definitions differing ONLY in their
// scoped action catalog compare equal here, so the second publish is a silent
// no-op rather than a conflict. That is acceptable and deliberate: the catalog
// itself is never stored, only the names are, and those are marshal-only, so
// nothing a reader can observe through this API differs between the two. It
// also fails closed — nothing is overwritten either way.
func sameDefinitionContent(stored, incoming []byte) (bool, error) {
	normStored, err := normaliseDefinition(stored)
	if err != nil {
		return false, fmt.Errorf("stored definition: %w", err)
	}
	normIncoming, err := normaliseDefinition(incoming)
	if err != nil {
		return false, fmt.Errorf("incoming definition: %w", err)
	}
	return bytes.Equal(normStored, normIncoming), nil
}

// normaliseDefinition decodes data into a [model.ProcessDefinition] and
// re-encodes it with the current marshaller, yielding the canonical form used
// for content comparison. Applying it to both sides of a comparison makes the
// transform idempotent over the marshal-only fields described on
// [sameDefinitionContent].
func normaliseDefinition(data []byte) ([]byte, error) {
	var def model.ProcessDefinition
	if err := json.Unmarshal(data, &def); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	normalised, err := json.Marshal(&def)
	if err != nil {
		return nil, fmt.Errorf("re-marshal: %w", err)
	}
	return normalised, nil
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

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
// publish is an insert-if-absent whose conflict clause comes from
// [dialect.Dialect.InsertIgnoreDefinition] — deliberately NOT the
// InsertIgnorePrefix/InsertIgnoreDedup pair the dedup site uses — declined by
// the (def_id, version) primary key. No inline dialect-name comparisons are
// used.
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

// querier returns a pool-backed [database.Querier] over ds.conn.
//
// DefinitionStore uses only read-only SELECT queries through this path plus
// [DefinitionStore.PublishDefinition]'s insert, which needs no explicit
// transaction: the insert is a single statement made atomic by the conflict
// clause, and when it is declined the follow-up read-back is safe to run
// outside a transaction BECAUSE published definitions are immutable. A
// published (def_id, version) never changes, so whatever row the read-back
// finds is final — there is no interleaving that could make it observe a
// value that later becomes something else. That property is established by
// this change, and it is what makes the pool sufficient here.
//
// Composing a publish with a caller's own writes in one transaction is issue
// #151, deliberately not attempted here.
func (ds *DefinitionStore) querier(ctx context.Context) database.Querier {
	_ = ctx // retained for API stability; callers pass ctx to the returned Querier's methods
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
// # Retry
//
// Retrying is the right response, with a bounded budget. Every backend now
// reports a genuinely failed write as an error rather than as a declined
// insert — the definitions statement suppresses only the duplicate key, never
// truncation or an out-of-range value (see
// [dialect.Dialect.InsertIgnoreDefinition]) — so reaching this branch really
// does mean "not yet decidable" rather than "quietly broken". A retry budget
// that expires is therefore a bug report, not a reason to keep waiting.
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
//   - no row is readable afterwards at all → returns [ErrConcurrentPublish].
//     See that sentinel.
//
// The outcome is decided by reading the row back and comparing content, not by
// the driver's affected-rows count, so it does not depend on connection flags
// such as MySQL's clientFoundRows.
//
// def is checked by [kernel.ValidateDefinition] — the same gate
// [kernel.MemDefinitionRegistry.Register] uses — before any I/O, so an
// in-memory registration and a durable publish accept exactly the same
// definitions. A Version of 0 is rejected there as [model.ErrInvalidVersion];
// versions are never auto-assigned.
//
// # Transactions
//
// PublishDefinition runs on the connection pool and does NOT join a caller's
// ambient transaction, so a publish cannot currently be made atomic with the
// caller's own writes. That composition is issue #151.
//
// It needs no transaction of its own. The insert is a single statement whose
// conflict clause makes it atomic, and the read-back on the declined path is
// sound outside a transaction because a published version is IMMUTABLE: the row
// the read-back finds can never subsequently change, so there is no interleaving
// in which this returns a value that was not final.
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

	q := ds.querier(ctx)

	// Insert-if-absent. The conflict clause comes from the dialect so no inline
	// dialect checks are needed here; see [dialect.Dialect.InsertIgnoreDefinition]
	// for why the definitions site does NOT use MySQL's INSERT IGNORE.
	if _, err := q.Exec(ctx, ds.dialect.Rebind(
		`INSERT INTO wrkflw_definitions (def_id, version, definition, created_at)
		 VALUES (?,?,?,?)`+ds.dialect.InsertIgnoreDefinition()),
		def.ID, def.Version, data, timeArg(ds.dialect, ds.clk.Now().UTC()),
	); err != nil {
		return fmt.Errorf("workflow-store: publish definition %s:%d: exec: %w", def.ID, def.Version, err)
	}

	// The outcome is determined by observed STATE, not by RowsAffected.
	//
	// RowsAffected is a driver-dependent signal, not a fact about the database.
	// MySQL's affected-rows count is switched by the connection's
	// clientFoundRows flag, which lives in the CONSUMER's DSN: with it set,
	// ON DUPLICATE KEY UPDATE reports 1 for a duplicate rather than 0, and a
	// refused publish would have been reported as success. (INSERT IGNORE
	// reported 0 either way, so this became load-bearing only when the
	// statement changed — it is a regression introduced by that change and
	// removed here.)
	//
	// Reading the row back and comparing answers the question directly, and
	// gives the same answer whatever the driver reports: if the stored content
	// matches, the version is published as requested, whether this statement
	// inserted it or found it already there. Those two are indistinguishable to
	// a caller and both mean success.
	//
	// This costs one extra read on an administrative write path. It adds
	// nothing to Lookup or GetDefinition, which are the hot read paths.
	stored, err := ds.readPublished(ctx, q, def)
	if err != nil {
		return err
	}
	return ds.compareWithPublished(stored, data, def)
}

// readPublished reads the already-published definition back through q, the same
// querier the declined insert ran on.
//
// Returns a wrapped [ErrConcurrentPublish] when no row is visible. Any other
// non-nil error is an infrastructure failure of the read itself.
func (ds *DefinitionStore) readPublished(
	ctx context.Context,
	q database.Querier,
	def *model.ProcessDefinition,
) ([]byte, error) {
	var stored []byte
	err := q.QueryRow(ctx, ds.dialect.Rebind(
		`SELECT definition FROM wrkflw_definitions WHERE def_id = ? AND version = ?`),
		def.ID, def.Version,
	).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s:%d", ErrConcurrentPublish, def.ID, def.Version)
	}
	if err != nil {
		return nil, fmt.Errorf("workflow-store: publish definition %s:%d: read back: %w", def.ID, def.Version, err)
	}
	return stored, nil
}

// compareWithPublished reports the publish outcome for an insert that was
// declined because (def.ID, def.Version) already holds stored: nil when the
// content is the same, [kernel.ErrDefinitionExists] when it differs, or a
// decode error naming WHICH side failed.
func (ds *DefinitionStore) compareWithPublished(stored, incoming []byte, def *model.ProcessDefinition) error {
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
	// The INCOMING side first, so a fault in the caller's own definition is
	// never misattributed to stored data and never sends an operator looking in
	// the wrong place. Its detail is safe to return: these are the caller's own
	// bytes, which it just supplied.
	normIncoming, err := normaliseDefinition(incoming)
	if err != nil {
		return false, fmt.Errorf("cannot re-decode the definition supplied for publication: %w", err)
	}

	normStored, err := normaliseDefinition(stored)
	if err != nil {
		// Deliberately drops err. A decode failure here carries fragments of
		// the STORED definition — an unknown node kind name, a malformed field
		// — and this error goes to whoever attempted the publish, who is not
		// necessarily entitled to read what is stored under this key. The
		// caller gets the key it already supplied and nothing it did not.
		//
		// The dropped detail is NOT logged anywhere: this type holds no logger,
		// and adding one is out of scope here. An operator diagnosing this must
		// read the row directly. Stated plainly because an earlier wording
		// claimed the detail "stays server-side", implying a server-side record
		// that does not exist.
		return false, errors.New("cannot decode the stored definition")
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

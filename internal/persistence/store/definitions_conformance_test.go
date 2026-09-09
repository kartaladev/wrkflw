package store_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/gateway"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/internal/database/transaction"
	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/kartaladev/wrkflw/internal/persistence/dialect"
	"github.com/kartaladev/wrkflw/internal/persistence/store"
	"github.com/kartaladev/wrkflw/persistence"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// Compile-time assertion: *store.DefinitionStore must satisfy the public facade
// interface persistence.DefinitionStore (PublishDefinition + Lookup). This guard
// lives in the external test package so the assertion can import both
// internal/persistence/store and persistence without creating an import cycle.
var _ persistence.DefinitionStore = (*store.DefinitionStore)(nil)

// richConformanceDefinition builds a realistic ProcessDefinition with multiple
// typed nodes and sequence flows to exercise the JSON round-trip on all dialects.
// All fields of model.ProcessDefinition and its nested types must survive the
// round-trip; the equality assertion in the rich-round-trip test case validates
// this exhaustively.
//
// The definition is also VALID: PublishDefinition runs model.Validate before it
// writes, so an exhaustive fixture now has to be a well-formed process as well
// as a field-coverage vehicle. Four structural properties are load-bearing and
// must survive any future edit — each one stands for a rule this fixture used
// to break:
//
//   - Two starts, each with exactly ONE trigger. A single start carrying both a
//     signal and a message correlator is an ambiguous trigger; multiple
//     EVENT-triggered starts are legal (only multiple MANUAL starts are not),
//     so the signal and the message start are split apart to keep coverage of
//     both options.
//   - Every non-end node has an outgoing flow. That includes the two boundary
//     events, the sub-process and the call activity.
//   - The sub-process and the call activity are reachable from a start
//     (fulfill -> sub -> call -> end), not floating.
//   - The user task carries a completion action alongside its compensate
//     action: for a user/receive task the completion action IS the forward
//     action, and compensating a node that never ran forward is dead config.
func richConformanceDefinition() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "order-process",
		Version: 2,
		Nodes: []model.Node{
			// Signal-triggered start.
			event.NewStart("start",
				event.WithName("Order Received"),
				event.WithSignalName("sig-order"),
			),
			// Message-triggered start. Split from the signal start above: one
			// start carrying both triggers is ambiguous, two event starts are
			// not, and splitting keeps both options covered.
			event.NewStart("start-msg",
				event.WithName("Order Message Received"),
				event.WithMessageCorrelator("msg-order", "vars.orderID"),
			),
			activity.NewUserTask("review", activity.WithEligibleRoles("reviewer", "manager"),
				activity.WithName("Review Order"),
				activity.WithEligibleExpr("vars.amount > 100"),
				activity.WithWaitDeadline(schedule.AfterExpr("PT24H"), "sla-breach"), activity.WithDeadlineAction("notify-manager"),
				activity.WithWaitAction(schedule.EveryExpr("PT6H"), "send-reminder"),
				// The forward action the compensate action undoes.
				activity.WithCompletionAction("record-review"),
				activity.WithCompensateAction("cancel-review"),
			),
			gateway.NewExclusive("approve", gateway.WithName("Approved?")),
			activity.NewServiceTask("fulfill", activity.WithTaskAction("fulfillment-service"),
				activity.WithName("Fulfill Order"),
				activity.WithCompensateAction("rollback-fulfillment"),
			),
			activity.NewSubProcess("sub", &model.ProcessDefinition{
				ID:      "nested",
				Version: 1,
				Nodes: []model.Node{
					event.NewStart("n-start"),
					event.NewEnd("n-end"),
				},
				Flows: []flow.SequenceFlow{
					{ID: "nf1", Source: "n-start", Target: "n-end"},
				},
			}, activity.WithName("Nested Sub")),
			event.NewBoundary("boundary-err", "fulfill",
				event.WithBoundaryErrorCode("FULFILLMENT_ERROR"),
			),
			event.NewBoundary("boundary-sig", "review",
				event.WithBoundaryNonInterrupting(),
				event.WithSignalName("sig-cancel"),
			),
			activity.NewCallActivity("call", model.Version("sub-def", 3),
				activity.WithName("Call Sub-process"),
			),
			event.NewEnd("end", event.WithName("Done")),
			event.NewEnd("err-end", event.WithErrorCode("ORDER_ERROR")),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "review"},
			{ID: "f1m", Source: "start-msg", Target: "review"},
			{ID: "f2", Source: "review", Target: "approve"},
			{ID: "f3", Source: "approve", Target: "fulfill", Condition: "vars.approved == true", IsDefault: false},
			{ID: "f4", Source: "approve", Target: "end", Condition: "vars.approved != true", IsDefault: true},
			// fulfill -> sub -> call -> end is what makes the sub-process and
			// the call activity both reachable and non-dead-ended.
			{ID: "f5", Source: "fulfill", Target: "sub"},
			{ID: "f6", Source: "sub", Target: "call"},
			{ID: "f7", Source: "call", Target: "end"},
			// Every boundary event needs somewhere to go.
			{ID: "f8", Source: "boundary-err", Target: "err-end"},
			{ID: "f9", Source: "boundary-sig", Target: "end"},
			{ID: "sla-breach", Source: "review", Target: "err-end"},
		},
		CancelActions: []string{"cancel-order"},
	}
}

// TestDefinitionStorePublishGetRoundTrip verifies the basic PublishDefinition → GetDefinition
// round-trip on all 3 dialects.
func TestDefinitionStorePublishGetRoundTrip(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		ds, err := store.NewDefinitionStore(b.conn, b.dialect)
		require.NoError(t, err)
		// compile-time interface checks
		var _ kernel.DefinitionRegistry = ds

		def := minimalValidDef("d-rr", 1)
		require.NoError(t, ds.PublishDefinition(t.Context(), def), "%s: PublishDefinition", b.name)

		got, err := ds.GetDefinition(t.Context(), "d-rr", 1)
		require.NoError(t, err, "%s: GetDefinition", b.name)
		assert.Equal(t, "d-rr", got.ID, "%s: ID round-trip", b.name)
		assert.Equal(t, 1, got.Version, "%s: Version round-trip", b.name)
	})
}

// TestDefinitionStoreLookupByQualifier verifies Lookup(ctx, model.Qualifier) for
// latest, pinned, and not-found cases on all 3 dialects.
func TestDefinitionStoreLookupByQualifier(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		ds, err := store.NewDefinitionStore(b.conn, b.dialect)
		require.NoError(t, err)

		v1 := minimalValidDef("d-lq", 1)
		v2 := minimalValidDef("d-lq", 2)
		require.NoError(t, ds.PublishDefinition(t.Context(), v1), "%s: PublishDefinition v1", b.name)
		require.NoError(t, ds.PublishDefinition(t.Context(), v2), "%s: PublishDefinition v2", b.name)

		// Pinned: Version(id, 1) must return v1.
		got1, err := ds.Lookup(t.Context(), model.Version("d-lq", 1))
		require.NoError(t, err, "%s: Lookup pinned v1", b.name)
		assert.Equal(t, 1, got1.Version, "%s: pinned must return v1", b.name)

		// Pinned: Version(id, 2) must return v2.
		got2, err := ds.Lookup(t.Context(), model.Version("d-lq", 2))
		require.NoError(t, err, "%s: Lookup pinned v2", b.name)
		assert.Equal(t, 2, got2.Version, "%s: pinned must return v2", b.name)

		// Latest: Latest(id) must return the highest version.
		gotLatest, err := ds.Lookup(t.Context(), model.Latest("d-lq"))
		require.NoError(t, err, "%s: Lookup latest", b.name)
		assert.Equal(t, 2, gotLatest.Version, "%s: latest must return highest version", b.name)

		// Not found: pinned qualifier for non-existent version must wrap ErrDefinitionNotFound.
		_, err = ds.Lookup(t.Context(), model.Version("d-lq", 99))
		require.ErrorIs(t, err, kernel.ErrDefinitionNotFound,
			"%s: pinned not-found must wrap ErrDefinitionNotFound; got %v", b.name, err)

		// Not found: latest qualifier for non-existent ID must wrap ErrDefinitionNotFound.
		_, err = ds.Lookup(t.Context(), model.Latest("no-such-id"))
		require.ErrorIs(t, err, kernel.ErrDefinitionNotFound,
			"%s: latest not-found must wrap ErrDefinitionNotFound; got %v", b.name, err)
	})
}

// TestDefinitionStoreGetNotFound verifies that GetDefinition wraps
// kernel.ErrDefinitionNotFound when no row matches (defID, version).
func TestDefinitionStoreGetNotFound(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		ds, err := store.NewDefinitionStore(b.conn, b.dialect)
		require.NoError(t, err)

		_, err = ds.GetDefinition(t.Context(), "no-such-def", 99)
		require.Error(t, err, "%s: GetDefinition missing must error", b.name)
		require.ErrorIs(t, err, kernel.ErrDefinitionNotFound,
			"%s: must wrap ErrDefinitionNotFound; got %v", b.name, err)
	})
}

// TestDefinitionStoreLookupCancelledContext verifies that Lookup propagates ctx
// to the SQL query: a pre-cancelled context causes the query to fail immediately.
func TestDefinitionStoreLookupCancelledContext(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		ds, err := store.NewDefinitionStore(b.conn, b.dialect)
		require.NoError(t, err)

		// Seed a real definition so the query would otherwise succeed.
		require.NoError(t,
			ds.PublishDefinition(t.Context(), minimalValidDef("cancel-ctx-"+b.name, 1)),
			"%s: seed definition", b.name,
		)

		cctx, cancel := context.WithCancel(t.Context())
		cancel() // pre-cancel

		_, err = ds.Lookup(cctx, model.Version("cancel-ctx-"+b.name, 1))
		require.Error(t, err, "%s: cancelled context must return an error", b.name)
	})
}

// TestDefinitionStoreRichRoundTrip verifies that a realistic ProcessDefinition
// with multiple typed nodes, sequence flows, and option-bearing fields (deadline,
// signal, compensation) survives the JSON store/load round-trip on all dialects.
func TestDefinitionStoreRichRoundTrip(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		ds, err := store.NewDefinitionStore(b.conn, b.dialect)
		require.NoError(t, err)

		orig := richConformanceDefinition()
		require.NoError(t, ds.PublishDefinition(t.Context(), orig), "%s: PublishDefinition rich", b.name)

		got, err := ds.GetDefinition(t.Context(), orig.ID, orig.Version)
		require.NoError(t, err, "%s: GetDefinition rich", b.name)
		assert.Equal(t, orig, got, "%s: all fields must survive the JSON round-trip", b.name)
	})
}

// overMaxDefinitionVersion is one past the largest publishable version.
//
// It is a VARIABLE of type int64 on purpose. Written as the constant expression
// kernel.MaxDefinitionVersion+1 it does not compile where int is 32 bits — the
// untyped constant 2147483648 overflows int — so the file would fail to build
// under GOARCH=386 rather than fail a test. Go through int64 and convert at run
// time instead.
var overMaxDefinitionVersion int64 = int64(kernel.MaxDefinitionVersion) + 1

// intCanExceedMaxDefinitionVersion reports whether this platform's int can even
// represent a version above the bound. Where it cannot — a 32-bit int, whose
// maximum IS kernel.MaxDefinitionVersion — an over-limit version is
// unrepresentable, so there is nothing to test and the cases below skip.
const intCanExceedMaxDefinitionVersion = math.MaxInt > math.MaxInt32

// ── Publish semantics: immutable, idempotent-if-identical ────────────────────

// minimalValidDef returns the smallest definition that passes model.Validate:
// one manual start wired straight to one end event. It mirrors the helper of
// the same name in runtime/kernel's tests; the two cannot be shared because
// they live in different test packages.
func minimalValidDef(id string, version int) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      id,
		Version: version,
		Nodes:   []model.Node{event.NewStart("s"), event.NewEnd("e")},
		Flows:   []flow.SequenceFlow{{ID: "f1", Source: "s", Target: "e"}},
	}
}

// rawDefinition reads the definition column exactly as stored, bypassing the
// JSON decode.
//
// The bytes it returns are the DATABASE's encoding, not the one that was
// written: Postgres stores the column as JSONB and MySQL as JSON, and both
// re-serialise on write (JSONB orders keys by length then bytes). Only SQLite,
// whose column is TEXT, returns the bytes verbatim. So this is usable to show
// that the stored encoding DIFFERS from Go's canonical marshal — which is the
// precondition the normalisation case needs — and not to assert any particular
// byte sequence.
func rawDefinition(t *testing.T, b backend, defID string, version int) []byte {
	t.Helper()

	s, err := store.New(b.conn, b.dialect)
	require.NoError(t, err)

	var data []byte
	require.NoError(t,
		s.QuerierForTest(t.Context()).QueryRow(t.Context(), b.dialect.Rebind(
			`SELECT definition FROM wrkflw_definitions WHERE def_id = ? AND version = ?`),
			defID, version,
		).Scan(&data),
		"read raw definition %s:%d", defID, version,
	)
	return data
}

// countDefinitionRows reports how many rows exist for defID, so an idempotent
// re-publish can be shown to leave exactly one.
func countDefinitionRows(t *testing.T, b backend, defID string) int {
	t.Helper()

	s, err := store.New(b.conn, b.dialect)
	require.NoError(t, err)

	var n int
	require.NoError(t,
		s.QuerierForTest(t.Context()).QueryRow(t.Context(), b.dialect.Rebind(
			`SELECT COUNT(*) FROM wrkflw_definitions WHERE def_id = ?`), defID,
		).Scan(&n),
		"count definition rows for %s", defID,
	)
	return n
}

// countDefinitionRowsWithPrefix counts rows whose def_id starts with prefix.
//
// It exists because an ID that no backend can store is also an ID that some
// backend cannot QUERY: Postgres rejects invalid UTF-8 and NUL bytes as bind
// parameters with SQLSTATE 22021, so asking "how many rows have this exact
// hostile id?" is not answerable there. prefix must be plain ASCII; the point
// is to ask a question every backend can represent.
func countDefinitionRowsWithPrefix(t *testing.T, b backend, prefix string) int {
	t.Helper()

	s, err := store.New(b.conn, b.dialect)
	require.NoError(t, err)

	var n int
	require.NoError(t,
		s.QuerierForTest(t.Context()).QueryRow(t.Context(), b.dialect.Rebind(
			`SELECT COUNT(*) FROM wrkflw_definitions WHERE def_id LIKE ?`), prefix+"%",
		).Scan(&n),
		"count definition rows with prefix %q", prefix,
	)
	return n
}

// divergentEncoding re-encodes def into JSON that decodes to exactly the same
// definition but is byte-different from what json.Marshal currently emits, in
// the two ways a serialisation change realistically shows up:
//
//   - key order: re-marshalling through a map emits the top-level keys
//     alphabetically rather than in struct-declaration order;
//   - an omitted-empty field written out explicitly: "cancel_actions": null,
//     which the current marshaller omits entirely and the unmarshaller reads
//     back as the same nil slice.
//
// This stands in for a library upgrade that changes how an UNCHANGED definition
// serialises. Normalised comparison has to absorb it without reporting a
// content conflict; a naive bytes.Equal against the stored column would not.
func divergentEncoding(t *testing.T, def *model.ProcessDefinition) []byte {
	t.Helper()

	canonical, err := json.Marshal(def)
	require.NoError(t, err)

	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(canonical, &fields))
	require.NotContains(t, fields, "cancel_actions",
		"this helper assumes the canonical marshal omits the empty cancel_actions field")
	fields["cancel_actions"] = json.RawMessage("null")

	divergent, err := json.Marshal(fields)
	require.NoError(t, err)

	// Guard against the case degenerating into a tautology: if the two
	// encodings were byte-equal, it would prove nothing.
	require.NotEqual(t, string(canonical), string(divergent),
		"divergent encoding must differ from the canonical one for this case to mean anything")
	return divergent
}

// seedDefinitionRow inserts a definitions row directly through SQL, bypassing
// the store's write path entirely, so a test can plant an exact stored encoding
// that PublishDefinition would never itself produce.
func seedDefinitionRow(t *testing.T, b backend, defID string, version int, definition []byte) {
	t.Helper()

	s, err := store.New(b.conn, b.dialect)
	require.NoError(t, err)

	// created_at mirrors what the store's own timeArg would bind: a TEXT
	// RFC3339 string on SQLite, a time.Time on Postgres and MySQL.
	var createdAt any = time.Now().UTC()
	if b.dialect.TimestampsAsText() {
		createdAt = time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z07:00")
	}

	_, err = s.QuerierForTest(t.Context()).Exec(t.Context(), b.dialect.Rebind(
		`INSERT INTO wrkflw_definitions (def_id, version, definition, created_at)
		 VALUES (?,?,?,?)`),
		defID, version, definition, createdAt,
	)
	require.NoError(t, err, "%s: seed definitions row %s:%d", b.name, defID, version)
}

// TestDefinitionStorePublishImmutable pins the publish contract on all three
// dialects: a published (def_id, version) is immutable. Re-publishing identical
// content is a successful no-op; re-publishing different content under the same
// version is refused and the first content survives; a definition that fails
// model.Validate — including one with Version == 0 — is refused before anything
// is written.
//
// The cases run serially inside each dialect: they share one database (and, on
// SQLite, one connection), and running them in parallel would trade a real
// assertion for lock contention. Each case uses its own def_id, so they do not
// interfere.
func TestDefinitionStorePublishImmutable(t *testing.T) {
	type testCase struct {
		name   string
		seed   func(t *testing.T, b backend, ds *store.DefinitionStore)
		def    *model.ProcessDefinition
		assert func(t *testing.T, b backend, ds *store.DefinitionStore, err error)
	}

	cases := []testCase{
		{
			// CODE-F6: sameDefinitionContent rests entirely on
			// marshal(unmarshal(x)) == marshal(x) holding over the polymorphic
			// model.Node surface, and that identity is exactly where round-trip
			// drift would appear. Proving it on a two-node definition proves
			// almost nothing; the rich fixture is where it can actually break,
			// and if it ever does, EVERY republish of a real definition starts
			// returning ErrDefinitionExists.
			name: "identical RICH content twice is an idempotent no-op leaving one row",
			seed: func(t *testing.T, b backend, ds *store.DefinitionStore) {
				require.NoError(t, ds.PublishDefinition(t.Context(), richConformanceDefinition()),
					"%s: first publish of the rich fixture", b.name)
			},
			def: richConformanceDefinition(),
			assert: func(t *testing.T, b backend, ds *store.DefinitionStore, err error) {
				require.NoError(t, err,
					"%s: republishing the rich fixture unchanged must be a no-op", b.name)
				assert.Equal(t, 1, countDefinitionRows(t, b, richConformanceDefinition().ID),
					"%s: idempotent republish must leave exactly one row", b.name)

				got, err := ds.GetDefinition(t.Context(), richConformanceDefinition().ID, richConformanceDefinition().Version)
				require.NoError(t, err)
				assert.Equal(t, richConformanceDefinition(), got,
					"%s: stored rich definition must be unchanged", b.name)
			},
		},
		{
			name: "identical content twice is an idempotent no-op leaving one row",
			seed: func(t *testing.T, b backend, ds *store.DefinitionStore) {
				require.NoError(t, ds.PublishDefinition(t.Context(), minimalValidDef("pub-same", 1)),
					"%s: first publish", b.name)
			},
			def: minimalValidDef("pub-same", 1),
			assert: func(t *testing.T, b backend, ds *store.DefinitionStore, err error) {
				require.NoError(t, err, "%s: re-publishing identical content must succeed", b.name)
				assert.Equal(t, 1, countDefinitionRows(t, b, "pub-same"),
					"%s: idempotent publish must leave exactly one row", b.name)

				got, err := ds.GetDefinition(t.Context(), "pub-same", 1)
				require.NoError(t, err)
				assert.Equal(t, minimalValidDef("pub-same", 1), got,
					"%s: stored content must be unchanged", b.name)
			},
		},
		{
			name: "different content under the same version is refused and the first survives",
			seed: func(t *testing.T, b backend, ds *store.DefinitionStore) {
				first := minimalValidDef("pub-conflict", 1)
				first.CancelActions = []string{"action-first"}
				require.NoError(t, ds.PublishDefinition(t.Context(), first), "%s: first publish", b.name)
			},
			def: func() *model.ProcessDefinition {
				second := minimalValidDef("pub-conflict", 1)
				second.CancelActions = []string{"action-second"}
				return second
			}(),
			assert: func(t *testing.T, b backend, ds *store.DefinitionStore, err error) {
				require.Error(t, err, "%s: republishing different content must be refused", b.name)
				require.ErrorIs(t, err, kernel.ErrDefinitionExists,
					"%s: must wrap ErrDefinitionExists; got %v", b.name, err)
				assert.ErrorContains(t, err, "pub-conflict:1",
					"%s: error must name the id:version key", b.name)
				assert.ErrorContains(t, err, "differs from the published version",
					"%s: error must say the content differs from the published version", b.name)

				got, err := ds.GetDefinition(t.Context(), "pub-conflict", 1)
				require.NoError(t, err)
				assert.Equal(t, []string{"action-first"}, got.CancelActions,
					"%s: the first published content must survive", b.name)
			},
		},
		{
			// Where this case has teeth: SQLite, whose definition column is
			// TEXT and therefore hands the seeded bytes back verbatim, so the
			// divergent encoding really does reach the comparison. On Postgres
			// (JSONB) and MySQL (JSON) the column re-serialises the seed before
			// anything reads it, so those two exercise the path but do not
			// prove that a divergent stored encoding is tolerated. A reader who
			// sees this pass on Postgres should not conclude Postgres proved
			// it.
			name: "a stored encoding differing only in serialisation is not a conflict",
			seed: func(t *testing.T, b backend, _ *store.DefinitionStore) {
				// Seeded through raw SQL, not through the store, so the stored
				// bytes are exactly the divergent encoding and not anything the
				// write path would have normalised on the way in.
				seedDefinitionRow(t, b, "pub-normalise", 1,
					divergentEncoding(t, minimalValidDef("pub-normalise", 1)))
			},
			def: minimalValidDef("pub-normalise", 1),
			assert: func(t *testing.T, b backend, ds *store.DefinitionStore, err error) {
				// Precondition: the stored encoding must really differ from
				// Go's canonical marshal, or the case proves nothing.
				canonical, mErr := json.Marshal(minimalValidDef("pub-normalise", 1))
				require.NoError(t, mErr)
				require.NotEqual(t, string(canonical), string(rawDefinition(t, b, "pub-normalise", 1)),
					"%s: stored encoding must differ from the canonical marshal for this case to mean anything", b.name)

				require.NoError(t, err,
					"%s: a stored encoding that normalises to the incoming one is not a conflict", b.name)
				assert.Equal(t, 1, countDefinitionRows(t, b, "pub-normalise"),
					"%s: the no-op must leave exactly one row", b.name)

				got, err := ds.GetDefinition(t.Context(), "pub-normalise", 1)
				require.NoError(t, err)
				assert.Equal(t, minimalValidDef("pub-normalise", 1), got,
					"%s: the stored definition must be unchanged", b.name)
			},
		},
		{
			name: "a definition failing model.Validate is refused and writes no row",
			def:  &model.ProcessDefinition{ID: "pub-invalid", Version: 1},
			assert: func(t *testing.T, b backend, ds *store.DefinitionStore, err error) {
				require.Error(t, err, "%s: an invalid definition must be refused", b.name)
				require.ErrorIs(t, err, kernel.ErrInvalidDefinition,
					"%s: must wrap ErrInvalidDefinition; got %v", b.name, err)
				assert.ErrorIs(t, err, model.ErrNoStartEvent,
					"%s: must also wrap the broken rule; got %v", b.name, err)

				assert.Equal(t, 0, countDefinitionRows(t, b, "pub-invalid"),
					"%s: a refused definition must write no row", b.name)
			},
		},
		{
			// SEC-F1. 255 is the narrowest backend limit (MySQL
			// def_id VARCHAR(255)); at exactly the limit a publish must
			// succeed on every dialect, or the bound is wrong.
			name: "a definition ID at the length limit is accepted",
			def:  minimalValidDef(strings.Repeat("a", kernel.MaxDefinitionIDRunes), 1),
			assert: func(t *testing.T, b backend, ds *store.DefinitionStore, err error) {
				require.NoError(t, err,
					"%s: an ID of exactly %d runes must publish", b.name, kernel.MaxDefinitionIDRunes)

				id := strings.Repeat("a", kernel.MaxDefinitionIDRunes)
				got, err := ds.GetDefinition(t.Context(), id, 1)
				require.NoError(t, err, "%s: and must be readable back under the FULL id", b.name)
				assert.Equal(t, id, got.ID,
					"%s: the stored ID must be the one published, not a truncation", b.name)
			},
		},
		{
			// SEC-F1, the fail-open this closes. One rune over the limit,
			// MySQL's INSERT IGNORE would truncate to 255 and report success,
			// storing the row under a key the caller never chose. The gate
			// refuses it on every dialect instead, before any I/O.
			name: "a definition ID one rune over the limit is refused and writes no row",
			// A distinct rune from the at-the-limit case above: that case
			// legitimately publishes 255 "a"s, and this case has to be able to
			// assert that NOTHING exists under its own truncated prefix.
			def: minimalValidDef(strings.Repeat("b", kernel.MaxDefinitionIDRunes+1), 1),
			assert: func(t *testing.T, b backend, ds *store.DefinitionStore, err error) {
				require.Error(t, err, "%s: an over-long ID must be refused", b.name)
				require.ErrorIs(t, err, kernel.ErrInvalidDefinition,
					"%s: must wrap ErrInvalidDefinition; got %v", b.name, err)
				assert.ErrorIs(t, err, kernel.ErrDefinitionIDTooLong,
					"%s: must also wrap ErrDefinitionIDTooLong; got %v", b.name, err)

				assert.Equal(t, 0, countDefinitionRows(t, b, strings.Repeat("b", kernel.MaxDefinitionIDRunes+1)),
					"%s: the over-long ID must write no row", b.name)
				// The fail-open this closes: on MySQL an unguarded INSERT
				// IGNORE would have stored the row under the 255-rune
				// TRUNCATION of this ID. Nothing may appear there either.
				assert.Equal(t, 0, countDefinitionRows(t, b, strings.Repeat("b", kernel.MaxDefinitionIDRunes)),
					"%s: and nothing may be stored under the truncated prefix", b.name)
			},
		},
		{
			// Encoding, not length. utf8.RuneCountInString counts each bad byte
			// as one RuneError, so an invalid-UTF-8 ID sails through the length
			// bound. Measured: Postgres refuses it (SQLSTATE 22021), MySQL
			// refuses it (Error 1366) through the current statement, and SQLite
			// stores the raw bytes — so SQLite alone is why the gate is needed.
			// Under the earlier INSERT IGNORE statement MySQL instead truncated
			// at the first bad byte, which made two distinct IDs one row and
			// let a lookup for one identity return another's definition.
			name: "a definition ID that is not valid UTF-8 is refused and writes no row",
			def:  minimalValidDef("utf8-bad-\xff\xfe-tail", 1),
			assert: func(t *testing.T, b backend, ds *store.DefinitionStore, err error) {
				require.Error(t, err, "%s: invalid UTF-8 must be refused", b.name)
				require.ErrorIs(t, err, kernel.ErrInvalidDefinition,
					"%s: must wrap ErrInvalidDefinition; got %v", b.name, err)
				assert.ErrorIs(t, err, kernel.ErrDefinitionIDNotUTF8,
					"%s: must also wrap ErrDefinitionIDNotUTF8; got %v", b.name, err)

				// Asked by ASCII prefix, not by the hostile ID itself: Postgres
				// rejects that ID as a bind parameter too, so the exact-match
				// question is unanswerable there. This covers both the ID as
				// sent and the prefix MySQL would have truncated it to.
				assert.Equal(t, 0, countDefinitionRowsWithPrefix(t, b, "utf8-bad-"),
					"%s: nothing may be stored under the ID or its truncation", b.name)
			},
		},
		{
			// The accept side of the encoding bound: multibyte UTF-8 is
			// perfectly storable and must not be swept up by the check.
			name: "a definition ID of valid multibyte UTF-8 is accepted",
			def:  minimalValidDef("utf8-good-héllo-wörld-日本語", 1),
			assert: func(t *testing.T, b backend, ds *store.DefinitionStore, err error) {
				require.NoError(t, err, "%s: valid multibyte UTF-8 must publish", b.name)

				got, err := ds.GetDefinition(t.Context(), "utf8-good-héllo-wörld-日本語", 1)
				require.NoError(t, err, "%s: and read back under the same ID", b.name)
				assert.Equal(t, "utf8-good-héllo-wörld-日本語", got.ID,
					"%s: stored faithfully, byte for byte", b.name)
			},
		},
		{
			// NUL is VALID UTF-8, so the encoding check above cannot catch it
			// and it needs its own. Measured: Postgres cannot store NUL in a
			// text column at all (SQLSTATE 22021); MySQL and SQLite accept it.
			name: "a definition ID containing a NUL byte is refused and writes no row",
			def:  minimalValidDef("nul-mid\x00tail", 1),
			assert: func(t *testing.T, b backend, ds *store.DefinitionStore, err error) {
				require.Error(t, err, "%s: an embedded NUL must be refused", b.name)
				require.ErrorIs(t, err, kernel.ErrInvalidDefinition,
					"%s: must wrap ErrInvalidDefinition; got %v", b.name, err)
				assert.ErrorIs(t, err, kernel.ErrDefinitionIDContainsNUL,
					"%s: must also wrap ErrDefinitionIDContainsNUL; got %v", b.name, err)
				// It is valid UTF-8, so it must NOT be reported as an encoding
				// fault — that would be the right refusal for the wrong reason.
				assert.NotErrorIs(t, err, kernel.ErrDefinitionIDNotUTF8,
					"%s: NUL is valid UTF-8; the encoding sentinel must not fire", b.name)

				assert.Equal(t, 0, countDefinitionRowsWithPrefix(t, b, "nul-mid"),
					"%s: nothing may be stored", b.name)
			},
		},
		{
			// The version column is INT (32-bit) on MySQL and Postgres while
			// Go's Version is 64-bit. At the limit every backend stores it.
			name: "a version at the backend maximum is accepted",
			def:  minimalValidDef("ver-at-max", kernel.MaxDefinitionVersion),
			assert: func(t *testing.T, b backend, ds *store.DefinitionStore, err error) {
				require.NoError(t, err, "%s: MaxDefinitionVersion must publish", b.name)

				got, err := ds.GetDefinition(t.Context(), "ver-at-max", kernel.MaxDefinitionVersion)
				require.NoError(t, err, "%s: and read back at the version published", b.name)
				assert.Equal(t, kernel.MaxDefinitionVersion, got.Version, "%s: stored version", b.name)
			},
		},
		{
			// One over. Measured: SQLite stores it, Postgres refuses to encode
			// it for int4, and MySQL refuses it (Error 1264) through the
			// current statement — so SQLite alone is why the gate is needed.
			// Under the earlier INSERT IGNORE statement MySQL instead CLAMPED
			// it to 2147483647 and reported success, so the publish returned
			// nil and the row landed at a version the caller never asked for.
			name: "a version one over the backend maximum is refused and writes no row",
			def:  minimalValidDef("ver-over-max", int(overMaxDefinitionVersion)),
			assert: func(t *testing.T, b backend, ds *store.DefinitionStore, err error) {
				if !intCanExceedMaxDefinitionVersion {
					t.Skip("int is 32-bit here, so a version above MaxDefinitionVersion is unrepresentable")
				}
				require.Error(t, err, "%s: an out-of-range version must be refused", b.name)
				require.ErrorIs(t, err, kernel.ErrInvalidDefinition,
					"%s: must wrap ErrInvalidDefinition; got %v", b.name, err)
				assert.ErrorIs(t, err, kernel.ErrDefinitionVersionTooLarge,
					"%s: must also wrap ErrDefinitionVersionTooLarge; got %v", b.name, err)

				assert.Equal(t, 0, countDefinitionRows(t, b, "ver-over-max"),
					"%s: nothing may be stored at any version, clamped or otherwise", b.name)
			},
		},
		{
			name: "version 0 is refused before any I/O",
			def:  minimalValidDef("pub-zero", 0),
			assert: func(t *testing.T, b backend, ds *store.DefinitionStore, err error) {
				require.Error(t, err, "%s: version 0 must be refused", b.name)
				require.ErrorIs(t, err, kernel.ErrInvalidDefinition,
					"%s: must wrap ErrInvalidDefinition; got %v", b.name, err)
				assert.ErrorIs(t, err, model.ErrInvalidVersion,
					"%s: must also wrap model.ErrInvalidVersion; got %v", b.name, err)

				assert.Equal(t, 0, countDefinitionRows(t, b, "pub-zero"),
					"%s: version 0 must not reach the database", b.name)
			},
		},
	}

	forEachDialect(t, func(t *testing.T, b backend) {
		ds, err := store.NewDefinitionStore(b.conn, b.dialect)
		require.NoError(t, err)

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				if tc.seed != nil {
					tc.seed(t, b, ds)
				}
				tc.assert(t, b, ds, ds.PublishDefinition(t.Context(), tc.def))
			})
		}
	})
}

// TestDefinitionStorePublishIgnoresClientFoundRows pins that the publish
// outcome does not depend on MySQL's clientFoundRows connection flag.
//
// The flag lives in the CONSUMER's DSN, not in this package. It switches what
// MySQL reports as affected rows: with it set, ON DUPLICATE KEY UPDATE reports
// 1 for a duplicate instead of 0. A publish path that inferred its outcome from
// that count would report a REFUSED publish as success for any consumer whose
// DSN happened to carry the flag.
//
// This was introduced by this PR's own statement change and is fixed here:
// INSERT IGNORE reported 0 regardless of the flag, ON DUPLICATE KEY UPDATE does
// not. The outcome is now read back from stored state instead, which is
// flag-independent.
//
// Both arms are exercised against the same database. Without the
// clientFoundRows arm the test could not fail for the stated reason, because
// the default DSN passes today either way.
func TestDefinitionStorePublishIgnoresClientFoundRows(t *testing.T) {
	dsn := dbtest.RunTestMySQLDSN(t)

	withFlag := dsn
	if strings.Contains(withFlag, "?") {
		withFlag += "&clientFoundRows=true"
	} else {
		withFlag += "?clientFoundRows=true"
	}

	type arm struct {
		name string
		dsn  string
	}
	arms := []arm{
		{name: "default DSN", dsn: dsn},
		{name: "clientFoundRows=true", dsn: withFlag},
	}

	// Migrate once through the plain DSN; both arms address the same schema.
	base, err := sql.Open("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = base.Close() })
	require.NoError(t, persistence.MigrateMySQL(t.Context(), base), "migrate mysql")

	for i, a := range arms {
		t.Run(a.name, func(t *testing.T) {
			db, err := sql.Open("mysql", a.dsn)
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			require.NoError(t, db.PingContext(t.Context()))

			ds, err := store.NewDefinitionStore(db, dialect.NewMySQL())
			require.NoError(t, err)

			// Distinct id per arm so the two cannot interfere.
			id := fmt.Sprintf("cfr-%d", i)

			first := minimalValidDef(id, 1)
			first.CancelActions = []string{"first"}
			require.NoError(t, ds.PublishDefinition(t.Context(), first),
				"%s: a fresh insert must succeed", a.name)

			// The happy path must not become a spurious conflict now that the
			// insert is verified by reading back.
			require.NoError(t, ds.PublishDefinition(t.Context(), minimalValidDef(id, 2)),
				"%s: another fresh version must still publish cleanly", a.name)

			// Identical republish: still an idempotent no-op.
			require.NoError(t, ds.PublishDefinition(t.Context(), first),
				"%s: republishing identical content must be a no-op", a.name)

			// The case the flag used to break: a DIFFERING republish must be
			// refused on BOTH arms.
			second := minimalValidDef(id, 1)
			second.CancelActions = []string{"second"}
			err = ds.PublishDefinition(t.Context(), second)
			require.ErrorIs(t, err, kernel.ErrDefinitionExists,
				"%s: a differing republish must be refused whatever the driver reports; got %v", a.name, err)

			got, err := ds.GetDefinition(t.Context(), id, 1)
			require.NoError(t, err)
			assert.Equal(t, []string{"first"}, got.CancelActions,
				"%s: the first published content must survive", a.name)
		})
	}
}

// TestDefinitionStorePublishUnderContention pins the outcome when two
// transactions publish the same version at once, on Postgres.
//
// It also records a measurement that corrects a premise this work started from.
// "INSERT ... ON CONFLICT DO NOTHING" is often described as returning zero rows
// immediately rather than waiting on an in-flight conflicting insert. Measured
// on Postgres 16, it does the opposite: it BLOCKS until the other transaction
// resolves. That is why the contending publish below cannot observe a
// half-finished state, and why store.ErrConcurrentPublish is a defensive branch
// rather than the routine outcome of a race.
//
// The assertion does not depend on any interleaving: the holder inserts before
// the contender starts, so the contender either waits for the commit or sees
// the committed row, and both routes give the same answer. There is no sleep
// and no deadline here.
func TestDefinitionStorePublishUnderContention(t *testing.T) {
	pool := dbtest.RunTestDatabase(t)
	require.NoError(t, persistence.Migrate(t.Context(), pool), "migrate postgres")
	d := dialect.NewPostgres()

	ds, err := store.NewDefinitionStore(pool, d)
	require.NoError(t, err)

	// Holder: insert (contended:1) with content A and keep the unit open.
	holder, holderCtx, err := transaction.Begin(t.Context(), pool)
	require.NoError(t, err)

	holderDef := minimalValidDef("contended", 1)
	holderDef.CancelActions = []string{"holder-content"}
	holderJSON, err := json.Marshal(holderDef)
	require.NoError(t, err)

	_, err = holder.Exec(holderCtx, d.Rebind(
		`INSERT INTO wrkflw_definitions (def_id, version, definition, created_at) VALUES (?,?,?,?)`),
		"contended", 1, holderJSON, time.Now().UTC(),
	)
	require.NoError(t, err, "holder insert")

	// Contender: publish DIFFERENT content for the same version.
	contender := minimalValidDef("contended", 1)
	contender.CancelActions = []string{"contender-content"}

	result := make(chan error, 1)
	go func() { result <- ds.PublishDefinition(t.Context(), contender) }()

	// Let the holder win. Whether the contender is already blocked on the
	// insert or has not yet reached it, the outcome after this commit is the
	// same, so no synchronisation is needed to make the assertion stable.
	require.NoError(t, holder.Commit(t.Context()), "holder commit")

	err = <-result
	require.Error(t, err, "the contending publish must be refused")
	require.ErrorIs(t, err, kernel.ErrDefinitionExists,
		"contention with different content resolves to ErrDefinitionExists, got %v", err)

	got, err := ds.GetDefinition(t.Context(), "contended", 1)
	require.NoError(t, err)
	assert.Equal(t, []string{"holder-content"}, got.CancelActions,
		"the holder's content must survive: a published version is immutable")
}

// scopedActionDefinition builds a definition through the ORDINARY documented
// route — model.NewBuilder with RegisterActionFunc — so it carries a
// definition-scoped action catalog.
//
// That shape matters and is not interchangeable with the other fixtures here.
// richConformanceDefinition and minimalValidDef are raw struct literals whose
// scoped fields are nil, which is the one shape that CANNOT exercise the
// marshal-only scoped_actions key: ProcessDefinition.MarshalJSON emits it from
// ScopedActionNames() and UnmarshalJSON deliberately drops it. Any normalisation
// that is not symmetric therefore reports a definition as differing from itself,
// and only a builder-built definition can catch that.
func scopedActionDefinition(t *testing.T, id string, version int) *model.ProcessDefinition {
	t.Helper()

	noop := func(context.Context, map[string]any) (map[string]any, error) { return nil, nil }
	def, err := model.NewBuilder(id, version).
		RegisterActionFunc("alpha", noop).
		RegisterActionFunc("beta", noop).
		Add(event.NewStart("s")).
		Add(activity.NewServiceTask("task", activity.WithTaskAction("alpha"))).
		Add(event.NewEnd("e")).
		Connect("s", "task").
		Connect("task", "e").
		Build()
	require.NoError(t, err, "build scoped definition")
	require.NotEmpty(t, def.ScopedActionNames(),
		"this fixture is pointless unless it actually carries scoped actions")
	return def
}

// TestDefinitionStoreScopedRepublishIsIdempotent pins the PR's headline promise
// for the definition shape that can actually break it: republishing the very
// same object must be a successful no-op, not a content conflict.
//
// ProcessDefinition.MarshalJSON emits scoped_actions and UnmarshalJSON drops it,
// so decode->re-encode is not a fixed point for these definitions. Normalising
// only the STORED side compares a definition against itself and finds them
// different, which refuses an identical republish with ErrDefinitionExists —
// exactly the case #112 exists to make a no-op.
func TestDefinitionStoreScopedRepublishIsIdempotent(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		ds, err := store.NewDefinitionStore(b.conn, b.dialect)
		require.NoError(t, err)

		def := scopedActionDefinition(t, "scoped-republish", 1)
		require.NoError(t, ds.PublishDefinition(t.Context(), def),
			"%s: first publish", b.name)

		// The same object, republished. Nothing about it has changed.
		require.NoError(t, ds.PublishDefinition(t.Context(), def),
			"%s: republishing the IDENTICAL object must be a no-op", b.name)

		// And a freshly built, equal definition must behave the same way — a
		// retry after a lost response does not reuse the original pointer.
		require.NoError(t, ds.PublishDefinition(t.Context(), scopedActionDefinition(t, "scoped-republish", 1)),
			"%s: republishing an equal rebuild must also be a no-op", b.name)

		assert.Equal(t, 1, countDefinitionRows(t, b, "scoped-republish"),
			"%s: three identical publishes must leave exactly one row", b.name)

		// A genuine content change under the same version is still refused:
		// the fix must not turn the comparison into "always equal".
		conflicting := scopedActionDefinition(t, "scoped-republish", 1)
		conflicting.CancelActions = []string{"something-else"}
		err = ds.PublishDefinition(t.Context(), conflicting)
		require.ErrorIs(t, err, kernel.ErrDefinitionExists,
			"%s: a real content difference must still be refused; got %v", b.name, err)
	})
}

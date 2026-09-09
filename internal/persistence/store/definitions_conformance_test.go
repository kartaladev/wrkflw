package store_test

import (
	"context"
	"encoding/json"
	"errors"
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

// errPublishBoom is the sentinel a RunInTx unit returns to force a rollback.
var errPublishBoom = errors.New("boom")

// TestDefinitionStorePublishJoinsAmbientTransaction pins that a publish issued
// inside a Store.RunInTx unit is part of that unit: it disappears when the unit
// rolls back and survives when the unit commits.
//
// Both cases are required. The rollback case alone would also pass if
// PublishDefinition simply never wrote anything, so the committing case is what
// makes the rollback case mean "the write joined the ambient transaction"
// rather than "there was no write".
func TestDefinitionStorePublishJoinsAmbientTransaction(t *testing.T) {
	type testCase struct {
		name   string
		defID  string
		unit   func(txCtx context.Context, ds *store.DefinitionStore, def *model.ProcessDefinition) error
		assert func(t *testing.T, b backend, ds *store.DefinitionStore, err error)
	}

	cases := []testCase{
		{
			name:  "a rolled-back unit leaves no row",
			defID: "tx-rollback",
			unit: func(txCtx context.Context, ds *store.DefinitionStore, def *model.ProcessDefinition) error {
				if err := ds.PublishDefinition(txCtx, def); err != nil {
					return err
				}
				return errPublishBoom
			},
			assert: func(t *testing.T, b backend, ds *store.DefinitionStore, err error) {
				require.ErrorIs(t, err, errPublishBoom, "%s: RunInTx must surface the unit's error", b.name)

				_, err = ds.GetDefinition(t.Context(), "tx-rollback", 1)
				require.ErrorIs(t, err, kernel.ErrDefinitionNotFound,
					"%s: the publish must roll back with the unit; got %v", b.name, err)
			},
		},
		{
			name:  "a committed unit leaves the row",
			defID: "tx-commit",
			unit: func(txCtx context.Context, ds *store.DefinitionStore, def *model.ProcessDefinition) error {
				return ds.PublishDefinition(txCtx, def)
			},
			assert: func(t *testing.T, b backend, ds *store.DefinitionStore, err error) {
				require.NoError(t, err, "%s: the committing unit must succeed", b.name)

				got, err := ds.GetDefinition(t.Context(), "tx-commit", 1)
				require.NoError(t, err, "%s: the publish must survive the commit", b.name)
				assert.Equal(t, 1, got.Version, "%s: stored version", b.name)
			},
		},
	}

	forEachDialect(t, func(t *testing.T, b backend) {
		s, err := store.New(b.conn, b.dialect)
		require.NoError(t, err)
		ds, err := store.NewDefinitionStore(b.conn, b.dialect)
		require.NoError(t, err)

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				def := minimalValidDef(tc.defID, 1)
				tc.assert(t, b, ds, s.RunInTx(t.Context(), func(txCtx context.Context) error {
					return tc.unit(txCtx, ds, def)
				}))
			})
		}
	})
}

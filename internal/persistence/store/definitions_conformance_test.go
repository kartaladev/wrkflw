package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/gateway"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/store"
	"github.com/zakyalvan/krtlwrkflw/persistence"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// Compile-time assertion: *store.DefinitionStore must satisfy the public facade
// interface persistence.DefinitionStore (PutDefinition + Lookup). This guard
// lives in the external test package so the assertion can import both
// internal/persistence/store and persistence without creating an import cycle.
var _ persistence.DefinitionStore = (*store.DefinitionStore)(nil)

// richConformanceDefinition builds a realistic ProcessDefinition with multiple
// typed nodes and sequence flows to exercise the JSON round-trip on all dialects.
// All fields of model.ProcessDefinition and its nested types must survive the
// round-trip; the equality assertion in the rich-round-trip test case validates
// this exhaustively.
func richConformanceDefinition() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "order-process",
		Version: 2,
		Nodes: []model.Node{
			event.NewStart("start",
				event.WithName("Order Received"),
				event.WithStartSignal("sig-order"),
				event.WithStartMessage("msg-order", "vars.orderID"),
			),
			activity.NewUserTask("review", []string{"reviewer", "manager"},
				activity.WithName("Review Order"),
				activity.WithEligibilityExpr("vars.amount > 100"),
				activity.WithDeadline("PT24H", "sla-breach", "notify-manager"),
				activity.WithReminder("PT6H", "send-reminder"),
				activity.WithCompensation("cancel-review"),
			),
			gateway.NewExclusive("approve", "Approved?"),
			activity.NewServiceTask("fulfill", activity.WithActionName("fulfillment-service"),
				activity.WithName("Fulfill Order"),
				activity.WithCompensation("rollback-fulfillment"),
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
				event.WithBoundarySignal("sig-cancel"),
			),
			activity.NewCallActivity("call", "sub-def:3",
				activity.WithName("Call Sub-process"),
			),
			event.NewEnd("end", "Done"),
			event.NewErrorEnd("err-end", "ORDER_ERROR"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "review"},
			{ID: "f2", Source: "review", Target: "approve"},
			{ID: "f3", Source: "approve", Target: "fulfill", Condition: "vars.approved == true", IsDefault: false},
			{ID: "f4", Source: "approve", Target: "end", Condition: "vars.approved != true", IsDefault: true},
			{ID: "f5", Source: "fulfill", Target: "end"},
			{ID: "sla-breach", Source: "review", Target: "err-end"},
		},
	}
}

// TestDefinitionStorePutGetRoundTrip verifies the basic Put → GetDefinition
// round-trip on all 3 dialects.
func TestDefinitionStorePutGetRoundTrip(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		ds, err := store.NewDefinitionStore(b.conn, b.dialect)
		require.NoError(t, err)
		// compile-time interface checks
		var _ kernel.DefinitionRegistry = ds

		def := &model.ProcessDefinition{ID: "d-rr", Version: 1}
		require.NoError(t, ds.PutDefinition(t.Context(), def), "%s: PutDefinition", b.name)

		got, err := ds.GetDefinition(t.Context(), "d-rr", 1)
		require.NoError(t, err, "%s: GetDefinition", b.name)
		assert.Equal(t, "d-rr", got.ID, "%s: ID round-trip", b.name)
		assert.Equal(t, 1, got.Version, "%s: Version round-trip", b.name)
	})
}

// TestDefinitionStoreLookupExact verifies Lookup("defID:version") on all 3 dialects.
func TestDefinitionStoreLookupExact(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		ds, err := store.NewDefinitionStore(b.conn, b.dialect)
		require.NoError(t, err)

		def := &model.ProcessDefinition{ID: "d-lx", Version: 1}
		require.NoError(t, ds.PutDefinition(t.Context(), def), "%s: PutDefinition", b.name)

		got, err := ds.Lookup(t.Context(), "d-lx:1")
		require.NoError(t, err, "%s: Lookup exact", b.name)
		assert.Equal(t, "d-lx", got.ID, "%s: ID", b.name)
		assert.Equal(t, 1, got.Version, "%s: Version", b.name)
	})
}

// TestDefinitionStoreLookupLatest verifies that Lookup("defID") returns the
// definition with the highest version when multiple versions exist.
func TestDefinitionStoreLookupLatest(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		ds, err := store.NewDefinitionStore(b.conn, b.dialect)
		require.NoError(t, err)

		require.NoError(t, ds.PutDefinition(t.Context(), &model.ProcessDefinition{ID: "d-ll", Version: 1}))
		require.NoError(t, ds.PutDefinition(t.Context(), &model.ProcessDefinition{ID: "d-ll", Version: 2}))

		got, err := ds.Lookup(t.Context(), "d-ll")
		require.NoError(t, err, "%s: Lookup latest", b.name)
		assert.Equal(t, 2, got.Version, "%s: must return highest version", b.name)
	})
}

// TestDefinitionStoreUpsertOverwrite verifies idempotent upsert semantics:
// putting the same (def_id, version) twice with different content must store
// the second value (no duplicate rows, second content wins).
func TestDefinitionStoreUpsertOverwrite(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		ds, err := store.NewDefinitionStore(b.conn, b.dialect)
		require.NoError(t, err)

		first := &model.ProcessDefinition{ID: "d-up", Version: 1, CancelActions: []string{"action-first"}}
		second := &model.ProcessDefinition{ID: "d-up", Version: 1, CancelActions: []string{"action-second"}}

		require.NoError(t, ds.PutDefinition(t.Context(), first), "%s: first put", b.name)
		require.NoError(t, ds.PutDefinition(t.Context(), second), "%s: second put (upsert)", b.name)

		got, err := ds.GetDefinition(t.Context(), "d-up", 1)
		require.NoError(t, err, "%s: GetDefinition after upsert", b.name)
		assert.Equal(t, []string{"action-second"}, got.CancelActions,
			"%s: upsert must overwrite with second value", b.name)
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

// TestDefinitionStoreLookupNotFound verifies that Lookup wraps
// kernel.ErrDefinitionNotFound for both exact and latest forms.
func TestDefinitionStoreLookupNotFound(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		ds, err := store.NewDefinitionStore(b.conn, b.dialect)
		require.NoError(t, err)

		// Exact ref not found.
		_, err = ds.Lookup(t.Context(), "no-such:1")
		require.ErrorIs(t, err, kernel.ErrDefinitionNotFound,
			"%s: exact Lookup must wrap ErrDefinitionNotFound; got %v", b.name, err)

		// Latest ref not found.
		_, err = ds.Lookup(t.Context(), "no-such-either")
		require.ErrorIs(t, err, kernel.ErrDefinitionNotFound,
			"%s: latest Lookup must wrap ErrDefinitionNotFound; got %v", b.name, err)
	})
}

// TestDefinitionStoreLookupBadVersion verifies that Lookup returns an error
// containing "bad version segment" when the version segment cannot be parsed.
func TestDefinitionStoreLookupBadVersion(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		ds, err := store.NewDefinitionStore(b.conn, b.dialect)
		require.NoError(t, err)

		_, err = ds.Lookup(t.Context(), "proc:notanumber")
		require.Error(t, err, "%s: bad version must error", b.name)
		require.Contains(t, err.Error(), "bad version segment",
			"%s: error must mention 'bad version segment'", b.name)
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
			ds.PutDefinition(t.Context(), &model.ProcessDefinition{ID: "cancel-ctx-" + b.name, Version: 1}),
			"%s: seed definition", b.name,
		)

		cctx, cancel := context.WithCancel(t.Context())
		cancel() // pre-cancel

		_, err = ds.Lookup(cctx, "cancel-ctx-"+b.name+":1")
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
		require.NoError(t, ds.PutDefinition(t.Context(), orig), "%s: PutDefinition rich", b.name)

		got, err := ds.GetDefinition(t.Context(), orig.ID, orig.Version)
		require.NoError(t, err, "%s: GetDefinition rich", b.name)
		assert.Equal(t, orig, got, "%s: all fields must survive the JSON round-trip", b.name)
	})
}

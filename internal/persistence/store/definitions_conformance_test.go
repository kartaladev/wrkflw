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
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
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
				event.WithSignalName("sig-order"),
				event.WithMessageCorrelator("msg-order", "vars.orderID"),
			),
			activity.NewUserTask("review", activity.WithEligibleRoles("reviewer", "manager"),
				activity.WithName("Review Order"),
				activity.WithEligibleExpr("vars.amount > 100"),
				activity.WithWaitDeadline(schedule.AfterExpr("PT24H"), "sla-breach"), activity.WithDeadlineAction("notify-manager"),
				activity.WithWaitAction(schedule.EveryExpr("PT6H"), "send-reminder"),
				activity.WithCompensateAction("cancel-review"),
			),
			gateway.NewExclusive("approve", "Approved?"),
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

// TestDefinitionStoreLookupByQualifier verifies Lookup(ctx, model.Qualifier) for
// latest, pinned, and not-found cases on all 3 dialects.
func TestDefinitionStoreLookupByQualifier(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		ds, err := store.NewDefinitionStore(b.conn, b.dialect)
		require.NoError(t, err)

		v1 := &model.ProcessDefinition{ID: "d-lq", Version: 1}
		v2 := &model.ProcessDefinition{ID: "d-lq", Version: 2}
		require.NoError(t, ds.PutDefinition(t.Context(), v1), "%s: PutDefinition v1", b.name)
		require.NoError(t, ds.PutDefinition(t.Context(), v2), "%s: PutDefinition v2", b.name)

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
		require.NoError(t, ds.PutDefinition(t.Context(), orig), "%s: PutDefinition rich", b.name)

		got, err := ds.GetDefinition(t.Context(), orig.ID, orig.Version)
		require.NoError(t, err, "%s: GetDefinition rich", b.name)
		assert.Equal(t, orig, got, "%s: all fields must survive the JSON round-trip", b.name)
	})
}

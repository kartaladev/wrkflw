package postgres_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/internal/database"
	pg "github.com/zakyalvan/krtlwrkflw/internal/persistence/postgres"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// richDefinition builds a ProcessDefinition with nodes and sequence flows to
// exercise the JSON round-trip fully. Any field that is not serialised will
// cause the equality assertion to fail, surfacing the gap rather than papering
// over it.
func richDefinition() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "order-process",
		Version: 2,
		Nodes: []model.Node{
			model.NewStartEvent("start",
				model.WithName("Order Received"),
				model.WithStartSignal("sig-order"),
				model.WithStartMessage("msg-order", "vars.orderID"),
			),
			model.NewUserTask("review", []string{"reviewer", "manager"},
				model.WithName("Review Order"),
				model.WithEligibilityExpr("vars.amount > 100"),
				model.WithSLA("PT24H", "sla-breach", "notify-manager"),
				model.WithReminder("PT6H", "send-reminder"),
				model.WithCompensation("cancel-review"),
			),
			model.NewExclusiveGateway("approve", "Approved?"),
			model.NewServiceTask("fulfill", "fulfillment-service",
				model.WithName("Fulfill Order"),
				model.WithCompensation("rollback-fulfillment"),
			),
			model.NewSubProcess("sub", &model.ProcessDefinition{
				ID:      "nested",
				Version: 1,
				Nodes: []model.Node{
					model.NewStartEvent("n-start"),
					model.NewEndEvent("n-end"),
				},
				Flows: []model.SequenceFlow{
					{ID: "nf1", Source: "n-start", Target: "n-end"},
				},
			}, model.WithName("Nested Sub")),
			model.NewBoundaryEvent("boundary-err", "fulfill",
				model.WithBoundaryErrorCode("FULFILLMENT_ERROR"),
			),
			model.NewBoundaryEvent("boundary-sig", "review",
				model.BoundaryNonInterrupting(),
				model.WithBoundarySignal("sig-cancel"),
			),
			model.NewCallActivity("call", "sub-def:3",
				model.WithName("Call Sub-process"),
			),
			model.NewEndEvent("end", "Done"),
			model.NewErrorEndEvent("err-end", "ORDER_ERROR"),
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "review"},
			{ID: "f2", Source: "review", Target: "approve"},
			{ID: "f3", Source: "approve", Target: "fulfill", Condition: "vars.approved == true", IsDefault: false},
			{ID: "f4", Source: "approve", Target: "end", Condition: "vars.approved != true", IsDefault: true},
			{ID: "f5", Source: "fulfill", Target: "end"},
			{ID: "sla-breach", Source: "review", Target: "err-end"},
		},
	}
}

// TestDefinitionStoreLookupCancelledContext verifies that Lookup propagates ctx to
// the SQL query: a pre-cancelled context causes the query to fail immediately.
func TestDefinitionStoreLookupCancelledContext(t *testing.T) {
	t.Parallel()
	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))
	ds := pg.NewDefinitionStore(pool)

	// Seed a real definition so the query would otherwise succeed.
	require.NoError(t, ds.PutDefinition(t.Context(), &model.ProcessDefinition{ID: "cancel-ctx", Version: 1}))

	// Pre-cancel the context before calling Lookup.
	cctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := ds.Lookup(cctx, "cancel-ctx:1")
	require.Error(t, err, "Lookup with a cancelled context must return an error")
}

func TestDefinitionStoreLookupBadVersion(t *testing.T) {
	t.Parallel()
	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))
	ds := pg.NewDefinitionStore(pool)

	_, err := ds.Lookup(t.Context(), "d:notanumber")
	require.Error(t, err)
	require.Contains(t, err.Error(), "bad version segment")
}

func TestDefinitionStore(t *testing.T) {
	t.Parallel()
	pool := database.RunTestDatabase(t)
	require.NoError(t, pg.Migrate(t.Context(), pool))
	ds := pg.NewDefinitionStore(pool)

	t.Run("put and get round-trip — simple definition", func(t *testing.T) {
		t.Parallel()
		def := &model.ProcessDefinition{ID: "d", Version: 1}
		require.NoError(t, ds.PutDefinition(t.Context(), def))

		got, err := ds.GetDefinition(t.Context(), "d", 1)
		require.NoError(t, err)
		require.Equal(t, "d", got.ID)
		require.Equal(t, 1, got.Version)
	})

	t.Run("put and lookup via defRef d:1", func(t *testing.T) {
		t.Parallel()
		def := &model.ProcessDefinition{ID: "d-ref", Version: 1}
		require.NoError(t, ds.PutDefinition(t.Context(), def))

		viaRef, err := ds.Lookup(t.Context(), "d-ref:1")
		require.NoError(t, err)
		require.Equal(t, 1, viaRef.Version)
		require.Equal(t, "d-ref", viaRef.ID)
	})

	t.Run("lookup latest without version suffix", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, ds.PutDefinition(t.Context(), &model.ProcessDefinition{ID: "latest-test", Version: 1}))
		require.NoError(t, ds.PutDefinition(t.Context(), &model.ProcessDefinition{ID: "latest-test", Version: 2}))

		got, err := ds.Lookup(t.Context(), "latest-test")
		require.NoError(t, err)
		require.Equal(t, 2, got.Version, "latest version must be returned")
	})

	t.Run("get missing returns ErrDefinitionNotFound", func(t *testing.T) {
		t.Parallel()
		_, err := ds.GetDefinition(t.Context(), "missing", 9)
		require.ErrorIs(t, err, runtime.ErrDefinitionNotFound)
	})

	t.Run("lookup missing defRef returns ErrDefinitionNotFound", func(t *testing.T) {
		t.Parallel()
		_, err := ds.Lookup(t.Context(), "no-such:99")
		require.ErrorIs(t, err, runtime.ErrDefinitionNotFound)
	})

	t.Run("put is idempotent — upsert same (defID, version)", func(t *testing.T) {
		t.Parallel()
		def := &model.ProcessDefinition{ID: "idem", Version: 1}
		require.NoError(t, ds.PutDefinition(t.Context(), def))
		require.NoError(t, ds.PutDefinition(t.Context(), def), "second put on same key must not error")
	})

	t.Run("rich definition full JSON round-trip", func(t *testing.T) {
		t.Parallel()
		orig := richDefinition()
		require.NoError(t, ds.PutDefinition(t.Context(), orig))

		got, err := ds.GetDefinition(t.Context(), orig.ID, orig.Version)
		require.NoError(t, err)
		require.Equal(t, orig, got, "all fields must survive the JSON/JSONB round-trip")
	})
}

package event_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
)

// TestBoundaryNewOptions verifies that the three new boundary options
// (WithBoundaryAction, WithBoundaryErrorExpr, WithBoundaryErrorCheck) each set
// their respective field on the BoundaryEvent node produced by NewBoundary.
func TestBoundaryNewOptions(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		opts   []event.BoundaryOption
		assert func(t *testing.T, b event.BoundaryEvent)
	}

	cases := []testCase{
		{
			name: "WithBoundaryAction sets Action field",
			opts: []event.BoundaryOption{event.WithBoundaryAction("notify-ops")},
			assert: func(t *testing.T, b event.BoundaryEvent) {
				assert.Equal(t, "notify-ops", b.Action)
				assert.Empty(t, b.ErrorExpr)
				assert.Nil(t, b.ErrorCheck)
			},
		},
		{
			name: "WithBoundaryErrorExpr sets ErrorExpr field",
			opts: []event.BoundaryOption{event.WithBoundaryErrorExpr(`_error == "PAYMENT_FAILED"`)},
			assert: func(t *testing.T, b event.BoundaryEvent) {
				assert.Equal(t, `_error == "PAYMENT_FAILED"`, b.ErrorExpr)
				assert.Empty(t, b.Action)
				assert.Nil(t, b.ErrorCheck)
			},
		},
		{
			name: "WithBoundaryErrorCheck sets ErrorCheck field",
			opts: []event.BoundaryOption{
				event.WithBoundaryErrorCheck(func(_ map[string]any, err error) bool {
					return errors.Is(err, errors.New("boom"))
				}),
			},
			assert: func(t *testing.T, b event.BoundaryEvent) {
				assert.NotNil(t, b.ErrorCheck)
				assert.Empty(t, b.Action)
				assert.Empty(t, b.ErrorExpr)
			},
		},
		{
			name: "all three options combined set their respective fields",
			opts: []event.BoundaryOption{
				event.WithBoundaryAction("log-error"),
				event.WithBoundaryErrorExpr(`_error == "TIMEOUT"`),
				event.WithBoundaryErrorCheck(func(_ map[string]any, _ error) bool { return true }),
			},
			assert: func(t *testing.T, b event.BoundaryEvent) {
				assert.Equal(t, "log-error", b.Action)
				assert.Equal(t, `_error == "TIMEOUT"`, b.ErrorExpr)
				assert.NotNil(t, b.ErrorCheck)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			n := event.NewBoundary("b", "host", tc.opts...)
			b, ok := n.(event.BoundaryEvent)
			require.True(t, ok, "NewBoundary must return event.BoundaryEvent, got %T", n)
			tc.assert(t, b)
		})
	}
}

// TestBoundaryWireRoundTrip verifies that Action and ErrorExpr survive a
// ToWire → json.Marshal → json.Unmarshal → FromWire round-trip, while
// ErrorCheck (a Go closure, non-serializable) is nil after the round-trip.
func TestBoundaryWireRoundTrip(t *testing.T) {
	t.Parallel()

	original := event.NewBoundary("b1", "task-host",
		event.WithBoundaryAction("fire-once-action"),
		event.WithBoundaryErrorExpr(`_error == "E_PAYMENT"`),
		event.WithBoundaryErrorCheck(func(_ map[string]any, _ error) bool { return true }),
	)

	def := &model.ProcessDefinition{
		ID:      "proc",
		Version: 1,
		Nodes:   []model.Node{original},
	}

	data, err := json.Marshal(def)
	require.NoError(t, err, "json.Marshal must succeed")

	var decoded model.ProcessDefinition
	require.NoError(t, json.Unmarshal(data, &decoded), "json.Unmarshal must succeed")
	require.Len(t, decoded.Nodes, 1, "must have exactly 1 node after round-trip")

	b, ok := decoded.Nodes[0].(event.BoundaryEvent)
	require.True(t, ok, "decoded node must be event.BoundaryEvent, got %T", decoded.Nodes[0])

	assert.Equal(t, "b1", b.ID(), "ID must survive round-trip")
	assert.Equal(t, "task-host", b.AttachedTo, "AttachedTo must survive round-trip")
	assert.Equal(t, "fire-once-action", b.Action, "Action must survive wire round-trip")
	assert.Equal(t, `_error == "E_PAYMENT"`, b.ErrorExpr, "ErrorExpr must survive wire round-trip")
	assert.Nil(t, b.ErrorCheck, "ErrorCheck (Go closure) must be nil after wire round-trip — non-serializable")
}

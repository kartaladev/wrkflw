package runtime_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime/internal/runtimetest"
	"github.com/zakyalvan/krtlwrkflw/validation"
	vexpr "github.com/zakyalvan/krtlwrkflw/validation/expr"
)

// receiveTaskPayloadValidatedDef returns start -> recv(msg "OrderPlaced", validated) -> end.
// The ReceiveTask is a standalone (tier-4) parked wait, so MessageTargetNode
// resolves directly to it via tokenAwaitingMessage.
func receiveTaskPayloadValidatedDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "recv-payload-validated",
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewReceiveTask("recv", "OrderPlaced",
				activity.WithPayloadValidation(vexpr.New("orderID != nil"))),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "recv"},
			{ID: "f2", Source: "recv", Target: "end"},
		},
	}
}

// TestDeliverMessage_RejectsInvalidPayload verifies that DeliverMessage validates
// an inbound message payload against the woken tier-4 ReceiveTask's
// PayloadValidation strategy BEFORE applying the trigger: an invalid payload is
// rejected with validation.ErrInvalidInput and the token stays parked (the
// instance never advances past the receive), while a valid payload resumes the
// instance to completion as normal.
func TestDeliverMessage_RejectsInvalidPayload(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		payload map[string]any
		assert  func(t *testing.T, err error, before, after engine.InstanceState)
	}

	cases := []testCase{
		{
			name:    "rejected: empty payload, token stays parked",
			payload: map[string]any{},
			assert: func(t *testing.T, err error, before, after engine.InstanceState) {
				t.Helper()
				require.Error(t, err)
				assert.True(t, errors.Is(err, validation.ErrInvalidInput), "want ErrInvalidInput, got %v", err)

				assert.Equal(t, engine.StatusRunning, after.Status, "instance must not advance on rejection")
				require.Len(t, after.Tokens, 1, "token must remain parked")
				assert.Equal(t, "recv", after.Tokens[0].NodeID, "token must still be parked at the ReceiveTask")
				assert.Equal(t, before, after, "state must be unchanged by a rejected delivery")
			},
		},
		{
			name:    "accepted: valid payload, instance resumes to completion",
			payload: map[string]any{"orderID": "o1"},
			assert: func(t *testing.T, err error, before, after engine.InstanceState) {
				t.Helper()
				require.NoError(t, err)
				assert.False(t, errors.Is(err, validation.ErrInvalidInput))
				assert.Equal(t, engine.StatusCompleted, after.Status, "instance must complete once the message is accepted")
				assert.Empty(t, after.Tokens, "no tokens remain after completion")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			store := runtimetest.MustMemStore(t)
			driver := runtimetest.MustRunner(t, nil, store)
			def := receiveTaskPayloadValidatedDef()

			before, err := driver.Drive(ctx, def, "i1", nil)
			require.NoError(t, err)
			require.Equal(t, engine.StatusRunning, before.Status, "instance must park at the ReceiveTask")
			require.Len(t, before.Tokens, 1)
			require.Equal(t, "recv", before.Tokens[0].NodeID)

			deliverErr := driver.DeliverMessage(ctx, def, "OrderPlaced", "", tc.payload)

			after, _, loadErr := store.Load(ctx, "i1")
			require.NoError(t, loadErr)

			tc.assert(t, deliverErr, before, after)
		})
	}
}

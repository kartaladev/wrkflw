package processtest_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/processtest"
)

func approvalDef(t *testing.T) *model.ProcessDefinition {
	t.Helper()
	def, err := definition.NewBuilder("approval", 1).
		Add(event.NewStart("start")).
		Add(activity.NewUserTask("approve", activity.WithCandidateRoles("manager"))).
		Add(event.NewEnd("end")).
		Connect("start", "approve").
		Connect("approve", "end").
		Build()
	require.NoError(t, err)
	return def
}

func signalDef(t *testing.T) *model.ProcessDefinition {
	t.Helper()
	def, err := definition.NewBuilder("sig", 1).
		Add(event.NewStart("start")).
		Add(event.NewIntermediateCatch("await", event.WithSignalName("go"))).
		Add(event.NewEnd("end")).
		Connect("start", "await").
		Connect("await", "end").
		Build()
	require.NoError(t, err)
	return def
}

func messageDef(t *testing.T) *model.ProcessDefinition {
	t.Helper()
	def, err := definition.NewBuilder("msg", 1).
		Add(event.NewStart("start")).
		Add(event.NewIntermediateCatch("await", event.WithMessageCorrelator("PaymentReceived", ""))).
		Add(event.NewEnd("end")).
		Connect("start", "await").
		Connect("await", "end").
		Build()
	require.NoError(t, err)
	return def
}

// approve is a task decision that claims + completes every task as alice.
func approve(humantask.HumanTask) (authz.Actor, map[string]any, bool) {
	return authz.Actor{ID: "alice", Roles: []string{"manager"}}, map[string]any{"approved": true}, true
}

func TestHandlers(t *testing.T) {
	type testCase struct {
		name    string
		def     func(t *testing.T) *model.ProcessDefinition
		handler func(h *processtest.Harness) processtest.ParkHandler
		assert  func(t *testing.T, h *processtest.Harness, final engine.InstanceState, err error)
	}

	cases := []testCase{
		{
			name:    "AutoTimers drives a timer flow to completion",
			def:     func(t *testing.T) *model.ProcessDefinition { return timerDef(t, "at", 1) },
			handler: func(*processtest.Harness) processtest.ParkHandler { return processtest.AutoTimers() },
			assert: func(t *testing.T, _ *processtest.Harness, final engine.InstanceState, err error) {
				require.NoError(t, err)
				assert.Equal(t, engine.StatusCompleted, final.Status)
			},
		},
		{
			name:    "CompleteTasks drives an approval flow to completion",
			def:     approvalDef,
			handler: func(h *processtest.Harness) processtest.ParkHandler { return h.CompleteTasks(approve) },
			assert: func(t *testing.T, h *processtest.Harness, final engine.InstanceState, err error) {
				require.NoError(t, err)
				assert.Equal(t, engine.StatusCompleted, final.Status)
				assert.NotEmpty(t, h.Authorizer().Calls(), "authorizer must be consulted for the task")
			},
		},
		{
			name: "Chain falls through AutoTimers to CompleteTasks",
			def:  approvalDef,
			handler: func(h *processtest.Harness) processtest.ParkHandler {
				return processtest.Chain(processtest.AutoTimers(), h.CompleteTasks(approve))
			},
			assert: func(t *testing.T, _ *processtest.Harness, final engine.InstanceState, err error) {
				require.NoError(t, err)
				assert.Equal(t, engine.StatusCompleted, final.Status)
			},
		},
		{
			name:    "PublishSignal drives a signal flow to completion",
			def:     signalDef,
			handler: func(h *processtest.Harness) processtest.ParkHandler { return h.PublishSignal("go", nil) },
			assert: func(t *testing.T, _ *processtest.Harness, final engine.InstanceState, err error) {
				require.NoError(t, err)
				assert.Equal(t, engine.StatusCompleted, final.Status)
			},
		},
		{
			name: "DeliverMessage drives a message flow to completion",
			def:  messageDef,
			handler: func(h *processtest.Harness) processtest.ParkHandler {
				return h.DeliverMessage("PaymentReceived", "", nil)
			},
			assert: func(t *testing.T, _ *processtest.Harness, final engine.InstanceState, err error) {
				require.NoError(t, err)
				assert.Equal(t, engine.StatusCompleted, final.Status)
			},
		},
		{
			name: "Chain where every handler passes yields an unhandled park",
			def:  signalDef,
			handler: func(h *processtest.Harness) processtest.ParkHandler {
				// AutoTimers passes (no timer) and a nil handler is skipped; the signal
				// park stays unresolved.
				return processtest.Chain(processtest.AutoTimers(), nil)
			},
			assert: func(t *testing.T, _ *processtest.Harness, _ engine.InstanceState, err error) {
				require.ErrorIs(t, err, processtest.ErrUnhandledPark)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, err := processtest.New()
			require.NoError(t, err)

			def := tc.def(t)
			_, err = h.Start(context.Background(), def, "inst", nil)
			require.NoError(t, err)

			final, err := h.DriveToCompletion(context.Background(), def, "inst", tc.handler(h))
			tc.assert(t, h, final, err)
		})
	}
}

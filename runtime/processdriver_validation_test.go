package runtime_test

// processdriver_validation_test.go proves the pre-Step validation hook wired
// into deliverLoop (ProcessDriver.validateInput, fed by engine.TargetNode +
// model.ValidationStrategyFor + runtime/validation.Gate): a rejection must
// surface BEFORE any state is committed, for both the start boundary
// (StartInstance) and — the regression this redesign fixes — a message
// boundary on a node NESTED inside a sub-process (the old flat def.Node
// lookup silently skipped nested nodes).

import (
	"context"
	"testing"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	vexpr "github.com/zakyalvan/krtlwrkflw/definition/model/validate/expr"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/internal/runtimetest"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/validation"
)

// startValidationDef returns start[validated: amount > 0] → svc → end. The
// service task uses an inline no-op action so the test needs no catalog.
func startValidationDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "start-validation", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start", event.WithInputValidation(vexpr.New("amount > 0"))),
			activity.NewServiceTask("svc", activity.WithActionFunc(
				func(_ context.Context, _ map[string]any) (map[string]any, error) {
					return nil, nil
				})),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "svc"},
			{ID: "f2", Source: "svc", Target: "end"},
		},
	}
}

// TestValidateInputStart verifies the hook rejects invalid start vars BEFORE
// any instance is created (store.Load must report kernel.ErrInstanceNotFound),
// and lets valid vars proceed.
func TestValidateInputStart(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name       string
		instanceID string
		vars       map[string]any
		assert     func(t *testing.T, store *kernel.MemInstanceStore, instanceID string, st engine.InstanceState, err error)
	}

	cases := []testCase{
		{
			name:       "reject: amount <= 0 rejects before any commit",
			instanceID: "start-reject-1",
			vars:       map[string]any{"amount": -1},
			assert: func(t *testing.T, store *kernel.MemInstanceStore, instanceID string, _ engine.InstanceState, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, validation.ErrInvalidInput)
				_, _, loadErr := store.Load(t.Context(), instanceID)
				assert.ErrorIs(t, loadErr, kernel.ErrInstanceNotFound, "rejected start trigger must not create an instance")
			},
		},
		{
			name:       "accept: amount > 0 proceeds and completes",
			instanceID: "start-accept-1",
			vars:       map[string]any{"amount": 5},
			assert: func(t *testing.T, store *kernel.MemInstanceStore, instanceID string, st engine.InstanceState, err error) {
				require.NoError(t, err)
				assert.Equal(t, engine.StatusCompleted, st.Status)
				_, _, loadErr := store.Load(t.Context(), instanceID)
				assert.NoError(t, loadErr, "accepted start trigger must create the instance")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fc := clockwork.NewFakeClock()
			store := runtimetest.MustMemStore(t)
			r := runtimetest.MustRunner(t, nil, store, runtime.WithClock(fc))
			def := startValidationDef()

			st, err := r.Drive(t.Context(), def, tc.instanceID, tc.vars)
			tc.assert(t, store, tc.instanceID, st, err)
		})
	}
}

// nestedMessageValidationDef returns start → SubProcess{ inner-start →
// ReceiveTask("wait", validated: ok == true) → inner-end } → end. This is the
// regression scenario: with the old flat def.Node lookup, "wait" (nested
// inside the sub-process) was invisible to validation and silently skipped.
func nestedMessageValidationDef() *model.ProcessDefinition {
	nested := &model.ProcessDefinition{
		ID: "nested-wait", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			activity.NewReceiveTask("wait", "proceed", activity.WithPayloadValidation(vexpr.New("ok == true"))),
			event.NewEnd("inner-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "wait"},
			{ID: "if2", Source: "wait", Target: "inner-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "nested-msg-validation", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("sub", nested),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "sub"},
			{ID: "f2", Source: "sub", Target: "end"},
		},
	}
}

// TestValidateInputNestedMessage verifies the hook resolves the message
// target node through engine.TargetNode's scope-aware lookup even when the
// node lives inside a sub-process: a rejected payload leaves the instance
// state byte-for-byte unchanged (no advance), and an accepted payload
// advances past the nested ReceiveTask to completion.
func TestValidateInputNestedMessage(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		payload map[string]any
		assert  func(t *testing.T, before, after engine.InstanceState, err error)
	}

	cases := []testCase{
		{
			name:    "reject: ok=false rejects before any advance",
			payload: map[string]any{"ok": false},
			assert: func(t *testing.T, before, after engine.InstanceState, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, validation.ErrInvalidInput)
				assert.Equal(t, before, after, "instance state must be unchanged when the nested payload is rejected")
			},
		},
		{
			name:    "accept: ok=true advances past the nested ReceiveTask",
			payload: map[string]any{"ok": true},
			assert: func(t *testing.T, _, after engine.InstanceState, err error) {
				require.NoError(t, err)
				assert.Equal(t, engine.StatusCompleted, after.Status)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fc := clockwork.NewFakeClock()
			store := runtimetest.MustMemStore(t)
			r := runtimetest.MustRunner(t, nil, store, runtime.WithClock(fc))
			def := nestedMessageValidationDef()

			const instanceID = "nested-msg-1"
			parked, err := r.Drive(t.Context(), def, instanceID, nil)
			require.NoError(t, err)
			require.Equal(t, engine.StatusRunning, parked.Status, "must park at the nested ReceiveTask")

			before, _, err := store.Load(t.Context(), instanceID)
			require.NoError(t, err)

			derr := r.DeliverMessage(t.Context(), def, "proceed", "", tc.payload)

			after, _, loadErr := store.Load(t.Context(), instanceID)
			require.NoError(t, loadErr)

			tc.assert(t, before, after, derr)
		})
	}
}

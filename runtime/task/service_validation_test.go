package task_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/task"
	"github.com/zakyalvan/krtlwrkflw/validation"
	vexpr "github.com/zakyalvan/krtlwrkflw/validation/expr"
)

// approvalValidatedDef returns start -> userTask("approve", validated: decision
// in ['approve','reject']) -> end, used to exercise TaskService.Complete's
// completion-output validation via an injected DefinitionResolver.
func approvalValidatedDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "approval-validated",
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("approve", []string{"manager"},
				activity.WithCompletionValidation(vexpr.New(`decision in ['approve','reject']`))),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "approve"},
			{ID: "f2", Source: "approve", Target: "end"},
		},
	}
}

// TestComplete_ValidatesCompletionOutput verifies TaskService.Complete's opt-in
// completion-output validation: when a DefinitionResolver is wired (via
// WithDefinitionResolver) and the resolved UserTask node carries a
// CompletionValidation strategy, Complete validates the actor's output before
// returning a trigger — rejecting with validation.ErrInvalidInput and issuing no
// trigger on failure. Without a resolver wired, validation is skipped entirely
// (opt-in), even though the node still carries a CompletionValidation slot.
func TestComplete_ValidatesCompletionOutput(t *testing.T) {
	t.Parallel()

	def := approvalValidatedDef()
	reg := kernel.NewMapDefinitionRegistry(def)

	seedTask := func(t *testing.T, taskToken string) humantask.TaskStore {
		t.Helper()
		store := humantask.NewMemTaskStore()
		require.NoError(t, store.Upsert(t.Context(), humantask.HumanTask{
			TaskToken:  taskToken,
			NodeID:     "approve",
			DefID:      def.ID,
			DefVersion: def.Version,
			State:      humantask.Claimed,
			ClaimedBy:  "alice",
		}))
		return store
	}

	type testCase struct {
		name         string
		taskToken    string
		withResolver bool
		output       map[string]any
		assert       func(t *testing.T, trg engine.Trigger, err error)
	}

	cases := []testCase{
		{
			name:         "rejects invalid decision",
			taskToken:    "tok-reject",
			withResolver: true,
			output:       map[string]any{"decision": "maybe"},
			assert: func(t *testing.T, trg engine.Trigger, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, validation.ErrInvalidInput)
				assert.Nil(t, trg, "no trigger must be returned when validation rejects the output")
			},
		},
		{
			name:         "accepts valid decision",
			taskToken:    "tok-accept",
			withResolver: true,
			output:       map[string]any{"decision": "approve"},
			assert: func(t *testing.T, trg engine.Trigger, err error) {
				require.NoError(t, err)
				completed, ok := trg.(engine.HumanCompleted)
				require.True(t, ok, "want engine.HumanCompleted, got %T", trg)
				assert.Equal(t, "approve", completed.Output["decision"])
			},
		},
		{
			name:         "no resolver wired: validation is skipped (opt-in)",
			taskToken:    "tok-optout",
			withResolver: false,
			output:       map[string]any{"decision": "not-a-valid-choice"},
			assert: func(t *testing.T, trg engine.Trigger, err error) {
				require.NoError(t, err)
				_, ok := trg.(engine.HumanCompleted)
				assert.True(t, ok, "want engine.HumanCompleted, got %T", trg)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := seedTask(t, tc.taskToken)

			var opts []task.TaskServiceOption
			if tc.withResolver {
				opts = append(opts, task.WithDefinitionResolver(reg))
			}
			svc, err := task.NewTaskService(store, authz.AllowAll{}, opts...)
			require.NoError(t, err)

			trg, err := svc.Complete(t.Context(), tc.taskToken, authz.Actor{ID: "alice"}, tc.output)
			tc.assert(t, trg, err)
		})
	}
}

// nilDefinitionResolver is a misbehaving DefinitionResolver stub: it returns a
// nil *model.ProcessDefinition alongside a nil error, simulating a
// consumer-supplied resolver that violates the (def, nil) == found contract.
// DefinitionResolver is an open, consumer-satisfiable interface, so
// TaskService.Complete must guard against this shape rather than trust it.
type nilDefinitionResolver struct{}

func (nilDefinitionResolver) Lookup(ctx context.Context, q model.Qualifier) (*model.ProcessDefinition, error) {
	return nil, nil
}

// TestComplete_NilDefinitionFromResolver verifies TaskService.Complete guards
// against a DefinitionResolver that returns (nil, nil): rather than panicking
// on the subsequent def.Node lookup, Complete must return a non-nil error and
// a nil trigger.
func TestComplete_NilDefinitionFromResolver(t *testing.T) {
	t.Parallel()

	const taskToken = "tok-nil-def"
	store := humantask.NewMemTaskStore()
	require.NoError(t, store.Upsert(t.Context(), humantask.HumanTask{
		TaskToken:  taskToken,
		NodeID:     "approve",
		DefID:      "approval-validated",
		DefVersion: 1,
		State:      humantask.Claimed,
		ClaimedBy:  "alice",
	}))

	svc, err := task.NewTaskService(store, authz.AllowAll{}, task.WithDefinitionResolver(nilDefinitionResolver{}))
	require.NoError(t, err)

	trg, err := svc.Complete(t.Context(), taskToken, authz.Actor{ID: "alice"}, map[string]any{"decision": "approve"})
	require.Error(t, err, "Complete must error, not panic, when the resolver returns a nil definition")
	assert.Nil(t, trg, "no trigger must be returned when the resolver returns a nil definition")
}

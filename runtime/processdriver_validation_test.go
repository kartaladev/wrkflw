package runtime_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/validation"
	vexpr "github.com/zakyalvan/krtlwrkflw/validation/expr"
)

// startValidatedDef returns a minimal process: start(validated) → end, where the
// start event requires "amount > 0" of the manually-provided start vars.
func startValidatedDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "start-validated",
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start", event.WithInputValidation(vexpr.New("amount > 0"))),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "end"},
		},
	}
}

// TestDrive_RejectsInvalidStartVars_NoInstanceCreated verifies that Drive
// validates the caller-supplied start vars against the start event's
// InputValidation strategy BEFORE any instance is created: a rejected input
// returns an error wrapping validation.ErrInvalidInput and leaves the store
// untouched, while an accepted input proceeds to completion as normal.
func TestDrive_RejectsInvalidStartVars_NoInstanceCreated(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		vars       map[string]any
		instanceID string
		assert     func(t *testing.T, store *kernel.MemInstanceStore, instanceID string, st engine.InstanceState, err error)
	}{
		"rejected: invalid vars, no instance created": {
			vars:       map[string]any{"amount": -1},
			instanceID: "inst-rejected",
			assert: func(t *testing.T, store *kernel.MemInstanceStore, instanceID string, st engine.InstanceState, err error) {
				t.Helper()
				require.Error(t, err)
				assert.True(t, errors.Is(err, validation.ErrInvalidInput), "want ErrInvalidInput, got %v", err)

				_, _, loadErr := store.Load(t.Context(), instanceID)
				assert.ErrorIs(t, loadErr, kernel.ErrInstanceNotFound, "no instance must be created on rejection")
			},
		},
		"accepted: valid vars, instance proceeds": {
			vars:       map[string]any{"amount": 5},
			instanceID: "inst-accepted",
			assert: func(t *testing.T, store *kernel.MemInstanceStore, instanceID string, st engine.InstanceState, err error) {
				t.Helper()
				require.NoError(t, err)
				assert.False(t, errors.Is(err, validation.ErrInvalidInput))

				loaded, _, loadErr := store.Load(t.Context(), instanceID)
				require.NoError(t, loadErr, "instance must have been created")
				assert.Equal(t, st.Status, loaded.Status)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			customStore, storeErr := kernel.NewMemInstanceStore()
			require.NoError(t, storeErr)

			driver, err := runtime.NewProcessDriver(runtime.WithInstanceStore(customStore))
			require.NoError(t, err)
			require.NotNil(t, driver)
			t.Cleanup(func() { _ = driver.Shutdown(t.Context()) })

			st, runErr := driver.Drive(t.Context(), startValidatedDef(), tc.instanceID, tc.vars)
			tc.assert(t, customStore, tc.instanceID, st, runErr)
		})
	}
}

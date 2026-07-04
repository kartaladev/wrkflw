package processtest_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/processtest"
)

func timerDef(t *testing.T, id string, waits int) *model.ProcessDefinition {
	t.Helper()
	b := definition.NewBuilder(id, 1).Add(event.NewStart("start"))
	prev := "start"
	for i := range waits {
		name := "wait" + string(rune('1'+i))
		b = b.Add(event.NewCatch(name, event.WithCatchTimer(`"1h"`))).Connect(prev, name)
		prev = name
	}
	b = b.Add(event.NewEnd("end")).Connect(prev, "end")
	def, err := b.Build()
	require.NoError(t, err)
	return def
}

// advanceOnTimer resolves a timer park by auto-advancing; passes otherwise.
func advanceOnTimer(_ context.Context, p processtest.Park) (processtest.Decision, error) {
	if p.HasArmedTimers {
		return processtest.AdvanceTimers(), nil
	}
	return processtest.Pass(), nil
}

func TestHarness_New_WiresStack(t *testing.T) {
	t.Parallel()

	h, err := processtest.New()
	require.NoError(t, err)

	assert.NotNil(t, h.Driver())
	assert.NotNil(t, h.Clock())
	assert.NotNil(t, h.Scheduler())
	assert.NotNil(t, h.Catalog())
	assert.NotNil(t, h.Authorizer())
	assert.NotNil(t, h.Tasks())
	assert.NotNil(t, h.TaskService())
	assert.NotNil(t, h.Store())
	assert.Nil(t, h.Bus(), "no signal bus unless WithSignalBus")
}

func TestHarness_DriveToCompletion(t *testing.T) {
	type testCase struct {
		name   string
		opts   []processtest.Option
		def    func(t *testing.T) *model.ProcessDefinition
		assert func(t *testing.T, final engine.InstanceState, err error)
	}

	cases := []testCase{
		{
			name: "single timer auto-advances to completion",
			def:  func(t *testing.T) *model.ProcessDefinition { return timerDef(t, "t1", 1) },
			assert: func(t *testing.T, final engine.InstanceState, err error) {
				require.NoError(t, err)
				assert.Equal(t, engine.StatusCompleted, final.Status)
			},
		},
		{
			name: "two timers complete exactly at the drive limit (boundary)",
			opts: []processtest.Option{processtest.WithDriveLimit(2)},
			def:  func(t *testing.T) *model.ProcessDefinition { return timerDef(t, "t2", 2) },
			assert: func(t *testing.T, final engine.InstanceState, err error) {
				require.NoError(t, err)
				assert.Equal(t, engine.StatusCompleted, final.Status)
			},
		},
		{
			name: "drive limit too small returns ErrDriveLimitExceeded",
			opts: []processtest.Option{processtest.WithDriveLimit(1)},
			def:  func(t *testing.T) *model.ProcessDefinition { return timerDef(t, "t3", 2) },
			assert: func(t *testing.T, _ engine.InstanceState, err error) {
				require.ErrorIs(t, err, processtest.ErrDriveLimitExceeded)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, err := processtest.New(tc.opts...)
			require.NoError(t, err)

			def := tc.def(t)
			_, err = h.Start(t.Context(), def, "inst", nil)
			require.NoError(t, err)

			final, err := h.DriveToCompletion(t.Context(), def, "inst", advanceOnTimer)
			tc.assert(t, final, err)
		})
	}
}

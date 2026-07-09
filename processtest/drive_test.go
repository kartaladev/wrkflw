package processtest_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/processtest"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// buildSignalCase builds a start → signal-catch → end definition, a driver over a
// fresh MemInstanceStore, and runs one instance that parks awaiting the "go" signal.
func buildSignalCase(t *testing.T) (*runtime.ProcessDriver, engine.InstanceState, *model.ProcessDefinition) {
	t.Helper()

	def, err := definition.NewBuilder("sig", 1).
		Add(event.NewStart("start")).
		Add(event.NewIntermediateCatch("await", event.WithSignalName("go"))).
		Add(event.NewEnd("end")).
		Connect("start", "await").
		Connect("await", "end").
		Build()
	require.NoError(t, err)

	store, err := kernel.NewMemInstanceStore()
	require.NoError(t, err)
	driver, err := runtime.NewProcessDriver(runtime.WithInstanceStore(store))
	require.NoError(t, err)

	parked, err := driver.Drive(t.Context(), def, "i1", nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, parked.Status)

	return driver, parked, def
}

func TestDriveToCompletion_FreeFunction(t *testing.T) {
	boom := errors.New("boom")

	type testCase struct {
		name    string
		handler func(context.Context, processtest.Park) (processtest.Decision, error)
		assert  func(t *testing.T, final engine.InstanceState, err error)
	}

	cases := []testCase{
		{
			name: "delivering the awaited signal drives to completion",
			handler: func(_ context.Context, p processtest.Park) (processtest.Decision, error) {
				if p.Reason == processtest.ReasonSignal {
					return processtest.Deliver(engine.NewSignalReceived(time.Now(), "go", nil)), nil
				}
				return processtest.Pass(), nil
			},
			assert: func(t *testing.T, final engine.InstanceState, err error) {
				require.NoError(t, err)
				require.Equal(t, engine.StatusCompleted, final.Status)
			},
		},
		{
			name: "Pass on a real park is an unhandled-park error",
			handler: func(context.Context, processtest.Park) (processtest.Decision, error) {
				return processtest.Pass(), nil
			},
			assert: func(t *testing.T, final engine.InstanceState, err error) {
				require.ErrorIs(t, err, processtest.ErrUnhandledPark)
				require.Equal(t, engine.StatusRunning, final.Status)
			},
		},
		{
			name: "Stop returns the current non-terminal state without error",
			handler: func(context.Context, processtest.Park) (processtest.Decision, error) {
				return processtest.Stop(), nil
			},
			assert: func(t *testing.T, final engine.InstanceState, err error) {
				require.NoError(t, err)
				require.Equal(t, engine.StatusRunning, final.Status)
			},
		},
		{
			name: "Abort returns the supplied error",
			handler: func(context.Context, processtest.Park) (processtest.Decision, error) {
				return processtest.Abort(boom), nil
			},
			assert: func(t *testing.T, _ engine.InstanceState, err error) {
				require.ErrorIs(t, err, boom)
			},
		},
		{
			name: "AdvanceTimers is unsupported on the free function",
			handler: func(context.Context, processtest.Park) (processtest.Decision, error) {
				return processtest.AdvanceTimers(), nil
			},
			assert: func(t *testing.T, _ engine.InstanceState, err error) {
				require.ErrorIs(t, err, processtest.ErrAdvanceTimersUnsupported)
			},
		},
		{
			name: "a handler error aborts the drive",
			handler: func(context.Context, processtest.Park) (processtest.Decision, error) {
				return processtest.Pass(), boom
			},
			assert: func(t *testing.T, _ engine.InstanceState, err error) {
				require.ErrorIs(t, err, boom)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			driver, parked, def := buildSignalCase(t)
			final, err := processtest.DriveToCompletion(t.Context(), driver, def, parked, tc.handler)
			tc.assert(t, final, err)
		})
	}
}

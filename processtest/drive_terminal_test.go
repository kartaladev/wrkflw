package processtest_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/processtest"
)

// TestDriveToCompletionIsShieldedFromTheTerminalPolicy pins the public harness
// against the engine's terminal policy. DriveToCompletion is the surface
// consumers write their own process tests through, so a behaviour change here
// breaks THEIR suites on upgrade, not ours.
//
// The drive loop checks IsTerminal both before classifying (so the handler is
// never asked to resolve a terminal instance) and immediately after a productive
// step (so a flow that ends mid-drive returns at once). Together those two checks
// mean the loop never hands a trigger to a terminal instance, and therefore never
// meets either new sentinel.
//
// The two checks are individually redundant for the ERROR outcome — deleting
// either one alone still returns nil, because the other catches the state one
// iteration later. They are not redundant together: with both removed, this
// test's handler keeps proposing the same trigger, the terminal instance keeps
// absorbing it silently (SignalReceived is rejectSilently), the state never
// changes, and the drive spins to ErrDriveLimitExceeded. Verified by mutation in
// both configurations.
//
// Both cases therefore assert the handler CALL COUNT, not just the returned
// error: the count is what distinguishes "shielded before classification" from
// "delivered once and caught on the way out".
func TestDriveToCompletionIsShieldedFromTheTerminalPolicy(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name string
		// startFromTerminal drives the instance to completion BEFORE handing it to
		// DriveToCompletion, so the loop's input state is already terminal.
		startFromTerminal bool
		assert            func(t *testing.T, final engine.InstanceState, err error, handlerCalls int32)
	}

	cases := []testCase{
		{
			name: "a flow that terminates mid-drive returns cleanly",
			assert: func(t *testing.T, final engine.InstanceState, err error, handlerCalls int32) {
				require.NoError(t, err,
					"the terminating step must not surface the terminal policy to the consumer")
				assert.Equal(t, engine.StatusCompleted, final.Status)
				assert.Equal(t, int32(1), handlerCalls,
					"the handler resolves the one signal park and is never consulted again")
			},
		},
		{
			name:              "an already-terminal input state returns without consulting the handler",
			startFromTerminal: true,
			assert: func(t *testing.T, final engine.InstanceState, err error, handlerCalls int32) {
				require.NoError(t, err,
					"a terminal instance is a finished drive, not an error")
				assert.Equal(t, engine.StatusCompleted, final.Status)
				assert.Zero(t, handlerCalls,
					"the loop must return before classifying a terminal state, so the handler "+
						"is never asked to resolve a park it cannot resolve")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			driver, parked, def := buildSignalCase(t)

			state := parked
			if tc.startFromTerminal {
				done, err := driver.ApplyTrigger(ctx, def, parked.InstanceID,
					engine.NewSignalReceived(time.Now(), "go", nil))
				require.NoError(t, err)
				require.Equal(t, engine.StatusCompleted, done.Status,
					"precondition: the instance must already be terminal")
				state = done
			}

			var handlerCalls atomic.Int32
			// The handler unconditionally proposes the awaited signal. On the
			// live park that resolves it; on a terminal instance it would be a
			// silent no-op, which is exactly the state the loop must not reach.
			handler := func(_ context.Context, _ processtest.Park) (processtest.Decision, error) {
				handlerCalls.Add(1)
				return processtest.Deliver(engine.NewSignalReceived(time.Now(), "go", nil)), nil
			}

			final, err := processtest.DriveToCompletion(ctx, driver, def, state, handler)
			tc.assert(t, final, err, handlerCalls.Load())
		})
	}
}

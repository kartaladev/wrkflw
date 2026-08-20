package engine_test

// step_terminal_arms_test.go — backlog blocker 8, corrected scope.
//
// The blocker was FILED as "the forceTerminate → endInstance boundary sweep is
// entirely uncovered". That is false: measured on this tree, engine is 93.0 %,
// endInstance and cancelAllScheduledWork are 100 %, forceTerminate 90 % and
// cancelAllArmsAndBoundaries 80 %. Exactly two statements on that path carried a
// zero count, and this file covers those two and nothing else:
//
//	state_arms.go:142.35,143.23  0   ┐ the ArmedEvents loop in
//	state_arms.go:143.23,145.4   0   ┘ cancelAllArmsAndBoundaries
//	step_nodes.go:628.19,630.4   0     forceTerminate's empty-reason default
//
// The BOUNDARIES half of that same loop was already covered (count 1) — only the
// ArmedEvents half was not, i.e. every existing caller reached
// cancelAllArmsAndBoundaries with an EMPTY s.ArmedEvents.
//
// One fixture reaches both: a parallel fork whose first branch parks on an
// event-based gateway (creating an event-gateway TIMER ARM) while the second
// branch runs into a force-termination end event carrying an EMPTY
// TerminationReason.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/gateway"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
)

// forceTerminateWithArmDef forks a live event-gateway timer arm alongside a
// force-termination end event. The gateway branch is declared FIRST so drive()
// parks it — arming the event — before the terminating branch runs.
//
//	start → fork ⇉ evtgw → tcatch[timer "1h"]  → end-timer
//	                     → scatch[signal "ok"] → end-signal
//	             ⇉ halt[End, force-terminate, reason ""]
func forceTerminateWithArmDef(reason string) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-force-terminate-arm", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewParallel("fork"),
			gateway.NewEventBased("evtgw"),
			event.NewIntermediateCatch("tcatch", event.WithCatchTimer(schedule.AfterExpr(`"1h"`))),
			event.NewIntermediateCatch("scatch", event.WithSignalName("ok")),
			event.NewEnd("end-timer"),
			event.NewEnd("end-signal"),
			event.NewEnd("halt", event.WithForceTermination(reason, event.OutcomeAbort)),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f-start", Source: "start", Target: "fork"},
			{ID: "f-fork-gw", Source: "fork", Target: "evtgw"},
			{ID: "f-fork-halt", Source: "fork", Target: "halt"},
			{ID: "f-gw-timer", Source: "evtgw", Target: "tcatch"},
			{ID: "f-gw-signal", Source: "evtgw", Target: "scatch"},
			{ID: "f-timer-end", Source: "tcatch", Target: "end-timer"},
			{ID: "f-signal-end", Source: "scatch", Target: "end-signal"},
		},
	}
}

// TestForceTerminateCancelsEventGatewayArms pins the two statements the
// termination path never executed.
//
// Falsifiability (both verified by mutation, see the delivery's evidence file):
//
//   - Deleting the ArmedEvents loop from cancelAllArmsAndBoundaries drops the
//     CancelTimer assertion to zero commands. The assertion is NOT satisfiable by
//     the sibling s.Timers sweep: the test first asserts the parked state's
//     Timers table is EMPTY, so an event-gateway arm is the only possible source
//     of that CancelTimer.
//   - Removing the `reason = "force-terminated"` default makes FailInstance.Err
//     empty.
func TestForceTerminateCancelsEventGatewayArms(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)

	type testCase struct {
		name   string
		reason string
		assert func(t *testing.T, armTimerID string, res engine.StepResult, err error)
	}

	cases := []testCase{
		{
			name:   "empty termination reason falls back to the default",
			reason: "",
			assert: func(t *testing.T, armTimerID string, res engine.StepResult, err error) {
				require.NoError(t, err)
				assert.Equal(t, engine.StatusTerminated, res.State.Status)

				var fail *engine.FailInstance
				for _, cmd := range res.Commands {
					if fi, ok := cmd.(engine.FailInstance); ok {
						fail = &fi
					}
				}
				require.NotNil(t, fail, "an aborting force-termination must emit FailInstance")
				assert.Equal(t, "force-terminated", fail.Err,
					"an empty TerminationReason must fall back to the default reason")

				assertCancelsTimer(t, res.Commands, armTimerID)
			},
		},
		{
			// Control: an explicit reason must survive untouched, so the default
			// above cannot be mistaken for an unconditional overwrite.
			name:   "explicit termination reason is preserved",
			reason: "fraud detected",
			assert: func(t *testing.T, armTimerID string, res engine.StepResult, err error) {
				require.NoError(t, err)
				assert.Equal(t, engine.StatusTerminated, res.State.Status)

				var fail *engine.FailInstance
				for _, cmd := range res.Commands {
					if fi, ok := cmd.(engine.FailInstance); ok {
						fail = &fi
					}
				}
				require.NotNil(t, fail)
				assert.Equal(t, "fraud detected", fail.Err)

				assertCancelsTimer(t, res.Commands, armTimerID)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			def := forceTerminateWithArmDef(tc.reason)
			res, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i1"},
				engine.NewStartInstance(at, nil), engine.StepOptions{})

			// The arm's timer id is the one the gateway scheduled on entry.
			var armTimerID string
			for _, cmd := range res.Commands {
				if st, ok := cmd.(engine.ScheduleTimer); ok {
					armTimerID = st.TimerID
				}
			}
			require.NotEmpty(t, armTimerID, "the event gateway must have armed a timer")

			tc.assert(t, armTimerID, res, err)
		})
	}
}

// TestEventGatewayArmIsNotATimerRecord is the vacuity guard for the CancelTimer
// assertion above: it pins that an event-gateway timer arm lives ONLY in
// s.ArmedEvents and contributes no row to s.Timers (ADR-0177 — s.Timers was once
// read as the sole arm authority and reported "no armed timers" for this
// source). If this ever stops holding, the CancelTimer assertion could be
// satisfied by cancelAllTimers instead of the loop under test.
func TestEventGatewayArmIsNotATimerRecord(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)

	def := gatewayTimerArmDef()
	res, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)

	assert.Empty(t, res.State.Timers,
		"an event-gateway timer arm must live only in s.ArmedEvents, never in the s.Timers record table")
}

// assertCancelsTimer asserts cmds retires timerID exactly once.
func assertCancelsTimer(t *testing.T, cmds []engine.Command, timerID string) {
	t.Helper()
	n := 0
	for _, cmd := range cmds {
		if ct, ok := cmd.(engine.CancelTimer); ok && ct.TimerID == timerID {
			n++
		}
	}
	assert.Equal(t, 1, n, "force-termination must emit exactly one CancelTimer for the event-gateway arm %q", timerID)
}

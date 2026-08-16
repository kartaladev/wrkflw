package runtime

import (
	"testing"

	"github.com/kartaladev/wrkflw/engine"
	"github.com/stretchr/testify/assert"
)

// TestCompensationFailedIncidentIsNotPublishedAsCauseOfDeath pins both cause-of-death
// resolvers — terminalEventErr (runtime/outbox.go, the instance.failed /
// instance.terminated payload) and terminalErr (runtime/processdriver_action.go, the
// kernel.CallOutcome a call-activity parent sees) — against a walk-scoped incident
// sitting at index 0.
//
// Every row asserts the REPLACEMENT string, never merely the absence of the
// compensation error: "not the compensation error" alone cannot tell "filtered
// correctly, real cause published" apart from "filtered, and the real cause lost too".
//
// What makes each row fail before the allow-list lands: both resolvers return
// st.Incidents[0].Error unconditionally, so every row carrying a walk-scoped incident
// at index 0 gets "compensation action failed" / "compensation action stalled" where
// it expects the genuine cause. The last row is the control — a plain action incident
// is unaffected and passes both before and after, which is what proves the allow-list
// does not simply drop the cause of death.
func TestCompensationFailedIncidentIsNotPublishedAsCauseOfDeath(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		st     engine.InstanceState
		cmds   []engine.Command
		assert func(t *testing.T, eventErr, driverErr string)
	}

	cases := []testCase{
		{
			// A cancel walk raises its incident before any FailInstance is emitted, so a
			// walk-scoped record lands at index 0 ahead of the action incident that is the
			// real cause of death.
			name: "compensation-failed at index 0 yields to the action incident behind it",
			st: engine.InstanceState{
				InstanceID: "i-failed-then-action",
				Status:     engine.StatusFailed,
				Incidents: []engine.Incident{
					{ID: "inc-1", Kind: engine.IncidentCompensationFailed, CommandID: "c-undo", Error: "compensation action failed"},
					{ID: "inc-2", Kind: engine.IncidentAction, TokenID: "tok-1", NodeID: "chargeCard", Error: "charge-card: gateway timeout"},
				},
			},
			// A FailInstance is present so a pass cannot come from falling through to the
			// command rung: only finding the action incident produces this string.
			cmds: []engine.Command{engine.FailInstance{Err: "cancelled"}},
			assert: func(t *testing.T, eventErr, driverErr string) {
				assert.Equal(t, "charge-card: gateway timeout", eventErr)
				assert.Equal(t, "charge-card: gateway timeout", driverErr)
			},
		},
		{
			name: "compensation-stall at index 0 yields to the action incident behind it",
			st: engine.InstanceState{
				InstanceID: "i-stall-then-action",
				Status:     engine.StatusFailed,
				Incidents: []engine.Incident{
					{ID: "inc-1", Kind: engine.IncidentCompensationStall, CommandID: "c-undo", Error: "compensation action stalled"},
					{ID: "inc-2", Kind: engine.IncidentAction, TokenID: "tok-1", NodeID: "chargeCard", Error: "charge-card: gateway timeout"},
				},
			},
			cmds: []engine.Command{engine.FailInstance{Err: "cancelled"}},
			assert: func(t *testing.T, eventErr, driverErr string) {
				assert.Equal(t, "charge-card: gateway timeout", eventErr)
				assert.Equal(t, "charge-card: gateway timeout", driverErr)
			},
		},
		{
			// The two resolvers DIVERGE here, and that is intended: terminalEventErr has a
			// FailInstance{Err} rung behind the incident scan, terminalErr has none.
			name: "compensation-failed alone falls to the command rung for the event and the status default for the driver",
			st: engine.InstanceState{
				InstanceID: "i-failed-only",
				Status:     engine.StatusTerminated,
				Incidents: []engine.Incident{
					{ID: "inc-1", Kind: engine.IncidentCompensationFailed, CommandID: "c-undo", Error: "compensation action failed"},
				},
			},
			cmds: []engine.Command{engine.FailInstance{Err: "cancelled"}},
			assert: func(t *testing.T, eventErr, driverErr string) {
				assert.Equal(t, "cancelled", eventErr)
				assert.Equal(t, "instance terminated", driverErr)
			},
		},
		{
			name: "compensation-stall alone falls to the command rung for the event and the status default for the driver",
			st: engine.InstanceState{
				InstanceID: "i-stall-only",
				Status:     engine.StatusTerminated,
				Incidents: []engine.Incident{
					{ID: "inc-1", Kind: engine.IncidentCompensationStall, CommandID: "c-undo", Error: "compensation action stalled"},
				},
			},
			cmds: []engine.Command{engine.FailInstance{Err: "cancelled"}},
			assert: func(t *testing.T, eventErr, driverErr string) {
				assert.Equal(t, "cancelled", eventErr)
				assert.Equal(t, "instance terminated", driverErr)
			},
		},
		{
			name: "compensation-failed alone with no terminal command falls to the status default at both sites",
			st: engine.InstanceState{
				InstanceID: "i-failed-only-no-cmd",
				Status:     engine.StatusFailed,
				Incidents: []engine.Incident{
					{ID: "inc-1", Kind: engine.IncidentCompensationFailed, CommandID: "c-undo", Error: "compensation action failed"},
				},
			},
			assert: func(t *testing.T, eventErr, driverErr string) {
				assert.Equal(t, "instance failed", eventErr)
				assert.Equal(t, "instance failed", driverErr)
			},
		},
		{
			// Control. Kind is left at its zero value on purpose: that is what every
			// pre-ADR-0175 stored incident decodes to, and the allow-list must keep
			// publishing it. This row passes both before and after the fix.
			name: "a zero-Kind action incident is still the published cause of death",
			st: engine.InstanceState{
				InstanceID: "i-action-only",
				Status:     engine.StatusFailed,
				Incidents: []engine.Incident{
					{ID: "inc-1", TokenID: "tok-1", NodeID: "chargeCard", Error: "charge-card: gateway timeout"},
				},
			},
			cmds: []engine.Command{engine.FailInstance{Err: "cancelled"}},
			assert: func(t *testing.T, eventErr, driverErr string) {
				assert.Equal(t, "charge-card: gateway timeout", eventErr)
				assert.Equal(t, "charge-card: gateway timeout", driverErr)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.assert(t, terminalEventErr(tc.st, tc.cmds), terminalErr(tc.st))
		})
	}
}

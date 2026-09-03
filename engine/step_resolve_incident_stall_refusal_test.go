package engine_test

// ResolveIncident refuses a compensation-stall incident.
//
// handleResolveIncident removes the incident BEFORE looking up the token and
// returns no commands when the token is nil. Measured on a TokenID:"" incident:
// err=<nil>, cmds=[], incidents=0 — it silently EATS it. So an operator hitting
// the already-shipped resolve-incident endpoint would delete the stall incident
// and get nothing back, making the stall invisible as well as unresolved.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/engine"
)

// TestResolveIncidentRefusesACompensationStall covers the refusal itself.
//
// ⚠ The fixture MUST be StatusCompensating. On a terminated instance dispatch's
// structural guard returns ErrInstanceTerminal first and the test would pass for
// entirely the wrong reason — see
// TestResolveIncidentOnATerminalInstanceIsRefusedAsTerminal below, which pins
// that the two refusals are distinguishable.
//
// ⚠ MUTATION-VERIFIED, and the prescribed mutation was the WRONG one. The design
// said the guard must sit BEFORE the s.Incidents removal line and that moving it
// below would redden the "incident still present" assertion. Measured:
// moving it below leaves this test fully GREEN. Step returns the zero StepResult
// on error, so the caller discards the clone whose slice the removal mutated —
// the position is defence-in-depth, not the protection. The mutation that does
// redden is DELETING the guard: the call then returns err=<nil> with the
// incident consumed, which is the earlier behaviour this exists to stop.
func TestResolveIncidentRefusesACompensationStall(t *testing.T) {
	state, _, timerID := startedStallWalk(t)
	def := threeCompensableDef()
	opt := engine.StepOptions{CompensationStallAfter: stallWindow}
	at := time.Date(2026, 6, 21, 11, 40, 0, 0, time.UTC)

	fired, err := engine.Step(t.Context(), def, state, engine.NewTimerFired(at, timerID), opt)
	require.NoError(t, err)
	require.Len(t, fired.State.Incidents, 1)
	inc := fired.State.Incidents[0]
	require.Equal(t, engine.IncidentCompensationStall, inc.Kind)
	require.Equal(t, engine.StatusCompensating, fired.State.Status,
		"fixture precondition: NOT terminal, or ErrInstanceTerminal answers first")

	res, err := engine.Step(t.Context(), def, fired.State,
		engine.NewResolveIncident(at.Add(time.Minute), inc.ID, 1), opt)

	require.Error(t, err, "resolve-incident must refuse a compensation-stall incident")
	assert.ErrorIs(t, err, engine.ErrIncidentNotResolvable)
	assert.Contains(t, err.Error(), "retry", "the error must name the verbs that DO work")
	assert.Empty(t, res.Commands, "and it must re-invoke nothing")

	// The load-bearing half: the incident SURVIVES the refusal and is still
	// nameable. Without the guard it is consumed and the stall becomes invisible.
	replay, err := engine.Step(t.Context(), def, fired.State,
		engine.NewSkipStalledCompensation(at.Add(2*time.Minute), inc.CommandID, inc.ID), opt)
	require.NoError(t, err,
		"the incident must still be nameable by an escape verb after the refusal")
	assert.NotNil(t, invokeActionNamed(replay.Commands, "c2"))
}

// TestResolveIncidentOnATerminalInstanceIsRefusedAsTerminal pins that the two
// refusals must be told apart, or the stall refusal could be passing on the
// terminal guard.
func TestResolveIncidentOnATerminalInstanceIsRefusedAsTerminal(t *testing.T) {
	state, cmdID, timerID := startedStallWalk(t)
	def := threeCompensableDef()
	opt := engine.StepOptions{CompensationStallAfter: stallWindow}
	at := time.Date(2026, 6, 21, 11, 40, 0, 0, time.UTC)

	fired, err := engine.Step(t.Context(), def, state, engine.NewTimerFired(at, timerID), opt)
	require.NoError(t, err)
	incID := fired.State.Incidents[0].ID

	// Abandon terminates the instance (walkAdmin walk).
	dead, err := engine.Step(t.Context(), def, fired.State,
		engine.NewAbandonCompensationWalk(at.Add(time.Minute), cmdID, incID), opt)
	require.NoError(t, err)
	require.True(t, dead.State.Status.IsTerminal(), "fixture precondition: now terminal")

	_, err = engine.Step(t.Context(), def, dead.State,
		engine.NewResolveIncident(at.Add(2*time.Minute), incID, 1), opt)

	require.Error(t, err)
	assert.ErrorIs(t, err, engine.ErrInstanceTerminal,
		"on a terminal instance the STRUCTURAL guard answers first, not the kind refusal")
	assert.NotErrorIs(t, err, engine.ErrIncidentNotResolvable,
		"the two refusals must be distinguishable")
}

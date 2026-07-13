package engine_test

// step_compensation_finish_endedat_test.go — T3 (ADR-0109 hardening) regression.
//
// The unified-resume refactor of stepCompensationFinish (finishPlan/applyFinish)
// clears s.EndedAt on EVERY resume outcome (throw-resume, partial-rollback,
// full-reverse). Before the refactor only the full-reverse branch cleared it,
// so a partial-rollback or throw-resume that finished with a stale EndedAt left
// a Running instance carrying an end timestamp — an invariant violation
// (finding #4). These cases hand-inject a stale EndedAt onto a mid-walk state
// and prove the resume clears it.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
)

func TestCompensationFinish_ClearsEndedAtOnResume(t *testing.T) {
	t0 := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)

	type testCase struct {
		name string
		// setup drives an instance to a mid-compensation-walk state whose NEXT
		// ActionCompleted finishes the walk on a RESUME outcome (Running, not
		// terminate). It returns the def, that mid-walk state, and the finishing
		// trigger. The test injects a stale EndedAt onto the mid-walk state before
		// applying the trigger.
		setup func(t *testing.T) (def *model.ProcessDefinition, midWalk engine.InstanceState, finish engine.Trigger)
	}

	cases := []testCase{
		{
			name: "partial rollback resume clears stale EndedAt",
			setup: func(t *testing.T) (*model.ProcessDefinition, engine.InstanceState, engine.Trigger) {
				t.Helper()
				def := reverseLoopDef()
				base := driveReverseLoopToCompletion(t, def, t0)
				// Partial rollback to "prep" walks the 3 svc "undo" records; the walk
				// finishes on completing the 3rd undo, resuming Running at "prep".
				p1, err := engine.Step(t.Context(), def, base.State, engine.NewCompensateRequested(t0, "prep"), engine.StepOptions{})
				require.NoError(t, err)
				undo1 := findInvokeActionID(t, p1.Commands, "undo")
				p2, err := engine.Step(t.Context(), def, p1.State, engine.NewActionCompleted(t0, undo1, nil), engine.StepOptions{})
				require.NoError(t, err)
				undo2 := findInvokeActionID(t, p2.Commands, "undo")
				p3, err := engine.Step(t.Context(), def, p2.State, engine.NewActionCompleted(t0, undo2, nil), engine.StepOptions{})
				require.NoError(t, err)
				undo3 := findInvokeActionID(t, p3.Commands, "undo")
				// p3.State is mid-walk (undo3 in flight); completing undo3 finishes.
				return def, p3.State, engine.NewActionCompleted(t0, undo3, nil)
			},
		},
		{
			name: "throw resume clears stale EndedAt",
			setup: func(t *testing.T) (*model.ProcessDefinition, engine.InstanceState, engine.Trigger) {
				t.Helper()
				def, r2 := driveToThrowEmitStep(t)
				require.Equal(t, engine.StatusCompensating, r2.State.Status, "precondition: throw walk in flight")
				cancelInner, ok := findInvokeAction(r2.Commands, "cancel-inner")
				require.True(t, ok, "precondition: throw walk emits cancel-inner")
				// r2.State is mid-walk (cancel-inner in flight); completing it finishes
				// the throw walk and resumes at "afterThrow" (Running, parked UserTask).
				return def, r2.State, engine.NewActionCompleted(t0, cancelInner.CommandID, nil)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			def, midWalk, finish := tc.setup(t)

			// Inject a stale end timestamp: a resume outcome must clear it so the
			// resumed (Running) instance never carries an EndedAt.
			ended := t0
			midWalk.EndedAt = &ended

			r, err := engine.Step(t.Context(), def, midWalk, finish, engine.StepOptions{})
			require.NoError(t, err)
			require.Equal(t, engine.StatusRunning, r.State.Status, "walk must finish on a RESUME (Running) outcome")
			assert.Nil(t, r.State.EndedAt, "resume must clear the stale EndedAt (Running instance carries no end timestamp)")
		})
	}
}

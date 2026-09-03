package engine_test

// step_compensation_duplicate_reply_test.go — a late or redelivered reply to a
// compensation command the walk has already moved past is
// a benign no-op, not ErrTokenNotFound.
//
// The defect: both reply handlers fall through their
// StatusCompensating short-circuit when the reply's CommandID is not the CURRENT
// ActiveCmdID, reach s.tokenAwaiting(CommandID), find nothing (a compensation
// walk parks no token on its dispatch), and return ErrTokenNotFound — which
// wraps ErrInvalidTransition and surfaces to a consumer as HTTP 422. An
// at-least-once worker's ordinary redelivery therefore looks like a client error
// on a perfectly healthy walk.
//
// ⚠ FIXTURE CHOICE IS LOAD-BEARING, and the two fixtures below are the two cells
// that can actually fail:
//
//   - MID-WALK superseded: the cancel walk has advanced from undo-beta to
//     undo-alpha and is still StatusCompensating. Measured RED:
//     `workflow-engine: no token awaiting command: workflow-engine: invalid state
//     transition: "i-dup-c3"`.
//   - POST-FINISH on a RESUMING walk: a compensation-throw walk has finished and
//     resumed the instance at afterThrow (StatusRunning). This is the cell
//     likeliest to be hit in production, and the reason
//     the ring lives on InstanceState rather than on the cursor — the cursor is
//     zeroed at both finish sites, so a cursor-resident set could not cover it.
//     Measured RED: `no token awaiting command: ... "throw-dup-c3"`.
//
// A post-finish CANCEL-walk fixture is deliberately NOT used: it would be
// vacuous. Measured on this tree with the predicate present, a reply to the
// resulting TERMINATED instance returns err=<nil> even for a command id that was
// never dispatched at all — the terminal dispatch guard short-circuits
// before either handler is entered, so such a row passes with the predicate
// absent.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
)

// duplicateReplyDef is
//
//	start → alpha(CompensateAction:"undo-alpha") → beta(CompensateAction:"undo-beta")
//	      → park(user task) → end
//
// The user task keeps the instance alive after both compensable activities have
// completed, so a CancelRequested has a TWO-record walk to run — which is what
// makes a superseded MID-walk reply constructible at all. With one record the
// walk finishes on the first reply and only the (vacuous) post-finish shape
// exists.
func duplicateReplyDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-compensation-duplicate-reply", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("alpha",
				activity.WithTaskAction("do-alpha"), activity.WithCompensateAction("undo-alpha")),
			activity.NewServiceTask("beta",
				activity.WithTaskAction("do-beta"), activity.WithCompensateAction("undo-beta")),
			activity.NewUserTask("park"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "alpha"},
			{ID: "f2", Source: "alpha", Target: "beta"},
			{ID: "f3", Source: "beta", Target: "park"},
			{ID: "f4", Source: "park", Target: "end"},
		},
	}
}

// driveToWalkStart runs duplicateReplyDef to a cancel-started compensation walk
// whose first dispatch is beta's undo action, returning that state and the
// dispatched command id.
//
// Every require is a control: if the drive stops producing a two-record walk the
// assertions downstream would go vacuously green.
func driveToWalkStart(t *testing.T, def *model.ProcessDefinition, at time.Time) (engine.InstanceState, string) {
	t.Helper()

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i-dup"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)

	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewActionCompleted(at.Add(time.Second), invokeIDForAction(r1.Commands, "do-alpha"), nil),
		engine.StepOptions{})
	require.NoError(t, err)

	r3, err := engine.Step(t.Context(), def, r2.State,
		engine.NewActionCompleted(at.Add(2*time.Second), invokeIDForAction(r2.Commands, "do-beta"), nil),
		engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, r3.State.Status,
		"control: the user task must keep the instance alive")

	r4, err := engine.Step(t.Context(), def, r3.State,
		engine.NewCancelRequested(at.Add(3*time.Second)), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompensating, r4.State.Status,
		"control: the cancel must have started a compensation walk")

	cmdID := invokeIDForAction(r4.Commands, "undo-beta")
	require.NotEmpty(t, cmdID, "control: the walk must have dispatched undo-beta")
	return r4.State, cmdID
}

// driveToSupersededReply advances the walk one record past undo-beta and returns
// the MID-walk state plus the now-superseded undo-beta command id.
func driveToSupersededReply(t *testing.T, def *model.ProcessDefinition, at time.Time) (engine.InstanceState, string) {
	t.Helper()

	state, betaCmdID := driveToWalkStart(t, def, at)

	res, err := engine.Step(t.Context(), def, state,
		engine.NewActionCompleted(at.Add(4*time.Second), betaCmdID, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompensating, res.State.Status,
		"control: the walk must still be MID-flight — a terminal instance would be "+
			"short-circuited by the terminal dispatch guard and the row could not fail")
	require.NotEmpty(t, invokeIDForAction(res.Commands, "undo-alpha"),
		"control: the walk must have SUPERSEDED undo-beta with undo-alpha, or there "+
			"is no late reply to test")

	return res.State, betaCmdID
}

// driveToFinishedResumingWalk runs rootSagaWithScopeWideThrow to a compensation
// THROW walk that has fully drained (undoB then undoA) and RESUMED the instance
// at afterThrow, returning the post-finish state and the first walk command id
// (undoB's), which by then has been superseded twice over.
//
// This is the cell that makes the ring's placement on InstanceState load-bearing:
// stepCompensationFinish zeroes s.Compensating here, so a cursor-resident id set
// would have been erased along with it. The instance is StatusRunning at the end,
// NOT terminal, so the terminal dispatch guard does not stand in front of the
// handler and the row can genuinely fail.
func driveToFinishedResumingWalk(t *testing.T, def *model.ProcessDefinition, at time.Time) (engine.InstanceState, string) {
	t.Helper()

	r3 := driveToScopeWideThrow(t, def, "throw-dup", at)
	undoB := invokeIDForAction(r3.Commands, "undoB")
	require.NotEmpty(t, undoB, "control: the throw must have dispatched undoB first")

	r4, err := engine.Step(t.Context(), def, r3.State,
		engine.NewActionCompleted(at.Add(3*time.Second), undoB, nil), engine.StepOptions{})
	require.NoError(t, err)
	undoA := invokeIDForAction(r4.Commands, "undoA")
	require.NotEmpty(t, undoA, "control: the walk must advance to undoA")

	r5, err := engine.Step(t.Context(), def, r4.State,
		engine.NewActionCompleted(at.Add(4*time.Second), undoA, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, r5.State.Status,
		"control: the throw walk RESUMES the instance — a terminal one would be "+
			"short-circuited by the terminal dispatch guard and the row could not fail")

	return r5.State, undoB
}

// TestActiveCompensationCommandIsNotTreatedAsDuplicate is the guard against the
// trap that turns this feature into a hung walk.
//
// The ring is appended to AT DISPATCH, so the in-flight command id is already in
// it the moment it is dispatched. A duplicate predicate written as bare
// membership — without the `!= ActiveCmdID` term, or placed BEFORE the
// short-circuit instead of after it — makes every normal reply a duplicate, the
// walk never advances, and a 422 has been traded for a permanent stall.
//
// ⚠ It asserts FORWARD PROGRESS, not the absence of an error: a hung walk also
// returns no error, so `require.NoError` alone could not tell the two apart.
func TestActiveCompensationCommandIsNotTreatedAsDuplicate(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	def := duplicateReplyDef()

	state, activeCmdID := driveToWalkStart(t, def, at)

	res, err := engine.Step(t.Context(), def, state,
		engine.NewActionCompleted(at.Add(4*time.Second), activeCmdID, nil), engine.StepOptions{})
	require.NoError(t, err)

	assert.NotEmpty(t, invokeIDForAction(res.Commands, "undo-alpha"),
		"the ACTIVE compensation command's reply must advance the walk to the next "+
			"record — treating it as a duplicate hangs the walk forever")
	assert.Equal(t, engine.StatusCompensating, res.State.Status,
		"the walk is still draining its second record")
}

// TestLateCompensationReplyIsABenignNoOp covers the late-reply no-op across both
// reply paths (handleActionCompleted and handleActionFailed) and both
// non-vacuous duplicate cells (mid-walk superseded, and post-finish on a walk
// that resumed).
//
// What makes every row fail without the predicate: the reply's CommandID is not
// the current ActiveCmdID, so the handler falls past its StatusCompensating
// short-circuit to s.tokenAwaiting(CommandID) — which finds nothing, because a
// compensation walk parks no token on its dispatch — and returns
// ErrTokenNotFound, wrapping ErrInvalidTransition. Measured on this tree with
// both predicate blocks deleted: all four rows RED with
// `workflow-engine: no token awaiting command: workflow-engine: invalid state
// transition: …`.
//
// No ctx modifier: neither handler consults ctx.Done or ctx.Err on this path.
func TestLateCompensationReplyIsABenignNoOp(t *testing.T) {
	at := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)

	type testCase struct {
		name       string
		setup      func(t *testing.T) (*model.ProcessDefinition, engine.InstanceState, string)
		trigger    func(supersededCmdID string) engine.Trigger
		wantStatus engine.Status
	}

	midWalk := func(t *testing.T) (*model.ProcessDefinition, engine.InstanceState, string) {
		t.Helper()
		def := duplicateReplyDef()
		state, cmdID := driveToSupersededReply(t, def, at)
		return def, state, cmdID
	}
	postFinish := func(t *testing.T) (*model.ProcessDefinition, engine.InstanceState, string) {
		t.Helper()
		def := rootSagaWithScopeWideThrow()
		state, cmdID := driveToFinishedResumingWalk(t, def, at)
		return def, state, cmdID
	}
	completed := func(supersededCmdID string) engine.Trigger {
		return engine.NewActionCompleted(at.Add(10*time.Second), supersededCmdID, nil)
	}
	failed := func(supersededCmdID string) engine.Trigger {
		return engine.NewActionFailed(at.Add(10*time.Second), supersededCmdID, "boom", true)
	}

	cases := []testCase{
		{
			name:       "mid-walk: a redelivered ActionCompleted for a superseded command",
			setup:      midWalk,
			trigger:    completed,
			wantStatus: engine.StatusCompensating,
		},
		{
			name:       "mid-walk: a late ActionFailed for a superseded command",
			setup:      midWalk,
			trigger:    failed,
			wantStatus: engine.StatusCompensating,
		},
		{
			name:       "post-finish: a redelivered ActionCompleted after the walk resumed",
			setup:      postFinish,
			trigger:    completed,
			wantStatus: engine.StatusRunning,
		},
		{
			name:       "post-finish: a late ActionFailed after the walk resumed",
			setup:      postFinish,
			trigger:    failed,
			wantStatus: engine.StatusRunning,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			def, state, supersededCmdID := tc.setup(t)
			before := state.Clone()

			res, err := engine.Step(t.Context(), def, state, tc.trigger(supersededCmdID),
				engine.StepOptions{})

			require.NoError(t, err,
				"a reply to a command the walk has moved past must NOT be an error — "+
					"ErrTokenNotFound wraps ErrInvalidTransition and reaches consumers as 422")
			assert.Empty(t, res.Commands,
				"a benign duplicate must emit nothing — re-dispatching would run a "+
					"money-moving compensation action twice")
			assert.Equal(t, tc.wantStatus, res.State.Status,
				"the healthy instance is untouched")
			assert.Equal(t, before.Incidents, res.State.Incidents,
				"a benign duplicate raises no incident: nothing is wrong")
			assert.Equal(t, before.History, res.State.History,
				"a benign duplicate advances nothing")
			assert.Equal(t, before.Tokens, res.State.Tokens,
				"a benign duplicate moves no token")
		})
	}
}

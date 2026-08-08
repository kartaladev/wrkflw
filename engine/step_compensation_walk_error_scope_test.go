package engine_test

import (
	"testing"
	"time"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/gateway"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var walkScopeErrorT0 = time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)

// targetedThrowVsUncaughtErrorDef builds the narrow-record-source reproduction
// for a TARGETED compensation throw:
//
//	start → svcRoot(doRoot/undoRoot) → sub(subStart → svcInner(doInner/undoInner) → subEnd)
//	      → fork ⇒
//	  A: taskA [bndA "s1"] → rb(CompensateThrow ref="sub") → endA ; taskA → endTaskA
//	  B: taskB [bndB "s2"] → errEnd(EndError "boom", uncaught)    ; taskB → endTaskB
//
// The throw's record source is ArchivedCompensations["sub"] — it can only ever
// reach undoInner. undoRoot lives in RootCompensations, OUTSIDE that source, and
// is the record the uncaught error must still get compensated.
//
// The two wiring traps of this repo's throw fixtures apply: it is the BOUNDARY's
// outgoing flow that reaches rb (taskA still needs its own normal outgoing flow),
// and rb MUST carry an outgoing flow or compensationThrowEventStrategy.enter
// auto-advances and no walk starts at all.
func targetedThrowVsUncaughtErrorDef() *model.ProcessDefinition {
	nested := &model.ProcessDefinition{
		ID: "p-targeted-throw-sub", Version: 1,
		Nodes: []model.Node{
			event.NewStart("subStart"),
			activity.NewServiceTask("svcInner",
				activity.WithTaskAction("doInner"),
				activity.WithCompensateAction("undoInner"),
			),
			event.NewEnd("subEnd"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "sf1", Source: "subStart", Target: "svcInner"},
			{ID: "sf2", Source: "svcInner", Target: "subEnd"},
		},
	}
	return &model.ProcessDefinition{
		ID: "p-targeted-throw-vs-uncaught-error", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("svcRoot",
				activity.WithTaskAction("doRoot"),
				activity.WithCompensateAction("undoRoot"),
			),
			activity.NewSubProcess("sub", nested),
			gateway.NewParallel("fork"),
			activity.NewUserTask("taskA"),
			event.NewBoundary("bndA", "taskA", event.WithSignalName("s1")),
			event.NewCompensateThrow("rb", event.WithCompensateRef("sub")),
			event.NewEnd("endA"),
			event.NewEnd("endTaskA"),
			activity.NewUserTask("taskB"),
			event.NewBoundary("bndB", "taskB", event.WithSignalName("s2")),
			event.NewEnd("errEnd", event.WithErrorCode("boom")),
			event.NewEnd("endTaskB"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f-start", Source: "start", Target: "svcRoot"},
			{ID: "f-root-sub", Source: "svcRoot", Target: "sub"},
			{ID: "f-sub-fork", Source: "sub", Target: "fork"},
			{ID: "f-fork-a", Source: "fork", Target: "taskA"},
			{ID: "f-fork-b", Source: "fork", Target: "taskB"},
			{ID: "f-a-end", Source: "taskA", Target: "endTaskA"},
			{ID: "f-bnda-rb", Source: "bndA", Target: "rb"},
			{ID: "f-rb-enda", Source: "rb", Target: "endA"},
			{ID: "f-b-end", Source: "taskB", Target: "endTaskB"},
			{ID: "f-bndb-err", Source: "bndB", Target: "errEnd"},
		},
	}
}

// nestedScopeWideThrowVsUncaughtErrorDef builds the narrow-record-source
// reproduction for a SCOPE-WIDE compensation throw raised INSIDE a sub-process
// scope. withRootRecord selects between the two halves of the defect:
//
//	true:  start → svcRoot(doRoot/undoRoot) → sub → rootEnd
//	false: start → sub → rootEnd
//
//	sub: subStart → svcB(doB/undoB) → innerFork ⇒
//	   A: taskA [bndA "s1"] → rb(CompensateThrow, no ref) → innerEndA ; taskA → innerEndTaskA
//	   B: taskB [bndB "s2"] → errEnd(EndError "boom", uncaught)       ; taskB → innerEndTaskB
//
// The throw's record source is the SUB scope's own Compensations — it can only
// reach undoB. With withRootRecord, undoRoot sits in RootCompensations outside
// that source. Without it, the instance has NO records outside the walk at all,
// which is the case that reaches handleUnhandledError's records guard as false.
func nestedScopeWideThrowVsUncaughtErrorDef(withRootRecord bool) *model.ProcessDefinition {
	nested := &model.ProcessDefinition{
		ID: "p-nested-scopewide-sub", Version: 1,
		Nodes: []model.Node{
			event.NewStart("subStart"),
			activity.NewServiceTask("svcB",
				activity.WithTaskAction("doB"),
				activity.WithCompensateAction("undoB"),
			),
			gateway.NewParallel("innerFork"),
			activity.NewUserTask("taskA"),
			event.NewBoundary("bndA", "taskA", event.WithSignalName("s1")),
			event.NewCompensateThrow("rb"),
			event.NewEnd("innerEndA"),
			event.NewEnd("innerEndTaskA"),
			activity.NewUserTask("taskB"),
			event.NewBoundary("bndB", "taskB", event.WithSignalName("s2")),
			event.NewEnd("errEnd", event.WithErrorCode("boom")),
			event.NewEnd("innerEndTaskB"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "sf-start", Source: "subStart", Target: "svcB"},
			{ID: "sf-b-fork", Source: "svcB", Target: "innerFork"},
			{ID: "sf-fork-a", Source: "innerFork", Target: "taskA"},
			{ID: "sf-fork-b", Source: "innerFork", Target: "taskB"},
			{ID: "sf-a-end", Source: "taskA", Target: "innerEndTaskA"},
			{ID: "sf-bnda-rb", Source: "bndA", Target: "rb"},
			{ID: "sf-rb-enda", Source: "rb", Target: "innerEndA"},
			{ID: "sf-b-end", Source: "taskB", Target: "innerEndTaskB"},
			{ID: "sf-bndb-err", Source: "bndB", Target: "errEnd"},
		},
	}

	nodes := []model.Node{
		event.NewStart("start"),
		activity.NewSubProcess("sub", nested),
		event.NewEnd("rootEnd"),
	}
	flows := []flow.SequenceFlow{
		{ID: "f-sub-end", Source: "sub", Target: "rootEnd"},
	}
	if withRootRecord {
		nodes = append(nodes, activity.NewServiceTask("svcRoot",
			activity.WithTaskAction("doRoot"),
			activity.WithCompensateAction("undoRoot"),
		))
		flows = append(flows,
			flow.SequenceFlow{ID: "f-start", Source: "start", Target: "svcRoot"},
			flow.SequenceFlow{ID: "f-root-sub", Source: "svcRoot", Target: "sub"},
		)
	} else {
		flows = append(flows, flow.SequenceFlow{ID: "f-start", Source: "start", Target: "sub"})
	}

	return &model.ProcessDefinition{
		ID: "p-nested-scopewide-vs-uncaught-error", Version: 1,
		Nodes: nodes, Flows: flows,
	}
}

// midWalkErrorRun is one full observation of "an uncaught error arrives while a
// compensation walk is in flight", from the walk's start to the instance's
// terminal state.
type midWalkErrorRun struct {
	// walkCmdID is Compensating.ActiveCmdID as it stood when the walk started.
	walkCmdID string
	// actionsAtWalkStart / actionsAtError / actionsAfterError are the compensation
	// action names dispatched by the walk-start step, the error step, and every
	// step that follows the cursor to exhaustion, in order.
	actionsAtWalkStart []string
	actionsAtError     []string
	actionsAfterError  []string
	// stateAtError / finalState are the instance immediately after the uncaught
	// error and after the cursor has been followed to exhaustion.
	stateAtError engine.InstanceState
	finalState   engine.InstanceState
	// failInstanceErrs / completeInstances are the terminal commands observed
	// while draining.
	failInstanceErrs  []string
	completeInstances int
	// drainErr is the first error a drain Step returned, if any.
	drainErr error
}

// completePendingActions delivers ActionCompleted for the single InvokeAction a
// setup step emitted, repeatedly, until a step emits none. It is the shared
// arrival path for fixtures whose setup dispatches a different number of
// ordinary (non-compensation) actions.
func completePendingActions(t *testing.T, def *model.ProcessDefinition, st engine.InstanceState, cmds []engine.Command, at time.Time) engine.InstanceState {
	t.Helper()
	for step := 0; step < 8; step++ {
		var pending []engine.InvokeAction
		for _, c := range cmds {
			if ia, ok := c.(engine.InvokeAction); ok {
				pending = append(pending, ia)
			}
		}
		if len(pending) == 0 {
			return st
		}
		require.Len(t, pending, 1, "setup: expected exactly one action per arrival step")
		at = at.Add(time.Second)
		r, err := engine.Step(t.Context(), def, st, engine.NewActionCompleted(at, pending[0].CommandID, nil), engine.StepOptions{})
		require.NoError(t, err)
		st, cmds = r.State, r.Commands
	}
	require.Fail(t, "setup: arrival did not settle")
	return st
}

// driveMidWalkUncaughtError drives def through: start → (complete every setup
// action) → signal "s1" (starts the compensation walk) → signal "s2" (raises the
// uncaught error mid-walk) → follow the compensation cursor to exhaustion.
//
// Every require here is a setup control, not an assertion about the fix.
func driveMidWalkUncaughtError(t *testing.T, def *model.ProcessDefinition, instanceID string) midWalkErrorRun {
	t.Helper()

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: instanceID},
		engine.NewStartInstance(walkScopeErrorT0, nil), engine.StepOptions{})
	require.NoError(t, err)
	st := completePendingActions(t, def, r1.State, r1.Commands, walkScopeErrorT0)
	require.NotEqual(t, engine.StatusCompensating, st.Status, "setup: no walk before s1")

	r3, err := engine.Step(t.Context(), def, st,
		engine.NewSignalReceived(walkScopeErrorT0.Add(10*time.Second), "s1", nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompensating, r3.State.Status, "setup: s1 must start the compensation walk")
	require.NotEmpty(t, r3.State.Compensating.ActiveCmdID, "setup: the walk cursor must be live")
	require.NotEmpty(t, r3.State.Compensating.ResumeNode, "setup: a throw walk must carry a resume target")

	run := midWalkErrorRun{
		walkCmdID:          r3.State.Compensating.ActiveCmdID,
		actionsAtWalkStart: invokeActionNames(r3.Commands),
	}

	r4, err := engine.Step(t.Context(), def, r3.State,
		engine.NewSignalReceived(walkScopeErrorT0.Add(20*time.Second), "s2", nil), engine.StepOptions{})
	require.NoError(t, err)
	run.actionsAtError = invokeActionNames(r4.Commands)
	run.stateAtError = r4.State

	st = r4.State
	at := walkScopeErrorT0.Add(30 * time.Second)
	for drain := 0; drain < 8; drain++ {
		if st.Status.IsTerminal() || st.Compensating.ActiveCmdID == "" {
			break
		}
		at = at.Add(time.Second)
		r, stepErr := engine.Step(t.Context(), def, st, engine.NewActionCompleted(at, st.Compensating.ActiveCmdID, nil), engine.StepOptions{})
		if stepErr != nil {
			run.drainErr = stepErr
			break
		}
		run.actionsAfterError = append(run.actionsAfterError, invokeActionNames(r.Commands)...)
		for _, c := range r.Commands {
			switch v := c.(type) {
			case engine.FailInstance:
				run.failInstanceErrs = append(run.failInstanceErrs, v.Err)
			case engine.CompleteInstance:
				run.completeInstances++
			}
		}
		st = r.State
	}
	run.finalState = st
	return run
}

func invokeActionNames(cmds []engine.Command) []string {
	var names []string
	for _, c := range cmds {
		if ia, ok := c.(engine.InvokeAction); ok {
			names = append(names, ia.Name)
		}
	}
	return names
}

// TestUncaughtErrorMidWalkCompensatesRecordsOutsideTheWalkSource pins that a
// deferred termination compensates EVERY record, not only the ones the in-flight
// walk's own narrow record source could reach.
//
// Measured before the fix (same fixtures, `go test ./engine/`):
//
//	targeted throw            actionsAfterError=[]        final=failed  RootCompensations 1→0 (undoRoot ERASED)
//	nested scope-wide + root  actionsAfterError=[]        final=failed  RootCompensations 1 stranded
//	nested scope-wide, none   statusAtError=failed  activeCmd=""  walk ABANDONED mid-flight
//
// so every "afterError" row below, and the "still compensating" row, go RED
// against the pre-fix engine.
func TestUncaughtErrorMidWalkCompensatesRecordsOutsideTheWalkSource(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name       string
		instanceID string
		def        *model.ProcessDefinition
		assert     func(t *testing.T, run midWalkErrorRun)
	}

	cases := []testCase{
		{
			name:       "targeted throw leaves the root record uncompensated",
			instanceID: "i-targeted",
			def:        targetedThrowVsUncaughtErrorDef(),
			assert: func(t *testing.T, run midWalkErrorRun) {
				require.Equal(t, []string{"undoInner"}, run.actionsAtWalkStart,
					"control: the targeted walk compensates the archived sub-process record")
				require.Len(t, run.stateAtError.RootCompensations, 1,
					"control: undoRoot's record is outside the walk's archive source")

				assert.Empty(t, run.actionsAtError,
					"the error must not dispatch a second compensation action")
				assert.Equal(t, []string{"undoRoot"}, run.actionsAfterError,
					"the root record must be compensated by the deferred termination")
				assert.NotContains(t, run.actionsAfterError, "undoInner",
					"the archived record must not be compensated twice")
			},
		},
		{
			name:       "nested scope-wide throw leaves the root record uncompensated",
			instanceID: "i-nested-root",
			def:        nestedScopeWideThrowVsUncaughtErrorDef(true),
			assert: func(t *testing.T, run midWalkErrorRun) {
				require.Equal(t, []string{"undoB"}, run.actionsAtWalkStart,
					"control: the scope-wide walk compensates the sub scope's own record")
				require.Len(t, run.stateAtError.RootCompensations, 1,
					"control: undoRoot's record is outside the walk's scope source")

				assert.Empty(t, run.actionsAtError,
					"the error must not dispatch a second compensation action")
				assert.Equal(t, []string{"undoRoot"}, run.actionsAfterError,
					"the root record must be compensated by the deferred termination")
			},
		},
		{
			name:       "nested scope-wide throw with no record outside the walk",
			instanceID: "i-nested-bare",
			def:        nestedScopeWideThrowVsUncaughtErrorDef(false),
			assert: func(t *testing.T, run midWalkErrorRun) {
				require.Equal(t, []string{"undoB"}, run.actionsAtWalkStart,
					"control: the scope-wide walk compensates the sub scope's own record")
				require.Empty(t, run.stateAtError.RootCompensations,
					"control: nothing lives outside the walk's scope source here")

				assert.Equal(t, engine.StatusCompensating, run.stateAtError.Status,
					"the walk must not be abandoned mid-flight by the error")
				assert.Equal(t, run.walkCmdID, run.stateAtError.Compensating.ActiveCmdID,
					"the in-flight compensation command must keep its awaiter")
				assert.Empty(t, run.actionsAfterError,
					"there is nothing left to compensate once the walk drains")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			run := driveMidWalkUncaughtError(t, tc.def, tc.instanceID)
			require.NoError(t, run.drainErr, "following the cursor to exhaustion must not error")

			// Shared terminal contract: every one of these ends failed, carrying
			// the uncaught code, and never reports success.
			assert.Equal(t, engine.StatusFailed, run.finalState.Status,
				"a rollback ended by an uncaught error must leave the instance failed")
			assert.Equal(t, []string{"boom"}, run.failInstanceErrs,
				"exactly one FailInstance carrying the uncaught code")
			assert.Zero(t, run.completeInstances, "the instance must never be reported complete")
			assert.Empty(t, run.finalState.Tokens, "no token may survive the failure")

			tc.assert(t, run)
		})
	}
}

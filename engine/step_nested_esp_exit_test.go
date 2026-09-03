package engine_test

// step_nested_esp_exit_test.go — two things inside
// exitNestedEventSubprocessScope (engine/step_nodes.go) were uncovered, and the
// function's own comment says so, having measured both ablations:
//
//   - the non-completing tail
//     `appendCancelTimers(cmds, c.s.removeEventTriggeredSubprocessArmsForScope(parentScopeID))`
//     — replacing it with a no-op left `go test ./engine/...` at EXIT=0;
//   - the `&& c.s.Compensating.ActiveCmdID == ""` conjunct in the
//     root-completion branch — deleting it likewise left the suite green, with
//     the test that supposedly covered it RUNNING and PASSING.
//
// The arm retirement is the whole reason that call exists: an event-sub-process
// arm outliving the scope it belongs to is a leaked scheduler job. The conjunct
// is the rule that an outstanding compensation walk blocks completion.
//
// Both tests below are pins on already-correct code, so neither can produce an
// honest RED by being written. Both are instead MUTATION-verified against the
// exact ablations the production comment names — see the delivery's evidence file
// for the observed failures.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/gateway"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
)

var nestedESPExitT0 = time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)

// nestedESPNonRootGrandparentDef puts THREE scope levels above the nested event
// sub-process so its exit resumes in a NON-ROOT grandparent — which is the path
// that falls through to the arm-retirement tail rather than to the completion
// block.
//
//	root:  start → mid[SubProcess] → root-end
//	mid:   mid-start → outer[SubProcess] → mid-end
//	outer: outer-start → gate[UserTask] → outer-end
//	       [n-esp   — signal "boom", non-interrupting]  ← fires, then exits
//	       [sib-esp — timer "1h"]                       ← stays ARMED throughout
//
// sib-esp is the observable: it is an event-sub-process arm belonging to the
// ENCLOSING (outer) scope, never fired, so when the nested event sub-process's
// exit closes that scope the arm must be retired with it. "outer" HAS an
// outgoing flow inside mid, so resumeInParentScope succeeds and the completion
// block is skipped — the tail is the only thing that can emit the CancelTimer.
func nestedESPNonRootGrandparentDef() *model.ProcessDefinition {
	nespBody := &model.ProcessDefinition{
		ID: "nesp-body", Version: 1,
		Nodes: []model.Node{
			event.NewStart("n-esp-start", event.WithSignalName("boom"), event.WithNonInterrupting()),
			activity.NewServiceTask("n-esp-svc", activity.WithTaskAction("n-esp-action")),
			event.NewEnd("n-esp-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "nf1", Source: "n-esp-start", Target: "n-esp-svc"},
			{ID: "nf2", Source: "n-esp-svc", Target: "n-esp-end"},
		},
	}
	sibESPBody := &model.ProcessDefinition{
		ID: "sib-esp-body", Version: 1,
		Nodes: []model.Node{
			event.NewStart("sib-esp-start",
				event.WithStartTimer(schedule.AfterExpr(`"1h"`)),
				event.WithNonInterrupting()),
			event.NewEnd("sib-esp-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "sf1", Source: "sib-esp-start", Target: "sib-esp-end"},
		},
	}
	outerBody := &model.ProcessDefinition{
		ID: "outer-body", Version: 1,
		Nodes: []model.Node{
			event.NewStart("outer-start"),
			activity.NewUserTask("gate", activity.WithEligibleRoles("ops")),
			event.NewEnd("outer-end"),
			activity.NewSubProcess("n-esp", nespBody),
			activity.NewSubProcess("sib-esp", sibESPBody),
		},
		Flows: []flow.SequenceFlow{
			{ID: "of1", Source: "outer-start", Target: "gate"},
			{ID: "of2", Source: "gate", Target: "outer-end"},
		},
	}
	midBody := &model.ProcessDefinition{
		ID: "mid-body", Version: 1,
		Nodes: []model.Node{
			event.NewStart("mid-start"),
			activity.NewSubProcess("outer", outerBody),
			event.NewEnd("mid-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "mf1", Source: "mid-start", Target: "outer"},
			{ID: "mf2", Source: "outer", Target: "mid-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "p-nested-esp-nonroot", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewParallel("fork"),
			activity.NewSubProcess("mid", midBody),
			// hold keeps the INSTANCE alive after mid completes. Without it the
			// same Step that exits the event sub-process also completes the
			// instance, and endInstance's own cancelAllScheduledWork sweep emits
			// the CancelTimer -- which made the first version of this test pass
			// with the arm-retirement tail replaced by a no-op. Measured, not
			// reasoned: mutation A returned EXIT=0 until this branch was added.
			activity.NewUserTask("hold", activity.WithEligibleRoles("ops")),
			event.NewEnd("root-end"),
			event.NewEnd("hold-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "fork"},
			{ID: "f2", Source: "fork", Target: "mid"},
			{ID: "f3", Source: "fork", Target: "hold"},
			{ID: "f4", Source: "mid", Target: "root-end"},
			{ID: "f5", Source: "hold", Target: "hold-end"},
		},
	}
}

// TestNestedEventSubprocessExitRetiresEnclosingScopeArms pins the non-completing
// tail: when a nested event sub-process's exit closes its enclosing scope and
// resumes in a non-root grandparent, every event-sub-process arm belonging to
// that now-closed scope must be retired.
//
// What makes it fail: replacing the arm-retirement tail with a no-op. The arm
// then survives its scope as a leaked scheduler job and no CancelTimer is
// emitted.
func TestNestedEventSubprocessExitRetiresEnclosingScopeArms(t *testing.T) {
	t.Parallel()

	def := nestedESPNonRootGrandparentDef()
	actor := authz.Actor{ID: "alice", Roles: []string{"ops"}}

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i-nested-esp-nonroot"},
		engine.NewStartInstance(nestedESPExitT0, nil), engine.StepOptions{})
	require.NoError(t, err)

	// The sibling event sub-process's timer arm, armed when the outer scope opened.
	var sibArmTimerID string
	for _, c := range r1.Commands {
		if st, ok := c.(engine.ScheduleTimer); ok {
			sibArmTimerID = st.TimerID
		}
	}
	require.NotEmpty(t, sibArmTimerID,
		"setup: the sibling event sub-process's timer arm must be scheduled")

	// 1. Fire the nested non-interrupting event sub-process alongside the gate.
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewSignalReceived(nestedESPExitT0.Add(time.Minute), "boom", nil), engine.StepOptions{})
	require.NoError(t, err)
	nespCmdID := firstInvokeCommandID(t, r2.Commands)

	// 2. Complete the gate: the outer scope drains but is held open by the event
	//    sub-process still running inside it.
	r3, err := engine.Step(t.Context(), def, r2.State,
		engine.NewHumanCompleted(nestedESPExitT0.Add(2*time.Minute), taskIDForNode(t, r2.State, "gate"),
			engine.CompletionInput{}, actor), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, r3.State.Status,
		"control: the outer scope must be HELD OPEN, so the event-sub-process exit is what closes it")

	// 3. Complete the event sub-process. Its exit closes the enclosing scope and
	//    resumes at mid-end in the NON-ROOT grandparent, so the completion block
	//    is skipped and the arm-retirement tail runs.
	r4, err := engine.Step(t.Context(), def, r3.State,
		engine.NewActionCompleted(nestedESPExitT0.Add(3*time.Minute), nespCmdID, nil), engine.StepOptions{})
	require.NoError(t, err)

	// ⚠ VACUITY GUARD. If the instance completes in this Step, endInstance's
	// cancelAllScheduledWork sweep emits the CancelTimer and the assertion below
	// passes without the tail ever running. Pin that it stayed alive.
	require.Equal(t, engine.StatusRunning, r4.State.Status,
		"the sibling branch must keep the instance alive, so endInstance's sweep cannot supply the CancelTimer")

	cancelled := make([]string, 0, len(r4.Commands))
	for _, c := range r4.Commands {
		if ct, ok := c.(engine.CancelTimer); ok {
			cancelled = append(cancelled, ct.TimerID)
		}
	}
	assert.Contains(t, cancelled, sibArmTimerID,
		"the enclosing scope's surviving event-sub-process arm must be retired when that scope closes")
}

// TestNestedEventSubprocessRootExitBlockedByLiveCompensationWalk pins the
// `&& c.s.Compensating.ActiveCmdID == ""` conjunct: at the root-grandparent
// completion block, an outstanding compensation walk must prevent the instance
// from completing.
//
// What makes it fail: deleting that conjunct. The instance then reaches
// StatusCompleted with a walk still in flight — the walk's own ActionCompleted
// would arrive at an already-terminal instance and be dropped.
//
// The cursor is planted rather than driven: no production path reaches THIS exit
// with a live walk (which is exactly why the conjunct measured as uncovered), and
// the guard is a claim about the cursor's state, not about how it got there.
func TestNestedEventSubprocessRootExitBlockedByLiveCompensationWalk(t *testing.T) {
	t.Parallel()

	def := nestedEventSubprocessNoOutgoingFlowDef()
	actor := authz.Actor{ID: "alice", Roles: []string{"ops"}}

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i-nested-esp-walk"},
		engine.NewStartInstance(nestedESPExitT0, nil), engine.StepOptions{})
	require.NoError(t, err)

	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewSignalReceived(nestedESPExitT0.Add(time.Minute), "boom", nil), engine.StepOptions{})
	require.NoError(t, err)
	nespCmdID := firstInvokeCommandID(t, r2.Commands)

	r3, err := engine.Step(t.Context(), def, r2.State,
		engine.NewHumanCompleted(nestedESPExitT0.Add(2*time.Minute), taskIDForNode(t, r2.State, "gate"),
			engine.CompletionInput{}, actor), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, r3.State.Status)

	// Plant the outstanding walk, then take the exit that would otherwise complete.
	st := r3.State
	engine.SetActiveCompensationCmdID(&st, "walk-cmd-1")

	r4, err := engine.Step(t.Context(), def, st,
		engine.NewActionCompleted(nestedESPExitT0.Add(3*time.Minute), nespCmdID, nil), engine.StepOptions{})
	require.NoError(t, err)

	assert.NotEqual(t, engine.StatusCompleted, r4.State.Status,
		"an outstanding compensation walk must block completion at the nested event-sub-process exit")
	for _, c := range r4.Commands {
		_, isComplete := c.(engine.CompleteInstance)
		assert.False(t, isComplete, "no CompleteInstance may be emitted while a walk is outstanding")
	}
}

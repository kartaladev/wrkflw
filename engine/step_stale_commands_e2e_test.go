package engine_test

// step_stale_commands_e2e_test.go — the stale-command filter end to end, through
// engine.Step.
//
// Every "the command was dropped" row carries a POSITIVE CONTROL, because
// absence is satisfied two ways: the filter dropped it, or it was never emitted.
// The control asserts the node was entered and abnormally closed in this step,
// so a fixture whose parking branch never ran fails loudly instead of passing
// green against an empty implementation.
//
// Branch declaration order is load-bearing throughout: forkParallel places
// tokens in Outgoing() order, drive takes firstActive, and forceTerminate
// returns halt=true so drive stops immediately. The PARKING branch's flow must
// be declared BEFORE the destroying branch's.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/gateway"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
)

var staleT0 = time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)

// forkThenTerminateDef returns:
//
//	Start → fork ⇒ { f1: parking (declared FIRST) ; f2: halt = force-terminate }
//
// parking is supplied by the caller so the same shape covers a ServiceTask, a
// UserTask and a CallActivity.
func forkThenTerminateDef(parking model.Node) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-stale-fork", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewParallel("fork"),
			parking,
			event.NewEnd("halt", event.WithForceTermination("kill", event.OutcomeAbort)),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f-start", Source: "start", Target: "fork"},
			// f1 BEFORE f2: the parking branch must be driven first.
			{ID: "f1", Source: "fork", Target: parking.ID()},
			{ID: "f2", Source: "fork", Target: "halt"},
			{ID: "f-park-end", Source: parking.ID(), Target: "end"},
		},
	}
}

func hasInvokeAction(cmds []engine.Command, name string) bool {
	for _, c := range cmds {
		if ia, ok := c.(engine.InvokeAction); ok && ia.Name == name {
			return true
		}
	}
	return false
}

func awaitHumanCount(cmds []engine.Command) int {
	n := 0
	for _, c := range cmds {
		if _, ok := c.(engine.AwaitHuman); ok {
			n++
		}
	}
	return n
}

func updateTaskCount(cmds []engine.Command) int {
	n := 0
	for _, c := range cmds {
		if _, ok := c.(engine.UpdateTask); ok {
			n++
		}
	}
	return n
}

func startSubInstanceCount(cmds []engine.Command) int {
	n := 0
	for _, c := range cmds {
		if _, ok := c.(engine.StartSubInstance); ok {
			n++
		}
	}
	return n
}

// TestStaleCommandFilterE2E pins the stale-command filter through the public
// Step API. The headline property needs no signal, no arms and no multi-arm
// dispatch: a bare StartInstance on a parallel fork whose sibling
// force-terminates is enough.
func TestStaleCommandFilterE2E(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		def    *model.ProcessDefinition
		assert func(t *testing.T, res engine.StepResult, err error)
	}

	cases := []testCase{
		{
			name: "a force-terminated sibling drops the parking branch's InvokeAction",
			def: forkThenTerminateDef(
				activity.NewServiceTask("work", activity.WithTaskAction("charge")),
			),
			assert: func(t *testing.T, res engine.StepResult, err error) {
				require.NoError(t, err)
				// Positive control: the node WAS entered and abnormally closed,
				// so the InvokeAction genuinely existed before the filter ran.
				kind, ok := closeKindOf(res.State, "work")
				require.True(t, ok, "control: the parking branch must have been driven")
				assert.Equal(t, engine.CloseKindTerminated, kind)

				assert.False(t, hasInvokeAction(res.Commands, "charge"),
					"the action must not run for a token force-termination destroyed")
			},
		},
		{
			name: "a force-terminated sibling drops the parking branch's AwaitHuman",
			def: forkThenTerminateDef(
				activity.NewUserTask("review"),
			),
			assert: func(t *testing.T, res engine.StepResult, err error) {
				require.NoError(t, err)
				kind, ok := closeKindOf(res.State, "review")
				require.True(t, ok, "control: the parking branch must have been driven")
				assert.Equal(t, engine.CloseKindTerminated, kind)

				assert.Zero(t, awaitHumanCount(res.Commands),
					"no inbox task may be minted for a destroyed token")
				// cancelOpenTasks already cancelled the record on the terminal
				// path, so the filter's IsOpen guard makes it a no-op here and
				// exactly ONE UpdateTask is emitted, not two.
				require.Len(t, res.State.Tasks, 1)
				assert.Equal(t, humantask.Cancelled, res.State.Tasks[0].State)
				assert.Equal(t, 1, updateTaskCount(res.Commands),
					"the IsOpen guard must not add a second UpdateTask")
			},
		},
		{
			name: "a force-terminated sibling drops the parking branch's StartSubInstance",
			def: forkThenTerminateDef(
				activity.NewCallActivity("child", model.Qualifier{ID: "sub"}),
			),
			assert: func(t *testing.T, res engine.StepResult, err error) {
				require.NoError(t, err)
				kind, ok := closeKindOf(res.State, "child")
				require.True(t, ok, "control: the parking branch must have been driven")
				assert.Equal(t, engine.CloseKindTerminated, kind)

				assert.Zero(t, startSubInstanceCount(res.Commands),
					"an orphan child instance must not be started")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			res, err := engine.Step(t.Context(), tc.def, engine.InstanceState{InstanceID: "i1"},
				engine.NewStartInstance(staleT0, nil), engine.StepOptions{})
			tc.assert(t, res, err)
		})
	}
}

// compensableThenThrowDef returns:
//
//	Start → S(ServiceTask, compensable) → tail…
//
// tail is appended by the caller so the same prefix serves both the
// walk-survives and the walk-then-terminate fixtures. Driving it needs TWO
// steps: StartInstance parks on S's InvokeAction, and ActionCompleted records
// the compensation (step_triggers.go:104) before execution reaches the tail.
func compensableThenThrowDef(tailNodes []model.Node, tailFlows []flow.SequenceFlow) *model.ProcessDefinition {
	nodes := []model.Node{
		event.NewStart("start"),
		activity.NewServiceTask("charge",
			activity.WithTaskAction("charge-action"),
			activity.WithCompensateAction("refund-action"),
		),
	}
	flows := []flow.SequenceFlow{
		{ID: "f-start", Source: "start", Target: "charge"},
	}
	return &model.ProcessDefinition{
		ID: "p-stale-comp", Version: 1,
		Nodes: append(nodes, tailNodes...),
		Flows: append(flows, tailFlows...),
	}
}

func invokeActionByID(cmds []engine.Command, id string) bool {
	for _, c := range cmds {
		if ia, ok := c.(engine.InvokeAction); ok && ia.CommandID == id {
			return true
		}
	}
	return false
}

// firstInvokeCommandID returns the CommandID of the first non-fire-and-forget
// InvokeAction in cmds.
func firstInvokeCommandID(t *testing.T, cmds []engine.Command) string {
	t.Helper()
	for _, c := range cmds {
		if ia, ok := c.(engine.InvokeAction); ok && !ia.FireAndForget {
			return ia.CommandID
		}
	}
	t.Fatalf("no non-fire-and-forget InvokeAction in %#v", cmds)
	return ""
}

// TestStaleCommandFilterKeepsCompensationInvoke pins the highest-risk detail of
// the filter: startCompensationWalk CONSUMES the throw token before emitting a
// non-fire-and-forget InvokeAction correlated only by Compensating.ActiveCmdID.
// A filter built on the surviving token set alone would drop that command and
// hang every compensation walk.
func TestStaleCommandFilterKeepsCompensationInvoke(t *testing.T) {
	t.Parallel()

	// Start → charge → throw → afterThrow(UserTask) → end. The throw MUST have an
	// outgoing flow, or enter() auto-advances and no walk starts at all.
	def := compensableThenThrowDef(
		[]model.Node{
			event.NewCompensateThrow("throw"),
			activity.NewUserTask("afterThrow"),
			event.NewEnd("end"),
		},
		[]flow.SequenceFlow{
			{ID: "f-charge-throw", Source: "charge", Target: "throw"},
			{ID: "f-throw-after", Source: "throw", Target: "afterThrow"},
			{ID: "f-after-end", Source: "afterThrow", Target: "end"},
		},
	)

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(staleT0, nil), engine.StepOptions{})
	require.NoError(t, err)
	chargeCmd := firstInvokeCommandID(t, r1.Commands)

	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewActionCompleted(staleT0.Add(time.Second), chargeCmd, nil), engine.StepOptions{})
	require.NoError(t, err)

	// Control: the walk actually started. Without this the "is present" assertion
	// below could pass for a fixture that never reached the throw.
	require.NotEmpty(t, r2.State.Compensating.ActiveCmdID,
		"control: the compensation walk must have started")
	assert.Equal(t, engine.StatusCompensating, r2.State.Status)

	assert.True(t, invokeActionByID(r2.Commands, r2.State.Compensating.ActiveCmdID),
		"the compensation InvokeAction has no token awaiting it — only the cursor keeps it alive")
}

// TestStaleCommandFilterDropsCompensationInvokeWhenTerminal pins the mirror
// image: a step that starts a walk and then force-terminates must not leave a
// compensation action alive for a terminated instance.
//
// The assertion itself is unchanged; its POSITIVE CONTROL had to move.
// It used to read the id back from r2.State.Compensating.ActiveCmdID, which
// doubled as proof that a walk had started — endInstance now zeroes that cursor
// at the terminal site, so the control reads the throw node's History visit
// instead and the dropped command is identified by its action name. "refund-action"
// is emitted by nothing but the compensation walk in this fixture, so the two
// forms name the same command.
func TestStaleCommandFilterDropsCompensationInvokeWhenTerminal(t *testing.T) {
	t.Parallel()

	// Start → charge → fork ⇒ { f1: throw → end1 (declared FIRST) ; f2: halt }.
	// f1 first so the walk starts before forceTerminate halts drive.
	def := compensableThenThrowDef(
		[]model.Node{
			gateway.NewParallel("fork"),
			event.NewCompensateThrow("throw"),
			event.NewEnd("end1"),
			event.NewEnd("halt", event.WithForceTermination("kill", event.OutcomeAbort)),
		},
		[]flow.SequenceFlow{
			{ID: "f-charge-fork", Source: "charge", Target: "fork"},
			{ID: "f1", Source: "fork", Target: "throw"},
			{ID: "f2", Source: "fork", Target: "halt"},
			{ID: "f-throw-end", Source: "throw", Target: "end1"},
		},
	)

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(staleT0, nil), engine.StepOptions{})
	require.NoError(t, err)
	chargeCmd := firstInvokeCommandID(t, r1.Commands)

	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewActionCompleted(staleT0.Add(time.Second), chargeCmd, nil), engine.StepOptions{})
	require.NoError(t, err)

	// Two positive controls: the walk started AND the instance then went
	// terminal. Both are needed — the state under test is the combination.
	var reachedThrow bool
	for _, v := range r2.State.History {
		if v.NodeID == "throw" {
			reachedThrow = true
		}
	}
	require.True(t, reachedThrow,
		"control: the walk must have started before the terminate")
	require.True(t, r2.State.Status.IsTerminal(),
		"control: the sibling branch must have force-terminated the instance")

	assert.False(t, hasInvokeActionForName(r2.Commands, "refund-action"),
		"a terminated instance awaits nothing; the compensation action must not run")
}

// TestStaleCommandFilterDropsOnUnhandledError pins the destroyer that the
// force-terminate fixtures do NOT cover. handleUnhandledError's immediate-fail
// branch sets StatusFailed, stamps EndedAt and reconciles tasks and timers — but
// it never clears s.Tokens. Every sibling
// therefore survives with its AwaitCommand intact, so a live-awaiter set built
// from tokens alone re-admits it and the filter becomes a no-op on this path.
//
// A terminal instance can never consume a resumption trigger, so nothing it
// still holds is a live awaiter.
func TestStaleCommandFilterDropsOnUnhandledError(t *testing.T) {
	t.Parallel()

	// Start → fork ⇒ { f1: work=ServiceTask (parks, declared FIRST) ;
	//                  f2: boom = error end event with no matching boundary }
	def := &model.ProcessDefinition{
		ID: "p-stale-unhandled", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewParallel("fork"),
			activity.NewServiceTask("work", activity.WithTaskAction("charge")),
			event.NewEnd("boom", event.WithErrorCode("E9")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f-start", Source: "start", Target: "fork"},
			{ID: "f1", Source: "fork", Target: "work"},
			{ID: "f2", Source: "fork", Target: "boom"},
			{ID: "f-work-end", Source: "work", Target: "end"},
		},
	}

	res, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(staleT0, nil), engine.StepOptions{})
	require.NoError(t, err)

	// Control: the destroyer ran. Unlike forceTerminate this path leaves the
	// sibling token in place, so closeKindOf is not the control here — the
	// terminal status is.
	require.True(t, res.State.Status.IsTerminal(),
		"control: the unhandled error must have failed the instance")

	assert.False(t, hasInvokeAction(res.Commands, "charge"),
		"a failed instance awaits nothing; the action must not run for its orphaned sibling")
}

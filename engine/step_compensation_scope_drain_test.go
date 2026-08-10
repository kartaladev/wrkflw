package engine_test

// Two TestXxx functions live here rather than one table: the drain table below
// and TestCompensationWalkKeepsItsRecordsWhenAnErrorBoundaryTearsDownItsScope
// destroy the walk's scope by structurally different mechanisms (a sibling's
// normal end event vs. an error boundary on the enclosing sub-process), need
// different definitions, and stop at different points — the drain cases run to
// instance completion, the boundary case stops at a documented ADR-0171 bound.

import (
	"strings"
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
	"github.com/kartaladev/wrkflw/engine"
)

// scopeDrainT0 is the fixed base time for the sibling-drain fixtures. The engine
// core is clock-free, so every trigger below carries an explicit OccurredAt
// derived from it.
var scopeDrainT0 = time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)

// scopeWideDrainDef builds a process whose sub-process body runs `compensable`
// service tasks in order, then forks into a scope-wide compensation throw and a
// sibling user task. Both branches end at the sub-process's own end events, so
// completing the user task DRAINS the scope while the throw's compensation walk
// is still in flight (ADR-0171).
//
// The number of compensable tasks is the case dimension: two records make the
// walk advance to a second record after the drain, one record makes it finish
// immediately.
func scopeWideDrainDef(compensable []string) *model.ProcessDefinition {
	nodes := []model.Node{event.NewStart("bodyStart")}
	flows := []flow.SequenceFlow{}
	prev := "bodyStart"
	for _, name := range compensable {
		id := "svc" + name
		nodes = append(nodes, activity.NewServiceTask(id,
			activity.WithTaskAction("do"+name),
			activity.WithCompensateAction("undo"+name)))
		flows = append(flows, flow.SequenceFlow{ID: "bf" + name, Source: prev, Target: id})
		prev = id
	}
	nodes = append(nodes,
		gateway.NewParallel("bodyFork"),
		event.NewCompensateThrow("bodyThrow"),
		event.NewEnd("bodyEndThrow"),
		activity.NewUserTask("bodySibling"),
		event.NewEnd("bodyEndSibling"))
	flows = append(flows,
		flow.SequenceFlow{ID: "bfork", Source: prev, Target: "bodyFork"},
		flow.SequenceFlow{ID: "bthrow", Source: "bodyFork", Target: "bodyThrow"},
		flow.SequenceFlow{ID: "bthrowend", Source: "bodyThrow", Target: "bodyEndThrow"},
		flow.SequenceFlow{ID: "bsib", Source: "bodyFork", Target: "bodySibling"},
		flow.SequenceFlow{ID: "bsibend", Source: "bodySibling", Target: "bodyEndSibling"})

	body := &model.ProcessDefinition{ID: "drain-body", Version: 1, Nodes: nodes, Flows: flows}
	return &model.ProcessDefinition{
		ID: "scope-wide-drain", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("outer", body),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "outer"},
			{ID: "f2", Source: "outer", Target: "end"},
		},
	}
}

// targetedDrainDef builds the TARGETED-throw shape of the same race: the
// compensable work lives in a nested sub-process ("inner") whose records are
// archived on its normal exit, and the throw names it via CompensateRef. The
// walk's record source is therefore an archive entry (which no sibling can nil),
// but its RESUME SCOPE is still the outer sub-process scope the sibling drains.
func targetedDrainDef() *model.ProcessDefinition {
	inner := &model.ProcessDefinition{
		ID: "drain-inner", Version: 1,
		Nodes: []model.Node{
			event.NewStart("innerStart"),
			activity.NewServiceTask("svcA",
				activity.WithTaskAction("doA"), activity.WithCompensateAction("undoA")),
			event.NewEnd("innerEnd"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "i1", Source: "innerStart", Target: "svcA"},
			{ID: "i2", Source: "svcA", Target: "innerEnd"},
		},
	}
	body := &model.ProcessDefinition{
		ID: "drain-body-targeted", Version: 1,
		Nodes: []model.Node{
			event.NewStart("bodyStart"),
			activity.NewSubProcess("inner", inner),
			gateway.NewParallel("bodyFork"),
			event.NewCompensateThrow("bodyThrow", event.WithCompensateRef("inner")),
			event.NewEnd("bodyEndThrow"),
			activity.NewUserTask("bodySibling"),
			event.NewEnd("bodyEndSibling"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "b1", Source: "bodyStart", Target: "inner"},
			{ID: "b2", Source: "inner", Target: "bodyFork"},
			{ID: "b3", Source: "bodyFork", Target: "bodyThrow"},
			{ID: "b4", Source: "bodyThrow", Target: "bodyEndThrow"},
			{ID: "b5", Source: "bodyFork", Target: "bodySibling"},
			{ID: "b6", Source: "bodySibling", Target: "bodyEndSibling"},
		},
	}
	return &model.ProcessDefinition{
		ID: "targeted-drain", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("outer", body),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "outer"},
			{ID: "f2", Source: "outer", Target: "end"},
		},
	}
}

// compensationInvokeNames returns, in emission order, the Name of every
// InvokeAction in cmds whose name marks it as a compensation action in these
// fixtures (the "undo" prefix). Forward work uses the "do" prefix.
func compensationInvokeNames(cmds []engine.Command) []string {
	var out []string
	for _, c := range cmds {
		if ia, ok := c.(engine.InvokeAction); ok && strings.HasPrefix(ia.Name, "undo") {
			out = append(out, ia.Name)
		}
	}
	return out
}

// TestCompensationWalkSurvivesSiblingDrainingItsScope pins ADR-0171: a sibling
// branch reaching its scope's end event while a compensation throw walk is in
// flight must not destroy the walk.
//
// What makes each case fail before the fix (measured on
// fix/compensation-walk-and-mid-delivery-terminal at the ADR-0168/0169/0170
// bundle commit):
//   - "two records, walk advances after the drain": the scope's Compensations
//     were nil'd by archiveCompensations, and stepCompensationAdvance indexed the
//     resulting empty slice — `panic: runtime error: index out of range [0] with
//     length 0`.
//   - "one record, walk finishes after the drain" and "targeted throw, resume
//     scope drained": the walk finished and resumed into a scope closeScope had
//     already pruned — every Step returned
//     `workflow-engine: defForScope: unknown scope "…-s1"`, permanently.
func TestCompensationWalkSurvivesSiblingDrainingItsScope(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name       string
		def        *model.ProcessDefinition
		forward    []string // forward action names to complete, in order
		assert     func(t *testing.T, compensated []string, final engine.StepResult, err error)
		instanceID string
	}

	cases := []testCase{
		{
			name:       "two records, walk advances after the drain",
			instanceID: "drain2",
			def:        scopeWideDrainDef([]string{"A", "B"}),
			forward:    []string{"doA", "doB"},
			assert: func(t *testing.T, compensated []string, final engine.StepResult, err error) {
				require.NoError(t, err)
				assert.Equal(t, []string{"undoB", "undoA"}, compensated,
					"both records compensate, in reverse completion order")
				assert.Equal(t, engine.StatusCompleted, final.State.Status)
				assert.Empty(t, final.State.Tokens)
			},
		},
		{
			name:       "one record, walk finishes after the drain",
			instanceID: "drain1",
			def:        scopeWideDrainDef([]string{"A"}),
			forward:    []string{"doA"},
			assert: func(t *testing.T, compensated []string, final engine.StepResult, err error) {
				require.NoError(t, err)
				assert.Equal(t, []string{"undoA"}, compensated)
				assert.Equal(t, engine.StatusCompleted, final.State.Status)
				assert.Empty(t, final.State.Tokens)
			},
		},
		{
			name:       "targeted throw, resume scope drained",
			instanceID: "draint",
			def:        targetedDrainDef(),
			forward:    []string{"doA"},
			assert: func(t *testing.T, compensated []string, final engine.StepResult, err error) {
				require.NoError(t, err)
				assert.Equal(t, []string{"undoA"}, compensated)
				assert.Equal(t, engine.StatusCompleted, final.State.Status)
				assert.Empty(t, final.State.Tokens)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			at := scopeDrainT0
			next := func() time.Time {
				at = at.Add(time.Second)
				return at
			}

			res, err := engine.Step(ctx, tc.def, engine.InstanceState{InstanceID: tc.instanceID},
				engine.NewStartInstance(scopeDrainT0, nil), engine.StepOptions{})
			require.NoError(t, err)

			var compensated []string
			compensated = append(compensated, compensationInvokeNames(res.Commands)...)

			// Drive the forward path to the fork, which starts the walk and parks
			// the sibling on its user task.
			for _, action := range tc.forward {
				cmdID := firstInvokeCmd(res.Commands, action)
				require.NotEmpty(t, cmdID, "control: %q must have been invoked", action)
				res, err = engine.Step(ctx, tc.def, res.State,
					engine.NewActionCompleted(next(), cmdID, nil), engine.StepOptions{})
				require.NoError(t, err)
				compensated = append(compensated, compensationInvokeNames(res.Commands)...)
			}

			walkCmd := res.State.Compensating.ActiveCmdID
			require.NotEmpty(t, walkCmd, "control: the compensation walk must be in flight")
			human := firstAwaitHuman(res.Commands)
			require.NotNil(t, human, "control: the sibling must be parked on a user task")

			// The sibling completes and runs to its end event, draining the scope
			// while the walk is still outstanding.
			res, err = engine.Step(ctx, tc.def, res.State,
				engine.NewHumanCompleted(next(), human.TaskID, engine.CompletionInput{}, authz.Actor{ID: "u1"}),
				engine.StepOptions{})
			require.NoError(t, err, "the sibling's drain must not fail")
			require.Equal(t, engine.StatusCompensating, res.State.Status,
				"ADR-0168: the drain must not complete the instance over the outstanding walk")
			require.Equal(t, walkCmd, res.State.Compensating.ActiveCmdID,
				"the drain must leave the in-flight walk's command untouched")

			// Now drain the walk itself: each compensation action completes in turn.
			for res.State.Compensating.ActiveCmdID != "" {
				cmdID := res.State.Compensating.ActiveCmdID
				res, err = engine.Step(ctx, tc.def, res.State,
					engine.NewActionCompleted(next(), cmdID, nil), engine.StepOptions{})
				if err != nil {
					break
				}
				compensated = append(compensated, compensationInvokeNames(res.Commands)...)
			}

			tc.assert(t, compensated, res, err)
		})
	}
}

// errorBoundaryTeardownDef puts a compensable saga and a failing sibling inside
// a sub-process that carries an ERROR BOUNDARY. When the sibling fails with E1
// while the throw's walk is in flight, propagateError's enclosing-scope route
// cancels the scope subtree and closes the scope — a teardown that cannot be
// deferred the way a normal drain exit can, since the boundary must fire.
func errorBoundaryTeardownDef() *model.ProcessDefinition {
	body := &model.ProcessDefinition{
		ID: "boundary-teardown-body", Version: 1,
		Nodes: []model.Node{
			event.NewStart("bodyStart"),
			activity.NewServiceTask("svcA",
				activity.WithTaskAction("doA"), activity.WithCompensateAction("undoA")),
			activity.NewServiceTask("svcB",
				activity.WithTaskAction("doB"), activity.WithCompensateAction("undoB")),
			gateway.NewParallel("bodyFork"),
			event.NewCompensateThrow("bodyThrow"),
			event.NewEnd("bodyEndThrow"),
			activity.NewServiceTask("svcFails", activity.WithTaskAction("doFails")),
			event.NewEnd("bodyEndSibling"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "q1", Source: "bodyStart", Target: "svcA"},
			{ID: "q2", Source: "svcA", Target: "svcB"},
			{ID: "q3", Source: "svcB", Target: "bodyFork"},
			{ID: "q4", Source: "bodyFork", Target: "bodyThrow"},
			{ID: "q5", Source: "bodyThrow", Target: "bodyEndThrow"},
			{ID: "q6", Source: "bodyFork", Target: "svcFails"},
			{ID: "q7", Source: "svcFails", Target: "bodyEndSibling"},
		},
	}
	return &model.ProcessDefinition{
		ID: "boundary-teardown", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("outer", body),
			event.NewBoundary("bnd", "outer", event.WithBoundaryErrorCode("E1")),
			activity.NewServiceTask("recover", activity.WithTaskAction("doRecover")),
			event.NewEnd("end"),
			event.NewEnd("endRecover"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "outer"},
			{ID: "f2", Source: "outer", Target: "end"},
			{ID: "f3", Source: "bnd", Target: "recover"},
			{ID: "f4", Source: "recover", Target: "endRecover"},
		},
	}
}

// TestCompensationWalkKeepsItsRecordsWhenAnErrorBoundaryTearsDownItsScope is the
// case that makes ADR-0171's PIN load-bearing on its own. The scope-exit hold
// cannot help here: an error boundary must fire, so its teardown of the walk's
// scope cannot be deferred. Only the snapshot taken at walk start keeps the
// remaining record reachable.
//
// What makes the undoA assertion fail without the pin: cursorRecords falls back
// to the live read, the closed scope resolves to nil, and the bounds check then
// routes the walk straight to its finish — measured, undoA is NEVER invoked and
// the rollback silently loses a record. (Without the bounds check as well, the
// same state panicked with an index out of range.)
func TestCompensationWalkKeepsItsRecordsWhenAnErrorBoundaryTearsDownItsScope(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	def := errorBoundaryTeardownDef()

	res, err := engine.Step(ctx, def, engine.InstanceState{InstanceID: "bteardown"},
		engine.NewStartInstance(scopeDrainT0, nil), engine.StepOptions{})
	require.NoError(t, err)
	for i, action := range []string{"doA", "doB"} {
		cmdID := firstInvokeCmd(res.Commands, action)
		require.NotEmpty(t, cmdID, "control: %q must have been invoked", action)
		res, err = engine.Step(ctx, def, res.State,
			engine.NewActionCompleted(scopeDrainT0.Add(time.Duration(i+1)*time.Second), cmdID, nil),
			engine.StepOptions{})
		require.NoError(t, err)
	}

	walkCmd := res.State.Compensating.ActiveCmdID
	require.NotEmpty(t, walkCmd, "control: the scope-wide walk must be in flight")
	failCmd := firstInvokeCmd(res.Commands, "doFails")
	require.NotEmpty(t, failCmd, "control: the sibling must be parked on its action")
	require.Len(t, res.State.Scopes, 1, "control: the sub-process scope is open")

	// The sibling fails; the boundary on "outer" catches E1 and tears the scope
	// down underneath the live walk.
	res, err = engine.Step(ctx, def, res.State,
		engine.NewActionFailed(scopeDrainT0.Add(3*time.Second), failCmd, "E1", false),
		engine.StepOptions{})
	require.NoError(t, err)
	require.Empty(t, res.State.Scopes,
		"control: the boundary teardown closed the scope the walk was walking")
	require.Equal(t, walkCmd, res.State.Compensating.ActiveCmdID,
		"control: the walk cursor survived the teardown")

	// The pinned snapshot keeps the walk advancing over the records it committed
	// to, even though its live source no longer exists.
	res, err = engine.Step(ctx, def, res.State,
		engine.NewActionCompleted(scopeDrainT0.Add(4*time.Second), walkCmd, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"undoA"}, compensationInvokeNames(res.Commands),
		"the pinned records keep the second compensation reachable after the teardown")

	// The walk's finish then RECOVERS instead of stranding. This assertion
	// replaces the "KNOWN LIMITATION" this test used to pin — a
	// require.EqualError on `workflow-engine: defForScope: unknown scope
	// "bteardown-s1"`, which every subsequent Step also returned. ADR-0171's
	// hold cannot help here (a boundary must fire, so its teardown cannot be
	// deferred), so the recovery lives at the resume instead: the resume into
	// the destroyed scope is DROPPED, and the boundary's own target — already
	// running in the parent scope — carries the instance forward.
	res, err = engine.Step(ctx, def, res.State,
		engine.NewActionCompleted(scopeDrainT0.Add(5*time.Second), res.State.Compensating.ActiveCmdID, nil),
		engine.StepOptions{})
	require.NoError(t, err, "the finish must not strand on the scope the boundary destroyed")
	assert.Equal(t, engine.StatusRunning, res.State.Status,
		"the boundary's handler branch keeps the instance running")
	assert.Empty(t, res.State.Compensating.ActiveCmdID, "the walk is finished")
	require.Len(t, res.State.Tokens, 1,
		"only the boundary's own target may be live; tokens=%#v", res.State.Tokens)
	assert.Equal(t, "recover", res.State.Tokens[0].NodeID,
		"no token may be placed in the destroyed scope")
	assert.Empty(t, res.State.Tokens[0].ScopeID,
		"the surviving token belongs to the parent scope")
}

// interruptingESPTeardownDef puts a live compensation THROW walk inside a
// sub-process scope P and then destroys P from a route the ADR-0171 hold does
// not cover: a nested INTERRUPTING event sub-process declared in P.
//
//	root: start → outer(sub-process) → end
//	outer body:
//	  bodyStart → svcSaga(doA/undoA) → bodyFork ⇒
//	     { throwP(CompensateThrow) → resumeTask(UserTask) → bodyEnd1 ;
//	       bodySibling(UserTask) → bodyEnd2 }
//	  espP: SubProcess whose start is signal "boom", INTERRUPTING
//	        espStart → espTask(UserTask) → espEnd
//
// The interrupt cancels P's remaining tokens and opens a child scope for espP;
// when espP drains, exitNestedEventSubprocessScope closes P — the enclosing
// scope the walk still names as its ResumeScope. That call site consults no
// hold, which is the whole point of the fixture.
//
// throwP MUST have an outgoing flow or compensationThrowEventStrategy.enter
// auto-advances and no walk starts at all; resumeTask must be a user task so the
// resume is observable rather than immediately draining the scope again.
func interruptingESPTeardownDef() *model.ProcessDefinition {
	body := &model.ProcessDefinition{
		ID: "esp-teardown-body", Version: 1,
		Nodes: []model.Node{
			event.NewStart("bodyStart"),
			activity.NewServiceTask("svcSaga",
				activity.WithTaskAction("doA"), activity.WithCompensateAction("undoA")),
			gateway.NewParallel("bodyFork"),
			event.NewCompensateThrow("throwP"),
			activity.NewUserTask("resumeTask"),
			event.NewEnd("bodyEnd1"),
			activity.NewUserTask("bodySibling"),
			event.NewEnd("bodyEnd2"),
			activity.NewSubProcess("espP", &model.ProcessDefinition{
				ID: "esp-teardown-esp", Version: 1,
				Nodes: []model.Node{
					event.NewStart("espStart", event.WithSignalName("boom")),
					activity.NewUserTask("espTask"),
					event.NewEnd("espEnd"),
				},
				Flows: []flow.SequenceFlow{
					{ID: "x1", Source: "espStart", Target: "espTask"},
					{ID: "x2", Source: "espTask", Target: "espEnd"},
				},
			}),
		},
		Flows: []flow.SequenceFlow{
			{ID: "b1", Source: "bodyStart", Target: "svcSaga"},
			{ID: "b2", Source: "svcSaga", Target: "bodyFork"},
			{ID: "b3", Source: "bodyFork", Target: "throwP"},
			{ID: "b4", Source: "throwP", Target: "resumeTask"},
			{ID: "b5", Source: "resumeTask", Target: "bodyEnd1"},
			{ID: "b6", Source: "bodyFork", Target: "bodySibling"},
			{ID: "b7", Source: "bodySibling", Target: "bodyEnd2"},
		},
	}
	return &model.ProcessDefinition{
		ID: "p-esp-teardown", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("outer", body),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "outer"},
			{ID: "f2", Source: "outer", Target: "end"},
		},
	}
}

// TestCompensationWalkSurvivesNestedEventSubprocessTearingDownItsResumeScope is
// the F3 reproduction: a nested interrupting event sub-process closes the scope
// a live throw walk resumes into, through the one closeScope call site that
// consults no hold.
//
// What makes it fail without the recovery: applyFinish placed the resume token
// in the pruned scope, drive's first defForScope failed, and EVERY subsequent
// Step returned `workflow-engine: defForScope: unknown scope "esp-td-s1"` — the
// instance could no longer be completed, cancelled or terminated by any trigger.
func TestCompensationWalkSurvivesNestedEventSubprocessTearingDownItsResumeScope(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	def := interruptingESPTeardownDef()

	res, err := engine.Step(ctx, def, engine.InstanceState{InstanceID: "esp-td"},
		engine.NewStartInstance(scopeDrainT0, nil), engine.StepOptions{})
	require.NoError(t, err)
	doA := firstInvokeCmd(res.Commands, "doA")
	require.NotEmpty(t, doA, "control: svcSaga must park on doA")

	res, err = engine.Step(ctx, def, res.State,
		engine.NewActionCompleted(scopeDrainT0.Add(time.Second), doA, nil), engine.StepOptions{})
	require.NoError(t, err)
	walkCmd := res.State.Compensating.ActiveCmdID
	require.NotEmpty(t, walkCmd, "control: the scope-wide throw walk must be in flight")
	scopeP := res.State.Compensating.ResumeScope
	require.NotEmpty(t, scopeP, "control: the walk resumes into the sub-process scope")
	espTaskBefore := res.State.Tasks
	require.NotEmpty(t, espTaskBefore, "control: the sibling user task is open")

	// The interrupting event sub-process fires: it cancels P's tokens and opens
	// its own child scope inside P.
	res, err = engine.Step(ctx, def, res.State,
		engine.NewSignalReceived(scopeDrainT0.Add(2*time.Second), "boom", nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, walkCmd, res.State.Compensating.ActiveCmdID,
		"control: the interrupt must not disturb the live walk")
	espTask := humanTaskIDForNode(t, res.State, "espTask")

	// It drains — and exitNestedEventSubprocessScope closes P underneath the walk.
	res, err = engine.Step(ctx, def, res.State,
		engine.NewHumanCompleted(scopeDrainT0.Add(3*time.Second), espTask,
			engine.CompletionInput{}, authz.Actor{ID: "u1"}), engine.StepOptions{})
	require.NoError(t, err)
	require.Nil(t, scopeByID(res.State, scopeP),
		"control: the event-sub-process exit closed the walk's resume scope")
	require.Equal(t, walkCmd, res.State.Compensating.ActiveCmdID,
		"control: the walk cursor outlived its resume scope")

	// The walk finishes. Its resume target no longer exists, so the resume is
	// dropped — and because nothing else is left running, the instance reaches a
	// terminal state instead of wedging.
	res, err = engine.Step(ctx, def, res.State,
		engine.NewActionCompleted(scopeDrainT0.Add(4*time.Second), walkCmd, nil), engine.StepOptions{})
	require.NoError(t, err, "the finish must not fail on the pruned resume scope")
	assert.Equal(t, engine.StatusCompleted, res.State.Status,
		"an instance whose resume scope is gone must still reach a terminal state")
	assert.Empty(t, res.State.Tokens, "no token may be left naming a pruned scope")
	assert.Empty(t, res.State.Compensating.ActiveCmdID, "the walk is finished")
	assert.Equal(t, 1, countCompleteInstance(res.Commands),
		"exactly one terminal command; cmds=%#v", res.Commands)
}

// scopeByID finds a scope in a state snapshot by ID, or nil. The engine's own
// lookup is unexported, and this file is a black-box test.
func scopeByID(s engine.InstanceState, id string) *engine.Scope {
	for i := range s.Scopes {
		if s.Scopes[i].ID == id {
			return &s.Scopes[i]
		}
	}
	return nil
}

package engine_test

// step_harvest_live_sites_test.go — the two sites where the harvest runs
// while the instance is still alive and may yet hand itself to a compensation walk:
// handleUnhandledError and handleCancelRequested.
//
// These are the routes that carry the actual integrity harm. On `main` a compensable
// activity that completed inside a sub-process is invisible to both sites' records-exist
// predicates, because the record is still in the OPEN scope and those predicates read
// only RootCompensations + ArchivedCompensations. So the instance dies emitting no
// compensation action at all, and the record is unreachable afterwards: `undo-inner`
// never runs and never can.
//
// ⚠ Assertions here are on the PRESENCE of an InvokeAction named "undo-inner", never on
// a command count. The count is fixture-dependent — an audit lens measured cmds=2
// ([UpdateTask FailInstance]) on the same route with a human-task park, because the
// parked task is cancelled too. A count-based criterion would have been unreproducible.
//
// No `ctx` case modifier (table-test skill rule 3): engine.Step documents ctx as
// carrying no cancellation semantics — it is used only for trace-correlated logging and
// is never inspected for control flow.

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

// invokedActionNames returns the Name of every non-fire-and-forget InvokeAction in
// cmds, in emission order. Used instead of a command count so a row asserts what was
// actually dispatched rather than how many commands happened to accompany it.
func invokedActionNames(cmds []engine.Command) []string {
	var names []string
	for _, c := range cmds {
		if ia, ok := c.(engine.InvokeAction); ok && !ia.FireAndForget {
			names = append(names, ia.Name)
		}
	}
	return names
}

// liveSiteSubDef is
//
//	root: start → sub[ inner-start → inner-svc(compensable) → inner-hold → inner-end ] → end
//
// Driven two Steps in, the token is parked on inner-hold INSIDE the sub-process and
// the scope for `sub` is open holding inner-svc's compensation record. That is the
// state both killing triggers are delivered against.
func liveSiteSubDef() *model.ProcessDefinition {
	nested := &model.ProcessDefinition{
		ID: "p-livesite-nested", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			activity.NewServiceTask("inner-svc",
				activity.WithTaskAction("book-inner"),
				activity.WithCompensateAction("undo-inner"),
			),
			activity.NewServiceTask("inner-hold", activity.WithTaskAction("hold-inner")),
			event.NewEnd("inner-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "inner-svc"},
			{ID: "if2", Source: "inner-svc", Target: "inner-hold"},
			{ID: "if3", Source: "inner-hold", Target: "inner-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "p-livesite", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("sub", nested),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "sub"},
			{ID: "f2", Source: "sub", Target: "end"},
		},
	}
}

func TestDyingInstanceCompensatesOpenScopeRecords(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name string
		// kill builds the trigger that ends the instance while it is parked inside the
		// sub-process. holdCmd is the outstanding inner-hold command id, which the
		// unhandled-error row needs and the cancel row ignores.
		kill   func(at time.Time, holdCmd string) engine.Trigger
		assert func(t *testing.T, before engine.InstanceState, r engine.StepResult)
	}

	cases := []testCase{
		{
			// On main: cmds carries FailInstance and NO InvokeAction — the intended
			// walk is skipped because the predicate at step_errors.go:253
			// cannot see a record sitting in an open scope.
			name: "an_unhandled_error_compensates_the_open_scope_before_failing",
			kill: func(at time.Time, holdCmd string) engine.Trigger {
				return engine.NewActionFailed(at, holdCmd, "BOOM", false)
			},
			assert: func(t *testing.T, before engine.InstanceState, r engine.StepResult) {
				assert.Contains(t, invokedActionNames(r.Commands), "undo-inner",
					"the record completed inside the sub-process must be compensated before the instance dies")
				assert.Equal(t, engine.StatusCompensating, r.State.Status,
					"the instance must enter the walk rather than failing immediately")
			},
		},
		{
			// Same defect via the operator path: handleCancelRequested's predicate
			// at step_triggers.go:213 is the same shape.
			name: "an_operator_cancel_compensates_the_open_scope_before_terminating",
			kill: func(at time.Time, _ string) engine.Trigger {
				return engine.NewCancelRequested(at)
			},
			assert: func(t *testing.T, before engine.InstanceState, r engine.StepResult) {
				assert.Contains(t, invokedActionNames(r.Commands), "undo-inner",
					"a cancel must roll back completed compensable work inside the sub-process")
				assert.Equal(t, engine.StatusCompensating, r.State.Status,
					"the instance must enter the walk rather than terminating immediately")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			def := liveSiteSubDef()

			r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i-livesite"},
				engine.NewStartInstance(harvestT0, nil), engine.StepOptions{})
			require.NoError(t, err)

			svcCmd := firstPendingInvoke(r1.Commands)
			require.NotEmpty(t, svcCmd, "fixture: inner-svc must have been dispatched")

			r2, err := engine.Step(t.Context(), def, r1.State,
				engine.NewActionCompleted(harvestT0.Add(time.Second), svcCmd, nil), engine.StepOptions{})
			require.NoError(t, err)

			// The state every row is really about: alive, parked inside the sub-process,
			// with the record held in the OPEN scope and NOTHING in root or the archive.
			// Without these controls a row could pass for the wrong reason.
			require.Len(t, r2.State.Scopes, 1, "fixture: the sub-process scope must be open")
			require.Len(t, r2.State.Scopes[0].Compensations, 1,
				"fixture: the record must be in the OPEN scope, or this row tests nothing")
			require.Empty(t, r2.State.RootCompensations,
				"fixture: nothing at root, or the existing predicate would already have seen it")
			require.Empty(t, r2.State.ArchivedCompensations,
				"fixture: nothing archived, or the existing predicate would already have seen it")

			holdCmd := firstPendingInvoke(r2.Commands)
			require.NotEmpty(t, holdCmd, "fixture: inner-hold must have been dispatched")

			r3, err := engine.Step(t.Context(), def, r2.State,
				tc.kill(harvestT0.Add(2*time.Second), holdCmd), engine.StepOptions{})
			require.NoError(t, err)

			tc.assert(t, r2.State, r3)
		})
	}
}

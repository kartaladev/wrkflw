package runtime_test

// cancel_dropped_propagation_test.go — ADR-0180 decision 2, the half that nearly
// shipped a regression.
//
// engine.ErrCancelNotApplicable is a REPORTING outcome, not a propagation-halting
// error. Two independent sites would otherwise swallow the subtree: CancelInstance
// returns before the propagation block, and propagateCancel's child loop
// `continue`s past the recursion. Measured with the naive shape, a terminated
// parent kept a permanently RUNNING child and grandchild — strictly worse than the
// silent nil the sentinel replaces.
// See docs/specs/2026-08-13-engine-visibility-and-truthfulness.md §3b.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/internal/runtimetest"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// droppedCancelSagaDef is the definition whose PARTIAL rollback makes a cancel
// inapplicable: two compensable service tasks, then a receive task that parks.
//
//	start → a (undoA) → b (undoB) → wait (receive "go") → end
func droppedCancelSagaDef(id string) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: id, Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("a", activity.WithTaskAction("doA"), activity.WithCompensateAction("undoA")),
			activity.NewServiceTask("b", activity.WithTaskAction("doB"), activity.WithCompensateAction("undoB")),
			activity.NewReceiveTask("wait", "go"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "a"},
			{ID: "f2", Source: "a", Target: "b"},
			{ID: "f3", Source: "b", Target: "wait"},
			{ID: "f4", Source: "wait", Target: "end"},
		},
	}
}

// droppedCancelState builds, with the pure engine, an instance parked in an admin
// PARTIAL rollback whose compensation action is still out with a worker — the one
// state whose CancelRequested the engine drops.
func droppedCancelState(t *testing.T, def *model.ProcessDefinition, instanceID string) engine.InstanceState {
	t.Helper()

	at := time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC)
	res, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: instanceID},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	for i, name := range []string{"doA", "doB"} {
		var cmdID string
		for _, c := range res.Commands {
			if ia, ok := c.(engine.InvokeAction); ok && ia.Name == name {
				cmdID = ia.CommandID
			}
		}
		require.NotEmpty(t, cmdID, "fixture: %q must be dispatched", name)
		res, err = engine.Step(t.Context(), def, res.State,
			engine.NewActionCompleted(at.Add(time.Duration(i+1)*time.Second), cmdID, nil), engine.StepOptions{})
		require.NoError(t, err)
	}
	res, err = engine.Step(t.Context(), def, res.State,
		engine.NewCompensateRequested(at.Add(time.Minute), "a"), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompensating, res.State.Status)
	require.NotEmpty(t, res.State.Compensating.ToNode, "fixture: the walk must be a PARTIAL rollback")
	require.NotEmpty(t, res.State.Compensating.ActiveCmdID, "fixture: the walk must be in flight")

	// The precondition the whole file rests on: this state's cancel is dropped.
	_, stepErr := engine.Step(t.Context(), def, res.State,
		engine.NewCancelRequested(at.Add(2*time.Minute)), engine.StepOptions{})
	require.ErrorIs(t, stepErr, engine.ErrCancelNotApplicable,
		"fixture: the seeded state's own cancel must be the DROPPED one, or these tests prove nothing")

	return res.State
}

// runningChildState builds a child instance parked at a user task, whose cancel
// is an ordinary terminating one.
func runningChildState(t *testing.T, def *model.ProcessDefinition, instanceID string) engine.InstanceState {
	t.Helper()

	res, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: instanceID},
		engine.NewStartInstance(time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC), nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, res.State.Status, "fixture: the child must park, not complete")
	return res.State
}

// seedInstance persists st, optionally recording a parent↔child call link in the
// same Create — the exported seam (AppliedStep.NewCallLink) the driver itself uses.
func seedInstance(t *testing.T, store *kernel.MemInstanceStore, st engine.InstanceState, link *kernel.CallLink) {
	t.Helper()

	_, err := store.Create(context.Background(), kernel.AppliedStep{
		State:       st,
		Trigger:     engine.NewStartInstance(time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC), nil),
		NewCallLink: link,
	})
	require.NoError(t, err)
}

// droppedCancelDriver wires a driver with call links, definitions and human tasks —
// everything propagateCancel needs to resolve and terminate a child.
func droppedCancelDriver(t *testing.T, store *kernel.MemInstanceStore, cl *kernel.MemCallLinkStore, defs ...*model.ProcessDefinition) *runtime.ProcessDriver {
	t.Helper()

	return runtimetest.MustProcessDriver(t, nil, store,
		runtime.WithCallLinkStore(cl),
		runtime.WithDefinitions(kernel.NewMapDefinitionRegistry(defs...)),
		runtime.WithHumanTasks(humantask.NewStaticActorResolver(map[string][]authz.Actor{}), humantask.NewMemTaskStore(), nil),
	)
}

// TestDroppedCancelStillPropagates is ADR-0180's anti-regression pair: the
// sentinel must not cost the subtree its cancellation. Row (a) attacks the early
// return in CancelInstance, row (b) the `continue` in propagateCancel's child
// loop — two independent sites, so one row cannot cover both.
func TestDroppedCancelStillPropagates(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name  string
		setup func(t *testing.T) (driver *runtime.ProcessDriver, store *kernel.MemInstanceStore, rootDef *model.ProcessDefinition, rootID string)
		// assert receives the CancelInstance answer plus a loader for any seeded id.
		assert func(t *testing.T, err error, load func(id string) engine.InstanceState)
	}

	cases := []testCase{
		{
			name: "a parent whose OWN cancel is dropped still terminates its child",
			setup: func(t *testing.T) (*runtime.ProcessDriver, *kernel.MemInstanceStore, *model.ProcessDefinition, string) {
				parentDef := droppedCancelSagaDef("dcp-a-parent")
				childDef := cancelPropChildDef("dcp-a-child")

				cl := kernel.NewMemCallLinkStore()
				store := runtimetest.MustMemStore(t, kernel.WithCallLinks(cl))

				seedInstance(t, store, droppedCancelState(t, parentDef, "dcp-a-p1"), nil)
				seedInstance(t, store, runningChildState(t, childDef, "dcp-a-c1"), &kernel.CallLink{
					ChildInstanceID:  "dcp-a-c1",
					ParentInstanceID: "dcp-a-p1",
					ParentCommandID:  "dcp-a-cmd1",
					ParentDefID:      parentDef.ID,
					ParentDefVersion: parentDef.Version,
					Depth:            1,
				})

				return droppedCancelDriver(t, store, cl, parentDef, childDef), store, parentDef, "dcp-a-p1"
			},
			assert: func(t *testing.T, err error, load func(string) engine.InstanceState) {
				require.ErrorIs(t, err, engine.ErrCancelNotApplicable,
					"the parent's dropped cancel must still be REPORTED after propagation")
				assert.Equal(t, engine.StatusTerminated, load("dcp-a-c1").Status,
					"the child must still be terminated — the sentinel reports, it does not halt")
			},
		},
		{
			name: "a child whose cancel is dropped does not orphan its grandchild",
			setup: func(t *testing.T) (*runtime.ProcessDriver, *kernel.MemInstanceStore, *model.ProcessDefinition, string) {
				parentDef := cancelPropChildDef("dcp-b-parent")
				childDef := droppedCancelSagaDef("dcp-b-child")
				grandchildDef := cancelPropChildDef("dcp-b-grandchild")

				cl := kernel.NewMemCallLinkStore()
				store := runtimetest.MustMemStore(t, kernel.WithCallLinks(cl))

				seedInstance(t, store, runningChildState(t, parentDef, "dcp-b-p1"), nil)
				seedInstance(t, store, droppedCancelState(t, childDef, "dcp-b-c1"), &kernel.CallLink{
					ChildInstanceID:  "dcp-b-c1",
					ParentInstanceID: "dcp-b-p1",
					ParentCommandID:  "dcp-b-cmd1",
					ParentDefID:      parentDef.ID,
					ParentDefVersion: parentDef.Version,
					Depth:            1,
				})
				seedInstance(t, store, runningChildState(t, grandchildDef, "dcp-b-g1"), &kernel.CallLink{
					ChildInstanceID:  "dcp-b-g1",
					ParentInstanceID: "dcp-b-c1",
					ParentCommandID:  "dcp-b-cmd2",
					ParentDefID:      childDef.ID,
					ParentDefVersion: childDef.Version,
					Depth:            2,
				})

				return droppedCancelDriver(t, store, cl, parentDef, childDef, grandchildDef), store, parentDef, "dcp-b-p1"
			},
			assert: func(t *testing.T, err error, load func(string) engine.InstanceState) {
				require.NoError(t, err, "the ROOT cancel here is an ordinary one — only the child's is dropped")
				assert.Equal(t, engine.StatusTerminated, load("dcp-b-p1").Status, "the parent must terminate")
				assert.Equal(t, engine.StatusCompensating, load("dcp-b-c1").Status,
					"the child's own cancel really is dropped — that is the premise, not a failure")
				assert.Equal(t, engine.StatusTerminated, load("dcp-b-g1").Status,
					"the grandchild must be terminated: the loop must recurse THROUGH a dropped child")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			driver, store, rootDef, rootID := tc.setup(t)

			_, err := driver.CancelInstance(ctx, rootDef, rootID)

			tc.assert(t, err, func(id string) engine.InstanceState {
				st, _, loadErr := store.Load(ctx, id)
				require.NoError(t, loadErr)
				return st
			})
		})
	}
}

// TestDroppedCancelSentinelSurvivesTheDriver pins the classification callers key
// on: CancelInstance's answer must still satisfy errors.Is for BOTH the engine
// sentinel and ErrInvalidTransition, so the service layer and the HTTP
// classifier see a conflict rather than an opaque 500.
func TestDroppedCancelSentinelSurvivesTheDriver(t *testing.T) {
	t.Parallel()

	def := droppedCancelSagaDef("dcp-c-def")
	store := runtimetest.MustMemStore(t)
	seedInstance(t, store, droppedCancelState(t, def, "dcp-c1"), nil)

	driver := runtimetest.MustProcessDriver(t, nil, store,
		runtime.WithDefinitions(kernel.NewMapDefinitionRegistry(def)))

	st, err := driver.CancelInstance(t.Context(), def, "dcp-c1")
	require.ErrorIs(t, err, engine.ErrCancelNotApplicable)
	assert.True(t, errors.Is(err, engine.ErrInvalidTransition),
		"the wrapped classification must survive the driver's error wrapping")
	assert.Equal(t, engine.StatusCompensating, st.Status,
		"the returned state is the untouched one — nothing happened, and now the caller knows")
}

package engine_test

import (
	"context"
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
)

// inclusiveJoinInnerDef is the nested definition both fixtures below run inside a
// sub-process: one service task between a start and an end.
func inclusiveJoinInnerDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "or-inner", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			activity.NewServiceTask("inner-svc", activity.WithTaskAction("inner-action")),
			event.NewEnd("inner-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "i1", Source: "inner-start", Target: "inner-svc"},
			{ID: "i2", Source: "inner-svc", Target: "inner-end"},
		},
	}
}

// inclusiveJoinOverSubProcessDef puts one branch of an OR-split inside a
// sub-process and takes both branches back into an OR-join.
//
//	start → orsplit (inclusive, 1 in / 2 out)
//	  g1: orsplit → sub    (sub-process running or-inner)
//	  g2: orsplit → svcB   (service task "b")
//	  g3: sub  → orjoin ; g4: svcB → orjoin   (orjoin = inclusive, 2 in / 1 out)
//	  g5: orjoin → end
//
// While the sub-process runs, its token lives in a CHILD scope and carries a
// NodeID from the nested definition ("inner-svc"), which is not a node of the
// outer definition at all. An OR-join that asks "can any token in MY scope still
// reach me?" cannot see it, and fires with the sub-process branch outstanding.
func inclusiveJoinOverSubProcessDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "or-join-subprocess", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewInclusive("orsplit"),
			activity.NewSubProcess("sub", inclusiveJoinInnerDef()),
			activity.NewServiceTask("svcB", activity.WithTaskAction("b")),
			gateway.NewInclusive("orjoin"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "g0", Source: "start", Target: "orsplit"},
			{ID: "g1", Source: "orsplit", Target: "sub"},
			{ID: "g2", Source: "orsplit", Target: "svcB"},
			{ID: "g3", Source: "sub", Target: "orjoin"},
			{ID: "g4", Source: "svcB", Target: "orjoin"},
			{ID: "g5", Source: "orjoin", Target: "end"},
		},
	}
}

// inclusiveJoinBesideSubProcessDef is the control for the fix's other direction:
// a sub-process running in a child scope that CANNOT reach the join.
//
//	start → orsplit (inclusive, 1 in / 3 out)
//	  h1: orsplit → svcA     ; h4: svcA → orjoin
//	  h2: orsplit → svcB     ; h5: svcB → orjoin
//	  h3: orsplit → sideSub  ; h6: sideSub → side-end   ← never reaches orjoin
//	  orjoin (inclusive, 2 in / 1 out) → h7 → end
//
// Once svcA and svcB have both arrived, the OR-join must FIRE even though a
// descendant scope is still holding a token. "Some child scope is busy" is not
// the question; "can the node that owns that child scope still reach me" is. A
// fix that waits on the first is indistinguishable from a correct one on
// inclusiveJoinOverSubProcessDef alone, and wrong here.
func inclusiveJoinBesideSubProcessDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "or-join-beside-subprocess", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewInclusive("orsplit"),
			activity.NewServiceTask("svcA", activity.WithTaskAction("a")),
			activity.NewServiceTask("svcB", activity.WithTaskAction("b")),
			activity.NewSubProcess("sideSub", inclusiveJoinInnerDef()),
			gateway.NewInclusive("orjoin"),
			event.NewEnd("end"),
			event.NewEnd("side-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "h0", Source: "start", Target: "orsplit"},
			{ID: "h1", Source: "orsplit", Target: "svcA"},
			{ID: "h2", Source: "orsplit", Target: "svcB"},
			{ID: "h3", Source: "orsplit", Target: "sideSub"},
			{ID: "h4", Source: "svcA", Target: "orjoin"},
			{ID: "h5", Source: "svcB", Target: "orjoin"},
			{ID: "h6", Source: "sideSub", Target: "side-end"},
			{ID: "h7", Source: "orjoin", Target: "end"},
		},
	}
}

// TestInclusiveJoinWaitsForSubProcessBranch pins that a converging inclusive
// gateway keeps waiting while a sibling branch is executing inside a sub-process,
// and that it does NOT wait for a sub-process that cannot reach it.
//
// Both directions live in one table on purpose. An OR-join that waits for every
// open child scope satisfies the "must not fire" row exactly as well as a correct
// one does, and is wrong on the "must fire" row; an OR-join that ignores child
// scopes entirely is the reverse. Neither row alone discriminates.
//
// Every row is driven by an ActionCompleted trigger rather than by branch
// declaration order. forkInclusive places tokens in definition order of outgoing
// flows and drive() picks firstActive() in slice order, so a fixture that relies
// on which branch auto-advances first is order-sensitive; evaluating the join on
// a resume trigger is not, because the sub-process branch is definitely inside
// its child scope by then.
func TestInclusiveJoinWaitsForSubProcessBranch(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)

	type testCase struct {
		name string
		def  func() *model.ProcessDefinition
		// joinID and wantIncoming guard the FIXTURE: a join with one incoming flow
		// is not a join, and "it did not fire" would then pass for the wrong reason.
		joinID       string
		wantIncoming int
		// ctx modifies the context handed to engine.Step; nil means identity. See
		// the note in TestParallelJoinCountsFlowsNotArrivals on why no row cancels.
		ctx    func(ctx context.Context) context.Context
		then   func(t *testing.T, ctx context.Context, def *model.ProcessDefinition, res engine.StepResult) (engine.StepResult, error)
		assert func(t *testing.T, res engine.StepResult, err error)
	}

	cases := []testCase{
		{
			name:         "join must not fire while a sibling branch is inside a sub-process",
			def:          inclusiveJoinOverSubProcessDef,
			joinID:       "orjoin",
			wantIncoming: 2,
			then: func(t *testing.T, ctx context.Context, def *model.ProcessDefinition, res engine.StepResult) (engine.StepResult, error) {
				// PRECONDITION, not the assertion: the fork must really have put one
				// token in the root scope and one inside the sub-process's child scope.
				require.Len(t, res.State.Scopes, 1, "the sub-process must have opened a child scope")
				require.Len(t, res.State.Tokens, 2)
				require.Len(t, tokensAt(res.State, "svcB"), 1, "branch B parks in the root scope")

				b := invokedAction(t, res.Commands, "b")
				return engine.Step(ctx, def, res.State,
					engine.NewActionCompleted(at.Add(time.Second), b.CommandID, nil), engine.StepOptions{})
			},
			assert: func(t *testing.T, res engine.StepResult, err error) {
				require.NoError(t, err)

				joined := tokensAt(res.State, "orjoin")
				require.Len(t, joined, 1, "branch B's token must be parked at the join")
				assert.Equal(t, engine.TokenJoining, joined[0].State)

				require.Len(t, res.State.Scopes, 1, "the sub-process is still running")
				assert.Positive(t, len(res.State.Tokens)-len(joined),
					"the sub-process branch must still hold a token in its child scope")
				assert.False(t, visited(res.State, "end"),
					"the sub-process branch has not arrived: the join must not have fired")
				assert.Equal(t, engine.StatusRunning, res.State.Status)
			},
		},
		{
			name:         "join fires exactly once, consuming both branches, after the sub-process drains",
			def:          inclusiveJoinOverSubProcessDef,
			joinID:       "orjoin",
			wantIncoming: 2,
			then: func(t *testing.T, ctx context.Context, def *model.ProcessDefinition, res engine.StepResult) (engine.StepResult, error) {
				b := invokedAction(t, res.Commands, "b")
				inner := invokedAction(t, res.Commands, "inner-action")

				r1, err := engine.Step(ctx, def, res.State,
					engine.NewActionCompleted(at.Add(time.Second), b.CommandID, nil), engine.StepOptions{})
				if err != nil {
					return r1, err
				}
				// The sub-process now drains: its end event closes the child scope
				// and resumeInParentScope delivers the second token to the join.
				return engine.Step(ctx, def, r1.State,
					engine.NewActionCompleted(at.Add(2*time.Second), inner.CommandID, nil), engine.StepOptions{})
			},
			assert: func(t *testing.T, res engine.StepResult, err error) {
				require.NoError(t, err)

				ends := visitsOf(res.State, "end")
				require.Len(t, ends, 1, "the join must fire EXACTLY once: a second firing would visit end twice")
				assert.Equal(t, at.Add(2*time.Second), ends[0].EnteredAt,
					"the join fires on the step that delivered the sub-process branch, not before")

				assert.Empty(t, tokensAt(res.State, "orjoin"), "both branch tokens are consumed by the firing")
				assert.Equal(t, engine.StatusCompleted, res.State.Status)
				assert.Empty(t, res.State.Tokens)
			},
		},
		{
			name:         "join still fires when the open child scope cannot reach it",
			def:          inclusiveJoinBesideSubProcessDef,
			joinID:       "orjoin",
			wantIncoming: 2,
			then: func(t *testing.T, ctx context.Context, def *model.ProcessDefinition, res engine.StepResult) (engine.StepResult, error) {
				require.Len(t, res.State.Scopes, 1, "sideSub must have opened a child scope")

				a := invokedAction(t, res.Commands, "a")
				b := invokedAction(t, res.Commands, "b")
				r1, err := engine.Step(ctx, def, res.State,
					engine.NewActionCompleted(at.Add(time.Second), a.CommandID, nil), engine.StepOptions{})
				if err != nil {
					return r1, err
				}
				return engine.Step(ctx, def, r1.State,
					engine.NewActionCompleted(at.Add(2*time.Second), b.CommandID, nil), engine.StepOptions{})
			},
			assert: func(t *testing.T, res engine.StepResult, err error) {
				require.NoError(t, err)

				// sideSub is still running in its own scope, and must not hold the
				// join up: no path from sideSub reaches orjoin.
				require.Len(t, res.State.Scopes, 1, "sideSub is still running")
				assert.True(t, visited(res.State, "end"),
					"both reaching branches arrived; an unreachable child scope must not block the join")
				assert.Empty(t, tokensAt(res.State, "orjoin"), "both branch tokens are consumed")
				assert.Equal(t, engine.StatusRunning, res.State.Status,
					"the side sub-process is still running, so the instance is not complete")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			if tc.ctx != nil {
				ctx = tc.ctx(ctx)
			}

			def := tc.def()
			require.NoError(t, model.Validate(def),
				"the fixture must be a definition the validator accepts, or the finding is about the validator")
			require.Len(t, def.Incoming(tc.joinID), tc.wantIncoming, "fixture shape guard")

			res, err := engine.Step(ctx, def, engine.InstanceState{InstanceID: "i1"},
				engine.NewStartInstance(at, nil), engine.StepOptions{})
			if tc.then != nil && err == nil {
				res, err = tc.then(t, ctx, def, res)
			}
			tc.assert(t, res, err)
		})
	}
}

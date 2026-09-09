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

// inclusiveJoinBypassedBySubProcessDef is the shape whose join wait is discharged
// by a SCOPE CLOSING rather than by a token arriving.
//
//	start → orsplit (inclusive, 1 in / 2 out)
//	  g1: orsplit → sub    (sub-process running or-inner)
//	  g2: orsplit → svcB   (service task "b")
//	  g3: sub → xg         (exclusive gateway)
//	    g6: xg → orjoin      (condition, FALSE)
//	    g7: xg → bypass-end  (default, TAKEN)
//	  g4: svcB → orjoin
//	  g5: orjoin → end     (orjoin = inclusive, 2 in / 1 out)
//
// `sub` CAN reach `orjoin` — via g3/g6 — so the join correctly waits while the
// sub-process runs. But the sub-process's branch then diverges away down g7 and
// its scope closes, and nothing on that path ever enters `orjoin`. The condition
// that was holding the join open is discharged by an event with no token entry
// behind it.
//
// This is the direction the shipped control row does not pin. That row covers a
// child scope whose owning node CANNOT reach the join; this one covers a child
// scope whose owning node CAN reach it and then does not.
func inclusiveJoinBypassedBySubProcessDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "or-join-bypassed", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewInclusive("orsplit"),
			activity.NewSubProcess("sub", inclusiveJoinInnerDef()),
			activity.NewServiceTask("svcB", activity.WithTaskAction("b")),
			gateway.NewExclusive("xg"),
			gateway.NewInclusive("orjoin"),
			event.NewEnd("end"),
			event.NewEnd("bypass-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "g0", Source: "start", Target: "orsplit"},
			{ID: "g1", Source: "orsplit", Target: "sub"},
			{ID: "g2", Source: "orsplit", Target: "svcB"},
			{ID: "g3", Source: "sub", Target: "xg"},
			{ID: "g6", Source: "xg", Target: "orjoin", Condition: "rejoin > 0"},
			{ID: "g7", Source: "xg", Target: "bypass-end", IsDefault: true},
			{ID: "g4", Source: "svcB", Target: "orjoin"},
			{ID: "g5", Source: "orjoin", Target: "end"},
		},
	}
}

// parallelJoinWithIdleBranchDef gives a converging parallel gateway two incoming
// flows plus a third, independent branch that never touches it.
//
//	start → fork (parallel, 1 in / 3 out)
//	  q1: fork → svcA ; q4: svcA → join
//	  q2: fork → svcB ; q5: svcB → join
//	  q3: fork → svcZ ; q6: svcZ → z-end   ← never reaches the join
//	  join (parallel, 2 in / 1 out) → q7 → end
//
// The idle branch is what lets a trigger advance the instance WITHOUT any token
// entering the join, which is the only way to observe whether join readiness is
// re-evaluated when the token set settles.
func parallelJoinWithIdleBranchDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "join-idle-branch", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewParallel("fork"),
			activity.NewServiceTask("svcA", activity.WithTaskAction("a")),
			activity.NewServiceTask("svcB", activity.WithTaskAction("b")),
			activity.NewServiceTask("svcZ", activity.WithTaskAction("z")),
			gateway.NewParallel("join"),
			event.NewEnd("end"),
			event.NewEnd("z-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "q0", Source: "start", Target: "fork"},
			{ID: "q1", Source: "fork", Target: "svcA"},
			{ID: "q2", Source: "fork", Target: "svcB"},
			{ID: "q3", Source: "fork", Target: "svcZ"},
			{ID: "q4", Source: "svcA", Target: "join"},
			{ID: "q5", Source: "svcB", Target: "join"},
			{ID: "q6", Source: "svcZ", Target: "z-end"},
			{ID: "q7", Source: "join", Target: "end"},
		},
	}
}

// TestJoinReadinessIsReEvaluatedWhenTheTokenSetSettles pins the one property both
// H-1 and M-1 of the round-1 review are symptoms of: **join readiness must not be
// evaluated only when a token walks into the join.**
//
// A join's wait can be discharged by an event that carries no token entry with it
// — a child scope closing, or another firing leaving a complete match behind. If
// nothing re-checks, the instance stalls forever with no incident and nothing
// distinguishing the stuck token from one that is legitimately waiting. A silent
// unrecoverable stall is worse than the early fire it replaced, which is why the
// first row here is a REGRESSION guard, not a new capability.
//
// Both rows drive `engine.Step` and both end with the token set settling on a
// state that only a re-evaluation can move.
func TestJoinReadinessIsReEvaluatedWhenTheTokenSetSettles(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)

	type testCase struct {
		name  string
		def   func() *model.ProcessDefinition
		state func(def *model.ProcessDefinition) engine.InstanceState
		// trig is the trigger fed to the SUT. For the scope-closing row it is
		// produced by `then`, which runs the earlier steps first.
		then   func(t *testing.T, ctx context.Context, def *model.ProcessDefinition, res engine.StepResult) (engine.StepResult, error)
		assert func(t *testing.T, res engine.StepResult, err error)
	}

	cases := []testCase{
		{
			name: "REGRESSION: a join wait discharged by a scope closing must not strand the instance",
			def:  inclusiveJoinBypassedBySubProcessDef,
			then: func(t *testing.T, ctx context.Context, def *model.ProcessDefinition, res engine.StepResult) (engine.StepResult, error) {
				b := invokedAction(t, res.Commands, "b")
				inner := invokedAction(t, res.Commands, "inner-action")

				// Branch B arrives and parks: the join waits, correctly, because
				// `sub` can still reach it.
				r1, err := engine.Step(ctx, def, res.State,
					engine.NewActionCompleted(at.Add(time.Second), b.CommandID, nil), engine.StepOptions{})
				if err != nil {
					return r1, err
				}
				require.Len(t, tokensAt(r1.State, "orjoin"), 1, "precondition: the join is waiting on the sub-process branch")

				// The sub-process finishes and its branch takes g7 to bypass-end.
				// Its scope closes. Nothing enters the join on this path.
				return engine.Step(ctx, def, r1.State,
					engine.NewActionCompleted(at.Add(2*time.Second), inner.CommandID, nil), engine.StepOptions{})
			},
			assert: func(t *testing.T, res engine.StepResult, err error) {
				require.NoError(t, err)
				assert.True(t, visited(res.State, "bypass-end"), "precondition: the sub-process branch bypassed the join")
				assert.Empty(t, res.State.Scopes, "the sub-process scope has closed")

				assert.True(t, visited(res.State, "end"),
					"nothing can reach the join any more, so it must fire rather than stall")
				assert.Empty(t, tokensAt(res.State, "orjoin"))
				assert.Equal(t, engine.StatusCompleted, res.State.Status,
					"this shape completed correctly before the sub-process wait existed; it must still complete")
			},
		},
		{
			name: "a settled complete match at a join fires without a token entering it",
			def:  parallelJoinWithIdleBranchDef,
			// A settled COMPLETE match is not reachable by parallel-join arithmetic
			// alone: the join fires the instant coverage completes, and a firing
			// drains exactly one token per flow, so it can never leave one behind
			// on every flow. It IS reachable from a decoded snapshot — an instance
			// that was mid-join when this binary replaced one without the
			// provenance field, whose parked tokens carry no provenance and are
			// matched by the empty-provenance pass. Without re-evaluation those two
			// tokens are inert: they satisfy every incoming flow, never fire, and
			// hold the instance short of completion because exitRootScope counts
			// them.
			state: func(_ *model.ProcessDefinition) engine.InstanceState {
				return engine.InstanceState{
					InstanceID: "i1", Status: engine.StatusRunning,
					TokenSeq: 3, CmdSeq: 1,
					Tokens: []engine.Token{
						{ID: "t-a", NodeID: "join", State: engine.TokenJoining, EnteredAt: at},
						{ID: "t-b", NodeID: "join", State: engine.TokenJoining, EnteredAt: at},
						{ID: "t-z", NodeID: "svcZ", State: engine.TokenWaiting, AwaitCommand: "i1-c1", EnteredAt: at},
					},
					History: []engine.NodeVisit{
						{NodeID: "join", TokenID: "t-a", EnteredAt: at},
						{NodeID: "join", TokenID: "t-b", EnteredAt: at},
						{NodeID: "svcZ", TokenID: "t-z", EnteredAt: at},
					},
				}
			},
			assert: func(t *testing.T, res engine.StepResult, err error) {
				require.NoError(t, err)
				// The trigger advances only the idle branch, which ends at z-end.
				assert.True(t, visited(res.State, "z-end"), "precondition: the idle branch advanced")
				assert.True(t, visited(res.State, "end"),
					"the parked tokens cover every incoming flow: the join must fire even though nothing entered it")
				assert.Empty(t, res.State.Tokens)
				assert.Equal(t, engine.StatusCompleted, res.State.Status)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			def := tc.def()
			require.NoError(t, model.Validate(def),
				"the fixture must be a definition the validator accepts")

			var res engine.StepResult
			var err error
			if tc.state != nil {
				res, err = engine.Step(ctx, def, tc.state(def),
					engine.NewActionCompleted(at.Add(time.Second), "i1-c1", nil), engine.StepOptions{})
			} else {
				res, err = engine.Step(ctx, def, engine.InstanceState{InstanceID: "i1"},
					engine.NewStartInstance(at, nil), engine.StepOptions{})
			}
			if tc.then != nil && err == nil {
				res, err = tc.then(t, ctx, def, res)
			}
			tc.assert(t, res, err)
		})
	}
}

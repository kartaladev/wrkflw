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

// implicitMergeIntoJoinDef is the shape that puts TWO tokens on ONE incoming
// flow of a converging parallel gateway, while the gateway's other incoming
// flow is never traversed at all.
//
//	start → pfork (parallel, 1 in / 3 out)
//	  f2: pfork → svcC   (service task "c" — parks; source of fj2)
//	  f3: pfork → xa     (exclusive gateway, pass-through)
//	  f4: pfork → xb     (exclusive gateway, pass-through)
//	  f5: xa → merge ; f6: xb → merge   (merge = exclusive gateway, 2 in / 1 out)
//	  fj1: merge → join    ← BOTH fork branches traverse THIS one edge
//	  fj2: svcC  → join    ← never traversed while "c" is outstanding
//	  f7:  join → end      (join = parallel gateway, 2 in / 1 out)
//
// The fork puts three tokens in flight. Two of them re-merge implicitly through
// the pass-through exclusive gateways and cross fj1 one after the other, so the
// join sees two arrivals — but only one of its two incoming sequence flows has
// been traversed. BPMN 2.0 §13.3.2 requires a converging parallel gateway to
// consume one token per incoming sequence flow, so the join must NOT fire here.
//
// model.Validate accepts this definition: ErrMixedGateway rejects only a gateway
// that is both >1-in and >1-out, and nothing constrains an exclusive gateway
// with several incoming flows.
func implicitMergeIntoJoinDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "implicit-merge-join", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewParallel("pfork"),
			activity.NewServiceTask("svcC", activity.WithTaskAction("c")),
			gateway.NewExclusive("xa"),
			gateway.NewExclusive("xb"),
			gateway.NewExclusive("merge"),
			gateway.NewParallel("join"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "pfork"},
			{ID: "f2", Source: "pfork", Target: "svcC"},
			{ID: "f3", Source: "pfork", Target: "xa"},
			{ID: "f4", Source: "pfork", Target: "xb"},
			{ID: "f5", Source: "xa", Target: "merge"},
			{ID: "f6", Source: "xb", Target: "merge"},
			{ID: "fj1", Source: "merge", Target: "join"},
			{ID: "fj2", Source: "svcC", Target: "join"},
			{ID: "f7", Source: "join", Target: "end"},
		},
	}
}

// loopBackIntoJoinDef is the shape issue #120 itself proposed as the way two
// tokens might end up on one incoming edge: a branch that loops back on itself
// while its sibling branch is parked.
//
//	start → fork (parallel, 1 in / 2 out)
//	  f2: fork → svcA  (service task "a" — parks)
//	  f3: fork → svcB  (service task "b" — parks)
//	  f4: svcA → join
//	  f5: svcB → retry (exclusive gateway, 1 in / 2 out)
//	  f6: retry → svcB   (conditional: loop back)
//	  f7: retry → join   (default: leave the loop)
//	  f8: join → end     (join = parallel gateway, 2 in / 1 out)
//
// This is the MEASURED NEGATIVE the issue asked for. Looping does NOT reproduce
// the defect: the branch holds exactly one token, so however many times it goes
// round, only one token is ever on f7 at a time. Two tokens on one incoming edge
// need a FORK that implicitly re-merges (implicitMergeIntoJoinDef), not a cycle.
// This row is green before the fix and must stay green after it.
func loopBackIntoJoinDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "loop-back-join", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewParallel("fork"),
			activity.NewServiceTask("svcA", activity.WithTaskAction("a")),
			activity.NewServiceTask("svcB", activity.WithTaskAction("b")),
			gateway.NewExclusive("retry"),
			gateway.NewParallel("join"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "fork"},
			{ID: "f2", Source: "fork", Target: "svcA"},
			{ID: "f3", Source: "fork", Target: "svcB"},
			{ID: "f4", Source: "svcA", Target: "join"},
			{ID: "f5", Source: "svcB", Target: "retry"},
			{ID: "f6", Source: "retry", Target: "svcB", Condition: "again > 0"},
			{ID: "f7", Source: "retry", Target: "join", IsDefault: true},
			{ID: "f8", Source: "join", Target: "end"},
		},
	}
}

// invokedAction returns the InvokeAction command with the given action name, or
// fails the test. It exists so the table's `then` closures can address a branch
// by name rather than by command index, which the engine does not promise.
func invokedAction(t *testing.T, cmds []engine.Command, name string) engine.InvokeAction {
	t.Helper()
	for _, c := range cmds {
		if ia, ok := c.(engine.InvokeAction); ok && ia.Name == name {
			return ia
		}
	}
	require.FailNowf(t, "no InvokeAction for the requested action", "action %q not found in %d commands", name, len(cmds))
	return engine.InvokeAction{}
}

// tokensAt returns the tokens currently sitting on nodeID in the root scope.
func tokensAt(st engine.InstanceState, nodeID string) []engine.Token {
	var out []engine.Token
	for _, tk := range st.Tokens {
		if tk.NodeID == nodeID && tk.ScopeID == "" {
			out = append(out, tk)
		}
	}
	return out
}

// visitsOf returns every NodeVisit ever opened for nodeID, in history order.
func visitsOf(st engine.InstanceState, nodeID string) []engine.NodeVisit {
	var out []engine.NodeVisit
	for _, v := range st.History {
		if v.NodeID == nodeID {
			out = append(out, v)
		}
	}
	return out
}

// visited reports whether any NodeVisit was ever opened for nodeID.
func visited(st engine.InstanceState, nodeID string) bool {
	return len(visitsOf(st, nodeID)) > 0
}

// TestParallelJoinCountsFlowsNotArrivals pins that a converging parallel gateway
// is satisfied by one token per INCOMING SEQUENCE FLOW, not by a count of
// arrivals. See BPMN 2.0 §13.3.2, and the `# BPMN 2.0` conformance table in
// doc.go, which lists parallelGateway under "Matches BPMN".
//
// Both directions are pinned by the same table on purpose: a join that refuses
// everything satisfies the "did not fire" rows as easily as a correct one, and a
// join that evaluates nothing satisfies the "fired" rows as easily as a correct
// one. Rows 1 and 3 are the refuse side, rows 2 and 4 the accept side.
//
// No `ctx` modifier case cancels the context: engine.Step is a pure function of
// (definition, state, trigger) — measured, `git grep 'ctx.Err()\|<-ctx.Done()'
// over engine/*.go is empty — and carries ctx only for structured logging and
// incident recording. A cancellation row would assert nothing the engine reads.
func TestParallelJoinCountsFlowsNotArrivals(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)

	type testCase struct {
		name string
		def  func() *model.ProcessDefinition
		// joinID and wantIncoming guard the FIXTURE, not the engine: a typo that
		// gives the join one incoming flow instead of two makes "the join did not
		// fire" pass for entirely the wrong reason.
		joinID       string
		wantIncoming int
		// ctx modifies the context handed to engine.Step; nil means identity.
		ctx func(ctx context.Context) context.Context
		// then runs any further triggers after StartInstance, returning the last
		// result. nil means the assertions are about the start step alone.
		then   func(t *testing.T, ctx context.Context, def *model.ProcessDefinition, res engine.StepResult) (engine.StepResult, error)
		assert func(t *testing.T, res engine.StepResult, err error)
	}

	cases := []testCase{
		{
			name:         "two tokens over one incoming flow do not satisfy the other flow",
			def:          implicitMergeIntoJoinDef,
			joinID:       "join",
			wantIncoming: 2,
			assert: func(t *testing.T, res engine.StepResult, err error) {
				require.NoError(t, err)
				assert.Equal(t, engine.StatusRunning, res.State.Status)

				// fj2's only source is still parked awaiting its action, so the
				// join cannot have been satisfied.
				parked := tokensAt(res.State, "svcC")
				require.Len(t, parked, 1, "svcC must still hold the token for fj2")
				assert.Equal(t, engine.TokenWaiting, parked[0].State)

				joined := tokensAt(res.State, "join")
				assert.Len(t, joined, 2, "both tokens that crossed fj1 must be parked at the join")
				for _, tk := range joined {
					assert.Equal(t, engine.TokenJoining, tk.State)
				}
				assert.False(t, visited(res.State, "end"),
					"the join fired with fj2 never traversed: end must not have been reached")
			},
		},
		{
			name:         "join fires once every incoming flow delivered, leaving the surplus parked",
			def:          implicitMergeIntoJoinDef,
			joinID:       "join",
			wantIncoming: 2,
			then: func(t *testing.T, ctx context.Context, def *model.ProcessDefinition, res engine.StepResult) (engine.StepResult, error) {
				// Completing "c" sends svcC's token across fj2. Now fj1 holds two
				// tokens and fj2 holds one: the join is satisfied and fires,
				// consuming exactly ONE token per incoming flow. The second fj1
				// token is surplus and stays parked.
				cmd := invokedAction(t, res.Commands, "c")
				return engine.Step(ctx, def, res.State,
					engine.NewActionCompleted(at.Add(time.Second), cmd.CommandID, nil), engine.StepOptions{})
			},
			assert: func(t *testing.T, res engine.StepResult, err error) {
				require.NoError(t, err)

				// WHEN end was entered is the load-bearing assertion, not THAT it
				// was. An arrival-counting join also leaves exactly one token
				// parked at the join after this step and has also reached end —
				// but it reached end on the START step, before fj2 was ever
				// traversed. Pinning the timestamp is what makes this row
				// discriminate instead of passing for the wrong reason.
				ends := visitsOf(res.State, "end")
				require.Len(t, ends, 1, "end must be reached exactly once")
				assert.Equal(t, at.Add(time.Second), ends[0].EnteredAt,
					"the join must fire on the step that delivered its LAST unsatisfied incoming flow (fj2), not at start")

				surplus := tokensAt(res.State, "join")
				require.Len(t, surplus, 1,
					"one token per incoming flow is consumed; the second fj1 token is surplus and stays parked")
				assert.Equal(t, engine.TokenJoining, surplus[0].State)
				assert.Equal(t, engine.StatusRunning, res.State.Status,
					"a token is still parked at the join, so the instance is not complete")
			},
		},
		{
			name:         "MEASURED NEGATIVE: a loop-back branch never puts two tokens on one edge",
			def:          loopBackIntoJoinDef,
			joinID:       "join",
			wantIncoming: 2,
			then: func(t *testing.T, ctx context.Context, def *model.ProcessDefinition, res engine.StepResult) (engine.StepResult, error) {
				// Go round the loop once: "b" completes with again > 0, so retry
				// takes f6 back to svcB and re-invokes it.
				b1 := invokedAction(t, res.Commands, "b")
				r1, err := engine.Step(ctx, def, res.State,
					engine.NewActionCompleted(at.Add(time.Second), b1.CommandID, map[string]any{"again": 1}),
					engine.StepOptions{})
				if err != nil {
					return r1, err
				}
				require.NotEmpty(t, r1.Commands, "the loop must re-invoke b")

				// Leave the loop: retry takes its default flow f7 into the join,
				// while svcA is still parked on f4's source.
				b2 := invokedAction(t, r1.Commands, "b")
				return engine.Step(ctx, def, r1.State,
					engine.NewActionCompleted(at.Add(2*time.Second), b2.CommandID, map[string]any{"again": 0}),
					engine.StepOptions{})
			},
			assert: func(t *testing.T, res engine.StepResult, err error) {
				require.NoError(t, err)
				// One token went round the loop and arrived over f7; f4 has not
				// been traversed. Arrival counting and flow counting AGREE here —
				// which is exactly why the issue's proposed shape does not
				// reproduce the defect.
				joined := tokensAt(res.State, "join")
				require.Len(t, joined, 1, "the looping branch contributes exactly one token")
				assert.Equal(t, engine.TokenJoining, joined[0].State)
				assert.False(t, visited(res.State, "end"), "svcA has not arrived; the join must wait")
				assert.Equal(t, engine.StatusRunning, res.State.Status)
			},
		},
		{
			name:         "plain diamond still fires when each incoming flow delivers one token",
			def:          diamondDef,
			joinID:       "join",
			wantIncoming: 2,
			then: func(t *testing.T, ctx context.Context, def *model.ProcessDefinition, res engine.StepResult) (engine.StepResult, error) {
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
				assert.Equal(t, engine.StatusCompleted, res.State.Status)
				assert.Empty(t, res.State.Tokens, "both tokens consumed, one per incoming flow")
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

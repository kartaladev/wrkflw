package engine_test

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
)

// No ctx modifier is declared on the table below: engine.Step does not observe
// cancellation or deadlines. Measured at this tree — the engine package's
// non-test sources contain no ctx.Err() or ctx.Done() call at all, so a
// cancelled-context row could only assert that nothing changes.

// Two tables live here rather than one: they vary different things over
// structurally different fixtures — the first varies the flow IDs of a
// well-formed definition, the second varies whether the boundary's outgoing
// flow names a node that exists. Neither ctx modifier is declared, for the
// reason above.

// boundaryDecoyFlowDef returns a definition whose boundary event's outgoing flow
// carries flowID, and which declares an UNRELATED flow carrying decoyID EARLIER
// in the Flows slice:
//
//	start → fork ⇉ work (UserTask, message boundary "bnd") → endWork
//	             ⇉ decoySource (UserTask) → decoyTarget (action "decoy-action") → endDecoy
//	                        bnd → realTarget (action "real-action") → endReal
//
// The decoy edge is declared at index 0 and the boundary's own edge last, so a
// fire path that resolves the boundary's outgoing flow by ID alone takes the
// FIRST flow carrying that ID — the decoy — whenever the ID is not unique.
// A blank ID is exactly that case: definition/model's duplicate-flow-ID check
// skips blank IDs, so two blank-ID flows are a valid definition.
func boundaryDecoyFlowDef(decoyID, flowID string) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-bnd-decoy", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewParallel("fork"),
			activity.NewUserTask("work"),
			event.NewBoundary("bnd", "work", event.WithMessageCorrelator("cancel", "")),
			activity.NewUserTask("decoySource"),
			activity.NewServiceTask("decoyTarget", activity.WithTaskAction("decoy-action")),
			activity.NewServiceTask("realTarget", activity.WithTaskAction("real-action")),
			event.NewEnd("endWork"),
			event.NewEnd("endDecoy"),
			event.NewEnd("endReal"),
		},
		Flows: []flow.SequenceFlow{
			// The decoy, declared first so it wins any first-match-by-ID lookup.
			{ID: decoyID, Source: "decoySource", Target: "decoyTarget"},
			{ID: "f-start", Source: "start", Target: "fork"},
			{ID: "f-fork-work", Source: "fork", Target: "work"},
			{ID: "f-fork-decoy", Source: "fork", Target: "decoySource"},
			{ID: "f-work-end", Source: "work", Target: "endWork"},
			{ID: "f-decoy-end", Source: "decoyTarget", Target: "endDecoy"},
			// The boundary's own outgoing flow, declared last.
			{ID: flowID, Source: "bnd", Target: "realTarget"},
			{ID: "f-real-end", Source: "realTarget", Target: "endReal"},
		},
	}
}

// TestBoundaryRoutesToItsOwnOutgoingFlowTarget pins that a fired boundary event
// routes its token to the target of ITS OWN outgoing flow — the flow the arming
// code already resolved by Source, which is unique — and never to a different
// flow that merely shares the same authored ID.
//
// The blank-ID row is the defect in #212: `model.Validate` accepts the
// definition, the boundary fires, and the token lands on the decoy's target with
// the decoy's action invoked. The named-ID rows are the controls that keep the
// ordinary routing path honest.
func TestBoundaryRoutesToItsOwnOutgoingFlowTarget(t *testing.T) {
	t.Parallel()

	// Shared across every row: the boundary must reach its own target, and the
	// decoy must never be entered.
	routesToRealTarget := func(t *testing.T, r engine.StepResult, err error) {
		t.Helper()
		require.NoError(t, err)

		var actions []string
		for _, c := range r.Commands {
			if ia, ok := c.(engine.InvokeAction); ok {
				actions = append(actions, ia.Name)
			}
		}
		assert.Contains(t, actions, "real-action",
			"the boundary's own outgoing flow target must be entered")
		assert.NotContains(t, actions, "decoy-action",
			"a flow that merely shares the boundary flow's ID must never be taken")

		var nodes []string
		for _, tk := range r.State.Tokens {
			nodes = append(nodes, tk.NodeID)
		}
		assert.Contains(t, nodes, "realTarget", "a token must be parked at realTarget")
		assert.NotContains(t, nodes, "decoyTarget", "no token may reach decoyTarget")
	}

	type testCase struct {
		name    string
		decoyID string
		flowID  string
		assert  func(t *testing.T, r engine.StepResult, err error)
	}

	cases := []testCase{
		{
			name:    "blank boundary flow ID with an earlier blank-ID flow",
			decoyID: "",
			flowID:  "",
			assert:  routesToRealTarget,
		},
		{
			name:    "control: both flow IDs named",
			decoyID: "f-decoy",
			flowID:  "f-bnd",
			assert:  routesToRealTarget,
		},
		{
			name:    "control: a blank-ID flow elsewhere, boundary flow named",
			decoyID: "",
			flowID:  "f-bnd",
			assert:  routesToRealTarget,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			def := boundaryDecoyFlowDef(tc.decoyID, tc.flowID)
			require.NoError(t, model.Validate(def),
				"precondition: the definition must be accepted by model.Validate")

			t0 := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
			r1, err := engine.Step(ctx, def, engine.InstanceState{InstanceID: "i1"},
				engine.NewStartInstance(t0, nil), engine.StepOptions{})
			require.NoError(t, err)
			require.Len(t, r1.State.Boundaries, 1,
				"precondition: the message boundary must be armed")

			r2, err := engine.Step(ctx, def, r1.State,
				engine.NewMessageReceived(t0.Add(time.Minute), "cancel", "", nil), engine.StepOptions{})
			tc.assert(t, r2, err)
		})
	}
}

// boundaryTargetDef returns:
//
//	start → work (UserTask, interrupting message boundary "bnd") → end
//	              bnd → targetID
//
// When declareTarget is true the definition also declares a node carrying
// targetID and an edge out of it, so the boundary's flow resolves; when it is
// false the flow dangles and model.Validate refuses the definition with
// ErrDanglingFlow.
//
// The blank targetID with declareTarget true is the case that matters: a node
// ID may legitimately be blank (definition/model deduplicates node IDs but does
// not refuse a blank one), so "the target string is empty" is NOT the same
// question as "the target names nothing".
func boundaryTargetDef(targetID string, declareTarget bool) *model.ProcessDefinition {
	def := &model.ProcessDefinition{
		ID: "p-bnd-target", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("work"),
			event.NewBoundary("bnd", "work", event.WithMessageCorrelator("cancel", "")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f-start", Source: "start", Target: "work"},
			{ID: "f-work-end", Source: "work", Target: "end"},
			{ID: "f-bnd", Source: "bnd", Target: targetID},
		},
	}
	if declareTarget {
		def.Nodes = append(def.Nodes,
			activity.NewServiceTask(targetID, activity.WithTaskAction("target-action")),
			event.NewEnd("end2"))
		def.Flows = append(def.Flows, flow.SequenceFlow{ID: "f-target-end", Source: targetID, Target: "end2"})
	}
	return def
}

// TestBoundaryFireFailsLoudlyWhenItsTargetNamesNothing pins that a boundary
// whose outgoing flow points at a node the definition does not declare fails
// CLOSED and AUDIBLY: a named error, the host token left parked, and no token
// placed on a node that does not exist.
//
// The definition is one model.Validate refuses (ErrDanglingFlow), so it only
// reaches the engine on the unvalidated path — which this repo states is real:
// warnUnarmedBoundaries' own doc says "every authoring route runs
// model.Validate, but a definition can still reach the engine without passing
// through it", and step_nodes.go says of exactly this argument that "validation
// is the authoring gate, not the only door".
//
// The blank-ID-node row is the discriminator. The pre-#212 code guarded on
// `flowTarget == ""`, which refuses that row wrongly; the guard has to ask
// whether the target RESOLVES, not whether its string is empty.
func TestBoundaryFireFailsLoudlyWhenItsTargetNamesNothing(t *testing.T) {
	t.Parallel()

	// armed is the caller's state as it stood before the boundary fired — the
	// state a failed Step leaves it holding, and therefore where "the host was
	// not consumed" is actually observable.
	type testCase struct {
		name          string
		targetID      string
		declareTarget bool
		validates     bool
		assert        func(t *testing.T, armed engine.InstanceState, r engine.StepResult, err error)
	}

	refusedLoudly := func(t *testing.T, armed engine.InstanceState, r engine.StepResult, err error) {
		t.Helper()
		require.Error(t, err, "an unresolvable boundary target must fail, not route")
		assert.Contains(t, err.Error(), `boundary "bnd"`,
			"the failure must name the boundary that could not be routed")

		var returned []string
		for _, tk := range r.State.Tokens {
			returned = append(returned, tk.NodeID)
		}
		assert.NotContains(t, returned, "", "no token may be parked on a node that does not exist")

		// The failure is refused before anything is mutated, so the caller keeps
		// the state it passed in, host token included. Work is not lost.
		var held []string
		for _, tk := range armed.Tokens {
			held = append(held, tk.NodeID)
		}
		require.NotEmpty(t, held, "instrument: the armed state must actually carry a token")
		assert.Contains(t, held, "work",
			"the host token must survive: a boundary that cannot route must not consume it")

		for _, c := range r.Commands {
			_, isAction := c.(engine.InvokeAction)
			assert.False(t, isAction, "no fire-once action may be emitted for a boundary that cannot route")
		}
	}

	cases := []testCase{
		{
			name:     "the outgoing flow dangles on a blank target",
			targetID: "", declareTarget: false, validates: false,
			assert: refusedLoudly,
		},
		{
			name:     "the outgoing flow dangles on a named target",
			targetID: "ghost", declareTarget: false, validates: false,
			assert: refusedLoudly,
		},
		{
			name:     "the outgoing flow names a node whose ID is genuinely blank",
			targetID: "", declareTarget: true, validates: true,
			assert: func(t *testing.T, _ engine.InstanceState, r engine.StepResult, err error) {
				t.Helper()
				require.NoError(t, err, "a declared blank-ID node is a legitimate target")

				var actions []string
				for _, c := range r.Commands {
					if ia, ok := c.(engine.InvokeAction); ok {
						actions = append(actions, ia.Name)
					}
				}
				assert.Contains(t, actions, "target-action",
					"the blank-ID target node must be entered, not refused")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			def := boundaryTargetDef(tc.targetID, tc.declareTarget)
			if tc.validates {
				require.NoError(t, model.Validate(def), "precondition")
			} else {
				require.Error(t, model.Validate(def),
					"precondition: this definition reaches the engine only on the unvalidated path")
			}

			t0 := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
			r1, err := engine.Step(ctx, def, engine.InstanceState{InstanceID: "i1"},
				engine.NewStartInstance(t0, nil), engine.StepOptions{})
			require.NoError(t, err)
			require.Len(t, r1.State.Boundaries, 1, "precondition: the boundary must be armed")

			armed := r1.State
			r2, err := engine.Step(ctx, def, armed,
				engine.NewMessageReceived(t0.Add(time.Minute), "cancel", "", nil), engine.StepOptions{})
			tc.assert(t, armed, r2, err)
		})
	}
}

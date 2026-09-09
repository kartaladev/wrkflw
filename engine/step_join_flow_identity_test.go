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

// blankFlowIDJoinDef is implicitMergeIntoJoinDef parameterised on the IDs of the
// join's two incoming flows, so a test can make either or both of them BLANK.
//
// A blank sequence-flow ID is legal and reaches this code from stored data, not
// only from a Go literal: `model.Validate` accepts any number of blank-ID flows,
// `flow.SequenceFlow.ID` carries no `omitempty` so a blank ID re-marshals
// explicitly as `"id":""`, and JSON with `id` omitted, JSON with `"id":""` and
// YAML with `id` omitted all decode to the same blank. So this is not an
// authoring hazard that a careful author avoids — it is a shape already sitting
// in stores.
//
// fj1 (merge → join) is crossed by TWO tokens; fj2 (svcC → join) is never
// crossed while "c" is outstanding.
func blankFlowIDJoinDef(fj1ID, fj2ID string) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "blank-flow-id-join", Version: 1,
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
			{ID: "f0", Source: "start", Target: "pfork"},
			{ID: "f1", Source: "pfork", Target: "svcC"},
			{ID: "f2", Source: "pfork", Target: "xa"},
			{ID: "f3", Source: "pfork", Target: "xb"},
			{ID: "f4", Source: "xa", Target: "merge"},
			{ID: "f5", Source: "xb", Target: "merge"},
			{ID: fj1ID, Source: "merge", Target: "join"},
			{ID: fj2ID, Source: "svcC", Target: "join"},
			{ID: "f6", Source: "join", Target: "end"},
		},
	}
}

// twinBlankEdgeJoinDef gives a converging parallel gateway two incoming flows
// that are indistinguishable by every authored field — same blank ID, same
// source, same target — and arranges for one of them to be crossed TWICE while
// its twin is never crossed at all.
//
//	start → pfork (parallel, 1 in / 2 out)
//	  e1: pfork → xa ; e2: pfork → xb    (exclusive pass-throughs)
//	  e3: xa → merge ; e4: xb → merge    (merge = exclusive, 2 in / 1 out)
//	  e5: merge → snd                    (send task: 1 in, 2 out)
//	  (blank): snd → join   ×2   ← two distinct edges, identical in every field
//	  join (parallel, 2 in / 1 out) → e6 → end
//
// `snd` is a SEND TASK rather than a gateway for two reasons, both measured. A
// gateway with two incoming and two outgoing is refused by ErrMixedGateway, and a
// send task advances through moveAlongSingleFlow, which always takes out[0] — so
// both tokens cross the FIRST blank edge and neither crosses the second.
// `model.Validate` accepts this definition.
//
// This is the row that decides the identity scheme, and nothing else does. An
// ENDPOINT-derived synthetic gives both edges one value, so the two tokens
// satisfy both incoming flows and the join fires with an edge never traversed —
// the same fail-open the authored ID had, merely relocated. An identity derived
// from the flow's POSITION separates them by construction.
//
// `pfork` is what keeps ErrUnpairedJoin happy: it wants a real concurrency source
// with two branches that each forward-reach the join.
func twinBlankEdgeJoinDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "twin-blank-edge-join", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewParallel("pfork"),
			gateway.NewExclusive("xa"),
			gateway.NewExclusive("xb"),
			gateway.NewExclusive("merge"),
			activity.NewSendTask("snd", "twin-topic"),
			gateway.NewParallel("join"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "e0", Source: "start", Target: "pfork"},
			{ID: "e1", Source: "pfork", Target: "xa"},
			{ID: "e2", Source: "pfork", Target: "xb"},
			{ID: "e3", Source: "xa", Target: "merge"},
			{ID: "e4", Source: "xb", Target: "merge"},
			{ID: "e5", Source: "merge", Target: "snd"},
			{Source: "snd", Target: "join"},
			{Source: "snd", Target: "join"},
			{ID: "e6", Source: "join", Target: "end"},
		},
	}
}

// TestParallelJoinIdentifiesFlowsTheEngineMints pins that a converging parallel
// gateway accounts per incoming EDGE, using an identity the engine mints, and not
// the authored `flow.SequenceFlow.ID`.
//
// The authored ID is not a key. `model.Validate` deliberately permits any number
// of blank flow IDs, so two distinct incoming edges can carry the same one. Keyed
// on it, the join reads two tokens that crossed the same edge as two branches and
// fires with a synchronisation point skipped.
//
// The rows are the four combinations of blank and named on the join's two
// incoming flows, plus the twin-edge shape. Row "control" and row "C" are the
// discriminators: a fix that simply refuses everything satisfies "A" and "B" as
// easily as a correct one, and both of these must keep waiting for the RIGHT
// reason — a flow that genuinely delivered nothing.
func TestParallelJoinIdentifiesFlowsTheEngineMints(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)

	type testCase struct {
		name string
		def  func() *model.ProcessDefinition
		// wantParkedAtJoin is how many tokens must be parked at the join after the
		// start step. In every row the join must NOT have fired.
		wantParkedAtJoin int
		// undeliveredFrom is a node that must still hold a parked token — the
		// source of the join's never-traversed incoming edge. It is the guard that
		// stops "the join did not fire" passing because the branch simply is not
		// there. Empty when the untraversed edge has no parked source, as in the
		// twin-edge row where both edges share one.
		undeliveredFrom string
	}

	cases := []testCase{
		{
			name:             "control: both incoming flows named",
			undeliveredFrom:  "svcC",
			def:              func() *model.ProcessDefinition { return blankFlowIDJoinDef("fj1", "fj2") },
			wantParkedAtJoin: 2,
		},
		{
			name:             "A: both incoming flows blank",
			undeliveredFrom:  "svcC",
			def:              func() *model.ProcessDefinition { return blankFlowIDJoinDef("", "") },
			wantParkedAtJoin: 2,
		},
		{
			name:             "B: only the doubled edge blank",
			undeliveredFrom:  "svcC",
			def:              func() *model.ProcessDefinition { return blankFlowIDJoinDef("", "fj2") },
			wantParkedAtJoin: 2,
		},
		{
			name:             "C: only the never-traversed edge blank",
			undeliveredFrom:  "svcC",
			def:              func() *model.ProcessDefinition { return blankFlowIDJoinDef("fj1", "") },
			wantParkedAtJoin: 2,
		},
		{
			name:             "twin edges: one crossed twice, its identical sibling never",
			def:              twinBlankEdgeJoinDef,
			wantParkedAtJoin: 2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			def := tc.def()
			require.NoError(t, model.Validate(def),
				"every row must be a definition the validator accepts — blank flow IDs included, which is the point")
			require.Len(t, def.Incoming("join"), 2, "fixture shape guard")

			res, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i1"},
				engine.NewStartInstance(at, nil), engine.StepOptions{})
			require.NoError(t, err)

			assert.False(t, visited(res.State, "end"),
				"one of the join's incoming edges has delivered nothing: the join must not have fired")
			assert.Len(t, tokensAt(res.State, "join"), tc.wantParkedAtJoin)
			if tc.undeliveredFrom != "" {
				require.Len(t, tokensAt(res.State, tc.undeliveredFrom), 1,
					"precondition: the source of the never-traversed edge is still parked")
			}
			assert.Equal(t, engine.StatusRunning, res.State.Status)
		})
	}
}

// compensationThrowIntoJoinDef routes a compensation-throw resume straight into a
// converging parallel gateway, with a sibling branch parked on the join's OTHER
// incoming edge.
//
//	start → pfork (parallel, 1 in / 3 out)
//	  c1: pfork → svcC   (service task "c" — parks; the only source of cj2)
//	  c2: pfork → xa ; c3: pfork → xb   (exclusive pass-throughs)
//	  c4: xa → thr ; c5: xb → thr       (thr = compensation throw, targeted)
//	  cj1: thr → join                    ← BOTH throw branches resume over THIS edge
//	  cj2: svcC → join
//	  c6: join → end                     (join = parallel gateway, 2 in / 1 out)
//
// Two tokens reach the throw. The first starts a compensation walk; the second is
// deferred and runs after it. Each walk finishes by placing a token at the throw's
// successor — the join — and that placement goes through applyFinish, not through
// a flow traversal. Both tokens therefore arrive over cj1 and neither over cj2.
func compensationThrowIntoJoinDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "comp-throw-into-join", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewParallel("pfork"),
			activity.NewServiceTask("svcC", activity.WithTaskAction("c")),
			gateway.NewExclusive("xa"),
			gateway.NewExclusive("xb"),
			event.NewCompensateThrow("thr"),
			gateway.NewParallel("join"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "c0", Source: "start", Target: "pfork"},
			{ID: "c1", Source: "pfork", Target: "svcC"},
			{ID: "c2", Source: "pfork", Target: "xa"},
			{ID: "c3", Source: "pfork", Target: "xb"},
			{ID: "c4", Source: "xa", Target: "thr"},
			{ID: "c5", Source: "xb", Target: "thr"},
			{ID: "cj1", Source: "thr", Target: "join"},
			{ID: "cj2", Source: "svcC", Target: "join"},
			{ID: "c6", Source: "join", Target: "end"},
		},
	}
}

// TestCompensationResumeCarriesItsArrivalEdge pins that the compensation-throw
// resume records the edge it resumes over.
//
// The throw's resume IS a flow traversal — the walk finishes and the branch
// continues down the throw event's single outgoing flow — so a token placed there
// with no provenance is not the documented "arrived over no edge" case. It is a
// lost edge, and at a converging parallel gateway a lost edge is spent by the
// empty-provenance fallback on a flow the token never crossed. Every flow ID in
// this fixture is named: the defect needs no blank-ID trick.
func TestCompensationResumeCarriesItsArrivalEdge(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	def := compensationThrowIntoJoinDef()
	require.NoError(t, model.Validate(def))
	require.Len(t, def.Incoming("join"), 2, "fixture shape guard")

	// A scope-wide throw drains the throwing scope's own records. Seeding one is
	// what makes the FIRST throw start a real walk (and so resume through
	// applyFinish); the second throw is deferred behind it.
	st := engine.InstanceState{
		InstanceID:        "i1",
		RootCompensations: []engine.CompensationRecord{{NodeID: "act1", Action: "cancel1", CompletedAt: at}},
	}
	r0, err := engine.Step(t.Context(), def, st, engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)

	// Precondition: one walk in flight, the second throw deferred, and svcC —
	// cj2's only source — still parked.
	cancel1 := invokedAction(t, r0.Commands, "cancel1")
	require.Len(t, r0.State.DeferredCompensationThrows, 1, "the second throw must be deferred")
	require.Len(t, tokensAt(r0.State, "svcC"), 1, "cj2's source must still be parked")

	// Completing the walk resumes the first branch onto the join over cj1.
	r1, err := engine.Step(t.Context(), def, r0.State,
		engine.NewActionCompleted(at.Add(time.Second), cancel1.CommandID, nil), engine.StepOptions{})
	require.NoError(t, err)

	require.Len(t, tokensAt(r1.State, "svcC"), 1,
		"precondition: nothing has crossed cj2 — svcC is still awaiting its action")
	assert.False(t, visited(r1.State, "end"),
		"the resume arrived over cj1; cj2 has delivered nothing, so the join must not fire")
	assert.Equal(t, engine.StatusRunning, r1.State.Status)
}

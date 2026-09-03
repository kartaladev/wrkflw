package engine_test

// step_clone_history_test.go — cloneState's History deep copy
// (engine/step_state.go, the `s.History = append([]NodeVisit(nil), st.History...)`
// line) was load-bearing and COMPLETELY untested: the whole engine package stayed
// green when it was replaced by a shallow assignment.
//
// It is the line a performance pass invites you to delete: every Step is already
// O(entire state) and this copy is the obvious cost. Deleting it under a green
// suite ships silent state corruption, which is what this file exists to stop.
//
// ⚠ THE FIXTURE IS THE WHOLE POINT. A History whose cap == len cannot observe
// the corruption: `append` on a full slice allocates a fresh array, so two Steps
// sharing a base never write the same slot. Only a base with SPARE CAPACITY
// (cap > len) makes both Steps append into the SAME backing array. The two cases
// below are run against the same assertions precisely so the difference is
// re-derived here rather than taken on trust — see the delivery's evidence file
// for the observed mutation results.

import (
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
)

// cloneHistoryDef is a two-task chain: completing workA closes its visit and
// opens a visit at workB, so a Step from the parked base both MUTATES an
// existing NodeVisit and APPENDS a new one.
//
//	start → workA[UserTask] → workB[UserTask] → end
func cloneHistoryDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-clone-history", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("workA"),
			activity.NewUserTask("workB"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "workA"},
			{ID: "f2", Source: "workA", Target: "workB"},
			{ID: "f3", Source: "workB", Target: "end"},
		},
	}
}

// snapshotHistory copies h into an independently allocated slice so a later
// mutation of h's backing array is detectable by comparison.
func snapshotHistory(h []engine.NodeVisit) []engine.NodeVisit {
	out := make([]engine.NodeVisit, len(h))
	copy(out, h)
	return out
}

// TestCloneStateHistoryIsIndependentlyAllocated pins cloneState's History deep
// copy: two Steps driven from ONE base state must not share the base's History
// backing array, so neither can observe or overwrite the other's visits.
//
// What makes it fail: replacing the deep copy with `s.History = st.History`.
// Under that mutation the "spare capacity" case corrupts both directions — the
// two Steps append their new workB visit into the SAME slot, and each Step's
// close of the workA visit writes through to the shared element.
func TestCloneStateHistoryIsIndependentlyAllocated(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)

	type testCase struct {
		name string
		// grow rebuilds the base History with the capacity profile under test.
		grow func(h []engine.NodeVisit) []engine.NodeVisit
	}

	cases := []testCase{
		{
			name: "base history with spare capacity",
			grow: func(h []engine.NodeVisit) []engine.NodeVisit {
				out := make([]engine.NodeVisit, len(h), len(h)+4)
				copy(out, h)
				return out
			},
		},
		{
			name: "base history with no spare capacity",
			grow: func(h []engine.NodeVisit) []engine.NodeVisit {
				out := make([]engine.NodeVisit, len(h))
				copy(out, h)
				return out
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			def := cloneHistoryDef()

			started, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i1"},
				engine.NewStartInstance(at, nil), engine.StepOptions{})
			require.NoError(t, err)

			var taskID string
			for _, cmd := range started.Commands {
				if ah, ok := cmd.(engine.AwaitHuman); ok {
					taskID = ah.TaskID
				}
			}
			require.NotEmpty(t, taskID, "the fixture must park on a human task")

			base := started.State
			base.History = tc.grow(base.History)
			require.NotEmpty(t, base.History, "the base must carry history for the copy to matter")
			baseSnapshot := snapshotHistory(base.History)

			// Two independent Steps from the SAME base, four hours apart so a
			// leaked write is visible as a wrong timestamp rather than a
			// coincidentally equal one.
			resA, err := engine.Step(t.Context(), def, base,
				engine.NewHumanCompleted(at.Add(time.Hour), taskID, engine.CompletionInput{}, authz.Actor{ID: "alice"}),
				engine.StepOptions{})
			require.NoError(t, err)
			afterA := snapshotHistory(resA.State.History)

			resB, err := engine.Step(t.Context(), def, base,
				engine.NewHumanCompleted(at.Add(5*time.Hour), taskID, engine.CompletionInput{}, authz.Actor{ID: "bob"}),
				engine.StepOptions{})
			require.NoError(t, err)

			// 1. The base state the caller still holds is untouched by either Step.
			assert.Equal(t, baseSnapshot, base.History,
				"Step must not write through to the caller's History")

			// 2. The first result is unchanged by the second Step.
			assert.Equal(t, afterA, resA.State.History,
				"the second Step wrote into the first result's History backing array")

			// 3. The two results occupy distinct backing arrays.
			require.NotEmpty(t, resA.State.History)
			require.NotEmpty(t, resB.State.History)
			assert.NotSame(t, &resA.State.History[0], &resB.State.History[0],
				"the two Steps' History slices alias the same backing array")
			assert.NotSame(t, &base.History[0], &resA.State.History[0],
				"the clone's History aliases the base's backing array")
		})
	}
}

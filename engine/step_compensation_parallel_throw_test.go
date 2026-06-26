package engine_test

// step_compensation_parallel_throw_test.go — P1 (ADR-0071): serialize concurrent
// compensation throws.
//
// In Macro mode drive() advances every active token in one pass. Two
// compensation-throw IntermediateThrowEvents in parallel branches are both
// processed in the SAME drive pass. Before ADR-0071 the second throw silently
// OVERWROTE the single Compensating cursor, orphaning the first walk (its
// ActionCompleted → ErrTokenNotFound).
//
// These tests are strict RED-first per the TDD discipline: the first test
// reproduces the bug (cursor overwrite), then the fix flips it to "both
// compensations complete in sequence; the deferred queue drains".
//
// Design ref: docs/specs/2026-06-27-parallel-compensation-throw-design.md
// ADR: 0071

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/model"
)

// parallelThrowDef returns a process definition with a parallel fork to N
// compensation-throw branches that join and end:
//
//	start → forkGW (ParallelGateway)
//	         ├─ compThrow_1 (CompensateRef "ref1") → joinGW
//	         ├─ compThrow_2 (CompensateRef "ref2") → joinGW
//	         └─ ... (N branches)
//	joinGW (ParallelGateway) → end
//
// The compensation records are NOT produced by sub-processes here; the caller
// pre-seeds ArchivedCompensations under "ref1".."refN" so both throws reach the
// compensation-throw branch in the SAME drive pass after the fork.
func parallelThrowDef(n int) *model.ProcessDefinition {
	nodes := []model.Node{
		model.NewStartEvent("start"),
		model.NewParallelGateway("forkGW"),
		model.NewParallelGateway("joinGW"),
		model.NewEndEvent("end"),
	}
	flows := []model.SequenceFlow{
		{ID: "f-start", Source: "start", Target: "forkGW"},
		{ID: "f-join-end", Source: "joinGW", Target: "end"},
	}
	for i := 1; i <= n; i++ {
		throwID := throwName(i)
		ref := refName(i)
		nodes = append(nodes, model.NewIntermediateThrowEvent(throwID, model.WithCompensateRef(ref)))
		flows = append(flows,
			model.SequenceFlow{ID: "f-fork-" + throwID, Source: "forkGW", Target: throwID},
			model.SequenceFlow{ID: "f-" + throwID + "-join", Source: throwID, Target: "joinGW"},
		)
	}
	return &model.ProcessDefinition{
		ID: "parallel-throw-proc", Version: 1,
		Nodes: nodes,
		Flows: flows,
	}
}

func throwName(i int) string {
	return "compThrow" + itoa(i)
}

func refName(i int) string {
	return "ref" + itoa(i)
}

// startParallelThrows builds an InstanceState whose forkGW forks into N active
// throw tokens, seeds ArchivedCompensations for the refs listed in seededRefs
// (1-based indices), and drives the StartInstance trigger. Branches whose ref is
// NOT in seededRefs have no archived records and so auto-advance at their throw.
func startParallelThrows(t *testing.T, def *model.ProcessDefinition, n int, seededRefs []int, at time.Time) engine.StepResult {
	t.Helper()
	archive := make(map[string][]engine.CompensationRecord, len(seededRefs))
	for _, i := range seededRefs {
		archive[refName(i)] = []engine.CompensationRecord{
			{NodeID: "act" + itoa(i), Action: "cancel" + itoa(i), CompletedAt: at},
		}
	}
	st := engine.InstanceState{InstanceID: "par-throw-inst", ArchivedCompensations: archive}
	r, err := engine.Step(def, st, engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	return r
}

// allRefs returns [1..n], the convenience "seed every branch" selector.
func allRefs(n int) []int {
	out := make([]int, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, i)
	}
	return out
}

// invokeActionsByName returns the InvokeAction commands in cmds keyed by Name.
func invokeActionsByName(cmds []engine.Command) map[string]engine.InvokeAction {
	out := make(map[string]engine.InvokeAction)
	for _, cmd := range cmds {
		if ia, ok := cmd.(engine.InvokeAction); ok {
			out[ia.Name] = ia
		}
	}
	return out
}

// TestParallelCompensationThrowsSerialize is the P1 regression test (ADR-0071).
//
// RED (before the fix): the fork drives both throws in one pass; the second throw
// overwrites the single Compensating cursor. Completing the FIRST throw's
// InvokeAction (cancel1) then yields ErrTokenNotFound (its cmd id no longer
// matches the cursor's ActiveCmdID).
//
// GREEN (after the fix): only ONE walk starts (cancel1); the second throw is
// parked/deferred. Completing cancel1 finishes the first walk and re-activates
// the deferred throw, which starts its walk (cancel2). Completing cancel2 drains
// the deferred queue, both branches resume to the join, and the instance
// completes.
func TestParallelCompensationThrowsSerialize(t *testing.T) {
	at := time.Date(2026, 6, 27, 9, 0, 0, 0, time.UTC)
	def := parallelThrowDef(2)

	r := startParallelThrows(t, def, 2, allRefs(2), at)

	// Exactly ONE compensation walk must start in the fork pass (serialized).
	assert.Equal(t, engine.StatusCompensating, r.State.Status,
		"a compensation walk must be in flight after the fork")
	firstActions := invokeActionsByName(r.Commands)
	require.Len(t, firstActions, 1,
		"only ONE compensation InvokeAction may be emitted in the fork pass (serialize)")
	_, hasCancel1 := firstActions["cancel1"]
	require.True(t, hasCancel1, "the first throw's walk (cancel1) must start")
	require.Len(t, r.State.DeferredCompensationThrows, 1,
		"the second throw must be deferred")

	// Complete cancel1 → first walk finishes → branch resumes AND the deferred
	// second throw is re-activated, starting its walk (cancel2). This is the step
	// that, before the fix, fails with ErrTokenNotFound because the cursor was
	// overwritten by the second throw in the fork pass.
	r2, err := engine.Step(def, r.State,
		engine.NewActionCompleted(at.Add(time.Second), firstActions["cancel1"].CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err, "completing the first throw's compensation must not orphan the walk")

	secondActions := invokeActionsByName(r2.Commands)
	require.Contains(t, secondActions, "cancel2",
		"finishing the first walk must start the deferred second throw (cancel2)")
	assert.Equal(t, engine.StatusCompensating, r2.State.Status,
		"the second (deferred) walk must now be in flight")
	assert.Empty(t, r2.State.DeferredCompensationThrows,
		"the deferred queue must drain to empty once the second throw starts")

	// Complete cancel2 → second walk finishes → second branch resumes → both
	// tokens reach joinGW → end → StatusCompleted.
	r3, err := engine.Step(def, r2.State,
		engine.NewActionCompleted(at.Add(2*time.Second), secondActions["cancel2"].CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, r3.State.Status,
		"both branches resume and the instance completes after both throws serialize")
	assert.Empty(t, r3.State.DeferredCompensationThrows,
		"the deferred queue must be empty at completion")
}

// TestParallelCompensationThrowsCursorOverwriteIsFixed asserts the precise bug
// signature is gone: with the fix, completing the first throw's compensation
// never returns ErrTokenNotFound.
func TestParallelCompensationThrowsCursorOverwriteIsFixed(t *testing.T) {
	at := time.Date(2026, 6, 27, 9, 30, 0, 0, time.UTC)
	def := parallelThrowDef(2)
	r := startParallelThrows(t, def, 2, allRefs(2), at)

	first := invokeActionsByName(r.Commands)
	require.Contains(t, first, "cancel1")

	_, err := engine.Step(def, r.State,
		engine.NewActionCompleted(at.Add(time.Second), first["cancel1"].CommandID, nil),
		engine.StepOptions{})
	require.False(t, errors.Is(err, engine.ErrTokenNotFound),
		"the first throw's compensation must not be orphaned (cursor overwrite bug)")
}

// TestParallelCompensationThrowsDrainOrdering is the Task-3 table for the
// serialize/drain behaviour: it varies the branch count and which refs carry
// archived records, asserting the deferred queue drains exactly one walk per
// finish and that branches whose ref has no records auto-advance WITHOUT being
// enqueued. The SUT call shape is identical across cases (build def → seed →
// drive throws → complete compensations until terminal), so it is a table.
func TestParallelCompensationThrowsDrainOrdering(t *testing.T) {
	type testCase struct {
		name       string
		branches   int
		seededRefs []int
		// expectOrder is the sequence of compensation action names that must be
		// emitted, one per finish, in drain order.
		expectOrder []string
		assert      func(t *testing.T, finalState engine.InstanceState)
	}

	cases := []testCase{
		{
			name:        "three throws drain one per finish",
			branches:    3,
			seededRefs:  []int{1, 2, 3},
			expectOrder: []string{"cancel1", "cancel2", "cancel3"},
			assert: func(t *testing.T, st engine.InstanceState) {
				assert.Equal(t, engine.StatusCompleted, st.Status,
					"all three branches resume and the instance completes")
				assert.Empty(t, st.DeferredCompensationThrows,
					"the deferred queue must be empty at completion")
			},
		},
		{
			name:       "empty-ref throw auto-advances and is not enqueued",
			branches:   2,
			seededRefs: []int{1}, // branch 2's ref has NO archived records.
			// Only cancel1 runs a walk; the empty-ref branch (compThrow2) auto-advances.
			expectOrder: []string{"cancel1"},
			assert: func(t *testing.T, st engine.InstanceState) {
				assert.Equal(t, engine.StatusCompleted, st.Status,
					"the seeded branch compensates and the empty-ref branch auto-advances to completion")
				assert.Empty(t, st.DeferredCompensationThrows,
					"the empty-ref throw must NOT be enqueued")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			at := time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)
			def := parallelThrowDef(tc.branches)
			r := startParallelThrows(t, def, tc.branches, tc.seededRefs, at)

			// On the fork pass, at most ONE walk may be in flight; the empty-ref
			// branches auto-advance and must never appear in the deferred queue.
			require.LessOrEqual(t, len(r.State.DeferredCompensationThrows), tc.branches-1,
				"empty-ref throws must not be enqueued")

			state := r.State
			cmds := r.Commands
			for step, wantAction := range tc.expectOrder {
				actions := invokeActionsByName(cmds)
				require.Contains(t, actions, wantAction,
					"finish step %d must emit %q (one walk per finish)", step, wantAction)
				require.Len(t, actions, 1,
					"exactly ONE compensation walk may be in flight per finish (serialize)")

				next, err := engine.Step(def, state,
					engine.NewActionCompleted(at.Add(time.Duration(step+1)*time.Second), actions[wantAction].CommandID, nil),
					engine.StepOptions{})
				require.NoError(t, err, "completing %q must not orphan the walk", wantAction)
				state = next.State
				cmds = next.Commands
			}

			tc.assert(t, state)
		})
	}
}

// TestParallelThrowWithCancelMidFirstThrow documents the resolved ordering when a
// CancelRequested arrives mid-first-throw while a SECOND throw is deferred. It has
// a distinct multi-step shape (cancel trigger interleaved), so it stays a
// standalone test rather than folding into the drain table above.
//
// Resolved ordering (ADR-0040 + ADR-0071):
//   - The cancel is DEFERRED (PendingCancel set) because a throw walk is in flight
//     (handleCancelRequested's in-flight guard, ResumeNode != "").
//   - When the first throw's compensation completes, stepCompensationFinish sees
//     PendingCancel and runs a FULL cancel over the REMAINING records, terminating
//     the instance instead of resuming.
//   - The deferred SECOND throw token is parked (TokenWaitingCommand) and is NOT
//     left orphaned: the full cancel consumes all tokens (beginCompensation cancels
//     in-flight tokens) and the instance terminates. The deferred queue id is moot
//     because the instance never resumes to re-activate it.
func TestParallelThrowWithCancelMidFirstThrow(t *testing.T) {
	at := time.Date(2026, 6, 27, 11, 0, 0, 0, time.UTC)
	def := parallelThrowDef(2)
	r := startParallelThrows(t, def, 2, allRefs(2), at)

	first := invokeActionsByName(r.Commands)
	require.Contains(t, first, "cancel1", "first throw walk must start")
	require.Len(t, r.State.DeferredCompensationThrows, 1, "second throw must be deferred")
	require.Equal(t, engine.StatusCompensating, r.State.Status)
	require.NotEmpty(t, r.State.Compensating.ResumeNode, "in-flight walk is a throw walk")

	// CancelRequested mid-first-throw: deferred (PendingCancel), instance stays
	// compensating, the deferred second throw remains queued.
	r2, err := engine.Step(def, r.State, engine.NewCancelRequested(at.Add(time.Second)), engine.StepOptions{})
	require.NoError(t, err)
	assert.True(t, r2.State.PendingCancel, "cancel mid-throw-walk must be deferred (PendingCancel)")
	assert.Equal(t, engine.StatusCompensating, r2.State.Status,
		"the throw walk is still in flight; cancel does not pre-empt it")

	// Complete the first throw's compensation → deferred cancel fires → full cancel
	// over remaining records → terminate. No deferred throw is left orphaned: the
	// instance terminates rather than resuming.
	r3, err := engine.Step(def, r2.State,
		engine.NewActionCompleted(at.Add(2*time.Second), first["cancel1"].CommandID, nil),
		engine.StepOptions{})
	require.NoError(t, err)

	// The full cancel walk may need one more step to drain its own InvokeAction.
	finalState := r3.State
	finalCmds := r3.Commands
	if finalState.Status == engine.StatusCompensating {
		// Find the in-flight cancel-walk InvokeAction (the remaining record's action,
		// "cancel2") and complete it.
		acts := invokeActionsByName(r3.Commands)
		require.NotEmpty(t, acts, "deferred cancel must emit a compensation InvokeAction")
		var cmdID string
		for _, ia := range acts {
			cmdID = ia.CommandID
		}
		r4, err := engine.Step(def, r3.State,
			engine.NewActionCompleted(at.Add(3*time.Second), cmdID, nil),
			engine.StepOptions{})
		require.NoError(t, err)
		finalState = r4.State
		finalCmds = r4.Commands
	}

	assert.Equal(t, engine.StatusTerminated, finalState.Status,
		"deferred cancel must terminate the instance after the in-flight throw walk finishes")
	var hasFailInstance bool
	for _, cmd := range finalCmds {
		if fi, ok := cmd.(engine.FailInstance); ok && fi.Err == "cancelled" {
			hasFailInstance = true
		}
	}
	assert.True(t, hasFailInstance, "deferred cancel must emit FailInstance{cancelled}")
}

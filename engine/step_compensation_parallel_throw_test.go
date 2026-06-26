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

// seedArchivedThrows builds an InstanceState whose forkGW already forked into N
// active throw tokens, and whose ArchivedCompensations carry one record per ref.
// It is the deterministic mid-flow state used to drive both throws in one pass.
func startParallelThrows(t *testing.T, def *model.ProcessDefinition, n int, at time.Time) engine.StepResult {
	t.Helper()
	st := engine.InstanceState{
		InstanceID: "par-throw-inst",
		ArchivedCompensations: func() map[string][]engine.CompensationRecord {
			m := make(map[string][]engine.CompensationRecord, n)
			for i := 1; i <= n; i++ {
				m[refName(i)] = []engine.CompensationRecord{
					{NodeID: "act" + itoa(i), Action: "cancel" + itoa(i), CompletedAt: at},
				}
			}
			return m
		}(),
	}
	r, err := engine.Step(def, st, engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	return r
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

	r := startParallelThrows(t, def, 2, at)

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
	r := startParallelThrows(t, def, 2, at)

	first := invokeActionsByName(r.Commands)
	require.Contains(t, first, "cancel1")

	_, err := engine.Step(def, r.State,
		engine.NewActionCompleted(at.Add(time.Second), first["cancel1"].CommandID, nil),
		engine.StepOptions{})
	require.False(t, errors.Is(err, engine.ErrTokenNotFound),
		"the first throw's compensation must not be orphaned (cursor overwrite bug)")
}

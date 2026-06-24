package engine_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/model"
)

// microForkDef returns a process definition without end events:
//
//	start → fork (parallel) → svc-a
//	                        → svc-b
//
// Unlike the existing parallelForkDef (which has end events after each branch),
// this simpler variant is used for Micro-mode fork tests where we only care
// about the parallel service tasks and want no end-event noise. Both svc-a and
// svc-b park awaiting InvokeAction completion.
func microForkDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "mfork", Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("start"),
			model.NewParallelGateway("fork"),
			model.NewServiceTask("svc-a", "do-a"),
			model.NewServiceTask("svc-b", "do-b"),
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "fork"},
			{ID: "f2", Source: "fork", Target: "svc-a"},
			{ID: "f3", Source: "fork", Target: "svc-b"},
		},
	}
}

// linearEndDef returns a simple linear process: start → svc → end.
// Used for the convergence test so we can drive to completion with Micro steps.
func linearEndDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "lend", Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("start"),
			model.NewServiceTask("svc", "work"),
			model.NewEndEvent("end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "svc"},
			{ID: "f2", Source: "svc", Target: "end"},
		},
	}
}

// TestMicroStepAdvancesOneNode verifies the core micro-step invariant: a
// parallel-fork process emits InvokeAction for exactly ONE branch per
// Step(Micro) call, whereas Macro emits for both branches at once.
//
// Precise "one node-advance" definition:
//   - drive runs until the FIRST token park or terminal event, then stops.
//   - Auto-advancing nodes (StartEvent, ExclusiveGateway, ParallelGateway fork)
//     do NOT count as stops; execution passes through them within the same
//     Micro drive call.
//   - Parking a ServiceTask (emitting InvokeAction, setting TokenWaitingCommand)
//     counts as ONE stop.
//
// Consequence on start→fork→(svc-a, svc-b):
//   - Micro:  start(auto)→fork(auto-fork: tokens on svc-a and svc-b)→
//     svc-a(PARKS) → stop.  One InvokeAction(do-a); svc-b token active.
//   - Macro:  same to svc-a park, then continues → svc-b(PARKS).
//     Two InvokeActions (do-a + do-b).
func TestMicroStepAdvancesOneNode(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	def := microForkDef()
	st := engine.InstanceState{InstanceID: "mi1"}

	t.Run("micro emits exactly one InvokeAction", func(t *testing.T) {
		res, err := engine.Step(def, st,
			engine.NewStartInstance(at, nil),
			engine.StepOptions{Mode: engine.Micro})
		require.NoError(t, err)

		// Exactly one InvokeAction — only the first branch was processed.
		require.Len(t, res.Commands, 1, "micro step must emit exactly one command (InvokeAction for first branch)")
		ia, ok := res.Commands[0].(engine.InvokeAction)
		require.True(t, ok, "command must be InvokeAction")
		assert.Equal(t, "do-a", ia.Name, "first branch action must be do-a")

		// Two tokens exist: svc-a parked, svc-b still active.
		require.Len(t, res.State.Tokens, 2, "two tokens must exist: parked svc-a + active svc-b")
		var svcATok, svcBTok *engine.Token
		for i := range res.State.Tokens {
			tok := &res.State.Tokens[i]
			switch tok.NodeID {
			case "svc-a":
				svcATok = tok
			case "svc-b":
				svcBTok = tok
			}
		}
		require.NotNil(t, svcATok, "token on svc-a must exist")
		require.NotNil(t, svcBTok, "token on svc-b must exist")
		assert.Equal(t, engine.TokenWaitingCommand, svcATok.State, "svc-a token must be parked")
		assert.Equal(t, engine.TokenActive, svcBTok.State, "svc-b token must still be active")

		assert.Equal(t, engine.StatusRunning, res.State.Status)
	})

	t.Run("macro emits both InvokeActions in one step", func(t *testing.T) {
		res, err := engine.Step(def, st,
			engine.NewStartInstance(at, nil),
			engine.StepOptions{Mode: engine.Macro})
		require.NoError(t, err)

		// Both branches processed: two InvokeActions.
		require.Len(t, res.Commands, 2, "macro step must emit InvokeAction for both branches")
		var actions []string
		for _, cmd := range res.Commands {
			if ia, ok := cmd.(engine.InvokeAction); ok {
				actions = append(actions, ia.Name)
			}
		}
		assert.ElementsMatch(t, []string{"do-a", "do-b"}, actions, "macro must emit InvokeAction for do-a and do-b")

		// Both tokens parked after macro.
		for _, tok := range res.State.Tokens {
			assert.Equal(t, engine.TokenWaitingCommand, tok.State,
				"all tokens must be parked after macro step, token on %q", tok.NodeID)
		}
	})

	t.Run("second micro step processes the remaining active token", func(t *testing.T) {
		// Get the state after the first micro step (svc-a parked, svc-b active).
		res1, err := engine.Step(def, st,
			engine.NewStartInstance(at, nil),
			engine.StepOptions{Mode: engine.Micro})
		require.NoError(t, err)
		require.Len(t, res1.Commands, 1)
		ia1 := res1.Commands[0].(engine.InvokeAction)

		// Deliver ActionCompleted for svc-a. The Step handler moves svc-a's token
		// forward (no outgoing flow in microForkDef, so it parks defensively), then
		// drive(Micro) picks up the still-active svc-b token and parks it, emitting
		// InvokeAction(do-b).
		res2, err := engine.Step(def, res1.State,
			engine.NewActionCompleted(at, ia1.CommandID, nil),
			engine.StepOptions{Mode: engine.Micro})
		require.NoError(t, err)

		// The second Micro step must emit InvokeAction for do-b.
		var gotDoB bool
		for _, cmd := range res2.Commands {
			if ia, ok := cmd.(engine.InvokeAction); ok && ia.Name == "do-b" {
				gotDoB = true
			}
		}
		assert.True(t, gotDoB, "second Micro step must emit InvokeAction for do-b")
	})
}

// TestMicroStepEventuallyCompletesLikeMacro verifies that driving a linear
// process entirely with Micro steps reaches the same final StatusCompleted state
// as a Macro drive.  This is the convergence invariant: Micro and Macro produce
// identical results; Micro only differs in the number of Step calls required.
//
// The test explicitly asserts that the FINAL STATE from both modes is equal on
// the meaningful fields: Status, Variables, and final token count. History length
// is also compared — both modes should produce the same number of node visits on
// a linear process. Per-step timestamps within History entries may differ between
// runs only if time.Now() is used; this engine is deterministic (all timestamps
// come from the trigger's OccurredAt) so History entry count and content match.
func TestMicroStepEventuallyCompletesLikeMacro(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	def := linearEndDef()

	var microFinal, macroFinal engine.InstanceState

	t.Run("micro converges to completed", func(t *testing.T) {
		st := engine.InstanceState{InstanceID: "conv-micro"}

		// Step 1 (Micro): StartInstance → parks at svc, emits InvokeAction.
		res1, err := engine.Step(def, st,
			engine.NewStartInstance(at, nil),
			engine.StepOptions{Mode: engine.Micro})
		require.NoError(t, err)
		require.Len(t, res1.Commands, 1)
		ia, ok := res1.Commands[0].(engine.InvokeAction)
		require.True(t, ok)
		assert.Equal(t, "work", ia.Name)
		assert.Equal(t, engine.StatusRunning, res1.State.Status)

		// Step 2 (Micro): ActionCompleted → svc token advances to end event,
		// end event fires → StatusCompleted + CompleteInstance.
		res2, err := engine.Step(def, res1.State,
			engine.NewActionCompleted(at, ia.CommandID, map[string]any{"result": "ok"}),
			engine.StepOptions{Mode: engine.Micro})
		require.NoError(t, err)
		assert.Equal(t, engine.StatusCompleted, res2.State.Status)
		require.Len(t, res2.Commands, 1)
		_, isComplete := res2.Commands[0].(engine.CompleteInstance)
		assert.True(t, isComplete, "final micro step must emit CompleteInstance")
		assert.Equal(t, "ok", res2.State.Variables["result"])

		microFinal = res2.State
	})

	t.Run("macro reaches same completed state", func(t *testing.T) {
		st := engine.InstanceState{InstanceID: "conv-macro"}

		// Macro StartInstance: also parks at svc (same as Micro for linear process).
		res1, err := engine.Step(def, st,
			engine.NewStartInstance(at, nil),
			engine.StepOptions{Mode: engine.Macro})
		require.NoError(t, err)
		require.Len(t, res1.Commands, 1)
		ia, ok := res1.Commands[0].(engine.InvokeAction)
		require.True(t, ok)
		assert.Equal(t, "work", ia.Name)

		// Macro ActionCompleted: same result.
		res2, err := engine.Step(def, res1.State,
			engine.NewActionCompleted(at, ia.CommandID, map[string]any{"result": "ok"}),
			engine.StepOptions{Mode: engine.Macro})
		require.NoError(t, err)
		assert.Equal(t, engine.StatusCompleted, res2.State.Status)
		require.Len(t, res2.Commands, 1)
		_, isComplete := res2.Commands[0].(engine.CompleteInstance)
		assert.True(t, isComplete)
		assert.Equal(t, "ok", res2.State.Variables["result"])

		macroFinal = res2.State
	})

	// ── Convergence invariant: Micro and Macro produce identical final states ──
	// These assertions are the load-bearing part of this test: they verify that
	// the two modes are semantically equivalent, not merely that each mode works
	// in isolation. We compare the fields that carry semantic meaning:
	//   - Status: both must be StatusCompleted.
	//   - Variables: both must carry {"result":"ok"} (output merged correctly).
	//   - Tokens: both must have no tokens remaining (all consumed at EndEvent).
	//   - History length: same node visits in the same count (deterministic engine).
	// We do NOT compare InstanceID (intentionally different for the two runs) or
	// EndedAt (pointer equality would fail; the value is the same time but different
	// allocations; each subtest already asserts on status/vars independently).
	t.Run("micro-final-state-equals-macro-final-state", func(t *testing.T) {
		assert.Equal(t, macroFinal.Status, microFinal.Status,
			"micro and macro must produce the same final Status")
		assert.Equal(t, macroFinal.Variables, microFinal.Variables,
			"micro and macro must produce the same final Variables")
		assert.Empty(t, microFinal.Tokens,
			"micro final state must have no remaining tokens")
		assert.Empty(t, macroFinal.Tokens,
			"macro final state must have no remaining tokens")
		assert.Equal(t, len(macroFinal.History), len(microFinal.History),
			"micro and macro must produce the same number of History node visits")
	})
}

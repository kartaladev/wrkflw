package engine_test

// step_errorend_drive_test.go — regression test for the behavior-divergence
// introduced by the nodeStrategy migration of KindErrorEndEvent.
//
// Bug: after migration, errorEndEventStrategy.enter sets tok.State =
// TokenWaitingCommand and returns, which makes drive() see stopped=true and
// continue to the NEXT active token on the NEXT loop iteration. The ORIGINAL
// switch arm executed `return cmds, nil` which exited drive() entirely.
//
// When the instance hits the immediate-failure path inside propagateError (no
// matching boundary handler + no compensation records → StatusFailed +
// FailInstance but s.Tokens NOT cleared), a surviving sibling token from a
// parallel fork that is STILL TokenActive (because the ErrorEndEvent branch
// was processed FIRST) continues to be driven — emitting a spurious
// InvokeAction on an already-Failed instance.
//
// Reproducing ordering: put the ErrorEndEvent branch FIRST in the fork's
// outgoing-flow list so forkParallel places its token before the ServiceTask
// token. firstActive() picks err-end first, propagateError fails the instance
// but leaves svc-a's token as TokenActive; drive continues and drives svc-a.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/gateway"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
)

// parallelErrorEndFirstNoHandlerDef builds a process where the ErrorEndEvent
// branch is the FIRST outgoing flow of the parallel fork, so forkParallel
// places its token before the ServiceTask token.
//
//	start → fork (parallel gateway)
//	          ├── [0] err-end (ErrorEndEvent "FATAL", no boundary handler)  ← FIRST
//	          └── [1] svc-a  (ServiceTask, parks awaiting InvokeAction)
//
// No boundary error handlers, no compensation records → propagateError takes
// the immediate-failure path: sets StatusFailed + emits FailInstance but does
// NOT clear s.Tokens.
//
// Bug behaviour: drive() continues after errorEndEventStrategy.enter returns
// (stopped=true is not a break in Macro mode), picks up svc-a (still
// TokenActive), and emits a spurious second InvokeAction("svc-a") after
// FailInstance.
//
// Fixed behaviour: errorEndEventStrategy returns halt=true, drive() exits
// immediately via `return cmds, nil`; no InvokeAction appears after
// FailInstance.
func parallelErrorEndFirstNoHandlerDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-par-errend-first", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewParallel("fork"),
			// err-end FIRST so forkParallel places its token before svc-a.
			event.NewErrorEnd("err-end", "FATAL"),
			activity.NewServiceTask("svc-a", activity.WithTaskAction("svc-a")),
			// NO boundary error handler anywhere.
		},
		Flows: []flow.SequenceFlow{
			{ID: "f-start-fork", Source: "start", Target: "fork"},
			// err-end branch is listed first → token placed first by forkParallel.
			{ID: "f-fork-err", Source: "fork", Target: "err-end"},
			{ID: "f-fork-a", Source: "fork", Target: "svc-a"},
		},
	}
}

// TestErrorEndEventHaltsDriveOnImmediateFailure is the regression test for the
// behavior-divergence bug.
//
// Before the fix (current code):
//   - drive picks err-end first (it's the first active token after forkParallel).
//   - errorEndEventStrategy.enter consumes the err-end token, calls propagateError.
//   - propagateError: no handler → StatusFailed + FailInstance, but s.Tokens still
//     contains svc-a with TokenActive.
//   - errorEndEventStrategy sets tok.State = TokenWaitingCommand, returns.
//   - drive: stopped = (TokenWaitingCommand != TokenActive) = true.
//   - Macro mode: loop does NOT break on stopped; calls firstActive() again.
//   - firstActive() finds svc-a (still TokenActive) → drives it → InvokeAction("svc-a").
//   - RESULT: InvokeAction("svc-a") appears AFTER FailInstance → BUG.
//
// After the fix:
//   - errorEndEventStrategy returns halt=true.
//   - drive: halt=true → `return cmds, nil` immediately.
//   - RESULT: no InvokeAction after FailInstance → CORRECT.
func TestErrorEndEventHaltsDriveOnImmediateFailure(t *testing.T) {
	at := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	def := parallelErrorEndFirstNoHandlerDef()

	// Single Step in Macro mode (default). The full fork drives in one call.
	res, err := engine.Step(def, engine.InstanceState{InstanceID: "i-errend-halt"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)

	// The instance MUST be Failed (propagateError took the immediate-failure path).
	assert.Equal(t, engine.StatusFailed, res.State.Status,
		"instance must be StatusFailed after unhandled ErrorEndEvent")
	require.NotNil(t, res.State.EndedAt, "EndedAt must be set on failure")

	// Locate FailInstance — exactly one must exist.
	failIdx := -1
	for i, c := range res.Commands {
		if _, ok := c.(engine.FailInstance); ok {
			require.Equal(t, -1, failIdx, "exactly one FailInstance must be emitted; found a second at index %d", i)
			failIdx = i
		}
	}
	require.NotEqual(t, -1, failIdx, "FailInstance must be emitted")

	// CRITICAL assertion: no InvokeAction may appear after FailInstance.
	// (An InvokeAction before FailInstance would mean svc-a was driven before
	// err-end, which is a different interleaving — acceptable either way.)
	for _, c := range res.Commands[failIdx+1:] {
		if _, isInvoke := c.(engine.InvokeAction); isInvoke {
			t.Errorf("spurious InvokeAction after FailInstance: %+v — drive() must exit on halt, not continue to sibling token", c)
		}
	}

	// The instance must not be Completed.
	assert.NotEqual(t, engine.StatusCompleted, res.State.Status,
		"instance must not be Completed when ErrorEndEvent fails the instance")
}

// newAPIErrorEndCaughtByBoundaryDef mirrors errorEndCaughtByBoundaryDef but
// authors the inner error end via the unified EndEvent API (ADR-0127):
// event.NewEnd(id, event.WithErrorCode("BOOM")) instead of the retired
// event.NewErrorEnd. The behavioral contract is identical: the thrown error is
// caught by the sub-process's boundary error event, the recovery flow runs, and
// the instance is NOT failed.
//
//	Root: start → sub(sp) → end-ok
//	      sp has boundary error "BOOM" → recover → end
//	Nested (sp): start → svc → end[EndError "BOOM"]
func newAPIErrorEndCaughtByBoundaryDef() *model.ProcessDefinition {
	nestedDef := &model.ProcessDefinition{
		ID: "sp-nested-newapi", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			activity.NewServiceTask("inner-svc", activity.WithTaskAction("inner-action")),
			event.NewEnd("inner-err-end", event.WithErrorCode("BOOM")),
		},
		Flows: []flow.SequenceFlow{
			{ID: "fi1", Source: "inner-start", Target: "inner-svc"},
			{ID: "fi2", Source: "inner-svc", Target: "inner-err-end"},
		},
	}

	return &model.ProcessDefinition{
		ID: "p-err-boundary-newapi", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("sp", nestedDef),
			event.NewBoundary("bnd-err", "sp", event.WithBoundaryErrorCode("BOOM")),
			activity.NewServiceTask("recover", activity.WithTaskAction("recover-action")),
			event.NewEnd("end"),
			event.NewEnd("end-ok"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f-start-sp", Source: "start", Target: "sp"},
			{ID: "f-sp-end", Source: "sp", Target: "end-ok"},
			{ID: "f-bnd-recover", Source: "bnd-err", Target: "recover"},
			{ID: "f-recover-end", Source: "recover", Target: "end"},
		},
	}
}

// TestNewAPIErrorEndCaughtByBoundary verifies that an EndEvent with
// Behavior==EndError (authored via event.WithErrorCode) throws exactly like the
// former ErrorEndEvent: the error is caught by the sub-process boundary, the
// recovery ServiceTask is invoked, and the instance is NOT failed (ADR-0127).
func TestNewAPIErrorEndCaughtByBoundary(t *testing.T) {
	def := newAPIErrorEndCaughtByBoundaryDef()
	at := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)

	// Step 1: StartInstance → enters sub-process → inner-svc parks with InvokeAction.
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i-newapi"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, r1.State.Status)

	var innerIA *engine.InvokeAction
	for _, c := range r1.Commands {
		if ia, ok := c.(engine.InvokeAction); ok {
			vv := ia
			innerIA = &vv
			break
		}
	}
	require.NotNil(t, innerIA, "expected InvokeAction for inner-action (inner-svc)")
	assert.Equal(t, "inner-action", innerIA.Name)
	require.Len(t, r1.State.Scopes, 1, "sub-process scope must be open")

	// Step 2: ActionCompleted → token reaches inner-err-end → error "BOOM" thrown
	//         → caught by boundary bnd-err on sp → recovery flow runs.
	r2, err := engine.Step(def, r1.State,
		engine.NewActionCompleted(at.Add(time.Second), innerIA.CommandID, nil), engine.StepOptions{})
	require.NoError(t, err)

	// Instance must NOT be failed — error was caught (routes to recovery, not
	// the default per-scope completion path).
	assert.Equal(t, engine.StatusRunning, r2.State.Status, "instance must still be running (error caught, not completed/failed)")
	assert.NotEqual(t, engine.StatusFailed, r2.State.Status)
	assert.Empty(t, r2.State.Scopes, "sub-process scope must be closed after error is caught")

	var recoverIA *engine.InvokeAction
	for _, c := range r2.Commands {
		if ia, ok := c.(engine.InvokeAction); ok {
			vv := ia
			recoverIA = &vv
		}
	}
	require.NotNil(t, recoverIA, "expected InvokeAction for recover-action")
	assert.Equal(t, "recover-action", recoverIA.Name)

	require.Len(t, r2.State.Tokens, 1, "exactly one token must remain (at recover)")
	assert.Equal(t, "recover", r2.State.Tokens[0].NodeID)
}

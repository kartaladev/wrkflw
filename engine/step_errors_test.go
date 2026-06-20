package engine_test

// step_errors_test.go — black-box tests for Plan 8 Task 2:
// error end events, boundary error events, scope-chain propagation,
// and ActionFailed re-routed through propagateError.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/model"
)

// ─────────────────────────────────────────────────────────────────────────────
// Definition builders
// ─────────────────────────────────────────────────────────────────────────────

// errorEndCaughtByBoundaryDef builds:
//
//	Root: start → sub(sp) → recover → end
//	      sp has boundary error "E1" → recover
//	Nested (sp): start → svc → errorEnd(E1)
func errorEndCaughtByBoundaryDef() *model.ProcessDefinition {
	nestedDef := &model.ProcessDefinition{
		ID: "sp-nested", Version: 1,
		Nodes: []model.Node{
			{ID: "inner-start", Kind: model.KindStartEvent},
			{ID: "inner-svc", Kind: model.KindServiceTask, Action: "inner-action"},
			{ID: "inner-err-end", Kind: model.KindErrorEndEvent, ErrorCode: "E1"},
		},
		Flows: []model.SequenceFlow{
			{ID: "fi1", Source: "inner-start", Target: "inner-svc"},
			{ID: "fi2", Source: "inner-svc", Target: "inner-err-end"},
		},
	}

	return &model.ProcessDefinition{
		ID: "p-err-boundary", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{
				ID:         "sp",
				Kind:       model.KindSubProcess,
				Subprocess: nestedDef,
			},
			// Boundary error event on sp, catches "E1"
			{
				ID:         "bnd-err",
				Kind:       model.KindBoundaryEvent,
				AttachedTo: "sp",
				ErrorCode:  "E1",
			},
			{ID: "recover", Kind: model.KindServiceTask, Action: "recover-action"},
			{ID: "end", Kind: model.KindEndEvent},
			{ID: "end-ok", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f-start-sp", Source: "start", Target: "sp"},
			{ID: "f-sp-end", Source: "sp", Target: "end-ok"},
			{ID: "f-bnd-recover", Source: "bnd-err", Target: "recover"},
			{ID: "f-recover-end", Source: "recover", Target: "end"},
		},
	}
}

// unhandledErrorDef builds:
//
//	Root: start → errorEnd(E2)
//
// No boundary error handler → should fail instance.
func unhandledErrorDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-unhandled-err", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "svc", Kind: model.KindServiceTask, Action: "svc-action"},
			{ID: "err-end", Kind: model.KindErrorEndEvent, ErrorCode: "E2"},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "svc"},
			{ID: "f2", Source: "svc", Target: "err-end"},
		},
	}
}

// unhandledErrorInSubprocessDef builds:
//
//	Root: start → sub(sp) → end
//	Nested (sp): start → errorEnd(E3)
//	No boundary error handler on sp → fails instance.
func unhandledErrorInSubprocessDef() *model.ProcessDefinition {
	nestedDef := &model.ProcessDefinition{
		ID: "sp-nested-nohandler", Version: 1,
		Nodes: []model.Node{
			{ID: "inner-start", Kind: model.KindStartEvent},
			{ID: "inner-err-end", Kind: model.KindErrorEndEvent, ErrorCode: "E3"},
		},
		Flows: []model.SequenceFlow{
			{ID: "fi1", Source: "inner-start", Target: "inner-err-end"},
		},
	}

	return &model.ProcessDefinition{
		ID: "p-unhandled-err-sp", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "sp", Kind: model.KindSubProcess, Subprocess: nestedDef},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f-start-sp", Source: "start", Target: "sp"},
			{ID: "f-sp-end", Source: "sp", Target: "end"},
		},
	}
}

// actionFailedBoundaryDef builds:
//
//	Root: start → sub(sp) → end-ok
//	      sp has boundary error "ACT_ERR" (catch-all: ErrorCode=="") → recover → end
//	Nested (sp): start → svc("work") → end-inner
//
// When svc fails with ActionFailed, the error propagates to the boundary error
// handler on the sub-process → recovery path is taken.
func actionFailedBoundaryDef() *model.ProcessDefinition {
	nestedDef := &model.ProcessDefinition{
		ID: "sp-af-nested", Version: 1,
		Nodes: []model.Node{
			{ID: "inner-start", Kind: model.KindStartEvent},
			{ID: "inner-svc", Kind: model.KindServiceTask, Action: "work-action"},
			{ID: "inner-end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "fi1", Source: "inner-start", Target: "inner-svc"},
			{ID: "fi2", Source: "inner-svc", Target: "inner-end"},
		},
	}

	return &model.ProcessDefinition{
		ID: "p-af-boundary", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "sp", Kind: model.KindSubProcess, Subprocess: nestedDef},
			// Catch-all boundary error (ErrorCode == "") catches any error thrown from sp
			{ID: "bnd-err", Kind: model.KindBoundaryEvent, AttachedTo: "sp", ErrorCode: ""},
			{ID: "recover", Kind: model.KindServiceTask, Action: "recover-action"},
			{ID: "end", Kind: model.KindEndEvent},
			{ID: "end-ok", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f-start-sp", Source: "start", Target: "sp"},
			{ID: "f-sp-end", Source: "sp", Target: "end-ok"},
			{ID: "f-bnd-recover", Source: "bnd-err", Target: "recover"},
			{ID: "f-recover-end", Source: "recover", Target: "end"},
		},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Tests
// ─────────────────────────────────────────────────────────────────────────────

// TestErrorEndCaughtByBoundary verifies that a KindErrorEndEvent inside a
// sub-process is caught by a boundary error event on the sub-process activity.
// The recovery path must run (InvokeAction("recover-action")), the sub-process
// scope's tokens must be cancelled (scope closed), and the instance must NOT
// be set to StatusFailed.
func TestErrorEndCaughtByBoundary(t *testing.T) {
	def := errorEndCaughtByBoundaryDef()
	at := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)

	// Step 1: StartInstance → enters sub-process → inner-svc parks with InvokeAction.
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, r1.State.Status)

	// Locate the InvokeAction for inner-svc.
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

	// The sub-process scope must be open.
	require.Len(t, r1.State.Scopes, 1, "sub-process scope must be open")

	// Step 2: ActionCompleted for inner-svc → token moves to inner-err-end →
	//         error "E1" is thrown → caught by boundary bnd-err on sp →
	//         scope cancelled, token placed on recover.
	r2, err := engine.Step(def, r1.State,
		engine.NewActionCompleted(at.Add(time.Second), innerIA.CommandID, nil), engine.StepOptions{})
	require.NoError(t, err)

	// Instance must NOT be failed — error was caught.
	assert.Equal(t, engine.StatusRunning, r2.State.Status, "instance must still be running (error caught)")

	// The sub-process scope must be closed.
	assert.Empty(t, r2.State.Scopes, "sub-process scope must be closed after error is caught")

	// The recovery action must be invoked.
	var recoverIA *engine.InvokeAction
	for _, c := range r2.Commands {
		if ia, ok := c.(engine.InvokeAction); ok {
			vv := ia
			recoverIA = &vv
		}
	}
	require.NotNil(t, recoverIA, "expected InvokeAction for recover-action")
	assert.Equal(t, "recover-action", recoverIA.Name)

	// The token must be parked at recover (waiting for recover-action).
	require.Len(t, r2.State.Tokens, 1, "exactly one token must remain (at recover)")
	assert.Equal(t, "recover", r2.State.Tokens[0].NodeID)
}

// TestUnhandledErrorFailsInstance verifies that an error thrown with no
// matching boundary error handler (neither at the scope level nor up the
// scope chain to root) causes StatusFailed + FailInstance.
func TestUnhandledErrorFailsInstance(t *testing.T) {
	tests := []struct {
		name      string
		buildDef  func() *model.ProcessDefinition
		stepsToErr func(def *model.ProcessDefinition, at time.Time) (engine.InstanceState, string, error)
		errorCode string
	}{
		{
			name:      "root-level error end with no handler",
			errorCode: "E2",
			buildDef:  unhandledErrorDef,
			stepsToErr: func(def *model.ProcessDefinition, at time.Time) (engine.InstanceState, string, error) {
				// Start → svc parks
				r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
					engine.NewStartInstance(at, nil), engine.StepOptions{})
				if err != nil {
					return engine.InstanceState{}, "", err
				}
				var ia engine.InvokeAction
				for _, c := range r1.Commands {
					if v, ok := c.(engine.InvokeAction); ok {
						ia = v
						break
					}
				}
				// svc completes → errorEnd fires → propagate → fail
				r2, err := engine.Step(def, r1.State,
					engine.NewActionCompleted(at.Add(time.Second), ia.CommandID, nil), engine.StepOptions{})
				return r2.State, "", err
			},
		},
		{
			name:      "sub-process error end with no boundary handler",
			errorCode: "E3",
			buildDef:  unhandledErrorInSubprocessDef,
			stepsToErr: func(def *model.ProcessDefinition, at time.Time) (engine.InstanceState, string, error) {
				// Start → inner-start → inner-err-end all in one drive (no service task)
				r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
					engine.NewStartInstance(at, nil), engine.StepOptions{})
				return r1.State, "", err
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			at := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
			def := tc.buildDef()
			st, _, err := tc.stepsToErr(def, at)
			require.NoError(t, err)

			assert.Equal(t, engine.StatusFailed, st.Status, "unhandled error must set StatusFailed")
			require.NotNil(t, st.EndedAt, "EndedAt must be set on failure")
		})
	}
}

// TestActionFailedPropagatesToBoundaryError verifies two complementary behaviors:
//  1. A ServiceTask ActionFailed INSIDE a sub-process that has a catch-all
//     boundary error handler → error is caught, recovery path runs, NOT FailInstance.
//  2. A root-level ServiceTask ActionFailed with NO boundary error handler →
//     still produces FailInstance (existing behavior preserved).
func TestActionFailedPropagatesToBoundaryError(t *testing.T) {
	at := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)

	t.Run("action-failed-inside-subprocess-caught-by-boundary", func(t *testing.T) {
		def := actionFailedBoundaryDef()

		// Step 1: Start → enters sub-process → inner-svc parks.
		r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
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
		require.NotNil(t, innerIA, "expected InvokeAction for work-action")

		// Step 2: ActionFailed for inner-svc → propagateError → catch-all boundary catches →
		//         recovery path (recover-action invoked).
		r2, err := engine.Step(def, r1.State,
			engine.NewActionFailed(at.Add(time.Second), innerIA.CommandID, "svc-err", false), engine.StepOptions{})
		require.NoError(t, err)

		// Must NOT fail the instance.
		assert.Equal(t, engine.StatusRunning, r2.State.Status, "error must be caught — instance should still run")

		// Must NOT emit FailInstance.
		for _, c := range r2.Commands {
			if _, ok := c.(engine.FailInstance); ok {
				t.Fatal("FailInstance must NOT be emitted when error is caught by boundary")
			}
		}

		// Recovery action must be invoked.
		var recoverIA *engine.InvokeAction
		for _, c := range r2.Commands {
			if ia, ok := c.(engine.InvokeAction); ok {
				vv := ia
				recoverIA = &vv
			}
		}
		require.NotNil(t, recoverIA, "expected InvokeAction for recover-action")
		assert.Equal(t, "recover-action", recoverIA.Name)

		// Sub-process scope must be closed.
		assert.Empty(t, r2.State.Scopes, "sub-process scope must be closed after boundary fires")
	})

	t.Run("action-failed-root-level-still-fails-instance", func(t *testing.T) {
		// Reuse the standard linear definition (no boundary error handler on svc).
		def := linearDef()

		r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
			engine.NewStartInstance(at, nil), engine.StepOptions{})
		require.NoError(t, err)
		cmdID := r1.Commands[0].(engine.InvokeAction).CommandID

		r2, err := engine.Step(def, r1.State,
			engine.NewActionFailed(at.Add(time.Second), cmdID, "boom", false), engine.StepOptions{})
		require.NoError(t, err)

		assert.Equal(t, engine.StatusFailed, r2.State.Status, "root-level ActionFailed with no boundary → StatusFailed")

		var fi *engine.FailInstance
		for _, c := range r2.Commands {
			if v, ok := c.(engine.FailInstance); ok {
				vv := v
				fi = &vv
				break
			}
		}
		require.NotNil(t, fi, "FailInstance must be emitted for unhandled root-level ActionFailed")
		assert.Equal(t, "boom", fi.Err)
	})
}

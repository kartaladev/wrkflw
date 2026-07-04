package engine_test

// step_errors_test.go — black-box tests for Plan 8 Task 2:
// error end events, boundary error events, scope-chain propagation,
// and ActionFailed re-routed through propagateError.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/engine"
)

// ─────────────────────────────────────────────────────────────────────────────
// Definition builders
// ─────────────────────────────────────────────────────────────────────────────

// errorEndCaughtByBoundaryDef builds:
//
//	Root: start → sub(sp) → recover → end
//	      sp has boundary error "E1" → recover
//	Nested (sp): start → svc → errorEnd(E1)
func errorEndCaughtByBoundaryDef() *definition.ProcessDefinition {
	nestedDef := &definition.ProcessDefinition{
		ID: "sp-nested", Version: 1,
		Nodes: []definition.Node{
			event.NewStart("inner-start"),
			activity.NewServiceTask("inner-svc", activity.WithActionName("inner-action")),
			event.NewErrorEnd("inner-err-end", "E1"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "fi1", Source: "inner-start", Target: "inner-svc"},
			{ID: "fi2", Source: "inner-svc", Target: "inner-err-end"},
		},
	}

	return &definition.ProcessDefinition{
		ID: "p-err-boundary", Version: 1,
		Nodes: []definition.Node{
			event.NewStart("start"),
			activity.NewSubProcess("sp", nestedDef),
			// Boundary error event on sp, catches "E1"
			event.NewBoundary("bnd-err", "sp", event.WithBoundaryErrorCode("E1")),
			activity.NewServiceTask("recover", activity.WithActionName("recover-action")),
			event.NewEnd("end"),
			event.NewEnd("end-ok"),
		},
		Flows: []definition.SequenceFlow{
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
func unhandledErrorDef() *definition.ProcessDefinition {
	return &definition.ProcessDefinition{
		ID: "p-unhandled-err", Version: 1,
		Nodes: []definition.Node{
			event.NewStart("start"),
			activity.NewServiceTask("svc", activity.WithActionName("svc-action")),
			event.NewErrorEnd("err-end", "E2"),
		},
		Flows: []definition.SequenceFlow{
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
func unhandledErrorInSubprocessDef() *definition.ProcessDefinition {
	nestedDef := &definition.ProcessDefinition{
		ID: "sp-nested-nohandler", Version: 1,
		Nodes: []definition.Node{
			event.NewStart("inner-start"),
			event.NewErrorEnd("inner-err-end", "E3"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "fi1", Source: "inner-start", Target: "inner-err-end"},
		},
	}

	return &definition.ProcessDefinition{
		ID: "p-unhandled-err-sp", Version: 1,
		Nodes: []definition.Node{
			event.NewStart("start"),
			activity.NewSubProcess("sp", nestedDef),
			event.NewEnd("end"),
		},
		Flows: []definition.SequenceFlow{
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
func actionFailedBoundaryDef() *definition.ProcessDefinition {
	nestedDef := &definition.ProcessDefinition{
		ID: "sp-af-nested", Version: 1,
		Nodes: []definition.Node{
			event.NewStart("inner-start"),
			activity.NewServiceTask("inner-svc", activity.WithActionName("work-action")),
			event.NewEnd("inner-end"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "fi1", Source: "inner-start", Target: "inner-svc"},
			{ID: "fi2", Source: "inner-svc", Target: "inner-end"},
		},
	}

	return &definition.ProcessDefinition{
		ID: "p-af-boundary", Version: 1,
		Nodes: []definition.Node{
			event.NewStart("start"),
			activity.NewSubProcess("sp", nestedDef),
			// Catch-all boundary error (ErrorCode == "") catches any error thrown from sp
			event.NewBoundary("bnd-err", "sp", event.WithBoundaryErrorCode("")),
			activity.NewServiceTask("recover", activity.WithActionName("recover-action")),
			event.NewEnd("end"),
			event.NewEnd("end-ok"),
		},
		Flows: []definition.SequenceFlow{
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
		name       string
		buildDef   func() *definition.ProcessDefinition
		stepsToErr func(def *definition.ProcessDefinition, at time.Time) (engine.InstanceState, string, error)
		errorCode  string
	}{
		{
			name:      "root-level error end with no handler",
			errorCode: "E2",
			buildDef:  unhandledErrorDef,
			stepsToErr: func(def *definition.ProcessDefinition, at time.Time) (engine.InstanceState, string, error) {
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
			stepsToErr: func(def *definition.ProcessDefinition, at time.Time) (engine.InstanceState, string, error) {
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

// directBoundaryOnRootSvcDef builds:
//
//	Root: start → svc → end
//	      svc has a boundary error event "E1" → recover → end-recover
//
// When svc's ActionFailed fires (errorCode="E1"), the boundary attached directly to
// svc (at root level, no sub-process scope) should catch the error, route to
// recover, and NOT fail the instance.
func directBoundaryOnRootSvcDef() *definition.ProcessDefinition {
	return &definition.ProcessDefinition{
		ID: "p-direct-bnd", Version: 1,
		Nodes: []definition.Node{
			event.NewStart("start"),
			activity.NewServiceTask("svc", activity.WithActionName("svc-action")),
			// Direct boundary error event on svc (specific error code "E1")
			event.NewBoundary("bnd-svc-err", "svc", event.WithBoundaryErrorCode("E1")),
			activity.NewServiceTask("recover", activity.WithActionName("recover-action")),
			event.NewEnd("end"),
			event.NewEnd("end-recover"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "f-start-svc", Source: "start", Target: "svc"},
			{ID: "f-svc-end", Source: "svc", Target: "end"},
			{ID: "f-bnd-recover", Source: "bnd-svc-err", Target: "recover"},
			{ID: "f-recover-end", Source: "recover", Target: "end-recover"},
		},
	}
}

// directBoundaryCatchAllOnRootSvcDef builds:
//
//	Root: start → svc → end
//	      svc has a catch-all boundary error event (ErrorCode=="") → recover → end-recover
func directBoundaryCatchAllOnRootSvcDef() *definition.ProcessDefinition {
	return &definition.ProcessDefinition{
		ID: "p-direct-bnd-catchall", Version: 1,
		Nodes: []definition.Node{
			event.NewStart("start"),
			activity.NewServiceTask("svc", activity.WithActionName("svc-action")),
			// Catch-all boundary error event on svc
			event.NewBoundary("bnd-svc-err", "svc", event.WithBoundaryErrorCode("")),
			activity.NewServiceTask("recover", activity.WithActionName("recover-action")),
			event.NewEnd("end"),
			event.NewEnd("end-recover"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "f-start-svc", Source: "start", Target: "svc"},
			{ID: "f-svc-end", Source: "svc", Target: "end"},
			{ID: "f-bnd-recover", Source: "bnd-svc-err", Target: "recover"},
			{ID: "f-recover-end", Source: "recover", Target: "end-recover"},
		},
	}
}

// directBoundaryOnInnerSvcInSubprocessDef builds:
//
//	Root: start → sub(sp) → end
//	Nested (sp): start → inner-svc → end-inner
//	             inner-svc has a boundary error event "INNER_ERR" → inner-recover → end-recover
//
// When inner-svc's ActionFailed fires, the boundary attached DIRECTLY to inner-svc
// (inside the sub-process scope) should catch the error. Only inner-svc's token is
// consumed; the sub-process scope stays open with inner-recover running.
func directBoundaryOnInnerSvcInSubprocessDef() *definition.ProcessDefinition {
	nestedDef := &definition.ProcessDefinition{
		ID: "sp-direct-bnd-inner", Version: 1,
		Nodes: []definition.Node{
			event.NewStart("inner-start"),
			activity.NewServiceTask("inner-svc", activity.WithActionName("inner-action")),
			// Boundary attached directly to inner-svc (specific error code)
			event.NewBoundary("inner-bnd", "inner-svc", event.WithBoundaryErrorCode("INNER_ERR")),
			activity.NewServiceTask("inner-recover", activity.WithActionName("inner-recover-action")),
			event.NewEnd("inner-end"),
			event.NewEnd("inner-end-recover"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "fi-start-svc", Source: "inner-start", Target: "inner-svc"},
			{ID: "fi-svc-end", Source: "inner-svc", Target: "inner-end"},
			{ID: "fi-bnd-recover", Source: "inner-bnd", Target: "inner-recover"},
			{ID: "fi-recover-end", Source: "inner-recover", Target: "inner-end-recover"},
		},
	}

	return &definition.ProcessDefinition{
		ID: "p-direct-bnd-inner", Version: 1,
		Nodes: []definition.Node{
			event.NewStart("start"),
			activity.NewSubProcess("sp", nestedDef),
			event.NewEnd("end"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "f-start-sp", Source: "start", Target: "sp"},
			{ID: "f-sp-end", Source: "sp", Target: "end"},
		},
	}
}

// TestActionFailedCaughtByDirectBoundary verifies that a KindBoundaryEvent error
// event attached DIRECTLY to the failing activity (not just to an enclosing
// sub-process) is matched when ActionFailed fires, even when the failing token is
// at root scope (ScopeID == "").
//
// This is the critical regression test for Fix 1: previously propagateError only
// walked ENCLOSING SCOPES, so a root-level service task with a direct boundary
// would wrongly FailInstance instead of routing to the recovery path.
func TestActionFailedCaughtByDirectBoundary(t *testing.T) {
	at := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)

	t.Run("root-level-svc-specific-error-code", func(t *testing.T) {
		def := directBoundaryOnRootSvcDef()

		// Step 1: Start → svc parks with InvokeAction.
		r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
			engine.NewStartInstance(at, nil), engine.StepOptions{})
		require.NoError(t, err)
		require.Equal(t, engine.StatusRunning, r1.State.Status)

		var ia *engine.InvokeAction
		for _, c := range r1.Commands {
			if v, ok := c.(engine.InvokeAction); ok {
				vv := v
				ia = &vv
				break
			}
		}
		require.NotNil(t, ia, "expected InvokeAction for svc-action")
		assert.Equal(t, "svc-action", ia.Name)

		// Step 2: ActionFailed with errorCode matching boundary's ErrorCode "E1" →
		// boundary on svc should catch it → recover-action invoked → NOT FailInstance.
		r2, err := engine.Step(def, r1.State,
			engine.NewActionFailed(at.Add(time.Second), ia.CommandID, "E1", false), engine.StepOptions{})
		require.NoError(t, err)

		// Instance must NOT fail — direct boundary catches the error.
		assert.Equal(t, engine.StatusRunning, r2.State.Status, "instance must still be running (direct boundary caught error)")
		assert.Nil(t, r2.State.EndedAt, "EndedAt must NOT be set when error is caught")

		// Must NOT emit FailInstance.
		for _, c := range r2.Commands {
			if _, ok := c.(engine.FailInstance); ok {
				t.Fatal("FailInstance must NOT be emitted when direct boundary catches the error")
			}
		}

		// Recovery action must be invoked.
		var recoverIA *engine.InvokeAction
		for _, c := range r2.Commands {
			if v, ok := c.(engine.InvokeAction); ok {
				vv := v
				recoverIA = &vv
			}
		}
		require.NotNil(t, recoverIA, "expected InvokeAction for recover-action")
		assert.Equal(t, "recover-action", recoverIA.Name)

		// Exactly one token must remain — parked at recover.
		require.Len(t, r2.State.Tokens, 1, "exactly one token must remain (at recover)")
		assert.Equal(t, "recover", r2.State.Tokens[0].NodeID)
		// Token must be in root scope (same scope as the failing svc).
		assert.Equal(t, "", r2.State.Tokens[0].ScopeID, "recovery token must be in root scope")
	})

	t.Run("root-level-svc-catch-all-boundary", func(t *testing.T) {
		def := directBoundaryCatchAllOnRootSvcDef()

		r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
			engine.NewStartInstance(at, nil), engine.StepOptions{})
		require.NoError(t, err)

		var ia *engine.InvokeAction
		for _, c := range r1.Commands {
			if v, ok := c.(engine.InvokeAction); ok {
				vv := v
				ia = &vv
				break
			}
		}
		require.NotNil(t, ia)

		// ActionFailed with any error code — catch-all boundary should catch it.
		r2, err := engine.Step(def, r1.State,
			engine.NewActionFailed(at.Add(time.Second), ia.CommandID, "any-error", false), engine.StepOptions{})
		require.NoError(t, err)

		assert.Equal(t, engine.StatusRunning, r2.State.Status, "catch-all direct boundary must catch any error")

		for _, c := range r2.Commands {
			if _, ok := c.(engine.FailInstance); ok {
				t.Fatal("FailInstance must NOT be emitted when catch-all direct boundary catches the error")
			}
		}

		var recoverIA *engine.InvokeAction
		for _, c := range r2.Commands {
			if v, ok := c.(engine.InvokeAction); ok {
				vv := v
				recoverIA = &vv
			}
		}
		require.NotNil(t, recoverIA, "expected InvokeAction for recover-action")
		assert.Equal(t, "recover-action", recoverIA.Name)
	})

	t.Run("root-level-svc-error-code-mismatch-still-fails", func(t *testing.T) {
		// Boundary has ErrorCode "E1" but ActionFailed throws "OTHER" → no match → FailInstance.
		def := directBoundaryOnRootSvcDef()

		r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
			engine.NewStartInstance(at, nil), engine.StepOptions{})
		require.NoError(t, err)

		var ia *engine.InvokeAction
		for _, c := range r1.Commands {
			if v, ok := c.(engine.InvokeAction); ok {
				vv := v
				ia = &vv
				break
			}
		}
		require.NotNil(t, ia)

		// ActionFailed with error code that does NOT match the boundary's "E1".
		r2, err := engine.Step(def, r1.State,
			engine.NewActionFailed(at.Add(time.Second), ia.CommandID, "OTHER", false), engine.StepOptions{})
		require.NoError(t, err)

		assert.Equal(t, engine.StatusFailed, r2.State.Status, "mismatched error code must still fail instance")

		var fi *engine.FailInstance
		for _, c := range r2.Commands {
			if v, ok := c.(engine.FailInstance); ok {
				vv := v
				fi = &vv
				break
			}
		}
		require.NotNil(t, fi, "FailInstance must be emitted when error code doesn't match boundary")
		assert.Equal(t, "OTHER", fi.Err)
	})

	t.Run("direct-boundary-on-inner-svc-inside-subprocess", func(t *testing.T) {
		// A boundary attached to inner-svc INSIDE a sub-process is a direct-attachment
		// check on the token's own scope def. The sub-process scope stays open after
		// the direct boundary fires (only inner-svc's token is cancelled).
		def := directBoundaryOnInnerSvcInSubprocessDef()

		r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
			engine.NewStartInstance(at, nil), engine.StepOptions{})
		require.NoError(t, err)
		require.Equal(t, engine.StatusRunning, r1.State.Status)
		// Sub-process scope must be open.
		require.Len(t, r1.State.Scopes, 1, "sub-process scope must be open")

		var ia *engine.InvokeAction
		for _, c := range r1.Commands {
			if v, ok := c.(engine.InvokeAction); ok {
				vv := v
				ia = &vv
				break
			}
		}
		require.NotNil(t, ia, "expected InvokeAction for inner-action")
		assert.Equal(t, "inner-action", ia.Name)

		// ActionFailed with matching error code → direct boundary on inner-svc catches it.
		r2, err := engine.Step(def, r1.State,
			engine.NewActionFailed(at.Add(time.Second), ia.CommandID, "INNER_ERR", false), engine.StepOptions{})
		require.NoError(t, err)

		// Instance must NOT fail.
		assert.Equal(t, engine.StatusRunning, r2.State.Status, "direct boundary on inner-svc must catch error")

		for _, c := range r2.Commands {
			if _, ok := c.(engine.FailInstance); ok {
				t.Fatal("FailInstance must NOT be emitted when direct boundary on inner-svc catches error")
			}
		}

		// Recovery action must be invoked.
		var recoverIA *engine.InvokeAction
		for _, c := range r2.Commands {
			if v, ok := c.(engine.InvokeAction); ok {
				vv := v
				recoverIA = &vv
			}
		}
		require.NotNil(t, recoverIA, "expected InvokeAction for inner-recover-action")
		assert.Equal(t, "inner-recover-action", recoverIA.Name)

		// The sub-process scope must still be open (only inner-svc's token was consumed).
		assert.Len(t, r2.State.Scopes, 1, "sub-process scope must remain open (only svc token cancelled, not whole scope)")
	})
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

// ─────────────────────────────────────────────────────────────────────────────
// Task 4: CancelRequested — cancel instance with timer/await cleanup
// ─────────────────────────────────────────────────────────────────────────────

// cancelWithTimerDef builds a process whose single service task has a boundary
// timer so that when the instance is cancelled, both the parked token and an
// armed timer are in flight.
//
//	Root: start → svc(Action:"work") → end
//	      svc has a timer boundary (30s) → timeout-path → end-timeout
func cancelWithTimerDef() *definition.ProcessDefinition {
	return &definition.ProcessDefinition{
		ID: "p-cancel", Version: 1,
		Nodes: []definition.Node{
			event.NewStart("start"),
			activity.NewServiceTask("svc", activity.WithActionName("work")),
			// Timer boundary on svc (30-second deadline).
			event.NewBoundary("bnd-timer", "svc", event.WithBoundaryTimer(`"30s"`)),
			event.NewEnd("timeout-end"),
			event.NewEnd("end"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "f-start-svc", Source: "start", Target: "svc"},
			{ID: "f-svc-end", Source: "svc", Target: "end"},
			{ID: "f-bnd-end", Source: "bnd-timer", Target: "timeout-end"},
		},
	}
}

// cancelUserTaskDef builds a process with a user task parked and waiting for a
// human actor. Used to verify CancelRequested clears all tokens without a timer
// arm (simpler scenario).
//
//	Root: start → userTask → end
func cancelUserTaskDef() *definition.ProcessDefinition {
	return &definition.ProcessDefinition{
		ID: "p-cancel-ut", Version: 1,
		Nodes: []definition.Node{
			event.NewStart("start"),
			activity.NewUserTask("userTask", nil),
			event.NewEnd("end"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "f1", Source: "start", Target: "userTask"},
			{ID: "f2", Source: "userTask", Target: "end"},
		},
	}
}

// TestCancelRequestedTerminates verifies that CancelRequested terminates a
// running instance:
//   - All tokens are consumed (s.Tokens empty).
//   - Status becomes StatusTerminated.
//   - EndedAt is set to the trigger's OccurredAt.
//   - A terminal FailInstance{Err:"cancelled"} command is emitted.
//   - Any outstanding armed timers receive a CancelTimer command.
//   - A late trigger after cancellation (HumanCompleted) returns ErrTokenNotFound
//     (clean deterministic error, not a panic).
func TestCancelRequestedTerminates(t *testing.T) {
	at := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	cancelAt := at.Add(5 * time.Second)

	t.Run("service-task-with-boundary-timer", func(t *testing.T) {
		def := cancelWithTimerDef()

		// Start the instance: start → svc parks, boundary timer armed.
		r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i-cancel-1"},
			engine.NewStartInstance(at, nil), engine.StepOptions{})
		require.NoError(t, err)
		require.Equal(t, engine.StatusRunning, r1.State.Status)

		// Collect the InvokeAction command ID.
		var invokeCmd *engine.InvokeAction
		for _, c := range r1.Commands {
			if ia, ok := c.(engine.InvokeAction); ok {
				vv := ia
				invokeCmd = &vv
				break
			}
		}
		require.NotNil(t, invokeCmd, "expected InvokeAction for work")

		// Collect armed boundary timer.
		var scheduleTimer *engine.ScheduleTimer
		for _, c := range r1.Commands {
			if st, ok := c.(engine.ScheduleTimer); ok {
				vv := st
				scheduleTimer = &vv
				break
			}
		}
		require.NotNil(t, scheduleTimer, "expected ScheduleTimer for boundary timer")

		// One token must be parked at svc, one Boundaries arm must exist.
		require.Len(t, r1.State.Tokens, 1)
		assert.Equal(t, "svc", r1.State.Tokens[0].NodeID)

		// CancelRequested: should terminate the instance.
		r2, err := engine.Step(def, r1.State,
			engine.NewCancelRequested(cancelAt), engine.StepOptions{})
		require.NoError(t, err)

		// Status must be StatusTerminated.
		assert.Equal(t, engine.StatusTerminated, r2.State.Status, "CancelRequested must set StatusTerminated")

		// EndedAt must be set to cancelAt.
		require.NotNil(t, r2.State.EndedAt, "EndedAt must be set on cancellation")
		assert.Equal(t, cancelAt, *r2.State.EndedAt, "EndedAt must equal the trigger's OccurredAt")

		// All tokens must be cleared.
		assert.Empty(t, r2.State.Tokens, "all tokens must be consumed on cancellation")

		// Terminal FailInstance{Err:"cancelled"} must be emitted.
		var fi *engine.FailInstance
		for _, c := range r2.Commands {
			if v, ok := c.(engine.FailInstance); ok {
				vv := v
				fi = &vv
				break
			}
		}
		require.NotNil(t, fi, "FailInstance must be emitted on cancel")
		assert.Equal(t, "cancelled", fi.Err, "FailInstance.Err must be 'cancelled'")

		// CancelTimer must be emitted for the boundary timer.
		var cancelTimer *engine.CancelTimer
		for _, c := range r2.Commands {
			if ct, ok := c.(engine.CancelTimer); ok {
				if ct.TimerID == scheduleTimer.TimerID {
					vv := ct
					cancelTimer = &vv
					break
				}
			}
		}
		require.NotNil(t, cancelTimer, "CancelTimer must be emitted for the outstanding boundary timer")

		// Late trigger after cancellation: ActionCompleted for the already-cancelled
		// token → ErrTokenNotFound (deterministic, no panic).
		_, lateErr := engine.Step(def, r2.State,
			engine.NewActionCompleted(cancelAt.Add(time.Second), invokeCmd.CommandID, nil), engine.StepOptions{})
		require.Error(t, lateErr, "a late ActionCompleted after cancel must return an error")
		assert.ErrorIs(t, lateErr, engine.ErrTokenNotFound, "late trigger must return ErrTokenNotFound")
		assert.ErrorIs(t, lateErr, engine.ErrInvalidTransition,
			"a late/wrong-state trigger is classifiable as an invalid transition")
	})

	t.Run("user-task-parked", func(t *testing.T) {
		def := cancelUserTaskDef()

		// Start the instance: start → userTask parks.
		r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i-cancel-2"},
			engine.NewStartInstance(at, nil), engine.StepOptions{})
		require.NoError(t, err)
		require.Equal(t, engine.StatusRunning, r1.State.Status)

		// One token parked at userTask.
		require.Len(t, r1.State.Tokens, 1)
		assert.Equal(t, "userTask", r1.State.Tokens[0].NodeID)

		// Collect task token for late-trigger test.
		taskToken := r1.State.Tokens[0].AwaitCommand
		require.NotEmpty(t, taskToken, "userTask token must have AwaitCommand set")

		// CancelRequested.
		r2, err := engine.Step(def, r1.State,
			engine.NewCancelRequested(cancelAt), engine.StepOptions{})
		require.NoError(t, err)

		assert.Equal(t, engine.StatusTerminated, r2.State.Status)
		require.NotNil(t, r2.State.EndedAt)
		assert.Equal(t, cancelAt, *r2.State.EndedAt)
		assert.Empty(t, r2.State.Tokens)

		// FailInstance{Err:"cancelled"} must be emitted.
		var fi *engine.FailInstance
		for _, c := range r2.Commands {
			if v, ok := c.(engine.FailInstance); ok {
				vv := v
				fi = &vv
				break
			}
		}
		require.NotNil(t, fi)
		assert.Equal(t, "cancelled", fi.Err)

		// Late trigger: HumanCompleted after cancel → ErrTokenNotFound.
		_, lateErr := engine.Step(def, r2.State,
			engine.NewHumanCompleted(cancelAt.Add(time.Second), taskToken, nil,
				authz.Actor{ID: "u1"}), engine.StepOptions{})
		require.Error(t, lateErr)
		assert.ErrorIs(t, lateErr, engine.ErrTokenNotFound)
	})

	t.Run("already-terminal-is-noop-or-error", func(t *testing.T) {
		// Cancelling an already-completed instance: CancelRequested is handled by
		// the CancelRequested case in Step regardless of current Status. The logic
		// is idempotent — there are no tokens or timers to cancel — so it succeeds
		// (NoError) and overwrites Status to StatusTerminated. The resulting state
		// has empty tokens and no harmful side effects.
		//
		// Callers must NOT send CancelRequested to an already-terminal instance in
		// production; this subtest only ensures no panic and deterministic behaviour.
		def := cancelUserTaskDef()
		completedState := engine.InstanceState{
			InstanceID: "i-cancel-term",
			Status:     engine.StatusCompleted,
		}
		// Should not panic; the result is deterministic.
		r, err := engine.Step(def, completedState,
			engine.NewCancelRequested(cancelAt), engine.StepOptions{})
		require.NoError(t, err, "CancelRequested on a completed instance must not error (idempotent)")
		// Post-cancel: status is terminal (Terminated), tokens still empty.
		assert.Equal(t, engine.StatusTerminated, r.State.Status)
		assert.Empty(t, r.State.Tokens)
	})
}

// TestCompensateRequestedUnknownToNodeErrors verifies that when CompensateRequested
// specifies a ToNode that does not match any compensation record in scope,
// the engine returns a descriptive error rather than silently rolling back everything.
//
// This is the Task-3 review fix folded into Task 4.
func TestCompensateRequestedUnknownToNodeErrors(t *testing.T) {
	// Build a simple process with one compensable service task.
	def := &definition.ProcessDefinition{
		ID: "p-comp-unknown", Version: 1,
		Nodes: []definition.Node{
			event.NewStart("start"),
			activity.NewServiceTask("svc", activity.WithActionName("charge"), activity.WithCompensation("refund")),
			activity.NewUserTask("userTask", nil),
			event.NewEnd("end"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "f1", Source: "start", Target: "svc"},
			{ID: "f2", Source: "svc", Target: "userTask"},
			{ID: "f3", Source: "userTask", Target: "end"},
		},
	}

	at := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)

	// Start → svc parks with InvokeAction.
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i-comp-unk"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	ia := r1.Commands[0].(engine.InvokeAction)

	// Complete svc → userTask parks. Compensation record for "svc" is recorded.
	r2, err := engine.Step(def, r1.State,
		engine.NewActionCompleted(at.Add(time.Second), ia.CommandID, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, r2.State.Status)
	require.Len(t, r2.State.RootCompensations, 1, "one compensation record for svc")

	// CompensateRequested with a ToNode that is NOT in the compensation records.
	_, err = engine.Step(def, r2.State,
		engine.NewCompensateRequested(at.Add(2*time.Second), "nonexistent-node"),
		engine.StepOptions{})
	require.Error(t, err, "CompensateRequested with unknown ToNode must return an error")
	assert.Contains(t, err.Error(), "nonexistent-node",
		"error message must identify the unknown ToNode")
}

// ─────────────────────────────────────────────────────────────────────────────
// Task 2 (CancelActions): CancelRequested emits InvokeCancelAction per definition
// ─────────────────────────────────────────────────────────────────────────────

func TestCancelRequestedEmitsCancelActions(t *testing.T) {
	def := &definition.ProcessDefinition{
		ID: "d", Version: 1,
		Nodes: []definition.Node{
			event.NewStart("start"),
			activity.NewServiceTask("svc", activity.WithActionName("work")),
			event.NewEnd("end"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "f1", Source: "start", Target: "svc"},
			{ID: "f2", Source: "svc", Target: "end"},
		},
		CancelActions: []string{"notify", "refund"},
	}
	// A running instance with a live token and some variables.
	st := engine.InstanceState{
		InstanceID: "i1", DefID: "d", DefVersion: 1, Status: engine.StatusRunning,
		Variables: map[string]any{"amount": 10},
		Tokens:    []engine.Token{{ID: "i1-t1", NodeID: "svc", State: engine.TokenActive}},
	}
	res, err := engine.Step(def, st, engine.NewCancelRequested(time.Unix(100, 0).UTC()), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusTerminated, res.State.Status)
	assert.Empty(t, res.State.Tokens)

	// The first two commands are the cancel actions, in definition order, before FailInstance.
	var cancelNames []string
	var sawFail bool
	for _, c := range res.Commands {
		switch cmd := c.(type) {
		case engine.InvokeCancelAction:
			cancelNames = append(cancelNames, cmd.Name)
			assert.Equal(t, 10, cmd.Input["amount"], "cancel action receives a variables snapshot")
			assert.False(t, sawFail, "cancel actions must be emitted before FailInstance")
		case engine.FailInstance:
			sawFail = true
		}
	}
	assert.Equal(t, []string{"notify", "refund"}, cancelNames)
	assert.True(t, sawFail, "FailInstance must still be emitted")
}

func TestCancelRequestedNoCancelActionsUnchanged(t *testing.T) {
	def := &definition.ProcessDefinition{
		ID: "d", Version: 1,
		Nodes: []definition.Node{event.NewStart("start"), event.NewEnd("end")},
		Flows: []definition.SequenceFlow{{ID: "f1", Source: "start", Target: "end"}},
	}
	st := engine.InstanceState{
		InstanceID: "i1", DefID: "d", DefVersion: 1, Status: engine.StatusRunning,
		Tokens: []engine.Token{{ID: "i1-t1", NodeID: "start", State: engine.TokenActive}},
	}
	res, err := engine.Step(def, st, engine.NewCancelRequested(time.Unix(100, 0).UTC()), engine.StepOptions{})
	require.NoError(t, err)
	for _, c := range res.Commands {
		_, isCancel := c.(engine.InvokeCancelAction)
		assert.False(t, isCancel, "no InvokeCancelAction when CancelActions is empty")
	}
}

package engine_test

// boundary_error_matching_test.go — black-box tests for Task 4 (ADR-0104):
// three-tier boundary error matching: Check → Expr → Code precedence,
// live-error cause threading, and bare-code-source synthesis.

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
)

// ─────────────────────────────────────────────────────────────────────────────
// Sentinel error type for typed-error (errors.As) testing
// ─────────────────────────────────────────────────────────────────────────────

// paymentError is a custom error type used to verify that ErrorCheck can
// distinguish typed errors via errors.As / errors.Is.
type paymentError struct {
	Code   string
	Reason string
}

func (e *paymentError) Error() string { return "payment: " + e.Code + ": " + e.Reason }

// ─────────────────────────────────────────────────────────────────────────────
// Definition builders
// ─────────────────────────────────────────────────────────────────────────────

// boundaryCheckDef builds a root-level service task with a boundary that uses
// an ErrorCheck closure for matching.
//
//	Root: start → svc → end
//	      svc has boundary (ErrorCheck fn) → recover → end-recover
func boundaryCheckDef(checkFn func(map[string]any, error) bool) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-bnd-check", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("svc", activity.WithActionName("svc-action")),
			event.NewBoundary("bnd", "svc", event.WithBoundaryErrorCheck(checkFn)),
			activity.NewServiceTask("recover", activity.WithActionName("recover-action")),
			event.NewEnd("end"),
			event.NewEnd("end-recover"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "svc"},
			{ID: "f2", Source: "svc", Target: "end"},
			{ID: "f3", Source: "bnd", Target: "recover"},
			{ID: "f4", Source: "recover", Target: "end-recover"},
		},
	}
}

// boundaryExprDef builds a root-level service task with a boundary that uses
// an ErrorExpr for matching.
//
//	Root: start → svc → end
//	      svc has boundary (ErrorExpr) → recover → end-recover
func boundaryExprDef(expr string) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-bnd-expr", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("svc", activity.WithActionName("svc-action")),
			event.NewBoundary("bnd", "svc", event.WithBoundaryErrorExpr(expr)),
			activity.NewServiceTask("recover", activity.WithActionName("recover-action")),
			event.NewEnd("end"),
			event.NewEnd("end-recover"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "svc"},
			{ID: "f2", Source: "svc", Target: "end"},
			{ID: "f3", Source: "bnd", Target: "recover"},
			{ID: "f4", Source: "recover", Target: "end-recover"},
		},
	}
}

// boundaryAllThreeDef builds a root-level service task boundary with all three
// matching mechanisms set. Used for precedence testing.
//
//	Check wins over Expr and Code.
func boundaryAllThreeDef(checkFn func(map[string]any, error) bool, expr, code string) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-bnd-all-three", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("svc", activity.WithActionName("svc-action")),
			event.NewBoundary("bnd", "svc",
				event.WithBoundaryErrorCheck(checkFn),
				event.WithBoundaryErrorExpr(expr),
				event.WithBoundaryErrorCode(code),
			),
			activity.NewServiceTask("recover", activity.WithActionName("recover-action")),
			event.NewEnd("end"),
			event.NewEnd("end-recover"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "svc"},
			{ID: "f2", Source: "svc", Target: "end"},
			{ID: "f3", Source: "bnd", Target: "recover"},
			{ID: "f4", Source: "recover", Target: "end-recover"},
		},
	}
}

// boundaryExprAndCodeDef builds a boundary with Expr+Code set (no Check).
func boundaryExprAndCodeDef(expr, code string) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-bnd-expr-code", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("svc", activity.WithActionName("svc-action")),
			event.NewBoundary("bnd", "svc",
				event.WithBoundaryErrorExpr(expr),
				event.WithBoundaryErrorCode(code),
			),
			activity.NewServiceTask("recover", activity.WithActionName("recover-action")),
			event.NewEnd("end"),
			event.NewEnd("end-recover"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "svc"},
			{ID: "f2", Source: "svc", Target: "end"},
			{ID: "f3", Source: "bnd", Target: "recover"},
			{ID: "f4", Source: "recover", Target: "end-recover"},
		},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// stepToParked starts the given definition and returns the result with the
// InvokeAction command. Caller gets back the state with svc parked + the
// command ID for the follow-up ActionFailed/ActionCompleted.
func stepToParked(t *testing.T, def *model.ProcessDefinition) (engine.InstanceState, engine.InvokeAction) {
	t.Helper()
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC), nil),
		engine.StepOptions{})
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
	return r1.State, *ia
}

// fireActionFailed fires ActionFailed with the given error code and cause.
// Cause nil means use NewActionFailed without WithCause.
func fireActionFailed(t *testing.T, def *model.ProcessDefinition, st engine.InstanceState, ia engine.InvokeAction, errCode string, cause error) engine.StepResult {
	t.Helper()
	at := time.Date(2026, 7, 8, 12, 0, 1, 0, time.UTC)
	var r engine.StepResult
	var err error
	if cause != nil {
		r, err = engine.Step(def, st,
			engine.NewActionFailed(at, ia.CommandID, errCode, false, engine.WithCause(cause)),
			engine.StepOptions{})
	} else {
		r, err = engine.Step(def, st,
			engine.NewActionFailed(at, ia.CommandID, errCode, false),
			engine.StepOptions{})
	}
	require.NoError(t, err)
	return r
}

// assertCaught asserts that the boundary caught the error (instance still running,
// recover-action invoked, no FailInstance).
func assertCaught(t *testing.T, r engine.StepResult) {
	t.Helper()
	assert.Equal(t, engine.StatusRunning, r.State.Status, "instance must still be running (boundary caught)")
	for _, c := range r.Commands {
		if _, ok := c.(engine.FailInstance); ok {
			t.Fatal("FailInstance must NOT be emitted when boundary catches the error")
		}
	}
	var recoverIA *engine.InvokeAction
	for _, c := range r.Commands {
		if v, ok := c.(engine.InvokeAction); ok {
			vv := v
			recoverIA = &vv
		}
	}
	require.NotNil(t, recoverIA, "expected InvokeAction for recover-action")
	assert.Equal(t, "recover-action", recoverIA.Name)
}

// assertPropagated asserts that the error was NOT caught (instance failed,
// FailInstance emitted).
func assertPropagated(t *testing.T, r engine.StepResult) {
	t.Helper()
	assert.Equal(t, engine.StatusFailed, r.State.Status, "instance must be failed (boundary did not catch)")
	var fi *engine.FailInstance
	for _, c := range r.Commands {
		if v, ok := c.(engine.FailInstance); ok {
			vv := v
			fi = &vv
			break
		}
	}
	require.NotNil(t, fi, "FailInstance must be emitted when error propagates")
}

// ─────────────────────────────────────────────────────────────────────────────
// Tests
// ─────────────────────────────────────────────────────────────────────────────

// TestBoundaryErrorCheck tests ErrorCheck (Go closure) matching — highest precedence.
func TestBoundaryErrorCheck(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		checkFn func(map[string]any, error) bool
		errCode string
		cause   error
		caught  bool
	}

	cases := []testCase{
		{
			name:    "check-returns-true catches",
			checkFn: func(_ map[string]any, _ error) bool { return true },
			errCode: "ANY_CODE",
			cause:   nil,
			caught:  true,
		},
		{
			name:    "check-returns-false propagates",
			checkFn: func(_ map[string]any, _ error) bool { return false },
			errCode: "ANY_CODE",
			cause:   nil,
			caught:  false,
		},
		{
			name:    "check-uses-error-code-from-cause.Error()",
			checkFn: func(_ map[string]any, err error) bool { return err != nil && err.Error() == "EXPECTED_CODE" },
			errCode: "EXPECTED_CODE",
			cause:   nil, // nil cause → synthesized errors.New("EXPECTED_CODE")
			caught:  true,
		},
		{
			name: "check-typed-error-via-errors.As catches",
			checkFn: func(_ map[string]any, err error) bool {
				var pe *paymentError
				return errors.As(err, &pe) && pe.Code == "PAYMENT_DECLINED"
			},
			errCode: "payment-err",
			cause:   &paymentError{Code: "PAYMENT_DECLINED", Reason: "insufficient funds"},
			caught:  true,
		},
		{
			name: "check-typed-error-via-errors.As wrong type propagates",
			checkFn: func(_ map[string]any, err error) bool {
				var pe *paymentError
				return errors.As(err, &pe) && pe.Code == "PAYMENT_DECLINED"
			},
			errCode: "some-other-error",
			cause:   errors.New("plain error"), // not *paymentError
			caught:  false,
		},
		{
			name: "check-reads-instance-variables",
			checkFn: func(vars map[string]any, _ error) bool {
				return vars["level"] == "critical"
			},
			errCode: "ERR",
			cause:   nil,
			caught:  true, // vars will be injected before the step via priming; below we handle inline
		},
	}

	// The "check-reads-instance-variables" case needs a variable in the
	// instance at failure time. We handle it differently (see inline override below).
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			def := boundaryCheckDef(tc.checkFn)
			st, ia := stepToParked(t, def)

			// For the variable-reading check, inject vars into instance state.
			if tc.name == "check-reads-instance-variables" {
				if st.Variables == nil {
					st.Variables = map[string]any{}
				}
				st.Variables["level"] = "critical"
			}

			r := fireActionFailed(t, def, st, ia, tc.errCode, tc.cause)
			if tc.caught {
				assertCaught(t, r)
			} else {
				assertPropagated(t, r)
			}
		})
	}
}

// TestBoundaryErrorExpr tests ErrorExpr (expr-lang) matching — middle precedence.
func TestBoundaryErrorExpr(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		expr    string
		errCode string
		caught  bool
	}

	cases := []testCase{
		{
			name:    "expr-matches-_error-catches",
			expr:    `_error == "PAY_ERR"`,
			errCode: "PAY_ERR",
			caught:  true,
		},
		{
			name:    "expr-does-not-match-propagates",
			expr:    `_error == "PAY_ERR"`,
			errCode: "DIFFERENT_CODE",
			caught:  false,
		},
		{
			name:    "expr-true-literal-always-catches",
			expr:    `true`,
			errCode: "ANY",
			caught:  true,
		},
		{
			name:    "expr-false-literal-always-propagates",
			expr:    `false`,
			errCode: "ANY",
			caught:  false,
		},
		{
			name:    "expr-matches-one-of-multiple-codes",
			expr:    `_error == "payment-declined" || _error == "payment-expired"`,
			errCode: "payment-declined",
			caught:  true,
		},
		{
			name:    "expr-matches-none-of-multiple-codes",
			expr:    `_error == "payment-declined" || _error == "payment-expired"`,
			errCode: "network-error",
			caught:  false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			def := boundaryExprDef(tc.expr)
			st, ia := stepToParked(t, def)
			r := fireActionFailed(t, def, st, ia, tc.errCode, nil)
			if tc.caught {
				assertCaught(t, r)
			} else {
				assertPropagated(t, r)
			}
		})
	}
}

// TestBoundaryErrorMatchingPrecedence verifies the Check→Expr→Code precedence:
//   - All three set: Check wins.
//   - Expr+Code (no Check): Expr wins.
//   - Only Code: Code wins.
func TestBoundaryErrorMatchingPrecedence(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	_ = at

	t.Run("all-three-set-check-wins", func(t *testing.T) {
		t.Parallel()
		// Check returns true (catches), Expr returns false, Code doesn't match.
		// Check must win → caught.
		checkCalled := false
		checkFn := func(_ map[string]any, _ error) bool {
			checkCalled = true
			return true
		}
		// Expr is truthy but should never be reached when Check is set.
		// Code "WRONG" won't match "ANY_CODE" either.
		def := boundaryAllThreeDef(checkFn, `false`, "WRONG")
		st, ia := stepToParked(t, def)
		r := fireActionFailed(t, def, st, ia, "ANY_CODE", nil)
		assertCaught(t, r)
		assert.True(t, checkCalled, "ErrorCheck must be evaluated when all three are set")
	})

	t.Run("check-false-does-not-fallback-to-expr", func(t *testing.T) {
		t.Parallel()
		// Check returns false. Even though Expr is truthy, Check takes full
		// authority at highest precedence — false = no-match = propagate.
		checkFn := func(_ map[string]any, _ error) bool { return false }
		def := boundaryAllThreeDef(checkFn, `true`, "")
		st, ia := stepToParked(t, def)
		r := fireActionFailed(t, def, st, ia, "ANY_CODE", nil)
		assertPropagated(t, r)
	})

	t.Run("expr-wins-over-code-when-no-check", func(t *testing.T) {
		t.Parallel()
		// Expr is truthy for the thrown code; Code would NOT match (different).
		// Expr must win → caught.
		def := boundaryExprAndCodeDef(`_error == "THROWN_CODE"`, "DIFFERENT_CODE")
		st, ia := stepToParked(t, def)
		r := fireActionFailed(t, def, st, ia, "THROWN_CODE", nil)
		assertCaught(t, r)
	})

	t.Run("expr-false-does-not-fallback-to-code", func(t *testing.T) {
		t.Parallel()
		// Expr is falsy (won't match), Code would catch-all ("").
		// Expr takes authority when set — false = no-match = propagate.
		def := boundaryExprAndCodeDef(`false`, "")
		st, ia := stepToParked(t, def)
		r := fireActionFailed(t, def, st, ia, "ANY_CODE", nil)
		assertPropagated(t, r)
	})

	t.Run("only-code-uses-code-matching", func(t *testing.T) {
		t.Parallel()
		// No Check, no Expr; only Code "SPECIFIC" — matches.
		def := &model.ProcessDefinition{
			ID: "p-only-code", Version: 1,
			Nodes: []model.Node{
				event.NewStart("start"),
				activity.NewServiceTask("svc", activity.WithActionName("svc-action")),
				event.NewBoundary("bnd", "svc", event.WithBoundaryErrorCode("SPECIFIC")),
				activity.NewServiceTask("recover", activity.WithActionName("recover-action")),
				event.NewEnd("end"),
				event.NewEnd("end-recover"),
			},
			Flows: []flow.SequenceFlow{
				{ID: "f1", Source: "start", Target: "svc"},
				{ID: "f2", Source: "svc", Target: "end"},
				{ID: "f3", Source: "bnd", Target: "recover"},
				{ID: "f4", Source: "recover", Target: "end-recover"},
			},
		}
		st, ia := stepToParked(t, def)
		r := fireActionFailed(t, def, st, ia, "SPECIFIC", nil)
		assertCaught(t, r)

		// Wrong code → propagates.
		st2, ia2 := stepToParked(t, def)
		r2 := fireActionFailed(t, def, st2, ia2, "OTHER_CODE", nil)
		assertPropagated(t, r2)
	})
}

// TestBoundaryErrorCheckNilCauseSynthesis verifies that when ActionFailed has
// no live Cause (bare-code source like ErrorEndEvent/sub-instance), the ErrorCheck
// closure receives a synthesized errors.New(errorCode) so it can inspect the code
// via err.Error().
func TestBoundaryErrorCheckNilCauseSynthesis(t *testing.T) {
	t.Parallel()

	var receivedErr error
	checkFn := func(_ map[string]any, err error) bool {
		receivedErr = err
		return true // always catch so we can inspect
	}
	def := boundaryCheckDef(checkFn)
	st, ia := stepToParked(t, def)

	// Fire WITHOUT a Cause (nil).
	r := fireActionFailed(t, def, st, ia, "BARE_CODE", nil)
	assertCaught(t, r)

	require.NotNil(t, receivedErr, "ErrorCheck must receive a non-nil error even when cause is nil")
	assert.Equal(t, "BARE_CODE", receivedErr.Error(),
		"synthesized error must have .Error() == errorCode for bare-code sources")
}

// TestBoundaryErrorExprDoesNotLeakErrorIntoVars verifies that the _error variable
// injected during ErrorExpr evaluation does not leak into instance state. After a
// non-matching Expr causes the error to propagate and StatusFailed is set, the
// instance Variables must not contain _error from the expr eval env.
func TestBoundaryErrorExprDoesNotLeakErrorIntoVars(t *testing.T) {
	t.Parallel()

	// Expr that returns false: doesn't catch, so instance fails.
	def := boundaryExprDef(`false`)
	st, ia := stepToParked(t, def)
	r := fireActionFailed(t, def, st, ia, "ERR_CODE", nil)

	// Instance must have failed (expr didn't catch).
	assert.Equal(t, engine.StatusFailed, r.State.Status)

	// _error must NOT be present in the instance variables.
	if r.State.Variables != nil {
		_, hasError := r.State.Variables["_error"]
		assert.False(t, hasError, "_error must not leak into instance variables from Expr eval env")
	}
}

// TestBoundaryErrorCheckVsExprPrecedenceWithTypedError tests the full cascade
// with a typed error: Check uses errors.As to inspect the type, Expr would
// catch on error code. Check has highest precedence so when Check returns true
// it should catch regardless of what Expr says.
func TestBoundaryErrorCheckVsExprPrecedenceWithTypedError(t *testing.T) {
	t.Parallel()

	// Scenario: Check does typed-error match (catches), Expr would also catch.
	// Check wins.
	t.Run("check-typed-catch-wins-over-expr", func(t *testing.T) {
		t.Parallel()
		cause := &paymentError{Code: "AUTH_FAIL", Reason: "card expired"}
		checkFn := func(_ map[string]any, err error) bool {
			var pe *paymentError
			return errors.As(err, &pe)
		}
		def := boundaryAllThreeDef(checkFn, `_error == "AUTH_FAIL"`, "AUTH_FAIL")
		st, ia := stepToParked(t, def)
		r := fireActionFailed(t, def, st, ia, "AUTH_FAIL", cause)
		assertCaught(t, r)
	})

	// Scenario: Check does typed-error match but returns false (wrong type);
	// even though Expr would catch, Check false = no-catch (no fallback).
	t.Run("check-typed-no-match-propagates-despite-expr", func(t *testing.T) {
		t.Parallel()
		cause := errors.New("plain error") // not *paymentError
		checkFn := func(_ map[string]any, err error) bool {
			var pe *paymentError
			return errors.As(err, &pe) // always false for plain error
		}
		def := boundaryAllThreeDef(checkFn, `true`, "") // Expr catches everything
		st, ia := stepToParked(t, def)
		r := fireActionFailed(t, def, st, ia, "ANY", cause)
		assertPropagated(t, r)
	})
}

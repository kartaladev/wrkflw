package engine_test

// boundary_error_matching_test.go — black-box tests for three-tier boundary
// error matching: Check → Expr → Code precedence,
// live-error cause threading, and bare-code-source synthesis.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
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
			activity.NewServiceTask("svc", activity.WithTaskAction("svc-action")),
			event.NewBoundary("bnd", "svc", event.WithBoundaryErrorCheck(checkFn)),
			activity.NewServiceTask("recover", activity.WithTaskAction("recover-action")),
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
			activity.NewServiceTask("svc", activity.WithTaskAction("svc-action")),
			event.NewBoundary("bnd", "svc", event.WithBoundaryErrorExpr(expr)),
			activity.NewServiceTask("recover", activity.WithTaskAction("recover-action")),
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
			activity.NewServiceTask("svc", activity.WithTaskAction("svc-action")),
			event.NewBoundary("bnd", "svc",
				event.WithBoundaryErrorCheck(checkFn),
				event.WithBoundaryErrorExpr(expr),
				event.WithBoundaryErrorCode(code),
			),
			activity.NewServiceTask("recover", activity.WithTaskAction("recover-action")),
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
			activity.NewServiceTask("svc", activity.WithTaskAction("svc-action")),
			event.NewBoundary("bnd", "svc",
				event.WithBoundaryErrorExpr(expr),
				event.WithBoundaryErrorCode(code),
			),
			activity.NewServiceTask("recover", activity.WithTaskAction("recover-action")),
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
	return stepToParkedWithVars(t, def, nil)
}

// stepToParkedWithVars is stepToParked with caller-supplied StartInstance
// variables, which become the environment a boundary's ErrorExpr is evaluated
// over. It exists so a test can plant a process variable named after an expr
// builtin.
func stepToParkedWithVars(t *testing.T, def *model.ProcessDefinition, vars map[string]any) (engine.InstanceState, engine.InvokeAction) {
	t.Helper()
	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC), vars),
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
		r, err = engine.Step(t.Context(), def, st,
			engine.NewActionFailed(at, ia.CommandID, errCode, false, engine.WithCause(cause)),
			engine.StepOptions{})
	} else {
		r, err = engine.Step(t.Context(), def, st,
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
				activity.NewServiceTask("svc", activity.WithTaskAction("svc-action")),
				event.NewBoundary("bnd", "svc", event.WithBoundaryErrorCode("SPECIFIC")),
				activity.NewServiceTask("recover", activity.WithTaskAction("recover-action")),
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
// no live Cause (bare-code source like an error end event / sub-instance), the ErrorCheck
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

// TestBoundaryErrorCheckVarsMutationTrap verifies that the ErrorCheck closure
// receives a SHALLOW CLONE of instance variables, not the live map. A closure
// that mutates its vars argument must not corrupt committed instance variables
// after error propagation.
func TestBoundaryErrorCheckVarsMutationTrap(t *testing.T) {
	t.Parallel()

	// A malicious (or buggy) closure that writes into the vars map it receives.
	mutateFn := func(vars map[string]any, _ error) bool {
		vars["__injected_by_closure__"] = "should-not-leak"
		return true // still catches
	}
	def := boundaryCheckDef(mutateFn)

	at := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	// Seed a known variable so we can verify the map content.
	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)

	// Inject a known variable into the parked state.
	st := r1.State
	if st.Variables == nil {
		st.Variables = map[string]any{}
	}
	st.Variables["original"] = "value"

	var ia engine.InvokeAction
	for _, c := range r1.Commands {
		if v, ok := c.(engine.InvokeAction); ok {
			ia = v
			break
		}
	}
	require.NotEmpty(t, ia.CommandID)

	r2, err := engine.Step(t.Context(), def, st,
		engine.NewActionFailed(at.Add(time.Second), ia.CommandID, "ERR", false),
		engine.StepOptions{})
	require.NoError(t, err)

	// Boundary caught the error (closure returns true).
	assertCaught(t, r2)

	// The mutation must NOT have leaked into the resulting instance state.
	_, leaked := r2.State.Variables["__injected_by_closure__"]
	assert.False(t, leaked,
		"closure mutation of vars argument must NOT propagate to instance variables")

	// The original variable must still be present.
	assert.Equal(t, "value", r2.State.Variables["original"],
		"original instance variable must be preserved after boundary catch")
}

// twoErrorBoundaryDef builds a root-level service task with TWO boundaries:
// the first has a runtime-failing ErrorExpr (type error at eval: _error + 42),
// the second has a matching ErrorCode. Used by TestMalformedErrorExprNonFatal.
//
//	Root: start → svc → end
//	      svc has boundary-1 (failing ErrorExpr) → end-bad (should never route here)
//	      svc has boundary-2 (ErrorCode "REAL_CODE") → end-recover
func twoErrorBoundaryDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-two-err-bnd", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("svc", activity.WithTaskAction("svc-action")),
			// boundary-1: ErrorExpr that will type-error at runtime (_error + 42 adds string+int)
			event.NewBoundary("bnd-bad-expr", "svc",
				event.WithBoundaryErrorExpr(`_error + 42`),
			),
			// boundary-2: specific matching ErrorCode
			event.NewBoundary("bnd-real", "svc",
				event.WithBoundaryErrorCode("REAL_CODE"),
			),
			activity.NewServiceTask("recover", activity.WithTaskAction("recover-action")),
			event.NewEnd("end"),
			event.NewEnd("end-bad"),
			event.NewEnd("end-recover"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "svc"},
			{ID: "f2", Source: "svc", Target: "end"},
			{ID: "f3", Source: "bnd-bad-expr", Target: "end-bad"},
			{ID: "f4", Source: "bnd-real", Target: "recover"},
			{ID: "f5", Source: "recover", Target: "end-recover"},
		},
	}
}

// TestMalformedErrorExprNonFatal pins the non-fatal rule: a boundary ErrorExpr
// that compiles but type-errors at runtime (e.g. _error + 42) must be treated as a
// non-match (the boundary is skipped), allowing subsequent boundaries in the
// same scope to still be evaluated. The Step must NOT return an error.
func TestMalformedErrorExprNonFatal(t *testing.T) {
	t.Parallel()

	def := twoErrorBoundaryDef()

	at := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)

	var ia engine.InvokeAction
	for _, c := range r1.Commands {
		if v, ok := c.(engine.InvokeAction); ok {
			ia = v
			break
		}
	}
	require.NotEmpty(t, ia.CommandID)

	// Fire with "REAL_CODE": bnd-bad-expr's ErrorExpr type-errors at runtime
	// (string + int), so bnd-bad-expr is skipped. bnd-real has matching ErrorCode
	// → it catches → recovery path.
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewActionFailed(at.Add(time.Second), ia.CommandID, "REAL_CODE", false),
		engine.StepOptions{})

	// Step must NOT return an error — a malformed ErrorExpr is non-fatal.
	require.NoError(t, err, "Step must not error when an ErrorExpr type-errors at runtime; it should skip to next boundary")

	// The second boundary (bnd-real) must have caught the error.
	assertCaught(t, r2)
}

// ─────────────────────────────────────────────────────────────────────────────
// #141 — what a caller-chosen VARIABLE KEY NAME can do to boundary routing
// ─────────────────────────────────────────────────────────────────────────────

// strictDecodeErrorFor returns the ActionFailed error string the runtime records
// when a strict [action.Typed] action is invoked with caller-supplied keys its In
// does not declare.
//
// The string is produced by the real decoder rather than written out here, so
// these tests are measuring the live message and break if its shape changes. That
// matters: the whole of #141 turns on which bytes of that message an attacker
// chooses, and a hand-written literal would keep passing after the decoder stopped
// emitting them.
func strictDecodeErrorFor(t *testing.T, keys ...string) string {
	t.Helper()

	type strictIn struct {
		OrderID string `json:"orderId"`
	}
	a := action.Typed(func(_ context.Context, in strictIn) (map[string]any, error) {
		return map[string]any{"orderId": in.OrderID}, nil
	}, action.WithStrictInput())

	vars := map[string]any{"orderId": "ORD-1"}
	for _, k := range keys {
		vars[k] = "x"
	}
	_, err := a.Do(t.Context(), vars)
	require.Error(t, err, "the probe must actually produce a decode error, or it is measuring nothing")
	return err.Error()
}

// TestBoundaryRoutingUnderCallerChosenKeyNames is #141's measurement, and it keeps
// the issue's two halves apart because they have opposite verdicts.
//
// The reachability chain, derived rather than recalled: a strict decode failure
// names the offending keys (action/typed.go rejectUnknownKeys) → the runtime
// records err.Error() as ActionFailed.Err → engine/step_triggers.go passes that
// string to propagateError as the errorCode → engine/step_errors.go injects it as
// env["_error"] for a boundary's ErrorExpr. Key names are caller-supplied: eight
// mergeVars call sites in step_triggers.go, seven of them copy a caller-supplied
// map into s.Variables wholesale — StartInstance vars, action and task output,
// message and signal payloads — keys and all.
//
//	INJECTION half — MEASURED NEGATIVE. The value never becomes expr SOURCE, and
//	a caller-chosen NAME cannot change what a definition's program means either.
//	Two barriers hold it, neither of them strconv.Quote: the predicate string
//	comes from the process definition and the error reaches the evaluator as one
//	entry of an environment map; and expr's builtins are not shadowable by env
//	keys. The DOES NOT REPRODUCE cases attempt both.
//
//	DATA-INFLUENCE half — MEASURED POSITIVE, and it is not a code-injection bug.
//	A definition author who writes a substring predicate over _error is writing it
//	over a string that partly consists of caller-chosen key names. The REPRODUCES
//	case shows it; the at-limit accept beside it shows the predicate is not one
//	that matches everything.
//
// Every refusal here has an accept next to it, named "at-limit". Without them,
// "did not catch" would be satisfied by a boundary that never catches anything —
// which is the failure mode a table of negatives invites. Cases are referred to
// by name rather than by position, because an earlier version of this comment
// numbered them and the numbering went stale the first time a case was inserted.
func TestBoundaryRoutingUnderCallerChosenKeyNames(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name string
		expr string
		code string
		// keys are the caller-chosen variable names the strict decode will name.
		keys []string
		// rawCode, when set, is used as the ActionFailed error verbatim instead of
		// deriving one from keys.
		rawCode string
		// vars seeds the instance variables, which become the ErrorExpr env.
		vars   map[string]any
		assert func(t *testing.T, r engine.StepResult)
	}

	cases := []testCase{
		{
			name: "REPRODUCES: a caller-chosen key name flips a definition-authored substring predicate",
			expr: `_error contains "fatal"`,
			keys: []string{"fatal-boundary"},
			assert: func(t *testing.T, r engine.StepResult) {
				assertCaught(t, r)
			},
		},
		{
			name: "at-limit accept: a key name the predicate does not ask for leaves routing alone",
			expr: `_error contains "fatal"`,
			keys: []string{"harmless-variable"},
			assert: func(t *testing.T, r engine.StepResult) {
				assertPropagated(t, r)
			},
		},
		{
			name: "DOES NOT REPRODUCE: a key name that is expr source is not evaluated as expr source",
			expr: `_error == "PAY_ERR"`,
			keys: []string{`" or true or "`},
			assert: func(t *testing.T, r engine.StepResult) {
				assertPropagated(t, r)
			},
		},
		{
			// THE STRONGEST PAYLOAD KNOWN, and it is here because a weaker one
			// misled this PR once. expr accepts SINGLE-quoted literals, which
			// strconv.Quote does not escape, and the two " delimiters Quote wraps
			// a key in go into the message unescaped. So interpolated naively
			// into `_error == "…"` this reads
			//	("…unknown key " == 'x') or true or ("… only)" == "PAY_ERR")
			// and evaluates TRUE — measured. It still does not reproduce here,
			// and that is the binding barrier doing the work, nothing else.
			//
			// An earlier version of this case used a payload containing a ",
			// which Quote renders \" and expr's lexer rejects. That payload
			// survives an interpolating engine for a reason peculiar to itself,
			// and reading its survival as a property of the escaping is what put
			// a false "second barrier" claim into this tree.
			name: "DOES NOT REPRODUCE: a key name that is a complete, well-typed expr fragment",
			expr: `_error == "PAY_ERR"`,
			keys: []string{`== 'x' or true or`},
			assert: func(t *testing.T, r engine.StepResult) {
				assertPropagated(t, r)
			},
		},
		{
			// The second barrier, and the one that is real. The attacker supplies
			// NAMES, so the sharp question is whether a caller-chosen key can
			// change what a definition's program MEANS in CALL position. It
			// cannot, under this package's compile config — see the comment on
			// env["_error"] in step_errors.go, and the 71-builtin sweep in
			// TestBuiltinsAreNotShadowableByEnvKeys, which is what carries the
			// general claim. This case pins the reachable instance of it.
			//
			// The `plantReached` conjunct is load-bearing and must not be
			// dropped. Without it the case asserts only that len() is the
			// builtin, which is equally true when the planted variables never
			// reach the env at all — so it would pass over an env it had no
			// effect on, which is the vacuity this group keeps having to reject.
			// Now the case fails unless the plant demonstrably arrived.
			name: "DOES NOT REPRODUCE: a process variable cannot shadow an expr builtin in call position",
			expr: `len(_error) > 3 && plantReached == "yes"`,
			keys: []string{"harmless"},
			vars: map[string]any{"len": "pwned", "lower": "pwned", "plantReached": "yes"},
			assert: func(t *testing.T, r engine.StepResult) {
				// len() must still be the builtin: the message is far longer than
				// 3, so a shadowed len returning "pwned" would make len(_error)
				// a type error and the boundary would not catch.
				assertCaught(t, r)
			},
		},
		{
			// at-limit refuse beside the shadowing accept, so it is not satisfied
			// by a predicate that is true whatever len does.
			name: "at-limit for shadowing: the same predicate is false when the builtin says so",
			expr: `len(_error) > 100000 && plantReached == "yes"`,
			keys: []string{"harmless"},
			vars: map[string]any{"len": "pwned", "plantReached": "yes"},
			assert: func(t *testing.T, r engine.StepResult) {
				assertPropagated(t, r)
			},
		},
		{
			name: "DOES NOT REPRODUCE: a key name equal to the awaited code cannot satisfy an equality",
			expr: `_error == "PAY_ERR"`,
			keys: []string{"PAY_ERR"},
			assert: func(t *testing.T, r engine.StepResult) {
				assertPropagated(t, r)
			},
		},
		{
			name: "at-limit accept for the two refusals: this message shape does route when the predicate asks for it",
			expr: `_error contains "unknown key"`,
			keys: []string{"PAY_ERR"},
			assert: func(t *testing.T, r engine.StepResult) {
				assertCaught(t, r)
			},
		},
		{
			name: "the ErrorCode tier is unreachable: the message is prefixed, so it can never equal a code",
			code: "PAY_ERR",
			keys: []string{"PAY_ERR"},
			assert: func(t *testing.T, r engine.StepResult) {
				assertPropagated(t, r)
			},
		},
		{
			name:    "at-limit accept for the ErrorCode tier: the same boundary catches its own code",
			code:    "PAY_ERR",
			rawCode: "PAY_ERR",
			assert: func(t *testing.T, r engine.StepResult) {
				assertCaught(t, r)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			errCode := tc.rawCode
			if errCode == "" {
				errCode = strictDecodeErrorFor(t, tc.keys...)
			}

			def := boundaryExprAndCodeDef(tc.expr, tc.code)
			st, ia := stepToParkedWithVars(t, def, tc.vars)
			tc.assert(t, fireActionFailed(t, def, st, ia, errCode, nil))
		})
	}
}

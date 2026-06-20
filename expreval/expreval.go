// Package expreval is the engine's only wrapper over expr-lang/expr. It is the
// single place that imports the expression vendor, so the dependency stays
// swappable. It memoizes compiled programs (compilation is deterministic, so the
// cache is referentially transparent) and is safe for concurrent use.
package expreval

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/file"
	"github.com/expr-lang/expr/vm"
)

// Evaluator compiles and evaluates expression strings, caching compiled programs.
type Evaluator struct {
	mu    sync.Mutex
	cache map[string]*vm.Program
}

// New returns an empty Evaluator.
func New() *Evaluator { return &Evaluator{cache: make(map[string]*vm.Program)} }

func (e *Evaluator) compile(code string) (*vm.Program, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if p, ok := e.cache[code]; ok {
		return p, nil
	}
	p, err := expr.Compile(code, expr.AllowUndefinedVariables())
	if err != nil {
		return nil, fmt.Errorf("expreval: compile %q: %w", code, err)
	}
	e.cache[code] = p
	return p, nil
}

// EvalBool evaluates code against env and requires a boolean result. Undefined
// variables evaluate to nil rather than erroring.
func (e *Evaluator) EvalBool(code string, env map[string]any) (bool, error) {
	p, err := e.compile(code)
	if err != nil {
		return false, err
	}
	out, err := expr.Run(p, env)
	if err != nil {
		// A process variable that is absent from env resolves to nil (because
		// expr.AllowUndefinedVariables() is used at compile time). Comparing nil
		// against a typed value (e.g. "amount > 100" with amount absent) then
		// panics inside the VM and is surfaced as a nil-operand error.  For
		// gateway condition evaluation, a missing variable means the condition is
		// not satisfied — map that to (false, nil) instead of propagating an
		// error.
		if isNilOperandError(err) {
			return false, nil
		}
		return false, fmt.Errorf("expreval: run %q: %w", code, err)
	}
	b, ok := out.(bool)
	if !ok {
		return false, fmt.Errorf("expreval: %q did not evaluate to bool (got %T)", code, out)
	}
	return b, nil
}

// EvalDuration evaluates code against env and normalizes the result to a
// time.Duration. Three result forms are accepted:
//
//   - time.Duration — used as-is.
//   - integer (any int/uint kind): interpreted as a number of whole seconds
//     (e.g. the literal `90` yields 90s). This matches the most common author
//     intent when writing SLA values in process definitions.
//   - float64: interpreted as fractional seconds (e.g. `1.5` yields 1500ms).
//     expr-lang/expr frequently produces float64 for numeric literals without
//     a decimal, so fractional support is a strict superset of integral-only;
//     authors writing whole-number float literals (e.g. `float(90)`) still get
//     the expected result.
//   - string: parsed via time.ParseDuration (e.g. `"3h"` yields 3h).
//
// Anything else, or a string that time.ParseDuration rejects, is returned as an
// error.
func (e *Evaluator) EvalDuration(code string, env map[string]any) (time.Duration, error) {
	p, err := e.compile(code)
	if err != nil {
		return 0, err
	}
	out, err := expr.Run(p, env)
	if err != nil {
		return 0, fmt.Errorf("expreval: run %q: %w", code, err)
	}
	switch v := out.(type) {
	case time.Duration:
		return v, nil
	case int:
		return time.Duration(v) * time.Second, nil
	case int8:
		return time.Duration(v) * time.Second, nil
	case int16:
		return time.Duration(v) * time.Second, nil
	case int32:
		return time.Duration(v) * time.Second, nil
	case int64:
		return time.Duration(v) * time.Second, nil
	case uint:
		return time.Duration(v) * time.Second, nil
	case uint8:
		return time.Duration(v) * time.Second, nil
	case uint16:
		return time.Duration(v) * time.Second, nil
	case uint32:
		return time.Duration(v) * time.Second, nil
	case uint64:
		return time.Duration(v) * time.Second, nil
	case float64:
		return time.Duration(v * float64(time.Second)), nil
	case string:
		d, parseErr := time.ParseDuration(v)
		if parseErr != nil {
			return 0, fmt.Errorf("expreval: %q yielded string %q not parseable as duration: %w", code, v, parseErr)
		}
		return d, nil
	default:
		return 0, fmt.Errorf("expreval: %q did not evaluate to a duration-compatible type (got %T)", code, out)
	}
}

// isNilOperandError reports whether err is the runtime error produced by
// expr-lang/expr when a nil operand (typically an undefined process variable
// resolved via expr.AllowUndefinedVariables) participates in a comparison or
// arithmetic operation.
//
// Why this mapping exists: gateway conditions are compiled with
// AllowUndefinedVariables so that a condition like "amount > 100" compiles even
// when "amount" is not declared in the environment type.  At runtime, an absent
// variable resolves to nil.  Comparing nil to a typed value then panics in the
// VM, which surfaces the panic as a *file.Error whose Message starts with
// "invalid operation:" and contains "<nil>" (the %T rendering of a nil
// interface).  We treat this as "condition not satisfied" (false) rather than a
// hard error.
//
// Detection strategy: use errors.As to match the concrete *file.Error type
// (avoiding string-matching on the full formatted output that includes location
// and source-snippet decoration), then check Message for the nil-operand
// signature.
//
// Verified against github.com/expr-lang/expr v1.17.8.  The canary test
// TestExprNilComparisonErrorShapeUnchanged in expreval_test.go will fail loudly
// if a future expr upgrade changes this error format.
func isNilOperandError(err error) bool {
	var fileErr *file.Error
	if !errors.As(err, &fileErr) {
		return false
	}
	return strings.HasPrefix(fileErr.Message, "invalid operation:") &&
		strings.Contains(fileErr.Message, "<nil>")
}

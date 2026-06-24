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

// DefaultTimeout bounds a single expression evaluation. It is generous enough
// that no legitimate gateway/timer/correlation expression approaches it, so it
// never affects normal (deterministic) results — it only backstops a pathological
// or malicious expression that would otherwise hang the engine's single-threaded
// driver loop and stall every other instance.
const DefaultTimeout = 5 * time.Second

// ErrEvalTimeout is returned when an expression evaluation exceeds the Evaluator's
// timeout. The evaluation is abandoned (the caller regains control) but, because
// Go cannot preempt a running goroutine, a pure-CPU expression keeps consuming a
// core until it finishes — the timeout bounds latency, not CPU. Disable the guard
// with WithTimeout(0) only for fully trusted definitions.
var ErrEvalTimeout = errors.New("workflow-expreval: expression evaluation timed out")

// Evaluator compiles and evaluates expression strings, caching compiled programs.
type Evaluator struct {
	mu      sync.Mutex
	cache   map[string]*vm.Program
	timeout time.Duration
}

// Option configures an Evaluator.
type Option func(*Evaluator)

// WithTimeout sets the per-evaluation wall-clock timeout. A value <= 0 disables
// the guard (the fast path: no goroutine, current behavior) for consumers who
// fully trust their definitions and want maximum throughput.
func WithTimeout(d time.Duration) Option {
	return func(e *Evaluator) { e.timeout = d }
}

// New returns an Evaluator. By default a DefaultTimeout guard is enabled; pass
// WithTimeout to override it (including WithTimeout(0) to disable).
func New(opts ...Option) *Evaluator {
	e := &Evaluator{cache: make(map[string]*vm.Program), timeout: DefaultTimeout}
	for _, o := range opts {
		o(e)
	}
	return e
}

// run evaluates a compiled program, enforcing the timeout guard when configured.
// When timeout <= 0 it calls expr.Run directly (no goroutine). Otherwise it runs
// the evaluation on a goroutine and races it against the timeout; the channel is
// buffered so the goroutine never blocks on send even after a timeout, and a
// recover guards against a panic escaping the goroutine.
func (e *Evaluator) run(p *vm.Program, env map[string]any) (any, error) {
	if e.timeout <= 0 {
		return expr.Run(p, env)
	}
	type result struct {
		out any
		err error
	}
	ch := make(chan result, 1)
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				ch <- result{nil, fmt.Errorf("workflow-expreval: evaluation panicked: %v", rec)}
			}
		}()
		out, err := expr.Run(p, env)
		ch <- result{out, err}
	}()
	timer := time.NewTimer(e.timeout)
	defer timer.Stop()
	select {
	case r := <-ch:
		return r.out, r.err
	case <-timer.C:
		return nil, ErrEvalTimeout
	}
}

func (e *Evaluator) compile(code string) (*vm.Program, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if p, ok := e.cache[code]; ok {
		return p, nil
	}
	p, err := expr.Compile(code, expr.AllowUndefinedVariables())
	if err != nil {
		return nil, fmt.Errorf("workflow-expreval: compile %q: %w", code, err)
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
	out, err := e.run(p, env)
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
		return false, fmt.Errorf("workflow-expreval: run %q: %w", code, err)
	}
	b, ok := out.(bool)
	if !ok {
		return false, fmt.Errorf("workflow-expreval: %q did not evaluate to bool (got %T)", code, out)
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
	out, err := e.run(p, env)
	if err != nil {
		return 0, fmt.Errorf("workflow-expreval: run %q: %w", code, err)
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
			return 0, fmt.Errorf("workflow-expreval: %q yielded string %q not parseable as duration: %w", code, v, parseErr)
		}
		return d, nil
	default:
		return 0, fmt.Errorf("workflow-expreval: %q did not evaluate to a duration-compatible type (got %T)", code, out)
	}
}

// EvalString evaluates code against env and converts the result to a string.
// If the expression already returns a string it is used as-is. Any other type
// is formatted via fmt.Sprintf("%v", v) so that authors can use integer or
// boolean correlation keys without explicit conversions in the definition.
//
// An empty code returns an empty string without compilation (fast path for the
// common "no correlation key" case).
func (e *Evaluator) EvalString(code string, env map[string]any) (string, error) {
	if code == "" {
		return "", nil
	}
	p, err := e.compile(code)
	if err != nil {
		return "", err
	}
	out, err := e.run(p, env)
	if err != nil {
		return "", fmt.Errorf("workflow-expreval: run %q: %w", code, err)
	}
	if s, ok := out.(string); ok {
		return s, nil
	}
	return fmt.Sprintf("%v", out), nil
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

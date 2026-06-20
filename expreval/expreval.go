// Package expreval is the engine's only wrapper over expr-lang/expr. It is the
// single place that imports the expression vendor, so the dependency stays
// swappable. It memoizes compiled programs (compilation is deterministic, so the
// cache is referentially transparent) and is safe for concurrent use.
package expreval

import (
	"fmt"
	"strings"
	"sync"

	"github.com/expr-lang/expr"
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
		// If the error is about an invalid operation with nil, treat the result as false
		if strings.Contains(err.Error(), "invalid operation") && strings.Contains(err.Error(), "<nil>") {
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

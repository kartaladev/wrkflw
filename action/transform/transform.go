// Package transform provides a pure service action that computes output
// variables from expr-lang expressions evaluated against the instance variables.
package transform

import (
	"context"
	"fmt"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"github.com/zakyalvan/krtlwrkflw/action"
)

// Option configures a transform action.
type Option func(*config)

type binding struct {
	out  string
	prog *vm.Program
	src  string
}

type config struct {
	sets []setSpec
}

type setSpec struct {
	out string
	src string
}

// Set maps an output variable key to an expr expression evaluated against the
// input variables. Repeatable; later Sets with the same key overwrite earlier.
// Later Sets can reference the outputs of earlier Sets — each computed value is
// merged into the evaluation environment before the next expression runs.
func Set(outKey, exprStr string) Option {
	return func(c *config) { c.sets = append(c.sets, setSpec{outKey, exprStr}) }
}

type transform struct {
	bindings []binding
}

// NewTransform compiles each Set expression and returns a service action that, on
// Do, evaluates them against the input variables and returns the results. A
// malformed expression fails here (at wiring time), not mid-process.
func NewTransform(opts ...Option) (action.ServiceAction, error) {
	var c config
	for _, o := range opts {
		o(&c)
	}
	t := &transform{}
	for _, s := range c.sets {
		prog, err := expr.Compile(s.src)
		if err != nil {
			return nil, fmt.Errorf("workflow-transform: compile %q for %q: %w", s.src, s.out, err)
		}
		t.bindings = append(t.bindings, binding{out: s.out, prog: prog, src: s.src})
	}
	return t, nil
}

func (t *transform) Do(_ context.Context, in map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(t.bindings))
	// Merge input and computed outputs for each expression evaluation.
	env := make(map[string]any)
	for k, v := range in {
		env[k] = v
	}
	for _, b := range t.bindings {
		v, err := expr.Run(b.prog, env)
		if err != nil {
			return nil, fmt.Errorf("workflow-transform: eval %q for %q: %w", b.src, b.out, err)
		}
		out[b.out] = v
		env[b.out] = v // accumulate for next expressions
	}
	return out, nil
}

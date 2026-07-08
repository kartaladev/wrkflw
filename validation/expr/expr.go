// Package expr is a validation adapter over github.com/expr-lang/expr. A strategy holds
// a list of boolean predicates; ALL must evaluate true against the input map. It imports
// expr-lang directly (an allowed adapter boundary) and does NOT reuse internal/expreval,
// whose EvalBool maps missing vars to false (gateway semantics) — undesirable for validation.
package expr

import (
	"context"
	"fmt"
	"strings"

	exprlang "github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"

	"github.com/zakyalvan/krtlwrkflw/validation"
)

// Kind is the registry key for expr strategies.
const Kind = "expr"

type strategy struct{ predicates []string }

// New returns a strategy requiring all predicates to hold against the input.
func New(predicates ...string) validation.DescribableStrategy {
	return strategy{predicates: predicates}
}

// Factory rebuilds a strategy from newline-separated predicate text.
func Factory(schema string) (validation.ValidationStrategy, error) {
	var preds []string
	for _, line := range strings.Split(schema, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			preds = append(preds, s)
		}
	}
	return strategy{predicates: preds}, nil
}

func (s strategy) Descriptor() validation.ValidationDescriptor {
	return validation.ValidationDescriptor{Kind: Kind, Schema: strings.Join(s.predicates, "\n")}
}

func (s strategy) NewValidator() (validation.Validator, error) {
	programs := make([]*vm.Program, 0, len(s.predicates))
	for _, p := range s.predicates {
		prog, err := exprlang.Compile(p, exprlang.AsBool())
		if err != nil {
			return nil, fmt.Errorf("workflow-validation/expr: compile %q: %w", p, err)
		}
		programs = append(programs, prog)
	}
	return &validator{source: s.predicates, programs: programs}, nil
}

type validator struct {
	source   []string
	programs []*vm.Program
}

func (v *validator) Validate(_ context.Context, input map[string]any) error {
	for i, prog := range v.programs {
		out, err := exprlang.Run(prog, input)
		if err != nil {
			return fmt.Errorf("predicate %q: %w", v.source[i], err)
		}
		ok, _ := out.(bool)
		if !ok {
			return fmt.Errorf("predicate %q not satisfied", v.source[i])
		}
	}
	return nil
}

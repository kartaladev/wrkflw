// Package transform provides a service action that enriches a working variable
// set via I/O-capable mappers and computes output process variables from
// expr-lang expressions. Only WithExpr results are returned (and persisted);
// WithMapper results are scratch that flow into later stages via the internal
// env but are never written to the returned output map.
package transform

import (
	"context"
	"fmt"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"github.com/kartaladev/wrkflw/action"
)

// Mapper computes zero or more output variables from the current variable set.
// It MAY perform I/O (database/HTTP lookups) and MUST honour ctx. It may return
// an action.NonRetryable error for permanent failures.
// Mapper-enriched variables are available to later stages within this action
// but are NOT written back as process variables; to persist one, project it
// with a following WithExpr.
type Mapper func(ctx context.Context, vars map[string]any) (map[string]any, error)

// Option configures a transform action.
type Option func(*config)

// stage is a single pipeline step — either an expr binding or a mapper call.
type stage struct {
	// exprBinding is non-nil when this is a WithExpr stage.
	exprBinding *exprBinding
	// mapper is non-nil when this is a WithMapper stage.
	mapper Mapper
}

type exprBinding struct {
	outKey string
	prog   *vm.Program
	src    string
}

type config struct {
	// stages accumulate in registration order; resolved at NewTransform time for
	// expr stages (compile), at Do time for mapper stages (execution).
	stages []stageSpec
}

type stageSpec struct {
	// Exactly one of exprSpec or mapper is non-nil.
	exprSpec *exprSpec
	mapper   Mapper
}

type exprSpec struct {
	outKey string
	src    string
}

// WithExpr maps an output key to an expr-lang expression evaluated against the
// current variables (input merged with all prior stage outputs). The expression
// is compiled eagerly: a malformed expression fails NewTransform, not Do.
func WithExpr(outKey, exprStr string) Option {
	return func(c *config) {
		c.stages = append(c.stages, stageSpec{exprSpec: &exprSpec{outKey: outKey, src: exprStr}})
	}
}

// WithMapper registers an I/O-capable mapper. Mappers run in registration order;
// each sees the input variables merged with all prior stages' outputs (chaining).
// Mapper results go into the internal env working set only — they are NOT written
// to the returned out map and are NOT persisted as process variables. To persist a
// fetched field, follow it with a WithExpr that projects it.
func WithMapper(m Mapper) Option {
	return func(c *config) {
		c.stages = append(c.stages, stageSpec{mapper: m})
	}
}

type transform struct {
	stages []stage
}

// NewTransform compiles each WithExpr expression eagerly and returns a
// service action. A malformed expression causes NewTransform to return an error
// immediately (wiring time), not mid-process. WithMapper stages are recorded
// as-is and invoked at Do time.
func NewTransform(opts ...Option) (action.Action, error) {
	var c config
	for _, o := range opts {
		o(&c)
	}

	t := &transform{}
	for _, spec := range c.stages {
		switch {
		case spec.exprSpec != nil:
			prog, err := expr.Compile(spec.exprSpec.src)
			if err != nil {
				return nil, fmt.Errorf("workflow-transform: compile %q for %q: %w",
					spec.exprSpec.src, spec.exprSpec.outKey, err)
			}
			t.stages = append(t.stages, stage{
				exprBinding: &exprBinding{
					outKey: spec.exprSpec.outKey,
					prog:   prog,
					src:    spec.exprSpec.src,
				},
			})
		case spec.mapper != nil:
			t.stages = append(t.stages, stage{mapper: spec.mapper})
		default:
			// spec.exprSpec == nil && spec.mapper == nil → nil mapper was passed via WithMapper.
			return nil, fmt.Errorf("workflow-transform: nil mapper")
		}
	}
	return t, nil
}

// Do executes all registered stages in registration order against in and returns
// the process variables projected by WithExpr stages.
//
// Internal working set (env): starts as a copy of in. ALL stage outputs are merged
// into env so later stages can chain from earlier ones.
//
// Returned out map: ONLY WithExpr results. Mapper results are scratch — they
// update env for chaining but are never written to out.
//
// ctx is checked before each stage; a cancelled/expired context terminates Do
// immediately.
func (t *transform) Do(ctx context.Context, in map[string]any) (map[string]any, error) {
	// env is the internal working set; starts as a copy of in.
	env := make(map[string]any, len(in))
	for k, v := range in {
		env[k] = v
	}
	// out receives only WithExpr results.
	out := make(map[string]any)

	for _, s := range t.stages {
		// Check ctx before each stage.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		switch {
		case s.exprBinding != nil:
			b := s.exprBinding
			v, err := expr.Run(b.prog, env)
			if err != nil {
				return nil, fmt.Errorf("workflow-transform: eval %q for %q: %w", b.src, b.outKey, err)
			}
			// expr results go to BOTH env (for chaining) and out (persisted).
			env[b.outKey] = v
			out[b.outKey] = v

		case s.mapper != nil:
			res, err := s.mapper(ctx, env)
			if err != nil {
				return nil, fmt.Errorf("workflow-transform: mapper: %w", err)
			}
			// Mapper results go to env ONLY (scratch for chaining), NOT to out.
			for k, v := range res {
				env[k] = v
			}
		}
	}
	return out, nil
}

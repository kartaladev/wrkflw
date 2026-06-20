package runtime

import (
	"context"
	"fmt"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/model"
)

// Runner is the reference single-process driver loop.
type Runner struct {
	cat   action.Catalog
	clk   clock.Clock
	store StateStore
	jnl   Journal
	out   OutboxWriter
}

func NewRunner(cat action.Catalog, clk clock.Clock, store StateStore, jnl Journal, out OutboxWriter) *Runner {
	return &Runner{cat: cat, clk: clk, store: store, jnl: jnl, out: out}
}

// Run starts an instance and drives it to a terminal state, performing each
// command and feeding results back as triggers. Linear processes only in Plan 1.
func (r *Runner) Run(ctx context.Context, def *model.ProcessDefinition, instanceID string, vars map[string]any) (engine.InstanceState, error) {
	queue := []engine.Trigger{engine.NewStartInstance(r.clk.Now(), vars)}
	st := engine.InstanceState{InstanceID: instanceID}

	for len(queue) > 0 {
		trg := queue[0]
		queue = queue[1:]

		r.jnl.Append(instanceID, trg)
		res, err := engine.Step(def, st, trg, engine.StepOptions{})
		if err != nil {
			return st, fmt.Errorf("runtime: step: %w", err)
		}
		st = res.State
		r.store.Save(st)

		for _, c := range res.Commands {
			next, err := r.perform(ctx, c)
			if err != nil {
				return st, err
			}
			if next != nil {
				queue = append(queue, next)
			}
		}
	}
	return st, nil
}

// perform executes one command and returns the resulting trigger, if any.
func (r *Runner) perform(ctx context.Context, c engine.Command) (engine.Trigger, error) {
	switch cmd := c.(type) {
	case engine.InvokeAction:
		a, ok := r.cat.Resolve(cmd.Name)
		if !ok {
			return engine.NewActionFailed(r.clk.Now(), cmd.CommandID, "unknown action: "+cmd.Name, false), nil
		}
		out, err := a.Do(ctx, cmd.Input)
		if err != nil {
			return engine.NewActionFailed(r.clk.Now(), cmd.CommandID, err.Error(), true), nil
		}
		return engine.NewActionCompleted(r.clk.Now(), cmd.CommandID, out), nil

	case engine.CompleteInstance:
		r.out.Write("instance.completed", cmd.Result)
		return nil, nil

	case engine.FailInstance:
		r.out.Write("instance.failed", map[string]any{"error": cmd.Err})
		return nil, nil

	default:
		return nil, fmt.Errorf("runtime: unsupported command %T", c)
	}
}

package runtime

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/model"
)

// Runner is the reference single-process driver loop.
type Runner struct {
	cat      action.Catalog
	clk      clock.Clock
	store    StateStore
	jnl      Journal
	out      OutboxWriter
	resolver humantask.ActorResolver
	tasks    humantask.TaskStore
	authz    authz.Authorizer
	sched    Scheduler
}

// Option is a functional option for Runner. Optional capability bundles (human
// tasks, scheduler) are configured via options; required core dependencies are
// positional in NewRunner.
type Option func(*Runner)

// WithHumanTasks wires the human-task capability into the Runner. Without this
// option, any process that reaches a user-task node will return a descriptive
// error rather than panic.
//
//   - resolver resolves an eligibility spec to the candidate actor list.
//   - tasks persists human-task records.
//   - az authorizes actors against task eligibility specs (used by TaskService,
//     not by the engine core).
func WithHumanTasks(resolver humantask.ActorResolver, tasks humantask.TaskStore, az authz.Authorizer) Option {
	return func(r *Runner) {
		r.resolver = resolver
		r.tasks = tasks
		r.authz = az
	}
}

// WithScheduler wires a Scheduler into the Runner, enabling timer commands
// (ScheduleTimer / CancelTimer). Without this option any process that reaches a
// timer node will return a descriptive error.
func WithScheduler(sched Scheduler) Option {
	return func(r *Runner) { r.sched = sched }
}

// NewRunner constructs a Runner with the five required core ports (cat, clk,
// store, jnl, out) and any optional capability bundles supplied as functional
// options.
//
// Required ports:
//   - cat: the service-action catalog (may be nil for processes with no service tasks).
//   - clk: the time source. Pass a fake clock in tests.
//   - store: the authoritative instance state store.
//   - jnl: the append-only trigger journal.
//   - out: the outbox writer for domain events.
//
// Optional capabilities (via Option):
//   - [WithHumanTasks]: human-task support (resolver, task store, authorizer).
//   - [WithScheduler]: timer scheduling support.
func NewRunner(
	cat action.Catalog,
	clk clock.Clock,
	store StateStore,
	jnl Journal,
	out OutboxWriter,
	opts ...Option,
) *Runner {
	r := &Runner{
		cat:   cat,
		clk:   clk,
		store: store,
		jnl:   jnl,
		out:   out,
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Run starts an instance and drives it to a terminal state or until the engine
// parks (e.g. awaiting a human task). It returns the state at the point it stopped.
func (r *Runner) Run(ctx context.Context, def *model.ProcessDefinition, instanceID string, vars map[string]any) (engine.InstanceState, error) {
	st := engine.InstanceState{InstanceID: instanceID}
	return r.deliverLoop(ctx, def, st, engine.NewStartInstance(r.clk.Now(), vars))
}

// Deliver loads the current instance state, applies one trigger via engine.Step,
// saves the new state, records the trigger in the journal, and performs the
// resulting commands (feeding follow-up triggers back through the loop).
//
// It is the entry point for external triggers such as HumanClaimed and
// HumanCompleted that arrive after Run has returned (parked at a human task),
// and for TimerFired triggers delivered by the scheduler's fire callback.
//
// Authorization contract: human-task triggers (HumanClaimed, HumanReassigned,
// HumanCompleted) MUST originate from TaskService, which performs authorization
// before returning the trigger. Delivering such a trigger from any other source
// bypasses authorization entirely — the engine core is authorization-unaware by
// design. It is the caller's responsibility to ensure human-task triggers pass
// through TaskService.
func (r *Runner) Deliver(ctx context.Context, def *model.ProcessDefinition, instanceID string, trg engine.Trigger) (engine.InstanceState, error) {
	st, err := r.store.Load(instanceID)
	if err != nil {
		return engine.InstanceState{}, fmt.Errorf("runtime: deliver: load: %w", err)
	}
	return r.deliverLoop(ctx, def, st, trg)
}

// deliverLoop applies one trigger and then any follow-up triggers emitted by
// perform (action results, etc.) until all commands are resolved or the engine
// parks. It encapsulates the journal→Step→save→perform cycle shared by Run and
// Deliver.
func (r *Runner) deliverLoop(ctx context.Context, def *model.ProcessDefinition, st engine.InstanceState, trg engine.Trigger) (engine.InstanceState, error) {
	queue := []engine.Trigger{trg}

	for len(queue) > 0 {
		t := queue[0]
		queue = queue[1:]

		if err := r.jnl.Append(st.InstanceID, t); err != nil {
			return st, fmt.Errorf("runtime: journal: %w", err)
		}
		res, err := engine.Step(def, st, t, engine.StepOptions{})
		if err != nil {
			return st, fmt.Errorf("runtime: step: %w", err)
		}
		st = res.State
		if err := r.store.Save(st); err != nil {
			return st, fmt.Errorf("runtime: save: %w", err)
		}

		for _, c := range res.Commands {
			next, err := r.perform(ctx, def, st, c)
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
// st is the current instance state, used for variable access when resolving
// human-task candidates. def is the process definition, captured by timer
// fire callbacks that need to call Deliver.
//
//nolint:cyclop // the command switch is intentionally exhaustive; each case is simple.
func (r *Runner) perform(ctx context.Context, def *model.ProcessDefinition, st engine.InstanceState, c engine.Command) (engine.Trigger, error) {
	switch cmd := c.(type) {
	case engine.InvokeAction:
		if r.cat == nil {
			return engine.NewActionFailed(r.clk.Now(), cmd.CommandID, "no action catalog: "+cmd.Name, false), nil
		}
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
		if err := r.out.Write("instance.completed", cmd.Result); err != nil {
			return nil, fmt.Errorf("runtime: outbox: %w", err)
		}
		return nil, nil

	case engine.FailInstance:
		if err := r.out.Write("instance.failed", map[string]any{"error": cmd.Err}); err != nil {
			return nil, fmt.Errorf("runtime: outbox: %w", err)
		}
		return nil, nil

	case engine.AwaitHuman:
		if r.resolver == nil {
			return nil, fmt.Errorf("runtime: perform AwaitHuman: no ActorResolver configured")
		}
		if r.tasks == nil {
			return nil, fmt.Errorf("runtime: perform AwaitHuman: no TaskStore configured")
		}
		// Resolve candidates from the eligibility spec and process variables.
		actors, err := r.resolver.Candidates(ctx, cmd.Eligibility, st.Variables)
		if err != nil {
			return nil, fmt.Errorf("runtime: resolve candidates: %w", err)
		}
		candidateIDs := make([]string, len(actors))
		for i, a := range actors {
			candidateIDs[i] = a.ID
		}

		// Build and persist the HumanTask record. The task skeleton was already
		// added to st.Tasks by the engine (drive → KindUserTask); we find it and
		// enrich it with resolved candidates before upserting.
		task := humantask.HumanTask{
			TaskToken:   cmd.TaskToken,
			InstanceID:  st.InstanceID,
			Eligibility: cmd.Eligibility,
			Candidates:  candidateIDs,
			State:       humantask.Unclaimed,
			CreatedAt:   r.clk.Now(),
		}
		// Copy NodeID from the in-state task record if present.
		if t := st.TaskByToken(cmd.TaskToken); t != nil {
			task.NodeID = t.NodeID
			task.CreatedAt = t.CreatedAt // preserve engine-stamped time
		}
		if err := r.tasks.Upsert(ctx, task); err != nil {
			return nil, fmt.Errorf("runtime: upsert task: %w", err)
		}
		// No follow-up trigger: the instance parks here.
		return nil, nil

	case engine.UpdateTask:
		if r.tasks == nil {
			return nil, fmt.Errorf("runtime: perform UpdateTask: no TaskStore configured")
		}
		if err := r.tasks.Upsert(ctx, cmd.Task); err != nil {
			return nil, fmt.Errorf("runtime: update task: %w", err)
		}
		return nil, nil

	case engine.ScheduleTimer:
		if r.sched == nil {
			return nil, fmt.Errorf("runtime: perform ScheduleTimer %q: no Scheduler configured", cmd.TimerID)
		}
		// Capture the values needed by the fire callback; do not close over
		// mutable references to cmd (already a value type, so this is safe).
		instanceID := st.InstanceID
		timerID := cmd.TimerID
		r.sched.Schedule(cmd.TimerID, cmd.FireAt, func() {
			// This callback runs from the scheduler's goroutine (or Tick caller).
			// Use a background context: the originating request context may have
			// been cancelled by the time the timer fires.
			fireCtx := context.Background()
			trg := engine.NewTimerFired(r.clk.Now(), timerID)
			if _, err := r.Deliver(fireCtx, def, instanceID, trg); err != nil {
				slog.Error("runtime: timer fire: Deliver failed",
					"timerID", timerID,
					"instanceID", instanceID,
					"err", err,
				)
			}
		})
		return nil, nil

	case engine.CancelTimer:
		if r.sched == nil {
			return nil, fmt.Errorf("runtime: perform CancelTimer %q: no Scheduler configured", cmd.TimerID)
		}
		r.sched.Cancel(cmd.TimerID)
		return nil, nil

	default:
		return nil, fmt.Errorf("runtime: unsupported command %T", c)
	}
}

package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/model"
)

// msgKey is the composite key used to look up a message waiter by name+correlation.
type msgKey struct {
	Name           string
	CorrelationKey string
}

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
	sigbus   *SignalBus

	// msgMu guards msgWaiters.
	msgMu sync.Mutex
	// msgWaiters maps a (messageName, correlationKey) pair to the instance ID
	// that is waiting on it. Message catch events are 1:1 (each correlation key
	// routes to exactly one instance), so a simple map suffices.
	msgWaiters map[msgKey]string
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

// WithSignalBus wires a [SignalBus] into the Runner, enabling signal throw
// commands (ThrowSignal). Without this option any process that reaches a signal
// throw node will return a descriptive error.
//
// After each deliverLoop iteration the runner reconciles the instance's
// AwaitSignal tokens with the bus (via [SignalBus.Sync]) so that a later
// [SignalBus.Publish] reaches all parked instances.
func WithSignalBus(bus *SignalBus) Option {
	return func(r *Runner) { r.sigbus = bus }
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
//   - [WithSignalBus]: signal broadcast support (ThrowSignal).
func NewRunner(
	cat action.Catalog,
	clk clock.Clock,
	store StateStore,
	jnl Journal,
	out OutboxWriter,
	opts ...Option,
) *Runner {
	r := &Runner{
		cat:        cat,
		clk:        clk,
		store:      store,
		jnl:        jnl,
		out:        out,
		msgWaiters: make(map[msgKey]string),
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
//
// After each save, if a SignalBus is configured, the loop reconciles the
// instance's AwaitSignal tokens with the bus so that a future
// [SignalBus.Publish] reaches this instance.
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

		// Reconcile signal-bus and message waiters after each state save so both
		// the SignalBus and msgWaiters maps always reflect the current parked state.
		r.syncWaiters(st)

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

// syncWaiters reconciles both the SignalBus subscriptions and the internal
// message-waiter table for st after each deliverLoop save. It calls
// syncSignalBus (if a bus is configured) and syncMsgWaiters so both are
// always consistent with the current parked state of the instance.
func (r *Runner) syncWaiters(st engine.InstanceState) {
	r.syncSignalBus(st)
	r.syncMsgWaiters(st)
}

// syncSignalBus reconciles st's AwaitSignal tokens with the SignalBus, if one
// is configured. This is a no-op when r.sigbus is nil.
func (r *Runner) syncSignalBus(st engine.InstanceState) {
	if r.sigbus == nil {
		return
	}
	var awaiting []string
	for _, tok := range st.Tokens {
		if tok.AwaitSignal != "" {
			awaiting = append(awaiting, tok.AwaitSignal)
		}
	}
	r.sigbus.Sync(st.InstanceID, awaiting)
}

// syncMsgWaiters reconciles the runner's internal message-waiter table with the
// current state of st. It registers new message-awaiting tokens and removes
// entries that are no longer waiting.
func (r *Runner) syncMsgWaiters(st engine.InstanceState) {
	r.msgMu.Lock()
	defer r.msgMu.Unlock()

	// Remove all existing entries for this instance.
	for k, id := range r.msgWaiters {
		if id == st.InstanceID {
			delete(r.msgWaiters, k)
		}
	}

	// Re-register from current tokens.
	for _, tok := range st.Tokens {
		if tok.AwaitMessage != "" {
			k := msgKey{Name: tok.AwaitMessage, CorrelationKey: tok.AwaitMessageKey}
			r.msgWaiters[k] = st.InstanceID
		}
	}
}

// findMessageWaiter returns the instance ID that is currently waiting for a
// message with the given name and correlation key, and whether one was found.
func (r *Runner) findMessageWaiter(name, correlationKey string) (string, bool) {
	r.msgMu.Lock()
	defer r.msgMu.Unlock()
	id, ok := r.msgWaiters[msgKey{Name: name, CorrelationKey: correlationKey}]
	return id, ok
}

// DeliverMessage finds the single process instance that is currently waiting for
// a message with the given name and correlationKey, then delivers a
// [engine.MessageReceived] trigger to it. If no matching instance is found it
// is a clean no-op.
//
// The runner tracks message waiters internally via [syncMsgWaiters], which is
// called after each deliverLoop iteration. This keeps the state in sync without
// requiring an enumeration API on StateStore.
//
// def is required to call Deliver on the matched instance.
func (r *Runner) DeliverMessage(ctx context.Context, def *model.ProcessDefinition, name, correlationKey string, payload map[string]any) error {
	instanceID, found := r.findMessageWaiter(name, correlationKey)
	if !found {
		return nil
	}
	trg := engine.NewMessageReceived(r.clk.Now(), name, correlationKey, payload)
	_, err := r.Deliver(ctx, def, instanceID, trg)
	return err
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

	case engine.ThrowSignal:
		if r.sigbus == nil {
			return nil, fmt.Errorf("runtime: perform ThrowSignal %q: no SignalBus configured", cmd.Name)
		}
		if err := r.sigbus.Publish(ctx, cmd.Name, cmd.Payload); err != nil {
			return nil, fmt.Errorf("runtime: perform ThrowSignal %q: %w", cmd.Name, err)
		}
		return nil, nil

	default:
		return nil, fmt.Errorf("runtime: unsupported command %T", c)
	}
}

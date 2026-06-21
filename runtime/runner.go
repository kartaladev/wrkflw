package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/model"
)

// callDepthKey is the private context key used to thread the call-activity
// recursion depth counter through perform → r.Run → deliverLoop → perform chains.
// It is unexported so that no caller outside this package can set or read it
// accidentally; the helpers callDepth / withCallDepth are the only access points.
type callDepthKey struct{}

// maxCallActivityDepth is the maximum nesting depth allowed for synchronous
// call-activity invocations. Exceeding this limit returns a descriptive
// SubInstanceFailed error instead of allowing unbounded recursion that would
// eventually cause a stack overflow or exhaust memory.
//
// Child instance IDs use a SHORT suffix scheme (see StartSubInstance handling):
// "<parentInstanceID>-sub-c<N>" where c<N> is only the command-sequence suffix,
// not the full parent ID. This gives O(depth) growth rather than O(2^depth), so
// depth 64 is safely bounded. True async call activities (a future enhancement)
// do not use this counter at all.
const maxCallActivityDepth = 64

// callDepth returns the current call-activity nesting depth stored in ctx.
// Returns 0 if no depth has been set (i.e. the outermost call).
func callDepth(ctx context.Context) int {
	if d, ok := ctx.Value(callDepthKey{}).(int); ok {
		return d
	}
	return 0
}

// withCallDepth returns a child context with the call-activity depth set to d.
func withCallDepth(ctx context.Context, d int) context.Context {
	return context.WithValue(ctx, callDepthKey{}, d)
}

// msgKey is the composite key used to look up a message waiter by name+correlation.
type msgKey struct {
	Name           string
	CorrelationKey string
}

// Runner is the reference single-process driver loop.
type Runner struct {
	cat      action.Catalog
	clk      clock.Clock
	store    Store
	resolver humantask.ActorResolver
	tasks    humantask.TaskStore
	authz    authz.Authorizer
	sched    Scheduler
	sigbus   *SignalBus
	defsReg  DefinitionRegistry

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

// WithDefinitions wires a [DefinitionRegistry] into the Runner, enabling
// [engine.StartSubInstance] commands (call activities). Without this option,
// any process that reaches a KindCallActivity node will return a descriptive
// error rather than panicking.
//
// The registry resolves DefRef strings (as stored on KindCallActivity nodes)
// to *model.ProcessDefinition values. Use [NewMapDefinitionRegistry] to build
// an in-memory registry from a plain map.
func WithDefinitions(reg DefinitionRegistry) Option {
	return func(r *Runner) { r.defsReg = reg }
}

// NewRunner constructs a Runner with the three required core ports (cat, clk,
// store) and any optional capability bundles supplied as functional options.
//
// Required ports:
//   - cat: the service-action catalog (may be nil for processes with no service tasks).
//   - clk: the time source. Pass a fake clock in tests.
//   - store: the transactional persistence port (snapshot + journal + outbox).
//     See [Store]; the in-memory [MemStore] is the reference fake.
//
// ADR-0007 amends ADR-0005: the former store/jnl/out positionals collapse to
// one transactional Store, so snapshot, journal, and outbox commit atomically
// per applied trigger.
//
// Optional capabilities (via Option):
//   - [WithHumanTasks]: human-task support (resolver, task store, authorizer).
//   - [WithScheduler]: timer scheduling support.
//   - [WithSignalBus]: signal broadcast support (ThrowSignal).
func NewRunner(
	cat action.Catalog,
	clk clock.Clock,
	store Store,
	opts ...Option,
) *Runner {
	r := &Runner{
		cat:        cat,
		clk:        clk,
		store:      store,
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
	return r.deliverLoop(ctx, def, st, 0, true, engine.NewStartInstance(r.clk.Now(), vars))
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
	st, token, err := r.store.Load(ctx, instanceID)
	if err != nil {
		return engine.InstanceState{}, fmt.Errorf("runtime: deliver: load: %w", err)
	}
	return r.deliverLoop(ctx, def, st, token, false, trg)
}

// deliverLoop applies triggers from queue and then any follow-up triggers emitted
// by perform (action results, etc.) until all commands are resolved or the engine
// parks. It encapsulates the Step→outboxEventsFor→Create/Commit→perform cycle
// shared by Run and Deliver.
//
// token is the current optimistic-concurrency token; create=true on the very
// first step (Run path, no row yet) and false on all subsequent steps.
//
// After each committed save, if a SignalBus or message waiters are configured,
// the loop reconciles them so that a future [SignalBus.Publish] reaches this
// instance.
func (r *Runner) deliverLoop(
	ctx context.Context,
	def *model.ProcessDefinition,
	st engine.InstanceState,
	token Token,
	create bool,
	trg engine.Trigger,
) (engine.InstanceState, error) {
	queue := []engine.Trigger{trg}

	for len(queue) > 0 {
		t := queue[0]
		queue = queue[1:]

		res, err := engine.Step(def, st, t, engine.StepOptions{})
		if err != nil {
			return st, fmt.Errorf("runtime: step: %w", err)
		}
		st = res.State

		events := outboxEventsFor(res.Commands)
		appliedStep := AppliedStep{State: st, Trigger: t, Events: events}

		if create {
			token, err = r.store.Create(ctx, appliedStep)
			create = false
		} else {
			token, err = r.store.Commit(ctx, token, appliedStep)
		}
		if err != nil {
			return st, fmt.Errorf("runtime: commit: %w", err)
		}

		// Reconcile signal-bus and message waiters after each committed save.
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
// requiring an enumeration API on Store.
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
		// Outbox event ("instance.completed") is derived by outboxEventsFor and
		// written inside the Commit tx; nothing to perform here.
		return nil, nil

	case engine.FailInstance:
		// Outbox event ("instance.failed") is derived by outboxEventsFor and
		// written inside the Commit tx; nothing to perform here.
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
			const maxAttempts = 5
			var err error
			for range maxAttempts {
				if _, err = r.Deliver(fireCtx, def, instanceID, trg); err == nil {
					return
				}
				if !errors.Is(err, ErrConcurrentUpdate) {
					slog.Error("runtime: timer fire: Deliver failed",
						"timerID", timerID,
						"instanceID", instanceID,
						"err", err,
					)
					return
				}
				// ErrConcurrentUpdate: another Deliver won the CAS; Deliver
				// internally reloads fresh state on the next call. Retry
				// immediately (no sleep needed — store reloads on each Deliver).
			}
			slog.Error("runtime: timer fire: Deliver permanently dropped after CAS conflicts",
				"timerID", timerID,
				"instanceID", instanceID,
				"attempts", maxAttempts,
				"err", err,
			)
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

	case engine.StartSubInstance:
		// Nil-registry guard: a missing registry is a configuration error, not a
		// retryable runtime failure, so we fail fast with a descriptive message.
		if r.defsReg == nil {
			return nil, fmt.Errorf("runtime: perform StartSubInstance %q: no definition registry configured (use WithDefinitions)", cmd.DefRef)
		}
		childDef, err := r.defsReg.Lookup(cmd.DefRef)
		if err != nil {
			return nil, fmt.Errorf("runtime: perform StartSubInstance %q: registry lookup: %w", cmd.DefRef, err)
		}

		// Fix 2: Recursion / cycle depth guard.
		//
		// A definition whose call activity references itself (direct: A→A, or via a
		// cycle: A→B→A) causes unbounded synchronous recursion through perform →
		// r.Run → deliverLoop → perform, which ultimately stack-overflows. We thread
		// the depth counter through ctx so every nested call increments it; when the
		// limit is reached we return a descriptive SubInstanceFailed instead of
		// crashing. The synchronous runner only supports children that run to
		// completion in one pass; async call activities (a future enhancement) would
		// not use this counter.
		depth := callDepth(ctx)
		if depth >= maxCallActivityDepth {
			return engine.NewSubInstanceFailed(r.clk.Now(), cmd.CommandID,
				fmt.Sprintf("runtime: call activity depth limit %d exceeded (possible recursive definition: %q); "+
					"the synchronous runner does not support cyclic or deeply nested call activities",
					maxCallActivityDepth, cmd.DefRef),
			), nil
		}
		childCtx := withCallDepth(ctx, depth+1)

		// Derive a deterministic child instance ID from the parent and command ID.
		// Scheme: "<parentInstanceID>-sub-<suffix>" where <suffix> is only the
		// trailing segment of cmd.CommandID after the last "-" (e.g. "c1", "c2").
		// cmd.CommandID format is "<instanceID>-c<N>"; embedding the full commandID
		// would cause child IDs to grow O(2^depth). Using just the short suffix
		// keeps growth linear: each nesting level adds a constant-length segment.
		// IDs remain unique because CmdSeq is monotonic within an instance.
		suffix := cmd.CommandID
		if idx := strings.LastIndex(cmd.CommandID, "-"); idx >= 0 {
			suffix = cmd.CommandID[idx+1:]
		}
		childInstanceID := st.InstanceID + "-sub-" + suffix

		// Run the child to completion (synchronous within perform). The child uses
		// the same Runner so it shares the store, journal, outbox, catalog, and
		// scheduler. The child's Run call drives the child's deliverLoop until the
		// child parks or completes.
		childSt, err := r.Run(childCtx, childDef, childInstanceID, cmd.Input)
		if err != nil {
			// Child run returned a hard error (e.g. storage failure). Propagate as
			// SubInstanceFailed so the parent instance can respond.
			return engine.NewSubInstanceFailed(r.clk.Now(), cmd.CommandID, err.Error()), nil
		}

		// Translate the child's terminal status into a parent trigger.
		switch childSt.Status {
		case engine.StatusCompleted:
			// Pass the child's final variables back as the Output so the parent can
			// merge them. This gives the parent access to everything the child computed.
			return engine.NewSubInstanceCompleted(r.clk.Now(), cmd.CommandID, childSt.Variables), nil

		case engine.StatusRunning:
			// Fix 1: Explicit parked-child error.
			//
			// The child parked (StatusRunning) without completing. This happens when
			// the child contains a node that requires external input — a human task,
			// timer, signal catch event, or its own call activity — that cannot be
			// resolved within a single synchronous Run. The synchronous reference
			// runner does not support re-entering a parked child; async call activities
			// are a future enhancement.
			//
			// Return a clear, diagnosable error message so the consumer understands
			// the limitation rather than receiving a generic "did not complete" message.
			return engine.NewSubInstanceFailed(r.clk.Now(), cmd.CommandID,
				fmt.Sprintf("runtime: call activity child %q parked (status running): "+
					"the synchronous runner does not support children that wait on human tasks, "+
					"timers, or events; async call activity is a future enhancement",
					childInstanceID),
			), nil

		default:
			// StatusFailed or any other non-completed, non-running terminal state.
			// Include the numeric status in the message so failures are diagnosable.
			return engine.NewSubInstanceFailed(r.clk.Now(), cmd.CommandID,
				fmt.Sprintf("runtime: call activity child %q ended with status %d", childInstanceID, childSt.Status),
			), nil
		}

	default:
		return nil, fmt.Errorf("runtime: unsupported command %T", c)
	}
}

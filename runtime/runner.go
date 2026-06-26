package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/internal/expreval"
	"github.com/zakyalvan/krtlwrkflw/internal/observability"
	"github.com/zakyalvan/krtlwrkflw/model"
)

// callDepthKey is the private context key used to thread the call-activity
// recursion depth counter through perform → r.Run → deliverLoop → perform chains.
// It is unexported so that no caller outside this package can set or read it
// accidentally; the helpers callDepth / withCallDepth are the only access points.
type callDepthKey struct{}

// maxCallDepth is the maximum nesting depth allowed for call-activity invocations.
// For the synchronous path (no CallLinkStore) it guards against stack overflow via
// the ctx-threaded depth counter. For the async path (CallLinkStore present) it is
// computed from stored link depths and blocks runaway call chains before they start.
//
// Child instance IDs use a SHORT suffix scheme (see StartSubInstance handling):
// "<parentInstanceID>-sub-c<N>" where c<N> is only the command-sequence suffix,
// not the full parent ID. This gives O(depth) growth rather than O(2^depth), so
// depth 64 is safely bounded.
const maxCallDepth = 64

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
	cat        action.Catalog
	clk        clock.Clock
	store      Store
	resolver   humantask.ActorResolver
	tasks      humantask.TaskStore
	authz      authz.Authorizer
	sched      Scheduler
	sigbus     *SignalBus
	defsReg    DefinitionRegistry
	callLinks  CallLinkStore
	timerStore TimerStore
	msgSink    MessageSink
	// jitter supplies the random fraction used to de-synchronize retry backoff.
	// It is sampled at the runtime edge (perform) and recorded on the ActionFailed
	// trigger so that engine replay remains deterministic.
	jitter JitterSource

	// defaultRetryPolicy is the fallback retry policy applied to any action-bearing
	// node that declares no RetryPolicy of its own. When nil, retry is disabled by
	// default and a failed action behaves as before (error boundary or instance failure).
	// Set via [WithDefaultRetryPolicy].
	defaultRetryPolicy *model.RetryPolicy

	// conditionEval, when non-nil, is the expression evaluator the runner passes
	// into engine.Step via StepOptions.Evaluator for every step. When nil (the
	// default) the engine uses its pure, wall-clock-free package-global evaluator,
	// preserving deterministic replay. A long-lived evaluator is held here so its
	// compile cache is reused across steps. Set via [WithExpressionTimeout] or
	// [WithConditionEvaluator] (ADR-0056).
	conditionEval engine.ConditionEvaluator

	// logOpt, tpOpt, mpOpt are staged observability options collected by the
	// With* option functions and passed together to newRunnerObs after the option
	// loop. They are nil when the corresponding With* option was not provided.
	logOpt observability.Option
	tpOpt  observability.Option
	mpOpt  observability.Option

	// obs carries the logger/tracer/meter and the pre-built process instruments.
	// Always non-nil after NewRunner (defaults to noop providers + slog.Default()).
	obs *runnerObs

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

// WithCallLinks wires a [CallLinkStore] into the Runner, enabling the
// non-blocking (async) path for [engine.StartSubInstance] commands (call
// activities). When this option is set, [perform] records the parent↔child link
// and starts the child's first burst without waiting for the child to complete —
// the parent parks at the call node until a notifier delivers the outcome. When
// this option is NOT set, the synchronous behavior (run child to completion
// in-process) is preserved verbatim.
func WithCallLinks(store CallLinkStore) Option {
	return func(r *Runner) { r.callLinks = store }
}

// WithTimerStore wires a [TimerStore] into the Runner. When set, the runtime
// records each armed/cancelled timer into the AppliedStep so the Store persists
// them atomically with state, and [Runner.RehydrateTimers] can re-arm them on
// restart. Absent this option, timers are in-memory only and lost on restart.
func WithTimerStore(store TimerStore) Option {
	return func(r *Runner) { r.timerStore = store }
}

// WithMessageSink wires a [MessageSink] into the Runner, enabling the outbound
// [engine.SendMessage] command emitted by KindSendTask nodes. Without this option,
// any process that reaches a send-task node returns a descriptive error rather
// than silently dropping the message.
//
// The sink owns routing: intra-engine delivery to a parked ReceiveTask (e.g. via
// [Runner.DeliverMessage]), publication to an external broker / the eventing
// outbox, or both. Send is invoked synchronously and its error is surfaced; the
// sink should be idempotent (ADR-0060).
func WithMessageSink(sink MessageSink) Option {
	return func(r *Runner) { r.msgSink = sink }
}

// WithJitterSource overrides the retry-backoff jitter source (default: [NewJitterSource]).
// Inject a deterministic source in tests to produce predictable fire-at times.
func WithJitterSource(src JitterSource) Option { return func(r *Runner) { r.jitter = src } }

// WithDefaultRetryPolicy sets the fallback retry policy applied to any action-bearing
// node that declares no RetryPolicy of its own. Without this option, retry is disabled
// by default and a failed action behaves as before (error boundary or instance failure).
//
// The policy value is copied on each call, so subsequent mutations by the caller do
// not affect the Runner.
func WithDefaultRetryPolicy(p model.RetryPolicy) Option {
	return func(r *Runner) { r.defaultRetryPolicy = &p }
}

// WithExpressionTimeout builds a long-lived, timeout-capable expression evaluator
// (compile cache reused across steps) and injects it into the engine for every
// step, bounding each in-engine expression evaluation — gateway conditions,
// timer/deadline durations, correlation keys — to d of wall-clock time. A runaway or
// hostile expression then aborts with [expreval.ErrEvalTimeout] instead of
// stalling the driver loop and every sibling instance behind it (the DoS the
// audit flagged; ADR-0049).
//
// This is the explicit, per-runner opt-in the ADR-0049 follow-up called for
// (ADR-0056). DETERMINISM TRADE-OFF: enabling the guard makes the engine's
// expression evaluation wall-clock-dependent, so a timed-out result is no longer
// reproducible on replay. Enable it only when you must evaluate UNTRUSTED
// definitions; trusted-definition deployments should leave it off (the default)
// to keep deterministic replay. A non-positive d disables the guard (equivalent
// to the default pure evaluator).
//
// WithExpressionTimeout and [WithConditionEvaluator] set the same field; the last
// option wins.
func WithExpressionTimeout(d time.Duration) Option {
	return func(r *Runner) {
		r.conditionEval = expreval.New(expreval.WithTimeout(d))
	}
}

// WithConditionEvaluator injects a caller-supplied [engine.ConditionEvaluator]
// into the engine for every step, overriding the pure package-global default.
// Use it when you need full control over compilation/evaluation (e.g. a custom
// builtin set or a shared evaluator instance); for the common DoS-guard case
// prefer [WithExpressionTimeout].
//
// A nil evaluator is ignored (the default pure evaluator remains in effect).
// DETERMINISM: supplying an evaluator whose results depend on wall-clock time
// (e.g. one built with expreval.WithTimeout(d>0)) trades deterministic replay for
// that behaviour — an explicit consumer choice (ADR-0056).
//
// WithConditionEvaluator and [WithExpressionTimeout] set the same field; the last
// option wins.
func WithConditionEvaluator(eval engine.ConditionEvaluator) Option {
	return func(r *Runner) {
		if eval != nil {
			r.conditionEval = eval
		}
	}
}

// WithLogger sets the structured logger used by the Runner (default: [slog.Default]).
// A nil value is ignored.
func WithLogger(l *slog.Logger) Option {
	return func(r *Runner) { r.logOpt = observability.WithLogger(l) }
}

// WithTracerProvider sets the OTel tracer provider used by the Runner
// (default: the OTel global provider). A nil value is ignored.
func WithTracerProvider(tp trace.TracerProvider) Option {
	return func(r *Runner) { r.tpOpt = observability.WithTracerProvider(tp) }
}

// WithMeterProvider sets the OTel meter provider used by the Runner
// (default: the OTel global provider). A nil value is ignored.
func WithMeterProvider(mp metric.MeterProvider) Option {
	return func(r *Runner) { r.mpOpt = observability.WithMeterProvider(mp) }
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
		jitter:     NewJitterSource(),
		msgWaiters: make(map[msgKey]string),
	}
	for _, o := range opts {
		o(r)
	}
	r.obs = newRunnerObs(r.logOpt, r.tpOpt, r.mpOpt)
	return r
}

// Run starts an instance and drives it to a terminal state or until the engine
// parks (e.g. awaiting a human task). It returns the state at the point it stopped.
func (r *Runner) Run(ctx context.Context, def *model.ProcessDefinition, instanceID string, vars map[string]any) (engine.InstanceState, error) {
	ctx, span := r.obs.tracer().Start(ctx, "wrkflw.runner.Run", trace.WithAttributes(
		attribute.String("wrkflw.instance_id", instanceID),
		attribute.String("wrkflw.def_id", def.ID),
		attribute.Int("wrkflw.def_version", def.Version),
	))
	defer span.End()
	st := engine.InstanceState{InstanceID: instanceID}
	out, err := r.deliverLoop(ctx, def, st, 0, true, nil, engine.NewStartInstance(r.clk.Now(), vars))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else {
		span.SetAttributes(attribute.String("wrkflw.status", statusName(out.Status)))
	}
	return out, err
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
	ctx, span := r.obs.tracer().Start(ctx, "wrkflw.runner.Deliver", trace.WithAttributes(
		attribute.String("wrkflw.instance_id", instanceID),
		attribute.String("wrkflw.trigger", triggerName(trg)),
	))
	defer span.End()
	st, token, err := r.store.Load(ctx, instanceID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return engine.InstanceState{}, fmt.Errorf("workflow-runtime: deliver: load: %w", err)
	}
	out, err := r.deliverLoop(ctx, def, st, token, false, nil, trg)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return out, err
}

// deliverLoop applies triggers from queue and then any follow-up triggers emitted
// by perform (action results, etc.) until all commands are resolved or the engine
// parks. It encapsulates the Step→terminalOutboxEvent→Create/Commit→perform cycle
// shared by Run and Deliver.
//
// token is the current optimistic-concurrency token; create=true on the very
// first step (Run path, no row yet) and false on all subsequent steps.
//
// firstCallLink, when non-nil, is attached to the FIRST applied step's
// AppliedStep.NewCallLink (the create step) and then cleared. All existing
// callers (Run, Deliver) pass nil — no behavior change for them. The internal
// runChild helper passes the link so the child's Create records it atomically.
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
	firstCallLink *CallLink,
	trg engine.Trigger,
) (engine.InstanceState, error) {
	queue := []engine.Trigger{trg}

	for len(queue) > 0 {
		t := queue[0]
		queue = queue[1:]

		prevStatus := st.Status
		prevIncidents := len(st.Incidents)

		stepCtx, span := r.obs.tracer().Start(ctx, "wrkflw.step", trace.WithAttributes(
			attribute.String("wrkflw.instance_id", st.InstanceID),
			attribute.String("wrkflw.def_id", def.ID),
			attribute.String("wrkflw.trigger", triggerName(t)),
		))
		start := r.clk.Now()
		res, err := engine.Step(def, st, t, engine.StepOptions{
			DefaultRetryPolicy: r.defaultRetryPolicy,
			Evaluator:          r.conditionEval,
		})
		r.obs.stepDuration.Record(stepCtx, r.clk.Now().Sub(start).Seconds(),
			metric.WithAttributes(attribute.String("trigger", triggerName(t))))
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			span.End()
			return st, fmt.Errorf("workflow-runtime: step: %w", err)
		}
		st = res.State
		span.SetAttributes(
			attribute.Int("wrkflw.command_count", len(res.Commands)),
			attribute.String("wrkflw.status", statusName(st.Status)),
		)
		span.End()

		if create {
			r.obs.instStarted.Add(ctx, 1, metric.WithAttributes(attribute.String("def", def.ID)))
			r.obs.instActive.Add(ctx, 1)
		}
		if isTerminal(st.Status) && !isTerminal(prevStatus) {
			r.obs.instCompleted.Add(ctx, 1, metric.WithAttributes(
				attribute.String("def", def.ID),
				attribute.String("status", statusName(st.Status)),
			))
			r.obs.instActive.Add(ctx, -1)
		}
		if len(st.Incidents) > prevIncidents {
			r.obs.incidentsRaised.Add(ctx, 1, metric.WithAttributes(attribute.String("def", def.ID)))
		}

		events := terminalOutboxEvent(prevStatus, st, res.Commands)

		// Compute a CallOutcome when this step transitions the instance into a
		// terminal status AND a CallLinkStore is configured. The Store's Commit
		// implementation uses this to flip the call link to terminal atomically
		// (one transaction / one MemStore lock). For root instances the Store
		// treats a missing link as a no-op, so setting CallOutcome unconditionally
		// on terminal is safe even without special-casing here.
		var outcome *CallOutcome
		if r.callLinks != nil && isTerminal(st.Status) && !isTerminal(prevStatus) {
			switch st.Status {
			case engine.StatusCompleted:
				outcome = &CallOutcome{Completed: true, Output: copyVarsForOutcome(st.Variables)}
			default: // StatusFailed, StatusTerminated, or any other terminal
				outcome = &CallOutcome{Completed: false, Err: terminalErr(st)}
			}
		}

		var timerArms []ArmedTimer
		var timerCancels []string
		if r.timerStore != nil {
			timerArms, timerCancels = timerOpsFor(res.Commands, t, st.DefID, st.DefVersion, st.InstanceID)
		}

		appliedStep := AppliedStep{State: st, Trigger: t, Events: events, CallOutcome: outcome, TimerArms: timerArms, TimerCancels: timerCancels}

		if create {
			// Attach the firstCallLink to the Create step (child async path only).
			// After the first iteration this is nil for all callers.
			appliedStep.NewCallLink = firstCallLink
			firstCallLink = nil // consumed; cleared so subsequent steps don't carry it
			token, err = r.store.Create(ctx, appliedStep)
			create = false
		} else {
			token, err = r.store.Commit(ctx, token, appliedStep)
		}
		if err != nil {
			return st, fmt.Errorf("workflow-runtime: commit: %w", err)
		}

		// Reconcile signal-bus and message waiters after each committed save.
		r.syncWaiters(st)

		for _, c := range res.Commands {
			next, err := r.perform(stepCtx, def, st, c)
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

// runChild starts a child instance — driving its first burst SYNCHRONOUSLY on the
// caller's goroutine — with the call link threaded into the child's first Create.
// It is "non-blocking" only in the engine sense: the PARENT does not wait for the
// child's eventual terminal state (a notifier resumes the parent later). Do NOT
// wrap this in a goroutine — it shares the Store, and concurrent child starts would
// break the store's write ordering. It is called by the async StartSubInstance path
// when r.callLinks != nil.
//
// It drives the child's first burst (StartInstance trigger) through deliverLoop
// with create=true, passing link so the child's first AppliedStep.NewCallLink
// is set atomically. The parent stays parked; the child may park too (e.g. at a
// human task) — that is the expected outcome for the async path.
func (r *Runner) runChild(ctx context.Context, def *model.ProcessDefinition, childInstanceID string, vars map[string]any, link *CallLink) error {
	st := engine.InstanceState{InstanceID: childInstanceID}
	_, err := r.deliverLoop(ctx, def, st, 0, true, link, engine.NewStartInstance(r.clk.Now(), vars))
	return err
}

// terminalErr derives a short, human-readable error message from a terminal
// instance state. It prefers the first recorded incident's error text (the most
// concrete description of what went wrong); if no incidents are present it
// falls back to a status-keyed generic message.
func terminalErr(st engine.InstanceState) string {
	if len(st.Incidents) > 0 {
		return st.Incidents[0].Error
	}
	switch st.Status {
	case engine.StatusTerminated:
		return "instance terminated"
	default:
		return "instance failed"
	}
}

// copyVarsForOutcome returns a shallow copy of vars to avoid aliasing the
// live InstanceState.Variables map via the CallOutcome.Output reference.
// Returns nil when vars is nil (preserving the zero value).
func copyVarsForOutcome(vars map[string]any) map[string]any {
	if vars == nil {
		return nil
	}
	out := make(map[string]any, len(vars))
	for k, v := range vars {
		out[k] = v
	}
	return out
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

	// Re-register from current tokens (message-catch intermediate events / receive tasks).
	for _, tok := range st.Tokens {
		if tok.AwaitMessage != "" {
			k := msgKey{Name: tok.AwaitMessage, CorrelationKey: tok.AwaitMessageKey}
			r.msgWaiters[k] = st.InstanceID
		}
	}

	// Re-register from armed message BOUNDARY events. Their host token parks on a
	// task/command (not on the message), so they are not covered by the token loop
	// above; DeliverMessage must still be able to correlate a delivered message to
	// this instance to fire the boundary (ADR-0053).
	for _, w := range st.MessageBoundaryWaiters() {
		k := msgKey{Name: w.Name, CorrelationKey: w.CorrelationKey}
		r.msgWaiters[k] = st.InstanceID
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

// ResolveIncident clears the named incident on an instance, grants addAttempts
// additional retries, and re-invokes the parked action. It is the admin entry
// point for recovering a retry-exhausted activity. Delegates through Deliver so
// the trigger is journalled and persisted.
func (r *Runner) ResolveIncident(ctx context.Context, def *model.ProcessDefinition, instanceID, incidentID string, addAttempts int) (engine.InstanceState, error) {
	st, err := r.Deliver(ctx, def, instanceID, engine.NewResolveIncident(r.clk.Now(), incidentID, addAttempts))
	if err == nil {
		r.obs.incidentsResolved.Add(ctx, 1, metric.WithAttributes(attribute.String("def", def.ID)))
	}
	return st, err
}

// CancelInstance terminates a running instance by delivering a CancelRequested
// trigger. Any definition-level CancelActions run best-effort inside the same
// deliverLoop (failures are logged, never fail the cancel). When CallLinks and
// a DefinitionRegistry are both configured, running async child instances are
// cancelled recursively (best-effort: errors are logged, never returned). Returns
// the terminated parent InstanceState. See ADR-0028, ADR-0032.
func (r *Runner) CancelInstance(ctx context.Context, def *model.ProcessDefinition, instanceID string) (engine.InstanceState, error) {
	// Parent-first: terminate the parent before propagating to children so that
	// no CallNotifier can resume a child-completed parent during propagation.
	st, err := r.Deliver(ctx, def, instanceID, engine.NewCancelRequested(r.clk.Now()))
	if err != nil {
		return st, err
	}
	if r.callLinks != nil && r.defsReg != nil {
		visited := map[string]bool{instanceID: true}
		r.propagateCancel(ctx, instanceID, visited)
	}
	return st, nil
}

// propagateCancel recursively cancels all running async child instances of
// parentID. Every error is logged and swallowed — this is best-effort only
// (ADR-0032). visited is shared across the entire cancel tree so that a node
// reachable via multiple paths (diamond topology) is delivered CancelRequested
// exactly once and never double-cancelled.
func (r *Runner) propagateCancel(ctx context.Context, parentID string, visited map[string]bool) {
	children, err := r.callLinks.ListRunningChildren(ctx, parentID)
	if err != nil {
		r.obs.tel.Logger.LogAttrs(ctx, slog.LevelWarn,
			"runtime: propagateCancel: list running children failed",
			slog.String("parent_id", parentID),
			slog.String("error", err.Error()),
		)
		return
	}
	for _, child := range children {
		if visited[child.ChildInstanceID] {
			continue
		}
		visited[child.ChildInstanceID] = true

		childSt, _, loadErr := r.store.Load(ctx, child.ChildInstanceID)
		if loadErr != nil {
			r.obs.tel.Logger.LogAttrs(ctx, slog.LevelWarn,
				"runtime: propagateCancel: load child instance failed",
				slog.String("child_id", child.ChildInstanceID),
				slog.String("error", loadErr.Error()),
			)
			continue
		}

		ref := fmt.Sprintf("%s:%d", childSt.DefID, childSt.DefVersion)
		childDef, lookupErr := r.defsReg.Lookup(ctx, ref)
		if lookupErr != nil {
			r.obs.tel.Logger.LogAttrs(ctx, slog.LevelWarn,
				"runtime: propagateCancel: child def not found",
				slog.String("child_id", child.ChildInstanceID),
				slog.String("def_ref", ref),
				slog.String("error", lookupErr.Error()),
			)
			continue
		}

		// Deliver CancelRequested directly (parent-first) then recurse into
		// propagateCancel with the SAME shared visited map. Re-entering CancelInstance
		// would allocate a fresh visited map per child, breaking the diamond guard.
		if _, cancelErr := r.Deliver(ctx, childDef, child.ChildInstanceID, engine.NewCancelRequested(r.clk.Now())); cancelErr != nil {
			r.obs.tel.Logger.LogAttrs(ctx, slog.LevelWarn,
				"runtime: propagateCancel: cancel child instance failed",
				slog.String("child_id", child.ChildInstanceID),
				slog.String("error", cancelErr.Error()),
			)
			continue
		}
		// Recurse into the child's own subtree with the shared visited map.
		r.propagateCancel(ctx, child.ChildInstanceID, visited)
	}
}

// safeActionDo invokes a.Do, converting a panic into an error so a buggy or
// malicious service action cannot crash the runner — and with it every in-flight
// instance on the replica. A recovered panic is surfaced as an ordinary action
// error, so callers route it through their normal failure path (a retryable
// ActionFailed for InvokeAction, a best-effort log for InvokeCancelAction).
func safeActionDo(ctx context.Context, a action.ServiceAction, in map[string]any) (out map[string]any, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			out = nil
			err = fmt.Errorf("workflow-runtime: action panicked: %v", rec)
		}
	}()
	return a.Do(ctx, in)
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
		actx, aspan := r.obs.tracer().Start(ctx, "wrkflw.action "+cmd.Name,
			trace.WithAttributes(attribute.String("wrkflw.action", cmd.Name)))
		outcome := "error"
		var elapsed float64
		defer func() {
			r.obs.actionDuration.Record(actx, elapsed,
				metric.WithAttributes(attribute.String("action", cmd.Name), attribute.String("outcome", outcome)))
			aspan.End()
		}()

		a, ok := r.resolveInvokeAction(def, cmd)
		if !ok {
			err := errors.New("unknown action: " + cmd.Name)
			aspan.RecordError(err)
			aspan.SetStatus(codes.Error, err.Error())
			return engine.NewActionFailed(r.clk.Now(), cmd.CommandID, "unknown action: "+cmd.Name, false), nil
		}
		start := r.clk.Now()
		out, err := safeActionDo(actx, a, cmd.Input)
		elapsed = r.clk.Now().Sub(start).Seconds()
		if err != nil {
			aspan.RecordError(err)
			aspan.SetStatus(codes.Error, err.Error())
			return engine.NewActionFailedJittered(r.clk.Now(), cmd.CommandID, err.Error(), true, r.jitter.Fraction()), nil
		}
		outcome = "ok"
		return engine.NewActionCompleted(r.clk.Now(), cmd.CommandID, out), nil

	case engine.InvokeCancelAction:
		// Best-effort, fire-and-forget: run the action for its side effect, log any
		// failure, and NEVER feed a result back or return an error — the instance is
		// already terminal and cancellation must report success regardless (ADR-0028).
		a, ok := r.resolveActionName(def, cmd.Name)
		if !ok {
			r.obs.tel.Logger.LogAttrs(ctx, slog.LevelWarn, "runtime: cancel action not found",
				slog.String("action", cmd.Name))
			return nil, nil
		}
		if _, err := safeActionDo(ctx, a, cmd.Input); err != nil {
			r.obs.tel.Logger.LogAttrs(ctx, slog.LevelError, "runtime: cancel action failed",
				slog.String("action", cmd.Name), slog.Any("error", err))
		}
		return nil, nil

	case engine.CompleteInstance:
		// The terminal outbox event is derived status-driven by
		// terminalOutboxEvent at the deliverLoop terminal edge and written inside
		// the Commit tx; nothing to perform here (ADR-0046).
		return nil, nil

	case engine.FailInstance:
		// The terminal outbox event is derived status-driven by
		// terminalOutboxEvent at the deliverLoop terminal edge and written inside
		// the Commit tx; nothing to perform here (ADR-0046).
		return nil, nil

	case engine.AwaitHuman:
		if r.resolver == nil {
			return nil, fmt.Errorf("workflow-runtime: perform AwaitHuman: no ActorResolver configured")
		}
		if r.tasks == nil {
			return nil, fmt.Errorf("workflow-runtime: perform AwaitHuman: no TaskStore configured")
		}
		// Resolve candidates from the eligibility spec and process variables.
		actors, err := r.resolver.Candidates(ctx, cmd.Eligibility, st.Variables)
		if err != nil {
			return nil, fmt.Errorf("workflow-runtime: resolve candidates: %w", err)
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
			// Snapshot the process variables so attribute-based eligibility predicates
			// that reference data variables (e.g. vars["region"] == "EU") are
			// deterministically evaluated against the state at task-creation time.
			// maps.Clone returns nil when st.Variables is nil, which is safe.
			// Note: this is a SHALLOW copy — top-level keys are copied defensively,
			// but nested maps/slices remain shared with the instance variables;
			// eligibility predicates should rely on top-level scalar variables only.
			Vars: maps.Clone(st.Variables),
		}
		// Copy NodeID from the in-state task record if present.
		if t := st.TaskByToken(cmd.TaskToken); t != nil {
			task.NodeID = t.NodeID
			task.CreatedAt = t.CreatedAt // preserve engine-stamped time
		}
		if err := r.tasks.Upsert(ctx, task); err != nil {
			return nil, fmt.Errorf("workflow-runtime: upsert task: %w", err)
		}
		r.obs.humanTasks.Add(ctx, 1, metric.WithAttributes(attribute.String("event", "created")))
		// No follow-up trigger: the instance parks here.
		return nil, nil

	case engine.UpdateTask:
		if r.tasks == nil {
			return nil, fmt.Errorf("workflow-runtime: perform UpdateTask: no TaskStore configured")
		}
		if err := r.tasks.Upsert(ctx, cmd.Task); err != nil {
			return nil, fmt.Errorf("workflow-runtime: update task: %w", err)
		}
		return nil, nil

	case engine.ScheduleTimer:
		if r.sched == nil {
			return nil, fmt.Errorf("workflow-runtime: perform ScheduleTimer %q: no Scheduler configured", cmd.TimerID)
		}
		if cmd.Kind == engine.TimerRetry {
			r.obs.actionRetries.Add(ctx, 1)
		}
		r.armTimer(def, st.InstanceID, cmd.TimerID, cmd.FireAt)
		return nil, nil

	case engine.CancelTimer:
		if r.sched == nil {
			return nil, fmt.Errorf("workflow-runtime: perform CancelTimer %q: no Scheduler configured", cmd.TimerID)
		}
		r.sched.Cancel(cmd.TimerID)
		return nil, nil

	case engine.ThrowSignal:
		if r.sigbus == nil {
			return nil, fmt.Errorf("workflow-runtime: perform ThrowSignal %q: no SignalBus configured", cmd.Name)
		}
		if err := r.sigbus.Publish(ctx, cmd.Name, cmd.Payload); err != nil {
			return nil, fmt.Errorf("workflow-runtime: perform ThrowSignal %q: %w", cmd.Name, err)
		}
		return nil, nil

	case engine.SendMessage:
		if r.msgSink == nil {
			return nil, fmt.Errorf("workflow-runtime: perform SendMessage %q: no MessageSink configured (use WithMessageSink)", cmd.Name)
		}
		if err := r.msgSink.Send(ctx, OutboundMessage{
			InstanceID:     st.InstanceID,
			Name:           cmd.Name,
			CorrelationKey: cmd.CorrelationKey,
			Payload:        cmd.Payload,
		}); err != nil {
			return nil, fmt.Errorf("workflow-runtime: perform SendMessage %q: %w", cmd.Name, err)
		}
		return nil, nil

	case engine.StartSubInstance:
		// Nil-registry guard: a missing registry is a configuration error, not a
		// retryable runtime failure, so we fail fast with a descriptive message.
		if r.defsReg == nil {
			return nil, fmt.Errorf("workflow-runtime: perform StartSubInstance %q: no definition registry configured (use WithDefinitions)", cmd.DefRef)
		}
		childDef, err := r.defsReg.Lookup(ctx, cmd.DefRef)
		if err != nil {
			return nil, fmt.Errorf("workflow-runtime: perform StartSubInstance %q: registry lookup: %w", cmd.DefRef, err)
		}

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

		// Async path: when a CallLinkStore is configured, the child is started
		// non-blocking. The parent parks at the call node; a notifier delivers
		// the outcome (SubInstanceCompleted / SubInstanceFailed) later.
		if r.callLinks != nil {
			// Compute depth: look up THIS instance's own link (is it itself a child?).
			// Found ⇒ depth = parentLink.Depth + 1; not found ⇒ depth = 1.
			// A store error must NOT be swallowed as "not found": that would
			// miscompute depth and start a child that the guard should have
			// rejected. Propagate it so the caller can retry.
			depth := 1
			parentLink, ok, lerr := r.callLinks.LookupChild(ctx, st.InstanceID)
			if lerr != nil {
				return nil, fmt.Errorf("workflow-runtime: call activity: depth lookup for %q: %w", st.InstanceID, lerr)
			}
			if ok {
				depth = parentLink.Depth + 1
			}
			if depth > maxCallDepth {
				return engine.NewSubInstanceFailed(r.clk.Now(), cmd.CommandID,
					fmt.Sprintf("workflow-runtime: call activity depth limit %d exceeded (possible recursive definition: %q); "+
						"async call activity chain is too deep",
						maxCallDepth, cmd.DefRef),
				), nil
			}

			link := CallLink{
				ChildInstanceID:  childInstanceID,
				ParentInstanceID: st.InstanceID,
				ParentCommandID:  cmd.CommandID,
				ParentDefID:      def.ID,
				ParentDefVersion: def.Version,
				Depth:            depth,
			}

			// Start the child's first burst non-blocking: drive it until it parks or
			// completes. The link is threaded into the child's first Create atomically.
			if err := r.runChild(ctx, childDef, childInstanceID, cmd.Input, &link); err != nil {
				return engine.NewSubInstanceFailed(r.clk.Now(), cmd.CommandID, err.Error()), nil
			}

			// Return nil, nil — no synchronous resume trigger. The parent stays parked
			// at the call node; the engine already handled parking when it emitted
			// StartSubInstance. The notifier will deliver SubInstanceCompleted/Failed later.
			return nil, nil
		}

		// Synchronous path (opt-out: r.callLinks == nil): run the child to completion
		// in-process. This is the VERBATIM original behavior.

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
		if depth >= maxCallDepth {
			return engine.NewSubInstanceFailed(r.clk.Now(), cmd.CommandID,
				fmt.Sprintf("workflow-runtime: call activity depth limit %d exceeded (possible recursive definition: %q); "+
					"the synchronous runner does not support cyclic or deeply nested call activities",
					maxCallDepth, cmd.DefRef),
			), nil
		}
		childCtx := withCallDepth(ctx, depth+1)

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
				fmt.Sprintf("workflow-runtime: call activity child %q parked (status running): "+
					"the synchronous runner does not support children that wait on human tasks, "+
					"timers, or events; async call activity is a future enhancement",
					childInstanceID),
			), nil

		default:
			// StatusFailed or any other non-completed, non-running terminal state.
			// Include the numeric status in the message so failures are diagnosable.
			return engine.NewSubInstanceFailed(r.clk.Now(), cmd.CommandID,
				fmt.Sprintf("workflow-runtime: call activity child %q ended with status %d", childInstanceID, childSt.Status),
			), nil
		}

	default:
		return nil, fmt.Errorf("workflow-runtime: unsupported command %T", c)
	}
}

// armTimer registers timerID on the scheduler with the engine's standard
// fire callback: deliver a TimerFired trigger, retrying on optimistic-CAS
// conflicts. Used by perform(ScheduleTimer) and RehydrateTimers.
func (r *Runner) armTimer(def *model.ProcessDefinition, instanceID, timerID string, fireAt time.Time) {
	r.sched.Schedule(timerID, fireAt, func() {
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
				r.obs.tel.Logger.LogAttrs(fireCtx, slog.LevelError, "runtime: timer fire: Deliver failed",
					append(r.obs.tel.LogAttrs(fireCtx),
						slog.String("timer_id", timerID),
						slog.String("instance_id", instanceID),
						slog.Any("error", err))...)
				return
			}
			// ErrConcurrentUpdate: another Deliver won the CAS; Deliver
			// internally reloads fresh state on the next call. Retry
			// immediately (no sleep needed — store reloads on each Deliver).
		}
		r.obs.tel.Logger.LogAttrs(fireCtx, slog.LevelError, "runtime: timer fire: Deliver permanently dropped after CAS conflicts",
			append(r.obs.tel.LogAttrs(fireCtx),
				slog.String("timer_id", timerID),
				slog.String("instance_id", instanceID),
				slog.Int("attempts", maxAttempts),
				slog.Any("error", err))...)
	})
}

// RehydrateTimers re-arms every persisted armed timer on the scheduler. Call it
// once at startup, after constructing the Runner, to recover timers lost when the
// process restarted. Requires WithScheduler, WithTimerStore, and WithDefinitions.
// A timer whose FireAt is already in the past fires immediately; a re-fire of an
// already-consumed timer is an idempotent engine no-op. Timers whose definition
// the registry cannot resolve are skipped and counted in the returned error.
func (r *Runner) RehydrateTimers(ctx context.Context) error {
	if r.sched == nil || r.timerStore == nil || r.defsReg == nil {
		return fmt.Errorf("workflow-runtime: RehydrateTimers requires WithScheduler, WithTimerStore, and WithDefinitions")
	}
	armed, err := r.timerStore.ListArmed(ctx)
	if err != nil {
		return fmt.Errorf("workflow-runtime: RehydrateTimers: list armed: %w", err)
	}
	var unresolved int
	for _, a := range armed {
		ref := fmt.Sprintf("%s:%d", a.DefID, a.DefVersion)
		def, err := r.defsReg.Lookup(ctx, ref)
		if err != nil {
			unresolved++
			r.obs.tel.Logger.LogAttrs(ctx, slog.LevelError, "runtime: rehydrate: definition not found, skipping timer",
				append(r.obs.tel.LogAttrs(ctx),
					slog.String("def_ref", ref),
					slog.String("timer_id", a.TimerID),
					slog.String("instance_id", a.InstanceID))...)
			continue
		}
		r.armTimer(def, a.InstanceID, a.TimerID, a.FireAt)
	}
	if unresolved > 0 {
		return fmt.Errorf("workflow-runtime: RehydrateTimers: %d timer(s) skipped (definition not found)", unresolved)
	}
	return nil
}

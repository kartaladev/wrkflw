package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/internal/observability"
	"github.com/zakyalvan/krtlwrkflw/runtime/idgen"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/signal"
	"github.com/zakyalvan/krtlwrkflw/scheduling"
)

// ProcessDriver is the reference single-process driver loop.
type ProcessDriver struct {
	cat        action.Catalog
	clk        clock.Clock
	idgen      idgen.Generator
	store      kernel.InstanceStore
	resolver   humantask.ActorResolver
	tasks      humantask.TaskStore
	authz      authz.Authorizer
	sched      kernel.Scheduler
	sigbus     *signal.SignalBus
	defsReg    kernel.DefinitionRegistry
	callLinks  kernel.CallLinkStore
	timerStore kernel.TimerStore
	// jitter supplies the random fraction used to de-synchronize retry backoff.
	// It is sampled at the runtime edge (perform) and recorded on the ActionFailed
	// trigger so that engine replay remains deterministic.
	jitter kernel.JitterSource

	// actionTimeout bounds how long a single service-action invocation may run
	// before its context is cancelled. Defaults to defaultActionTimeout; a
	// non-positive value disables the bound. Set via [WithActionTimeout].
	actionTimeout time.Duration

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
	// Always non-nil after NewProcessDriver (defaults to noop providers + slog.Default()).
	obs *driverObs

	// msgMu guards msgWaiters.
	msgMu sync.Mutex
	// msgWaiters maps a (messageName, correlationKey) pair to the instance ID
	// that is waiting on it. Message catch events are 1:1 (each correlation key
	// routes to exactly one instance), so a simple map suffices.
	msgWaiters map[msgKey]string

	// shutdown aggregates the teardown of resources the driver itself created and
	// owns (currently the default in-process scheduler's gocron goroutine). A
	// consumer-injected component (e.g. a WithScheduler-provided scheduler) is
	// consumer-owned and is deliberately NOT registered here. [ProcessDriver.Shutdown]
	// delegates to this group. The zero value is ready to use.
	shutdown ShutdownGroup

	// ownedScheduler is the in-process default scheduler the driver created when
	// no [WithScheduler] was supplied. It is non-nil only for the owned default;
	// [ProcessDriver.Start] starts it and [ProcessDriver.Shutdown] closes it. A
	// consumer-injected scheduler is consumer-owned, so this stays nil and the
	// consumer manages its lifecycle.
	ownedScheduler *scheduling.Scheduler
}

// NewProcessDriver constructs a ProcessDriver with sensible in-memory defaults
// and any optional overrides supplied as functional options.
//
// Defaults (applied before options):
//   - Catalog: [action.DefaultCatalog] — the process-global action registry.
//     Override via [WithActionCatalog].
//   - Store: [kernel.NewMemInstanceStore] — a transactional in-memory instance
//     store suitable for single-process and test deployments. Override via
//     [WithInstanceStore] to supply a persistent SQL-backed store.
//   - Time source: [clock.System]. Override via [WithClock] (ADR-0003).
//
// Optional capabilities are supplied via functional options; the full set of
// With* functions returning [Option] is (see each for details):
//   - Core overrides: [WithActionCatalog], [WithInstanceStore].
//   - Node-kind capabilities: [WithHumanTasks], [WithScheduler], [WithSignalBus],
//     [WithDefinitions], [WithCallLinkStore], [WithTimerStore].
//   - Execution policy: [WithDefaultRetryPolicy], [WithActionTimeout],
//     [WithExpressionTimeout], [WithConditionEvaluator], [WithJitterSource].
//   - Time source: [WithClock] (default [clock.System]).
//   - Observability: [WithLogger], [WithTracerProvider], [WithMeterProvider].
func NewProcessDriver(opts ...Option) (*ProcessDriver, error) {
	memStore, err := kernel.NewMemInstanceStore()
	if err != nil {
		return nil, fmt.Errorf("workflow-runtime: default instance store: %w", err)
	}
	// Capture the default sentinels before the option loop so we can detect
	// whether the consumer replaced them with custom implementations.
	defaultStore := memStore

	r := &ProcessDriver{
		cat:           action.DefaultCatalog(),
		clk:           clock.System(),
		idgen:         idgen.XID(),
		store:         memStore,
		defsReg:       defaultDefinitionRegistry,
		jitter:        kernel.NewJitterSource(),
		actionTimeout: defaultActionTimeout,
		msgWaiters:    make(map[msgKey]string),
	}
	for _, o := range opts {
		o(r)
	}

	// Default scheduler: when the consumer did not wire one via [WithScheduler],
	// create an in-process gocron-backed scheduler (real clock, single-node) so
	// timer nodes work zero-config. The driver OWNS this default and registers it
	// for teardown via [ProcessDriver.Shutdown]. A consumer-injected scheduler is
	// consumer-owned — left untouched and never closed by the driver.
	customScheduler := r.sched != nil
	if r.sched == nil {
		sched, serr := scheduling.NewScheduler()
		if serr != nil {
			return nil, fmt.Errorf("workflow-runtime: default scheduler: %w", serr)
		}
		r.sched = sched
		r.ownedScheduler = sched
		r.shutdown.AddCloser(sched)
	}

	r.obs = newDriverObs(r.logOpt, r.tpOpt, r.mpOpt)
	r.logConstructionSummary(defaultStore, customScheduler)
	return r, nil
}

// Start starts the driver-owned in-process default scheduler (created by
// [NewProcessDriver] when no [WithScheduler] was supplied), binding its lifetime
// to ctx: cancelling ctx stops the scheduler. Start is idempotent.
//
// It is optional — the owned scheduler also auto-starts on the first timer it is
// asked to arm — but calling Start lets a consumer tie the scheduler's goroutine
// to their application context and fail fast if it cannot start. When the driver
// uses a consumer-injected scheduler (via [WithScheduler]), that scheduler is
// consumer-owned and Start is a no-op; the consumer starts it themselves.
func (r *ProcessDriver) Start(ctx context.Context) error {
	if r.ownedScheduler == nil {
		return nil
	}
	if err := r.ownedScheduler.Start(ctx); err != nil {
		return fmt.Errorf("workflow-runtime: start scheduler: %w", err)
	}
	return nil
}

// Shutdown releases the resources the ProcessDriver itself created and owns —
// currently the default in-process scheduler's gocron goroutine (see
// [NewProcessDriver]). Consumer-injected collaborators (a [WithScheduler]-provided
// scheduler, a [WithInstanceStore]-provided store, …) are consumer-owned and are
// NOT torn down here.
//
// Shutdown honours ctx for a bounded drain, aggregates every closer's error with
// [errors.Join], and is idempotent (a second call returns nil). Its signature
// matches samber/do's ShutdownerWithContextAndError, so a do.Provide(driver) is
// released by inj.ShutdownWithContext(ctx).
func (r *ProcessDriver) Shutdown(ctx context.Context) error {
	return r.shutdown.Shutdown(ctx)
}

// onOff returns "on" when v is true and "off" otherwise.
func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

// logConstructionSummary emits a single DEBUG log record summarising the
// ProcessDriver's wiring after construction. defaultStore is the MemInstanceStore
// that was created inside NewProcessDriver as the pre-option default; when r.store
// still points to that same value after the option loop, the store is in-memory
// (non-durable); otherwise a custom implementation was supplied.
// defOrigin returns "default-global" when the driver is using the process-global
// DefaultDefinitionRegistry and "custom" otherwise, mirroring the storeLabel /
// catalogLabel helpers in logConstructionSummary.
func defOrigin(defsReg kernel.DefinitionRegistry) string {
	if defsReg == defaultDefinitionRegistry {
		return "default-global"
	}
	return "custom"
}

func (r *ProcessDriver) logConstructionSummary(defaultStore kernel.InstanceStore, customScheduler bool) {
	storeLabel := "in-memory(non-durable)"
	if r.store != defaultStore {
		storeLabel = "custom"
	}

	catalogLabel := "custom"
	if r.cat == action.DefaultCatalog() {
		catalogLabel = "default-global"
	}

	// The driver always has a scheduler after construction: a consumer-injected
	// one (custom) or the driver-owned in-process default.
	schedulerLabel := "default-inprocess"
	if customScheduler {
		schedulerLabel = "custom"
	}

	r.obs.tel.Logger.LogAttrs(
		context.Background(),
		slog.LevelDebug,
		"ProcessDriver constructed",
		slog.String("store", storeLabel),
		slog.String("catalog", catalogLabel),
		slog.String("scheduler", schedulerLabel),
		slog.String("signalBus", onOff(r.sigbus != nil)),
		slog.String("humanTasks", onOff(r.tasks != nil)),
		slog.String("definitions", defOrigin(r.defsReg)),
		slog.String("callLinks", onOff(r.callLinks != nil)),
		slog.String("timerStore", onOff(r.timerStore != nil)),
		slog.String("actionTimeout", r.actionTimeout.String()),
		slog.Bool("retryDefault", r.defaultRetryPolicy != nil),
		slog.Bool("conditionEval", r.conditionEval != nil),
		slog.String("hint", "in-memory store is not durable; for production wire persistence.OpenPostgres/OpenMySQL/OpenSQLite + runtime.WithInstanceStore, and enable WithScheduler/WithTimerStore/WithCallLinkStore as needed"),
	)
}

// Drive starts an instance and drives it to a terminal state or until the engine
// parks (e.g. awaiting a human task). It returns the state at the point it stopped.
func (r *ProcessDriver) Drive(ctx context.Context, def *model.ProcessDefinition, instanceID string, vars map[string]any) (engine.InstanceState, error) {
	ctx, span := r.obs.tracer().Start(ctx, "wrkflw.runner.Run", trace.WithAttributes(
		attribute.String("wrkflw.instance_id", instanceID),
		attribute.String("wrkflw.def_id", def.ID),
		attribute.Int("wrkflw.def_version", def.Version),
	))
	defer span.End()
	if instanceID == "" {
		id, gerr := r.idgen.NewID()
		if gerr != nil {
			span.RecordError(gerr)
			return engine.InstanceState{}, fmt.Errorf("workflow-runtime: run: generate id: %w", gerr)
		}
		instanceID = id
	}
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
// HumanCompleted that arrive after Drive has returned (parked at a human task),
// and for TimerFired triggers delivered by the scheduler's fire callback.
//
// Authorization contract: human-task triggers (HumanClaimed, HumanReassigned,
// HumanCompleted) MUST originate from TaskService, which performs authorization
// before returning the trigger. Delivering such a trigger from any other source
// bypasses authorization entirely — the engine core is authorization-unaware by
// design. It is the caller's responsibility to ensure human-task triggers pass
// through TaskService.
func (r *ProcessDriver) Deliver(ctx context.Context, def *model.ProcessDefinition, instanceID string, trg engine.Trigger) (engine.InstanceState, error) {
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
// shared by Drive and Deliver.
//
// token is the current optimistic-concurrency token; create=true on the very
// first step (Drive path, no row yet) and false on all subsequent steps.
//
// firstCallLink, when non-nil, is attached to the FIRST applied step's
// AppliedStep.NewCallLink (the create step) and then cleared. All existing
// callers (Drive, Deliver) pass nil — no behavior change for them. The internal
// runChild helper passes the link so the child's Create records it atomically.
//
// After each committed save, if a SignalBus or message waiters are configured,
// the loop reconciles them so that a future [SignalBus.Publish] reaches this
// instance.
func (r *ProcessDriver) deliverLoop(
	ctx context.Context,
	def *model.ProcessDefinition,
	st engine.InstanceState,
	token kernel.Version,
	create bool,
	firstCallLink *kernel.CallLink,
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
		events = append(events, outboundMessageEvents(st, res.Commands)...)

		// Compute a CallOutcome when this step transitions the instance into a
		// terminal status AND a CallLinkStore is configured. The InstanceStore's Commit
		// implementation uses this to flip the call link to terminal atomically
		// (one transaction / one MemInstanceStore lock). For root instances the Store
		// treats a missing link as a no-op, so setting CallOutcome unconditionally
		// on terminal is safe even without special-casing here.
		var outcome *kernel.CallOutcome
		if r.callLinks != nil && isTerminal(st.Status) && !isTerminal(prevStatus) {
			switch st.Status {
			case engine.StatusCompleted:
				outcome = &kernel.CallOutcome{Completed: true, Output: copyVarsForOutcome(st.Variables)}
			default: // StatusFailed, StatusTerminated, or any other terminal
				outcome = &kernel.CallOutcome{Completed: false, Err: terminalErr(st)}
			}
		}

		var timerArms []kernel.ArmedTimer
		var timerCancels []string
		if r.timerStore != nil {
			// armedRecurring reports whether the fired timer is armed with a
			// recurring trigger, so timerOpsFor knows a recurring timer must
			// survive its fire (the native scheduler re-arms it) rather than be
			// consumed. It reads the armed set lazily — timerOpsFor only calls it
			// for a TimerFired trigger — and defaults to non-recurring (safe:
			// consume) on any lookup failure or unknown timer.
			timerArms, timerCancels = timerOpsFor(res.Commands, t, st.DefID, st.DefVersion, st.InstanceID, r.clk.Now(),
				func(timerID string) bool { return r.armedTimerRecurring(stepCtx, st.InstanceID, timerID) })
		}

		appliedStep := kernel.AppliedStep{State: st, Trigger: t, Events: events, CallOutcome: outcome, TimerArms: timerArms, TimerCancels: timerCancels}

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

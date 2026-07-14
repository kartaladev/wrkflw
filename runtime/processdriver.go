package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/clock"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/internal/observability"
	"github.com/kartaladev/wrkflw/runtime/idgen"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/runtime/signal"
	"github.com/kartaladev/wrkflw/runtime/validation"
	"github.com/kartaladev/wrkflw/scheduling"
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
	// routes to exactly one instance), so a simple map suffices. A violation of
	// that 1:1 contract — two running instances awaiting the same (name, key) — is
	// WARN-logged by syncMsgWaiters before the last-writer-wins overwrite (ADR-0125);
	// delivery stays point-to-point (fan-out is the signal model).
	msgWaiters map[msgKey]string

	// shutdown aggregates the teardown of resources the driver itself created and
	// owns (currently the default in-process scheduler's gocron goroutine). A
	// consumer-injected component (e.g. a WithScheduler-provided scheduler) is
	// consumer-owned and is deliberately NOT registered here. [ProcessDriver.Shutdown]
	// delegates to this group. The zero value is ready to use.
	shutdown ShutdownGroup

	// draining is set true at the start of Shutdown; once set, admit() refuses new
	// externally-initiated work so it is rejected with ErrDriverShuttingDown.
	draining atomic.Bool
	// inflight counts admitted, currently-executing units of work (each deliverLoop-
	// driving call and each in-flight timer continuation). Shutdown waits on it to drain.
	inflight sync.WaitGroup
	// shutdownTimeout is the fallback drain deadline applied by Shutdown ONLY when the
	// ctx passed to Shutdown carries no deadline of its own. Zero = no fallback. Set via
	// WithShutdownTimeout.
	shutdownTimeout time.Duration

	// ownedScheduler is the in-process default scheduler the driver created when
	// no [WithScheduler] was supplied. It is non-nil only for the owned default;
	// [ProcessDriver.Start] starts it and [ProcessDriver.Shutdown] closes it. A
	// consumer-injected scheduler is consumer-owned, so this stays nil and the
	// consumer manages its lifecycle.
	ownedScheduler *scheduling.Scheduler

	// gate is the executor-side validation memoizer (runtime/validation.Gate)
	// used by validateInput to compile-once-and-cache each
	// validate.ValidationStrategy by its descriptor (kind + schema). Always
	// non-nil after NewProcessDriver.
	gate *validation.Gate
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

	driver := &ProcessDriver{
		cat:           action.DefaultCatalog(),
		clk:           clock.System(),
		idgen:         idgen.XID(),
		store:         memStore,
		defsReg:       defaultDefinitionRegistry,
		jitter:        kernel.NewJitterSource(),
		actionTimeout: defaultActionTimeout,
		msgWaiters:    make(map[msgKey]string),
		gate:          validation.NewGate(),
	}
	for _, o := range opts {
		o(driver)
	}

	// Default scheduler: when the consumer did not wire a usable one via
	// [WithScheduler], create an in-process gocron-backed scheduler (real clock,
	// single-node) so timer nodes work zero-config. The driver OWNS this default
	// and registers it for teardown via [ProcessDriver.Shutdown]. A consumer-injected
	// scheduler is consumer-owned — left untouched and never closed by the driver.
	//
	// A nil scheduler — including a TYPED nil (a nil concrete pointer boxed in a
	// non-nil interface) — is treated as "not provided" and falls back to the
	// default, consistent with WithInstanceStore(nil)/WithActionCatalog(nil) being
	// ignored, so a stray typed nil cannot slip past and panic on the first timer.
	customScheduler := !isNilScheduler(driver.sched)
	if !customScheduler {
		var schedOpts []scheduling.Option
		// Auto-wire self-rehydration when a durable timer store is configured.
		// defsReg is always non-nil (defaults to the process-global
		// defaultDefinitionRegistry), so the check is omitted. Rehydration is
		// best-effort: timers whose definitions are not yet registered are skipped
		// with a WARN (see kernel.ErrUnresolvedTimerDefinitions). The provider is
		// a thunk that captures the driver pointer (already allocated); it is
		// resolved lazily at first Start/Schedule, by which time the driver is
		// fully constructed — breaking the driver↔jobstore↔scheduler cycle.
		if driver.timerStore != nil {
			schedOpts = append(schedOpts, scheduling.WithJobStore(func() kernel.JobStore { return NewJobStore(driver) }))
		}
		sched, serr := scheduling.NewScheduler(schedOpts...)
		if serr != nil {
			return nil, fmt.Errorf("workflow-runtime: default scheduler: %w", serr)
		}
		driver.sched = sched
		driver.ownedScheduler = sched
		// Register a deadline-raced closer instead of AddCloser so Shutdown's ctx actually
		// bounds the scheduler drain (audit Finding 3). sched.Close() blocks on gocron's
		// own stop timeout; racing it against ctx.Done() lets a caller-supplied deadline win.
		// If ctx wins, Close keeps running in its goroutine and finishes shortly after
		// (bounded by gocron's stop timeout) — not leaked indefinitely.
		driver.shutdown.Add(func(ctx context.Context) error {
			done := make(chan error, 1)
			go func() { done <- sched.Close() }()
			select {
			case err := <-done:
				return err
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}

	driver.obs = newDriverObs(driver.logOpt, driver.tpOpt, driver.mpOpt)
	driver.logConstructionSummary(defaultStore, customScheduler)
	return driver, nil
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
func (driver *ProcessDriver) Start(ctx context.Context) error {
	if driver.ownedScheduler == nil {
		return nil
	}
	if err := driver.ownedScheduler.Start(ctx); err != nil {
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
// Shutdown performs a graceful shutdown of the driver's own execution paths:
//
//  1. It sets the draining flag so every externally-initiated entry point
//     (Drive, ApplyTrigger, DeliverMessage, BroadcastSignal, CancelInstance,
//     ResolveIncident, ReverseInstance, and timer-start fires) rejects new work
//     with [ErrDriverShuttingDown].
//  2. It closes the owned scheduler (deadline-raced so ctx bounds the drain),
//     which stops dispatching and waits for in-flight timer fires to finish.
//  3. It waits for consumer-initiated deliverLoops (Drive/ApplyTrigger calls) still
//     running to complete, bounded by ctx. On ctx expiry it returns
//     [ErrDrainTimeout] (joined) WITHOUT force-cancelling in-flight work — that
//     work keeps running to completion on its own goroutine.
//
// Ordering invariant: step 2 precedes step 3 so that the only post-draining source
// of a new inflight reservation (an in-flight timer continuation) is fully drained
// before waitInflight's WaitGroup.Wait runs, ruling out an Add-after-Wait panic.
//
// Consumer-injected collaborators (a [WithScheduler]-provided scheduler, a
// [WithInstanceStore]-provided store, …) are consumer-owned and are NOT torn down
// here. Shutdown honours ctx for a bounded drain (or the [WithShutdownTimeout]
// fallback when ctx carries no deadline), aggregates errors with [errors.Join],
// and is idempotent (a second call returns nil). Its signature matches samber/do's
// ShutdownerWithContextAndError, so a do.Provide(driver) is released by
// inj.ShutdownWithContext(ctx).
func (driver *ProcessDriver) Shutdown(ctx context.Context) error {
	// 1. Stop admitting new external work. Set before anything else so a command
	//    racing Shutdown is rejected rather than admitted mid-teardown.
	driver.draining.Store(true)

	// Apply the WithShutdownTimeout fallback iff ctx carries no deadline (ADR-0133).
	ctx, cancel := driver.effectiveShutdownCtx(ctx)
	defer cancel()

	// 2. Close the owned scheduler: gocron stops dispatching and waits for in-flight
	//    timer fires (which hold reserveInternal slots) to finish. Bounded by ctx via
	//    the deadline-raced closer registered in NewProcessDriver. A consumer-injected
	//    scheduler is not registered, so this is a no-op for it.
	schedErr := driver.shutdown.Shutdown(ctx)

	// 3. Wait for consumer-initiated deliverLoops still running. By now no new inflight
	//    Add can occur: draining rejects external work, and the only internal source
	//    (timer fires) drained in step 2. This ordering rules out Add-after-Wait.
	drainErr := driver.waitInflight(ctx)

	return errors.Join(schedErr, drainErr)
}

// isNilScheduler reports whether s is unusable as a scheduler: either an untyped
// nil interface, or a typed nil (a nil concrete pointer/map/chan/func boxed in a
// non-nil interface). The latter would otherwise pass a plain `s != nil` check and
// panic on first use, so NewProcessDriver treats both as "no scheduler supplied".
func isNilScheduler(s kernel.Scheduler) bool {
	if s == nil {
		return true
	}
	rv := reflect.ValueOf(s)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Chan, reflect.Func, reflect.Slice, reflect.UnsafePointer:
		return rv.IsNil()
	default:
		return false
	}
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
// that was created inside NewProcessDriver as the pre-option default; when driver.store
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

func (driver *ProcessDriver) logConstructionSummary(defaultStore kernel.InstanceStore, customScheduler bool) {
	storeLabel := "in-memory(non-durable)"
	if driver.store != defaultStore {
		storeLabel = "custom"
	}

	catalogLabel := "custom"
	if driver.cat == action.DefaultCatalog() {
		catalogLabel = "default-global"
	}

	// The driver always has a scheduler after construction: a consumer-injected
	// one (custom) or the driver-owned in-process default.
	schedulerLabel := "default-inprocess"
	if customScheduler {
		schedulerLabel = "custom"
	}

	driver.obs.tel.Logger.LogAttrs(
		context.Background(),
		slog.LevelDebug,
		"ProcessDriver constructed",
		slog.String("store", storeLabel),
		slog.String("catalog", catalogLabel),
		slog.String("scheduler", schedulerLabel),
		slog.String("signalBus", onOff(driver.sigbus != nil)),
		slog.String("humanTasks", onOff(driver.tasks != nil)),
		slog.String("definitions", defOrigin(driver.defsReg)),
		slog.String("callLinks", onOff(driver.callLinks != nil)),
		slog.String("timerStore", onOff(driver.timerStore != nil)),
		slog.String("actionTimeout", driver.actionTimeout.String()),
		slog.Bool("retryDefault", driver.defaultRetryPolicy != nil),
		slog.Bool("conditionEval", driver.conditionEval != nil),
		slog.String("hint", "in-memory store is not durable; for production wire persistence.OpenPostgres/OpenMySQL/OpenSQLite + runtime.WithInstanceStore, and enable WithScheduler/WithTimerStore/WithCallLinkStore as needed"),
	)
}

// Drive starts an instance and drives it to a terminal state or until the engine
// parks (e.g. awaiting a human task). It returns the state at the point it stopped.
func (driver *ProcessDriver) Drive(ctx context.Context, def *model.ProcessDefinition, instanceID string, vars map[string]any) (engine.InstanceState, error) {
	release, ok := driver.admit()
	if !ok {
		return engine.InstanceState{}, ErrDriverShuttingDown
	}
	defer release()
	ctx, span := driver.obs.tracer().Start(ctx, "wrkflw.runner.Run", trace.WithAttributes(
		attribute.String("wrkflw.instance_id", instanceID),
		attribute.String("wrkflw.def_id", def.ID),
		attribute.Int("wrkflw.def_version", def.Version),
	))
	defer span.End()
	if instanceID == "" {
		id, gerr := driver.idgen.NewID()
		if gerr != nil {
			span.RecordError(gerr)
			return engine.InstanceState{}, fmt.Errorf("workflow-runtime: run: generate id: %w", gerr)
		}
		instanceID = id
		// The span was opened before the id existed; record the minted id so the
		// trace carries it instead of the empty argument.
		span.SetAttributes(attribute.String("wrkflw.instance_id", instanceID))
	}
	st := engine.InstanceState{InstanceID: instanceID}
	out, err := driver.deliverLoop(ctx, def, st, 0, true, nil, engine.NewStartInstance(driver.clk.Now(), vars))
	if errors.Is(err, engine.ErrNoManualStart) {
		// def has ONLY event-triggered start events (message/signal/timer) — Drive
		// always asks for the manual/"none" start via an empty StartNodeID, so wrap
		// the engine's sentinel once with a friendly hint at the event entry points
		// that DO work (ADR-0121). Wrapped with %w so errors.Is(err,
		// engine.ErrNoManualStart) still holds for the caller.
		err = fmt.Errorf("workflow-runtime: definition %s has no manual start; use an event entry point (DeliverMessage / BroadcastSignal / timer start): %w", def.ID, err)
	}
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	} else {
		span.SetAttributes(attribute.String("wrkflw.status", statusName(out.Status)))
	}
	return out, err
}

// createAtNode starts a new instance seeded at a specific start node (ADR-0121)
// and drives it through deliverLoop, exactly like Drive but for an explicit start
// node rather than the definition's manual start. When instanceID is empty a
// fresh id is minted via the driver's id generator (signal/timer starts); a
// non-empty instanceID is used verbatim (the message-start deterministic id).
//
// It surfaces kernel.ErrInstanceExists from Store.Create UNCHANGED — the caller
// decides whether a pre-existing id is a duplicate no-op (message-start dedup) or
// a real error.
func (driver *ProcessDriver) createAtNode(ctx context.Context, def *model.ProcessDefinition, nodeID, instanceID string, vars map[string]any) (engine.InstanceState, error) {
	if instanceID == "" {
		id, err := driver.idgen.NewID()
		if err != nil {
			return engine.InstanceState{}, fmt.Errorf("workflow-runtime: create-at-node: generate id: %w", err)
		}
		instanceID = id
	}
	st := engine.InstanceState{InstanceID: instanceID}
	return driver.deliverLoop(ctx, def, st, 0, true, nil, engine.NewStartInstanceAtNode(driver.clk.Now(), nodeID, vars))
}

// resolveInstanceDef loads instanceID's snapshot and resolves its definition from
// the registry via the snapshot's own DefID/DefVersion. It is how the message
// correlate path recovers the definition the caller does not supply.
func (driver *ProcessDriver) resolveInstanceDef(ctx context.Context, instanceID string) (*model.ProcessDefinition, error) {
	st, _, err := driver.store.Load(ctx, instanceID)
	if err != nil {
		return nil, fmt.Errorf("workflow-runtime: resolve instance definition: load %q: %w", instanceID, err)
	}
	def, err := driver.defsReg.Lookup(ctx, model.Version(st.DefID, st.DefVersion))
	if err != nil {
		return nil, fmt.Errorf("workflow-runtime: resolve instance definition: lookup %s: %w", model.Version(st.DefID, st.DefVersion), err)
	}
	return def, nil
}

// listDefinitions returns every registered definition when the registry supports
// enumeration (kernel.DefinitionLister), or nil otherwise. A registry that does
// not implement DefinitionLister simply disables event-based START; correlating
// a message to an already-running instance still works through Lookup alone.
func (driver *ProcessDriver) listDefinitions(ctx context.Context) []*model.ProcessDefinition {
	lister, ok := driver.defsReg.(kernel.DefinitionLister)
	if !ok {
		return nil
	}
	return lister.ListDefinitions(ctx)
}

// ApplyTrigger loads the current instance state, applies one trigger via engine.Step,
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
func (driver *ProcessDriver) ApplyTrigger(ctx context.Context, def *model.ProcessDefinition, instanceID string, trg engine.Trigger) (engine.InstanceState, error) {
	ctx, span := driver.obs.tracer().Start(ctx, "wrkflw.runner.ApplyTrigger", trace.WithAttributes(
		attribute.String("wrkflw.instance_id", instanceID),
		attribute.String("wrkflw.trigger", triggerName(trg)),
	))
	defer span.End()
	st, token, err := driver.store.Load(ctx, instanceID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return engine.InstanceState{}, fmt.Errorf("workflow-runtime: deliver: load: %w", err)
	}
	out, err := driver.deliverLoop(ctx, def, st, token, false, nil, trg)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return out, err
}

// deliverLoop applies triggers from queue and then any follow-up triggers emitted
// by perform (action results, etc.) until all commands are resolved or the engine
// parks. It encapsulates the Step→terminalOutboxEvent→Create/Commit→perform cycle
// shared by Drive and ApplyTrigger.
//
// token is the current optimistic-concurrency token; create=true on the very
// first step (Drive path, no row yet) and false on all subsequent steps.
//
// firstCallLink, when non-nil, is attached to the FIRST applied step's
// AppliedStep.NewCallLink (the create step) and then cleared. All existing
// callers (Drive, ApplyTrigger) pass nil — no behavior change for them. The internal
// runChild helper passes the link so the child's Create records it atomically.
//
// After each committed save, if a SignalBus or message waiters are configured,
// the loop reconciles them so that a future [SignalBus.Publish] reaches this
// instance.
func (driver *ProcessDriver) deliverLoop(
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

		if err := driver.validateInput(ctx, def, st, t); err != nil {
			return st, fmt.Errorf("workflow-runtime: validate input: %w", err)
		}

		stepCtx, span := driver.obs.tracer().Start(ctx, "wrkflw.step", trace.WithAttributes(
			attribute.String("wrkflw.instance_id", st.InstanceID),
			attribute.String("wrkflw.def_id", def.ID),
			attribute.String("wrkflw.trigger", triggerName(t)),
		))
		start := driver.clk.Now()
		res, err := engine.Step(stepCtx, def, st, t, engine.StepOptions{
			DefaultRetryPolicy:  driver.defaultRetryPolicy,
			OverrideRetryPolicy: driver.overrideRetryPolicy(def, st, t),
			Evaluator:           driver.conditionEval,
		})
		driver.obs.stepDuration.Record(stepCtx, driver.clk.Now().Sub(start).Seconds(),
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
			driver.obs.instStarted.Add(ctx, 1, metric.WithAttributes(attribute.String("def", def.ID)))
			driver.obs.instActive.Add(ctx, 1)
		}
		if isTerminal(st.Status) && !isTerminal(prevStatus) {
			driver.obs.instCompleted.Add(ctx, 1, metric.WithAttributes(
				attribute.String("def", def.ID),
				attribute.String("status", statusName(st.Status)),
			))
			driver.obs.instActive.Add(ctx, -1)
		}
		if len(st.Incidents) > prevIncidents {
			driver.obs.incidentsRaised.Add(ctx, 1, metric.WithAttributes(attribute.String("def", def.ID)))
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
		if driver.callLinks != nil && isTerminal(st.Status) && !isTerminal(prevStatus) {
			switch st.Status {
			case engine.StatusCompleted:
				outcome = &kernel.CallOutcome{Completed: true, Output: copyVarsForOutcome(st.Variables)}
			default: // StatusFailed, StatusTerminated, or any other terminal
				outcome = &kernel.CallOutcome{Completed: false, Err: terminalErr(st)}
			}
		}

		var timerArms []kernel.ArmedTimer
		var timerCancels []string
		if driver.timerStore != nil {
			// armedRecurring reports whether the fired timer is armed with a
			// recurring trigger, so timerOpsFor knows a recurring timer must
			// survive its fire (the native scheduler re-arms it) rather than be
			// consumed. It reads the armed set lazily — timerOpsFor only calls it
			// for a TimerFired trigger — and defaults to non-recurring (safe:
			// consume) on any lookup failure or unknown timer.
			timerArms, timerCancels = timerOpsFor(res.Commands, t, st.DefID, st.DefVersion, st.InstanceID, driver.clk.Now(),
				func(timerID string) bool { return driver.armedTimerRecurring(stepCtx, st.InstanceID, timerID) })
		}

		appliedStep := kernel.AppliedStep{State: st, Trigger: t, Events: events, CallOutcome: outcome, TimerArms: timerArms, TimerCancels: timerCancels}

		if create {
			// Attach the firstCallLink to the Create step (child async path only).
			// After the first iteration this is nil for all callers.
			appliedStep.NewCallLink = firstCallLink
			firstCallLink = nil // consumed; cleared so subsequent steps don't carry it
			token, err = driver.store.Create(ctx, appliedStep)
			create = false
		} else {
			token, err = driver.store.Commit(ctx, token, appliedStep)
		}
		if err != nil {
			return st, fmt.Errorf("workflow-runtime: commit: %w", err)
		}

		// Reconcile signal-bus and message waiters after each committed save.
		driver.syncWaiters(st)

		for _, c := range res.Commands {
			next, err := driver.perform(stepCtx, def, st, c)
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

// validateInput enforces the target node's validation strategy against an external-input trigger's
// payload BEFORE Step, so a failure rejects the trigger before any state is committed. Returns nil
// when the trigger is not input-bearing or the target node has no validation slot.
func (driver *ProcessDriver) validateInput(ctx context.Context, def *model.ProcessDefinition, st engine.InstanceState, trg engine.Trigger) error {
	node, ok := engine.TargetNode(def, st, trg)
	if !ok {
		return nil
	}
	strat := model.ValidationStrategyFor(node)
	if strat == nil {
		return nil
	}
	if err := driver.gate.Validate(ctx, strat, inputOf(trg)); err != nil {
		return err // already wraps validation.ErrInvalidInput; errors.Is survives the caller's %w
	}
	return nil
}

// inputOf extracts the external-input payload carried by trg, or nil for any trigger kind that does
// not carry one (engine.TargetNode already gates which trigger kinds reach here).
func inputOf(trg engine.Trigger) map[string]any {
	switch t := trg.(type) {
	case engine.StartInstance:
		return t.Vars
	case engine.MessageReceived:
		return t.Payload
	case engine.HumanCompleted:
		return t.Output
	default:
		return nil
	}
}

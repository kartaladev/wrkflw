package runtime

import (
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/internal/expreval"
	"github.com/zakyalvan/krtlwrkflw/internal/observability"
	"github.com/zakyalvan/krtlwrkflw/runtime/idgen"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/signal"
)

// Option is a functional option for ProcessDriver. All dependencies — including
// the service-action catalog and instance store — are configurable via options.
// NewProcessDriver applies in-memory defaults before running the option list.
type Option func(*ProcessDriver)

// WithActionCatalog sets the service-action catalog. A nil cat is ignored, so
// the process-global action.DefaultCatalog() registry remains in effect.
func WithActionCatalog(cat action.Catalog) Option {
	return func(r *ProcessDriver) {
		if cat != nil {
			r.cat = cat
		}
	}
}

// WithInstanceStore sets the transactional instance store. A nil store is
// ignored, so the default in-memory MemInstanceStore remains in effect.
func WithInstanceStore(store kernel.InstanceStore) Option {
	return func(r *ProcessDriver) {
		if store != nil {
			r.store = store
		}
	}
}

// defaultActionTimeout bounds each service-action invocation unless overridden
// via [WithActionTimeout]. It guards against a hung action stalling an instance
// and tying up the goroutine indefinitely.
const defaultActionTimeout = 30 * time.Second

// WithActionTimeout sets the maximum duration a single service-action invocation
// may run before its context is cancelled. The default is 30s. A non-positive d
// disables the bound (no deadline is applied). The action's Do must honour ctx
// cancellation for the timeout to take effect; a timed-out action surfaces as a
// retryable failure.
func WithActionTimeout(d time.Duration) Option { return func(r *ProcessDriver) { r.actionTimeout = d } }

// WithHumanTasks wires the human-task capability into the ProcessDriver. Without this
// option, any process that reaches a user-task node will return a descriptive
// error rather than panic.
//
//   - resolver resolves an eligibility spec to the candidate actor list.
//   - tasks persists human-task records.
//   - az authorizes actors against task eligibility specs (used by TaskService,
//     not by the engine core).
func WithHumanTasks(resolver humantask.ActorResolver, tasks humantask.TaskStore, az authz.Authorizer) Option {
	return func(r *ProcessDriver) {
		r.resolver = resolver
		r.tasks = tasks
		r.authz = az
	}
}

// WithScheduler wires a Scheduler into the ProcessDriver, enabling timer commands
// (ScheduleTimer / CancelTimer). Without this option any process that reaches a
// timer node will return a descriptive error.
func WithScheduler(sched kernel.Scheduler) Option {
	return func(r *ProcessDriver) { r.sched = sched }
}

// WithSignalBus wires a [SignalBus] into the ProcessDriver, enabling signal throw
// commands (ThrowSignal). Without this option any process that reaches a signal
// throw node will return a descriptive error.
//
// After each deliverLoop iteration the runner reconciles the instance's
// AwaitSignal tokens with the bus (via [SignalBus.Sync]) so that a later
// [SignalBus.Publish] reaches all parked instances.
func WithSignalBus(bus *signal.SignalBus) Option {
	return func(r *ProcessDriver) { r.sigbus = bus }
}

// WithDefinitions overrides the DefinitionRegistry used by the ProcessDriver for
// resolving [engine.StartSubInstance] commands (call activities). A nil reg is
// ignored — the process-global [DefaultDefinitionRegistry] remains in effect,
// matching the nil-ignored contract of [WithActionCatalog] and [WithInstanceStore].
//
// The registry resolves DefRef strings (as stored on KindCallActivity nodes) to
// *model.ProcessDefinition values. Use [kernel.NewMapDefinitionRegistry] to build
// an immutable in-memory registry from a plain map, or
// [kernel.NewMemDefinitionRegistry] for a mutable, incrementally-populated one.
//
// A zero-config [NewProcessDriver] already uses [DefaultDefinitionRegistry];
// call activities only error when the requested DefRef is not found in that
// registry. Use [RegisterDefinition] to populate the global default at init time,
// or pass an isolated registry here for test isolation.
func WithDefinitions(reg kernel.DefinitionRegistry) Option {
	return func(r *ProcessDriver) {
		if reg != nil {
			r.defsReg = reg
		}
	}
}

// WithCallLinkStore wires a [CallLinkStore] into the ProcessDriver, enabling the
// non-blocking (async) path for [engine.StartSubInstance] commands (call
// activities). When this option is set, [perform] records the parent↔child link
// and starts the child's first burst without waiting for the child to complete —
// the parent parks at the call node until a notifier delivers the outcome. When
// this option is NOT set, the synchronous behavior (run child to completion
// in-process) is preserved verbatim.
func WithCallLinkStore(store kernel.CallLinkStore) Option {
	return func(r *ProcessDriver) { r.callLinks = store }
}

// WithTimerStore wires a [TimerStore] into the ProcessDriver. When set, the runtime
// records each armed/cancelled timer into the AppliedStep so the Store persists
// them atomically with state, and [ProcessDriver.RehydrateTimers] can re-arm them on
// restart. Absent this option, timers are in-memory only and lost on restart.
func WithTimerStore(store kernel.TimerStore) Option {
	return func(r *ProcessDriver) { r.timerStore = store }
}

// WithJitterSource overrides the retry-backoff jitter source (default: [NewJitterSource]).
// Inject a deterministic source in tests to produce predictable fire-at times.
func WithJitterSource(src kernel.JitterSource) Option {
	return func(r *ProcessDriver) { r.jitter = src }
}

// WithDefaultRetryPolicy sets the fallback retry policy applied to any action-bearing
// node that declares no RetryPolicy of its own. Without this option, retry is disabled
// by default and a failed action behaves as before (error boundary or instance failure).
//
// The policy value is copied on each call, so subsequent mutations by the caller do
// not affect the ProcessDriver.
func WithDefaultRetryPolicy(p model.RetryPolicy) Option {
	return func(r *ProcessDriver) { r.defaultRetryPolicy = &p }
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
	return func(r *ProcessDriver) {
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
	return func(r *ProcessDriver) {
		if eval != nil {
			r.conditionEval = eval
		}
	}
}

// WithClock sets the time source the ProcessDriver uses to stamp triggers,
// step-duration metrics, and armed-timer times. Default: clock.System().
// A nil clock is ignored. Inject a fake clock in tests for determinism (ADR-0003).
func WithClock(clk clock.Clock) Option {
	return func(r *ProcessDriver) {
		if clk != nil {
			r.clk = clk
		}
	}
}

// WithIDGenerator sets the strategy used to mint a process-instance ID when
// ProcessDriver.Drive is called with an empty instanceID. Default: idgen.XID().
// A nil generator is ignored. Inject idgen.Func in tests for determinism.
func WithIDGenerator(gen idgen.Generator) Option {
	return func(r *ProcessDriver) {
		if gen != nil {
			r.idgen = gen
		}
	}
}

// WithLogger sets the structured logger used by the ProcessDriver (default: [slog.Default]).
// A nil value is ignored.
func WithLogger(l *slog.Logger) Option {
	return func(r *ProcessDriver) { r.logOpt = observability.WithLogger(l) }
}

// WithTracerProvider sets the OTel tracer provider used by the ProcessDriver
// (default: the OTel global provider). A nil value is ignored.
func WithTracerProvider(tp trace.TracerProvider) Option {
	return func(r *ProcessDriver) { r.tpOpt = observability.WithTracerProvider(tp) }
}

// WithMeterProvider sets the OTel meter provider used by the ProcessDriver
// (default: the OTel global provider). A nil value is ignored.
func WithMeterProvider(mp metric.MeterProvider) Option {
	return func(r *ProcessDriver) { r.mpOpt = observability.WithMeterProvider(mp) }
}

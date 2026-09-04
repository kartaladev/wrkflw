package runtime

import (
	"log/slog"
	"time"

	"github.com/jonboulle/clockwork"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/internal/expreval"
	"github.com/kartaladev/wrkflw/internal/observability"
	"github.com/kartaladev/wrkflw/runtime/idgen"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/runtime/signal"
	"github.com/kartaladev/wrkflw/scheduler"
)

// Option is a functional option for ProcessDriver. All dependencies — including
// the service-action catalog and instance store — are configurable via options.
// NewProcessDriver applies in-memory defaults before running the option list.
type Option func(*ProcessDriver)

// WithActionCatalog sets the service-action catalog. A nil cat is ignored, so
// the process-global action.DefaultCatalog() registry remains in effect.
func WithActionCatalog(cat action.Catalog) Option {
	return func(driver *ProcessDriver) {
		if cat != nil {
			driver.cat = cat
		}
	}
}

// WithInstanceStore sets the transactional instance store. A nil store is
// ignored, so the default in-memory MemInstanceStore remains in effect.
func WithInstanceStore(store kernel.InstanceStore) Option {
	return func(driver *ProcessDriver) {
		if store != nil {
			driver.store = store
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
func WithActionTimeout(d time.Duration) Option {
	return func(driver *ProcessDriver) { driver.actionTimeout = d }
}

// defaultCandidateResolveTimeout bounds a single [humantask.ActorResolver]
// lookup unless overridden via [WithCandidateResolveTimeout]. Candidate
// resolution runs before the commit that parks a human task, so an unresponsive
// directory service would otherwise hold the step — and the instance's commit —
// open indefinitely.
//
// It is shorter than defaultActionTimeout because a resolver expands a group
// membership rather than performing business work, and it sits on the critical
// path of every user-task entry.
const defaultCandidateResolveTimeout = 10 * time.Second

// WithCandidateResolveTimeout sets the maximum duration a single
// [humantask.ActorResolver] lookup may run before its context is cancelled. The
// default is 10s. A non-positive d disables the bound (no deadline is applied).
//
// The resolver's Candidates must honour ctx cancellation for the timeout to take
// effect; a timed-out resolution fails the step before anything is committed, so
// no instance is left parked on a task the task store never received.
func WithCandidateResolveTimeout(d time.Duration) Option {
	return func(driver *ProcessDriver) { driver.candidateResolveTimeout = d }
}

// WithHumanTasks wires the human-task capability into the ProcessDriver. Without this
// option, any process that reaches a user-task node will return a descriptive
// error rather than panic.
//
//   - resolver resolves an eligibility spec to the candidate actor list.
//   - tasks persists human-task records.
//   - az authorizes actors against task eligibility specs (used by TaskService,
//     not by the engine core).
func WithHumanTasks(resolver humantask.ActorResolver, tasks humantask.TaskStore, az authz.Authorizer) Option {
	return func(driver *ProcessDriver) {
		driver.resolver = resolver
		driver.tasks = tasks
		driver.authz = az
	}
}

// WithScheduler wires a [scheduler.Scheduler] into the ProcessDriver, enabling
// timer commands (ScheduleTimer / CancelTimer). Without this option the driver
// creates and owns an in-process gocron-backed default. A consumer-injected
// scheduler is consumer-owned: the driver never starts or closes it.
func WithScheduler(sched scheduler.Scheduler) Option {
	return func(driver *ProcessDriver) { driver.sched = sched }
}

// WithSignalBus wires a [SignalBus] into the ProcessDriver, enabling signal throw
// commands (ThrowSignal). Without this option any process that reaches a signal
// throw node will return a descriptive error.
//
// After each deliverLoop iteration the runner reconciles every signal name the
// instance can be woken by — signal-catch tokens, armed signal boundaries,
// event-based-gateway signal arms, and signal-triggered event sub-processes, as
// reported by engine.InstanceState.SignalWaiters — with the bus (via
// [SignalBus.Sync]) so that a later [SignalBus.Publish] reaches all parked
// instances.
func WithSignalBus(bus *signal.SignalBus) Option {
	return func(driver *ProcessDriver) { driver.sigbus = bus }
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
// The two differ in more than mutability. [kernel.MemDefinitionRegistry.Register]
// runs [model.Validate] and rejects a definition that fails it with
// [kernel.ErrInvalidDefinition], which is what upholds [engine.Step]'s assumption
// that the definitions reaching it are structurally valid.
// [kernel.NewMapDefinitionRegistry] returns no error and so cannot reject: a
// definition assembled into one is never validated, and its caller owns doing so.
// Prefer the mutable registry unless immutability is worth owning that check.
//
// A zero-config [NewProcessDriver] already uses [DefaultDefinitionRegistry];
// call activities only error when the requested DefRef is not found in that
// registry. Use [RegisterDefinition] to populate the global default at init time,
// or pass an isolated registry here for test isolation.
func WithDefinitions(reg kernel.DefinitionRegistry) Option {
	return func(driver *ProcessDriver) {
		if reg != nil {
			driver.defsReg = reg
		}
	}
}

// WithCallLinkStore wires a [CallLinkStore] into the ProcessDriver, enabling the
// non-blocking (async) path for [engine.StartSubInstance] commands (call
// activities). When this option is set, [perform] records the parent↔child link
// and starts the child's first burst without waiting for the child to complete —
// the parent parks at the call node until a notifier delivers the outcome. When
// this option is NOT set, the synchronous behavior (run child to completion
// in-process) applies.
func WithCallLinkStore(store kernel.CallLinkStore) Option {
	return func(driver *ProcessDriver) { driver.callLinks = store }
}

// WithTimerStore wires a [TimerStore] into the ProcessDriver. When set, the runtime
// records each armed/cancelled timer into the AppliedStep so the Store persists
// them atomically with state, and [ProcessDriver.RehydrateTimers] can re-arm them on
// restart. Absent this option, timers are in-memory only and lost on restart.
func WithTimerStore(store kernel.TimerStore) Option {
	return func(driver *ProcessDriver) { driver.timerStore = store }
}

// WithJitterSource overrides the retry-backoff jitter source (default: [NewJitterSource]).
// Inject a deterministic source in tests to produce predictable fire-at times.
func WithJitterSource(src kernel.JitterSource) Option {
	return func(driver *ProcessDriver) { driver.jitter = src }
}

// WithDefaultRetryPolicy sets the fallback retry policy applied to any action-bearing
// node that declares no RetryPolicy of its own. Without this option, retry is disabled
// by default and a failed action falls through to its error boundary or fails the instance.
//
// The policy value is copied on each call, so subsequent mutations by the caller do
// not affect the ProcessDriver.
func WithDefaultRetryPolicy(p model.RetryPolicy) Option {
	return func(driver *ProcessDriver) { driver.defaultRetryPolicy = &p }
}

// WithExpressionTimeout builds a long-lived, timeout-capable expression evaluator
// (compile cache reused across steps) and injects it into the engine for every
// step, bounding each in-engine expression evaluation — gateway conditions,
// timer/deadline durations, correlation keys — to d of wall-clock time. A runaway or
// hostile expression then aborts with [expreval.ErrEvalTimeout] instead of
// stalling the driver loop and every sibling instance behind it (the DoS the
// audit flagged).
//
// This is the explicit, per-driver opt-in for untrusted definitions.
// DETERMINISM TRADE-OFF: enabling the guard makes the engine's
// expression evaluation wall-clock-dependent, so a timed-out result is no longer
// reproducible on replay. Enable it only when you must evaluate UNTRUSTED
// definitions; trusted-definition deployments should leave it off (the default)
// to keep deterministic replay. A non-positive d disables the guard (equivalent
// to the default pure evaluator).
//
// WithExpressionTimeout and [WithConditionEvaluator] set the same field; the last
// option wins.
func WithExpressionTimeout(d time.Duration) Option {
	return func(driver *ProcessDriver) {
		driver.conditionEval = expreval.New(expreval.WithTimeout(d))
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
// that behaviour — an explicit consumer choice.
//
// WithConditionEvaluator and [WithExpressionTimeout] set the same field; the last
// option wins.
func WithConditionEvaluator(eval engine.ConditionEvaluator) Option {
	return func(driver *ProcessDriver) {
		if eval != nil {
			driver.conditionEval = eval
		}
	}
}

// WithClock sets the time source the ProcessDriver uses to stamp triggers,
// step-duration metrics, and armed-timer times. Default: clockwork.NewRealClock().
// A nil clock is ignored. Inject a fake clock in tests for determinism.
func WithClock(clk clockwork.Clock) Option {
	return func(driver *ProcessDriver) {
		if clk != nil {
			driver.clk = clk
		}
	}
}

// WithIDGenerator sets the strategy used to mint a process-instance ID when
// ProcessDriver.Drive is called with an empty instanceID. Default: idgen.XID().
// A nil generator is ignored. Inject idgen.Func in tests for determinism.
func WithIDGenerator(gen idgen.Generator) Option {
	return func(driver *ProcessDriver) {
		if gen != nil {
			driver.idgen = gen
		}
	}
}

// WithLogger sets the structured logger used by the ProcessDriver (default: [slog.Default]).
// A nil value is ignored.
func WithLogger(l *slog.Logger) Option {
	return func(driver *ProcessDriver) { driver.logOpt = observability.WithLogger(l) }
}

// WithTracerProvider sets the OTel tracer provider used by the ProcessDriver
// (default: the OTel global provider). A nil value is ignored.
func WithTracerProvider(tp trace.TracerProvider) Option {
	return func(driver *ProcessDriver) { driver.tpOpt = observability.WithTracerProvider(tp) }
}

// WithMeterProvider sets the OTel meter provider used by the ProcessDriver
// (default: the OTel global provider). A nil value is ignored.
func WithMeterProvider(mp metric.MeterProvider) Option {
	return func(driver *ProcessDriver) { driver.mpOpt = observability.WithMeterProvider(mp) }
}

// WithShutdownTimeout sets a FALLBACK drain deadline applied by [ProcessDriver.Shutdown]
// only when the ctx passed to Shutdown carries no deadline of its own. A ctx deadline
// always wins (the caller was explicit). Zero or unset means no fallback — Shutdown then
// respects ctx as-is, waiting unbounded if ctx has no deadline. A non-positive d is ignored
// (treated as unset), consistent with [WithActionTimeout].
func WithShutdownTimeout(d time.Duration) Option {
	return func(driver *ProcessDriver) {
		if d > 0 {
			driver.shutdownTimeout = d
		}
	}
}

// WithCompensationStallTimeout bounds how long a dispatched compensation action
// may go without reporting back before the engine raises a walk-scoped stall
// incident. Zero — the default — disables detection entirely, adding
// no timer and leaving every command stream byte-identical.
//
// A stalled compensation walk is both stuck and INVISIBLE: it advances only on a
// trigger carrying its cursor's command id, and holds no tokens and no other
// timers, so nothing else can wake it. Enabling this is what turns that into an
// incident an operator can see and act on with ProcessDriver.ResolveCompensationStall.
//
// ⚠ This does NOT cover the default in-process path, which WithActionTimeout
// already bounds — a hang there becomes an ActionFailed that advances the walk
// by itself. It covers the shapes that survive: a lost callback from an
// out-of-process worker, and a driver that died between dispatch and reply.
//
// ⚠ One engine-wide window is a deliberate v1 simplification. A ledger reversal
// returns in milliseconds and a manual-approval-gated refund takes hours, so a
// single value forces sizing for the slowest; a per-node tier is backlog.
func WithCompensationStallTimeout(d time.Duration) Option {
	return func(driver *ProcessDriver) { driver.compensationStallAfter = d }
}

// WithCompensationRetryPolicy makes a compensation action that reports back
// [engine.ActionFailed] be RE-DISPATCHED after a backoff instead of skipped.
// Without this option retry is disabled — the default — and the command stream
// keeps the skip-and-advance timing exactly; only the always-on WARN and the
// IncidentCompensationFailed record are new.
//
// The attempt budget is PER COMPENSATION RECORD and is zeroed whenever the walk
// advances to the next one, so a walk draining ten records gives each of them
// MaxAttempts rather than sharing one budget across the walk.
//
// A failure is retried only when the action reported it as retryable (see
// [action.IsRetryable]) and the policy's NonRetryableErrors does not match the
// error text — the same two tests the forward token path applies.
//
// ⚠ On exhaustion the walk SKIPS AND CONTINUES; it never parks. Parking would
// reverse the safety rule that a failed compensation must not strand the
// instance. The incident is the durable record that it happened.
//
// ⚠ MaxAttempts == 0 means UNLIMITED, matching the engine's token-retry
// convention. It is not a way to disable retry — leaving the option unset is.
//
// ⚠ MaxElapsed is NOT honoured on this path. A compensation walk holds no token
// of its own, so there is no per-attempt start timestamp to measure elapsed time
// against; bound the retries with MaxAttempts instead.
//
// ⚠ An unset MaxInterval is NOT "no cap": [model.RetryPolicy.Normalize] fills it
// from [model.DefaultRetryPolicy] (100s) and the backoff is capped against it, so
// a policy of {InitialInterval: 2 * time.Minute} arms at now+100s. Set MaxInterval
// explicitly when the compensation backoff must exceed that.
//
// ⚠ The backoff is NOT jittered, deliberately diverging from the forward token
// path. That path multiplies the interval by a runtime-sampled jitter fraction
// which defaults to zero — on this path that would make the first backoff
// instantaneous, i.e. not a backoff at all.
//
// ⚠ One engine-wide policy is a deliberate v1 simplification, the same
// trade-off [WithCompensationStallTimeout] documents. Compare
// [WithDefaultRetryPolicy], which is the base of a three-tier action > node >
// default chain precisely because one policy for every action is wrong. A
// per-node tier for compensation is deliberate backlog, not scope.
//
// The policy value is copied on each call, so subsequent mutations by the caller
// do not affect the ProcessDriver.
func WithCompensationRetryPolicy(p model.RetryPolicy) Option {
	return func(driver *ProcessDriver) { driver.compensationRetryPolicy = &p }
}

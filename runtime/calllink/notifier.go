package calllink

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/internal/observability"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// CallDeliverFunc delivers a trigger to a parent process instance. The
// definition is resolved by the CallNotifier via the DefinitionRegistry and
// passed to the delivery function so the caller can route to the correct
// Runner.Deliver call. The instanceID is the parent's instance ID.
//
// A typical wiring:
//
//	fn := CallDeliverFunc(func(ctx context.Context, def *definition.ProcessDefinition, id string, trg engine.Trigger) error {
//	    _, err := runner.Deliver(ctx, def, id, trg)
//	    return err
//	})
type CallDeliverFunc func(ctx context.Context, def *definition.ProcessDefinition, instanceID string, trg engine.Trigger) error

// CallNotifier drains terminal call links and resumes the parked parent token
// with SubInstanceCompleted or SubInstanceFailed (ADR-0024). Delivery is
// idempotent: a parent whose token was already resumed (engine.ErrTokenNotFound)
// is treated as successfully notified and the link is marked notified.
type CallNotifier struct {
	cl      kernel.CallLinkStore
	deliver CallDeliverFunc
	reg     kernel.DefinitionRegistry
	clk     clock.Clock
	batch   int
	poll    time.Duration

	// staged telemetry option values; assembled into tel after all
	// CallNotifierOptions have been applied in NewCallNotifier.
	logOpt observability.Option
	tpOpt  observability.Option
	mpOpt  observability.Option

	tel                  observability.Telemetry
	linksNotifiedCounter metric.Int64Counter
}

// CallNotifierOption configures a [CallNotifier].
type CallNotifierOption func(*CallNotifier)

// WithCallNotifierBatchSize sets the maximum number of terminal links claimed
// per DrainOnce call. Default: 100.
func WithCallNotifierBatchSize(n int) CallNotifierOption {
	return func(c *CallNotifier) {
		if n > 0 {
			c.batch = n
		}
	}
}

// WithCallNotifierPollInterval sets the interval between DrainOnce calls in
// Run. Default: 1s.
func WithCallNotifierPollInterval(d time.Duration) CallNotifierOption {
	return func(c *CallNotifier) {
		if d > 0 {
			c.poll = d
		}
	}
}

// WithClock sets the time source for trigger timestamps (ADR-0003).
// Default: clock.System(). A nil clock is ignored. Inject a fake clock in tests.
func WithClock(clk clock.Clock) CallNotifierOption {
	return func(n *CallNotifier) {
		if clk != nil {
			n.clk = clk
		}
	}
}

// WithCallNotifierLogger sets the structured logger used by the call notifier.
// Default: slog.Default(). A nil value is ignored.
func WithCallNotifierLogger(l *slog.Logger) CallNotifierOption {
	return func(n *CallNotifier) { n.logOpt = observability.WithLogger(l) }
}

// WithCallNotifierTracerProvider sets the OTel TracerProvider for call notifier
// batch spans. Default: the OTel global provider.
func WithCallNotifierTracerProvider(tp trace.TracerProvider) CallNotifierOption {
	return func(n *CallNotifier) { n.tpOpt = observability.WithTracerProvider(tp) }
}

// WithCallNotifierMeterProvider sets the OTel MeterProvider for call notifier
// metrics. Default: the OTel global provider.
func WithCallNotifierMeterProvider(mp metric.MeterProvider) CallNotifierOption {
	return func(n *CallNotifier) { n.mpOpt = observability.WithMeterProvider(mp) }
}

// NewCallNotifier constructs a CallNotifier that claims terminal call links
// from cl, resolves each parent definition via reg, and delivers the
// SubInstanceCompleted / SubInstanceFailed trigger via deliver.
//
//   - cl: the CallLinkStore to claim pending notifications from.
//   - deliver: wraps Runner.Deliver (the parent def is pre-resolved by DrainOnce via reg).
//   - reg: resolves parent definition references (format "defID:version").
//   - opts: optional configuration overrides (use [WithClock] to set the
//     time source; default is clock.System() per ADR-0003).
//
// REQUIRED registration contract: every parent definition MUST be resolvable from
// reg under the exact key "<defID>:<version>" (the format DrainOnce uses to look it
// up). If a parent def cannot be resolved, DrainOnce SKIPS that link (it stays
// claimable for a later drain) — so a registry missing the "id:version" key leaves
// the parked parent unresumed until the registration is fixed.
func NewCallNotifier(cl kernel.CallLinkStore, deliver CallDeliverFunc, reg kernel.DefinitionRegistry, opts ...CallNotifierOption) (*CallNotifier, error) {
	if cl == nil {
		return nil, fmt.Errorf("%w: call link store", kernel.ErrNilDependency)
	}
	if deliver == nil {
		return nil, fmt.Errorf("%w: deliver", kernel.ErrNilDependency)
	}
	if reg == nil {
		return nil, fmt.Errorf("%w: definition registry", kernel.ErrNilDependency)
	}
	n := &CallNotifier{
		cl:      cl,
		deliver: deliver,
		reg:     reg,
		clk:     clock.System(),
		batch:   100,
		poll:    time.Second,
	}
	for _, o := range opts {
		o(n)
	}
	// Build the Telemetry value after all options have been applied so that any
	// subset of logger/tracer/meter providers can be set independently.
	n.tel = observability.New(
		kernel.InstrumentationScope,
		filterCallNotifierNilOpts(n.logOpt, n.tpOpt, n.mpOpt)...,
	)
	n.linksNotifiedCounter = n.tel.Int64Counter(
		"wrkflw_callnotifier_links_notified_total",
		"Total number of call-link notifications delivered by the CallNotifier.",
	)
	return n, nil
}

// filterCallNotifierNilOpts returns only the non-nil observability.Option values.
func filterCallNotifierNilOpts(opts ...observability.Option) []observability.Option {
	out := opts[:0]
	for _, o := range opts {
		if o != nil {
			out = append(out, o)
		}
	}
	return out
}

// DrainOnce claims up to one batch of terminal call links and resumes each
// parent instance. Returns the count of links successfully notified.
//
// Idempotency contract:
//   - If deliver returns a non-nil error that is NOT engine.ErrTokenNotFound,
//     the link is skipped (left claimable) so a later drain retries it.
//   - If deliver succeeds OR returns engine.ErrTokenNotFound (parent already
//     resumed), the link is marked notified and counted.
//   - If reg.Lookup fails, the link is skipped (not marked notified) so a
//     later drain retries it after the definition is available.
func (n *CallNotifier) DrainOnce(ctx context.Context) (int, error) {
	ctx, span := n.tel.Tracer.Start(ctx, "wrkflw.callnotifier.batch")
	defer span.End()

	pending, err := n.cl.ClaimPending(ctx, n.batch)
	if err != nil {
		return 0, fmt.Errorf("workflow-runtime: call notifier: claim: %w", err)
	}

	notified := 0
	for _, p := range pending {
		// Resolve the parent definition. Failure is a skip: a transient lookup
		// failure must not permanently block delivery; a later drain retries.
		defRef := fmt.Sprintf("%s:%d", p.Link.ParentDefID, p.Link.ParentDefVersion)
		parentDef, lookupErr := n.reg.Lookup(ctx, defRef)
		if lookupErr != nil {
			continue
		}

		// Build the appropriate trigger based on the child's terminal outcome.
		var trg engine.Trigger
		if p.Outcome.Completed {
			trg = engine.NewSubInstanceCompleted(n.clk.Now(), p.Link.ParentCommandID, p.Outcome.Output)
		} else {
			trg = engine.NewSubInstanceFailed(n.clk.Now(), p.Link.ParentCommandID, p.Outcome.Err)
		}

		// Deliver the trigger to the parent instance.
		derr := n.deliver(ctx, parentDef, p.Link.ParentInstanceID, trg)
		if derr != nil && !errors.Is(derr, engine.ErrTokenNotFound) {
			// Transient or structural failure — leave the link claimable for retry.
			continue
		}
		// Success OR duplicate (ErrTokenNotFound = parent already resumed): mark notified.
		if merr := n.cl.MarkNotified(ctx, p.Link.ChildInstanceID); merr != nil {
			return notified, fmt.Errorf("workflow-runtime: call notifier: mark notified: %w", merr)
		}
		notified++
	}
	if notified > 0 {
		n.linksNotifiedCounter.Add(ctx, int64(notified))
	}
	return notified, nil
}

// Run drains the call link store on each poll interval tick until ctx is
// cancelled. It returns ctx.Err() when the context is done.
//
// Run mirrors the structure of the postgres Relay.Run: an immediate drain is
// attempted before the first tick, and DrainOnce errors are logged and do not
// terminate the loop (unlike infrastructure errors).
func (n *CallNotifier) Run(ctx context.Context) error {
	ticker := time.NewTicker(n.poll)
	defer ticker.Stop()

	// Immediate drain before waiting for the first tick.
	if _, err := n.DrainOnce(ctx); err != nil {
		if ctx.Err() != nil { // Canceled or DeadlineExceeded: honor the Run contract.
			return ctx.Err()
		}
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := n.DrainOnce(ctx); err != nil {
				if ctx.Err() != nil { // Canceled or DeadlineExceeded: honor the Run contract.
					return ctx.Err()
				}
				return err
			}
		}
	}
}

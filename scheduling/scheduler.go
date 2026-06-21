// Package scheduling is the consumer-facing façade over the internal gocron
// scheduler (ADR-0008, ADR-0009). Consumers import only this root package;
// the concrete gocron implementation stays in internal/scheduling/gocron so
// the vendor dependency is not visible to the library API surface.
package scheduling

import (
	"io"
	"log/slog"
	"time"

	"github.com/jonboulle/clockwork"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	gocronsched "github.com/zakyalvan/krtlwrkflw/internal/scheduling/gocron"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// Scheduler is the production, gocron-backed [runtime.Scheduler]. Construct it
// with [NewScheduler], passing the same [clockwork.Clock] instance used to
// build the runtime so one fake-clock advance drives both engine timestamps and
// timer firing under test (ADR-0003). Call [Close] on shutdown to release the
// underlying gocron goroutine.
type Scheduler struct {
	impl *gocronsched.GocronScheduler
}

// Compile-time contract assertions.
var (
	_ runtime.Scheduler = (*Scheduler)(nil)
	_ io.Closer         = (*Scheduler)(nil)
)

// config holds façade-level options.
type config struct {
	logger *slog.Logger
	tp     trace.TracerProvider
	mp     metric.MeterProvider
}

// Option configures a [Scheduler].
type Option func(*config)

// WithLogger sets the scheduler's structured logger (default: [slog.Default]).
// A nil value is ignored.
func WithLogger(l *slog.Logger) Option {
	return func(c *config) {
		if l != nil {
			c.logger = l
		}
	}
}

// WithTracerProvider sets the OTel TracerProvider for the scheduler.
// Default: the OTel global provider. The scheduler emits no spans in this
// track (API parity only — consistent with relay/rest/grpc). A nil value is
// ignored.
func WithTracerProvider(tp trace.TracerProvider) Option {
	return func(c *config) {
		if tp != nil {
			c.tp = tp
		}
	}
}

// WithMeterProvider sets the OTel MeterProvider for the scheduler.
// Default: the OTel global provider. The scheduler emits no metrics in this
// track (API parity only — consistent with relay/rest/grpc). A nil value is
// ignored.
func WithMeterProvider(mp metric.MeterProvider) Option {
	return func(c *config) {
		if mp != nil {
			c.mp = mp
		}
	}
}

// NewScheduler constructs and starts a gocron-backed [Scheduler] driven by
// clk. The returned scheduler must be closed via [Scheduler.Close] when the
// application shuts down.
func NewScheduler(clk clockwork.Clock, opts ...Option) (*Scheduler, error) {
	cfg := &config{}
	for _, o := range opts {
		o(cfg)
	}

	var internalOpts []gocronsched.Option
	if cfg.logger != nil {
		internalOpts = append(internalOpts, gocronsched.WithLogger(cfg.logger))
	}
	if cfg.tp != nil {
		internalOpts = append(internalOpts, gocronsched.WithTracerProvider(cfg.tp))
	}
	if cfg.mp != nil {
		internalOpts = append(internalOpts, gocronsched.WithMeterProvider(cfg.mp))
	}

	impl, err := gocronsched.NewGocronScheduler(clk, internalOpts...)
	if err != nil {
		return nil, err
	}
	return &Scheduler{impl: impl}, nil
}

// Schedule registers a one-time timer identified by timerID that calls fire at
// or after fireAt. If a timer with the same timerID already exists it is
// replaced.
func (s *Scheduler) Schedule(timerID string, fireAt time.Time, fire func()) {
	s.impl.Schedule(timerID, fireAt, fire)
}

// Cancel removes a pending timer. No-op if the timer is unknown or has already
// fired.
func (s *Scheduler) Cancel(timerID string) {
	s.impl.Cancel(timerID)
}

// Close shuts the underlying gocron scheduler down gracefully. The scheduler
// cannot be reused after this call.
func (s *Scheduler) Close() error {
	return s.impl.Close()
}

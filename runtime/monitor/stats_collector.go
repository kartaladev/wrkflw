package monitor

import (
	"context"
	"log/slog"
	"time"

	"github.com/jonboulle/clockwork"
	"go.opentelemetry.io/otel/metric"

	"github.com/kartaladev/wrkflw/internal/observability"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

const statsInstrumentationName = "github.com/kartaladev/wrkflw/runtime/stats"

// OutboxStatsCollector is an OTel observable-gauge collector for the wrkflw_outbox
// table. It registers three int64 gauges and reads from the underlying
// OutboxStatsReader at each OTel collection cycle (no background goroutine).
//
// Construct it with [NewOutboxStatsCollector]; the zero value is not useful.
type OutboxStatsCollector struct {
	tel    observability.Telemetry
	reader kernel.OutboxStatsReader
}

// NewOutboxStatsCollector creates an OutboxStatsCollector that registers three
// observable gauges:
//   - wrkflw_outbox_pending
//   - wrkflw_outbox_dead
//   - wrkflw_outbox_oldest_pending_age_seconds
//
// Configure it with this package's own options — [WithMeterProvider],
// [WithLogger], [WithTracerProvider] — or omit them to use the OTel globals. A
// nil opt is silently ignored.
//
// The collector adds no background goroutines — all data is read inside the OTel
// SDK's collection callback.
func NewOutboxStatsCollector(r kernel.OutboxStatsReader, opts ...Option) *OutboxStatsCollector {
	var cfg collectorConfig
	tel := cfg.telemetry(statsInstrumentationName, opts)

	c := &OutboxStatsCollector{tel: tel, reader: r}

	// Register the three gauges and share a single callback so the reader is
	// called once per collection cycle.
	g1, err := tel.Meter.Int64ObservableGauge(
		"wrkflw_outbox_pending",
		metric.WithDescription("Number of outbox rows with status='pending' (not yet published)."),
	)
	if err != nil {
		tel.Logger.Error("failed to register wrkflw_outbox_pending gauge", slog.String("err", err.Error()))
	}

	g2, err := tel.Meter.Int64ObservableGauge(
		"wrkflw_outbox_dead",
		metric.WithDescription("Number of outbox rows quarantined with status='dead'."),
	)
	if err != nil {
		tel.Logger.Error("failed to register wrkflw_outbox_dead gauge", slog.String("err", err.Error()))
	}

	g3, err := tel.Meter.Int64ObservableGauge(
		"wrkflw_outbox_oldest_pending_age_seconds",
		metric.WithDescription("Age in seconds of the oldest pending outbox row. Zero when there are no pending rows."),
	)
	if err != nil {
		tel.Logger.Error("failed to register wrkflw_outbox_oldest_pending_age_seconds gauge",
			slog.String("err", err.Error()))
	}

	// RegisterCallback wires a shared callback that reads the reader once per scrape.
	if g1 != nil && g2 != nil && g3 != nil {
		_, regErr := tel.Meter.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
			stats, err := c.reader.OutboxStats(ctx)
			if err != nil {
				c.tel.Logger.Error("OutboxStatsCollector: failed to read outbox stats",
					slog.String("err", err.Error()))
				return nil //nolint:nilerr // log and swallow; never panic
			}
			o.ObserveInt64(g1, stats.Pending)
			o.ObserveInt64(g2, stats.Dead)
			o.ObserveInt64(g3, int64(stats.OldestPendingAge.Seconds()))
			return nil
		}, g1, g2, g3)
		if regErr != nil {
			tel.Logger.Error("OutboxStatsCollector: failed to register callback",
				slog.String("err", regErr.Error()))
		}
	}

	return c
}

// TimerStatsCollector is an OTel observable-gauge collector for the wrkflw_timers
// table. It registers two int64 gauges and reads from the underlying
// TimerStatsReader at each OTel collection cycle (no background goroutine).
//
// Construct it with [NewTimerStatsCollector]; the zero value is not useful.
type TimerStatsCollector struct {
	tel    observability.Telemetry
	reader kernel.TimerStatsReader
	clk    clockwork.Clock
}

// overdueSeconds is how far past due the earliest armed timer's next_run is at
// now, in whole seconds, clamped at zero.
//
// Three inputs must all clamp rather than subtract. nextFireAt is nil when no
// timer is armed at all; a stored row can carry the zero time (ADR-0181), which
// would otherwise report a ~2000-year age; and a HEALTHY timer is in the future,
// which would otherwise report a negative age. Zero therefore means "nothing is
// late", and is the value a well-behaved scheduler emits continuously.
func overdueSeconds(now time.Time, nextFireAt *time.Time) int64 {
	if nextFireAt == nil || nextFireAt.IsZero() {
		return 0
	}
	overdue := now.Sub(*nextFireAt)
	if overdue <= 0 {
		return 0
	}
	return int64(overdue.Seconds())
}

// NewTimerStatsCollector creates a TimerStatsCollector that registers two
// observable gauges:
//   - wrkflw_timers_armed
//   - wrkflw_timers_next_fire_age_seconds
//
// The second one is the health signal: armed alone cannot distinguish a
// scheduler that is keeping up from one that is 45 minutes behind, because both
// report the same count. It costs no extra query — [kernel.TimerStats] already
// carries NextFireAt, computed in SQL on every read, and it was being discarded.
// Zero means nothing is overdue; see [overdueSeconds] for the clamped cases.
//
// Configure it with this package's own options — [WithMeterProvider],
// [WithLogger], [WithTracerProvider], [WithClock] — or omit them to use the OTel
// globals and the real clock. A nil opt is silently ignored.
//
// The collector adds no background goroutines — all data is read inside the OTel
// SDK's collection callback.
func NewTimerStatsCollector(r kernel.TimerStatsReader, opts ...Option) *TimerStatsCollector {
	var cfg collectorConfig
	tel := cfg.telemetry(statsInstrumentationName, opts)

	c := &TimerStatsCollector{tel: tel, reader: r, clk: cfg.clk}

	g, err := tel.Meter.Int64ObservableGauge(
		"wrkflw_timers_armed",
		metric.WithDescription("Total number of armed timer rows in wrkflw_timers."),
	)
	if err != nil {
		tel.Logger.Error("failed to register wrkflw_timers_armed gauge", slog.String("err", err.Error()))
		return c
	}

	gAge, err := tel.Meter.Int64ObservableGauge(
		"wrkflw_timers_next_fire_age_seconds",
		metric.WithDescription("Age in seconds of the earliest armed timer's next run, i.e. how far past due it is. "+
			"Zero when no armed timer is overdue."),
	)
	if err != nil {
		tel.Logger.Error("failed to register wrkflw_timers_next_fire_age_seconds gauge",
			slog.String("err", err.Error()))
		return c
	}

	_, regErr := tel.Meter.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		stats, err := c.reader.Stats(ctx)
		if err != nil {
			c.tel.Logger.Error("TimerStatsCollector: failed to read timer stats",
				slog.String("err", err.Error()))
			return nil //nolint:nilerr // log and swallow; never panic
		}
		o.ObserveInt64(g, stats.Armed)
		o.ObserveInt64(gAge, overdueSeconds(c.clk.Now(), stats.NextFireAt))
		return nil
	}, g, gAge)
	if regErr != nil {
		tel.Logger.Error("TimerStatsCollector: failed to register callback",
			slog.String("err", regErr.Error()))
	}

	return c
}

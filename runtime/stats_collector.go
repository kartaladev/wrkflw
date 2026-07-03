package runtime

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/metric"

	"github.com/zakyalvan/krtlwrkflw/internal/observability"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

const statsInstrumentationName = "github.com/zakyalvan/krtlwrkflw/runtime/stats"

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
// The supplied opts are passed directly to [observability.New] (e.g.
// [observability.WithMeterProvider]). A nil opt is silently ignored.
//
// The collector adds no background goroutines — all data is read inside the OTel
// SDK's collection callback.
func NewOutboxStatsCollector(r kernel.OutboxStatsReader, opts ...observability.Option) *OutboxStatsCollector {
	var real []observability.Option
	for _, o := range opts {
		if o != nil {
			real = append(real, o)
		}
	}
	tel := observability.New(statsInstrumentationName, real...)

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
// table. It registers one int64 gauge and reads from the underlying
// TimerStatsReader at each OTel collection cycle (no background goroutine).
//
// Construct it with [NewTimerStatsCollector]; the zero value is not useful.
type TimerStatsCollector struct {
	tel    observability.Telemetry
	reader kernel.TimerStatsReader
}

// NewTimerStatsCollector creates a TimerStatsCollector that registers one
// observable gauge:
//   - wrkflw_timers_armed
//
// The supplied opts are passed directly to [observability.New] (e.g.
// [observability.WithMeterProvider]). A nil opt is silently ignored.
//
// The collector adds no background goroutines — all data is read inside the OTel
// SDK's collection callback.
func NewTimerStatsCollector(r kernel.TimerStatsReader, opts ...observability.Option) *TimerStatsCollector {
	var real []observability.Option
	for _, o := range opts {
		if o != nil {
			real = append(real, o)
		}
	}
	tel := observability.New(statsInstrumentationName, real...)

	c := &TimerStatsCollector{tel: tel, reader: r}

	g, err := tel.Meter.Int64ObservableGauge(
		"wrkflw_timers_armed",
		metric.WithDescription("Total number of armed timer rows in wrkflw_timers."),
	)
	if err != nil {
		tel.Logger.Error("failed to register wrkflw_timers_armed gauge", slog.String("err", err.Error()))
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
		return nil
	}, g)
	if regErr != nil {
		tel.Logger.Error("TimerStatsCollector: failed to register callback",
			slog.String("err", regErr.Error()))
	}

	return c
}

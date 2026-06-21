package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/internal/observability"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// Relay drains wrkflw_outbox and hands each event to a runtime.Publisher
// (at-least-once delivery). It claims due pending rows with FOR UPDATE SKIP
// LOCKED so multiple concurrent Relay instances cooperate without
// double-publishing.
//
// Per-row isolation (ADR-0017): each claimed row's outcome is recorded
// independently within the single drain transaction. A successful Publish marks
// only that row published; a failed Publish increments that row's retry_count
// and pushes next_attempt_at out by a capped exponential backoff — it does NOT
// roll back the batch. A persistently-failing ("poison") row therefore never
// blocks healthy peers claimed alongside it (no head-of-line blocking); healthy
// events are delivered while the poison row is retried on its own schedule.
//
// Dead-letter quarantine: once a row's retry_count reaches MaxDeliveryAttempts
// it is moved to status 'dead' and is no longer claimed, isolating it for
// operator inspection rather than retrying forever.
//
// Ordering: global FIFO is not guaranteed when a row fails — its delivery is
// deferred relative to later-arriving healthy rows. Per ADR-0017 ordering loss
// is bounded to the affected row's own lane (its instance/dedup partition);
// healthy rows in other lanes proceed unaffected.
type Relay struct {
	pool         *pgxpool.Pool
	pub          runtime.Publisher
	clk          clock.Clock
	pollInterval time.Duration
	batch        int
	maxDelivery  int
	backoffBase  time.Duration
	backoffMax   time.Duration

	// staged telemetry option values; assembled into tel after all RelayOptions
	// have been applied in NewRelay.
	logOpt observability.Option
	tpOpt  observability.Option
	mpOpt  observability.Option

	tel observability.Telemetry
}

// RelayOption configures a Relay.
type RelayOption func(*Relay)

// WithPollInterval sets the interval between DrainOnce calls in Run.
// Default: 1s.
func WithPollInterval(d time.Duration) RelayOption { return func(r *Relay) { r.pollInterval = d } }

// WithBatchSize sets the maximum number of outbox rows claimed per DrainOnce call.
// Default: 100.
func WithBatchSize(n int) RelayOption { return func(r *Relay) { r.batch = n } }

// WithClock sets the clock used to stamp published_at / next_attempt_at and to
// evaluate the claim predicate. Default: clock.System(). Tests inject a fake
// clock so backoff windows are deterministic.
func WithClock(clk clock.Clock) RelayOption { return func(r *Relay) { r.clk = clk } }

// WithMaxDeliveryAttempts sets how many failed publish attempts a row tolerates
// before it is quarantined to status 'dead'. Default: 10. A value <= 0 is
// ignored (the default is kept).
func WithMaxDeliveryAttempts(n int) RelayOption {
	return func(r *Relay) {
		if n > 0 {
			r.maxDelivery = n
		}
	}
}

// WithRelayBackoff sets the base and maximum interval of the capped exponential
// backoff applied to a row's next_attempt_at after a failed publish.
// Defaults: base 1s, max 1m. Non-positive values are ignored.
func WithRelayBackoff(base, maxInterval time.Duration) RelayOption {
	return func(r *Relay) {
		if base > 0 {
			r.backoffBase = base
		}
		if maxInterval > 0 {
			r.backoffMax = maxInterval
		}
	}
}

// WithRelayLogger sets the structured logger used by the relay for drain logs.
// Default: slog.Default().
func WithRelayLogger(l *slog.Logger) RelayOption {
	return func(r *Relay) { r.logOpt = observability.WithLogger(l) }
}

// WithRelayTracerProvider sets the OTel TracerProvider for relay batch spans.
// Default: the OTel global provider.
func WithRelayTracerProvider(tp trace.TracerProvider) RelayOption {
	return func(r *Relay) { r.tpOpt = observability.WithTracerProvider(tp) }
}

// WithRelayMeterProvider sets the OTel MeterProvider for relay metrics.
// Default: the OTel global provider. The relay creates no metric instruments
// in this track (API parity only; DLQ counters live in the resilience adapter).
func WithRelayMeterProvider(mp metric.MeterProvider) RelayOption {
	return func(r *Relay) { r.mpOpt = observability.WithMeterProvider(mp) }
}

// NewRelay constructs a Relay that drains the outbox in pool and publishes each
// event via pub.
func NewRelay(pool *pgxpool.Pool, pub runtime.Publisher, opts ...RelayOption) *Relay {
	r := &Relay{
		pool:         pool,
		pub:          pub,
		clk:          clock.System(),
		pollInterval: time.Second,
		batch:        100,
		maxDelivery:  10,
		backoffBase:  time.Second,
		backoffMax:   time.Minute,
	}
	for _, o := range opts {
		o(r)
	}
	// Build the Telemetry value after all options have been applied so that any
	// subset of logger/tracer/meter providers can be set independently.
	r.tel = observability.New(
		"github.com/zakyalvan/krtlwrkflw/persistence",
		filterNilOpts(r.logOpt, r.tpOpt, r.mpOpt)...,
	)
	return r
}

// filterNilOpts returns only the non-nil observability.Option values from opts.
func filterNilOpts(opts ...observability.Option) []observability.Option {
	out := opts[:0]
	for _, o := range opts {
		if o != nil {
			out = append(out, o)
		}
	}
	return out
}

// Run drains the outbox on each poll interval tick until ctx is cancelled.
// It returns ctx.Err() when the context is done.
//
// Publish failures no longer terminate Run: with per-row isolation they are
// recorded against the failing row (retry / quarantine) and the loop keeps
// polling. Only infrastructure errors (claim/commit failures) are propagated
// and terminate the loop fail-fast.
func (r *Relay) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()
	// Attempt an immediate drain before waiting for the first tick.
	if _, err := r.DrainOnce(ctx); err != nil {
		if errors.Is(err, context.Canceled) {
			return ctx.Err()
		}
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := r.DrainOnce(ctx); err != nil {
				if errors.Is(err, context.Canceled) {
					return ctx.Err()
				}
				return err
			}
		}
	}
}

// ListDeadLettered returns up to limit dead-lettered outbox rows ordered by id
// ascending (oldest first). Dead rows have status='dead' and were quarantined
// after exhausting MaxDeliveryAttempts consecutive publish failures.
//
// Use Redrive to re-queue selected rows for re-delivery.
func (r *Relay) ListDeadLettered(ctx context.Context, limit int) ([]runtime.DeadLetter, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, instance_id, topic, retry_count, COALESCE(last_error, ''), created_at
		   FROM wrkflw_outbox
		  WHERE status = 'dead'
		  ORDER BY id
		  LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("postgres: relay: list dead-lettered: %w", err)
	}
	defer rows.Close()

	var out []runtime.DeadLetter
	for rows.Next() {
		var dl runtime.DeadLetter
		if err := rows.Scan(&dl.ID, &dl.InstanceID, &dl.Topic, &dl.RetryCount, &dl.LastError, &dl.CreatedAt); err != nil {
			return nil, fmt.Errorf("postgres: relay: list dead-lettered: scan: %w", err)
		}
		out = append(out, dl)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: relay: list dead-lettered: rows: %w", err)
	}
	return out, nil
}

// Redrive resets the given dead outbox rows back to status='pending' with
// retry_count=0, last_error=NULL, and next_attempt_at=now (via r.clk). Only
// rows that are currently status='dead' are affected; rows with any other status
// are silently skipped. Returns the number of rows successfully re-queued.
//
// Passing no ids is a no-op (returns 0, nil).
func (r *Relay) Redrive(ctx context.Context, ids ...int64) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	now := r.clk.Now()
	tag, err := r.pool.Exec(ctx,
		`UPDATE wrkflw_outbox
		    SET status = 'pending',
		        retry_count = 0,
		        next_attempt_at = $1,
		        last_error = NULL
		  WHERE status = 'dead'
		    AND id = ANY($2)`,
		now, ids,
	)
	if err != nil {
		return 0, fmt.Errorf("postgres: relay: redrive: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// DrainOnce claims one batch of due pending outbox rows (status='pending' AND
// next_attempt_at <= now, ORDER BY id FOR UPDATE SKIP LOCKED), publishes each via
// the Publisher, and records each row's outcome independently in the same
// transaction:
//
//   - on success: status='published', published_at=now.
//   - on failure: retry_count++, next_attempt_at=now+backoff, last_error=err; the
//     row is quarantined to status='dead' once retry_count reaches
//     MaxDeliveryAttempts, otherwise it stays 'pending' for a future drain.
//
// A publish failure does NOT abort the drain — healthy rows in the same batch are
// still marked published. The whole batch commits atomically. At-least-once is
// preserved: a row becomes 'published' only after a successful Publish.
//
// Returns the number of rows successfully published in this drain.
func (r *Relay) DrainOnce(ctx context.Context) (int, error) {
	ctx, span := r.tel.Tracer.Start(ctx, "wrkflw.relay.batch")
	defer span.End()

	now := r.clk.Now()

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		infraErr := fmt.Errorf("postgres: relay: begin tx: %w", err)
		span.RecordError(infraErr)
		span.SetStatus(otelcodes.Error, infraErr.Error())
		r.tel.Logger.LogAttrs(ctx, slog.LevelError, "persistence: relay begin tx failed",
			append(r.tel.LogAttrs(ctx), slog.Any("error", infraErr))...)
		return 0, infraErr
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx,
		`SELECT id, topic, payload, instance_id, dedup_key, retry_count
		   FROM wrkflw_outbox
		  WHERE status = 'pending' AND next_attempt_at <= $1
		  ORDER BY id
		    FOR UPDATE SKIP LOCKED
		  LIMIT $2`,
		now, r.batch,
	)
	if err != nil {
		infraErr := fmt.Errorf("postgres: relay: claim: %w", err)
		span.RecordError(infraErr)
		span.SetStatus(otelcodes.Error, infraErr.Error())
		r.tel.Logger.LogAttrs(ctx, slog.LevelError, "persistence: relay claim failed",
			append(r.tel.LogAttrs(ctx), slog.Any("error", infraErr))...)
		return 0, infraErr
	}

	type claim struct {
		id         int64
		retryCount int
		event      runtime.OutboxEvent
	}

	var claims []claim
	for rows.Next() {
		var id int64
		var topic string
		var rawPayload []byte
		var instanceID, dedupKey string
		var retryCount int
		// scan order matches the SELECT projection.
		if err := rows.Scan(&id, &topic, &rawPayload, &instanceID, &dedupKey, &retryCount); err != nil {
			rows.Close()
			return 0, fmt.Errorf("postgres: relay: scan: %w", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(rawPayload, &payload); err != nil {
			rows.Close()
			return 0, fmt.Errorf("postgres: relay: unmarshal payload id=%d: %w", id, err)
		}
		claims = append(claims, claim{id: id, retryCount: retryCount, event: runtime.OutboxEvent{
			Topic:      topic,
			Payload:    payload,
			DedupKey:   dedupKey,
			InstanceID: instanceID,
		}})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		infraErr := fmt.Errorf("postgres: relay: rows: %w", err)
		span.RecordError(infraErr)
		span.SetStatus(otelcodes.Error, infraErr.Error())
		r.tel.Logger.LogAttrs(ctx, slog.LevelError, "persistence: relay rows iteration failed",
			append(r.tel.LogAttrs(ctx), slog.Any("error", infraErr))...)
		return 0, infraErr
	}

	if len(claims) == 0 {
		span.SetAttributes(attribute.Int("wrkflw.batch_size", 0))
		return 0, nil
	}

	published := 0
	for _, c := range claims {
		// Publish the event. Both branches record their outcome in the open tx;
		// a failure must NOT return early — that would roll back healthy peers
		// already marked published in this batch (head-of-line blocking).
		if pubErr := r.pub.Publish(ctx, c.event); pubErr != nil {
			newRetry := c.retryCount + 1
			status := "pending"
			if newRetry >= r.maxDelivery {
				status = "dead"
			}
			nextAttempt := now.Add(RelayBackoff(c.retryCount, r.backoffBase, r.backoffMax))
			if _, err := tx.Exec(ctx,
				`UPDATE wrkflw_outbox
				    SET retry_count = $2, status = $3, next_attempt_at = $4, last_error = $5
				  WHERE id = $1`,
				c.id, newRetry, status, nextAttempt, pubErr.Error(),
			); err != nil {
				infraErr := fmt.Errorf("postgres: relay: quarantine id=%d: %w", c.id, err)
				span.RecordError(infraErr)
				span.SetStatus(otelcodes.Error, infraErr.Error())
				r.tel.Logger.LogAttrs(ctx, slog.LevelError, "persistence: relay quarantine failed",
					append(r.tel.LogAttrs(ctx), slog.Any("error", infraErr))...)
				return 0, infraErr
			}
			continue
		}
		// Mark this row published, inside the open transaction. If the tx later
		// fails to commit the row remains pending (at-least-once, not at-most-once).
		if _, err := tx.Exec(ctx,
			`UPDATE wrkflw_outbox SET status = 'published', published_at = $2 WHERE id = $1`,
			c.id, now,
		); err != nil {
			infraErr := fmt.Errorf("postgres: relay: mark published id=%d: %w", c.id, err)
			span.RecordError(infraErr)
			span.SetStatus(otelcodes.Error, infraErr.Error())
			r.tel.Logger.LogAttrs(ctx, slog.LevelError, "persistence: relay mark-published failed",
				append(r.tel.LogAttrs(ctx), slog.Any("error", infraErr))...)
			return 0, infraErr
		}
		published++
	}

	if err := tx.Commit(ctx); err != nil {
		infraErr := fmt.Errorf("postgres: relay: commit: %w", err)
		span.RecordError(infraErr)
		span.SetStatus(otelcodes.Error, infraErr.Error())
		r.tel.Logger.LogAttrs(ctx, slog.LevelError, "persistence: relay commit failed",
			append(r.tel.LogAttrs(ctx), slog.Any("error", infraErr))...)
		return 0, infraErr
	}

	span.SetAttributes(attribute.Int("wrkflw.batch_size", published))
	r.tel.Logger.LogAttrs(ctx, slog.LevelDebug, "persistence: relay drained batch",
		append(r.tel.LogAttrs(ctx), slog.Int("published", published))...)
	return published, nil
}

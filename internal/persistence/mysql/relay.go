package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

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
// MySQL has no LISTEN/NOTIFY, so Relay is poll-only: Run loops on the poll
// interval calling DrainOnce until the context is cancelled.
//
// Per-row isolation: each claimed row's outcome is recorded independently
// within the single drain transaction. A successful Publish marks only that
// row published; a failed Publish increments that row's retry_count and
// pushes next_attempt_at out by a capped exponential backoff — it does NOT
// roll back the batch. A persistently-failing row is moved to status 'dead'
// after MaxDeliveryAttempts, isolating it for operator inspection.
type Relay struct {
	db           *sql.DB
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

	tel              observability.Telemetry
	eventsPublished  metric.Int64Counter
	batchDurationSec metric.Float64Histogram
}

// RelayOption configures a Relay.
type RelayOption func(*Relay)

// WithPollInterval sets the interval between DrainOnce calls in Run.
// Default: 1s.
func WithPollInterval(d time.Duration) RelayOption { return func(r *Relay) { r.pollInterval = d } }

// WithBatchSize sets the maximum number of outbox rows claimed per DrainOnce call.
// Default: 100.
func WithBatchSize(n int) RelayOption { return func(r *Relay) { r.batch = n } }

// WithRelayClock sets the clock used to stamp published_at / next_attempt_at and to
// evaluate the claim predicate. Default: clock.System(). Tests inject a fake
// clock so backoff windows are deterministic. A nil clock is ignored (the
// default is kept).
func WithRelayClock(clk clock.Clock) RelayOption {
	return func(r *Relay) {
		if clk != nil {
			r.clk = clk
		}
	}
}

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
// Default: the OTel global provider. Records wrkflw_relay_events_published_total
// (Int64Counter) and wrkflw_relay_batch_duration_seconds (Float64Histogram).
func WithRelayMeterProvider(mp metric.MeterProvider) RelayOption {
	return func(r *Relay) { r.mpOpt = observability.WithMeterProvider(mp) }
}

// NewRelay constructs a Relay that drains the MySQL outbox and publishes each
// event via pub. MySQL has no LISTEN/NOTIFY; Run polls on the interval.
func NewRelay(db *sql.DB, pub runtime.Publisher, opts ...RelayOption) *Relay {
	r := &Relay{
		db:           db,
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
	r.tel = observability.New(
		"github.com/zakyalvan/krtlwrkflw/persistence",
		filterRelayNilOpts(r.logOpt, r.tpOpt, r.mpOpt)...,
	)
	r.eventsPublished = r.tel.Int64Counter(
		"wrkflw_relay_events_published_total",
		"Total number of outbox events successfully published by the MySQL Relay.",
	)
	r.batchDurationSec = r.tel.Float64Histogram(
		"wrkflw_relay_batch_duration_seconds",
		"Wall-clock duration of each DrainOnce call in seconds.",
	)
	return r
}

// filterRelayNilOpts returns only the non-nil observability.Option values.
func filterRelayNilOpts(opts ...observability.Option) []observability.Option {
	out := opts[:0]
	for _, o := range opts {
		if o != nil {
			out = append(out, o)
		}
	}
	return out
}

// drainUntilEmpty repeatedly drains batches until DrainOnce reports an empty
// batch (coalescing a burst of events into one sweep) or an error.
func (r *Relay) drainUntilEmpty(ctx context.Context) error {
	for {
		n, err := r.DrainOnce(ctx)
		if err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
	}
}

// Run drains the outbox on each poll interval tick until ctx is cancelled.
// It returns ctx.Err() when the context is done.
//
// MySQL has no LISTEN/NOTIFY; this relay is poll-only. Publish failures no
// longer terminate Run — with per-row isolation they are recorded against the
// failing row (retry / quarantine) and the loop keeps polling. Only
// infrastructure errors (claim/commit failures) propagate and terminate the loop.
func (r *Relay) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()

	// Attempt an immediate drain before waiting for the first tick.
	if err := r.drainUntilEmpty(ctx); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := r.drainUntilEmpty(ctx); err != nil {
				if ctx.Err() != nil {
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
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, instance_id, topic, retry_count, COALESCE(last_error, ''), created_at
		   FROM wrkflw_outbox
		  WHERE status = 'dead'
		  ORDER BY id
		  LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("workflow-persistence-mysql: relay: list dead-lettered: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []runtime.DeadLetter
	for rows.Next() {
		var dl runtime.DeadLetter
		if err := rows.Scan(&dl.ID, &dl.InstanceID, &dl.Topic, &dl.RetryCount, &dl.LastError, &dl.CreatedAt); err != nil {
			return nil, fmt.Errorf("workflow-persistence-mysql: relay: list dead-lettered: scan: %w", err)
		}
		out = append(out, dl)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("workflow-persistence-mysql: relay: list dead-lettered: rows: %w", err)
	}
	return out, nil
}

// Redrive resets the given dead outbox rows back to status='pending' with
// retry_count=0, last_error=NULL, and next_attempt_at=now (via r.clk). Only
// rows that are currently status='dead' are affected; rows with any other status
// are silently skipped. Returns the number of rows successfully re-queued.
//
// Passing no ids is a no-op (returns 0, nil).
//
// MySQL has no array parameter type; the IN clause placeholders are expanded
// programmatically.
func (r *Relay) Redrive(ctx context.Context, ids ...int64) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	now := r.clk.Now()

	// Build "?,?,..." for IN clause — MySQL has no array parameter.
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1] // strip trailing comma

	args := make([]any, 0, len(ids)+1)
	args = append(args, now)
	for _, id := range ids {
		args = append(args, id)
	}

	query := fmt.Sprintf(
		`UPDATE wrkflw_outbox
		    SET status = 'pending',
		        retry_count = 0,
		        next_attempt_at = ?,
		        last_error = NULL
		  WHERE status = 'dead'
		    AND id IN (%s)`,
		placeholders,
	)
	res, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("workflow-persistence-mysql: relay: redrive: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("workflow-persistence-mysql: relay: redrive: rows affected: %w", err)
	}
	return int(n), nil
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

	drainStart := r.clk.Now()
	now := drainStart

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		infraErr := fmt.Errorf("workflow-persistence-mysql: relay: begin tx: %w", err)
		span.RecordError(infraErr)
		span.SetStatus(otelcodes.Error, infraErr.Error())
		r.tel.Logger.LogAttrs(ctx, slog.LevelError, "persistence: relay begin tx failed",
			append(r.tel.LogAttrs(ctx), slog.Any("error", infraErr))...)
		return 0, infraErr
	}
	defer func() { _ = tx.Rollback() }()

	// In MySQL 8.0 the locking clause (FOR UPDATE SKIP LOCKED) must appear AFTER
	// LIMIT, not before. LIMIT also cannot use a placeholder when combined with a
	// locking clause, so we pre-format the literal integer — it is an internal
	// value bounded by constructor validation, never external input.
	claimSQL := fmt.Sprintf(
		`SELECT id, topic, payload, instance_id, dedup_key, retry_count, definition_ref
		   FROM wrkflw_outbox
		  WHERE status = 'pending' AND next_attempt_at <= ?
		  ORDER BY id
		  LIMIT %d
		    FOR UPDATE SKIP LOCKED`,
		r.batch,
	)
	rows, err := tx.QueryContext(ctx, claimSQL, now)
	if err != nil {
		infraErr := fmt.Errorf("workflow-persistence-mysql: relay: claim: %w", err)
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
		var definitionRef string
		if err := rows.Scan(&id, &topic, &rawPayload, &instanceID, &dedupKey, &retryCount, &definitionRef); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("workflow-persistence-mysql: relay: scan: %w", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(rawPayload, &payload); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("workflow-persistence-mysql: relay: unmarshal payload id=%d: %w", id, err)
		}
		claims = append(claims, claim{id: id, retryCount: retryCount, event: runtime.OutboxEvent{
			Topic:         topic,
			Payload:       payload,
			DedupKey:      dedupKey,
			InstanceID:    instanceID,
			DefinitionRef: definitionRef,
		}})
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		infraErr := fmt.Errorf("workflow-persistence-mysql: relay: rows: %w", err)
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
		if pubErr := r.pub.Publish(ctx, c.event); pubErr != nil {
			newRetry := c.retryCount + 1
			status := "pending"
			if newRetry >= r.maxDelivery {
				status = "dead"
			}
			nextAttempt := now.Add(RelayBackoff(c.retryCount, r.backoffBase, r.backoffMax))
			if _, err := tx.ExecContext(ctx,
				`UPDATE wrkflw_outbox
				    SET retry_count = ?, status = ?, next_attempt_at = ?, last_error = ?
				  WHERE id = ?`,
				newRetry, status, nextAttempt, pubErr.Error(), c.id,
			); err != nil {
				infraErr := fmt.Errorf("workflow-persistence-mysql: relay: quarantine id=%d: %w", c.id, err)
				span.RecordError(infraErr)
				span.SetStatus(otelcodes.Error, infraErr.Error())
				r.tel.Logger.LogAttrs(ctx, slog.LevelError, "persistence: relay quarantine failed",
					append(r.tel.LogAttrs(ctx), slog.Any("error", infraErr))...)
				return 0, infraErr
			}
			continue
		}
		// Mark this row published, inside the open transaction.
		if _, err := tx.ExecContext(ctx,
			`UPDATE wrkflw_outbox SET status = 'published', published_at = ? WHERE id = ?`,
			now, c.id,
		); err != nil {
			infraErr := fmt.Errorf("workflow-persistence-mysql: relay: mark published id=%d: %w", c.id, err)
			span.RecordError(infraErr)
			span.SetStatus(otelcodes.Error, infraErr.Error())
			r.tel.Logger.LogAttrs(ctx, slog.LevelError, "persistence: relay mark-published failed",
				append(r.tel.LogAttrs(ctx), slog.Any("error", infraErr))...)
			return 0, infraErr
		}
		published++
	}

	if err := tx.Commit(); err != nil {
		infraErr := fmt.Errorf("workflow-persistence-mysql: relay: commit: %w", err)
		span.RecordError(infraErr)
		span.SetStatus(otelcodes.Error, infraErr.Error())
		r.tel.Logger.LogAttrs(ctx, slog.LevelError, "persistence: relay commit failed",
			append(r.tel.LogAttrs(ctx), slog.Any("error", infraErr))...)
		return 0, infraErr
	}

	span.SetAttributes(attribute.Int("wrkflw.batch_size", published))
	r.tel.Logger.LogAttrs(ctx, slog.LevelDebug, "persistence: relay drained batch",
		append(r.tel.LogAttrs(ctx), slog.Int("published", published))...)

	if published > 0 {
		r.eventsPublished.Add(ctx, int64(published))
	}
	elapsed := r.clk.Now().Sub(drainStart)
	r.batchDurationSec.Record(ctx, elapsed.Seconds())

	return published, nil
}

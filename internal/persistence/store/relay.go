package store

import (
	"context"
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
	"github.com/zakyalvan/krtlwrkflw/internal/database"
	"github.com/zakyalvan/krtlwrkflw/internal/database/transaction"
	"github.com/zakyalvan/krtlwrkflw/internal/observability"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// Relay drains wrkflw_outbox and hands each event to a [runtime.Publisher]
// (at-least-once delivery). It branches on dialect capabilities to claim due
// pending rows without double-publishing:
//
//   - Postgres (SupportsSkipLocked=true): SELECT … FOR UPDATE SKIP LOCKED inside
//     a transaction so multiple concurrent Relay instances cooperate.
//   - MySQL (SupportsSkipLocked=true): same SELECT … LIMIT n FOR UPDATE SKIP
//     LOCKED (literal LIMIT, MySQL restriction) inside a transaction.
//   - SQLite (SupportsSkipLocked=false): plain SELECT … LIMIT n under the
//     single-writer contract — no concurrent relay is expected.
//
// Per-row isolation (ADR-0017): each claimed row's outcome is recorded
// independently. A successful Publish marks only that row published; a failed
// Publish increments retry_count and advances next_attempt_at by a capped
// exponential backoff. A row reaching MaxDeliveryAttempts is moved to
// status='dead'. A poison row never blocks healthy peers in the same batch.
//
// Run polls on the configured interval. For Task 18, a nil wake channel is
// already wired into the select — the notifier-wake path is a clean addition.
type Relay struct {
	conn    any
	d       dialect.Dialect
	pub     runtime.Publisher
	clk     clock.Clock
	poll    time.Duration
	batch   int
	maxDel  int
	backoff struct{ base, max time.Duration }

	// wake is an optional channel a LISTEN notifier (Task 18) can signal to
	// trigger an immediate drain. It is nil in the current poll-only mode.
	wake <-chan struct{}

	// staged telemetry options
	logOpt observability.Option
	tpOpt  observability.Option
	mpOpt  observability.Option

	tel              observability.Telemetry
	eventsPublished  metric.Int64Counter
	batchDurationSec metric.Float64Histogram
}

// compile-time interface checks.
var (
	_ runtime.OutboxStatsReader = (*Relay)(nil)
)

// RelayOption configures a [Relay] built by [NewRelay].
type RelayOption func(*Relay)

// WithRelayPollInterval sets the interval between [Relay.DrainOnce] calls in
// [Relay.Run]. Default: 1 s.
func WithRelayPollInterval(d time.Duration) RelayOption { return func(r *Relay) { r.poll = d } }

// WithRelayBatchSize sets the maximum number of outbox rows claimed per
// [Relay.DrainOnce] call. Default: 100.
func WithRelayBatchSize(n int) RelayOption { return func(r *Relay) { r.batch = n } }

// WithRelayClock sets the clock used to stamp published_at / next_attempt_at
// and to evaluate which rows are due. A nil clock is ignored; the default
// (clock.System()) is kept.
func WithRelayClock(clk clock.Clock) RelayOption {
	return func(r *Relay) {
		if clk != nil {
			r.clk = clk
		}
	}
}

// WithRelayMaxDeliveryAttempts sets how many consecutive publish failures a row
// tolerates before it is quarantined to status='dead'. Default: 10.
// A value ≤ 0 is ignored.
func WithRelayMaxDeliveryAttempts(n int) RelayOption {
	return func(r *Relay) {
		if n > 0 {
			r.maxDel = n
		}
	}
}

// WithRelayBackoff sets the base and maximum interval of the capped exponential
// backoff applied to next_attempt_at after a failed publish.
// Defaults: base 1 s, max 1 m. Non-positive values are ignored.
func WithRelayBackoff(base, maxInterval time.Duration) RelayOption {
	return func(r *Relay) {
		if base > 0 {
			r.backoff.base = base
		}
		if maxInterval > 0 {
			r.backoff.max = maxInterval
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

// NewRelay constructs a Relay that drains the outbox via conn and publishes
// each event via pub. conn must be a *pgxpool.Pool (Postgres) or *sql.DB
// (MySQL / SQLite); d is the matching [dialect.Dialect].
func NewRelay(conn any, d dialect.Dialect, pub runtime.Publisher, opts ...RelayOption) *Relay {
	r := &Relay{
		conn:   conn,
		d:      d,
		pub:    pub,
		clk:    clock.System(),
		poll:   time.Second,
		batch:  100,
		maxDel: 10,
	}
	r.backoff.base = time.Second
	r.backoff.max = time.Minute
	for _, o := range opts {
		o(r)
	}
	r.tel = observability.New(
		"github.com/zakyalvan/krtlwrkflw/persistence",
		filterNilOpts(r.logOpt, r.tpOpt, r.mpOpt)...,
	)
	r.eventsPublished = r.tel.Int64Counter(
		"wrkflw_relay_events_published_total",
		"Total number of outbox events successfully published by the Relay.",
	)
	r.batchDurationSec = r.tel.Float64Histogram(
		"wrkflw_relay_batch_duration_seconds",
		"Wall-clock duration of each DrainOnce call in seconds.",
	)
	return r
}

// DrainOnce claims one batch of due pending outbox rows (status='pending' AND
// next_attempt_at <= now, ORDER BY id [FOR UPDATE SKIP LOCKED on PG/MySQL]),
// publishes each via the Publisher, and records each row's outcome independently
// in the SAME transaction:
//
//   - on success: status='published', published_at=now.
//   - on publish failure: retry_count++, next_attempt_at+=backoff, last_error=err;
//     if retry_count reaches MaxDeliveryAttempts, status='dead'.
//
// Correctness invariant: claim + Publish + mark all run inside ONE transaction
// that is committed only at the end. The SELECT … FOR UPDATE SKIP LOCKED lock is
// held across the entire publish+mark phase so concurrent Relay replicas skip
// already-claimed rows instead of re-claiming and re-publishing them
// (no double-publish). SQLite uses the same single-tx shape without the locking
// clause because it is single-writer by contract.
//
// A publish failure does NOT abort the drain — healthy rows in the same batch
// are still marked published. The whole batch commits atomically. At-least-once
// is preserved: a row becomes 'published' only after a successful Publish call.
//
// Returns the number of rows successfully published in this drain.
func (r *Relay) DrainOnce(ctx context.Context) (int, error) {
	ctx, span := r.tel.Tracer.Start(ctx, "wrkflw.relay.batch")
	defer span.End()

	drainStart := r.clk.Now()
	now := drainStart

	// Open the single transaction that holds claim+publish+mark.
	q, txCtx, err := r.beginDrainTx(ctx)
	if err != nil {
		infraErr := fmt.Errorf("workflow-store: relay: begin tx: %w", err)
		span.RecordError(infraErr)
		span.SetStatus(otelcodes.Error, infraErr.Error())
		r.tel.Logger.LogAttrs(ctx, slog.LevelError, "persistence: relay begin tx failed",
			append(r.tel.LogAttrs(ctx), slog.Any("error", infraErr))...)
		return 0, infraErr
	}
	committed := false
	defer func() {
		if !committed {
			_ = q.Rollback(txCtx)
		}
	}()

	// Claim due pending rows — locks held until commit (FOR UPDATE SKIP LOCKED
	// on PG/MySQL; plain SELECT on SQLite single-writer).
	claims, err := r.claimInTx(txCtx, q, now)
	if err != nil {
		infraErr := fmt.Errorf("workflow-store: relay: claim: %w", err)
		span.RecordError(infraErr)
		span.SetStatus(otelcodes.Error, infraErr.Error())
		r.tel.Logger.LogAttrs(ctx, slog.LevelError, "persistence: relay claim failed",
			append(r.tel.LogAttrs(ctx), slog.Any("error", infraErr))...)
		return 0, infraErr
	}

	if len(claims) == 0 {
		// No rows to drain; commit the empty tx (or rollback — either is fine).
		_ = q.Commit(txCtx)
		committed = true
		span.SetAttributes(attribute.Int("wrkflw.batch_size", 0))
		return 0, nil
	}

	// Publish each claim and record per-row outcome inside the same open tx.
	published := 0
	for _, c := range claims {
		if pubErr := r.pub.Publish(ctx, c.event); pubErr != nil {
			// Publish failure: increment retry_count, advance next_attempt_at,
			// quarantine to 'dead' once MaxDeliveryAttempts is reached.
			newRetry := c.retryCount + 1
			status := "pending"
			if newRetry >= r.maxDel {
				status = "dead"
			}
			nextAttempt := now.Add(RelayBackoff(c.retryCount, r.backoff.base, r.backoff.max))
			if _, err := q.Exec(txCtx, r.d.Rebind(
				`UPDATE wrkflw_outbox
				    SET retry_count = ?, status = ?, next_attempt_at = ?, last_error = ?
				  WHERE id = ?`),
				newRetry, status, timeArg(r.d, nextAttempt), pubErr.Error(), c.id,
			); err != nil {
				infraErr := fmt.Errorf("workflow-store: relay: quarantine id=%d: %w", c.id, err)
				span.RecordError(infraErr)
				span.SetStatus(otelcodes.Error, infraErr.Error())
				r.tel.Logger.LogAttrs(ctx, slog.LevelError, "persistence: relay quarantine failed",
					append(r.tel.LogAttrs(ctx), slog.Any("error", infraErr))...)
				return 0, infraErr
			}
			continue
		}
		// Mark published inside the open transaction. If commit fails the row
		// stays pending (at-least-once, not at-most-once).
		if _, err := q.Exec(txCtx, r.d.Rebind(
			`UPDATE wrkflw_outbox SET status = 'published', published_at = ? WHERE id = ?`),
			timeArg(r.d, now), c.id,
		); err != nil {
			infraErr := fmt.Errorf("workflow-store: relay: mark published id=%d: %w", c.id, err)
			span.RecordError(infraErr)
			span.SetStatus(otelcodes.Error, infraErr.Error())
			r.tel.Logger.LogAttrs(ctx, slog.LevelError, "persistence: relay mark-published failed",
				append(r.tel.LogAttrs(ctx), slog.Any("error", infraErr))...)
			return 0, infraErr
		}
		published++
	}

	if err := q.Commit(txCtx); err != nil {
		infraErr := fmt.Errorf("workflow-store: relay: commit: %w", err)
		span.RecordError(infraErr)
		span.SetStatus(otelcodes.Error, infraErr.Error())
		r.tel.Logger.LogAttrs(ctx, slog.LevelError, "persistence: relay commit failed",
			append(r.tel.LogAttrs(ctx), slog.Any("error", infraErr))...)
		return 0, infraErr
	}
	committed = true

	span.SetAttributes(
		attribute.Int("wrkflw.batch_size", len(claims)),
		attribute.Int("wrkflw.published_count", published),
	)
	r.tel.Logger.LogAttrs(ctx, slog.LevelDebug, "persistence: relay drained batch",
		append(r.tel.LogAttrs(ctx), slog.Int("published", published))...)

	if published > 0 {
		r.eventsPublished.Add(ctx, int64(published))
	}
	elapsed := r.clk.Now().Sub(drainStart)
	r.batchDurationSec.Record(ctx, elapsed.Seconds())

	return published, nil
}

// beginDrainTx starts the single transaction used for claim+publish+mark.
// It returns the transaction Querier together with a context that carries the
// tx (so nested JoinOrBegin calls in helpers join it automatically).
func (r *Relay) beginDrainTx(ctx context.Context) (transaction.Querier, context.Context, error) {
	return transaction.Begin(ctx, r.conn)
}

// claimRow holds one claimed outbox row.
type claimRow struct {
	id         int64
	retryCount int
	event      runtime.OutboxEvent
}

// claimInTx selects due pending rows on the already-open Querier q.
// For SupportsSkipLocked dialects (PG/MySQL) the query includes FOR UPDATE
// SKIP LOCKED so concurrent Relay replicas see no overlap; for SQLite
// (single-writer) a plain SELECT is used — no locking clause needed.
//
// MySQL requires the LIMIT to be a literal integer in the query string when
// combined with a locking clause — it is an internal constant, never external input.
func (r *Relay) claimInTx(ctx context.Context, q transaction.Querier, now time.Time) ([]claimRow, error) {
	var claimSQL string
	if r.d.SupportsSkipLocked() {
		claimSQL = fmt.Sprintf(
			`SELECT id, topic, payload, instance_id, dedup_key, retry_count, definition_ref
			   FROM wrkflw_outbox
			  WHERE status = 'pending' AND next_attempt_at <= ?
			  ORDER BY id
			  LIMIT %d
			    FOR UPDATE SKIP LOCKED`,
			r.batch,
		)
	} else {
		// SQLite: single-writer, plain SELECT.
		claimSQL = fmt.Sprintf(
			`SELECT id, topic, payload, instance_id, dedup_key, retry_count, definition_ref
			   FROM wrkflw_outbox
			  WHERE status = 'pending' AND next_attempt_at <= ?
			  ORDER BY id
			  LIMIT %d`,
			r.batch,
		)
	}

	rows, err := q.Query(ctx, r.d.Rebind(claimSQL), timeArg(r.d, now))
	if err != nil {
		return nil, fmt.Errorf("claim query: %w", err)
	}

	claims, scanErr := scanClaimRows(rows)
	_ = rows.Close()
	if scanErr != nil {
		return nil, scanErr
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration: %w", err)
	}
	return claims, nil
}

// scanClaimRows scans a rows result into []claimRow.
func scanClaimRows(rows database.Rows) ([]claimRow, error) {
	var out []claimRow
	for rows.Next() {
		var (
			id            int64
			topic         string
			rawPayload    []byte
			instanceID    string
			dedupKey      string
			retryCount    int
			definitionRef string
		)
		if err := rows.Scan(&id, &topic, &rawPayload, &instanceID, &dedupKey, &retryCount, &definitionRef); err != nil {
			return nil, fmt.Errorf("workflow-store: relay: scan: %w", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(rawPayload, &payload); err != nil {
			return nil, fmt.Errorf("workflow-store: relay: unmarshal payload id=%d: %w", id, err)
		}
		out = append(out, claimRow{id: id, retryCount: retryCount, event: runtime.OutboxEvent{
			Topic:         topic,
			Payload:       payload,
			DedupKey:      dedupKey,
			InstanceID:    instanceID,
			DefinitionRef: definitionRef,
		}})
	}
	return out, nil
}

// drainUntilEmpty repeatedly calls DrainOnce until the batch is empty or an
// error occurs (coalescing bursts into a single sweep).
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
// Publish failures no longer terminate Run: with per-row isolation they are
// recorded against the failing row (retry / quarantine) and the loop keeps
// polling. Only infrastructure errors (claim / commit failures) propagate and
// terminate the loop.
//
// Task 18 hook: populate r.wake before Run to add notifier-driven drain.
// The poll ticker stays as a fallback; Run's select is already structured to
// accept the wake case once Task 18 wires the channel.
func (r *Relay) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.poll)
	defer ticker.Stop()

	// Attempt an immediate drain before the first tick.
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
		case <-r.wake: // nil in poll-only mode; Task 18 wires this channel
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
	q, err := database.From(r.conn)
	if err != nil {
		return nil, fmt.Errorf("workflow-store: relay: list dead-lettered: conn: %w", err)
	}
	rows, err := q.Query(ctx, r.d.Rebind(fmt.Sprintf(
		`SELECT id, instance_id, topic, retry_count, COALESCE(last_error, ''), created_at
		   FROM wrkflw_outbox
		  WHERE status = 'dead'
		  ORDER BY id
		  LIMIT %d`, limit)),
	)
	if err != nil {
		return nil, fmt.Errorf("workflow-store: relay: list dead-lettered: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []runtime.DeadLetter
	for rows.Next() {
		var dl runtime.DeadLetter
		if r.d.TimestampsAsText() {
			var createdAtStr string
			if err := rows.Scan(&dl.ID, &dl.InstanceID, &dl.Topic, &dl.RetryCount, &dl.LastError, &createdAtStr); err != nil {
				return nil, fmt.Errorf("workflow-store: relay: list dead-lettered: scan: %w", err)
			}
			t, err := parseTimeText(createdAtStr)
			if err != nil {
				return nil, fmt.Errorf("workflow-store: relay: list dead-lettered: parse created_at: %w", err)
			}
			dl.CreatedAt = t
		} else {
			if err := rows.Scan(&dl.ID, &dl.InstanceID, &dl.Topic, &dl.RetryCount, &dl.LastError, &dl.CreatedAt); err != nil {
				return nil, fmt.Errorf("workflow-store: relay: list dead-lettered: scan: %w", err)
			}
			dl.CreatedAt = dl.CreatedAt.UTC()
		}
		out = append(out, dl)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("workflow-store: relay: list dead-lettered: rows: %w", err)
	}
	return out, nil
}

// Redrive resets the given dead outbox rows back to status='pending' with
// retry_count=0, last_error=NULL, and next_attempt_at=now (via r.clk). Only
// rows with status='dead' are affected; others are silently skipped. Returns
// the number of rows successfully re-queued.
//
// Passing no ids is a no-op (returns 0, nil).
//
// A generic IN(?,?,...) placeholder list is used for all dialects — Postgres
// accepts IN just as well as = ANY($n), and it keeps the code path unified.
func (r *Relay) Redrive(ctx context.Context, ids ...int64) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	now := r.clk.Now()

	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]

	args := make([]any, 0, len(ids)+1)
	args = append(args, timeArg(r.d, now))
	for _, id := range ids {
		args = append(args, id)
	}

	q, err := database.From(r.conn)
	if err != nil {
		return 0, fmt.Errorf("workflow-store: relay: redrive: conn: %w", err)
	}
	res, err := q.Exec(ctx, r.d.Rebind(
		`UPDATE wrkflw_outbox
		    SET status = 'pending',
		        retry_count = 0,
		        next_attempt_at = ?,
		        last_error = NULL
		  WHERE status = 'dead'
		    AND id IN (`+placeholders+`)`),
		args...,
	)
	if err != nil {
		return 0, fmt.Errorf("workflow-store: relay: redrive: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("workflow-store: relay: redrive: rows affected: %w", err)
	}
	return int(n), nil
}

// OutboxStats returns aggregate statistics about the wrkflw_outbox table:
// the count of pending rows, the count of dead rows, and the age of the oldest
// pending row. When there are no pending rows OldestPendingAge is zero.
//
// The query is dialect-specific and provided by [dialect.Dialect.OutboxStatsQuery].
// Postgres returns the age as a float64 (EXTRACT(EPOCH FROM …) yields numeric);
// MySQL and SQLite return an integer. We scan into float64 for all dialects —
// pgx coerces its numeric to float64 cleanly, and Go's database/sql converts
// integer values to float64 without loss for the typical age range.
func (r *Relay) OutboxStats(ctx context.Context) (runtime.OutboxStats, error) {
	q, err := database.From(r.conn)
	if err != nil {
		return runtime.OutboxStats{}, fmt.Errorf("workflow-store: relay: outbox stats: conn: %w", err)
	}
	var pending, dead int64
	var ageSec float64
	err = q.QueryRow(ctx, r.d.OutboxStatsQuery()).Scan(&pending, &dead, &ageSec)
	if err != nil {
		return runtime.OutboxStats{}, fmt.Errorf("workflow-store: relay: outbox stats: %w", err)
	}
	return runtime.OutboxStats{
		Pending:          pending,
		Dead:             dead,
		OldestPendingAge: time.Duration(ageSec * float64(time.Second)),
	}, nil
}

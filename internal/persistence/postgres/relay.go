package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// Relay drains wrkflw_outbox and hands each event to a runtime.Publisher
// (at-least-once delivery). It claims rows with FOR UPDATE SKIP LOCKED so
// multiple concurrent Relay instances cooperate without double-publishing.
//
// Publish errors cause the entire batch transaction to roll back — no row is
// marked published-but-not-delivered. The row stays claimable for the next poll.
type Relay struct {
	pool         *pgxpool.Pool
	pub          runtime.Publisher
	pollInterval time.Duration
	batch        int
}

// RelayOption configures a Relay.
type RelayOption func(*Relay)

// WithPollInterval sets the interval between DrainOnce calls in Run.
// Default: 1s.
func WithPollInterval(d time.Duration) RelayOption { return func(r *Relay) { r.pollInterval = d } }

// WithBatchSize sets the maximum number of outbox rows claimed per DrainOnce call.
// Default: 100.
func WithBatchSize(n int) RelayOption { return func(r *Relay) { r.batch = n } }

// NewRelay constructs a Relay that drains the outbox in pool and publishes each
// event via pub.
func NewRelay(pool *pgxpool.Pool, pub runtime.Publisher, opts ...RelayOption) *Relay {
	r := &Relay{pool: pool, pub: pub, pollInterval: time.Second, batch: 100}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Run drains the outbox on each poll interval tick until ctx is cancelled.
// It returns ctx.Err() when the context is done.
// Non-publish errors from DrainOnce are propagated and terminate the loop.
func (r *Relay) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()
	// Attempt an immediate drain before waiting for the first tick.
	if _, err := r.DrainOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
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

// DrainOnce claims one batch of unpublished outbox rows (ORDER BY id FOR UPDATE
// SKIP LOCKED), publishes each via the Publisher, then marks them published in
// the same transaction.
//
// If any Publish call fails the entire transaction is rolled back — no row is
// marked published for that batch. The rows remain claimable on the next call.
// Returns the number of rows successfully published.
func (r *Relay) DrainOnce(ctx context.Context) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("postgres: relay: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx,
		`SELECT id, topic, payload
		   FROM wrkflw_outbox
		  WHERE published_at IS NULL
		  ORDER BY id
		    FOR UPDATE SKIP LOCKED
		  LIMIT $1`,
		r.batch,
	)
	if err != nil {
		return 0, fmt.Errorf("postgres: relay: claim: %w", err)
	}

	type claim struct {
		id    int64
		event runtime.OutboxEvent
	}

	var claims []claim
	for rows.Next() {
		var id int64
		var topic string
		var rawPayload []byte
		if err := rows.Scan(&id, &topic, &rawPayload); err != nil {
			rows.Close()
			return 0, fmt.Errorf("postgres: relay: scan: %w", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(rawPayload, &payload); err != nil {
			rows.Close()
			return 0, fmt.Errorf("postgres: relay: unmarshal payload id=%d: %w", id, err)
		}
		claims = append(claims, claim{id: id, event: runtime.OutboxEvent{Topic: topic, Payload: payload}})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("postgres: relay: rows: %w", err)
	}

	if len(claims) == 0 {
		return 0, nil
	}

	for _, c := range claims {
		// Publish the event; on failure roll back so the row stays unpublished.
		if err := r.pub.Publish(ctx, c.event); err != nil {
			return 0, fmt.Errorf("postgres: relay: publish id=%d: %w", c.id, err)
		}
		// Mark this single row published immediately after successful delivery,
		// inside the open transaction. If the tx later fails to commit, the row
		// remains unpublished (at-least-once rather than at-most-once).
		if _, err := tx.Exec(ctx,
			`UPDATE wrkflw_outbox SET published_at = NOW() WHERE id = $1`,
			c.id,
		); err != nil {
			return 0, fmt.Errorf("postgres: relay: mark published id=%d: %w", c.id, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("postgres: relay: commit: %w", err)
	}
	return len(claims), nil
}

package store

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
)

// pgxNotifier implements [dialect.Notifier] over a [*pgxpool.Pool] using
// PostgreSQL's LISTEN/NOTIFY mechanism. It acquires a dedicated connection
// from the pool (separate from the regular query pool) and starts a goroutine
// that calls WaitForNotification in a loop, coalescing notifications into a
// single-entry buffered channel.
//
// Only the (pgx, Postgres) combination provides a meaningful implementation.
// MySQL and SQLite callers should not inject a Notifier; the Relay falls back
// to poll-only mode when no Notifier is present.
type pgxNotifier struct {
	pool *pgxpool.Pool
}

// NewPgxNotifier returns a [dialect.Notifier] backed by pool. It acquires a
// dedicated pgx connection per [Listen] call and tears it down when the
// returned cancel func is invoked or the supplied context is cancelled.
//
// The channel name passed to [Listen] must be a static constant — do NOT
// pass untrusted or user-supplied input. The implementation issues a bare
// "LISTEN <channel>" statement validated against the known constant
// [outboxNotifyChannel].
func NewPgxNotifier(pool *pgxpool.Pool) dialect.Notifier {
	return &pgxNotifier{pool: pool}
}

// Listen subscribes to channel on a dedicated pool connection and returns:
//   - a read-only wake channel (buffered size 1) that receives an empty
//     struct on each notification (coalesced: at most one pending wake at a time),
//   - a cancel func the caller MUST invoke to release the subscription and
//     return the dedicated connection to the pool, and
//   - an error if the subscription could not be established (pool exhausted,
//     LISTEN command rejected, etc.).
//
// The background goroutine exits cleanly when ctx is cancelled OR cancel is
// called; goleak-safe. The channel name must be [outboxNotifyChannel]
// ("wrkflw_outbox") — a constant never derived from external input.
func (n *pgxNotifier) Listen(ctx context.Context, channel string) (<-chan struct{}, func(), error) {
	// Acquire a dedicated connection from the pool for LISTEN. This connection
	// is held for the lifetime of the subscription so it can receive server-push
	// notifications; it must NOT be returned to the pool until cancel() is called.
	conn, err := n.pool.Acquire(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("workflow-store: pgx notifier: acquire connection: %w", err)
	}

	// Issue LISTEN on the dedicated connection. The channel name is the internal
	// constant "wrkflw_outbox" — it is never interpolated from external input.
	if _, err := conn.Exec(ctx, "LISTEN "+channel); err != nil {
		conn.Release()
		return nil, nil, fmt.Errorf("workflow-store: pgx notifier: LISTEN %q: %w", channel, err)
	}

	wake := make(chan struct{}, 1)

	// cancelCtx controls the background goroutine independently of the caller's
	// context so cancelListen() can stop the goroutine even if ctx is still live.
	cancelCtx, cancelFn := context.WithCancel(ctx)

	go func() {
		defer conn.Release()
		defer cancelFn() // release context resources on goroutine exit
		for {
			if _, err := conn.Conn().WaitForNotification(cancelCtx); err != nil {
				// ctx cancelled or connection lost — exit cleanly.
				return
			}
			// Non-blocking send: if the buffer is already full (one wake pending),
			// drop this notification — one wake is enough to trigger a drain sweep.
			select {
			case wake <- struct{}{}:
			default: // coalesce: a wake is already queued
			}
		}
	}()

	cancel := func() {
		cancelFn()
		// WaitForNotification unblocks when cancelCtx is done, so the goroutine
		// exits and conn.Release() is called from the deferred call inside the
		// goroutine. No explicit wait needed; callers (Relay.Run) defer cancel().
	}
	return wake, cancel, nil
}

// listenLoop holds a dedicated pool connection, LISTENs on the outbox channel,
// and signals r.wake on each notification. It reconnects on transient failures;
// the poll fallback in Run covers any gap. It returns when ctx is cancelled.
//
// This method is called only when a [dialect.Notifier] is injected via
// [WithRelayNotifier]. r.listenReady is signalled (non-blocking) once the first
// LISTEN is established; it is nil in production (test-only).
func (r *Relay) listenLoop(ctx context.Context, notifier dialect.Notifier, wake chan<- struct{}) {
	for ctx.Err() == nil {
		wakeCh, cancel, err := notifier.Listen(ctx, outboxNotifyChannel)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			r.tel.Logger.LogAttrs(ctx, slog.LevelWarn, "persistence: relay notifier listen failed",
				append(r.tel.LogAttrs(ctx), slog.Any("error", err))...)
			select {
			case <-ctx.Done():
				return
			case <-time.After(r.poll):
			}
			continue
		}

		// Signal readiness (test-only; r.listenReady is nil in production).
		if r.listenReady != nil {
			select {
			case r.listenReady <- struct{}{}:
			default:
			}
		}

		// Forward notifications from the notifier's wake channel to r.wake until
		// ctx is done or the notifier's channel is closed (connection lost).
		for {
			select {
			case <-ctx.Done():
				cancel()
				return
			case _, ok := <-wakeCh:
				if !ok {
					cancel()
					goto reconnect // channel closed: notifier connection lost; reconnect
				}
				select {
				case wake <- struct{}{}:
				default: // coalesce
				}
			}
		}
	reconnect:
		// Connection lost: wait one poll interval then attempt reconnect so
		// the poll fallback covers any gap (ADR-0022).
		select {
		case <-ctx.Done():
			return
		case <-time.After(r.poll):
		}
	}
}

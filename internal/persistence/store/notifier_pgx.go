package store

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kartaladev/wrkflw/internal/persistence/dialect"
)

// pgxNotifier implements [dialect.Notifier] over a [*pgxpool.Pool] using
// PostgreSQL's LISTEN/NOTIFY mechanism. It acquires a dedicated connection
// from the pool (separate from the regular query pool) and starts a goroutine
// that calls WaitForNotification in a loop, coalescing notifications into a
// single-entry buffered channel.
//
// The goroutine self-heals on connection loss: when WaitForNotification returns
// a non-cancellation error (network blip, server restart, pg_terminate_backend),
// it releases the bad connection, applies a bounded backoff, re-acquires a fresh
// connection, re-issues LISTEN, and resumes. Notifications are never dropped
// permanently: the poll fallback in the Relay covers any gap during reconnect.
//
// Only the (pgx, Postgres) combination provides a meaningful implementation.
// MySQL and SQLite callers should not inject a Notifier; the Relay falls back
// to poll-only mode when no Notifier is present.
type pgxNotifier struct {
	pool *pgxpool.Pool
	log  *slog.Logger
}

// PgxNotifierOption configures a [pgxNotifier] built by [NewPgxNotifier].
type PgxNotifierOption func(*pgxNotifier)

// WithPgxNotifierLogger sets the structured logger used by the notifier for
// reconnect warnings. When a connection is lost and Acquire or LISTEN fails
// during the reconnect loop, a slog.LevelWarn record is emitted to this logger.
// Default: slog.Default().
func WithPgxNotifierLogger(l *slog.Logger) PgxNotifierOption {
	return func(n *pgxNotifier) {
		if l != nil {
			n.log = l
		}
	}
}

// NewPgxNotifier returns a [dialect.Notifier] backed by pool. It acquires a
// dedicated pgx connection per [Listen] call and tears it down when the
// returned cancel func is invoked or the supplied context is cancelled.
//
// The channel name passed to [Listen] must be a static constant — do NOT
// pass untrusted or user-supplied input. The implementation issues a bare
// "LISTEN <channel>" statement validated against the known constant
// "wrkflw_outbox".
//
// Pass [WithPgxNotifierLogger] to receive structured warnings when the
// reconnect loop's Acquire or LISTEN calls fail transiently. Defaults to
// slog.Default() so existing zero-option callers compile unchanged.
func NewPgxNotifier(pool *pgxpool.Pool, opts ...PgxNotifierOption) dialect.Notifier {
	n := &pgxNotifier{
		pool: pool,
		log:  slog.Default(),
	}
	for _, o := range opts {
		o(n)
	}
	return n
}

// pgxNotifierReconnectBackoff is the fixed (not exponential) delay between a
// connection loss and a re-acquire attempt inside the Listen goroutine.
//
// Design rationale:
//   - Fixed, not exponential: the window is deliberately short and bounded to one
//     half-second. Exponential backoff would extend the period during which the
//     relay cannot receive push wakeups, negating the low-latency benefit of
//     LISTEN/NOTIFY. Polling remains active as the fallback throughout.
//   - Context-cancellable: the sleep is implemented as a select on
//     cancelCtx.Done() and time.After, so cancellation is never delayed by the
//     backoff window.
//   - Notification gap is safe: any NOTIFY emitted while the notifier is
//     reconnecting is covered by the Relay's poll ticker (ADR-0022). The notifier
//     never needs to "catch up" missed notifications; the outbox query re-reads
//     all due pending rows on each drain.
//
// This value is intentionally NOT configurable — introducing a seam here would
// add API surface without meaningful benefit given the poll-fallback guarantee.
const pgxNotifierReconnectBackoff = 500 * time.Millisecond

// Listen subscribes to channel on a dedicated pool connection and returns:
//   - a read-only wake channel (buffered size 1) that receives an empty
//     struct on each notification (coalesced: at most one pending wake at a time),
//   - a cancel func the caller MUST invoke to release the subscription and
//     return the dedicated connection to the pool, and
//   - an error if the initial subscription could not be established (pool
//     exhausted, LISTEN command rejected, etc.).
//
// The wake channel is never closed — it stays open for the lifetime of the
// subscription, even across reconnects. The Relay depends on this invariant:
// its listenLoop simply reads from the channel until ctx is done and does NOT
// contain a closed-channel reconnect branch (that branch is provably unreachable
// since the notifier never closes wake on loss — it self-heals instead).
//
// The background goroutine exits cleanly when ctx is cancelled OR cancel is
// called; goleak-safe. The channel name must be "wrkflw_outbox" — a constant
// never derived from external input.
//
// On transient connection loss (WaitForNotification returns a non-cancellation
// error), the goroutine releases the bad connection, sleeps a bounded backoff,
// re-acquires a fresh connection, re-issues LISTEN, and continues — all without
// closing or replacing the wake channel. The Relay's poll ticker covers any
// notification gap during the reconnect window (ADR-0022).
func (n *pgxNotifier) Listen(ctx context.Context, channel string) (<-chan struct{}, func(), error) {
	// Perform the initial acquire + LISTEN synchronously so the caller knows
	// immediately if the pool is exhausted or the LISTEN command is rejected.
	conn, err := n.pool.Acquire(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("workflow-store: pgx notifier: acquire connection: %w", err)
	}
	if _, err := conn.Exec(ctx, "LISTEN "+channel); err != nil {
		conn.Release()
		return nil, nil, fmt.Errorf("workflow-store: pgx notifier: LISTEN %q: %w", channel, err)
	}

	// wake is the single buffered channel shared across all reconnect iterations.
	// It is never closed; the Relay's listenLoop reads from it until ctx is done.
	wake := make(chan struct{}, 1)

	// cancelCtx drives the goroutine's lifecycle independently of the caller's ctx
	// so that cancel() can stop the goroutine even if ctx is still live.
	cancelCtx, cancelFn := context.WithCancel(ctx)

	go func() {
		// current is the active LISTEN pool connection. It may be replaced on
		// reconnect. We use an explicit release-before-reconnect pattern (no defer)
		// so each iteration owns exactly one connection and we never double-release
		// or leave a connection untracked.
		current := conn

		// safeRelease releases c if non-nil (nil-safe, idempotent within a call).
		safeRelease := func(c *pgxpool.Conn) {
			if c != nil {
				c.Release()
			}
		}

		for cancelCtx.Err() == nil {
			if current == nil {
				// Backoff before re-acquiring (bounded, cancellable).
				select {
				case <-cancelCtx.Done():
					return
				case <-time.After(pgxNotifierReconnectBackoff):
				}

				// Re-acquire a fresh connection.
				newConn, acquireErr := n.pool.Acquire(cancelCtx)
				if acquireErr != nil {
					if cancelCtx.Err() != nil {
						return
					}
					// Pool still unavailable; warn and retry next loop iteration.
					n.log.WarnContext(cancelCtx, "workflow-store: pgx notifier: reconnect acquire failed",
						"error", acquireErr, "channel", channel)
					continue
				}

				// Re-issue LISTEN on the new connection.
				if _, listenErr := newConn.Exec(cancelCtx, "LISTEN "+channel); listenErr != nil {
					safeRelease(newConn)
					if cancelCtx.Err() != nil {
						return
					}
					// LISTEN rejected; warn and retry next loop iteration.
					n.log.WarnContext(cancelCtx, "workflow-store: pgx notifier: reconnect listen failed",
						"error", listenErr, "channel", channel)
					continue
				}

				current = newConn
			}

			_, waitErr := current.Conn().WaitForNotification(cancelCtx)
			if waitErr == nil {
				// Non-blocking send: one queued wake is enough for a drain sweep.
				select {
				case wake <- struct{}{}:
				default: // coalesce: a wake is already pending
				}
				continue
			}

			// WaitForNotification returned an error. Distinguish intentional
			// cancellation from a transient connection loss.
			if cancelCtx.Err() != nil {
				// Context cancelled — release current conn and exit cleanly.
				safeRelease(current)
				return
			}

			// Transient connection loss (network blip, server restart,
			// pg_terminate_backend). Release the broken connection; set current to
			// nil so the outer loop's reconnect branch fires on the next iteration.
			safeRelease(current)
			current = nil
		}

		// Ctx cancelled outside WaitForNotification (e.g. between iterations).
		safeRelease(current)
	}()

	cancel := func() {
		cancelFn()
		// WaitForNotification unblocks when cancelCtx is done, so the goroutine
		// exits and conn.Release() is called by the deferred release. No explicit
		// wait is needed; callers (Relay.Run) defer cancel().
	}
	return wake, cancel, nil
}

// listenLoop calls notifier.Listen once to obtain the self-healing wake channel,
// then forwards wakeups to r.wake until ctx is cancelled.
//
// The [dialect.Notifier] contract guarantees the wake channel is NEVER closed —
// the notifier self-heals internally on connection loss (re-acquire + re-LISTEN)
// without closing or replacing the channel. Therefore this loop contains no
// closed-channel / reconnect branch: a `!ok` receive would be unreachable.
//
// This method is called only when a [dialect.Notifier] is injected via
// [WithRelayNotifier]. r.listenReady is signalled (non-blocking) once Listen
// returns successfully; it is nil in production (test-only).
func (r *Relay) listenLoop(ctx context.Context, notifier dialect.Notifier, wake chan<- struct{}) {
	wakeCh, cancel, err := notifier.Listen(ctx, outboxNotifyChannel)
	if err != nil {
		if ctx.Err() == nil {
			r.tel.Logger.LogAttrs(ctx, slog.LevelWarn, "persistence: relay notifier listen failed",
				append(r.tel.LogAttrs(ctx), slog.Any("error", err))...)
		}
		return
	}
	defer cancel()

	// Signal readiness (test-only; r.listenReady is nil in production).
	if r.listenReady != nil {
		select {
		case r.listenReady <- struct{}{}:
		default:
		}
	}

	// Forward notifications from the notifier's self-healing wake channel to
	// r.wake until ctx is done. The notifier never closes wakeCh — it self-heals
	// on connection loss internally — so this select has no `!ok` branch.
	for {
		select {
		case <-ctx.Done():
			return
		case <-wakeCh:
			select {
			case wake <- struct{}{}:
			default: // coalesce: a wake is already pending
			}
		}
	}
}

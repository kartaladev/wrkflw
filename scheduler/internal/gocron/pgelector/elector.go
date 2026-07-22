// Package pgelector holds the Postgres-backed leader [gocron.Elector]
// (PostgresElector). It is split out of the neutral gocron scheduler package so
// that importing the scheduler does not transitively pull in pgx/pgxpool; the DB
// coupling lives only here (and the parallel myelector package for MySQL).
package pgelector

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/go-co-op/gocron/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonboulle/clockwork"
)

// defaultElectorKey is the well-known leader-lock key. All replicas of one engine
// contend for this single key, so exactly one wins leadership. Override it via
// WithElectorKey when several independent engines share one database.
const defaultElectorKey = "workflow-scheduling: timer-leader"

// defaultHeartbeatInterval is how often a leader re-validates that its dedicated
// connection (and thus its advisory lock) is still alive. It bounds the residual
// split-brain window to at most one interval (ADR-0061). Five seconds keeps the
// re-validation cheap while closing the window promptly.
const defaultHeartbeatInterval = 5 * time.Second

// ErrNotLeader is returned by PostgresElector.IsLeader when another instance
// currently holds leadership. gocron treats any IsLeader error as "do not run
// jobs on this instance", which is exactly the single-leader behaviour we want.
var ErrNotLeader = errors.New("workflow-scheduling: not the timer leader")

// PostgresElector is a [gocron.Elector] backed by a single Postgres session-level
// advisory lock. Plugged into the scheduler via WithElector
// (→ gocron.WithDistributedElector), it gives single-leader timer firing across
// replicas: exactly one replica wins pg_try_advisory_lock(<leader-key>) and runs
// ALL timer fires; the others' IsLeader returns ErrNotLeader so gocron skips every
// job on them.
//
// Leadership is held on one dedicated pooled connection for the elector's lifetime
// (like AdvisoryLockOwnership, ADR-0020). When the leader process dies the
// connection drops and Postgres auto-releases the lock; a follower then wins it on
// its next IsLeader attempt — natural failover with no lease-renewal loop.
//
// IsLeader is sticky: once leadership is held, it returns nil from an in-memory
// flag without a DB round-trip, satisfying gocron's per-job-run hot path. To close
// the split-brain window left by that stickiness (ADR-0059), a bounded background
// heartbeat (ADR-0061) periodically re-validates the dedicated connection; if it
// has been severed server-side (the advisory lock auto-released), the heartbeat
// flips isLeader back to false so the next IsLeader re-attempts acquisition. The
// residual two-leader window is therefore at most one heartbeat interval, and the
// engine's version-CAS (ADR-0027) remains the exactly-once backstop.
//
// The Elector is the single-leader ALTERNATIVE to a load-balanced advisory-lock
// locker (ADR-0050): use one or the other, never both (see ADR-0059).
type PostgresElector struct {
	conn      *pgxpool.Conn
	key       string
	clk       clockwork.Clock
	heartbeat time.Duration

	mu       sync.Mutex
	isLeader bool
	closed   bool

	// onAcquire, if set, is invoked each time this elector transitions to leader
	// (Option A, ADR-0072). Wiring it to ProcessDriver.RehydrateTimers re-arms persisted
	// timers on a new leader after failover. It runs in a wg-tracked goroutine on
	// bgCtx so Close waits for it and cancellation propagates; acquiring coalesces
	// overlapping invocations from rapid step-down/re-acquire cycles.
	onAcquire func(context.Context)
	acquiring bool

	// heartbeat goroutine lifecycle. started guards a single lazy start on first
	// leadership acquisition; bgCancel/done stop it and wg waits for its exit so
	// Close leaves no goroutine behind (goleak-enforced).
	started  bool
	wg       sync.WaitGroup
	done     chan struct{}
	bgCtx    context.Context
	bgCancel context.CancelFunc
}

// Compile-time assertion: PostgresElector must satisfy gocron.Elector.
var _ gocron.Elector = (*PostgresElector)(nil)

// ElectorOption configures a [PostgresElector].
type ElectorOption func(*PostgresElector)

// WithElectorKey overrides the leader-lock key (default: a fixed well-known
// constant). Give each independent engine sharing one database a distinct key so
// their leader elections do not contend. An empty value is ignored.
func WithElectorKey(key string) ElectorOption {
	return func(e *PostgresElector) {
		if key != "" {
			e.key = key
		}
	}
}

// WithElectorClock sets the clock that drives the leadership heartbeat ticker
// (default: a real clock). Pass the same [clockwork.Clock] used to build the
// scheduler so a fake clock advances both timer firing and heartbeat ticks in
// tests. A nil value is ignored.
func WithElectorClock(clk clockwork.Clock) ElectorOption {
	return func(e *PostgresElector) {
		if clk != nil {
			e.clk = clk
		}
	}
}

// WithHeartbeatInterval overrides how often a leader re-validates its dedicated
// connection (default: [defaultHeartbeatInterval]). It bounds the residual
// split-brain window to at most one interval (ADR-0061). A non-positive value is
// ignored.
func WithHeartbeatInterval(d time.Duration) ElectorOption {
	return func(e *PostgresElector) {
		if d > 0 {
			e.heartbeat = d
		}
	}
}

// WithOnLeadershipAcquired registers a callback invoked each time this elector
// wins leadership (including re-acquisition after a heartbeat step-down). It runs
// asynchronously — never blocking gocron's IsLeader hot path — on a background
// context that is cancelled when Close is called; Close waits for it to return.
// Overlapping invocations from rapid step-down/re-acquire cycles are coalesced.
// Wire it to ProcessDriver.RehydrateTimers to re-arm persisted timers on a new leader
// after failover (Option A, ADR-0072). A nil value is ignored.
func WithOnLeadershipAcquired(fn func(context.Context)) ElectorOption {
	return func(e *PostgresElector) {
		if fn != nil {
			e.onAcquire = fn
		}
	}
}

// NewPostgresElector acquires a dedicated session connection from pool and returns
// an Elector that contends for a single leader advisory lock on it.
//
// Call [PostgresElector.Close] to release leadership, stop the heartbeat, and
// return the dedicated connection to the pool.
func NewPostgresElector(ctx context.Context, pool *pgxpool.Pool, opts ...ElectorOption) (*PostgresElector, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("workflow-scheduling: elector: acquire session conn: %w", err)
	}
	bgCtx, bgCancel := context.WithCancel(context.Background())
	e := &PostgresElector{
		conn:      conn,
		key:       defaultElectorKey,
		clk:       clockwork.NewRealClock(),
		heartbeat: defaultHeartbeatInterval,
		done:      make(chan struct{}),
		bgCtx:     bgCtx,
		bgCancel:  bgCancel,
	}
	for _, o := range opts {
		o(e)
	}
	return e, nil
}

// BackendPID returns the Postgres backend PID of the elector's dedicated
// connection. It lets operators correlate the leader's session in pg_stat_activity
// and lets tests sever the connection out-of-band (pg_terminate_backend) to
// exercise the heartbeat step-down path (ADR-0061). It returns 0 after Close.
func (e *PostgresElector) BackendPID() uint32 {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return 0
	}
	return e.conn.Conn().PgConn().PID()
}

// IsLeader returns nil if this instance should run jobs (it is the leader) and
// ErrNotLeader otherwise. It is sticky: an already-held leadership returns nil
// without a DB round-trip. Otherwise it attempts pg_try_advisory_lock on the
// dedicated connection; on success it becomes leader (starting the heartbeat on
// first acquisition), on refusal it returns ErrNotLeader.
func (e *PostgresElector) IsLeader(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return ErrNotLeader
	}

	if e.isLeader {
		// Sticky fast-path: still leader, no DB round-trip. The heartbeat is what
		// catches a silently-lost lock and flips this flag back.
		return nil
	}

	var ok bool
	if err := e.conn.QueryRow(ctx,
		`SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, e.key,
	).Scan(&ok); err != nil {
		return fmt.Errorf("workflow-scheduling: elector: try leader lock %q: %w", e.key, err)
	}
	if !ok {
		return ErrNotLeader
	}
	e.isLeader = true
	e.startHeartbeatLocked()
	e.fireOnAcquireLocked()
	return nil
}

// fireOnAcquireLocked launches the on-leadership-acquired callback (if registered)
// in a wg-tracked goroutine. The caller must hold e.mu. It coalesces overlapping
// invocations via the acquiring flag so rapid step-down/re-acquire cycles do not
// stack concurrent callbacks. The callback runs on bgCtx (cancelled by Close), and
// wg tracking lets Close wait for it so no goroutine outlives the elector.
func (e *PostgresElector) fireOnAcquireLocked() {
	if e.onAcquire == nil || e.acquiring {
		return
	}
	e.acquiring = true
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.onAcquire(e.bgCtx)
		e.mu.Lock()
		e.acquiring = false
		e.mu.Unlock()
	}()
}

// startHeartbeatLocked launches the heartbeat goroutine the first time leadership
// is won. The caller must hold e.mu. Subsequent calls are no-ops (the goroutine
// lives for the elector's whole lifetime and re-checks leadership each tick).
func (e *PostgresElector) startHeartbeatLocked() {
	if e.started {
		return
	}
	e.started = true

	e.wg.Add(1)
	go e.heartbeatLoop()
}

// heartbeatLoop periodically re-validates the dedicated connection. If the
// connection has been severed server-side (so Postgres auto-released the advisory
// lock), it steps the elector down by clearing isLeader, so the next IsLeader
// re-attempts acquisition. It exits when Close signals done / cancels bgCtx.
func (e *PostgresElector) heartbeatLoop() {
	defer e.wg.Done()

	ticker := e.clk.NewTicker(e.heartbeat)
	defer ticker.Stop()

	for {
		select {
		case <-e.done:
			return
		case <-ticker.Chan():
			e.revalidate()
		}
	}
}

// revalidate pings the dedicated connection under the mutex (the conn is also used
// by IsLeader and Close, so all access is mutex-guarded to avoid a pgx data race).
// A failed ping means the connection — and with it the advisory lock — is gone, so
// the elector steps down.
func (e *PostgresElector) revalidate() {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed || !e.isLeader {
		return
	}

	// Ping is a cheap round-trip on the dedicated conn; it fails iff the backend
	// was terminated/severed, which is exactly when the lock has been released.
	if err := e.conn.Ping(e.bgCtx); err != nil {
		// Lost the connection (and thus leadership): step down so the next IsLeader
		// re-attempts acquisition or a follower takes over.
		e.isLeader = false
	}
}

// Close releases ALL advisory locks the session holds (via pg_advisory_unlock_all,
// covering any re-entrant stack a false step-down may have built up), stops the
// heartbeat goroutine, and returns the dedicated session connection to the pool.
// Note: conn.Release() returns the connection to the pool — it does NOT drop the
// session or reset its locks — so the explicit unlock is what guarantees release.
// Close is idempotent: a second call returns nil without any action. After Close,
// IsLeader returns ErrNotLeader.
func (e *PostgresElector) Close() error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	started := e.started
	e.mu.Unlock()

	// Stop the heartbeat (if it was started) before touching the conn so it cannot
	// race a concurrent Ping against the Release below.
	e.bgCancel()
	if started {
		close(e.done)
		e.wg.Wait()
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.isLeader {
		// Release EVERY advisory lock the session holds, not just one targeted unlock.
		// A transient heartbeat ping failure can falsely step the elector down while
		// the lock is still held; the next IsLeader re-runs pg_try_advisory_lock on
		// this same conn, stacking the re-entrant counter. A single pg_advisory_unlock
		// would only decrement that counter (leaving the lock held), and conn.Release()
		// merely returns the conn to the pool — it does NOT drop the session or reset
		// its advisory locks — so the lock would linger on a pooled backend.
		// pg_advisory_unlock_all clears the whole stack regardless of re-entrant depth.
		// Best-effort: ignore the error on shutdown.
		_, _ = e.conn.Exec(context.Background(), `SELECT pg_advisory_unlock_all()`)
		e.isLeader = false
	}
	e.conn.Release()
	return nil
}

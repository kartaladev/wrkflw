package gocron

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/go-co-op/gocron/v2"
	"github.com/jonboulle/clockwork"
)

// defaultMySQLElectorKey is the well-known leader-lock key for the MySQL elector.
// All replicas of one engine contend for this single key, so exactly one wins
// leadership. Override it via WithMySQLElectorKey when several independent engines
// share one database. GET_LOCK names are capped at 64 chars; this value is well
// within that limit but consumer-supplied keys are SHA-256 hashed (see hashMySQLKey).
const defaultMySQLElectorKey = "workflow-scheduling:timer-leader-mysql"

// MySQLElector is a [gocron.Elector] backed by a single MySQL session-scoped
// advisory lock via GET_LOCK. Plugged into the scheduler it gives single-leader
// timer firing across replicas: exactly one replica wins GET_LOCK(<leader-key>, 0)
// and runs ALL timer fires; the others' IsLeader returns ErrNotLeader so gocron
// skips every job on them.
//
// Leadership is held on one dedicated *sql.Conn for the elector's lifetime.
// MySQL advisory locks are SESSION-scoped: they are automatically released when
// the connection drops, so the elector MUST use the same conn for every
// GET_LOCK / RELEASE_ALL_LOCKS call and must never return it to the pool.
//
// IsLeader is sticky: once leadership is held, it returns nil from an in-memory
// flag without a DB round-trip. A background heartbeat (ADR-0061) periodically
// pings the dedicated connection; on failure (conn severed → MySQL auto-releases
// the lock) it steps down so the next IsLeader re-attempts acquisition.
//
// The Elector is the single-leader ALTERNATIVE to the load-balanced Locker
// (ADR-0050): use one or the other, never both (see ADR-0059).
//
// Mirror of [PostgresElector] translated to MySQL; options and lifecycle are
// intentionally parallel to avoid a shared-struct refactor that could regress
// the working Postgres path.
type MySQLElector struct {
	conn      *sql.Conn
	key       string
	clk       clockwork.Clock
	heartbeat time.Duration

	mu       sync.Mutex
	isLeader bool
	closed   bool

	// onAcquire, if set, is invoked each time this elector transitions to leader
	// (Option A, ADR-0072). Runs in a wg-tracked goroutine on bgCtx so Close waits
	// for it and coalesces overlapping invocations via acquiring flag.
	onAcquire func(context.Context)
	acquiring bool

	// heartbeat goroutine lifecycle; mirrors PostgresElector.
	started  bool
	wg       sync.WaitGroup
	done     chan struct{}
	bgCtx    context.Context
	bgCancel context.CancelFunc
}

// Compile-time assertion: MySQLElector must satisfy gocron.Elector.
var _ gocron.Elector = (*MySQLElector)(nil)

// MySQLElectorOption configures a [MySQLElector]. Intentionally parallel to
// ElectorOption (PostgresElector) to avoid a shared-struct refactor that could
// regress the working Postgres elector path (per plan global constraint).
type MySQLElectorOption func(*MySQLElector)

// WithMySQLElectorKey overrides the leader-lock key (default: a fixed well-known
// constant). Give each independent engine sharing one database a distinct key so
// their leader elections do not contend. An empty value is ignored.
// Keys longer than 64 chars are SHA-256 hashed so they fit within MySQL's
// GET_LOCK name limit.
func WithMySQLElectorKey(key string) MySQLElectorOption {
	return func(e *MySQLElector) {
		if key != "" {
			e.key = hashMySQLKey(key)
		}
	}
}

// WithMySQLElectorClock sets the clock that drives the leadership heartbeat ticker
// (default: a real clock). Pass the same [clockwork.Clock] used to build the
// scheduler so a fake clock drives both engine + scheduler in tests. A nil value
// is ignored.
func WithMySQLElectorClock(clk clockwork.Clock) MySQLElectorOption {
	return func(e *MySQLElector) {
		if clk != nil {
			e.clk = clk
		}
	}
}

// WithMySQLHeartbeatInterval overrides how often a leader re-validates its
// dedicated connection (default: [defaultHeartbeatInterval]). It bounds the
// residual split-brain window to at most one interval (ADR-0061). A non-positive
// value is ignored.
func WithMySQLHeartbeatInterval(d time.Duration) MySQLElectorOption {
	return func(e *MySQLElector) {
		if d > 0 {
			e.heartbeat = d
		}
	}
}

// WithMySQLOnLeadershipAcquired registers a callback invoked each time this
// elector wins leadership (including re-acquisition after a heartbeat step-down).
// It runs asynchronously — never blocking gocron's IsLeader hot path — on a
// background context cancelled when Close is called; Close waits for it to
// return. Overlapping invocations from rapid step-down/re-acquire cycles are
// coalesced. Wire it to Runner.RehydrateTimers to re-arm persisted timers on a
// new leader after failover (Option A, ADR-0072). A nil value is ignored.
func WithMySQLOnLeadershipAcquired(fn func(context.Context)) MySQLElectorOption {
	return func(e *MySQLElector) {
		if fn != nil {
			e.onAcquire = fn
		}
	}
}

// hashMySQLKey returns a deterministic representation of key that fits within
// MySQL's 64-character GET_LOCK name limit. Keys that already fit are returned
// unchanged; longer keys are replaced by their SHA-256 hex digest (exactly 64
// chars), which is stable and collision-resistant for practical lock-key lengths.
func hashMySQLKey(key string) string {
	if len(key) <= 64 {
		return key
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(key)))
}

// NewMySQLElector acquires a dedicated *sql.Conn from db and returns a
// [MySQLElector] that contends for a single leader advisory lock via GET_LOCK.
//
// The dedicated connection is held for the elector's lifetime — MySQL advisory
// locks are SESSION-scoped and auto-released when the connection drops — so
// the same conn is reused for every DB call. Never use the pool from *sql.DB
// directly for these calls.
//
// Call [MySQLElector.Close] to release leadership, stop the heartbeat, and
// close the dedicated connection.
func NewMySQLElector(ctx context.Context, db *sql.DB, opts ...MySQLElectorOption) (*MySQLElector, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("workflow-scheduling: mysql elector: acquire session conn: %w", err)
	}
	bgCtx, bgCancel := context.WithCancel(context.Background())
	e := &MySQLElector{
		conn:      conn,
		key:       hashMySQLKey(defaultMySQLElectorKey),
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

// IsLeader returns nil if this instance should run jobs (it is the leader) and
// ErrNotLeader otherwise. It is sticky: an already-held leadership returns nil
// without a DB round-trip. Otherwise it attempts GET_LOCK on the dedicated
// connection; 1 = acquired (leader), 0 = held by another session (follower).
func (e *MySQLElector) IsLeader(ctx context.Context) error {
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

	var result sql.NullInt64
	if err := e.conn.QueryRowContext(ctx,
		`SELECT GET_LOCK(?, 0)`, e.key,
	).Scan(&result); err != nil {
		return fmt.Errorf("workflow-scheduling: mysql elector: try leader lock %q: %w", e.key, err)
	}
	// GET_LOCK returns 1 on success, 0 if another session holds it, NULL on error.
	if !result.Valid || result.Int64 != 1 {
		return ErrNotLeader
	}
	e.isLeader = true
	e.startHeartbeatLocked()
	e.fireOnAcquireLockedMySQL()
	return nil
}

// fireOnAcquireLockedMySQL launches the on-leadership-acquired callback (if
// registered) in a wg-tracked goroutine. The caller must hold e.mu. It coalesces
// overlapping invocations via the acquiring flag so rapid step-down/re-acquire
// cycles do not stack concurrent callbacks. The callback runs on bgCtx
// (cancelled by Close), and wg tracking lets Close wait for it.
func (e *MySQLElector) fireOnAcquireLockedMySQL() {
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
// is won. The caller must hold e.mu. Subsequent calls are no-ops.
func (e *MySQLElector) startHeartbeatLocked() {
	if e.started {
		return
	}
	e.started = true
	e.wg.Add(1)
	go e.mysqlHeartbeatLoop()
}

// mysqlHeartbeatLoop periodically re-validates the dedicated connection. If the
// connection has been severed server-side (MySQL auto-released the advisory lock),
// it steps the elector down by clearing isLeader so the next IsLeader re-attempts
// acquisition. Exits when Close signals done / cancels bgCtx.
func (e *MySQLElector) mysqlHeartbeatLoop() {
	defer e.wg.Done()

	ticker := e.clk.NewTicker(e.heartbeat)
	defer ticker.Stop()

	for {
		select {
		case <-e.done:
			return
		case <-ticker.Chan():
			e.mysqlRevalidate()
		}
	}
}

// mysqlRevalidate pings the dedicated connection under the mutex. A failed ping
// means the connection and the advisory lock are gone, so the elector steps down.
func (e *MySQLElector) mysqlRevalidate() {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed || !e.isLeader {
		return
	}

	if err := e.conn.PingContext(e.bgCtx); err != nil {
		// Lost the connection (and thus leadership): step down so the next IsLeader
		// re-attempts acquisition or a follower takes over.
		e.isLeader = false
	}
}

// Close releases ALL advisory locks the session holds (via RELEASE_ALL_LOCKS()),
// stops the heartbeat goroutine, and closes the dedicated connection. Close is
// idempotent: a second call returns nil without any action. After Close,
// IsLeader returns ErrNotLeader.
//
// RELEASE_ALL_LOCKS() is called unconditionally: a heartbeat step-down can
// clear isLeader while the session lock is still held, so gating the release
// on isLeader would leave a stale lock on the connection until MySQL detects
// the closed connection. RELEASE_ALL_LOCKS() is idempotent in MySQL (returns 0
// when no locks are held) so calling it when not leader is harmless.
func (e *MySQLElector) Close() error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	started := e.started
	e.mu.Unlock()

	// Stop the heartbeat (if it was started) before touching the conn so it cannot
	// race a concurrent PingContext against the Close below.
	e.bgCancel()
	if started {
		close(e.done)
		e.wg.Wait()
	}

	// RELEASE_ALL_LOCKS() unconditionally: releases every advisory lock held by
	// this session regardless of whether isLeader is set. A heartbeat step-down
	// can clear isLeader while the lock is still held on the session, so we must
	// not gate this on isLeader. Idempotent in MySQL — safe when no locks held.
	// Best-effort: ignore the error on shutdown.
	_, _ = e.conn.ExecContext(context.Background(), `SELECT RELEASE_ALL_LOCKS()`)
	_ = e.conn.Close()
	return nil
}

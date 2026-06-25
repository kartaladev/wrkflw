package gocron

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/go-co-op/gocron/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

// defaultElectorKey is the well-known leader-lock key. All replicas of one engine
// contend for this single key, so exactly one wins leadership. Override it via
// WithElectorKey when several independent engines share one database.
const defaultElectorKey = "workflow-scheduling: timer-leader"

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
// flag without a DB round-trip, satisfying gocron's per-job-run hot path.
//
// The Elector is the single-leader ALTERNATIVE to the load-balanced PostgresLocker
// (ADR-0050): use one or the other, never both (see ADR-0059).
type PostgresElector struct {
	conn *pgxpool.Conn
	key  string

	mu       sync.Mutex
	isLeader bool
	closed   bool
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

// NewPostgresElector acquires a dedicated session connection from pool and returns
// an Elector that contends for a single leader advisory lock on it.
//
// Call [PostgresElector.Close] to release leadership and return the dedicated
// connection to the pool.
func NewPostgresElector(ctx context.Context, pool *pgxpool.Pool, opts ...ElectorOption) (*PostgresElector, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("workflow-scheduling: elector: acquire session conn: %w", err)
	}
	e := &PostgresElector{
		conn: conn,
		key:  defaultElectorKey,
	}
	for _, o := range opts {
		o(e)
	}
	return e, nil
}

// IsLeader returns nil if this instance should run jobs (it is the leader) and
// ErrNotLeader otherwise. It is sticky: an already-held leadership returns nil
// without a DB round-trip. Otherwise it attempts pg_try_advisory_lock on the
// dedicated connection; on success it becomes leader, on refusal it returns
// ErrNotLeader.
func (e *PostgresElector) IsLeader(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return ErrNotLeader
	}

	if e.isLeader {
		// Sticky fast-path: still leader, no DB round-trip.
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
	return nil
}

// Close releases the leader advisory lock (if held) and returns the dedicated
// session connection to the pool. Close is idempotent: a second call returns nil
// without any action. After Close, IsLeader returns ErrNotLeader.
func (e *PostgresElector) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return nil
	}

	if e.isLeader {
		// Best-effort: ignore the unlock error on shutdown; dropping the connection
		// auto-releases the lock regardless.
		_, _ = e.conn.Exec(context.Background(),
			`SELECT pg_advisory_unlock(hashtextextended($1, 0))`, e.key)
		e.isLeader = false
	}
	e.conn.Release()
	e.closed = true
	return nil
}

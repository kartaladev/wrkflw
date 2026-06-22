package postgres

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// Compile-time assertion: AdvisoryLockOwnership must satisfy runtime.Ownership.
var _ runtime.Ownership = (*AdvisoryLockOwnership)(nil)

// ErrOwnershipClosed is returned by Acquire and Release when called after Close.
var ErrOwnershipClosed = errors.New("workflow-postgres: ownership: closed")

// AdvisoryLockOwnership implements [runtime.Ownership] for multi-process
// deployments using Postgres session-level advisory locks (ADR-0020). It holds
// one dedicated pool connection for its whole lifetime; every owned instance
// maps to a pg_advisory_lock on that session.
//
// If the process dies the connection drops and Postgres auto-releases all its
// locks (natural fencing); the version-CAS rejects any stale in-flight Commit
// from the usurped owner.
//
// Acquire is sticky: an already-held instance returns true from an in-memory
// set without a round-trip, satisfying the O(1) hot-path requirement.
type AdvisoryLockOwnership struct {
	conn *pgxpool.Conn

	mu     sync.Mutex
	held   map[string]bool
	closed bool
}

// NewAdvisoryLockOwnership acquires a dedicated session connection from pool
// and returns an Ownership backed by Postgres session advisory locks on it.
//
// Call [AdvisoryLockOwnership.Close] to release every held lock and return the
// dedicated connection to the pool.
func NewAdvisoryLockOwnership(ctx context.Context, pool *pgxpool.Pool) (*AdvisoryLockOwnership, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("workflow-postgres: ownership: acquire session conn: %w", err)
	}
	return &AdvisoryLockOwnership{
		conn: conn,
		held: make(map[string]bool),
	}, nil
}

// Acquire takes a session advisory lock for instanceID. If the instance is
// already held by this process (sticky), it returns true immediately without a
// DB round-trip. owned=false means another session holds the lock.
func (o *AdvisoryLockOwnership) Acquire(ctx context.Context, instanceID string) (bool, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.closed {
		return false, ErrOwnershipClosed
	}

	if o.held[instanceID] {
		// Sticky fast-path: no DB round-trip.
		return true, nil
	}

	var ok bool
	if err := o.conn.QueryRow(ctx,
		`SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, instanceID,
	).Scan(&ok); err != nil {
		return false, fmt.Errorf("workflow-postgres: ownership: try lock %q: %w", instanceID, err)
	}

	// Only add to the held set when the lock was actually granted.
	if ok {
		o.held[instanceID] = true
	}
	return ok, nil
}

// Release drops the session advisory lock for instanceID. If the instance is
// not held by this process, it is a no-op (returns nil).
func (o *AdvisoryLockOwnership) Release(ctx context.Context, instanceID string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.closed {
		return ErrOwnershipClosed
	}

	if !o.held[instanceID] {
		return nil
	}

	if _, err := o.conn.Exec(ctx,
		`SELECT pg_advisory_unlock(hashtextextended($1, 0))`, instanceID,
	); err != nil {
		return fmt.Errorf("workflow-postgres: ownership: unlock %q: %w", instanceID, err)
	}
	delete(o.held, instanceID)
	return nil
}

// Close releases every held advisory lock and returns the dedicated session
// connection to the pool. Close is idempotent: a second call returns nil without
// any action. After Close, Acquire and Release return [ErrOwnershipClosed];
// create a new AdvisoryLockOwnership if continued ownership is needed.
func (o *AdvisoryLockOwnership) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.closed {
		return nil
	}

	for id := range o.held {
		// Best-effort: ignore unlock errors on shutdown.
		_, _ = o.conn.Exec(context.Background(),
			`SELECT pg_advisory_unlock(hashtextextended($1, 0))`, id)
		delete(o.held, id)
	}
	o.conn.Release()
	o.closed = true
	return nil
}

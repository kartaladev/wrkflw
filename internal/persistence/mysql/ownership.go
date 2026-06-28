package mysql

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// Compile-time assertion: AdvisoryLockOwnership must satisfy runtime.Ownership.
var _ runtime.Ownership = (*AdvisoryLockOwnership)(nil)

// ErrOwnershipClosed is returned by Acquire and Release when called after Close.
var ErrOwnershipClosed = errors.New("workflow-persistence-mysql: ownership: closed")

// hashKey converts any instanceID into a stable key of exactly 64 hex characters
// (SHA-256), satisfying MySQL 8.0's GET_LOCK 64-character name limit.
func hashKey(instanceID string) string {
	sum := sha256.Sum256([]byte(instanceID))
	return fmt.Sprintf("%x", sum[:]) // 32 bytes × 2 hex chars = 64 chars
}

// AdvisoryLockOwnership implements runtime.Ownership for multi-process
// deployments using MySQL GET_LOCK/RELEASE_LOCK (advisory named locks). It
// holds one dedicated *sql.Conn for its whole lifetime; every owned instance
// maps to a GET_LOCK call on that connection.
//
// If the process dies the connection drops and MySQL auto-releases all its
// named locks (natural fencing).
//
// Acquire is sticky: an already-held instance returns true from an in-memory
// set without a round-trip, satisfying the O(1) hot-path requirement.
type AdvisoryLockOwnership struct {
	conn *sql.Conn

	mu     sync.Mutex
	held   map[string]bool
	closed bool
}

// NewAdvisoryLockOwnership acquires a dedicated session connection from db
// and returns an Ownership backed by MySQL advisory locks on it.
//
// Call [AdvisoryLockOwnership.Close] to release every held lock and close the
// dedicated connection.
func NewAdvisoryLockOwnership(ctx context.Context, db *sql.DB) (*AdvisoryLockOwnership, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("workflow-persistence-mysql: ownership: acquire session conn: %w", err)
	}
	return &AdvisoryLockOwnership{
		conn: conn,
		held: make(map[string]bool),
	}, nil
}

// Acquire takes a GET_LOCK advisory lock for instanceID. If the instance is
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

	key := hashKey(instanceID)

	// GET_LOCK(name, timeout=0): 0 means non-blocking. Returns 1 if acquired,
	// 0 if the lock is held by another connection, NULL on error.
	var result sql.NullInt64
	if err := o.conn.QueryRowContext(ctx, `SELECT GET_LOCK(?, 0)`, key).Scan(&result); err != nil {
		return false, fmt.Errorf("workflow-persistence-mysql: ownership: try lock %q: %w", instanceID, err)
	}

	if !result.Valid || result.Int64 != 1 {
		// Lock not acquired (held by another session or null on error).
		return false, nil
	}

	o.held[instanceID] = true
	return true, nil
}

// Release drops the GET_LOCK advisory lock for instanceID. If the instance is
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

	key := hashKey(instanceID)
	if _, err := o.conn.ExecContext(ctx, `SELECT RELEASE_LOCK(?)`, key); err != nil {
		return fmt.Errorf("workflow-persistence-mysql: ownership: unlock %q: %w", instanceID, err)
	}
	delete(o.held, instanceID)
	return nil
}

// Close releases every held advisory lock and closes the dedicated session
// connection. Close is idempotent: a second call returns nil without any
// action. After Close, Acquire and Release return [ErrOwnershipClosed];
// create a new AdvisoryLockOwnership if continued ownership is needed.
func (o *AdvisoryLockOwnership) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.closed {
		return nil
	}

	// Release all locks at once; then close the connection which also auto-releases.
	// Best-effort: ignore errors on shutdown.
	_, _ = o.conn.ExecContext(context.Background(), `SELECT RELEASE_ALL_LOCKS()`)
	o.held = make(map[string]bool)

	_ = o.conn.Close()
	o.closed = true
	return nil
}

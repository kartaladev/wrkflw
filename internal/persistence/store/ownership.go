package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// Compile-time assertion: AdvisoryLockOwnership must satisfy runtime.Ownership.
var _ runtime.Ownership = (*AdvisoryLockOwnership)(nil)

// ErrOwnershipClosed is returned by Acquire and Release when called after Close.
var ErrOwnershipClosed = errors.New("workflow-store: ownership: closed")

// AdvisoryLockOwnership implements [runtime.Ownership] for multi-process
// deployments using database-level advisory locks. The concrete locking
// mechanism is supplied via an injected [dialect.Locker]:
//
//   - Postgres: session-scoped advisory locks via pg_try_advisory_lock /
//     pg_advisory_unlock, held on a dedicated connection for the lifetime
//     of the ownership value ([NewPostgresOwnership]).
//   - MySQL: connection-scoped GET_LOCK / RELEASE_LOCK on a dedicated *sql.Conn
//     ([NewMySQLOwnership]).
//   - SQLite: no advisory locking — [NewSQLiteLocker] returns
//     [dialect.ErrUnsupported] from both methods.
//
// Acquire is sticky: an already-held instance returns true from an in-memory
// set without a DB round-trip, satisfying the O(1) hot-path requirement.
//
// If the process dies the underlying connection drops and the DB auto-releases
// all held locks (natural fencing). The version-CAS rejects any stale in-flight
// Commit from the previously-owning process.
type AdvisoryLockOwnership struct {
	locker dialect.Locker
	close  func() error // called once by Close to release the locker's resources

	mu     sync.Mutex
	held   map[string]bool
	closed bool
}

// NewPostgresOwnership acquires a dedicated session connection from pool and
// returns an [AdvisoryLockOwnership] backed by Postgres session-level advisory
// locks on that connection.
//
// Advisory locks are SESSION-scoped: TryLock acquires the lock on a dedicated
// connection that is held for the lifetime of the ownership value; Unlock
// releases the lock on the same connection. The connection is returned to the
// pool when [AdvisoryLockOwnership.Close] is called.
//
// Call [AdvisoryLockOwnership.Close] to release every held lock and return the
// dedicated connection to the pool.
func NewPostgresOwnership(ctx context.Context, pool *pgxpool.Pool) (*AdvisoryLockOwnership, error) {
	locker, err := newPostgresLocker(ctx, pool)
	if err != nil {
		return nil, err
	}
	return &AdvisoryLockOwnership{
		locker: locker,
		close:  locker.closeConn,
		held:   make(map[string]bool),
	}, nil
}

// NewMySQLOwnership acquires a dedicated connection from db and returns an
// [AdvisoryLockOwnership] backed by MySQL GET_LOCK / RELEASE_LOCK on that
// connection.
//
// The dedicated *sql.Conn is held for the lifetime of the ownership value;
// [AdvisoryLockOwnership.Close] releases all locks and closes it.
func NewMySQLOwnership(ctx context.Context, db *sql.DB) (*AdvisoryLockOwnership, error) {
	locker, err := newMySQLLocker(ctx, db)
	if err != nil {
		return nil, err
	}
	return &AdvisoryLockOwnership{
		locker: locker,
		close:  locker.closeConn,
		held:   make(map[string]bool),
	}, nil
}

// NewSQLiteOwnership returns an [AdvisoryLockOwnership] for SQLite deployments.
// SQLite provides no advisory locking mechanism: [AdvisoryLockOwnership.Acquire]
// returns [dialect.ErrUnsupported] (fail-loud); [AdvisoryLockOwnership.Release]
// is a no-op for an un-held lock.
//
// Use this constructor to satisfy the ownership parameter required by
// [runtime.NewCachingStore] in single-node SQLite deployments. Ownership-dependent
// flows must guard against [dialect.ErrUnsupported] and skip the ownership path
// when running on SQLite.
//
// No connection or context is needed: the SQLite locker is stateless.
func NewSQLiteOwnership() (*AdvisoryLockOwnership, error) {
	return &AdvisoryLockOwnership{
		locker: NewSQLiteLocker(),
		close:  nil, // no connection to release
		held:   make(map[string]bool),
	}, nil
}

// Acquire takes an advisory lock for instanceID. If the instance is already
// held by this process (sticky), it returns true immediately without a DB
// round-trip. owned=false means another session holds the lock.
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

	ok, err := o.locker.TryLock(ctx, instanceID)
	if err != nil {
		return false, fmt.Errorf("workflow-store: ownership: acquire %q: %w", instanceID, err)
	}

	if ok {
		o.held[instanceID] = true
	}
	return ok, nil
}

// Release drops the advisory lock for instanceID. If the instance is not held
// by this process, it is a no-op (returns nil).
func (o *AdvisoryLockOwnership) Release(ctx context.Context, instanceID string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.closed {
		return ErrOwnershipClosed
	}

	if !o.held[instanceID] {
		return nil
	}

	if err := o.locker.Unlock(ctx, instanceID); err != nil {
		return fmt.Errorf("workflow-store: ownership: release %q: %w", instanceID, err)
	}
	delete(o.held, instanceID)
	return nil
}

// Close releases every held advisory lock and frees the underlying session
// connection. Close is idempotent: a second call returns nil without any
// action. After Close, Acquire and Release return [ErrOwnershipClosed]; create
// a new AdvisoryLockOwnership if continued ownership is needed.
func (o *AdvisoryLockOwnership) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.closed {
		return nil
	}

	// Best-effort: unlock all held instances before freeing the connection.
	for id := range o.held {
		_ = o.locker.Unlock(context.Background(), id) //nolint:errcheck
		delete(o.held, id)
	}

	if o.close != nil {
		_ = o.close() //nolint:errcheck
	}
	o.closed = true
	return nil
}

// ── postgresLocker ────────────────────────────────────────────────────────────

// postgresLocker implements [dialect.Locker] using Postgres session-level
// advisory locks. It holds a DEDICATED connection from the pool for its entire
// lifetime; TryLock and Unlock operate on that single session so the advisory
// lock's session-scoped lifetime is correctly maintained.
//
// pg_try_advisory_lock(int8) is non-blocking: it returns true if the lock was
// acquired, false if another session holds it.
// pg_advisory_unlock(int8) releases the lock on the current session.
//
// The int8 key is derived via hashtextextended(key, 0), which is the same
// function Postgres uses internally, giving a stable int64 from any string.
type postgresLocker struct {
	conn *pgxpool.Conn
}

func newPostgresLocker(ctx context.Context, pool *pgxpool.Pool) (*postgresLocker, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("workflow-store: ownership: acquire session conn: %w", err)
	}
	return &postgresLocker{conn: conn}, nil
}

// TryLock acquires a Postgres session advisory lock for key without blocking.
// Returns (true, nil) on success, (false, nil) if another session holds it.
func (l *postgresLocker) TryLock(ctx context.Context, key string) (bool, error) {
	var ok bool
	err := l.conn.QueryRow(ctx,
		`SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, key,
	).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("workflow-store: postgres locker: try lock %q: %w", key, err)
	}
	return ok, nil
}

// Unlock releases the Postgres session advisory lock for key.
func (l *postgresLocker) Unlock(ctx context.Context, key string) error {
	if _, err := l.conn.Exec(ctx,
		`SELECT pg_advisory_unlock(hashtextextended($1, 0))`, key,
	); err != nil {
		return fmt.Errorf("workflow-store: postgres locker: unlock %q: %w", key, err)
	}
	return nil
}

// closeConn releases the dedicated connection back to the pool.
func (l *postgresLocker) closeConn() error {
	l.conn.Release()
	return nil
}

// ── mysqlLocker ───────────────────────────────────────────────────────────────

// mysqlLocker implements [dialect.Locker] using MySQL named advisory locks
// (GET_LOCK / RELEASE_LOCK). It holds a DEDICATED *sql.Conn for its entire
// lifetime because MySQL advisory locks are connection-scoped: they are
// automatically released when the connection closes.
//
// MySQL's GET_LOCK name argument is limited to 64 characters. mysqlHashKey
// converts any instanceID to a stable 64-char hex string via SHA-256.
type mysqlLocker struct {
	conn *sql.Conn
}

func newMySQLLocker(ctx context.Context, db *sql.DB) (*mysqlLocker, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("workflow-store: ownership: acquire session conn: %w", err)
	}
	return &mysqlLocker{conn: conn}, nil
}

// mysqlHashKey converts instanceID to a stable key of exactly 64 hex characters
// (SHA-256), satisfying MySQL 8.0's GET_LOCK 64-character name limit.
func mysqlHashKey(instanceID string) string {
	sum := sha256.Sum256([]byte(instanceID))
	return fmt.Sprintf("%x", sum[:]) // 32 bytes × 2 hex chars = 64 chars
}

// TryLock calls GET_LOCK(key, 0) — timeout=0 means non-blocking.
// Returns (true, nil) if acquired, (false, nil) if held by another session.
func (l *mysqlLocker) TryLock(ctx context.Context, key string) (bool, error) {
	name := mysqlHashKey(key)
	// GET_LOCK returns 1=acquired, 0=another session holds it, NULL on error.
	var result sql.NullInt64
	if err := l.conn.QueryRowContext(ctx, `SELECT GET_LOCK(?, 0)`, name).Scan(&result); err != nil {
		return false, fmt.Errorf("workflow-store: mysql locker: try lock %q: %w", key, err)
	}
	if !result.Valid || result.Int64 != 1 {
		return false, nil
	}
	return true, nil
}

// Unlock calls RELEASE_LOCK(key).
func (l *mysqlLocker) Unlock(ctx context.Context, key string) error {
	name := mysqlHashKey(key)
	var result sql.NullInt64
	if err := l.conn.QueryRowContext(ctx, `SELECT RELEASE_LOCK(?)`, name).Scan(&result); err != nil {
		return fmt.Errorf("workflow-store: mysql locker: unlock %q: %w", key, err)
	}
	_ = result // non-1 result is non-fatal (held set guards the call site)
	return nil
}

// closeConn releases all MySQL advisory locks and closes the dedicated connection.
func (l *mysqlLocker) closeConn() error {
	// RELEASE_ALL_LOCKS() releases all named locks held by the current session.
	_, _ = l.conn.ExecContext(context.Background(), `SELECT RELEASE_ALL_LOCKS()`)
	return l.conn.Close()
}

// ── sqliteLocker ──────────────────────────────────────────────────────────────

// sqliteLocker implements [dialect.Locker] for SQLite. SQLite provides no
// advisory locking mechanism — both TryLock and Unlock return
// [dialect.ErrUnsupported] (fail-loud). Callers that require distributed locking
// must guard against ErrUnsupported and skip the ownership-dependent path when
// running on SQLite.
type sqliteLocker struct{}

// NewSQLiteLocker returns a [dialect.Locker] for SQLite that returns
// [dialect.ErrUnsupported] from both TryLock and Unlock.
func NewSQLiteLocker() dialect.Locker {
	return sqliteLocker{}
}

// TryLock always returns (false, [dialect.ErrUnsupported]).
func (sqliteLocker) TryLock(_ context.Context, _ string) (bool, error) {
	return false, dialect.ErrUnsupported
}

// Unlock always returns [dialect.ErrUnsupported].
func (sqliteLocker) Unlock(_ context.Context, _ string) error {
	return dialect.ErrUnsupported
}

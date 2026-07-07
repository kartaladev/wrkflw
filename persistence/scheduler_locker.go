package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/store"
	"github.com/zakyalvan/krtlwrkflw/scheduling"
)

// ErrSchedulerLockNotObtained is returned by the bridge's [scheduling.Locker.Lock]
// when the underlying advisory lock is already held by another session. The
// scheduler treats any Lock error as "do not run this timer on this replica",
// which is exactly the per-timer exclusion the bridge provides.
var ErrSchedulerLockNotObtained = errors.New("workflow-persistence: scheduler advisory lock not obtained")

// NewSchedulerLocker bridges a single-session database advisory lock
// ([dialect.Locker]) to the neutral [scheduling.Locker] used for multi-replica
// timer exclusion, reusing the very same advisory-lock SQL the store uses for
// instance ownership — no lock code is duplicated in the scheduling package
// (ADR-0102). Pass the result to scheduling.WithLocker.
//
// Concurrency note: a [dialect.Locker] holds ONE session connection, so the
// returned locker serializes concurrent timer fires onto that single session. Use
// it when a single serialized session is acceptable, or bring your own
// concurrency-safe [dialect.Locker]. For the common case of many timers firing
// concurrently prefer [NewPostgresSchedulerLocker] / [NewMySQLSchedulerLocker],
// which acquire a fresh session per lock so distinct timers never contend on one
// connection.
//
// Lock(ctx, key) calls dl.TryLock: on success it returns a [scheduling.Lock] whose
// Unlock calls dl.Unlock(ctx, key); when the key is already held it returns
// [ErrSchedulerLockNotObtained]; any TryLock error is propagated.
func NewSchedulerLocker(dl dialect.Locker) scheduling.Locker {
	return &schedulerLocker{lock: dl.TryLock, unlock: dl.Unlock}
}

// NewPostgresSchedulerLocker returns a [scheduling.Locker] backed by Postgres
// session-level advisory locks over pool. Pass the result to
// scheduling.WithLocker for multi-replica timer exclusion.
//
// Each Lock acquires a FRESH pooled connection and holds the advisory lock on it
// for the fire's duration; Unlock releases the lock and returns the connection to
// the pool. This mirrors the store's advisory-lock SQL exactly (no duplication)
// while letting distinct timers fire concurrently without contending on a single
// session. Nothing to close: connections are borrowed from and returned to pool.
func NewPostgresSchedulerLocker(pool *pgxpool.Pool) scheduling.Locker {
	return &poolSchedulerLocker{
		acquire: func(ctx context.Context) (dialect.Locker, func() error, error) {
			return store.NewPostgresLocker(ctx, pool)
		},
	}
}

// NewMySQLSchedulerLocker returns a [scheduling.Locker] backed by MySQL GET_LOCK /
// RELEASE_LOCK over db. Pass the result to scheduling.WithLocker for multi-replica
// timer exclusion.
//
// Each Lock acquires a FRESH session connection and holds the named lock on it for
// the fire's duration; Unlock releases the lock and closes the connection. This
// mirrors the store's advisory-lock SQL exactly (no duplication) while letting
// distinct timers fire concurrently without contending on a single session.
func NewMySQLSchedulerLocker(db *sql.DB) scheduling.Locker {
	return &poolSchedulerLocker{
		acquire: func(ctx context.Context) (dialect.Locker, func() error, error) {
			return store.NewMySQLLocker(ctx, db)
		},
	}
}

// schedulerLocker adapts a single-session dialect.Locker to scheduling.Locker.
type schedulerLocker struct {
	lock   func(ctx context.Context, key string) (bool, error)
	unlock func(ctx context.Context, key string) error
}

func (l *schedulerLocker) Lock(ctx context.Context, key string) (scheduling.Lock, error) {
	ok, err := l.lock(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("workflow-persistence: scheduler locker try %q: %w", key, err)
	}
	if !ok {
		return nil, ErrSchedulerLockNotObtained
	}
	return &schedulerLock{unlock: l.unlock, key: key}, nil
}

// schedulerLock is the held lock returned by schedulerLocker.Lock.
type schedulerLock struct {
	unlock func(ctx context.Context, key string) error
	key    string
}

func (l *schedulerLock) Unlock(ctx context.Context) error {
	if err := l.unlock(ctx, l.key); err != nil {
		return fmt.Errorf("workflow-persistence: scheduler locker unlock %q: %w", l.key, err)
	}
	return nil
}

// poolSchedulerLocker acquires a fresh single-session dialect.Locker per Lock call
// so distinct timers firing concurrently never share one database session.
type poolSchedulerLocker struct {
	acquire func(ctx context.Context) (dialect.Locker, func() error, error)
}

func (l *poolSchedulerLocker) Lock(ctx context.Context, key string) (scheduling.Lock, error) {
	dl, closeConn, err := l.acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("workflow-persistence: scheduler locker acquire session: %w", err)
	}
	ok, err := dl.TryLock(ctx, key)
	if err != nil {
		_ = closeConn()
		return nil, fmt.Errorf("workflow-persistence: scheduler locker try %q: %w", key, err)
	}
	if !ok {
		_ = closeConn()
		return nil, ErrSchedulerLockNotObtained
	}
	return &poolSchedulerLock{dl: dl, closeConn: closeConn, key: key}, nil
}

// poolSchedulerLock releases the advisory lock and returns its dedicated session
// connection when Unlock is called.
type poolSchedulerLock struct {
	dl        dialect.Locker
	closeConn func() error
	key       string
}

func (l *poolSchedulerLock) Unlock(ctx context.Context) error {
	unlockErr := l.dl.Unlock(ctx, l.key)
	closeErr := l.closeConn()
	if unlockErr != nil {
		return fmt.Errorf("workflow-persistence: scheduler locker unlock %q: %w", l.key, unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("workflow-persistence: scheduler locker release session %q: %w", l.key, closeErr)
	}
	return nil
}

package gocron

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-co-op/gocron/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrLockNotObtained is returned by PostgresLocker.Lock when another holder
// already owns the key. gocron treats any Lock error as "do not run this job on
// this instance", which is exactly the per-timer exclusion we want.
var ErrLockNotObtained = errors.New("workflow-scheduling: advisory lock not obtained")

// PostgresLocker is a [gocron.Locker] backed by Postgres session-level advisory
// locks. Plugged into the scheduler via WithLocker (→ gocron.WithDistributedLocker),
// it gives per-timer mutual exclusion across replicas: when many replicas arm the
// same timer and it fires, only the replica that wins pg_try_advisory_lock(key)
// runs the fire callback; the others skip.
//
// The lock is held only for the duration of the fire (gocron releases it via
// Unlock after the job run). It therefore dedups CONCURRENT fires of a timer; the
// engine's version-CAS plus the in-tx timer-row deletion (ADR-0027) remain the
// exactly-once backstop for the rarer sequential case. The net effect is to remove
// the steady-state N×-replica redundant Deliver storm (and its CAS-conflict logs).
//
// Each Lock holds one pooled connection for the run's duration (the advisory lock
// is bound to that session); Unlock releases the lock and returns the connection.
type PostgresLocker struct {
	pool *pgxpool.Pool
}

// Compile-time assertion: PostgresLocker must satisfy gocron.Locker.
var _ gocron.Locker = (*PostgresLocker)(nil)

// NewPostgresLocker returns a PostgresLocker over pool.
func NewPostgresLocker(pool *pgxpool.Pool) *PostgresLocker {
	return &PostgresLocker{pool: pool}
}

// Lock acquires a session advisory lock for key on a dedicated pooled connection.
// It returns ErrLockNotObtained (so gocron skips the job) when another session
// holds the key, and any acquisition/query error otherwise.
func (l *PostgresLocker) Lock(ctx context.Context, key string) (gocron.Lock, error) {
	conn, err := l.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("workflow-scheduling: locker acquire conn: %w", err)
	}
	var ok bool
	if err := conn.QueryRow(ctx,
		`SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, key,
	).Scan(&ok); err != nil {
		conn.Release()
		return nil, fmt.Errorf("workflow-scheduling: locker try lock %q: %w", key, err)
	}
	if !ok {
		conn.Release()
		return nil, ErrLockNotObtained
	}
	return &advisoryLock{conn: conn, key: key}, nil
}

// advisoryLock is the held lock returned by PostgresLocker.Lock.
type advisoryLock struct {
	conn *pgxpool.Conn
	key  string
}

// Unlock releases the advisory lock and returns the connection to the pool.
func (l *advisoryLock) Unlock(ctx context.Context) error {
	defer l.conn.Release()
	if _, err := l.conn.Exec(ctx,
		`SELECT pg_advisory_unlock(hashtextextended($1, 0))`, l.key,
	); err != nil {
		return fmt.Errorf("workflow-scheduling: locker unlock %q: %w", l.key, err)
	}
	return nil
}

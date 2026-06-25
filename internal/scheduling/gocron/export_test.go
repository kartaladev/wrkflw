package gocron

import "context"

// ReacquireLockForTest re-runs pg_try_advisory_lock on the elector's OWN dedicated
// connection, stacking the session-level advisory-lock counter the way a transient
// heartbeat step-down followed by a re-acquisition does (a false step-down clears
// isLeader while the lock is still held; the next IsLeader re-locks the same conn,
// so the re-entrant counter climbs). It lets a test construct the re-entrant-stack
// scenario deterministically. Test-only.
func (e *PostgresElector) ReacquireLockForTest(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	var ok bool
	return e.conn.QueryRow(ctx,
		`SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, e.key,
	).Scan(&ok)
}

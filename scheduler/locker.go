package scheduler

import "context"

// Lock is a held distributed lock. It is released by [Lock.Unlock]. A Lock value
// is returned by [Locker.Lock] and is neutral of any database driver: every
// method is context-in, error-out, so the public scheduler API never exposes a
// vendor type (ADR-0102).
type Lock interface {
	// Unlock releases the held lock. It is called by the scheduler once the timer
	// fire it guarded has completed.
	Unlock(ctx context.Context) error
}

// Locker is a distributed advisory lock used for multi-replica timer exclusion:
// across replicas that arm the same timer, only the one that acquires the lock
// for that timer's key runs the fire callback; the others skip. The lock key is
// the timerID.
//
// Locker is neutral of any database driver — it mirrors the shape the internal
// gocron adapter needs without importing gocron or a DB driver into the public
// scheduler signatures. Obtain a database-backed implementation from the
// persistence facade (persistence.NewSchedulerLocker over the engine's advisory
// lock) and pass it via [WithLocker].
//
// It is the load-balanced ALTERNATIVE to [Elector]'s single-leader mode; the two
// are mutually exclusive (ADR-0059).
type Locker interface {
	// Lock attempts to acquire the advisory lock identified by key. It returns a
	// held [Lock] on success, or an error (so the scheduler skips the guarded fire)
	// when the key is already held elsewhere or acquisition fails.
	Lock(ctx context.Context, key string) (Lock, error)
}

// WithLocker enables multi-replica timer exclusivity backed by the supplied
// [Locker]. When set, many replicas may arm the same timer but only the replica
// that acquires the per-timer lock runs its fire callback — removing the
// steady-state N×-replica redundant delivery. The engine's version-CAS plus the
// in-tx timer-row deletion remain the exactly-once backstop.
//
// [WithLocker] and [WithElector] are mutually exclusive (load-balanced per-timer
// exclusion vs. single-leader firing); requesting both returns
// [ErrTimerLockElectorConflict]. A nil value is ignored. See ADR-0050, ADR-0102.
func WithLocker(l Locker) Option {
	return func(c *config) {
		if l != nil {
			c.locker = l
		}
	}
}

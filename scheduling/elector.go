package scheduling

import "context"

// Elector drives single-leader timer firing across replicas: exactly one replica
// is elected leader and runs ALL timer fires; on the others [Elector.IsLeader]
// returns a non-nil error so the scheduler skips every job. On leader death a
// follower is elected on its next attempt.
//
// Elector is neutral of any database driver — it mirrors the shape the internal
// gocron adapter needs (a single ctx-in/error-out method) without importing
// gocron or a DB driver into the public scheduling signatures. Obtain a
// database-backed implementation from scheduling/backend/{postgres,mysql} and
// pass it via [WithElector].
//
// It is the single-leader ALTERNATIVE to [Locker]'s load-balanced mode; the two
// are mutually exclusive (ADR-0059).
type Elector interface {
	// IsLeader returns nil if this replica should run jobs (it is the leader) and
	// a non-nil error otherwise (so the scheduler skips jobs on this replica).
	IsLeader(ctx context.Context) error
}

// WithElector enables multi-replica timer firing in single-leader mode backed by
// the supplied [Elector]. Exactly one replica is elected leader and runs ALL
// timer fires; the others skip. The engine's version-CAS plus the in-tx
// timer-row deletion remain the exactly-once backstop.
//
// Ownership: the elector is constructed by the consumer via a backend
// constructor (scheduling/backend/{postgres,mysql}.NewElector) and its lifecycle
// — including closing its dedicated database connection — belongs to the
// consumer. If the supplied elector also implements [io.Closer], [Scheduler.Close]
// closes it as a convenience; a consumer that shares one elector across schedulers
// should not rely on that.
//
// [WithElector] and [WithLocker] are mutually exclusive (single-leader firing vs.
// load-balanced per-timer exclusion); requesting both returns
// [ErrTimerLockElectorConflict]. A nil value is ignored. See ADR-0059, ADR-0102.
func WithElector(e Elector) Option {
	return func(c *config) {
		if e != nil {
			c.elector = e
		}
	}
}

package persistence

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kartaladev/wrkflw/internal/persistence/dialect"
	"github.com/kartaladev/wrkflw/internal/persistence/store"
)

// Pruner is the stable public interface for data-lifecycle retention
// pruning. Each method deletes only safely-eligible rows older than the
// caller-supplied cutoff and returns the number of rows deleted, so a consumer
// can drive retention from their own scheduled job (they own the cron). Pruning
// is the consumer's responsibility — the library ships no daemon.
type Pruner interface {
	// PruneOutbox deletes published outbox rows (status='published') whose
	// published_at is strictly before cutoff. Pending and dead-lettered rows are
	// never touched. Returns the number of rows deleted.
	PruneOutbox(ctx context.Context, cutoff time.Time) (int64, error)

	// PruneCallLinks deletes call-link rows already delivered to their parent
	// (status='notified') whose notified_at is strictly before cutoff. Running
	// children and terminal-but-undelivered children survive — a parent may still
	// need to be resumed from them. Returns the number of rows deleted.
	PruneCallLinks(ctx context.Context, cutoff time.Time) (int64, error)

	// PruneChainLinks deletes process-chaining lineage rows whose created_at is
	// strictly before cutoff. Trade-off: this loses ancestry and the exactly-once
	// chaining backstop for the affected hops — choose a cutoff far beyond any
	// terminal-event redelivery window. Returns the number of rows deleted.
	PruneChainLinks(ctx context.Context, cutoff time.Time) (int64, error)

	// PruneProcessedMessages deletes idempotent-consumer dedup records whose
	// processed_at is strictly before cutoff (equivalent to Deduper.Prune). Supply
	// a cutoff well past the relay max-delivery × backoff window so in-flight
	// messages are never evicted. Returns the number of rows deleted.
	PruneProcessedMessages(ctx context.Context, cutoff time.Time) (int64, error)

	// PruneTimers deletes timer rows whose next_run is strictly before cutoff.
	// Fired/expired timers accumulate in wrkflw_timers; a retention job uses
	// this to drop them. Choose a cutoff safely past any window in which a timer
	// could still fire or be rescheduled. Applies to all dialects (Postgres,
	// MySQL, SQLite). Returns the number of rows deleted.
	//
	// Two populations are excluded at EVERY cutoff, so a caller must not read
	// this as "deletes every expired row":
	//
	//   - Rows whose trigger is a recurring kind. next_run is written once when
	//     such a timer is armed and is not updated per recurrence, so an expired
	//     next_run there does not mean the timer is done firing.
	//     Reclaiming the never-due subset of these needs
	//     [NeverDueTimerReclaimer].
	//   - Compensation-retry rows (engine.TimerCompensationRetry).
	//     The backoff is armed as a one-shot, so the recurring-kind clause does
	//     not cover it, and the row is the only thing that will resume its
	//     compensation walk — deleting it strands the walk.
	//
	// A row of either kind therefore survives this call no matter how old it is.
	PruneTimers(ctx context.Context, cutoff time.Time) (int64, error)
}

// Compile-time check: the neutral store concrete type satisfies the public interface.
var _ Pruner = (*store.Pruner)(nil)

// NeverDueTimerReclaimer is an optional capability of a [Pruner]: it reclaims
// orphan timer rows — rows whose next_run is sub-epoch (strictly before
// 1970-01-01T00:00:00Z) while their trigger is a recurring kind.
//
// Such a row is never-due by construction, and no retention cutoff reaches it:
// [Pruner.PruneTimers] deletes only non-recurring kinds, so its reachable set
// and the orphan set are disjoint. The engine no longer writes a zero next_run
// for a recurring trigger it could not schedule, but nothing reclaimed the rows
// already stored — they sit at the head of the armed-timer keyset index
// forever, pinning the reported next-fire time at 0001-01-01.
//
// Every Pruner this package constructs ([NewPruner], [NewMySQLPruner],
// [NewSQLitePruner]) satisfies NeverDueTimerReclaimer today, but the method is
// deliberately NOT part of [Pruner]: widening that interface would break every
// consumer who implements it. Reach the capability by type assertion:
//
//	pruner, err := persistence.NewSQLitePruner(db)
//	if err != nil { /* handle */ }
//	if r, ok := pruner.(persistence.NeverDueTimerReclaimer); ok {
//	    n, err := r.ReclaimNeverDueTimers(ctx)
//	    // n orphan rows reclaimed
//	}
//
// Two properties to plan around:
//
//   - ⚠ Reclaiming a row does NOT unpark the instance that armed it. The orphan
//     is the artefact of an instance parked forever; the sweep removes the
//     timer-side diagnostic while the instance stays stuck. Read the armed
//     timers (ListArmed / Stats on a timer store) BEFORE sweeping if the
//     instance identities matter — the sweep reports only a count.
//   - On MySQL running under its DEFAULT strict mode the sweep is a no-op: a
//     zero next_run cannot be stored there at all (next_run is DATETIME(6) NOT
//     NULL and MySQL's DATETIME range starts at 1000-01-01), so no orphan
//     population was ever written. ⚠ That is NOT true of a MySQL configured
//     without STRICT_TRANS_TABLES / NO_ZERO_DATE: it coerces the value to
//     '0000-00-00 00:00:00' instead of erroring, which IS sub-epoch and IS
//     reclaimed. Do not read "no-op on MySQL" as unconditional.
type NeverDueTimerReclaimer interface {
	// ReclaimNeverDueTimers deletes every orphan timer row and returns the
	// number of rows deleted. It takes no cutoff: the epoch sentinel is
	// structural, not a retention policy. It is a single atomic statement, so a
	// row concurrently re-armed with a real next_run survives.
	ReclaimNeverDueTimers(ctx context.Context) (int64, error)
}

// Compile-time check: the concrete store pruner behind every constructor in this
// package carries the optional capability, so the documented type assertion
// cannot silently start failing.
var _ NeverDueTimerReclaimer = (*store.Pruner)(nil)

// NewPruner constructs a Pruner over pool (returns the stable Pruner interface).
// Migrate must have been applied before calling any method.
//
// Wire it into a scheduled job the consumer owns, e.g.:
//
//	pruner := persistence.NewPruner(pool)
//	// every hour, drop outbox events published more than 7 days ago:
//	_, err := pruner.PruneOutbox(ctx, time.Now().Add(-7*24*time.Hour))
func NewPruner(pool *pgxpool.Pool) (Pruner, error) {
	return store.NewPruner(pool, dialect.NewPostgres())
}

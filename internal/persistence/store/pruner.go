package store

import (
	"context"
	"fmt"
	"time"

	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/internal/database"
	"github.com/kartaladev/wrkflw/internal/persistence/dialect"
)

// Pruner deletes safely-eligible rows from the unbounded-growth tables so a
// consumer's scheduled retention job can keep them from overwhelming the
// database (ADR-0052). Every method deletes only rows older than a
// caller-supplied cutoff that are provably safe to drop, and returns the number
// of rows deleted. Pruning cadence and cutoffs are the consumer's
// responsibility — see docs/retention.md.
//
// All DELETE operations run against the pool directly (not inside a
// transaction) because retention pruning is a background maintenance operation
// that must not join a caller's ambient transaction or hold locks that block
// hot-path writes.
//
// Every timestamp cutoff is encoded via [timeArg] so the value is
// format-compatible with the values written by the store layer on every
// backend. On SQLite (TimestampsAsText) this ensures that the lexicographic
// TEXT comparison is apples-to-apples with the fixed-width RFC3339 strings
// stored in the relevant columns (ADR-0080, ADR-0151) — the fixed width is what
// makes string order equal chronological order here.
//
// Processed-message dedup records are pruned through [Deduper.Prune];
// [Pruner.PruneProcessedMessages] re-exposes that method for one-stop ergonomics.
//
// Pruner is safe for concurrent use: it carries no mutable state.
type Pruner struct {
	conn    any // *pgxpool.Pool or *sql.DB
	dialect dialect.Dialect
}

// NewPruner constructs a Pruner over conn using dialect d. conn must be either
// a *pgxpool.Pool (Postgres) or a *sql.DB (MySQL, SQLite). Migrate must be
// applied before calling any method.
// Returns [ErrNilDependency] when conn is nil or d is nil.
//
// Example (Postgres):
//
//	pool, _ := pgxpool.New(ctx, dsn)
//	p, err := store.NewPruner(pool, dialect.NewPostgres())
//
// Example (SQLite, tests):
//
//	db := dbtest.RunTestSQLite(t)
//	p, err := store.NewPruner(db, dialect.NewSQLite())
func NewPruner(conn any, d dialect.Dialect) (*Pruner, error) {
	if isNilDep(conn) {
		return nil, fmt.Errorf("%w: conn", ErrNilDependency)
	}
	if isNilDep(d) {
		return nil, fmt.Errorf("%w: dialect", ErrNilDependency)
	}
	return &Pruner{conn: conn, dialect: d}, nil
}

// PruneOutbox deletes published outbox rows whose published_at is strictly
// before cutoff. Only status='published' rows are eligible: pending rows (not
// yet drained) and dead-lettered rows (awaiting operator redrive) are never
// touched. Returns the number of rows deleted.
//
// A safe cutoff is well past the relay's poll/backoff window so a row that was
// just published is never reclaimed before any late subscriber or replica has
// drained it.
func (p *Pruner) PruneOutbox(ctx context.Context, cutoff time.Time) (int64, error) {
	q, err := database.From(p.conn)
	if err != nil {
		return 0, fmt.Errorf("workflow-store: pruner: prune outbox: conn: %w", err)
	}

	res, err := q.Exec(ctx,
		p.dialect.Rebind(
			`DELETE FROM wrkflw_outbox
			  WHERE status = 'published' AND published_at < ?`),
		timeArg(p.dialect, cutoff.UTC()),
	)
	if err != nil {
		return 0, fmt.Errorf("workflow-store: pruner: prune outbox: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("workflow-store: pruner: prune outbox: rows affected: %w", err)
	}
	return n, nil
}

// PruneCallLinks deletes call-link rows that have already been delivered to
// their parent — status='notified' with a notified_at strictly before cutoff.
//
// This predicate is deliberately conservative. A row is only eligible once the
// parent has been resumed (MarkNotified set status='notified' and stamped
// notified_at), so a row a parent might still need is never deleted:
//   - 'running' children (still executing) survive.
//   - 'completed'/'failed' children that are terminal but NOT yet notified
//     (notified_at IS NULL) survive — the notifier may still have to resume the
//     parent from them.
//
// Returns the number of rows deleted.
func (p *Pruner) PruneCallLinks(ctx context.Context, cutoff time.Time) (int64, error) {
	q, err := database.From(p.conn)
	if err != nil {
		return 0, fmt.Errorf("workflow-store: pruner: prune call links: conn: %w", err)
	}

	res, err := q.Exec(ctx,
		p.dialect.Rebind(
			`DELETE FROM wrkflw_call_links
			  WHERE status = 'notified' AND notified_at < ?`),
		timeArg(p.dialect, cutoff.UTC()),
	)
	if err != nil {
		return 0, fmt.Errorf("workflow-store: pruner: prune call links: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("workflow-store: pruner: prune call links: rows affected: %w", err)
	}
	return n, nil
}

// PruneChainLinks deletes process-chaining lineage rows whose created_at is
// strictly before cutoff. Returns the number of rows deleted.
//
// Trade-off: chain links are ancestry (which predecessor produced which
// successor) and double as the exactly-once chaining backstop. Pruning them
// loses that ancestry for the affected hops and removes the backstop, so
// re-fire of a predecessor's terminal event after pruning could re-chain a
// successor. Choose a cutoff far beyond any window in which a terminal event
// could be redelivered. See docs/retention.md.
func (p *Pruner) PruneChainLinks(ctx context.Context, cutoff time.Time) (int64, error) {
	q, err := database.From(p.conn)
	if err != nil {
		return 0, fmt.Errorf("workflow-store: pruner: prune chain links: conn: %w", err)
	}

	res, err := q.Exec(ctx,
		p.dialect.Rebind(`DELETE FROM wrkflw_chain_links WHERE created_at < ?`),
		timeArg(p.dialect, cutoff.UTC()),
	)
	if err != nil {
		return 0, fmt.Errorf("workflow-store: pruner: prune chain links: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("workflow-store: pruner: prune chain links: rows affected: %w", err)
	}
	return n, nil
}

// PruneProcessedMessages deletes idempotent-consumer dedup records whose
// processed_at is strictly before cutoff. It delegates to [Deduper.Prune];
// supply a cutoff well past the relay max-delivery × backoff window so
// in-flight messages are never evicted. Returns the number of rows deleted.
func (p *Pruner) PruneProcessedMessages(ctx context.Context, cutoff time.Time) (int64, error) {
	// p.conn and p.dialect are guaranteed non-nil by the constructor guard, so
	// the only error from NewDeduper here would be unreachable — ignore it.
	d, err := NewDeduper(p.conn, p.dialect)
	if err != nil {
		return 0, fmt.Errorf("workflow-store: pruner: prune processed messages: %w", err)
	}
	return d.Prune(ctx, cutoff)
}

// PruneTimers deletes timer rows whose next_run is strictly before cutoff,
// whose trigger is not recurring, and which are not [engine.TimerCompensationRetry]
// records. Returns the number of rows deleted.
//
// Fired timers that are no longer needed can accumulate in wrkflw_timers; this
// method lets a consumer's retention job drop them. Choose a cutoff safely past
// any window in which a timer could still fire or be rescheduled.
//
// Compensation-retry rows (kind = [engine.TimerCompensationRetry]) are excluded
// unconditionally, at any cutoff (ADR-0179). Such a row is the only thing that
// will resume its compensation walk: between the compensation action's failure
// and the backoff firing the walk makes no forward progress and holds no token
// of its own, and the retry budget's exhaustion is reachable only by the timer
// firing. The exclusion is needed because the trigger_kind IN-list does not
// cover it — the backoff is armed with [schedule.AfterDuration], whose
// [schedule.Kind] is [schedule.KindOneTime] and therefore inside
// [nonRecurringTriggerKinds]. Note the two clauses read DIFFERENT columns
// carrying different enums: trigger_kind carries a [schedule.Kind], kind carries
// an [engine.TimerKind].
//
// ⚠ This closes the retention-job route to a stranded walk, and only that route.
// A retry row skipped by the runtime's job-store load at boot, or never
// rehydrated at all, still strands its walk; the escape there is ADR-0175's
// operator verbs. Do not read this exclusion as making a lost retry timer
// impossible.
//
// Recurring rows (trigger_kind outside [nonRecurringTriggerKinds]) are excluded
// even when next_run is expired: under D16, next_run is written once when the
// timer is armed and never updated on each recurrence, so an expired next_run
// on a recurring row does not mean the timer is done firing — deleting it would
// drop a still-armed durable row. This is a known caveat, not a full fix; see
// docs/production-checklist.md § timer pruning for the deferred run-count
// follow-up that will let recurring rows be pruned precisely too.
//
// This method mirrors the MySQL-specific PruneTimers extension and is available
// on all three dialects in the neutral store.
func (p *Pruner) PruneTimers(ctx context.Context, cutoff time.Time) (int64, error) {
	q, err := database.From(p.conn)
	if err != nil {
		return 0, fmt.Errorf("workflow-store: pruner: prune timers: conn: %w", err)
	}

	res, err := q.Exec(ctx,
		p.dialect.Rebind(
			`DELETE FROM wrkflw_timers
			  WHERE next_run < ?
			    AND trigger_kind IN (?, ?, ?)
			    AND kind <> ?`),
		timeArg(p.dialect, cutoff.UTC()),
		int16(nonRecurringTriggerKinds[0]), int16(nonRecurringTriggerKinds[1]), int16(nonRecurringTriggerKinds[2]),
		int16(engine.TimerCompensationRetry),
	)
	if err != nil {
		return 0, fmt.Errorf("workflow-store: pruner: prune timers: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("workflow-store: pruner: prune timers: rows affected: %w", err)
	}
	return n, nil
}

// ReclaimNeverDueTimers deletes orphan timer rows: rows whose next_run is
// strictly before the Unix epoch AND whose trigger is recurring. Returns the
// number of rows deleted. It takes no cutoff — the epoch sentinel is structural,
// not a retention policy.
//
// Such a row is never-due by construction. ADR-0176 stopped the engine writing
// a zero next_run for a recurring trigger it could not schedule, but it did not
// reclaim the rows already stored, and [Pruner.PruneTimers] provably cannot: its
// trigger_kind IN-list is exactly [nonRecurringTriggerKinds], so the reachable
// set and the orphan set are disjoint by construction — no cutoff makes
// PruneTimers see an orphan. Such a row heads the keyset index forever, pinning
// [TimerStore.Stats] NextFireAt at 0001-01-01 (ADR-0181).
//
// This is a second, disjoint predicate and deliberately NOT a widening of
// [Pruner.PruneTimers]' IN-list. Widening that list would make an expired
// next_run on a recurring row eligible for deletion — but under D16 next_run is
// written once when a recurring timer is armed and never updated per
// recurrence, so an expired next_run there does not mean the timer is done
// firing. Deleting those rows is exactly the bug ADR-0134 fixed, and the reason
// the IN-list exists.
//
// The threshold is the Unix epoch, not an equality against the zero instant.
// SQLite stores next_run as TEXT and compares it lexicographically, and rows
// written with the pre-ADR-0151 trimmed encoding ("0001-01-01T00:00:00Z") are
// still readable — see [parseTimeText] — but are not byte-equal to the
// fixed-width zero ("0001-01-01T00:00:00.000000000Z"). An equality predicate
// silently misses those rows and reports success. The epoch also sits inside
// MySQL's DATETIME range and above a non-strict MySQL's coerced
// '0000-00-00 00:00:00', and is unreachable by a legitimately armed recurring
// row, whose next_run is always computed strictly forward from the arming
// instant.
//
// The sweep is a single statement so the predicate is re-evaluated atomically:
// a row concurrently re-armed by upsertTimer is simply no longer sub-epoch and
// survives. A SELECT-then-DELETE-by-PK variant would open a TOCTOU window in
// between and destroy such a row.
//
// Deleting the row does not unpark the instance it belongs to — the orphan is
// the artefact of an instance parked forever, and removing it removes the
// timer-side diagnostic while the instance stays stuck. Read
// [TimerStore.ListArmed] or [TimerStore.Stats] first if the identities matter;
// the sweep reports only a count.
//
// On MySQL this is a no-op by construction: a zero next_run cannot be stored
// there at all under the default strict mode, so there is no orphan population.
func (p *Pruner) ReclaimNeverDueTimers(ctx context.Context) (int64, error) {
	q, err := database.From(p.conn)
	if err != nil {
		return 0, fmt.Errorf("workflow-store: pruner: reclaim never-due timers: conn: %w", err)
	}

	res, err := q.Exec(ctx,
		p.dialect.Rebind(
			`DELETE FROM wrkflw_timers
			  WHERE next_run < ?
			    AND trigger_kind IN (?, ?, ?, ?, ?, ?, ?)`),
		timeArg(p.dialect, time.Unix(0, 0).UTC()),
		int16(recurringTriggerKinds[0]), int16(recurringTriggerKinds[1]),
		int16(recurringTriggerKinds[2]), int16(recurringTriggerKinds[3]),
		int16(recurringTriggerKinds[4]), int16(recurringTriggerKinds[5]),
		int16(recurringTriggerKinds[6]),
	)
	if err != nil {
		return 0, fmt.Errorf("workflow-store: pruner: reclaim never-due timers: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("workflow-store: pruner: reclaim never-due timers: rows affected: %w", err)
	}
	return n, nil
}

// nonRecurringTriggerKinds are the [schedule.Kind] values that fire at most
// once — the trigger_kind values [Pruner.PruneTimers] treats as eligible for
// expiry-based deletion. Every other schedule.Kind value is recurring
// ([schedule.TriggerSpec.Recurring] reports true) and is excluded regardless
// of next_run; see the PruneTimers doc comment for why.
var nonRecurringTriggerKinds = [3]schedule.Kind{
	schedule.KindUnset,
	schedule.KindOneTime,
	schedule.KindExpr,
}

// recurringTriggerKinds are the [schedule.Kind] values that fire repeatedly —
// the trigger_kind values [Pruner.ReclaimNeverDueTimers] treats as eligible for
// sub-epoch orphan deletion.
//
// It is the exact complement of [nonRecurringTriggerKinds]: measured against
// the schedule package, [schedule.TriggerSpec.Recurring] reports true for
// exactly these seven kinds and false for exactly those three, and the two
// lists together are every declared Kind. Keep them complementary — the two
// sweeps are correct only while their trigger_kind sets are disjoint and their
// union is total.
var recurringTriggerKinds = [7]schedule.Kind{
	schedule.KindDuration,
	schedule.KindDurationRand,
	schedule.KindCron,
	schedule.KindDaily,
	schedule.KindWeekly,
	schedule.KindMonthly,
	schedule.KindEveryExpr,
}

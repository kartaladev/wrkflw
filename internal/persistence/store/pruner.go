package store

import (
	"context"
	"fmt"
	"time"

	"github.com/kartaladev/wrkflw/definition/schedule"
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
// TEXT comparison is apples-to-apples with the RFC3339Nano strings stored in
// the relevant columns (ADR-0080).
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

// PruneTimers deletes timer rows whose next_run is strictly before cutoff and
// whose trigger is not recurring. Returns the number of rows deleted.
//
// Fired timers that are no longer needed can accumulate in wrkflw_timers; this
// method lets a consumer's retention job drop them. Choose a cutoff safely past
// any window in which a timer could still fire or be rescheduled.
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
			    AND trigger_kind IN (?, ?, ?)`),
		timeArg(p.dialect, cutoff.UTC()),
		int16(nonRecurringTriggerKinds[0]), int16(nonRecurringTriggerKinds[1]), int16(nonRecurringTriggerKinds[2]),
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

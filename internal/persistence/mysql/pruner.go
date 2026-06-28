package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Pruner deletes safely-eligible rows from the unbounded-growth tables so a
// consumer's scheduled retention job can keep them from overwhelming the
// database (ADR-0052). Every method deletes only rows older than a
// caller-supplied cutoff that are provably safe to drop, and returns the number
// of rows deleted. Pruning cadence and cutoffs are the consumer's
// responsibility — see docs/retention.md.
//
// Processed-message dedup records are pruned through [Deduper.Prune]; this type
// re-exposes that as [Pruner.PruneProcessedMessages] for one-stop ergonomics.
type Pruner struct {
	db *sql.DB
}

// NewPruner constructs a Pruner over db. Migrate must be applied before
// calling any method.
func NewPruner(db *sql.DB) *Pruner { return &Pruner{db: db} }

// PruneOutbox deletes published outbox rows whose published_at is strictly
// before cutoff. Only status='published' rows are eligible: pending rows (not
// yet drained) and dead-lettered rows (awaiting operator redrive) are never
// touched. Returns the number of rows deleted.
//
// A safe cutoff is well past the relay's poll/backoff window so a row that was
// just published is never reclaimed before any late subscriber has drained it.
func (p *Pruner) PruneOutbox(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := p.db.ExecContext(ctx,
		`DELETE FROM wrkflw_outbox
		  WHERE status = 'published' AND published_at < ?`,
		cutoff)
	if err != nil {
		return 0, fmt.Errorf("workflow-persistence-mysql: pruner: prune outbox: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("workflow-persistence-mysql: pruner: prune outbox: rows affected: %w", err)
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
	res, err := p.db.ExecContext(ctx,
		`DELETE FROM wrkflw_call_links
		  WHERE status = 'notified' AND notified_at < ?`,
		cutoff)
	if err != nil {
		return 0, fmt.Errorf("workflow-persistence-mysql: pruner: prune call links: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("workflow-persistence-mysql: pruner: prune call links: rows affected: %w", err)
	}
	return n, nil
}

// PruneChainLinks deletes process-chaining lineage rows whose created_at is
// strictly before cutoff. Returns the number of rows deleted.
//
// Trade-off: chain links are ancestry (which predecessor produced which
// successor) and double as the exactly-once chaining backstop. Pruning them
// loses that ancestry for the affected hops and removes the backstop, so re-fire
// of a predecessor's terminal event after pruning could re-chain a successor.
// Choose a cutoff far beyond any window in which a terminal event could be
// redelivered. See docs/retention.md.
func (p *Pruner) PruneChainLinks(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := p.db.ExecContext(ctx,
		`DELETE FROM wrkflw_chain_links WHERE created_at < ?`,
		cutoff)
	if err != nil {
		return 0, fmt.Errorf("workflow-persistence-mysql: pruner: prune chain links: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("workflow-persistence-mysql: pruner: prune chain links: rows affected: %w", err)
	}
	return n, nil
}

// PruneProcessedMessages deletes idempotent-consumer dedup records whose
// processed_at is strictly before cutoff. It delegates to [Deduper.Prune];
// supply a cutoff well past the relay max-delivery × backoff window so in-flight
// messages are never evicted. Returns the number of rows deleted.
func (p *Pruner) PruneProcessedMessages(ctx context.Context, cutoff time.Time) (int64, error) {
	return NewDeduper(p.db).Prune(ctx, cutoff)
}

// PruneTimers deletes timer rows whose fire_at is strictly before cutoff.
// Returns the number of rows deleted.
//
// NOTE: This is a MySQL-specific addition with no Postgres analog — the Postgres
// Pruner does not expose PruneTimers. Fired timers that are no longer needed can
// accumulate in wrkflw_timers; this lets a consumer's retention job drop them.
// Choose a cutoff safely past any window in which a timer could still fire or be
// rescheduled.
func (p *Pruner) PruneTimers(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := p.db.ExecContext(ctx,
		`DELETE FROM wrkflw_timers WHERE fire_at < ?`,
		cutoff)
	if err != nil {
		return 0, fmt.Errorf("workflow-persistence-mysql: pruner: prune timers: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("workflow-persistence-mysql: pruner: prune timers: rows affected: %w", err)
	}
	return n, nil
}

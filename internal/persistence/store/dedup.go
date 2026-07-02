package store

import (
	"context"
	"fmt"
	"time"

	"github.com/zakyalvan/krtlwrkflw/internal/database"
	"github.com/zakyalvan/krtlwrkflw/internal/database/transaction"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/dialect"
)

// Deduper records processed message IDs in wrkflw_processed_message so an
// at-least-once consumer can achieve exactly-once effect (idempotent-consumer
// pattern, ADR-0018). This type is dialect-neutral and operates on any backend
// that provides the wrkflw_processed_message table.
//
// The dedup record joins the caller's ambient transaction (via
// [transaction.JoinOrBegin]) so it commits or rolls back atomically with the
// surrounding business operation. When no ambient transaction is present,
// Deduper begins and commits a fresh leaf transaction so Seen is always atomic.
type Deduper struct {
	conn    any // *pgxpool.Pool or *sql.DB
	dialect dialect.Dialect
}

// NewDeduper constructs a Deduper backed by conn and using the supplied dialect
// for placeholder rewriting and insert-ignore syntax.
//
// conn must be either a *pgxpool.Pool (Postgres) or a *sql.DB (MySQL, SQLite).
// Returns [ErrNilDependency] when conn is nil or d is nil.
func NewDeduper(conn any, d dialect.Dialect) (*Deduper, error) {
	if conn == nil {
		return nil, fmt.Errorf("%w: conn", ErrNilDependency)
	}
	if d == nil {
		return nil, fmt.Errorf("%w: dialect", ErrNilDependency)
	}
	return &Deduper{conn: conn, dialect: d}, nil
}

// Seen records (subscriber, messageID) and reports whether this is the FIRST
// time the pair was seen. firstTime==true means the message is new and the
// caller should process it; firstTime==false means it is a duplicate and the
// caller should skip the side effect.
//
// Seen participates in the caller's ambient transaction when one is stashed in
// ctx (i.e. the context was produced by [transaction.Begin]). In that case the
// dedup record commits or rolls back together with the caller's transaction: a
// rolled-back business transaction also rolls back the Seen mark, so a
// re-delivered message is treated as first-time again (correct at-least-once
// semantics). When no ambient transaction exists, Seen begins and commits its
// own leaf transaction.
//
// The insert uses the dialect's insert-ignore form (INSERT IGNORE on MySQL;
// INSERT … ON CONFLICT DO NOTHING on Postgres and SQLite) so concurrent inserts
// of the same pair resolve without error — RowsAffected()==0 on a duplicate.
func (d *Deduper) Seen(ctx context.Context, subscriber, messageID string) (firstTime bool, err error) {
	q, err := transaction.JoinOrBegin(ctx, d.conn)
	if err != nil {
		return false, fmt.Errorf("workflow-store: deduper: seen: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = q.Rollback(ctx)
		}
	}()

	// Build the insert-ignore statement using the dialect's prefix and suffix so
	// no inline dialect checks are needed here.
	//
	// processed_at is written explicitly via timeArg so the value is stored in
	// the same format that Prune uses for its cutoff comparison. On SQLite the
	// table's DEFAULT uses strftime('%Y-%m-%dT%H:%M:%SZ', 'now'), which omits
	// sub-second precision, while Prune formats its cutoff as RFC3339Nano. Writing
	// processed_at explicitly here ensures the stored string is always in the same
	// RFC3339Nano form as the cutoff, so lexicographic comparison in Prune is
	// correct on all backends.
	stmt := d.dialect.Rebind(
		d.dialect.InsertIgnorePrefix() +
			` INTO wrkflw_processed_message (subscriber, message_id, processed_at)
			 VALUES (?, ?, ?)` +
			d.dialect.InsertIgnoreDedup(),
	)
	res, err := q.Exec(ctx, stmt, subscriber, messageID, timeArg(d.dialect, time.Now().UTC()))
	if err != nil {
		return false, fmt.Errorf("workflow-store: deduper: seen: exec: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("workflow-store: deduper: seen: rows affected: %w", err)
	}

	if err := q.Commit(ctx); err != nil {
		return false, fmt.Errorf("workflow-store: deduper: seen: commit: %w", err)
	}
	committed = true

	// RowsAffected == 1 → INSERT succeeded → first time.
	// RowsAffected == 0 → INSERT was silently ignored (duplicate) → not first time.
	return n == 1, nil
}

// Prune deletes all processed-message records whose processed_at is strictly
// before before. Callers should supply a cutoff well past the relay
// max-delivery × backoff window so in-flight messages are never evicted
// prematurely. Returns the number of rows deleted.
//
// Prune runs on the pool directly (not inside a transaction) because it is a
// background maintenance operation independent of any in-flight message
// processing. The cutoff is converted via [timeArg] to guarantee format parity
// with the values written by [Seen] on every backend.
func (d *Deduper) Prune(ctx context.Context, before time.Time) (int64, error) {
	q, err := database.From(d.conn)
	if err != nil {
		return 0, fmt.Errorf("workflow-store: deduper: prune: from: %w", err)
	}

	res, err := q.Exec(ctx,
		d.dialect.Rebind(`DELETE FROM wrkflw_processed_message WHERE processed_at < ?`),
		timeArg(d.dialect, before.UTC()),
	)
	if err != nil {
		return 0, fmt.Errorf("workflow-store: deduper: prune: exec: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("workflow-store: deduper: prune: rows affected: %w", err)
	}

	return n, nil
}

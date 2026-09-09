package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/internal/database/transaction"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// Compile-time check: Store carries the optional clear-the-mark capability.
var _ kernel.PendingCommandClearer = (*Store)(nil)

// ClearPendingCommands drops the pending-command mark from id's durable
// snapshot, and is the write side of the crash-recovery mark the runtime stamps
// on a step it has committed but not yet performed (issue #110).
//
// It deliberately does NOT behave like [Store.Commit], and the three
// differences are the whole point of having a separate method rather than
// reusing one:
//
//   - The version is NOT advanced. Clearing the mark records that already-
//     committed work has now been performed. It applies no trigger, so it is not
//     a step, and bumping the version would invalidate the token its caller is
//     still holding mid-loop — turning a bookkeeping write into a spurious
//     ErrConcurrentUpdate on the caller's very next commit.
//   - No journal row and no outbox row are written. The journal is the ordered
//     replay log of applied triggers; a clear applies none, and a synthetic entry
//     would corrupt replay.
//   - A stale expected is a NO-OP returning nil, not [kernel.ErrConcurrentUpdate].
//     A version that moved means a later step already rewrote the snapshot, mark
//     included: the caller's intent — "this mark must not survive" — is satisfied,
//     and reporting a conflict would push a retry loop at work already done.
//
// The read and the write run in one transaction and the UPDATE re-asserts the
// version, so a writer that commits between them loses the race harmlessly:
// zero rows match and the newer snapshot, carrying its own mark, is untouched.
func (s *Store) ClearPendingCommands(ctx context.Context, id string, expected kernel.Version) error {
	ctx, span := s.tel.Tracer.Start(ctx, "wrkflw.store.clear_pending_commands")
	defer span.End()

	q, err := transaction.JoinOrBegin(ctx, s.conn)
	if err != nil {
		return s.mapConflict(fmt.Errorf("workflow-store: clear pending commands: begin: %w", err))
	}
	committed := false
	defer func() {
		if !committed {
			_ = q.Rollback(ctx)
		}
	}()

	var snap []byte
	err = q.QueryRow(ctx, s.dialect.Rebind(
		`SELECT snapshot FROM wrkflw_instances WHERE instance_id = ? AND version = ?`),
		id, int64(expected),
	).Scan(&snap)
	if errors.Is(err, sql.ErrNoRows) {
		// Gone, or already advanced past expected. Either way there is no mark of
		// ours left to clear. See the doc comment: this is success.
		return nil
	}
	if err != nil {
		return fmt.Errorf("workflow-store: clear pending commands %q: %w", id, err)
	}

	var st engine.InstanceState
	if err := json.Unmarshal(snap, &st); err != nil {
		return fmt.Errorf("workflow-store: clear pending commands %q: unmarshal snapshot: %w", id, err)
	}
	if len(st.PendingCommands) == 0 && st.PendingCommandsAt.IsZero() {
		return nil // nothing marked; skip the rewrite entirely
	}
	st.PendingCommands = nil
	st.PendingCommandsAt = time.Time{}

	out, err := marshalSnapshot(st, s.historyCap)
	if err != nil {
		return fmt.Errorf("workflow-store: clear pending commands %q: marshal snapshot: %w", id, err)
	}
	if _, err := q.Exec(ctx, s.dialect.Rebind(
		`UPDATE wrkflw_instances SET snapshot = ?, updated_at = ? WHERE instance_id = ? AND version = ?`),
		out, timeArg(s.dialect, s.clk.Now().UTC()), id, int64(expected),
	); err != nil {
		return s.mapConflict(fmt.Errorf("workflow-store: clear pending commands %q: update: %w", id, err))
	}

	if err := q.Commit(ctx); err != nil {
		return s.mapConflict(fmt.Errorf("workflow-store: clear pending commands %q: commit: %w", id, err))
	}
	committed = true
	return nil
}

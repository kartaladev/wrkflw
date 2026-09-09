package store

import (
	"bytes"
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

// Compile-time check: Store carries the optional mark-rewriting capability.
var _ kernel.PendingCommandWriter = (*Store)(nil)

// Snapshot JSON keys for the two pending-command mark fields.
//
// [engine.InstanceState] declares no struct tags, so encoding/json derives these
// from the Go field names — which is why they are spelled out here and pinned by
// TestPendingCommandMarkKeysMatchTheSnapshotEncoding: renaming either field
// without renaming the constant would leave this function writing a key nothing
// reads, silently and with no compiler help.
const (
	pendingCommandsKey   = "PendingCommands"
	pendingCommandsAtKey = "PendingCommandsAt"
)

// WritePendingCommands rewrites the pending-command mark on id's durable
// snapshot, and is the write side of the crash-recovery mark the runtime stamps
// on a step it has committed but not yet performed (issue #110). Passing a nil
// cmds and a zero at clears the mark.
//
// It deliberately does NOT behave like [Store.Commit], and the three
// differences are the whole point of having a separate method rather than
// reusing one:
//
//   - The version is NOT advanced. The mark records what has and has not been
//     performed. It applies no trigger, so it is not a step, and bumping the
//     version would invalidate the token its caller is still holding mid-loop —
//     turning a bookkeeping write into a spurious ErrConcurrentUpdate on the
//     caller's very next commit.
//   - No journal row and no outbox row are written. The journal is the ordered
//     replay log of applied triggers; this applies none, and a synthetic entry
//     would corrupt replay.
//   - A stale expected is a NO-OP returning nil, not [kernel.ErrConcurrentUpdate].
//     A version that moved means a later step already rewrote the snapshot, mark
//     included: the caller's intent is satisfied, and reporting a conflict would
//     push a retry loop at work already done.
//
// ⚠ It edits the snapshot as a JSON OBJECT, replacing exactly two keys, rather
// than decoding into [engine.InstanceState] and re-encoding. That is not
// stylistic. Decoding through the struct discards every key the struct does not
// declare and re-applies [marshalSnapshot]'s history cap under THIS process's
// configuration — and unlike Commit, which only ever runs on the replica
// actively driving one instance, this runs on every replica against every
// instance a sweep lists. A lossy round trip here strips newer fields, or
// re-truncates history under a differently-configured replica's cap, fleet-wide,
// with no version bump for any CAS to notice.
//
// The read and the write run in one transaction and the UPDATE re-asserts the
// version, so a writer that commits between them loses the race harmlessly:
// zero rows match and the newer snapshot, carrying its own mark, is untouched.
func (s *Store) WritePendingCommands(ctx context.Context, id string, expected kernel.Version, cmds []engine.PendingCommand, at time.Time) error {
	ctx, span := s.tel.Tracer.Start(ctx, "wrkflw.store.write_pending_commands")
	defer span.End()

	cmdsJSON, err := json.Marshal(cmds)
	if err != nil {
		return fmt.Errorf("workflow-store: write pending commands %q: marshal mark: %w", id, err)
	}
	atJSON, err := json.Marshal(at)
	if err != nil {
		return fmt.Errorf("workflow-store: write pending commands %q: marshal mark stamp: %w", id, err)
	}

	q, err := transaction.JoinOrBegin(ctx, s.conn)
	if err != nil {
		return s.mapConflict(fmt.Errorf("workflow-store: write pending commands: begin: %w", err))
	}
	committed := false
	defer func() {
		if !committed {
			_ = q.Rollback(ctx)
		}
	}()
	// finish ends the unit on a nothing-to-do exit. It must COMMIT, not fall into
	// the rollback above: under an ambient handle JoinOrBegin returns a joined
	// querier whose Rollback marks the OWNER's unit rollback-only, so a no-op
	// here would fail the caller's whole transaction. Same shape as
	// call_links.go's nothing-to-do exit.
	finish := func() error {
		if cerr := q.Commit(ctx); cerr != nil {
			return s.mapConflict(fmt.Errorf("workflow-store: write pending commands %q: commit: %w", id, cerr))
		}
		committed = true
		return nil
	}

	var snap []byte
	err = q.QueryRow(ctx, s.dialect.Rebind(
		`SELECT snapshot FROM wrkflw_instances WHERE instance_id = ? AND version = ?`),
		id, int64(expected),
	).Scan(&snap)
	if errors.Is(err, sql.ErrNoRows) {
		// Gone, or already advanced past expected. Either way there is no mark of
		// ours left to write. See the doc comment: this is success.
		return finish()
	}
	if err != nil {
		return fmt.Errorf("workflow-store: write pending commands %q: %w", id, err)
	}

	var doc map[string]json.RawMessage
	if err := json.Unmarshal(snap, &doc); err != nil {
		return fmt.Errorf("workflow-store: write pending commands %q: unmarshal snapshot: %w", id, err)
	}
	// ⚠ A JSON `null` snapshot decodes WITHOUT error and leaves doc nil, and
	// assigning into a nil map panics. Every other malformed shape — an array, a
	// string, a number, a truncated object — is rejected by Unmarshal above; only
	// `null` gets this far, and Postgres JSONB, MySQL JSON and SQLite TEXT all
	// accept the literal. Reproduced on all three.
	//
	// It is rejected rather than repaired: a null snapshot is a corrupted row (no
	// build in this tree writes one — marshalSnapshot always emits an object), and
	// silently replacing it with an object containing only a mark would make a
	// corrupted instance look repaired. Returning an error puts it on the same
	// footing as every other malformed shape: the sweep logs it and skips that
	// instance without aborting the batch.
	//
	// The guard is load-bearing, not defensive decoration. There is exactly one
	// recover() in runtime/ (processdriver_action.go, around a service action) and
	// nothing guards the sweep, which walks every instance the store holds — so an
	// unrecovered panic here would take down ProcessDriver.Start and the sweep
	// goroutine, crash-looping every replica at boot for as long as the row exists.
	if doc == nil {
		return fmt.Errorf("workflow-store: write pending commands %q: snapshot is JSON null, not an object", id)
	}
	if bytes.Equal(doc[pendingCommandsKey], cmdsJSON) && bytes.Equal(doc[pendingCommandsAtKey], atJSON) {
		return finish() // already exactly this; skip the rewrite entirely
	}
	doc[pendingCommandsKey] = cmdsJSON
	doc[pendingCommandsAtKey] = atJSON

	out, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("workflow-store: write pending commands %q: marshal snapshot: %w", id, err)
	}
	if _, err := q.Exec(ctx, s.dialect.Rebind(
		`UPDATE wrkflw_instances SET snapshot = ?, updated_at = ? WHERE instance_id = ? AND version = ?`),
		out, timeArg(s.dialect, s.clk.Now().UTC()), id, int64(expected),
	); err != nil {
		return s.mapConflict(fmt.Errorf("workflow-store: write pending commands %q: update: %w", id, err))
	}
	return finish()
}

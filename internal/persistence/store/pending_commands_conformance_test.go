package store_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/internal/persistence/store"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// allPendingKinds is a mark carrying ALL FIVE recoverable kinds, each populated
// down to its non-scalar fields.
//
// Every kind, not a representative sample. The two that an earlier version of
// this file omitted are the two most likely to break:
//
//   - PendingUpdateTask holds the envelope's only POINTER field, and a nil Task
//     is exactly what PendingCommand.Command branches on — so "absent" and "zero"
//     must stay distinguishable across the wire. It is also not hypothetical:
//     InstanceState.endInstance's cancelOpenTasks emits one UpdateTask per open
//     human task on every terminal transition that closes one.
//   - PendingAwaitHuman holds the only `omitzero` STRUCT field (authz.AuthzSpec),
//     whose two string slices are the thing AuthzSpec.Clone exists to isolate.
func allPendingKinds(markedAt time.Time) []engine.PendingCommand {
	task := humantask.HumanTask{
		TaskID: "task-1", NodeID: "approve", InstanceID: "i-pending",
		State: humantask.Claimed, CreatedAt: markedAt,
		Vars:        map[string]any{"region": "EU"},
		Eligibility: authz.AuthzSpec{Roles: []string{"manager"}, Privileges: []string{"task claim"}},
		Candidates:  []authz.Actor{{ID: "alice", Roles: []string{"manager"}}},
		Claim:       &humantask.Claim{Actor: authz.Actor{ID: "alice"}, At: markedAt},
	}
	return []engine.PendingCommand{
		{
			Kind:      engine.PendingInvokeAction,
			CommandID: "cmd-1",
			Name:      "charge",
			Input:     map[string]any{"amount": "12.50"},
		},
		{
			Kind:        engine.PendingAwaitHuman,
			TaskID:      "task-1",
			Eligibility: authz.AuthzSpec{Roles: []string{"manager"}, Privileges: []string{"task claim"}},
		},
		{Kind: engine.PendingUpdateTask, Task: &task},
		{
			Kind:    engine.PendingThrowSignal,
			Name:    "escalated",
			Payload: map[string]any{"who": "alice"},
		},
		{
			Kind:      engine.PendingStartSubInstance,
			CommandID: "cmd-2",
			DefRef:    model.Version("child", 3),
			Input:     map[string]any{"seed": "v"},
		},
	}
}

// pendingMarkedStep is newTestInstance — the package's richer seed, carrying
// tokens, history and sequence counters — plus a mark of every recoverable kind.
func pendingMarkedStep(t *testing.T, id string, markedAt time.Time) kernel.AppliedStep {
	t.Helper()
	step := newTestInstance(t, id)
	step.State.PendingCommands = allPendingKinds(markedAt)
	step.State.PendingCommandsAt = markedAt
	return step
}

// TestPendingCommandMarkKeysMatchTheSnapshotEncoding pins the two JSON keys
// Store.WritePendingCommands edits by name.
//
// engine.InstanceState declares no struct tags, so those keys are derived from
// the Go field names. Renaming a field without renaming the constant would leave
// the store writing a key nothing reads — a mark that silently never clears,
// with no compiler error anywhere. This is the only thing that fails if that
// happens.
func TestPendingCommandMarkKeysMatchTheSnapshotEncoding(t *testing.T) {
	t.Parallel()

	markedAt := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	raw, err := json.Marshal(engine.InstanceState{
		InstanceID:        "i",
		PendingCommands:   allPendingKinds(markedAt),
		PendingCommandsAt: markedAt,
	})
	require.NoError(t, err)

	var doc map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &doc))
	for _, key := range store.PendingCommandMarkKeysForTest() {
		assert.Contains(t, doc, key,
			"Store.WritePendingCommands edits %q by name; the snapshot encoding must still use it", key)
	}
}

// TestPendingCommandMarkRoundTripsAndClearsOnEveryDialect covers the three claims
// mechanism (a) rests on, in one 3-dialect pass because each one's assertion is
// the next one's setup.
//
// FIRST: the mark rides the existing `snapshot` column and therefore needs NO
// migration and no dialect-parity work — Postgres JSONB, MySQL JSON and SQLite
// TEXT all carry all five kinds, field for field. A silently dropped Input map
// would leave a mark that survives the round trip and re-drives the wrong command,
// so the comparison is field-level and not merely "non-empty".
//
// SECOND: rewriting the mark is a BOOKKEEPING write, not a step. The version does
// not move, the journal does not grow, and a stale expected is a silent no-op.
// Each is load-bearing: a version bump would hand the driver a spurious
// ErrConcurrentUpdate on its very next commit, a journal row would corrupt
// replay, and treating a stale version as a conflict would push a retry loop at
// work that is already done.
//
// THIRD: the rewrite is LOSSLESS. It edits the snapshot as a JSON object rather
// than decoding through engine.InstanceState, because this write — unlike Commit
// — runs on every replica against every listed instance, so a decode-and-re-encode
// through a stale struct definition would strip newer fields fleet-wide with no
// version bump for any CAS to notice.
func TestPendingCommandMarkRoundTripsAndClearsOnEveryDialect(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		ctx := t.Context()
		s, err := store.New(b.conn, b.dialect)
		require.NoError(t, err)

		markedAt := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
		id := "i-pending-" + b.name
		seeded := pendingMarkedStep(t, id, markedAt)

		version, err := s.Create(ctx, seeded)
		require.NoError(t, err)

		// ── the mark round-trips, all five kinds ─────────────────────────────
		got, gotVersion, err := s.Load(ctx, id)
		require.NoError(t, err)
		require.Len(t, got.PendingCommands, 5, "dialect %s must carry every kind", b.name)
		assert.Equal(t, seeded.State.PendingCommands, got.PendingCommands,
			"dialect %s must round-trip every pending-command field", b.name)
		assert.True(t, got.PendingCommandsAt.Equal(markedAt),
			"dialect %s must round-trip the mark stamp", b.name)
		require.Equal(t, version, gotVersion)

		// Each kind must reconstruct into the command it envelopes.
		for _, pending := range got.PendingCommands {
			cmd, cerr := pending.Command()
			require.NoError(t, cerr, "dialect %s: %s must reconstruct", b.name, pending.Kind)
			require.NotNil(t, cmd)
		}

		journalBefore, err := s.Entries(ctx, id)
		require.NoError(t, err)

		// ── a partial rewrite records progress without advancing the version ──
		remainder := got.PendingCommands[3:]
		restampedAt := markedAt.Add(time.Hour)
		require.NoError(t, s.WritePendingCommands(ctx, id, version, remainder, restampedAt))

		partial, partialVersion, err := s.Load(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, remainder, partial.PendingCommands,
			"dialect %s: a partial write must leave exactly the remainder", b.name)
		assert.True(t, partial.PendingCommandsAt.Equal(restampedAt),
			"dialect %s: a failed attempt must re-stamp the mark", b.name)
		assert.Equal(t, version, partialVersion,
			"dialect %s: rewriting the mark must NOT advance the concurrency token", b.name)

		// ── and the clear leaves every other byte alone ──────────────────────
		require.NoError(t, s.WritePendingCommands(ctx, id, version, nil, time.Time{}))

		cleared, clearedVersion, err := s.Load(ctx, id)
		require.NoError(t, err)
		assert.Empty(t, cleared.PendingCommands, "dialect %s: the mark must be gone", b.name)
		assert.True(t, cleared.PendingCommandsAt.IsZero(),
			"dialect %s: the stamp must be cleared with the mark it dates", b.name)
		assert.Equal(t, version, clearedVersion,
			"dialect %s: clearing the mark must NOT advance the concurrency token", b.name)

		// Everything except the two mark fields must be untouched — asserted by
		// comparing the WHOLE state, not a hand-picked field or two.
		wantUnchanged := got
		wantUnchanged.PendingCommands = nil
		wantUnchanged.PendingCommandsAt = time.Time{}
		assert.Equal(t, wantUnchanged, cleared,
			"dialect %s: only the mark may change", b.name)

		journalAfter, err := s.Entries(ctx, id)
		require.NoError(t, err)
		assert.Len(t, journalAfter, len(journalBefore),
			"dialect %s: rewriting the mark applies no trigger, so it must write no journal row", b.name)

		// A stale expected is success, not a conflict.
		require.NoError(t, s.WritePendingCommands(ctx, id, version+7, nil, time.Time{}),
			"dialect %s: a stale version must be a silent no-op", b.name)
		// So is an instance that does not exist at all.
		require.NoError(t, s.WritePendingCommands(ctx, "no-such-instance", version, nil, time.Time{}),
			"dialect %s: an unknown instance must be a silent no-op", b.name)
	})
}

// TestWritePendingCommandsPreservesUnrecognisedSnapshotFields is the regression
// guard for the lossy round trip.
//
// A replica running an older build — mid rolling-deploy, which is exactly when a
// sweep runs — must not strip snapshot keys its engine.InstanceState does not
// declare. Decoding through the struct would; editing the JSON object does not.
// The test writes a key no Go struct in this tree declares and asserts it
// survives a full write-and-clear cycle.
func TestWritePendingCommandsPreservesUnrecognisedSnapshotFields(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		ctx := t.Context()
		s, err := store.New(b.conn, b.dialect)
		require.NoError(t, err)

		markedAt := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
		id := "i-unknown-" + b.name
		version, err := s.Create(ctx, pendingMarkedStep(t, id, markedAt))
		require.NoError(t, err)

		// Plant a field written by a hypothetical newer build.
		q := s.QuerierForTest(ctx)
		var raw []byte
		require.NoError(t, q.QueryRow(ctx, b.dialect.Rebind(
			`SELECT snapshot FROM wrkflw_instances WHERE instance_id = ?`), id).Scan(&raw))
		var doc map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &doc))
		doc["FieldFromANewerBuild"] = json.RawMessage(`{"kept":true}`)
		planted, err := json.Marshal(doc)
		require.NoError(t, err)
		_, err = q.Exec(ctx, b.dialect.Rebind(
			`UPDATE wrkflw_instances SET snapshot = ? WHERE instance_id = ?`), planted, id)
		require.NoError(t, err)

		require.NoError(t, s.WritePendingCommands(ctx, id, version, nil, time.Time{}))

		require.NoError(t, q.QueryRow(ctx, b.dialect.Rebind(
			`SELECT snapshot FROM wrkflw_instances WHERE instance_id = ?`), id).Scan(&raw))
		var after map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &after))
		assert.JSONEq(t, `{"kept":true}`, string(after["FieldFromANewerBuild"]),
			"dialect %s: a key this build does not declare must survive the mark rewrite", b.name)
		assert.JSONEq(t, `null`, string(after["PendingCommands"]),
			"dialect %s: the mark must still have been cleared", b.name)
	})
}

// TestWritePendingCommandsRejectsAMalformedSnapshot pins that no snapshot shape
// a column will accept can panic the mark write.
//
// A JSON `null` is the one that could: it decodes WITHOUT error and leaves the
// target map nil, and assigning into a nil map panics. Postgres JSONB, MySQL
// JSON and SQLite TEXT all accept the literal. Reproduced on all three in review.
//
// It matters far out of proportion to its likelihood — no build in this tree
// writes a null snapshot — because of where the panic lands. There is exactly one
// recover() in runtime/ and it is around a service action, not this path; the
// sweep walks every instance the store holds; so one corrupted row would
// crash-loop every replica at boot, fleet-wide, for as long as it existed.
//
// The assertion is positive on both halves: an ERROR is returned (not a panic,
// and not a silent success), and the row is left exactly as it was.
func TestWritePendingCommandsRejectsAMalformedSnapshot(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		ctx := t.Context()
		s, err := store.New(b.conn, b.dialect)
		require.NoError(t, err)

		markedAt := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
		id := "i-null-" + b.name
		version, err := s.Create(ctx, pendingMarkedStep(t, id, markedAt))
		require.NoError(t, err)

		q := s.QuerierForTest(ctx)
		_, err = q.Exec(ctx, b.dialect.Rebind(
			`UPDATE wrkflw_instances SET snapshot = ? WHERE instance_id = ?`), []byte(`null`), id)
		require.NoError(t, err, "dialect %s must accept a JSON null in the snapshot column", b.name)

		require.NotPanics(t, func() {
			err = s.WritePendingCommands(ctx, id, version, nil, time.Time{})
		}, "dialect %s: a null snapshot must not panic the mark write", b.name)
		require.Error(t, err, "dialect %s: a null snapshot must be reported, not silently accepted", b.name)
		assert.Contains(t, err.Error(), "JSON null")

		var after []byte
		require.NoError(t, q.QueryRow(ctx, b.dialect.Rebind(
			`SELECT snapshot FROM wrkflw_instances WHERE instance_id = ?`), id).Scan(&after))
		assert.JSONEq(t, `null`, string(after),
			"dialect %s: a refused write must leave the row exactly as it was", b.name)
	})
}

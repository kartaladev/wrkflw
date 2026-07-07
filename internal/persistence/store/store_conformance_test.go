package store_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/internal/database"
	"github.com/zakyalvan/krtlwrkflw/internal/persistence/store"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// appliedStep builds a minimal AppliedStep for instance id emitting one outbox
// event on topic. The trigger is always a StartInstance so Entries assertions
// are deterministic across dialects.
func appliedStep(id, topic string) kernel.AppliedStep {
	now := time.Unix(1700000000, 0).UTC()
	return kernel.AppliedStep{
		State:   engine.InstanceState{InstanceID: id, DefID: "d", DefVersion: 1, Status: engine.StatusRunning, StartedAt: now},
		Trigger: engine.NewStartInstance(now, map[string]any{"k": "v"}),
		Events:  []kernel.OutboxEvent{{Topic: topic, Payload: map[string]any{"x": float64(1)}}},
	}
}

// newTestInstance builds a richer valid AppliedStep (with tokens, history and
// sequence counters) suitable for exercising snapshot + time round-trip.
func newTestInstance(t *testing.T, id string) kernel.AppliedStep {
	t.Helper()
	now := time.Unix(1700000000, 123456789).UTC() // sub-second to catch precision loss
	return kernel.AppliedStep{
		State: engine.InstanceState{
			InstanceID: id,
			DefID:      "d",
			DefVersion: 1,
			Status:     engine.StatusRunning,
			StartedAt:  now,
			Variables:  map[string]any{"str": "hello"},
			Tokens: []engine.Token{
				{ID: "tok-1", NodeID: "node-start", State: engine.TokenActive, EnteredAt: now},
			},
			History: []engine.NodeVisit{
				{NodeID: "node-start", TokenID: "tok-1", EnteredAt: now},
			},
			Scopes:   []engine.Scope{},
			Tasks:    []humantask.HumanTask{},
			CmdSeq:   1,
			TokenSeq: 1,
		},
		Trigger: engine.NewStartInstance(now, map[string]any{"str": "hello"}),
		Events:  []kernel.OutboxEvent{{Topic: "created", Payload: map[string]any{"x": float64(1)}}},
	}
}

// countRows runs a scalar COUNT(*) query against b.conn via the neutral Querier,
// rebinding placeholders for the backend under test.
func countRows(t *testing.T, b backend, query string, args ...any) int {
	t.Helper()
	q, err := database.From(b.conn)
	require.NoError(t, err, "database.From(%s)", b.name)
	var n int
	require.NoError(t,
		q.QueryRow(t.Context(), b.dialect.Rebind(query), args...).Scan(&n),
		"count query on %s: %s", b.name, query)
	return n
}

// TestStoreCreateLoadCommit is the 3-dialect conformance suite for the neutral
// store core: Create → Load round-trip, Commit CAS + journal + outbox, stale
// version conflict, and Entries ordering. It folds in the postgres/mysql store
// test assertions so nothing regresses in the port.
func TestStoreCreateLoadCommit(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		s, err := store.New(b.conn, b.dialect)
		require.NoError(t, err)
		var _ kernel.InstanceStore = s // compile-time interface check
		var _ kernel.JournalReader = s // compile-time JournalReader check

		// --- Create → Load round-trip (with time round-trip) ---
		inst := newTestInstance(t, "i1")
		tok, err := s.Create(t.Context(), inst)
		require.NoError(t, err, "%s: create", b.name)
		require.Equal(t, kernel.Version(1), tok, "%s: first token is 1", b.name)

		got, loaded, err := s.Load(t.Context(), "i1")
		require.NoError(t, err, "%s: load", b.name)
		require.Equal(t, "i1", got.InstanceID)
		require.Equal(t, tok, loaded)
		// TIME round-trip (ADR-0080): StartedAt must survive to the SAME instant, UTC.
		require.True(t, got.StartedAt.Equal(inst.State.StartedAt),
			"%s: StartedAt must round-trip to same instant: want %v got %v",
			b.name, inst.State.StartedAt, got.StartedAt)
		require.Equal(t, time.UTC, got.StartedAt.Location(),
			"%s: StartedAt must be UTC-located after load", b.name)

		// Create writes exactly one journal row + one outbox row at seq 1.
		require.Equal(t, 1, countRows(t, b,
			`SELECT COUNT(*) FROM wrkflw_journal WHERE instance_id = ? AND seq = 1`, "i1"),
			"%s: create journal row", b.name)
		require.Equal(t, 1, countRows(t, b,
			`SELECT COUNT(*) FROM wrkflw_outbox WHERE instance_id = ? AND dedup_key = ?`, "i1", "i1:1:0"),
			"%s: create outbox row", b.name)

		// --- Commit advances version + persists journal + outbox ---
		next, err := s.Commit(t.Context(), tok, appliedStep("i1", "b"))
		require.NoError(t, err, "%s: commit", b.name)
		require.Equal(t, kernel.Version(2), next, "%s: commit advances token", b.name)

		require.Equal(t, 1, countRows(t, b,
			`SELECT COUNT(*) FROM wrkflw_journal WHERE instance_id = ? AND seq = 2`, "i1"),
			"%s: commit journal row at seq 2", b.name)
		require.Equal(t, 1, countRows(t, b,
			`SELECT COUNT(*) FROM wrkflw_outbox WHERE dedup_key = ?`, "i1:2:0"),
			"%s: commit outbox row", b.name)

		// --- Entries returns journal rows in seq order ---
		entries, err := s.Entries(t.Context(), "i1")
		require.NoError(t, err, "%s: entries", b.name)
		require.Len(t, entries, 2, "%s: two journal entries", b.name)
		for _, e := range entries {
			_, ok := e.(engine.StartInstance)
			require.True(t, ok, "%s: expected StartInstance trigger", b.name)
		}

		// --- Stale version conflicts (CAS sentinel preserved) ---
		_, err = s.Commit(t.Context(), tok, appliedStep("i1", "c")) // tok is now stale (current is 2)
		require.ErrorIs(t, err, kernel.ErrConcurrentUpdate,
			"%s: stale token must map to ErrConcurrentUpdate", b.name)

		// --- Load missing ---
		_, _, err = s.Load(t.Context(), "nope")
		require.ErrorIs(t, err, kernel.ErrInstanceNotFound, "%s: load missing", b.name)

		// --- Duplicate create ---
		_, err = s.Create(t.Context(), appliedStep("i1", "dup"))
		require.ErrorIs(t, err, kernel.ErrInstanceExists, "%s: duplicate create", b.name)

		// --- Entries for unknown instance is empty, not an error ---
		empty, err := s.Entries(t.Context(), "unknown")
		require.NoError(t, err, "%s: entries unknown", b.name)
		require.Empty(t, empty, "%s: entries unknown is empty", b.name)
	})
}

// TestStoreSideEffects exercises the atomic side-effect helpers that Create and
// Commit fuse into the state transaction: call-link insert + flip, timer arm +
// cancel, and the WithOutboxNotify emit. It runs on all three dialects and
// asserts the resulting rows through the neutral Querier.
func TestStoreSideEffects(t *testing.T) {
	forEachDialect(t, func(t *testing.T, b backend) {
		s, err := store.New(b.conn, b.dialect, store.WithHistoryCap(4), store.WithOutboxNotify())
		require.NoError(t, err)
		now := time.Unix(1700000000, 0).UTC()

		// Create a parent + a child that carries a NewCallLink and an armed timer.
		_, err = s.Create(t.Context(), appliedStep("parent", "p"))
		require.NoError(t, err, "%s: create parent", b.name)

		child := appliedStep("child", "c")
		child.NewCallLink = &kernel.CallLink{
			ChildInstanceID:  "child",
			ParentInstanceID: "parent",
			ParentCommandID:  "cmd-1",
			ParentDefID:      "d",
			ParentDefVersion: 1,
			Depth:            1,
		}
		child.TimerArms = []kernel.ArmedTimer{
			{InstanceID: "child", DefID: "d", DefVersion: 1, TimerID: "t1", NextRun: now.Add(time.Hour), Kind: engine.TimerIntermediate},
		}
		childTok, err := s.Create(t.Context(), child)
		require.NoError(t, err, "%s: create child", b.name)

		require.Equal(t, 1, countRows(t, b,
			`SELECT COUNT(*) FROM wrkflw_call_links WHERE child_instance_id = ? AND status = 'running'`, "child"),
			"%s: call link inserted running", b.name)
		require.Equal(t, 1, countRows(t, b,
			`SELECT COUNT(*) FROM wrkflw_timers WHERE instance_id = ? AND timer_id = ?`, "child", "t1"),
			"%s: timer armed", b.name)

		// Commit a terminal step: flip the call link to completed + cancel the timer.
		term := appliedStep("child", "done")
		term.CallOutcome = &kernel.CallOutcome{Completed: true, Output: map[string]any{"r": float64(1)}}
		term.TimerCancels = []string{"t1"}
		_, err = s.Commit(t.Context(), childTok, term)
		require.NoError(t, err, "%s: commit terminal", b.name)

		require.Equal(t, 1, countRows(t, b,
			`SELECT COUNT(*) FROM wrkflw_call_links WHERE child_instance_id = ? AND status = 'completed'`, "child"),
			"%s: call link flipped completed", b.name)
		require.Equal(t, 0, countRows(t, b,
			`SELECT COUNT(*) FROM wrkflw_timers WHERE instance_id = ? AND timer_id = ?`, "child", "t1"),
			"%s: timer cancelled", b.name)

		// A second child with a FAILED terminal outcome exercises the error-text
		// branch of flipCallLink (status='failed', error column set).
		child2 := appliedStep("child2", "c2")
		child2.NewCallLink = &kernel.CallLink{
			ChildInstanceID: "child2", ParentInstanceID: "parent", ParentCommandID: "cmd-2",
			ParentDefID: "d", ParentDefVersion: 1, Depth: 1,
		}
		tok2, err := s.Create(t.Context(), child2)
		require.NoError(t, err, "%s: create child2", b.name)
		fail := appliedStep("child2", "failed")
		fail.CallOutcome = &kernel.CallOutcome{Completed: false, Err: "boom"}
		_, err = s.Commit(t.Context(), tok2, fail)
		require.NoError(t, err, "%s: commit failed outcome", b.name)
		require.Equal(t, 1, countRows(t, b,
			`SELECT COUNT(*) FROM wrkflw_call_links WHERE child_instance_id = ? AND status = 'failed'`, "child2"),
			"%s: call link flipped failed", b.name)

		// A CallOutcome on a root instance (no link row) is a clean no-op flip.
		rootTok, err := s.Create(t.Context(), appliedStep("root", "r"))
		require.NoError(t, err, "%s: create root", b.name)
		rootTerm := appliedStep("root", "root-done")
		rootTerm.CallOutcome = &kernel.CallOutcome{Completed: true}
		_, err = s.Commit(t.Context(), rootTok, rootTerm)
		require.NoError(t, err, "%s: root flip no-op must not error", b.name)
	})
}

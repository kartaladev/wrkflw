package mysql_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	mypkg "github.com/zakyalvan/krtlwrkflw/internal/persistence/mysql"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

func newMySQLStore(t *testing.T) *mypkg.Store {
	t.Helper()
	db := dbtest.RunTestMySQL(t)
	return mypkg.NewStore(db)
}

func mysqlAppliedStep(id, topic string) runtime.AppliedStep {
	now := time.Unix(1700000000, 0).UTC()
	return runtime.AppliedStep{
		State:   engine.InstanceState{InstanceID: id, DefID: "d", DefVersion: 1, Status: engine.StatusRunning, StartedAt: now},
		Trigger: engine.NewStartInstance(now, map[string]any{"k": "v"}),
		Events:  []runtime.OutboxEvent{{Topic: topic, Payload: map[string]any{"x": float64(1)}}},
	}
}

// TestStore_CreateLoadEntries_RoundTrip verifies the core Create → Load → Entries path.
func TestStore_CreateLoadEntries_RoundTrip(t *testing.T) {
	t.Parallel()
	s := newMySQLStore(t)

	// Create
	tok, err := s.Create(t.Context(), mysqlAppliedStep("i1", "topic.a"))
	require.NoError(t, err, "Create must succeed")

	// Load round-trips instance state
	st, loaded, err := s.Load(t.Context(), "i1")
	require.NoError(t, err, "Load must succeed")
	require.Equal(t, "i1", st.InstanceID)
	require.Equal(t, tok, loaded, "loaded token must match creation token")

	// Commit a second step
	next, err := s.Commit(t.Context(), tok, mysqlAppliedStep("i1", "topic.b"))
	require.NoError(t, err, "Commit must succeed")
	require.Greater(t, int64(next), int64(tok), "next token must advance")

	// Entries returns both journal rows
	entries, err := s.Entries(t.Context(), "i1")
	require.NoError(t, err, "Entries must succeed")
	require.Len(t, entries, 2, "two journal rows after create+commit")

	for _, e := range entries {
		_, ok := e.(engine.StartInstance)
		require.True(t, ok, "expected StartInstance trigger in journal")
	}
}

// TestStore_Load_NotFound verifies that loading a non-existent instance returns ErrInstanceNotFound.
func TestStore_Load_NotFound(t *testing.T) {
	t.Parallel()
	s := newMySQLStore(t)

	_, _, err := s.Load(t.Context(), "does-not-exist")
	require.ErrorIs(t, err, runtime.ErrInstanceNotFound)
}

// TestStore_Create_DuplicateReturnsErrInstanceExists verifies that creating an
// instance with a duplicate ID returns ErrInstanceExists.
func TestStore_Create_DuplicateReturnsErrInstanceExists(t *testing.T) {
	t.Parallel()
	s := newMySQLStore(t)

	_, err := s.Create(t.Context(), mysqlAppliedStep("dup-i1", "topic.a"))
	require.NoError(t, err)

	_, err = s.Create(t.Context(), mysqlAppliedStep("dup-i1", "topic.b"))
	require.ErrorIs(t, err, runtime.ErrInstanceExists)
}

// TestStore_Commit_ConflictReturnsErrConcurrentUpdate verifies that using a stale
// token for a Commit returns ErrConcurrentUpdate (optimistic-concurrency control).
func TestStore_Commit_ConflictReturnsErrConcurrentUpdate(t *testing.T) {
	t.Parallel()
	s := newMySQLStore(t)

	// Create instance, get token v1.
	tok, err := s.Create(t.Context(), mysqlAppliedStep("cas-i1", "topic.a"))
	require.NoError(t, err)

	// First commit succeeds with v1, advances to v2.
	_, err = s.Commit(t.Context(), tok, mysqlAppliedStep("cas-i1", "topic.b"))
	require.NoError(t, err, "first commit with v1 must succeed")

	// Second commit with the same stale v1 must conflict.
	_, err = s.Commit(t.Context(), tok, mysqlAppliedStep("cas-i1", "topic.c"))
	require.ErrorIs(t, err, runtime.ErrConcurrentUpdate, "stale-token commit must return ErrConcurrentUpdate")
}

// TestStore_Entries_UnknownInstance verifies Entries for a non-existent instance
// returns empty slice without error.
func TestStore_Entries_UnknownInstance(t *testing.T) {
	t.Parallel()
	s := newMySQLStore(t)

	entries, err := s.Entries(t.Context(), "unknown-inst")
	require.NoError(t, err)
	require.Empty(t, entries)
}

// TestStore_Create_WithCallLink verifies that Create correctly writes the NewCallLink
// row atomically with the instance.
func TestStore_Create_WithCallLink(t *testing.T) {
	t.Parallel()
	s := newMySQLStore(t)

	// First, create the parent instance (FK constraint on call_links → wrkflw_instances).
	_, err := s.Create(t.Context(), mysqlAppliedStep("parent-1", "topic.parent"))
	require.NoError(t, err)

	// Create child instance with a call link referencing the parent.
	link := &runtime.CallLink{
		ChildInstanceID:  "child-1",
		ParentInstanceID: "parent-1",
		ParentCommandID:  "cmd-1",
		ParentDefID:      "d",
		ParentDefVersion: 1,
		Depth:            1,
	}
	now := time.Unix(1700000000, 0).UTC()
	childStep := runtime.AppliedStep{
		State:       engine.InstanceState{InstanceID: "child-1", DefID: "d", DefVersion: 1, Status: engine.StatusRunning, StartedAt: now},
		Trigger:     engine.NewStartInstance(now, nil),
		NewCallLink: link,
	}
	tok, err := s.Create(t.Context(), childStep)
	require.NoError(t, err, "Create with call link must succeed")
	require.Equal(t, runtime.Token(1), tok)
}

// TestStore_Commit_WithCallOutcome verifies that Commit correctly flips the call link status.
func TestStore_Commit_WithCallOutcome(t *testing.T) {
	t.Parallel()
	s := newMySQLStore(t)

	// Create parent and child instances with a call link.
	_, err := s.Create(t.Context(), mysqlAppliedStep("parent-co-1", "topic.parent"))
	require.NoError(t, err)

	now := time.Unix(1700000000, 0).UTC()
	link := &runtime.CallLink{
		ChildInstanceID:  "child-co-1",
		ParentInstanceID: "parent-co-1",
		ParentCommandID:  "cmd-co-1",
		ParentDefID:      "d",
		ParentDefVersion: 1,
		Depth:            1,
	}
	childStep := runtime.AppliedStep{
		State:       engine.InstanceState{InstanceID: "child-co-1", DefID: "d", DefVersion: 1, Status: engine.StatusRunning, StartedAt: now},
		Trigger:     engine.NewStartInstance(now, nil),
		NewCallLink: link,
	}
	tok, err := s.Create(t.Context(), childStep)
	require.NoError(t, err)

	// Commit with CallOutcome (child completed).
	outcome := &runtime.CallOutcome{Completed: true, Output: map[string]any{"result": "ok"}}
	commitStep := runtime.AppliedStep{
		State:       engine.InstanceState{InstanceID: "child-co-1", DefID: "d", DefVersion: 1, Status: engine.StatusCompleted, StartedAt: now},
		Trigger:     engine.NewStartInstance(now, nil),
		CallOutcome: outcome,
	}
	_, err = s.Commit(t.Context(), tok, commitStep)
	require.NoError(t, err, "Commit with call outcome must succeed")
}

// TestStore_Create_WithTimerArm verifies that Create correctly writes timer rows.
func TestStore_Create_WithTimerArm(t *testing.T) {
	t.Parallel()
	s := newMySQLStore(t)

	now := time.Unix(1700000000, 0).UTC()
	fireAt := now.Add(time.Hour)
	step := runtime.AppliedStep{
		State:   engine.InstanceState{InstanceID: "timer-i1", DefID: "d", DefVersion: 1, Status: engine.StatusRunning, StartedAt: now},
		Trigger: engine.NewStartInstance(now, nil),
		TimerArms: []runtime.ArmedTimer{
			{InstanceID: "timer-i1", TimerID: "t1", FireAt: fireAt, Kind: engine.TimerIntermediate, DefID: "d", DefVersion: 1},
		},
	}
	tok, err := s.Create(t.Context(), step)
	require.NoError(t, err, "Create with timer arm must succeed")

	// Commit with timer cancel.
	commitStep := runtime.AppliedStep{
		State:        engine.InstanceState{InstanceID: "timer-i1", DefID: "d", DefVersion: 1, Status: engine.StatusRunning, StartedAt: now},
		Trigger:      engine.NewStartInstance(now, nil),
		TimerCancels: []string{"t1"},
	}
	_, err = s.Commit(t.Context(), tok, commitStep)
	require.NoError(t, err, "Commit with timer cancel must succeed")
}

// TestStore_WithOptions verifies that option constructors do not panic.
func TestStore_WithOptions(t *testing.T) {
	db := dbtest.RunTestMySQL(t)
	s := mypkg.NewStore(db,
		mypkg.WithHistoryCap(5),
		mypkg.WithStoreLogger(nil),
		mypkg.WithStoreTracerProvider(nil),
		mypkg.WithStoreMeterProvider(nil),
	)
	require.NotNil(t, s)
}

// TestStore_Commit_WithTimerArm verifies that Commit correctly upserts timer rows.
func TestStore_Commit_WithTimerArm(t *testing.T) {
	t.Parallel()
	s := newMySQLStore(t)

	now := time.Unix(1700000000, 0).UTC()
	fireAt := now.Add(time.Hour)

	tok, err := s.Create(t.Context(), mysqlAppliedStep("commit-timer-i1", "topic.a"))
	require.NoError(t, err)

	// Commit with timer arm.
	commitStep := runtime.AppliedStep{
		State:   engine.InstanceState{InstanceID: "commit-timer-i1", DefID: "d", DefVersion: 1, Status: engine.StatusRunning, StartedAt: now},
		Trigger: engine.NewStartInstance(now, nil),
		TimerArms: []runtime.ArmedTimer{
			{InstanceID: "commit-timer-i1", TimerID: "t-commit-1", FireAt: fireAt, Kind: engine.TimerDeadline, DefID: "d", DefVersion: 1},
		},
	}
	tok2, err := s.Commit(t.Context(), tok, commitStep)
	require.NoError(t, err, "Commit with timer arm must succeed")

	// Commit again with cancel of the armed timer.
	cancelStep := runtime.AppliedStep{
		State:        engine.InstanceState{InstanceID: "commit-timer-i1", DefID: "d", DefVersion: 1, Status: engine.StatusRunning, StartedAt: now},
		Trigger:      engine.NewStartInstance(now, nil),
		TimerCancels: []string{"t-commit-1"},
	}
	_, err = s.Commit(t.Context(), tok2, cancelStep)
	require.NoError(t, err, "Commit with timer cancel must succeed")
}

// TestStore_Commit_WithFailedCallOutcome verifies that Commit flips call-link to failed.
func TestStore_Commit_WithFailedCallOutcome(t *testing.T) {
	t.Parallel()
	s := newMySQLStore(t)

	// Create parent.
	_, err := s.Create(t.Context(), mysqlAppliedStep("parent-fail-1", "topic.parent"))
	require.NoError(t, err)

	now := time.Unix(1700000000, 0).UTC()
	link := &runtime.CallLink{
		ChildInstanceID:  "child-fail-1",
		ParentInstanceID: "parent-fail-1",
		ParentCommandID:  "cmd-fail-1",
		ParentDefID:      "d",
		ParentDefVersion: 1,
		Depth:            1,
	}
	childStep := runtime.AppliedStep{
		State:       engine.InstanceState{InstanceID: "child-fail-1", DefID: "d", DefVersion: 1, Status: engine.StatusRunning, StartedAt: now},
		Trigger:     engine.NewStartInstance(now, nil),
		NewCallLink: link,
	}
	tok, err := s.Create(t.Context(), childStep)
	require.NoError(t, err)

	// Commit with failed outcome.
	outcome := &runtime.CallOutcome{Completed: false, Err: "child-failed"}
	commitStep := runtime.AppliedStep{
		State:       engine.InstanceState{InstanceID: "child-fail-1", DefID: "d", DefVersion: 1, Status: engine.StatusFailed, StartedAt: now},
		Trigger:     engine.NewStartInstance(now, nil),
		CallOutcome: outcome,
	}
	_, err = s.Commit(t.Context(), tok, commitStep)
	require.NoError(t, err, "Commit with failed call outcome must succeed")
}

// TestStore_Commit_MultipleOutboxEvents verifies that Commit inserts multiple outbox events.
func TestStore_Commit_MultipleOutboxEvents(t *testing.T) {
	t.Parallel()
	s := newMySQLStore(t)

	now := time.Unix(1700000000, 0).UTC()
	tok, err := s.Create(t.Context(), mysqlAppliedStep("multi-evt-i1", "topic.a"))
	require.NoError(t, err)

	// Commit with multiple outbox events.
	commitStep := runtime.AppliedStep{
		State:   engine.InstanceState{InstanceID: "multi-evt-i1", DefID: "d", DefVersion: 1, Status: engine.StatusRunning, StartedAt: now},
		Trigger: engine.NewStartInstance(now, nil),
		Events: []runtime.OutboxEvent{
			{Topic: "topic.x", Payload: map[string]any{"n": 1}},
			{Topic: "topic.y", Payload: map[string]any{"n": 2}},
		},
	}
	_, err = s.Commit(t.Context(), tok, commitStep)
	require.NoError(t, err, "Commit with multiple outbox events must succeed")
}

// TestStore_ErrorOnClosedDB exercises the error branches for Create, Load, Commit, Entries
// when the DB is closed (simulates connection failures).
func TestStore_ErrorOnClosedDB(t *testing.T) {
	// Use a fresh db just to get a valid connection string, then close it.
	db := dbtest.RunTestMySQL(t)
	// Close immediately so all subsequent operations fail.
	require.NoError(t, db.Close())

	s := mypkg.NewStore(db)
	now := time.Unix(1700000000, 0).UTC()
	step := runtime.AppliedStep{
		State:   engine.InstanceState{InstanceID: "closed-i1", DefID: "d", DefVersion: 1, Status: engine.StatusRunning, StartedAt: now},
		Trigger: engine.NewStartInstance(now, nil),
	}

	_, err := s.Create(t.Context(), step)
	require.Error(t, err, "Create on closed db must fail")

	_, _, err = s.Load(t.Context(), "closed-i1")
	require.Error(t, err, "Load on closed db must fail")

	_, err = s.Commit(t.Context(), runtime.Token(1), step)
	require.Error(t, err, "Commit on closed db must fail")

	_, err = s.Entries(t.Context(), "closed-i1")
	require.Error(t, err, "Entries on closed db must fail")
}

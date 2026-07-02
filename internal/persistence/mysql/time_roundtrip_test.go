package mysql_test

import (
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/internal/dbtest"
	mypkg "github.com/zakyalvan/krtlwrkflw/internal/persistence/mysql"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// TestTimerFireAtRehydratesUTC asserts that fire_at timestamps read back from
// wrkflw_timers via TimerStore.ListArmed are UTC-located (zone offset == 0),
// regardless of the process time zone. Run under TZ=Asia/Jakarta to catch
// time.Local leakage from DATETIME(6) without explicit UTC normalization.
// MySQL with loc=UTC typically returns UTC already; this test acts as a
// documented regression guard ensuring the .UTC() normalization is always
// applied after the fix.
func TestTimerFireAtRehydratesUTC(t *testing.T) {
	t.Parallel()
	db := dbtest.RunTestMySQL(t)
	store := mypkg.NewStore(db)
	ts := mypkg.NewTimerStore(db)

	// A known fire_at instant stored in UTC.
	want := time.Date(2031, 3, 4, 5, 6, 7, 0, time.UTC) // MySQL DATETIME(6) precision; no sub-second ns

	base := time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC)
	st := engine.InstanceState{
		InstanceID: "tr-my-utc-1",
		DefID:      "def-utc",
		DefVersion: 1,
		Status:     engine.StatusRunning,
		StartedAt:  base,
	}
	_, err := store.Create(t.Context(), runtime.AppliedStep{
		State:   st,
		Trigger: engine.NewStartInstance(base, nil),
		TimerArms: []runtime.ArmedTimer{{
			InstanceID: "tr-my-utc-1",
			DefID:      "def-utc",
			DefVersion: 1,
			TimerID:    "fire-utc",
			FireAt:     want,
			Kind:       engine.TimerIntermediate,
		}},
	})
	require.NoError(t, err)

	armed, err := ts.ListArmed(t.Context())
	require.NoError(t, err)
	require.Len(t, armed, 1)

	got := armed[0].FireAt
	_, off := got.Zone()
	if off != 0 {
		t.Fatalf("FireAt zone offset = %d, want 0 (UTC); got %v", off, got)
	}
	if !got.Equal(want) {
		t.Fatalf("FireAt = %v, want %v", got, want)
	}
}

// TestListerStartedAtEndedAtRehydratesUTC asserts that started_at and ended_at
// timestamps scanned by the MySQL lister are UTC-located (zone offset == 0) and
// instant-preserved. MySQL with loc=UTC returns UTC; this test is a regression
// guard that the .UTC() normalization is in place. Run under TZ=Asia/Jakarta.
func TestListerStartedAtEndedAtRehydratesUTC(t *testing.T) {
	t.Parallel()
	db := dbtest.RunTestMySQL(t)
	store := mypkg.NewStore(db)
	lister := mypkg.NewLister(db)

	wantStarted := time.Date(2032, 1, 15, 8, 30, 0, 0, time.UTC)

	_, err := store.Create(t.Context(), runtime.AppliedStep{
		State: engine.InstanceState{
			InstanceID: "lister-utc-my-1",
			DefID:      "def-lister-utc",
			DefVersion: 1,
			Status:     engine.StatusRunning,
			StartedAt:  wantStarted,
		},
		Trigger: engine.NewStartInstance(wantStarted, nil),
	})
	require.NoError(t, err)

	page, err := lister.List(t.Context(), runtime.InstanceFilter{})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)

	got := page.Items[0]

	// started_at: must be UTC-located and instant-preserved.
	_, startOff := got.StartedAt.Zone()
	if startOff != 0 {
		t.Errorf("StartedAt zone offset = %d, want 0 (UTC); got %v", startOff, got.StartedAt)
	}
	if !got.StartedAt.Equal(wantStarted) {
		t.Errorf("StartedAt = %v, want %v", got.StartedAt, wantStarted)
	}

	// ended_at: for a running instance it should be nil; verify nil-safe handling.
	if got.EndedAt != nil {
		_, endOff := got.EndedAt.Zone()
		if endOff != 0 {
			t.Errorf("EndedAt zone offset = %d, want 0 (UTC); got %v", endOff, *got.EndedAt)
		}
	}
}

// TestRelayDeadLetterCreatedAtRehydratesUTC asserts that the created_at column
// of a dead-lettered outbox row is UTC-located (zone offset == 0) and
// instant-preserved when scanned by ListDeadLettered. Run under TZ=Asia/Jakarta.
// MySQL with loc=UTC returns UTC; this test acts as a regression guard.
func TestRelayDeadLetterCreatedAtRehydratesUTC(t *testing.T) {
	t.Parallel()
	db := dbtest.RunTestMySQL(t)

	wantCreatedAt := time.Date(2032, 2, 20, 12, 0, 0, 0, time.UTC)

	res, err := db.ExecContext(t.Context(),
		`INSERT INTO wrkflw_outbox
		   (instance_id, topic, payload, dedup_key, created_at, status, retry_count, next_attempt_at, last_error)
		 VALUES (?, ?, ?, ?, ?, 'dead', 5, ?, 'test-error')`,
		"dl-utc-my-instance", "dl.utc.event", `{}`, "dl-utc-my-1", wantCreatedAt, wantCreatedAt,
	)
	require.NoError(t, err)
	_, err = res.LastInsertId()
	require.NoError(t, err)

	relay := mypkg.NewRelay(db, &recordingPub{}, mypkg.WithRelayClock(clockwork.NewFakeClock()))

	dead, err := relay.ListDeadLettered(t.Context(), 10)
	require.NoError(t, err)
	require.Len(t, dead, 1)

	got := dead[0].CreatedAt
	_, off := got.Zone()
	if off != 0 {
		t.Errorf("DeadLetter.CreatedAt zone offset = %d, want 0 (UTC); got %v", off, got)
	}
	if !got.Equal(wantCreatedAt) {
		t.Errorf("DeadLetter.CreatedAt = %v, want %v", got, wantCreatedAt)
	}
}

// TestChainLinkCreatedAtRehydratesUTC asserts that the created_at column of a
// recorded chain link is UTC-located (zone offset == 0) and instant-preserved
// when scanned by LookupBySuccessor. Run under TZ=Asia/Jakarta.
// MySQL with loc=UTC returns UTC; this test acts as a regression guard.
func TestChainLinkCreatedAtRehydratesUTC(t *testing.T) {
	t.Parallel()
	db := dbtest.RunTestMySQL(t)
	cls := mypkg.NewChainLinkStore(db)

	wantCreatedAt := time.Date(2032, 3, 10, 9, 0, 0, 0, time.UTC)

	link := runtime.ChainLink{
		PredecessorID:            "cl-utc-pred-my",
		PredecessorDefinitionRef: "order:1",
		Outcome:                  runtime.OutcomeCompleted,
		SuccessorID:              "cl-utc-succ-my",
		SuccessorDefinitionRef:   "fulfillment:1",
		StartVars:                map[string]any{"k": "v"},
		CreatedAt:                wantCreatedAt,
	}
	require.NoError(t, cls.Record(t.Context(), link))

	got, ok, err := cls.LookupBySuccessor(t.Context(), "cl-utc-succ-my")
	require.NoError(t, err)
	require.True(t, ok)

	_, off := got.CreatedAt.Zone()
	if off != 0 {
		t.Errorf("ChainLink.CreatedAt zone offset = %d, want 0 (UTC); got %v", off, got.CreatedAt)
	}
	if !got.CreatedAt.Equal(wantCreatedAt) {
		t.Errorf("ChainLink.CreatedAt = %v, want %v", got.CreatedAt, wantCreatedAt)
	}
}

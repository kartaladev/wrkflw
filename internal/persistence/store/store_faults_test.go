package store_test

import (
	"testing"
	"time"

	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/kartaladev/wrkflw/internal/persistence/dialect"
	"github.com/kartaladev/wrkflw/internal/persistence/store"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/stretchr/testify/require"
)

// TestStoreCreateBeginError covers the begin-error branch of Create and Commit:
// an unsupported connection type makes transaction.JoinOrBegin fail before any
// write is attempted.
func TestStoreCreateBeginError(t *testing.T) {
	s, err := store.New(struct{}{}, dialect.NewSQLite()) // unsupported conn type
	require.NoError(t, err)
	_, err = s.Create(t.Context(), appliedStep("i", "a"))
	require.Error(t, err, "create must fail on unsupported conn")
	_, err = s.Commit(t.Context(), 1, appliedStep("i", "b"))
	require.Error(t, err, "commit must fail on unsupported conn")
}

// TestStoreWriteErrors uses a migrated SQLite DB with a table dropped mid-flight
// to force the driver-error wrap branches of the write helpers. This exercises
// the error paths that the happy-path conformance suite cannot reach, on the
// in-process backend so no Docker is required.
func TestStoreWriteErrors(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()

	tests := map[string]struct {
		drop string
		run  func(t *testing.T, s *store.Store)
	}{
		"create insert-instance error": {
			drop: "wrkflw_instances",
			run: func(t *testing.T, s *store.Store) {
				_, err := s.Create(t.Context(), appliedStep("i", "a"))
				require.Error(t, err)
			},
		},
		"create write-journal error": {
			drop: "wrkflw_journal",
			run: func(t *testing.T, s *store.Store) {
				_, err := s.Create(t.Context(), appliedStep("i", "a"))
				require.Error(t, err)
			},
		},
		"create write-outbox error": {
			drop: "wrkflw_outbox",
			run: func(t *testing.T, s *store.Store) {
				_, err := s.Create(t.Context(), appliedStep("i", "a"))
				require.Error(t, err)
			},
		},
		"create call-link error": {
			drop: "wrkflw_call_links",
			run: func(t *testing.T, s *store.Store) {
				step := appliedStep("i", "a")
				step.NewCallLink = &kernel.CallLink{ChildInstanceID: "i", ParentInstanceID: "p", ParentDefID: "d", ParentDefVersion: 1}
				_, err := s.Create(t.Context(), step)
				require.Error(t, err)
			},
		},
		"create timer-arm error": {
			drop: "wrkflw_timers",
			run: func(t *testing.T, s *store.Store) {
				step := appliedStep("i", "a")
				step.TimerArms = []kernel.ArmedTimer{{InstanceID: "i", DefID: "d", DefVersion: 1, TimerID: "t", NextRun: now, Kind: engine.TimerIntermediate}}
				_, err := s.Create(t.Context(), step)
				require.Error(t, err)
			},
		},
		"entries query error": {
			drop: "wrkflw_journal",
			run: func(t *testing.T, s *store.Store) {
				_, err := s.Entries(t.Context(), "i")
				require.Error(t, err)
			},
		},
		"load query error": {
			drop: "wrkflw_instances",
			run: func(t *testing.T, s *store.Store) {
				_, _, err := s.Load(t.Context(), "i")
				require.Error(t, err)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			db := dbtest.RunTestSQLite(t)
			_, err := db.ExecContext(t.Context(), "DROP TABLE "+tc.drop)
			require.NoError(t, err, "drop %s", tc.drop)
			s, err := store.New(db, dialect.NewSQLite())
			require.NoError(t, err)
			tc.run(t, s)
		})
	}
}

// TestStoreCommitWriteErrors forces the write-error branches inside Commit
// (after the CAS UPDATE succeeds) by dropping the journal/outbox/timer tables of
// a live instance mid-flight.
func TestStoreCommitWriteErrors(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()

	tests := map[string]struct {
		drop string
		mut  func(step *kernel.AppliedStep)
	}{
		"commit write-journal error": {drop: "wrkflw_journal"},
		"commit write-outbox error":  {drop: "wrkflw_outbox"},
		"commit call-link error": {
			drop: "wrkflw_call_links",
			mut:  func(s *kernel.AppliedStep) { s.CallOutcome = &kernel.CallOutcome{Completed: true} },
		},
		"commit timer-cancel error": {
			drop: "wrkflw_timers",
			mut:  func(s *kernel.AppliedStep) { s.TimerCancels = []string{"t"} },
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			db := dbtest.RunTestSQLite(t)
			s, err := store.New(db, dialect.NewSQLite())
			require.NoError(t, err)
			tok, err := s.Create(t.Context(), appliedStep("i", "a"))
			require.NoError(t, err)

			_, err = db.ExecContext(t.Context(), "DROP TABLE "+tc.drop)
			require.NoError(t, err, "drop %s", tc.drop)

			step := appliedStep("i", "b")
			if tc.mut != nil {
				tc.mut(&step)
			}
			_ = now
			_, err = s.Commit(t.Context(), tok, step)
			require.Error(t, err, "commit must surface the dropped-table write error")
		})
	}
}

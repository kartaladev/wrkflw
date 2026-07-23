package store_test

import (
	"testing"
	"time"

	"github.com/kartaladev/wrkflw/definition/schedule"
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
// (after the CAS UPDATE succeeds) by dropping the journal/outbox/call-link
// tables of a live instance mid-flight.
func TestStoreCommitWriteErrors(t *testing.T) {
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
			_, err = s.Commit(t.Context(), tok, step)
			require.Error(t, err, "commit must surface the dropped-table write error")
		})
	}
}

// TestTimerWriterWriteErrors covers the driver-level SQL-error branches of the
// standalone [kernel.TimerWriter] capability (ADR-0134) — UpsertJob, DeleteJob,
// and DeleteJobByTimerID — by dropping wrkflw_timers mid-flight, mirroring
// [TestStoreWriteErrors]'s dropped-table harness. TestTimerWriterAtomicWithCommit
// only exercises fn-returned rollbacks; these cases force the writer's own
// driver-error wrap branches (the `workflow-store:` prefix) directly, which the
// happy-path TimerWriter suite in timerwriter_test.go cannot reach.
func TestTimerWriterWriteErrors(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()

	tests := map[string]struct {
		run func(t *testing.T, ts *store.TimerStore)
	}{
		"upsert job error": {
			run: func(t *testing.T, ts *store.TimerStore) {
				err := ts.UpsertJob(t.Context(), kernel.JobSpec{
					InstanceID: "i", TimerID: "t", DefID: "d", DefVersion: 1,
					Trigger: schedule.At(now.Add(time.Hour)), NextRun: now.Add(time.Hour),
					Kind: engine.TimerDeadline,
				})
				require.Error(t, err)
				require.ErrorContains(t, err, "workflow-store:")
			},
		},
		"delete job error": {
			run: func(t *testing.T, ts *store.TimerStore) {
				err := ts.DeleteJob(t.Context(), "i", "t")
				require.Error(t, err)
				require.ErrorContains(t, err, "workflow-store:")
			},
		},
		"delete job by timer id error": {
			run: func(t *testing.T, ts *store.TimerStore) {
				err := ts.DeleteJobByTimerID(t.Context(), "t")
				require.Error(t, err)
				require.ErrorContains(t, err, "workflow-store:")
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			db := dbtest.RunTestSQLite(t)
			_, err := db.ExecContext(t.Context(), "DROP TABLE wrkflw_timers")
			require.NoError(t, err, "drop wrkflw_timers")
			ts, err := store.NewTimerStore(db, dialect.NewSQLite())
			require.NoError(t, err)
			tc.run(t, ts)
		})
	}
}

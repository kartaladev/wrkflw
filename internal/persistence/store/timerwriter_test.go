package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/kartaladev/wrkflw/internal/persistence/dialect"
	"github.com/kartaladev/wrkflw/internal/persistence/store"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// errUpsertJobBoom is an injected sentinel distinct from any package error, so
// assertions can tell "fn's own error propagated unwrapped" apart from a
// store-internal error (mirrors errRunInTxBoom in txrunner_test.go).
var errUpsertJobBoom = errors.New("upsert job: boom")

// seedTimerWriterInstance creates a bare running instance (no fused TimerArms)
// via Store.Create, satisfying wrkflw_timers.instance_id's FK so a subsequent
// TimerWriter.UpsertJob/DeleteJob call has a row to reference.
func seedTimerWriterInstance(t *testing.T, s *store.Store, id string, at time.Time) {
	t.Helper()
	_, err := s.Create(t.Context(), kernel.AppliedStep{
		State:   engine.InstanceState{InstanceID: id, DefID: "d", DefVersion: 1, Status: engine.StatusRunning, StartedAt: at},
		Trigger: engine.NewStartInstance(at, nil),
	})
	require.NoError(t, err, "seedTimerWriterInstance %q", id)
}

// TestTimerWriterUpsertJobDescriptorRoundTrip verifies that UpsertJob persists
// all 8 wrkflw_timers columns (instance_id, timer_id, next_run, kind, def_id,
// def_version, trigger_kind, trigger_payload) and that ListArmed reconstructs
// an equal ArmedTimer — including a real non-zero engine.TimerKind (ADR-0134
// TimerWriter capability).
func TestTimerWriterUpsertJobDescriptorRoundTrip(t *testing.T) {
	base := time.Date(2026, 7, 23, 9, 0, 0, 0, time.UTC)

	forEachDialect(t, func(t *testing.T, b backend) {
		s, err := store.New(b.conn, b.dialect)
		require.NoError(t, err)
		ts, err := store.NewTimerStore(b.conn, b.dialect)
		require.NoError(t, err)
		var _ kernel.TimerWriter = ts // compile-time interface check

		seedTimerWriterInstance(t, s, "tw-desc-inst", base)

		spec := kernel.JobSpec{
			TimerID:    "tw-cron",
			InstanceID: "tw-desc-inst",
			DefID:      "proc-def",
			DefVersion: 3,
			Trigger:    schedule.Cron("0 9 * * *"),
			NextRun:    base.Add(24 * time.Hour),
			Kind:       engine.TimerDeadline,
		}
		require.NoError(t, ts.UpsertJob(t.Context(), spec), "%s: UpsertJob", b.name)

		armed, err := ts.ListArmed(t.Context())
		require.NoError(t, err, "%s: ListArmed", b.name)
		require.Len(t, armed, 1, "%s: want 1 timer", b.name)
		got := armed[0]

		assert.Equal(t, spec.InstanceID, got.InstanceID, "%s: InstanceID", b.name)
		assert.Equal(t, spec.TimerID, got.TimerID, "%s: TimerID", b.name)
		assert.Equal(t, spec.DefID, got.DefID, "%s: DefID", b.name)
		assert.Equal(t, spec.DefVersion, got.DefVersion, "%s: DefVersion", b.name)
		assert.Equal(t, engine.TimerDeadline, got.Kind, "%s: Kind", b.name)
		assert.True(t, got.NextRun.Equal(spec.NextRun),
			"%s: NextRun: want %v got %v", b.name, spec.NextRun, got.NextRun)
		assert.Equal(t, schedule.KindCron, got.Trigger.Kind(), "%s: Trigger.Kind", b.name)
		assert.True(t, got.Trigger.Recurring(), "%s: Trigger.Recurring", b.name)
		expr, ok := got.Trigger.CronExpr()
		assert.True(t, ok, "%s: cron expr present", b.name)
		assert.Equal(t, "0 9 * * *", expr, "%s: cron expr survives", b.name)
	})
}

// TestTimerWriterDeleteJob verifies both delete paths: DeleteJob (scoped by
// instanceID+timerID) and DeleteJobByTimerID (timerID alone — engine timer ids
// are globally unique, so a bare lookup is unambiguous; Task 10's
// JobStore.Delete(id) uses this form).
func TestTimerWriterDeleteJob(t *testing.T) {
	base := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)

	cases := []struct {
		name   string
		delete func(t *testing.T, ts *store.TimerStore, instanceID, timerID string) error
	}{
		{
			name: "DeleteJob by (instanceID, timerID)",
			delete: func(t *testing.T, ts *store.TimerStore, instanceID, timerID string) error {
				t.Helper()
				return ts.DeleteJob(t.Context(), instanceID, timerID)
			},
		},
		{
			name: "DeleteJobByTimerID by timerID alone",
			delete: func(t *testing.T, ts *store.TimerStore, instanceID, timerID string) error {
				t.Helper()
				return ts.DeleteJobByTimerID(t.Context(), timerID)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			forEachDialect(t, func(t *testing.T, b backend) {
				s, err := store.New(b.conn, b.dialect)
				require.NoError(t, err)
				ts, err := store.NewTimerStore(b.conn, b.dialect)
				require.NoError(t, err)

				instID := "tw-del-inst"
				seedTimerWriterInstance(t, s, instID, base)
				require.NoError(t, ts.UpsertJob(t.Context(), kernel.JobSpec{
					TimerID: "del-timer", InstanceID: instID, DefID: "d", DefVersion: 1,
					Trigger: schedule.At(base.Add(time.Hour)), NextRun: base.Add(time.Hour),
					Kind: engine.TimerRetry,
				}), "%s: seed UpsertJob", b.name)

				require.NoError(t, tc.delete(t, ts, instID, "del-timer"), "%s: delete", b.name)

				armed, err := ts.ListArmed(t.Context())
				require.NoError(t, err, "%s: ListArmed after delete", b.name)
				assert.Empty(t, armed, "%s: row must be gone", b.name)
			})
		})
	}
}

// TestTimerWriterAtomicWithCommit verifies same-tx atomicity (ADR-0134):
// inside store.RunInTx, a Store.Commit and a TimerStore.UpsertJob both join
// the ambient handle; when fn errors after both calls, NEITHER write
// persists — the state commit and the durable job descriptor rise and fall
// together.
func TestTimerWriterAtomicWithCommit(t *testing.T) {
	base := time.Date(2026, 7, 23, 11, 0, 0, 0, time.UTC)

	forEachDialect(t, func(t *testing.T, b backend) {
		s, err := store.New(b.conn, b.dialect)
		require.NoError(t, err)
		ts, err := store.NewTimerStore(b.conn, b.dialect)
		require.NoError(t, err)

		instID := "tw-atomic-inst"
		seedTimerWriterInstance(t, s, instID, base)

		spec := kernel.JobSpec{
			TimerID: "atomic-timer", InstanceID: instID, DefID: "d", DefVersion: 1,
			Trigger: schedule.At(base.Add(time.Hour)), NextRun: base.Add(time.Hour),
			Kind: engine.TimerDeadline,
		}

		err = s.RunInTx(t.Context(), func(txCtx context.Context) error {
			if _, cerr := s.Commit(txCtx, 1, kernel.AppliedStep{
				State: engine.InstanceState{
					InstanceID: instID, DefID: "d", DefVersion: 1,
					Status: engine.StatusRunning, StartedAt: base,
				},
				Trigger: engine.NewTimerFired(base, "unused"),
			}); cerr != nil {
				return cerr
			}
			if uerr := ts.UpsertJob(txCtx, spec); uerr != nil {
				return uerr
			}
			return errUpsertJobBoom
		})
		require.ErrorIs(t, err, errUpsertJobBoom,
			"%s: RunInTx must surface fn's error unchanged", b.name)

		// The Commit step must have rolled back: version stays at 1.
		_, ver, lerr := s.Load(t.Context(), instID)
		require.NoError(t, lerr, "%s: instance must still exist", b.name)
		assert.Equal(t, kernel.Version(1), ver,
			"%s: Commit must roll back with the unit — version stays 1", b.name)

		// UpsertJob must have rolled back too: no armed timer.
		armed, aerr := ts.ListArmed(t.Context())
		require.NoError(t, aerr, "%s: ListArmed", b.name)
		assert.Empty(t, armed, "%s: UpsertJob must roll back together with the Commit step", b.name)
	})
}

// TestTimerWriterJoinByCtxAcrossDifferentConn documents that TimerWriter
// atomicity composition is entirely ctx-carried, not conn-carried (audit v2
// correction of the old "same-conn negative"): a TimerStore constructed over a
// completely DIFFERENT SQLite connection than the Store still writes into —
// and rolls back with — the ambient transaction, because
// [transaction.JoinOrBegin] joins the handle stashed in ctx and ignores its
// own conn argument whenever an ambient handle is present. The deployment
// invariant this proves is same-DATABASE wiring, not same-connection/pool
// identity. SQLite-only: no Docker daemon required, and the mechanism under
// test (ctx-carried join) is dialect-independent.
func TestTimerWriterJoinByCtxAcrossDifferentConn(t *testing.T) {
	db1 := dbtest.RunTestSQLite(t)
	db2 := dbtest.RunTestSQLite(t) // a genuinely different SQLite file/connection

	s, err := store.New(db1, dialect.NewSQLite())
	require.NoError(t, err)
	tsForeign, err := store.NewTimerStore(db2, dialect.NewSQLite()) // DIFFERENT conn than s
	require.NoError(t, err)

	base := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	instID := "tw-join-ctx"
	seedTimerWriterInstance(t, s, instID, base)

	spec := kernel.JobSpec{
		TimerID: "foreign-timer", InstanceID: instID, DefID: "d", DefVersion: 1,
		Trigger: schedule.At(base.Add(time.Hour)), NextRun: base.Add(time.Hour),
		Kind: engine.TimerInWait,
	}

	err = s.RunInTx(t.Context(), func(txCtx context.Context) error {
		// tsForeign's own conn is db2 — an entirely separate database — yet
		// handing it txCtx makes JoinOrBegin ignore db2 and join the ambient
		// handle (backed by db1, s's real conn). The write lands in the SAME
		// unit of work s.RunInTx owns.
		if uerr := tsForeign.UpsertJob(txCtx, spec); uerr != nil {
			return uerr
		}
		return errUpsertJobBoom
	})
	require.ErrorIs(t, err, errUpsertJobBoom, "RunInTx must surface fn's error unchanged")

	// db1 (the real conn, read back via a fresh TimerStore over it) must show
	// NO row: the foreign-conn writer's row rolled back too.
	tsReal, err := store.NewTimerStore(db1, dialect.NewSQLite())
	require.NoError(t, err)
	armed, err := tsReal.ListArmed(t.Context())
	require.NoError(t, err, "ListArmed over the real conn (db1)")
	assert.Empty(t, armed, "the foreign-conn writer's row must have rolled back with the ambient tx")

	// db2 (tsForeign's OWN conn) must also show no row: the write never
	// touched it at all — it joined db1's transaction instead of using its
	// own conn.
	tsForeignReadBack, err := store.NewTimerStore(db2, dialect.NewSQLite())
	require.NoError(t, err)
	armed2, err := tsForeignReadBack.ListArmed(t.Context())
	require.NoError(t, err, "ListArmed over db2")
	assert.Empty(t, armed2, "db2 (tsForeign's own conn) must never have received the write at all")
}

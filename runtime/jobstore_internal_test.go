package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/scheduler"
)

// otherKindJob wraps a *timerJob but reports a foreign Kind, so it still
// satisfies the package's private descriptor-recovery assertion (descriptor()
// is promoted from the embedded *timerJob) while failing the timerJobKind
// guard — proving jobStore.Save rejects any job whose Kind() is not
// timerJobKind, not merely a wholly foreign implementation with no descriptor
// at all (see TestJobStoreSave's "foreign job implementation" case for that).
type otherKindJob struct{ *timerJob }

func (o *otherKindJob) Kind() scheduler.JobKind { return "other.kind" }
func (o *otherKindJob) NextRun() time.Time      { return time.Time{} }

// TestJobStoreSave locks jobStore.Save's real write path (ADR-0134 B1):
// type-assert the incoming scheduler.ScheduledJob to recover its typed
// kernel.JobSpec, guard against a foreign implementation or a job under a
// foreign Kind, and upsert the full descriptor via the driver's TimerWriter.
// A driver with no TimerWriter configured (no TimerStore wired) is a
// documented no-op.
func TestJobStoreSave(t *testing.T) {
	t.Parallel()

	buildSpecJob := func(kind engine.TimerKind) scheduler.ScheduledJob {
		spec := kernel.JobSpec{
			TimerID:    "i1-tm1",
			InstanceID: "i1",
			DefID:      "d1",
			DefVersion: 2,
			Trigger:    schedule.AfterDuration(time.Hour),
			NextRun:    time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC),
			Kind:       kind,
		}
		j := &timerJob{
			spec: spec,
			trig: scheduler.After(time.Hour),
			fn:   func(context.Context, scheduler.DataProvider) error { return nil },
			data: scheduler.NewEmptyDataProvider(),
		}
		return newScheduledTimerJob(j, spec.NextRun.Add(-time.Hour))
	}

	type testCase struct {
		name   string
		build  func(t *testing.T) (js *jobStore, sj scheduler.ScheduledJob, mts *kernel.MemTimerStore)
		assert func(t *testing.T, mts *kernel.MemTimerStore, err error)
	}

	cases := []testCase{
		{
			name: "foreign job implementation returns typed error",
			build: func(t *testing.T) (*jobStore, scheduler.ScheduledJob, *kernel.MemTimerStore) {
				mts := kernel.NewMemTimerStore()
				driver, err := NewProcessDriver(WithTimerStore(mts))
				require.NoError(t, err)
				foreign, err := scheduler.NewJobWithID("foreign-1", timerJobKind, scheduler.At(time.Now()),
					func(context.Context, scheduler.DataProvider) error { return nil }, scheduler.NewEmptyDataProvider())
				require.NoError(t, err)
				sj, err := scheduler.NewScheduledJob(foreign, time.Now())
				require.NoError(t, err)
				return driver.jobStore, sj, mts
			},
			assert: func(t *testing.T, mts *kernel.MemTimerStore, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "unexpected job implementation")
				armed, lerr := mts.ListArmed(t.Context())
				require.NoError(t, lerr)
				assert.Empty(t, armed, "a rejected foreign job must not be written")
			},
		},
		{
			name: "job under a foreign Kind returns typed error",
			build: func(t *testing.T) (*jobStore, scheduler.ScheduledJob, *kernel.MemTimerStore) {
				mts := kernel.NewMemTimerStore()
				driver, err := NewProcessDriver(WithTimerStore(mts))
				require.NoError(t, err)
				j := &timerJob{
					spec: kernel.JobSpec{TimerID: "i1-tm1", InstanceID: "i1"},
					trig: scheduler.After(time.Hour),
					fn:   func(context.Context, scheduler.DataProvider) error { return nil },
					data: scheduler.NewEmptyDataProvider(),
				}
				sj := &otherKindJob{timerJob: j}
				return driver.jobStore, sj, mts
			},
			assert: func(t *testing.T, mts *kernel.MemTimerStore, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "unexpected job implementation")
				armed, lerr := mts.ListArmed(t.Context())
				require.NoError(t, lerr)
				assert.Empty(t, armed)
			},
		},
		{
			name: "successful save writes the full descriptor including Kind",
			build: func(t *testing.T) (*jobStore, scheduler.ScheduledJob, *kernel.MemTimerStore) {
				mts := kernel.NewMemTimerStore()
				driver, err := NewProcessDriver(WithTimerStore(mts))
				require.NoError(t, err)
				return driver.jobStore, buildSpecJob(engine.TimerDeadline), mts
			},
			assert: func(t *testing.T, mts *kernel.MemTimerStore, err error) {
				require.NoError(t, err)
				armed, lerr := mts.ListArmed(t.Context())
				require.NoError(t, lerr)
				require.Len(t, armed, 1)
				got := armed[0]
				assert.Equal(t, "i1-tm1", got.TimerID)
				assert.Equal(t, "i1", got.InstanceID)
				assert.Equal(t, "d1", got.DefID)
				assert.Equal(t, 2, got.DefVersion)
				assert.Equal(t, schedule.AfterDuration(time.Hour), got.Trigger)
				assert.True(t, got.NextRun.Equal(time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)))
				assert.Equal(t, engine.TimerDeadline, got.Kind)
			},
		},
		{
			name: "nil TimerWriter is a documented no-op",
			build: func(t *testing.T) (*jobStore, scheduler.ScheduledJob, *kernel.MemTimerStore) {
				driver, err := NewProcessDriver()
				require.NoError(t, err)
				require.Nil(t, driver.timerWriter, "no TimerStore configured means no TimerWriter")
				return driver.jobStore, buildSpecJob(engine.TimerIntermediate), nil
			},
			assert: func(t *testing.T, _ *kernel.MemTimerStore, err error) {
				assert.NoError(t, err)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			js, sj, mts := tc.build(t)
			err := js.Save(t.Context(), sj)
			tc.assert(t, mts, err)
		})
	}
}

// TestJobStoreDelete locks jobStore.Delete: it removes the durable timer row
// identified by timer id ALONE (engine timer ids are globally unique) via
// TimerWriter.DeleteJobByTimerID, and is a documented no-op when no
// TimerWriter is configured.
func TestJobStoreDelete(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		build  func(t *testing.T) (js *jobStore, id string, mts *kernel.MemTimerStore)
		assert func(t *testing.T, mts *kernel.MemTimerStore, err error)
	}

	cases := []testCase{
		{
			name: "nil TimerWriter is a documented no-op",
			build: func(t *testing.T) (*jobStore, string, *kernel.MemTimerStore) {
				driver, err := NewProcessDriver()
				require.NoError(t, err)
				return driver.jobStore, "whatever-id", nil
			},
			assert: func(t *testing.T, _ *kernel.MemTimerStore, err error) {
				assert.NoError(t, err)
			},
		},
		{
			name: "removes the row identified by timer id alone",
			build: func(t *testing.T) (*jobStore, string, *kernel.MemTimerStore) {
				mts := kernel.NewMemTimerStore()
				mts.Arm(kernel.ArmedTimer{InstanceID: "i1", TimerID: "i1-tm1"})
				driver, err := NewProcessDriver(WithTimerStore(mts))
				require.NoError(t, err)
				return driver.jobStore, "i1-tm1", mts
			},
			assert: func(t *testing.T, mts *kernel.MemTimerStore, err error) {
				require.NoError(t, err)
				armed, lerr := mts.ListArmed(t.Context())
				require.NoError(t, lerr)
				assert.Empty(t, armed)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			js, id, mts := tc.build(t)
			err := js.Delete(t.Context(), id)
			tc.assert(t, mts, err)
		})
	}
}

// TestJobStoreDeleteTimer locks the runtime-internal deleteTimer helper: a
// PK-exact delete by (instanceID, timerID) via TimerWriter.DeleteJob, used by
// Task 11's Drive cancel path. It removes only the exact pair — a different
// instance's row sharing the same timer id is left untouched — and is a
// documented no-op when no TimerWriter is configured.
func TestJobStoreDeleteTimer(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		build  func(t *testing.T) (js *jobStore, instanceID, timerID string, mts *kernel.MemTimerStore)
		assert func(t *testing.T, mts *kernel.MemTimerStore, err error)
	}

	cases := []testCase{
		{
			name: "nil TimerWriter is a documented no-op",
			build: func(t *testing.T) (*jobStore, string, string, *kernel.MemTimerStore) {
				driver, err := NewProcessDriver()
				require.NoError(t, err)
				return driver.jobStore, "i1", "i1-tm1", nil
			},
			assert: func(t *testing.T, _ *kernel.MemTimerStore, err error) {
				assert.NoError(t, err)
			},
		},
		{
			name: "removes only the exact (instanceID, timerID) pair",
			build: func(t *testing.T) (*jobStore, string, string, *kernel.MemTimerStore) {
				mts := kernel.NewMemTimerStore()
				mts.Arm(kernel.ArmedTimer{InstanceID: "i1", TimerID: "shared-timer"})
				mts.Arm(kernel.ArmedTimer{InstanceID: "i2", TimerID: "shared-timer"})
				driver, err := NewProcessDriver(WithTimerStore(mts))
				require.NoError(t, err)
				return driver.jobStore, "i1", "shared-timer", mts
			},
			assert: func(t *testing.T, mts *kernel.MemTimerStore, err error) {
				require.NoError(t, err)
				armed, lerr := mts.ListArmed(t.Context())
				require.NoError(t, lerr)
				require.Len(t, armed, 1)
				assert.Equal(t, "i2", armed[0].InstanceID,
					"must remove only i1's row, leaving i2's same-timer-id row intact")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			js, instanceID, timerID, mts := tc.build(t)
			err := js.deleteTimer(t.Context(), instanceID, timerID)
			tc.assert(t, mts, err)
		})
	}
}

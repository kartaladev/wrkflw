package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	gocronsched "github.com/kartaladev/wrkflw/scheduler/internal/gocron"
)

// activateJobFakeData is a minimal DataProvider for a job that never actually
// fires in this test — activateJob only needs a Job to read Trigger/Action/
// Data/ID/singleton from before handing off to the (already-closed) impl.
type activateJobFakeData struct{}

func (activateJobFakeData) Get(context.Context) (map[string]any, error) { return nil, nil }
func (activateJobFakeData) Static() bool                                { return true }

// TestActivateJob_ImplClosedFacadeOpen_ReturnsPublicSentinel reproduces the
// graceful-shutdown race: Activate/Schedule pass the façade's own
// s.closed check in ensureStarted, then release s.mu and return the live
// impl; a concurrent Close (or ctx-cancel) can set impl.closed before
// activateJob calls impl.ScheduleJob. That call returns the INTERNAL
// package's ErrSchedulerClosed sentinel — a distinct value from the public
// scheduler.ErrSchedulerClosed both Schedule and Activate document
// returning, so errors.Is(err, scheduler.ErrSchedulerClosed) must hold even
// though the façade's own s.closed is never set to true in this scenario.
func TestActivateJob_ImplClosedFacadeOpen_ReturnsPublicSentinel(t *testing.T) {
	impl, err := gocronsched.NewGocronScheduler()
	require.NoError(t, err)
	require.NoError(t, impl.Close()) // impl.closed = true; s.closed (façade) stays false

	j, err := NewJob(JobKind("demo"), After(time.Minute), func(context.Context, DataProvider) error { return nil }, activateJobFakeData{})
	require.NoError(t, err)
	sj, err := NewScheduledJob(j, time.Now())
	require.NoError(t, err)

	s := &NativeScheduler{}
	err = s.activateJob(t.Context(), impl, sj)
	require.Error(t, err)
	require.Truef(t, errors.Is(err, ErrSchedulerClosed),
		"activateJob's error must satisfy errors.Is(err, scheduler.ErrSchedulerClosed); got %v", err)
}

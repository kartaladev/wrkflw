package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/scheduler"
)

// TestTimerJob locks the runtime's concrete timer Job shape: it satisfies
// scheduler.Job with Manual activation under timerJobKind, descriptor()
// round-trips the kernel.JobSpec it was built from, and newScheduledTimerJob
// stamps NextRun = trig.Next(now).
func TestTimerJob(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	spec := kernel.JobSpec{
		TimerID:    "i1-tm1",
		InstanceID: "i1",
		DefID:      "d1",
		DefVersion: 3,
		Trigger:    schedule.AfterDuration(time.Hour),
		NextRun:    now.Add(time.Hour),
	}
	trig := scheduler.After(time.Hour)
	fn := func(context.Context, scheduler.DataProvider) error { return nil }
	data := scheduler.NewStaticDataProvider(map[string]any{"instance_id": "i1"})

	j := &timerJob{spec: spec, trig: trig, fn: fn, data: data}

	// timerJob is a scheduler.Job with Manual activation under timerJobKind.
	var asJob scheduler.Job = j
	assert.Equal(t, "i1-tm1", asJob.ID(), "job id must be the engine timer id")
	assert.Equal(t, timerJobKind, asJob.Kind())
	assert.Equal(t, scheduler.ActivationManual, asJob.Activation(),
		"runtime timer jobs are Manual: persisted in-tx, armed post-commit")
	assert.NotNil(t, asJob.Action())
	assert.Equal(t, data, asJob.Data())

	// descriptor round-trips the typed kernel.JobSpec.
	assert.Equal(t, spec, j.descriptor())

	// newScheduledTimerJob computes NextRun = trig.Next(now).
	sj := newScheduledTimerJob(j, now)
	wantNext, ok := trig.Next(now)
	require.True(t, ok)
	assert.True(t, sj.NextRun().Equal(wantNext),
		"scheduledTimerJob.NextRun must be trig.Next(now): want %v got %v", wantNext, sj.NextRun())

	// The wrapper still IS the job (ScheduledJob embeds Job).
	var asScheduled scheduler.ScheduledJob = sj
	assert.Equal(t, "i1-tm1", asScheduled.ID())
	assert.Equal(t, timerJobKind, asScheduled.Kind())
}

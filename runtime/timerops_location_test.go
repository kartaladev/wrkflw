package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/kartaladev/wrkflw/scheduler"
)

// TestNewScheduledTimerJob_CalendarNextRunHonorsNowLocation verifies that
// newScheduledTimerJob resolves NextRun in the location of the now it is given
// (ADR-0137); the runtime passes now.In(scheduler location) at its call sites.
func TestNewScheduledTimerJob_CalendarNextRunHonorsNowLocation(t *testing.T) {
	plusTwo := time.FixedZone("plusTwo", 2*60*60)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, plusTwo)
	j := &timerJob{trig: scheduler.Daily(1, scheduler.ClockTime{Hour: 9})}

	sj := newScheduledTimerJob(j, now)

	// 09:00 in +02:00 == 07:00 UTC.
	want := time.Date(2026, 1, 1, 7, 0, 0, 0, time.UTC)
	assert.True(t, sj.NextRun().UTC().Equal(want), "want %s UTC, got %s", want, sj.NextRun().UTC())
}

// locFake is a local scheduler.Scheduler double used only to give a fixed
// Location() answer for schedulingLocation's capability-detection test; every
// other Scheduler method is inherited (nil-embedded, unused).
type locFake struct {
	scheduler.Scheduler
	loc *time.Location
}

func (f locFake) Location() *time.Location { return f.loc }

// TestSchedulingLocation verifies driver.schedulingLocation resolves the
// runtime's compute location from the driver's scheduler: the capability zone
// when the scheduler reports one, or time.UTC when it does not (ADR-0137).
func TestSchedulingLocation(t *testing.T) {
	plusThree := time.FixedZone("plusThree", 3*60*60)
	cases := []struct {
		name   string
		sched  scheduler.Scheduler
		assert func(t *testing.T, got *time.Location)
	}{
		{
			name:  "capability reports zone",
			sched: locFake{loc: plusThree},
			assert: func(t *testing.T, got *time.Location) {
				t.Helper()
				assert.Equal(t, plusThree, got)
			},
		},
		{
			name:  "no capability defaults UTC",
			sched: nil,
			assert: func(t *testing.T, got *time.Location) {
				t.Helper()
				assert.Equal(t, time.UTC, got)
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := &ProcessDriver{sched: c.sched}
			c.assert(t, d.schedulingLocation())
		})
	}
}

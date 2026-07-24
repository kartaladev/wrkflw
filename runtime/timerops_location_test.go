package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/kartaladev/wrkflw/scheduler"
)

// The reported NextRun for a calendar trigger is the UTC reference, matching
// the default (UTC) live scheduler (ADR-0136).
func TestNewScheduledTimerJob_CalendarNextRunIsUTC(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("plusTwo", 2*60*60))
	j := &timerJob{trig: scheduler.Daily(1, scheduler.ClockTime{Hour: 9})}

	sj := newScheduledTimerJob(j, now)

	want := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	assert.True(t, sj.NextRun().UTC().Equal(want),
		"want %s UTC, got %s", want, sj.NextRun().UTC())
}

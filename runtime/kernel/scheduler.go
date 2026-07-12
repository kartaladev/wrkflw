package kernel

import (
	"context"
	"errors"
	"time"

	"github.com/kartaladev/wrkflw/definition/schedule"
)

// ErrUnsupportedTrigger is returned by a [Scheduler.Schedule] call when the
// implementation cannot honour the given [schedule.TriggerSpec] kind (e.g. an
// in-memory test scheduler that only understands one-shot and fixed-interval
// triggers, asked to schedule a cron or calendar trigger).
var ErrUnsupportedTrigger = errors.New("workflow-scheduler: trigger kind not supported by this scheduler")

// Scheduler is the port through which the runtime registers and cancels timers.
// Implementations may be in-memory (for tests, see processtest.MemScheduler),
// gocron-backed (production), or any other backing store.
//
// A timer is identified by an opaque timerID; scheduling a second timer with an
// existing timerID replaces the first.
type Scheduler interface {
	// Schedule registers a timer with the given timerID whose firing schedule is
	// described by trig, invoking fire when it becomes due. It returns the next
	// computed run time, or ErrUnsupportedTrigger if the implementation cannot
	// honour the trigger kind. If a timer with the same timerID already exists it
	// is replaced.
	Schedule(ctx context.Context, timerID string, trig schedule.TriggerSpec, fire func()) (nextRun time.Time, err error)

	// Cancel removes a pending timer. It is a no-op if the timer does not exist
	// or has already fired.
	Cancel(ctx context.Context, timerID string)

	// NextRun returns the next scheduled run time of the timer with the given id
	// and true, or the zero time and false when no such timer is pending.
	NextRun(timerID string) (time.Time, bool)
}

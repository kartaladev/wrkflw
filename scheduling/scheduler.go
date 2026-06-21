// Package scheduling is the consumer-facing façade over the internal gocron
// scheduler (ADR-0008, ADR-0009). Consumers import only this root package;
// the concrete gocron implementation stays in internal/scheduling/gocron so
// the vendor dependency is not visible to the library API surface.
package scheduling

import (
	"io"
	"time"

	"github.com/jonboulle/clockwork"

	gocronsched "github.com/zakyalvan/krtlwrkflw/internal/scheduling/gocron"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// Scheduler is the production, gocron-backed [runtime.Scheduler]. Construct it
// with [NewScheduler], passing the same [clockwork.Clock] instance used to
// build the runtime so one fake-clock advance drives both engine timestamps and
// timer firing under test (ADR-0003). Call [Close] on shutdown to release the
// underlying gocron goroutine.
type Scheduler struct {
	impl *gocronsched.GocronScheduler
}

// Compile-time contract assertions.
var (
	_ runtime.Scheduler = (*Scheduler)(nil)
	_ io.Closer         = (*Scheduler)(nil)
)

// NewScheduler constructs and starts a gocron-backed [Scheduler] driven by
// clk. The returned scheduler must be closed via [Scheduler.Close] when the
// application shuts down.
func NewScheduler(clk clockwork.Clock) (*Scheduler, error) {
	impl, err := gocronsched.NewGocronScheduler(clk)
	if err != nil {
		return nil, err
	}
	return &Scheduler{impl: impl}, nil
}

// Schedule registers a one-time timer identified by timerID that calls fire at
// or after fireAt. If a timer with the same timerID already exists it is
// replaced.
func (s *Scheduler) Schedule(timerID string, fireAt time.Time, fire func()) {
	s.impl.Schedule(timerID, fireAt, fire)
}

// Cancel removes a pending timer. No-op if the timer is unknown or has already
// fired.
func (s *Scheduler) Cancel(timerID string) {
	s.impl.Cancel(timerID)
}

// Close shuts the underlying gocron scheduler down gracefully. The scheduler
// cannot be reused after this call.
func (s *Scheduler) Close() error {
	return s.impl.Close()
}

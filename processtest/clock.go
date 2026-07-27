package processtest

import (
	"time"

	"github.com/jonboulle/clockwork"
)

// FakeClock is a manually-advanced clock for deterministic tests. It embeds a
// clockwork.FakeClock (so it satisfies clockwork.Clock and drives fake tickers,
// timers, and after-channels) and adds Set for absolute jumps. A Harness shares
// one FakeClock between its driver and scheduler.
type FakeClock struct {
	*clockwork.FakeClock
}

// Compile-time assertion.
var _ clockwork.Clock = (*FakeClock)(nil)

// NewFakeClock returns a FakeClock positioned at start.
func NewFakeClock(start time.Time) *FakeClock {
	return &FakeClock{clockwork.NewFakeClockAt(start)}
}

// Set jumps Now to t (forward or backward) by advancing the embedded fake by the
// delta. Forward jumps fire any timers/tickers scheduled in the interval.
func (c *FakeClock) Set(t time.Time) {
	c.Advance(t.Sub(c.Now()))
}

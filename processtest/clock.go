package processtest

import (
	"sync"
	"time"

	"github.com/zakyalvan/krtlwrkflw/clock"
)

// FakeClock is a manually-advanced [clock.Clock] for deterministic tests. It
// holds a single instant that only changes when the test calls [FakeClock.Advance]
// or [FakeClock.Set], so timers driven by a [kernel.MemScheduler] fire exactly
// when the test decides.
//
// A [Harness] shares one FakeClock between its driver and scheduler; construct a
// standalone one with [NewFakeClock] when wiring a driver by hand. FakeClock is
// safe for concurrent use.
//
// It intentionally implements only [clock.Clock] (Now) — the engine never reads
// wall-clock time and the MemScheduler needs nothing more — so the harness keeps
// its public API free of any third-party clock dependency.
type FakeClock struct {
	mu  sync.Mutex
	now time.Time
}

// Compile-time assertion.
var _ clock.Clock = (*FakeClock)(nil)

// NewFakeClock returns a FakeClock positioned at start.
func NewFakeClock(start time.Time) *FakeClock {
	return &FakeClock{now: start}
}

// Now returns the clock's current instant.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward by d. A non-positive d leaves the clock
// unchanged.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d > 0 {
		c.now = c.now.Add(d)
	}
}

// Set jumps the clock to t (forward or backward).
func (c *FakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

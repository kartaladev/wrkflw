package runtimetest

import "time"

// RecordingScheduler is a Scheduler stub that records the first scheduled fire-at
// time WITHOUT firing the callback. This lets tests inspect the jittered delay
// without triggering retry loops. It satisfies kernel.Scheduler.
type RecordingScheduler struct {
	FireAt    time.Time
	Scheduled bool
}

// Schedule records the fire-at time and marks the scheduler as invoked. It never
// runs the callback.
func (s *RecordingScheduler) Schedule(_ string, at time.Time, _ func()) {
	s.FireAt = at
	s.Scheduled = true
}

// Cancel is a no-op.
func (s *RecordingScheduler) Cancel(string) {}

// FixedJitter is a deterministic JitterSource that always returns the same
// fraction. It satisfies kernel.JitterSource.
type FixedJitter struct{ F float64 }

// Fraction returns the fixed fraction.
func (j FixedJitter) Fraction() float64 { return j.F }

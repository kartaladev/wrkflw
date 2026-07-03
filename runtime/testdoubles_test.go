package runtime_test

// Shared test doubles for the root runtime_test package. These mirror doubles of
// the same name in runtime/kernel's tests (Go test doubles are package-scoped);
// keep the two copies in sync when editing.

import "time"

// recordingScheduler is a Scheduler stub that records the first scheduled fire-at
// time WITHOUT firing the callback. This lets us inspect the jittered delay without
// triggering retry loops.
type recordingScheduler struct {
	fireAt    time.Time
	scheduled bool
}

func (s *recordingScheduler) Schedule(_ string, at time.Time, _ func()) {
	s.fireAt = at
	s.scheduled = true
}

func (s *recordingScheduler) Cancel(string) {}

// fixedJitter is a deterministic JitterSource that always returns the same fraction.
type fixedJitter struct{ f float64 }

func (j fixedJitter) Fraction() float64 { return j.f }

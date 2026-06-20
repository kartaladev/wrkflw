// Package clock is the engine's sole time abstraction (ADR-0003). Stateful
// components depend on Clock rather than calling time.Now() or importing a time
// vendor; clockwork.Clock satisfies this interface structurally.
package clock

import "time"

// Clock reports the current time.
type Clock interface {
	Now() time.Time
}

// System returns a Clock backed by the standard library.
func System() Clock { return systemClock{} }

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

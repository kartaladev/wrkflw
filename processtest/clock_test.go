package processtest_test

import (
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"

	"github.com/kartaladev/wrkflw/processtest"
)

var clockBase = time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)

func TestFakeClock(t *testing.T) {
	t.Parallel()

	fc := processtest.NewFakeClock(clockBase)

	// Satisfies clockwork.Clock.
	var _ clockwork.Clock = fc

	assert.Equal(t, clockBase, fc.Now(), "Now returns the base time")

	fc.Advance(90 * time.Minute)
	assert.Equal(t, clockBase.Add(90*time.Minute), fc.Now(), "Advance moves Now forward")

	target := time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC)
	fc.Set(target)
	assert.Equal(t, target, fc.Now(), "Set jumps Now to the given instant")
}

package clock_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/kartaladev/wrkflw/clock"
)

func TestSystemClockNow(t *testing.T) {
	c := clock.System()
	before := time.Now()
	got := c.Now()
	after := time.Now()

	assert.False(t, got.Before(before), "Now() should not precede the call")
	assert.False(t, got.After(after), "Now() should not exceed the call")
}

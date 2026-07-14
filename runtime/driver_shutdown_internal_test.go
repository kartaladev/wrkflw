package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdmitGate(t *testing.T) {
	driver, err := NewProcessDriver()
	require.NoError(t, err)
	t.Cleanup(func() { _ = driver.Shutdown(t.Context()) })

	release, ok := driver.admit()
	require.True(t, ok, "admit must succeed before draining")
	release()

	assert.False(t, driver.IsShuttingDown(), "not draining yet")
	driver.draining.Store(true)
	assert.True(t, driver.IsShuttingDown(), "draining now")

	_, ok = driver.admit()
	assert.False(t, ok, "admit must fail once draining")

	// reserveInternal ignores the draining flag (continuations must proceed).
	rel := driver.reserveInternal()
	rel()
}

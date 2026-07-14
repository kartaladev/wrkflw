package runtime

import (
	"context"
	"testing"
	"time"

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

func TestEffectiveShutdownCtx(t *testing.T) {
	t.Run("ctx deadline wins over option", func(t *testing.T) {
		driver, err := NewProcessDriver(WithShutdownTimeout(time.Hour))
		require.NoError(t, err)
		t.Cleanup(func() { _ = driver.Shutdown(t.Context()) })

		parent, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		got, done := driver.effectiveShutdownCtx(parent)
		defer done()
		dl, ok := got.Deadline()
		require.True(t, ok)
		assert.WithinDuration(t, time.Now().Add(5*time.Second), dl, time.Second)
	})

	t.Run("option applies when ctx has no deadline", func(t *testing.T) {
		driver, err := NewProcessDriver(WithShutdownTimeout(2 * time.Second))
		require.NoError(t, err)
		t.Cleanup(func() { _ = driver.Shutdown(t.Context()) })

		got, done := driver.effectiveShutdownCtx(context.Background())
		defer done()
		_, ok := got.Deadline()
		assert.True(t, ok, "fallback must impose a deadline")
	})

	t.Run("unbounded when neither set", func(t *testing.T) {
		driver, err := NewProcessDriver()
		require.NoError(t, err)
		t.Cleanup(func() { _ = driver.Shutdown(t.Context()) })

		got, done := driver.effectiveShutdownCtx(context.Background())
		defer done()
		_, ok := got.Deadline()
		assert.False(t, ok, "no deadline, no fallback => unbounded")
	})
}

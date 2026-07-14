package runtime

import (
	"context"
	"sync"
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
}

// TestAdmitConcurrentWithShutdown exercises many admit() calls racing a Shutdown to
// guard against the WaitGroup Add/Wait data race: admit's draining-check+inflight.Add
// must be mutually exclusive with Shutdown's draining-set (via gateMu) so no admit Add
// can land concurrently with waitInflight's Wait. The race is prevented BY CONSTRUCTION
// (gateMu mutual exclusion), not merely unobserved — this test cannot deterministically
// force the interleaving, but it must run clean under -race and never panic with
// "sync: WaitGroup misuse: Add called concurrently with Wait".
func TestAdmitConcurrentWithShutdown(t *testing.T) {
	driver, err := NewProcessDriver()
	require.NoError(t, err)

	const workers, iterations = 64, 200
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for range iterations {
				if release, ok := driver.admit(); ok {
					release()
				}
			}
		}()
	}

	// Race a Shutdown against the admit storm; once it returns, admit must reject.
	require.NoError(t, driver.Shutdown(context.Background()))
	wg.Wait()

	_, ok := driver.admit()
	assert.False(t, ok, "admit must reject after Shutdown has drained")
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

// Package gocron — white-box telemetry-injection tests.
// These tests sit in package gocron (not gocron_test) so they can inspect the
// unexported tel field directly, which lets us assert injection without
// needing to trigger a live error path.
package gocron

import (
	"log/slog"
	"testing"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWithLogger_FieldInjection verifies that WithLogger stages the logger
// into the telemetry and that the default (no option) uses slog.Default().
func TestWithLogger_FieldInjection(t *testing.T) {
	// Sub-test 1: default (no option).
	t.Run("default uses slog.Default()", func(t *testing.T) {
		clk := clockwork.NewFakeClock()
		s, err := NewGocronScheduler(clk)
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		assert.Same(t, slog.Default(), s.tel.Logger,
			"scheduler constructed with no logger option must use slog.Default()")
	})

	// Sub-test 2: WithLogger injects a custom logger.
	t.Run("WithLogger sets injected logger", func(t *testing.T) {
		custom := slog.New(slog.Default().Handler())
		clk := clockwork.NewFakeClock()
		s, err := NewGocronScheduler(clk, WithLogger(custom))
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		assert.Same(t, custom, s.tel.Logger,
			"scheduler must store the injected logger pointer")
	})
}

// TestWithLogger_NilIgnored verifies that passing a nil logger to WithLogger is
// a no-op (the scheduler falls back to slog.Default()).
func TestWithLogger_NilIgnored(t *testing.T) {
	clk := clockwork.NewFakeClock()
	s, err := NewGocronScheduler(clk, WithLogger(nil))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	assert.Equal(t, slog.Default(), s.tel.Logger,
		"nil logger option must be ignored; default should remain slog.Default()")
}

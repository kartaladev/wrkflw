// Package gocron — white-box logger-injection tests.
// These tests sit in package gocron (not gocron_test) so they can inspect the
// unexported logger field directly, which lets us assert injection without
// needing to trigger a live error path.
package gocron

import (
	"log/slog"
	"testing"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWithLogger_FieldInjection verifies that WithLogger sets the logger field
// and that the default (no option) uses slog.Default().
func TestWithLogger_FieldInjection(t *testing.T) {
	type tc struct {
		name   string
		assert func(t *testing.T, s *GocronScheduler)
	}

	cases := []tc{
		{
			name: "default uses slog.Default()",
			assert: func(t *testing.T, s *GocronScheduler) {
				assert.Equal(t, slog.Default(), s.logger,
					"default logger should be slog.Default()")
			},
		},
		{
			name: "WithLogger sets injected logger",
			assert: func(t *testing.T, s *GocronScheduler) {
				// This case provides a custom logger; the scheduler must use it.
				// The actual scheduler in this sub-test has a custom logger set
				// (see setup below).
				assert.NotEqual(t, slog.Default(), s.logger,
					"injected logger should differ from slog.Default()")
				assert.NotNil(t, s.logger, "logger must never be nil")
			},
		},
	}

	// Sub-test 1: default (no option).
	t.Run(cases[0].name, func(t *testing.T) {
		clk := clockwork.NewFakeClock()
		s, err := NewGocronScheduler(clk)
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		cases[0].assert(t, s)
	})

	// Sub-test 2: injected logger differs from slog.Default().
	t.Run(cases[1].name, func(t *testing.T) {
		// Build a distinct logger so we can compare identity.
		custom := slog.New(slog.Default().Handler())
		clk := clockwork.NewFakeClock()
		s, err := NewGocronScheduler(clk, WithLogger(custom))
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		// Override what the "assert" checks to compare with the same custom value.
		assert.Same(t, custom, s.logger, "scheduler must store the injected logger pointer")
	})
}

// TestWithLogger_NilIgnored verifies that passing a nil logger to WithLogger is
// a no-op (the scheduler falls back to slog.Default()).
func TestWithLogger_NilIgnored(t *testing.T) {
	clk := clockwork.NewFakeClock()
	s, err := NewGocronScheduler(clk, WithLogger(nil))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	assert.Equal(t, slog.Default(), s.logger,
		"nil logger option must be ignored; default should remain slog.Default()")
}

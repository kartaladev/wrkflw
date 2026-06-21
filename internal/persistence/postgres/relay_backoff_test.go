package postgres_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	pg "github.com/zakyalvan/krtlwrkflw/internal/persistence/postgres"
)

// TestRelayBackoff verifies the capped exponential backoff schedule used to
// space out retries of a failing outbox row: min(base * 2^retryCount, max), with
// defensive handling of non-positive base and negative attempts.
func TestRelayBackoff(t *testing.T) {
	t.Parallel()

	const (
		base        = time.Second
		maxInterval = time.Minute
	)

	type testCase struct {
		name        string
		retryCount  int
		base        time.Duration
		maxInterval time.Duration
		assert      func(t *testing.T, got time.Duration)
	}

	cases := []testCase{
		{
			name:        "attempt 0 yields base",
			retryCount:  0,
			base:        base,
			maxInterval: maxInterval,
			assert: func(t *testing.T, got time.Duration) {
				assert.Equal(t, time.Second, got)
			},
		},
		{
			name:        "attempt 3 doubles three times",
			retryCount:  3,
			base:        base,
			maxInterval: maxInterval,
			assert: func(t *testing.T, got time.Duration) {
				assert.Equal(t, 8*time.Second, got)
			},
		},
		{
			name:        "large attempt is capped at max",
			retryCount:  100,
			base:        base,
			maxInterval: maxInterval,
			assert: func(t *testing.T, got time.Duration) {
				assert.Equal(t, time.Minute, got)
			},
		},
		{
			name:        "delay exactly reaching max is capped",
			retryCount:  6, // 1s * 2^6 = 64s > 60s cap
			base:        base,
			maxInterval: maxInterval,
			assert: func(t *testing.T, got time.Duration) {
				assert.Equal(t, time.Minute, got)
			},
		},
		{
			name:        "non-positive base yields zero",
			retryCount:  5,
			base:        0,
			maxInterval: maxInterval,
			assert: func(t *testing.T, got time.Duration) {
				assert.Equal(t, time.Duration(0), got)
			},
		},
		{
			name:        "negative attempt yields zero",
			retryCount:  -1,
			base:        base,
			maxInterval: maxInterval,
			assert: func(t *testing.T, got time.Duration) {
				assert.Equal(t, time.Duration(0), got)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := pg.RelayBackoff(tc.retryCount, tc.base, tc.maxInterval)
			tc.assert(t, got)
		})
	}
}

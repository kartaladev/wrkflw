package persistence_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/persistence"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	rest "github.com/zakyalvan/krtlwrkflw/transport/rest"
)

// fakeStatsReader is an in-test fake implementing kernel.OutboxStatsReader.
type fakeStatsReader struct {
	stats kernel.OutboxStats
	err   error
}

func (f fakeStatsReader) OutboxStats(_ context.Context) (kernel.OutboxStats, error) {
	return f.stats, f.err
}

func TestRelayBacklogCheck(t *testing.T) {
	t.Parallel()

	readerErr := errors.New("db unavailable")

	type testCase struct {
		name   string
		reader kernel.OutboxStatsReader
		opts   []persistence.RelayBacklogOption
		ctx    func(ctx context.Context) context.Context // nil → identity
		assert func(t *testing.T, err error)
	}

	cases := []testCase{
		{
			name:   "name is relay-backlog",
			reader: fakeStatsReader{stats: kernel.OutboxStats{Pending: 0, Dead: 0}},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name:   "both thresholds disabled (0): dead=999 pending=999 never fails",
			reader: fakeStatsReader{stats: kernel.OutboxStats{Pending: 999, Dead: 999}},
			// no WithMaxDead / WithMaxPending → defaults 0 = disabled
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name:   "under maxDead threshold: ok",
			reader: fakeStatsReader{stats: kernel.OutboxStats{Dead: 4}},
			opts:   []persistence.RelayBacklogOption{persistence.WithMaxDead(5)},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name:   "dead equals maxDead: ok (boundary)",
			reader: fakeStatsReader{stats: kernel.OutboxStats{Dead: 5}},
			opts:   []persistence.RelayBacklogOption{persistence.WithMaxDead(5)},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name:   "dead exceeds maxDead: error",
			reader: fakeStatsReader{stats: kernel.OutboxStats{Dead: 6}},
			opts:   []persistence.RelayBacklogOption{persistence.WithMaxDead(5)},
			assert: func(t *testing.T, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "workflow-")
				assert.Contains(t, err.Error(), "dead")
			},
		},
		{
			name:   "under maxPending threshold: ok",
			reader: fakeStatsReader{stats: kernel.OutboxStats{Pending: 99}},
			opts:   []persistence.RelayBacklogOption{persistence.WithMaxPending(100)},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name:   "pending equals maxPending: ok (boundary)",
			reader: fakeStatsReader{stats: kernel.OutboxStats{Pending: 100}},
			opts:   []persistence.RelayBacklogOption{persistence.WithMaxPending(100)},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name:   "pending exceeds maxPending: error",
			reader: fakeStatsReader{stats: kernel.OutboxStats{Pending: 101}},
			opts:   []persistence.RelayBacklogOption{persistence.WithMaxPending(100)},
			assert: func(t *testing.T, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "workflow-")
				assert.Contains(t, err.Error(), "pending")
			},
		},
		{
			name:   "both dead and pending exceeded: error",
			reader: fakeStatsReader{stats: kernel.OutboxStats{Dead: 10, Pending: 200}},
			opts: []persistence.RelayBacklogOption{
				persistence.WithMaxDead(5),
				persistence.WithMaxPending(100),
			},
			assert: func(t *testing.T, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "workflow-")
			},
		},
		{
			name:   "reader returns error: Check returns that error",
			reader: fakeStatsReader{err: readerErr},
			assert: func(t *testing.T, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "workflow-")
				assert.ErrorIs(t, err, readerErr)
			},
		},
		{
			name:   "cancelled context is honoured",
			reader: fakeStatsReader{stats: kernel.OutboxStats{}, err: context.Canceled},
			ctx: func(ctx context.Context) context.Context {
				cctx, cancel := context.WithTimeout(ctx, time.Millisecond)
				cancel() // cancel immediately
				return cctx
			},
			assert: func(t *testing.T, err error) {
				require.Error(t, err)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			if tc.ctx != nil {
				ctx = tc.ctx(ctx)
			}

			check := persistence.NewRelayBacklogCheck(tc.reader, tc.opts...)

			// Must structurally satisfy rest.HealthCheck (compile-time assertion).
			var _ rest.HealthCheck = check

			if tc.name == "name is relay-backlog" {
				assert.Equal(t, "relay-backlog", check.Name())
			}

			tc.assert(t, check.Check(ctx))
		})
	}
}

// TestRelayBacklogCheckName is a standalone test confirming Name() is "relay-backlog".
func TestRelayBacklogCheckName(t *testing.T) {
	t.Parallel()

	check := persistence.NewRelayBacklogCheck(fakeStatsReader{})
	assert.Equal(t, "relay-backlog", check.Name())
}

package casbin

import (
	"context"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"
)

// TestPgWatcher_BackoffIsClockDriven proves pgWatcher.backoff's reconnect wait
// is routed through the injected clockwork.Clock: under a
// clockwork.FakeClock, no wall time passes, so only
// fc.Advance(watcherReconnectDelay) — not real time — releases it. A second
// case proves an already-cancelled context short-circuits the wait without
// ever touching the clock.
//
// This is a white-box test (package casbin) constructing the pgWatcher via a
// struct literal rather than newPGWatcher: newPGWatcher unconditionally
// spawns the listen goroutine, which immediately calls w.pool.Acquire(ctx) —
// with a nil pool (this test has no live Postgres connection) that panics
// with a nil-pointer dereference and would crash the whole test binary.
// backoff only reads w.clk and the ctx argument, so the struct literal
// isolates exactly the unit under test.
func TestPgWatcher_BackoffIsClockDriven(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		ctx    func(ctx context.Context) context.Context // nil means identity
		assert func(t *testing.T, fc *clockwork.FakeClock, done <-chan struct{})
	}

	cases := []testCase{
		{
			name: "unblocks only after the fake clock advances by the full backoff",
			assert: func(t *testing.T, fc *clockwork.FakeClock, done <-chan struct{}) {
				// Confirm the waiter is armed on w.clk.After before advancing — a
				// stdlib time.After would never be released by fc.Advance, so this
				// IS the assertion that the wait is clock-routed.
				require.NoError(t, fc.BlockUntilContext(t.Context(), 1))
				fc.Advance(watcherReconnectDelay)
				requireBackoffDone(t, done)
			},
		},
		{
			name: "returns immediately when ctx is already cancelled",
			ctx: func(ctx context.Context) context.Context {
				cctx, cancel := context.WithCancel(ctx)
				cancel()
				return cctx
			},
			assert: func(t *testing.T, _ *clockwork.FakeClock, done <-chan struct{}) {
				requireBackoffDone(t, done)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fc := clockwork.NewFakeClock()
			w := &pgWatcher{clk: fc}

			ctx := t.Context()
			if tc.ctx != nil {
				ctx = tc.ctx(ctx)
			}

			done := make(chan struct{})
			go func() {
				w.backoff(ctx)
				close(done)
			}()

			tc.assert(t, fc, done)
		})
	}
}

// requireBackoffDone waits for backoff's done signal with a generous bound
// that only trips if backoff never returns.
func requireBackoffDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("backoff did not return in time")
	}
}

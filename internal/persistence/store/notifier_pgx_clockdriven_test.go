package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/internal/persistence/store"
)

// TestPgxNotifier_BackoffIsClockDriven proves the pgxNotifier reconnect
// backoff wait (waitBackoff) is routed through the injected clockwork.Clock:
// under a clockwork.FakeClock, no wall time passes, so only
// fc.Advance(pgxNotifierReconnectBackoff) — not real time — can unblock it.
// A second case proves an already-cancelled context short-circuits the wait
// and returns ctx.Err() without waiting on the clock at all.
func TestPgxNotifier_BackoffIsClockDriven(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name      string
		ctxCancel bool
		assert    func(t *testing.T, err error)
	}

	cases := []testCase{
		{
			name: "unblocks only after the fake clock advances by the full backoff",
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		{
			name:      "returns ctx.Err immediately when ctx is already cancelled",
			ctxCancel: true,
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, context.Canceled)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fc := clockwork.NewFakeClock()
			// pool is nil: waitBackoff never touches the pool, only n.clk.
			notifier := store.NewPgxNotifier(nil, store.WithPgxNotifierClock(fc))

			ctx := t.Context()
			if tc.ctxCancel {
				cancelledCtx, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelledCtx
			}

			errCh := make(chan error, 1)
			go func() { errCh <- store.WaitBackoffForTest(notifier, ctx) }()

			if !tc.ctxCancel {
				// Confirm the waiter is armed on n.clk.After before advancing —
				// a stdlib time.After would never be released by fc.Advance, so
				// this IS the assertion that the wait is clock-routed.
				require.NoError(t, fc.BlockUntilContext(t.Context(), 1))
				fc.Advance(store.PgxNotifierReconnectBackoffForTest)
			}

			select {
			case err := <-errCh:
				tc.assert(t, err)
			case <-time.After(2 * time.Second):
				t.Fatal("waitBackoff did not return in time")
			}
		})
	}
}

package eventing_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/eventing"
)

// TestBaselineInProcessRefusesSubscriptionsAfterClose characterizes the
// event-delivery scenario "Calls after Close" (openspec/specs/event-delivery)
// for the rows the neighbouring Close tests leave unasserted: once the
// in-process bus is closed, Subscribe and Start return eventing.ErrBusClosed.
func TestBaselineInProcessRefusesSubscriptionsAfterClose(t *testing.T) {
	t.Parallel()

	noop := func(context.Context, eventing.Envelope) error { return nil }

	cases := []struct {
		name   string
		call   func(ctx context.Context, bus *eventing.InProcess) error
		assert func(t *testing.T, err error)
	}{
		{
			name: "Subscribe",
			call: func(ctx context.Context, bus *eventing.InProcess) error {
				return bus.Subscribe(ctx, eventing.TopicInstanceCompleted, noop)
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, eventing.ErrBusClosed)
			},
		},
		{
			name: "Start",
			call: func(ctx context.Context, bus *eventing.InProcess) error {
				stop, err := bus.Start(ctx, eventing.TopicInstanceCompleted, noop)
				if stop != nil {
					stop()
				}
				return err
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, eventing.ErrBusClosed)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			bus := eventing.NewInProcess()
			require.NoError(t, bus.Close())

			tc.assert(t, tc.call(t.Context(), bus))
		})
	}
}

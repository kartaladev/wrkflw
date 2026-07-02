package runtime

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/model"
)

// TestPerformInvokeActionFireAndForget verifies that perform() runs a
// FireAndForget InvokeAction for its side effect but returns NO trigger, while a
// regular InvokeAction returns an ActionCompleted trigger as before.
func TestPerformInvokeActionFireAndForget(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		fnf    bool
		assert func(t *testing.T, trg engine.Trigger, err error, ran *atomic.Bool)
	}

	cases := []testCase{
		{
			name: "fire-and-forget runs action but returns no trigger",
			fnf:  true,
			assert: func(t *testing.T, trg engine.Trigger, err error, ran *atomic.Bool) {
				require.NoError(t, err)
				assert.Nil(t, trg, "fire-and-forget must return no trigger to feed back")
				assert.True(t, ran.Load(), "fire-and-forget action must still run for its side effect")
			},
		},
		{
			name: "regular invoke returns ActionCompleted",
			fnf:  false,
			assert: func(t *testing.T, trg engine.Trigger, err error, ran *atomic.Bool) {
				require.NoError(t, err)
				require.NotNil(t, trg, "regular invoke must feed back a trigger")
				ac, ok := trg.(engine.ActionCompleted)
				require.True(t, ok, "expected ActionCompleted, got %T", trg)
				assert.Equal(t, "cmd-1", ac.CommandID)
				assert.True(t, ran.Load(), "regular action must run")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var ran atomic.Bool
			cat := action.NewMapCatalog(map[string]action.ServiceAction{
				"x": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
					ran.Store(true)
					return map[string]any{"ok": true}, nil
				}),
			})
			fc := clockwork.NewFakeClockAt(time.Date(2026, 6, 26, 10, 0, 0, 0, time.UTC))
			st, err := NewMemStore()
			require.NoError(t, err)
			r, err := NewRunner(cat, st, WithRunnerClock(fc))
			require.NoError(t, err)

			cmd := engine.InvokeAction{
				CommandID:     "cmd-1",
				Name:          "x",
				FireAndForget: tc.fnf,
			}
			trg, err := r.perform(t.Context(), &model.ProcessDefinition{}, engine.InstanceState{}, cmd)
			tc.assert(t, trg, err, &ran)
		})
	}
}

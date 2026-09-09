// Internal test for the recovery sweep's LEASE.
//
// The lease is only reachable through RunRecoverySweep's ticker, and driving a
// ticker to prove a time-based rule buys a scheduling race for no extra
// coverage. sweepPendingCommands takes the lease as a parameter precisely so it
// can be exercised directly, against a fake clock, with no waiting at all —
// which is also what docs/agents/eventually-waits.md and the per-package wait
// budget both prefer.
package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// markedInstance seeds store with one running instance carrying a
// pending-command mark stamped at markedAt, without going through a driver: the
// lease rule is about the mark's AGE, so manufacturing the mark directly keeps
// the test about that one rule.
func markedInstance(t *testing.T, store *kernel.MemInstanceStore, def *model.ProcessDefinition, id string, markedAt time.Time) {
	t.Helper()
	st := engine.InstanceState{
		InstanceID: id,
		DefID:      def.ID,
		DefVersion: def.Version,
		Status:     engine.StatusRunning,
		StartedAt:  markedAt,
		PendingCommands: []engine.PendingCommand{{
			Kind:          engine.PendingInvokeAction,
			CommandID:     "cmd-1",
			Name:          "lease-action",
			FireAndForget: true, // no token awaits it, so no follow-up trigger to apply
		}},
		PendingCommandsAt: markedAt,
	}
	_, err := store.Create(t.Context(), kernel.AppliedStep{
		State:   st,
		Trigger: engine.NewStartInstance(markedAt, nil),
	})
	require.NoError(t, err)
}

// TestRecoverySweepLeaseGatesPeriodicPasses pins the rule that keeps the
// periodic sweep from overtaking a perform that is still legitimately running,
// and the deliberate exception the boot pass takes to it.
func TestRecoverySweepLeaseGatesPeriodicPasses(t *testing.T) {
	t.Parallel()

	markedAt := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

	type testCase struct {
		name   string
		now    time.Time
		lease  time.Duration
		assert func(t *testing.T, recovered int, calls int, st engine.InstanceState)
	}

	cases := []testCase{
		{
			name:  "inside the lease the mark is left alone",
			now:   markedAt.Add(time.Minute),
			lease: 5 * time.Minute,
			assert: func(t *testing.T, recovered, calls int, st engine.InstanceState) {
				assert.Zero(t, recovered, "a mark younger than the lease must not be re-driven")
				assert.Zero(t, calls, "the action must not be invoked while the lease holds")
				assert.NotEmpty(t, st.PendingCommands, "the mark must survive a declined pass")
			},
		},
		{
			name:  "past the lease the mark is re-driven",
			now:   markedAt.Add(6 * time.Minute),
			lease: 5 * time.Minute,
			assert: func(t *testing.T, recovered, calls int, st engine.InstanceState) {
				assert.Equal(t, 1, recovered)
				assert.Equal(t, 1, calls, "the abandoned action must be invoked exactly once")
				assert.Empty(t, st.PendingCommands, "a re-driven mark must be cleared")
			},
		},
		{
			name:  "the boot pass ignores the lease entirely",
			now:   markedAt.Add(time.Minute),
			lease: 0, // what RecoverPendingCommands passes
			assert: func(t *testing.T, recovered, calls int, st engine.InstanceState) {
				assert.Equal(t, 1, recovered,
					"a mark found at boot cannot belong to a perform this process is running")
				assert.Equal(t, 1, calls)
				assert.Empty(t, st.PendingCommands)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			calls := 0
			def := &model.ProcessDefinition{
				ID: "lease-def", Version: 1,
				Nodes: []model.Node{
					event.NewStart("start"),
					activity.NewServiceTask("work", activity.WithTaskAction("lease-action")),
					event.NewEnd("end"),
				},
				Flows: []flow.SequenceFlow{
					{ID: "f1", Source: "start", Target: "work"},
					{ID: "f2", Source: "work", Target: "end"},
				},
			}
			cat := action.NewCatalog(map[string]action.Action{
				"lease-action": action.ActionFunc(func(context.Context, map[string]any) (map[string]any, error) {
					calls++
					return nil, nil
				}),
			})
			reg := kernel.NewMemDefinitionRegistry()
			require.NoError(t, reg.Register(def))

			store, err := kernel.NewMemInstanceStore()
			require.NoError(t, err)
			markedInstance(t, store, def, "i-lease", markedAt)

			driver, err := NewProcessDriver(
				WithActionCatalog(cat),
				WithInstanceStore(store),
				WithDefinitions(reg),
				WithClock(clockwork.NewFakeClockAt(tc.now)),
			)
			require.NoError(t, err)
			t.Cleanup(func() { _ = driver.Shutdown(context.Background()) })

			recovered, err := driver.sweepPendingCommands(ctx, tc.lease)
			require.NoError(t, err)

			st, _, err := store.Load(ctx, "i-lease")
			require.NoError(t, err)
			tc.assert(t, recovered, calls, st)
		})
	}
}

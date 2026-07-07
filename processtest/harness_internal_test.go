package processtest

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
)

// TestCompleteTasksWithSkipsDeclined is a white-box test for finding #7: when the
// leading open task is declined by decide, the handler must act on a later
// actionable task rather than passing (which would strand it via ErrUnhandledPark).
func TestCompleteTasksWithSkipsDeclined(t *testing.T) {
	t.Parallel()

	h, err := New()
	require.NoError(t, err)
	ctx := t.Context()

	taskA := humantask.HumanTask{TaskToken: "tkA", NodeID: "reviewA", State: humantask.Unclaimed}
	taskB := humantask.HumanTask{TaskToken: "tkB", NodeID: "reviewB", State: humantask.Unclaimed}
	require.NoError(t, h.tasks.Upsert(ctx, taskA))
	require.NoError(t, h.tasks.Upsert(ctx, taskB))

	// Decline reviewA; accept reviewB.
	decide := func(tsk humantask.HumanTask) (authz.Actor, map[string]any, bool) {
		if tsk.NodeID == "reviewA" {
			return authz.Actor{}, nil, false
		}
		return authz.Actor{ID: "bob"}, map[string]any{"ok": true}, true
	}
	handler := CompleteTasksWith(h.taskSvc, decide)

	t.Run("acts on the accepted task behind a declined one", func(t *testing.T) {
		d, err := handler(ctx, Park{Reason: ReasonHumanTask, OpenTasks: []humantask.HumanTask{taskA, taskB}})
		require.NoError(t, err)
		assert.Equal(t, kindDeliver, d.kind, "must claim the accepted task, not pass")
	})

	t.Run("passes when the only open task is declined", func(t *testing.T) {
		d, err := handler(ctx, Park{Reason: ReasonHumanTask, OpenTasks: []humantask.HumanTask{taskA}})
		require.NoError(t, err)
		assert.Equal(t, kindPass, d.kind)
	})
}

// TestHarnessEnvClassifyPrecise is a white-box test for the harness's timer
// enrichment (finding #4): a command-wait park is promoted to ReasonTimer only
// when THAT token's own AwaitCommand matches a pending scheduler timer — never
// because some unrelated timer happens to be armed.
func TestHarnessEnvClassifyPrecise(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	type testCase struct {
		name     string
		schedule map[string]time.Time // timer id -> fireAt
		token    engine.Token
		assert   func(t *testing.T, p Park)
	}

	cases := []testCase{
		{
			name:     "own timer id armed -> promoted to ReasonTimer",
			schedule: map[string]time.Time{"tm1": base.Add(time.Hour)},
			token:    engine.Token{ID: "t", NodeID: "wait", State: engine.TokenWaitingCommand, AwaitCommand: "tm1"},
			assert: func(t *testing.T, p Park) {
				assert.Equal(t, ReasonTimer, p.Reason)
				assert.True(t, p.HasArmedTimers)
				assert.Equal(t, "wait", p.Node)
			},
		},
		{
			name:     "unrelated timer armed -> stays async-child, not promoted",
			schedule: map[string]time.Time{"other-tm": base.Add(time.Hour)},
			token:    engine.Token{ID: "t", NodeID: "call", State: engine.TokenWaitingCommand, AwaitCommand: "child-cmd"},
			assert: func(t *testing.T, p Park) {
				assert.Equal(t, ReasonAsyncChild, p.Reason, "an unrelated pending timer must not promote an async-child park")
				assert.False(t, p.HasArmedTimers)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h, err := New(WithClockStart(base))
			require.NoError(t, err)
			for id, at := range tc.schedule {
				if _, err := h.sched.Schedule(t.Context(), id, schedule.At(at), func() {}); err != nil {
					t.Fatalf("Schedule(%q): %v", id, err)
				}
			}

			env := harnessEnv{h: h}
			p := env.classify(engine.InstanceState{
				Status: engine.StatusRunning,
				Tokens: []engine.Token{tc.token},
			})
			tc.assert(t, p)
		})
	}
}

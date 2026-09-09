package runtime_test

import (
	"context"
	"errors"
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
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/internal/runtimetest"
)

// typedApproveIn is a consumer's own input type. Count is an int, so a fractional
// variable cannot decode into it.
type typedApproveIn struct {
	Ref   string `json:"ref"`
	Count int    `json:"count"`
}

type typedApproveOut struct {
	Approved bool `json:"approved"`
}

func typedApprove(_ context.Context, in typedApproveIn) (typedApproveOut, error) {
	return typedApproveOut{Approved: in.Count > 0}, nil
}

// typedIdemIn is the escape hatch the WithStrictInput godoc recommends: it
// declares the engine's own stamp so a primary service task can use strict mode.
type typedIdemIn struct {
	Ref            string `json:"ref"`
	IdempotencyKey string `json:"_idempotencyKey"`
}

func typedIdemEcho(_ context.Context, in typedIdemIn) (typedApproveOut, error) {
	return typedApproveOut{Approved: in.IdempotencyKey != ""}, nil
}

// typedTaskDef builds start → task("t") → end.
func typedTaskDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "typed-test", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("task", activity.WithTaskAction("t")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "task"},
			{ID: "f2", Source: "task", Target: "end"},
		},
	}
}

// TestTypedActionDecodeFailureIsNonRetryable drives a real instance through the
// ProcessDriver (in-memory store, fake clock, no database) and asserts that an
// action.Typed input-decode failure survives the runtime's wrapping: the emitted
// engine.ActionFailed carries Retryable == false and a Cause that still satisfies
// errors.Is(cause, action.ErrDecodeInput).
//
// Every case runs under the SAME MaxAttempts=3 default retry policy, so the only
// variable is the error's retry classification. The last case is the control: a
// plain action error under that same policy DOES arm a retry timer, which is what
// makes "no timer armed" meaningful for the decode failures rather than vacuous.
func TestTypedActionDecodeFailureIsNonRetryable(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		act    action.Action
		vars   map[string]any
		assert func(t *testing.T, st engine.InstanceState, af engine.ActionFailed, armed bool)
	}

	cases := []testCase{
		{
			name: "fractional variable into an int field is non-retryable",
			act:  action.Typed(typedApprove),
			vars: map[string]any{"ref": "ana-3", "count": 2.5},
			assert: func(t *testing.T, st engine.InstanceState, af engine.ActionFailed, armed bool) {
				assert.False(t, af.Retryable,
					"a decode failure is deterministic for a snapshot, so the runtime must not retry it")
				require.ErrorIs(t, af.Cause, action.ErrDecodeInput,
					"the sentinel must survive the runtime's wrapping on ActionFailed.Cause")
				assert.Contains(t, af.Err, "count")
				assert.False(t, armed, "a non-retryable failure must not arm a retry timer")
				assert.Equal(t, engine.StatusRunning, st.Status)
				assert.Len(t, st.Incidents, 1,
					"a terminal failure under a retry policy parks the instance as an incident")
			},
		},
		{
			// End-to-end proof of the documented WithStrictInput limit: the engine
			// really does stamp _idempotencyKey into a primary service task's input,
			// so strict decoding rejects the invocation, loudly and by name.
			name: "strict input rejects the engine's _idempotencyKey stamp",
			act:  action.Typed(typedApprove, action.WithStrictInput()),
			vars: map[string]any{"ref": "ana-3", "count": 2},
			assert: func(t *testing.T, st engine.InstanceState, af engine.ActionFailed, armed bool) {
				assert.False(t, af.Retryable)
				require.ErrorIs(t, af.Cause, action.ErrDecodeInput)
				assert.Contains(t, af.Err, "_idempotencyKey",
					"the engine stamps _idempotencyKey, so strict input must name it as the offender")
				assert.False(t, armed)
				assert.Equal(t, engine.StatusRunning, st.Status)
				assert.Len(t, st.Incidents, 1)
			},
		},
		{
			// The security case, end to end through the real engine. An attacker
			// who can start an instance places "_idempotencykey" in the variables
			// map. That is a DIFFERENT map key from the engine's "_idempotencyKey"
			// stamp, so it survives serviceActionInput's clone; encoding/json then
			// folds case, and because json.Marshal sorts keys byte-wise
			// ('K' 0x4b < 'k' 0x6b) the attacker's twin is applied LAST and would
			// win. Strict must reject it by exact byte match instead.
			name: "strict rejects an attacker's case-variant of the idempotency stamp",
			act:  action.Typed(typedIdemEcho, action.WithStrictInput()),
			vars: map[string]any{"ref": "req-1", "_idempotencykey": "SPOOFED-BY-ATTACKER"},
			assert: func(t *testing.T, st engine.InstanceState, af engine.ActionFailed, armed bool) {
				assert.False(t, af.Retryable)
				require.ErrorIs(t, af.Cause, action.ErrDecodeInput)
				assert.Contains(t, af.Err, "_idempotencykey",
					"the spoofing key must be named in the durable failure message")
				assert.NotContains(t, af.Err, "SPOOFED-BY-ATTACKER",
					"the rejection names the offending KEY, never the attacker's value")
				assert.False(t, armed)
				assert.Equal(t, engine.StatusRunning, st.Status)
				assert.Len(t, st.Incidents, 1)
			},
		},
		{
			// Control: proves the harness CAN arm a timer, so Armed == false above
			// reports the retry classification rather than an inert scheduler.
			name: "control: a plain action error stays retryable and arms a retry",
			act: action.ActionFunc(func(context.Context, map[string]any) (map[string]any, error) {
				return nil, errors.New("transient upstream failure")
			}),
			vars: map[string]any{"ref": "ana-3", "count": 2},
			assert: func(t *testing.T, st engine.InstanceState, af engine.ActionFailed, armed bool) {
				assert.True(t, af.Retryable, "a plain error keeps the retry-by-default contract")
				assert.NotErrorIs(t, af.Cause, action.ErrDecodeInput)
				assert.True(t, armed, "a retryable failure under MaxAttempts=3 must arm a retry timer")
				assert.Equal(t, engine.StatusRunning, st.Status)
				assert.Empty(t, st.Incidents, "a retryable failure parks for retry, not as an incident")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			clk := clockwork.NewFakeClockAt(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
			sched := &runtimetest.RecordingScheduler{Clock: clk}
			store := runtimetest.MustMemStore(t)
			cat := action.NewCatalog(map[string]action.Action{"t": tc.act})

			driver := runtimetest.MustProcessDriver(t, cat, store,
				runtime.WithClock(clk),
				runtime.WithScheduler(sched),
				runtime.WithDefaultRetryPolicy(model.RetryPolicy{
					MaxAttempts:     3,
					InitialInterval: time.Second,
					BackoffCoef:     2,
					MaxInterval:     time.Minute,
				}),
			)

			const instanceID = "typed-1"
			st, err := driver.Drive(t.Context(), typedTaskDef(), instanceID, tc.vars)
			require.NoError(t, err, "an action failure must not surface as a Go error from Drive")

			// The in-memory journal stores the trigger VALUE, so the non-persisted
			// ActionFailed.Cause (json:"-") survives for inspection here.
			entries, err := store.Entries(t.Context(), instanceID)
			require.NoError(t, err)
			var af engine.ActionFailed
			var found bool
			for _, entry := range entries {
				if v, ok := entry.(engine.ActionFailed); ok {
					af, found = v, true
					break
				}
			}
			require.True(t, found, "journal must contain an ActionFailed trigger; entries: %v", entries)

			tc.assert(t, st, af, sched.Armed)
		})
	}
}

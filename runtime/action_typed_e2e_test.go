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

// typedIdemValueOut surfaces the idempotency key the action actually RECEIVED as
// an output variable, so a test can assert on the value rather than on its mere
// presence. Asserting "not the attacker's value" would be satisfied by a decoder
// that dropped every underscore-prefixed key; asserting the engine's exact
// instanceID:nodeID is not.
type typedIdemValueOut struct {
	Idem string `json:"idem"`
}

func typedIdemValueEcho(_ context.Context, in typedIdemIn) (typedIdemValueOut, error) {
	return typedIdemValueOut{Idem: in.IdempotencyKey}, nil
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

	// Shared with the case closures so the expected engine stamp is derived from
	// the same identifier the driver is given, not written out twice.
	const instanceID = "typed-1"

	type testCase struct {
		name   string
		act    action.Action
		vars   map[string]any
		assert func(t *testing.T, st engine.InstanceState, af engine.ActionFailed, failed, armed bool)
	}

	cases := []testCase{
		{
			name: "fractional variable into an int field is non-retryable",
			act:  action.Typed(typedApprove),
			vars: map[string]any{"ref": "ana-3", "count": 2.5},
			assert: func(t *testing.T, st engine.InstanceState, af engine.ActionFailed, failed, armed bool) {
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
			assert: func(t *testing.T, st engine.InstanceState, af engine.ActionFailed, failed, armed bool) {
				require.True(t, failed, "this case must produce an ActionFailed")
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
			// THE POSITIVE CONTROL for the case below, and the measurement that
			// keeps its framing honest. Reserving the engine names removes ONE
			// input from an unbounded set: under strict, ANY caller-supplied key
			// the In does not declare still fails exactly as it did before. So
			// the alarm the next case retires is one instance of an alarm anybody
			// can re-trigger with a different key name — which is what makes
			// losing that particular one acceptable, and why "the fix removes an
			// attacker-triggerable failure" would be too strong a claim.
			//
			// It is also what proves the next case can observe a failure at all.
			// Both run through the same table and the same runner: this one
			// asserts failed == true, the next asserts failed == false. Without
			// it, "no ActionFailed" there would be indistinguishable from a
			// harness that cannot see one.
			name: "an ordinary undeclared key still fails a strict action — the reservation removes one input, not the vector",
			act:  action.Typed(typedIdemValueEcho, action.WithStrictInput()),
			vars: map[string]any{"ref": "req-1", "attackerJunk": "SPOOFED-BY-ATTACKER"},
			assert: func(t *testing.T, st engine.InstanceState, af engine.ActionFailed, failed, armed bool) {
				require.True(t, failed, "this case must produce an ActionFailed")
				assert.False(t, af.Retryable)
				require.ErrorIs(t, af.Cause, action.ErrDecodeInput)
				assert.Contains(t, af.Err, "attackerJunk",
					"the offending key is named in the durable failure message")
				assert.NotContains(t, af.Err, "SPOOFED-BY-ATTACKER",
					"the rejection names the offending KEY, never the attacker's value")
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
			// #150 CHANGED THIS CASE'S OUTCOME, and the replacement pins what the
			// fix now guarantees rather than deleting what it retired.
			//
			// Before: the engine copied the caller's "_idempotencykey" into the
			// action input alongside its own stamp, strict decoding found an
			// unknown key, and the invocation failed loudly — ActionFailed,
			// ErrDecodeInput, one Incident. That was the STRICT-mode defence, and
			// it never covered lenient, which is the mode a service task actually
			// uses.
			//
			// After: serviceActionInput reserves the engine-stamped names, so the
			// variant never reaches ANY action, in either mode. The invocation
			// succeeds and the action receives the ENGINE's key.
			//
			// The trade, stated because it is real: an operator-visible alarm is
			// gone. What replaces it is (iii) below — the caller's key is still
			// durably in the instance variables, so the RECORD survives even
			// though the ALARM does not. Measured alongside it: a strict action
			// remains failable by any OTHER undeclared key, so this removes one
			// input from an unbounded set rather than closing a vector.
			name: "the engine's reserved stamp survives an attacker's case-variant, and the action runs",
			act:  action.Typed(typedIdemValueEcho, action.WithStrictInput()),
			vars: map[string]any{"ref": "req-1", "_idempotencykey": "SPOOFED-BY-ATTACKER"},
			assert: func(t *testing.T, st engine.InstanceState, af engine.ActionFailed, failed, armed bool) {
				// (i) the action succeeds — the variant never reaches it.
				assert.False(t, failed,
					"the reserved-name filter removes the variant before the action "+
						"decodes, so strict input finds nothing unknown")
				assert.False(t, armed)
				assert.Equal(t, engine.StatusCompleted, st.Status)
				assert.Empty(t, st.Incidents)

				// (ii) the action received THE ENGINE'S key, by value. "not
				// SPOOFED-BY-ATTACKER" would also be satisfied by a filter that
				// dropped every underscore-prefixed key; instanceID:nodeID is not.
				assert.Equal(t, instanceID+":task", st.Variables["idem"],
					"the action must receive the engine's stamp, not merely be denied "+
						"the attacker's value")

				// (iii) THE RECORD SURVIVES EVEN THOUGH THE ALARM DOES NOT. The
				// filter applies to the action-input COPY, never to the instance
				// variables, so the caller's key is still durably present and an
				// operator or an audit can still see it was supplied.
				assert.Equal(t, "SPOOFED-BY-ATTACKER", st.Variables["_idempotencykey"],
					"reserving must not erase the caller's variable from the instance; "+
						"losing the alarm is the accepted cost, losing the evidence is not")
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
			assert: func(t *testing.T, st engine.InstanceState, af engine.ActionFailed, failed, armed bool) {
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

			// Whether an ActionFailed exists is now a per-case OUTCOME, not a
			// precondition: reserving the engine-stamped keys (#150) means one of
			// these inputs no longer fails at all, and a runner that required a
			// failure could not express that.
			tc.assert(t, st, af, found, sched.Armed)
		})
	}
}

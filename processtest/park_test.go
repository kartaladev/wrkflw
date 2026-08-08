package processtest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/processtest"
)

func TestIsTerminal(t *testing.T) {
	t.Parallel()

	type testCase struct {
		status engine.Status
		want   bool
	}
	cases := []testCase{
		{engine.StatusRunning, false},
		{engine.StatusCompensating, false},
		{engine.StatusCompleted, true},
		{engine.StatusFailed, true},
		{engine.StatusTerminated, true},
	}
	for _, tc := range cases {
		t.Run(tc.status.String(), func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, processtest.IsTerminal(tc.status))
		})
	}
}

func TestReasonString(t *testing.T) {
	t.Parallel()

	type testCase struct {
		reason processtest.Reason
		want   string
	}
	cases := []testCase{
		{processtest.ReasonTerminal, "terminal"},
		{processtest.ReasonHumanTask, "human-task"},
		{processtest.ReasonIncident, "incident"},
		{processtest.ReasonSignal, "signal"},
		{processtest.ReasonMessage, "message"},
		{processtest.ReasonTimer, "timer"},
		{processtest.ReasonAsyncChild, "async-child"},
		{processtest.ReasonUnknown, "unknown"},
		{processtest.Reason(99), "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.reason.String())
		})
	}
}

func TestClassify(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		state  engine.InstanceState
		assert func(t *testing.T, p processtest.Park)
	}

	cases := []testCase{
		{
			name:  "completed instance is terminal",
			state: engine.InstanceState{Status: engine.StatusCompleted},
			assert: func(t *testing.T, p processtest.Park) {
				assert.Equal(t, processtest.ReasonTerminal, p.Reason)
			},
		},
		{
			name: "open human task",
			state: engine.InstanceState{
				Status: engine.StatusRunning,
				Tasks: []humantask.HumanTask{
					{TaskID: "tk-1", NodeID: "approve", State: humantask.Unclaimed},
				},
			},
			assert: func(t *testing.T, p processtest.Park) {
				assert.Equal(t, processtest.ReasonHumanTask, p.Reason)
				require.Len(t, p.OpenTasks, 1)
				assert.Equal(t, "tk-1", p.OpenTasks[0].TaskID)
				assert.Equal(t, "approve", p.Node)
			},
		},
		{
			name: "incident takes precedence over signal",
			state: engine.InstanceState{
				Status:    engine.StatusRunning,
				Incidents: []engine.Incident{{ID: "inc-1", NodeID: "call-api"}},
				Tokens:    []engine.Token{{ID: "t1", NodeID: "wait-sig", AwaitSignal: "go"}},
			},
			assert: func(t *testing.T, p processtest.Park) {
				assert.Equal(t, processtest.ReasonIncident, p.Reason)
				require.Len(t, p.Incidents, 1)
				assert.Equal(t, "call-api", p.Node)
				// Secondary park still surfaced.
				assert.Equal(t, []string{"go"}, p.AwaitingSignals)
			},
		},
		{
			name: "awaiting signal",
			state: engine.InstanceState{
				Status: engine.StatusRunning,
				Tokens: []engine.Token{{ID: "t1", NodeID: "wait-sig", AwaitSignal: "market-open"}},
			},
			assert: func(t *testing.T, p processtest.Park) {
				assert.Equal(t, processtest.ReasonSignal, p.Reason)
				assert.Equal(t, []string{"market-open"}, p.AwaitingSignals)
				assert.Equal(t, "wait-sig", p.Node)
			},
		},
		{
			name: "awaiting message",
			state: engine.InstanceState{
				Status: engine.StatusRunning,
				Tokens: []engine.Token{{ID: "t1", NodeID: "wait-msg", AwaitMessage: "PaymentReceived"}},
			},
			assert: func(t *testing.T, p processtest.Park) {
				assert.Equal(t, processtest.ReasonMessage, p.Reason)
				assert.Equal(t, []engine.MessageWaiter{{Name: "PaymentReceived"}}, p.AwaitingMessages)
			},
		},
		{
			name: "waiting on a command (async child)",
			state: engine.InstanceState{
				Status: engine.StatusRunning,
				Tokens: []engine.Token{{ID: "t1", NodeID: "call-sub", State: engine.TokenWaiting, AwaitCommand: "cmd-9"}},
			},
			assert: func(t *testing.T, p processtest.Park) {
				assert.Equal(t, processtest.ReasonAsyncChild, p.Reason)
				assert.Equal(t, "call-sub", p.Node)
			},
		},
		{
			name: "running with an active token but nothing to wait on is unknown",
			state: engine.InstanceState{
				Status: engine.StatusRunning,
				Tokens: []engine.Token{{ID: "t1", NodeID: "node", State: engine.TokenActive}},
			},
			assert: func(t *testing.T, p processtest.Park) {
				assert.Equal(t, processtest.ReasonUnknown, p.Reason)
			},
		},
		{
			name: "human task with a concurrent signal is primary human-task",
			state: engine.InstanceState{
				Status: engine.StatusRunning,
				Tasks:  []humantask.HumanTask{{TaskID: "tk-1", NodeID: "review", State: humantask.Claimed}},
				Tokens: []engine.Token{{ID: "t1", NodeID: "wait-sig", AwaitSignal: "escalate"}},
			},
			assert: func(t *testing.T, p processtest.Park) {
				assert.Equal(t, processtest.ReasonHumanTask, p.Reason)
				assert.Equal(t, []string{"escalate"}, p.AwaitingSignals)
				require.Len(t, p.OpenTasks, 1)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.assert(t, processtest.Classify(tc.state))
		})
	}
}

// TestClassifyPinsUnknownReasonForCompensationWalkPark PINS A KNOWN GAP. It is
// NOT an endorsement and NOT a fix: it records, so the next reader is not
// surprised, that processtest cannot name a compensation-walk park.
//
// An instance waiting on an in-flight reverse-order compensation walk sits at
// StatusCompensating with zero tokens. The walk is awaited through the engine's
// compensation cursor (InstanceState.Compensating.ActiveCmdID) — no token
// carries it — so ReasonAsyncChild, which requires a token with AwaitCommand,
// does not match, and the ladder falls through to ReasonUnknown. A handler with
// no case for that Passes (asserted below), and drive turns a passed park into
// ErrUnhandledPark.
//
// Measured 2026-08-09 on this tree, both shapes agreeing:
//
//	Classify(InstanceState{Status: StatusCompensating})        → reason="unknown"
//	Classify(real mid-walk state, activeCmd="i1-c2" tokens=0)  → reason="unknown"
//
// The real state came from the engine's own ADR-0168 fixture
// (TestCompensationWalkInFlightBlocksCompletion): status=compensating tokens=0
// activeCmd="i1-c2" tasks=2 (both closed) timers=0 boundaries=0 armed=0. It
// classifies identically to the literal below because Classify never reads the
// cursor — which is the gap. The literal is therefore a faithful stand-in, and
// is used because Compensating's type is unexported and cannot be built here.
//
// ADR-0168 widens the routes that reach this state (an ordinary in-definition
// compensation throw now defers completion instead of racing it); the state was
// already reachable on main through whole-instance rollback, so this pin is not
// a consequence of that change.
//
// How bad is it, measured rather than assumed: driving the ADR-0168 fixture
// through the Harness with an ordinary synchronous catalog action does NOT hit
// the gap. The runtime performs the walk's compensating InvokeAction and feeds
// its ActionCompleted back inside the same ApplyTrigger, so the walk finishes
// before control returns to drive. Observed: after s1 status=running
// activeCmd="" reason="human-task"; after s2 status=completed reason="terminal";
// undoA invoked once. So the gap bites a consumer that classifies a STORED
// snapshot taken mid-walk, not the default synchronous drive loop.
// ASSUMPTION (unverified): whether any harness-driveable shape reaches it — only
// this one was executed.
//
// The real fix is a ReasonCompensation whose Park surfaces the awaited command
// id, so a handler can deliver the walk's ActionCompleted. That is deliberately
// out of scope for this bundle. It is the same class of defect as backlog item
// 6b (Park.HasArmedTimers is false for a timer-arm-only park, leaving it
// undriveable) — processtest cannot see a park the engine tracks off-token.
//
// What makes this test fail: it discriminates on Classify's fallthrough arm AND
// on the fixture's status. Both verified by mutation, byte-clean restore after
// each:
//
//	Classify default arm returns ReasonAsyncChild → FAIL "expected: 7 actual: 6"
//	IsTerminal also accepts Compensating          → FAIL "expected: 7 actual: 0"
//	AutoTimers also acts on ReasonUnknown         → FAIL "kind: 0 → 2"
//
// Delete this test only together with the gap.
func TestClassifyPinsUnknownReasonForCompensationWalkPark(t *testing.T) {
	t.Parallel()

	// A mid-walk snapshot: the compensable activity has completed and been
	// recorded, its token is gone, and the instance is waiting for the
	// compensating action to report back.
	state := engine.InstanceState{
		InstanceID: "i1",
		DefID:      "order",
		Status:     engine.StatusCompensating,
		Variables:  map[string]any{"orderID": "o-7"},
		RootCompensations: []engine.CompensationRecord{
			{NodeID: "charge", Action: "refund"},
		},
	}

	p := processtest.Classify(state)

	require.Equal(t, processtest.ReasonUnknown, p.Reason,
		"KNOWN GAP: a compensation-walk park has no Reason of its own")
	assert.Equal(t, "unknown", p.Reason.String())
	assert.Empty(t, p.Node, "with no tokens there is no node to name")

	// Positive controls: the pin is about the missing reason, not about a state
	// that trivially matches nothing. StatusCompensating is genuinely non-terminal,
	// so drive keeps going and reaches the handler.
	assert.False(t, processtest.IsTerminal(state.Status),
		"a compensating instance is not terminal — drive does not stop on its own")
	assert.Empty(t, p.AwaitingSignals)
	assert.Empty(t, p.AwaitingMessages)
	assert.False(t, p.HasArmedTimers)
	assert.Empty(t, p.OpenTasks)
	assert.Empty(t, p.Incidents)

	// The consumer-visible consequence, asserted rather than reasoned about: every
	// built-in handler is Reason-gated, so a Chain of them Passes on this park —
	// and drive turns a passed park into ErrUnhandledPark. The contrast that keeps
	// this from being vacuous is the existing "AutoTimers drives a timer flow to
	// completion" case in handlers_test.go: AutoTimers does not pass on the one
	// park it handles. A ReasonTimer park cannot be built here for an inline
	// contrast — InstanceState.Timers has an unexported element type.
	d, err := processtest.Chain(processtest.AutoTimers())(t.Context(), p)
	require.NoError(t, err)
	assert.Equal(t, processtest.Pass(), d,
		"no built-in handler can resolve a compensation-walk park")
}

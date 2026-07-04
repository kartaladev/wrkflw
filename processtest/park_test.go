package processtest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/processtest"
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
					{TaskToken: "tk-1", NodeID: "approve", State: humantask.Unclaimed},
				},
			},
			assert: func(t *testing.T, p processtest.Park) {
				assert.Equal(t, processtest.ReasonHumanTask, p.Reason)
				require.Len(t, p.OpenTasks, 1)
				assert.Equal(t, "tk-1", p.OpenTasks[0].TaskToken)
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
				assert.Equal(t, []string{"PaymentReceived"}, p.AwaitingMessages)
			},
		},
		{
			name: "waiting on a command (async child)",
			state: engine.InstanceState{
				Status: engine.StatusRunning,
				Tokens: []engine.Token{{ID: "t1", NodeID: "call-sub", State: engine.TokenWaitingCommand, AwaitCommand: "cmd-9"}},
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
				Tasks:  []humantask.HumanTask{{TaskToken: "tk-1", NodeID: "review", State: humantask.Claimed}},
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

package engine_test

// retry_test.go — black-box tests for Task 5: schedule a retry timer on a
// retryable action failure.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/model"
)

// retryDef builds a one-service-task definition with an optional node-level
// retry policy. Shape: start → task(service) → end.
func retryDef(p *model.RetryPolicy) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "task", Kind: model.KindServiceTask, Action: "a", RetryPolicy: p},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "task"},
			{ID: "f2", Source: "task", Target: "end"},
		},
	}
}

// findInvokeActionCmdID scans commands for the first InvokeAction and returns
// its CommandID. Fails the test if none is found.
func findInvokeActionCmdID(t *testing.T, cmds []engine.Command) string {
	t.Helper()
	for _, c := range cmds {
		if ia, ok := c.(engine.InvokeAction); ok {
			return ia.CommandID
		}
	}
	t.Fatal("no InvokeAction found in commands")
	return ""
}

// findScheduleTimerByKind scans commands for the first ScheduleTimer with the
// given kind and returns it. Returns false if not found.
func findScheduleTimerByKind(cmds []engine.Command, kind engine.TimerKind) (engine.ScheduleTimer, bool) {
	for _, c := range cmds {
		if st, ok := c.(engine.ScheduleTimer); ok && st.Kind == kind {
			return st, true
		}
	}
	return engine.ScheduleTimer{}, false
}

// findTokenByNodeID scans tokens for the one parked at the given node. Fails
// if not found.
func findTokenByNodeID(t *testing.T, tokens []engine.Token, nodeID string) engine.Token {
	t.Helper()
	for _, tok := range tokens {
		if tok.NodeID == nodeID {
			return tok
		}
	}
	t.Fatalf("no token found at node %q", nodeID)
	return engine.Token{}
}

// TestStepSchedulesRetryWithJitteredBackoff verifies that an ActionFailed on a
// node with a retry policy and remaining budget schedules a TimerRetry command
// with the correct FireAt (backoff × JitterFraction) and increments RetryAttempts.
func TestStepSchedulesRetryWithJitteredBackoff(t *testing.T) {
	policy := &model.RetryPolicy{
		MaxAttempts:     3,
		InitialInterval: time.Second,
		BackoffCoef:     2.0,
		MaxInterval:     time.Minute,
	}
	def := retryDef(policy)

	// Drive start → InvokeAction for "task".
	at0 := time.Unix(0, 0)
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "p"},
		engine.NewStartInstance(at0, nil), engine.StepOptions{})
	require.NoError(t, err)
	cmdID := findInvokeActionCmdID(t, r1.Commands)

	// Deliver ActionFailed: retryable, jitter=0.5, at t=10s.
	failAt := time.Unix(10, 0)
	fail := engine.NewActionFailedJittered(failAt, cmdID, "boom", true, 0.5)
	r2, err := engine.Step(def, r1.State, fail, engine.StepOptions{})
	require.NoError(t, err)

	// Must find a ScheduleTimer of kind TimerRetry.
	st, found := findScheduleTimerByKind(r2.Commands, engine.TimerRetry)
	require.True(t, found, "expected a ScheduleTimer{Kind:TimerRetry} in commands")

	// attempt 0 backoff = 1s × 2^0 = 1s; jitter 0.5 → delay = 500ms.
	wantFire := failAt.Add(500 * time.Millisecond)
	assert.True(t, st.FireAt.Equal(wantFire),
		"FireAt = %v, want %v", st.FireAt, wantFire)

	// Token at "task" must have RetryAttempts == 1.
	tok := findTokenByNodeID(t, r2.State.Tokens, "task")
	assert.Equal(t, 1, tok.RetryAttempts,
		"RetryAttempts should be 1 after first retry scheduled")

	// Token must be parked waiting on the timer.
	assert.Equal(t, engine.TokenWaitingCommand, tok.State)
	assert.Equal(t, st.TimerID, tok.AwaitCommand)
}

// TestStepNoPolicyKeepsLegacyBehaviour verifies that without a retry policy
// (no node policy, nil DefaultRetryPolicy) an ActionFailed falls through to
// the existing propagateError path (FailInstance, no TimerRetry).
func TestStepNoPolicyKeepsLegacyBehaviour(t *testing.T) {
	def := retryDef(nil) // no node policy
	at0 := time.Unix(0, 0)
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "p"},
		engine.NewStartInstance(at0, nil), engine.StepOptions{})
	require.NoError(t, err)
	cmdID := findInvokeActionCmdID(t, r1.Commands)

	failAt := time.Unix(1, 0)
	fail := engine.NewActionFailed(failAt, cmdID, "boom", true)
	r2, err := engine.Step(def, r1.State, fail, engine.StepOptions{}) // nil DefaultRetryPolicy
	require.NoError(t, err)

	_, found := findScheduleTimerByKind(r2.Commands, engine.TimerRetry)
	assert.False(t, found, "no-policy path must not schedule a TimerRetry")

	// Legacy path should fail the instance.
	var hasFail bool
	for _, c := range r2.Commands {
		if _, ok := c.(engine.FailInstance); ok {
			hasFail = true
			break
		}
	}
	assert.True(t, hasFail, "legacy path must emit FailInstance")
}

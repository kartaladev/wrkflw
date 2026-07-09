package engine_test

// retry_test.go — black-box tests for Task 5: schedule a retry timer on a
// retryable action failure.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
)

// retryDef builds a one-service-task definition with an optional node-level
// retry policy. Shape: start → task(service) → end.
func retryDef(p *model.RetryPolicy) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("task", activity.WithTaskAction("a"), activity.WithRetryPolicy(p)),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
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

	// ApplyTrigger ActionFailed: retryable, jitter=0.5, at t=10s.
	failAt := time.Unix(10, 0)
	fail := engine.NewActionFailed(failAt, cmdID, "boom", true, engine.WithJitter(0.5))
	r2, err := engine.Step(def, r1.State, fail, engine.StepOptions{})
	require.NoError(t, err)

	// Must find a ScheduleTimer of kind TimerRetry.
	st, found := findScheduleTimerByKind(r2.Commands, engine.TimerRetry)
	require.True(t, found, "expected a ScheduleTimer{Kind:TimerRetry} in commands")

	// attempt 0 backoff = 1s × 2^0 = 1s; jitter 0.5 → delay = 500ms. The retry
	// timer now carries a concrete AfterDuration(delay) trigger (no FireAt); the
	// scheduler owns the absolute fire instant.
	gotDelay, ok := st.Trigger.Duration()
	require.True(t, ok, "retry trigger must be a concrete duration, got %+v", st.Trigger)
	assert.Equal(t, 500*time.Millisecond, gotDelay,
		"retry trigger delay = %v, want 500ms", gotDelay)

	// Token at "task" must have RetryAttempts == 1.
	tok := findTokenByNodeID(t, r2.State.Tokens, "task")
	assert.Equal(t, 1, tok.RetryAttempts,
		"RetryAttempts should be 1 after first retry scheduled")

	// Token must be parked waiting on the timer.
	assert.Equal(t, engine.TokenWaitingCommand, tok.State)
	assert.Equal(t, st.TimerID, tok.AwaitCommand)
}

// TestStepRetryTimerReinvokesAction verifies that when a retry timer fires, the
// engine re-emits an InvokeAction for the same node, effectively re-invoking
// the action as if it were the first attempt.
func TestStepRetryTimerReinvokesAction(t *testing.T) {
	def := retryDef(&model.RetryPolicy{
		MaxAttempts:     3,
		InitialInterval: time.Second,
		BackoffCoef:     2.0,
		MaxInterval:     time.Minute,
	})

	// Drive start → InvokeAction for "task".
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "p"},
		engine.NewStartInstance(time.Unix(0, 0), nil), engine.StepOptions{})
	require.NoError(t, err)
	cmdID := findInvokeActionCmdID(t, r1.Commands)

	// ApplyTrigger ActionFailed: retryable, jitter=0.5, at t=10s.
	r2, err := engine.Step(def, r1.State,
		engine.NewActionFailed(time.Unix(10, 0), cmdID, "boom", true, engine.WithJitter(0.5)),
		engine.StepOptions{})
	require.NoError(t, err)

	// Grab the retry timer ID from r2.Commands.
	var timerID string
	for _, c := range r2.Commands {
		if st, ok := c.(engine.ScheduleTimer); ok && st.Kind == engine.TimerRetry {
			timerID = st.TimerID
			break
		}
	}
	require.NotEmpty(t, timerID, "expected a ScheduleTimer{Kind:TimerRetry} in r2 commands")

	// Fire the retry timer.
	r3, err := engine.Step(def, r2.State,
		engine.NewTimerFired(time.Unix(11, 0), timerID),
		engine.StepOptions{})
	require.NoError(t, err)

	// Assert a fresh InvokeAction for node "task" is emitted.
	var gotInvoke bool
	for _, c := range r3.Commands {
		if ia, ok := c.(engine.InvokeAction); ok && ia.Name == "a" {
			gotInvoke = true
			require.NotEmpty(t, ia.CommandID, "re-invocation must carry a new command ID")
		}
	}
	assert.True(t, gotInvoke, "expected a fresh InvokeAction for node 'task' after retry timer fired")
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

// recoveryFlowDef builds a service task with a RecoveryFlow "rf" → "recover"
// service task, plus a terminal node policy. On terminal exhaustion the engine
// should route the failing token down "rf" instead of raising an incident.
func recoveryFlowDef(p *model.RetryPolicy) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("task", activity.WithTaskAction("a"), activity.WithRecoveryFlow("rf"), activity.WithRetryPolicy(p)),
			activity.NewServiceTask("recover", activity.WithTaskAction("compensate")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "task"},
			{ID: "f2", Source: "task", Target: "end"},
			{ID: "rf", Source: "task", Target: "recover"},
		},
	}
}

// boundaryWithPolicyDef builds a service task carrying BOTH a terminal retry
// policy AND a direct error boundary "bnd" → "recover". On terminal exhaustion
// (no RecoveryFlow) the boundary must still catch the error — proving precedence
// (2) survives the presence of a policy.
func boundaryWithPolicyDef(p *model.RetryPolicy) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("task", activity.WithTaskAction("a"), activity.WithRetryPolicy(p)),
			event.NewBoundary("bnd", "task"),
			activity.NewServiceTask("recover", activity.WithTaskAction("compensate")),
			event.NewEnd("end"),
			event.NewEnd("end-recover"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "task"},
			{ID: "f2", Source: "task", Target: "end"},
			{ID: "f-bnd", Source: "bnd", Target: "recover"},
			{ID: "f-rec", Source: "recover", Target: "end-recover"},
		},
	}
}

// hasInvokeActionForName reports whether any InvokeAction command targets the
// given action name.
func hasInvokeActionForName(cmds []engine.Command, name string) bool {
	for _, c := range cmds {
		if ia, ok := c.(engine.InvokeAction); ok && ia.Name == name {
			return true
		}
	}
	return false
}

// hasInvokeActionForNode reports whether any InvokeAction command targets the
// action of the node with the given ID in def.
func hasInvokeActionForNode(t *testing.T, r engine.StepResult, def *model.ProcessDefinition, nodeID string) bool {
	t.Helper()
	node, ok := def.Node(nodeID)
	if !ok {
		t.Fatalf("hasInvokeActionForNode: node %q not found in definition", nodeID)
	}
	// Mirror the engine's main-action lookup key: explicit action name, or the
	// node id when none is set (default-by-id). Avoids a panic on non-ServiceTask
	// nodes and a wrong empty-string match for default-by-id nodes.
	name := model.ActionOf(node)
	if name == "" {
		name = node.ID()
	}
	return hasInvokeActionForName(r.Commands, name)
}

// TestStepResolveIncidentReinvokes verifies that delivering a ResolveIncident
// trigger for an existing incident clears the incident and re-emits an
// InvokeAction for the parked node.
func TestStepResolveIncidentReinvokes(t *testing.T) {
	def := retryDef(&model.RetryPolicy{MaxAttempts: 1})

	// Drive start → InvokeAction for "task".
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "p"},
		engine.NewStartInstance(time.Unix(0, 0), nil), engine.StepOptions{})
	require.NoError(t, err)
	cmdID := findInvokeActionCmdID(t, r1.Commands)

	// ApplyTrigger terminal ActionFailed (MaxAttempts:1 → first failure is terminal) → incident.
	r2, err := engine.Step(def, r1.State,
		engine.NewActionFailed(time.Unix(1, 0), cmdID, "boom", true), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r2.State.Incidents, 1, "expected one incident after terminal failure")
	incID := r2.State.Incidents[0].ID

	// Resolve the incident, granting 2 extra attempts.
	r3, err := engine.Step(def, r2.State,
		engine.NewResolveIncident(time.Unix(2, 0), incID, 2), engine.StepOptions{})
	require.NoError(t, err)

	assert.Empty(t, r3.State.Incidents, "incident must be cleared after ResolveIncident")
	assert.True(t, hasInvokeActionForNode(t, r3, def, "task"),
		"action must be re-invoked after ResolveIncident")
}

// TestStepResolveUnknownIncidentNoop verifies that delivering a ResolveIncident
// for an unknown incident ID is a clean no-op (no commands, no error).
func TestStepResolveUnknownIncidentNoop(t *testing.T) {
	def := retryDef(&model.RetryPolicy{MaxAttempts: 1})
	base := engine.InstanceState{InstanceID: "p"}

	r, err := engine.Step(def, base,
		engine.NewResolveIncident(time.Unix(0, 0), "nope", 1), engine.StepOptions{})
	require.NoError(t, err)
	assert.Empty(t, r.Commands, "unknown incident must be a no-op")
}

// hasFailInstance reports whether any FailInstance command is present.
func hasFailInstance(cmds []engine.Command) bool {
	for _, c := range cmds {
		if _, ok := c.(engine.FailInstance); ok {
			return true
		}
	}
	return false
}

// firstInvokeAction scans commands for the first InvokeAction and returns it.
// Fails the test if none is found.
func firstInvokeAction(t *testing.T, cmds []engine.Command) engine.InvokeAction {
	t.Helper()
	for _, c := range cmds {
		if ia, ok := c.(engine.InvokeAction); ok {
			return ia
		}
	}
	t.Fatal("no InvokeAction found in commands")
	return engine.InvokeAction{}
}

// firstScheduleTimer scans commands for the first ScheduleTimer (any kind) and
// returns it. Fails the test if none is found.
func firstScheduleTimer(t *testing.T, cmds []engine.Command) engine.ScheduleTimer {
	t.Helper()
	for _, c := range cmds {
		if st, ok := c.(engine.ScheduleTimer); ok {
			return st
		}
	}
	t.Fatal("no ScheduleTimer found in commands")
	return engine.ScheduleTimer{}
}

// TestInvokeActionCarriesStableIdempotencyKey verifies that:
//  1. The first InvokeAction for a service-task carries
//     Input["_idempotencyKey"] == "<instanceID>:<nodeID>".
//  2. After a retryable failure the re-invocation carries the SAME key
//     (stable across retries — no attempt number suffix).
func TestInvokeActionCarriesStableIdempotencyKey(t *testing.T) {
	def := retryDef(&model.RetryPolicy{MaxAttempts: 3, InitialInterval: time.Second, BackoffCoef: 2})

	// Drive start → service task.
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "p"},
		engine.NewStartInstance(time.Unix(0, 0), nil), engine.StepOptions{})
	require.NoError(t, err)

	inv1 := firstInvokeAction(t, r1.Commands)
	require.Equal(t, "p:task", inv1.Input["_idempotencyKey"],
		"first invocation must carry stable idempotency key")

	// ApplyTrigger retryable failure → retry timer scheduled.
	r2, err := engine.Step(def, r1.State,
		engine.NewActionFailed(time.Unix(10, 0), inv1.CommandID, "boom", true, engine.WithJitter(0.5)),
		engine.StepOptions{})
	require.NoError(t, err)

	timerID := firstScheduleTimer(t, r2.Commands).TimerID

	// Fire the retry timer → re-invocation.
	r3, err := engine.Step(def, r2.State,
		engine.NewTimerFired(time.Unix(11, 0), timerID),
		engine.StepOptions{})
	require.NoError(t, err)

	inv2 := firstInvokeAction(t, r3.Commands)
	require.Equal(t, "p:task", inv2.Input["_idempotencyKey"],
		"re-invocation after retry must carry the SAME idempotency key (attempt-independent)")
}

// TestStepExhaustion exercises the terminal-exhaustion precedence:
// (1) catch-flow (RecoveryFlow), (2) error boundary, (3) incident. All three
// cases share the call shape: drive start → fail terminally → assert.
func TestStepExhaustion(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		def    *model.ProcessDefinition
		assert func(t *testing.T, r engine.StepResult)
	}

	terminal := &model.RetryPolicy{MaxAttempts: 1} // first failure is terminal

	cases := []testCase{
		{
			name: "incident when no catch-flow and no boundary",
			def:  retryDef(terminal),
			assert: func(t *testing.T, r engine.StepResult) {
				assert.Equal(t, engine.StatusRunning, r.State.Status,
					"instance must stay running on incident, not fail")
				require.Len(t, r.State.Incidents, 1)
				inc := r.State.Incidents[0]
				assert.Equal(t, "task", inc.NodeID)
				assert.Equal(t, "boom", inc.Error)
				assert.Equal(t, 1, inc.Attempts)
				assert.Equal(t, engine.TokenIncident,
					findTokenByNodeID(t, r.State.Tokens, "task").State)
				assert.False(t, hasFailInstance(r.Commands),
					"incident must not emit FailInstance")
			},
		},
		{
			name: "recovery flow pre-empts incident and boundary",
			def:  recoveryFlowDef(terminal),
			assert: func(t *testing.T, r engine.StepResult) {
				assert.Empty(t, r.State.Incidents, "recovery flow must pre-empt incident")
				assert.True(t, hasInvokeActionForName(r.Commands, "compensate"),
					"token must be routed down RecoveryFlow into the recover task")
				assert.Equal(t, "boom", r.State.Variables["_errorMessage"])
				assert.Equal(t, 1, r.State.Variables["_errorAttempts"])
			},
		},
		{
			name: "error boundary still catches with a policy present",
			def:  boundaryWithPolicyDef(terminal),
			assert: func(t *testing.T, r engine.StepResult) {
				assert.Empty(t, r.State.Incidents, "boundary catch must pre-empt incident")
				assert.NotEqual(t, engine.StatusFailed, r.State.Status,
					"boundary catch must not fail the instance")
				assert.True(t, hasInvokeActionForName(r.Commands, "compensate"),
					"boundary must route to the recover task")
				assert.False(t, hasFailInstance(r.Commands),
					"boundary catch must not emit FailInstance")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			at0 := time.Unix(0, 0)
			r1, err := engine.Step(tc.def, engine.InstanceState{InstanceID: "p"},
				engine.NewStartInstance(at0, nil), engine.StepOptions{})
			require.NoError(t, err)
			cmdID := findInvokeActionCmdID(t, r1.Commands)

			r2, err := engine.Step(tc.def, r1.State,
				engine.NewActionFailed(time.Unix(1, 0), cmdID, "boom", true),
				engine.StepOptions{})
			require.NoError(t, err)
			tc.assert(t, r2)
		})
	}
}

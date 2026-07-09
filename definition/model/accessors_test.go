package model_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/gateway"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
)

func TestRetryPolicyOf(t *testing.T) {
	p := &model.RetryPolicy{MaxAttempts: 5}
	n := activity.NewServiceTask("a", activity.WithTaskAction("act"), activity.WithRetryPolicy(p))
	if model.RetryPolicyOf(n) != p {
		t.Fatal("RetryPolicyOf did not return the activity's policy")
	}
	if model.RetryPolicyOf(event.NewStart("s")) != nil {
		t.Fatal("non-activity must return nil")
	}
	// Test all activity kinds
	cases := []model.Node{
		activity.NewUserTask("ut", nil, activity.WithRetryPolicy(p)),
		activity.NewReceiveTask("rt", "msg", activity.WithRetryPolicy(p)),
		activity.NewSendTask("st", "msg", activity.WithRetryPolicy(p)),
		activity.NewBusinessRuleTask("brt", activity.WithTaskAction("act"), activity.WithRetryPolicy(p)),
		activity.NewSubProcess("sp", nil, activity.WithRetryPolicy(p)),
		activity.NewCallActivity("ca", model.Latest("ref"), activity.WithRetryPolicy(p)),
	}
	for _, c := range cases {
		require.Equal(t, p, model.RetryPolicyOf(c), "kind %v should return policy", c.Kind())
	}
	// Non-activity kinds should return nil
	nonActivities := []model.Node{
		event.NewStart("s"),
		event.NewEnd("e"),
		event.NewTerminateEnd("te"),
		event.NewErrorEnd("ee", "ERR"),
		gateway.NewExclusive("xor"),
		gateway.NewParallel("par"),
		gateway.NewInclusive("inc"),
		gateway.NewEventBased("ebg"),
		event.NewBoundary("be", "host"),
		event.NewIntermediateCatch("ice"),
		event.NewIntermediateThrow("ite"),
		event.NewEventSubProcess("esp", nil),
	}
	for _, c := range nonActivities {
		require.Nil(t, model.RetryPolicyOf(c), "kind %v should return nil", c.Kind())
	}
}

func TestDeadlineOf(t *testing.T) {
	assert := func(t *testing.T, spec schedule.TriggerSpec, flow, action string, wantFlow, wantAct string, wantDur time.Duration) {
		t.Helper()
		d, ok := spec.Duration()
		if !ok || d != wantDur {
			t.Errorf("DeadlineOf: duration = %v (ok=%v), want %v", d, ok, wantDur)
		}
		if flow != wantFlow {
			t.Errorf("DeadlineOf: flow = %q, want %q", flow, wantFlow)
		}
		if action != wantAct {
			t.Errorf("DeadlineOf: action = %q, want %q", action, wantAct)
		}
	}

	t.Run("service task with deadline", func(t *testing.T) {
		n := activity.NewServiceTask("st", activity.WithTaskAction("act"),
			activity.WithWaitDeadline(schedule.AfterDuration(24*time.Hour), "sla-flow"), activity.WithDeadlineAction("sla-act"))
		spec, fl, act := model.DeadlineOf(n)
		assert(t, spec, fl, act, "sla-flow", "sla-act", 24*time.Hour)
	})
	t.Run("user task with deadline", func(t *testing.T) {
		n := activity.NewUserTask("ut", nil,
			activity.WithWaitDeadline(schedule.AfterDuration(2*time.Hour), "ut-flow"), activity.WithDeadlineAction("ut-act"))
		spec, fl, act := model.DeadlineOf(n)
		assert(t, spec, fl, act, "ut-flow", "ut-act", 2*time.Hour)
	})
	t.Run("intermediate catch event with deadline", func(t *testing.T) {
		n := event.NewIntermediateCatch("ice",
			event.WithWaitDeadline(schedule.AfterDuration(48*time.Hour), "ice-flow"), event.WithDeadlineAction("ice-act"))
		spec, fl, act := model.DeadlineOf(n)
		assert(t, spec, fl, act, "ice-flow", "ice-act", 48*time.Hour)
	})
	t.Run("start event returns zero", func(t *testing.T) {
		spec, fl, act := model.DeadlineOf(event.NewStart("s"))
		if !spec.IsZero() || fl != "" || act != "" {
			t.Errorf("DeadlineOf(start) = %v %q %q, want zero", spec, fl, act)
		}
	})
}

func TestWaitActionOf(t *testing.T) {
	p := &model.RetryPolicy{MaxAttempts: 3, InitialInterval: time.Second, BackoffCoef: 2}
	n := activity.NewUserTask("ut", nil,
		activity.WithRetryPolicy(p),
		activity.WithWaitAction(schedule.Every(4*time.Hour), "send-reminder"),
	)
	every, act := model.WaitActionOf(n)
	d, ok := every.Duration()
	require.True(t, ok)
	assert.Equal(t, 4*time.Hour, d)
	assert.Equal(t, "send-reminder", act)

	// Non-activity returns zero TriggerSpec + empty action
	every, act = model.WaitActionOf(event.NewStart("s"))
	assert.True(t, every.IsZero())
	assert.Equal(t, "", act)

	// IntermediateCatchEvent with ICE reminder
	ice := event.NewIntermediateCatch("ice", event.WithWaitAction(schedule.Every(2*time.Hour), "ice-remind"))
	every, act = model.WaitActionOf(ice)
	d, ok = every.Duration()
	require.True(t, ok)
	assert.Equal(t, 2*time.Hour, d)
	assert.Equal(t, "ice-remind", act)
}

func TestCompletionActionOf(t *testing.T) {
	assert.Equal(t, "recordApproval",
		model.CompletionActionOf(activity.NewUserTask("ut", nil, activity.WithCompletionAction("recordApproval"))))
	assert.Equal(t, "ackOrder",
		model.CompletionActionOf(activity.NewReceiveTask("rt", "msg", activity.WithCompletionAction("ackOrder"))))
	assert.Equal(t, "", model.CompletionActionOf(activity.NewUserTask("ut2", nil)))
	assert.Equal(t, "", model.CompletionActionOf(event.NewStart("s")))
}

func TestActionOf(t *testing.T) {
	assert.Equal(t, "charge-card", model.ActionOf(activity.NewServiceTask("st", activity.WithTaskAction("charge-card"))))
	assert.Equal(t, "apply-discount", model.ActionOf(activity.NewBusinessRuleTask("brt", activity.WithTaskAction("apply-discount"))))
	assert.Equal(t, "", model.ActionOf(activity.NewUserTask("ut", nil)))
	assert.Equal(t, "", model.ActionOf(event.NewStart("s")))
}

// TestProcessDefinitionJSONRoundTrip verifies that Marshal(def) then Unmarshal
// produces a definition equal to the original.
func TestProcessDefinitionJSONRoundTrip(t *testing.T) {
	p := &model.RetryPolicy{MaxAttempts: 3, InitialInterval: time.Second, BackoffCoef: 2}
	original := &model.ProcessDefinition{
		ID:      "order",
		Version: 2,
		Nodes: []model.Node{
			event.NewStart("start", event.WithName("Start")),
			activity.NewServiceTask("charge",
				activity.WithTaskAction("charge-card"),
				activity.WithCompensateAction("refund-card"),
				activity.WithRecoveryFlow("f-error"),
				activity.WithRetryPolicy(p),
				activity.WithWaitDeadline(schedule.AfterDuration(24*time.Hour), "sla-flow"), activity.WithDeadlineAction("sla-act"),
				activity.WithCancelAction("cancel-charge"),
			),
			activity.NewUserTask("approve", []string{"manager", "admin"},
				activity.WithEligibilityExpr("amount > 1000"),
				activity.WithName("Approve"),
				activity.WithWaitAction(schedule.Every(4*time.Hour), "remind-act"),
			),
			event.NewIntermediateCatch("wait",
				event.WithCatchTimer(schedule.AfterExpr("PT30M")),
				event.WithName("Wait"),
			),
			event.NewIntermediateThrow("signal-done", event.WithThrowSignalName("order.done")),
			event.NewBoundary("error-bnd", "charge",
				event.WithBoundaryErrorCode("ERR_PAYMENT"),
			),
			gateway.NewExclusive("xor", "Decision"),
			event.NewEnd("end", "End"),
			event.NewErrorEnd("err-end", "ERR_FATAL"),
			event.NewTerminateEnd("term-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "charge"},
			{ID: "f2", Source: "charge", Target: "approve"},
			{ID: "f3", Source: "approve", Target: "xor"},
			{ID: "f4", Source: "xor", Target: "end", IsDefault: true},
			{ID: "f5", Source: "xor", Target: "wait", Condition: "retry"},
			{ID: "f6", Source: "wait", Target: "charge"},
			{ID: "f7", Source: "signal-done", Target: "end"},
			{ID: "f-error", Source: "charge", Target: "err-end"},
			{ID: "f8", Source: "error-bnd", Target: "term-end"},
		},
		CancelActions: []string{"notify-cancel"},
	}

	data, err := json.Marshal(original)
	require.NoError(t, err, "marshal should not fail")

	var restored model.ProcessDefinition
	require.NoError(t, json.Unmarshal(data, &restored), "unmarshal should not fail")

	// Verify key structural properties
	assert.Equal(t, original.ID, restored.ID)
	assert.Equal(t, original.Version, restored.Version)
	assert.Len(t, restored.Nodes, len(original.Nodes))
	assert.Len(t, restored.Flows, len(original.Flows))
	assert.Equal(t, original.CancelActions, restored.CancelActions)

	// Verify kind discrimination
	for i, orig := range original.Nodes {
		got := restored.Nodes[i]
		assert.Equal(t, orig.Kind(), got.Kind(), "node[%d] kind mismatch", i)
		assert.Equal(t, orig.ID(), got.ID(), "node[%d] ID mismatch", i)
		assert.Equal(t, orig.Name(), got.Name(), "node[%d] Name mismatch", i)
	}

	// Verify ServiceTask fields survived round-trip
	charge, ok := restored.Nodes[1].(activity.ServiceTask)
	require.True(t, ok)
	assert.Equal(t, "charge-card", charge.Action)
	assert.Equal(t, "refund-card", charge.CompensateAction)
	assert.Equal(t, "f-error", charge.RecoveryFlow)
	assert.Equal(t, "cancel-charge", charge.CancelAction)
	// DeadlineTimer is now schedule.TriggerSpec; verify via Duration()
	deadlineDur, deadlineOk := charge.DeadlineTimer.Duration()
	require.True(t, deadlineOk, "DeadlineTimer should be set")
	assert.Equal(t, 24*time.Hour, deadlineDur)
	require.NotNil(t, charge.RetryPolicy)
	assert.Equal(t, 3, charge.RetryPolicy.MaxAttempts)

	// Verify UserTask fields
	approve, ok := restored.Nodes[2].(activity.UserTask)
	require.True(t, ok)
	assert.Equal(t, []string{"manager", "admin"}, approve.CandidateRoles)
	assert.Equal(t, "amount > 1000", approve.EligibilityExpr)
	// WaitEvery moved from charge (ServiceTask) to approve (UserTask) — verify it survived round-trip
	reminderDur, reminderOk := approve.WaitEvery.Duration()
	require.True(t, reminderOk, "WaitEvery should be set on UserTask")
	assert.Equal(t, 4*time.Hour, reminderDur)
}

// TestProcessDefinitionJSONBackwardCompat verifies that legacy flat-shaped JSON
// (the pre-interface Node struct layout) still decodes into correct concrete types.
func TestProcessDefinitionJSONBackwardCompat(t *testing.T) {
	// Hand-written flat JSON matching the pre-interface Node struct layout.
	legacyJSON := `{
		"id": "legacy",
		"version": 1,
		"nodes": [
			{"id": "start", "kind": "startEvent", "name": "Start"},
			{"id": "charge", "kind": "serviceTask", "action": "charge-card", "compensateAction": "refund-card", "cancelAction": "cancel-charge"},
			{"id": "approve", "kind": "userTask", "candidateRoles": ["manager"], "eligibilityExpr": "amount > 1000"},
			{"id": "wait", "kind": "intermediateCatchEvent", "timerDuration": "PT1H"},
			{"id": "throw", "kind": "intermediateThrowEvent", "signalName": "done"},
			{"id": "bnd", "kind": "boundaryEvent", "attachedTo": "charge", "errorCode": "ERR", "nonInterrupting": false},
			{"id": "sp", "kind": "subProcess", "subprocess": {"id": "inner", "version": 1, "nodes": [{"id": "ns", "kind": "startEvent"}, {"id": "ne", "kind": "endEvent"}], "flows": [{"id": "nf1", "source": "ns", "target": "ne"}]}},
			{"id": "ca", "kind": "callActivity", "defRef": "ext-process"},
			{"id": "xor", "kind": "exclusiveGateway"},
			{"id": "par", "kind": "parallelGateway"},
			{"id": "inc", "kind": "inclusiveGateway"},
			{"id": "ebg", "kind": "eventBasedGateway"},
			{"id": "end", "kind": "endEvent"},
			{"id": "terend", "kind": "terminateEndEvent"},
			{"id": "errend", "kind": "errorEndEvent", "errorCode": "FATAL"},
			{"id": "send", "kind": "sendTask", "messageName": "msg.send"},
			{"id": "recv", "kind": "receiveTask", "messageName": "msg.recv", "correlationKey": "order.id"},
			{"id": "brt", "kind": "businessRuleTask", "action": "apply-discount"},
			{"id": "esp", "kind": "eventSubProcess", "subprocess": {"id": "esp-inner", "version": 1, "nodes": [{"id": "es", "kind": "startEvent"}, {"id": "ee", "kind": "endEvent"}], "flows": [{"id": "ef1", "source": "es", "target": "ee"}]}},
			{"id": "comp-throw", "kind": "intermediateThrowEvent", "compensateRef": "charge"}
		],
		"flows": []
	}`

	var def model.ProcessDefinition
	require.NoError(t, json.Unmarshal([]byte(legacyJSON), &def))

	assert.Equal(t, "legacy", def.ID)
	require.Len(t, def.Nodes, 20)

	// Spot-check types
	_, ok := def.Nodes[0].(event.StartEvent)
	require.True(t, ok, "nodes[0] should be StartEvent")

	st, ok := def.Nodes[1].(activity.ServiceTask)
	require.True(t, ok, "nodes[1] should be ServiceTask")
	assert.Equal(t, "charge-card", st.Action)
	assert.Equal(t, "refund-card", st.CompensateAction)
	assert.Equal(t, "cancel-charge", st.CancelAction)

	ut, ok := def.Nodes[2].(activity.UserTask)
	require.True(t, ok, "nodes[2] should be UserTask")
	assert.Equal(t, []string{"manager"}, ut.CandidateRoles)
	assert.Equal(t, "amount > 1000", ut.EligibilityExpr)

	ice, ok := def.Nodes[3].(event.IntermediateCatchEvent)
	require.True(t, ok, "nodes[3] should be IntermediateCatchEvent")
	assert.False(t, ice.Timer.IsZero(), "Timer should be set")
	iceExpr, _, iceExprOk := ice.Timer.Expr()
	require.True(t, iceExprOk, "legacy timerDuration should decode as expr form")
	assert.Equal(t, "PT1H", iceExpr, "legacy timerDuration PT1H should be preserved as expr value")

	ite, ok := def.Nodes[4].(event.IntermediateThrowEvent)
	require.True(t, ok, "nodes[4] should be IntermediateThrowEvent")
	assert.Equal(t, "done", ite.SignalName)

	be, ok := def.Nodes[5].(event.BoundaryEvent)
	require.True(t, ok, "nodes[5] should be BoundaryEvent")
	assert.Equal(t, "charge", be.AttachedTo)
	assert.Equal(t, "ERR", be.ErrorCode)

	sp, ok := def.Nodes[6].(activity.SubProcess)
	require.True(t, ok, "nodes[6] should be SubProcess")
	require.NotNil(t, sp.Subprocess)
	assert.Equal(t, "inner", sp.Subprocess.ID)

	ca, ok := def.Nodes[7].(activity.CallActivity)
	require.True(t, ok, "nodes[7] should be CallActivity")
	assert.Equal(t, model.Latest("ext-process"), ca.DefRef)

	_, ok = def.Nodes[8].(gateway.ExclusiveGateway)
	require.True(t, ok, "nodes[8] should be ExclusiveGateway")

	_, ok = def.Nodes[9].(gateway.ParallelGateway)
	require.True(t, ok, "nodes[9] should be ParallelGateway")

	_, ok = def.Nodes[10].(gateway.InclusiveGateway)
	require.True(t, ok, "nodes[10] should be InclusiveGateway")

	_, ok = def.Nodes[11].(gateway.EventBasedGateway)
	require.True(t, ok, "nodes[11] should be EventBasedGateway")

	_, ok = def.Nodes[12].(event.EndEvent)
	require.True(t, ok, "nodes[12] should be EndEvent")

	_, ok = def.Nodes[13].(event.TerminateEndEvent)
	require.True(t, ok, "nodes[13] should be TerminateEndEvent")

	ee, ok := def.Nodes[14].(event.ErrorEndEvent)
	require.True(t, ok, "nodes[14] should be ErrorEndEvent")
	assert.Equal(t, "FATAL", ee.ErrorCode)

	send, ok := def.Nodes[15].(activity.SendTask)
	require.True(t, ok, "nodes[15] should be SendTask")
	assert.Equal(t, "msg.send", send.MessageName)

	recv, ok := def.Nodes[16].(activity.ReceiveTask)
	require.True(t, ok, "nodes[16] should be ReceiveTask")
	assert.Equal(t, "msg.recv", recv.MessageName)
	assert.Equal(t, "order.id", recv.CorrelationKey)

	brt, ok := def.Nodes[17].(activity.BusinessRuleTask)
	require.True(t, ok, "nodes[17] should be BusinessRuleTask")
	assert.Equal(t, "apply-discount", brt.Action)

	esp, ok := def.Nodes[18].(event.EventSubProcess)
	require.True(t, ok, "nodes[18] should be EventSubProcess")
	require.NotNil(t, esp.Subprocess)

	compThrow, ok := def.Nodes[19].(event.IntermediateThrowEvent)
	require.True(t, ok, "nodes[19] should be IntermediateThrowEvent")
	assert.Equal(t, "charge", compThrow.CompensateRef)
}

// TestDeadlineReminderTyped verifies that DeadlineOf and WaitActionOf return
// schedule.TriggerSpec values (Task 3: typed deadline/wait migration).
func TestDeadlineReminderTyped(t *testing.T) {
	n := activity.NewUserTask("ut", nil,
		activity.WithWaitDeadline(schedule.AfterDuration(2*time.Hour), "sla"), activity.WithDeadlineAction("notify"),
		activity.WithWaitAction(schedule.Every(time.Hour), "remind"),
	)
	spec, flow, action := model.DeadlineOf(n)
	if d, ok := spec.Duration(); !ok || d != 2*time.Hour || flow != "sla" || action != "notify" {
		t.Fatalf("DeadlineOf = %v %q %q", d, flow, action)
	}
	every, ra := model.WaitActionOf(n)
	if d, ok := every.Duration(); !ok || d != time.Hour || ra != "remind" {
		t.Fatalf("WaitActionOf = %v %q", d, ra)
	}
}

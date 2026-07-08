package model_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/gateway"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
)

func TestServiceTaskConstructorAndAccessors(t *testing.T) {
	n := activity.NewServiceTask("pay",
		activity.WithActionName("charge-card"),
		activity.WithCompensation("refund-card"),
		activity.WithRecoveryFlow("to-manual"),
	)
	if n.Kind() != model.KindServiceTask {
		t.Fatalf("Kind() = %v, want KindServiceTask", n.Kind())
	}
	if n.ID() != "pay" {
		t.Fatalf("ID() = %q, want pay", n.ID())
	}
	st, ok := n.(activity.ServiceTask)
	if !ok {
		t.Fatalf("node is %T, want activity.ServiceTask", n)
	}
	if st.Action != "charge-card" || st.CompensationAction != "refund-card" || st.RecoveryFlow != "to-manual" {
		t.Fatalf("fields = %+v", st)
	}
}

func TestStartEventConstructor(t *testing.T) {
	n := event.NewStart("start")
	if n.Kind() != model.KindStartEvent {
		t.Fatalf("Kind() = %v, want KindStartEvent", n.Kind())
	}
	if n.ID() != "start" {
		t.Fatalf("ID() = %q, want start", n.ID())
	}
	if n.Name() != "" {
		t.Fatalf("Name() = %q, want empty", n.Name())
	}
}

func TestStartEventConstructorWithName(t *testing.T) {
	n := event.NewStart("s", event.WithName("Start Process"))
	if n.Name() != "Start Process" {
		t.Fatalf("Name() = %q, want 'Start Process'", n.Name())
	}
}

func TestEndEventConstructor(t *testing.T) {
	n := event.NewEnd("end")
	if n.Kind() != model.KindEndEvent {
		t.Fatalf("Kind() = %v, want KindEndEvent", n.Kind())
	}
}

func TestTerminateEndEventConstructor(t *testing.T) {
	n := event.NewTerminateEnd("t-end")
	if n.Kind() != model.KindTerminateEndEvent {
		t.Fatalf("Kind() = %v, want KindTerminateEndEvent", n.Kind())
	}
}

func TestErrorEndEventConstructor(t *testing.T) {
	n := event.NewErrorEnd("err-end", "ERR_PAYMENT")
	if n.Kind() != model.KindErrorEndEvent {
		t.Fatalf("Kind() = %v, want KindErrorEndEvent", n.Kind())
	}
	ee, ok := n.(event.ErrorEndEvent)
	if !ok {
		t.Fatalf("node is %T, want event.ErrorEndEvent", n)
	}
	if ee.ErrorCode != "ERR_PAYMENT" {
		t.Fatalf("ErrorCode = %q, want ERR_PAYMENT", ee.ErrorCode)
	}
}

func TestUserTaskConstructor(t *testing.T) {
	n := activity.NewUserTask("task-1", []string{"manager", "admin"},
		activity.WithEligibilityExpr("amount > 1000"),
		activity.WithDeadline(schedule.AfterDuration(24*time.Hour), "sla-breach", "notify-manager"),
		activity.WithWaitReminder(schedule.Every(4*time.Hour), "send-reminder"),
	)
	if n.Kind() != model.KindUserTask {
		t.Fatalf("Kind() = %v, want KindUserTask", n.Kind())
	}
	ut, ok := n.(activity.UserTask)
	if !ok {
		t.Fatalf("node is %T, want activity.UserTask", n)
	}
	if ut.EligibilityExpr != "amount > 1000" {
		t.Fatalf("EligibilityExpr = %q", ut.EligibilityExpr)
	}
	if len(ut.CandidateRoles) != 2 || ut.CandidateRoles[0] != "manager" {
		t.Fatalf("CandidateRoles = %v", ut.CandidateRoles)
	}
	if dd, ok := ut.DeadlineTimer.Duration(); !ok || dd != 24*time.Hour || ut.DeadlineFlow != "sla-breach" || ut.DeadlineAction != "notify-manager" {
		t.Fatalf("deadline fields = %v/%q/%q", dd, ut.DeadlineFlow, ut.DeadlineAction)
	}
	if rd, ok := ut.ReminderEvery.Duration(); !ok || rd != 4*time.Hour || ut.ReminderAction != "send-reminder" {
		t.Fatalf("reminder fields = %v/%q", rd, ut.ReminderAction)
	}
}

func TestReceiveTaskConstructor(t *testing.T) {
	n := activity.NewReceiveTask("recv", "payment.received",
		activity.WithCorrelationKey("order.id"),
		activity.WithCancelHandler("cancel-payment"),
	)
	if n.Kind() != model.KindReceiveTask {
		t.Fatalf("Kind() = %v, want KindReceiveTask", n.Kind())
	}
	rt, ok := n.(activity.ReceiveTask)
	if !ok {
		t.Fatalf("node is %T, want activity.ReceiveTask", n)
	}
	if rt.MessageName != "payment.received" {
		t.Fatalf("MessageName = %q", rt.MessageName)
	}
	if rt.CorrelationKey != "order.id" {
		t.Fatalf("CorrelationKey = %q", rt.CorrelationKey)
	}
	if rt.CancelHandler != "cancel-payment" {
		t.Fatalf("CancelHandler = %q", rt.CancelHandler)
	}
}

func TestSendTaskConstructor(t *testing.T) {
	n := activity.NewSendTask("send", "order.shipped")
	if n.Kind() != model.KindSendTask {
		t.Fatalf("Kind() = %v, want KindSendTask", n.Kind())
	}
	st, ok := n.(activity.SendTask)
	if !ok {
		t.Fatalf("node is %T, want activity.SendTask", n)
	}
	if st.MessageName != "order.shipped" {
		t.Fatalf("MessageName = %q", st.MessageName)
	}
}

func TestSendTaskCorrelationKey(t *testing.T) {
	n := activity.NewSendTask("send", "order.shipped", activity.WithCorrelationKey(`vars.orderID`))
	st, ok := n.(activity.SendTask)
	if !ok {
		t.Fatalf("node is %T, want activity.SendTask", n)
	}
	if st.CorrelationKey != `vars.orderID` {
		t.Fatalf("CorrelationKey = %q, want %q", st.CorrelationKey, `vars.orderID`)
	}
}

func TestBusinessRuleTaskConstructor(t *testing.T) {
	n := activity.NewBusinessRuleTask("brt", activity.WithActionName("apply-discount"))
	if n.Kind() != model.KindBusinessRuleTask {
		t.Fatalf("Kind() = %v, want KindBusinessRuleTask", n.Kind())
	}
	brt, ok := n.(activity.BusinessRuleTask)
	if !ok {
		t.Fatalf("node is %T, want activity.BusinessRuleTask", n)
	}
	if brt.Action != "apply-discount" {
		t.Fatalf("Action = %q", brt.Action)
	}
}

func TestSubProcessConstructor(t *testing.T) {
	sub := &model.ProcessDefinition{ID: "sub", Version: 1}
	n := activity.NewSubProcess("sp", sub)
	if n.Kind() != model.KindSubProcess {
		t.Fatalf("Kind() = %v, want KindSubProcess", n.Kind())
	}
	sp, ok := n.(activity.SubProcess)
	if !ok {
		t.Fatalf("node is %T, want activity.SubProcess", n)
	}
	if sp.Subprocess != sub {
		t.Fatal("Subprocess pointer not preserved")
	}
}

func TestCallActivityConstructor(t *testing.T) {
	n := activity.NewCallActivity("ca", model.Latest("external-v2"))
	if n.Kind() != model.KindCallActivity {
		t.Fatalf("Kind() = %v, want KindCallActivity", n.Kind())
	}
	ca, ok := n.(activity.CallActivity)
	if !ok {
		t.Fatalf("node is %T, want activity.CallActivity", n)
	}
	if ca.DefRef != model.Latest("external-v2") {
		t.Fatalf("DefRef = %v", ca.DefRef)
	}
}

func TestEventSubProcessConstructor(t *testing.T) {
	sub := &model.ProcessDefinition{ID: "esp-sub", Version: 1}
	n := event.NewEventSubProcess("esp", sub)
	if n.Kind() != model.KindEventSubProcess {
		t.Fatalf("Kind() = %v, want KindEventSubProcess", n.Kind())
	}
	esp, ok := n.(event.EventSubProcess)
	if !ok {
		t.Fatalf("node is %T, want event.EventSubProcess", n)
	}
	if esp.Subprocess != sub {
		t.Fatal("Subprocess pointer not preserved")
	}
}

func TestIntermediateCatchEventConstructor(t *testing.T) {
	n := event.NewCatch("ice",
		event.WithCatchTimer(schedule.AfterExpr("PT1H")),
		event.WithCatchDeadline(schedule.AfterDuration(24*time.Hour), "sla-flow", "sla-act"),
		event.WithCatchWaitReminder(schedule.Every(2*time.Hour), "remind-act"),
	)
	if n.Kind() != model.KindIntermediateCatchEvent {
		t.Fatalf("Kind() = %v, want KindIntermediateCatchEvent", n.Kind())
	}
	ice, ok := n.(event.IntermediateCatchEvent)
	if !ok {
		t.Fatalf("node is %T, want event.IntermediateCatchEvent", n)
	}
	if ice.Timer.IsZero() {
		t.Fatalf("Timer is zero, want AfterExpr(PT1H)")
	}
	if dd, ok := ice.DeadlineTimer.Duration(); !ok || dd != 24*time.Hour {
		t.Fatalf("DeadlineTimer = %v (ok=%v)", dd, ok)
	}
}

func TestIntermediateCatchEventSignal(t *testing.T) {
	n := event.NewCatch("ice-sig", event.WithCatchSignal("my.signal"))
	ice, ok := n.(event.IntermediateCatchEvent)
	if !ok {
		t.Fatalf("node is %T, want event.IntermediateCatchEvent", n)
	}
	if ice.SignalName != "my.signal" {
		t.Fatalf("SignalName = %q", ice.SignalName)
	}
}

func TestIntermediateCatchEventMessage(t *testing.T) {
	n := event.NewCatch("ice-msg",
		event.WithCatchMessage("payment.received", "order.id"),
	)
	ice, ok := n.(event.IntermediateCatchEvent)
	if !ok {
		t.Fatalf("node is %T, want event.IntermediateCatchEvent", n)
	}
	if ice.MessageName != "payment.received" {
		t.Fatalf("MessageName = %q", ice.MessageName)
	}
	if ice.CorrelationKey != "order.id" {
		t.Fatalf("CorrelationKey = %q", ice.CorrelationKey)
	}
}

func TestIntermediateThrowEventConstructor(t *testing.T) {
	n := event.NewThrow("ite",
		event.WithThrowSignal("order.shipped"),
	)
	if n.Kind() != model.KindIntermediateThrowEvent {
		t.Fatalf("Kind() = %v, want KindIntermediateThrowEvent", n.Kind())
	}
	ite, ok := n.(event.IntermediateThrowEvent)
	if !ok {
		t.Fatalf("node is %T, want event.IntermediateThrowEvent", n)
	}
	if ite.SignalName != "order.shipped" {
		t.Fatalf("SignalName = %q", ite.SignalName)
	}
}

func TestIntermediateThrowEventCompensateRef(t *testing.T) {
	n := event.NewThrow("comp-throw",
		event.WithCompensateRef("my-task"),
	)
	ite, ok := n.(event.IntermediateThrowEvent)
	if !ok {
		t.Fatalf("node is %T, want event.IntermediateThrowEvent", n)
	}
	if ite.CompensateRef != "my-task" {
		t.Fatalf("CompensateRef = %q", ite.CompensateRef)
	}
}

func TestBoundaryEventConstructor(t *testing.T) {
	n := event.NewBoundary("bnd", "task-1",
		event.WithBoundarySignal("cancel.signal"),
		event.WithBoundaryNonInterrupting(),
	)
	if n.Kind() != model.KindBoundaryEvent {
		t.Fatalf("Kind() = %v, want KindBoundaryEvent", n.Kind())
	}
	be, ok := n.(event.BoundaryEvent)
	if !ok {
		t.Fatalf("node is %T, want event.BoundaryEvent", n)
	}
	if be.AttachedTo != "task-1" {
		t.Fatalf("AttachedTo = %q", be.AttachedTo)
	}
	if be.SignalName != "cancel.signal" {
		t.Fatalf("SignalName = %q", be.SignalName)
	}
	if !be.NonInterrupting {
		t.Fatal("NonInterrupting should be true")
	}
}

func TestGatewayConstructors(t *testing.T) {
	cases := []struct {
		n    model.Node
		kind model.NodeKind
	}{
		{gateway.NewExclusive("xor"), model.KindExclusiveGateway},
		{gateway.NewParallel("par"), model.KindParallelGateway},
		{gateway.NewInclusive("inc"), model.KindInclusiveGateway},
		{gateway.NewEventBased("ebg"), model.KindEventBasedGateway},
	}
	for _, tc := range cases {
		if tc.n.Kind() != tc.kind {
			t.Errorf("Kind() = %v, want %v", tc.n.Kind(), tc.kind)
		}
	}
}

func TestWithNameOnActivities(t *testing.T) {
	// WithName option should work on all kinds.
	n := activity.NewServiceTask("st", activity.WithActionName("act"), activity.WithName("My Task"))
	if n.Name() != "My Task" {
		t.Fatalf("Name() = %q, want 'My Task'", n.Name())
	}

	n2 := activity.NewUserTask("ut", nil, activity.WithName("User Step"))
	if n2.Name() != "User Step" {
		t.Fatalf("Name() = %q, want 'User Step'", n2.Name())
	}

	n3 := event.NewBoundary("bnd", "host", event.WithName("Timer Boundary"))
	if n3.Name() != "Timer Boundary" {
		t.Fatalf("Name() = %q, want 'Timer Boundary'", n3.Name())
	}

	n4 := event.NewCatch("ice", event.WithName("Wait"))
	if n4.Name() != "Wait" {
		t.Fatalf("Name() = %q, want 'Wait'", n4.Name())
	}
}

func TestRetryPolicyOption(t *testing.T) {
	p := &model.RetryPolicy{MaxAttempts: 5}
	n := activity.NewServiceTask("st", activity.WithActionName("act"), activity.WithRetryPolicy(p))
	st, _ := n.(activity.ServiceTask)
	if st.RetryPolicy != p {
		t.Fatal("RetryPolicy not set")
	}
}

func TestEventSubProcessNonInterrupting(t *testing.T) {
	sub := &model.ProcessDefinition{ID: "esp-ni-sub", Version: 1}
	n := event.NewEventSubProcess("esp-ni", sub, event.WithEventSubProcessNonInterrupting())
	esp, ok := n.(event.EventSubProcess)
	if !ok {
		t.Fatalf("node is %T, want event.EventSubProcess", n)
	}
	if !esp.NonInterrupting {
		t.Fatal("NonInterrupting should be true when WithESPNonInterrupting is used")
	}
}

// TestUserTaskCombinedOptions verifies that WithEligibilityExpr, WithName, and
// WithRetryPolicy can all be combined on NewUserTask and that each field is set.
func TestUserTaskCombinedOptions(t *testing.T) {
	p := &model.RetryPolicy{MaxAttempts: 1}
	n := activity.NewUserTask("u", []string{"reviewer"},
		activity.WithEligibilityExpr("vars.score > 50"),
		activity.WithName("Review Task"),
		activity.WithRetryPolicy(p),
	)
	ut, ok := n.(activity.UserTask)
	if !ok {
		t.Fatalf("node is %T, want activity.UserTask", n)
	}
	if ut.EligibilityExpr != "vars.score > 50" {
		t.Errorf("EligibilityExpr = %q, want %q", ut.EligibilityExpr, "vars.score > 50")
	}
	if ut.Name() != "Review Task" {
		t.Errorf("Name() = %q, want %q", ut.Name(), "Review Task")
	}
	if ut.RetryPolicy != p {
		t.Errorf("RetryPolicy not set")
	}
}

// TestReceiveTaskCombinedOptions verifies that WithCorrelationKey, WithName, and
// WithRetryPolicy can all be combined on NewReceiveTask and that each field is set.
func TestReceiveTaskCombinedOptions(t *testing.T) {
	p := &model.RetryPolicy{MaxAttempts: 2}
	n := activity.NewReceiveTask("recv-combo", "order.confirmed",
		activity.WithCorrelationKey("order.id"),
		activity.WithName("Wait For Confirmation"),
		activity.WithRetryPolicy(p),
	)
	rt, ok := n.(activity.ReceiveTask)
	if !ok {
		t.Fatalf("node is %T, want activity.ReceiveTask", n)
	}
	if rt.CorrelationKey != "order.id" {
		t.Errorf("CorrelationKey = %q, want %q", rt.CorrelationKey, "order.id")
	}
	if rt.Name() != "Wait For Confirmation" {
		t.Errorf("Name() = %q, want %q", rt.Name(), "Wait For Confirmation")
	}
	if rt.RetryPolicy != p {
		t.Errorf("RetryPolicy not set")
	}
}

// TestWithEligibilityPrivileges verifies that WithEligibilityPrivileges sets
// EligibilityPrivileges on the UserTask node, and that attempting to pass it to
// a non-UserTask constructor is a compile-time error (not tested here, by design).
func TestWithEligibilityPrivileges(t *testing.T) {
	privs := []string{"finance-task claim", "finance-task read"}
	n := activity.NewUserTask("approve", []string{"approver"},
		activity.WithEligibilityPrivileges(privs...),
	)
	ut, ok := n.(activity.UserTask)
	if !ok {
		t.Fatalf("node is %T, want activity.UserTask", n)
	}
	if len(ut.EligibilityPrivileges) != 2 {
		t.Fatalf("EligibilityPrivileges len = %d, want 2; got %v", len(ut.EligibilityPrivileges), ut.EligibilityPrivileges)
	}
	if ut.EligibilityPrivileges[0] != "finance-task claim" {
		t.Fatalf("EligibilityPrivileges[0] = %q, want %q", ut.EligibilityPrivileges[0], "finance-task claim")
	}
}

// TestWithEligibilityPrivilegesRoundTrip verifies that EligibilityPrivileges survives
// a JSON marshal/unmarshal round-trip (via NodeWire).
func TestWithEligibilityPrivilegesRoundTrip(t *testing.T) {
	privs := []string{"doc read"}
	n := activity.NewUserTask("u2", nil, activity.WithEligibilityPrivileges(privs...))
	def := &model.ProcessDefinition{
		ID:      "p",
		Version: 1,
		Nodes:   []model.Node{n},
		Flows:   []flow.SequenceFlow{},
	}
	data, err := json.Marshal(def)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded model.ProcessDefinition
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	ut, ok := decoded.Nodes[0].(activity.UserTask)
	if !ok {
		t.Fatalf("decoded node is %T, want activity.UserTask", decoded.Nodes[0])
	}
	if len(ut.EligibilityPrivileges) != 1 || ut.EligibilityPrivileges[0] != "doc read" {
		t.Fatalf("EligibilityPrivileges = %v, want [doc read]", ut.EligibilityPrivileges)
	}
}

func TestEventSubProcessNonInterruptingRoundTrip(t *testing.T) {
	inner := &model.ProcessDefinition{
		ID:      "inner",
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("s"),
			event.NewEnd("e"),
		},
		Flows: []flow.SequenceFlow{{ID: "f1", Source: "s", Target: "e"}},
	}
	outer := &model.ProcessDefinition{
		ID:      "outer",
		Version: 1,
		Nodes: []model.Node{
			event.NewEventSubProcess("esp-ni", inner,
				event.WithEventSubProcessNonInterrupting(),
				event.WithName("Non-Interrupting ESP"),
			),
		},
		Flows: []flow.SequenceFlow{},
	}

	data, err := json.Marshal(outer)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded model.ProcessDefinition
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if len(decoded.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(decoded.Nodes))
	}
	esp, ok := decoded.Nodes[0].(event.EventSubProcess)
	if !ok {
		t.Fatalf("decoded node is %T, want event.EventSubProcess", decoded.Nodes[0])
	}
	if !esp.NonInterrupting {
		t.Fatal("NonInterrupting not preserved through JSON round-trip")
	}
	if esp.Name() != "Non-Interrupting ESP" {
		t.Fatalf("Name = %q, want 'Non-Interrupting ESP'", esp.Name())
	}
}

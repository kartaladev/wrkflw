package model_test

import (
	"testing"

	"github.com/zakyalvan/krtlwrkflw/model"
)

func TestServiceTaskConstructorAndAccessors(t *testing.T) {
	n := model.NewServiceTask("pay", "charge-card",
		model.WithCompensation("refund-card"),
		model.WithRecoveryFlow("to-manual"),
	)
	if n.Kind() != model.KindServiceTask {
		t.Fatalf("Kind() = %v, want KindServiceTask", n.Kind())
	}
	if n.ID() != "pay" {
		t.Fatalf("ID() = %q, want pay", n.ID())
	}
	st, ok := n.(model.ServiceTask)
	if !ok {
		t.Fatalf("node is %T, want model.ServiceTask", n)
	}
	if st.Action != "charge-card" || st.CompensationAction != "refund-card" || st.RecoveryFlow != "to-manual" {
		t.Fatalf("fields = %+v", st)
	}
}

func TestStartEventConstructor(t *testing.T) {
	n := model.NewStartEvent("start")
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
	n := model.NewStartEvent("s", "Start Process")
	if n.Name() != "Start Process" {
		t.Fatalf("Name() = %q, want 'Start Process'", n.Name())
	}
}

func TestEndEventConstructor(t *testing.T) {
	n := model.NewEndEvent("end")
	if n.Kind() != model.KindEndEvent {
		t.Fatalf("Kind() = %v, want KindEndEvent", n.Kind())
	}
}

func TestTerminateEndEventConstructor(t *testing.T) {
	n := model.NewTerminateEndEvent("t-end")
	if n.Kind() != model.KindTerminateEndEvent {
		t.Fatalf("Kind() = %v, want KindTerminateEndEvent", n.Kind())
	}
}

func TestErrorEndEventConstructor(t *testing.T) {
	n := model.NewErrorEndEvent("err-end", "ERR_PAYMENT")
	if n.Kind() != model.KindErrorEndEvent {
		t.Fatalf("Kind() = %v, want KindErrorEndEvent", n.Kind())
	}
	ee, ok := n.(model.ErrorEndEvent)
	if !ok {
		t.Fatalf("node is %T, want model.ErrorEndEvent", n)
	}
	if ee.ErrorCode != "ERR_PAYMENT" {
		t.Fatalf("ErrorCode = %q, want ERR_PAYMENT", ee.ErrorCode)
	}
}

func TestUserTaskConstructor(t *testing.T) {
	n := model.NewUserTask("task-1", []string{"manager", "admin"},
		model.WithEligibilityExpr("amount > 1000"),
		model.WithSLA("P1D", "sla-breach", "notify-manager"),
		model.WithReminder("PT4H", "send-reminder"),
	)
	if n.Kind() != model.KindUserTask {
		t.Fatalf("Kind() = %v, want KindUserTask", n.Kind())
	}
	ut, ok := n.(model.UserTask)
	if !ok {
		t.Fatalf("node is %T, want model.UserTask", n)
	}
	if ut.EligibilityExpr != "amount > 1000" {
		t.Fatalf("EligibilityExpr = %q", ut.EligibilityExpr)
	}
	if len(ut.CandidateRoles) != 2 || ut.CandidateRoles[0] != "manager" {
		t.Fatalf("CandidateRoles = %v", ut.CandidateRoles)
	}
	if ut.SLADuration != "P1D" || ut.SLAFlow != "sla-breach" || ut.SLAAction != "notify-manager" {
		t.Fatalf("SLA fields = %q/%q/%q", ut.SLADuration, ut.SLAFlow, ut.SLAAction)
	}
	if ut.ReminderEvery != "PT4H" || ut.ReminderAction != "send-reminder" {
		t.Fatalf("Reminder fields = %q/%q", ut.ReminderEvery, ut.ReminderAction)
	}
}

func TestReceiveTaskConstructor(t *testing.T) {
	n := model.NewReceiveTask("recv", "payment.received",
		model.WithCorrelationKey("order.id"),
		model.WithCancelHandler("cancel-payment"),
	)
	if n.Kind() != model.KindReceiveTask {
		t.Fatalf("Kind() = %v, want KindReceiveTask", n.Kind())
	}
	rt, ok := n.(model.ReceiveTask)
	if !ok {
		t.Fatalf("node is %T, want model.ReceiveTask", n)
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
	n := model.NewSendTask("send", "order.shipped")
	if n.Kind() != model.KindSendTask {
		t.Fatalf("Kind() = %v, want KindSendTask", n.Kind())
	}
	st, ok := n.(model.SendTask)
	if !ok {
		t.Fatalf("node is %T, want model.SendTask", n)
	}
	if st.MessageName != "order.shipped" {
		t.Fatalf("MessageName = %q", st.MessageName)
	}
}

func TestBusinessRuleTaskConstructor(t *testing.T) {
	n := model.NewBusinessRuleTask("brt", "apply-discount")
	if n.Kind() != model.KindBusinessRuleTask {
		t.Fatalf("Kind() = %v, want KindBusinessRuleTask", n.Kind())
	}
	brt, ok := n.(model.BusinessRuleTask)
	if !ok {
		t.Fatalf("node is %T, want model.BusinessRuleTask", n)
	}
	if brt.Action != "apply-discount" {
		t.Fatalf("Action = %q", brt.Action)
	}
}

func TestSubProcessConstructor(t *testing.T) {
	sub := &model.ProcessDefinition{ID: "sub", Version: 1}
	n := model.NewSubProcess("sp", sub)
	if n.Kind() != model.KindSubProcess {
		t.Fatalf("Kind() = %v, want KindSubProcess", n.Kind())
	}
	sp, ok := n.(model.SubProcess)
	if !ok {
		t.Fatalf("node is %T, want model.SubProcess", n)
	}
	if sp.Subprocess != sub {
		t.Fatal("Subprocess pointer not preserved")
	}
}

func TestCallActivityConstructor(t *testing.T) {
	n := model.NewCallActivity("ca", "external-v2")
	if n.Kind() != model.KindCallActivity {
		t.Fatalf("Kind() = %v, want KindCallActivity", n.Kind())
	}
	ca, ok := n.(model.CallActivity)
	if !ok {
		t.Fatalf("node is %T, want model.CallActivity", n)
	}
	if ca.DefRef != "external-v2" {
		t.Fatalf("DefRef = %q", ca.DefRef)
	}
}

func TestEventSubProcessConstructor(t *testing.T) {
	sub := &model.ProcessDefinition{ID: "esp-sub", Version: 1}
	n := model.NewEventSubProcess("esp", sub)
	if n.Kind() != model.KindEventSubProcess {
		t.Fatalf("Kind() = %v, want KindEventSubProcess", n.Kind())
	}
	esp, ok := n.(model.EventSubProcess)
	if !ok {
		t.Fatalf("node is %T, want model.EventSubProcess", n)
	}
	if esp.Subprocess != sub {
		t.Fatal("Subprocess pointer not preserved")
	}
}

func TestIntermediateCatchEventConstructor(t *testing.T) {
	n := model.NewIntermediateCatchEvent("ice",
		model.WithTimerDuration("PT1H"),
		model.WithICESLA("P1D", "sla-flow", "sla-act"),
		model.WithICEReminder("PT2H", "remind-act"),
	)
	if n.Kind() != model.KindIntermediateCatchEvent {
		t.Fatalf("Kind() = %v, want KindIntermediateCatchEvent", n.Kind())
	}
	ice, ok := n.(model.IntermediateCatchEvent)
	if !ok {
		t.Fatalf("node is %T, want model.IntermediateCatchEvent", n)
	}
	if ice.TimerDuration != "PT1H" {
		t.Fatalf("TimerDuration = %q", ice.TimerDuration)
	}
	if ice.SLADuration != "P1D" {
		t.Fatalf("SLADuration = %q", ice.SLADuration)
	}
}

func TestIntermediateCatchEventSignal(t *testing.T) {
	n := model.NewIntermediateCatchEvent("ice-sig", model.WithSignalName("my.signal"))
	ice, ok := n.(model.IntermediateCatchEvent)
	if !ok {
		t.Fatalf("node is %T, want model.IntermediateCatchEvent", n)
	}
	if ice.SignalName != "my.signal" {
		t.Fatalf("SignalName = %q", ice.SignalName)
	}
}

func TestIntermediateCatchEventMessage(t *testing.T) {
	n := model.NewIntermediateCatchEvent("ice-msg",
		model.WithMessageNameAndKey("payment.received", "order.id"),
	)
	ice, ok := n.(model.IntermediateCatchEvent)
	if !ok {
		t.Fatalf("node is %T, want model.IntermediateCatchEvent", n)
	}
	if ice.MessageName != "payment.received" {
		t.Fatalf("MessageName = %q", ice.MessageName)
	}
	if ice.CorrelationKey != "order.id" {
		t.Fatalf("CorrelationKey = %q", ice.CorrelationKey)
	}
}

func TestIntermediateThrowEventConstructor(t *testing.T) {
	n := model.NewIntermediateThrowEvent("ite",
		model.WithThrowSignal("order.shipped"),
	)
	if n.Kind() != model.KindIntermediateThrowEvent {
		t.Fatalf("Kind() = %v, want KindIntermediateThrowEvent", n.Kind())
	}
	ite, ok := n.(model.IntermediateThrowEvent)
	if !ok {
		t.Fatalf("node is %T, want model.IntermediateThrowEvent", n)
	}
	if ite.SignalName != "order.shipped" {
		t.Fatalf("SignalName = %q", ite.SignalName)
	}
}

func TestIntermediateThrowEventCompensateRef(t *testing.T) {
	n := model.NewIntermediateThrowEvent("comp-throw",
		model.WithCompensateRef("my-task"),
	)
	ite, ok := n.(model.IntermediateThrowEvent)
	if !ok {
		t.Fatalf("node is %T, want model.IntermediateThrowEvent", n)
	}
	if ite.CompensateRef != "my-task" {
		t.Fatalf("CompensateRef = %q", ite.CompensateRef)
	}
}

func TestBoundaryEventConstructor(t *testing.T) {
	n := model.NewBoundaryEvent("bnd", "task-1",
		model.WithBoundarySignal("cancel.signal"),
		model.BoundaryNonInterrupting(),
	)
	if n.Kind() != model.KindBoundaryEvent {
		t.Fatalf("Kind() = %v, want KindBoundaryEvent", n.Kind())
	}
	be, ok := n.(model.BoundaryEvent)
	if !ok {
		t.Fatalf("node is %T, want model.BoundaryEvent", n)
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
		{model.NewExclusiveGateway("xor"), model.KindExclusiveGateway},
		{model.NewParallelGateway("par"), model.KindParallelGateway},
		{model.NewInclusiveGateway("inc"), model.KindInclusiveGateway},
		{model.NewEventBasedGateway("ebg"), model.KindEventBasedGateway},
	}
	for _, tc := range cases {
		if tc.n.Kind() != tc.kind {
			t.Errorf("Kind() = %v, want %v", tc.n.Kind(), tc.kind)
		}
	}
}

func TestWithNameOnActivities(t *testing.T) {
	// WithName option should work on all kinds.
	n := model.NewServiceTask("st", "act", model.WithName("My Task"))
	if n.Name() != "My Task" {
		t.Fatalf("Name() = %q, want 'My Task'", n.Name())
	}

	n2 := model.NewUserTask("ut", nil, model.WithName("User Step"))
	if n2.Name() != "User Step" {
		t.Fatalf("Name() = %q, want 'User Step'", n2.Name())
	}

	n3 := model.NewBoundaryEvent("bnd", "host", model.WithName("Timer Boundary"))
	if n3.Name() != "Timer Boundary" {
		t.Fatalf("Name() = %q, want 'Timer Boundary'", n3.Name())
	}

	n4 := model.NewIntermediateCatchEvent("ice", model.WithName("Wait"))
	if n4.Name() != "Wait" {
		t.Fatalf("Name() = %q, want 'Wait'", n4.Name())
	}
}

func TestRetryPolicyOption(t *testing.T) {
	p := &model.RetryPolicy{MaxAttempts: 5}
	n := model.NewServiceTask("st", "act", model.WithRetryPolicy(p))
	st, _ := n.(model.ServiceTask)
	if st.RetryPolicy != p {
		t.Fatal("RetryPolicy not set")
	}
}

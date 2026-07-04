package definition_test

import (
	"encoding/json"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/definition"
)

func TestServiceTaskConstructorAndAccessors(t *testing.T) {
	n := definition.NewServiceTask("pay",
		definition.WithActionName("charge-card"),
		definition.WithCompensation("refund-card"),
		definition.WithRecoveryFlow("to-manual"),
	)
	if n.Kind() != definition.KindServiceTask {
		t.Fatalf("Kind() = %v, want KindServiceTask", n.Kind())
	}
	if n.ID() != "pay" {
		t.Fatalf("ID() = %q, want pay", n.ID())
	}
	st, ok := n.(definition.ServiceTask)
	if !ok {
		t.Fatalf("node is %T, want definition.ServiceTask", n)
	}
	if st.Action != "charge-card" || st.CompensationAction != "refund-card" || st.RecoveryFlow != "to-manual" {
		t.Fatalf("fields = %+v", st)
	}
}

func TestStartEventConstructor(t *testing.T) {
	n := definition.NewStartEvent("start")
	if n.Kind() != definition.KindStartEvent {
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
	n := definition.NewStartEvent("s", definition.WithName("Start Process"))
	if n.Name() != "Start Process" {
		t.Fatalf("Name() = %q, want 'Start Process'", n.Name())
	}
}

func TestEndEventConstructor(t *testing.T) {
	n := definition.NewEndEvent("end")
	if n.Kind() != definition.KindEndEvent {
		t.Fatalf("Kind() = %v, want KindEndEvent", n.Kind())
	}
}

func TestTerminateEndEventConstructor(t *testing.T) {
	n := definition.NewTerminateEndEvent("t-end")
	if n.Kind() != definition.KindTerminateEndEvent {
		t.Fatalf("Kind() = %v, want KindTerminateEndEvent", n.Kind())
	}
}

func TestErrorEndEventConstructor(t *testing.T) {
	n := definition.NewErrorEndEvent("err-end", "ERR_PAYMENT")
	if n.Kind() != definition.KindErrorEndEvent {
		t.Fatalf("Kind() = %v, want KindErrorEndEvent", n.Kind())
	}
	ee, ok := n.(definition.ErrorEndEvent)
	if !ok {
		t.Fatalf("node is %T, want definition.ErrorEndEvent", n)
	}
	if ee.ErrorCode != "ERR_PAYMENT" {
		t.Fatalf("ErrorCode = %q, want ERR_PAYMENT", ee.ErrorCode)
	}
}

func TestUserTaskConstructor(t *testing.T) {
	n := definition.NewUserTask("task-1", []string{"manager", "admin"},
		definition.WithEligibilityExpr("amount > 1000"),
		definition.WithDeadline("P1D", "sla-breach", "notify-manager"),
		definition.WithReminder("PT4H", "send-reminder"),
	)
	if n.Kind() != definition.KindUserTask {
		t.Fatalf("Kind() = %v, want KindUserTask", n.Kind())
	}
	ut, ok := n.(definition.UserTask)
	if !ok {
		t.Fatalf("node is %T, want definition.UserTask", n)
	}
	if ut.EligibilityExpr != "amount > 1000" {
		t.Fatalf("EligibilityExpr = %q", ut.EligibilityExpr)
	}
	if len(ut.CandidateRoles) != 2 || ut.CandidateRoles[0] != "manager" {
		t.Fatalf("CandidateRoles = %v", ut.CandidateRoles)
	}
	if ut.DeadlineDuration != "P1D" || ut.DeadlineFlow != "sla-breach" || ut.DeadlineAction != "notify-manager" {
		t.Fatalf("deadline fields = %q/%q/%q", ut.DeadlineDuration, ut.DeadlineFlow, ut.DeadlineAction)
	}
	if ut.ReminderEvery != "PT4H" || ut.ReminderAction != "send-reminder" {
		t.Fatalf("Reminder fields = %q/%q", ut.ReminderEvery, ut.ReminderAction)
	}
}

func TestReceiveTaskConstructor(t *testing.T) {
	n := definition.NewReceiveTask("recv", "payment.received",
		definition.WithCorrelationKey("order.id"),
		definition.WithCancelHandler("cancel-payment"),
	)
	if n.Kind() != definition.KindReceiveTask {
		t.Fatalf("Kind() = %v, want KindReceiveTask", n.Kind())
	}
	rt, ok := n.(definition.ReceiveTask)
	if !ok {
		t.Fatalf("node is %T, want definition.ReceiveTask", n)
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
	n := definition.NewSendTask("send", "order.shipped")
	if n.Kind() != definition.KindSendTask {
		t.Fatalf("Kind() = %v, want KindSendTask", n.Kind())
	}
	st, ok := n.(definition.SendTask)
	if !ok {
		t.Fatalf("node is %T, want definition.SendTask", n)
	}
	if st.MessageName != "order.shipped" {
		t.Fatalf("MessageName = %q", st.MessageName)
	}
}

func TestSendTaskCorrelationKey(t *testing.T) {
	n := definition.NewSendTask("send", "order.shipped", definition.WithCorrelationKey(`vars.orderID`))
	st, ok := n.(definition.SendTask)
	if !ok {
		t.Fatalf("node is %T, want definition.SendTask", n)
	}
	if st.CorrelationKey != `vars.orderID` {
		t.Fatalf("CorrelationKey = %q, want %q", st.CorrelationKey, `vars.orderID`)
	}
}

func TestBusinessRuleTaskConstructor(t *testing.T) {
	n := definition.NewBusinessRuleTask("brt", definition.WithActionName("apply-discount"))
	if n.Kind() != definition.KindBusinessRuleTask {
		t.Fatalf("Kind() = %v, want KindBusinessRuleTask", n.Kind())
	}
	brt, ok := n.(definition.BusinessRuleTask)
	if !ok {
		t.Fatalf("node is %T, want definition.BusinessRuleTask", n)
	}
	if brt.Action != "apply-discount" {
		t.Fatalf("Action = %q", brt.Action)
	}
}

func TestSubProcessConstructor(t *testing.T) {
	sub := &definition.ProcessDefinition{ID: "sub", Version: 1}
	n := definition.NewSubProcess("sp", sub)
	if n.Kind() != definition.KindSubProcess {
		t.Fatalf("Kind() = %v, want KindSubProcess", n.Kind())
	}
	sp, ok := n.(definition.SubProcess)
	if !ok {
		t.Fatalf("node is %T, want definition.SubProcess", n)
	}
	if sp.Subprocess != sub {
		t.Fatal("Subprocess pointer not preserved")
	}
}

func TestCallActivityConstructor(t *testing.T) {
	n := definition.NewCallActivity("ca", "external-v2")
	if n.Kind() != definition.KindCallActivity {
		t.Fatalf("Kind() = %v, want KindCallActivity", n.Kind())
	}
	ca, ok := n.(definition.CallActivity)
	if !ok {
		t.Fatalf("node is %T, want definition.CallActivity", n)
	}
	if ca.DefRef != "external-v2" {
		t.Fatalf("DefRef = %q", ca.DefRef)
	}
}

func TestEventSubProcessConstructor(t *testing.T) {
	sub := &definition.ProcessDefinition{ID: "esp-sub", Version: 1}
	n := definition.NewEventSubProcess("esp", sub)
	if n.Kind() != definition.KindEventSubProcess {
		t.Fatalf("Kind() = %v, want KindEventSubProcess", n.Kind())
	}
	esp, ok := n.(definition.EventSubProcess)
	if !ok {
		t.Fatalf("node is %T, want definition.EventSubProcess", n)
	}
	if esp.Subprocess != sub {
		t.Fatal("Subprocess pointer not preserved")
	}
}

func TestIntermediateCatchEventConstructor(t *testing.T) {
	n := definition.NewIntermediateCatchEvent("ice",
		definition.WithTimerDuration("PT1H"),
		definition.WithICEDeadline("P1D", "sla-flow", "sla-act"),
		definition.WithICEReminder("PT2H", "remind-act"),
	)
	if n.Kind() != definition.KindIntermediateCatchEvent {
		t.Fatalf("Kind() = %v, want KindIntermediateCatchEvent", n.Kind())
	}
	ice, ok := n.(definition.IntermediateCatchEvent)
	if !ok {
		t.Fatalf("node is %T, want definition.IntermediateCatchEvent", n)
	}
	if ice.TimerDuration != "PT1H" {
		t.Fatalf("TimerDuration = %q", ice.TimerDuration)
	}
	if ice.DeadlineDuration != "P1D" {
		t.Fatalf("DeadlineDuration = %q", ice.DeadlineDuration)
	}
}

func TestIntermediateCatchEventSignal(t *testing.T) {
	n := definition.NewIntermediateCatchEvent("ice-sig", definition.WithSignalName("my.signal"))
	ice, ok := n.(definition.IntermediateCatchEvent)
	if !ok {
		t.Fatalf("node is %T, want definition.IntermediateCatchEvent", n)
	}
	if ice.SignalName != "my.signal" {
		t.Fatalf("SignalName = %q", ice.SignalName)
	}
}

func TestIntermediateCatchEventMessage(t *testing.T) {
	n := definition.NewIntermediateCatchEvent("ice-msg",
		definition.WithMessageNameAndKey("payment.received", "order.id"),
	)
	ice, ok := n.(definition.IntermediateCatchEvent)
	if !ok {
		t.Fatalf("node is %T, want definition.IntermediateCatchEvent", n)
	}
	if ice.MessageName != "payment.received" {
		t.Fatalf("MessageName = %q", ice.MessageName)
	}
	if ice.CorrelationKey != "order.id" {
		t.Fatalf("CorrelationKey = %q", ice.CorrelationKey)
	}
}

func TestIntermediateThrowEventConstructor(t *testing.T) {
	n := definition.NewIntermediateThrowEvent("ite",
		definition.WithThrowSignal("order.shipped"),
	)
	if n.Kind() != definition.KindIntermediateThrowEvent {
		t.Fatalf("Kind() = %v, want KindIntermediateThrowEvent", n.Kind())
	}
	ite, ok := n.(definition.IntermediateThrowEvent)
	if !ok {
		t.Fatalf("node is %T, want definition.IntermediateThrowEvent", n)
	}
	if ite.SignalName != "order.shipped" {
		t.Fatalf("SignalName = %q", ite.SignalName)
	}
}

func TestIntermediateThrowEventCompensateRef(t *testing.T) {
	n := definition.NewIntermediateThrowEvent("comp-throw",
		definition.WithCompensateRef("my-task"),
	)
	ite, ok := n.(definition.IntermediateThrowEvent)
	if !ok {
		t.Fatalf("node is %T, want definition.IntermediateThrowEvent", n)
	}
	if ite.CompensateRef != "my-task" {
		t.Fatalf("CompensateRef = %q", ite.CompensateRef)
	}
}

func TestBoundaryEventConstructor(t *testing.T) {
	n := definition.NewBoundaryEvent("bnd", "task-1",
		definition.WithBoundarySignal("cancel.signal"),
		definition.BoundaryNonInterrupting(),
	)
	if n.Kind() != definition.KindBoundaryEvent {
		t.Fatalf("Kind() = %v, want KindBoundaryEvent", n.Kind())
	}
	be, ok := n.(definition.BoundaryEvent)
	if !ok {
		t.Fatalf("node is %T, want definition.BoundaryEvent", n)
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
		n    definition.Node
		kind definition.NodeKind
	}{
		{definition.NewExclusiveGateway("xor"), definition.KindExclusiveGateway},
		{definition.NewParallelGateway("par"), definition.KindParallelGateway},
		{definition.NewInclusiveGateway("inc"), definition.KindInclusiveGateway},
		{definition.NewEventBasedGateway("ebg"), definition.KindEventBasedGateway},
	}
	for _, tc := range cases {
		if tc.n.Kind() != tc.kind {
			t.Errorf("Kind() = %v, want %v", tc.n.Kind(), tc.kind)
		}
	}
}

func TestWithNameOnActivities(t *testing.T) {
	// WithName option should work on all kinds.
	n := definition.NewServiceTask("st", definition.WithActionName("act"), definition.WithName("My Task"))
	if n.Name() != "My Task" {
		t.Fatalf("Name() = %q, want 'My Task'", n.Name())
	}

	n2 := definition.NewUserTask("ut", nil, definition.WithName("User Step"))
	if n2.Name() != "User Step" {
		t.Fatalf("Name() = %q, want 'User Step'", n2.Name())
	}

	n3 := definition.NewBoundaryEvent("bnd", "host", definition.WithName("Timer Boundary"))
	if n3.Name() != "Timer Boundary" {
		t.Fatalf("Name() = %q, want 'Timer Boundary'", n3.Name())
	}

	n4 := definition.NewIntermediateCatchEvent("ice", definition.WithName("Wait"))
	if n4.Name() != "Wait" {
		t.Fatalf("Name() = %q, want 'Wait'", n4.Name())
	}
}

func TestRetryPolicyOption(t *testing.T) {
	p := &definition.RetryPolicy{MaxAttempts: 5}
	n := definition.NewServiceTask("st", definition.WithActionName("act"), definition.WithRetryPolicy(p))
	st, _ := n.(definition.ServiceTask)
	if st.RetryPolicy != p {
		t.Fatal("RetryPolicy not set")
	}
}

func TestEventSubProcessNonInterrupting(t *testing.T) {
	sub := &definition.ProcessDefinition{ID: "esp-ni-sub", Version: 1}
	n := definition.NewEventSubProcess("esp-ni", sub, definition.WithESPNonInterrupting())
	esp, ok := n.(definition.EventSubProcess)
	if !ok {
		t.Fatalf("node is %T, want definition.EventSubProcess", n)
	}
	if !esp.NonInterrupting {
		t.Fatal("NonInterrupting should be true when WithESPNonInterrupting is used")
	}
}

// TestUserTaskCombinedOptions verifies that WithEligibilityExpr, WithName, and
// WithRetryPolicy can all be combined on NewUserTask and that each field is set.
func TestUserTaskCombinedOptions(t *testing.T) {
	p := &definition.RetryPolicy{MaxAttempts: 1}
	n := definition.NewUserTask("u", []string{"reviewer"},
		definition.WithEligibilityExpr("vars.score > 50"),
		definition.WithName("Review Task"),
		definition.WithRetryPolicy(p),
	)
	ut, ok := n.(definition.UserTask)
	if !ok {
		t.Fatalf("node is %T, want definition.UserTask", n)
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
	p := &definition.RetryPolicy{MaxAttempts: 2}
	n := definition.NewReceiveTask("recv-combo", "order.confirmed",
		definition.WithCorrelationKey("order.id"),
		definition.WithName("Wait For Confirmation"),
		definition.WithRetryPolicy(p),
	)
	rt, ok := n.(definition.ReceiveTask)
	if !ok {
		t.Fatalf("node is %T, want definition.ReceiveTask", n)
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
	n := definition.NewUserTask("approve", []string{"approver"},
		definition.WithEligibilityPrivileges(privs...),
	)
	ut, ok := n.(definition.UserTask)
	if !ok {
		t.Fatalf("node is %T, want definition.UserTask", n)
	}
	if len(ut.EligibilityPrivileges) != 2 {
		t.Fatalf("EligibilityPrivileges len = %d, want 2; got %v", len(ut.EligibilityPrivileges), ut.EligibilityPrivileges)
	}
	if ut.EligibilityPrivileges[0] != "finance-task claim" {
		t.Fatalf("EligibilityPrivileges[0] = %q, want %q", ut.EligibilityPrivileges[0], "finance-task claim")
	}
}

// TestWithEligibilityPrivilegesRoundTrip verifies that EligibilityPrivileges survives
// a JSON marshal/unmarshal round-trip (via nodeWire).
func TestWithEligibilityPrivilegesRoundTrip(t *testing.T) {
	privs := []string{"doc read"}
	n := definition.NewUserTask("u2", nil, definition.WithEligibilityPrivileges(privs...))
	def := &definition.ProcessDefinition{
		ID:      "p",
		Version: 1,
		Nodes:   []definition.Node{n},
		Flows:   []definition.SequenceFlow{},
	}
	data, err := json.Marshal(def)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded definition.ProcessDefinition
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	ut, ok := decoded.Nodes[0].(definition.UserTask)
	if !ok {
		t.Fatalf("decoded node is %T, want definition.UserTask", decoded.Nodes[0])
	}
	if len(ut.EligibilityPrivileges) != 1 || ut.EligibilityPrivileges[0] != "doc read" {
		t.Fatalf("EligibilityPrivileges = %v, want [doc read]", ut.EligibilityPrivileges)
	}
}

func TestEventSubProcessNonInterruptingRoundTrip(t *testing.T) {
	inner := &definition.ProcessDefinition{
		ID:      "inner",
		Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("s"),
			definition.NewEndEvent("e"),
		},
		Flows: []definition.SequenceFlow{{ID: "f1", Source: "s", Target: "e"}},
	}
	outer := &definition.ProcessDefinition{
		ID:      "outer",
		Version: 1,
		Nodes: []definition.Node{
			definition.NewEventSubProcess("esp-ni", inner,
				definition.WithESPNonInterrupting(),
				definition.WithName("Non-Interrupting ESP"),
			),
		},
		Flows: []definition.SequenceFlow{},
	}

	data, err := json.Marshal(outer)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded definition.ProcessDefinition
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if len(decoded.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(decoded.Nodes))
	}
	esp, ok := decoded.Nodes[0].(definition.EventSubProcess)
	if !ok {
		t.Fatalf("decoded node is %T, want definition.EventSubProcess", decoded.Nodes[0])
	}
	if !esp.NonInterrupting {
		t.Fatal("NonInterrupting not preserved through JSON round-trip")
	}
	if esp.Name() != "Non-Interrupting ESP" {
		t.Fatalf("Name = %q, want 'Non-Interrupting ESP'", esp.Name())
	}
}

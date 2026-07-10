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
		activity.WithTaskAction("charge-card"),
		activity.WithCompensateAction("refund-card"),
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
	if st.Action != "charge-card" || st.CompensateAction != "refund-card" || st.RecoveryFlow != "to-manual" {
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
	n := activity.NewUserTask("task-1", activity.WithEligibleRoles("manager", "admin"),
		activity.WithEligibleExpr("amount > 1000"),
		activity.WithWaitDeadline(schedule.AfterDuration(24*time.Hour), "sla-breach"), activity.WithDeadlineAction("notify-manager"),
		activity.WithWaitAction(schedule.Every(4*time.Hour), "send-reminder"),
	)
	if n.Kind() != model.KindUserTask {
		t.Fatalf("Kind() = %v, want KindUserTask", n.Kind())
	}
	ut, ok := n.(activity.UserTask)
	if !ok {
		t.Fatalf("node is %T, want activity.UserTask", n)
	}
	if ut.EligibleExpr != "amount > 1000" {
		t.Fatalf("EligibleExpr = %q", ut.EligibleExpr)
	}
	if len(ut.EligibleRoles) != 2 || ut.EligibleRoles[0] != "manager" {
		t.Fatalf("EligibleRoles = %v", ut.EligibleRoles)
	}
	if dd, ok := ut.DeadlineTimer.Duration(); !ok || dd != 24*time.Hour || ut.DeadlineFlow != "sla-breach" || ut.DeadlineAction != "notify-manager" {
		t.Fatalf("deadline fields = %v/%q/%q", dd, ut.DeadlineFlow, ut.DeadlineAction)
	}
	if rd, ok := ut.WaitEvery.Duration(); !ok || rd != 4*time.Hour || ut.WaitAction != "send-reminder" {
		t.Fatalf("wait fields = %v/%q", rd, ut.WaitAction)
	}
}

func TestReceiveTaskConstructor(t *testing.T) {
	n := activity.NewReceiveTask("recv", "payment.received",
		activity.WithCorrelationKey("order.id"),
		activity.WithCancelAction("cancel-payment"),
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
	if rt.CancelAction != "cancel-payment" {
		t.Fatalf("CancelAction = %q", rt.CancelAction)
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
	n := activity.NewBusinessRuleTask("brt", activity.WithTaskAction("apply-discount"))
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

func TestIntermediateCatchEventConstructor(t *testing.T) {
	n := event.NewIntermediateCatch("ice",
		event.WithCatchTimer(schedule.AfterExpr("PT1H")),
		event.WithWaitDeadline(schedule.AfterDuration(24*time.Hour), "sla-flow"), event.WithDeadlineAction("sla-act"),
		event.WithWaitAction(schedule.Every(2*time.Hour), "remind-act"),
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
	n := event.NewIntermediateCatch("ice-sig", event.WithSignalName("my.signal"))
	ice, ok := n.(event.IntermediateCatchEvent)
	if !ok {
		t.Fatalf("node is %T, want event.IntermediateCatchEvent", n)
	}
	if ice.SignalName != "my.signal" {
		t.Fatalf("SignalName = %q", ice.SignalName)
	}
}

func TestIntermediateCatchEventMessage(t *testing.T) {
	n := event.NewIntermediateCatch("ice-msg",
		event.WithMessageCorrelator("payment.received", "order.id"),
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
	n := event.NewIntermediateThrow("ite",
		event.WithThrowSignalName("order.shipped"),
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

func TestCompensateThrowEventConstructor(t *testing.T) {
	n := event.NewCompensateThrow("comp-throw",
		event.WithCompensateRef("my-task"),
	)
	if n.Kind() != model.KindCompensationThrowEvent {
		t.Fatalf("Kind() = %v, want KindCompensationThrowEvent", n.Kind())
	}
	cte, ok := n.(event.CompensationThrowEvent)
	if !ok {
		t.Fatalf("node is %T, want event.CompensationThrowEvent", n)
	}
	if cte.CompensateRef != "my-task" {
		t.Fatalf("CompensateRef = %q", cte.CompensateRef)
	}
}

func TestBoundaryEventConstructor(t *testing.T) {
	n := event.NewBoundary("bnd", "task-1",
		event.WithSignalName("cancel.signal"),
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
	n := activity.NewServiceTask("st", activity.WithTaskAction("act"), activity.WithName("My Task"))
	if n.Name() != "My Task" {
		t.Fatalf("Name() = %q, want 'My Task'", n.Name())
	}

	n2 := activity.NewUserTask("ut", activity.WithName("User Step"))
	if n2.Name() != "User Step" {
		t.Fatalf("Name() = %q, want 'User Step'", n2.Name())
	}

	n3 := event.NewBoundary("bnd", "host", event.WithName("Timer Boundary"))
	if n3.Name() != "Timer Boundary" {
		t.Fatalf("Name() = %q, want 'Timer Boundary'", n3.Name())
	}

	n4 := event.NewIntermediateCatch("ice", event.WithName("Wait"))
	if n4.Name() != "Wait" {
		t.Fatalf("Name() = %q, want 'Wait'", n4.Name())
	}
}

func TestRetryPolicyOption(t *testing.T) {
	p := &model.RetryPolicy{MaxAttempts: 5}
	n := activity.NewServiceTask("st", activity.WithTaskAction("act"), activity.WithRetryPolicy(p))
	st, _ := n.(activity.ServiceTask)
	if st.RetryPolicy != p {
		t.Fatal("RetryPolicy not set")
	}
}

// TestUserTaskCombinedOptions verifies that WithEligibleExpr, WithName, and
// WithRetryPolicy can all be combined on NewUserTask and that each field is set.
func TestUserTaskCombinedOptions(t *testing.T) {
	p := &model.RetryPolicy{MaxAttempts: 1}
	n := activity.NewUserTask("u", activity.WithEligibleRoles("reviewer"),
		activity.WithEligibleExpr("vars.score > 50"),
		activity.WithName("Review Task"),
		activity.WithRetryPolicy(p),
	)
	ut, ok := n.(activity.UserTask)
	if !ok {
		t.Fatalf("node is %T, want activity.UserTask", n)
	}
	if ut.EligibleExpr != "vars.score > 50" {
		t.Errorf("EligibleExpr = %q, want %q", ut.EligibleExpr, "vars.score > 50")
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

// TestWithEligiblePrivileges verifies that WithEligiblePrivileges sets
// EligiblePrivileges on the UserTask node, and that attempting to pass it to
// a non-UserTask constructor is a compile-time error (not tested here, by design).
func TestWithEligiblePrivileges(t *testing.T) {
	privs := []string{"finance-task claim", "finance-task read"}
	n := activity.NewUserTask("approve", activity.WithEligibleRoles("approver"),
		activity.WithEligiblePrivileges(privs...),
	)
	ut, ok := n.(activity.UserTask)
	if !ok {
		t.Fatalf("node is %T, want activity.UserTask", n)
	}
	if len(ut.EligiblePrivileges) != 2 {
		t.Fatalf("EligiblePrivileges len = %d, want 2; got %v", len(ut.EligiblePrivileges), ut.EligiblePrivileges)
	}
	if ut.EligiblePrivileges[0] != "finance-task claim" {
		t.Fatalf("EligiblePrivileges[0] = %q, want %q", ut.EligiblePrivileges[0], "finance-task claim")
	}
}

// TestWithEligiblePrivilegesRoundTrip verifies that EligiblePrivileges survives
// a JSON marshal/unmarshal round-trip (via NodeWire).
func TestWithEligiblePrivilegesRoundTrip(t *testing.T) {
	privs := []string{"doc read"}
	n := activity.NewUserTask("u2", activity.WithEligiblePrivileges(privs...))
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
	if len(ut.EligiblePrivileges) != 1 || ut.EligiblePrivileges[0] != "doc read" {
		t.Fatalf("EligiblePrivileges = %v, want [doc read]", ut.EligiblePrivileges)
	}
}

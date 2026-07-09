package activity_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
)

func TestServiceTaskOptions(t *testing.T) {
	n := activity.NewServiceTask("charge",
		activity.WithName("Charge"),
		activity.WithTaskAction("charge-card"),
		activity.WithCompensateAction("refund"),
		activity.WithCancelAction("abort"),
		activity.WithRecoveryFlow("charge->manual"),
		activity.WithWaitDeadline(schedule.AfterExpr(`"2h"`), "sla"), activity.WithDeadlineAction("notify"),
		activity.WithRetryPolicy(&model.RetryPolicy{MaxAttempts: 5}),
	)
	if n.Kind() != model.KindServiceTask || n.Name() != "Charge" {
		t.Fatalf("kind/name = %v/%q", n.Kind(), n.Name())
	}
	if model.ActionOf(n) != "charge-card" {
		t.Errorf("ActionOf = %q", model.ActionOf(n))
	}
	d, f, a := model.DeadlineOf(n)
	if d.IsZero() || f != "sla" || a != "notify" {
		t.Errorf("DeadlineOf = %v,%q,%q", d, f, a)
	}
	if dExpr, _, dOk := d.Expr(); !dOk || dExpr != `"2h"` {
		t.Errorf("deadline Timer expr = %q, ok=%v", dExpr, dOk)
	}
	if rp := model.RetryPolicyOf(n); rp == nil || rp.MaxAttempts != 5 {
		t.Errorf("RetryPolicyOf = %+v", rp)
	}
}

func TestNewCallActivityQualifier(t *testing.T) {
	n := activity.NewCallActivity("call", model.Version("order", 2))
	ca, ok := n.(activity.CallActivity)
	if !ok {
		t.Fatalf("want CallActivity, got %T", n)
	}
	if ca.DefRef != model.Version("order", 2) {
		t.Fatalf("DefRef = %+v", ca.DefRef)
	}
}

// TestCallActivityWireDefRefString verifies the CallActivity DefRef survives a
// JSON wire round-trip as its string form (ToWire String() / FromWire parse).
func TestCallActivityWireDefRefString(t *testing.T) {
	def := &model.ProcessDefinition{
		ID: "p", Version: 1,
		Nodes: []model.Node{activity.NewCallActivity("call", model.Version("order", 3))},
	}
	data, err := json.Marshal(def)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"defRef":"order:3"`) {
		t.Fatalf("wire not string-form: %s", data)
	}
	var got model.ProcessDefinition
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	ca, ok := got.Nodes[0].(activity.CallActivity)
	if !ok {
		t.Fatalf("want CallActivity, got %T", got.Nodes[0])
	}
	if ca.DefRef != model.Version("order", 3) {
		t.Fatalf("round-trip DefRef = %+v", ca.DefRef)
	}
}

func TestOtherActivityConstructors(t *testing.T) {
	sub := &model.ProcessDefinition{ID: "s", Version: 1}
	nodes := []struct {
		n model.Node
		k model.NodeKind
	}{
		{activity.NewUserTask("u", activity.WithEligibleRoles("mgr"), activity.WithEligibleExpr(`vars["r"]=="EU"`), activity.WithEligiblePrivileges("t claim")), model.KindUserTask},
		{activity.NewReceiveTask("r", "msg", activity.WithCorrelationKey("k")), model.KindReceiveTask},
		{activity.NewSendTask("s", "msg", activity.WithCorrelationKey("k")), model.KindSendTask},
		{activity.NewBusinessRuleTask("b", activity.WithTaskAction("rule")), model.KindBusinessRuleTask},
		{activity.NewSubProcess("sp", sub, activity.WithName("Sub")), model.KindSubProcess},
		{activity.NewCallActivity("ca", model.Version("ref", 1), activity.WithName("Call")), model.KindCallActivity},
	}
	for _, c := range nodes {
		if c.n.Kind() != c.k {
			t.Errorf("Kind() = %v, want %v", c.n.Kind(), c.k)
		}
	}
}

// TestSharedOptionsAllConstructors exercises WithName + a shared activity option
// through every activity constructor's option-interface dispatch.
func TestSharedOptionsAllConstructors(t *testing.T) {
	rp := &model.RetryPolicy{MaxAttempts: 2}
	sub := &model.ProcessDefinition{ID: "s", Version: 1}
	nodes := []model.Node{
		activity.NewServiceTask("st", activity.WithName("N"), activity.WithRetryPolicy(rp)),
		activity.NewUserTask("ut", activity.WithName("N"), activity.WithRetryPolicy(rp)),
		activity.NewReceiveTask("rt", "m", activity.WithName("N"), activity.WithRetryPolicy(rp)),
		activity.NewSendTask("snt", "m", activity.WithName("N"), activity.WithRetryPolicy(rp)),
		activity.NewBusinessRuleTask("br", activity.WithName("N"), activity.WithRetryPolicy(rp)),
		activity.NewSubProcess("sp", sub, activity.WithName("N"), activity.WithRetryPolicy(rp)),
		activity.NewCallActivity("ca", model.Version("ref", 1), activity.WithName("N"), activity.WithRetryPolicy(rp)),
	}
	for _, n := range nodes {
		if n.Name() != "N" {
			t.Errorf("%v: WithName not applied", n.Kind())
		}
		if model.RetryPolicyOf(n) == nil {
			t.Errorf("%v: WithRetryPolicy not applied", n.Kind())
		}
	}
}

func TestActivityRoundTrip(t *testing.T) {
	def := &model.ProcessDefinition{
		ID: "a", Version: 1,
		Nodes: []model.Node{
			activity.NewServiceTask("st", activity.WithTaskAction("act"), activity.WithWaitDeadline(schedule.AfterExpr(`"1h"`), "f"), activity.WithDeadlineAction("a")),
			activity.NewUserTask("ut", activity.WithEligibleRoles("mgr"), activity.WithEligibleExpr("x")),
			activity.NewReceiveTask("rt", "m", activity.WithCorrelationKey("k")),
			activity.NewSendTask("snt", "m"),
			activity.NewCallActivity("ca", model.Version("ref", 2)),
		},
	}
	data, err := json.Marshal(def)
	if err != nil {
		t.Fatal(err)
	}
	var got model.ProcessDefinition
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if model.ActionOf(got.Nodes[0]) != "act" {
		t.Errorf("service action lost: %q", model.ActionOf(got.Nodes[0]))
	}
	if d, _, _ := model.DeadlineOf(got.Nodes[0]); d.IsZero() {
		t.Errorf("deadline lost after round-trip")
	} else if dExpr, _, dOk := d.Expr(); !dOk || dExpr != `"1h"` {
		t.Errorf("deadline Timer expr after round-trip = %q, ok=%v", dExpr, dOk)
	}
}

// TestUserTaskManualWireRoundTrip verifies UserTask.Manual (ADR-0118) survives
// a JSON wire round-trip (ToWire -> NodeWire -> FromWire).
func TestUserTaskManualWireRoundTrip(t *testing.T) {
	def := &model.ProcessDefinition{
		ID: "d", Version: 1,
		Nodes: []model.Node{
			activity.NewUserTask("confirm", activity.WithManual(false)),
		},
	}
	data, err := json.Marshal(def)
	if err != nil {
		t.Fatal(err)
	}
	var got model.ProcessDefinition
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	ut, ok := got.Nodes[0].(activity.UserTask)
	if !ok {
		t.Fatalf("node is %T, want activity.UserTask", got.Nodes[0])
	}
	if !ut.Manual {
		t.Fatal("Manual not preserved across JSON round-trip")
	}
}

package activity_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
)

func TestServiceTaskOptions(t *testing.T) {
	n := activity.NewServiceTask("charge",
		activity.WithName("Charge"),
		activity.WithActionName("charge-card"),
		activity.WithCompensation("refund"),
		activity.WithCancelHandler("abort"),
		activity.WithRecoveryFlow("charge->manual"),
		activity.WithDeadline("2h", "sla", "notify"),
		activity.WithReminder("30m", "ping"),
		activity.WithRetryPolicy(&definition.RetryPolicy{MaxAttempts: 5}),
	)
	if n.Kind() != definition.KindServiceTask || n.Name() != "Charge" {
		t.Fatalf("kind/name = %v/%q", n.Kind(), n.Name())
	}
	if definition.ActionOf(n) != "charge-card" {
		t.Errorf("ActionOf = %q", definition.ActionOf(n))
	}
	if d, f, a := definition.DeadlineOf(n); d != "2h" || f != "sla" || a != "notify" {
		t.Errorf("DeadlineOf = %q,%q,%q", d, f, a)
	}
	if e, a := definition.ReminderOf(n); e != "30m" || a != "ping" {
		t.Errorf("ReminderOf = %q,%q", e, a)
	}
	if rp := definition.RetryPolicyOf(n); rp == nil || rp.MaxAttempts != 5 {
		t.Errorf("RetryPolicyOf = %+v", rp)
	}
}

func TestInlineActionOptions(t *testing.T) {
	fn := func(context.Context, map[string]any) (map[string]any, error) { return nil, nil }
	n := activity.NewServiceTask("x", activity.WithActionFunc(fn))
	if definition.InlineActionOf(n) == nil {
		t.Fatal("WithActionFunc: expected inline action")
	}
	n2 := activity.NewBusinessRuleTask("y", activity.WithAction(action.Func(fn)))
	if definition.InlineActionOf(n2) == nil {
		t.Fatal("WithAction: expected inline action")
	}
}

func TestOtherActivityConstructors(t *testing.T) {
	sub := &definition.ProcessDefinition{ID: "s", Version: 1}
	nodes := []struct {
		n definition.Node
		k definition.NodeKind
	}{
		{activity.NewUserTask("u", []string{"mgr"}, activity.WithEligibilityExpr(`vars["r"]=="EU"`), activity.WithEligibilityPrivileges("t claim")), definition.KindUserTask},
		{activity.NewReceiveTask("r", "msg", activity.WithCorrelationKey("k")), definition.KindReceiveTask},
		{activity.NewSendTask("s", "msg", activity.WithCorrelationKey("k")), definition.KindSendTask},
		{activity.NewBusinessRuleTask("b", activity.WithActionName("rule")), definition.KindBusinessRuleTask},
		{activity.NewSubProcess("sp", sub, activity.WithName("Sub")), definition.KindSubProcess},
		{activity.NewCallActivity("ca", "ref", activity.WithName("Call")), definition.KindCallActivity},
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
	rp := &definition.RetryPolicy{MaxAttempts: 2}
	sub := &definition.ProcessDefinition{ID: "s", Version: 1}
	nodes := []definition.Node{
		activity.NewServiceTask("st", activity.WithName("N"), activity.WithRetryPolicy(rp)),
		activity.NewUserTask("ut", nil, activity.WithName("N"), activity.WithRetryPolicy(rp)),
		activity.NewReceiveTask("rt", "m", activity.WithName("N"), activity.WithRetryPolicy(rp)),
		activity.NewSendTask("snt", "m", activity.WithName("N"), activity.WithRetryPolicy(rp)),
		activity.NewBusinessRuleTask("br", activity.WithName("N"), activity.WithRetryPolicy(rp)),
		activity.NewSubProcess("sp", sub, activity.WithName("N"), activity.WithRetryPolicy(rp)),
		activity.NewCallActivity("ca", "ref", activity.WithName("N"), activity.WithRetryPolicy(rp)),
	}
	for _, n := range nodes {
		if n.Name() != "N" {
			t.Errorf("%v: WithName not applied", n.Kind())
		}
		if definition.RetryPolicyOf(n) == nil {
			t.Errorf("%v: WithRetryPolicy not applied", n.Kind())
		}
	}
}

func TestActivityRoundTrip(t *testing.T) {
	def := &definition.ProcessDefinition{
		ID: "a", Version: 1,
		Nodes: []definition.Node{
			activity.NewServiceTask("st", activity.WithActionName("act"), activity.WithDeadline("1h", "f", "a")),
			activity.NewUserTask("ut", []string{"mgr"}, activity.WithEligibilityExpr("x")),
			activity.NewReceiveTask("rt", "m", activity.WithCorrelationKey("k")),
			activity.NewSendTask("snt", "m"),
			activity.NewCallActivity("ca", "ref"),
		},
	}
	data, err := json.Marshal(def)
	if err != nil {
		t.Fatal(err)
	}
	var got definition.ProcessDefinition
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if definition.ActionOf(got.Nodes[0]) != "act" {
		t.Errorf("service action lost: %q", definition.ActionOf(got.Nodes[0]))
	}
	if d, _, _ := definition.DeadlineOf(got.Nodes[0]); d != "1h" {
		t.Errorf("deadline lost: %q", d)
	}
}

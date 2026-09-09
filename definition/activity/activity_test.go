package activity_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	if !strings.Contains(string(data), `"def_ref":"order:3"`) {
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

// TestUserTaskManualWireRoundTrip verifies UserTask.Manual survives a JSON
// wire round-trip (ToWire -> NodeWire -> FromWire).
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

// TestUserTaskManualImmediateWireRoundTrip verifies UserTask.ManualImmediate
// survives a JSON wire round-trip (ToWire -> NodeWire -> FromWire) alongside
// Manual.
func TestUserTaskManualImmediateWireRoundTrip(t *testing.T) {
	def := &model.ProcessDefinition{
		ID: "d", Version: 1,
		Nodes: []model.Node{activity.NewUserTask("confirm", activity.WithManual(true))},
	}
	data, err := json.Marshal(def)
	if err != nil {
		t.Fatal(err)
	}
	var got model.ProcessDefinition
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	ut := got.Nodes[0].(activity.UserTask)
	if !ut.Manual || !ut.ManualImmediate {
		t.Fatalf("Manual=%v ManualImmediate=%v, want both true", ut.Manual, ut.ManualImmediate)
	}
}

// TestUserTaskOutcomeWireRoundTrip verifies the completion-outcome declaration
// survives a JSON wire round-trip under its snake_case keys.
func TestUserTaskOutcomeWireRoundTrip(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		node   model.Node
		assert func(t *testing.T, encoded string, got activity.UserTask)
	}

	cases := []testCase{
		{
			name: "outcomes with explicit variable",
			node: activity.NewUserTask("review",
				activity.WithOutcomes("approve", "reject"),
				activity.WithOutcomeVariable("review_decision"),
			),
			assert: func(t *testing.T, encoded string, got activity.UserTask) {
				assert.Contains(t, encoded, `"outcomes":["approve","reject"]`)
				assert.Contains(t, encoded, `"outcome_variable":"review_decision"`)
				assert.NotContains(t, encoded, "expose_outcome")
				assert.Equal(t, []string{"approve", "reject"}, got.Outcomes)
				assert.Equal(t, "review_decision", got.OutcomeVariable)
				assert.False(t, got.ExposeOutcome)
			},
		},
		{
			name: "conventional exposure",
			node: activity.NewUserTask("review", activity.WithExposeOutcome()),
			assert: func(t *testing.T, encoded string, got activity.UserTask) {
				assert.Contains(t, encoded, `"expose_outcome":true`)
				assert.True(t, got.ExposeOutcome)
			},
		},
		{
			name: "unconstrained task omits every outcome key",
			node: activity.NewUserTask("review"),
			assert: func(t *testing.T, encoded string, got activity.UserTask) {
				assert.NotContains(t, encoded, "outcome")
				assert.Empty(t, got.Outcomes)
				assert.False(t, got.ExposeOutcome)
				assert.Empty(t, got.OutcomeVariable)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			def := &model.ProcessDefinition{ID: "p", Version: 1, Nodes: []model.Node{tc.node}}
			data, err := json.Marshal(def)
			require.NoError(t, err)

			var back model.ProcessDefinition
			require.NoError(t, json.Unmarshal(data, &back))
			got, ok := back.Nodes[0].(activity.UserTask)
			require.True(t, ok, "node is %T, want activity.UserTask", back.Nodes[0])

			tc.assert(t, string(data), got)
		})
	}
}

// TestBusinessRuleTaskRuleOptions owns the option -> field seam: each reserved rule
// option must land the shape it names on BusinessRuleTask.Rule, and the field must
// survive a round-trip through the kind's NodeSpec unchanged. The exact wire
// STRINGS are pinned by model's TestBusinessRuleTaskRuleRoundTrip, which owns the
// persisted-blob seam; they are not re-asserted here.
//
// The no-option row is the at-limit accept: an option set that always allocated a
// RuleSpec would satisfy the two positive rows just as well, and would put a rule
// on every businessRuleTask.
func TestBusinessRuleTaskRuleOptions(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		opts   []activity.BusinessRuleOption
		assert func(t *testing.T, task activity.BusinessRuleTask, encoded string)
	}

	cases := []testCase{
		{
			name: "WithRule sets the catalog name",
			opts: []activity.BusinessRuleOption{activity.WithRule("pricing.v3")},
			assert: func(t *testing.T, task activity.BusinessRuleTask, encoded string) {
				require.NotNil(t, task.Rule)
				assert.Equal(t, "pricing.v3", task.Rule.Name)
				assert.Empty(t, task.Rule.Inline)
			},
		},
		{
			name: "WithInlineRule sets the inline document",
			opts: []activity.BusinessRuleOption{
				activity.WithInlineRule(json.RawMessage(`{"stages":[]}`)),
			},
			assert: func(t *testing.T, task activity.BusinessRuleTask, encoded string) {
				require.NotNil(t, task.Rule)
				assert.Empty(t, task.Rule.Name)
				assert.JSONEq(t, `{"stages":[]}`, string(task.Rule.Inline))
			},
		},
		{
			name: "the last rule option wins",
			opts: []activity.BusinessRuleOption{
				activity.WithRule("pricing.v2"), activity.WithRule("pricing.v3"),
			},
			assert: func(t *testing.T, task activity.BusinessRuleTask, encoded string) {
				require.NotNil(t, task.Rule)
				assert.Equal(t, "pricing.v3", task.Rule.Name)
			},
		},
		{
			name: "no rule option leaves Rule nil and emits no key",
			opts: []activity.BusinessRuleOption{activity.WithTaskAction("risk-score")},
			assert: func(t *testing.T, task activity.BusinessRuleTask, encoded string) {
				assert.Nil(t, task.Rule)
				assert.NotContains(t, encoded, `"rule"`, "an unset rule must not reach the wire at all")
				assert.Contains(t, encoded, `"action":"risk-score"`)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			node := activity.NewBusinessRuleTask("score", tc.opts...)
			task, ok := node.(activity.BusinessRuleTask)
			require.True(t, ok)

			def := &model.ProcessDefinition{ID: "d", Version: 1, Nodes: []model.Node{node}}
			data, err := json.Marshal(def)
			require.NoError(t, err)

			// Round-trip back so the assertions cover FromWire as well as ToWire.
			var back model.ProcessDefinition
			require.NoError(t, json.Unmarshal(data, &back))
			require.Len(t, back.Nodes, 1)
			reloaded, ok := back.Nodes[0].(activity.BusinessRuleTask)
			require.True(t, ok)
			assert.Equal(t, task.Rule, reloaded.Rule, "Rule must survive the wire round-trip")

			tc.assert(t, task, string(data))
		})
	}
}

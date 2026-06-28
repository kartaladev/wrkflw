package model_test

import (
	"reflect"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/model"
)

// minimalSubDef returns a minimal valid sub-process definition (start→end).
func minimalSubDef(t *testing.T) *model.ProcessDefinition {
	t.Helper()
	def, err := model.NewDefinition("sub", 1).
		Add(model.NewStartEvent("ss")).
		Add(model.NewEndEvent("se")).
		Connect("ss", "se").
		Build()
	if err != nil {
		t.Fatalf("minimalSubDef: %v", err)
	}
	return def
}

// nodeByID returns the node with the given ID from a definition, or fails.
func nodeByID(t *testing.T, def *model.ProcessDefinition, id string) model.Node {
	t.Helper()
	for _, n := range def.Nodes {
		if n.ID() == id {
			return n
		}
	}
	t.Fatalf("node %q not found in definition", id)
	return nil
}

// TestBuilderFluentAddMethods verifies that each AddX method adds a node of the
// expected Kind and ID.
func TestBuilderFluentAddMethods(t *testing.T) {
	t.Parallel()

	sub := minimalSubDef(t)

	type testCase struct {
		name  string
		build func(t *testing.T) *model.ProcessDefinition
		// nodeID to look up and assert on
		nodeID string
		assert func(t *testing.T, n model.Node)
	}

	cases := []testCase{
		{
			name:   "AddStartEvent",
			nodeID: "s",
			build: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					AddStartEvent("s").
					AddEndEvent("e").
					Connect("s", "e").
					Build()
				if err != nil {
					t.Fatalf("Build: %v", err)
				}
				return def
			},
			assert: func(t *testing.T, n model.Node) {
				if n.Kind() != model.KindStartEvent {
					t.Errorf("Kind() = %v, want KindStartEvent", n.Kind())
				}
			},
		},
		{
			name:   "AddEndEvent",
			nodeID: "e",
			build: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					AddStartEvent("s").
					AddEndEvent("e").
					Connect("s", "e").
					Build()
				if err != nil {
					t.Fatalf("Build: %v", err)
				}
				return def
			},
			assert: func(t *testing.T, n model.Node) {
				if n.Kind() != model.KindEndEvent {
					t.Errorf("Kind() = %v, want KindEndEvent", n.Kind())
				}
			},
		},
		{
			name:   "AddTerminateEndEvent",
			nodeID: "te",
			build: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					AddStartEvent("s").
					AddTerminateEndEvent("te").
					Connect("s", "te").
					Build()
				if err != nil {
					t.Fatalf("Build: %v", err)
				}
				return def
			},
			assert: func(t *testing.T, n model.Node) {
				if n.Kind() != model.KindTerminateEndEvent {
					t.Errorf("Kind() = %v, want KindTerminateEndEvent", n.Kind())
				}
			},
		},
		{
			name:   "AddErrorEndEvent",
			nodeID: "ee",
			build: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					AddStartEvent("s").
					AddErrorEndEvent("ee", "ERR_X").
					Connect("s", "ee").
					Build()
				if err != nil {
					t.Fatalf("Build: %v", err)
				}
				return def
			},
			assert: func(t *testing.T, n model.Node) {
				if n.Kind() != model.KindErrorEndEvent {
					t.Errorf("Kind() = %v, want KindErrorEndEvent", n.Kind())
				}
				ee, ok := n.(model.ErrorEndEvent)
				if !ok {
					t.Fatalf("node is %T, want model.ErrorEndEvent", n)
				}
				if ee.ErrorCode != "ERR_X" {
					t.Errorf("ErrorCode = %q, want ERR_X", ee.ErrorCode)
				}
			},
		},
		{
			name:   "AddExclusiveGateway",
			nodeID: "gw",
			build: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					AddStartEvent("s").
					AddExclusiveGateway("gw").
					AddEndEvent("e").
					Connect("s", "gw").
					Connect("gw", "e").
					Build()
				if err != nil {
					t.Fatalf("Build: %v", err)
				}
				return def
			},
			assert: func(t *testing.T, n model.Node) {
				if n.Kind() != model.KindExclusiveGateway {
					t.Errorf("Kind() = %v, want KindExclusiveGateway", n.Kind())
				}
			},
		},
		{
			name:   "AddParallelGateway",
			nodeID: "gw",
			build: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					AddStartEvent("s").
					AddParallelGateway("gw").
					AddEndEvent("e").
					Connect("s", "gw").
					Connect("gw", "e").
					Build()
				if err != nil {
					t.Fatalf("Build: %v", err)
				}
				return def
			},
			assert: func(t *testing.T, n model.Node) {
				if n.Kind() != model.KindParallelGateway {
					t.Errorf("Kind() = %v, want KindParallelGateway", n.Kind())
				}
			},
		},
		{
			name:   "AddInclusiveGateway",
			nodeID: "gw",
			build: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					AddStartEvent("s").
					AddInclusiveGateway("gw").
					AddEndEvent("e").
					Connect("s", "gw").
					Connect("gw", "e").
					Build()
				if err != nil {
					t.Fatalf("Build: %v", err)
				}
				return def
			},
			assert: func(t *testing.T, n model.Node) {
				if n.Kind() != model.KindInclusiveGateway {
					t.Errorf("Kind() = %v, want KindInclusiveGateway", n.Kind())
				}
			},
		},
		{
			name:   "AddEventBasedGateway",
			nodeID: "gw",
			build: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				// EventBasedGateway must target catch events.
				def, err := model.NewDefinition("p", 1).
					AddStartEvent("s").
					AddEventBasedGateway("gw").
					AddIntermediateCatchEvent("ice", model.WithTimerDuration("PT1H")).
					AddEndEvent("e").
					Connect("s", "gw").
					Connect("gw", "ice").
					Connect("ice", "e").
					Build()
				if err != nil {
					t.Fatalf("Build: %v", err)
				}
				return def
			},
			assert: func(t *testing.T, n model.Node) {
				if n.Kind() != model.KindEventBasedGateway {
					t.Errorf("Kind() = %v, want KindEventBasedGateway", n.Kind())
				}
			},
		},
		{
			name:   "AddServiceTask",
			nodeID: "t",
			build: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					AddStartEvent("s").
					AddServiceTask("t").
					AddEndEvent("e").
					Connect("s", "t").
					Connect("t", "e").
					Build()
				if err != nil {
					t.Fatalf("Build: %v", err)
				}
				return def
			},
			assert: func(t *testing.T, n model.Node) {
				if n.Kind() != model.KindServiceTask {
					t.Errorf("Kind() = %v, want KindServiceTask", n.Kind())
				}
			},
		},
		{
			name:   "AddUserTask",
			nodeID: "t",
			build: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					AddStartEvent("s").
					AddUserTask("t", []string{"manager"}).
					AddEndEvent("e").
					Connect("s", "t").
					Connect("t", "e").
					Build()
				if err != nil {
					t.Fatalf("Build: %v", err)
				}
				return def
			},
			assert: func(t *testing.T, n model.Node) {
				if n.Kind() != model.KindUserTask {
					t.Errorf("Kind() = %v, want KindUserTask", n.Kind())
				}
			},
		},
		{
			name:   "AddReceiveTask",
			nodeID: "t",
			build: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					AddStartEvent("s").
					AddReceiveTask("t", "order.placed").
					AddEndEvent("e").
					Connect("s", "t").
					Connect("t", "e").
					Build()
				if err != nil {
					t.Fatalf("Build: %v", err)
				}
				return def
			},
			assert: func(t *testing.T, n model.Node) {
				if n.Kind() != model.KindReceiveTask {
					t.Errorf("Kind() = %v, want KindReceiveTask", n.Kind())
				}
				rt, ok := n.(model.ReceiveTask)
				if !ok {
					t.Fatalf("node is %T, want model.ReceiveTask", n)
				}
				if rt.MessageName != "order.placed" {
					t.Errorf("MessageName = %q, want order.placed", rt.MessageName)
				}
			},
		},
		{
			name:   "AddSendTask",
			nodeID: "t",
			build: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					AddStartEvent("s").
					AddSendTask("t", "order.confirmed").
					AddEndEvent("e").
					Connect("s", "t").
					Connect("t", "e").
					Build()
				if err != nil {
					t.Fatalf("Build: %v", err)
				}
				return def
			},
			assert: func(t *testing.T, n model.Node) {
				if n.Kind() != model.KindSendTask {
					t.Errorf("Kind() = %v, want KindSendTask", n.Kind())
				}
				st, ok := n.(model.SendTask)
				if !ok {
					t.Fatalf("node is %T, want model.SendTask", n)
				}
				if st.MessageName != "order.confirmed" {
					t.Errorf("MessageName = %q, want order.confirmed", st.MessageName)
				}
			},
		},
		{
			name:   "AddBusinessRuleTask",
			nodeID: "t",
			build: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					AddStartEvent("s").
					AddBusinessRuleTask("t").
					AddEndEvent("e").
					Connect("s", "t").
					Connect("t", "e").
					Build()
				if err != nil {
					t.Fatalf("Build: %v", err)
				}
				return def
			},
			assert: func(t *testing.T, n model.Node) {
				if n.Kind() != model.KindBusinessRuleTask {
					t.Errorf("Kind() = %v, want KindBusinessRuleTask", n.Kind())
				}
			},
		},
		{
			name:   "AddSubProcess",
			nodeID: "t",
			build: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					AddStartEvent("s").
					AddSubProcess("t", sub).
					AddEndEvent("e").
					Connect("s", "t").
					Connect("t", "e").
					Build()
				if err != nil {
					t.Fatalf("Build: %v", err)
				}
				return def
			},
			assert: func(t *testing.T, n model.Node) {
				if n.Kind() != model.KindSubProcess {
					t.Errorf("Kind() = %v, want KindSubProcess", n.Kind())
				}
			},
		},
		{
			name:   "AddCallActivity",
			nodeID: "t",
			build: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					AddStartEvent("s").
					AddCallActivity("t", "other-def").
					AddEndEvent("e").
					Connect("s", "t").
					Connect("t", "e").
					Build()
				if err != nil {
					t.Fatalf("Build: %v", err)
				}
				return def
			},
			assert: func(t *testing.T, n model.Node) {
				if n.Kind() != model.KindCallActivity {
					t.Errorf("Kind() = %v, want KindCallActivity", n.Kind())
				}
				ca, ok := n.(model.CallActivity)
				if !ok {
					t.Fatalf("node is %T, want model.CallActivity", n)
				}
				if ca.DefRef != "other-def" {
					t.Errorf("DefRef = %q, want other-def", ca.DefRef)
				}
			},
		},
		{
			name:   "AddEventSubProcess",
			nodeID: "esp",
			build: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					AddStartEvent("s").
					AddEventSubProcess("esp", sub).
					AddEndEvent("e").
					Connect("s", "esp").
					Connect("esp", "e").
					Build()
				if err != nil {
					t.Fatalf("Build: %v", err)
				}
				return def
			},
			assert: func(t *testing.T, n model.Node) {
				if n.Kind() != model.KindEventSubProcess {
					t.Errorf("Kind() = %v, want KindEventSubProcess", n.Kind())
				}
			},
		},
		{
			name:   "AddIntermediateCatchEvent",
			nodeID: "ice",
			build: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					AddStartEvent("s").
					AddIntermediateCatchEvent("ice", model.WithTimerDuration("PT1H")).
					AddEndEvent("e").
					Connect("s", "ice").
					Connect("ice", "e").
					Build()
				if err != nil {
					t.Fatalf("Build: %v", err)
				}
				return def
			},
			assert: func(t *testing.T, n model.Node) {
				if n.Kind() != model.KindIntermediateCatchEvent {
					t.Errorf("Kind() = %v, want KindIntermediateCatchEvent", n.Kind())
				}
			},
		},
		{
			name:   "AddIntermediateThrowEvent",
			nodeID: "ite",
			build: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					AddStartEvent("s").
					AddIntermediateThrowEvent("ite", model.WithThrowSignal("sig")).
					AddEndEvent("e").
					Connect("s", "ite").
					Connect("ite", "e").
					Build()
				if err != nil {
					t.Fatalf("Build: %v", err)
				}
				return def
			},
			assert: func(t *testing.T, n model.Node) {
				if n.Kind() != model.KindIntermediateThrowEvent {
					t.Errorf("Kind() = %v, want KindIntermediateThrowEvent", n.Kind())
				}
			},
		},
		{
			name:   "AddBoundaryEvent",
			nodeID: "be",
			build: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				// BoundaryEvent must attach to an activity node (here "task").
				def, err := model.NewDefinition("p", 1).
					AddStartEvent("s").
					AddServiceTask("task", model.WithActionName("do")).
					AddBoundaryEvent("be", "task", model.WithBoundaryTimer("PT30M")).
					AddEndEvent("e").
					AddEndEvent("be-end").
					Connect("s", "task").
					Connect("task", "e").
					Connect("be", "be-end").
					Build()
				if err != nil {
					t.Fatalf("Build: %v", err)
				}
				return def
			},
			assert: func(t *testing.T, n model.Node) {
				if n.Kind() != model.KindBoundaryEvent {
					t.Errorf("Kind() = %v, want KindBoundaryEvent", n.Kind())
				}
				be, ok := n.(model.BoundaryEvent)
				if !ok {
					t.Fatalf("node is %T, want model.BoundaryEvent", n)
				}
				if be.AttachedTo != "task" {
					t.Errorf("AttachedTo = %q, want task", be.AttachedTo)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			def := tc.build(t)
			n := nodeByID(t, def, tc.nodeID)
			if n.ID() != tc.nodeID {
				t.Errorf("ID() = %q, want %q", n.ID(), tc.nodeID)
			}
			tc.assert(t, n)
		})
	}
}

// TestBuilderFluentOptionThreading verifies that options passed to AddX are
// forwarded correctly to the underlying node.
func TestBuilderFluentOptionThreading(t *testing.T) {
	t.Parallel()

	t.Run("AddStartEvent_WithName", func(t *testing.T) {
		t.Parallel()
		def, err := model.NewDefinition("p", 1).
			AddStartEvent("s", model.WithName("Entry Point")).
			AddEndEvent("e").
			Connect("s", "e").
			Build()
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		n := nodeByID(t, def, "s")
		if n.Name() != "Entry Point" {
			t.Errorf("Name() = %q, want 'Entry Point'", n.Name())
		}
	})

	t.Run("AddServiceTask_WithActionName", func(t *testing.T) {
		t.Parallel()
		def, err := model.NewDefinition("p", 1).
			AddStartEvent("s").
			AddServiceTask("t", model.WithActionName("do-work")).
			AddEndEvent("e").
			Connect("s", "t").
			Connect("t", "e").
			Build()
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		n := nodeByID(t, def, "t")
		if model.ActionOf(n) != "do-work" {
			t.Errorf("ActionOf() = %q, want do-work", model.ActionOf(n))
		}
	})

	t.Run("AddUserTask_roles", func(t *testing.T) {
		t.Parallel()
		def, err := model.NewDefinition("p", 1).
			AddStartEvent("s").
			AddUserTask("t", []string{"manager", "admin"}).
			AddEndEvent("e").
			Connect("s", "t").
			Connect("t", "e").
			Build()
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		n := nodeByID(t, def, "t")
		ut, ok := n.(model.UserTask)
		if !ok {
			t.Fatalf("node is %T, want model.UserTask", n)
		}
		if len(ut.CandidateRoles) != 2 || ut.CandidateRoles[0] != "manager" || ut.CandidateRoles[1] != "admin" {
			t.Errorf("CandidateRoles = %v, want [manager admin]", ut.CandidateRoles)
		}
	})

	t.Run("AddReceiveTask_messageName", func(t *testing.T) {
		t.Parallel()
		def, err := model.NewDefinition("p", 1).
			AddStartEvent("s").
			AddReceiveTask("t", "payment.received").
			AddEndEvent("e").
			Connect("s", "t").
			Connect("t", "e").
			Build()
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		n := nodeByID(t, def, "t")
		rt, ok := n.(model.ReceiveTask)
		if !ok {
			t.Fatalf("node is %T, want model.ReceiveTask", n)
		}
		if rt.MessageName != "payment.received" {
			t.Errorf("MessageName = %q, want payment.received", rt.MessageName)
		}
	})

	t.Run("AddSendTask_messageName", func(t *testing.T) {
		t.Parallel()
		def, err := model.NewDefinition("p", 1).
			AddStartEvent("s").
			AddSendTask("t", "invoice.issued").
			AddEndEvent("e").
			Connect("s", "t").
			Connect("t", "e").
			Build()
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		n := nodeByID(t, def, "t")
		st, ok := n.(model.SendTask)
		if !ok {
			t.Fatalf("node is %T, want model.SendTask", n)
		}
		if st.MessageName != "invoice.issued" {
			t.Errorf("MessageName = %q, want invoice.issued", st.MessageName)
		}
	})

	t.Run("AddBusinessRuleTask_WithActionName", func(t *testing.T) {
		t.Parallel()
		def, err := model.NewDefinition("p", 1).
			AddStartEvent("s").
			AddBusinessRuleTask("t", model.WithActionName("credit-check")).
			AddEndEvent("e").
			Connect("s", "t").
			Connect("t", "e").
			Build()
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		n := nodeByID(t, def, "t")
		if model.ActionOf(n) != "credit-check" {
			t.Errorf("ActionOf() = %q, want credit-check", model.ActionOf(n))
		}
	})
}

// TestBuilderFluentEquivalentToAdd proves that AddX is pure forwarding sugar:
// building via AddX produces structurally identical nodes to Add(NewX(...)).
func TestBuilderFluentEquivalentToAdd(t *testing.T) {
	t.Parallel()

	sub, err := model.NewDefinition("sub", 1).
		Add(model.NewStartEvent("ss")).
		Add(model.NewEndEvent("se")).
		Connect("ss", "se").
		Build()
	if err != nil {
		t.Fatalf("sub Build: %v", err)
	}

	buildViaFluent := func(t *testing.T) *model.ProcessDefinition {
		t.Helper()
		def, err := model.NewDefinition("p", 1).
			AddStartEvent("s").
			AddServiceTask("t1", model.WithActionName("do")).
			AddUserTask("t2", []string{"manager"}).
			AddReceiveTask("t3", "msg.in").
			AddSendTask("t4", "msg.out").
			AddSubProcess("t5", sub).
			AddEndEvent("e").
			Connect("s", "t1").
			Connect("t1", "t2").
			Connect("t2", "t3").
			Connect("t3", "t4").
			Connect("t4", "t5").
			Connect("t5", "e").
			Build()
		if err != nil {
			t.Fatalf("fluent Build: %v", err)
		}
		return def
	}

	buildViaAdd := func(t *testing.T) *model.ProcessDefinition {
		t.Helper()
		def, err := model.NewDefinition("p", 1).
			Add(model.NewStartEvent("s")).
			Add(model.NewServiceTask("t1", model.WithActionName("do"))).
			Add(model.NewUserTask("t2", []string{"manager"})).
			Add(model.NewReceiveTask("t3", "msg.in")).
			Add(model.NewSendTask("t4", "msg.out")).
			Add(model.NewSubProcess("t5", sub)).
			Add(model.NewEndEvent("e")).
			Connect("s", "t1").
			Connect("t1", "t2").
			Connect("t2", "t3").
			Connect("t3", "t4").
			Connect("t4", "t5").
			Connect("t5", "e").
			Build()
		if err != nil {
			t.Fatalf("Add Build: %v", err)
		}
		return def
	}

	fluentDef := buildViaFluent(t)
	addDef := buildViaAdd(t)

	if len(fluentDef.Nodes) != len(addDef.Nodes) {
		t.Fatalf("node count: fluent=%d add=%d", len(fluentDef.Nodes), len(addDef.Nodes))
	}
	for i := range fluentDef.Nodes {
		fn := fluentDef.Nodes[i]
		an := addDef.Nodes[i]
		if fn.Kind() != an.Kind() {
			t.Errorf("node[%d] Kind: fluent=%v add=%v", i, fn.Kind(), an.Kind())
		}
		if fn.ID() != an.ID() {
			t.Errorf("node[%d] ID: fluent=%q add=%q", i, fn.ID(), an.ID())
		}
		if !reflect.DeepEqual(fn, an) {
			t.Errorf("node[%d] %q not deeply equal: fluent=%+v add=%+v", i, fn.ID(), fn, an)
		}
	}

	if len(fluentDef.Flows) != len(addDef.Flows) {
		t.Fatalf("flow count: fluent=%d add=%d", len(fluentDef.Flows), len(addDef.Flows))
	}
}

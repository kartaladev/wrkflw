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

// TestBuilderFluentEquivalentToAdd proves that every AddX method is pure
// forwarding sugar: building via AddX(args...) produces a structurally
// identical node to Add(NewX(args...)) for all 19 node kinds.
//
// For each kind a minimal valid graph is built two ways — once with the
// fluent AddX method and once with the explicit Add(NewX(...)) form — using
// the same IDs, args, and options. The node-under-test is located by ID in
// both definitions and compared via reflect.DeepEqual. Node count and flow
// count are asserted as a structural backstop.
//
// The graph recipes mirror TestBuilderFluentAddMethods exactly so the only
// difference between the two builds is the call form.
func TestBuilderFluentEquivalentToAdd(t *testing.T) {
	t.Parallel()

	sub := minimalSubDef(t)

	type testCase struct {
		name   string
		nodeID string
		// buildFluent builds the minimal valid graph using the AddX fluent method.
		buildFluent func(t *testing.T) *model.ProcessDefinition
		// buildAdd builds the same graph using Add(NewX(...)).
		buildAdd func(t *testing.T) *model.ProcessDefinition
	}

	cases := []testCase{
		{
			name:   "AddStartEvent",
			nodeID: "s",
			buildFluent: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					AddStartEvent("s").
					AddEndEvent("e").
					Connect("s", "e").
					Build()
				if err != nil {
					t.Fatalf("fluent Build: %v", err)
				}
				return def
			},
			buildAdd: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					Add(model.NewStartEvent("s")).
					Add(model.NewEndEvent("e")).
					Connect("s", "e").
					Build()
				if err != nil {
					t.Fatalf("Add Build: %v", err)
				}
				return def
			},
		},
		{
			name:   "AddEndEvent",
			nodeID: "e",
			buildFluent: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					AddStartEvent("s").
					AddEndEvent("e").
					Connect("s", "e").
					Build()
				if err != nil {
					t.Fatalf("fluent Build: %v", err)
				}
				return def
			},
			buildAdd: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					Add(model.NewStartEvent("s")).
					Add(model.NewEndEvent("e")).
					Connect("s", "e").
					Build()
				if err != nil {
					t.Fatalf("Add Build: %v", err)
				}
				return def
			},
		},
		{
			name:   "AddTerminateEndEvent",
			nodeID: "te",
			buildFluent: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					AddStartEvent("s").
					AddTerminateEndEvent("te").
					Connect("s", "te").
					Build()
				if err != nil {
					t.Fatalf("fluent Build: %v", err)
				}
				return def
			},
			buildAdd: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					Add(model.NewStartEvent("s")).
					Add(model.NewTerminateEndEvent("te")).
					Connect("s", "te").
					Build()
				if err != nil {
					t.Fatalf("Add Build: %v", err)
				}
				return def
			},
		},
		{
			name:   "AddErrorEndEvent",
			nodeID: "ee",
			buildFluent: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					AddStartEvent("s").
					AddErrorEndEvent("ee", "ERR_X").
					Connect("s", "ee").
					Build()
				if err != nil {
					t.Fatalf("fluent Build: %v", err)
				}
				return def
			},
			buildAdd: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					Add(model.NewStartEvent("s")).
					Add(model.NewErrorEndEvent("ee", "ERR_X")).
					Connect("s", "ee").
					Build()
				if err != nil {
					t.Fatalf("Add Build: %v", err)
				}
				return def
			},
		},
		{
			name:   "AddExclusiveGateway",
			nodeID: "gw",
			buildFluent: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					AddStartEvent("s").
					AddExclusiveGateway("gw").
					AddEndEvent("e").
					Connect("s", "gw").
					Connect("gw", "e").
					Build()
				if err != nil {
					t.Fatalf("fluent Build: %v", err)
				}
				return def
			},
			buildAdd: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					Add(model.NewStartEvent("s")).
					Add(model.NewExclusiveGateway("gw")).
					Add(model.NewEndEvent("e")).
					Connect("s", "gw").
					Connect("gw", "e").
					Build()
				if err != nil {
					t.Fatalf("Add Build: %v", err)
				}
				return def
			},
		},
		{
			name:   "AddParallelGateway",
			nodeID: "gw",
			buildFluent: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					AddStartEvent("s").
					AddParallelGateway("gw").
					AddEndEvent("e").
					Connect("s", "gw").
					Connect("gw", "e").
					Build()
				if err != nil {
					t.Fatalf("fluent Build: %v", err)
				}
				return def
			},
			buildAdd: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					Add(model.NewStartEvent("s")).
					Add(model.NewParallelGateway("gw")).
					Add(model.NewEndEvent("e")).
					Connect("s", "gw").
					Connect("gw", "e").
					Build()
				if err != nil {
					t.Fatalf("Add Build: %v", err)
				}
				return def
			},
		},
		{
			name:   "AddInclusiveGateway",
			nodeID: "gw",
			buildFluent: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					AddStartEvent("s").
					AddInclusiveGateway("gw").
					AddEndEvent("e").
					Connect("s", "gw").
					Connect("gw", "e").
					Build()
				if err != nil {
					t.Fatalf("fluent Build: %v", err)
				}
				return def
			},
			buildAdd: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					Add(model.NewStartEvent("s")).
					Add(model.NewInclusiveGateway("gw")).
					Add(model.NewEndEvent("e")).
					Connect("s", "gw").
					Connect("gw", "e").
					Build()
				if err != nil {
					t.Fatalf("Add Build: %v", err)
				}
				return def
			},
		},
		{
			name:   "AddEventBasedGateway",
			nodeID: "gw",
			buildFluent: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
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
					t.Fatalf("fluent Build: %v", err)
				}
				return def
			},
			buildAdd: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					Add(model.NewStartEvent("s")).
					Add(model.NewEventBasedGateway("gw")).
					Add(model.NewIntermediateCatchEvent("ice", model.WithTimerDuration("PT1H"))).
					Add(model.NewEndEvent("e")).
					Connect("s", "gw").
					Connect("gw", "ice").
					Connect("ice", "e").
					Build()
				if err != nil {
					t.Fatalf("Add Build: %v", err)
				}
				return def
			},
		},
		{
			name:   "AddServiceTask",
			nodeID: "t",
			buildFluent: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					AddStartEvent("s").
					AddServiceTask("t").
					AddEndEvent("e").
					Connect("s", "t").
					Connect("t", "e").
					Build()
				if err != nil {
					t.Fatalf("fluent Build: %v", err)
				}
				return def
			},
			buildAdd: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					Add(model.NewStartEvent("s")).
					Add(model.NewServiceTask("t")).
					Add(model.NewEndEvent("e")).
					Connect("s", "t").
					Connect("t", "e").
					Build()
				if err != nil {
					t.Fatalf("Add Build: %v", err)
				}
				return def
			},
		},
		{
			name:   "AddUserTask",
			nodeID: "t",
			buildFluent: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					AddStartEvent("s").
					AddUserTask("t", []string{"manager"}).
					AddEndEvent("e").
					Connect("s", "t").
					Connect("t", "e").
					Build()
				if err != nil {
					t.Fatalf("fluent Build: %v", err)
				}
				return def
			},
			buildAdd: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					Add(model.NewStartEvent("s")).
					Add(model.NewUserTask("t", []string{"manager"})).
					Add(model.NewEndEvent("e")).
					Connect("s", "t").
					Connect("t", "e").
					Build()
				if err != nil {
					t.Fatalf("Add Build: %v", err)
				}
				return def
			},
		},
		{
			name:   "AddReceiveTask",
			nodeID: "t",
			buildFluent: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					AddStartEvent("s").
					AddReceiveTask("t", "order.placed").
					AddEndEvent("e").
					Connect("s", "t").
					Connect("t", "e").
					Build()
				if err != nil {
					t.Fatalf("fluent Build: %v", err)
				}
				return def
			},
			buildAdd: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					Add(model.NewStartEvent("s")).
					Add(model.NewReceiveTask("t", "order.placed")).
					Add(model.NewEndEvent("e")).
					Connect("s", "t").
					Connect("t", "e").
					Build()
				if err != nil {
					t.Fatalf("Add Build: %v", err)
				}
				return def
			},
		},
		{
			name:   "AddSendTask",
			nodeID: "t",
			buildFluent: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					AddStartEvent("s").
					AddSendTask("t", "order.confirmed").
					AddEndEvent("e").
					Connect("s", "t").
					Connect("t", "e").
					Build()
				if err != nil {
					t.Fatalf("fluent Build: %v", err)
				}
				return def
			},
			buildAdd: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					Add(model.NewStartEvent("s")).
					Add(model.NewSendTask("t", "order.confirmed")).
					Add(model.NewEndEvent("e")).
					Connect("s", "t").
					Connect("t", "e").
					Build()
				if err != nil {
					t.Fatalf("Add Build: %v", err)
				}
				return def
			},
		},
		{
			name:   "AddBusinessRuleTask",
			nodeID: "t",
			buildFluent: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					AddStartEvent("s").
					AddBusinessRuleTask("t").
					AddEndEvent("e").
					Connect("s", "t").
					Connect("t", "e").
					Build()
				if err != nil {
					t.Fatalf("fluent Build: %v", err)
				}
				return def
			},
			buildAdd: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					Add(model.NewStartEvent("s")).
					Add(model.NewBusinessRuleTask("t")).
					Add(model.NewEndEvent("e")).
					Connect("s", "t").
					Connect("t", "e").
					Build()
				if err != nil {
					t.Fatalf("Add Build: %v", err)
				}
				return def
			},
		},
		{
			name:   "AddSubProcess",
			nodeID: "t",
			buildFluent: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					AddStartEvent("s").
					AddSubProcess("t", sub).
					AddEndEvent("e").
					Connect("s", "t").
					Connect("t", "e").
					Build()
				if err != nil {
					t.Fatalf("fluent Build: %v", err)
				}
				return def
			},
			buildAdd: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					Add(model.NewStartEvent("s")).
					Add(model.NewSubProcess("t", sub)).
					Add(model.NewEndEvent("e")).
					Connect("s", "t").
					Connect("t", "e").
					Build()
				if err != nil {
					t.Fatalf("Add Build: %v", err)
				}
				return def
			},
		},
		{
			name:   "AddCallActivity",
			nodeID: "t",
			buildFluent: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					AddStartEvent("s").
					AddCallActivity("t", "other-def").
					AddEndEvent("e").
					Connect("s", "t").
					Connect("t", "e").
					Build()
				if err != nil {
					t.Fatalf("fluent Build: %v", err)
				}
				return def
			},
			buildAdd: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					Add(model.NewStartEvent("s")).
					Add(model.NewCallActivity("t", "other-def")).
					Add(model.NewEndEvent("e")).
					Connect("s", "t").
					Connect("t", "e").
					Build()
				if err != nil {
					t.Fatalf("Add Build: %v", err)
				}
				return def
			},
		},
		{
			name:   "AddEventSubProcess",
			nodeID: "esp",
			buildFluent: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					AddStartEvent("s").
					AddEventSubProcess("esp", sub).
					AddEndEvent("e").
					Connect("s", "esp").
					Connect("esp", "e").
					Build()
				if err != nil {
					t.Fatalf("fluent Build: %v", err)
				}
				return def
			},
			buildAdd: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					Add(model.NewStartEvent("s")).
					Add(model.NewEventSubProcess("esp", sub)).
					Add(model.NewEndEvent("e")).
					Connect("s", "esp").
					Connect("esp", "e").
					Build()
				if err != nil {
					t.Fatalf("Add Build: %v", err)
				}
				return def
			},
		},
		{
			name:   "AddIntermediateCatchEvent",
			nodeID: "ice",
			buildFluent: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					AddStartEvent("s").
					AddIntermediateCatchEvent("ice", model.WithTimerDuration("PT1H")).
					AddEndEvent("e").
					Connect("s", "ice").
					Connect("ice", "e").
					Build()
				if err != nil {
					t.Fatalf("fluent Build: %v", err)
				}
				return def
			},
			buildAdd: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					Add(model.NewStartEvent("s")).
					Add(model.NewIntermediateCatchEvent("ice", model.WithTimerDuration("PT1H"))).
					Add(model.NewEndEvent("e")).
					Connect("s", "ice").
					Connect("ice", "e").
					Build()
				if err != nil {
					t.Fatalf("Add Build: %v", err)
				}
				return def
			},
		},
		{
			name:   "AddIntermediateThrowEvent",
			nodeID: "ite",
			buildFluent: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					AddStartEvent("s").
					AddIntermediateThrowEvent("ite", model.WithThrowSignal("sig")).
					AddEndEvent("e").
					Connect("s", "ite").
					Connect("ite", "e").
					Build()
				if err != nil {
					t.Fatalf("fluent Build: %v", err)
				}
				return def
			},
			buildAdd: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					Add(model.NewStartEvent("s")).
					Add(model.NewIntermediateThrowEvent("ite", model.WithThrowSignal("sig"))).
					Add(model.NewEndEvent("e")).
					Connect("s", "ite").
					Connect("ite", "e").
					Build()
				if err != nil {
					t.Fatalf("Add Build: %v", err)
				}
				return def
			},
		},
		{
			name:   "AddBoundaryEvent",
			nodeID: "be",
			buildFluent: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
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
					t.Fatalf("fluent Build: %v", err)
				}
				return def
			},
			buildAdd: func(t *testing.T) *model.ProcessDefinition {
				t.Helper()
				def, err := model.NewDefinition("p", 1).
					Add(model.NewStartEvent("s")).
					Add(model.NewServiceTask("task", model.WithActionName("do"))).
					Add(model.NewBoundaryEvent("be", "task", model.WithBoundaryTimer("PT30M"))).
					Add(model.NewEndEvent("e")).
					Add(model.NewEndEvent("be-end")).
					Connect("s", "task").
					Connect("task", "e").
					Connect("be", "be-end").
					Build()
				if err != nil {
					t.Fatalf("Add Build: %v", err)
				}
				return def
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fluentDef := tc.buildFluent(t)
			addDef := tc.buildAdd(t)

			fluentNode := nodeByID(t, fluentDef, tc.nodeID)
			addNode := nodeByID(t, addDef, tc.nodeID)

			if len(fluentDef.Nodes) != len(addDef.Nodes) {
				t.Errorf("node count: fluent=%d add=%d", len(fluentDef.Nodes), len(addDef.Nodes))
			}
			if len(fluentDef.Flows) != len(addDef.Flows) {
				t.Errorf("flow count: fluent=%d add=%d", len(fluentDef.Flows), len(addDef.Flows))
			}
			if !reflect.DeepEqual(fluentNode, addNode) {
				t.Errorf("node %q not deeply equal:\n  fluent=%+v\n  add=%+v", tc.nodeID, fluentNode, addNode)
			}
		})
	}
}

package definition_test

import (
	"reflect"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/definition"
)

// minimalSubDef returns a minimal valid sub-process definition (start→end).
func minimalSubDef(t *testing.T) *definition.ProcessDefinition {
	t.Helper()
	def, err := definition.NewDefinition("sub", 1).
		Add(definition.NewStartEvent("ss")).
		Add(definition.NewEndEvent("se")).
		Connect("ss", "se").
		Build()
	if err != nil {
		t.Fatalf("minimalSubDef: %v", err)
	}
	return def
}

// nodeByID returns the node with the given ID from a definition, or fails.
func nodeByID(t *testing.T, def *definition.ProcessDefinition, id string) definition.Node {
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
		build func(t *testing.T) *definition.ProcessDefinition
		// nodeID to look up and assert on
		nodeID string
		assert func(t *testing.T, n definition.Node)
	}

	cases := []testCase{
		{
			name:   "AddStartEvent",
			nodeID: "s",
			build: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
					AddStartEvent("s").
					AddEndEvent("e").
					Connect("s", "e").
					Build()
				if err != nil {
					t.Fatalf("Build: %v", err)
				}
				return def
			},
			assert: func(t *testing.T, n definition.Node) {
				if n.Kind() != definition.KindStartEvent {
					t.Errorf("Kind() = %v, want KindStartEvent", n.Kind())
				}
			},
		},
		{
			name:   "AddEndEvent",
			nodeID: "e",
			build: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
					AddStartEvent("s").
					AddEndEvent("e").
					Connect("s", "e").
					Build()
				if err != nil {
					t.Fatalf("Build: %v", err)
				}
				return def
			},
			assert: func(t *testing.T, n definition.Node) {
				if n.Kind() != definition.KindEndEvent {
					t.Errorf("Kind() = %v, want KindEndEvent", n.Kind())
				}
			},
		},
		{
			name:   "AddTerminateEndEvent",
			nodeID: "te",
			build: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
					AddStartEvent("s").
					AddTerminateEndEvent("te").
					Connect("s", "te").
					Build()
				if err != nil {
					t.Fatalf("Build: %v", err)
				}
				return def
			},
			assert: func(t *testing.T, n definition.Node) {
				if n.Kind() != definition.KindTerminateEndEvent {
					t.Errorf("Kind() = %v, want KindTerminateEndEvent", n.Kind())
				}
			},
		},
		{
			name:   "AddErrorEndEvent",
			nodeID: "ee",
			build: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
					AddStartEvent("s").
					AddErrorEndEvent("ee", "ERR_X").
					Connect("s", "ee").
					Build()
				if err != nil {
					t.Fatalf("Build: %v", err)
				}
				return def
			},
			assert: func(t *testing.T, n definition.Node) {
				if n.Kind() != definition.KindErrorEndEvent {
					t.Errorf("Kind() = %v, want KindErrorEndEvent", n.Kind())
				}
				ee, ok := n.(definition.ErrorEndEvent)
				if !ok {
					t.Fatalf("node is %T, want definition.ErrorEndEvent", n)
				}
				if ee.ErrorCode != "ERR_X" {
					t.Errorf("ErrorCode = %q, want ERR_X", ee.ErrorCode)
				}
			},
		},
		{
			name:   "AddExclusiveGateway",
			nodeID: "gw",
			build: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
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
			assert: func(t *testing.T, n definition.Node) {
				if n.Kind() != definition.KindExclusiveGateway {
					t.Errorf("Kind() = %v, want KindExclusiveGateway", n.Kind())
				}
			},
		},
		{
			name:   "AddParallelGateway",
			nodeID: "gw",
			build: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
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
			assert: func(t *testing.T, n definition.Node) {
				if n.Kind() != definition.KindParallelGateway {
					t.Errorf("Kind() = %v, want KindParallelGateway", n.Kind())
				}
			},
		},
		{
			name:   "AddInclusiveGateway",
			nodeID: "gw",
			build: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
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
			assert: func(t *testing.T, n definition.Node) {
				if n.Kind() != definition.KindInclusiveGateway {
					t.Errorf("Kind() = %v, want KindInclusiveGateway", n.Kind())
				}
			},
		},
		{
			name:   "AddEventBasedGateway",
			nodeID: "gw",
			build: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				// EventBasedGateway must target catch events.
				def, err := definition.NewDefinition("p", 1).
					AddStartEvent("s").
					AddEventBasedGateway("gw").
					AddIntermediateCatchEvent("ice", definition.WithTimerDuration("PT1H")).
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
			assert: func(t *testing.T, n definition.Node) {
				if n.Kind() != definition.KindEventBasedGateway {
					t.Errorf("Kind() = %v, want KindEventBasedGateway", n.Kind())
				}
			},
		},
		{
			name:   "AddServiceTask",
			nodeID: "t",
			build: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
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
			assert: func(t *testing.T, n definition.Node) {
				if n.Kind() != definition.KindServiceTask {
					t.Errorf("Kind() = %v, want KindServiceTask", n.Kind())
				}
			},
		},
		{
			name:   "AddUserTask",
			nodeID: "t",
			build: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
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
			assert: func(t *testing.T, n definition.Node) {
				if n.Kind() != definition.KindUserTask {
					t.Errorf("Kind() = %v, want KindUserTask", n.Kind())
				}
			},
		},
		{
			name:   "AddReceiveTask",
			nodeID: "t",
			build: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
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
			assert: func(t *testing.T, n definition.Node) {
				if n.Kind() != definition.KindReceiveTask {
					t.Errorf("Kind() = %v, want KindReceiveTask", n.Kind())
				}
				rt, ok := n.(definition.ReceiveTask)
				if !ok {
					t.Fatalf("node is %T, want definition.ReceiveTask", n)
				}
				if rt.MessageName != "order.placed" {
					t.Errorf("MessageName = %q, want order.placed", rt.MessageName)
				}
			},
		},
		{
			name:   "AddSendTask",
			nodeID: "t",
			build: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
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
			assert: func(t *testing.T, n definition.Node) {
				if n.Kind() != definition.KindSendTask {
					t.Errorf("Kind() = %v, want KindSendTask", n.Kind())
				}
				st, ok := n.(definition.SendTask)
				if !ok {
					t.Fatalf("node is %T, want definition.SendTask", n)
				}
				if st.MessageName != "order.confirmed" {
					t.Errorf("MessageName = %q, want order.confirmed", st.MessageName)
				}
			},
		},
		{
			name:   "AddBusinessRuleTask",
			nodeID: "t",
			build: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
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
			assert: func(t *testing.T, n definition.Node) {
				if n.Kind() != definition.KindBusinessRuleTask {
					t.Errorf("Kind() = %v, want KindBusinessRuleTask", n.Kind())
				}
			},
		},
		{
			name:   "AddSubProcess",
			nodeID: "t",
			build: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
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
			assert: func(t *testing.T, n definition.Node) {
				if n.Kind() != definition.KindSubProcess {
					t.Errorf("Kind() = %v, want KindSubProcess", n.Kind())
				}
			},
		},
		{
			name:   "AddCallActivity",
			nodeID: "t",
			build: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
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
			assert: func(t *testing.T, n definition.Node) {
				if n.Kind() != definition.KindCallActivity {
					t.Errorf("Kind() = %v, want KindCallActivity", n.Kind())
				}
				ca, ok := n.(definition.CallActivity)
				if !ok {
					t.Fatalf("node is %T, want definition.CallActivity", n)
				}
				if ca.DefRef != "other-def" {
					t.Errorf("DefRef = %q, want other-def", ca.DefRef)
				}
			},
		},
		{
			name:   "AddEventSubProcess",
			nodeID: "esp",
			build: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
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
			assert: func(t *testing.T, n definition.Node) {
				if n.Kind() != definition.KindEventSubProcess {
					t.Errorf("Kind() = %v, want KindEventSubProcess", n.Kind())
				}
			},
		},
		{
			name:   "AddIntermediateCatchEvent",
			nodeID: "ice",
			build: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
					AddStartEvent("s").
					AddIntermediateCatchEvent("ice", definition.WithTimerDuration("PT1H")).
					AddEndEvent("e").
					Connect("s", "ice").
					Connect("ice", "e").
					Build()
				if err != nil {
					t.Fatalf("Build: %v", err)
				}
				return def
			},
			assert: func(t *testing.T, n definition.Node) {
				if n.Kind() != definition.KindIntermediateCatchEvent {
					t.Errorf("Kind() = %v, want KindIntermediateCatchEvent", n.Kind())
				}
			},
		},
		{
			name:   "AddIntermediateThrowEvent",
			nodeID: "ite",
			build: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
					AddStartEvent("s").
					AddIntermediateThrowEvent("ite", definition.WithThrowSignal("sig")).
					AddEndEvent("e").
					Connect("s", "ite").
					Connect("ite", "e").
					Build()
				if err != nil {
					t.Fatalf("Build: %v", err)
				}
				return def
			},
			assert: func(t *testing.T, n definition.Node) {
				if n.Kind() != definition.KindIntermediateThrowEvent {
					t.Errorf("Kind() = %v, want KindIntermediateThrowEvent", n.Kind())
				}
			},
		},
		{
			name:   "AddBoundaryEvent",
			nodeID: "be",
			build: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				// BoundaryEvent must attach to an activity node (here "task").
				def, err := definition.NewDefinition("p", 1).
					AddStartEvent("s").
					AddServiceTask("task", definition.WithActionName("do")).
					AddBoundaryEvent("be", "task", definition.WithBoundaryTimer("PT30M")).
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
			assert: func(t *testing.T, n definition.Node) {
				if n.Kind() != definition.KindBoundaryEvent {
					t.Errorf("Kind() = %v, want KindBoundaryEvent", n.Kind())
				}
				be, ok := n.(definition.BoundaryEvent)
				if !ok {
					t.Fatalf("node is %T, want definition.BoundaryEvent", n)
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
		def, err := definition.NewDefinition("p", 1).
			AddStartEvent("s", definition.WithName("Entry Point")).
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
		def, err := definition.NewDefinition("p", 1).
			AddStartEvent("s").
			AddServiceTask("t", definition.WithActionName("do-work")).
			AddEndEvent("e").
			Connect("s", "t").
			Connect("t", "e").
			Build()
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		n := nodeByID(t, def, "t")
		if definition.ActionOf(n) != "do-work" {
			t.Errorf("ActionOf() = %q, want do-work", definition.ActionOf(n))
		}
	})

	t.Run("AddUserTask_roles", func(t *testing.T) {
		t.Parallel()
		def, err := definition.NewDefinition("p", 1).
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
		ut, ok := n.(definition.UserTask)
		if !ok {
			t.Fatalf("node is %T, want definition.UserTask", n)
		}
		if len(ut.CandidateRoles) != 2 || ut.CandidateRoles[0] != "manager" || ut.CandidateRoles[1] != "admin" {
			t.Errorf("CandidateRoles = %v, want [manager admin]", ut.CandidateRoles)
		}
	})

	t.Run("AddReceiveTask_messageName", func(t *testing.T) {
		t.Parallel()
		def, err := definition.NewDefinition("p", 1).
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
		rt, ok := n.(definition.ReceiveTask)
		if !ok {
			t.Fatalf("node is %T, want definition.ReceiveTask", n)
		}
		if rt.MessageName != "payment.received" {
			t.Errorf("MessageName = %q, want payment.received", rt.MessageName)
		}
	})

	t.Run("AddSendTask_messageName", func(t *testing.T) {
		t.Parallel()
		def, err := definition.NewDefinition("p", 1).
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
		st, ok := n.(definition.SendTask)
		if !ok {
			t.Fatalf("node is %T, want definition.SendTask", n)
		}
		if st.MessageName != "invoice.issued" {
			t.Errorf("MessageName = %q, want invoice.issued", st.MessageName)
		}
	})

	t.Run("AddBusinessRuleTask_WithActionName", func(t *testing.T) {
		t.Parallel()
		def, err := definition.NewDefinition("p", 1).
			AddStartEvent("s").
			AddBusinessRuleTask("t", definition.WithActionName("credit-check")).
			AddEndEvent("e").
			Connect("s", "t").
			Connect("t", "e").
			Build()
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		n := nodeByID(t, def, "t")
		if definition.ActionOf(n) != "credit-check" {
			t.Errorf("ActionOf() = %q, want credit-check", definition.ActionOf(n))
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
		buildFluent func(t *testing.T) *definition.ProcessDefinition
		// buildAdd builds the same graph using Add(NewX(...)).
		buildAdd func(t *testing.T) *definition.ProcessDefinition
	}

	cases := []testCase{
		{
			name:   "AddStartEvent",
			nodeID: "s",
			buildFluent: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
					AddStartEvent("s").
					AddEndEvent("e").
					Connect("s", "e").
					Build()
				if err != nil {
					t.Fatalf("fluent Build: %v", err)
				}
				return def
			},
			buildAdd: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
					Add(definition.NewStartEvent("s")).
					Add(definition.NewEndEvent("e")).
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
			buildFluent: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
					AddStartEvent("s").
					AddEndEvent("e").
					Connect("s", "e").
					Build()
				if err != nil {
					t.Fatalf("fluent Build: %v", err)
				}
				return def
			},
			buildAdd: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
					Add(definition.NewStartEvent("s")).
					Add(definition.NewEndEvent("e")).
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
			buildFluent: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
					AddStartEvent("s").
					AddTerminateEndEvent("te").
					Connect("s", "te").
					Build()
				if err != nil {
					t.Fatalf("fluent Build: %v", err)
				}
				return def
			},
			buildAdd: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
					Add(definition.NewStartEvent("s")).
					Add(definition.NewTerminateEndEvent("te")).
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
			buildFluent: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
					AddStartEvent("s").
					AddErrorEndEvent("ee", "ERR_X").
					Connect("s", "ee").
					Build()
				if err != nil {
					t.Fatalf("fluent Build: %v", err)
				}
				return def
			},
			buildAdd: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
					Add(definition.NewStartEvent("s")).
					Add(definition.NewErrorEndEvent("ee", "ERR_X")).
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
			buildFluent: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
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
			buildAdd: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
					Add(definition.NewStartEvent("s")).
					Add(definition.NewExclusiveGateway("gw")).
					Add(definition.NewEndEvent("e")).
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
			buildFluent: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
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
			buildAdd: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
					Add(definition.NewStartEvent("s")).
					Add(definition.NewParallelGateway("gw")).
					Add(definition.NewEndEvent("e")).
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
			buildFluent: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
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
			buildAdd: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
					Add(definition.NewStartEvent("s")).
					Add(definition.NewInclusiveGateway("gw")).
					Add(definition.NewEndEvent("e")).
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
			buildFluent: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
					AddStartEvent("s").
					AddEventBasedGateway("gw").
					AddIntermediateCatchEvent("ice", definition.WithTimerDuration("PT1H")).
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
			buildAdd: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
					Add(definition.NewStartEvent("s")).
					Add(definition.NewEventBasedGateway("gw")).
					Add(definition.NewIntermediateCatchEvent("ice", definition.WithTimerDuration("PT1H"))).
					Add(definition.NewEndEvent("e")).
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
			buildFluent: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
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
			buildAdd: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
					Add(definition.NewStartEvent("s")).
					Add(definition.NewServiceTask("t")).
					Add(definition.NewEndEvent("e")).
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
			buildFluent: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
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
			buildAdd: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
					Add(definition.NewStartEvent("s")).
					Add(definition.NewUserTask("t", []string{"manager"})).
					Add(definition.NewEndEvent("e")).
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
			buildFluent: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
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
			buildAdd: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
					Add(definition.NewStartEvent("s")).
					Add(definition.NewReceiveTask("t", "order.placed")).
					Add(definition.NewEndEvent("e")).
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
			buildFluent: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
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
			buildAdd: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
					Add(definition.NewStartEvent("s")).
					Add(definition.NewSendTask("t", "order.confirmed")).
					Add(definition.NewEndEvent("e")).
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
			buildFluent: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
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
			buildAdd: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
					Add(definition.NewStartEvent("s")).
					Add(definition.NewBusinessRuleTask("t")).
					Add(definition.NewEndEvent("e")).
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
			buildFluent: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
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
			buildAdd: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
					Add(definition.NewStartEvent("s")).
					Add(definition.NewSubProcess("t", sub)).
					Add(definition.NewEndEvent("e")).
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
			buildFluent: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
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
			buildAdd: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
					Add(definition.NewStartEvent("s")).
					Add(definition.NewCallActivity("t", "other-def")).
					Add(definition.NewEndEvent("e")).
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
			buildFluent: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
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
			buildAdd: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
					Add(definition.NewStartEvent("s")).
					Add(definition.NewEventSubProcess("esp", sub)).
					Add(definition.NewEndEvent("e")).
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
			buildFluent: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
					AddStartEvent("s").
					AddIntermediateCatchEvent("ice", definition.WithTimerDuration("PT1H")).
					AddEndEvent("e").
					Connect("s", "ice").
					Connect("ice", "e").
					Build()
				if err != nil {
					t.Fatalf("fluent Build: %v", err)
				}
				return def
			},
			buildAdd: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
					Add(definition.NewStartEvent("s")).
					Add(definition.NewIntermediateCatchEvent("ice", definition.WithTimerDuration("PT1H"))).
					Add(definition.NewEndEvent("e")).
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
			buildFluent: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
					AddStartEvent("s").
					AddIntermediateThrowEvent("ite", definition.WithThrowSignal("sig")).
					AddEndEvent("e").
					Connect("s", "ite").
					Connect("ite", "e").
					Build()
				if err != nil {
					t.Fatalf("fluent Build: %v", err)
				}
				return def
			},
			buildAdd: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
					Add(definition.NewStartEvent("s")).
					Add(definition.NewIntermediateThrowEvent("ite", definition.WithThrowSignal("sig"))).
					Add(definition.NewEndEvent("e")).
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
			buildFluent: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
					AddStartEvent("s").
					AddServiceTask("task", definition.WithActionName("do")).
					AddBoundaryEvent("be", "task", definition.WithBoundaryTimer("PT30M")).
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
			buildAdd: func(t *testing.T) *definition.ProcessDefinition {
				t.Helper()
				def, err := definition.NewDefinition("p", 1).
					Add(definition.NewStartEvent("s")).
					Add(definition.NewServiceTask("task", definition.WithActionName("do"))).
					Add(definition.NewBoundaryEvent("be", "task", definition.WithBoundaryTimer("PT30M"))).
					Add(definition.NewEndEvent("e")).
					Add(definition.NewEndEvent("be-end")).
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

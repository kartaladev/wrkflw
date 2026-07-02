package model_test

import (
	"os"
	"strings"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/model"
)

func TestParseYAML(t *testing.T) {
	data, err := os.ReadFile("testdata/order.yaml")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	ld, err := model.ParseYAML(data)
	if err != nil {
		t.Fatalf("ParseYAML: %v", err)
	}
	def, err := ld.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if def.ID != "order" || len(def.Nodes) != 3 {
		t.Fatalf("def = %+v", def)
	}
	st, ok := def.Nodes[1].(model.ServiceTask)
	if !ok || st.Action != "charge-card" || st.CompensationAction != "refund-card" {
		t.Fatalf("node[1] = %#v", def.Nodes[1])
	}
}

func TestParseYAMLFlows(t *testing.T) {
	data, err := os.ReadFile("testdata/order.yaml")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	ld, err := model.ParseYAML(data)
	if err != nil {
		t.Fatalf("ParseYAML: %v", err)
	}
	def, err := ld.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(def.Flows) != 2 {
		t.Fatalf("got %d flows, want 2", len(def.Flows))
	}
	if def.Flows[0].ID != "f1" || def.Flows[0].Source != "s" || def.Flows[0].Target != "charge" {
		t.Fatalf("flow[0] = %+v", def.Flows[0])
	}
}

func TestLoadYAML(t *testing.T) {
	f, err := os.Open("testdata/order.yaml")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })

	ld, err := model.LoadYAML(f)
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}
	def, err := ld.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if def.ID != "order" {
		t.Fatalf("ID = %q, want order", def.ID)
	}
}

func TestParseYAMLRejectsInvalid(t *testing.T) {
	// A definition with no start event must produce a validation error at Build time.
	yamlInput := `
id: bad
version: 1
nodes:
  - id: t
    kind: serviceTask
    action: do-something
flows: []
`
	ld, err := model.ParseYAML([]byte(yamlInput))
	if err != nil {
		t.Fatalf("ParseYAML: unexpected parse error: %v", err)
	}
	_, err = ld.Build()
	if err == nil {
		t.Fatal("expected validation error (no start event)")
	}
}

func TestParseYAMLCancelActions(t *testing.T) {
	yamlInput := `
id: p
version: 1
nodes:
  - id: s
    kind: startEvent
  - id: e
    kind: endEvent
flows:
  - { id: f1, source: s, target: e }
cancelActions:
  - cleanup-a
  - cleanup-b
`
	ld, err := model.ParseYAML([]byte(yamlInput))
	if err != nil {
		t.Fatalf("ParseYAML: %v", err)
	}
	def, err := ld.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(def.CancelActions) != 2 || def.CancelActions[0] != "cleanup-a" {
		t.Fatalf("CancelActions = %v", def.CancelActions)
	}
}

func TestParseYAMLFlowCondition(t *testing.T) {
	yamlInput := `
id: p
version: 1
nodes:
  - id: s
    kind: startEvent
  - id: gw
    kind: exclusiveGateway
  - id: a
    kind: serviceTask
    action: act-a
  - id: b
    kind: serviceTask
    action: act-b
  - id: e
    kind: endEvent
flows:
  - { id: f1, source: s, target: gw }
  - { id: f2, source: gw, target: a, condition: "vars.x == 1" }
  - { id: f3, source: gw, target: b, isDefault: true }
  - { id: f4, source: a, target: e }
  - { id: f5, source: b, target: e }
`
	ld, err := model.ParseYAML([]byte(yamlInput))
	if err != nil {
		t.Fatalf("ParseYAML: %v", err)
	}
	def, err := ld.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, f := range def.Flows {
		if f.ID == "f2" && f.Condition != "vars.x == 1" {
			t.Fatalf("f2.Condition = %q, want vars.x == 1", f.Condition)
		}
		if f.ID == "f3" && !f.IsDefault {
			t.Fatal("f3.IsDefault = false, want true")
		}
	}
}

func TestParseYAMLBadYAML(t *testing.T) {
	_, err := model.ParseYAML([]byte("not: valid: yaml: ["))
	if err == nil {
		t.Fatal("expected parse error for invalid YAML")
	}
	_ = strings.Contains(err.Error(), "yaml")
}

func TestParseYAMLEligibilityPrivilegesRoundTrip(t *testing.T) {
	// Hand-written YAML with eligibilityPrivileges on a UserTask.
	yamlInput := `
id: approval-process
version: 1
nodes:
  - id: start
    kind: startEvent
  - id: approve
    kind: userTask
    candidateRoles: ["manager"]
    eligibilityPrivileges: ["finance-task claim"]
  - id: end
    kind: endEvent
flows:
  - { id: f1, source: start, target: approve }
  - { id: f2, source: approve, target: end }
`

	// Parse the YAML and build.
	ld, err := model.ParseYAML([]byte(yamlInput))
	if err != nil {
		t.Fatalf("ParseYAML: %v", err)
	}
	parsed, err := ld.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Verify the parsed UserTask has the correct eligibilityPrivileges.
	approveNode := parsed.Nodes[1]
	ut, ok := approveNode.(model.UserTask)
	if !ok {
		t.Fatalf("node[1] is %T, want model.UserTask", approveNode)
	}
	if len(ut.EligibilityPrivileges) != 1 || ut.EligibilityPrivileges[0] != "finance-task claim" {
		t.Fatalf("EligibilityPrivileges = %v, want [finance-task claim]", ut.EligibilityPrivileges)
	}
	if len(ut.CandidateRoles) != 1 || ut.CandidateRoles[0] != "manager" {
		t.Fatalf("CandidateRoles = %v, want [manager]", ut.CandidateRoles)
	}
}

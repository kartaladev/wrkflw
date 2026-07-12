package model_test

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/model"
)

func TestParseYAML(t *testing.T) {
	data, err := os.ReadFile("testdata/order.yaml")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	ld, err := model.ParseYAML(bytes.NewReader(data))
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
	st, ok := def.Nodes[1].(activity.ServiceTask)
	if !ok || st.Action != "charge-card" || st.CompensateAction != "refund-card" {
		t.Fatalf("node[1] = %#v", def.Nodes[1])
	}
}

func TestParseYAMLFlows(t *testing.T) {
	data, err := os.ReadFile("testdata/order.yaml")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	ld, err := model.ParseYAML(bytes.NewReader(data))
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

	ld, err := model.ParseYAML(f)
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
	ld, err := model.ParseYAML(strings.NewReader(yamlInput))
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
	ld, err := model.ParseYAML(strings.NewReader(yamlInput))
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
	ld, err := model.ParseYAML(strings.NewReader(yamlInput))
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
	_, err := model.ParseYAML(strings.NewReader("not: valid: yaml: ["))
	if err == nil {
		t.Fatal("expected parse error for invalid YAML")
	}
	_ = strings.Contains(err.Error(), "yaml")
}

func TestParseYAMLEligiblePrivilegesRoundTrip(t *testing.T) {
	// Hand-written YAML with eligiblePrivileges on a UserTask.
	yamlInput := `
id: approval-process
version: 1
nodes:
  - id: start
    kind: startEvent
  - id: approve
    kind: userTask
    eligibleRoles: ["manager"]
    eligiblePrivileges: ["finance-task claim"]
  - id: end
    kind: endEvent
flows:
  - { id: f1, source: start, target: approve }
  - { id: f2, source: approve, target: end }
`

	// Parse the YAML and build.
	ld, err := model.ParseYAML(strings.NewReader(yamlInput))
	if err != nil {
		t.Fatalf("ParseYAML: %v", err)
	}
	parsed, err := ld.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Verify the parsed UserTask has the correct eligiblePrivileges.
	approveNode := parsed.Nodes[1]
	ut, ok := approveNode.(activity.UserTask)
	if !ok {
		t.Fatalf("node[1] is %T, want activity.UserTask", approveNode)
	}
	if len(ut.EligiblePrivileges) != 1 || ut.EligiblePrivileges[0] != "finance-task claim" {
		t.Fatalf("EligiblePrivileges = %v, want [finance-task claim]", ut.EligiblePrivileges)
	}
	if len(ut.EligibleRoles) != 1 || ut.EligibleRoles[0] != "manager" {
		t.Fatalf("EligibleRoles = %v, want [manager]", ut.EligibleRoles)
	}
}

func TestParseYAMLUserTaskManual(t *testing.T) {
	const src = `
id: d
version: 1
nodes:
  - {id: s, kind: startEvent}
  - {id: confirm, kind: userTask, manual: true}
  - {id: e, kind: endEvent}
flows:
  - {id: f1, source: s, target: confirm}
  - {id: f2, source: confirm, target: e}
`
	loader, err := model.ParseYAML(strings.NewReader(src))
	if err != nil {
		t.Fatalf("ParseYAML: %v", err)
	}
	parsed, err := loader.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	n, ok := parsed.Node("confirm")
	if !ok {
		t.Fatal("node confirm not found")
	}
	ut, ok := n.(activity.UserTask)
	if !ok {
		t.Fatalf("node is %T, want activity.UserTask", n)
	}
	if !ut.Manual {
		t.Fatal("Manual not decoded from YAML")
	}
}

func TestParseYAMLUserTaskManualImmediate(t *testing.T) {
	const src = `
id: d
version: 1
nodes:
  - {id: s, kind: startEvent}
  - {id: confirm, kind: userTask, manual: true, manualImmediate: true}
  - {id: e, kind: endEvent}
flows:
  - {id: f1, source: s, target: confirm}
  - {id: f2, source: confirm, target: e}
`
	loader, err := model.ParseYAML(strings.NewReader(src))
	if err != nil {
		t.Fatalf("ParseYAML: %v", err)
	}
	parsed, err := loader.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	n, ok := parsed.Node("confirm")
	if !ok {
		t.Fatal("node confirm not found")
	}
	ut := n.(activity.UserTask)
	if !ut.Manual || !ut.ManualImmediate {
		t.Fatalf("Manual=%v ManualImmediate=%v, want both true", ut.Manual, ut.ManualImmediate)
	}
}

// TestParseYAMLEndBehavior verifies that an EndEvent's behavior discriminator
// and its full payload round-trip through the YAML authoring form: the
// terminate payload (terminationReason + terminationOutcome) and the error
// payload (errorCode) are all parsed alongside the endBehavior discriminator.
func TestParseYAMLEndBehavior(t *testing.T) {
	t.Parallel()

	// tmpl is a minimal start→end definition; %s injects the end node's
	// behavior fields (4-space indented to align under "- id: e").
	const tmpl = `id: p
version: 1
nodes:
  - id: s
    kind: startEvent
  - id: e
    kind: endEvent
%s
flows:
  - { id: f1, source: s, target: e }
`

	type testCase struct {
		name   string
		fields string
		assert func(t *testing.T, end event.EndEvent)
	}

	cases := []testCase{
		{
			name:   "terminate abort carries reason and outcome",
			fields: "    endBehavior: terminate\n    terminationReason: fraud detected\n    terminationOutcome: abort",
			assert: func(t *testing.T, end event.EndEvent) {
				assert.Equal(t, event.EndTerminate, end.Behavior)
				assert.Equal(t, "fraud detected", end.TerminationReason)
				assert.Equal(t, event.OutcomeAbort, end.Outcome)
			},
		},
		{
			name:   "terminate complete outcome",
			fields: "    endBehavior: terminate\n    terminationReason: done early\n    terminationOutcome: complete",
			assert: func(t *testing.T, end event.EndEvent) {
				assert.Equal(t, event.EndTerminate, end.Behavior)
				assert.Equal(t, "done early", end.TerminationReason)
				assert.Equal(t, event.OutcomeComplete, end.Outcome)
			},
		},
		{
			name:   "error end carries code",
			fields: "    endBehavior: error\n    errorCode: E_BOOM",
			assert: func(t *testing.T, end event.EndEvent) {
				assert.Equal(t, event.EndError, end.Behavior)
				assert.Equal(t, "E_BOOM", end.ErrorCode)
			},
		},
		{
			name:   "plain end is normal",
			fields: "",
			assert: func(t *testing.T, end event.EndEvent) {
				assert.Equal(t, event.EndNormal, end.Behavior)
				assert.Empty(t, end.TerminationReason)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ld, err := model.ParseYAML(strings.NewReader(fmt.Sprintf(tmpl, tc.fields)))
			require.NoError(t, err)
			def, err := ld.Build()
			require.NoError(t, err)

			end, ok := def.Nodes[1].(event.EndEvent)
			require.True(t, ok, "node[1] should be an EndEvent, got %T", def.Nodes[1])
			tc.assert(t, end)
		})
	}
}

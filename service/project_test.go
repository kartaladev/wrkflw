package service_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/service"
)

func projectDef(t *testing.T) *model.ProcessDefinition {
	t.Helper()
	return &model.ProcessDefinition{ID: "d1", Version: 1}
}

// newProjectEngine builds an engine carrying opts, reusing the package harness.
func newProjectEngine(t *testing.T, opts ...service.Option) service.Service {
	t.Helper()

	h := newHarness(t, linearDef())
	base := []service.Option{
		service.WithProcessDriver(h.driver),
		service.WithInstanceStore(h.store),
		service.WithDefinitions(h.reg),
		service.WithLister(h.lister),
		service.WithHumanTasks(h.taskStore, h.az),
		service.WithClock(h.clk),
	}
	svc, err := service.NewProcessEngine(append(base, opts...)...)
	if err != nil {
		t.Fatalf("NewProcessEngine: %v", err)
	}
	return svc
}

func seedProjectInstance(t *testing.T, svc service.Service) service.ProcessInstance {
	t.Helper()

	pi, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
		DefRef: model.Latest("greeting"),
		Vars:   map[string]any{"name": "x", "ssn": "111-22-3333"},
	})
	if err != nil {
		t.Fatalf("StartInstance: %v", err)
	}
	return pi
}

func projectState(t *testing.T) engine.InstanceState {
	t.Helper()
	return engine.InstanceState{
		InstanceID: "i1", DefID: "d1", DefVersion: 1,
		Variables: map[string]any{"ssn": "111-22-3333"},
	}
}

// stripVariables stands in for runtime/view.PublicState, which service must not import.
func stripVariables(st engine.InstanceState) engine.InstanceState {
	st.Variables = nil
	return st
}

func TestProjectFor_AppliesTheProjection(t *testing.T) {
	t.Parallel()

	pi := service.NewProcessInstance(projectDef(t), projectState(t))
	got := service.ProjectFor(pi, stripVariables, false)

	blob, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(blob), "111-22-3333") {
		t.Errorf("projection not applied\nbody=%s", blob)
	}
	if !strings.Contains(string(blob), `"instance_id":"i1"`) {
		t.Errorf("structural fields lost\nbody=%s", blob)
	}
}

func TestProjectFor_WithholdDefinitionDropsTheEmbed(t *testing.T) {
	t.Parallel()

	pi := service.NewProcessInstance(projectDef(t), projectState(t))

	kept, _ := json.Marshal(service.ProjectFor(pi, stripVariables, false))
	if !strings.Contains(string(kept), `"definition"`) {
		t.Fatalf("precondition: NewProcessInstance embeds the definition\nbody=%s", kept)
	}

	dropped, _ := json.Marshal(service.ProjectFor(pi, stripVariables, true))
	if strings.Contains(string(dropped), `"definition"`) {
		t.Errorf("withholdDefinition must drop the embed\nbody=%s", dropped)
	}
}

// TestProjectFor_NeverUnsetsAnEngineOmission is the guard for the defect this function
// exists to avoid.
//
// An earlier design rebuilt the projected instance with NewProcessInstance, which hardcodes
// omitDefinition=false — so a consumer running WithoutEmbeddedDefinition() had the template
// silently RE-EMBEDDED on this one route, carrying every node's eligibility spec. A
// disclosure fix that widened disclosure. ProjectFor may only ADD omission, never remove it.
func TestProjectFor_NeverUnsetsAnEngineOmission(t *testing.T) {
	t.Parallel()

	eng := newProjectEngine(t, service.WithoutEmbeddedDefinition())
	pi := seedProjectInstance(t, eng)

	before, _ := json.Marshal(pi)
	if strings.Contains(string(before), `"definition"`) {
		t.Fatalf("precondition: WithoutEmbeddedDefinition must suppress the embed\nbody=%s", before)
	}

	after, _ := json.Marshal(service.ProjectFor(pi, stripVariables, false))
	if strings.Contains(string(after), `"definition"`) {
		t.Errorf("ProjectFor RE-EMBEDDED a definition the engine suppressed\nbody=%s", after)
	}
}

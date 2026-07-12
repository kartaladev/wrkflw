package view_test

import (
	"testing"

	"github.com/kartaladev/wrkflw/definition"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/gateway"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/runtime/view"
)

// TestNewActionableView verifies that NewActionableView produces a curated
// ActionableView containing only open tasks with their allowed next actions
// derived from the process definition's outgoing flows.
//
// Note: model.Validate rejects conditions on flows from non-gateway nodes, so
// the definition routes through an ExclusiveGateway to exercise WithCondition.
// The task's AllowedActions reflect the task's own outgoing flows (the unconditional
// flow to the gateway); the gateway's conditional flows are separate.
func TestNewActionableView(t *testing.T) {
	// approve → gw (unconditional); gw → e (conditional with FlowID "go-e").
	// The task's AllowedActions come from def.Outgoing("approve") = [{approve->gw}].
	def, err := definition.NewBuilder("d1", 1).
		Add(event.NewStart("s")).
		Add(activity.NewUserTask("approve", activity.WithEligibleRoles("manager"))).
		Add(gateway.NewExclusive("gw")).
		Add(event.NewEnd("e")).
		Connect("s", "approve").
		Connect("approve", "gw", flow.WithFlowID("approve-gw")).
		Connect("gw", "e", flow.WithFlowID("go-e"), flow.WithCondition("vars.ok")).
		Build()
	if err != nil {
		t.Fatalf("build definition: %v", err)
	}

	st := engine.InstanceState{
		InstanceID: "i1",
		Status:     engine.StatusRunning,
		Tasks: []humantask.HumanTask{
			{TaskToken: "tk", NodeID: "approve", State: humantask.Unclaimed},
		},
	}

	v := view.NewActionableView(st, def)

	if v.Status != "running" || len(v.OpenTasks) != 1 {
		t.Fatalf("view = %+v", v)
	}
	if v.InstanceID != "i1" {
		t.Fatalf("view.InstanceID = %q, want %q", v.InstanceID, "i1")
	}

	ot := v.OpenTasks[0]
	if ot.NodeID != "approve" || ot.State != "unclaimed" {
		t.Fatalf("open task = %+v", ot)
	}
	if ot.TaskToken != "tk" {
		t.Fatalf("open task.TaskToken = %q, want %q", ot.TaskToken, "tk")
	}

	// def.Outgoing("approve") yields the single flow "approve-gw" → gateway.
	if len(ot.AllowedActions) != 1 || ot.AllowedActions[0].FlowID != "approve-gw" {
		t.Fatalf("allowed actions = %+v", ot.AllowedActions)
	}
	if ot.AllowedActions[0].Target != "gw" {
		t.Fatalf("allowed action target = %q, want %q", ot.AllowedActions[0].Target, "gw")
	}
}

// TestNewActionableView_NilDef verifies that when def is nil, AllowedActions is
// empty (not nil-panicking), documenting the nil-def contract.
func TestNewActionableView_NilDef(t *testing.T) {
	st := engine.InstanceState{
		InstanceID: "i2",
		Status:     engine.StatusRunning,
		Tasks: []humantask.HumanTask{
			{TaskToken: "tk2", NodeID: "approve", State: humantask.Unclaimed},
		},
	}
	v := view.NewActionableView(st, nil)
	if len(v.OpenTasks) != 1 {
		t.Fatalf("expected 1 open task, got %d", len(v.OpenTasks))
	}
	if v.OpenTasks[0].AllowedActions != nil {
		t.Fatalf("AllowedActions should be nil when def is nil, got %v", v.OpenTasks[0].AllowedActions)
	}
}

// TestNewActionableView_ClosedTasksExcluded verifies that completed and cancelled
// tasks are NOT included in OpenTasks.
func TestNewActionableView_ClosedTasksExcluded(t *testing.T) {
	st := engine.InstanceState{
		InstanceID: "i3",
		Status:     engine.StatusRunning,
		Tasks: []humantask.HumanTask{
			{TaskToken: "tk-open", NodeID: "approve", State: humantask.Claimed},
			{TaskToken: "tk-done", NodeID: "review", State: humantask.Completed},
			{TaskToken: "tk-cancel", NodeID: "sign", State: humantask.Cancelled},
		},
	}
	v := view.NewActionableView(st, nil)
	if len(v.OpenTasks) != 1 {
		t.Fatalf("expected 1 open task, got %d: %+v", len(v.OpenTasks), v.OpenTasks)
	}
	if v.OpenTasks[0].TaskToken != "tk-open" {
		t.Fatalf("unexpected open task: %+v", v.OpenTasks[0])
	}
}

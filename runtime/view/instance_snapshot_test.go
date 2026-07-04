package view_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime/view"
)

// TestStatusString verifies that StatusString maps every engine.Status value to
// its canonical string representation.
func TestStatusString(t *testing.T) {
	cases := map[engine.Status]string{
		engine.StatusRunning:      "running",
		engine.StatusCompleted:    "completed",
		engine.StatusFailed:       "failed",
		engine.StatusCompensating: "compensating",
		engine.StatusTerminated:   "terminated",
	}
	for in, want := range cases {
		if got := view.StatusString(in); got != want {
			t.Errorf("StatusString(%v) = %q, want %q", in, got, want)
		}
	}
}

// TestStatusString_Unknown verifies that an out-of-range Status maps to "unknown".
func TestStatusString_Unknown(t *testing.T) {
	if got := view.StatusString(engine.Status(99)); got != "unknown" {
		t.Errorf("StatusString(99) = %q, want %q", got, "unknown")
	}
}

// TestNewInstanceSnapshot verifies that NewInstanceSnapshot maps an
// engine.InstanceState to an InstanceSnapshot DTO correctly, and that the
// serialized JSON contains no engine bookkeeping keys.
func TestNewInstanceSnapshot(t *testing.T) {
	end := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	st := engine.InstanceState{
		InstanceID: "i1",
		DefID:      "d1",
		DefVersion: 2,
		Status:     engine.StatusCompleted,
		Variables:  map[string]any{"amount": 10},
		Tokens:     []engine.Token{{ID: "t1", NodeID: "n1", State: engine.TokenActive}},
		History:    []engine.NodeVisit{{NodeID: "n1", TokenID: "t1"}},
		EndedAt:    &end,
		Tasks: []humantask.HumanTask{{
			TaskToken: "tk1",
			NodeID:    "task-node",
			State:     humantask.Unclaimed,
		}},
		Incidents: []engine.Incident{{
			ID:        "inc1",
			TokenID:   "t1",
			NodeID:    "n1",
			Error:     "something went wrong",
			CreatedAt: time.Date(2026, 6, 23, 11, 0, 0, 0, time.UTC),
		}},
	}
	snap := view.NewInstanceSnapshot(st, nil)

	if snap.InstanceID != "i1" || snap.Status != "completed" || snap.DefVersion != 2 {
		t.Fatalf("snap = %+v", snap)
	}
	if snap.DefID != "d1" {
		t.Fatalf("snap.DefID = %q, want %q", snap.DefID, "d1")
	}
	if snap.EndedAt == nil || !snap.EndedAt.Equal(end) {
		t.Fatalf("snap.EndedAt = %v, want %v", snap.EndedAt, end)
	}
	if len(snap.Variables) != 1 {
		t.Fatalf("snap.Variables = %v", snap.Variables)
	}

	if len(snap.Tokens) != 1 || snap.Tokens[0].State != "active" {
		t.Fatalf("tokens = %+v", snap.Tokens)
	}
	if snap.Tokens[0].NodeID != "n1" || snap.Tokens[0].ID != "t1" {
		t.Fatalf("token fields = %+v", snap.Tokens[0])
	}

	if len(snap.History) != 1 || snap.History[0].NodeID != "n1" {
		t.Fatalf("history = %+v", snap.History)
	}

	if len(snap.Tasks) != 1 || snap.Tasks[0].State != "unclaimed" {
		t.Fatalf("tasks = %+v", snap.Tasks)
	}
	if snap.Tasks[0].TaskToken != "tk1" || snap.Tasks[0].NodeID != "task-node" {
		t.Fatalf("task fields = %+v", snap.Tasks[0])
	}

	if len(snap.Incidents) != 1 || snap.Incidents[0].ID != "inc1" {
		t.Fatalf("incidents = %+v", snap.Incidents)
	}

	// no-leak guard: serialized JSON must not contain engine bookkeeping keys.
	b, _ := json.Marshal(snap)
	for _, banned := range []string{"armed", "boundaries", "scopes", "compensat", "Seq", "pendingCancel"} {
		if bytes.Contains(bytes.ToLower(b), bytes.ToLower([]byte(banned))) {
			t.Errorf("snapshot JSON leaks bookkeeping key %q: %s", banned, b)
		}
	}
}

// noop returns a trivial inline ServiceAction for use in test definitions.
func noop() action.ServiceAction {
	return action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
		return nil, nil
	})
}

// TestNewInstanceSnapshotActionMetadata asserts that NewInstanceSnapshot populates
// ScopedActions and ActionBindings from the supplied definition, and that passing
// nil leaves both fields empty.
func TestNewInstanceSnapshotActionMetadata(t *testing.T) {
	st := engine.InstanceState{
		InstanceID: "i-meta",
		DefID:      "meta-proc",
		DefVersion: 1,
		Status:     engine.StatusRunning,
	}

	cases := []struct {
		name   string
		def    func() *model.ProcessDefinition
		assert func(t *testing.T, snap view.InstanceSnapshot)
	}{
		{
			name: "nil_def_leaves_fields_empty",
			def:  func() *model.ProcessDefinition { return nil },
			assert: func(t *testing.T, snap view.InstanceSnapshot) {
				t.Helper()
				if snap.ScopedActions != nil {
					t.Errorf("ScopedActions = %v, want nil", snap.ScopedActions)
				}
				if snap.ActionBindings != nil {
					t.Errorf("ActionBindings = %v, want nil", snap.ActionBindings)
				}
			},
		},
		{
			name: "service_task_with_action_name",
			def: func() *model.ProcessDefinition {
				def, err := definition.NewBuilder("meta-proc", 1).
					Add(event.NewStart("start")).
					Add(activity.NewServiceTask("svc-named", activity.WithActionName("my-action"))).
					Add(event.NewEnd("end")).
					Connect("start", "svc-named").
					Connect("svc-named", "end").
					RegisterAction("my-action", noop()).
					Build()
				if err != nil {
					t.Fatalf("Build: %v", err)
				}
				return def
			},
			assert: func(t *testing.T, snap view.InstanceSnapshot) {
				t.Helper()
				// Scoped action name exposed.
				if len(snap.ScopedActions) != 1 || snap.ScopedActions[0] != "my-action" {
					t.Errorf("ScopedActions = %v, want [my-action]", snap.ScopedActions)
				}
				// One binding for the service task.
				if len(snap.ActionBindings) != 1 {
					t.Fatalf("ActionBindings = %v, want 1 entry", snap.ActionBindings)
				}
				b := snap.ActionBindings[0]
				if b.NodeID != "svc-named" {
					t.Errorf("NodeID = %q, want svc-named", b.NodeID)
				}
				if b.NodeKind != "serviceTask" {
					t.Errorf("NodeKind = %q, want serviceTask", b.NodeKind)
				}
				if b.Action != "my-action" {
					t.Errorf("Action = %q, want my-action", b.Action)
				}
				if b.Inline {
					t.Errorf("Inline = true, want false for named action")
				}
			},
		},
		{
			name: "service_task_inline_action",
			def: func() *model.ProcessDefinition {
				def, err := definition.NewBuilder("meta-proc", 1).
					Add(event.NewStart("start")).
					Add(activity.NewServiceTask("svc-inline", activity.WithAction(noop()))).
					Add(event.NewEnd("end")).
					Connect("start", "svc-inline").
					Connect("svc-inline", "end").
					Build()
				if err != nil {
					t.Fatalf("Build: %v", err)
				}
				return def
			},
			assert: func(t *testing.T, snap view.InstanceSnapshot) {
				t.Helper()
				if snap.ScopedActions != nil {
					t.Errorf("ScopedActions = %v, want nil", snap.ScopedActions)
				}
				if len(snap.ActionBindings) != 1 {
					t.Fatalf("ActionBindings = %v, want 1 entry", snap.ActionBindings)
				}
				b := snap.ActionBindings[0]
				if b.NodeID != "svc-inline" {
					t.Errorf("NodeID = %q, want svc-inline", b.NodeID)
				}
				if b.NodeKind != "serviceTask" {
					t.Errorf("NodeKind = %q, want serviceTask", b.NodeKind)
				}
				if b.Action != "" {
					t.Errorf("Action = %q, want empty (default-by-id for inline)", b.Action)
				}
				if !b.Inline {
					t.Errorf("Inline = false, want true for inline action")
				}
			},
		},
		{
			name: "service_task_default_by_id",
			def: func() *model.ProcessDefinition {
				def, err := definition.NewBuilder("meta-proc", 1).
					Add(event.NewStart("start")).
					Add(activity.NewServiceTask("svc-default")).
					Add(event.NewEnd("end")).
					Connect("start", "svc-default").
					Connect("svc-default", "end").
					Build()
				if err != nil {
					t.Fatalf("Build: %v", err)
				}
				return def
			},
			assert: func(t *testing.T, snap view.InstanceSnapshot) {
				t.Helper()
				if len(snap.ActionBindings) != 1 {
					t.Fatalf("ActionBindings = %v, want 1 entry", snap.ActionBindings)
				}
				b := snap.ActionBindings[0]
				if b.NodeID != "svc-default" {
					t.Errorf("NodeID = %q, want svc-default", b.NodeID)
				}
				if b.NodeKind != "serviceTask" {
					t.Errorf("NodeKind = %q, want serviceTask", b.NodeKind)
				}
				// Default-by-id: Action is empty, not substituted with the node ID.
				if b.Action != "" {
					t.Errorf("Action = %q, want empty for default-by-id", b.Action)
				}
				if b.Inline {
					t.Errorf("Inline = true, want false for default-by-id")
				}
			},
		},
		{
			name: "business_rule_task",
			def: func() *model.ProcessDefinition {
				def, err := definition.NewBuilder("meta-proc", 1).
					Add(event.NewStart("start")).
					Add(activity.NewBusinessRuleTask("brt-node", activity.WithActionName("rule-action"))).
					Add(event.NewEnd("end")).
					Connect("start", "brt-node").
					Connect("brt-node", "end").
					Build()
				if err != nil {
					t.Fatalf("Build: %v", err)
				}
				return def
			},
			assert: func(t *testing.T, snap view.InstanceSnapshot) {
				t.Helper()
				if len(snap.ActionBindings) != 1 {
					t.Fatalf("ActionBindings = %v, want 1 entry", snap.ActionBindings)
				}
				b := snap.ActionBindings[0]
				if b.NodeID != "brt-node" {
					t.Errorf("NodeID = %q, want brt-node", b.NodeID)
				}
				if b.NodeKind != "businessRuleTask" {
					t.Errorf("NodeKind = %q, want businessRuleTask", b.NodeKind)
				}
				if b.Action != "rule-action" {
					t.Errorf("Action = %q, want rule-action", b.Action)
				}
				if b.Inline {
					t.Errorf("Inline = true, want false")
				}
			},
		},
		{
			name: "mixed_nodes_sorted_by_node_id",
			def: func() *model.ProcessDefinition {
				def, err := definition.NewBuilder("meta-proc", 1).
					Add(event.NewStart("start")).
					Add(activity.NewServiceTask("z-svc", activity.WithActionName("svc-x"))).
					Add(activity.NewBusinessRuleTask("a-brt")).
					Add(event.NewEnd("end")).
					Connect("start", "z-svc").
					Connect("z-svc", "a-brt").
					Connect("a-brt", "end").
					RegisterAction("svc-x", noop()).
					Build()
				if err != nil {
					t.Fatalf("Build: %v", err)
				}
				return def
			},
			assert: func(t *testing.T, snap view.InstanceSnapshot) {
				t.Helper()
				if len(snap.ScopedActions) != 1 || snap.ScopedActions[0] != "svc-x" {
					t.Errorf("ScopedActions = %v, want [svc-x]", snap.ScopedActions)
				}
				if len(snap.ActionBindings) != 2 {
					t.Fatalf("ActionBindings = %v, want 2 entries", snap.ActionBindings)
				}
				// Sorted by NodeID: a-brt < z-svc.
				if snap.ActionBindings[0].NodeID != "a-brt" {
					t.Errorf("ActionBindings[0].NodeID = %q, want a-brt", snap.ActionBindings[0].NodeID)
				}
				if snap.ActionBindings[1].NodeID != "z-svc" {
					t.Errorf("ActionBindings[1].NodeID = %q, want z-svc", snap.ActionBindings[1].NodeID)
				}
				if snap.ActionBindings[0].NodeKind != "businessRuleTask" {
					t.Errorf("ActionBindings[0].NodeKind = %q, want businessRuleTask", snap.ActionBindings[0].NodeKind)
				}
				if snap.ActionBindings[1].NodeKind != "serviceTask" {
					t.Errorf("ActionBindings[1].NodeKind = %q, want serviceTask", snap.ActionBindings[1].NodeKind)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snap := view.NewInstanceSnapshot(st, tc.def())
			tc.assert(t, snap)
		})
	}
}

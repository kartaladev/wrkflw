package view

import (
	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
)

// NextAction describes a single outgoing sequence flow from a task node —
// i.e. one possible "next step" the process can take after the task is completed.
type NextAction struct {
	// FlowID is the sequence flow identifier.
	FlowID string `json:"flow_id"`
	// Target is the BPMN node ID the flow leads to.
	Target string `json:"target"`
	// Condition is the routing expression guarding this flow (empty = unconditional).
	Condition string `json:"condition,omitempty"`
	// IsDefault marks this flow as the exclusive-gateway default.
	IsDefault bool `json:"is_default,omitempty"`
}

// ActionableTask is the curated view of a single open human task together with
// the allowed next actions derived from the process definition.
type ActionableTask struct {
	// TaskID is the unique task instance identifier.
	TaskID string `json:"task_id"`
	// NodeID is the BPMN node that generated this task.
	NodeID string `json:"node_id"`
	// State is the string representation of the task's lifecycle state.
	State string `json:"state"`
	// Claim records who claimed the task and when; nil when unclaimed.
	Claim *humantask.Claim `json:"claim,omitempty"`
	// Candidates holds the resolved actors eligible to act on this task, rendered
	// verbatim as {id, roles, attributes}.
	Candidates []authz.Actor `json:"candidates,omitempty"`
	// AllowedActions lists the outgoing sequence flows from this task's node,
	// derived from the process definition. When def is nil, this is nil (no
	// routing information is available).
	AllowedActions []NextAction `json:"allowed_actions,omitempty"`
}

// ActionableView is the curated, JSON-serializable projection of a process
// instance focused on actionability: it exposes only the open human tasks and,
// for each task, the allowed next actions derived from the process definition.
//
// Use NewActionableView to construct one from an engine.InstanceState.
type ActionableView struct {
	// InstanceID is the unique process instance identifier.
	InstanceID string `json:"instance_id"`
	// Status is the string representation of the instance lifecycle state.
	Status string `json:"status"`
	// OpenTasks lists the tasks that are currently open (Unclaimed or Claimed).
	OpenTasks []ActionableTask `json:"open_tasks,omitempty"`
}

// NewActionableView maps an engine.InstanceState and a process definition to an
// ActionableView DTO. Only open tasks (IsOpen() == true) are included.
//
// If def is nil, AllowedActions on each ActionableTask is nil — no routing
// information can be derived without the definition.
func NewActionableView(st engine.InstanceState, def *model.ProcessDefinition) ActionableView {
	openTasks := make([]ActionableTask, 0, len(st.Tasks))
	for _, t := range st.Tasks {
		if !t.IsOpen() {
			continue
		}

		var allowedActions []NextAction
		if def != nil {
			flows := def.Outgoing(t.NodeID)
			if len(flows) > 0 {
				allowedActions = make([]NextAction, 0, len(flows))
				for _, f := range flows {
					allowedActions = append(allowedActions, NextAction{
						FlowID:    f.ID,
						Target:    f.Target,
						Condition: f.Condition,
						IsDefault: f.IsDefault,
					})
				}
			}
		}

		// Clone before exposing: Claim is a pointer and Candidates a slice, both
		// reachable into the caller's live InstanceState. A consumer mutating the
		// returned view must not reach the engine's audit record.
		audit := t.Clone()
		openTasks = append(openTasks, ActionableTask{
			TaskID:         audit.TaskID,
			NodeID:         audit.NodeID,
			State:          audit.State.String(),
			Claim:          audit.Claim,
			Candidates:     audit.Candidates,
			AllowedActions: allowedActions,
		})
	}

	return ActionableView{
		InstanceID: st.InstanceID,
		Status:     StatusString(st.Status),
		OpenTasks:  openTasks,
	}
}

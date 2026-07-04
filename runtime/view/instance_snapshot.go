package view

import (
	"sort"
	"time"

	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
)

// StatusString converts an engine.Status to its canonical string representation.
// The mapping mirrors the private statusString in transport/rest.
// Out-of-range values map to "unknown".
func StatusString(s engine.Status) string {
	switch s {
	case engine.StatusRunning:
		return "running"
	case engine.StatusCompleted:
		return "completed"
	case engine.StatusFailed:
		return "failed"
	case engine.StatusCompensating:
		return "compensating"
	case engine.StatusTerminated:
		return "terminated"
	default:
		return "unknown"
	}
}

// tokenStateString converts an engine.TokenState to its canonical string representation.
// Out-of-range values map to "unknown".
func tokenStateString(s engine.TokenState) string {
	switch s {
	case engine.TokenActive:
		return "active"
	case engine.TokenWaitingCommand:
		return "waitingCommand"
	case engine.TokenAtJoin:
		return "atJoin"
	case engine.TokenIncident:
		return "incident"
	default:
		return "unknown"
	}
}

// TokenView is the JSON-serializable projection of an engine.Token.
// Only consumer-relevant fields are exposed; engine bookkeeping is excluded.
type TokenView struct {
	// ID is the unique token identifier.
	ID string `json:"id"`
	// NodeID is the BPMN node where this token is currently parked.
	NodeID string `json:"node_id"`
	// ScopeID is the execution scope of this token (empty = root scope).
	ScopeID string `json:"scope_id,omitempty"`
	// State is the string representation of the token's execution state.
	State string `json:"state"`
	// Payload holds data carried by this token.
	Payload map[string]any `json:"payload,omitempty"`
	// EnteredAt is the time the token entered the current node.
	EnteredAt time.Time `json:"entered_at"`
	// RetryAttempts is the number of execution attempts already made for the
	// token's current node.
	RetryAttempts int `json:"retry_attempts,omitempty"`
}

// NodeVisitView is the JSON-serializable projection of an engine.NodeVisit.
type NodeVisitView struct {
	// NodeID is the BPMN node that was visited.
	NodeID string `json:"node_id"`
	// TokenID is the token that visited this node.
	TokenID string `json:"token_id"`
	// EnteredAt is the time the node was entered.
	EnteredAt time.Time `json:"entered_at"`
	// LeftAt is the time the node was exited, or nil if the token is still parked.
	LeftAt *time.Time `json:"left_at,omitempty"`
	// ActorID is the actor who completed a human-task visit, or nil if not applicable.
	ActorID *string `json:"actor_id,omitempty"`
}

// IncidentView is the JSON-serializable projection of an engine.Incident.
type IncidentView struct {
	// ID is the unique incident identifier.
	ID string `json:"id"`
	// TokenID is the ID of the token that encountered the error.
	TokenID string `json:"token_id"`
	// NodeID is the BPMN node where the failure occurred.
	NodeID string `json:"node_id"`
	// ScopeID is the execution scope of the failed token (empty = root scope).
	ScopeID string `json:"scope_id,omitempty"`
	// Error is the error message or error code reported by the failing action.
	Error string `json:"error"`
	// Attempts is the total number of execution attempts made before the incident.
	Attempts int `json:"attempts"`
	// CreatedAt is the time the incident was created.
	CreatedAt time.Time `json:"created_at"`
}

// TaskView is the JSON-serializable projection of a humantask.HumanTask.
// Only consumer-relevant fields are exposed.
type TaskView struct {
	// TaskToken is the unique task instance identifier.
	TaskToken string `json:"task_token"`
	// NodeID is the BPMN node that generated this task.
	NodeID string `json:"node_id"`
	// State is the string representation of the task's lifecycle state.
	State string `json:"state"`
	// ClaimedBy is the actor ID that claimed the task; empty when unclaimed.
	ClaimedBy string `json:"claimed_by,omitempty"`
	// Candidates holds the resolved actor IDs eligible to act on this task.
	Candidates []string `json:"candidates,omitempty"`
	// CreatedAt is the time the task was created.
	CreatedAt time.Time `json:"created_at"`
	// DueAt is the optional deadline for this task.
	DueAt *time.Time `json:"due_at,omitempty"`
}

// ActionBindingView describes how a single service-action-bearing node (ServiceTask
// or BusinessRuleTask) is wired to its action within a process definition.
type ActionBindingView struct {
	// NodeID is the process node identifier.
	NodeID string `json:"node_id"`
	// NodeKind is the BPMN node kind: "serviceTask" or "businessRuleTask".
	NodeKind string `json:"node_kind"`
	// Action is the explicit catalog-action name set on the node. Empty means
	// the node uses the default-by-id resolution (action name == node ID).
	Action string `json:"action,omitempty"`
	// Inline is true when the node carries a node-local inline action.Action
	// (attached via WithAction/WithActionFunc). Inline actions are never serialized.
	Inline bool `json:"inline"`
}

// InstanceSnapshot is the full, stable, JSON-serializable snapshot of a process
// instance. It includes all consumer-relevant fields and deliberately excludes
// engine bookkeeping (Timers, ArmedEvents, Boundaries, Scopes, RootCompensations,
// ArchivedCompensations, EventSubprocesses, Compensating, PendingCancel,
// DeferredCompensationThrows, and the *Seq counters).
//
// Use NewInstanceSnapshot to construct one from an engine.InstanceState.
type InstanceSnapshot struct {
	// InstanceID is the unique process instance identifier.
	InstanceID string `json:"instance_id"`
	// DefID is the process-definition ID.
	DefID string `json:"def_id"`
	// DefVersion is the process-definition version.
	DefVersion int `json:"def_version"`
	// Status is the string representation of the instance lifecycle state.
	Status string `json:"status"`
	// Variables holds the current process variables.
	Variables map[string]any `json:"variables,omitempty"`
	// Tokens holds the current token positions.
	Tokens []TokenView `json:"tokens,omitempty"`
	// History is the ordered audit trail of node visits.
	History []NodeVisitView `json:"history,omitempty"`
	// Tasks holds the in-flight human-task records.
	Tasks []TaskView `json:"tasks,omitempty"`
	// Incidents holds the open incident records.
	Incidents []IncidentView `json:"incidents,omitempty"`
	// StartedAt is the time the instance was created.
	StartedAt time.Time `json:"started_at"`
	// EndedAt is the time the instance reached a terminal state, or nil if
	// the instance is still running.
	EndedAt *time.Time `json:"ended_at,omitempty"`
	// ScopedActions holds the sorted names registered in the definition-scoped
	// action catalog. Nil when no scoped actions are registered or when no
	// definition is available.
	ScopedActions []string `json:"scoped_actions,omitempty"`
	// ActionBindings lists the action wiring for each ServiceTask and
	// BusinessRuleTask in the definition, sorted by NodeID. Nil when no
	// definition is available or the definition has no such nodes.
	ActionBindings []ActionBindingView `json:"action_bindings,omitempty"`
}

// NewInstanceSnapshot maps an engine.InstanceState to an InstanceSnapshot DTO.
// When def is non-nil, ScopedActions and ActionBindings are populated from the
// definition's scoped catalog and service-action node wiring. Pass nil when the
// definition is unavailable; both fields will be omitted.
func NewInstanceSnapshot(st engine.InstanceState, def *model.ProcessDefinition) InstanceSnapshot {
	tokens := make([]TokenView, 0, len(st.Tokens))
	for _, t := range st.Tokens {
		tokens = append(tokens, TokenView{
			ID:            t.ID,
			NodeID:        t.NodeID,
			ScopeID:       t.ScopeID,
			State:         tokenStateString(t.State),
			Payload:       t.Payload,
			EnteredAt:     t.EnteredAt,
			RetryAttempts: t.RetryAttempts,
		})
	}

	history := make([]NodeVisitView, 0, len(st.History))
	for _, v := range st.History {
		history = append(history, NodeVisitView{
			NodeID:    v.NodeID,
			TokenID:   v.TokenID,
			EnteredAt: v.EnteredAt,
			LeftAt:    v.LeftAt,
			ActorID:   v.ActorID,
		})
	}

	tasks := make([]TaskView, 0, len(st.Tasks))
	for _, t := range st.Tasks {
		tasks = append(tasks, TaskView{
			TaskToken:  t.TaskToken,
			NodeID:     t.NodeID,
			State:      t.State.String(),
			ClaimedBy:  t.ClaimedBy,
			Candidates: t.Candidates,
			CreatedAt:  t.CreatedAt,
			DueAt:      t.DueAt,
		})
	}

	incidents := make([]IncidentView, 0, len(st.Incidents))
	for _, i := range st.Incidents {
		incidents = append(incidents, IncidentView{
			ID:        i.ID,
			TokenID:   i.TokenID,
			NodeID:    i.NodeID,
			ScopeID:   i.ScopeID,
			Error:     i.Error,
			Attempts:  i.Attempts,
			CreatedAt: i.CreatedAt,
		})
	}

	snap := InstanceSnapshot{
		InstanceID: st.InstanceID,
		DefID:      st.DefID,
		DefVersion: st.DefVersion,
		Status:     StatusString(st.Status),
		Variables:  st.Variables,
		Tokens:     tokens,
		History:    history,
		Tasks:      tasks,
		Incidents:  incidents,
		StartedAt:  st.StartedAt,
		EndedAt:    st.EndedAt,
	}

	if def != nil {
		snap.ScopedActions = def.ScopedActionNames()
		var bindings []ActionBindingView
		for _, n := range def.Nodes {
			switch n.Kind() {
			case model.KindServiceTask:
				bindings = append(bindings, ActionBindingView{
					NodeID:   n.ID(),
					NodeKind: "serviceTask",
					Action:   model.ActionOf(n),
					Inline:   model.InlineActionOf(n) != nil,
				})
			case model.KindBusinessRuleTask:
				bindings = append(bindings, ActionBindingView{
					NodeID:   n.ID(),
					NodeKind: "businessRuleTask",
					Action:   model.ActionOf(n),
					Inline:   model.InlineActionOf(n) != nil,
				})
			}
		}
		if len(bindings) > 0 {
			sort.Slice(bindings, func(i, j int) bool {
				return bindings[i].NodeID < bindings[j].NodeID
			})
			snap.ActionBindings = bindings
		}
	}

	return snap
}

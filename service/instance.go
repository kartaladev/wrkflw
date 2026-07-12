package service

import (
	"encoding/json"
	"sort"
	"time"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
)

// ProcessInstance is the read-only, fused view of a running instance: its
// definition and state. It serializes directly to a stable, frontend-ready JSON
// document via MarshalJSON; the serialized shape is an internal detail (no
// exported DTO fields), so a consumer can embed it in its own domain/DTO type
// and marshal with no transformation.
type ProcessInstance interface {
	Definition() *model.ProcessDefinition // raw template (nil if unresolved)
	State() engine.InstanceState          // raw running state
	json.Marshaler                        // MarshalJSON() ([]byte, error)
}

// NewProcessInstance fuses a definition (may be nil) and instance state into a
// ProcessInstance. Exported so consumers and tests can fabricate one.
func NewProcessInstance(def *model.ProcessDefinition, st engine.InstanceState) ProcessInstance {
	return processInstance{def: def, st: st}
}

type processInstance struct {
	def *model.ProcessDefinition
	st  engine.InstanceState
}

func (p processInstance) Definition() *model.ProcessDefinition { return p.def }
func (p processInstance) State() engine.InstanceState          { return p.st }

func (p processInstance) MarshalJSON() ([]byte, error) {
	return json.Marshal(newInstanceJSON(p.def, p.st))
}

// instanceJSON is the UNEXPORTED serialized projection. Field names/tags match
// the retired runtime/view.InstanceSnapshot for wire compatibility.
type instanceJSON struct {
	InstanceID     string              `json:"instance_id"`
	DefID          string              `json:"def_id"`
	DefVersion     int                 `json:"def_version"`
	Status         string              `json:"status"`
	Variables      map[string]any      `json:"variables,omitempty"`
	Tokens         []tokenJSON         `json:"tokens,omitempty"`
	History        []nodeVisitJSON     `json:"history,omitempty"`
	Tasks          []taskJSON          `json:"tasks,omitempty"`
	Incidents      []incidentJSON      `json:"incidents,omitempty"`
	StartedAt      time.Time           `json:"started_at"`
	EndedAt        *time.Time          `json:"ended_at,omitempty"`
	ScopedActions  []string            `json:"scoped_actions,omitempty"`
	ActionBindings []actionBindingJSON `json:"action_bindings,omitempty"`
}

type tokenJSON struct {
	ID            string         `json:"id"`
	NodeID        string         `json:"node_id"`
	ScopeID       string         `json:"scope_id,omitempty"`
	State         string         `json:"state"`
	Payload       map[string]any `json:"payload,omitempty"`
	EnteredAt     time.Time      `json:"entered_at"`
	RetryAttempts int            `json:"retry_attempts,omitempty"`
}

type nodeVisitJSON struct {
	NodeID    string     `json:"node_id"`
	TokenID   string     `json:"token_id"`
	EnteredAt time.Time  `json:"entered_at"`
	LeftAt    *time.Time `json:"left_at,omitempty"`
	ActorID   *string    `json:"actor_id,omitempty"`
}

type incidentJSON struct {
	ID        string    `json:"id"`
	TokenID   string    `json:"token_id"`
	NodeID    string    `json:"node_id"`
	ScopeID   string    `json:"scope_id,omitempty"`
	Error     string    `json:"error"`
	Attempts  int       `json:"attempts"`
	CreatedAt time.Time `json:"created_at"`
}

type taskJSON struct {
	TaskToken  string     `json:"task_token"`
	NodeID     string     `json:"node_id"`
	State      string     `json:"state"`
	ClaimedBy  string     `json:"claimed_by,omitempty"`
	Candidates []string   `json:"candidates,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	DueAt      *time.Time `json:"due_at,omitempty"`
}

type actionBindingJSON struct {
	NodeID   string `json:"node_id"`
	NodeKind string `json:"node_kind"`
	Action   string `json:"action,omitempty"`
}

// tokenStateString converts an engine.TokenState to its canonical string
// representation. Out-of-range values map to "unknown". Copied verbatim from
// runtime/view to keep the unexported mapping co-located with its consumer.
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

func newInstanceJSON(def *model.ProcessDefinition, st engine.InstanceState) instanceJSON {
	tokens := make([]tokenJSON, 0, len(st.Tokens))
	for _, t := range st.Tokens {
		tokens = append(tokens, tokenJSON{
			ID:            t.ID,
			NodeID:        t.NodeID,
			ScopeID:       t.ScopeID,
			State:         tokenStateString(t.State),
			Payload:       t.Payload,
			EnteredAt:     t.EnteredAt,
			RetryAttempts: t.RetryAttempts,
		})
	}

	history := make([]nodeVisitJSON, 0, len(st.History))
	for _, v := range st.History {
		history = append(history, nodeVisitJSON{
			NodeID:    v.NodeID,
			TokenID:   v.TokenID,
			EnteredAt: v.EnteredAt,
			LeftAt:    v.LeftAt,
			ActorID:   v.ActorID,
		})
	}

	tasks := make([]taskJSON, 0, len(st.Tasks))
	for _, t := range st.Tasks {
		tasks = append(tasks, taskJSON{
			TaskToken:  t.TaskToken,
			NodeID:     t.NodeID,
			State:      t.State.String(),
			ClaimedBy:  t.ClaimedBy,
			Candidates: t.Candidates,
			CreatedAt:  t.CreatedAt,
			DueAt:      t.DueAt,
		})
	}

	incidents := make([]incidentJSON, 0, len(st.Incidents))
	for _, i := range st.Incidents {
		incidents = append(incidents, incidentJSON{
			ID:        i.ID,
			TokenID:   i.TokenID,
			NodeID:    i.NodeID,
			ScopeID:   i.ScopeID,
			Error:     i.Error,
			Attempts:  i.Attempts,
			CreatedAt: i.CreatedAt,
		})
	}

	out := instanceJSON{
		InstanceID: st.InstanceID,
		DefID:      st.DefID,
		DefVersion: st.DefVersion,
		Status:     st.Status.String(),
		Variables:  st.Variables,
		Tokens:     tokens,
		History:    history,
		Tasks:      tasks,
		Incidents:  incidents,
		StartedAt:  st.StartedAt,
		EndedAt:    st.EndedAt,
	}

	if def != nil {
		out.ScopedActions = def.ScopedActionNames()
		var bindings []actionBindingJSON
		for _, n := range def.Nodes {
			switch n.Kind() {
			case model.KindServiceTask:
				bindings = append(bindings, actionBindingJSON{
					NodeID:   n.ID(),
					NodeKind: "serviceTask",
					Action:   model.ActionOf(n),
				})
			case model.KindBusinessRuleTask:
				bindings = append(bindings, actionBindingJSON{
					NodeID:   n.ID(),
					NodeKind: "businessRuleTask",
					Action:   model.ActionOf(n),
				})
			}
		}
		if len(bindings) > 0 {
			sort.Slice(bindings, func(i, j int) bool { return bindings[i].NodeID < bindings[j].NodeID })
			out.ActionBindings = bindings
		}
	}
	return out
}

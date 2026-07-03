// Package rest provides a stdlib net/http handler that exposes the workflow
// engine's Service facade over HTTP/JSON. Consumers mount it in their own
// server; this package never owns a listener.
package rest

import (
	"time"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// InstanceView is the stable JSON projection of an engine.InstanceState.
// Field names are the canonical REST body shape; do not change them without
// a version bump.
type InstanceView struct {
	InstanceID string         `json:"instance_id"`
	DefID      string         `json:"def_id"`
	DefVersion int            `json:"def_version"`
	Status     string         `json:"status"`
	StartedAt  time.Time      `json:"started_at"`
	EndedAt    *time.Time     `json:"ended_at,omitempty"`
	Variables  map[string]any `json:"variables,omitempty"`
}

// deadLetterView is the JSON projection of a runtime.DeadLetter for the DLQ admin API.
type deadLetterView struct {
	ID         int64     `json:"id"`
	InstanceID string    `json:"instance_id"`
	Topic      string    `json:"topic"`
	RetryCount int       `json:"retry_count"`
	LastError  string    `json:"last_error"`
	Category   string    `json:"category"`
	CreatedAt  time.Time `json:"created_at"`
}

// policyView is the JSON projection of a service.PolicyRule for the policy-admin API.
type policyView struct {
	Subject string `json:"subject"`
	Object  string `json:"object"`
	Action  string `json:"action"`
}

// roleBindingView is the JSON projection of a service.RoleBinding for the policy-admin API.
type roleBindingView struct {
	User string `json:"user"`
	Role string `json:"role"`
}

// policyListResponse is the JSON envelope returned by GET /admin/policies.
type policyListResponse struct {
	Policies []policyView `json:"policies"`
}

// policyMutateResponse is the JSON envelope returned by POST/DELETE /admin/policies.
type policyMutateResponse struct {
	Added   *bool `json:"added,omitempty"`
	Removed *bool `json:"removed,omitempty"`
}

// roleBindingListResponse is the JSON envelope returned by GET /admin/role-bindings.
type roleBindingListResponse struct {
	RoleBindings []roleBindingView `json:"role_bindings"`
}

// roleBindingMutateResponse is the JSON envelope returned by POST/DELETE /admin/role-bindings.
type roleBindingMutateResponse struct {
	Added   *bool `json:"added,omitempty"`
	Removed *bool `json:"removed,omitempty"`
}

// NewInstanceView converts an engine.InstanceState into the stable InstanceView DTO.
func NewInstanceView(st engine.InstanceState) InstanceView {
	return InstanceView{
		InstanceID: st.InstanceID,
		DefID:      st.DefID,
		DefVersion: st.DefVersion,
		Status:     statusString(st.Status),
		StartedAt:  st.StartedAt,
		EndedAt:    st.EndedAt,
		Variables:  st.Variables,
	}
}

// callLinkRefView is the snake_case REST projection of a kernel.CallLinkRef.
type callLinkRefView struct {
	InstanceID string `json:"instance_id"`
	DefID      string `json:"def_id"`
	DefVersion int    `json:"def_version"`
	Depth      int    `json:"depth"`
}

// chainLinkRefView is the snake_case REST projection of a kernel.ChainLinkRef.
type chainLinkRefView struct {
	InstanceID    string `json:"instance_id"`
	DefinitionRef string `json:"definition_ref"`
	Outcome       string `json:"outcome"`
}

// lineageView is the snake_case REST projection of a kernel.InstanceLineage.
// call_parent and chain_predecessor are omitted when nil; call_children and
// chain_successors are always serialized as arrays (never null).
type lineageView struct {
	InstanceID       string             `json:"instance_id"`
	CallParent       *callLinkRefView   `json:"call_parent,omitempty"`
	CallChildren     []callLinkRefView  `json:"call_children"`
	ChainPredecessor *chainLinkRefView  `json:"chain_predecessor,omitempty"`
	ChainSuccessors  []chainLinkRefView `json:"chain_successors"`
}

// newLineageView maps a kernel.InstanceLineage to the snake_case lineageView
// DTO. call_children and chain_successors are initialized to empty slices so
// they serialize as [] rather than null.
func newLineageView(l kernel.InstanceLineage) lineageView {
	v := lineageView{
		InstanceID:      l.InstanceID,
		CallChildren:    make([]callLinkRefView, len(l.CallChildren)),
		ChainSuccessors: make([]chainLinkRefView, len(l.ChainSuccessors)),
	}
	if l.CallParent != nil {
		r := callLinkRefView{
			InstanceID: l.CallParent.InstanceID,
			DefID:      l.CallParent.DefID,
			DefVersion: l.CallParent.DefVersion,
			Depth:      l.CallParent.Depth,
		}
		v.CallParent = &r
	}
	for i, c := range l.CallChildren {
		v.CallChildren[i] = callLinkRefView{
			InstanceID: c.InstanceID,
			DefID:      c.DefID,
			DefVersion: c.DefVersion,
			Depth:      c.Depth,
		}
	}
	if l.ChainPredecessor != nil {
		r := chainLinkRefView{
			InstanceID:    l.ChainPredecessor.InstanceID,
			DefinitionRef: l.ChainPredecessor.DefinitionRef,
			Outcome:       l.ChainPredecessor.Outcome,
		}
		v.ChainPredecessor = &r
	}
	for i, s := range l.ChainSuccessors {
		v.ChainSuccessors[i] = chainLinkRefView{
			InstanceID:    s.InstanceID,
			DefinitionRef: s.DefinitionRef,
			Outcome:       s.Outcome,
		}
	}
	return v
}

func statusString(s engine.Status) string {
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

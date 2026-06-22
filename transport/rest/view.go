// Package rest provides a stdlib net/http handler that exposes the workflow
// engine's Service facade over HTTP/JSON. Consumers mount it in their own
// server; this package never owns a listener.
package rest

import (
	"time"

	"github.com/zakyalvan/krtlwrkflw/engine"
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
	CreatedAt  time.Time `json:"created_at"`
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

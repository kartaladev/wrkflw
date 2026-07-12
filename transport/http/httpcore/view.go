package httpcore

import (
	"time"

	"github.com/kartaladev/wrkflw/engine"
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

// NewInstanceView converts an engine.InstanceState into the stable InstanceView DTO.
func NewInstanceView(st engine.InstanceState) InstanceView {
	return InstanceView{
		InstanceID: st.InstanceID,
		DefID:      st.DefID,
		DefVersion: st.DefVersion,
		Status:     st.Status.String(),
		StartedAt:  st.StartedAt,
		EndedAt:    st.EndedAt,
		Variables:  st.Variables,
	}
}

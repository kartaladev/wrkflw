package rest

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/service"
)

// instanceSummaryView is the admin-list JSON projection of a runtime.InstanceSummary.
// It intentionally omits large fields (tokens, history, tasks) to keep the payload small.
type instanceSummaryView struct {
	InstanceID    string     `json:"instance_id"`
	DefID         string     `json:"def_id"`
	DefVersion    int        `json:"def_version"`
	Status        string     `json:"status"`
	StartedAt     time.Time  `json:"started_at"`
	EndedAt       *time.Time `json:"ended_at,omitempty"`
	IncidentCount int        `json:"incident_count"`
}

// adminListResponse is the JSON envelope returned by GET /admin/instances.
type adminListResponse struct {
	Items      []instanceSummaryView `json:"items"`
	NextCursor string                `json:"next_cursor"`
	HasMore    bool                  `json:"has_more"`
}

// handleAdminListInstances handles GET /admin/instances.
//
// Query parameters:
//
//	status  (optional) — filter by lifecycle status; unknown values → 400.
//	limit   (optional) — page size; clamped by runtime.NormalizeLimit.
//	cursor  (optional) — opaque keyset cursor; malformed → 400.
func (h *handler) handleAdminListInstances(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	// Parse status filter.
	var statusFilter *engine.Status
	if raw := q.Get("status"); raw != "" {
		st, err := parseStatus(raw)
		if err != nil {
			WriteHTTPError(w, err)
			return
		}
		statusFilter = &st
	}

	// Parse limit (atoi; NormalizeLimit handles ≤0 and >200).
	var limit int
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			WriteHTTPError(w, fmt.Errorf("%w: invalid limit %q", ErrBadInput, raw))
			return
		}
		limit = n
	}

	filter := runtime.InstanceFilter{
		Status: statusFilter,
		Limit:  runtime.NormalizeLimit(limit),
		Cursor: q.Get("cursor"),
	}

	page, err := h.svc.ListInstances(r.Context(), filter)
	if err != nil {
		WriteHTTPError(w, err)
		return
	}

	resp := adminListResponse{
		Items:      make([]instanceSummaryView, len(page.Items)),
		NextCursor: page.NextCursor,
		HasMore:    page.HasMore,
	}
	for i, s := range page.Items {
		resp.Items[i] = instanceSummaryView{
			InstanceID:    s.InstanceID,
			DefID:         s.DefID,
			DefVersion:    s.DefVersion,
			Status:        statusString(s.Status),
			StartedAt:     s.StartedAt,
			EndedAt:       s.EndedAt,
			IncidentCount: s.IncidentCount,
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleResolveIncident handles POST /admin/instances/{id}/incidents/{incidentID}/resolve.
//
// It accepts an optional JSON body:
//
//	{"add_attempts": N}   — number of additional execution attempts to grant (default 1 when absent or ≤ 0)
//
// On success it responds with 200 and the resulting instance body rendered via
// the consumer-configured instance mapper. On error it delegates to WriteHTTPError.
func (h *handler) handleResolveIncident(w http.ResponseWriter, r *http.Request) {
	instanceID := r.PathValue("id")
	incidentID := r.PathValue("incidentID")

	type reqBody struct {
		AddAttempts int `json:"add_attempts"`
	}
	var body reqBody
	if !decodeBody(w, r, &body) {
		return
	}

	st, err := h.svc.ResolveIncident(r.Context(), service.ResolveIncidentRequest{
		InstanceID:  instanceID,
		IncidentID:  incidentID,
		AddAttempts: body.AddAttempts,
	})
	if err != nil {
		WriteHTTPError(w, err)
		return
	}
	h.renderInstance(w, http.StatusOK, st)
}

// parseStatus converts a status string (as emitted by statusString) back to an engine.Status.
// Returns a wrapped ErrBadInput for unknown values so classifyError maps it to 400.
func parseStatus(s string) (engine.Status, error) {
	switch s {
	case "running":
		return engine.StatusRunning, nil
	case "completed":
		return engine.StatusCompleted, nil
	case "failed":
		return engine.StatusFailed, nil
	case "compensating":
		return engine.StatusCompensating, nil
	case "terminated":
		return engine.StatusTerminated, nil
	default:
		return 0, fmt.Errorf("%w: unknown status %q", ErrBadInput, s)
	}
}

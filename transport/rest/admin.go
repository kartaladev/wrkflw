package rest

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/service"
)

// instanceSummaryView is the admin-list JSON projection of a kernel.InstanceSummary.
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
	TotalCount int                   `json:"total_count"`
}

// handleAdminListInstances handles GET /admin/instances.
//
// Query parameters:
//
//	status  (optional) — filter by lifecycle status; unknown values → 400.
//	limit   (optional) — page size; clamped by kernel.NormalizeLimit.
//	cursor  (optional) — opaque keyset cursor; malformed → 400.
//	total   (optional) — "true" or "1" to compute and include total_count (the
//	                     count of all status-matching instances) in the response.
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

	totalParam := q.Get("total")
	includeTotal := totalParam == "true" || totalParam == "1"

	filter := kernel.InstanceFilter{
		Status:       statusFilter,
		Limit:        kernel.NormalizeLimit(limit),
		Cursor:       q.Get("cursor"),
		IncludeTotal: includeTotal,
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
		TotalCount: page.TotalCount,
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

	h.writeJSON(w, r, http.StatusOK, resp)
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
	h.renderInstance(w, r, http.StatusOK, st)
}

// handleCancelInstance handles POST /admin/instances/{id}/cancel. It cancels the
// instance (running any definition-level cancel actions best-effort) and renders
// the resulting terminated instance. No request body.
func (h *handler) handleCancelInstance(w http.ResponseWriter, r *http.Request) {
	instanceID := r.PathValue("id")
	st, err := h.svc.CancelInstance(r.Context(), service.CancelInstanceRequest{InstanceID: instanceID})
	if err != nil {
		WriteHTTPError(w, err)
		return
	}
	h.renderInstance(w, r, http.StatusOK, st)
}

// dlqListResponse is the JSON envelope returned by GET /admin/dead-letters.
type dlqListResponse struct {
	Items []deadLetterView `json:"items"`
}

// dlqRedriveResponse is the JSON envelope returned by POST /admin/dead-letters/redrive.
type dlqRedriveResponse struct {
	Redriven int `json:"redriven"`
}

// handleListDeadLetters handles GET /admin/dead-letters.
//
// Query parameters:
//
//	limit (optional) — page size; clamped by kernel.NormalizeLimit (default 50, max 200).
//
// It is registered only when a DeadLetterAdmin is wired via WithDeadLetterAdmin,
// so h.cfg.deadLetters is guaranteed non-nil here.
func (h *handler) handleListDeadLetters(w http.ResponseWriter, r *http.Request) {
	var limit int
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			WriteHTTPError(w, fmt.Errorf("%w: invalid limit %q", ErrBadInput, raw))
			return
		}
		limit = n
	}

	rows, err := h.cfg.deadLetters.ListDeadLettered(r.Context(), kernel.NormalizeLimit(limit))
	if err != nil {
		WriteHTTPError(w, err)
		return
	}

	resp := dlqListResponse{Items: make([]deadLetterView, len(rows))}
	for i, dl := range rows {
		resp.Items[i] = deadLetterView{
			ID:         dl.ID,
			InstanceID: dl.InstanceID,
			Topic:      dl.Topic,
			RetryCount: dl.RetryCount,
			LastError:  dl.LastError,
			Category:   runtime.ClassifyDeadLetter(dl.LastError),
			CreatedAt:  dl.CreatedAt,
		}
	}
	h.writeJSON(w, r, http.StatusOK, resp)
}

// handleRedriveDeadLetters handles POST /admin/dead-letters/redrive.
//
// Body: {"ids":[int64,...]}. Empty or absent ids is a no-op (returns {"redriven":0}).
//
// It is registered only when a DeadLetterAdmin is wired via WithDeadLetterAdmin,
// so h.cfg.deadLetters is guaranteed non-nil here.
func (h *handler) handleRedriveDeadLetters(w http.ResponseWriter, r *http.Request) {
	type reqBody struct {
		IDs []int64 `json:"ids"`
	}
	var body reqBody
	if !decodeBody(w, r, &body) {
		return
	}

	n, err := h.cfg.deadLetters.Redrive(r.Context(), body.IDs...)
	if err != nil {
		WriteHTTPError(w, err)
		return
	}
	h.writeJSON(w, r, http.StatusOK, dlqRedriveResponse{Redriven: n})
}

// handleListPolicies handles GET /admin/policies.
//
// It is registered only when a PolicyAdmin is wired via WithPolicyAdmin.
func (h *handler) handleListPolicies(w http.ResponseWriter, r *http.Request) {
	rules, err := h.cfg.policyAdmin.ListPolicies(r.Context())
	if err != nil {
		WriteHTTPError(w, err)
		return
	}
	resp := policyListResponse{Policies: make([]policyView, len(rules))}
	for i, rule := range rules {
		resp.Policies[i] = policyView{Subject: rule.Subject, Object: rule.Object, Action: rule.Action}
	}
	h.writeJSON(w, r, http.StatusOK, resp)
}

// handleAddPolicy handles POST /admin/policies.
//
// Body: {"subject":..,"object":..,"action":..}. Returns {"added":bool}.
func (h *handler) handleAddPolicy(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Subject string `json:"subject"`
		Object  string `json:"object"`
		Action  string `json:"action"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	added, err := h.cfg.policyAdmin.AddPolicy(r.Context(), service.PolicyRule{
		Subject: body.Subject, Object: body.Object, Action: body.Action,
	})
	if err != nil {
		WriteHTTPError(w, err)
		return
	}
	v := added
	h.writeJSON(w, r, http.StatusOK, policyMutateResponse{Added: &v})
}

// handleRemovePolicy handles DELETE /admin/policies.
//
// Body: {"subject":..,"object":..,"action":..}. Returns {"removed":bool}.
func (h *handler) handleRemovePolicy(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Subject string `json:"subject"`
		Object  string `json:"object"`
		Action  string `json:"action"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	removed, err := h.cfg.policyAdmin.RemovePolicy(r.Context(), service.PolicyRule{
		Subject: body.Subject, Object: body.Object, Action: body.Action,
	})
	if err != nil {
		WriteHTTPError(w, err)
		return
	}
	v := removed
	h.writeJSON(w, r, http.StatusOK, policyMutateResponse{Removed: &v})
}

// handleListRoleBindings handles GET /admin/role-bindings.
//
// It is registered only when a PolicyAdmin is wired via WithPolicyAdmin.
func (h *handler) handleListRoleBindings(w http.ResponseWriter, r *http.Request) {
	bindings, err := h.cfg.policyAdmin.ListRoles(r.Context())
	if err != nil {
		WriteHTTPError(w, err)
		return
	}
	resp := roleBindingListResponse{RoleBindings: make([]roleBindingView, len(bindings))}
	for i, b := range bindings {
		resp.RoleBindings[i] = roleBindingView{User: b.User, Role: b.Role}
	}
	h.writeJSON(w, r, http.StatusOK, resp)
}

// handleAddRoleBinding handles POST /admin/role-bindings.
//
// Body: {"user":..,"role":..}. Returns {"added":bool}.
func (h *handler) handleAddRoleBinding(w http.ResponseWriter, r *http.Request) {
	var body struct {
		User string `json:"user"`
		Role string `json:"role"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	added, err := h.cfg.policyAdmin.AddRole(r.Context(), service.RoleBinding{User: body.User, Role: body.Role})
	if err != nil {
		WriteHTTPError(w, err)
		return
	}
	v := added
	h.writeJSON(w, r, http.StatusOK, roleBindingMutateResponse{Added: &v})
}

// handleRemoveRoleBinding handles DELETE /admin/role-bindings.
//
// Body: {"user":..,"role":..}. Returns {"removed":bool}.
func (h *handler) handleRemoveRoleBinding(w http.ResponseWriter, r *http.Request) {
	var body struct {
		User string `json:"user"`
		Role string `json:"role"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	removed, err := h.cfg.policyAdmin.RemoveRole(r.Context(), service.RoleBinding{User: body.User, Role: body.Role})
	if err != nil {
		WriteHTTPError(w, err)
		return
	}
	v := removed
	h.writeJSON(w, r, http.StatusOK, roleBindingMutateResponse{Removed: &v})
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

// relayStatsResponse is the JSON body returned by GET /admin/relay-stats.
type relayStatsResponse struct {
	Pending                 int64 `json:"pending"`
	Dead                    int64 `json:"dead"`
	OldestPendingAgeSeconds int64 `json:"oldest_pending_age_seconds"`
}

// handleAdminRelayStats handles GET /admin/relay-stats.
//
// It is registered only when a RelayStatsAdmin is wired via WithRelayStatsAdmin,
// so h.cfg.relayStats is guaranteed non-nil here.
func (h *handler) handleAdminRelayStats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.cfg.relayStats.OutboxStats(r.Context())
	if err != nil {
		WriteHTTPError(w, err)
		return
	}
	h.writeJSON(w, r, http.StatusOK, relayStatsResponse{
		Pending:                 stats.Pending,
		Dead:                    stats.Dead,
		OldestPendingAgeSeconds: int64(stats.OldestPendingAge / time.Second),
	})
}

// timerItemView is the JSON projection of a single kernel.ArmedTimer.
type timerItemView struct {
	InstanceID string `json:"instance_id"`
	DefID      string `json:"def_id"`
	DefVersion int    `json:"def_version"`
	TimerID    string `json:"timer_id"`
	FireAt     string `json:"fire_at"`
	Kind       string `json:"kind"`
}

// timerListResponse is the JSON body returned by GET /admin/timers.
type timerListResponse struct {
	Count      int64           `json:"count"`
	NextFireAt *time.Time      `json:"next_fire_at,omitempty"`
	Items      []timerItemView `json:"items"`
}

// handleAdminTimers handles GET /admin/timers.
//
// It is registered only when a TimerAdmin is wired via WithTimerAdmin,
// so h.cfg.timerAdmin is guaranteed non-nil here.
func (h *handler) handleAdminTimers(w http.ResponseWriter, r *http.Request) {
	stats, err := h.cfg.timerAdmin.Stats(r.Context())
	if err != nil {
		WriteHTTPError(w, err)
		return
	}
	armed, err := h.cfg.timerAdmin.ListArmed(r.Context())
	if err != nil {
		WriteHTTPError(w, err)
		return
	}

	items := make([]timerItemView, len(armed))
	for i, t := range armed {
		items[i] = timerItemView{
			InstanceID: t.InstanceID,
			DefID:      t.DefID,
			DefVersion: t.DefVersion,
			TimerID:    t.TimerID,
			FireAt:     t.FireAt.Format(time.RFC3339),
			Kind:       t.Kind.String(),
		}
	}
	h.writeJSON(w, r, http.StatusOK, timerListResponse{
		Count:      stats.Armed,
		NextFireAt: stats.NextFireAt,
		Items:      items,
	})
}

// handleAdminInstanceLineage handles GET /admin/instances/{id}/lineage.
//
// It is registered only when a LineageAdmin is wired via WithLineageAdmin,
// so h.cfg.lineageAdmin is guaranteed non-nil here.
func (h *handler) handleAdminInstanceLineage(w http.ResponseWriter, r *http.Request) {
	instanceID := r.PathValue("id")
	lineage, err := h.cfg.lineageAdmin.Lineage(r.Context(), instanceID)
	if err != nil {
		WriteHTTPError(w, err)
		return
	}
	h.writeJSON(w, r, http.StatusOK, newLineageView(lineage))
}

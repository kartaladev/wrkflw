package httpcore

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/monitor"
	"github.com/zakyalvan/krtlwrkflw/service"
)

// parseAdminStatus converts a status string back to an engine.Status value.
// Returns a wrapped ErrBadInput for unknown values so ClassifyError maps it to 400.
func parseAdminStatus(s string) (engine.Status, error) {
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

// adminListResponse is the JSON envelope returned by AdminListInstances.
type adminListResponse struct {
	Items      []instanceSummaryView `json:"items"`
	NextCursor string                `json:"next_cursor"`
	HasMore    bool                  `json:"has_more"`
	TotalCount int                   `json:"total_count"`
}

// AdminListInstances returns a paginated list of process instance summaries
// matching the filter in q. Returns (200, adminListResponse, nil) on success.
// Returns (0, nil, ErrBadInput-wrapping error) when q.Status is unknown.
func AdminListInstances(ctx context.Context, svc service.Service, q ListInstancesQuery) (int, any, error) {
	var statusFilter *engine.Status
	if q.Status != "" {
		st, err := parseAdminStatus(q.Status)
		if err != nil {
			return 0, nil, err
		}
		statusFilter = &st
	}

	filter := kernel.InstanceFilter{
		Status:       statusFilter,
		Limit:        kernel.NormalizeLimit(q.Limit),
		Cursor:       q.Cursor,
		IncludeTotal: q.IncludeTotal,
	}

	page, err := svc.ListInstances(ctx, filter)
	if err != nil {
		return 0, nil, err
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
			Status:        s.Status.String(),
			StartedAt:     s.StartedAt,
			EndedAt:       s.EndedAt,
			IncidentCount: s.IncidentCount,
		}
	}
	return http.StatusOK, resp, nil
}

// ResolveIncident resolves an open incident on a process instance, optionally
// granting additional execution attempts via in.AddAttempts. Returns (200,
// InstanceView, nil) on success.
func ResolveIncident(ctx context.Context, svc service.Service, instanceID, incidentID string, in ResolveIncidentInput) (int, any, error) {
	pi, err := svc.ResolveIncident(ctx, service.ResolveIncidentRequest{
		InstanceID:  instanceID,
		IncidentID:  incidentID,
		AddAttempts: in.AddAttempts,
	})
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, NewInstanceView(pi.State()), nil
}

// CancelInstance cancels the given process instance. Returns (200,
// InstanceView, nil) on success.
func CancelInstance(ctx context.Context, svc service.Service, instanceID string) (int, any, error) {
	pi, err := svc.CancelInstance(ctx, service.CancelInstanceRequest{InstanceID: instanceID})
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, NewInstanceView(pi.State()), nil
}

// dlqListResponse is the JSON envelope returned by ListDeadLetters.
type dlqListResponse struct {
	Items []deadLetterView `json:"items"`
}

// dlqRedriveResponse is the JSON envelope returned by RedriveDeadLetters.
type dlqRedriveResponse struct {
	Redriven int `json:"redriven"`
}

// deadLetterView is the JSON projection of a monitor.DeadLetter for the DLQ admin API.
type deadLetterView struct {
	ID         int64     `json:"id"`
	InstanceID string    `json:"instance_id"`
	Topic      string    `json:"topic"`
	RetryCount int       `json:"retry_count"`
	LastError  string    `json:"last_error"`
	Category   string    `json:"category"`
	CreatedAt  time.Time `json:"created_at"`
}

// ListDeadLetters returns a page of dead-lettered outbox entries. Returns (200,
// dlqListResponse, nil) on success. q.Limit is clamped by kernel.NormalizeLimit.
func ListDeadLetters(ctx context.Context, a service.DeadLetterAdmin, q DeadLetterQuery) (int, any, error) {
	rows, err := a.ListDeadLettered(ctx, kernel.NormalizeLimit(q.Limit))
	if err != nil {
		return 0, nil, err
	}

	resp := dlqListResponse{Items: make([]deadLetterView, len(rows))}
	for i, dl := range rows {
		resp.Items[i] = deadLetterView{
			ID:         dl.ID,
			InstanceID: dl.InstanceID,
			Topic:      dl.Topic,
			RetryCount: dl.RetryCount,
			LastError:  dl.LastError,
			Category:   monitor.ClassifyDeadLetter(dl.LastError),
			CreatedAt:  dl.CreatedAt,
		}
	}
	return http.StatusOK, resp, nil
}

// RedriveDeadLetters re-queues the dead-lettered entries identified by in.IDs.
// Returns (200, dlqRedriveResponse, nil) on success. An empty IDs slice is a
// no-op that returns {"redriven":0}.
func RedriveDeadLetters(ctx context.Context, a service.DeadLetterAdmin, in RedriveInput) (int, any, error) {
	n, err := a.Redrive(ctx, in.IDs...)
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, dlqRedriveResponse{Redriven: n}, nil
}

// policyView is the JSON projection of a service.PolicyRule for the policy-admin API.
type policyView struct {
	Subject string `json:"subject"`
	Object  string `json:"object"`
	Action  string `json:"action"`
}

// policyListResponse is the JSON envelope returned by ListPolicies.
type policyListResponse struct {
	Policies []policyView `json:"policies"`
}

// policyMutateResponse is the JSON envelope returned by AddPolicy / RemovePolicy.
type policyMutateResponse struct {
	Added   *bool `json:"added,omitempty"`
	Removed *bool `json:"removed,omitempty"`
}

// roleBindingView is the JSON projection of a service.RoleBinding for the policy-admin API.
type roleBindingView struct {
	User string `json:"user"`
	Role string `json:"role"`
}

// roleBindingListResponse is the JSON envelope returned by ListRoleBindings.
type roleBindingListResponse struct {
	RoleBindings []roleBindingView `json:"role_bindings"`
}

// roleBindingMutateResponse is the JSON envelope returned by AddRoleBinding / RemoveRoleBinding.
type roleBindingMutateResponse struct {
	Added   *bool `json:"added,omitempty"`
	Removed *bool `json:"removed,omitempty"`
}

// ListPolicies returns all authorization policy rules. Returns (200,
// policyListResponse, nil) on success.
func ListPolicies(ctx context.Context, a service.PolicyAdmin) (int, any, error) {
	rules, err := a.ListPolicies(ctx)
	if err != nil {
		return 0, nil, err
	}
	resp := policyListResponse{Policies: make([]policyView, len(rules))}
	for i, rule := range rules {
		resp.Policies[i] = policyView{Subject: rule.Subject, Object: rule.Object, Action: rule.Action}
	}
	return http.StatusOK, resp, nil
}

// AddPolicy adds a permission rule. Returns (200, policyMutateResponse{Added:&bool},
// nil) on success. added is false when the rule already exists.
func AddPolicy(ctx context.Context, a service.PolicyAdmin, in PolicyRuleInput) (int, any, error) {
	added, err := a.AddPolicy(ctx, service.PolicyRule{
		Subject: in.Subject, Object: in.Object, Action: in.Action,
	})
	if err != nil {
		return 0, nil, err
	}
	v := added
	return http.StatusOK, policyMutateResponse{Added: &v}, nil
}

// RemovePolicy removes a permission rule. Returns (200,
// policyMutateResponse{Removed:&bool}, nil) on success. removed is false when
// the rule did not exist.
func RemovePolicy(ctx context.Context, a service.PolicyAdmin, in PolicyRuleInput) (int, any, error) {
	removed, err := a.RemovePolicy(ctx, service.PolicyRule{
		Subject: in.Subject, Object: in.Object, Action: in.Action,
	})
	if err != nil {
		return 0, nil, err
	}
	v := removed
	return http.StatusOK, policyMutateResponse{Removed: &v}, nil
}

// ListRoleBindings returns all role inheritance rules. Returns (200,
// roleBindingListResponse, nil) on success.
func ListRoleBindings(ctx context.Context, a service.PolicyAdmin) (int, any, error) {
	bindings, err := a.ListRoles(ctx)
	if err != nil {
		return 0, nil, err
	}
	resp := roleBindingListResponse{RoleBindings: make([]roleBindingView, len(bindings))}
	for i, b := range bindings {
		resp.RoleBindings[i] = roleBindingView{User: b.User, Role: b.Role}
	}
	return http.StatusOK, resp, nil
}

// AddRoleBinding adds a role assignment. Returns (200,
// roleBindingMutateResponse{Added:&bool}, nil) on success. added is false when
// the binding already exists.
func AddRoleBinding(ctx context.Context, a service.PolicyAdmin, in RoleBindingInput) (int, any, error) {
	added, err := a.AddRole(ctx, service.RoleBinding{User: in.User, Role: in.Role})
	if err != nil {
		return 0, nil, err
	}
	v := added
	return http.StatusOK, roleBindingMutateResponse{Added: &v}, nil
}

// RemoveRoleBinding removes a role assignment. Returns (200,
// roleBindingMutateResponse{Removed:&bool}, nil) on success. removed is false
// when the binding did not exist.
func RemoveRoleBinding(ctx context.Context, a service.PolicyAdmin, in RoleBindingInput) (int, any, error) {
	removed, err := a.RemoveRole(ctx, service.RoleBinding{User: in.User, Role: in.Role})
	if err != nil {
		return 0, nil, err
	}
	v := removed
	return http.StatusOK, roleBindingMutateResponse{Removed: &v}, nil
}

// relayStatsResponse is the JSON body returned by AdminRelayStats.
type relayStatsResponse struct {
	Pending                 int64 `json:"pending"`
	Dead                    int64 `json:"dead"`
	OldestPendingAgeSeconds int64 `json:"oldest_pending_age_seconds"`
}

// AdminRelayStats returns aggregate statistics about the outbox relay. Returns
// (200, relayStatsResponse, nil) on success.
func AdminRelayStats(ctx context.Context, a service.RelayStatsAdmin) (int, any, error) {
	stats, err := a.OutboxStats(ctx)
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, relayStatsResponse{
		Pending:                 stats.Pending,
		Dead:                    stats.Dead,
		OldestPendingAgeSeconds: int64(stats.OldestPendingAge / time.Second),
	}, nil
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

// timerListResponse is the JSON body returned by AdminTimers.
type timerListResponse struct {
	Count      int64           `json:"count"`
	NextFireAt *time.Time      `json:"next_fire_at,omitempty"`
	Items      []timerItemView `json:"items"`
}

// AdminTimers returns the count, next fire time, and full list of armed timers.
// Returns (200, timerListResponse, nil) on success.
func AdminTimers(ctx context.Context, a service.TimerAdmin) (int, any, error) {
	stats, err := a.Stats(ctx)
	if err != nil {
		return 0, nil, err
	}
	armed, err := a.ListArmed(ctx)
	if err != nil {
		return 0, nil, err
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
	return http.StatusOK, timerListResponse{
		Count:      stats.Armed,
		NextFireAt: stats.NextFireAt,
		Items:      items,
	}, nil
}

// lineageCallRefView is the snake_case REST projection of a kernel.CallLinkRef.
type lineageCallRefView struct {
	InstanceID string `json:"instance_id"`
	DefID      string `json:"def_id"`
	DefVersion int    `json:"def_version"`
	Depth      int    `json:"depth"`
}

// lineageChainRefView is the snake_case REST projection of a kernel.ChainLinkRef.
type lineageChainRefView struct {
	InstanceID    string `json:"instance_id"`
	DefinitionRef string `json:"definition_ref"`
	Outcome       string `json:"outcome"`
}

// lineageView is the snake_case REST projection of a kernel.InstanceLineage.
// call_parent and chain_predecessor are omitted when nil; call_children and
// chain_successors are always serialized as arrays (never null).
type lineageView struct {
	InstanceID       string                `json:"instance_id"`
	CallParent       *lineageCallRefView   `json:"call_parent,omitempty"`
	CallChildren     []lineageCallRefView  `json:"call_children"`
	ChainPredecessor *lineageChainRefView  `json:"chain_predecessor,omitempty"`
	ChainSuccessors  []lineageChainRefView `json:"chain_successors"`
}

// newLineageView maps a kernel.InstanceLineage to the lineageView DTO.
func newLineageView(l kernel.InstanceLineage) lineageView {
	v := lineageView{
		InstanceID:      l.InstanceID,
		CallChildren:    make([]lineageCallRefView, len(l.CallChildren)),
		ChainSuccessors: make([]lineageChainRefView, len(l.ChainSuccessors)),
	}
	if l.CallParent != nil {
		r := lineageCallRefView{
			InstanceID: l.CallParent.InstanceID,
			DefID:      l.CallParent.DefID,
			DefVersion: l.CallParent.DefVersion,
			Depth:      l.CallParent.Depth,
		}
		v.CallParent = &r
	}
	for i, c := range l.CallChildren {
		v.CallChildren[i] = lineageCallRefView{
			InstanceID: c.InstanceID,
			DefID:      c.DefID,
			DefVersion: c.DefVersion,
			Depth:      c.Depth,
		}
	}
	if l.ChainPredecessor != nil {
		r := lineageChainRefView{
			InstanceID:    l.ChainPredecessor.InstanceID,
			DefinitionRef: l.ChainPredecessor.DefinitionRef,
			Outcome:       l.ChainPredecessor.Outcome,
		}
		v.ChainPredecessor = &r
	}
	for i, s := range l.ChainSuccessors {
		v.ChainSuccessors[i] = lineageChainRefView{
			InstanceID:    s.InstanceID,
			DefinitionRef: s.DefinitionRef,
			Outcome:       s.Outcome,
		}
	}
	return v
}

// AdminInstanceLineage returns the call and chain lineage for the given instance.
// Returns (200, lineageView, nil) on success.
func AdminInstanceLineage(ctx context.Context, a service.LineageAdmin, instanceID string) (int, any, error) {
	lineage, err := a.Lineage(ctx, instanceID)
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, newLineageView(lineage), nil
}

package httpcore

import "github.com/zakyalvan/krtlwrkflw/definition/model"

// Actor carries identity and role membership for task-related requests.
// It mirrors the original inline actorBody request structs.
type Actor struct {
	ID    string   `json:"id"`
	Roles []string `json:"roles"`
}

// StartInput is the request body for POST /instances (start a process instance).
// DefRef is required; the instance ID is server-generated.
type StartInput struct {
	DefRef model.Qualifier `json:"def_ref" validate:"required"`
	Vars   map[string]any  `json:"vars"`
}

// SignalInput is the request body for POST /instances/{id}/signals.
// Signal is required on the wire.
type SignalInput struct {
	Signal  string         `json:"signal"  validate:"required"`
	Payload map[string]any `json:"payload"`
}

// MessageInput is the request body for POST /messages (deliver a message).
// DefRef and Name are required on the wire.
type MessageInput struct {
	DefRef         model.Qualifier `json:"def_ref"         validate:"required"`
	Name           string          `json:"name"            validate:"required"`
	CorrelationKey string          `json:"correlation_key"`
	Payload        map[string]any  `json:"payload"`
}

// ClaimInput is the request body for POST /tasks/{token}/claim.
// No fields are required — an empty actor is allowed.
type ClaimInput struct {
	Actor Actor `json:"actor"`
}

// CompleteInput is the request body for POST /tasks/{token}/complete.
// No fields are required — actor and output are both optional.
type CompleteInput struct {
	Actor  Actor          `json:"actor"`
	Output map[string]any `json:"output"`
}

// ReassignInput is the request body for POST /tasks/{token}/reassign.
// No fields carry explicit required-field validation in the rest handler,
// so no validate tags are added.
type ReassignInput struct {
	From string `json:"from"`
	To   string `json:"to"`
	By   Actor  `json:"by"`
}

// --- Admin DTOs (the admin request bodies) ---

// PolicyRuleInput is the request body for POST /admin/policies and
// DELETE /admin/policies. The rest handler reads all three fields without
// explicit required-field checks; no validate tags are added to avoid
// diverging from the current behaviour.
type PolicyRuleInput struct {
	Subject string `json:"subject"`
	Object  string `json:"object"`
	Action  string `json:"action"`
}

// RoleBindingInput is the request body for POST /admin/role-bindings and
// DELETE /admin/role-bindings. The rest handler reads both fields without
// explicit required-field checks; no validate tags are added.
type RoleBindingInput struct {
	User string `json:"user"`
	Role string `json:"role"`
}

// RedriveInput is the request body for POST /admin/dead-letters/redrive.
// IDs may be empty (no-op that returns {"redriven":0}).
type RedriveInput struct {
	IDs []int64 `json:"ids"`
}

// ResolveIncidentInput is the optional request body for
// POST /admin/instances/{id}/incidents/{incidentID}/resolve.
// AddAttempts defaults to 1 when absent or ≤ 0; no required-field check.
type ResolveIncidentInput struct {
	AddAttempts int `json:"add_attempts"`
}

// ListInstancesQuery carries the decoded query parameters for
// GET /admin/instances. It is a convenience type — adapters parse these
// from the URL query string rather than from a JSON body.
// No validate tags: status validation is done by parseStatus; limit and
// cursor are validated inline by the handler.
type ListInstancesQuery struct {
	Status       string `json:"status"`
	Limit        int    `json:"limit"`
	Cursor       string `json:"cursor"`
	IncludeTotal bool   `json:"total"`
}

// DeadLetterQuery carries the decoded query parameters for
// GET /admin/dead-letters. Limit is optional and clamped by
// kernel.NormalizeLimit (default 50, max 200).
type DeadLetterQuery struct {
	Limit int `json:"limit"`
}

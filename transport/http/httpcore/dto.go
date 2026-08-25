package httpcore

import (
	"fmt"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
)

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
// Name is required on the wire; the definition is resolved by the engine from
// the correlated instance or a message-start event (ADR-0121), so no def_ref is
// carried.
type MessageInput struct {
	Name           string         `json:"name"            validate:"required"`
	CorrelationKey string         `json:"correlation_key"`
	Payload        map[string]any `json:"payload"`
}

// ClaimInput is the request body for POST /tasks/{token}/claim.
//
// It is deliberately EMPTY. The claimant is the AUTHENTICATED actor resolved from the
// request context (ADR-0189), never a value the caller supplies — before that record
// this struct carried an Actor and any caller could name their own roles.
//
// The type is kept rather than dropped so a body posted by a pre-ADR-0189 client still
// decodes to a no-op instead of failing, and so the route keeps a stable signature.
type ClaimInput struct{}

// CompleteInput is the request body for POST /tasks/{token}/complete.
// No fields are required. ⚠ The completing actor is NOT carried here: it is resolved
// from the request context (ADR-0189).
type CompleteInput struct {
	// Outcome is the business outcome the actor chose (e.g. "approve"); optional.
	// When the user-task node declares an outcome set, an outcome outside it is
	// rejected by the engine.
	Outcome string `json:"outcome,omitempty"`
	// Note is the actor's free-text remark accompanying the completion; optional.
	Note   string         `json:"note,omitempty"`
	Output map[string]any `json:"output"`
}

// ReassignInput is the request body for POST /tasks/{token}/reassign.
// From and To name task PARTICIPANTS, not the requester. ⚠ The actor performing the
// reassignment is resolved from the request context (ADR-0189), not from the body.
type ReassignInput struct {
	From string `json:"from"`
	To   string `json:"to"`
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

// ListArmedTimersQuery carries the decoded query parameters for
// GET /admin/timers. Adapters parse these from the URL query string rather
// than from a JSON body, mirroring [ListInstancesQuery].
//
// Limit is clamped — never rejected — via kernel.NormalizeLimit (default 50,
// max 200), by [AdminTimers] before the value reaches service.TimerAdmin. The
// SQL store clamps again; NormalizeLimit is idempotent, so that is free, and it
// keeps a direct store caller safe too. Cursor is the opaque token from the
// previous page's next_cursor; a malformed one is a 400, not a silent reset to
// page one. IncludeTotal ("total") gates the aggregate count and next-fire
// time, which cost an extra query (ADR-0159).
type ListArmedTimersQuery struct {
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

// ResolveCompensationStallInput is the request body for
// POST /admin/instances/{id}/compensation/resolve-stall (ADR-0175).
//
// Unlike ResolveIncidentInput nothing here is optional-with-a-default:
// CommandID is a required identity the engine matches against the walk's cursor,
// and Disposition names a destructive or re-executing action. Both are validated
// before the service is touched.
type ResolveCompensationStallInput struct {
	// CommandID is the stalled compensation dispatch, read from an instance's
	// `compensating.active_command_id`. REQUIRED.
	CommandID string `json:"command_id"`
	// IncidentID optionally names the stall incident being cleared. Empty targets
	// the walk in flight.
	IncidentID string `json:"incident_id"`
	// Disposition is "retry", "skip" or "abandon". REQUIRED — see
	// ParseCompensationDisposition for why it is not allowed to default.
	Disposition string `json:"disposition"`
}

// ParseCompensationDisposition maps the wire string to its engine value.
//
// It fails closed on an empty or unrecognised value rather than defaulting.
// That is not pedantry: the zero value of engine.CompensationDisposition is
// CompensationRetry, a remote RE-EXECUTION primitive, so a defaulted unknown
// verb would re-invoke a named action with the record's captured input — work
// the operator never asked for.
func ParseCompensationDisposition(s string) (engine.CompensationDisposition, error) {
	switch s {
	case "retry":
		return engine.CompensationRetry, nil
	case "skip":
		return engine.CompensationSkip, nil
	case "abandon":
		return engine.CompensationAbandon, nil
	default:
		return 0, fmt.Errorf("%w: disposition must be one of retry, skip, abandon (got %q)",
			ErrBadInput, s)
	}
}

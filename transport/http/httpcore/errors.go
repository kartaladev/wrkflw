package httpcore

import (
	"errors"
	"net/http"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/runtime/validation"
	"github.com/kartaladev/wrkflw/service"
)

// ErrBadInput is the sentinel for 400-class decode/validation errors.
var ErrBadInput = errors.New("workflow-httpcore: bad input")

// ErrRequestBodyTooLarge is the sentinel for an INBOUND request body that
// exceeded [CustomizeConfig.MaxBodyBytes]. It classifies as 413.
//
// ⚠ Not to be confused with action/httpcall.ErrBodyTooLarge, a distinct
// sentinel meaning an OUTBOUND response exceeded httpcall's own cap — a
// server-side fault the caller cannot correct, which correctly stays a 500.
var ErrRequestBodyTooLarge = errors.New("workflow-httpcore: request body too large")

// ErrorBody is the JSON error envelope. Message is omitted for 5xx responses.
type ErrorBody struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

// ClassifyError maps err to an HTTP status and a CLIENT-SAFE body. For 5xx the
// Message is empty; callers log the raw error instead of exposing it.
func ClassifyError(err error) (int, ErrorBody) {
	switch {
	case errors.Is(err, kernel.ErrInstanceNotFound),
		errors.Is(err, kernel.ErrDefinitionNotFound),
		errors.Is(err, humantask.ErrTaskNotFound):
		return http.StatusNotFound, ErrorBody{Error: "not_found", Message: err.Error()}
	case errors.Is(err, authz.ErrNotAuthorized):
		return http.StatusForbidden, ErrorBody{Error: "forbidden", Message: err.Error()}
	case errors.Is(err, kernel.ErrConcurrentUpdate):
		return http.StatusConflict, ErrorBody{Error: "conflict", Message: err.Error()}
	// ⚠ POSITION IS BEHAVIOUR. This arm MUST precede the 400 arm below. Go's
	// switch is ordered, so an error matching two arms resolves to whichever
	// comes first — and an oversize body arrives wrapped in ErrBadInput from
	// every decode site, so it matches both. Below the 400 arm this returns
	// "bad_request" (MEASURED: 413 expected, 400 actual) and the oversize case
	// becomes indistinguishable from malformed JSON.
	//
	// STANDING INVARIANT for any arm added to this switch: state its position
	// relative to the arms it can co-match, and carry a test asserting an error
	// matching two arms resolves to the intended one. Ordering that is not
	// pinned by a test is ordering that silently rots on the next edit.
	//
	// ⚠ The body is STATIC — no err.Error(), unlike every other 4xx arm, which
	// this one must not inherit by accident. It deliberately does not name the
	// configured limit: that is deployment configuration, and echoing it tells
	// an attacker exactly what to stay under.
	case errors.Is(err, ErrRequestBodyTooLarge):
		return http.StatusRequestEntityTooLarge, ErrorBody{
			Error:   "request_too_large",
			Message: "request body exceeds the configured limit",
		}
	case errors.Is(err, kernel.ErrBadCursor), errors.Is(err, kernel.ErrBadArmedTimerCursor),
		errors.Is(err, ErrBadInput), errors.Is(err, validation.ErrInvalidInput),
		// Both outcome sentinels describe a completion payload the caller can
		// correct — an outcome outside the node's declared set, or none supplied
		// where the node declares one (ADR-0146). Without these arms they fall to
		// the 500 default, which hides an actionable 4xx behind an empty body.
		errors.Is(err, engine.ErrInvalidOutcome), errors.Is(err, engine.ErrOutcomeRequired),
		// An empty trigger identity key is a malformed request the caller can fix
		// by supplying the id (ADR-0152), not a server fault. This changes the
		// human-task routes from 404/422 and the incident route from 200.
		errors.Is(err, engine.ErrEmptyTriggerKey),
		// Likewise a reassignment naming no actor: a required field the caller
		// omitted and can supply, not a server fault (ADR-0183).
		errors.Is(err, engine.ErrEmptyReassignTarget):
		return http.StatusBadRequest, ErrorBody{Error: "bad_request", Message: err.Error()}
	case errors.Is(err, service.ErrConflict), errors.Is(err, engine.ErrInvalidTransition),
		// A contradictory task shape is engine-authored — editing the request
		// cannot fix it — so it is 422 like the other unprocessable states, and
		// the message names the task and the contradiction (ADR-0183).
		errors.Is(err, humantask.ErrInvalidTask):
		return http.StatusUnprocessableEntity, ErrorBody{Error: "conflict_state", Message: err.Error()}
	default:
		return http.StatusInternalServerError, ErrorBody{Error: "internal_error"}
	}
}

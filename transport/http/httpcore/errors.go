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
		errors.Is(err, engine.ErrEmptyTriggerKey):
		return http.StatusBadRequest, ErrorBody{Error: "bad_request", Message: err.Error()}
	case errors.Is(err, service.ErrConflict), errors.Is(err, engine.ErrInvalidTransition):
		return http.StatusUnprocessableEntity, ErrorBody{Error: "conflict_state", Message: err.Error()}
	default:
		return http.StatusInternalServerError, ErrorBody{Error: "internal_error"}
	}
}

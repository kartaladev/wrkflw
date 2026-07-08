package httpcore

import (
	"errors"
	"net/http"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/service"
	"github.com/zakyalvan/krtlwrkflw/validation"
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
	case errors.Is(err, kernel.ErrBadCursor), errors.Is(err, ErrBadInput), errors.Is(err, validation.ErrInvalidInput):
		return http.StatusBadRequest, ErrorBody{Error: "bad_request", Message: err.Error()}
	case errors.Is(err, service.ErrConflict), errors.Is(err, engine.ErrInvalidTransition):
		return http.StatusUnprocessableEntity, ErrorBody{Error: "conflict_state", Message: err.Error()}
	default:
		return http.StatusInternalServerError, ErrorBody{Error: "internal_error"}
	}
}

package rest

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/service"
)

// ErrBadInput is the sentinel for 400-class decode/validation errors.
var ErrBadInput = errors.New("bad input")

type errorBody struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// WriteHTTPError classifies err, sets the status, and encodes a JSON error body.
func WriteHTTPError(w http.ResponseWriter, err error) {
	code, errCode := classifyError(err)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(errorBody{Error: errCode, Message: err.Error()})
}

func classifyError(err error) (int, string) {
	switch {
	case errors.Is(err, runtime.ErrInstanceNotFound),
		errors.Is(err, runtime.ErrDefinitionNotFound),
		errors.Is(err, humantask.ErrTaskNotFound):
		return http.StatusNotFound, "not_found"
	case errors.Is(err, authz.ErrNotAuthorized):
		return http.StatusForbidden, "forbidden"
	case errors.Is(err, runtime.ErrConcurrentUpdate):
		return http.StatusConflict, "conflict"
	case errors.Is(err, runtime.ErrBadCursor),
		errors.Is(err, ErrBadInput):
		return http.StatusBadRequest, "bad_request"
	case errors.Is(err, service.ErrConflict),
		errors.Is(err, engine.ErrInvalidTransition):
		return http.StatusUnprocessableEntity, "conflict_state"
	default:
		return http.StatusInternalServerError, "internal_error"
	}
}

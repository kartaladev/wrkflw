package rest_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/service"
	rest "github.com/zakyalvan/krtlwrkflw/transport/rest"
)

func TestMapToHTTPError(t *testing.T) {
	tests := []struct {
		name          string
		err           error
		wantStatus    int
		wantErrorCode string // if non-empty, assert body["error"] == wantErrorCode
	}{
		{
			name:       "instance not found",
			err:        fmt.Errorf("wrap: %w", kernel.ErrInstanceNotFound),
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "definition not found",
			err:        fmt.Errorf("wrap: %w", kernel.ErrDefinitionNotFound),
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "task not found",
			err:        fmt.Errorf("wrap: %w", humantask.ErrTaskNotFound),
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "not authorized",
			err:        fmt.Errorf("wrap: %w", authz.ErrNotAuthorized),
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "concurrent update",
			err:        fmt.Errorf("wrap: %w", kernel.ErrConcurrentUpdate),
			wantStatus: http.StatusConflict,
		},
		{
			name:       "bad cursor",
			err:        fmt.Errorf("wrap: %w", kernel.ErrBadCursor),
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "bad input sentinel",
			err:        fmt.Errorf("wrap: %w", rest.ErrBadInput),
			wantStatus: http.StatusBadRequest,
		},
		{
			name:          "conflict state",
			err:           fmt.Errorf("wrap: %w", service.ErrConflict),
			wantStatus:    http.StatusUnprocessableEntity,
			wantErrorCode: "conflict_state",
		},
		{
			name:          "engine invalid transition (bare runner)",
			err:           fmt.Errorf("wrap: %w", engine.ErrInvalidTransition),
			wantStatus:    http.StatusUnprocessableEntity,
			wantErrorCode: "conflict_state",
		},
		{
			name:       "unknown error",
			err:        fmt.Errorf("some unexpected error"),
			wantStatus: http.StatusInternalServerError,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			rest.WriteHTTPError(rec, tc.err)
			if rec.Code != tc.wantStatus {
				t.Fatalf("want status %d got %d", tc.wantStatus, rec.Code)
			}
			var body map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("body not valid JSON: %v — body: %s", err, rec.Body.String())
			}
			if body["error"] == "" {
				t.Fatal("want non-empty 'error' field in body")
			}
			if body["message"] == "" {
				t.Fatal("want non-empty 'message' field in body")
			}
			if tc.wantErrorCode != "" && body["error"] != tc.wantErrorCode {
				t.Fatalf("want body error code %q got %q", tc.wantErrorCode, body["error"])
			}
		})
	}
}

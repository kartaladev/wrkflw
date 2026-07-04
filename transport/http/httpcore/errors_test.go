package httpcore_test

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/transport/http/httpcore"
)

func TestClassifyError(t *testing.T) {
	tests := map[string]struct {
		err    error
		assert func(t *testing.T, status int, body httpcore.ErrorBody)
	}{
		"not found": {
			err: fmt.Errorf("wrap: %w", kernel.ErrInstanceNotFound),
			assert: func(t *testing.T, status int, body httpcore.ErrorBody) {
				if status != http.StatusNotFound || body.Error != "not_found" {
					t.Fatalf("got %d/%q", status, body.Error)
				}
			},
		},
		"forbidden": {
			err: authz.ErrNotAuthorized,
			assert: func(t *testing.T, status int, body httpcore.ErrorBody) {
				if status != http.StatusForbidden || body.Error != "forbidden" {
					t.Fatalf("got %d/%q", status, body.Error)
				}
			},
		},
		"bad input keeps message": {
			err: fmt.Errorf("%w: def_ref required", httpcore.ErrBadInput),
			assert: func(t *testing.T, status int, body httpcore.ErrorBody) {
				if status != http.StatusBadRequest || body.Message == "" {
					t.Fatalf("4xx must keep message; got %d/%q", status, body.Message)
				}
			},
		},
		"internal hides message": {
			err: errors.New("pgx: connection refused at 10.0.0.5:5432"),
			assert: func(t *testing.T, status int, body httpcore.ErrorBody) {
				if status != http.StatusInternalServerError {
					t.Fatalf("status=%d", status)
				}
				if body.Error != "internal_error" || body.Message != "" {
					t.Fatalf("5xx must not leak: error=%q message=%q", body.Error, body.Message)
				}
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			status, body := httpcore.ClassifyError(tc.err)
			tc.assert(t, status, body)
		})
	}
}

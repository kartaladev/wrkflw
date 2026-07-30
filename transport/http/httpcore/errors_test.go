package httpcore_test

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/runtime/validation"
	"github.com/kartaladev/wrkflw/transport/http/httpcore"
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
		"validation invalid input -> 400": {
			err: fmt.Errorf("wrap: %w", validation.ErrInvalidInput),
			assert: func(t *testing.T, status int, body httpcore.ErrorBody) {
				if status != http.StatusBadRequest || body.Error != "bad_request" {
					t.Fatalf("got %d/%q", status, body.Error)
				}
			},
		},
		"malformed armed-timer cursor -> 400": {
			// A cursor an operator pasted wrong must be an actionable 400, never a
			// silent reset to page one — which would loop a large listing forever
			// without the operator noticing (ADR-0159).
			err: fmt.Errorf("wrap: %w", kernel.ErrBadArmedTimerCursor),
			assert: func(t *testing.T, status int, body httpcore.ErrorBody) {
				if status != http.StatusBadRequest || body.Error != "bad_request" {
					t.Fatalf("got %d/%q", status, body.Error)
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

// TestClassifyErrorOutcomeSentinels pins that the two user-task outcome sentinels
// are client errors, not server errors. Both describe a bad completion payload the
// caller can correct — an outcome outside the node's declared set, or a missing
// outcome on a node that declares one — so answering 500 tells the client nothing
// and hides a 4xx behind an opaque body (ADR-0146).
func TestClassifyErrorOutcomeSentinels(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		err    error
		assert func(t *testing.T, status int, body httpcore.ErrorBody)
	}

	cases := []testCase{
		{
			name: "an outcome outside the declared set is a bad request",
			err:  fmt.Errorf("workflow-service: complete task: %w", engine.ErrInvalidOutcome),
			assert: func(t *testing.T, status int, body httpcore.ErrorBody) {
				assert.Equal(t, http.StatusBadRequest, status)
				assert.Equal(t, "bad_request", body.Error)
				assert.Contains(t, body.Message, "outcome")
			},
		},
		{
			name: "a missing required outcome is a bad request",
			err:  fmt.Errorf("workflow-service: complete task: %w", engine.ErrOutcomeRequired),
			assert: func(t *testing.T, status int, body httpcore.ErrorBody) {
				assert.Equal(t, http.StatusBadRequest, status)
				assert.Equal(t, "bad_request", body.Error)
				assert.Contains(t, body.Message, "outcome")
			},
		},
		{
			// ADR-0152: a malformed trigger is a caller-correctable input error,
			// not a server fault.
			name: "empty trigger key is a bad request",
			err:  fmt.Errorf("apply trigger: %w", engine.ErrEmptyTriggerKey),
			assert: func(t *testing.T, status int, body httpcore.ErrorBody) {
				assert.Equal(t, http.StatusBadRequest, status)
				assert.Equal(t, "bad_request", body.Error)
				assert.NotEmpty(t, body.Message, "a 4xx body must carry an actionable message")
			},
		},
		{
			name: "wrapped empty trigger key is still a bad request",
			err:  fmt.Errorf("service: %w", fmt.Errorf("engine: %w", engine.ErrEmptyTriggerKey)),
			assert: func(t *testing.T, status int, body httpcore.ErrorBody) {
				assert.Equal(t, http.StatusBadRequest, status)
				assert.Equal(t, "bad_request", body.Error)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status, body := httpcore.ClassifyError(tc.err)
			tc.assert(t, status, body)
		})
	}
}

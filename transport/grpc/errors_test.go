// Package grpctransport_test is the black-box test suite for the gRPC transport.
package grpctransport_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/service"
	grpctransport "github.com/zakyalvan/krtlwrkflw/transport/grpc"
)

// TestMapToGRPCStatus verifies that the error-to-gRPC-status mapping in
// RegisterWorkflowServiceServer produces the correct status codes for all
// domain errors. The mapping is exercised indirectly through the exported
// helper exposed for testing.
func TestMapToGRPCStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want codes.Code
	}{
		{"instance not found", kernel.ErrInstanceNotFound, codes.NotFound},
		{"definition not found", kernel.ErrDefinitionNotFound, codes.NotFound},
		{"task not found", humantask.ErrTaskNotFound, codes.NotFound},
		{"not authorized", authz.ErrNotAuthorized, codes.PermissionDenied},
		{"concurrent update", kernel.ErrConcurrentUpdate, codes.Aborted},
		{"bad cursor", kernel.ErrBadCursor, codes.InvalidArgument},
		{"unknown error", errors.New("boom"), codes.Internal},
		{"wrapped not found", fmt.Errorf("wrap: %w", kernel.ErrInstanceNotFound), codes.NotFound},
		{"conflict state", fmt.Errorf("x: %w", service.ErrConflict), codes.FailedPrecondition},
		{"engine invalid transition (bare runner)", fmt.Errorf("wrap: %w", engine.ErrInvalidTransition), codes.FailedPrecondition},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := status.Code(grpctransport.MapToGRPCStatus(tc.err))
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestMapToGRPCStatus_ErrorInfoDetail verifies that the gRPC status carries a
// machine-readable error code in an errdetails.ErrorInfo detail, mirroring the
// REST {error,message} taxonomy so clients can branch on the code rather than
// parsing the status message string.
func TestMapToGRPCStatus_ErrorInfoDetail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantReason string
	}{
		{"instance not found", kernel.ErrInstanceNotFound, "not_found"},
		{"not authorized", authz.ErrNotAuthorized, "forbidden"},
		{"concurrent update", kernel.ErrConcurrentUpdate, "conflict"},
		{"bad cursor", kernel.ErrBadCursor, "bad_request"},
		{"conflict state", fmt.Errorf("x: %w", service.ErrConflict), "conflict_state"},
		{"unknown error", errors.New("boom"), "internal_error"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			st, ok := status.FromError(grpctransport.MapToGRPCStatus(tc.err))
			require.True(t, ok, "expected a gRPC status error")

			var info *errdetails.ErrorInfo
			for _, d := range st.Details() {
				if ei, isInfo := d.(*errdetails.ErrorInfo); isInfo {
					info = ei
					break
				}
			}
			require.NotNil(t, info, "status must carry an ErrorInfo detail")
			assert.Equal(t, tc.wantReason, info.GetReason())
			assert.NotEmpty(t, info.GetDomain(), "ErrorInfo.Domain must identify the engine")
		})
	}
}

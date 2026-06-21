// Package grpctransport_test is the black-box test suite for the gRPC transport.
package grpctransport_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	grpctransport "github.com/zakyalvan/krtlwrkflw/transport/grpc"
	"github.com/zakyalvan/krtlwrkflw/runtime"
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
		{"instance not found", runtime.ErrInstanceNotFound, codes.NotFound},
		{"definition not found", runtime.ErrDefinitionNotFound, codes.NotFound},
		{"task not found", humantask.ErrTaskNotFound, codes.NotFound},
		{"not authorized", authz.ErrNotAuthorized, codes.PermissionDenied},
		{"concurrent update", runtime.ErrConcurrentUpdate, codes.Aborted},
		{"bad cursor", runtime.ErrBadCursor, codes.InvalidArgument},
		{"unknown error", errors.New("boom"), codes.Internal},
		{"wrapped not found", fmt.Errorf("wrap: %w", runtime.ErrInstanceNotFound), codes.NotFound},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := status.Code(grpctransport.MapToGRPCStatus(tc.err))
			assert.Equal(t, tc.want, got)
		})
	}
}

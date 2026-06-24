package grpctransport_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/zakyalvan/krtlwrkflw/transport/grpc/workflowpb"
)

// TestStartInstanceRequestValidation asserts the gRPC StartInstance handler
// rejects missing required fields with InvalidArgument at the transport boundary
// (mirroring the REST handler), rather than letting them fall through to a less
// clear deep error.
func TestStartInstanceRequestValidation(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name string
		req  *workflowpb.StartInstanceRequest
	}

	cases := []testCase{
		{name: "empty def_ref", req: &workflowpb.StartInstanceRequest{DefRef: "", InstanceId: "i1"}},
		{name: "empty instance_id", req: &workflowpb.StartInstanceRequest{DefRef: "d:1", InstanceId: ""}},
	}

	h := newGRPCHarness(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := h.client.StartInstance(t.Context(), tc.req)
			require.Error(t, err)
			assert.Equal(t, codes.InvalidArgument, status.Code(err),
				"missing required field must map to InvalidArgument")
		})
	}
}

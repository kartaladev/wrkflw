package grpctransport_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/zakyalvan/krtlwrkflw/transport/grpc/workflowpb"
)

// TestInvalidArgumentCarriesErrorInfo verifies the end-to-end bufconn path: an
// InvalidArgument produced by the transport-boundary validation sweep carries a
// machine-readable errdetails.ErrorInfo with Reason "invalid_argument" and a
// non-empty Domain — identical in shape to the classified-error path
// (mapToGRPCStatus) — so a client can branch on the structured code rather than
// parsing the human-readable status message.
func TestInvalidArgumentCarriesErrorInfo(t *testing.T) {
	t.Parallel()
	h := newGRPCHarness(t)

	// Empty def_ref triggers the invalidArg validation guard before any service call.
	_, err := h.client.StartInstance(t.Context(), &workflowpb.StartInstanceRequest{
		DefRef:     "",
		InstanceId: "inst-x",
	})
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok, "expected a gRPC status error")
	require.Equal(t, codes.InvalidArgument, st.Code())

	var info *errdetails.ErrorInfo
	for _, d := range st.Details() {
		if ei, isInfo := d.(*errdetails.ErrorInfo); isInfo {
			info = ei
			break
		}
	}
	require.NotNil(t, info, "InvalidArgument status must carry an ErrorInfo detail")
	assert.Equal(t, "invalid_argument", info.GetReason())
	assert.NotEmpty(t, info.GetDomain(), "ErrorInfo.Domain must identify the engine module")
}

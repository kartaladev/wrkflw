package grpctransport_test

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	grpctransport "github.com/zakyalvan/krtlwrkflw/transport/grpc"
	"github.com/zakyalvan/krtlwrkflw/transport/grpc/workflowpb"
)

// dialSecure stands up the given server over bufconn and returns a connected
// client. The server's lifecycle is bound to the test via t.Cleanup.
func dialSecure(t *testing.T, srv *grpc.Server) workflowpb.WorkflowServiceClient {
	t.Helper()

	lis := bufconn.Listen(bufSize)
	t.Cleanup(func() { srv.Stop() })
	go func() { _ = srv.Serve(lis) }()

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return workflowpb.NewWorkflowServiceClient(conn)
}

// TestNewMethodAuthInterceptor verifies the reusable per-method auth interceptor:
// the policy decision is taken before the handler runs, keyed off the RPC's
// full method name. A denied admin method returns the policy's code; an allowed
// non-admin method reaches the handler (and only then fails validation, proving
// it was let through the gate).
func TestNewMethodAuthInterceptor(t *testing.T) {
	t.Parallel()

	// policy denies ListInstances (admin), allows everything else.
	policy := func(_ context.Context, fullMethod string) error {
		if fullMethod == workflowpb.WorkflowService_ListInstances_FullMethodName {
			return status.Error(codes.PermissionDenied, "admin only")
		}
		return nil
	}

	srv := grpctransport.NewSecureServer(minimalSvc(t), grpctransport.NewMethodAuthInterceptor(policy))
	client := dialSecure(t, srv)

	t.Run("denied admin method returns the policy code", func(t *testing.T) {
		t.Parallel()
		_, err := client.ListInstances(t.Context(), &workflowpb.ListInstancesRequest{Limit: 10})
		require.Error(t, err)
		assert.Equal(t, codes.PermissionDenied, status.Code(err),
			"the interceptor must reject before the handler with the policy's code")
	})

	t.Run("allowed method reaches the handler", func(t *testing.T) {
		t.Parallel()
		// StartInstance is allowed by the policy, so it passes the gate and reaches
		// the handler — which then rejects the empty def_ref with InvalidArgument.
		// That InvalidArgument is the proof the interceptor let the call through.
		_, err := client.StartInstance(t.Context(), &workflowpb.StartInstanceRequest{
			DefRef:     "",
			InstanceId: "inst-1",
		})
		require.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err),
			"an allowed method must reach the handler, not be gated")
	})
}

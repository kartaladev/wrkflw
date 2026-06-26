package grpctransport_test

import (
	"context"
	"net"
	"testing"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/service"
	grpctransport "github.com/zakyalvan/krtlwrkflw/transport/grpc"
	"github.com/zakyalvan/krtlwrkflw/transport/grpc/workflowpb"
)

// minimalSvc builds a do-nothing service.Service for transport-level tests where
// the handler is never reached (the interceptor rejects first).
func minimalSvc(t *testing.T) service.Service {
	t.Helper()
	fc := clockwork.NewFakeClock()
	store := runtime.NewMemStore()
	taskStore := humantask.NewMemTaskStore()
	az := authz.RoleAuthorizer{}
	runner := runtime.NewRunner(action.NewMapCatalog(nil), store, runtime.WithRunnerClock(fc))
	reg := runtime.NewMapDefinitionRegistry(nil)
	tasks := runtime.NewTaskService(taskStore, az, runtime.WithTaskServiceClock(fc))
	return service.New(runner, tasks, reg, store, store, taskStore, service.WithEngineClock(fc))
}

// TestNewSecureServerRequiresInterceptor asserts the fail-closed contract: a nil
// auth interceptor panics, so the service can never be exposed ungated.
func TestNewSecureServerRequiresInterceptor(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() {
		grpctransport.NewSecureServer(minimalSvc(t), nil)
	}, "a nil auth interceptor must panic (fail-closed)")
}

// TestNewSecureServerInstallsInterceptor asserts the auth interceptor gates every
// RPC: an unauthenticated call is rejected before reaching the handler.
func TestNewSecureServerInstallsInterceptor(t *testing.T) {
	t.Parallel()

	deny := func(_ context.Context, _ any, _ *grpc.UnaryServerInfo, _ grpc.UnaryHandler) (any, error) {
		return nil, status.Error(codes.Unauthenticated, "no token")
	}
	srv := grpctransport.NewSecureServer(minimalSvc(t), deny)
	require.NotNil(t, srv)

	lis := bufconn.Listen(bufSize)
	t.Cleanup(func() { srv.Stop() })
	go func() { _ = srv.Serve(lis) }()

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	client := workflowpb.NewWorkflowServiceClient(conn)
	_, err = client.GetInstance(t.Context(), &workflowpb.GetInstanceRequest{InstanceId: "x"})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err),
		"the auth interceptor must gate the RPC before the handler")
}

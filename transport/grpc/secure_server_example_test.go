package grpctransport_test

import (
	"context"
	"errors"

	"github.com/jonboulle/clockwork"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/service"
	grpctransport "github.com/zakyalvan/krtlwrkflw/transport/grpc"
)

// principalKey is the context key under which the auth interceptor stores the
// authenticated principal for handlers to read.
type principalKey struct{}

// verifyToken stands in for the consumer's real authentication (JWT, session
// lookup, mTLS peer identity, …). It returns the authenticated principal id.
func verifyToken(token string) (string, error) {
	if token == "" {
		return "", errors.New("empty token")
	}
	return "alice", nil // your auth derives this from the token
}

// buildService constructs a minimal service.Service for the example.
func buildService() service.Service {
	fc := clockwork.NewFakeClock()
	store, err := runtime.NewMemStore()
	if err != nil {
		panic(err)
	}
	taskStore := humantask.NewMemTaskStore()
	az := authz.RoleAuthorizer{}
	runner := runtime.NewRunner(action.NewMapCatalog(nil), store, runtime.WithRunnerClock(fc))
	reg := runtime.NewMapDefinitionRegistry(nil)
	tasks := runtime.NewTaskService(taskStore, az, runtime.WithTaskServiceClock(fc))
	return service.New(runner, tasks, reg, store, store, taskStore, service.WithEngineClock(fc))
}

// ExampleNewSecureServer shows the fail-closed gRPC wiring: an auth interceptor
// authenticates every RPC and puts the trusted principal in the context, so
// handlers derive the actor from the authenticated identity — never from a
// client-supplied field. NewSecureServer panics if the interceptor is nil.
func ExampleNewSecureServer() {
	authInterceptor := func(
		ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler,
	) (any, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		tokens := md.Get("authorization")
		if len(tokens) == 0 {
			return nil, status.Error(codes.Unauthenticated, "missing authorization")
		}
		principal, err := verifyToken(tokens[0])
		if err != nil {
			return nil, status.Error(codes.Unauthenticated, "invalid token")
		}
		// Hand the trusted principal to handlers; do NOT trust a client-supplied actor.
		ctx = context.WithValue(ctx, principalKey{}, principal)
		return handler(ctx, req)
	}

	srv := grpctransport.NewSecureServer(buildService(), authInterceptor)
	_ = srv // The consumer owns the listener: srv.Serve(lis) / srv.GracefulStop().
}

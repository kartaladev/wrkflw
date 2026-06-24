package grpctransport

import (
	"google.golang.org/grpc"

	"github.com/zakyalvan/krtlwrkflw/service"
)

// NewSecureServer builds a *grpc.Server with auth installed as a unary
// interceptor and the WorkflowService registered on it. It is the fail-closed
// counterpart to [RegisterWorkflowServiceServer]: it PANICS if auth is nil, so the
// service can never be exposed without an authentication/authorization gate (the
// gRPC analogue of the REST transport's default-deny admin middleware).
//
// The consumer owns the returned server's lifecycle (Serve / GracefulStop) and may
// not need this helper at all — [RegisterWorkflowServiceServer] remains available
// for consumers who build their own *grpc.Server (e.g. to add TLS credentials or
// extra interceptors). To install several interceptors here, pre-combine them with
// grpc.ChainUnaryInterceptor and pass the result as auth.
//
// Actor identity: handlers that act on behalf of a principal (ClaimTask,
// CompleteTask, ReassignTask) read the actor from the request message. The auth
// interceptor MUST authenticate the caller and SHOULD make the trusted principal
// available to handlers (e.g. via context) so the consumer derives the actor from
// the authenticated identity rather than trusting a client-supplied actor field.
// See the example interceptor in secure_server_example_test.go.
func NewSecureServer(svc service.Service, auth grpc.UnaryServerInterceptor, opts ...Option) *grpc.Server {
	if auth == nil {
		panic("workflow-grpc: NewSecureServer: auth interceptor must not be nil")
	}
	gsrv := grpc.NewServer(grpc.ChainUnaryInterceptor(auth))
	RegisterWorkflowServiceServer(gsrv, svc, opts...)
	return gsrv
}

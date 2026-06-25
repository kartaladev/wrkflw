package grpctransport_test

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	grpctransport "github.com/zakyalvan/krtlwrkflw/transport/grpc"
	"github.com/zakyalvan/krtlwrkflw/transport/grpc/workflowpb"
)

// adminMethods is the set of admin-scoped RPCs that require an "admin" claim.
// Deriving authorization from grpc.UnaryServerInfo.FullMethod keeps the policy
// in one place instead of scattering checks across handlers.
var adminMethods = map[string]bool{
	workflowpb.WorkflowService_ListInstances_FullMethodName:      true,
	workflowpb.WorkflowService_ListDeadLetters_FullMethodName:    true,
	workflowpb.WorkflowService_RedriveDeadLetters_FullMethodName: true,
	workflowpb.WorkflowService_AddPolicy_FullMethodName:          true,
	workflowpb.WorkflowService_RemovePolicy_FullMethodName:       true,
	workflowpb.WorkflowService_ListPolicies_FullMethodName:       true,
	workflowpb.WorkflowService_AddRole_FullMethodName:            true,
	workflowpb.WorkflowService_RemoveRole_FullMethodName:         true,
	workflowpb.WorkflowService_ListRoles_FullMethodName:          true,
}

// taskMethods is the set of human-task RPCs whose actor must be derived from the
// authenticated principal, never trusted from the request body.
var taskMethods = map[string]bool{
	workflowpb.WorkflowService_ClaimTask_FullMethodName:    true,
	workflowpb.WorkflowService_CompleteTask_FullMethodName: true,
	workflowpb.WorkflowService_ReassignTask_FullMethodName: true,
}

// authenticate stands in for the consumer's real authentication. It returns the
// caller's principal id and claims from the bearer token.
func authenticate(ctx context.Context) (principal string, claims map[string]bool, err error) {
	md, _ := metadata.FromIncomingContext(ctx)
	tokens := md.Get("authorization")
	if len(tokens) == 0 {
		return "", nil, status.Error(codes.Unauthenticated, "missing authorization")
	}
	// Your real auth verifies the token (JWT/session/mTLS) and extracts claims.
	// Here: "Bearer admin-token" → principal "root" with the admin claim.
	switch strings.TrimPrefix(tokens[0], "Bearer ") {
	case "admin-token":
		return "root", map[string]bool{"admin": true}, nil
	case "user-token":
		return "alice", map[string]bool{}, nil
	default:
		return "", nil, status.Error(codes.Unauthenticated, "invalid token")
	}
}

// perMethodAuthInterceptor authorizes each RPC by its info.FullMethod:
//   - admin-scoped methods require the "admin" claim (else PermissionDenied);
//   - task methods stash the authenticated principal so the handler derives the
//     actor from identity rather than the client-supplied actor field;
//   - everything else only requires authentication.
func perMethodAuthInterceptor(
	ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler,
) (any, error) {
	principal, claims, err := authenticate(ctx)
	if err != nil {
		return nil, err
	}
	if adminMethods[info.FullMethod] && !claims["admin"] {
		return nil, status.Errorf(codes.PermissionDenied, "%s requires the admin claim", info.FullMethod)
	}
	if taskMethods[info.FullMethod] {
		// Hand the trusted principal to the handler; the handler must use this
		// identity as the actor instead of trusting req's actor field.
		ctx = context.WithValue(ctx, principalKey{}, principal)
	}
	return handler(ctx, req)
}

// Example_perMethodAuth shows the recommended per-method gRPC authorization
// pattern: a single unary interceptor that authorizes by
// grpc.UnaryServerInfo.FullMethod — admin RPCs require an "admin" claim, while
// task RPCs derive the actor from the authenticated principal in context rather
// than from the request body. Mount it with NewSecureServer (which panics on a
// nil interceptor, so the control plane can never be exposed ungated).
func Example_perMethodAuth() {
	srv := grpctransport.NewSecureServer(buildService(), perMethodAuthInterceptor)
	_ = srv // The consumer owns the listener: srv.Serve(lis) / srv.GracefulStop().

	// Demonstrate the routing decision the interceptor makes per method.
	for _, m := range []string{
		workflowpb.WorkflowService_ListInstances_FullMethodName,
		workflowpb.WorkflowService_ClaimTask_FullMethodName,
		workflowpb.WorkflowService_StartInstance_FullMethodName,
	} {
		switch {
		case adminMethods[m]:
			fmt.Printf("%s -> admin claim required\n", m)
		case taskMethods[m]:
			fmt.Printf("%s -> actor derived from principal\n", m)
		default:
			fmt.Printf("%s -> authenticated only\n", m)
		}
	}
	// Output:
	// /wrkflw.v1.WorkflowService/ListInstances -> admin claim required
	// /wrkflw.v1.WorkflowService/ClaimTask -> actor derived from principal
	// /wrkflw.v1.WorkflowService/StartInstance -> authenticated only
}

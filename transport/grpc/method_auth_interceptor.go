package grpctransport

import (
	"context"

	"google.golang.org/grpc"
)

// NewMethodAuthInterceptor returns a unary server interceptor that authorizes
// each RPC by its full method name before the handler runs. For every request it
// calls authorize(ctx, info.FullMethod); a non-nil result rejects the RPC with
// that error and the handler is never reached, otherwise the RPC proceeds.
//
// The interceptor is deliberately unopinionated: it ships no policy of its own.
// The consumer supplies authorize, deciding per fullMethod (e.g.
// workflowpb.WorkflowService_ListInstances_FullMethodName) whether the caller may
// proceed — typically by reading an authenticated principal/claims the
// consumer's authentication layer placed in ctx, NOT from the request body. The
// returned error should be a grpc/status error so the client sees a meaningful
// code (e.g. codes.PermissionDenied or codes.Unauthenticated).
//
// It composes with [NewSecureServer]: pass it as the auth interceptor —
// NewSecureServer(svc, NewMethodAuthInterceptor(policy)) — to install a
// fail-closed, per-method gate in front of every RPC. To run it alongside other
// interceptors, pre-combine them with grpc.ChainUnaryInterceptor.
//
// Actor identity: handlers that act on behalf of a principal (ClaimTask,
// CompleteTask, ReassignTask) must derive the actor from the authenticated
// identity in ctx rather than trusting a client-supplied actor field. The
// authorize callback is the place to authenticate and stash that principal in
// the returned context (return the derived ctx via context.WithValue inside a
// wrapping interceptor if needed); this interceptor itself only gates.
//
// It panics if authorize is nil: a nil callback would build a fail-OPEN gate
// that nil-derefs on the first RPC, so the constructor fails fast at wiring time
// (mirroring [NewSecureServer], which panics on a nil interceptor).
func NewMethodAuthInterceptor(authorize func(ctx context.Context, fullMethod string) error) grpc.UnaryServerInterceptor {
	if authorize == nil {
		panic("workflow-grpc: NewMethodAuthInterceptor: authorize callback must not be nil")
	}
	return func(
		ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler,
	) (any, error) {
		if err := authorize(ctx, info.FullMethod); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

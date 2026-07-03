// Package grpctransport provides a gRPC transport adapter for the workflow
// engine's Service facade. Consumers register the WorkflowService on their own
// grpc.Server via RegisterWorkflowServiceServer; this package never owns a listener.
//
// To regenerate workflowpb from the proto file:
//
//	go generate ./transport/grpc/...
//
//go:generate sh -c "export GOPATH=$(go env GOPATH) && export PATH=$PATH:$GOPATH/bin && protoc --proto_path=proto --go_out=workflowpb --go_opt=paths=source_relative --go-grpc_out=workflowpb --go-grpc_opt=paths=source_relative proto/workflow.proto"
package grpctransport

import (
	"errors"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/service"
)

// errorDomain is the ErrorInfo.Domain value stamped on every structured gRPC
// error, identifying the engine module as the authority for the reason codes.
const errorDomain = "github.com/zakyalvan/krtlwrkflw"

// classifyError maps a domain error to a gRPC status code and a machine-readable
// reason code. The reason codes are identical to the REST taxonomy in
// transport/rest/errors.go so a client sees the same {code} regardless of
// transport. Sentinels are matched via errors.Is so wrapped errors classify
// correctly.
//
//   - kernel.ErrInstanceNotFound / kernel.ErrDefinitionNotFound / humantask.ErrTaskNotFound → NotFound / "not_found"
//   - authz.ErrNotAuthorized → PermissionDenied / "forbidden"
//   - kernel.ErrConcurrentUpdate → Aborted / "conflict"
//   - kernel.ErrBadCursor → InvalidArgument / "bad_request"
//   - service.ErrConflict / engine.ErrInvalidTransition → FailedPrecondition / "conflict_state"
//   - everything else → Internal / "internal_error"
func classifyError(err error) (codes.Code, string) {
	switch {
	case errors.Is(err, kernel.ErrInstanceNotFound),
		errors.Is(err, kernel.ErrDefinitionNotFound),
		errors.Is(err, humantask.ErrTaskNotFound):
		return codes.NotFound, "not_found"
	case errors.Is(err, authz.ErrNotAuthorized):
		return codes.PermissionDenied, "forbidden"
	case errors.Is(err, kernel.ErrConcurrentUpdate):
		return codes.Aborted, "conflict"
	case errors.Is(err, kernel.ErrBadCursor):
		return codes.InvalidArgument, "bad_request"
	case errors.Is(err, service.ErrConflict),
		errors.Is(err, engine.ErrInvalidTransition):
		return codes.FailedPrecondition, "conflict_state"
	default:
		return codes.Internal, "internal_error"
	}
}

// reasonInvalidArgument is the ErrorInfo.Reason stamped on transport-boundary
// validation failures (the codes.InvalidArgument sweep from ADR-0058). It is a
// stable, machine-readable code for "the request was malformed" so a client
// branches on the reason rather than parsing the human-readable status message.
const reasonInvalidArgument = "invalid_argument"

// statusWithReason builds a gRPC status carrying a machine-readable
// errdetails.ErrorInfo detail (Reason = the given reason code, Domain = the
// engine module) so clients can branch on the code rather than parsing the
// human-readable status message. It is the single place that stamps the detail
// shape, shared by both the classified-error path (mapToGRPCStatus) and the
// transport-boundary validation path (invalidArg).
//
// If attaching the detail fails (it should not, for a freshly built status), the
// status without the detail is returned — a degraded but still-valid error.
func statusWithReason(code codes.Code, reason, msg string) error {
	st := status.New(code, msg)

	withDetail, detailErr := st.WithDetails(&errdetails.ErrorInfo{
		Reason: reason,
		Domain: errorDomain,
	})
	if detailErr != nil {
		return st.Err()
	}
	return withDetail.Err()
}

// mapToGRPCStatus classifies a domain error into the appropriate gRPC status and
// attaches a machine-readable errdetails.ErrorInfo detail (Reason = the REST
// taxonomy code, Domain = the engine module) so clients can branch on the code
// rather than parsing the human-readable status message.
//
// If attaching the detail fails (it should not, for a freshly built status), the
// status without the detail is returned — a degraded but still-valid error.
func mapToGRPCStatus(err error) error {
	code, reason := classifyError(err)
	return statusWithReason(code, reason, err.Error())
}

// MapToGRPCStatus is the exported version of mapToGRPCStatus exposed for
// testing. It forwards to the unexported implementation.
func MapToGRPCStatus(err error) error {
	return mapToGRPCStatus(err)
}

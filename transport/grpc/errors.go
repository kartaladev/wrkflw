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

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/service"
)

// mapToGRPCStatus classifies a domain error into the appropriate gRPC status.
// Domain sentinels are matched via errors.Is so wrapped errors are handled correctly.
//
//   - runtime.ErrInstanceNotFound / runtime.ErrDefinitionNotFound / humantask.ErrTaskNotFound → codes.NotFound
//   - authz.ErrNotAuthorized → codes.PermissionDenied
//   - runtime.ErrConcurrentUpdate → codes.Aborted
//   - runtime.ErrBadCursor → codes.InvalidArgument
//   - service.ErrConflict → codes.FailedPrecondition
//   - everything else → codes.Internal
func mapToGRPCStatus(err error) error {
	switch {
	case errors.Is(err, runtime.ErrInstanceNotFound),
		errors.Is(err, runtime.ErrDefinitionNotFound),
		errors.Is(err, humantask.ErrTaskNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, authz.ErrNotAuthorized):
		return status.Error(codes.PermissionDenied, err.Error())
	case errors.Is(err, runtime.ErrConcurrentUpdate):
		return status.Error(codes.Aborted, err.Error())
	case errors.Is(err, runtime.ErrBadCursor):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, service.ErrConflict):
		return status.Error(codes.FailedPrecondition, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}

// MapToGRPCStatus is the exported version of mapToGRPCStatus exposed for
// testing. It forwards to the unexported implementation.
func MapToGRPCStatus(err error) error {
	return mapToGRPCStatus(err)
}

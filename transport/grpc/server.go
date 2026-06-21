package grpctransport

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/service"
	"github.com/zakyalvan/krtlwrkflw/transport/grpc/workflowpb"
)

// server is the gRPC service implementation. It delegates all operations to the
// service.Service facade and converts between proto and service types.
type server struct {
	workflowpb.UnimplementedWorkflowServiceServer
	svc service.Service
}

// RegisterWorkflowServiceServer constructs a WorkflowService gRPC implementation
// and registers it with the given grpc.ServiceRegistrar. The consumer owns the
// grpc.Server; this package never creates or starts one.
func RegisterWorkflowServiceServer(reg grpc.ServiceRegistrar, svc service.Service) {
	workflowpb.RegisterWorkflowServiceServer(reg, &server{svc: svc})
}

// ---- RPC implementations ----

// StartInstance creates a new process instance.
func (s *server) StartInstance(ctx context.Context, req *workflowpb.StartInstanceRequest) (*workflowpb.InstanceResponse, error) {
	st, err := s.svc.StartInstance(ctx, service.StartInstanceRequest{
		DefRef:     req.GetDefRef(),
		InstanceID: req.GetInstanceId(),
		Vars:       structToMap(req.GetVars()),
	})
	if err != nil {
		return nil, mapToGRPCStatus(err)
	}
	proto, err := instanceToProto(st)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "response serialization: %s", err)
	}
	return &workflowpb.InstanceResponse{Instance: proto}, nil
}

// GetInstance returns the current state of an instance.
func (s *server) GetInstance(ctx context.Context, req *workflowpb.GetInstanceRequest) (*workflowpb.InstanceResponse, error) {
	st, err := s.svc.GetInstance(ctx, req.GetInstanceId())
	if err != nil {
		return nil, mapToGRPCStatus(err)
	}
	proto, err := instanceToProto(st)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "response serialization: %s", err)
	}
	return &workflowpb.InstanceResponse{Instance: proto}, nil
}

// DeliverSignal resumes a parked instance with a named signal.
func (s *server) DeliverSignal(ctx context.Context, req *workflowpb.DeliverSignalRequest) (*workflowpb.InstanceResponse, error) {
	st, err := s.svc.DeliverSignal(ctx, service.DeliverSignalRequest{
		InstanceID: req.GetInstanceId(),
		Signal:     req.GetSignal(),
		Payload:    structToMap(req.GetPayload()),
	})
	if err != nil {
		return nil, mapToGRPCStatus(err)
	}
	proto, err := instanceToProto(st)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "response serialization: %s", err)
	}
	return &workflowpb.InstanceResponse{Instance: proto}, nil
}

// DeliverMessage routes a message to a waiting instance.
func (s *server) DeliverMessage(ctx context.Context, req *workflowpb.DeliverMessageRequest) (*workflowpb.DeliverMessageResponse, error) {
	err := s.svc.DeliverMessage(ctx, service.DeliverMessageRequest{
		DefRef:         req.GetDefRef(),
		Name:           req.GetName(),
		CorrelationKey: req.GetCorrelationKey(),
		Payload:        structToMap(req.GetPayload()),
	})
	if err != nil {
		return nil, mapToGRPCStatus(err)
	}
	return &workflowpb.DeliverMessageResponse{}, nil
}

// ClaimTask authorizes and claims a human task.
func (s *server) ClaimTask(ctx context.Context, req *workflowpb.ClaimTaskRequest) (*workflowpb.InstanceResponse, error) {
	st, err := s.svc.ClaimTask(ctx, service.ClaimTaskRequest{
		TaskToken: req.GetTaskToken(),
		Actor:     protoToActor(req.GetActor()),
	})
	if err != nil {
		return nil, mapToGRPCStatus(err)
	}
	proto, err := instanceToProto(st)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "response serialization: %s", err)
	}
	return &workflowpb.InstanceResponse{Instance: proto}, nil
}

// CompleteTask authorizes and completes a human task.
func (s *server) CompleteTask(ctx context.Context, req *workflowpb.CompleteTaskRequest) (*workflowpb.InstanceResponse, error) {
	st, err := s.svc.CompleteTask(ctx, service.CompleteTaskRequest{
		TaskToken: req.GetTaskToken(),
		Actor:     protoToActor(req.GetActor()),
		Output:    structToMap(req.GetOutput()),
	})
	if err != nil {
		return nil, mapToGRPCStatus(err)
	}
	proto, err := instanceToProto(st)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "response serialization: %s", err)
	}
	return &workflowpb.InstanceResponse{Instance: proto}, nil
}

// ReassignTask authorizes and reassigns a human task.
func (s *server) ReassignTask(ctx context.Context, req *workflowpb.ReassignTaskRequest) (*workflowpb.InstanceResponse, error) {
	st, err := s.svc.ReassignTask(ctx, service.ReassignTaskRequest{
		TaskToken: req.GetTaskToken(),
		From:      req.GetFrom(),
		To:        req.GetTo(),
		By:        protoToActor(req.GetBy()),
	})
	if err != nil {
		return nil, mapToGRPCStatus(err)
	}
	proto, err := instanceToProto(st)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "response serialization: %s", err)
	}
	return &workflowpb.InstanceResponse{Instance: proto}, nil
}

// ListInstances returns a paginated list of instance summaries.
func (s *server) ListInstances(ctx context.Context, req *workflowpb.ListInstancesRequest) (*workflowpb.ListInstancesResponse, error) {
	filter := runtime.InstanceFilter{
		Limit:  int(req.GetLimit()),
		Cursor: req.GetCursor(),
	}
	if st := req.GetStatus(); st != "" {
		parsed, err := parseStatus(st)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "unknown status filter %q", st)
		}
		filter.Status = &parsed
	}

	page, err := s.svc.ListInstances(ctx, filter)
	if err != nil {
		return nil, mapToGRPCStatus(err)
	}

	items := make([]*workflowpb.InstanceSummary, len(page.Items))
	for i, item := range page.Items {
		items[i] = summaryToProto(item)
	}

	return &workflowpb.ListInstancesResponse{
		Items:      items,
		NextCursor: page.NextCursor,
		HasMore:    page.HasMore,
	}, nil
}

// ---- Conversion helpers ----

// toStruct converts a map[string]any to *structpb.Struct, returning an error
// when the map contains a value that cannot be represented as a proto Struct
// (e.g. time.Time, []byte, channel, or other non-JSON-compatible types).
// A nil map returns (nil, nil).
func toStruct(m map[string]any) (*structpb.Struct, error) {
	if m == nil {
		return nil, nil
	}
	s, err := structpb.NewStruct(m)
	if err != nil {
		return nil, fmt.Errorf("structpb conversion: %w", err)
	}
	return s, nil
}

// instanceToProto converts an engine.InstanceState to a workflowpb.Instance.
// Returns an error when the instance's Variables map contains a value that
// cannot be serialized to a proto Struct.
func instanceToProto(st engine.InstanceState) (*workflowpb.Instance, error) {
	vars, err := toStruct(st.Variables)
	if err != nil {
		return nil, err
	}
	inst := &workflowpb.Instance{
		InstanceId: st.InstanceID,
		DefId:      st.DefID,
		DefVersion: int32(st.DefVersion),
		Status:     statusToString(st.Status),
		StartedAt:  timestamppb.New(st.StartedAt),
		Variables:  vars,
	}
	if st.EndedAt != nil {
		inst.EndedAt = timestamppb.New(*st.EndedAt)
	}
	return inst, nil
}

// summaryToProto converts a runtime.InstanceSummary to a workflowpb.InstanceSummary.
func summaryToProto(s runtime.InstanceSummary) *workflowpb.InstanceSummary {
	sum := &workflowpb.InstanceSummary{
		InstanceId: s.InstanceID,
		DefId:      s.DefID,
		DefVersion: int32(s.DefVersion),
		Status:     statusToString(s.Status),
		StartedAt:  timestamppb.New(s.StartedAt),
	}
	if s.EndedAt != nil {
		sum.EndedAt = timestamppb.New(*s.EndedAt)
	}
	return sum
}

// structToMap converts a *structpb.Struct to a map[string]any. Nil input returns nil.
func structToMap(s *structpb.Struct) map[string]any {
	if s == nil {
		return nil
	}
	return s.AsMap()
}

// protoToActor converts a *workflowpb.Actor to an authz.Actor. Nil input returns zero value.
func protoToActor(a *workflowpb.Actor) authz.Actor {
	if a == nil {
		return authz.Actor{}
	}
	var attrs map[string]any
	if a.GetAttributes() != nil {
		attrs = a.GetAttributes().AsMap()
	}
	return authz.Actor{
		ID:         a.GetId(),
		Roles:      a.GetRoles(),
		Attributes: attrs,
	}
}

// statusToString converts an engine.Status to the canonical string representation
// used by both the REST and gRPC transports.
func statusToString(s engine.Status) string {
	switch s {
	case engine.StatusRunning:
		return "running"
	case engine.StatusCompleted:
		return "completed"
	case engine.StatusFailed:
		return "failed"
	case engine.StatusCompensating:
		return "compensating"
	case engine.StatusTerminated:
		return "terminated"
	default:
		return "unknown"
	}
}

// errUnknownStatus is returned by parseStatus when the input string does not
// match any known engine.Status value.
var errUnknownStatus = errors.New("unknown status")

// parseStatus parses a status string to engine.Status. An unrecognised
// non-empty string returns (0, errUnknownStatus); callers should surface that
// as codes.InvalidArgument. An empty string is not a valid input; callers
// must guard on that before calling parseStatus.
func parseStatus(s string) (engine.Status, error) {
	switch s {
	case "running":
		return engine.StatusRunning, nil
	case "completed":
		return engine.StatusCompleted, nil
	case "failed":
		return engine.StatusFailed, nil
	case "compensating":
		return engine.StatusCompensating, nil
	case "terminated":
		return engine.StatusTerminated, nil
	default:
		return 0, fmt.Errorf("%w: %q", errUnknownStatus, s)
	}
}

// Compile-time assertion: *server satisfies the generated interface.
var _ workflowpb.WorkflowServiceServer = (*server)(nil)

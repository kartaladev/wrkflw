package grpctransport_test

import (
	"context"
	"fmt"
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
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/service"
	grpctransport "github.com/zakyalvan/krtlwrkflw/transport/grpc"
	"github.com/zakyalvan/krtlwrkflw/transport/grpc/workflowpb"
)

const bufSize = 1024 * 1024

// grpcHarness wires an in-memory engine and stands up a bufconn gRPC server.
type grpcHarness struct {
	client workflowpb.WorkflowServiceClient
	svc    service.Service
	// expose lister for seeding
	store *runtime.MemStore
}

// greetAction is a minimal service action used in linear-def tests.
type serverTestGreetAction struct{}

func (serverTestGreetAction) Do(_ context.Context, in map[string]any) (map[string]any, error) {
	name, _ := in["name"].(string)
	return map[string]any{"greeting": "hi " + name}, nil
}

// linearDef is a simple start → serviceTask(greet) → end definition.
func serverLinearDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "greeting",
		Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "greet", Kind: model.KindServiceTask, Action: "greet"},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "greet"},
			{ID: "f2", Source: "greet", Target: "end"},
		},
	}
}

// approvalDef returns start → userTask("approve", role "manager") → end.
func serverApprovalDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "approval",
		Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "approve", Kind: model.KindUserTask, CandidateRoles: []string{"manager"}},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "approve"},
			{ID: "f2", Source: "approve", Target: "end"},
		},
	}
}

// signalDef returns start → signal-catch(name) → end.
func serverSignalDef(signalName string) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "signal-catch-" + signalName,
		Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "wait-signal", Kind: model.KindIntermediateCatchEvent, SignalName: signalName},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "wait-signal"},
			{ID: "f2", Source: "wait-signal", Target: "end"},
		},
	}
}

func defRefFor(def *model.ProcessDefinition) string {
	return fmt.Sprintf("%s:%d", def.ID, def.Version)
}

// serverMessageDef returns start → message-catch(msgName, orderId) → end.
func serverMessageDef(msgName string) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "message-catch-" + msgName,
		Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "wait-msg", Kind: model.KindIntermediateCatchEvent, MessageName: msgName, CorrelationKey: "orderId"},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "wait-msg"},
			{ID: "f2", Source: "wait-msg", Target: "end"},
		},
	}
}

// mustStruct converts a map to a *structpb.Struct, panicking on error (test helper only).
func mustStruct(m map[string]any) *structpb.Struct {
	s, err := structpb.NewStruct(m)
	if err != nil {
		panic(fmt.Sprintf("mustStruct: %v", err))
	}
	return s
}

// newGRPCHarness sets up the service + gRPC server over bufconn.
func newGRPCHarness(t *testing.T, defs ...*model.ProcessDefinition) *grpcHarness {
	t.Helper()

	fc := clockwork.NewFakeClock()
	taskStore := humantask.NewMemTaskStore()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{
		"manager": {{ID: "alice", Roles: []string{"manager"}}},
	})
	az := authz.RoleAuthorizer{}
	store := runtime.NewMemStore()

	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"greet": serverTestGreetAction{},
	})

	runner := runtime.NewRunner(cat, fc, store, runtime.WithHumanTasks(resolver, taskStore, az))

	defsMap := make(map[string]*model.ProcessDefinition, len(defs)*2)
	for _, d := range defs {
		defsMap[defRefFor(d)] = d
		defsMap[d.ID] = d
	}
	reg := runtime.NewMapDefinitionRegistry(defsMap)
	tasks := runtime.NewTaskService(taskStore, az, fc)

	svc := service.New(runner, tasks, reg, store, store, taskStore, fc)

	// Stand up bufconn server.
	lis := bufconn.Listen(bufSize)
	grpcServer := grpc.NewServer()
	grpctransport.RegisterWorkflowServiceServer(grpcServer, svc)

	t.Cleanup(func() { grpcServer.Stop() })
	go func() { _ = grpcServer.Serve(lis) }()

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	client := workflowpb.NewWorkflowServiceClient(conn)
	return &grpcHarness{client: client, svc: svc, store: store}
}

// ---- Tests ----

// TestStartInstanceGetInstance verifies the start → get round-trip.
func TestStartInstanceGetInstance(t *testing.T) {
	t.Parallel()
	h := newGRPCHarness(t, serverLinearDef())

	ctx := t.Context()
	vars, err := structpb.NewStruct(map[string]any{"name": "world"})
	require.NoError(t, err)

	startResp, err := h.client.StartInstance(ctx, &workflowpb.StartInstanceRequest{
		DefRef:     "greeting",
		InstanceId: "grpc-inst-1",
		Vars:       vars,
	})
	require.NoError(t, err)
	require.NotNil(t, startResp.Instance)
	assert.Equal(t, "grpc-inst-1", startResp.Instance.InstanceId)
	assert.Equal(t, "completed", startResp.Instance.Status)

	getResp, err := h.client.GetInstance(ctx, &workflowpb.GetInstanceRequest{
		InstanceId: "grpc-inst-1",
	})
	require.NoError(t, err)
	assert.Equal(t, "grpc-inst-1", getResp.Instance.InstanceId)
	assert.Equal(t, "completed", getResp.Instance.Status)
}

// TestGetInstanceNotFound verifies that an unknown instance ID returns codes.NotFound.
func TestGetInstanceNotFound(t *testing.T) {
	t.Parallel()
	h := newGRPCHarness(t)

	_, err := h.client.GetInstance(t.Context(), &workflowpb.GetInstanceRequest{
		InstanceId: "no-such-id",
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

// TestDeliverSignalResumesInstance verifies that DeliverSignal resumes a parked instance.
func TestDeliverSignalResumesInstance(t *testing.T) {
	t.Parallel()
	def := serverSignalDef("approved")
	h := newGRPCHarness(t, def)
	ctx := t.Context()

	// Start and park at signal-catch.
	startResp, err := h.client.StartInstance(ctx, &workflowpb.StartInstanceRequest{
		DefRef:     "signal-catch-approved",
		InstanceId: "sig-inst-1",
	})
	require.NoError(t, err)
	assert.Equal(t, "running", startResp.Instance.Status)

	// Deliver the signal.
	payload, err := structpb.NewStruct(map[string]any{"decision": "yes"})
	require.NoError(t, err)

	sigResp, err := h.client.DeliverSignal(ctx, &workflowpb.DeliverSignalRequest{
		InstanceId: "sig-inst-1",
		Signal:     "approved",
		Payload:    payload,
	})
	require.NoError(t, err)
	assert.Equal(t, "completed", sigResp.Instance.Status)
}

// TestClaimTaskAuthorized verifies a manager can claim a task.
func TestClaimTaskAuthorized(t *testing.T) {
	t.Parallel()
	def := serverApprovalDef()
	h := newGRPCHarness(t, def)
	ctx := t.Context()

	// Start and park at user task.
	startResp, err := h.client.StartInstance(ctx, &workflowpb.StartInstanceRequest{
		DefRef:     "approval",
		InstanceId: "approval-inst-1",
	})
	require.NoError(t, err)
	assert.Equal(t, "running", startResp.Instance.Status)

	// Extract the task token.
	inst, err := h.svc.GetInstance(ctx, "approval-inst-1")
	require.NoError(t, err)
	require.Len(t, inst.Tokens, 1)
	taskToken := inst.Tokens[0].AwaitCommand
	require.NotEmpty(t, taskToken)

	claimResp, err := h.client.ClaimTask(ctx, &workflowpb.ClaimTaskRequest{
		TaskToken: taskToken,
		Actor: &workflowpb.Actor{
			Id:    "alice",
			Roles: []string{"manager"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "approval-inst-1", claimResp.Instance.InstanceId)
	assert.Equal(t, "running", claimResp.Instance.Status)
}

// TestClaimTaskUnauthorized verifies that an ineligible actor gets codes.PermissionDenied.
func TestClaimTaskUnauthorized(t *testing.T) {
	t.Parallel()
	def := serverApprovalDef()
	h := newGRPCHarness(t, def)
	ctx := t.Context()

	// Start and park.
	_, err := h.client.StartInstance(ctx, &workflowpb.StartInstanceRequest{
		DefRef:     "approval",
		InstanceId: "approval-inst-unauth",
	})
	require.NoError(t, err)

	inst, err := h.svc.GetInstance(ctx, "approval-inst-unauth")
	require.NoError(t, err)
	require.Len(t, inst.Tokens, 1)
	taskToken := inst.Tokens[0].AwaitCommand

	_, err = h.client.ClaimTask(ctx, &workflowpb.ClaimTaskRequest{
		TaskToken: taskToken,
		Actor: &workflowpb.Actor{
			Id:    "bob",
			Roles: []string{"viewer"},
		},
	})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

// TestCompleteAndReassignTask verifies CompleteTask and ReassignTask flows.
func TestCompleteAndReassignTask(t *testing.T) {
	t.Parallel()
	def := serverApprovalDef()
	h := newGRPCHarness(t, def)
	ctx := t.Context()

	// Start and park at user task.
	_, err := h.client.StartInstance(ctx, &workflowpb.StartInstanceRequest{
		DefRef:     "approval",
		InstanceId: "approve-complete-1",
	})
	require.NoError(t, err)

	inst, err := h.svc.GetInstance(ctx, "approve-complete-1")
	require.NoError(t, err)
	require.Len(t, inst.Tokens, 1)
	taskToken := inst.Tokens[0].AwaitCommand

	manager := &workflowpb.Actor{Id: "alice", Roles: []string{"manager"}}

	// Claim the task.
	_, err = h.client.ClaimTask(ctx, &workflowpb.ClaimTaskRequest{
		TaskToken: taskToken,
		Actor:     manager,
	})
	require.NoError(t, err)

	// Reassign alice → carol (same-role manager reassigning is authorized).
	reassignResp, err := h.client.ReassignTask(ctx, &workflowpb.ReassignTaskRequest{
		TaskToken: taskToken,
		From:      "alice",
		To:        "carol",
		By:        manager,
	})
	require.NoError(t, err)
	assert.Equal(t, "running", reassignResp.Instance.Status)

	// Complete the task.
	output, err := structpb.NewStruct(map[string]any{"approved": true})
	require.NoError(t, err)

	completeResp, err := h.client.CompleteTask(ctx, &workflowpb.CompleteTaskRequest{
		TaskToken: taskToken,
		Actor:     manager,
		Output:    output,
	})
	require.NoError(t, err)
	assert.Equal(t, "completed", completeResp.Instance.Status)
}

// TestDeliverMessage verifies DeliverMessage routes a message to a waiting instance.
func TestDeliverMessage(t *testing.T) {
	t.Parallel()
	def := serverMessageDef("order-shipped")
	h := newGRPCHarness(t, def)
	ctx := t.Context()

	// Start and park at message-catch.
	_, err := h.client.StartInstance(ctx, &workflowpb.StartInstanceRequest{
		DefRef:     "message-catch-order-shipped",
		InstanceId: "order-100",
		Vars:       mustStruct(map[string]any{"orderId": "100"}),
	})
	require.NoError(t, err)

	// Deliver the message.
	_, err = h.client.DeliverMessage(ctx, &workflowpb.DeliverMessageRequest{
		DefRef:         fmt.Sprintf("%s:%d", def.ID, def.Version),
		Name:           "order-shipped",
		CorrelationKey: "100",
		Payload:        mustStruct(map[string]any{"shipped": true}),
	})
	require.NoError(t, err)

	// Instance must be completed now.
	getResp, err := h.client.GetInstance(ctx, &workflowpb.GetInstanceRequest{InstanceId: "order-100"})
	require.NoError(t, err)
	assert.Equal(t, "completed", getResp.Instance.Status)
}

// TestDeliverMessageUnknownDef verifies DeliverMessage returns NotFound for unknown DefRef.
func TestDeliverMessageUnknownDef(t *testing.T) {
	t.Parallel()
	h := newGRPCHarness(t)

	_, err := h.client.DeliverMessage(t.Context(), &workflowpb.DeliverMessageRequest{
		DefRef:         "no-such-def:1",
		Name:           "some-msg",
		CorrelationKey: "key",
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

// TestStartInstanceUnknownDef verifies that StartInstance returns NotFound for unknown DefRef.
func TestStartInstanceUnknownDef(t *testing.T) {
	t.Parallel()
	h := newGRPCHarness(t)

	_, err := h.client.StartInstance(t.Context(), &workflowpb.StartInstanceRequest{
		DefRef:     "no-such-def",
		InstanceId: "inst-x",
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

// TestListInstancesWithStatusFilter verifies ListInstances with a status filter.
func TestListInstancesWithStatusFilter(t *testing.T) {
	t.Parallel()
	h := newGRPCHarness(t, serverLinearDef())
	ctx := t.Context()

	_, err := h.client.StartInstance(ctx, &workflowpb.StartInstanceRequest{
		DefRef:     "greeting",
		InstanceId: "filter-inst-1",
		Vars:       mustStruct(map[string]any{"name": "test"}),
	})
	require.NoError(t, err)

	// Filter by "completed" — should find our instance.
	listResp, err := h.client.ListInstances(ctx, &workflowpb.ListInstancesRequest{
		Status: "completed",
		Limit:  10,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, listResp.Items)
}

// TestListInstances verifies that ListInstances returns seeded items and pagination info.
func TestListInstances(t *testing.T) {
	t.Parallel()
	h := newGRPCHarness(t, serverLinearDef())
	ctx := t.Context()

	vars, err := structpb.NewStruct(map[string]any{"name": "a"})
	require.NoError(t, err)

	_, err = h.client.StartInstance(ctx, &workflowpb.StartInstanceRequest{
		DefRef:     "greeting",
		InstanceId: "list-inst-1",
		Vars:       vars,
	})
	require.NoError(t, err)

	vars2, err := structpb.NewStruct(map[string]any{"name": "b"})
	require.NoError(t, err)
	_, err = h.client.StartInstance(ctx, &workflowpb.StartInstanceRequest{
		DefRef:     "greeting",
		InstanceId: "list-inst-2",
		Vars:       vars2,
	})
	require.NoError(t, err)

	listResp, err := h.client.ListInstances(ctx, &workflowpb.ListInstancesRequest{
		Limit: 10,
	})
	require.NoError(t, err)
	assert.Len(t, listResp.Items, 2)
	assert.False(t, listResp.HasMore)
}

// TestDeliverSignalError verifies that DeliverSignal maps ErrInstanceNotFound → NotFound.
func TestDeliverSignalError(t *testing.T) {
	t.Parallel()
	def := serverSignalDef("approved")
	h := newGRPCHarness(t, def)

	_, err := h.client.DeliverSignal(t.Context(), &workflowpb.DeliverSignalRequest{
		InstanceId: "no-such-instance",
		Signal:     "approved",
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

// TestCompleteTaskUnauthorized verifies that CompleteTask maps ErrNotAuthorized → PermissionDenied.
func TestCompleteTaskUnauthorized(t *testing.T) {
	t.Parallel()
	def := serverApprovalDef()
	h := newGRPCHarness(t, def)
	ctx := t.Context()

	_, err := h.client.StartInstance(ctx, &workflowpb.StartInstanceRequest{
		DefRef:     "approval",
		InstanceId: "approve-unauth-complete",
	})
	require.NoError(t, err)

	inst, err := h.svc.GetInstance(ctx, "approve-unauth-complete")
	require.NoError(t, err)
	require.Len(t, inst.Tokens, 1)
	taskToken := inst.Tokens[0].AwaitCommand

	_, err = h.client.CompleteTask(ctx, &workflowpb.CompleteTaskRequest{
		TaskToken: taskToken,
		Actor:     &workflowpb.Actor{Id: "bob", Roles: []string{"viewer"}},
	})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

// TestReassignTaskUnauthorized verifies that ReassignTask maps ErrNotAuthorized → PermissionDenied.
func TestReassignTaskUnauthorized(t *testing.T) {
	t.Parallel()
	def := serverApprovalDef()
	h := newGRPCHarness(t, def)
	ctx := t.Context()

	_, err := h.client.StartInstance(ctx, &workflowpb.StartInstanceRequest{
		DefRef:     "approval",
		InstanceId: "reassign-unauth-1",
	})
	require.NoError(t, err)

	inst, err := h.svc.GetInstance(ctx, "reassign-unauth-1")
	require.NoError(t, err)
	require.Len(t, inst.Tokens, 1)
	taskToken := inst.Tokens[0].AwaitCommand

	manager := &workflowpb.Actor{Id: "alice", Roles: []string{"manager"}}
	_, err = h.client.ClaimTask(ctx, &workflowpb.ClaimTaskRequest{
		TaskToken: taskToken,
		Actor:     manager,
	})
	require.NoError(t, err)

	_, err = h.client.ReassignTask(ctx, &workflowpb.ReassignTaskRequest{
		TaskToken: taskToken,
		From:      "alice",
		To:        "carol",
		By:        &workflowpb.Actor{Id: "dave", Roles: []string{"viewer"}},
	})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

// TestClaimTaskWithActorAttributes verifies ClaimTask passes actor attributes correctly.
func TestClaimTaskWithActorAttributes(t *testing.T) {
	t.Parallel()
	def := serverApprovalDef()
	h := newGRPCHarness(t, def)
	ctx := t.Context()

	_, err := h.client.StartInstance(ctx, &workflowpb.StartInstanceRequest{
		DefRef:     "approval",
		InstanceId: "approval-attrs-1",
	})
	require.NoError(t, err)

	inst, err := h.svc.GetInstance(ctx, "approval-attrs-1")
	require.NoError(t, err)
	require.Len(t, inst.Tokens, 1)
	taskToken := inst.Tokens[0].AwaitCommand

	// Actor with attributes — manager role still authorizes the claim.
	attrs := mustStruct(map[string]any{"department": "ops"})
	_, err = h.client.ClaimTask(ctx, &workflowpb.ClaimTaskRequest{
		TaskToken: taskToken,
		Actor: &workflowpb.Actor{
			Id:         "alice",
			Roles:      []string{"manager"},
			Attributes: attrs,
		},
	})
	require.NoError(t, err)
}

// stubService is a minimal service.Service implementation that returns a
// pre-configured InstanceState from GetInstance, allowing the test to inject
// non-JSON-serializable values into Variables.
type stubService struct {
	service.Service // embed to satisfy interface; only GetInstance is overridden
	state           engine.InstanceState
}

func (s *stubService) GetInstance(_ context.Context, _ string) (engine.InstanceState, error) {
	return s.state, nil
}

// newStubHarness stands up a bufconn gRPC server backed by a stubService.
func newStubHarness(t *testing.T, svc service.Service) workflowpb.WorkflowServiceClient {
	t.Helper()

	lis := bufconn.Listen(bufSize)
	grpcServer := grpc.NewServer()
	grpctransport.RegisterWorkflowServiceServer(grpcServer, svc)

	t.Cleanup(func() { grpcServer.Stop() })
	go func() { _ = grpcServer.Serve(lis) }()

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return workflowpb.NewWorkflowServiceClient(conn)
}

// TestGetInstanceNonSerializableVariablesReturnsInternal verifies that when
// the service returns an InstanceState whose Variables map contains a value
// that cannot be represented as protobuf Struct (e.g. a channel), GetInstance
// returns codes.Internal instead of silently dropping the field.
func TestGetInstanceNonSerializableVariablesReturnsInternal(t *testing.T) {
	t.Parallel()

	stub := &stubService{
		state: engine.InstanceState{
			InstanceID: "stub-inst-1",
			DefID:      "stub-def",
			DefVersion: 1,
			Status:     engine.StatusRunning,
			Variables:  map[string]any{"ch": make(chan int)}, // non-JSON-serializable
		},
	}

	client := newStubHarness(t, stub)

	_, err := client.GetInstance(t.Context(), &workflowpb.GetInstanceRequest{
		InstanceId: "stub-inst-1",
	})
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

// TestListInstancesUnknownStatusReturnsInvalidArgument verifies that providing
// an unrecognized status filter string returns codes.InvalidArgument instead of
// silently defaulting to running instances.
func TestListInstancesUnknownStatusReturnsInvalidArgument(t *testing.T) {
	t.Parallel()
	h := newGRPCHarness(t, serverLinearDef())

	_, err := h.client.ListInstances(t.Context(), &workflowpb.ListInstancesRequest{
		Status: "junk",
		Limit:  10,
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

// TestListInstancesEmptyStatusReturnsAll verifies that an empty status filter
// returns all instances regardless of their status.
func TestListInstancesEmptyStatusReturnsAll(t *testing.T) {
	t.Parallel()
	h := newGRPCHarness(t, serverLinearDef())
	ctx := t.Context()

	// Start one instance (it will complete immediately for the linear def).
	_, err := h.client.StartInstance(ctx, &workflowpb.StartInstanceRequest{
		DefRef:     "greeting",
		InstanceId: "empty-filter-inst-1",
		Vars:       mustStruct(map[string]any{"name": "x"}),
	})
	require.NoError(t, err)

	// Empty status — no filter, should return all instances.
	listResp, err := h.client.ListInstances(ctx, &workflowpb.ListInstancesRequest{
		Limit: 10,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, listResp.Items)
}

// TestListInstancesCompletedStatusFiltersCorrectly verifies that status:"completed"
// correctly returns only completed instances.
func TestListInstancesCompletedStatusFiltersCorrectly(t *testing.T) {
	t.Parallel()
	h := newGRPCHarness(t, serverLinearDef())
	ctx := t.Context()

	_, err := h.client.StartInstance(ctx, &workflowpb.StartInstanceRequest{
		DefRef:     "greeting",
		InstanceId: "completed-filter-inst-1",
		Vars:       mustStruct(map[string]any{"name": "y"}),
	})
	require.NoError(t, err)

	listResp, err := h.client.ListInstances(ctx, &workflowpb.ListInstancesRequest{
		Status: "completed",
		Limit:  10,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, listResp.Items)
	for _, item := range listResp.Items {
		assert.Equal(t, "completed", item.Status)
	}
}

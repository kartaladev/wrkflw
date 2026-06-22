package grpctransport_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/service"
	grpctransport "github.com/zakyalvan/krtlwrkflw/transport/grpc"
	"github.com/zakyalvan/krtlwrkflw/transport/grpc/workflowpb"
)

// dlaStub is a configurable service.DeadLetterAdmin test double.
type dlaStub struct {
	listFn    func(ctx context.Context, limit int) ([]runtime.DeadLetter, error)
	redriveFn func(ctx context.Context, ids ...int64) (int, error)
	gotLimit  int
	gotIDs    []int64
}

func (s *dlaStub) ListDeadLettered(ctx context.Context, limit int) ([]runtime.DeadLetter, error) {
	s.gotLimit = limit
	return s.listFn(ctx, limit)
}

func (s *dlaStub) Redrive(ctx context.Context, ids ...int64) (int, error) {
	s.gotIDs = ids
	return s.redriveFn(ctx, ids...)
}

// newStubHarnessWithOpts is newStubHarness with transport options.
func newStubHarnessWithOpts(t *testing.T, svc service.Service, opts ...grpctransport.Option) workflowpb.WorkflowServiceClient {
	t.Helper()
	lis := bufconn.Listen(bufSize)
	grpcServer := grpc.NewServer()
	grpctransport.RegisterWorkflowServiceServer(grpcServer, svc, opts...)
	t.Cleanup(func() { grpcServer.Stop() })
	go func() { _ = grpcServer.Serve(lis) }()
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return workflowpb.NewWorkflowServiceClient(conn)
}

func TestServerListDeadLetters(t *testing.T) {
	t.Parallel()

	t.Run("wired returns items and normalizes limit", func(t *testing.T) {
		t.Parallel()
		created := time.Now()
		dla := &dlaStub{listFn: func(_ context.Context, _ int) ([]runtime.DeadLetter, error) {
			return []runtime.DeadLetter{{ID: 7, InstanceID: "p1", Topic: "instance.completed", RetryCount: 5, LastError: "boom", CreatedAt: created}}, nil
		}}
		client := newStubHarnessWithOpts(t, &resolveStub{}, grpctransport.WithDeadLetterAdmin(dla))
		resp, err := client.ListDeadLetters(t.Context(), &workflowpb.ListDeadLettersRequest{Limit: 0})
		require.NoError(t, err)
		require.Len(t, resp.GetItems(), 1)
		assert.Equal(t, int64(7), resp.GetItems()[0].GetId())
		assert.Equal(t, "p1", resp.GetItems()[0].GetInstanceId())
		assert.Equal(t, int32(5), resp.GetItems()[0].GetRetryCount())
		assert.Equal(t, 50, dla.gotLimit) // NormalizeLimit(0) == 50
	})

	t.Run("not wired returns Unimplemented", func(t *testing.T) {
		t.Parallel()
		client := newStubHarnessWithOpts(t, &resolveStub{})
		_, err := client.ListDeadLetters(t.Context(), &workflowpb.ListDeadLettersRequest{Limit: 10})
		assert.Equal(t, codes.Unimplemented, status.Code(err))
	})

	t.Run("admin error maps to Internal", func(t *testing.T) {
		t.Parallel()
		dla := &dlaStub{listFn: func(_ context.Context, _ int) ([]runtime.DeadLetter, error) {
			return nil, errors.New("workflow-postgres: relay: list dead-lettered: boom")
		}}
		client := newStubHarnessWithOpts(t, &resolveStub{}, grpctransport.WithDeadLetterAdmin(dla))
		_, err := client.ListDeadLetters(t.Context(), &workflowpb.ListDeadLettersRequest{Limit: 10})
		assert.Equal(t, codes.Internal, status.Code(err))
	})
}

func TestServerRedriveDeadLetters(t *testing.T) {
	t.Parallel()

	t.Run("wired returns count", func(t *testing.T) {
		t.Parallel()
		dla := &dlaStub{redriveFn: func(_ context.Context, ids ...int64) (int, error) { return len(ids), nil }}
		client := newStubHarnessWithOpts(t, &resolveStub{}, grpctransport.WithDeadLetterAdmin(dla))
		resp, err := client.RedriveDeadLetters(t.Context(), &workflowpb.RedriveDeadLettersRequest{Ids: []int64{1, 2, 3}})
		require.NoError(t, err)
		assert.Equal(t, int32(3), resp.GetRedrivenCount())
		assert.Equal(t, []int64{1, 2, 3}, dla.gotIDs)
	})

	t.Run("not wired returns Unimplemented", func(t *testing.T) {
		t.Parallel()
		client := newStubHarnessWithOpts(t, &resolveStub{})
		_, err := client.RedriveDeadLetters(t.Context(), &workflowpb.RedriveDeadLettersRequest{Ids: []int64{1}})
		assert.Equal(t, codes.Unimplemented, status.Code(err))
	})

	t.Run("admin error maps to Internal", func(t *testing.T) {
		t.Parallel()
		dla := &dlaStub{redriveFn: func(_ context.Context, _ ...int64) (int, error) {
			return 0, errors.New("workflow-postgres: relay: redrive: boom")
		}}
		client := newStubHarnessWithOpts(t, &resolveStub{}, grpctransport.WithDeadLetterAdmin(dla))
		_, err := client.RedriveDeadLetters(t.Context(), &workflowpb.RedriveDeadLettersRequest{Ids: []int64{1}})
		assert.Equal(t, codes.Internal, status.Code(err))
	})
}

func TestWithDeadLetterAdminNilPanics(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() { grpctransport.WithDeadLetterAdmin(nil) })
}

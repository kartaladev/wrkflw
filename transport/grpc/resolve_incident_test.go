package grpctransport_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/service"
	"github.com/zakyalvan/krtlwrkflw/transport/grpc/workflowpb"
)

// resolveStub is a service.Service stub with a configurable ResolveIncident.
type resolveStub struct {
	service.Service
	resolveFn func(ctx context.Context, req service.ResolveIncidentRequest) (engine.InstanceState, error)
	gotReq    service.ResolveIncidentRequest
}

func (s *resolveStub) ResolveIncident(ctx context.Context, req service.ResolveIncidentRequest) (engine.InstanceState, error) {
	s.gotReq = req
	return s.resolveFn(ctx, req)
}

func TestServerResolveIncident(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		fn     func(ctx context.Context, req service.ResolveIncidentRequest) (engine.InstanceState, error)
		assert func(t *testing.T, resp *workflowpb.InstanceResponse, err error)
	}{
		{
			name: "success maps fields and returns instance",
			fn: func(_ context.Context, _ service.ResolveIncidentRequest) (engine.InstanceState, error) {
				return engine.InstanceState{InstanceID: "p1", DefID: "d", DefVersion: 1, Status: engine.StatusRunning, StartedAt: time.Now()}, nil
			},
			assert: func(t *testing.T, resp *workflowpb.InstanceResponse, err error) {
				require.NoError(t, err)
				assert.Equal(t, "p1", resp.GetInstance().GetInstanceId())
			},
		},
		{
			name: "not-found maps to NotFound",
			fn: func(_ context.Context, _ service.ResolveIncidentRequest) (engine.InstanceState, error) {
				return engine.InstanceState{}, fmt.Errorf("workflow-service: %w", runtime.ErrInstanceNotFound)
			},
			assert: func(t *testing.T, _ *workflowpb.InstanceResponse, err error) {
				assert.Equal(t, codes.NotFound, status.Code(err))
			},
		},
		{
			name: "conflict maps to FailedPrecondition",
			fn: func(_ context.Context, _ service.ResolveIncidentRequest) (engine.InstanceState, error) {
				return engine.InstanceState{}, fmt.Errorf("workflow-service: %w", service.ErrConflict)
			},
			assert: func(t *testing.T, _ *workflowpb.InstanceResponse, err error) {
				assert.Equal(t, codes.FailedPrecondition, status.Code(err))
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			stub := &resolveStub{resolveFn: tc.fn}
			client := newStubHarness(t, stub)
			resp, err := client.ResolveIncident(t.Context(), &workflowpb.ResolveIncidentRequest{
				InstanceId: "p1", IncidentId: "i1", AddAttempts: 2,
			})
			tc.assert(t, resp, err)
		})
	}
}

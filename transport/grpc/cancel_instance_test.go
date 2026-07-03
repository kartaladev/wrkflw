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
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/service"
	"github.com/zakyalvan/krtlwrkflw/transport/grpc/workflowpb"
)

// cancelStub is a service.Service stub with a configurable CancelInstance implementation.
type cancelStub struct {
	service.Service // embed to satisfy remaining interface methods
	cancelFn        func(ctx context.Context, req service.CancelInstanceRequest) (engine.InstanceState, error)
}

func (s *cancelStub) CancelInstance(ctx context.Context, req service.CancelInstanceRequest) (engine.InstanceState, error) {
	return s.cancelFn(ctx, req)
}

// okCancelStub returns a stub whose CancelInstance returns a terminated InstanceState.
func okCancelStub() service.Service {
	now := time.Now()
	ended := now
	return &cancelStub{
		cancelFn: func(_ context.Context, _ service.CancelInstanceRequest) (engine.InstanceState, error) {
			return engine.InstanceState{
				InstanceID: "p1",
				DefID:      "some-def",
				DefVersion: 1,
				Status:     engine.StatusTerminated,
				StartedAt:  now,
				EndedAt:    &ended,
			}, nil
		},
	}
}

// conflictCancelStub returns a stub whose CancelInstance returns a wrapped ErrConflict.
func conflictCancelStub() service.Service {
	return &cancelStub{
		cancelFn: func(_ context.Context, _ service.CancelInstanceRequest) (engine.InstanceState, error) {
			return engine.InstanceState{}, fmt.Errorf("%w: instance is already terminal", service.ErrConflict)
		},
	}
}

// notFoundCancelStub returns a stub whose CancelInstance returns a wrapped ErrInstanceNotFound.
func notFoundCancelStub() service.Service {
	return &cancelStub{
		cancelFn: func(_ context.Context, _ service.CancelInstanceRequest) (engine.InstanceState, error) {
			return engine.InstanceState{}, fmt.Errorf("workflow-service: cancel instance: %w", kernel.ErrInstanceNotFound)
		},
	}
}

// TestServerCancelInstance verifies the CancelInstance RPC across success and error cases.
func TestServerCancelInstance(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		svc      service.Service
		wantCode codes.Code
	}{
		{name: "success", svc: okCancelStub(), wantCode: codes.OK},
		{name: "already-terminal -> FailedPrecondition", svc: conflictCancelStub(), wantCode: codes.FailedPrecondition},
		{name: "unknown -> NotFound", svc: notFoundCancelStub(), wantCode: codes.NotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := newStubHarness(t, tc.svc)
			resp, err := client.CancelInstance(t.Context(), &workflowpb.CancelInstanceRequest{InstanceId: "p1"})
			if tc.wantCode == codes.OK {
				require.NoError(t, err)
				assert.NotNil(t, resp.GetInstance())
			} else {
				assert.Equal(t, tc.wantCode, status.Code(err))
			}
		})
	}
}

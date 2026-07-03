package rest_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/service"
	rest "github.com/zakyalvan/krtlwrkflw/transport/rest"
)

// cancelInstanceStub is a minimal service.Service stub for testing the
// POST /admin/instances/{id}/cancel route.
// It embeds service.Service to satisfy the full interface (panics on any
// method not overridden, which is fine — these tests only exercise CancelInstance).
type cancelInstanceStub struct {
	service.Service
	state engine.InstanceState
	err   error
}

func (s *cancelInstanceStub) CancelInstance(_ context.Context, _ service.CancelInstanceRequest) (engine.InstanceState, error) {
	return s.state, s.err
}

func okCancelStub() service.Service {
	return &cancelInstanceStub{
		state: engine.InstanceState{InstanceID: "p1", Status: engine.StatusTerminated},
	}
}

func conflictCancelStub() service.Service {
	return &cancelInstanceStub{
		err: fmt.Errorf("%w", service.ErrConflict),
	}
}

func notFoundCancelStub() service.Service {
	return &cancelInstanceStub{
		err: kernel.ErrInstanceNotFound,
	}
}

// TestHandleCancelInstance tests the POST /admin/instances/{id}/cancel route.
func TestHandleCancelInstance(t *testing.T) {
	cases := []struct {
		name       string
		middleware bool // install allow-admin middleware?
		svc        service.Service
		wantStatus int
	}{
		{name: "default-deny without admin middleware", middleware: false, svc: okCancelStub(), wantStatus: http.StatusForbidden},
		{name: "admin success", middleware: true, svc: okCancelStub(), wantStatus: http.StatusOK},
		{name: "already-terminal maps to 422", middleware: true, svc: conflictCancelStub(), wantStatus: http.StatusUnprocessableEntity},
		{name: "unknown instance maps to 404", middleware: true, svc: notFoundCancelStub(), wantStatus: http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var opts []rest.Option
			if tc.middleware {
				opts = append(opts, rest.WithAdminMiddleware(allowAdmin))
			}
			h := rest.NewHandler(tc.svc, opts...)
			req := httptest.NewRequest(http.MethodPost, "/admin/instances/p1/cancel", nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("case %q: want %d, got %d — body: %s", tc.name, tc.wantStatus, rec.Code, rec.Body.String())
			}
		})
	}
}

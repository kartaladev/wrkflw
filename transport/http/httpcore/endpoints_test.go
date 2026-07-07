package httpcore_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/internal/transporttest"
	"github.com/zakyalvan/krtlwrkflw/service"
	"github.com/zakyalvan/krtlwrkflw/transport/http/httpcore"
)

// TestStartInstance exercises httpcore.StartInstance with a real in-memory service.
func TestStartInstance(t *testing.T) {
	t.Parallel()

	def := transporttest.LinearProcess()

	tests := map[string]struct {
		in     httpcore.StartInput
		mapper func(engine.InstanceState) any
		assert func(t *testing.T, status int, body any, err error)
	}{
		"missing def_ref → ErrBadInput, no service call": {
			in: httpcore.StartInput{DefRef: model.Qualifier{}},
			assert: func(t *testing.T, _ int, _ any, err error) {
				if !errors.Is(err, httpcore.ErrBadInput) {
					t.Fatalf("want ErrBadInput, got %v", err)
				}
			},
		},
		"success → 201 default mapped body": {
			in: httpcore.StartInput{DefRef: model.Latest("greeting"), Vars: map[string]any{"name": "ada"}},
			assert: func(t *testing.T, status int, body any, err error) {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if status != http.StatusCreated {
					t.Fatalf("want 201, got %d", status)
				}
				if body == nil {
					t.Fatal("want non-nil body")
				}
			},
		},
		"custom mapper → returned in body": {
			in:     httpcore.StartInput{DefRef: model.Latest("greeting"), Vars: map[string]any{"name": "ada"}},
			mapper: func(_ engine.InstanceState) any { return map[string]string{"custom": "yes"} },
			assert: func(t *testing.T, status int, body any, err error) {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if status != http.StatusCreated {
					t.Fatalf("want 201, got %d", status)
				}
				m, ok := body.(map[string]string)
				if !ok || m["custom"] != "yes" {
					t.Fatalf("want custom mapper result, got %v", body)
				}
			},
		},
		"unknown definition → service error propagated": {
			in: httpcore.StartInput{DefRef: model.Latest("no-such-def")},
			assert: func(t *testing.T, status int, body any, err error) {
				if err == nil {
					t.Fatal("want error for unknown definition")
				}
				if status != 0 || body != nil {
					t.Fatalf("want (0, nil) on error, got (%d, %v)", status, body)
				}
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, svc := transporttest.NewHarness(t, def)
			status, body, err := httpcore.StartInstance(t.Context(), svc, tc.in, tc.mapper)
			tc.assert(t, status, body, err)
		})
	}
}

// TestGetInstance exercises httpcore.GetInstance.
func TestGetInstance(t *testing.T) {
	t.Parallel()

	def := transporttest.LinearProcess()

	tests := map[string]struct {
		setup  func(svc service.Service) string
		assert func(t *testing.T, status int, body any, err error)
	}{
		"existing instance → 200 with body": {
			setup: func(svc service.Service) string {
				pi, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
					DefRef: model.Latest("greeting"), Vars: map[string]any{"name": "x"},
				})
				if err != nil {
					t.Fatalf("StartInstance: %v", err)
				}
				return pi.State().InstanceID
			},
			assert: func(t *testing.T, status int, body any, err error) {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if status != http.StatusOK {
					t.Fatalf("want 200, got %d", status)
				}
				if body == nil {
					t.Fatal("want non-nil body")
				}
			},
		},
		"not found → error propagated": {
			setup: func(_ service.Service) string { return "no-such-id" },
			assert: func(t *testing.T, _ int, _ any, err error) {
				if err == nil {
					t.Fatal("want error for missing instance")
				}
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, svc := transporttest.NewHarness(t, def)
			id := tc.setup(svc)
			status, body, err := httpcore.GetInstance(t.Context(), svc, id, nil)
			tc.assert(t, status, body, err)
		})
	}
}

// TestGetInstanceSnapshot exercises httpcore.GetInstanceSnapshot.
func TestGetInstanceSnapshot(t *testing.T) {
	t.Parallel()

	def := transporttest.LinearProcess()

	tests := map[string]struct {
		setup  func(svc service.Service) string
		assert func(t *testing.T, status int, body any, err error)
	}{
		"existing instance → 200 snapshot body": {
			setup: func(svc service.Service) string {
				pi, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
					DefRef: model.Latest("greeting"), Vars: map[string]any{"name": "x"},
				})
				if err != nil {
					t.Fatalf("StartInstance: %v", err)
				}
				return pi.State().InstanceID
			},
			assert: func(t *testing.T, status int, body any, err error) {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if status != http.StatusOK {
					t.Fatalf("want 200, got %d", status)
				}
				if body == nil {
					t.Fatal("want non-nil body")
				}
			},
		},
		"not found → error propagated": {
			setup: func(_ service.Service) string { return "snap-missing" },
			assert: func(t *testing.T, _ int, _ any, err error) {
				if err == nil {
					t.Fatal("want error for missing instance")
				}
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, svc := transporttest.NewHarness(t, def)
			id := tc.setup(svc)
			status, body, err := httpcore.GetInstanceSnapshot(t.Context(), svc, id)
			tc.assert(t, status, body, err)
		})
	}
}

// TestGetActionableView exercises httpcore.GetActionableView.
func TestGetActionableView(t *testing.T) {
	t.Parallel()

	def := transporttest.ApprovalProcess()

	tests := map[string]struct {
		setup  func(svc service.Service) string
		assert func(t *testing.T, status int, body any, err error)
	}{
		"existing instance → 200 actionable body": {
			setup: func(svc service.Service) string {
				pi, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
					DefRef: model.Latest("approval"),
				})
				if err != nil {
					t.Fatalf("StartInstance: %v", err)
				}
				return pi.State().InstanceID
			},
			assert: func(t *testing.T, status int, body any, err error) {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if status != http.StatusOK {
					t.Fatalf("want 200, got %d", status)
				}
				if body == nil {
					t.Fatal("want non-nil body")
				}
			},
		},
		"not found → error propagated": {
			setup: func(_ service.Service) string { return "actionable-missing" },
			assert: func(t *testing.T, _ int, _ any, err error) {
				if err == nil {
					t.Fatal("want error for missing instance")
				}
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, svc := transporttest.NewHarness(t, def)
			id := tc.setup(svc)
			status, body, err := httpcore.GetActionableView(t.Context(), svc, id)
			tc.assert(t, status, body, err)
		})
	}
}

// TestDeliverSignal exercises httpcore.DeliverSignal.
func TestDeliverSignal(t *testing.T) {
	t.Parallel()

	def := transporttest.SignalProcess("approved")

	tests := map[string]struct {
		setup  func(svc service.Service) string
		in     httpcore.SignalInput
		assert func(t *testing.T, status int, body any, err error)
	}{
		"missing signal → ErrBadInput": {
			setup: func(_ service.Service) string { return "any-id" },
			in:    httpcore.SignalInput{Signal: ""},
			assert: func(t *testing.T, _ int, _ any, err error) {
				if !errors.Is(err, httpcore.ErrBadInput) {
					t.Fatalf("want ErrBadInput, got %v", err)
				}
			},
		},
		"success → 200 with body": {
			setup: func(svc service.Service) string {
				pi, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
					DefRef: model.Latest("signal-catch-approved"),
				})
				if err != nil {
					t.Fatalf("StartInstance: %v", err)
				}
				return pi.State().InstanceID
			},
			in: httpcore.SignalInput{Signal: "approved", Payload: map[string]any{"decision": "yes"}},
			assert: func(t *testing.T, status int, body any, err error) {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if status != http.StatusOK {
					t.Fatalf("want 200, got %d", status)
				}
				if body == nil {
					t.Fatal("want non-nil body")
				}
			},
		},
		"instance not found → service error propagated": {
			setup: func(_ service.Service) string { return "no-such-instance" },
			in:    httpcore.SignalInput{Signal: "approved"},
			assert: func(t *testing.T, status int, body any, err error) {
				if err == nil {
					t.Fatal("want error for missing instance")
				}
				if status != 0 || body != nil {
					t.Fatalf("want (0, nil) on error, got (%d, %v)", status, body)
				}
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, svc := transporttest.NewHarness(t, def)
			id := tc.setup(svc)
			status, body, err := httpcore.DeliverSignal(t.Context(), svc, id, tc.in, nil)
			tc.assert(t, status, body, err)
		})
	}
}

// TestDeliverMessage exercises httpcore.DeliverMessage.
func TestDeliverMessage(t *testing.T) {
	t.Parallel()

	def := transporttest.MessageProcess("order-shipped")

	tests := map[string]struct {
		setup  func(svc service.Service)
		in     httpcore.MessageInput
		assert func(t *testing.T, status int, body any, err error)
	}{
		"missing def_ref → ErrBadInput": {
			setup: func(_ service.Service) {},
			in:    httpcore.MessageInput{DefRef: model.Qualifier{}, Name: "order-shipped"},
			assert: func(t *testing.T, _ int, _ any, err error) {
				if !errors.Is(err, httpcore.ErrBadInput) {
					t.Fatalf("want ErrBadInput, got %v", err)
				}
			},
		},
		"missing name → ErrBadInput": {
			setup: func(_ service.Service) {},
			in:    httpcore.MessageInput{DefRef: model.Version("message-catch-order-shipped", 1), Name: ""},
			assert: func(t *testing.T, _ int, _ any, err error) {
				if !errors.Is(err, httpcore.ErrBadInput) {
					t.Fatalf("want ErrBadInput, got %v", err)
				}
			},
		},
		"success → 202 nil body": {
			setup: func(svc service.Service) {
				_, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
					DefRef: model.Latest("message-catch-order-shipped"),
					Vars:   map[string]any{"orderId": "42"},
				})
				if err != nil {
					t.Fatalf("StartInstance: %v", err)
				}
			},
			in: httpcore.MessageInput{
				DefRef:         model.Version("message-catch-order-shipped", 1),
				Name:           "order-shipped",
				CorrelationKey: "42",
				Payload:        map[string]any{"shipped": true},
			},
			assert: func(t *testing.T, status int, body any, err error) {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if status != http.StatusAccepted {
					t.Fatalf("want 202, got %d", status)
				}
				if body != nil {
					t.Fatalf("want nil body, got %v", body)
				}
			},
		},
		"unknown definition → service error propagated": {
			setup: func(_ service.Service) {},
			in:    httpcore.MessageInput{DefRef: model.Version("no-such-def", 1), Name: "order-shipped"},
			assert: func(t *testing.T, status int, body any, err error) {
				if err == nil {
					t.Fatal("want error for unknown definition")
				}
				if status != 0 || body != nil {
					t.Fatalf("want (0, nil) on error, got (%d, %v)", status, body)
				}
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, svc := transporttest.NewHarness(t, def)
			tc.setup(svc)
			status, body, err := httpcore.DeliverMessage(t.Context(), svc, tc.in)
			tc.assert(t, status, body, err)
		})
	}
}

// TestClaimTask exercises httpcore.ClaimTask.
func TestClaimTask(t *testing.T) {
	t.Parallel()

	def := transporttest.ApprovalProcess()

	tests := map[string]struct {
		setupToken func(h *transporttest.Harness) string
		in         httpcore.ClaimInput
		assert     func(t *testing.T, status int, body any, err error)
	}{
		"success → 200 with body": {
			setupToken: func(h *transporttest.Harness) string {
				return transporttest.StartedApprovalInstance(t, h, "claim-ok-1")
			},
			in: httpcore.ClaimInput{Actor: httpcore.Actor{ID: "alice", Roles: []string{"manager"}}},
			assert: func(t *testing.T, status int, body any, err error) {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if status != http.StatusOK {
					t.Fatalf("want 200, got %d", status)
				}
				if body == nil {
					t.Fatal("want non-nil body")
				}
			},
		},
		"forbidden actor → error propagated": {
			setupToken: func(h *transporttest.Harness) string {
				return transporttest.StartedApprovalInstance(t, h, "claim-forbidden-1")
			},
			in: httpcore.ClaimInput{Actor: httpcore.Actor{ID: "bob", Roles: []string{"viewer"}}},
			assert: func(t *testing.T, _ int, _ any, err error) {
				if err == nil {
					t.Fatal("want error for unauthorized actor")
				}
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h, svc := transporttest.NewHarness(t, def)
			token := tc.setupToken(h)
			status, body, err := httpcore.ClaimTask(t.Context(), svc, token, tc.in, nil)
			tc.assert(t, status, body, err)
		})
	}
}

// TestCompleteTask exercises httpcore.CompleteTask.
func TestCompleteTask(t *testing.T) {
	t.Parallel()

	def := transporttest.ApprovalProcess()

	tests := map[string]struct {
		setupToken func(h *transporttest.Harness, svc service.Service) string
		in         httpcore.CompleteInput
		assert     func(t *testing.T, status int, body any, err error)
	}{
		"success → 200 with completed status": {
			setupToken: func(h *transporttest.Harness, svc service.Service) string {
				token := transporttest.StartedApprovalInstance(t, h, "complete-ok-1")
				_, err := svc.ClaimTask(t.Context(), service.ClaimTaskRequest{
					TaskToken: token,
					Actor:     authz.Actor{ID: "alice", Roles: []string{"manager"}},
				})
				if err != nil {
					t.Fatalf("ClaimTask: %v", err)
				}
				return token
			},
			in: httpcore.CompleteInput{
				Actor:  httpcore.Actor{ID: "alice", Roles: []string{"manager"}},
				Output: map[string]any{"approved": true},
			},
			assert: func(t *testing.T, status int, body any, err error) {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if status != http.StatusOK {
					t.Fatalf("want 200, got %d", status)
				}
				if body == nil {
					t.Fatal("want non-nil body")
				}
			},
		},
		"unauthorized actor → error propagated": {
			setupToken: func(h *transporttest.Harness, _ service.Service) string {
				return transporttest.StartedApprovalInstance(t, h, "complete-unauth-1")
			},
			in: httpcore.CompleteInput{Actor: httpcore.Actor{ID: "bob", Roles: []string{"viewer"}}},
			assert: func(t *testing.T, _ int, _ any, err error) {
				if err == nil {
					t.Fatal("want error for unauthorized actor")
				}
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h, svc := transporttest.NewHarness(t, def)
			token := tc.setupToken(h, svc)
			status, body, err := httpcore.CompleteTask(t.Context(), svc, token, tc.in, nil)
			tc.assert(t, status, body, err)
		})
	}
}

// TestReassignTask exercises httpcore.ReassignTask.
func TestReassignTask(t *testing.T) {
	t.Parallel()

	def := transporttest.ApprovalProcess()

	tests := map[string]struct {
		setupToken func(h *transporttest.Harness, svc service.Service) string
		in         httpcore.ReassignInput
		assert     func(t *testing.T, status int, body any, err error)
	}{
		"success → 200 with body": {
			setupToken: func(h *transporttest.Harness, svc service.Service) string {
				token := transporttest.StartedApprovalInstance(t, h, "reassign-ok-1")
				_, err := svc.ClaimTask(t.Context(), service.ClaimTaskRequest{
					TaskToken: token,
					Actor:     authz.Actor{ID: "alice", Roles: []string{"manager"}},
				})
				if err != nil {
					t.Fatalf("ClaimTask: %v", err)
				}
				return token
			},
			in: httpcore.ReassignInput{
				From: "alice",
				To:   "carol",
				By:   httpcore.Actor{ID: "alice", Roles: []string{"manager"}},
			},
			assert: func(t *testing.T, status int, body any, err error) {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if status != http.StatusOK {
					t.Fatalf("want 200, got %d", status)
				}
				if body == nil {
					t.Fatal("want non-nil body")
				}
			},
		},
		"unauthorized reassigner → error propagated": {
			setupToken: func(h *transporttest.Harness, svc service.Service) string {
				token := transporttest.StartedApprovalInstance(t, h, "reassign-unauth-1")
				_, err := svc.ClaimTask(t.Context(), service.ClaimTaskRequest{
					TaskToken: token,
					Actor:     authz.Actor{ID: "alice", Roles: []string{"manager"}},
				})
				if err != nil {
					t.Fatalf("ClaimTask: %v", err)
				}
				return token
			},
			in: httpcore.ReassignInput{
				From: "alice",
				To:   "carol",
				By:   httpcore.Actor{ID: "bob", Roles: []string{"viewer"}},
			},
			assert: func(t *testing.T, _ int, _ any, err error) {
				if err == nil {
					t.Fatal("want error for unauthorized reassigner")
				}
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h, svc := transporttest.NewHarness(t, def)
			token := tc.setupToken(h, svc)
			status, body, err := httpcore.ReassignTask(t.Context(), svc, token, tc.in, nil)
			tc.assert(t, status, body, err)
		})
	}
}

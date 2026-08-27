package httpcore_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/internal/transporttest"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/runtime/monitor"
	"github.com/kartaladev/wrkflw/service"
	"github.com/kartaladev/wrkflw/transport/http/httpcore"
)

// --- Tests ---

// TestAdminListInstances exercises httpcore.AdminListInstances.
func TestAdminListInstances(t *testing.T) {
	t.Parallel()

	def := transporttest.LinearProcess()

	tests := map[string]struct {
		setup  func(svc service.Service)
		q      httpcore.ListInstancesQuery
		assert func(t *testing.T, status int, body any, err error)
	}{
		"empty store → 200 empty items": {
			setup: func(_ service.Service) {},
			q:     httpcore.ListInstancesQuery{},
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
		"one instance → 200 with items": {
			setup: func(svc service.Service) {
				_, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
					DefRef: model.Latest("greeting"), Vars: map[string]any{"name": "z"},
				})
				if err != nil {
					t.Fatalf("StartInstance: %v", err)
				}
			},
			q: httpcore.ListInstancesQuery{},
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
		"invalid status → error propagated": {
			setup: func(_ service.Service) {},
			q:     httpcore.ListInstancesQuery{Status: "bogus"},
			assert: func(t *testing.T, _ int, _ any, err error) {
				if err == nil {
					t.Fatal("want error for unknown status")
				}
				if !errors.Is(err, httpcore.ErrBadInput) {
					t.Fatalf("want ErrBadInput, got %v", err)
				}
			},
		},
		"status=completed filter → 200": {
			setup: func(svc service.Service) {
				_, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
					DefRef: model.Latest("greeting"), Vars: map[string]any{"name": "z"},
				})
				if err != nil {
					t.Fatalf("StartInstance: %v", err)
				}
			},
			q: httpcore.ListInstancesQuery{Status: "completed"},
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
		"status=running filter → 200 empty": {
			setup: func(_ service.Service) {},
			q:     httpcore.ListInstancesQuery{Status: "running"},
			assert: func(t *testing.T, status int, body any, err error) {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if status != http.StatusOK {
					t.Fatalf("want 200, got %d", status)
				}
			},
		},
		"status=failed filter → 200 empty": {
			setup: func(_ service.Service) {},
			q:     httpcore.ListInstancesQuery{Status: "failed"},
			assert: func(t *testing.T, status int, body any, err error) {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if status != http.StatusOK {
					t.Fatalf("want 200, got %d", status)
				}
			},
		},
		"status=compensating filter → 200 empty": {
			setup: func(_ service.Service) {},
			q:     httpcore.ListInstancesQuery{Status: "compensating"},
			assert: func(t *testing.T, status int, body any, err error) {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if status != http.StatusOK {
					t.Fatalf("want 200, got %d", status)
				}
			},
		},
		"status=terminated filter → 200 empty": {
			setup: func(_ service.Service) {},
			q:     httpcore.ListInstancesQuery{Status: "terminated"},
			assert: func(t *testing.T, status int, body any, err error) {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if status != http.StatusOK {
					t.Fatalf("want 200, got %d", status)
				}
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, svc := transporttest.NewHarness(t, def)
			tc.setup(svc)
			status, body, err := httpcore.AdminListInstances(t.Context(), svc, tc.q)
			tc.assert(t, status, body, err)
		})
	}
}

// TestResolveIncident exercises httpcore.ResolveIncident.
func TestResolveIncident(t *testing.T) {
	t.Parallel()

	def := transporttest.LinearProcess()

	tests := map[string]struct {
		setup  func(svc service.Service) (instanceID, incidentID string)
		in     httpcore.ResolveIncidentInput
		assert func(t *testing.T, status int, body any, err error)
	}{
		"unknown instance → service error propagated": {
			setup: func(_ service.Service) (string, string) { return "no-such-inst", "inc-1" },
			in:    httpcore.ResolveIncidentInput{},
			assert: func(t *testing.T, status int, body any, err error) {
				if err == nil {
					t.Fatal("want error for unknown instance")
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
			instanceID, incidentID := tc.setup(svc)
			status, body, err := httpcore.ResolveIncident(t.Context(), svc, instanceID, incidentID, tc.in, nil)
			tc.assert(t, status, body, err)
		})
	}
}

// TestCancelInstance exercises httpcore.CancelInstance.
func TestCancelInstance(t *testing.T) {
	t.Parallel()

	def := transporttest.ApprovalProcess()

	tests := map[string]struct {
		setup  func(svc service.Service) string
		assert func(t *testing.T, status int, body any, err error)
	}{
		"running instance → 200 with body": {
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
		"unknown instance → service error propagated": {
			setup: func(_ service.Service) string { return "no-such-inst" },
			assert: func(t *testing.T, status int, body any, err error) {
				if err == nil {
					t.Fatal("want error for unknown instance")
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
			instanceID := tc.setup(svc)
			status, body, err := httpcore.CancelInstance(t.Context(), svc, instanceID, nil)
			tc.assert(t, status, body, err)
		})
	}
}

// TestListDeadLetters exercises httpcore.ListDeadLetters.
func TestListDeadLetters(t *testing.T) {
	t.Parallel()

	now := time.Now()

	tests := map[string]struct {
		buildDLA func(t *testing.T) service.DeadLetterAdmin
		q        httpcore.DeadLetterQuery
		assert   func(t *testing.T, status int, body any, err error)
	}{
		"empty list → 200 empty items": {
			buildDLA: func(t *testing.T) service.DeadLetterAdmin {
				t.Helper()
				m := service.NewMockDeadLetterAdmin(gomock.NewController(t))
				m.EXPECT().ListDeadLettered(gomock.Any(), gomock.Any()).Return(nil, nil)
				return m
			},
			q: httpcore.DeadLetterQuery{},
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
		"one dead letter → 200 with item": {
			buildDLA: func(t *testing.T) service.DeadLetterAdmin {
				t.Helper()
				m := service.NewMockDeadLetterAdmin(gomock.NewController(t))
				m.EXPECT().ListDeadLettered(gomock.Any(), gomock.Any()).Return(
					[]monitor.DeadLetter{
						{ID: 1, InstanceID: "inst-1", Topic: "instance.failed", RetryCount: 3, LastError: "timeout", CreatedAt: now},
					}, nil)
				return m
			},
			q: httpcore.DeadLetterQuery{Limit: 10},
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
		"service error → propagated": {
			buildDLA: func(t *testing.T) service.DeadLetterAdmin {
				t.Helper()
				m := service.NewMockDeadLetterAdmin(gomock.NewController(t))
				m.EXPECT().ListDeadLettered(gomock.Any(), gomock.Any()).Return(nil, errors.New("db error"))
				return m
			},
			q: httpcore.DeadLetterQuery{},
			assert: func(t *testing.T, status int, body any, err error) {
				if err == nil {
					t.Fatal("want error from service")
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
			status, body, err := httpcore.ListDeadLetters(t.Context(), tc.buildDLA(t), tc.q)
			tc.assert(t, status, body, err)
		})
	}
}

// TestRedriveDeadLetters exercises httpcore.RedriveDeadLetters.
func TestRedriveDeadLetters(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		buildDLA func(t *testing.T) service.DeadLetterAdmin
		in       httpcore.RedriveInput
		assert   func(t *testing.T, status int, body any, err error)
	}{
		"redrive two → 200 redriven:2": {
			buildDLA: func(t *testing.T) service.DeadLetterAdmin {
				t.Helper()
				m := service.NewMockDeadLetterAdmin(gomock.NewController(t))
				m.EXPECT().Redrive(gomock.Any(), int64(1), int64(2)).Return(2, nil)
				return m
			},
			in: httpcore.RedriveInput{IDs: []int64{1, 2}},
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
		"empty ids → 200 redriven:0": {
			buildDLA: func(t *testing.T) service.DeadLetterAdmin {
				t.Helper()
				m := service.NewMockDeadLetterAdmin(gomock.NewController(t))
				m.EXPECT().Redrive(gomock.Any()).Return(0, nil)
				return m
			},
			in: httpcore.RedriveInput{IDs: nil},
			assert: func(t *testing.T, status int, body any, err error) {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if status != http.StatusOK {
					t.Fatalf("want 200, got %d", status)
				}
			},
		},
		"service error → propagated": {
			buildDLA: func(t *testing.T) service.DeadLetterAdmin {
				t.Helper()
				m := service.NewMockDeadLetterAdmin(gomock.NewController(t))
				m.EXPECT().Redrive(gomock.Any(), int64(99)).Return(0, errors.New("db error"))
				return m
			},
			in: httpcore.RedriveInput{IDs: []int64{99}},
			assert: func(t *testing.T, status int, body any, err error) {
				if err == nil {
					t.Fatal("want error from service")
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
			status, body, err := httpcore.RedriveDeadLetters(t.Context(), tc.buildDLA(t), tc.in)
			tc.assert(t, status, body, err)
		})
	}
}

// TestListPolicies exercises httpcore.ListPolicies.
func TestListPolicies(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		buildPA func(t *testing.T) service.PolicyAdmin
		assert  func(t *testing.T, status int, body any, err error)
	}{
		"success → 200 with policies": {
			buildPA: func(t *testing.T) service.PolicyAdmin {
				t.Helper()
				m := service.NewMockPolicyAdmin(gomock.NewController(t))
				m.EXPECT().ListPolicies(gomock.Any()).Return(
					[]service.PolicyRule{{Subject: "alice", Object: "/orders", Action: "read"}}, nil)
				return m
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
		"empty → 200 with empty policies": {
			buildPA: func(t *testing.T) service.PolicyAdmin {
				t.Helper()
				m := service.NewMockPolicyAdmin(gomock.NewController(t))
				m.EXPECT().ListPolicies(gomock.Any()).Return(nil, nil)
				return m
			},
			assert: func(t *testing.T, status int, body any, err error) {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if status != http.StatusOK {
					t.Fatalf("want 200, got %d", status)
				}
			},
		},
		"service error → propagated": {
			buildPA: func(t *testing.T) service.PolicyAdmin {
				t.Helper()
				m := service.NewMockPolicyAdmin(gomock.NewController(t))
				m.EXPECT().ListPolicies(gomock.Any()).Return(nil, errors.New("casbin error"))
				return m
			},
			assert: func(t *testing.T, status int, body any, err error) {
				if err == nil {
					t.Fatal("want error from service")
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
			status, body, err := httpcore.ListPolicies(t.Context(), tc.buildPA(t))
			tc.assert(t, status, body, err)
		})
	}
}

// TestAddPolicy exercises httpcore.AddPolicy.
func TestAddPolicy(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		buildPA func(t *testing.T) service.PolicyAdmin
		in      httpcore.PolicyRuleInput
		assert  func(t *testing.T, status int, body any, err error)
	}{
		"new policy → 200 added:true": {
			buildPA: func(t *testing.T) service.PolicyAdmin {
				t.Helper()
				m := service.NewMockPolicyAdmin(gomock.NewController(t))
				m.EXPECT().AddPolicy(gomock.Any(), gomock.Any()).Return(true, nil)
				return m
			},
			in: httpcore.PolicyRuleInput{Subject: "alice", Object: "/orders", Action: "read"},
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
		"already exists → 200 added:false": {
			buildPA: func(t *testing.T) service.PolicyAdmin {
				t.Helper()
				m := service.NewMockPolicyAdmin(gomock.NewController(t))
				m.EXPECT().AddPolicy(gomock.Any(), gomock.Any()).Return(false, nil)
				return m
			},
			in: httpcore.PolicyRuleInput{Subject: "alice", Object: "/orders", Action: "read"},
			assert: func(t *testing.T, status int, body any, err error) {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if status != http.StatusOK {
					t.Fatalf("want 200, got %d", status)
				}
			},
		},
		"service error → propagated": {
			buildPA: func(t *testing.T) service.PolicyAdmin {
				t.Helper()
				m := service.NewMockPolicyAdmin(gomock.NewController(t))
				m.EXPECT().AddPolicy(gomock.Any(), gomock.Any()).Return(false, errors.New("casbin error"))
				return m
			},
			in: httpcore.PolicyRuleInput{Subject: "alice", Object: "/orders", Action: "read"},
			assert: func(t *testing.T, status int, body any, err error) {
				if err == nil {
					t.Fatal("want error from service")
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
			status, body, err := httpcore.AddPolicy(t.Context(), tc.buildPA(t), tc.in)
			tc.assert(t, status, body, err)
		})
	}
}

// TestRemovePolicy exercises httpcore.RemovePolicy.
func TestRemovePolicy(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		buildPA func(t *testing.T) service.PolicyAdmin
		in      httpcore.PolicyRuleInput
		assert  func(t *testing.T, status int, body any, err error)
	}{
		"exists → 200 removed:true": {
			buildPA: func(t *testing.T) service.PolicyAdmin {
				t.Helper()
				m := service.NewMockPolicyAdmin(gomock.NewController(t))
				m.EXPECT().RemovePolicy(gomock.Any(), gomock.Any()).Return(true, nil)
				return m
			},
			in: httpcore.PolicyRuleInput{Subject: "alice", Object: "/orders", Action: "read"},
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
		"not found → 200 removed:false": {
			buildPA: func(t *testing.T) service.PolicyAdmin {
				t.Helper()
				m := service.NewMockPolicyAdmin(gomock.NewController(t))
				m.EXPECT().RemovePolicy(gomock.Any(), gomock.Any()).Return(false, nil)
				return m
			},
			in: httpcore.PolicyRuleInput{Subject: "alice", Object: "/orders", Action: "read"},
			assert: func(t *testing.T, status int, body any, err error) {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if status != http.StatusOK {
					t.Fatalf("want 200, got %d", status)
				}
			},
		},
		"service error → propagated": {
			buildPA: func(t *testing.T) service.PolicyAdmin {
				t.Helper()
				m := service.NewMockPolicyAdmin(gomock.NewController(t))
				m.EXPECT().RemovePolicy(gomock.Any(), gomock.Any()).Return(false, errors.New("casbin error"))
				return m
			},
			in: httpcore.PolicyRuleInput{Subject: "alice", Object: "/orders", Action: "read"},
			assert: func(t *testing.T, status int, body any, err error) {
				if err == nil {
					t.Fatal("want error from service")
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
			status, body, err := httpcore.RemovePolicy(t.Context(), tc.buildPA(t), tc.in)
			tc.assert(t, status, body, err)
		})
	}
}

// TestListRoleBindings exercises httpcore.ListRoleBindings.
func TestListRoleBindings(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		buildPA func(t *testing.T) service.PolicyAdmin
		assert  func(t *testing.T, status int, body any, err error)
	}{
		"success → 200 with bindings": {
			buildPA: func(t *testing.T) service.PolicyAdmin {
				t.Helper()
				m := service.NewMockPolicyAdmin(gomock.NewController(t))
				m.EXPECT().ListRoles(gomock.Any()).Return(
					[]service.RoleBinding{{User: "alice", Role: "admin"}}, nil)
				return m
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
		"service error → propagated": {
			buildPA: func(t *testing.T) service.PolicyAdmin {
				t.Helper()
				m := service.NewMockPolicyAdmin(gomock.NewController(t))
				m.EXPECT().ListRoles(gomock.Any()).Return(nil, errors.New("casbin error"))
				return m
			},
			assert: func(t *testing.T, status int, body any, err error) {
				if err == nil {
					t.Fatal("want error from service")
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
			status, body, err := httpcore.ListRoleBindings(t.Context(), tc.buildPA(t))
			tc.assert(t, status, body, err)
		})
	}
}

// TestAddRoleBinding exercises httpcore.AddRoleBinding.
func TestAddRoleBinding(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		buildPA func(t *testing.T) service.PolicyAdmin
		in      httpcore.RoleBindingInput
		assert  func(t *testing.T, status int, body any, err error)
	}{
		"success → 200 added:true": {
			buildPA: func(t *testing.T) service.PolicyAdmin {
				t.Helper()
				m := service.NewMockPolicyAdmin(gomock.NewController(t))
				m.EXPECT().AddRole(gomock.Any(), gomock.Any()).Return(true, nil)
				return m
			},
			in: httpcore.RoleBindingInput{User: "alice", Role: "admin"},
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
		"already exists → 200 added:false": {
			buildPA: func(t *testing.T) service.PolicyAdmin {
				t.Helper()
				m := service.NewMockPolicyAdmin(gomock.NewController(t))
				m.EXPECT().AddRole(gomock.Any(), gomock.Any()).Return(false, nil)
				return m
			},
			in: httpcore.RoleBindingInput{User: "alice", Role: "admin"},
			assert: func(t *testing.T, status int, body any, err error) {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if status != http.StatusOK {
					t.Fatalf("want 200, got %d", status)
				}
			},
		},
		"service error → propagated": {
			buildPA: func(t *testing.T) service.PolicyAdmin {
				t.Helper()
				m := service.NewMockPolicyAdmin(gomock.NewController(t))
				m.EXPECT().AddRole(gomock.Any(), gomock.Any()).Return(false, errors.New("casbin error"))
				return m
			},
			in: httpcore.RoleBindingInput{User: "alice", Role: "admin"},
			assert: func(t *testing.T, status int, body any, err error) {
				if err == nil {
					t.Fatal("want error from service")
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
			status, body, err := httpcore.AddRoleBinding(t.Context(), tc.buildPA(t), tc.in)
			tc.assert(t, status, body, err)
		})
	}
}

// TestRemoveRoleBinding exercises httpcore.RemoveRoleBinding.
func TestRemoveRoleBinding(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		buildPA func(t *testing.T) service.PolicyAdmin
		in      httpcore.RoleBindingInput
		assert  func(t *testing.T, status int, body any, err error)
	}{
		"exists → 200 removed:true": {
			buildPA: func(t *testing.T) service.PolicyAdmin {
				t.Helper()
				m := service.NewMockPolicyAdmin(gomock.NewController(t))
				m.EXPECT().RemoveRole(gomock.Any(), gomock.Any()).Return(true, nil)
				return m
			},
			in: httpcore.RoleBindingInput{User: "alice", Role: "admin"},
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
		"not found → 200 removed:false": {
			buildPA: func(t *testing.T) service.PolicyAdmin {
				t.Helper()
				m := service.NewMockPolicyAdmin(gomock.NewController(t))
				m.EXPECT().RemoveRole(gomock.Any(), gomock.Any()).Return(false, nil)
				return m
			},
			in: httpcore.RoleBindingInput{User: "alice", Role: "admin"},
			assert: func(t *testing.T, status int, body any, err error) {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if status != http.StatusOK {
					t.Fatalf("want 200, got %d", status)
				}
			},
		},
		"service error → propagated": {
			buildPA: func(t *testing.T) service.PolicyAdmin {
				t.Helper()
				m := service.NewMockPolicyAdmin(gomock.NewController(t))
				m.EXPECT().RemoveRole(gomock.Any(), gomock.Any()).Return(false, errors.New("casbin error"))
				return m
			},
			in: httpcore.RoleBindingInput{User: "alice", Role: "admin"},
			assert: func(t *testing.T, status int, body any, err error) {
				if err == nil {
					t.Fatal("want error from service")
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
			status, body, err := httpcore.RemoveRoleBinding(t.Context(), tc.buildPA(t), tc.in)
			tc.assert(t, status, body, err)
		})
	}
}

// TestAdminRelayStats exercises httpcore.AdminRelayStats.
func TestAdminRelayStats(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		buildRSA func(t *testing.T) service.RelayStatsAdmin
		assert   func(t *testing.T, status int, body any, err error)
	}{
		"success → 200 with stats": {
			buildRSA: func(t *testing.T) service.RelayStatsAdmin {
				t.Helper()
				m := service.NewMockRelayStatsAdmin(gomock.NewController(t))
				m.EXPECT().OutboxStats(gomock.Any()).Return(
					kernel.OutboxStats{Pending: 5, Dead: 1, OldestPendingAge: 10 * time.Second}, nil)
				return m
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
		"service error → propagated": {
			buildRSA: func(t *testing.T) service.RelayStatsAdmin {
				t.Helper()
				m := service.NewMockRelayStatsAdmin(gomock.NewController(t))
				m.EXPECT().OutboxStats(gomock.Any()).Return(kernel.OutboxStats{}, errors.New("db error"))
				return m
			},
			assert: func(t *testing.T, status int, body any, err error) {
				if err == nil {
					t.Fatal("want error from service")
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
			status, body, err := httpcore.AdminRelayStats(t.Context(), tc.buildRSA(t))
			tc.assert(t, status, body, err)
		})
	}
}

// TestAdminTimers exercises httpcore.AdminTimers, which pages armed timers
// rather than returning the whole table (ADR-0159). The aggregate fields are
// gated behind total=true so an ordinary paged request issues no count(*).
func TestAdminTimers(t *testing.T) {
	t.Parallel()

	fireAt := time.Now().Add(5 * time.Minute)

	tests := map[string]struct {
		query   httpcore.ListArmedTimersQuery
		buildTA func(t *testing.T) service.TimerAdmin
		assert  func(t *testing.T, status int, body any, err error)
	}{
		"success → 200 with a page and its continuation cursor": {
			query: httpcore.ListArmedTimersQuery{Limit: 1},
			buildTA: func(t *testing.T) service.TimerAdmin {
				t.Helper()
				m := service.NewMockTimerAdmin(gomock.NewController(t))
				m.EXPECT().ListArmedPage(gomock.Any(), kernel.ArmedTimerFilter{Limit: 1}).Return(
					kernel.ArmedTimerPage{
						Items:      []kernel.ArmedTimer{{InstanceID: "inst-1", DefID: "d", DefVersion: 1, TimerID: "t1", NextRun: fireAt}},
						NextCursor: "cursor-2",
						HasMore:    true,
					}, nil)
				return m
			},
			assert: func(t *testing.T, status int, body any, err error) {
				require.NoError(t, err)
				assert.Equal(t, http.StatusOK, status)
				raw, marshalErr := json.Marshal(body)
				require.NoError(t, marshalErr)
				var got map[string]any
				require.NoError(t, json.Unmarshal(raw, &got))
				assert.Equal(t, "cursor-2", got["next_cursor"])
				assert.Equal(t, true, got["has_more"])
				assert.Len(t, got["items"], 1)
				assert.NotContains(t, got, "total_count", "a plain paged request must not report a table total it never queried")
				assert.NotContains(t, got, "count", "the pre-ADR-0159 field name is gone; total_count replaced it")
				assert.NotContains(t, got, "next_fire_at")
			},
		},
		"total=true → aggregates present, and the filter still asks the store for no total": {
			query: httpcore.ListArmedTimersQuery{Limit: 1, Cursor: "opaque-cursor", IncludeTotal: true},
			buildTA: func(t *testing.T) service.TimerAdmin {
				t.Helper()
				m := service.NewMockTimerAdmin(gomock.NewController(t))
				m.EXPECT().Stats(gomock.Any()).Return(kernel.TimerStats{Armed: 7, NextFireAt: &fireAt}, nil)
				// IncludeTotal MUST stay false here even though the request asked
				// for the total. Stats above already returns the count and
				// MIN(next_run) from ONE aggregate query, and NextFireAt cannot be
				// derived from ArmedTimerPage — so Stats runs regardless. Forwarding
				// IncludeTotal would make the store issue a SECOND count(*) whose
				// result AdminTimers then discards, i.e. total=true would cost more
				// database work than before ADR-0159. Do not "helpfully" re-add it.
				m.EXPECT().ListArmedPage(gomock.Any(), kernel.ArmedTimerFilter{
					Limit:        1,
					Cursor:       "opaque-cursor",
					IncludeTotal: false,
				}).Return(
					kernel.ArmedTimerPage{
						Items:      []kernel.ArmedTimer{{InstanceID: "inst-1", TimerID: "t1", NextRun: fireAt}},
						NextCursor: "cursor-2",
						HasMore:    true,
					}, nil)
				return m
			},
			assert: func(t *testing.T, status int, body any, err error) {
				require.NoError(t, err)
				assert.Equal(t, http.StatusOK, status)
				raw, marshalErr := json.Marshal(body)
				require.NoError(t, marshalErr)
				var got map[string]any
				require.NoError(t, json.Unmarshal(raw, &got))
				assert.EqualValues(t, 7, got["total_count"], "total_count is the table total from Stats, not the page size")
				assert.NotContains(t, got, "count", "the total is reported as total_count; a stray count would be the retired field name")
				assert.Len(t, got["items"], 1)
				assert.Contains(t, got, "next_fire_at")
			},
		},
		"limit above the maximum is clamped by the handler, not passed through": {
			query: httpcore.ListArmedTimersQuery{Limit: math.MaxInt},
			buildTA: func(t *testing.T) service.TimerAdmin {
				t.Helper()
				m := service.NewMockTimerAdmin(gomock.NewController(t))
				// The handler clamps via kernel.NormalizeLimit before the port is
				// reached, matching AdminListInstances. The SQL store clamps again
				// (idempotent, free), but service.TimerAdmin is a public port a
				// consumer may implement, so an unclamped limit must never arrive
				// at their code.
				m.EXPECT().ListArmedPage(gomock.Any(), kernel.ArmedTimerFilter{Limit: 200}).
					Return(kernel.ArmedTimerPage{}, nil)
				return m
			},
			assert: func(t *testing.T, status int, _ any, err error) {
				require.NoError(t, err)
				assert.Equal(t, http.StatusOK, status)
			},
		},
		"unset limit is defaulted by the handler, never sent as zero": {
			query: httpcore.ListArmedTimersQuery{},
			buildTA: func(t *testing.T) service.TimerAdmin {
				t.Helper()
				m := service.NewMockTimerAdmin(gomock.NewController(t))
				m.EXPECT().ListArmedPage(gomock.Any(), kernel.ArmedTimerFilter{Limit: 50}).
					Return(kernel.ArmedTimerPage{}, nil)
				return m
			},
			assert: func(t *testing.T, status int, _ any, err error) {
				require.NoError(t, err)
				assert.Equal(t, http.StatusOK, status)
			},
		},
		"malformed cursor → 400, never a silent reset to page one": {
			query: httpcore.ListArmedTimersQuery{Cursor: "!!!"},
			buildTA: func(t *testing.T) service.TimerAdmin {
				t.Helper()
				m := service.NewMockTimerAdmin(gomock.NewController(t))
				m.EXPECT().ListArmedPage(gomock.Any(), gomock.Any()).
					Return(kernel.ArmedTimerPage{}, fmt.Errorf("wrap: %w", kernel.ErrBadArmedTimerCursor))
				return m
			},
			assert: func(t *testing.T, status int, body any, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, kernel.ErrBadArmedTimerCursor)
				gotStatus, _ := httpcore.ClassifyError(err)
				assert.Equal(t, http.StatusBadRequest, gotStatus)
				assert.Zero(t, status)
				assert.Nil(t, body)
			},
		},
		"stats error → propagated": {
			query: httpcore.ListArmedTimersQuery{IncludeTotal: true},
			buildTA: func(t *testing.T) service.TimerAdmin {
				t.Helper()
				m := service.NewMockTimerAdmin(gomock.NewController(t))
				m.EXPECT().Stats(gomock.Any()).Return(kernel.TimerStats{}, errors.New("db error"))
				m.EXPECT().ListArmedPage(gomock.Any(), gomock.Any()).Return(kernel.ArmedTimerPage{}, nil).AnyTimes()
				return m
			},
			assert: func(t *testing.T, status int, body any, err error) {
				require.Error(t, err)
				assert.Zero(t, status)
				assert.Nil(t, body)
			},
		},
		"listArmedPage error → propagated": {
			buildTA: func(t *testing.T) service.TimerAdmin {
				t.Helper()
				m := service.NewMockTimerAdmin(gomock.NewController(t))
				m.EXPECT().ListArmedPage(gomock.Any(), gomock.Any()).
					Return(kernel.ArmedTimerPage{}, errors.New("list error"))
				return m
			},
			assert: func(t *testing.T, status int, body any, err error) {
				require.Error(t, err)
				assert.Zero(t, status)
				assert.Nil(t, body)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			status, body, err := httpcore.AdminTimers(t.Context(), tc.buildTA(t), tc.query)
			tc.assert(t, status, body, err)
		})
	}
}

// TestAdminInstanceLineage exercises httpcore.AdminInstanceLineage.
func TestAdminInstanceLineage(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		buildLA    func(t *testing.T) service.LineageAdmin
		instanceID string
		assert     func(t *testing.T, status int, body any, err error)
	}{
		"root instance → 200 with lineage": {
			buildLA: func(t *testing.T) service.LineageAdmin {
				t.Helper()
				m := service.NewMockLineageAdmin(gomock.NewController(t))
				m.EXPECT().Lineage(gomock.Any(), "inst-root").Return(
					kernel.InstanceLineage{
						InstanceID:      "inst-root",
						CallChildren:    []kernel.CallLinkRef{},
						ChainSuccessors: []kernel.ChainLinkRef{},
					}, nil)
				return m
			},
			instanceID: "inst-root",
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
		"instance with call parent → 200 parent populated": {
			buildLA: func(t *testing.T) service.LineageAdmin {
				t.Helper()
				m := service.NewMockLineageAdmin(gomock.NewController(t))
				m.EXPECT().Lineage(gomock.Any(), "inst-with-parent").Return(
					kernel.InstanceLineage{
						InstanceID: "inst-with-parent",
						CallParent: &kernel.CallLinkRef{
							InstanceID: "parent-inst", DefID: "parent-def", DefVersion: 1, Depth: 0,
						},
						CallChildren:     []kernel.CallLinkRef{{InstanceID: "child-inst", DefID: "", DefVersion: 0, Depth: 1}},
						ChainPredecessor: &kernel.ChainLinkRef{InstanceID: "pred-inst", DefinitionRef: model.Version("pred-def", 1), Outcome: "approved"},
						ChainSuccessors:  []kernel.ChainLinkRef{{InstanceID: "succ-inst", DefinitionRef: model.Version("succ-def", 1), Outcome: "done"}},
					}, nil)
				return m
			},
			instanceID: "inst-with-parent",
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
		"service error → propagated": {
			buildLA: func(t *testing.T) service.LineageAdmin {
				t.Helper()
				m := service.NewMockLineageAdmin(gomock.NewController(t))
				m.EXPECT().Lineage(gomock.Any(), "no-such-inst").Return(kernel.InstanceLineage{}, errors.New("not found"))
				return m
			},
			instanceID: "no-such-inst",
			assert: func(t *testing.T, status int, body any, err error) {
				if err == nil {
					t.Fatal("want error from lineage")
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
			status, body, err := httpcore.AdminInstanceLineage(t.Context(), tc.buildLA(t), tc.instanceID)
			tc.assert(t, status, body, err)
		})
	}
}

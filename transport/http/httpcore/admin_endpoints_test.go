package httpcore_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/zakyalvan/krtlwrkflw/internal/transporttest"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/monitor"
	"github.com/zakyalvan/krtlwrkflw/service"
	"github.com/zakyalvan/krtlwrkflw/transport/http/httpcore"
)

// --- in-mem fakes for admin sub-interfaces ---

// fakePolicyAdmin is a hand-written test double for service.PolicyAdmin.
type fakePolicyAdmin struct {
	addPolicyFn    func(ctx context.Context, r service.PolicyRule) (bool, error)
	removePolicyFn func(ctx context.Context, r service.PolicyRule) (bool, error)
	listPoliciesFn func(ctx context.Context) ([]service.PolicyRule, error)
	addRoleFn      func(ctx context.Context, b service.RoleBinding) (bool, error)
	removeRoleFn   func(ctx context.Context, b service.RoleBinding) (bool, error)
	listRolesFn    func(ctx context.Context) ([]service.RoleBinding, error)
}

func (f *fakePolicyAdmin) AddPolicy(ctx context.Context, r service.PolicyRule) (bool, error) {
	return f.addPolicyFn(ctx, r)
}
func (f *fakePolicyAdmin) RemovePolicy(ctx context.Context, r service.PolicyRule) (bool, error) {
	return f.removePolicyFn(ctx, r)
}
func (f *fakePolicyAdmin) ListPolicies(ctx context.Context) ([]service.PolicyRule, error) {
	return f.listPoliciesFn(ctx)
}
func (f *fakePolicyAdmin) AddRole(ctx context.Context, b service.RoleBinding) (bool, error) {
	return f.addRoleFn(ctx, b)
}
func (f *fakePolicyAdmin) RemoveRole(ctx context.Context, b service.RoleBinding) (bool, error) {
	return f.removeRoleFn(ctx, b)
}
func (f *fakePolicyAdmin) ListRoles(ctx context.Context) ([]service.RoleBinding, error) {
	return f.listRolesFn(ctx)
}

// fakeDLQAdmin is a hand-written test double for service.DeadLetterAdmin.
type fakeDLQAdmin struct {
	listFn   func(ctx context.Context, limit int) ([]monitor.DeadLetter, error)
	redriveFn func(ctx context.Context, ids ...int64) (int, error)
}

func (f *fakeDLQAdmin) ListDeadLettered(ctx context.Context, limit int) ([]monitor.DeadLetter, error) {
	return f.listFn(ctx, limit)
}
func (f *fakeDLQAdmin) Redrive(ctx context.Context, ids ...int64) (int, error) {
	return f.redriveFn(ctx, ids...)
}

// fakeRelayStatsAdmin is a hand-written test double for service.RelayStatsAdmin.
type fakeRelayStatsAdmin struct {
	statsFn func(ctx context.Context) (kernel.OutboxStats, error)
}

func (f *fakeRelayStatsAdmin) OutboxStats(ctx context.Context) (kernel.OutboxStats, error) {
	return f.statsFn(ctx)
}

// fakeTimerAdmin is a hand-written test double for service.TimerAdmin.
type fakeTimerAdmin struct {
	statsFn    func(ctx context.Context) (kernel.TimerStats, error)
	listArmedFn func(ctx context.Context) ([]kernel.ArmedTimer, error)
}

func (f *fakeTimerAdmin) Stats(ctx context.Context) (kernel.TimerStats, error) {
	return f.statsFn(ctx)
}
func (f *fakeTimerAdmin) ListArmed(ctx context.Context) ([]kernel.ArmedTimer, error) {
	return f.listArmedFn(ctx)
}

// fakeLineageAdmin is a hand-written test double for service.LineageAdmin.
type fakeLineageAdmin struct {
	lineageFn func(ctx context.Context, instanceID string) (kernel.InstanceLineage, error)
}

func (f *fakeLineageAdmin) Lineage(ctx context.Context, instanceID string) (kernel.InstanceLineage, error) {
	return f.lineageFn(ctx, instanceID)
}

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
					DefRef: "greeting", InstanceID: "admin-list-ok-1", Vars: map[string]any{"name": "z"},
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
	}

	for name, tc := range tests {
		tc := tc
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
		setup      func(svc service.Service) (instanceID, incidentID string)
		in         httpcore.ResolveIncidentInput
		assert     func(t *testing.T, status int, body any, err error)
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
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, svc := transporttest.NewHarness(t, def)
			instanceID, incidentID := tc.setup(svc)
			status, body, err := httpcore.ResolveIncident(t.Context(), svc, instanceID, incidentID, tc.in)
			tc.assert(t, status, body, err)
		})
	}
}

// TestCancelInstance exercises httpcore.CancelInstance.
func TestCancelInstance(t *testing.T) {
	t.Parallel()

	def := transporttest.ApprovalProcess()

	tests := map[string]struct {
		setup      func(svc service.Service) string
		assert     func(t *testing.T, status int, body any, err error)
	}{
		"running instance → 200 with body": {
			setup: func(svc service.Service) string {
				_, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
					DefRef: "approval", InstanceID: "cancel-ok-1",
				})
				if err != nil {
					t.Fatalf("StartInstance: %v", err)
				}
				return "cancel-ok-1"
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
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, svc := transporttest.NewHarness(t, def)
			instanceID := tc.setup(svc)
			status, body, err := httpcore.CancelInstance(t.Context(), svc, instanceID)
			tc.assert(t, status, body, err)
		})
	}
}

// TestListDeadLetters exercises httpcore.ListDeadLetters.
func TestListDeadLetters(t *testing.T) {
	t.Parallel()

	now := time.Now()

	tests := map[string]struct {
		dla    service.DeadLetterAdmin
		q      httpcore.DeadLetterQuery
		assert func(t *testing.T, status int, body any, err error)
	}{
		"empty list → 200 empty items": {
			dla: &fakeDLQAdmin{
				listFn: func(_ context.Context, _ int) ([]monitor.DeadLetter, error) {
					return nil, nil
				},
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
			dla: &fakeDLQAdmin{
				listFn: func(_ context.Context, _ int) ([]monitor.DeadLetter, error) {
					return []monitor.DeadLetter{
						{ID: 1, InstanceID: "inst-1", Topic: "instance.failed", RetryCount: 3, LastError: "timeout", CreatedAt: now},
					}, nil
				},
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
			dla: &fakeDLQAdmin{
				listFn: func(_ context.Context, _ int) ([]monitor.DeadLetter, error) {
					return nil, errors.New("db error")
				},
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
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			status, body, err := httpcore.ListDeadLetters(t.Context(), tc.dla, tc.q)
			tc.assert(t, status, body, err)
		})
	}
}

// TestRedriveDeadLetters exercises httpcore.RedriveDeadLetters.
func TestRedriveDeadLetters(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		dla    service.DeadLetterAdmin
		in     httpcore.RedriveInput
		assert func(t *testing.T, status int, body any, err error)
	}{
		"redrive two → 200 redriven:2": {
			dla: &fakeDLQAdmin{
				redriveFn: func(_ context.Context, ids ...int64) (int, error) {
					return len(ids), nil
				},
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
			dla: &fakeDLQAdmin{
				redriveFn: func(_ context.Context, ids ...int64) (int, error) {
					return 0, nil
				},
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
			dla: &fakeDLQAdmin{
				redriveFn: func(_ context.Context, ids ...int64) (int, error) {
					return 0, errors.New("db error")
				},
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
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			status, body, err := httpcore.RedriveDeadLetters(t.Context(), tc.dla, tc.in)
			tc.assert(t, status, body, err)
		})
	}
}

// TestListPolicies exercises httpcore.ListPolicies.
func TestListPolicies(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		pa     service.PolicyAdmin
		assert func(t *testing.T, status int, body any, err error)
	}{
		"success → 200 with policies": {
			pa: &fakePolicyAdmin{
				listPoliciesFn: func(_ context.Context) ([]service.PolicyRule, error) {
					return []service.PolicyRule{{Subject: "alice", Object: "/orders", Action: "read"}}, nil
				},
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
			pa: &fakePolicyAdmin{
				listPoliciesFn: func(_ context.Context) ([]service.PolicyRule, error) {
					return nil, nil
				},
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
			pa: &fakePolicyAdmin{
				listPoliciesFn: func(_ context.Context) ([]service.PolicyRule, error) {
					return nil, errors.New("casbin error")
				},
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
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			status, body, err := httpcore.ListPolicies(t.Context(), tc.pa)
			tc.assert(t, status, body, err)
		})
	}
}

// TestAddPolicy exercises httpcore.AddPolicy.
func TestAddPolicy(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		pa     service.PolicyAdmin
		in     httpcore.PolicyRuleInput
		assert func(t *testing.T, status int, body any, err error)
	}{
		"new policy → 200 added:true": {
			pa: &fakePolicyAdmin{
				addPolicyFn: func(_ context.Context, r service.PolicyRule) (bool, error) {
					return true, nil
				},
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
			pa: &fakePolicyAdmin{
				addPolicyFn: func(_ context.Context, r service.PolicyRule) (bool, error) {
					return false, nil
				},
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
			pa: &fakePolicyAdmin{
				addPolicyFn: func(_ context.Context, r service.PolicyRule) (bool, error) {
					return false, errors.New("casbin error")
				},
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
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			status, body, err := httpcore.AddPolicy(t.Context(), tc.pa, tc.in)
			tc.assert(t, status, body, err)
		})
	}
}

// TestRemovePolicy exercises httpcore.RemovePolicy.
func TestRemovePolicy(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		pa     service.PolicyAdmin
		in     httpcore.PolicyRuleInput
		assert func(t *testing.T, status int, body any, err error)
	}{
		"exists → 200 removed:true": {
			pa: &fakePolicyAdmin{
				removePolicyFn: func(_ context.Context, r service.PolicyRule) (bool, error) {
					return true, nil
				},
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
			pa: &fakePolicyAdmin{
				removePolicyFn: func(_ context.Context, r service.PolicyRule) (bool, error) {
					return false, nil
				},
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
			pa: &fakePolicyAdmin{
				removePolicyFn: func(_ context.Context, r service.PolicyRule) (bool, error) {
					return false, errors.New("casbin error")
				},
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
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			status, body, err := httpcore.RemovePolicy(t.Context(), tc.pa, tc.in)
			tc.assert(t, status, body, err)
		})
	}
}

// TestListRoleBindings exercises httpcore.ListRoleBindings.
func TestListRoleBindings(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		pa     service.PolicyAdmin
		assert func(t *testing.T, status int, body any, err error)
	}{
		"success → 200 with bindings": {
			pa: &fakePolicyAdmin{
				listRolesFn: func(_ context.Context) ([]service.RoleBinding, error) {
					return []service.RoleBinding{{User: "alice", Role: "admin"}}, nil
				},
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
			pa: &fakePolicyAdmin{
				listRolesFn: func(_ context.Context) ([]service.RoleBinding, error) {
					return nil, errors.New("casbin error")
				},
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
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			status, body, err := httpcore.ListRoleBindings(t.Context(), tc.pa)
			tc.assert(t, status, body, err)
		})
	}
}

// TestAddRoleBinding exercises httpcore.AddRoleBinding.
func TestAddRoleBinding(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		pa     service.PolicyAdmin
		in     httpcore.RoleBindingInput
		assert func(t *testing.T, status int, body any, err error)
	}{
		"success → 200 added:true": {
			pa: &fakePolicyAdmin{
				addRoleFn: func(_ context.Context, b service.RoleBinding) (bool, error) {
					return true, nil
				},
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
			pa: &fakePolicyAdmin{
				addRoleFn: func(_ context.Context, b service.RoleBinding) (bool, error) {
					return false, nil
				},
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
			pa: &fakePolicyAdmin{
				addRoleFn: func(_ context.Context, b service.RoleBinding) (bool, error) {
					return false, errors.New("casbin error")
				},
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
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			status, body, err := httpcore.AddRoleBinding(t.Context(), tc.pa, tc.in)
			tc.assert(t, status, body, err)
		})
	}
}

// TestRemoveRoleBinding exercises httpcore.RemoveRoleBinding.
func TestRemoveRoleBinding(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		pa     service.PolicyAdmin
		in     httpcore.RoleBindingInput
		assert func(t *testing.T, status int, body any, err error)
	}{
		"exists → 200 removed:true": {
			pa: &fakePolicyAdmin{
				removeRoleFn: func(_ context.Context, b service.RoleBinding) (bool, error) {
					return true, nil
				},
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
			pa: &fakePolicyAdmin{
				removeRoleFn: func(_ context.Context, b service.RoleBinding) (bool, error) {
					return false, nil
				},
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
			pa: &fakePolicyAdmin{
				removeRoleFn: func(_ context.Context, b service.RoleBinding) (bool, error) {
					return false, errors.New("casbin error")
				},
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
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			status, body, err := httpcore.RemoveRoleBinding(t.Context(), tc.pa, tc.in)
			tc.assert(t, status, body, err)
		})
	}
}

// TestAdminRelayStats exercises httpcore.AdminRelayStats.
func TestAdminRelayStats(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		rsa    service.RelayStatsAdmin
		assert func(t *testing.T, status int, body any, err error)
	}{
		"success → 200 with stats": {
			rsa: &fakeRelayStatsAdmin{
				statsFn: func(_ context.Context) (kernel.OutboxStats, error) {
					return kernel.OutboxStats{Pending: 5, Dead: 1, OldestPendingAge: 10 * time.Second}, nil
				},
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
			rsa: &fakeRelayStatsAdmin{
				statsFn: func(_ context.Context) (kernel.OutboxStats, error) {
					return kernel.OutboxStats{}, errors.New("db error")
				},
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
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			status, body, err := httpcore.AdminRelayStats(t.Context(), tc.rsa)
			tc.assert(t, status, body, err)
		})
	}
}

// TestAdminTimers exercises httpcore.AdminTimers.
func TestAdminTimers(t *testing.T) {
	t.Parallel()

	fireAt := time.Now().Add(5 * time.Minute)

	tests := map[string]struct {
		ta     service.TimerAdmin
		assert func(t *testing.T, status int, body any, err error)
	}{
		"success → 200 with timer list": {
			ta: &fakeTimerAdmin{
				statsFn: func(_ context.Context) (kernel.TimerStats, error) {
					return kernel.TimerStats{Armed: 1, NextFireAt: &fireAt}, nil
				},
				listArmedFn: func(_ context.Context) ([]kernel.ArmedTimer, error) {
					return []kernel.ArmedTimer{
						{InstanceID: "inst-1", DefID: "d", DefVersion: 1, TimerID: "t1", FireAt: fireAt},
					}, nil
				},
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
		"stats error → propagated": {
			ta: &fakeTimerAdmin{
				statsFn: func(_ context.Context) (kernel.TimerStats, error) {
					return kernel.TimerStats{}, errors.New("db error")
				},
				listArmedFn: func(_ context.Context) ([]kernel.ArmedTimer, error) {
					return nil, nil
				},
			},
			assert: func(t *testing.T, status int, body any, err error) {
				if err == nil {
					t.Fatal("want error from stats")
				}
				if status != 0 || body != nil {
					t.Fatalf("want (0, nil) on error, got (%d, %v)", status, body)
				}
			},
		},
	}

	for name, tc := range tests {
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			status, body, err := httpcore.AdminTimers(t.Context(), tc.ta)
			tc.assert(t, status, body, err)
		})
	}
}

// TestAdminInstanceLineage exercises httpcore.AdminInstanceLineage.
func TestAdminInstanceLineage(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		la         service.LineageAdmin
		instanceID string
		assert     func(t *testing.T, status int, body any, err error)
	}{
		"root instance → 200 with lineage": {
			la: &fakeLineageAdmin{
				lineageFn: func(_ context.Context, instanceID string) (kernel.InstanceLineage, error) {
					return kernel.InstanceLineage{
						InstanceID:      instanceID,
						CallChildren:    []kernel.CallLinkRef{},
						ChainSuccessors: []kernel.ChainLinkRef{},
					}, nil
				},
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
		"service error → propagated": {
			la: &fakeLineageAdmin{
				lineageFn: func(_ context.Context, _ string) (kernel.InstanceLineage, error) {
					return kernel.InstanceLineage{}, errors.New("not found")
				},
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
		tc := tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			status, body, err := httpcore.AdminInstanceLineage(t.Context(), tc.la, tc.instanceID)
			tc.assert(t, status, body, err)
		})
	}
}

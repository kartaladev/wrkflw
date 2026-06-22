package grpctransport_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/zakyalvan/krtlwrkflw/service"
	grpctransport "github.com/zakyalvan/krtlwrkflw/transport/grpc"
	"github.com/zakyalvan/krtlwrkflw/transport/grpc/workflowpb"
)

// paStub is a configurable service.PolicyAdmin test double.
type paStub struct {
	addPolicyFn    func(ctx context.Context, r service.PolicyRule) (bool, error)
	removePolicyFn func(ctx context.Context, r service.PolicyRule) (bool, error)
	listPoliciesFn func(ctx context.Context) ([]service.PolicyRule, error)
	addRoleFn      func(ctx context.Context, b service.RoleBinding) (bool, error)
	removeRoleFn   func(ctx context.Context, b service.RoleBinding) (bool, error)
	listRolesFn    func(ctx context.Context) ([]service.RoleBinding, error)
}

func (s *paStub) AddPolicy(ctx context.Context, r service.PolicyRule) (bool, error) {
	return s.addPolicyFn(ctx, r)
}
func (s *paStub) RemovePolicy(ctx context.Context, r service.PolicyRule) (bool, error) {
	return s.removePolicyFn(ctx, r)
}
func (s *paStub) ListPolicies(ctx context.Context) ([]service.PolicyRule, error) {
	return s.listPoliciesFn(ctx)
}
func (s *paStub) AddRole(ctx context.Context, b service.RoleBinding) (bool, error) {
	return s.addRoleFn(ctx, b)
}
func (s *paStub) RemoveRole(ctx context.Context, b service.RoleBinding) (bool, error) {
	return s.removeRoleFn(ctx, b)
}
func (s *paStub) ListRoles(ctx context.Context) ([]service.RoleBinding, error) {
	return s.listRolesFn(ctx)
}

func TestServerListPolicies(t *testing.T) {
	t.Parallel()

	t.Run("wired returns items", func(t *testing.T) {
		t.Parallel()
		pa := &paStub{
			listPoliciesFn: func(_ context.Context) ([]service.PolicyRule, error) {
				return []service.PolicyRule{
					{Subject: "alice", Object: "/orders", Action: "read"},
					{Subject: "bob", Object: "/invoices", Action: "write"},
				}, nil
			},
		}
		client := newStubHarnessWithOpts(t, &resolveStub{}, grpctransport.WithPolicyAdmin(pa))
		resp, err := client.ListPolicies(t.Context(), &workflowpb.ListPoliciesRequest{})
		require.NoError(t, err)
		require.Len(t, resp.GetPolicies(), 2)
		assert.Equal(t, "alice", resp.GetPolicies()[0].GetSubject())
		assert.Equal(t, "/orders", resp.GetPolicies()[0].GetObject())
		assert.Equal(t, "read", resp.GetPolicies()[0].GetAction())
		assert.Equal(t, "bob", resp.GetPolicies()[1].GetSubject())
	})

	t.Run("not wired returns Unimplemented", func(t *testing.T) {
		t.Parallel()
		client := newStubHarnessWithOpts(t, &resolveStub{})
		_, err := client.ListPolicies(t.Context(), &workflowpb.ListPoliciesRequest{})
		assert.Equal(t, codes.Unimplemented, status.Code(err))
	})
}

func TestServerAddPolicy(t *testing.T) {
	t.Parallel()

	t.Run("wired returns ok=true", func(t *testing.T) {
		t.Parallel()
		var got service.PolicyRule
		pa := &paStub{
			addPolicyFn: func(_ context.Context, r service.PolicyRule) (bool, error) {
				got = r
				return true, nil
			},
		}
		client := newStubHarnessWithOpts(t, &resolveStub{}, grpctransport.WithPolicyAdmin(pa))
		resp, err := client.AddPolicy(t.Context(), &workflowpb.AddPolicyRequest{
			Rule: &workflowpb.PolicyRule{Subject: "alice", Object: "/orders", Action: "read"},
		})
		require.NoError(t, err)
		assert.True(t, resp.GetOk())
		assert.Equal(t, service.PolicyRule{Subject: "alice", Object: "/orders", Action: "read"}, got)
	})

	t.Run("wired returns ok=false when already exists", func(t *testing.T) {
		t.Parallel()
		pa := &paStub{
			addPolicyFn: func(_ context.Context, _ service.PolicyRule) (bool, error) {
				return false, nil
			},
		}
		client := newStubHarnessWithOpts(t, &resolveStub{}, grpctransport.WithPolicyAdmin(pa))
		resp, err := client.AddPolicy(t.Context(), &workflowpb.AddPolicyRequest{
			Rule: &workflowpb.PolicyRule{Subject: "alice", Object: "/orders", Action: "read"},
		})
		require.NoError(t, err)
		assert.False(t, resp.GetOk())
	})

	t.Run("not wired returns Unimplemented", func(t *testing.T) {
		t.Parallel()
		client := newStubHarnessWithOpts(t, &resolveStub{})
		_, err := client.AddPolicy(t.Context(), &workflowpb.AddPolicyRequest{
			Rule: &workflowpb.PolicyRule{Subject: "alice", Object: "/orders", Action: "read"},
		})
		assert.Equal(t, codes.Unimplemented, status.Code(err))
	})
}

func TestServerRemovePolicy(t *testing.T) {
	t.Parallel()

	t.Run("wired returns ok=true", func(t *testing.T) {
		t.Parallel()
		pa := &paStub{
			removePolicyFn: func(_ context.Context, _ service.PolicyRule) (bool, error) {
				return true, nil
			},
		}
		client := newStubHarnessWithOpts(t, &resolveStub{}, grpctransport.WithPolicyAdmin(pa))
		resp, err := client.RemovePolicy(t.Context(), &workflowpb.RemovePolicyRequest{
			Rule: &workflowpb.PolicyRule{Subject: "alice", Object: "/orders", Action: "read"},
		})
		require.NoError(t, err)
		assert.True(t, resp.GetOk())
	})

	t.Run("not wired returns Unimplemented", func(t *testing.T) {
		t.Parallel()
		client := newStubHarnessWithOpts(t, &resolveStub{})
		_, err := client.RemovePolicy(t.Context(), &workflowpb.RemovePolicyRequest{
			Rule: &workflowpb.PolicyRule{Subject: "alice", Object: "/orders", Action: "read"},
		})
		assert.Equal(t, codes.Unimplemented, status.Code(err))
	})
}

func TestServerAddRole(t *testing.T) {
	t.Parallel()

	t.Run("wired returns ok=true", func(t *testing.T) {
		t.Parallel()
		var got service.RoleBinding
		pa := &paStub{
			addRoleFn: func(_ context.Context, b service.RoleBinding) (bool, error) {
				got = b
				return true, nil
			},
		}
		client := newStubHarnessWithOpts(t, &resolveStub{}, grpctransport.WithPolicyAdmin(pa))
		resp, err := client.AddRole(t.Context(), &workflowpb.AddRoleRequest{
			Binding: &workflowpb.RoleBinding{User: "alice", Role: "manager"},
		})
		require.NoError(t, err)
		assert.True(t, resp.GetOk())
		assert.Equal(t, service.RoleBinding{User: "alice", Role: "manager"}, got)
	})

	t.Run("not wired returns Unimplemented", func(t *testing.T) {
		t.Parallel()
		client := newStubHarnessWithOpts(t, &resolveStub{})
		_, err := client.AddRole(t.Context(), &workflowpb.AddRoleRequest{
			Binding: &workflowpb.RoleBinding{User: "alice", Role: "manager"},
		})
		assert.Equal(t, codes.Unimplemented, status.Code(err))
	})
}

func TestServerRemoveRole(t *testing.T) {
	t.Parallel()

	t.Run("wired returns ok=true", func(t *testing.T) {
		t.Parallel()
		pa := &paStub{
			removeRoleFn: func(_ context.Context, _ service.RoleBinding) (bool, error) {
				return true, nil
			},
		}
		client := newStubHarnessWithOpts(t, &resolveStub{}, grpctransport.WithPolicyAdmin(pa))
		resp, err := client.RemoveRole(t.Context(), &workflowpb.RemoveRoleRequest{
			Binding: &workflowpb.RoleBinding{User: "alice", Role: "manager"},
		})
		require.NoError(t, err)
		assert.True(t, resp.GetOk())
	})

	t.Run("not wired returns Unimplemented", func(t *testing.T) {
		t.Parallel()
		client := newStubHarnessWithOpts(t, &resolveStub{})
		_, err := client.RemoveRole(t.Context(), &workflowpb.RemoveRoleRequest{
			Binding: &workflowpb.RoleBinding{User: "alice", Role: "manager"},
		})
		assert.Equal(t, codes.Unimplemented, status.Code(err))
	})
}

func TestServerListRoles(t *testing.T) {
	t.Parallel()

	t.Run("wired returns items", func(t *testing.T) {
		t.Parallel()
		pa := &paStub{
			listRolesFn: func(_ context.Context) ([]service.RoleBinding, error) {
				return []service.RoleBinding{
					{User: "alice", Role: "manager"},
					{User: "bob", Role: "viewer"},
				}, nil
			},
		}
		client := newStubHarnessWithOpts(t, &resolveStub{}, grpctransport.WithPolicyAdmin(pa))
		resp, err := client.ListRoles(t.Context(), &workflowpb.ListRolesRequest{})
		require.NoError(t, err)
		require.Len(t, resp.GetRoleBindings(), 2)
		assert.Equal(t, "alice", resp.GetRoleBindings()[0].GetUser())
		assert.Equal(t, "manager", resp.GetRoleBindings()[0].GetRole())
	})

	t.Run("not wired returns Unimplemented", func(t *testing.T) {
		t.Parallel()
		client := newStubHarnessWithOpts(t, &resolveStub{})
		_, err := client.ListRoles(t.Context(), &workflowpb.ListRolesRequest{})
		assert.Equal(t, codes.Unimplemented, status.Code(err))
	})
}

func TestWithPolicyAdminNilPanics(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() { grpctransport.WithPolicyAdmin(nil) })
}

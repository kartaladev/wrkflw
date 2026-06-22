package rest_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/zakyalvan/krtlwrkflw/service"
	"github.com/zakyalvan/krtlwrkflw/transport/rest"
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

func paHandler(pa service.PolicyAdmin) http.Handler {
	return rest.NewHandler(&dlqStubService{}, rest.WithAdminMiddleware(allowAdmin), rest.WithPolicyAdmin(pa))
}

// ---------- GET /admin/policies ----------

func TestRESTListPolicies(t *testing.T) {
	t.Parallel()

	t.Run("wired + admin-allow returns policies", func(t *testing.T) {
		t.Parallel()
		pa := &paStub{listPoliciesFn: func(_ context.Context) ([]service.PolicyRule, error) {
			return []service.PolicyRule{{Subject: "alice", Object: "/orders", Action: "read"}}, nil
		}}
		h := paHandler(pa)
		rec := doReq(t, h, http.MethodGet, "/admin/policies", "")
		assert.Equal(t, http.StatusOK, rec.Code)
		body := rec.Body.String()
		assert.Contains(t, body, `"policies"`)
		assert.Contains(t, body, `"alice"`)
		assert.Contains(t, body, `"/orders"`)
		assert.Contains(t, body, `"read"`)
	})

	t.Run("default-deny without admin middleware -> 403", func(t *testing.T) {
		t.Parallel()
		pa := &paStub{listPoliciesFn: func(_ context.Context) ([]service.PolicyRule, error) { return nil, nil }}
		h := rest.NewHandler(&dlqStubService{}, rest.WithPolicyAdmin(pa))
		rec := doReq(t, h, http.MethodGet, "/admin/policies", "")
		assert.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("not wired -> 404", func(t *testing.T) {
		t.Parallel()
		h := rest.NewHandler(&dlqStubService{}, rest.WithAdminMiddleware(allowAdmin))
		rec := doReq(t, h, http.MethodGet, "/admin/policies", "")
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
}

// ---------- POST /admin/policies ----------

func TestRESTAddPolicy(t *testing.T) {
	t.Parallel()

	t.Run("wired + admin-allow adds policy -> added:true", func(t *testing.T) {
		t.Parallel()
		pa := &paStub{addPolicyFn: func(_ context.Context, r service.PolicyRule) (bool, error) {
			return true, nil
		}}
		h := paHandler(pa)
		rec := doReq(t, h, http.MethodPost, "/admin/policies", `{"subject":"alice","object":"/orders","action":"read"}`)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), `"added":true`)
	})

	t.Run("already exists -> added:false", func(t *testing.T) {
		t.Parallel()
		pa := &paStub{addPolicyFn: func(_ context.Context, r service.PolicyRule) (bool, error) {
			return false, nil
		}}
		h := paHandler(pa)
		rec := doReq(t, h, http.MethodPost, "/admin/policies", `{"subject":"alice","object":"/orders","action":"read"}`)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), `"added":false`)
	})

	t.Run("not wired -> 404", func(t *testing.T) {
		t.Parallel()
		h := rest.NewHandler(&dlqStubService{}, rest.WithAdminMiddleware(allowAdmin))
		rec := doReq(t, h, http.MethodPost, "/admin/policies", `{"subject":"alice","object":"/orders","action":"read"}`)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("malformed body -> 400", func(t *testing.T) {
		t.Parallel()
		pa := &paStub{addPolicyFn: func(_ context.Context, r service.PolicyRule) (bool, error) { return true, nil }}
		h := paHandler(pa)
		rec := doReq(t, h, http.MethodPost, "/admin/policies", `not-json`)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("default-deny without admin middleware -> 403", func(t *testing.T) {
		t.Parallel()
		pa := &paStub{addPolicyFn: func(_ context.Context, r service.PolicyRule) (bool, error) { return true, nil }}
		h := rest.NewHandler(&dlqStubService{}, rest.WithPolicyAdmin(pa))
		rec := doReq(t, h, http.MethodPost, "/admin/policies", `{"subject":"alice","object":"/orders","action":"read"}`)
		assert.Equal(t, http.StatusForbidden, rec.Code)
	})
}

// ---------- DELETE /admin/policies ----------

func TestRESTRemovePolicy(t *testing.T) {
	t.Parallel()

	t.Run("wired + admin-allow removes policy -> removed:true", func(t *testing.T) {
		t.Parallel()
		pa := &paStub{removePolicyFn: func(_ context.Context, r service.PolicyRule) (bool, error) {
			return true, nil
		}}
		h := paHandler(pa)
		rec := doReq(t, h, http.MethodDelete, "/admin/policies", `{"subject":"alice","object":"/orders","action":"read"}`)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), `"removed":true`)
	})

	t.Run("not found -> removed:false", func(t *testing.T) {
		t.Parallel()
		pa := &paStub{removePolicyFn: func(_ context.Context, r service.PolicyRule) (bool, error) {
			return false, nil
		}}
		h := paHandler(pa)
		rec := doReq(t, h, http.MethodDelete, "/admin/policies", `{"subject":"alice","object":"/orders","action":"read"}`)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), `"removed":false`)
	})

	t.Run("not wired -> 404", func(t *testing.T) {
		t.Parallel()
		h := rest.NewHandler(&dlqStubService{}, rest.WithAdminMiddleware(allowAdmin))
		rec := doReq(t, h, http.MethodDelete, "/admin/policies", `{"subject":"alice","object":"/orders","action":"read"}`)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("malformed body -> 400", func(t *testing.T) {
		t.Parallel()
		pa := &paStub{removePolicyFn: func(_ context.Context, r service.PolicyRule) (bool, error) { return true, nil }}
		h := paHandler(pa)
		rec := doReq(t, h, http.MethodDelete, "/admin/policies", `not-json`)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

// ---------- GET /admin/role-bindings ----------

func TestRESTListRoleBindings(t *testing.T) {
	t.Parallel()

	t.Run("wired + admin-allow returns role_bindings", func(t *testing.T) {
		t.Parallel()
		pa := &paStub{listRolesFn: func(_ context.Context) ([]service.RoleBinding, error) {
			return []service.RoleBinding{{User: "alice", Role: "admin"}}, nil
		}}
		h := paHandler(pa)
		rec := doReq(t, h, http.MethodGet, "/admin/role-bindings", "")
		assert.Equal(t, http.StatusOK, rec.Code)
		body := rec.Body.String()
		assert.Contains(t, body, `"role_bindings"`)
		assert.Contains(t, body, `"alice"`)
		assert.Contains(t, body, `"admin"`)
	})

	t.Run("default-deny without admin middleware -> 403", func(t *testing.T) {
		t.Parallel()
		pa := &paStub{listRolesFn: func(_ context.Context) ([]service.RoleBinding, error) { return nil, nil }}
		h := rest.NewHandler(&dlqStubService{}, rest.WithPolicyAdmin(pa))
		rec := doReq(t, h, http.MethodGet, "/admin/role-bindings", "")
		assert.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("not wired -> 404", func(t *testing.T) {
		t.Parallel()
		h := rest.NewHandler(&dlqStubService{}, rest.WithAdminMiddleware(allowAdmin))
		rec := doReq(t, h, http.MethodGet, "/admin/role-bindings", "")
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
}

// ---------- POST /admin/role-bindings ----------

func TestRESTAddRoleBinding(t *testing.T) {
	t.Parallel()

	t.Run("wired + admin-allow adds role binding -> added:true", func(t *testing.T) {
		t.Parallel()
		pa := &paStub{addRoleFn: func(_ context.Context, b service.RoleBinding) (bool, error) {
			return true, nil
		}}
		h := paHandler(pa)
		rec := doReq(t, h, http.MethodPost, "/admin/role-bindings", `{"user":"alice","role":"admin"}`)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), `"added":true`)
	})

	t.Run("not wired -> 404", func(t *testing.T) {
		t.Parallel()
		h := rest.NewHandler(&dlqStubService{}, rest.WithAdminMiddleware(allowAdmin))
		rec := doReq(t, h, http.MethodPost, "/admin/role-bindings", `{"user":"alice","role":"admin"}`)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("malformed body -> 400", func(t *testing.T) {
		t.Parallel()
		pa := &paStub{addRoleFn: func(_ context.Context, b service.RoleBinding) (bool, error) { return true, nil }}
		h := paHandler(pa)
		rec := doReq(t, h, http.MethodPost, "/admin/role-bindings", `not-json`)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("default-deny without admin middleware -> 403", func(t *testing.T) {
		t.Parallel()
		pa := &paStub{addRoleFn: func(_ context.Context, b service.RoleBinding) (bool, error) { return true, nil }}
		h := rest.NewHandler(&dlqStubService{}, rest.WithPolicyAdmin(pa))
		rec := doReq(t, h, http.MethodPost, "/admin/role-bindings", `{"user":"alice","role":"admin"}`)
		assert.Equal(t, http.StatusForbidden, rec.Code)
	})
}

// ---------- DELETE /admin/role-bindings ----------

func TestRESTRemoveRoleBinding(t *testing.T) {
	t.Parallel()

	t.Run("wired + admin-allow removes role binding -> removed:true", func(t *testing.T) {
		t.Parallel()
		pa := &paStub{removeRoleFn: func(_ context.Context, b service.RoleBinding) (bool, error) {
			return true, nil
		}}
		h := paHandler(pa)
		rec := doReq(t, h, http.MethodDelete, "/admin/role-bindings", `{"user":"alice","role":"admin"}`)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), `"removed":true`)
	})

	t.Run("not found -> removed:false", func(t *testing.T) {
		t.Parallel()
		pa := &paStub{removeRoleFn: func(_ context.Context, b service.RoleBinding) (bool, error) {
			return false, nil
		}}
		h := paHandler(pa)
		rec := doReq(t, h, http.MethodDelete, "/admin/role-bindings", `{"user":"alice","role":"admin"}`)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), `"removed":false`)
	})

	t.Run("not wired -> 404", func(t *testing.T) {
		t.Parallel()
		h := rest.NewHandler(&dlqStubService{}, rest.WithAdminMiddleware(allowAdmin))
		rec := doReq(t, h, http.MethodDelete, "/admin/role-bindings", `{"user":"alice","role":"admin"}`)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("malformed body -> 400", func(t *testing.T) {
		t.Parallel()
		pa := &paStub{removeRoleFn: func(_ context.Context, b service.RoleBinding) (bool, error) { return true, nil }}
		h := paHandler(pa)
		rec := doReq(t, h, http.MethodDelete, "/admin/role-bindings", `not-json`)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

// ---------- WithPolicyAdmin nil panics ----------

func TestRESTWithPolicyAdminNilPanics(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() { rest.WithPolicyAdmin(nil) })
}

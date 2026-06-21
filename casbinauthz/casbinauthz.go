// Package casbinauthz is the consumer-facing façade for the casbin-backed
// authz.Authorizer. It is the only module-root package that imports casbin; the
// concrete evaluator lives in internal/authz/casbin.
//
// Consumers wire this package directly:
//
//	a, err := casbinauthz.NewCasbinAuthorizerFromStrings("", policyCSV)
//	// or
//	a := casbinauthz.NewCasbinAuthorizer(syncedEnforcer)
//
// The returned authz.Authorizer is the stable port type; no internal types are
// exposed. The [Authorizer] concrete type additionally implements ReloadPolicy
// for hot-reloading the casbin policy without restarting the application.
package casbinauthz

import (
	"context"
	"fmt"

	casbinv2 "github.com/casbin/casbin/v2"
	casbinmodel "github.com/casbin/casbin/v2/model"
	stringadapter "github.com/casbin/casbin/v2/persist/string-adapter"

	"github.com/zakyalvan/krtlwrkflw/authz"
	internalcasbin "github.com/zakyalvan/krtlwrkflw/internal/authz/casbin"
)

// DefaultModel is a combined RBAC (g) + resource-privilege (p) casbin model used
// when [NewCasbinAuthorizerFromStrings] receives an empty model text.
//
// The matcher uses g(r.sub, p.sub) so that inherited roles (via g lines) are
// taken into account, and accepts wildcard "*" on both obj and act so that
// broad grant lines like `p, manager, approve, *` work alongside fine-grained
// ones. The attribute predicate is NOT modeled here — it is evaluated by
// expr-lang (see ADR-0010).
const DefaultModel = `
[request_definition]
r = sub, obj, act
[policy_definition]
p = sub, obj, act
[role_definition]
g = _, _
[policy_effect]
e = some(where (p.eft == allow))
[matchers]
m = g(r.sub, p.sub) && (r.obj == p.obj || p.obj == "*") && (r.act == p.act || p.act == "*")
`

// Authorizer is the module-root façade type. It satisfies [authz.Authorizer] and
// additionally exposes [Authorizer.ReloadPolicy] for consumers that want to
// hot-reload after a policy change without restarting the application.
//
// Obtain one via [NewCasbinAuthorizer] or [NewCasbinAuthorizerFromStrings];
// never construct it directly.
type Authorizer struct {
	inner    *internalcasbin.Authorizer
	enforcer *casbinv2.SyncedEnforcer
}

var _ authz.Authorizer = (*Authorizer)(nil)

// Authorize delegates to the internal casbin evaluator.
func (a *Authorizer) Authorize(ctx context.Context, spec authz.AuthzSpec, actor authz.Actor, vars map[string]any) error {
	return a.inner.Authorize(ctx, spec, actor, vars)
}

// ReloadPolicy reloads the enforcer's policy from its backing adapter. Useful
// when the policy CSV is stored externally (file, DB) and has been updated.
func (a *Authorizer) ReloadPolicy() error {
	if err := a.enforcer.LoadPolicy(); err != nil {
		return fmt.Errorf("casbinauthz: reload policy: %w", err)
	}
	return nil
}

// NewCasbinAuthorizer wraps a consumer-built [*casbin.SyncedEnforcer] and
// returns an [authz.Authorizer]. The returned value also implements
// ReloadPolicy (accessible via type assertion).
func NewCasbinAuthorizer(e *casbinv2.SyncedEnforcer) authz.Authorizer {
	return &Authorizer{inner: internalcasbin.New(e), enforcer: e}
}

// NewCasbinAuthorizerFromStrings builds a [*casbin.SyncedEnforcer] from plain
// model and policy strings (using casbin's bundled string adapter) and returns
// the authorizer. An empty modelText defaults to [DefaultModel].
//
// Returns an error if the model string is malformed or the enforcer cannot be
// initialised; never panics.
func NewCasbinAuthorizerFromStrings(modelText, policyText string) (authz.Authorizer, error) {
	if modelText == "" {
		modelText = DefaultModel
	}
	m, err := casbinmodel.NewModelFromString(modelText)
	if err != nil {
		return nil, fmt.Errorf("casbinauthz: parse model: %w", err)
	}
	e, err := casbinv2.NewSyncedEnforcer(m, stringadapter.NewAdapter(policyText))
	if err != nil {
		return nil, fmt.Errorf("casbinauthz: build enforcer: %w", err)
	}
	return NewCasbinAuthorizer(e), nil
}

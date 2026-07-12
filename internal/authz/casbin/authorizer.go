// Package casbin provides the casbin-backed implementation of authz.Authorizer.
// It is internal: consumers construct it through the module-root casbinauthz
// package. This is the only package (besides casbinauthz) that imports casbin.
package casbin

import (
	"context"
	"fmt"
	"strings"

	casbinv2 "github.com/casbin/casbin/v2"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/internal/expreval"
)

// Authorizer is a hybrid authz.Authorizer: casbin owns the RBAC role graph and
// resource-privilege enforcement; expr-lang (via expreval) owns the attribute
// predicate. It wraps a *casbin.SyncedEnforcer so concurrent Authorize calls are
// race-safe.
type Authorizer struct {
	enforcer *casbinv2.SyncedEnforcer
	attrEval *expreval.Evaluator
}

var _ authz.Authorizer = (*Authorizer)(nil)

// New constructs an Authorizer over the given synced enforcer.
func New(e *casbinv2.SyncedEnforcer) *Authorizer {
	return &Authorizer{enforcer: e, attrEval: expreval.New()}
}

// Authorize evaluates the three checks in order, short-circuiting on the first
// denial. An empty spec allows.
//
// Error asymmetry: A policy DENIAL (casbin Enforce returns false) or a failed
// role/attribute check returns authz.ErrNotAuthorized. A genuine casbin/expr
// ENGINE error (e.g., remote-adapter failure, expression parse error) is wrapped
// and propagated as a different error type, NOT ErrNotAuthorized. Both paths are
// fail-closed: the caller blocks the action on any non-nil error. In-memory
// adapter (string-based) cannot error; remote adapters may; expr-lang errors are
// mapped to ErrNotAuthorized by the attribute check.
func (a *Authorizer) Authorize(_ context.Context, spec authz.AuthzSpec, actor authz.Actor, vars map[string]any) error {
	// Step 1: role check with hierarchy.
	if len(spec.Roles) > 0 {
		eff, err := a.effectiveRoles(actor)
		if err != nil {
			return err
		}
		if !intersects(eff, spec.Roles) {
			return authz.ErrNotAuthorized
		}
	}

	// Step 2: resource-privilege.
	if len(spec.Privileges) > 0 {
		ok, err := a.anyPrivilege(actor, spec.Privileges)
		if err != nil {
			return err
		}
		if !ok {
			return authz.ErrNotAuthorized
		}
	}

	// Step 3: attribute predicate (expr-lang).
	if spec.Attribute != "" {
		ok, err := a.attrEval.EvalBool(spec.Attribute, map[string]any{"actor": actor, "vars": vars})
		if err != nil {
			return fmt.Errorf("%w: attribute predicate: %w", authz.ErrNotAuthorized, err)
		}
		if !ok {
			return authz.ErrNotAuthorized
		}
	}

	return nil
}

// effectiveRoles expands the actor's roles and ID through the casbin g graph.
func (a *Authorizer) effectiveRoles(actor authz.Actor) (map[string]struct{}, error) {
	eff := make(map[string]struct{})
	for _, seed := range subjects(actor) {
		eff[seed] = struct{}{}
		implicit, err := a.enforcer.GetImplicitRolesForUser(seed)
		if err != nil {
			return nil, fmt.Errorf("workflow-casbin: implicit roles for %q: %w", seed, err)
		}
		for _, r := range implicit {
			eff[r] = struct{}{}
		}
	}
	return eff, nil
}

// anyPrivilege reports whether any (subject, privilege) pair enforces true.
func (a *Authorizer) anyPrivilege(actor authz.Actor, privileges []string) (bool, error) {
	for _, priv := range privileges {
		if priv == "" {
			continue
		}
		obj, act := splitPrivilege(priv)
		for _, sub := range subjects(actor) {
			ok, err := a.enforcer.Enforce(sub, obj, act)
			if err != nil {
				return false, fmt.Errorf("workflow-casbin: enforce %q/%q/%q: %w", sub, obj, act, err)
			}
			if ok {
				return true, nil
			}
		}
	}
	return false, nil
}

// subjects returns the casbin subjects for an actor: its ID followed by its roles.
func subjects(actor authz.Actor) []string {
	subs := make([]string, 0, 1+len(actor.Roles))
	if actor.ID != "" {
		subs = append(subs, actor.ID)
	}
	subs = append(subs, actor.Roles...)
	return subs
}

// splitPrivilege parses "obj act" into (obj, act); a single token gets act "*".
func splitPrivilege(priv string) (obj, act string) {
	if obj, act, ok := strings.Cut(priv, " "); ok {
		return obj, act
	}
	return priv, "*"
}

// intersects reports whether any value in want is present in have.
func intersects(have map[string]struct{}, want []string) bool {
	for _, w := range want {
		if _, ok := have[w]; ok {
			return true
		}
	}
	return false
}

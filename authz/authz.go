// Package authz defines the authorization contract used by the workflow engine's
// human-task nodes. It is intentionally pure: it imports only stdlib and the
// in-repo expreval package so that the abstraction remains independent of any
// transport, storage, or event-bus vendor.
//
// Implementations that perform I/O (e.g. a casbin-backed authorizer) live in
// internal/ and are never imported by the engine core.
package authz

import (
	"context"
	"errors"
	"fmt"

	"github.com/zakyalvan/krtlwrkflw/internal/expreval"
)

// attrEval is the package-level expression evaluator for attribute predicates.
// A single shared instance is safe for concurrent use; memoization is
// referentially transparent. Mirrors the pattern in engine/conditions.go.
var attrEval = expreval.New()

// ErrNotAuthorized is returned by an [Authorizer] when the actor does not
// satisfy the given [AuthzSpec].
var ErrNotAuthorized = errors.New("workflow-authz: not authorized")

// Actor is a principal that can act on human tasks.
type Actor struct {
	ID         string
	Roles      []string
	Attributes map[string]any
}

// AuthzSpec describes who may act: any-of roles, any-of resource privileges,
// and an optional attribute predicate (expr over {actor, vars}). An empty spec
// means allow-all.
type AuthzSpec struct {
	Roles      []string // actor authorized if it has any of these roles
	Privileges []string // resource-privilege tokens evaluated by a casbin-backed Authorizer (e.g. "finance-task claim")
	Attribute  string   // expr predicate over {"actor": Actor, "vars": map} (optional)
}

// Authorizer decides whether an actor satisfies a spec given process variables.
// Implementations may perform I/O (e.g. casbin policy lookups); the engine
// core never calls this directly — it goes through the runtime abstraction.
type Authorizer interface {
	Authorize(ctx context.Context, spec AuthzSpec, actor Actor, vars map[string]any) error
}

// Compile-time interface assertions.
var (
	_ Authorizer = AllowAll{}
	_ Authorizer = RoleAuthorizer{}
)

// AllowAll is an [Authorizer] that unconditionally permits every actor.
// Useful in tests and permissive development environments.
type AllowAll struct{}

// Authorize always returns nil.
func (AllowAll) Authorize(_ context.Context, _ AuthzSpec, _ Actor, _ map[string]any) error {
	return nil
}

// RoleAuthorizer authorizes an actor when:
//  1. spec.Roles is empty (open access), OR the actor shares at least one role
//     with spec.Roles.
//  2. If spec.Attribute is non-empty, the predicate is evaluated via expreval
//     against {"actor": actor, "vars": vars} and must return true.
//
// On failure [ErrNotAuthorized] is returned. An expression evaluation error is
// wrapped with [ErrNotAuthorized] so callers can always use errors.Is.
//
// Note: [AuthzSpec].Privileges is reserved for future resource-privilege checks
// and is NOT evaluated by RoleAuthorizer.
type RoleAuthorizer struct{}

// Authorize implements [Authorizer].
func (RoleAuthorizer) Authorize(_ context.Context, spec AuthzSpec, actor Actor, vars map[string]any) error {
	// Step 1: role check.
	if len(spec.Roles) > 0 && !hasAnyRole(actor.Roles, spec.Roles) {
		return ErrNotAuthorized
	}

	// Step 2: attribute predicate (optional).
	if spec.Attribute != "" {
		env := map[string]any{
			"actor": actor,
			"vars":  vars,
		}
		ok, err := attrEval.EvalBool(spec.Attribute, env)
		if err != nil {
			return fmt.Errorf("%w: attribute predicate: %w", ErrNotAuthorized, err)
		}
		if !ok {
			return ErrNotAuthorized
		}
	}

	return nil
}

// hasAnyRole reports whether actorRoles and specRoles share at least one value.
func hasAnyRole(actorRoles, specRoles []string) bool {
	set := make(map[string]struct{}, len(actorRoles))
	for _, r := range actorRoles {
		set[r] = struct{}{}
	}
	for _, r := range specRoles {
		if _, ok := set[r]; ok {
			return true
		}
	}
	return false
}

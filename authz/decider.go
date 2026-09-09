package authz

import (
	"context"
	"fmt"
)

// PrivilegeDecider authorizes on resource privileges: the actor must hold, in
// [Actor].Privileges, at least one of the tokens in [AuthzSpec].Privileges.
//
// It is [NotApplicable] when the spec declares no privileges, which is what lets
// [Composite] fall through to the next identity decider instead of refusing a
// spec that expresses identity some other way.
//
// Matching is exact string equality and nothing else. wrkflw stores no policy
// and expands no grammar: "finance-task *" does not match "finance-task claim",
// and a hierarchy is the consumer translator's job to flatten before it builds
// the [Actor].
type PrivilegeDecider struct{}

// Decide implements [Decider].
func (PrivilegeDecider) Decide(_ context.Context, r Request) (Decision, error) {
	if len(r.Spec.Privileges) == 0 {
		return NotApplicable, nil
	}
	if hasAny(r.Actor.Privileges, r.Spec.Privileges) {
		return Allow, nil
	}
	return Deny, nil
}

// RoleDecider authorizes on roles: the actor must hold, in [Actor].Roles, at
// least one of the roles in [AuthzSpec].Roles. It is [NotApplicable] when the
// spec declares no roles.
//
// Matching is exact string equality; wrkflw has no role model and performs no
// role inheritance. A role graph belongs in the consumer's identity provider,
// which flattens it into the [Actor].
type RoleDecider struct{}

// Decide implements [Decider].
func (RoleDecider) Decide(_ context.Context, r Request) (Decision, error) {
	if len(r.Spec.Roles) == 0 {
		return NotApplicable, nil
	}
	if hasAny(r.Actor.Roles, r.Spec.Roles) {
		return Allow, nil
	}
	return Deny, nil
}

// AttributeDecider evaluates [AuthzSpec].Attribute, an expr predicate over
// {"actor": Actor, "vars": map}. It is [NotApplicable] when the predicate is
// empty, [Allow] when it evaluates to true and [Deny] when it evaluates to
// false.
//
// ⚠ A predicate that FAILS TO EVALUATE returns an ERROR, not [Deny], and the
// error does NOT satisfy errors.Is(err, [ErrNotAuthorized]). The distinction is
// deliberate (#69) and it is a disclosure boundary, not a modelling nicety:
//
// A predicate that evaluates to false is a DENIAL. One that will not compile, or
// that does not yield a bool, has determined NOTHING — the policy is broken — so
// calling it a denial claims a fact this code does not have.
//
// And ErrNotAuthorized classifies 403, whose arm in httpcore.ClassifyError
// renders err.Error(): the whole wrapped chain, into the client's response body.
// Every error path in the evaluator embeds the expression SOURCE verbatim
// (compile %q, run %q, %q did not evaluate to bool). MEASURED before the fix, a
// denied caller received the deployment's own authorization rule:
//
//	403 {"message":"workflow-authz: not authorized: attribute
//	 predicate: workflow-expreval: \"actor.Attributes[...]\" did
//	 not evaluate to bool (got string)"}
//
// Unwrapped it falls to ClassifyError's 500 default, which sends an empty
// Message and whose raw error the adapters' writeErr logs — so operators keep
// the full diagnostic and the caller gets none of it.
//
// ⚠ It still fails CLOSED: this returns a non-nil error, and [Composite] — like
// every caller of [Authorizer.Authorize] — treats any error as a refusal. The
// Decision returned alongside the error is [NotApplicable], the zero value, so a
// caller that ignored the error could not mistake it for an allow.
//
// TestRoleAuthorizer_PredicateFailureIsNotADenial pins all of this.
type AttributeDecider struct{}

// Decide implements [Decider].
func (AttributeDecider) Decide(_ context.Context, r Request) (Decision, error) {
	if r.Spec.Attribute == "" {
		return NotApplicable, nil
	}
	env := map[string]any{
		"actor": r.Actor,
		"vars":  r.Vars,
	}
	ok, err := attrEval.EvalBool(r.Spec.Attribute, env)
	if err != nil {
		return NotApplicable, fmt.Errorf("workflow-authz: attribute predicate: %w", err)
	}
	if !ok {
		return Deny, nil
	}
	return Allow, nil
}

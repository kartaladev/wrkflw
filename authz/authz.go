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
	"maps"
	"slices"

	"github.com/kartaladev/wrkflw/internal/expreval"
)

// attrEval is the package-level expression evaluator for attribute predicates.
// A single shared instance is safe for concurrent use; memoization is
// referentially transparent. Mirrors the pattern in engine/conditions.go.
var attrEval = expreval.New()

// ErrNotAuthorized is returned by an [Authorizer] when the actor does not
// satisfy the given [AuthzSpec].
var ErrNotAuthorized = errors.New("workflow-authz: not authorized")

// Actor is a principal that can act on human tasks.
//
// The JSON tags are the actor's wire contract: human-task audit records render
// actors by faithful passthrough, so the shape is defined once here
// rather than re-mapped by every view. There is no first-class username or
// email — populate Attributes from your [ActorResolver] if you need them.
type Actor struct {
	ID    string   `json:"id"`
	Roles []string `json:"roles,omitempty"`
	// Privileges are the resource-privilege tokens the principal has been
	// granted, flattened by the consumer's translator. wrkflw stores no policy
	// and performs no expansion: no wildcards, no hierarchy, no inheritance.
	// A spec privilege matches only by exact string equality.
	Privileges []string       `json:"privileges,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

// Clone returns a copy of the actor whose Roles slice, Privileges slice and
// Attributes map are independently allocated, so mutating the copy cannot affect
// the receiver. Nil fields stay nil. Attributes are cloned one level deep: nested maps and slices
// inside an attribute value remain shared, matching the shallow-snapshot rule
// that applies to process variables elsewhere in the engine.
//
// Use it wherever an actor crosses an isolation boundary — a cached human-task
// record, a cloned instance state — so callers cannot mutate stored audit data.
func (a Actor) Clone() Actor {
	// Guard on nil, not on length: a zero-length slice with spare capacity is
	// still shared between clones. slices.Clone already maps nil to nil.
	a.Roles = slices.Clone(a.Roles)
	a.Privileges = slices.Clone(a.Privileges)
	if a.Attributes != nil {
		a.Attributes = maps.Clone(a.Attributes)
	}
	return a
}

// CloneActors returns a deep copy of actors in which every element is
// independently allocated via [Actor.Clone], so mutating the result cannot
// affect the input. Nil in, nil out; a non-nil empty slice yields a non-nil
// empty slice, preserving the caller's distinction between "no actors resolved"
// and "resolved to nobody".
//
// This is the single slice-level deep copy for actors: the engine's trigger
// handling, the runtime driver, and the human-task clone all delegate here
// rather than re-deriving the loop.
func CloneActors(actors []Actor) []Actor {
	if actors == nil {
		return nil
	}
	out := make([]Actor, len(actors))
	for i, a := range actors {
		out[i] = a.Clone()
	}
	return out
}

// AuthzSpec describes who may act: any-of roles, any-of resource privileges,
// and an optional attribute predicate (expr over {actor, vars}). An empty spec
// means allow-all.
type AuthzSpec struct {
	Roles      []string // actor authorized if it has any of these roles
	Privileges []string // resource-privilege tokens matched verbatim against [Actor.Privileges] (e.g. "finance-task claim")
	Attribute  string   // expr predicate over {"actor": Actor, "vars": map} (optional)
}

// Operation names the human-task action a [Request] is asking about. It lets a
// [Decider] scope itself to the operations it has an opinion on and return
// [NotApplicable] for the rest, which is how the ownership rule stays out of
// claim and reassign without every decider growing a switch.
type Operation string

// The operations the library authorizes today. OpProgress arrives with the
// in-progress task state; deciders must therefore treat an unrecognised
// Operation as one they have no opinion on rather than as an implicit allow.
const (
	OpClaim             Operation = "claim"
	OpComplete          Operation = "complete"
	OpReassign          Operation = "reassign"
	OpRefreshCandidates Operation = "refresh_candidates"
)

// TaskView is the authorization-relevant projection of a human task: whether it
// is claimed, and by whom.
//
// It exists because authz must not import humantask — [TestAuthzPurity] pins
// that authz depends on nothing in this repo but internal/expreval, and the
// engine core imports authz, so any dependency added here propagates into it.
// The caller projects the task; authz never loads one.
type TaskView struct {
	Claimed    bool
	ClaimantID string
}

// Request is one authorization question: may this actor perform this operation,
// against a task carrying this spec, in this variable context?
//
// It is a struct rather than a parameter list so that a new authorization input
// does not break every [Authorizer] and [Decider] implementation again.
type Request struct {
	Operation Operation
	Spec      AuthzSpec
	Actor     Actor
	Vars      map[string]any
	Task      TaskView
}

// Decision is what a single [Decider] concluded about a [Request].
//
// [NotApplicable] is the zero value deliberately: a Decider that returns an
// error, or that is reached by a code path nobody wrote on purpose, must be read
// as having decided NOTHING rather than as having allowed. The combiner treats
// [Allow] and [Deny] as decisions and NotApplicable as abstention.
type Decision int

// Decision values. NotApplicable is at iota 0 so the zero Decision abstains.
const (
	// NotApplicable means the Decider has no opinion: the spec field it reads
	// is unset, or the Operation is outside its remit.
	NotApplicable Decision = iota
	// Allow means the Decider is satisfied.
	Allow
	// Deny means the Decider is not satisfied and the request must be refused.
	Deny
)

// String implements [fmt.Stringer] so a Decision reads in test output and logs.
func (d Decision) String() string {
	switch d {
	case NotApplicable:
		return "NotApplicable"
	case Allow:
		return "Allow"
	case Deny:
		return "Deny"
	default:
		return fmt.Sprintf("Decision(%d)", int(d))
	}
}

// Decider evaluates one authorization rule and nothing else. Each decider in
// this package is single-purpose and usable standalone; [Composite] is what
// combines them.
//
// ⚠ The (Decision, error) pair is not redundant. A Decider that CANNOT reach a
// conclusion — a predicate that will not compile, a directory lookup that failed
// — returns a non-nil error and NOT [Deny]. Deny asserts that the actor is not
// authorized; an error asserts only that the rule could not be evaluated. See
// [AttributeDecider] for why that distinction is a disclosure boundary and not
// only a modelling nicety. Both paths fail closed: [Composite] refuses on either.
type Decider interface {
	Decide(ctx context.Context, r Request) (Decision, error)
}

// Authorizer decides whether a [Request] is permitted, returning nil when it is.
// It is the port the runtime consumes. Implementations may perform I/O; the
// engine core never calls this directly — it goes through the runtime
// abstraction.
//
// A refusal returns [ErrNotAuthorized] and nothing else: the 403 arm of the HTTP
// adapters renders the whole error chain into the client response body, so an
// Authorizer must never fold policy text into a denial. An error that is not
// ErrNotAuthorized means the decision could not be made; it still fails closed.
type Authorizer interface {
	Authorize(ctx context.Context, r Request) error
}

// Compile-time interface assertions.
var (
	_ Authorizer = AllowAll{}
	_ Authorizer = RoleAuthorizer{}
	_ Authorizer = Composite{}

	_ Decider = PrivilegeDecider{}
	_ Decider = RoleDecider{}
	_ Decider = AttributeDecider{}
)

// AllowAll is an [Authorizer] that unconditionally permits every actor.
// Useful in tests and permissive development environments.
type AllowAll struct{}

// Authorize always returns nil.
func (AllowAll) Authorize(_ context.Context, _ Request) error {
	return nil
}

// RoleAuthorizer is the historical default [Authorizer].
//
// Deprecated: use [NewComposite], or assemble a [Composite] from the deciders in
// this package. RoleAuthorizer is retained for one release so existing wiring
// keeps compiling; it now delegates to [NewComposite] and has no behaviour of
// its own.
//
// ⚠ Its behaviour CHANGED when it started delegating. It previously read
// spec.Roles and spec.Attribute and never spec.Privileges, so a spec whose only
// identity field was Privileges was allow-all. It is now evaluated. A deployment
// running a Privileges-only spec was open and now refuses actors that do not
// carry the privilege; that is the fix, and it is a breaking behavioural change.
// Populate [Actor].Privileges from your actor translator.
//
// The zero value is usable and equivalent to NewComposite().
type RoleAuthorizer struct{}

// Authorize implements [Authorizer] by delegating to [NewComposite].
func (RoleAuthorizer) Authorize(ctx context.Context, r Request) error {
	return NewComposite().Authorize(ctx, r)
}

// hasAny reports whether have and want share at least one value, compared by
// exact string equality. It is the matching rule for both roles and privileges:
// wrkflw expands nothing — no wildcards, no hierarchy, no inheritance — so the
// consumer's translator must present the principal's grants already flattened.
func hasAny(have, want []string) bool {
	if len(have) == 0 || len(want) == 0 {
		return false
	}
	set := make(map[string]struct{}, len(have))
	for _, v := range have {
		set[v] = struct{}{}
	}
	for _, v := range want {
		if _, ok := set[v]; ok {
			return true
		}
	}
	return false
}

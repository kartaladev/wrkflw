package authz

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// ErrNoDeciders is returned by [Composite.Authorize] when the composite holds no
// deciders at all. It is a configuration error, not an authorization decision:
// it does not satisfy errors.Is(err, [ErrNotAuthorized]), for the same reason an
// unevaluable predicate does not. A composite with nothing to consult has
// determined nothing.
//
// It exists so the zero [Composite] fails CLOSED. Without it, an empty decider
// set would reach the end of both stages with no opinion and allow everything —
// exactly the shape that made a Privileges-only spec allow-all before this port.
var ErrNoDeciders = errors.New("workflow-authz: composite has no deciders")

// ErrSpecNotEvaluable is returned by [Composite.Authorize] when the spec sets a
// field that no installed [Decider] reads. Like [ErrNoDeciders] it is a
// configuration error and does NOT satisfy errors.Is(err, [ErrNotAuthorized]).
//
// This is #69's rule one level up. A spec field nobody evaluates has determined
// NOTHING, so allowing the request claims a fact the authorizer does not have —
// and denying it claims the opposite one. The honest answer is that the
// authorizer is misconfigured, which classifies 500 rather than 403, so no
// policy detail reaches the refused caller. It still fails CLOSED: every caller
// treats a non-nil error as a refusal.
//
// ⚠ Without this, a Composite holding SOME deciders silently ignored every field
// none of them read and fell through to "an empty spec allows" — a
// Privileges-only spec against a privilege-less composite was ALLOWED. That is
// the #107 defect reproduced inside the code written to close it.
var ErrSpecNotEvaluable = errors.New("workflow-authz: no decider evaluates a field this spec sets")

// ErrNilDecider is returned by [Composite.Authorize] when a decider slice holds
// a nil element. Like the other two it is a configuration error and does not
// satisfy errors.Is(err, [ErrNotAuthorized]). It turns a wiring typo into a
// diagnosable refusal instead of a nil-pointer panic on the request path.
var ErrNilDecider = errors.New("workflow-authz: composite holds a nil decider")

// Composite combines single-purpose [Decider]s into an [Authorizer] in two
// stages.
//
//  1. Identity, first-applicable: the deciders in Identity are consulted in
//     order and the FIRST one that does not return [NotApplicable] decides. An
//     [Allow] moves on to stage 2; a [Deny] refuses immediately. If every
//     identity decider abstains, the spec expresses no identity requirement and
//     stage 2 still runs.
//  2. Constraints, all-must-not-deny: every decider in Constraint is consulted
//     and a single [Deny] refuses. [Allow] and [NotApplicable] both pass.
//
// An empty [AuthzSpec] therefore allows: every decider abstains, and neither
// stage produces a Deny.
//
// ⚠ Read this as "whichever identity field is set, then the constraints" — NOT
// as a general policy algebra. First-applicable means that a spec setting BOTH
// Roles and Privileges never consults the second one, which is why
// model.Validate rejects that combination as an authoring error
// (ErrRolesAndPrivileges) rather than letting a field be silently dropped.
//
// Any decider returning an error aborts the evaluation and that error is
// returned unwrapped by ErrNotAuthorized, so a broken rule is never reported as
// a denial. It still fails closed: the caller sees a non-nil error either way.
//
// A refusal returns bare [ErrNotAuthorized] and names no decider. The 403 arm of
// the HTTP adapters renders the whole error chain to the client, so a denial
// must carry no policy detail.
type Composite struct {
	// Identity holds the deciders consulted first-applicable. Order is the
	// precedence order.
	Identity []Decider
	// Constraint holds the deciders that must not deny, all of them consulted.
	Constraint []Decider
}

// NewComposite returns the library's default [Composite]: identity by privilege
// then by role, constrained by the attribute predicate.
//
// This is the authorizer to reach for unless you are adding a decider of your
// own, in which case build a Composite literal — the fields are exported so the
// default is a starting point, not a wall.
func NewComposite() Composite {
	return Composite{
		Identity:   []Decider{PrivilegeDecider{}, RoleDecider{}},
		Constraint: []Decider{AttributeDecider{}},
	}
}

// Authorize implements [Authorizer].
func (c Composite) Authorize(ctx context.Context, r Request) error {
	if len(c.Identity) == 0 && len(c.Constraint) == 0 {
		return ErrNoDeciders
	}
	if err := c.checkCoverage(r.Spec); err != nil {
		return err
	}

	// Stage 1: identity, first-applicable.
	for _, d := range c.Identity {
		decision, err := d.Decide(ctx, r)
		if err != nil {
			return fmt.Errorf("workflow-authz: identity decider: %w", err)
		}
		if decision == NotApplicable {
			continue
		}
		if decision != Allow {
			// Deny, or a Decision value this package does not define. Anything
			// that is not an explicit Allow refuses: an unrecognised decision
			// has not authorized anything.
			return ErrNotAuthorized
		}
		break
	}

	// Stage 2: constraints, every one must not deny.
	for _, d := range c.Constraint {
		decision, err := d.Decide(ctx, r)
		if err != nil {
			return fmt.Errorf("workflow-authz: constraint decider: %w", err)
		}
		if decision != Allow && decision != NotApplicable {
			// Deny, or a Decision value this package does not define. The
			// direction matters: an unrecognised decision must refuse here
			// exactly as it does in stage 1, or a constraint decider could fail
			// open by returning a value nobody handled.
			return ErrNotAuthorized
		}
	}

	return nil
}

// checkCoverage refuses a spec that sets a field no installed [Decider] reads.
//
// It runs BEFORE any decider is consulted, so a misconfigured composite is
// reported as a misconfiguration rather than as whatever the reachable subset of
// its rules happened to conclude.
//
// A decider that does not implement [SpecReader] declares no coverage and is
// treated as covering nothing — see that interface for why that direction, and
// not the permissive one, is the safe default.
func (c Composite) checkCoverage(spec AuthzSpec) error {
	required := requiredFields(spec)
	if len(required) == 0 {
		// An empty spec asks nothing of the authorizer, so no composite is
		// under-equipped for it.
		return nil
	}

	for _, d := range append(append([]Decider(nil), c.Identity...), c.Constraint...) {
		if d == nil {
			return fmt.Errorf("%w: a decider slice holds a nil element", ErrNilDecider)
		}
		reader, ok := d.(SpecReader)
		if !ok {
			continue
		}
		for _, f := range reader.ReadsSpecFields() {
			delete(required, f)
		}
	}

	if len(required) == 0 {
		return nil
	}
	missing := make([]string, 0, len(required))
	for f := range required {
		missing = append(missing, string(f))
	}
	slices.Sort(missing) // deterministic message
	return fmt.Errorf("%w: %s", ErrSpecNotEvaluable, strings.Join(missing, ", "))
}

// requiredFields is the set of spec fields that are SET and therefore have to be
// evaluated by somebody.
func requiredFields(spec AuthzSpec) map[SpecField]struct{} {
	required := make(map[SpecField]struct{}, 3)
	if len(spec.Roles) > 0 {
		required[FieldRoles] = struct{}{}
	}
	if len(spec.Privileges) > 0 {
		required[FieldPrivileges] = struct{}{}
	}
	if spec.Attribute != "" {
		required[FieldAttribute] = struct{}{}
	}
	return required
}

package authz

import (
	"context"
	"errors"
	"fmt"
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

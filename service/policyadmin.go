package service

//go:generate mockgen -source=policyadmin.go -package=service -destination=policyadmin_mock.go -typed

import "context"

// PolicyRule represents a single casbin policy line (p, subject, object, action).
type PolicyRule struct {
	Subject string
	Object  string
	Action  string
}

// RoleBinding represents a casbin grouping rule (g, user, role).
type RoleBinding struct {
	User string
	Role string
}

// PolicyAdmin manages authorization policy at runtime — adding, removing, and
// listing both permission rules and role assignments — without restarting the
// application.
//
// ctx is accepted for interface consistency and future I/O; the current casbin
// implementation executes synchronously and does not block on ctx.
type PolicyAdmin interface {
	// AddPolicy adds a permission rule. Returns (false, nil) when the rule already exists.
	AddPolicy(ctx context.Context, rule PolicyRule) (bool, error)

	// RemovePolicy removes a permission rule. Returns (false, nil) when the rule does not exist.
	RemovePolicy(ctx context.Context, rule PolicyRule) (bool, error)

	// ListPolicies returns all permission rules currently in effect.
	ListPolicies(ctx context.Context) ([]PolicyRule, error)

	// AddRole adds a role inheritance rule (user → role). Returns (false, nil) when already set.
	AddRole(ctx context.Context, binding RoleBinding) (bool, error)

	// RemoveRole removes a role inheritance rule. Returns (false, nil) when not found.
	RemoveRole(ctx context.Context, binding RoleBinding) (bool, error)

	// ListRoles returns all role inheritance rules currently in effect.
	ListRoles(ctx context.Context) ([]RoleBinding, error)
}

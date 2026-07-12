package casbinauthz

import (
	"context"
	"fmt"

	casbinv2 "github.com/casbin/casbin/v2"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/service"
)

// policyAdmin wraps a *casbinv2.SyncedEnforcer and implements service.PolicyAdmin.
// ctx is accepted for interface consistency; casbin mutators are synchronous and
// do not use the context internally.
type policyAdmin struct {
	e *casbinv2.SyncedEnforcer
}

var _ service.PolicyAdmin = (*policyAdmin)(nil)

// PolicyAdminFor returns (service.PolicyAdmin, true) when a is a *casbinauthz.Authorizer
// (i.e. was created via NewCasbinAuthorizer with any source option), backed by that
// authorizer's shared casbin enforcer. Returns (nil, false) for any other
// authz.Authorizer implementation.
func PolicyAdminFor(a authz.Authorizer) (service.PolicyAdmin, bool) {
	ca, ok := a.(*Authorizer)
	if !ok {
		return nil, false
	}
	return &policyAdmin{e: ca.enforcer}, true
}

func (p *policyAdmin) AddPolicy(_ context.Context, rule service.PolicyRule) (bool, error) {
	added, err := p.e.AddPolicy(rule.Subject, rule.Object, rule.Action)
	if err != nil {
		return false, fmt.Errorf("workflow-casbin: policy admin: %w", err)
	}
	return added, nil
}

func (p *policyAdmin) RemovePolicy(_ context.Context, rule service.PolicyRule) (bool, error) {
	removed, err := p.e.RemovePolicy(rule.Subject, rule.Object, rule.Action)
	if err != nil {
		return false, fmt.Errorf("workflow-casbin: policy admin: %w", err)
	}
	return removed, nil
}

func (p *policyAdmin) ListPolicies(_ context.Context) ([]service.PolicyRule, error) {
	rows, err := p.e.GetPolicy()
	if err != nil {
		return nil, fmt.Errorf("workflow-casbin: policy admin: %w", err)
	}
	rules := make([]service.PolicyRule, 0, len(rows))
	for _, row := range rows {
		if len(row) < 3 {
			continue
		}
		rules = append(rules, service.PolicyRule{Subject: row[0], Object: row[1], Action: row[2]})
	}
	return rules, nil
}

func (p *policyAdmin) AddRole(_ context.Context, binding service.RoleBinding) (bool, error) {
	added, err := p.e.AddGroupingPolicy(binding.User, binding.Role)
	if err != nil {
		return false, fmt.Errorf("workflow-casbin: policy admin: %w", err)
	}
	return added, nil
}

func (p *policyAdmin) RemoveRole(_ context.Context, binding service.RoleBinding) (bool, error) {
	removed, err := p.e.RemoveGroupingPolicy(binding.User, binding.Role)
	if err != nil {
		return false, fmt.Errorf("workflow-casbin: policy admin: %w", err)
	}
	return removed, nil
}

func (p *policyAdmin) ListRoles(_ context.Context) ([]service.RoleBinding, error) {
	rows, err := p.e.GetGroupingPolicy()
	if err != nil {
		return nil, fmt.Errorf("workflow-casbin: policy admin: %w", err)
	}
	bindings := make([]service.RoleBinding, 0, len(rows))
	for _, row := range rows {
		if len(row) < 2 {
			continue
		}
		bindings = append(bindings, service.RoleBinding{User: row[0], Role: row[1]})
	}
	return bindings, nil
}

package runtime_test

// TestCasbinPrivilegeViaBuilderE2E is an end-to-end test that proves the full
// privilege authz chain: model builder (WithEligibilityPrivileges) →
// engine (AwaitHuman.Eligibility.Privileges) → runtime runner (task stored with
// Privileges) → TaskService.Claim (casbin Authorize). No Docker; in-memory only.
//
// Policy: "approver" role may perform "claim" on "finance-task".
// An actor WITH the "approver" role must be ALLOWED.
// An actor WITHOUT it must be DENIED with authz.ErrNotAuthorized.

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/casbinauthz"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/internal/runtimetest"
)

const casbinFinancePolicy = `
p, approver, finance-task, claim
`

// financePrivilegeDef returns a process: start → userTask("review") [no roles,
// privilege "finance-task claim"] → end.
func financePrivilegeDef() *definition.ProcessDefinition {
	return &definition.ProcessDefinition{
		ID:      "finance-approval",
		Version: 1,
		Nodes: []definition.Node{
			definition.NewStartEvent("start"),
			definition.NewUserTask("review", nil,
				definition.WithEligibilityPrivileges("finance-task claim"),
			),
			definition.NewEndEvent("end"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "f1", Source: "start", Target: "review"},
			{ID: "f2", Source: "review", Target: "end"},
		},
	}
}

// TestCasbinPrivilegeViaBuilderE2E_Allow verifies that an actor with the
// "approver" role can claim a task whose privilege is "finance-task claim".
func TestCasbinPrivilegeViaBuilderE2E_Allow(t *testing.T) {
	ctx := t.Context()

	casbinAz, _, err := casbinauthz.NewCasbinAuthorizer(casbinauthz.FromStrings("", casbinFinancePolicy))
	require.NoError(t, err, "build casbin authorizer")

	approver := authz.Actor{ID: "bob", Roles: []string{"approver"}}
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{
		"approver": {approver},
	})

	taskStore := humantask.NewMemTaskStore()
	r := runtimetest.MustRunner(t, nil, runtimetest.MustMemStore(t),
		runtime.WithHumanTasks(resolver, taskStore, casbinAz),
	)

	def := financePrivilegeDef()
	parked, runErr := r.Run(ctx, def, "finance-allow-001", nil)
	require.NoError(t, runErr)

	// The task token is in the parked token's AwaitCommand.
	require.Len(t, parked.Tokens, 1)
	taskToken := parked.Tokens[0].AwaitCommand

	// Verify the stored task carries Privileges by fetching it directly.
	storedTask, getErr := taskStore.Get(ctx, taskToken)
	require.NoError(t, getErr)
	assert.Equal(t, []string{"finance-task claim"}, storedTask.Eligibility.Privileges,
		"task.Eligibility.Privileges must carry the builder-set privilege")

	// Claim must succeed for the approver.
	svc := runtimetest.MustTaskService(t, taskStore, casbinAz)
	_, claimErr := svc.Claim(ctx, taskToken, approver)
	assert.NoError(t, claimErr, "approver with matching casbin policy should be ALLOWED")
}

// TestCasbinPrivilegeViaBuilderE2E_Deny verifies that an actor WITHOUT the
// "approver" role is denied claiming the same task.
func TestCasbinPrivilegeViaBuilderE2E_Deny(t *testing.T) {
	ctx := t.Context()

	casbinAz, _, err := casbinauthz.NewCasbinAuthorizer(casbinauthz.FromStrings("", casbinFinancePolicy))
	require.NoError(t, err, "build casbin authorizer")

	approver := authz.Actor{ID: "bob", Roles: []string{"approver"}}
	viewer := authz.Actor{ID: "carol", Roles: []string{"viewer"}}
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{
		"approver": {approver},
	})

	taskStore := humantask.NewMemTaskStore()
	r := runtimetest.MustRunner(t, nil, runtimetest.MustMemStore(t),
		runtime.WithHumanTasks(resolver, taskStore, casbinAz),
	)

	def := financePrivilegeDef()
	parked, runErr := r.Run(ctx, def, "finance-deny-001", nil)
	require.NoError(t, runErr)
	require.Len(t, parked.Tokens, 1)
	taskToken := parked.Tokens[0].AwaitCommand

	// Claim must be denied for the viewer.
	svc := runtimetest.MustTaskService(t, taskStore, casbinAz)
	_, claimErr := svc.Claim(ctx, taskToken, viewer)
	require.Error(t, claimErr, "viewer without matching casbin policy should be DENIED")
	assert.True(t, errors.Is(claimErr, authz.ErrNotAuthorized),
		"error must be (or wrap) authz.ErrNotAuthorized, got: %v", claimErr)
}

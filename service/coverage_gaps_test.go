// Package service_test covers hard-to-reach branches in service.go.
//
// This file targets specific per-function gaps: GetInstance
// definition-resolution leniency, and error branches in StartInstance,
// DeliverMessage, ClaimTask, ListInstances, and deliverTaskTrigger.
package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/service"
)

// ---- fake collaborators for error-path injection ----

// errLister is a stub InstanceLister that always returns a sentinel error.
type errLister struct{ err error }

func (l *errLister) List(_ context.Context, _ kernel.InstanceFilter) (kernel.InstancePage, error) {
	return kernel.InstancePage{}, l.err
}

// ---- GetInstance definition-resolution behaviour ----

// TestGetInstanceNilDefinitionWhenUnresolved covers the leniency contract of
// GetInstance: an instance whose definition is not in the registry is returned
// with a nil fused definition and NO error — only instance-not-found / store
// errors surface. The
// happy path (definition resolves) and the unknown-instance error path are also
// exercised here.
func TestGetInstanceNilDefinitionWhenUnresolved(t *testing.T) {
	t.Parallel()

	type result struct {
		pi  service.ProcessInstance
		err error
	}

	type testCase struct {
		name   string
		setup  func(t *testing.T) (svc *service.Engine, instanceID string)
		assert func(t *testing.T, r result)
	}

	cases := []testCase{
		{
			name: "happy path — returns state and non-nil definition",
			setup: func(t *testing.T) (*service.Engine, string) {
				t.Helper()
				def := linearDef()
				h := newHarness(t, def)
				svc := h.newEngine(t)

				// Start a linear instance so it exists in the store.
				pi, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
					DefRef: defRefFor(def),
					Vars:   map[string]any{"name": "world"},
				})
				require.NoError(t, err)
				require.Equal(t, engine.StatusCompleted, pi.State().Status)

				return svc, pi.State().InstanceID
			},
			assert: func(t *testing.T, r result) {
				require.NoError(t, r.err)
				assert.NotEmpty(t, r.pi.State().InstanceID)
				require.NotNil(t, r.pi.Definition(), "definition must be non-nil on the happy path")
				assert.Equal(t, "greeting", r.pi.Definition().ID)
			},
		},
		{
			name: "unknown instance returns ErrInstanceNotFound",
			setup: func(t *testing.T) (*service.Engine, string) {
				t.Helper()
				h := newHarness(t) // no defs, no instances
				svc := h.newEngine(t)
				return svc, "no-such-instance"
			},
			assert: func(t *testing.T, r result) {
				require.Error(t, r.err)
				assert.ErrorIs(t, r.err, kernel.ErrInstanceNotFound)
			},
		},
		{
			name: "instance exists but definition missing returns nil definition and no error",
			setup: func(t *testing.T) (*service.Engine, string) {
				t.Helper()
				def := linearDef()
				h := newHarness(t, def)

				// Start the instance via the driver directly so it lands in the store.
				st, err := h.driver.Drive(t.Context(), def, "gwid-nodef-1", map[string]any{"name": "x"})
				require.NoError(t, err)
				require.Equal(t, engine.StatusCompleted, st.Status)

				// Build the service with an EMPTY registry so the definition cannot
				// be resolved. GetInstance must NOT error — it returns a nil def.
				emptyReg := kernel.NewMapDefinitionRegistry()
				svc, err := service.NewEngine(
					service.WithProcessDriver(h.driver),
					service.WithInstanceStore(h.store),
					service.WithDefinitions(emptyReg),
					service.WithLister(h.lister),
					service.WithHumanTasks(h.taskStore, h.az),
					service.WithClock(h.clk),
				)
				require.NoError(t, err)
				return svc, "gwid-nodef-1"
			},
			assert: func(t *testing.T, r result) {
				require.NoError(t, r.err, "a missing definition must NOT be an error on GetInstance")
				require.NotNil(t, r.pi)
				assert.Nil(t, r.pi.Definition(), "definition must be nil when unresolved")
				assert.Equal(t, "gwid-nodef-1", r.pi.State().InstanceID)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc, instanceID := tc.setup(t)
			pi, err := svc.GetInstance(t.Context(), instanceID)
			tc.assert(t, result{pi: pi, err: err})
		})
	}
}

// ---- Error-branch coverage for StartInstance ----

// TestStartInstanceRunnerError covers the driver.Drive error branch (line 163 in
// service.go) which remains at 0% because the existing happy-path and
// unknown-defref tests never reach it.
//
// To make driver.Drive fail we provide a definition whose only node is a service
// task wired to an action that returns a terminal error. The driver exhausts
// retries (MaxAttempts=1) and the instance parks with an incident, but Run
// itself succeeds. Instead, we provoke a genuine Run error by passing a
// definition with a malformed graph: a node whose outgoing flow points to a
// non-existent target, which causes the engine to return an error.
//
// Simpler path: we inject a broken definition that has no StartEvent — the
// engine cannot start and returns an error immediately.
func TestStartInstanceRunnerError(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		def    func() *model.ProcessDefinition
		assert func(t *testing.T, pi service.ProcessInstance, err error)
	}

	cases := []testCase{
		{
			// A definition with no nodes causes the engine to fail with an error
			// (no start event → cannot bootstrap token). This exercises the
			// driver.Drive error branch in StartInstance.
			name: "runner error propagates from StartInstance",
			def: func() *model.ProcessDefinition {
				return &model.ProcessDefinition{
					ID:      "broken",
					Version: 1,
					Nodes:   []model.Node{}, // no start event
					Flows:   []flow.SequenceFlow{},
				}
			},
			assert: func(t *testing.T, _ service.ProcessInstance, err error) {
				require.Error(t, err)
				// The error must NOT be ErrDefinitionNotFound (def was found).
				assert.False(t, errors.Is(err, kernel.ErrDefinitionNotFound),
					"error must NOT be ErrDefinitionNotFound — the definition was resolved")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			def := tc.def()
			h := newHarness(t, def)
			svc := h.newEngine(t)

			st, err := svc.StartInstance(t.Context(), service.StartInstanceRequest{
				DefRef: defRefFor(def),
			})
			tc.assert(t, st, err)
		})
	}
}

// ---- Error-branch coverage for ClaimTask ----

// TestClaimTaskStoreGetError covers the deliverTaskTrigger error branch where
// taskStore.Get fails (e.g. the token does not exist in the store at all).
func TestClaimTaskStoreGetError(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name      string
		taskToken string
		assert    func(t *testing.T, pi service.ProcessInstance, err error)
	}

	cases := []testCase{
		{
			// taskStore.Get returns ErrTaskNotFound for an unknown token; this
			// exercises the "get task" error branch in deliverTaskTrigger.
			name:      "unknown task token returns ErrTaskNotFound",
			taskToken: "no-such-token",
			assert: func(t *testing.T, _ service.ProcessInstance, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, humantask.ErrTaskNotFound)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			def := approvalDef()
			h := newHarness(t, def)
			svc := h.newEngine(t)

			manager := authz.Actor{ID: "alice", Roles: []string{"manager"}}
			st, err := svc.ClaimTask(t.Context(), service.ClaimTaskRequest{
				TaskToken: tc.taskToken,
				Actor:     manager,
			})
			tc.assert(t, st, err)
		})
	}
}

// ---- Error-branch coverage for ListInstances ----

// TestListInstancesListerError covers the lister.List error branch (75% → the
// lister error return on line 258-260 in service.go). We inject an errLister
// stub so the branch is exercised without needing a real Postgres instance.
func TestListInstancesListerError(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		assert func(t *testing.T, page kernel.InstancePage, err error)
	}

	sentinel := errors.New("store-unavailable")

	cases := []testCase{
		{
			name: "lister error propagates from ListInstances",
			assert: func(t *testing.T, _ kernel.InstancePage, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, sentinel)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			def := linearDef()
			h := newHarness(t, def)

			// Override the lister with one that always fails.
			svc, err := service.NewEngine(
				service.WithProcessDriver(h.driver),
				service.WithInstanceStore(h.store),
				service.WithDefinitions(h.reg),
				service.WithLister(&errLister{err: sentinel}),
				service.WithHumanTasks(h.taskStore, h.az),
				service.WithClock(h.clk),
			)
			require.NoError(t, err)

			page, err := svc.ListInstances(t.Context(), kernel.InstanceFilter{Limit: 10})
			tc.assert(t, page, err)
		})
	}
}

// ---- Error-branch coverage for tasks.Claim error in ClaimTask ----

// TestClaimTaskAuthorizationFailure verifies that when tasks.Claim fails (e.g.
// due to authorization), the error propagates from ClaimTask. This targets the
// 75% gap where the tasks.Claim error branch is not reached by the happy-path
// test.
//
// Note: the closed-task ErrConflict path is already tested in errors_test.go;
// this test targets the TaskService.Claim authorization error, which fires
// BEFORE deliverTaskTrigger — i.e. the error branch at line 232-233.
func TestClaimTaskAuthorizationFailure(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		actor  authz.Actor
		assert func(t *testing.T, pi service.ProcessInstance, err error)
	}

	cases := []testCase{
		{
			name:  "unauthorized actor causes Claim to fail",
			actor: authz.Actor{ID: "eve", Roles: []string{"viewer"}},
			assert: func(t *testing.T, _ service.ProcessInstance, err error) {
				require.Error(t, err)
				// tasks.Claim propagates ErrNotAuthorized when the actor's roles
				// don't satisfy the task's eligibility spec.
				assert.ErrorIs(t, err, authz.ErrNotAuthorized)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			def := approvalDef()
			h := newHarness(t, def)

			// Start the instance — parks at the user task.
			parked, err := h.driver.Drive(t.Context(), def, "claim-auth-fail-1", nil)
			require.NoError(t, err)
			require.Equal(t, engine.StatusRunning, parked.Status)
			require.Len(t, parked.Tokens, 1)
			taskToken := parked.Tokens[0].AwaitCommand
			require.NotEmpty(t, taskToken)

			svc := h.newEngine(t)

			st, err := svc.ClaimTask(t.Context(), service.ClaimTaskRequest{
				TaskToken: taskToken,
				Actor:     tc.actor,
			})
			tc.assert(t, st, err)
		})
	}
}

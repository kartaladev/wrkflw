package processtest_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/processtest"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// serviceDef is start → service-task(actionName) → end. A service task runs
// synchronously, so Start drives the instance straight to completion.
func serviceDef(t *testing.T, actionName string) *model.ProcessDefinition {
	t.Helper()
	def, err := definition.NewBuilder("svc", 1).
		Add(event.NewStart("start")).
		Add(activity.NewServiceTask("call", activity.WithTaskAction(actionName))).
		Add(event.NewEnd("end")).
		Connect("start", "call").
		Connect("call", "end").
		Build()
	require.NoError(t, err)
	return def
}

func TestHarnessOptions(t *testing.T) {
	type testCase struct {
		name  string
		opts  []processtest.Option
		check func(t *testing.T, h *processtest.Harness)
	}

	noted := false

	cases := []testCase{
		{
			name: "WithCatalogActionFunc registers an invokable action",
			opts: []processtest.Option{
				processtest.WithCatalogActionFunc("greet", func(_ context.Context, in map[string]any) (map[string]any, error) {
					return map[string]any{"greeted": true}, nil
				}),
			},
			check: func(t *testing.T, h *processtest.Harness) {
				def := serviceDef(t, "greet")
				final, err := h.Start(t.Context(), def, "i", nil)
				require.NoError(t, err)
				assert.Equal(t, engine.StatusCompleted, final.Status)
				assert.Equal(t, 1, h.Catalog().Count("greet"))
			},
		},
		{
			name: "WithActions and WithCatalogAction register actions",
			opts: []processtest.Option{
				processtest.WithActions(map[string]action.Action{
					"a": action.ActionFunc(func(context.Context, map[string]any) (map[string]any, error) { return nil, nil }),
				}),
				processtest.WithCatalogAction("b", action.ActionFunc(func(context.Context, map[string]any) (map[string]any, error) {
					noted = true
					return nil, nil
				})),
			},
			check: func(t *testing.T, h *processtest.Harness) {
				def := serviceDef(t, "b")
				_, err := h.Start(t.Context(), def, "i", nil)
				require.NoError(t, err)
				assert.True(t, noted)
				_, okA := h.Catalog().Resolve("a")
				assert.True(t, okA, "action a is registered")
			},
		},
		{
			name: "WithAuthorizer deny blocks a task claim",
			opts: []processtest.Option{
				processtest.WithAuthorizer(func(context.Context, authz.AuthzSpec, authz.Actor, map[string]any) error {
					return authz.ErrNotAuthorized
				}),
			},
			check: func(t *testing.T, h *processtest.Harness) {
				def := approvalDef(t)
				_, err := h.Start(t.Context(), def, "i", nil)
				require.NoError(t, err)
				_, err = h.DriveToCompletion(t.Context(), def, "i", h.CompleteTasks(approve))
				require.ErrorIs(t, err, authz.ErrNotAuthorized)
			},
		},
		{
			name: "WithActorResolver supplies candidates",
			opts: []processtest.Option{
				processtest.WithActorResolver(humantask.NewStaticActorResolver(map[string][]authz.Actor{
					"manager": {{ID: "alice", Roles: []string{"manager"}}},
				})),
			},
			check: func(t *testing.T, h *processtest.Harness) {
				def := approvalDef(t)
				_, err := h.Start(t.Context(), def, "i", nil)
				require.NoError(t, err)
				final, err := h.DriveToCompletion(t.Context(), def, "i", h.CompleteTasks(approve))
				require.NoError(t, err)
				assert.Equal(t, engine.StatusCompleted, final.Status)
			},
		},
		{
			name: "WithClockStart positions the fake clock",
			opts: []processtest.Option{processtest.WithClockStart(time.Date(2030, 5, 1, 8, 0, 0, 0, time.UTC))},
			check: func(t *testing.T, h *processtest.Harness) {
				assert.Equal(t, time.Date(2030, 5, 1, 8, 0, 0, 0, time.UTC), h.Clock().Now())
			},
		},
		{
			name: "WithDefinitions wires a registry",
			opts: []processtest.Option{processtest.WithDefinitions(kernel.NewMapDefinitionRegistry(nil))},
			check: func(t *testing.T, h *processtest.Harness) {
				assert.NotNil(t, h.Driver())
			},
		},
		{
			name: "WithSignalBus lets Publish resume a parked instance",
			opts: []processtest.Option{processtest.WithSignalBus()},
			check: func(t *testing.T, h *processtest.Harness) {
				require.NotNil(t, h.Bus())
				def := signalDef(t)
				_, err := h.Start(t.Context(), def, "i", nil)
				require.NoError(t, err)
				require.NoError(t, h.Bus().Publish(t.Context(), "go", nil))

				final, _, err := h.Store().Load(t.Context(), "i")
				require.NoError(t, err)
				assert.Equal(t, engine.StatusCompleted, final.Status)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, err := processtest.New(tc.opts...)
			require.NoError(t, err)
			tc.check(t, h)
		})
	}
}

// TestAdvanceTimersNoPendingTimer covers the ErrNoPendingTimer branch: an
// AdvanceTimers decision on an instance with no armed timer fails.
func TestAdvanceTimersNoPendingTimer(t *testing.T) {
	t.Parallel()

	h, err := processtest.New()
	require.NoError(t, err)

	def := signalDef(t)
	_, err = h.Start(t.Context(), def, "i", nil)
	require.NoError(t, err)

	_, err = h.DriveToCompletion(t.Context(), def, "i", func(context.Context, processtest.Park) (processtest.Decision, error) {
		return processtest.AdvanceTimers(), nil
	})
	require.ErrorIs(t, err, processtest.ErrNoPendingTimer)
}

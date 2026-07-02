package service_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/service"
)

// incidentDef returns start → serviceTask("failing") → end.
// The action in the catalog fails on first call and succeeds on subsequent calls.
func incidentDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "incident-test", Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("start"),
			model.NewServiceTask("task", model.WithActionName("failing")),
			model.NewEndEvent("end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "task"},
			{ID: "f2", Source: "task", Target: "end"},
		},
	}
}

// TestEngineResolveIncident verifies that ResolveIncident clears the incident
// and resumes instance execution to completion. It also verifies AddAttempts ≤ 0
// is coerced to 1 by confirming no error is returned and the instance completes.
func TestEngineResolveIncident(t *testing.T) {
	T := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clockwork.NewFakeClockAt(T)

	var calls atomic.Int32
	failingAction := action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
		if calls.Add(1) == 1 {
			return nil, errors.New("first call fails")
		}
		return map[string]any{"done": true}, nil
	})

	taskStore := humantask.NewMemTaskStore()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{})
	az := authz.RoleAuthorizer{}
	store, err := runtime.NewMemStore()
	require.NoError(t, err)
	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"failing": failingAction,
	})

	r, err := runtime.NewRunner(cat, store, runtime.WithRunnerClock(clk),
		runtime.WithHumanTasks(resolver, taskStore, az),
		// MaxAttempts=1 → first failure becomes an incident.
		runtime.WithDefaultRetryPolicy(model.RetryPolicy{
			MaxAttempts:     1,
			InitialInterval: time.Second,
			BackoffCoef:     1,
			MaxInterval:     time.Minute,
		}),
	)
	require.NoError(t, err)

	def := incidentDef()
	defsMap := map[string]*model.ProcessDefinition{
		defRefFor(def): def,
		def.ID:         def,
	}
	reg := runtime.NewMapDefinitionRegistry(defsMap)
	taskSvc, err := runtime.NewTaskService(taskStore, az, runtime.WithTaskServiceClock(clk))
	require.NoError(t, err)
	svc := service.New(r, taskSvc, reg, store, store, taskStore, service.WithEngineClock(clk))

	// Start the instance — parks with an incident after the first failure.
	ctx := t.Context()
	parked, err := r.Run(ctx, def, "inc-inst-1", nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, parked.Status, "instance must park with an incident")
	require.Len(t, parked.Incidents, 1, "want exactly one incident")

	incID := parked.Incidents[0].ID

	t.Run("ResolveIncident clears incident and resumes", func(t *testing.T) {
		st, err := svc.ResolveIncident(ctx, service.ResolveIncidentRequest{
			InstanceID:  "inc-inst-1",
			IncidentID:  incID,
			AddAttempts: 2,
		})
		require.NoError(t, err)
		assert.Empty(t, st.Incidents, "incident must be cleared after ResolveIncident")
		assert.Equal(t, engine.StatusCompleted, st.Status, "instance must complete after resolve")
	})
}

// TestEngineResolveIncidentDefaultsAddAttempts verifies that AddAttempts <= 0 is coerced to 1.
func TestEngineResolveIncidentDefaultsAddAttempts(t *testing.T) {
	T := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clockwork.NewFakeClockAt(T)

	var calls atomic.Int32
	failingAction := action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
		if calls.Add(1) == 1 {
			return nil, errors.New("first call fails")
		}
		return map[string]any{"done": true}, nil
	})

	taskStore := humantask.NewMemTaskStore()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{})
	az := authz.RoleAuthorizer{}
	store, err := runtime.NewMemStore()
	require.NoError(t, err)
	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"failing": failingAction,
	})

	r, err := runtime.NewRunner(cat, store, runtime.WithRunnerClock(clk),
		runtime.WithHumanTasks(resolver, taskStore, az),
		runtime.WithDefaultRetryPolicy(model.RetryPolicy{
			MaxAttempts:     1,
			InitialInterval: time.Second,
			BackoffCoef:     1,
			MaxInterval:     time.Minute,
		}),
	)
	require.NoError(t, err)

	def := incidentDef()
	defsMap := map[string]*model.ProcessDefinition{
		defRefFor(def): def,
		def.ID:         def,
	}
	reg := runtime.NewMapDefinitionRegistry(defsMap)
	taskSvc, err := runtime.NewTaskService(taskStore, az, runtime.WithTaskServiceClock(clk))
	require.NoError(t, err)
	svc := service.New(r, taskSvc, reg, store, store, taskStore, service.WithEngineClock(clk))

	ctx := t.Context()
	parked, err := r.Run(ctx, def, "inc-inst-zero", nil)
	require.NoError(t, err)
	require.Len(t, parked.Incidents, 1)
	incID := parked.Incidents[0].ID

	// AddAttempts=0 → should default to 1, allowing the action to succeed.
	st, err := svc.ResolveIncident(ctx, service.ResolveIncidentRequest{
		InstanceID:  "inc-inst-zero",
		IncidentID:  incID,
		AddAttempts: 0,
	})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, st.Status, "AddAttempts=0 must default to 1 and allow completion")
}

// TestEngineResolveIncidentInstanceNotFound verifies that ResolveIncident propagates
// ErrInstanceNotFound for an unknown instance ID.
func TestEngineResolveIncidentInstanceNotFound(t *testing.T) {
	h := newHarness(t, linearDef())
	svc := service.New(h.runner, h.tasks, h.reg, h.store, h.lister, h.taskStore, service.WithEngineClock(h.clk))

	_, err := svc.ResolveIncident(t.Context(), service.ResolveIncidentRequest{
		InstanceID:  "no-such-instance",
		IncidentID:  "any-incident",
		AddAttempts: 1,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, runtime.ErrInstanceNotFound)
}

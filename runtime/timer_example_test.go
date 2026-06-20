package runtime_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jonboulle/clockwork"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// timerIntermediateDef returns: start → timer-catch("1h") → service("greet") → end.
func timerIntermediateDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "timer-intermediate",
		Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "wait1h", Kind: model.KindIntermediateCatchEvent, TimerDuration: `"1h"`},
			{ID: "greet", Kind: model.KindServiceTask, Action: "greet"},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "wait1h"},
			{ID: "f2", Source: "wait1h", Target: "greet"},
			{ID: "f3", Source: "greet", Target: "end"},
		},
	}
}

// TestRunnerTimerIntermediateFiresUnderFakeClock verifies the full fake-clock
// timer-intermediate e2e path:
//
//  1. Run parks at the intermediate-catch timer node (ScheduleTimer registered).
//  2. Advancing the fake clock past FireAt and calling Tick fires TimerFired.
//  3. The service task runs and the instance reaches StatusCompleted.
func TestRunnerTimerIntermediateFiresUnderFakeClock(t *testing.T) {
	ctx := t.Context()

	startAt := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(startAt)

	serviceRan := false
	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"greet": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			serviceRan = true
			return map[string]any{"greeted": true}, nil
		}),
	})

	sched := runtime.NewMemScheduler(fc)
	store := runtime.NewMemStore()

	r := runtime.NewRunner(cat, fc, store, runtime.WithScheduler(sched))

	def := timerIntermediateDef()
	const instanceID = "timer-e2e-1"

	// Run → parks at the intermediate timer node.
	parked, err := r.Run(ctx, def, instanceID, nil)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, parked.Status)
	require.Len(t, parked.Tokens, 1)
	assert.Equal(t, "wait1h", parked.Tokens[0].NodeID)
	assert.False(t, serviceRan, "service must not run while timer is pending")

	// Advance clock past FireAt (1h from start). The scheduler fires the timer
	// which calls Deliver internally; instance should complete synchronously.
	fc.Advance(1*time.Hour + 1*time.Second)
	require.NoError(t, sched.Tick(ctx))

	// After Tick, the instance should be completed.
	final, _, err := store.Load(ctx, instanceID)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status, "instance must be StatusCompleted after timer fires and service runs")
	assert.True(t, serviceRan, "service action must have run after timer fired")
	assert.Equal(t, true, final.Variables["greeted"])
	assert.Empty(t, final.Tokens, "no tokens remain after completion")

	// Outbox recorded instance.completed.
	evs := store.Events()
	require.NotEmpty(t, evs)
	assert.Equal(t, "instance.completed", evs[len(evs)-1].Topic)
}

// slaUserTaskDef returns: start → userTask(SLADuration="PT30M", SLAFlow="escalate",
// SLAAction="notify-escalation") → end; with an escalation path to an alt-end.
func slaUserTaskDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "sla-user-task",
		Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{
				ID:             "review",
				Kind:           model.KindUserTask,
				CandidateRoles: []string{"reviewer"},
				SLADuration:    `"30m"`,
				SLAFlow:        "escalate",
				SLAAction:      "notify-escalation",
			},
			{ID: "end-normal", Kind: model.KindEndEvent},
			{ID: "end-escalated", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "review"},
			{ID: "f2", Source: "review", Target: "end-normal"},
			{ID: "escalate", Source: "review", Target: "end-escalated"},
		},
	}
}

// TestRunnerUserTaskSLAFiresUnderFakeClock verifies that, when the SLA timer
// fires (clock advanced past FireAt), the alternative path is taken and the task
// is Cancelled — without ever completing the task.
func TestRunnerUserTaskSLAFiresUnderFakeClock(t *testing.T) {
	ctx := t.Context()

	startAt := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(startAt)

	escalationRan := false
	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"notify-escalation": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			escalationRan = true
			return map[string]any{"escalated": true}, nil
		}),
	})

	reviewer := authz.Actor{ID: "alice", Roles: []string{"reviewer"}}
	taskStore := humantask.NewMemTaskStore()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{
		"reviewer": {reviewer},
	})
	az := authz.RoleAuthorizer{}
	sched := runtime.NewMemScheduler(fc)
	store := runtime.NewMemStore()

	r := runtime.NewRunner(
		cat, fc, store,
		runtime.WithHumanTasks(resolver, taskStore, az),
		runtime.WithScheduler(sched),
	)

	def := slaUserTaskDef()
	const instanceID = "sla-e2e-1"

	// Run → parks at the user task, SLA timer is registered.
	parked, err := r.Run(ctx, def, instanceID, nil)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, parked.Status)
	require.Len(t, parked.Tokens, 1)
	assert.Equal(t, "review", parked.Tokens[0].NodeID)

	// Verify the task is in the store and unclaimed.
	claimable, err := taskStore.ClaimableBy(ctx, reviewer)
	require.NoError(t, err)
	require.Len(t, claimable, 1)
	taskToken := claimable[0].TaskToken
	assert.False(t, escalationRan, "escalation must not run before SLA fires")

	// Do NOT complete the task. Advance clock past the 30-minute SLA.
	fc.Advance(31 * time.Minute)
	require.NoError(t, sched.Tick(ctx))

	// After Tick, the instance must have taken the escalation path.
	final, _, err := store.Load(ctx, instanceID)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status,
		"instance must be StatusCompleted via the escalation path")
	assert.Empty(t, final.Tokens, "no tokens remain after escalation completes")
	assert.True(t, escalationRan, "SLA action must have run on breach")

	// The task must be Cancelled.
	cancelledTask, err := taskStore.Get(ctx, taskToken)
	require.NoError(t, err)
	assert.Equal(t, humantask.Cancelled, cancelledTask.State,
		"the human task must be Cancelled after SLA breach")
}

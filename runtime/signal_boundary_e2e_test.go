package runtime_test

import (
	"context"
	"testing"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/internal/runtimetest"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/runtime/signal"
)

// signalBoundaryDef returns a definition whose host UserTask("review") parks
// awaiting human action, with an interrupting SIGNAL boundary on "escalate":
//
//	start → UserTask("review") → end-ok
//	                ↑ interrupting signal boundary "escalate" → end-escalated
//
// It is the signal mirror of messageBoundaryDef in message_boundary_e2e_test.go.
func signalBoundaryDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "sig-boundary",
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("review", activity.WithEligibleRoles("manager")),
			event.NewBoundary("bnd-escalate", "review",
				event.WithSignalName("escalate")),
			event.NewEnd("end-ok"),
			event.NewEnd("end-escalated"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f-start", Source: "start", Target: "review"},
			{ID: "f-ok", Source: "review", Target: "end-ok"},
			{ID: "f-escalate", Source: "bnd-escalate", Target: "end-escalated"},
		},
	}
}

// TestBroadcastSignalFiresBoundary is the signal mirror of
// TestDeliverMessageFiresBoundary: a broadcast must wake a parked instance via a
// signal BOUNDARY, not only via a signal-catch token.
//
// The host UserTask parks on AwaitCommand — nothing sets Token.AwaitSignal for a
// boundary — so the name lives solely on the boundary arm. If SignalWaiters()
// omits boundary arms, the runtime never subscribes "escalate", Publish finds no
// waiter, and the instance parks forever.
func TestBroadcastSignalFiresBoundary(t *testing.T) {
	ctx := t.Context()
	fc := clockwork.NewFakeClock()
	store := runtimetest.MustMemStore(t)

	manager := authz.Actor{ID: "alice", Roles: []string{"manager"}}
	taskStore := humantask.NewMemTaskStore()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{
		"manager": {manager},
	})
	def := signalBoundaryDef()
	reg := kernel.NewMemDefinitionRegistry()
	require.NoError(t, reg.Register(def))

	// Forward-reference wiring: the bus delivers via driver.ApplyTrigger and the
	// driver owns the bus — the same graph a consumer builds once.
	var r *runtime.ProcessDriver
	bus := runtimetest.MustSignalBus(t, func(bCtx context.Context, instanceID string, trg engine.Trigger) error {
		_, derr := r.ApplyTrigger(bCtx, def, instanceID, trg)
		return derr
	}, signal.WithClock(fc))
	r = runtimetest.MustProcessDriver(t, nil, store,
		runtime.WithClock(fc),
		runtime.WithDefinitions(reg),
		runtime.WithSignalBus(bus),
		runtime.WithHumanTasks(resolver, taskStore, authz.RoleAuthorizer{}))

	st, err := r.Drive(ctx, def, "i1", nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, st.Status, "instance must park at the UserTask")
	require.Len(t, st.Boundaries, 1, "signal boundary must be armed on the parked host")

	// The engine's authoritative waiter set must surface the boundary's signal,
	// otherwise the runtime cannot subscribe it.
	require.Contains(t, st.SignalWaiters(), "escalate",
		"SignalWaiters must include armed signal BOUNDARY names")

	require.NoError(t, r.BroadcastSignal(ctx, "escalate", nil))

	final, _, err := store.Load(ctx, "i1")
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status,
		"signal boundary must interrupt the host task and complete via the boundary flow")
	assert.Empty(t, final.Tokens, "no tokens remain after completion")
}

// TestBroadcastSignalWinsEventGatewayArm is the signal mirror of
// TestDeliverMessageFiresEventGatewayArm: a broadcast must reach the SIGNAL arm
// of an event-based gateway and win the race.
//
// The arm is tracked as an armedEvent, not a token carrying AwaitSignal, so the
// runtime can only reach it if SignalWaiters() surfaces event-gateway signal
// arms (ADR-0154). Without that, the gateway parks forever and neither arm ever
// wins. It reuses eventGatewayCorrelatedMsgDef, whose "sig-catch" arm races the
// correlated message arm.
func TestBroadcastSignalWinsEventGatewayArm(t *testing.T) {
	ctx := t.Context()
	fc := clockwork.NewFakeClock()
	store := runtimetest.MustMemStore(t)

	cat := action.NewCatalog(map[string]action.Action{
		"ship-order": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return map[string]any{"shipped": true}, nil
		}),
		"cancel-order": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return map[string]any{"cancelled": true}, nil
		}),
	})

	def := eventGatewayCorrelatedMsgDef()
	reg := kernel.NewMemDefinitionRegistry()
	require.NoError(t, reg.Register(def))

	var r *runtime.ProcessDriver
	bus := runtimetest.MustSignalBus(t, func(bCtx context.Context, instanceID string, trg engine.Trigger) error {
		_, derr := r.ApplyTrigger(bCtx, def, instanceID, trg)
		return derr
	}, signal.WithClock(fc))
	r = runtimetest.MustProcessDriver(t, cat, store,
		runtime.WithClock(fc), runtime.WithDefinitions(reg), runtime.WithSignalBus(bus))

	st, err := r.Drive(ctx, def, "order-cancel", map[string]any{"order": "order-cancel"})
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, st.Status, "instance must park at the event gateway")
	require.Len(t, st.ArmedEvents, 2, "both gateway arms must be armed")
	require.Contains(t, st.SignalWaiters(), "cancelled",
		"SignalWaiters must include event-based-gateway SIGNAL arm names")

	require.NoError(t, r.BroadcastSignal(ctx, "cancelled", nil))

	final, _, err := store.Load(ctx, "order-cancel")
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status,
		"the signal arm must win the gateway race and complete via the cancel flow")
	var reachedCancelled bool
	for _, v := range final.History {
		if v.NodeID == "end-cancelled" {
			reachedCancelled = true
		}
	}
	assert.True(t, reachedCancelled, "instance must terminate at end-cancelled")
}

package runtime_test

import (
	"testing"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// messageBoundaryDef returns a definition whose host UserTask("review") parks
// awaiting human action, with an interrupting message boundary on "cancel":
//
//	start → UserTask("review") → end-ok
//	                ↑ interrupting message boundary "cancel" → end-cancelled
func messageBoundaryDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "msg-boundary",
		Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("start"),
			model.NewUserTask("review", []string{"manager"}),
			model.NewBoundaryEvent("bnd-cancel", "review",
				model.WithBoundaryMessage("cancel", "")),
			model.NewEndEvent("end-ok"),
			model.NewEndEvent("end-cancelled"),
		},
		Flows: []model.SequenceFlow{
			{ID: "f-start", Source: "start", Target: "review"},
			{ID: "f-ok", Source: "review", Target: "end-ok"},
			{ID: "f-cancel", Source: "bnd-cancel", Target: "end-cancelled"},
		},
	}
}

// TestDeliverMessageFiresBoundary verifies that DeliverMessage wakes a parked
// instance via a message BOUNDARY (not only via a message-catch token): the host
// UserTask is interrupted and the instance completes through the boundary flow.
func TestDeliverMessageFiresBoundary(t *testing.T) {
	ctx := t.Context()
	fc := clockwork.NewFakeClock()
	store := mustMemStore(t)

	manager := authz.Actor{ID: "alice", Roles: []string{"manager"}}
	taskStore := humantask.NewMemTaskStore()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{
		"manager": {manager},
	})
	r := mustRunner(t, nil, store,
		runtime.WithRunnerClock(fc),
		runtime.WithHumanTasks(resolver, taskStore, authz.RoleAuthorizer{}))

	def := messageBoundaryDef()

	st, err := r.Run(ctx, def, "i1", nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, st.Status, "instance must park at the UserTask")
	require.Len(t, st.Boundaries, 1, "message boundary must be armed on the parked host")

	// Deliver the BOUNDARY message. This must be routed to the parked instance
	// even though no token has AwaitMessage == "cancel" (the boundary arm holds it).
	err = r.DeliverMessage(ctx, def, "cancel", "", nil)
	require.NoError(t, err)

	final, _, err := store.Load(ctx, "i1")
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status,
		"the boundary must interrupt the host and complete via the boundary flow")
	assert.Empty(t, final.Tokens, "no tokens remain after completion")
}

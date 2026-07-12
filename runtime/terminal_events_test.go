package runtime_test

import (
	"testing"

	clockwork "github.com/jonboulle/clockwork"
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
)

// topicsOf collects the topics of all buffered outbox events in append order.
func topicsOf(evs []kernel.OutboxEvent) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.Topic
	}
	return out
}

// TestCancelEmitsInstanceTerminated is the ADR-0046 regression: a cancelled
// instance reaches StatusTerminated and must publish a single
// "instance.terminated" event — NOT the old, status-inaccurate "instance.failed".
func TestCancelEmitsInstanceTerminated(t *testing.T) {
	fc := clockwork.NewFakeClock()
	store := runtimetest.MustMemStore(t)
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{})
	tasks := humantask.NewMemTaskStore()
	r := runtimetest.MustRunner(t, action.NewCatalog(nil), store,
		runtime.WithClock(fc),
		runtime.WithHumanTasks(resolver, tasks, nil))

	def := &model.ProcessDefinition{
		ID: "cancel-evt", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("wait", activity.WithEligibleRoles("r")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "wait"},
			{ID: "f2", Source: "wait", Target: "end"},
		},
	}

	_, err := r.Drive(t.Context(), def, "ce1", nil)
	require.NoError(t, err)

	st, err := r.CancelInstance(t.Context(), def, "ce1")
	require.NoError(t, err)
	require.Equal(t, engine.StatusTerminated, st.Status)

	topics := topicsOf(store.Events())
	assert.Contains(t, topics, "instance.terminated", "cancel must emit instance.terminated")
	assert.NotContains(t, topics, "instance.failed", "cancel must NOT emit instance.failed (ADR-0046)")
}

// TestCompleteEmitsInstanceCompleted guards the unchanged happy path: a normally
// completing instance still emits exactly one "instance.completed".
func TestCompleteEmitsInstanceCompleted(t *testing.T) {
	fc := clockwork.NewFakeClock()
	store := runtimetest.MustMemStore(t)
	r := runtimetest.MustRunner(t, action.NewCatalog(nil), store, runtime.WithClock(fc))

	def := &model.ProcessDefinition{
		ID: "complete-evt", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "end"},
		},
	}

	st, err := r.Drive(t.Context(), def, "co1", nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompleted, st.Status)

	assert.Equal(t, []string{"instance.completed"}, topicsOf(store.Events()))
}

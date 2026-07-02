package runtime_test

import (
	"testing"

	clockwork "github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// topicsOf collects the topics of all buffered outbox events in append order.
func topicsOf(evs []runtime.OutboxEvent) []string {
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
	store := mustMemStore(t)
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{})
	tasks := humantask.NewMemTaskStore()
	r := runtime.NewRunner(action.NewMapCatalog(nil), store,
		runtime.WithRunnerClock(fc),
		runtime.WithHumanTasks(resolver, tasks, nil))

	def := &model.ProcessDefinition{
		ID: "cancel-evt", Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("start"),
			model.NewUserTask("wait", []string{"r"}),
			model.NewEndEvent("end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "wait"},
			{ID: "f2", Source: "wait", Target: "end"},
		},
	}

	_, err := r.Run(t.Context(), def, "ce1", nil)
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
	store := mustMemStore(t)
	r := runtime.NewRunner(action.NewMapCatalog(nil), store, runtime.WithRunnerClock(fc))

	def := &model.ProcessDefinition{
		ID: "complete-evt", Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("start"),
			model.NewEndEvent("end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "end"},
		},
	}

	st, err := r.Run(t.Context(), def, "co1", nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompleted, st.Status)

	assert.Equal(t, []string{"instance.completed"}, topicsOf(store.Events()))
}

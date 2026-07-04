package processtest_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/gateway"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/processtest"
)

// TestReview_CompleteTasksMemoizesDecision covers finding #8: a parallel flow with
// two concurrent user tasks drives to completion, and decide is invoked at most
// once per task token (claim and completion reuse the memoized decision) — not
// twice per token.
func TestReview_CompleteTasksMemoizesDecision(t *testing.T) {
	t.Parallel()

	def, err := definition.NewBuilder("two-approvals", 1).
		Add(event.NewStart("start")).
		Add(gateway.NewParallel("fork")).
		Add(activity.NewUserTask("reviewA", []string{"r"})).
		Add(activity.NewUserTask("reviewB", []string{"r"})).
		Add(gateway.NewParallel("join")).
		Add(event.NewEnd("end")).
		Connect("start", "fork").
		Connect("fork", "reviewA").
		Connect("fork", "reviewB").
		Connect("reviewA", "join").
		Connect("reviewB", "join").
		Connect("join", "end").
		Build()
	require.NoError(t, err)

	h, err := processtest.New()
	require.NoError(t, err)

	var mu sync.Mutex
	calls := map[string]int{}
	decide := func(tsk humantask.HumanTask) (authz.Actor, map[string]any, bool) {
		mu.Lock()
		calls[tsk.TaskToken]++
		mu.Unlock()
		return authz.Actor{ID: "alice", Roles: []string{"r"}}, map[string]any{"ok": true}, true
	}

	_, err = h.Start(t.Context(), def, "i", nil)
	require.NoError(t, err)

	final, err := h.DriveToCompletion(t.Context(), def, "i", h.CompleteTasks(decide))
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status)

	require.Len(t, calls, 2, "both tasks should be decided")
	for token, n := range calls {
		assert.Equalf(t, 1, n, "decide must be called exactly once for token %s (memoized across claim+complete)", token)
	}
}

package runtime_test

// subprocess_inline_action_test.go — regression proof for I-1: scope-aware
// inline action resolution for nodes inside a sub-process.
//
// Before the fix the runtime resolved a node's main action against the
// TOP-LEVEL definition with a flat lookup that did not descend into
// SubProcess.Subprocess. So an inline action (WithActionFunc) on a ServiceTask
// INSIDE a sub-process silently failed: the inline tier was skipped, the name
// defaulted to the node id, name-only resolution missed → ActionFailed
// (retryable=false) → instance Failed, action never ran.
//
// This test runs the exact failing scenario end-to-end through the public
// Runner and asserts the inline action RAN and the instance did NOT fail.

import (
	"context"
	"sync/atomic"
	"testing"

	clockwork "github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/internal/runtimetest"
)

// TestInlineActionInsideSubProcessRunsE2E reproduces I-1: a ServiceTask carrying
// an inline action (WithActionFunc) that lives inside a sub-process must run,
// and the instance must reach a non-Failed terminal state.
func TestInlineActionInsideSubProcessRunsE2E(t *testing.T) {
	fc := clockwork.NewFakeClock()

	var ran atomic.Bool

	nested := &model.ProcessDefinition{
		ID: "inline-sub-nested", Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("inner-start"),
			model.NewServiceTask("inner-svc", model.WithActionFunc(
				func(_ context.Context, in map[string]any) (map[string]any, error) {
					ran.Store(true)
					return map[string]any{"done": true}, nil
				})),
			model.NewEndEvent("inner-end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "inner-svc"},
			{ID: "if2", Source: "inner-svc", Target: "inner-end"},
		},
	}
	def := &model.ProcessDefinition{
		ID: "inline-sub-def", Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("start"),
			model.NewSubProcess("sub", nested),
			model.NewEndEvent("end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "sub"},
			{ID: "f2", Source: "sub", Target: "end"},
		},
	}

	// Empty global catalog: the inline action is the ONLY way "inner-svc" resolves.
	cat := action.NewMapCatalog(map[string]action.ServiceAction{})
	store := runtimetest.MustMemStore(t)
	r := runtimetest.MustRunner(t, cat, store, runtime.WithClock(fc))

	st, err := r.Run(t.Context(), def, "inline-sub-i1", nil)
	require.NoError(t, err)

	assert.True(t, ran.Load(),
		"inline action on the ServiceTask inside the sub-process must have run")
	assert.NotEqual(t, engine.StatusFailed, st.Status,
		"instance must not be Failed: inline action inside sub-process must resolve")
	assert.Equal(t, engine.StatusCompleted, st.Status,
		"instance must complete after running the inline sub-process action")
}

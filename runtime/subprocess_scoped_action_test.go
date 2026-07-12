package runtime_test

// subprocess_scoped_action_test.go — regression proof for I-1: scope-aware
// action resolution for nodes inside a sub-process.
//
// Before the original fix the runtime resolved a node's main action against
// the TOP-LEVEL definition with a flat lookup that did not descend into
// SubProcess.Subprocess. A definition-scoped action registered on the NESTED
// definition (visible only inside that sub-process's scope) on a ServiceTask
// INSIDE a sub-process would silently fail: name-only resolution against the
// top-level definition's scoped catalog missed → ActionFailed
// (retryable=false) → instance Failed, action never ran.
//
// This test runs the exact scenario end-to-end through the public ProcessDriver and
// asserts the nested definition's scoped action RAN and the instance did NOT
// fail. The global catalog is deliberately empty: the nested definition's own
// scoped catalog is the ONLY way "inner-svc" resolves.

import (
	"context"
	"sync/atomic"
	"testing"

	clockwork "github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/definition"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/internal/runtimetest"
)

// TestScopedActionInsideSubProcessRunsE2E reproduces I-1: a ServiceTask
// resolving a definition-scoped action (registered on the nested definition)
// that lives inside a sub-process must run, and the instance must reach a
// non-Failed terminal state.
func TestScopedActionInsideSubProcessRunsE2E(t *testing.T) {
	fc := clockwork.NewFakeClock()

	var ran atomic.Bool

	nested, err := definition.NewBuilder("scoped-sub-nested", 1).
		RegisterActionFunc("inner-svc-action", func(_ context.Context, in map[string]any) (map[string]any, error) {
			ran.Store(true)
			return map[string]any{"done": true}, nil
		}).
		Add(event.NewStart("inner-start")).
		Add(activity.NewServiceTask("inner-svc", activity.WithTaskAction("inner-svc-action"))).
		Add(event.NewEnd("inner-end")).
		Connect("inner-start", "inner-svc").
		Connect("inner-svc", "inner-end").
		Build()
	require.NoError(t, err)

	def, err := definition.NewBuilder("scoped-sub-def", 1).
		Add(event.NewStart("start")).
		Add(activity.NewSubProcess("sub", nested)).
		Add(event.NewEnd("end")).
		Connect("start", "sub").
		Connect("sub", "end").
		Build()
	require.NoError(t, err)

	// Empty global catalog: the nested definition's scoped action is the ONLY
	// way "inner-svc" resolves.
	cat := action.NewCatalog(map[string]action.Action{})
	store := runtimetest.MustMemStore(t)
	r := runtimetest.MustProcessDriver(t, cat, store, runtime.WithClock(fc))

	st, err := r.Drive(t.Context(), def, "scoped-sub-i1", nil)
	require.NoError(t, err)

	assert.True(t, ran.Load(),
		"the nested definition's scoped action on the ServiceTask inside the sub-process must have run")
	assert.NotEqual(t, engine.StatusFailed, st.Status,
		"instance must not be Failed: scoped action inside sub-process must resolve")
	assert.Equal(t, engine.StatusCompleted, st.Status,
		"instance must complete after running the nested sub-process action")
}

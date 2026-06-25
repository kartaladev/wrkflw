package runtime_test

// scope_compensation_test.go — Task 4: runtime e2e for compensation throw event
// (scope-targeted compensation).
//
// Design: docs/specs/2026-06-23-scope-targeted-compensation-design.md
// ADR: 0039
//
// Verifies end-to-end (through the public Runner + MemStore + service-action
// catalog) that a process with a completed compensable sub-process, followed by
// a compensation throw event referencing that sub-process, runs the inner
// compensation action and then continues to completion.

import (
	"context"
	"testing"

	clockwork "github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// scopeCompensationDef returns a process definition:
//
//	start → sub(KindSubProcess: inner-start → inner-svc(ServiceTask, Action:"book",
//	            CompensationAction:"cancel-book") → inner-end)
//	       → compThrow(KindIntermediateThrowEvent, CompensateRef:"sub")
//	       → end
//
// The compensation throw refers to "sub". After the sub-process completes
// normally, inner-svc is archived under "sub". When compThrow fires, it runs
// cancel-book then resumes past the throw straight to end.
func scopeCompensationDef() *model.ProcessDefinition {
	nested := &model.ProcessDefinition{
		ID: "scope-comp-nested", Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("inner-start"),
			model.NewServiceTask("inner-svc", model.WithActionName("book"), model.WithCompensation("cancel-book")),
			model.NewEndEvent("inner-end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "inner-svc"},
			{ID: "if2", Source: "inner-svc", Target: "inner-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "scope-comp-def", Version: 1,
		Nodes: []model.Node{
			model.NewStartEvent("start"),
			model.NewSubProcess("sub", nested),
			// Compensation throw: runs ArchivedCompensations["sub"] then resumes.
			model.NewIntermediateThrowEvent("compThrow", model.WithCompensateRef("sub")),
			model.NewEndEvent("end"),
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "sub"},
			{ID: "f2", Source: "sub", Target: "compThrow"},
			{ID: "f3", Source: "compThrow", Target: "end"},
		},
	}
}

// TestCompensationThrowRunsSubProcessCompensationE2E is the runtime e2e for
// scope-targeted compensation (ADR-0039, Task 4).
//
// Asserts:
//  1. "book" is invoked when the sub-process runs.
//  2. "cancel-book" is invoked by the compensation throw event.
//  3. "book" is invoked before "cancel-book" (order preserved).
//  4. The instance reaches StatusCompleted (throw resumes, process runs to end).
func TestCompensationThrowRunsSubProcessCompensationE2E(t *testing.T) {
	fc := clockwork.NewFakeClock()

	// Recording catalog: tracks invocation order via appended slice.
	var invoked []string

	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"book": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			invoked = append(invoked, "book")
			return nil, nil
		}),
		"cancel-book": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			invoked = append(invoked, "cancel-book")
			return nil, nil
		}),
	})

	store := runtime.NewMemStore()
	r := runtime.NewRunner(cat, fc, store)

	def := scopeCompensationDef()
	const instanceID = "scope-comp-i1"

	// Run drives: start → sub (book) → sub exits (archived) → compThrow fires
	// (cancel-book) → resumes → end. All synchronous via deliverLoop.
	st, err := r.Run(t.Context(), def, instanceID, nil)
	require.NoError(t, err)

	// Instance must have reached a terminal COMPLETED state.
	assert.Equal(t, engine.StatusCompleted, st.Status,
		"compensation throw must resume and drive instance to StatusCompleted")

	// book must have been invoked (sub-process ran).
	assert.Contains(t, invoked, "book", "inner service task 'book' must have been invoked")

	// cancel-book must have been invoked (compensation throw ran compensation).
	assert.Contains(t, invoked, "cancel-book", "compensation action 'cancel-book' must have been invoked by the throw")

	// Order: book before cancel-book.
	require.GreaterOrEqual(t, len(invoked), 2, "at least 2 actions must have been invoked")
	bookIdx := -1
	cancelIdx := -1
	for i, name := range invoked {
		switch name {
		case "book":
			bookIdx = i
		case "cancel-book":
			cancelIdx = i
		}
	}
	assert.Less(t, bookIdx, cancelIdx,
		"'book' must be invoked before 'cancel-book' (sub runs then compensation fires)")

	// No live tokens after terminal state.
	assert.Empty(t, st.Tokens, "no live tokens after StatusCompleted")
}

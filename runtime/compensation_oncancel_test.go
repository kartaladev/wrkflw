package runtime_test

// compensation_oncancel_test.go — Task 3: runtime e2e for compensation on cancel.
//
// Design: docs/specs/2026-06-23-compensation-on-error-cancel-design.md §4 (last bullet).
// ADR: 0034.
//
// Verifies that when a running instance with a completed compensable service task
// is cancelled, the Runner drives the full compensation walk (InvokeAction →
// ActionCompleted → StatusTerminated) within the single CancelInstance call.

import (
	"context"
	"sync/atomic"
	"testing"

	clockwork "github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/internal/runtimetest"
)

// compensationOnCancelDef returns a definition that:
//   - executes a compensable service task "charge" (CompensationAction:"refund")
//   - parks at a user task "approve" so Run returns StatusRunning
//   - ending flow leads to the end event
//
// start → charge (KindServiceTask, CompensationAction:"refund") → approve (KindUserTask) → end
func compensationOnCancelDef() *definition.ProcessDefinition {
	return &definition.ProcessDefinition{
		ID: "comp-cancel-def", Version: 1,
		Nodes: []definition.Node{
			event.NewStart("start"),
			activity.NewServiceTask("charge", activity.WithActionName("charge"), activity.WithCompensation("refund")),
			activity.NewUserTask("approve", []string{"reviewer"}),
			event.NewEnd("end"),
		},
		Flows: []definition.SequenceFlow{
			{ID: "f1", Source: "start", Target: "charge"},
			{ID: "f2", Source: "charge", Target: "approve"},
			{ID: "f3", Source: "approve", Target: "end"},
		},
	}
}

// TestRunnerCompensationOnCancel is the runtime e2e for compensation on cancel (ADR-0034).
//
// Asserts:
//  1. Run completes "charge" and parks at "approve" → StatusRunning.
//  2. CancelInstance drives the compensation walk to completion within one call.
//  3. The "refund" compensation action runs exactly once.
//  4. Final status is StatusTerminated (no extra store load needed).
func TestRunnerCompensationOnCancel(t *testing.T) {
	fc := clockwork.NewFakeClock()

	var chargeRan atomic.Int32
	var refundRan atomic.Int32

	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"charge": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			chargeRan.Add(1)
			return nil, nil
		}),
		"refund": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			refundRan.Add(1)
			return nil, nil
		}),
	})

	store := runtimetest.MustMemStore(t)
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{})
	tasks := humantask.NewMemTaskStore()
	r := runtimetest.MustRunner(t, cat, store, runtime.WithClock(fc), runtime.WithHumanTasks(resolver, tasks, nil))

	def := compensationOnCancelDef()
	const instanceID = "comp-cancel-i1"

	// Run: charge completes, approve parks the instance.
	runSt, err := r.Run(t.Context(), def, instanceID, nil)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, runSt.Status, "instance must park at approve (StatusRunning)")
	assert.EqualValues(t, 1, chargeRan.Load(), "charge action must have run exactly once")
	assert.EqualValues(t, 0, refundRan.Load(), "refund must not have run yet")

	// Cancel: the engine enters StatusCompensating, performs the "refund" InvokeAction
	// inside deliverLoop, delivers ActionCompleted, and advances to StatusTerminated —
	// all within this single CancelInstance call.
	cancelSt, err := r.CancelInstance(t.Context(), def, instanceID)
	require.NoError(t, err)

	assert.Equal(t, engine.StatusTerminated, cancelSt.Status,
		"compensation walk must complete within CancelInstance (single call, no extra drive needed)")
	assert.EqualValues(t, 1, refundRan.Load(),
		"refund (compensation action) must have run exactly once on cancel")
	assert.Empty(t, cancelSt.Tokens, "no live tokens after terminal")
}

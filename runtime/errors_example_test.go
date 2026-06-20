package runtime_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// ── helpers ──────────────────────────────────────────────────────────────────

// orderRecorder is a concurrency-safe ordered slice of action names that
// records the sequence in which actions are invoked by the runner.
type orderRecorder struct {
	mu    sync.Mutex
	order []string
}

func (r *orderRecorder) record(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.order = append(r.order, name)
}

func (r *orderRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// recordingAction is a ServiceAction that records its name in rec and optionally
// fails with a given error string. If errMsg is empty the action succeeds.
type recordingAction struct {
	name   string
	rec    *orderRecorder
	errMsg string
}

func (a *recordingAction) Do(_ context.Context, _ map[string]any) (map[string]any, error) {
	a.rec.record(a.name)
	if a.errMsg != "" {
		return nil, &sagaActionError{msg: a.errMsg}
	}
	return nil, nil
}

type sagaActionError struct{ msg string }

func (e *sagaActionError) Error() string { return e.msg }

// ── saga definition ───────────────────────────────────────────────────────────

// sagaDef builds a process modelling a classic booking saga:
//
//	start → book (CompensationAction:"cancel-booking")
//	       → pay  (CompensationAction:"refund")
//	       → ship (may fail via ActionFailed)
//	       → end
//
// The ship node has no compensation action — if it fails the engine propagates
// to an error end event (no boundary handler), setting StatusFailed.  After the
// failure an admin can trigger CompensateRequested to roll back book+pay in
// reverse order (refund THEN cancel-booking).
func sagaDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "saga", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "book", Kind: model.KindServiceTask, Action: "book",
				CompensationAction: "cancel-booking"},
			{ID: "pay", Kind: model.KindServiceTask, Action: "pay",
				CompensationAction: "refund"},
			{ID: "ship", Kind: model.KindServiceTask, Action: "ship"},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "book"},
			{ID: "f2", Source: "book", Target: "pay"},
			{ID: "f3", Source: "pay", Target: "ship"},
			{ID: "f4", Source: "ship", Target: "end"},
		},
	}
}

// ── TestSagaCompensationRollback ──────────────────────────────────────────────

// TestSagaCompensationRollback is the saga e2e test:
//
//  1. book and pay run to completion, recording their compensation actions.
//  2. ship fails (ActionFailed, no boundary handler) → instance → StatusFailed.
//  3. Admin delivers CompensateRequested{ToNode:""} via Runner.Deliver.
//  4. The runner drives the compensation InvokeAction stream to completion:
//     refund (for pay) runs BEFORE cancel-booking (for book) — reverse order.
//  5. Final status is StatusTerminated (full rollback, ToNode=="").
func TestSagaCompensationRollback(t *testing.T) {
	ctx := t.Context()

	rec := &orderRecorder{}

	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"book":           &recordingAction{name: "book", rec: rec},
		"pay":            &recordingAction{name: "pay", rec: rec},
		"ship":           &recordingAction{name: "ship", rec: rec, errMsg: "ship-failed"},
		"cancel-booking": &recordingAction{name: "cancel-booking", rec: rec},
		"refund":         &recordingAction{name: "refund", rec: rec},
	})

	clk := clock.System()
	store := runtime.NewMemStateStore()
	jnl := runtime.NewMemJournal()
	out := runtime.NewMemOutbox()

	runner := runtime.NewRunner(cat, clk, store, jnl, out)

	def := sagaDef()

	// --- Step 1: run the saga until ship fails.
	st, err := runner.Run(ctx, def, "saga-i1", nil)
	require.NoError(t, err, "runner.Run must not return a hard error; failure is a FailInstance command")

	// book and pay completed; ship failed → StatusFailed.
	assert.Equal(t, engine.StatusFailed, st.Status, "instance must be StatusFailed after ship fails")
	require.NotNil(t, st.EndedAt)

	// Compensation records must have been recorded for book and pay (in that order).
	require.Len(t, st.RootCompensations, 2, "book and pay must have recorded compensation entries")
	assert.Equal(t, "book", st.RootCompensations[0].NodeID, "first compensation record must be book")
	assert.Equal(t, "pay", st.RootCompensations[1].NodeID, "second compensation record must be pay")

	// Forward-execution order: book, pay, ship.
	forwardOrder := rec.snapshot()
	require.Equal(t, []string{"book", "pay", "ship"}, forwardOrder, "forward actions must run in order")

	// --- Step 2: admin triggers full compensation rollback.
	trg := engine.NewCompensateRequested(clk.Now(), "") // ToNode="" → full rollback
	finalSt, err := runner.Deliver(ctx, def, "saga-i1", trg)
	require.NoError(t, err, "Deliver(CompensateRequested) must not error")

	// --- Step 3: assert reverse-order compensation.
	// refund (for pay) must run BEFORE cancel-booking (for book).
	allActions := rec.snapshot()
	// allActions = [book, pay, ship, refund, cancel-booking]
	require.GreaterOrEqual(t, len(allActions), 5, "at least 5 actions must have been recorded")

	compActions := allActions[3:] // everything after the 3 forward actions
	assert.Equal(t, []string{"refund", "cancel-booking"}, compActions,
		"compensation actions must run in reverse order: refund THEN cancel-booking")

	// --- Step 4: final status.
	// Full rollback (ToNode=="") → StatusTerminated.
	assert.Equal(t, engine.StatusTerminated, finalSt.Status,
		"full rollback must leave the instance StatusTerminated")
}

// ── boundaryErrorDef ─────────────────────────────────────────────────────────

// boundaryErrorDef builds a process where a service task may fail and is
// caught by a boundary error event, routing execution to a recovery task:
//
//	start → risky (ServiceTask "risky-action")
//	             ↘ (boundary error, catch-all) → recover (ServiceTask "recover-action") → end
//	       → end  (normal path, if risky succeeds)
func boundaryErrorDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "boundary-error-recovery", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "risky", Kind: model.KindServiceTask, Action: "risky-action"},
			// KindBoundaryEvent: error boundary attached to "risky", catch-all (ErrorCode=="").
			{ID: "err-boundary", Kind: model.KindBoundaryEvent, AttachedTo: "risky", ErrorCode: ""},
			{ID: "recover", Kind: model.KindServiceTask, Action: "recover-action"},
			{ID: "end", Kind: model.KindEndEvent},
			{ID: "end-recovery", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "risky"},
			{ID: "f2", Source: "risky", Target: "end"},          // normal path
			{ID: "f3", Source: "err-boundary", Target: "recover"}, // recovery path
			{ID: "f4", Source: "recover", Target: "end-recovery"},
		},
	}
}

// TestBoundaryErrorRecoveryE2E verifies that when a service task fails and a
// boundary error event is attached to it, the runner catches the error and routes
// execution to the recovery path, completing the instance via the recovery branch.
func TestBoundaryErrorRecoveryE2E(t *testing.T) {
	ctx := t.Context()

	rec := &orderRecorder{}

	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"risky-action":   &recordingAction{name: "risky-action", rec: rec, errMsg: "risky-failed"},
		"recover-action": &recordingAction{name: "recover-action", rec: rec},
	})

	clk := clock.System()
	store := runtime.NewMemStateStore()
	jnl := runtime.NewMemJournal()
	out := runtime.NewMemOutbox()

	runner := runtime.NewRunner(cat, clk, store, jnl, out)

	def := boundaryErrorDef()

	// Run: risky-action fails → boundary error catches it → recover-action runs → end.
	st, err := runner.Run(ctx, def, "boundary-i1", nil)
	require.NoError(t, err, "runner.Run must not return a hard error: error is caught by boundary")

	// Instance must have completed via the recovery path.
	assert.Equal(t, engine.StatusCompleted, st.Status,
		"instance must be StatusCompleted after recovery path executes")
	require.NotNil(t, st.EndedAt)
	assert.Empty(t, st.Tokens, "all tokens must be consumed on completion")

	// Both actions must have been invoked in order: risky-action first, then recover-action.
	actions := rec.snapshot()
	assert.Equal(t, []string{"risky-action", "recover-action"}, actions,
		"risky-action must run first (fails), then recover-action on the boundary recovery path")

	// The outbox must carry instance.completed (not instance.failed).
	events := out.Events()
	var foundCompleted bool
	for _, e := range events {
		if e.Topic == "instance.completed" {
			foundCompleted = true
			break
		}
	}
	assert.True(t, foundCompleted, "expected instance.completed outbox event — not instance.failed")
}

package runtime_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/internal/runtimetest"
)

// ── helpers ──────────────────────────────────────────────────────────────────

// orderRecorder is a concurrency-safe ordered slice of action names that
// records the sequence in which actions are invoked by the driver.
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

// recordingAction is a action.Action that records its name in rec and optionally
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
//	start → book (CompensateAction:"cancel-booking")
//	       → pay  (CompensateAction:"refund")
//	       → ship (may fail; caught by boundary error → end-fail)
//	       → end
//
// ship failure is caught by a boundary error event that routes to end-fail
// (StatusCompleted). This keeps RootCompensations intact so that an admin can
// trigger CompensateRequested to roll back book+pay in reverse order
// (refund THEN cancel-booking). Without the boundary, ADR-0034 auto-runs
// compensation on the unhandled-error terminal path before StatusFailed.
func sagaDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "saga", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("book", activity.WithTaskAction("book"), activity.WithCompensateAction("cancel-booking")),
			activity.NewServiceTask("pay", activity.WithTaskAction("pay"), activity.WithCompensateAction("refund")),
			activity.NewServiceTask("ship", activity.WithTaskAction("ship")),
			// Boundary catches ship failure so the unhandled-error auto-compensation
			// path (ADR-0034) is not triggered; RootCompensations stays intact for
			// the admin-triggered CompensateRequested below.
			event.NewBoundary("ship-err", "ship", event.WithBoundaryErrorCode("")),
			event.NewEnd("end"),
			event.NewEnd("end-fail"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "book"},
			{ID: "f2", Source: "book", Target: "pay"},
			{ID: "f3", Source: "pay", Target: "ship"},
			{ID: "f4", Source: "ship", Target: "end"},
			{ID: "f5", Source: "ship-err", Target: "end-fail"},
		},
	}
}

// ── TestSagaCompensationRollback ──────────────────────────────────────────────

// TestSagaCompensationRollback is the saga e2e test:
//
//  1. book and pay run to completion, recording their compensation actions.
//  2. ship fails; a boundary error event catches it → instance routes to end-fail
//     → StatusCompleted (boundary path; RootCompensations preserved for admin rollback).
//  3. Admin delivers CompensateRequested{ToNode:""} via ProcessDriver.ApplyTrigger.
//  4. The driver drives the compensation InvokeAction stream to completion:
//     refund (for pay) runs BEFORE cancel-booking (for book) — reverse order.
//  5. Final status is StatusTerminated (full rollback, ToNode=="").
//
// Note: sagaDef uses a boundary error on ship so that ship failure is not an
// unhandled-error terminal event. Without the boundary, ADR-0034 auto-runs the
// compensation walk before StatusFailed, consuming records before the admin trigger.
func TestSagaCompensationRollback(t *testing.T) {
	ctx := t.Context()

	rec := &orderRecorder{}

	cat := action.NewCatalog(map[string]action.Action{
		"book":           &recordingAction{name: "book", rec: rec},
		"pay":            &recordingAction{name: "pay", rec: rec},
		"ship":           &recordingAction{name: "ship", rec: rec, errMsg: "ship-failed"},
		"cancel-booking": &recordingAction{name: "cancel-booking", rec: rec},
		"refund":         &recordingAction{name: "refund", rec: rec},
	})

	// Use a fake clock per ADR-0003 / project test policy. The saga has no
	// timer-driven nodes, so behaviour is identical to a real clock. clockwork.FakeClock
	// structurally satisfies clock.Clock (it implements Now() time.Time).
	fakeClock := clockwork.NewFakeClockAt(time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC))

	store := runtimetest.MustMemStore(t)

	driver := runtimetest.MustRunner(t, cat, store, runtime.WithClock(fakeClock))

	def := sagaDef()

	// --- Step 1: run the saga until ship fails (caught by boundary → StatusCompleted).
	st, err := driver.Drive(ctx, def, "saga-i1", nil)
	require.NoError(t, err, "runner.Run must not return a hard error; ship failure is caught by boundary")

	// ship failure is caught by the boundary → routes to end-fail → StatusCompleted.
	// (A boundary-caught error does NOT trigger the unhandled-error auto-compensation
	// path introduced in ADR-0034, so RootCompensations remain intact for admin rollback.)
	assert.Equal(t, engine.StatusCompleted, st.Status, "instance must be StatusCompleted after ship fails via boundary")
	require.NotNil(t, st.EndedAt)

	// Compensation records must have been recorded for book and pay (in that order).
	require.Len(t, st.RootCompensations, 2, "book and pay must have recorded compensation entries")
	assert.Equal(t, "book", st.RootCompensations[0].NodeID, "first compensation record must be book")
	assert.Equal(t, "pay", st.RootCompensations[1].NodeID, "second compensation record must be pay")

	// Forward-execution order: book, pay, ship (boundary caught; no auto-compensation yet).
	forwardOrder := rec.snapshot()
	require.Equal(t, []string{"book", "pay", "ship"}, forwardOrder, "forward actions must run in order")

	// --- Step 2: admin triggers full compensation rollback.
	trg := engine.NewCompensateRequested(fakeClock.Now(), "") // ToNode="" → full rollback
	finalSt, err := driver.ApplyTrigger(ctx, def, "saga-i1", trg)
	require.NoError(t, err, "ApplyTrigger(CompensateRequested) must not error")

	// --- Step 3: assert reverse-order compensation.
	// Full-slice equality catches both order AND count regressions.
	// allActions = [book, pay, ship, refund, cancel-booking]
	allActions := rec.snapshot()
	assert.Equal(t, []string{"book", "pay", "ship", "refund", "cancel-booking"}, allActions,
		"all actions must run in exact order: forward (book, pay, ship) then compensation in reverse (refund, cancel-booking)")

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
			event.NewStart("start"),
			activity.NewServiceTask("risky", activity.WithTaskAction("risky-action")),
			// KindBoundaryEvent: error boundary attached to "risky", catch-all (ErrorCode=="").
			event.NewBoundary("err-boundary", "risky", event.WithBoundaryErrorCode("")),
			activity.NewServiceTask("recover", activity.WithTaskAction("recover-action")),
			event.NewEnd("end"),
			event.NewEnd("end-recovery"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "risky"},
			{ID: "f2", Source: "risky", Target: "end"},            // normal path
			{ID: "f3", Source: "err-boundary", Target: "recover"}, // recovery path
			{ID: "f4", Source: "recover", Target: "end-recovery"},
		},
	}
}

// TestBoundaryErrorRecoveryE2E verifies that when a service task fails and a
// boundary error event is attached to it, the driver catches the error and routes
// execution to the recovery path, completing the instance via the recovery branch.
func TestBoundaryErrorRecoveryE2E(t *testing.T) {
	ctx := t.Context()

	rec := &orderRecorder{}

	cat := action.NewCatalog(map[string]action.Action{
		"risky-action":   &recordingAction{name: "risky-action", rec: rec, errMsg: "risky-failed"},
		"recover-action": &recordingAction{name: "recover-action", rec: rec},
	})

	store := runtimetest.MustMemStore(t)

	driver := runtimetest.MustRunner(t, cat, store)

	def := boundaryErrorDef()

	// Run: risky-action fails → boundary error catches it → recover-action runs → end.
	st, err := driver.Drive(ctx, def, "boundary-i1", nil)
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
	events := store.Events()
	var foundCompleted bool
	for _, e := range events {
		if e.Topic == "instance.completed" {
			foundCompleted = true
			break
		}
	}
	assert.True(t, foundCompleted, "expected instance.completed outbox event — not instance.failed")
}

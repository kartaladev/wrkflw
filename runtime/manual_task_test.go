package runtime_test

import (
	"errors"
	"testing"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/runtime/task"
)

// TestManualTaskCompletesOnBareTrigger locks ADR-0118: a manual, roleless
// UserTask (WithManual(false), no eligibility) drives to StatusCompleted via a
// bare completion trigger — no claim, no payload.
func TestManualTaskCompletesOnBareTrigger(t *testing.T) {
	ctx := t.Context()

	def, err := definition.NewBuilder("manual-demo", 1).
		Add(event.NewStart("s")).
		Add(activity.NewUserTask("confirm", activity.WithManual(false))). // no eligibility
		Add(event.NewEnd("e")).
		Connect("s", "confirm").Connect("confirm", "e").
		Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	taskStore := humantask.NewMemTaskStore()
	memSt, err := kernel.NewMemInstanceStore()
	if err != nil {
		t.Fatalf("memstore: %v", err)
	}
	driver, err := runtime.NewProcessDriver(
		runtime.WithInstanceStore(memSt),
		runtime.WithHumanTasks(humantask.NewStaticActorResolver(nil), taskStore, authz.RoleAuthorizer{}),
	)
	if err != nil {
		t.Fatalf("driver: %v", err)
	}

	const id = "m-1"
	parked, err := driver.Drive(ctx, def, id, nil)
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	if parked.Status != engine.StatusRunning {
		t.Fatalf("status = %v, want Running (parked at manual task)", parked.Status)
	}

	// Find the OPEN task (Tasks accumulates; never index 0 blindly).
	var token string
	for i := range parked.Tasks {
		if parked.Tasks[i].IsOpen() {
			token = parked.Tasks[i].TaskToken
			break
		}
	}
	if token == "" {
		t.Fatal("no open human task after driving to the manual node")
	}

	svc, err := task.NewTaskService(taskStore, authz.RoleAuthorizer{})
	if err != nil {
		t.Fatalf("task service: %v", err)
	}
	// Bare completion: no claim, no payload.
	trg, err := svc.Complete(ctx, token, authz.Actor{ID: "operator"}, nil)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	final, err := driver.ApplyTrigger(ctx, def, id, trg)
	if err != nil {
		t.Fatalf("apply complete: %v", err)
	}
	if final.Status != engine.StatusCompleted {
		t.Fatalf("status = %v, want Completed", final.Status)
	}
}

// TestManualWaitTaskRejectsPayload locks ADR-0118: a wait-mode manual UserTask
// (WithManual(false)) is a form-less checkpoint — completing it with a
// non-empty output must be rejected with engine.ErrManualTaskPayload.
func TestManualWaitTaskRejectsPayload(t *testing.T) {
	ctx := t.Context()
	def, err := definition.NewBuilder("manual-payload", 1).
		Add(event.NewStart("s")).
		Add(activity.NewUserTask("confirm", activity.WithManual(false))).
		Add(event.NewEnd("e")).
		Connect("s", "confirm").Connect("confirm", "e").
		Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	taskStore := humantask.NewMemTaskStore()
	memSt, err := kernel.NewMemInstanceStore()
	if err != nil {
		t.Fatalf("memstore: %v", err)
	}
	driver, err := runtime.NewProcessDriver(
		runtime.WithInstanceStore(memSt),
		runtime.WithHumanTasks(humantask.NewStaticActorResolver(nil), taskStore, authz.RoleAuthorizer{}),
	)
	if err != nil {
		t.Fatalf("driver: %v", err)
	}
	const id = "mp-1"
	parked, err := driver.Drive(ctx, def, id, nil)
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	var token string
	for i := range parked.Tasks {
		if parked.Tasks[i].IsOpen() {
			token = parked.Tasks[i].TaskToken
			break
		}
	}
	if token == "" {
		t.Fatal("no open human task after driving to the manual node")
	}
	svc, err := task.NewTaskService(taskStore, authz.RoleAuthorizer{})
	if err != nil {
		t.Fatalf("svc: %v", err)
	}
	trg, err := svc.Complete(ctx, token, authz.Actor{ID: "operator"}, map[string]any{"note": "oops"})
	if err != nil {
		t.Fatalf("complete build: %v", err)
	}
	_, err = driver.ApplyTrigger(ctx, def, id, trg)
	if !errors.Is(err, engine.ErrManualTaskPayload) {
		t.Fatalf("err = %v, want ErrManualTaskPayload", err)
	}
	// The rejected payload must not have been merged into instance variables:
	// the guard must run before mergeVars, not after.
	reloaded, _, loadErr := memSt.Load(ctx, id)
	if loadErr != nil {
		t.Fatalf("reload: %v", loadErr)
	}
	if _, ok := reloaded.Variables["note"]; ok {
		t.Fatal("rejected payload key \"note\" leaked into instance variables")
	}
}

// TestImmediateManualTaskAutoCompletes locks ADR-0118: an immediate-mode
// manual UserTask (WithManual(true)) never parks — driving alone, with no
// external trigger, must record a completed human task (audit trail) and
// reach StatusCompleted.
func TestImmediateManualTaskAutoCompletes(t *testing.T) {
	ctx := t.Context()
	def, err := definition.NewBuilder("manual-immediate", 1).
		Add(event.NewStart("s")).
		Add(activity.NewUserTask("noted", activity.WithManual(true))).
		Add(event.NewEnd("e")).
		Connect("s", "noted").Connect("noted", "e").
		Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	taskStore := humantask.NewMemTaskStore()
	memSt, err := kernel.NewMemInstanceStore()
	if err != nil {
		t.Fatalf("memstore: %v", err)
	}
	driver, err := runtime.NewProcessDriver(
		runtime.WithInstanceStore(memSt),
		runtime.WithHumanTasks(humantask.NewStaticActorResolver(nil), taskStore, authz.RoleAuthorizer{}),
	)
	if err != nil {
		t.Fatalf("driver: %v", err)
	}
	// No external trigger: driving alone must reach Completed.
	final, err := driver.Drive(ctx, def, "mi-1", nil)
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	if final.Status != engine.StatusCompleted {
		t.Fatalf("status = %v, want Completed (immediate manual auto-completes)", final.Status)
	}
	// Audit: a completed task for the manual node exists in history.
	var found bool
	for i := range final.Tasks {
		if final.Tasks[i].NodeID == "noted" && final.Tasks[i].State == humantask.Completed {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("no completed task recorded for the immediate manual node")
	}
}

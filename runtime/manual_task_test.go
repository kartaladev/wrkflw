package runtime_test

import (
	"testing"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/task"
)

// TestManualTaskCompletesOnBareTrigger locks ADR-0118: a manual, roleless
// UserTask (WithManual(), no eligibility) drives to StatusCompleted via a
// bare completion trigger — no claim, no payload.
func TestManualTaskCompletesOnBareTrigger(t *testing.T) {
	ctx := t.Context()

	def, err := definition.NewBuilder("manual-demo", 1).
		Add(event.NewStart("s")).
		Add(activity.NewUserTask("confirm", activity.WithManual())). // no eligibility
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

package runtime_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/runtime"
)

// barrierDef builds a one-service-task definition (start -> service("barrier") -> end)
// whose action is resolved by name "barrier" from the driver's catalog. Register a
// blocking "barrier" action to hold a Drive call in-flight while a test calls Shutdown.
func barrierDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "barrier", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("task", activity.WithTaskAction("barrier")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "task"},
			{ID: "f2", Source: "task", Target: "end"},
		},
	}
}

// barrierAction returns an action that signals `enter` when it starts, then blocks on
// `release`, letting a test hold a Drive call parked inside the action.
func barrierAction(enter, release chan struct{}) action.ActionFunc {
	return func(_ context.Context, _ map[string]any) (map[string]any, error) {
		close(enter)
		<-release
		return nil, nil
	}
}

func TestShutdownDrainsInFlight(t *testing.T) {
	enter, release := make(chan struct{}), make(chan struct{})
	cat := action.NewCatalog(map[string]action.Action{"barrier": barrierAction(enter, release)})
	driver, err := runtime.NewProcessDriver(runtime.WithActionCatalog(cat))
	require.NoError(t, err)

	def := barrierDef()

	driveDone := make(chan struct{})
	go func() {
		_, _ = driver.Drive(context.Background(), def, "i-barrier", nil)
		close(driveDone)
	}()
	<-enter // Drive is now parked inside the blocking action

	shutdownReturned := make(chan error, 1)
	go func() { shutdownReturned <- driver.Shutdown(context.Background()) }()

	select {
	case <-shutdownReturned:
		t.Fatal("Shutdown returned before in-flight Drive finished")
	case <-time.After(100 * time.Millisecond):
		// good: Shutdown is blocked on the drain
	}

	close(release) // let the action finish
	<-driveDone
	require.NoError(t, <-shutdownReturned)
}

func TestShutdownDrainTimeout(t *testing.T) {
	enter, release := make(chan struct{}), make(chan struct{})
	cat := action.NewCatalog(map[string]action.Action{"barrier": barrierAction(enter, release)})
	driver, err := runtime.NewProcessDriver(runtime.WithActionCatalog(cat))
	require.NoError(t, err)
	def := barrierDef()

	driveDone := make(chan struct{})
	go func() { _, _ = driver.Drive(context.Background(), def, "i-timeout", nil); close(driveDone) }()
	<-enter

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err = driver.Shutdown(ctx)
	assert.ErrorIs(t, err, runtime.ErrDrainTimeout)

	// goleak: release the barrier so the Drive goroutine + waitInflight goroutine exit
	// before the test (and TestMain's leak check) finishes.
	close(release)
	<-driveDone
}

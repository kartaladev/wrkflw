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
	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/kernel"
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

func TestApplyTriggerRejectedWhenDraining(t *testing.T) {
	driver, err := runtime.NewProcessDriver()
	require.NoError(t, err)
	require.NoError(t, driver.Shutdown(context.Background()))

	// Any def/id: the gate rejects before touching the store.
	_, err = driver.ApplyTrigger(context.Background(), linearDef(), "i-x",
		engine.NewCancelRequested(time.Now()))
	assert.ErrorIs(t, err, runtime.ErrDriverShuttingDown)
}

func TestExternalEntryPointsRejectedWhenDraining(t *testing.T) {
	// Each case funnels through a distinct exported entry point; all must reject
	// with ErrDriverShuttingDown once the driver is draining (strict quiescence, D1).
	tests := map[string]func(d *runtime.ProcessDriver) error{
		"Drive": func(d *runtime.ProcessDriver) error {
			_, err := d.Drive(context.Background(), linearDef(), "i-1", nil)
			return err
		},
		"DeliverMessage": func(d *runtime.ProcessDriver) error {
			return d.DeliverMessage(context.Background(), "m", "k", nil)
		},
		"BroadcastSignal": func(d *runtime.ProcessDriver) error {
			return d.BroadcastSignal(context.Background(), "s", nil)
		},
		"CancelInstance": func(d *runtime.ProcessDriver) error {
			_, err := d.CancelInstance(context.Background(), linearDef(), "i-1")
			return err
		},
		"ResolveIncident": func(d *runtime.ProcessDriver) error {
			_, err := d.ResolveIncident(context.Background(), linearDef(), "i-1", "inc", 1)
			return err
		},
		"ReverseInstance": func(d *runtime.ProcessDriver) error {
			_, err := d.ReverseInstance(context.Background(), linearDef(), "i-1")
			return err
		},
	}
	for name, call := range tests {
		t.Run(name, func(t *testing.T) {
			driver, err := runtime.NewProcessDriver()
			require.NoError(t, err)
			require.NoError(t, driver.Shutdown(context.Background()))
			assert.ErrorIs(t, call(driver), runtime.ErrDriverShuttingDown)
		})
	}
}

// timerStartFireDef builds start(timer-start) -> end, so createAtNode seeded at
// "start" drives straight to completion, creating exactly one instance per fire.
func timerStartFireDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "timer-start-fire", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start", event.WithStartTimer(schedule.AfterExpr(`"1h"`))),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "end"},
		},
	}
}

func TestTimerStartFire(t *testing.T) {
	// The timer-start fire callback creates a brand-new instance on each fire — but
	// only when the driver is admitting work. Once draining, the fire is dropped so
	// no new instance is created (strict quiescence, D1).
	type testCase struct {
		name     string
		draining bool
		assert   func(t *testing.T, items []kernel.InstanceSummary)
	}
	cases := []testCase{
		{
			name:     "fires and creates an instance when not draining",
			draining: false,
			assert: func(t *testing.T, items []kernel.InstanceSummary) {
				assert.Len(t, items, 1, "a fire must create exactly one instance")
			},
		},
		{
			name:     "dropped when draining",
			draining: true,
			assert: func(t *testing.T, items []kernel.InstanceSummary) {
				assert.Empty(t, items, "timer-start must not create an instance during shutdown")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, err := kernel.NewMemInstanceStore()
			require.NoError(t, err)
			driver, err := runtime.NewProcessDriver(runtime.WithInstanceStore(store))
			require.NoError(t, err)
			t.Cleanup(func() { _ = driver.Shutdown(context.Background()) })

			def := timerStartFireDef()
			if tc.draining {
				require.NoError(t, driver.Shutdown(context.Background()))
			}

			fire := driver.StartTimerFireFuncForTest(def, "start", "start-timer:timer-start-fire:1:start")
			fire()

			page, err := store.List(context.Background(), kernel.InstanceFilter{})
			require.NoError(t, err)
			tc.assert(t, page.Items)
		})
	}
}

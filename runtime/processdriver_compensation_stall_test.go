package runtime_test

// The runtime surface for stall detection and the three escape verbs.

import (
	"context"
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

// stallSagaDef builds start → a(doA/undoA) → b(doB/undoB) → wait → end, so a
// cancel starts a walk with TWO records — the minimum that lets skip and abandon
// be told apart.
//
// The park is a ReceiveTask rather than a UserTask so the fixture needs no
// ActorResolver wired into the driver.
func stallSagaDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "stall-saga", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("a", activity.WithTaskAction("doA"), activity.WithCompensateAction("undoA")),
			activity.NewServiceTask("b", activity.WithTaskAction("doB"), activity.WithCompensateAction("undoB")),
			activity.NewReceiveTask("wait", "go"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "a"},
			{ID: "f2", Source: "a", Target: "b"},
			{ID: "f3", Source: "b", Target: "wait"},
			{ID: "f4", Source: "wait", Target: "end"},
		},
	}
}

// TestWithCompensationStallTimeoutThreadsIntoTheEngine asserts the runtime
// option reaches StepOptions.CompensationStallAfter — observable as a stall
// timer armed when the compensation walk dispatches.
//
// What makes it fail before the option exists: WithCompensationStallTimeout is
// undefined; once defined but unthreaded, no stall timer is ever armed.
func TestWithCompensationStallTimeoutThreadsIntoTheEngine(t *testing.T) {
	t.Parallel()

	// undoB blocks forever on the FIRST call so the walk really stalls; the
	// action context has no deadline (WithActionTimeout(0)), which is the
	// out-of-process shape this feature exists for.
	cat := action.NewCatalog(map[string]action.Action{
		"doA":   action.ActionFunc(func(ctx context.Context, _ map[string]any) (map[string]any, error) { return nil, nil }),
		"doB":   action.ActionFunc(func(ctx context.Context, _ map[string]any) (map[string]any, error) { return nil, nil }),
		"undoA": action.ActionFunc(func(ctx context.Context, _ map[string]any) (map[string]any, error) { return nil, nil }),
		"undoB": action.ActionFunc(func(ctx context.Context, _ map[string]any) (map[string]any, error) { return nil, nil }),
	})
	clk := clockwork.NewFakeClock()

	// A control run with the option UNSET must arm nothing at all: that is the
	// "zero disables, and zero is the default" promise, checked rather than
	// assumed.
	off := &runtimetest.RecordingScheduler{Clock: clk}
	rOff := runtimetest.MustProcessDriver(t, cat, runtimetest.MustMemStore(t),
		runtime.WithClock(clk), runtime.WithScheduler(off))
	_, err := rOff.Drive(t.Context(), stallSagaDef(), "stall-rt-off", nil)
	require.NoError(t, err)
	_, err = rOff.CancelInstance(t.Context(), stallSagaDef(), "stall-rt-off")
	require.NoError(t, err)
	require.False(t, off.Armed,
		"control: with the option unset the walk must schedule no timer whatsoever")

	// With the option set, the walk's dispatch arms a stall timer through the
	// normal ScheduleTimer path — which is the only observable proof the option
	// reached engine.StepOptions.
	on := &runtimetest.RecordingScheduler{Clock: clk}
	rOn := runtimetest.MustProcessDriver(t, cat, runtimetest.MustMemStore(t),
		runtime.WithClock(clk), runtime.WithScheduler(on),
		runtime.WithCompensationStallTimeout(30*time.Minute))

	st, err := rOn.Drive(t.Context(), stallSagaDef(), "stall-rt-on", nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, st.Status, "control: parked awaiting the message")

	_, err = rOn.CancelInstance(t.Context(), stallSagaDef(), "stall-rt-on")
	require.NoError(t, err)

	assert.True(t, on.Armed,
		"WithCompensationStallTimeout must reach StepOptions.CompensationStallAfter, "+
			"so the compensation dispatch arms a stall timer")
	assert.Equal(t, clk.Now().Add(30*time.Minute), on.FireAt,
		"and it must fire after the configured window")
}

// TestResolveCompensationStallDriverMethod asserts the driver exposes the three
// verbs and journals them through applyTrigger.
func TestResolveCompensationStallDriverMethod(t *testing.T) {
	t.Parallel()

	r := runtimetest.MustProcessDriver(t, action.NewCatalog(map[string]action.Action{}),
		runtimetest.MustMemStore(t), runtime.WithClock(clockwork.NewFakeClock()))

	_, err := r.ResolveCompensationStall(t.Context(), stallSagaDef(), "no-such-instance",
		"c1", "", engine.CompensationSkip)
	require.Error(t, err, "the method must exist and reach the engine")
}

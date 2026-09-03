package runtime_test

// The runtime surface that makes compensation retry REACHABLE. The engine knob
// is engine.StepOptions.CompensationRetryPolicy; this file pins the
// ProcessDriver option that threads a consumer's policy into it.

import (
	"context"
	"errors"
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

// retrySagaDef builds start → a(doA/undoA) → b(doB/undoB) → wait → end, so a
// cancel starts a walk with TWO records. Two is the minimum that makes "the walk
// did not advance" observable: with undoB failing, whether undoA is ever invoked
// is what separates retry-the-record from skip-and-advance.
//
// The park is a ReceiveTask rather than a UserTask so the fixture needs no
// ActorResolver wired into the driver.
func retrySagaDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "retry-saga", Version: 1,
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

// callRecorder records, in order, the names of the actions the driver invoked.
// The driver runs actions on the calling goroutine, but the mutex keeps the
// recorder honest under -race regardless.
type callRecorder struct {
	mu    sync.Mutex
	names []string
}

func (r *callRecorder) add(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.names = append(r.names, name)
}

func (r *callRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.names...)
}

// retrySagaCatalog builds the four actions of retrySagaDef with undoB always
// failing on a plain (therefore action.IsRetryable ⇒ true) error, recording every
// invocation into rec.
func retrySagaCatalog(rec *callRecorder) action.Catalog {
	ok := func(name string) action.Action {
		return action.ActionFunc(func(context.Context, map[string]any) (map[string]any, error) {
			rec.add(name)
			return nil, nil
		})
	}
	return action.NewCatalog(map[string]action.Action{
		"doA":   ok("doA"),
		"doB":   ok("doB"),
		"undoA": ok("undoA"),
		"undoB": action.ActionFunc(func(context.Context, map[string]any) (map[string]any, error) {
			rec.add("undoB")
			return nil, errors.New("undoB blew up")
		}),
	})
}

// TestWithCompensationRetryPolicyThreadsIntoTheEngine asserts the runtime option
// reaches engine.StepOptions.CompensationRetryPolicy — observable as a backoff
// timer armed for the FAILED record instead of the walk skipping past it.
//
// What makes it fail before the option exists: WithCompensationRetryPolicy is
// undefined, so the file does not compile. Once defined but NOT threaded into the
// engine.StepOptions literal in ProcessDriver's Step call, the "on" leg still
// takes the skip-and-advance path — no timer is ever armed and
// undoA is invoked — which is exactly what the control leg measures. So the two
// legs cannot both pass unless the policy actually reached the engine.
func TestWithCompensationRetryPolicyThreadsIntoTheEngine(t *testing.T) {
	t.Parallel()

	// A control run with the option UNSET must behave exactly as it did before
	// retry existed: undoB fails, the walk SKIPS it and runs undoA, and nothing is
	// scheduled. That is the "nil disables, and nil is the default" promise,
	// checked rather than assumed.
	clkOff := clockwork.NewFakeClock()
	recOff := &callRecorder{}
	off := &runtimetest.RecordingScheduler{Clock: clkOff}
	rOff := runtimetest.MustProcessDriver(t, retrySagaCatalog(recOff), runtimetest.MustMemStore(t),
		runtime.WithClock(clkOff), runtime.WithScheduler(off))

	_, err := rOff.Drive(t.Context(), retrySagaDef(), "retry-rt-off", nil)
	require.NoError(t, err)
	_, err = rOff.CancelInstance(t.Context(), retrySagaDef(), "retry-rt-off")
	require.NoError(t, err)

	require.Equal(t, []string{"doA", "doB", "undoB", "undoA"}, recOff.snapshot(),
		"control: with the option unset a failed compensation is SKIPPED and the walk advances to undoA")
	require.False(t, off.Armed,
		"control: with the option unset the walk must schedule no timer whatsoever")

	// With the option set, undoB's failure arms a TimerCompensationRetry through
	// the normal ScheduleTimer path — the only observable proof the policy reached
	// engine.StepOptions — and the walk stays on the failed record.
	clkOn := clockwork.NewFakeClock()
	recOn := &callRecorder{}
	on := &runtimetest.RecordingScheduler{Clock: clkOn}
	rOn := runtimetest.MustProcessDriver(t, retrySagaCatalog(recOn), runtimetest.MustMemStore(t),
		runtime.WithClock(clkOn), runtime.WithScheduler(on),
		// ⚠ InitialInterval is kept well under 100s deliberately. RetryPolicy.Normalize
		// fills an unset MaxInterval from DefaultRetryPolicy (100s) and Backoff caps
		// against it, so a policy of {InitialInterval: 2m} arms at now+100s, not
		// now+2m — measured on this branch before the fixture was corrected.
		runtime.WithCompensationRetryPolicy(model.RetryPolicy{
			MaxAttempts:     5,
			InitialInterval: 30 * time.Second,
		}))

	st, err := rOn.Drive(t.Context(), retrySagaDef(), "retry-rt-on", nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, st.Status, "control: parked awaiting the message")

	_, err = rOn.CancelInstance(t.Context(), retrySagaDef(), "retry-rt-on")
	require.NoError(t, err)

	assert.Equal(t, []string{"doA", "doB", "undoB"}, recOn.snapshot(),
		"WithCompensationRetryPolicy must reach StepOptions.CompensationRetryPolicy, so the "+
			"walk holds on the FAILED record and never advances to undoA")
	assert.True(t, on.Armed, "and the failure arms a compensation-retry backoff")
	assert.Equal(t, clkOn.Now().Add(30*time.Second), on.FireAt,
		"the backoff is the policy's InitialInterval, undiminished — the compensation path "+
			"deliberately does not jitter")
}

// TestCompensationRetryPolicyIsCopiedFromTheCaller pins the copy-on-call
// semantics WithDefaultRetryPolicy documents and shares: the option takes a
// model.RetryPolicy BY VALUE, so a caller mutating its own variable afterwards
// cannot reach into the constructed driver.
//
// What makes it fail today: WithCompensationRetryPolicy does not exist. Were it
// later changed to accept a *model.RetryPolicy (or to store the caller's pointer),
// the post-construction mutation below would change the armed backoff from 30s to
// 45s and the assertion would fail. Both values sit under the 100s MaxInterval cap
// Normalize applies, so the cap cannot mask the difference.
func TestCompensationRetryPolicyIsCopiedFromTheCaller(t *testing.T) {
	t.Parallel()

	clk := clockwork.NewFakeClock()
	rec := &callRecorder{}
	sch := &runtimetest.RecordingScheduler{Clock: clk}

	pol := model.RetryPolicy{MaxAttempts: 5, InitialInterval: 30 * time.Second}
	driver := runtimetest.MustProcessDriver(t, retrySagaCatalog(rec), runtimetest.MustMemStore(t),
		runtime.WithClock(clk), runtime.WithScheduler(sch),
		runtime.WithCompensationRetryPolicy(pol))
	pol.InitialInterval = 45 * time.Second

	_, err := driver.Drive(t.Context(), retrySagaDef(), "retry-rt-copy", nil)
	require.NoError(t, err)
	_, err = driver.CancelInstance(t.Context(), retrySagaDef(), "retry-rt-copy")
	require.NoError(t, err)

	require.True(t, sch.Armed, "control: the fixture must actually arm a backoff")
	assert.Equal(t, clk.Now().Add(30*time.Second), sch.FireAt,
		"the policy is copied at option time; a later mutation by the caller must not reach the driver")
}

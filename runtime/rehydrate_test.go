package runtime_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	clockwork "github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/processtest"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/internal/runtimetest"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// timerStartOnlyDef returns a minimal process whose start event carries a timer
// trigger (ADR-0121 timer-start): start(timer) → end. Used to exercise
// RehydrateStartTimers, whose fire callback creates one instance per fire.
func timerStartOnlyDef(defID string, trig schedule.TriggerSpec) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      defID,
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start", event.WithStartTimer(trig)),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "end"},
		},
	}
}

// timerStartOnlyDefVersion is timerStartOnlyDef with an explicit Version, so a
// test can register two distinct versions of the same def id — each keeping the
// SAME start node id ("start") — to exercise cross-version timer-start keying.
func timerStartOnlyDefVersion(defID string, version int, trig schedule.TriggerSpec) *model.ProcessDefinition {
	d := timerStartOnlyDef(defID, trig)
	d.Version = version
	return d
}

// fixedIDGenerator is a deterministic idgen.Generator test double that always
// returns the same id, so a test can assert on the exact instance id a
// timer-start fire will create.
type fixedIDGenerator struct{ id string }

func (g fixedIDGenerator) NewID() (string, error) { return g.id, nil }

// seqIDGenerator is an idgen.Generator test double that mints monotonically
// numbered ids ("prefix-1", "prefix-2", …), so a test in which several
// timer-start fires each create a fresh instance gets a distinct id per fire
// (a fixed id would collide on Store.Create's ErrInstanceExists after the first).
type seqIDGenerator struct {
	prefix string
	n      atomic.Int64
}

func (g *seqIDGenerator) NewID() (string, error) {
	return fmt.Sprintf("%s-%d", g.prefix, g.n.Add(1)), nil
}

func TestRehydrateTimersResumesAfterRestart(t *testing.T) {
	startAt := time.Date(2026, 6, 22, 13, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(startAt)
	mts := kernel.NewMemTimerStore()
	store := runtimetest.MustMemStore(t, kernel.WithTimers(mts))
	def := runtimetest.TimerIntermediateDef()
	reg := kernel.NewMapDefinitionRegistry(def) // auto-indexed by both "DefID" and "DefID:1"

	cat := action.NewCatalog(map[string]action.Action{
		"greet": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return map[string]any{"greeted": true}, nil
		}),
	})

	// Original process: arm the timer, then it "crashes" — discard runner + scheduler.
	{
		sched := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(fc))
		driver := runtimetest.MustRunner(t, cat, store,
			runtime.WithClock(fc),
			runtime.WithScheduler(sched), runtime.WithTimerStore(mts), runtime.WithDefinitions(reg))
		_, err := driver.Drive(t.Context(), def, "rh-1", nil)
		require.NoError(t, err)
	}

	// New process: fresh runner + fresh scheduler, same store + timer store.
	sched2 := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(fc))
	r2 := runtimetest.MustRunner(t, cat, store,
		runtime.WithClock(fc),
		runtime.WithScheduler(sched2), runtime.WithTimerStore(mts), runtime.WithDefinitions(reg))

	require.NoError(t, r2.RehydrateTimers(t.Context()))

	// Advance + tick the NEW scheduler: the rehydrated timer fires and resumes.
	fc.Advance(time.Hour + time.Second)
	require.NoError(t, sched2.Tick(t.Context()))

	final, _, err := store.Load(t.Context(), "rh-1")
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status, "rehydrated timer must resume the instance")
}

func TestRehydrateTimersRequiresWiring(t *testing.T) {
	store := runtimetest.MustMemStore(t)
	driver := runtimetest.MustRunner(t, action.NewCatalog(nil), store, runtime.WithClock(clockwork.NewFakeClock()))
	err := driver.RehydrateTimers(t.Context())
	require.Error(t, err, "RehydrateTimers without scheduler/timer-store/registry must error")
}

func TestRehydrateStartTimersFiresCreatesInstance(t *testing.T) {
	startAt := time.Date(2026, 6, 22, 13, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(startAt)
	reg := kernel.NewMemDefinitionRegistry()
	require.NoError(t, reg.Register(timerStartOnlyDef("cron", schedule.AfterDuration(time.Hour))))
	store := runtimetest.MustMemStore(t)
	sched := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(fc))
	driver := runtimetest.MustRunner(t, action.NewCatalog(nil), store,
		runtime.WithClock(fc),
		runtime.WithScheduler(sched), runtime.WithDefinitions(reg),
		runtime.WithIDGenerator(fixedIDGenerator{id: "cron-instance-1"}))

	require.NoError(t, driver.RehydrateStartTimers(t.Context()))

	// Advance + tick the scheduler: the armed timer-start fires and creates a
	// brand-new instance (no pre-existing instance to resume, unlike a
	// TimerFired delivered to an already-running one).
	fc.Advance(time.Hour + time.Minute)
	require.NoError(t, sched.Tick(t.Context()))

	final, _, err := store.Load(t.Context(), "cron-instance-1")
	require.NoError(t, err, "timer-start fire must have created the instance")
	assert.Equal(t, engine.StatusCompleted, final.Status, "start->end with no other nodes must complete immediately")
}

func TestRehydrateStartTimersMultiVersionNoCollision(t *testing.T) {
	startAt := time.Date(2026, 6, 22, 13, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(startAt)

	// Two VERSIONS of the same def id, both with a timer-start on the SAME node id
	// ("start"). Their scheduler timer ids must differ by version, or the second
	// Schedule silently replaces the first's callback and one version never fires.
	reg := kernel.NewMemDefinitionRegistry()
	require.NoError(t, reg.Register(timerStartOnlyDefVersion("cron", 1, schedule.AfterDuration(time.Hour))))
	require.NoError(t, reg.Register(timerStartOnlyDefVersion("cron", 2, schedule.AfterDuration(time.Hour))))

	store := runtimetest.MustMemStore(t)
	sched := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(fc))
	driver := runtimetest.MustRunner(t, action.NewCatalog(nil), store,
		runtime.WithClock(fc),
		runtime.WithScheduler(sched), runtime.WithDefinitions(reg),
		runtime.WithIDGenerator(&seqIDGenerator{prefix: "cron-inst"}))

	require.NoError(t, driver.RehydrateStartTimers(t.Context()))

	fc.Advance(time.Hour + time.Minute)
	require.NoError(t, sched.Tick(t.Context()))

	// Both versions' timer-starts must have fired → two distinct instances created.
	inst1, _, err1 := store.Load(t.Context(), "cron-inst-1")
	require.NoError(t, err1)
	inst2, _, err2 := store.Load(t.Context(), "cron-inst-2")
	require.NoError(t, err2, "both def versions' timer-starts must fire; a shared timer id drops one")
	assert.Equal(t, engine.StatusCompleted, inst1.Status)
	assert.Equal(t, engine.StatusCompleted, inst2.Status)

	// The two instances must be seeded from DIFFERENT def versions (1 and 2), not
	// the same version fired twice.
	gotVersions := map[int]struct{}{inst1.DefVersion: {}, inst2.DefVersion: {}}
	_, has1 := gotVersions[1]
	_, has2 := gotVersions[2]
	assert.True(t, has1 && has2, "expected one instance per def version (1 and 2), got versions %v", gotVersions)
}

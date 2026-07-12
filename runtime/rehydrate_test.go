package runtime_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	clockwork "github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/processtest"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/internal/runtimetest"
	"github.com/kartaladev/wrkflw/runtime/kernel"
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

// lookupOnlyReg is a DefinitionRegistry that does NOT implement
// kernel.DefinitionLister, so event-based START enumeration is unavailable.
type lookupOnlyReg struct{}

func (lookupOnlyReg) Lookup(context.Context, model.Qualifier) (*model.ProcessDefinition, error) {
	return nil, kernel.ErrDefinitionNotFound
}

// TestRehydrateStartTimersWarnsWhenRegistryNotEnumerable verifies that when the
// registry cannot enumerate definitions (no DefinitionLister), RehydrateStartTimers
// arms nothing but logs a single WARN explaining event-based start is disabled.
func TestRehydrateStartTimersWarnsWhenRegistryNotEnumerable(t *testing.T) {
	fc := clockwork.NewFakeClock()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	sched := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(fc))

	driver := runtimetest.MustRunner(t, action.NewCatalog(nil), runtimetest.MustMemStore(t),
		runtime.WithClock(fc),
		runtime.WithScheduler(sched),
		runtime.WithDefinitions(lookupOnlyReg{}),
		runtime.WithLogger(logger))

	require.NoError(t, driver.RehydrateStartTimers(t.Context()))

	var sawWarn bool
	for _, line := range splitNonEmpty(buf.Bytes()) {
		var entry struct {
			Level string `json:"level"`
			Msg   string `json:"msg"`
		}
		require.NoError(t, json.Unmarshal(line, &entry))
		if entry.Level == "WARN" && strings.Contains(entry.Msg, "event-based start") {
			sawWarn = true
		}
	}
	assert.True(t, sawWarn, "expected a WARN that event-based start is disabled without an enumerable registry")
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

func TestRehydrateStartTimersLatestVersionOnly(t *testing.T) {
	startAt := time.Date(2026, 6, 22, 13, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(startAt)

	// Two VERSIONS of the same def id, both with a timer-start on the SAME node id
	// ("start"). A MemDefinitionRegistry retains both versions so in-flight
	// instances can resume, but only the LATEST (v2) holds an active start
	// subscription (ADR-0121 Camunda semantics): a version bump replaces, it does
	// not duplicate. Exactly ONE instance must be created per occurrence, from v2.
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

	// Only the latest version's timer-start fires → exactly ONE instance created.
	inst1, _, err1 := store.Load(t.Context(), "cron-inst-1")
	require.NoError(t, err1, "the latest version's timer-start must fire and create one instance")
	assert.Equal(t, engine.StatusCompleted, inst1.Status)
	assert.Equal(t, 2, inst1.DefVersion, "the sole instance must be seeded from the LATEST version (v2)")

	// The superseded version (v1) must NOT start a new instance — no second id.
	_, _, err2 := store.Load(t.Context(), "cron-inst-2")
	require.Error(t, err2, "superseded version must not start a new instance")
}

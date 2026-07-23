package runtime_test

// Task 11 (ADR-0134) hot-path tests: durable timer writes ride the runtime
// jobStore INSIDE the state-commit transaction (direct-Save model); the
// scheduler is touched post-commit only.
//
// These are standalone TestXxx functions (not one table): each scenario has a
// structurally different setup — different fault injection points, different
// scheduler doubles (fire-on-activate, gated), and different orchestration
// (goroutine interleave) — so the shared-call-shape assumption of the
// project's table-test rule does not hold here.
//
// SQLite (dbtest.RunTestSQLite, in-process, no Docker) backs most scenarios
// because rollback-parity guarantees are SQL-only (Mem RunInTx is
// sequencing-only). TestTimerTxFlowSameTxAtomicityPostgres is the one
// Postgres variant; like every RunTestDatabase-based test it requires a
// running Docker daemon.

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	clockwork "github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/kartaladev/wrkflw/internal/persistence/dialect"
	"github.com/kartaladev/wrkflw/internal/persistence/store"
	"github.com/kartaladev/wrkflw/persistence"
	"github.com/kartaladev/wrkflw/processtest"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/internal/runtimetest"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/scheduler"
)

// errInjected is the sentinel returned by faultTimerWriter's injected
// failures; tests assert on it to prove the observed error is theirs.
var errInjected = errors.New("timer_txflow_test: injected timer-write failure")

// faultTimerWriter wraps a real *store.TimerStore as both kernel.TimerStore
// and kernel.TimerWriter, with switchable fault injection and an UpsertJob
// call counter. It is handed to runtime.WithTimerStore, so the driver
// type-asserts the TimerWriter capability off it and every durable timer
// write flows through here — the direct-Save path is the ONLY write path
// (the fused AppliedStep.TimerArms/TimerCancels path was retired by Task 12,
// ADR-0134), so the upsert counter counts every durable timer write.
type faultTimerWriter struct {
	inner *store.TimerStore

	mu              sync.Mutex
	upserts         int
	failUpsert      bool
	failAfterDelete bool
}

var (
	_ kernel.TimerStore  = (*faultTimerWriter)(nil)
	_ kernel.TimerWriter = (*faultTimerWriter)(nil)
)

func (f *faultTimerWriter) ListArmed(ctx context.Context) ([]kernel.ArmedTimer, error) {
	return f.inner.ListArmed(ctx)
}

func (f *faultTimerWriter) UpsertJob(ctx context.Context, spec kernel.JobSpec) error {
	f.mu.Lock()
	f.upserts++
	fail := f.failUpsert
	f.mu.Unlock()
	if fail {
		return errInjected
	}
	return f.inner.UpsertJob(ctx, spec)
}

// DeleteJob performs the real delete FIRST and only then injects the failure,
// modeling a fault that strikes after the row-delete already executed on the
// transaction — the rolled-back-cancel scenario.
func (f *faultTimerWriter) DeleteJob(ctx context.Context, instanceID, timerID string) error {
	if err := f.inner.DeleteJob(ctx, instanceID, timerID); err != nil {
		return err
	}
	f.mu.Lock()
	fail := f.failAfterDelete
	f.mu.Unlock()
	if fail {
		return errInjected
	}
	return nil
}

func (f *faultTimerWriter) DeleteJobByTimerID(ctx context.Context, timerID string) error {
	return f.inner.DeleteJobByTimerID(ctx, timerID)
}

func (f *faultTimerWriter) setFailUpsert(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failUpsert = v
}

func (f *faultTimerWriter) setFailAfterDelete(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failAfterDelete = v
}

func (f *faultTimerWriter) upsertCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.upserts
}

// twoTimerDef: start → wait1 (1h one-shot) → wait2 (2h one-shot) → end. The
// second catch arms its timer on a COMMIT (non-create) step, which is the
// same-tx-atomicity scenario's injection point.
func twoTimerDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "txflow-two-timers",
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			event.NewIntermediateCatch("wait1", event.WithCatchTimer(schedule.AfterDuration(time.Hour))),
			event.NewIntermediateCatch("wait2", event.WithCatchTimer(schedule.AfterDuration(2*time.Hour))),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "wait1"},
			{ID: "f2", Source: "wait1", Target: "wait2"},
			{ID: "f3", Source: "wait2", Target: "end"},
		},
	}
}

// immediateTimerDef: start → wait0 (AfterDuration(0), i.e. past-due the moment
// it is armed) → end.
func immediateTimerDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "txflow-immediate-timer",
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			event.NewIntermediateCatch("wait0", event.WithCatchTimer(schedule.AfterDuration(0))),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "wait0"},
			{ID: "f2", Source: "wait0", Target: "end"},
		},
	}
}

// newTxFlowStores builds the SQL InstanceStore + fault-wrapped TimerStore over
// one connection/dialect pair.
func newTxFlowStores(t *testing.T, conn any, dlct dialect.Dialect) (*store.Store, *faultTimerWriter) {
	t.Helper()
	sqlStore, err := store.New(conn, dlct)
	require.NoError(t, err)
	timerStore, err := store.NewTimerStore(conn, dlct)
	require.NoError(t, err)
	return sqlStore, &faultTimerWriter{inner: timerStore}
}

// assertSameTxAtomicity is the dialect-neutral body shared by the SQLite and
// Postgres same-tx-atomicity tests (hot-path test 1): a step that consumes
// wait1's timer and arms wait2's timer hits an injected Save failure → the
// WHOLE unit rolls back: state not advanced, wait1's row restored, no wait2
// row, and no in-memory arm for wait2.
func assertSameTxAtomicity(t *testing.T, conn any, dlct dialect.Dialect) {
	t.Helper()
	ctx := t.Context()
	startAt := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(startAt)

	sqlStore, fw := newTxFlowStores(t, conn, dlct)
	sched := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(fc))
	driver := runtimetest.MustProcessDriver(t, nil, sqlStore,
		runtime.WithClock(fc),
		runtime.WithScheduler(sched),
		runtime.WithTimerStore(fw))

	def := twoTimerDef()
	const instanceID = "txf-atomic-1"

	parked, err := driver.Drive(ctx, def, instanceID, nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, parked.Status)

	armed, err := fw.ListArmed(ctx)
	require.NoError(t, err)
	require.Len(t, armed, 1, "wait1's timer row must be durably armed")
	require.Equal(t, instanceID+"-tm1", armed[0].TimerID)

	_, preVersion, err := sqlStore.Load(ctx, instanceID)
	require.NoError(t, err)

	// Inject: the step that fires tm1 (consume/delete) and arms tm2 (Save)
	// fails on the Save. Everything in that transaction must roll back.
	fw.setFailUpsert(true)
	fc.Advance(time.Hour + time.Second)
	_, err = driver.ApplyTrigger(ctx, def, instanceID,
		engine.NewTimerFired(fc.Now(), instanceID+"-tm1"))
	require.Error(t, err, "an injected in-tx Save failure must surface as a commit error")
	require.ErrorIs(t, err, errInjected)

	// State NOT advanced: same version, still Running.
	st, postVersion, err := sqlStore.Load(ctx, instanceID)
	require.NoError(t, err)
	assert.Equal(t, preVersion, postVersion, "a rolled-back step must not advance the instance version")
	assert.Equal(t, engine.StatusRunning, st.Status)

	// Durable rows: wait1's consumption-delete rolled back, wait2 never armed.
	armed, err = fw.ListArmed(ctx)
	require.NoError(t, err)
	require.Len(t, armed, 1, "rollback must restore exactly the pre-step timer rows")
	assert.Equal(t, instanceID+"-tm1", armed[0].TimerID, "wait1's row must survive the rollback")

	// No in-memory arm for wait2: Activate must not have run post-error.
	_, err = sched.Scheduled(ctx, instanceID+"-tm2")
	require.ErrorIs(t, err, scheduler.ErrJobNotFound,
		"a rolled-back arm must never reach the scheduler")
}

// TestTimerTxFlowSameTxAtomicity is hot-path test 1 on SQLite.
func TestTimerTxFlowSameTxAtomicity(t *testing.T) {
	t.Parallel()
	db := dbtest.RunTestSQLite(t)
	assertSameTxAtomicity(t, db, dialect.NewSQLite())
}

// TestTimerTxFlowSameTxAtomicityPostgres is the Postgres variant of hot-path
// test 1 (Docker-gated via testcontainers, like every RunTestDatabase test).
func TestTimerTxFlowSameTxAtomicityPostgres(t *testing.T) {
	t.Parallel()
	pool := dbtest.RunTestDatabase(t)
	require.NoError(t, persistence.Migrate(t.Context(), pool), "migrate postgres")
	assertSameTxAtomicity(t, pool, dialect.NewPostgres())
}

// TestTimerTxFlowCreateRollback is hot-path test 2 — the BLOCKER-1 regression
// test: StartInstance whose first step arms an immediate one-shot hits an
// injected in-tx failure → NO instance row, NO timer row, and NO fire EVER
// (the timer must not exist in any form that could later fire).
func TestTimerTxFlowCreateRollback(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	startAt := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(startAt)

	db := dbtest.RunTestSQLite(t)
	sqlStore, fw := newTxFlowStores(t, db, dialect.NewSQLite())
	sched := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(fc))
	driver := runtimetest.MustProcessDriver(t, nil, sqlStore,
		runtime.WithClock(fc),
		runtime.WithScheduler(sched),
		runtime.WithTimerStore(fw))

	def := immediateTimerDef()
	const instanceID = "txf-create-1"

	fw.setFailUpsert(true)
	_, err := driver.Drive(ctx, def, instanceID, nil)
	require.Error(t, err, "an injected in-tx failure on the Create step must fail Drive")
	require.ErrorIs(t, err, errInjected)

	// No instance row.
	_, _, err = sqlStore.Load(ctx, instanceID)
	require.ErrorIs(t, err, kernel.ErrInstanceNotFound, "the Create must have rolled back")

	// No timer row.
	armed, err := fw.ListArmed(ctx)
	require.NoError(t, err)
	assert.Empty(t, armed, "no timer row may survive the rolled-back Create")

	// NO fire ever: nothing armed, and advancing the clock fires nothing.
	_, pending := sched.NextFireAt()
	assert.False(t, pending, "a rolled-back Create must leave nothing armed")
	fc.Advance(time.Minute)
	require.NoError(t, sched.Tick(ctx))
	_, _, err = sqlStore.Load(ctx, instanceID)
	require.ErrorIs(t, err, kernel.ErrInstanceNotFound,
		"no late fire may resurrect the rolled-back instance")
}

// fireOnActivateScheduler wraps MemScheduler and — like gocron with a past-due
// one-shot — fires a job synchronously the moment it is activated when its
// next run is already due. It makes the activation-ordering property
// deterministic: if Activate ran before the commit, the fire's Load would miss
// the instance row.
type fireOnActivateScheduler struct {
	*processtest.MemScheduler
	clk clockwork.Clock
}

func (s *fireOnActivateScheduler) Activate(ctx context.Context, j scheduler.ScheduledJob) error {
	if err := s.MemScheduler.Activate(ctx, j); err != nil {
		return err
	}
	if !j.NextRun().After(s.clk.Now()) {
		// Past-due at arm time: fire immediately, then consume the one-shot,
		// mimicking the production scheduler's immediate dispatch.
		_ = j.Action()(context.Background(), j.Data())
		_ = s.Deactivate(ctx, j.ID())
	}
	return nil
}

// TestTimerTxFlowActivationOrdering is hot-path test 3: a past-due
// AfterDuration(0) timer armed by a step fires only AFTER the commit — the
// fire's Load sees the committed instance and drives it to completion. It also
// pins the direct-Save contract: the durable arm goes through the TimerWriter
// capability exactly once.
func TestTimerTxFlowActivationOrdering(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	startAt := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(startAt)

	db := dbtest.RunTestSQLite(t)
	sqlStore, fw := newTxFlowStores(t, db, dialect.NewSQLite())
	mem := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(fc))
	sched := &fireOnActivateScheduler{MemScheduler: mem, clk: fc}
	driver := runtimetest.MustProcessDriver(t, nil, sqlStore,
		runtime.WithClock(fc),
		runtime.WithScheduler(sched),
		runtime.WithTimerStore(fw))

	def := immediateTimerDef()
	const instanceID = "txf-order-1"

	_, err := driver.Drive(ctx, def, instanceID, nil)
	require.NoError(t, err)

	// The synchronous past-due fire ran during Drive's post-commit activation:
	// its Load MUST have found the committed instance and completed it. Had the
	// arm fired before the commit, the fire would have been dropped on
	// ErrInstanceNotFound and the instance would still be Running.
	final, _, err := sqlStore.Load(ctx, instanceID)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status,
		"the past-due fire must land AFTER the commit and complete the instance")

	// Direct-Save contract: the arm was persisted through the TimerWriter
	// capability (in-tx), exactly once.
	assert.Equal(t, 1, fw.upsertCount(),
		"the durable arm must ride TimerWriter.UpsertJob exactly once")
}

// TestTimerTxFlowRolledBackCancel is hot-path test 4: cancelling an armed
// recurring timer hits an injected failure AFTER the row-delete already
// executed in the transaction → the whole step rolls back and the in-memory
// arm is untouched, so the timer STILL fires on the next advance.
func TestTimerTxFlowRolledBackCancel(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	startAt := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(startAt)
	const waitEvery = 15 * time.Minute

	var reminderRuns atomic.Int64
	cat := action.NewCatalog(map[string]action.Action{
		"ping": action.ActionFunc(func(context.Context, map[string]any) (map[string]any, error) {
			reminderRuns.Add(1)
			return nil, nil
		}),
	})

	db := dbtest.RunTestSQLite(t)
	sqlStore, fw := newTxFlowStores(t, db, dialect.NewSQLite())
	sched := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(fc))

	manager := authz.Actor{ID: "alice", Roles: []string{"manager"}}
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{"manager": {manager}})
	driver := runtimetest.MustProcessDriver(t, cat, sqlStore,
		runtime.WithClock(fc),
		runtime.WithScheduler(sched),
		runtime.WithTimerStore(fw),
		runtime.WithHumanTasks(resolver, humantask.NewMemTaskStore(), authz.RoleAuthorizer{}))

	def := runtimetest.ApprovalWithReminderDef(waitEvery, "ping")
	const instanceID = "txf-cancel-1"

	parked, err := driver.Drive(ctx, def, instanceID, nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, parked.Status)
	require.Len(t, parked.Tasks, 1)
	taskToken := parked.Tasks[0].TaskToken

	armed, err := fw.ListArmed(ctx)
	require.NoError(t, err)
	require.Len(t, armed, 1, "the recurring reminder must be durably armed")

	// Completing the task emits CancelTimer(reminder). Inject a failure AFTER
	// the delete executed on the tx → the whole step must roll back.
	fw.setFailAfterDelete(true)
	_, err = driver.ApplyTrigger(ctx, def, instanceID,
		engine.NewHumanCompleted(fc.Now(), taskToken, nil, manager))
	require.Error(t, err, "the injected post-delete failure must fail the step")
	require.ErrorIs(t, err, errInjected)

	// Rollback restored the durable row.
	armed, err = fw.ListArmed(ctx)
	require.NoError(t, err)
	require.Len(t, armed, 1, "the rolled-back cancel must restore the timer row")

	// The in-memory arm is untouched: the reminder still fires on advance.
	fc.Advance(waitEvery + time.Second)
	require.NoError(t, sched.Tick(ctx))
	assert.Equal(t, int64(1), reminderRuns.Load(),
		"a rolled-back cancel must leave the in-memory arm live — the reminder still fires")
}

// TestTimerTxFlowSingleSavePerArm is hot-path test 5 — the double-arm
// regression guard: with the perform-path timer cases deleted, one armed timer
// makes EXACTLY one TimerWriter.UpsertJob call per transaction (the fused
// AppliedStep.TimerArms/TimerCancels path was retired by Task 12, ADR-0134,
// so the direct-Save path is the only writer left to count).
func TestTimerTxFlowSingleSavePerArm(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	startAt := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(startAt)

	db := dbtest.RunTestSQLite(t)
	sqlStore, fw := newTxFlowStores(t, db, dialect.NewSQLite())
	sched := processtest.NewMemScheduler(processtest.WithMemSchedulerClock(fc))
	driver := runtimetest.MustProcessDriver(t, nil, sqlStore,
		runtime.WithClock(fc),
		runtime.WithScheduler(sched),
		runtime.WithTimerStore(fw))

	def := twoTimerDef()
	const instanceID = "txf-single-1"

	_, err := driver.Drive(ctx, def, instanceID, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, fw.upsertCount(), "arming wait1's timer must Save exactly once")

	// Fire wait1 → the step consumes tm1 and arms tm2: exactly one more Save.
	fc.Advance(time.Hour + time.Second)
	require.NoError(t, sched.Tick(ctx))
	assert.Equal(t, 2, fw.upsertCount(), "the wait2 arm must add exactly one more Save")

	// Fire wait2 (a 2h one-shot) → completion arms nothing: no further Save.
	fc.Advance(2*time.Hour + time.Second)
	require.NoError(t, sched.Tick(ctx))
	final, _, err := sqlStore.Load(ctx, instanceID)
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompleted, final.Status)
	assert.Equal(t, 2, fw.upsertCount(), "completion must not Save any timer")
}

// failingFlipScheduler errors on every post-commit flip (Activate and
// Deactivate), modeling a scheduler closed during shutdown drain.
type failingFlipScheduler struct {
	*processtest.MemScheduler
}

func (s *failingFlipScheduler) Activate(context.Context, scheduler.ScheduledJob) error {
	return errInjected
}

func (s *failingFlipScheduler) Deactivate(context.Context, string) error {
	return errInjected
}

// TestTimerTxFlowPostCommitFlipFailureIsBenign pins the post-commit contract:
// a failing Activate or Deactivate (e.g. the scheduler is closed during the
// ADR-0133 drain window) is WARN-logged and NEVER fails the committed step —
// the durable rows are the truth and rehydration self-heals the in-memory arm.
func TestTimerTxFlowPostCommitFlipFailureIsBenign(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	startAt := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(startAt)

	db := dbtest.RunTestSQLite(t)
	sqlStore, fw := newTxFlowStores(t, db, dialect.NewSQLite())
	sched := &failingFlipScheduler{MemScheduler: processtest.NewMemScheduler(processtest.WithMemSchedulerClock(fc))}
	driver := runtimetest.MustProcessDriver(t, nil, sqlStore,
		runtime.WithClock(fc),
		runtime.WithScheduler(sched),
		runtime.WithTimerStore(fw))

	def := twoTimerDef()
	const instanceID = "txf-benign-1"

	// Arm flip fails → Drive still succeeds and the durable arm is committed.
	parked, err := driver.Drive(ctx, def, instanceID, nil)
	require.NoError(t, err, "a failing post-commit Activate must never fail the committed step")
	require.Equal(t, engine.StatusRunning, parked.Status)
	armed, err := fw.ListArmed(ctx)
	require.NoError(t, err)
	require.Len(t, armed, 1, "the durable arm must survive the failed in-memory flip")

	// Cancel flip (fired-timer consumption Deactivate) fails → the step still
	// succeeds and the durable delete is committed.
	fc.Advance(time.Hour + time.Second)
	_, err = driver.ApplyTrigger(ctx, def, instanceID,
		engine.NewTimerFired(fc.Now(), instanceID+"-tm1"))
	require.NoError(t, err, "a failing post-commit Deactivate must never fail the committed step")
	armed, err = fw.ListArmed(ctx)
	require.NoError(t, err)
	require.Len(t, armed, 1, "tm1's consumption delete committed; wait2's arm committed")
	assert.Equal(t, instanceID+"-tm2", armed[0].TimerID)
}

// gateScheduler wraps MemScheduler and blocks the FIRST Activate of gateID
// until release is closed, signalling arrival via reached. It deterministically
// forces the post-commit flip order Deactivate(T)-before-Activate(T) across
// two steps of one instance.
type gateScheduler struct {
	*processtest.MemScheduler
	gateID  string
	reached chan struct{}
	release chan struct{}
	once    sync.Once
}

func (g *gateScheduler) Activate(ctx context.Context, j scheduler.ScheduledJob) error {
	if j.ID() == g.gateID {
		g.once.Do(func() { close(g.reached) })
		<-g.release
	}
	return g.MemScheduler.Activate(ctx, j)
}

// TestTimerTxFlowArmCancelInterleave is hot-path test 6 (audit v2-A4): step A
// commits an arm of the recurring reminder T but is gated BEFORE its
// post-commit Activate(T); step B (task completion) commits the cancel of T
// and runs its post-commit Deactivate(T) first; then A's delayed Activate(T)
// lands, creating a phantom in-memory arm with no durable row. The phantom
// must fire AT MOST once as a stale no-op (terminal instance ⇒ engine no-op,
// reminder action never runs) and then disappear — its TimerFired consumption
// deactivates it. Instance state is unaffected throughout.
//
// Design choice (documented per the brief): rather than racing two goroutines
// through the store, the interleave is made deterministic with channel gates
// around the post-commit flips — step A runs in a goroutine that parks inside
// Activate(T) after its commit is durable, step B runs to completion on the
// main goroutine, then A is released. Everything else (stores, scheduler,
// clock) is real.
func TestTimerTxFlowArmCancelInterleave(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	startAt := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	fc := clockwork.NewFakeClockAt(startAt)
	const waitEvery = 15 * time.Minute

	var reminderRuns atomic.Int64
	cat := action.NewCatalog(map[string]action.Action{
		"ping": action.ActionFunc(func(context.Context, map[string]any) (map[string]any, error) {
			reminderRuns.Add(1)
			return nil, nil
		}),
	})

	db := dbtest.RunTestSQLite(t)
	sqlStore, fw := newTxFlowStores(t, db, dialect.NewSQLite())

	const instanceID = "txf-inter-1"
	timerID := instanceID + "-tm1"
	sched := &gateScheduler{
		MemScheduler: processtest.NewMemScheduler(processtest.WithMemSchedulerClock(fc)),
		gateID:       timerID,
		reached:      make(chan struct{}),
		release:      make(chan struct{}),
	}

	manager := authz.Actor{ID: "alice", Roles: []string{"manager"}}
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{"manager": {manager}})
	driver := runtimetest.MustProcessDriver(t, cat, sqlStore,
		runtime.WithClock(fc),
		runtime.WithScheduler(sched),
		runtime.WithTimerStore(fw),
		runtime.WithHumanTasks(resolver, humantask.NewMemTaskStore(), authz.RoleAuthorizer{}))

	def := runtimetest.ApprovalWithReminderDef(waitEvery, "ping")

	// Step A: Drive commits the create (arming T durably in-tx) and parks
	// inside its post-commit Activate(T).
	driveErr := make(chan error, 1)
	go func() {
		_, err := driver.Drive(context.WithoutCancel(ctx), def, instanceID, nil)
		driveErr <- err
	}()
	<-sched.reached

	// A's commit is durable: the timer row and the parked task exist.
	armed, err := fw.ListArmed(ctx)
	require.NoError(t, err)
	require.Len(t, armed, 1, "step A's commit (arm of T) must be durable before its Activate")
	st, _, err := sqlStore.Load(ctx, instanceID)
	require.NoError(t, err)
	require.Len(t, st.Tasks, 1)
	taskToken := st.Tasks[0].TaskToken

	// Step B: completing the task cancels T — durable delete in-tx, then the
	// post-commit Deactivate(T) (a no-op: T is not armed yet).
	final, err := driver.ApplyTrigger(ctx, def, instanceID,
		engine.NewHumanCompleted(fc.Now(), taskToken, nil, manager))
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompleted, final.Status)
	armed, err = fw.ListArmed(ctx)
	require.NoError(t, err)
	require.Empty(t, armed, "step B's cancel must remove T's durable row")

	// Release A: its delayed Activate(T) lands AFTER B's Deactivate(T) — the
	// phantom in-memory arm.
	close(sched.release)
	require.NoError(t, <-driveErr)
	_, pending := sched.Pending(timerID)
	require.True(t, pending, "the delayed Activate must leave a phantom in-memory arm")

	// The phantom fires at most once, as a stale no-op, then disappears.
	fc.Advance(waitEvery + time.Second)
	require.NoError(t, sched.Tick(ctx))
	assert.Equal(t, int64(0), reminderRuns.Load(),
		"a stale fire against a terminal instance must not run the reminder action")
	stAfter, _, err := sqlStore.Load(ctx, instanceID)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, stAfter.Status, "instance state must be unaffected")
	_, pending = sched.Pending(timerID)
	assert.False(t, pending,
		"the stale fire's TimerFired consumption must deactivate the phantom arm")

	// And it never fires again.
	fc.Advance(waitEvery + time.Second)
	require.NoError(t, sched.Tick(ctx))
	assert.Equal(t, int64(0), reminderRuns.Load(), "the phantom must not fire twice")
}

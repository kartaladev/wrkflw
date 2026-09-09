// Package runtime_test — crash-recovery coverage for the window between a
// step's commit and perform.
//
// deliverLoop commits the step and only THEN performs its commands. A process
// that dies inside that window leaves a durable snapshot whose commands were
// never performed: an instance parked on a service task whose action never ran,
// or on a user task whose task row was never written. Issue #110.
//
// Three artifacts live here, and they are deliberately distinct:
//
//  1. TestCommittedStepWithUnperformedCommandsStaysParkedWithoutRecovery — the
//     REPRODUCTION. It pins the defect: a restart that cannot sweep leaves the
//     instance exactly where the fault left it. It passed before the sweep
//     existed and still passes after, because its store cannot be enumerated.
//  2. TestCommittedStepWithUnperformedCommandsIsRecoveredOnRestart — the RED. It
//     fails without the sweep and goes green with it.
//  3. The fault-injection guard, asserted inside BOTH: the spy must read exactly
//     zero performed commands before the restart, and the injected abort must
//     have fired exactly once. Without it, a green in (2) is indistinguishable
//     from a fault that silently never fired.
package runtime_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/internal/runtimetest"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// errAbortedAfterCommit is the injected fault: the step committed durably and
// the driver then died before performing any of its commands.
var errAbortedAfterCommit = errors.New("recovery-sweep-test: aborted after commit, before perform")

// crashAfterCommitStore is the fault-injection seam this package did not have.
//
// deliverLoop's commit is `err = tx.RunInTx(ctx, commitFn)` and the very next
// statement returns on a non-nil err — BEFORE the timer activation, syncWaiters
// and the perform loop. So a RunInTx that runs fn to completion (the commit
// lands; kernel.MemInstanceStore.RunInTx is sequencing-only and does not roll
// back) and only then reports failure reproduces "the process died between the
// commit and perform" exactly, with no reliance on goroutine timing.
//
// Arm is one-shot: it fires for the first commit and disarms itself, so the
// restart drives the recovered instance normally.
type crashAfterCommitStore struct {
	*kernel.MemInstanceStore

	armed atomic.Bool
	fired atomic.Int32
}

func newCrashAfterCommitStore(t *testing.T) *crashAfterCommitStore {
	t.Helper()
	return &crashAfterCommitStore{MemInstanceStore: runtimetest.MustMemStore(t)}
}

// Arm makes the next committed step abort before perform.
func (s *crashAfterCommitStore) Arm() { s.armed.Store(true) }

// RunInTx commits through the embedded store and then, when armed, reports the
// injected failure. Disarms itself so exactly one step is aborted.
func (s *crashAfterCommitStore) RunInTx(ctx context.Context, fn func(context.Context) error) error {
	if err := s.MemInstanceStore.RunInTx(ctx, fn); err != nil {
		return err
	}
	if s.armed.CompareAndSwap(true, false) {
		s.fired.Add(1)
		return errAbortedAfterCommit
	}
	return nil
}

// unlistableStore hides its backing store's kernel.InstanceLister implementation
// while forwarding everything the driver needs to run. It is how the
// reproduction test opts out of recovery WITHOUT depending on any API the fix
// adds — so both tests in this file compile against the pre-fix tree.
//
// Embedding the two INTERFACES rather than the concrete store is what hides the
// lister: a struct embedding *kernel.MemInstanceStore would promote List along
// with everything else. It also means this double never needs re-editing when
// kernel.InstanceStore grows a method.
//
// It pins the sweep's documented opt-out: a driver that cannot enumerate
// instances must leave the parked instance untouched.
type unlistableStore struct {
	kernel.InstanceStore
	kernel.TxRunner
}

func newUnlistableStore(inner *crashAfterCommitStore) *unlistableStore {
	return &unlistableStore{InstanceStore: inner, TxRunner: inner}
}

// serviceTaskDef is start → serviceTask(action) → end. The token parks on the
// service task until the action reports back.
func serviceTaskDef(actionName string) *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "recovery-service",
		Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("work", activity.WithTaskAction(actionName)),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "work"},
			{ID: "f2", Source: "work", Target: "end"},
		},
	}
}

// recoveryFixture is one scenario's wiring: a definition whose first committed
// step carries a command perform would have to run, plus `performed`, the
// observation that proves the command actually ran.
type recoveryFixture struct {
	def       *model.ProcessDefinition
	catalog   action.Catalog
	resolver  humantask.ActorResolver
	tasks     humantask.TaskStore
	performed func(t *testing.T, st engine.InstanceState) int
}

// serviceTaskFixture observes perform through a counting action catalog.
func serviceTaskFixture() recoveryFixture {
	var calls atomic.Int32
	cat := action.NewCatalog(map[string]action.Action{
		"work-action": action.ActionFunc(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			calls.Add(1)
			return map[string]any{"ok": true}, nil
		}),
	})
	return recoveryFixture{
		def:     serviceTaskDef("work-action"),
		catalog: cat,
		performed: func(_ *testing.T, _ engine.InstanceState) int {
			return int(calls.Load())
		},
	}
}

// humanTaskFixture observes perform through the task store: performAwaitHuman
// writes the row AFTER the commit, so the row's existence IS the observation.
func humanTaskFixture() recoveryFixture {
	tasks := humantask.NewMemTaskStore()
	return recoveryFixture{
		def:     runtimetest.ApprovalDef(),
		catalog: action.NewCatalog(nil),
		resolver: humantask.NewStaticActorResolver(map[string][]authz.Actor{
			"manager": {{ID: "alice", Roles: []string{"manager"}}},
		}),
		tasks: tasks,
		performed: func(t *testing.T, st engine.InstanceState) int {
			t.Helper()
			n := 0
			for _, task := range st.Tasks {
				if _, err := tasks.Get(context.Background(), task.TaskID); err == nil {
					n++
				}
			}
			return n
		},
	}
}

// driverFor builds a driver over store wired for f.
func driverFor(t *testing.T, f recoveryFixture, store kernel.InstanceStore) *runtime.ProcessDriver {
	t.Helper()
	reg := kernel.NewMemDefinitionRegistry()
	require.NoError(t, reg.Register(f.def))
	opts := []runtime.Option{runtime.WithDefinitions(reg)}
	if f.tasks != nil {
		opts = append(opts, runtime.WithHumanTasks(f.resolver, f.tasks, authz.AllowAll{}))
	}
	return runtimetest.MustProcessDriver(t, f.catalog, store, opts...)
}

// TestCommittedStepWithUnperformedCommandsIsRecoveredOnRestart is THE RED.
//
// After a fault injected between the step commit and perform, a driver restarted
// over the same store must re-drive the committed step's unperformed commands:
// the service action is invoked, and the human task's row appears.
func TestCommittedStepWithUnperformedCommandsIsRecoveredOnRestart(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		fixture func() recoveryFixture
		assert  func(t *testing.T, f recoveryFixture, st engine.InstanceState)
	}

	cases := []testCase{
		{
			name:    "service task action is re-invoked",
			fixture: serviceTaskFixture,
			assert: func(t *testing.T, f recoveryFixture, st engine.InstanceState) {
				assert.GreaterOrEqual(t, f.performed(t, st), 1,
					"the recovery sweep must re-invoke the committed step's unperformed InvokeAction")
				assert.Equal(t, engine.StatusCompleted, st.Status,
					"the recovered instance must advance past the service task")
			},
		},
		{
			name:    "human task row is written",
			fixture: humanTaskFixture,
			assert: func(t *testing.T, f recoveryFixture, st engine.InstanceState) {
				assert.GreaterOrEqual(t, f.performed(t, st), 1,
					"the recovery sweep must re-perform the committed step's unperformed AwaitHuman")
				assert.Equal(t, engine.StatusRunning, st.Status,
					"the recovered instance stays parked on the user task")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			f := tc.fixture()
			store := newCrashAfterCommitStore(t)

			// ── the fault ────────────────────────────────────────────────────
			store.Arm()
			crashed := driverFor(t, f, store)
			_, err := crashed.Drive(ctx, f.def, "i-recover", nil)
			require.ErrorIs(t, err, errAbortedAfterCommit,
				"the injected fault must abort the drive after the commit")

			// ── the fault-injection guard (rule 15) ──────────────────────────
			require.Equal(t, int32(1), store.fired.Load(),
				"fault-injection guard: the injected abort must have fired exactly once")
			committed, _, lerr := store.Load(ctx, "i-recover")
			require.NoError(t, lerr, "the aborted step must still have committed durably")
			require.Equal(t, engine.StatusRunning, committed.Status)
			require.Equal(t, 0, f.performed(t, committed),
				"fault-injection guard: no command may have been performed before the restart")

			// ── the restart ──────────────────────────────────────────────────
			restarted := driverFor(t, f, store)
			require.NoError(t, restarted.Start(ctx))

			final, _, err := store.Load(ctx, "i-recover")
			require.NoError(t, err)
			tc.assert(t, f, final)
		})
	}
}

// TestCommittedStepWithUnperformedCommandsStaysParkedWithoutRecovery is the
// REPRODUCTION, and it is also the guard on the sweep's documented opt-out: a
// driver whose store cannot be enumerated must leave the parked instance
// exactly as the fault left it rather than silently half-recovering it.
func TestCommittedStepWithUnperformedCommandsStaysParkedWithoutRecovery(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	f := serviceTaskFixture()
	backing := newCrashAfterCommitStore(t)
	store := newUnlistableStore(backing)

	backing.Arm()
	crashed := driverFor(t, f, store)
	_, err := crashed.Drive(ctx, f.def, "i-parked", nil)
	require.ErrorIs(t, err, errAbortedAfterCommit)

	require.Equal(t, int32(1), backing.fired.Load(),
		"fault-injection guard: the injected abort must have fired exactly once")
	committed, _, err := store.Load(ctx, "i-parked")
	require.NoError(t, err)
	require.Equal(t, 0, f.performed(t, committed),
		"fault-injection guard: no command may have been performed before the restart")

	restarted := driverFor(t, f, store)
	require.NoError(t, restarted.Start(ctx))

	st, _, err := store.Load(ctx, "i-parked")
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, st.Status,
		"without an enumerable store the instance stays parked forever")
	assert.Equal(t, 0, f.performed(t, st),
		"without an enumerable store the committed step's commands are never performed")
}

// TestSuccessfulDriveLeavesNoPendingCommandMark is the counterweight to the red:
// a step that completes normally must clear its own mark.
//
// Without it the sweep would find every parked instance in the system marked,
// and re-perform its commands once per lease window forever — a recovery
// mechanism that manufactures the duplication it exists to bound. The two park
// shapes are covered because they clear through different routes: the human task
// parks with the queue drained (the durable clear), while the service task's
// follow-up trigger drives another commit that persists the cleared value.
func TestSuccessfulDriveLeavesNoPendingCommandMark(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name    string
		fixture func() recoveryFixture
		assert  func(t *testing.T, f recoveryFixture, st engine.InstanceState)
	}

	cases := []testCase{
		{
			name:    "service task clears through the follow-up commit",
			fixture: serviceTaskFixture,
			assert: func(t *testing.T, f recoveryFixture, st engine.InstanceState) {
				assert.Equal(t, engine.StatusCompleted, st.Status)
				assert.Equal(t, 1, f.performed(t, st), "the action must run exactly once")
			},
		},
		{
			name:    "human task clears through the durable clear on park",
			fixture: humanTaskFixture,
			assert: func(t *testing.T, f recoveryFixture, st engine.InstanceState) {
				assert.Equal(t, engine.StatusRunning, st.Status)
				assert.Equal(t, 1, f.performed(t, st), "the task row must be written exactly once")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			f := tc.fixture()
			store := newCrashAfterCommitStore(t) // never armed: no fault in this test
			driver := driverFor(t, f, store)

			_, err := driver.Drive(ctx, f.def, "i-clean", nil)
			require.NoError(t, err)

			st, _, err := store.Load(ctx, "i-clean")
			require.NoError(t, err)
			assert.Empty(t, st.PendingCommands,
				"a step whose commands were all performed must not stay marked")
			assert.True(t, st.PendingCommandsAt.IsZero(),
				"the lease stamp must be cleared with the mark it dates")
			tc.assert(t, f, st)

			// And the sweep must therefore find nothing to do. Re-load afterwards
			// rather than re-asserting the pre-sweep snapshot: a stale `st` would
			// make the status half of the assertion vacuous.
			n, err := driver.RecoverPendingCommands(ctx)
			require.NoError(t, err)
			assert.Zero(t, n, "the sweep must not re-drive an instance that performed its commands")

			after, _, err := store.Load(ctx, "i-clean")
			require.NoError(t, err)
			tc.assert(t, f, after)
		})
	}
}

// TestRecoverPendingCommandsWithoutCapabilitiesIsANoOp pins the two ways a
// sweep declines to act, and that in both the MARK SURVIVES.
//
// Surviving is the point. A driver with no enumeration capability is exactly the
// pre-#110 driver and must keep working; an instance whose definition is not
// registered becomes recoverable the moment the registration is fixed, which it
// would not be had the sweep cleared the mark on its way past. Neither is an
// error at the pass level — one bad instance never aborts a batch, the same rule
// RehydrateTimers applies to one unschedulable timer.
func TestRecoverPendingCommandsWithoutCapabilitiesIsANoOp(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name  string
		build func(t *testing.T, f recoveryFixture, store *crashAfterCommitStore) *runtime.ProcessDriver
	}

	cases := []testCase{
		{
			name: "no enumeration capability",
			build: func(t *testing.T, f recoveryFixture, store *crashAfterCommitStore) *runtime.ProcessDriver {
				t.Helper()
				return driverFor(t, f, newUnlistableStore(store))
			},
		},
		{
			name: "definition not registered",
			build: func(t *testing.T, f recoveryFixture, store *crashAfterCommitStore) *runtime.ProcessDriver {
				t.Helper()
				// An EMPTY isolated registry: the sweep enumerates the instance,
				// fails to resolve its definition, and skips it with the mark intact.
				return runtimetest.MustProcessDriver(t, f.catalog, store,
					runtime.WithDefinitions(kernel.NewMemDefinitionRegistry()))
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			f := serviceTaskFixture()
			store := newCrashAfterCommitStore(t)

			store.Arm()
			crashed := driverFor(t, f, store)
			_, err := crashed.Drive(ctx, f.def, "i-nocap", nil)
			require.ErrorIs(t, err, errAbortedAfterCommit)
			require.Equal(t, int32(1), store.fired.Load(),
				"fault-injection guard: the injected abort must have fired exactly once")

			n, err := tc.build(t, f, store).RecoverPendingCommands(ctx)
			require.NoError(t, err, "a missing capability is not an error")
			assert.Zero(t, n)

			st, _, err := store.Load(ctx, "i-nocap")
			require.NoError(t, err)
			assert.NotEmpty(t, st.PendingCommands, "the mark must survive an un-swept driver")
		})
	}
}

// TestRunRecoverySweepStopsOnContextCancellation pins the loop's exit contract,
// which mirrors calllink.CallNotifier.Run's.
func TestRunRecoverySweepStopsOnContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	f := serviceTaskFixture()
	driver := driverFor(t, f, newCrashAfterCommitStore(t))

	cancel()
	require.ErrorIs(t, driver.RunRecoverySweep(ctx), context.Canceled)
}

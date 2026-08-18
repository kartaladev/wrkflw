package runtime_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/gateway"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/runtime"
	"github.com/kartaladev/wrkflw/runtime/internal/runtimetest"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// openTaskIndex returns the index of the single OPEN task in st, failing the
// test when there is not exactly one. Tasks accumulates across a run, so no
// caller may index 0 blindly.
func openTaskIndex(t *testing.T, st engine.InstanceState) int {
	t.Helper()
	idx := -1
	for i := range st.Tasks {
		if st.Tasks[i].IsOpen() {
			require.Equal(t, -1, idx, "expected exactly one open task")
			idx = i
		}
	}
	require.NotEqual(t, -1, idx, "expected exactly one open task")
	return idx
}

// TestPreCommitRejectionDoesNotCommitTheStep is the load-bearing test for
// ADR-0183's primary seam: a step whose task projection contradicts itself is
// refused BEFORE this iteration's commit, so the persisted snapshot is exactly
// what it was before the trigger arrived.
//
// The fixture reproduces backlog 32's downgrade: an instance is driven to a
// parked user task and claimed normally, then its SNAPSHOT is corrupted
// out-of-band (the Claim nilled on a Claimed task) the way a rollback to a build
// without the write guard would leave it. The corrupted shape is then re-emitted
// through engine.HumanCandidatesResolved — the one UpdateTask emitter that
// passes the stored record through untouched, setting neither State nor Claim.
//
// The discriminating assertion is the VERSION: without the hook the step commits
// and the version advances, and the rejection would land post-commit inside
// TaskStore.Upsert with the state already durable.
func TestPreCommitRejectionDoesNotCommitTheStep(t *testing.T) {
	ctx := t.Context()

	at := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	manager := authz.Actor{ID: "alice", Roles: []string{"manager"}}

	taskStore := humantask.NewMemTaskStore()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{"manager": {manager}})
	az := authz.RoleAuthorizer{}
	store := runtimetest.MustMemStore(t)
	driver := runtimetest.MustProcessDriver(t, nil, store,
		runtime.WithHumanTasks(resolver, taskStore, az),
	)
	def := runtimetest.ApprovalDef()
	const instanceID = "inst-claim-invariant"

	// --- 1. Park at the user task and claim it normally. ---
	parked, err := driver.Drive(ctx, def, instanceID, nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, parked.Status)
	taskID := parked.Tasks[openTaskIndex(t, parked)].TaskID

	svc := runtimetest.MustTaskService(t, taskStore, az)
	claimTrg, err := svc.Claim(ctx, taskID, manager)
	require.NoError(t, err)
	claimed, err := driver.ApplyTrigger(ctx, def, instanceID, claimTrg)
	require.NoError(t, err)
	require.Equal(t, humantask.Claimed, claimed.Tasks[openTaskIndex(t, claimed)].State)

	// --- 2. Corrupt the SNAPSHOT out of band, as a downgrade would. ---
	loaded, version, err := store.Load(ctx, instanceID)
	require.NoError(t, err)
	idx := openTaskIndex(t, loaded)
	require.NotNil(t, loaded.Tasks[idx].Claim, "precondition: the claim is present before corruption")
	loaded.Tasks[idx].Claim = nil
	// The Trigger is a fabrication — no trigger produces this shape, which is the
	// point — and it lands in the journal, so any Entries() count must allow for
	// it. Commit only persists State; it never applies the Trigger.
	corruptVersion, err := store.Commit(ctx, version, kernel.AppliedStep{
		State:   loaded,
		Trigger: engine.NewHumanCandidatesResolved(at, taskID, []authz.Actor{manager}),
	})
	require.NoError(t, err)

	before, beforeVersion, err := store.Load(ctx, instanceID)
	require.NoError(t, err)
	require.Equal(t, corruptVersion, beforeVersion)
	require.Equal(t, humantask.Claimed, before.Tasks[idx].State,
		"the corrupted shape must survive the Clone round-trip")
	require.Nil(t, before.Tasks[idx].Claim,
		"the corrupted shape must survive the Clone round-trip")
	require.Empty(t, before.Incidents, "precondition: no incidents before the refused trigger")

	// --- 3. Re-emit the corrupt shape through the pass-through emitter. ---
	refresh := engine.NewHumanCandidatesResolved(at.Add(time.Minute), taskID, []authz.Actor{manager})
	_, err = driver.ApplyTrigger(ctx, def, instanceID, refresh)
	require.ErrorIs(t, err, humantask.ErrInvalidTask,
		"a contradictory task projection must refuse the step")

	// --- 4. Nothing of that step was committed. ---
	after, afterVersion, err := store.Load(ctx, instanceID)
	require.NoError(t, err)
	assert.Equal(t, corruptVersion, afterVersion,
		"the refused step must not commit: the version must not advance")
	assert.Empty(t, after.Incidents, "a refused step raises no incident")
	require.Len(t, after.Tokens, len(before.Tokens), "no token added or retired")
	assert.Equal(t, before.Tokens[0].AwaitCommand, after.Tokens[0].AwaitCommand,
		"the token must still await exactly what it awaited before")
	assert.Equal(t, before.Tokens[0].State, after.Tokens[0].State)
	assert.Equal(t, before.Tasks[idx].Candidates, after.Tasks[idx].Candidates,
		"the refused step must not have written its resolved candidates")
	assert.Equal(t, before.Status, after.Status)
}

// TestTerminalSweepReconcilesEveryTaskDespiteACorruptOne is audit finding B8's
// regression guard, in the shape the engine can actually produce.
//
// B8 asked for a terminal sweep whose FIRST UpdateTask is invalid, to pin that a
// rejection never drops tasks 2..N. MEASURED: that shape is unconstructible.
// cancelOpenTasks (engine/state.go) assigns humantask.Cancelled to every task it
// sweeps, and Validate leaves Cancelled unconstrained on the claim axis, so a
// swept task is valid no matter how corrupt the snapshot was. Probed on a
// two-user-task definition with task 0 corrupted: both tasks came out
// state=cancelled with Validate returning nil, and CancelInstance committed
// normally (version 2 -> 3).
//
// What is left to guard is the property that measurement exposes, and it is the
// one that matters: the pre-commit hook must NOT block the sweep. A corrupted
// instance has to stay killable, and both tasks must be reconciled out of the
// inboxes cancelOpenTasks exists to clear — in ONE commit, so neither is lost.
//
// This fails if the hook over-rejects (e.g. if it constrained a Cancelled task
// carrying a claim, the shape task 0 is deliberately corrupted into here), or if
// the sweep stopped emitting one UpdateTask per open task.
func TestTerminalSweepReconcilesEveryTaskDespiteACorruptOne(t *testing.T) {
	ctx := t.Context()

	at := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	manager := authz.Actor{ID: "alice", Roles: []string{"manager"}}

	taskStore := humantask.NewMemTaskStore()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{"manager": {manager}})
	az := authz.RoleAuthorizer{}
	store := runtimetest.MustMemStore(t)
	driver := runtimetest.MustProcessDriver(t, nil, store,
		runtime.WithHumanTasks(resolver, taskStore, az),
	)

	def, err := definition.NewBuilder("two-open-tasks", 1).
		Add(event.NewStart("s")).
		Add(gateway.NewParallel("fork")).
		Add(activity.NewUserTask("a", activity.WithEligibleRoles("manager"))).
		Add(activity.NewUserTask("b", activity.WithEligibleRoles("manager"))).
		Add(gateway.NewParallel("join")).
		Add(event.NewEnd("e")).
		Connect("s", "fork").Connect("fork", "a").Connect("fork", "b").
		Connect("a", "join").Connect("b", "join").Connect("join", "e").
		Build()
	require.NoError(t, err)

	const instanceID = "inst-two-tasks"
	parked, err := driver.Drive(ctx, def, instanceID, nil)
	require.NoError(t, err)
	require.Len(t, parked.Tasks, 2, "the fork must park two open tasks")

	// Corrupt task 0 into an R2 violation: Unclaimed while carrying a claim. The
	// sweep will turn it into Cancelled + claim, which stays legal by design (a
	// task cancelled while held keeps its claim as audit).
	loaded, version, err := store.Load(ctx, instanceID)
	require.NoError(t, err)
	require.True(t, loaded.Tasks[0].IsOpen())
	require.True(t, loaded.Tasks[1].IsOpen())
	loaded.Tasks[0].Claim = &humantask.Claim{Actor: authz.Actor{ID: "alice"}, At: at}
	corruptVersion, err := store.Commit(ctx, version, kernel.AppliedStep{
		State:   loaded,
		Trigger: engine.NewCancelRequested(at),
	})
	require.NoError(t, err)

	// --- The corrupted instance must still be killable. ---
	terminated, err := driver.CancelInstance(ctx, def, instanceID)
	require.NoError(t, err, "a corrupted instance must stay killable")
	assert.True(t, terminated.Status.IsTerminal())

	after, afterVersion, err := store.Load(ctx, instanceID)
	require.NoError(t, err)
	assert.Equal(t, corruptVersion+1, afterVersion,
		"the sweep must land in exactly one commit")

	// --- Neither task's reconciliation is lost. ---
	require.Len(t, after.Tasks, 2)
	for i := range after.Tasks {
		assert.Equal(t, humantask.Cancelled, after.Tasks[i].State,
			"task %d must be cancelled in instance state", i)

		stored, getErr := taskStore.Get(ctx, after.Tasks[i].TaskID)
		require.NoError(t, getErr, "task %d must have reached the store", i)
		assert.Equal(t, humantask.Cancelled, stored.State,
			"task %d must be cancelled in the store, or it stays in the inboxes", i)
	}
	assert.NotNil(t, after.Tasks[0].Claim,
		"a task cancelled while held keeps its claim — the shape must stay legal")

	claimable, err := taskStore.ClaimableBy(ctx, manager)
	require.NoError(t, err)
	assert.Empty(t, claimable, "no task of a terminated instance may remain claimable")

	// AssignedTo filters on the Claim pointer alone and never on State (measured:
	// humantask/memory.go:77), so the cancelled-while-held task IS returned — by
	// design, since ADR-0148 keeps the claim as audit. What must hold is that the
	// row the inbox hands back is the RECONCILED one, so a reader can see it is
	// closed rather than acting on a task that no longer exists.
	assigned, err := taskStore.AssignedTo(ctx, manager.ID)
	require.NoError(t, err)
	require.Len(t, assigned, 1, "only the corrupted task carries a claim for this actor")
	assert.Equal(t, humantask.Cancelled, assigned[0].State,
		"the inbox row must reflect the terminal sweep, not the pre-sweep state")
}

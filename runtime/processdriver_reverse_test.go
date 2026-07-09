package runtime_test

// processdriver_reverse_test.go — Task 5 (ADR-0109): facade tests for
// ProcessDriver.ReverseInstance. TestReverseInstance below is a table because
// every case shares the exact same SUT call shape (driver.ReverseInstance(ctx,
// def, id, opts...) -> (engine.InstanceState, error)) even though the per-case
// fixture setup differs (some cases drive a fresh compensable instance, some
// reuse a terminal or malformed-definition fixture) — the divergent setup is
// pulled into each case's `setup` closure per the project table-test skill.

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/internal/runtimetest"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// reverseActionCounts tallies how many times each named action ran, so a case's
// assert closure can prove exactly which compensate/forward actions fired (and
// how many times) without depending on brittle command-order inspection.
type reverseActionCounts struct {
	do     atomic.Int32
	undo   atomic.Int32
	next   atomic.Int32
	unnext atomic.Int32
}

// reverseFixtureDef returns: start -> approve1(UserTask) -> svc(compensable
// "do"/"undo") -> next(compensable "next"/"unnext") -> approve2(UserTask) -> end.
//
// Two parking points (approve1, approve2) bracket the two compensable nodes so a
// test can: drive to approve1 (vars untouched), complete approve1 with an output
// that mutates vars, auto-drive through svc+next to approve2 (vars mutated,
// 2 compensation records), then reverse from there — a FULL reverse resumes at
// "start" and parks again at approve1 WITHOUT re-running svc/next (proving vars
// reset, since approve1 is reached before either), while a WithTargetNode("svc")
// reverse resumes AT svc, re-drives through next to approve2 again (proving vars
// were kept, and that svc's own record was excluded from the walk).
func reverseFixtureDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-reverse-facade", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("approve1", activity.WithCandidateRoles("manager")),
			activity.NewServiceTask("svc", activity.WithTaskAction("do"), activity.WithCompensateAction("undo")),
			activity.NewServiceTask("next", activity.WithTaskAction("next"), activity.WithCompensateAction("unnext")),
			activity.NewUserTask("approve2", activity.WithCandidateRoles("manager")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "approve1"},
			{ID: "f2", Source: "approve1", Target: "svc"},
			{ID: "f3", Source: "svc", Target: "next"},
			{ID: "f4", Source: "next", Target: "approve2"},
			{ID: "f5", Source: "approve2", Target: "end"},
		},
	}
}

// reverseFixtureCatalog builds the action.Catalog for reverseFixtureDef, tallying
// every invocation into counts.
func reverseFixtureCatalog(counts *reverseActionCounts) action.Catalog {
	return action.NewCatalog(map[string]action.Action{
		"do": action.ActionFunc(func(context.Context, map[string]any) (map[string]any, error) {
			counts.do.Add(1)
			return nil, nil
		}),
		"undo": action.ActionFunc(func(context.Context, map[string]any) (map[string]any, error) {
			counts.undo.Add(1)
			return nil, nil
		}),
		"next": action.ActionFunc(func(context.Context, map[string]any) (map[string]any, error) {
			counts.next.Add(1)
			return nil, nil
		}),
		"unnext": action.ActionFunc(func(context.Context, map[string]any) (map[string]any, error) {
			counts.unnext.Add(1)
			return nil, nil
		}),
	})
}

// reverseFixture bundles everything a ReverseInstance case needs: the wired
// driver, the definition it was driven with, the instance ID, the action-tally
// counts, and the raw store (so a case can reload state after a rejected call to
// prove no state change occurred, without ReverseInstance itself needing to
// expose that).
type reverseFixture struct {
	driver     *runtime.ProcessDriver
	def        *model.ProcessDefinition
	instanceID string
	counts     *reverseActionCounts
	store      *kernel.MemInstanceStore
}

// driveReverseFixtureToApprove2 drives a fresh reverseFixtureDef instance to
// approve1, completes it with {"amount": 999} (mutating vars away from the
// {"amount": 100} StartVariables), then lets the instance auto-drive through svc
// and next (recording 2 compensation entries) to park at approve2.
func driveReverseFixtureToApprove2(t *testing.T) reverseFixture {
	t.Helper()
	ctx := t.Context()

	counts := &reverseActionCounts{}
	manager := authz.Actor{ID: "alice", Roles: []string{"manager"}}
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{"manager": {manager}})
	taskStore := humantask.NewMemTaskStore()
	az := authz.RoleAuthorizer{}
	store := runtimetest.MustMemStore(t)

	driver := runtimetest.MustRunner(t, reverseFixtureCatalog(counts), store, runtime.WithHumanTasks(resolver, taskStore, az))
	def := reverseFixtureDef()
	instanceID := "reverse-facade-1"

	parked, err := driver.Drive(ctx, def, instanceID, map[string]any{"amount": 100})
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, parked.Status)
	require.Len(t, parked.Tokens, 1)
	require.Equal(t, "approve1", parked.Tokens[0].NodeID)

	svc := runtimetest.MustTaskService(t, taskStore, az)
	taskToken := parked.Tasks[0].TaskToken

	claimTrg, err := svc.Claim(ctx, taskToken, manager)
	require.NoError(t, err)
	_, err = driver.ApplyTrigger(ctx, def, instanceID, claimTrg)
	require.NoError(t, err)

	completeTrg, err := svc.Complete(ctx, taskToken, manager, map[string]any{"amount": 999})
	require.NoError(t, err)
	parked2, err := driver.ApplyTrigger(ctx, def, instanceID, completeTrg)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, parked2.Status, "instance must park at approve2")
	require.Len(t, parked2.Tokens, 1)
	require.Equal(t, "approve2", parked2.Tokens[0].NodeID)
	require.Len(t, parked2.RootCompensations, 2, "svc + next compensation records")
	require.EqualValues(t, 999, parked2.Variables["amount"])

	return reverseFixture{driver: driver, def: def, instanceID: instanceID, counts: counts, store: store}
}

// reverseTargetVarsFixtureDef returns: start -> approve1(UserTask) ->
// svc(compensable "do"/"undo") -> approve2(UserTask) -> next(compensable
// "next"/"unnext") -> approve3(UserTask) -> end.
//
// Both approve1 and approve2 mutate variables on completion, with approve2's
// mutation happening strictly AFTER svc's start-of-visit snapshot was
// captured (that snapshot is taken when the token first arrives at svc,
// right after approve1 completes). A WithTargetNode("svc") reverse driven
// from approve3 must restore Variables to svc's start-of-visit snapshot —
// discarding approve2's later mutation — rather than keeping the current
// (approve2-mutated) variables. Because resuming at svc re-parks at approve2
// (a UserTask needing an explicit Complete), the auto-drive following the
// reverse does NOT re-apply approve2's mutation, so the restored snapshot is
// directly observable in the state ReverseInstance returns.
func reverseTargetVarsFixtureDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-reverse-target-vars", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("approve1", activity.WithCandidateRoles("manager")),
			activity.NewServiceTask("svc", activity.WithTaskAction("do"), activity.WithCompensateAction("undo")),
			activity.NewUserTask("approve2", activity.WithCandidateRoles("manager")),
			activity.NewServiceTask("next", activity.WithTaskAction("next"), activity.WithCompensateAction("unnext")),
			activity.NewUserTask("approve3", activity.WithCandidateRoles("manager")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "approve1"},
			{ID: "f2", Source: "approve1", Target: "svc"},
			{ID: "f3", Source: "svc", Target: "approve2"},
			{ID: "f4", Source: "approve2", Target: "next"},
			{ID: "f5", Source: "next", Target: "approve3"},
			{ID: "f6", Source: "approve3", Target: "end"},
		},
	}
}

// driveReverseTargetVarsFixtureToApprove3 drives a fresh
// reverseTargetVarsFixtureDef instance: completes approve1 with
// {"amount": 999} (mutating vars away from the {"amount": 100}
// StartVariables), auto-drives through svc to approve2, completes approve2
// with {"amount": 5000} (a SECOND mutation, occurring after svc's
// start-of-visit snapshot was already captured at 999), then auto-drives
// through next to park at approve3.
func driveReverseTargetVarsFixtureToApprove3(t *testing.T) reverseFixture {
	t.Helper()
	ctx := t.Context()

	counts := &reverseActionCounts{}
	manager := authz.Actor{ID: "alice", Roles: []string{"manager"}}
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{"manager": {manager}})
	taskStore := humantask.NewMemTaskStore()
	az := authz.RoleAuthorizer{}
	store := runtimetest.MustMemStore(t)

	driver := runtimetest.MustRunner(t, reverseFixtureCatalog(counts), store, runtime.WithHumanTasks(resolver, taskStore, az))
	def := reverseTargetVarsFixtureDef()
	instanceID := "reverse-target-vars-1"
	svc := runtimetest.MustTaskService(t, taskStore, az)

	parked, err := driver.Drive(ctx, def, instanceID, map[string]any{"amount": 100})
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, parked.Status)
	require.Len(t, parked.Tokens, 1)
	require.Equal(t, "approve1", parked.Tokens[0].NodeID)

	completeApprove := func(state engine.InstanceState, amount int) engine.InstanceState {
		t.Helper()
		// state.Tasks accumulates every human-task record for the instance's
		// lifetime (completed ones are retained, not removed) so the OPEN
		// task is not reliably at index 0 once a second UserTask has run;
		// find it explicitly via HumanTask.IsOpen.
		var taskToken string
		for _, ht := range state.Tasks {
			if ht.IsOpen() {
				taskToken = ht.TaskToken
				break
			}
		}
		require.NotEmpty(t, taskToken, "expected exactly one open human task")
		claimTrg, err := svc.Claim(ctx, taskToken, manager)
		require.NoError(t, err)
		_, err = driver.ApplyTrigger(ctx, def, instanceID, claimTrg)
		require.NoError(t, err)
		completeTrg, err := svc.Complete(ctx, taskToken, manager, map[string]any{"amount": amount})
		require.NoError(t, err)
		next, err := driver.ApplyTrigger(ctx, def, instanceID, completeTrg)
		require.NoError(t, err)
		return next
	}

	parked2 := completeApprove(parked, 999)
	require.Equal(t, engine.StatusRunning, parked2.Status, "instance must park at approve2")
	require.Len(t, parked2.Tokens, 1)
	require.Equal(t, "approve2", parked2.Tokens[0].NodeID)
	require.EqualValues(t, 999, parked2.Variables["amount"])

	parked3 := completeApprove(parked2, 5000)
	require.Equal(t, engine.StatusRunning, parked3.Status, "instance must park at approve3")
	require.Len(t, parked3.Tokens, 1)
	require.Equal(t, "approve3", parked3.Tokens[0].NodeID)
	require.Len(t, parked3.RootCompensations, 2, "svc + next compensation records")
	require.EqualValues(t, 5000, parked3.Variables["amount"])

	return reverseFixture{driver: driver, def: def, instanceID: instanceID, counts: counts, store: store}
}

// driveTerminalFixture builds a driver over a trivial start->end definition,
// drives it to completion (StatusCompleted), and returns it so a case can attempt
// to reverse an already-terminal instance.
func driveTerminalFixture(t *testing.T) reverseFixture {
	t.Helper()
	def := &model.ProcessDefinition{
		ID: "p-reverse-terminal", Version: 1,
		Nodes: []model.Node{event.NewStart("start"), event.NewEnd("end")},
		Flows: []flow.SequenceFlow{{ID: "f1", Source: "start", Target: "end"}},
	}
	store := runtimetest.MustMemStore(t)
	driver := runtimetest.MustRunner(t, nil, store)
	instanceID := "reverse-terminal-1"

	final, err := driver.Drive(t.Context(), def, instanceID, nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompleted, final.Status)
	return reverseFixture{driver: driver, def: def, instanceID: instanceID, store: store}
}

// driveRunningInstanceForMalformedDefCases builds a driver over a plain
// start->approve->end definition and parks it (Running), so cases that want to
// exercise the start-node-resolution guard can pass a DIFFERENT, malformed
// definition (0 or 2 start events) for the SAME instance ID: the guard inspects
// def.StartNodes() before ever touching the engine, so the mismatch between
// "definition used to create the instance" and "definition passed to
// ReverseInstance" is immaterial to what's under test here.
func driveRunningInstanceForMalformedDefCases(t *testing.T) reverseFixture {
	t.Helper()
	def := runtimetest.ApprovalDef()
	resolver := humantask.NewStaticActorResolver(map[string][]authz.Actor{"manager": {{ID: "alice", Roles: []string{"manager"}}}})
	store := runtimetest.MustMemStore(t)
	driver := runtimetest.MustRunner(t, nil, store, runtime.WithHumanTasks(resolver, humantask.NewMemTaskStore(), authz.RoleAuthorizer{}))
	instanceID := "reverse-malformed-1"

	parked, err := driver.Drive(t.Context(), def, instanceID, nil)
	require.NoError(t, err)
	require.Equal(t, engine.StatusRunning, parked.Status)
	return reverseFixture{driver: driver, instanceID: instanceID, store: store}
}

func zeroStartDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-zero-start", Version: 1,
		Nodes: []model.Node{activity.NewServiceTask("x", activity.WithTaskAction("x")), event.NewEnd("end")},
		Flows: []flow.SequenceFlow{{ID: "f1", Source: "x", Target: "end"}},
	}
}

func twoStartDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-two-start", Version: 1,
		Nodes: []model.Node{event.NewStart("s1"), event.NewStart("s2"), event.NewEnd("end")},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "s1", Target: "end"},
			{ID: "f2", Source: "s2", Target: "end"},
		},
	}
}

// TestReverseInstance exercises every ReverseInstance case over the single shared
// SUT call driver.ReverseInstance(ctx, def, instanceID, opts...).
func TestReverseInstance(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		setup  func(t *testing.T) (fx reverseFixture, def *model.ProcessDefinition, opts []runtime.ReverseOption)
		assert func(t *testing.T, fx reverseFixture, got engine.InstanceState, err error)
	}

	cases := []testCase{
		{
			name: "default (no option) performs a full reverse: resumes at start with reset vars",
			setup: func(t *testing.T) (reverseFixture, *model.ProcessDefinition, []runtime.ReverseOption) {
				fx := driveReverseFixtureToApprove2(t)
				return fx, fx.def, nil
			},
			assert: func(t *testing.T, fx reverseFixture, got engine.InstanceState, err error) {
				require.NoError(t, err)
				assert.Equal(t, engine.StatusRunning, got.Status)
				require.Len(t, got.Tokens, 1)
				assert.Equal(t, "approve1", got.Tokens[0].NodeID, "full reverse resumes from start and parks at the first node again")
				assert.EqualValues(t, 100, got.Variables["amount"], "vars reset to StartVariables")
				assert.Empty(t, got.RootCompensations, "records cleared after full reverse")
				assert.EqualValues(t, 1, fx.counts.undo.Load(), "svc's compensate action must fire")
				assert.EqualValues(t, 1, fx.counts.unnext.Load(), "next's compensate action must fire")
				assert.EqualValues(t, 1, fx.counts.do.Load(), "svc's forward action must NOT re-run (parked at approve1 first)")
				assert.EqualValues(t, 1, fx.counts.next.Load(), "next's forward action must NOT re-run (parked at approve1 first)")
			},
		},
		{
			name: "WithFullReverse() behaves identically to the default",
			setup: func(t *testing.T) (reverseFixture, *model.ProcessDefinition, []runtime.ReverseOption) {
				fx := driveReverseFixtureToApprove2(t)
				return fx, fx.def, []runtime.ReverseOption{runtime.WithFullReverse()}
			},
			assert: func(t *testing.T, fx reverseFixture, got engine.InstanceState, err error) {
				require.NoError(t, err)
				assert.Equal(t, engine.StatusRunning, got.Status)
				require.Len(t, got.Tokens, 1)
				assert.Equal(t, "approve1", got.Tokens[0].NodeID)
				assert.EqualValues(t, 100, got.Variables["amount"], "vars reset to StartVariables")
				assert.EqualValues(t, 1, fx.counts.undo.Load())
				assert.EqualValues(t, 1, fx.counts.unnext.Load())
			},
		},
		{
			name: "WithTargetNode(svc) performs a partial reverse: resumes at svc, restores svc's start-of-visit vars",
			setup: func(t *testing.T) (reverseFixture, *model.ProcessDefinition, []runtime.ReverseOption) {
				fx := driveReverseFixtureToApprove2(t)
				return fx, fx.def, []runtime.ReverseOption{runtime.WithTargetNode("svc")}
			},
			assert: func(t *testing.T, fx reverseFixture, got engine.InstanceState, err error) {
				require.NoError(t, err)
				assert.Equal(t, engine.StatusRunning, got.Status)
				require.Len(t, got.Tokens, 1)
				assert.Equal(t, "approve2", got.Tokens[0].NodeID, "resumed at svc and auto-drove back through next to approve2")
				assert.EqualValues(t, 999, got.Variables["amount"], "restored to svc's start-of-visit snapshot; equals the current value here because nothing mutated vars between svc's arrival and this reverse")
				assert.EqualValues(t, 0, fx.counts.undo.Load(), "svc is the rollback target and its own record is excluded")
				assert.EqualValues(t, 1, fx.counts.unnext.Load(), "next's record (after svc) must be compensated")
				assert.EqualValues(t, 2, fx.counts.do.Load(), "svc's forward action re-runs on resume")
				assert.EqualValues(t, 2, fx.counts.next.Load(), "next's forward action re-runs while driving back to approve2")
			},
		},
		{
			name: "WithTargetNode(svc) restores start-of-visit vars, discarding a later mutation made after svc's arrival",
			setup: func(t *testing.T) (reverseFixture, *model.ProcessDefinition, []runtime.ReverseOption) {
				fx := driveReverseTargetVarsFixtureToApprove3(t)
				return fx, fx.def, []runtime.ReverseOption{runtime.WithTargetNode("svc")}
			},
			assert: func(t *testing.T, fx reverseFixture, got engine.InstanceState, err error) {
				require.NoError(t, err)
				assert.Equal(t, engine.StatusRunning, got.Status)
				require.Len(t, got.Tokens, 1)
				assert.Equal(t, "approve2", got.Tokens[0].NodeID, "resumed at svc, re-parks at approve2 (UserTask) before approve2's mutation can be re-applied")
				assert.EqualValues(t, 999, got.Variables["amount"], "restored to svc's start-of-visit snapshot, discarding approve2's later mutation to 5000")
				assert.EqualValues(t, 0, fx.counts.undo.Load(), "svc is the rollback target and its own record is excluded")
				assert.EqualValues(t, 1, fx.counts.unnext.Load(), "next's record (after svc) must be compensated")
				assert.EqualValues(t, 2, fx.counts.do.Load(), "svc's forward action re-runs on resume")
			},
		},
		{
			name: "WithFullReverse and WithTargetNode together is a mutual-exclusion error with no state change",
			setup: func(t *testing.T) (reverseFixture, *model.ProcessDefinition, []runtime.ReverseOption) {
				fx := driveReverseFixtureToApprove2(t)
				return fx, fx.def, []runtime.ReverseOption{runtime.WithFullReverse(), runtime.WithTargetNode("svc")}
			},
			assert: func(t *testing.T, fx reverseFixture, got engine.InstanceState, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "workflow-runtime")
				assert.EqualValues(t, 0, fx.counts.undo.Load(), "no compensation must fire before the guard rejects")
				assert.EqualValues(t, 0, fx.counts.unnext.Load())

				reloaded, _, loadErr := fx.store.Load(t.Context(), fx.instanceID)
				require.NoError(t, loadErr)
				assert.Equal(t, engine.StatusRunning, reloaded.Status, "no state change on a rejected call")
				assert.EqualValues(t, 999, reloaded.Variables["amount"])
				assert.Len(t, reloaded.RootCompensations, 2)
			},
		},
		{
			name: "WithTargetNode empty string is a rejected error with no state change",
			setup: func(t *testing.T) (reverseFixture, *model.ProcessDefinition, []runtime.ReverseOption) {
				fx := driveReverseFixtureToApprove2(t)
				return fx, fx.def, []runtime.ReverseOption{runtime.WithTargetNode("")}
			},
			assert: func(t *testing.T, fx reverseFixture, got engine.InstanceState, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "workflow-runtime")
				assert.EqualValues(t, 0, fx.counts.undo.Load(), "no compensation must fire before the guard rejects")
				assert.EqualValues(t, 0, fx.counts.unnext.Load())

				reloaded, _, loadErr := fx.store.Load(t.Context(), fx.instanceID)
				require.NoError(t, loadErr)
				assert.Equal(t, engine.StatusRunning, reloaded.Status, "no state change on a rejected call; instance must NOT be silently terminated")
				assert.EqualValues(t, 999, reloaded.Variables["amount"])
				assert.Len(t, reloaded.RootCompensations, 2)
			},
		},
		{
			name: "zero start events surfaces a start-resolution error",
			setup: func(t *testing.T) (reverseFixture, *model.ProcessDefinition, []runtime.ReverseOption) {
				fx := driveRunningInstanceForMalformedDefCases(t)
				return fx, zeroStartDef(), nil
			},
			assert: func(t *testing.T, fx reverseFixture, got engine.InstanceState, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "workflow-runtime")
			},
		},
		{
			name: "two start events surfaces a start-resolution error",
			setup: func(t *testing.T) (reverseFixture, *model.ProcessDefinition, []runtime.ReverseOption) {
				fx := driveRunningInstanceForMalformedDefCases(t)
				return fx, twoStartDef(), nil
			},
			assert: func(t *testing.T, fx reverseFixture, got engine.InstanceState, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "workflow-runtime")
			},
		},
		{
			name: "reversing a terminal instance is a clean error",
			setup: func(t *testing.T) (reverseFixture, *model.ProcessDefinition, []runtime.ReverseOption) {
				fx := driveTerminalFixture(t)
				return fx, fx.def, nil
			},
			assert: func(t *testing.T, fx reverseFixture, got engine.InstanceState, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "workflow-runtime")
			},
		},
		{
			name: "unknown WithTargetNode node surfaces the engine's own error",
			setup: func(t *testing.T) (reverseFixture, *model.ProcessDefinition, []runtime.ReverseOption) {
				fx := driveReverseFixtureToApprove2(t)
				return fx, fx.def, []runtime.ReverseOption{runtime.WithTargetNode("does-not-exist")}
			},
			assert: func(t *testing.T, fx reverseFixture, got engine.InstanceState, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "not found in scope records")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fx, def, opts := tc.setup(t)

			got, err := fx.driver.ReverseInstance(t.Context(), def, fx.instanceID, opts...)

			tc.assert(t, fx, got, err)
		})
	}
}

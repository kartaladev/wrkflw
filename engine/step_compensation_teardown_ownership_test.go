package engine_test

// ADR-0173, scope-wide branch: what a compensation walk OWNS when a scope
// teardown that cannot be deferred destroys its scope mid-walk.
//
// ADR-0171 gave such a walk a pinned record SOURCE so it survives the teardown.
// It left ownership open: the teardown copied the walk's own records into
// ArchivedCompensations, and the finish's clearRecordsPrefix then no-opped on the
// destroyed scope — so a later walk re-ran them. Compensation actions are nowhere
// required to be idempotent.
//
// The fixtures below reuse errorBoundaryTeardownDef and interruptingESPTeardownDef
// from step_compensation_scope_drain_test.go, which are the two teardown routes
// ADR-0171 documented. ⚠ A THIRD exists and gets its own fixture here
// (teardownNestedDef): cancelScopeSubtree archives every DESCENDANT scope, so an
// ancestor teardown reaches a nested walk's own scope.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/gateway"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
)

// teardownBodyOpts parameterizes the sub-process body every fixture here embeds.
type teardownBodyOpts struct {
	// saga names the compensable tasks run in order before the fork.
	saga []string
	// siblingSaga, when non-empty, names a compensable task the SIBLING branch
	// completes mid-walk — a record appended to the scope ABOVE the prefix the
	// walk committed to (ADR-0120 review A1), which must survive the teardown.
	siblingSaga string
	// terminateAfterRecovery routes the boundary's recovery branch into a
	// force-termination end event, abandoning the walk before it finishes.
	terminateAfterRecovery bool
	// recoveryIsUserTask parks the recovery branch so the walk can be advanced
	// before the abandonment.
	recoveryIsUserTask bool
}

// teardownDef builds `start → outer(sub-process) [error boundary E1] → end`, whose
// body runs a compensable saga, then forks into a scope-wide compensation throw and
// a sibling that fails with E1. The boundary must fire, so its teardown of the
// walk's scope cannot be deferred the way a normal drain exit can — which is what
// makes this the reproduction and not merely a race.
func teardownDef(o teardownBodyOpts) *model.ProcessDefinition {
	nodes := []model.Node{event.NewStart("bodyStart")}
	var flows []flow.SequenceFlow
	prev := "bodyStart"
	for _, name := range o.saga {
		id := "svc" + name
		nodes = append(nodes, activity.NewServiceTask(id,
			activity.WithTaskAction("do"+name), activity.WithCompensateAction("undo"+name)))
		flows = append(flows, flow.SequenceFlow{ID: "bf" + name, Source: prev, Target: id})
		prev = id
	}
	nodes = append(nodes,
		gateway.NewParallel("bodyFork"),
		event.NewCompensateThrow("bodyThrow"),
		event.NewEnd("bodyEndThrow"),
		activity.NewServiceTask("svcFails", activity.WithTaskAction("doFails")),
		event.NewEnd("bodyEndSibling"))
	flows = append(flows,
		flow.SequenceFlow{ID: "bfork", Source: prev, Target: "bodyFork"},
		flow.SequenceFlow{ID: "bthrow", Source: "bodyFork", Target: "bodyThrow"},
		flow.SequenceFlow{ID: "bthrowend", Source: "bodyThrow", Target: "bodyEndThrow"},
		flow.SequenceFlow{ID: "bsibend", Source: "svcFails", Target: "bodyEndSibling"})

	if o.siblingSaga != "" {
		// The sibling completes its own compensable task BEFORE failing, so the
		// record lands in the scope while the walk is outstanding.
		id := "svc" + o.siblingSaga
		nodes = append(nodes, activity.NewServiceTask(id,
			activity.WithTaskAction("do"+o.siblingSaga),
			activity.WithCompensateAction("undo"+o.siblingSaga)))
		flows = append(flows,
			flow.SequenceFlow{ID: "bsib0", Source: "bodyFork", Target: id},
			flow.SequenceFlow{ID: "bsib1", Source: id, Target: "svcFails"})
	} else {
		flows = append(flows, flow.SequenceFlow{ID: "bsib", Source: "bodyFork", Target: "svcFails"})
	}

	body := &model.ProcessDefinition{ID: "teardown-body", Version: 1, Nodes: nodes, Flows: flows}

	recovery := model.Node(activity.NewServiceTask("recover", activity.WithTaskAction("doRecover")))
	if o.recoveryIsUserTask {
		recovery = activity.NewUserTask("recover")
	}
	tail := event.NewEnd("endRecover")
	if o.terminateAfterRecovery {
		tail = event.NewEnd("endRecover", event.WithForceTermination("killed", event.OutcomeAbort))
	}
	return &model.ProcessDefinition{
		ID: "teardown", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("outer", body),
			event.NewBoundary("bnd", "outer", event.WithBoundaryErrorCode("E1")),
			recovery,
			event.NewEnd("end"),
			tail,
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "outer"},
			{ID: "f2", Source: "outer", Target: "end"},
			{ID: "f3", Source: "bnd", Target: "recover"},
			{ID: "f4", Source: "recover", Target: "endRecover"},
		},
	}
}

// teardownNestedDef puts the compensable saga and the throw in a NESTED scope
// ("inner"), with its own compensable work in the enclosing scope ("outer") and
// the error boundary on outer. The boundary's teardown therefore reaches the
// walk's scope through cancelScopeSubtree's DESCENDANT loop, not through the
// scope it names — the third route, on the call site ADR-0173's first draft
// asserted "names scopes this walk does not own".
//
// outer carries TWO compensable records deliberately: with one, dropping the
// predicate's `ScopeID == scopeID` conjunct is an identity and the test cannot
// discriminate (measured).
func teardownNestedDef() *model.ProcessDefinition {
	inner := &model.ProcessDefinition{
		ID: "nested-inner", Version: 1,
		Nodes: []model.Node{
			event.NewStart("innerStart"),
			activity.NewServiceTask("svcA",
				activity.WithTaskAction("doA"), activity.WithCompensateAction("undoA")),
			activity.NewServiceTask("svcB",
				activity.WithTaskAction("doB"), activity.WithCompensateAction("undoB")),
			gateway.NewParallel("innerFork"),
			event.NewCompensateThrow("innerThrow"),
			event.NewEnd("innerEndThrow"),
			activity.NewUserTask("innerSibling"),
			event.NewEnd("innerEndSibling"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "n1", Source: "innerStart", Target: "svcA"},
			{ID: "n2", Source: "svcA", Target: "svcB"},
			{ID: "n3", Source: "svcB", Target: "innerFork"},
			{ID: "n4", Source: "innerFork", Target: "innerThrow"},
			{ID: "n5", Source: "innerThrow", Target: "innerEndThrow"},
			{ID: "n6", Source: "innerFork", Target: "innerSibling"},
			{ID: "n7", Source: "innerSibling", Target: "innerEndSibling"},
		},
	}
	outerBody := &model.ProcessDefinition{
		ID: "nested-outer-body", Version: 1,
		Nodes: []model.Node{
			event.NewStart("outerStart"),
			activity.NewServiceTask("svcOuter",
				activity.WithTaskAction("doOuter"), activity.WithCompensateAction("undoOuter")),
			activity.NewServiceTask("svcOuter2",
				activity.WithTaskAction("doOuter2"), activity.WithCompensateAction("undoOuter2")),
			gateway.NewParallel("outerFork"),
			activity.NewSubProcess("inner", inner),
			event.NewEnd("outerEndInner"),
			activity.NewServiceTask("svcFails", activity.WithTaskAction("doFails")),
			event.NewEnd("outerEndSibling"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "o1", Source: "outerStart", Target: "svcOuter"},
			{ID: "o2", Source: "svcOuter", Target: "svcOuter2"},
			{ID: "o3", Source: "svcOuter2", Target: "outerFork"},
			{ID: "o4", Source: "outerFork", Target: "inner"},
			{ID: "o5", Source: "inner", Target: "outerEndInner"},
			{ID: "o6", Source: "outerFork", Target: "svcFails"},
			{ID: "o7", Source: "svcFails", Target: "outerEndSibling"},
		},
	}
	return &model.ProcessDefinition{
		ID: "teardown-nested", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("outer", outerBody),
			event.NewBoundary("bnd", "outer", event.WithBoundaryErrorCode("E1")),
			activity.NewServiceTask("recover", activity.WithTaskAction("doRecover")),
			event.NewEnd("end"),
			event.NewEnd("endRecover"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "outer"},
			{ID: "f2", Source: "outer", Target: "end"},
			{ID: "f3", Source: "bnd", Target: "recover"},
			{ID: "f4", Source: "recover", Target: "endRecover"},
		},
	}
}

// invokeTracker accumulates action-name → CommandID across every step, because a
// forked branch's action is invoked in the step that REACHED the fork, not in the
// step where the test happens to look for it. Scanning only the latest result is
// how two of these fixtures first failed on a control assertion rather than on the
// behaviour under test.
type invokeTracker map[string]string

func (tr invokeTracker) observe(cmds []engine.Command) {
	for _, c := range cmds {
		if ia, ok := c.(engine.InvokeAction); ok {
			if _, seen := tr[ia.Name]; !seen {
				tr[ia.Name] = ia.CommandID
			}
		}
	}
}

// drainWalk completes each outstanding compensation command until the walk ends,
// accumulating every compensation action the walk dispatched.
func drainWalk(t *testing.T, def *model.ProcessDefinition, res engine.StepResult,
	next func() time.Time, got *[]string,
) engine.StepResult {
	t.Helper()
	for res.State.Compensating.ActiveCmdID != "" {
		cmdID := res.State.Compensating.ActiveCmdID
		var err error
		res, err = engine.Step(t.Context(), def, res.State,
			engine.NewActionCompleted(next(), cmdID, nil), engine.StepOptions{})
		require.NoError(t, err)
		*got = append(*got, compensationInvokeNames(res.Commands)...)
	}
	return res
}

// TestTeardownMidWalkCompensatesEachRecordExactlyOnce is T1, T5, T9 and T10 — the
// four scope-wide routes, each driven to a LATER walk and asserted on what that
// later walk re-dispatches.
//
// What makes each row fail on main (measured):
//   - "boundary teardown, later cancel": re-dispatched=[undoB undoA] — both records
//     run a second time.
//   - "sibling record appended mid-walk": the sibling's undoS is archived alongside
//     the drained prefix, so the later walk runs [undoB undoA undoS] instead of the
//     one genuinely-uncompensated record.
//   - "ancestor teardown reaches the walk's scope": re-dispatched=[undoB undoA
//     undoOuter2 undoOuter] — two double-runs, through cancelScopeSubtree's
//     descendant loop.
//   - "unhandled error re-enters the archive": the error path finds the leaked
//     archive and re-runs [undoB undoA]. This is the only AUTOMATIC re-entry route;
//     the other two need an operator.
func TestTeardownMidWalkCompensatesEachRecordExactlyOnce(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		def    *model.ProcessDefinition
		saga   []string
		nested bool
		// siblingAction, when set, is completed after the walk starts — it is the
		// sibling's own compensable task, whose record lands in the scope MID-WALK.
		siblingAction string

		// reenter drives whatever comes AFTER the walk drains, and returns the
		// compensation actions that second walk dispatches.
		reenter func(t *testing.T, def *model.ProcessDefinition, res engine.StepResult, next func() time.Time, tr invokeTracker) []string
		want    []string
		wantMsg string
	}

	cancelReentry := func(t *testing.T, def *model.ProcessDefinition, res engine.StepResult, next func() time.Time, _ invokeTracker) []string {
		t.Helper()
		res, err := engine.Step(t.Context(), def, res.State,
			engine.NewCancelRequested(next()), engine.StepOptions{})
		require.NoError(t, err)
		got := compensationInvokeNames(res.Commands)
		drainWalk(t, def, res, next, &got)
		return got
	}
	errorReentry := func(t *testing.T, def *model.ProcessDefinition, res engine.StepResult, next func() time.Time, tr invokeTracker) []string {
		t.Helper()
		tr.observe(res.Commands)
		recoverCmd := tr["doRecover"]
		require.NotEmpty(t, recoverCmd, "control: the boundary's recovery action is in flight")
		// An error code no boundary catches → handleUnhandledError, which consults
		// ArchivedCompensations and runs a compensation walk before terminating.
		res, err := engine.Step(t.Context(), def, res.State,
			engine.NewActionFailed(next(), recoverCmd, "UNCAUGHT", false), engine.StepOptions{})
		require.NoError(t, err)
		got := compensationInvokeNames(res.Commands)
		drainWalk(t, def, res, next, &got)
		return got
	}

	cases := []testCase{
		{
			name: "boundary teardown, later cancel",
			def:  teardownDef(teardownBodyOpts{saga: []string{"A", "B"}}),
			saga: []string{"doA", "doB"}, reenter: cancelReentry,
			want:    nil,
			wantMsg: "every record the walk drained is compensated exactly once",
		},
		{
			name: "sibling record appended mid-walk survives the teardown",
			def:  teardownDef(teardownBodyOpts{saga: []string{"A", "B"}, siblingSaga: "S"}),
			saga: []string{"doA", "doB"}, siblingAction: "doS", reenter: cancelReentry,
			want:    []string{"undoS"},
			wantMsg: "the sibling's record is genuinely uncompensated and must survive; the drained prefix must not",
		},
		{
			name: "ancestor teardown reaches the walk's scope via the descendant loop",
			def:  teardownNestedDef(), nested: true,
			saga: []string{"doOuter", "doOuter2", "doA", "doB"}, reenter: cancelReentry,
			want:    []string{"undoOuter2", "undoOuter"},
			wantMsg: "only the ENCLOSING scope's records are still owed; the nested walk's are not",
		},
		{
			name: "unhandled error re-enters the archive",
			def:  teardownDef(teardownBodyOpts{saga: []string{"A", "B"}}),
			saga: []string{"doA", "doB"}, reenter: errorReentry,
			want:    nil,
			wantMsg: "the only AUTOMATIC re-entry route must re-run nothing either",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			at := scopeDrainT0
			next := func() time.Time { at = at.Add(time.Second); return at }

			res, err := engine.Step(ctx, tc.def, engine.InstanceState{InstanceID: "td"},
				engine.NewStartInstance(scopeDrainT0, nil), engine.StepOptions{})
			require.NoError(t, err)
			tr := invokeTracker{}
			tr.observe(res.Commands)

			var walkDispatched []string
			for _, action := range tc.saga {
				cmdID := tr[action]
				require.NotEmpty(t, cmdID, "control: %q must have been invoked", action)
				res, err = engine.Step(ctx, tc.def, res.State,
					engine.NewActionCompleted(next(), cmdID, nil), engine.StepOptions{})
				require.NoError(t, err)
				tr.observe(res.Commands)
				walkDispatched = append(walkDispatched, compensationInvokeNames(res.Commands)...)
			}
			walkCmd := res.State.Compensating.ActiveCmdID
			require.NotEmpty(t, walkCmd, "control: the scope-wide throw walk must be in flight")

			if tc.nested {
				// The nested walk's sibling parks on a user task; leave it parked so
				// the inner scope stays alive until the ancestor teardown.
				require.NotNil(t, firstAwaitHuman(res.Commands),
					"control: the nested sibling parks, keeping the inner scope open")
			}

			if tc.siblingAction != "" {
				sibCmd := tr[tc.siblingAction]
				require.NotEmpty(t, sibCmd, "control: %q must have been invoked", tc.siblingAction)
				res, err = engine.Step(ctx, tc.def, res.State,
					engine.NewActionCompleted(next(), sibCmd, nil), engine.StepOptions{})
				require.NoError(t, err)
				tr.observe(res.Commands)
				require.Equal(t, walkCmd, res.State.Compensating.ActiveCmdID,
					"control: the sibling's record landed mid-walk without disturbing the walk")
			}

			failCmd := tr["doFails"]
			require.NotEmpty(t, failCmd, "control: the failing sibling is in flight")
			res, err = engine.Step(ctx, tc.def, res.State,
				engine.NewActionFailed(next(), failCmd, "E1", false), engine.StepOptions{})
			require.NoError(t, err)
			tr.observe(res.Commands)
			require.Equal(t, engine.StatusCompensating, res.State.Status,
				"control: the teardown must not complete the instance over the outstanding walk")

			res = drainWalk(t, tc.def, res, next, &walkDispatched)
			require.NotEmpty(t, walkDispatched, "control: the walk dispatched something")

			got := tc.reenter(t, tc.def, res, next, tr)
			assert.Equal(t, tc.want, got, tc.wantMsg+
				"\n(the first walk had dispatched %v)", walkDispatched)
		})
	}
}

// TestTeardownMidWalkLeavesTheRemainderWhenTheWalkIsABANDONED is T4 and T12.
//
// The walk's records are dropped from the archive as it DISPATCHES them, not at
// its finish, so a walk abandoned in between leaves exactly what it never ran.
// A force-termination end event (ADR-0119) is the measured abandonment route:
// forceTerminate deliberately runs no compensation, and endInstance clears the
// cursor.
//
// ⚠ The "advances into the head first" row is the one that refuted ADR-0173's
// first design, which removed the window only at the finish. On main it yields
// [undoC undoB undoA]; on that first design [undoB undoA] — undoB twice, because
// the walk dispatched it AND left it archived.
func TestTeardownMidWalkLeavesTheRemainderWhenTheWalkIsABANDONED(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name          string
		saga          []string
		advanceBefore int // walk advances taken BEFORE the abandonment
		want          []string
	}

	cases := []testCase{
		{
			name: "abandoned immediately after the teardown",
			saga: []string{"A", "B"}, advanceBefore: 0,
			want: []string{"undoA"},
		},
		{
			name: "abandoned after advancing INTO the archived head",
			saga: []string{"A", "B", "C"}, advanceBefore: 1,
			want: []string{"undoA"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			def := teardownDef(teardownBodyOpts{
				saga: tc.saga, terminateAfterRecovery: true, recoveryIsUserTask: true,
			})
			at := scopeDrainT0
			next := func() time.Time { at = at.Add(time.Second); return at }

			res, err := engine.Step(ctx, def, engine.InstanceState{InstanceID: "ab"},
				engine.NewStartInstance(scopeDrainT0, nil), engine.StepOptions{})
			require.NoError(t, err)
			var walkDispatched []string
			for _, name := range tc.saga {
				cmdID := firstInvokeCmd(res.Commands, "do"+name)
				require.NotEmpty(t, cmdID, "control: do%s invoked", name)
				res, err = engine.Step(ctx, def, res.State,
					engine.NewActionCompleted(next(), cmdID, nil), engine.StepOptions{})
				require.NoError(t, err)
				walkDispatched = append(walkDispatched, compensationInvokeNames(res.Commands)...)
			}
			require.NotEmpty(t, res.State.Compensating.ActiveCmdID, "control: walk in flight")

			failCmd := firstInvokeCmd(res.Commands, "doFails")
			require.NotEmpty(t, failCmd)
			res, err = engine.Step(ctx, def, res.State,
				engine.NewActionFailed(next(), failCmd, "E1", false), engine.StepOptions{})
			require.NoError(t, err)
			human := firstAwaitHuman(res.Commands)
			require.NotNil(t, human, "control: the recovery branch parks so the walk can advance first")

			for i := 0; i < tc.advanceBefore; i++ {
				require.NotEmpty(t, res.State.Compensating.ActiveCmdID,
					"control: advance %d needs a live walk", i)
				res, err = engine.Step(ctx, def, res.State,
					engine.NewActionCompleted(next(), res.State.Compensating.ActiveCmdID, nil),
					engine.StepOptions{})
				require.NoError(t, err)
				walkDispatched = append(walkDispatched, compensationInvokeNames(res.Commands)...)
			}

			// Abandon: the recovery task completes into the force-termination end.
			res, err = engine.Step(ctx, def, res.State,
				engine.NewHumanCompleted(next(), human.TaskID, engine.CompletionInput{}, authz.Actor{ID: "u1"}),
				engine.StepOptions{})
			require.NoError(t, err)
			require.Equal(t, engine.StatusTerminated, res.State.Status,
				"control: the force-termination end event abandoned the walk")
			require.Empty(t, res.State.Compensating.ActiveCmdID, "control: endInstance cleared the cursor")

			// An admin rollback is the operator's recovery for what the walk never ran.
			res2, err := engine.Step(ctx, def, res.State,
				engine.NewCompensateRequested(next(), ""), engine.StepOptions{})
			require.NoError(t, err,
				"the records the walk never dispatched must stay compensable")
			got := compensationInvokeNames(res2.Commands)
			drainWalk(t, def, res2, next, &got)

			assert.Equal(t, tc.want, got,
				"the admin rollback runs exactly what the walk never dispatched"+
					"\n(the walk had dispatched %v)", walkDispatched)
		})
	}
}

// TestTeardownMidWalkLeavesAPreADR0171CursorAlone is T11 — the audit's Critical.
//
// A cursor persisted before ADR-0171 carries no pinned snapshot, so it CANNOT
// dispatch the head a teardown would park: cursorRecords falls back to a live read
// the teardown just nilled, and stepCompensationAdvance's bounds check routes the
// walk straight to its finish. Partitioning on its behalf would delete records
// nobody ever runs — the exact loss ADR-0173 rejects its simpler alternative for.
//
// So such a walk is left ENTIRELY alone and keeps main's behaviour, double-run
// included. That is a deliberate bound (spec §7.3), not an oversight, and this
// test exists so nobody closes it by widening the predicate.
//
// It goes RED the moment `len(cur.Records) > 0` is dropped from
// scopeWideWalkDraining: the head is then parked and consumed, and the admin
// rollback recovers nothing.
func TestTeardownMidWalkLeavesAPreADR0171CursorAlone(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	def := teardownDef(teardownBodyOpts{saga: []string{"A", "B"}})
	at := scopeDrainT0
	next := func() time.Time { at = at.Add(time.Second); return at }

	res, err := engine.Step(ctx, def, engine.InstanceState{InstanceID: "pre171"},
		engine.NewStartInstance(scopeDrainT0, nil), engine.StepOptions{})
	require.NoError(t, err)
	var dispatched []string
	for _, action := range []string{"doA", "doB"} {
		cmdID := firstInvokeCmd(res.Commands, action)
		require.NotEmpty(t, cmdID)
		res, err = engine.Step(ctx, def, res.State,
			engine.NewActionCompleted(next(), cmdID, nil), engine.StepOptions{})
		require.NoError(t, err)
		dispatched = append(dispatched, compensationInvokeNames(res.Commands)...)
	}
	require.Equal(t, []string{"undoB"}, dispatched, "control: the walk dispatched its first record")

	// The round-trip through a row written before ADR-0171: no pinned Records.
	// ADR-0171's own bounds-check comment names this state as reachable after a
	// process restart.
	st := res.State
	st.Compensating.Records = nil
	require.NotEmpty(t, st.Compensating.ScopeID, "control: the cursor still names its scope")

	failCmd := firstInvokeCmd(res.Commands, "doFails")
	require.NotEmpty(t, failCmd)
	res, err = engine.Step(ctx, def, st,
		engine.NewActionFailed(next(), failCmd, "E1", false), engine.StepOptions{})
	require.NoError(t, err)

	assert.Empty(t, res.State.Compensating.TeardownArchiveKey,
		"no window may be stamped for a walk that cannot dispatch its head")
	require.Len(t, res.State.ArchivedCompensations["outer"], 2,
		"the teardown archives the WHOLE list, exactly as on main")

	res = drainWalk(t, def, res, next, &dispatched)
	assert.Equal(t, []string{"undoB"}, dispatched,
		"control: a walk with no snapshot dispatches nothing further — it finishes on the bounds check")

	res, err = engine.Step(ctx, def, res.State, engine.NewCancelRequested(next()), engine.StepOptions{})
	require.NoError(t, err)
	var recovered []string
	recovered = append(recovered, compensationInvokeNames(res.Commands)...)
	drainWalk(t, def, res, next, &recovered)
	assert.Equal(t, []string{"undoB", "undoA"}, recovered,
		"identical to main: undoA is recovered, and undoB's second run is the "+
			"pre-existing defect this delivery deliberately does NOT close for legacy cursors")
}

// TestNestedEventSubprocessTeardownLeavesNothingToRollBack is T2: the SECOND
// non-deferrable teardown route. A nested interrupting event sub-process closes
// the enclosing scope through exitNestedEventSubprocessScope, which consults no
// hold, and the instance then reaches a terminal state.
//
// On main the walk's already-dispatched record survives in the archive onto the
// COMPLETED instance, and an admin rollback is admitted precisely BECAUSE it does
// — hasCompensationRecordsToWalk sees it (ADR-0164 carve-out #1) — and re-runs
// undoA. Measured there: `archive={outer=[undoA]}`, `re-dispatched=[undoA]`.
//
// With ownership settled there is genuinely nothing left, so the same rollback is
// refused rather than silently double-compensating.
func TestNestedEventSubprocessTeardownLeavesNothingToRollBack(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	def := interruptingESPTeardownDef()
	at := scopeDrainT0
	next := func() time.Time { at = at.Add(time.Second); return at }

	res, err := engine.Step(ctx, def, engine.InstanceState{InstanceID: "espown"},
		engine.NewStartInstance(scopeDrainT0, nil), engine.StepOptions{})
	require.NoError(t, err)
	doA := firstInvokeCmd(res.Commands, "doA")
	require.NotEmpty(t, doA, "control: the saga task is invoked")
	res, err = engine.Step(ctx, def, res.State,
		engine.NewActionCompleted(next(), doA, nil), engine.StepOptions{})
	require.NoError(t, err)
	walkCmd := res.State.Compensating.ActiveCmdID
	require.NotEmpty(t, walkCmd, "control: the scope-wide throw walk is in flight")
	require.Equal(t, []string{"undoA"}, compensationInvokeNames(res.Commands),
		"control: the walk dispatched its only record")

	// The interrupting event sub-process fires, then drains — closing the walk's
	// enclosing scope through the call site that consults no hold.
	res, err = engine.Step(ctx, def, res.State,
		engine.NewSignalReceived(next(), "boom", nil), engine.StepOptions{})
	require.NoError(t, err)
	espTask := humanTaskIDForNode(t, res.State, "espTask")
	res, err = engine.Step(ctx, def, res.State,
		engine.NewHumanCompleted(next(), espTask, engine.CompletionInput{}, authz.Actor{ID: "u1"}),
		engine.StepOptions{})
	require.NoError(t, err)
	require.Empty(t, res.State.Scopes, "control: the event sub-process exit closed the walk's scope")

	var dispatched []string
	res = drainWalk(t, def, res, next, &dispatched)
	require.Equal(t, engine.StatusCompleted, res.State.Status,
		"control: the instance reaches a terminal state (ADR-0171's dropped-resume recovery)")
	assert.Empty(t, dispatched, "control: the walk had nothing further to dispatch")
	assert.Nil(t, res.State.ArchivedCompensations,
		"the record the walk already ran must not survive onto the terminal instance")

	_, err = engine.Step(ctx, def, res.State,
		engine.NewCompensateRequested(next(), ""), engine.StepOptions{})
	require.Error(t, err, "an admin rollback must be refused, not silently re-run undoA")
	assert.ErrorIs(t, err, engine.ErrInstanceTerminal)
	assert.Contains(t, err.Error(), "nothing left to compensate")
}

// TestCancelArrivingMidWalkAfterATeardownRunsEachRecordOnce covers applyFinish's
// consumePendingCancel re-entry, the one finish path that does NOT terminate at
// the plan it built: a cancel delivered while the walk is still in flight sets
// PendingCancel, and the finish then re-enters beginCompensation over whatever
// records REMAIN, instead of resuming.
//
// That re-entry is the sharpest test of ownership, because it runs a second walk
// over the same state microseconds after the first one ended. On main the
// teardown has left the first walk's records in the archive, so the re-entered
// walk consolidates and re-runs them: [undoB undoA] a second time.
func TestCancelArrivingMidWalkAfterATeardownRunsEachRecordOnce(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	def := teardownDef(teardownBodyOpts{saga: []string{"A", "B"}})
	at := scopeDrainT0
	next := func() time.Time { at = at.Add(time.Second); return at }

	res, err := engine.Step(ctx, def, engine.InstanceState{InstanceID: "midcancel"},
		engine.NewStartInstance(scopeDrainT0, nil), engine.StepOptions{})
	require.NoError(t, err)
	tr := invokeTracker{}
	tr.observe(res.Commands)

	var dispatched []string
	for _, action := range []string{"doA", "doB"} {
		res, err = engine.Step(ctx, def, res.State,
			engine.NewActionCompleted(next(), tr[action], nil), engine.StepOptions{})
		require.NoError(t, err)
		tr.observe(res.Commands)
		dispatched = append(dispatched, compensationInvokeNames(res.Commands)...)
	}
	require.Equal(t, []string{"undoB"}, dispatched, "control: the walk dispatched its first record")

	// Tear the scope down under the live walk.
	res, err = engine.Step(ctx, def, res.State,
		engine.NewActionFailed(next(), tr["doFails"], "E1", false), engine.StepOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, res.State.Compensating.ActiveCmdID, "control: the walk survived the teardown")
	require.NotEmpty(t, res.State.Compensating.TeardownArchiveKey,
		"control: the teardown parked the un-dispatched head under a window")

	// Cancel MID-WALK: deferred, not applied (ADR-0039's PendingCancel protocol).
	res, err = engine.Step(ctx, def, res.State,
		engine.NewCancelRequested(next()), engine.StepOptions{})
	require.NoError(t, err)
	require.True(t, res.State.PendingCancel, "control: the cancel is deferred behind the walk")
	require.NotEmpty(t, res.State.Compensating.ActiveCmdID, "control: the walk is still in flight")

	// Drain the walk. Its finish takes the consumePendingCancel branch and
	// re-enters beginCompensation over the REMAINDER.
	res = drainWalk(t, def, res, next, &dispatched)

	assert.Equal(t, []string{"undoB", "undoA"}, dispatched,
		"each record is compensated exactly once across BOTH walks")
	assert.Equal(t, engine.StatusTerminated, res.State.Status,
		"the deferred cancel is applied once the walk drains")
	assert.Nil(t, res.State.ArchivedCompensations,
		"the re-entered walk must find nothing left to re-run")
}

// TestFinishToleratesACorruptTeardownWindow drives the REAL route for the window
// normalization: a persisted cursor carrying a window count with no key.
//
// InstanceState round-trips as whole-struct JSON and `Compensating` is an
// exported field, so that pair is representable in a stored row even though
// archiveCompensations can never produce it (it stamps key and count together).
// Before the normalization, reaching the finish with it panicked in the pure
// engine core — `finishPlan invariant violated: archiveWindowCount without
// archiveWindowKey` — i.e. in the CONSUMER's process, since this ships as a
// library. `main` does not panic on the same input, so it was a regression.
//
// ⚠ This test exists because a unit test calling normalizeTeardownWindow directly
// did NOT discriminate: deleting its call site left the suite green. The mutation
// has to be able to see the wiring, not just the helper.
func TestFinishToleratesACorruptTeardownWindow(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	def := teardownDef(teardownBodyOpts{saga: []string{"A", "B"}})
	at := scopeDrainT0
	next := func() time.Time { at = at.Add(time.Second); return at }

	res, err := engine.Step(ctx, def, engine.InstanceState{InstanceID: "corruptwin"},
		engine.NewStartInstance(scopeDrainT0, nil), engine.StepOptions{})
	require.NoError(t, err)
	tr := invokeTracker{}
	tr.observe(res.Commands)
	for _, action := range []string{"doA", "doB"} {
		res, err = engine.Step(ctx, def, res.State,
			engine.NewActionCompleted(next(), tr[action], nil), engine.StepOptions{})
		require.NoError(t, err)
		tr.observe(res.Commands)
	}
	res, err = engine.Step(ctx, def, res.State,
		engine.NewActionFailed(next(), tr["doFails"], "E1", false), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, "outer", res.State.Compensating.TeardownArchiveKey,
		"control: the teardown stamped a window")
	require.Positive(t, res.State.Compensating.TeardownArchiveCount,
		"control: the window is non-empty, so clearing the key leaves the corrupt pair")

	// The corrupt row: a count that addresses no slot.
	st := res.State
	st.Compensating.TeardownArchiveKey = ""

	require.NotPanics(t, func() {
		for st.Compensating.ActiveCmdID != "" {
			r, stepErr := engine.Step(ctx, def, st,
				engine.NewActionCompleted(next(), st.Compensating.ActiveCmdID, nil),
				engine.StepOptions{})
			require.NoError(t, stepErr)
			st = r.State
		}
	}, "a corrupt persisted cursor must not panic the pure engine core")
}

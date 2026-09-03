package engine_test

// A dying instance ignores a fired timer.
//
// handleTimerFired's path 4 (the s.Timers record switch) was the only one of its
// five paths with no !spawnsNewWork() guard, and it is the only path whose
// timers carry a record. Measured against the pre-guard build with this fixture:
// the reminder dispatched InvokeAction{remind}, the retry re-dispatched
// InvokeAction{work} (NOT fire-and-forget), and the deadline dispatched
// InvokeAction{notify}, cancelled the open human task and CONSUMED the token
// (tokens 2 → 1) — advancing a dying instance's live branch to an end event.
//
// ⚠ The fixture's dying-ness is ASSERTED, never assumed. A compensation walk is
// NOT dying by definition: the last row below measures the SAME mid-walk state
// with spawnsNewWork() == TRUE, so a fixture chosen for convenience rather than
// for this property yields a test that passes whether or not the guard exists.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/gateway"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
)

// dyingTimerDef builds the one definition that can hold all three token-hung
// timer records on an instance the engine has decided is dying:
//
//	start → fork ┬─ userTask  (reminder Every 1h "remind", deadline After 3h "notify")
//	             ├─ svc       (action "work", retry policy)
//	             └─ compThrow (targeted, ref "ref1") → afterThrowEnd
//
// The throw branch starts a compensation walk that RESUMES, so the sibling
// branches' tokens and timer records survive it. A cancel-STARTED walk cannot
// hold these three kinds at all: beginCompensation's prologue cancels every
// token and sweeps s.Timers — measured, a cancel delivered to an instance parked
// on a user task with a reminder and a deadline leaves tokens=0, records=0.
// A CancelRequested arriving mid-walk is then DEFERRED (PendingCancel = true),
// which is what flips walkTerminates and makes spawnsNewWork() false.
func dyingTimerDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-dying-timers", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewParallel("fork"),
			activity.NewUserTask("userTask",
				activity.WithEligibleRoles("manager"),
				activity.WithWaitDeadline(schedule.AfterExpr(`"3h"`), "escalate"),
				activity.WithDeadlineAction("notify"),
				activity.WithWaitAction(schedule.EveryExpr(`"1h"`), "remind")),
			event.NewEnd("userEnd"),
			event.NewEnd("escalateEnd"),
			activity.NewServiceTask("svc",
				activity.WithTaskAction("work"),
				activity.WithRetryPolicy(&model.RetryPolicy{MaxAttempts: 3, InitialInterval: time.Minute})),
			event.NewEnd("svcEnd"),
			event.NewCompensateThrow("compThrow", event.WithCompensateRef("ref1")),
			event.NewEnd("afterThrowEnd"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f-start-fork", Source: "start", Target: "fork"},
			{ID: "f-fork-user", Source: "fork", Target: "userTask"},
			{ID: "f-fork-svc", Source: "fork", Target: "svc"},
			{ID: "f-fork-throw", Source: "fork", Target: "compThrow"},
			{ID: "f-user-end", Source: "userTask", Target: "userEnd"},
			{ID: "escalate", Source: "userTask", Target: "escalateEnd"},
			{ID: "f-svc-end", Source: "svc", Target: "svcEnd"},
			{ID: "f-throw-end", Source: "compThrow", Target: "afterThrowEnd"},
		},
	}
}

// instanceWithArmedTimers drives dyingTimerDef to a compensating instance
// holding a live TimerInWait, TimerDeadline and TimerRetry record.
//
// deferCancel selects the ONLY axis that matters here: with it, a cancel is
// deferred behind the in-flight walk and spawnsNewWork() is false (the walk will
// terminate); without it, the same walk will RESUME and spawnsNewWork() is true.
// Both states have Status == StatusCompensating, which is what makes the pair
// discriminate the guard's predicate from a status test.
func instanceWithArmedTimers(t *testing.T, deferCancel bool) engine.InstanceState {
	t.Helper()
	def := dyingTimerDef()
	at := time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC)
	seed := engine.InstanceState{
		InstanceID: "dying-1",
		ArchivedCompensations: map[string][]engine.CompensationRecord{
			"ref1": {{NodeID: "archived", Action: "undo1"}},
		},
	}

	r1, err := engine.Step(t.Context(), def, seed, engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompensating, r1.State.Status,
		"fixture: the targeted throw must start a compensation walk")
	require.NotNil(t, invokeActionNamed(r1.Commands, "undo1"),
		"fixture: the walk must dispatch its compensation action and stay in flight")
	work := invokeActionNamed(r1.Commands, "work")
	require.NotNil(t, work, "fixture: the service branch must dispatch its action")

	// A retryable failure parks the service token on a TimerRetry record.
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewActionFailed(at.Add(time.Minute), work.CommandID, "boom", true), engine.StepOptions{})
	require.NoError(t, err)

	if !deferCancel {
		return r2.State
	}

	// A cancel arriving while a RESUMING walk is in flight is deferred, which is
	// what makes the instance dying without killing its branches.
	r3, err := engine.Step(t.Context(), def, r2.State,
		engine.NewCancelRequested(at.Add(2*time.Minute)), engine.StepOptions{})
	require.NoError(t, err)
	require.True(t, engine.PendingCancelOf(&r3.State), "fixture: the cancel must be DEFERRED, not applied")

	return r3.State
}

// timerRecordOfKind returns the single record of the given kind, failing when
// the fixture does not hold exactly one.
func timerRecordOfKind(t *testing.T, s engine.InstanceState, kind engine.TimerKind) engine.TimerRecordView {
	t.Helper()
	var found []engine.TimerRecordView
	for _, tr := range engine.TimerRecords(&s) {
		if tr.Kind == kind {
			found = append(found, tr)
		}
	}
	require.Len(t, found, 1, "fixture: expected exactly one %v record, got %v", kind, engine.TimerRecords(&s))
	return found[0]
}

// assertRefused holds for every refused fire, whatever the kind: the ONLY
// command is the CancelTimer that disarms the scheduler job, no token moves, and
// the record is retired rather than left armed.
//
// ⚠ The CancelTimer is load-bearing, not cosmetic. Retiring the record is
// exactly what stops the later terminal sweep (endInstance → cancelAllTimers)
// from ever emitting it, and a TimerInWait reminder is armed with a RECURRING
// trigger — so a refusal that emitted nothing left the scheduler job firing
// forever against a terminated instance. Measured on this fixture before the
// fix: firing tm2 once while dying left the terminal sweep emitting
// cancelTimers=[tm1 tm3], versus [tm1 tm2 tm3] when it never fired.
func assertRefused(t *testing.T, before engine.InstanceState, rec engine.TimerRecordView, res engine.StepResult) {
	t.Helper()
	assert.Equal(t, []engine.Command{engine.CancelTimer{TimerID: rec.TimerID}}, res.Commands,
		"a dying instance must be given no WORK by a fired timer — but the refusal "+
			"retires the record, so it must itself disarm the scheduler job")
	assert.Len(t, res.State.Tokens, len(before.Tokens), "a refused fire moves no token")
	for _, tr := range engine.TimerRecords(&res.State) {
		assert.NotEqual(t, rec.TimerID, tr.TimerID,
			"a refused fire RETIRES the record rather than leaving it armed")
	}
}

// TestFiredTimerOnDyingInstanceOnlyDisarmsItsTimer covers the guard: each
// of the three token-hung path-4 kinds is refused on a dying instance, its
// record retired, and the ONLY command emitted is the CancelTimer disarming the
// scheduler job it just orphaned.
//
// The last row is the control that discriminates the predicate. It fires the
// same reminder on the same fixture MINUS the deferred cancel: still
// StatusCompensating, still mid-walk, but the walk will resume, so
// spawnsNewWork() is true and the reminder must still be dispatched. A guard
// written against the status rather than against spawnsNewWork() passes every
// other row here and fails this one.
func TestFiredTimerOnDyingInstanceOnlyDisarmsItsTimer(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name string
		// dying selects the fixture: true defers a cancel behind the in-flight
		// walk (spawnsNewWork false), false leaves the walk resuming.
		dying  bool
		kind   engine.TimerKind
		assert func(t *testing.T, before engine.InstanceState, rec engine.TimerRecordView, res engine.StepResult)
	}

	cases := []testCase{
		{
			name:  "in-wait reminder dispatches no reminder action",
			dying: true,
			kind:  engine.TimerInWait,
			assert: func(t *testing.T, before engine.InstanceState, rec engine.TimerRecordView, res engine.StepResult) {
				assertRefused(t, before, rec, res)
				assert.Nil(t, invokeActionNamed(res.Commands, "remind"))
			},
		},
		{
			name:  "deadline neither advances the token nor cancels the task",
			dying: true,
			kind:  engine.TimerDeadline,
			assert: func(t *testing.T, before engine.InstanceState, rec engine.TimerRecordView, res engine.StepResult) {
				assertRefused(t, before, rec, res)
				for _, tok := range res.State.Tokens {
					assert.NotEqual(t, "escalateEnd", tok.NodeID,
						"no token may be routed down the deadline flow")
				}
				require.Len(t, res.State.Tasks, 1)
				assert.True(t, res.State.Tasks[0].IsOpen(),
					"the open human task must not be cancelled by a refused deadline")
			},
		},
		{
			name:  "retry re-invokes the service action on nothing",
			dying: true,
			kind:  engine.TimerRetry,
			assert: func(t *testing.T, before engine.InstanceState, rec engine.TimerRecordView, res engine.StepResult) {
				assertRefused(t, before, rec, res)
				assert.Nil(t, invokeActionNamed(res.Commands, "work"),
					"the retry is the path whose InvokeAction is NOT fire-and-forget: "+
						"its ActionCompleted would land on a terminated instance")
			},
		},
		{
			name:  "control: a resuming walk still gets its reminder",
			dying: false,
			kind:  engine.TimerInWait,
			assert: func(t *testing.T, _ engine.InstanceState, rec engine.TimerRecordView, res engine.StepResult) {
				assert.NotNil(t, invokeActionNamed(res.Commands, "remind"),
					"a walk that will RESUME is a live instance: refusing here would "+
						"silence every reminder during a compensation throw")
				var kept bool
				for _, tr := range engine.TimerRecords(&res.State) {
					kept = kept || tr.TimerID == rec.TimerID
				}
				assert.True(t, kept, "a recurring reminder keeps its record so the next fire finds it")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			state := instanceWithArmedTimers(t, tc.dying)
			require.Equal(t, engine.StatusCompensating, state.Status,
				"fixture precondition: both rows are mid-walk, so status cannot tell them apart")
			require.Equal(t, !tc.dying, engine.SpawnsNewWork(&state),
				"fixture precondition: spawnsNewWork is the axis under test")
			rec := timerRecordOfKind(t, state, tc.kind)

			res, err := engine.Step(t.Context(), dyingTimerDef(), state,
				engine.NewTimerFired(time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC), rec.TimerID),
				engine.StepOptions{})
			require.NoError(t, err)

			tc.assert(t, state, rec, res)
		})
	}
}

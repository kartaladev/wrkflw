package engine_test

// step_compensation_walk_timer_retirement_test.go — ADR-0179 Decision 3 /
// spec §3.3: the walk-scoped timer sweep must cover the RETRY kind, not only
// the stall kind.
//
// Until this, the sweep (then named cancelCompensationStallTimers, now
// cancelCompensationWalkTimers) filtered strictly on TimerCompensationStall, so
// a retry record was invisible to it. Measured on a
// two-record RESUMING (compensation-throw) walk whose records both failed under
// a retry policy and were then answered late:
//
//	AFTER RESUME FINISH: status=running
//	leakedRecords=[TimerCompensationRetry/probe-1-tm1/probe-1-c3
//	               TimerCompensationRetry/probe-1-tm2/probe-1-c4]
//	cmds=[engine.AwaitHuman{…}]              ← not one CancelTimer
//	leakedTimerRecords=2
//
// stepCompensationFinish has already zeroed the cursor by then, so each orphan
// later fires against compensationCursor{} — the shape ADR-0171 documents as
// having panicked in the pure core, in a consumer's process — and its scheduler
// job outlives the walk regardless.
//
// ⚠ THE FIXTURE MUST BE A RESUMING WALK. On a TERMINATE finish endInstance's
// cancelAllTimers sweeps every record regardless of kind: the same drive on a
// cancel-started walk measures `leakedRecords=[] leakedTimerRecords=0
// cmds=[FailInstance CancelTimer{tm1}]` with this fix ABSENT, so a cancel-walk
// fixture cannot fail (plan trap 6).

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

// compensationRetryOn is the opt-in that makes a failed compensation record
// re-dispatch after a backoff instead of being skipped.
func compensationRetryOn() engine.StepOptions {
	return engine.StepOptions{
		CompensationRetryPolicy: &model.RetryPolicy{MaxAttempts: 5, InitialInterval: time.Second},
	}
}

// compensationRetryAndStallOn adds stall detection to the retry opt-in, so a
// walk-scoped record is live at the moment a sweep runs.
func compensationRetryAndStallOn() engine.StepOptions {
	opt := compensationRetryOn()
	opt.CompensationStallAfter = 30 * time.Minute
	return opt
}

// twoRecordCompensableThrowDef is
//
//	start → sub(inner-start → i1(comp "u1") → i2(comp "u2") → inner-end)
//	      → compThrow(CompensateRef:"sub") → afterThrow(user task) → end
//
// Two compensable records inside the sub-process, so the throw walk dispatches
// u2 then u1 and each can be failed independently. The throw RESUMES at
// afterThrow, which is what makes the finish a resume rather than a terminate —
// the only finish on which a leaked walk-scoped record is observable.
func twoRecordCompensableThrowDef() *model.ProcessDefinition {
	nested := &model.ProcessDefinition{
		ID: "retire-nested", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			activity.NewServiceTask("i1", activity.WithTaskAction("do-i1"), activity.WithCompensateAction("u1")),
			activity.NewServiceTask("i2", activity.WithTaskAction("do-i2"), activity.WithCompensateAction("u2")),
			event.NewEnd("inner-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "i1"},
			{ID: "if2", Source: "i1", Target: "i2"},
			{ID: "if3", Source: "i2", Target: "inner-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "retire-throw", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("sub", nested),
			event.NewCompensateThrow("compThrow", event.WithCompensateRef("sub")),
			activity.NewUserTask("afterThrow"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "sub"},
			{ID: "f2", Source: "sub", Target: "compThrow"},
			{ID: "f3", Source: "compThrow", Target: "afterThrow"},
			{ID: "f4", Source: "afterThrow", Target: "end"},
		},
	}
}

// driveToThrowWalkWithFailedFirstRecord drives twoRecordCompensableThrowDef to a
// RESUMING compensation walk whose first dispatched record (u2) has failed under
// a retry policy, leaving a TimerCompensationRetry armed. It returns the state
// and the failed command id.
//
// Every require is a control: without them the assertions below could pass over
// a state that never armed a backoff, or never reached a resuming walk at all.
func driveToThrowWalkWithFailedFirstRecord(t *testing.T, at time.Time) (engine.InstanceState, string) {
	t.Helper()
	def := twoRecordCompensableThrowDef()
	opt := compensationRetryOn()

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i-retire"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewActionCompleted(at.Add(time.Second), invokeIDForAction(r1.Commands, "do-i1"), nil),
		engine.StepOptions{})
	require.NoError(t, err)
	// Completing i2 exits the sub-process (archiving both records) and reaches the
	// throw, which starts the walk.
	r3, err := engine.Step(t.Context(), def, r2.State,
		engine.NewActionCompleted(at.Add(2*time.Second), invokeIDForAction(r2.Commands, "do-i2"), nil), opt)
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompensating, r3.State.Status,
		"control: the throw must have started a walk")
	require.Equal(t, "walkThrowTargeted", engine.WalkModeName(&r3.State),
		"control: a RESUMING walk — a terminating one cannot fail this test (trap 6)")

	u2 := invokeActionNamed(r3.Commands, "u2")
	require.NotNil(t, u2, "control: reverse order dispatches u2 first")

	r4, err := engine.Step(t.Context(), def, r3.State,
		engine.NewActionFailed(at.Add(3*time.Second), u2.CommandID, "u2 blew up", true), opt)
	require.NoError(t, err)
	require.Len(t, retryTimerRecords(r4.State), 1,
		"control: the fixture must actually arm a retry backoff")

	return r4.State, u2.CommandID
}

// retryTimerRecords returns only the TimerCompensationRetry records in s.
func retryTimerRecords(s engine.InstanceState) []engine.TimerRecordView {
	var out []engine.TimerRecordView
	for _, tr := range engine.TimerRecords(&s) {
		if tr.Kind == engine.TimerCompensationRetry {
			out = append(out, tr)
		}
	}
	return out
}

// TestCompensationRetryTimerIsRetiredWithTheWalk covers plan P1 step 10 and the
// P1-D carry-over: the walk-scoped sweep must reach a RETRY record both at the
// walk's finish and at every re-dispatch that supersedes it.
//
// What makes each row fail before the sweep is widened is recorded per row; both
// numbers were measured on this branch before the change.
func TestCompensationRetryTimerIsRetiredWithTheWalk(t *testing.T) {
	at := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)

	type testCase struct {
		name string
		// def is the definition the SUT step runs against. It is per-case because
		// the retain row needs a parallel sibling the other two rows must not have.
		def func() *model.ProcessDefinition
		// opt is the options for the SUT step. It is per-case because the retain
		// row must have stall detection ON: without a walk-scoped record live at
		// the moment of the sweep, the emit loop matches nothing, the `cmds == nil`
		// early return fires, and the rebuild — the branch under test — never runs.
		// ⚠ Measured: with StepOptions carrying only the retry policy this row went
		// GREEN against a sweep whose retain branch had been DELETED.
		opt engine.StepOptions
		// setup returns the state to step, and the trigger to step it with.
		setup  func(t *testing.T) (engine.InstanceState, engine.Trigger)
		assert func(t *testing.T, res engine.StepResult)
	}

	cases := []testCase{
		{
			// Measured before the fix: leakedTimerRecords=2, cmds=[AwaitHuman] — the
			// resume finish emitted no CancelTimer at all.
			name: "a resuming walk retires every leaked retry record at its finish",
			def:  twoRecordCompensableThrowDef,
			opt:  compensationRetryOn(),
			setup: func(t *testing.T) (engine.InstanceState, engine.Trigger) {
				state, failedCmdID := driveToThrowWalkWithFailedFirstRecord(t, at)
				def := twoRecordCompensableThrowDef()
				opt := compensationRetryOn()

				// The worker answers late: u2 actually succeeded, so the walk advances
				// to u1 while u2's backoff is still armed.
				adv, err := engine.Step(t.Context(), def, state,
					engine.NewActionCompleted(at.Add(4*time.Second), failedCmdID, nil), opt)
				require.NoError(t, err)
				u1 := invokeActionNamed(adv.Commands, "u1")
				require.NotNil(t, u1, "control: the walk must have advanced to the second record")

				// u1 fails too, arming its own backoff…
				failed, err := engine.Step(t.Context(), def, adv.State,
					engine.NewActionFailed(at.Add(5*time.Second), u1.CommandID, "u1 blew up", true), opt)
				require.NoError(t, err)
				require.Len(t, retryTimerRecords(failed.State), 1,
					"control: exactly one live backoff going into the finish")

				// …and is then answered late as well, which drains the walk and
				// RESUMES the instance at afterThrow.
				return failed.State, engine.NewActionCompleted(at.Add(6*time.Second), u1.CommandID, nil)
			},
			assert: func(t *testing.T, res engine.StepResult) {
				require.Equal(t, engine.StatusRunning, res.State.Status,
					"control: the walk finished by RESUMING, the only finish on which a "+
						"leaked record is observable")

				assert.Empty(t, retryTimerRecords(res.State),
					"no retry record may outlive the walk: stepCompensationFinish has "+
						"already zeroed the cursor, so an orphan fires against "+
						"compensationCursor{} (ADR-0171's panic shape)")
				assert.True(t, engine.TimersAreNil(&res.State),
					"nil, not an empty slice, when the sweep removed the last record: "+
						"s.Timers is marshalled into the persisted snapshot and "+
						"null → [] is stored-shape drift (ADR-0174)")

				var cancelled []string
				for _, c := range res.Commands {
					if ct, ok := c.(engine.CancelTimer); ok {
						cancelled = append(cancelled, ct.TimerID)
					}
				}
				assert.Len(t, cancelled, 1,
					"and the scheduler is told, or the job outlives the walk it belonged to")
			},
		},
		{
			// Measured before the fix: after the advance the state still carried
			// records=[TimerCompensationRetry/probe-1-tm1/probe-1-c3] — the backoff for
			// a command the walk had already moved past.
			name: "an advance during a live backoff sweeps the superseded retry record",
			def:  twoRecordCompensableThrowDef,
			opt:  compensationRetryOn(),
			setup: func(t *testing.T) (engine.InstanceState, engine.Trigger) {
				state, failedCmdID := driveToThrowWalkWithFailedFirstRecord(t, at)
				return state, engine.NewActionCompleted(at.Add(4*time.Second), failedCmdID, nil)
			},
			assert: func(t *testing.T, res engine.StepResult) {
				require.NotNil(t, invokeActionNamed(res.Commands, "u1"),
					"control: the walk advanced to the second record")

				assert.Empty(t, retryTimerRecords(res.State),
					"the superseded record's backoff is swept by the re-dispatch's own "+
						"cancel-then-arm, not left armed until the finish")
				assert.True(t, engine.TimersAreNil(&res.State))
				assert.Len(t, cancelTimerIDs(res.Commands), 1,
					"and its scheduler job is cancelled with it")
			},
		},
		{
			// The sweep's RETAIN branch, which nothing exercised before: it needs a
			// walk-scoped record and a NON-walk-scoped one alive at the same time,
			// and a compensation walk normally has the table to itself
			// (beginCompensation's prologue nils s.Timers). A parallel sibling parked
			// on a deadline is the shape that produces both — and it is exactly the
			// shape the widening could have broken, since the predicate now decides
			// what the sweep destroys rather than only what it cancels.
			//
			// It is a REGRESSION GUARD rather than a red-first row: it passes before
			// the widening too (the old predicate also retained a deadline). Its
			// mutation is inverting the retain filter, which drops the sibling's
			// record; recorded in the delivery report.
			name: "the sweep retains a parallel sibling's deadline record",
			def:  throwWithParallelDeadlineSiblingDef,
			opt:  compensationRetryAndStallOn(),
			setup: func(t *testing.T) (engine.InstanceState, engine.Trigger) {
				state, cmdID := driveToParallelThrowWalkWithDeadlineSibling(t, at)
				return state, engine.NewActionFailed(at.Add(3*time.Second), cmdID, "undo-x blew up", true)
			},
			assert: func(t *testing.T, res engine.StepResult) {
				require.Len(t, retryTimerRecords(res.State), 1,
					"control: the failure armed a backoff, so the sweep really ran")
				assert.Empty(t, timerRecordsOfKindView(res.State, engine.TimerCompensationStall),
					"control: the stall guard that made the sweep REBUILD rather than "+
						"early-return has itself been swept")

				deadlines := timerRecordsOfKindView(res.State, engine.TimerDeadline)
				require.Len(t, deadlines, 1,
					"the sibling branch's deadline is NOT walk-scoped and must survive: "+
						"the walk owns its own timers, not the instance's")
				assert.NotContains(t, cancelTimerIDs(res.Commands), deadlines[0].TimerID,
					"and its scheduler job must not be cancelled either")
				assert.False(t, engine.TimersAreNil(&res.State),
					"s.Timers is REBUILT here rather than nilled — the branch that is "+
						"skipped entirely when the walk owns the whole table")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			def := tc.def()
			state, trig := tc.setup(t)

			res, err := engine.Step(t.Context(), def, state, trig, tc.opt)
			require.NoError(t, err)

			tc.assert(t, res)
		})
	}
}

// cancelTimerIDs returns the TimerID of every CancelTimer in cmds.
func cancelTimerIDs(cmds []engine.Command) []string {
	var out []string
	for _, c := range cmds {
		if ct, ok := c.(engine.CancelTimer); ok {
			out = append(out, ct.TimerID)
		}
	}
	return out
}

// timerRecordsOfKindView returns only the records of kind k in s.
func timerRecordsOfKindView(s engine.InstanceState, k engine.TimerKind) []engine.TimerRecordView {
	var out []engine.TimerRecordView
	for _, tr := range engine.TimerRecords(&s) {
		if tr.Kind == k {
			out = append(out, tr)
		}
	}
	return out
}

// throwWithParallelDeadlineSiblingDef is
//
//	start → forkGW ─┬─ compThrow(CompensateRef:"arch") → joinGW
//	                └─ waitTask(user task, WaitDeadline 4h) → joinGW
//	joinGW → end
//
// The throw branch starts a compensation walk while the sibling branch stays
// parked on a user task whose deadline has armed a TimerDeadline record — the
// only shape in which a walk-scoped sweep and a non-walk-scoped record coexist.
// A compensation walk otherwise has s.Timers to itself: beginCompensation's
// prologue nils it, and a throw walk consumes only the throwing token.
func throwWithParallelDeadlineSiblingDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "retire-parallel", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			gateway.NewParallel("forkGW"),
			event.NewCompensateThrow("compThrow", event.WithCompensateRef("arch")),
			activity.NewUserTask("waitTask",
				activity.WithWaitDeadline(schedule.AfterDuration(4*time.Hour), "f-timeout")),
			gateway.NewParallel("joinGW"),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "forkGW"},
			{ID: "f2", Source: "forkGW", Target: "compThrow"},
			{ID: "f3", Source: "forkGW", Target: "waitTask"},
			{ID: "f4", Source: "compThrow", Target: "joinGW"},
			{ID: "f5", Source: "waitTask", Target: "joinGW"},
			{ID: "f6", Source: "joinGW", Target: "end"},
		},
	}
}

// driveToParallelThrowWalkWithDeadlineSibling starts
// throwWithParallelDeadlineSiblingDef with a pre-seeded archive, so the fork
// produces a compensation walk on one branch and a deadline-armed park on the
// other. It returns the state and the walk's dispatched command id.
func driveToParallelThrowWalkWithDeadlineSibling(t *testing.T, at time.Time) (engine.InstanceState, string) {
	t.Helper()
	def := throwWithParallelDeadlineSiblingDef()

	seeded := engine.InstanceState{
		InstanceID: "i-retire-parallel",
		ArchivedCompensations: map[string][]engine.CompensationRecord{
			"arch": {{NodeID: "x", Action: "undo-x", CompletedAt: at}},
		},
	}
	// Stall detection ON: the walk's first dispatch must arm a walk-scoped record
	// alongside the sibling's deadline, or the sweep under test early-returns.
	res, err := engine.Step(t.Context(), def, seeded,
		engine.NewStartInstance(at, nil), compensationRetryAndStallOn())
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompensating, res.State.Status,
		"control: the throw branch must have started a walk")

	undo := invokeActionNamed(res.Commands, "undo-x")
	require.NotNil(t, undo, "control: the walk dispatched the archived record")
	require.Len(t, timerRecordsOfKindView(res.State, engine.TimerDeadline), 1,
		"control: the SIBLING branch must be parked on an armed deadline, or the "+
			"sweep never sees a non-walk-scoped record and the retain branch is unrun")
	require.Len(t, timerRecordsOfKindView(res.State, engine.TimerCompensationStall), 1,
		"control: and a WALK-SCOPED record must be live too, or the sweep takes its "+
			"`cmds == nil` early return and the rebuild is never reached")

	return res.State, undo.CommandID
}

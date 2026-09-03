package engine_test

// step_compensation_terminal_test.go — what a CompensateRequested does when it
// reaches an ALREADY-TERMINAL instance, now
// that stepCompensateRequested's own resume guard is gone and dispatch's single
// structural guard is the only one left.
//
// The shapes this file pins split on WHERE the refusal now comes from:
//
//   - Two MINIMAL malformed shapes (ResetVars with no ReverseNode,
//     RestoreTargetVars with no ToNode) leave BOTH ToNode and ReverseNode empty,
//     so terminalPolicy reads allowOnTerminal, dispatch waves them through, and
//     stepCompensateRequested's pure trigger-SHAPE guards reject them exactly as
//     before.
//     ⚠ Both guards were ALREADY pinned before this file, by
//     TestCompensateRequested_ResetVarsWithoutReverseNode and
//     TestCompensateRequested_RestoreTargetVarsWithoutToNode in
//     reverse_instance_test.go — but both drive a RUNNING instance. What was
//     unpinned, and is what these two rows add, is the same shapes on a
//     TERMINAL one: that dispatch's new guard does not swallow a shape defect,
//     and that the shape error still wins for the minimal shapes.
//   - Two NON-MINIMAL malformed shapes carry a resume field as well, which flips
//     terminalPolicy to rejectWithError, so dispatch refuses them BEFORE the
//     handler runs and the shape error is never reached. That is a real change
//     of reported error on a terminal instance, and arguably an improvement: the
//     shape errors carry no sentinel and classify 500, while ErrInstanceTerminal
//     wraps ErrInvalidTransition and classifies 422.
//   - The PLAIN full rollback (both fields empty) is the one allowOnTerminal
//     shape, and the last three rows pin the whole of its behaviour: it walks
//     when there is anything to compensate — including a record that is only
//     ARCHIVED under a closed sub-process scope — and is refused when there is
//     not.
//
// ⚠ The guard's predicate was INVERTED as originally written, and the
// correction is recorded here because the rows are what enforce it. The
// original predicate refused a plain full rollback when the instance is
// terminal AND compensation records SURVIVE, on the stated premise that with no
// surviving records it "still walks". Measurement inverted both halves:
//
//   - WITH records the rollback is a real walk, dispatching the compensation
//     InvokeActions one at a time — precisely the "compensating a finished
//     instance whose records are still present is a legitimate admin action"
//     carve-out, and what TestTerminalResumeGuard/plain_full_rollback_allowed
//     and TestTerminalDispatchOutcomes/engine.CompensateRequested already assert.
//   - WITHOUT records there is no walk at all: beginCompensation finds nothing
//     and finishes immediately, re-stamping the terminal status, discarding any
//     surviving token and overwriting EndedAt, for zero compensation benefit.
//
// The INTENT — refuse the rollback in the case where it harms without
// compensating — was audited and stands; only its expression was wrong, so the
// implemented guard uses the corrected predicate. See stepCompensateRequested
// and hasCompensationRecordsToWalk.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
)

// firstPendingInvoke returns the CommandID of the first pending InvokeAction in
// cmds, or "" when the walk dispatched none. Unlike firstInvokeCommandID it does
// NOT fail the test on absence: "no action was dispatched" is the loop-exit
// condition for driveTerminalRollbackToCompletion below.
func firstPendingInvoke(cmds []engine.Command) string {
	for _, c := range cmds {
		if ia, ok := c.(engine.InvokeAction); ok && !ia.FireAndForget {
			return ia.CommandID
		}
	}
	return ""
}

// driveTerminalRollbackToCompletion runs a plain full rollback on an
// already-terminal instance all the way to the end of its walk, and returns the
// resulting state: still terminal, but with RootCompensations consumed.
//
// It is driven rather than hand-crafted on purpose — "terminal with no surviving
// compensation records" has to be a state the engine really produces for the
// last row to assert anything about a reachable situation, and this is the
// realistic route to it: an admin rollback that already ran, followed by a
// second delivery of the same trigger.
func driveTerminalRollbackToCompletion(t *testing.T, instanceID string) engine.InstanceState {
	t.Helper()

	def := resumableForceTerminatedDef()
	st := driveToForceTerminatedWithBothRecords(t, def, instanceID)

	at := terminalT0.Add(time.Hour)
	r, err := engine.Step(t.Context(), def, st, engine.NewCompensateRequested(at, ""), engine.StepOptions{})
	require.NoError(t, err)
	require.Equal(t, engine.StatusCompensating, r.State.Status,
		"setup: the rollback must actually start, or this helper is not exercising a walk")

	// Two records, so at most two compensation round-trips; the bound stops a
	// mis-driven walk from spinning instead of failing.
	for range 4 {
		cmdID := firstPendingInvoke(r.Commands)
		if cmdID == "" {
			break
		}
		at = at.Add(time.Second)
		r, err = engine.Step(t.Context(), def, r.State, engine.NewActionCompleted(at, cmdID, nil), engine.StepOptions{})
		require.NoError(t, err)
	}

	require.True(t, r.State.Status.IsTerminal(),
		"setup: the completed rollback must leave the instance terminal")
	require.Empty(t, r.State.RootCompensations,
		"setup: the walk must have consumed every record, or the no-records row is testing the wrong state")

	return r.State
}

// archivedOnlyTerminalDef is
//
//	root: start → sub[ inner-start → inner-svc(compensable) → inner-end ] → end(force termination)
//
// Driven to its end it produces the state the surviving-records guard MUST NOT
// reject and that a naive `len(RootCompensations) == 0` predicate would have:
// terminal, ZERO root records, but one record archived under "sub".
// beginCompensation calls consolidateArchiveIntoRoot BEFORE it counts, so that
// archived record is a real walk waiting to happen.
func archivedOnlyTerminalDef() *model.ProcessDefinition {
	nested := &model.ProcessDefinition{
		ID: "p-archived-only-nested", Version: 1,
		Nodes: []model.Node{
			event.NewStart("inner-start"),
			activity.NewServiceTask("inner-svc",
				activity.WithTaskAction("book-inner"),
				activity.WithCompensateAction("undo-inner"),
			),
			event.NewEnd("inner-end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "if1", Source: "inner-start", Target: "inner-svc"},
			{ID: "if2", Source: "inner-svc", Target: "inner-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "p-archived-only", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("sub", nested),
			event.NewEnd("end", event.WithForceTermination("abort", event.OutcomeAbort)),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "sub"},
			{ID: "f2", Source: "sub", Target: "end"},
		},
	}
}

// driveToTerminalWithArchivedOnlyRecords walks archivedOnlyTerminalDef to its
// force-termination end: the sub-process exits (archiving inner-svc's record
// under "sub"), then the outer token reaches the terminating end.
func driveToTerminalWithArchivedOnlyRecords(t *testing.T, def *model.ProcessDefinition, instanceID string) engine.InstanceState {
	t.Helper()

	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: instanceID},
		engine.NewStartInstance(terminalT0, nil), engine.StepOptions{})
	require.NoError(t, err)

	cmdID := firstPendingInvoke(r1.Commands)
	require.NotEmpty(t, cmdID, "setup: the inner service task must have been dispatched")

	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewActionCompleted(terminalT0.Add(time.Second), cmdID, nil), engine.StepOptions{})
	require.NoError(t, err)

	require.True(t, r2.State.Status.IsTerminal(),
		"setup: the force-termination end must have run")
	require.Empty(t, r2.State.RootCompensations,
		"setup: the record must be ARCHIVED, not at root, or this row cannot distinguish the two predicates")
	require.Len(t, r2.State.ArchivedCompensations["sub"], 1,
		"setup: the sub-process record must survive archived, or the row is vacuous")

	return r2.State
}

// TestCompensateRequestedOnTerminalInstance pins every CompensateRequested shape
// against an already-terminal instance now that stepCompensateRequested's own
// resume guard is gone.
//
// No `ctx` case modifier (table-test skill rule 3): engine.Step documents ctx as
// carrying no cancellation semantics — it is used only for trace-correlated
// logging and is never inspected for control flow — so a cancelled-context row
// would assert nothing about this SUT.
func TestCompensateRequestedOnTerminalInstance(t *testing.T) {
	t.Parallel()

	at := terminalT0.Add(time.Hour)

	type testCase struct {
		name string
		// state builds the terminal instance this row runs against. Two shapes
		// exist — with and without surviving compensation records — and which one
		// a row needs is the whole point of the last two rows.
		state func(t *testing.T) engine.InstanceState
		// def is the definition the row runs against. nil means
		// resumableForceTerminatedDef, which every row but the archived-records
		// one uses.
		def     *model.ProcessDefinition
		trigger func() engine.CompensateRequested
		assert  func(t *testing.T, before engine.InstanceState, r engine.StepResult, err error)
	}

	withRecords := func(t *testing.T) engine.InstanceState {
		t.Helper()
		return driveToForceTerminatedWithBothRecords(t, resumableForceTerminatedDef(), "i-compensate-terminal")
	}

	cases := []testCase{
		{
			// Minimal malformed shape: allowOnTerminal, so dispatch falls through
			// and the handler's pure trigger-shape guard is what rejects it.
			name:  "reset_vars_without_reverse_node_still_reports_the_shape_error",
			state: withRecords,
			trigger: func() engine.CompensateRequested {
				trg := engine.NewCompensateRequested(at, "")
				trg.ResetVars = true
				return trg
			},
			assert: func(t *testing.T, _ engine.InstanceState, r engine.StepResult, err error) {
				require.Error(t, err)
				assert.ErrorContains(t, err, "ResetVars requires ReverseNode",
					"a shape defect must still be reported as a shape defect, not absorbed by the terminal guard")
				assert.NotErrorIs(t, err, engine.ErrInstanceTerminal,
					"this shape is allowOnTerminal, so the terminal guard must not have fired")
				assert.Empty(t, r.Commands, "a rejected trigger must dispatch nothing")
			},
		},
		{
			name:  "restore_target_vars_without_to_node_still_reports_the_shape_error",
			state: withRecords,
			trigger: func() engine.CompensateRequested {
				trg := engine.NewCompensateRequested(at, "")
				trg.RestoreTargetVars = true
				return trg
			},
			assert: func(t *testing.T, _ engine.InstanceState, r engine.StepResult, err error) {
				require.Error(t, err)
				assert.ErrorContains(t, err, "RestoreTargetVars requires ToNode",
					"a shape defect must still be reported as a shape defect, not absorbed by the terminal guard")
				assert.NotErrorIs(t, err, engine.ErrInstanceTerminal,
					"this shape is allowOnTerminal, so the terminal guard must not have fired")
				assert.Empty(t, r.Commands, "a rejected trigger must dispatch nothing")
			},
		},
		{
			// Non-minimal malformed shape: ToNode makes it rejectWithError, so
			// dispatch refuses it before stepCompensateRequested's shape guard is
			// reached. On main this reported the shape error instead.
			name:  "reset_vars_with_a_to_node_reports_terminal_first",
			state: withRecords,
			trigger: func() engine.CompensateRequested {
				trg := engine.NewCompensateRequested(at, "svc")
				trg.ResetVars = true
				return trg
			},
			assert: func(t *testing.T, _ engine.InstanceState, r engine.StepResult, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, engine.ErrInstanceTerminal,
					"a resume-shaped rollback carries resume intent, so the terminal guard must win over the shape guard")
				assert.ErrorIs(t, err, engine.ErrInvalidTransition,
					"the refusal must stay classifiable as an invalid transition, or service/ stops mapping it to a conflict")
				assert.NotContains(t, err.Error(), "ResetVars requires ReverseNode",
					"the handler must never have run, so its shape guard cannot be what reported this")
				assert.Empty(t, r.Commands, "a rejected trigger must dispatch nothing")
			},
		},
		{
			name:  "restore_target_vars_with_a_reverse_node_reports_terminal_first",
			state: withRecords,
			trigger: func() engine.CompensateRequested {
				trg := engine.NewReverseToStart(at, "start")
				trg.RestoreTargetVars = true
				trg.ResetVars = false
				return trg
			},
			assert: func(t *testing.T, _ engine.InstanceState, r engine.StepResult, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, engine.ErrInstanceTerminal,
					"a resume-shaped rollback carries resume intent, so the terminal guard must win over the shape guard")
				assert.ErrorIs(t, err, engine.ErrInvalidTransition,
					"the refusal must stay classifiable as an invalid transition, or service/ stops mapping it to a conflict")
				assert.NotContains(t, err.Error(), "RestoreTargetVars requires ToNode",
					"the handler must never have run, so its shape guard cannot be what reported this")
				assert.Empty(t, r.Commands, "a rejected trigger must dispatch nothing")
			},
		},
		{
			// The admin carve-out, and the case the inverted predicate would have
			// rejected.
			name:  "plain_full_rollback_with_surviving_records_walks",
			state: withRecords,
			trigger: func() engine.CompensateRequested {
				return engine.NewCompensateRequested(at, "")
			},
			assert: func(t *testing.T, before engine.InstanceState, r engine.StepResult, err error) {
				require.NoError(t, err,
					"compensating a finished instance whose records survive is a legitimate admin action")
				require.Len(t, before.RootCompensations, 2,
					"precondition: this row is only meaningful while records survive")
				assert.Equal(t, engine.StatusCompensating, r.State.Status,
					"the walk must actually start")
				assert.NotEmpty(t, firstPendingInvoke(r.Commands),
					"the walk must dispatch a compensation InvokeAction, not silently swallow the trigger")
			},
		},
		{
			// The route the guard exists to close, with its predicate corrected
			// to the case that actually does harm.
			//
			// ⚠ Why this guard is worth its weight, recorded because the evidence
			// is otherwise lost with the behaviour it removed: BEFORE the guard,
			// this trigger returned no error and no compensation action, yet
			// mutated the instance three ways — it flipped the status, DISCARDED
			// the surviving token, and rewrote EndedAt, moving the recorded moment
			// of death an hour later. The `NotEqual(before.EndedAt, …)` assertion
			// that used to stand here was the only thing in the whole suite that
			// ever caught that re-stamp. Nothing was compensated in exchange:
			// beginCompensation found no eligible record and went straight to
			// stepCompensationFinish. That is a terminal transition re-run for
			// zero benefit, which is why the refusal is an ERROR rather than a
			// silent drop — the only caller that can still reach this path is an
			// explicit admin action, since CancelRequested is now rejectSilently
			// at dispatch and never arrives here at all.
			name: "plain_full_rollback_with_no_surviving_records_is_rejected",
			state: func(t *testing.T) engine.InstanceState {
				t.Helper()
				return driveTerminalRollbackToCompletion(t, "i-compensate-terminal-consumed")
			},
			trigger: func() engine.CompensateRequested {
				return engine.NewCompensateRequested(terminalT0.Add(2*time.Hour), "")
			},
			assert: func(t *testing.T, before engine.InstanceState, r engine.StepResult, err error) {
				require.Error(t, err,
					"a rollback with nothing to compensate only re-stamps the terminal transition, so it must be refused")
				require.Empty(t, before.RootCompensations,
					"precondition: this row is only meaningful once every record is consumed")
				assert.ErrorIs(t, err, engine.ErrInstanceTerminal,
					"the refusal must carry the same sentinel as the partial/targeted rollback rejections")
				assert.ErrorIs(t, err, engine.ErrInvalidTransition,
					"the refusal must stay classifiable as an invalid transition, or service/ stops mapping it to a conflict")
				assert.ErrorContains(t, err, "nothing left to compensate",
					"the message must name the actual reason, not just repeat that the instance is terminal")
				assert.Empty(t, r.Commands, "a rejected trigger must dispatch nothing")
			},
		},
		{
			// ⚠ The row that decides HOW the guard counts. Measured, not assumed:
			// beginCompensation calls consolidateArchiveIntoRoot BEFORE it looks
			// for eligible records, so an archived sub-process record is a walk
			// waiting to happen even though RootCompensations is empty. A guard
			// written as `len(s.RootCompensations) == 0` would refuse this real
			// compensation walk — verified by running exactly that predicate,
			// which turns this row RED and nothing else.
			name: "plain_full_rollback_with_only_archived_sub_process_records_still_walks",
			state: func(t *testing.T) engine.InstanceState {
				t.Helper()
				return driveToTerminalWithArchivedOnlyRecords(t, archivedOnlyTerminalDef(), "i-compensate-terminal-archived")
			},
			def: archivedOnlyTerminalDef(),
			trigger: func() engine.CompensateRequested {
				return engine.NewCompensateRequested(terminalT0.Add(time.Hour), "")
			},
			assert: func(t *testing.T, before engine.InstanceState, r engine.StepResult, err error) {
				require.NoError(t, err,
					"an archived sub-process record is still a record to walk — the guard must not count root records alone")
				require.Empty(t, before.RootCompensations,
					"precondition: the record must be archived rather than at root, or this row proves nothing")
				assert.Equal(t, engine.StatusCompensating, r.State.Status,
					"the walk must actually start")
				assert.NotEmpty(t, firstPendingInvoke(r.Commands),
					"the archived record's compensation action must be dispatched")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			def := tc.def
			if def == nil {
				def = resumableForceTerminatedDef()
			}
			before := tc.state(t)
			require.True(t, before.Status.IsTerminal(),
				"the fixture this row runs against must be terminal, or the row asserts nothing")

			r, err := engine.Step(t.Context(), def, before, tc.trigger(), engine.StepOptions{})
			tc.assert(t, before, r, err)
		})
	}
}

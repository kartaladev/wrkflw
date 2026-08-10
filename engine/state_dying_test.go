package engine

// state_dying_test.go — ADR-0172: a dying instance spawns no new work.
//
// ⚠ The predicate this replaces was `walkMode() == walkAdmin || PendingCancel`,
// and it was REFUTED by execution: consumePendingCancel is set on the throw and
// reverse finish plans but NOT on walkPartial, so a partial walk RESUMES with
// PendingCancel set. The refuted predicate silenced an arm on that live instance
// — reintroducing the very defect ADR-0172 exists to close.
//
// walkTerminates therefore mirrors stepCompensationFinish's own plan
// construction, so the two cannot drift.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/definition/schedule"
)

var dyingT0 = time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)

func TestCompensationCursorWalkTerminates(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name          string
		cursor        compensationCursor
		pendingCancel bool
		assert        func(t *testing.T, got bool)
	}

	cases := []testCase{
		{
			name:   "an admin/cancel/error full rollback terminates",
			cursor: compensationCursor{ActiveCmdID: "c1"},
			assert: func(t *testing.T, got bool) {
				assert.True(t, got, "walkAdmin is the only mode whose finishPlan has resume=false")
			},
		},
		{
			// THE CRITICAL. consumePendingCancel is NOT set on walkPartial, so
			// this walk resumes even with a cancel deferred onto it.
			name:          "a PARTIAL rollback resumes EVEN WITH PendingCancel",
			cursor:        compensationCursor{ToNode: "svcA", ActiveCmdID: "c1"},
			pendingCancel: true,
			assert: func(t *testing.T, got bool) {
				assert.False(t, got,
					"walkPartial does not consume PendingCancel — the instance keeps running")
			},
		},
		{
			name:   "a partial rollback without PendingCancel resumes",
			cursor: compensationCursor{ToNode: "svcA", ActiveCmdID: "c1"},
			assert: func(t *testing.T, got bool) {
				assert.False(t, got)
			},
		},
		{
			// A full reverse RESUMES, but is treated as terminating anyway —
			// see ADR-0172 Decision 1a. Firing an arm into it gives two
			// concurrent tokens, ResetVars wiping the event sub-process body's
			// variables under it, and rearmRootESP resurrecting an interrupting
			// one-shot arm while its body still runs.
			name:   "a full REVERSE is treated as terminating, by decision",
			cursor: compensationCursor{ReverseNode: "start", ActiveCmdID: "c1"},
			assert: func(t *testing.T, got bool) {
				assert.True(t, got, "ADR-0172 Decision 1a excludes walkReverse deliberately")
			},
		},
		{
			name:   "a scope-wide compensation THROW resumes",
			cursor: compensationCursor{ResumeNode: "after", ActiveCmdID: "c1"},
			assert: func(t *testing.T, got bool) {
				assert.False(t, got, "a local throw leaves the process legitimately running")
			},
		},
		{
			name:          "a throw with a deferred cancel terminates",
			cursor:        compensationCursor{ResumeNode: "after", ActiveCmdID: "c1"},
			pendingCancel: true,
			assert: func(t *testing.T, got bool) {
				assert.True(t, got, "the throw finish plan DOES set consumePendingCancel")
			},
		},
		{
			name:   "a scope-TARGETED throw resumes",
			cursor: compensationCursor{ResumeNode: "after", ArchiveKey: "k", ActiveCmdID: "c1"},
			assert: func(t *testing.T, got bool) {
				assert.False(t, got, "walkThrowTargeted is a resume — the fifth mode. ⚠ walkMode() discriminates on ArchiveKey, NOT ResumeScope: an earlier version of this row set ResumeScope and silently duplicated the scope-wide case above")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.assert(t, tc.cursor.walkTerminates(tc.pendingCancel))
		})
	}
}

func TestInstanceStateSpawnsNewWork(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		state  *InstanceState
		assert func(t *testing.T, got bool)
	}

	cases := []testCase{
		{
			name:  "a running instance spawns work",
			state: &InstanceState{Status: StatusRunning},
			assert: func(t *testing.T, got bool) {
				assert.True(t, got)
			},
		},
		{
			name: "an instance in a TERMINATING rollback does not",
			state: &InstanceState{
				Status:       StatusCompensating,
				Compensating: compensationCursor{ActiveCmdID: "c1"},
			},
			assert: func(t *testing.T, got bool) {
				assert.False(t, got, "a cancel/error/admin rollback will end the instance")
			},
		},
		{
			name: "an instance in a RESUMING throw walk does",
			state: &InstanceState{
				Status:       StatusCompensating,
				Compensating: compensationCursor{ResumeNode: "after", ActiveCmdID: "c1"},
			},
			assert: func(t *testing.T, got bool) {
				assert.True(t, got, "a local throw resumes — silencing it loses a delivery permanently")
			},
		},
		{
			name: "a PARTIAL rollback carrying PendingCancel still does",
			state: &InstanceState{
				Status:        StatusCompensating,
				Compensating:  compensationCursor{ToNode: "svcA", ActiveCmdID: "c1"},
				PendingCancel: true,
			},
			assert: func(t *testing.T, got bool) {
				assert.True(t, got, "the refuted predicate silenced this LIVE instance")
			},
		},
		{
			name:  "a terminal instance does not",
			state: &InstanceState{Status: StatusCompleted},
			assert: func(t *testing.T, got bool) {
				assert.False(t, got)
			},
		},
		{
			// ALLOW-LIST, not deny-list. Status.IsTerminal's own godoc records
			// that an out-of-range value is treated as NOT terminal, so a
			// deny-list predicate would start FIRING arms on one. Measured: main
			// silences it; a deny-list fires it.
			name:  "an OUT-OF-RANGE status fails CLOSED",
			state: &InstanceState{Status: Status(9)},
			assert: func(t *testing.T, got bool) {
				assert.False(t, got, "unknown status must not spawn work")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.assert(t, tc.state.spawnsNewWork())
		})
	}
}

// ── The predicate at the FIRE SITE ───────────────────────────────────────────

// dyingProbeDef declares a ROOT event sub-process and a NON-ROOT one inside a
// sub-process scope, both armed on "boom".
func dyingProbeDef() *model.ProcessDefinition {
	body := func(id, action string) *model.ProcessDefinition {
		return &model.ProcessDefinition{
			ID: id + "-body", Version: 1,
			Nodes: []model.Node{
				event.NewStart(id+"-start", event.WithSignalName("boom")),
				activity.NewServiceTask(id+"-work", activity.WithTaskAction(action)),
				event.NewEnd(id + "-end"),
			},
			Flows: []flow.SequenceFlow{
				{ID: id + "-f1", Source: id + "-start", Target: id + "-work"},
				{ID: id + "-f2", Source: id + "-work", Target: id + "-end"},
			},
		}
	}
	nested := &model.ProcessDefinition{
		ID: "dying-nested", Version: 1,
		Nodes: []model.Node{
			event.NewStart("n-start"),
			activity.NewUserTask("n-work"),
			event.NewEnd("n-end"),
			activity.NewSubProcess("nesp", body("nesp", "nested-esp-action")),
		},
		Flows: []flow.SequenceFlow{
			{ID: "nf1", Source: "n-start", Target: "n-work"},
			{ID: "nf2", Source: "n-work", Target: "n-end"},
		},
	}
	return &model.ProcessDefinition{
		ID: "dying-probe", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewSubProcess("sub", nested),
			event.NewEnd("end"),
			activity.NewSubProcess("resp", body("resp", "root-esp-action")),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "sub"},
			{ID: "f2", Source: "sub", Target: "end"},
		},
	}
}

// TestEventSubprocessArmRespectsSpawnsNewWork drives the fire function directly
// for each meaning s.Status can carry, ROOT and NON-ROOT.
//
// ⚠ Direct-call, deliberately: this is the predicate's own unit, and the
// end-to-end consequences are covered separately. ADR-0172 Correction 1 exists
// precisely because an earlier direct-call result was over-read, so this test
// claims only what it measures — whether the fire function spawns work.
func TestEventSubprocessArmRespectsSpawnsNewWork(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		status Status
		cursor compensationCursor
		pend   bool
		root   bool
		assert func(t *testing.T, cmds []Command, s *InstanceState)
	}

	fires := func(t *testing.T, cmds []Command, s *InstanceState) {
		t.Helper()
		assert.NotEmpty(t, cmds, "the arm must fire: this instance is legitimately running")
		assert.NotEmpty(t, s.Scopes, "and open its child scope")
	}
	silent := func(t *testing.T, cmds []Command, s *InstanceState) {
		t.Helper()
		assert.Empty(t, cmds, "a dying instance must dispatch NO new work")
	}

	cases := []testCase{
		{name: "root, running", status: StatusRunning, root: true, assert: fires},
		{name: "non-root, running", status: StatusRunning, assert: fires},
		{
			name: "root, RESUMING throw walk", status: StatusCompensating, root: true,
			cursor: compensationCursor{ResumeNode: "after", ActiveCmdID: "c1"},
			assert: fires, // today SILENCED by `!= StatusRunning` — direction (b)
		},
		{
			name: "non-root, TERMINATING rollback", status: StatusCompensating,
			cursor: compensationCursor{ActiveCmdID: "c1"},
			assert: silent, // today FIRES into a dying rollback — direction (a)
		},
		{
			name: "root, TERMINATING rollback", status: StatusCompensating, root: true,
			cursor: compensationCursor{ActiveCmdID: "c1"},
			assert: silent,
		},
		{
			name: "non-root, PARTIAL rollback with PendingCancel", status: StatusCompensating,
			cursor: compensationCursor{ToNode: "svcA", ActiveCmdID: "c1"}, pend: true,
			assert: fires, // the refuted predicate silenced this LIVE instance
		},
		{
			name: "non-root, terminal", status: StatusCompleted,
			assert: silent,
		},
		{
			name: "non-root, OUT-OF-RANGE status", status: Status(9),
			assert: silent, // allow-list: unknown fails closed
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			def := dyingProbeDef()
			s := &InstanceState{
				InstanceID:    "d1",
				Status:        tc.status,
				Compensating:  tc.cursor,
				PendingCancel: tc.pend,
				Scopes:        []Scope{{ID: "sc1", NodeID: "sub"}},
			}
			arm := eventTriggeredSubprocessArm{
				EnclosingScopeID:    "sc1",
				EventSubprocessNode: "nesp",
				triggerMatch:        triggerMatch{Signal: "boom"},
			}
			if tc.root {
				arm = eventTriggeredSubprocessArm{
					EventSubprocessNode: "resp",
					triggerMatch:        triggerMatch{Signal: "boom"},
				}
				s.Scopes = nil
			}

			cmds, err := fireEventTriggeredSubprocessArm(t.Context(), def, s, arm, dyingT0, Macro, nil)
			require.NoError(t, err)
			tc.assert(t, cmds, s)
		})
	}
}

// TestDyingInstanceSpawnsNoWorkOnAnyTriggerKind closes the gap `/code-review`
// found: ADR-0172 Decision 4 claims "a dying instance spawns no new work" is not
// a signal-specific rule, but only the SIGNAL path's token loop was guarded.
//
// handleTimerFired's standalone-timer fall-through and handleMessageReceived's
// standalone-message fall-through resumed a parked token and drove it with no
// eligibility check, dispatching a real InvokeAction to a worker whose
// ActionCompleted would later land on a terminated instance.
//
// ⚠ The state below is production-reachable, not hand-forged: handleCancelRequested's
// deferral branch records PendingCancel WITHOUT cancelling tokens or arms (unlike
// beginCompensation), so a throw walk carrying a deferred cancel keeps its parked
// tokens while walkTerminates reports true.
func TestDyingInstanceSpawnsNoWorkOnAnyTriggerKind(t *testing.T) {
	t.Parallel()

	def := &model.ProcessDefinition{
		ID: "dying-any-trigger", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			event.NewIntermediateCatch("catchM", event.WithMessageCorrelator("M", "")),
			activity.NewServiceTask("after", activity.WithTaskAction("after-action")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "df1", Source: "start", Target: "catchM"},
			{ID: "df2", Source: "catchM", Target: "after"},
			{ID: "df3", Source: "after", Target: "end"},
		},
	}

	dying := func() InstanceState {
		return InstanceState{
			InstanceID: "d1",
			Status:     StatusCompensating,
			// A scope-wide throw walk that a deferred cancel has flipped to
			// terminating: walkTerminates(pendingCancel=true) == true.
			Compensating:  compensationCursor{ResumeNode: "after", ActiveCmdID: "c1"},
			PendingCancel: true,
			Tokens: []Token{{
				ID: "d1-t1", NodeID: "catchM", State: TokenWaiting, AwaitMessage: "M",
			}},
		}
	}

	s := dying()
	require.False(t, s.spawnsNewWork(), "fixture control: this instance IS dying")

	r, err := Step(t.Context(), def, s,
		NewMessageReceived(dyingT0, "M", "", nil), StepOptions{})
	require.NoError(t, err)

	for _, c := range r.Commands {
		_, isInvoke := c.(InvokeAction)
		assert.False(t, isInvoke,
			"a dying instance must not dispatch a message-resumed token's action to a worker")
	}
	assert.Equal(t, "catchM", r.State.Tokens[0].NodeID,
		"the parked token must not advance while the rollback is terminating")
}

// TestDyingInstanceSpawnsNoWorkOnTimerResume is the timer half of the same gate
// finding. It is a SEPARATE test, not a table row, because the two paths are
// separate code with separate guards — a single row would let one regress
// silently behind the other.
func TestDyingInstanceSpawnsNoWorkOnTimerResume(t *testing.T) {
	t.Parallel()

	def := &model.ProcessDefinition{
		ID: "dying-timer", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			event.NewIntermediateCatch("catchT", event.WithCatchTimer(schedule.AfterDuration(time.Hour))),
			activity.NewServiceTask("after", activity.WithTaskAction("after-action")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "tf1", Source: "start", Target: "catchT"},
			{ID: "tf2", Source: "catchT", Target: "after"},
			{ID: "tf3", Source: "after", Target: "end"},
		},
	}

	s := InstanceState{
		InstanceID:    "d2",
		Status:        StatusCompensating,
		Compensating:  compensationCursor{ResumeNode: "after", ActiveCmdID: "c1"},
		PendingCancel: true,
		Tokens: []Token{{
			ID: "d2-t1", NodeID: "catchT", State: TokenWaiting, AwaitCommand: "d2-tm1",
		}},
	}
	require.False(t, s.spawnsNewWork(), "fixture control: this instance IS dying")

	r, err := Step(t.Context(), def, s, NewTimerFired(dyingT0, "d2-tm1"), StepOptions{})
	require.NoError(t, err)

	for _, c := range r.Commands {
		_, isInvoke := c.(InvokeAction)
		assert.False(t, isInvoke,
			"a dying instance must not dispatch a timer-resumed token's action to a worker")
	}
	assert.Equal(t, "catchT", r.State.Tokens[0].NodeID,
		"the parked token must not advance while the rollback is terminating")
}

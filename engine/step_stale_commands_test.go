package engine

// step_stale_commands_test.go — white-box tests for the stale-command filter.
// liveAwaiters and dropStaleTokenCommands are unexported and have no
// black-box path of their own; the end-to-end behaviour through Step is pinned
// separately in step_stale_commands_e2e_test.go.

import (
	"log/slog"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/humantask"
)

// TestLiveAwaiters pins the set the filter is built on: every surviving
// token's NON-EMPTY AwaitCommand, plus Compensating.ActiveCmdID while the
// instance is not terminal.
//
// The cursor source is load-bearing. startCompensationWalk (step_nodes.go:983)
// consumes the throw token BEFORE emitting a non-FireAndForget InvokeAction, so
// a token-only set would drop that command and hang every compensation walk.
// The terminal exclusion is the mirror image: no terminal transition clears the
// cursor today, so a step that starts a walk and then force-terminates would
// otherwise keep a compensation action alive for a terminated instance.
func TestLiveAwaiters(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		state  InstanceState
		assert func(t *testing.T, got map[string]struct{})
	}

	cases := []testCase{
		{
			name: "two parked tokens contribute both AwaitCommand ids",
			state: InstanceState{Tokens: []Token{
				{ID: "t1", State: TokenWaiting, AwaitCommand: "c1"},
				{ID: "t2", State: TokenWaiting, AwaitCommand: "c2"},
			}},
			assert: func(t *testing.T, got map[string]struct{}) {
				assert.Contains(t, got, "c1")
				assert.Contains(t, got, "c2")
				assert.Len(t, got, 2)
			},
		},
		{
			name: "an active token's empty AwaitCommand never enters the set",
			state: InstanceState{Tokens: []Token{
				{ID: "t1", State: TokenActive},
				{ID: "t2", State: TokenWaiting, AwaitCommand: "c1"},
			}},
			assert: func(t *testing.T, got map[string]struct{}) {
				assert.NotContains(t, got, "",
					"an empty key must not become a wildcard awaiter")
				assert.Len(t, got, 1)
			},
		},
		{
			name:  "no tokens and no cursor yields an empty set, not a nil deref",
			state: InstanceState{},
			assert: func(t *testing.T, got map[string]struct{}) {
				assert.Empty(t, got)
			},
		},
		{
			name: "the compensation cursor counts while compensating",
			state: InstanceState{
				Status:       StatusCompensating,
				Compensating: compensationCursor{ActiveCmdID: "comp1"},
			},
			assert: func(t *testing.T, got map[string]struct{}) {
				assert.Contains(t, got, "comp1",
					"a token-only set would hang every compensation walk")
			},
		},
		{
			name: "the cursor is excluded once the instance is terminated",
			state: InstanceState{
				Status:       StatusTerminated,
				Compensating: compensationCursor{ActiveCmdID: "comp1"},
			},
			assert: func(t *testing.T, got map[string]struct{}) {
				assert.NotContains(t, got, "comp1")
			},
		},
		{
			name: "the cursor is excluded once the instance is completed",
			state: InstanceState{
				Status:       StatusCompleted,
				Compensating: compensationCursor{ActiveCmdID: "comp1"},
			},
			assert: func(t *testing.T, got map[string]struct{}) {
				assert.NotContains(t, got, "comp1")
			},
		},
		{
			name: "the cursor is excluded once the instance has failed",
			state: InstanceState{
				Status:       StatusFailed,
				Compensating: compensationCursor{ActiveCmdID: "comp1"},
			},
			assert: func(t *testing.T, got map[string]struct{}) {
				assert.NotContains(t, got, "comp1",
					"StatusFailed is the third IsTerminal branch")
			},
		},
		{
			name: "an empty cursor contributes nothing",
			state: InstanceState{
				Status: StatusRunning,
				Tokens: []Token{{ID: "t1", State: TokenWaiting, AwaitCommand: "c1"}},
			},
			assert: func(t *testing.T, got map[string]struct{}) {
				assert.Equal(t, map[string]struct{}{"c1": {}}, got)
			},
		},
		{
			name: "non-command AwaitCommand values enter the set harmlessly",
			state: InstanceState{Tokens: []Token{
				{ID: "t1", State: TokenWaiting, AwaitCommand: "evtgw:t1"},
				{ID: "t2", State: TokenWaiting, AwaitCommand: "tm3"},
			}},
			assert: func(t *testing.T, got map[string]struct{}) {
				// Three of the seven AwaitCommand assignment sites store
				// a timer id or the event-gateway sentinel. They can only ever
				// cause UNDER-filtering, never a dropped live command.
				assert.Contains(t, got, "evtgw:t1")
				assert.Contains(t, got, "tm3")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := liveAwaiters(&tc.state)
			require.NotNil(t, got, "liveAwaiters must return a usable map, never nil")
			tc.assert(t, got)
		})
	}
}

// staleFilterState builds a state with the given parked-token awaiters.
func staleFilterState(awaitCommands ...string) InstanceState {
	st := InstanceState{Status: StatusRunning}
	for i, id := range awaitCommands {
		st.Tokens = append(st.Tokens, Token{
			ID:           "t" + strconv.Itoa(i+1),
			State:        TokenWaiting,
			AwaitCommand: id,
		})
	}
	return st
}

// openTask returns an Unclaimed human-task record for taskID.
func openTask(taskID string) humantask.HumanTask {
	return humantask.HumanTask{TaskID: taskID, InstanceID: "i1", NodeID: "n1", State: humantask.Unclaimed}
}

// invokeIDs returns the CommandID of every InvokeAction in cmds, in order.
func invokeIDs(cmds []Command) []string {
	var out []string
	for _, c := range cmds {
		if ia, ok := c.(InvokeAction); ok {
			out = append(out, ia.CommandID)
		}
	}
	return out
}

// updateTaskIDs returns the TaskID of every UpdateTask in cmds, in order.
func updateTaskIDs(cmds []Command) []string {
	var out []string
	for _, c := range cmds {
		if ut, ok := c.(UpdateTask); ok {
			out = append(out, ut.Task.TaskID)
		}
	}
	return out
}

// TestDropStaleTokenCommands pins the filter: a command whose awaiter the
// state no longer holds is dropped, everything else survives untouched, and a
// dropped AwaitHuman cancels its task record and emits the UpdateTask that keeps
// the store in step.
func TestDropStaleTokenCommands(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		state  InstanceState
		cmds   []Command
		assert func(t *testing.T, s *InstanceState, got []Command)
	}

	cases := []testCase{
		{
			name:  "a non-fire-and-forget InvokeAction with no awaiter is dropped",
			state: staleFilterState(),
			cmds:  []Command{InvokeAction{CommandID: "c1", Name: "charge"}},
			assert: func(t *testing.T, _ *InstanceState, got []Command) {
				assert.Empty(t, got)
			},
		},
		{
			name:  "a fire-and-forget InvokeAction with no awaiter survives",
			state: staleFilterState(),
			cmds:  []Command{InvokeAction{CommandID: "c1", Name: "notify", FireAndForget: true}},
			assert: func(t *testing.T, _ *InstanceState, got []Command) {
				assert.Equal(t, []string{"c1"}, invokeIDs(got),
					"a FireAndForget action has no awaiter by design (command.go:97-104)")
			},
		},
		{
			name:  "a fire-and-forget InvokeAction survives the SLOW path too",
			state: staleFilterState("c1"),
			cmds: []Command{
				InvokeAction{CommandID: "c1", Name: "charge"},
				InvokeAction{CommandID: "ff", Name: "notify", FireAndForget: true},
			},
			assert: func(t *testing.T, _ *InstanceState, got []Command) {
				// The row above cannot pin the switch's !FireAndForget guard: a
				// delivery carrying ONLY a fire-and-forget action fails the
				// pre-scan and returns before the switch is ever reached, so
				// deleting that guard leaves it green. A live sibling forces the
				// slow path, which is where the exemption actually has to hold.
				assert.Equal(t, []string{"c1", "ff"}, invokeIDs(got))
			},
		},
		{
			name:  "an InvokeAction whose awaiter is still parked survives",
			state: staleFilterState("c1"),
			cmds:  []Command{InvokeAction{CommandID: "c1", Name: "charge"}},
			assert: func(t *testing.T, _ *InstanceState, got []Command) {
				assert.Equal(t, []string{"c1"}, invokeIDs(got))
			},
		},
		{
			name: "an InvokeAction correlated by the compensation cursor survives",
			state: InstanceState{
				Status:       StatusCompensating,
				Compensating: compensationCursor{ActiveCmdID: "comp1"},
			},
			cmds: []Command{InvokeAction{CommandID: "comp1", Name: "refund"}},
			assert: func(t *testing.T, _ *InstanceState, got []Command) {
				assert.Equal(t, []string{"comp1"}, invokeIDs(got),
					"no token parks on a compensation invoke; the cursor is its only awaiter")
			},
		},
		{
			name: "a stale AwaitHuman is dropped, its record cancelled and an UpdateTask emitted",
			state: func() InstanceState {
				st := staleFilterState()
				st.Tasks = []humantask.HumanTask{openTask("task1")}
				return st
			}(),
			cmds: []Command{AwaitHuman{TaskID: "task1"}},
			assert: func(t *testing.T, s *InstanceState, got []Command) {
				for _, c := range got {
					_, isAwait := c.(AwaitHuman)
					assert.False(t, isAwait, "the stale AwaitHuman must be dropped")
				}
				require.Len(t, s.Tasks, 1, "the record is cancelled, never removed")
				assert.Equal(t, humantask.Cancelled, s.Tasks[0].State)
				assert.Equal(t, []string{"task1"}, updateTaskIDs(got),
					"every other cancel site emits UpdateTask; so does this one")
			},
		},
		{
			name: "an AwaitHuman whose awaiter is still parked survives untouched",
			state: func() InstanceState {
				st := staleFilterState("task1")
				st.Tasks = []humantask.HumanTask{openTask("task1")}
				return st
			}(),
			cmds: []Command{AwaitHuman{TaskID: "task1"}},
			assert: func(t *testing.T, s *InstanceState, got []Command) {
				assert.Len(t, got, 1)
				assert.Equal(t, humantask.Unclaimed, s.Tasks[0].State)
				assert.Empty(t, updateTaskIDs(got))
			},
		},
		{
			name:  "a stale StartSubInstance is dropped",
			state: staleFilterState(),
			cmds:  []Command{StartSubInstance{CommandID: "c1"}},
			assert: func(t *testing.T, _ *InstanceState, got []Command) {
				assert.Empty(t, got, "an orphan child instance must not be started")
			},
		},
		{
			name:  "a StartSubInstance whose parent token is parked survives",
			state: staleFilterState("c1"),
			cmds:  []Command{StartSubInstance{CommandID: "c1"}},
			assert: func(t *testing.T, _ *InstanceState, got []Command) {
				assert.Len(t, got, 1)
			},
		},
		{
			name:  "a ScheduleTimer for a cancelled token survives",
			state: staleFilterState(),
			cmds:  []Command{ScheduleTimer{TimerID: "tm1", Token: "gone"}},
			assert: func(t *testing.T, _ *InstanceState, got []Command) {
				assert.Len(t, got, 1,
					"no timer id is an awaiter key; filtering would mass-drop deadline/reminder/boundary timers")
			},
		},
		{
			name:  "every never-filtered command kind survives without an awaiter",
			state: staleFilterState(),
			cmds: []Command{
				ScheduleTimer{TimerID: "tm1"},
				CancelTimer{TimerID: "tm1"},
				CompleteInstance{},
				FailInstance{Err: "boom"},
				UpdateTask{Task: openTask("task1")},
				ThrowSignal{Name: "sig"},
				SendMessage{Name: "msg"},
				InvokeCancelAction{},
				Compensate{},
			},
			assert: func(t *testing.T, _ *InstanceState, got []Command) {
				assert.Equal(t, []Command{
					ScheduleTimer{TimerID: "tm1"},
					CancelTimer{TimerID: "tm1"},
					CompleteInstance{},
					FailInstance{Err: "boom"},
					UpdateTask{Task: openTask("task1")},
					ThrowSignal{Name: "sig"},
					SendMessage{Name: "msg"},
					InvokeCancelAction{},
					Compensate{},
				}, got, "nine of the twelve command kinds are never filtered, and none is reordered")
			},
		},
		{
			name:  "a delivery with nothing stale returns the input element for element",
			state: staleFilterState("c1", "task1"),
			cmds: []Command{
				InvokeAction{CommandID: "c1"},
				AwaitHuman{TaskID: "task1"},
				CancelTimer{TimerID: "tm1"},
			},
			assert: func(t *testing.T, _ *InstanceState, got []Command) {
				assert.Equal(t, []Command{
					InvokeAction{CommandID: "c1"},
					AwaitHuman{TaskID: "task1"},
					CancelTimer{TimerID: "tm1"},
				}, got)
			},
		},
		{
			name: "two stale AwaitHumans are both dropped and both cancelled",
			state: func() InstanceState {
				st := staleFilterState()
				st.Tasks = []humantask.HumanTask{openTask("task1"), openTask("task2")}
				return st
			}(),
			cmds: []Command{AwaitHuman{TaskID: "task1"}, AwaitHuman{TaskID: "task2"}},
			assert: func(t *testing.T, s *InstanceState, got []Command) {
				assert.Equal(t, humantask.Cancelled, s.Tasks[0].State)
				assert.Equal(t, humantask.Cancelled, s.Tasks[1].State,
					"the loop must not stop at the first stale command")
				assert.Equal(t, []string{"task1", "task2"}, updateTaskIDs(got))
			},
		},
		{
			name:  "a stale AwaitHuman with no matching record drops cleanly",
			state: staleFilterState(),
			cmds:  []Command{AwaitHuman{TaskID: "ghost"}},
			assert: func(t *testing.T, _ *InstanceState, got []Command) {
				assert.Empty(t, got)
			},
		},
		{
			name: "an already-completed record is neither overwritten nor re-updated",
			state: func() InstanceState {
				st := staleFilterState()
				done := openTask("task1")
				done.State = humantask.Completed
				st.Tasks = []humantask.HumanTask{done}
				return st
			}(),
			cmds: []Command{AwaitHuman{TaskID: "task1"}},
			assert: func(t *testing.T, s *InstanceState, got []Command) {
				assert.Equal(t, humantask.Completed, s.Tasks[0].State,
					"the IsOpen guard must not overwrite a resolved record")
				assert.Empty(t, updateTaskIDs(got), "and must not emit a second UpdateTask")
			},
		},
		{
			name: "a CLAIMED record is cancelled and its Claim survives into the UpdateTask",
			state: func() InstanceState {
				st := staleFilterState()
				held := openTask("task1")
				held.State = humantask.Claimed
				held.Claim = &humantask.Claim{Actor: authz.Actor{ID: "alice"}}
				st.Tasks = []humantask.HumanTask{held}
				return st
			}(),
			cmds: []Command{AwaitHuman{TaskID: "task1"}},
			assert: func(t *testing.T, s *InstanceState, got []Command) {
				// IsOpen covers Unclaimed AND Claimed; cancelling a task someone
				// is actively holding is the audit-relevant branch.
				assert.Equal(t, humantask.Cancelled, s.Tasks[0].State)
				require.Len(t, got, 1)
				ut, ok := got[0].(UpdateTask)
				require.True(t, ok)
				require.NotNil(t, ut.Task.Claim, "the claim must survive into the store update")
				assert.Equal(t, "alice", ut.Task.Claim.Actor.ID)
			},
		},
		{
			name: "the emitted UpdateTask shares no mutable state with the instance",
			state: func() InstanceState {
				st := staleFilterState()
				held := openTask("task1")
				held.State = humantask.Claimed
				held.Claim = &humantask.Claim{Actor: authz.Actor{ID: "alice"}}
				held.Vars = map[string]any{"amount": 100}
				st.Tasks = []humantask.HumanTask{held}
				return st
			}(),
			cmds: []Command{AwaitHuman{TaskID: "task1"}},
			assert: func(t *testing.T, s *InstanceState, got []Command) {
				require.Len(t, got, 1)
				ut, ok := got[0].(UpdateTask)
				require.True(t, ok)
				require.NotNil(t, ut.Task.Claim)

				// The command escapes into a consumer-supplied TaskStore while the
				// record it was built from is about to be committed as instance
				// state. A store that retains the value verbatim (both in-repo
				// stores happen to copy on ingest; TaskStore is public API) would
				// otherwise share the Claim pointee and the Vars map with committed
				// engine state. HumanTask.Clone is the single deep-copy definition
				// for exactly this.
				assert.NotSame(t, s.Tasks[0].Claim, ut.Task.Claim,
					"the Claim pointee must not be shared with committed state")

				ut.Task.Claim.Actor.ID = "mallory"
				assert.Equal(t, "alice", s.Tasks[0].Claim.Actor.ID,
					"mutating the escaped command must not rewrite the instance record")

				ut.Task.Vars["amount"] = 999
				assert.Equal(t, 100, s.Tasks[0].Vars["amount"],
					"the Vars map must not be shared either")
			},
		},
		{
			name: "a mixed delivery keeps relative order and substitutes in place",
			state: func() InstanceState {
				st := staleFilterState("live")
				st.Tasks = []humantask.HumanTask{openTask("task1")}
				return st
			}(),
			cmds: []Command{
				CancelTimer{TimerID: "tm1"},
				AwaitHuman{TaskID: "task1"},
				InvokeAction{CommandID: "live"},
			},
			assert: func(t *testing.T, _ *InstanceState, got []Command) {
				require.Len(t, got, 3)
				assert.Equal(t, CancelTimer{TimerID: "tm1"}, got[0])
				_, isUpdate := got[1].(UpdateTask)
				assert.True(t, isUpdate, "the UpdateTask takes the dropped AwaitHuman's slot")
				assert.Equal(t, InvokeAction{CommandID: "live"}, got[2])
			},
		},
		{
			name: "an EMPTY-id command alongside a stale one is kept while the stale one goes",
			// The empty-id command must travel WITH a filterable command, or the
			// fast path returns before the loop and stale("") is never reached —
			// which would leave the empty-id guard unpinned.
			state: staleFilterState(""),
			cmds: []Command{
				InvokeAction{CommandID: ""},
				InvokeAction{CommandID: "gone"},
			},
			assert: func(t *testing.T, _ *InstanceState, got []Command) {
				assert.Equal(t, []string{""}, invokeIDs(got),
					"a malformed id must keep failing loudly, not park the instance silently")
			},
		},
		{
			name:  "a command with an EMPTY id is kept, not dropped",
			state: staleFilterState(""),
			cmds:  []Command{InvokeAction{CommandID: ""}},
			assert: func(t *testing.T, _ *InstanceState, got []Command) {
				assert.Len(t, got, 1,
					"a malformed id must keep failing loudly, not park the instance silently")
			},
		},
		{
			name:  "a nil command slice is handled",
			state: staleFilterState(),
			cmds:  nil,
			assert: func(t *testing.T, _ *InstanceState, got []Command) {
				assert.Nil(t, got, "nil in, nil out: the fast path returns cmds untouched")
			},
		},
		{
			name:  "a state with no tokens at all still drops a stale command",
			state: InstanceState{Status: StatusRunning},
			cmds:  []Command{InvokeAction{CommandID: "c1"}},
			assert: func(t *testing.T, _ *InstanceState, got []Command) {
				assert.Empty(t, got)
			},
		},
		{
			name:  "a nil Tasks slice is handled",
			state: InstanceState{Status: StatusRunning},
			cmds:  []Command{AwaitHuman{TaskID: "task1"}},
			assert: func(t *testing.T, _ *InstanceState, got []Command) {
				assert.Empty(t, got)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			st := tc.state
			got := dropStaleTokenCommands(t.Context(), &st, tc.cmds)
			tc.assert(t, &st, got)
		})
	}
}

// TestDropStaleTokenCommandsLogsDrop pins the filter's observability contract:
// every suppressed command emits ONE Warn record naming the instance, the
// command kind and the correlation id, and nothing else emits one.
//
// A dropped command has no other trace anywhere — no error, no event, no history
// entry — so without this record an operator investigating "why was this refund
// never invoked?" has nothing to work from. This is the site class Step's own doc
// comment reserves ctx for, and drive already follows the convention
// at step.go:187.
//
// No ctx modifier: ctx reaches the filter for slog correlation only, and no
// branch of it consults Done or Err — cancellation cannot change its output.
//
// Sequential by construction (no t.Parallel anywhere): installCaptureHandler
// swaps the process-global slog.Default.
func TestDropStaleTokenCommandsLogsDrop(t *testing.T) {
	type testCase struct {
		name   string
		state  InstanceState
		cmds   []Command
		assert func(t *testing.T, h *captureHandler, got []Command)
	}

	const dropMsg = "dropping command whose awaiter this step cancelled"

	cases := []testCase{
		{
			name: "a dropped InvokeAction is logged with its kind and correlation id",
			state: func() InstanceState {
				st := staleFilterState()
				st.InstanceID = "i1"
				return st
			}(),
			cmds: []Command{InvokeAction{CommandID: "c1", Name: "charge"}},
			assert: func(t *testing.T, h *captureHandler, got []Command) {
				require.Empty(t, got, "positive control: the command really was dropped")

				rec, ok := h.find(dropMsg)
				require.True(t, ok, "a suppressed side effect must leave a trace")
				assert.Equal(t, slog.LevelWarn, rec.Level)

				instanceID, ok := attrString(rec, "instance_id")
				assert.True(t, ok, "expected instance_id attribute")
				assert.Equal(t, "i1", instanceID)

				kind, ok := attrString(rec, "command_kind")
				assert.True(t, ok, "expected command_kind attribute")
				assert.Equal(t, "InvokeAction", kind)

				correlationID, ok := attrString(rec, "correlation_id")
				assert.True(t, ok, "expected correlation_id attribute")
				assert.Equal(t, "c1", correlationID)
			},
		},
		{
			name: "a dropped StartSubInstance is logged under its own kind",
			state: func() InstanceState {
				st := staleFilterState()
				st.InstanceID = "i2"
				return st
			}(),
			cmds: []Command{StartSubInstance{CommandID: "c9"}},
			assert: func(t *testing.T, h *captureHandler, got []Command) {
				require.Empty(t, got, "positive control: the command really was dropped")

				rec, ok := h.find(dropMsg)
				require.True(t, ok)
				kind, _ := attrString(rec, "command_kind")
				assert.Equal(t, "StartSubInstance", kind)
				correlationID, _ := attrString(rec, "correlation_id")
				assert.Equal(t, "c9", correlationID)
			},
		},
		{
			name: "a dropped AwaitHuman is logged with its task id as the correlation id",
			state: func() InstanceState {
				st := staleFilterState()
				st.InstanceID = "i3"
				st.Tasks = []humantask.HumanTask{openTask("task1")}
				return st
			}(),
			cmds: []Command{AwaitHuman{TaskID: "task1"}},
			assert: func(t *testing.T, h *captureHandler, got []Command) {
				require.Equal(t, []string{"task1"}, updateTaskIDs(got),
					"positive control: the AwaitHuman really was substituted")

				rec, ok := h.find(dropMsg)
				require.True(t, ok)
				kind, _ := attrString(rec, "command_kind")
				assert.Equal(t, "AwaitHuman", kind)
				correlationID, _ := attrString(rec, "correlation_id")
				assert.Equal(t, "task1", correlationID)
			},
		},
		{
			name: "a surviving command logs nothing",
			state: func() InstanceState {
				st := staleFilterState("c1")
				st.InstanceID = "i4"
				return st
			}(),
			cmds: []Command{InvokeAction{CommandID: "c1", Name: "charge"}},
			assert: func(t *testing.T, h *captureHandler, got []Command) {
				require.Len(t, got, 1, "positive control: the command really did survive")

				_, ok := h.find(dropMsg)
				assert.False(t, ok, "a kept command is not an anomaly")
			},
		},
		{
			name: "the fast path logs nothing",
			state: func() InstanceState {
				st := staleFilterState()
				st.InstanceID = "i5"
				return st
			}(),
			cmds: []Command{CancelTimer{TimerID: "tm1"}},
			assert: func(t *testing.T, h *captureHandler, got []Command) {
				require.Len(t, got, 1, "positive control: the never-filtered kind survived")

				_, ok := h.find(dropMsg)
				assert.False(t, ok)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := installCaptureHandler(t)

			st := tc.state
			got := dropStaleTokenCommands(t.Context(), &st, tc.cmds)
			tc.assert(t, h, got)
		})
	}
}

// BenchmarkDropStaleTokenCommandsFastPath measures the cheapest shape the filter
// sees: a delivery carrying only kinds it can never touch, so the pre-scan
// returns cmds untouched. This must stay allocation-free.
//
// It is NOT the typical step — see BenchmarkDropStaleTokenCommandsLiveAwaiter
// for that — but it is the shape a pure timer/task-reconciliation delivery
// (TimerFired rescheduling a recurring timer, a terminal cancelOpenTasks sweep)
// really produces, and it is the only shape the pre-scan exists to protect.
func BenchmarkDropStaleTokenCommandsFastPath(b *testing.B) {
	s := staleFilterState("c1", "c2", "c3", "c4")
	cmds := []Command{
		ScheduleTimer{TimerID: "tm1"},
		CancelTimer{TimerID: "tm0"},
		UpdateTask{Task: openTask("task1")},
		SendMessage{Name: "msg"},
	}
	b.ReportAllocs()
	for b.Loop() {
		_ = dropStaleTokenCommands(b.Context(), &s, cmds)
	}
}

// BenchmarkDropStaleTokenCommandsLiveAwaiter measures the TYPICAL step: one
// filterable command whose awaiter is live. Every ServiceTask entry, retry
// re-invoke, UserTask entry, CallActivity entry and completion-action park emits
// exactly one of these, so this — not the fast path — is what Step pays on a
// normal non-trivial trigger: the pre-scan misses, the awaiter map is built and
// the slice is copied, all to keep every command.
func BenchmarkDropStaleTokenCommandsLiveAwaiter(b *testing.B) {
	s := staleFilterState("c1", "c2", "c3", "c4")
	cmds := []Command{
		InvokeAction{CommandID: "c1", Name: "charge"},
		ScheduleTimer{TimerID: "tm1"},
	}
	b.ReportAllocs()
	for b.Loop() {
		_ = dropStaleTokenCommands(b.Context(), &s, cmds)
	}
}

// BenchmarkDropStaleTokenCommandsFiltering measures the rare path: a delivery
// that actually contains a stale command and must be rebuilt.
//
// The default logger is swapped for a discarding one so the per-drop Warn record
// does not turn this into a measurement of the test binary's stderr. Production
// pays its handler's cost on top of the number reported here — acceptable
// because a drop is by construction rare (it needs a step that both emitted a
// parking command and then destroyed its awaiter).
func BenchmarkDropStaleTokenCommandsFiltering(b *testing.B) {
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.DiscardHandler))
	b.Cleanup(func() { slog.SetDefault(prev) })

	s := staleFilterState("c1", "c2", "c3", "c4")
	cmds := []Command{
		ScheduleTimer{TimerID: "tm1"},
		InvokeAction{CommandID: "gone"},
		UpdateTask{Task: openTask("task1")},
	}
	b.ReportAllocs()
	for b.Loop() {
		_ = dropStaleTokenCommands(b.Context(), &s, cmds)
	}
}

package engine_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/humantask"
)

// userTaskDef returns a linear definition with a single user-task node between
// start and end.
//
//	Start → UserTask(approve) → End
func userTaskDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-ht", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("approve", activity.WithEligibleRoles("manager"), activity.WithEligibleExpr(`actor.ID != ""`)),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "approve"},
			{ID: "f2", Source: "approve", Target: "end"},
		},
	}
}

// startUserTask is a helper that drives the instance from zero to the parked
// user-task state and returns that StepResult.
func startUserTask(t *testing.T, def *model.ProcessDefinition, at time.Time) engine.StepResult {
	t.Helper()
	r, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, map[string]any{"region": "APAC"}), engine.StepOptions{})
	require.NoError(t, err)
	return r
}

// TestUserTaskEmitsAwaitHumanAndParks verifies that driving into a KindUserTask
// node emits AwaitHuman (with the correct eligibility spec), creates an Unclaimed
// HumanTask in state, and parks the token (TokenWaiting with the TaskID
// in AwaitCommand). The instance must still be running.
func TestUserTaskEmitsAwaitHumanAndParks(t *testing.T) {
	at := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)
	def := userTaskDef()

	res := startUserTask(t, def, at)

	// Exactly one command: AwaitHuman.
	require.Len(t, res.Commands, 1)
	ah, ok := res.Commands[0].(engine.AwaitHuman)
	require.True(t, ok, "expected AwaitHuman command, got %T", res.Commands[0])

	// TaskID must be deterministic: <instanceID>-h<seq> where seq starts at 1.
	assert.Equal(t, "i1-h1", ah.TaskID)

	// Eligibility is built from the node definition.
	assert.Equal(t, []string{"manager"}, ah.Eligibility.Roles)
	assert.Equal(t, `actor.ID != ""`, ah.Eligibility.Attribute)

	// Token parked on the user task node.
	require.Len(t, res.State.Tokens, 1)
	tok := res.State.Tokens[0]
	assert.Equal(t, "approve", tok.NodeID)
	assert.Equal(t, engine.TokenWaiting, tok.State)
	assert.Equal(t, "i1-h1", tok.AwaitCommand)

	// Instance still running.
	assert.Equal(t, engine.StatusRunning, res.State.Status)

	// HumanTask record created in state.
	require.Len(t, res.State.Tasks, 1)
	ht := res.State.Tasks[0]
	assert.Equal(t, "i1-h1", ht.TaskID)
	assert.Equal(t, "i1", ht.InstanceID)
	assert.Equal(t, "approve", ht.NodeID)
	assert.Equal(t, humantask.Unclaimed, ht.State)
	assert.Equal(t, []string{"manager"}, ht.Eligibility.Roles)
	assert.Equal(t, `actor.ID != ""`, ht.Eligibility.Attribute)
	assert.Equal(t, at, ht.CreatedAt)
}

// TestUserTaskPrivilegesFlowToAwaitHuman verifies that EligiblePrivileges set
// via activity.WithEligiblePrivileges flow through to the AwaitHuman command's
// Eligibility.Privileges field and the HumanTask stored in engine state.
func TestUserTaskPrivilegesFlowToAwaitHuman(t *testing.T) {
	at := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)
	privs := []string{"finance-task claim"}
	def := &model.ProcessDefinition{
		ID: "p-priv", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("approve",
				activity.WithEligiblePrivileges(privs...),
			),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "approve"},
			{ID: "f2", Source: "approve", Target: "end"},
		},
	}

	res, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i-priv"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)

	require.Len(t, res.Commands, 1)
	ah, ok := res.Commands[0].(engine.AwaitHuman)
	require.True(t, ok, "expected AwaitHuman, got %T", res.Commands[0])

	// Privileges must be carried in the AwaitHuman eligibility spec.
	assert.Equal(t, privs, ah.Eligibility.Privileges, "Eligibility.Privileges mismatch")
	assert.Empty(t, ah.Eligibility.Roles, "Roles should be empty (none set)")

	// HumanTask in state must also carry the Privileges.
	require.Len(t, res.State.Tasks, 1)
	assert.Equal(t, privs, res.State.Tasks[0].Eligibility.Privileges)
}

// TestUserTaskEmitsAwaitHumanAndParks_SecondTask verifies that a second user-task
// on the same instance gets TaskID "i1-h2" (TaskSeq increments).
func TestUserTaskTaskSeqIncrements(t *testing.T) {
	at := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)

	// Definition with two sequential user tasks.
	def := &model.ProcessDefinition{
		ID: "p-ht2", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("task1", activity.WithEligibleRoles("a")),
			activity.NewUserTask("task2", activity.WithEligibleRoles("b")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "task1"},
			{ID: "f2", Source: "task1", Target: "task2"},
			{ID: "f3", Source: "task2", Target: "end"},
		},
	}

	// Start → parked at task1.
	r1, err := engine.Step(t.Context(), def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.Commands, 1)
	ah1 := r1.Commands[0].(engine.AwaitHuman)
	assert.Equal(t, "i1-h1", ah1.TaskID)

	// Complete task1 → drives to task2.
	actor := authz.Actor{ID: "alice", Roles: []string{"a"}}
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewHumanCompleted(at.Add(time.Minute), "i1-h1", engine.CompletionInput{Output: map[string]any{"ok": true}}, actor),
		engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r2.Commands, 2) // UpdateTask + AwaitHuman
	// Find AwaitHuman.
	var ah2 engine.AwaitHuman
	for _, c := range r2.Commands {
		if a, ok := c.(engine.AwaitHuman); ok {
			ah2 = a
		}
	}
	assert.Equal(t, "i1-h2", ah2.TaskID)
}

// TestHumanClaimedUpdatesTask verifies that a HumanClaimed trigger records the
// Claim and sets State=Claimed on the task, emits UpdateTask, and does NOT move
// the token.
func TestHumanClaimedUpdatesTask(t *testing.T) {
	at := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)
	def := userTaskDef()
	actor := authz.Actor{ID: "bob", Roles: []string{"manager"}}

	r1 := startUserTask(t, def, at)
	taskID := "i1-h1"
	tokenBefore := r1.State.Tokens[0]

	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewHumanClaimed(at.Add(time.Minute), taskID, actor),
		engine.StepOptions{})
	require.NoError(t, err)

	// Exactly one command: UpdateTask.
	require.Len(t, r2.Commands, 1)
	ut, ok := r2.Commands[0].(engine.UpdateTask)
	require.True(t, ok, "expected UpdateTask, got %T", r2.Commands[0])
	assert.Equal(t, taskID, ut.Task.TaskID)
	require.NotNil(t, ut.Task.Claim)
	assert.Equal(t, "bob", ut.Task.Claim.Actor.ID)
	assert.Equal(t, humantask.Claimed, ut.Task.State)

	// Task in state also updated.
	require.Len(t, r2.State.Tasks, 1)
	ht := r2.State.Tasks[0]
	require.NotNil(t, ht.Claim)
	assert.Equal(t, "bob", ht.Claim.Actor.ID)
	assert.Equal(t, humantask.Claimed, ht.State)

	// Token unchanged (same nodeID, state, awaitCommand).
	require.Len(t, r2.State.Tokens, 1)
	tokAfter := r2.State.Tokens[0]
	assert.Equal(t, tokenBefore.NodeID, tokAfter.NodeID)
	assert.Equal(t, tokenBefore.State, tokAfter.State)
	assert.Equal(t, tokenBefore.AwaitCommand, tokAfter.AwaitCommand)

	// Instance still running.
	assert.Equal(t, engine.StatusRunning, r2.State.Status)
}

// TestHumanClaimedUnknownTaskIDErrors verifies that claiming a non-existent
// task id returns ErrTokenNotFound.
func TestHumanClaimedUnknownTaskIDErrors(t *testing.T) {
	at := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)
	def := userTaskDef()
	actor := authz.Actor{ID: "bob", Roles: []string{"manager"}}

	r1 := startUserTask(t, def, at)

	_, err := engine.Step(t.Context(), def, r1.State,
		engine.NewHumanClaimed(at.Add(time.Minute), "no-such-token", actor),
		engine.StepOptions{})
	require.ErrorIs(t, err, engine.ErrTokenNotFound)
}

// TestHumanReassignedUpdatesTask verifies that HumanReassigned overwrites the Claim
// to To, keeps State=Claimed, and emits UpdateTask. No token movement.
func TestHumanReassignedUpdatesTask(t *testing.T) {
	at := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)
	def := userTaskDef()

	r1 := startUserTask(t, def, at)
	taskID := "i1-h1"

	// First claim it.
	alice := authz.Actor{ID: "alice", Roles: []string{"manager"}}
	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewHumanClaimed(at.Add(time.Minute), taskID, alice),
		engine.StepOptions{})
	require.NoError(t, err)

	// Now reassign from alice → bob.
	by := authz.Actor{ID: "admin"}
	r3, err := engine.Step(t.Context(), def, r2.State,
		engine.NewHumanReassigned(at.Add(2*time.Minute), taskID, "alice", "bob", by),
		engine.StepOptions{})
	require.NoError(t, err)

	// Exactly one command: UpdateTask.
	require.Len(t, r3.Commands, 1)
	ut, ok := r3.Commands[0].(engine.UpdateTask)
	require.True(t, ok, "expected UpdateTask, got %T", r3.Commands[0])
	require.NotNil(t, ut.Task.Claim)
	assert.Equal(t, "bob", ut.Task.Claim.Actor.ID)
	assert.Equal(t, humantask.Claimed, ut.Task.State)

	// State reflects reassignment.
	require.Len(t, r3.State.Tasks, 1)
	require.NotNil(t, r3.State.Tasks[0].Claim)
	assert.Equal(t, "bob", r3.State.Tasks[0].Claim.Actor.ID)
	assert.Equal(t, humantask.Claimed, r3.State.Tasks[0].State)

	// Token still parked.
	require.Len(t, r3.State.Tokens, 1)
	assert.Equal(t, engine.TokenWaiting, r3.State.Tokens[0].State)
}

// TestHumanReassignedUnknownTaskIDErrors verifies that reassigning a
// non-existent task id returns ErrTokenNotFound.
func TestHumanReassignedUnknownTaskIDErrors(t *testing.T) {
	at := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)
	def := userTaskDef()
	by := authz.Actor{ID: "admin"}

	r1 := startUserTask(t, def, at)

	_, err := engine.Step(t.Context(), def, r1.State,
		engine.NewHumanReassigned(at.Add(time.Minute), "no-such-token", "alice", "bob", by),
		engine.StepOptions{})
	require.ErrorIs(t, err, engine.ErrTokenNotFound)
}

// TestHumanCompletedAdvancesAndAudits verifies that HumanCompleted:
//   - merges Output into variables,
//   - sets the user-task NodeVisit's ActorID to the completing actor,
//   - marks the task Completed,
//   - advances the token past the user-task to End (→ CompleteInstance),
//   - emits UpdateTask (with Completed state) followed (in any order) by
//     CompleteInstance.
func TestHumanCompletedAdvancesAndAudits(t *testing.T) {
	at := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)
	def := userTaskDef()
	actor := authz.Actor{ID: "carol", Roles: []string{"manager"}}

	r1 := startUserTask(t, def, at)
	taskID := "i1-h1"
	doneAt := at.Add(5 * time.Minute)

	r2, err := engine.Step(t.Context(), def, r1.State,
		engine.NewHumanCompleted(doneAt, taskID, engine.CompletionInput{Output: map[string]any{"approved": true}}, actor),
		engine.StepOptions{})
	require.NoError(t, err)

	// Commands: UpdateTask + CompleteInstance (order: UpdateTask first, then drive
	// produces CompleteInstance — but we search by type for robustness).
	require.Len(t, r2.Commands, 2)

	var utCmd engine.UpdateTask
	var ciCmd engine.CompleteInstance
	var foundUT, foundCI bool
	for _, c := range r2.Commands {
		switch v := c.(type) {
		case engine.UpdateTask:
			utCmd = v
			foundUT = true
		case engine.CompleteInstance:
			ciCmd = v
			foundCI = true
		}
	}
	require.True(t, foundUT, "UpdateTask not found in commands")
	require.True(t, foundCI, "CompleteInstance not found in commands")

	// UpdateTask carries Completed state.
	assert.Equal(t, taskID, utCmd.Task.TaskID)
	assert.Equal(t, humantask.Completed, utCmd.Task.State)

	// CompleteInstance carries merged vars.
	assert.Equal(t, true, ciCmd.Result["approved"])

	// State: instance completed, no tokens.
	assert.Equal(t, engine.StatusCompleted, r2.State.Status)
	assert.Empty(t, r2.State.Tokens)
	require.NotNil(t, r2.State.EndedAt)

	// Variables merged.
	assert.Equal(t, true, r2.State.Variables["approved"])

	// Task in state is Completed.
	require.Len(t, r2.State.Tasks, 1)
	assert.Equal(t, humantask.Completed, r2.State.Tasks[0].State)

	// The CLOSED visit for "approve" links to the completed task, which is where
	// the completion audit (who/when/outcome) lives (ADR-0145). Using the closed
	// visit is unambiguous even if the same node is visited more than once.
	var approveVisit *engine.NodeVisit
	for i := range r2.State.History {
		v := &r2.State.History[i]
		if v.NodeID == "approve" && v.LeftAt != nil {
			approveVisit = v
			break
		}
	}
	require.NotNil(t, approveVisit, "closed NodeVisit for 'approve' not found")
	assert.Equal(t, r2.State.Tasks[0].TaskID, approveVisit.TaskID)
	// A normal completion is not an abnormal close, so no close reason is stamped.
	assert.Empty(t, approveVisit.CloseKind)
}

// TestHumanTaskTriggersAgreeOnUnknownTaskID pins that ALL FOUR human-task
// triggers report the SAME sentinel for the same condition — an id this instance
// has no task record for and no token parked on — so a consumer cannot get two
// different HTTP statuses for one id.
//
// They converge on engine.ErrTokenNotFound (→ ErrInvalidTransition → 422), not on
// humantask.ErrTaskNotFound (→ 404), and the reason is what can actually REACH
// this branch. service.deliverTaskTrigger reads the task store FIRST
// (service/service.go), so a genuinely unknown id is answered
// humantask.ErrTaskNotFound there and never reaches the engine. The engine's
// branch therefore fires only for a GHOST — an id the store knows and instance
// state does not — which is a state conflict, not a missing task. Reporting 404
// for a task the store still holds would be actively misleading.
//
// ⚠ What makes this test able to fail: ADR-0165 Phase 5 briefly moved
// HumanCompleted to humantask.ErrTaskNotFound, and a first pass at this
// convergence moved the other three with it; every row then failed the ErrorIs
// assertion. Phase 6.4 converged the other way instead. See ADR-0165's
// correction block.
//
// The instance is RUNNING, so dispatch's terminal guard is not involved — this is
// purely the "no such task record" key.
//
// This subsumes the former single-trigger TestHumanCompletedUnknownTaskIDErrors:
// its HumanCompleted row runs the identical scenario, and keeping both would have
// left two tests asserting one behaviour with rationales free to drift apart.
// The neighbouring TestHumanCompletedMissingTaskRecordErrors is NOT subsumed — it
// covers the other half of the no-record branch, where a token IS parked.
func TestHumanTaskTriggersAgreeOnUnknownTaskID(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)
	actor := authz.Actor{ID: "carol", Roles: []string{"manager"}}

	type testCase struct {
		name    string
		trigger engine.Trigger
	}

	cases := []testCase{
		{
			name:    "HumanClaimed",
			trigger: engine.NewHumanClaimed(at.Add(time.Minute), "no-such-task", actor),
		},
		{
			name:    "HumanReassigned",
			trigger: engine.NewHumanReassigned(at.Add(time.Minute), "no-such-task", "carol", "dave", actor),
		},
		{
			name:    "HumanCandidatesResolved",
			trigger: engine.NewHumanCandidatesResolved(at.Add(time.Minute), "no-such-task", []authz.Actor{actor}),
		},
		{
			name: "HumanCompleted",
			trigger: engine.NewHumanCompleted(at.Add(time.Minute), "no-such-task",
				engine.CompletionInput{Output: map[string]any{"approved": true}}, actor),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			def := userTaskDef()
			r1 := startUserTask(t, def, at)

			_, err := engine.Step(t.Context(), def, r1.State, tc.trigger, engine.StepOptions{})

			require.ErrorIs(t, err, engine.ErrTokenNotFound,
				"an id with no task record and no parked token is a wrong-state transition")
			require.ErrorIs(t, err, engine.ErrInvalidTransition,
				"it must classify 422 through ErrInvalidTransition, not 404")
			assert.NotErrorIs(t, err, humantask.ErrTaskNotFound,
				"reaching this branch through the service means the STORE still holds the task "+
					"(deliverTaskTrigger short-circuits a genuinely unknown id), so 404 would "+
					"deny a task that exists")
		})
	}
}

// TestHumanCompletedMissingTaskRecordErrors verifies that HumanCompleted fails fast
// when a parked token is found for the given TaskID but the corresponding
// HumanTask record is absent from state (invariant violation / data corruption).
//
// The test constructs state by hand: a token parked awaiting "i1-h1" but Tasks is
// empty (simulating the corrupted case). Expect an error wrapping
// humantask.ErrTaskNotFound, and the token must NOT advance (state unchanged,
// no CompleteInstance command).
func TestHumanCompletedMissingTaskRecordErrors(t *testing.T) {
	at := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)
	def := userTaskDef()
	actor := authz.Actor{ID: "carol", Roles: []string{"manager"}}

	// Build corrupted state: token is parked awaiting the task id, but no
	// HumanTask record exists in Tasks.
	corruptedState := engine.InstanceState{
		InstanceID: "i1",
		Status:     engine.StatusRunning,
		Tokens: []engine.Token{
			{
				ID:           "i1-t1",
				NodeID:       "approve",
				State:        engine.TokenWaiting,
				AwaitCommand: "i1-h1",
			},
		},
		// Tasks deliberately empty — no matching HumanTask record.
		History: []engine.NodeVisit{
			{NodeID: "approve", TokenID: "i1-t1", EnteredAt: at},
		},
	}

	_, err := engine.Step(t.Context(), def, corruptedState,
		engine.NewHumanCompleted(at.Add(time.Minute), "i1-h1", engine.CompletionInput{Output: map[string]any{"approved": true}}, actor),
		engine.StepOptions{})

	// Must error, wrapping humantask.ErrTaskNotFound.
	require.Error(t, err)
	require.ErrorIs(t, err, humantask.ErrTaskNotFound,
		"expected error wrapping humantask.ErrTaskNotFound, got: %v", err)
}

// TestUserTaskVisitCarriesTaskID verifies the open node visit for a parked
// user task links to the minted human task (ADR-0145), so the rendered history
// can resolve the actor/outcome from the task record rather than duplicating it.
func TestUserTaskVisitCarriesTaskID(t *testing.T) {
	at := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)

	res := startUserTask(t, userTaskDef(), at)

	require.Len(t, res.State.Tasks, 1)
	taskID := res.State.Tasks[0].TaskID

	var visit *engine.NodeVisit
	for i := range res.State.History {
		if res.State.History[i].NodeID == "approve" {
			visit = &res.State.History[i]
		}
	}
	require.NotNil(t, visit, "history has no visit for the user-task node")
	assert.Equal(t, taskID, visit.TaskID, "user-task visit links to its task")

	for _, v := range res.State.History {
		if v.NodeID != "approve" {
			assert.Empty(t, v.TaskID, "non-user-task visit %q must not carry a task link", v.NodeID)
		}
	}
}

// TestHumanTaskAuditRecording verifies the ADR-0146/0147 audit trail the engine
// stamps onto the HumanTask: a claim records the full claiming actor and the
// trigger's occurrence time, a reassignment overwrites that claim, and a
// completion records the completing actor, time, outcome and note. These are the
// values the instance view renders, so each must survive on both the emitted
// UpdateTask command and the returned state.
func TestHumanTaskAuditRecording(t *testing.T) {
	at := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)
	claimAt := at.Add(time.Minute)
	doneAt := at.Add(5 * time.Minute)
	alice := authz.Actor{ID: "alice", Roles: []string{"manager"}, Attributes: map[string]any{"email": "alice@acme.com"}}
	bob := authz.Actor{ID: "bob", Roles: []string{"manager"}}

	type testCase struct {
		name    string
		trigger func(taskID string) engine.Trigger
		// prior is applied before the trigger under test, if non-nil.
		prior  func(taskID string) engine.Trigger
		assert func(t *testing.T, task humantask.HumanTask)
	}

	cases := []testCase{
		{
			name:    "claim records the full actor and the trigger time",
			trigger: func(taskID string) engine.Trigger { return engine.NewHumanClaimed(claimAt, taskID, alice) },
			assert: func(t *testing.T, task humantask.HumanTask) {
				require.NotNil(t, task.Claim)
				assert.Equal(t, alice, task.Claim.Actor)
				assert.Equal(t, claimAt, task.Claim.At)
				assert.Nil(t, task.Completion)
			},
		},
		{
			name:  "reassignment overwrites the claim with the new assignee",
			prior: func(taskID string) engine.Trigger { return engine.NewHumanClaimed(claimAt, taskID, alice) },
			trigger: func(taskID string) engine.Trigger {
				return engine.NewHumanReassigned(claimAt.Add(time.Minute), taskID, "alice", "bob", alice)
			},
			assert: func(t *testing.T, task humantask.HumanTask) {
				require.NotNil(t, task.Claim)
				assert.Equal(t, "bob", task.Claim.Actor.ID)
				assert.Equal(t, claimAt.Add(time.Minute), task.Claim.At)
			},
		},
		{
			name:  "completion records actor, time, outcome and note",
			prior: func(taskID string) engine.Trigger { return engine.NewHumanClaimed(claimAt, taskID, bob) },
			trigger: func(taskID string) engine.Trigger {
				return engine.NewHumanCompleted(doneAt, taskID,
					engine.CompletionInput{Outcome: "approve", Note: "looks good", Output: map[string]any{"ok": true}}, bob)
			},
			assert: func(t *testing.T, task humantask.HumanTask) {
				require.NotNil(t, task.Completion)
				assert.Equal(t, bob, task.Completion.Actor)
				assert.Equal(t, doneAt, task.Completion.At)
				assert.Equal(t, "approve", task.Completion.Outcome)
				assert.Equal(t, "looks good", task.Completion.Note)
				// The claim from before completion is preserved.
				require.NotNil(t, task.Claim)
				assert.Equal(t, "bob", task.Claim.Actor.ID)
			},
		},
		{
			name: "completion without an outcome records an empty outcome and note",
			trigger: func(taskID string) engine.Trigger {
				return engine.NewHumanCompleted(doneAt, taskID, engine.CompletionInput{Output: map[string]any{"ok": true}}, bob)
			},
			assert: func(t *testing.T, task humantask.HumanTask) {
				require.NotNil(t, task.Completion)
				assert.Empty(t, task.Completion.Outcome)
				assert.Empty(t, task.Completion.Note)
				assert.Equal(t, bob, task.Completion.Actor)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			def := userTaskDef()
			state := startUserTask(t, def, at).State
			taskID := state.Tasks[0].TaskID

			if tc.prior != nil {
				r, err := engine.Step(t.Context(), def, state, tc.prior(taskID), engine.StepOptions{})
				require.NoError(t, err)
				state = r.State
			}

			res, err := engine.Step(t.Context(), def, state, tc.trigger(taskID), engine.StepOptions{})
			require.NoError(t, err)

			// The audit must be visible on the emitted UpdateTask command...
			var updated *humantask.HumanTask
			for _, c := range res.Commands {
				if ut, ok := c.(engine.UpdateTask); ok {
					task := ut.Task
					updated = &task
				}
			}
			require.NotNil(t, updated, "expected an UpdateTask command")
			tc.assert(t, *updated)

			// ...and on the task carried in the returned state.
			require.Len(t, res.State.Tasks, 1)
			tc.assert(t, res.State.Tasks[0])
		})
	}
}

// TestHumanCandidatesResolved verifies the trigger that carries the runtime's
// resolved candidate actors into the committed snapshot (ADR-0147). The engine
// core cannot resolve candidates itself — resolution is I/O — so the runtime
// resolves and feeds the result back as a trigger, exactly as it does for a
// completed service action.
//
// Semantics under test: the list is REPLACED (never merged), applying the same
// resolution twice is idempotent, the token never moves, an unknown task is
// rejected, and a closed task is left frozen so a late refresh cannot rewrite a
// completed task's audit.
func TestHumanCandidatesResolved(t *testing.T) {
	at := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)
	resolveAt := at.Add(time.Second)
	jane := authz.Actor{ID: "u-jane", Roles: []string{"manager"}, Attributes: map[string]any{"email": "jane@acme.com"}}
	mike := authz.Actor{ID: "u-mike", Roles: []string{"manager"}}
	raj := authz.Actor{ID: "u-raj", Roles: []string{"finance"}}

	type testCase struct {
		name string
		// prior triggers are applied before the trigger under test.
		prior   func(taskID string) []engine.Trigger
		trigger func(taskID string) engine.Trigger
		assert  func(t *testing.T, res engine.StepResult, err error)
	}

	cases := []testCase{
		{
			name: "resolved actors land on the task and are emitted as UpdateTask",
			trigger: func(taskID string) engine.Trigger {
				return engine.NewHumanCandidatesResolved(resolveAt, taskID, []authz.Actor{jane, mike})
			},
			assert: func(t *testing.T, res engine.StepResult, err error) {
				require.NoError(t, err)
				require.Len(t, res.State.Tasks, 1)
				assert.Equal(t, []authz.Actor{jane, mike}, res.State.Tasks[0].Candidates)

				require.Len(t, res.Commands, 1)
				ut, ok := res.Commands[0].(engine.UpdateTask)
				require.True(t, ok, "expected UpdateTask, got %T", res.Commands[0])
				assert.Equal(t, []authz.Actor{jane, mike}, ut.Task.Candidates)

				// The token must not move: resolution is bookkeeping, not progress.
				require.Len(t, res.State.Tokens, 1)
				assert.Equal(t, engine.TokenWaiting, res.State.Tokens[0].State)
			},
		},
		{
			name: "a later resolution replaces the list rather than merging it",
			prior: func(taskID string) []engine.Trigger {
				return []engine.Trigger{engine.NewHumanCandidatesResolved(resolveAt, taskID, []authz.Actor{jane, mike})}
			},
			trigger: func(taskID string) engine.Trigger {
				return engine.NewHumanCandidatesResolved(resolveAt.Add(time.Hour), taskID, []authz.Actor{raj})
			},
			assert: func(t *testing.T, res engine.StepResult, err error) {
				require.NoError(t, err)
				assert.Equal(t, []authz.Actor{raj}, res.State.Tasks[0].Candidates)
			},
		},
		{
			name: "re-applying the same resolution is idempotent",
			prior: func(taskID string) []engine.Trigger {
				return []engine.Trigger{engine.NewHumanCandidatesResolved(resolveAt, taskID, []authz.Actor{jane})}
			},
			trigger: func(taskID string) engine.Trigger {
				return engine.NewHumanCandidatesResolved(resolveAt, taskID, []authz.Actor{jane})
			},
			assert: func(t *testing.T, res engine.StepResult, err error) {
				require.NoError(t, err)
				assert.Equal(t, []authz.Actor{jane}, res.State.Tasks[0].Candidates)
			},
		},
		{
			name: "an unknown task is rejected",
			trigger: func(string) engine.Trigger {
				return engine.NewHumanCandidatesResolved(resolveAt, "no-such-task", []authz.Actor{jane})
			},
			assert: func(t *testing.T, _ engine.StepResult, err error) {
				require.ErrorIs(t, err, engine.ErrTokenNotFound)
			},
		},
		{
			name: "a closed task is left frozen",
			prior: func(taskID string) []engine.Trigger {
				return []engine.Trigger{
					engine.NewHumanCandidatesResolved(resolveAt, taskID, []authz.Actor{jane}),
					engine.NewHumanCompleted(resolveAt.Add(time.Minute), taskID, engine.CompletionInput{}, jane),
				}
			},
			trigger: func(taskID string) engine.Trigger {
				return engine.NewHumanCandidatesResolved(resolveAt.Add(time.Hour), taskID, []authz.Actor{raj})
			},
			assert: func(t *testing.T, res engine.StepResult, err error) {
				require.NoError(t, err)
				assert.Empty(t, res.Commands, "a frozen task must not emit an UpdateTask")
				require.Len(t, res.State.Tasks, 1)
				assert.Equal(t, []authz.Actor{jane}, res.State.Tasks[0].Candidates,
					"a completed task's candidate audit must not be rewritten")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			def := userTaskDef()
			state := startUserTask(t, def, at).State
			taskID := state.Tasks[0].TaskID

			if tc.prior != nil {
				for _, p := range tc.prior(taskID) {
					r, err := engine.Step(t.Context(), def, state, p, engine.StepOptions{})
					require.NoError(t, err)
					state = r.State
				}
			}

			res, err := engine.Step(t.Context(), def, state, tc.trigger(taskID), engine.StepOptions{})
			tc.assert(t, res, err)
		})
	}
}

// TestUserTaskSnapshotsVariables verifies that the task the engine mints carries
// a snapshot of the process variables at creation time. HumanTask.Vars backs
// attribute-based eligibility predicates (e.g. vars["region"] == "EU"), and every
// UpdateTask command round-trips the engine's task through the task store — so a
// task created without Vars silently erases them from the store on the first
// update, breaking authorization for the rest of the task's life.
//
// The snapshot must be independent of the live variable map: later writes to
// instance variables must not retroactively change what the task was evaluated
// against.
func TestUserTaskSnapshotsVariables(t *testing.T) {
	at := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)
	def := userTaskDef()

	res := startUserTask(t, def, at)
	require.Len(t, res.State.Tasks, 1)
	task := res.State.Tasks[0]

	assert.Equal(t, map[string]any{"region": "APAC"}, task.Vars,
		"the task must snapshot the variables present at creation time")

	// Mutating the instance variables must not reach the task's snapshot.
	res.State.Variables["region"] = "EU"
	assert.Equal(t, "APAC", res.State.Tasks[0].Vars["region"],
		"the snapshot must not alias the live variable map")
}

// TestHumanCandidatesResolvedIsolation covers two defences the rule-#9 audit
// required on the candidate-resolution handler.
//
// Terminal guard: every other externally-arriving trigger no-ops on a terminal
// instance (see handleTimerFired). Without the guard, a sibling branch that fails
// or terminates the instance in the same step still leaves an open task, so a
// late resolution would commit a new version — and re-upsert the task — for a
// dead instance.
//
// Defensive ingest: the handler must not alias the caller's slice into committed
// instance state, or a caller that reuses its slice mutates the audit record.
func TestHumanCandidatesResolvedIsolation(t *testing.T) {
	at := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)

	t.Run("a terminal instance is left untouched", func(t *testing.T) {
		def := userTaskDef()
		state := startUserTask(t, def, at).State
		taskID := state.Tasks[0].TaskID

		// Cancel the instance: it becomes terminal while the task record survives.
		cancelled, err := engine.Step(t.Context(), def, state,
			engine.NewCancelRequested(at.Add(time.Minute)), engine.StepOptions{})
		require.NoError(t, err)
		require.True(t, cancelled.State.Status.IsTerminal(), "precondition: instance must be terminal")

		res, err := engine.Step(t.Context(), def, cancelled.State,
			engine.NewHumanCandidatesResolved(at.Add(2*time.Minute), taskID,
				[]authz.Actor{{ID: "u-late"}}), engine.StepOptions{})
		require.NoError(t, err)
		assert.Empty(t, res.Commands, "a terminal instance must emit no commands")
		assert.Equal(t, cancelled.State.Status, res.State.Status)
	})

	t.Run("the caller's slice is not aliased into instance state", func(t *testing.T) {
		def := userTaskDef()
		state := startUserTask(t, def, at).State
		taskID := state.Tasks[0].TaskID

		actors := []authz.Actor{{ID: "u-jane", Roles: []string{"manager"}}}
		res, err := engine.Step(t.Context(), def, state,
			engine.NewHumanCandidatesResolved(at.Add(time.Minute), taskID, actors), engine.StepOptions{})
		require.NoError(t, err)

		// Mutate what the caller still holds.
		actors[0].ID = "mutated"
		actors[0].Roles[0] = "mutated"

		require.Len(t, res.State.Tasks[0].Candidates, 1)
		assert.Equal(t, "u-jane", res.State.Tasks[0].Candidates[0].ID)
		assert.Equal(t, "manager", res.State.Tasks[0].Candidates[0].Roles[0])
	})
}

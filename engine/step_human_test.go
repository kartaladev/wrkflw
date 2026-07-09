package engine_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
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
			activity.NewUserTask("approve", activity.WithCandidateRoles("manager"), activity.WithEligibilityExpr(`actor.ID != ""`)),
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
	r, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, map[string]any{"region": "APAC"}), engine.StepOptions{})
	require.NoError(t, err)
	return r
}

// TestUserTaskEmitsAwaitHumanAndParks verifies that driving into a KindUserTask
// node emits AwaitHuman (with the correct eligibility spec), creates an Unclaimed
// HumanTask in state, and parks the token (TokenWaitingCommand with the TaskToken
// in AwaitCommand). The instance must still be running.
func TestUserTaskEmitsAwaitHumanAndParks(t *testing.T) {
	at := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)
	def := userTaskDef()

	res := startUserTask(t, def, at)

	// Exactly one command: AwaitHuman.
	require.Len(t, res.Commands, 1)
	ah, ok := res.Commands[0].(engine.AwaitHuman)
	require.True(t, ok, "expected AwaitHuman command, got %T", res.Commands[0])

	// TaskToken must be deterministic: <instanceID>-h<seq> where seq starts at 1.
	assert.Equal(t, "i1-h1", ah.TaskToken)

	// Eligibility is built from the node definition.
	assert.Equal(t, []string{"manager"}, ah.Eligibility.Roles)
	assert.Equal(t, `actor.ID != ""`, ah.Eligibility.Attribute)

	// Token parked on the user task node.
	require.Len(t, res.State.Tokens, 1)
	tok := res.State.Tokens[0]
	assert.Equal(t, "approve", tok.NodeID)
	assert.Equal(t, engine.TokenWaitingCommand, tok.State)
	assert.Equal(t, "i1-h1", tok.AwaitCommand)

	// Instance still running.
	assert.Equal(t, engine.StatusRunning, res.State.Status)

	// HumanTask record created in state.
	require.Len(t, res.State.Tasks, 1)
	ht := res.State.Tasks[0]
	assert.Equal(t, "i1-h1", ht.TaskToken)
	assert.Equal(t, "i1", ht.InstanceID)
	assert.Equal(t, "approve", ht.NodeID)
	assert.Equal(t, humantask.Unclaimed, ht.State)
	assert.Equal(t, []string{"manager"}, ht.Eligibility.Roles)
	assert.Equal(t, `actor.ID != ""`, ht.Eligibility.Attribute)
	assert.Equal(t, at, ht.CreatedAt)
}

// TestUserTaskPrivilegesFlowToAwaitHuman verifies that EligibilityPrivileges set
// via activity.WithEligibilityPrivileges flow through to the AwaitHuman command's
// Eligibility.Privileges field and the HumanTask stored in engine state.
func TestUserTaskPrivilegesFlowToAwaitHuman(t *testing.T) {
	at := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)
	privs := []string{"finance-task claim"}
	def := &model.ProcessDefinition{
		ID: "p-priv", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("approve",
				activity.WithEligibilityPrivileges(privs...),
			),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "approve"},
			{ID: "f2", Source: "approve", Target: "end"},
		},
	}

	res, err := engine.Step(def, engine.InstanceState{InstanceID: "i-priv"},
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
// on the same instance gets TaskToken "i1-h2" (TaskSeq increments).
func TestUserTaskTaskSeqIncrements(t *testing.T) {
	at := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)

	// Definition with two sequential user tasks.
	def := &model.ProcessDefinition{
		ID: "p-ht2", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("task1", activity.WithCandidateRoles("a")),
			activity.NewUserTask("task2", activity.WithCandidateRoles("b")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "task1"},
			{ID: "f2", Source: "task1", Target: "task2"},
			{ID: "f3", Source: "task2", Target: "end"},
		},
	}

	// Start → parked at task1.
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r1.Commands, 1)
	ah1 := r1.Commands[0].(engine.AwaitHuman)
	assert.Equal(t, "i1-h1", ah1.TaskToken)

	// Complete task1 → drives to task2.
	actor := authz.Actor{ID: "alice", Roles: []string{"a"}}
	r2, err := engine.Step(def, r1.State,
		engine.NewHumanCompleted(at.Add(time.Minute), "i1-h1", map[string]any{"ok": true}, actor),
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
	assert.Equal(t, "i1-h2", ah2.TaskToken)
}

// TestHumanClaimedUpdatesTask verifies that a HumanClaimed trigger sets
// ClaimedBy and State=Claimed on the task, emits UpdateTask, and does NOT move
// the token.
func TestHumanClaimedUpdatesTask(t *testing.T) {
	at := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)
	def := userTaskDef()
	actor := authz.Actor{ID: "bob", Roles: []string{"manager"}}

	r1 := startUserTask(t, def, at)
	taskToken := "i1-h1"
	tokenBefore := r1.State.Tokens[0]

	r2, err := engine.Step(def, r1.State,
		engine.NewHumanClaimed(at.Add(time.Minute), taskToken, actor),
		engine.StepOptions{})
	require.NoError(t, err)

	// Exactly one command: UpdateTask.
	require.Len(t, r2.Commands, 1)
	ut, ok := r2.Commands[0].(engine.UpdateTask)
	require.True(t, ok, "expected UpdateTask, got %T", r2.Commands[0])
	assert.Equal(t, taskToken, ut.Task.TaskToken)
	assert.Equal(t, "bob", ut.Task.ClaimedBy)
	assert.Equal(t, humantask.Claimed, ut.Task.State)

	// Task in state also updated.
	require.Len(t, r2.State.Tasks, 1)
	ht := r2.State.Tasks[0]
	assert.Equal(t, "bob", ht.ClaimedBy)
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

// TestHumanClaimedUnknownTaskTokenErrors verifies that claiming a non-existent
// task token returns ErrTokenNotFound.
func TestHumanClaimedUnknownTaskTokenErrors(t *testing.T) {
	at := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)
	def := userTaskDef()
	actor := authz.Actor{ID: "bob", Roles: []string{"manager"}}

	r1 := startUserTask(t, def, at)

	_, err := engine.Step(def, r1.State,
		engine.NewHumanClaimed(at.Add(time.Minute), "no-such-token", actor),
		engine.StepOptions{})
	require.ErrorIs(t, err, engine.ErrTokenNotFound)
}

// TestHumanReassignedUpdatesTask verifies that HumanReassigned changes ClaimedBy
// to To, keeps State=Claimed, and emits UpdateTask. No token movement.
func TestHumanReassignedUpdatesTask(t *testing.T) {
	at := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)
	def := userTaskDef()

	r1 := startUserTask(t, def, at)
	taskToken := "i1-h1"

	// First claim it.
	alice := authz.Actor{ID: "alice", Roles: []string{"manager"}}
	r2, err := engine.Step(def, r1.State,
		engine.NewHumanClaimed(at.Add(time.Minute), taskToken, alice),
		engine.StepOptions{})
	require.NoError(t, err)

	// Now reassign from alice → bob.
	by := authz.Actor{ID: "admin"}
	r3, err := engine.Step(def, r2.State,
		engine.NewHumanReassigned(at.Add(2*time.Minute), taskToken, "alice", "bob", by),
		engine.StepOptions{})
	require.NoError(t, err)

	// Exactly one command: UpdateTask.
	require.Len(t, r3.Commands, 1)
	ut, ok := r3.Commands[0].(engine.UpdateTask)
	require.True(t, ok, "expected UpdateTask, got %T", r3.Commands[0])
	assert.Equal(t, "bob", ut.Task.ClaimedBy)
	assert.Equal(t, humantask.Claimed, ut.Task.State)

	// State reflects reassignment.
	require.Len(t, r3.State.Tasks, 1)
	assert.Equal(t, "bob", r3.State.Tasks[0].ClaimedBy)
	assert.Equal(t, humantask.Claimed, r3.State.Tasks[0].State)

	// Token still parked.
	require.Len(t, r3.State.Tokens, 1)
	assert.Equal(t, engine.TokenWaitingCommand, r3.State.Tokens[0].State)
}

// TestHumanReassignedUnknownTaskTokenErrors verifies that reassigning a
// non-existent task token returns ErrTokenNotFound.
func TestHumanReassignedUnknownTaskTokenErrors(t *testing.T) {
	at := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)
	def := userTaskDef()
	by := authz.Actor{ID: "admin"}

	r1 := startUserTask(t, def, at)

	_, err := engine.Step(def, r1.State,
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
	taskToken := "i1-h1"
	doneAt := at.Add(5 * time.Minute)

	r2, err := engine.Step(def, r1.State,
		engine.NewHumanCompleted(doneAt, taskToken, map[string]any{"approved": true}, actor),
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
	assert.Equal(t, taskToken, utCmd.Task.TaskToken)
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

	// NodeVisit for "approve" has ActorID set to the completing actor.
	// We look specifically for the CLOSED visit (LeftAt != nil) — that is the
	// user-task visit that was stamped with the actor on completion. Using the
	// closed visit is unambiguous even if the same node is visited more than once.
	var approveVisit *engine.NodeVisit
	for i := range r2.State.History {
		v := &r2.State.History[i]
		if v.NodeID == "approve" && v.LeftAt != nil {
			approveVisit = v
			break
		}
	}
	require.NotNil(t, approveVisit, "closed NodeVisit for 'approve' not found")
	require.NotNil(t, approveVisit.ActorID, "NodeVisit.ActorID is nil")
	assert.Equal(t, "carol", *approveVisit.ActorID)
}

// TestHumanCompletedUnknownTaskTokenErrors verifies that completing a
// non-existent task token returns ErrTokenNotFound.
func TestHumanCompletedUnknownTaskTokenErrors(t *testing.T) {
	at := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)
	def := userTaskDef()
	actor := authz.Actor{ID: "carol", Roles: []string{"manager"}}

	r1 := startUserTask(t, def, at)

	_, err := engine.Step(def, r1.State,
		engine.NewHumanCompleted(at.Add(time.Minute), "no-such-token",
			map[string]any{"approved": true}, actor),
		engine.StepOptions{})
	require.ErrorIs(t, err, engine.ErrTokenNotFound)
}

// TestHumanCompletedMissingTaskRecordErrors verifies that HumanCompleted fails fast
// when a parked token is found for the given TaskToken but the corresponding
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

	// Build corrupted state: token is parked awaiting the task token, but no
	// HumanTask record exists in Tasks.
	corruptedState := engine.InstanceState{
		InstanceID: "i1",
		Status:     engine.StatusRunning,
		Tokens: []engine.Token{
			{
				ID:           "i1-t1",
				NodeID:       "approve",
				State:        engine.TokenWaitingCommand,
				AwaitCommand: "i1-h1",
			},
		},
		// Tasks deliberately empty — no matching HumanTask record.
		History: []engine.NodeVisit{
			{NodeID: "approve", TokenID: "i1-t1", EnteredAt: at},
		},
	}

	_, err := engine.Step(def, corruptedState,
		engine.NewHumanCompleted(at.Add(time.Minute), "i1-h1",
			map[string]any{"approved": true}, actor),
		engine.StepOptions{})

	// Must error, wrapping humantask.ErrTaskNotFound.
	require.Error(t, err)
	require.ErrorIs(t, err, humantask.ErrTaskNotFound,
		"expected error wrapping humantask.ErrTaskNotFound, got: %v", err)
}

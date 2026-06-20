package engine_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
)

func TestCommandsImplementInterface(t *testing.T) {
	cmds := []engine.Command{
		engine.InvokeAction{CommandID: "c1", Name: "greet", Input: map[string]any{"a": 1}},
		engine.CompleteInstance{Result: map[string]any{"done": true}},
		engine.FailInstance{Err: "boom"},
	}
	assert.Len(t, cmds, 3)

	ia, ok := cmds[0].(engine.InvokeAction)
	assert.True(t, ok)
	assert.Equal(t, "greet", ia.Name)
}

// TestHumanCommandsImplementInterface asserts AwaitHuman and UpdateTask satisfy Command.
func TestHumanCommandsImplementInterface(t *testing.T) {
	spec := authz.AuthzSpec{Roles: []string{"approver"}, Attribute: "actor.ID != \"\""}
	task := humantask.HumanTask{
		TaskToken:  "tok1",
		InstanceID: "i1",
		NodeID:     "approve",
		Eligibility: authz.AuthzSpec{Roles: []string{"manager"}},
		Candidates: []string{"u1", "u2"},
		State:      humantask.Unclaimed,
	}

	cmds := []engine.Command{
		engine.AwaitHuman{TaskToken: "tok1", Eligibility: spec},
		engine.UpdateTask{Task: task},
	}
	assert.Len(t, cmds, 2)

	ah, ok := cmds[0].(engine.AwaitHuman)
	require.True(t, ok)
	assert.Equal(t, "tok1", ah.TaskToken)
	assert.Equal(t, spec, ah.Eligibility)

	ut, ok := cmds[1].(engine.UpdateTask)
	require.True(t, ok)
	assert.Equal(t, task, ut.Task)
}

// TestAwaitHumanEligibilityRoundTrip asserts AuthzSpec is faithfully stored.
func TestAwaitHumanEligibilityRoundTrip(t *testing.T) {
	spec := authz.AuthzSpec{
		Roles:      []string{"manager", "admin"},
		Privileges: []string{"approve"},
		Attribute:  "actor.ID != \"\"",
	}
	cmd := engine.AwaitHuman{TaskToken: "tok42", Eligibility: spec}
	assert.Equal(t, spec.Roles, cmd.Eligibility.Roles)
	assert.Equal(t, spec.Privileges, cmd.Eligibility.Privileges)
	assert.Equal(t, spec.Attribute, cmd.Eligibility.Attribute)
}

// TestUpdateTaskRoundTrip asserts HumanTask is faithfully stored.
func TestUpdateTaskRoundTrip(t *testing.T) {
	task := humantask.HumanTask{
		TaskToken:   "tok7",
		InstanceID:  "inst1",
		NodeID:      "review",
		Eligibility: authz.AuthzSpec{Roles: []string{"reviewer"}},
		Candidates:  []string{"alice", "bob"},
		State:       humantask.Claimed,
		ClaimedBy:   "alice",
	}
	cmd := engine.UpdateTask{Task: task}
	assert.Equal(t, task, cmd.Task)
}

// Compile-time interface assertions: each human command must satisfy engine.Command.
var (
	_ engine.Command = engine.AwaitHuman{}
	_ engine.Command = engine.UpdateTask{}
)

// TestInstanceStateTasksDeepCopied asserts that cloneState (via Step) deep-copies
// Tasks so that mutating the returned state's Tasks does not affect the input.
func TestInstanceStateTasksDeepCopied(t *testing.T) {
	task := humantask.HumanTask{
		TaskToken:   "tok1",
		InstanceID:  "i1",
		NodeID:      "approve",
		Eligibility: authz.AuthzSpec{Roles: []string{"approver"}},
		Candidates:  []string{"u1", "u2"},
		State:       humantask.Unclaimed,
	}
	in := engine.InstanceState{
		InstanceID: "i1",
		Tasks:      []humantask.HumanTask{task},
	}

	// task() lookup on the original state: must find the task by token.
	found := in.TaskByToken("tok1")
	require.NotNil(t, found)
	assert.Equal(t, task, *found)

	// nil for unknown token.
	assert.Nil(t, in.TaskByToken("no-such-token"))

	// Clone the state; mutate the clone's Tasks and Candidates — original must be unchanged.
	cloned := in.Clone()
	cloned.Tasks[0].State = humantask.Claimed
	cloned.Tasks[0].Candidates = append(cloned.Tasks[0].Candidates, "u3")
	cloned.Tasks[0].Eligibility.Roles = append(cloned.Tasks[0].Eligibility.Roles, "extra-role")

	assert.Equal(t, humantask.Unclaimed, in.Tasks[0].State)
	assert.Equal(t, []string{"u1", "u2"}, in.Tasks[0].Candidates)
	assert.Equal(t, []string{"approver"}, in.Tasks[0].Eligibility.Roles)
}

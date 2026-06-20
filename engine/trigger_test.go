package engine_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/engine"
)

func TestTriggersCarryOccurredAt(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)

	trs := []engine.Trigger{
		engine.NewStartInstance(at, map[string]any{"x": 1}),
		engine.NewActionCompleted(at, "c1", map[string]any{"ok": true}),
		engine.NewActionFailed(at, "c1", "boom", true),
	}
	for _, tr := range trs {
		assert.Equal(t, at, tr.OccurredAt())
	}

	ac := engine.NewActionCompleted(at, "c1", map[string]any{"ok": true})
	assert.Equal(t, "c1", ac.CommandID)
	assert.Equal(t, true, ac.Output["ok"])
}

// TestHumanTriggersCarryOccurredAt asserts the three human-task triggers satisfy
// the Trigger interface and that constructors stamp OccurredAt correctly.
func TestHumanTriggersCarryOccurredAt(t *testing.T) {
	at := time.Date(2026, 6, 20, 11, 0, 0, 0, time.UTC)
	actor := authz.Actor{ID: "u1", Roles: []string{"approver"}}

	trs := []engine.Trigger{
		engine.NewHumanCompleted(at, "tok1", map[string]any{"approved": true}, actor),
		engine.NewHumanClaimed(at, "tok1", actor),
		engine.NewHumanReassigned(at, "tok1", "u0", "u1", actor),
	}
	for _, tr := range trs {
		assert.Equal(t, at, tr.OccurredAt())
	}
}

// TestHumanCompletedFields asserts that NewHumanCompleted stores all fields.
func TestHumanCompletedFields(t *testing.T) {
	at := time.Date(2026, 6, 20, 11, 0, 0, 0, time.UTC)
	actor := authz.Actor{ID: "u1", Roles: []string{"approver"}}
	output := map[string]any{"decision": "yes"}

	hc := engine.NewHumanCompleted(at, "tok1", output, actor)
	assert.Equal(t, "tok1", hc.TaskToken)
	assert.Equal(t, output, hc.Output)
	assert.Equal(t, actor, hc.Actor)
}

// TestHumanClaimedFields asserts that NewHumanClaimed stores all fields.
func TestHumanClaimedFields(t *testing.T) {
	at := time.Date(2026, 6, 20, 11, 0, 0, 0, time.UTC)
	actor := authz.Actor{ID: "u2", Roles: []string{"reviewer"}}

	hcl := engine.NewHumanClaimed(at, "tok2", actor)
	assert.Equal(t, "tok2", hcl.TaskToken)
	assert.Equal(t, actor, hcl.Actor)
}

// TestHumanReassignedFields asserts that NewHumanReassigned stores all fields.
func TestHumanReassignedFields(t *testing.T) {
	at := time.Date(2026, 6, 20, 11, 0, 0, 0, time.UTC)
	by := authz.Actor{ID: "admin", Roles: []string{"admin"}}

	hr := engine.NewHumanReassigned(at, "tok3", "u0", "u1", by)
	assert.Equal(t, "tok3", hr.TaskToken)
	assert.Equal(t, "u0", hr.From)
	assert.Equal(t, "u1", hr.To)
	assert.Equal(t, by, hr.By)
}

// Compile-time interface assertions: each human trigger must satisfy engine.Trigger.
var (
	_ engine.Trigger = engine.HumanCompleted{}
	_ engine.Trigger = engine.HumanClaimed{}
	_ engine.Trigger = engine.HumanReassigned{}
)

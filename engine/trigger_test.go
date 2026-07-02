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

// Compile-time interface assertion: TimerFired must satisfy engine.Trigger.
var _ engine.Trigger = engine.TimerFired{}

// Compile-time interface assertions: sub-instance triggers must satisfy engine.Trigger.
var (
	_ engine.Trigger = engine.SubInstanceCompleted{}
	_ engine.Trigger = engine.SubInstanceFailed{}
)

// Compile-time interface assertion: CompensateRequested must satisfy engine.Trigger.
var _ engine.Trigger = engine.CompensateRequested{}

// Compile-time interface assertion: CancelRequested must satisfy engine.Trigger.
var _ engine.Trigger = engine.CancelRequested{}

// TestCompensateRequestedFields asserts that NewCompensateRequested stamps OccurredAt
// and stores ToNode faithfully.
func TestCompensateRequestedFields(t *testing.T) {
	at := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)

	cases := []struct {
		name   string
		toNode string
	}{
		{"partial rollback", "step1"},
		{"full rollback", ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cr := engine.NewCompensateRequested(at, tc.toNode)
			assert.Equal(t, at, cr.OccurredAt(), "OccurredAt must match the given time")
			assert.Equal(t, tc.toNode, cr.ToNode, "ToNode must be stored faithfully")
		})
	}
}

// TestCancelRequestedFields asserts that NewCancelRequested stamps OccurredAt
// faithfully and satisfies the Trigger interface.
func TestCancelRequestedFields(t *testing.T) {
	at := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	cr := engine.NewCancelRequested(at)
	assert.Equal(t, at, cr.OccurredAt(), "OccurredAt must match the given time")
}

// TestTimerFiredSatisfiesTrigger asserts TimerFired satisfies Trigger and
// NewTimerFired stamps OccurredAt correctly.
func TestTimerFiredSatisfiesTrigger(t *testing.T) {
	at := time.Date(2026, 6, 21, 9, 30, 0, 0, time.UTC)
	tf := engine.NewTimerFired(at, "tmr-99")

	var _ engine.Trigger = tf // runtime check

	assert.Equal(t, at, tf.OccurredAt())
	assert.Equal(t, "tmr-99", tf.TimerID)
}

// TestTimerFiredFieldsRoundTrip asserts all TimerFired fields are stored faithfully.
func TestTimerFiredFieldsRoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		at      time.Time
		timerID string
	}{
		{"early", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), "tmr-a"},
		{"late", time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC), "tmr-b"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			tf := engine.NewTimerFired(tc.at, tc.timerID)
			assert.Equal(t, tc.at, tf.OccurredAt())
			assert.Equal(t, tc.timerID, tf.TimerID)
		})
	}
}

// TestNewActionFailedWithJitterCarriesFraction asserts that NewActionFailed with
// WithJitter stores JitterFraction and all other ActionFailed fields faithfully,
// and that the result satisfies the Trigger interface.
func TestNewActionFailedWithJitterCarriesFraction(t *testing.T) {
	at := time.Unix(0, 0)
	f := engine.NewActionFailed(at, "c-1", "boom", true, engine.WithJitter(0.5))
	if f.JitterFraction != 0.5 || !f.Retryable || f.CommandID != "c-1" {
		t.Fatalf("bad ActionFailed: %+v", f)
	}
	var _ engine.Trigger = f
}

// TestNewActionFailedJitterOption asserts that NewActionFailed supports a
// variadic WithJitter option and that omitting the option leaves JitterFraction at 0.
func TestNewActionFailedJitterOption(t *testing.T) {
	at := time.Unix(0, 0)
	base := engine.NewActionFailed(at, "cmd", "boom", true)
	if base.JitterFraction != 0 {
		t.Fatalf("default jitter = %v, want 0", base.JitterFraction)
	}
	jit := engine.NewActionFailed(at, "cmd", "boom", true, engine.WithJitter(0.5))
	if jit.JitterFraction != 0.5 {
		t.Fatalf("jitter = %v, want 0.5", jit.JitterFraction)
	}
	if !jit.Retryable || jit.CommandID != "cmd" || jit.Err != "boom" {
		t.Fatalf("unexpected fields: %+v", jit)
	}
}

// TestResolveIncidentIsTrigger asserts that ResolveIncident satisfies the Trigger
// interface and that NewResolveIncident stores all fields faithfully.
func TestResolveIncidentIsTrigger(t *testing.T) {
	at := time.Unix(0, 0)
	r := engine.NewResolveIncident(at, "p-in0", 2)
	if r.IncidentID != "p-in0" || r.AddAttempts != 2 {
		t.Fatalf("bad ResolveIncident: %+v", r)
	}
	var _ engine.Trigger = r
	if !r.OccurredAt().Equal(at) {
		t.Fatal("OccurredAt mismatch")
	}
}

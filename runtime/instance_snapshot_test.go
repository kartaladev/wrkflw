package runtime_test

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// TestStatusString verifies that StatusString maps every engine.Status value to
// its canonical string representation.
func TestStatusString(t *testing.T) {
	cases := map[engine.Status]string{
		engine.StatusRunning:      "running",
		engine.StatusCompleted:    "completed",
		engine.StatusFailed:       "failed",
		engine.StatusCompensating: "compensating",
		engine.StatusTerminated:   "terminated",
	}
	for in, want := range cases {
		if got := runtime.StatusString(in); got != want {
			t.Errorf("StatusString(%v) = %q, want %q", in, got, want)
		}
	}
}

// TestStatusString_Unknown verifies that an out-of-range Status maps to "unknown".
func TestStatusString_Unknown(t *testing.T) {
	if got := runtime.StatusString(engine.Status(99)); got != "unknown" {
		t.Errorf("StatusString(99) = %q, want %q", got, "unknown")
	}
}

// TestNewInstanceSnapshot verifies that NewInstanceSnapshot maps an
// engine.InstanceState to an InstanceSnapshot DTO correctly, and that the
// serialized JSON contains no engine bookkeeping keys.
func TestNewInstanceSnapshot(t *testing.T) {
	end := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	st := engine.InstanceState{
		InstanceID: "i1",
		DefID:      "d1",
		DefVersion: 2,
		Status:     engine.StatusCompleted,
		Variables:  map[string]any{"amount": 10},
		Tokens:     []engine.Token{{ID: "t1", NodeID: "n1", State: engine.TokenActive}},
		History:    []engine.NodeVisit{{NodeID: "n1", TokenID: "t1"}},
		EndedAt:    &end,
		Tasks: []humantask.HumanTask{{
			TaskToken: "tk1",
			NodeID:    "task-node",
			State:     humantask.Unclaimed,
		}},
		Incidents: []engine.Incident{{
			ID:        "inc1",
			TokenID:   "t1",
			NodeID:    "n1",
			Error:     "something went wrong",
			CreatedAt: time.Date(2026, 6, 23, 11, 0, 0, 0, time.UTC),
		}},
	}
	snap := runtime.NewInstanceSnapshot(st, nil)

	if snap.InstanceID != "i1" || snap.Status != "completed" || snap.DefVersion != 2 {
		t.Fatalf("snap = %+v", snap)
	}
	if snap.DefID != "d1" {
		t.Fatalf("snap.DefID = %q, want %q", snap.DefID, "d1")
	}
	if snap.EndedAt == nil || !snap.EndedAt.Equal(end) {
		t.Fatalf("snap.EndedAt = %v, want %v", snap.EndedAt, end)
	}
	if len(snap.Variables) != 1 {
		t.Fatalf("snap.Variables = %v", snap.Variables)
	}

	if len(snap.Tokens) != 1 || snap.Tokens[0].State != "active" {
		t.Fatalf("tokens = %+v", snap.Tokens)
	}
	if snap.Tokens[0].NodeID != "n1" || snap.Tokens[0].ID != "t1" {
		t.Fatalf("token fields = %+v", snap.Tokens[0])
	}

	if len(snap.History) != 1 || snap.History[0].NodeID != "n1" {
		t.Fatalf("history = %+v", snap.History)
	}

	if len(snap.Tasks) != 1 || snap.Tasks[0].State != "unclaimed" {
		t.Fatalf("tasks = %+v", snap.Tasks)
	}
	if snap.Tasks[0].TaskToken != "tk1" || snap.Tasks[0].NodeID != "task-node" {
		t.Fatalf("task fields = %+v", snap.Tasks[0])
	}

	if len(snap.Incidents) != 1 || snap.Incidents[0].ID != "inc1" {
		t.Fatalf("incidents = %+v", snap.Incidents)
	}

	// no-leak guard: serialized JSON must not contain engine bookkeeping keys.
	b, _ := json.Marshal(snap)
	for _, banned := range []string{"armed", "boundaries", "scopes", "compensat", "Seq", "pendingCancel"} {
		if bytes.Contains(bytes.ToLower(b), bytes.ToLower([]byte(banned))) {
			t.Errorf("snapshot JSON leaks bookkeeping key %q: %s", banned, b)
		}
	}
}

package httpcore_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/transport/http/httpcore"
)

func TestStatusString_httpcore(t *testing.T) {
	tests := []struct {
		status engine.Status
		want   string
	}{
		{engine.StatusRunning, "running"},
		{engine.StatusCompleted, "completed"},
		{engine.StatusFailed, "failed"},
		{engine.StatusCompensating, "compensating"},
		{engine.StatusTerminated, "terminated"},
		{engine.Status(99), "unknown"},
	}
	for _, tc := range tests {
		st := engine.InstanceState{InstanceID: "x", Status: tc.status}
		v := httpcore.NewInstanceView(st)
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		want := `"status":"` + tc.want + `"`
		if !strings.Contains(string(b), want) {
			t.Errorf("status %v: want %q in %s", tc.status, want, b)
		}
	}
}

func TestNewInstanceView_httpcore(t *testing.T) {
	st := engine.InstanceState{
		InstanceID: "inst-1", DefID: "d", DefVersion: 2,
		Status: engine.StatusRunning, Variables: map[string]any{"n": "ada"},
		StartedAt: time.Now(),
	}
	v := httpcore.NewInstanceView(st)
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if !strings.Contains(got, `"status":"running"`) {
		t.Fatalf("want status=running in: %s", got)
	}
	if !strings.Contains(got, `"instance_id":"inst-1"`) {
		t.Fatalf("want instance_id=inst-1 in: %s", got)
	}
	if !strings.Contains(got, `"def_id":"d"`) {
		t.Fatalf("want def_id=d in: %s", got)
	}
	if !strings.Contains(got, `"def_version":2`) {
		t.Fatalf("want def_version=2 in: %s", got)
	}
}

func TestNewInstanceView_returnType(t *testing.T) {
	st := engine.InstanceState{InstanceID: "typed-1", Status: engine.StatusCompleted}
	v := httpcore.NewInstanceView(st)
	// The real implementation returns httpcore.InstanceView, not engine.InstanceState.
	// This assertion confirms the concrete type is httpcore.InstanceView.
	if _, ok := v.(httpcore.InstanceView); !ok {
		t.Fatalf("want httpcore.InstanceView, got %T", v)
	}
}

package rest_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zakyalvan/krtlwrkflw/engine"
	rest "github.com/zakyalvan/krtlwrkflw/transport/rest"
)

func TestStatusString(t *testing.T) {
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
		b, err := json.Marshal(rest.NewInstanceView(st))
		if err != nil {
			t.Fatal(err)
		}
		want := `"status":"` + tc.want + `"`
		if !strings.Contains(string(b), want) {
			t.Errorf("status %v: want %q in %s", tc.status, want, b)
		}
	}
}

func TestNewInstanceView(t *testing.T) {
	st := engine.InstanceState{
		InstanceID: "inst-1", DefID: "d", DefVersion: 2,
		Status: engine.StatusRunning, Variables: map[string]any{"n": "ada"},
		StartedAt: time.Now(),
	}
	b, err := json.Marshal(rest.NewInstanceView(st))
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

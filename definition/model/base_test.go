package model

import (
	"testing"
	"time"

	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
)

// TestBaseNewAndSetName covers the exported Base embed used by every leaf
// node package to carry node identity.
func TestBaseNewAndSetName(t *testing.T) {
	b := NewBase("n1", "First")
	if b.ID() != "n1" {
		t.Fatalf("ID() = %q, want n1", b.ID())
	}
	if b.Name() != "First" {
		t.Fatalf("Name() = %q, want First", b.Name())
	}
	b.SetName("Second")
	if b.Name() != "Second" {
		t.Fatalf("after SetName: Name() = %q, want Second", b.Name())
	}
}

// TestActivityFieldsCarriers covers the unexported carrier methods that the
// kind-agnostic accessors (DeadlineOf/WaitActionOf/RetryPolicyOf/recoveryFlowOf)
// dispatch on after the node kinds move into leaf packages.
func TestActivityFieldsCarriers(t *testing.T) {
	a := ActivityFields{
		WaitFields: WaitFields{
			DeadlineTimer: schedule.AfterDuration(2 * time.Hour), DeadlineFlow: "f", DeadlineAction: "act",
			WaitEvery: schedule.Every(time.Hour), WaitAction: "r",
		},
		RetryPolicy:  &RetryPolicy{MaxAttempts: 5},
		RecoveryFlow: "rec",
	}
	if dt, f, act := a.deadline(); f != "f" || act != "act" {
		t.Fatalf("deadline() flow/action = %q,%q", f, act)
	} else if d, ok := dt.Duration(); !ok || d != 2*time.Hour {
		t.Fatalf("deadline() duration = %v", d)
	}
	if re, r := a.waitAction(); r != "r" {
		t.Fatalf("waitAction() action = %q", r)
	} else if d, ok := re.Duration(); !ok || d != time.Hour {
		t.Fatalf("waitAction() duration = %v", d)
	}
	if a.retry().MaxAttempts != 5 {
		t.Fatalf("retry() = %+v", a.retry())
	}
	if a.recoveryFlow() != "rec" {
		t.Fatalf("recoveryFlow() = %q", a.recoveryFlow())
	}
}

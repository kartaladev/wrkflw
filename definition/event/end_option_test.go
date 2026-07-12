package event_test

import (
	"testing"

	"github.com/zakyalvan/krtlwrkflw/definition/event"
)

func TestTerminationOutcomeString(t *testing.T) {
	t.Parallel()
	cases := []struct {
		outcome event.TerminationOutcome
		assert  func(t *testing.T, got string)
	}{
		{event.OutcomeComplete, func(t *testing.T, got string) {
			if got != "complete" {
				t.Fatalf("OutcomeComplete.String() = %q, want %q", got, "complete")
			}
		}},
		{event.OutcomeAbort, func(t *testing.T, got string) {
			if got != "abort" {
				t.Fatalf("OutcomeAbort.String() = %q, want %q", got, "abort")
			}
		}},
	}
	for _, c := range cases {
		c.assert(t, c.outcome.String())
	}
}

func TestNewEndWithForceTermination(t *testing.T) {
	t.Parallel()
	n := event.NewEnd("halt", event.WithName("Halt"), event.WithForceTermination("fraud detected", event.OutcomeAbort))
	ev, ok := n.(event.EndEvent)
	if !ok {
		t.Fatalf("NewEnd returned %T, want event.EndEvent", n)
	}
	if ev.Behavior != event.EndTerminate {
		t.Fatalf("Behavior = %v, want EndTerminate", ev.Behavior)
	}
	if ev.TerminationReason != "fraud detected" {
		t.Fatalf("TerminationReason = %q, want %q", ev.TerminationReason, "fraud detected")
	}
	if ev.Outcome != event.OutcomeAbort {
		t.Fatalf("Outcome = %v, want OutcomeAbort", ev.Outcome)
	}
	if ev.Name() != "Halt" {
		t.Fatalf("Name() = %q, want %q", ev.Name(), "Halt")
	}
}

func TestNewEndPlain(t *testing.T) {
	t.Parallel()
	ev := event.NewEnd("done").(event.EndEvent)
	if ev.Behavior != event.EndNormal {
		t.Fatalf("plain NewEnd Behavior = %v, want EndNormal", ev.Behavior)
	}
	if ev.Outcome != event.OutcomeComplete {
		t.Fatalf("plain NewEnd Outcome = %v, want OutcomeComplete (zero value)", ev.Outcome)
	}
}

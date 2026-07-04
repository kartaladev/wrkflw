package event_test

import (
	"encoding/json"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
)

func TestStartEventOptions(t *testing.T) {
	n := event.NewStart("s",
		event.WithName("Start"),
		event.WithStartSignal("go"),
		event.WithStartMessage("kick", "k"),
		event.WithStartTimer("1h"),
	).(event.StartEvent)
	if n.Kind() != definition.KindStartEvent || n.Name() != "Start" {
		t.Fatalf("kind/name = %v/%q", n.Kind(), n.Name())
	}
	if n.SignalName != "go" || n.MessageName != "kick" || n.CorrelationKey != "k" || n.TimerDuration != "1h" {
		t.Fatalf("fields = %+v", n)
	}
}

func TestCatchRenamedOptions(t *testing.T) {
	n := event.NewCatch("wait",
		event.WithName("Wait"),
		event.WithCatchTimer("15m"),
		event.WithCatchSignal("resume"),
		event.WithCatchMessage("go", "k"),
		event.WithCatchDeadline("4h", "esc", "escalate"),
		event.WithCatchReminder("1h", "nudge"),
	)
	if n.Kind() != definition.KindIntermediateCatchEvent {
		t.Fatalf("kind = %v", n.Kind())
	}
	if d, f, a := definition.DeadlineOf(n); d != "4h" || f != "esc" || a != "escalate" {
		t.Errorf("DeadlineOf = %q,%q,%q", d, f, a)
	}
	if e, a := definition.ReminderOf(n); e != "1h" || a != "nudge" {
		t.Errorf("ReminderOf = %q,%q", e, a)
	}
}

func TestThrowAndBoundaryAndEspOptions(t *testing.T) {
	th := event.NewThrow("t", event.WithThrowName("Emit"), event.WithThrowSignal("done"), event.WithCompensateRef("charge")).(event.IntermediateThrowEvent)
	if th.Name() != "Emit" || th.SignalName != "done" || th.CompensateRef != "charge" {
		t.Errorf("throw = %+v", th)
	}
	b := event.NewBoundary("b", "host",
		event.WithName("B"),
		event.WithBoundaryTimer("5m"),
		event.WithBoundarySignal("s"),
		event.WithBoundaryMessage("m", "k"),
		event.WithBoundaryErrorCode("E"),
		event.WithBoundaryNonInterrupting(),
	).(event.BoundaryEvent)
	if b.AttachedTo != "host" || !b.NonInterrupting || b.ErrorCode != "E" || b.TimerDuration != "5m" {
		t.Errorf("boundary = %+v", b)
	}
	sub := &definition.ProcessDefinition{ID: "s", Version: 1}
	esp := event.NewEventSubProcess("esp", sub, event.WithName("ESP"), event.WithEventSubProcessNonInterrupting()).(event.EventSubProcess)
	if !esp.NonInterrupting || esp.Subprocess != sub || esp.Name() != "ESP" {
		t.Errorf("esp = %+v", esp)
	}
}

func TestEndEventConstructors(t *testing.T) {
	cases := []struct {
		n definition.Node
		k definition.NodeKind
	}{
		{event.NewEnd("e", "End"), definition.KindEndEvent},
		{event.NewTerminateEnd("t"), definition.KindTerminateEndEvent},
		{event.NewErrorEnd("er", "E_BOOM", "Boom"), definition.KindErrorEndEvent},
	}
	for _, c := range cases {
		if c.n.Kind() != c.k {
			t.Errorf("Kind() = %v, want %v", c.n.Kind(), c.k)
		}
	}
	if ee := event.NewErrorEnd("er", "E_X").(event.ErrorEndEvent); ee.ErrorCode != "E_X" {
		t.Errorf("ErrorCode = %q", ee.ErrorCode)
	}
}

func TestEventRoundTrip(t *testing.T) {
	def := &definition.ProcessDefinition{
		ID: "e", Version: 1,
		Nodes: []definition.Node{
			event.NewStart("s", event.WithStartSignal("go")),
			event.NewCatch("c", event.WithCatchTimer("1h"), event.WithCatchDeadline("2h", "f", "a")),
			event.NewThrow("th", event.WithCompensateRef("s")),
			event.NewBoundary("b", "c", event.WithBoundaryErrorCode("E")),
			event.NewEnd("end"),
		},
	}
	data, err := json.Marshal(def)
	if err != nil {
		t.Fatal(err)
	}
	var got definition.ProcessDefinition
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Nodes[0].(event.StartEvent).SignalName != "go" {
		t.Error("start signal lost")
	}
	if d, _, _ := definition.DeadlineOf(got.Nodes[1]); d != "2h" {
		t.Errorf("catch deadline lost: %q", d)
	}
}

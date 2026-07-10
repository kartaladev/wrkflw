package event_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
)

func TestStartEventOptions(t *testing.T) {
	n := event.NewStart("s",
		event.WithName("Start"),
		event.WithSignalName("go"),
		event.WithMessageCorrelator("kick", "k"),
		event.WithStartTimer(schedule.AfterExpr(`"1h"`)),
	).(event.StartEvent)
	if n.Kind() != model.KindStartEvent || n.Name() != "Start" {
		t.Fatalf("kind/name = %v/%q", n.Kind(), n.Name())
	}
	if n.SignalName != "go" || n.MessageName != "kick" || n.CorrelationKey != "k" {
		t.Fatalf("fields = %+v", n)
	}
	if n.Timer.IsZero() {
		t.Fatalf("Timer not set: %+v", n)
	}
}

func TestCatchRenamedOptions(t *testing.T) {
	n := event.NewIntermediateCatch("wait",
		event.WithName("Wait"),
		event.WithCatchTimer(schedule.AfterExpr(`"15m"`)),
		event.WithSignalName("resume"),
		event.WithMessageCorrelator("go", "k"),
		event.WithWaitDeadline(schedule.AfterExpr(`"4h"`), "esc"), event.WithDeadlineAction("escalate"),
		event.WithWaitAction(schedule.EveryExpr(`"1h"`), "nudge"),
	)
	if n.Kind() != model.KindIntermediateCatchEvent {
		t.Fatalf("kind = %v", n.Kind())
	}
	d, f, a := model.DeadlineOf(n)
	if d.IsZero() || f != "esc" || a != "escalate" {
		t.Errorf("DeadlineOf = %v,%q,%q", d, f, a)
	}
	re, ra := model.WaitActionOf(n)
	if re.IsZero() || ra != "nudge" {
		t.Errorf("WaitActionOf = %v,%q", re, ra)
	}
	ce := n.(event.IntermediateCatchEvent)
	if ce.Timer.IsZero() {
		t.Errorf("catch Timer not set: %+v", ce)
	}
}

func TestThrowAndBoundaryAndEspOptions(t *testing.T) {
	th := event.NewIntermediateThrow("t", event.WithThrowName("Emit"), event.WithThrowSignalName("done"), event.WithCompensateRef("charge")).(event.IntermediateThrowEvent)
	if th.Name() != "Emit" || th.SignalName != "done" || th.CompensateRef != "charge" {
		t.Errorf("throw = %+v", th)
	}
	b := event.NewBoundary("b", "host",
		event.WithName("B"),
		event.WithBoundaryTimer(schedule.AfterExpr("5m")),
		event.WithSignalName("s"),
		event.WithMessageCorrelator("m", "k"),
		event.WithBoundaryErrorCode("E"),
		event.WithBoundaryNonInterrupting(),
	).(event.BoundaryEvent)
	if b.AttachedTo != "host" || !b.NonInterrupting || b.ErrorCode != "E" {
		t.Errorf("boundary base fields = %+v", b)
	}
	if b.Timer.IsZero() {
		t.Errorf("boundary Timer not set: %+v", b)
	}
	bExpr, _, bOk := b.Timer.Expr()
	if !bOk || bExpr != "5m" {
		t.Errorf("boundary Timer expr = %q, ok=%v", bExpr, bOk)
	}
	sub := &model.ProcessDefinition{ID: "s", Version: 1}
	esp := event.NewEventSubProcess("esp", sub, event.WithName("ESP"), event.WithEventSubProcessNonInterrupting()).(event.EventSubProcess)
	if !esp.NonInterrupting || esp.Subprocess != sub || esp.Name() != "ESP" {
		t.Errorf("esp = %+v", esp)
	}
}

func TestEndEventConstructors(t *testing.T) {
	cases := []struct {
		n model.Node
		k model.NodeKind
	}{
		{event.NewEnd("e", event.WithName("End")), model.KindEndEvent},
		{event.NewEnd("t", event.WithForceTermination("terminated", event.OutcomeAbort)), model.KindEndEvent},
		{event.NewErrorEnd("er", "E_BOOM", "Boom"), model.KindErrorEndEvent},
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
	def := &model.ProcessDefinition{
		ID: "e", Version: 1,
		Nodes: []model.Node{
			event.NewStart("s", event.WithSignalName("go"), event.WithStartTimer(schedule.AfterExpr(`"30m"`))),
			event.NewIntermediateCatch("c", event.WithCatchTimer(schedule.AfterExpr(`"1h"`)), event.WithWaitDeadline(schedule.AfterExpr(`"2h"`), "f"), event.WithDeadlineAction("a")),
			event.NewIntermediateThrow("th", event.WithCompensateRef("s")),
			event.NewBoundary("b", "c", event.WithBoundaryTimer(schedule.AfterDuration(5*time.Minute)), event.WithBoundaryErrorCode("E")),
			event.NewEnd("end"),
		},
	}
	data, err := json.Marshal(def)
	if err != nil {
		t.Fatal(err)
	}
	var got model.ProcessDefinition
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Nodes[0].(event.StartEvent).SignalName != "go" {
		t.Error("start signal lost")
	}
	startTimer := got.Nodes[0].(event.StartEvent).Timer
	if startTimer.IsZero() {
		t.Error("start Timer lost in round-trip")
	}
	if expr, _, ok := startTimer.Expr(); !ok || expr != `"30m"` {
		t.Errorf("start Timer expr lost: %q, ok=%v", expr, ok)
	}
	d, _, _ := model.DeadlineOf(got.Nodes[1])
	if d.IsZero() {
		t.Errorf("catch deadline lost after round-trip")
	}
	catchTimer := got.Nodes[1].(event.IntermediateCatchEvent).Timer
	if catchTimer.IsZero() {
		t.Error("catch Timer lost in round-trip")
	}
	if expr, _, ok := catchTimer.Expr(); !ok || expr != `"1h"` {
		t.Errorf("catch Timer expr lost: %q, ok=%v", expr, ok)
	}
	boundTimer := got.Nodes[3].(event.BoundaryEvent).Timer
	if boundTimer.IsZero() {
		t.Error("boundary Timer lost in round-trip")
	}
	dur, ok := boundTimer.Duration()
	if !ok || dur != 5*time.Minute {
		t.Errorf("boundary Timer duration lost: %v, ok=%v", dur, ok)
	}
}

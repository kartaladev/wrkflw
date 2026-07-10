package runtime

import (
	"sync"

	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
)

// eventStart holds the driver's event-based-start bookkeeping (ADR-0121): the
// active-correlation map used to make a message-start idempotent per
// (name, correlation key). It carries no reference to *ProcessDriver so its
// pure resolution helpers (messageStartNode, signalStartDefs,
// uniqueMessageStartDef, timerStartDefs) stay independently testable.
type eventStart struct {
	// mu guards active. Not yet locked anywhere in this file: the
	// record/evict methods that take it land in a later task (ADR-0121) once
	// a driver-side consumer (Drive-on-message-start) exists to call them.
	//nolint:unused // guards `active`; taken by the Task 5 record/evict methods.
	mu sync.Mutex
	// active maps a message-start (name, correlation key) to the instance ID
	// it already created, so a repeat delivery of the same correlated message
	// does not spawn a second instance.
	active map[msgKey]string
}

// newEventStart returns a ready-to-use *eventStart with an initialized active
// map (zero entries).
func newEventStart() *eventStart {
	return &eventStart{active: make(map[msgKey]string)}
}

// signalStartHit identifies a definition + node pair whose start event listens
// for a given signal name.
type signalStartHit struct {
	Def    *model.ProcessDefinition
	NodeID string
}

// timerStartHit identifies a definition + node pair whose start event carries
// a timer trigger, along with that trigger.
type timerStartHit struct {
	Def     *model.ProcessDefinition
	NodeID  string
	Trigger schedule.TriggerSpec
}

// messageStartNode returns the ID of def's start node whose message-start
// name equals name, and ok=true. It returns ok=false when def has no start
// node with a matching message name.
func messageStartNode(def *model.ProcessDefinition, name string) (nodeID string, ok bool) {
	if def == nil {
		return "", false
	}
	for _, n := range def.StartNodes() {
		se, isStart := n.(event.StartEvent)
		if !isStart {
			continue
		}
		if se.MessageName == name {
			return se.ID(), true
		}
	}
	return "", false
}

// signalStartDefs returns every definition+node pair, across defs, whose
// start event listens for the signal name. Order follows defs then each
// def's StartNodes order.
func signalStartDefs(defs []*model.ProcessDefinition, name string) []signalStartHit {
	var hits []signalStartHit
	for _, def := range defs {
		for _, n := range def.StartNodes() {
			se, isStart := n.(event.StartEvent)
			if !isStart {
				continue
			}
			if se.SignalName == name {
				hits = append(hits, signalStartHit{Def: def, NodeID: se.ID()})
			}
		}
	}
	return hits
}

// uniqueMessageStartDef finds the definition (and its start node) whose
// message-start name equals name, across defs. count is the number of
// matching def+node pairs found: 0 means no match, 1 means a unique match
// (def and nodeID are populated), and >=2 means the name is ambiguous across
// multiple definitions (def and nodeID are the zero value / empty in that
// case — callers must treat count>=2 as an error, not fall back to the first
// hit).
func uniqueMessageStartDef(defs []*model.ProcessDefinition, name string) (*model.ProcessDefinition, string, int) {
	var (
		match   *model.ProcessDefinition
		nodeID  string
		matches int
	)
	for _, def := range defs {
		id, ok := messageStartNode(def, name)
		if !ok {
			continue
		}
		matches++
		if matches == 1 {
			match, nodeID = def, id
		}
	}
	if matches != 1 {
		return nil, "", matches
	}
	return match, nodeID, matches
}

// timerStartDefs returns every definition+node pair, across defs, whose start
// event carries a timer trigger (Timer.IsZero() == false), along with that
// trigger.
func timerStartDefs(defs []*model.ProcessDefinition) []timerStartHit {
	var hits []timerStartHit
	for _, def := range defs {
		for _, n := range def.StartNodes() {
			se, isStart := n.(event.StartEvent)
			if !isStart {
				continue
			}
			if !se.Timer.IsZero() {
				hits = append(hits, timerStartHit{Def: def, NodeID: se.ID(), Trigger: se.Timer})
			}
		}
	}
	return hits
}

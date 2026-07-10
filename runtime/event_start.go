package runtime

import (
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
)

// This file holds the pure, driver-independent start-node resolution helpers
// used by the event-based-start subsystem (ADR-0121): messageStartNode,
// signalStartDefs, uniqueMessageStartDef, and timerStartDefs. Message-start
// dedup is handled by a deterministic instance id (see messageStartInstanceID)
// plus Store.Create's ErrInstanceExists, so no in-process correlation state is
// kept here.

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

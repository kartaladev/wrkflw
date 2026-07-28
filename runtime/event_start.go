package runtime

import (
	"iter"

	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/definition/schedule"
)

// This file holds the pure, driver-independent start-node resolution helpers
// used by the event-based-start subsystem (ADR-0121): messageStartNode,
// signalStartDefs, uniqueMessageStartDef, and timerStartDefs. Message-start
// dedup is handled by a deterministic instance id (see messageStartInstanceID)
// plus Store.Create's ErrInstanceExists, so no in-process correlation state is
// kept here.

// startEvents yields def's start nodes that are event.StartEvent, in StartNodes
// order. A nil def yields nothing.
//
// It exists so the narrowing preamble — range StartNodes, type-assert, skip
// non-start-events — is written once rather than repeated at every site that
// inspects a definition's start triggers (message, signal, and timer starts).
//
// It is an iterator rather than a slice because these sites are hot — every
// signal broadcast and every delivered message scans the registry — and because
// a caller looking for one match (messageStartNode) must be able to stop early.
// Ranging it allocates nothing and honours break/return.
func startEvents(def *model.ProcessDefinition) iter.Seq[event.StartEvent] {
	return func(yield func(event.StartEvent) bool) {
		if def == nil {
			return
		}
		for _, n := range def.StartNodes() {
			se, ok := n.(event.StartEvent)
			if !ok {
				continue
			}
			if !yield(se) {
				return
			}
		}
	}
}

// latestPerID collapses defs to at most one definition per def.ID: the one with
// the highest Version. It is the runtime counterpart of the model.Latest /
// Qualifier{Version:0} "latest" convention, applied to event-based START
// enumeration (ADR-0121): a MemDefinitionRegistry keeps every registered version
// so in-flight instances can still resume, but only the LATEST version of each id
// starts NEW instances — a redeploy replaces the old version's start subscription
// (Camunda semantics). Without this collapse a version bump would make a message
// start perpetually ambiguous and a signal/timer start fan out one spurious
// instance per retained version.
//
// Input order is not significant: the highest Version wins deterministically
// (versions are unique per id, so there is no tie to break). The returned slice
// preserves first-seen order of the winning definitions.
func latestPerID(defs []*model.ProcessDefinition) []*model.ProcessDefinition {
	if len(defs) == 0 {
		return nil
	}
	// idx maps def.ID to its position in out, so we can overwrite in place when a
	// higher version arrives while keeping first-seen order stable.
	idx := make(map[string]int, len(defs))
	out := make([]*model.ProcessDefinition, 0, len(defs))
	for _, def := range defs {
		if def == nil {
			continue
		}
		if pos, ok := idx[def.ID]; ok {
			if def.Version > out[pos].Version {
				out[pos] = def
			}
			continue
		}
		idx[def.ID] = len(out)
		out = append(out, def)
	}
	return out
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
	for se := range startEvents(def) {
		if se.MessageName == name {
			return se.ID(), true
		}
	}
	return "", false
}

// signalStartDefs returns every definition+node pair, across defs, whose
// start event listens for the signal name. defs is first collapsed via
// latestPerID so a superseded version never fans out a spurious instance
// (ADR-0121); order then follows the surviving latest defs and each def's
// StartNodes order.
func signalStartDefs(defs []*model.ProcessDefinition, name string) []signalStartHit {
	var hits []signalStartHit
	for _, def := range latestPerID(defs) {
		for se := range startEvents(def) {
			if se.SignalName == name {
				hits = append(hits, signalStartHit{Def: def, NodeID: se.ID()})
			}
		}
	}
	return hits
}

// uniqueMessageStartDef finds the definition (and its start node) whose
// message-start name equals name, across defs. defs is first collapsed via
// latestPerID so two retained versions of the same id resolve to a single
// latest match rather than a false ambiguity (ADR-0121). count is the number of
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
	for _, def := range latestPerID(defs) {
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
// trigger. defs is first collapsed via latestPerID so a superseded version does
// not arm a duplicate start timer (ADR-0121).
func timerStartDefs(defs []*model.ProcessDefinition) []timerStartHit {
	var hits []timerStartHit
	for _, def := range latestPerID(defs) {
		for se := range startEvents(def) {
			if !se.Timer.IsZero() {
				hits = append(hits, timerStartHit{Def: def, NodeID: se.ID(), Trigger: se.Timer})
			}
		}
	}
	return hits
}

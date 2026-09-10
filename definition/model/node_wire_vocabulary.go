package model

import (
	"fmt"
	"slices"
)

// This file holds the value-vocabulary gate: the decode-side refusal of a value
// outside the closed set the key it is authored under declares.
//
// It is the sibling of node_wire_keys.go's kind-key gate, at the same seam and
// for the same structural reason, one step further in. The kind gate refuses a
// key the node's kind never READS. This one refuses a value that a kind does
// read and cannot RECOGNISE — which is the worse of the two, because an
// unrecognised value in a closed vocabulary does not vanish: it collapses onto
// the zero value of the type it parses into, and that zero value already means
// something.
//
// WHY THE DECODER AND NOT Validate, and it is a stronger claim here than for
// the kind gate. A dropped key is merely absent by the time Validate runs; a
// collapsed value has been REPLACED by a legal one. Validate receives []Node,
// where termination_outcome:"Abort" has already become event.OutcomeComplete —
// a value it must accept, because it is the same value a definition authored
// with "complete" produces. Nothing distinguishes them any more. fromWire is
// the only place the authored string still exists.
//
// WHY THE VOCABULARY IS SPELLED HERE rather than read off the type that owns
// it. definition/event declares the outcome type and its names, and event
// imports this package, so this package cannot import event: the dependency
// only runs one way. What removes the drift risk that would otherwise create is
// not a comment but a test — TestTerminationOutcomeVocabularyMatchesTheType, in
// the external test package that CAN import both — which fails if a name in
// event.TerminationOutcome.String() ever leaves this set.
//
// SCOPE, deliberately narrow. This gate covers termination_outcome and nothing
// else. The other closed vocabularies reachable from a NodeWire — end_behavior
// on an endEvent, and a trigger's kind (definition/model/trigger_wire.go's
// ReadTrigger, whose switch has no default arm either) — still collapse onto
// their zero values today. They are the same class of defect and are tracked as
// one; they are not fixed here, because establishing the shape on the one case
// whose collapse INVERTS a meaning is what makes the mechanism reviewable
// before the rest of the class rides on it.

// terminationOutcomes is the closed vocabulary the termination_outcome key
// declares, in the spelling definition/event's TerminationOutcome.String()
// writes back out. Authoring none of them is legal and is not listed: doc.go
// publishes "complete" as what a terminate end without an outcome means.
var terminationOutcomes = []string{"complete", "abort"}

// checkTerminationOutcome refuses w if it carries a termination_outcome outside
// terminationOutcomes.
//
// It runs AFTER checkNodeKeys, and the order is load-bearing rather than
// incidental: on a kind that never reads the key at all — a serviceTask, say —
// the key gate's "this kind does not carry key" is the accurate diagnostic, and
// reporting a vocabulary violation there would point the author at the value
// when the problem is the key.
//
// An empty value is accepted, and it has to be: NodeWire.TerminationOutcome is
// a plain string, so an authored "" and an absent key decode to the same wire.
// That costs nothing here, unlike the general case, because absent is legal.
// This is the one half of engine/errors.go's two-part precedent that does not
// transfer — see ErrInvalidTerminationOutcome.
func checkTerminationOutcome(w NodeWire) error {
	if w.TerminationOutcome == "" || slices.Contains(terminationOutcomes, w.TerminationOutcome) {
		return nil
	}
	return fmt.Errorf("%w: node %q declares termination_outcome %q, want one of %q or none",
		ErrInvalidTerminationOutcome, w.ID, w.TerminationOutcome, terminationOutcomes)
}

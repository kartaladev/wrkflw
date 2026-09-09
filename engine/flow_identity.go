package engine

import (
	"strconv"

	"github.com/kartaladev/wrkflw/definition/flow"
	"github.com/kartaladev/wrkflw/definition/model"
)

// flowRef pairs a sequence flow with its INDEX in the definition's Flows slice.
//
// The index is the engine's edge identity, and it exists because the authored
// `flow.SequenceFlow.ID` is not a key. `model.Validate` deliberately accepts any
// number of blank flow IDs — `flow.SequenceFlow` literals may omit the field, and
// it carries no `omitempty`, so a blank ID re-marshals explicitly as `"id":""`
// and round-trips through a store. Two distinct incoming edges of one gateway can
// therefore share an ID, and a converging parallel gateway keyed on that ID reads
// two tokens that crossed the SAME edge as two branches.
//
// Endpoint-derived synthetics do not fix that. Two edges may share a blank ID AND
// both endpoints — `engine/step_join_flow_identity_test.go`'s twin-edge fixture is
// such a definition and `model.Validate` returns nil for it — so `Source→Target`
// collapses them exactly as the blank ID does. The position does not: every flow
// in a definition has exactly one index, so the identity is unique by
// construction and the residual is empty rather than merely smaller.
type flowRef struct {
	// Index is the flow's position in the owning definition's Flows slice.
	Index int
	// Flow is the flow itself, by value, as model's accessors return it.
	Flow flow.SequenceFlow
}

// Identity returns the engine-minted identity of the edge, for stamping on
// [Token.ArrivalFlow] and for matching at a converging parallel gateway.
//
// The authored ID is appended after the index purely so the value is legible in a
// snapshot and in the public projection; the index alone is what makes it unique.
// Both halves are read from the SAME definition on both sides of the comparison —
// a token resolves its scope's effective definition before its join is tested —
// so an index never means one edge at the stamp and another at the match.
func (r flowRef) Identity() string {
	return strconv.Itoa(r.Index) + ":" + r.Flow.ID
}

// outgoingFlows returns the sequence flows leaving nodeID, each paired with its
// index in def.Flows.
//
// It is the index-carrying twin of [model.ProcessDefinition.Outgoing] and yields
// exactly the same flows in exactly the same order — both filter def.Flows in
// slice order. That equivalence is load-bearing and pinned by a test, not assumed:
// branch declaration order is a documented behaviour of this engine (forkParallel
// places tokens in definition order of outgoing flows, and an exclusive gateway
// takes the first flow whose condition holds), so a helper that reordered them
// would silently reroute processes.
func outgoingFlows(def *model.ProcessDefinition, nodeID string) []flowRef {
	var out []flowRef
	for i, f := range def.Flows {
		if f.Source == nodeID {
			out = append(out, flowRef{Index: i, Flow: f})
		}
	}
	return out
}

// incomingFlows returns the sequence flows entering nodeID, each paired with its
// index in def.Flows. It is the index-carrying twin of
// [model.ProcessDefinition.Incoming]; see [outgoingFlows] on why the order match
// is pinned rather than assumed.
func incomingFlows(def *model.ProcessDefinition, nodeID string) []flowRef {
	var in []flowRef
	for i, f := range def.Flows {
		if f.Target == nodeID {
			in = append(in, flowRef{Index: i, Flow: f})
		}
	}
	return in
}

// flowRefByID returns the flow with the given ID, paired with its index.
//
// It serves the three sites that hold a flow ID rather than a flow — a boundary
// arm's recorded outgoing flow, a node's DeadlineFlow, and a retry policy's
// RecoveryFlow — all of which are authored references that already resolve by ID
// and are unaffected by the ID not being a key: a blank one names no flow and is
// rejected upstream, and a duplicate non-blank one is refused by ErrDuplicateFlowID.
func flowRefByID(def *model.ProcessDefinition, id string) (flowRef, bool) {
	for i, f := range def.Flows {
		if f.ID == id {
			return flowRef{Index: i, Flow: f}, true
		}
	}
	return flowRef{}, false
}

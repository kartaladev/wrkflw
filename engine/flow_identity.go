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
// It serves the two sites that hold an AUTHORED flow ID rather than a flow: a
// node's DeadlineFlow (handleDeadlineFired) and a retry policy's RecoveryFlow
// (handleActionFailed). A duplicate NON-BLANK ID is refused by
// ErrDuplicateFlowID (definition/model/validate.go, the flowIDs loop; pinned by
// validate_test.go's "duplicate flow ID is rejected" case), so for a non-blank
// id the match is unambiguous in a validated definition.
//
// ⚠ AN AUTHORED FLOW ID IS NOT A KEY, and this lookup's safety is supplied by
// its callers rather than by the definition. definition/model skips f.ID == ""
// before the duplicate check and REFUSES A BLANK FLOW ID NOWHERE — the one
// `ID == ""` test in the package is that skip. (Flow IDs are examined
// elsewhere; the RecoveryFlow rule below is one such site. What does not exist
// is a rule that rejects a blank one.) So a blank id passed here matches the
// FIRST blank-ID flow in the whole definition, which may belong to an entirely
// unrelated pair of nodes.
//
// Both remaining callers refuse a blank before reaching the lookup, and the
// refusals are load-bearing rather than incidental — each is annotated at its
// site, so an edit that removes one is not silent:
//
//   - handleDeadlineFired returns a named error on an empty DeadlineFlow and
//     only then calls this function (engine/step_timers.go).
//   - handleActionFailed reaches its call from inside `if rf :=
//     recoveryFlowOf(node); rf != ""`, so the blank case is not merely
//     unguarded-against but lexically unable to reach it
//     (engine/step_triggers.go).
//
// Both fail CLOSED: a blank reference produces a named error and no routing,
// never a token on the wrong edge. That is why they are documented here rather
// than replaced. A caller that did NOT refuse a blank failed OPEN and was fixed
// instead — the boundary arm used to record its outgoing flow's authored ID and
// re-resolve it here, and a blank one routed the token to an unrelated node and
// invoked that node's action with model.Validate returning nil (#212).
// fireBoundaryArm now resolves the flow by its SOURCE, the boundary node. That
// key rests on TWO validation rules, not one — ErrDuplicateNodeID (node IDs
// deduplicated with no blank exemption) AND ErrDanglingFlow (every f.Source
// must name an existing node); relaxing either re-opens #212. boundaryArm's
// godoc carries the full statement.
//
// ⚠ One further asymmetry, recorded because nothing fails while it holds: the
// RecoveryFlow reference is licensed by a validation that matches on TWO keys
// (f.ID == rf && f.Source == n.ID(), in validate.go's RecoveryFlow loop) while
// this lookup matches on f.ID alone. The two agree for a validated definition —
// rf is non-blank there, and non-blank IDs are unique — so the lookup is weaker
// than the rule that licenses it rather than wrong. DeadlineFlow has no
// validator RULE at all — validate.go names it only in comments (five, all
// prose; there is no DeadlineFlow predicate in the file), so nothing upstream
// checks that a DeadlineFlow reference resolves, or that it is non-blank.
func flowRefByID(def *model.ProcessDefinition, id string) (flowRef, bool) {
	for i, f := range def.Flows {
		if f.ID == id {
			return flowRef{Index: i, Flow: f}, true
		}
	}
	return flowRef{}, false
}

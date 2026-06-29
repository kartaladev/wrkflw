package runtime

import (
	"context"
	"fmt"
)

// LineageReader composes the four lineage reads — call parent, call children,
// chain predecessor, chain successors — into a single InstanceLineage DTO.
// Construct it with NewLineageReader; it satisfies service.LineageAdmin.
type LineageReader struct {
	calls  CallLineageReader
	chains ChainLineageReader
}

// NewLineageReader constructs a LineageReader from the provided call and chain
// lineage reader ports. Both arguments are required.
func NewLineageReader(calls CallLineageReader, chains ChainLineageReader) *LineageReader {
	return &LineageReader{calls: calls, chains: chains}
}

// Lineage returns the single-hop lineage for instanceID: call parent (nil when
// root), call children (empty when none), chain predecessor (nil when chain
// root), chain successors (empty when none). It returns an error if any of the
// four underlying reads fails; all reads are performed sequentially and the
// first error terminates early.
func (r *LineageReader) Lineage(ctx context.Context, instanceID string) (InstanceLineage, error) {
	// 1. Call parent.
	callParentLink, err := r.calls.ParentOf(ctx, instanceID)
	if err != nil {
		return InstanceLineage{}, fmt.Errorf("workflow-runtime: lineage: parent of %s: %w", instanceID, err)
	}

	// 2. Call children.
	callChildLinks, err := r.calls.ChildrenOf(ctx, instanceID)
	if err != nil {
		return InstanceLineage{}, fmt.Errorf("workflow-runtime: lineage: children of %s: %w", instanceID, err)
	}

	// 3. Chain predecessor.
	chainPredLink, err := r.chains.PredecessorOf(ctx, instanceID)
	if err != nil {
		return InstanceLineage{}, fmt.Errorf("workflow-runtime: lineage: predecessor of %s: %w", instanceID, err)
	}

	// 4. Chain successors.
	chainSuccLinks, err := r.chains.SuccessorsOf(ctx, instanceID)
	if err != nil {
		return InstanceLineage{}, fmt.Errorf("workflow-runtime: lineage: successors of %s: %w", instanceID, err)
	}

	// Assemble DTO.
	lin := InstanceLineage{
		InstanceID:      instanceID,
		CallChildren:    make([]CallLinkRef, 0, len(callChildLinks)),
		ChainSuccessors: make([]ChainLinkRef, 0, len(chainSuccLinks)),
	}

	if callParentLink != nil {
		ref := callLinkParentToRef(*callParentLink)
		lin.CallParent = &ref
	}

	for _, cl := range callChildLinks {
		lin.CallChildren = append(lin.CallChildren, callLinkChildToRef(cl))
	}

	if chainPredLink != nil {
		ref := chainLinkToPredRef(*chainPredLink)
		lin.ChainPredecessor = &ref
	}

	for _, ch := range chainSuccLinks {
		lin.ChainSuccessors = append(lin.ChainSuccessors, chainLinkToSuccRef(ch))
	}

	return lin, nil
}

// callLinkToRef maps a CallLink to a CallLinkRef DTO. The InstanceID and
// fields are taken from the parent side (ParentInstanceID, ParentDefID, etc.)
// when the link describes the parent of the queried instance. When describing a
// child (ChildrenOf), the ChildInstanceID and its fields are used.
//
// For ParentOf: the interesting identity is the parent instance.
// For ChildrenOf: the interesting identity is the child instance.
//
// Since we reuse one mapper, the caller chooses which perspective to expose via
// the dedicated helpers below.
func callLinkParentToRef(cl CallLink) CallLinkRef {
	return CallLinkRef{
		InstanceID: cl.ParentInstanceID,
		DefID:      cl.ParentDefID,
		DefVersion: cl.ParentDefVersion,
		Depth:      cl.Depth,
	}
}

func callLinkChildToRef(cl CallLink) CallLinkRef {
	return CallLinkRef{
		InstanceID: cl.ChildInstanceID,
		DefID:      cl.ParentDefID,
		DefVersion: cl.ParentDefVersion,
		Depth:      cl.Depth,
	}
}

// chainLinkToPredRef maps a ChainLink to a ChainLinkRef describing the
// predecessor (its PredecessorID and PredecessorDefinitionRef).
func chainLinkToPredRef(ch ChainLink) ChainLinkRef {
	return ChainLinkRef{
		InstanceID:    ch.PredecessorID,
		DefinitionRef: ch.PredecessorDefinitionRef,
		Outcome:       string(ch.Outcome),
	}
}

// chainLinkToSuccRef maps a ChainLink to a ChainLinkRef describing the
// successor (its SuccessorID and SuccessorDefinitionRef).
func chainLinkToSuccRef(ch ChainLink) ChainLinkRef {
	return ChainLinkRef{
		InstanceID:    ch.SuccessorID,
		DefinitionRef: ch.SuccessorDefinitionRef,
		Outcome:       string(ch.Outcome),
	}
}

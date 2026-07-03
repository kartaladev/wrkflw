package monitor

import (
	"context"
	"fmt"

	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// LineageReader composes the four lineage reads — call parent, call children,
// chain predecessor, chain successors — into a single InstanceLineage DTO.
// Construct it with NewLineageReader; it satisfies service.LineageAdmin.
type LineageReader struct {
	calls  kernel.CallLineageReader
	chains kernel.ChainLineageReader
}

// NewLineageReader constructs a LineageReader from the provided call and chain
// lineage reader ports. Both arguments are required; it returns ErrNilDependency
// if either is nil.
func NewLineageReader(calls kernel.CallLineageReader, chains kernel.ChainLineageReader) (*LineageReader, error) {
	if calls == nil {
		return nil, fmt.Errorf("%w: calls", kernel.ErrNilDependency)
	}
	if chains == nil {
		return nil, fmt.Errorf("%w: chains", kernel.ErrNilDependency)
	}
	return &LineageReader{calls: calls, chains: chains}, nil
}

// Lineage returns the single-hop lineage for instanceID: call parent (nil when
// root), call children (empty when none), chain predecessor (nil when chain
// root), chain successors (empty when none). It returns an error if any of the
// four underlying reads fails; all reads are performed sequentially and the
// first error terminates early.
func (r *LineageReader) Lineage(ctx context.Context, instanceID string) (kernel.InstanceLineage, error) {
	// 1. Call parent.
	callParentLink, err := r.calls.ParentOf(ctx, instanceID)
	if err != nil {
		return kernel.InstanceLineage{}, fmt.Errorf("workflow-runtime: lineage: parent of %s: %w", instanceID, err)
	}

	// 2. Call children.
	callChildLinks, err := r.calls.ChildrenOf(ctx, instanceID)
	if err != nil {
		return kernel.InstanceLineage{}, fmt.Errorf("workflow-runtime: lineage: children of %s: %w", instanceID, err)
	}

	// 3. Chain predecessor.
	chainPredLink, err := r.chains.PredecessorOf(ctx, instanceID)
	if err != nil {
		return kernel.InstanceLineage{}, fmt.Errorf("workflow-runtime: lineage: predecessor of %s: %w", instanceID, err)
	}

	// 4. Chain successors.
	chainSuccLinks, err := r.chains.SuccessorsOf(ctx, instanceID)
	if err != nil {
		return kernel.InstanceLineage{}, fmt.Errorf("workflow-runtime: lineage: successors of %s: %w", instanceID, err)
	}

	// Assemble DTO.
	lin := kernel.InstanceLineage{
		InstanceID:      instanceID,
		CallChildren:    make([]kernel.CallLinkRef, 0, len(callChildLinks)),
		ChainSuccessors: make([]kernel.ChainLinkRef, 0, len(chainSuccLinks)),
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

// callLinkParentToRef maps a CallLink to a CallLinkRef for the parent side.
// The parent's definition (ParentDefID, ParentDefVersion) is faithfully recorded
// in wrkflw_call_links, so both identity and definition fields are populated.
func callLinkParentToRef(cl kernel.CallLink) kernel.CallLinkRef {
	return kernel.CallLinkRef{
		InstanceID: cl.ParentInstanceID,
		DefID:      cl.ParentDefID,
		DefVersion: cl.ParentDefVersion,
		Depth:      cl.Depth,
	}
}

// callLinkChildToRef maps a CallLink to a CallLinkRef for the child side.
// DefID and DefVersion are intentionally left at their zero values: the
// wrkflw_call_links table only stores the parent's definition — the child's
// own definition is not recorded there. An operator must fetch the child's
// instance snapshot to learn its definition ref.
func callLinkChildToRef(cl kernel.CallLink) kernel.CallLinkRef {
	return kernel.CallLinkRef{
		InstanceID: cl.ChildInstanceID,
		Depth:      cl.Depth,
	}
}

// chainLinkToPredRef maps a ChainLink to a ChainLinkRef describing the
// predecessor (its PredecessorID and PredecessorDefinitionRef).
func chainLinkToPredRef(ch kernel.ChainLink) kernel.ChainLinkRef {
	return kernel.ChainLinkRef{
		InstanceID:    ch.PredecessorID,
		DefinitionRef: ch.PredecessorDefinitionRef,
		Outcome:       string(ch.Outcome),
	}
}

// chainLinkToSuccRef maps a ChainLink to a ChainLinkRef describing the
// successor (its SuccessorID and SuccessorDefinitionRef).
func chainLinkToSuccRef(ch kernel.ChainLink) kernel.ChainLinkRef {
	return kernel.ChainLinkRef{
		InstanceID:    ch.SuccessorID,
		DefinitionRef: ch.SuccessorDefinitionRef,
		Outcome:       string(ch.Outcome),
	}
}

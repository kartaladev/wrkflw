package engine

import (
	"fmt"
	"time"

	"github.com/zakyalvan/krtlwrkflw/model"
)

// forkParallel consumes the incoming token and creates one Active token at each
// outgoing flow target (definition order). Used for a diverging parallel gateway.
// scopeID is the gateway token's scope; forked tokens inherit it.
func (s *InstanceState) forkParallel(def *model.ProcessDefinition, tok *Token, node model.Node, scopeID string, at time.Time) {
	outs := def.Outgoing(node.ID())
	s.consumeToken(tok, at)
	for _, f := range outs {
		s.placeTokenInScope(f.Target, scopeID, at)
	}
}

// forkInclusive consumes the incoming token and creates an Active token for every
// non-default outgoing flow whose condition is empty or true (definition order).
// If none are true it takes the default flow; if none are true and there is no
// default it returns ErrNoMatchingFlow.
// scopeID is the gateway token's scope; forked tokens inherit it.
func (s *InstanceState) forkInclusive(def *model.ProcessDefinition, tok *Token, node model.Node, scopeID string, at time.Time) error {
	var taken []model.SequenceFlow
	var dflt *model.SequenceFlow
	for _, f := range def.Outgoing(node.ID()) {
		if f.IsDefault {
			ff := f
			dflt = &ff
			continue
		}
		if f.Condition == "" {
			taken = append(taken, f)
			continue
		}
		ok, err := conditions.EvalBool(f.Condition, s.Variables)
		if err != nil {
			return fmt.Errorf("workflow-engine: gateway %q flow %q: %w", node.ID(), f.ID, err)
		}
		if ok {
			taken = append(taken, f)
		}
	}
	if len(taken) == 0 {
		if dflt == nil {
			return fmt.Errorf("%w: gateway %q", ErrNoMatchingFlow, node.ID())
		}
		taken = append(taken, *dflt)
	}
	s.consumeToken(tok, at)
	for _, f := range taken {
		s.placeTokenInScope(f.Target, scopeID, at)
	}
	return nil
}

// tryParallelJoin parks the arriving token at a converging parallel gateway and,
// once a token has arrived on every incoming flow within the SAME scope, consumes
// them all and forks to the gateway's outgoing flows. Until then the token waits
// as TokenAtJoin.
// scopeID is the joining token's scope; output tokens inherit it.
//
// SCOPE-LOCAL INVARIANT: both the arrived-count loop and the consume loop filter
// tokens by ScopeID == scopeID. This ensures that two concurrently-open scopes
// sharing the same inner join node ID (e.g. two sub-process instances using the
// same nested *ProcessDefinition) are independently counted and consumed.
// Cross-scope token counting would fire joins prematurely and merge executions.
func (s *InstanceState) tryParallelJoin(def *model.ProcessDefinition, tok *Token, node model.Node, scopeID string, at time.Time) {
	tok.State = TokenAtJoin

	arrived := 0
	for i := range s.Tokens {
		if s.Tokens[i].NodeID == node.ID() && s.Tokens[i].State == TokenAtJoin && s.Tokens[i].ScopeID == scopeID {
			arrived++
		}
	}
	if arrived < len(def.Incoming(node.ID())) {
		return // still waiting on other branches in this scope
	}

	// Fire: remove all tokens parked at this join IN THIS SCOPE (closing their visits),
	// then create one Active token per outgoing flow.
	kept := make([]Token, 0, len(s.Tokens))
	for _, t := range s.Tokens {
		if t.NodeID == node.ID() && t.State == TokenAtJoin && t.ScopeID == scopeID {
			s.closeVisit(t.ID, t.NodeID, at)
			continue
		}
		kept = append(kept, t)
	}
	s.Tokens = kept
	for _, f := range def.Outgoing(node.ID()) {
		s.placeTokenInScope(f.Target, scopeID, at)
	}
}

// tryInclusiveJoin parks the arriving token at an OR-join and fires only once no
// token OTHER THAN those already parked at the join (within the SAME scope) can
// still reach it (so it never waits for branches that were never activated). On
// firing it consumes all tokens parked at the join and creates one Active token per
// outgoing flow.
// scopeID is the joining token's scope; output tokens inherit it.
//
// SCOPE-LOCAL INVARIANT: the reachability check and the consume loop both filter
// by ScopeID == scopeID so that concurrent scopes sharing the same inner node IDs
// do not cause cross-scope waiting or cross-scope token consumption.
func (s *InstanceState) tryInclusiveJoin(def *model.ProcessDefinition, tok *Token, node model.Node, scopeID string, at time.Time) {
	tok.State = TokenAtJoin

	canReach := nodesThatCanReach(def, node.ID())
	for i := range s.Tokens {
		t := &s.Tokens[i]
		if t.NodeID == node.ID() && t.State == TokenAtJoin && t.ScopeID == scopeID {
			continue // already arrived at the join in this scope
		}
		if t.ScopeID == scopeID && canReach[t.NodeID] {
			return // some token in this scope can still reach the join; keep waiting
		}
	}

	// Fire: consume all tokens parked at this join IN THIS SCOPE, then fork to outgoing flows.
	kept := make([]Token, 0, len(s.Tokens))
	for _, t := range s.Tokens {
		if t.NodeID == node.ID() && t.State == TokenAtJoin && t.ScopeID == scopeID {
			s.closeVisit(t.ID, t.NodeID, at)
			continue
		}
		kept = append(kept, t)
	}
	s.Tokens = kept
	for _, f := range def.Outgoing(node.ID()) {
		s.placeTokenInScope(f.Target, scopeID, at)
	}
}

// nodesThatCanReach returns the set of node IDs (excluding target) from which
// target is reachable by following sequence flows forward. Implemented as a
// reverse breadth-first search from target over incoming flows; the visited guard
// makes it safe on graphs with cycles that do not pass through target.
func nodesThatCanReach(def *model.ProcessDefinition, target string) map[string]bool {
	canReach := make(map[string]bool)
	var queue []string
	enqueue := func(n string) {
		if n != target && !canReach[n] {
			canReach[n] = true
			queue = append(queue, n)
		}
	}
	for _, f := range def.Incoming(target) {
		enqueue(f.Source)
	}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		for _, f := range def.Incoming(n) {
			enqueue(f.Source)
		}
	}
	return canReach
}

// selectExclusiveTarget picks the target of an exclusive gateway: the first
// outgoing flow (in definition order) with an empty or true condition, else the
// default flow, else ErrNoMatchingFlow.
func selectExclusiveTarget(def *model.ProcessDefinition, s *InstanceState, node model.Node) (string, error) {
	var defaultFlow *model.SequenceFlow
	for _, f := range def.Outgoing(node.ID()) {
		if f.IsDefault {
			ff := f
			defaultFlow = &ff
			continue
		}
		if f.Condition == "" {
			return f.Target, nil
		}
		ok, err := conditions.EvalBool(f.Condition, s.Variables)
		if err != nil {
			return "", fmt.Errorf("workflow-engine: gateway %q flow %q: %w", node.ID(), f.ID, err)
		}
		if ok {
			return f.Target, nil
		}
	}
	if defaultFlow != nil {
		return defaultFlow.Target, nil
	}
	return "", fmt.Errorf("%w: gateway %q", ErrNoMatchingFlow, node.ID())
}

// resolveGatewayWin routes an event-based gateway when one of its armed events
// fires. It is called from the TimerFired/SignalReceived/MessageReceived handlers
// in Step when the fired event correlates to an armedEvent entry.
//
// Contract:
//   - The winning arm is identified by ae (the armedEvent that matched).
//   - The gateway token (ae.GatewayToken) is moved directly to the catch node's
//     outgoing target (skipping the catch node itself — it has already "fired").
//   - All armedEvent entries for this gateway are removed; CancelTimer commands are
//     emitted for any sibling timer arms (the winning arm's TimerID is also in the
//     removal list and a CancelTimer is emitted for it too — but it fired, so the
//     runtime should handle a redundant cancel gracefully; alternatively, we skip
//     cancelling the winner's timer since it already fired). We SKIP cancelling the
//     winner's timer since it has already fired and no longer exists in the scheduler.
//   - drive() is called to advance execution beyond the routed target.
func resolveGatewayWin(def *model.ProcessDefinition, s *InstanceState, ae armedEvent, at time.Time, mode StepMode) ([]Command, error) {
	// Find the gateway token.
	tok := s.tokenAwaiting("evtgw:" + ae.GatewayToken)
	if tok == nil {
		// Gateway token is gone (already resolved by another concurrent path).
		// This is a late/duplicate trigger: clean no-op.
		// Remove any stale armed events for this gateway.
		s.removeArmedEventsForGateway(ae.GatewayToken)
		return nil, nil
	}

	// Resolve the effective definition for the gateway token's scope so that
	// an event-based gateway inside a sub-process resolves its catch nodes
	// against the nested definition.
	tdef, err := defForScope(def, s, tok.ScopeID)
	if err != nil {
		return nil, err
	}

	// Find the catch node's outgoing target so we can skip directly to the branch.
	// The catch node has "fired" by the arriving event; we route the gateway token
	// straight to the catch node's outgoing target (its downstream node).
	catchOuts := tdef.Outgoing(ae.CatchNode)
	var branchTarget string
	if len(catchOuts) > 0 {
		branchTarget = catchOuts[0].Target
	}

	// Activate the gateway token and route it to the branch target.
	tok.AwaitCommand = ""
	tok.State = TokenActive
	if branchTarget != "" {
		// Close the gateway-node visit and open a visit at the branch target,
		// skipping the catch node (it fires implicitly).
		s.closeVisit(tok.ID, tok.NodeID, at)
		tok.NodeID = branchTarget
		tok.EnteredAt = at
		s.openVisit(tok.ID, branchTarget, at)
	} else {
		// Fallback: move along the gateway's outgoing flow to the catch node.
		// model.Validate rejects a catch node with no outgoing flow, so this
		// branch is unreachable in a validated definition. It is retained as a
		// defensive fallback so the engine degrades gracefully rather than
		// panicking if an unvalidated definition is passed.
		s.moveAlongSingleFlow(tdef, tok, at)
	}

	// Remove ALL armedEvent entries for this gateway (winning + sibling arms).
	// The winning arm's timer (if it was a timer arm) is excluded from CancelTimer
	// commands because it already fired and no longer exists in the scheduler.
	// All sibling timer arms are cancelled.
	winningTimerID := ae.TimerID
	siblingsToCancel := s.removeArmedEventsForGateway(ae.GatewayToken)

	var cmds []Command
	for _, tid := range siblingsToCancel {
		if tid == winningTimerID {
			continue // skip cancelling the timer that already fired
		}
		cmds = append(cmds, CancelTimer{TimerID: tid})
	}

	// Drive forward from the branch target.
	driveCmds, err := drive(def, s, at, mode)
	if err != nil {
		return nil, err
	}
	cmds = append(cmds, driveCmds...)
	return cmds, nil
}

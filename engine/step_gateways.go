package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/kartaladev/wrkflw/definition/model"
)

// forkParallel consumes the incoming token and creates one Active token at each
// outgoing flow target (definition order). Used for a diverging parallel gateway.
// scopeID is the gateway token's scope; forked tokens inherit it.
func (s *InstanceState) forkParallel(def *model.ProcessDefinition, tok *Token, node model.Node, scopeID string, at time.Time) {
	outs := outgoingFlows(def, node.ID())
	s.consumeToken(tok, at)
	for _, f := range outs {
		s.placeTokenInScope(f.Flow.Target, scopeID, f.Identity(), at)
	}
}

// forkInclusive consumes the incoming token and creates an Active token for every
// non-default outgoing flow whose condition is empty or true (definition order).
// If none are true it takes the default flow; if none are true and there is no
// default it returns ErrNoMatchingFlow.
// scopeID is the gateway token's scope; forked tokens inherit it.
func (s *InstanceState) forkInclusive(def *model.ProcessDefinition, tok *Token, node model.Node, scopeID string, at time.Time, eval ConditionEvaluator) error {
	var taken []flowRef
	var dflt *flowRef
	for _, f := range outgoingFlows(def, node.ID()) {
		if f.Flow.IsDefault {
			ff := f
			dflt = &ff
			continue
		}
		if f.Flow.Condition == "" {
			taken = append(taken, f)
			continue
		}
		ok, err := eval.EvalBool(f.Flow.Condition, s.Variables)
		if err != nil {
			return fmt.Errorf("workflow-engine: gateway %q flow %q: %w", node.ID(), f.Flow.ID, err)
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
		s.placeTokenInScope(f.Flow.Target, scopeID, f.Identity(), at)
	}
	return nil
}

// joinedAt reports whether t is a token already parked at nodeID's join within
// scopeID. The ScopeID term is what keeps two concurrently-open scopes that
// share the same inner join node ID accounted and consumed independently.
//
// It answers "is this token at the join", never "which branch did it come from":
// the parallel join needs both, and gets the second from [Token.ArrivalFlow]
// via matchJoinTokensToFlows.
func joinedAt(t *Token, nodeID, scopeID string) bool {
	return t.NodeID == nodeID && t.State == TokenJoining && t.ScopeID == scopeID
}

// fireJoin consumes tokens parked at node's join in scopeID (closing their
// visits) and places one Active token per outgoing flow, in the same scope. It
// is the shared tail of tryParallelJoin and tryInclusiveJoin, which differ in
// the readiness test that decides when to call it AND in how much they consume.
//
// consume names the token IDs to remove. A nil set means "every token parked at
// this join in this scope", which is the INCLUSIVE join's rule: an OR-join fires
// only once nothing else can reach it, so every token that did reach it belongs
// to this firing. The PARALLEL join passes an explicit set holding exactly one
// token per incoming sequence flow, leaving any surplus parked as TokenJoining
// for a later firing — see [InstanceState.tryParallelJoin].
func (s *InstanceState) fireJoin(def *model.ProcessDefinition, node model.Node, scopeID string, at time.Time, consume map[string]bool) {
	kept := make([]Token, 0, len(s.Tokens))
	for _, t := range s.Tokens {
		if joinedAt(&t, node.ID(), scopeID) && (consume == nil || consume[t.ID]) {
			s.closeVisit(t.ID, t.NodeID, at)
			continue
		}
		kept = append(kept, t)
	}
	s.Tokens = kept
	for _, f := range outgoingFlows(def, node.ID()) {
		s.placeTokenInScope(f.Flow.Target, scopeID, f.Identity(), at)
	}
}

// tryParallelJoin parks the arriving token at a converging parallel gateway and,
// once EVERY INCOMING SEQUENCE FLOW has delivered a token within the SAME scope,
// consumes one token per incoming flow and forks to the gateway's outgoing flows.
// Until then the token waits as TokenJoining.
// scopeID is the joining token's scope; output tokens inherit it.
//
// PER FLOW, NOT PER ARRIVAL. BPMN 2.0 §13.3.2 makes a converging parallel gateway
// consume one token from each incoming sequence flow. Counting arrivals instead
// is not the same test: a diverging gateway whose branches implicitly re-merge
// (two branches routed through one shared exclusive gateway, say) delivers two
// tokens over ONE incoming edge, which satisfies an arrival count of two while
// the gateway's other edge has never been traversed. The join then fires with a
// branch still outstanding and the process continues past a synchronisation point
// that never happened. Tokens carry [Token.ArrivalFlow] so the two tests can be
// told apart; matchJoinTokensToFlows is where they diverge.
//
// SCOPE-LOCAL INVARIANT: both the coverage match and the consume set filter
// tokens by ScopeID == scopeID. This ensures that two concurrently-open scopes
// sharing the same inner join node ID (e.g. two sub-process instances using the
// same nested *ProcessDefinition) are independently satisfied and consumed.
// Cross-scope token accounting would fire joins prematurely and merge executions.
// It reports whether the join fired.
func (s *InstanceState) tryParallelJoin(def *model.ProcessDefinition, tok *Token, node model.Node, scopeID string, at time.Time) bool {
	tok.State = TokenJoining

	consume, ready := s.matchJoinTokensToFlows(def, node, scopeID)
	if !ready {
		return false // at least one incoming flow has delivered nothing in this scope
	}

	// Fire: remove exactly the matched tokens (closing their visits), leaving any
	// surplus parked as TokenJoining, then create one Active token per outgoing flow.
	s.fireJoin(def, node, scopeID, at, consume)
	return true
}

// matchJoinTokensToFlows assigns at most one parked token to each of node's
// incoming sequence flows, within scopeID. It returns the set of assigned token
// IDs and whether EVERY incoming flow was assigned one.
//
// The match runs in two passes, and the order matters. The first pass gives each
// flow a token whose [Token.ArrivalFlow] names that exact flow. Only then does
// the second pass spend the tokens whose provenance is empty, each satisfying one
// still-unassigned flow. Doing it the other way round — letting an empty-provenance
// token be consumed by the first flow that asks — would let it displace a token
// that could only ever have satisfied that one flow, and report a join unready
// that is in fact ready.
//
// An empty ArrivalFlow means the token traversed NO sequence flow, and after #120
// that is a narrow, enumerated population rather than a catch-all — the list is on
// [Token.ArrivalFlow]. Two members can actually reach a join: an operator-directed
// compensation relocation (CompensateRequested's partial rollback,
// ReverseInstance's full reverse), and a token decoded from a snapshot written
// before the field existed. Every genuine traversal mints an identity, including
// the compensation THROW resume, which carries compensationCursor.ResumeFlow.
//
// Spending each such token on at most ONE otherwise-unsatisfied flow is a
// deliberate concession: it stops an instance that was mid-join at upgrade time
// from deadlocking. It is fail-OPEN for the two populations above, and that is the
// chosen trade — a silent unrecoverable stall is worse than an early fire, and
// making the residual visible rather than silent is #79's subject. "At most one"
// is the half that keeps it bounded: consuming an unknown token per flow would let
// a single one satisfy an entire join.
func (s *InstanceState) matchJoinTokensToFlows(def *model.ProcessDefinition, node model.Node, scopeID string) (map[string]bool, bool) {
	incoming := incomingFlows(def, node.ID())
	assigned := make(map[string]bool, len(incoming))
	satisfied := make([]bool, len(incoming))

	for pass := range 2 {
		for i, f := range incoming {
			if satisfied[i] {
				continue
			}
			for j := range s.Tokens {
				t := &s.Tokens[j]
				if !joinedAt(t, node.ID(), scopeID) || assigned[t.ID] {
					continue
				}
				matches := t.ArrivalFlow == f.Identity()
				if pass == 1 {
					matches = t.ArrivalFlow == ""
				}
				if matches {
					assigned[t.ID] = true
					satisfied[i] = true
					break
				}
			}
		}
	}

	for _, ok := range satisfied {
		if !ok {
			return nil, false
		}
	}
	return assigned, true
}

// tryInclusiveJoin parks the arriving token at an OR-join and fires only once no
// token OTHER THAN those already parked at the join can still reach it (so it
// never waits for branches that were never activated). On firing it consumes all
// tokens parked at the join IN THIS SCOPE and creates one Active token per
// outgoing flow.
// scopeID is the joining token's scope; output tokens inherit it.
//
// A BRANCH INSIDE A SUB-PROCESS IS STILL A BRANCH. A token that has descended
// into a sub-process carries the CHILD scope's ID and a NodeID drawn from the
// nested definition — a node that does not exist in def at all — so a check that
// only looks at tokens with ScopeID == scopeID cannot see it and fires with that
// branch outstanding. Worse, it does not merely fire early: when the sub-process
// later drains, resumeInParentScope delivers a second token to the same join,
// which fires it a SECOND time and runs everything downstream twice.
// [InstanceState.subtreeCanReachJoin] is the check that closes this.
//
// SCOPE INVARIANTS, and they are deliberately different on the two sides:
//   - CONSUMPTION stays strictly scope-local. Two concurrently-open scopes sharing
//     the same inner join node ID (two instances of one nested definition) must
//     never consume each other's tokens.
//   - WAITING extends to the scope SUBTREE, but only downwards and only through a
//     node this definition can see: a child scope holds the join open when the
//     sub-process ACTIVITY that owns it — sc.NodeID, a node of def — can still
//     reach the join. A sibling or ancestor scope never does. That keeps the
//     property the scope-local filter was protecting (no cross-scope waiting
//     between unrelated executions) while admitting the one relationship that is
//     not cross-scope at all: a branch of this very execution, one level down.
//
// It reports whether the join fired.
func (s *InstanceState) tryInclusiveJoin(def *model.ProcessDefinition, tok *Token, node model.Node, scopeID string, at time.Time) bool {
	tok.State = TokenJoining

	canReach := nodesThatCanReach(def, node.ID())
	for i := range s.Tokens {
		t := &s.Tokens[i]
		if joinedAt(t, node.ID(), scopeID) {
			continue // already arrived at the join in this scope
		}
		if t.ScopeID == scopeID && canReach[t.NodeID] {
			return false // some token in this scope can still reach the join; keep waiting
		}
	}
	if s.subtreeCanReachJoin(scopeID, canReach) {
		return false // a branch of this scope is running inside a sub-process; keep waiting
	}

	// Fire: consume all tokens parked at this join IN THIS SCOPE, then fork to
	// outgoing flows. A nil consume set is the OR-join's consume-all rule: nothing
	// else can reach the join, so every token that did reach it belongs here.
	s.fireJoin(def, node, scopeID, at, nil)
	return true
}

// retryParkedJoins re-evaluates every token parked at a join and reports whether
// any join fired. It is the answer to a hazard both join kinds share: readiness is
// otherwise tested ONLY when a token walks into the join node, and not every event
// that discharges a wait is a token entry.
//
// Two such events are reachable, and each was a defect before this existed:
//   - a CHILD SCOPE CLOSING. An inclusive join waits while a sub-process branch
//     that can reach it is running. If that branch then diverges — takes a flow
//     away from the join and ends — its scope closes with nothing entering the
//     join, and the wait is discharged by an event no token accompanies. Without
//     re-evaluation the instance stalls forever, with no incident and nothing to
//     distinguish the stuck token from a legitimate wait.
//   - A SETTLED TOKEN SET that already covers every incoming flow. A parallel join
//     leaves surplus tokens parked, and a decoded snapshot can carry tokens parked
//     at a join that no later arrival will ever join. Such tokens are otherwise
//     inert: they satisfy the join and still hold the instance short of completion,
//     because exitRootScope counts them.
//
// It runs when drive() finds no active token — the point at which the token set
// has settled and the instance would otherwise be declared to have nothing to do.
//
// The scan restarts on the first firing rather than continuing: fireJoin
// RESLICES s.Tokens, so both the loop index and the *Token taken from it are
// invalid the moment a join fires. Reporting true sends drive() back through its
// own loop, which re-enters here only after the tokens the firing placed have
// been driven. That is also what bounds it: a firing consumes at least one parked
// token, so it cannot re-fire on an unchanged token set.
func (s *InstanceState) retryParkedJoins(def *model.ProcessDefinition, at time.Time) bool {
	for i := range s.Tokens {
		tok := &s.Tokens[i]
		if tok.State != TokenJoining {
			continue
		}
		tdef, err := defForScope(def, s, tok.ScopeID)
		if err != nil {
			continue // an unresolvable scope is drive()'s problem, not this sweep's
		}
		node, ok := tdef.Node(tok.NodeID)
		if !ok {
			continue
		}
		// Mirror the strategies' own guard: a gateway with one incoming flow is a
		// fork, and no token should be parked TokenJoining on one.
		if len(tdef.Incoming(node.ID())) <= 1 {
			continue
		}
		switch node.Kind() {
		case model.KindParallelGateway:
			if s.tryParallelJoin(tdef, tok, node, tok.ScopeID, at) {
				return true
			}
		case model.KindInclusiveGateway:
			if s.tryInclusiveJoin(tdef, tok, node, tok.ScopeID, at) {
				return true
			}
		default:
		}
	}
	return false
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

// subtreeCanReachJoin reports whether a scope nested directly inside scopeID is
// still holding a token AND is owned by a node that can still reach the join.
//
// canReach is the node set produced by nodesThatCanReach over the JOIN's own
// definition. A child Scope's NodeID is the sub-process (or call-activity, or
// event-sub-process) activity in the PARENT definition — which is that
// definition's own vocabulary — so it is the one identifier that translates a
// token sitting on a nested node back into a node the join can reason about. The
// tokens inside carry nested NodeIDs and are deliberately never consulted.
//
// Only DIRECT children are enumerated; tokensInScopeSubtree already reaches every
// grandchild beneath each of them, so a token three scopes down still holds the
// join open through its top-level ancestor. A child whose owning node cannot
// reach the join — a sub-process on a branch that ends elsewhere — holds nothing
// open, which is what stops this from degenerating into "wait for every open
// scope".
func (s *InstanceState) subtreeCanReachJoin(scopeID string, canReach map[string]bool) bool {
	for i := range s.Scopes {
		sc := &s.Scopes[i]
		if sc.ParentID != scopeID || !canReach[sc.NodeID] {
			continue
		}
		if s.tokensInScopeSubtree(sc.ID) > 0 {
			return true
		}
	}
	return false
}

// selectExclusiveTarget picks the outgoing flow an exclusive gateway takes: the
// first (in definition order) with an empty or true condition, else the default
// flow, else ErrNoMatchingFlow.
//
// It returns the whole flow rather than only its Target because the token that
// takes it must record which edge it traversed — an exclusive gateway can sit
// directly upstream of a converging parallel gateway, and that join accounts per
// incoming flow. See [Token.ArrivalFlow].
func selectExclusiveTarget(def *model.ProcessDefinition, s *InstanceState, node model.Node, eval ConditionEvaluator) (flowRef, error) {
	var defaultFlow *flowRef
	for _, f := range outgoingFlows(def, node.ID()) {
		if f.Flow.IsDefault {
			ff := f
			defaultFlow = &ff
			continue
		}
		if f.Flow.Condition == "" {
			return f, nil
		}
		ok, err := eval.EvalBool(f.Flow.Condition, s.Variables)
		if err != nil {
			return flowRef{}, fmt.Errorf("workflow-engine: gateway %q flow %q: %w", node.ID(), f.Flow.ID, err)
		}
		if ok {
			return f, nil
		}
	}
	if defaultFlow != nil {
		return *defaultFlow, nil
	}
	return flowRef{}, fmt.Errorf("%w: gateway %q", ErrNoMatchingFlow, node.ID())
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
func resolveGatewayWin(ctx context.Context, def *model.ProcessDefinition, s *InstanceState, ae armedEvent, at time.Time, pol stepPolicy) ([]Command, error) {
	// Find the gateway token BY IDENTITY, then confirm it is still parked on its
	// own sentinel.
	//
	// Resolving by the sentinel STRING alone (tokenAwaiting) is unsafe: Token.
	// AwaitCommand also carries consumer-minted ids (a human-task id, a
	// service-action command id) and [IDGenerator] is a seam the engine delegates
	// to verbatim — nothing validates the string it returns. A generator that
	// mints "evtgw:<token id>" would make an unrelated token match first
	// (tokenAwaiting returns the first hit in s.Tokens), driving that bystander
	// down the gateway's branch while the real gateway token parks forever.
	// The AwaitCommand check is still required: a gateway token whose await has
	// been cleared has already been resolved and must read as gone.
	tok := s.tokenByID(ae.GatewayToken)
	if tok != nil && tok.AwaitCommand != "evtgw:"+ae.GatewayToken {
		tok = nil
	}
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
	catchOuts := outgoingFlows(tdef, ae.CatchNode)
	var branchTarget, branchFlowID string
	if len(catchOuts) > 0 {
		branchTarget = catchOuts[0].Flow.Target
		// Captured here rather than re-indexed below: the routing branch guards on
		// branchTarget, which only implies len(catchOuts) > 0 by inference.
		branchFlowID = catchOuts[0].Identity()
	}

	// Activate the gateway token and route it to the branch target.
	tok.clearAwait()
	tok.State = TokenActive
	if branchTarget != "" {
		// Close the gateway-node visit and open a visit at the branch target,
		// skipping the catch node (it fires implicitly).
		s.closeVisit(tok.ID, tok.NodeID, at)
		tok.NodeID = branchTarget
		tok.EnteredAt = at
		// The catch node fires implicitly, so the edge actually traversed into
		// branchTarget is the catch node's own outgoing flow, not the gateway's.
		tok.ArrivalFlow = branchFlowID
		s.openVisit(tok.ID, branchTarget, at)
	} else {
		// Fallback: move along the gateway's outgoing flow to the catch node.
		// model.Validate rejects a catch node with no outgoing flow, so this
		// branch is unreachable in a validated definition. It is retained as a
		// defensive fallback so the engine degrades gracefully rather than
		// panicking if an unvalidated definition is passed.
		s.moveAlongSingleFlow(ctx, tdef, tok, at)
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
	driveCmds, err := drive(ctx, def, s, at, pol)
	if err != nil {
		return nil, err
	}
	cmds = append(cmds, driveCmds...)
	return cmds, nil
}

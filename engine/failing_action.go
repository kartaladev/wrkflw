package engine

import "github.com/zakyalvan/krtlwrkflw/definition/model"

// FailingActionNode maps a command ID to the node a token is currently parked on
// awaiting it, together with the scope-effective definition that node belongs to.
// It is the seam the runtime uses to resolve a failing action's node (from an
// ActionFailed trigger's CommandID) so it can look up a per-action retry policy and
// surface it as [StepOptions.OverrideRetryPolicy] — the engine itself stays free of
// the action package.
//
// ok is false when no token awaits commandID, or when the token's scope or node
// cannot be resolved (defensive; unreachable for a well-formed state built by Step).
// The state is passed by value and not mutated.
func FailingActionNode(def *model.ProcessDefinition, st InstanceState, commandID string) (model.Node, *model.ProcessDefinition, bool) {
	tok := st.tokenAwaiting(commandID)
	if tok == nil {
		return nil, nil, false
	}
	scopeDef, err := defForScope(def, &st, tok.ScopeID)
	if err != nil {
		return nil, nil, false
	}
	node, ok := scopeDef.Node(tok.NodeID)
	if !ok {
		return nil, nil, false
	}
	return node, scopeDef, true
}

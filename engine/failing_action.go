package engine

import "github.com/zakyalvan/krtlwrkflw/definition/model"

// FailingActionName maps a command ID to the catalog lookup name of the action a
// token is currently parked on awaiting it, together with the scope-effective
// definition that node belongs to. It is the seam the runtime uses to resolve a
// failing action (from an ActionFailed trigger's CommandID) so it can look up a
// per-action retry policy and surface it as [StepOptions.OverrideRetryPolicy] — the
// engine itself stays free of the action package, and the default-by-id lookup rule
// ([mainActionName]) stays owned here rather than duplicated in the runtime.
//
// ok is false when no token awaits commandID, or when the token's scope or node
// cannot be resolved (defensive; unreachable for a well-formed state built by Step).
// The state is passed by value and not mutated.
func FailingActionName(def *model.ProcessDefinition, st InstanceState, commandID string) (string, *model.ProcessDefinition, bool) {
	tok := st.tokenAwaiting(commandID)
	if tok == nil {
		return "", nil, false
	}
	scopeDef, err := defForScope(def, &st, tok.ScopeID)
	if err != nil {
		return "", nil, false
	}
	node, ok := scopeDef.Node(tok.NodeID)
	if !ok {
		return "", nil, false
	}
	return mainActionName(node), scopeDef, true
}

package engine

import "github.com/kartaladev/wrkflw/definition/model"

// TargetNode resolves the scope-correct node an external-input trigger
// targets, or (nil,false) if trg is not an external-input trigger kind
// (StartInstance, MessageReceived, HumanCompleted) or the trigger does not
// match any live token/arm in st.
//
// It mirrors Step's own dispatch (engine/step_triggers.go) tier-for-tier so
// the two never disagree on which node wins — in particular, a node nested
// inside a sub-process is resolved against ITS OWN nested ProcessDefinition
// (via defForScope), not the top-level def, which a flat def.Node lookup
// would silently miss.
func TargetNode(def *model.ProcessDefinition, st InstanceState, trg Trigger) (model.Node, bool) {
	switch t := trg.(type) {
	case StartInstance:
		// Mirror handleStartInstance's resolution (engine/step_triggers.go): an
		// explicit StartNodeID (set by NewStartInstanceAtNode for event-started
		// instances) targets that node directly; an empty StartNodeID resolves the
		// definition's manual (trigger-less) start. The old len(starts)==1 guard
		// silently skipped input validation for every multi-start / event-started
		// definition, since TargetNode is the sole enforcer of a StartEvent's
		// InputValidation (via validateInput in runtime/processdriver.go).
		startID := t.StartNodeID
		if startID == "" {
			n, err := resolveManualStart(def)
			if err != nil {
				return nil, false
			}
			startID = n
		}
		return def.Node(startID)
	case MessageReceived:
		nodeID, scopeID, ok := st.messageTargetNodeScoped(t.Name, t.CorrelationKey)
		if !ok {
			return nil, false
		}
		return nodeInScope(def, &st, scopeID, nodeID)
	case HumanCompleted:
		tok := st.tokenAwaiting(t.TaskToken)
		if tok == nil {
			return nil, false
		}
		return nodeInScope(def, &st, tok.ScopeID, tok.NodeID)
	default:
		return nil, false
	}
}

// nodeInScope resolves nodeID against the ProcessDefinition that governs
// scopeID (the top-level def for scopeID == "", or a sub-process's nested
// definition otherwise), returning (nil,false) if the scope or node cannot be
// resolved.
func nodeInScope(def *model.ProcessDefinition, st *InstanceState, scopeID, nodeID string) (model.Node, bool) {
	d, err := defForScope(def, st, scopeID)
	if err != nil {
		return nil, false
	}
	return d.Node(nodeID)
}

// messageTargetNodeScoped resolves the winning message target node AND its
// scope, tier-for-tier as handleMessageReceived (engine/step_triggers.go):
// event-gateway arm, then boundary arm, then event sub-process arm, then a
// plain parked token. scopeID "" means the root/top-level definition.
func (s *InstanceState) messageTargetNodeScoped(name, correlationKey string) (nodeID, scopeID string, ok bool) {
	if ae := s.armedEventByMessage(name, correlationKey); ae != nil {
		return ae.CatchNode, s.scopeOfToken(ae.GatewayToken), true
	}
	if ba := s.boundaryArmByMessage(name, correlationKey); ba != nil {
		return ba.BoundaryNode, s.scopeOfToken(ba.HostToken), true
	}
	if ea := s.eventTriggeredSubprocessArmByMessage(name, correlationKey); ea != nil {
		return ea.EventSubprocessNode, ea.EnclosingScopeID, true
	}
	if tok := s.tokenAwaitingMessage(name, correlationKey); tok != nil {
		return tok.NodeID, tok.ScopeID, true
	}
	return "", "", false
}

// scopeOfToken returns the ScopeID of the token with the given id, or "" (root
// scope) if no such token exists.
func (s *InstanceState) scopeOfToken(tokenID string) string {
	if tok := s.tokenByID(tokenID); tok != nil {
		return tok.ScopeID
	}
	return ""
}

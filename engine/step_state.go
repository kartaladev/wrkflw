package engine

import (
	"fmt"
	"time"

	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/humantask"
)

// defForScope returns the ProcessDefinition that a token in the given scope
// executes against. An empty scopeID (root) returns top. Otherwise the scope's
// NodeID is a sub-process activity node in the PARENT scope's definition; this
// function resolves that node and returns its Subprocess definition recursively.
//
// Returns an error if the scope or its subprocess definition cannot be resolved
// (defensive; unreachable for a well-formed state that was built by Step).
func defForScope(top *model.ProcessDefinition, s *InstanceState, scopeID string) (*model.ProcessDefinition, error) {
	if scopeID == "" {
		return top, nil
	}
	scope := s.scopeByID(scopeID)
	if scope == nil {
		return nil, fmt.Errorf("workflow-engine: defForScope: unknown scope %q", scopeID)
	}
	parentDef, err := defForScope(top, s, scope.ParentID)
	if err != nil {
		return nil, err
	}
	node, ok := parentDef.Node(scope.NodeID)
	if !ok {
		return nil, fmt.Errorf("workflow-engine: defForScope: sub-process node %q not found in parent definition", scope.NodeID)
	}
	switch n := node.(type) {
	case activity.SubProcess:
		if n.Subprocess == nil {
			return nil, fmt.Errorf("workflow-engine: defForScope: node %q has no Subprocess definition", scope.NodeID)
		}
		return n.Subprocess, nil
	case event.EventSubProcess:
		if n.Subprocess == nil {
			return nil, fmt.Errorf("workflow-engine: defForScope: node %q has no Subprocess definition", scope.NodeID)
		}
		return n.Subprocess, nil
	default:
		return nil, fmt.Errorf("workflow-engine: defForScope: node %q has no Subprocess definition", scope.NodeID)
	}
}

func (s *InstanceState) placeToken(nodeID string, at time.Time) {
	s.TokenSeq++
	id := fmt.Sprintf("%s-t%d", s.InstanceID, s.TokenSeq)
	s.Tokens = append(s.Tokens, Token{ID: id, NodeID: nodeID, State: TokenActive, EnteredAt: at})
	s.openVisit(id, nodeID, at)
}

// placeTokenInScope creates a new active token at nodeID tagged with the given
// scopeID. It is the scoped variant of placeToken, used when entering a
// sub-process scope so that inner tokens carry the correct ScopeID for
// defForScope resolution.
func (s *InstanceState) placeTokenInScope(nodeID, scopeID string, at time.Time) {
	s.TokenSeq++
	id := fmt.Sprintf("%s-t%d", s.InstanceID, s.TokenSeq)
	s.Tokens = append(s.Tokens, Token{ID: id, NodeID: nodeID, ScopeID: scopeID, State: TokenActive, EnteredAt: at})
	s.openVisit(id, nodeID, at)
}

func (s *InstanceState) firstActive() *Token {
	for i := range s.Tokens {
		if s.Tokens[i].State == TokenActive {
			return &s.Tokens[i]
		}
	}
	return nil
}

func (s *InstanceState) tokenAwaiting(cmdID string) *Token {
	for i := range s.Tokens {
		if s.Tokens[i].AwaitCommand == cmdID {
			return &s.Tokens[i]
		}
	}
	return nil
}

// tokenByID returns the first token whose ID matches, or nil.
func (s *InstanceState) tokenByID(tokenID string) *Token {
	for i := range s.Tokens {
		if s.Tokens[i].ID == tokenID {
			return &s.Tokens[i]
		}
	}
	return nil
}

// tokenIDsAwaitingSignal returns a snapshot of the token IDs (in slice order)
// of all tokens currently awaiting the given signal name. The returned slice
// captures the state at the call instant; tokens added to s.Tokens after this
// call are NOT included. Used by SignalReceived dispatch to implement snapshot
// semantics: only tokens awaiting the signal AT DELIVERY TIME are resumed.
func (s *InstanceState) tokenIDsAwaitingSignal(name string) []string {
	var ids []string
	for i := range s.Tokens {
		if s.Tokens[i].AwaitSignal == name {
			ids = append(ids, s.Tokens[i].ID)
		}
	}
	return ids
}

// tokenAwaitingMessage returns the first token whose AwaitMessage matches name
// AND whose AwaitMessageKey matches correlationKey. An empty correlationKey on
// the token (no key configured on the catch node) matches only when the
// incoming MessageReceived.CorrelationKey is also empty.
func (s *InstanceState) tokenAwaitingMessage(name, correlationKey string) *Token {
	for i := range s.Tokens {
		t := &s.Tokens[i]
		if t.AwaitMessage == name && t.AwaitMessageKey == correlationKey {
			return t
		}
	}
	return nil
}

func (s *InstanceState) nextCommandID() string {
	s.CmdSeq++
	return fmt.Sprintf("%s-c%d", s.InstanceID, s.CmdSeq)
}

func (s *InstanceState) nextTaskToken() string {
	s.TaskSeq++
	return fmt.Sprintf("%s-h%d", s.InstanceID, s.TaskSeq)
}

func (s *InstanceState) nextTimerID() string {
	s.TimerSeq++
	return fmt.Sprintf("%s-tm%d", s.InstanceID, s.TimerSeq)
}

// nextIncidentID returns the next deterministic incident ID of the form
// "<instanceID>-inc<IncidentSeq>", advancing the monotonic IncidentSeq counter.
func (s *InstanceState) nextIncidentID() string {
	s.IncidentSeq++
	return fmt.Sprintf("%s-inc%d", s.InstanceID, s.IncidentSeq)
}

// setVisitActor sets the ActorID on the most recent open NodeVisit for the
// given (tokenID, nodeID) pair. Used to record who completed a human task.
//
// If no matching open visit exists the call is a no-op. On the HumanCompleted
// path the visit is invariant-guaranteed to be open (a WaitingCommand token
// always has a corresponding open visit), so the silent no-op is safe there.
func (s *InstanceState) setVisitActor(tokenID, nodeID, actorID string) {
	for i := len(s.History) - 1; i >= 0; i-- {
		v := &s.History[i]
		if v.TokenID == tokenID && v.NodeID == nodeID && v.LeftAt == nil {
			v.ActorID = &actorID
			return
		}
	}
}

func (s *InstanceState) moveAlongSingleFlow(def *model.ProcessDefinition, tok *Token, at time.Time) {
	out := def.Outgoing(tok.NodeID)
	s.closeVisit(tok.ID, tok.NodeID, at)
	if len(out) == 0 {
		tok.State = TokenWaitingCommand // defensive; Validate forbids this
		return
	}
	tok.NodeID = out[0].Target
	tok.EnteredAt = at
	s.openVisit(tok.ID, tok.NodeID, at)
}

func (s *InstanceState) consumeToken(tok *Token, at time.Time) {
	s.closeVisit(tok.ID, tok.NodeID, at)
	id := tok.ID
	out := make([]Token, 0, len(s.Tokens))
	for _, t := range s.Tokens {
		if t.ID != id {
			out = append(out, t)
		}
	}
	s.Tokens = out
}

func (s *InstanceState) openVisit(tokenID, nodeID string, at time.Time) {
	s.History = append(s.History, NodeVisit{NodeID: nodeID, TokenID: tokenID, EnteredAt: at})
}

func (s *InstanceState) closeVisit(tokenID, nodeID string, at time.Time) {
	for i := len(s.History) - 1; i >= 0; i-- {
		v := &s.History[i]
		if v.TokenID == tokenID && v.NodeID == nodeID && v.LeftAt == nil {
			left := at
			v.LeftAt = &left
			return
		}
	}
}

// moveTokenToTarget moves a token to targetID, closing the old visit and opening
// a new one, leaving the token Active.
func (s *InstanceState) moveTokenToTarget(tok *Token, target string, at time.Time) {
	s.closeVisit(tok.ID, tok.NodeID, at)
	tok.NodeID = target
	tok.EnteredAt = at
	tok.State = TokenActive
	s.openVisit(tok.ID, target, at)
}

// effectiveRetryPolicy returns the retry policy to apply for the given node and
// step options, plus a boolean indicating whether a policy is in effect.
// Precedence: node-level policy > StepOptions.DefaultRetryPolicy > none.
// The returned policy has been normalized via [model.RetryPolicy.Normalize].
func effectiveRetryPolicy(node model.Node, opt StepOptions) (model.RetryPolicy, bool) {
	rp := model.RetryPolicyOf(node)
	switch {
	case rp != nil:
		return rp.Normalize(), true
	case opt.DefaultRetryPolicy != nil:
		return opt.DefaultRetryPolicy.Normalize(), true
	default:
		return model.RetryPolicy{}, false
	}
}

func mergeVars(s *InstanceState, in map[string]any) {
	if len(in) == 0 {
		return
	}
	if s.Variables == nil {
		s.Variables = make(map[string]any, len(in))
	}
	for k, v := range in {
		s.Variables[k] = v
	}
}

func copyVars(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// serviceActionInput builds the Input map for a node's primary action.Action
// invocation. It copies the instance variables and stamps a stable,
// attempt-independent idempotency key ("<instanceID>:<nodeID>") so action
// authors can dedup external side effects across retries.
//
// v1 scope: only the primary service-task action carries this key. Deadline,
// reminder, and compensation actions do NOT — those are separate fire-once
// operations on the same node; stamping instanceID:nodeID on them would
// collide with the primary action's key and could cause an external system to
// wrongly dedup distinct operations.
func serviceActionInput(s *InstanceState, node model.Node) map[string]any {
	in := copyVars(s.Variables)
	if in == nil {
		in = map[string]any{}
	}
	in["_idempotencyKey"] = s.InstanceID + ":" + node.ID()
	return in
}

func cloneState(st InstanceState) InstanceState {
	s := st
	s.Variables = copyVars(st.Variables)
	s.StartVariables = copyVars(st.StartVariables)
	s.Tokens = append([]Token(nil), st.Tokens...)
	for i := range s.Tokens {
		s.Tokens[i].Payload = copyVars(s.Tokens[i].Payload)
	}
	s.History = append([]NodeVisit(nil), st.History...)
	if st.EndedAt != nil {
		e := *st.EndedAt
		s.EndedAt = &e
	}
	// Deep-copy Tasks: each task's slice fields (Candidates, Eligibility.Roles,
	// Eligibility.Privileges) are independently allocated so mutations to the clone
	// do not affect the original — required for TestStepDoesNotMutateInput to hold.
	if len(st.Tasks) > 0 {
		s.Tasks = make([]humantask.HumanTask, len(st.Tasks))
		for i, t := range st.Tasks {
			ct := t
			ct.Candidates = append([]string(nil), t.Candidates...)
			ct.Eligibility.Roles = append([]string(nil), t.Eligibility.Roles...)
			ct.Eligibility.Privileges = append([]string(nil), t.Eligibility.Privileges...)
			s.Tasks[i] = ct
		}
	}
	// Deep-copy Timers: timerRecord is a value type (no pointers), so a slice copy
	// is sufficient to ensure mutations to the clone do not affect the original.
	s.Timers = append([]timerRecord(nil), st.Timers...)
	// Deep-copy ArmedEvents: armedEvent is a value type (no pointers), so a slice
	// copy is sufficient.
	s.ArmedEvents = append([]armedEvent(nil), st.ArmedEvents...)
	// Deep-copy Boundaries: boundaryArm is a value type (no pointers), so a slice
	// copy is sufficient.
	s.Boundaries = append([]boundaryArm(nil), st.Boundaries...)
	// Deep-copy EventSubprocesses: eventSubprocessArm is a value type (no pointers),
	// so a slice copy is sufficient to ensure mutations to the clone do not affect
	// the original.
	s.EventSubprocesses = append([]eventSubprocessArm(nil), st.EventSubprocesses...)
	// Deep-copy RootCompensations: each CompensationRecord contains an Input
	// map[string]any (a reference type) that must be independently allocated so
	// mutations to a clone's record do not affect the original.
	// Use append([]T(nil), src...) instead of a len>0 guard + make so that a
	// non-nil empty source produces a non-nil empty clone (nil-vs-empty consistency).
	{
		src := st.RootCompensations
		if src == nil {
			s.RootCompensations = nil
		} else {
			s.RootCompensations = make([]CompensationRecord, len(src))
			for i, cr := range src {
				ccr := cr
				ccr.Input = copyVars(cr.Input)
				s.RootCompensations[i] = ccr
			}
		}
	}
	// Deep-copy Scopes: each Scope contains a Compensations slice that must be
	// independently allocated so mutations to a clone's compensation records do
	// not affect the original. The other Scope fields (ID, NodeID, ParentID) are
	// plain strings (value types) and are correctly copied by the struct copy.
	// ScopeSeq is a scalar (int) and is already carried by the struct copy above.
	if len(st.Scopes) > 0 {
		s.Scopes = make([]Scope, len(st.Scopes))
		for i, sc := range st.Scopes {
			cs := sc
			// Deep-copy each CompensationRecord: the Input field is a map[string]any
			// (a reference type) and must be independently allocated so mutations to
			// a clone's Input do not propagate back to the original's record.
			// Use explicit nil-check for nil-vs-empty consistency: a nil Compensations
			// in the source produces nil in the clone; a non-nil empty slice produces
			// a non-nil empty clone.
			if sc.Compensations == nil {
				cs.Compensations = nil
			} else if len(sc.Compensations) > 0 {
				cs.Compensations = make([]CompensationRecord, len(sc.Compensations))
				for j, cr := range sc.Compensations {
					ccr := cr
					ccr.Input = copyVars(cr.Input)
					cs.Compensations[j] = ccr
				}
			}
			s.Scopes[i] = cs
		}
	}
	// Deep-copy ArchivedCompensations: each entry in the map holds a
	// []CompensationRecord whose Input fields are map[string]any (reference types)
	// and must be independently allocated so mutations to the clone do not affect
	// the original. nil map in source → nil in clone.
	if st.ArchivedCompensations != nil {
		s.ArchivedCompensations = make(map[string][]CompensationRecord, len(st.ArchivedCompensations))
		for k, recs := range st.ArchivedCompensations {
			if recs == nil {
				s.ArchivedCompensations[k] = nil
			} else {
				cloned := make([]CompensationRecord, len(recs))
				for i, cr := range recs {
					ccr := cr
					ccr.Input = copyVars(cr.Input)
					cloned[i] = ccr
				}
				s.ArchivedCompensations[k] = cloned
			}
		}
	}
	// Deep-copy Incidents: Incident is a flat value struct (all fields are plain
	// scalars — no pointers or maps), so an append-copy of the slice is sufficient
	// to ensure mutations to the clone's Incidents do not affect the original.
	if len(st.Incidents) > 0 {
		s.Incidents = append([]Incident(nil), st.Incidents...)
	}
	// Deep-copy DeferredCompensationThrows: a []string of token IDs (value type),
	// so an append-copy is sufficient to isolate the clone from the original.
	s.DeferredCompensationThrows = append([]string(nil), st.DeferredCompensationThrows...)
	return s
}

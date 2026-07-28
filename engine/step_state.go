package engine

import (
	"context"
	"fmt"
	"maps"
	"time"

	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/humantask"
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
	default:
		return nil, fmt.Errorf("workflow-engine: defForScope: node %q has no Subprocess definition", scope.NodeID)
	}
}

func (s *InstanceState) placeToken(nodeID string, at time.Time) {
	id := s.nextID("t", &s.TokenSeq)
	s.Tokens = append(s.Tokens, Token{ID: id, NodeID: nodeID, State: TokenActive, EnteredAt: at})
	s.openVisit(id, nodeID, at)
}

// placeTokenInScope creates a new active token at nodeID tagged with the given
// scopeID. It is the scoped variant of placeToken, used when entering a
// sub-process scope so that inner tokens carry the correct ScopeID for
// defForScope resolution.
func (s *InstanceState) placeTokenInScope(nodeID, scopeID string, at time.Time) {
	id := s.nextID("t", &s.TokenSeq)
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

// tokenAwaiting returns the token parked on the given command id, or nil.
//
// An empty cmdID names no token (ADR-0152): a Token parks on exactly one of
// AwaitCommand/AwaitSignal/AwaitMessage (state.go:89-101), leaving the other
// two "". Without this guard, cmdID == "" would match any token NOT parked
// on a command — an unrelated wildcard hit.
func (s *InstanceState) tokenAwaiting(cmdID string) *Token {
	if cmdID == "" {
		return nil
	}
	for i := range s.Tokens {
		if s.Tokens[i].AwaitCommand == cmdID {
			return &s.Tokens[i]
		}
	}
	return nil
}

// tokenByID returns the first token whose ID matches, or nil.
//
// An empty tokenID names no token (ADR-0152); a Token's ID is always assigned
// by nextID and is never legitimately empty.
func (s *InstanceState) tokenByID(tokenID string) *Token {
	if tokenID == "" {
		return nil
	}
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
//
// An empty name names no signal (ADR-0152): a Token parks on exactly one of
// AwaitCommand/AwaitSignal/AwaitMessage (state.go:89-101), so without this
// guard name == "" would select every token NOT awaiting a signal and a
// consumer-built SignalReceived{Name: ""} would broadcast-resume them all.
func (s *InstanceState) tokenIDsAwaitingSignal(name string) []string {
	if name == "" {
		return nil
	}
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
//
// An empty name names no message (ADR-0152): a Token parks on exactly one of
// AwaitCommand/AwaitSignal/AwaitMessage (state.go:89-101), so without this
// guard name == "" would match any token NOT parked on a message.
// correlationKey is deliberately NOT guarded: "" is the legitimate
// "uncorrelated" value (see the AwaitMessageKey doc on Token), and must keep
// matching a token whose AwaitMessageKey is also empty.
func (s *InstanceState) tokenAwaitingMessage(name, correlationKey string) *Token {
	if name == "" {
		return nil
	}
	for i := range s.Tokens {
		t := &s.Tokens[i]
		if t.AwaitMessage == name && t.AwaitMessageKey == correlationKey {
			return t
		}
	}
	return nil
}

func (s *InstanceState) nextCommandID() string { return s.nextID("c", &s.CmdSeq) }

func (s *InstanceState) nextTaskID() string { return s.nextID("h", &s.TaskSeq) }

func (s *InstanceState) nextTimerID() string { return s.nextID("tm", &s.TimerSeq) }

// nextIncidentID returns the next incident ID, advancing the monotonic
// IncidentSeq counter. See [InstanceState.nextID] for the id strategy.
func (s *InstanceState) nextIncidentID() string { return s.nextID("inc", &s.IncidentSeq) }

// setVisitTask links the most recent open NodeVisit for the given
// (tokenID, nodeID) pair to the human task minted for it (ADR-0145).
//
// If no matching open visit exists the call is a no-op. On the user-task entry
// path the visit is invariant-guaranteed to be open (the token opened it when
// it arrived at the node), so the silent no-op is safe there.
func (s *InstanceState) setVisitTask(tokenID, nodeID, taskID string) {
	if v := s.openVisitFor(tokenID, nodeID); v != nil {
		v.TaskID = taskID
	}
}

// openVisitFor returns the most recent open (not-yet-left) NodeVisit for the
// given (tokenID, nodeID) pair, or nil when none is open.
//
// Both tokenID and nodeID are identity keys into s.History; an empty value on
// either side names no visit (ADR-0152), so both are guarded. Callers
// (setVisitTask, closeVisit, closeVisitAs) pass through to this guard rather
// than duplicating it.
func (s *InstanceState) openVisitFor(tokenID, nodeID string) *NodeVisit {
	if tokenID == "" || nodeID == "" {
		return nil
	}
	for i := len(s.History) - 1; i >= 0; i-- {
		v := &s.History[i]
		if v.TokenID == tokenID && v.NodeID == nodeID && v.LeftAt == nil {
			return v
		}
	}
	return nil
}

func (s *InstanceState) moveAlongSingleFlow(def *model.ProcessDefinition, tok *Token, at time.Time) {
	out := def.Outgoing(tok.NodeID)
	s.closeVisit(tok.ID, tok.NodeID, at)
	if len(out) == 0 {
		tok.State = TokenWaiting // defensive; Validate forbids this
		return
	}
	tok.NodeID = out[0].Target
	tok.EnteredAt = at
	s.openVisit(tok.ID, tok.NodeID, at)
}

// resumeAndDrive resumes a parked token whose scope-effective definition tdef
// and cancel/cleanup commands (preCmds) have already been resolved by the
// caller — the five trigger handlers that resume a parked token each clear a
// different await field and collect a different set of CancelTimer commands
// before reaching this common tail, so those steps stay at the call site.
// resumeAndDrive marks the token Active, moves it along its single outgoing
// flow within tdef, and drives the instance forward, returning preCmds
// followed by the driven commands (preserving each call site's existing
// command order) and any drive error.
func resumeAndDrive(ctx context.Context, def *model.ProcessDefinition, tdef *model.ProcessDefinition, s *InstanceState, tok *Token, at time.Time, opt StepOptions, preCmds []Command) ([]Command, error) {
	tok.State = TokenActive
	s.moveAlongSingleFlow(tdef, tok, at)
	driveCmds, err := drive(ctx, def, s, at, opt.Mode, resolveEvaluator(opt))
	if err != nil {
		return nil, err
	}
	return append(preCmds, driveCmds...), nil
}

// consumeToken removes tok from the token set, closing its visit as a NORMAL
// close (no CloseKind). Terminal/abnormal sites use consumeTokenAs instead.
func (s *InstanceState) consumeToken(tok *Token, at time.Time) {
	s.consumeTokenAs(tok, at, "")
}

// removeToken drops the token with the given id from the token set.
//
// An empty id names no token (ADR-0152): a Token's ID is always assigned by
// nextID and is never legitimately empty, so an empty id is a no-op rather
// than a mass-removal of every token that happens to lack one.
func (s *InstanceState) removeToken(id string) {
	if id == "" {
		return
	}
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
	if v := s.openVisitFor(tokenID, nodeID); v != nil {
		left := at
		v.LeftAt = &left
	}
}

// moveTokenToTarget moves a token to targetID, closing the old visit as a
// NORMAL close and opening a new one, leaving the token Active.
func (s *InstanceState) moveTokenToTarget(tok *Token, target string, at time.Time) {
	s.moveTokenToTargetAs(tok, target, at, "")
}

// moveTokenToTargetAs is moveTokenToTarget with an abnormal close reason
// stamped on the visit being left (ADR-0145).
func (s *InstanceState) moveTokenToTargetAs(tok *Token, target string, at time.Time, closeKind CloseKind) {
	s.closeVisitAs(tok.ID, tok.NodeID, at, closeKind)
	tok.NodeID = target
	tok.EnteredAt = at
	tok.State = TokenActive
	s.openVisit(tok.ID, target, at)
}

// effectiveRetryPolicy returns the retry policy to apply for the given node and
// step options, plus a boolean indicating whether a policy is in effect.
// Precedence: StepOptions.OverrideRetryPolicy > node-level policy >
// StepOptions.DefaultRetryPolicy > none. The override is the runtime's seam for a
// per-action retry policy (action > node > runtime-default).
//
// FIELD-MERGE (ADR-0126): the override tier is fed from action.RetrySpecs, which
// can express only MaxAttempts/InitialInterval/BackoffCoef/MaxInterval. When the
// override is applied AND the node also declares a policy, the node's
// safety-only fields the action tier cannot express — MaxElapsed and
// NonRetryableErrors — are PRESERVED from the node rather than silently dropped.
// The action still wins on every field it can express.
//
// The returned policy has been normalized via [model.RetryPolicy.Normalize].
func effectiveRetryPolicy(node model.Node, opt StepOptions) (model.RetryPolicy, bool) {
	rp := model.RetryPolicyOf(node)
	switch {
	case opt.OverrideRetryPolicy != nil:
		eff := *opt.OverrideRetryPolicy
		if rp != nil {
			// Inherit the node's safety-only fields the action tier can't express.
			eff.MaxElapsed = rp.MaxElapsed
			eff.NonRetryableErrors = rp.NonRetryableErrors
		}
		return eff.Normalize(), true
	case rp != nil:
		return rp.Normalize(), true
	case opt.DefaultRetryPolicy != nil:
		return opt.DefaultRetryPolicy.Normalize(), true
	default:
		return model.RetryPolicy{}, false
	}
}

// mergeVars copies in over s.Variables, allocating the destination when it is
// nil — maps.Copy panics on a nil destination, so the guard must stay.
func mergeVars(s *InstanceState, in map[string]any) {
	if len(in) == 0 {
		return
	}
	if s.Variables == nil {
		s.Variables = make(map[string]any, len(in))
	}
	maps.Copy(s.Variables, in)
}

// copyVars returns an independently allocated shallow copy of in. nil in, nil out.
func copyVars(in map[string]any) map[string]any { return maps.Clone(in) }

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

// cloneCompensationRecords returns an independently allocated copy of recs with
// each record's Input map deep-copied, so mutating the clone cannot affect the
// original. nil in, nil out; a non-nil empty source yields a non-nil empty clone.
func cloneCompensationRecords(recs []CompensationRecord) []CompensationRecord {
	if recs == nil {
		return nil
	}
	out := make([]CompensationRecord, len(recs))
	for i, cr := range recs {
		cr.Input = copyVars(cr.Input)
		out[i] = cr
	}
	return out
}

func cloneState(st InstanceState) InstanceState {
	s := st
	s.Variables = copyVars(st.Variables)
	// StartVariables is immutable-by-convention after handleStartInstance sets it
	// once, so a per-Step deep copy is not required for correctness — but it is
	// kept deep anyway to satisfy Clone's "independently allocated maps" contract
	// (see TestCloneStateDeepCopiesStartVariables); sharing the reference here
	// would be a footgun for any future caller that assumes clone independence.
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
	// Deep-copy Tasks via HumanTask.Clone, the single deep-copy definition for a
	// task: it independently allocates the candidate actors (including each
	// actor's Roles/Attributes), the eligibility slices, and the Claim/Completion
	// pointees, so mutations to the clone do not affect the original — required
	// for TestStepDoesNotMutateInput to hold.
	// Guard on nil, not on length: a non-nil zero-length source still shares its
	// backing array with the struct copy above, so a later append on either side
	// would write the same slot.
	if st.Tasks != nil {
		s.Tasks = make([]humantask.HumanTask, len(st.Tasks))
		for i, t := range st.Tasks {
			s.Tasks[i] = t.Clone()
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
	// Deep-copy EventTriggeredSubprocesses: eventTriggeredSubprocessArm is a value
	// type (no pointers), so a slice copy is sufficient to ensure mutations to
	// the clone do not affect the original.
	s.EventTriggeredSubprocesses = append([]eventTriggeredSubprocessArm(nil), st.EventTriggeredSubprocesses...)
	// Deep-copy RootCompensations: each CompensationRecord contains an Input
	// map[string]any (a reference type) that must be independently allocated so
	// mutations to a clone's record do not affect the original. nil-vs-empty is
	// preserved (see cloneCompensationRecords).
	s.RootCompensations = cloneCompensationRecords(st.RootCompensations)
	// Deep-copy Scopes: each Scope contains a Compensations slice that must be
	// independently allocated so mutations to a clone's compensation records do
	// not affect the original. The other Scope fields (ID, NodeID, ParentID) are
	// plain strings (value types) and are correctly copied by the struct copy.
	// ScopeSeq is a scalar (int) and is already carried by the struct copy above.
	// Guard on nil, not on length: closeScope rebuilds Scopes with make(..., 0, n),
	// so closing the last scope leaves a non-nil zero-length slice whose backing
	// array would otherwise stay shared with the clone.
	if st.Scopes != nil {
		s.Scopes = make([]Scope, len(st.Scopes))
		for i, sc := range st.Scopes {
			// Deep-copy each CompensationRecord: the Input field is a map[string]any
			// (a reference type) and must be independently allocated so mutations to
			// a clone's Input do not propagate back to the original's record.
			// nil-vs-empty is preserved: a nil Compensations in the source produces
			// nil in the clone; a non-nil empty slice produces a non-nil empty clone.
			sc.Compensations = cloneCompensationRecords(sc.Compensations)
			s.Scopes[i] = sc
		}
	}
	// Deep-copy ArchivedCompensations: each entry in the map holds a
	// []CompensationRecord whose Input fields are map[string]any (reference types)
	// and must be independently allocated so mutations to the clone do not affect
	// the original. nil map in source → nil in clone.
	if st.ArchivedCompensations != nil {
		s.ArchivedCompensations = make(map[string][]CompensationRecord, len(st.ArchivedCompensations))
		for k, recs := range st.ArchivedCompensations {
			s.ArchivedCompensations[k] = cloneCompensationRecords(recs)
		}
	}
	// Deep-copy Incidents: Incident is a flat value struct (all fields are plain
	// scalars — no pointers or maps), so an append-copy of the slice is sufficient
	// to ensure mutations to the clone's Incidents do not affect the original.
	// append([]Incident(nil), ...) already maps nil to nil and allocates fresh for
	// anything else, so no length guard is needed — and a non-nil zero-length
	// source (left by incident resolution) stays unshared.
	s.Incidents = append([]Incident(nil), st.Incidents...)
	// Deep-copy DeferredCompensationThrows: a []string of token IDs (value type),
	// so an append-copy is sufficient to isolate the clone from the original.
	s.DeferredCompensationThrows = append([]string(nil), st.DeferredCompensationThrows...)
	return s
}

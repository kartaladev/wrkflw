package engine

import (
	"errors"
	"fmt"
	"time"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/model"
)

var (
	ErrUnknownTrigger      = errors.New("engine: unknown trigger")
	ErrTokenNotFound       = errors.New("engine: no token awaiting command")
	ErrMicroNotImplemented = errors.New("engine: micro stepping not implemented")
	ErrNoMatchingFlow      = errors.New("engine: no matching outgoing flow")
)

// StepMode selects how far one Step advances. Micro behaves as Macro in this
// plan; true single-node stepping arrives in Plan 5.
type StepMode int

const (
	Macro StepMode = iota
	Micro
)

type StepOptions struct{ Mode StepMode }

type StepResult struct {
	State    InstanceState
	Commands []Command
}

// Step applies one trigger to the instance state and returns the new state plus
// the commands the runtime must perform. It is pure: it does not mutate st.
//
// The engine assumes the definition has passed [model.Validate]; in particular,
// an exclusive gateway is assumed to have at most one unconditional non-default
// outgoing flow — the engine takes the first matching flow in definition order
// and does not detect ambiguous multi-unconditional configurations.
func Step(def *model.ProcessDefinition, st InstanceState, trg Trigger, opt StepOptions) (StepResult, error) {
	if opt.Mode == Micro {
		return StepResult{}, ErrMicroNotImplemented
	}

	s := cloneState(st)

	switch t := trg.(type) {
	case StartInstance:
		s.Status = StatusRunning
		s.StartedAt = t.OccurredAt()
		s.DefID = def.ID
		s.DefVersion = def.Version
		mergeVars(&s, t.Vars)
		starts := def.StartNodes()
		if len(starts) != 1 {
			return StepResult{}, fmt.Errorf("engine: expected exactly one start, got %d", len(starts))
		}
		s.placeToken(starts[0].ID, t.OccurredAt())

	case ActionCompleted:
		tok := s.tokenAwaiting(t.CommandID)
		if tok == nil {
			return StepResult{}, fmt.Errorf("%w: %q", ErrTokenNotFound, t.CommandID)
		}
		mergeVars(&s, t.Output)
		tok.State = TokenActive
		tok.AwaitCommand = ""
		// Advance the token past the completed ServiceTask so drive sees it at
		// the next node, not re-firing the action.
		s.moveAlongSingleFlow(def, tok, t.OccurredAt())

	case ActionFailed:
		tok := s.tokenAwaiting(t.CommandID)
		if tok == nil {
			return StepResult{}, fmt.Errorf("%w: %q", ErrTokenNotFound, t.CommandID)
		}
		s.Status = StatusFailed
		ended := t.OccurredAt()
		s.EndedAt = &ended
		return StepResult{State: s, Commands: []Command{FailInstance{Err: t.Err}}}, nil

	case HumanClaimed:
		task := s.TaskByToken(t.TaskToken)
		if task == nil {
			return StepResult{}, fmt.Errorf("%w: %q", ErrTokenNotFound, t.TaskToken)
		}
		task.ClaimedBy = t.Actor.ID
		task.State = humantask.Claimed
		return StepResult{State: s, Commands: []Command{UpdateTask{Task: *task}}}, nil

	case HumanReassigned:
		task := s.TaskByToken(t.TaskToken)
		if task == nil {
			return StepResult{}, fmt.Errorf("%w: %q", ErrTokenNotFound, t.TaskToken)
		}
		task.ClaimedBy = t.To
		task.State = humantask.Claimed
		return StepResult{State: s, Commands: []Command{UpdateTask{Task: *task}}}, nil

	case TimerFired:
		// Look up the timer record first; this handles both intermediate and SLA timers.
		rec := s.timerByID(t.TimerID)
		if rec != nil {
			switch rec.Kind {
			case TimerSLA:
				return handleSLAFired(def, &s, *rec, t.OccurredAt())
			case TimerInWait:
				return handleReminderFired(def, &s, *rec, t.OccurredAt())
			default:
				// For intermediate timers recorded here, fall through to the
				// tokenAwaiting path which is still valid because
				// intermediate-timer tokens park on the TimerID as their AwaitCommand.
			}
		}

		tok := s.tokenAwaiting(t.TimerID)
		if tok == nil {
			// Stale/already-moved timer (no record and no parked token): clean no-op.
			// Timers are inherently racy with other completion paths (e.g. the
			// instance may have advanced via a different branch), so we never error here.
			return StepResult{State: s, Commands: nil}, nil
		}
		// Intermediate timer: remove its record (if any) so a later dup is a no-op.
		s.removeTimer(t.TimerID)
		tok.State = TokenActive
		tok.AwaitCommand = ""
		s.moveAlongSingleFlow(def, tok, t.OccurredAt())
		driveCmds, err := drive(def, &s, t.OccurredAt())
		if err != nil {
			return StepResult{}, err
		}
		return StepResult{State: s, Commands: driveCmds}, nil

	case HumanCompleted:
		tok := s.tokenAwaiting(t.TaskToken)
		if tok == nil {
			return StepResult{}, fmt.Errorf("%w: %q", ErrTokenNotFound, t.TaskToken)
		}
		// Fail-fast: a parked token without a matching HumanTask record is an
		// invariant violation (token and task are always created together in
		// KindUserTask). Advancing silently would corrupt state without emitting
		// UpdateTask, so we reject the trigger with a descriptive error.
		task := s.TaskByToken(t.TaskToken)
		if task == nil {
			return StepResult{}, fmt.Errorf("engine: human-completed for token %q has no task record: %w", t.TaskToken, humantask.ErrTaskNotFound)
		}
		mergeVars(&s, t.Output)
		s.setVisitActor(tok.ID, tok.NodeID, t.Actor.ID)
		task.State = humantask.Completed
		tok.State = TokenActive
		tok.AwaitCommand = ""
		s.moveAlongSingleFlow(def, tok, t.OccurredAt())
		cmds := []Command{UpdateTask{Task: *task}}
		// Cancel any SLA or reminder timers that were guarding this task.
		for _, timerID := range s.cancelTimersByTaskToken(t.TaskToken, "") {
			cmds = append(cmds, CancelTimer{TimerID: timerID})
		}
		driveCmds, err := drive(def, &s, t.OccurredAt())
		if err != nil {
			return StepResult{}, err
		}
		cmds = append(cmds, driveCmds...)
		return StepResult{State: s, Commands: cmds}, nil

	default:
		return StepResult{}, fmt.Errorf("%w: %T", ErrUnknownTrigger, trg)
	}

	cmds, err := drive(def, &s, trg.OccurredAt())
	if err != nil {
		return StepResult{}, err
	}
	return StepResult{State: s, Commands: cmds}, nil
}

// drive advances all active tokens until each is parked or consumed (Macro).
func drive(def *model.ProcessDefinition, s *InstanceState, at time.Time) ([]Command, error) {
	var cmds []Command
	for {
		tok := s.firstActive()
		if tok == nil {
			break
		}
		node, ok := def.Node(tok.NodeID)
		if !ok {
			// Defensive: a token on a missing node cannot advance.
			tok.State = TokenWaitingCommand
			continue
		}

		switch node.Kind {
		case model.KindStartEvent:
			s.moveAlongSingleFlow(def, tok, at)

		case model.KindServiceTask:
			cmdID := s.nextCommandID()
			cmds = append(cmds, InvokeAction{
				CommandID: cmdID,
				Name:      node.Action,
				Input:     copyVars(s.Variables),
			})
			tok.State = TokenWaitingCommand
			tok.AwaitCommand = cmdID

		case model.KindUserTask:
			taskToken := s.nextTaskToken()
			spec := authz.AuthzSpec{
				Roles:     node.CandidateRoles,
				Attribute: node.EligibilityExpr,
			}
			ht := humantask.HumanTask{
				TaskToken:   taskToken,
				InstanceID:  s.InstanceID,
				NodeID:      node.ID,
				Eligibility: spec,
				State:       humantask.Unclaimed,
				CreatedAt:   at,
			}
			// If the node carries an SLA, schedule the SLA timer and record the
			// deadline on the HumanTask so callers can surface the due date.
			if node.SLADuration != "" {
				dur, err := conditions.EvalDuration(node.SLADuration, s.Variables)
				if err != nil {
					return cmds, fmt.Errorf("engine: SLA node %q: %w", node.ID, err)
				}
				fireAt := at.Add(dur)
				slaTimerID := s.nextTimerID()
				cmds = append(cmds, ScheduleTimer{
					TimerID: slaTimerID,
					Token:   tok.ID,
					FireAt:  fireAt,
					Kind:    TimerSLA,
				})
				s.Timers = append(s.Timers, timerRecord{
					TimerID:   slaTimerID,
					Kind:      TimerSLA,
					Token:     tok.ID,
					TaskToken: taskToken,
					NodeID:    node.ID,
				})
				ht.DueAt = &fireAt
			}
			// If the node carries a reminder interval, schedule the first in-wait
			// timer. Subsequent reminders are re-scheduled each time the timer fires
			// (see handleReminderFired), so a single ScheduleTimer is enough here.
			if node.ReminderEvery != "" {
				dur, err := conditions.EvalDuration(node.ReminderEvery, s.Variables)
				if err != nil {
					return cmds, fmt.Errorf("engine: reminder node %q: %w", node.ID, err)
				}
				reminderTimerID := s.nextTimerID()
				cmds = append(cmds, ScheduleTimer{
					TimerID: reminderTimerID,
					Token:   tok.ID,
					FireAt:  at.Add(dur),
					Kind:    TimerInWait,
				})
				s.Timers = append(s.Timers, timerRecord{
					TimerID:   reminderTimerID,
					Kind:      TimerInWait,
					Token:     tok.ID,
					TaskToken: taskToken,
					NodeID:    node.ID,
				})
			}
			s.Tasks = append(s.Tasks, ht)
			cmds = append(cmds, AwaitHuman{TaskToken: taskToken, Eligibility: spec})
			tok.State = TokenWaitingCommand
			tok.AwaitCommand = taskToken

		case model.KindIntermediateCatchEvent:
			if node.TimerDuration != "" {
				dur, err := conditions.EvalDuration(node.TimerDuration, s.Variables)
				if err != nil {
					return cmds, fmt.Errorf("engine: timer node %q: %w", node.ID, err)
				}
				timerID := s.nextTimerID()
				cmds = append(cmds, ScheduleTimer{
					TimerID: timerID,
					Token:   tok.ID,
					FireAt:  at.Add(dur),
					Kind:    TimerIntermediate,
				})
				tok.State = TokenWaitingCommand
				tok.AwaitCommand = timerID
			} else {
				// Non-timer intermediate catch event: park the token.
				// Other event variants (message, signal, etc.) arrive in later plans.
				tok.State = TokenWaitingCommand
			}

		case model.KindEndEvent:
			s.consumeToken(tok, at)
			if len(s.Tokens) == 0 {
				s.Status = StatusCompleted
				ended := at
				s.EndedAt = &ended
				cmds = append(cmds, CompleteInstance{Result: copyVars(s.Variables)})
			}

		case model.KindExclusiveGateway:
			target, err := selectExclusiveTarget(def, s, node)
			if err != nil {
				// cmds is carried here for a future error-handling plan (Plan 8);
				// Step currently discards StepResult on error, so partial commands
				// are intentionally not delivered today.
				return cmds, err
			}
			s.moveTokenToTarget(tok, target, at)

		case model.KindParallelGateway:
			if len(def.Incoming(node.ID)) > 1 {
				s.tryParallelJoin(def, tok, node, at)
			} else {
				s.forkParallel(def, tok, node, at)
			}

		case model.KindInclusiveGateway:
			if len(def.Incoming(node.ID)) > 1 {
				s.tryInclusiveJoin(def, tok, node, at)
			} else {
				if err := s.forkInclusive(def, tok, node, at); err != nil {
					return cmds, err
				}
			}

		default:
			// Node kinds beyond linear flow arrive in later plans; park the
			// token so the loop terminates rather than spinning.
			tok.State = TokenWaitingCommand
		}
	}
	return cmds, nil
}

// ---- InstanceState helpers (unexported) ----

func (s *InstanceState) placeToken(nodeID string, at time.Time) {
	s.TokenSeq++
	id := fmt.Sprintf("%s-t%d", s.InstanceID, s.TokenSeq)
	s.Tokens = append(s.Tokens, Token{ID: id, NodeID: nodeID, State: TokenActive, EnteredAt: at})
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

// forkParallel consumes the incoming token and creates one Active token at each
// outgoing flow target (definition order). Used for a diverging parallel gateway.
func (s *InstanceState) forkParallel(def *model.ProcessDefinition, tok *Token, node model.Node, at time.Time) {
	outs := def.Outgoing(node.ID)
	s.consumeToken(tok, at)
	for _, f := range outs {
		s.placeToken(f.Target, at)
	}
}

// forkInclusive consumes the incoming token and creates an Active token for every
// non-default outgoing flow whose condition is empty or true (definition order).
// If none are true it takes the default flow; if none are true and there is no
// default it returns ErrNoMatchingFlow.
func (s *InstanceState) forkInclusive(def *model.ProcessDefinition, tok *Token, node model.Node, at time.Time) error {
	var taken []model.SequenceFlow
	var dflt *model.SequenceFlow
	for _, f := range def.Outgoing(node.ID) {
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
			return fmt.Errorf("engine: gateway %q flow %q: %w", node.ID, f.ID, err)
		}
		if ok {
			taken = append(taken, f)
		}
	}
	if len(taken) == 0 {
		if dflt == nil {
			return fmt.Errorf("%w: gateway %q", ErrNoMatchingFlow, node.ID)
		}
		taken = append(taken, *dflt)
	}
	s.consumeToken(tok, at)
	for _, f := range taken {
		s.placeToken(f.Target, at)
	}
	return nil
}

// tryParallelJoin parks the arriving token at a converging parallel gateway and,
// once a token has arrived on every incoming flow, consumes them all and forks to
// the gateway's outgoing flows. Until then the token waits as TokenAtJoin.
func (s *InstanceState) tryParallelJoin(def *model.ProcessDefinition, tok *Token, node model.Node, at time.Time) {
	tok.State = TokenAtJoin

	arrived := 0
	for i := range s.Tokens {
		if s.Tokens[i].NodeID == node.ID && s.Tokens[i].State == TokenAtJoin {
			arrived++
		}
	}
	// INVARIANT: single non-nested, acyclic diamond only — counting TokenAtJoin
	// on the node vs incoming-flow count over-counts under nested/re-entered joins
	// (deferred to a later scopes/loops plan).
	if arrived < len(def.Incoming(node.ID)) {
		return // still waiting on other branches
	}

	// Fire: remove all tokens parked at this join (closing their visits), then
	// create one Active token per outgoing flow.
	kept := make([]Token, 0, len(s.Tokens))
	for _, t := range s.Tokens {
		if t.NodeID == node.ID && t.State == TokenAtJoin {
			s.closeVisit(t.ID, t.NodeID, at)
			continue
		}
		kept = append(kept, t)
	}
	s.Tokens = kept
	for _, f := range def.Outgoing(node.ID) {
		s.placeToken(f.Target, at)
	}
}

// tryInclusiveJoin parks the arriving token at an OR-join and fires only once no
// token other than those already parked at the join can still reach it (so it
// never waits for branches that were never activated). On firing it consumes all
// tokens parked at the join and creates one Active token per outgoing flow.
func (s *InstanceState) tryInclusiveJoin(def *model.ProcessDefinition, tok *Token, node model.Node, at time.Time) {
	tok.State = TokenAtJoin

	canReach := nodesThatCanReach(def, node.ID)
	// INVARIANT: single non-nested, acyclic diamond only — the reachability check
	// (nodesThatCanReach) assumes no nested/re-entered joins or loops back toward
	// this join (deferred to a later scopes/loops plan).
	for i := range s.Tokens {
		t := &s.Tokens[i]
		if t.NodeID == node.ID && t.State == TokenAtJoin {
			continue // already arrived at the join
		}
		if canReach[t.NodeID] {
			return // some token can still reach the join; keep waiting
		}
	}

	// Fire: consume all tokens parked at this join, then fork to outgoing flows.
	kept := make([]Token, 0, len(s.Tokens))
	for _, t := range s.Tokens {
		if t.NodeID == node.ID && t.State == TokenAtJoin {
			s.closeVisit(t.ID, t.NodeID, at)
			continue
		}
		kept = append(kept, t)
	}
	s.Tokens = kept
	for _, f := range def.Outgoing(node.ID) {
		s.placeToken(f.Target, at)
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
	for _, f := range def.Outgoing(node.ID) {
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
			return "", fmt.Errorf("engine: gateway %q flow %q: %w", node.ID, f.ID, err)
		}
		if ok {
			return f.Target, nil
		}
	}
	if defaultFlow != nil {
		return defaultFlow.Target, nil
	}
	return "", fmt.Errorf("%w: gateway %q", ErrNoMatchingFlow, node.ID)
}

// handleSLAFired processes a TimerFired event for an SLA timer. It is called
// from the TimerFired handler in Step when the timer record's Kind is TimerSLA.
//
// Contract:
//   - If the guarded task is already completed (or the parked token is gone),
//     it is a clean no-op: no commands, no error.
//   - If the task is still in progress, it performs the SLA breach:
//     (a) emits InvokeAction for node.SLAAction (if set),
//     (b) moves the token to the target of node.SLAFlow (alternative path),
//     (c) marks the task Cancelled and emits UpdateTask,
//     (d) cancels any other timers (e.g. reminders) for the same task,
//     (e) removes the SLA timer record and drives forward.
func handleSLAFired(def *model.ProcessDefinition, s *InstanceState, rec timerRecord, at time.Time) (StepResult, error) {
	// Find the parked token. If the token is gone (task completed, instance
	// advanced), the SLA fired late → clean no-op.
	tok := s.tokenAwaiting(rec.TaskToken)
	if tok == nil {
		// Also clean up the stale timer record.
		s.removeTimer(rec.TimerID)
		return StepResult{State: *s, Commands: nil}, nil
	}

	// If the task has already been completed or cancelled, treat as no-op.
	// Fix C: treat both Completed and Cancelled as "already resolved" so a
	// duplicate SLA fire after cancellation is also a clean no-op.
	task := s.TaskByToken(rec.TaskToken)
	if task != nil && task.State != humantask.Unclaimed && task.State != humantask.Claimed {
		s.removeTimer(rec.TimerID)
		return StepResult{State: *s, Commands: nil}, nil
	}

	// Resolve the SLA alternative-path flow.
	node, ok := def.Node(rec.NodeID)
	if !ok {
		return StepResult{}, fmt.Errorf("engine: SLA breach: node %q not found in definition", rec.NodeID)
	}
	if node.SLAFlow == "" {
		return StepResult{}, fmt.Errorf("engine: SLA breach: node %q has no SLAFlow defined", rec.NodeID)
	}
	// Find the sequence flow with ID == node.SLAFlow.
	var slaTarget string
	for _, f := range def.Flows {
		if f.ID == node.SLAFlow {
			slaTarget = f.Target
			break
		}
	}
	if slaTarget == "" {
		return StepResult{}, fmt.Errorf("engine: SLA breach: SLAFlow %q not found in definition flows for node %q", node.SLAFlow, rec.NodeID)
	}

	var cmds []Command

	// (a) Emit the SLA alternative action, if configured.
	if node.SLAAction != "" {
		cmdID := s.nextCommandID()
		cmds = append(cmds, InvokeAction{
			CommandID: cmdID,
			Name:      node.SLAAction,
			Input:     copyVars(s.Variables),
		})
	}

	// (b) Move the token to the alternative path target. The token was parked
	//     (TokenWaitingCommand / AwaitCommand == TaskToken); reactivate it and
	//     route to the SLA path.
	// Fix A: explicitly set TokenActive before moveTokenToTarget for symmetry
	// with HumanCompleted and as a defensive measure (moveTokenToTarget also
	// sets it, but being explicit here makes the intent unambiguous).
	tok.AwaitCommand = ""
	tok.State = TokenActive
	s.moveTokenToTarget(tok, slaTarget, at)

	// (c) Mark the task Cancelled and emit UpdateTask.
	if task != nil {
		task.State = humantask.Cancelled
		cmds = append(cmds, UpdateTask{Task: *task})
	}

	// (d) Cancel any other timers (e.g. reminder timers) for this task.
	for _, reminderID := range s.cancelTimersByTaskToken(rec.TaskToken, rec.TimerID) {
		cmds = append(cmds, CancelTimer{TimerID: reminderID})
	}

	// Remove the SLA timer record — it has been consumed.
	s.removeTimer(rec.TimerID)

	// (e) Drive forward from the alternative path.
	driveCmds, err := drive(def, s, at)
	if err != nil {
		return StepResult{}, err
	}
	cmds = append(cmds, driveCmds...)

	return StepResult{State: *s, Commands: cmds}, nil
}

// handleReminderFired processes a TimerFired event for a TimerInWait reminder
// timer. It is called from the TimerFired handler in Step.
//
// Contract:
//   - If the guarded task is no longer open (token gone, task Completed or
//     Cancelled), the reminder is stale: clean no-op, stale record removed.
//   - If the task is still open:
//     (1) emits InvokeAction(node.ReminderAction) if non-empty (fire-and-forget),
//     (2) removes the fired reminder record and schedules the next reminder at
//         firedAt + every (new timer id from the counter), recording the new
//         timerRecord; the token does NOT move.
func handleReminderFired(def *model.ProcessDefinition, s *InstanceState, rec timerRecord, firedAt time.Time) (StepResult, error) {
	// If the parked token is gone (task completed/cancelled and advanced), the
	// reminder fired late → clean no-op, remove the stale record.
	tok := s.tokenAwaiting(rec.TaskToken)
	if tok == nil {
		s.removeTimer(rec.TimerID)
		return StepResult{State: *s, Commands: nil}, nil
	}

	// If the task is already resolved (Completed or Cancelled), stale no-op.
	task := s.TaskByToken(rec.TaskToken)
	if task != nil && task.State != humantask.Unclaimed && task.State != humantask.Claimed {
		s.removeTimer(rec.TimerID)
		return StepResult{State: *s, Commands: nil}, nil
	}

	// Resolve the node to get ReminderEvery and ReminderAction.
	node, ok := def.Node(rec.NodeID)
	if !ok {
		return StepResult{}, fmt.Errorf("engine: reminder fired: node %q not found in definition", rec.NodeID)
	}

	var cmds []Command

	// (1) Fire-and-forget reminder action, if configured.
	if node.ReminderAction != "" {
		cmdID := s.nextCommandID()
		cmds = append(cmds, InvokeAction{
			CommandID: cmdID,
			Name:      node.ReminderAction,
			Input:     copyVars(s.Variables),
		})
	}

	// (2) Replace the fired reminder record with a new one for the next interval.
	// Remove the old record first so the timer table stays consistent.
	s.removeTimer(rec.TimerID)

	// Re-evaluate the duration from the expression (node variables may differ,
	// but correctness requires the same expression path as initial scheduling).
	dur, err := conditions.EvalDuration(node.ReminderEvery, s.Variables)
	if err != nil {
		return StepResult{}, fmt.Errorf("engine: reminder node %q re-schedule: %w", node.ID, err)
	}
	newTimerID := s.nextTimerID()
	cmds = append(cmds, ScheduleTimer{
		TimerID: newTimerID,
		Token:   rec.Token,
		FireAt:  firedAt.Add(dur),
		Kind:    TimerInWait,
	})
	s.Timers = append(s.Timers, timerRecord{
		TimerID:   newTimerID,
		Kind:      TimerInWait,
		Token:     rec.Token,
		TaskToken: rec.TaskToken,
		NodeID:    rec.NodeID,
	})

	// The token does NOT move — the task is still pending.
	return StepResult{State: *s, Commands: cmds}, nil
}

// ---- value helpers ----

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

func cloneState(st InstanceState) InstanceState {
	s := st
	s.Variables = copyVars(st.Variables)
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
	return s
}

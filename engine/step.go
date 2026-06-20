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

// StepResult is the output of a single [Step] call. Commands is the ordered
// list of side effects the runtime must perform. On a no-op step (e.g. a stale
// TimerFired with no matching token) Commands may be nil; callers should use
// len(Commands) rather than Commands != nil to check for work to do.
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
		// Cancel any boundary arms on this host token before advancing.
		var preCmds []Command
		for _, timerID := range s.removeBoundaryArmsForHost(tok.ID) {
			preCmds = append(preCmds, CancelTimer{TimerID: timerID})
		}
		mergeVars(&s, t.Output)
		tok.State = TokenActive
		tok.AwaitCommand = ""
		// Advance the token past the completed ServiceTask so drive sees it at
		// the next node, not re-firing the action.
		s.moveAlongSingleFlow(def, tok, t.OccurredAt())
		driveCmds, err := drive(def, &s, t.OccurredAt())
		if err != nil {
			return StepResult{}, err
		}
		return StepResult{State: s, Commands: append(preCmds, driveCmds...)}, nil

	case ActionFailed:
		tok := s.tokenAwaiting(t.CommandID)
		if tok == nil {
			return StepResult{}, fmt.Errorf("%w: %q", ErrTokenNotFound, t.CommandID)
		}
		s.Status = StatusFailed
		ended := t.OccurredAt()
		s.EndedAt = &ended
		cmds := []Command{FailInstance{Err: t.Err}}
		cmds = append(cmds, s.cancelAllTimers()...)
		return StepResult{State: s, Commands: cmds}, nil

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
		// Dispatch order:
		// 1) event-based gateway arm (first-event-wins routing).
		// 2) boundary event arm (interrupting/non-interrupting).
		// 3) SLA/in-wait timer record (task-guarded timers).
		// 4) standalone intermediate catch event (token parks on TimerID).

		// 1) Gateway arm check.
		if ae := s.armedEventByTimer(t.TimerID); ae != nil {
			gwCmds, err := resolveGatewayWin(def, &s, *ae, t.OccurredAt())
			if err != nil {
				return StepResult{}, err
			}
			return StepResult{State: s, Commands: gwCmds}, nil
		}

		// 2) Boundary arm check.
		if ba := s.boundaryArmByTimer(t.TimerID); ba != nil {
			baCmds, err := fireBoundaryArm(def, &s, *ba, t.OccurredAt())
			if err != nil {
				return StepResult{}, err
			}
			return StepResult{State: s, Commands: baCmds}, nil
		}

		// 3) SLA/in-wait timer record.
		// s.Timers holds only SLA (TimerSLA) and in-wait/reminder (TimerInWait)
		// records. Intermediate timers (TimerIntermediate) are never appended to
		// s.Timers; for those, the token parks on the TimerID as its AwaitCommand,
		// so they route via the tokenAwaiting path below.
		rec := s.timerByID(t.TimerID)
		if rec != nil {
			switch rec.Kind {
			case TimerSLA:
				return handleSLAFired(def, &s, *rec, t.OccurredAt())
			case TimerInWait:
				return handleReminderFired(def, &s, *rec, t.OccurredAt())
			}
		}

		// 4) Standalone intermediate timer.
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
		// Cancel any boundary arms on this host token (token ID is the same as the
		// HostToken recorded at arm time; at this point tok.ID is still valid since
		// moveAlongSingleFlow keeps the same token — it just changes NodeID).
		// We find the original token ID via the task token's parked token:
		// tok.ID is already the token that was parked (we looked it up via
		// tokenAwaiting(t.TaskToken) above, and moveAlongSingleFlow does not change
		// the token ID, only its NodeID). So tok.ID is the correct HostToken.
		for _, timerID := range s.removeBoundaryArmsForHost(tok.ID) {
			cmds = append(cmds, CancelTimer{TimerID: timerID})
		}
		driveCmds, err := drive(def, &s, t.OccurredAt())
		if err != nil {
			return StepResult{}, err
		}
		cmds = append(cmds, driveCmds...)
		return StepResult{State: s, Commands: cmds}, nil

	case SignalReceived:
		// Broadcast semantics within the instance: resume every token that is
		// awaiting this signal name. Tokens are processed in slice order for
		// determinism. A signal that matches no token (and no gateway arm, and no
		// boundary arm) is a clean no-op — mergeVars runs ONLY when at least one
		// match is found.
		//
		// NOTE: mergeVars is deferred until after match-checking so that a no-match
		// delivery does not mutate instance variables (Task-2 review fix).
		//
		// Dispatch order for signal:
		// 1) event-based gateway arm (first-event-wins).
		// 2) boundary event arm (interrupting/non-interrupting).
		// 3) standalone parked-signal tokens (broadcast).
		//
		// INTENTIONAL BROADCAST: A single signal name may simultaneously resolve a
		// gateway arm (step 1), fire a boundary arm (step 2), AND resume one or more
		// standalone parked-signal tokens (step 3) within the same Step call. All
		// three dispatch points are evaluated in order; matching is not mutually
		// exclusive across them. Using the same signal name on competing constructs
		// (e.g. an event-gateway arm and a standalone catch) is permitted by the
		// engine but is the definition author's responsibility — a future model.Validate
		// rule could warn about ambiguous multi-construct signal names.
		var signalCmds []Command
		matched := false

		// 1) Check whether the signal matches an event-gateway arm.
		if ae := s.armedEventBySignal(t.Name); ae != nil {
			if !matched {
				mergeVars(&s, t.Payload)
				matched = true
			}
			gwCmds, err := resolveGatewayWin(def, &s, *ae, t.OccurredAt())
			if err != nil {
				return StepResult{}, err
			}
			signalCmds = append(signalCmds, gwCmds...)
		}

		// 2) Check whether the signal matches a boundary arm.
		if ba := s.boundaryArmBySignal(t.Name); ba != nil {
			if !matched {
				mergeVars(&s, t.Payload)
				matched = true
			}
			baCmds, err := fireBoundaryArm(def, &s, *ba, t.OccurredAt())
			if err != nil {
				return StepResult{}, err
			}
			signalCmds = append(signalCmds, baCmds...)
		}

		// 3) Resume all standalone parked-signal tokens (broadcast).
		for {
			tok := s.tokenAwaitingSignal(t.Name)
			if tok == nil {
				break
			}
			if !matched {
				mergeVars(&s, t.Payload)
				matched = true
			}
			tok.AwaitSignal = ""
			tok.State = TokenActive
			s.moveAlongSingleFlow(def, tok, t.OccurredAt())
			driveCmds, err := drive(def, &s, t.OccurredAt())
			if err != nil {
				return StepResult{}, err
			}
			signalCmds = append(signalCmds, driveCmds...)
		}
		return StepResult{State: s, Commands: signalCmds}, nil

	case MessageReceived:
		// Point-to-point semantics: resume the single token whose AwaitMessage
		// matches the name AND whose AwaitMessageKey matches the correlation key.
		// A message that matches no token (and no gateway arm) is a clean no-op.
		//
		// NOTE: mergeVars is deferred until after match-checking so that a no-match
		// delivery does not mutate instance variables (Task-2 review fix).

		// Check whether the message matches an event-gateway arm (first-event-wins).
		if ae := s.armedEventByMessage(t.Name, t.CorrelationKey); ae != nil {
			mergeVars(&s, t.Payload)
			gwCmds, err := resolveGatewayWin(def, &s, *ae, t.OccurredAt())
			if err != nil {
				return StepResult{}, err
			}
			return StepResult{State: s, Commands: gwCmds}, nil
		}

		tok := s.tokenAwaitingMessage(t.Name, t.CorrelationKey)
		if tok == nil {
			// No matching token: clean no-op (message may be for a different instance
			// or arrived after the instance advanced).
			return StepResult{State: s, Commands: nil}, nil
		}
		mergeVars(&s, t.Payload)
		tok.AwaitMessage = ""
		tok.AwaitMessageKey = ""
		tok.State = TokenActive
		s.moveAlongSingleFlow(def, tok, t.OccurredAt())
		driveCmds, err := drive(def, &s, t.OccurredAt())
		if err != nil {
			return StepResult{}, err
		}
		return StepResult{State: s, Commands: driveCmds}, nil

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
			// Arm any boundary events attached to this host activity.
			bndCmds, err := armBoundaries(def, s, tok.ID, node.ID, at)
			if err != nil {
				return cmds, err
			}
			cmds = append(cmds, bndCmds...)

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
			// Arm any boundary events attached to this host activity.
			bndCmds, err := armBoundaries(def, s, tok.ID, node.ID, at)
			if err != nil {
				return cmds, err
			}
			cmds = append(cmds, bndCmds...)

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
			} else if node.SignalName != "" {
				// Signal intermediate catch event: park the token awaiting the signal.
				// The SignalReceived trigger (broadcast) will resume it later.
				tok.State = TokenWaitingCommand
				tok.AwaitSignal = node.SignalName
			} else if node.MessageName != "" {
				// Message intermediate catch event: park the token awaiting the message.
				// Evaluate the correlation key (if set) now against instance variables
				// for determinism; store the resolved key on the token.
				resolvedKey, err := conditions.EvalString(node.CorrelationKey, s.Variables)
				if err != nil {
					return cmds, fmt.Errorf("engine: message node %q correlation key: %w", node.ID, err)
				}
				tok.State = TokenWaitingCommand
				tok.AwaitMessage = node.MessageName
				tok.AwaitMessageKey = resolvedKey
			} else {
				// Non-timer, non-signal, non-message intermediate catch event: park.
				// Further event variants arrive in later plans.
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

		case model.KindEventBasedGateway:
			// Event-based gateway: arm all outgoing catch-event branches simultaneously.
			// The gateway token is parked; the first armed event to fire wins and
			// routes the token to that arm's branch, cancelling sibling arms.
			//
			// Routing: for each outgoing flow (definition order) we look at the target
			// catch-event node and create an armedEvent record. Timer arms also emit a
			// ScheduleTimer. Signal and message arms are recorded only (delivery happens
			// via SignalReceived/MessageReceived triggers later).
			//
			// The gateway token is parked with a sentinel AwaitCommand set to
			// "evtgw:<tokenID>" so firstActive() skips it, while still being
			// identifiable as a gateway-parked token. The ArmedEvents slice is the
			// primary correlation table — the gateway token is found via armedEvent.GatewayToken.
			sentinel := "evtgw:" + tok.ID
			tok.State = TokenWaitingCommand
			tok.AwaitCommand = sentinel
			for _, f := range def.Outgoing(node.ID) {
				catchNode, ok := def.Node(f.Target)
				if !ok {
					continue
				}
				ae := armedEvent{
					GatewayToken: tok.ID,
					CatchNode:    catchNode.ID,
					Flow:         f.ID,
				}
				if catchNode.TimerDuration != "" {
					dur, err := conditions.EvalDuration(catchNode.TimerDuration, s.Variables)
					if err != nil {
						return cmds, fmt.Errorf("engine: event-gateway %q timer arm %q: %w", node.ID, catchNode.ID, err)
					}
					timerID := s.nextTimerID()
					cmds = append(cmds, ScheduleTimer{
						TimerID: timerID,
						Token:   tok.ID,
						FireAt:  at.Add(dur),
						Kind:    TimerIntermediate,
					})
					ae.TimerID = timerID
				} else if catchNode.SignalName != "" {
					ae.Signal = catchNode.SignalName
				} else if catchNode.MessageName != "" {
					resolvedKey, err := conditions.EvalString(catchNode.CorrelationKey, s.Variables)
					if err != nil {
						return cmds, fmt.Errorf("engine: event-gateway %q message arm %q correlation key: %w", node.ID, catchNode.ID, err)
					}
					ae.Message = catchNode.MessageName
					ae.MessageKey = resolvedKey
				}
				s.ArmedEvents = append(s.ArmedEvents, ae)
			}

		case model.KindIntermediateThrowEvent:
			if node.SignalName != "" {
				// Signal intermediate throw: emit ThrowSignal and continue along the
				// single outgoing flow. The runtime broadcasts the signal; the engine
				// does not wait for delivery (fire-and-forget from the engine's view).
				cmds = append(cmds, ThrowSignal{
					Name:    node.SignalName,
					Payload: nil, // no per-instance payload from throw nodes in this plan
				})
				s.moveAlongSingleFlow(def, tok, at)
			} else {
				// Non-signal intermediate throw: park for future plans (e.g. message
				// throw, error throw). Parking avoids an infinite drive loop.
				tok.State = TokenWaitingCommand
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

// tokenByID returns the first token whose ID matches, or nil.
func (s *InstanceState) tokenByID(tokenID string) *Token {
	for i := range s.Tokens {
		if s.Tokens[i].ID == tokenID {
			return &s.Tokens[i]
		}
	}
	return nil
}

// tokenAwaitingSignal returns the first token whose AwaitSignal matches name
// (tokens are stored in TokenSeq order, so iteration is deterministic).
func (s *InstanceState) tokenAwaitingSignal(name string) *Token {
	for i := range s.Tokens {
		if s.Tokens[i].AwaitSignal == name {
			return &s.Tokens[i]
		}
	}
	return nil
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
func resolveGatewayWin(def *model.ProcessDefinition, s *InstanceState, ae armedEvent, at time.Time) ([]Command, error) {
	// Find the gateway token.
	tok := s.tokenAwaiting("evtgw:" + ae.GatewayToken)
	if tok == nil {
		// Gateway token is gone (already resolved by another concurrent path).
		// This is a late/duplicate trigger: clean no-op.
		// Remove any stale armed events for this gateway.
		s.removeArmedEventsForGateway(ae.GatewayToken)
		return nil, nil
	}

	// Find the catch node's outgoing target so we can skip directly to the branch.
	// The catch node has "fired" by the arriving event; we route the gateway token
	// straight to the catch node's outgoing target (its downstream node).
	catchOuts := def.Outgoing(ae.CatchNode)
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
		s.moveAlongSingleFlow(def, tok, at)
	}

	// Remove ALL armedEvent entries for this gateway (winning + sibling arms).
	// removeArmedEventsForGateway returns timer IDs that need CancelTimer commands.
	// The winning arm's TimerID is also returned if it is a timer arm; since it
	// already fired, the runtime will simply receive a redundant cancel — which is
	// safe. To avoid the redundant cancel, we exclude the winning timer from cancellation.
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
	driveCmds, err := drive(def, s, at)
	if err != nil {
		return nil, err
	}
	cmds = append(cmds, driveCmds...)
	return cmds, nil
}

// armBoundaries finds all KindBoundaryEvent nodes with AttachedTo == hostNode,
// records a boundaryArm for each, and returns ScheduleTimer commands for timer
// boundaries. Called from the ServiceTask and UserTask park points in drive.
//
// Definition-scan order is deterministic (Nodes slice order); boundary arms are
// appended in the same order so s.Boundaries is deterministic.
//
// A bad TimerDuration expression is returned as a wrapped error — consistent with
// the intermediate-timer and SLA paths — so callers can fail fast rather than
// silently no-arming the boundary.
func armBoundaries(def *model.ProcessDefinition, s *InstanceState, hostTokenID, hostNode string, at time.Time) ([]Command, error) {
	var cmds []Command
	for _, n := range def.Nodes {
		if n.Kind != model.KindBoundaryEvent || n.AttachedTo != hostNode {
			continue
		}
		// Find the boundary's single outgoing flow.
		outs := def.Outgoing(n.ID)
		if len(outs) == 0 {
			continue // unreachable if model.Validate passes
		}
		flowID := outs[0].ID

		arm := boundaryArm{
			HostToken:       hostTokenID,
			HostNode:        hostNode,
			BoundaryNode:    n.ID,
			Flow:            flowID,
			NonInterrupting: n.NonInterrupting,
		}

		if n.TimerDuration != "" {
			dur, err := conditions.EvalDuration(n.TimerDuration, s.Variables)
			if err != nil {
				return nil, fmt.Errorf("engine: boundary %q on %q: %w", n.ID, hostNode, err)
			}
			timerID := s.nextTimerID()
			arm.TimerID = timerID
			cmds = append(cmds, ScheduleTimer{
				TimerID: timerID,
				Token:   hostTokenID,
				FireAt:  at.Add(dur),
				Kind:    TimerIntermediate,
			})
		} else if n.SignalName != "" {
			arm.Signal = n.SignalName
		}
		s.Boundaries = append(s.Boundaries, arm)
	}
	return cmds, nil
}

// fireBoundaryArm executes a boundary event arm that has fired. It is called
// from the TimerFired and SignalReceived handlers.
//
// For interrupting boundaries (!ba.NonInterrupting):
//  1. Verify the host token is still parked. If not, it's a late/stale fire → no-op.
//  2. Cancel any SLA/reminder timers on the host (UserTask) via cancelTimersByTaskToken
//     using the host's taskToken (AwaitCommand == taskToken for UserTask hosts).
//  3. Consume the host token (close its visit, remove from slice).
//  4. Remove ALL boundary arms for this host (emit CancelTimer for timer siblings).
//  5. Place a new Active token at the boundary's outgoing flow target.
//  6. Drive forward.
//
// For non-interrupting boundaries (ba.NonInterrupting):
//  1. Verify the host token is still parked. If not, no-op.
//  2. Leave the host parked.
//  3. Remove ONLY this boundary arm (fired once; do not re-arm — repeating out of scope).
//  4. Place an additional Active token at the boundary's outgoing flow target.
//  5. Drive forward (the new token).
func fireBoundaryArm(def *model.ProcessDefinition, s *InstanceState, ba boundaryArm, at time.Time) ([]Command, error) {
	// Find the host token by ID (not by AwaitCommand — the host token parks on
	// taskToken/cmdID, not on the boundary timer). If the token is gone (already
	// consumed by another path), this is a late fire — clean no-op.
	hostTok := s.tokenByID(ba.HostToken)
	if hostTok == nil {
		// Also clean up stale boundary arms for this host (defensive).
		s.removeBoundaryArmsForHost(ba.HostToken)
		return nil, nil
	}

	// Resolve the boundary's outgoing flow target.
	var flowTarget string
	for _, f := range def.Flows {
		if f.ID == ba.Flow {
			flowTarget = f.Target
			break
		}
	}
	if flowTarget == "" {
		// No target: unreachable if model.Validate passes (boundary must have outgoing flow).
		return nil, fmt.Errorf("engine: boundary %q: outgoing flow %q not found", ba.BoundaryNode, ba.Flow)
	}

	var cmds []Command

	if !ba.NonInterrupting {
		// Interrupting: consume the host, cancel its task timers and boundary siblings.

		// Cancel SLA/reminder timers for the host (UserTask case: AwaitCommand == taskToken).
		// For a ServiceTask host, AwaitCommand is a cmdID (not a taskToken), so
		// cancelTimersByTaskToken will find no records — which is correct.
		hostTaskToken := hostTok.AwaitCommand
		for _, timerID := range s.cancelTimersByTaskToken(hostTaskToken, "") {
			cmds = append(cmds, CancelTimer{TimerID: timerID})
		}

		// Consume the host token (close its visit, remove from slice).
		s.consumeToken(hostTok, at)

		// Remove ALL boundary arms for this host and emit CancelTimer for timer siblings.
		// The fired arm's timerID (if any) is included; it already fired so the
		// runtime's cancel is idempotent — no special handling needed.
		for _, timerID := range s.removeBoundaryArmsForHost(ba.HostToken) {
			cmds = append(cmds, CancelTimer{TimerID: timerID})
		}

		// Place a new Active token at the boundary's outgoing flow target.
		s.placeToken(flowTarget, at)
	} else {
		// Non-interrupting: leave host parked, spawn an additional token.

		// Remove only THIS boundary arm (it fired once; no re-arm in scope).
		s.removeBoundaryArm(ba.HostToken, ba.BoundaryNode)

		// Spawn a new Active token at the boundary's outgoing flow target.
		s.placeToken(flowTarget, at)
	}

	// Drive forward (the newly placed token(s)).
	driveCmds, err := drive(def, s, at)
	if err != nil {
		return nil, err
	}
	cmds = append(cmds, driveCmds...)
	return cmds, nil
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
	// task.IsOpen() returns true only for Unclaimed or Claimed states; both
	// Completed and Cancelled are "already resolved", including a duplicate SLA
	// fire after cancellation.
	task := s.TaskByToken(rec.TaskToken)
	if task != nil && !task.IsOpen() {
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

	// If the task is nil (already resolved/advanced) or no longer open
	// (Completed or Cancelled), the reminder is stale — clean no-op, remove
	// the stale record. A nil task means no open task: treat as not-open.
	task := s.TaskByToken(rec.TaskToken)
	if task == nil || !task.IsOpen() {
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
	// Deep-copy ArmedEvents: armedEvent is a value type (no pointers), so a slice
	// copy is sufficient.
	s.ArmedEvents = append([]armedEvent(nil), st.ArmedEvents...)
	// Deep-copy Boundaries: boundaryArm is a value type (no pointers), so a slice
	// copy is sufficient.
	s.Boundaries = append([]boundaryArm(nil), st.Boundaries...)
	return s
}

package engine

import (
	"fmt"
	"strings"
	"time"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/model"
)

// StepMode selects how far one Step advances.
// Macro (default) runs drive until all active tokens are parked or consumed.
// Micro runs drive until the first token park or terminal event, then stops,
// leaving any remaining active tokens for subsequent Step calls.
type StepMode int

const (
	Macro StepMode = iota
	Micro
)

// StepOptions controls optional behaviour of a [Step] call.
type StepOptions struct {
	// Mode selects the step granularity: [Macro] (default) or [Micro].
	Mode StepMode
	// DefaultRetryPolicy is the fallback retry policy applied when a node does
	// not carry its own RetryPolicy. nil means retry is disabled by default.
	DefaultRetryPolicy *model.RetryPolicy
}

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
			return StepResult{}, fmt.Errorf("workflow-engine: expected exactly one start, got %d", len(starts))
		}
		s.placeToken(starts[0].ID, t.OccurredAt())
		// Arm any top-level event sub-processes (root scope, enclosingScopeID == "").
		espCmds, espErr := armEventSubprocesses(def, &s, "", t.OccurredAt())
		if espErr != nil {
			return StepResult{}, espErr
		}
		// Drive forward from the start node; prepend any esp ScheduleTimer commands.
		driveCmdsStart, driveErrStart := drive(def, &s, t.OccurredAt(), opt.Mode)
		if driveErrStart != nil {
			return StepResult{}, driveErrStart
		}
		return StepResult{State: s, Commands: append(espCmds, driveCmdsStart...)}, nil

	case ActionCompleted:
		// If the engine is in compensation mode AND this ActionCompleted corresponds
		// to the in-flight compensation action (cursor.ActiveCmdID), advance the
		// compensation cursor rather than doing normal token routing. This keeps
		// compensation sequencing deterministic and observable (one action at a time).
		if s.Status == StatusCompensating && s.Compensating.ActiveCmdID == t.CommandID {
			return stepCompensationAdvance(def, &s, t.OccurredAt(), opt.Mode)
		}

		tok := s.tokenAwaiting(t.CommandID)
		if tok == nil {
			return StepResult{}, fmt.Errorf("%w: %q", ErrTokenNotFound, t.CommandID)
		}
		// Cancel any boundary arms on this host token before advancing.
		var preCmds []Command
		for _, timerID := range s.removeBoundaryArmsForHost(tok.ID) {
			preCmds = append(preCmds, CancelTimer{TimerID: timerID})
		}
		// Resolve the effective definition for the token's scope so we can look up
		// the node and check for a CompensationAction BEFORE merging output.
		tdef, err := defForScope(def, &s, tok.ScopeID)
		if err != nil {
			return StepResult{}, err
		}
		// Record compensation BEFORE merging the output: the snapshot captures the
		// instance variables as they existed when the activity was invoked.
		if node, ok := tdef.Node(tok.NodeID); ok && node.CompensationAction != "" {
			s.recordCompensation(tok.ScopeID, node.ID, node.CompensationAction, t.OccurredAt(), copyVars(s.Variables))
		}
		mergeVars(&s, t.Output)
		tok.State = TokenActive
		tok.AwaitCommand = ""
		// Advance the token past the completed ServiceTask so drive sees it at
		// the next node, not re-firing the action. Use the token's scope definition
		// so inner-scope tokens resolve flows against the nested definition.
		s.moveAlongSingleFlow(tdef, tok, t.OccurredAt())
		driveCmds, err := drive(def, &s, t.OccurredAt(), opt.Mode)
		if err != nil {
			return StepResult{}, err
		}
		return StepResult{State: s, Commands: append(preCmds, driveCmds...)}, nil

	case CancelRequested:
		// Admin trigger: terminate the instance, optionally running compensation first.
		// Emit InvokeCancelAction (fire-and-forget) for each entry in def.CancelActions
		// regardless of whether compensation records exist (ADR-0028 unchanged).
		var cancelActionCmds []Command
		for _, name := range def.CancelActions {
			cancelActionCmds = append(cancelActionCmds, InvokeCancelAction{Name: name, Input: copyVars(s.Variables)})
		}

		if len(s.RootCompensations) > 0 {
			// Compensation walk before termination (ADR-0034).
			// beginCompensation clears tokens/timers/arms and emits the first compensation
			// InvokeAction, setting the cursor with FinalStatus=Terminated and FinalErr="cancelled".
			// stepCompensationFinish will emit FailInstance{"cancelled"} at walk end.
			s.Status = StatusCompensating
			res, err := beginCompensation(def, &s, "", StatusTerminated, "cancelled", t.OccurredAt(), opt.Mode)
			if err != nil {
				return StepResult{}, err
			}
			res.Commands = append(cancelActionCmds, res.Commands...)
			return res, nil
		}

		// No compensation records: immediate termination (unchanged behaviour).
		ended := t.OccurredAt()
		s.Status = StatusTerminated
		s.EndedAt = &ended
		for i := range s.Tokens {
			tok := &s.Tokens[i]
			s.closeVisit(tok.ID, tok.NodeID, t.OccurredAt())
		}
		s.Tokens = nil
		cmds := cancelActionCmds
		cmds = append(cmds, FailInstance{Err: "cancelled"})
		cmds = append(cmds, s.cancelAllTimers()...)
		cmds = append(cmds, s.cancelAllArmsAndBoundaries()...)
		return StepResult{State: s, Commands: cmds}, nil

	case CompensateRequested:
		// Admin/debug reverse-order compensation trigger.
		// 1. Set status to StatusCompensating.
		// 2. Build the compensation cursor pointing at the first (most-recent) record
		//    to emit (the last index in the relevant slice that is AFTER ToNode).
		// 3. Emit the first InvokeAction and record the cursor.
		return stepCompensateRequested(def, &s, t, opt.Mode)

	case ActionFailed:
		// Best-effort compensation: if the engine is compensating and the failed
		// command is the active compensation action, skip that record and advance
		// the walk rather than re-entering propagateError/retry (ADR-0034 §2.5).
		if s.Status == StatusCompensating && s.Compensating.ActiveCmdID == t.CommandID {
			return stepCompensationAdvance(def, &s, t.OccurredAt(), opt.Mode)
		}

		tok := s.tokenAwaiting(t.CommandID)
		if tok == nil {
			return StepResult{}, fmt.Errorf("%w: %q", ErrTokenNotFound, t.CommandID)
		}
		// Cancel any boundary arms on this host token before propagating.
		// On the unhandled path cancelAllArmsAndBoundaries covers the rest;
		// on the caught path we clear them before routing to the recovery flow.
		var preCmds []Command
		for _, timerID := range s.removeBoundaryArmsForHost(tok.ID) {
			preCmds = append(preCmds, CancelTimer{TimerID: timerID})
		}
		// Retry interception: when the node (or the default policy) carries a retry
		// policy and the failure is non-terminal, schedule a TimerRetry instead of
		// propagating the error immediately.
		tdef, err := defForScope(def, &s, tok.ScopeID)
		if err != nil {
			return StepResult{}, err
		}
		node, _ := tdef.Node(tok.NodeID)
		if eff, hasPolicy := effectiveRetryPolicy(node, opt); hasPolicy {
			attempt := tok.RetryAttempts
			terminal := !t.Retryable ||
				eff.IsNonRetryable(t.Err) ||
				(eff.MaxAttempts != 0 && attempt+1 >= eff.MaxAttempts) ||
				(eff.MaxElapsed > 0 && !tok.RetryStartedAt.IsZero() &&
					t.OccurredAt().Sub(tok.RetryStartedAt) > eff.MaxElapsed)
			if !terminal {
				delay := time.Duration(t.JitterFraction * float64(eff.Backoff(attempt)))
				fireAt := t.OccurredAt().Add(delay)
				timerID := s.nextTimerID()
				retryCmds := []Command{ScheduleTimer{
					TimerID: timerID,
					Token:   tok.ID,
					FireAt:  fireAt,
					Kind:    TimerRetry,
				}}
				s.Timers = append(s.Timers, timerRecord{
					TimerID: timerID,
					Kind:    TimerRetry,
					Token:   tok.ID,
					NodeID:  tok.NodeID,
					ScopeID: tok.ScopeID,
				})
				tok.RetryAttempts++
				if tok.RetryStartedAt.IsZero() {
					tok.RetryStartedAt = t.OccurredAt()
				}
				tok.State = TokenWaitingCommand
				tok.AwaitCommand = timerID
				return StepResult{State: s, Commands: append(preCmds, retryCmds...)}, nil
			}
			// Terminal exhaustion: precedence is (1) catch-flow → (2) error
			// boundary → (3) incident.
			if node.RecoveryFlow != "" {
				// (1) Catch-flow: inject error context onto instance variables and
				// route the failing token down RecoveryFlow.
				if s.Variables == nil {
					s.Variables = map[string]any{}
				}
				s.Variables["_errorMessage"] = t.Err
				// Total executions: initial attempt plus all retries.
				s.Variables["_errorAttempts"] = tok.RetryAttempts + 1
				if node.ErrorCode != "" {
					s.Variables["_error"] = node.ErrorCode
				}
				// Resolve the RecoveryFlow target (mirror the SLAFlow routing in
				// handleSLAFired: scan the scope def's flows for the flow ID).
				var target string
				for _, f := range tdef.Flows {
					if f.ID == node.RecoveryFlow {
						target = f.Target
						break
					}
				}
				if target == "" {
					return StepResult{}, fmt.Errorf("workflow-engine: retry exhaustion: RecoveryFlow %q not found for node %q", node.RecoveryFlow, node.ID)
				}
				tok.RetryAttempts = 0
				tok.RetryStartedAt = time.Time{}
				tok.AwaitCommand = ""
				tok.State = TokenActive
				s.moveTokenToTarget(tok, target, t.OccurredAt())
				driveCmds, err := drive(def, &s, t.OccurredAt(), opt.Mode)
				if err != nil {
					return StepResult{}, err
				}
				return StepResult{State: s, Commands: append(preCmds, driveCmds...)}, nil
			}
			// (2)+(3): no catch-flow → let propagateError catch the error via a
			// boundary handler; if none is found, raise an incident (the
			// raiseIncidentOnUnhandled=true flag) instead of failing the instance.
			errCmds, err := propagateError(def, &s, tok.ScopeID, tok.NodeID, tok.ID, t.Err, t.OccurredAt(), opt.Mode, true)
			if err != nil {
				return StepResult{}, err
			}
			return StepResult{State: s, Commands: append(preCmds, errCmds...)}, nil
		}
		// Route through propagateError: if a boundary error handler is found in
		// the scope chain (direct-attachment or enclosing-scope), the error is
		// caught and execution continues on the recovery path (no FailInstance).
		// If no handler is found, propagateError sets StatusFailed, emits
		// FailInstance, and performs terminal cleanup — preserving existing
		// behavior for root-level service tasks with no handler.
		// Pass tok.ID so the direct-attachment branch consumes THIS specific
		// token, not the first token found at the same NodeID+ScopeID.
		errCmds, err := propagateError(def, &s, tok.ScopeID, tok.NodeID, tok.ID, t.Err, t.OccurredAt(), opt.Mode, false)
		if err != nil {
			return StepResult{}, err
		}
		return StepResult{State: s, Commands: append(preCmds, errCmds...)}, nil

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
		// 3) event sub-process arm (interrupting: cancel scope; non-interrupting: spawn alongside).
		// 4) SLA/in-wait timer record (task-guarded timers).
		// 5) standalone intermediate catch event (token parks on TimerID).

		// 1) Gateway arm check.
		if ae := s.armedEventByTimer(t.TimerID); ae != nil {
			gwCmds, err := resolveGatewayWin(def, &s, *ae, t.OccurredAt(), opt.Mode)
			if err != nil {
				return StepResult{}, err
			}
			return StepResult{State: s, Commands: gwCmds}, nil
		}

		// 2) Boundary arm check.
		if ba := s.boundaryArmByTimer(t.TimerID); ba != nil {
			baCmds, err := fireBoundaryArm(def, &s, *ba, t.OccurredAt(), opt.Mode)
			if err != nil {
				return StepResult{}, err
			}
			return StepResult{State: s, Commands: baCmds}, nil
		}

		// 3) Event sub-process arm check.
		if ea := s.eventSubprocessArmByTimer(t.TimerID); ea != nil {
			eaCmds, err := fireEventSubprocessArm(def, &s, *ea, t.OccurredAt(), opt.Mode)
			if err != nil {
				return StepResult{}, err
			}
			return StepResult{State: s, Commands: eaCmds}, nil
		}

		// 4) SLA/in-wait/retry timer record.
		// s.Timers holds SLA (TimerSLA), in-wait/reminder (TimerInWait), and retry
		// (TimerRetry) records. Intermediate timers (TimerIntermediate) are never
		// appended to s.Timers; for those, the token parks on the TimerID as its
		// AwaitCommand, so they route via the tokenAwaiting path below.
		rec := s.timerByID(t.TimerID)
		if rec != nil {
			switch rec.Kind {
			case TimerSLA:
				return handleSLAFired(def, &s, *rec, t.OccurredAt(), opt.Mode)
			case TimerInWait:
				return handleReminderFired(def, &s, *rec, t.OccurredAt())
			case TimerRetry:
				return handleRetryFired(def, &s, *rec, t.OccurredAt(), opt.Mode)
			}
		}

		// 5) Standalone intermediate timer.
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
		timerTdef, timerTdefErr := defForScope(def, &s, tok.ScopeID)
		if timerTdefErr != nil {
			return StepResult{}, timerTdefErr
		}
		s.moveAlongSingleFlow(timerTdef, tok, t.OccurredAt())
		driveCmds, err := drive(def, &s, t.OccurredAt(), opt.Mode)
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
			return StepResult{}, fmt.Errorf("workflow-engine: human-completed for token %q has no task record: %w", t.TaskToken, humantask.ErrTaskNotFound)
		}
		mergeVars(&s, t.Output)
		s.setVisitActor(tok.ID, tok.NodeID, t.Actor.ID)
		task.State = humantask.Completed
		tok.State = TokenActive
		tok.AwaitCommand = ""
		humanTdef, humanTdefErr := defForScope(def, &s, tok.ScopeID)
		if humanTdefErr != nil {
			return StepResult{}, humanTdefErr
		}
		s.moveAlongSingleFlow(humanTdef, tok, t.OccurredAt())
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
		driveCmds, err := drive(def, &s, t.OccurredAt(), opt.Mode)
		if err != nil {
			return StepResult{}, err
		}
		cmds = append(cmds, driveCmds...)
		return StepResult{State: s, Commands: cmds}, nil

	case SignalReceived:
		// Broadcast semantics within the instance: resume every token that is
		// awaiting this signal name. Tokens are processed in slice order for
		// determinism. A signal that matches no token (and no gateway arm, no
		// boundary arm, and no event sub-process arm) is a clean no-op — mergeVars
		// runs ONLY when at least one match is found.
		//
		// NOTE: mergeVars is deferred until after match-checking so that a no-match
		// delivery does not mutate instance variables (Task-2 review fix).
		//
		// Dispatch order for signal:
		// 1) event-based gateway arm (first-event-wins).
		// 2) boundary event arm (interrupting/non-interrupting).
		// 3) event sub-process arm (interrupting: cancel scope; non-interrupting: spawn alongside).
		// 4) standalone parked-signal tokens (broadcast).
		//
		// INTENTIONAL BROADCAST: A single signal name may simultaneously resolve a
		// gateway arm (step 1), fire a boundary arm (step 2), fire an event sub-process
		// arm (step 3), AND resume one or more standalone parked-signal tokens (step 4)
		// within the same Step call. All dispatch points are evaluated in order; matching
		// is not mutually exclusive across them. Using the same signal name on competing
		// constructs is permitted but is the definition author's responsibility.
		//
		// SNAPSHOT SEMANTICS: BPMN signal delivery is a single event at the delivery
		// instant — only tokens already awaiting the signal AT DELIVERY TIME should
		// catch it. We snapshot the set of token IDs awaiting this signal BEFORE running
		// steps 1–3. Step 4 only resumes tokens whose ID is in that snapshot.
		// A token spawned by a non-interrupting boundary/event-subprocess arm during
		// this Step is NOT in the snapshot and will not be re-consumed by the same delivery.
		snapshotIDs := s.tokenIDsAwaitingSignal(t.Name)

		var signalCmds []Command
		matched := false

		// 1) Check whether the signal matches an event-gateway arm.
		if ae := s.armedEventBySignal(t.Name); ae != nil {
			if !matched {
				mergeVars(&s, t.Payload)
				matched = true
			}
			gwCmds, err := resolveGatewayWin(def, &s, *ae, t.OccurredAt(), opt.Mode)
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
			baCmds, err := fireBoundaryArm(def, &s, *ba, t.OccurredAt(), opt.Mode)
			if err != nil {
				return StepResult{}, err
			}
			signalCmds = append(signalCmds, baCmds...)
		}

		// 3) Check whether the signal matches an event sub-process arm.
		if ea := s.eventSubprocessArmBySignal(t.Name); ea != nil {
			if !matched {
				mergeVars(&s, t.Payload)
				matched = true
			}
			eaCmds, err := fireEventSubprocessArm(def, &s, *ea, t.OccurredAt(), opt.Mode)
			if err != nil {
				return StepResult{}, err
			}
			signalCmds = append(signalCmds, eaCmds...)
		}

		// 4) Resume all standalone parked-signal tokens that were in the snapshot
		// (i.e. awaiting this signal at the delivery instant). Tokens spawned by
		// steps 1–3 above are not in the snapshot and will not be consumed here.
		for _, tokenID := range snapshotIDs {
			tok := s.tokenByID(tokenID)
			// Skip if the token was consumed by an interrupting boundary/event-subprocess (steps 2–3)
			// or is no longer awaiting this signal.
			if tok == nil || tok.AwaitSignal != t.Name {
				continue
			}
			if !matched {
				mergeVars(&s, t.Payload)
				matched = true
			}
			tok.AwaitSignal = ""
			tok.State = TokenActive
			signalTdef, signalTdefErr := defForScope(def, &s, tok.ScopeID)
			if signalTdefErr != nil {
				return StepResult{}, signalTdefErr
			}
			s.moveAlongSingleFlow(signalTdef, tok, t.OccurredAt())
			driveCmds, err := drive(def, &s, t.OccurredAt(), opt.Mode)
			if err != nil {
				return StepResult{}, err
			}
			signalCmds = append(signalCmds, driveCmds...)
		}
		return StepResult{State: s, Commands: signalCmds}, nil

	case SubInstanceCompleted:
		// A child process instance (started by StartSubInstance) has finished
		// successfully. Resume the parent token that was parked at the call-activity
		// node, merge the child's output variables into the parent, then drive forward.
		//
		// Mirror of ActionCompleted: find the parked token by CommandID, merge vars,
		// activate the token, move along its single outgoing flow, then drive.
		tok := s.tokenAwaiting(t.CommandID)
		if tok == nil {
			return StepResult{}, fmt.Errorf("%w: %q", ErrTokenNotFound, t.CommandID)
		}
		mergeVars(&s, t.Output)
		tok.State = TokenActive
		tok.AwaitCommand = ""
		// Advance the token past the call-activity node using the token's scope
		// definition (call-activity nodes can live inside a sub-process scope).
		tdef, err := defForScope(def, &s, tok.ScopeID)
		if err != nil {
			return StepResult{}, err
		}
		s.moveAlongSingleFlow(tdef, tok, t.OccurredAt())
		driveCmds, err := drive(def, &s, t.OccurredAt(), opt.Mode)
		if err != nil {
			return StepResult{}, err
		}
		return StepResult{State: s, Commands: driveCmds}, nil

	case SubInstanceFailed:
		// A child process instance has terminated with an error. For this plan (Plan 7),
		// a failed child fails the parent instance (boundary error events are Plan 8).
		// Mirror of ActionFailed: find the parked token, set StatusFailed, emit
		// FailInstance, and cancel all timers/arms to clean up.
		//
		// Plan 8 note: when a boundary error event is present on the call-activity
		// node, SubInstanceFailed should route to it instead of failing the parent.
		// This fallback (FailInstance) is correct until Plan 8 arrives.
		tok := s.tokenAwaiting(t.CommandID)
		if tok == nil {
			return StepResult{}, fmt.Errorf("%w: %q", ErrTokenNotFound, t.CommandID)
		}
		s.Status = StatusFailed
		ended := t.OccurredAt()
		s.EndedAt = &ended
		cmds := []Command{FailInstance{Err: t.Err}}
		cmds = append(cmds, s.cancelAllTimers()...)
		cmds = append(cmds, s.cancelAllArmsAndBoundaries()...)
		return StepResult{State: s, Commands: cmds}, nil

	case MessageReceived:
		// Point-to-point semantics: resume the single token whose AwaitMessage
		// matches the name AND whose AwaitMessageKey matches the correlation key.
		// A message that matches no token (and no gateway arm, and no event sub-process arm)
		// is a clean no-op.
		//
		// Dispatch order for message:
		// 1) event-based gateway arm (first-event-wins).
		// 2) event sub-process arm (interrupting/non-interrupting).
		// 3) standalone parked-message token (point-to-point).
		//
		// NOTE: mergeVars is deferred until after match-checking so that a no-match
		// delivery does not mutate instance variables (Task-2 review fix).

		// 1) Check whether the message matches an event-gateway arm (first-event-wins).
		if ae := s.armedEventByMessage(t.Name, t.CorrelationKey); ae != nil {
			mergeVars(&s, t.Payload)
			gwCmds, err := resolveGatewayWin(def, &s, *ae, t.OccurredAt(), opt.Mode)
			if err != nil {
				return StepResult{}, err
			}
			return StepResult{State: s, Commands: gwCmds}, nil
		}

		// 2) Check whether the message matches an event sub-process arm.
		if ea := s.eventSubprocessArmByMessage(t.Name, t.CorrelationKey); ea != nil {
			mergeVars(&s, t.Payload)
			eaCmds, err := fireEventSubprocessArm(def, &s, *ea, t.OccurredAt(), opt.Mode)
			if err != nil {
				return StepResult{}, err
			}
			return StepResult{State: s, Commands: eaCmds}, nil
		}

		// 3) Resume the standalone parked-message token.
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
		msgTdef, msgTdefErr := defForScope(def, &s, tok.ScopeID)
		if msgTdefErr != nil {
			return StepResult{}, msgTdefErr
		}
		s.moveAlongSingleFlow(msgTdef, tok, t.OccurredAt())
		driveCmds, err := drive(def, &s, t.OccurredAt(), opt.Mode)
		if err != nil {
			return StepResult{}, err
		}
		return StepResult{State: s, Commands: driveCmds}, nil

	case ResolveIncident:
		// Admin trigger: clear a parked incident, grant additional retry budget,
		// and re-invoke the stalled service action so the process can continue.
		//
		// Idempotency: an unknown or already-cleared IncidentID is a clean no-op;
		// a missing token (removed by a concurrent path) clears the record and
		// returns without re-invoking.
		idx := -1
		for i := range s.Incidents {
			if s.Incidents[i].ID == t.IncidentID {
				idx = i
				break
			}
		}
		if idx < 0 {
			// Unknown or already-resolved incident: idempotent no-op.
			return StepResult{State: s, Commands: nil}, nil
		}
		inc := s.Incidents[idx]
		// Remove the incident from the slice (order-preserving, avoids aliasing).
		s.Incidents = append(s.Incidents[:idx], s.Incidents[idx+1:]...)
		tok := s.tokenByID(inc.TokenID)
		if tok == nil {
			// Token is gone (concurrent resolution); incident cleared, no re-invoke.
			return StepResult{State: s, Commands: nil}, nil
		}
		// Grant the additional retry budget: reducing RetryAttempts by AddAttempts
		// effectively gives the action that many more opportunities before the
		// policy declares it terminal again.
		tok.RetryAttempts = max(0, tok.RetryAttempts-t.AddAttempts)
		tok.State = TokenActive
		cmds, err := reinvokeServiceAction(def, &s, tok, t.OccurredAt())
		if err != nil {
			return StepResult{}, err
		}
		return StepResult{State: s, Commands: cmds}, nil

	default:
		return StepResult{}, fmt.Errorf("%w: %T", ErrUnknownTrigger, trg)
	}
}

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
	if node.Subprocess == nil {
		return nil, fmt.Errorf("workflow-engine: defForScope: node %q has no Subprocess definition", scope.NodeID)
	}
	return node.Subprocess, nil
}

// drive advances active tokens until each is parked or consumed.
//
// In Macro mode (default) drive loops until no active tokens remain.
// In Micro mode drive stops after the first token park or terminal event,
// leaving any remaining active tokens for subsequent Step(Micro) calls.
// Auto-advancing nodes (StartEvent, gateway routing that produces new active
// tokens) do not count as stops in Micro mode; execution passes through them
// within the same drive call until a park/terminal is reached.
//
// def is the TOP-LEVEL process definition. For each token, the effective
// definition (tdef) is resolved via defForScope against the token's ScopeID so
// that tokens inside a sub-process scope resolve nodes/flows against the nested
// definition rather than the top-level one.
func drive(def *model.ProcessDefinition, s *InstanceState, at time.Time, mode StepMode) ([]Command, error) {
	var cmds []Command
	for {
		tok := s.firstActive()
		if tok == nil {
			break
		}

		// Resolve the effective definition for this token's scope.
		tdef, err := defForScope(def, s, tok.ScopeID)
		if err != nil {
			return cmds, err
		}

		node, ok := tdef.Node(tok.NodeID)
		if !ok {
			// Defensive: a token on a missing node cannot advance.
			tok.State = TokenWaitingCommand
			continue
		}

		// stopped is set to true by any case that parks or terminally consumes
		// this token (ServiceTask, UserTask, EndEvent, etc.). In Micro mode the
		// loop breaks as soon as stopped is true, leaving remaining active tokens
		// for the next Step call. Auto-advancing cases (StartEvent, gateway routing
		// that produces new active tokens) leave stopped false so the loop continues.
		stopped := false

		switch node.Kind {
		case model.KindStartEvent:
			s.moveAlongSingleFlow(tdef, tok, at)

		case model.KindServiceTask:
			cmdID := s.nextCommandID()
			cmds = append(cmds, InvokeAction{
				CommandID: cmdID,
				Name:      node.Action,
				Input:     serviceActionInput(s, node),
			})
			tok.State = TokenWaitingCommand
			tok.AwaitCommand = cmdID
			// Arm any boundary events attached to this host activity.
			bndCmds, err := armBoundaries(tdef, s, tok.ID, node.ID, at)
			if err != nil {
				return cmds, err
			}
			cmds = append(cmds, bndCmds...)
			stopped = true // token parked: Micro stops here

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
					return cmds, fmt.Errorf("workflow-engine: SLA node %q: %w", node.ID, err)
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
					ScopeID:   tok.ScopeID,
				})
				ht.DueAt = &fireAt
			}
			// If the node carries a reminder interval, schedule the first in-wait
			// timer. Subsequent reminders are re-scheduled each time the timer fires
			// (see handleReminderFired), so a single ScheduleTimer is enough here.
			if node.ReminderEvery != "" {
				dur, err := conditions.EvalDuration(node.ReminderEvery, s.Variables)
				if err != nil {
					return cmds, fmt.Errorf("workflow-engine: reminder node %q: %w", node.ID, err)
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
					ScopeID:   tok.ScopeID,
				})
			}
			s.Tasks = append(s.Tasks, ht)
			cmds = append(cmds, AwaitHuman{TaskToken: taskToken, Eligibility: spec})
			tok.State = TokenWaitingCommand
			tok.AwaitCommand = taskToken
			// Arm any boundary events attached to this host activity.
			bndCmds, err := armBoundaries(tdef, s, tok.ID, node.ID, at)
			if err != nil {
				return cmds, err
			}
			cmds = append(cmds, bndCmds...)
			stopped = true // token parked: Micro stops here

		case model.KindIntermediateCatchEvent:
			if node.TimerDuration != "" {
				dur, err := conditions.EvalDuration(node.TimerDuration, s.Variables)
				if err != nil {
					return cmds, fmt.Errorf("workflow-engine: timer node %q: %w", node.ID, err)
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
					return cmds, fmt.Errorf("workflow-engine: message node %q correlation key: %w", node.ID, err)
				}
				tok.State = TokenWaitingCommand
				tok.AwaitMessage = node.MessageName
				tok.AwaitMessageKey = resolvedKey
			} else {
				// Non-timer, non-signal, non-message intermediate catch event: park.
				// Further event variants arrive in later plans.
				tok.State = TokenWaitingCommand
			}
			stopped = true // token parked: Micro stops here

		case model.KindErrorEndEvent:
			// Error end event: throw an error with node.ErrorCode from the token's
			// current scope. propagateError walks the scope chain outward looking for
			// a matching boundary error handler on the enclosing sub-process. An error
			// end event is not an activity node that carries a direct boundary, so we
			// pass "" as originatingNodeID (no direct-attachment check needed) and ""
			// as failingTokenID (the error-end token is already consumed above).
			currentScopeID := tok.ScopeID
			s.consumeToken(tok, at)
			errCmds, propErr := propagateError(def, s, currentScopeID, "", "", node.ErrorCode, at, mode, false)
			if propErr != nil {
				return cmds, propErr
			}
			cmds = append(cmds, errCmds...)
			// propagateError either caught the error (routing a token to the recovery
			// flow and calling drive) or failed the instance. Either way, we stop
			// the current drive loop iteration — the recovery token is already
			// active and will be picked up by a subsequent drive call inside
			// propagateError, or the instance is terminal.
			return cmds, nil

		case model.KindEndEvent:
			// An EndEvent behaves differently depending on whether the token is at the
			// root scope or inside a sub-process scope:
			//   - Root scope (tok.ScopeID == ""): consume the token; when no tokens
			//     remain anywhere, the instance is complete → CompleteInstance.
			//   - Sub-process scope: consume the inner token; when the scope drains
			//     (tokensInScope == 0), close the scope and resume the parent by placing
			//     a token on the sub-process activity's outgoing flow in the parent scope.
			currentScopeID := tok.ScopeID
			s.consumeToken(tok, at)

			if currentScopeID == "" {
				// Root scope: instance completion when all tokens are gone.
				if len(s.Tokens) == 0 {
					s.Status = StatusCompleted
					ended := at
					s.EndedAt = &ended
					cmds = append(cmds, CompleteInstance{Result: copyVars(s.Variables)})
				}
			} else {
				// Sub-process scope: check whether the scope is now empty.
				// We use tokensInScope for the immediate scope; child scope tokens have
				// a different ScopeID (the child scope's ID), so they do NOT count here.
				if s.tokensInScope(currentScopeID) == 0 {
					scope := s.scopeByID(currentScopeID)
					if scope == nil {
						return cmds, fmt.Errorf("workflow-engine: sub-process end: scope %q not found", currentScopeID)
					}
					subNodeID := scope.NodeID
					parentScopeID := scope.ParentID

					// Determine whether this scope belongs to a KindEventSubProcess node
					// in the parent definition. Event sub-process scope exit is handled
					// differently from regular sub-process scope exit:
					//   - Non-interrupting: just close this child scope; the enclosing scope
					//     keeps running (its tokens are still there).
					//   - Interrupting: the event sub-process replaces the enclosing scope;
					//     on completion, it closes the enclosing scope and resumes from the
					//     enclosing scope's parent (grandparent level). The enclosing scope
					//     was intentionally kept open (its tokens cancelled) so that we can
					//     check for remaining non-interrupting children before exiting.
					//
					// Fix 2: detect ESP child scope by checking the NodeID in the parent
					// definition regardless of whether parentScopeID is "" (root scope).
					// The previous guard (parentScopeID != "") excluded root-level ESPs,
					// causing the engine to fall into the regular sub-process branch and
					// error ("no outgoing flows from root-esp in root definition").
					isEventSubProcess := false
					parentDef, pErr := defForScope(def, s, parentScopeID)
					if pErr == nil {
						if espNode, ok2 := parentDef.Node(subNodeID); ok2 && espNode.Kind == model.KindEventSubProcess {
							isEventSubProcess = true
						}
					}

					if isEventSubProcess {
						// Event sub-process scope drained.
						// Close this child scope.
						s.closeScope(currentScopeID)

						// Fix 2: handle root-level ESP (parentScopeID == "") distinctly from
						// nested ESP (parentScopeID != ""). The root scope is implicit (no Scope
						// object exists for it), so scopeByID("") always returns nil. We must
						// NOT treat that nil as "enclosing scope already closed".
						if parentScopeID == "" {
							// Root-level event sub-process.
							// Non-interrupting: the root scope still has tokens → just close child.
							if s.tokensInScope("") > 0 {
								break
							}
							// Check if any other child scopes of the root still have tokens.
							hasOtherRootChildren := false
							for _, sc := range s.Scopes {
								if sc.ParentID == "" && sc.ID != currentScopeID {
									if s.tokensInScope(sc.ID) > 0 {
										hasOtherRootChildren = true
										break
									}
								}
							}
							if hasOtherRootChildren {
								break
							}
							// Interrupting root-level ESP completed: all root tokens were cancelled
							// and no sibling child scopes remain. The instance is now complete.
							// Cancel any remaining ESP arms for the root scope.
							for _, timerID := range s.removeEventSubprocessArmsForScope("") {
								cmds = append(cmds, CancelTimer{TimerID: timerID})
							}
							// Instance completes: all tokens gone, no active root children.
							if len(s.Tokens) == 0 {
								s.Status = StatusCompleted
								ended := at
								s.EndedAt = &ended
								cmds = append(cmds, CompleteInstance{Result: copyVars(s.Variables)})
							}
							break
						}

						// Nested event sub-process (parentScopeID != "").
						// Check what kind of event sub-process this is:
						// If the parent scope (enclosingScopeID) still has tokens or is still
						// a normal running scope, this was NON-interrupting → just close child.
						// If the parent scope has 0 tokens (they were all cancelled by interrupting
						// fire) AND no other child scopes of the parent have tokens, the
						// interrupting event sub-process is done → close enclosing scope and
						// resume the grandparent.
						enclosingScope := s.scopeByID(parentScopeID)
						if enclosingScope == nil {
							// Enclosing scope was already closed (defensive).
							break
						}
						if s.tokensInScope(parentScopeID) > 0 {
							// Enclosing scope still has tokens → non-interrupting case.
							// Child is done; enclosing scope keeps running. No further action.
							break
						}
						// No tokens in enclosing scope. Check if any other children still running.
						hasOtherChildren := false
						for _, sc := range s.Scopes {
							if sc.ParentID == parentScopeID && sc.ID != currentScopeID {
								if s.tokensInScope(sc.ID) > 0 {
									hasOtherChildren = true
									break
								}
							}
						}
						if hasOtherChildren {
							break
						}
						// Interrupting event sub-process completed: close enclosing scope and
						// resume in the grandparent.
						grandparentScopeID := enclosingScope.ParentID
						enclosingNodeID := enclosingScope.NodeID
						// Cancel remaining event sub-process arms for the enclosing scope.
						for _, timerID := range s.removeEventSubprocessArmsForScope(parentScopeID) {
							cmds = append(cmds, CancelTimer{TimerID: timerID})
						}
						s.closeScope(parentScopeID)

						// Resume execution: place a token on the enclosing sub-process
						// activity's outgoing flow in the grandparent scope.
						grandparentDef, gpErr := defForScope(def, s, grandparentScopeID)
						if gpErr != nil {
							return cmds, fmt.Errorf("workflow-engine: event sub-process exit: %w", gpErr)
						}
						if grandparentScopeID == "" {
							// Grandparent is the root scope.
							outs := grandparentDef.Outgoing(enclosingNodeID)
							if len(outs) == 0 {
								// Root scope: no outgoing flows from the sub-process → instance completes.
								if len(s.Tokens) == 0 {
									s.Status = StatusCompleted
									ended := at
									s.EndedAt = &ended
									cmds = append(cmds, CompleteInstance{Result: copyVars(s.Variables)})
								}
							} else {
								// Root scope: place token on sub-process outgoing flow target.
								s.placeToken(outs[0].Target, at)
							}
						} else {
							outs := grandparentDef.Outgoing(enclosingNodeID)
							if len(outs) == 0 {
								return cmds, fmt.Errorf("workflow-engine: event sub-process exit: enclosing node %q has no outgoing flows in grandparent definition", enclosingNodeID)
							}
							s.placeTokenInScope(outs[0].Target, grandparentScopeID, at)
						}
					} else {
						// Regular sub-process scope. Check if there are any active child scopes
						// (non-interrupting event sub-processes running alongside).
						hasActiveChildren := false
						for _, sc := range s.Scopes {
							if sc.ParentID == currentScopeID {
								if s.tokensInScope(sc.ID) > 0 {
									hasActiveChildren = true
									break
								}
							}
						}
						if hasActiveChildren {
							// Still waiting for child scopes to drain. Do not exit this scope yet.
							break
						}

						// Scope drained (and no active children): close it and resume in parent.
						// Cancel any still-armed event sub-process arms for this scope.
						for _, timerID := range s.removeEventSubprocessArmsForScope(currentScopeID) {
							cmds = append(cmds, CancelTimer{TimerID: timerID})
						}
						s.hoistCompensations(currentScopeID, parentScopeID)
						s.closeScope(currentScopeID)

						// Resolve the parent definition and find the sub-process activity's
						// outgoing flow in the parent scope.
						parentDef, err := defForScope(def, s, parentScopeID)
						if err != nil {
							return cmds, fmt.Errorf("workflow-engine: sub-process exit: %w", err)
						}

						// If the sub-process node itself carries a CompensationAction, record
						// it in the parent scope. The snapshot is taken after the scope is
						// closed (consistent: the sub-process completed at this point).
						if spNode, spOK := parentDef.Node(subNodeID); spOK && spNode.CompensationAction != "" {
							s.recordCompensation(parentScopeID, subNodeID, spNode.CompensationAction, at, copyVars(s.Variables))
						}

						outs := parentDef.Outgoing(subNodeID)
						if len(outs) == 0 {
							return cmds, fmt.Errorf("workflow-engine: sub-process exit: node %q has no outgoing flows in parent definition", subNodeID)
						}
						// Place a token on the first outgoing flow's target in the parent scope.
						s.placeTokenInScope(outs[0].Target, parentScopeID, at)
					}
				}
			}
			// Token consumed (end event). In Micro mode, stop after this node-advance
			// so the newly placed continuation token (if any) is processed in the next
			// Step call. Paths that hit 'break' above exit the switch before reaching
			// here, so stopped=false for those (continue-driving) paths.
			stopped = true

		case model.KindSubProcess:
			// Embedded sub-process entry: open a scope, place a token on the nested
			// start node, and consume the sub-process activity token (it is "inside" now).
			if node.Subprocess == nil {
				// Defensive: a KindSubProcess without a Subprocess definition cannot
				// execute; park to avoid infinite drive loop. model.Validate prevents this.
				tok.State = TokenWaitingCommand
				continue
			}
			innerStarts := node.Subprocess.StartNodes()
			if len(innerStarts) == 0 {
				return cmds, fmt.Errorf("workflow-engine: sub-process %q: nested definition has no start node", node.ID)
			}
			// Open a scope parented to the current token's scope.
			scopeID := s.openScope(node.ID, tok.ScopeID)
			// Place the inner start-event token in the new scope.
			s.placeTokenInScope(innerStarts[0].ID, scopeID, at)
			// Consume the sub-process activity token (execution is now "inside").
			s.consumeToken(tok, at)
			// Arm any KindEventSubProcess nodes defined inside this sub-process's
			// nested definition. They are scoped to the newly opened scope.
			espCmdsScope, espErrScope := armEventSubprocesses(node.Subprocess, s, scopeID, at)
			if espErrScope != nil {
				return cmds, espErrScope
			}
			cmds = append(cmds, espCmdsScope...)
			stopped = true // outer token consumed, inner token active: Micro stops here

		case model.KindExclusiveGateway:
			target, err := selectExclusiveTarget(tdef, s, node)
			if err != nil {
				// cmds is carried here for a future error-handling plan (Plan 8);
				// Step currently discards StepResult on error, so partial commands
				// are intentionally not delivered today.
				return cmds, err
			}
			s.moveTokenToTarget(tok, target, at)

		case model.KindParallelGateway:
			if len(tdef.Incoming(node.ID)) > 1 {
				s.tryParallelJoin(tdef, tok, node, tok.ScopeID, at)
				// Join pending: token is still in Tokens with State==TokenAtJoin.
				// Join fired: token was consumed by tryParallelJoin (no longer in Tokens).
				// Only stop in Micro when the join is pending (token still exists at join).
				if t := s.tokenByID(tok.ID); t != nil && t.State == TokenAtJoin {
					stopped = true
				}
			} else {
				s.forkParallel(tdef, tok, node, tok.ScopeID, at)
				// Fork: original token consumed, new active tokens placed. Auto-advance
				// so the loop picks up the first new token and processes it (in Micro,
				// it will stop when THAT token parks, not here at the fork itself).
			}

		case model.KindInclusiveGateway:
			if len(tdef.Incoming(node.ID)) > 1 {
				s.tryInclusiveJoin(tdef, tok, node, tok.ScopeID, at)
				// Join pending: token still exists at join. Stop in Micro.
				if t := s.tokenByID(tok.ID); t != nil && t.State == TokenAtJoin {
					stopped = true
				}
			} else {
				if err := s.forkInclusive(tdef, tok, node, tok.ScopeID, at); err != nil {
					return cmds, err
				}
				// Fork: original token consumed, new active tokens placed. Auto-advance.
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
			for _, f := range tdef.Outgoing(node.ID) {
				catchNode, ok := tdef.Node(f.Target)
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
						return cmds, fmt.Errorf("workflow-engine: event-gateway %q timer arm %q: %w", node.ID, catchNode.ID, err)
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
						return cmds, fmt.Errorf("workflow-engine: event-gateway %q message arm %q correlation key: %w", node.ID, catchNode.ID, err)
					}
					ae.Message = catchNode.MessageName
					ae.MessageKey = resolvedKey
				}
				s.ArmedEvents = append(s.ArmedEvents, ae)
			}
			stopped = true // gateway token parked: Micro stops here

		case model.KindCallActivity:
			// Call activity: emit StartSubInstance and park the token. The runtime
			// resolves DefRef via a DefinitionRegistry, runs the child to completion,
			// and returns a SubInstanceCompleted / SubInstanceFailed trigger that
			// resumes this parked token. No scope is opened here; the child instance
			// is fully isolated (separate instance lifecycle).
			//
			// Input: pass a copy of the current process variables so the child starts
			// with the parent's context. The parent does NOT read/write the child's
			// variables during the child's execution; they are merged on completion.
			cmdID := s.nextCommandID()
			cmds = append(cmds, StartSubInstance{
				CommandID: cmdID,
				DefRef:    node.DefRef,
				Input:     copyVars(s.Variables),
			})
			tok.State = TokenWaitingCommand
			tok.AwaitCommand = cmdID
			stopped = true // token parked: Micro stops here

		case model.KindIntermediateThrowEvent:
			if node.SignalName != "" {
				// Signal intermediate throw: emit ThrowSignal and continue along the
				// single outgoing flow. The runtime broadcasts the signal; the engine
				// does not wait for delivery (fire-and-forget from the engine's view).
				cmds = append(cmds, ThrowSignal{
					Name:    node.SignalName,
					Payload: nil, // no per-instance payload from throw nodes in this plan
				})
				s.moveAlongSingleFlow(tdef, tok, at)
				// Auto-advance: signal throw is fire-and-forget; stopped remains false.
			} else {
				// Non-signal intermediate throw: park for future plans (e.g. message
				// throw, error throw). Parking avoids an infinite drive loop.
				tok.State = TokenWaitingCommand
				stopped = true // token parked: Micro stops here
			}

		default:
			// Node kinds beyond linear flow arrive in later plans; park the
			// token so the loop terminates rather than spinning.
			tok.State = TokenWaitingCommand
			stopped = true // token parked: Micro stops here
		}

		// Micro-mode: stop after the first park or terminal event. Auto-advancing
		// cases (StartEvent, gateway routing that produces new active tokens) leave
		// stopped=false so the loop continues to the next token within this Step call.
		if mode == Micro && stopped {
			break
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

// forkParallel consumes the incoming token and creates one Active token at each
// outgoing flow target (definition order). Used for a diverging parallel gateway.
// scopeID is the gateway token's scope; forked tokens inherit it.
func (s *InstanceState) forkParallel(def *model.ProcessDefinition, tok *Token, node model.Node, scopeID string, at time.Time) {
	outs := def.Outgoing(node.ID)
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
			return fmt.Errorf("workflow-engine: gateway %q flow %q: %w", node.ID, f.ID, err)
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
		if s.Tokens[i].NodeID == node.ID && s.Tokens[i].State == TokenAtJoin && s.Tokens[i].ScopeID == scopeID {
			arrived++
		}
	}
	if arrived < len(def.Incoming(node.ID)) {
		return // still waiting on other branches in this scope
	}

	// Fire: remove all tokens parked at this join IN THIS SCOPE (closing their visits),
	// then create one Active token per outgoing flow.
	kept := make([]Token, 0, len(s.Tokens))
	for _, t := range s.Tokens {
		if t.NodeID == node.ID && t.State == TokenAtJoin && t.ScopeID == scopeID {
			s.closeVisit(t.ID, t.NodeID, at)
			continue
		}
		kept = append(kept, t)
	}
	s.Tokens = kept
	for _, f := range def.Outgoing(node.ID) {
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

	canReach := nodesThatCanReach(def, node.ID)
	for i := range s.Tokens {
		t := &s.Tokens[i]
		if t.NodeID == node.ID && t.State == TokenAtJoin && t.ScopeID == scopeID {
			continue // already arrived at the join in this scope
		}
		if t.ScopeID == scopeID && canReach[t.NodeID] {
			return // some token in this scope can still reach the join; keep waiting
		}
	}

	// Fire: consume all tokens parked at this join IN THIS SCOPE, then fork to outgoing flows.
	kept := make([]Token, 0, len(s.Tokens))
	for _, t := range s.Tokens {
		if t.NodeID == node.ID && t.State == TokenAtJoin && t.ScopeID == scopeID {
			s.closeVisit(t.ID, t.NodeID, at)
			continue
		}
		kept = append(kept, t)
	}
	s.Tokens = kept
	for _, f := range def.Outgoing(node.ID) {
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
			return "", fmt.Errorf("workflow-engine: gateway %q flow %q: %w", node.ID, f.ID, err)
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

// propagateError implements BPMN error propagation for a thrown errorCode.
//
// It performs two checks in order, stopping at the first match:
//
//  1. Direct-attachment check (only when originatingNodeID != ""):
//     Inspect the token's OWN scope definition (defForScope(top, s, scopeID)) for
//     a KindBoundaryEvent error event with AttachedTo == originatingNodeID and
//     (ErrorCode == errorCode || ErrorCode == ""). This covers the case where a
//     boundary error event is attached directly to the failing activity itself —
//     e.g. a root-level ServiceTask "svc" with a KindBoundaryEvent{AttachedTo:"svc"}
//     in the root definition. When found: consume ONLY the originating activity's
//     token (already done by the caller), cancel its arms, and route a token to the
//     boundary's outgoing flow TARGET in the SAME scope (scopeID). The scope is NOT
//     closed — only the individual activity's token is consumed (interrupting
//     boundary on a single node, contrast with enclosing-scope case below).
//
//  2. Enclosing-scope walk (unchanged from original behavior):
//     Walk outward from scopeID to root. At each level, inspect the scope's activity
//     node in the PARENT definition for a matching boundary error event with
//     AttachedTo == scope.NodeID (the sub-process that owns the scope). When found:
//     cancel ALL tokens in currentScopeID, close the scope, and route a token to the
//     boundary's outgoing flow in the PARENT scope. This is the interrupting-boundary
//     behavior for a sub-process error escape.
//
// Matching rule for a KindBoundaryEvent node bnd (both checks):
//   - bnd.AttachedTo == <activity-node-id>
//   - bnd.ErrorCode == errorCode (specific-code match) OR bnd.ErrorCode == "" (catch-all)
//   - No timer/signal/message fields set (it is an error boundary, not a timer/signal boundary)
//
// When NO handler is found (neither direct nor enclosing):
//   - Set s.Status = StatusFailed, s.EndedAt = &at.
//   - Emit FailInstance{Err: errorCode}.
//   - Emit CancelTimer for all outstanding timers, armed events, and boundaries.
//
// originatingNodeID should be set to the failing activity's NodeID (tok.NodeID) when
// called from ActionFailed. For KindErrorEndEvent, pass "" (an error end event is not
// an activity with a direct-attaching boundary).
//
// failingTokenID is the ID of the specific token that failed. When originatingNodeID
// is non-empty (ActionFailed path), the direct-attachment branch consumes THIS token
// by ID rather than by NodeID+ScopeID. This is correct when two active tokens occupy
// the same node in the same scope (e.g. in a parallel/loop topology) — consuming by
// ID ensures only the exact failing token is removed. For the KindErrorEndEvent path
// (originatingNodeID == ""), the error-end token is already consumed by drive before
// propagateError is called, so failingTokenID is unused.
// raiseIncidentOnUnhandled controls the no-handler fallback: when true, an
// unhandled error parks the failing token as a [TokenIncident] and keeps the
// instance running (admin-resumable) instead of setting StatusFailed.
func propagateError(top *model.ProcessDefinition, s *InstanceState, scopeID, originatingNodeID, failingTokenID, errorCode string, at time.Time, mode StepMode, raiseIncidentOnUnhandled bool) ([]Command, error) {
	// ── Step 1: Direct-attachment check ──────────────────────────────────────
	// Only when the caller provides an originating node (ActionFailed path).
	// Inspect the failing token's OWN scope definition for a boundary error event
	// with AttachedTo == originatingNodeID. If found, consume only the failing
	// activity's token and route a recovery token in the SAME scope.
	if originatingNodeID != "" {
		ownDef, err := defForScope(top, s, scopeID)
		if err != nil {
			return nil, fmt.Errorf("workflow-engine: propagateError: resolving own scope def for direct-attachment check: %w", err)
		}

		var directHandler *model.Node
		for i := range ownDef.Nodes {
			n := &ownDef.Nodes[i]
			if n.Kind != model.KindBoundaryEvent {
				continue
			}
			if n.AttachedTo != originatingNodeID {
				continue
			}
			// Must be an error boundary (no timer/signal/message fields).
			if n.TimerDuration != "" || n.SignalName != "" || n.MessageName != "" {
				continue
			}
			// Match: catch-all or specific code.
			if n.ErrorCode == "" || n.ErrorCode == errorCode {
				nn := *n
				directHandler = &nn
				break
			}
		}

		if directHandler != nil {
			// Direct boundary found. The failing activity's token was already cleaned
			// up by the ActionFailed handler (preCmds cancelled its arms; the token
			// itself is still present but parked — we need to consume it now).
			var cmds []Command

			// Consume the failing activity's token by its specific ID (failingTokenID).
			// Using the ID rather than NodeID+ScopeID ensures correctness when two
			// active tokens share the same node in the same scope (e.g. a parallel or
			// loop topology) — we remove only the exact failing token, not the first
			// one found by position.
			var failingTok *Token
			if failingTokenID != "" {
				failingTok = s.tokenByID(failingTokenID)
			}
			if failingTok == nil {
				// Fallback: locate by NodeID+ScopeID (defensive; should not occur when
				// the caller passes a valid failingTokenID).
				for i := range s.Tokens {
					if s.Tokens[i].NodeID == originatingNodeID && s.Tokens[i].ScopeID == scopeID {
						failingTok = &s.Tokens[i]
						break
					}
				}
			}
			if failingTok != nil {
				s.consumeToken(failingTok, at)
			}

			// Find the boundary's outgoing flow target in the own scope definition.
			outs := ownDef.Outgoing(directHandler.ID)
			if len(outs) == 0 {
				return cmds, fmt.Errorf("workflow-engine: propagateError: direct boundary %q has no outgoing flow", directHandler.ID)
			}
			flowTarget := outs[0].Target

			// Place a recovery token in the SAME scope (the failing token's scope).
			// This is the key distinction from the enclosing-scope case: only the
			// failing activity's token is consumed; the scope itself stays open.
			s.placeTokenInScope(flowTarget, scopeID, at)

			// Drive forward from the recovery token.
			driveCmds, err := drive(top, s, at, mode)
			if err != nil {
				return cmds, err
			}
			cmds = append(cmds, driveCmds...)
			return cmds, nil
		}
	}

	// ── Step 2: Enclosing-scope walk ─────────────────────────────────────────
	// Walk the scope chain from scopeID outward to root ("").
	// At each step, inspect the scope's activity node in the PARENT definition
	// for a matching boundary error event. The loop terminates when currentScopeID
	// reaches "" (root — no scope to inspect) or when a handler is found (early return).
	for currentScopeID := scopeID; currentScopeID != ""; {
		scope := s.scopeByID(currentScopeID)
		if scope == nil {
			// Scope is already closed (defensive). Stop walking.
			break
		}

		parentScopeID := scope.ParentID
		activityNodeID := scope.NodeID // the sub-process activity in the parent def

		// Resolve the parent definition.
		parentDef, err := defForScope(top, s, parentScopeID)
		if err != nil {
			return nil, fmt.Errorf("workflow-engine: propagateError: resolving parent def for scope %q: %w", currentScopeID, err)
		}

		// Scan the parent def for a boundary error event attached to activityNodeID
		// that matches errorCode (specific or catch-all).
		var handler *model.Node
		for i := range parentDef.Nodes {
			n := &parentDef.Nodes[i]
			if n.Kind != model.KindBoundaryEvent {
				continue
			}
			if n.AttachedTo != activityNodeID {
				continue
			}
			// Only boundary error events have a non-zero ErrorCode or catch-all
			// behavior. We identify a "boundary error event" as a KindBoundaryEvent
			// that has NO TimerDuration and NO SignalName and NO MessageName set
			// (i.e. it is not a timer/signal/message boundary but an error boundary).
			// The presence of ErrorCode (specific or empty catch-all) is the marker.
			//
			// Design note: we check !n.SignalName && !n.TimerDuration && !n.MessageName
			// so timer/signal boundary events on the same host are skipped. A boundary
			// event with no trigger fields at all defaults to error boundary semantics.
			if n.TimerDuration != "" || n.SignalName != "" || n.MessageName != "" {
				continue // not an error boundary
			}
			// Match: catch-all (n.ErrorCode=="") or specific code match.
			if n.ErrorCode == "" || n.ErrorCode == errorCode {
				nn := *n
				handler = &nn
				break
			}
		}

		if handler != nil {
			// Handler found in the PARENT scope. Cancel all tokens in currentScopeID,
			// close the scope, then route a token along the boundary's outgoing flow
			// in the parent scope.

			// 1. Cancel all tokens in the erroring scope.
			var cmds []Command
			tokensToCancel := make([]Token, 0, len(s.Tokens))
			for _, tok := range s.Tokens {
				if tok.ScopeID == currentScopeID {
					tokensToCancel = append(tokensToCancel, tok)
				}
			}
			for _, tok := range tokensToCancel {
				// Cancel SLA/reminder timers (UserTask case).
				for _, timerID := range s.cancelTimersByTaskToken(tok.AwaitCommand, "") {
					cmds = append(cmds, CancelTimer{TimerID: timerID})
				}
				// Cancel boundary arms for this host token.
				for _, timerID := range s.removeBoundaryArmsForHost(tok.ID) {
					cmds = append(cmds, CancelTimer{TimerID: timerID})
				}
				// Cancel any event-gateway arms.
				if strings.HasPrefix(tok.AwaitCommand, "evtgw:") {
					for _, timerID := range s.removeArmedEventsForGateway(tok.ID) {
						cmds = append(cmds, CancelTimer{TimerID: timerID})
					}
				}
				tokPtr := s.tokenByID(tok.ID)
				if tokPtr != nil {
					s.consumeToken(tokPtr, at)
				}
			}
			// Cancel ESP arms for the scope.
			for _, timerID := range s.removeEventSubprocessArmsForScope(currentScopeID) {
				cmds = append(cmds, CancelTimer{TimerID: timerID})
			}

			// 2. Close the erroring scope.
			s.closeScope(currentScopeID)

			// 3. Find the boundary's outgoing flow target in the parent definition.
			outs := parentDef.Outgoing(handler.ID)
			if len(outs) == 0 {
				return cmds, fmt.Errorf("workflow-engine: propagateError: boundary error %q has no outgoing flow", handler.ID)
			}
			flowTarget := outs[0].Target

			// 4. Place a token on the recovery path in the parent scope.
			s.placeTokenInScope(flowTarget, parentScopeID, at)

			// 5. Drive forward from the recovery token.
			driveCmds, err := drive(top, s, at, mode)
			if err != nil {
				return cmds, err
			}
			cmds = append(cmds, driveCmds...)
			return cmds, nil
		}

		// No handler at this scope level — walk up to the parent.
		currentScopeID = parentScopeID
	}

	// No handler found anywhere in the scope chain → unhandled error.
	if raiseIncidentOnUnhandled {
		// Do NOT fail the instance. Raise an incident on the failing token and
		// keep the instance running (admin-resumable). Used by the retry-
		// exhaustion path when an effective policy exists but neither a catch
		// flow nor a boundary handled the terminal failure.
		failingTok := s.tokenByID(failingTokenID)
		attempts, cmdID := 1, ""
		if failingTok != nil {
			// Attempts is the total executions: the initial attempt plus all
			// retries (RetryAttempts counts retries only).
			attempts = failingTok.RetryAttempts + 1
			cmdID = failingTok.AwaitCommand
			failingTok.State = TokenIncident
		}
		s.Incidents = append(s.Incidents, Incident{
			ID:        s.nextIncidentID(),
			TokenID:   failingTokenID,
			NodeID:    originatingNodeID,
			ScopeID:   scopeID,
			CommandID: cmdID,
			Error:     errorCode,
			Attempts:  attempts,
			CreatedAt: at,
		})
		return nil, nil
	}

	// Terminal unhandled error: run compensation walk before terminating (ADR-0034).
	if len(s.RootCompensations) > 0 {
		s.Status = StatusCompensating
		res, err := beginCompensation(top, s, "", StatusFailed, errorCode, at, mode)
		if err != nil {
			return nil, err
		}
		return res.Commands, nil
	}

	// No compensation records: immediate failure (unchanged behaviour).
	s.Status = StatusFailed
	ended := at
	s.EndedAt = &ended
	var cmds []Command
	cmds = append(cmds, FailInstance{Err: errorCode})
	cmds = append(cmds, s.cancelAllTimers()...)
	cmds = append(cmds, s.cancelAllArmsAndBoundaries()...)
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
				return nil, fmt.Errorf("workflow-engine: boundary %q on %q: %w", n.ID, hostNode, err)
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
func fireBoundaryArm(def *model.ProcessDefinition, s *InstanceState, ba boundaryArm, at time.Time, mode StepMode) ([]Command, error) {
	// Find the host token by ID (not by AwaitCommand — the host token parks on
	// taskToken/cmdID, not on the boundary timer). If the token is gone (already
	// consumed by another path), this is a late fire — clean no-op.
	hostTok := s.tokenByID(ba.HostToken)
	if hostTok == nil {
		// Also clean up stale boundary arms for this host (defensive).
		s.removeBoundaryArmsForHost(ba.HostToken)
		return nil, nil
	}

	// Resolve the effective definition for the boundary's scope. A boundary event
	// inside a sub-process must look up its outgoing flow in the INNER definition,
	// not the top-level one. defForScope returns the inner def for a scoped token.
	tdef, err := defForScope(def, s, hostTok.ScopeID)
	if err != nil {
		return nil, err
	}

	// Resolve the boundary's outgoing flow target.
	var flowTarget string
	for _, f := range tdef.Flows {
		if f.ID == ba.Flow {
			flowTarget = f.Target
			break
		}
	}
	if flowTarget == "" {
		// No target: unreachable if model.Validate passes (boundary must have outgoing flow).
		return nil, fmt.Errorf("workflow-engine: boundary %q: outgoing flow %q not found", ba.BoundaryNode, ba.Flow)
	}

	hostScopeID := hostTok.ScopeID
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

		// Place a new Active token at the boundary's outgoing flow target, keeping
		// the host token's scope so boundary-routed tokens stay in the same scope.
		s.placeTokenInScope(flowTarget, hostScopeID, at)
	} else {
		// Non-interrupting: leave host parked, spawn an additional token.

		// Remove only THIS boundary arm (it fired once; no re-arm in scope).
		s.removeBoundaryArm(ba.HostToken, ba.BoundaryNode)

		// Spawn a new Active token at the boundary's outgoing flow target, keeping
		// the host token's scope.
		s.placeTokenInScope(flowTarget, hostScopeID, at)
	}

	// Drive forward (the newly placed token(s)).
	driveCmds, err := drive(def, s, at, mode)
	if err != nil {
		return nil, err
	}
	cmds = append(cmds, driveCmds...)
	return cmds, nil
}

// armEventSubprocesses scans the given definition for KindEventSubProcess nodes
// and records an eventSubprocessArm for each. Called when a scope opens (via
// openScope in the KindSubProcess drive case) and at StartInstance for the root
// definition. enclosingScopeID is "" for root-level event sub-processes.
//
// Trigger encoding:
//   - Signal trigger: the nested definition's StartNodes()[0].SignalName is non-empty.
//   - Timer trigger: the nested definition's StartNodes()[0].TimerDuration is non-empty.
//   - Message trigger: the nested definition's StartNodes()[0].MessageName is non-empty.
//
// Timer triggers emit a ScheduleTimer command. Signal/message triggers are recorded
// only (delivery arrives via SignalReceived/MessageReceived).
//
// Definition-scan order is deterministic; arms are appended in that order.
func armEventSubprocesses(def *model.ProcessDefinition, s *InstanceState, enclosingScopeID string, at time.Time) ([]Command, error) {
	var cmds []Command
	for _, n := range def.Nodes {
		if n.Kind != model.KindEventSubProcess {
			continue
		}
		if n.Subprocess == nil {
			continue // defensive: no nested def, skip
		}
		starts := n.Subprocess.StartNodes()
		if len(starts) == 0 {
			continue // defensive: no start node in nested def, skip
		}
		startNode := starts[0]

		arm := eventSubprocessArm{
			EnclosingScopeID:    enclosingScopeID,
			EventSubprocessNode: n.ID,
			NonInterrupting:     n.NonInterrupting,
		}

		if startNode.SignalName != "" {
			arm.Signal = startNode.SignalName
		} else if startNode.TimerDuration != "" {
			dur, err := conditions.EvalDuration(startNode.TimerDuration, s.Variables)
			if err != nil {
				return nil, fmt.Errorf("workflow-engine: event sub-process %q timer: %w", n.ID, err)
			}
			timerID := s.nextTimerID()
			arm.TimerID = timerID
			cmds = append(cmds, ScheduleTimer{
				TimerID: timerID,
				Token:   "", // no host token; keyed by enclosing scope
				FireAt:  at.Add(dur),
				Kind:    TimerIntermediate,
			})
		} else if startNode.MessageName != "" {
			resolvedKey, err := conditions.EvalString(startNode.CorrelationKey, s.Variables)
			if err != nil {
				return nil, fmt.Errorf("workflow-engine: event sub-process %q message correlation key: %w", n.ID, err)
			}
			arm.Message = startNode.MessageName
			arm.MessageKey = resolvedKey
		}

		s.EventSubprocesses = append(s.EventSubprocesses, arm)
	}
	return cmds, nil
}

// fireEventSubprocessArm executes an event sub-process arm that has been triggered.
// Called from the SignalReceived, TimerFired, and MessageReceived handlers when the
// trigger matches an eventSubprocessArm entry.
//
// Dispatch order (relative to gateway/boundary/SLA/standalone):
//  1. Event-gateway arm (first-event-wins routing).
//  2. Boundary event arm (interrupting/non-interrupting on host activity).
//  3. Event sub-process arm (interrupting: cancel scope; non-interrupting: spawn alongside).
//  4. SLA/in-wait timer record.
//  5. Standalone parked token.
//
// For interrupting (!ea.NonInterrupting):
//  1. Verify the enclosing scope is still active (if not, clean no-op).
//  2. Cancel ALL tokens in the enclosing scope (consuming them + closing visits).
//  3. Cancel all other event-subprocess arms for the same scope (emit CancelTimer for timer arms).
//  4. Cancel all boundary arms for tokens that were in the scope.
//  5. Open a NEW child scope for the event sub-process (parent = enclosing scope).
//  6. Place a token on the event sub-process's start node in that child scope.
//  7. Drive forward. When this child scope drains (KindEndEvent path), it exits via the
//     ENCLOSING scope's parent (since the enclosing scope is now "completed" by the
//     event sub-process completion).
//
// For non-interrupting (ea.NonInterrupting):
//  1. Verify the enclosing scope is still active.
//  2. Do NOT cancel the enclosing scope's tokens.
//  3. Remove ONLY this arm (one-shot).
//  4. Open a child scope and place a start token — runs alongside.
//  5. Drive forward.
func fireEventSubprocessArm(def *model.ProcessDefinition, s *InstanceState, ea eventSubprocessArm, at time.Time, mode StepMode) ([]Command, error) {
	// Verify the enclosing scope is still active. For root scope (empty enclosingScopeID),
	// the scope is always "active" as long as the instance is running.
	if ea.EnclosingScopeID != "" {
		scope := s.scopeByID(ea.EnclosingScopeID)
		if scope == nil {
			// Enclosing scope is gone (completed or cancelled): stale trigger, clean no-op.
			return nil, nil
		}
	} else {
		// Root scope: active if instance is running.
		if s.Status != StatusRunning {
			return nil, nil
		}
	}

	// Resolve the enclosing scope's definition so we can find the event sub-process node.
	enclosingDef, err := defForScope(def, s, ea.EnclosingScopeID)
	if err != nil {
		return nil, err
	}

	// Resolve the event sub-process node in the enclosing definition.
	espNode, ok := enclosingDef.Node(ea.EventSubprocessNode)
	if !ok || espNode.Subprocess == nil {
		// Node missing or has no nested def: defensive no-op.
		return nil, nil
	}
	innerStarts := espNode.Subprocess.StartNodes()
	if len(innerStarts) == 0 {
		return nil, fmt.Errorf("workflow-engine: event sub-process %q: nested definition has no start node", ea.EventSubprocessNode)
	}

	var cmds []Command

	if !ea.NonInterrupting {
		// Interrupting: cancel all tokens in the enclosing scope, keep the enclosing
		// scope itself open (so the drain code can detect its children), then open a
		// child scope for the event sub-process.

		// Collect all tokens in the enclosing scope (snapshot to avoid mutating while iterating).
		var tokensToCancel []Token
		for _, tok := range s.Tokens {
			if tok.ScopeID == ea.EnclosingScopeID {
				tokensToCancel = append(tokensToCancel, tok)
			}
		}
		// Cancel SLA/reminder timers and boundary arms for each token in scope, then consume.
		for _, tok := range tokensToCancel {
			// Cancel SLA/reminder timers (UserTask case).
			taskTok := tok.AwaitCommand
			for _, timerID := range s.cancelTimersByTaskToken(taskTok, "") {
				cmds = append(cmds, CancelTimer{TimerID: timerID})
			}
			// Cancel boundary arms for this host token.
			for _, timerID := range s.removeBoundaryArmsForHost(tok.ID) {
				cmds = append(cmds, CancelTimer{TimerID: timerID})
			}
			// Fix 1: if the token is an event-based-gateway-parked token (its
			// AwaitCommand starts with the "evtgw:" sentinel), cancel all of its
			// armed events so their timers do not fire as stale orphans later.
			// Deterministic: removeArmedEventsForGateway returns timer IDs in
			// ArmedEvents slice order; we emit CancelTimer for each.
			if strings.HasPrefix(tok.AwaitCommand, "evtgw:") {
				for _, timerID := range s.removeArmedEventsForGateway(tok.ID) {
					cmds = append(cmds, CancelTimer{TimerID: timerID})
				}
			}
			// Consume the token (close visit).
			tokPtr := s.tokenByID(tok.ID)
			if tokPtr != nil {
				s.consumeToken(tokPtr, at)
			}
		}

		// Cancel sibling event-subprocess arms for the same enclosing scope (all arms,
		// including this one). Emit CancelTimer for timer arms.
		// removeEventSubprocessArmsForScope removes ALL arms for the scope including this one.
		for _, timerID := range s.removeEventSubprocessArmsForScope(ea.EnclosingScopeID) {
			cmds = append(cmds, CancelTimer{TimerID: timerID})
		}

		// Open a child scope for the event sub-process, parented to the ENCLOSING scope.
		// NodeID = the event sub-process node ID (KindEventSubProcess).
		// The drain code (KindEndEvent case) detects this as an event sub-process scope
		// (by checking the node kind in the parent definition) and handles completion:
		// when this child scope drains with no tokens left in the enclosing scope,
		// it closes the enclosing scope and resumes in the grandparent.
		childScopeID := s.openScope(ea.EventSubprocessNode, ea.EnclosingScopeID)
		s.placeTokenInScope(innerStarts[0].ID, childScopeID, at)
	} else {
		// Non-interrupting: leave enclosing scope running, spawn alongside.

		// Remove only THIS arm (one-shot).
		s.removeEventSubprocessArm(ea.EnclosingScopeID, ea.EventSubprocessNode)

		// Open a child scope for the event sub-process, parented to the enclosing scope.
		// NodeID = the event sub-process node ID (KindEventSubProcess).
		// This child scope runs alongside; when it drains, it is closed without affecting
		// the enclosing scope (tokensInScope for the enclosing scope is unaffected).
		childScopeID := s.openScope(ea.EventSubprocessNode, ea.EnclosingScopeID)
		s.placeTokenInScope(innerStarts[0].ID, childScopeID, at)
	}

	// Drive forward.
	driveCmds, err := drive(def, s, at, mode)
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
func handleSLAFired(def *model.ProcessDefinition, s *InstanceState, rec timerRecord, at time.Time, mode StepMode) (StepResult, error) {
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

	// Resolve the effective definition for the timer's scope so that SLA timers
	// inside a sub-process resolve nodes against the nested definition.
	tdefSLA, tdefSLAErr := defForScope(def, s, rec.ScopeID)
	if tdefSLAErr != nil {
		return StepResult{}, tdefSLAErr
	}

	// Resolve the SLA alternative-path flow.
	node, ok := tdefSLA.Node(rec.NodeID)
	if !ok {
		return StepResult{}, fmt.Errorf("workflow-engine: SLA breach: node %q not found in definition", rec.NodeID)
	}
	if node.SLAFlow == "" {
		return StepResult{}, fmt.Errorf("workflow-engine: SLA breach: node %q has no SLAFlow defined", rec.NodeID)
	}
	// Find the sequence flow with ID == node.SLAFlow.
	var slaTarget string
	for _, f := range tdefSLA.Flows {
		if f.ID == node.SLAFlow {
			slaTarget = f.Target
			break
		}
	}
	if slaTarget == "" {
		return StepResult{}, fmt.Errorf("workflow-engine: SLA breach: SLAFlow %q not found in definition flows for node %q", node.SLAFlow, rec.NodeID)
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
	driveCmds, err := drive(def, s, at, mode)
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
//     firedAt + every (new timer id from the counter), recording the new
//     timerRecord; the token does NOT move.
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

	// Resolve the effective definition for the timer's scope so that reminder
	// timers inside a sub-process resolve the node against the nested definition.
	tdefReminder, tdefReminderErr := defForScope(def, s, rec.ScopeID)
	if tdefReminderErr != nil {
		return StepResult{}, tdefReminderErr
	}

	// Resolve the node to get ReminderEvery and ReminderAction.
	node, ok := tdefReminder.Node(rec.NodeID)
	if !ok {
		return StepResult{}, fmt.Errorf("workflow-engine: reminder fired: node %q not found in definition", rec.NodeID)
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
		return StepResult{}, fmt.Errorf("workflow-engine: reminder node %q re-schedule: %w", node.ID, err)
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
		ScopeID:   rec.ScopeID,
	})

	// The token does NOT move — the task is still pending.
	return StepResult{State: *s, Commands: cmds}, nil
}

// reinvokeServiceAction re-emits an InvokeAction for tok's node, re-parks tok
// on the new command ID, and re-arms its boundary events. It is shared by the
// retry-timer path (handleRetryFired) and the incident-resolution path
// (ResolveIncident) so both use an identical re-invocation sequence.
//
// The caller is responsible for any pre-work specific to each path (e.g.
// removing the consumed timer record before calling this for the retry path).
func reinvokeServiceAction(def *model.ProcessDefinition, s *InstanceState, tok *Token, at time.Time) ([]Command, error) {
	tdef, err := defForScope(def, s, tok.ScopeID)
	if err != nil {
		return nil, fmt.Errorf("workflow-engine: reinvoke: %w", err)
	}
	node, ok := tdef.Node(tok.NodeID)
	if !ok {
		return nil, fmt.Errorf("workflow-engine: reinvoke: node %q not found", tok.NodeID)
	}

	// Re-emit InvokeAction — mirrors the KindServiceTask drive path exactly,
	// including the stable idempotency key (see serviceActionInput).
	cmdID := s.nextCommandID()
	cmds := []Command{InvokeAction{
		CommandID: cmdID,
		Name:      node.Action,
		Input:     serviceActionInput(s, node),
	}}
	tok.State = TokenWaitingCommand
	tok.AwaitCommand = cmdID

	// Re-arm boundary events (SLA timers, reminder timers) so they are active
	// for this invocation attempt.
	bndCmds, err := armBoundaries(tdef, s, tok.ID, node.ID, at)
	if err != nil {
		return cmds, err
	}
	return append(cmds, bndCmds...), nil
}

// handleRetryFired processes a TimerFired event for a TimerRetry timer. It is
// called from the TimerFired handler in Step after Task 5 parks a token on a
// retry timer following a retryable ActionFailed.
//
// Contract:
//   - If the parked token is gone (stale/duplicate retry fire), clean no-op.
//   - Otherwise: removes the consumed timer record, re-emits InvokeAction for
//     the node (mirroring the service-task drive path), re-parks the token on
//     the new command ID, and re-arms any boundary events (which Task 5 cancelled
//     on failure) so SLA and reminder timers are active for the retry attempt.
func handleRetryFired(def *model.ProcessDefinition, s *InstanceState, rec timerRecord, at time.Time, mode StepMode) (StepResult, error) {
	// Find the parked token. The token was parked with AwaitCommand == rec.TimerID
	// by Task 5 (ActionFailed retry path). If absent, the timer fired after the
	// instance advanced via another path (race / duplicate): clean no-op.
	tok := s.tokenAwaiting(rec.TimerID)
	if tok == nil {
		return StepResult{State: *s, Commands: nil}, nil
	}

	// Consume the timer record so a duplicate fire is a no-op.
	s.removeTimer(rec.TimerID)

	// Re-invoke the service action via the shared helper.
	cmds, err := reinvokeServiceAction(def, s, tok, at)
	if err != nil {
		return StepResult{}, fmt.Errorf("workflow-engine: retry fired: %w", err)
	}
	return StepResult{State: *s, Commands: cmds}, nil
}

// ── Compensation helpers ──────────────────────────────────────────────────────

// compensationRecordsForScope returns a read-only slice of CompensationRecords for
// the given scope. scopeID "" means the root scope (s.RootCompensations).
func compensationRecordsForScope(s *InstanceState, scopeID string) []CompensationRecord {
	if scopeID == "" {
		return s.RootCompensations
	}
	sc := s.scopeByID(scopeID)
	if sc == nil {
		return nil
	}
	return sc.Compensations
}

// stepCompensateRequested handles a CompensateRequested trigger. It sets the
// instance to StatusCompensating, then delegates to beginCompensation to cancel
// in-flight tokens, look up compensation records, and emit the first
// InvokeAction for the reverse-order walk (or finish immediately when there are
// no eligible records).
//
// The admin path always calls beginCompensation with zero finalStatus and empty
// finalErr, producing StatusTerminated with no FailInstance on a full rollback —
// identical to the prior behaviour.
func stepCompensateRequested(def *model.ProcessDefinition, s *InstanceState, t CompensateRequested, mode StepMode) (StepResult, error) {
	s.Status = StatusCompensating
	return beginCompensation(def, s, t.ToNode, 0, "", t.OccurredAt(), mode)
}

// beginCompensation is the shared initiator of a reverse-order compensation walk.
// It is called by stepCompensateRequested (admin path, finalStatus=0, finalErr="")
// and will be called by the error/cancel terminal paths (Task 2) with the
// appropriate outcome.
//
// s.Status must already be set to StatusCompensating by the caller.
//
// beginCompensation:
//  1. Cancels all in-flight tokens, timers, armed events, and boundaries (emitting
//     CancelTimer commands for each).
//  2. Looks up the root-scope (scopeID="") compensation records.
//  3. Validates toNode: if non-empty and not found in records, returns an error; if
//     toNode is the last (most-recently completed) record, calls stepCompensationFinish
//     immediately with the cancel commands prepended (nothing to compensate above it).
//  4. If there are no eligible records (empty records list), calls
//     stepCompensationFinish immediately, applying finalStatus/finalErr via the cursor.
//  5. Otherwise sets compensationCursor (stamping finalStatus and finalErr for the
//     terminal outcome) and emits the first compensation InvokeAction for the
//     most-recently completed record (reverse walk).
//
// The FinalStatus/FinalErr values are carried on the cursor across all advance steps
// so that stepCompensationFinish applies them when the walk completes.
func beginCompensation(def *model.ProcessDefinition, s *InstanceState, toNode string, finalStatus Status, finalErr string, at time.Time, mode StepMode) (StepResult, error) {
	// Cancel all in-flight tokens (interrupting normal execution).
	// Also emit CancelTimer for any outstanding timers, armed events, and boundaries.
	var preCmds []Command

	// Snapshot tokens to cancel (avoid mutating while iterating).
	tokensToCancel := make([]Token, len(s.Tokens))
	copy(tokensToCancel, s.Tokens)
	for _, tok := range tokensToCancel {
		// Cancel SLA/reminder timers for this token.
		for _, timerID := range s.cancelTimersByTaskToken(tok.AwaitCommand, "") {
			preCmds = append(preCmds, CancelTimer{TimerID: timerID})
		}
		// Cancel boundary arms.
		for _, timerID := range s.removeBoundaryArmsForHost(tok.ID) {
			preCmds = append(preCmds, CancelTimer{TimerID: timerID})
		}
		// Cancel event-gateway arms.
		if strings.HasPrefix(tok.AwaitCommand, "evtgw:") {
			for _, timerID := range s.removeArmedEventsForGateway(tok.ID) {
				preCmds = append(preCmds, CancelTimer{TimerID: timerID})
			}
		}
		tokPtr := s.tokenByID(tok.ID)
		if tokPtr != nil {
			s.consumeToken(tokPtr, at)
		}
	}
	// Cancel any remaining timers and event-subprocess arms.
	preCmds = append(preCmds, s.cancelAllTimers()...)
	preCmds = append(preCmds, s.cancelAllArmsAndBoundaries()...)

	// For now we compensate the root scope (top-level admin path).
	// Task 4 will extend this to scope-targeted compensation.
	const scopeID = ""
	records := compensationRecordsForScope(s, scopeID)

	// Determine the starting index: the last record whose NodeID != toNode.
	// We walk from the end of records backward; the first record (from the right)
	// that is NOT the ToNode is the starting point.
	//
	// Because records are stored in completion order (oldest first), the reverse
	// walk is: len(records)-1 down to 0.
	//
	// Find how many records are eligible (those AFTER ToNode in completion order).
	// If ToNode == "", all records are eligible.
	// If ToNode != "", we exclude the record whose NodeID == ToNode (it is the
	// rollback TARGET — we do not compensate it). All records recorded AFTER ToNode
	// (i.e. later in the slice) are eligible.
	startIndex := len(records) - 1
	if toNode != "" {
		// Find the index of toNode in the records (it's recorded in completion order).
		toNodeIdx := -1
		for i, r := range records {
			if r.NodeID == toNode {
				toNodeIdx = i
			}
		}
		if toNodeIdx >= 0 {
			// Only records AFTER toNodeIdx (i.e. indices > toNodeIdx) are eligible.
			// The first to emit is the last eligible record.
			if toNodeIdx >= len(records)-1 {
				// ToNode was the last completed node — nothing to compensate above it.
				// Stamp the cursor so stepCompensationFinish applies the outcome; since
				// toNode != "", stepCompensationFinish takes the partial-rollback branch
				// (resume at toNode) regardless of FinalStatus/FinalErr — which is correct
				// for the admin path (outcome fields are zero).
				s.Compensating = compensationCursor{FinalStatus: finalStatus, FinalErr: finalErr}
				finishRes, finishErr := stepCompensationFinish(def, s, toNode, at, mode)
				if finishErr != nil {
					return StepResult{}, finishErr
				}
				finishRes.Commands = append(preCmds, finishRes.Commands...)
				return finishRes, nil
			}
			// startIndex = the last eligible record = len(records)-1
			// (all records after toNodeIdx — no change needed; startIndex already set).
		} else {
			// toNode was specified but not found in the compensation records.
			// Return a descriptive error so that an admin typo is surfaced rather
			// than silently rolling back everything.
			return StepResult{}, fmt.Errorf("workflow-engine: compensation target node %q not found in scope records", toNode)
		}
	}

	if startIndex < 0 {
		// No records at all — apply the terminal outcome immediately.
		// Stamp the cursor so stepCompensationFinish reads the outcome fields.
		s.Compensating = compensationCursor{FinalStatus: finalStatus, FinalErr: finalErr}
		finishRes, finishErr := stepCompensationFinish(def, s, toNode, at, mode)
		if finishErr != nil {
			return StepResult{}, finishErr
		}
		finishRes.Commands = append(preCmds, finishRes.Commands...)
		return finishRes, nil
	}

	// Emit the first compensation InvokeAction (record at startIndex).
	rec := records[startIndex]
	cmdID := s.nextCommandID()
	s.Compensating = compensationCursor{
		ScopeID:     scopeID,
		ToNode:      toNode,
		NextIndex:   startIndex,
		ActiveCmdID: cmdID,
		FinalStatus: finalStatus,
		FinalErr:    finalErr,
	}
	cmd := InvokeAction{
		CommandID: cmdID,
		Name:      rec.Action,
		Input:     copyVars(rec.Input),
	}
	cmds := append(preCmds, cmd)
	return StepResult{State: *s, Commands: cmds}, nil
}

// stepCompensationAdvance advances the compensation cursor after a compensation
// InvokeAction completes (ActionCompleted with cursor.ActiveCmdID). It emits the
// next InvokeAction in reverse order, or finalises compensation if the walk is done.
func stepCompensationAdvance(def *model.ProcessDefinition, s *InstanceState, at time.Time, mode StepMode) (StepResult, error) {
	cur := s.Compensating
	records := compensationRecordsForScope(s, cur.ScopeID)

	// Advance: the record we just completed is at cur.NextIndex. Move to the
	// previous record (next in reverse order). nextIdx = cur.NextIndex - 1.
	nextIdx := cur.NextIndex - 1

	// Determine the stop boundary: the index of ToNode (exclusive, i.e. we stop
	// BEFORE emitting that index's compensation).
	toNodeIdx := -1
	if cur.ToNode != "" {
		for i, r := range records {
			if r.NodeID == cur.ToNode {
				toNodeIdx = i
			}
		}
	}

	// Check if next record is within the eligible range.
	// Eligible: nextIdx >= 0 AND nextIdx > toNodeIdx (i.e. the record is AFTER ToNode).
	if nextIdx < 0 || nextIdx <= toNodeIdx {
		// Walk complete: either exhausted all records, or reached ToNode boundary.
		return stepCompensationFinish(def, s, cur.ToNode, at, mode)
	}

	// Emit the next compensation action.
	rec := records[nextIdx]
	cmdID := s.nextCommandID()
	s.Compensating = compensationCursor{
		ScopeID:     cur.ScopeID,
		ToNode:      cur.ToNode,
		NextIndex:   nextIdx,
		ActiveCmdID: cmdID,
		FinalStatus: cur.FinalStatus,
		FinalErr:    cur.FinalErr,
	}
	cmd := InvokeAction{
		CommandID: cmdID,
		Name:      rec.Action,
		Input:     copyVars(rec.Input),
	}
	return StepResult{State: *s, Commands: []Command{cmd}}, nil
}

// stepCompensationFinish finalises the compensation walk:
//   - If toNode != "": place a token at toNode, set Status = StatusRunning, and
//     drive forward (resuming execution from the rollback target).
//   - If toNode == "": all records have been compensated; set Status =
//     StatusTerminated (full rollback — no resume point).
func stepCompensationFinish(def *model.ProcessDefinition, s *InstanceState, toNode string, at time.Time, mode StepMode) (StepResult, error) {
	// Save outcome fields AND scope BEFORE clearing the cursor.
	finalStatus := s.Compensating.FinalStatus
	finalErr := s.Compensating.FinalErr
	scopeID := s.Compensating.ScopeID
	// Clear the cursor — compensation walk is done.
	s.Compensating = compensationCursor{}

	if toNode == "" {
		// Full rollback: no resume point → apply the cursor's terminal outcome.
		// Zero FinalStatus (== StatusRunning, the iota-0 constant) means UNSET;
		// map it to StatusTerminated. Safe: full-rollback finish is always
		// terminal, so no caller of beginCompensation ever wants a non-terminal
		// outcome here. The admin path (CompensateRequested) leaves FinalStatus
		// zero; error/cancel paths set it explicitly (StatusFailed / StatusTerminated).
		if finalStatus == 0 {
			finalStatus = StatusTerminated
		}
		s.Status = finalStatus
		ended := at
		s.EndedAt = &ended

		// Clear the compensation records for the walk's scope so that a re-delivered
		// CancelRequested (or any other terminal trigger) on an already-terminal instance
		// cannot re-enter the walk and double-compensate money-moving actions.
		// beginCompensation today always uses scopeID="" (root scope); future scope-targeted
		// walks (ADR-0035) will set a non-empty scopeID, which the else branch handles.
		if scopeID == "" {
			s.RootCompensations = nil
		} else {
			if sc := s.scopeByID(scopeID); sc != nil {
				sc.Compensations = nil
			}
		}

		var cmds []Command
		if finalErr != "" {
			cmds = append(cmds, FailInstance{Err: finalErr})
		}
		cmds = append(cmds, s.cancelAllTimers()...)
		cmds = append(cmds, s.cancelAllArmsAndBoundaries()...)
		return StepResult{State: *s, Commands: cmds}, nil
	}

	// Partial rollback: resume at toNode.
	s.Status = StatusRunning
	// Place a new token at toNode and drive forward.
	s.placeToken(toNode, at)
	driveCmds, err := drive(def, s, at, mode)
	if err != nil {
		return StepResult{}, err
	}
	return StepResult{State: *s, Commands: driveCmds}, nil
}

// ---- retry helpers ----

// effectiveRetryPolicy returns the retry policy to apply for the given node and
// step options, plus a boolean indicating whether a policy is in effect.
// Precedence: node-level policy > StepOptions.DefaultRetryPolicy > none.
// The returned policy has been normalized via [model.RetryPolicy.Normalize].
func effectiveRetryPolicy(node model.Node, opt StepOptions) (model.RetryPolicy, bool) {
	switch {
	case node.RetryPolicy != nil:
		return node.RetryPolicy.Normalize(), true
	case opt.DefaultRetryPolicy != nil:
		return opt.DefaultRetryPolicy.Normalize(), true
	default:
		return model.RetryPolicy{}, false
	}
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

// serviceActionInput builds the Input map for a node's primary ServiceAction
// invocation. It copies the instance variables and stamps a stable,
// attempt-independent idempotency key ("<instanceID>:<nodeID>") so action
// authors can dedup external side effects across retries.
//
// v1 scope: only the primary service-task action carries this key. SLA,
// reminder, and compensation actions do NOT — those are separate fire-once
// operations on the same node; stamping instanceID:nodeID on them would
// collide with the primary action's key and could cause an external system to
// wrongly dedup distinct operations.
func serviceActionInput(s *InstanceState, node model.Node) map[string]any {
	in := copyVars(s.Variables)
	if in == nil {
		in = map[string]any{}
	}
	in["_idempotencyKey"] = s.InstanceID + ":" + node.ID
	return in
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
	// Deep-copy Incidents: Incident is a flat value struct (all fields are plain
	// scalars — no pointers or maps), so an append-copy of the slice is sufficient
	// to ensure mutations to the clone's Incidents do not affect the original.
	if len(st.Incidents) > 0 {
		s.Incidents = append([]Incident(nil), st.Incidents...)
	}
	return s
}

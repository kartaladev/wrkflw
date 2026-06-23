package engine

import (
	"fmt"
	"time"

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

		// Per-node cancel handlers (ADR-0035): collect InvokeCancelAction for each
		// active token whose node carries a non-empty CancelHandler.
		// This MUST happen before the compensation/immediate branch because
		// beginCompensation (and the immediate path) clear s.Tokens.
		// Tokens are iterated in slice order for determinism.
		var nodeCancelCmds []Command
		for i := range s.Tokens {
			tok := &s.Tokens[i]
			tdef, derr := defForScope(def, &s, tok.ScopeID)
			if derr != nil {
				// Defensive: skip on scope resolution error; cancel must not fail.
				continue
			}
			if node, ok := tdef.Node(tok.NodeID); ok && node.CancelHandler != "" {
				nodeCancelCmds = append(nodeCancelCmds, InvokeCancelAction{Name: node.CancelHandler, Input: copyVars(s.Variables)})
			}
		}

		// If a compensation walk is ALREADY in flight, never start a second one: a
		// second beginCompensation re-walks records that are still mid-consumption and
		// re-emits the in-flight compensation, double-running money-moving actions.
		if s.Status == StatusCompensating && s.Compensating.ActiveCmdID != "" {
			if s.Compensating.ResumeNode != "" {
				// ADR-0039 (B1): a compensation THROW walk is in flight; it would
				// otherwise RESUME past the throw, so the instance would keep running.
				// Defer this cancel — record the intent and let the throw walk finish;
				// stepCompensationFinish then runs a full cancel over the REMAINING
				// records (the throw's target is deleted by then) and terminates instead
				// of resuming.
				s.PendingCancel = true
				cmds := append(append([]Command(nil), cancelActionCmds...), nodeCancelCmds...)
				return StepResult{State: s, Commands: cmds}, nil
			}
			// A TERMINAL (cancel/error/full-rollback) or admin PARTIAL-rollback walk is
			// already in flight (ResumeNode == ""). The instance is already being
			// compensated; a redundant cancel must NOT re-enter beginCompensation (which
			// would re-emit the in-flight record → double-compensation). No-op: the
			// in-flight walk drives the instance to its terminal (or, for an admin partial
			// rollback, resuming) end on its own. The records already fired their cancel
			// actions when the in-flight walk began, so none are re-emitted here.
			//
			// Limitation: a cancel racing an admin PARTIAL rollback is therefore dropped
			// (the partial walk resumes at its ToNode) — a rare admin-debug edge accepted
			// in exchange for the no-double-compensation guarantee.
			return StepResult{State: s, Commands: nil}, nil
		}

		if len(s.RootCompensations) > 0 || len(s.ArchivedCompensations) > 0 {
			// Compensation walk before termination (ADR-0034).
			// beginCompensation consolidates ArchivedCompensations into RootCompensations
			// first (ADR-0039), then clears tokens/timers/arms and emits the first
			// compensation InvokeAction, setting the cursor with FinalStatus=Terminated
			// and FinalErr="cancelled". stepCompensationFinish will emit
			// FailInstance{"cancelled"} at walk end.
			s.Status = StatusCompensating
			res, err := beginCompensation(def, &s, "", StatusTerminated, "cancelled", t.OccurredAt(), opt.Mode)
			if err != nil {
				return StepResult{}, err
			}
			// Ordering: [def.CancelActions…, per-node CancelHandlers…, compensation walk…]
			res.Commands = append(append(cancelActionCmds, nodeCancelCmds...), res.Commands...)
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
		// Ordering: [def.CancelActions…, per-node CancelHandlers…, FailInstance, timers, arms].
		// Start from a fresh slice so we never alias cancelActionCmds' backing array
		// (matches the compensation branch's append(append(...)) idiom).
		cmds := append(append([]Command(nil), cancelActionCmds...), nodeCancelCmds...)
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

		// Dispatch through the nodeStrategy registry for migrated kinds.
		// Kinds not yet in the registry fall through to the switch below.
		if strat, ok := nodeStrategies[node.Kind]; ok {
			c := &stepCtx{def: def, tdef: tdef, s: s, at: at, mode: mode}
			produced, stratErr := strat.enter(c, tok, node)
			if stratErr != nil {
				return nil, stratErr
			}
			cmds = append(cmds, produced...)
			// Preserve Micro-mode semantics: a strategy that parks the token
			// (tok.State != TokenActive) counts as a stop, identical to the
			// old `stopped = true` in the case arm.
			stopped = tok.State != TokenActive
		} else {
			// Unhandled node kinds: park the token so the loop terminates rather
			// than spinning. These are intentionally not in the registry:
			// KindTerminateEndEvent, KindBusinessRuleTask, KindReceiveTask,
			// KindSendTask, KindBoundaryEvent, KindEventSubProcess, KindUnspecified.
			tok.State = TokenWaitingCommand
			stopped = true // token parked: Micro stops here
		} // end else (non-registry kinds)

		// Micro-mode: stop after the first park or terminal event. Auto-advancing
		// cases (StartEvent, gateway routing that produces new active tokens) leave
		// stopped=false so the loop continues to the next token within this Step call.
		if mode == Micro && stopped {
			break
		}
	}
	return cmds, nil
}


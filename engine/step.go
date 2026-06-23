package engine

import (
	"fmt"
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
			switch node.Kind {
			case model.KindStartEvent:
				s.moveAlongSingleFlow(tdef, tok, at)

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
						s.archiveCompensations(currentScopeID)
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
			if node.CompensateRef != "" {
				// Compensation throw intermediate event (ADR-0039, Phase 3).
				// Runs the archived compensation records for the referenced sub-process
				// node in reverse order, then resumes execution past the throw node.
				// This is a localized walk — it does NOT call beginCompensation
				// (which cancels ALL tokens and is designed for full/partial rollbacks).
				ref := node.CompensateRef
				records := s.ArchivedCompensations[ref]
				// Determine the resume node: the throw's single outgoing successor.
				resumeNode := ""
				if out := tdef.Outgoing(node.ID); len(out) > 0 {
					resumeNode = out[0].Target
				}
				if len(records) == 0 || resumeNode == "" {
					// No archived records (never ran, already compensated by a prior
					// throw, or the sub-process had no compensable activities), OR the
					// throw has no outgoing flow. A throw with no successor must NOT
					// start a walk: stepCompensationFinish would see ResumeNode=="" and
					// take the terminal branch, wrongly terminating the instance. Validate
					// forbids a dead-end throw (ErrDeadEnd); this guards Step defensively
					// regardless. Auto-advance — fire-and-forget, no InvokeAction emitted.
					s.moveAlongSingleFlow(tdef, tok, at)
					// stopped remains false: auto-advance.
				} else {
					// Start the throw compensation walk (resumeNode is non-empty here).
					// Remember the throw token's scope for correct placeTokenInScope on finish.
					tokScope := tok.ScopeID
					// Consume the throw token now (finish will place a fresh token at resumeNode).
					s.consumeToken(tok, at)
					// Set instance into compensation mode and stamp the cursor.
					s.Status = StatusCompensating
					cmdID := s.nextCommandID()
					s.Compensating = compensationCursor{
						ArchiveKey:  ref,
						ResumeNode:  resumeNode,
						ResumeScope: tokScope,
						NextIndex:   len(records) - 1,
						ActiveCmdID: cmdID,
					}
					cmds = append(cmds, InvokeAction{
						CommandID: cmdID,
						Name:      records[len(records)-1].Action,
						Input:     copyVars(records[len(records)-1].Input),
					})
					stopped = true // walk started; Micro stops here
				}
			} else if node.SignalName != "" {
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
				// Non-signal, non-compensation intermediate throw: park for future plans
				// (e.g. message throw, error throw). Parking avoids an infinite drive loop.
				tok.State = TokenWaitingCommand
				stopped = true // token parked: Micro stops here
			}

		default:
			// Node kinds beyond linear flow arrive in later plans; park the
			// token so the loop terminates rather than spinning.
			tok.State = TokenWaitingCommand
			stopped = true // token parked: Micro stops here
		}
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


package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/humantask"
)

// ErrNoManualStart is returned when a StartInstance with an empty StartNodeID is
// applied to a definition that has no manual (trigger-less, caller-driven) start
// event — i.e. every start event carries a message, signal, or timer trigger
// (ADR-0121). A manual start maps to BPMN's "none start event".
var ErrNoManualStart = errors.New("workflow-engine: definition has no manual start event")

// handleStartInstance processes a StartInstance trigger: initialises instance
// state, places the start token, arms top-level event sub-processes, and drives
// forward from the start node.
func handleStartInstance(ctx context.Context, def *model.ProcessDefinition, s *InstanceState, t StartInstance, opt StepOptions) (StepResult, error) {
	s.Status = StatusRunning
	s.StartedAt = t.OccurredAt()
	s.DefID = def.ID
	s.DefVersion = def.Version
	mergeVars(s, t.Vars)
	s.StartVariables = copyVars(s.Variables)
	startID := t.StartNodeID
	if startID == "" {
		n, err := resolveManualStart(def)
		if err != nil {
			return StepResult{}, err
		}
		startID = n
	}
	s.placeToken(startID, t.OccurredAt())
	// Arm any top-level event sub-processes (root scope, enclosingScopeID == "").
	espCmds, espErr := armEventTriggeredSubprocesses(def, s, "", t.OccurredAt(), resolveEvaluator(opt))
	if espErr != nil {
		return StepResult{}, espErr
	}
	// Drive forward from the start node; prepend any esp ScheduleTimer commands.
	driveCmdsStart, driveErrStart := drive(ctx, def, s, t.OccurredAt(), opt.Mode, resolveEvaluator(opt))
	if driveErrStart != nil {
		return StepResult{}, driveErrStart
	}
	return StepResult{State: *s, Commands: append(espCmds, driveCmdsStart...)}, nil
}

// resolveManualStart returns the id of the definition's single manual
// (trigger-less, caller-driven) start event — a StartEvent with no message name,
// signal name, or timer, i.e. BPMN's "none start event" — or ErrNoManualStart if
// the definition has none (e.g. every start is event-triggered). When multiple
// manual starts exist (a definition-authoring choice outside this seam's remit to
// validate), the first one found in def.StartNodes() order wins.
func resolveManualStart(def *model.ProcessDefinition) (string, error) {
	for _, s := range def.StartNodes() {
		se, ok := s.(event.StartEvent)
		if !ok {
			continue
		}
		if se.MessageName == "" && se.SignalName == "" && se.Timer.IsZero() {
			return se.ID(), nil
		}
	}
	return "", ErrNoManualStart
}

// handleActionCompleted processes an ActionCompleted trigger: resumes the token
// waiting for the completed command, records compensation if applicable, merges
// output variables, and drives forward.
func handleActionCompleted(ctx context.Context, def *model.ProcessDefinition, s *InstanceState, t ActionCompleted, opt StepOptions) (StepResult, error) {
	// If the engine is in compensation mode AND this ActionCompleted corresponds
	// to the in-flight compensation action (cursor.ActiveCmdID), advance the
	// compensation cursor rather than doing normal token routing. This keeps
	// compensation sequencing deterministic and observable (one action at a time).
	if s.Status == StatusCompensating && s.Compensating.ActiveCmdID == t.CommandID {
		return stepCompensationAdvance(ctx, def, s, t.OccurredAt(), opt.Mode, resolveEvaluator(opt))
	}

	tok := s.tokenAwaiting(t.CommandID)
	if tok == nil {
		// Unreachable for a terminal instance — dispatch's structural guard
		// returned before this handler was entered (ADR-0165) — so reaching here
		// means a non-terminal instance with no matching token, which is a genuine
		// caller error.
		return StepResult{}, fmt.Errorf("%w: %q", ErrTokenNotFound, t.CommandID)
	}
	// Cancel any boundary arms on this host token before advancing.
	preCmds := appendCancelTimers(nil, s.removeBoundaryArmsForHost(tok.ID))
	// Resolve the effective definition for the token's scope so we can look up
	// the node and check for a CompensateAction BEFORE merging output.
	tdef, err := defForScope(def, s, tok.ScopeID)
	if err != nil {
		return StepResult{}, err
	}
	// Record compensation BEFORE merging the output: the snapshot captures the
	// instance variables as they existed when the activity was invoked.
	if node, ok := tdef.Node(tok.NodeID); ok {
		if compAction := compensateActionOf(node); compAction != "" {
			s.recordCompensation(tok.ScopeID, node.ID(), compAction, t.OccurredAt(), copyVars(s.Variables))
		}
	}
	mergeVars(s, t.Output)
	tok.AwaitCommand = ""
	// Advance the token past the completed ServiceTask so drive sees it at
	// the next node, not re-firing the action. Use the token's scope definition
	// so inner-scope tokens resolve flows against the nested definition.
	cmds, err := resumeAndDrive(ctx, def, tdef, s, tok, t.OccurredAt(), opt, preCmds)
	if err != nil {
		return StepResult{}, err
	}
	return StepResult{State: *s, Commands: cmds}, nil
}

// handleCancelRequested processes a CancelRequested trigger: terminates the
// instance, running compensation first when records exist.
func handleCancelRequested(ctx context.Context, def *model.ProcessDefinition, s *InstanceState, t CancelRequested, opt StepOptions) (StepResult, error) {
	// Admin trigger: terminate the instance, optionally running compensation first.
	// Emit InvokeCancelAction (fire-and-forget) for each entry in def.CancelActions
	// regardless of whether compensation records exist (ADR-0028 unchanged).
	var cancelActionCmds []Command
	for _, name := range def.CancelActions {
		cancelActionCmds = append(cancelActionCmds, InvokeCancelAction{Name: name, Input: copyVars(s.Variables)})
	}

	// Per-node cancel handlers (ADR-0035): collect InvokeCancelAction for each
	// active token whose node carries a non-empty CancelAction.
	// This MUST happen before the compensation/immediate branch because
	// beginCompensation (and the immediate path) clear s.Tokens.
	// Tokens are iterated in slice order for determinism.
	var nodeCancelCmds []Command
	for i := range s.Tokens {
		tok := &s.Tokens[i]
		tdef, derr := defForScope(def, s, tok.ScopeID)
		if derr != nil {
			// Defensive: skip on scope resolution error; cancel must not fail.
			slog.DebugContext(ctx, "cancel: scope resolution error",
				"instance_id", s.InstanceID,
				"token_id", tok.ID,
				"scope_id", tok.ScopeID,
				"error", derr,
			)
			continue
		}
		if node, ok := tdef.Node(tok.NodeID); ok {
			if ch := cancelActionOf(node); ch != "" {
				nodeCancelCmds = append(nodeCancelCmds, InvokeCancelAction{Name: ch, Input: copyVars(s.Variables)})
			}
		}
	}

	// If a compensation walk is ALREADY in flight, never start a second one: a
	// second beginCompensation re-walks records that are still mid-consumption and
	// re-emits the in-flight compensation, double-running money-moving actions.
	if s.Status == StatusCompensating && s.Compensating.ActiveCmdID != "" {
		// Checked directly on the fields (not via walkMode()) because this site's
		// defer-if-resuming semantics differ from walkMode()'s ToNode-precedence
		// ordering: a cursor with BOTH ResumeNode and ReverseNode set must still
		// defer here, whereas walkMode() would classify it as walkPartial (via
		// ToNode) or otherwise not match the resuming case. That combination is
		// unreachable via any constructor today, but this predicate restores the
		// exact pre-refactor behaviour.
		if s.Compensating.ResumeNode != "" || s.Compensating.ReverseNode != "" {
			// A RESUMING walk is in flight — either a compensation THROW walk
			// (ResumeNode set, ADR-0039 B1) or a full-REVERSE walk (ReverseNode
			// set, ADR-0109 Fork B). Both would otherwise RESUME (Running) at
			// finish, so the instance would keep running and the caller who
			// cancelled would be left with a live instance. Defer this cancel —
			// record the intent and let the in-flight walk finish; applyFinish then
			// consumes PendingCancel and runs a full cancel over the REMAINING
			// records (the throw's archive / the reverse's records are cleared by
			// then) and terminates instead of resuming.
			//
			// The outcome is stamped EXPLICITLY, not left to applyFinish's
			// zero-value default. Since ADR-0170 the same two fields also carry a
			// deferred unhandled error's outcome, so a cancel arriving after one
			// found them already set to StatusFailed + that error's code and
			// inherited it — measured, the finish produced status=failed with
			// FailInstance{Err:"boom"} for an instance whose last operator action
			// was a cancel. Writing both fields here makes this deferral
			// last-writer-wins in the same way ADR-0170 documents for successive
			// errors. applyFinish's zero-value default now covers only a state
			// PERSISTED before this change (PendingCancel true, outcome unset).
			s.PendingCancel = true
			s.PendingFinalStatus = StatusTerminated
			s.PendingFinalErr = "cancelled"
			cmds := append(append([]Command(nil), cancelActionCmds...), nodeCancelCmds...)
			return StepResult{State: *s, Commands: cmds}, nil
		}
		// A TERMINAL (cancel/error/full-rollback) or admin PARTIAL-rollback
		// (ToNode set) walk is already in flight. The instance is already being
		// compensated; a redundant cancel must NOT re-enter beginCompensation
		// (which would re-emit the in-flight record → double-compensation).
		// No-op: the in-flight walk drives the instance to its terminal (or, for
		// an admin partial rollback, resuming) end on its own. The records
		// already fired their cancel actions when the in-flight walk began, so
		// none are re-emitted here.
		//
		// Limitation: a cancel racing an admin PARTIAL rollback is therefore dropped
		// (the partial walk resumes at its ToNode) — a rare admin-debug edge accepted
		// in exchange for the no-double-compensation guarantee.
		return StepResult{State: *s, Commands: nil}, nil
	}

	// The instance is dying, so harvest what its open scopes still hold BEFORE asking
	// whether there is anything to compensate — otherwise a record for an activity that
	// completed inside a still-open sub-process is invisible here, the cancel terminates
	// immediately, and the record can never be compensated (ADR-0174). Measured on
	// `main`: a cancel delivered to an instance parked inside a sub-process holding
	// `undo-inner` dispatched no compensation action at all.
	//
	// Placed AFTER the in-flight-walk guard above, which must keep winning (ADR-0170).
	s.harvestOpenScopeCompensations()

	if len(s.RootCompensations) > 0 || len(s.ArchivedCompensations) > 0 {
		// Compensation walk before termination (ADR-0034). The harvest above is what
		// makes this predicate correct for an open scope's records.
		// beginCompensation consolidates ArchivedCompensations into RootCompensations
		// first (ADR-0039), then clears tokens/timers/arms and emits the first
		// compensation InvokeAction, setting the cursor with FinalStatus=Terminated
		// and FinalErr="cancelled". stepCompensationFinish will emit
		// FailInstance{"cancelled"} at walk end.
		s.Status = StatusCompensating
		res, err := beginCompensation(ctx, def, s, t.OccurredAt(), opt.Mode, resolveEvaluator(opt), compensationOutcome{CloseKind: CloseKindInstanceCancelled, FinalStatus: StatusTerminated, FinalErr: "cancelled"})
		if err != nil {
			return StepResult{}, err
		}
		// Ordering: [def.CancelActions…, per-node CancelActions…, compensation walk…].
		// The explicit task-cancel prepend ADR-0088 documented here is gone. Since
		// ADR-0163, beginCompensation cancels each token's open task inside its own
		// preCmds, so for any state this engine version produces the sweep that used
		// to sit here has nothing left to find — the same UpdateTask commands are
		// still emitted, one call site earlier.
		//
		// That is not an absolute: an instance persisted before ADR-0163 can carry a
		// leaked task (open, with no live token), which no per-token teardown
		// reaches. Dropping the sweep stays safe for those, because
		// stepCompensationFinish runs cancelOpenTasks over whatever remains at walk
		// end, before the instance reaches its terminal state — a legacy leak is
		// reconciled there rather than lost.
		res.Commands = append(append(cancelActionCmds, nodeCancelCmds...), res.Commands...)
		return res, nil
	}

	// No compensation records: immediate termination (unchanged behaviour).
	// The token drop runs BEFORE endInstance so its removeOrphanedIncidents sweep
	// sees the empty token set and retires the incidents those tokens carried
	// (ADR-0164).
	for i := range s.Tokens {
		tok := &s.Tokens[i]
		s.closeVisitAs(tok.ID, tok.NodeID, t.OccurredAt(), CloseKindInstanceCancelled)
	}
	s.Tokens = nil
	// Ordering: [def.CancelActions…, per-node CancelActions…, task cancels…,
	// FailInstance, timers, arms].
	// Start from a fresh slice so we never alias cancelActionCmds' backing array
	// (matches the compensation branch's append(append(...)) idiom).
	cmds := append(append([]Command(nil), cancelActionCmds...), nodeCancelCmds...)
	// endInstance's cancelOpenTasks is the ONLY task sweep on this path: the branch
	// drops its tokens via closeVisitAs + s.Tokens = nil and never routes through
	// cancelTokenWaits, so a parked task would otherwise stay open in the TaskStore
	// (ADR-0088).
	cmds = append(cmds, s.endInstance(StatusTerminated, t.OccurredAt(), FailInstance{Err: "cancelled"})...)
	return StepResult{State: *s, Commands: cmds}, nil
}

// handleCompensateRequested processes a CompensateRequested trigger: initiates
// the admin/debug reverse-order compensation walk.
func handleCompensateRequested(ctx context.Context, def *model.ProcessDefinition, s *InstanceState, t CompensateRequested, opt StepOptions) (StepResult, error) {
	// Admin/debug reverse-order compensation trigger.
	// 1. Set status to StatusCompensating.
	// 2. Build the compensation cursor pointing at the first (most-recent) record
	//    to emit (the last index in the relevant slice that is AFTER ToNode).
	// 3. Emit the first InvokeAction and record the cursor.
	return stepCompensateRequested(ctx, def, s, t, opt.Mode, resolveEvaluator(opt))
}

// handleActionFailed processes an ActionFailed trigger: handles retry scheduling,
// recovery flows, boundary error propagation, and compensation advancement.
func handleActionFailed(ctx context.Context, def *model.ProcessDefinition, s *InstanceState, t ActionFailed, opt StepOptions) (StepResult, error) {
	// Best-effort compensation: if the engine is compensating and the failed
	// command is the active compensation action, skip that record and advance
	// the walk rather than re-entering propagateError/retry (ADR-0034 §2.5).
	if s.Status == StatusCompensating && s.Compensating.ActiveCmdID == t.CommandID {
		return stepCompensationAdvance(ctx, def, s, t.OccurredAt(), opt.Mode, resolveEvaluator(opt))
	}

	tok := s.tokenAwaiting(t.CommandID)
	if tok == nil {
		// Unreachable for a terminal instance — dispatch's structural guard
		// returned before this handler was entered (ADR-0165; the route it closes
		// is on ActionFailed.terminalPolicy). So reaching here means a
		// non-terminal instance with no matching token, a genuine caller error.
		return StepResult{}, fmt.Errorf("%w: %q", ErrTokenNotFound, t.CommandID)
	}
	// Cancel any boundary arms on this host token before propagating.
	// On the unhandled path cancelAllArmsAndBoundaries covers the rest;
	// on the caught path we clear them before routing to the recovery flow.
	preCmds := appendCancelTimers(nil, s.removeBoundaryArmsForHost(tok.ID))
	// Retry interception: when the node (or the default policy) carries a retry
	// policy and the failure is non-terminal, schedule a TimerRetry instead of
	// propagating the error immediately.
	tdef, err := defForScope(def, s, tok.ScopeID)
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
			timerID := s.nextTimerID()
			// Retry backoff is a concrete one-shot delay: carry it as an
			// AfterDuration trigger so the scheduler fires once after `delay`.
			retryCmds := []Command{ScheduleTimer{
				TimerID: timerID,
				Token:   tok.ID,
				Trigger: schedule.AfterDuration(delay),
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
			tok.State = TokenWaiting
			tok.AwaitCommand = timerID
			return StepResult{State: *s, Commands: append(preCmds, retryCmds...)}, nil
		}
		// Terminal exhaustion: precedence is (1) catch-flow → (2) error
		// boundary → (3) incident.
		if rf := recoveryFlowOf(node); rf != "" {
			// (1) Catch-flow: inject error context onto instance variables and
			// route the failing token down RecoveryFlow.
			if s.Variables == nil {
				s.Variables = map[string]any{}
			}
			s.Variables["_errorMessage"] = t.Err
			// Total executions: initial attempt plus all retries.
			s.Variables["_errorAttempts"] = tok.RetryAttempts + 1
			// ErrorCode on activity nodes is not part of the new model; the
			// _error variable injection is skipped (no ErrorCode field on activities).
			// Resolve the RecoveryFlow target (mirror the DeadlineFlow routing in
			// handleDeadlineFired: scan the scope def's flows for the flow ID).
			var target string
			for _, f := range tdef.Flows {
				if f.ID == rf {
					target = f.Target
					break
				}
			}
			if target == "" {
				return StepResult{}, fmt.Errorf("workflow-engine: retry exhaustion: RecoveryFlow %q not found for node %q", rf, node.ID())
			}
			tok.RetryAttempts = 0
			tok.RetryStartedAt = time.Time{}
			tok.AwaitCommand = ""
			tok.State = TokenActive
			s.moveTokenToTarget(tok, target, t.OccurredAt())
			driveCmds, err := drive(ctx, def, s, t.OccurredAt(), opt.Mode, resolveEvaluator(opt))
			if err != nil {
				return StepResult{}, err
			}
			return StepResult{State: *s, Commands: append(preCmds, driveCmds...)}, nil
		}
		// (2)+(3): no catch-flow → let propagateError catch the error via a
		// boundary handler; if none is found, raise an incident (the
		// raiseIncident policy) instead of failing the instance.
		errCmds, err := propagateError(ctx, def, s, tok.ScopeID, tok.NodeID, tok.ID, t.Err, t.Cause, t.OccurredAt(), opt.Mode, resolveEvaluator(opt), raiseIncident)
		if err != nil {
			return StepResult{}, err
		}
		return StepResult{State: *s, Commands: append(preCmds, errCmds...)}, nil
	}
	// Route through propagateError: if a boundary error handler is found in
	// the scope chain (direct-attachment or enclosing-scope), the error is
	// caught and execution continues on the recovery path (no FailInstance).
	// If no handler is found, propagateError sets StatusFailed, emits
	// FailInstance, and performs terminal cleanup — preserving existing
	// behavior for root-level service tasks with no handler.
	// Pass tok.ID so the direct-attachment branch consumes THIS specific
	// token, not the first token found at the same NodeID+ScopeID.
	// Pass t.Cause so ErrorCheck closures receive the original typed error.
	errCmds, err := propagateError(ctx, def, s, tok.ScopeID, tok.NodeID, tok.ID, t.Err, t.Cause, t.OccurredAt(), opt.Mode, resolveEvaluator(opt), failFast)
	if err != nil {
		return StepResult{}, err
	}
	return StepResult{State: *s, Commands: append(preCmds, errCmds...)}, nil
}

// handleHumanClaimed processes a HumanClaimed trigger: updates the task state
// to Claimed and emits an UpdateTask command.
//
// Errors with [ErrTaskNotOpen] when the task is already Completed or Cancelled.
// That is the task-lifetime key, independent of the instance-status key
// dispatch enforces (ADR-0165 Decision 6): ADR-0163 closes a task with the token
// it is attached to — an interrupting boundary or a breached deadline — while the
// instance carries on down the alternative path, so a closed task on a RUNNING
// instance is reachable and the status guard cannot see it.
func handleHumanClaimed(s *InstanceState, t HumanClaimed) (StepResult, error) {
	task := s.TaskByID(t.TaskID)
	if task == nil {
		return StepResult{}, fmt.Errorf("%w: %q", ErrTokenNotFound, t.TaskID)
	}
	if !task.IsOpen() {
		return StepResult{}, fmt.Errorf("%w: task %q", ErrTaskNotOpen, t.TaskID)
	}
	// Record the full claiming actor and the claim time (ADR-0147): the instance
	// view renders who claimed and when, not just an ID.
	task.Claim = &humantask.Claim{Actor: t.Actor, At: t.OccurredAt()}
	task.State = humantask.Claimed
	// Clone before the record escapes to a consumer-supplied TaskStore: task
	// points into s.Tasks, which is committed as instance state (ADR-0163).
	return StepResult{State: *s, Commands: []Command{UpdateTask{Task: task.Clone()}}}, nil
}

// handleHumanCandidatesResolved processes a HumanCandidatesResolved trigger: it
// replaces the task's candidate actors with the freshly resolved set and emits an
// UpdateTask command so the task store follows the snapshot.
//
// The list is replaced wholesale, never merged, so re-applying a resolution is
// idempotent and a shrinking candidate set (an actor who left the role) is
// reflected. A closed task is left frozen and emits nothing: once a task is
// completed or cancelled, its candidate list is part of the audit record and a
// late refresh must not rewrite history.
func handleHumanCandidatesResolved(s *InstanceState, t HumanCandidatesResolved) (StepResult, error) {
	// A terminal instance never reaches here: dispatch's structural guard returns
	// first (ADR-0165; the route it closes is on
	// HumanCandidatesResolved.terminalPolicy). The !task.IsOpen() check below is
	// the INDEPENDENT task-lifetime key — a task cancelled by an interrupting
	// boundary on a still-running instance — not a stand-in for it.
	task := s.TaskByID(t.TaskID)
	if task == nil {
		return StepResult{}, fmt.Errorf("%w: %q", ErrTokenNotFound, t.TaskID)
	}
	if !task.IsOpen() {
		return StepResult{State: *s, Commands: nil}, nil
	}
	// Copy on ingest: the trigger's slice belongs to the caller (and, on the
	// refresh path, to the resolver), so aliasing it here would let a later
	// mutation reach committed instance state.
	task.Candidates = authz.CloneActors(t.Candidates)
	// Clone before the record escapes to a consumer-supplied TaskStore: task
	// points into s.Tasks, which is committed as instance state (ADR-0163).
	return StepResult{State: *s, Commands: []Command{UpdateTask{Task: task.Clone()}}}, nil
}

// handleHumanReassigned processes a HumanReassigned trigger: updates the task
// assignment and emits an UpdateTask command.
//
// Errors with [ErrTaskNotOpen] on an already Completed or Cancelled task, for the
// same reason as [handleHumanClaimed] — see its doc comment.
func handleHumanReassigned(s *InstanceState, t HumanReassigned) (StepResult, error) {
	task := s.TaskByID(t.TaskID)
	if task == nil {
		return StepResult{}, fmt.Errorf("%w: %q", ErrTokenNotFound, t.TaskID)
	}
	if !task.IsOpen() {
		return StepResult{}, fmt.Errorf("%w: task %q", ErrTaskNotOpen, t.TaskID)
	}
	// A task carries at most one claim: reassignment overwrites it rather than
	// appending, so there is no claim history (ADR-0147). The reassignment
	// trigger identifies the new assignee by ID only — unlike a claim, which
	// carries the actor the caller authorized — so the recorded actor has an ID
	// and no roles or attributes.
	task.Claim = &humantask.Claim{Actor: authz.Actor{ID: t.To}, At: t.OccurredAt()}
	task.State = humantask.Claimed
	// Clone before the record escapes to a consumer-supplied TaskStore: task
	// points into s.Tasks, which is committed as instance state (ADR-0163).
	return StepResult{State: *s, Commands: []Command{UpdateTask{Task: task.Clone()}}}, nil
}

// handleTimerFired processes a TimerFired trigger: dispatches in priority order
// through event-gateway arms, boundary arms, event sub-process arms,
// deadline/in-wait/retry timer records, and finally standalone intermediate timers.
//
// The TimerDeadline/TimerInWait/TimerRetry sub-dispatch is preserved exactly as today.
func handleTimerFired(ctx context.Context, def *model.ProcessDefinition, s *InstanceState, t TimerFired, opt StepOptions) (StepResult, error) {
	// Dispatch order:
	// 1) event-based gateway arm (first-event-wins routing).
	// 2) boundary event arm (interrupting/non-interrupting).
	// 3) event sub-process arm (interrupting: cancel scope; non-interrupting: spawn alongside).
	// 4) deadline/in-wait timer record (task-guarded timers).
	// 5) standalone intermediate catch event (token parks on TimerID).

	// A terminal instance never reaches this cascade: dispatch's structural guard
	// returns first (ADR-0165; the route it closes — a late timer firing a
	// boundary, deadline or event-sub arm that the terminal transition never
	// swept — is on TimerFired.terminalPolicy).
	//
	// 1)-3) Gateway arm / boundary arm / event sub-process arm cascade,
	// first-match-wins (dispatchArmCascade — no payload to merge for timer).
	if cmds, matched, err := dispatchArmCascade(ctx, def, s, t.OccurredAt(), opt.Mode, resolveEvaluator(opt), nil,
		func() *armedEvent { return s.armedEventByTimer(t.TimerID) },
		func() *boundaryArm { return s.boundaryArmByTimer(t.TimerID) },
		func() *eventTriggeredSubprocessArm { return s.eventTriggeredSubprocessArmByTimer(t.TimerID) },
	); matched {
		if err != nil {
			return StepResult{}, err
		}
		return StepResult{State: *s, Commands: cmds}, nil
	}

	// 4) deadline/in-wait/retry timer record.
	// s.Timers holds deadline (TimerDeadline), in-wait/reminder (TimerInWait), and retry
	// (TimerRetry) records. Intermediate timers (TimerIntermediate) are never
	// appended to s.Timers; for those, the token parks on the TimerID as its
	// AwaitCommand, so they route via the tokenAwaiting path below.
	rec := s.timerByID(t.TimerID)
	if rec != nil {
		switch rec.Kind {
		case TimerDeadline:
			return handleDeadlineFired(ctx, def, s, *rec, t.OccurredAt(), opt.Mode, resolveEvaluator(opt))
		case TimerInWait:
			return handleReminderFired(def, s, *rec)
		case TimerRetry:
			return handleRetryFired(ctx, def, s, *rec, t.OccurredAt(), opt.Mode, resolveEvaluator(opt))
		}
	}

	// 5) Standalone intermediate timer.
	tok := s.tokenAwaiting(t.TimerID)
	if tok == nil {
		// Stale/already-moved timer (no record and no parked token): clean no-op.
		// Timers are inherently racy with other completion paths (e.g. the
		// instance may have advanced via a different branch), so we never error here.
		return StepResult{State: *s, Commands: nil}, nil
	}
	// ADR-0172: a DYING instance spawns no new work — and that is NOT a
	// signal-specific rule. Resuming this token would drive it forward and
	// dispatch a real InvokeAction to a worker, whose ActionCompleted then lands
	// on an instance the in-flight rollback has already terminated.
	//
	// The check sits AHEAD of the resume (and, on the message path, ahead of
	// mergeVars) so the delivery is neither consumed nor half-applied: a message
	// swallowed here would never be redelivered.
	//
	// ⚠ dispatchArmCascade guards the ARM families; this guards the token
	// fall-through. Both are needed — an earlier revision of this bundle guarded
	// only the arms and asserted in a comment that the callers covered the
	// token path themselves. They did not; `/code-review` measured a live
	// InvokeAction escaping on both the timer and message paths.
	if !s.spawnsNewWork() {
		return StepResult{State: *s, Commands: nil}, nil
	}
	// Intermediate timer: remove its record (if any) so a later dup is a no-op.
	s.removeTimer(t.TimerID)
	tok.AwaitCommand = ""
	// Cancel any in-wait reminder armed on this parked token (timer catch): the
	// reminder is a DIFFERENT TimerInWait than the intermediate that just fired;
	// exclude t.TimerID defensively.
	timerPreCmds := appendCancelTimers(nil, s.cancelTimersForToken(tok.ID, t.TimerID))
	timerTdef, timerTdefErr := defForScope(def, s, tok.ScopeID)
	if timerTdefErr != nil {
		return StepResult{}, timerTdefErr
	}
	cmds, err := resumeAndDrive(ctx, def, timerTdef, s, tok, t.OccurredAt(), opt, timerPreCmds)
	if err != nil {
		return StepResult{}, err
	}
	return StepResult{State: *s, Commands: cmds}, nil
}

// parkOnCompletionAction checks whether tok's node (resolved against tdef, the
// scope-effective definition) carries a CompletionAction and, if so, parks the
// token on the action round-trip instead of advancing now: it appends an
// InvokeAction to cmds, sets the token to TokenWaiting awaiting the new
// command, and returns (result, true) — the caller must return this result
// immediately without falling through to its own moveAlongSingleFlow+drive.
// When the node carries no CompletionAction, it returns (StepResult{}, false)
// and the caller proceeds with its own advance-and-drive as usual.
//
// Shared by handleHumanCompleted and handleMessageReceived (both completion
// triggers whose park-then-invoke-then-resume shape is otherwise identical).
func parkOnCompletionAction(s *InstanceState, tdef *model.ProcessDefinition, tok *Token, cmds []Command) (StepResult, bool) {
	node, ok := tdef.Node(tok.NodeID)
	if !ok {
		return StepResult{}, false
	}
	ca := completionActionOf(node)
	if ca == "" {
		return StepResult{}, false
	}
	cmdID := s.nextCommandID()
	cmds = append(cmds, InvokeAction{
		CommandID: cmdID,
		Name:      ca,
		Scoped:    tdef.ScopedCatalog(),
		Input:     copyVars(s.Variables),
	})
	tok.State = TokenWaiting
	tok.AwaitCommand = cmdID
	return StepResult{State: *s, Commands: cmds}, true
}

// applyOutcomeExposure publishes a completion outcome as a process variable when
// the user task opts in (ADR-0146): an explicit OutcomeVariable name wins,
// otherwise ExposeOutcome publishes under the "<node id>_outcome" convention. A
// node opting into neither keeps the outcome audit-only, and an empty outcome is
// never published. Call it after the completion output is merged so the outcome
// wins when both target the same variable.
func applyOutcomeExposure(s *InstanceState, ut activity.UserTask, outcome string) {
	if outcome == "" {
		return
	}
	name := ut.OutcomeVariable
	if name == "" {
		if !ut.ExposeOutcome {
			return
		}
		name = ut.ID() + "_outcome"
	}
	mergeVars(s, map[string]any{name: outcome})
}

// handleHumanCompleted processes a HumanCompleted trigger: merges output,
// completes the task, advances the token, cancels guarding timers, and drives forward.
//
// Errors with [ErrTaskNotOpen] on an already Completed or Cancelled task — the
// task-lifetime key described on [handleHumanClaimed].
func handleHumanCompleted(ctx context.Context, def *model.ProcessDefinition, s *InstanceState, t HumanCompleted, opt StepOptions) (StepResult, error) {
	// The task record is resolved BEFORE the token (ADR-0165 Decision 6). The
	// lookup order is what makes the task-lifetime guard reachable at all: with
	// tokenAwaiting first the guard below is dead code, because the three paths
	// that close a task on a live instance today each also detach its token.
	// cancelTokenWaits consumes the token outright (interrupting boundary, scope
	// cancellation); a breached deadline clears the token's AwaitCommand before
	// cancelling the task and moves it onto the alternative path; and
	// dropStaleTokenCommands only closes tasks whose awaiter is already gone.
	// Verified by mutation rather than inspection: reverting this order alone,
	// with the guard left in place, reddens both closed-task HumanCompleted rows
	// in step_task_lifetime_guard_test.go.
	//
	// The reorder is therefore a deliberate error change, not a refactor: those
	// paths reported ErrTokenNotFound ("no such task", to a caller) and now report
	// ErrTaskNotOpen ("the task exists, you are too late").
	//
	// The no-record branch below must NOT collapse the two cases the pre-reorder
	// order told apart, which is why it re-checks the token rather than answering
	// with one sentinel (ADR-0165 correction, Phase 6.4):
	//
	//   - no record AND no parked token — the id is a GHOST. Reached through
	//     service.deliverTaskTrigger, which reads the task store FIRST and answers
	//     a genuinely unknown id itself, this can only mean the store holds a task
	//     that instance state does not. That is a wrong-state transition, so it
	//     stays [ErrTokenNotFound] (→ ErrInvalidTransition → 422). Answering 404
	//     would deny a task the store still holds.
	//   - no record but a token IS parked on it — the record vanished from under a
	//     live token, an invariant violation, and [humantask.ErrTaskNotFound] is
	//     the pre-existing answer that TestHumanCompletedMissingTaskRecordErrors
	//     pins. Unchanged.
	//
	// All four human-task handlers agree on the first case; only this one can
	// observe the second, because only it looks a token up at all.
	task := s.TaskByID(t.TaskID)
	if task == nil {
		if s.tokenAwaiting(t.TaskID) == nil {
			return StepResult{}, fmt.Errorf("%w: %q", ErrTokenNotFound, t.TaskID)
		}
		return StepResult{}, fmt.Errorf("workflow-engine: human-completed for token %q has no task record: %w", t.TaskID, humantask.ErrTaskNotFound)
	}
	if !task.IsOpen() {
		return StepResult{}, fmt.Errorf("%w: task %q", ErrTaskNotOpen, t.TaskID)
	}
	// An OPEN task without a parked token is the remaining invariant violation
	// (token and task are created together in KindUserTask, and the paths that
	// separate them close the task on the way out). Advancing silently would
	// corrupt state without emitting UpdateTask, so the trigger is rejected.
	tok := s.tokenAwaiting(t.TaskID)
	if tok == nil {
		return StepResult{}, fmt.Errorf("%w: %q", ErrTokenNotFound, t.TaskID)
	}
	humanTdef, humanTdefErr := defForScope(def, s, tok.ScopeID)
	if humanTdefErr != nil {
		return StepResult{}, humanTdefErr
	}
	// The node declaration drives both completion guards below. A node that is
	// missing or is not a UserTask leaves ut at its zero value, i.e. unconstrained.
	var ut activity.UserTask
	if n, ok := humanTdef.Node(tok.NodeID); ok {
		ut, _ = n.(activity.UserTask)
	}
	// A wait-mode manual UserTask (Manual && !ManualImmediate) is a form-less
	// checkpoint: it completes on a bare trigger only. Reject any completion
	// payload before any output is merged (ADR-0118). This runs BEFORE the
	// outcome-set check because a manual task declares no outcomes, so that check
	// would fail open and silently record an outcome/note (ADR-0146).
	if ut.Manual && !ut.ManualImmediate && (len(t.Output) > 0 || t.Outcome != "" || t.Note != "") {
		return StepResult{}, fmt.Errorf("%w: node %q", ErrManualTaskPayload, tok.NodeID)
	}
	// Outcome validation fails closed: a declared set is a closed AND mandatory
	// value domain, so the completion must carry exactly one of its members
	// (ADR-0146). Declaring none leaves the task unconstrained.
	switch {
	case ut.Manual:
		// A manual task is forbidden from declaring outcomes at authoring time
		// (model.ErrManualTaskOutcome) and completes on a bare trigger, so it can
		// never be required to supply one. Reached only by an unvalidated
		// definition; the payload guard above already rejected any outcome sent
		// to a wait-mode manual task.
	case len(ut.Outcomes) == 0:
		// Unconstrained: any outcome, or none at all, completes the task.
	case t.Outcome == "":
		return StepResult{}, fmt.Errorf("%w: node %q", ErrOutcomeRequired, tok.NodeID)
	case !slices.Contains(ut.Outcomes, t.Outcome):
		return StepResult{}, fmt.Errorf("%w: node %q outcome %q", ErrInvalidOutcome, tok.NodeID, t.Outcome)
	}
	mergeVars(s, t.Output)
	applyOutcomeExposure(s, ut, t.Outcome)
	task.State = humantask.Completed
	// Record who completed the task, when, and with what disposition (ADR-0147).
	// The outcome is audited whether or not the node exposes it as a variable.
	task.Completion = &humantask.Completion{
		Actor:   t.Actor,
		At:      t.OccurredAt(),
		Outcome: t.Outcome,
		Note:    t.Note,
	}
	tok.State = TokenActive
	tok.AwaitCommand = ""
	// Clone before the record escapes to a consumer-supplied TaskStore: task
	// points into s.Tasks, which is committed as instance state (ADR-0163).
	cmds := []Command{UpdateTask{Task: task.Clone()}}
	// Cancel any deadline or reminder timers that were guarding this task.
	cmds = appendCancelTimers(cmds, s.cancelTimersByTaskID(t.TaskID, ""))
	// Cancel any boundary arms on this host token (token ID is the same as the
	// HostToken recorded at arm time; at this point tok.ID is still valid since
	// the token has not moved yet — tok.ID is already the token that was
	// parked, looked up via tokenAwaiting(t.TaskID) above).
	cmds = appendCancelTimers(cmds, s.removeBoundaryArmsForHost(tok.ID))
	// Completion action: park on the action round-trip instead of advancing now.
	// handleActionCompleted resumes: it merges the action output and advances the
	// token along the single outgoing flow (the token is still at humanTdef's node).
	if res, parked := parkOnCompletionAction(s, humanTdef, tok, cmds); parked {
		return res, nil
	}
	// No completion action: advance + drive as before.
	s.moveAlongSingleFlow(humanTdef, tok, t.OccurredAt())
	driveCmds, err := drive(ctx, def, s, t.OccurredAt(), opt.Mode, resolveEvaluator(opt))
	if err != nil {
		return StepResult{}, err
	}
	cmds = append(cmds, driveCmds...)
	return StepResult{State: *s, Commands: cmds}, nil
}

// handleSignalReceived processes a SignalReceived trigger: dispatches in priority
// order through event-gateway arms, boundary arms, event sub-process arms, and
// standalone parked-signal tokens (broadcast semantics).
//
// Broadcast applies BOTH across families and WITHIN each one (ADR-0158): every
// arm matching the signal name fires, not just the first. Only arms armed at the
// DELIVERY INSTANT are eligible — each family's matching identities are
// snapshotted before any dispatch, so an arm created by this delivery's own drive
// is not fired by it.
func handleSignalReceived(ctx context.Context, def *model.ProcessDefinition, s *InstanceState, t SignalReceived, opt StepOptions) (StepResult, error) {
	// Broadcast semantics within the instance: resume every token that is
	// awaiting this signal name. Tokens are processed in slice order for
	// determinism. A signal that matches no token (and no gateway arm, no
	// boundary arm, and no event sub-process arm) is a clean no-op — mergeVars
	// runs ONLY when at least one match is found.
	//
	// NOTE: mergeVars is deferred until after match-checking so that a no-match
	// delivery does not mutate instance variables (Task-2 review fix).
	//
	// Dispatch order for signal (ADR-0158 — EVERY matching arm in each family,
	// in the order the definition declares its nodes; the engine does not
	// re-order them):
	// 1) event-based gateway arms (first-event-wins WITHIN one gateway token,
	//    plural ACROSS distinct gateway tokens).
	// 2) boundary event arms (interrupting/non-interrupting).
	// 3) event sub-process arms (interrupting: cancel scope; non-interrupting: spawn alongside).
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

	// markMatched merges the delivery payload the FIRST time any dispatch point
	// matches, and only then (ADR-0169 Decision 4). It runs inside the tier that
	// matches, before that tier fires, so the tier that legitimately fired is the
	// one that merged: a delivery aborted after a match returns state carrying the
	// payload, and one aborted before any match leaves Variables untouched.
	markMatched := func() {
		if !matched {
			mergeVars(s, t.Payload)
			matched = true
		}
	}

	// ADR-0158: SNAPSHOT each family's matching arm IDENTITIES before dispatching
	// anything, then build one lookup-and-fire closure per identity. Tiers 1–3 used
	// to be three closures doing three FIRST-MATCH lookups; a broadcast signal now
	// fires every matching arm per family.
	//
	// The snapshot is load-bearing TWICE OVER, and the second reason is not about
	// termination:
	//
	//  1. TERMINATION. A non-interrupting arm deliberately stays armed after firing
	//     (ADR-0124), in both the boundary and event-sub-process families, so a loop
	//     that re-scanned for "the next match" would find the same arm forever.
	//  2. SINGLE-INSTANT SEMANTICS. It confines the delivery to arms that existed at
	//     the DELIVERY INSTANT. Without it a later tier fires an arm an earlier
	//     tier's own drive created — measured on main, a gateway fire parked at a
	//     user task and armed its boundary, which tier 2 then fired, minting and
	//     cancelling a human task inside one step. Tier 4 has always applied this
	//     rule to tokens (see snapshotIDs above); tiers 1–3 did not.
	//
	// ⚠ Identities are VALUES, not pointers. removeArmsWhere reallocates the backing
	// array, so a pointer captured before an earlier fire addresses the DETACHED old
	// array where the removed arm is still intact — firing through it would dispatch
	// an already-retired arm. See armIDsBySignal.
	//
	// ⚠ Each closure RE-RESOLVES its identity at the moment it runs, and skips the
	// arm when it no longer resolves. This is the successor to ADR-0169's "the three
	// lookups must NOT be hoisted" requirement, which this change would otherwise
	// have broken: what may not be hoisted is the RESOLUTION (an earlier fire can
	// retire a later arm — an interrupting boundary cancels its host's siblings, an
	// interrupting event sub-process cancels its scope subtree). What IS hoisted is
	// the ENUMERATION, deliberately, per reason 2 above. Re-resolution is an
	// EXISTENCE CHECK ONLY: using it to pick the NEXT arm reinstates reason 1's
	// non-terminating re-scan.
	//
	// ⚠ NO SORT. Arms fire in declaration order. A per-family NonInterrupting sort
	// was specified by an earlier draft of ADR-0158 and refuted by execution in BOTH
	// directions — whether an earlier arm's effects are destroyable depends on the
	// arm's BODY, not its flag. Ordering is outcome-affecting and author-controlled.
	gatewayIDs := s.armedEventIDsBySignal(t.Name)
	boundaryIDs := s.boundaryArmIDsBySignal(t.Name)
	eventSubIDs := s.eventTriggeredSubprocessArmIDsBySignal(t.Name)

	// resolvedGateways keeps first-event-wins unforgeable across the delivery.
	// resolveGatewayWin does NOT consume the gateway token, so a branch looping back
	// through a merge gateway can re-arm (GatewayToken, CatchNode) byte-identically
	// WITHIN this delivery — an ABA that would let one signal resolve the same
	// gateway twice. (The naive direct loop is rejected by model.Validate; the
	// reachable shape routes through an exclusive-gateway merge.)
	resolvedGateways := make(map[string]struct{}, len(gatewayIDs))

	tiers := make([]func() ([]Command, error), 0, len(gatewayIDs)+len(boundaryIDs)+len(eventSubIDs))

	// 1) Event-gateway arms (first-event-wins WITHIN a gateway; plural ACROSS
	// distinct gateway tokens).
	for _, id := range gatewayIDs {
		tiers = append(tiers, func() ([]Command, error) {
			if _, done := resolvedGateways[id.GatewayToken]; done {
				return nil, nil
			}
			ae := s.armedEventByID(id)
			if ae == nil {
				return nil, nil
			}
			markMatched()
			cmds, err := resolveGatewayWin(ctx, def, s, *ae, t.OccurredAt(), opt.Mode, resolveEvaluator(opt))
			if err != nil {
				return nil, err
			}
			resolvedGateways[id.GatewayToken] = struct{}{}
			return cmds, nil
		})
	}

	// 2) Boundary event arms (interrupting/non-interrupting).
	for _, id := range boundaryIDs {
		tiers = append(tiers, func() ([]Command, error) {
			ba := s.boundaryArmByID(id)
			if ba == nil {
				return nil, nil
			}
			markMatched()
			return fireBoundaryArm(ctx, def, s, *ba, t.OccurredAt(), opt.Mode, resolveEvaluator(opt))
		})
	}

	// 3) Event sub-process arms (interrupting: cancel scope; non-interrupting:
	// spawn alongside).
	for _, id := range eventSubIDs {
		tiers = append(tiers, func() ([]Command, error) {
			ea := s.eventTriggeredSubprocessArmByID(id)
			if ea == nil {
				return nil, nil
			}
			markMatched()
			return fireEventTriggeredSubprocessArm(ctx, def, s, *ea, t.OccurredAt(), opt.Mode, resolveEvaluator(opt))
		})
	}

	// MID-DELIVERY RE-CHECK (ADR-0169, predicate widened by ADR-0172). A tier's
	// own drive can reach an uncaught error and end the instance — or begin a
	// TERMINATING compensation rollback — and dispatch's entry guard (ADR-0165)
	// cannot see either: the instance was running when the trigger was admitted.
	// So every remaining dispatch point re-checks, and an aborted delivery returns
	// the partial commands the earlier tiers legitimately produced — including the
	// FailInstance that made the instance terminal. Dropping them would lose the
	// earlier tiers' task-store reconciliation (ADR-0089) and degrade the terminal
	// event's error payload. No Warn is emitted; that cost is recorded in
	// ADR-0169 Decision 5.
	//
	// ⚠ The predicate is spawnsNewWork(), NOT IsTerminal(). IsTerminal() is false
	// for StatusCompensating, so it let a later arm fire into a rollback that was
	// already going to end the instance. ADR-0158's fan-out multiplies the number
	// of dispatch points per delivery and therefore that window.
	for _, fire := range tiers {
		if !s.spawnsNewWork() {
			return StepResult{State: *s, Commands: signalCmds}, nil
		}
		tierCmds, err := fire()
		if err != nil {
			return StepResult{}, err
		}
		signalCmds = append(signalCmds, tierCmds...)
	}

	// 4) Resume all standalone parked-signal tokens that were in the snapshot
	// (i.e. awaiting this signal at the delivery instant). Tokens spawned by
	// steps 1–3 above are not in the snapshot and will not be consumed here.
	//
	// The terminal re-check belongs INSIDE this loop, not only ahead of it: the
	// loop is multi-iteration, so one token's drive can end the instance while a
	// later token still awaits resumption (ADR-0169 Decision 3).
	for _, tokenID := range snapshotIDs {
		if !s.spawnsNewWork() {
			return StepResult{State: *s, Commands: signalCmds}, nil
		}
		tok := s.tokenByID(tokenID)
		// Skip if the token was consumed by an interrupting boundary/event-subprocess (steps 2–3)
		// or is no longer awaiting this signal.
		if tok == nil || tok.AwaitSignal != t.Name {
			continue
		}
		markMatched()
		tok.AwaitSignal = ""
		// Cancel any in-wait reminder armed on this parked token (signal catch):
		// the wait has resolved, so the recurring reminder job must be removed.
		timerPreCmds := appendCancelTimers(nil, s.cancelTimersForToken(tok.ID, ""))
		signalTdef, signalTdefErr := defForScope(def, s, tok.ScopeID)
		if signalTdefErr != nil {
			return StepResult{}, signalTdefErr
		}
		cmds, err := resumeAndDrive(ctx, def, signalTdef, s, tok, t.OccurredAt(), opt, timerPreCmds)
		if err != nil {
			return StepResult{}, err
		}
		signalCmds = append(signalCmds, cmds...)
	}
	return StepResult{State: *s, Commands: signalCmds}, nil
}

// handleSubInstanceCompleted processes a SubInstanceCompleted trigger: resumes
// the parent token at the call-activity node, merges child output, and drives forward.
func handleSubInstanceCompleted(ctx context.Context, def *model.ProcessDefinition, s *InstanceState, t SubInstanceCompleted, opt StepOptions) (StepResult, error) {
	// A child process instance (started by StartSubInstance) has finished
	// successfully. Resume the parent token that was parked at the call-activity
	// node, merge the child's output variables into the parent, then drive forward.

	// Mirror of ActionCompleted: find the parked token by CommandID, merge vars,
	// activate the token, move along its single outgoing flow, then drive.
	tok := s.tokenAwaiting(t.CommandID)
	if tok == nil {
		// Unreachable for a terminal instance — dispatch's structural guard
		// returned before this handler was entered (ADR-0165; the route it closes,
		// and why returning nil there does not make CallNotifier retry forever, is
		// on SubInstanceCompleted.terminalPolicy).
		return StepResult{}, fmt.Errorf("%w: %q", ErrTokenNotFound, t.CommandID)
	}
	mergeVars(s, t.Output)
	tok.AwaitCommand = ""
	// Advance the token past the call-activity node using the token's scope
	// definition (call-activity nodes can live inside a sub-process scope).
	tdef, err := defForScope(def, s, tok.ScopeID)
	if err != nil {
		return StepResult{}, err
	}
	cmds, err := resumeAndDrive(ctx, def, tdef, s, tok, t.OccurredAt(), opt, nil)
	if err != nil {
		return StepResult{}, err
	}
	return StepResult{State: *s, Commands: cmds}, nil
}

// handleSubInstanceFailed processes a SubInstanceFailed trigger: routes to a
// parent error boundary attached to the call-activity node when the child's
// error code matches (ADR-0128), else fails the parent instance.
func handleSubInstanceFailed(ctx context.Context, def *model.ProcessDefinition, s *InstanceState, t SubInstanceFailed, opt StepOptions) (StepResult, error) {
	// A child process instance has terminated with an error. A SubInstanceFailed
	// is semantically an error thrown AT the call-activity node that spawned the
	// child (ADR-0128): when that node carries a boundary error event whose
	// ErrorCode matches the child's error, route to it exactly as propagateError's
	// direct-boundary path does for an ActionFailed on an ordinary activity —
	// cancel the call-activity token, fire the boundary's outgoing flow, and
	// drive. When no boundary matches, fall back to the original behavior:
	// FailInstance and cancel all timers/arms to clean up.

	tok := s.tokenAwaiting(t.CommandID)
	if tok == nil {
		// Unreachable for a terminal instance — dispatch's structural guard
		// returned before this handler was entered (ADR-0165; the route it closes
		// is on SubInstanceFailed.terminalPolicy).
		return StepResult{}, fmt.Errorf("%w: %q", ErrTokenNotFound, t.CommandID)
	}

	hostDef, err := defForScope(def, s, tok.ScopeID)
	if err != nil {
		return StepResult{}, err
	}
	cause := error(errors.New(t.Err))
	if boundary, ok := findDirectBoundary(ctx, hostDef, tok.NodeID, t.Err, s.Variables, cause, resolveEvaluator(opt)); ok {
		callActivityTok := tok
		consume := func(cmds []Command) []Command {
			// Mirror the ActionFailed direct-boundary path (propagateError's
			// Step 1, fed by handleActionFailed's preCmds): cancel the call-activity
			// token's OTHER boundary arms (e.g. a sibling boundary timer/deadline)
			// before consuming it, so a matched error boundary doesn't leak a
			// still-armed sibling arm that would otherwise fire later against a
			// gone host.
			cmds = appendCancelTimers(cmds, s.removeBoundaryArmsForHost(callActivityTok.ID))
			s.consumeTokenAs(callActivityTok, t.OccurredAt(), CloseKindBoundaryInterrupted)
			return cmds
		}
		cmds, routeErr := routeToBoundary(ctx, def, s, hostDef, boundary, "call-activity boundary", tok.ScopeID,
			t.OccurredAt(), opt.Mode, resolveEvaluator(opt), consume)
		if routeErr != nil {
			return StepResult{}, routeErr
		}
		return StepResult{State: *s, Commands: cmds}, nil
	}

	// endInstance reconciles the human-task projection (a UserTask parked on a
	// sibling branch must not be left open when a child failure fails the parent —
	// ADR-0089) and cancels the scheduled work. FailInstance moves from FIRST to
	// the canonical position AFTER the task cancels: this was the one terminal path
	// that emitted the other order (ADR-0164 Decision 1). Tokens are not dropped
	// here, so incidents whose token survives are deliberately kept.
	//
	// endInstance mutates *s, so its result is bound BEFORE the StepResult literal
	// dereferences s: Go orders function calls left-to-right but leaves a plain
	// dereference operand's position unspecified, and inlining the call would risk
	// snapshotting the pre-terminal state.
	cmds := s.endInstance(StatusFailed, t.OccurredAt(), FailInstance{Err: t.Err})
	return StepResult{State: *s, Commands: cmds}, nil
}

// handleMessageReceived processes a MessageReceived trigger: dispatches in
// priority order through event-gateway arms, event sub-process arms, and
// the standalone parked-message token (point-to-point semantics).
func handleMessageReceived(ctx context.Context, def *model.ProcessDefinition, s *InstanceState, t MessageReceived, opt StepOptions) (StepResult, error) {
	// Point-to-point semantics: resume the single token whose AwaitMessage
	// matches the name AND whose AwaitMessageKey matches the correlation key.
	// A message that matches no token (and no gateway arm, no boundary arm, and
	// no event sub-process arm) is a clean no-op.
	//
	// Dispatch order for message:
	// 1) event-based gateway arm (first-event-wins).
	// 2) boundary event arm (interrupting/non-interrupting on a host activity).
	// 3) event sub-process arm (interrupting/non-interrupting).
	// 4) standalone parked-message token (point-to-point).
	//
	// First match wins: unlike signal (broadcast), message delivery is
	// point-to-point, so each dispatch branch returns immediately on a match.
	//
	// NOTE: mergeVars is deferred until after match-checking so that a no-match
	// delivery does not mutate instance variables (Task-2 review fix).

	// 1)-3) Gateway arm / boundary arm (reuses the same fireBoundaryArm
	// machinery as timer/signal boundaries) / event sub-process arm cascade,
	// first-match-wins (dispatchArmCascade). onMatch merges the trigger
	// payload into instance variables exactly once, before firing, only when
	// a match is found — mirroring the pre-extraction per-branch mergeVars.
	if cmds, matched, err := dispatchArmCascade(ctx, def, s, t.OccurredAt(), opt.Mode, resolveEvaluator(opt),
		func() { mergeVars(s, t.Payload) },
		func() *armedEvent { return s.armedEventByMessage(t.Name, t.CorrelationKey) },
		func() *boundaryArm { return s.boundaryArmByMessage(t.Name, t.CorrelationKey) },
		func() *eventTriggeredSubprocessArm {
			return s.eventTriggeredSubprocessArmByMessage(t.Name, t.CorrelationKey)
		},
	); matched {
		if err != nil {
			return StepResult{}, err
		}
		return StepResult{State: *s, Commands: cmds}, nil
	}

	// 4) Resume the standalone parked-message token.
	tok := s.tokenAwaitingMessage(t.Name, t.CorrelationKey)
	if tok == nil {
		// No matching token: clean no-op (message may be for a different instance
		// or arrived after the instance advanced).
		return StepResult{State: *s, Commands: nil}, nil
	}
	// ADR-0172: a DYING instance spawns no new work — and that is NOT a
	// signal-specific rule. Resuming this token would drive it forward and
	// dispatch a real InvokeAction to a worker, whose ActionCompleted then lands
	// on an instance the in-flight rollback has already terminated.
	//
	// The check sits AHEAD of the resume (and, on the message path, ahead of
	// mergeVars) so the delivery is neither consumed nor half-applied: a message
	// swallowed here would never be redelivered.
	//
	// ⚠ dispatchArmCascade guards the ARM families; this guards the token
	// fall-through. Both are needed — an earlier revision of this bundle guarded
	// only the arms and asserted in a comment that the callers covered the
	// token path themselves. They did not; `/code-review` measured a live
	// InvokeAction escaping on both the timer and message paths.
	if !s.spawnsNewWork() {
		return StepResult{State: *s, Commands: nil}, nil
	}
	mergeVars(s, t.Payload)
	tok.AwaitMessage = ""
	tok.AwaitMessageKey = ""
	// Cancel any in-wait reminder armed on this parked token (ReceiveTask or
	// message intermediate catch): the wait has resolved, so the recurring
	// reminder job must be removed to stop it firing forever.
	preCmds := appendCancelTimers(nil, s.cancelTimersForToken(tok.ID, ""))
	// Disarm any boundary arms on this host token (e.g. a ReceiveTask with an
	// attached timer/message boundary) now that the awaited message resolved
	// the host before the boundary fired.
	preCmds = appendCancelTimers(preCmds, s.removeBoundaryArmsForHost(tok.ID))
	msgTdef, msgTdefErr := defForScope(def, s, tok.ScopeID)
	if msgTdefErr != nil {
		return StepResult{}, msgTdefErr
	}
	// Completion action: park on the action round-trip instead of advancing now.
	// handleActionCompleted resumes: it merges the action output and advances the
	// token along the single outgoing flow (the token is still at msgTdef's node).
	if res, parked := parkOnCompletionAction(s, msgTdef, tok, preCmds); parked {
		return res, nil
	}
	// No completion action: advance + drive as before.
	cmds, err := resumeAndDrive(ctx, def, msgTdef, s, tok, t.OccurredAt(), opt, preCmds)
	if err != nil {
		return StepResult{}, err
	}
	return StepResult{State: *s, Commands: cmds}, nil
}

// handleResolveIncident processes a ResolveIncident trigger: clears a parked
// incident, grants additional retry budget, and re-invokes the stalled action.
func handleResolveIncident(ctx context.Context, def *model.ProcessDefinition, s *InstanceState, t ResolveIncident, opt StepOptions) (StepResult, error) {
	// Admin trigger: clear a parked incident, grant additional retry budget,
	// and re-invoke the stalled service action so the process can continue.
	//
	// Idempotency: an unknown or already-cleared IncidentID is a clean no-op;
	// a missing token (removed by a concurrent path) clears the record and
	// returns without re-invoking.
	//
	// A terminal instance never reaches here: dispatch's structural guard returns
	// ErrInstanceTerminal first, BEFORE the incident is removed below (ADR-0165).
	// That ordering is the whole point — the route it closes, and ADR-0164
	// Decision 3's write-consumer lesson behind it, is on
	// ResolveIncident.terminalPolicy.

	idx := -1
	for i := range s.Incidents {
		if s.Incidents[i].ID == t.IncidentID {
			idx = i
			break
		}
	}
	if idx < 0 {
		// Unknown or already-resolved incident: idempotent no-op.
		return StepResult{State: *s, Commands: nil}, nil
	}
	inc := s.Incidents[idx]
	// Remove the incident from the slice (order-preserving, avoids aliasing).
	s.Incidents = append(s.Incidents[:idx], s.Incidents[idx+1:]...)
	tok := s.tokenByID(inc.TokenID)
	if tok == nil {
		// Token is gone (concurrent resolution); incident cleared, no re-invoke.
		return StepResult{State: *s, Commands: nil}, nil
	}
	// Grant the additional retry budget: reducing RetryAttempts by AddAttempts
	// effectively gives the action that many more opportunities before the
	// policy declares it terminal again.
	tok.RetryAttempts = max(0, tok.RetryAttempts-t.AddAttempts)
	tok.State = TokenActive
	cmds, err := reinvokeServiceAction(ctx, def, s, tok, t.OccurredAt(), resolveEvaluator(opt))
	if err != nil {
		return StepResult{}, err
	}
	return StepResult{State: *s, Commands: cmds}, nil
}

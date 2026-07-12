package engine

import (
	"fmt"
	"time"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/humantask"
)

// handleDeadlineFired processes a TimerFired event for a deadline timer. It is called
// from the TimerFired handler in Step when the timer record's Kind is TimerDeadline.
//
// Contract:
//   - If the guarded task is already completed (or the parked token is gone),
//     it is a clean no-op: no commands, no error.
//   - If the task is still in progress, it performs the deadline breach:
//     (a) emits InvokeAction for node.DeadlineAction (if set),
//     (b) moves the token to the target of node.DeadlineFlow (alternative path),
//     (c) marks the task Cancelled and emits UpdateTask,
//     (d) cancels any other timers (e.g. reminders) for the same task,
//     (e) removes the deadline timer record and drives forward.
func handleDeadlineFired(def *model.ProcessDefinition, s *InstanceState, rec timerRecord, at time.Time, mode StepMode, eval ConditionEvaluator) (StepResult, error) {
	// Find the parked token. If the token is gone (task completed, instance
	// advanced), the deadline fired late → clean no-op.
	tok := s.tokenAwaiting(rec.TaskToken)
	if tok == nil {
		// Also clean up the stale timer record.
		s.removeTimer(rec.TimerID)
		return StepResult{State: *s, Commands: nil}, nil
	}

	// If the task has already been completed or cancelled, treat as no-op.
	// task.IsOpen() returns true only for Unclaimed or Claimed states; both
	// Completed and Cancelled are "already resolved", including a duplicate deadline
	// fire after cancellation.
	task := s.TaskByToken(rec.TaskToken)
	if task != nil && !task.IsOpen() {
		s.removeTimer(rec.TimerID)
		return StepResult{State: *s, Commands: nil}, nil
	}

	// Resolve the effective definition for the timer's scope so that deadline timers
	// inside a sub-process resolve nodes against the nested definition.
	tdefDeadline, tdefDeadlineErr := defForScope(def, s, rec.ScopeID)
	if tdefDeadlineErr != nil {
		return StepResult{}, tdefDeadlineErr
	}

	// Resolve the deadline alternative-path flow.
	node, ok := tdefDeadline.Node(rec.NodeID)
	if !ok {
		return StepResult{}, fmt.Errorf("workflow-engine: deadline breach: node %q not found in definition", rec.NodeID)
	}
	_, deadlineFlow, deadlineAction := model.DeadlineOf(node)
	if deadlineFlow == "" {
		return StepResult{}, fmt.Errorf("workflow-engine: deadline breach: node %q has no DeadlineFlow defined", rec.NodeID)
	}
	// Find the sequence flow with ID == deadlineFlow.
	var deadlineTarget string
	for _, f := range tdefDeadline.Flows {
		if f.ID == deadlineFlow {
			deadlineTarget = f.Target
			break
		}
	}
	if deadlineTarget == "" {
		return StepResult{}, fmt.Errorf("workflow-engine: deadline breach: DeadlineFlow %q not found in definition flows for node %q", deadlineFlow, rec.NodeID)
	}

	var cmds []Command

	// (a) Emit the deadline alternative action, if configured.
	if deadlineAction != "" {
		cmdID := s.nextCommandID()
		cmds = append(cmds, InvokeAction{
			CommandID:     cmdID,
			Name:          deadlineAction,
			Input:         copyVars(s.Variables),
			FireAndForget: true,
		})
	}

	// (b) Move the token to the alternative path target. The token was parked
	//     (TokenWaitingCommand / AwaitCommand == TaskToken); reactivate it and
	//     route to the deadline path.
	// Fix A: explicitly set TokenActive before moveTokenToTarget for symmetry
	// with HumanCompleted and as a defensive measure (moveTokenToTarget also
	// sets it, but being explicit here makes the intent unambiguous).
	tok.AwaitCommand = ""
	tok.State = TokenActive
	s.moveTokenToTarget(tok, deadlineTarget, at)

	// (c) Mark the task Cancelled and emit UpdateTask.
	if task != nil {
		task.State = humantask.Cancelled
		cmds = append(cmds, UpdateTask{Task: *task})
	}

	// (d) Cancel any other timers (e.g. reminder timers) for this task.
	for _, reminderID := range s.cancelTimersByTaskToken(rec.TaskToken, rec.TimerID) {
		cmds = append(cmds, CancelTimer{TimerID: reminderID})
	}

	// Remove the deadline timer record — it has been consumed.
	s.removeTimer(rec.TimerID)

	// (e) Drive forward from the alternative path.
	driveCmds, err := drive(def, s, at, mode, eval)
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
//     (1) emits InvokeAction(node.WaitAction) if non-empty (fire-and-forget),
//     (2) does NOT reschedule — the reminder was armed once at task entry with
//     its recurring trigger and the scheduler re-delivers TimerFired natively;
//     the reminder record stays in place and the token does NOT move.
func handleReminderFired(def *model.ProcessDefinition, s *InstanceState, rec timerRecord) (StepResult, error) {
	// Locate the parked token this reminder guards. For a UserTask the reminder
	// keys on the human-task token (rec.TaskToken == the token's AwaitCommand);
	// for a ReceiveTask / IntermediateCatchEvent it keys on the parked token id
	// (rec.Token), which awaits a message/signal/timer rather than a command.
	// Resolve via AwaitCommand first (UserTask), then by token id (the others).
	tok := s.tokenAwaiting(rec.TaskToken)
	if tok == nil {
		tok = s.tokenByID(rec.Token)
	}
	if tok == nil {
		// The token is gone (wait resolved and advanced): reminder fired late →
		// clean no-op, remove the stale record.
		s.removeTimer(rec.TimerID)
		return StepResult{State: *s, Commands: nil}, nil
	}

	// If a HumanTask exists for this token (UserTask path), honour its open
	// state: a Completed/Cancelled task makes the reminder stale. For a token
	// with no HumanTask (ReceiveTask / catch), the parked token still being
	// present is itself sufficient — it is live.
	if task := s.TaskByToken(rec.TaskToken); task != nil && !task.IsOpen() {
		s.removeTimer(rec.TimerID)
		return StepResult{State: *s, Commands: nil}, nil
	}

	// Resolve the effective definition for the timer's scope so that reminder
	// timers inside a sub-process resolve the node against the nested definition.
	tdefReminder, tdefReminderErr := defForScope(def, s, rec.ScopeID)
	if tdefReminderErr != nil {
		return StepResult{}, tdefReminderErr
	}

	// Resolve the node to get WaitEvery and WaitAction.
	node, ok := tdefReminder.Node(rec.NodeID)
	if !ok {
		return StepResult{}, fmt.Errorf("workflow-engine: reminder fired: node %q not found in definition", rec.NodeID)
	}

	_, waitActionName := model.WaitActionOf(node)

	var cmds []Command

	// (1) Fire-and-forget reminder action, if configured.
	if waitActionName != "" {
		cmdID := s.nextCommandID()
		cmds = append(cmds, InvokeAction{
			CommandID:     cmdID,
			Name:          waitActionName,
			Input:         copyVars(s.Variables),
			FireAndForget: true,
		})
	}

	// (2) No engine reschedule: the reminder timer was armed once at task entry
	// with the recurring trigger (Every/EveryExpr), and native scheduler
	// recurrence re-delivers TimerFired on the interval. The reminder record
	// therefore STAYS in s.Timers (do NOT remove it) so the guard above keeps
	// finding it on each fire, and the token does NOT move — the task is still
	// pending. (Repeated-fire behavior is the scheduler's responsibility, not
	// re-armed here.)
	return StepResult{State: *s, Commands: cmds}, nil
}

// reinvokeServiceAction re-emits an InvokeAction for tok's node, re-parks tok
// on the new command ID, and re-arms its boundary events. It is shared by the
// retry-timer path (handleRetryFired) and the incident-resolution path
// (ResolveIncident) so both use an identical re-invocation sequence.
//
// The caller is responsible for any pre-work specific to each path (e.g.
// removing the consumed timer record before calling this for the retry path).
func reinvokeServiceAction(def *model.ProcessDefinition, s *InstanceState, tok *Token, at time.Time, eval ConditionEvaluator) ([]Command, error) {
	tdef, err := defForScope(def, s, tok.ScopeID)
	if err != nil {
		return nil, fmt.Errorf("workflow-engine: reinvoke: %w", err)
	}
	node, ok := tdef.Node(tok.NodeID)
	if !ok {
		return nil, fmt.Errorf("workflow-engine: reinvoke: node %q not found", tok.NodeID)
	}

	// Re-emit InvokeAction — mirrors the KindServiceTask drive path exactly
	// (emitActionInvoke), including the stable idempotency key (see
	// serviceActionInput) and re-arming boundary events (deadline timers,
	// reminder timers) so they are active for this invocation attempt.
	return emitActionInvoke(&stepCtx{def: def, tdef: tdef, s: s, at: at, eval: eval}, tok, node)
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
//     on failure) so deadline and reminder timers are active for the retry attempt.
func handleRetryFired(def *model.ProcessDefinition, s *InstanceState, rec timerRecord, at time.Time, mode StepMode, eval ConditionEvaluator) (StepResult, error) {
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
	cmds, err := reinvokeServiceAction(def, s, tok, at, eval)
	if err != nil {
		return StepResult{}, fmt.Errorf("workflow-engine: retry fired: %w", err)
	}
	return StepResult{State: *s, Commands: cmds}, nil
}

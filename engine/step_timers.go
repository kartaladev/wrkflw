package engine

import (
	"fmt"
	"time"

	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/model"
)

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
	_, slaFlow, slaAction := model.SLAOf(node)
	if slaFlow == "" {
		return StepResult{}, fmt.Errorf("workflow-engine: SLA breach: node %q has no SLAFlow defined", rec.NodeID)
	}
	// Find the sequence flow with ID == slaFlow.
	var slaTarget string
	for _, f := range tdefSLA.Flows {
		if f.ID == slaFlow {
			slaTarget = f.Target
			break
		}
	}
	if slaTarget == "" {
		return StepResult{}, fmt.Errorf("workflow-engine: SLA breach: SLAFlow %q not found in definition flows for node %q", slaFlow, rec.NodeID)
	}

	var cmds []Command

	// (a) Emit the SLA alternative action, if configured.
	if slaAction != "" {
		cmdID := s.nextCommandID()
		cmds = append(cmds, InvokeAction{
			CommandID: cmdID,
			Name:      slaAction,
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

	reminderEvery, reminderAction := model.ReminderOf(node)

	var cmds []Command

	// (1) Fire-and-forget reminder action, if configured.
	if reminderAction != "" {
		cmdID := s.nextCommandID()
		cmds = append(cmds, InvokeAction{
			CommandID: cmdID,
			Name:      reminderAction,
			Input:     copyVars(s.Variables),
		})
	}

	// (2) Replace the fired reminder record with a new one for the next interval.
	// Remove the old record first so the timer table stays consistent.
	s.removeTimer(rec.TimerID)

	// Re-evaluate the duration from the expression (node variables may differ,
	// but correctness requires the same expression path as initial scheduling).
	dur, err := conditions.EvalDuration(reminderEvery, s.Variables)
	if err != nil {
		return StepResult{}, fmt.Errorf("workflow-engine: reminder node %q re-schedule: %w", node.ID(), err)
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
		Name:      model.ActionOf(node),
		Input:     serviceActionInput(s, node),
	}}
	tok.State = TokenWaitingCommand
	tok.AwaitCommand = cmdID

	// Re-arm boundary events (SLA timers, reminder timers) so they are active
	// for this invocation attempt.
	bndCmds, err := armBoundaries(tdef, s, tok.ID, node.ID(), at)
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

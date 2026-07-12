package engine

import (
	"fmt"
	"time"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/definition/activity"
	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/humantask"
)

// stepCtx carries the repeated inputs to the node-kind dispatch layer.
// tdef is the scope-resolved process definition for the current token;
// def is the top-level definition. Both are provided by drive().
type stepCtx struct {
	def  *model.ProcessDefinition
	tdef *model.ProcessDefinition
	s    *InstanceState
	at   time.Time
	mode StepMode
	// eval is the resolved expression evaluator for this Step: the one injected
	// via StepOptions.Evaluator, or the pure package-global default. drive()
	// resolves it once and threads it here so every strategy evaluates through
	// the same evaluator (ADR-0056).
	eval ConditionEvaluator
	// opt is otherwise not needed by any drive() arm strategy; it remains in
	// Step() scope for the ActionFailed handler (effectiveRetryPolicy) only.
}

// emitActionInvoke is the shared node-entry body for the action-delegating
// task kinds (KindServiceTask, KindBusinessRuleTask) and for service-action
// re-invocation after a retry timer or incident resolution
// (reinvokeServiceAction, engine/step_timers.go): resolve the primary action
// name and input for node, emit InvokeAction, park tok on the new command ID,
// and arm any boundary events attached to node. The three call sites differ
// only in how they obtain c and node (drive()-supplied stepCtx for the
// strategies, a freshly resolved stepCtx for re-invocation) — the invoke body
// itself is identical.
func emitActionInvoke(c *stepCtx, tok *Token, node model.Node) ([]Command, error) {
	cmdID := c.s.nextCommandID()
	cmds := []Command{InvokeAction{
		CommandID: cmdID,
		Name:      mainActionName(node),
		Scoped:    c.tdef.ScopedCatalog(),
		Input:     serviceActionInput(c.s, node),
	}}
	tok.State = TokenWaitingCommand
	tok.AwaitCommand = cmdID
	// Arm any boundary events attached to this host activity.
	bndCmds, err := armBoundaries(c.tdef, c.s, tok.ID, node.ID(), c.at, c.eval)
	if err != nil {
		return cmds, err
	}
	return append(cmds, bndCmds...), nil
}

// serviceTaskStrategy handles KindServiceTask node entry.
type serviceTaskStrategy struct{}

func (serviceTaskStrategy) enter(c *stepCtx, tok *Token, node model.Node) ([]Command, bool, error) {
	cmds, err := emitActionInvoke(c, tok, node)
	return cmds, false, err
}

// businessRuleTaskStrategy handles KindBusinessRuleTask node entry. It mirrors
// serviceTaskStrategy: emit the primary InvokeAction (default-by-id name plus
// the scope-resolved inline action and scoped catalog), park the token, and arm
// boundary events.
type businessRuleTaskStrategy struct{}

func (businessRuleTaskStrategy) enter(c *stepCtx, tok *Token, node model.Node) ([]Command, bool, error) {
	cmds, err := emitActionInvoke(c, tok, node)
	return cmds, false, err
}

// receiveTaskStrategy handles KindReceiveTask node entry: park the token
// awaiting the task's message (with resolved correlation key) and arm any
// boundary events attached to the ReceiveTask host.
type receiveTaskStrategy struct{}

func (receiveTaskStrategy) enter(c *stepCtx, tok *Token, node model.Node) ([]Command, bool, error) {
	rt := node.(activity.ReceiveTask)
	resolvedKey, err := c.eval.EvalString(rt.CorrelationKey, c.s.Variables)
	if err != nil {
		return nil, false, fmt.Errorf("workflow-engine: receive task %q correlation key: %w", node.ID(), err)
	}
	tok.State = TokenWaitingCommand
	tok.AwaitMessage = rt.MessageName
	tok.AwaitMessageKey = resolvedKey
	// Arm the node's in-wait reminder, if configured. For a ReceiveTask the
	// reminder is cancelled by the parked token (cancelKey = tok.ID) when the
	// awaited message resolves it.
	cmds, err := armWaitReminder(c, tok, node, tok.ID, nil)
	if err != nil {
		return cmds, false, err
	}
	bndCmds, err := armBoundaries(c.tdef, c.s, tok.ID, node.ID(), c.at, c.eval)
	if err != nil {
		return cmds, false, err
	}
	cmds = append(cmds, bndCmds...)
	return cmds, false, nil
}

// sendTaskStrategy handles KindSendTask node entry: emit a fire-and-forget
// SendMessage command (carrying the resolved correlation key and a copy of the
// instance variables) and AUTO-ADVANCE the token along its single outgoing flow.
// The engine does not park or wait for delivery — the consumer-wired message sink
// owns routing (intra-engine delivery, external publish, or both); ADR-0060.
type sendTaskStrategy struct{}

func (sendTaskStrategy) enter(c *stepCtx, tok *Token, node model.Node) ([]Command, bool, error) {
	st := node.(activity.SendTask)
	resolvedKey, err := c.eval.EvalString(st.CorrelationKey, c.s.Variables)
	if err != nil {
		return nil, false, fmt.Errorf("workflow-engine: send task %q correlation key: %w", node.ID(), err)
	}
	cmds := []Command{SendMessage{
		Name:           st.MessageName,
		CorrelationKey: resolvedKey,
		Payload:        copyVars(c.s.Variables),
	}}
	c.s.moveAlongSingleFlow(c.tdef, tok, c.at)
	// tok.State stays TokenActive (auto-advance): drive() derives stopped=false.
	return cmds, false, nil
}

// startEventStrategy handles KindStartEvent node entry.
type startEventStrategy struct{}

func (startEventStrategy) enter(c *stepCtx, tok *Token, node model.Node) ([]Command, bool, error) {
	c.s.moveAlongSingleFlow(c.tdef, tok, c.at)
	// tok.State stays TokenActive (auto-advance): drive() derives stopped=false.
	return nil, false, nil
}

// endEventStrategy handles KindEndEvent node entry.
//
// Stopped semantics: most paths stop (tok.State set to TokenWaitingCommand so
// drive() sees stopped=true). The "break" paths (scope still has tokens, or
// child scopes still running) leave tok.State==TokenActive so drive() sees
// stopped=false and keeps advancing the next active token.
type endEventStrategy struct{}

func (endEventStrategy) enter(c *stepCtx, tok *Token, node model.Node) ([]Command, bool, error) {
	// A non-normal EndEvent short-circuits the per-scope completion logic below
	// (ADR-0127). EndTerminate cancels ALL remaining parallel work and ends the
	// whole instance at the outcome-selected status (ADR-0119); EndError throws
	// ev.ErrorCode from the token's scope.
	if ev, ok := node.(event.EndEvent); ok {
		switch ev.Behavior {
		case event.EndTerminate:
			return forceTerminate(c, ev)
		case event.EndError:
			// propagateError walks the scope chain to a matching boundary error
			// handler (may catch + recover) or fails the instance.
			currentScopeID := tok.ScopeID
			c.s.consumeToken(tok, c.at)
			errCmds, propErr := propagateError(c.def, c.s, currentScopeID, "", "", ev.ErrorCode, nil, c.at, c.mode, c.eval, false)
			if propErr != nil {
				return nil, false, propErr
			}
			return errCmds, true, nil
		}
	}

	var cmds []Command
	// An EndEvent behaves differently depending on whether the token is at the
	// root scope or inside a sub-process scope:
	//   - Root scope (tok.ScopeID == ""): consume the token; when no tokens
	//     remain anywhere, the instance is complete → CompleteInstance.
	//   - Sub-process scope: consume the inner token; when the scope drains
	//     (tokensInScope == 0), close the scope and resume the parent by placing
	//     a token on the sub-process activity's outgoing flow in the parent scope.
	currentScopeID := tok.ScopeID
	c.s.consumeToken(tok, c.at)

	if currentScopeID == "" {
		// Root scope: instance completion when all tokens are gone.
		if len(c.s.Tokens) == 0 {
			c.s.Status = StatusCompleted
			ended := c.at
			c.s.EndedAt = &ended
			cmds = append(cmds, CompleteInstance{Result: copyVars(c.s.Variables)})
		}
	} else {
		// Sub-process scope: check whether the scope is now empty.
		// We use tokensInScope for the immediate scope; child scope tokens have
		// a different ScopeID (the child scope's ID), so they do NOT count here.
		if c.s.tokensInScope(currentScopeID) == 0 {
			scope := c.s.scopeByID(currentScopeID)
			if scope == nil {
				return cmds, false, fmt.Errorf("workflow-engine: sub-process end: scope %q not found", currentScopeID)
			}
			subNodeID := scope.NodeID
			parentScopeID := scope.ParentID

			// Determine whether this scope belongs to an event sub-process node
			// (a SubProcess with an event-triggered inner start) in the parent
			// definition. Event sub-process scope exit is handled
			// differently from regular sub-process scope exit:
			//   - Non-interrupting: just close this child scope; the enclosing scope
			//     keeps running (its tokens are still there).
			//   - Interrupting: the event sub-process replaces the enclosing scope;
			//     on completion, it closes the enclosing scope and resumes from the
			//     enclosing scope's parent (grandparent level). The enclosing scope
			//     was intentionally kept open (its tokens cancelled) so that we can
			//     check for remaining non-interrupting children before exiting.
			//
			// Detect an ESP child scope by checking the NodeID in the parent
			// definition regardless of whether parentScopeID is "" (root scope), so
			// root-level ESPs are recognized rather than falling into the regular
			// sub-process branch (which would error with "no outgoing flows").
			isEventSubprocess := false
			parentDef, pErr := defForScope(c.def, c.s, parentScopeID)
			if pErr == nil {
				if espNode, ok2 := parentDef.Node(subNodeID); ok2 {
					_, _, _, isEventSubprocess = eventSubprocessNested(espNode)
				}
			}

			if isEventSubprocess {
				// Event sub-process scope drained.
				// Close this child scope.
				c.s.closeScope(currentScopeID)

				// Handle root-level ESP (parentScopeID == "") distinctly from
				// nested ESP (parentScopeID != ""). The root scope is implicit (no Scope
				// object exists for it), so scopeByID("") always returns nil. We must
				// NOT treat that nil as "enclosing scope already closed".
				if parentScopeID == "" {
					// Root-level event sub-process.
					// Non-interrupting: the root scope still has tokens → just close child.
					if c.s.tokensInScope("") > 0 {
						// stopped=false: tok.State left as TokenActive (consumed token,
						// but continuation tokens exist; keep driving).
						return cmds, false, nil
					}
					// Check if any other child scopes of the root still have tokens.
					hasOtherRootChildren := false
					for _, sc := range c.s.Scopes {
						if sc.ParentID == "" && sc.ID != currentScopeID {
							if c.s.tokensInScope(sc.ID) > 0 {
								hasOtherRootChildren = true
								break
							}
						}
					}
					if hasOtherRootChildren {
						// stopped=false: other children still running.
						return cmds, false, nil
					}
					// Interrupting root-level ESP completed: all root tokens were cancelled
					// and no sibling child scopes remain. The instance is now complete.
					// Cancel any remaining ESP arms for the root scope.
					for _, timerID := range c.s.removeEventTriggeredSubprocessArmsForScope("") {
						cmds = append(cmds, CancelTimer{TimerID: timerID})
					}
					// Instance completes: all tokens gone, no active root children.
					if len(c.s.Tokens) == 0 {
						c.s.Status = StatusCompleted
						ended := c.at
						c.s.EndedAt = &ended
						cmds = append(cmds, CompleteInstance{Result: copyVars(c.s.Variables)})
					}
					// stopped=true (original break path that falls through to stopped=true).
					tok.State = TokenWaitingCommand
					return cmds, false, nil
				}

				// Nested event sub-process (parentScopeID != "").
				// Check what kind of event sub-process this is:
				// If the parent scope (enclosingScopeID) still has tokens or is still
				// a normal running scope, this was NON-interrupting → just close child.
				// If the parent scope has 0 tokens (they were all cancelled by interrupting
				// fire) AND no other child scopes of the parent have tokens, the
				// interrupting event sub-process is done → close enclosing scope and
				// resume the grandparent.
				enclosingScope := c.s.scopeByID(parentScopeID)
				if enclosingScope == nil {
					// Enclosing scope was already closed (defensive).
					// stopped=false: leave tok.State as TokenActive.
					return cmds, false, nil
				}
				if c.s.tokensInScope(parentScopeID) > 0 {
					// Enclosing scope still has tokens → non-interrupting case.
					// Child is done; enclosing scope keeps running. No further action.
					// stopped=false: leave tok.State as TokenActive.
					return cmds, false, nil
				}
				// No tokens in enclosing scope. Check if any other children still running.
				hasOtherChildren := false
				for _, sc := range c.s.Scopes {
					if sc.ParentID == parentScopeID && sc.ID != currentScopeID {
						if c.s.tokensInScope(sc.ID) > 0 {
							hasOtherChildren = true
							break
						}
					}
				}
				if hasOtherChildren {
					// stopped=false: leave tok.State as TokenActive.
					return cmds, false, nil
				}
				// Interrupting event sub-process completed: close enclosing scope and
				// resume in the grandparent.
				grandparentScopeID := enclosingScope.ParentID
				enclosingNodeID := enclosingScope.NodeID
				// Cancel remaining event sub-process arms for the enclosing scope.
				for _, timerID := range c.s.removeEventTriggeredSubprocessArmsForScope(parentScopeID) {
					cmds = append(cmds, CancelTimer{TimerID: timerID})
				}
				c.s.closeScope(parentScopeID)

				// Resume execution: place a token on the enclosing sub-process
				// activity's outgoing flow in the grandparent scope.
				grandparentDef, gpErr := defForScope(c.def, c.s, grandparentScopeID)
				if gpErr != nil {
					return cmds, false, fmt.Errorf("workflow-engine: event sub-process exit: %w", gpErr)
				}
				if grandparentScopeID == "" {
					// Grandparent is the root scope.
					outs := grandparentDef.Outgoing(enclosingNodeID)
					if len(outs) == 0 {
						// Root scope: no outgoing flows from the sub-process → instance completes.
						if len(c.s.Tokens) == 0 {
							c.s.Status = StatusCompleted
							ended := c.at
							c.s.EndedAt = &ended
							cmds = append(cmds, CompleteInstance{Result: copyVars(c.s.Variables)})
						}
					} else {
						// Root scope: place token on sub-process outgoing flow target.
						c.s.placeToken(outs[0].Target, c.at)
					}
				} else {
					outs := grandparentDef.Outgoing(enclosingNodeID)
					if len(outs) == 0 {
						return cmds, false, fmt.Errorf("workflow-engine: event sub-process exit: enclosing node %q has no outgoing flows in grandparent definition", enclosingNodeID)
					}
					c.s.placeTokenInScope(outs[0].Target, grandparentScopeID, c.at)
				}
			} else {
				// Regular sub-process scope. Check if there are any active child scopes
				// (non-interrupting event sub-processes running alongside).
				hasActiveChildren := false
				for _, sc := range c.s.Scopes {
					if sc.ParentID == currentScopeID {
						if c.s.tokensInScope(sc.ID) > 0 {
							hasActiveChildren = true
							break
						}
					}
				}
				if hasActiveChildren {
					// Still waiting for child scopes to drain. Do not exit this scope yet.
					// stopped=false: leave tok.State as TokenActive.
					return cmds, false, nil
				}

				// Scope drained (and no active children): close it and resume in parent.
				// Cancel any still-armed event sub-process arms for this scope.
				for _, timerID := range c.s.removeEventTriggeredSubprocessArmsForScope(currentScopeID) {
					cmds = append(cmds, CancelTimer{TimerID: timerID})
				}
				c.s.archiveCompensations(currentScopeID)
				c.s.closeScope(currentScopeID)

				// Resolve the parent definition and find the sub-process activity's
				// outgoing flow in the parent scope.
				parentDef, err := defForScope(c.def, c.s, parentScopeID)
				if err != nil {
					return cmds, false, fmt.Errorf("workflow-engine: sub-process exit: %w", err)
				}

				// If the sub-process node itself carries a CompensateAction, record
				// it in the parent scope. The snapshot is taken after the scope is
				// closed (consistent: the sub-process completed at this point).
				if spNode, spOK := parentDef.Node(subNodeID); spOK {
					if sp, spIsSubProc := spNode.(activity.SubProcess); spIsSubProc && sp.CompensateAction != "" {
						c.s.recordCompensation(parentScopeID, subNodeID, sp.CompensateAction, c.at, copyVars(c.s.Variables))
					}
				}

				outs := parentDef.Outgoing(subNodeID)
				if len(outs) == 0 {
					return cmds, false, fmt.Errorf("workflow-engine: sub-process exit: node %q has no outgoing flows in parent definition", subNodeID)
				}
				// Place a token on the first outgoing flow's target in the parent scope.
				c.s.placeTokenInScope(outs[0].Target, parentScopeID, c.at)
			}
		}
	}
	// Token consumed (end event). In Micro mode, stop after this node-advance
	// so the newly placed continuation token (if any) is processed in the next
	// Step call. Paths that returned early above leave tok.State==TokenActive
	// (stopped=false); this path sets tok.State=TokenWaitingCommand so drive() sees
	// stopped=true.
	tok.State = TokenWaitingCommand
	return cmds, false, nil
}

// forceTerminate implements a force-termination end event (ADR-0119): it cancels
// all remaining parallel work — open tasks, timers, boundaries/arms, and event
// sub-process arms — then ends the instance at the outcome-selected status
// (OutcomeAbort → StatusTerminated + FailInstance, OutcomeComplete →
// StatusCompleted + CompleteInstance). It mirrors the immediate-termination tail
// of handleCancelRequested and returns halt=true so drive() exits immediately:
// the instance is terminal and its tokens have been dropped.
//
// Scope-agnostic: it terminates the whole instance regardless of the end
// event's scope. A force-termination end firing inside a sub-process scope still
// ends the entire instance; scoped (sub-process-local) force-termination is not
// yet modeled.
//
// Intentionally does not invoke node/def CancelActions or run compensation —
// this is a modeled terminate, not an admin cancel (unlike handleCancelRequested).
func forceTerminate(c *stepCtx, ev event.EndEvent) ([]Command, bool, error) {
	ended := c.at
	c.s.EndedAt = &ended
	// Close every open visit and drop all tokens (including this end-event token).
	for i := range c.s.Tokens {
		tok := &c.s.Tokens[i]
		c.s.closeVisit(tok.ID, tok.NodeID, c.at)
	}
	c.s.Tokens = nil

	// Reconcile open human tasks before the terminal command (matches ADR-0088).
	cmds := c.s.cancelOpenTasks()

	if ev.Outcome == event.OutcomeAbort {
		c.s.Status = StatusTerminated
		reason := ev.TerminationReason
		if reason == "" {
			reason = "force-terminated"
		}
		cmds = append(cmds, FailInstance{Err: reason})
	} else {
		c.s.Status = StatusCompleted
		cmds = append(cmds, CompleteInstance{Result: copyVars(c.s.Variables)})
	}

	cmds = append(cmds, c.s.cancelAllTimers()...)
	cmds = append(cmds, c.s.cancelAllArmsAndBoundaries()...)
	for _, timerID := range c.s.removeAllEventTriggeredSubprocessArms() {
		cmds = append(cmds, CancelTimer{TimerID: timerID})
	}
	return cmds, true, nil
}

// subProcessStrategy handles KindSubProcess node entry.
type subProcessStrategy struct{}

func (subProcessStrategy) enter(c *stepCtx, tok *Token, node model.Node) ([]Command, bool, error) {
	sp := node.(activity.SubProcess)
	var cmds []Command
	// Embedded sub-process entry: open a scope, place a token on the nested
	// start node, and consume the sub-process activity token (it is "inside" now).
	if sp.Subprocess == nil {
		// Defensive: a KindSubProcess without a Subprocess definition cannot
		// execute; park to avoid infinite drive loop. model.Validate prevents this.
		tok.State = TokenWaitingCommand
		return cmds, false, nil
	}
	innerStarts := sp.Subprocess.StartNodes()
	if len(innerStarts) == 0 {
		return cmds, false, fmt.Errorf("workflow-engine: sub-process %q: nested definition has no start node", node.ID())
	}
	// An embedded sub-process is entered INLINE at its MANUAL (trigger-less)
	// start; any event-triggered starts in the nested def are ESP-style arms
	// handled by armEventTriggeredSubprocesses below, not the inline entry point. Once
	// multi-start became legal in nested defs (ADR-0121), innerStarts[0] could be
	// an event-start, so resolve the manual start explicitly.
	manualStart, msErr := resolveManualStart(sp.Subprocess)
	if msErr != nil {
		return cmds, false, fmt.Errorf("workflow-engine: sub-process %q: %w", node.ID(), msErr)
	}
	// Open a scope parented to the current token's scope.
	scopeID := c.s.openScope(node.ID(), tok.ScopeID)
	// Place the inner manual-start token in the new scope.
	c.s.placeTokenInScope(manualStart, scopeID, c.at)
	// Consume the sub-process activity token (execution is now "inside").
	c.s.consumeToken(tok, c.at)
	// Arm any event sub-process nodes (SubProcess with an event-triggered inner
	// start) defined inside this sub-process's nested definition. They are scoped
	// to the newly opened scope.
	espCmdsScope, espErrScope := armEventTriggeredSubprocesses(sp.Subprocess, c.s, scopeID, c.at, c.eval)
	if espErrScope != nil {
		return cmds, false, espErrScope
	}
	cmds = append(cmds, espCmdsScope...)
	// outer token consumed, inner token active: stopped=true.
	tok.State = TokenWaitingCommand
	return cmds, false, nil
}

// armWaitReminder arms a node's in-wait reminder if one is configured, appending
// the ScheduleTimer command and its timer record to cmds and returning the result.
// Recurrence is native to the scheduler: it re-fires on the interval on its own,
// so the engine arms once here and handleReminderFired only runs the reminder
// action per fire. cancelKey is the token whose resume/interrupt cancels the
// reminder — the human-task token for a UserTask, the parked token id for a
// ReceiveTask or IntermediateCatchEvent.
func armWaitReminder(c *stepCtx, tok *Token, node model.Node, cancelKey string, cmds []Command) ([]Command, error) {
	rawSpec, _ := model.WaitActionOf(node)
	reminderSpec, err := ResolveTrigger(c.eval, rawSpec, c.s.Variables)
	if err != nil {
		return cmds, fmt.Errorf("workflow-engine: reminder node %q: %w", node.ID(), err)
	}
	if reminderSpec.IsZero() {
		return cmds, nil
	}
	reminderTimerID := c.s.nextTimerID()
	cmds = append(cmds, ScheduleTimer{
		TimerID: reminderTimerID,
		Token:   tok.ID,
		Trigger: reminderSpec,
		Kind:    TimerInWait,
	})
	c.s.Timers = append(c.s.Timers, timerRecord{
		TimerID:   reminderTimerID,
		Kind:      TimerInWait,
		Token:     tok.ID,
		TaskToken: cancelKey,
		NodeID:    node.ID(),
		ScopeID:   tok.ScopeID,
	})
	return cmds, nil
}

// userTaskStrategy handles KindUserTask node entry.
type userTaskStrategy struct{}

func (userTaskStrategy) enter(c *stepCtx, tok *Token, node model.Node) ([]Command, bool, error) {
	ut := node.(activity.UserTask)
	var cmds []Command
	taskToken := c.s.nextTaskToken()
	spec := authz.AuthzSpec{
		Roles:      ut.EligibleRoles,
		Privileges: ut.EligiblePrivileges,
		Attribute:  ut.EligibleExpr,
	}
	ht := humantask.HumanTask{
		TaskToken:   taskToken,
		InstanceID:  c.s.InstanceID,
		NodeID:      node.ID(),
		Eligibility: spec,
		State:       humantask.Unclaimed,
		CreatedAt:   c.at,
	}
	if ut.Manual && ut.ManualImmediate {
		// Immediate manual task: no actor acts on it, so it never parks. Record a
		// completed task for audit (mirrors the state handleHumanCompleted sets)
		// and advance the token along its single outgoing flow immediately. No
		// eligibility check, no payload, no deadline/reminder/boundary arming —
		// none of those are meaningful without a wait period.
		ht.State = humantask.Completed
		c.s.Tasks = append(c.s.Tasks, ht)
		c.s.moveAlongSingleFlow(c.tdef, tok, c.at)
		// tok.State is left TokenActive (unchanged by moveAlongSingleFlow), so
		// drive()'s stopped = tok.State != TokenActive is false and the loop
		// continues to the next node for this token — same contract as
		// exclusiveGatewayStrategy.enter's pass-through.
		return nil, false, nil
	}
	// If the node carries a deadline, schedule the deadline timer and record the
	// deadline on the HumanTask so callers can surface the due date.
	deadlineSpec, err := ResolveTrigger(c.eval, ut.DeadlineTimer, c.s.Variables)
	if err != nil {
		return cmds, false, fmt.Errorf("workflow-engine: deadline node %q: %w", node.ID(), err)
	}
	if !deadlineSpec.IsZero() {
		deadlineTimerID := c.s.nextTimerID()
		cmds = append(cmds, ScheduleTimer{
			TimerID: deadlineTimerID,
			Token:   tok.ID,
			Trigger: deadlineSpec,
			Kind:    TimerDeadline,
		})
		c.s.Timers = append(c.s.Timers, timerRecord{
			TimerID:   deadlineTimerID,
			Kind:      TimerDeadline,
			Token:     tok.ID,
			TaskToken: taskToken,
			NodeID:    node.ID(),
			ScopeID:   tok.ScopeID,
		})
		// Surface the human-task due date ONLY when the resolved deadline reduces
		// to a concrete one-shot: an absolute time is the due date directly, and a
		// concrete delay makes it c.at + delay. Recurring/native forms have no
		// single due instant, so DueAt stays nil (the scheduler owns their firing).
		if at, ok := deadlineSpec.AbsTime(); ok {
			due := at
			ht.DueAt = &due
		} else if d, ok := deadlineSpec.Duration(); ok {
			due := c.at.Add(d)
			ht.DueAt = &due
		}
	}
	// Arm the node's in-wait reminder, if configured. For a UserTask the reminder
	// is cancelled by the human-task token (cancelKey = taskToken), preserving the
	// original behaviour.
	cmds, err = armWaitReminder(c, tok, node, taskToken, cmds)
	if err != nil {
		return cmds, false, err
	}
	c.s.Tasks = append(c.s.Tasks, ht)
	cmds = append(cmds, AwaitHuman{TaskToken: taskToken, Eligibility: spec})
	tok.State = TokenWaitingCommand
	tok.AwaitCommand = taskToken
	// Arm any boundary events attached to this host activity.
	bndCmds, err := armBoundaries(c.tdef, c.s, tok.ID, node.ID(), c.at, c.eval)
	if err != nil {
		return cmds, false, err
	}
	cmds = append(cmds, bndCmds...)
	// token parked: stopped=true (tok.State == TokenWaitingCommand != TokenActive).
	return cmds, false, nil
}

// intermediateCatchEventStrategy handles KindIntermediateCatchEvent node entry.
type intermediateCatchEventStrategy struct{}

func (intermediateCatchEventStrategy) enter(c *stepCtx, tok *Token, node model.Node) ([]Command, bool, error) {
	ice := node.(event.IntermediateCatchEvent)
	var cmds []Command
	timerSpec, err := ResolveTrigger(c.eval, ice.Timer, c.s.Variables)
	if err != nil {
		return cmds, false, fmt.Errorf("workflow-engine: timer node %q: %w", node.ID(), err)
	}
	if !timerSpec.IsZero() {
		timerID := c.s.nextTimerID()
		cmds = append(cmds, ScheduleTimer{
			TimerID: timerID,
			Token:   tok.ID,
			Trigger: timerSpec,
			Kind:    TimerIntermediate,
		})
		tok.State = TokenWaitingCommand
		tok.AwaitCommand = timerID
	} else if ice.SignalName != "" {
		// Signal intermediate catch event: park the token awaiting the signal.
		// The SignalReceived trigger (broadcast) will resume it later.
		tok.State = TokenWaitingCommand
		tok.AwaitSignal = ice.SignalName
	} else if ice.MessageName != "" {
		// Message intermediate catch event: park the token awaiting the message.
		// Evaluate the correlation key (if set) now against instance variables
		// for determinism; store the resolved key on the token.
		resolvedKey, err := c.eval.EvalString(ice.CorrelationKey, c.s.Variables)
		if err != nil {
			return cmds, false, fmt.Errorf("workflow-engine: message node %q correlation key: %w", node.ID(), err)
		}
		tok.State = TokenWaitingCommand
		tok.AwaitMessage = ice.MessageName
		tok.AwaitMessageKey = resolvedKey
	} else {
		// Non-timer, non-signal, non-message intermediate catch event: park.
		// Further event variants arrive in later plans.
		tok.State = TokenWaitingCommand
	}
	// Arm the node's in-wait reminder, if configured. It is cancelled by the
	// parked token (cancelKey = tok.ID) when the awaited signal/message/timer
	// resolves it. For the timer variant the reminder is a DIFFERENT TimerInWait
	// than the intermediate timer the token awaits via AwaitCommand.
	cmds, err = armWaitReminder(c, tok, node, tok.ID, cmds)
	if err != nil {
		return cmds, false, err
	}
	// token parked: stopped=true (tok.State == TokenWaitingCommand != TokenActive).
	return cmds, false, nil
}

// exclusiveGatewayStrategy handles KindExclusiveGateway node entry.
type exclusiveGatewayStrategy struct{}

func (exclusiveGatewayStrategy) enter(c *stepCtx, tok *Token, node model.Node) ([]Command, bool, error) {
	target, err := selectExclusiveTarget(c.tdef, c.s, node, c.eval)
	if err != nil {
		// cmds is carried here for a future error-handling plan (Plan 8);
		// Step currently discards StepResult on error, so partial commands
		// are intentionally not delivered today.
		return nil, false, err
	}
	c.s.moveTokenToTarget(tok, target, c.at)
	// tok.State stays TokenActive (auto-advance): drive() derives stopped=false.
	return nil, false, nil
}

// parallelGatewayStrategy handles KindParallelGateway node entry.
type parallelGatewayStrategy struct{}

func (parallelGatewayStrategy) enter(c *stepCtx, tok *Token, node model.Node) ([]Command, bool, error) {
	if len(c.tdef.Incoming(node.ID())) > 1 {
		c.s.tryParallelJoin(c.tdef, tok, node, tok.ScopeID, c.at)
		// tryParallelJoin always sets tok.State = TokenAtJoin first, then
		// conditionally removes all join-side tokens if the join fires.
		// Stopped semantics must match the original switch arm:
		//   - Join pending: token still in slice with State==TokenAtJoin → stopped=true.
		//   - Join fired: token removed from slice → stopped=false (auto-advance).
		// Re-read the token from the slice to distinguish the two cases:
		if t := c.s.tokenByID(tok.ID); t != nil && t.State == TokenAtJoin {
			// Pending: tok.State is already TokenAtJoin → drive() sees stopped=true.
		} else {
			// Fired: all join tokens consumed; reset tok.State to TokenActive so
			// drive() derives stopped=false and keeps advancing.
			tok.State = TokenActive
		}
	} else {
		c.s.forkParallel(c.tdef, tok, node, tok.ScopeID, c.at)
		// Fork: original token consumed, new active tokens placed. Auto-advance
		// so the loop picks up the first new token and processes it (in Micro,
		// it will stop when THAT token parks, not here at the fork itself).
		// tok.State is still TokenActive → stopped=false.
	}
	return nil, false, nil
}

// inclusiveGatewayStrategy handles KindInclusiveGateway node entry.
type inclusiveGatewayStrategy struct{}

func (inclusiveGatewayStrategy) enter(c *stepCtx, tok *Token, node model.Node) ([]Command, bool, error) {
	if len(c.tdef.Incoming(node.ID())) > 1 {
		c.s.tryInclusiveJoin(c.tdef, tok, node, tok.ScopeID, c.at)
		// tryInclusiveJoin always sets tok.State = TokenAtJoin first, then
		// conditionally removes all join-side tokens if the join fires.
		// Stopped semantics must match the original switch arm:
		//   - Join pending: token still in slice with State==TokenAtJoin → stopped=true.
		//   - Join fired: token removed from slice → stopped=false (auto-advance).
		// Re-read the token from the slice to distinguish the two cases:
		if t := c.s.tokenByID(tok.ID); t != nil && t.State == TokenAtJoin {
			// Pending: signal stop to drive() by leaving tok.State == TokenAtJoin.
			// (tok and t are the same pointer; already set by tryInclusiveJoin.)
		} else {
			// Fired: all join tokens consumed; reset tok.State to TokenActive so
			// drive() derives stopped=false and keeps advancing.
			tok.State = TokenActive
		}
	} else {
		if err := c.s.forkInclusive(c.tdef, tok, node, tok.ScopeID, c.at, c.eval); err != nil {
			return nil, false, err
		}
		// Fork: original token consumed, new active tokens placed. Auto-advance.
		// tok.State stays TokenActive → stopped=false.
	}
	return nil, false, nil
}

// eventBasedGatewayStrategy handles KindEventBasedGateway node entry.
type eventBasedGatewayStrategy struct{}

func (eventBasedGatewayStrategy) enter(c *stepCtx, tok *Token, node model.Node) ([]Command, bool, error) {
	var cmds []Command
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
	for _, f := range c.tdef.Outgoing(node.ID()) {
		catchNodeRaw, ok := c.tdef.Node(f.Target)
		if !ok {
			continue
		}
		ce, ok := catchNodeRaw.(event.IntermediateCatchEvent)
		if !ok {
			continue
		}
		ae := armedEvent{
			GatewayToken: tok.ID,
			CatchNode:    catchNodeRaw.ID(),
			Flow:         f.ID,
		}
		gwTimerSpec, err := ResolveTrigger(c.eval, ce.Timer, c.s.Variables)
		if err != nil {
			return cmds, false, fmt.Errorf("workflow-engine: event-gateway %q timer arm %q: %w", node.ID(), catchNodeRaw.ID(), err)
		}
		if !gwTimerSpec.IsZero() {
			timerID := c.s.nextTimerID()
			cmds = append(cmds, ScheduleTimer{
				TimerID: timerID,
				Token:   tok.ID,
				Trigger: gwTimerSpec,
				Kind:    TimerIntermediate,
			})
			ae.TimerID = timerID
		} else if ce.SignalName != "" {
			ae.Signal = ce.SignalName
		} else if ce.MessageName != "" {
			resolvedKey, err := c.eval.EvalString(ce.CorrelationKey, c.s.Variables)
			if err != nil {
				return cmds, false, fmt.Errorf("workflow-engine: event-gateway %q message arm %q correlation key: %w", node.ID(), catchNodeRaw.ID(), err)
			}
			ae.Message = ce.MessageName
			ae.MessageKey = resolvedKey
		}
		c.s.ArmedEvents = append(c.s.ArmedEvents, ae)
	}
	// gateway token parked: tok.State == TokenWaitingCommand → stopped=true.
	return cmds, false, nil
}

// callActivityStrategy handles KindCallActivity node entry.
type callActivityStrategy struct{}

func (callActivityStrategy) enter(c *stepCtx, tok *Token, node model.Node) ([]Command, bool, error) {
	ca := node.(activity.CallActivity)
	var cmds []Command
	// Call activity: emit StartSubInstance and park the token. The runtime
	// resolves DefRef via a DefinitionRegistry, runs the child to completion,
	// and returns a SubInstanceCompleted / SubInstanceFailed trigger that
	// resumes this parked token. No scope is opened here; the child instance
	// is fully isolated (separate instance lifecycle).
	//
	// Input: pass a copy of the current process variables so the child starts
	// with the parent's context. The parent does NOT read/write the child's
	// variables during the child's execution; they are merged on completion.
	cmdID := c.s.nextCommandID()
	cmds = append(cmds, StartSubInstance{
		CommandID: cmdID,
		DefRef:    ca.DefRef,
		Input:     copyVars(c.s.Variables),
	})
	tok.State = TokenWaitingCommand
	tok.AwaitCommand = cmdID
	// token parked: tok.State == TokenWaitingCommand → stopped=true.
	return cmds, false, nil
}

// intermediateThrowEventStrategy handles KindIntermediateThrowEvent node entry.
// A throw either emits a signal (broadcast, fire-and-forget) or, with no
// signal set, parks for future plans (e.g. message/error throw). Compensation
// throws are handled by the separate compensationThrowEventStrategy (ADR-0120);
// IntermediateThrowEvent does not carry compensation behaviour.
type intermediateThrowEventStrategy struct{}

func (intermediateThrowEventStrategy) enter(c *stepCtx, tok *Token, node model.Node) ([]Command, bool, error) {
	ite := node.(event.IntermediateThrowEvent)
	var cmds []Command
	if ite.SignalName != "" {
		// Signal intermediate throw: emit ThrowSignal and continue along the
		// single outgoing flow. The runtime broadcasts the signal; the engine
		// does not wait for delivery (fire-and-forget from the engine's view).
		cmds = append(cmds, ThrowSignal{
			Name:    ite.SignalName,
			Payload: nil, // no per-instance payload from throw nodes in this plan
		})
		c.s.moveAlongSingleFlow(c.tdef, tok, c.at)
		// Auto-advance: signal throw is fire-and-forget; tok.State == TokenActive → stopped=false.
	} else {
		// Non-signal intermediate throw: park for future plans (e.g. message
		// throw, error throw). Parking avoids an infinite drive loop.
		tok.State = TokenWaitingCommand
		// token parked: tok.State == TokenWaitingCommand → stopped=true.
	}
	return cmds, false, nil
}

// compensationThrowEventStrategy handles KindCompensationThrowEvent node entry
// (ADR-0120). A compensation throw runs completed compensable activities'
// compensation actions in reverse order, then RESUMES past the throw node
// (throw-then-continue — it never terminates the instance). It handles two
// forms, distinguished by CompensateRef:
//
//   - Targeted (CompensateRef != ""): runs the archived records of a specific
//     completed sub-process node (ArchivedCompensations[ref]): it uses that
//     archive as its record source, resumes, serializes/defers, and consumes with
//     single ownership (ArchiveKey cursor).
//   - Scope-wide (CompensateRef == ""): runs the throwing scope's completed
//     compensable activities. At the root scope the WHOLE-INSTANCE default first
//     consolidates archived sub-process records into RootCompensations (BPMN
//     conformant); WithScopeLocalCompensation (ScopeLocal) narrows it to
//     root-direct records only. The drained records are cleared on finish
//     (compensate-once — see stepCompensationFinish) so a second throw or a later
//     cancel cannot re-run them.
//
// Like the targeted branch, a scope-wide throw with no eligible records or no
// outgoing flow auto-advances (fire-and-forget); an overlapping walk already in
// flight defers this throw (ADR-0071 serialization, one walk at a time).
type compensationThrowEventStrategy struct{}

func (compensationThrowEventStrategy) enter(c *stepCtx, tok *Token, node model.Node) ([]Command, bool, error) {
	cte := node.(event.CompensationThrowEvent)
	var cmds []Command

	// The resume target is the throw's single outgoing successor. A throw with no
	// successor must NOT start a walk: stepCompensationFinish would see
	// ResumeNode == "" and take the terminal branch, wrongly terminating the
	// instance. Validate forbids a dead-end throw (ErrDeadEnd); this guards Step
	// defensively regardless.
	resumeNode := ""
	if out := c.tdef.Outgoing(node.ID()); len(out) > 0 {
		resumeNode = out[0].Target
	}
	tokScope := tok.ScopeID

	if cte.CompensateRef != "" {
		// Targeted throw: run the archived records for the referenced sub-process
		// node in reverse, resume past the throw, and consume the archive entry on
		// finish (single ownership via the ArchiveKey cursor).
		ref := cte.CompensateRef
		records := c.s.ArchivedCompensations[ref]
		if len(records) == 0 || resumeNode == "" {
			// No archived records (never ran or already compensated), OR no
			// outgoing flow — auto-advance, no InvokeAction emitted.
			c.s.moveAlongSingleFlow(c.tdef, tok, c.at)
		} else if c.s.Compensating.ActiveCmdID != "" {
			// SERIALIZE (ADR-0071): a walk is already in flight — defer this throw.
			tok.State = TokenWaitingCommand
			c.s.DeferredCompensationThrows = append(c.s.DeferredCompensationThrows, tok.ID)
		} else {
			// Start the throw compensation walk (resumeNode is non-empty here).
			c.s.consumeToken(tok, c.at)
			c.s.Status = StatusCompensating
			cmdID := c.s.nextCommandID()
			c.s.Compensating = compensationCursor{
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
			tok.State = TokenWaitingCommand
		}
	} else {
		// Scope-wide throw (ADR-0120): compensate the throwing scope's completed
		// compensable activities in reverse, then resume forward.
		//
		// consolidateArchiveIntoRoot MUST run only on the path that actually
		// commits to a walk — it MOVES ArchivedCompensations into RootCompensations
		// and nils the archive, so running it on a path that never compensates
		// would silently destroy the archive (review C1). The branch order below
		// keeps it off the dead-end and defer paths for exactly that reason.
		switch {
		case resumeNode == "":
			// Dead-end throw (no successor): auto-advance/park without starting a
			// walk. Do NOT consolidate — a throw that never compensates must leave
			// ArchivedCompensations intact for a later targeted throw or cancel (C1).
			c.s.moveAlongSingleFlow(c.tdef, tok, c.at)
		case c.s.Compensating.ActiveCmdID != "":
			// SERIALIZE (ADR-0071): defer this throw behind the in-flight walk. Do
			// NOT consolidate here — merging into RootCompensations under a live
			// cursor could corrupt the in-flight walk's record source. The deferred
			// token is re-driven through this strategy when the current walk finishes
			// (popOneDeferredThrow → drive), so consolidation happens then, on the
			// committing path below.
			tok.State = TokenWaitingCommand
			c.s.DeferredCompensationThrows = append(c.s.DeferredCompensationThrows, tok.ID)
		default:
			// Committing to a walk. Whole-instance default (BPMN conformant): merge
			// archived sub-process records into RootCompensations FIRST so they are
			// compensated too, THEN read the throwing scope's records. ScopeLocal
			// skips the merge — root-direct records only.
			if tokScope == "" && !cte.ScopeLocal {
				c.s.consolidateArchiveIntoRoot()
			}
			records := compensationRecordsForScope(c.s, tokScope)
			if len(records) == 0 {
				// Nothing to compensate even after consolidation — auto-advance
				// (harmless: nothing was there to merge).
				c.s.moveAlongSingleFlow(c.tdef, tok, c.at)
			} else {
				// Start the scope-wide walk. The cursor carries NO ArchiveKey, so
				// cursorRecords reads the throwing scope's live records; the finish
				// then clears them (compensate-once).
				c.s.consumeToken(tok, c.at)
				c.s.Status = StatusCompensating
				cmdID := c.s.nextCommandID()
				c.s.Compensating = compensationCursor{
					ScopeID:          tokScope,
					ResumeNode:       resumeNode,
					ResumeScope:      tokScope,
					NextIndex:        len(records) - 1,
					StartRecordCount: len(records),
					ActiveCmdID:      cmdID,
				}
				cmds = append(cmds, InvokeAction{
					CommandID: cmdID,
					Name:      records[len(records)-1].Action,
					Input:     copyVars(records[len(records)-1].Input),
				})
				tok.State = TokenWaitingCommand
			}
		}
	}
	return cmds, false, nil
}

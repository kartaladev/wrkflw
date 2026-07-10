package engine

import (
	"fmt"
	"time"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/humantask"
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

// nodeStrategy executes node-entry for one NodeKind.
// Implementations are stateless zero-size structs; the registry is built once
// at package init and never mutated.
type nodeStrategy interface {
	// enter runs the node's entry logic for tok. It returns the commands
	// produced for this token and, via tok mutations, updates the token state.
	// It does NOT append to a shared slice — drive() appends the returned
	// commands to its accumulator.
	//
	// halt signals that drive() must exit immediately (return cmds, nil) rather
	// than continuing to the next active token. Only errorEndEventStrategy
	// returns halt=true; all other strategies return halt=false.
	//
	// Stopped semantics: drive() derives stopped = tok.State != TokenActive
	// after a registry hit. Strategies that auto-advance (e.g. StartEvent)
	// leave tok.State == TokenActive so stopped=false. Strategies that park or
	// consume the token must ensure tok.State != TokenActive. For consumed tokens
	// (where consumeToken already removed them from the slice), strategies that
	// want stopped=true must set tok.State = TokenWaitingCommand explicitly.
	// Strategies that want stopped=false on a consumed token (e.g. EndEvent
	// sub-process "break" paths where a continuation token was placed) must leave
	// tok.State == TokenActive.
	enter(c *stepCtx, tok *Token, node model.Node) (cmds []Command, halt bool, err error)
}

// nodeStrategies maps each arm-bearing NodeKind to its strategy.
// Kinds NOT in this map (KindBoundaryEvent, KindEventSubProcess,
// KindUnspecified) fall through to the post-dispatch logic in drive() unchanged.
var nodeStrategies = map[model.NodeKind]nodeStrategy{
	model.KindServiceTask:            serviceTaskStrategy{},
	model.KindBusinessRuleTask:       businessRuleTaskStrategy{},
	model.KindReceiveTask:            receiveTaskStrategy{},
	model.KindSendTask:               sendTaskStrategy{},
	model.KindStartEvent:             startEventStrategy{},
	model.KindEndEvent:               endEventStrategy{},
	model.KindSubProcess:             subProcessStrategy{},
	model.KindUserTask:               userTaskStrategy{},
	model.KindIntermediateCatchEvent: intermediateCatchEventStrategy{},
	model.KindErrorEndEvent:          errorEndEventStrategy{},
	model.KindExclusiveGateway:       exclusiveGatewayStrategy{},
	model.KindParallelGateway:        parallelGatewayStrategy{},
	model.KindInclusiveGateway:       inclusiveGatewayStrategy{},
	model.KindEventBasedGateway:      eventBasedGatewayStrategy{},
	model.KindCallActivity:           callActivityStrategy{},
	model.KindIntermediateThrowEvent: intermediateThrowEventStrategy{},
	model.KindCompensationThrowEvent: compensationThrowEventStrategy{},
}

// serviceTaskStrategy handles KindServiceTask node entry.
type serviceTaskStrategy struct{}

func (serviceTaskStrategy) enter(c *stepCtx, tok *Token, node model.Node) ([]Command, bool, error) {
	if _, ok := node.(activity.ServiceTask); !ok {
		tok.State = TokenWaitingCommand
		return nil, false, nil
	}
	var cmds []Command
	cmdID := c.s.nextCommandID()
	cmds = append(cmds, InvokeAction{
		CommandID: cmdID,
		Name:      mainActionName(node),
		Scoped:    c.tdef.ScopedCatalog(),
		Input:     serviceActionInput(c.s, node),
	})
	tok.State = TokenWaitingCommand
	tok.AwaitCommand = cmdID
	// Arm any boundary events attached to this host activity.
	bndCmds, err := armBoundaries(c.tdef, c.s, tok.ID, node.ID(), c.at, c.eval)
	if err != nil {
		return cmds, false, err
	}
	cmds = append(cmds, bndCmds...)
	return cmds, false, nil
}

// businessRuleTaskStrategy handles KindBusinessRuleTask node entry. It mirrors
// serviceTaskStrategy: emit the primary InvokeAction (default-by-id name plus
// the scope-resolved inline action and scoped catalog), park the token, and arm
// boundary events.
type businessRuleTaskStrategy struct{}

func (businessRuleTaskStrategy) enter(c *stepCtx, tok *Token, node model.Node) ([]Command, bool, error) {
	if _, ok := node.(activity.BusinessRuleTask); !ok {
		tok.State = TokenWaitingCommand
		return nil, false, nil
	}
	var cmds []Command
	cmdID := c.s.nextCommandID()
	cmds = append(cmds, InvokeAction{
		CommandID: cmdID,
		Name:      mainActionName(node),
		Scoped:    c.tdef.ScopedCatalog(),
		Input:     serviceActionInput(c.s, node),
	})
	tok.State = TokenWaitingCommand
	tok.AwaitCommand = cmdID
	// Arm any boundary events attached to this host activity.
	bndCmds, err := armBoundaries(c.tdef, c.s, tok.ID, node.ID(), c.at, c.eval)
	if err != nil {
		return cmds, false, err
	}
	cmds = append(cmds, bndCmds...)
	return cmds, false, nil
}

// receiveTaskStrategy handles KindReceiveTask node entry: park the token
// awaiting the task's message (with resolved correlation key) and arm any
// boundary events attached to the ReceiveTask host.
type receiveTaskStrategy struct{}

func (receiveTaskStrategy) enter(c *stepCtx, tok *Token, node model.Node) ([]Command, bool, error) {
	rt, ok := node.(activity.ReceiveTask)
	if !ok {
		tok.State = TokenWaitingCommand
		return nil, false, nil
	}
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
	st, ok := node.(activity.SendTask)
	if !ok {
		tok.State = TokenWaitingCommand
		return nil, false, nil
	}
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
	// Force-termination (ADR-0119) short-circuits the normal per-scope completion
	// logic below: instead of consuming only this token, it cancels ALL remaining
	// parallel work and ends the whole instance at the outcome-selected status.
	if ev, ok := node.(event.EndEvent); ok && ev.ForceTermination {
		return forceTerminate(c, ev)
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
			parentDef, pErr := defForScope(c.def, c.s, parentScopeID)
			if pErr == nil {
				if espNode, ok2 := parentDef.Node(subNodeID); ok2 {
					_, isEventSubProcess = espNode.(event.EventSubProcess)
				}
			}

			if isEventSubProcess {
				// Event sub-process scope drained.
				// Close this child scope.
				c.s.closeScope(currentScopeID)

				// Fix 2: handle root-level ESP (parentScopeID == "") distinctly from
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
					for _, timerID := range c.s.removeEventSubprocessArmsForScope("") {
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
				for _, timerID := range c.s.removeEventSubprocessArmsForScope(parentScopeID) {
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
				for _, timerID := range c.s.removeEventSubprocessArmsForScope(currentScopeID) {
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
	for _, timerID := range c.s.removeAllEventSubprocessArms() {
		cmds = append(cmds, CancelTimer{TimerID: timerID})
	}
	return cmds, true, nil
}

// subProcessStrategy handles KindSubProcess node entry.
type subProcessStrategy struct{}

func (subProcessStrategy) enter(c *stepCtx, tok *Token, node model.Node) ([]Command, bool, error) {
	sp, ok := node.(activity.SubProcess)
	if !ok {
		tok.State = TokenWaitingCommand
		return nil, false, nil
	}
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
	// Open a scope parented to the current token's scope.
	scopeID := c.s.openScope(node.ID(), tok.ScopeID)
	// Place the inner start-event token in the new scope.
	c.s.placeTokenInScope(innerStarts[0].ID(), scopeID, c.at)
	// Consume the sub-process activity token (execution is now "inside").
	c.s.consumeToken(tok, c.at)
	// Arm any KindEventSubProcess nodes defined inside this sub-process's
	// nested definition. They are scoped to the newly opened scope.
	espCmdsScope, espErrScope := armEventSubprocesses(sp.Subprocess, c.s, scopeID, c.at, c.eval)
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
	ut, ok := node.(activity.UserTask)
	if !ok {
		tok.State = TokenWaitingCommand
		return nil, false, nil
	}
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
	ice, ok := node.(event.IntermediateCatchEvent)
	if !ok {
		tok.State = TokenWaitingCommand
		return nil, false, nil
	}
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

// errorEndEventStrategy handles KindErrorEndEvent node entry.
type errorEndEventStrategy struct{}

func (errorEndEventStrategy) enter(c *stepCtx, tok *Token, node model.Node) ([]Command, bool, error) {
	eee, ok := node.(event.ErrorEndEvent)
	if !ok {
		tok.State = TokenWaitingCommand
		return nil, false, nil
	}
	var cmds []Command
	// Error end event: throw an error with eee.ErrorCode from the token's
	// current scope. propagateError walks the scope chain outward looking for
	// a matching boundary error handler on the enclosing sub-process. An error
	// end event is not an activity node that carries a direct boundary, so we
	// pass "" as originatingNodeID (no direct-attachment check needed) and ""
	// as failingTokenID (the error-end token is already consumed above).
	currentScopeID := tok.ScopeID
	c.s.consumeToken(tok, c.at)
	errCmds, propErr := propagateError(c.def, c.s, currentScopeID, "", "", eee.ErrorCode, nil, c.at, c.mode, c.eval, false)
	if propErr != nil {
		// Real error from propagateError: surface it; drive() returns it as-is.
		return cmds, false, propErr
	}
	cmds = append(cmds, errCmds...)
	// propagateError either caught the error (routing a token to the recovery
	// flow and calling drive() internally) or failed the instance (terminal path).
	// Either way, drive() must exit immediately — identical to the original
	// switch arm's `return cmds, nil`. halt=true signals drive() to do so.
	// (tok.State need not be set: tok was already consumed above and is no
	// longer in s.Tokens; drive() exits before rechecking it.)
	return cmds, true, nil
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
	ca, ok := node.(activity.CallActivity)
	if !ok {
		tok.State = TokenWaitingCommand
		return nil, false, nil
	}
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
type intermediateThrowEventStrategy struct{}

func (intermediateThrowEventStrategy) enter(c *stepCtx, tok *Token, node model.Node) ([]Command, bool, error) {
	ite, ok := node.(event.IntermediateThrowEvent)
	if !ok {
		tok.State = TokenWaitingCommand
		return nil, false, nil
	}
	var cmds []Command
	if ite.CompensateRef != "" {
		// Compensation throw intermediate event (ADR-0039, Phase 3).
		// Runs the archived compensation records for the referenced sub-process
		// node in reverse order, then resumes execution past the throw node.
		// This is a localized walk — it does NOT call beginCompensation
		// (which cancels ALL tokens and is designed for full/partial rollbacks).
		ref := ite.CompensateRef
		records := c.s.ArchivedCompensations[ref]
		// Determine the resume node: the throw's single outgoing successor.
		resumeNode := ""
		if out := c.tdef.Outgoing(node.ID()); len(out) > 0 {
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
			c.s.moveAlongSingleFlow(c.tdef, tok, c.at)
			// stopped remains false: auto-advance (tok.State == TokenActive).
		} else if c.s.Compensating.ActiveCmdID != "" {
			// SERIALIZE (ADR-0071): a compensation walk is already in flight. The
			// single-cursor model permits at most ONE walk at a time; starting a
			// second here would overwrite the in-flight cursor and orphan the first
			// walk. This happens when two compensation throws in parallel branches
			// are processed in the SAME Macro drive pass. Park (defer) this throw:
			// do NOT consume the token, do NOT touch the cursor, emit no InvokeAction.
			// stepCompensationFinish re-activates exactly one deferred throw per finish,
			// draining the queue one walk at a time. The parked token is re-activated
			// (TokenActive) later, re-entering this handler when the cursor is clear.
			tok.State = TokenWaitingCommand
			c.s.DeferredCompensationThrows = append(c.s.DeferredCompensationThrows, tok.ID)
			// token parked: tok.State == TokenWaitingCommand → stopped=true.
		} else {
			// Start the throw compensation walk (resumeNode is non-empty here).
			// Remember the throw token's scope for correct placeTokenInScope on finish.
			tokScope := tok.ScopeID
			// Consume the throw token now (finish will place a fresh token at resumeNode).
			c.s.consumeToken(tok, c.at)
			// Set instance into compensation mode and stamp the cursor.
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
			// walk started; stopped=true: set tok.State to signal stop.
			tok.State = TokenWaitingCommand
		}
	} else if ite.SignalName != "" {
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
		// Non-signal, non-compensation intermediate throw: park for future plans
		// (e.g. message throw, error throw). Parking avoids an infinite drive loop.
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
//     completed sub-process node (ArchivedCompensations[ref]). This is the
//     behaviour ported from the legacy intermediateThrowEventStrategy compensation
//     branch — same archive source, resume, serialize/defer, and single-ownership
//     consume (ArchiveKey cursor).
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
	cte, ok := node.(event.CompensationThrowEvent)
	if !ok {
		tok.State = TokenWaitingCommand
		return nil, false, nil
	}
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
		// Targeted throw (ported verbatim from intermediateThrowEventStrategy's
		// CompensateRef branch): run the archived records for the referenced
		// sub-process node in reverse, resume past the throw, and consume the
		// archive entry on finish (single ownership via the ArchiveKey cursor).
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
		if tokScope == "" && !cte.ScopeLocal {
			// Whole-instance default (BPMN conformant): merge archived sub-process
			// records into RootCompensations so they are compensated too. ScopeLocal
			// skips this — root-direct records only.
			c.s.consolidateArchiveIntoRoot()
		}
		records := compensationRecordsForScope(c.s, tokScope)
		if len(records) == 0 || resumeNode == "" {
			// Nothing to compensate (or no successor) — auto-advance.
			c.s.moveAlongSingleFlow(c.tdef, tok, c.at)
		} else if c.s.Compensating.ActiveCmdID != "" {
			// SERIALIZE (ADR-0071): defer this throw behind the in-flight walk.
			tok.State = TokenWaitingCommand
			c.s.DeferredCompensationThrows = append(c.s.DeferredCompensationThrows, tok.ID)
		} else {
			// Start the scope-wide walk. The cursor carries NO ArchiveKey, so
			// cursorRecords reads the throwing scope's live records; the finish
			// then clears them (compensate-once).
			c.s.consumeToken(tok, c.at)
			c.s.Status = StatusCompensating
			cmdID := c.s.nextCommandID()
			c.s.Compensating = compensationCursor{
				ScopeID:     tokScope,
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
	}
	return cmds, false, nil
}

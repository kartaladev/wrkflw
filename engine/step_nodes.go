package engine

import (
	"context"
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
	// ctx is the Step caller's context, threaded through for trace-correlated
	// logging ONLY (ADR-0129) — never inspected for control flow. Storing it on
	// this struct is an accepted exception to "never store a context in a
	// struct": stepCtx is a short-lived, request-scoped bundle constructed fresh
	// by drive() for the duration of a single Step call and never persisted or
	// reused across calls, unlike a long-lived struct field.
	ctx  context.Context
	def  *model.ProcessDefinition
	tdef *model.ProcessDefinition
	s    *InstanceState
	at   time.Time
	// pol is the per-Step policy resolved once by dispatch and threaded here, so
	// every strategy evaluates through the same evaluator (ADR-0056) and reads
	// the same granularity. The remaining StepOptions fields stay in Step() scope
	// for the ActionFailed handler (effectiveRetryPolicy) only.
	pol stepPolicy
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
	tok.State = TokenWaiting
	tok.AwaitCommand = cmdID
	// Arm any boundary events attached to this host activity.
	bndCmds, err := armBoundaries(c.tdef, c.s, tok.ID, node.ID(), c.at, c.pol.eval)
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
	resolvedKey, err := c.pol.eval.EvalString(rt.CorrelationKey, c.s.Variables)
	if err != nil {
		return nil, false, fmt.Errorf("workflow-engine: receive task %q correlation key: %w", node.ID(), err)
	}
	tok.State = TokenWaiting
	tok.AwaitMessage = rt.MessageName
	tok.AwaitMessageKey = resolvedKey
	// Arm the node's in-wait reminder, if configured. For a ReceiveTask the
	// reminder is cancelled by the parked token (cancelKey = tok.ID) when the
	// awaited message resolves it.
	cmds, err := armWaitReminder(c, tok, node, tok.ID, nil)
	if err != nil {
		return cmds, false, err
	}
	bndCmds, err := armBoundaries(c.tdef, c.s, tok.ID, node.ID(), c.at, c.pol.eval)
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
	resolvedKey, err := c.pol.eval.EvalString(st.CorrelationKey, c.s.Variables)
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
// Stopped semantics: most paths stop (tok.State set to TokenWaiting so
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
			c.s.consumeTokenAs(tok, c.at, CloseKindErrored)
			errCmds, propErr := propagateError(c.ctx, c.def, c.s, currentScopeID, "", "", ev.ErrorCode, nil, c.at, c.pol, failFast)
			if propErr != nil {
				return nil, false, propErr
			}
			return errCmds, true, nil
		}
	}

	// An EndEvent behaves differently depending on whether the token is at the
	// root scope or inside a sub-process scope:
	//   - Root scope (tok.ScopeID == ""): consume the token; when no tokens
	//     remain anywhere, the instance is complete → CompleteInstance.
	//   - Sub-process scope: consume the inner token; when the scope drains
	//     (tokensInScope == 0), close the scope and resume the parent by placing
	//     a token on the sub-process activity's outgoing flow in the parent scope.
	currentScopeID := tok.ScopeID
	c.s.consumeToken(tok, c.at)

	var (
		cmds []Command
		stop bool
		err  error
	)
	if currentScopeID == "" {
		cmds, stop = exitRootScope(c), true
	} else {
		cmds, stop, err = exitSubprocessScope(c, currentScopeID)
	}
	if err != nil {
		return cmds, false, err
	}
	// Token consumed (end event). In Micro mode, stop after this node-advance
	// so the newly placed continuation token (if any) is processed in the next
	// Step call. stop=false leaves tok.State==TokenActive so drive() sees
	// stopped=false and keeps advancing; stop=true parks the token
	// (tok.State=TokenWaiting) so drive() sees stopped=true.
	if stop {
		tok.State = TokenWaiting
	}
	return cmds, false, nil
}

// exitRootScope handles KindEndEvent token consumption when the token is at
// the root scope (tok.ScopeID == ""): instance completion when all tokens are
// gone AND no compensation walk is outstanding. The token is always parked
// after this (drive() sees stopped=true).
//
// The cursor conjunct is ADR-0168. startCompensationWalk consumes the throwing
// token before parking the walk on Compensating.ActiveCmdID, so a sibling
// branch draining the last token makes len(Tokens) == 0 read true while the
// rollback is still outstanding; without the conjunct this site published
// CompleteInstance for a process whose compensation never finished, and the
// walk's own ActionCompleted was then refused by dispatch's terminal guard.
// Status.IsTerminal() cannot serve here: StatusCompensating is deliberately
// non-terminal so that very trigger can be dispatched.
func exitRootScope(c *stepCtx) []Command {
	if len(c.s.Tokens) == 0 && c.s.Compensating.ActiveCmdID == "" {
		return c.s.endInstance(StatusCompleted, c.at, CompleteInstance{Result: copyVars(c.s.Variables)})
	}
	return nil
}

// exitSubprocessScope handles KindEndEvent token consumption when the token
// is inside a sub-process scope. We use tokensInScope for the immediate
// scope; child scope tokens have a different ScopeID (the child scope's ID),
// so they do NOT count here. When the scope hasn't drained yet it reports
// stop=true with no commands (the original fall-through-to-park path);
// once drained it dispatches to the event-sub-process or regular-sub-process
// scope-close-and-resume logic depending on what currentScopeID's owning node
// is.
func exitSubprocessScope(c *stepCtx, currentScopeID string) ([]Command, bool, error) {
	var cmds []Command
	if c.s.tokensInScope(currentScopeID) != 0 {
		return cmds, true, nil
	}
	// A compensation throw walk still needs this scope (ADR-0171): hold the exit
	// exactly as the not-yet-drained return above does. Nothing is lost by
	// waiting — the walk's own resume places a token at the throw's successor
	// INSIDE this scope, and that token re-runs this exit once the cursor is
	// clear. Closing here instead destroyed the walk's record source (panic) or
	// its resume scope (permanent `defForScope: unknown scope`).
	if c.s.compensationWalkHoldsScope(currentScopeID) {
		return cmds, true, nil
	}
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
		return exitEventSubprocessScope(c, currentScopeID, parentScopeID)
	}
	return exitRegularSubprocessScope(c, currentScopeID, subNodeID, parentScopeID)
}

// exitEventSubprocessScope handles scope-close + resume when the drained
// scope belongs to an event sub-process (a SubProcess with an event-triggered
// inner start). It closes the child scope, then branches on root-level
// (parentScopeID == "") vs nested event sub-process exit. The root scope is
// implicit (no Scope object exists for it), so scopeByID("") always returns
// nil — the root-level branch must not treat that nil as "enclosing scope
// already closed".
func exitEventSubprocessScope(c *stepCtx, currentScopeID, parentScopeID string) ([]Command, bool, error) {
	// A descendant scope holding a live token must keep this scope from being
	// pruned: closeScope cascades (ADR-0130), so closing here would orphan that
	// token and wedge the instance permanently. The self check upstream (in
	// exitSubprocessScope) is exact-match and cannot see it — this site had NO
	// descendant check at all (ADR-0162 problem 6). stop=false matches the other
	// "not drained yet" returns on this path: stop=true would halt drive and park
	// the instance rather than letting the remaining tokens advance.
	if c.s.hasChildScopeWithTokens(currentScopeID, "") {
		return nil, false, nil
	}
	// Archive before closing, exactly as exitRegularSubprocessScope does. Without
	// this, compensable work completed inside any event sub-process is dropped on
	// its NORMAL exit.
	c.s.archiveCompensations(currentScopeID)
	c.s.closeScope(currentScopeID)

	if parentScopeID == "" {
		return exitRootEventSubprocessScope(c, currentScopeID)
	}
	return exitNestedEventSubprocessScope(c, currentScopeID, parentScopeID)
}

// exitRootEventSubprocessScope handles root-level event sub-process scope
// exit (parentScopeID == ""): when the root scope or a sibling child scope still
// has tokens, just close the child and keep running; otherwise the root-level
// event sub-process was the last thing running and the instance may now be
// complete. Which of the two applies is decided by DRAIN STATE, not by whether
// the event sub-process was interrupting — interrupting is only the common route
// to the second branch, and a non-interrupting one reaches it whenever it
// outlives the root scope's tokens.
func exitRootEventSubprocessScope(c *stepCtx, currentScopeID string) ([]Command, bool, error) {
	var cmds []Command
	// Non-interrupting: the root scope still has tokens → just close child.
	if c.s.tokensInScope("") > 0 {
		return cmds, false, nil
	}
	// Subtree, not the immediate scope: a grandchild holding the live token must
	// keep the root from being declared drained (ADR-0162).
	if c.s.hasChildScopeWithTokens("", currentScopeID) {
		return cmds, false, nil
	}
	// The root-level ESP has completed and the root scope holds no token, with no
	// sibling child scopes left. Interrupting is only the COMMON way to get here
	// (the interrupt cancelled the root tokens); a NON-interrupting root ESP
	// reaches this same branch whenever it outlives the root scope's own tokens —
	// measured, that is the shape
	// TestCompensationWalkBlocksRootEventSubprocessCompletion drives.
	//
	// Instance completes: all tokens gone, no compensation walk outstanding
	// (ADR-0168 — see exitRootScope for why the cursor conjunct is needed), no
	// active root children. endInstance's own cancelAllScheduledWork retires the
	// root ESP arms (and everything else the instance still owns), so the arm
	// retirement below is NOT hoisted above this branch: doing so would put
	// CancelTimers on both sides of CompleteInstance and break the
	// [task cancels…, terminal, scheduled-work cancels…] order every terminal path
	// now emits (ADR-0164).
	if len(c.s.Tokens) == 0 && c.s.Compensating.ActiveCmdID == "" {
		return append(cmds, c.s.endInstance(StatusCompleted, c.at,
			CompleteInstance{Result: copyVars(c.s.Variables)})...), true, nil
	}
	// NOT defensive, and NOT unreachable — this tail is LIVE. The two guards above
	// already established that the root scope holds no token and no child subtree
	// does either, and currentScopeID was drained and closed by the caller, so the
	// only thing the completion branch above can still decline is a LIVE
	// COMPENSATION CURSOR. That is reachable: a non-interrupting root-level event
	// sub-process whose body forks into a CompensateThrow and a plain end drains its
	// own scope with the walk outstanding.
	//
	// This comment previously read "DEFENSIVE, and unreachable today", reasoning
	// that "nothing can be left for len(c.s.Tokens) != 0 to find". ADR-0168's
	// conjunct changed this tail's entry condition, so both halves are restated
	// rather than inherited. Measured without the conjunct on
	// TestRootEventSubprocessExitBlocksCompletionUnderRootWalk, which reaches this
	// site with a ROOT-scope walk the ADR-0171 hold cannot match: the instance
	// went `completed`, published CompleteInstance, and the walk's InvokeAction
	// was dropped as stale in the same step. On the completing path the root ESP
	// arms are retired by endInstance's cancelAllScheduledWork, not here.
	//
	// This tail retires NOTHING, and that is a correction to ADR-0168 Decision 3.
	// It used to call removeEventTriggeredSubprocessArmsForScope("") — cleanup
	// written when the tail was believed to run only as an instance finished, and
	// documented afterwards as an "accepted cost". It is not a cost that can be
	// accepted: the deferral this tail implements can end in a RESUME, and the
	// resume does not re-arm (finishPlan.rearmRootESP is set only on the
	// full-reverse resume). Measured on the fixture
	// TestRootEventSubprocessExitKeepsRootArmsWhenTheWalkCanResume drives, arms
	// went 1 → 0 here, the walk resumed the instance to `running`, and a second
	// delivery of the arm's signal opened no scope at all — a NON-interrupting
	// root event sub-process, which ADR-0124 makes repeatable, silently stopped
	// being triggerable.
	//
	// Dropping the call leaks nothing. The arms retired here belong to the ROOT
	// scope, which is implicit and is never closed, so they cannot outlive their
	// scope; and whichever way the walk ends terminally, endInstance's
	// cancelAllScheduledWork sweeps them then. Re-arming on the throw resume was
	// considered instead and rejected: armEventTriggeredSubprocesses re-arms every
	// root event sub-process in the definition, which would resurrect an
	// INTERRUPTING one-shot arm that had already fired and been legitimately
	// retired.
	//
	// Contrast exitNestedEventSubprocessScope below, whose identical-looking tail
	// retires the ENCLOSING scope's arms and must keep doing so: that scope has
	// just been closed, and an arm outliving its scope is the defect that call
	// exists to prevent.
	return cmds, true, nil
}

// exitNestedEventSubprocessScope handles nested event sub-process scope exit
// (parentScopeID != ""). It either just closes the child scope, or ALSO closes
// the enclosing scope and resumes in the grandparent.
//
// Which of the two it does is decided purely by DRAIN STATE — whether the
// enclosing scope and its other children still hold tokens — and NOT by whether
// the event sub-process was interrupting. Interrupting is only the common way to
// reach the second branch (the interrupt cancelled the enclosing scope's tokens,
// so the event sub-process is the last thing running inside it). A
// NON-interrupting event sub-process reaches exactly the same branch whenever it
// OUTLIVES its enclosing scope: the enclosing scope drains normally but is held
// open by exitRegularSubprocessScope's child check, so when this event
// sub-process finally finishes it is the one that prunes the enclosing scope —
// which is why the archive below is not interrupt-specific.
// TestNestedEventSubprocessExitArchivesEnclosingScope drives that path.
func exitNestedEventSubprocessScope(c *stepCtx, currentScopeID, parentScopeID string) ([]Command, bool, error) {
	var cmds []Command
	enclosingScope := c.s.scopeByID(parentScopeID)
	if enclosingScope == nil {
		// Enclosing scope was already closed (defensive).
		return cmds, false, nil
	}
	if c.s.tokensInScope(parentScopeID) > 0 {
		// Enclosing scope is still working → close the child only and leave it
		// running. Deliberately EXACT-match, not subtree: a non-interrupting event
		// sub-process running alongside carries its own ScopeID, and counting the
		// subtree here would make a scope wait forever on a sibling it does not own
		// (ADR-0162).
		return cmds, false, nil
	}
	// No tokens in enclosing scope. Check if any other children still running —
	// SUBTREE-wide, so a grandchild holding the live token blocks the exit
	// (ADR-0162).
	if c.s.hasChildScopeWithTokens(parentScopeID, currentScopeID) {
		return cmds, false, nil
	}
	// The enclosing scope has fully drained and this event sub-process was the
	// last thing running inside it (whether because an interrupt emptied it or
	// because it drained normally and was held open for this child): close the
	// enclosing scope and resume in the grandparent.
	grandparentScopeID := enclosingScope.ParentID
	enclosingNodeID := enclosingScope.NodeID
	// The enclosing scope's remaining event sub-process arms are retired at each
	// non-completing return below rather than here. They MUST still be retired on
	// every one of those paths — leaving them armed after their scope closes is the
	// defect this call exists to prevent — but on the completing path endInstance's
	// own cancelAllScheduledWork owns the sweep, and retiring them here first would
	// put CancelTimers on both sides of CompleteInstance, breaking the
	// [task cancels…, terminal, scheduled-work cancels…] order every terminal path
	// now emits (ADR-0164). closeScope only prunes s.Scopes, so deferring the call
	// past it is state-equivalent.
	// Archive the enclosing scope's records before pruning it: it may have
	// recorded compensable work before it drained — whether an interrupt emptied
	// it or it completed normally and waited on this child (ADR-0162). Harmless
	// when cancelScopeSubtree already archived it on the interrupting path:
	// archiveCompensations nils the scope's records, so the second call returns
	// at its len == 0 guard.
	c.s.archiveCompensations(parentScopeID)
	c.s.closeScope(parentScopeID)

	// Resume execution: place a token on the enclosing sub-process
	// activity's outgoing flow in the grandparent scope.
	grandparentDef, gpErr := defForScope(c.def, c.s, grandparentScopeID)
	if gpErr != nil {
		return cmds, false, fmt.Errorf("workflow-engine: event sub-process exit: %w", gpErr)
	}
	// If the ENCLOSING sub-process node itself carries a CompensateAction, record
	// it in the grandparent scope — the same step exitRegularSubprocessScope
	// takes, in the same position (after the scope is closed, before the resume).
	// This exit closes that scope in strictly more cases since ADR-0162 added the
	// child-scope deferral to the regular exit, so omitting the record here left
	// the sub-process silently non-compensable. A record written here cannot
	// dangle on the error return below: Step discards the working clone whenever
	// a handler returns an error, so the state carrying it never escapes.
	recordSubProcessCompensation(c, grandparentDef, enclosingNodeID, grandparentScopeID)

	if found := resumeInParentScope(c, grandparentDef, enclosingNodeID, grandparentScopeID); !found {
		if grandparentScopeID == "" {
			// Root scope: no outgoing flows from the sub-process → instance completes,
			// unless a compensation walk is still outstanding (ADR-0168 — see
			// exitRootScope).
			//
			// ⚠ CORRECTED (ADR-0172). This comment previously claimed the conjunct
			// "DOES discriminate:
			// TestCompensationWalkBlocksNestedEventSubprocessCompletion reaches it,
			// and without the conjunct that fixture completed the instance", and
			// that the arm retirement below was "asserted by the named test".
			// BOTH claims are false, measured: deleting
			// `&& c.s.Compensating.ActiveCmdID == ""` leaves `go test ./engine/...`
			// at EXIT=0 with that test RUNNING and PASSING, and replacing the arm
			// retirement below with a no-op likewise leaves the suite green. The
			// named test's own docstring already says it does not reach here.
			//
			// The conjunct is kept as defence in depth — undemonstrated is not
			// unreachable — but it is UNCOVERED, and so is the arm retirement
			// below. Both are recorded as coverage gaps rather than deleted on the
			// strength of a green suite, which is the inference this repo has been
			// burned by before.
			if len(c.s.Tokens) == 0 && c.s.Compensating.ActiveCmdID == "" {
				return append(cmds, c.s.endInstance(StatusCompleted, c.at,
					CompleteInstance{Result: copyVars(c.s.Variables)})...), true, nil
			}
		} else {
			return cmds, false, fmt.Errorf("workflow-engine: event sub-process exit: enclosing node %q has no outgoing flows in grandparent definition", enclosingNodeID)
		}
	}
	// Not completing: retire the enclosing scope's remaining ESP arms (see above).
	cmds = appendCancelTimers(cmds, c.s.removeEventTriggeredSubprocessArmsForScope(parentScopeID))
	return cmds, true, nil
}

// exitRegularSubprocessScope handles scope-close + resume for a regular
// (non-event) sub-process scope: if a non-interrupting event sub-process is
// still running alongside it (an active child scope), the exit waits;
// otherwise it closes the scope, records the sub-process's compensation
// snapshot (if any), and resumes in the parent scope.
func exitRegularSubprocessScope(c *stepCtx, currentScopeID, subNodeID, parentScopeID string) ([]Command, bool, error) {
	var cmds []Command
	// Check if there are any active child scopes (non-interrupting event
	// sub-processes running alongside, or a nested sub-process still working).
	// SUBTREE-wide: a grandchild holding the live token leaves its own parent
	// scope empty, so a direct-children scan would declare the subtree drained
	// and closeScope would prune the grandchild out from under that token
	// (ADR-0162).
	if c.s.hasChildScopeWithTokens(currentScopeID, "") {
		// Still waiting for child scopes to drain. Do not exit this scope yet.
		return cmds, false, nil
	}

	// Scope drained (and no active children): close it and resume in parent.
	// Cancel any still-armed event sub-process arms for this scope.
	cmds = appendCancelTimers(cmds, c.s.removeEventTriggeredSubprocessArmsForScope(currentScopeID))
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
	recordSubProcessCompensation(c, parentDef, subNodeID, parentScopeID)

	if found := resumeInParentScope(c, parentDef, subNodeID, parentScopeID); !found {
		return cmds, false, fmt.Errorf("workflow-engine: sub-process exit: node %q has no outgoing flows in parent definition", subNodeID)
	}
	return cmds, true, nil
}

// recordSubProcessCompensation records enclosingNodeID's OWN CompensateAction —
// the one declared on the sub-process node itself, not on any activity inside it
// — as a compensation snapshot in the scope that ENCLOSES that node. It is the
// engine's only site for that record, and it is shared by BOTH exits that can
// close a sub-process's scope: the regular drain exit
// (exitRegularSubprocessScope) and exitNestedEventSubprocessScope, which prunes
// the enclosing scope whenever a nested event sub-process is the last thing
// running inside it — because it outlived a normal drain or because an interrupt
// emptied the scope around it (ADR-0162). Living at only the first of the two is
// what made a sub-process closed by the event-sub-process path silently
// non-compensable.
//
// Call it AFTER the sub-process's own scope has been closed, mirroring the
// regular exit: the variable snapshot is then the state the sub-process finished
// with, and nothing that follows can reopen the scope.
//
// No-op when parentDef has no such node, the node is not a SubProcess, or it
// declares no CompensateAction.
func recordSubProcessCompensation(c *stepCtx, parentDef *model.ProcessDefinition, enclosingNodeID, parentScopeID string) {
	node, ok := parentDef.Node(enclosingNodeID)
	if !ok {
		return
	}
	sp, isSubProcess := node.(activity.SubProcess)
	if !isSubProcess || sp.CompensateAction == "" {
		return
	}
	c.s.recordCompensation(parentScopeID, enclosingNodeID, sp.CompensateAction, c.at, copyVars(c.s.Variables))
}

// resumeInParentScope looks up enclosingNodeID's outgoing flows in parentDef
// and, if one exists, places a token on its target within parentScopeID (root
// scope when parentScopeID == ""; placeTokenInScope with an empty scope ID is
// equivalent to placeToken). It reports whether an outgoing flow was found —
// callers decide what an absent flow means for their scope kind: a regular
// sub-process treats it as an error, while a root-level event sub-process
// treats it as a legitimate instance-completion signal.
func resumeInParentScope(c *stepCtx, parentDef *model.ProcessDefinition, enclosingNodeID, parentScopeID string) bool {
	outs := parentDef.Outgoing(enclosingNodeID)
	if len(outs) == 0 {
		return false
	}
	c.s.placeTokenInScope(outs[0].Target, parentScopeID, c.at)
	return true
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
	// Close every open visit and drop all tokens (including this end-event token).
	// This runs BEFORE endInstance so its removeOrphanedIncidents sweep sees the
	// empty token set and retires the incidents those tokens carried (ADR-0164).
	for i := range c.s.Tokens {
		tok := &c.s.Tokens[i]
		c.s.closeVisitAs(tok.ID, tok.NodeID, c.at, CloseKindTerminated)
	}
	c.s.Tokens = nil

	if ev.Outcome == event.OutcomeAbort {
		reason := ev.TerminationReason
		if reason == "" {
			reason = "force-terminated"
		}
		return c.s.endInstance(StatusTerminated, c.at, FailInstance{Err: reason}), true, nil
	}
	return c.s.endInstance(StatusCompleted, c.at, CompleteInstance{Result: copyVars(c.s.Variables)}), true, nil
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
		tok.State = TokenWaiting
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
	espCmdsScope, espErrScope := armEventTriggeredSubprocesses(sp.Subprocess, c.s, scopeID, c.at, c.pol.eval)
	if espErrScope != nil {
		return cmds, false, espErrScope
	}
	cmds = append(cmds, espCmdsScope...)
	// outer token consumed, inner token active: stopped=true.
	tok.State = TokenWaiting
	return cmds, false, nil
}

// armWaitReminder arms a node's in-wait reminder if one is configured, appending
// the ScheduleTimer command and its timer record to cmds and returning the result.
// Recurrence is native to the scheduler: it re-fires on the interval on its own,
// so the engine arms once here and handleReminderFired only runs the reminder
// action per fire. cancelKey is the token whose resume/interrupt cancels the
// reminder — the human-task id for a UserTask, the parked token id for a
// ReceiveTask or IntermediateCatchEvent.
func armWaitReminder(c *stepCtx, tok *Token, node model.Node, cancelKey string, cmds []Command) ([]Command, error) {
	rawSpec, _ := model.WaitActionOf(node)
	reminderSpec, err := ResolveTrigger(c.pol.eval, rawSpec, c.s.Variables)
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
		TimerID: reminderTimerID,
		Kind:    TimerInWait,
		Token:   tok.ID,
		TaskID:  cancelKey,
		NodeID:  node.ID(),
		ScopeID: tok.ScopeID,
	})
	return cmds, nil
}

// userTaskStrategy handles KindUserTask node entry.
type userTaskStrategy struct{}

func (userTaskStrategy) enter(c *stepCtx, tok *Token, node model.Node) ([]Command, bool, error) {
	ut := node.(activity.UserTask)
	var cmds []Command
	taskID := c.s.nextTaskID()
	spec := authz.AuthzSpec{
		Roles:      ut.EligibleRoles,
		Privileges: ut.EligiblePrivileges,
		Attribute:  ut.EligibleExpr,
	}
	ht := humantask.HumanTask{
		TaskID:      taskID,
		InstanceID:  c.s.InstanceID,
		NodeID:      node.ID(),
		Eligibility: spec,
		State:       humantask.Unclaimed,
		CreatedAt:   c.at,
		// Snapshot the variables the task's attribute-based eligibility predicate
		// is evaluated against (e.g. vars["region"] == "EU"). The engine owns task
		// creation and already holds the variables, so it fills this rather than
		// leaving it to the runtime: every UpdateTask command round-trips this
		// task through the task store, so a task minted without Vars erases them
		// from the store on the first update and breaks authorization thereafter.
		// copyVars is a shallow copy — nested maps stay shared, matching the rule
		// documented on the field.
		Vars: copyVars(c.s.Variables),
	}
	// Link the open visit to the task so the rendered history can resolve the
	// task's claim/completion audit (ADR-0145). Done for the immediate-manual
	// case too: that visit closes in the same step but still names its task.
	c.s.setVisitTask(tok.ID, node.ID(), taskID)
	if ut.Manual && ut.ManualImmediate {
		// Immediate manual task: no actor acts on it, so it never parks. Record the
		// task as Completed for audit and advance the token along its single outgoing
		// flow immediately. No eligibility check, no payload, no deadline/reminder/
		// boundary arming — none are meaningful without a wait period.
		//
		// The record carries NEITHER a Claim NOR a Completion: no actor claimed it and
		// none completed it. This deliberately does NOT mirror handleHumanCompleted,
		// which records a Completion from the completing actor's trigger. It is why
		// humantask.Validate leaves the completion axis unconstrained (ADR-0183), and
		// why the deferred completion rule must carve this path out.
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
	deadlineSpec, err := ResolveTrigger(c.pol.eval, ut.DeadlineTimer, c.s.Variables)
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
			TimerID: deadlineTimerID,
			Kind:    TimerDeadline,
			Token:   tok.ID,
			TaskID:  taskID,
			NodeID:  node.ID(),
			ScopeID: tok.ScopeID,
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
	// is cancelled by the human-task id (cancelKey = taskID), preserving the
	// original behaviour.
	cmds, err = armWaitReminder(c, tok, node, taskID, cmds)
	if err != nil {
		return cmds, false, err
	}
	c.s.Tasks = append(c.s.Tasks, ht)
	cmds = append(cmds, AwaitHuman{TaskID: taskID, Eligibility: spec})
	tok.State = TokenWaiting
	tok.AwaitCommand = taskID
	// Arm any boundary events attached to this host activity.
	bndCmds, err := armBoundaries(c.tdef, c.s, tok.ID, node.ID(), c.at, c.pol.eval)
	if err != nil {
		return cmds, false, err
	}
	cmds = append(cmds, bndCmds...)
	// token parked: stopped=true (tok.State == TokenWaiting != TokenActive).
	return cmds, false, nil
}

// intermediateCatchEventStrategy handles KindIntermediateCatchEvent node entry.
type intermediateCatchEventStrategy struct{}

func (intermediateCatchEventStrategy) enter(c *stepCtx, tok *Token, node model.Node) ([]Command, bool, error) {
	ice := node.(event.IntermediateCatchEvent)
	var cmds []Command
	timerSpec, err := ResolveTrigger(c.pol.eval, ice.Timer, c.s.Variables)
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
		tok.State = TokenWaiting
		// Dual-write (ADR-0177): AwaitCommand stays the dispatch key that
		// handleTimerFired's fall-through routes on; AwaitTimer is the
		// enumeration marker TimerTokenWaiters reads, because AwaitCommand alone
		// cannot say whether the id names a timer, a task or an action command.
		tok.AwaitCommand = timerID
		tok.AwaitTimer = timerID
	} else if ice.SignalName != "" {
		// Signal intermediate catch event: park the token awaiting the signal.
		// The SignalReceived trigger (broadcast) will resume it later.
		tok.State = TokenWaiting
		tok.AwaitSignal = ice.SignalName
	} else if ice.MessageName != "" {
		// Message intermediate catch event: park the token awaiting the message.
		// Evaluate the correlation key (if set) now against instance variables
		// for determinism; store the resolved key on the token.
		resolvedKey, err := c.pol.eval.EvalString(ice.CorrelationKey, c.s.Variables)
		if err != nil {
			return cmds, false, fmt.Errorf("workflow-engine: message node %q correlation key: %w", node.ID(), err)
		}
		tok.State = TokenWaiting
		tok.AwaitMessage = ice.MessageName
		tok.AwaitMessageKey = resolvedKey
	} else {
		// Non-timer, non-signal, non-message intermediate catch event: park.
		// Further event variants arrive in later plans.
		tok.State = TokenWaiting
	}
	// Arm the node's in-wait reminder, if configured. It is cancelled by the
	// parked token (cancelKey = tok.ID) when the awaited signal/message/timer
	// resolves it. For the timer variant the reminder is a DIFFERENT TimerInWait
	// than the intermediate timer the token awaits via AwaitCommand.
	cmds, err = armWaitReminder(c, tok, node, tok.ID, cmds)
	if err != nil {
		return cmds, false, err
	}
	// token parked: stopped=true (tok.State == TokenWaiting != TokenActive).
	return cmds, false, nil
}

// exclusiveGatewayStrategy handles KindExclusiveGateway node entry.
type exclusiveGatewayStrategy struct{}

func (exclusiveGatewayStrategy) enter(c *stepCtx, tok *Token, node model.Node) ([]Command, bool, error) {
	target, err := selectExclusiveTarget(c.tdef, c.s, node, c.pol.eval)
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
		// tryParallelJoin always sets tok.State = TokenJoining first, then
		// conditionally removes all join-side tokens if the join fires.
		// Stopped semantics must match the original switch arm:
		//   - Join pending: token still in slice with State==TokenJoining → stopped=true.
		//   - Join fired: token removed from slice → stopped=false (auto-advance).
		// Re-read the token from the slice to distinguish the two cases:
		if t := c.s.tokenByID(tok.ID); t != nil && t.State == TokenJoining {
			// Pending: tok.State is already TokenJoining → drive() sees stopped=true.
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
		// tryInclusiveJoin always sets tok.State = TokenJoining first, then
		// conditionally removes all join-side tokens if the join fires.
		// Stopped semantics must match the original switch arm:
		//   - Join pending: token still in slice with State==TokenJoining → stopped=true.
		//   - Join fired: token removed from slice → stopped=false (auto-advance).
		// Re-read the token from the slice to distinguish the two cases:
		if t := c.s.tokenByID(tok.ID); t != nil && t.State == TokenJoining {
			// Pending: signal stop to drive() by leaving tok.State == TokenJoining.
			// (tok and t are the same pointer; already set by tryInclusiveJoin.)
		} else {
			// Fired: all join tokens consumed; reset tok.State to TokenActive so
			// drive() derives stopped=false and keeps advancing.
			tok.State = TokenActive
		}
	} else {
		if err := c.s.forkInclusive(c.tdef, tok, node, tok.ScopeID, c.at, c.pol.eval); err != nil {
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
	tok.State = TokenWaiting
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
		gwTimerSpec, err := ResolveTrigger(c.pol.eval, ce.Timer, c.s.Variables)
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
			resolvedKey, err := c.pol.eval.EvalString(ce.CorrelationKey, c.s.Variables)
			if err != nil {
				return cmds, false, fmt.Errorf("workflow-engine: event-gateway %q message arm %q correlation key: %w", node.ID(), catchNodeRaw.ID(), err)
			}
			ae.Message = ce.MessageName
			ae.MessageKey = resolvedKey
		}
		c.s.ArmedEvents = append(c.s.ArmedEvents, ae)
	}
	// gateway token parked: tok.State == TokenWaiting → stopped=true.
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
	tok.State = TokenWaiting
	tok.AwaitCommand = cmdID
	// token parked: tok.State == TokenWaiting → stopped=true.
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
		tok.State = TokenWaiting
		// token parked: tok.State == TokenWaiting → stopped=true.
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

// deferCompensationThrow parks tok behind the compensation walk already in
// flight (ADR-0071 serialization, one walk at a time). The token is re-driven
// through the throw strategy when that walk finishes (popOneDeferredThrow).
func deferCompensationThrow(s *InstanceState, tok *Token) {
	tok.State = TokenWaiting
	s.DeferredCompensationThrows = append(s.DeferredCompensationThrows, tok.ID)
}

// startCompensationWalk commits tok to a compensation walk and returns cmds with
// the walk's first InvokeAction appended: it consumes the token, flips the
// instance to StatusCompensating, mints the command ID identifying the in-flight
// action, completes cursor with the fields both throw branches share, installs
// it, and parks the token.
//
// cursor carries only the caller's distinguishing fields — ArchiveKey for a
// targeted throw, ScopeID/StartRecordCount for a scope-wide one. resumeScope
// must be the throw token's scope read BEFORE the call, since consumeToken
// invalidates tok's slot in s.Tokens.
//
// It also PINS records onto the cursor (ADR-0171). A throw resumes past itself,
// so sibling branches keep running while the walk is in flight; one of them
// reaching its scope's end event archives and closes that scope, nilling the
// live slice the cursor's ScopeID names. Snapshotting here makes the walk's
// indices independent of every later mutation of that source.
//
// The mutation order is load-bearing: consumeToken must run before
// nextCommandID, as both advance state on c.s.
func startCompensationWalk(c *stepCtx, cmds []Command, tok *Token, records []CompensationRecord, cursor compensationCursor, resumeNode, resumeScope string) []Command {
	c.s.consumeToken(tok, c.at)
	c.s.Status = StatusCompensating
	cmdID := c.s.nextCommandID()
	cursor.StartedAt = c.at
	cursor.ResumeNode = resumeNode
	cursor.ResumeScope = resumeScope
	cursor.Records = cloneCompensationRecords(records)
	cursor.NextIndex = len(records) - 1
	cursor.ActiveCmdID = cmdID
	c.s.Compensating = cursor
	// The first record is dispatched HERE rather than through
	// stepCompensationAdvance, so its ownership hand-off happens here too
	// (ADR-0173). Without it a single-record targeted walk would leave its one
	// record in the archive for a later walk to re-run.
	c.s.consumeDispatchedRecord(len(records) - 1)
	rec := records[len(records)-1]
	c.s.recordCompensationDispatch(cmdID)
	cmds = append(cmds, compensationInvoke(rec, cmdID))
	// This is the throw walk's FIRST dispatch, and one of the five compensation
	// dispatch sites that must arm the stall timer (ADR-0175): beginCompensation,
	// stepCompensationAdvance, retryStalledCompensation, retryFailedCompensation
	// (ADR-0179's fifth site) and this one. Leaving it
	// unarmed would give a single-record throw walk no stall detection at all —
	// and this is the site the measured deferred-cancel deadlock arises at.
	cmds = append(cmds, armCompensationStallTimer(c.s, c.pol, rec.NodeID)...)
	tok.State = TokenWaiting
	return cmds
}

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
			deferCompensationThrow(c.s, tok)
		} else {
			// Start the throw compensation walk (resumeNode is non-empty here).
			cmds = startCompensationWalk(c, cmds, tok, records,
				compensationCursor{ArchiveKey: ref}, resumeNode, tokScope)
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
			deferCompensationThrow(c.s, tok)
		default:
			// Committing to a walk. Whole-instance default (BPMN conformant): merge
			// archived sub-process records into RootCompensations FIRST so they are
			// compensated too, THEN read the throwing scope's records. ScopeLocal
			// skips the merge — root-direct records only.
			if tokScope == "" && !cte.ScopeLocal {
				c.s.consolidateArchiveIntoRoot()
			}
			// The second result is ignored HERE, and that is a measured decision,
			// not an oversight: tokScope cannot name a closed scope at this point.
			// drive() resolves defForScope(def, s, tok.ScopeID) before dispatching
			// to any strategy, and that returns
			// `workflow-engine: defForScope: unknown scope %q` for a scope absent
			// from s.Scopes — so a token in a closed scope fails the Step outright
			// and never reaches this line. Verified by execution: driving a token
			// with ScopeID "gone" produces that error, not an auto-advance.
			// A closed scope therefore cannot masquerade as an empty one here.
			records, _ := compensationRecordsForScope(c.s, tokScope)
			if len(records) == 0 {
				// Nothing to compensate even after consolidation — auto-advance
				// (harmless: nothing was there to merge).
				c.s.moveAlongSingleFlow(c.tdef, tok, c.at)
			} else {
				// Start the scope-wide walk over the records just read from the
				// throwing scope. startCompensationWalk PINS them onto the cursor
				// (ADR-0171), so cursorRecords iterates that snapshot and not the
				// live list a sibling branch may nil mid-walk. The cursor carries
				// no ArchiveKey, which is what makes walkMode classify the finish
				// as walkThrowScopeWide: it clears the drained prefix of the
				// scope's LIVE list (compensate-once) while the snapshot the walk
				// iterated stays intact on the cursor until the cursor is cleared.
				cmds = startCompensationWalk(c, cmds, tok, records,
					compensationCursor{ScopeID: tokScope, StartRecordCount: len(records)},
					resumeNode, tokScope)
			}
		}
	}
	return cmds, false, nil
}

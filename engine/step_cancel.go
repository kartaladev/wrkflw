package engine

import (
	"strings"
	"time"

	"github.com/kartaladev/wrkflw/humantask"
)

// cancelTokenWaits cancels every wait attached to tok — deadline/reminder timers, the
// token-keyed in-wait reminder, boundary arms on the token's node, (for an event-based
// gateway token, AwaitCommand prefixed "evtgw:") its armed events, and the open human
// task the token is parked on — retires the incidents raised against the token, and
// consumes the token. Returns the CancelTimer commands produced by the sweep plus, when
// a human task was closed, the UpdateTask that reconciles the task store.
func cancelTokenWaits(s *InstanceState, tok *Token, at time.Time, closeKind CloseKind) []Command {
	var cmds []Command
	// Cancel deadline/reminder timers for this token (UserTask case).
	cmds = appendCancelTimers(cmds, s.cancelTimersByTaskID(tok.AwaitCommand, ""))
	// Cancel any token-keyed in-wait reminder (ReceiveTask / catch): its parked
	// token is being consumed, so the recurring reminder must go.
	cmds = appendCancelTimers(cmds, s.cancelTimersForToken(tok.ID, ""))
	// Cancel boundary arms for this host token.
	cmds = appendCancelTimers(cmds, s.removeBoundaryArmsForHost(tok.ID))
	// Cancel any event-gateway arms.
	if strings.HasPrefix(tok.AwaitCommand, "evtgw:") {
		cmds = appendCancelTimers(cmds, s.removeArmedEventsForGateway(tok.ID))
	}
	// An open human task is a wait attached to this token.
	// AwaitCommand is the taskID for a UserTask (set by userTaskStrategy.enter
	// in step_nodes.go) and a command ID otherwise, where TaskByID returns nil —
	// the same assumption cancelTimersByTaskID already makes above, so this is
	// a natural no-op for non-task tokens.
	//
	// Clone before the record escapes: the command is handed to a
	// consumer-supplied TaskStore while the record it was built from is
	// committed as instance state.
	if task := s.TaskByID(tok.AwaitCommand); task != nil && task.IsOpen() {
		task.State = humantask.Cancelled
		cmds = append(cmds, UpdateTask{Task: task.Clone()})
	}
	// An incident names the token that failed (Incident.TokenID). Cancelling
	// that token must retire it, or it stays visible on a completed or
	// terminated instance with nothing left to resolve.
	//
	// Scoped to the paths that cancel a TOKEN — this function's call sites — and
	// deliberately NOT "on every path". Four terminal transitions end
	// an instance without coming through here: forceTerminate (step_nodes.go) and
	// handleCancelRequested's immediate-termination branch (step_triggers.go) drop
	// every token wholesale, while handleUnhandledError's immediate-failure branch
	// (step_errors.go) and handleSubInstanceFailed's tail (step_triggers.go) end
	// the instance with its tokens still in place. All four now route through
	// endInstance, whose removeOrphanedIncidents retires the incidents whose token
	// is gone — token linkage, not a
	// wholesale clear, so the two sites that keep their tokens keep their incidents.
	s.removeIncidentsForToken(tok.ID)

	tokPtr := s.tokenByID(tok.ID)
	if tokPtr != nil {
		s.consumeTokenAs(tokPtr, at, closeKind)
	}
	return cmds
}

// cancelScopeSubtree cancels every token in scopeID and in all its descendant
// scopes, retires their event-sub-process arms, archives their compensation
// records, and returns the commands produced by the sweep: CancelTimer for
// retired arms and timers, and UpdateTask for human tasks retired with their
// token.
//
// It does NOT close the scopes. The caller decides: the interrupting
// event-sub-process path keeps the enclosing scope open so the drain code can
// detect its children (and calls closeScopeDescendants), while the error-boundary
// path calls closeScope on the whole subtree.
//
// scopeID may be "" — the implicit root scope — in which case the doomed set is
// the entire instance. That is the correct reading for a root-level interrupting
// event sub-process: BPMN interrupting event sub-processes at process level
// terminate all other activity in the process.
func cancelScopeSubtree(s *InstanceState, scopeID string, at time.Time, kind CloseKind) []Command {
	ids := s.descendantScopeIDs(scopeID)

	// Snapshot before cancelling: cancelTokenWaits mutates s.Tokens.
	tokensToCancel := make([]Token, 0, len(s.Tokens))
	for _, tok := range s.Tokens {
		if ids[tok.ScopeID] {
			tokensToCancel = append(tokensToCancel, tok)
		}
	}
	var cmds []Command
	for _, tok := range tokensToCancel {
		cmds = append(cmds, cancelTokenWaits(s, &tok, at, kind)...)
	}

	// scopeID itself first: it may be the implicit root (""), which has no entry
	// in s.Scopes and so would be missed by the slice walk below. Both call
	// sites retire the named scope's arms today, so this preserves that exactly.
	// archiveCompensations("") is a no-op by construction — root records live in
	// s.RootCompensations, which is never pruned.
	cmds = appendCancelTimers(cmds, s.removeEventTriggeredSubprocessArmsForScope(scopeID))
	s.archiveCompensations(scopeID)

	// Then every descendant, in s.Scopes SLICE order — parent before child,
	// because openScope appends children after parents. Never map order: the
	// emitted command sequence and the ArchivedCompensations append order must
	// be deterministic.
	for i := range s.Scopes {
		id := s.Scopes[i].ID
		if id == scopeID || !ids[id] {
			continue
		}
		cmds = appendCancelTimers(cmds, s.removeEventTriggeredSubprocessArmsForScope(id))
		s.archiveCompensations(id)
	}
	return cmds
}

// emitFireOnceAction returns the fire-and-forget InvokeAction for a boundary/handler action
// named actionName, with a defensive copy of the instance variables as input. Empty name -> nil.
func emitFireOnceAction(s *InstanceState, actionName string) []Command {
	if actionName == "" {
		return nil
	}
	return []Command{InvokeAction{
		CommandID:     s.nextCommandID(),
		Name:          actionName,
		Input:         copyVars(s.Variables),
		FireAndForget: true,
	}}
}

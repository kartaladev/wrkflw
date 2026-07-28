package engine

import (
	"strings"
	"time"
)

// cancelTokenWaits cancels every wait attached to tok — deadline/reminder timers, the
// token-keyed in-wait reminder, boundary arms on the token's node, and (for an event-based
// gateway token, AwaitCommand prefixed "evtgw:") its armed events — and consumes the token.
// Returns the CancelTimer commands produced by the sweep.
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
	tokPtr := s.tokenByID(tok.ID)
	if tokPtr != nil {
		s.consumeTokenAs(tokPtr, at, closeKind)
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

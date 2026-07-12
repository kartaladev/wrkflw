package engine

import (
	"strings"
	"time"
)

// cancelTokenWaits cancels every wait attached to tok — deadline/reminder timers, the
// token-keyed in-wait reminder, boundary arms on the token's node, and (for an event-based
// gateway token, AwaitCommand prefixed "evtgw:") its armed events — and consumes the token.
// Returns the CancelTimer commands produced by the sweep.
func cancelTokenWaits(s *InstanceState, tok *Token, at time.Time) []Command {
	var cmds []Command
	// Cancel deadline/reminder timers for this token (UserTask case).
	for _, timerID := range s.cancelTimersByTaskToken(tok.AwaitCommand, "") {
		cmds = append(cmds, CancelTimer{TimerID: timerID})
	}
	// Cancel any token-keyed in-wait reminder (ReceiveTask / catch): its parked
	// token is being consumed, so the recurring reminder must go.
	for _, timerID := range s.cancelTimersForToken(tok.ID, "") {
		cmds = append(cmds, CancelTimer{TimerID: timerID})
	}
	// Cancel boundary arms for this host token.
	for _, timerID := range s.removeBoundaryArmsForHost(tok.ID) {
		cmds = append(cmds, CancelTimer{TimerID: timerID})
	}
	// Cancel any event-gateway arms.
	if strings.HasPrefix(tok.AwaitCommand, "evtgw:") {
		for _, timerID := range s.removeArmedEventsForGateway(tok.ID) {
			cmds = append(cmds, CancelTimer{TimerID: timerID})
		}
	}
	tokPtr := s.tokenByID(tok.ID)
	if tokPtr != nil {
		s.consumeToken(tokPtr, at)
	}
	return cmds
}

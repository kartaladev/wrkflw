package engine

import (
	"context"
	"time"

	"github.com/kartaladev/wrkflw/definition/model"
)

// dispatchArmCascade runs the shared gateway → boundary → event-sub
// first-match-wins precedence cascade used by handleTimerFired and
// handleMessageReceived: it calls gw, then boundary, then eventSub (each
// already bound to the trigger's own correlation — timer ID, or message
// name+correlation key) and, on the first match, invokes onMatch (nil is a
// no-op; message dispatch uses it to merge the trigger payload into
// instance variables before firing, matching the pre-extraction order) and
// fires the matched arm via the same resolveGatewayWin/fireBoundaryArm/
// fireEventTriggeredSubprocessArm calls the inline cascades used. matched is
// false when none of the three lookups found anything, letting the caller
// fall through to its own trailing dispatch (deadline/in-wait/retry timer
// records and the standalone parked token for timer; the standalone
// parked-message token for message).
//
// handleSignalReceived does NOT use this helper: signal delivery is
// broadcast (it must check all three arm kinds, not stop at the first, and
// additionally resume every parked token awaiting the signal), which is a
// fundamentally different shape from the first-match-wins cascade here.
func dispatchArmCascade(
	ctx context.Context,
	def *model.ProcessDefinition, s *InstanceState, at time.Time, pol stepPolicy,
	onMatch func(),
	gw func() *armedEvent, boundary func() *boundaryArm, eventSub func() *eventTriggeredSubprocessArm,
) (cmds []Command, matched bool, err error) {
	// A DYING instance spawns no new work. The guard sits AHEAD of every
	// lookup, not inside the fire functions, for two reasons:
	//
	//   - it covers all three arm families, not just event sub-processes; and
	//   - onMatch runs BEFORE the fire, merging the trigger payload and marking
	//     the delivery matched. A fire that silently no-ops behind onMatch would
	//     therefore CONSUME the message and short-circuit the caller's
	//     fall-through — the same silent-swallow this guard exists to remove,
	//     reintroduced on the message and timer paths.
	//
	// Returning matched=false lets the caller fall through to its own token
	// handling, which applies the SAME check separately — see the
	// spawnsNewWork guards at handleTimerFired's standalone-timer resume and
	// handleMessageReceived's standalone-message resume.
	//
	// ⚠ That is TWO guards, not one, and the duplication is deliberate. An
	// earlier revision of this bundle asserted here that the callers "apply the
	// same rule for itself" while neither actually did, and `/code-review`
	// measured a live InvokeAction dispatched to a worker on both paths. If a
	// fourth trigger kind is added, it needs its own token-path guard too.
	if !s.spawnsNewWork() {
		return nil, false, nil
	}
	if ae := gw(); ae != nil {
		if onMatch != nil {
			onMatch()
		}
		cmds, err = resolveGatewayWin(ctx, def, s, *ae, at, pol)
		return cmds, true, err
	}
	if ba := boundary(); ba != nil {
		if onMatch != nil {
			onMatch()
		}
		cmds, err = fireBoundaryArm(ctx, def, s, *ba, at, pol)
		return cmds, true, err
	}
	if ea := eventSub(); ea != nil {
		if onMatch != nil {
			onMatch()
		}
		cmds, err = fireEventTriggeredSubprocessArm(ctx, def, s, *ea, at, pol)
		return cmds, true, err
	}
	return nil, false, nil
}

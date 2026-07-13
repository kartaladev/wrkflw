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
	def *model.ProcessDefinition, s *InstanceState, at time.Time, mode StepMode, eval ConditionEvaluator,
	onMatch func(),
	gw func() *armedEvent, boundary func() *boundaryArm, eventSub func() *eventTriggeredSubprocessArm,
) (cmds []Command, matched bool, err error) {
	if ae := gw(); ae != nil {
		if onMatch != nil {
			onMatch()
		}
		cmds, err = resolveGatewayWin(ctx, def, s, *ae, at, mode, eval)
		return cmds, true, err
	}
	if ba := boundary(); ba != nil {
		if onMatch != nil {
			onMatch()
		}
		cmds, err = fireBoundaryArm(ctx, def, s, *ba, at, mode, eval)
		return cmds, true, err
	}
	if ea := eventSub(); ea != nil {
		if onMatch != nil {
			onMatch()
		}
		cmds, err = fireEventTriggeredSubprocessArm(ctx, def, s, *ea, at, mode, eval)
		return cmds, true, err
	}
	return nil, false, nil
}

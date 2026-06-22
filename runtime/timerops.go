package runtime

import "github.com/zakyalvan/krtlwrkflw/engine"

// timerOpsFor derives the armed-timer side-effects of one applied step from its
// commands and trigger. ScheduleTimer commands become arms; CancelTimer commands
// and a TimerFired trigger (the fired timer is consumed) become cancels. Pure;
// kind-agnostic so it covers every timer kind uniformly.
func timerOpsFor(cmds []engine.Command, trg engine.Trigger, defID string, defVersion int, instanceID string) ([]ArmedTimer, []string) {
	var arms []ArmedTimer
	var cancels []string
	for _, c := range cmds {
		switch cmd := c.(type) {
		case engine.ScheduleTimer:
			arms = append(arms, ArmedTimer{
				InstanceID: instanceID,
				DefID:      defID,
				DefVersion: defVersion,
				TimerID:    cmd.TimerID,
				FireAt:     cmd.FireAt,
				Kind:       cmd.Kind,
			})
		case engine.CancelTimer:
			cancels = append(cancels, cmd.TimerID)
		}
	}
	if tf, ok := trg.(engine.TimerFired); ok {
		cancels = append(cancels, tf.TimerID)
	}
	return arms, cancels
}

package runtime

import "github.com/zakyalvan/krtlwrkflw/engine"

// outboxEventsFor derives the domain events to relay from the commands a Step
// produced. Only terminal commands produce events: CompleteInstance →
// "instance.completed", FailInstance → "instance.failed". Every other command
// contributes nothing (it is performed as external I/O, not relayed). This is
// the logic that previously lived inline in perform; an exhaustiveness test
// guards the mapping (ADR-0007).
func outboxEventsFor(cmds []engine.Command) []OutboxEvent {
	var events []OutboxEvent
	for _, c := range cmds {
		switch cmd := c.(type) {
		case engine.CompleteInstance:
			events = append(events, OutboxEvent{Topic: "instance.completed", Payload: cmd.Result})
		case engine.FailInstance:
			events = append(events, OutboxEvent{Topic: "instance.failed", Payload: map[string]any{"error": cmd.Err}})
		}
	}
	return events
}

package runtime

import (
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// instanceDefRef builds the id:version Qualifier of an instance, carried on
// terminal outbox events so a consumer (chaining's PredecessorDefinitionRef) can
// route on the source definition (ADR-0047).
func instanceDefRef(st engine.InstanceState) model.Qualifier {
	return model.Version(st.DefID, st.DefVersion)
}

// terminalOutboxEvent derives the single domain event to relay when a step
// transitions an instance into a terminal status. The TOPIC is computed
// status-driven at the deliverLoop terminal edge (the same place CallOutcome is
// derived), not from the terminal command, so every terminal outcome maps to an
// accurate topic (ADR-0046):
//
//	StatusCompleted  -> "instance.completed"  payload = st.Variables (copied)
//	StatusFailed     -> "instance.failed"     payload = {"error": terminalEventErr}
//	StatusTerminated -> "instance.terminated" payload = {"error": terminalEventErr}
//
// It returns nil when (prevStatus, st.Status) is not a terminal edge — i.e. the
// instance was already terminal, or has not reached a terminal status. Routing
// the topic off status (not the command) fixes two gaps: a cancelled instance
// (StatusTerminated) used to publish "instance.failed", and an admin
// full-rollback termination (also StatusTerminated, no terminal command) used to
// publish nothing.
func terminalOutboxEvent(prevStatus engine.Status, st engine.InstanceState, cmds []engine.Command) []kernel.OutboxEvent {
	if !isTerminal(st.Status) || isTerminal(prevStatus) {
		return nil
	}
	def := instanceDefRef(st)
	switch st.Status {
	case engine.StatusCompleted:
		return []kernel.OutboxEvent{{Topic: "instance.completed", Payload: copyVarsForOutcome(st.Variables), InstanceID: st.InstanceID, DefinitionRef: def}}
	case engine.StatusFailed:
		return []kernel.OutboxEvent{{Topic: "instance.failed", Payload: map[string]any{"error": terminalEventErr(st, cmds)}, InstanceID: st.InstanceID, DefinitionRef: def}}
	case engine.StatusTerminated:
		return []kernel.OutboxEvent{{Topic: "instance.terminated", Payload: map[string]any{"error": terminalEventErr(st, cmds)}, InstanceID: st.InstanceID, DefinitionRef: def}}
	default:
		return nil
	}
}

// outboundMessageEvents turns each engine.SendMessage command into a message.<Name>
// outbox event so a SendTask message is written atomically in the state-commit tx and
// relayed at-least-once, exactly like a domain event (ADR-0067). The payload carries the
// message name, the resolved correlation key, and a copy of the sender's variables.
func outboundMessageEvents(st engine.InstanceState, cmds []engine.Command) []kernel.OutboxEvent {
	var out []kernel.OutboxEvent
	for _, c := range cmds {
		m, ok := c.(engine.SendMessage)
		if !ok {
			continue
		}
		out = append(out, kernel.OutboxEvent{
			Topic:         "message." + m.Name,
			Payload:       map[string]any{"messageName": m.Name, "correlationKey": m.CorrelationKey, "variables": m.Payload},
			InstanceID:    st.InstanceID,
			DefinitionRef: instanceDefRef(st),
		})
	}
	return out
}

// terminalEventErr resolves the error string for a terminal outbox event. Only
// the topic is status-driven (ADR-0046); the error string stays best-effort so
// existing diagnostics survive. It prefers the most concrete description
// available, in order:
//
//  1. the first recorded incident's error (the normal unhandled-error path),
//  2. the terminal FailInstance command's Err — the SubInstanceFailed path
//     records no incident yet carries a rich message ("child parked…",
//     "recursion depth limit…") there, and the cancel path carries "cancelled",
//  3. a status-keyed generic fallback.
func terminalEventErr(st engine.InstanceState, cmds []engine.Command) string {
	if len(st.Incidents) > 0 {
		return st.Incidents[0].Error
	}
	for _, c := range cmds {
		if f, ok := c.(engine.FailInstance); ok && f.Err != "" {
			return f.Err
		}
	}
	if st.Status == engine.StatusTerminated {
		return "instance terminated"
	}
	return "instance failed"
}

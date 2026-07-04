package model

import "github.com/zakyalvan/krtlwrkflw/action"

// The kind-agnostic accessors below dispatch on the unexported carrier methods
// of the shared embed types (ActivityFields, WaitFields, TaskAction) rather than
// enumerating concrete kinds. Because those carrier methods are defined in this
// package, the assertions keep working after the concrete node types move into
// the event/activity/gateway leaf packages (which embed these types) — without
// this package importing the leaves.

// RetryPolicyOf returns the RetryPolicy of a Node if it is an activity kind that
// carries one, or nil otherwise. Non-activity nodes (events, gateways) always
// return nil.
func RetryPolicyOf(n Node) *RetryPolicy {
	if a, ok := n.(interface{ retry() *RetryPolicy }); ok {
		return a.retry()
	}
	return nil
}

// DeadlineOf returns the DeadlineDuration, DeadlineFlow, and DeadlineAction of a
// Node that carries deadline fields (activities and IntermediateCatchEvent).
// Returns ("", "", "") for nodes that do not carry deadline fields.
func DeadlineOf(n Node) (duration, flow, action string) {
	if w, ok := n.(interface {
		deadline() (string, string, string)
	}); ok {
		return w.deadline()
	}
	return "", "", ""
}

// ReminderOf returns the ReminderEvery and ReminderAction of a Node that carries
// reminder fields (activities and IntermediateCatchEvent). Returns ("", "") for
// nodes that do not carry reminder fields.
func ReminderOf(n Node) (every, action string) {
	if w, ok := n.(interface{ reminder() (string, string) }); ok {
		return w.reminder()
	}
	return "", ""
}

// ActionOf returns the Action field of a node that has one (ServiceTask or
// BusinessRuleTask), or "" for all other kinds.
func ActionOf(n Node) string {
	if t, ok := n.(interface {
		taskAction() (string, action.ServiceAction)
	}); ok {
		name, _ := t.taskAction()
		return name
	}
	return ""
}

// InlineActionOf returns the node-local inline ServiceAction of a ServiceTask or
// BusinessRuleTask, or nil when the node has none (or is another kind). Inline
// actions are never serialized; a node decoded from JSONB always returns nil.
func InlineActionOf(n Node) action.ServiceAction {
	if t, ok := n.(interface {
		taskAction() (string, action.ServiceAction)
	}); ok {
		_, inline := t.taskAction()
		return inline
	}
	return nil
}

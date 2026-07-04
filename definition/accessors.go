package definition

import "github.com/zakyalvan/krtlwrkflw/action"

// RetryPolicyOf returns the RetryPolicy of a Node if it is an activity kind that
// carries one, or nil otherwise. Non-activity nodes (events, gateways) always
// return nil.
func RetryPolicyOf(n Node) *RetryPolicy {
	switch v := n.(type) {
	case ServiceTask:
		return v.RetryPolicy
	case UserTask:
		return v.RetryPolicy
	case ReceiveTask:
		return v.RetryPolicy
	case SendTask:
		return v.RetryPolicy
	case BusinessRuleTask:
		return v.RetryPolicy
	case SubProcess:
		return v.RetryPolicy
	case CallActivity:
		return v.RetryPolicy
	default:
		return nil
	}
}

// DeadlineOf returns the DeadlineDuration, DeadlineFlow, and DeadlineAction of a Node that carries
// deadline fields. Returns ("", "", "") for nodes that do not carry deadline fields.
func DeadlineOf(n Node) (duration, flow, action string) {
	switch v := n.(type) {
	case ServiceTask:
		return v.DeadlineDuration, v.DeadlineFlow, v.DeadlineAction
	case UserTask:
		return v.DeadlineDuration, v.DeadlineFlow, v.DeadlineAction
	case ReceiveTask:
		return v.DeadlineDuration, v.DeadlineFlow, v.DeadlineAction
	case SendTask:
		return v.DeadlineDuration, v.DeadlineFlow, v.DeadlineAction
	case BusinessRuleTask:
		return v.DeadlineDuration, v.DeadlineFlow, v.DeadlineAction
	case SubProcess:
		return v.DeadlineDuration, v.DeadlineFlow, v.DeadlineAction
	case CallActivity:
		return v.DeadlineDuration, v.DeadlineFlow, v.DeadlineAction
	case IntermediateCatchEvent:
		return v.DeadlineDuration, v.DeadlineFlow, v.DeadlineAction
	default:
		return "", "", ""
	}
}

// ReminderOf returns the ReminderEvery and ReminderAction of a Node that carries
// reminder fields. Returns ("", "") for nodes that do not carry reminder fields.
func ReminderOf(n Node) (every, action string) {
	switch v := n.(type) {
	case ServiceTask:
		return v.ReminderEvery, v.ReminderAction
	case UserTask:
		return v.ReminderEvery, v.ReminderAction
	case ReceiveTask:
		return v.ReminderEvery, v.ReminderAction
	case SendTask:
		return v.ReminderEvery, v.ReminderAction
	case BusinessRuleTask:
		return v.ReminderEvery, v.ReminderAction
	case SubProcess:
		return v.ReminderEvery, v.ReminderAction
	case CallActivity:
		return v.ReminderEvery, v.ReminderAction
	case IntermediateCatchEvent:
		return v.ReminderEvery, v.ReminderAction
	default:
		return "", ""
	}
}

// ActionOf returns the Action field of a node that has one (ServiceTask or
// BusinessRuleTask), or "" for all other kinds.
func ActionOf(n Node) string {
	switch v := n.(type) {
	case ServiceTask:
		return v.Action
	case BusinessRuleTask:
		return v.Action
	default:
		return ""
	}
}

// InlineActionOf returns the node-local inline ServiceAction of a ServiceTask or
// BusinessRuleTask, or nil when the node has none (or is another kind). Inline
// actions are never serialized; a node decoded from JSONB always returns nil.
func InlineActionOf(n Node) action.ServiceAction {
	switch v := n.(type) {
	case ServiceTask:
		return v.inline
	case BusinessRuleTask:
		return v.inline
	default:
		return nil
	}
}

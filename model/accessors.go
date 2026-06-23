package model

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

// SLAOf returns the SLADuration, SLAFlow, and SLAAction of a Node that carries
// SLA fields. Returns ("", "", "") for nodes that do not carry SLA fields.
func SLAOf(n Node) (duration, flow, action string) {
	switch v := n.(type) {
	case ServiceTask:
		return v.SLADuration, v.SLAFlow, v.SLAAction
	case UserTask:
		return v.SLADuration, v.SLAFlow, v.SLAAction
	case ReceiveTask:
		return v.SLADuration, v.SLAFlow, v.SLAAction
	case SendTask:
		return v.SLADuration, v.SLAFlow, v.SLAAction
	case BusinessRuleTask:
		return v.SLADuration, v.SLAFlow, v.SLAAction
	case SubProcess:
		return v.SLADuration, v.SLAFlow, v.SLAAction
	case CallActivity:
		return v.SLADuration, v.SLAFlow, v.SLAAction
	case IntermediateCatchEvent:
		return v.SLADuration, v.SLAFlow, v.SLAAction
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

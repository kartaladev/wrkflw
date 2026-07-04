package engine

import "github.com/zakyalvan/krtlwrkflw/definition"

// compensationActionOf returns the CompensationAction of an activity node, or "".
func compensationActionOf(n definition.Node) string {
	switch v := n.(type) {
	case definition.ServiceTask:
		return v.CompensationAction
	case definition.UserTask:
		return v.CompensationAction
	case definition.ReceiveTask:
		return v.CompensationAction
	case definition.SendTask:
		return v.CompensationAction
	case definition.BusinessRuleTask:
		return v.CompensationAction
	case definition.SubProcess:
		return v.CompensationAction
	case definition.CallActivity:
		return v.CompensationAction
	default:
		return ""
	}
}

// cancelHandlerOf returns the CancelHandler of an activity node, or "".
func cancelHandlerOf(n definition.Node) string {
	switch v := n.(type) {
	case definition.ServiceTask:
		return v.CancelHandler
	case definition.UserTask:
		return v.CancelHandler
	case definition.ReceiveTask:
		return v.CancelHandler
	case definition.SendTask:
		return v.CancelHandler
	case definition.BusinessRuleTask:
		return v.CancelHandler
	case definition.SubProcess:
		return v.CancelHandler
	case definition.CallActivity:
		return v.CancelHandler
	default:
		return ""
	}
}

// recoveryFlowOf returns the RecoveryFlow of an activity node, or "".
func recoveryFlowOf(n definition.Node) string {
	switch v := n.(type) {
	case definition.ServiceTask:
		return v.RecoveryFlow
	case definition.UserTask:
		return v.RecoveryFlow
	case definition.ReceiveTask:
		return v.RecoveryFlow
	case definition.SendTask:
		return v.RecoveryFlow
	case definition.BusinessRuleTask:
		return v.RecoveryFlow
	case definition.SubProcess:
		return v.RecoveryFlow
	case definition.CallActivity:
		return v.RecoveryFlow
	default:
		return ""
	}
}

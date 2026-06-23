package engine

import "github.com/zakyalvan/krtlwrkflw/model"

// compensationActionOf returns the CompensationAction of an activity node, or "".
func compensationActionOf(n model.Node) string {
	switch v := n.(type) {
	case model.ServiceTask:
		return v.CompensationAction
	case model.UserTask:
		return v.CompensationAction
	case model.ReceiveTask:
		return v.CompensationAction
	case model.SendTask:
		return v.CompensationAction
	case model.BusinessRuleTask:
		return v.CompensationAction
	case model.SubProcess:
		return v.CompensationAction
	case model.CallActivity:
		return v.CompensationAction
	default:
		return ""
	}
}

// cancelHandlerOf returns the CancelHandler of an activity node, or "".
func cancelHandlerOf(n model.Node) string {
	switch v := n.(type) {
	case model.ServiceTask:
		return v.CancelHandler
	case model.UserTask:
		return v.CancelHandler
	case model.ReceiveTask:
		return v.CancelHandler
	case model.SendTask:
		return v.CancelHandler
	case model.BusinessRuleTask:
		return v.CancelHandler
	case model.SubProcess:
		return v.CancelHandler
	case model.CallActivity:
		return v.CancelHandler
	default:
		return ""
	}
}

// recoveryFlowOf returns the RecoveryFlow of an activity node, or "".
func recoveryFlowOf(n model.Node) string {
	switch v := n.(type) {
	case model.ServiceTask:
		return v.RecoveryFlow
	case model.UserTask:
		return v.RecoveryFlow
	case model.ReceiveTask:
		return v.RecoveryFlow
	case model.SendTask:
		return v.RecoveryFlow
	case model.BusinessRuleTask:
		return v.RecoveryFlow
	case model.SubProcess:
		return v.RecoveryFlow
	case model.CallActivity:
		return v.RecoveryFlow
	default:
		return ""
	}
}

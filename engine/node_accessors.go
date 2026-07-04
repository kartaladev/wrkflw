package engine

import (
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
)

// compensationActionOf returns the CompensationAction of an activity node, or "".
func compensationActionOf(n definition.Node) string {
	switch v := n.(type) {
	case activity.ServiceTask:
		return v.CompensationAction
	case activity.UserTask:
		return v.CompensationAction
	case activity.ReceiveTask:
		return v.CompensationAction
	case activity.SendTask:
		return v.CompensationAction
	case activity.BusinessRuleTask:
		return v.CompensationAction
	case activity.SubProcess:
		return v.CompensationAction
	case activity.CallActivity:
		return v.CompensationAction
	default:
		return ""
	}
}

// cancelHandlerOf returns the CancelHandler of an activity node, or "".
func cancelHandlerOf(n definition.Node) string {
	switch v := n.(type) {
	case activity.ServiceTask:
		return v.CancelHandler
	case activity.UserTask:
		return v.CancelHandler
	case activity.ReceiveTask:
		return v.CancelHandler
	case activity.SendTask:
		return v.CancelHandler
	case activity.BusinessRuleTask:
		return v.CancelHandler
	case activity.SubProcess:
		return v.CancelHandler
	case activity.CallActivity:
		return v.CancelHandler
	default:
		return ""
	}
}

// recoveryFlowOf returns the RecoveryFlow of an activity node, or "".
func recoveryFlowOf(n definition.Node) string {
	switch v := n.(type) {
	case activity.ServiceTask:
		return v.RecoveryFlow
	case activity.UserTask:
		return v.RecoveryFlow
	case activity.ReceiveTask:
		return v.RecoveryFlow
	case activity.SendTask:
		return v.RecoveryFlow
	case activity.BusinessRuleTask:
		return v.RecoveryFlow
	case activity.SubProcess:
		return v.RecoveryFlow
	case activity.CallActivity:
		return v.RecoveryFlow
	default:
		return ""
	}
}

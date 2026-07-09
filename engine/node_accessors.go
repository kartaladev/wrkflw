package engine

import (
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
)

// compensateActionOf returns the CompensateAction of an activity node, or "".
func compensateActionOf(n model.Node) string {
	switch v := n.(type) {
	case activity.ServiceTask:
		return v.CompensateAction
	case activity.UserTask:
		return v.CompensateAction
	case activity.ReceiveTask:
		return v.CompensateAction
	case activity.SendTask:
		return v.CompensateAction
	case activity.BusinessRuleTask:
		return v.CompensateAction
	case activity.SubProcess:
		return v.CompensateAction
	case activity.CallActivity:
		return v.CompensateAction
	default:
		return ""
	}
}

// completionActionOf returns the CompletionAction of a completion-triggered
// activity node (UserTask, ReceiveTask), or "".
func completionActionOf(n model.Node) string {
	switch v := n.(type) {
	case activity.UserTask:
		return v.CompletionAction
	case activity.ReceiveTask:
		return v.CompletionAction
	default:
		return ""
	}
}

// cancelActionOf returns the CancelAction of an activity node, or "".
func cancelActionOf(n model.Node) string {
	switch v := n.(type) {
	case activity.ServiceTask:
		return v.CancelAction
	case activity.UserTask:
		return v.CancelAction
	case activity.ReceiveTask:
		return v.CancelAction
	case activity.SendTask:
		return v.CancelAction
	case activity.BusinessRuleTask:
		return v.CancelAction
	case activity.SubProcess:
		return v.CancelAction
	case activity.CallActivity:
		return v.CancelAction
	default:
		return ""
	}
}

// recoveryFlowOf returns the RecoveryFlow of an activity node, or "".
func recoveryFlowOf(n model.Node) string {
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

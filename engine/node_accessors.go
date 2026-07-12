package engine

import (
	"github.com/kartaladev/wrkflw/definition/model"
)

// compensateActionOf returns the CompensateAction of an activity node, or "".
// All 7 activity kinds (ServiceTask, UserTask, ReceiveTask, SendTask,
// BusinessRuleTask, SubProcess, CallActivity) carry it via the shared
// model.ActivityFields embed, so this delegates to the kind-agnostic
// model.CompensateActionOf rather than enumerating each concrete type.
func compensateActionOf(n model.Node) string {
	return model.CompensateActionOf(n)
}

// completionActionOf returns the CompletionAction of a completion-triggered
// activity node (UserTask, ReceiveTask), or "". Unlike the other three
// accessors, CompletionAction is honored only by these two kinds at execution
// time (see model.CompletionActionOf's godoc and the
// ErrCompletionActionUnsupportedKind validation rule), so the kind check is
// preserved here even though the field itself lives on the shared
// model.ActivityFields embed carried by all 7 activity kinds.
func completionActionOf(n model.Node) string {
	switch n.Kind() {
	case model.KindUserTask, model.KindReceiveTask:
		return model.CompletionActionOf(n)
	default:
		return ""
	}
}

// cancelActionOf returns the CancelAction of an activity node, or "".
func cancelActionOf(n model.Node) string {
	return model.CancelActionOf(n)
}

// recoveryFlowOf returns the RecoveryFlow of an activity node, or "".
func recoveryFlowOf(n model.Node) string {
	return model.RecoveryFlowOf(n)
}

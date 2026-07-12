package model

import (
	"github.com/kartaladev/wrkflw/definition/schedule"
)

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

// DeadlineOf returns the DeadlineTimer (schedule.TriggerSpec), DeadlineFlow, and
// DeadlineAction of a Node that carries deadline fields (activities and
// IntermediateCatchEvent). Returns a zero TriggerSpec and empty strings for nodes
// that do not carry deadline fields.
func DeadlineOf(n Node) (schedule.TriggerSpec, string, string) {
	if w, ok := n.(interface {
		deadline() (schedule.TriggerSpec, string, string)
	}); ok {
		return w.deadline()
	}
	return schedule.TriggerSpec{}, "", ""
}

// WaitActionOf returns the WaitEvery (schedule.TriggerSpec) and WaitAction
// of a Node that carries in-wait fields (activities and IntermediateCatchEvent).
// Returns a zero TriggerSpec and an empty string for nodes that do not carry
// in-wait fields.
func WaitActionOf(n Node) (schedule.TriggerSpec, string) {
	if w, ok := n.(interface {
		waitAction() (schedule.TriggerSpec, string)
	}); ok {
		return w.waitAction()
	}
	return schedule.TriggerSpec{}, ""
}

// CompletionActionOf returns the CompletionAction field of a node that carries
// ActivityFields (any activity kind), or "" for nodes that do not carry it at
// all (events, gateways). It is kind-agnostic — it does not check whether the
// concrete kind actually honors CompletionAction at execution time (only
// UserTask and ReceiveTask do); combine it with Node.Kind() where that
// distinction matters.
func CompletionActionOf(n Node) string {
	if a, ok := n.(interface{ completionAction() string }); ok {
		return a.completionAction()
	}
	return ""
}

// CompensateActionOf returns the CompensateAction field of a node that carries
// ActivityFields (any activity kind), or "" for nodes that do not carry it at
// all (events, gateways). It is kind-agnostic — it does not check whether the
// concrete kind can ever produce a compensation record at execution time (only
// a node that also ran a forward action can); combine it with Node.Kind() and
// CompletionActionOf where that distinction matters (see
// ErrCompensateActionWithoutForwardAction).
func CompensateActionOf(n Node) string {
	if a, ok := n.(interface{ compensateAction() string }); ok {
		return a.compensateAction()
	}
	return ""
}

// CancelActionOf returns the CancelAction field of a node that carries
// ActivityFields (any activity kind), or "" for nodes that do not carry it at
// all (events, gateways). Kind-agnostic, same contract as CompensateActionOf.
func CancelActionOf(n Node) string {
	if a, ok := n.(interface{ cancelAction() string }); ok {
		return a.cancelAction()
	}
	return ""
}

// RecoveryFlowOf returns the RecoveryFlow field of a node that carries
// ActivityFields (any activity kind), or "" for nodes that do not carry it at
// all (events, gateways). Kind-agnostic, same contract as CompensateActionOf.
func RecoveryFlowOf(n Node) string {
	if a, ok := n.(interface{ recoveryFlow() string }); ok {
		return a.recoveryFlow()
	}
	return ""
}

// ActionOf returns the Action field of a node that has one (ServiceTask or
// BusinessRuleTask), or "" for all other kinds.
func ActionOf(n Node) string {
	if t, ok := n.(interface{ taskAction() string }); ok {
		return t.taskAction()
	}
	return ""
}

package model

import (
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
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

// ActionOf returns the Action field of a node that has one (ServiceTask or
// BusinessRuleTask), or "" for all other kinds.
func ActionOf(n Node) string {
	if t, ok := n.(interface{ taskAction() string }); ok {
		return t.taskAction()
	}
	return ""
}

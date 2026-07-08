package definition

import (
	"github.com/zakyalvan/krtlwrkflw/definition/model"
)

// Warning is a non-fatal advisory produced by [Lint]: a node carries an option
// that its kind does not honour, so the option is silently ignored at runtime.
// Warnings are data, not errors — a consumer (e.g. the runtime driver) decides
// whether and how to surface them.
type Warning struct {
	// NodeID is the node the warning concerns.
	NodeID string
	// Rule is a stable, machine-readable code for the advisory (e.g. "reminder-ignored").
	Rule string
	// Detail is a human-readable explanation.
	Detail string
}

// reminderArmingKinds are the node kinds whose engine strategy actually arms an
// in-wait reminder. A reminder set on any other kind is silently ignored.
var reminderArmingKinds = map[model.NodeKind]struct{}{
	model.KindUserTask:               {},
	model.KindReceiveTask:            {},
	model.KindIntermediateCatchEvent: {},
}

// Lint inspects def for options that are set but not honoured by the node kind
// they are attached to, returning one [Warning] per offending node. It is a pure,
// side-effect-free advisory pass — it never mutates def and never fails.
//
// v1 rule "reminder-ignored": an in-wait reminder ([activity.WithWaitReminder] /
// [event.WithCatchWaitReminder]) is set on a node whose kind does not arm
// reminders (only UserTask, ReceiveTask, and IntermediateCatchEvent do). A nil
// def yields no warnings.
func Lint(def *model.ProcessDefinition) []Warning {
	if def == nil {
		return nil
	}
	var warnings []Warning
	for _, node := range def.Nodes {
		if node == nil {
			continue
		}
		if reminder, _ := model.ReminderOf(node); !reminder.IsZero() {
			if _, ok := reminderArmingKinds[node.Kind()]; !ok {
				warnings = append(warnings, Warning{
					NodeID: node.ID(),
					Rule:   "reminder-ignored",
					Detail: "in-wait reminder set on a node kind that does not arm reminders " +
						"(only UserTask, ReceiveTask, and IntermediateCatchEvent do); it will be silently ignored",
				})
			}
		}
	}
	return warnings
}

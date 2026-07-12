package model

import (
	"github.com/kartaladev/wrkflw/definition/schedule"
)

// Node is a single point in a process: an event, activity, or gateway. The
// concrete types (one per NodeKind) live in the node-family leaf packages —
// definition/event, definition/gateway, definition/activity — and are built with
// their New* constructors. Each embeds the shared identity/field-group types
// declared here (Base, ActivityFields, WaitFields, TaskAction).
type Node interface {
	Kind() NodeKind
	ID() string
	Name() string
}

// Base supplies the identity common to every node kind. Every concrete node type
// in the leaf packages embeds it.
type Base struct {
	id   string
	name string
}

// NewBase constructs the identity embed for a node. Leaf-package constructors
// call it; consumers use the New* constructors instead.
func NewBase(id, name string) Base { return Base{id: id, name: name} }

func (b Base) ID() string   { return b.id }
func (b Base) Name() string { return b.name }

// SetName sets the display name. Used by the WithName options in the leaf
// packages, which mutate the embedded Base.
func (b *Base) SetName(name string) { b.name = name }

// WaitFields holds the deadline + in-wait fields shared by activity kinds and by
// IntermediateCatchEvent (all of which can wait and so can carry a deadline
// escalation and periodic in-wait actions). It is embedded by ActivityFields and by
// event.IntermediateCatchEvent; the kind-agnostic accessors DeadlineOf/WaitActionOf
// dispatch on its (unexported) carrier methods.
type WaitFields struct {
	// DeadlineTimer is the trigger spec that governs when the deadline fires
	// (e.g. schedule.AfterDuration(72*time.Hour) or schedule.AfterExpr("deadlineExpr")).
	DeadlineTimer schedule.TriggerSpec
	// DeadlineFlow is the ID of the sequence flow to take on deadline breach.
	DeadlineFlow string
	// DeadlineAction is the name of the action.Action to invoke on deadline breach.
	DeadlineAction string
	// WaitEvery is the trigger spec that governs the in-wait action interval
	// (e.g. schedule.Every(24*time.Hour) or schedule.EveryExpr("waitExpr")).
	WaitEvery schedule.TriggerSpec
	// WaitAction is the name of the action.Action to invoke for each in-wait firing.
	WaitAction string
}

func (w WaitFields) deadline() (schedule.TriggerSpec, string, string) {
	return w.DeadlineTimer, w.DeadlineFlow, w.DeadlineAction
}
func (w WaitFields) waitAction() (schedule.TriggerSpec, string) {
	return w.WaitEvery, w.WaitAction
}

// ActivityFields holds the cross-cutting fields every activity kind shares (retry,
// recovery, compensation, cancel, plus the embedded WaitFields). Embedded into
// each activity type so the engine reads e.g. node.DeadlineDuration with no kind
// prefix. The RetryPolicyOf/recoveryFlowOf accessors dispatch on its carrier methods.
type ActivityFields struct {
	WaitFields
	// RetryPolicy is the optional per-node retry policy. Nil means use runtime default.
	RetryPolicy *RetryPolicy
	// RecoveryFlow is the ID of the sequence flow to take when retries are exhausted.
	RecoveryFlow string
	// CompensateAction is the name of the action.Action to invoke during rollback.
	CompensateAction string
	// CancelAction is the optional action.Action to run when this node is interrupted.
	CancelAction string
	// CompletionAction is the optional action.Action invoked when the node's
	// completion is triggered (human completion / message receive), before the
	// token advances. Its returned vars merge into the instance variables.
	CompletionAction string
}

func (a ActivityFields) retry() *RetryPolicy  { return a.RetryPolicy }
func (a ActivityFields) recoveryFlow() string { return a.RecoveryFlow }

// completionAction returns the raw CompletionAction field. Note this carrier is
// present on EVERY activity kind (CompletionAction lives on the shared
// ActivityFields embed) even though only UserTask/ReceiveTask honor it at
// execution time; CompletionActionOf is therefore kind-agnostic by design —
// callers that must restrict it to UserTask/ReceiveTask (e.g. validateStructure's
// ErrCompletionActionUnsupportedKind guard) combine it with Node.Kind().
func (a ActivityFields) completionAction() string { return a.CompletionAction }

// compensateAction returns the raw CompensateAction field. Present on every
// activity kind (CompensateAction lives on the shared ActivityFields embed);
// CompensateActionOf is therefore kind-agnostic by design.
func (a ActivityFields) compensateAction() string { return a.CompensateAction }

// TaskAction holds the action reference shared by ServiceTask and BusinessRuleTask:
// the catalog action name. Embedded so the ActionOf accessor dispatches on its
// carrier method across the activity leaf.
type TaskAction struct {
	// Action is the service-action name; empty means default to the node id.
	Action string
}

func (t TaskAction) taskAction() string { return t.Action }

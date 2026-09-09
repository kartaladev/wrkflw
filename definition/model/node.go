package model

import (
	"github.com/kartaladev/wrkflw/definition/schedule"
)

// Node is a single point in a process: an event, activity, or gateway.
//
// Node is a CLOSED SET, not a consumer extension seam. The concrete types — one
// per NodeKind — live in the node-family leaf packages (definition/event,
// definition/gateway, definition/activity), are built with their New*
// constructors, and each embeds the shared identity/field-group types declared
// here (Base, ActivityFields, WaitFields, TaskAction). Implementing this
// interface outside those packages is not supported, and the engine will not
// execute the result.
//
// The interface cannot enforce that by itself. Sealing it with an unexported
// method does not work here: the leaf types satisfy Node by embedding Base, and
// any consumer can embed Base too, inheriting the seal along with it. So the set
// is closed on either side of the interface instead:
//
//   - Registration is closed at compile time. RegisterKind takes a capability
//     token from definition/internal/kindreg, which only definition/... can
//     import, so a consumer cannot claim a kind.
//   - Construction is closed at the two ingresses through which a definition
//     legitimately enters the module: model.Validate and Builder.Build. Each
//     kind records the concrete type its FromWire returns, and both ingresses
//     run the same check, rejecting a node whose dynamic type differs with
//     ErrForeignNodeType.
//
// That second half is what lets the leaf NodeSpec functions and
// engine/step_nodes.go assert node.(activity.UserTask) bare rather than
// defensively — but it holds only for a node that arrived through one of those
// two doors, plus ProcessDefinition.MarshalJSON, which runs the same check
// (#147). Two paths are still deliberately not gated and panic on a
// counterfeit: engine.Step, which does not validate (#53's escape hatch); and
// the exported ValidationStrategyFor, which returns no error and so cannot
// report one. ErrForeignNodeType's doc comment carries the authoritative list.
//
// Consumers extend workflows through actions and validation strategies, which
// are registration seams built for it. Consumer-defined KINDS are a different
// feature and are not available: a new kind needs execution semantics in the
// engine's strategy table, not merely a type that satisfies this interface, so
// supporting them would mean designing a strategy-registration seam of its own.
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
// IntermediateCatchEvent (all of which can wait and so can carry a deadline and
// periodic in-wait actions). It is embedded by ActivityFields and by
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

// cancelAction returns the raw CancelAction field. Present on every activity
// kind (CancelAction lives on the shared ActivityFields embed); CancelActionOf
// is therefore kind-agnostic by design.
func (a ActivityFields) cancelAction() string { return a.CancelAction }

// TaskAction holds the action reference shared by ServiceTask and BusinessRuleTask:
// the catalog action name. Embedded so the ActionOf accessor dispatches on its
// carrier method across the activity leaf.
type TaskAction struct {
	// Action is the service-action name; empty means default to the node id.
	Action string
}

func (t TaskAction) taskAction() string { return t.Action }

// RuleReference holds the reserved rule-engine reference carried by
// BusinessRuleTask. It is a model-side field group, embedded by the leaf type, so
// that the kind-agnostic RuleOf accessor dispatches on its carrier method exactly
// as ActionOf does on TaskAction's — an unexported carrier declared in a leaf
// package would not satisfy an interface written here.
type RuleReference struct {
	// Rule is the reserved rule-engine reference: a catalog name or an inline rule
	// document (see RuleSpec). nil means unset, which is the only state Validate
	// accepts until the rule-engine adapter ships (ErrRuleNotSupported).
	Rule *RuleSpec
}

func (r RuleReference) rule() *RuleSpec { return r.Rule }

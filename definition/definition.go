// Package definition is the root of the process-definition authoring layer. It
// is a thin aggregator: the core types and logic live in definition/model, the
// node kinds in definition/{event,gateway,activity}, sequence flows in
// definition/flow, and the fluent builder in definition/build. This package
// re-exports the core public surface and provides NewBuilder — the fluent entry
// point — so consumers can start from a single, well-named package:
//
//	def, err := definition.NewBuilder("order", 1).
//		AddStartEvent("start").
//		AddServiceTask("charge", activity.WithActionName("charge-card")).
//		AddEndEvent("end").
//		Connect("start", "charge").Connect("charge", "end").
//		Build()
//
// Because this package imports definition/build (which imports every node-family
// leaf), importing definition also registers every node kind for
// (de)serialization; deserialization paths that import only definition/model
// should blank-import definition/kinds.
package definition

import (
	"github.com/zakyalvan/krtlwrkflw/definition/build"
	"github.com/zakyalvan/krtlwrkflw/definition/flow"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
)

// NewBuilder starts the fluent builder for a definition with the given id and
// version. It is the root-package entry point for Go authoring; each AddX method
// mirrors a node-family constructor, and Build returns a *ProcessDefinition.
func NewBuilder(id string, version int) *build.Builder { return build.New(id, version) }

// --- re-exported core types (definition/model) ---

type (
	Node              = model.Node
	NodeKind          = model.NodeKind
	ProcessDefinition = model.ProcessDefinition
	RetryPolicy       = model.RetryPolicy
	Base              = model.Base
	ActivityFields    = model.ActivityFields
	WaitFields        = model.WaitFields
	TaskAction        = model.TaskAction
	NodeWire          = model.NodeWire
	NodeSpec          = model.NodeSpec
	DefinitionBuilder = model.DefinitionBuilder
	DefinitionLoader  = model.DefinitionLoader
)

// SequenceFlow is a directed edge between two nodes; it lives in definition/flow
// and is re-exported here for convenience.
type SequenceFlow = flow.SequenceFlow

// --- re-exported functions and accessors (definition/model) ---

var (
	Validate           = model.Validate
	NewBase            = model.NewBase
	DefaultRetryPolicy = model.DefaultRetryPolicy
	ParseYAML          = model.ParseYAML
	LoadYAML           = model.LoadYAML
	RegisterKind       = model.RegisterKind
	RetryPolicyOf      = model.RetryPolicyOf
	DeadlineOf         = model.DeadlineOf
	ReminderOf         = model.ReminderOf
	ActionOf           = model.ActionOf
	InlineActionOf     = model.InlineActionOf
)

// --- re-exported NodeKind constants (definition/model) ---

const (
	KindUnspecified            = model.KindUnspecified
	KindStartEvent             = model.KindStartEvent
	KindEndEvent               = model.KindEndEvent
	KindTerminateEndEvent      = model.KindTerminateEndEvent
	KindErrorEndEvent          = model.KindErrorEndEvent
	KindServiceTask            = model.KindServiceTask
	KindUserTask               = model.KindUserTask
	KindReceiveTask            = model.KindReceiveTask
	KindSendTask               = model.KindSendTask
	KindBusinessRuleTask       = model.KindBusinessRuleTask
	KindSubProcess             = model.KindSubProcess
	KindCallActivity           = model.KindCallActivity
	KindEventSubProcess        = model.KindEventSubProcess
	KindIntermediateCatchEvent = model.KindIntermediateCatchEvent
	KindIntermediateThrowEvent = model.KindIntermediateThrowEvent
	KindBoundaryEvent          = model.KindBoundaryEvent
	KindExclusiveGateway       = model.KindExclusiveGateway
	KindParallelGateway        = model.KindParallelGateway
	KindInclusiveGateway       = model.KindInclusiveGateway
	KindEventBasedGateway      = model.KindEventBasedGateway
)

// --- re-exported sentinel errors (definition/model) ---

var (
	ErrActionInlineAndNameConflict = model.ErrActionInlineAndNameConflict
	ErrDuplicateScopedAction       = model.ErrDuplicateScopedAction
	ErrKindNotRegistered           = model.ErrKindNotRegistered
	ErrNoStartEvent                = model.ErrNoStartEvent
	ErrMultipleStartEvents         = model.ErrMultipleStartEvents
	ErrDanglingFlow                = model.ErrDanglingFlow
	ErrDeadEnd                     = model.ErrDeadEnd
	ErrStartHasIncoming            = model.ErrStartHasIncoming
	ErrEndHasOutgoing              = model.ErrEndHasOutgoing
	ErrConditionNotAllowed         = model.ErrConditionNotAllowed
	ErrDefaultNotAllowed           = model.ErrDefaultNotAllowed
	ErrMultipleDefaults            = model.ErrMultipleDefaults
	ErrEventGatewayTarget          = model.ErrEventGatewayTarget
	ErrMixedGateway                = model.ErrMixedGateway
	ErrUnreachableNode             = model.ErrUnreachableNode
	ErrUnpairedJoin                = model.ErrUnpairedJoin
	ErrBoundaryAttachment          = model.ErrBoundaryAttachment
	ErrBoundaryErrorHost           = model.ErrBoundaryErrorHost
	ErrMissingSubprocess           = model.ErrMissingSubprocess
	ErrMissingDefRef               = model.ErrMissingDefRef
	ErrInvalidRetryPolicy          = model.ErrInvalidRetryPolicy
	ErrInvalidRecoveryFlow         = model.ErrInvalidRecoveryFlow
	ErrEmptyCancelAction           = model.ErrEmptyCancelAction
	ErrCompensateRefNotFound       = model.ErrCompensateRefNotFound
)

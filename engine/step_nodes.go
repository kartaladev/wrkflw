package engine

import (
	"time"

	"github.com/zakyalvan/krtlwrkflw/model"
)

// stepCtx carries the repeated inputs to the node-kind dispatch layer.
// tdef is the scope-resolved process definition for the current token;
// def is the top-level definition. Both are provided by drive().
type stepCtx struct {
	def  *model.ProcessDefinition
	tdef *model.ProcessDefinition
	s    *InstanceState
	at   time.Time
	mode StepMode
	// opt is added in Task 3 when strategies that consume StepOptions (e.g. retry
	// policy resolution via effectiveRetryPolicy) are migrated to the registry.
}

// nodeStrategy executes node-entry for one NodeKind.
// Implementations are stateless zero-size structs; the registry is built once
// at package init and never mutated.
type nodeStrategy interface {
	// enter runs the node's entry logic for tok. It returns the commands
	// produced for this token and, via tok mutations, updates the token state.
	// It does NOT append to a shared slice — drive() appends the returned
	// commands to its accumulator.
	enter(c *stepCtx, tok *Token, node model.Node) ([]Command, error)
}

// nodeStrategies maps each arm-bearing NodeKind to its strategy.
// Kinds NOT in this map (KindTerminateEndEvent, KindBusinessRuleTask,
// KindReceiveTask, KindSendTask, KindBoundaryEvent, KindEventSubProcess,
// KindUnspecified) fall through to the post-dispatch logic in drive() unchanged.
var nodeStrategies = map[model.NodeKind]nodeStrategy{
	model.KindServiceTask: serviceTaskStrategy{},
	// remaining kinds added in Task 3
}

// serviceTaskStrategy handles KindServiceTask node entry.
type serviceTaskStrategy struct{}

func (serviceTaskStrategy) enter(c *stepCtx, tok *Token, node model.Node) ([]Command, error) {
	var cmds []Command
	cmdID := c.s.nextCommandID()
	cmds = append(cmds, InvokeAction{
		CommandID: cmdID,
		Name:      node.Action,
		Input:     serviceActionInput(c.s, node),
	})
	tok.State = TokenWaitingCommand
	tok.AwaitCommand = cmdID
	// Arm any boundary events attached to this host activity.
	bndCmds, err := armBoundaries(c.tdef, c.s, tok.ID, node.ID, c.at)
	if err != nil {
		return cmds, err
	}
	cmds = append(cmds, bndCmds...)
	return cmds, nil
}

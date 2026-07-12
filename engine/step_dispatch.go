package engine

import (
	"github.com/kartaladev/wrkflw/definition/model"
)

// nodeStrategy executes node-entry for one NodeKind.
// Implementations are stateless zero-size structs; the registry is built once
// at package init and never mutated.
type nodeStrategy interface {
	// enter runs the node's entry logic for tok. It returns the commands
	// produced for this token and, via tok mutations, updates the token state.
	// It does NOT append to a shared slice — drive() appends the returned
	// commands to its accumulator.
	//
	// halt signals that drive() must exit immediately (return cmds, nil) rather
	// than continuing to the next active token. Only endEventStrategy's error
	// branch (an EndEvent with Behavior==EndError, ADR-0127) returns halt=true;
	// all other strategies and end behaviors return halt=false.
	//
	// Stopped semantics: drive() derives stopped = tok.State != TokenActive
	// after a registry hit. Strategies that auto-advance (e.g. StartEvent)
	// leave tok.State == TokenActive so stopped=false. Strategies that park or
	// consume the token must ensure tok.State != TokenActive. For consumed tokens
	// (where consumeToken already removed them from the slice), strategies that
	// want stopped=true must set tok.State = TokenWaitingCommand explicitly.
	// Strategies that want stopped=false on a consumed token (e.g. EndEvent
	// sub-process "break" paths where a continuation token was placed) must leave
	// tok.State == TokenActive.
	enter(c *stepCtx, tok *Token, node model.Node) (cmds []Command, halt bool, err error)
}

// nodeStrategies maps each arm-bearing NodeKind to its strategy.
// Kinds NOT in this map (KindBoundaryEvent, KindUnspecified) fall through to the
// post-dispatch logic in drive() unchanged.
var nodeStrategies = map[model.NodeKind]nodeStrategy{
	model.KindServiceTask:            serviceTaskStrategy{},
	model.KindBusinessRuleTask:       businessRuleTaskStrategy{},
	model.KindReceiveTask:            receiveTaskStrategy{},
	model.KindSendTask:               sendTaskStrategy{},
	model.KindStartEvent:             startEventStrategy{},
	model.KindEndEvent:               endEventStrategy{},
	model.KindSubProcess:             subProcessStrategy{},
	model.KindUserTask:               userTaskStrategy{},
	model.KindIntermediateCatchEvent: intermediateCatchEventStrategy{},
	model.KindExclusiveGateway:       exclusiveGatewayStrategy{},
	model.KindParallelGateway:        parallelGatewayStrategy{},
	model.KindInclusiveGateway:       inclusiveGatewayStrategy{},
	model.KindEventBasedGateway:      eventBasedGatewayStrategy{},
	model.KindCallActivity:           callActivityStrategy{},
	model.KindIntermediateThrowEvent: intermediateThrowEventStrategy{},
	model.KindCompensationThrowEvent: compensationThrowEventStrategy{},
}

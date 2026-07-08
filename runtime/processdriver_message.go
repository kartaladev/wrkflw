package runtime

import (
	"context"
	"strconv"

	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/validation"
)

// DeliverMessage finds the single process instance that is currently waiting for
// a message with the given name and correlationKey, then delivers a
// [engine.MessageReceived] trigger to it. If no matching instance is found it
// is a clean no-op.
//
// The runner tracks message waiters internally via [syncMsgWaiters], which is
// called after each deliverLoop iteration. This keeps the state in sync without
// requiring an enumeration API on Store.
//
// def is required to call ApplyTrigger on the matched instance.
func (driver *ProcessDriver) DeliverMessage(ctx context.Context, def *model.ProcessDefinition, name, correlationKey string, payload map[string]any) error {
	instanceID, found := driver.findMessageWaiter(name, correlationKey)
	if !found {
		return nil
	}
	// Validate the payload against the woken node's PayloadValidation strategy, if
	// any, BEFORE applying the trigger. MessageTargetNode mirrors the engine's own
	// dispatch priority (event-based-gateway arm, boundary arm, event-subprocess
	// arm, then standalone parked token) so the node picked here is the same node
	// ApplyTrigger will actually wake. Only tier-4 standalone ReceiveTask /
	// IntermediateCatchEvent nodes carry a PayloadValidation slot; tiers 1-3
	// (gateway arm / boundary / event-subprocess) have none, so validation is
	// skipped for those wakes.
	st, _, err := driver.store.Load(ctx, instanceID)
	if err != nil {
		return err
	}
	if nodeID, ok := st.MessageTargetNode(name, correlationKey); ok {
		if strat := payloadValidationStrategy(def, nodeID); strat != nil {
			key := def.ID + ":" + strconv.Itoa(def.Version) + ":" + nodeID
			if verr := driver.validationGate.Validate(ctx, key, strat, payload); verr != nil {
				return verr
			}
		}
	}

	trg := engine.NewMessageReceived(driver.clk.Now(), name, correlationKey, payload)
	_, err = driver.ApplyTrigger(ctx, def, instanceID, trg)
	return err
}

// payloadValidationStrategy returns the PayloadValidation strategy carried by
// the node nodeID in def, or nil if the node has no such slot (either it isn't
// a ReceiveTask/IntermediateCatchEvent, or it doesn't validate).
func payloadValidationStrategy(def *model.ProcessDefinition, nodeID string) validation.ValidationStrategy {
	n, ok := def.Node(nodeID)
	if !ok {
		return nil
	}
	switch nn := n.(type) {
	case activity.ReceiveTask:
		return nn.PayloadValidation
	case event.IntermediateCatchEvent:
		return nn.PayloadValidation
	default:
		return nil
	}
}

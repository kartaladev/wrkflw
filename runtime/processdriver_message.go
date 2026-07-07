package runtime

import (
	"context"

	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
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
// def is required to call Deliver on the matched instance.
func (driver *ProcessDriver) DeliverMessage(ctx context.Context, def *model.ProcessDefinition, name, correlationKey string, payload map[string]any) error {
	instanceID, found := driver.findMessageWaiter(name, correlationKey)
	if !found {
		return nil
	}
	trg := engine.NewMessageReceived(driver.clk.Now(), name, correlationKey, payload)
	_, err := driver.Deliver(ctx, def, instanceID, trg)
	return err
}

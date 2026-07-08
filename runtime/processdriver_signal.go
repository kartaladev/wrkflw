package runtime

import (
	"context"
	"fmt"
)

// BroadcastSignal publishes a signal with the given name and payload to every
// process instance currently awaiting it, through the [signal.SignalBus] the
// driver owns. It is the signal counterpart to [ProcessDriver.DeliverMessage]:
// a consumer broadcasts through the driver facade rather than holding and
// reaching into the bus directly.
//
// Signal semantics are broadcast-by-name: the payload reaches ALL instances
// parked on name, with no correlation key. Use this only for genuine fan-out;
// for a per-entity wait that must resume a single instance, use
// [ProcessDriver.DeliverMessage] with a correlation key instead.
//
// It returns a descriptive error if no SignalBus is configured (via
// [WithSignalBus]); a delivered signal that matches no waiter is a clean no-op.
func (driver *ProcessDriver) BroadcastSignal(ctx context.Context, name string, payload map[string]any) error {
	if driver.sigbus == nil {
		return fmt.Errorf("workflow-runtime: BroadcastSignal %q: no SignalBus configured (use WithSignalBus)", name)
	}
	return driver.sigbus.Publish(ctx, name, payload)
}

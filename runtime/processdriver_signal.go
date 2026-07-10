package runtime

import (
	"context"
	"errors"
	"fmt"
)

// BroadcastSignal publishes a signal with the given name and payload to every
// process instance currently awaiting it, through the [signal.SignalBus] the
// driver owns, and additionally STARTS one new instance per registered
// definition whose start event listens for name (ADR-0121). It is the signal
// counterpart to [ProcessDriver.DeliverMessage]: a consumer broadcasts through
// the driver facade rather than holding and reaching into the bus directly.
//
// Signal semantics are broadcast-by-name: the payload reaches ALL instances
// parked on name, with no correlation key, AND every matching signal-start
// definition — signal fan-out is NOT deduped, unlike message-start: each
// broadcast is a distinct event and mints a fresh instance per match, which is
// correct BPMN signal-start semantics. Use this only for genuine fan-out; for a
// per-entity wait that must resume a single instance, use
// [ProcessDriver.DeliverMessage] with a correlation key instead.
//
// An empty name is a clean no-op (nil): it is meaningless and must never match a
// manual (trigger-less) start, whose SignalName is also "". Otherwise it returns
// a descriptive error only when there is NEITHER a SignalBus configured (via
// [WithSignalBus]) NOR any signal-start match — i.e. the broadcast could not
// possibly do anything. A delivered signal that matches no waiter and no
// signal-start is otherwise a clean no-op; errors encountered while resuming
// waiters or creating signal-start instances are joined via [errors.Join] rather
// than aborting the rest of the fan-out.
func (driver *ProcessDriver) BroadcastSignal(ctx context.Context, name string, payload map[string]any) error {
	// An empty signal name is meaningless and must never match a manual
	// (trigger-less) start, whose SignalName is also "" — a clean no-op.
	if name == "" {
		return nil
	}

	hits := signalStartDefs(driver.listDefinitions(ctx), name)
	if driver.sigbus == nil && len(hits) == 0 {
		return fmt.Errorf("workflow-runtime: BroadcastSignal %q: no SignalBus configured and no signal start (use WithSignalBus)", name)
	}

	var errs []error
	if driver.sigbus != nil {
		if err := driver.sigbus.Publish(ctx, name, payload); err != nil {
			errs = append(errs, err)
		}
	}
	for _, h := range hits {
		// Empty instanceID: signal-start create is not deduped — each broadcast
		// mints a fresh instance via the driver's id generator (see createAtNode).
		if _, err := driver.createAtNode(ctx, h.Def, h.NodeID, "", payload); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

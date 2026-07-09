package runtime

import (
	"context"
	"fmt"

	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/engine"
)

// ReverseOption configures a [ProcessDriver.ReverseInstance] call. Construct
// with [WithFullReverse] or [WithTargetNode]; they are mutually exclusive.
type ReverseOption func(*reverseConfig)

// reverseConfig accumulates the ReverseOption values applied to a single
// ReverseInstance call before it is validated and dispatched.
type reverseConfig struct {
	full     bool
	target   string
	targeted bool
}

// WithFullReverse compensates every recorded activity (LIFO), resets the
// instance's variables back to the values it started with, and resumes the
// instance at its start node (Running). This is the default behaviour when
// [ProcessDriver.ReverseInstance] is called with no option at all; passing it
// explicitly is equivalent. It is mutually exclusive with [WithTargetNode].
func WithFullReverse() ReverseOption {
	return func(c *reverseConfig) { c.full = true }
}

// WithTargetNode compensates back to nodeID (exclusive — nodeID's own
// compensation record is not re-run) and resumes the instance at nodeID
// (Running), keeping the instance's current variables as-is. It is mutually
// exclusive with [WithFullReverse].
//
// nodeID is matched against [engine.CompensationRecord.NodeID]. When the same
// node was visited more than once (e.g. a retry loop), the walk resolves to the
// most-recently completed visit — see [engine.NewCompensateRequested].
func WithTargetNode(nodeID string) ReverseOption {
	return func(c *reverseConfig) { c.targeted = true; c.target = nodeID }
}

// ReverseInstance rolls a running (or already-compensating) instance backward
// WITHOUT terminating it — termination remains [ProcessDriver.CancelInstance]'s
// job. With no option (or with [WithFullReverse]) it compensates everything
// recorded so far and resumes fresh at the definition's start node, with
// variables reset to their start-of-instance values. With [WithTargetNode] it
// compensates back to a specific node and resumes there, keeping the instance's
// current variables.
//
// ReverseInstance rejects a terminal instance (Completed, Failed, or
// Terminated) with a descriptive error before touching any state — reversing a
// terminal instance is not a supported admin/debug operation, unlike
// [ProcessDriver.CancelInstance] which treats re-cancelling a terminal instance
// as an idempotent no-op. It also rejects supplying both [WithFullReverse] and
// [WithTargetNode] on the same call, and a full reverse against a definition
// that does not resolve to exactly one start event — both checked before any
// state change.
//
// It returns the reversed [engine.InstanceState].
func (driver *ProcessDriver) ReverseInstance(ctx context.Context, def *model.ProcessDefinition, instanceID string, opts ...ReverseOption) (engine.InstanceState, error) {
	var cfg reverseConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.full && cfg.targeted {
		return engine.InstanceState{}, fmt.Errorf("workflow-runtime: ReverseInstance %q: WithFullReverse and WithTargetNode are mutually exclusive", instanceID)
	}
	if cfg.targeted && cfg.target == "" {
		return engine.InstanceState{}, fmt.Errorf("workflow-runtime: ReverseInstance %q: WithTargetNode requires a non-empty node ID", instanceID)
	}

	st, _, err := driver.store.Load(ctx, instanceID)
	if err != nil {
		return engine.InstanceState{}, fmt.Errorf("workflow-runtime: ReverseInstance %q: load: %w", instanceID, err)
	}
	if st.Status != engine.StatusRunning && st.Status != engine.StatusCompensating {
		return engine.InstanceState{}, fmt.Errorf("workflow-runtime: ReverseInstance %q: instance is terminal (status %s), only a Running or Compensating instance can be reversed", instanceID, st.Status)
	}

	if cfg.targeted {
		return driver.ApplyTrigger(ctx, def, instanceID, engine.NewCompensateRequested(driver.clk.Now(), cfg.target))
	}

	starts := def.StartNodes()
	if len(starts) != 1 {
		return engine.InstanceState{}, fmt.Errorf("workflow-runtime: ReverseInstance %q: definition %q must have exactly one start event to resolve a full reverse, found %d", instanceID, def.ID, len(starts))
	}
	return driver.ApplyTrigger(ctx, def, instanceID, engine.NewReverseToStart(driver.clk.Now(), starts[0].ID()))
}

package runtime

import (
	"context"
	"fmt"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
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
// (Running), restoring the instance's Variables to nodeID's own
// start-of-visit snapshot — the variables as they stood the moment execution
// first arrived at nodeID, before nodeID ran — discarding any mutation made
// after that point. It is mutually exclusive with [WithFullReverse].
//
// If nodeID's start-of-visit snapshot is empty/nil (only possible when the
// instance started with no variables at all and nodeID was the first
// recorded node), the current variables are left untouched rather than being
// wiped to an empty map.
//
// nodeID is matched against [engine.CompensationRecord.NodeID]. When the same
// node was visited more than once (e.g. a retry loop), the walk resolves to the
// most-recently completed visit — see [engine.NewReverseToNode].
func WithTargetNode(nodeID string) ReverseOption {
	return func(c *reverseConfig) { c.targeted = true; c.target = nodeID }
}

// ReverseInstance rolls a running (or already-compensating) instance backward
// WITHOUT terminating it — termination remains [ProcessDriver.CancelInstance]'s
// job. With no option (or with [WithFullReverse]) it compensates everything
// recorded so far and resumes fresh at the definition's start node, with
// variables reset to their start-of-instance values. With [WithTargetNode] it
// compensates back to a specific node and resumes there, restoring the
// instance's variables to that node's own start-of-visit snapshot (see
// [WithTargetNode] for the exact semantics, including the empty-snapshot
// carve-out).
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
// The engine independently rejects reversing a terminal instance and
// reversing while a compensation walk is already in flight (ADR-0109
// hardening) — defense in depth against the TOCTOU window between this
// facade's pre-check Load and the engine's own Step, so this facade's guards
// are not the only line of defense.
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
		return driver.applyTrigger(ctx, def, instanceID, engine.NewReverseToNode(driver.clk.Now(), cfg.target))
	}

	starts := def.StartNodes()
	if len(starts) != 1 {
		return engine.InstanceState{}, fmt.Errorf("workflow-runtime: ReverseInstance %q: definition %q must have exactly one start event to resolve a full reverse, found %d", instanceID, def.ID, len(starts))
	}
	return driver.applyTrigger(ctx, def, instanceID, engine.NewReverseToStart(driver.clk.Now(), starts[0].ID()))
}

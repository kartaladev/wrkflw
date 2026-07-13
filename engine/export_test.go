// export_test.go exposes unexported methods on InstanceState for white-box
// testing from the engine_test package. This file is compiled only during
// `go test` (it belongs to package engine, not engine_test) and is therefore
// invisible to consumers of the library.
//
// Pattern: thin, named shim functions that forward to the unexported methods.
// No logic lives here — only delegation.
package engine

import (
	"context"
	"time"

	"github.com/kartaladev/wrkflw/definition/model"
)

// OpenScope exposes (*InstanceState).openScope for engine_test.
func OpenScope(s *InstanceState, nodeID, parentScopeID string) string {
	return s.openScope(nodeID, parentScopeID)
}

// TokensInScope exposes (*InstanceState).tokensInScope for engine_test.
func TokensInScope(s *InstanceState, scopeID string) int {
	return s.tokensInScope(scopeID)
}

// CloseScope exposes (*InstanceState).closeScope for engine_test.
func CloseScope(s *InstanceState, scopeID string) {
	s.closeScope(scopeID)
}

// ScopeByID exposes (*InstanceState).scopeByID for engine_test.
func ScopeByID(s *InstanceState, id string) *Scope {
	return s.scopeByID(id)
}

// BeginCompensation exposes beginCompensation for engine_test. Used to test
// the non-zero FinalStatus/FinalErr outcome branch of stepCompensationFinish
// without going through a full trigger-dispatch path.
func BeginCompensation(ctx context.Context, def *model.ProcessDefinition, s *InstanceState, toNode string, finalStatus Status, finalErr string, at time.Time, mode StepMode) (StepResult, error) {
	return beginCompensation(ctx, def, s, at, mode, conditions, compensationOutcome{ToNode: toNode, FinalStatus: finalStatus, FinalErr: finalErr})
}

// ArmBoundaryTimerForHost appends a boundaryArm for a timer boundary event
// attached to hostToken/hostNode directly to s.Boundaries, for engine_test.
//
// This bypasses the normal arming path (armBoundaries, called from drive()'s
// per-node-kind strategies) so tests can exercise the arm-cleanup machinery
// (e.g. removeBoundaryArmsForHost) for a host kind the engine does not yet
// call armBoundaries for — currently KindCallActivity: a CallActivity may
// validly carry an attached boundary timer/signal/message event (definition
// validation allows it), but callActivityStrategy.enter (engine/step_nodes.go)
// only checks the direct-attachment ERROR-boundary case via findDirectBoundary
// (ADR-0128) and never arms non-error boundary siblings. This helper lets a
// test simulate "an arm exists for this host" independent of whether/when that
// gap is closed, so the cleanup path (e.g. handleSubInstanceFailed's
// consume callback) is verified in isolation.
func ArmBoundaryTimerForHost(s *InstanceState, hostToken, hostNode, boundaryNode, timerID string) {
	s.Boundaries = append(s.Boundaries, boundaryArm{
		HostToken:    hostToken,
		HostNode:     hostNode,
		BoundaryNode: boundaryNode,
		triggerMatch: triggerMatch{TimerID: timerID},
	})
}

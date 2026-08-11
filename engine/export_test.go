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
	"fmt"
	"time"

	"github.com/kartaladev/wrkflw/definition/model"
)

// TimerRecordView is a read-only projection of one internal timerRecord, so
// engine_test can assert on the engine's timer bookkeeping without the
// unexported type escaping the package.
type TimerRecordView struct {
	TimerID   string
	Kind      TimerKind
	Token     string
	TaskID    string
	NodeID    string
	ScopeID   string
	CommandID string
}

// TimerRecords projects s.Timers for engine_test, preserving slice order.
func TimerRecords(s *InstanceState) []TimerRecordView {
	out := make([]TimerRecordView, 0, len(s.Timers))
	for _, tr := range s.Timers {
		out = append(out, TimerRecordView(tr))
	}
	return out
}

// CompensationCursorView returns a comparable snapshot of s.Compensating, so a
// test can assert the cursor is byte-identical across a step without the
// unexported cursor type escaping the package.
func CompensationCursorView(s *InstanceState) string {
	return fmt.Sprintf("%+v", s.Compensating)
}

// RemoveIncidentsForToken exposes (*InstanceState).removeIncidentsForToken for engine_test.
func RemoveIncidentsForToken(s *InstanceState, tokenID string) {
	s.removeIncidentsForToken(tokenID)
}

// CancelOpenTasks exposes (*InstanceState).cancelOpenTasks for engine_test.
func CancelOpenTasks(s *InstanceState) []Command {
	return s.cancelOpenTasks()
}

// EndInstance exposes (*InstanceState).endInstance for engine_test. The eight
// terminal sites are all reachable through Step, so this shim exists for the one
// case no production path can construct: an [Incident] whose TokenID is empty,
// which the orphaned-incident sweep must treat as naming nothing (ADR-0152).
func EndInstance(s *InstanceState, status Status, at time.Time, terminal Command) []Command {
	return s.endInstance(status, at, terminal)
}

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

// DescendantScopeIDs exposes (*InstanceState).descendantScopeIDs for engine_test.
func DescendantScopeIDs(s *InstanceState, scopeID string) map[string]bool {
	return s.descendantScopeIDs(scopeID)
}

// CloseScopeDescendants exposes (*InstanceState).closeScopeDescendants for engine_test.
func CloseScopeDescendants(s *InstanceState, scopeID string) {
	s.closeScopeDescendants(scopeID)
}

// TokensInScopeSubtree exposes (*InstanceState).tokensInScopeSubtree for engine_test.
func TokensInScopeSubtree(s *InstanceState, scopeID string) int {
	return s.tokensInScopeSubtree(scopeID)
}

// HasChildScopeWithTokens exposes (*InstanceState).hasChildScopeWithTokens for engine_test.
func HasChildScopeWithTokens(s *InstanceState, parentID, exceptID string) bool {
	return s.hasChildScopeWithTokens(parentID, exceptID)
}

// ScopeByID exposes (*InstanceState).scopeByID for engine_test.
func ScopeByID(s *InstanceState, id string) *Scope {
	return s.scopeByID(id)
}

// BeginCompensation exposes beginCompensation for engine_test. Used to test
// the non-zero FinalStatus/FinalErr outcome branch of stepCompensationFinish
// without going through a full trigger-dispatch path.
func BeginCompensation(ctx context.Context, def *model.ProcessDefinition, s *InstanceState, toNode string, finalStatus Status, finalErr string, at time.Time, mode StepMode) (StepResult, error) {
	return beginCompensation(ctx, def, s, at, stepPolicy{mode: mode, eval: conditions}, compensationOutcome{ToNode: toNode, FinalStatus: finalStatus, FinalErr: finalErr})
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

// TimersAreNil reports whether s.Timers is nil (as opposed to an empty slice),
// for engine_test assertions about persisted snapshot shape.
func TimersAreNil(s *InstanceState) bool { return s.Timers == nil }

// SpawnsNewWork exposes (*InstanceState).spawnsNewWork for engine_test, so a
// test can ASSERT that its fixture is a dying instance rather than assume it.
func SpawnsNewWork(s *InstanceState) bool { return s.spawnsNewWork() }

// AppendStallTimerRecord appends a TimerCompensationStall record directly, for
// engine_test.
//
// It exists for the state no production path inside one build can construct: a
// stall record whose CommandID no longer matches the cursor. All four arm sites
// — beginCompensation, startCompensationWalk, stepCompensationAdvance and the
// retry verb — go through the same cancel-then-arm helper, so a live walk with
// detection enabled holds exactly one record and it always MATCHES. The
// late-fire guard it lets us test is defence against the gap between the
// engine's record and the scheduler's job — a timer the scheduler still holds
// after the engine moved on.
func AppendStallTimerRecord(s *InstanceState, timerID, nodeID, scopeID, commandID string) {
	s.Timers = append(s.Timers, timerRecord{
		TimerID:   timerID,
		Kind:      TimerCompensationStall,
		NodeID:    nodeID,
		ScopeID:   scopeID,
		CommandID: commandID,
	})
}

// RecordNodeIDs projects the NodeIDs of s.RootCompensations for engine_test.
func RecordNodeIDs(s *InstanceState) []string {
	out := make([]string, 0, len(s.RootCompensations))
	for _, r := range s.RootCompensations {
		out = append(out, r.NodeID)
	}
	return out
}

// PendingCancelOf exposes s.PendingCancel for engine_test.
func PendingCancelOf(s *InstanceState) bool { return s.PendingCancel }

// ArchiveKeyOf exposes the compensation cursor's ArchiveKey for engine_test.
func ArchiveKeyOf(s *InstanceState) string { return s.Compensating.ArchiveKey }

// WalkModeName names the compensation cursor's walk mode, for engine_test.
func WalkModeName(s *InstanceState) string {
	switch s.Compensating.walkMode() {
	case walkAdmin:
		return "walkAdmin"
	case walkThrowTargeted:
		return "walkThrowTargeted"
	case walkThrowScopeWide:
		return "walkThrowScopeWide"
	case walkPartial:
		return "walkPartial"
	case walkReverse:
		return "walkReverse"
	default:
		return "unknown"
	}
}

// CompensatingSince exposes the compensation cursor's StartedAt for engine_test.
func CompensatingSince(s *InstanceState) time.Time { return s.Compensating.StartedAt }

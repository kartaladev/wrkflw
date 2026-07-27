package engine

import "time"

// CloseKind names why a [NodeVisit] closed ABNORMALLY — the token left the node
// for a reason other than completing it and advancing (ADR-0145).
//
// A normal advance leaves [NodeVisit.CloseKind] EMPTY (the zero value). That is
// the load-bearing half of the contract: gateway forks and joins, sub-process
// entry, end-event consumption, and every ordinary flow traversal all close
// visits, and none of them is an anomaly. A consumer can therefore treat any
// non-empty close kind as "something interrupted this node", with the value
// saying what.
//
// It is a defined type rather than a bare string so the compiler rejects an
// undeclared reason: the field is a discriminator consumers switch on, and
// `v.CloseKind = "cancelled"` should not compile. This matches the sibling
// discriminators in this package ([TokenState], [Status]).
type CloseKind string

// String returns the wire value of the close kind, so it reads correctly in logs
// and format verbs. The zero value renders as the empty string, which is exactly
// what a normal close serializes to.
func (k CloseKind) String() string { return string(k) }

const (
	// CloseKindInstanceCancelled — an administrative instance cancel
	// (CancelRequested) tore the token down, with or without a compensation walk.
	CloseKindInstanceCancelled CloseKind = "instance_cancelled"
	// CloseKindTerminated — a terminating end event (WithForceTermination) ended
	// the whole instance, sweeping this still-open visit closed.
	CloseKindTerminated CloseKind = "terminated"
	// CloseKindBoundaryInterrupted — an interrupting boundary event (timer,
	// signal, message, or a matched error boundary) fired on this activity and
	// consumed its token, or an interrupting event sub-process cancelled the
	// enclosing scope this token belonged to.
	CloseKindBoundaryInterrupted CloseKind = "boundary_interrupted"
	// CloseKindErrored — the visit threw: an error end event, or a failing
	// activity whose error nothing caught.
	CloseKindErrored CloseKind = "errored"
	// CloseKindCompensated — the token was torn down to run a compensation walk
	// (an administrative CompensateRequested with no rollback target).
	CloseKindCompensated CloseKind = "compensated"
	// CloseKindReversed — the token was torn down by a ReverseInstance rollback
	// (full reverse to the start, or partial rollback to a target node).
	CloseKindReversed CloseKind = "reversed"
	// CloseKindDeadlineExpired — a human task's deadline breached and the engine
	// rerouted the token onto the node's alternative (deadline) flow.
	CloseKindDeadlineExpired CloseKind = "deadline_expired"
)

// closeVisitAs closes the open visit for (tokenID, nodeID) and stamps why it
// closed. An empty kind means a normal close and leaves CloseKind unset.
func (s *InstanceState) closeVisitAs(tokenID, nodeID string, at time.Time, kind CloseKind) {
	if v := s.openVisitFor(tokenID, nodeID); v != nil {
		left := at
		v.LeftAt = &left
		v.CloseKind = kind
	}
}

// consumeTokenAs removes tok from the token set and closes its visit with the
// given abnormal close reason. consumeToken is the normal-close form.
func (s *InstanceState) consumeTokenAs(tok *Token, at time.Time, kind CloseKind) {
	s.closeVisitAs(tok.ID, tok.NodeID, at, kind)
	s.removeToken(tok.ID)
}

package scheduling

import (
	"context"
)

// backendPIDer is implemented by the Postgres backend elector so heartbeat tests
// can sever its dedicated connection out-of-band (ADR-0061).
type backendPIDer interface {
	BackendPID() uint32
}

// ElectorBackendPID exposes the Postgres backend PID of the scheduler's leader
// elector connection so heartbeat tests can sever it out-of-band (ADR-0061). It
// returns 0 when the scheduler is not in single-leader mode or the elector does
// not expose a backend PID. Test-only.
func ElectorBackendPID(s *Scheduler) uint32 {
	if s.elector == nil {
		return 0
	}
	if p, ok := s.elector.(backendPIDer); ok {
		return p.BackendPID()
	}
	return 0
}

// SchedulerIsLeader reports whether the scheduler's leader elector currently holds
// leadership. It returns false when the scheduler is not in single-leader mode.
// Test-only.
func SchedulerIsLeader(ctx context.Context, s *Scheduler) bool {
	if s.elector == nil {
		return false
	}
	return s.elector.IsLeader(ctx) == nil
}

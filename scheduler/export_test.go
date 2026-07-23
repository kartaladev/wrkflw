package scheduler

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
func ElectorBackendPID(s *NativeScheduler) uint32 {
	if s.cfg.elector == nil {
		return 0
	}
	if p, ok := s.cfg.elector.(backendPIDer); ok {
		return p.BackendPID()
	}
	return 0
}

// SchedulerIsLeader reports whether the scheduler's leader elector currently holds
// leadership. It returns false when the scheduler is not in single-leader mode.
// Test-only.
func SchedulerIsLeader(ctx context.Context, s *NativeScheduler) bool {
	if s.cfg.elector == nil {
		return false
	}
	return s.cfg.elector.IsLeader(ctx) == nil
}

// JobIsSingleton exposes a [Job]'s unexported singleton() flag for tests —
// the same private interface assertion the in-package façade (Tasks 5-11)
// uses to read it. It returns false for a Job that doesn't implement the
// assertion (e.g. a bespoke consumer-supplied Job). Test-only.
func JobIsSingleton(j Job) bool {
	s, ok := j.(interface{ singleton() bool })
	if !ok {
		return false
	}
	return s.singleton()
}

// JobStores exposes the scheduler's kind-routed JobStore provider map
// recorded via [WithJobStore], for tests. Test-only.
func JobStores(s *NativeScheduler) map[JobKind]func() JobStore {
	return s.cfg.jobStores
}

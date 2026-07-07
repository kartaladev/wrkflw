package kernel

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
	"github.com/zakyalvan/krtlwrkflw/engine"
)

// ArmedTimer is one timer currently armed (scheduled, not yet fired or cancelled).
// DefID/DefVersion are stored so RehydrateTimers can resolve the process
// definition via the registry without loading instance state per timer.
//
// Trigger is the resolved [schedule.TriggerSpec] the timer was armed with; it is
// authoritative for re-arm at rehydration and for deciding whether a fired timer
// is recurring (and therefore survives its fire). NextRun is the next scheduled
// run time as computed by the scheduler at arm time. Durable persistence of the
// Trigger descriptor lands in Plan 3; today the fields exist and travel through
// the in-memory store.
type ArmedTimer struct {
	InstanceID string
	DefID      string
	DefVersion int
	TimerID    string
	Trigger    schedule.TriggerSpec
	NextRun    time.Time
	Kind       engine.TimerKind
}

// TimerStore is the read-side port for enumerating armed timers at startup. The
// write side is fused into the transactional Store (AppliedStep.TimerArms /
// TimerCancels), atomically with the state commit — see ADR-0027.
type TimerStore interface {
	// ListArmed returns all timers currently armed, ordered by
	// (NextRun, InstanceID, TimerID) for deterministic re-arm order.
	ListArmed(ctx context.Context) ([]ArmedTimer, error)
}

// MemTimerStore is the in-memory reference TimerStore. It is both the write
// target (MemInstanceStore records arms/cancels into it) and the read source.
type MemTimerStore struct {
	mu    sync.Mutex
	armed map[timerKey]ArmedTimer
}

type timerKey struct{ instanceID, timerID string }

// NewMemTimerStore constructs an empty in-memory TimerStore.
func NewMemTimerStore() *MemTimerStore {
	return &MemTimerStore{armed: make(map[timerKey]ArmedTimer)}
}

// Arm records (or upserts) an armed timer.
func (s *MemTimerStore) Arm(t ArmedTimer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.armed[timerKey{t.InstanceID, t.TimerID}] = t
}

// Cancel removes an armed timer; a no-op if absent.
func (s *MemTimerStore) Cancel(instanceID, timerID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.armed, timerKey{instanceID, timerID})
}

// ListArmed implements TimerStore.
func (s *MemTimerStore) ListArmed(_ context.Context) ([]ArmedTimer, error) {
	s.mu.Lock()
	out := make([]ArmedTimer, 0, len(s.armed))
	for _, t := range s.armed {
		out = append(out, t)
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if !out[i].NextRun.Equal(out[j].NextRun) {
			return out[i].NextRun.Before(out[j].NextRun)
		}
		if out[i].InstanceID != out[j].InstanceID {
			return out[i].InstanceID < out[j].InstanceID
		}
		return out[i].TimerID < out[j].TimerID
	})
	return out, nil
}

var _ TimerStore = (*MemTimerStore)(nil)

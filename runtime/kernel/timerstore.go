package kernel

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"time"

	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
)

// ArmedTimer is one timer currently armed (scheduled, not yet fired or cancelled).
// DefID/DefVersion are stored so RehydrateTimers can resolve the process
// definition via the registry without loading instance state per timer.
//
// Trigger is the resolved [schedule.TriggerSpec] the timer was armed with; it is
// authoritative for re-arm at rehydration and for deciding whether a fired timer
// is recurring (and therefore survives its fire). NextRun is the next scheduled
// run time as computed by the scheduler at arm time. Durable persistence of the
// Trigger descriptor is not implemented yet; today the fields exist and travel
// through the in-memory store.
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
// write side is the standalone [TimerWriter] capability, persisted
// atomically with the state commit via the runtime's own JobStore.
type TimerStore interface {
	// ListArmed returns all timers currently armed, ordered by
	// (NextRun, InstanceID, TimerID) for deterministic re-arm order.
	//
	// Implementations MUST serve this as a single read so callers see one
	// consistent snapshot: startup rehydration re-arms from the whole set while
	// live traffic may be arming and cancelling timers concurrently.
	ListArmed(ctx context.Context) ([]ArmedTimer, error)

	// ArmedTimer returns the timer armed for the exact (instanceID, timerID)
	// pair. It exists so a caller needing one timer does not read the whole
	// armed set — the timer-fire hot path asks only whether the timer that just
	// fired is recurring.
	//
	// found is false when no such timer is armed; that is not an error. err is
	// reserved for genuine infrastructure failures, and callers MUST distinguish
	// it from found == false: an error means "recurrence is undeterminable", not
	// "the timer is one-shot".
	//
	// Invariant: ArmedTimer(i, t) returns exactly the row ListArmed would return
	// for (i, t), WHEN THAT ROW ITSELF IS WELL-FORMED. The qualifier is
	// deliberate — ListArmed fails wholesale if any row in the table is corrupt,
	// while ArmedTimer is unaffected by a corrupt sibling.
	ArmedTimer(ctx context.Context, instanceID, timerID string) (t ArmedTimer, found bool, err error)
}

// TimerWriter is the write-side capability a TimerStore MAY implement. It is
// type-asserted off the store supplied via WithTimerStore. Writes join an
// ambient ctx-transaction (JoinOrBegin) so the runtime JobStore can persist
// atomically with the state commit.
//
// DeleteJobByTimerID removes a job by TimerID alone, without an InstanceID —
// engine timer ids are globally unique (`<instanceID>-tm<seq>`), so a bare
// TimerID lookup is unambiguous. It exists for the runtime JobStore's
// Delete(id), which only carries the timer id.
type TimerWriter interface {
	// UpsertJob persists (or updates) the durable descriptor for spec's timer.
	UpsertJob(ctx context.Context, spec JobSpec) error
	// DeleteJob removes the durable descriptor for (instanceID, timerID).
	// A no-op (nil error) if no such row exists.
	DeleteJob(ctx context.Context, instanceID, timerID string) error
	// DeleteJobByTimerID removes the durable descriptor for timerID alone.
	// A no-op (nil error) if no such row exists.
	DeleteJobByTimerID(ctx context.Context, timerID string) error
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

// ArmedTimer implements TimerStore. The armed set is already keyed by
// (instanceID, timerID), so this is a single map lookup — no copy and no sort,
// which is the whole point on the timer-fire hot path.
func (s *MemTimerStore) ArmedTimer(_ context.Context, instanceID, timerID string) (ArmedTimer, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.armed[timerKey{instanceID, timerID}]
	return t, ok, nil
}

// ListArmed implements TimerStore.
func (s *MemTimerStore) ListArmed(_ context.Context) ([]ArmedTimer, error) {
	s.mu.Lock()
	out := make([]ArmedTimer, 0, len(s.armed))
	for _, t := range s.armed {
		out = append(out, t)
	}
	s.mu.Unlock()
	slices.SortFunc(out, func(a, b ArmedTimer) int {
		return cmp.Or(
			a.NextRun.Compare(b.NextRun),
			cmp.Compare(a.InstanceID, b.InstanceID),
			cmp.Compare(a.TimerID, b.TimerID),
		)
	})
	return out, nil
}

var _ TimerStore = (*MemTimerStore)(nil)
var _ TimerWriter = (*MemTimerStore)(nil)

// UpsertJob implements TimerWriter by arming (or re-arming) the descriptor
// built from spec.
func (s *MemTimerStore) UpsertJob(_ context.Context, spec JobSpec) error {
	s.Arm(ArmedTimer{
		InstanceID: spec.InstanceID,
		DefID:      spec.DefID,
		DefVersion: spec.DefVersion,
		TimerID:    spec.TimerID,
		Trigger:    spec.Trigger,
		NextRun:    spec.NextRun,
		Kind:       spec.Kind,
	})
	return nil
}

// DeleteJob implements TimerWriter by cancelling the (instanceID, timerID) entry.
func (s *MemTimerStore) DeleteJob(_ context.Context, instanceID, timerID string) error {
	s.Cancel(instanceID, timerID)
	return nil
}

// DeleteJobByTimerID implements TimerWriter by scanning for and removing the
// entry matching timerID alone, regardless of InstanceID.
func (s *MemTimerStore) DeleteJobByTimerID(_ context.Context, timerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.armed {
		if k.timerID == timerID {
			delete(s.armed, k)
		}
	}
	return nil
}

// JobSpec is the typed descriptor of one durable scheduled timer job.
type JobSpec struct {
	TimerID    string
	InstanceID string
	DefID      string
	DefVersion int
	// Trigger is the TriggerSpec to (re)register the job with. For a non-recurring
	// timer with a persisted NextRun it is schedule.At(NextRun) (faithful original
	// fire instant); otherwise it is the stored recurring Trigger.
	Trigger schedule.TriggerSpec
	NextRun time.Time
	// Kind discriminates the purpose of the timer (intermediate/deadline/
	// in-wait/retry — see [engine.TimerKind]), mirroring [ArmedTimer.Kind].
	// Zero-value (engine.TimerIntermediate) is compatible with all pre-existing
	// JobSpec literals that don't set it.
	Kind engine.TimerKind
}

package runtime

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/scheduler"
)

// jobStore is the runtime's [scheduler.JobStore] for timerJobKind: Load
// rebuilds executable Manual timer jobs from the durable TimerStore, resolving
// each timer's definition via the registry and rebuilding its fire callback
// via the shared timerFireFunc. Save and Delete give the scheduler a real
// durable write path (ADR-0134 B1), routed through the driver's TimerWriter
// capability (nil when no durable TimerStore is configured).
type jobStore struct {
	driver *ProcessDriver
}

// newJobStore is the package-internal constructor, held by the driver as a
// concrete field (driver.jobStore) so other runtime call sites can reach the
// unexported deleteTimer helper directly. NewJobStore is the public wrapper.
func newJobStore(driver *ProcessDriver) *jobStore { return &jobStore{driver: driver} }

// NewJobStore returns the [scheduler.JobStore] for the runtime's timer job
// kind ("wrkflw.timer"), backed by driver's TimerStore and definition
// registry. Register it (as a thunk) on the scheduler via
// [scheduler.WithJobStore] so the scheduler can self-rehydrate armed timers on
// start, re-arming each with a faithful fire time via rehydrateTrigger.
//
// Load returns (nil, nil) only when no TimerStore is configured on the
// driver. When some armed timers reference process definitions not present in
// the registry, Load returns the resolvable jobs plus an error wrapping
// [scheduler.ErrUnresolvedTimerDefinitions]. The scheduler's automatic
// self-rehydration treats this error as non-fatal and logs it at WARN; callers
// requiring strict resolution (e.g. [ProcessDriver.RehydrateTimers]) propagate
// it.
//
// A genuine infrastructure failure (e.g. a ListArmed DB error) is returned as
// a plain non-sentinel error and is always fatal.
//
// Save persists j's typed descriptor via the driver's TimerWriter (recovered
// by type-asserting j to the runtime's own descriptor-bearing job shape);
// Delete removes the durable row identified by timer id alone. Both are
// documented no-ops when the driver has no TimerWriter (no durable TimerStore
// configured) — durability is simply unavailable, not an error.
func NewJobStore(driver *ProcessDriver) scheduler.JobStore { return newJobStore(driver) }

func (j *jobStore) Load(ctx context.Context) ([]scheduler.ScheduledJob, error) {
	// defsReg is always non-nil (defaults to the process-global defaultDefinitionRegistry),
	// so only timerStore needs to be checked.
	if j.driver.timerStore == nil {
		return nil, nil
	}
	armed, err := j.driver.timerStore.ListArmed(ctx)
	if err != nil {
		return nil, fmt.Errorf("workflow-runtime: load scheduled timers: list armed: %w", err)
	}
	jobs := make([]scheduler.ScheduledJob, 0, len(armed))
	var unresolved int
	for _, a := range armed {
		defQ := model.Version(a.DefID, a.DefVersion)
		def, err := j.driver.defsReg.Lookup(ctx, defQ)
		if err != nil {
			unresolved++
			j.driver.obs.tel.Logger.LogAttrs(ctx, slog.LevelWarn, "runtime: load scheduled timers: definition not found, skipping timer",
				append(j.driver.obs.tel.LogAttrs(ctx),
					slog.String("def_ref", defQ.String()),
					slog.String("timer_id", a.TimerID),
					slog.String("instance_id", a.InstanceID))...)
			continue
		}
		sj, err := j.driver.buildTimerJob(def, a.InstanceID, a.TimerID, rehydrateTrigger(a), a.NextRun, a.Kind)
		if err != nil {
			// A persisted trigger is always a concrete, convertible kind; an
			// unconvertible row is corrupt — skip it, never abort the batch.
			j.driver.obs.tel.Logger.LogAttrs(ctx, slog.LevelWarn, "runtime: load scheduled timers: trigger not convertible, skipping timer",
				append(j.driver.obs.tel.LogAttrs(ctx),
					slog.String("timer_id", a.TimerID),
					slog.String("instance_id", a.InstanceID),
					slog.Any("error", err))...)
			continue
		}
		jobs = append(jobs, sj)
	}
	if unresolved > 0 {
		return jobs, fmt.Errorf("workflow-runtime: load scheduled timers: %d timer(s) skipped (definition not found): %w",
			unresolved, scheduler.ErrUnresolvedTimerDefinitions)
	}
	return jobs, nil
}

// timerJobDescriptor is satisfied by the runtime's own timerJob (and, via
// embedding, scheduledTimerJob) — it is deliberately unexported so only code
// in package runtime can spell the assertion, mirroring how the scheduler
// package itself gates its own private capability checks (see job.go's
// singleton()). Save type-asserts an incoming scheduler.ScheduledJob to this
// shape to recover the typed kernel.JobSpec it was built from; a consumer- or
// scheduler-package-implemented Job never satisfies it.
type timerJobDescriptor interface {
	descriptor() kernel.JobSpec
}

// Save persists sj's typed descriptor via the driver's TimerWriter
// (ADR-0134 B1). It requires sj to be the runtime's own timer job shape
// (recovered via the timerJobDescriptor type-assertion) AND to report
// timerJobKind — the only kind this store is ever registered for
// (startTimerJobKind timer-starts are deliberately non-durable and never
// reach here in practice; the Kind check is a defensive belt-and-braces
// guard). Any other job — a foreign scheduler.Job implementation, or one
// that reports a different Kind — is rejected with a typed error rather than
// silently ignored or miswritten.
//
// A nil TimerWriter (no durable TimerStore configured) is a documented
// no-op: durability is simply unavailable, not an error.
func (j *jobStore) Save(ctx context.Context, sj scheduler.ScheduledJob) error {
	if j.driver.timerWriter == nil {
		return nil
	}
	td, ok := sj.(timerJobDescriptor)
	if !ok || sj.Kind() != timerJobKind {
		return fmt.Errorf("workflow-runtime: job store: unexpected job implementation %T", sj)
	}
	return j.driver.timerWriter.UpsertJob(ctx, td.descriptor())
}

// Delete removes the durable timer row identified by id ALONE, via
// [kernel.TimerWriter.DeleteJobByTimerID]. Engine timer ids are globally
// unique (`<instanceID>-tm<seq>`), so a bare id lookup is unambiguous — no
// composite-id parsing is involved. A nil TimerWriter is a documented no-op.
func (j *jobStore) Delete(ctx context.Context, id string) error {
	if j.driver.timerWriter == nil {
		return nil
	}
	return j.driver.timerWriter.DeleteJobByTimerID(ctx, id)
}

// deleteTimer is the runtime-internal, PK-exact counterpart to Delete: it
// removes the durable row for the exact (instanceID, timerID) pair via
// [kernel.TimerWriter.DeleteJob]. It exists for call sites that already carry
// both parts — e.g. the Drive cancel path — where a bare-id delete would be
// needlessly ambiguous-looking even though ids are globally unique. A nil
// TimerWriter is a documented no-op.
func (j *jobStore) deleteTimer(ctx context.Context, instanceID, timerID string) error {
	if j.driver.timerWriter == nil {
		return nil
	}
	return j.driver.timerWriter.DeleteJob(ctx, instanceID, timerID)
}

package runtime

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// jobStore is the runtime JobStore: it rebuilds executable ScheduledJobs from the
// durable TimerStore, resolving each timer's definition via the registry and
// rebuilding its Fire via the shared timerFireFunc.
type jobStore struct {
	driver *ProcessDriver
}

// NewJobStore returns a kernel.JobStore backed by driver's TimerStore and
// definition registry. Pass it (as a provider) to the scheduler so it can
// self-rehydrate armed timers on start, re-registering each with a faithful
// fire time via rehydrateTrigger.
//
// LoadScheduled returns (nil, nil) when no TimerStore or definition registry
// is configured on the driver — there are no durable timers to rehydrate.
func NewJobStore(driver *ProcessDriver) kernel.JobStore { return &jobStore{driver: driver} }

func (j *jobStore) LoadScheduled(ctx context.Context) ([]kernel.ScheduledJob, error) {
	if j.driver.timerStore == nil || j.driver.defsReg == nil {
		return nil, nil
	}
	armed, err := j.driver.timerStore.ListArmed(ctx)
	if err != nil {
		return nil, fmt.Errorf("workflow-runtime: LoadScheduled: list armed: %w", err)
	}
	jobs := make([]kernel.ScheduledJob, 0, len(armed))
	var unresolved int
	for _, a := range armed {
		defQ := model.Version(a.DefID, a.DefVersion)
		def, err := j.driver.defsReg.Lookup(ctx, defQ)
		if err != nil {
			unresolved++
			j.driver.obs.tel.Logger.LogAttrs(ctx, slog.LevelError, "runtime: LoadScheduled: definition not found, skipping timer",
				append(j.driver.obs.tel.LogAttrs(ctx),
					slog.String("def_ref", defQ.String()),
					slog.String("timer_id", a.TimerID),
					slog.String("instance_id", a.InstanceID))...)
			continue
		}
		jobs = append(jobs, kernel.ScheduledJob{
			Spec: kernel.JobSpec{
				TimerID:    a.TimerID,
				InstanceID: a.InstanceID,
				DefID:      a.DefID,
				DefVersion: a.DefVersion,
				Trigger:    rehydrateTrigger(a),
				NextRun:    a.NextRun,
			},
			Fire: j.driver.timerFireFunc(def, a.InstanceID, a.TimerID),
		})
	}
	if unresolved > 0 {
		return jobs, fmt.Errorf("workflow-runtime: LoadScheduled: %d timer(s) skipped (definition not found)", unresolved)
	}
	return jobs, nil
}

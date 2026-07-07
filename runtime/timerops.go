package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/zakyalvan/krtlwrkflw/definition/model"
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// timerOpsFor derives the armed-timer side-effects of one applied step from its
// commands and trigger. ScheduleTimer commands become arms carrying their
// resolved [schedule.TriggerSpec]; CancelTimer commands become cancels.
//
// A TimerFired trigger normally consumes (cancels) the fired timer — EXCEPT when
// the fired timer's armed trigger is recurring: a recurring native job keeps
// firing on its own schedule and never self-disarms, so it must NOT be cancelled
// on each fire. armedRecurring reports whether the timer with the given id is
// currently armed with a recurring trigger; when it reports false (unknown timer,
// or a genuinely one-shot timer) the fired timer is cancelled, preserving the
// pre-recurrence safe default. An explicit CancelTimer command always cancels,
// recurring or not — that is how a scope-exit / instance-terminate stops a
// recurring native job. Pure; kind-agnostic so it covers every timer kind.
func timerOpsFor(cmds []engine.Command, trg engine.Trigger, defID string, defVersion int, instanceID string, armedRecurring func(timerID string) bool) ([]kernel.ArmedTimer, []string) {
	var arms []kernel.ArmedTimer
	var cancels []string
	for _, c := range cmds {
		switch cmd := c.(type) {
		case engine.ScheduleTimer:
			arms = append(arms, kernel.ArmedTimer{
				InstanceID: instanceID,
				DefID:      defID,
				DefVersion: defVersion,
				TimerID:    cmd.TimerID,
				Trigger:    cmd.Trigger,
				// NextRun is populated by the scheduler; durable descriptor
				// persistence (and a faithful NextRun) lands in Plan 3.
				Kind: cmd.Kind,
			})
		case engine.CancelTimer:
			cancels = append(cancels, cmd.TimerID)
		}
	}
	if tf, ok := trg.(engine.TimerFired); ok {
		// A recurring timer survives its fire (the scheduler re-arms it natively);
		// only consume one-shot (or unknown) timers.
		if armedRecurring == nil || !armedRecurring(tf.TimerID) {
			cancels = append(cancels, tf.TimerID)
		}
	}
	return arms, cancels
}

// armedTimerRecurring reports whether the timer (instanceID, timerID) is
// currently armed with a recurring trigger. It reads the armed set from the
// timer store; on any error, when the store is absent, or when the timer is not
// found it returns false — the safe default that consumes a fired timer (today's
// behaviour before recurrence-aware cancel). It is invoked only for a TimerFired
// trigger, so the ListArmed read stays off the hot path of non-timer steps.
func (r *ProcessDriver) armedTimerRecurring(ctx context.Context, instanceID, timerID string) bool {
	if r.timerStore == nil {
		return false
	}
	armed, err := r.timerStore.ListArmed(ctx)
	if err != nil {
		r.obs.tel.Logger.LogAttrs(ctx, slog.LevelWarn, "runtime: recurrence lookup: list armed failed, treating as non-recurring",
			append(r.obs.tel.LogAttrs(ctx),
				slog.String("timer_id", timerID),
				slog.String("instance_id", instanceID),
				slog.Any("error", err))...)
		return false
	}
	for _, a := range armed {
		if a.InstanceID == instanceID && a.TimerID == timerID {
			return a.Trigger.Recurring()
		}
	}
	return false
}

// armTimer registers timerID on the scheduler from its resolved
// [schedule.TriggerSpec], with the engine's standard fire callback: deliver a
// TimerFired trigger, retrying on optimistic-CAS conflicts. Used by
// perform(ScheduleTimer) and RehydrateTimers.
//
// An unschedulable trigger (e.g. kernel.ErrUnsupportedTrigger from an in-memory
// scheduler asked to run a cron trigger, or a gocron mapping error) is logged at
// WARN and skipped — it must never crash the driver or the in-flight instance.
func (r *ProcessDriver) armTimer(ctx context.Context, def *model.ProcessDefinition, instanceID, timerID string, trig schedule.TriggerSpec) {
	nextRun, err := r.sched.Schedule(ctx, timerID, trig, func() {
		// This callback runs from the scheduler's goroutine (or Tick caller).
		// Use a background context: the originating request context may have
		// been cancelled by the time the timer fires.
		fireCtx := context.Background()
		trg := engine.NewTimerFired(r.clk.Now(), timerID)
		r.obs.timerFired.Add(fireCtx, 1)
		const maxAttempts = 5
		var err error
		for range maxAttempts {
			if _, err = r.Deliver(fireCtx, def, instanceID, trg); err == nil {
				return
			}
			if !errors.Is(err, kernel.ErrConcurrentUpdate) {
				r.obs.tel.Logger.LogAttrs(fireCtx, slog.LevelError, "runtime: timer fire: Deliver failed",
					append(r.obs.tel.LogAttrs(fireCtx),
						slog.String("timer_id", timerID),
						slog.String("instance_id", instanceID),
						slog.Any("error", err))...)
				return
			}
			// ErrConcurrentUpdate: another Deliver won the CAS; Deliver
			// internally reloads fresh state on the next call. Retry
			// immediately (no sleep needed — store reloads on each Deliver).
		}
		r.obs.tel.Logger.LogAttrs(fireCtx, slog.LevelError, "runtime: timer fire: Deliver permanently dropped after CAS conflicts",
			append(r.obs.tel.LogAttrs(fireCtx),
				slog.String("timer_id", timerID),
				slog.String("instance_id", instanceID),
				slog.Int("attempts", maxAttempts),
				slog.Any("error", err))...)
	})
	if err != nil {
		// The trigger could not be scheduled (unsupported kind or a mapping
		// error). Skip it — an unschedulable timer must never crash the driver.
		// (Durable descriptor persistence + NextRun recording is Plan 3.)
		r.obs.tel.Logger.LogAttrs(ctx, slog.LevelWarn, "runtime: armTimer: trigger not schedulable, skipping timer",
			append(r.obs.tel.LogAttrs(ctx),
				slog.String("timer_id", timerID),
				slog.String("instance_id", instanceID),
				slog.Any("error", err))...)
		return
	}
	r.obs.tel.Logger.LogAttrs(ctx, slog.LevelDebug, "runtime: armTimer: scheduled",
		append(r.obs.tel.LogAttrs(ctx),
			slog.String("timer_id", timerID),
			slog.String("instance_id", instanceID),
			slog.Time("next_run", nextRun))...)
}

// RehydrateTimers re-arms every persisted armed timer on the scheduler from its
// stored [schedule.TriggerSpec]. Call it once at startup, after constructing the
// ProcessDriver, to recover timers lost when the process restarted. Requires
// WithScheduler, WithTimerStore, and WithDefinitions. Each timer is re-armed via
// its Trigger, so recurring timers resume their native recurrence; a re-fire of an
// already-consumed one-shot timer is an idempotent engine no-op. Note: for an
// AfterDuration one-shot this restarts the delay from now rather than firing at
// the original absolute instant — an accepted Plan-2 gap; NextRun-faithful
// rehydration lands in Plan 3. Timers whose definition the registry cannot
// resolve are skipped and counted in the returned error.
func (r *ProcessDriver) RehydrateTimers(ctx context.Context) error {
	if r.sched == nil || r.timerStore == nil || r.defsReg == nil {
		return fmt.Errorf("workflow-runtime: RehydrateTimers requires WithScheduler, WithTimerStore, and WithDefinitions")
	}
	armed, err := r.timerStore.ListArmed(ctx)
	if err != nil {
		return fmt.Errorf("workflow-runtime: RehydrateTimers: list armed: %w", err)
	}
	var unresolved int
	for _, a := range armed {
		defQ := model.Version(a.DefID, a.DefVersion)
		def, err := r.defsReg.Lookup(ctx, defQ)
		if err != nil {
			unresolved++
			r.obs.tel.Logger.LogAttrs(ctx, slog.LevelError, "runtime: rehydrate: definition not found, skipping timer",
				append(r.obs.tel.LogAttrs(ctx),
					slog.String("def_ref", defQ.String()),
					slog.String("timer_id", a.TimerID),
					slog.String("instance_id", a.InstanceID))...)
			continue
		}
		r.armTimer(ctx, def, a.InstanceID, a.TimerID, a.Trigger)
	}
	if unresolved > 0 {
		return fmt.Errorf("workflow-runtime: RehydrateTimers: %d timer(s) skipped (definition not found)", unresolved)
	}
	return nil
}

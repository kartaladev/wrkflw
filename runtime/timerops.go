package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

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
func timerOpsFor(cmds []engine.Command, trg engine.Trigger, defID string, defVersion int, instanceID string, now time.Time, armedRecurring func(timerID string) bool) ([]kernel.ArmedTimer, []string) {
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
				// NextRun is the persisted authoritative next-run instant so a
				// SQL-backed one-shot re-arms at its original absolute time after
				// a restart (rather than restarting its delay from "now"). It is
				// computed synchronously here — in the same tx as the timer row —
				// so it is crash-safe. armTimer refines it post-Schedule with the
				// scheduler's own next-run (interim until the Plan-3 JobStore owns
				// the arm/persist lifecycle under one ambient tx).
				NextRun: nextRunFor(cmd.Trigger, now),
				Kind:    cmd.Kind,
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

// nextRunFor computes the absolute next-run instant to persist for a timer arm,
// in UTC, synchronously and in the state-commit transaction (crash-safe):
//
//   - At one-shot → the trigger's absolute time.
//   - AfterDuration one-shot → now + duration, so a restart re-arms at the
//     ORIGINAL instant (not restart + duration). RehydrateTimers re-arms it via
//     schedule.At(NextRun).
//   - Every (fixed-interval recurring) → now + interval, a truthful first-fire
//     instant so the persisted next_run keeps timer Stats (MIN(next_run))
//     meaningful. Rehydration still re-arms it from its Trigger.
//
// It returns the zero time for triggers whose next occurrence cannot be computed
// without the scheduler (cron, calendar). Those keep next_run zero and are
// rehydrated purely from their persisted Trigger; recording their true next-run
// is deferred to the Plan-3 scheduler-owned lifecycle (interim gap). Engine-
// resolved Expr forms are resolved to concrete one-shot/interval triggers before
// reaching here, so they take the branches above.
func nextRunFor(trig schedule.TriggerSpec, now time.Time) time.Time {
	if at, ok := trig.AbsTime(); ok {
		return at.UTC()
	}
	if d, ok := trig.Duration(); ok {
		// Covers both AfterDuration (one-shot) and Every (recurring interval).
		return now.UTC().Add(d)
	}
	return time.Time{}
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

// RehydrateTimers re-arms every persisted armed timer on the scheduler. Call it
// once at startup, after constructing the ProcessDriver, to recover timers lost
// when the process restarted. Requires WithScheduler, WithTimerStore, and
// WithDefinitions.
//
// Re-arm is faithful to the original fire time:
//
//   - A NON-recurring timer with a valid persisted NextRun is re-armed via
//     schedule.At(NextRun), so it fires at its ORIGINAL absolute instant. This
//     correctly handles an AfterDuration one-shot, which would otherwise restart
//     its delay from "now" (the Plan-2 rehydration regression this closes). A
//     re-fire of an already-consumed one-shot is an idempotent engine no-op.
//   - A RECURRING timer is re-armed via its stored Trigger, so the scheduler
//     recomputes the next occurrence natively.
//   - A non-recurring timer whose NextRun was not persisted (e.g. an
//     engine-resolved dynamic trigger, or a row written before this column
//     existed) falls back to re-arming from its Trigger.
//
// Timers whose definition the registry cannot resolve are skipped and counted in
// the returned error.
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
		r.armTimer(ctx, def, a.InstanceID, a.TimerID, rehydrateTrigger(a))
	}
	if unresolved > 0 {
		return fmt.Errorf("workflow-runtime: RehydrateTimers: %d timer(s) skipped (definition not found)", unresolved)
	}
	return nil
}

// rehydrateTrigger picks the TriggerSpec to re-arm a persisted timer with. A
// non-recurring timer with a valid persisted NextRun re-arms via
// schedule.At(NextRun) so it fires at its original absolute instant; every other
// case (recurring, or a one-shot with no persisted NextRun) re-arms from the
// stored Trigger.
func rehydrateTrigger(a kernel.ArmedTimer) schedule.TriggerSpec {
	if !a.Trigger.Recurring() && !a.NextRun.IsZero() {
		return schedule.At(a.NextRun)
	}
	return a.Trigger
}

package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// timerOpsFor derives the armed-timer side-effects of one applied step from its
// commands and trigger. ScheduleTimer commands become arms; CancelTimer commands
// and a TimerFired trigger (the fired timer is consumed) become cancels. Pure;
// kind-agnostic so it covers every timer kind uniformly.
func timerOpsFor(cmds []engine.Command, trg engine.Trigger, defID string, defVersion int, instanceID string) ([]kernel.ArmedTimer, []string) {
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
				FireAt:     cmd.FireAt,
				Kind:       cmd.Kind,
			})
		case engine.CancelTimer:
			cancels = append(cancels, cmd.TimerID)
		}
	}
	if tf, ok := trg.(engine.TimerFired); ok {
		cancels = append(cancels, tf.TimerID)
	}
	return arms, cancels
}

// armTimer registers timerID on the scheduler with the engine's standard
// fire callback: deliver a TimerFired trigger, retrying on optimistic-CAS
// conflicts. Used by perform(ScheduleTimer) and RehydrateTimers.
func (r *ProcessDriver) armTimer(def *definition.ProcessDefinition, instanceID, timerID string, fireAt time.Time) {
	r.sched.Schedule(timerID, fireAt, func() {
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
}

// RehydrateTimers re-arms every persisted armed timer on the scheduler. Call it
// once at startup, after constructing the Runner, to recover timers lost when the
// process restarted. Requires WithScheduler, WithTimerStore, and WithDefinitions.
// A timer whose FireAt is already in the past fires immediately; a re-fire of an
// already-consumed timer is an idempotent engine no-op. Timers whose definition
// the registry cannot resolve are skipped and counted in the returned error.
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
		ref := fmt.Sprintf("%s:%d", a.DefID, a.DefVersion)
		def, err := r.defsReg.Lookup(ctx, ref)
		if err != nil {
			unresolved++
			r.obs.tel.Logger.LogAttrs(ctx, slog.LevelError, "runtime: rehydrate: definition not found, skipping timer",
				append(r.obs.tel.LogAttrs(ctx),
					slog.String("def_ref", ref),
					slog.String("timer_id", a.TimerID),
					slog.String("instance_id", a.InstanceID))...)
			continue
		}
		r.armTimer(def, a.InstanceID, a.TimerID, a.FireAt)
	}
	if unresolved > 0 {
		return fmt.Errorf("workflow-runtime: RehydrateTimers: %d timer(s) skipped (definition not found)", unresolved)
	}
	return nil
}

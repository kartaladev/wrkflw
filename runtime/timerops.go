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
				// so it is crash-safe (no out-of-band write-back). Cron/calendar
				// triggers persist a zero NextRun for now and rehydrate from their
				// Trigger; the true scheduler-computed next-run for those is
				// deferred to the Plan-3 JobStore, which will own the arm/persist
				// lifecycle under one ambient tx.
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
func (driver *ProcessDriver) armedTimerRecurring(ctx context.Context, instanceID, timerID string) bool {
	if driver.timerStore == nil {
		return false
	}
	armed, err := driver.timerStore.ListArmed(ctx)
	if err != nil {
		driver.obs.tel.Logger.LogAttrs(ctx, slog.LevelWarn, "runtime: recurrence lookup: list armed failed, treating as non-recurring",
			append(driver.obs.tel.LogAttrs(ctx),
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
func (driver *ProcessDriver) armTimer(ctx context.Context, def *model.ProcessDefinition, instanceID, timerID string, trig schedule.TriggerSpec) {
	nextRun, err := driver.sched.Schedule(ctx, timerID, trig, driver.timerFireFunc(def, instanceID, timerID))
	if err != nil {
		// The trigger could not be scheduled (unsupported kind or a mapping
		// error). Skip it — an unschedulable timer must never crash the driver.
		// (Durable descriptor persistence + NextRun recording is Plan 3.)
		driver.obs.tel.Logger.LogAttrs(ctx, slog.LevelWarn, "runtime: armTimer: trigger not schedulable, skipping timer",
			append(driver.obs.tel.LogAttrs(ctx),
				slog.String("timer_id", timerID),
				slog.String("instance_id", instanceID),
				slog.Any("error", err))...)
		return
	}
	driver.obs.tel.Logger.LogAttrs(ctx, slog.LevelDebug, "runtime: armTimer: scheduled",
		append(driver.obs.tel.LogAttrs(ctx),
			slog.String("timer_id", timerID),
			slog.String("instance_id", instanceID),
			slog.Time("next_run", nextRun))...)
}

// timerFireFunc builds the fire callback for a timer. The callback runs from the
// scheduler's goroutine when the timer becomes due, so it uses a background
// context (the arming request's context may be cancelled by fire time). It
// delivers a TimerFired trigger to the instance via ApplyTrigger, retrying up to
// maxAttempts times on an optimistic-CAS conflict (ErrConcurrentUpdate); any
// other error is logged and dropped. It is shared by armTimer and the JobStore's
// rehydration path so both build byte-identical fire behaviour.
func (driver *ProcessDriver) timerFireFunc(def *model.ProcessDefinition, instanceID, timerID string) func() {
	return func() {
		fireCtx := context.Background()
		trg := engine.NewTimerFired(driver.clk.Now(), timerID)
		driver.obs.timerFired.Add(fireCtx, 1)
		const maxAttempts = 5
		var err error
		for range maxAttempts {
			if _, err = driver.ApplyTrigger(fireCtx, def, instanceID, trg); err == nil {
				return
			}
			if !errors.Is(err, kernel.ErrConcurrentUpdate) {
				driver.obs.tel.Logger.LogAttrs(fireCtx, slog.LevelError, "runtime: timer fire: ApplyTrigger failed",
					append(driver.obs.tel.LogAttrs(fireCtx),
						slog.String("timer_id", timerID),
						slog.String("instance_id", instanceID),
						slog.Any("error", err))...)
				return
			}
		}
		driver.obs.tel.Logger.LogAttrs(fireCtx, slog.LevelError, "runtime: timer fire: ApplyTrigger permanently dropped after CAS conflicts",
			append(driver.obs.tel.LogAttrs(fireCtx),
				slog.String("timer_id", timerID),
				slog.String("instance_id", instanceID),
				slog.Int("attempts", maxAttempts),
				slog.Any("error", err))...)
	}
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
func (driver *ProcessDriver) RehydrateTimers(ctx context.Context) error {
	if driver.sched == nil || driver.timerStore == nil || driver.defsReg == nil {
		return fmt.Errorf("workflow-runtime: RehydrateTimers requires WithScheduler, WithTimerStore, and WithDefinitions")
	}
	armed, err := driver.timerStore.ListArmed(ctx)
	if err != nil {
		return fmt.Errorf("workflow-runtime: RehydrateTimers: list armed: %w", err)
	}
	var unresolved int
	for _, a := range armed {
		defQ := model.Version(a.DefID, a.DefVersion)
		def, err := driver.defsReg.Lookup(ctx, defQ)
		if err != nil {
			unresolved++
			driver.obs.tel.Logger.LogAttrs(ctx, slog.LevelError, "runtime: rehydrate: definition not found, skipping timer",
				append(driver.obs.tel.LogAttrs(ctx),
					slog.String("def_ref", defQ.String()),
					slog.String("timer_id", a.TimerID),
					slog.String("instance_id", a.InstanceID))...)
			continue
		}
		driver.armTimer(ctx, def, a.InstanceID, a.TimerID, rehydrateTrigger(a))
	}
	if unresolved > 0 {
		return fmt.Errorf("workflow-runtime: RehydrateTimers: %d timer(s) skipped (definition not found)", unresolved)
	}
	return nil
}

// startTimerID computes the stable, unique scheduler timer id for a timer-start
// event (ADR-0121): a definition's (id, version) and node nodeID. Including
// version is load-bearing — driver.listDefinitions can return MULTIPLE
// registered versions of the same def id, and a node id like "start" is
// routinely kept stable across a version bump, so a (defID, nodeID)-only key
// would collide across versions and the second Schedule would silently replace
// the first's callback (one version's timer-start would never fire). Each
// registered VERSION arms independently. Stable across process restarts so
// [ProcessDriver.RehydrateStartTimers] re-arming on boot replaces the SAME
// scheduler entry rather than accumulating duplicates.
func startTimerID(defID string, version int, nodeID string) string {
	return fmt.Sprintf("start-timer:%s:%d:%s", defID, version, nodeID)
}

// startTimerFireFunc builds the fire callback for a timer-start event
// (ADR-0121). Unlike timerFireFunc — whose fire delivers a TimerFired trigger to
// an EXISTING instance — a timer-start has no instance yet: each fire CREATES a
// brand-new one, seeded at nodeID via createAtNode with a fresh generated id (so
// a recurring schedule produces one new instance per occurrence, and concurrent
// fires never collide on id).
//
// It uses a background context, mirroring timerFireFunc: the arming request's
// context may be cancelled by the time the timer becomes due. Unlike
// timerFireFunc, no CAS-retry loop is needed — createAtNode always targets a
// fresh instance id, so there is no concurrent writer to conflict with. Any
// error from createAtNode is logged at ERROR and dropped; a failed create must
// never crash the scheduler's fire goroutine.
func (driver *ProcessDriver) startTimerFireFunc(def *model.ProcessDefinition, nodeID, timerID string) func() {
	return func() {
		fireCtx := context.Background()
		driver.obs.timerFired.Add(fireCtx, 1)
		if _, err := driver.createAtNode(fireCtx, def, nodeID, "", nil); err != nil {
			driver.obs.tel.Logger.LogAttrs(fireCtx, slog.LevelError, "runtime: timer-start fire: createAtNode failed",
				append(driver.obs.tel.LogAttrs(fireCtx),
					slog.String("timer_id", timerID),
					slog.String("def_id", def.ID),
					slog.Int("def_version", def.Version),
					slog.String("node_id", nodeID),
					slog.Any("error", err))...)
		}
	}
}

// armStartTimer registers a timer-start event's timerID on the scheduler from
// its resolved [schedule.TriggerSpec], firing startTimerFireFunc on each
// occurrence. An unschedulable trigger is logged at WARN and skipped — it must
// never crash the driver.
func (driver *ProcessDriver) armStartTimer(ctx context.Context, def *model.ProcessDefinition, nodeID, timerID string, trig schedule.TriggerSpec) {
	nextRun, err := driver.sched.Schedule(ctx, timerID, trig, driver.startTimerFireFunc(def, nodeID, timerID))
	if err != nil {
		driver.obs.tel.Logger.LogAttrs(ctx, slog.LevelWarn, "runtime: armStartTimer: trigger not schedulable, skipping timer-start",
			append(driver.obs.tel.LogAttrs(ctx),
				slog.String("timer_id", timerID),
				slog.String("def_id", def.ID),
				slog.Int("def_version", def.Version),
				slog.String("node_id", nodeID),
				slog.Any("error", err))...)
		return
	}
	driver.obs.tel.Logger.LogAttrs(ctx, slog.LevelDebug, "runtime: armStartTimer: scheduled",
		append(driver.obs.tel.LogAttrs(ctx),
			slog.String("timer_id", timerID),
			slog.String("def_id", def.ID),
			slog.Int("def_version", def.Version),
			slog.String("node_id", nodeID),
			slog.Time("next_run", nextRun))...)
}

// RehydrateStartTimers arms every registered definition's timer-start event on
// the scheduler (ADR-0121). Call it once at startup, after constructing the
// ProcessDriver and registering definitions — it is a separate explicit boot
// step, sibling to [ProcessDriver.RehydrateTimers]:
//
//   - RehydrateTimers restores IN-FLIGHT instance timers from the durable timer
//     store (there is an existing instance to resume).
//   - RehydrateStartTimers re-derives its arms purely from registered
//     definitions — a timer-start has no instance yet, so nothing about it is
//     persisted in the timer store; no durable store is required.
//
// Each timer-start is armed under the stable id computed by startTimerID, so
// calling RehydrateStartTimers again (e.g. across restarts) re-arms the SAME
// scheduler entry idempotently rather than accumulating duplicates. Each fire
// creates exactly one new process instance (see startTimerFireFunc); a
// recurring trigger therefore keeps creating a fresh instance on every
// occurrence.
//
// Requires [WithScheduler] and [WithDefinitions].
func (driver *ProcessDriver) RehydrateStartTimers(ctx context.Context) error {
	if driver.sched == nil || driver.defsReg == nil {
		return fmt.Errorf("workflow-runtime: RehydrateStartTimers requires WithScheduler and WithDefinitions")
	}
	for _, hit := range timerStartDefs(driver.listDefinitions(ctx)) {
		driver.armStartTimer(ctx, hit.Def, hit.NodeID, startTimerID(hit.Def.ID, hit.Def.Version, hit.NodeID), hit.Trigger)
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

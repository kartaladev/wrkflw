package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/runtime/kernel"
	"github.com/kartaladev/wrkflw/scheduler"
)

// convertTrigger maps a resolved [schedule.TriggerSpec] to the scheduler's own
// [scheduler.Trigger] vocabulary. Total over all 10 schedule.Kind values:
// KindUnset, KindExpr, and KindEveryExpr are programming errors (the engine
// resolves dynamic expressions to concrete triggers before arming) reported as
// errors wrapping [scheduler.ErrUnsupportedTrigger].
func convertTrigger(t schedule.TriggerSpec) (scheduler.Trigger, error) {
	switch t.Kind() {
	case schedule.KindOneTime:
		if at, ok := t.AbsTime(); ok {
			return scheduler.At(at), nil
		}
		d, _ := t.Duration()
		return scheduler.After(d), nil
	case schedule.KindDuration:
		d, _ := t.Duration()
		return scheduler.Every(d), nil
	case schedule.KindDurationRand:
		minimum, maximum, _ := t.Random()
		return scheduler.EveryRandom(minimum, maximum), nil
	case schedule.KindCron:
		expr, _ := t.CronExpr()
		return scheduler.Cron(expr), nil
	case schedule.KindDaily:
		interval, _, _, at, _ := t.Calendar()
		return scheduler.Daily(interval, convertClockTimes(at)...), nil
	case schedule.KindWeekly:
		interval, _, weekdays, at, _ := t.Calendar()
		return scheduler.Weekly(interval, weekdays, convertClockTimes(at)...), nil
	case schedule.KindMonthly:
		interval, days, _, at, _ := t.Calendar()
		return scheduler.Monthly(interval, days, convertClockTimes(at)...), nil
	default:
		return scheduler.Trigger{}, fmt.Errorf("workflow-runtime: convert trigger: %w: kind %v",
			scheduler.ErrUnsupportedTrigger, t.Kind())
	}
}

// convertClockTimes maps schedule.ClockTime values to the scheduler package's
// identically-shaped ClockTime.
func convertClockTimes(cs []schedule.ClockTime) []scheduler.ClockTime {
	out := make([]scheduler.ClockTime, len(cs))
	for i, c := range cs {
		out[i] = scheduler.ClockTime{Hour: c.Hour, Minute: c.Minute, Second: c.Second}
	}
	return out
}

// cancelKey identifies one durable timer row to delete inside the commit
// transaction — the PK-exact (instanceID, timerID) pair. Cancels carry both
// parts as a struct; no composite string ids are involved (ADR-0134).
type cancelKey struct {
	instanceID string
	timerID    string
}

// timerJobsFor derives the timer side-effects of one applied step from its
// commands and trigger, in executable form: ScheduleTimer commands become
// Manual [timerJob]s ready to Save in-tx and Activate post-commit; CancelTimer
// commands become PK-exact [cancelKey]s. The derivation mirrors the retired
// timerOpsFor exactly; the difference is the output shape.
//
// Each arm's spec.NextRun is the persisted authoritative next-run instant,
// computed as the converted trigger's [scheduler.Trigger.Next] at now and
// UTC-normalised — synchronously, so the value saved in the state-commit
// transaction is crash-safe (no out-of-band write-back). A one-shot therefore
// re-arms at its ORIGINAL absolute instant after a restart, and recurring
// triggers (including cron/calendar, whose next occurrence Next computes
// natively) persist a truthful first-fire instant for timer Stats.
//
// An unconvertible trigger (KindUnset/Expr — programming errors, the engine
// resolves dynamic expressions before arming) is WARN-logged and skipped
// entirely: no scheduler arm and no durable row (a row that cannot convert
// would only be re-skipped as corrupt at rehydration). It must never crash
// the driver or the in-flight instance.
//
// A TimerFired trigger normally consumes (cancels) the fired timer — EXCEPT
// when the fired timer's armed trigger is recurring: a recurring native job
// keeps firing on its own schedule and never self-disarms, so it must NOT be
// cancelled on each fire. armedRecurring reports whether the timer with the
// given id is currently armed with a recurring trigger; when it reports false
// (unknown timer, or a genuinely one-shot timer) the fired timer is consumed,
// preserving the pre-recurrence safe default. A NIL armedRecurring means
// recurrence is undeterminable (no timer store configured): the fired timer is
// left alone — there is no durable row to delete, and disarming a
// possibly-recurring native job would kill it (one-shot native jobs self-
// consume on fire, so nothing leaks). An explicit CancelTimer command always
// cancels, recurring or not — that is how a scope-exit / instance-terminate
// stops a recurring native job. Kind-agnostic so it covers every timer kind;
// the TimerRetry metric is counted at this single derivation site.
func (driver *ProcessDriver) timerJobsFor(ctx context.Context, def *model.ProcessDefinition, cmds []engine.Command, trg engine.Trigger, instanceID string, armedRecurring func(timerID string) bool) ([]*timerJob, []cancelKey) {
	var arms []*timerJob
	var cancels []cancelKey
	now := driver.clk.Now()
	for _, c := range cmds {
		switch cmd := c.(type) {
		case engine.ScheduleTimer:
			if cmd.Kind == engine.TimerRetry {
				driver.obs.actionRetries.Add(ctx, 1)
			}
			strig, err := convertTrigger(cmd.Trigger)
			if err != nil {
				// A skip during shutdown (scheduler closed) is expected, not a lost
				// timer: nothing durable exists for an unconvertible trigger anyway.
				driver.obs.tel.Logger.LogAttrs(ctx, slog.LevelWarn, "runtime: timer arm: trigger not schedulable, skipping timer",
					append(driver.obs.tel.LogAttrs(ctx),
						slog.String("timer_id", cmd.TimerID),
						slog.String("instance_id", instanceID),
						slog.Any("error", err))...)
				continue
			}
			var nextRun time.Time
			if next, ok := strig.Next(now); ok {
				nextRun = next.UTC()
			}
			arms = append(arms, driver.newTimerJob(def, instanceID, cmd.TimerID, cmd.Trigger, strig, nextRun, cmd.Kind))
		case engine.CancelTimer:
			cancels = append(cancels, cancelKey{instanceID: instanceID, timerID: cmd.TimerID})
		}
	}
	if tf, ok := trg.(engine.TimerFired); ok {
		// A recurring timer survives its fire (the scheduler re-arms it natively);
		// only consume one-shot (or unknown) timers, and only when recurrence is
		// determinable at all (armedRecurring non-nil).
		if armedRecurring != nil && !armedRecurring(tf.TimerID) {
			cancels = append(cancels, cancelKey{instanceID: instanceID, timerID: tf.TimerID})
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

// buildTimerJob assembles the runtime's Manual scheduler job for a process-
// instance timer: the typed descriptor (kind included, so a durable Save of
// this job's descriptor faithfully mirrors the persisted ArmedTimer.Kind —
// ADR-0134 B1), the converted trigger, the engine's standard fire callback
// wrapped as a [scheduler.JobFunc], and a static data provider carrying the
// timer's identity. It errors when trig cannot be converted (an unsupported
// kind).
//
// The wrapped fire deliberately keeps timerFireFunc's internal
// context.Background() usage: gocron cancels a one-shot's injected per-run ctx
// shortly after the task returns, and the fire is a self-contained
// continuation by design.
func (driver *ProcessDriver) buildTimerJob(def *model.ProcessDefinition, instanceID, timerID string, trig schedule.TriggerSpec, nextRun time.Time, kind engine.TimerKind) (*scheduledTimerJob, error) {
	strig, err := convertTrigger(trig)
	if err != nil {
		return nil, err
	}
	j := driver.newTimerJob(def, instanceID, timerID, trig, strig, nextRun, kind)
	return newScheduledTimerJob(j, driver.clk.Now()), nil
}

// newTimerJob assembles the runtime's Manual [timerJob] from its parts: the
// typed descriptor spec (with the caller-computed authoritative nextRun), the
// pre-converted scheduler trigger strig, the engine's standard fire callback
// wrapped as a [scheduler.JobFunc], and a static data provider carrying the
// timer's identity. Shared by buildTimerJob (rehydration, persisted nextRun)
// and timerJobsFor (fresh arms, nextRun = Trigger.Next(now)).
func (driver *ProcessDriver) newTimerJob(def *model.ProcessDefinition, instanceID, timerID string, trig schedule.TriggerSpec, strig scheduler.Trigger, nextRun time.Time, kind engine.TimerKind) *timerJob {
	fire := driver.timerFireFunc(def, instanceID, timerID)
	return &timerJob{
		spec: kernel.JobSpec{
			TimerID:    timerID,
			InstanceID: instanceID,
			DefID:      def.ID,
			DefVersion: def.Version,
			Trigger:    trig,
			NextRun:    nextRun,
			Kind:       kind,
		},
		trig: strig,
		fn:   func(context.Context, scheduler.DataProvider) error { fire(); return nil },
		data: scheduler.NewStaticDataProvider(map[string]any{
			"instance_id": instanceID,
			"timer_id":    timerID,
			"def_id":      def.ID,
			"def_version": def.Version,
		}),
	}
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
		// A timer fire advances an ALREADY-running instance: it is a continuation, not
		// new external work, so it drives via the ungated applyTrigger (never rejected
		// mid-fire during drain). It takes NO inflight slot: an in-flight owned-scheduler
		// fire is drained by the scheduler Close (Shutdown step 2), which blocks until
		// gocron joins its running fire jobs — so Shutdown still waits for a mid-flight
		// fire to finish, and no timer-fire Add can race waitInflight's Wait (closes F2).
		fireCtx := context.Background()
		trg := engine.NewTimerFired(driver.clk.Now(), timerID)
		driver.obs.timerFired.Add(fireCtx, 1)
		const maxAttempts = 5
		var err error
		for range maxAttempts {
			if _, err = driver.applyTrigger(fireCtx, def, instanceID, trg); err == nil {
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
	jobs, err := NewJobStore(driver).Load(ctx)
	if err != nil && !errors.Is(err, scheduler.ErrUnresolvedTimerDefinitions) {
		return fmt.Errorf("workflow-runtime: RehydrateTimers: %w", err)
	}
	for _, j := range jobs {
		if aerr := driver.sched.Activate(ctx, j); aerr != nil {
			// One unschedulable timer must never abort the batch.
			driver.obs.tel.Logger.LogAttrs(ctx, slog.LevelWarn, "runtime: rehydrate: failed to re-arm timer, skipping",
				append(driver.obs.tel.LogAttrs(ctx),
					slog.String("timer_id", j.ID()),
					slog.Any("error", aerr))...)
		}
	}
	// Propagate the unresolved-definitions error (if any) so this strict,
	// explicit entry point still reports the skipped subset to its caller.
	if err != nil {
		return fmt.Errorf("workflow-runtime: RehydrateTimers: %w", err)
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
		// A timer-start creates NEW work, so it goes through the admission gate. Once
		// draining, drop the fire (benign: the durable arm rehydrates on next boot).
		release, ok := driver.admit()
		if !ok {
			driver.obs.tel.Logger.LogAttrs(context.Background(), slog.LevelDebug,
				"runtime: timer-start fire skipped: driver shutting down",
				slog.String("timer_id", timerID),
				slog.String("def_id", def.ID),
				slog.Int("def_version", def.Version),
				slog.String("node_id", nodeID))
			return
		}
		defer release()
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
// occurrence. It schedules an [scheduler.ActivationAuto] job under
// startTimerJobKind: no JobStore is registered for that kind, so the job is
// in-memory-only (persist-nothing) and arms immediately — a timer-start
// re-derives from the registered definition on boot, so nothing durable is
// needed. An unschedulable trigger is logged at WARN and skipped — it must
// never crash the driver.
func (driver *ProcessDriver) armStartTimer(ctx context.Context, def *model.ProcessDefinition, nodeID, timerID string, trig schedule.TriggerSpec) {
	sj, err := driver.scheduleStartTimerJob(ctx, def, nodeID, timerID, trig)
	if err != nil {
		// A skip during shutdown (scheduler closed) is expected, not a lost timer-start:
		// it re-arms purely from the registered definition on next boot (Finding 4).
		driver.obs.tel.Logger.LogAttrs(ctx, slog.LevelWarn, "runtime: armStartTimer: trigger not schedulable, skipping timer-start (re-arms from definition on next boot)",
			append(driver.obs.tel.LogAttrs(ctx),
				slog.String("timer_id", timerID),
				slog.String("def_id", def.ID),
				slog.Int("def_version", def.Version),
				slog.String("node_id", nodeID),
				slog.Bool("driver_shutting_down", driver.IsShuttingDown()),
				slog.Any("error", err))...)
		return
	}
	driver.obs.tel.Logger.LogAttrs(ctx, slog.LevelDebug, "runtime: armStartTimer: scheduled",
		append(driver.obs.tel.LogAttrs(ctx),
			slog.String("timer_id", timerID),
			slog.String("def_id", def.ID),
			slog.Int("def_version", def.Version),
			slog.String("node_id", nodeID),
			slog.Time("next_run", sj.NextRun()))...)
}

// scheduleStartTimerJob converts trig and schedules the timer-start's Auto job
// on the driver's scheduler, wrapping the existing startTimerFireFunc closure.
// Like buildTimerJob's wrapping of timerFireFunc, the fire keeps its internal
// context.Background() usage — it is self-contained by design.
func (driver *ProcessDriver) scheduleStartTimerJob(ctx context.Context, def *model.ProcessDefinition, nodeID, timerID string, trig schedule.TriggerSpec) (scheduler.ScheduledJob, error) {
	strig, err := convertTrigger(trig)
	if err != nil {
		return nil, err
	}
	fire := driver.startTimerFireFunc(def, nodeID, timerID)
	job, err := scheduler.NewJobWithID(timerID, startTimerJobKind, strig,
		func(context.Context, scheduler.DataProvider) error { fire(); return nil },
		scheduler.NewEmptyDataProvider())
	if err != nil {
		return nil, err
	}
	return driver.sched.Schedule(ctx, job)
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
	// Event-based START (timer/signal/message) needs to enumerate registered
	// definitions. A registry that does not implement kernel.DefinitionLister
	// silently disables it: warn once here so the missing capability is
	// diagnosable rather than a mystery non-event.
	if _, ok := driver.defsReg.(kernel.DefinitionLister); !ok {
		driver.obs.tel.Logger.LogAttrs(ctx, slog.LevelWarn,
			"runtime: event-based start disabled: definition registry does not implement DefinitionLister (no start timers armed)",
			driver.obs.tel.LogAttrs(ctx)...)
		return nil
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

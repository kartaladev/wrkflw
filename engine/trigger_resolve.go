package engine

import (
	"errors"
	"time"

	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
)

// ErrUnsupportedTrigger reports that a trigger form requires a native scheduler
// (recurring/calendar/random: cron, daily, weekly, monthly, every-random) that
// this build does not wire. The reducible one-shot and interval forms
// (AfterDuration/At/AfterExpr and Every/EveryExpr) never return this error; the
// native forms are wired live in a later plan.
var ErrUnsupportedTrigger = errors.New("workflow-engine: trigger kind needs a native scheduler (not available in this build)")

// ResolveTrigger resolves the dynamic expr forms of a [schedule.TriggerSpec] to
// concrete durations and returns all other forms unchanged. The expr path reuses
// the engine's [ConditionEvaluator.EvalDuration] so a definition may compute a
// deadline/reminder interval from process-instance variables.
//
// AfterExpr resolves to AfterDuration; EveryExpr resolves to Every; every other
// form (including the native recurring/calendar forms) passes through untouched.
func ResolveTrigger(eval ConditionEvaluator, spec schedule.TriggerSpec, env map[string]any) (schedule.TriggerSpec, error) {
	code, kind, ok := spec.Expr()
	if !ok {
		return spec, nil
	}
	d, err := eval.EvalDuration(code, env)
	if err != nil {
		return schedule.TriggerSpec{}, err
	}
	if kind == schedule.KindEveryExpr {
		return schedule.Every(d), nil
	}
	return schedule.AfterDuration(d), nil
}

// triggerDelay returns the one-shot delay for a reducible trigger measured from
// now: AfterDuration yields its duration, At yields at.Sub(now), and Every yields
// its interval (used as the reminder delay to the next fire). The native
// recurring/calendar/random forms return [ErrUnsupportedTrigger]; a later plan
// wires them onto the native scheduler.
func triggerDelay(spec schedule.TriggerSpec, now time.Time) (time.Duration, error) {
	if d, ok := spec.Duration(); ok { // AfterDuration or Every (interval)
		return d, nil
	}
	if at, ok := spec.AbsTime(); ok {
		return at.Sub(now), nil
	}
	return 0, ErrUnsupportedTrigger
}

package engine

import (
	"github.com/zakyalvan/krtlwrkflw/definition/schedule"
)

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

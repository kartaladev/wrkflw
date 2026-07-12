package engine

import (
	"time"

	"github.com/kartaladev/wrkflw/internal/expreval"
)

// ConditionEvaluator evaluates the expression strings a process definition
// carries — gateway conditions, timer/deadline durations, and message/event
// correlation keys — against a process-instance variable environment.
//
// The engine depends on this interface, not on a concrete evaluator, so a
// consumer that must evaluate UNTRUSTED definitions can supply a
// timeout-capable evaluator (see [StepOptions.Evaluator] and the runtime
// ProcessDriver's WithExpressionTimeout / WithConditionEvaluator options) without the
// default deterministic path acquiring any wall-clock dependency.
//
// The in-repo *expreval.Evaluator satisfies this interface.
type ConditionEvaluator interface {
	// EvalBool evaluates code to a boolean (gateway/flow conditions).
	EvalBool(code string, env map[string]any) (bool, error)
	// EvalDuration evaluates code to a time.Duration (timer/deadline durations).
	EvalDuration(code string, env map[string]any) (time.Duration, error)
	// EvalString evaluates code to a string (correlation keys).
	EvalString(code string, env map[string]any) (string, error)
}

// conditions is the engine's shared, memoizing expression evaluator and the
// pure DEFAULT used when a Step carries no injected evaluator. Compilation is
// deterministic and the cache is referentially transparent, so using a shared
// instance does not affect Step's determinism.
//
// The wall-clock evaluation guard (expreval.WithTimeout, ADR-0049) is explicitly
// DISABLED here: the engine core must stay wall-clock-free and side-effect-free
// (locked invariant, ADR-0003), so the default Step never spawns the guard's
// goroutine/timer.
//
// A consumer that needs the DoS guard for in-engine evaluation supplies its own
// timeout-capable [ConditionEvaluator] via [StepOptions.Evaluator] (ADR-0056);
// that is an explicit opt-in trading the deterministic-replay guarantee for DoS
// protection.
var conditions = expreval.New(expreval.WithTimeout(0))

// resolveEvaluator returns the evaluator a Step must use: the one injected via
// opt when present, else the pure package-global default. Keeping this in one
// place guarantees every call site falls back identically, so the default path
// stays byte-identical to the pre-injection behaviour.
func resolveEvaluator(opt StepOptions) ConditionEvaluator {
	if opt.Evaluator != nil {
		return opt.Evaluator
	}
	return conditions
}

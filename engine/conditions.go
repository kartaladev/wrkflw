package engine

import "github.com/zakyalvan/krtlwrkflw/internal/expreval"

// conditions is the engine's shared, memoizing expression evaluator. Compilation
// is deterministic and the cache is referentially transparent, so using a shared
// instance does not affect Step's determinism.
//
// The wall-clock evaluation guard (expreval.WithTimeout, ADR-0049) is explicitly
// DISABLED here: the engine core must stay wall-clock-free and side-effect-free
// (locked invariant, ADR-0003), so Step never spawns the guard's goroutine/timer.
// The timeout remains an opt-in capability of expreval.Evaluator for callers that
// evaluate untrusted definitions; wiring an injectable, timeout-capable evaluator
// into the engine is a deferred follow-up (see ADR-0049).
var conditions = expreval.New(expreval.WithTimeout(0))

package engine

import "github.com/zakyalvan/krtlwrkflw/internal/expreval"

// conditions is the engine's shared, memoizing expression evaluator. Compilation
// is deterministic and the cache is referentially transparent, so using a shared
// instance does not affect Step's determinism.
var conditions = expreval.New()

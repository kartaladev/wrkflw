# 0104. Flexible boundary error matching (Check → Expr → Code precedence)

- Status: Accepted
- Date: 2026-07-08

## Context

The original error boundary implementation matched errors by exact error-code
string comparison only: `BoundaryEvent.ErrorCode` was compared against
`ActionFailed.Err` (the thrown error's `Error()` string). This was sufficient for
simple coded errors (`errors.New("PAYMENT_FAILED")`) but limiting in practice:

- Authors could not use `errors.Is` / `errors.As` to match wrapped or sentinel
  errors, since the engine only saw the error string at the point of code.
- Authors could not express multi-condition or variable-based matching without
  post-processing the error code into a string and comparing it exactly.
- YAML-based definitions had no way to match errors by pattern or instance
  variable context — they were limited to a single exact code or catch-all.

Additionally, `ActionFailed.Cause error` (the original `error` value thrown by
the action) was not threaded through to the matching layer, so Go-level
`errors.Is` / `errors.As` checks were impossible.

## Decision

We will implement a three-level precedence chain for error boundary matching,
evaluated by `propagateError` at the moment of action failure:

**1. `ErrorCheck func(map[string]any, error) bool` (highest precedence)**

Set via `event.WithBoundaryErrorCheck`. A Go closure that receives the current
instance variables and the original `error` value (threaded from the action as
`ActionFailed.Cause`). It may use `errors.Is` / `errors.As` to match wrapped
or sentinel errors. Non-serializable: absent from the definition wire format.
Intended for Go-authored definitions only.

**2. `ErrorExpr string` (second precedence)**

Set via `event.WithBoundaryErrorExpr`. A serializable expr-lang predicate
evaluated over the process-instance variables plus an injected `_error` variable
(the thrown error code string, i.e. `err.Error()`). Truthy return = catch.
Written to the wire as `BoundaryErrorExpr`. Appropriate for YAML-authored
definitions and runtime-configurable matching.

**Expr-lang limitation (IMPORTANT):** the default `expr-lang/expr` evaluator
does NOT support `strings.HasPrefix` or the `in` operator on plain strings.
Authors should use explicit equality comparisons for multi-code matching:

```
_error == "PAYMENT_FAILED" || _error == "CARD_EXPIRED"
```

**3. `ErrorCode string` (lowest precedence, existing behavior)**

Set via `event.WithBoundaryErrorCode`. Exact string match of `err.Error()` against
the code. Empty string is the catch-all (matches any error). This is the original
behavior, unchanged and preserved as the default.

The helper `boundaryErrorMatches(n event.BoundaryEvent, vars map[string]any, cause error, errorCode string, eval ConditionEvaluator) (bool, error)`
encapsulates the three-level check; `propagateError` calls it once per
error-type boundary at failure time.

A runtime eval error from `ErrorExpr` (e.g. a type mismatch such as
`_error + 42`) is **non-fatal for matching**: `propagateError` treats the
boundary as non-matching, continues evaluating subsequent boundaries in
the same scope, and does not abort the Step. Rationale: this is the
error-recovery path — one malformed predicate must not brick routing for
all boundaries; an unmatched error still falls through to the next
handler or raises an incident.

**`ActionFailed.Cause error`** is threaded from the failing action through the
runtime deliver loop to `propagateError` so that `ErrorCheck` receives the live
`error` value. The field is non-persisted (Go-only, runtime-only): it is not
written to the instance state or wire format, preserving snapshot-based
determinism.

**Snapshot determinism.** `propagateError` runs exactly once at failure time
inside the deliver loop, which commits `InstanceState` as an atomic step. There
is no replay or re-evaluation of error predicates: the engine is snapshot-based,
not event-sourced, so the matched boundary's flow is recorded as part of the
committed state and re-evaluated on rehydration is neither needed nor performed.
`ErrorCheck` closures should therefore avoid external mutable state (they run at
most once per error event).

## Consequences

- **Positive.** `WithBoundaryErrorCheck` enables `errors.Is` / `errors.As`
  matching on wrapped or typed sentinel errors — Go-authored definitions can
  use the full Go error-handling idiom.
- **Positive.** `WithBoundaryErrorExpr` gives YAML-authored definitions
  pattern-based and instance-variable-aware error matching without code changes.
- **Positive.** Exact-code matching (`WithBoundaryErrorCode`) is preserved
  unchanged; existing definitions are unaffected.
- **Neutral.** `ErrorCheck` is non-serializable: a definition using it cannot
  be stored and reloaded from YAML/JSON without re-supplying the closure. Authors
  who need serializable matching should use `WithBoundaryErrorExpr` instead.
- **Neutral.** The `_error` variable injected into `ErrorExpr` evaluation is
  boundary-expr-only; it does not pre-exist in the instance variable map and
  is not visible outside the error-boundary predicate.
- **Neutral (expr-lang limitation).** The default evaluator does not support
  `strings.HasPrefix` or `in` on strings; authors must write explicit equality
  chains. A future ADR may swap in a richer evaluator if this becomes a pain point.
- **Positive (determinism).** Because `propagateError` runs once at failure time
  and commits the result atomically, there is no risk of non-deterministic
  re-evaluation on rehydration. The closed-over state in `ErrorCheck` is
  irrelevant after the first (and only) match evaluation.

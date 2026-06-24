# 49. Bound expression evaluation with a wall-clock timeout

- Status: Accepted
- Date: 2026-06-25

## Context

Every gateway condition, timer/SLA duration, and correlation key is evaluated by
`internal/expreval` via `expr.Run`, which had **no timeout, deadline, or resource
budget.** A pathological or malicious expression (deeply nested ranges, an
expensive builtin, a hostile data-driven predicate) could run unbounded. Because
the engine's `Step` is driven by a single-threaded loop and `expreval` is invoked
synchronously deep inside it, one runaway expression **stalls every other instance
behind it and makes the engine miss its SLA/timer fires** — the DoS the audit
flagged HIGH.

Two constraints shaped the fix:

1. **`expr.WithContext` is insufficient.** It only propagates a context to
   context-aware *function calls*; it does not preempt a pure-CPU expression.
2. **`Step` must stay pure and ctx-free** for determinism/replay. `expreval` is
   reached through a package-global `conditions = expreval.New()` called from many
   sites inside `Step`, none of which carry a `context.Context`. Threading a real
   context would break engine purity.

Go also cannot preempt a running goroutine, so no in-process mechanism can *kill*
a runaway evaluation — only abandon it.

## Decision

Add an internal **wall-clock guard** to the `Evaluator`, default-on:

- `New(opts ...Option)` defaults `timeout` to `DefaultTimeout = 5s`;
  `WithTimeout(d)` overrides it, and `WithTimeout(0)` disables the guard (fast
  path, no goroutine — for fully trusted definitions).
- A private `run(p, env)` wraps `expr.Run`: when `timeout > 0` it runs the
  evaluation on a goroutine and races it against a timer, returning the new
  sentinel `ErrEvalTimeout` if the timeout fires. The result channel is buffered
  so the goroutine never blocks on send after a timeout; a `recover` keeps a panic
  from escaping the goroutine.
- All three `Eval*` methods call `run` instead of `expr.Run`. No engine call site
  changes; the package-global `conditions` evaluator is guarded by default.

## Consequences

- The engine driver loop regains control after at most `DefaultTimeout` per
  evaluation; a runaway expression can no longer stall sibling instances or starve
  the scheduler. A timed-out gateway/timer surfaces `ErrEvalTimeout` and the
  instance fails cleanly.
- **The guard bounds latency, not CPU.** An abandoned pure-CPU expression keeps
  consuming a core (one goroutine) until it finishes — Go cannot preempt it. This
  is documented on `ErrEvalTimeout`. Defense against CPU exhaustion (a compile-time
  AST/complexity budget) is a deferred follow-up; `expr` v1.17 exposes no
  instruction limit.
- **Determinism:** the timeout is generous enough that no legitimate expression
  approaches it, so normal results are unaffected and `Step` stays deterministic
  for replay. Only pathological inputs trip it, and failing is the safe, acceptable
  outcome — but the trip itself is wall-clock-dependent, so the guard is explicitly
  a *safety backstop*, not part of normal semantics.
- Each guarded evaluation spawns one goroutine + timer (~µs). Negligible for a
  workflow engine's evaluation rate; consumers who need maximum throughput on
  fully trusted definitions can disable it with `WithTimeout(0)`.
- **The engine disables the guard by default (revised after whole-branch review).**
  `expreval.New()` defaults the guard on (5s) for general callers, but the engine's
  package-global evaluator (`engine/conditions.go`) is constructed with
  `WithTimeout(0)`. Enabling it there would make the deterministic `Step` reach a
  real `time.NewTimer` and spawn a goroutine on every gateway/timer/correlation
  evaluation — violating the **locked** "core never reads the wall clock" invariant
  (ADR-0003) and adding hot-path overhead. So the core stays wall-clock-free and the
  DoS guard is **not** active for in-engine evaluation by default. The `WithTimeout`
  capability, `ErrEvalTimeout`, and the guarded `run` path remain fully implemented
  and tested for any caller that constructs its own `Evaluator`.
- **Known limitation / deferred follow-up:** because the engine evaluator is a
  package-global with the guard off, a consumer that must evaluate *untrusted*
  definitions cannot currently turn the DoS guard on for in-engine evaluation.
  Making the engine's evaluator injectable (so such a consumer can supply a
  timeout-capable evaluator while the default stays pure) is the deferred follow-up
  — it is a deliberate engine-API change and must itself be ADR'd.

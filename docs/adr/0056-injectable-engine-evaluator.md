# 56. Injectable, timeout-capable engine evaluator

- Status: Accepted
- Date: 2026-06-25

## Context

ADR-0049 added an opt-in wall-clock timeout to `internal/expreval`
(`WithTimeout` / `ErrEvalTimeout`) so a runaway or hostile expression — a gateway
condition, timer/SLA duration, or correlation key — can be abandoned instead of
stalling the engine's single-threaded driver loop and every sibling instance
behind it (the DoS the audit flagged HIGH). Go cannot preempt a running
goroutine, so the guard bounds *latency*, not CPU.

But ADR-0049 also kept the engine's package-global evaluator
(`engine/conditions.go`) built with `WithTimeout(0)`: enabling the guard there
would make the deterministic `Step` reach a real `time.NewTimer` and spawn a
goroutine on every evaluation, violating the **locked** "core never reads the
wall clock" invariant (ADR-0003) and adding hot-path overhead. The consequence,
named explicitly in ADR-0049 as a deferred follow-up: a consumer that must
evaluate **untrusted** definitions could not turn the DoS guard on for in-engine
evaluation, because the evaluator was a hard-coded package global.

The fix had to make the engine's evaluator **injectable** so such a consumer can
supply a timeout-capable evaluator, **without** making the default path impure or
non-deterministic.

## Decision

Introduce a small `engine.ConditionEvaluator` interface
(`EvalBool` / `EvalDuration` / `EvalString`) — already satisfied by
`*expreval.Evaluator` — and depend on it rather than the concrete global:

- `StepOptions` gains an optional `Evaluator ConditionEvaluator` field. When it
  is nil (the default), a single `resolveEvaluator(opt)` helper falls back to the
  pure, wall-clock-free package-global `conditions` evaluator, so the default path
  stays **byte-identical** and deterministic.
- The resolved evaluator is threaded through `Step`: into `stepCtx.eval` for the
  node-strategy dispatch layer, and as an explicit `eval ConditionEvaluator`
  parameter on the free functions that evaluate expressions or drive forward
  (`selectExclusiveTarget`, `forkInclusive`, `armBoundaries`,
  `armEventSubprocesses`, `handleReminderFired`, `reinvokeServiceAction`, and the
  `drive`/`propagateError`/`beginCompensation`/`resolveGatewayWin`/`fireBoundaryArm`/
  `fireEventSubprocessArm` chain). All ~13 former `conditions.Eval*` call sites now
  go through the resolved evaluator. Signatures and `(state, commands)` outputs are
  otherwise unchanged.
- The reference `runtime.Runner` gains two options that build/hold a **long-lived**
  `*expreval.Evaluator` (so its compile cache is reused across steps) and pass it
  into the engine via the `StepOptions` the runner already constructs:
  `WithExpressionTimeout(d)` (the common DoS-guard case) and
  `WithConditionEvaluator(eval)` (full control). Default: nil — the pure global,
  current behaviour.

## Consequences

- A consumer that evaluates untrusted definitions can now enable the DoS guard for
  in-engine evaluation per runner (`WithExpressionTimeout(d)`); a runaway gateway
  condition surfaces `expreval.ErrEvalTimeout` through `Step` and fails the
  instance cleanly instead of hanging the driver loop. The ADR-0049 follow-up is
  resolved.
- **The default stays pure.** With no injected evaluator, every call site resolves
  to the same `WithTimeout(0)` global as before: no `time.NewTimer`, no goroutine,
  no wall-clock read. `Step` remains deterministic for replay; the existing engine
  tests pass with no assertion changes and `FuzzStep` still passes.
- **The determinism trade-off is the consumer's explicit, opt-in choice.** Enabling
  the timeout makes expression evaluation wall-clock-dependent, so a timed-out
  result is no longer reproducible on replay — documented on both runner options.
  Trusted-definition deployments leave it off and keep deterministic replay.
- The interface is an additive, low-churn engine-API change: `StepOptions` gains
  one optional field (zero value = old behaviour) and the internal threading is
  mechanical. No transport, persistence, or scheduling code is touched.
- The guard still bounds latency, not CPU (ADR-0049): an abandoned pure-CPU
  expression keeps consuming a core until it finishes. A compile-time
  AST/complexity budget remains the separate deferred follow-up.

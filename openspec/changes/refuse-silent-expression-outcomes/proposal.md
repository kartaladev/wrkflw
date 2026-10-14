# Proposal

## Why

Two expression outcomes are silent today, against the project rule that silence is never correct. A third outcome cannot be matched by a consumer. The baseline found all three (finding 2 of D7 in the archived `establish-baseline-specs` design). The maintainer has decided how each one ends.

Re-measured against `main` (expr-lang/expr v1.17.8, the version `go.mod` pins):

| Outcome | What happens today | Where |
|---|---|---|
| A gateway condition reads a variable the instance does not hold | `missing > 1` raises a nil-operand error, which `EvalBool` maps to `false`. `status == "x"` returns `false` with no error at all. Either way the token takes the default flow, or the step fails with `ErrNoMatchingFlow`. | `internal/expreval` `EvalBool` / `isNilOperandError`, measured with a scratch probe |
| A boundary error expression (`ErrorExpr`) times out under `WithExpressionTimeout` | Every evaluation error, the timeout included, is logged at Debug and treated as a non-match. The error then propagates as if the boundary did not exist. | `engine/step_errors.go` `findDirectBoundary` |
| An expression evaluation times out anywhere | The error is `expreval.ErrEvalTimeout`, which lives in an `internal` package. A consumer cannot name it in `errors.Is`. Only in-module tests can, for example `runtime/expression_timeout_test.go`. | `internal/expreval/expreval.go` |

## What Changes

- **BREAKING.** A flow condition on an exclusive or inclusive gateway that reads a process variable the instance does not hold fails the step with a new exported sentinel, `engine.ErrAbsentVariable`, instead of evaluating to false. The refusal surfaces the way every other gateway-condition error surfaces today: `Step` returns it wrapped with the gateway and flow IDs, and through a `ProcessDriver` the call returns it. It does not raise an incident, because no gateway-condition error does today. A definition that relied on absent-means-false must now supply the variable, or use a default flow fed by a variable it always sets.
- A boundary error expression that names an absent variable stays a non-match, which is what it observably is today. The non-timeout evaluation errors on that path keep their existing rule (logged and skipped, so scanning continues to the next boundary), and the new refusal joins them. Only the timeout changes rule (next bullet).
- **BREAKING.** A timeout while evaluating a boundary error expression fails the step with the expression-timeout sentinel. That matches how a gateway condition, a timer or deadline duration and a correlation key already fail under `WithExpressionTimeout`. Scanning stops at that point: no later boundary is tried, and the error does not propagate to an enclosing scope.
- The expression-evaluation timeout is exported as `engine.ErrExpressionTimeout`, and consumers can match it with `errors.Is` at every site that times out. The internal `expreval.ErrEvalTimeout` stays as the same value, re-exported the way `runtime/task.ErrTaskNotOpen` re-exports `engine.ErrTaskNotOpen`. A consumer-supplied `ConditionEvaluator` signals a timeout by returning (or wrapping) `engine.ErrExpressionTimeout`.
- Godoc on `WithExpressionTimeout`, `WithConditionEvaluator`, `ConditionEvaluator.EvalBool`, `StepOptions.Evaluator` and `findDirectBoundary` is updated to name the two sentinels and the boundary-timeout rule. The comments that state "a missing variable means the condition is not satisfied" are removed.
- Persisted formats: no impact. No snapshot, journal, outbox or column changes. A stored instance whose next gateway reads an absent variable now fails that step on the new version instead of taking the default flow. That is the BREAKING behavior above, not a format change.
- Default security posture: no impact. Authorization attribute predicates (`authz.AttributeDecider`, `casbinauthz`) share the evaluator, but they keep their current absent-means-false (deny) behavior, pinned by a test. See out of scope.
- Consumer go.mod: no impact. There is no new dependency, and the code must not add `expr.Env`; see kartaladev/wrkflw#186.
- No GitHub issue is absorbed. The source is finding 2 of D7 in the archived `establish-baseline-specs` design.

Decisions this change is specified with (made by the maintainer):
- A condition that reads an absent variable is refused with a new exported sentinel. It does not evaluate to false.
- A boundary error-expression timeout fails the step with the expression-timeout error. It is not a non-match.
- The expression-timeout error is an exported sentinel that consumers match with `errors.Is`.

Decided here, from the code (see design D3 and D5):
- Surfacing is a step error, not an incident, for gateways and for the boundary timeout alike. That is how every expression-evaluation error on the routing path surfaces today.
- The sentinels live in package `engine`, next to `ErrNoMatchingFlow`, as re-exports of the values in `internal/expreval`. `engine` imports `expreval`, so `expreval` cannot import `engine`.

Open, needing the maintainer before apply (design, Open Questions). The specs deliberately state no row that depends on them:
- Q1. Does "reads" mean the condition *names* the variable (static), or that the evaluation actually *reaches* it at runtime? The two differ for `amount > 100 || missing > 1` when `amount` is 150.
- Q2. Do the explicit absence idioms `missing ?? 0` and `missing == nil` stay legal, or are they refused too?
- Q3. Does a variable that is present with a null value, or a missing key inside a present map (`order.amount`), keep today's nil-operand-means-false mapping, or does it become an ordinary evaluation error?

Out of scope:
- Absent variables in timer or deadline duration expressions (these already fail, without a sentinel) and in correlation keys. A correlation key over an absent variable currently renders as the string `"<nil>"` (measured with a scratch probe). That is a silent outcome too, but the decision covers conditions only, so it needs its own change.
- Sentinels for the other expression refusals (compile, type, duration and correlation-key errors). These belong to `export-refusal-sentinels` (D7 finding 4); this change owns only the timeout sentinel and the absent-variable sentinel, and that change's proposal already defers both to this one.
- Whether non-timeout evaluation errors in a boundary error expression should also fail the step instead of being skipped.
- Authorization attribute predicates. They are not definition conditions. Making them refuse absent variables would turn a 403 deny into a 5xx error, which is a posture question for its own change.
- kartaladev/wrkflw#186 (the `expr.Env` hazard, pending migration). The absent-variable check must not be built on `expr.Env`, and this change neither fixes nor widens that issue.
- kartaladev/wrkflw#187 (engine-stamped keys not reserved at the ingress, pending migration). It is unaffected.
- kartaladev/wrkflw#141 is closed. Its measurement is baselined as "Instance data is bound as data", and that requirement is unchanged here.

**Coordination**
- Size: M.
- Depends on: `establish-baseline-specs`.
- Blocks: nothing.
- Readiness: human (Q1 to Q3 need the maintainer before apply).

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `expression-evaluation`:
  - "A condition that is not a valid boolean expression is an error" gains the absent-variable refusal.
  - "Expression evaluation is bounded on opt-in" covers boundary error expressions and names `engine.ErrExpressionTimeout`.
  - A new requirement states the boundary error-expression evaluation rule.
- `token-routing-and-joins`: "Exclusive gateway takes one branch or refuses the step" and "Inclusive fork takes every true branch" state that a condition over an absent variable fails the step with `ErrAbsentVariable`, not a fall-through to the default flow.

## Impact

- `internal/expreval/expreval.go`:
  - a new `ErrAbsentVariable`;
  - an absent-variable check in `EvalBool`, behind an evaluator option so that the authz evaluators keep their current semantics;
  - removal or narrowing of the nil-operand mapping, depending on Q3.
- `engine/errors.go`: `ErrAbsentVariable` and `ErrExpressionTimeout`, re-exported.
- `engine/conditions.go` and `runtime/processdriver_options.go`: the engine's default evaluator and `WithExpressionTimeout` turn the absent-variable check on.
- `engine/step_errors.go` and `engine/step_triggers.go`: `findDirectBoundary` and `findEnclosingBoundary` return a timeout error, and `propagateError` and the message-trigger call site pass it up.
- Tests:
  - `internal/expreval` (the "undefined variable treated as nil" row and the `TestExprNilComparisonErrorShapeUnchanged` canary);
  - `engine` boundary-matching tests;
  - the `runtime` timeout tests, which move from `expreval.ErrEvalTimeout` to `engine.ErrExpressionTimeout`;
  - any engine or runtime test that drives a gateway without supplying the variable its condition reads.

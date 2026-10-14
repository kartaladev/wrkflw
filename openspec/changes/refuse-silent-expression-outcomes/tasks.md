# Tasks

Gate: Q1 to Q3 in design.md are answered by the maintainer before group 1 starts. Each answer that adds a spec row goes into the delta specs first, and its row joins the red test of the group that owns it (Q1 and Q2 in 2.1, Q3 in 2.1 or 4.1). Start every Go task with `cc-skills-golang:golang-how-to`. Write table tests in the `table-test` form. Waits and deadlines follow `docs/agents/eventually-waits.md` and `docs/agents/test-deadlines.md`. Verification commands run from the repository root, on the toolchain `go.mod` declares.

## 0. Gate

- [ ] 0.1 Record the maintainer's answers to Q1, Q2 and Q3 under design.md Open Questions. Add each resulting row to `specs/expression-evaluation/spec.md`. If Q1 is answered "reaches", switch D1 to the `expr.Patch` mechanism. Verify: `openspec validate refuse-silent-expression-outcomes --strict` passes, and design.md lists no unanswered question.

## 1. Export the expression-timeout sentinel

- [ ] 1.1 Red. In `engine`, add a test asserting that a timed-out evaluation matches `engine.ErrExpressionTimeout` under `errors.Is`, through `Step` with an `expreval.WithTimeout` evaluator in `StepOptions.Evaluator`. Switch `TestProcessDriverWithExpressionTimeoutGuardsGateway` and `TestBaselineExpressionTimeoutThroughDriver` from `expreval.ErrEvalTimeout` to `engine.ErrExpressionTimeout`. Scenario: "A runaway expression is abandoned under the opt-in timeout" (rows exclusive gateway condition, timer duration, message correlation key). Verify: `GOTOOLCHAIN=go1.26.9 go test ./engine ./runtime -run 'ExpressionTimeout' -count=1` fails to compile on the undefined `engine.ErrExpressionTimeout`.
- [ ] 1.2 Green. Add `ErrExpressionTimeout = expreval.ErrEvalTimeout` to `engine/errors.go`, with godoc naming every site that can return it (D5). Update the godoc of `WithExpressionTimeout`, `WithConditionEvaluator`, `ConditionEvaluator` and `StepOptions.Evaluator` to name `engine.ErrExpressionTimeout`. Tell consumer evaluators to wrap it on a timeout. Verify: the 1.1 command passes, and `bash scripts/check-doc-refs.sh` passes.

## 2. Refuse absent variables in conditions

- [ ] 2.1 Red. In `internal/expreval`, add a table test over an evaluator built with the absent-variable option:
  - the rows of "A condition over an absent variable is refused", asserting `errors.Is(err, ErrAbsentVariable)` and that the error names the variable;
  - the rows of "Negative control - the same conditions evaluate once the variable is present";
  - one present-variable control per construct the D1 walk must exclude: a builtin call (`len(xs) > 0`), a `let` binding, a predicate (`all(xs, # > 0)`, `filter(xs, .p > 0)`), a member access (`order.amount > 1`) and the injected `_error`.

  Change the existing "undefined variable treated as nil (no error)" row of `TestEvalBool` to expect the refusal under the option. Verify: `GOTOOLCHAIN=go1.26.9 go test ./internal/expreval -count=1` fails on the undefined option and sentinel, and once stubbed, fails on the absent rows.
- [ ] 2.2 Green. Implement D1. Record the free identifiers at compile time, cached with the program, and check them before running when the option is on. Return `fmt.Errorf("%w: %q in %q", ErrAbsentVariable, name, code)`. Do not use `expr.Env` (kartaladev/wrkflw#186). Keep or remove `isNilOperandError` according to Q3, and update its canary test `TestExprNilComparisonErrorShapeUnchanged` to match. Scenarios: "A condition over an absent variable is refused" and "Negative control - the same conditions evaluate once the variable is present". Verify: `GOTOOLCHAIN=go1.26.9 go test ./internal/expreval -count=1` passes, `TestBuiltinsAreNotShadowableByEnvKeys` included.
- [ ] 2.3 Red. Add an `authz` test pinning that an attribute predicate over an absent variable still denies (`ErrNotAuthorized`, not an evaluation error) under `RoleAuthorizer` and `AttributeDecider`, and the same for `casbinauthz`. It must stay green. Then, as a temporary mutation, turn the option on in `authz/authz.go` and confirm the test goes red, and revert the mutation. Verify: `GOTOOLCHAIN=go1.26.9 go test ./authz ./internal/authz/... -count=1` passes on the reverted tree.
- [ ] 2.4 Refactor. Consider `/simplify` on `internal/expreval`. Update the package doc and the `EvalBool` godoc to drop "undefined variables evaluate to nil", and update the `definition/model/validate/expr` package comment that cites that mapping. Verify: the 2.2 and 2.3 commands pass, and `bash scripts/check-doc-refs.sh` passes.

## 3. Wire the refusal into gateways

- [ ] 3.1 Red. In `engine`, add table tests driving `Step` through an exclusive and an inclusive gateway:
  - the rows of "An absent variable refuses the step instead of taking the default" and "An absent variable refuses the inclusive fork", asserting `errors.Is(err, engine.ErrAbsentVariable)` and that no action is invoked;
  - "Negative control - a condition the gateway never evaluates is not refused";
  - the existing rows of "Exclusive gateway routing by condition and default" and "Inclusive fork branch selection", unchanged.

  Verify: `GOTOOLCHAIN=go1.26.9 go test ./engine -run 'AbsentVariable' -count=1` fails, and the absent rows take the default flow.
- [ ] 3.2 Green. Add `ErrAbsentVariable = expreval.ErrAbsentVariable` to `engine/errors.go` with godoc (D5). Turn the option on for the engine default evaluator (`engine/conditions.go`) and for `runtime.WithExpressionTimeout` (D2). State on `WithConditionEvaluator` that a consumer evaluator decides for itself. Add a `runtime` test that a `ProcessDriver` with no expression option fails `Drive` with `engine.ErrAbsentVariable` for the first row of "An absent variable refuses the step instead of taking the default". Verify: `GOTOOLCHAIN=go1.26.9 go test ./engine ./runtime -run 'AbsentVariable' -count=1` passes.

## 4. Fail the step on a boundary error-expression timeout

- [ ] 4.1 Red. Add a `runtime` table test over a `ProcessDriver` built with `WithExpressionTimeout(50ms)`, with rows for:
  - "Boundary outcome by evaluation failure", all six rows;
  - "A timed-out boundary does not fall through to an enclosing scope";
  - "Negative control - a boundary error expression that evaluates is decided by its result";
  - "Negative control - a prompt boundary error expression still matches under the opt-in timeout";
  - the "error boundary expression on a failing service task" row of "A runaway expression is abandoned under the opt-in timeout".

  The timeout rows assert `errors.Is(err, engine.ErrExpressionTimeout)`, that the fallback is not entered, and that the instance is not failed. Verify: `GOTOOLCHAIN=go1.26.9 go test ./runtime -run 'BoundaryErrorExpr' -count=1` fails, and the timeout rows route to the catch-all.
- [ ] 4.2 Green. Implement D4. `findDirectBoundary` returns a third value, the error, set only when `errors.Is(matchErr, ErrExpressionTimeout)`. Every other error keeps the Debug log and `continue`. Thread the error through `findEnclosingBoundary`, `propagateError` and the sub-instance-failed handler in `engine/step_triggers.go`, returning it before any token is consumed. Update the godoc of `boundaryErrorMatches` and `findDirectBoundary`. `TestMalformedErrorExprNonFatal` and `TestFindDirectBoundary_MalformedErrorExpr_LogsDebug` stay green unchanged. Verify: `GOTOOLCHAIN=go1.26.9 go test ./engine ./runtime -count=1` passes.
- [ ] 4.3 Refactor. Consider `/simplify` on `engine/step_errors.go` and the three call sites. Verify: the 4.2 command passes.

## 5. Integration

- [ ] 5.1 List every test in either module that goes red because it drives a condition without supplying the variable the condition reads (design, Risks). Give each one the variable; delete no test and change no other assertion. Verify: `GOTOOLCHAIN=go1.26.9 go test ./... -count=1` passes in the root module, and `(cd examples && GOTOOLCHAIN=go1.26.9 go test ./... -count=1)` passes.
- [ ] 5.2 Write the PR body's BREAKING section (the repository has no CHANGELOG file), marking the absent-variable refusal and the boundary timeout failure BREAKING, with the migration from design.md. Verify: the PR body names `engine.ErrAbsentVariable`, `engine.ErrExpressionTimeout`, the three migration options and the change name, and `scripts/lint.sh` passes.
- [ ] 5.3 Run the full gate. Verify: `openspec validate refuse-silent-expression-outcomes --strict` passes, `bash scripts/check-openspec.sh` prints ok, and the 5.1 commands pass with `-race`.

## Workflow follow-up

- Open the PR naming `refuse-silent-expression-outcomes`. Its last commit runs `openspec archive refuse-silent-expression-outcomes`.
- If `export-refusal-sentinels` archives first and modified "A condition that is not a valid boolean expression is an error", rebase this change's MODIFIED block on the archived text before archiving (design, Cross-change coupling).
- No GitHub intake issue to close.

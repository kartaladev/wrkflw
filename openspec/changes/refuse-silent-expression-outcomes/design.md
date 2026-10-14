# Design

## Context

See proposal.md (Why) for the three outcomes and where each one comes from. The constraints below shape the approach. Each was measured on `main` with expr-lang/expr v1.17.8, using code reads and a scratch probe outside the repository.

- `internal/expreval` is the only package that imports expr. Every evaluation passes a plain `map[string]any` and compiles with `expr.AllowUndefinedVariables()` and no `expr.Env`.
  - Probe: without `expr.Env`, adding or removing `AllowUndefinedVariables` changes nothing. `missing > 1` fails with `invalid operation: <nil> > int` either way, `status == "x"` returns `false` with no error either way, and `amount > 100 || missing > 1` returns `true` without reaching `missing`.
  - So expr's own strict mode is unreachable without `expr.Env`. `EvalBool` gets absent-means-false from two mechanisms: the `isNilOperandError` mapping, and nil comparing unequal to anything.
- Not using `expr.Env` is load-bearing. The no-shadowing barrier described at `boundaryErrorMatches` (`engine/step_errors.go`) exists only because there is no `expr.Env`, and `TestBuiltinsAreNotShadowableByEnvKeys` sweeps all 71 builtins to detect the barrier's loss. The program cache is keyed by the code string alone. kartaladev/wrkflw#186 records both hazards.
- Gateway-condition errors already surface as step errors. `selectExclusiveTarget` and `forkInclusive` (`engine/step_gateways.go`) wrap any `EvalBool` error as `workflow-engine: gateway %q flow %q: %w` and return it from `Step`. Through a `ProcessDriver` the call returns it: `TestProcessDriverWithExpressionTimeoutGuardsGateway` asserts this for the timeout. No expression error raises an incident today.
- Boundary error expressions are evaluated in `boundaryErrorMatches`, which is called only by `findDirectBoundary`. `findDirectBoundary` turns every evaluation error into a non-match with a Debug log and keeps scanning. `TestFindDirectBoundary_MalformedErrorExpr_LogsDebug` pins that log, and `TestMalformedErrorExprNonFatal` pins "a malformed ErrorExpr is non-fatal". It is reached from three places:
  - `propagateError` step 1 (action failure on the host);
  - `findEnclosingBoundary` (the scope walk);
  - the sub-instance-failed handler (`engine/step_triggers.go`, call-activity host).
- Other packages share `expreval.New()` for authorization attribute predicates: `authz/authz.go` with `authz/decider.go`, and `internal/authz/casbin/authorizer.go`. Both fail closed on an evaluation error, returning an error and not `ErrNotAuthorized`. So making `EvalBool` refuse everywhere would turn today's deny (403) into a 5xx.
- Package `engine` imports `internal/expreval`, so `expreval` cannot import `engine`. There is a precedent for re-exporting a sentinel value under a public name: `runtime/task.ErrTaskNotOpen = engine.ErrTaskNotOpen`.

## Goals / Non-Goals

**Goals:**
- Make the absent-variable refusal and the boundary timeout failure observable through the public sentinels in `engine`, at every site the specs name.
- Keep the engine's default evaluation free of wall-clock time and deterministic.

**Non-Goals:**
- Type-checking expressions, or any use of `expr.Env`.
- Changing authorization predicate semantics.
- Any change to non-timeout boundary evaluation errors beyond the new refusal joining them.

## Decisions

**D1. Detect absent variables statically, from the compiled program's free identifiers, before running it.**
At compile time, `expreval` walks the parsed AST once and records the free identifiers of the program. These are the identifier nodes that are not:
- builtins in call position;
- names bound by `let`;
- closure or predicate parameters (`#`, `.field` inside a predicate);
- member names after `.`.
The list is cached next to the `*vm.Program`. Before running, when the absent-variable check is on, `EvalBool` checks each free identifier against the env and returns `fmt.Errorf("%w: %q in %q", ErrAbsentVariable, name, code)` for the first one missing, in source order. The names are a pure function of the code string, so the code-keyed cache stays sound. That is the property kartaladev/wrkflw#186 says an `Env` would break.
- *Rejected: `expr.Env` plus strict mode.* The probe shows strict undefined-name checking needs an `Env`. An `Env` deletes the no-shadowing barrier, and `TestBuiltinsAreNotShadowableByEnvKeys` would go red. It also makes the program cache unsound (kartaladev/wrkflw#186).
- *Rejected: turn the `isNilOperandError` mapping into the refusal.* The probe shows `status == "x"` over an absent `status` returns `false` with no error, so this would leave the equality form silent. It would also misreport a null-valued variable that is present as absent (see Q3).
- *Rejected for now: `expr.Patch`, rewriting each free identifier into a checked fetch at runtime.* This is the mechanism to use if Q1 is answered "reaches at runtime", because it respects short-circuiting. But it adds a function name to every program's namespace, which the no-shadowing sweep must then be taught about. It costs a call per identifier read.
- Every spec row is chosen so that both readings of Q1 give the same outcome. D1 is the default pending Q1. If Q1 is answered "reaches", D1 switches to the `expr.Patch` mechanism, and the specs and the task list are unchanged except for the row Q1 adds.

**D2. The check is an evaluator option, on for the engine's evaluators and off for authorization.**
`expreval` gains an option, for example `WithAbsentVariableRefusal()`. The final name is the implementer's choice. Two evaluators turn it on: the engine's package default (`engine/conditions.go`) and the one `runtime.WithExpressionTimeout` builds. The `authz` and `casbinauthz` evaluators do not. A consumer evaluator passed to `WithConditionEvaluator` is the consumer's own and is not bound by the refusal. The godoc on `WithConditionEvaluator` says so.
- *Rejected: always on in `EvalBool`.* Authorization attribute predicates would change from a deny (403) to an evaluation error (5xx). The context above shows both deciders return errors, not `ErrNotAuthorized`. That is a change in the posture a consumer deploys, and the maintainer decided it for conditions only.
- *Rejected: the engine checks the env itself before calling `EvalBool`.* The engine cannot know a condition's identifiers without parsing it, and the `expreval` package doc makes `expreval` the single place that touches the expression vendor.

**D3. The refusal surfaces as a step error, not an incident.**
`ErrAbsentVariable` takes the path every gateway-condition error already takes. It is wrapped with the gateway and flow IDs and returned from `Step`, and through a `ProcessDriver` the call fails with it. Nothing is committed for that step.
- *Rejected: raise an `IncidentDefinitionDefect` on the gateway token.* No expression error raises an incident today, and neither does `ErrNoMatchingFlow`, which belongs to the same "definition or data cannot route" family. An absent variable is usually a data fault, not a definition fault, so an incident would send the operator to the wrong fix. Changing how routing errors surface belongs to its own change, covering `ErrNoMatchingFlow` too.

**D4. A boundary timeout fails the step; every other boundary evaluation error stays a skip.**
`findDirectBoundary` returns `(boundary, found, err)`. `err` is non-nil only when `errors.Is(matchErr, ErrExpressionTimeout)`. Every other error keeps today's Debug log and `continue`. `findEnclosingBoundary`, `propagateError` and the sub-instance-failed handler return that error as a step error before anything is consumed or routed, so the scan stops: no later boundary in this scope or an enclosing one is tried, and the instance is not failed. An absent variable in a boundary error expression is refused by D1 and then skipped by this rule. That is the same outcome as today (`false` was a non-match too), so boundaries see no observable change from D1.
- *Rejected: fail the step on every boundary evaluation error.* The maintainer's decision covers the timeout. For malformed predicates, the existing rule in `findDirectBoundary`'s godoc ("one malformed predicate must not brick routing for all boundaries") is a decided behavior pinned by two tests. Reversing it is listed as out of scope in the proposal.
- *Rejected: treat a timed-out boundary as a non-match and continue.* That is today's silent outcome, which the maintainer decided against. A timeout says nothing about whether the boundary matches, so continuing could route to a catch-all that the definition ranks below it.

**D5. The sentinels are declared in `internal/expreval` and re-exported as `engine.ErrAbsentVariable` and `engine.ErrExpressionTimeout`.**
`engine/errors.go` gains `ErrExpressionTimeout = expreval.ErrEvalTimeout` and `ErrAbsentVariable = expreval.ErrAbsentVariable`. The same value under two names makes `errors.Is` work whichever name a caller holds. In-module tests move to the `engine` names. Godoc on `ConditionEvaluator` tells consumer evaluators to wrap `engine.ErrExpressionTimeout` on a timeout, so D4 applies to them too.
- *Rejected: declare the sentinels in `engine` and translate at each call site.* There are 10 evaluator call sites in `engine`, and translation would also miss errors from consumer evaluators that wrap the internal value.
- *Rejected: a new public `expression` package.* It adds an import path consumers must learn for two values, when `engine` already holds the routing sentinels (`ErrNoMatchingFlow`).
- *Rejected: move `expreval` out of `internal`.* That would publish the vendor wrapper, which is internal on purpose so that the dependency stays swappable.

**Cross-change coupling.**
- `export-refusal-sentinels` (D7 finding 4) exports the remaining refusal sentinels. This change owns `engine.ErrExpressionTimeout`, `engine.ErrAbsentVariable` and the requirement "Expression evaluation is bounded on opt-in"; that change's proposal already says so and places its own expression sentinels in `engine` as re-exports of `internal/expreval` values, matching D5. Both changes edit `engine/errors.go` and `internal/expreval/expreval.go`, and may both MODIFY "A condition that is not a valid boolean expression is an error". The code merge is additive and either can land first, but whichever archives second must rebase its MODIFIED block on the first one's archived text.
- kartaladev/wrkflw#186 owns any future decision about `expr.Env`. D1 depends on there being no `Env`. If #186 later adopts `Env`, D1's free-identifier walk becomes redundant with strict mode, and #186's change must keep `ErrAbsentVariable` as the error it returns.

## Risks / Trade-offs

- [Definitions that relied on absent-means-false start failing steps after upgrade, including instances already in flight.] → The proposal marks this BREAKING. The PR body and the release notes for the next tag give the migration: supply the variable at start or from an upstream task, or gate on a variable that is always set. The error names the variable and the condition, so the failing definition is easy to find.
- [Tests across `engine` and `runtime` that drive gateways without supplying the condition's variable go red for reasons unrelated to what they test.] → Task 4.1 measures that set before the fix and gives each one the variable it reads. No test is deleted to make it pass.
- [A free-identifier walk that misclassifies a construct, such as a `let` name, a predicate field or a builtin, refuses a valid condition.] → Task 1.1's table covers each construct with a present-variable control, and the existing `internal/expreval` and engine suites run unchanged.
- [A boundary timeout now fails an action-failure step. Under a driver that redelivers the failure, a deterministically slow expression fails on every redelivery.] → This matches how gateway, duration and correlation-key timeouts behave today. The timeout guard is an opt-in for untrusted definitions, where failing loudly is the point.

## Migration Plan

- No persisted format changes, so rollback is a revert.
- Consumers on the default evaluator or `WithExpressionTimeout` get the refusal on upgrade. Consumers on `WithConditionEvaluator` with their own evaluator are unaffected until they adopt the option.
- Any `errors.Is(err, expreval.ErrEvalTimeout)` in this repository moves to `engine.ErrExpressionTimeout`. External consumers could not name the old value.

## Open Questions

These need the maintainer before apply. Each would add a spec row, but none changes the approach above, given D1's fallback, or the task breakdown. The specs deliberately contain no row that depends on them.

- **Q1. Static or runtime "reads".** Should `amount > 100 || missing > 1` with `amount = 150` be refused (it names `missing`) or return `true` (it never reaches `missing`)? D1 refuses it. The runtime reading lets a variable's absence depend on other variables' values, which is harder to reason about, but it matches expr's own evaluation order.
- **Q2. Explicit absence idioms.** Are `missing ?? 0` and `missing == nil` still legal ways to say "absent is fine"? D1 as written refuses them, because they name `missing`. Allowing them means the walk exempts the left operand of `??` and nil comparisons. It would also give authors a supported escape hatch for the BREAKING change.
- **Q3. Present-but-null and missing members.** When `{"amount": null}` is evaluated against `amount > 100`, or `{"order": {}}` against `order.amount > 1`, should the result stay `false` (today, through `isNilOperandError`) or become an ordinary evaluation error? Neither is an absent variable under D1, so neither produces `ErrAbsentVariable`. The question is only whether the nil-operand mapping survives.

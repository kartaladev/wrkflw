# Tasks

Every behavior task is test-first. Write the test, run it and watch it go red, then make it pass. A red test in groups 2 to 9 asserts two things: `errors.Is` against the new sentinel, which is red until the fix, and `err.Error()` equal to the exact message the current code prints, which must already be green before the fix and stay green after it (design D1). Each sentinel's godoc cites the capability spec it implements (`openspec/specs/<capability>/spec.md`), never a change path. Start Go work with the `cc-skills-golang:golang-how-to` skill. Table tests follow the `table-test` skill.

## 1. Message-preserving mark helper

- [ ] 1.1 Red: in a new package `internal/errmark`, write a table test for `Mark(sentinel, err)`. It covers: nil err returns nil; `Error()` equals `err.Error()`; `errors.Is` holds for both the sentinel and a sentinel inside err's chain; `errors.As` still reaches a typed error inside err (`*yaml.TypeError`-shaped); an outer `fmt.Errorf("x: %w", marked)` keeps both. Verify: `GOTOOLCHAIN=go1.26.9 go test ./internal/errmark/... -count=1` fails to compile or goes red.
- [ ] 1.2 Green: implement `Mark` with `Unwrap() []error` (design D1), with godoc on the single-`Unwrap` caveat. Verify: the 1.1 command passes.

## 2. definition-wire-format: ErrUnknownNodeKind and ErrUnknownKey

- [ ] 2.1 Red: a table test for scenarios *An unknown or retired kind name is refused*, *YAML refuses an unknown kind name* and *Negative control - a registered kind name is not refused*. It asserts `model.ErrUnknownNodeKind`, the named value, not `ErrKindNotRegistered`, and the unchanged messages `workflow-definition: unknown NodeKind name %q` and `workflow-definition: unknown node kind %q`. Verify: `GOTOOLCHAIN=go1.26.9 go test ./definition/model/... -run UnknownNodeKind -count=1` is red only on the sentinel assertions.
- [ ] 2.2 Green: declare `ErrUnknownNodeKind` and mark both decode sites. Verify: the 2.1 command passes.
- [ ] 2.3 Red: a table test for scenarios *An unknown key is refused in either format*, *The sentinel survives the bound on reported YAML field errors*, *Negative control - a defined key the kind never reads is not an unknown key*, *An unrelated unknown key is not reported as label* and *Any label key is refused* (the new "not ErrUnknownKey" line). Add the two D4 canaries pinning `json: unknown field ` and yaml.v3's `field <k> not found in type`. Verify: `GOTOOLCHAIN=go1.26.9 go test ./definition/model/... -run 'UnknownKey|RetiredLabel|KeyNotOnKind' -count=1` is red only on the `ErrUnknownKey` assertions.
- [ ] 2.4 Green: declare `ErrUnknownKey`. Mark the JSON decode error in `ProcessDefinition.UnmarshalJSON`, and mark the YAML `*yaml.TypeError` before `boundFieldErrors` trims it (design D4). Verify: the 2.3 command passes and `GOTOOLCHAIN=go1.26.9 go test ./definition/... -count=1` stays green.

## 3. expression-evaluation: compile, non-boolean and duration sentinels

- [ ] 3.1 Red: extend the expreval baseline table for scenarios *Invalid conditions are errors*, *Duration by result form* (the four refused rows), *Negative control - an unparseable duration is not a syntax error* and *Correlation key by result form* (the refused row). Assert through the `engine.` names, and assert each message unchanged. Verify: `GOTOOLCHAIN=go1.26.9 go test ./internal/expreval/... ./engine/... -run 'Baseline|Expression' -count=1` is red on the sentinel assertions only.
- [ ] 3.2 Red: an engine `Step` test for scenario *The sentinel reaches the caller of a gateway step*. Verify: `GOTOOLCHAIN=go1.26.9 go test ./engine/... -run GatewayConditionSentinel -count=1` is red.
- [ ] 3.3 Green: declare the three sentinels in `internal/expreval` and mark `compile`, the non-boolean branch of `EvalBool` and both `EvalDuration` refusals. Re-export them in `engine/errors.go` (design D2). Verify: the 3.1 and 3.2 commands pass and *A valid boolean condition evaluates* stays green.

## 4. expression-evaluation: expr validation predicate

- [ ] 4.1 Red: a table test for scenario *Validation outcome by input*, all five rows, asserting `expr.ErrPredicateNotSatisfied` on the three refused rows and both unchanged message forms. Verify: `GOTOOLCHAIN=go1.26.9 go test ./definition/model/validate/expr/... -count=1` is red on the sentinel assertions.
- [ ] 4.2 Green: declare `ErrPredicateNotSatisfied` and mark both refusal paths in `Validate`. Verify: the 4.1 command passes.

## 5. human-task-authorization: unevaluable attribute predicate

- [ ] 5.1 Red: extend the authz baseline test for scenarios *Unevaluable predicates fail closed as errors* and *A predicate evaluating false is still a denial*, asserting `authz.ErrPredicateNotEvaluable`, its absence on the false row, and the unchanged message. Extend the httpcore classifier test for *The HTTP response hides the predicate* with a marked error, which must still give 500 and an empty message. Verify: `GOTOOLCHAIN=go1.26.9 go test ./authz/... ./transport/http/httpcore/... -run 'Predicate|Unevaluable' -count=1` is red on the sentinel assertions.
- [ ] 5.2 Green: declare `ErrPredicateNotEvaluable` and mark the error in `AttributeDecider.Decide`. Verify: the 5.1 command passes.

## 6. event-delivery: unencodable payload

- [ ] 6.1 Red: extend the publisher table for scenario *Failure outcomes of a publish*: `eventing.ErrPayloadNotEncodable` with 0 PublishFunc calls, and its absence on the broker-error row. Verify: `GOTOOLCHAIN=go1.26.9 go test ./eventing/... -run Publisher -count=1` is red.
- [ ] 6.2 Green: declare `ErrPayloadNotEncodable` and mark the marshal error. Verify: the 6.1 command passes.

## 7. outbound-http-calls: hop limit and body validation

- [ ] 7.1 Red: tests for scenarios *Redirect destinations under the default client* (the loop row), *The hop limit refuses the 10th redirect* (1, 9, 10 and 11 hops) and *The hop-limit refusal keeps its retry classification*. They assert `ErrTooManyRedirects`, `action.IsRetryable` true, and the unchanged message. Verify: `GOTOOLCHAIN=go1.26.9 go test ./action/httpcall/... -run 'Redirect' -count=1` is red on the sentinel assertions only.
- [ ] 7.2 Green: declare `ErrTooManyRedirects`. Return it marked from `refuseUnsafeRedirect`. Add the D5 comment at `Do`'s redirect classification saying the hop limit stays retryable on purpose. Verify: the 7.1 command passes.
- [ ] 7.3 Red: a test for scenarios *Validator rejects the body* (the validator returns its own sentinel) and *Negative control - a passing validator sends the request*. Verify: `GOTOOLCHAIN=go1.26.9 go test ./action/httpcall/... -run BodyValidator -count=1` is red on `ErrBodyRejected`.
- [ ] 7.4 Green: declare `ErrBodyRejected` and mark the body-validation error inside `action.NonRetryable`. Verify: the 7.3 command passes.

## 8. token-routing-and-joins: unresolvable boundary target

- [ ] 8.1 Red: extend the boundary-target test for scenario *Unresolvable and blank-but-declared boundary targets*, asserting `engine.ErrBoundaryTargetNotFound` on both refused rows and the unchanged message. Verify: `GOTOOLCHAIN=go1.26.9 go test ./engine/... -run BoundaryTarget -count=1` is red on the sentinel assertions.
- [ ] 8.2 Green: declare `ErrBoundaryTargetNotFound` in `engine/errors.go` and mark the error in `step_boundaries.go`. Verify: the 8.1 command passes, and the blank-but-declared row stays green.

## 9. human-task-lifecycle: reassignment from a non-claimant

- [ ] 9.1 Red: turn the existing mismatch check in `runtime/task/service_test.go` into a table test for scenario *Source of a reassignment request* (all seven rows). Add *The refusal is not an authorization denial and keeps its HTTP status*: the authorizer is a recording fake that must not be called, and the httpcore classifier must give 500. Assert the unchanged message `workflow-runtime: reassign: from %q is not the current claimant %q`. Verify: `GOTOOLCHAIN=go1.26.9 go test ./runtime/task/... ./transport/http/httpcore/... -run 'Reassign|NotCurrentClaimant' -count=1` is red on the sentinel assertions.
- [ ] 9.2 Green: declare `task.ErrNotCurrentClaimant` and mark the `from` mismatch error in `TaskService.Reassign`. Verify: the 9.1 command passes.

## 10. Integration

- [ ] 10.1 Run the full suite and guards for both modules on the declared toolchain. Verify that each passes: `for pat in $(scripts/modules.sh); do GOTOOLCHAIN=go1.26.9 go test -race "${pat}"; done`, `scripts/lint.sh`, `scripts/check-doc-refs.sh`, `scripts/check-test-timeout.sh`.
- [ ] 10.2 Check the cross-change coupling (design, *Cross-change coupling*). If `refuse-silent-expression-outcomes` or `refuse-case-variant-json-keys` has archived first, re-copy the shared requirement blocks from `openspec/specs/` into this change's deltas and re-apply this change's edits. Verify: `openspec validate export-refusal-sentinels --strict` and `scripts/check-openspec.sh` pass, and the shared blocks in the deltas contain the other change's archived text.

## Workflow follow-up

- Open the PR naming `export-refusal-sentinels`. Its last commit runs `openspec archive export-refusal-sentinels`.
- No GitHub intake issue is cited, so no issue needs to be closed.

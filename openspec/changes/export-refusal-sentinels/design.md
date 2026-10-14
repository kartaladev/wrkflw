# Design

## Context

See proposal.md, *Why*. Each of the 12 refusals builds its error with `fmt.Errorf` or `errors.New`, or passes on a decoder's error, and none of them wraps an exported sentinel. The messages below were measured on main with go1.26.9. Rows marked *probe* ran a throwaway test against the real code. Rows marked *code* were read at the cited line. This change must leave every one of them byte-for-byte unchanged.

| Refusal | Raised at | Message today |
|---|---|---|
| unknown kind, JSON | `definition/model/nodekind_json.go` `UnmarshalJSON` | `workflow-definition: unknown NodeKind name "terminateEndEvent"` (probe) |
| unknown kind, YAML | `definition/model/yaml.go` `fromNodeYAML` | `workflow-definition: unknown node kind %q` (code) |
| unknown key, JSON | `encoding/json` `DisallowUnknownFields`, surfaced by `ProcessDefinition.UnmarshalJSON` | `json: unknown field "bogus_top"`, an `*errors.errorString` (probe) |
| unknown key, YAML | yaml.v3 `KnownFields(true)`, surfaced by `ParseYAML` | `workflow-definition: parse YAML: yaml: unmarshal errors:` followed by `line 3: field bogus_top_level not found in type model.definitionYAML` (probe) |
| compile error | `internal/expreval` `compile` | `workflow-expreval: compile "amount >": unexpected token EOF (1:8) ...` (probe) |
| non-boolean condition | `internal/expreval` `EvalBool` | `workflow-expreval: %q did not evaluate to bool (got %T)` (code) |
| bad duration result | `internal/expreval` `EvalDuration` | `workflow-expreval: "true" did not evaluate to a duration-compatible type (got bool)` (probe), or `... %q yielded string %q not parseable as duration: %w` (code) |
| `expr` predicate | `definition/model/validate/expr` `Validate` | `workflow-validation/expr: predicate "amount > 0": invalid operation: <nil> > int ...` for an absent variable (probe), or `... predicate %q not satisfied` (code) |
| attribute predicate | `authz/decider.go` `AttributeDecider.Decide` | `workflow-authz: attribute predicate: %w` (code) |
| payload marshal | `eventing/publisher.go` | `workflow-eventing: marshal payload: %w` (code) |
| hop limit | `action/httpcall/redirect.go` `refuseUnsafeRedirect` | `workflow-httpcall: stopped after 10 redirects`, inside `*url.Error` and `workflow-httpcall: request failed: %w` (code) |
| body validation | `action/httpcall/httpcall.go` step 4 | `workflow-httpcall: body validation: %w`, wrapped in `action.NonRetryable` (code) |
| boundary target | `engine/step_boundaries.go` | `workflow-engine: boundary %q: outgoing flow %q targets unknown node %q` (code) |
| non-claimant | `runtime/task/service.go` `Reassign` | `workflow-runtime: reassign: from %q is not the current claimant %q` (code; pinned by `runtime/task/service_test.go`) |

Three of these messages put a variable in the middle (the non-boolean condition, the boundary target, the non-claimant). Three sentinels must each cover two different messages (the kind, the duration, the predicate). Two messages are written by a dependency rather than by wrkflw (the JSON and YAML unknown key).

## Goals / Non-Goals

**Goals:**
- `errors.Is(err, <sentinel>)` holds at every site in the table, and through every wrapper that already uses `%w` on the way to the caller: `Step`, `ProcessDriver`, `ParseYAML`, `json.Unmarshal`, `*url.Error`, `action.NonRetryable`.
- `err.Error()` is unchanged at every site, and so is every `errors.As` target that works today (`*yaml.TypeError`, `*file.Error`, `*url.Error`, the validator's or PublishFunc's own error).

**Non-Goals:**
- Changing an HTTP status, a retry classification, or the content of any message.
- Making any sentinel reachable through a wrapper that flattens with `%s` today (`validation.Gate`; see Open Questions).

## Decisions

### D1. Attach the sentinel without changing the message: one internal helper

A new package `internal/errmark` provides `Mark(sentinel, err error) error`. It returns nil for a nil err. Otherwise it returns a value whose `Error()` is `err.Error()` and whose `Unwrap() []error` returns `{sentinel, err}`, so `errors.Is` and `errors.As` see both the sentinel and the original chain. Every site in the Context table marks its error this way. The YAML unknown-key site marks the `*yaml.TypeError` before `fmt.Errorf("...parse YAML: %w", ...)` wraps it.

Rejected:
- **`fmt.Errorf("%w: ...", ErrX, ...)`**, the convention elsewhere in the repo (for example `ErrEmptyReassignTarget`). It prepends the sentinel's text, so every message changes. That breaks the constraint that makes this change non-breaking.
- **Sentinel text equal to the message prefix, followed by `%w %q`.** The message comes out identical only where the variable part is a suffix. It fails for the three mid-message sites and for the three sentinels that cover two messages, and it does not apply to dependency-written messages at all. It would also tie each sentinel's text to a message, so rewording one would silently break identity.
- **Exported error types for `errors.As`.** No package in the repo exports refusal types; every one exports `Err*` variables. A type would also make the variable parts (the key, the predicate) part of the API.

### D2. Where each sentinel lives

- **Expression sentinels:** `ErrInvalidExpression`, `ErrConditionNotBoolean` and `ErrInvalidDuration` are declared in `internal/expreval`, where they are raised. `engine/errors.go` re-exports them as the same values, as `runtime.ErrDriverShuttingDown = kernel.ErrDriverShuttingDown` already does. `engine` imports `expreval`, so the reverse is a cycle. `refuse-silent-expression-outcomes` places `ErrExpressionTimeout` and `ErrAbsentVariable` the same way, so all five expression sentinels sit together in `engine`. `engine.ConditionEvaluator` is the contract a consumer implements, so `engine` is where a consumer looks for them.
- **Side effect, accepted:** the attribute decider shares `internal/expreval`. An attribute predicate that fails to compile therefore also matches `engine.ErrInvalidExpression`, as well as `authz.ErrPredicateNotEvaluable`. That is true and harmless, and no HTTP classifier arm tests either sentinel.
- **Every other sentinel** lives in the package that raises it: `model`, `expr` (the validation adapter), `authz`, `eventing`, `httpcall`, `engine` (the boundary target) and `runtime/task` (the non-claimant check).
- **Rejected:** `engine` for the non-claimant check, next to `ErrEmptyReassignTarget`. `ErrEmptyReassignTarget` is raised by both the engine and the task service. The `from` check exists only in `TaskService.Reassign`, and `engine.Step` never sees it.
- **Rejected:** reusing `model.ErrDanglingFlow` for the boundary target. That sentinel is documented as a `model.Validate` result, the authoring gate. A step-time failure that matched it would tell a consumer that validation rejected something it never ran.

### D3. Names follow each package's existing `Err*` patterns

| Sentinel | Pattern it follows |
|---|---|
| `model.ErrUnknownNodeKind` | `ErrUnknown<Thing>` (`engine.ErrUnknownTrigger`). It says `NodeKind`, not `Kind`, because `validate.ErrUnknownKind` already means an unknown *validation* kind. It is distinct from `ErrKindNotRegistered`, which refuses a known kind constant whose node type is not registered. |
| `model.ErrUnknownKey` | the key-refusal family `ErrKeyNotOnKind` and `ErrRetiredLabelKey`; `refuse-case-variant-json-keys` adds `ErrCaseVariantKey` beside it |
| `engine.ErrInvalidExpression`, `engine.ErrInvalidDuration` | `ErrInvalid<Thing>` (`ErrInvalidOutcome`, `ErrInvalidTransition`, `model.ErrInvalidRule`) |
| `engine.ErrConditionNotBoolean` | `Err<Thing>Not<State>` (`ErrKeyNotOnKind`, `ErrTaskNotOpen`) |
| `engine.ErrBoundaryTargetNotFound` | `Err<Thing>NotFound` (`ErrTokenNotFound`, `ErrStallIncidentNotFound`, `model.ErrCompensateRefNotFound`) |
| `expr.ErrPredicateNotSatisfied` | the phrase the message already uses ("predicate %q not satisfied"). It covers the absent-variable row too: a predicate that cannot run is not satisfied. |
| `authz.ErrPredicateNotEvaluable` | `authz.ErrSpecNotEvaluable` |
| `eventing.ErrPayloadNotEncodable` | `Err<Thing>Not<State>`; the requirement says "cannot be JSON-encoded" |
| `httpcall.ErrTooManyRedirects` | the redirect family (`ErrCrossHostRedirect`, `ErrDowngradeRedirect`), named for the count it enforces, as Go's own message is |
| `httpcall.ErrBodyRejected` | `httpcall.ErrBodyTooLarge` |
| `task.ErrNotCurrentClaimant` | the message's own phrase ("is not the current claimant"); it sits beside `task.ErrTaskNotOpen` |

Each sentinel's text uses its package's prefix (`workflow-definition:`, `workflow-expreval:` and so on). Under D1 that text never appears in a refusal message; it is what `%v` prints for the bare sentinel.

### D4. Recognizing the dependency-written unknown-key errors

- **JSON:** `ProcessDefinition.UnmarshalJSON` marks a `dec.Decode` error whose text starts with `json: unknown field `. encoding/json exports no type for it; the probe shows a plain `*errors.errorString`.
- **YAML:** `ParseYAML` marks a `*yaml.TypeError` when any entry matches `field <k> not found in type`. The check runs before `boundFieldErrors` trims the list to 20 entries, so an unknown key past the bound still marks the error. A TypeError that mixes unknown keys with type mismatches is marked, because it does carry an unknown key.
- **Canaries:** a canary test for each format pins the dependency's message shape. If an upgrade changes the shape, the test fails loudly, the same way `TestExprNilComparisonErrorShapeUnchanged` guards expr.
- **Rejected: a wrkflw-owned raw-key pass**, which would decode to a map and compare keys. It duplicates the exact-key check that `refuse-case-variant-json-keys` builds for JSON. If that change lands first, the JSON half of this decision should raise `ErrUnknownKey` from its pass instead of reading the message. That is a local change, and no spec changes.

### D5. Nothing is reclassified

No new sentinel is added to `httpcore.ClassifyError` or to a retry decision.
- The hop limit stays retryable. `Do` classifies only `ErrCrossHostRedirect` and `ErrDowngradeRedirect` as non-retryable, and a comment there will say the omission is deliberate pending a decision (Open Questions).
- The non-claimant and predicate refusals still fall to the 500 default arm. The specs pin both, so an accidental new arm turns a test red.

### D6. The hop-limit wording is corrected to what was measured

The baseline said "up to 10 hops". A stdlib probe reproduced `refuseUnsafeRedirect`'s check (`len(via) >= 10`) on go1.26.9. A chain of 9 same-host redirects returns 200; chains of 10 and 11 fail with `stopped after 10 redirects`. The MODIFIED requirement now reads "at most 9 redirects" and refuses "the 10th redirect". The behavior is unchanged; only the sentence now matches it.

## Cross-change coupling

- **`refuse-silent-expression-outcomes`:**
  - both changes MODIFY *A condition that is not a valid boolean expression is an error* in `expression-evaluation`;
  - both add re-exports to `engine/errors.go`;
  - both edit `EvalBool` in `internal/expreval/expreval.go`.
  Neither depends on the other. Whichever archives second must re-copy the requirement block from `openspec/specs/expression-evaluation/spec.md`, as the first archive left it, and re-apply its own edit before archiving; otherwise the second archive erases the first's text. Task 10.2 carries that check.
- **`refuse-case-variant-json-keys`:** both changes MODIFY *Unknown keys are refused at decode* in `definition-wire-format`, and both edit the error path of `ProcessDefinition.UnmarshalJSON`. The re-copy rule is the same, and D4 covers the code overlap.
- **kartaladev/wrkflw#115** (pending migration) reworks reassign *authority*. The `from` check in the new requirement is upstream of authorization and unaffected. Whoever converts #115 into a change must keep that ordering, or MODIFY this requirement.

## Risks / Trade-offs

- [Stdlib or yaml.v3 rewords its unknown-field message, for example under `encoding/json/v2`] → the D4 canaries fail on upgrade; then switch to the raw-key pass.
- [A multi-unwrap error confuses a consumer that walks the chain with the single-`Unwrap` loop] → `errors.Is` and `errors.As` (Go 1.20+) handle `Unwrap() []error`. A hand-written loop would stop at the mark, which happens for any `errors.Join` value too. Godoc on `errmark.Mark` says so.
- [A message silently drifts while the sentinel is added] → each red test asserts the exact pre-change message string, captured from the current code before the fix (see tasks).

## Migration Plan

None. The change is additive and has no persisted or configuration effect. Rollback is a revert.

## Open Questions

None of these changes this change's specs or tasks; each would be its own change.
- Should `validation.Gate` wrap the validator's error with `%w` instead of `%s`? `expr.ErrPredicateNotSatisfied` would then reach `ProcessDriver` and HTTP callers. The message would be identical, but callback validators' own errors would also become matchable.
- Should the hop-limit refusal become non-retryable like the other two redirect refusals? Should a non-claimant reassignment classify as a 4xx instead of 500?
- Should `refuse-case-variant-json-keys`'s `ErrCaseVariantKey` also match `ErrUnknownKey`? That change owns the answer.

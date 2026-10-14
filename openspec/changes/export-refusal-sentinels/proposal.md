# Proposal

## Why

The baseline specs (archived change `establish-baseline-specs`, design D3 and D7 group 4) record 12 decided refusals whose error the library does not export as a sentinel. The specs say "refused with an error naming <thing>". A consumer can only branch on these errors by matching message text, which the project does not promise. This change exports one sentinel per refusal. Every existing message stays byte-for-byte identical, so only `errors.Is` gains a new answer.

## What Changes

- Export a sentinel for each refusal below. Each is matched with `errors.Is`, and the refused error prints exactly what it prints today.

  | Refusal (capability) | Sentinel |
  |---|---|
  | unknown or retired node kind name, JSON and YAML (`definition-wire-format`) | `model.ErrUnknownNodeKind` |
  | definition key the wire format does not define, JSON and YAML (`definition-wire-format`) | `model.ErrUnknownKey` |
  | expression that does not compile: condition, duration or correlation key (`expression-evaluation`) | `engine.ErrInvalidExpression` |
  | condition whose result is not a boolean (`expression-evaluation`) | `engine.ErrConditionNotBoolean` |
  | duration expression whose result is not a duration (`expression-evaluation`) | `engine.ErrInvalidDuration` |
  | `expr` validation predicate that is false or cannot run (`expression-evaluation`) | `expr.ErrPredicateNotSatisfied` (package `definition/model/validate/expr`) |
  | attribute predicate that cannot be evaluated (`human-task-authorization`) | `authz.ErrPredicateNotEvaluable` |
  | event payload that cannot be JSON-encoded (`event-delivery`) | `eventing.ErrPayloadNotEncodable` |
  | redirect chain past the 10-hop limit (`outbound-http-calls`) | `httpcall.ErrTooManyRedirects` |
  | request body rejected by the body validator (`outbound-http-calls`) | `httpcall.ErrBodyRejected` |
  | boundary flow that targets an undeclared node (`token-routing-and-joins`) | `engine.ErrBoundaryTargetNotFound` |
  | reassignment whose `from` is not the current claimant (`human-task-lifecycle`) | `task.ErrNotCurrentClaimant` (package `runtime/task`) |

- The last row has no baseline requirement yet. D7 lists it, and `TaskService.Reassign`'s godoc documents it as a decided contract. It is ADDED to `human-task-lifecycle`; every other row MODIFIES an existing requirement.
- Retryability, HTTP status and every other observable outcome are unchanged:
  - the hop-limit refusal stays retryable;
  - the body-validation refusal stays non-retryable;
  - the predicate and non-claimant refusals still classify as HTTP 500.
- Not **BREAKING**: the change only adds sentinels and leaves every message as it is. Persisted formats: no impact; no message is persisted under a new shape. Default security posture: no impact; no classification changes. Consumer go.mod: no impact; no new dependency.
- No GitHub issue is absorbed. The source is D7 group 4 of the archived `establish-baseline-specs` design.

**Decisions this change is specified with** (design D1 to D4):
- Message preservation. A new internal helper attaches the sentinel without changing `Error()`. Prefixing the sentinel with `%w: ` is rejected because it changes every message.
- Expression sentinels live in `engine` as re-exports of `internal/expreval` values. This is the placement `refuse-silent-expression-outcomes` chose for `ErrExpressionTimeout` and `ErrAbsentVariable`.
- Names follow each package's existing `Err*` patterns (design D3).

**Owned elsewhere**
- The expression-evaluation timeout sentinel (`engine.ErrExpressionTimeout`), the absent-variable refusal for conditions (`engine.ErrAbsentVariable`) and the requirement *Expression evaluation is bounded on opt-in* belong to `refuse-silent-expression-outcomes`.
- The case-variant JSON key refusal (`model.ErrCaseVariantKey`) belongs to `refuse-case-variant-json-keys`. That change scopes out the plain unknown-key sentinel, which this change owns.

**Out of scope**
- Changing any HTTP status or retry classification. Examples are making the hop limit non-retryable, or mapping a non-claimant reassignment to a 4xx (design, Open Questions).
- Letting `validation.ErrInvalidInput` carry the validator's chain. Today the gate flattens it with `%s`, so `expr.ErrPredicateNotSatisfied` is reachable from the validator but not through a `ProcessDriver` (design, Open Questions).
- Refusals that the baseline did not record as "an error naming": the `expr` validation compile error, the httpcall URL-expression errors, the envelope-ID mint failure, and expression run errors that are neither a timeout nor an absent variable.
- kartaladev/wrkflw#115 (claimant-only tasks, pending migration). It changes who may reassign, not the `from` check this change names.

**Coordination**
- Size: M
- Depends on: establish-baseline-specs
- Blocks: nothing
- Readiness: agent

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `definition-wire-format`: *Node kinds are encoded by stable name*, *Unknown keys are refused at decode* and *The retired label key is refused with a pointer to name* name `ErrUnknownNodeKind` and `ErrUnknownKey`.
- `expression-evaluation`: *A condition that is not a valid boolean expression is an error*, *Duration expressions accept durations, numbers of seconds and duration strings*, *Correlation key expressions render to strings* and *The expr validation kind requires every predicate to hold* name their sentinels.
- `human-task-authorization`: *A predicate that cannot be evaluated is a failure, not a denial* names `ErrPredicateNotEvaluable`.
- `human-task-lifecycle`: an ADDED requirement refuses a reassignment whose `from` is not the current claimant with `ErrNotCurrentClaimant`.
- `event-delivery`: *Publish failures are returned to the caller* names `ErrPayloadNotEncodable`.
- `outbound-http-calls`: *The default client refuses cross-host redirects* names `ErrTooManyRedirects`, and *A failing body validator blocks the request* names `ErrBodyRejected`.
- `token-routing-and-joins`: *A boundary whose target names no node fails the step* names `ErrBoundaryTargetNotFound`.

## Impact

- New package `internal/errmark`: one helper that marks an error with a sentinel without changing its text.
- `definition/model`: `nodekind_json.go`, `yaml.go` and `node_wire.go` (decode paths), plus sentinel declarations in `validate.go`.
- `internal/expreval/expreval.go` (marking) and `engine/errors.go` (re-exports).
- `engine/step_boundaries.go`, `definition/model/validate/expr/expr.go`, `authz/decider.go`, `eventing/publisher.go`, `action/httpcall/redirect.go`, `action/httpcall/httpcall.go` and `runtime/task/service.go`.
- Godoc on each sentinel names the capability spec it implements.
- Tests: one red-first test per sentinel, asserting both `errors.Is` and the unchanged message, plus a negative control per row.

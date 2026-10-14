# Design

## Context

See proposal.md for motivation and the re-measured premises. The routes, their bodies and the disclosure category of each field are decided by kartaladev/wrkflw#29 (Q7 (a), A5 (a), A10). The route-table mechanism is decided by kartaladev/wrkflw#23 and built by `unify-http-routes-as-data`. This design covers how the three routes read and write through the library, and the questions the decision leaves open.

Measured on `b8576841`:
- Disclosure narrows only callers the resolver does not identify. `DiscloseAll` disables projection entirely and is not the union of the categories. `DiscloseOperations` does not restore incident error text unless `DiscloseVariables` is also set (`authz/disclosure.go:42-70`).
- The task POST routes resolve identity before reading the body and fail closed with 401 or 503 (`human-task-authorization` spec). The instance GET routes serve unidentified callers a structural projection (`instance-disclosure` spec).
- The error classifier's arms are ordered, and every new arm must state its position and carry a co-match test (`transport/http/httpcore/errors.go:139-149`).

## Goals / Non-Goals

**Goals:**
- Three routes, each defined once in the route table, with byte-identical answers on stdlib, gin and fiber.
- A read surface that answers "what is still missing" and "what can I submit now" without a second request.

**Non-Goals:**
- Any engine or store behavior. Those come from `add-user-task-updates`.
- `Cache-Control`. That belongs to `define-access-and-disclosure` (coupling below).

## Decisions

### D1. GET /tasks/{token} reads the instance snapshot, not the task row

The task row is a post-commit projection without updates (`add-user-task-updates` D10). `completable`, `missing_updates`, `pending` and `applied` need the snapshot's `HumanTask` and the definition's update declarations. So the handler resolves token → instance ID, loads the instance and its definition version, finds the task, and computes the view with `humantask.Completable`. The token → instance ID step uses the task row or a snapshot index, whichever the service facade already exposes. This is consistent with `authorize-task-triggers-in-driver` D2, where the snapshot decides.
- Rejected: reading only the task row. It cannot answer completability and may lag the commit.

### D2. `submittable` is one predicate, computed in the library

`submittable(u) = task open ∧ Pending == nil ∧ every dependency of u applied ∧ ¬(u.Resubmit == refuse ∧ u applied)`. It lives next to `Completable` in `humantask`, so a library consumer building its own UI gets the same answer as the route. It does not consult authorization: a caller who is not eligible still sees `submittable: true` and is refused 403 on submit. Answering per caller would turn the GET into an authorization oracle.

### D3. Request body rules

`update` is required. Absent, empty or unknown values are all `engine.ErrUnknownUpdate` → 400 (the core change's exact-name rule). `input` must be a JSON object. An absent `input` is `{}`, and any other JSON type is 400 `bad_request` from the decoder. An `actor` key in the body is ignored, as on every task route. Unknown keys follow whatever `unify-http-routes-as-data` does for every route; today they are accepted, as a finding with no owner.

### D4. Classifier arms

`engine.ErrUnknownUpdate` joins the 400 arm. `ErrUpdateInFlight`, `ErrUpdateDependencyMissing`, `ErrRequiredUpdatesMissing` and `ErrUpdateRefused` need no arm, because their `ErrInvalidTransition` wrapping reaches the existing 422 arm. A co-match test pins the 400 arm against the 422 arm for an error wrapping both. The 422 message renders `err.Error()`, which names the missing updates. That is engine-authored text with no consumer data, so it is safe to return.

### D5. History row projection reuses the instance categories

A history row maps its fields to categories exactly as #29 A10 says, with `reason` pending Q1. The recommended rule reuses `DiscloseOperations` ∧ `DiscloseVariables`, the existing incident-error gate. Then "consumer error text" has one rule across the library.

### Cross-change coupling

- `unify-http-routes-as-data`: must land first. These are three `Route` entries plus the parity table rows. `WithMiddleware` scoping, body-limit and trailing-byte rules come from it.
- `define-access-and-disclosure`: adds `Cache-Control: private, no-store` to every route that renders through the disclosure decision. The two task GET routes qualify. Whichever change lands second adds the directive to them, and its parity row.
- `add-user-task-updates`: provides the trigger, sentinels, `TaskUpdateReader`, `Completable`, and its Q1 ruling on the refuse response.
- `bound-untrusted-input`: the body cap and the variables ceilings apply to `input`, as they do to a completion output.

## Risks / Trade-offs

- [GET /tasks/{token} loads a whole instance snapshot per request] → It is the same cost as `GET /instances/{id}`, and the caching store serves it.
- [`submittable` is caller-independent, so a UI may offer a button that answers 403] → Decided in D2 against an authorization oracle. The 403 is immediate and changes nothing.
- [A long history on one task] → Q2 paging.

## Migration Plan

Additive routes. No consumer change is needed. A consumer that mounted its own `GET /tasks/{token}` on the same router path gets a conflict at mount time, which the route-table change reports as it does for any duplicate path.

## Open Questions

- **Q1. Which disclosure set restores a rejection `reason` to an unidentified caller.**
  - (a) `DiscloseOperations` alone, as #29 A10 wrote it.
  - (b) The incident-error rule: `DiscloseOperations` together with `DiscloseVariables`, or `DiscloseAll`.
  - (c) Never, except under `DiscloseAll`.

  The reason is the consumer's action error verbatim (#69) and may embed process variables. That is exactly why `incidents[].error` is gated on variables too. Option (a) would make `operations` restore error text on one route and not on another. **Recommendation: (b).** The requirement is written for (b) and marked pending.
- **Q2. Paging the history route.** #29 did not say.
  - (a) Unpaged: every row, ascending.
  - (b) `limit` and `cursor` with the same parsing, default and maximum as the instance list routes (`unify-http-routes-as-data` OQ2), ascending seq.

  Rows grow per submission, without bound under `replace` (`add-user-task-updates`, Risks). **Recommendation: (b).** The paging rows are marked pending.

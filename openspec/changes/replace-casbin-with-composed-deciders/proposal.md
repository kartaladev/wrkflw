# Proposal: the single-role actor and claimant-only tasks

## Why

kartaladev/wrkflw#114 (decided) settled human-task authorization: the consumer's translator delivers a complete actor, and authorization is a composite of single-purpose deciders over it. The composite shipped (PR #174), and `remove-casbin` deletes the old policy engine. What remains is the actor those deciders read and the ownership rule #114 asked for. The actor still carries a `roles` list and no on-behalf-of principal, which kartaladev/wrkflw#119 (decided) replaced with one active role and an `OnBehalf` principal. That must land before the first tag, or every released deployment writes `roles` arrays into `claim_actor`, `completion_actor` and `candidates`. And a claim is still advisory, so any eligible actor may complete a task someone else claimed; #114 adds opt-in claimant-only tasks with implicit claim.

Re-measured on `main` (`9c6dd2c1`, go1.26.9):

| Premise | Re-measurement |
|---|---|
| #115 scope comment: `OwnershipDecider` covers `OpComplete`, plus a fourth `ReassignDecider`, and `AuthzSpec` gains `ClaimantOnly` and `ReassignEligibility` | **Rulings hold; nothing is built.** `authz/decider.go` has only Privilege, Role and Attribute deciders. `AuthzSpec` has only `Roles`, `Privileges` and `Attribute`. |
| #115 115c: implicit claim needs no migration, `claimed_by` exists in all three dialects | **Holds.** `wrkflw_human_task.claimed_by` is in every `0001_init.sql`. `ClaimantOnly` and `ReassignEligibility` travel inside the existing `eligibility` JSON column. |
| #119: `Actor.Roles` is read at 24 production sites in 8 files | **Drifted.** 17 non-example lines in 9 files, plus 15 example files. casbin's sites are gone from the count. |

## What Changes

Decided and applied, not reopened: kartaladev/wrkflw#114 (complete actor; composed deciders; `claimant_only` with implicit claim; privileges request-time only) and kartaladev/wrkflw#119 (one active `Role`; principal on `OnBehalf`; deciders evaluate the principal; identity via `PrincipalID()`; `Actor.Validate()` with `ErrInvalidActor` classified as 400). The rulings recorded on #115 (no `OpProgress`; a separate reassign decider; `AuthzSpec` carries the ownership fields) also stand.

- **BREAKING (persisted format, flagged): the actor model.** `authz.Actor` becomes `{ID, Role, Privileges, Attributes, OnBehalf *Actor}`, and `Roles []string` is gone everywhere. New methods are `Principal()`, `PrincipalID()` and `Validate()`, plus a new sentinel `authz.ErrInvalidActor`. Persisted actor copies (`Claim.Actor`, `Completion.Actor`, in the snapshot, the journal and the task row) store the caller whole: `role` is a string, `on_behalf` is nested, and privileges are stripped at both levels. `candidates` are principals with no privileges and no `on_behalf`. No schema migration ships; only JSON contents change. Pre-change rows are pending Q4.
- **BREAKING: the deciders evaluate the principal.** `RoleDecider` checks `Principal().Role` against the spec's role set. `PrivilegeDecider` and `AttributeDecider` read `Principal()`. A predicate sees `actor` (the principal) and `caller` (the caller whole), so a predicate written against `actor.Roles` must be rewritten. Every identity comparison goes through `PrincipalID()`. That covers ownership, reassign authority, candidate matching, `ClaimableBy`, `claimed_by` and `completed_by`.
- **BREAKING (default security posture TIGHTENED, pending Q1 confirmation): actor validation.** An actor with an empty ID, or a malformed `OnBehalf`, is refused with `authz.ErrInvalidActor` (HTTP 400 `bad_request`) before authorization, at the driver seam `authorize-task-triggers-in-driver` creates. This retires the documented "kiosk claimant" `{ID:"", Roles:["kiosk"]}` (`humantask/validate.go:24`, `doc.go:151-156`). #119 decided `ID != ""` but did not weigh the kiosk.
- **New: claimant-only tasks with implicit claim.** `userTask` gains `claimant_only` (default false, which keeps today's advisory claim) and `reassign_eligibility` (shape pending Q2). Under the rule, only the claimant (by `PrincipalID()`) may complete. An eligible actor completing an unclaimed task implicitly claims it in the same step: the claim is recorded before the completion, `claimed_by` is set, and the journal holds one `HumanCompleted`. Reassign is allowed to the claimant, or to an actor satisfying `reassign_eligibility`. Registering a definition that sets `claimant_only` without `reassign_eligibility` logs a WARN. When two completions race on one unclaimed task, the instance CAS admits one.
- **Docs.** The `doc.go` `# Authorization` and `# Trust boundary` sections and the `authz` package doc state the #119 rule. The rule: *the role on the actor is the role the caller is exercising; if the translator sets `on_behalf`, it has decided the caller may act as that principal: the task sees the principal, the audit records the caller.* `cut-v0-release` collects the release-note lines.

Absorbed issues:
- kartaladev/wrkflw#115: absorbed except its casbin-removal part (115b), including the #119 amendment. Items 1 to 4 already shipped, so they are not re-specified. The removal part, item 8 and the `internal/atrest` paths moved to `remove-casbin`. This change closes #115.
- kartaladev/wrkflw#179: moved to `remove-casbin`, which supersedes it.
- kartaladev/wrkflw#114, #119: decision issues, already closed and cited as decided.

Decisions the specs depend on (design, Open Questions). Specs for them carry the recommended option, marked pending:
- **Q1** confirm retiring the kiosk claimant as a consequence of `Validate()`.
- **Q2** the wire shape of `reassign_eligibility`, and refusing it without `claimant_only`.
- **Q3** a resolver-supplied candidate carrying `OnBehalf`: strip it or refuse it.
- **Q4** how a persisted actor written before this change (a `roles` array) decodes.

Out of scope:
- Removing casbin, `PolicyAdmin` and the policy routes: kartaladev/wrkflw#115 (115b) and #179, owned by `remove-casbin`, which lands first.
- Moving authorization into `ProcessDriver.applyTrigger`, the nil-authorizer sentinel, `HumanCandidatesResolved.By` and the counter move: kartaladev/wrkflw#106, owned by `authorize-task-triggers-in-driver`, which lands first. This change calls `Validate()` in that seam, before its authorization call (design D2).
- The actor-remainder totality guard and the whole-actor round-trip conformance case: kartaladev/wrkflw#198, #201 and #202, owned by `guard-authz-field-totality`. This change settles #198's open question: both stores drop privileges.
- Rendering privileges on read routes, the facade's default authorizer, and inbox privilege matching: owned by `define-access-and-disclosure`.
- `OpProgress` and update rows that store the caller: kartaladev/wrkflw#41, owned by `add-user-task-updates`.
- The entry action and `on_failure`: kartaladev/wrkflw#121, owned by `add-user-task-entry-action`.
- Release notes: `cut-v0-release`.

The actor model and claimant-only tasks stay in one change, because both rewrite the persisted actor copies and the same decider files. The directory keeps its original name, `replace-casbin-with-composed-deciders`, although the casbin deletion itself is in `remove-casbin`.

**Coordination**
- Size: L
- Depends on: remove-casbin, authorize-task-triggers-in-driver
- Blocks: add-user-task-entry-action, cut-v0-release, `add-user-task-updates`
- Readiness: human

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `human-task-authorization`:
  - MODIFIED: privilege matching (no role satisfies a privilege, for the single `Role`).
  - MODIFIED: the attribute predicate constrains identity (the `actor` and `caller` bindings).
  - MODIFIED: task routes fail closed (an ID-less resolved actor answers 400, pending Q1).
  - ADDED: role eligibility tests the principal's one active role.
  - ADDED: deciders evaluate the principal.
  - ADDED: actor validation precedes authorization.
  - ADDED: claimant-only completion with implicit claim.
  - ADDED: reassign authority under the rule (pending Q2).
  - ADDED: identity comparisons use the principal ID.
- `human-task-lifecycle`:
  - ADDED: persisted actor copies keep the caller whole without privileges.
  - ADDED: the implicit claim is recorded with the completion.
  - ADDED: racing completions on one unclaimed task.
  - ADDED: resolved candidates are principals (pending Q3).
  - ADDED: pre-change actor rows (pending Q4).
- `definition-wire-format`:
  - ADDED: `claimant_only` and `reassign_eligibility` round-trip on userTask and are refused on other kinds.
- `definition-validation`:
  - ADDED: `reassign_eligibility` validation (pending Q2).
  - ADDED: the registration WARN.
- `http-transport`:
  - ADDED: `ErrInvalidActor` classifies as 400.
- `instance-disclosure`:
  - ADDED: the on-behalf principal is withheld unless actors are disclosed.

## Impact

- **Consumer go.mod:** no impact (the module removals are `remove-casbin`'s).
- **`authz`:** `authz.go`, `decider.go` and `composite.go`, plus new ownership and reassign deciders and `ErrInvalidActor`. This package is shared with `define-access-and-disclosure` and `guard-authz-field-totality`.
- **Definition:** `definition/activity` (`UserTask`, options), `definition/model` (`node_wire.go`, `node_wire_keys.go`, `validate.go`) and the registration path. These files are shared with `refuse-unrecognized-wire-values` and `refuse-case-variant-json-keys`.
- **Engine:** `engine/step_triggers.go` (the completion handler's implicit claim, and stripping). Runtime: `runtime/task/service.go` (request building, `Reassign`'s claimant check) and `runtime/processdriver*.go` (`Validate` in `applyTrigger` before the authorization call, stripping candidates). `applyTrigger` is created as an authorization seam by `authorize-task-triggers-in-driver`.
- **Stores:** `humantask/memory.go`, `humantask/humantask.go`, `internal/persistence/store/humantask_store.go` and `processtest/taskstoreconformance.go`. These are shared with `define-access-and-disclosure` and `guard-authz-field-totality`.
- **Transport:** `transport/http/httpcore` (`resolve_actor.go`, `seam.go`, `disclosure.go`, `errors.go`) and `runtime/view` (`OnBehalf` under actors).
- **Guards and fixtures:** `internal/transporttest/harness.go` and `processtest`.
- **Docs and examples:** `doc.go` (shared with `fix-doc-rot`, `define-access-and-disclosure` and `remove-casbin`) and every example that builds an actor.

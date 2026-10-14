# Design

## Context

See proposal.md (Why) for the motivation and the re-measured premises. The state that shapes the approach:

- **The port exists.** `authz.Request`, `Decision`, `Decider`, `SpecReader`, `Composite` and `NewComposite` (Privilege and Role identity deciders, Attribute constraint) are on `main` (`authz/authz.go`, `authz/decider.go`, `authz/composite.go`). `Composite` refuses with `ErrSpecNotEvaluable` when a set spec field has no decider. No ownership or reassign decider exists.
- **Authorization moves to the driver before this change.** On `main` it happens in `runtime/task.TaskService`, which reads the task record from the task store (`service.go:325-331`). kartaladev/wrkflw#106 (`authorize-task-triggers-in-driver`, a Depends-on of this change) moves it into `ProcessDriver.applyTrigger`, building the request from the loaded snapshot. This change builds on that seam.
- **casbin is gone before this change.** `remove-casbin` (a Depends-on) deletes `casbinauthz`, so no casbin code reads `Actor.Roles` when this change rewrites it.
- **The actor is persisted in three shapes:**
  - inside the instance snapshot (`HumanTask.Claim`, `Completion`, `Candidates`);
  - inside journal trigger payloads (`HumanClaimed.Actor`, `HumanCompleted.Actor`, `HumanReassigned.By`);
  - in the task row. There the identity columns `claimed_by` and `completed_by` sit beside JSON columns `claim_actor`, `completion_actor` and `candidates`. The SQL remainder codec enumerates fields by hand (`humantask_store.go:557-584`), which is why privileges are dropped today (#198).
- **Eligibility is stored whole.** It goes into the task row's `eligibility` JSON column and into the snapshot. That is why the ownership fields can ride inside `AuthzSpec` without a schema change.

## Goals / Non-Goals

**Goals:**
- Land the #119 actor model before the first tag, so no released row carries a `roles` array.
- Add opt-in claimant-only tasks with implicit claim and reassign authority. With the rule off, behavior does not change.

**Non-Goals:**
- Removing casbin (`remove-casbin`).
- Moving authorization into the driver (`authorize-task-triggers-in-driver`).
- A totality guard on the persisted actor remainder (`guard-authz-field-totality`). This change does keep the hand-written codec correct for the new fields.
- Any policy store, role hierarchy or wildcard expansion. #114 rejected them.

## Decisions

### D1. The actor model is #119's, applied whole (decided, kartaladev/wrkflw#119)
The model is `Actor{ID, Role, Privileges, Attributes, OnBehalf *Actor}`, with `Principal()`, `PrincipalID()` and `Validate()`. The deciders read `Principal()`. Predicates bind `actor` to the principal and `caller` to the caller.

Rejected on #119, with its reasons:
- an id-only principal, because its eligibility cannot be evaluated;
- a reserved attribute key;
- evaluating the caller's role with the principal's identity, under which delegation grants nothing;
- requiring both the caller and the principal to pass, which defeats delegation;
- changing the role model after the release, which rewrites every deployment's actor rows;
- dropping privileges.

### D2. `Validate()` runs in `applyTrigger`, immediately before the authorization call
#119 places `Validate()` "in `applyTrigger` before authorization (#106's seam)". `authorize-task-triggers-in-driver` lands first and creates that seam: `applyTrigger` authorizes `HumanClaimed`, `HumanCompleted`, `HumanReassigned` and `HumanCandidatesResolved`, and `TaskService` no longer authorizes. This change calls `Validate()` on the trigger's actor (`Actor`, `By`) in `applyTrigger` right before that call. One seam covers the HTTP path (the facade delivers `TaskService`'s trigger through `applyTrigger`) and a hand-built trigger alike, and validation precedes authorization, so an invalid and ineligible actor gets 400, not 403 (the spec scenario *Validation outranks the eligibility refusal*).

Rejected alternatives:
- **Also validating in `TaskService`.** A second site for the same rule, which is what #106 removed for authorization. It would only save the directory lookup `RefreshCandidates` makes before the trigger is applied, a cost `authorize-task-triggers-in-driver` already accepted for ineligible actors.
- **Landing this change first and validating at both of today's seams** (the original plan). It made `authorize-task-triggers-in-driver` rewrite this change's `TaskService` call, and it put the two changes in a Blocks cycle. Resolved: the driver seam lands first.

Journal replay does not go through `applyTrigger`. Old rows are therefore not re-validated; Q4 handles them.

### D3. The ownership rule rides in `AuthzSpec`, so the `eligibility` column carries it
`AuthzSpec` gains two fields:
- `ClaimantOnly bool`;
- `ReassignEligibility *AuthzSpec`, one level, whose own `ReassignEligibility` must be nil.

`UserTask` maps `claimant_only` and `reassign_eligibility` onto them when the task's eligibility is built. This follows #115 ruling 3.

Rejected alternatives:
- **New task-row columns.** They need a migration in three dialects, and #115 established that none is needed.
- **Reading the definition at authorization time.** #106's driver has only the snapshot (and `TaskService` only the task record, `service.go:325-331`).

The Composite's coverage check learns two new `SpecField`s (`claimant_only`, `reassign_eligibility`). A custom composite without the ownership deciders then gets `ErrSpecNotEvaluable` and does not silently skip the rule. The disclosure classification puts both fields under policy (spec: instance-disclosure).

### D4. Two single-purpose deciders, unit-tested directly
- **`OwnershipDecider`** is a constraint. It is `NotApplicable` unless `Spec.ClaimantOnly` and `Operation == OpComplete`. It returns `Allow` when the task is unclaimed, which is the implicit claim; the engine records it (D5). It also returns `Allow` when `Task.ClaimantID == PrincipalID()`, and `Deny` otherwise.
- **`ReassignDecider`** handles `OpReassign` on a claimant-only spec. The claimant gets `Allow`. Otherwise the request is decided against `ReassignEligibility` through the same identity and constraint stages, and the task's own eligibility is not consulted. With the rule off it is `NotApplicable`, which keeps reassignment authorized by eligibility as today.

Rejected alternatives:
- **Requiring task eligibility as well as reassign eligibility.** The supervisor row in the spec would then need the approver role, which defeats `reassign_eligibility`.
- **Folding both rules into one decider.** #115 ruling 2 kept them separate, because item 3 and item 6 conflict.

Per #115 ruling 6, every decider is tested on its exact `Decision` constant (refuse, accept, `NotApplicable`), not only through `Composite`. `Composite` cannot tell "no opinion" from "yes". `OpProgress` is not added (#115 ruling 1). `add-user-task-updates` adds it together with its `OwnershipDecider` arm.

### D5. The implicit claim is recorded by the engine's completion handler, in one step
When `HumanCompleted` reaches an unclaimed task whose `Eligibility.ClaimantOnly` is set, the handler records `Claim{Actor, At: t.OccurredAt}` and then `Completion`. That makes one journal row and one CAS, and replaying that row reproduces the claim.

Rejected alternative: `TaskService` emitting `HumanClaimed` and then `HumanCompleted`. That is two rows and two CAS writes, with a window where the claim is committed and the completion is not. It is also not what #114 described ("leaves one journal row, documented").

Concurrent completers are separated by the instance version CAS, as #114 recorded. Whether the losing call surfaces the store's conflict error or retries internally is measured by the race test. The spec pins only the observable result: one winner, and a retry gets `ErrTaskNotOpen`.

### D6. One helper each for principal identity and for the recorded copy
- `PrincipalID()` is the only way identity is compared. A grep-shaped guard test (#119 A3) asserts that `authz`, `engine`, `runtime/task`, `humantask` and `internal/persistence/store` compare no `.ID` of an actor except through it. That covers `Reassign`'s `from == claimant` check (`service.go:219-235`), `ClaimableBy`, `AssignedTo` and the `claimed_by` and `completed_by` writes.
- An `Actor.Recorded()` helper (exact name settled in code) returns the caller with `Privileges` cleared at both levels. It is applied in the engine's claim, completion and reassign handlers and in candidate resolution. Candidates additionally carry no `OnBehalf` (Q3).

Rejected alternative: stripping in each store's codec. The snapshot and the journal would still carry privileges, and the two task stores would disagree, which is #198's defect.

### D7. `ErrInvalidActor` classifies 400 `bad_request`
`ErrInvalidActor` classifies as 400 `bad_request`, as decided on #119, using the existing code with no new envelope code. The classifier arm sits beside `ErrBadInput`. The error text names the broken rule, not the actor's attributes.

## Risks / Trade-offs

- **Consumers lose the kiosk claimant** (Q1). → Mitigation: documented in `doc.go` and the release notes. The consumer's translator gives the kiosk a stable ID, for example the device ID.
- **Predicates written against `actor.Roles` fail at evaluation.** That is a 500 with no message, not a 403. → Mitigation: the failure is loud, as intended (spec: *Attribute predicates bind the principal…*). The release-note line says how to rewrite them.
- **Development databases holding pre-change rows fail to load** if Q4 is answered as recommended. → Mitigation: nothing is released yet. The error names `roles`, so the fix is obvious: recreate the dev database.
- **The L size makes review hard.** → Mitigation: the casbin removal is already split out (`remove-casbin`). Land as separate commits in task order: the actor model, then validation, then ownership.

## Migration Plan

For consumers (release notes, owned by `cut-v0-release`):
1. Replace `Roles: []string{...}` with `Role: "..."`, the role being exercised. Set `OnBehalf` only when the translator has decided that the caller may act for that principal.
2. Rewrite predicates: `actor` is the principal, `caller` is the caller, and `actor.Role` is a string.
3. Give the kiosk actor an ID.
4. Opt in to `claimant_only` per task. Add `reassign_eligibility`, or accept the registration WARN.

Rollback is a revert of the change. No schema migration ships, so there is nothing to undo in a database. Rows written under the new model would not decode under the old one, because `role` is unknown to the old `Roles` field. That is acceptable before the tag.

## Cross-change coupling

| Change | Coupling | Owner and order |
|---|---|---|
| `remove-casbin` (#115 115b, #179) | `authz` package doc, `doc.go`, examples; casbin read `Actor.Roles` | That change lands first (Depends on), so no casbin code is migrated to the new actor |
| `authorize-task-triggers-in-driver` (#106) | `applyTrigger`; it creates the authorization seam and `HumanCandidatesResolved.By`; this change inserts `Validate()` before the authorization call and covers `By` | That change lands first (Depends on); its own Blocks lists this change |
| `guard-authz-field-totality` (#198, #201, #202) | `humantask_store.go`'s actor remainder, `processtest/taskstoreconformance.go`, `authz` `SpecField` coverage | This change settles #198 as "both stores drop privileges". The totality guard is written against the post-change remainder (`Role`, `Attributes`, `OnBehalf`). Either order works; whoever lands second rebases |
| `define-access-and-disclosure` | `authz` package, `ClaimableBy` (privileges there, principal here), `runtime/view` (privileges stripped on read there, `OnBehalf` under actors here), `doc.go` `# Trust boundary` | No ordering dependency. Shared files rebase |
| `add-user-task-entry-action` (#121) | Needs `Principal()` and the one-role model | Blocked by this change |
| `add-user-task-updates` (#41) | `OpProgress` and its `OwnershipDecider` arm; update rows reuse D6's recorded-copy helper | That change owns them |
| `refuse-unrecognized-wire-values`, `refuse-case-variant-json-keys` | `definition/model/node_wire.go` and `node_wire_keys.go` | Shared file. The new keys follow whichever decode rules are on `main` |
| `fix-doc-rot` | `doc.go` | Either order |
| `cut-v0-release` (#48) | Release-note lines from the Migration Plan | Blocked by this change |

## Open Questions

These need a maintainer ruling. The specs carry the recommended option, marked "pending".

- **Q1. Confirm retiring the kiosk claimant.** #119 decided that `Validate()` requires `ID != ""`. The thread never mentions the shipped, documented kiosk claimant `{ID:"", Roles:["kiosk"]}` (`humantask/validate.go:24`, `httpcore/resolve_actor.go:213`, `doc.go:151-156`). The options:
  - (a) apply #119 as written: the kiosk is refused with `ErrInvalidActor` (400) everywhere;
  - (b) keep ID-less actors legal and refuse them only under `claimant_only` (#114 item 4's narrower rule);
  - (c) as (a), plus documented guidance to give a kiosk a device ID.

  **Recommendation: (c).** It is the decision as written. An unattributable actor cannot satisfy `PrincipalID()`-based identity anyway, and #114's audit already found the anonymous claimant to be a bypass under the rule. This is listed only because a decided rule removes a deliberately shipped behavior that the decision did not weigh.
- **Q2. The wire shape of `reassign_eligibility` and its validation.** The options:
  - (a) an object `{roles, privileges, expr}` that mirrors the eligibility triple, with the `ErrRolesAndPrivileges` rule. An empty object, or one present without `claimant_only`, is refused with a new `model.ErrInvalidReassignEligibility`.
  - (b) flat keys (`reassign_roles`, `reassign_privileges`, `reassign_expr`);
  - (c) as (a), but silently ignored without `claimant_only`.

  **Recommendation: (a).** It keeps the one key name #115 chose. An empty spec means allow-all, so accepting `{}` would let any actor reassign: a fail-open. Under (c) the key would be accepted and do nothing, which the project forbids.
- **Q3. A resolver-supplied candidate carrying `OnBehalf`.** The options:
  - (a) fail the resolution with `ErrInvalidActor`;
  - (b) strip `OnBehalf` silently, as privileges are stripped.

  **Recommendation: (a).** Privileges are stripped because they are legitimate request-time facts. An `OnBehalf` on a candidate is a resolver defect: a candidate is by definition a principal. Stripping it would hide that defect.
- **Q4. How a persisted actor written before this change decodes.** #119 decided that no migration ships, since nothing is released. The options:
  - (a) refuse a recorded actor carrying the retired `roles` key with an error naming `roles`. No sentinel exists yet; exporting one, for example `authz.ErrLegacyActor`, is recorded as a finding.
  - (b) ignore the key, so the actor decodes with an empty role;
  - (c) migrate a single-element `roles` to `role`.

  **Recommendation: (a).** Under (b) a role-bearing claimant silently becomes role-less, which is the decay the project forbids. Under (c) the library would write a migration for data that no release ever wrote.

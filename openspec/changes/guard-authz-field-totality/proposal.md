# Proposal

## Why

Two hand-written field lists decide security-relevant behavior, and no test ties either one to the struct it describes. The composite authorizer's coverage check lists three `AuthzSpec` fields by hand. A fourth field would not participate in authorization, and a spec setting only that field would be allowed. The SQL task store's actor encoding also lists actor fields by hand. It already disagrees with the in-memory store about `Actor.Privileges`. Nothing fails when the next actor field is added, and `replace-casbin-with-composed-deciders` is about to add `Role` and `OnBehalf`.

Re-measured on `main` at `9c6dd2c1` by reading the code. No probe was run, so the first task of each defect is the red test that proves it.

| Premise | Measurement on main |
|---|---|
| #202: `requiredFields` lists `Roles`, `Privileges`, `Attribute` by hand as three `if` statements, and nothing ties the list to `AuthzSpec` | **Holds.** `authz/composite.go:194-208`. The issue's corrected citation `:196` holds. `SpecField` has exactly three constants (`authz/authz.go`, `FieldRoles`, `FieldPrivileges`, `FieldAttribute`). No test enumerates `AuthzSpec`'s fields. The issue's reproduction (add `Groups`, get `nil`) is a **hypothesis** until task 1.1 runs it. |
| #198: the SQL store's claim and completion actor remainder lists `Roles` and `Attributes` by hand, so `Privileges` is dropped | **Holds.** `htActorRemainder` is at `internal/persistence/store/humantask_store.go:557-560`, `htMarshalActorRemainder` at `:565-567`, `htDecodeActor` at `:573-584`. The doc comment at `:552-556` still says "everything an `authz.Actor` carries except its ID", which is false. |
| #198: `MemTaskStore` round-trips privileges | **Holds.** `Actor.Clone` clones `Privileges`. |
| #198: the conformance suite checks the claimant ID, not the actor's contents | **Holds.** `processtest/taskstoreconformance.go` compares claim presence and ID. Its probe actor carries no privileges. |
| #198 (not in the issue): the two stores also agree on one column | **New finding.** The SQL `candidates` column is `json.Marshal(t.Candidates)` (`humantask_store.go:161`), so it **persists** each candidate's privileges. Claims and completions drop them. The SQL store is inconsistent with itself as well as with the memory store. |
| #201: `internal/atrest` classifies columns and cannot see a privilege nested in a JSON column | **Stale.** Since #52 (PR #192), `internal/atrest` parses migration DDL and nothing more. Its package doc says "nothing assigns sensitivity to a column any more". There is no at-rest classification guard left whose green run could be misread as coverage. |
| #201: the at-rest exposure is limited to `MemTaskStore` | **Stale, and understated.** Privileges reach durable storage today through the SQL `candidates` column, the instance snapshot (`marshalSnapshot` is `json.Marshal` of `InstanceState`, whose `Tasks` carry `Claim`, `Completion` and `Candidates` actors) and the journal (`trigger_codec.go:67-71` encodes the trigger's `actor`, `by` and `candidates`). |

## What Changes

The target state for privileges at rest is decided, and this change applies it rather than reopening it: kartaladev/wrkflw#114 item 5 (decided) says persisted actor copies drop privileges. The #119 amendment (decided) recorded on kartaladev/wrkflw#115 keeps that rule at both levels of the actor. So #198's "both persist or both drop" question is closed: **both drop**.

- **Composite fails closed on every spec field.** The coverage check derives the spec fields from `AuthzSpec` itself instead of a hand list. A set field that maps to no `SpecField` is still required, so no decider covers it and the composite refuses with `authz.ErrSpecNotEvaluable`. A totality test fails when an `AuthzSpec` field is added without a `SpecField` constant, or a constant is left behind by a removed field. This adds no new behavior for today's three fields.
- **Task stores agree on actors, and keep no privileges.** Every shipped `humantask.TaskStore` (memory, and SQL on Postgres, MySQL and SQLite) returns each stored actor equal to the actor written, minus `Privileges`. This covers the claimant, the completer and every candidate. A row written by an earlier version that carries privileges reads back without them.
- **The actor encoding is total.** The SQL actor remainder covers every `authz.Actor` field except the ID, which has its own column, and the stripped privileges. A totality test fails when an `Actor` field is added and nobody decided whether it is persisted or stripped. The conformance suite compares whole actors, not IDs.
- **BREAKING (behavior).** `MemTaskStore` no longer returns privileges on a stored claim, completion or candidate. SQL stores no longer return privileges on candidates. A consumer that read a privilege back from a task store loses it. Privileges were always meant to be request-time only (#114 item 5).
- **Persisted format: changes.** New `candidates` rows stop carrying `privileges`. The `claim_actor` and `completion_actor` columns stay byte-identical for today's fields. No migration and no column change. Older rows still decode.
- **Default security posture: tightened, slightly.** A future `AuthzSpec` field fails closed instead of open. Privilege tokens stop accumulating in task-store rows.
- **Consumer go.mod: no impact.**

Absorbed issues:
- kartaladev/wrkflw#202: absorbed. Its corrected line citation holds. Its reproduction stays a hypothesis until the red test runs.
- kartaladev/wrkflw#198: absorbed. Its open question is superseded by #114 item 5 (both drop). It is widened by the new finding about the `candidates` column.
- kartaladev/wrkflw#201: **found stale on re-measurement.** The guard it describes no longer exists. The real concern is privileges at rest, and this change covers the task-store part of it. The snapshot and journal parts belong to `replace-casbin-with-composed-deciders` (see Out of scope).

Out of scope:
- Stripping privileges from the instance snapshot, the journal's trigger rows, and `resolveHumanCandidates`: #115 item 7, owned by `replace-casbin-with-composed-deciders`. Item 7 names "the engine handlers and `resolveHumanCandidates`". It does **not** name the journal trigger codec, which persists the request-time actor today. That change must decide whether journal rows are persisted copies (design, coupling).
- The `Actor` reshape (`Roles` → `Role`, `OnBehalf`), and new deciders: kartaladev/wrkflw#115, owned by `replace-casbin-with-composed-deciders`; the casbin removal (#115 in part, and #179) is owned by `remove-casbin`. Both totality guards trip on that change. They do not decide its classifications.
- Rendering privileges through the disclosure projection, and `ClaimableBy` learning privileges: kartaladev/wrkflw#199, owned by `define-access-and-disclosure`, and #200, owned by `surface-claimable-privileged-tasks`.
- An at-rest sensitivity classification: retired by #52. This change does not reintroduce one.

**Coordination**
- Size: M
- Depends on: nothing
- Blocks: nothing
- Readiness: agent

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `human-task-authorization`:
  - MODIFIED: "A spec field no decider evaluates is refused as misconfiguration" now covers every field the eligibility spec declares, including one with no named spec field (#202).
- `human-task-lifecycle`:
  - ADDED: task stores return stored actors whole and without privileges, with parity across memory and the three SQL dialects (#198, #114 item 5).
  - ADDED: rows written before the strip read back without privileges (#198, #201).

## Impact

- `authz/composite.go` (`requiredFields`, `checkCoverage`) and `authz/authz.go` (the `SpecField` vocabulary and its godoc). Tests in `authz/`.
- `internal/persistence/store/humantask_store.go`: the actor remainder, the candidates encoding, and the remainder's doc comment. `humantask/memory.go`: the strip on write and read. `humantask/humantask.go`: the `HumanTask`, `Claim` and `Completion` godoc, which promises "the full `authz.Actor`".
- `processtest/taskstoreconformance.go`: whole-actor round-trip cases with a privileged probe. The SQL conformance runs on all three dialects.
- Shared files: `humantask_store.go` and `humantask/memory.go` are also edited by `surface-claimable-privileged-tasks` (#200, `ClaimableBy`). `authz/authz.go` and the `Actor` struct are also edited by `replace-casbin-with-composed-deciders`. Landing order is in design.

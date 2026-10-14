# Proposal

## Why

A human task whose eligibility names only privileges can be claimed by a principal holding one of them, yet no inbox path ever shows it to that principal. `ClaimableBy` and `humantask.StaticActorResolver` match candidates and roles but ignore privileges, so authorization admits what discovery hides (kartaladev/wrkflw#200). This change specifies the recommended option, marked pending, so the maintainer rules once.

This change was split from `define-access-and-disclosure`. Its open question keeps its pre-split number, Q3.

Re-measured on `main` at `9c6dd2c1` (go1.26.9). The probe ran from the scratchpad through `go test -overlay`, so nothing was written to the repository.

| Premise | Measurement on main |
|---|---|
| #200: `ClaimableBy` (memory `humantask/memory.go:87-111`, SQL `internal/persistence/store/humantask_store.go:241`) and `StaticActorResolver.Candidates` ignore privileges | **Holds** (memory `:94-111`, resolver `:137-151`, SQL `:236-256`). Probe on a privileges-only task: `ClaimableBy` → 0, `authz.NewComposite().Authorize` → nil (allowed), static resolver → 0 candidates. |

## What Changes

- **Inbox parity (pending Q3).** `ClaimableBy` on the memory, Postgres, MySQL and SQLite task stores lists an unclaimed task whose eligibility privileges the actor holds, by exact string. `humantask.StaticActorResolver` returns mapped actors granted a spec privilege. Authorization and discovery then agree.
- Not BREAKING: listings only grow, for tasks an actor could already claim.
- Default security posture: **unchanged.** Discovery widens to what authorization already admits; no claim is newly allowed.
- Persisted formats: **no change.** Q3 reads the existing `eligibility` column, which already round-trips privileges.
- Consumer go.mod: no impact. No new dependency.

Absorbed issues:
- kartaladev/wrkflw#200: absorbed. It reproduces.

Decisions the specs cannot be final without (design, Open Questions). No maintainer ruling is recorded on this thread.
- **Q3** (#200) whether the inbox learns privileges, and the resolver's scope.

Out of scope:
- The SQL task store dropping `Actor.Privileges` on actor copies, and the composite's field totality: kartaladev/wrkflw#198, #201 and #202, owned by `guard-authz-field-totality`. Coupled: same two store files (design D6).
- `ClaimableBy` matching on the principal, and the composite's decider list: kartaladev/wrkflw#115, owned by `replace-casbin-with-composed-deciders`. Shared files rebase.
- Rendering privileges and the facade's default authorizer: kartaladev/wrkflw#199 and #176, owned by `define-access-and-disclosure`.
- Extending the `ActorResolver` contract for directory-backed resolvers (Q3 option (c)).

**Coordination**
- Size: S
- Depends on: `establish-baseline-specs`
- Blocks: nothing
- Readiness: human

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `human-task-lifecycle`:
  - ADDED: the claimable list honours eligibility privileges on every task store (#200, pending Q3).
  - ADDED: the static resolver returns actors granted a spec privilege (#200, pending Q3).

## Impact

- `humantask/memory.go`, `internal/persistence/store/humantask_store.go`, `persistence/caching_task_store.go`: `ClaimableBy` and `StaticActorResolver` (Q3). The two store files are shared with `guard-authz-field-totality` (#198) and `replace-casbin-with-composed-deciders`.

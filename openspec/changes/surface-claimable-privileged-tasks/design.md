# Design

## Context

See proposal.md (Why) for the motivation and the re-measurement. This change was split from `define-access-and-disclosure`; its decision and open question keep their pre-split numbers, D6 and Q3. The constraints that shape this design are:

- **Privilege eligibility matches by exact string** (`human-task-authorization`). The default composite authorizer already admits a privileges-only task for a principal holding the privilege.
- **Both task stores filter in Go.** The SQL store runs `state = ?` and filters eligibility in Go after the query, as the memory store does.
- **Candidates are a projection, not an ACL** (`runtime/task/service.go:274`).

## Goals / Non-Goals

**Goals:**
- Make discovery agree with authorization for privileges-only tasks.
- Keep the memory store and the Postgres, MySQL and SQLite stores in parity.

**Non-Goals:**
- No change to the `ActorResolver` interface.
- No change to how actor copies store privileges (`guard-authz-field-totality`, #198).

## Decisions

### D6. Inbox privilege matching reuses the decider's rule (pending Q3)

`ClaimableBy` adds an exact-string privilege overlap, the same rule as `PrivilegeDecider` (`human-task-authorization`, "Privilege eligibility matches by exact string"). It does this in the Go filter that both stores already apply after the SQL `state = ?` query, so no SQL text, migration or dialect divergence is added. `StaticActorResolver` scans its mapped actors for a granted spec privilege. The `ActorResolver` interface is unchanged. A directory-backed resolver that cannot enumerate by privilege may return no candidates, because candidates are a projection, not an ACL (`runtime/task/service.go:274`).

**Coupling:** `guard-authz-field-totality` (#198) fixes the SQL store's privilege handling on actor copies, in the same two files. This change reads only the task's `eligibility` column, which already round-trips privileges (`humantask_store.go:157`). Either can land first; whichever lands second rebases. `replace-casbin-with-composed-deciders` adds principal matching to `ClaimableBy` in the same files, with no ordering dependency. The scenario "The listing agrees with the default composite authorizer" names `authz.NewComposite()`, not its decider list, so it follows whatever that change makes the composite.

## Risks / Trade-offs

- [A directory-backed resolver still returns no privilege candidates.] → Candidates are a projection; the claimable list, which this change fixes, is the inbox path that reads the actor's own grants.

## Migration Plan

No migration. Rollback is a code revert.

## Open Questions

No maintainer ruling is recorded on this thread. The question changes both pending-marked requirements, so `Readiness: human`, and apply waits for the ruling.

- **Q3 (#200): defect or feature, and resolver scope.**
  - (a) Teach the stores and `StaticActorResolver`.
  - (b) Document the gap only.
  - (c) Also extend the `ActorResolver` contract.
  - **Recommend (a).** Authorization and discovery should agree, and the stores already filter in Go. (c) is premature against no real directory consumer.

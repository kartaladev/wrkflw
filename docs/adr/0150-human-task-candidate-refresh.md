# 150. Human-task candidate refresh

Status: Accepted — 2026-07-27. Spec:
[docs/specs/2026-07-27-processinstance-audit-view-and-idgen.md](../specs/2026-07-27-processinstance-audit-view-and-idgen.md).
Extends [ADR-0147](0147-humantask-audit-model.md), which introduces the
`HumanCandidatesResolved` trigger this operation reuses.

## Context

[ADR-0147] resolves a task's eligible actors **once**, when the task is created,
and stores them on the task as `Candidates []authz.Actor`. The actor registry
behind an `ActorResolver` is not static: people join and leave roles, and a human
task can sit open for days. A candidate list minted at creation therefore drifts.

How much that matters depends on which mechanism is reading it, and the three are
easily conflated:

1. **Authorization is already live.** `TaskService.Claim` and `Complete` call
   `authz.Authorize(ctx, task.Eligibility, actor, task.Vars)` against the stored
   spec — never against `Candidates`. An actor who becomes eligible after task
   creation can already act on the task. There is no security exposure here.
2. **Inbox routing is half live.** Both `ClaimableBy` implementations
   (`humantask/memory.go`, `internal/persistence/store/humantask_store.go`) match
   `candidateContains(t.Candidates, actor.ID) || hasRoleOverlap(actorRoles,
   t.Eligibility.Roles)`. The role-overlap arm reads the actor's *current* roles,
   so someone who gains a role sees the task immediately, unrefreshed.
3. **The rendered list is fully stale.** The instance view's `candidates` — the
   whole point of ADR-0147 — shows the creation-time set, including actors who
   have since left the role.

So the gap is narrow but real: the displayed list, and eligibility that is not
role-based (`Attribute` predicates, `Privileges`), where only the enumerated IDs
match.

ADR-0147's trigger already carries exactly the needed semantics — it replaces the
list wholesale, is idempotent, and refuses to touch a closed task — and is not
tied to task creation. The only missing piece is a caller-facing operation.

## Decision

Add an explicit **refresh** operation on the runtime task service, mirroring the
existing `Claim`/`Reassign`/`Complete` shape (authorize, return a trigger, never
write state directly):

```go
func (s *TaskService) RefreshCandidates(
    ctx context.Context, taskID string, by authz.Actor,
) (engine.Trigger, error)
```

1. **Re-resolve, don't override.** The resolver stays the single source of truth:
   `RefreshCandidates` re-runs `ActorResolver.Candidates` against the task's
   *stored* `Eligibility` and `Vars`, so a refreshed list is derived identically
   to the one minted at creation. An explicit `SetCandidates(actors)` override is
   **deliberately not added** — an arbitrary list can contradict `Eligibility`,
   and the resulting "candidate who cannot claim" is a confusing state to expose
   before there is a concrete need for ad-hoc delegation.
2. **Replace semantics.** The returned trigger replaces the whole list, so a
   departed actor disappears rather than lingering. Re-applying is idempotent.
3. **Open tasks only.** A completed or cancelled task returns
   `task.ErrTaskNotOpen`; its candidate list is part of the audit record and stays
   frozen. The engine handler independently no-ops on a closed task, so the
   guarantee holds even for a trigger delivered by replay or by a caller that
   bypasses the service.
4. **The resolver is an option, not a constructor argument.**
   `task.WithActorResolver(r)` keeps `NewTaskService(store, az, opts...)`
   source-compatible; `RefreshCandidates` returns `task.ErrNoActorResolver` when
   none was configured. Claim/Reassign/Complete never use it.
5. **Authorization policy: `by` must satisfy the task's eligibility spec** — the
   same rule `Reassign` applies. A distinct admin/refresh-privilege model is
   deferred rather than invented here.
6. **Explicit, not automatic.** No periodic sweep and no re-resolution on read.
   A sweep needs a scale story (every open task × every tick) and read-time
   resolution puts I/O on a hot path that ADR-0073 wants cached. Consumers call
   refresh when their directory changes, or on demand behind their own UI.

`Candidates` is documented on the public API as a **projection, not an ACL**, so a
consumer cannot reasonably mistake a stale list for an authorization decision.

## Consequences

- **Positive:** the audit view can be brought current on demand, with no new
  storage, no new trigger kind, and no new engine handler — ADR-0147's trigger is
  reused verbatim. The refresh is journalled and replay-deterministic like any
  other trigger.
- **Positive:** re-resolution through the resolver makes divergence between the
  eligibility spec and the candidate list impossible by construction.
- **Negative:** the caller must know *when* to refresh. Nothing observes the
  directory, so a consumer whose membership changes frequently and who never calls
  refresh keeps a stale rendered list (authorization stays correct throughout).
- **Negative:** `TaskService` gains a dependency it does not always need; a
  service built without `WithActorResolver` fails this one call at runtime rather
  than at construction. Accepted to keep the constructor source-compatible and
  because the other three operations are unaffected.
- **Deferred:** an explicit `SetCandidates` override for ad-hoc delegation, and an
  admin-privilege model distinct from task eligibility (shared with `Reassign`).

## Implementation amendments (2026-07-27, rule-#9 re-audit)

1. **The refresh authorization policy is a known hole, not a settled design.**
   Decision #5 requires `by` to satisfy the task's eligibility spec, mirroring
   `Reassign`. For refresh that is partly circular: §Context says the case refresh
   exists for is *non-role-based* eligibility, where `ClaimableBy`'s live
   role-overlap arm does not apply — so if the eligible set has fully rotated, the
   people who could refresh cannot discover the task, and an ops user is refused
   because they are not themselves eligible. Recorded as a **known limitation**;
   an explicit admin privilege distinct from task eligibility is deferred and is
   shared with `Reassign`. Note also that an **empty `AuthzSpec` means allow-all**
   (`authz/authz.go`), so on a task with no declared eligibility this check admits
   any caller.
2. **Refresh recovers directory drift, not variable drift.** Re-resolution runs
   against the task's **creation-time `Vars` snapshot**, so a candidate set that
   went stale because a *process variable* changed is not recoverable by refresh.
3. **"Authorization is always live" is scoped to the actor.** `Claim`, `Complete`
   and `RefreshCandidates` all evaluate against the frozen `task.Vars`. The claim
   in §Context 1 means live *with respect to the acting actor*, not with respect
   to process variables.
4. **The rejection of `SetCandidates` needed a better reason.** The original
   rationale — that an override could produce a "candidate who cannot claim" — is
   inconsistent with ADR-0147 amendment #4, which declares candidates a projection
   rather than an ACL, making exactly that state the normal condition of any stale
   list. The honest reason is that no concrete ad-hoc delegation use case exists
   yet. Still deferred, on that basis.
5. **"No new storage" was wrong.** Each refresh writes a journal row embedding the
   full `[]authz.Actor` (attributes included) plus a snapshot rewrite, and nothing
   caps resolver output. Consumers should bound their resolver and apply a
   deadline; the driver bounds its own creation-path lookup with
   `WithCandidateResolveTimeout`. A cheap future optimisation is to skip emitting
   the trigger when the re-resolved set equals the stored one.
6. **An empty resolution replaces a good list.** The handler replaces wholesale by
   design, so a resolver that returns `(nil, nil)` on a partial outage blanks the
   candidate arm of `ClaimableBy`. Callers should treat an empty result as
   suspicious before applying it.
7. **Replay is retention, not recovery.** This repo has no journal-replay path
   (`kernel.JournalReader.Entries` has one forwarding consumer; state is always
   rebuilt from the `snapshot` column). The journal row *retains* who was eligible
   when — genuine audit value — but does not re-apply anything today.
8. **Scope: service layer, no HTTP.** The spec's non-goals exclude new transport
   endpoints. `RefreshCandidates` is exposed through the library API only —
   `service.WithActorResolver` + `service.RefreshTaskCandidates` — which is the
   product per CLAUDE.md; consumers mount their own route. Adding the method to
   `TaskManager` (embedded in `service.Service`) is a **breaking interface
   addition** for anyone implementing that interface.

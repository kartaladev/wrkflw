# 147. Human-task audit model: rich candidates, claim, and completion

Status: Accepted — 2026-07-27. Spec:
[docs/specs/2026-07-27-processinstance-audit-view-and-idgen.md](../specs/2026-07-27-processinstance-audit-view-and-idgen.md).
Part of the ADR-0144…0149 delivery. Persistence in [ADR-0148]; completion carrier
in [ADR-0146].

## Context

`humantask.HumanTask` (`humantask.go:58`) records `Candidates []string`,
`ClaimedBy string`, and a `State` — but no *who/when/what* audit of claim or
completion, and candidates are **IDs only**. Yet the data already exists upstream
and is thrown away: `ActorResolver.Candidates(ctx, spec, vars)` returns
**`[]authz.Actor`** (`humantask.go:96`) but the runtime narrows it to IDs; the
`HumanClaimed`/`HumanCompleted` triggers already carry a full `authz.Actor` and the
engine already stamps the time (`NewHumanClaimed(clk.Now(), …)`). `authz.Actor` is
`{ID string, Roles []string, Attributes map[string]any}` — no first-class
username/email (those are freeform `Attributes`).

## Decision

Enrich `HumanTask` with a full audit, keeping the data the engine already produces:

1. `Candidates []string` → **`Candidates []authz.Actor`** — stop discarding the
   resolver's output.
2. `ClaimedBy string` → **`Claim *Claim`** where `Claim = {Actor authz.Actor; At
   time.Time}` (nil while Unclaimed). One claimant at a time; **no history** —
   reassignment overwrites, matching current semantics.
3. Add **`Completion *Completion`** where `Completion = {Actor authz.Actor; At
   time.Time; Outcome string; Note string}` (nil until Completed).
4. Recording sites: `Claim` on `HumanClaimed` and `Completion` on
   `HumanCompleted` are stamped by the engine into `st.Tasks` via `TaskByToken`.
   **Candidates are different:** `userTaskStrategy.enter` (`engine/step_nodes.go:607`)
   is pure and sets none; the runtime resolves `[]authz.Actor` in
   `runtime/processdriver_action.go:257` and today writes them **only** to the task
   store. This ADR requires the runtime's `AwaitHuman` path to **also write the
   resolved rich actors back into `st.Tasks[i].Candidates`** (the persisted
   snapshot), because the instance view marshals `st.Tasks` — otherwise `candidates`
   render empty. (Blocker B1 from the rule-#9 audit.)
5. **Actor rendering = faithful passthrough.** The instance view renders every
   actor (`candidates[]`, `claim.actor`, `completion.actor`) as
   `{id, roles, attributes}` — verbatim `authz.Actor`. `username`/`email` appear
   under `attributes` only if the consumer's `ActorResolver` populated them; the
   engine guarantees nothing beyond `id`.

Persistence of these fields (snapshot blob + normalized columns) is
[ADR-0148]. The tasks JSON key rename `task_token`→`task_id` is [ADR-0144].

## Consequences

- **Positive:** the instance view carries a complete human-task audit — who was
  eligible, who claimed and when, who completed with what outcome, when, and their
  note — with no new consumer-supplied provider (the resolver is the source).
- **Positive:** passthrough is lossless and convention-free — maximally flexible
  for a library; consumers flatten downstream if they wish.
- **Negative / breaking:** `Candidates`'s type change and `ClaimedBy`→`Claim`
  ripple through readers: `TaskStore.ClaimableBy`/`AssignedTo` (which key on actor
  IDs — `humantask.go`), `Reassign`'s `from != task.ClaimedBy` check
  (`runtime/task/service.go:138`), and every construction/read of `HumanTask`.
  These become `Claim.Actor.ID` / candidate-`.ID` comparisons.
- **Caveat:** candidate profiles are **snapshotted at task creation**, not
  re-fetched live; a later directory change is not reflected. Documented as
  intended (audit reflects state at the time).
- **Caveat:** the resolver runs at task creation; a resolver that returns bare
  `{ID}` actors yields `{"id": "…"}` — `roles` and `attributes` are omitted when
  empty (see amendment #3) — acceptable and inherent to passthrough.

## Implementation amendments (2026-07-27, session 3)

Four corrections folded in during implementation. Each heading names the Decision
or Caveat it supersedes; the numbering below is independent of the Decision
numbering above.

### 1. Supersedes Decision #4 — candidates are resolved BEFORE the commit

Decision #4 required the runtime's `AwaitHuman` perform to "write the resolved
rich actors back into `st.Tasks[i].Candidates` (the persisted snapshot)". **The
snapshot is already persisted at that point**, so the write was a no-op across a
reload. In `runtime/processdriver.go` `deliverLoop`, one iteration runs:

1. `engine.Step` → `st = res.State`
2. `appliedStep := kernel.AppliedStep{State: st, …}` — snapshot captured
3. `commitFn` → `store.Create/Commit(appliedStep)` — **state written to the DB**
4. `driver.perform(stepCtx, def, st, c)` — the AwaitHuman command is handled *here*

Nothing re-saves `st` after step 4: `deliverLoop` returns it, and both callers
(`Drive`, `ApplyTrigger`) return it unchanged. `service.newInstanceJSON` is a pure
projection over `st` with no store access, so a consumer fetching the instance
later would render `candidates: []` — the exact bug B1 set out to fix.

**Amended decision — resolve pre-commit, between steps 1 and 2.**
`ProcessDriver.resolveHumanCandidates` expands the eligibility spec of every
`AwaitHuman` command the step emitted and writes the actors onto the matching
task in `st`, so the candidates ride the **same commit that parks the task**.
`perform(AwaitHuman)` then only projects the committed task into the task store.

Consequences of resolving here rather than after the commit:

- **No second commit, no second journal row, no extra Step** per human task.
- **A resolver failure aborts the step before anything is committed**, replacing a
  strictly worse post-commit failure mode in which a committed instance was left
  parked on a task the task store never received.
- **Every `UpdateTask` carries the candidates**, because the engine's task holds
  them from creation. This matters because `UpdateTask` round-trips the engine's
  task through the store (see amendment #2): a snapshot without candidates would
  erase them from the store on the first claim.
- The resolver call is bounded by `WithCandidateResolveTimeout` (default 10s,
  non-positive disables), mirroring `WithActionTimeout`. Resolution now sits on
  the commit path, so an unresponsive directory service must not hold a step open.

**A trigger is still the right shape for a *later* update**, where no commit is in
flight and the caller owns the request. `engine.HumanCandidatesResolved{TaskID,
Candidates}` is therefore retained and dispatched, but is emitted only by
[ADR-0150]'s `RefreshCandidates` — never by the creation path. Its handler:
replaces the list wholesale (idempotent; a departed actor disappears), **copies on
ingest** so a caller's slice is not aliased into committed state, rejects an
unknown task with `ErrTokenNotFound`, and no-ops on both a **closed task** (its
candidate list is audit and stays frozen) and a **terminal instance** (mirroring
`handleTimerFired`).

Rejected: enriching the view from the `TaskStore` at render time — it breaks the
public `NewProcessInstance(def, st)` constructor and contradicts ADR-0098's
self-serializing `ProcessInstance` and ADR-0144's self-contained document.

Also rejected, after implementation: making the creation path emit the trigger
too. It cost a second commit whose conflict had to be swallowed to avoid failing
an already-successful `Drive`, and that swallow proved unsound — it returned out
of the trigger queue (discarding follow-up triggers for sibling branches whose
actions had already run), could not distinguish a loop-generated follow-up from a
caller's explicit refresh, and left a window in which the next `UpdateTask` erased
the store's candidate list. The post-commit scheduler `Activate` also runs before
`perform`, so a user task carrying a short reminder lost the race deterministically
rather than occasionally.

### 2. The task snapshot now carries `Vars`, fixing a latent authorization bug

`userTaskStrategy.enter` minted a task with no `Vars`, and the runtime filled
`Vars` only in the task it upserted to the store. Because **every `UpdateTask`
command round-trips the engine's task through the store**, the first update after
task creation erased `Vars` — silently breaking attribute-based eligibility
(`vars["region"] == "EU"`) for the rest of the task's life. Latent before this
delivery (the first `UpdateTask` came at claim time); the candidates trigger made
it fire immediately at creation, which is how it surfaced.

**Amendment:** the engine snapshots the variables at task creation
(`Vars: copyVars(c.s.Variables)` in `userTaskStrategy.enter`) — it owns task
creation and already holds the variables, so the task is self-sufficient and every
`UpdateTask` round-trip is lossless. The runtime now copies `Vars` from the
in-state task alongside `NodeID`/`CreatedAt`.

### 3. Actor and audit wire shapes live on the types

Decision #5 requires "faithful passthrough" rendering of `{id, roles,
attributes}`, but `authz.Actor`, `humantask.Claim` and `humantask.Completion`
carried **no JSON tags** and would marshal PascalCase. Rather than re-map them in
each renderer (`service.taskJSON`, `runtime/view.ActionableTask`, the persistence
columns of [ADR-0148]), the wire contract is defined once on the types:
`Actor{id, roles?, attributes?}`, `Claim{actor, timestamp}`,
`Completion{actor, timestamp, outcome?, note?}`.

Journalled actors written before this change use Go's default PascalCase keys;
Go's case-insensitive field matching still decodes them (no underscore is
crossed), so existing journals replay unaffected — pinned by a regression case in
`TestActorJSONWireShape`.

Deep-copying is likewise defined once: `authz.Actor.Clone` and
`humantask.HumanTask.Clone`. `engine.cloneState` and
`persistence.cloneTask` both delegate to the latter instead of re-deriving the
field list, so a newly added mutable field is isolated everywhere at once.

### 4. Candidate staleness is a documented contract, not a defect

The "Caveat" above (candidates snapshotted at creation) is refined: candidates are
a **projection**, never an access-control list.

- **Authorization is always live**: `Claim`/`Complete` evaluate the stored
  `AuthzSpec` through the `Authorizer`, never the candidate list. An actor who
  becomes eligible after task creation can act immediately.
- **Inbox routing is half live**: both `ClaimableBy` implementations match
  `candidateContains(t.Candidates, actor.ID) || hasRoleOverlap(actor.Roles,
  t.Eligibility.Roles)`. The role arm is evaluated against the actor's *current*
  roles, so role-based routing needs no refresh.
- **What goes stale** is the rendered list, plus eligibility that is not
  role-based (`Attribute` predicates, `Privileges`), where only the enumerated IDs
  match.

Refreshing is therefore an explicit operation, not an automatic sweep — see
[ADR-0150](0150-human-task-candidate-refresh.md).

### 5. Caveats accepted during the rule-#9 re-audit

- **Actor fidelity differs by slot.** `candidates[]` are resolver-sourced and
  carry whatever `Attributes` the resolver populated. `claim.actor` and
  `completion.actor` come from the acting caller. Passthrough is faithful to what
  the engine observed; it does not promise the same richness in every slot.

  > ⚠ **AMENDED by ADR-0189 (2026-08-25).** This caveat originally continued: *"and
  > the HTTP transport's `httpcore.Actor` is `{id, roles}` only — so over HTTP those
  > two slots can never carry attributes … Phase 8's whole-document test must
  > therefore build its fixture through the Go API, not the transport."*
  >
  > **That is no longer true.** ADR-0189 removed `httpcore.Actor` and the three
  > body-derived actor fields; the authenticated actor now reaches the engine whole,
  > `Attributes` included, so `claim.actor` and `completion.actor` over HTTP have the
  > same fidelity as the resolver-sourced `candidates[]`. The Go-API fixture in
  > `service/instance_test.go` is now a convenience, not a necessity.
- **A reassigned task's claim is ID-only.** `HumanReassigned` identifies the new
  assignee by ID, so `handleHumanReassigned` records
  `Claim{Actor: authz.Actor{ID: t.To}}` — no roles, no attributes. A reassigned
  task's claim audit is strictly poorer than a claimed one.
- **An immediate-manual user task has no `Completion`.** `userTaskStrategy.enter`
  marks a `Manual && ManualImmediate` task `Completed` inline, with no actor and
  no `AwaitHuman`/`UpdateTask`. `Completion` is therefore "nil until completed
  **by an actor**", not "nil until Completed"; such a task also never reaches the
  task store. Phase 8 must pin this shape rather than assume `completion` is
  present whenever `state == "completed"`.
- **`Vars` is snapshotted per task.** Amendment #2 puts a copy of the process
  variables on every task, and the whole snapshot is re-marshalled on every
  commit. For instances with many user tasks and large variables this is real
  write amplification. Accepted for now because the alternative — a read-modify-
  write merge in `perform(UpdateTask)` — puts an extra store read on a hot path
  and makes "which fields are store-owned" an implicit contract. Revisit if
  snapshot size becomes a problem; `capHistory` is the precedent.

# 183. A human task's claim invariant is enforced before it can be committed

- Status: Proposed (**audited twice, 2026-08-18**; 31 + 21 findings adjudicated and folded; round 2
  reversed one round-1 decision — see Consequences. Ready to implement.)
- Date: 2026-08-17, revised 2026-08-18

> Design and every measurement:
> [`docs/specs/2026-08-17-human-task-claim-invariant-on-write.md`](../specs/2026-08-17-human-task-claim-invariant-on-write.md).
> Premise evidence: `docs/specs/2026-08-17-adr-0183-premise-evidence.md`.
> Audit adjudication: `docs/specs/2026-08-17-adr-0183-audit-adjudication.md` (round 1 + ADDENDUM).
> Lenses: `…-audit-lens-{a,b,c}.md`, `…-reaudit-lens-{d,e}.md`.
>
> Closes pre-v0.1.0 blocker **3**. ⚠ **BREAKING in three ways** — see Consequences.

## Context

`humantask.HumanTask` documents a claim invariant on its own field — *"`Claim` records the current
claimant and claim time; **nil when Unclaimed**"* (`humantask/humantask.go:104`). The **read** path
is built around it: `scanTask` rebuilds a `Claim` whenever `claimed_at` is non-NULL, even when the
instant is unparseable, so a degraded row stays self-consistent. The **write** path upholds nothing:
`Upsert` binds `t.State.String()` and the claim columns independently.

Executed on SQLite, `Upsert` accepts and round-trips every contradictory shape, and the damaging one
is not the one the blocker filed. Measured with an actor the row is **eligible** for, an `Unclaimed`
row carrying a claim is returned by `AssignedTo("alice")` = **1** *and* `ClaimableBy(alice)` = **1** —
**double-listed** as both held and claimable. `Claimed`+nil is instead absent from *every* inbox,
reachable only by ID: a different consequence, not a smaller one.

Four structural facts, each established by running or by reading source rather than by assuming:

- **Validating inside `Upsert` strands the instance.** `perform` runs **after** the commit
  (`runtime/processdriver_action.go:315` — its doc comment says so and names the pre-commit
  counterpart); a `perform` error does `return st, err`, **aborting the remaining command queue**
  (`runtime/processdriver.go:870`); and on completion `UpdateTask` is command **index 0** with the
  drive commands after it (`engine/step_triggers.go:941`). Measured: `status=running incidents=0`,
  the token parked on a command nobody will answer, and a replay failing `ErrInvalidTransition`. A
  DB error there is transient; a validation error is permanent and deterministic.
- **The guard fires on `UpdateTask` emit sites (8), not on `State =` assignments.** And
  `engine/step_triggers.go:612` (`handleHumanCandidatesResolved`) emits `UpdateTask` touching
  **neither** `State` nor `Claim` — a pass-through of whatever instance state holds, reachable from
  `service.RefreshTaskCandidates`. That is how a corrupted snapshot reaches the guard; an
  assignment-only enumeration cannot see it.
- **A fifth production writer of `State` is the read path.** `humantask_store.go:388` does
  `t.State = htParseTaskState(stateStr)` with `default: Unclaimed`, while `scanTask` rebuilds
  `Claim` independently of state. So a two-rule guard is **bypassable** by an out-of-range
  `TaskState`, and `Get` can return a value `Upsert` refuses.
- **An empty reassignment target is reachable from a live route.**
  `POST /tasks/{token}/reassign {"to":""}` mints `Claimed` with an empty claimant:
  `transport/http/httpcore/dto.go:60-67` states the REST handler carries no required-field
  validation, `engine/trigger_validate.go:65` validates only `TaskID`, and
  `runtime/task/service.go:214-231` never checks `to`.

For completeness: no live engine path *authors* an inconsistent shape — `Claimed` has 2 assignment
sites, both setting `Claim` on the immediately preceding line, and **zero** sites anywhere set
`.Claim = nil`. The exposure is the public API plus a corrupted snapshot replayed through `:612`.

## Decision

**Three rules, enforced pre-commit as the primary seam, with the stores as defence-in-depth.**

`humantask.Validate(t HumanTask) error` — a package-level function matching the repo's
`model.Validate(d *ProcessDefinition)` precedent (`definition/model/validate.go:276`) — returning an
error wrapping a new `ErrInvalidTask`:

- **R1** `State == Claimed` ⟹ `Claim != nil`
- **R2** `State == Unclaimed` ⟹ `Claim == nil`
- **R3** `State` is one of the four declared constants (closes the bypass), written as an
  **enumerated switch** rather than a range check, which is exact today but silently coupled to
  `iota` contiguity

⚠ **An empty claimant is deliberately legal.** A revision of this ADR rejected it; the re-audit
established that the shape is **blessed by ADR-0148 amendment 1 §4** — the kiosk claimant, with
roles and no ID — and that rejecting it would delete a data-disclosure regression test guarding
`AssignedTo("")`. This ADR therefore does **not** supersede ADR-0148 on that point. The unintended
route that mints an empty claimant is closed at the trigger instead (point 3 below); the *intended*
one, an empty-ID **claim**, is left alone. That asymmetry is deliberate: a kiosk claim is anonymous
but carries roles, whereas an empty reassignment target names nobody at all.

`Completed` and `Cancelled` carry no claim rule: a task cancelled while held keeps its claim as
audit, and `ManualImmediate` completes with none. ⚠ `Unclaimed` is `iota` — the **zero value** — so
R2 also rejects a task carrying a `Claim` whose `State` was never set. Deliberate, and documented,
because the message otherwise reads as wrong.

Six enforcement and classification points:

1. **Pre-commit** (primary): a new hook validates the step's emitted `UpdateTask` commands and
   aborts the step **before this iteration's commit**, mirroring `resolveHumanCandidates`
   (`runtime/processdriver_action.go:262`, called at `runtime/processdriver.go:683`).
   **`UpdateTask` only** — and this is *structurally* complete, not merely sufficient: it is the
   only command carrying a `humantask.HumanTask`, `AwaitHuman` is `{TaskID, Eligibility}` with one
   non-test emit site, and `performAwaitHuman` hardcodes `State: Unclaimed` without copying `Claim`.
   ⚠ **Not "before anything is committed".** `deliverLoop` is a `for len(queue) > 0` loop
   (`runtime/processdriver.go:610`) and `perform` appends follow-up triggers (`:873`), so one call
   can commit repeatedly; an abort on iteration ≥2 leaves iteration 1 committed. ⚠ **The
   precedent's own comment — quoted as this ADR's justification in an earlier revision — is
   measurably false**: lens D ran that exact resolver-outage scenario and the instance *was*
   stranded. The honest rationale is that pre-commit removes the harm where claim-shaped commands
   arise (iteration 1). The hook is unreachable in iteration ≥2 today **only by accident**: of the
   eight `UpdateTask` emitters, the two reachable from a follow-up trigger (`engine/state.go:656`,
   `engine/step_stale_commands.go:171`) both normalize `State` to `Cancelled`, which carries no
   claim rule.
2. **All three `Upsert` implementations** — `MemTaskStore`, `HumanTaskStore`, `CachingTaskStore` —
   as defence-in-depth. This is what protects a consumer calling `Upsert` directly, which no
   pre-commit hook can reach. `MemTaskStore` is strict because it backs the reference wiring and
   much of the suite; `CachingTaskStore` validates although it delegates, because it can wrap a
   consumer's permissive store.
3. **`Step`** refuses `HumanReassigned` with an empty `To` at `engine/step.go:162`, **before
   `cloneState`** (immediately after the existing `validateTriggerKey` block at `:156-158`) — "keeps a rejected trigger free of side effects" — via the new sentinel **`ErrEmptyReassignTarget`**. ⚠ **AMENDED AT REVIEW:**
   `task.TaskService.Reassign` refuses the same shape first, before its store read. The engine guard
   alone left the service spending a `store.Get` plus an authorization round-trip and recording a
   `humanTasks{event="reassigned"}` count for a reassignment `Step` then always rejected — the
   metric reported reassignments that never happened. The engine guard stays as the seam a
   hand-built trigger cannot bypass. Not
   `ErrEmptyTriggerKey`, whose doc says *"an identity key names one specific record"*
   (`engine/errors.go:212-218`); `To` is a required field, not an identity key.
4. **`ClassifyError`**: `ErrInvalidTask` → **422 `conflict_state`** (an engine-authored shape the
   caller cannot fix by editing the request); the reassign sentinel → **400 `bad_request`**.
   Without arms both fall to `default:` → 500 with an empty body, discarding the message.
5. **The `TaskStore.Upsert` interface doc** states the invariant as a contract implementations must
   uphold, directing them to call `Validate` rather than re-derive it.
6. **An exported conformance helper** in the public `processtest` package, so a consumer can verify
   their own store — today `humantask_store_conformance_test.go` is locked inside `internal/`.

   ⚠ **AMENDED AT REVIEW (2026-08-18, `/code-review` finding 4).** The helper's factory takes the
   case's `*testing.T`: `RunTaskStoreConformance(t *testing.T, newStore func(t *testing.T) humantask.TaskStore)`,
   not the parameterless `func() humantask.TaskStore` this ADR and the plan first displayed. The
   documented consumer pattern is `mystore.New(newTestDB(t))`, and a provisioning helper fatals; with
   no parameter that closure captures the **parent** `T` and calls `FailNow` on it from the case's
   goroutine, which `testing` does not support. Measured under the old signature (Go 1.25): the setup
   message is re-attributed to the parent (`=== NAME  TestX`), the case reports
   `test executed panic(nil) or runtime.Goexit: subtest may have called FailNow on a parent test`
   instead of the real error, and the run stops at the **first** of the eight shapes. Passing the
   case's `T` also scopes each factory's `t.Cleanup` to that case rather than to the whole suite
   (this repo's own SQLite leg held 8 databases open otherwise). Taken now because the symbol is
   unmerged and has never shipped; after merge it would be a breaking public-API change.

   ⚠ **AMENDED AT REVIEW (2026-08-18, `/code-review` finding 3).** For a rejected write the helper
   also asserts the row reached **neither** `AssignedTo` nor `ClaimableBy`, not `Get` alone. `Get`
   alone certifies the one property the helper most needed to check: a store that writes first and
   validates afterwards, or whose `Get` filters differently from its list queries, hides the row
   from `Get` while still **double-listing** it — the very shape this ADR exists to close. The
   in-repo suite (`internal/persistence/store/humantask_store_conformance_test.go`) already made
   both assertions; the exported one now mirrors them. Measured per shape, and stated as such in
   the code rather than generalised: `Unclaimed`+claim trips BOTH queries, an out-of-range state
   trips `AssignedTo` only (`ClaimableBy` returns `Unclaimed` rows), `Claimed`+nil trips NEITHER —
   there `Get` is the sole discriminator. The out-of-range fixture gained a claim so that shape has
   an inbox to leak into; with a nil claim it had none, and the new assertion could not have failed
   for it (mutation-verified both ways).

⚠ **A stale ordering caveat, removed.** A revision of this ADR warned that "R1's empty-claimant
clause is safe only *behind* the pre-commit hook", because in front of it a live HTTP route would
become a permanent store/state divergence returning repeatable 500s. The reasoning was sound but the
clause it guarded **no longer exists** — round 2 dropped it (see above). The route it worried about
is closed at the trigger by point 3, which refuses the request before `cloneState` touches anything,
so no divergence can arise. Nothing about R1/R2/R3 is order-dependent: lens D measured R3-last as
byte-identical, and the only arm that could panic on a nil dereference went away with the clause.

⚠ **`Validate` is a TaskStore-WRITE contract, not a whole-model invariant.** The engine deliberately
mints `Completed` + nil `Claim` + nil `Completion` into instance state via `ManualImmediate`
(measured: `tasks=1 upsertsSeen=0`, `store.Get → task not found`), and consumers can read it through
`service.ProcessInstance.ActiveTasks()`. The deferred completion axis **must carve that path out**.

### Rejected

- **Validate only inside `Upsert`** — measured to strand an instance. This was the first version of
  this ADR; the audit refuted it.
- **Validate only pre-commit** — leaves a consumer calling `Upsert` directly unprotected, which is
  the entire public-API gap being closed.
- **Guard the read path** on an unrecognized `state` string — a real second seam for R2, but it makes
  `Get` fail on an already-durable row, turning a readable-if-odd row into an unreadable one.
- **Normalize instead of erroring** — only two moves exist: fabricate a `Claim` (inventing audit data
  about who holds a task) or downgrade `State` (destroying it). Both launder a caller's bug into
  plausible audit history.
- **A `ValidatingTaskStore` decorator** — opt-in enforcement is fail-open by default, the same shape
  as the still-open fail-open `AuthzSpec`.

## Consequences

**Positive.** The invariant has one definition instead of three. A contradictory shape can no longer
be **authored**, nor reach a `TaskStore`, and a rejection no longer strands an instance on iteration
1. A double-listed inbox row becomes
unrepresentable **through `Upsert` with an in-range state**. Consumers get a callable rule and an
exported conformance helper instead of having to re-derive the contract.

⚠ Not "can no longer be **committed**" — an earlier revision claimed that and it is false. The hook
validates *commands*, never the snapshot: a step that emits no `UpdateTask` for an already-corrupt
record re-commits it unchanged (measured — `refresh_candidates` on an out-of-range record:
`err=<nil>, version 2→3, taskstate=unknown`).

**Verified: the guard does not block the escape.** The re-audit's most important question was
whether a corrupted snapshot becomes un-advanceable *and un-killable*. Executed as a matrix of 4
corrupt shapes × 4 verbs plus `CancelInstance`: `claim`, `complete`, `compensate_terminate` and
`CancelInstance` succeed on **every** shape; only `refresh_candidates` (the `:612` pass-through) is
refused. An operator can always still kill a corrupted instance.

**The rejection is classified at only one of five entry points.** HTTP gets 422/400 (point 4). The
others do not, and the **timer-fire** seam is the one that matters: `runtime/timerops.go:344-353`
logs at ERROR and **drops** the error, `fn` returns nil so the scheduler believes the fire
succeeded, no incident is raised, and the durable row is not deleted — so it re-fires and re-fails
every boot, and deadline breaches and reminders silently stop. Child-cancel propagation logs WARN
and continues; message delivery returns the error to the publisher. This is an accepted residual,
recorded rather than fixed.

**BREAKING, three ways.** (1) `Upsert` rejects contradictory shapes that silently succeeded. (2) A
reassignment with an empty `to` now fails with a 400 — in `TaskService.Reassign`, and in `Step`. (3) `ErrInvalidTask` reaching HTTP is
a 422, not a 500. The CHANGELOG must additionally name `processtest/harness.go:345`
(`Tasks() *humantask.MemTaskStore`, through which consumer fixtures are seeded) and state that for a
consumer's **own** `TaskStore` the break is **silent** — the interface signature does not change, so
a non-conforming store keeps accepting bad rows. Measured churn inside this repo is **zero**, with a
positive control proving the guard fires; that measurement covers *this* repo only.

**Accepted residuals — do not present any as fixed.** The spec's `## Deferred` section is the
canonical list (five items); the summary below must not diverge from it.

- **The completion axis stays open.** `State: Completed, Completion: nil` is still accepted, and
  `ManualImmediate` mints exactly that.
- **Existing rows are not repaired.** No migration, no backfill; a blind backfill could only guess at
  claim data it must not invent. Rows written before the guard — including any double-listed one —
  stay as they are.
- **`Completed`/`Cancelled` with an empty claimant** is still accepted; R1's clause covers `Claimed`.
- **A row already durable with an unrecognized `state` string** still reads back R2-violating. R3
  refuses new writes; it does not repair old rows, and the read path is deliberately left permissive
  so such a row stays readable.

⚠⚠ **AMENDED AT IMPLEMENTATION — audit finding B8 is REFUTED.** Both audit rounds accepted that a
post-commit rejection "drops a terminal sweep's remaining reconciliations", and the plan prescribed a
test for it. Implementation measured that this cannot happen: `cancelOpenTasks` assigns `Cancelled`
to **every** task it sweeps, and `Cancelled` is unconstrained on the claim axis, so a swept task is
valid however corrupt the snapshot was and the sweep emits no rejectable command.

```
PROBE after task[0] state=cancelled claim=<nil> validate=<nil>
PROBE after task[1] state=cancelled claim=<nil> validate=<nil>
PROBE CancelInstance err=<nil>   after version=3 (was 2)
```

Re-derived over all eight `UpdateTask` emit sites: five force `Cancelled`; `step_triggers.go:581/
:637/:941` mint `Claimed`+claim or `Completed`; **`step_triggers.go:612` is the only emitter that
can produce an invalid command, and it never coexists with a sweep.** This is the same normalization
this ADR already recorded for *follow-up* emitters — neither round carried it across to B8, which
asserts a defect on the very path that normalization immunizes. The prescribed test could not have
failed. B8's substance survives as two mutation-verified replacements: unit rows pinning that an
invalid command refuses the **whole** step rather than dropping tasks 2..N, and
`TestTerminalSweepReconcilesEveryTaskDespiteACorruptOne`, guarding that the hook does **not** block
the sweep, so a corrupted instance stays killable.

**Corrections this ADR makes to its own first version** (each refuted by execution, per rule #11):

- The claim that the guard "converts backlog 32's silent corruption into a loud error — a benefit"
  was **wrong twice**: it converts it into a *stranded instance*, and the path it cited
  (`cancelOpenTasks`) is the one that **immunizes** the shape, because it sets `State = Cancelled`
  before cloning and `Cancelled` has no claim rule. The state-preserving re-emitter is
  `step_triggers.go:612`.
- "Write-side is the only seam" holds for **R1 only**; for R2 the read path is a producer.
- The `ManualImmediate` `ASSUMPTION (unverified)` is now **executed and true**.
- The inherited ADR-0148 sentence *"claimed_at is NULL exactly when the task is unclaimed"*
  (`humantask_store.go:129-130`) is **false pre- and post-fix** — `htClaimBinds` keys on `c == nil`,
  and a `Completed`/`Cancelled` task with no claim has NULL `claimed_at` while not being unclaimed.
  Corrected in this bundle.

**Adjacent correction.** The comment above the `ht.State` assignment in `userTaskStrategy.enter`
(`engine/step_nodes.go:749-759`, assignment at `:760`) claimed the `ManualImmediate` path "mirrors the
state `handleHumanCompleted` sets". It does not — that path records a `Completion` from the
completing actor's trigger and `ManualImmediate` records none. Corrected in this bundle;
comment-only. ⚠ `ManualImmediate` appears in **no** engine or runtime test, so "this changes no test"
is true but vacuous — not evidence of coverage.

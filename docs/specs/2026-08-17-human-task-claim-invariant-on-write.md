# A human task's claim invariant is enforced before it can be committed

**Status:** design approved 2026-08-17; **AUDITED TWICE (2026-08-18), all findings adjudicated and
folded — READY TO IMPLEMENT.** No third audit: round 2 shrank the design rather than adding
mechanism.
**Closes:** pre-v0.1.0 blocker **3** (`Upsert` can persist `State: Claimed, Claim: nil`)
**ADR:** 0183
**Audit:** `2026-08-17-adr-0183-audit-adjudication.md` — round 1 (31 findings, lenses
`-audit-lens-{a,b,c}.md`) **plus the ADDENDUM** for round 2 (21 findings, lenses
`-reaudit-lens-{d,e}.md`)
**Evidence:** `2026-08-17-adr-0183-premise-evidence.md`

> ⚠ **This spec was rewritten twice, by its two audits.** V1 enforced the invariant only inside
> `Upsert`; round-1 finding B1 **measured that this strands a process instance** — `Upsert` runs
> post-commit, so a rejection commits the state, drops every later command, raises no incident and
> cannot be retried. The guard moved pre-commit, and the owner chose full scope. Round 2 then
> **reversed one round-1 decision**: R1's empty-claimant clause is dropped, because it reverses
> ADR-0148 amendment 1 §4's blessed kiosk shape and would delete a data-disclosure regression test.
> Round 2 also verified the design's strongest property — a corrupted instance stays **killable**.

## Problem

`humantask.HumanTask` documents a claim invariant on its own field — *"`Claim` records the
current claimant and claim time; **nil when Unclaimed**"* (`humantask/humantask.go:104`). The
**read** path upholds it: `scanTask` rebuilds a `Claim` whenever `claimed_at` is non-NULL, even
when the instant is unparseable, so a degraded row stays self-consistent.

The **write** path upholds nothing. `Upsert` binds `t.State.String()` and the claim columns
independently.

### Measured today (SQLite, `dbtest.RunTestSQLite`; probes deleted, log in the evidence file)

| seeded | `Upsert` err | read back |
|---|---|---|
| `State: Claimed, Claim: nil` | `<nil>` | `state=claimed claim=<nil>` |
| `State: Completed, Completion: nil` | `<nil>` | `state=completed completion=<nil>` |
| `State: Unclaimed` **with** a claim | `<nil>` | `state=unclaimed claim=&{alice…}` |

Inbox behaviour, measured with an actor the row is **eligible** for (`Eligibility.Roles:
["mgr"]`, `Candidates: ["alice"]`, actor `alice/mgr`):

| shape | `AssignedTo("alice")` | `ClaimableBy(alice)` | consequence |
|---|---|---|---|
| `Unclaimed` **with** a claim | **1** | **1** | **double-listed** — alice both holds it and is offered it |
| `Claimed`, `Claim: nil` | 0 | 0 | lost from **every** inbox; reachable only by ID |

⚠ Two corrections folded from review, both to earlier statements of mine:

- An earlier probe reported `ClaimableBy=0` for the first row. That zero was **confounded** —
  the fixture declared no candidates and no eligible roles, so nothing could match on any axis.
  Re-measured with an eligible actor: **1**. The defect is a double-listing, not an omission.
- Calling the second row "inert" understated it. Being absent from every inbox is a *different*
  consequence, not a smaller one, and must not be used to rank the two directions.

### Why this cannot be fixed on read

For **R1** (`Claimed`+nil), `scanTask` keys on `claimed_at` being non-NULL, and this shape
writes NULL — the legitimate encoding of "never claimed". Nothing to detect.

For **R2** the read path is not merely blind, it is a **producer** — see the fifth writer below.
So "write-side is the only seam" holds for R1 only; a read-side guard on the `state` string is a
second available seam, considered and rejected under Decision 5.

## The production writers — re-derived over the RIGHT set

Two enumeration errors from the first version, both found by audit:

**The guard fires on `UpdateTask` emit sites, not on `State =` assignments.** There are **eight**
non-test emitters: `engine/step_timers.go:93`, `step_triggers.go:581`, `:612`, `:637`, `:941`,
`state.go:656`, `step_cancel.go:40`, `step_stale_commands.go:171`. Of these,
**`step_triggers.go:612` (`handleHumanCandidatesResolved`) touches neither `State` nor `Claim`** —
it re-emits whatever shape instance state already holds, reachable from
`service.RefreshTaskCandidates`. An assignment-only table structurally cannot see it, and it is
precisely how a corrupted snapshot would reach the guard.

**A fifth production writer of `State` exists, and it is the read path.**
`internal/persistence/store/humantask_store.go:363` does `t.State = htParseTaskState(stateStr)`,
whose `default:` returns `Unclaimed`, while `scanTask` rebuilds `Claim` independently of state.
Executed:

```
PROBE: Upsert(State=TaskState(99), Claim=alice) err = <nil>
PROBE: read back state=unclaimed claim=&{{alice [] map[]} …}
PROBE: R2 violated on read? true
```

Consequences: a two-rule `Validate` is **bypassable** by an out-of-range `TaskState`, and `Get`
can return a value `Upsert` would refuse. R3 below closes both.

For completeness, the `State`-assignment counts (production only, either form) are `Claimed` **2**
(`step_triggers.go:578`, `:634` — both set `Claim` on the immediately preceding line),
`Unclaimed` **2** (both composite literals), `Completed` **2**, `Cancelled` **4**, and **zero**
sites anywhere set `.Claim = nil`. ⇒ **No live engine path authors an inconsistent shape**; the
exposure is the public API plus a corrupted snapshot replayed through `:612`.

## Decision

### 1. The invariant — three rules (two on the claim axis, one on the state domain)

- **R1** `State == Claimed` ⟹ `Claim != nil`
- **R2** `State == Unclaimed` ⟹ `Claim == nil`
- **R3** `State` is one of the four declared constants

`Completed` and `Cancelled` carry **no** claim rule: a task cancelled while held keeps its claim
as audit, and `ManualImmediate` completes with none. Two test rows pin this silence as deliberate.

R3 is written as an **enumerated switch** over the four constants, matching `TaskState.String()`'s
house style — not a `State < Unclaimed || State > Cancelled` range check, which is exact today but
silently coupled to `iota` contiguity: a fifth constant added inside or outside the block would
change its coverage with no test failing.

⚠ **An empty claimant is NOT rejected, and that is deliberate.** A `Claimed` task whose
`Claim.Actor.ID` is empty stays legal. The re-audit established that this shape is
**blessed by ADR-0148 amendment 1 §4** — the conformance suite's subtest *"claim with an empty
actor id still reads back as a claim"* (`humantask_store_conformance_test.go:252-272`) seeds it
with the comment *"an empty claimant id is a legitimate value, and keying on it would resurrect
the fabricated/dropped-claim bug of amendment 1 §4."* It is the **kiosk claimant**: self-service,
no identity, but carrying roles. Rejecting it would also delete a data-disclosure regression test
(`humantask/memory_test.go`'s `claimedByNobody`, which guards `AssignedTo("")` against
degenerating into a wildcard).

An earlier revision of this spec *did* reject it, on the argument that the shape is "lost from
every inbox". That argument was correct about the mechanics and wrong about the intent — being
absent from the inboxes **is** the documented kiosk semantics. The unintended route that mints an
empty claimant is closed at the trigger instead (Decision 4). See the adjudication addendum.

⚠ `Unclaimed` is `iota`, i.e. the **zero value**, so R2 also rejects a task carrying a `Claim`
whose `State` was never set — including a decode that dropped only `State`. Deliberate; documented
on `Validate` and in the CHANGELOG, because the message otherwise reads as wrong.

### 2. The primary seam is PRE-COMMIT, in the runtime

`Validate` alone in `Upsert` is not merely insufficient, it is **harmful**. Measured (audit B1):

```
PROBE ApplyTrigger err = workflow-runtime: update task: workflow-humantask: invalid task
PROBE PERSISTED state: status=running incidents=0 tokens=1
PROBE   ptoken[0] node=act await="…kos0"      # parked on a command nobody will answer
PROBE RETRY same trigger err = … human task is not open: invalid state transition
```

Mechanism, verified in source: `perform` runs **after** the commit
(`runtime/processdriver_action.go:282`, whose doc comment says exactly this and names the
pre-commit counterpart); a `perform` error does `return st, err`, **aborting the remaining
command queue** (`runtime/processdriver.go:855`); and on completion `UpdateTask` is command
**index 0** with the drive commands appended after it (`engine/step_triggers.go:941`). A DB error
there is transient; a validation error is permanent and deterministic.

So a new pre-commit hook validates the step's emitted `UpdateTask` commands, mirroring
`resolveHumanCandidates` (`runtime/processdriver_action.go:236`, called at
`runtime/processdriver.go:668`). A rejection aborts the step **before this iteration's commit**.

⚠ **"Pre-commit" means "before THIS ITERATION's commit", not "before anything is committed".**
`deliverLoop` is a `for len(queue) > 0` loop (`runtime/processdriver.go:610`) and `perform` appends
follow-up triggers (`:858`), so one `ApplyTrigger`/`Drive` call can commit several times. An abort
on iteration ≥2 leaves iteration 1 committed.

⚠ **And the precedent's own comment is measurably FALSE**, so it is not quoted here as
justification. An earlier revision of this spec cited *"a resolver outage can no longer leave a
committed instance parked on a task that was never written to the task store"* as proof the pattern
works. Re-audit lens D executed exactly that scenario and the instance **was** stranded — durable
version reached 1, token parked, and a retry failed `instance already exists`. I had restated an
inherited comment as fact without running it.

The honest rationale is narrower and still sufficient: pre-commit removes the harm **where
claim-shaped commands actually arise**, which is iteration 1. The hook is unreachable in
iteration ≥2 today — but **only by accident**, and the accident must be recorded: of the eight
`UpdateTask` emitters, the two reachable from a follow-up trigger (`engine/state.go:656`,
`engine/step_stale_commands.go:171`) both normalize `State` to `Cancelled`, which carries no claim
rule. A future emitter that preserves `Claimed` on a follow-up path would reintroduce the gap.

**`UpdateTask` only.** `AwaitHuman` has exactly **one** non-test emit site
(`engine/step_nodes.go:806`) and `performAwaitHuman` hardcodes `State: humantask.Unclaimed`
without copying `Claim` (`runtime/processdriver_action.go:439`), so it cannot be claim-invalid.

⚠⚠ **Audit finding B8 is REFUTED BY IMPLEMENTATION — the terminal-sweep drop cannot occur.**
Both audit rounds accepted B8's premise: `engine/state.go:645-659` emits one `UpdateTask` per open
task, so a post-commit rejection on task 1 would drop tasks 2..N, leaving a terminated instance
still advertising them in the very inboxes `cancelOpenTasks` exists to clear. Task 3 probed it on a
two-user-task parallel definition with task 0 corrupted:

```
PROBE after task[0] state=cancelled claim=<nil> validate=<nil>
PROBE after task[1] state=cancelled claim=<nil> validate=<nil>
PROBE CancelInstance err=<nil>   after version=3 (was 2)
```

`cancelOpenTasks` assigns `humantask.Cancelled` to **every** task it sweeps, and `Cancelled` is
unconstrained on the claim axis — so a swept task is valid *however corrupt the snapshot was*, and
the sweep emits no rejectable command. Re-derived over all eight `UpdateTask` emit sites: five
(`state.go:656`, `step_cancel.go:40`, `step_stale_commands.go:171`, `step_timers.go:93`, and the
`IsOpen` guards) force `Cancelled`; `step_triggers.go:581/:637/:941` mint `Claimed`+claim or
`Completed`. **`step_triggers.go:612` is the only emitter that can produce an invalid command, and
it never coexists with a sweep.**

This is the same mechanism the ADR already recorded for *follow-up* emitters ("both normalize
`State` to `Cancelled`, which carries no claim rule") — neither audit round carried it across to
B8, which asserts a defect on the very path that normalization immunizes.

B8's substance was not dropped. It was split into two falsifiable, mutation-verified tests: two unit
rows pinning that an invalid command refuses the **whole** step rather than dropping tasks 2..N, and
`TestTerminalSweepReconcilesEveryTaskDespiteACorruptOne`, which guards the property the measurement
actually exposed — the hook must **not** block the sweep, so a corrupted instance stays killable and
both tasks reconcile in one commit.

### 3. The three `Upsert` guards stay — as defence-in-depth

They are what protects a consumer calling `Upsert` directly, which no pre-commit hook can reach:
`humantask.MemTaskStore` (`humantask/memory.go:33`), `store.HumanTaskStore`
(`internal/persistence/store/humantask_store.go:131`), `persistence.CachingTaskStore`
(`persistence/caching_task_store.go:98`).

`MemTaskStore` is strict too: it backs the reference wiring and much of the suite, so leaving it
permissive would let a test green-light a shape production rejects. `CachingTaskStore` validates
although it delegates — redundant with our own backing stores, but it can wrap a consumer's
permissive one.

### 4. An empty reassignment target is refused at the trigger, not at the store

`POST /tasks/{token}/reassign {"from":"alice","to":""}` succeeds today and mints `Claimed` with an
empty claimant. Nothing validates `To`: `transport/http/httpcore/dto.go:60-67` states outright
that the REST handler carries no required-field validation, `engine/trigger_validate.go:65`
validates only `TaskID` for `HumanReassigned`, and `runtime/task/service.go:214-231` checks `from`
against the claimant but never `to`.

`To` is validated in `Step`, at `engine/step.go:158` — immediately after the existing `validateTriggerKey` block and still **before `cloneState`**, whose comment says
this "keeps a rejected trigger free of side effects". The request is refused with no state touched.

⚠ **A new sentinel, not `ErrEmptyTriggerKey`.** That sentinel's doc says *"An identity key names
one specific record"* (`engine/errors.go:212-218`, ADR-0152); `To` is a required field, not an
identity key. Reusing it would contradict its documented meaning.

⚠ **The asymmetry with the CLAIM verb is deliberate.** Re-audit lens D found a second live route
that mints an empty claimant: `handleHumanClaimed` (`engine/step_triggers.go:577-581`) writes
`Claim{Actor: t.Actor}`, `validatedTriggerKinds["engine.HumanClaimed"]` checks `TaskID` only, and
`TaskService.Claim` runs only authz — which an empty-ID actor holding a matching role passes.
`transport/http/httpcore/dto.go:40-44` documents this as intended: *"No fields are required — an
empty actor is allowed."*

That route is **left alone**, and this one is closed, because the two are semantically different:

- a **kiosk claim** is self-service with no identity but *carries roles* — ADR-0148's blessed shape;
- an **empty reassignment target** is incoherent. Reassignment moves a claim *to someone*, and
  `handleHumanReassigned` mints `Actor{ID: t.To}` with no roles either — genuinely nobody, not an
  anonymous someone.

### 4b. A guard must not refuse the operation that clears the bad state — verified

The re-audit's most important question was whether a corrupted snapshot becomes un-advanceable
**and un-killable**. Executed: 4 corrupt shapes × 4 verbs, plus a separate `CancelInstance` matrix.
`claim`, `complete`, `compensate_terminate` and `CancelInstance` **succeed on every shape** (version
2→3, status→terminated, store row reconciled). Only `refresh_candidates` — the `:612` pass-through —
is refused. **The guard does not block the escape.** This is the design's strongest property and it
is recorded here because the first two revisions asserted it nowhere.

### 5. Rejected alternatives

- **Validate only inside `Upsert`** — measured to strand an instance (Decision 2).
- **Validate only pre-commit** — leaves a consumer calling `Upsert` directly unprotected, which is
  the entire public-API gap this closes.
- **Guard the read path** on an unrecognized `state` string. A real second seam for R2, but it
  makes `Get` fail on a row that is already durable, converting a readable-if-odd row into an
  unreadable one. R3 refuses the write instead.
- **Normalize instead of erroring.** Only two moves exist: fabricate a `Claim` (inventing audit
  data about who holds a task) or downgrade `State` (destroying it). Both launder a caller's bug
  into plausible audit history.
- **A `ValidatingTaskStore` decorator.** Opt-in enforcement is fail-open by default — the same
  shape as the still-open fail-open `AuthzSpec`.

### 6. The HTTP surface classifies the new errors

`ClassifyError` (`transport/http/httpcore/errors.go`) has no arm for either sentinel, so both fall
to `default:` → **500 with an empty body**, discarding the message that names the task and the
contradiction. That file's own comments are the repo's precedent that a sentinel reaching HTTP is
classified deliberately: *"Without these arms they fall to the 500 default, which hides an
actionable 4xx behind an empty body."*

- `humantask.ErrInvalidTask` → **422 `conflict_state`**. An engine-authored shape the caller
  cannot correct by editing the request, which is what 422 already means here
  (`service.ErrConflict`, `engine.ErrInvalidTransition`).
- the reassign-target sentinel → **400 `bad_request`**, alongside `ErrEmptyTriggerKey` and the
  other caller-correctable input sentinels.

### 7. `Validate` is a TaskStore-WRITE contract, not a whole-model invariant

The engine deliberately mints `Completed` + nil `Claim` + nil `Completion` into **instance state**
via `ManualImmediate`, and that record is reachable by consumers through
`service.ProcessInstance.ActiveTasks()` and the history DTO (`service/instance.go:72-95`). It never
reaches a store — executed:

```
PROBE immediate: driveErr=<nil> status=completed tasks=1 upsertsSeen=0
PROBE   task[0] state=completed claim=<nil> completion=<nil>
PROBE   store.Get(…) err=workflow-humantask: task not found
```

(This promotes the first version's `ASSUMPTION (unverified)` to a measurement.) The distinction
matters for the deferred completion axis: the moment it lands, the rule the interface doc tells
consumers to call would reject a record the engine authored on purpose. **The deferred axis must
carve out `ManualImmediate`.**

### 8. Consumers get an exported conformance helper

The contract is a documented MUST on a public interface, and today nothing lets a consumer verify
their own store against it — `humantask_store_conformance_test.go` is locked inside `internal/`.
A helper is exported from the public `processtest` package (already the home of `SpyAuthorizer`,
`SpyCatalog`, `CaptureSender`): given a factory it exercises all three rules plus "a rejected
`Upsert` persists nothing".

⚠ **AMENDED AT REVIEW (2026-08-18, `/code-review` finding 4):** the factory is
`func(t *testing.T) humantask.TaskStore` — it receives the CASE's `T`. Measured under the
parameterless shape: a factory closing over the parent `T` (the documented `newTestDB(t)` pattern)
calls `FailNow` across goroutines, so the setup message is re-attributed to the parent, the case
reports `test executed panic(nil) or runtime.Goexit` instead, and the run stops at the first of the
eight shapes. Per-case `t.Cleanup` scoping follows from the same change.

⚠ **AMENDED AT REVIEW (2026-08-18, `/code-review` finding 3):** for a rejected write the helper
asserts the row reached neither `AssignedTo` nor `ClaimableBy` as well as being absent from `Get`,
mirroring the internal suite's `mustNotBeListed`. `Get` alone passes a store that hides the row
there while still double-listing it. Per-shape reach (measured, and NOT uniform): `Unclaimed`+claim
→ both queries; out-of-range state carrying a claim → `AssignedTo` only; `Claimed`+nil → neither,
so `Get` remains its sole discriminator. A purpose-built `leakyRollbackTaskStore` stand-in (rejects
the write, hides it from `Get`, leaks it into the inboxes) is what proves the new assertions
discriminate rather than merely run.

## Blast radius

**Zero fixture churn inside this repo — RE-measured 2026-08-18 for the rules as they now stand.**
All seven `Upsert`-calling packages run unfiltered with the R1/R2/R3 guard patched into both
stores: **EXIT=0, 0 FAIL**, and this time `docker info` was probed and reported **up**, so the
Postgres and MySQL legs genuinely ran (store package 49.4s). A **four-axis positive control**
confirms the guard fires — and its fourth row is the discriminating one: the ADR-0148 kiosk shape
(`Claimed` + empty claimant) is still **accepted**.

⚠ **Two label corrections, both of my own claims.** The first version said the Postgres/MySQL legs
"did NOT run (no Docker probe)"; Docker was in fact up and `dbtest.RunTestDatabase` **fatals**
rather than skips, so those tests did boot — the conclusion stood but the stated reason was false.
Then the *rewrite* restated that number for a guard which had since gained two rules, and re-audit
lens E measured it **FALSE**: `humantask/memory_test.go`'s `claimedByNobody` fixture would have
failed. The number above is the re-measurement, after the empty-claimant clause was dropped.
Note also that `-run 'TestHumanTaskStoreConformance/sqlite/…'` boots **no** containers — element 0
of a `-run` pattern filters top-level names too. The "20 top-level Postgres/MySQL tests still run"
caveat applies only to `-run '.*/sqlite'`, whose element 0 is `.*`.

⚠ Still `UNVERIFIED`: the **pre-commit hook's** own churn and `Step`'s reassignment guard were not
patched in for that run. Lens E independently derived `Step`'s as zero — `NewHumanReassigned` has
16 call sites, none passing an empty target.

Likewise the first version dismissed the 35 `State: Claimed` fixtures with "every case assigns
`task.Claim`". **False**: of that group's two cases the claim case assigns `Claim`, while the
completion case reassigns `State` to `Completed`, which the rule leaves unconstrained. Zero-churn
holds; the reason was wrong — the "check the fixture, not the line" class.

**BREAKING**, three ways:

1. `Upsert` rejects contradictory shapes that silently succeeded before.
2. A reassignment with an empty `to` now fails at `Step` with a 400 instead of succeeding.
3. `ErrInvalidTask` reaching HTTP is a 422, not a 500.

Consumer-visible surfaces the CHANGELOG must name: `processtest/harness.go:345` exposes
`Tasks() *humantask.MemTaskStore`, so consumer fixtures seeded through it break. And for a
consumer's **own** `TaskStore` the break is **silent** — the interface signature does not change,
so nothing recompiles differently and a non-conforming store keeps accepting bad rows. Zero churn
was measured over *this* repo only.

## Test plan

Each test states what makes it fail today. Table tests use the project `assert`-closure form.

| test | package | fails today because |
|---|---|---|
| `TestValidate` — R1 (nil claim only), R2, R3, plus `Completed`+nil, `Cancelled`+claim and the `Claimed`+empty-claimant kiosk shape as **legal** | `humantask` | `undefined: humantask.Validate` |
| `MemTaskStore.Upsert` rejects every invalid shape; a rejected write stores nothing | `humantask` | returns nil today |
| Pre-commit: a corrupted `Claimed`+nil task replayed through `handleHumanCandidatesResolved` aborts the step with **nothing committed** — assert `incidents=0`, the token NOT parked, and the persisted state unchanged | `runtime` | today the state commits, later commands are dropped, and the token parks on an unanswerable command (measured) |
| ~~Terminal sweep with **N=2** open tasks: neither is dropped~~ — ⚠ **REFUTED, replaced.** The sweep normalizes every task to `Cancelled`, which is unconstrained, so no rejection can arise there and the prescribed test could not fail. Replaced by `TestTerminalSweepReconcilesEveryTaskDespiteACorruptOne` (the hook must not *block* the sweep) plus two unit rows pinning whole-step refusal | `runtime` | the replacement fails if the hook over-rejects `Cancelled`+claim, or if the command loop stops at the first entry — both mutation-verified |
| `HumanReassigned` with `To: ""` is refused by `Step` before `cloneState` | `engine` | `NewHumanReassigned(…, to: "")` succeeds today and mints `Claimed`+empty claimant (measured) |
| Conformance: all invalid shapes rejected, all dialects, **plus a positive control** upserting the same fixture in a LEGAL shape and asserting `AssignedTo`/`ClaimableBy` DO return it | `internal/persistence/store` | measured `Upsert err = <nil>`; and without the positive control the double-listing assertions are unreachable in both branches |
| `CachingTaskStore` rejects **before delegating** (via a `backing.upserts` counter) and caches nothing | `persistence` | delegates and caches unconditionally |
| `ClassifyError` maps `ErrInvalidTask` → 422 and the reassign sentinel → 400 | `transport/http/httpcore` | both fall to `default:` → 500, empty body |
| The exported conformance helper passes for all three bundled stores and fails for a permissive one | `processtest` | the helper does not exist |

⚠ **The double-listing regression must not repeat its first-version defect.** `require.ErrorIs` is
`FailNow`: with the guard working the follow-on inbox assertions are tautologies (a row never
written cannot be listed), and with it broken the subtest aborts before reaching them — unreachable
as evidence in **both** branches. Use `assert.*` **and** the legal-shape positive control.

## Adjacent cleanups, in this bundle

- `engine/step_nodes.go:751` (the comment above the `ht.State` assignment at `:755`) claims the `ManualImmediate` path "mirrors the state
  `handleHumanCompleted` sets". False — `handleHumanCompleted` sets `Completion`; that path sets
  neither. ⚠ `ManualImmediate` appears in **no** engine or runtime test, so "this must not move a
  single test" is true but vacuous; say so rather than presenting it as coverage.
- `internal/persistence/store/humantask_store.go:129-130` inherits from ADR-0148 amendment 2:
  *"claimed_at is NULL exactly when the task is unclaimed"*. **False pre- and post-fix** —
  `htClaimBinds` keys on `c == nil`, and a `Completed`/`Cancelled` task with no claim has NULL
  `claimed_at` while not being "unclaimed". Correct it while editing that block.

## Deferred — this list is CANONICAL; the ADR and the plan point at it

1. **The completion axis** (`Completed ⟹ Completion != nil`) — must carve out `ManualImmediate`
   (Decision 7).
2. **Repairing existing rows.** No migration: no live engine path authored an inconsistent row, and
   a blind backfill could only guess at claim data it must not invent. Rows written before the
   guard — including any double-listed one — are **not** repaired.
3. **An empty claimant, on any state.** Deliberately legal (Decision 1) — ADR-0148's kiosk shape.
   If ever revisited, ADR-0148 amendment 1 §4 must be explicitly superseded, the kiosk use case
   adjudicated, `humantask/memory_test.go`'s `claimedByNobody` disclosure test re-seeded through a
   non-`Upsert` seam, and the empty-ID **claim** route (Decision 4) decided at the same time.
4. **A durable record with an unrecognized `state` string.** R3 refuses new writes but repairs
   nothing. Such a record is **re-committed unchanged** by a step that emits no `UpdateTask` for
   it, and it is permanently unactionable — `claim` and `complete` both refuse it with
   `ErrTaskNotOpen` — while staying visible in `ClaimableBy` even after its instance is terminated,
   because `cancelOpenTasks` skips it via `IsOpen()`.
5. **The `instActive` gauge leak** on an iteration-≥2 abort, and the step span ending without
   `wrkflw.command_count` / `wrkflw.status` on a rejection. Both pre-existing; the gauge leak is
   live today on the resolver path.

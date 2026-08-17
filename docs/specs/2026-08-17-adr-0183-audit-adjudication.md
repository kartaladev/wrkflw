# ADR-0183 — rule-#9 audit adjudication

**Date:** 2026-08-17 (adjudicated 2026-08-18)
**Bundle audited:** spec `2026-08-17-human-task-claim-invariant-on-write.md`, ADR-0183, plan
`2026-08-17-human-task-claim-invariant.md`, evidence `2026-08-17-adr-0183-premise-evidence.md`.
**Lenses:** A = execution, B = end-to-end reachability / consumer seam, C = re-counting.
Write-ups: `2026-08-17-adr-0183-audit-lens-{a,b,c}.md`.

**Outcome: 31 findings. 30 accepted, 1 rejected with reason, 2 accepted with a DIFFERENT fix
than proposed. Two Criticals invalidated the delivery's core mechanism.** The owner chose
**full scope in one bundle** after being shown the size; the bundle is rewritten accordingly
and **must be re-audited** — the pre-commit design below is new and no lens has attacked it.

Silence is not an adjudication: every finding is listed with a verdict.

## The two Criticals, and what they changed

### B1 — ACCEPTED. Validating inside `Upsert` puts the check on the wrong side of the commit

A rejected `Upsert` runs inside `perform`, which executes **after** the state commit; the
error aborts the remaining command queue, raising no incident and leaving the trigger
unretryable. Controller-verified independently, in source:

| claim | verified at |
|---|---|
| `perform` runs after the commit | `runtime/processdriver_action.go:281` — its own doc comment says so, and names `resolveHumanCandidates` as "the pre-commit counterpart" |
| a `perform` error aborts the rest of the queue | `runtime/processdriver.go:855` — `if err != nil { return st, err }` |
| `UpdateTask` is command index 0 on completion, drive commands appended after | `engine/step_triggers.go:941` |

Lens B's measured consequence: `status=running incidents=0 tokens=1`, token parked on a
command id nobody will answer, and replaying the trigger fails `ErrInvalidTransition`.

**This refutes the bundle's own claim** (old spec §"interaction with backlog 32", ADR
Consequences) that the guard "converts backlog 32's silent corruption into a loud error". It
converts it into a **silently stranded instance**.

**Accepted fix — the repo's own established pattern.** Validation moves **pre-commit**,
mirroring `resolveHumanCandidates` (`runtime/processdriver_action.go:236`, called at
`runtime/processdriver.go:668`), whose comment reads verbatim: *"a resolver outage can no
longer leave a committed instance parked on a task that was never written to the task
store."* That is this failure shape, already solved once here.

**Narrowed by a verified fact:** the pre-commit validator needs to cover **`UpdateTask` only**.
`AwaitHuman` has exactly **one** non-test emit site (`engine/step_nodes.go:806`) and
`performAwaitHuman` hardcodes `State: humantask.Unclaimed` without copying `Claim`
(`runtime/processdriver_action.go:439`), so an `AwaitHuman`-derived task cannot be
claim-invalid.

The three `Upsert` guards are **kept as defence-in-depth** — they are what protects a
consumer calling `Upsert` directly, which no pre-commit hook can reach.

### C1 — ACCEPTED. A fifth production writer, a bypass, and a read-modify-write wedge

The bundle grepped the four state *constants* and never the *field*. `humantask_store.go:363`
does `t.State = htParseTaskState(stateStr)`, whose `default:` returns `Unclaimed`, while
`scanTask` rebuilds `Claim` from `claimed_at` **independently of state**. Lens C executed it:

```
PROBE: Upsert(State=TaskState(99), Claim=alice) err = <nil>
PROBE: read back state=unclaimed claim=&{{alice [] map[]} …}
PROBE: R2 violated on read? true
```

Two consequences the bundle did not carry: `Validate` is **bypassable** (an out-of-range
`TaskState` is neither `Claimed` nor `Unclaimed`), and `Get` can return a value `Upsert` now
refuses.

**Accepted fix:** add a third rule — **R3**: `t.State` must be one of the four declared
constants — which closes the bypass; add the sixth row to the writer table; and scope the
"write-side is the only seam" claim to R1 (see C7).

## Accepted with a DIFFERENT fix than the lens proposed

### B2 — problem ACCEPTED, fix CHANGED

`Claimed` + **empty claimant** is admitted by R1 and reachable today via
`POST /tasks/{token}/reassign {"to":""}` — nothing validates `To`
(`transport/http/httpcore/dto.go:60-67`, `engine/trigger_validate.go:65`,
`runtime/task/service.go:214-231`). The result is a `Claimed` row invisible to every inbox
query: blocker 3's harm surviving the fix for blocker 3.

Lens B proposed extending R1 to require a non-empty claimant. **Accepted, but not as the
primary fix, and the ordering matters:** extending R1 *alone* would turn a live HTTP route
into a permanent store/state divergence returning repeatable 500s. The primary fix is
**trigger-level validation** of `HumanReassigned.To` at `engine/step.go:156`, which runs
*before* `cloneState` — "keeps a rejected trigger free of side effects" — so the request is
refused with no state touched at all. R1's extension then lands as defence-in-depth behind
the pre-commit hook, not in front of it.

⚠ **A new sentinel, not `ErrEmptyTriggerKey`.** That sentinel's own doc says *"An identity key
names one specific record"* (`engine/errors.go:212-218`, ADR-0152); `To` is a required field,
not an identity key. Reusing it would contradict its documented meaning. New sentinel + its
own 400 arm.

### C10 — ACCEPTED, narrowed

"A double-listed inbox row becomes **unrepresentable**" holds only for in-range states and
only through `Upsert`; rows written before the guard are not repaired. Wording narrowed.

## Rejected

### A11 — REJECTED. The "second package doc comment" lint risk is not a risk

Lens A flagged (as `PLAUSIBLE`) that `validate_test.go` opening with `// Package humantask_test …`
would be a second package doc comment. Verified: **both** existing files already do this —
`humantask/humantask_test.go:1` and `humantask/memory_test.go:1` — and lint is clean today. A
third cannot newly break what two already pass. The plan simply omits a package comment on the
new file for tidiness, but this is not a finding.

## Accepted — the guard's blast radius up the stack (all from lens B)

| # | finding | fix |
|---|---|---|
| B3 | `ErrInvalidTask` has no arm in `ClassifyError` (`transport/http/httpcore/errors.go`) → **500 with an empty body**, discarding the message that names the task and the contradiction. That file's own comments are the precedent that a sentinel reaching HTTP gets classified deliberately. | Add a 422 `conflict_state` arm (an engine-authored shape a caller cannot fix) and a 400 arm for the new reassign-target sentinel. |
| B5 | The writer enumeration is over the wrong set: the guard fires on **`UpdateTask` emit sites (8)**, not `State =` assignments. `step_triggers.go:612` (`handleHumanCandidatesResolved`) emits `UpdateTask` **without touching `State` or `Claim`** — a pure pass-through of whatever instance state holds, reachable from `service.RefreshTaskCandidates`. | Re-derive the table over emit sites; name `:612` as the pass-through that would carry a corrupted snapshot into the guard. |
| B6 | `Validate` is sold as a general predicate, but the engine deliberately mints `Completed`+nil+nil into instance state (`ManualImmediate`), reachable via `service.ProcessInstance.ActiveTasks()`. The deferred completion axis would reject a record the engine authored on purpose. | State that `Validate` is a **TaskStore-write** contract, not a whole-model invariant, and record that the deferred axis must carve out `ManualImmediate`. |
| B7 | `processtest/harness.go:345` exposes `Tasks() *humantask.MemTaskStore`, so consumer fixtures break; the break is **silent** for a consumer's own store (no signature change); and the contract is a documented MUST with nothing exported to verify it. | Name `processtest` in the CHANGELOG; state the silent-break; **export a conformance helper** (full scope) in the public `processtest` package. |
| B8 | Terminal sweep: `engine/state.go:645-659` emits one `UpdateTask` per open task, so one rejection drops tasks 2..N — a terminated instance still advertising them in the inboxes `cancelOpenTasks` exists to clear. | Same root cause as B1; closed by pre-commit validation. |
| B4 | Duplicate of A2 — see below. | — |

## Accepted — corrections to the bundle's own claims

| # | claim | correction |
|---|---|---|
| A1 | "`cancelOpenTasks` re-emits a `Claimed` task whose `Claim` was lost … the guard converts corruption into a loud error — a benefit" | **Refuted by execution.** `cancelOpenTasks` sets `State = Cancelled` *before* `Clone()`, and `Cancelled` has no claim rule, so the guard stays silent. The state-preserving re-emitter is `step_triggers.go:612`. Rewrite; drop the `ASSUMPTION` label (now executed, and half wrong). |
| A2 / B4 | The double-listing regression test | **Vacuous.** `require.ErrorIs` is `FailNow`: guard present ⇒ the inbox assertions are tautologies (a row never written cannot be listed); guard absent ⇒ the subtest aborts before reaching them. Fix: `assert.*`, **plus a positive control** upserting the same fixture in a legal shape and asserting the queries DO return it — the only way the claim earns evidence. Add a Task 2 mutation step. |
| A3 | Plan's `-run` trap guard ("no `---` lines means nothing ran") | **Does not work.** The parent test body runs, emitting two `--- PASS` lines; the real discriminator `no tests to run` is filtered out by the plan's own grep. Fix: assert leaf names and grep for `no tests to run`. |
| A4 / C4 / C5 | "the Postgres/MySQL legs did NOT run (no Docker probe)" | **False as labelled.** Docker was up; `dbtest.RunTestDatabase` **fatals** rather than skips, so container-backed tests in `persistence` and `runtime` did boot, and `-run '.*/sqlite'` still runs ~15 top-level Postgres/MySQL tests in the store package. Also `internal/persistence/store` was covered **only** by a filtered run, so "all seven ran" overstates. Relabel; the zero-churn conclusion stands (re-verified with a positive control, 281 subtests). |
| A8 | `ManualImmediate` never reaches a task store — `ASSUMPTION (unverified)` | **Executed and TRUE.** Promote to a measurement. Adjacent minor: `ManualImmediate` appears in no engine/runtime test, so Task 4's "must not move a single test" is true but vacuous — say so. |
| A9 | `Claimed`+nil is "inert" | Understates: it is lost from **every** inbox, reachable only by ID. A different consequence, not a smaller one. Stop using it to rank the directions. |
| A10 | — | `Unclaimed` is `iota` = the zero value, so R2 also rejects a task carrying a `Claim` whose `State` was never set. Document as deliberate in `Validate`'s doc and the CHANGELOG. |
| A12 | Task 2 Step 3 leaves the validation error unprefixed by the store's own `workflow-store:` wrapper | Keep the decision (it already names the task and contradiction); **record** it rather than leaving it implicit. |
| C2 | "**Both** `Upsert` call sites" | **Three**: `runtime/processdriver_action.go:468`, `:483`, and `persistence/caching_task_store.go:99` — the third being the one Task 3 modifies. |
| C3 | "**every case** assigns `task.Claim` before upserting" | **False.** Of that group's two cases, the claim case assigns `Claim`; the completion case instead reassigns `State` to `Completed`, which the rule leaves unconstrained. Zero-churn holds; the stated reason was wrong — the "check the fixture, not the line" class. |
| C6 | "plus **a** test double" | **Two**: `capturingTaskStore` (`runtime/processdriver_action_test.go:48`) and `countingTaskStore` (`persistence/caching_task_store_test.go:21`, satisfying the interface by embedding `MemTaskStore`) — the latter being the one Task 3 rewrites. |
| C7 | "**Write-side is the only seam**" | Holds for **R1 only**. For R2 the read path *constructs* the violation (C1), so a read-side guard is a second available seam. Scope the sentence; record the read-side option. |
| C8 | Inherited ADR-0148 doc: "`claimed_at` is NULL exactly when the task is unclaimed" | **False pre- and post-fix**: `htClaimBinds` keys on `c == nil`, and a `Completed`/`Cancelled` task with no claim has NULL `claimed_at` while not being "unclaimed". Correct it while editing that block (Delivery-Gate item 2). |
| C9 | "Re-derived uniformly across assignment **and** composite-literal forms: `Claimed` has **2** sites" | Self-contradictory — 2 is production-assignment-only (6 across both forms incl. tests; 35 literals). Also the spec table's "assignment" column lists `State = Unclaimed`, but both `Unclaimed` sites are **composite literals**. Retitle the column "production write site (either form)". |

## Confirmed with no defect (re-derived independently — recorded so the next reader need not redo it)

- The corrected `AssignedTo=1` **and** `ClaimableBy=1` for the `Unclaimed`+claim row is right; the earlier `ClaimableBy=0` was the confounded measurement (A5).
- Zero fixture churn holds, with a positive control proving the guard fires in both stores and both directions; a rejected `Upsert` persists nothing (A6).
- Plan Task 3's reasoning is exactly right: without the `upserts` counter the case passes on the backing `MemTaskStore`'s guard alone; with it, the prescribed RED and the Step-6 mutation message reproduce verbatim (A7).
- The choke point is genuinely clean: exactly **3** non-test `.Upsert(` sites, exactly **one** mutating SQL statement (`humantask_store.go:155`, inside `Upsert`), no other store method mutates claim state, `runtime/task/service.go` never writes, exactly **3** `TaskStore` implementations (`persistence/humantask.go:60` is a re-export assertion, not a fourth), and **no** generated `MockTaskStore` (B).
- Counts that held: `2/2/2/4` production state writers with both `Claimed` sites setting `Claim` on the immediately preceding line; **zero** `.Claim = nil` sites; **seven** `Upsert`-calling test packages; **35** `State: Claimed` fixtures; **one** `humantask` sentinel; **26** packages derivable; **all 27** file:line citations resolve with no rot; inherited blocker-3 / backlog-32 wording restated with hedges intact (C).
- `humantask.Validate` is reachable by consumers — root package, stdlib + `authz` only (B).

## Process findings (not bundle defects)

1. **Two lenses mutated the shared primary tree concurrently** (both patched the guard into
   `humantask/memory.go` and `humantask_store.go`). This violates the repo's own "fan out by
   Go package" rule and risks one lens restoring over the other's patch. Lens C's counts were
   taken against a dirty tree and it correctly used `git show HEAD:` to compensate.
   **Correction for the re-audit: any lens that must mutate gets its own `git worktree`, and
   the brief must say so** — the existing rule was stated for implementation agents and was
   not carried into the audit briefs.
2. **`git status` must be verified clean as step 0 of implementation.** With the guard already
   patched in, Task 1's prescribed RED (`undefined: humantask.Validate`) would not appear and
   the TDD audit trail would be lost.
3. All three lenses restored the tree; `git status` clean and `go build ./...` EXIT=0 at
   adjudication time.

---

# ADDENDUM — RE-AUDIT adjudication (2026-08-18)

The revised bundle was re-audited by two fresh lenses in **their own worktrees** (process fix
from round 1). **Lens D** attacked the new pre-commit seam by implementing it; **lens E**
re-counted the rewrite. Write-ups: `…-reaudit-lens-{d,e}.md`.

**Outcome: 21 further findings. One REVERSES a finding accepted in round 1.** Four Criticals,
two of them introduced *by the rewrite that removed round 1's false claims* — this repo's
signature failure mode.

## ⛔ REVERSED — R1's empty-claimant clause is DROPPED

Round 1 accepted lens B2's proposal to extend R1 to `Claim.Actor.ID != ""`. **That was wrong,
and my adjudication of it was itself an unverified claim.** Three independent reasons:

1. **It reverses ADR-0148 amendment 1 §4** (lens E-F2). The conformance suite carries
   `"claim with an empty actor id still reads back as a claim"`
   (`humantask_store_conformance_test.go:252-272`) seeding `Claimed` +
   `Claim{Actor{Roles: ["kiosk"]}}` under the comment: *"an empty claimant id is a legitimate
   value, and keying on it would resurrect the fabricated/dropped-claim bug of amendment 1 §4."*
   An empty claimant is a **deliberately blessed kiosk shape**. ADR-0183 nowhere supersedes it.
2. **It would delete a security regression test** (lens E-F1). `humantask/memory_test.go`'s
   `claimedByNobody` seeds that same shape through `Upsert` to guard a **data-disclosure
   footgun** — `AssignedTo("")` must not degenerate into a wildcard. Measured: `EXIT=1`.
3. **The surface was asymmetric anyway** (lens D1). A *second* live route mints an empty
   claimant — the **claim** verb. `handleHumanClaimed` (`engine/step_triggers.go:577-581`)
   writes `Claim{Actor: t.Actor}`; `validatedTriggerKinds["engine.HumanClaimed"]` checks TaskID
   only; `TaskService.Claim` runs only authz, which an empty-ID actor holding a matching role
   passes. `dto.go:40-44` documents it: *"No fields are required — an empty actor is allowed."*

**Kept instead: only the trigger-level refusal of `HumanReassigned{To: ""}`.** The two cases are
semantically different and the asymmetry is now deliberate and recorded:

- a **kiosk claim** is self-service with no identity but *carries roles* — legitimate per ADR-0148;
- an **empty reassignment target** is incoherent — reassignment moves a claim *to someone*, and
  `handleHumanReassigned` mints `Actor{ID: t.To}` with no roles either, i.e. genuinely nobody.

Bonus: dropping the clause **removes a nil-dereference panic hazard** on the pre-commit hot path
(lens D5 — see below), because the arm that dereferenced `Claim.Actor.ID` no longer exists.

⇒ **R1 is `State == Claimed ⟹ Claim != nil`.** Empty-claimant handling moves to Deferred.

## Accepted — Criticals and Majors from lens D

| # | finding | verdict and fix |
|---|---|---|
| **D2** | **"Pre-commit" means "before THIS ITERATION's commit".** `deliverLoop` is `for len(queue) > 0` (`processdriver.go:610`) and `perform` appends follow-up triggers (`:858`) — controller-verified. So an abort on iteration ≥2 leaves iteration 1 committed. **And lens D executed the precedent this ADR quotes as its justification and showed it DOES strand an instance** (`version` reaches 1 on a resolver outage, token parked, retry fails `instance already exists`). | **ACCEPTED.** The design stands — pre-commit removes the harm where the claim-shaped commands actually arise — but its *justification* was overstated. Qualify every "before anything is committed" → "before this iteration's commit"; **drop the inherited quotation as justification** (I restated it as fact without executing it — Premise Discipline); rename the prescribed test `…DoesNotCommitTheStep`; and **document the accidental invariant**: the hook is unreachable in iteration ≥2 today only because the two follow-up-reachable emitters (`state.go:656`, `step_stale_commands.go:171`) normalize `State` to `Cancelled`, which carries no claim rule. |
| **D3** | **NO WEDGE.** 4 corrupt shapes × 4 verbs plus a `CancelInstance` matrix: `claim`, `complete`, `compensate_terminate` and `CancelInstance` succeed on **every** shape; only `refresh_candidates` is refused. The guard does not refuse the operation that clears the bad state. | **ACCEPTED — record it.** This answers the re-audit's most important question in the design's favour and is its strongest argument. The bundle asserted it nowhere. |
| **D4** | "A contradictory shape can no longer be **committed**" is **false** — the hook validates *commands*, never the snapshot. An out-of-range record is re-committed (`Commands: nil` for a non-open task), and a terminated instance can leave a permanently-claimable row because `cancelOpenTasks` skips it via `IsOpen()`. | **ACCEPTED.** Reword to "can no longer be **authored**, nor reach a `TaskStore`". New residual: a durable out-of-range record is re-committed unchanged and is permanently unactionable (`claim`/`complete` both refused `ErrTaskNotOpen`). |
| **D5** | The plan's one ordering warning names the **wrong arm**. R3-last is byte-identical; the real constraint is arms 2↔3 — swapping them **PANICS** on a nil dereference. (Lens E-F4 independently found the same falsity.) | **ACCEPTED.** Delete the false warning. Moot for the panic now that the empty-claimant arm is gone, but restructure so any such dependency is structural rather than positional. |
| **D6** | The rejection is classified for **1 of 5** entry points. The **timer-fire** seam (`runtime/timerops.go:344-353`) logs ERROR and **drops** the error; `fn` returns nil so the scheduler believes the fire succeeded, no incident is raised, and the durable row is not deleted — so it re-fires and re-fails every boot, and deadline breaches / reminders silently stop. | **ACCEPTED.** One Consequences paragraph enumerating the five entries, stating plainly that the timer-seam rejection is a **silent drop with no incident**. |
| **D7** | The abort skips no cleanup (verified: it precedes metrics, `commitFn`, scheduler activate, `syncWaiters`, the `perform` loop; `span.End()` explicit, `defer release()` still runs). But an iteration-≥2 abort leaks `instActive`, and the returned `st` is the **contradictory, uncommitted** projection. | **ACCEPTED** (minor). Record both; the `instActive` leak is pre-existing and live today on the resolver path. |
| **D8** | R3's `State < Unclaimed \|\| State > Cancelled` is exact today but silently coupled to `iota` contiguity. | **ACCEPTED.** Use an **enumerated switch**, matching `TaskState.String()`'s house style. |
| **D9** | The plan's corrupted-snapshot fixture **is** constructible — `AppliedStep{State, Trigger}` suffices, `Clone` guards `if t.Claim != nil` so nil survives, and `version 2→2` vs `2→3` is a discriminating assertion. | **ACCEPTED.** Stop hedging; add the wrinkle that the fabricated `AppliedStep.Trigger` lands in the **journal**, so any `Entries()` length assertion must account for it. |
| **D10** | Ordering vs `resolveHumanCandidates` is safe: it writes exactly one field (`task.Candidates`) and cannot mutate the commands — `AwaitHuman` is `{TaskID, Eligibility}` with no Candidates field. `UpdateTask` is the **only** command carrying a `humantask.HumanTask`, so "UpdateTask only" is **structurally complete**. | **ACCEPTED as confirmation.** Residual: on rejection the span ends without `wrkflw.command_count` / `wrkflw.status`. |

## Accepted — lens E

| # | finding | verdict and fix |
|---|---|---|
| **E-F1** | "Zero fixture churn, measured" was **FALSE** for the revised guard — measured against the *two-rule* guard and restated for a guard that had gained two rules. | **ACCEPTED and RE-MEASURED.** With the clause dropped, churn is **genuinely zero** across all seven `Upsert`-calling packages, `docker info` probed **up** so the Postgres/MySQL legs ran, with a **four-axis positive control** whose fourth row proves the kiosk shape stays legal. Recorded in the evidence file. Pre-commit-hook and `Step` churn remain `UNVERIFIED`; lens E independently derived `Step`'s as zero (16 `NewHumanReassigned` call sites, none with an empty target). |
| **E-F3** | The plan's Docker warning on Task 4 Step 2 is **false** — element 0 of a `-run` pattern *does* filter top-level names, so `/sqlite` never boots containers. Count also wrong: **20**, not ~15. | **ACCEPTED.** Drop the warning from Step 2, keep it on the unfiltered Step 4, and attach "20" to `-run '.*/sqlite'` where the claim is true. |
| **E-F5** | The rewrite's replacement doc comment introduces a **NEW false biconditional**, and half-fixes C8 — the sibling sentence about `completed_at` is false by the identical mechanism. | **ACCEPTED.** Third instance this delivery of an edit removing a false claim adding one. Fix both halves: both columns key on the **pointer**, never on `State`. |
| **E-F6** | Spec, ADR and plan carry **three different Deferred sets** (3 / 4 / 3-different). | **ACCEPTED.** The spec's list becomes canonical; ADR and plan point at it. |
| **E-F7** | The adjudication's fix instructions cite the **old** task numbers. | **ACCEPTED.** Mapping: old Task 2 → new Task 4 (store/conformance); old Task 3 → new Task 5 (caching); old Task 4 → new Task 2 (engine). |
| **E-F8/F9/F10/F11** | Spec omits one of the ADR's six enforcement points; the new sentinel is never *named* outside the plan; 4 of ~30 new citations rotted (`processdriver_action.go:281`→282/284, `step_nodes.go:755`→751 for the comment, `step.go:156`→158 for the insertion point, `conformance_test.go:531`→532); "fifth writer" is a *kind*, not a site count (9 sites). | **ACCEPTED**, all four. |

## Confirmed correct — do not re-derive

Eight `UpdateTask` emit sites with **all eight line numbers exact**, and `:612` really is the only
pass-through. The fifth-writer mechanism. `AwaitHuman`'s single emit site and
`performAwaitHuman`'s hardcoded `Unclaimed`. Three `.Upsert(` sites, three implementations, two
doubles, one mutating SQL statement. Zero `.Claim = nil` sites. 35 `Claimed` fixtures. Seven
`Upsert`-calling packages. **Only one non-test `engine.Step(` caller** (`processdriver.go:632`), so
the single hook does cover every path. `TaskState` is signed with four contiguous constants. The
plan's nine `TestValidate` rows all pass the prescribed switch and `PASS=10` is right. ~30 further
citations exact.

## Process

⚠ **I was the concurrent mutator this round.** Lens D correctly reported the primary tree dirty
with files that were not its own — they were mine, from the churn re-measurement (E-F1). I had
restored them and verified `git status` clean, but D observed the window. The round-1 rule I wrote
into these very briefs — *an agent that must mutate to measure gets its own worktree* — **applies
to the controller too.** Both worktrees were removed; `git worktree list` shows only the primary.

**Verdict: no third audit.** The design is not gaining mechanism — it is **shrinking** (a rule
dropped), and everything else is documentation correction. Both audits' Criticals are folded.

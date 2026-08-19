# wrkflw — Handover

Current state and the next work, for a session with zero prior context. Read it
top to bottom; it is meant to stay short enough that you can.

> **Maintenance rule: rewrite this file IN PLACE. Never append.**
>
> Its predecessor became a 2057-line append-only stack of twenty "PREVIOUS RESUME
> POINT" blocks and was silently abandoned for 45 ADRs — see
> `docs/plans/HANDOVER-archive.md`. Per-delivery detail does **not** belong here:
> it belongs in that delivery's plan under a `▶ Progress` block, where it dies
> with the plan. This file carries only: where `main` is, what is unmerged, and
> what to do next.

## State — updated 2026-08-18

**▶ IN FLIGHT: ADR-0184 (backlog 42 + 43). Tasks 1–4 IMPLEMENTED (Task 4 = docs and backlog, all 5
steps done) and reviewed clean; the Delivery Gate remains — `/code-review` and `/security-review`
are OWNER-INVOKED and have NOT run.**

Branch `feat/test-wait-budget-and-conformance-completeness`, cut from ADR-0183's merge `a7575ed5`.
Every step **amends the one bundle commit** — do not quote its SHA, it moves. **Not pushed.**

Read `docs/plans/2026-08-18-test-wait-budget-and-conformance-completeness.md` — its `▶ Progress`
block carries the per-task verdicts and the three corrections implementation forced on the plan.

Rule-#9 audit **passed before any code**: 3 Opus lenses, **14 findings, all accepted** —
`docs/specs/2026-08-18-adr-0184-audit-adjudication.md`. The Critical was the controller's own
prediction, refuted independently by all three lenses **because they executed it**.

⚠ **Things a fresh session must not get wrong:**

- `RunTaskStoreConformance`'s coverage **stays at 77.8 % and that is CORRECT** — the two misuse
  guards run in a **child** `go test` spawned without `-coverprofile`, so their counters are
  discarded. **Do not "fix" it**; the only way to raise it is the interface seam ADR-0184 rejected.
- The budget is **10 s, not 30 s**, sized against the *binary*: 31 predominantly serial sites (2 of
  the 31 run under `t.Parallel`) in `scheduler/internal/gocron` × 30 s = 930 s would blow `go test`'s
  600 s default and replace 31 legible failures with a goroutine dump. Rule: `budget × densest
  package's site count < 600 s`.
- `writeOnlyTaskStore`'s pinned count is **1** and must stay 1 — an early return at the read-back
  guard makes the new inbox check unreachable for it. This is backlog **47**.
- **13** test files were modified, not 15; the 2 `Never`-only `clock_option_test.go` files are
  deliberately untouched.
- ⚠ **Any check over Go call arguments must be paren-balanced.** Three fixed-context-window greps
  in this delivery's plan were wrong; two shipped into the plan before being caught by execution.
- ⚠⚠⚠ **Backlog 42 was MISDIAGNOSED and is closed by a RACE FIX, not by the budget change.**
  `ScheduleJob` returned a zero next-run with a nil error for a past-due one-shot. Measured (fresh
  re-derivation, 7 runs × 1,000 arms each): **~12 % of arms without `-race`**, **~0.9 % under
  `-race`** — roughly 13× apart; a single "~20 %" figure once attributed to `-race` alone was
  wrong. Post-fix the branch cannot return zero by construction, so "0 of N after" is not a
  comparable rate. The budget change addresses a real but *separate* class at the other 39 sites.
  ⭐ **A test that fails in 0.00 s is not waiting out a timeout** — that one number refuted a
  premise that had survived a three-lens audit.

---

**ADR-0183 has shipped and `main` is clean and pushed.**

ADR-0183 shipped as merge **`a7575ed5`**, pushed; its branch `feat/human-task-claim-invariant` is
deleted. ⚠ Do not quote main's head — it moves; re-derive with
`git rev-parse --short refs/heads/main`. Anchor on the **merge** SHA above, which never moves.

**Both gates passed before merge:**

- `/code-review high --fix` — **4 findings, all closed** (2 fixed by the reviewer; 2 it deferred to
  the owner, which the controller accepted and fixed). Table in the plan's `▶ Progress`.
- `/security-review` — **0 findings.** Judged net security-**positive**: the new empty-`to` early
  return is not an oracle and in fact **removes** a pre-existing 404-vs-403 existence oracle; the new
  422 message carries only `TaskID` + state (no actor IDs, `AuthzSpec`, `Candidates` or `Vars`); the
  403 authz arm is evaluated **before** it; task visibility only tightens; and the `Upsert` SQL is a
  compile-time constant with `State` as a **bind parameter**.

**Verification, all executed:** `go test -count=1 ./...` **EXIT=0** · `golangci-lint run ./...`
**repo-wide EXIT=0, 0 issues** · all container-backed packages `-race` **EXIT=0** (`store` 72.4 s,
`persistence` 34.3 s, `internal/database` 35.7 s, `runtime` 21.9 s, `scheduler` 23.6 s) · the new
conformance group proven to run on **all three dialects** (sqlite / postgres 1.83 s / mysql 6.35 s,
`no tests to run` = 0). Coverage: `humantask` 100 % · `httpcore` 94.6 % · `runtime/task` 94.3 % ·
`runtime` 93.8 % · `engine` 93.0 % · `processtest` 91.8 % · `store` 87.5 % · `persistence` 84.1 %.

⚠ **`/security-review` labelled ITSELF partial** (container-free subset only, SQL guard checked by
reading). That caveat is **closed by execution** — see the dialect run above. A gate that scopes its
own evidence must have that scope closed or restated, not inherited as if it were full.

⚠ **`RunTaskStoreConformance`'s signature changed during review** to
`newStore func(t *testing.T) humantask.TaskStore` — the factory needed the case's own `T`, because
the README's own pattern captured the **parent** `T` inside a child subtest and a setup failure then
called `FailNow` cross-goroutine, truncating the run at **1 of 8** shapes. Taken pre-merge
deliberately: free now, a breaking change to public API after.

### What ADR-0183 is, in one paragraph

`humantask.HumanTask` has always documented *"`Claim` … nil when Unclaimed"* on its own field and the
read path upheld it; the **write** path upheld nothing. `Upsert` bound state and claim columns
independently, so `State: Claimed, Claim: nil` round-tripped, and an `Unclaimed` row **carrying** a
claim was returned by `AssignedTo` *and* `ClaimableBy` — double-listed. Now `humantask.Validate`
(R1/R2/R3) is the single definition, enforced **pre-commit** in the runtime and in all three bundled
`Upsert` implementations as defence-in-depth; an empty reassignment target is refused in `Step`
before `cloneState`; both new sentinels are HTTP-classified; and consumers get an exported
`processtest.RunTaskStoreConformance`. Closes **blocker 3**.

### ⚠ Things a fresh session must not get wrong

- **BREAKING three ways** — `CHANGELOG.md` ▸ Unreleased ▸ Breaking changes is authoritative. For a
  consumer's **own** `TaskStore` the break is **SILENT**: no signature change, so nothing recompiles
  differently and a non-conforming store keeps accepting bad rows.
- ⚠⚠ **An empty claimant is LEGAL on every state** — ADR-0148 amendment 1 §4's kiosk shape. Round 2
  of the audit REVERSED an earlier decision to reject it. Only the empty *reassignment target* is
  refused; the empty-ID **claim** route is deliberately untouched. Six stale fragments across the
  plan/spec/ADR still said otherwise and were corrected before implementation — **if you find a
  seventh, it is stale, not a spec.**
- **`Completed`/`Cancelled` carry NO claim rule.** The completion axis stays deferred, and it must
  carve out `ManualImmediate`, which mints `Completed` + nil + nil on purpose.
- **Existing rows are NOT repaired** — no migration, no backfill.
- **The ADR was amended BY implementation**: audit finding **B8 is REFUTED**. See below.

### ⚠ The process lesson this delivery earned

**A twice-audited bundle still prescribed a test that could not fail.** Audit finding B8 asserted
that a post-commit rejection drops a terminal sweep's remaining reconciliations; both rounds accepted
it and the plan prescribed a regression test. Implementation measured that `cancelOpenTasks`
normalizes **every** swept task to `Cancelled`, which is unconstrained on the claim axis — so the
sweep emits no rejectable command and the test could never have gone red. The ADR *already recorded
this exact normalization* for follow-up emitters; **neither audit round carried it across to B8, one
paragraph away.**

⭐ **When a bundle records a normalization that immunizes a path, re-check every OTHER finding that
asserts a defect on that same path.** The fact was present and correct; nobody re-applied it.

Second lesson: **the controller's own briefs carried a false quantifier again** (4 of 6 wrong counts
on ADR-0179 were also the controller's). Here it was "every rejection fixture MUST declare Candidates
+ Eligibility or the inbox assertions cannot fail" — unachievable for `Claimed`+nil, where neither
inbox can fire regardless of fixture. The agent complied *and* flagged it. **Re-derivation is a
per-edit activity, not an audit-time one.**

Third: ⚠⚠⚠ **the controller's brief was wrong THREE times in the review round — third delivery
running.** The worst: "for an out-of-range state only `AssignedTo` fires" was measured in the
*internal* conformance suite, whose fixture carries a claim — but the **exported** helper's
out-of-range fixture carried a **nil claim**, so neither inbox query could reach it and the new
assertion would have been **unfailable for that shape**. ⭐ **A measurement inherited from a SIBLING
context must be re-derived in the TARGET context; two suites' fixtures are not the same fixtures.**

Fourth: **an agent caught itself pre-commit** about to state that a sibling package validated, while
that sibling was still in flight. Grepping before writing a comment about another package's present
state is the cheap version of this repo's most expensive recurring bug.

### Verification — executed, real numbers

`go test -count=1 ./...` **EXIT=0**. `golangci-lint run ./...` **repo-wide EXIT=0, 0 issues**.
Touched-package coverage: `humantask` 100 % · `httpcore` 94.6 % · `runtime` 93.8 % · `engine` 93.0 % ·
`processtest` 91.6 % · `store` 87.5 % · `persistence` 84.1 %.

⚠ **Two sub-floor/red results, BOTH proven pre-existing by execution rather than excused:**

1. **`persistence` 84.1 % < the 85 % floor.** Measured on unmodified `main` in a throwaway worktree:
   **also exactly 84.1 %.** This is backlog **34**.
2. **`TestGocronScheduleJobTriggers/At_(past-due)_…time-skew_branch` fails under full-suite `-race`
   contention.** This delivery touches **zero** `scheduler/` files; both trees pass `-race -count=25`
   isolated; and the identical failure **reproduces on unmodified `main`** under the same full-suite
   run. New backlog item **42**. ⚠ **Do not silence it.**

**Latest ADR = 0184 (implemented, not merged). ADR-0183 is shipped and merged.
Next free = 0185.** ADR numbers 0155–0157 remain reserved by the parked
`feat/durable-waiters-delivery-correctness`.

### ▶ NEXT WORK

**Finish ADR-0184's Delivery Gate.** Tasks 1–4 are done; what remains is owner-invoked and cannot
be run by an agent:

1. `/code-review` on the pending change — fix all findings, fold via `--amend`.
2. `/security-review` — fix all findings, fold via `--amend`.
3. Merge `--no-ff` to `main`, push, delete the branch.

⚠ The final whole-branch review flagged four deferred Minors — they are inlined in the plan's
`▶ Progress` block (`docs/plans/2026-08-18-test-wait-budget-and-conformance-completeness.md`), not
in the gitignored SDD ledger. The strongest is **Task 2's**: `TestRunTaskStoreConformanceRefusesANilStore`
never asserts that every case still got its turn. ⚠ Its ORIGINAL justification was FALSE and was
corrected during the final review-fix wave: `attributedTest`'s existing `strings.HasPrefix(…,
nilStoreHelper+"/")` assertion already catches a parent-level `FailNow` regression, so the real
split is **8 normally / 0 under that mutation**, not 8/1 — the gap is real, the reason given for it
was not.

⚠ **Blocker 3 is CLOSED by ADR-0183 — do not re-raise it.** Backlog **44–48** are opened by this
bundle and are already recorded in the Backlog section below.

**After ADR-0184**, the strongest candidates, roughly in order:

- **Backlog 32 — downgrade drops new state fields** (whole-state `json.Marshal`, no
  `DisallowUnknownFields`). Still the highest-stakes item: a dropped `RetryAttempts` **resets the
  retry budget so a poison compensation retries forever**, and a dropped `IncidentKind` degrades a
  walk-scoped incident into a *resolvable, deletable* one. ⚠ ADR-0183 raises the stakes again — a
  decode that drops only `State` now yields an `Unclaimed` task **carrying a claim**, which
  `Validate` rejects on write, so a downgrade can turn a readable row into an unwritable one.
- **The fail-open `AuthzSpec`** (under blocker 1) — an empty spec, `eligible_roles: []`, a bare key
  and `null` all parse cleanly and mean **allow-all**. Wants its own ADR; ADR-0167 did **not** close
  it, and ADR-0183 deliberately did not touch it either.
- **Blocker 7 — suite speed.** `internal/dbtest`'s `sync.Once` boot fires per package → 12 Postgres +
  7 MySQL boots. ADR-0183 paid that cost repeatedly (`store` alone is 72 s under `-race`). Fix:
  honour `WRKFLW_TEST_POSTGRES_DSN` / `WRKFLW_TEST_MYSQL_DSN` with testcontainers as fallback, plus
  `scripts/testdb.sh up|down` and CI wiring. Interacts with backlog 42.
- **Blocker 8** — the `forceTerminate` → `endInstance` boundary sweep is entirely uncovered.

⚠ **Cheap follow-ups opened by ADR-0183**, if you want a small delivery: backlog **42** (the
load-flaky gocron test — do NOT silence it) and **43** (`RunTaskStoreConformance`'s two untestable
misuse guards). Also flagged and deliberately not done: the exported conformance helper makes no
*positive* inbox assertion on its legal leg, while the internal suite does.

## Pre-v0.1.0 blockers

1. ✅ **Strict definition decoding — CLOSED by ADR-0167.** ⚠ Does **not** close the fail-open
   `AuthzSpec`: an empty spec, `eligible_roles: []`, a bare `eligible_roles:` and
   `eligible_roles: null` all parse cleanly and mean allow-all. Own ADR.
   🚨 **Before DEPLOYING ADR-0167**: audit stored definition rows for 5 pre-ADR-0144 camelCase keys
   (`compensateAction`, `compensationAction`, `completionAction`, `correlationKey`, `messageName`).
2. ✅ **A never-due timer arm — CLOSED by ADR-0176, ON `main`.**
2b. ✅ **The `scheduler.Activate` livelock on `Monthly(12,[31])` — CLOSED by ADR-0176, ON `main`.**
3. ✅ **`Upsert` can persist `State: Claimed, Claim: nil` — CLOSED by ADR-0183, MERGED to `main`**
4. ✅ **ADR-0159's misnamed symbols — CLOSED.**
5. **`TestPgxNotifierListenDrainsBeforePollInterval` is load-flaky**
   (`internal/persistence/store/notifier_pgx_test.go`). Interacts with item 7; **do not silence it.**
6. ✅ **`processtest` cannot drive an arm-only park — CLOSED by ADR-0166.**
7. **Suite speed.** `internal/dbtest`'s `sync.Once` boot fires per package → 12 Postgres + 7 MySQL
   boots. Fix: honour `WRKFLW_TEST_POSTGRES_DSN` / `WRKFLW_TEST_MYSQL_DSN` with testcontainers as
   fallback, plus `scripts/testdb.sh up|down` and CI wiring.
8. **The `forceTerminate` → `endInstance` boundary sweep is entirely uncovered.**
9. ✅ **`Park.HasArmedTimers` missed four arm sources — CLOSED by ADR-0177, ON `main`.**

## Backlog

⚠ Items **16** and **3g** are closed by bundle C (pending its merge).

**Pre-existing defects (measured, unclaimed):**

3b. **The cancel path flips `s.Timers` from nil to an empty non-nil slice.** Pre-existing shape drift.
3d. **The instance document gains fields** (`incidents[].kind`, `compensating` object) — additive.
3e. **`service.Service` gained a method** (ADR-0175) — BREAKING for a decorator; needs a release note.
3f. **The stall incident's `ScopeID` is empty for a TARGETED compensation throw** — read `NodeID`.

**Deferred from ADR-0175's design:** 4. a per-node `CompensationStallAfter` tier **and a per-node
compensation retry tier** (bundle C ships one engine-wide policy). 5. a bound on repeated `retry`.
6. whether stall detection should default ON.

**From ADR-0174:** 7. a pre-ADR-0171 unpinned cursor keeps ADR-0173's accepted double-run at the
`endInstance` harvest — wants a cursor-migration ADR. 8. records stranded on pre-ADR-0174 rows stay
unreachable. 9. ADR-0164's "eight terminal sites" is stale — ten today. 10.
`compensationRecordsForScope` reads an open scope as a records-exist decision.

**From ADR-0158/0172:** 11. a flow targeting a NON-EXISTENT node parks a permanent wedge. 12.
`PendingCancel=true` survives onto a `Running` instance and **will terminate the NEXT throw or
reverse walk**. 13. micro mode loses a signal delivery. 15. `engine/step_nodes.go`'s nested
arm retirement is uncovered.

**From ADR-0168–0171:** 17. the event-sub-process hole's remaining direction. 18. ADR-0171's two
open bounds. 19. ✅ **`processtest.Classify` has no reason for a compensation-walk park — PARTLY
CLOSED by bundle C**: a compensation-failure park is now drivable, but there is still no
`ReasonCompensation` of its own. 20. repo-wide coverage (⚠ `service` ~52.6 %). 21. **`AUDIT.md`**
on `docs/architecture-audit`, ⚠ NOT on `main` and NOT pushed (public repo, unfixed Critical/High
findings); claims **unverified**.

**From ADR-0176:** 24. a refused arm leaves an in-memory `timerRecord` with no durable row. 26. the
calendar scan is still linear in `interval`. 27. the definition store round-trips semantically
invalid definitions. 28. a weekday set mixing in/out-of-range weekdays changed answer — **release
note**. 29. the arm guard is not atomic with the arm. 30. `weeklyNext`'s `int(interval)*7` overflows.

**From ADR-0177/0178/0180:** 31. three dangling ADR-section citations. 32. **downgrade drops new
state fields** — whole-state `json.Marshal` with no `DisallowUnknownFields`. ⚠ **Bundle C raises the
stakes**: a dropped `RetryAttempts` **resets the retry budget so a poison compensation retries
forever**, and a dropped `IncidentKind` degrades the new incident into a *resolvable, deletable*
`IncidentAction`. 33. **`ProcessDriver.CancelInstance` still answers `err=<nil> status=terminated`**
on an already-terminal instance; `service` guards it with `ErrConflict`, the driver does not.

**From ADR-0181/0182:** 34. `persistence` under the 85 % floor — close it by testing the **advisory
lock**, not the option setters. 35. ADR-0182's gate cannot judge the legacy flat trigger strings.
36. Cron is out of scope for the never-due gate (owner decision).

**🆕 opened by bundle C:**

37. **A compensation retry timer lost at boot still strands the walk.** Un-prunability closes the
    retention-job route; a row skipped by `jobStore.Load` or never rehydrated is not covered.
    Escape is ADR-0175's operator verbs.
38. **`Incidents[0]` is read positionally by FOUR sites**, not three. Bundle C fixed the two
    `runtime` resolvers via an allow-list and de-positionalised `processtest`'s `incidentNode`.
    ⚠ **Still open: `examples/scenarios/admin_monitoring/main.go`**, which feeds the value to
    `ResolveIncident` and will now hit `ErrIncidentNotResolvable` on a walk-scoped incident.
39. **A leaked `TimerCompensationRetry` row is permanent.** `PruneTimers` excludes the kind and
    `ReclaimNeverDueTimers` only matches recurring `trigger_kind`, so no bulk sweep can reach it.
    Intended for a live walk; unbounded accumulation for a row leaked by an instance that died.
40. **`engine.NewActionFailed` gives a consumer a ZERO retry backoff.** The delay is
    `JitterFraction * Backoff(attempt)` and the public constructor defaults `JitterFraction` to 0.
    The shipped `ProcessDriver` passes a real fraction (full-jitter — correct by design), but this
    is **public API of a library** and a direct caller gets an immediate retry. ⚠ `engine/trigger.go`
    also documents zero as "no jitter", which is false — zero means zero *delay*.
41. **No `ReasonCompensation` in `processtest`** — see backlog 19.

**🆕 opened by ADR-0183:**

42. ✅ **CLOSED by ADR-0184 — and it was MISDIAGNOSED.** It was filed as "load-flaky under
    `-race` contention", a second instance of blocker 5's class. It is not. Measured: the test fails
    at `trigger_test.go:306` (`require.False(next.IsZero())`) in **0.00 s** — the `Eventually` never
    runs, and the same assertion exists pre-ADR-0184. The real defect was a race in `ScheduleJob`,
    which returned a zero next-run with a **nil error** for a past-due one-shot (`job.NextRun()`
    raced the immediate fire). Measured (fresh re-derivation, 7 runs × 1,000 arms each, against
    reverted code): **~12 % of arms without `-race`**, **~0.9 % under `-race`** — roughly 13× apart,
    not the single "~20 %" a code-review pass found mislabeled as a `-race`-mode figure. Post-fix
    the branch cannot return zero by construction (returns the captured clock reading
    unconditionally), so "0 of N after" is not a comparable rate. ⚠ The misdiagnosis was inherited
    from ADR-0183's handover, restated as fact in ADR-0184's first draft, and **survived a
    three-lens audit** — its execution lens ran the test `-count=25` **in isolation**, where it
    passes. ⭐ **A test that fails in 0.00 s is not waiting out a timeout.**
43. ✅ **`RunTaskStoreConformance`'s two `t.Fatal` misuse guards — CLOSED by ADR-0184.** Executed via
    the subprocess harness the package already had; no production change, no interface seam.
    ⚠ Its coverage still reads **77.8 %** and that is CORRECT — child `go test` counters are not
    merged. Do not "fix" it.

**🆕 opened by ADR-0184:**

44. **The 16 `Never` sites in `scheduler/` are vacuity-prone under contention** — "did not fire
    within 150 ms" passes trivially if the goroutine never ran at all. The mirror image of backlog
    42, and deliberately untouched by it (a `Never` budget is paid on every GREEN run, so it cannot
    simply be raised). Measured distribution: 100 ms×1, 150 ms×10, 200 ms×4, 300 ms×1.
45. **Blocker 5 and the `runtime/` `Eventually` sites are the same class, unconverted.** Adopting
    ADR-0184's pattern there adds a 5th copy of the constant and may justify promoting it to a
    shared `internal/` package.
46. **A `testing/synctest` spike for `scheduler/`** — Go 1.25's bubble could remove real-time
    budgets entirely, if gocron's internals tolerate one. Research, not a flake fix.
47. **`checkTaskStoreConformance` stops at the first break on the legal leg**, contradicting its own
    doc comment (*"never stops early: a store gets told about all of its contract breaks in one
    run"*). Measured: a store that accepts but never persists is told about the read-back miss and
    **not** about its broken inboxes. Surfaced as the root cause of a wrong prediction in ADR-0184's
    own bundle. Changing what an exported helper reports is its own decision.
48. **CI runs `go test` at the 600 s default timeout** — neither `.github/workflows/ci.yml` nor
    `scripts/coverage.sh` passes `-timeout`. Nothing enforces ADR-0184's
    `budget × densest-package site count < 600 s` rule, so a future budget raise or a new batch of
    `Eventually` sites can silently cross it. Wants a `-timeout` flag or a guard test.
49. **A past-due one-shot expressed as a DURATION (`After(-1m)`, `After(0)`) is NOT fired
    immediately** — unlike the absolute-time (`At`) form ADR-0184 Decision 6 hardened. gocron
    refuses it outright and a raw `gocron: OneTimeJob: start must not be in the past` error escapes
    `ScheduleJob` without a `workflow-scheduler:` sentinel wrap. Reachable via
    `runtime/timerops.go`'s `KindOneTime` mapping. Pre-existing, separate from backlog 42's race.
    Reproduction (verified):
    ```go
    s.ScheduleJob(ctx, "id", sched.After(-1*time.Minute), task, false)
    // next=0001-01-01 00:00:00 +0000 UTC
    // err=gocron: OneTimeJob: start must not be in the past
    ```

**🆕 opened by /code-review on ADR-0184's bundle (the `closed` guard added late in that delivery):**

50. **`GocronScheduler.Close`/`CloseWithContext`'s `closed` guard has a residual window**
    (`scheduler/internal/gocron/job_schedule.go`, `ScheduleJob`'s doc and
    `scheduler/internal/gocron/scheduler.go`, `ErrSchedulerClosed`'s doc). `Close` sets
    `closed=true` under `s.mu` but calls gocron's `Shutdown()` **outside** `s.mu`. A `ScheduleJob`
    call that acquires `s.mu` first blocks `Close` for its entire body, observes `closed==false`
    throughout, registers successfully (including a past-due one-shot's `fireImmediately` branch),
    and returns a non-zero next-run with a **nil** error — then `Shutdown()` runs immediately after
    `ScheduleJob` releases `s.mu` and can retire the underlying gocron scheduler before that job's
    own goroutine has fired, orphaning a call that reported success. `NativeScheduler.Schedule`
    (`scheduler/scheduler.go`) has already persisted the durable row by the time `activateJob`
    reaches this call, so the orphan survives as a durable row for a job that never fires.
    Non-trivial to close: holding `Shutdown()` under `s.mu` risks a deadlock against the
    `AfterJobRuns` listener (also `s.mu`-guarded); re-checking `closed` after `NewJob` does not help
    either, since `ScheduleJob` holds `s.mu` for its whole body so `closed` cannot change mid-call.

## Where things live

| | |
|---|---|
| `main` | **ADR-0183 merge `a7575ed5`** is the newest shipped code, pushed. ⚠ Never quote main's HEAD; re-derive |
| bundle C (ADR-0179) | ✅ shipped — its spec/plan/ADR/audits/adjudication are all on `main` under `docs/` |
| ADR-0183 | ✅ shipped — merge `a7575ed5`; spec/plan/ADR/audits/adjudication all on `main` under `docs/`; branch deleted |
| *(merged branches)* | Deleted once pushed. **`origin` carries only `main`** plus dependabot. ⚠ **Every unmerged branch below exists on this machine ONLY** |
| `backup/terminal-trigger-guard-presquash` | `a3aa889` — ADR-0165 pre-squash, provenance only |
| **`parked/scope-and-fanout-design`** | ⚠ **SUPERSEDED — do NOT use as an input** |
| `feat/signal-arm-fanout` | `67cb055` — superseded packaging, kept for its audit tags |
| `feat/durable-waiters-delivery-correctness` | `434535d` — parked, docs only; holds ADR **0155–0157** |
| `docs/architecture-audit` | `393e516` — `AUDIT.md`, ⚠ deliberately NOT on `main`, NOT pushed |
| worktrees | ✅ **CLEAN** — verified after ADR-0183; the two throwaway `main` measurement worktrees were removed |
| Latest ADR | **0184** implemented, NOT merged. Next free is **0185** |
| v0.1.0 | not tagged |

## Standing constraints

- **Docker: standing permission for the Verification coverage + no-regressions runs** (owner,
  2026-08-11). Probe and run; if unavailable, say so and let the owner start it or skip, labelling
  any container-free subset as the partial result it is. ⚠ Everything else asks, and **a subagent
  brief must say so explicitly**.
- **`golangci-lint`: probe and run; if absent, offer to install or skip** — never substitute
  `go vet`, never claim "lint clean" for a run that did not execute.
- **Container-free packages**: `engine`, `runtime/{calllink,signal,task}`, `service`, `processtest`,
  `transport/http`. ⚠ **`./runtime/...` as a whole is NOT**, and ⚠ `internal/persistence/store` is
  NOT — **but `RunTestSQLite` is pure-Go and starts no container**.
- ⚠ **`go vet ./...` compiles every test file**, including Docker-only ones — cheap proof that a
  breaking type or symbol change has no hidden consumer. It compiles them; it does not run them.
- **Judge a test run by its exit code**, never a pipeline tail; use `-count=1`.
- **Run the suite on the MERGED tree**, and re-run after any `/code-review` fix.
- `/code-review` and `/security-review` are **owner-invoked only**.
- **Fan out subagents by Go package.** A delivery entirely inside one package runs **strictly
  serial**. ⚠ Bundle C's wave 2 ran three packages concurrently and one agent saw a transient build
  failure from another's mid-edit file — expected, cleared on retry, but do not read a `go build`
  failure as your own during a parallel wave.
- **An agent that must measure against a patched tree gets a `git worktree`**, the brief must say
  so, *and* must require verifying the bundle is present as step 0.
- ⚠ **Brief long-running agents to persist findings per finding, before the next probe.**
- ⚠ **Restore a mutation from a `cp` backup, never `git checkout <path>`.**
- ⚠ **`git reset --soft main` on a branch cut from an OLDER main stages a REVERT.** Check
  `git merge-base HEAD main` before any soft reset.
- Push on merge (standing preference).

## Where the detail lives

- **Per-delivery state** — the `▶ Progress` block at the top of that delivery's plan.
- **Decisions** — `docs/adr/NNNN-*.md`, Nygard template.
- **Conventions and gates** — `CLAUDE.md`, including **Premise Discipline**.
- **Pre-2026-07-08 history** — `docs/plans/HANDOVER-archive.md`, frozen.

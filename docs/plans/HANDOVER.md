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

## State — updated 2026-08-19

**▶ NOTHING IS IN FLIGHT. `main` is clean and pushed; ADR-0184 has shipped.**

ADR-0184 shipped as merge **`be6e6b55`**, pushed; its branch
`feat/test-wait-budget-and-conformance-completeness` is deleted. ⚠ Do not quote main's head — it
moves; re-derive with `git rev-parse --short refs/heads/main`. Anchor on the **merge** SHA above,
which never moves.

**Both gates passed before merge:**

- `/code-review high` — **2 findings, both fixed.** Both were fallout from the delivery's own late
  `ErrSchedulerClosed` guard (see below).
- `/security-review` — **0 findings.** Checked and cleared: the `os/exec` child-process spawning in
  the conformance factory test (all argv values are compile-time constants, argv form so no shell);
  the new sentinel (fail-closed, static text plus a timer id already in the WARN logs); and the
  fabricated `now` (the sole non-test caller **discards** the return, so it reaches no expiry,
  deadline, lease or authorization window).

**Verification, all executed on the MERGED tree with Docker up:** `go build ./...` **EXIT=0** ·
`go vet ./...` **EXIT=0** · `golangci-lint run ./...` **repo-wide EXIT=0, 0 issues** ·
`go test -count=1 ./...` **EXIT=0** · `go test -race -coverprofile ./...` **EXIT=0, 0 FAIL** ·
3× full-suite runs **3/3 EXIT=0** (the same command was 2/3 FAIL before the race fix).
Coverage: `pgelector` 94.2 % · `scheduler` 93.3 % · `myelector` 92.5 % · `processtest` 91.8 % ·
`obs` 88.0 % · `gocron` 85.9 %. Repo total 74.9 %, **pre-existing** — the drag is `definition`
33.3 %, `internal/dbtest` 39.8 %, `service` 53.9 % (backlog 20), `persistence` 84.1 % (backlog 34,
identical to ADR-0183's measurement). None touched by this delivery.

### What ADR-0184 is, in one paragraph

`processtest.RunTaskStoreConformance` is exported precisely because adopting ADR-0183 is a *silent*
break for a consumer's own `TaskStore` — and it had the same defect itself. Its docs promised a
rejected write is *"neither readable through `Get` nor listed by `AssignedTo` or `ClaimableBy`"*,
and a thirty-line comment argued both inbox queries were essential; but the **legal** leg never
asked an inbox anything, so nothing established the queries worked. **Measured: a store answering
both inbox queries with `nil, nil` passed the entire suite**, every `NotContains` holding vacuously.
Each case now declares which inbox must return it. Separately, 40 `Eventually` sites moved to a
per-package `eventuallyBudget = 10 s`, and the two never-executed misuse guards now run through the
subprocess harness the package already had. Closes backlog **42** and **43**; opens **44–50**.

### ⚠ Things a fresh session must not get wrong

- **`RunTaskStoreConformance`'s coverage is 77.8 % and that is CORRECT.** The two misuse guards run
  in a **child** `go test` spawned without `-coverprofile`, so their counters are discarded. **Do
  not "fix" it** — the only way to raise it is the `conformanceRunner` interface seam ADR-0184
  explicitly rejected.
- **The `eventuallyBudget` is 10 s, sized against the BINARY not the site.** `go test` defaults to a
  600 s per-binary timeout; `scheduler/internal/gocron` holds 31 of the 40 sites, predominantly
  serial. At 30 s a systemic break costs 930 s and the binary dies with a goroutine dump printing
  **no assertion messages**. Rule: `budget × densest package's site count < 600 s`.
- **All 16 `Never` budgets are deliberately untouched** — a `Never` window is paid in full on every
  GREEN run, so raising it is pure cost. The 2 `Never`-only `clock_option_test.go` files were not
  modified at all.
- **`writeOnlyTaskStore`'s pinned failure count is 1 and must stay 1** — an early return at the
  read-back guard makes the new inbox check unreachable for it. That inconsistency is backlog 47.
- **`scheduler.ErrSchedulerClosed` is a var ALIAS** of `scheduler/internal/gocron`'s sentinel, not a
  copy. Do not "clean up" the duplicate by re-declaring it — two values with identical text is
  exactly the `/code-review` finding this closed.

### ⚠⚠⚠ The process lesson this delivery earned

**Backlog 42 was MISDIAGNOSED, and the misdiagnosis survived a three-lens adversarial audit.**
It was filed as "load-flaky under `-race` contention", inherited from ADR-0183's handover and
restated as fact in ADR-0184's spec, ADR and plan. The audit's execution lens ran the test
`-count=25` **in isolation** — where it passes — and read that as consistent.

The refutation was a single number: **the test fails in 0.00 s**. Nothing waiting out a one-second
timeout can fail instantly. The real defect was a race in `ScheduleJob`, which returned a zero
next-run with a **nil error** for a past-due one-shot (~12 % of arms without `-race`, ~0.9 % under
it — the two modes differ ~13×, which is itself a trap: the first measurement was taken in the
wrong mode and propagated into five documents before being caught).

⭐ **When a symptom is attributed to a timing bound, check how long the failure actually took.**
⭐ **Executing a test is not the same as executing the FAILURE.** The lens ran the test; it never
ran it under the conditions that break it, and never read the failure text.
⭐ **A grep with a fixed context window cannot parse a Go call whose length it does not know** —
three such verification commands in this delivery's plan were wrong, two of them shipped into the
plan before execution caught them.
⭐ **`/code-review` found what six prior review passes did not, for the 3rd delivery running** — and
again it was in the change that entered AFTER the design gate, living in the **seam between two
packages**: a second `ErrSchedulerClosed` with byte-identical text meant
`errors.Is(err, scheduler.ErrSchedulerClosed)` was **false** for an error whose message said exactly
that. A reviewer scoped to either package alone sees a correct sentinel.

### ▶ NEXT WORK

The queue is empty, so the next delivery starts at **brainstorming** (rule #7) → spec/ADR/plan →
**ONE** rule-#9 audit of the whole bundle → handover (rule #10) → subagent-driven implementation
(rule #11). ⚠ **Blockers 1–3 and backlog 42/43 are CLOSED — do not re-raise them.**

**Latest ADR = 0184 (shipped, merge `be6e6b55`). Next free = 0185.** ADR numbers 0155–0157 remain
reserved by the parked `feat/durable-waiters-delivery-correctness`.

Strongest candidates, roughly in order:

- **Backlog 32 — downgrade drops new state fields** (whole-state `json.Marshal`, no
  `DisallowUnknownFields`). The highest-stakes item, and **verified by execution** during this
  session's triage: an unknown field unmarshals with `err=<nil>` and is **gone** after the
  round-trip. A dropped `RetryAttempts` **resets the retry budget so a poison compensation retries
  forever**; a dropped `IncidentKind` degrades a walk-scoped incident into a *resolvable, deletable*
  one; and post-ADR-0183 a decode dropping only `State` yields an `Unclaimed` task **carrying a
  claim**, which `Validate` now rejects on write — turning a readable row unwritable. ⚠ The snapshot
  has **no json tags at all**, so the wire format is Go field names; that constrains any fix.
- **The fail-open `AuthzSpec`** (under blocker 1) — **verified by source**: `authz/authz.go`
  documents *"An empty spec means allow-all"*, and `RoleAuthorizer.Authorize` returns `nil` when
  `Roles` is empty and `Attribute` is `""`. An empty spec, `eligible_roles: []`, a bare key and
  `null` all parse cleanly and mean allow-all. ADR-0167's strict decoding does **not** close it — an
  empty list is *valid*, just empty. Wants its own ADR.
- **Blocker 7 — suite speed.** `internal/dbtest`'s `sync.Once` boot fires per package → 12 Postgres
  + 7 MySQL boots. Fix: honour `WRKFLW_TEST_POSTGRES_DSN` / `WRKFLW_TEST_MYSQL_DSN` with
  testcontainers as fallback, plus `scripts/testdb.sh up|down` and CI wiring. Interacts with backlog
  48 (CI passes no `-timeout`, so nothing enforces ADR-0184's `budget × site count < 600 s` rule).
- **Blocker 8** — the `forceTerminate` → `endInstance` boundary sweep is entirely uncovered.

⚠ **Cheap follow-ups opened by ADR-0184**, for a small delivery: **47** (the conformance helper's
legal leg stops at the first break while its doc says it never stops early), **49** (a past-due
one-shot expressed as a *duration* is refused by gocron, leaking a raw `gocron: OneTimeJob: …`
string through the public API instead of a `workflow-scheduler:` sentinel), and **50** (the residual
`Close`/`ScheduleJob` window). **44** (the 16 `Never` sites are vacuity-prone under contention) is
the mirror image of backlog 42 and now has a proven precedent for how that class hides.

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
   ⚠⚠ **TREAT THAT "load-flaky" LABEL AS UNVERIFIED.** Backlog 42 carried the *identical* diagnosis,
   inherited across two handovers and restated as fact through a three-lens audit — and it was
   **wrong**: the test was not waiting on a timing bound at all, it failed in 0.00 s on a different
   assertion, and the real cause was a race in production code. Before designing anything for this
   item, **reproduce it under contention and read the failure text and its duration.** A test that
   fails instantly is not waiting out a timeout. See ADR-0184's Context and Consequences.
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
| `main` | **ADR-0184 merge `be6e6b55`** is the newest shipped code, pushed. ⚠ Never quote main's HEAD; re-derive |
| ADR-0184 | ✅ shipped — merge `be6e6b55`; spec/plan/ADR/3 audit lenses/adjudication all on `main` under `docs/`; branch deleted |
| ADR-0183 | ✅ shipped — merge `a7575ed5`; spec/plan/ADR/audits/adjudication all on `main` under `docs/`; branch deleted |
| *(merged branches)* | Deleted once pushed. **`origin` carries only `main`** plus dependabot. ⚠ **Every unmerged branch below exists on this machine ONLY** |
| `backup/terminal-trigger-guard-presquash` | `a3aa889` — ADR-0165 pre-squash, provenance only |
| **`parked/scope-and-fanout-design`** | ⚠ **SUPERSEDED — do NOT use as an input** |
| `feat/signal-arm-fanout` | `67cb055` — superseded packaging, kept for its audit tags |
| `feat/durable-waiters-delivery-correctness` | `434535d` — parked, docs only; holds ADR **0155–0157** |
| `docs/architecture-audit` | `393e516` — `AUDIT.md`, ⚠ deliberately NOT on `main`, NOT pushed |
| worktrees | ✅ **CLEAN** — verified after ADR-0184; its three detached audit worktrees were removed. ⚠ Create audit worktrees **detached at the bundle commit** so the design docs are present by construction |
| Latest ADR | **0184** shipped, merge `be6e6b55`. Next free is **0185** |
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

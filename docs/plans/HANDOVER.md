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

## State — updated 2026-08-20

**▶ NO CODE DELIVERY IS IN FLIGHT.** The last shipped code is ADR-0184.

⚠⚠ **TWO THINGS EXIST ONLY ON THIS MACHINE — read this before anything else:**

1. **`main` is 1 commit AHEAD of `origin/main` and UNPUSHED** — `129c151a`, docs-only (the
   architecture-audit close-out below). Nothing is half-done; it simply has not been pushed. Push it
   or say why not.
2. **`docs/architecture-audit` (`9769a8e5`) is the ONLY copy of `AUDIT.md` and both verifications —
   4,726 lines across 11 files — and it CANNOT be pushed** (this repo is public and the probes are
   working exploit chains). A lost machine loses all of it. The *defect statements* are safe: they
   are on `main` as backlog 51–126.

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

### ▶ ARCHITECTURE AUDIT — ✅ COMPLETE, verified by execution 2026-08-19 and 2026-08-20

`AUDIT.md` (2026-08-10, 69 findings) said outright that **it ran no tests** and that its attack
chains were "composed from source, not executed". **All of it has now been executed.**

- **2026-08-19** — the 22 lettered findings A–V: **3 closed** (D by ADR-0171, M by ADR-0176, T by
  ADR-0179) · **1 partial** (F) · **18 open** → backlog **51–69**.
- **2026-08-20** — the remaining three tiers, ten agents in ten detached worktrees:
  **Tier 1** (the 47/48 never-examined Medium/Low findings): 38 open · 2 closed (ADR-0172,
  ADR-0177, both ablated) · 6 partial · **2 materially false**. **Tier 2** (the six behavioural
  claims): 5 confirmed · **1 false premise (N)**. **Tier 3** (citations/evidence): **zero D-class
  false positives**, but **8 of 23 citations rotted**. New defects → backlog **70–126**.

Evidence — probe code, observed output, attack chains — is on the unpushed `docs/architecture-audit`
branch (`9769a8e5`): `AUDIT-VERIFICATION-2026-08-20-TIERS-1-2-3.md` plus ten `audit-t*.md`
companions. It stays unpushed because this repo is public. `git show
docs/architecture-audit:AUDIT.md > /tmp/AUDIT.md` to read the original; do **not** merge or push.

**⚠ `AUDIT.md` is now known to be unreliable in specific, listed ways.** Do not cite it without
checking this list first: **three false premises** (J's repro sketch is rejected by validation; U's
key claim was wrong at its own base; **R's "exactly one non-reference"** — six `examples/` mains
call `ClaimableBy`), **two materially false findings** (§4.4 items 11 and 12's public-package
sub-claim), **eight wrong counts** (its own "~30 further findings" is **47**; `persistence`
constructors ~35 → **54**; pre-ADR-0144 keys 5 → **38**), and **three proposed fixes that had
already shipped** before it was written.

⭐ **Lessons worth keeping:**
- ⚠ **CORRECTION — the "D was reported against already-fixed code" story is FALSE**, in both halves.
  Executed: `2426eaa` (the audit's base) is **NOT an ancestor of `main`**, contains **zero**
  ADR-0171 files, and `adf39777` (the fix) is an **`--amend` of that very commit** six hours later.
  **Finding D was a genuinely live Critical when reported.** The past-tense comment at
  `engine/step_nodes.go:241` is *the fix transcribing D* — a corpse the audit itself created.
  Fold-don't-stack orphaned the base out of history, so resolving "the base" to the surviving commit
  shows the fix already in it. **A previous session recorded the opposite; do not re-propagate it.**
- **The comment trap is real, just not where it was hunted.** Tier 3 found no comment-as-evidence
  among the lettered findings; the ops seam found one in the unlettered set (§4.6 item 6 cites
  `pgelector/elector.go:196` as "can silently lose the lock"; that line today describes the
  heartbeat that *catches* one, ADR-0061).
- **Titles are not closures.** ADR-0155 is literally named "durable waiter projection" and does not
  close finding C — it is on `main` as a **document only**. Every claimed closure was source-verified
  and, where clean, mutation-ablated.
- ⚠ **Cited tests are not covering tests.** D's four cited tests all stay **GREEN** when the ADR-0171
  line the ADR is *named for* is deleted. The pin is covered — by four *different* tests.
- ⚠ **Prefer symbol names to line numbers in any future finding.** The rotted citations that mislead
  are not the dead ones; `engine/step_triggers.go:272-283` now lands on live, plausible, unrelated
  ADR-0175 code.

**Method that worked, and why:** one detached worktree per agent (`git worktree add --detach <p>
<sha>`) so probes cannot collide; write findings to a file **per finding, before the next probe**;
**capture the FAILURE TEXT and its duration**, never just pass/fail — a 0.00 s failure is not a
timeout, which is how backlog 42's misdiagnosis died, and L's verdict here turned on `Run` returning
in **126 µs** rather than after a tick; and **mutation-ablate any CLEAN result** to prove the probe
could have failed. ⚠ **A script is not evidence until sanity-checked against a hand count** — this
run's controller mis-derived the finding count with a whitespace-intolerant regex.

### ▶ NEXT WORK

The queue is empty, so the next delivery starts at **brainstorming** (rule #7) → spec/ADR/plan →
**ONE** rule-#9 audit of the whole bundle → handover (rule #10) → subagent-driven implementation
(rule #11). ⚠ **Blockers 1–3 and backlog 42/43 are CLOSED — do not re-raise them.**

**Latest ADR = 0184 (shipped, merge `be6e6b55`). Next free = 0185.** ⚠ ADR **0155–0157 are now ON `main`**
(imported 2026-08-19, banner-marked **NOT IMPLEMENTED / AUDIT FAILED**) — the 0154→0158 numbering gap is
closed, and the numbers are visibly taken. Do not reuse or build on them without revising them first.

Strongest candidates, roughly in order:

- **Backlog 32 — downgrade drops new state fields** (whole-state `json.Marshal`, no
  `DisallowUnknownFields`). The highest-stakes item, and **verified by execution** during this
  session's triage: an unknown field unmarshals with `err=<nil>` and is **gone** after the
  round-trip. A dropped `RetryAttempts` **resets the retry budget so a poison compensation retries
  forever**; a dropped `IncidentKind` degrades a walk-scoped incident into a *resolvable, deletable*
  one; and post-ADR-0183 a decode dropping only `State` yields an `Unclaimed` task **carrying a
  claim**, which `Validate` now rejects on write — turning a readable row unwritable. ⚠ The snapshot
  has **no json tags at all**, so the wire format is Go field names; that constrains any fix.
- **The fail-open `AuthzSpec`** (blocker 1's tail; = backlog **53**, one item not two) — **verified by source**: `authz/authz.go`
  documents *"An empty spec means allow-all"*, and `RoleAuthorizer.Authorize` returns `nil` when
  `Roles` is empty and `Attribute` is `""`. An empty spec, `eligible_roles: []`, a bare key and
  `null` all parse cleanly and mean allow-all. ADR-0167's strict decoding does **not** close it — an
  empty list is *valid*, just empty. Wants its own ADR.
- **Blocker 7 — suite speed.** `internal/dbtest`'s `sync.Once` boot fires per package → 12 Postgres
  + 7 MySQL boots. Fix: honour `WRKFLW_TEST_POSTGRES_DSN` / `WRKFLW_TEST_MYSQL_DSN` with
  testcontainers as fallback, plus `scripts/testdb.sh up|down` and CI wiring. Interacts with backlog
  48 (CI passes no `-timeout`, so nothing enforces ADR-0184's `budget × site count < 600 s` rule).
- **Blocker 8** — the `forceTerminate` → `endInstance` boundary sweep is entirely uncovered.

⚠⚠ **The list above predates BOTH audit verifications. Weigh it against backlog 51–126.** The
2026-08-20 run added the following, which are arguably sharper than anything in the list above and
are all **executed**, not inferred:

- **90** — **silent claim theft**: any eligible actor takes a task another actor holds, `err=<nil>`,
  bypassing the guard `Reassign` has twelve lines below it. Small, self-contained, and a genuine
  authorization hole independent of the transport chain (51/52/53).
- **114** — a **load-bearing deep-copy in `cloneState` with zero coverage**, whose obvious regression
  test **cannot fail** (`len == cap` does not reproduce). This one is a *trap for the next delivery*,
  not just a defect — pair it with **73** (1,880× step cost) so nobody deletes the line blind.
- **99 / 78 / 81** — an unmetered expression stall (37.66 s, HTTP 201, request deadline ignored), a
  cache that **fabricates an instance that never existed**, and a relay that turns SQLite engine
  commits into hard `SQLITE_BUSY` failures. All three were Medium in the audit and measured worse.
- **115** — **duplicate node IDs build clean**. Cheapest real fix on the whole list.
- **106–108, 116–123** — the documentation/example rot cluster. Individually small; together they
  are the entire first-hour experience of a new consumer, and **108 cannot be fixed by editing the
  doc** because **116** makes the recipe uncompilable from outside the module.

⚠ **Before starting any of these, read the finding's entry for a `⚠` correction.** Eight of the
audit's counts and several of its mechanisms are wrong in ways that would misdirect a fix — most
sharply **99**, whose proposed `MaxNodes` fix provably would not have stopped the stall it is named
for.

The older list, still valid:

- **55** — `drive` has no iteration budget: an **engine-core hang**, executed at 1.44 M hops in 2 s,
  reachable from a definition `model.Validate` accepts. Nothing gates it at authoring time.
- **56** — incident lifecycle is token-keyed, not command-keyed: an **executed double-invoke** of a
  service action whose first invocation is still in flight.
- **51 + 52 + 53** — the transport takes its authorization principal from the request body, the
  facade defaults to allow-all, and an empty spec means allow-all. These compose; fixing one alone
  leaves the path open, and **51 is BREAKING** (it breaks tests that currently pin the behaviour).
- **67** — waiters are process-local with no durable row: an **executed** silent message loss that
  is a **multi-replica** defect, not merely a restart one.
- **58** — `examples/production_wiring` silently disables every timer, now including ADR-0179's
  compensation retry. Cheapest fix on the list; worst first impression for a consumer copying it.

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
   🚨 **Before DEPLOYING ADR-0167**: audit stored definition rows for pre-ADR-0144 camelCase keys.
   ⚠ **NOT 5 keys — 38.** Measured 2026-08-20: all **34** camelCase JSON tags present in
   `node_wire.go` at `8179c0b^` were probed and **34/34 are rejected**. The five previously listed
   were a *sample* inherited from `CHANGELOG.md:180` and restated as an enumeration; that list also
   includes `compensationAction` (retired at ADR-0114) and omits `eligibleRoles`. Consumer-facing
   text: `workflow-store: lookup "ord": unmarshal: json: unknown field "compensateAction"`, raised
   from `Lookup(latest)` — the **instance-start hot path**. No checklist entry, no migration, no
   checker API (all three searches → 0).
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
`ReasonCompensation` of its own. 20. repo-wide coverage (⚠ `service` **53.9 %**, measured 2026-08-19; was ~52.6 % when filed). 21. ✅ **`AUDIT.md` — FULLY VERIFIED (2026-08-19 lettered, 2026-08-20 tiers 1–3), superseded by
backlog 51–126.** Still on `docs/architecture-audit` (`9769a8e5`), ⚠ NOT on `main` and NOT pushed
(public repo; the branch carries working exploit chains). Nothing in it is unverified any more.
Do not re-raise this item — track the open defects as 51–126, and ⚠ **do not cite `AUDIT.md`
without reading the reliability list in State**: three false premises, two false findings, eight
wrong counts, three fixes that had already shipped.

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

**🆕 from the 2026-08-19 verification of `AUDIT.md`** (all **executed**, not read — see the
`▶ ARCHITECTURE AUDIT` note in State). Numbered 51+ so they never collide with the ADR-derived
items above. Each is a **defect statement**; the reproduction detail deliberately lives on the
unpushed `docs/architecture-audit` branch, not here.

51. **The bundled HTTP task handlers derive the authorization principal from the request body.**
    `transport/http/httpcore/endpoints.go` builds `authz.Actor` from `in.Actor.ID`/`in.Actor.Roles`
    at all three task sites, and `httpcore.CustomizeConfig` exposes no actor seam, so nothing under
    `transport/` can read an identity established by consumer middleware. ⚠ Any fix is BREAKING and
    breaks `httpcore/endpoints_test.go:405,422`, which currently pin the present behaviour as the
    contract. Wants its own ADR. **(audit B; interacts with 52 and 53)**
52. **`service.NewProcessEngine` defaults to `authz.AllowAll{}`, and `DurableProvider` exposes no
    `Authorizer()`** — so the natural durable wiring has nowhere to supply one and silently lands on
    allow-all. Logged at DEBUG only. **(audit B tail — not in the audit's own B text)**
53. **⚠ SAME ITEM as blocker 1's tail and the NEXT WORK candidate above — not a second finding.**
    **`RoleAuthorizer` treats an empty/zero `AuthzSpec` as allow-all** and the `AuthzSpec` godoc
    documents it as intended. Executed: a zero spec, an empty role slice and a nil role slice each
    authorize an actor with no ID and no roles. ⚠ This is blocker 1's tail, now measured.
    **(audit G)**
54. **Instance/task routes carry no auth caveat, IDs default to `xid`, and instance variables are
    returned unredacted.** The `SECURITY:` comment exists on `AdminRoutes` only; `idgen.UUIDv7()`
    exists but is opt-in; `httpcore/view.go` returns `st.Variables` verbatim with no redaction hook;
    no ownership/tenant predicate exists on any instance read. **(audit H)**
55. **`drive` has no iteration budget.** `engine/step.go`'s loop has no hop cap, no cycle guard, and
    `openVisit` appends a `NodeVisit` per hop uncapped; `model.Validate` has no cycle detection.
    Executed: a validation-clean join/split cycle drove **1.44 M hops in 2 s** with `Step` never
    returning. ⚠ **ADR-0168–0171 does NOT close this** — that bundle stops execution once an
    instance is no longer normally executable and adds no budget. ⚠ The audit's own repro sketch
    (`gwA -[true]→ gwB → gwA`, both 2-in/2-out) is REJECTED by validation; use a join/split pair.
    **(audit J)**
56. **Incident lifecycle is token-keyed, not command-keyed.** `handleUnhandledError` leaves
    `AwaitCommand` set on an incident token; `tokenAwaiting` matches on `AwaitCommand` with no state
    check; `handleResolveIncident` re-invokes the node the token is at **now**, not `inc.NodeID`.
    Executed: a late `ActionCompleted` resumed a token in `TokenIncident`, the incident went stale
    naming the old node, and `ResolveIncident` then emitted a **second `InvokeAction` for the wrong
    node** while the first was still in flight. ⚠ The orphan-incident leg is conditional on the
    token surviving; the **double-invoke leg is unconditional**. **(audit K)**
57. **One undecodable outbox row halts the entire relay.** `scanClaimRows` fails the whole batch on a
    single bad payload/def-ref, and that error propagates through `DrainOnce` → `drainUntilEmpty` →
    `Run`, which returns rather than backing off; a restart re-claims the same head row. Per-row
    quarantine covers **publish** failures only, not decode. ⚠ `persistence.Relay`'s public godoc
    currently blesses this ("only infrastructure errors … terminate the loop"). **(audit L)**
58. **⚠ REWRITTEN 2026-08-20 — the original statement was FALSE.** `examples/production_wiring`'s
    timers **do fire** (measured: 503 ms): `NewProcessDriver` **creates and owns a default gocron
    scheduler** when `WithScheduler` is absent, and it auto-starts on the first arm. Ablation:
    forcing the other branch panics, so even the counterfactual is not *silent*. The "worse since
    ADR-0179" line was also false — `compensationRetryPolicy` is nil unless
    `WithCompensationRetryPolicy` is called, and the example never opts in. **The real defect is
    durability, not firing:** with no `WithTimerStore`, `RehydrateTimers` refuses and a 2 s timer
    never fired 8 s after restart while the instance stayed `running` in the store — so on the
    `DATABASE_URL` branch, instance state is durable while **every armed timer dies with the pod**.
    True sub-claims: no `WithScheduler`/`WithTimerStore`; `notifier` appears 3× and **all three are
    comments**; no metrics; `AdminRoutes` exists and is not mounted. See also item 121. **(audit N)**
59. **No stuck-instance observability.** ⚠ **Sharpened 2026-08-20 — it does not "reset", it drifts
    NEGATIVE and stays there.** Measured: `active=3` → (restart) series **absent entirely** → after
    cancelling the 3 survivors, **`active=-3`**, because the `-1` on a terminal transition fires for
    instances whose `+1` was booked by a dead process. A reset self-heals; negative drift does not.
    Exactly **2** DB-truth collectors (outbox, timer); `driverObs` holds 13 instruments and this is
    the only UpDownCounter; zero hits for `oldest_active`, `incidents_open`, `compensation_walks`.
    ⚠ ADR-0179's `IncidentCompensationFailed` is the durable evidence a compensation did not run and
    **nothing counts it** — and the generic `incidentsRaised` counter carries **no incident-kind
    attribute**, so it cannot be split on a dashboard. ⚠ **Fixing this is part of audit finding A's
    fix, not a separate task**: a permanently-stuck instance is *indistinguishable* from a healthy
    one in the only admin list projection that exists (see 66). **(audit O)**
60. **Traces die at every async boundary.** No trace-context propagation exists in `runtime/`,
    `engine/`, `eventing/`, `scheduler/` or the store (verified: zero `traceparent`/`TraceContext`
    hits), and no `traceparent` column exists on the outbox or timer tables. A timer fire, a relay
    publish and a task completion each start a fresh root trace. **(audit P)**
61. **`engine.InstanceState` exports public fields whose types are unexported** (`[]timerRecord`,
    `[]armedEvent`, `[]boundaryArm`, `compensationCursor`, …) while also being the custom
    `InstanceStore` contract, so a third-party store can only round-trip opaque JSON — which per
    backlog 32 is unversioned. ⚠ **Grew since the audit**: ADR-0171 added a field *inside*
    `compensationCursor`, ADR-0179 added `Incident.Kind`/`RetryAttempts`, ADR-0177 added
    `Token.AwaitTimer` — all wire-shape changes invisible in the public signature.
    ⚠ Window-limited: free to fix before v0.1.0. **(audit Q)**
62. **The read half of three APIs is missing**: no task inbox (`AssignedTo`/`ClaimableBy` exist on
    the store only, unpaginated, with no service or HTTP exposure and no `ListTasks` anywhere); no
    definition lifecycle (`PutDefinition`+`Lookup` only — no list, no retire, no instance
    migration); and `InstanceFilter` is `Status`+`Limit`+`Cursor`+`IncludeTotal` with no `DefID`,
    time range or variable filter. ⚠ ADR-0184 hardened the *store conformance contract*, NOT the API
    surface — do not read it as closing this. **(audit R)**
63. **A timer armed on a non-leader replica is never fired under stable leadership.** The job is
    registered in that replica's gocron only, its fire is gated off by `IsLeader`, and the leader
    has no such job; the only path that re-reads durable rows is boot-time rehydration, so nothing
    reconciles while leadership is stable. ⚠ `ReclaimNeverDueTimers` (ADR-0181) *deletes* orphan
    rows — it arms nothing. **(audit S)**
64. **The `Action` contract is unwritten** — no timeout, panic-recovery or at-least-once semantics
    are stated, and no `CommandID` or attempt number reaches `Do`. ⚠ **Correction to the audit,
    which was wrong at its own base**: a stable `_idempotencyKey` (`"<instanceID>:<nodeID>"`) IS
    passed to primary service-task actions. The real gap is narrower and hotter: compensation,
    deadline and reminder actions carry **no key at all**, which is exactly the class ADR-0179's new
    retry re-invokes. **(audit U)**
65. **`httpcall`'s URL/body may derive from process variables with no SSRF guard.** `WithURLExpr`
    compiles a raw `expr.Compile` (no `expreval`, so no timeout) and its own godoc names the hazard;
    the default client has no `CheckRedirect` policy and no address allowlist. Response bodies land
    in `httpBody`, readable via the unredacted instance read of item 54. **(audit V)**
66. **Post-commit projections have no crash-recovery path.** Timer activation, `syncWaiters`, human-
    task `Upsert` and action dispatch all run *after* the commit tx, non-transactionally; the only
    reconcilers in the repo are `RehydrateTimers`/`RehydrateStartTimers`. There is no waiter, task or
    command reconciler and no `ReconcileInstance`/`RetryCommand` verb. ⚠ Backlog 37 is one instance
    of this class; the **class itself** is this item. **(audit A — the audit's dominant theme)**
67. **Message and signal waiters are process-local, with no durable row and no boot reconciler.**
    They live in two in-memory maps written only as a side effect of stepping an instance. Executed
    on a durable store across a driver restart: a correlated message is **dropped** (`err=<nil>`,
    instance still parked, payload gone) or **misrouted** (a message-start definition consumed it
    and completed a *different* instance); signals are dropped; and the broker handler returns `nil`
    either way, so the delivery is **acked** and never redelivered. ⚠ The snapshot **does** carry the
    waiter — `MessageWaiters()` reports it after reload — nothing rebuilds the index from it.
    ⚠⚠ **This is a multi-replica defect, not merely a restart one**: the probe's second driver is
    exactly a second replica, and the repo ships an advisory `Locker` and an elector. ⚠ ADR-0155
    is on `main` as a **document only** (NOT IMPLEMENTED) — its title is not a closure.
    **(audit C)**
68. **The single Go module forces gin, fiber, watermill, redis, memcache and testcontainers on every
    consumer.** One `go.mod`; `persistence/cache/cachetest/containers.go` is a **non-test file in a
    public package** importing testcontainers. ⚠ Window-limited — cheap before v0.1.0, expensive
    after. **(audit E)**
69. **The general operator escape-hatch contract is missing.** ADR-0175 closed exactly **1 of 6**
    stuck states (the stalled compensation walk) with a fifth bespoke verb. The other five — lost
    in-flight action, lost waiter (67), lost human-task projection, dropped timer fire after CAS
    exhaustion, non-leader-armed timer (63) — have no escape but destructive cancel/reverse.
    **(audit F — partially closed)**

**🆕 from the 2026-08-20 verification of `AUDIT.md` tiers 1–3** (all **executed**; evidence on the
unpushed `docs/architecture-audit` branch, `9769a8e5`). Items 70–103 are the audit's own
never-examined §4 findings; **104–117 are defects `AUDIT.md` does not contain at all**.

⚠ **Corrections to items above, from this run:** **58** carried two false claims (see State);
**59** understated (the metric drifts **negative**, and `incidentsRaised` has no kind attribute);
**62** is corroborated — R's "exactly one non-mock reference" is false, six `examples/` mains call
`ClaimableBy`. **19/41** (no `ReasonCompensation`) is confirmed still open and is **not** renumbered.

*Engine core (audit §4.1):*

70. **`DeferredCompensationThrows` has no liveness maintenance.** A root interrupting ESP consumed
    both deferred throw tokens leaving **2 of 2 queue entries dead**; a mixed `[dead, live]` queue
    strands the live throw `TokenWaiting` with `AwaitCommand`/`AwaitSignal`/`AwaitMessage` **all
    empty** — unreachable by any trigger kind — while a sibling sits `TokenJoining` and the instance
    stays `running`. ⚠ **Bounded**: proven to strand *the next* throw, **not** "every throw behind
    it". Do not restate the audit's wording.
71. **Partial rollback into a sub-process-internal node resumes in the wrong scope.** `case
    walkPartial` builds `finishPlan` with **no `resumeScope`** → token placed at root → `WARN token
    routed to a missing node` → parked with no await key, `status=running`, zero commands. The
    `resumeDropped` safety net is gated on `resumeScope != ""` so it structurally cannot fire.
    Reachable from the public admin API **with no error returned**.
72. **Reverse-compensation order ties on `CompletedAt` and then flips on node NAME.** Identical
    topology/timestamps: `z-inner`/`a-root` → `[undo-inner undo-root]`; `a-inner`/`z-root` → the
    correct order. ⚠ Wider than the audit's "coarse/fake clock" framing: **one trigger writes two
    records with identical `CompletedAt`** (`step_triggers.go` `t.OccurredAt()` vs `step_nodes.go`
    `c.at`), so any sub-process declaring its own `CompensateAction` ties **by construction, on every
    clock**. Only runs in `consolidateArchiveIntoRoot`; flat root-only processes are unaffected.
73. **Every `Step` is O(entire state).** Benchmarked on an *inert* signal: history 0 → 556 ns/1.7 KB;
    **100k → 1,045,401 ns and 9.6 MB per `Step` (1,880×)**; 10k completed tasks → 901 µs, 20,011
    allocs. Ablating `s.History = append([]NodeVisit(nil), …)` makes it flat (~510 ns at every size).
    ⚠ **Read item 114 before touching that line — it is the trap that makes this fix ship state
    corruption under a green suite.**
74. **A consumer `IDGenerator` emitting an `evtgw:`-prefixed id cross-wires tokens.** With a task id
    equal to `evtgw:id3`, one timer fire **consumed the user-task token in the gateway's place**,
    left the real gateway token parked `arms=0` and `running` forever, orphaned the task — and
    completing that task then **drove the gateway token**. ⚠ Damage is at the **exact-equality**
    lookup in `resolveGatewayWin`, not the prefix match (`cancelTokenWaits` verified benign).
75. **The wall-clock purity guard is evadable by import alias.** `import chrono "time"` in a non-test
    engine file: all three purity tests **PASS (EXIT=0)**; unaliased is RED. ⚠ The audit's "passes
    both it and the vendor denylist" over-credits the denylist — `"time"` was never a denied path.

*Persistence (audit §4.2):*

76. **Every replica arms every timer; exclusion is opt-in and per-fire.** 3 replicas → 3 Loads →
    **9 fires** where one gives 3. No ownership filter on rehydrate, no per-occurrence fencing.
77. **A post-commit `Activate` failure loses the timer until reboot.** Live node, durable-but-unarmed
    200 ms timer: **0 fires in 1.6 s**, and a *second* `Start()` does not re-scan. ⚠ There are **two**
    `Activate` sites; the second is the boot-recovery path and swallows failures identically.
78. **A crash mid-`RunInTx` makes a shared cache fabricate an instance that never existed.** Real
    Redis + real `os.Exit(9)`: node B loaded `version=1, vars=map[phantom:true]` for an instance with
    **0 DB rows** — at the exact seam that would otherwise answer "not found". 5-min TTL window.
    ⚠ **Medium → High**, and ⚠ **fixing 80 by scaling out properly ARMS this one.**
79. **`HumanTaskStore.Upsert`'s completion axis is still unvalidated.** ADR-0183 closed the *claim*
    axis (ablation-confirmed); `Unclaimed + Completion` persists. ⚠ The audit's "the write path
    validates **nothing**" is false today — re-headline on the completion axis or it reads as fixed.
80. **Caching + `AlwaysOwn` is the DEFAULT `DurableProvider`.** Exactly one construction WARN, nil
    error; `WarnUnsafeConfig(MultiReplica:true)` emits `""`.
81. **The relay holds row locks and an open tx across a network `Publish`.** Engine commit latency
    `641 µs → 1.55 s` (**2418×**); past `busy_timeout` the commit **fails** `SQLITE_BUSY`. Shipped
    default batch (100) crosses that at any per-publish latency above ~50 ms. ⚠ **Medium → High on
    SQLite**; interacts with **117** (the documented `busy_timeout` is inert, which is what turns
    the block into a hard failure).
82. **No retention path for the three fastest-growing tables.** All 5 pruners at a year-2999 cutoff
    deleted **0** from `wrkflw_instances`/`wrkflw_journal`/`wrkflw_human_task`; a manual delete is
    **FK-blocked**, so the proposed cascade is *required*, not convenience. ⚠ `docs/retention.md`
    already exists (the audit proposes creating it) and documents the incomplete set as a feature.
83. **Schema-skew protection is prose; there is no `CheckSchema`.** On a skewed DB `List()`/`Load()`
    **succeed** while `Create()` dies — a replica that passes health checks and fails writes only.
84. **The store layer reads the wall clock directly**, against ADR-0138. ⚠ Enumeration is too small
    on **both** sides: **5** persisted wall-clock sites (incl. `ChainLinkStore.Record`,
    `DefinitionStore`), **3** clockwork-compliant types (incl. `CallLinkStore`, `PgxNotifier`).

*Runtime (audit §4.3):*

85. **`BroadcastSignal` is non-idempotent under retry.** A bus-publish failure after signal-start
    creation succeeded → caller retries → **2 defs became 4 instances**. ⚠ **ADR-0158 does NOT close
    it** — that changes waiter-*arm* dispatch in `engine/`, not signal-*start* creation in `runtime/`.
    Fan-out is also synchronous on the caller goroutine. ⚠ This is a **second** undeduplicated
    creator, so item 88's "the only one" is false.
86. **Nothing bounds concurrent step execution, and contention silently drops timer fires.**
    `timerFireFunc` burned **5 CAS attempts in 53.9 µs** then permanently dropped the fire. `admit()`
    accepted **500 concurrent units and refused none** (`inflight` is a `sync.WaitGroup` — no
    capacity by construction). Zero `LimitConcurrentJobs` hits repo-wide.
87. **Cancel propagation orphans the whole descendant subtree.** `CancelInstance err=<nil>`, parent
    terminated, **child AND grandchild still `running`**, `commits attempted = 1` — the child-cancel
    path has **no CAS retry at all**, and the `continue` on error skips the recursion.
    `ListRunningChildren` has exactly **one** non-test caller ⇒ nothing revisits them.
88. **`StartInstance` has no idempotency key.** Two identical requests → two independent instances;
    `StartInstanceRequest` is exactly `[DefRef Vars]`; zero `idempotency` hits in `transport/`. Plan
    exists (`docs/plans/2026-07-13-start-instance-idempotency-key.md`, `Status: OPEN`); ADR-0018 is
    about *action* keys. ⚠ "the only undeduplicated creator" is **false** — see 85.
89. **A foreign scheduler that omits `Location()` silently persists wrong `NextRun`s** — measured
    **7 h** skew (09:00 UTC vs 02:00 UTC) with **no log line at any level**. ⚠ The audit's mechanism
    is wrong on 2 of 3 seams: `Location()` is **exported**, and the jobstore-save assertion returns a
    **typed error**, not a silent fallback.
90. ⭐ **Silent claim theft: any eligible actor can take a task another actor holds.**
    `runtime/task/service.go` `Claim` does `Get` → `Authorize(eligibility)` → return trigger with
    **no unclaimed precondition**, while `Reassign` twelve lines below guards `from != claimant`.
    Executed: Bob claims Alice's task, `err=<nil>`, delivered `err=<nil>`, `Claim.Actor` flips
    alice→bob — bypassing `Reassign`'s guard entirely. ⚠ **ADR-0183 does not close it** (task shape,
    not a claim precondition). Filed by the audit as a *Low* sub-clause; **should be raised**.
91. **Eventing consumers get no schema/version envelope.** Wire bytes are the user's variable map at
    top level (`{"approved":true,…}`); metadata is only `topic`/`instance_id`/`definition_ref`.
    ADR-0012 deferred it as YAGNI. ⚠ Sharper than stated: an engine-added key can **collide with a
    user variable**, not merely break parsing.

*Public API (audit §4.4):*

92. **Generated gomock doubles are compiled into the public `service` package.** "generated GoMock
    package" appears **exactly 4×** in `go doc service`; **49 exported types, 22 `Mock*`, 27 real**
    (**1.81×**, not "~doubling"); a *non-test* external file calling `service.NewMockPolicyAdmin`
    builds `EXIT=0`. Only 4 mock files repo-wide — the fix is contained. ⚠ Window-limited.
93. **`persistence` is an N×M constructor lattice — `54` constructors, not "~35".** ⚠ The audit blames
    the wrong prefix: `WithDurable*` is only 3 and is legitimate; the real workaround is **14
    `MySQLWith*` aliases of identical types**, and `OpenMySQL(…, WithOutboxNotify())` /
    `NewSQLiteRelay(…, WithListenNotify())` **compile and silently no-op**.
94. **YAML semantic errors carry no source positions.** ⚠ Sub-claim false: strict-field/syntax/type
    errors **already carry line numbers** free from yaml.v3, and unknown node kind surfaces from
    `ParseYAML`, not `Build()`. The gap is the genuinely semantic cases (dangling flow, two starts).
95. **`STABILITY.md` contains stale facts** — gocron `v2.21.2` vs go.mod `v2.22.0`, and a root
    `model/` package that does not exist. See also item **120** (`samber/do` listed as a locked
    dependency that is not in `go.mod` at all). Adjacent rot: README says "19 node kinds"; the repo
    has **18**.
96. **The parity suite is blind to routes nobody listed.** Adding `/auditprobe` to **stdlib only**
    left `go test ./transport/...` at **EXIT=0** with parity included; the positive control (diverging
    an existing route) **does** go RED, so the suite is alive but unenforcing. Real surface: **26
    routes × 3 = 78** hand-written registrations. ⚠ Two audit sub-claims are **false**:
    `transport/http/parity` is `package parity_test` only (**not importable**, so "make it internal"
    is moot), and it is **three** frameworks, not four (`httpcore` is the core).
97. **Authoring fans a definition author across packages** — 7 (minimal) and 14 (`production_wiring`)
    both re-derive **exactly**. ⚠ Lowest priority here: the audit's own proposed fix already shipped
    (`README.md:56` leads with the fluent `AddX` builder, 2 imports).

*Security (audit §4.5):*

98. **No HTTP body or process-variable size limit.** A 256 MiB body → **201 Created** on stdlib and
    gin, 770–834 MiB heap (~3.2× amplification); a 64 MiB variable persists verbatim and is echoed
    back. Fiber rejects at 4 MiB — but that is **fiber's own** `DefaultBodyLimit`, not wrkflw's.
    ⚠ "every decode is a bare `json.NewDecoder`" is false: **13 sites per adapter × 3 = 39** across
    three idioms, httpcore zero — so a `MaxBytesReader` fix covers only the stdlib third.
99. **A pathological expression pins a core and the request deadline is ignored.** A **108 KB**
    request pinned one core for **37.66 s** and returned **201**; the request's 1 s ctx deadline is
    ignored because `ConditionEvaluator` takes no `ctx`. ⚠ **The audit's finding is inverted**:
    `expr.MaxNodes(0)` is what *disables* the check, so not calling it leaves `DefaultMaxNodes=1e4`
    **active** (20k-node expr rejected, 4k accepted) — and **its proposed `MaxNodes` fix would not
    have stopped this stall; that condition is 11 AST nodes.** The unmetered surface is
    **caller-supplied arrays** (`vm.memory` counts only VM-allocated data): clean O(n²), 60 ms →
    242 ms → 1.10 s → 4.00 s. ⚠ De-risking claim **verified true**: 26 routes, no definition-deploy
    route.
100. **No data-at-rest posture or codec for PII in variables.** SSN/card/OAuth-token read back as
     plaintext from the raw `snapshot` column **and** from `wrkflw_journal` — two copies.
101. **No tamper-evident audit trail.** A forged actor lands in the durable record; `UPDATE`/`DELETE`
     on the journal succeed silently (6 columns, no hash/chain). ⚠ The audit is wrong on both nouns:
     `engine.NodeVisit` has **no actor field** (ADR-0145) and the outbox emitted **zero**
     actor-bearing events — the actor's homes are the **task record** and the **journal**.
102. **A casbin cross-node policy-reload failure is silently swallowed**, leaving stale policy: after
     breaking reload, a **revoked** permission still returns `Enforce = true, err=nil`. Startup load
     does fail closed. ⚠ The audit's most accurate citation (`db.go:97`, exact).
103. ⭐ **Negative/deny-list ABAC predicates fail OPEN.** `!= "blocked"`, `!= true`, `!(… == true)`,
     `X == nil or …`, `… or "manager" in actor.Roles` **all silently allow everybody**. ⚠ The audit's
     own exemplar (`actor.attributes.…`) **errors** and 403s everyone, so its "silently" is false as
     written — but the conclusion holds for this class, which it never names. **Low → Medium bypass.**
     Transport `Actor` still drops `Attributes`.
104. **4xx bodies echo internals — including the verbatim ABAC predicate source on 403**, twice,
     with internal variable names. Five classes echo (**400, 403, 404, 409, 422**), so a "generic
     400/422" fix leaves the worst case leaking. 5xx correctly blanks.
105. **The default email sender silently downgrades to plaintext.** ⚠ Not "unconditional plaintext" —
     it *is* opportunistic STARTTLS and upgrades when advertised. Strip `250 STARTTLS` and the OTP and
     balance go clear while `Do` returns `{"emailSent":true}, err=nil` — failure is indistinguishable
     from success. SECURITY.md assigns SMTP TLS to the embedder, which mitigates nothing here.

*Operational readiness (audit §4.6):*

106. **Readiness cannot see the scheduler, elector or notifier.** `/readyz` → **200**
     `{"relay-backlog":"ok","sqlite":"ok"}` with a **leaderless elector and zero scheduler
     in-process**; exactly 2 shipped `HealthCheck` impls. Ablation: a registered leadership check
     gives 503, so the mechanism exists. ⚠ The audit's parenthetical is a **comment trap** — today
     `pgelector/elector.go:196` describes the heartbeat that *catches* a lost lock (ADR-0061, real
     code). The true gap: **no `is_leader` metric**, so the audit's own alert has nothing to read.
107. **Timer lateness is not measured.** `TimerStatsCollector` reads `NextFireAt` from the DB every
     cycle **and discards it**; a 45-min-overdue scheduler emits exactly **1** instrument,
     `wrkflw_timers_armed=7`. ⚠ Lateness *is* computed once (`job_schedule.go:109`) but only for a
     one-shot already past-due at arm time, and only as a WARN — do not write "not measured anywhere".
108. **`docs/observability.md`'s wiring recipes do not compile.** Compiled verbatim from an external
     module: the wiring section gives **4 compile errors / 6 defects**; ⚠ **a second recipe the audit
     never mentions** fails `go mod tidy` because **`github.com/kartaladev/wrkflw/rest` does not
     exist anywhere**. Its admin table also omits `POST
     /admin/instances/{id}/compensation/resolve-stall` (ADR-0175's escape verb) and its metric
     inventory is missing 5 real metrics. ⚠ "Correct the paths" is **insufficient** — see **116**:
     the recipe is uncompilable from outside the module regardless of what the doc says.
109. **`OpenSQLite` never checks the single-writer contract** — accepts `MaxOpenConns=8` silently;
     `DeploymentProfile` has 5 fields, none dialect-aware. ⚠ Causally corrected: pool>1 **alone is
     not sufficient** — with `busy_timeout` there were 0 failures in 4 runs; **without** it,
     **174–195 of 200 failures in 4–17 ms**. See 112.
110. **`ErrDrainTimeout` abandons an in-flight step with no cancellation, no log and no guidance.**
     Fired at 50.3 ms with the action still running; **`ctx.Err()` inside the action is `<nil>` — it
     is never cancelled**; **zero** log lines (the file has no logger). Rollback safety proven by
     `os.Exit(7)` mid-tx → 0 rows. The checklist mentions "shutdown" **0** times.
111. **Repair verbs below instance granularity are missing** — scenario reproduced exactly
     (`ResolveIncident` re-drives with the same `bad amount -1`, vars unchanged). ⚠ **"there is no
     move-token" is FALSE**: `ReverseInstance(WithTargetNode)` exists and predates ADR-0175 — but only
     when the target *completed* **and** declared a compensate action, and it is on **neither
     `service.Service` nor any HTTP route**.
112. **DB pool saturation is invisible** — symbol, metric-name and call-site searches all **0** hits.
     Mitigated by the consumer owning the pool (`db.Stats()` is reachable), so Low is right.
113. **No N-1 / rolling-upgrade compatibility statement.** ⚠ And the answer is known to be **NO**:
     mixed-version is declared unsafe in ADR-0173:226, ADR-0175:279 and `engine/state.go:223` (data
     loss), while CHANGELOG has **0** mentions. The audit's suggested wording *"schema N works with
     library N-1"* would be a **false guarantee**.

**🆕🆕 Defects `AUDIT.md` does not contain at all** (found by the probes; this is the main return on
the 2026-08-20 run):

114. ⭐⭐ **`cloneState`'s `History` deep-copy is completely untested, and the obvious regression test
     would be vacuous.** The entire `engine` package stays green (**EXIT=0**) with it replaced by a
     shallow assignment — yet it is load-bearing: with a **`cap > len`** base, two `Step`s from one
     base corrupted each other (`workA/09:00` → `workA/13:00`). ⚠ **A `len == cap` fixture does not
     reproduce it.** Anyone acting on item 73 deletes this line, sees a green suite, writes a test
     that cannot fail, and ships state corruption.
115. **Duplicate node IDs build clean.** Two nodes sharing `id="charge"` → `nodes=4`, no error. No
     duplicate node/flow-ID validation exists anywhere.
116. **`runtime/monitor`'s collector options are unreachable from outside the module.**
     `NewOutboxStatsCollector(r, opts ...observability.Option)` types its variadic with
     `internal/observability`; passing `WithMeterProvider(mp)` externally → `use of internal package
     … not allowed`. The DB-truth gauges are pinned to the OTel global, so **fixing item 108's doc
     cannot make the recipe work**. Same ADR-0004 shape as audit finding N.
117. **ADR-0082's documented SQLite DSN is inert.** `_busy_timeout=5000` is mattn syntax; on the
     pinned `modernc.org/sqlite` it is silently ignored (`PRAGMA busy_timeout` reads **0**). That is
     exactly the configuration with 87–97% failure rates under contention (item 109), and
     `busy_timeout` is absent from the production checklist.
118. **The same `SQLITE_BUSY` reaches callers under two identities.** `store_core.go:98-101` — the
     instance-INSERT branch in `Store.Create` — wraps raw **without** `mapConflict`, while all four
     other `Create` paths and all eight in `Commit` call it. Measured ~93% unclassified driver text /
     ~7% `kernel.ErrConcurrentUpdate`. ⚠ **Dialect-neutral**: a Postgres serialization failure or
     MySQL deadlock on that INSERT escapes unmapped too, so a consumer retrying on the documented
     sentinel misses nearly all of them.
119. **`NewSQLiteDeduper` does not exist**, though the SQLite migration ships
     `wrkflw_processed_message` and the internal implementation is dialect-neutral.
120. **`samber/do` is listed in `STABILITY.md` as a locked dependency and is not in `go.mod` at all**
     (direct or indirect), imported by zero files — only two comments reference it. ⚠ **`CLAUDE.md`'s
     own locked tech-stack table names it as the DI container.** Decide: adopt it, or correct both.
121. **`examples/production_wiring` never calls `driver.Start` or `driver.Shutdown`** (`grep
     "driver\."` returns nothing), so the scheduler that actually fires its timers is never drained;
     the hand-built `sched` it *does* register for shutdown fires nothing. See the item-58 correction.
122. **`examples/broker_wiring` claims `Run` "loops DrainOnce with backoff".** There is no backoff in
     `Run`, `drainUntilEmpty` or `DrainOnce`.
123. **`scheduler/job.go`'s comment says foreign jobs are "treated as non-singleton".** Measured
     `jobSingleton(foreign recurring) == true`.
124. **The engine already holds the data that refutes a forged actor and never compares it** — a task
     record simultaneously stores `Candidates: [alice]` and
     `Completion.Actor: mallory-not-in-any-directory`. A cheap partial mitigation for item 101,
     independent of fixing audit finding B.
125. **`store.Create` SIGSEGVs on a nil `AppliedStep.Trigger`** (`trigger_codec.go:100`) instead of
     erroring. `internal/`-only, no live vector.
126. ⚠ **Test-authoring trap, not a defect:** `clockwork.NewFakeClock()` **seeds from wall time** in
     this version, which would silently make a naive regression test for item 84 unable to fail.

## Where things live

| | |
|---|---|
| `main` | **ADR-0184 merge `be6e6b55`** is the newest shipped code, pushed. ⚠ Never quote main's HEAD; re-derive |
| ADR-0184 | ✅ shipped — merge `be6e6b55`; spec/plan/ADR/3 audit lenses/adjudication all on `main` under `docs/`; branch deleted |
| ADR-0183 | ✅ shipped — merge `a7575ed5`; spec/plan/ADR/audits/adjudication all on `main` under `docs/`; branch deleted |
| *(merged branches)* | Deleted once pushed. **`origin` carries only `main`** plus dependabot. ⚠ **ONE unmerged branch remains, and it exists on this machine ONLY.** `backup/terminal-trigger-guard-presquash` and `parked/scope-and-fanout-design` were deleted 2026-08-19 after verifying they held ZERO content absent from `main` |
| `docs/architecture-audit` | `9769a8e5` — ⚠⚠ **the ONLY copy of `AUDIT.md` + both verifications** (2,507 + ~4,900 lines). Deliberately NOT on `main` and NOT pushed: the repo is PUBLIC and the probes are working exploit chains. **It cannot be backed up to `origin`**, so a lost machine loses it. ✅ **FULLY verified**: all 22 lettered findings (2026-08-19) and all three remaining tiers (2026-08-20, `AUDIT-VERIFICATION-2026-08-20-TIERS-1-2-3.md` + ten `audit-t*.md`). Open defects are backlog **51–126** in neutral language |
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

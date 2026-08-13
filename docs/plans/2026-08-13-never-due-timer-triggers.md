# Plan — A timer arm must agree with the scheduler (ADR-0176, blocker 2 + livelock)

> Spec: [`docs/specs/2026-08-13-never-due-timer-triggers.md`](../specs/2026-08-13-never-due-timer-triggers.md)
> ADR: [`docs/adr/0176-reject-never-due-timer-triggers.md`](../adr/0176-reject-never-due-timer-triggers.md)
> Audit: `docs/specs/2026-08-13-adr-0176-audit-lens-{a,b,c}.md`

## ▶ Progress

- **Branch**: `feat/never-due-timer-triggers`. ⚠ Do not quote this bundle's own SHA here — the
  amend that lands each phase changes it.
- **Status**: **IMPLEMENTED; both review gates PASSED.** `/code-review`: 5 findings, 4 fixed, 1
  fixed-in-part with the remainder adjudicated. `/security-review`: **0 findings.** Ready to
  merge `--no-ff` after a suite re-run on the merged tree.
- **Phases landed**: **P1, P2, P3 — all of them**, in that order.
- **Base**: `main` @ `e04bd670`.
- **Verification, re-run after every review fix (all executed, judged by exit code):**
  `go test -race ./...` **EXIT=0** over **64 packages, no races**; repo coverage **74.4 %**
  (baseline 74.2 %); touched packages **`runtime` 93.4 %**, **`scheduler` 93.1 %** — both well
  above the 85 % floor, every new function at 100 % except `sortedClockTimes` 90 %.
  `go build ./...` EXIT=0 · `go vet ./...` EXIT=0 · `golangci-lint run ./...` **repo-wide, 0
  issues**. Docker was up; nothing was skipped.
- **All five prescribed mutations executed**, each RED observed, each restored from a `cp`
  backup with a clean `diff`. Mutation 2 behaved exactly as the plan predicted: deleting
  `|| next.IsZero()` left the arm-level test GREEN and turned only the predicate test RED —
  which is *why* that predicate test exists.
- **A sixth mutation was added**: the at-time comparator moved from `sort.Slice` to
  `slices.SortFunc`, and its minute/second tie-breaks had no test at all. Two pins added and
  mutation-verified (collapse the comparator to hour-only → both RED).
- **Both livelock regression tests were demonstrated, not asserted**: each hung for its full
  10 s timeout before the guard and returns in <1 s after — on the fresh-arm path
  (`TestDriveDoesNotWedgeOnALivelockingTimerArm`) and, newly, on the **boot** path
  (`TestRehydrateTimersDoesNotWedgeOnALivelockingRow`).
- ⚠ **Implementation refuted four of the bundle's own claims.** All four are folded back into
  the ADR (Decision 1, Decision 2, Consequences) and the spec §6.1/§6.2, with the measurements
  recorded in **§15 of `docs/specs/2026-08-13-adr-0176-measurements.md`**:
  1. "the raw start-timer path, **ungated today**" — false. Both shipped `Scheduler`
     implementations already refuse an `!ok` trigger in their own `Schedule`, **including the
     livelocking shape**, so it never reached `Activate` by that route. The guard's real
     justification is the **port**: a consumer-supplied scheduler was measured arming a
     never-due job with a zero next run and a nil error. Written as prescribed, that RED test
     would have been **vacuous**.
  2. "`IsZero()` is what closes the 30-February cron" — false once P1 lands first, which makes
     that cron report `!ok`. After P1 **no trigger shape reaches the `IsZero()` half**; it is
     kept as blocker 2's invariant plus a P1 regression guard, pinned by a predicate test.
  3. The rehydration wedge is **not** about a zero stored `next_run`. Rehydration re-arms from
     the trigger, so a row with a valid stored instant (`2026-08-31`) wedges a February boot,
     while a stored-zero row whose trigger still fires **heals**. Keying on the stored zero
     would have missed the first and stranded the second.
  4. "normalise an out-of-range weekday" understated it: such a weekday **ignores the interval**
     and **beats an in-range weekday the anchor has passed**. `calendarNext`'s weekly branch is
     now a transcription of gocron's `weeklyJob.next`. A **fifth** shape class therefore changes
     answer that the ADR never named — a MIXED in-range/out-of-range weekday set.
- **Two existing `scheduler` tests were changed** (the declared behaviour change: empty weekday /
  day-of-month sets). **No other test in the repo moved** — that is the evidence the weekly
  rewrite is behaviour-preserving for ordinary weekday sets.
- **Bonus cleanup**: the three stale `.claude/worktrees/agent-*` from the audit were verified
  clean (`git status --porcelain` empty, audit records already committed in-repo) and removed.

- **Scope was CUT after the audit** (owner decision): this delivery fixes the `Next`/scheduler
  divergence, the arm-path guard, and rehydration. The build gate, the step gate,
  `StepOptions.SchedulingLocation` and moving the calendar math are **deferred to their own
  ADR** — each for a measured defect, listed in the ADR's Consequences. Do **not** reintroduce
  them here.
- **Load-bearing measurements** (spec §2–§4; raw output in-repo at
  `docs/specs/2026-08-13-adr-0176-measurements.md` — ⚠ read it BACKWARDS: **§16 (`/code-review`)
  and §15 (what implementation refuted) supersede §14 and §13, which supersede §1–§10**):
  MySQL rejects the zero literal (not a range floor — it accepts year-1); `Next` disagrees with
  live `Activate` in BOTH directions; `Cron("0 0 30 2 *")` reports `ok=true` with a zero
  instant; `Activate` on `Monthly(12,[31])` **never returns** in the five months without a
  31st; `PruneTimers` deletes 1 of 5 such rows.
- **Adjudicated audit findings**: all 8 Criticals accepted. Three changed the design (see spec
  §8); five removed scope. Majors accepted: the `iff` header, the `ApplyTrigger`-not-`Drive`
  citation, the MySQL zero-literal mechanism, the wider anchor class, the unbounded-`interval`
  scan cost (filed), the pre-existing fourth gate in `Schedule`.

### `/code-review` outcome — 5 findings, all addressed (detail in measurements §16)

The reviewer independently differentially fuzzed `Trigger.Next` against gocron v2.22.0's own
`next` implementations over ~30k combinations including DST: **0 mismatches**, confirming the
weekly transcription and negative-day handling. Every finding was about shapes *outside* that
reconciliation.

1. **F1 Major — FIXED.** Eight more shapes reported a fire instant that gocron refuses at setup
   (a bad entry anywhere in a monthly day list, `EveryRandom` with `min >= max`, any at-time with
   hours > 23 / minutes / seconds > 59). Same failure mode as blocker 2 minus the zero literal.
   Re-measured after: **all 12 probed shapes agree with live `Schedule`**. Closes backlog 25.
2. **F2 Major — FIXED, window narrowed, residual documented.** TOCTOU: the guard reads the clock
   before the transaction, `activateJob` discards our instant and lets gocron re-derive at its
   own later reading. `Monthly(12,{31})` is armable at `2026-01-31T23:59:59Z` and never-due one
   second later. Reproduced deterministically with a store double that advances a fake clock
   inside the commit — `Drive` hung its full 10 s. A pre-`Activate` re-check fixes it. ⚠ It does
   **not** close the window, only narrows it; recorded as an accepted residual.
3. **F3 + F4 Minor — metric FIXED, error-propagation half ADJUDICATED.** Added
   `wrkflw_timer_arms_refused_total` at all four refusal sites. **Declined** F4's proposal to
   fold never-due skips into `jobStore.Load`'s `unresolved` error: that makes *boot* fail, and
   not failing boot is the entire point of the rehydration guard.
4. **F5 Minor — FIXED in part.** The monthly scan was linear in a consumer-supplied `interval` at
   DAY granularity (6.34 s at `interval = 120000`). Off-grid months are now skipped whole →
   392 ms. **Proven semantics-preserving by 255,438 differential comparisons** against a verbatim
   copy of the old walk, across UTC / `America/New_York` (DST) / a +05:30 zone. ⚠ Still linear in
   `interval`, ~16× smaller constant — backlog 26 stays open with the new numbers.

**`/security-review`: 0 findings.** Three items it explicitly adjudicated as NOT vulnerabilities:
the permanently-skipped rehydration row (availability, and strictly better than the wedge it
replaces), `scheduleStartTimerJob` now erroring where it used to succeed-then-never-fire
(availability), and an `int(interval)*7` overflow in `weeklyNext` at interval ≥ ~1.3e18 — no trust
boundary is crossed, and gocron's own transcribed algorithm overflows identically, so the two
still agree. ⚠ **That last one is a real correctness note carried to the backlog** (item 30).

⚠ **Two process lapses in this round, both disclosed rather than hidden:**
- The refusal **counter was written before its test**. Verified retroactively by mutation (delete
  the `Add`, observe RED, restore) — CLAUDE.md's disclosed-lapse path, not the intended cycle.
- The **scan guard's first timing bound was wrong**, and only the full `-race` run caught it: 3 s
  was tuned on the plain measurement, but `-race` is ~8× slower and the passing case costs 3.17 s
  there. Retuned to interval 12 000 / 2 s after measuring **both** modes, and mutation-verified
  RED under `-race`. *A timing bound measured in one mode is a claim about that mode only.*
## Fan-out rule

Fan out **by Go package**. The chain is short and mostly serial:

```
scheduler  →  runtime  (guard sites + rehydration)
   P1              P2, P3
```

`P2` and `P3` are both in `runtime`, so they run **strictly serial** — concurrent agents in one
package break each other's `go test` compile even on disjoint files.

Every subagent brief must state: TDD RED-first with an observable red state per new symbol; the
exact verification command; and, for any agent needing containers, that **Docker use is
explicitly authorised for that task** (standing permission covers only the two Verification
runs).

## ⚠ Phase ordering is load-bearing

P1 must land before P2. A guard keyed on `!ok` applied *before* P1 would refuse
`Weekly(1,nil)`, `Monthly(1,nil)`, `Weekly(1,[Weekday(9)])` and `Monthly(1,[-1])` — four shapes
that **arm and fire today** (spec §3). Landing them out of order ships a regression.

## Phase 1 — `scheduler`: make `Next` agree with the live scheduler

**Step 0 — derive the truth, do not guess it.** Probe `Activate` for each candidate shape and
record the instant gocron actually schedules. Spec §3 establishes *that* four shapes arm; it
deliberately does **not** fix their instants, because guessing them is how the first design
failed. Probe at several clock months, including one with no 31st.

1. **RED** — a table test asserting `Next` reports the instant gocron actually arms for:
   an empty weekday set, an empty day-of-month set, an out-of-range weekday, and a **negative**
   day-of-month (`-1` = last day of month). Fails today: all four report `!ok` with the zero
   time (measured).
2. **RED** — `Cron("0 0 30 2 *")` reports **`!ok`**, not `(zero, true)`. Fails today: measured
   `ok=true` with a zero instant, because `robfig/cron` gives up after five years and returns
   the zero time with no error.
3. **RED** — `Monthly(12,[31])` at a February anchor still reports `!ok`. ⚠ This is a
   **regression guard, not a new behaviour** — it passes today, so write it as a pin and
   confirm by mutation that it can fail (break the interval filter). It is what lets P2 refuse
   the arm before the livelock.
4. **GREEN** — correct `calendarNext` and the cron path.
5. Fix the rotted doc at `scheduler/trigger.go:170-175`: its "exactly when" enumeration omits
   the whole scan-exhaustion class, and — more importantly — it presents `Next` as authoritative
   about firing. Replace with the branch conditions **plus** a statement that gocron
   substitutes defaults for empty day sets and supports negative day-of-month, so `!ok` is a
   statement about `Next`, reconciled with the scheduler as of ADR-0176.
6. ⚠ **Do not touch `scheduler/selfcontainment_guard_test.go`.** Nothing in this scope requires
   an outside import; if you find yourself needing one, stop and report.

**Verify**: `go test -count=1 ./scheduler/... ; echo EXIT=$?`

## Phase 2 — `runtime`: guard the arm paths (STRICTLY SERIAL with P3)

1. **RED** — `timerJobsFor` produces **no arm** for a never-due `ScheduleTimer`. Construct the
   command **directly**. Fails today: the arm is appended with a zero `NextRun` (measured).
2. **RED** — the same for a zero-instant trigger where `Next` reports `ok=true`. ⚠ Distinct
   test: it is the half `!ok` cannot catch, and after P1 the cron case reports `!ok`, so this
   test must reach the `IsZero()` branch by another route or assert the predicate directly.
   **State in the test comment which half of `!ok || next.IsZero()` it pins.**
3. **RED** — `armStartTimer` / `scheduleStartTimerJob` refuses a never-due `StartEvent.Timer`.
   Fails today: that path is ungated (audit finding).
4. **GREEN** — the shared predicate at all three sites, WARN-logging timer id, instance id and
   kind.
5. **RED/GREEN** — **the livelock regression test**: arming `Monthly(12,[31])` with the
   scheduler clock in a month with no 31st must return promptly instead of hanging. ⚠ Give it
   an explicit short timeout and run the arm in a goroutine with a `select`; a naive test that
   simply calls the arm will hang the whole package run for the full `go test -timeout`. Fails
   today: `Activate` never returns (measured).
6. Fix `runtime/timerjob.go:66-69`, whose comment carries three false claims — it calls the
   not-ok path "impossible" and asserts "the scheduler re-validates at arm time anyway", which
   is exactly the gap this ADR closes.

**Verify**: `go test -count=1 ./runtime/... ; echo EXIT=$?` (needs Docker — `./runtime/...` is
not container-free).

## Phase 3 — `runtime`: the rehydration gate (STRICTLY SERIAL after P2)

1. **RED** — `jobStore.Load` skips a persisted row whose `next_run` is zero, so it never
   reaches `Activate`. Fails today: `rehydrateTrigger` returns recurring triggers verbatim and
   every never-due kind is recurring, so the row is re-armed at boot (audit finding).
2. **RED** — a legacy zero row does not wedge boot: rehydrating a store seeded with one
   completes promptly. Same goroutine+timeout shape as P2.5.
3. **GREEN** — the guard, with a WARN naming the skipped row.
4. Document the **manual remediation** for existing rows in the ADR's Consequences — audit-
   measured, `PruneTimers` deletes only 1 of 5, so nothing reclaims them automatically.

**Verify**: `go test -count=1 ./runtime/... ; echo EXIT=$?`

## Prescribed mutations

Break the impl on purpose, observe RED, restore from a **`cp` backup**, `diff` to confirm.
⚠ Never `git checkout <path>` — it restores from the index and has destroyed uncommitted work
twice in this repo.

1. Delete the arm guard → P2.1 must go RED.
2. Remove the `|| next.IsZero()` half → P2.2 must go RED and P2.1 must stay GREEN. If P2.2
   also stays green, the test is not reaching that branch.
3. Restore `calendarNext`'s empty-weekday-set early return → P1.1 must go RED.
4. Remove the rehydration skip → P3.2 must go RED (it should hang, so rely on its timeout).
5. Break `Monthly`'s interval filter → P1.3 must go RED, proving that pin can fail.

⚠ **A mutation that cannot discriminate is evidence the CLAIM is wrong, not that the test is
weak.** Record such a finding rather than strengthening the test to force a red. The previous
delivery shipped two prescribed mutations that were the wrong mutation.

## Verification checklist

- [x] Every new exported symbol had an **observable** red state before its implementation.
      (P1: 19 of 24 table cases RED, the pin and the controls green. P2: `undefined:
      neverDueNextRun` build failure, then the livelock test hanging its full 10 s. P3: two
      Load cases RED plus `RehydrateTimers` hanging 10 s.)
- [x] All five prescribed mutations executed, each RED observed, restored from a `cp` backup,
      `diff` clean. **Plus a sixth**, for the at-time comparator this delivery rewrote.
- [x] The livelock regression test genuinely fails (it hangs) and passes after — demonstrated,
      not asserted. **Twice**: fresh-arm path and boot-rehydration path.
- [x] `go test -race -coverprofile=cover.out ./...` **EXIT=0**, 64 packages, no races;
      `scripts/coverage.sh` **74.3 %** repo-wide (baseline 74.2 %); touched packages
      `runtime` **93.3 %** / `scheduler` **92.0 %**. Docker was up and nothing was skipped.
- [x] `go test ./...` — no regressions; judged by exit code, `-count=1`.
- [x] `go vet ./...` EXIT=0 — compiles every test file including Docker-only ones.
- [x] `golangci-lint run ./...` **repo-wide, EXIT=0, 0 issues** (one `unused` field found and
      fixed first — it was never "clean on the first run").
- [x] `git diff --stat` shows **no** change to `scheduler/selfcontainment_guard_test.go`.
- [x] Documents match what shipped — four refuted claims folded into the ADR and spec, with
      measurements in §15. Diff comments swept for unexecuted claims.
- [x] ADR status flipped `Proposed` → `Accepted`.
- [x] `/code-review` — 5 findings: 4 fixed, F4's error-propagation half explicitly adjudicated
      (declined, with reason). Detail above and in measurements §16.
- [x] `/security-review` — **0 findings.** It source-verified the two gocron validation claims
      `clockTimesSchedulable`/`monthDaysSchedulable` rest on, independently differential-tested
      the month-skip over 356,400 combinations (0 mismatches, including `America/Santiago`'s
      midnight DST transition), and confirmed no refusal path can fail a security control open —
      `TimerCompensationStall` and `TimerRetry` both build `AfterDuration`, whose `Next` is
      unconditionally ok with a non-zero instant, so the predicate cannot fire on them.
- [x] Suite re-run after every review fix (the `-race` run is what caught the bad timing bound).
- [ ] Suite re-run **on the merged tree** before pushing.
- [x] `HANDOVER.md` rewritten in place; this `▶ Progress` updated; memory topic + index line.
- [x] Backlog updated: the deferred gates, `EveryRandom(min>max)`, the unbounded-`interval`
      scan cost (**now `Daily`/`Monthly` only** — the weekly kind no longer scans), the
      un-migrated orphan rows, the semantically-invalid-definition round-trip.

## Risks

- **P1 is the risk concentration.** It changes `calendarNext`, which drives live firing. The
  existing `scheduler` suite is the guard; if a test there needs editing, that means behaviour
  changed — report it, do not absorb it.
- **The livelock tests can hang the suite.** Every test that arms a suspect trigger needs a
  goroutine + `select` + short timeout, or a failure becomes a 10-minute package timeout.
- **`Next` is consumed elsewhere.** Changing its answers for four shapes may affect
  `Schedule`'s pre-existing never-due gate and `processtest`. Enumerate `Next`'s callers before
  P1 and check each.
- **The four newly-schedulable shapes are a behaviour change to a public API.** They already
  armed and fired, so no working definition changes — but `Next` itself is exported, and a
  consumer asserting `!ok` on them would see a change. Worth a release note.

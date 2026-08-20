# scheduler/ sweep wave 1 — execution evidence

Agent-owned packages: `scheduler`, `scheduler/internal/gocron` (+ `myelector`).
Branch `main`, main working tree, no commits made by this agent.

Machine/mode note for every timing claim below: darwin/arm64, `go test -count=1`
without `-race` unless the line says `-race`. `-race` is roughly 8× slower here,
so each duration states its mode.

---

## Item 49 — a past-due one-shot expressed as a DURATION was refused — DONE

**Files changed**

- `scheduler/internal/gocron/trigger.go` — new unexported `oneShotFireTime(TriggerDef, now) (time.Time, bool)`;
  `jobDefinition`'s `triggerDefOneTime` case now resolves the due instant through it and applies the
  past-due `OneTimeJobStartImmediately()` guard to BOTH one-shot forms (previously only `At`).
- `scheduler/internal/gocron/job_schedule.go` — `ScheduleJob`'s `fireImmediately` predicate switched from
  `trig.AbsTime()` to `oneShotFireTime(trig, now)` (it could not see the duration form at all); the
  `s.sched.NewJob` error is now wrapped `workflow-scheduler: ScheduleJob %q: %w`; the doc comment's
  ⚠ caveat (lines 28-33), which described the bug as live, was rewritten to describe the fix.
- `scheduler/internal/gocron/job_schedule_test.go` — two new tests (below).

**RED 1 — `TestGocronScheduleJob_PastDueDurationOneShot`** (3 rows; `After(-1m)`, `After(0)`, control `After(+5s)`).
Observed, `go test -count=1 -run … -v ./scheduler/internal/gocron/...`, EXIT=1:

```
--- FAIL: TestGocronScheduleJob_PastDueDurationOneShot/After(-1m)_fires_immediately_instead_of_being_refused
    Error: Received unexpected error:
           gocron: OneTimeJob: start must not be in the past
--- FAIL: TestGocronScheduleJob_PastDueDurationOneShot/After(0)_fires_immediately_instead_of_being_refused
    Error: Received unexpected error:
           gocron: OneTimeJob: start must not be in the past
--- PASS: TestGocronScheduleJob_PastDueDurationOneShot/After(+5s)_still_waits_for_the_clock
```

The control row passing on unfixed code is deliberate: it is the row that would catch a
"make every duration one-shot fire immediately" over-fix, and it is not part of the RED.

**RED 2 — `TestGocronScheduleJob_NewJobErrorIsWrapped`** (2 rows). The reachability of a
`NewJob` error was established by executing a throwaway probe first (deleted), which produced:

```
Every(0)        => next=0001-01-01 00:00:00 +0000 UTC err=gocron: DurationJob: time interval is 0
Every(-1s)      => next=0001-01-01 00:00:00 +0000 UTC err=gocron: DurationJob: time interval must be greater than 0
Cron("not a cron") => next=0001-01-01 00:00:00 +0000 UTC err=gocron: CronJob: crontab parse failure
                                                          expected exactly 5 fields, found 3: [not a cron]
```

Observed RED, EXIT=1:

```
Error: Error "gocron: DurationJob: time interval is 0" does not contain "workflow-scheduler: ScheduleJob \"job-newjob-error\""
Error: Error "gocron: CronJob: crontab parse failure\nexpected exactly 5 fields, found 3: [not a cron]" does not contain "workflow-scheduler: ScheduleJob \"job-newjob-error\""
```

**GREEN** — `go test -count=1 ./scheduler/... ` EXIT=0 (whole tree: `scheduler` 14.6 s,
`internal/gocron` 3.0 s, `myelector` 11.8 s, `pgelector` 4.8 s, `internal/obs` 1.4 s).
The two new tests also pass under `-race` (EXIT=0, 3.1 s).

**Premise check.** The triage entry's reproduction is exact — the escaping string and the zero
`next` both reproduced verbatim. Nothing in the item was found false.

**Left for the doc owner (NOT edited by this agent).** `docs/plans/HANDOVER.md:383` and `:390`
still describe the duration form as refused, quoting the raw gocron string. Those lines are now
false; the file belongs to another agent this wave.

---

## ⚠ Coordinator hang report — RESOLVED, NOT item 49

**Claim received:** another agent's `go test ./runtime/` hung for 10 minutes in
`TestGocronSchedulerDrivesRunnerToCompletion`, isolated (via a pristine-`scheduler/` worktree)
to my uncommitted edits; hypothesised as an ADR-0176-class re-arm livelock introduced by
item 49's past-due `StartImmediately` guard.

**Executed, not reasoned.** With the item-49 fix in place and no mutation applied:

```
go test -count=1 -timeout 60s -v -run TestGocronSchedulerDrivesRunnerToCompletion ./runtime/
EXIT=0   === RUN present, --- PASS: TestGocronSchedulerDrivesRunnerToCompletion (0.01s)
same, -race:  EXIT=0  --- PASS (0.00s)
```

(`-run` on a nonexistent name exits 0, so the `=== RUN` line and the `--- PASS` line were both
confirmed present; the test really ran.)

**The actual cause: my own item-44 vacuity-ablation mutation, which was live in the shared
working tree in short windows.** Reproduced deliberately by re-applying it:

- **Mutation A — `ScheduleJob` registers nothing** (`return now, nil` before `s.sched.NewJob`).
  `TestGocronSchedulerDrivesRunnerToCompletion` **hangs**, exactly as reported. Goroutine dump
  from the timeout kill:

  ```
  panic: test timed out after 40s
  goroutine 1 [chan receive]:
  github.com/jonboulle/clockwork.(*FakeClock).BlockUntilContext(…) clockwork@v0.5.0/clockwork.go:244
  github.com/kartaladev/wrkflw/runtime_test.TestGocronSchedulerDrivesRunnerToCompletion(…)
      runtime/processdriver_scheduler_e2e_test.go:95
  ```

  No job is registered, so no fake-clock waiter ever appears and the test's
  `BlockUntilContext` barrier at `:95` never returns. This is a **blocked barrier, not a
  livelock** — the goroutine is parked in `chan receive`, burning no CPU.
- **Mutation B — the task is swallowed** (job registers and fires, caller's callback ignored):
  the same test **fails in 2.00 s**, not hangs:
  `processdriver_scheduler_e2e_test.go:105: service action did not run after timer fired: context deadline exceeded`.

So the reported symptom matches mutation A precisely and matches the shipped fix not at all.
Both mutations were restored by `cp` from a pre-mutation backup and confirmed byte-identical
with `diff`; `git diff -- scheduler/ | grep -n "MUTATION\|_ = task\|_, _ = def"` finds nothing.

**Livelock hypothesis, refuted on its own terms.** The item-49 guard cannot re-arm: it applies
only to `triggerDefOneTime`, which carries `WithLimitedRuns(1)`. Both past-due rows of
`TestGocronScheduleJob_PastDueDurationOneShot` now assert this directly —
`require.Never(fired > 1, 150ms)` after the `require.Eventually(fired >= 1)` that licenses it —
and pass. The ADR-0176 class needs a trigger whose *next* run is re-derived and still past-due;
a one-shot has no next run.

**Process lesson (for the controller): a shared working tree cannot host a mutation ablation.**
Item 44's whole method is "break production on purpose, observe, restore", and every window in
which that mutation is live is a window in which a concurrent agent's unrelated run fails or
hangs, with the failure pointing at the wrong package. Mutation ablations belong in a
`git worktree`, or the tree needs an exclusive lock for their duration.

---

## Item 44 — the 16 `Never` sites are vacuity-prone — DONE (15 hardened, 1 tracked-not-faked)

**The falsifier, established first.** Two mutations of `GocronScheduler.ScheduleJob`:

- **A: register nothing.** Too coarse — it defeats the `clk.BlockUntilContext` barriers that
  most of these tests already have, so they die as unnamed 600 s binary timeouts rather than
  passing. Useful as evidence, useless as a vacuity probe.
- **B: the job registers, arms a fake-clock waiter and fires normally, but the CALLER's callback
  is never invoked.** Every barrier still passes; nothing the caller asked for ever runs. This
  is the real vacuity probe and is what the numbers below use.

**Measured vacuity under mutation B, per site (before any change):**

| site | result under B |
|---|---|
| `internal/gocron/scheduler_test.go:141` cancel prevents fire | **PASS (0.20s) — vacuous** |
| `internal/gocron/scheduler_test.go:195` `func() bool { return false }` | **vacuous unconditionally** — the condition cannot become true; it was a 100 ms sleep |
| `internal/gocron/clock_option_test.go:87` | **PASS (0.20s) — vacuous** |
| `scheduler/clock_option_test.go:89` | **PASS (0.20s) — vacuous** |
| `scheduler_surface_test.go:169` manual job | **PASS — vacuous** |
| `scheduler_surface_test.go:256` Deactivate | **PASS (0.15s) — vacuous** |
| `scheduler_surface_test.go:289` Cancel | **PASS (0.15s) — vacuous** |
| `internal/gocron/scheduler_test.go:161` replace | hang → unnamed 600 s timeout |
| `internal/gocron/scheduler_test.go:220` exactly once | hang → unnamed 600 s timeout |
| `internal/gocron/bump_regression_test.go:47` | hang → unnamed 600 s timeout |
| `job_schedule_test.go:68/88/128/160` (4) | **FAIL by name at 10 s** — already guarded by the `require.Eventually` above each |
| `scheduler_surface_test.go:228` double-Activate | **FAIL by name at 10 s** — already guarded |
| `myelector/mysql_elector_heartbeat_test.go:64` | not covered by B (different machinery) |

**Changes.** New `scheduler/internal/gocron/liveness_test.go` and `scheduler/liveness_test.go`
(`newFireCanary` / `scheduleFireCanary` + `requireCanaryFired`): a canary is an ordinary sibling
job on the same scheduler and the same fake clock whose firing proves the whole chain the
subject depends on was alive across the window. **No `Never` budget was raised** — all 16 keep
their original 100/150/200/300 ms.

- 7 vacuous sites: canary at the same due instant (or, for the two "not fired without advance"
  tests, an advance-then-`Eventually` on the SAME registration placed *after* the `Never`, since
  the point of those tests is that nothing may happen until the clock moves).
- `scheduler_test.go:195`'s `return false` now names its actual subject (`oldFired > 0`).
- 3 hang-only sites: `wg.Wait()` → `require.Eventually(…, eventuallyBudget)`, so a broken run
  fails by name in 10 s instead of killing the binary at 600 s with no assertion message.
- The two "fires exactly once" claims (`scheduler_test.go:220`, `bump_regression_test.go:47`)
  got a **recurring** canary and a second clock advance: proving one fire happened is not
  enough for a no-second-fire claim, because a scheduler that stopped delivering entirely also
  never fires twice. The canary must reach tick 2 inside the window the one-shot must stay at 1.

**Mutation-ablation of the CONVERTED tests (the required proof that each new precondition can
fail).** Mutation B re-applied to the fixed tree, `-timeout 45s`:

```
TestGocronScheduler_Behaviour/cancel_prevents_fire                    FAIL (10.00s)
TestGocronScheduler_Behaviour/replace_reschedules_and_fires_once      FAIL (10.00s)
TestGocronScheduler_Behaviour/replace_then_fire_new…                  FAIL (10.00s)
TestGocronScheduler_Behaviour/callback_runs_exactly_once              FAIL (10.00s)
   msg: liveness canary must have fired at least 1 time(s); until it does,
        a Never window proves nothing about the subject
TestGocronScheduler_WithClock_NotFiredWithoutAdvance                  FAIL (10.20s)
   msg: the same job must fire as soon as the fake clock is advanced
TestBumpRegression_OneShotFiresExactlyOnce                            FAIL (10.00s)
   msg: the one-shot must fire at its due instant
TestNewScheduler_WithClock_NotFiredWithoutAdvance                     FAIL (10.20s)
TestNativeSchedulerSchedule/manual_job_persists_but_leaves_NO_…       FAIL (10.00s)
TestNativeSchedulerDeactivateCancel/Deactivate_disarms_without_Delete FAIL (10.00s)
TestNativeSchedulerDeactivateCancel/Cancel_deletes_from_the_store_…   FAIL (10.00s)
```

Every previously-vacuous site now fails, **by name, inside the 10 s `eventuallyBudget`** —
never as an unnamed binary timeout. Production restored by `cp` from backup, `diff` clean.

**⚠ One site deliberately NOT given a liveness precondition, because the obvious one is fake.**
`myelector/mysql_elector_heartbeat_test.go:64`. I wrote the natural barrier (block until the
heartbeat's ticker is re-armed after each `Advance`), then ablated it — `return` injected after
the first `mysqlRevalidate`, so the heartbeat goroutine dies on tick 1:

```
--- PASS: TestMySQLElectorHeartbeatKeepsLeadershipAlive (11.98s)   ← on a DEAD heartbeat
```

**The barrier does not discriminate**, and the reason is in clockwork: `fakeTicker.expire`
returns `&f.d` and is re-scheduled from inside `Advance` (clockwork@v0.5.0 `ticker.go:60-67`),
so a waiter exists whether or not any goroutine is listening; and `BlockUntilContext(ctx, n)`
is a `len(waiters) >= n` lower bound (`clockwork.go:255-258`). I renamed the helper to
`requireTickerArmed` for what it actually proves, wrote the measured non-discrimination into
the test as a ⚠ block, and left the hole open rather than shipping a false precondition. The
precondition that WOULD work is making the heartbeat act — sever the dedicated connection and
require a step-down, as `pgelector`'s `TestPostgresElectorHeartbeatStepsDownOnConnLoss` does
via `pg_terminate_backend`. MySQL has no equivalent here: killing the elector's connection
needs its `CONNECTION_ID()`, which is not reachable from outside `MySQLElector`.
**Recommend a follow-up backlog item.** The bounded-context change is still a net gain: the
barrier now fails by name instead of hanging to a 600 s binary timeout, and the per-tick loop
stops three back-to-back `Advance`es from collapsing into one delivered tick (a `fakeTicker`
drops a tick whose channel is already full — so "fire several ticks" was itself overstated).

**False premises found in item 44's own filing.**

1. *"'did not fire within 150 ms' passes trivially if the goroutine never ran at all"* — true for
   **7** of the 16 sites, not all 16. Five (`job_schedule_test.go:68/88/128/160`,
   `scheduler_surface_test.go:228`) already carry a `require.Eventually` liveness precondition
   immediately above the `Never` and fail by name under the ablation; three more fail by
   hanging. Left structurally alone; evidence recorded above.
2. The prescribed falsifier — *"stub the scheduler so `ScheduleJob` registers nothing at all …
   observe it still PASSES"* — **does not work as specified**. Registering nothing starves the
   `BlockUntilContext` barriers, so those tests hang instead of passing. The vacuity is only
   visible with the finer mutation B.

**Additional defect found and fixed while here (in `scheduler/`, my package).**
`internal/gocron/scheduler_test.go:138` read
`require.NoError(t, clk.BlockUntilContext(t.Context(), 0))` with the comment
*"drain: confirm gocron released its fake-clock waiter before advancing"*. It confirms nothing:
`newBlocker`'s fast path is `if len(fc.waiters) >= n { return nil }`, and `n == 0` is always
satisfied, so the call returns immediately no matter how many waiters exist
(clockwork@v0.5.0 `clockwork.go:255-258`). Line and comment removed.

**GREEN.** `go test -count=1 -timeout 200s ./scheduler/...` EXIT=0 —
`scheduler` 15.4 s, `internal/gocron` 4.8 s, `myelector` 12.5 s, `pgelector` 6.1 s,
`internal/obs` 0.5 s (plain mode).

---

## Item 123 — `scheduler/job.go`'s foreign-Job comment was false — DONE (comment-only)

**File changed:** `scheduler/job.go` (the `job.singleton` doc comment).

**The false sentence:** *"Consumer-implemented Jobs that don't satisfy it are simply treated as
non-singleton by the façade."*

**Executed, not reasoned.** Throwaway internal probe (`package scheduler`, deleted after the
run) with a `foreignJob` type that deliberately lacks the unexported `singleton()` method, so
the private interface assertion fails for it exactly as for a type declared outside the package:

```
foreign recurring: satisfies singleton() assertion = false
foreign RECURRING  Trigger().Recurring()=true   jobSingleton()=true
foreign ONE-SHOT   Trigger().Recurring()=false  jobSingleton()=false
```

So a foreign **recurring** job is singleton — `true`, not `false`. The backlog's diagnosis is
confirmed; the authority is `jobSingleton` (`scheduler/scheduler.go:561-567`), whose fallback
is `return j.Trigger().Recurring()`.

**Replacement wording** modelled on the correct text 9 lines away in `scheduler.go:557-559`
("it defaults to the safe equivalent: serialized when its Trigger is recurring, unrestricted
when one-shot"), with a pointer to `jobSingleton` as the authority.

**Other comments in `scheduler/job.go` checked against what I measured:** the two sentences
above the false one — "defaults to true for jobs built from a [Trigger.Recurring] trigger" and
"false for one-shot jobs" — are true of `job.singleton()` itself (`j.trig.Recurring()`), and
`WithoutOverrunProtection` forcing false is the `j.noOverrun` early return. No further
divergence found in this file.

---

## Item 30 — `weeklyNext`'s `int(interval)*7` overflows — DONE

**Files changed:** `scheduler/trigger.go` (new `maxSchedulableInterval = 1 << 20`; `calendarNext`'s
first guard widened from `interval == 0` to `interval == 0 || interval > maxSchedulableInterval`,
which covers all three calendar kinds since `calendarNext` dispatches to `weeklyNext`),
`scheduler/trigger_test.go` (`TestTrigger_NextCalendarIntervalCannotOverflow`).

**Observed RED** (anchor Thu 2026-08-20T12:00Z, weekdays `[Monday]`):

```
--- FAIL: …/weekly_MaxUint64_is_refused,_never_a_PAST_next-run_with_ok=true
    Next reported ok=true with a next-run BEFORE the reference instant:
    next=2026-08-10 00:00:00 +0000 UTC  after=2026-08-20 12:00:00 +0000 UTC
--- FAIL: …/weekly_MaxUint32_is_refused_rather_than_arming_80_million_years_out
```

The triage entry's headline finding reproduced **exactly**: a next fire ten days in the **past**,
reported `ok=true`. The triage's control rows also reproduced — `MaxUint64/7`, `Daily(MaxUint64)`
and `Monthly(MaxUint64)` already failed closed and passed before the change.

Per the item's warning, `ok` alone is not enough (it IS `true` on the defect row); every `ok=true`
row also asserts `!next.Before(after)`, and that assertion is what fires.

**Clamp value.** `1 << 20` as the triage proposed: ~87,000 years of months, and it keeps both
overflowing products inside `int` even on a 32-bit platform (`1830 * 1<<20 ≈ 1.92e9 < MaxInt32`).
Fails **closed**, matching the never-due kinds.

**GREEN:** `go test -count=1 -run 'TestTrigger_' ./scheduler/` EXIT=0.

---

## Item 26 — the calendar scan is linear in `interval` — DONE (NOT downgraded; the filed premise is too weak)

**⚠ The item's premise is understated, and I have the numbers.** Backlog 26 called this
effectively a non-issue on one datapoint — `Monthly(120000,{31})` at ~404 ms. That number is
real; it is also one point on a straight line. Measured across the range (anchor 2026-02-04,
day 31, intervals ≡ 0 mod 12 so every grid month is a February and the scan must exhaust):

| interval | plain | `-race` |
|---|---|---|
| 12000 | 0.047 s | 0.316 s |
| 120000 | 0.407 s | — |
| 300000 | 1.011 s | — |
| 786432 | 2.746 s | 20.677 s |
| 1044480 | **3.568 s** | **27.159 s** |

`-race` is 7.6x slower here, consistent with the ~8x rule of thumb. Item 30's clamp bounds this
at ~3.6 s / ~27 s rather than removing it — a multi-second synchronous scan on the arm path,
inside the commit transaction. **So I did the grid jump rather than downgrading the item.**

**Files changed:** `scheduler/trigger.go` — `calendarNext`'s monthly off-grid branch now jumps to
the next **on-grid** month (`monthIndex + (interval - monthIndex%interval)`) instead of the next
month; new DST-safe helper `civilDayIndex`. `scheduler/trigger_test.go` — two new tests.

It is cheap and provably equivalent: `monthIndex` is constant across a calendar month, so every
day of every month strictly between the current one and the next multiple of `interval` was
already guaranteed to be rejected. The set of days that can MATCH is unchanged.

**Observed RED** (`TestTrigger_NextMonthlyScanJumpsWholeGridStrides`, 2 s bound):

```
trigger_test.go:807: Next did not return within 2s: the scan is stepping one month at a
                     time instead of jumping whole interval strides
--- FAIL: TestTrigger_NextMonthlyScanJumpsWholeGridStrides (2.00s)
```

2 s discriminates in both modes: ~1.8x below the plain failing time (3.568 s) and ~13x below the
`-race` one (27.159 s); after the fix it returns in milliseconds in both.

**GREEN:** the same case now completes in 0.00 s.

### ⚠⚠ The cost test could not have caught a WRONG jump — and neither could the existing suite

A jump is arithmetic on a loop index, and its failure mode is silent and *fast*. Ablation:
dropping the `- 1` from `i = civilDayIndex(target) - civilDayIndex(start) - 1` makes the scan
land one day late and never test any grid month's 1st. Measured:

```
go test -run 'TestTrigger_' ./scheduler/   →  EXIT=0   ok  0.528s
```

**The entire `TestTrigger_*` suite stayed GREEN** — including
`TestTrigger_NextAgreesWithLiveScheduler` (the ADR-0176 reconciliation),
`TestTrigger_NextMonthlyScanSkipsOffGridMonths`, and both tests I had just written — on code where
`Monthly(2, {1})` silently reports **never-due**.

So I promoted the throwaway differential probe into a permanent test,
`TestTrigger_NextMonthlyGridJumpMatchesBruteForce`: 630 combinations (5 anchors × 7 day-sets × 18
intervals) compared against an independent brute-force reference that walks grid months one
stride at a time. 0 mismatches on the fixed code; under the same off-by-one it fails immediately:

```
--- FAIL: TestTrigger_NextMonthlyGridJumpMatchesBruteForce
    anchor=2026-02-04T12:00:00Z days=[1] interval=2: ok mismatch
    (got next=0001-01-01 00:00:00 +0000 UTC, want next=2026-04-01 00:00:00 +0000 UTC)
```

Production restored by `cp` from backup, `diff` clean, both times.

---

## Item 28 — out-of-range weekdays change the answer — DONE as documentation (⚠ ACTION NEEDED FROM CONTROLLER)

**No behaviour changed**, per the item's explicit instruction: `weeklyNext` transcribes gocron
v2.22.0's `weeklyJob.next` deliberately, and diverging would re-open the disagreement ADR-0176
closed.

**Measurement re-derived** (not inherited) from a Thursday anchor 2026-08-20T12:00Z, interval 1 —
matches the triage table exactly:

```
[Monday]               next=2026-08-24 ok=true
[Weekday(9)]           next=2026-08-25 ok=true
[Monday, Weekday(9)]   next=2026-08-25 ok=true    ← LATER than [Monday] alone
[Weekday(-1)]          next=zero       ok=false   ← never-due
[Monday, Weekday(-1)]  next=2026-08-24 ok=true
```

**File changed:** `scheduler/trigger.go` — the note went on the **exported `Weekly` constructor's
godoc**, with the table above. That is a better home than a changelog for a behavioural caveat: it
is what a consumer reads at the call site, and it is inside `scheduler/`, so it does not collide
with the agent editing `CHANGELOG.md`.

**⚠ FOR THE CONTROLLER:** a `CHANGELOG.md [Unreleased]` **Changed** entry is still wanted and I did
**not** write it — `CHANGELOG.md` is owned by another agent this wave and needs sequencing. The
table above is ready to paste. Confirmed by `grep` that no existing entry mentions weekday sets
(the two `Weekly` hits are the ADR-0136/0137 location note and the ADR-0140 interval note).

---

## Item 31 (scheduler half) — dangling `§N` ADR citations — DONE

**Both `scheduler/` citations fixed**, `engine/state_compensation.go:423` left to its owner.

**Verified by opening the ADR, not by assuming.** `grep -n "^#" docs/adr/0176-*.md` yields exactly:

```
# 176. A timer arm must agree with the scheduler that will run it
## Context
## Decision
## Consequences
```

No numbered sections, so `§4` resolves to nothing. Replaced with *"ADR-0176's Decision"* at
`scheduler/trigger.go` (`calendarNext`'s godoc) and `scheduler/trigger_test.go`.

**⚠ A related citation I checked rather than assumed.** Four pre-existing `waitbudget_test.go`
files — and the two `liveness_test.go` files I added for item 44 — cite `ADR-0184 §4`. That one
**RESOLVES**: `docs/adr/0184-*.md` carries `### 4. Never budgets are deliberately NOT changed`.
No action; recorded so the next reader does not "fix" a correct citation.

**Recommendation carried forward (not implemented — it is repo-wide, and `scripts/` is not mine
this wave):** the item's own better deliverable is a `scripts/` checker extracting
`ADR-\d{4} §[\d.]+` from `*.go` and asserting each resolves to a heading in the named ADR. It
would have gone RED on these three today and stays RED-able forever; the comment edits do not.

---

## Final verification (all items)

| run | mode | result |
|---|---|---|
| `go build ./...` | — | **EXIT=0** |
| `go test -count=1 ./scheduler/...` | plain | **EXIT=0** (scheduler 14.3 s, gocron 4.3 s, myelector 10.3 s, pgelector 3.4 s, obs 1.5 s) |
| `go test -count=1 -race ./scheduler/...` | `-race` | **EXIT=0** (14.0 / 4.0 / 11.7 / 6.1 / 3.2 s) |
| `go test -count=1 ./runtime/` | plain | **EXIT=0** — the consumer of the item-49 change |
| `golangci-lint run ./scheduler/...` | — | **EXIT=0, 0 issues** (one `gofmt` finding was fixed) |

⚠ `golangci-lint run ./scheduler/...` is a **package-scoped** run, not the repo-wide gate. Other
packages were being edited concurrently, so a repo-wide lint/test would have mixed in other
agents' in-flight work; that gate is the controller's to run at merge.

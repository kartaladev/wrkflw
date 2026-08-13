# A timer arm must agree with the scheduler that will run it

- Date: 2026-08-13 (rewritten after the rule-#9 audit refuted the first design)
- Decision record: [`docs/adr/0176-reject-never-due-timer-triggers.md`](../adr/0176-reject-never-due-timer-triggers.md)
- Plan: [`docs/plans/2026-08-13-never-due-timer-triggers.md`](../plans/2026-08-13-never-due-timer-triggers.md)
- Audit record: `docs/specs/2026-08-13-adr-0176-audit-lens-{a,b,c}.md` (1,874 lines, 8 Criticals)
- Closes pre-v0.1.0 **blocker 2**, and a **whole-process availability defect** found by the audit
- Base: `main` @ `e04bd670`

> Every factual claim about current behaviour was **executed**. §3 is the load-bearing
> measurement; the rest of the design follows from it. Claims that could not be executed are
> marked `ASSUMPTION (unverified)`.
>
> ⚠ **This spec replaces an earlier design that the audit refuted.** §8 records what was
> wrong and why, because the refuted claims are instructive: the first design measured
> `Trigger.Next` and treated it as the arming authority. It is not.

## 1. The defect

`runtime/timerops.go:156-160` computes an arm's persisted `next_run` from
`scheduler.Trigger.Next`, and keeps the **zero time** when `Next` reports `ok == false`:

```go
var nextRun time.Time
if next, ok := strig.Next(now); ok {
    nextRun = next.UTC()
}
arms = append(arms, driver.newTimerJob(def, instanceID, cmd.TimerID, cmd.Trigger, strig, nextRun, cmd.Kind))
```

That arm is then persisted **inside the state-commit transaction**
(`runtime/processdriver.go:730-757`; `jobStore.Save` per arm, errors abort `commitFn`, which
`:758-767` runs under `RunInTx`), and only **after** the commit is the scheduler touched —
where `Activate` failure is deliberately WARN-only and benign (`:773-788`).

Two consequences, both measured (§2, §3):

1. A zero `next_run` reaches the store. MySQL rejects it, failing the whole step, so **the
   instance can never advance past that node**. Postgres and SQLite accept it, hanging the
   instance and permanently poisoning `Stats.NextFireAt`.
2. Worse, and found only by the audit: for one shape of trigger the post-commit `Activate`
   call **never returns**, wedging the entire scheduler goroutine for the whole process.

## 2. Store behaviour — measured on all three dialects

| dialect | `UpsertJob` with a zero `NextRun` | reads back | `Stats.NextFireAt` |
|---|---|---|---|
| Postgres | accepted | `0001-01-01T00:00:00Z` | `0001-01-01T00:00:00Z` |
| SQLite | accepted | `0001-01-01T00:00:00Z` | `0001-01-01T00:00:00Z` |
| MySQL | **REJECTED** | — | — |

```
workflow-store: upsert timer "probe-inst"/"probe-timer": Error 1292 (22007):
Incorrect datetime value: '0000-00-00' for column 'next_run' at row 1
```

⚠ The rejection is of the **zero literal**, not a range floor. The audit measured MySQL
*accepting* year-1 and year-999 values, so the mechanism is that the driver renders Go's zero
time as `'0000-00-00'` and strict mode refuses it — **not** that `DATETIME`'s range begins at
`1000-01-01`. (An earlier draft of this spec asserted the range explanation. It was wrong.)

On Postgres/SQLite the row also **sorts first** in the `(next_run, instance_id, timer_id)`
keyset index, so it becomes `Stats.NextFireAt` forever.

The rollback consequence needs no new proof: the existing passing test `assertSameTxAtomicity`
(`runtime/timer_txflow_test.go:189-247`) injects an `UpsertJob` failure and asserts the step
errors, the instance version does not advance, and the pre-step rows are restored. ⚠ It
asserts on **`ApplyTrigger`**, not `Drive` as an earlier draft claimed, and it never runs on
MySQL — but it does exist, does assert the rest, and **can** fail (audit-verified against the
fixture, not just a matching line of text).

## 3. ⭐ The arming truth table — `Next` is not the authority

This is the measurement the whole design rests on. Each trigger was armed through the **live**
`NativeScheduler.Activate` — the ADR-0134 durable path the runtime actually uses — with the
scheduler clock pinned to `2026-02-10` (a month with no 31st):

| trigger | `Next.ok` | `Next` zero | live `Activate` |
|---|---|---|---|
| `Every(0)` | false | true | error `DurationJob: time interval is 0` |
| `Cron("not-a-cron")` | false | true | error `CronJob: crontab parse failure` |
| **`Cron("0 0 30 2 *")`** | **TRUE** | **TRUE** | error `CronJob: invalid crontab` |
| **`Weekly(1,nil)`** | false | true | **nil — ARMS AND FIRES** |
| **`Monthly(1,nil)`** | false | true | **nil — ARMS AND FIRES** |
| **`Weekly(1,[Weekday(9)])`** | false | true | **nil — ARMS AND FIRES** |
| `Monthly(1,[0])` / `[32]` | false | true | error `daysOfTheMonth must be between 31 and -31 inclusive, and not 0` |
| **`Monthly(1,[-1])`** | false | true | **nil — ARMS AND FIRES** |
| **`Monthly(12,[31])`** | false | true | ***LIVELOCK — Activate never returns*** |
| `Daily(0)` | false | true | error `DailyJob: interval must be greater than 0` |
| `Weekly(0,[Mon])` | false | true | error `WeeklyJob: interval must be greater than 0` |

`Trigger.Next` disagrees with the scheduler **in both directions**:

- **Four specs arm and fire while `Next` reports `!ok`.** `Weekly(1,nil)`, `Monthly(1,nil)`,
  `Weekly(1,[Weekday(9)])` and `Monthly(1,[-1])`. gocron substitutes defaults for an empty day
  set and normalises an out-of-range weekday; and it treats a **negative** day-of-month as
  counting back from month end (`-1` = last day) — a legitimate feature `calendarNext` does
  not model at all.
  ⇒ **A naive `!ok` arm guard would regress four working definitions.**
- **One spec reports `ok=true` with a ZERO instant.** `Cron("0 0 30 2 *")` — 30 February.
  `robfig/cron` gives up after five years and returns the zero time with **no error**, and
  `scheduler/trigger.go` returns `sched.Next(after), true` unconditionally. So the runtime
  persists a zero `next_run` in-transaction, and only then does `Activate` fail post-commit,
  where failure is benign. `0 0 31 4 *` is an ordinary human typo.
  ⇒ **`!ok` alone cannot close blocker 2. The guard needs `|| next.IsZero()`.**
- **One spec livelocks.** See §4.

This divergence is itself a defect: **ADR-0140 states that `Next`'s first reported fire
matches the live scheduler's first fire.** It does not.

## 4. ⚠⚠⚠ The livelock — a whole-process availability defect in shipped code

`Activate` on `Monthly(12,[31])` **never returns** when the scheduler's clock is in a month
with no 31st. Reproduced independently of the audit:

```
clock = clockwork.NewFakeClockAt(2026-02-10)  → Activate DID NOT RETURN within 10s
clock = real (2026-08-13; August HAS a 31st)  → Activate RETURNED err=<nil>
```

- **Mechanism** (audit, with captured stack): gocron v2.22.0's `monthlyJob.next` spins
  forever, inside gocron's single `selectNewJob` goroutine — so **every** job's arm, cancel and
  rehydrate in the process blocks behind it, not just this instance's.
- **It is clock-month dependent.** With `interval == 12` only months congruent to the anchor
  month qualify, so the wedge window is the five months without a 31st (Feb, Apr, Jun, Sep,
  Nov). ⚠ This is why a first reproduction attempt on the real August clock reported "no
  livelock" and would have been read as a refutation.
- **gocron v2.22.0 is a hard pin** (ADR-0135), so "upgrade" is not an available fix. The arm
  must be refused **before** `Activate` is called.
- ⚠ **It inverts the dialect framing.** MySQL is accidentally the *safe* dialect: its commit
  fails before `Activate` is ever reached. Postgres and SQLite commit the zero row and then
  hang the process.
- `Monthly(12,[31])` in February is genuinely never-due, not an artifact of the
  `366*5*interval` scan bound — audit-verified across 20 re-anchors spanning 1,000 years.

## 5. Reachability

- **Ordinary process data.** `engine/trigger_resolve.go:14-27` turns `EveryExpr(code)` into
  `schedule.Every(d)` with no positivity check, so a process variable of `0` produces a
  never-due arm. (`AfterExpr → 0` is safe: `After(0)` reports ok=true and fires immediately.)
- **A legal definition.** Nothing rejects `Monthly(12,[31])` or `Cron("0 0 30 2 *")`.
- **Three arm paths, not one.** Beyond the eight `ScheduleTimer{}` sites in `engine/` (six
  gated by `ResolveTrigger`, two building always-schedulable `AfterDuration` — all
  audit-re-derived as exact), there is a third path the first design missed entirely:
  `RehydrateStartTimers` → `armStartTimer` → `scheduleStartTimerJob` arms `StartEvent.Timer`
  **raw**. And **rehydration** re-arms from the *persisted* `next_run`
  (`rehydrateTrigger` returns recurring triggers verbatim, and every never-due kind is
  recurring), so a legacy zero row reaches `Activate` at boot.
- **Nothing reclaims the orphan rows.** Audit-measured: `PruneTimers` deleted **1 of 5** — only
  the non-recurring control. `Stats.NextFireAt` stayed poisoned. An earlier draft claimed
  "only `Pruner.PruneTimers` reclaims it"; in fact *nothing* does.

## 6. Design

**Invariant: the runtime never persists or activates a timer the scheduler cannot run.**

Stated deliberately narrowly. An earlier draft claimed "no timer is ever armed that can never
fire", which the audit falsified via the raw start-timer path.

### 6.1 First, make `Next` agree with the scheduler (ADR-0140's own contract)

Order matters: **this must land before the guard**, or the guard regresses the four working
cases in §3.

`calendarNext` and the cron path are corrected to model what gocron actually does:

- an empty weekday set and an empty day-of-month set take the substituted defaults
  rather than reporting never-due;
- an out-of-range weekday resolves as gocron resolves it;
- a **negative** day-of-month means counting back from month end (`-1` = last day);
- a cron whose next occurrence cannot be found reports `!ok` instead of `(zero, true)`.

⚠ The exact substitution gocron applies is **not** frozen in this spec. §3 establishes *that*
these four arm; the precise instants are Phase 1's job to derive by probing `Activate` and
then to encode. Freezing guessed values here is how the previous design failed.

**Derived and built** (measurements §14, §15.4). The empty-set defaults turned out to be *ours* —
`scheduler/internal/gocron/trigger.go` substitutes `time.Sunday` and `[]int{1}` before gocron
sees the job — so `Next` mirrors that file, not gocron's own behaviour for an empty set (which is
to return the zero time). An out-of-range weekday is **not** normalised into the week: gocron's
guard `wd >= lastRun.Weekday()` is always true for `wd >= 7`, so it resolves on gocron's first
pass at `anchorDay + (wd - anchorWeekday)`, which means it **ignores the interval** and **beats
an in-range weekday the anchor has already passed** — measured, both. A weekday-set membership
scan cannot express that, so the weekly branch is now a transcription of gocron's
`weeklyJob.next`; `Daily` and `Monthly` keep the bounded scan (which is what still reports `!ok`
for `Monthly(12,[31])`). Side effect: the weekly kind no longer scans, so the unbounded-`interval`
scan cost is now a `Daily`/`Monthly`-only concern.

`Monthly(12,[31])`-style specs continue to report `!ok` — that is correct and load-bearing:
it is what lets the guard refuse them before the livelock.

### 6.2 Then guard the three arm paths on `!ok || next.IsZero()`

One predicate (`neverDueNextRun`), applied where a `next_run` is computed or read back:

| site | why |
|---|---|
| `timerJobsFor` (`runtime/timerops.go`) | the in-tx write and the post-commit `Activate` both flow from here — refusing produces no row **and** no `Activate`, so the livelock becomes unreachable |
| `armStartTimer` / `scheduleStartTimerJob` | the raw start-timer path |
| `jobStore.Load` (rehydration) | a persisted row must not reach `Activate` at boot and wedge the process |

⚠ **As built, all three justifications were corrected by measurement** — see §15 of the
measurement record, and the ADR's Decision 2, which carries the corrections:

- the `IsZero()` half no longer closes the 30-February cron (§6.1 lands first and makes it
  report `!ok`), so it is kept as blocker 2's invariant and a regression guard, pinned by a
  predicate test because no trigger shape reaches it;
- the start-timer path is **not** "ungated today" — both shipped `Scheduler` implementations
  refuse an `!ok` trigger in their own `Schedule`; the guard exists because the runtime consumes
  the **port**, and a consumer-supplied scheduler was measured arming a never-due job with a zero
  next run and a nil error;
- the rehydration guard keys on the **re-derived** arm instant, not on a zero stored `next_run`:
  a row with a valid stored instant still wedges a February boot, while a stored-zero row whose
  trigger still fires is re-armed and heals.

Refusal is a WARN log naming timer id, instance id and kind, plus no arm and no row — matching
the unconvertible-trigger branch that already exists in `timerJobsFor`.

⚠ **Known limitation, stated because the audit found it:** on the success path a refused arm
leaves the engine's in-memory `timerRecord` in place with no durable row, so
`HasArmedTimers()` over-reports and the parked token is *less* diagnosable than a poisoned
row was. Making that visible needs an incident channel that does not exist post-step; it is
filed, not solved.

### 6.3 What is NOT in this delivery

Deferred to their own ADR, each because the audit found a concrete defect in the first design:

- **A deploy-time `model.Validate` gate.** Its prescribed mechanism was an **import cycle** —
  `definition/event` imports `definition/model`, so `model` cannot see `event.*.Timer`, and no
  accessor exposes them. A mechanism exists (the wire form, `NodeWire.TimerTrigger` via
  `toWire`), and it must live in `validateStructure`, not `Validate`, or every nested
  sub-process is exempt. Also: `Build()` **already** rejects every recurring `DeadlineTimer`
  (`ErrDeadlineTriggerRecurring`), and `Recurring()` is true for `KindEveryExpr`, which made
  one prescribed test unconstructable and that gate partly dead code.
- **A step-time gate in the engine.** Audit-measured: it **wedges a running instance** — the
  step rolls back with the already-fired timer row restored at a past `next_run` and fails
  identically forever, which is worse than the inert row it replaces.
- **`StepOptions.SchedulingLocation`.** Only needed by a step-time gate. It also does not
  eliminate both error directions (nil→UTC gives a direct-`engine.Step` consumer exactly the
  false rejection it exists to prevent), and cron-under-`FixedZone` is unfixable by any
  location (ADR-0136).
- **Moving the calendar math into a shared package.** Unnecessary once the guard lives in
  `runtime`, which already imports both packages — and it was **forbidden** by
  `scheduler/selfcontainment_guard_test.go`, which bans any `wrkflw/*` import outside
  `scheduler/...` from scheduler production code (two lenses proved it by mutation).
  `scheduler/internal/schedcalc` is not an escape: Go's internal rule would bar
  `definition/schedule` from reaching it.
- **A new `ErrTriggerNeverDue` sentinel.** It already exists in substance:
  `scheduler/scheduler.go:575-578` refuses a never-due trigger with *"trigger can never
  fire"* wrapping `ErrUnsupportedTrigger`. The real defect is that `Activate` bypasses that
  gate while `Schedule` honours it.
- **Migrating existing zero-`next_run` rows.** §6.2's rehydration guard stops them wedging
  boot, but nothing deletes them. Manual remediation is documented; an opt-in admin sweep is
  the plausible shape.

## 7. Consequences

**Fixed:** blocker 2 on all three dialects; a whole-process livelock reachable from a legal
definition; `Next`'s divergence from the scheduler (ADR-0140's contract); a legacy zero row
wedging boot.

**Behaviour changes:** four calendar shapes that report `!ok` today begin reporting a real
instant (§3) — this is a *correction*, and they already armed and fired, so no working
definition changes behaviour. A never-due arm is now skipped with a WARN instead of writing a
zero row or hanging.

**Costs accepted:** the `HasArmedTimers()` over-report of §6.2; orphan rows are not migrated.

**Opened:** the deferred gates of §6.3; `EveryRandom(min>max)` is an uncovered silent
forever-wait with a *non-zero* `next_run`; the calendar scan is unbounded in a
consumer-supplied `interval` (~102 h projected at `MaxUint32`); `runtime/timerjob.go:66-69`'s
comment carries three false claims (fixed in-bundle); the definition store round-trips
semantically invalid definitions.

## 8. What the audit refuted, and why it is recorded

Eight Criticals. The three that changed the design:

1. **`Trigger.Next` was treated as the arming authority.** It is not (§3). The first design
   would have rejected four definitions that work today. **Lesson: "executed" is not enough
   if the wrong call path was executed** — the probe ran `Next`, while the thing that arms is
   `Activate`.
2. **The design did not close blocker 2.** The parseable-never-matching cron reports `ok=true`
   with a zero instant, so every proposed gate missed it (§3).
3. **A livelock outranked the blocker** (§4), and it inverted which dialect is dangerous.

Also refuted: that the arm gate was "defence in depth" (it is the only layer that can catch
the anchor-dependent class); that a deploy-time gate was reachable as described (import
cycle); that `PruneTimers` reclaims the rows (it does not); that MySQL's rejection was a range
floor (it is the zero literal); and the "never due **iff**" table header, which contradicted
the anchor-dependence section two pages later.

What survived: all five enumerations exact (eight `ScheduleTimer{}` sites, six gated, five
spec-bearing fields, one `model.Validate` caller, four `calendarNext` false-branches); every
line citation exact; the three-dialect table character-for-character; `Monthly(12,[31])` in
February genuinely never-due across 1,000 years of re-anchoring; `StepOptions` additivity; and
the scan cost *not* being a new hot-path problem (valid triggers benchmark at 220 ns–1.4 µs).

## 9. Scratch: probe commands

All probes were throwaway files, run then deleted; the tree is clean. Raw captured output is
in-repo: [`2026-08-13-adr-0176-measurements.md`](2026-08-13-adr-0176-measurements.md), 13
sections. ⚠ Its §1–§10 record the FIRST design's premises; **§13 is the measurement that
refuted them** and wins wherever they conflict.

```
go test -count=1 -v -run TestZZZProbeNextOK ./scheduler/                                  → EXIT=0  (§3 Next column)
go test -count=1 -v -run TestZZZProbeArmingTruthTable ./scheduler/                        → EXIT=0  (§3 Activate column)
go test -count=1 -v -timeout 40s -run TestZZZProbeActivateLivelock ./scheduler/           → EXIT=0  (§4)
go test -count=1 -v -run 'TestZZZProbeZeroNextRun/{sqlite,mysql,postgres}' ./internal/persistence/store/ → EXIT=0 (§2)
go test -count=1 -v -run TestZZZProbeStoredDefinitionNotRevalidated ./internal/persistence/store/        → EXIT=0
```

## 10. Open questions

None. Owner decisions: 2026-08-13 (six design forks, all superseded by the re-scope below),
then post-audit — **split the delivery**, fixing the livelock and the arm boundary here and
deferring the build/step gates and `SchedulingLocation` to their own ADR; deliver normally on
the branch rather than as a hotfix.

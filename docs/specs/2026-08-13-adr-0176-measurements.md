# ADR-0176 — the raw measurement record (2026-08-13, base `main` @ `e04bd670`)

Evidence base for [`2026-08-13-never-due-timer-triggers.md`](2026-08-13-never-due-timer-triggers.md)
and [`../adr/0176-reject-never-due-timer-triggers.md`](../adr/0176-reject-never-due-timer-triggers.md).
Kept in-repo deliberately: the previous delivery lost its auditor write-ups to a temporary
directory and had to repair dangling citations afterwards.

All numbers OBSERVED, not reasoned. Every probe was a throwaway test file, run and then
deleted; the tree is clean. Sections are in the order they were measured, so §1–§10 record the
FIRST design's premises and **§13 is the measurement that refuted it** — `Trigger.Next` is not
the arming authority. Read §13 first; where §1–§10 conflict with it, §13 wins.

⚠ §11 documents a whole-process livelock in shipped code, and §12 that the "new" never-due
sentinel already existed.

## 1. Which triggers report `Next(now) == ok:false`

`go test -count=1 -v -run TestZZZProbeNextOK ./scheduler/` → EXIT=0

| trigger | ok | next |
|---|---|---|
| `Trigger{}` (zero) | false | 0001-01-01T00:00:00Z |
| `At(time.Time{})` | false | 0001-01-01T00:00:00Z |
| `After(0)` | **true** | 2026-08-12T10:00:00Z |
| `Every(0)` | false | zero |
| `Every(-1s)` | false | zero |
| `EveryRandom(0, 1m)` | false | zero |
| `Cron("not-a-cron")` | false | zero |
| `Cron("")` | false | zero |
| `Daily(0)` | false | zero |
| `Weekly(0, nil)` | false | zero |
| `Monthly(0, nil)` | false | zero |
| **`Weekly(1, nil)`** | **false** | zero |
| **`Monthly(1, nil)`** | **false** | zero |

⚠ FINDING A (doc rot in shipped code): `scheduler/trigger.go:170-175`'s doc says ok=false
happens "exactly when … the zero Trigger, an At built from the zero time, a Cron
expression that failed to parse, or a recurring interval that is non-positive."
`Weekly(1, nil)` and `Monthly(1, nil)` — a VALID positive interval with an empty
day list — also return false. The enumeration is incomplete.

## 2. What the runtime does with it

`runtime/timerops.go:156-160`:
```go
var nextRun time.Time
if next, ok := strig.Next(now); ok {
    nextRun = next.UTC()
}
arms = append(arms, driver.newTimerJob(..., nextRun, cmd.Kind))
```
ok=false ⇒ `nextRun` stays the ZERO time and the arm is appended anyway. Contrast the
branch 11 lines above: an unconvertible trigger (KindUnset/Expr) is WARN-logged and
`continue`d — skipped entirely. There is no such guard for "convertible but never due".

## 3. Reachability

- Static definition: `schedule.Every(0)`, `schedule.Cron("bogus")`, `Weekly(1,nil)`, …
  No validation rejects these (no `Validate` in `definition/schedule`; grep of
  `definition/model/validate.go` shows no cron-parse or positive-interval check).
- ⚠ **Data-driven**: `engine/trigger_resolve.go:23-25` — `EveryExpr(code)` evaluates
  `code` against process-instance variables via `EvalDuration` and returns
  `schedule.Every(d)` with NO positivity check. A process variable of 0 (or a
  negative) therefore produces an unschedulable arm from ordinary runtime DATA.
- `AfterExpr` resolving to 0 is SAFE: `AfterDuration(0)` → `Duration()==(0,true)` →
  `scheduler.After(0)` → ok=true, nextRun=now (measured above).
- MEASURED: `At(zero)` does NOT reach the zero-nextRun path through the runtime.
  `schedule.At(time.Time{})` has `AbsTime()==(_,false)` and `Duration()==(0,true)`
  (measured), so `convertTrigger` yields `After(0)`, which is ok=true.

## 4. Store behaviour, all three dialects

`go test -count=1 -v -run 'TestZZZProbeZeroNextRun/<dialect>' ./internal/persistence/store/`
→ EXIT=0 for each.

| dialect | `UpsertJob` with zero `NextRun` | reads back | `Stats.NextFireAt` |
|---|---|---|---|
| postgres | **accepted** | `0001-01-01T00:00:00Z` | `0001-01-01T00:00:00Z` |
| sqlite | **accepted** | `0001-01-01T00:00:00Z` | `0001-01-01T00:00:00Z` |
| mysql | **REJECTED** | — | — |

MySQL's exact error:
```
workflow-store: upsert timer "probe-inst"/"probe-timer": Error 1292 (22007):
Incorrect datetime value: '0000-00-00' for column 'next_run' at row 1
```
Note the wire value is `'0000-00-00'`, not `0001-01-01` — the driver renders Go's
zero time that way and strict mode rejects it. `DATETIME(6) NOT NULL`, MySQL's
DATETIME range starts at 1000-01-01.

⚠ FINDING B: on Postgres/SQLite the row is not merely tolerated — it sorts FIRST in
the `(next_run, instance_id, timer_id)` keyset index and becomes `Stats.NextFireAt`
forever, so the operator dashboard reports the next timer fire as year 1.

## 5. The MySQL consequence, end to end

The arm is written INSIDE the state-commit transaction — `runtime/processdriver.go:730-757`
(`commitFn`: `store.Commit` then `jobStore.Save` per arm; a `Save` error returns from
`commitFn`), and `:758-767` runs it under `RunInTx` and wraps any error as
`workflow-runtime: commit: %w`.

The rollback consequence is already covered by an EXISTING passing test,
`assertSameTxAtomicity` (`runtime/timer_txflow_test.go:189-247`), which injects an
`UpsertJob` failure and asserts: `Drive` returns the error, the instance version does
NOT advance, and the pre-step timer rows are restored unchanged.

⇒ Chain: an unschedulable trigger on MySQL ⇒ 1292 on the arm ⇒ `commitFn` fails ⇒ the
whole step rolls back ⇒ **the instance can never advance past that node**. Both ends
are executed; the middle is the two code sites above.

## 6. Blast radius asymmetry (the design problem)

- MySQL: LOUD and TOTAL — the step is permanently unexecutable. Retry cannot help;
  the trigger is deterministic.
- Postgres/SQLite: SILENT — the row persists, `Activate` failure post-commit is
  WARN-only and deliberately benign (`processdriver.go:773-788`), so the timer never
  fires, the instance waits forever, and the row lingers across every reboot
  (only `Pruner.PruneTimers` reclaims it) while poisoning `Stats.NextFireAt`.

So the same definition is a hard failure on one supported dialect and an invisible
hang on the other two. Any fix has to pick ONE behaviour for all three.

## 7. Does a deploy-time Validate rule brick STORED definitions? NO — measured

`go test -count=1 -v -run TestZZZProbeStoredDefinitionNotRevalidated ./internal/persistence/store/` → EXIT=0

```
PROBE model.Validate(crafted def) err=workflow-definition: flow references unknown node: flow "f1" target "nope"
PROBE PutDefinition err=<nil>
PROBE GetDefinition err=<nil> loaded=true
```

A definition that `model.Validate` REJECTS today round-trips through
`PutDefinition` → `GetDefinition` with no error. Structurally:
`PutDefinition` (`definitions.go:92`) is `json.Marshal` + INSERT;
`GetDefinition` (`definitions.go:114`) is SELECT + `json.Unmarshal`. Neither calls
`model.Validate`. `model.Validate` has exactly ONE non-test caller —
`definition/model/builder.go:133` (`Build()`), reached by the code builder and by
`ParseYAML(...).Build()`.

⇒ A new rule in `model.Validate` gates only NEWLY BUILT definitions. It cannot make
an existing stored row unloadable. This CORRECTS the migration hazard asserted while
scoping this delivery (deploy-time rejection was described as a breaking load change
"like ADR-0167" — ADR-0167 changed DECODING, which IS on the read path; Validate is
not). The residual: a stored row carrying an unschedulable trigger keeps loading and
is caught later by the step-time gate.

⚠ FINDING C (pre-existing, backlog): the definition store round-trips SEMANTICALLY
INVALID definitions. `json.Unmarshal` is strict about SHAPE (ADR-0167 —
`json: unknown field "from"` was observed while writing this probe) but no layer
re-checks `model.Validate` invariants on read.

## 8. The complete never-due set, re-derived from source + probed

`calendarNext` (`scheduler/trigger.go:316-400`) has FOUR false-returning branches, not
the two an obvious reading suggests:
1. `interval == 0` (:317)
2. `triggerWeekly` with an empty weekday SET (:345)
3. `triggerMonthly` with an empty day SET (:356)
4. **the bounded forward scan exhausting** (:365-400, bound = `366*5*interval` days)

Probed (`TestZZZProbeCalendarExhaustion`, EXIT=0):

| trigger | ok |
|---|---|
| `Monthly(1,[0])` / `[32]` / `[-1]` | false — class 4 |
| `Monthly(1,[31])` / `[29]` | true |
| `Weekly(1,[Weekday(9)])` / `[Weekday(-1)]` | false — class 4 |
| `Weekly(1,[Monday])` | true |
| `Daily(1,{Hour:25})` | **true** — `time.Date` NORMALISES to next day 01:00 |

⇒ out-of-range CLOCK times are NOT never-due; out-of-range day/weekday values ARE.

## 9. ⚠⚠ Schedulability is ANCHOR-DEPENDENT (falsifies a design claim)

`TestZZZProbeAnchorDependence`, EXIT=0:
```
PROBE Monthly(12,[31]) anchor=2026-02 ok=false next=0001-01-01T00:00:00Z
PROBE Monthly(12,[31]) anchor=2026-04 ok=false next=0001-01-01T00:00:00Z
PROBE Monthly(12,[31]) anchor=2026-08 ok=true  next=2026-08-31T00:00:00Z
PROBE Weekly(3,[Mon])  anchor=Monday   ok=true next=2026-08-31T00:00:00Z
PROBE Weekly(3,[Mon])  anchor=Tuesday  ok=true next=2026-08-31T00:00:00Z
```
The SAME spec is never-due at one anchor and schedulable at another: with
`interval == 12` only months congruent to the anchor month qualify, and February
never has a 31st, so the 60-year scan exhausts.

Consequences:
- A static `Validate()` can only be SOUND, never complete:
  `Validate() != nil ⟹ ∀t: Next(t) is !ok`. The CONVERSE IS FALSE.
- The "equivalence test" (`Validate()==nil ⟺ Next ok`) proposed while designing is
  WRONG as stated — it must be the one-directional implication, or it fails on
  `Monthly(12,[31])`.
- ⚠ The arm-time guard is NOT defence-in-depth: it is the ONLY layer that can catch
  the anchor-dependent class, and that class is reachable from a definition NOTHING
  rejects today (no bad process data required).

## 10. ScheduleTimer sites — counted, not assumed

EIGHT non-test `ScheduleTimer{}` constructions, all in `engine/` (grep over the whole
repo finds none elsewhere):

| site | via ResolveTrigger? | trigger built |
|---|---|---|
| `step_boundaries.go:54` | yes (:47) | resolved node spec |
| `step_eventsubprocess.go:97` | yes (:91) | resolved node spec |
| `step_nodes.go:699` (reminder) | yes (:691) | resolved node spec |
| `step_nodes.go:772` (deadline) | yes (:766) | resolved node spec |
| `step_nodes.go:831` (intermediate catch) | yes (:825) | resolved node spec |
| `step_nodes.go:988` (event gateway) | yes (:982) | resolved node spec |
| `step_compensation.go:493` | **NO** | `schedule.AfterDuration(pol.stallAfter)` |
| `step_triggers.go:328` | **NO** | `schedule.AfterDuration(delay)` |

Both bypass sites use `AfterDuration`, which is `KindOneTime` ⇒ `convertTrigger` yields
`scheduler.After(d)` ⇒ `Next` is ALWAYS ok (measured: `After(0)` → ok=true; a negative d
yields a past instant, non-zero, which MySQL accepts). So neither bypass site can
produce a zero `next_run` — the six gated sites plus the anchor-dependent class are the
whole exposure.

## 11. ⚠⚠⚠ CONFIRMED LIVELOCK in shipped code (lens C's C2, independently reproduced)

`scheduler.NativeScheduler.Activate` NEVER RETURNS for `Monthly(12,[31])` when the
scheduler's clock sits in a month with no 31st. My own probe, `./scheduler/`, EXIT=0:

```
clock = clockwork.NewFakeClockAt(2026-02-10)  → PROBE Activate DID NOT RETURN within 10s
clock = real (2026-08-13, August HAS a 31st)  → PROBE Activate RETURNED err=<nil>
```

⚠ **NEW PRECISION beyond lens C: the reproduction is CLOCK-MONTH DEPENDENT.** It is not
reachable at all in a month that has a 31st, which is why the first probe attempt (real
clock, August) reported "NO livelock" and would have been read as a refutation. For
`Monthly(12,[31])` only months congruent to the anchor month mod 12 qualify, so the wedge
window is the 5 months without a 31st (Feb, Apr, Jun, Sep, Nov) — consistent with lens C's
independently measured "5 of 12".

Mechanism per lens C (captured stack): gocron v2.22.0's `monthlyJob.next` spins forever, and
it does so inside gocron's single `selectNewJob` goroutine, so EVERY job's arm/cancel/
rehydrate in the process blocks. gocron v2.22.0 is a HARD PIN (ADR-0135), so the fix cannot
be "upgrade gocron" — it must be "refuse before calling Activate".

⇒ This is a **whole-process availability defect reachable from a legal definition**, not the
single-instance hang §1.2 describes. It also INVERTS the dialect framing: MySQL is
accidentally the SAFE dialect, because its commit fails BEFORE `Activate` is reached.

## 12. ⚠⚠ The never-due sentinel ALREADY EXISTS (lens C's C4, confirmed by reading)

`scheduler/scheduler.go:575-578`, inside `Schedule`:

```go
next, ok := j.Trigger().Next(s.now().In(s.location()))
if !ok {
    return nil, fmt.Errorf("workflow-scheduler: job %q trigger can never fire: %w", j.ID(), ErrUnsupportedTrigger)
}
```

So ADR-0176's proposed `ErrTriggerNeverDue` REINVENTS a shipped gate with the same wording.
The actual defect is that ADR-0134's durable/manual arm path (`Activate`) BYPASSES this
gate, while `Schedule` honours it. That is a far smaller fix than moving the calendar math.

## 13. ⭐ THE REAL ARMING TRUTH TABLE (measured against live gocron via Activate)

Clock pinned to 2026-02-10 (a month with no 31st). `./scheduler/`, EXIT=0.

| trigger | Next.ok | Next zero | live Activate |
|---|---|---|---|
| `Every(0)` | false | true | error `DurationJob: time interval is 0` |
| `Cron("not-a-cron")` | false | true | error `CronJob: crontab parse failure` |
| **`Cron("0 0 30 2 *")`** | **TRUE** | **TRUE** | error `CronJob: invalid crontab` |
| **`Weekly(1,nil)`** | false | true | **nil — ARMS** |
| **`Monthly(1,nil)`** | false | true | **nil — ARMS** |
| **`Weekly(1,[Weekday(9)])`** | false | true | **nil — ARMS** |
| `Monthly(1,[0])` | false | true | error `daysOfTheMonth must be between 31 and -31 inclusive, and not 0` |
| `Monthly(1,[32])` | false | true | error (same) |
| **`Monthly(1,[-1])`** | false | true | **nil — ARMS** |
| **`Monthly(12,[31])`** | false | true | ***LIVELOCK*** |
| `Daily(0)` | false | true | error `DailyJob: interval must be greater than 0` |
| `Weekly(0,[Mon])` | false | true | error `WeeklyJob: interval must be greater than 0` |

`Trigger.Next` disagrees with the live scheduler in BOTH directions:

- **FOUR specs arm and fire while `Next` says !ok** — `Weekly(1,nil)`, `Monthly(1,nil)`,
  `Weekly(1,[Weekday(9)])`, `Monthly(1,[-1])`. Lens B found three; the fourth is different in
  kind: gocron treats a NEGATIVE day-of-month as counting back from month end (`-1` = last
  day), a legitimate feature `calendarNext` does not model at all. ⇒ a naive `!ok` arm guard
  would REGRESS four working cases.
- **ONE spec reports ok=true with a ZERO instant** — `Cron("0 0 30 2 *")`. robfig/cron gives up
  after 5 years and returns the zero time with no error, so the runtime persists a zero
  `next_run` IN-TX (MySQL: step lost; PG/SQLite: poisoned row) and only then does `Activate`
  fail — post-commit, where failure is deliberately WARN-only and benign. ⇒ `!ok` alone
  cannot close blocker 2; the guard needs `|| next.IsZero()`.
- **ONE spec livelocks** — confirmed a third time.

⇒ The delivery must FIRST make `calendarNext` agree with gocron (ADR-0140's own contract),
THEN guard on `!ok || next.IsZero()`. In that order, or the guard breaks working definitions.

## 14. ⭐ PHASE 1 STEP 0 — the instants gocron actually arms (implementation input)

Plan Phase 1 Step 0 required deriving the substituted instants rather than guessing them (the
first design failed by guessing). Probe: a throwaway `scheduler/zzz_probe_activate_test.go`
arming each shape through the **live** `NativeScheduler.Activate` with `clockwork.NewFakeClockAt`,
then reading the engine's own next-run back through `NativeScheduler.Scheduled`. Five anchors,
`go test -count=1 -v -timeout 900s -run TestZZZProbeArmedInstants ./scheduler/` → **EXIT=0**.
Probe deleted afterwards; the numbers are kept here.

Anchors: `A1 = Tue 2026-02-10 09:30Z` (no 31st) · `A2 = Thu 2026-08-13 09:30Z` (has a 31st) ·
`A3 = Sat 2026-01-31 09:30Z` (last day of month) · `A4 = Thu 2026-04-30 23:00Z` (last day, late
hour) · `A5 = Sun 2026-11-01 00:00Z` (midnight exactly).

| trigger | A1 | A2 | A3 | A4 | A5 |
|---|---|---|---|---|---|
| `Weekly(1,nil)` | 02-15 | 08-16 | 02-01 | 05-03 | 11-08 |
| `Weekly(1,[])` | 02-15 | 08-16 | 02-01 | 05-03 | 11-08 |
| `Weekly(1,[Weekday(9)])` | 02-17 | 08-18 | 02-03 | 05-05 | 11-10 |
| `Weekly(1,[Weekday(8)])` | 02-16 | 08-17 | 02-02 | 05-04 | 11-09 |
| `Weekly(2,nil)` | 02-22 | 08-23 | 02-08 | 05-10 | 11-15 |
| `Weekly(1,[Mon])` *control* | 02-16 | 08-17 | 02-02 | 05-04 | 11-02 |
| `Monthly(1,nil)` | 03-01 | 09-01 | 02-01 | 05-01 | 12-01 |
| `Monthly(1,[-1])` | 02-28 | 08-31 | 02-28 | 05-31 | 11-30 |
| `Monthly(1,[-2])` | 02-27 | 08-30 | 02-27 | 05-30 | 11-29 |
| `Monthly(2,[-1])` | 02-28 | 08-31 | 03-31 | 06-30 | 11-30 |
| `Monthly(1,[15])` *control* | 02-15 | 08-15 | 02-15 | 05-15 | 11-15 |
| `Monthly(1,[31])` | 03-31 | 08-31 | 03-31 | 05-31 | 12-31 |
| `Daily(1)` *control* | 02-11 | 08-14 | 02-01 | 05-01 | 11-02 |

All at `00:00:00Z` (no at-times given). `Trigger.Next` at the same five anchors was captured in
the same run (`TestZZZProbeNextToday`): every `Weekly(…,nil)`, `Weekly(…,[Weekday(8|9)])`,
`Monthly(…,nil)` and `Monthly(…,[-1|-2])` row reports **`ok=false`, zero** — i.e. the whole
left column of the table above is a divergence — while the three controls, `Monthly(1,[31])`
and `Cron("0 0 30 2 *")` (`ok=true`, **zero instant**) reproduce §13 exactly.

### 14.1 The mechanisms, source-verified — not inferred from the outputs

- **The empty-set substitution is OURS, not gocron's.** `scheduler/internal/gocron/trigger.go:207-220`
  substitutes `[]time.Weekday{time.Sunday}` for an empty weekday set and `[]int{1}` for an empty
  day-of-month set before calling `gocron.WeeklyJob`/`MonthlyJob`. gocron itself would return the
  zero time for an empty set. ⚠ This matters: the substitution is a property of this repo's
  adapter, so `Trigger.Next` must mirror *that file*, not gocron's own defaults.
- **Out-of-range weekday** — gocron v2.22.0 `job.go:1412-1447` (`weeklyJob.nextWeekDayAtTime`):
  the guard is `wd >= lastRun.Weekday()` and the candidate is
  `time.Date(y, m, lastRun.Day()+int(wd-lastRun.Weekday()), …)`, relying on Go's date
  normalisation. For `wd >= 7` the guard is **always** true and the offset always positive, so an
  out-of-range weekday **always resolves on the first pass**, landing `wd - anchorWeekday` days
  after the anchor *regardless of interval*. That is why `Weekday(8)` (Mondays) gives 11-09 from
  A5 where a plain `Monday` gives 11-02: `8-0=8` days versus `1-0=1`.
- **Negative day-of-month** — `job.go:1479-1491` (`handleNegativeDays`):
  `Date(y, m+1, 1).AddDate(0,0,neg).Day()`, i.e. `daysInMonth(m) + 1 + neg`; `-1` = last day.
  Recomputed per candidate month, which is why `Monthly(1,[-1])` gives 02-28 at A1 but 08-31 at A2.
- **The livelock** — `job.go:1469-1474` (`monthlyJob.next`): `for next.IsZero() { … }` with no
  bound. A day-of-month that no qualifying month contains spins forever. Confirms §11's
  stack-derived mechanism from the source itself.

### 14.2 What this fixes for Phase 1

`calendarNext`'s bounded day-by-day scan already reproduces gocron for every in-range weekday and
positive day-of-month at every interval (all five controls, plus `Monthly(1,[31])` and
`Monthly(2,[-1])` once negatives are mapped — hand-checked against the table above). So:

- **empty sets** → substitute `{Sunday}` / `{1}` and the existing scan produces the measured
  column (`Weekly(2,nil)` → A1 02-22 falls out of the ADR-0140 week-index filter unchanged);
- **negative days** → map per candidate month inside the scan;
- **out-of-range weekdays** → the scan's `weekdaySet[day.Weekday()]` membership test cannot model
  them, because they are an *offset from the anchor* that deliberately escapes the interval grid.
  They need gocron's first-pass rule transcribed.

## 15. What IMPLEMENTATION refuted (2026-08-13, same branch)

Rule #11: some consequences are only visible once the change exists. Four of this bundle's
claims were corrected by building it; each correction is folded back into the ADR and spec.

### 15.1 ⚠ "The raw start-timer path is ungated today" — FALSE as written

Probe: `driver.scheduleStartTimerJob` called directly with each never-due spec, against two
`scheduler.Scheduler` implementations, clock at `2026-02-10`.

| trigger | live `NativeScheduler` | a consumer-supplied ungating Scheduler |
|---|---|---|
| `Every(0)` | **refused** — `job "t1" trigger can never fire: unsupported trigger` | armed, `nextRun` **zero**, err `nil` |
| `Monthly(12,[31])` | **refused** (same error) — never reaches `Activate`, so **no livelock** | armed, `nextRun` **zero**, err `nil` |
| `Cron("0 0 30 2 *")` | **refused** (same error, post-P1) | armed, `nextRun` **zero**, err `nil` |
| `Weekly(1,nil)` | armed, `nextRun` `2026-02-15` | armed, `nextRun` `2026-02-15` |

`NativeScheduler.Schedule`'s pre-existing never-due gate (`scheduler.go:575-578`) already covers
this site, and `processtest.MemScheduler.resolve` gates it too. The claim is true only for a
consumer's own implementation of the port — which is the justification the guard actually has,
and the shape the RED test must use. Written as "ungated", the prescribed test would have been
**vacuous**.

### 15.2 ⚠ The `IsZero()` half no longer closes the 30-February cron

The ADR justified `|| next.IsZero()` as the half that catches `Cron("0 0 30 2 *")`, which `!ok`
could not see. But P1 lands first and makes that cron report `!ok` — so the `!ok` half closes it,
and after P1 **no trigger shape reaches the `IsZero()` half at all**. It is kept as the direct
statement of blocker 2's invariant and as a regression guard on P1, and is pinned by a direct
predicate test (`TestNeverDueNextRun`) rather than through a trigger. Mutation confirms the
split: deleting `|| next.IsZero()` leaves `TestTimerJobsForRefusesANeverDueArm` **GREEN** and
turns only the predicate test RED.

### 15.3 ⚠ The rehydration wedge is not about a zero stored `next_run`

Rehydration re-arms from the TRIGGER (`rehydrateTrigger` returns recurring triggers verbatim,
and `newScheduledTimerJob` recomputes `Next(now)`); the stored `next_run` only rides along in the
descriptor. So the wedge condition is "the trigger is never-due **at boot**", and the stored
value is neither necessary nor sufficient. Measured on `jobStore.Load` at a `2026-02-10` clock:

| stored row | before the guard | after |
|---|---|---|
| `Monthly(12,[31])`, `next_run = 2026-08-31` (**valid**) | returned → `RehydrateTimers` **never returns** | skipped, WARN |
| `Every(0)`, `next_run` zero | returned | skipped, WARN |
| `Every(1h)`, `next_run` **zero** | returned, re-armed at a real instant | unchanged — the row **heals** |
| `Weekly(1,[Mon])`, valid | returned | unchanged |

A guard keyed on the stored zero would have missed row 1 (the one that actually wedges) and
stranded row 3 (which works). `RehydrateTimers` on row 1 was measured hanging for the full 10s
test timeout, and returning in 0.7s after.

### 15.4 ⚠ P1 needed gocron's weekly algorithm transcribed, not a normalisation

"Normalise an out-of-range weekday" understates it. gocron's guard `wd >= lastRun.Weekday()` is
always true for `wd >= 7`, so such a weekday resolves on gocron's FIRST pass — which means it
**ignores the interval entirely** and **beats an in-range weekday the anchor has already passed**,
even when that one would fire sooner. Both measured:

```
Weekly(1,[Wd(9)]) @ Tue 2026-02-10 → 2026-02-17   Weekly(2,[Wd(9)]) → 2026-02-17 (interval ignored)
Weekly(3,[Wd(9)]) @ same          → 2026-02-17    Weekly(1,[Wd(7)])  → 2026-02-15
Weekly(1,[Wd(13)]) @ same         → 2026-02-21    Weekly(1,[Sun,Wd(9)]) → 2026-02-17, NOT the
                                                   chronologically earlier Sunday 02-15
Weekly(1,[Wd(-1)]) @ same         → Activate returns nil, then the job is GONE (zero next run):
                                    it arms and silently never fires. Next reports !ok — kept.
```

A weekday-set membership scan cannot express "an offset that leaves the interval grid", so
`calendarNext`'s weekly branch was replaced by a transcription of `weeklyJob.next`. Consequence
the ADR did not name: a MIXED in-range/out-of-range weekday set also changes answer (it reported
the in-range instant before, now the gocron one). Side benefit: the weekly kind no longer scans
at all, so the unbounded-`interval` scan cost now applies only to `Daily` and `Monthly`.

### 15.5 Two existing tests encoded the divergence and were changed

`scheduler/trigger_test.go`'s "weekly with no weekdays reports no future fire" and "monthly with
no days-of-month reports no future fire" asserted `ok=false` for two shapes that arm and fire.
They are the declared behaviour change, now asserting the substituted Sunday / 1st. **No other
test in the repo moved** — the ADR-0140 interval tests, the location tests and the façade tests
all pass unchanged, which is the evidence that the weekly transcription is behaviour-preserving
for in-range weekday sets.

## 16. `/code-review` findings and what they changed (2026-08-13)

Five findings, **four fixed, one fixed-in-part with the remainder adjudicated**. The reviewer
differentially fuzzed `Trigger.Next` against a transcription of gocron v2.22.0's
`daily/weekly/monthlyJob.next` over ~30k anchor/shape combinations including `America/New_York`
DST and found **0 mismatches**, so the §15.4 weekly transcription and the negative-day handling
are confirmed faithful. All five findings were about shapes *outside* that reconciliation.

### 16.1 F1 (Major, FIXED) — eight shapes reported ok=true that gocron refuses at setup

The reconciliation covered the never-due classes but not the *invalid-argument* classes. Measured
against the repo's own `NativeScheduler.Schedule` at `2026-02-04T12:00Z`, **before** the fix:

| trigger | `Next` before | live scheduler |
|---|---|---|
| `Monthly(1,[15,32])`, `[15,0]`, `[15,-32]` | `2026-02-15`, ok=true | `ErrMonthlyJobDays` |
| `EveryRandom(2h,1h)`, `EveryRandom(1h,1h)` | ok=true | `ErrDurationRandomJobMinMax` |
| `Daily(1,{Hour:25})` | `2026-02-05T01:00`, ok=true | `ErrDailyJobHours` |
| `Weekly(1,[Mon],{Minute:99})` | `2026-02-09T01:39`, ok=true | `ErrWeeklyJobMinutesSeconds` |
| `Monthly(1,[15],{Hour:24})` / `{Second:60}` | ok=true | `ErrMonthlyJobHours` / `MinutesSeconds` |

This is blocker 2's failure mode without the zero literal: the arm is accepted, a **wrong**
`next_run` is written inside the commit transaction (`Hour:25` normalises to 01:00 the NEXT day,
poisoning `Stats.NextFireAt`), `Activate` then fails post-commit where failure is WARN-only, the
timer never fires, the token parks forever, and the row re-fails on every boot.

Two mechanisms confirmed in gocron's source, not inferred: it rejects the **whole monthly job** on
the first bad day (`job.go:522-524`, `day > 31 || day == 0 || day < -31`), and its at-time
validation is `hours > 23 || minutes > 59 || seconds > 59` (`util.go:94-98`). `EveryRandom`'s guard
is `min >= max` (`job.go:356`) — note **equal bounds are also rejected**.

Fixed by `clockTimesSchedulable` and `monthDaysSchedulable`, plus the `min >= max` branch.
**Re-measured after: all 12 shapes agree with live `Schedule`**, including the three boundary
controls (`Monthly(1,[31,-31])`, `EveryRandom(1h,2h)`, `Daily(1,{23,59,59})`) which still fire.
⚠ This also closes what the handover backlog carried as **item 25** (`EveryRandom(min>max)` as an
uncovered silent forever-wait), which no `next_run`-keyed guard could ever have caught because its
`next_run` is non-zero.

### 16.2 F2 (Major, FIXED — window narrowed, residual documented) — TOCTOU across the guard

`timerJobsFor` evaluates the guard at the step's clock reading, **before** the transaction;
`activateJob` (`scheduler/scheduler.go:637`) then **discards** `j.NextRun()` and lets gocron
re-derive from the trigger at its own later reading. A calendar trigger's answer is
anchor-dependent, so it can be armable at the first and never-due at the second. Measured:

```
Monthly(12,[31]).Next(2026-01-31T23:59:59Z) → 2027-01-31, ok=true
Monthly(12,[31]).Next(2026-02-01T00:00:01Z) → zero,       ok=false
```

Reproduced deterministically through the real driver by advancing a fake clock inside the commit
(`clockAdvancingStore`, which advances once right after the instance-state write): `Drive` hung for
its full 10 s timeout. A re-check on the instant computed *inside* the transaction, immediately
before `Activate`, makes it return.

⚠ **This narrows the window; it does not close it.** gocron reads the clock once more after our
check, so a residual of a few instructions remains — inherent while `Activate` re-derives rather
than using the instant it is handed. Recorded in the ADR's Consequences as an accepted residual.

### 16.3 F3 + F4 (Minor, FIXED for the metric; the error-propagation half ADJUDICATED)

A refused arm was a WARN line and nothing else — no counter, no event — so an instance parked on a
timer that will never fire was detectable only by grep. Added
`wrkflw_timer_arms_refused_total`, incremented at **all four** refusal sites (`timerJobsFor`,
`scheduleStartTimerJob`, `jobStore.Load`, and the new post-commit re-check).

⚠ **Adjudicated, not fixed:** F4 also proposed that a never-due skip join `jobStore.Load`'s
`unresolved` accounting so `RehydrateTimers` propagates it as an error. **Declined** — the sibling
branch's error makes *boot* fail, and the entire purpose of this guard is that a poisoned row does
not stop the process coming up. Failing boot on a row we just decided to skip would reintroduce the
outage in a politer form. The metric gives the aggregate signal without that cost.

⚠ **Process note:** the counter was written before its test, then verified retroactively by
mutation (delete the `Add`, observe RED, restore). That is the disclosed-lapse path in CLAUDE.md's
self-audit rule, not the intended cycle.

### 16.4 F5 (Minor, FIXED in part) — the monthly scan was linear in a consumer-supplied interval

The scan is bounded, but by `1830 × interval` DAYS, and `interval` is an unvalidated `uint` from
the definition. Only the *unsatisfiable* day case pays it (a satisfiable one returns in <3 µs).
Measured on this branch, `Monthly(interval,[31])` from a February anchor:

| interval | before | after |
|---|---|---|
| 12 | 0.93 ms | 0.17 ms |
| 1 200 | 65 ms | 6.1 ms |
| 12 000 | 633 ms | 45 ms |
| 120 000 | **6.34 s** | **392 ms** |

Fixed by skipping a whole off-grid month in one step instead of walking its days — the grid test is
per-month, so every day skipped would have been rejected anyway. **Proven semantics-preserving by
differential test against a verbatim copy of the pre-optimisation walk: 255,438 comparisons across
UTC, `America/New_York` (DST) and a +05:30 fixed zone, day lists including negatives and mixed
lists, multi-at-time sets, intervals 1–1200 → 0 mismatches.**

⚠ **Cost is still linear in `interval`, with a ~16× smaller constant** — the loop now visits every
month in range rather than every day, but does not jump straight to the next grid month. Going
further needs day-count arithmetic across DST inside the highest-risk function, for a definition
(`interval` in the tens of thousands of months) that is already absurd. Backlog item 26 stays open
with these numbers. `TestTrigger_NextMonthlyScanSkipsOffGridMonths` guards the day-walk from coming
back.

⚠ **That guard's first bound was itself wrong, and the full `-race` run caught it.** Tuned on the
plain measurement (3 s vs 0.39 s), it FAILED under `-race`, where everything is ~8x slower and
`Monthly(120000,…)` costs 3.17 s. Re-measured in the mode the bound actually has to survive:

| `Monthly(interval,[31])` | plain, fixed | plain, day-walk | `-race`, fixed | `-race`, day-walk |
|---|---|---|---|---|
| 1 200 | 6.1 ms | 65 ms | 32 ms | 530 ms |
| 12 000 | 45 ms | 633 ms | **0.32 s** | **5.31 s** |
| 120 000 | 392 ms | 6.34 s | 3.17 s | 53.4 s |

The test now uses interval 12 000 with a **2 s** bound — ~6x above the passing case and ~2.6x below
the failing one, in the slower of the two modes — and the failing case costs 5 s rather than 53 s.
Mutation-verified RED **under `-race`**, not just plain. *A timing bound measured in one mode is a
claim about that mode only.*

## 17. `/security-review` — 0 findings (2026-08-13)

No qualifying vulnerability. Nothing in the change reaches SQL, file paths, deserialization,
templating, crypto, secrets, network I/O, or the authorization layer.

What the review **verified rather than assumed**, and which is worth keeping:

- **A refusal cannot fail a security control open.** The two runtime-generated safety-relevant
  timer kinds — `TimerCompensationStall` (`engine/step_compensation.go:494`) and `TimerRetry`
  (`engine/step_triggers.go:331`) — both build `schedule.AfterDuration(...)`, whose `Next` is
  unconditionally `(after+dur, true)` with a non-zero instant, so `neverDueNextRun` **cannot fire
  on them**. The compensation-stall detector ADR-0175 shipped is untouched.
- **The two new predicates match gocron exactly**, re-verified independently against the pinned
  module: `util.go:94-98` (`hours > 23`, `minutes/seconds > 59`) and `job.go:522-524`
  (`day > 31 || day == 0 || day < -31`). So a refused definition timer is one that would never
  have fired anyway.
- **A changed `Next` value cannot shift an actual fire time.** Every kind whose answer moved is
  `Recurring()`, and `rehydrateTrigger` re-arms a recurring row **from the trigger**, not the
  stored `next_run`; only non-recurring rows re-arm via `schedule.At(NextRun)`, and `At`/`After`
  are unchanged. Residual `Next` inaccuracy is therefore bounded to the arm/refuse decision.
- **The month-skip is inert**, independently differential-tested over **356,400** combinations
  (3 zones incl. `America/Santiago`'s midnight DST transition, 9 day-sets, 11 intervals, 1,200
  consecutive anchors): **0 mismatches**. This is a second, independent confirmation of §16.4.
- **`int(interval)` overflow in `calendarNext`'s `bound` fails CLOSED** — a negative bound means
  the loop body never runs, so the answer is `ok=false` and the arm is refused.
- **No aliasing mutation of shared definition state**: `weeklyNext` and `sortedClockTimes` both
  `slices.Clone` before sorting. An in-place sort would have been a genuine data race, since
  `model.ProcessDefinition` is cached and shared across goroutines.
- The three new WARN sites emit only `timer_id`, `instance_id`, `timer_kind` and
  `stored_next_run` — identifiers, no PII, no credentials, no process variables.

⚠ **One correctness wart it surfaced while adjudicating it as NOT a vulnerability**, now backlog
item 30: `weeklyNext`'s `int(interval)*7` overflows at an interval ≥ ~1.3e18 and returns a PAST
instant with `ok=true` (measured: anchor 2026-08-13 → 2026-08-10). It crosses no trust boundary,
and gocron's own transcribed algorithm overflows identically so the two still agree — but it is a
real wart, and it pairs with §16.4's unbounded-`interval` cost. Both want `interval` bounded.

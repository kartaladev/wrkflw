# ADR-0176 bundle audit — Lens B (COUNTING LENS)

- Date: 2026-08-13
- Bundle audited: `docs/specs/2026-08-13-never-due-timer-triggers.md`,
  `docs/adr/0176-reject-never-due-timer-triggers.md`,
  `docs/plans/2026-08-13-never-due-timer-triggers.md`
- Worktree base: `e04bd670`. **Step 0: the bundle was ABSENT at base** and had to be
  obtained by fast-forwarding `feat/never-due-timer-triggers` (`726fe7ad`). The step-0
  instruction earned its keep for the fourth delivery running.
- Charter: re-derive every count, enumeration, quantifier and citation from source.
  Nothing in the documents is taken as true on its own authority.
- Probes: all throwaway, run, then deleted. `git status --short` at close shows only this
  file. `git diff --stat scheduler/` empty after the mutation probe was restored from a
  `cp` backup.

---

## Verdict in one line

The bundle's **counts are almost all correct** — 8 `ScheduleTimer` sites, 6 gated, 4
`calendarNext` branches, 5 spec fields, 1 `Validate` caller, 10 `Kind` values all check
out exactly, as do nearly every line citation. But **the set those counts describe is
wrong**: three of §2's "never-due" rows are not never-due in the live scheduler, and the
ADR's central refactor is forbidden by a test the plan promises not to touch. Two
Criticals, both invisible to a reader who trusts `Trigger.Next` as the oracle.

---

## FINDINGS (most severe first)

### C1 — CRITICAL. Three of §2's "never-due" rows DO fire in the live scheduler. The build and arm gates would break working definitions, and §1's Postgres/SQLite narrative is false for them.

**Attacked sentences**

- §2 table: `` | `KindWeekly` | `interval == 0`, or no weekday in `0..6` | `Weekly(0,nil)`, `Weekly(1,nil)`, `Weekly(1,[Weekday(9)])`, `Weekly(1,[Weekday(-1)])` → false | ``
- §2 table: `` | `KindMonthly` | `interval == 0`, or no day in `1..31` | `Monthly(0,nil)`, `Monthly(1,nil)`, … → false | ``
- §2 preamble: "Measured: `Weekly(1, nil)` and `Monthly(1, nil)` — a *valid positive*
  interval with an empty day list — **also return false**. The doc is fixed in this bundle."
- §1.2: "**On Postgres/SQLite — silent.** … so **the timer never fires and the instance
  waits forever**."
- ADR Decision 2: `Validate()` rejects "a weekday or day-of-month set with **no in-range
  member**".
- ADR Consequences: "A consumer using `EveryExpr → 0` to mean 'no reminder' is affected —
  but today that consumer loses the whole step on MySQL, so **no correct behaviour is
  withdrawn**."

**The defect.** Every measurement in §2 was taken against `scheduler.Trigger.Next` alone.
`Next` is not the arming authority. The live path for a durable timer arm is
`processdriver.go:780` → `NativeScheduler.Activate` → `activateJob` → `triggerDef` →
`scheduler/internal/gocron/trigger.go` `jobDefinition`, and **`jobDefinition` substitutes a
default for an empty day set**:

```
scheduler/internal/gocron/trigger.go:208-210   (triggerDefWeekly)
    if len(weekdays) == 0 {
        weekdays = []time.Weekday{time.Sunday}
    }
scheduler/internal/gocron/trigger.go:215-217   (triggerDefMonthly)
    if len(days) == 0 {
        days = []int{1}
    }
```

`calendarNext` returns `false` for those sets; gocron fires them.

**Command run** (throwaway `scheduler/zzz_lensb_probe2_test.go`, deleted): build each
trigger, take `Trigger.Next`, wrap with `NewScheduledJob(job, next)` and call
`Activate` — i.e. exactly the runtime's durable-arm path, which does **not** go through
`Schedule`.

```
go test -count=1 -v -run TestZZZLensBProbe2 ./scheduler/   → EXIT=0
```

**Real output**

```
PROBE Weekly(1,nil)            Next.ok=false Activate.err=<nil>  ARMED=true  armedNext=2026-08-16T00:00:00Z
PROBE Monthly(1,nil)           Next.ok=false Activate.err=<nil>  ARMED=true  armedNext=2026-09-01T00:00:00Z
PROBE Weekly(1,[Weekday(9)])   Next.ok=false Activate.err=<nil>  ARMED=true  armedNext=2026-08-18T00:00:00Z
PROBE Monthly(1,[32])          Next.ok=false Activate.err=gocron: MonthlyJob: daysOfTheMonth must be between 31 and -31 inclusive, and not 0   ARMED=false
PROBE Every(0)                 Next.ok=false Activate.err=gocron: DurationJob: time interval is 0                                              ARMED=false
PROBE Cron(bogus)              Next.ok=false Activate.err=gocron: CronJob: crontab parse failure … expected exactly 5 fields, found 1          ARMED=false
PROBE Monthly(12,[31])         Next.ok=true  Activate.err=<nil>  ARMED=true  armedNext=2026-08-31T00:00:00Z
```

So the never-due set splits in **two**, and the bundle treats it as one:

| row | `Next` | live scheduler | true classification |
|---|---|---|---|
| `Every(0)`, `Every(-1s)`, `EveryRandom(0,…)`, `Cron("bogus")`, `Cron("")`, `Monthly(1,[32])`, `Daily(0)`, `Weekly(0,…)`, `Monthly(0,…)` | false | **rejects** | genuinely never-due ✓ |
| **`Weekly(1,nil)`, `Monthly(1,nil)`, `Weekly(1,[Weekday(9)])`** | false | **ARMS AND FIRES** | ⚠ **`calendarNext` DISAGREES with the scheduler** — not never-due |
| `Monthly(12,[31])` @Feb | false | arms (interval grid differs) | needs its own measurement |

**Four consequences the bundle gets wrong:**

1. **§1.2's Postgres/SQLite story is false for these three.** They persist a zero
   `next_run` (poisoning `Stats.NextFireAt`, as §1.1 correctly measured) **but the timer
   still fires** — Sunday, or the 1st. The instance does **not** "wait forever". Only the
   dashboard is wrong.
2. **The build gate would reject definitions that work today.** `Build()` refusing
   `Weekly(1, nil)` withdraws working behaviour on Postgres and SQLite. The ADR's "no
   correct behaviour is withdrawn" is false as written.
3. **The arm gate would break them on every dialect** — a reminder that fires weekly on
   Sunday today would stop being armed at all.
4. **Finding A fixes the wrong artifact.** §7 calls `scheduler/trigger.go:170-175` a rotted
   *doc comment* and fixes the prose. But ADR-0140's contract, restated in `calendarNext`'s
   own doc at `scheduler/trigger.go:310-311` — "This matches the live scheduler's own
   interval-jumped first fire **exactly**" — is **violated** by these three cases. This is a
   **`calendarNext` bug**, not doc rot. Documenting the divergence as intended behaviour
   ossifies it.

**Concrete replacement.** Split §2's table into *genuinely never-due* (gocron also refuses)
and *`Next`/scheduler disagreement* rows, and add to §2:

> ⚠ `Trigger.Next` is **not** the arming authority. Measured 2026-08-13 against the live
> `Activate` path: `Weekly(1,nil)` arms and fires 2026-08-16, `Monthly(1,nil)` fires
> 2026-09-01, and `Weekly(1,[Weekday(9)])` fires 2026-08-18 — because
> `scheduler/internal/gocron/trigger.go` defaults an empty weekday set to Sunday and an
> empty day-of-month set to the 1st, while `calendarNext` returns `false`. These three are
> therefore **NOT never-due**; they are a `calendarNext`-vs-scheduler divergence that
> violates ADR-0140's "matches the live scheduler exactly" contract. Neither `Validate` nor
> the arm gate may reject them; closing the divergence is its own decision.

And in ADR Consequences, replace "no correct behaviour is withdrawn" with:

> Correct behaviour **is** withdrawn for the empty-day-set forms unless they are excluded:
> `Weekly(1,nil)` / `Monthly(1,nil)` fire today on Postgres and SQLite (gocron defaults the
> set). They are excluded from both gates and tracked as a `calendarNext` divergence.

**Plan mutation 2 must be replaced.** It currently reads: "Make `Validate` accept
`Weekly(1, nil)` → the table test must go RED (this is the §2 doc-rot case)". That mutation
asserts the defect. Use a genuinely never-due case — `Every(0)` or `Cron("")` — and add a
positive assertion that `Validate(Weekly(1,nil)) == nil`.

---

### C2 — CRITICAL. The ADR's central refactor (`internal/schedcalc`) is forbidden by `TestSchedulerTreeIsSelfContained`, which Plan Phase 2 promises not to touch. The bundle is unimplementable as written.

**Attacked sentences**

- ADR Decision 1: "**One copy of the schedulability math, in `internal/schedcalc`.** … moves
  into a new `internal/` package … `scheduler.Trigger.Next` and the new
  `definition/schedule` predicates both delegate to it."
- Spec §4.1: "An `internal/` package is invisible to consumers, keeps both public types
  independent, and — decisively — keeps **one** copy of the calendar/cron logic."
- Plan Phase 2 step 2: "**The guard is the existing `scheduler` suite, which must pass
  untouched.** Do not edit a `scheduler` test to accommodate the refactor; **a required edit
  means behaviour changed** and must be reported, not absorbed."
- Plan Verification checklist: "`git diff --stat` shows **no** changes under
  `scheduler/*_test.go` (Phase 2's guard)."

**The defect.** `scheduler/selfcontainment_guard_test.go` (a non-obvious member of "the
existing `scheduler` suite") asserts that **no production file under `scheduler/...` may
import any `github.com/kartaladev/wrkflw/*` package outside the `scheduler/...` subtree**:

```
scheduler/selfcontainment_guard_test.go:16-19
    modulePrefix = "github.com/kartaladev/wrkflw/"
    selfPrefix   = "github.com/kartaladev/wrkflw/scheduler"
```

`github.com/kartaladev/wrkflw/internal/schedcalc` is outside that subtree. So Decision 1
requires the import, and Phase 2 forbids the edit that would let it pass. There is no
ordering of the phases that satisfies both.

**Command run** (mutation probe; `scheduler/trigger.go` backed up with `cp`, restored, `git
diff --stat scheduler/` empty afterwards): created a stub `internal/schedcalc` and added
`_ "github.com/kartaladev/wrkflw/internal/schedcalc"` to `scheduler/trigger.go`'s imports.

```
go test -count=1 -v -run TestSchedulerTreeIsSelfContained ./scheduler/   → EXIT=1
```

**Real output**

```
=== RUN   TestSchedulerTreeIsSelfContained
    selfcontainment_guard_test.go:58: package github.com/kartaladev/wrkflw/scheduler imports
    github.com/kartaladev/wrkflw/internal/schedcalc, which is outside the scheduler/ subtree;
    the scheduler tree must stay self-contained and importable standalone
--- FAIL: TestSchedulerTreeIsSelfContained (0.06s)
```

This is also a **substantive** conflict, not merely a test-mechanics one. The guard's doc
states the promise being broken: "This is what lets a consumer depend on `scheduler` alone
without dragging in engine, runtime, persistence, or service." Moving the math to a
repo-root `internal/` package ends `scheduler`'s standalone importability. `go list -deps
./scheduler/` today returns exactly three wrkflw packages — `scheduler`,
`scheduler/internal/obs`, `scheduler/internal/gocron` — confirming the property is real and
currently held.

**Concrete replacement.** The bundle must pick one and say so in the ADR:

- **(a) Put the shared math under `scheduler/internal/schedcalc`** and have
  `definition/schedule` import *that*. Satisfies the guard unchanged, keeps one copy, and
  costs `definition/schedule` a dependency on a `scheduler/internal/...` path — legal,
  since both are in the same module, but it inverts the layering the spec was avoiding.
- **(b) Amend the guard deliberately**, with an explicit allowlist entry for
  `internal/schedcalc` and an ADR paragraph recording that `scheduler`'s
  standalone-importability promise now admits one shared pure-computation package. Requires
  deleting Phase 2 step 2's "must pass untouched" rule and the checklist line.
- **(c) Do not move the math.** Have `definition/schedule`'s predicates delegate to
  `scheduler` (accepting the layer inversion the spec rejected) or duplicate the logic
  (accepting the drift the spec rejected).

Whichever is chosen, ADR Decision 1 must name
`scheduler/selfcontainment_guard_test.go` explicitly, and Phase 2 must stop claiming the
`scheduler` suite passes untouched.

---

### M1 — MAJOR. There is a **fourth** never-due gate already in the tree — `NativeScheduler.Schedule` — and a **third** arm path (`armStartTimer`) that neither of the bundle's new runtime gates covers. The ADR's "No timer is ever armed that can never fire" is false after this change.

**Attacked sentences**

- ADR Decision headline: "**No timer is ever armed that can never fire**, and a zero
  `next_run` never reaches any dialect."
- Spec §4.3 heading: "**Three gates**".
- Spec §3.3: "**No layer rejects** `Every(0)`, `Cron("bogus")`, `Weekly(1, nil)`, or
  `Monthly(12,[31])`."
- Spec §3.4: "All **eight** non-test `ScheduleTimer{}` constructions are in `engine/` … So
  the exposure is **exactly**: the six gated sites, plus the anchor-dependent class."

**The defect, part 1 — an unenumerated existing gate.** `scheduler/scheduler.go:578-581`
already refuses a never-due trigger:

```go
next, ok := j.Trigger().Next(s.now().In(s.location()))
if !ok {
    return nil, fmt.Errorf("workflow-scheduler: job %q trigger can never fire: %w", j.ID(), ErrUnsupportedTrigger)
}
```

`scheduler/trigger.go` documents this in three places (`:47-48` "the scheduler enforces
validity at schedule time"; `:121-122`; `:110-112`). So "no layer rejects" is false — a
layer does, and the bundle adds a fourth gate without noting the third. Crucially,
**`Activate` has no such check** (`scheduler/scheduler.go:612-625` → `activateJob` →
`triggerDef` → `impl.ScheduleJob`), and `Activate` is the path durable timer arms take
(`processdriver.go:780`). That asymmetry is why the defect exists at all, and it belongs in
§1 as the mechanism.

**The defect, part 2 — a third arm path.** `ScheduleTimer{}` is not the only way a timer is
armed. Timer-**start** events are armed by a wholly separate path that the bundle never
mentions:

```
runtime/timerops.go:491  RehydrateStartTimers
  → runtime/timerops.go:505  timerStartDefs(driver.listDefinitions(ctx))
      → runtime/event_start.go:162-172  reads se.Timer RAW — no ResolveTrigger
  → runtime/timerops.go:429  armStartTimer
      → runtime/timerops.go:457  scheduleStartTimerJob → convertTrigger → sched.Schedule
```

Commands run:

```
grep -rn "convertTrigger" --include="*.go" . | grep -v _test.go
  → runtime/timerops.go:145 (timerJobsFor), :241 (buildTimerJob), :458 (scheduleStartTimerJob)
grep -rn -A15 "func timerStartDefs" runtime/    → reads se.Timer, guarded only by IsZero
```

So `event.StartEvent.Timer` — one of the five fields §4.3 enumerates — is reached by a path
with **no step gate** (there is no `Step`; there is no instance yet) and **no arm gate**
(`timerJobsFor` is not involved). Its only ADR-0176 protection is the **build** gate, which
per §3.5 does not apply to stored definitions. A stored definition with a never-due
timer-start therefore still reaches `armStartTimer` after this change.

Mitigating (and worth recording): `scheduleStartTimerJob` calls `sched.Schedule`, so the
pre-existing gate of part 1 **does** catch it — which is exactly why `armStartTimer`'s doc
already says "An unschedulable trigger is logged at WARN and skipped". And the job is
`ActivationAuto` under `startTimerJobKind` with no JobStore registered, so **nothing durable
is written** and no zero `next_run` appears. The narrow §4.3 claim ("the reason no zero
`next_run` can exist") survives; the broad ADR Decision headline does not.

**Concrete replacement.** ADR Decision headline:

> **A zero `next_run` never reaches any dialect, and no timer armed through the
> instance-timer path (`ScheduleTimer` → `timerJobsFor`) can be never-due.** Timer-**start**
> events arm through `armStartTimer`/`scheduleStartTimerJob` instead; they are covered by
> the build gate for newly built definitions and, for stored ones, by the pre-existing
> `NativeScheduler.Schedule` check at `scheduler/scheduler.go:578` (WARN and skip, nothing
> durable written). They are deliberately **not** covered by the new arm gate.

Spec §4.3 heading: "Three **new** gates (a fourth already exists — see §3.6)", plus a new
§3.6 recording `Schedule`-vs-`Activate` and the `armStartTimer` path. Spec §3.3's "No layer
rejects" → "No layer rejects them **at definition or step time**; `NativeScheduler.Schedule`
rejects them at schedule time, and gocron rejects `Every(0)` and `Cron("bogus")` at
`Activate` (measured) — but not the empty-day-set forms (C1)."

---

### M2 — MAJOR. `newScheduledTimerJob`'s comment asserts the never-due path is "impossible" and that "the scheduler re-validates the trigger at arm time anyway". Both halves are false, and the second is the exact gap this ADR exists to close.

**Attacked artifact** — `runtime/timerjob.go:66-71` (shipped code, not the bundle, but the
bundle's Delivery Gate step 2 sweeps adjacent comments and this one is directly adjacent):

```go
// newScheduledTimerJob wraps j with NextRun = j.trig.Next(now). Callers build
// j from a successfully converted trigger, so Next reports ok; on the
// impossible not-ok path the zero time is stamped (the scheduler re-validates
// the trigger at arm time anyway).
func newScheduledTimerJob(j *timerJob, now time.Time) *scheduledTimerJob {
	next, _ := j.trig.Next(now)
```

- "Callers build `j` from a successfully converted trigger, so `Next` reports ok" — false.
  A never-due trigger converts successfully and `Next` reports **not** ok; that is the whole
  premise of this ADR. The "impossible" path is the reachable one.
- "the scheduler re-validates the trigger at arm time anyway" — false for this caller.
  `newScheduledTimerJob` is called at `processdriver.go:744` for jobs armed via
  **`Activate`**, which does not re-validate (M1). Only `Schedule` does.

The `ok` flag is discarded with `_`. Verified that this does **not** corrupt the persisted
value: `jobStore.Save` (`runtime/jobstore.go:119-127`) persists `td.descriptor()`, i.e. the
`kernel.JobSpec.NextRun` set by `timerJobsFor` — not `scheduledTimerJob.nextRun`. So the
bundle's choice to gate inside `timerJobsFor` is the correct placement. ✓

**Concrete replacement.** Add to Plan Phase 5:

> 6. Correct `runtime/timerjob.go:66-69`'s comment. Both claims are false: a never-due
>    trigger converts successfully and `Next` reports not-ok (that is this ADR's premise),
>    and `Activate` — the path this caller's jobs take — does not re-validate; only
>    `NativeScheduler.Schedule` does. State that the discarded `ok` is safe **because** the
>    persisted value comes from `kernel.JobSpec.NextRun`, not from this field.

---

### M3 — MAJOR. "Every call site guards with `if !timerSpec.IsZero()`" is false at one of the six sites, and "six working paths" undercounts the guards by three.

**Attacked sentences** (§3.1, and the ADR's restatement)

- "**Every** call site guards with `if !timerSpec.IsZero()` (or `if reminderSpec.IsZero() {
  return }`)."
- "a predicate that rejects it breaks **six** working paths"
- ADR: "the established … sentinel that **six** call sites already guard on with `IsZero()`"

**Command run**

```
grep -rn "IsZero()" --include="*.go" engine/ runtime/ | grep -v _test | grep -iE "spec|timer|trigger"
```

**Real output** — `TriggerSpec.IsZero()` guards in non-test `engine/`: **nine**, not six —
`step_boundaries.go:51`, `step_errors.go:64`, `step_triggers.go:69`,
`step_eventsubprocess.go:24`, `step_eventsubprocess.go:90`, `step_nodes.go:695`, `:770`,
`:829`, `:986`. Two more in `runtime/` (`event_start.go:166`,
`definition_registry.go:248`) — eleven repo-wide.

And the blanket "every" is **false at the event-sub-process site**:
`step_eventsubprocess.go:90` guards `!se.Timer.IsZero()` — the **raw, pre-resolution** spec
— whereas the other five guard the **resolved** result (`timerSpec`, `reminderSpec`,
`deadlineSpec`, `gwTimerSpec`). It is the one site where resolution happens *inside* the
guard rather than before it.

Behaviourally harmless today (verified: `ResolveTrigger`,
`engine/trigger_resolve.go:14-27`, can never return a `KindUnset` spec with a nil error —
`KindEveryExpr` → `Every(d)`, `KindExpr` → `AfterDuration(d)`, and the `TriggerSpec{}`
return at `:21` always accompanies a non-nil error). But it is load-bearing for the plan:
Phase 4b step 5 says "apply `ValidateAt(anchor.In(loc))` at all **six**
`ResolveTrigger` sites", and at this site the insertion point is *inside* the
`else if !se.Timer.IsZero()` block, not before the guard like the other five.

**Concrete replacement** for §3.1:

> Five of the six arm sites guard the **resolved** spec (`step_boundaries.go:51`,
> `step_nodes.go:695`/`:770`/`:829`/`:986`); `step_eventsubprocess.go:90` guards the **raw**
> `se.Timer` and resolves inside the guard. `KindUnset` means "this node declares no timer",
> so both predicates must return nil for a zero spec — nine `TriggerSpec.IsZero()` guards in
> non-test `engine/` code depend on that reading (eleven repo-wide, counted 2026-08-13).

---

### m1 — MINOR. The three migration-file paths do not exist as cited.

Spec §1.1 cites `migrations/mysql/0001_init.sql:95`,
`postgres/0001_init.sql:103`, `sqlite/0001_init.sql:102`.

```
find . -name "*init*.sql" -not -path "./.git/*"
  → ./internal/persistence/store/migrations/{mysql,postgres,sqlite}/0001_init.sql
```

The **line numbers and column types are exact** (`next_run DATETIME(6) NOT NULL` at
mysql:95; `TIMESTAMPTZ NOT NULL` at postgres:103; `TEXT NOT NULL` at sqlite:102). Only the
path prefix `internal/persistence/store/` is missing. Fix: prefix all three.

### m2 — MINOR. It is `ApplyTrigger`, not `Drive`, that returns the error in the cited test — and the test never runs on MySQL.

Spec §1.2 and ADR Context both say `assertSameTxAtomicity` asserts "**`Drive`** returns the
error". Read `runtime/timer_txflow_test.go:189-247` (citation **exact**: the function opens
at 189 and closes at 247): `Drive` is the *successful setup* at line 205; the error
assertion at 223-226 is on `driver.ApplyTrigger`. The injected method is `UpsertJob`
(`:84-94`) ✓.

Additionally, `grep -rn "assertSameTxAtomicity" runtime/` → called at `:253` (SQLite) and
`:262` (Postgres) **only**. It never runs on MySQL — the one dialect where the zero
`next_run` rejection actually occurs. The tx-atomicity *mechanism* is shared, so the
inference is reasonable, but "needs no new proof" is an unlabelled analogy across dialects.

Fix: "`ApplyTrigger` returns the error … (the shared body runs on SQLite and Postgres; the
MySQL rollback is inferred from the same `RunInTx` seam — `ASSUMPTION (unverified)` on
MySQL specifically)."

### m3 — MINOR. "Eleven lines above" is line arithmetic that is both ambiguous and pre-rotted.

Spec §1: "**Eleven lines above**, the sibling failure … is WARN-logged and `continue`d."
ADR: "**eleven lines below** a sibling branch".

From line 156, eleven lines up is `runtime/timerops.go:145` — the `convertTrigger` call, not
the WARN or the `continue`. The `continue` is **two** lines above (`:154`); the WARN starts
seven above (`:149`). The count is defensible only if measured from the branch's first line,
and the two documents disagree on direction.

Fix: drop the arithmetic. "The immediately preceding branch — the `convertTrigger` failure
at `timerops.go:145-155` — WARN-logs and `continue`s, so no arm and no durable row is
produced."

### m4 — MINOR. `EveryRandom(min > max)` is a silent-forever-wait class the invariant does not cover and would falsely claim to.

Measured (probe 1): `EveryRandom(1m, 0)` → `Next` **ok=true**, `next=+1m`. So both new
predicates **accept** it and the arm gate lets it through with a valid non-zero `next_run`.
`scheduler/trigger.go:110-112` documents that min>max "validation happens at schedule
time", and gocron's `DurationRandomJob(mn, mx)` is where it surfaces — at `Activate`, which
is WARN-only and benign (`processdriver.go:773-788`).

Result: a durable row with a plausible `next_run` on **all three** dialects, a timer that
never fires, and an instance that waits forever — the exact §1.2 harm, reached without a
zero `next_run`. The ADR's "no timer is ever armed that can never fire" would cover this by
its wording and does not cover it in fact.

Fix: record it in §5 "Not closed here" — "`EveryRandom` with `min > max` reports
`Next` ok=true (measured) and is rejected only by gocron at `Activate`, i.e. WARN-only. It
produces a non-zero `next_run` row and a timer that never fires on all three dialects. Not
addressed here; bounds validation is its own decision."

### m5 — MINOR. `model.Validate` is the wrong insertion point to name; the recursion lives in `validateStructure`.

Spec §4.3 and ADR Decision 3 both say the build gate goes in `model.Validate`. `Validate`
(`definition/model/validate.go:260-268`) only checks `Version` and delegates; the node walk
and the **recursion into nested definitions** are in `validateStructure`
(`:277`, recursing at `:550` for `KindSubProcess` nodes). A gate written into `Validate`
would silently exempt every sub-process and every event sub-process — including the nested
`StartEvent.Timer` that `step_eventsubprocess.go:91` arms. That is precisely the
"gate that walks only some fields" hazard §4.3 warns about.

Confirmed the recursion reaches event sub-processes: an event sub-process is a
`KindSubProcess` node whose inner start is event-triggered (`validate.go:339`, `:759`), so
`:542`'s `KindSubProcess` filter admits it.

Fix: name `validateStructure`, not `Validate`, in spec §4.3, ADR Decision 3 and Plan Phase
4a step 4, and add a Phase 4a RED case for a never-due trigger on a node **inside a nested
sub-process definition**.

### m6 — MINOR. "definition/schedule pulling in nothing" — it imports `time`.

§4.1's parenthetical. `go list -deps ./definition/schedule/ | grep -c wrkflw` → **1** (itself);
the remaining deps are stdlib (`time` and its transitive `internal/godebug`,
`internal/oserror`, `syscall`). Accurate in spirit; say "no non-stdlib dependencies" so the
sentence is checkable.

---

## Numeric and citation claims that SURVIVE (re-derived independently)

| claim | how verified | verdict |
|---|---|---|
| **eight** non-test `ScheduleTimer{}` sites, all in `engine/` | `grep -rn "ScheduleTimer{" --include="*.go" . \| grep -v _test.go \| wc -l` → 8 | ✓ **exact**; no variable, helper, slice or re-emitted-`Commands` construction anywhere else (checked all 206 `ScheduleTimer` mentions repo-wide; the rest are the type decl, `isCommand()`, doc comments, and test assertions) |
| all eight site line numbers (`step_boundaries.go:54`, `step_triggers.go:328`, `step_eventsubprocess.go:97`, `step_compensation.go:493`, `step_nodes.go:699/772/831/988`) | same grep | ✓ **all exact** |
| **six** gated by `ResolveTrigger` at `:47`, `:91`, `:691`, `:766`, `:825`, `:982` | `grep -rn ResolveTrigger` (8 non-test hits = 6 calls + decl + doc) and reading each region | ✓ **all exact**; in each the resolved spec flows into that site's `ScheduleTimer.Trigger` |
| the two bypass sites are the only ungated ones, both `AfterDuration` | read `step_compensation.go:493`, `step_triggers.go:328` | ✓ `AfterDuration(pol.stallAfter)` / `AfterDuration(delay)`; `Next`'s `triggerAfter` branch (`:207-208`) returns `true` unconditionally, so "always ok" holds — measured `After(0)` ok=true, `After(-1h)` ok=true, next `09:00` (past, non-zero) |
| `calendarNext` has **four** false-returning branches | `grep -n "return.*false" scheduler/trigger.go` | ✓ exactly four, returning at **318/346/357/399**. Spec cites `:317/:345/:356/:365-400` — the `if` line one above each `return`; internally consistent. None unreachable. |
| `calendarNext` = `scheduler/trigger.go:316-400`; `:383` = `monthIndex%interval` | `sed -n '316,400p'`, `sed -n '383p'` | ✓ **exact** |
| no OTHER function in the trigger path returns an unenumerated false | `grep -n "return.*false"` → `Next`'s own five at 204 (At-zero), 211 (`Every<=0`), 216 (`EveryRandom min<=0`), 222 (cron parse), 232 (default/unset); the rest are `AbsTime`/`Duration`/`CronExpr`/`Random`/`Calendar` accessors and `Recurring` | ✓ all five accounted for in §2 (At-zero via the correct "unreachable from a definition" carve-out) |
| `runtime/timerops.go:156-160` quoted snippet | `sed -n '156,160p'` | ✓ **byte-for-byte exact** |
| `maxCalendarScanDays = 366 * 5`; bound `× interval`; **21,960** at interval 12; "~60-year scan" | `scheduler/trigger.go:297`, `:364`; `366*5*12` | ✓ 1830; 21960 **exact**; 21960/365.25 = 60.1 yr ✓ |
| **ten** `schedule.Kind` values, table covers every one | `definition/schedule/trigger.go:12-23` | ✓ exactly 10; §2's 9 rows cover all ten (`KindExpr`/`KindEveryExpr` share a row). `convertTrigger`'s own doc (`runtime/timerops.go:40`) independently says "all 10". No kind missing a row. |
| **five** definition-carried `TriggerSpec` fields, at `node.go:64`/`:71`, `event.go:32`/`:131`/`:171` | `grep -rn "schedule.TriggerSpec" definition/ \| grep -v _test \| grep -v definition/schedule/` | ✓ exactly five **struct fields**, all five line numbers **exact**. `definition/activity/options.go:156` (`every`) is an unexported option-struct field, correctly excluded. No TriggerSpec field reachable only through a nested definition — nesting reuses these same five. |
| `WaitFields` is embedded, so the five do not close the set | `grep -rn WaitFields` | ✓ embedded by `model.ActivityFields` (`node.go:88`) and `event.IntermediateCatchEvent` (`event.go:129`); `ActivityFields` in turn by 7 activity types (`activity.go:18,28,82,99,113,123,135`). `DeadlineOf`/`WaitActionOf` (`accessors.go:28`,`:41`) dispatch on the promoted `deadline()`/`waitAction()` methods, so they reach **all** embedders. The spec's instruction to re-enumerate rather than assume is correct and warranted. |
| `model.Validate` has **exactly one** non-test caller, `builder.go:133` | repo-wide `grep -rn "Validate("` | ✓ **exact**. `transport/http/httpcore/endpoints.go:26/83/101` call a *different* `Validate` (`httpcore/validate.go:32`) — correctly not counted. No indirect caller. |
| `definitions.go:92` = `PutDefinition` (marshal+INSERT), `:114` = `GetDefinition` (SELECT+unmarshal) | `sed -n '88,118p'` | ✓ **both exact**, both descriptions accurate |
| `processdriver.go:730-757` = `commitFn`; `:758-767` = `RunInTx` + `workflow-runtime: commit: %w`; `:773-788` = post-commit `Activate` WARN loop | `sed -n '725,795p'` | ✓ **all three exact** |
| `timer_txflow_test.go:189-247` = `assertSameTxAtomicity` | `grep -n` + read | ✓ **exact** (see m2 for the `Drive`/`ApplyTrigger` slip) |
| `engine/trigger_resolve.go:14-27`, `:23` = the `KindEveryExpr` branch; `EveryExpr` → `Every(d)` with no positivity check | `cat -n engine/trigger_resolve.go` | ✓ **exact** |
| `runtime/timerops.go:30-37` = `schedulingLocation()`, defaults UTC via `locatedScheduler` | `sed -n '30,37p'` | ✓ **exact** |
| `builder.go:133` reached by code builder and `ParseYAML(...).Build()` | `builder.go:198`, `:227` both call `c.build()` | ✓ |
| ADR-0137 made calendar resolution location-aware | read its Decision | ✓ **true** — Decision 1 "`calendarNext` builds candidate at-times … in `after.Location()`" |
| ADR-0167 changed *decoding*, which **is** on the read path (unlike `Validate`) | `docs/adr/0167-definition-decoding-rejects-unknown-fields.md` exists; §3.5's correction is sound | ✓ the distinction drawn is correct |
| every cited ADR exists | `ls docs/adr/` | ✓ ADR-0137, 0167, 0175 (the only three cited) all present. **No phantom `§`-style citation** of the `ADR-0034 §2.5` kind was inherited into this bundle — the bundle cites no section numbers at all. |
| plan Phase 1.3: `engine/purity_test.go`'s AST scan is **non-recursive**, so neither `definition/schedule` nor a new `internal/` package is covered | `grep -n "ReadDir\|IsDir" engine/purity_test.go` → `for _, dir := range []string{".", "../definition"}` + `if e.IsDir() { continue }` | ✓ **exact and correctly hedged** |
| §2's whole measured table (23 rows re-run independently) | throwaway `scheduler/zzz_lensb_probe_test.go`, `go test -count=1 -v -run TestZZZLensBProbe ./scheduler/` → EXIT=0 | ✓ **every row reproduces exactly**, including `Monthly(12,[31])` false@Feb/false@Apr/true@Aug, `Daily(1,{Hour:25})` → ok=true `2026-08-13T01:00:00Z`, and `At(zero)` → false. The spec's own numbers are honest. (What they do *not* establish is C1.) |
| MySQL `DATETIME` range starts at `1000-01-01` | MySQL documented range is `1000-01-01 00:00:00`–`9999-12-31 23:59:59`; consistent with the measured `Error 1292 … '0000-00-00'` | ✓ true, though this one is documentation rather than execution — label it `ASSUMPTION (unverified)` or cite the manual |
| §4.1's `go list -deps` measurement | `go list -deps ./scheduler/ \| grep wrkflw` → `scheduler`, `scheduler/internal/obs`, `scheduler/internal/gocron` | ✓ **exact** (and see C2: this property is *enforced*, which the spec did not notice) |

---

## What the counting lens did NOT find a problem with

- The eight/six/two split, the four branches, the five fields, the ten kinds, the one
  `Validate` caller: **all correct**. Every line citation except the migration paths is
  exact. This bundle's arithmetic is the best of the recent deliveries.
- The soundness/incompleteness argument of §2.1 is correct and correctly one-directional;
  the warning against asserting the converse is right, and `Monthly(12,[31])` really is the
  witness.
- The gate-placement choice (inside `timerJobsFor`, not `newScheduledTimerJob`) is
  **correct** — verified that `jobStore.Save` persists `kernel.JobSpec.NextRun`.
- `StepOptions.SchedulingLocation`'s justification holds: `schedulingLocation()` really is
  runtime-only, and the anchor's month really does decide the calendar verdict.

# 176. A timer arm must agree with the scheduler that will run it

- Status: Accepted (rewritten after the rule-#9 audit refuted the first design, 8 Criticals; then
  amended in-bundle where implementation refuted it again — see *What implementation corrected*)
- Date: 2026-08-13

> Design and every measurement:
> [`docs/specs/2026-08-13-never-due-timer-triggers.md`](../specs/2026-08-13-never-due-timer-triggers.md).
> Audit record: `docs/specs/2026-08-13-adr-0176-audit-lens-{a,b,c}.md`.
> Plan: [`docs/plans/2026-08-13-never-due-timer-triggers.md`](../plans/2026-08-13-never-due-timer-triggers.md).
>
> Closes pre-v0.1.0 **blocker 2** and a **whole-process availability defect** the audit found.
>
> Deliberately **not** closed here — each because the audit found a concrete defect in the
> first design: a deploy-time `model.Validate` gate, a step-time engine gate,
> `StepOptions.SchedulingLocation`, migrating existing zero-`next_run` rows. See Consequences.

## Context

The runtime computes a timer's persisted `next_run` from `scheduler.Trigger.Next` and keeps the
**zero time** when `Next` reports `ok == false` (`runtime/timerops.go:156-160`). The arm is
written **inside the state-commit transaction**, while the scheduler is touched only
**after** the commit, where `Activate` failure is deliberately WARN-only and benign.

Measured on all three dialects: MySQL rejects the zero value — `Error 1292 (22007):
Incorrect datetime value: '0000-00-00'` — which fails `commitFn` and rolls back the step, so
**the instance can never advance past that node**. Postgres and SQLite accept it as
`0001-01-01`, hang the instance, and let the row head the keyset index so it becomes
`Stats.NextFireAt` permanently. ⚠ The rejection is of the **zero literal**, not a range floor:
MySQL was measured accepting year-1 and year-999.

An adversarial audit of the first design (three lenses, eight Criticals) then established two
facts that changed the decision entirely.

**First, `Trigger.Next` is not the arming authority.** Armed through the live
`NativeScheduler.Activate` — the ADR-0134 durable path the runtime actually uses — with the
clock at `2026-02-10`:

| trigger | `Next.ok` | live `Activate` |
|---|---|---|
| `Weekly(1,nil)`, `Monthly(1,nil)`, `Weekly(1,[Weekday(9)])`, `Monthly(1,[-1])` | **false** | **nil — arms and fires** |
| `Cron("0 0 30 2 *")` | **true**, zero instant | error `CronJob: invalid crontab` |
| `Monthly(12,[31])` | false | ***never returns*** |
| `Every(0)`, `Daily(0)`, `Weekly(0,…)`, `Cron(bogus)`, `Monthly(1,[0])`/`[32]` | false | error |

`Next` disagrees in **both** directions. gocron substitutes defaults for an empty day set,
normalises an out-of-range weekday, and treats a **negative** day-of-month as counting back
from month end (`-1` = last day) — a feature `calendarNext` does not model. So four specs arm
and fire while `Next` calls them never-due, and any guard keyed on `!ok` alone would **regress
four working definitions**. Meanwhile `Cron("0 0 30 2 *")` (30 February) reports `ok=true` with
the **zero instant**, because `robfig/cron` gives up after five years and returns the zero time
with no error — so it defeats every `!ok`-keyed gate and still writes a zero row. This
divergence also violates **ADR-0140**, which states that `Next`'s first reported fire matches
the live scheduler's first fire.

**Second, and outranking the blocker: `Activate` on `Monthly(12,[31])` never returns.**
gocron v2.22.0's `monthlyJob.next` spins forever inside gocron's single `selectNewJob`
goroutine, so every job's arm, cancel and rehydrate in the process blocks behind it. It is
**clock-month dependent** — wedging in the five months without a 31st and passing cleanly in
the other seven, which is why a first reproduction on an August clock looked like a
refutation. gocron v2.22.0 is a **hard pin** (ADR-0135), so the arm must be refused *before*
`Activate` is called. ⚠ This inverts the dialect framing: MySQL is accidentally the *safe*
dialect, because its commit fails before `Activate` is reached.

Two further audit findings shape the fix: **rehydration bypasses everything** — it re-arms from
the persisted `next_run`, and `rehydrateTrigger` returns recurring triggers verbatim (every
never-due kind is recurring), so a legacy zero row reaches `Activate` at boot; and
**nothing reclaims those rows** — `PruneTimers` was measured deleting 1 of 5, only the
non-recurring control.

Finally, the sentinel the first design proposed to invent already exists:
`scheduler/scheduler.go:575-578` refuses a never-due trigger with *"trigger can never fire"*
wrapping `ErrUnsupportedTrigger`. The actual defect is that ADR-0134's durable `Activate` path
**bypasses** that gate while `Schedule` honours it.

## Decision

**The runtime never persists or activates a timer the scheduler cannot run.**

Stated narrowly and deliberately: the first design claimed "no timer is ever armed that can
never fire", which the audit falsified via the raw start-timer path.

1. **Make `Next` agree with the scheduler first.** Correct `calendarNext` and the cron path to
   model what gocron actually does: the substituted defaults for an empty weekday or
   day-of-month set, out-of-range weekdays, negative day-of-month as counting back from month
   end, and `!ok` (not `(zero, true)`) when a cron has no findable next occurrence. This
   restores ADR-0140's contract.

   ⚠ **Ordering is load-bearing**: this must land *before* the guard, or the guard regresses the
   four working shapes above. The exact substituted instants are **not** fixed by this
   decision — they are derived by probing `Activate` during implementation, because guessing
   them is precisely how the first design failed. (They were: measurements §14.)

   `Monthly(12,[31])`-style specs must keep reporting `!ok`; that is what lets the guard refuse
   them before the livelock.

   *As built* (§15.4): the empty-set defaults are **ours** — `scheduler/internal/gocron/trigger.go`
   substitutes Sunday and the 1st before gocron sees the job — and an out-of-range weekday is
   **not** normalised into the week. It stays a raw day offset from the anchor, so it resolves on
   gocron's first pass, **ignores the interval**, and beats an in-range weekday the anchor has
   already passed. No membership test can express that, so `calendarNext`'s weekly branch is a
   transcription of gocron's `weeklyJob.next` rather than a patched scan; `Daily` and `Monthly`
   keep the bounded scan, which is what still reports `!ok` for the livelocking shape.

2. **Guard every arm path on `!ok || next.IsZero()`.** Four sites: `timerJobsFor` (covering both
   the in-tx write and the post-commit `Activate`), `armStartTimer`/`scheduleStartTimerJob` (the
   raw start-timer path), `jobStore.Load` (rehydration), and — added at `/code-review` — a
   **re-check immediately before `Activate`**, because the guard and the arm read the clock at
   different instants and a calendar trigger's answer is anchor-dependent (§16.2).

   Every refusal increments `wrkflw_timer_arms_refused_total`. A refused arm parks its token
   forever, so it must be something an operator can alert on, not only a log line.

   Refusal is a WARN log naming timer id, instance id and kind, with no arm and no row —
   matching the unconvertible-trigger branch already in `timerJobsFor`. It logs rather than
   erroring because by then the `StepResult` is computed, and because the audit measured a
   step-time failure **wedging a running instance**.

   *As built*, each site's justification was corrected by measurement:

   - **The `IsZero()` half no longer closes the 30-February cron** (§15.2). Decision 1 lands
     first and makes that cron report `!ok`, so `!ok` closes it and **no trigger shape reaches
     the `IsZero()` half any more**. It is kept as the direct statement of blocker 2's invariant
     — a zero `next_run` must never be persisted — and as a regression guard on decision 1, and
     is pinned by a predicate test rather than through a trigger.
   - **The start-timer path is not "ungated today"** (§15.1). Both `Scheduler` implementations
     this repo ships refuse an `!ok` trigger in their own `Schedule` — including the livelocking
     shape, which therefore never reached `Activate` by this route. The guard's real
     justification is that the runtime consumes the **port**: a consumer-supplied `Scheduler` is
     free to arm a never-due job, and was measured doing so with a zero next run and a nil error.
   - **The rehydration guard is keyed on the re-derived instant, not a legacy zero row**
     (§15.3). Rehydration re-arms from the trigger, so a row with a perfectly valid stored
     `next_run` (`2026-08-31`) still wedges a February boot, while a row whose stored value is
     zero but whose trigger still fires is re-armed and **heals**. Keying on the stored zero
     would have missed the first and stranded the second.

3. **No new sentinel, no moved math, no new engine input.** The guard lives in `runtime`, which
   already imports both packages.

## Consequences

**Fixed:** blocker 2 on all three dialects; a whole-process livelock reachable from a legal
definition, on **both** the fresh-arm and the boot-rehydration path (each demonstrated by a
regression test that hangs for its full 10s timeout without the fix and returns in under a
second with it); `Next`'s divergence from the live scheduler.

**Behaviour changes:** four calendar shapes that report `!ok` today begin reporting a real
instant. This is a correction, and since they already armed and fired, **no working definition
changes behaviour**. A never-due arm is skipped with a WARN rather than writing a zero row or
hanging the process.

A fifth class moves that this ADR did not originally name: a weekday set MIXING in-range and
out-of-range weekdays reported the in-range instant and now reports gocron's (§15.4). Two
`scheduler` tests asserted the old answers for the empty-set shapes and were changed with the
behaviour; **no other test in the repo moved**, which is the evidence that the weekly rewrite is
behaviour-preserving for ordinary weekday sets. `Trigger.Next` is exported, so a consumer
asserting `!ok` on any of these shapes sees a change — worth a release note.

**Also fixed, found at `/code-review`** (§16, all measured):

- **Eight further shapes reported a fire instant that the live scheduler refuses at setup** — an
  out-of-range entry anywhere in a monthly day list, `EveryRandom` with `min >= max`, and any
  at-time with hours > 23 or minutes/seconds > 59. Same failure mode as blocker 2 without the zero
  literal: a *wrong* `next_run` committed, `Activate` failing where failure is WARN-only, the
  token parked forever. `Next` now refuses them, and all twelve probed shapes agree with live
  `Schedule`. This also closes the previously-filed `EveryRandom(min>max)` hole, which no
  `next_run`-keyed guard could have caught because its `next_run` is non-zero.
- **The monthly scan's cost is no longer linear in a consumer-supplied `interval` at day
  granularity** — off-grid months are skipped whole. 6.34 s → 392 ms at `interval = 120000`,
  proven semantics-preserving by 255,438 differential comparisons against the previous walk
  across three zones including DST.

**Costs accepted:**

- A refused arm leaves the engine's in-memory `timerRecord` with no durable row, so
  `HasArmedTimers()` over-reports and the parked token is *less* diagnosable than the poisoned
  row was. Making it fully visible needs a post-step incident channel that does not exist; the
  new counter is the aggregate signal available without one.
- **The arm guard is not atomic with the arm.** `activateJob` discards the `ScheduledJob`'s
  instant and lets gocron re-derive from the trigger at its own, later clock reading, so a trigger
  that goes never-due in between can still reach it. The pre-`Activate` re-check narrows that
  window from a whole commit to a few instructions; closing it entirely would require gocron to
  honour the instant it is handed.
- **The monthly scan is still linear in `interval`**, now at month rather than day granularity
  (~16× smaller constant). A definition with an interval in the tens of thousands of months still
  costs hundreds of milliseconds on the arm path.
- Existing zero-`next_run` rows are not migrated. The rehydration guard stops them wedging
  boot; nothing deletes them, and `PruneTimers` provably cannot. Manual remediation is
  documented.

**Deferred, each for a measured reason:**

- **A deploy-time `model.Validate` gate** — its prescribed mechanism was an **import cycle**
  (`definition/event` imports `definition/model`). A mechanism exists (the wire form, via
  `toWire`) and must live in `validateStructure` or every nested sub-process is exempt. Also
  `Build()` already rejects every recurring `DeadlineTimer`, making part of that gate dead code.
- **A step-time engine gate** — measured to **wedge a running instance**: the step rolls back
  with the already-fired timer row restored at a past `next_run` and fails identically forever,
  which is worse than the inert row it replaces.
- **`StepOptions.SchedulingLocation`** — only a step-time gate needs it; it does not eliminate
  both error directions (nil→UTC gives a direct-`engine.Step` consumer the very false
  rejection it exists to prevent), and cron-under-`FixedZone` is unfixable by any location
  (ADR-0136).
- **Moving the calendar math to a shared package** — unnecessary once the guard lives in
  `runtime`, and **forbidden** by `scheduler/selfcontainment_guard_test.go` (proven by
  mutation by two independent lenses). `scheduler/internal/schedcalc` is not an escape: Go's
  internal rule would bar `definition/schedule` from reaching it.

**Opened:** `EveryRandom(min>max)` is an uncovered silent forever-wait with a *non-zero*
`next_run`; the calendar scan is unbounded in a consumer-supplied `interval` (~102 h projected
at `MaxUint32`); `runtime/timerjob.go:66-69`'s comment carries three false claims (fixed
in-bundle); the definition store round-trips semantically invalid definitions.

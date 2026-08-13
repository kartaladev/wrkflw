# ADR-0176 bundle audit — Lens C: design coherence & failure modes

- Date: 2026-08-13
- Auditor lens: cross-document contradictions, unstated assumptions, missing failure
  modes, migration gaps, "does the design achieve its own invariant".
- Bundle audited: `docs/specs/2026-08-13-never-due-timer-triggers.md`,
  `docs/adr/0176-reject-never-due-timer-triggers.md`,
  `docs/plans/2026-08-13-never-due-timer-triggers.md`
- Worktree: `agent-a4a2d1ffc3ab80156`. **Step 0**: the bundle was ABSENT at the worktree's
  base commit (`e04bd670`); recovered by `git merge feat/never-due-timer-triggers`
  (`726fe7ad`) before auditing anything.
- Every behavioural claim below was **executed**. Probe files were written under
  `scheduler/zzz_audit_probe*_test.go` and
  `internal/persistence/store/zzz_audit_probe_test.go`, run, and deleted; the raw output is
  pasted inline. Mutations were restored from `cp` backups, never `git checkout`.

---

## C1 — CRITICAL: ADR Decision 1 is forbidden by a shipped, enforced architectural guard, and the plan forbids the only mechanical escape

**Attacks**: ADR Decision 1; spec §4.1 in full; plan Phases 1–2; plan verification checklist
line "`git diff --stat` shows **no** changes under `scheduler/*_test.go`".

`scheduler/selfcontainment_guard_test.go`'s `TestSchedulerTreeIsSelfContained` asserts that
**no production file anywhere under `scheduler/...` may import any
`github.com/kartaladev/wrkflw/*` package outside the `scheduler/...` subtree**. Its own doc
comment states the promise: *"This is what lets a consumer depend on scheduler alone without
dragging in engine, runtime, persistence, or service."*

ADR Decision 1 requires `scheduler.Trigger.Next` to delegate to
`github.com/kartaladev/wrkflw/internal/schedcalc` — outside that subtree.

**Evidence** (throwaway `internal/schedcalc` package + one import added to
`scheduler/trigger.go`, restored from a `cp` backup afterwards):

```
$ go test -count=1 -run 'TestSchedulerTreeIsSelfContained' -v ./scheduler/
EXIT=1
=== RUN   TestSchedulerTreeIsSelfContained
    selfcontainment_guard_test.go:58: package github.com/kartaladev/wrkflw/scheduler
      imports github.com/kartaladev/wrkflw/internal/schedcalc, which is outside the
      scheduler/ subtree; the scheduler tree must stay self-contained and importable
      standalone
--- FAIL: TestSchedulerTreeIsSelfContained (0.08s)
```

The bundle never mentions this guard. Spec §4.1 justifies `internal/` purely on "layer
inversion" grounds and on a `go list -deps` observation — it argues against the wrong
constraint. The real constraint is stronger and is a *test*, so Phase 2 hits it on its first
`go test ./scheduler/...`, and Phase 2's own rule ("Do not edit a `scheduler` test to
accommodate the refactor; a required edit means behaviour changed and must be reported, not
absorbed") plus the checklist's no-diff-under-`scheduler/*_test.go` gate make the bundle
**internally impossible to execute as written**.

Note also that the obvious "keep it inside the subtree" variant does not exist: Go's
`internal` rule makes `scheduler/internal/schedcalc` importable only by `scheduler/...`, so
`definition/schedule` could never reach it.

**Why it matters**: this is the load-bearing decision of the ADR. An implementer will hit it
in Phase 2 with no adjudication recorded, and the cheapest local escape (silently editing the
guard's allowlist) weakens a shipped public-API guarantee with no ADR.

**Concrete fix — pick one and record it in the ADR:**

1. *Amend the guarantee deliberately.* Add `internal/schedcalc` to an explicit allowlist in
   `TestSchedulerTreeIsSelfContained`, and add an ADR-0176 Decision paragraph stating that the
   scheduler subtree's self-containment promise now reads "no wrkflw import except the
   pure, dependency-free `internal/schedcalc`", with the reason. The guard edit becomes an
   *audited* change, not an absorbed one. Plan Phase 2's no-test-edit rule must be narrowed to
   "no changes to `scheduler/trigger_test.go` or any behavioural scheduler test", and the
   checklist line must be reworded to name the one permitted file.
2. *Do not move the math.* `definition/schedule` grows its own predicate implementation;
   `scheduler.Trigger.Next` stays exactly as it is (zero refactor risk, zero guard breach),
   and the **cross-package soundness property test the plan already prescribes** (Phase 5.4)
   becomes the anti-drift guard instead of shared code. Cost: two copies of the calendar scan —
   the very thing spec §4.1 argues against. Mitigation: the property test is executable and
   the duplication is ~60 lines, whereas §2's rot was in a *doc comment*, which no shared
   package would have prevented.
3. *Invert the direction.* Put the math in `definition/schedule` (public, already imported by
   `runtime`) and leave `scheduler` untouched. This is option 2 with the duplication in
   `scheduler` acknowledged as frozen legacy.

**What this fix invalidates elsewhere in the bundle** (angle 7): ADR Decision 1 and its
Consequences bullet "`definition/schedule` is no longer dependency-free" (still true under
option 2 only if the cron parse moves too — under option 3 it is true, under option 2 it is
true, under option 1 it is true; but the *reason* changes in every case); spec §4.1 entirely;
spec §6's test row "`scheduler` suite passes untouched after the math moves"; spec §4.1's
"behaviour-preserving" risk framing; plan Phases 1 and 2; plan Risks bullet 1 ("The math move
is the risk concentration"), which under options 2/3 stops existing.

---

## C2 — CRITICAL: a never-due CALENDAR trigger does not "hang the instance invisibly" — it deadlocks the caller goroutine AND the whole gocron scheduler

**Attacks**: spec §1.2 ("On Postgres/SQLite — silent … the timer never fires and the
instance waits forever"); the ADR Context table row "Postgres | accepted as
`0001-01-01T00:00:00Z` | silent forever-wait"; spec §1.1's dialect table framing; the
`newScheduledTimerJob` doc comment in shipped code.

`gocron/v2 v2.22.0`'s `monthlyJob.next` **spins forever** for `Monthly(12, []int{31})`, and
`NativeScheduler.Activate` blocks in `addOrUpdateJob` waiting for a reply that never comes.

**Evidence** (`scheduler/zzz_audit_probe2_test.go`, `Activate` with a directly-built
`ScheduledJob`, exactly as `newScheduledTimerJob` produces):

```
$ go test -count=1 -v -timeout 90s -run 'TestZZZAuditActivateNeverDue' ./scheduler/
EXIT=1
PROBE trig=Every(0)             Next.ok=false Activate.err=gocron: DurationJob: time interval is 0
PROBE trig=EveryRandom(0,1m)    Next.ok=false Activate.err=gocron: DurationRandomJob: minimum and maximum durations must be greater than 0
PROBE trig=Cron(bogus)          Next.ok=false Activate.err=gocron: CronJob: crontab parse failure
PROBE trig=Daily(0)             Next.ok=false Activate.err=gocron: DailyJob: interval must be greater than 0
PROBE trig=Weekly(1,nil)        Next.ok=false Activate.err=<nil>
PROBE trig=Monthly(1,nil)       Next.ok=false Activate.err=<nil>
panic: test timed out after 1m30s
```

Stack at the timeout — the caller blocked, and gocron's single job-selection goroutine
livelocked:

```
goroutine 26 [select, 1 minutes]:
  gocron/v2.(*scheduler).addOrUpdateJob(...) scheduler.go:1015
  gocron/v2.(*scheduler).NewJob(...)         scheduler.go:840
  wrkflw/scheduler/internal/gocron.(*GocronScheduler).ScheduleJob(...) job_schedule.go:91
  wrkflw/scheduler.(*NativeScheduler).activateJob(...) scheduler.go:638
  wrkflw/scheduler.(*NativeScheduler).Activate(...)    scheduler.go:624

goroutine 106 [runnable]:
  time.Time.AddDate(..., 0, 0xc, 0)
  gocron/v2.monthlyJob.next(...)                 job.go:1473
  gocron/v2.(*scheduler).selectNewJob(...)       scheduler.go:646
```

So today, on Postgres/SQLite, a step that arms `Monthly(12,[31])` in February:

1. commits the zero-`next_run` row, then
2. reaches `processdriver.go:780`'s post-commit `driver.sched.Activate(ctx, sj)` and
   **never returns** — the `Drive`/`ApplyTrigger` goroutine (i.e. the consumer's HTTP request
   goroutine) is wedged, and
3. `selectNewJob` is the *single* serialization point for every gocron job, so every
   subsequent arm, cancel and rehydration in the whole process blocks too. The scheduler is
   dead process-wide, not just for that instance.

On **MySQL** the commit fails first, so `Activate` is never reached — MySQL is accidentally
the *safe* dialect for the calendar class, which inverts the bundle's "MySQL loud and total /
PG-SQLite silent" framing.

Two secondary facts from the same run: `Weekly(1,nil)` and `Monthly(1,nil)` are **accepted**
by gocron (`Activate.err=<nil>`), so the comment on `newScheduledTimerJob`
(`runtime/timerjob.go:66-69`) — *"Callers build j from a successfully converted trigger, so
Next reports ok; on the impossible not-ok path the zero time is stamped (the scheduler
re-validates the trigger at arm time anyway)"* — contains **three** false claims in shipped
code: `Next` does not report ok, the path is not impossible, and the scheduler does not
re-validate for those two kinds.

**Why it matters**: the design is *more* justified than the bundle argues, but the bundle
argues for it from a false premise, and the false premise hides two things the design must
handle (see C3 and C4). It also means the ADR's "pick one behaviour for all three dialects"
motivation understates the severity by an order of magnitude, and a reviewer weighing the
`StepOptions` cost against "an invisible hang" is weighing it against the wrong thing.

**Concrete fix**: rewrite spec §1.2 and the ADR Context table with the measured behaviour
(caller-goroutine deadlock + process-wide scheduler livelock for the calendar class; WARN-only
`Activate` error for the duration/cron kinds; gocron silently accepting the empty-day-set
kinds). Fix the `newScheduledTimerJob` comment in the same bundle (Delivery-Gate item 2).
Elevate the arm gate from "defence in depth / makes the dialects agree" to "the only thing
standing between a never-due calendar spec and a process-wide scheduler deadlock", and say so
in the ADR Decision.

**What this fix invalidates elsewhere** (angle 7): spec §1.2's two-failure-mode structure;
spec §5's "on Postgres/SQLite it replaces an invisible forever-wait with a loud failure" (it
replaces a *deadlock*); the ADR Consequences bullet with the same wording; spec §4.3's
characterisation of the arm gate as catching "anything that reached the runtime regardless"
(it is load-bearing, not belt-and-braces); and spec §7 Finding B's severity (see C3).

---

## C3 — CRITICAL: the invariant is defeated at REHYDRATION, and after this change a pre-existing poisoned row wedges BOOT with no supported remediation

**Attacks**: spec §4 invariant sentence ("no timer is ever armed that can never fire, and a
zero `next_run` never reaches any dialect"); spec §1.2's "(only `Pruner.PruneTimers` reclaims
it)"; spec §7 Finding B; ADR Consequences "Pre-existing zero-`next_run` rows … prevented going
forward, not migrated".

Three arm paths exist in `runtime`, and the design gates exactly one:

| arm path | entry | gated by ADR-0176? |
|---|---|---|
| fresh step arms | `timerJobsFor` → `newTimerJob` → in-tx `Save` → post-commit `Activate` | **yes** (arm gate) |
| rehydration | `jobStore.Load` → `rehydrateTrigger(a)` → `buildTimerJob` → `RehydrateTimers`/`NativeScheduler.rehydrate` → `activateJob` | **no** |
| timer-START events | `armStartTimer` → `scheduleStartTimerJob` → `sched.Schedule` | **no** (but `Schedule` has its own `Next` check — see C4) |

`rehydrateTrigger` (`runtime/timerops.go:516-521`) returns `a.Trigger` verbatim whenever the
stored trigger is recurring — and **every** never-due kind is recurring. So a pre-existing
zero-`next_run` `Monthly(12,[31])` row is handed straight to `Activate`, which per C2
**deadlocks**. Worse, `NativeScheduler.Activate` runs `s.rehydrate(impl)` on the first arm, so
the poison detonates on the first timer any instance arms after boot, not only inside the
explicit `RehydrateTimers` call.

And there is no way out, because `PruneTimers` cannot delete such a row: it filters
`trigger_kind IN (KindUnset, KindOneTime, KindExpr)` and every never-due kind is outside that
set.

**Evidence** (`internal/persistence/store/zzz_audit_probe_test.go`, SQLite, pure Go):

```
$ go test -count=1 -v -run 'TestZZZAuditPruneTimersZeroNextRun' ./internal/persistence/store/
EXIT=0
PROBE before prune: 5 armed rows
PROBE   armed timer=zz-cronbogus     kind=4 recurring=true  next_run=0001-01-01T00:00:00Z
PROBE   armed timer=zz-every0        kind=2 recurring=true  next_run=0001-01-01T00:00:00Z
PROBE   armed timer=zz-monthly12-31  kind=7 recurring=true  next_run=0001-01-01T00:00:00Z
PROBE   armed timer=zz-weekly1-nil   kind=6 recurring=true  next_run=0001-01-01T00:00:00Z
PROBE   armed timer=zz-oneshot-past  kind=1 recurring=false next_run=2026-08-11T09:00:00Z
PROBE PruneTimers(cutoff=base+24h) deleted=1
PROBE after prune: 4 armed rows remain
PROBE Stats={Armed:4 NextFireAt:0001-01-01 00:00:00 +0000 UTC}
```

So spec §1.2's parenthetical "(only `Pruner.PruneTimers` reclaims it)" is **false in the
direction that matters**: *nothing* in the product reclaims it. The `Stats.NextFireAt`
poisoning half of the same sentence is confirmed.

**Why it matters**: "prevented going forward, not migrated" reads as a cosmetic leftover. It
is actually a boot-wedging landmine with no supported removal path, on the two dialects that
*accept* the row — i.e. on Postgres, the primary. A consumer upgrading to this release with
one such row in `wrkflw_timers` gets a scheduler that deadlocks on first arm, and the release
notes would say the bug is fixed.

**Concrete fix (three parts, all cheap):**

1. **Gate the rehydration path.** In `jobStore.Load`, apply `ValidateAt(now)` (or reuse
   `strig.Next(now)`'s `ok`) to `rehydrateTrigger(a)` before `buildTimerJob`, and WARN-skip
   exactly like the existing "trigger not convertible" branch three lines below. This is a
   ~5-line change in the same style, closes the migration gap without a migration, and turns
   the landmine into a log line. Add it as **plan Phase 5.6** with its own RED (a `ListArmed`
   double returning one poisoned row ⇒ `Load` returns zero jobs and logs WARN).
2. **State the remediation.** ADR Consequences must say the row is *not* reclaimable by
   `PruneTimers` (with the `nonRecurringTriggerKinds` reason), and that the supported
   remediation until Finding B's admin sweep ships is a manual
   `DELETE FROM wrkflw_timers WHERE next_run <= '0001-01-02'`.
3. **Restate the invariant honestly.** Replace the unqualified sentence with the closed set it
   actually covers: *"After this change, the three runtime arm paths — fresh step arms,
   rehydration, and timer-start events — each refuse a trigger that cannot fire, and no
   `Save`/`UpsertJob` is issued with a zero `NextRun`."*

**What this fix invalidates elsewhere** (angle 7): spec §4's invariant sentence; spec §4.3's
three-gate table becomes a four-gate table (and the ADR Decision's numbered item 3 with it);
spec §7 Finding B is downgraded from "an opt-in admin sweep is the plausible shape" to "the
sweep is still wanted, but the boot hazard is closed here"; spec §1.2's parenthetical; the
plan's Fan-out rule and phase count; and the plan's "Source-verified facts still true at time
of writing" list, which currently repeats the `PruneTimers` claim by implication.

---

## C4 — MAJOR: "No layer rejects it" is false — the scheduler already has a never-due gate and an error for it, which the ADR reinvents in ignorance

**Attacks**: spec §3.3 ("No layer rejects `Every(0)`, `Cron("bogus")`, `Weekly(1, nil)`, or
`Monthly(12,[31])`"); ADR Decision 2's introduction of a brand-new
`schedule.ErrTriggerNeverDue`; the ADR Context's framing that the defect is an absence of any
check.

`NativeScheduler.Schedule` (`scheduler/scheduler.go:578-581`) already refuses a never-due
trigger, with an error message that is almost word-for-word the new sentinel's:

```go
next, ok := j.Trigger().Next(s.now().In(s.location()))
if !ok {
    return nil, fmt.Errorf("workflow-scheduler: job %q trigger can never fire: %w", j.ID(), ErrUnsupportedTrigger)
}
```

`processtest.MemScheduler.resolve` (`processtest/memscheduler.go:119`) carries the same check
and the same wording.

**Evidence**:

```
$ go test -count=1 -v -run 'TestZZZAuditNeverDueArm' ./scheduler/     EXIT=0
PROBE trig=Every(0)             Next.ok=false Schedule.err=workflow-scheduler: job "probe" trigger can never fire: workflow-scheduler: unsupported trigger
PROBE trig=Cron(bogus)          Next.ok=false Schedule.err=… trigger can never fire: … unsupported trigger
PROBE trig=Weekly(1,nil)        Next.ok=false Schedule.err=… trigger can never fire: … unsupported trigger
PROBE trig=Monthly(12,[31])@Feb Next.ok=false Schedule.err=… trigger can never fire: … unsupported trigger
```

The truthful premise is narrower and sharper: **the runtime's timer arms are
`ActivationManual`, so they never pass through `Schedule` at all** — they are `Save`d in-tx by
`jobStore.Save` and armed post-commit via `Activate`, which has no such check
(`activateJob` → `triggerDef` → gocron). The bug is not "nobody checks"; it is "the one
existing check sits on a path the runtime deliberately bypasses (ADR-0134 direct-Save), and
the durable write happens before the only surviving validation."

**Why it matters**: three things follow that the bundle currently gets wrong.
(a) The reader cannot tell why `armStartTimer` is safe — it *is* safe, because it goes through
`Schedule`; that is worth one sentence, and it is the reason the timer-start path needs no
new gate (only a better log). (b) A second sentinel for a concept that already has one is a
divergence risk: a consumer matching on `scheduler.ErrUnsupportedTrigger` today will not match
`schedule.ErrTriggerNeverDue`, and vice-versa. (c) The design's real shape is "move the check
that already exists to *before* the durable write", which is a much easier sell than "add
three new gates".

**Concrete fix**: rewrite spec §3.3 and the ADR Context to say the above, citing
`scheduler.go:578-581` and `memscheduler.go:119`. Either (i) keep
`schedule.ErrTriggerNeverDue` but require it to be documented as the `definition/schedule`-layer
sibling of `scheduler.ErrUnsupportedTrigger`, naming both in each other's doc comments, or
(ii) drop the new sentinel and reuse a single shared one. Add one spec sentence recording that
`armStartTimer` is already covered by `Schedule`'s check and therefore needs no gate.

**What this fix invalidates elsewhere** (angle 7): spec §3.3's whole subsection; the ADR
Context's opening ("There is no guard for a trigger that converts but can never be **due**" —
there is, on a path the runtime bypasses); spec §4.2's presentation of `ErrTriggerNeverDue` as
novel; and C3's fix-part-3 wording ("timer-start events refuse …") which should credit the
existing check rather than promise a new one.

---

## C5 — MAJOR: the arm gate's own success case leaves a permanently wedged token with a phantom timer record, and less diagnosability than today

**Attacks**: spec §4.3's arm-gate row ("WARN-log, no arm, **no row**") and its justification
("loudness belongs at the step gate"); ADR Decision 3's arm bullet; spec §5's Consequences,
which never mention this outcome.

Every gated site records engine state *before* the command reaches the runtime:
`armWaitReminder` and the deadline site append a `timerRecord` to `c.s.Timers`
(`step_nodes.go:705`, `:779`); `armBoundaries` appends a `boundaryArm` carrying `arm.TimerID`;
`intermediateCatchEventStrategy` sets `tok.State = TokenWaiting; tok.AwaitCommand = timerID`;
the event-gateway site sets `ae.TimerID`. That state is committed by the same transaction.

So when the arm gate fires, the committed instance has a parked token awaiting a timer that
does not exist anywhere — no scheduler job, and now **no durable row either**. Today the row at
least exists and is visible in the admin timer listing and in `Stats`. After this change the
residual case is *less* observable, not more. Concretely, `engine.InstanceState.HasArmedTimers()`
(added by ADR-0175, reads `s.Timers`) reports **true** for an instance with zero armed timers —
and backlog item 6b already tracks `Park.HasArmedTimers` delegating to it.

The ADR explicitly expects this path to be reachable ("catches anything that reached the
runtime regardless"), and C6 below gives a concrete reachable route (a consumer calling
`engine.Step` directly whose scheduler is not UTC).

**Why it matters**: the delivery's stated purpose is to convert an invisible hang into a loud
failure. The arm gate's success case is an invisible hang with the evidence deleted, and the
bundle does not say so anywhere.

**Concrete fix**: keep the WARN-skip (erroring post-commit is genuinely not available), but
(a) state the residual outcome explicitly in spec §5 and the ADR Consequences — "the arm gate
leaves the instance parked on a phantom `timerRecord`; it is a wedge, and it is deliberately
quieter than an incident because the step gate is expected to make it unreachable"; (b) raise
the log to ERROR rather than WARN, with `def_id`, `def_version`, `node_id` and the trigger kind,
since unlike the sibling unconvertible-trigger branch this one indicates a *gate escape*, not
an expected shutdown race; (c) file a backlog item pairing this with 6b — `HasArmedTimers()`
over-reporting after an arm-gate skip.

**What this fix invalidates elsewhere** (angle 7): spec §4.3's "It logs rather than errors: by
the time the runtime derives side effects the `StepResult` is already computed, and the step
gate is where loudness belongs" (still true as a *mechanism* argument, no longer sufficient as
a *severity* argument); the ADR's "Not closed here" bullet about making the skip visible as an
incident, which should now cite this residual rather than describe it as speculative; and plan
Phase 5.2, whose brief says only "WARN-log and `continue`, mirroring the unconvertible-trigger
branch above it".

---

## C6 — MAJOR: `SchedulingLocation` does not eliminate both error directions; it defaults to the wrong answer for the direct-`engine.Step` consumer, and cannot cover the FixedZone/cron case at all

**Attacks**: spec §4.4's closing claim "This eliminates both error directions: a step is never
failed for a trigger that would have armed fine, and never passed for one that will not"; the
ADR Decision 4 paragraph carrying the same claim.

Two independent counterexamples, both executed.

**(a) A consumer calling `engine.Step` directly gets the false-rejection direction by
default.** The field is populated only by `runtime`. Spec §5 presents "nil → UTC preserves
current behaviour" as pure upside; but for a consumer who drives `engine.Step` themselves and
arms on a scheduler configured `WithLocation(Asia/Jakarta)`, the engine now decides in UTC
while the runtime arms in +07:00 — precisely the month-boundary disagreement the field exists
to prevent, silently, with the harmful polarity the spec calls "strictly worse". This is not
hypothetical: `engine.Step` is a module-root public API and the library-first property makes
direct use a first-class path.

**(b) The cron/FixedZone case cannot be fixed by any location value.** ADR-0136 records that
gocron resolves cron by zone *name*, so a `time.FixedZone` scheduler cannot arm a cron
trigger — but `robfig/cron` computes `Next` in a FixedZone happily, so `ValidateAt` returns
"schedulable" for a trigger the live scheduler will refuse.

```
$ go test -count=1 -v -run 'TestZZZAuditFixedZoneCron' ./scheduler/     EXIT=0
PROBE scheduler.Location()=plusThree (IANA name? "plusThree")
PROBE Cron.Next in FixedZone: ok=true next=2026-02-11T09:00:00+03:00  (what ValidateAt sees)
PROBE live Schedule(Cron) under FixedZone err=gocron: CronJob: crontab parse failure
```

`NativeScheduler.Location()` returns exactly this FixedZone (`scheduler.go:319-330`), so
`schedulingLocation()` faithfully reports a location under which the engine's verdict and the
scheduler's armability disagree. The mechanism is pre-existing; the ADR's *claim* is what
breaks.

**Why it matters**: the field is the delivery's only public API change and its whole
justification is that sentence. An over-claimed elimination invites the next reader to trust
the verdict as authoritative.

**Concrete fix**: replace the claim with the bounded one it can support — *"For a step driven
by `runtime.ProcessDriver`, the engine's anchor is the same instant, in the same location, that
the runtime will use to compute `NextRun`, so the two cannot disagree at a period boundary.
It does not make the verdict a prediction of armability: a `Cron` trigger under a non-IANA
`time.FixedZone` scheduler location resolves fine here and is still refused by gocron
(ADR-0136)."* Then add, in the same paragraph, the direct-`engine.Step` caveat and its
mitigation: document on the field that a consumer arming through a non-UTC scheduler **must**
set it, and add a testable example (Golang rule #6) showing that wiring.

**What this fix invalidates elsewhere** (angle 7): spec §4.4's "⚠ Direction of harm if this
were skipped" paragraph, whose asymmetry argument now cuts *against* the nil-means-UTC default
for direct consumers; spec §5's "`StepOptions` gains a field. Additive; the zero value (nil →
UTC) preserves current behaviour for a consumer calling `engine.Step` directly" — true of the
*value* but no longer a complete statement of the consequence; the ADR Decision 4 paragraph;
and plan Phase 4b.4's mutation-3 note, which should now record that mutation 3 tests only the
runtime-populated path.

---

## C7 — MAJOR: the build gate cannot reach three of the five enumerated fields by the mechanism the spec names, because `definition/model` must not import `definition/event`

**Attacks**: spec §4.3's "reached via the accessors `model.DeadlineOf` / `model.WaitActionOf`
**and via the concrete event types**"; plan Phase 4a.3–4a.4.

`definition/model` deliberately never imports its leaf packages — `definition/model/accessors.go`
says so and implements every accessor as a type assertion on an **unexported carrier method
declared in package `model`** (`deadline()`, `waitAction()`, `retry()`). `definition/event`
imports `model` (it embeds `model.WaitFields`), so the reverse import is a cycle.

Consequently `model.Validate` can reach `WaitFields.DeadlineTimer` and `WaitFields.WaitEvery`
(the two accessors exist), but it **cannot** reach `event.StartEvent.Timer`,
`event.IntermediateCatchEvent.Timer`, or `event.BoundaryEvent.Timer` "via the concrete event
types". Nor can the obvious workaround work: a new unexported carrier method defined in
package `event` cannot satisfy an interface declared in package `model` — Go qualifies
unexported method names by package.

The only mechanisms that do work are (i) `toWire(n).TimerTrigger` +
`model.ReadTrigger(w.TimerTrigger, w.TimerDuration, …)`, which `validateStructure` already
uses for other rules (`validate.go:521`, `:531`, `:545`), or (ii) a new **exported** carrier
method on the three event types plus a `model.TimerOf(n Node)` accessor.

Two adjacent traps the plan should name:

- The rule must live in **`validateStructure`**, not in `Validate`. `Validate` only wraps
  `validateStructure` (`validate.go:260-269`), and `validateStructure` is what recurses into
  nested sub-process definitions (`:550`). Putting the gate in `Validate` silently exempts
  every sub-process — the very place event sub-process timers live.
- Via `toWire`, the legacy flat `TimerDuration` string decodes as `AfterExpr`/`EveryExpr`
  (`trigger_wire.go:60-70`), i.e. an expr kind, which `Validate()` must pass by design. That
  is correct but should be asserted, or a future reader will "fix" it.

**Why it matters**: Phase 4a's brief tells an implementer to write one RED case per
spec-bearing field and then reach them "via the concrete event types". That does not compile.
The likely improvisation is to gate only the two accessor-reachable fields, shipping a build
gate that misses start, catch and boundary timers — three of the five, and the three that
matter most for the timer-start path.

**Concrete fix**: replace spec §4.3's mechanism sentence with the two workable options above,
pick one, and rewrite plan Phase 4a to (a) implement inside `validateStructure`, (b) name the
read mechanism explicitly, (c) add a RED case for a never-due timer **inside a nested
sub-process definition**, and (d) assert that a flat legacy `TimerDuration` still builds.

**What this fix invalidates elsewhere** (angle 7): spec §4.3's field enumeration paragraph;
plan Phase 4a steps 1–4; the plan Risks bullet "The enumeration in spec §4.3 may already be
incomplete", which is about *count* and misses that the *access path* is also wrong.

---

## C8 — MAJOR: the step gate's blast radius is bigger than the ADR's Consequences describe — a never-due ESP timer makes the whole instance unstartable

**Attacks**: ADR Consequences "A step reaching a node whose resolved trigger can never fire
now fails with `ErrTriggerNeverDue`"; spec §5's identical bullet.

Two of the six gated sites are not "the node the token reached":

- `armEventTriggeredSubprocesses` is called from `handleStartInstance`
  (`step_triggers.go:45-48`) and its error is returned as the **`StartInstance` error**
  (`return StepResult{}, espErr`). So one never-due timer on any top-level event sub-process
  makes **every** `StartInstance` for that definition fail — no instance can be created at all.
  It is also re-run on ESP re-arm (`step_compensation.go:1166`, `step_nodes.go:672`).
- `armBoundaries` is called on host entry (`step_nodes.go:57`, `:104`, `:810`), so a never-due
  boundary timer fails the **host activity's** step, and the event-gateway site fails the whole
  gateway even when its other (signal/message) arms are fine.

Combined with the anchor-dependent class this is a seasonal time bomb. Re-derived:

```
$ go test -count=1 -v -run 'TestZZZAuditAnchorTrap' ./scheduler/     EXIT=0
PROBE Monthly(12,[29]) never-due anchors (2026): 0/12 []
PROBE Monthly(12,[30]) never-due anchors (2026): 1/12 [February]
PROBE Monthly(12,[31]) never-due anchors (2026): 5/12 [February April June September November]
PROBE Monthly(1,[30]) @Feb ok=true next=2026-03-30T00:00:00Z
PROBE Monthly(1,[31]) @Feb ok=true next=2026-03-31T00:00:00Z
```

A definition carrying `Monthly(12,[31])` on an event sub-process passes `Build()` (the build
gate is sound-only, correctly), starts instances fine for seven months of the year, and
refuses to start **any** instance during February, April, June, September and November.

**Why it matters**: this is the answer to the audit brief's angle-3 question, and the bundle
does not contain it. It is the strongest argument that "never-due is an error always" needs a
stated blast radius, and it is exactly the kind of thing an operator must be told before
upgrading.

**Concrete fix**: add to spec §5 and the ADR Consequences a named enumeration of what fails at
each of the six sites — *instance creation* (ESP at start), *host activity entry* (boundary),
*gateway entry* (event gateway), *node entry* (deadline / reminder / intermediate catch) — and
the 5-of-12 measurement above as the concrete worked example. Then add a plan RED for the ESP
case specifically (Phase 4b currently prescribes deadline, wait-reminder and boundary only —
it omits the two widest-radius sites, ESP and event gateway).

**What this fix invalidates elsewhere** (angle 7): spec §2.1's "It is therefore a real MySQL
step-loss in ordinary use" understates it (it is also a Postgres deadlock per C2 and an
instance-creation refusal here); plan Phase 4b.2's three-site test list; and spec §6's test
table, which mirrors that list.

---

## C9 — MINOR: the location cannot reach two of the six gate sites without a signature change the plan does not mention

**Attacks**: plan Phase 4b.5 ("Each already has an anchor in scope (`stepCtx.at`, or the `at`
parameter)").

True of the *anchor*; false of the *location*. Four sites run inside a `stepCtx` and can read
`c.pol` — so `stepPolicy` needs a new field, exactly as `stallAfter` was added for ADR-0175.
But `armBoundaries(def, s, hostTokenID, hostNode, at, eval)` and
`armEventTriggeredSubprocesses(def, s, enclosingScopeID, at, eval)` take a bare
`ConditionEvaluator`, not `pol` — so both need a parameter added, and both have callers that
hold only `pol.eval` (`step_compensation.go:1166`) or only `opt` (`step_triggers.go:45`).

**Concrete fix**: Phase 4b gains an explicit step 0: "add `loc *time.Location` to
`stepPolicy` and `resolvePolicy`; change `armBoundaries` and
`armEventTriggeredSubprocesses` to take `pol stepPolicy` instead of `eval ConditionEvaluator`
(3 call sites each: `step_nodes.go:57`, `:104`, `:810` for the former; `step_triggers.go:45`,
`step_compensation.go:1166`, `step_nodes.go:672` for the latter — and the first of those holds
only `opt`, so it needs `resolvePolicy(opt)`)". Naming it prevents an implementer from threading a second bare parameter
and re-creating the hand-threaded pair `stepPolicy` was introduced to kill.

**What this fix invalidates elsewhere**: nothing else in the bundle depends on it; it is
additive detail.

---

## C10 — MINOR: the soundness-property test's stated home rests on a false uniqueness claim

**Attacks**: spec §6's "`runtime` (only package importing both)"; plan Phase 5.4's
"`runtime` is the only package importing both `definition/schedule` and `scheduler`".

**Evidence** (direct imports, `go list`):

```
github.com/kartaladev/wrkflw/runtime
github.com/kartaladev/wrkflw/runtime/internal/runtimetest
github.com/kartaladev/wrkflw/examples/scenarios/{boundary_action,catch_event_reminder,
  event_based_gateway,inwait_reminder,timer_boundary,timer_durability,usertask_deadline}
```

Nine packages, not one. The *real* reason the test must live in `runtime` is that
`convertTrigger` is unexported there — which also means the file must be an in-package
(`package runtime`) test, and that `./runtime/...` is **not** container-free (memory: the
Docker-free subset is `engine`, `runtime/{calllink,signal,task}`, `service`, `processtest`,
`transport/http`), so a purely computational property test lands behind Docker.

**Concrete fix**: restate the reason as "`convertTrigger` is unexported in `runtime`", note the
in-package test-file requirement (and `head -1` the target file — `runtime` mixes
`package runtime` and `package runtime_test`), and consider exporting a
`runtime.ConvertTriggerForTest` via `export_test.go` (the package already has one) so the
property test can be black-box, or accept the Docker dependency explicitly in the brief.

---

## C11 — MINOR: two quantifier/enumeration defects in the bundle's own text

1. Spec §3.1: *"Every call site guards with `if !timerSpec.IsZero()` … a predicate that
   rejects it breaks six working paths."* The six gated sites do guard (verified by `grep -n`:
   `step_boundaries.go:51`, `step_eventsubprocess.go:90`, `step_nodes.go:695`, `:770`, `:829`,
   `:986`). But the sentence is a recap over a *closed set* and should name it, per Premise
   Discipline's "prefer naming a closed set over counting it".
2. Spec §4.3's arm-gate row says the gate produces "**no row**". Precisely: no *new* row. The
   pre-existing row for the same `(instance_id, timer_id)` — there is none for a fresh arm, but
   `UpsertJob` is an upsert and the retry path re-uses timer ids only via fresh mints — so the
   claim holds; it is worth one clarifying clause rather than leaving a reader to check.

Also, for the record, three bundle claims I attacked and **could not** break:

- **Eight `ScheduleTimer{}` sites, all in `engine/`, six gated, two building `AfterDuration`.**
  Re-derived independently: `engine/step_boundaries.go:54`, `step_compensation.go:493`,
  `step_triggers.go:328`, `step_eventsubprocess.go:97`, `step_nodes.go:699`, `:772`, `:831`,
  `:988`. Both ungated sites do build `schedule.AfterDuration` (confirmed by reading both).
- **`StepOptions` is safely additive.** Adding the field and running `go vet ./...` (which
  compiles every test file, Docker-only ones included) exits 0, and no test compares a
  `StepOptions` by equality. Probe applied and restored.
- **The scan cost is not a new hot-path problem.** `Trigger.Next`, `-benchtime=200x`:
  `After_1h` 9.8 ns, `Every_1h` 7.5 ns, `Cron_valid` 1390 ns, `Daily_1` 220 ns,
  `Monthly_1_day15` 396 ns, `Weekly_1_nil` (never-due) 80 ns,
  `Monthly_12_day31` (never-due) **709 µs**. A *valid* calendar trigger exits the scan within
  its first matching period, so the step gate adds sub-microsecond cost per gated site; the
  1830×interval scan is paid only by the never-due case, exactly once, before the step fails.
  Spec §5's "the gates make this rare, not free" is accurate and the design does **not** put a
  21,960-iteration loop on a hot path.
- **`engine/purity_test.go` really is non-recursive** (`os.ReadDir` over `.` and
  `../definition`, `e.IsDir()` skipped), so `definition/schedule` and `internal/schedcalc` are
  uncovered by it — plan Phase 1.3's warning is correct, and `deniedEngineImports` does not
  ban `internal/` generally (only `/internal/persistence`).
- **`definition/schedule` is dependency-free today**: `go list -deps ./definition/schedule`
  shows stdlib only. The stated cost of gaining `robfig/cron` transitively is real and
  correctly described.
- **No existing test or example uses a statically never-due spec**, so the build gate should
  not break the suite (`grep` over `schedule.{Cron,Daily,Weekly,Monthly,EveryRandom,Every}(`
  outside `definition/schedule/`: every occurrence is a valid spec).

---

## Ranked summary

| # | Severity | One line |
|---|---|---|
| C1 | **Critical** | ADR Decision 1 breaks `TestSchedulerTreeIsSelfContained`, and the plan forbids editing it — bundle not executable as written. |
| C2 | **Critical** | A never-due calendar trigger deadlocks the caller goroutine and the whole gocron scheduler; the bundle calls it a silent wait. |
| C3 | **Critical** | Rehydration bypasses every gate, so a legacy zero-`next_run` row wedges boot after this change — and `PruneTimers` provably cannot remove it. |
| C4 | Major | "No layer rejects it" is false: `scheduler.Schedule` already has this check and this error message; the bug is that manual arms bypass it. |
| C5 | Major | The arm gate's success case leaves a phantom `timerRecord` and *less* diagnosability than today; unstated. |
| C6 | Major | `SchedulingLocation` does not eliminate both error directions: nil-means-UTC harms direct `engine.Step` consumers, and cron-under-FixedZone is unfixable by any location. |
| C7 | Major | The build gate cannot read three of the five enumerated fields the way the spec says; and it must live in `validateStructure` or it exempts sub-processes. |
| C8 | Major | Blast radius understated: a never-due ESP timer makes the whole definition unstartable, for 5 of 12 anchor months. |
| C9 | Minor | The location cannot reach `armBoundaries` / `armEventTriggeredSubprocesses` without a signature change the plan omits. |
| C10 | Minor | "`runtime` is the only package importing both" is false (nine packages); the real reason is `convertTrigger` being unexported. |
| C11 | Minor | Two recap/quantifier clauses to tighten. |

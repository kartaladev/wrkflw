# ADR-0176 bundle audit — Lens A (EXECUTION)

- Date: 2026-08-13
- Bundle audited: `docs/specs/2026-08-13-never-due-timer-triggers.md`,
  `docs/adr/0176-reject-never-due-timer-triggers.md`,
  `docs/plans/2026-08-13-never-due-timer-triggers.md`
- Bundle obtained by `git merge --ff-only feat/never-due-timer-triggers` into the audit
  worktree (worktree was created at base `e04bd670`, i.e. **the bundle was absent** — step 0
  of the brief fired exactly as designed).
- Method: every finding below was **executed**. Probe files were `scheduler/zzz_audit_*.go`,
  `runtime/zzz_audit_*.go`, `internal/persistence/store/zzz_audit_*.go`,
  `definition/model/zzz_audit_*.go`, all deleted after capture.

---

## C1 (CRITICAL) — A parseable cron that can never match yields `ok=TRUE` with a ZERO instant, so all three gates miss it and the bundle's headline invariant is FALSE

**Claim attacked.** Three, jointly:

- Spec §4, the invariant: *"no timer is ever armed that can never fire, and a zero
  `next_run` never reaches any dialect."*
- Spec §4.3: *"The **arm** gate … is the reason no zero `next_run` can exist after this
  change."*
- Spec §2's table row: `KindCron` | never due iff *"`cron.ParseStandard` errors, incl. empty"*.
  (Same sentence in ADR Decision 2: *"unparseable or empty `Cron`"*.)

**Command.**

```
go test -count=1 -v -run TestZZZAuditCronZeroInstant ./scheduler/
```

probe body: `scheduler.Cron(expr).Next(2026-08-13T10:00:00Z)` for a set of expressions.

**REAL output** (EXIT=0):

```
PROBE Cron("0 0 30 2 *") ok=true  zeroInstant=true  next=0001-01-01T00:00:00Z
PROBE Cron("0 0 31 2 *") ok=true  zeroInstant=true  next=0001-01-01T00:00:00Z
PROBE Cron("0 0 31 4 *") ok=true  zeroInstant=true  next=0001-01-01T00:00:00Z
PROBE Cron("0 0 30 2 1") ok=true  zeroInstant=false next=2027-02-01T00:00:00Z
PROBE Cron("@every 0s" ) ok=true  zeroInstant=false next=2026-08-13T10:00:01Z
PROBE Cron("0 0 29 2 *") ok=true  zeroInstant=false next=2028-02-29T00:00:00Z
PROBE Cron("* * * * *" ) ok=true  zeroInstant=false next=2026-08-13T10:01:00Z
```

**Why the claims are wrong.** `scheduler/trigger.go`'s cron branch is

```go
case triggerCron:
    sched, err := cron.ParseStandard(t.cron)
    if err != nil {
        return time.Time{}, false
    }
    return sched.Next(after), true      // <-- ok=true UNCONDITIONALLY
```

`robfig/cron`'s `SpecSchedule.Next` gives up after a 5-year forward search and returns the
**zero `time.Time`** — it has no error channel. `Trigger.Next` returns that zero instant with
**`ok = true`**. So:

- `Validate()` cannot catch it — the expression *parses*; there is nothing static to reject.
- `ValidateAt(anchor)` cannot catch it — it delegates to the same computation, which reports
  `ok = true`.
- **the arm gate cannot catch it** — the arm gate as specified refuses an arm when
  `Next` reports `!ok`; here `ok == true` and `nextRun = next.UTC()` = the zero time.

Therefore after ADR-0176 ships exactly as written, `runtime/timerops.go:156-160` still
writes a **zero `next_run`**, and **blocker 2 is not closed**: a definition carrying
`Cron("0 0 30 2 *")` still fails on MySQL with `Error 1292` at commit and still silently
hangs on Postgres/SQLite. This is not a corner of bad data either — `0 0 31 4 *`
("31st of April") is the kind of thing a human writes by mistake, and `ParseStandard`
accepts it because 31 ∈ [1,31] and 4 ∈ [1,12] are each individually in range.

Note the `0 0 30 2 1` row: adding a day-of-week field makes robfig OR the dom/dow filters,
so it matches again. The hazard is specifically an impossible **dom+month** pair with `*`
(or `?`) day-of-week.

**Concrete fix.** The gates must key on *"produced a usable instant"*, not on `ok`:

1. `internal/schedcalc`'s cron path must treat a zero result as not-due:
   ```go
   nxt := sched.Next(after)
   if nxt.IsZero() {
       return time.Time{}, false
   }
   return nxt, true
   ```
   ⚠ This **changes `scheduler.Trigger.Next`'s observable behaviour** for these expressions
   (`(zero, true)` → `(zero, false)`), so Phase 2's "behaviour-preserving, the `scheduler`
   suite is the guard, do not edit a `scheduler` test" instruction is no longer accurate for
   this one case and the ADR's "Unchanged: `scheduler.Trigger.Next`'s observable behaviour"
   must be corrected. Adjudicate whether the cron fix rides in this bundle (recommended —
   without it the bundle does not achieve its own invariant) or becomes ADR-0177.
2. **Independently**, the arm gate must be defended in depth against *any* zero instant, not
   only against `!ok`:
   ```go
   next, ok := strig.Next(now)
   if !ok || next.IsZero() {
       // WARN, no arm, no row
       continue
   }
   ```
   This is the only formulation that makes §4.3's sentence true, and it is cheap.
3. Spec §2's `KindCron` row should read: *"`cron.ParseStandard` errors (incl. empty), **or
   the expression parses but matches no instant within `robfig/cron`'s 5-year forward search
   — measured: `Cron("0 0 30 2 *").Next(t)` returns `(0001-01-01T00:00:00Z, true)`, i.e.
   ok=TRUE with a zero instant**"*. The ADR's Decision-2 bullet needs the same correction.
4. Spec §4.2's `ValidateAt` doc — *"complete for that anchor"* — is false while the cron
   branch reports ok=true for a never-matching expression. Either fix the cron branch (1) or
   drop the completeness claim.

---

## C2 (CRITICAL) — The build gate's prescribed MECHANISM is unimplementable: `definition/model` cannot see `definition/event`'s three `Timer` fields

**Claim attacked.** Spec §4.3: the five definition-carried spec fields are *"reached via the
accessors `model.DeadlineOf` / `model.WaitActionOf` **and via the concrete event types**"*, and
plan Phase 4a.4: *"`model.Validate` applies `spec.Validate()` to every such spec"*.

**Command.**

```
go list -f '{{.ImportPath}} imports: {{join .Imports " "}}' ./definition/event ./definition/model
grep -n 'func ' definition/model/accessors.go
```

**REAL output.**

```
github.com/kartaladev/wrkflw/definition/event imports: .../definition/model .../definition/model/validate .../definition/schedule
github.com/kartaladev/wrkflw/definition/model imports: bytes context encoding/json errors fmt .../action .../definition/flow .../definition/model/validate .../definition/schedule gopkg.in/yaml.v3 io math slices sort strconv strings time unicode
```

`definition/model/accessors.go` exports exactly `RetryPolicyOf`, `DeadlineOf`, `WaitActionOf`,
`CompletionActionOf`, `CompensateActionOf`, `CancelActionOf`, `RecoveryFlowOf`, `ActionOf`.
**None** exposes an event node's `Timer`.

**Why the claim is wrong.** `definition/event` imports `definition/model`, so `model` **cannot**
import `event` — "via the concrete event types" describes an import cycle.
`DeadlineOf`/`WaitActionOf` reach only `WaitFields.DeadlineTimer` and `WaitFields.WaitEvery`;
`event.StartEvent.Timer`, `event.IntermediateCatchEvent.Timer` and `event.BoundaryEvent.Timer`
are unreachable by the prescribed mechanism. Those three are the *primary* never-due vectors,
so a literal implementation of Phase 4a covers only the half of the gate that (see C3) can
never reject anything.

**A mechanism does exist** and the audit verified it — the **wire** form. `NodeWire` carries
`TimerTrigger *TriggerWire` (`definition/model/node_wire.go:42`) plus `DeadlineTrigger` and
`WaitTrigger`; `Validate` already uses `w := toWire(s)` for start events, and
`ReadTrigger(w.TimerTrigger, w.TimerDuration, false)` reconstructs the spec. Executed proof
that the cron survives into the wire form:

```
PROBE Build() Cron("bogus") at event.StartEvent.Timer              err=<nil>
PROBE   wire exposes "bogus"=true  via key: timer_trigger=true
PROBE Build() Cron("bogus") at event.IntermediateCatchEvent.Timer  err=<nil>
PROBE   wire exposes "bogus"=true  via key: timer_trigger=true
PROBE Build() Cron("bogus") at event.BoundaryEvent.Timer           err=<nil>
PROBE   wire exposes "bogus"=true  via key: timer_trigger=true
PROBE Build() Cron("bogus") at WaitFields.WaitEvery (activity)     err=<nil>
PROBE   wire exposes "bogus"=true  via key: wait_trigger=true
```

**Concrete fix.** Replace §4.3's mechanism sentence with:

> reached from `definition/model` through the **wire** projection — `toWire(n)` yields a
> `NodeWire` carrying `TimerTrigger`, `DeadlineTrigger` and `WaitTrigger`
> (`definition/model/node_wire.go:42-44`), each reconstructed with `ReadTrigger`. ⚠ `model`
> **cannot** type-assert `event.StartEvent`/`IntermediateCatchEvent`/`BoundaryEvent`:
> `definition/event` imports `definition/model`, so the dependency runs one way only
> (verified with `go list`). The gate must also return nil for the **legacy flat** string
> fields (`TimerDuration`, `DeadlineDuration`, `WaitEvery`), which `ReadTrigger` decodes as
> *expression* triggers and which are therefore undecidable.

Plan Phase 4a gains a step: *"walk nodes via `toWire`, not via concrete event types"*.

---

## C3 (CRITICAL) — Two prescribed tests are UNCONSTRUCTABLE: `Build()` already rejects every recurring `DeadlineTimer`

**Claims attacked.**

- Plan Phase 4a.3: *"**RED** — one case per spec-bearing field, so a gate that walks only
  *some* fields fails."*
- Plan Phase 4b.2 / spec §6: *"`Step` fails with `ErrTriggerNeverDue` when an `EveryExpr`
  resolves to `0`, at the **deadline**, reminder and boundary sites."*

**Command.** `go test -count=1 -v -run TestZZZAuditBuildAcceptsNeverDue ./definition/model/`

**REAL output** (the two deadline rows):

```
PROBE Build() Cron("bogus") at WaitFields.DeadlineTimer (activity)    err=workflow-definition: deadline trigger must be one-shot: node "ut"
PROBE Build() Cron("bogus") at WaitFields.DeadlineTimer (catch event) err=workflow-definition: deadline trigger must be one-shot: node "c"
```

**Why the claims are wrong.** `ErrDeadlineTriggerRecurring`
(`definition/model/validate.go:172-177`) already rejects any recurring spec on
`DeadlineTimer`, and `schedule.TriggerSpec.Recurring()` (`definition/schedule/trigger.go:101`)
returns **true for `KindEveryExpr`** as well as `KindDuration`/`KindCron`/calendar. So a built
definition's `DeadlineTimer` can only be `KindUnset`, `KindOneTime` or `KindExpr` — and §2
establishes `KindOneTime` is **never** never-due, while `AfterExpr → 0` is explicitly safe
(§3.2). Consequences:

1. **The build gate is dead code at `DeadlineTimer`.** `spec.Validate()` there can never reject
   anything reachable through `Build()`. Phase 4a.3's "one case per spec-bearing field" test
   therefore **cannot be written for that field** — every candidate spec is either one-shot
   (nothing to reject) or recurring (rejected earlier, by a different error). This is precisely
   the "test that cannot fail" defect the repo has shipped repeatedly.
2. **Phase 4b.2's deadline case is not reachable from a *built* definition.** It is still
   writable in `engine` (engine/runtime tests construct `*model.ProcessDefinition` literals
   directly — e.g. `runtime/timer_txflow_test.go`'s `twoTimerDef()` — bypassing `Validate`), but
   the plan must say so, or the implementer will try
   `activity.WithWaitDeadline(EveryExpr(...))` and be blocked by an unrelated error.

**Concrete fix.**

- Spec §4.3, after the five-field list: *"⚠ `DeadlineTimer` is **already** constrained to
  one-shot specs by `ErrDeadlineTriggerRecurring` (`validate.go:172`) — and `Recurring()` is
  true for `KindEveryExpr` too — so `Validate()` there is defence-in-depth against
  hand-constructed / wire-decoded definitions only and can reject nothing `Build()` admits.
  Measured: `Build()` with `Cron("bogus")` on a deadline returns `workflow-definition: deadline
  trigger must be one-shot`."*
- Plan Phase 4a.3: restrict "one case per spec-bearing field" to the **four** fields where a
  never-due spec is buildable (`StartEvent.Timer`, `IntermediateCatchEvent.Timer`,
  `BoundaryEvent.Timer`, `WaitFields.WaitEvery`), and cover `DeadlineTimer` with a
  **hand-constructed** definition instead.
- Plan Phase 4b.2: append *"the deadline case must use a hand-constructed
  `*model.ProcessDefinition` — `Build()` rejects a recurring/`EveryExpr` deadline before the
  step gate can see it."*

---

## M1 (MAJOR) — §2's table says "never due **iff**", but §2.1 proves the conditions are not iff

**Claim attacked.** Spec §2's table column header *"never due iff"*, specifically the
`KindMonthly` row *"`interval == 0`, or no day in `1..31`"* and the `KindWeekly` row.

**Command.** `go test -count=1 -v -run TestZZZAuditAnchorDependence ./scheduler/`

**REAL output.**

```
PROBE Monthly(12,[29]) NEVER-DUE anchors=[]  due anchors=[Jan Feb Mar Apr May Jun Jul Aug Sep Oct Nov Dec]
PROBE Monthly(12,[30]) NEVER-DUE anchors=[Feb]  due anchors=[Jan Mar Apr May Jun Jul Aug Sep Oct Nov Dec]
PROBE Monthly(12,[31]) NEVER-DUE anchors=[Feb Apr Jun Sep Nov]  due anchors=[Jan Mar May Jul Aug Oct Dec]
```

**Why the claim is wrong.** `Monthly(12,[31])` has `interval != 0` and `31 ∈ 1..31`, yet is
never-due at **five** anchor months. The table's conditions are **sufficient, not necessary** —
the header must not say *iff*. The document contradicts itself: §2 says *iff*, §2.1 says the
static predicate *"can only be **sound**, never complete"*. Plan Phase 3.1 tells the implementer
to build the `Validate` table *"from spec §2's table"*, and §2.1 warns that a
`Validate() == nil ⟺ Next ok` test *"would itself be wrong"* — the mislabelled column is exactly
what would produce that wrong test.

**Concrete fix.** Rename the column **"never due WHENEVER (sufficient, not necessary)"** and add
a footnote: *"⚠ Sufficient conditions only. `Monthly(12,[31])` satisfies none of them and is
still never-due at a February, April, June, September or November anchor — see §2.1. `Validate`
implements this column; `ValidateAt` implements the real predicate."*

---

## M2 (MAJOR) — §2.1's anchor-dependent class is presented as one curiosity; measured it is a clean, wider, statable rule

**Claim attacked.** Spec §2.1 / ADR Context 2, which give three data points
(`Monthly(12,[31])` at 2026-02, 2026-04, 2026-08) and the explanation *"February never has a
31st"*.

**REAL output.** As M1, plus:

```
PROBE Monthly( 1,[31]) anchor=2026-02 ok=true  next=2026-03-31T00:00:00Z
PROBE Monthly( 2,[31]) anchor=2026-02 ok=true  next=2026-08-31T00:00:00Z
... intervals 1..11 and 13 all ok=true ...
PROBE Monthly(12,[31]) anchor=2026-02 ok=false
PROBE Monthly(13,[31]) anchor=2026-02 ok=true  next=2027-03-31T00:00:00Z
PROBE Weekly(1..5,[Sun]) never-due anchor weekdays=[]   (all five intervals)
PROBE Daily(1..5)        never-due anchor hours=[]      (all five intervals)
PROBE Monthly(  48,[29]) anchor=2026-02 ok=false
PROBE Monthly( 480,[29]) anchor=2026-02 ok=false
PROBE Monthly(1200,[29]) anchor=2026-02 ok=false
```

**Why it matters.** The class is characterisable and the spec would be far more useful stating
it: `monthIndex % interval != 0` skips every month not congruent to the anchor month mod
`interval`, so **when `interval` is a multiple of 12 the fire month is pinned to the anchor
month** — and then never-due ⟺ that one month never contains a requested day. Corollaries the
audit measured and the bundle does not state:

- `Monthly(12,[30])` is never-due at a **February** anchor (one month); `Monthly(12,[31])` at
  **five**. Only `interval ≡ 0 (mod 12)` pins the month; intervals 1–11 and 13 all rotate it
  and are schedulable.
- The class extends to **leap-year pinning**: `Monthly(48,[29])` (every 4 years) anchored
  February 2026 is never-due because `2026 + 4k ≡ 2 (mod 4)` is never a leap year. Same for
  interval 480 and 1200. This is a *second* mechanism the bundle never mentions.
- **`Weekly` and `Daily` show no anchor dependence at any interval probed (1–5).** A useful
  negative result: it scopes the step gate's value to the monthly/cron kinds and means no
  weekly/daily fixture is needed for §2.1.

**Also confirmed, not refuted** (the brief asked whether the bound merely *makes* it look
never-due):

```
PROBE Monthly(12,[31]) Feb anchor: never-due across 20x50y re-anchors = true (bound-artifact=false)
```

Re-anchored at 2026-02, 2076-02 … 2976-02 — every one reports `!ok`. Sound, because
`interval == 12` pins the month to February and no February has 31 days. The audit found **no**
case where a larger bound would have found a fire, i.e. **no false-rejection class exists for
`Monthly`**. So: the class is **wider in extent than stated, but the spec's chosen example is
correct and not bound-limited.**

**Concrete fix.** Replace §2.1's explanation with the congruence rule, name both mechanisms
(month pinning and leap-year pinning), record the `Weekly`/`Daily` negative results, and add the
re-anchor probe as the evidence the class is genuine rather than bound-limited.

---

## M3 (MAJOR) — §5's "scan-cost smell" is stated as a bounded constant; it is unbounded in `interval`, and this ADR moves it onto the ENGINE step path

**Claim attacked.** Spec §5 / ADR Consequences: *"a never-due calendar spec costs a full
`366*5*interval`-iteration scan per `Next` call (21,960 iterations at `interval == 12`) before
reporting false. The gates make this rare, not free."*

**Command.** `go test -count=1 -v -timeout 300s -run TestZZZAuditLargeInterval ./scheduler/`

**REAL output.**

```
PROBE one exhausting scan interval=12 (bound=21960) took 1.032292ms => 47.0 ns/iter
PROBE Monthly(MaxUint64,[31]) ok=false next=0001-01-01T00:00:00Z int(interval)=-1 bound=-1830 elapsed=15.625µs
PROBE Monthly(100,[31])   ok=true bound=183000    elapsed=195.333µs
PROBE Monthly(1000,[31])  ok=true bound=1830000   elapsed=2.4495ms
PROBE Monthly(10000,[31]) ok=true bound=18300000  elapsed=23.37025ms
PROBE PROJECTION interval=MaxUint32 (4294967295): bound=7859790149850 iters => 102.6 hours of CPU in ONE Next() call
```

Also measured: 100 never-due `Monthly(12,[31]).Next` calls took 66.3 ms → **663 µs each**.

**Why the claim is wrong / incomplete.** Three distinct problems the deferral hides:

1. **Unbounded, not 21,960.** `bound := maxCalendarScanDays * int(interval)` scales linearly
   with a **consumer-supplied `uint`**. `interval: 4294967295` in a YAML definition is a
   ~102-hour single-call CPU sink. Nothing validates `interval`'s magnitude — the ADR's
   `Validate` checks only `interval == 0`.
2. **ADR-0176 moves this onto the engine step path and doubles it.** Today the scan runs once,
   in the runtime (`timerJobsFor`). After this change `ValidateAt(anchor)` runs it at each of the
   six gated step sites as well, so every armed calendar timer pays it **twice**, and the
   pathological case becomes a hang inside `engine.Step` — the pure core a consumer calls
   directly. That availability regression is not mentioned.
3. **Signed-conversion bug.** `int(interval)` for `interval == MaxUint64` is `-1`, giving
   `bound == -1830`; the loop body never runs and `Next` reports `!ok`. A huge interval is
   classified never-due by integer overflow rather than by the calendar — and under this ADR
   that silently becomes a step failure whose stated reason is wrong.

**Concrete fix.**

- Add a magnitude bound on `interval` to the **static** `Validate` (hence the build gate), e.g.
  reject `interval > 1200` (100 years) with a distinct sentinel, and state it in §4.2.
- Clamp the scan bound in `internal/schedcalc`, and convert `interval` with an explicit width
  check rather than a bare `int(interval)`.
- Rewrite §5's bullet as: *"a never-due calendar spec costs a full `366*5*interval`-iteration
  scan per `Next` call — measured **663 µs** at `interval == 12` (21,960 iterations, 47 ns
  each) — and is **linear in a consumer-supplied `interval`**: `interval == MaxUint32` projects
  to ~102 hours of CPU in one call. ADR-0176 runs this computation a **second** time, at step
  time, inside `engine.Step`. A magnitude bound on `interval` is therefore part of this
  delivery, not a follow-up."*

---

## M4 (MAJOR) — The `assertSameTxAtomicity` citation names the wrong entry point: the test asserts on `ApplyTrigger`, not `Drive`

**Claim attacked.** Spec §1.2: *"the existing passing test `assertSameTxAtomicity`
(`runtime/timer_txflow_test.go:189-247`) injects an `UpsertJob` failure and asserts exactly
this: **`Drive` returns the error**, the instance version does not advance, and the pre-step
timer rows are restored unchanged."* ADR Context repeats it: *"makes **`Drive`** error"*.

**Command.**

```
sed -n '184,250p' runtime/timer_txflow_test.go
go test -count=1 -v -run 'TestTimerTxFlowSameTxAtomicity$' ./runtime/
```

**REAL output.** The test's own lines:

```go
parked, err := driver.Drive(ctx, def, instanceID, nil)
require.NoError(t, err)                       // <-- Drive is asserted to SUCCEED
...
fw.setFailUpsert(true)
fc.Advance(time.Hour + time.Second)
_, err = driver.ApplyTrigger(ctx, def, instanceID, engine.NewTimerFired(fc.Now(), wait1TimerID))
require.Error(t, err, "an injected in-tx Save failure must surface as a commit error")
require.ErrorIs(t, err, errInjected)
```

```
--- PASS: TestTimerTxFlowSameTxAtomicity (0.01s)
```

**Adjudication of the three sub-claims the brief asked for:**

- **(a) exists — YES.** `runtime/timer_txflow_test.go:189`, body ends ~`:247`; the cited range is
  accurate. Two callers: `:253` (SQLite), `:262` (Postgres).
- **(b) asserts what is claimed — PARTLY; the entry point is wrong.** The injected failure is
  surfaced by **`ApplyTrigger`**; `Drive` is asserted to return **no** error earlier in the same
  test. The version-not-advanced and rows-restored halves are asserted exactly as claimed.
- **(c) CAN fail — YES, and the FIXTURE supports it.** `twoTimerDef()` is start → wait1(timer) →
  wait2(timer) → end, so the failing step genuinely *arms* a timer and the injected `UpsertJob`
  failure is reachable; `require.ErrorIs(err, errInjected)` cannot pass unless the `Save` was
  attempted. Not a vacuous citation.

**Does the substance survive?** Yes — `Drive` and `ApplyTrigger` share the same step loop and
`commitFn`: `runtime/processdriver.go:730` (`commitFn := func`), `:745`
(`jobStore.Save(txCtx, sj)`), `:759` (`tx.RunInTx(ctx, commitFn)`), `:766`
(`fmt.Errorf("workflow-runtime: commit: %w", err)`) — all four spec §1 line citations exact. But
the sentence is a false statement *about a test*, of exactly the kind Premise Discipline targets,
and it is **inherited verbatim into the ADR**; the documented failure mode ("the instance can
never advance past that node") concerns a *fresh arm during a step*, whereas the cited test
covers the *TimerFired* path.

**Concrete fix.** In both spec §1.2 and ADR Context: *"the existing passing test
`assertSameTxAtomicity` (`runtime/timer_txflow_test.go:189-247`) injects an `UpsertJob` failure
on a step that consumes one timer and arms another, and asserts that **`ApplyTrigger`** returns
the injected error, the instance version does not advance, and the pre-step timer rows are
restored unchanged. `Drive` shares the same `commitFn`/`RunInTx` path
(`runtime/processdriver.go:730-766`), so the rollback mechanism is identical; **no existing test
injects the failure through `Drive`.**"*

---

## M5 (MAJOR) — §1.1's stated MECHANISM for the MySQL rejection is refuted: MySQL accepts year-1 and year-999 `next_run`

**Claim attacked.** Spec §1.1: *"MySQL's `DATETIME` range starts at `1000-01-01`; Postgres
`TIMESTAMPTZ` … and SQLite `TEXT` … both cover year 1."*

**Command.** `go test -count=1 -v -timeout 600s -run 'TestZZZAuditZeroNextRun/mysql' ./internal/persistence/store/`

**REAL output.**

```
PROBE dialect=mysql UpsertJob(ZERO next_run) err=workflow-store: upsert timer "probe-inst"/"probe-timer": Error 1292 (22007): Incorrect datetime value: '0000-00-00' for column 'next_run' at row 1
PROBE dialect=mysql UpsertJob(year-1 non-zero next_run=0001-01-01T00:00:01Z) err=<nil>
PROBE dialect=mysql UpsertJob(year-999 next_run) err=<nil>
```

**Why the claim is wrong.** MySQL **accepted** `0001-01-01T00:00:01Z` and
`0999-12-31T23:00:00Z`. The rejection is not a range check — it is strict mode refusing the
**all-zero date literal `'0000-00-00'`** the driver renders for Go's zero `time.Time`. The
load-bearing consequence: MySQL's loudness depends on the value being **exactly** zero, not on a
range floor. Any *near*-zero instant is silently accepted on all three dialects, collapsing the
"loud on one dialect" property the whole §1.2 narrative rests on — and `After(d)` can produce
such an instant (see m1).

**Concrete fix.** *"The wire value is `'0000-00-00'`: the driver renders Go's zero time that way
and MySQL strict mode rejects the all-zero date against `next_run DATETIME(6) NOT NULL`
(`migrations/mysql/0001_init.sql:95`). ⚠ It is the **zero literal**, not a range floor:
measured, MySQL accepts `0001-01-01T00:00:01Z` and `0999-12-31T23:00:00Z` without error.
Postgres `TIMESTAMPTZ` (`postgres/0001_init.sql:103`) and SQLite `TEXT`
(`sqlite/0001_init.sql:102`) accept all three."*

---

## C4 (CRITICAL) — The step gate's reused failure semantics WEDGE an already-running instance against a surviving, past-due timer row; the ADR sells this as an improvement

**Claim attacked.** ADR Consequences / spec §5: *"A step reaching a node whose resolved trigger
can never fire now **fails** with `ErrTriggerNeverDue`. On MySQL this replaces `Error 1292` at
commit with a clear engine error at step time; on Postgres/SQLite it replaces an invisible
forever-wait with a **loud failure**."* And ADR Decision 3: *"The existing per-site error
wrappers carry it out, so **no new failure semantics are invented** — this is the path a
malformed expression already takes."*

**Command.** Two probes, using a **malformed `EveryExpr`** as an exact proxy for the failure
path the ADR says it will reuse (it fails at the same six sites, through the same wrappers):

```
go test -count=1 -v -timeout 300s -run 'TestZZZAuditMalformedExprStepFailure' ./runtime/
go test -count=1 -v -timeout 300s -run 'TestZZZAuditWedgeOnRunningInstance'   ./runtime/
```

**REAL output — case 1, failure on the FIRST step (fresh `StartInstance`):**

```
PROBE Drive err=workflow-runtime: step: workflow-engine: reminder node "ut": workflow-expreval: compile "!!!not an expression!!!": …
PROBE   returned status="running" instanceID="zzz-wedge-1" tokens=0
PROBE   store.Load err=workflow-runtime: instance not found version=0 status="running"
PROBE   wrkflw_timers err=<nil> rows=0
PROBE   second Drive err=… (deterministic wedge=true)
```

**REAL output — case 2, failure on a LATER step of an ALREADY-RUNNING instance:**

```
PROBE step1 Drive err=<nil> status="running"
PROBE   instance row: err=<nil> version=1 status="running"
PROBE   c1 armed timer="d9ukd1h83g3hpc3emi0g" next_run=2026-02-10T10:00:00Z
PROBE step2 ApplyTrigger err=workflow-runtime: step: workflow-engine: timer node "c2": workflow-expreval: compile "!!!bad!!!": …
PROBE   instance row AFTER failure: version=1 (was 1, advanced=false) status="running"
PROBE   wrkflw_timers rows=1
PROBE   ROW timer="d9ukd1h83g3hpc3emi0g" next_run=2026-02-10T10:00:00Z
PROBE step2-retry ApplyTrigger err=… (same error)
PROBE   WEDGED with a LIVE instance row = true
```

**Why the claim is wrong.** The two cases behave completely differently and the bundle describes
only the good one:

- **Fresh start (good, and worth stating):** no instance row is created at all, no timer row,
  and the error is deterministic. The consumer simply gets an error from `Drive`. This is a
  genuine improvement over today and a strong argument for the step gate — **the bundle should
  claim this explicitly, because it is the case it actually delivers.**
- **Already-running instance (the gap):** the instance row **survives** at the unadvanced
  version, still `running`; the whole step rolls back, which **restores the already-fired timer
  row** — now sitting at a `next_run` in the past. Every subsequent `ApplyTrigger` fails
  identically. So "loud failure" is really *a permanent wedge with a live row and a past-due
  armed timer*, and each fire attempt does full step + rollback work.
  **ASSUMPTION (unverified):** on reboot, rehydration re-arms that durable row, it fires
  immediately (past-due), the step fails again — an unbounded fire/fail/rollback loop. The row
  survival and the deterministic failure are measured; the reboot-loop half is inferred from
  `buildTimerJob`'s rehydration path and is **not** executed here.

On MySQL this is not a regression (today's `Error 1292` wedges identically). On
**Postgres/SQLite it is a resource regression**: today's cost is one inert zero row and silence;
after this change it is a repeatedly-failing step against a live row. The ADR's sentence
inverts that comparison.

**Concrete fix.** Split the consequence by case, and decide the wedge:

> - **Failure on the first step of a fresh instance** — measured: `Drive` returns the error, and
>   **no instance row and no timer row are created** (`store.Load` → `instance not found`,
>   `wrkflw_timers` rows=0). Clean, and strictly better than today.
> - **Failure on a later step of an already-running instance** — measured: the step rolls back,
>   the instance stays `running` at its previous version, and the **already-fired timer row is
>   restored with a past `next_run`**; every retry fails identically. On Postgres/SQLite this
>   replaces an inert zero row with a **repeatedly-failing step**, which is louder but not
>   cheaper. ⚠ This needs a decision: (a) accept the wedge and document it, (b) fail the
>   *instance* rather than the step so it stops being retried, or (c) gate only at **build** and
>   **arm** time, leaving the step gate out — which loses §2.1's anchor-dependent class.

⚠ Note this makes the **arm gate** (which merely WARN-skips, leaving the step to succeed) and
the **step gate** (which wedges) materially different in consequence, not just in loudness. §4.3
presents the choice as being only about where "loudness belongs".

---

## M6 (MAJOR) — There is already a FOURTH gate: the scheduler refuses never-due jobs, with a near-identically worded error; the bundle acknowledges neither

**Claim attacked.** Spec §4.3's three-gate table (build / step / arm), presented as the complete
set; and §4.2's new sentinel `ErrTriggerNeverDue` = `"workflow-schedule: trigger can never
fire"`.

**Command.** `grep -rn 'can never fire' --include='*.go' .`

**REAL output.**

```
scheduler/scheduler.go:580:  return nil, fmt.Errorf("workflow-scheduler: job %q trigger can never fire: %w", j.ID(), ErrUnsupportedTrigger)
processtest/memscheduler.go:119: return time.Time{}, 0, fmt.Errorf("processtest: job %q trigger can never fire: %w", j.ID(), scheduler.ErrUnsupportedTrigger)
```

`scheduler/scheduler.go:578-581`:

```go
next, ok := j.Trigger().Next(s.now().In(s.location()))
if !ok {
    return nil, fmt.Errorf("workflow-scheduler: job %q trigger can never fire: %w", j.ID(), ErrUnsupportedTrigger)
}
```

And it fires in practice — the end-to-end probe's WARN lines:

```
WARN runtime: timer arm: post-commit activate failed … error="processtest: job \"…\" trigger can never fire: workflow-scheduler: unsupported trigger"
```

**Why it matters.**

1. **A fourth gate exists and already detects this exact condition** — just post-commit, as a
   benign WARN, after the durable row is written. §4.3's table is incomplete, and §1.2's
   "Post-commit `Activate` failure is WARN-only and deliberately benign" does not say that the
   failure *is* a never-due detection. Once the arm gate lands, no arm reaches `Activate`, so
   the arm gate **replaces** this WARN rather than adding to it — the ADR should say so, or two
   readers will expect two log lines.
2. **The new sentinel's message duplicates the existing one.** `"workflow-schedule: trigger can
   never fire"` (new, `definition/schedule`) vs `"workflow-scheduler: job %q trigger can never
   fire"` (existing, `scheduler`) — differing by one character in the package prefix. That is a
   grep-hostile, log-hostile collision.
3. **It reinforces C1.** `NewScheduledJob` does **not** validate its `nextRun`
   (`scheduler/job.go:214-219` — it only rejects a nil `Job`), so the cron-zero-instant case
   passes `ok == true` at `:578`, sails through this fourth gate as well, and is scheduled with a
   zero fire time. C1 therefore defeats **four** layers, not three.

**Concrete fix.** Add a row to §4.3's table for the pre-existing scheduler guard
(`scheduler/scheduler.go:580`, post-commit, WARN via `Activate`), state that the arm gate
supersedes it, and rename the new sentinel so it cannot be confused with it — e.g.
`ErrTriggerNeverDue` with message **`"workflow-schedule: trigger spec can never be due"`**.

---

## m1 (MINOR) — §3.4's "cannot produce a zero `next_run`" rests on an unstated assumption about the clock

**Claim attacked.** Spec §3.4: *"Both bypass sites build `AfterDuration`, which is `KindOneTime`
⇒ `After(d)` ⇒ **always** ok (measured: `After(0)` → ok=true; a negative `d` yields a past,
non-zero instant that MySQL accepts). So the exposure is exactly: the six gated sites …"*

**Verdict: SURVIVES, with an unstated premise.** Confirmed by source that both sites literally
build `schedule.AfterDuration(...)` (`step_compensation.go:495` — guarded by
`if pol.stallAfter <= 0 { return }` at `:473`; `step_triggers.go:331` — `delay` derived from
`eff.Backoff(attempt)`), and confirmed by execution that `After(0)` and `After(-1s)` both report
`ok=true` with non-zero instants. The brief asked whether any `d` makes `After(d).Next()` report
`!ok`: **no** — the branch is `return after.Add(t.dur), true`, unconditionally.

But the *instant* is `after.Add(d)`, so it is zero exactly when `now.Add(d)` is the zero time.
That never happens in production because the runtime passes `driver.clk.Now()`; it **can** happen
under a fake clock anchored at the zero time. The claim is true, but for a reason the spec does
not state, and C1's fix #2 (`|| next.IsZero()` at the arm gate) covers it for free.

**Concrete fix.** Append to §3.4: *"⚠ `After(d).Next(after)` is `(after.Add(d), true)`
unconditionally — never `!ok` for any `d`. It yields a *zero instant* only if `now.Add(d)` is
itself the zero time, which the runtime's `clk.Now()` precludes in production (but not under a
fake clock anchored at the zero time). The arm gate's `next.IsZero()` check covers this."*

---

## m2 (MINOR) — §3.5's pasted probe output is truncated: the real error is a joined two-line error

**Claim attacked.** Spec §3.5's recorded output:
`PROBE model.Validate(crafted def) err=workflow-definition: flow references unknown node: flow "f1" target "nope"`.

**Command.** `go test -count=1 -v -run 'TestZZZAuditStoredDefinitionNotRevalidated' ./internal/persistence/store/`

**REAL output.**

```
PROBE model.Validate(broken def) err=workflow-definition: flow references unknown node: flow "f1" target "nope"
    workflow-definition: unreachable node: node "end"
PROBE PutDefinition(broken) err=<nil>
PROBE GetDefinition(broken) err=<nil> loaded=true
PROBE   re-Validate(loaded) err=workflow-definition: flow references unknown node: flow "f1" target "nope"
    workflow-definition: unreachable node: node "end"
```

**Why it matters (mildly).** `Validate` returns `errors.Join`, so the real value has a **second**
line the spec dropped. The §3.5 conclusion is unaffected and **fully confirmed** — including the
stronger fact the spec did not record: the *loaded* definition still fails `Validate`, proving the
round-trip preserves the invalid shape rather than repairing it. A pasted "raw output" that is
not the raw output is the habit Premise Discipline exists to break.

**Concrete fix.** Paste both lines, and add the re-validate line: *"`re-Validate(loaded)` returns
the same joined error — the store preserves the invalid shape."*

---

## m3 (MINOR, high value) — Prescribed mutation 3 IS discriminating; the plan should name the fixture instead of hedging

**Claim attacked.** Plan mutation 3 / spec §6 mutation 3: *"Drop `.In(loc)` from the engine's
anchor → the month-boundary test must go RED. ⚠ If it stays green, the *claim* is wrong … the
month-boundary window may be unreachable through that fixture."*

**Command.** `go test -count=1 -v -run 'TestZZZAuditMonthBoundaryWindow' ./scheduler/`

**REAL output.**

```
PROBE Monthly(12,[31]) anchor=2026-08-31T23:30:00Z      (month=August   ) ok=true  next=2027-08-31T00:00:00Z
PROBE Monthly(12,[31]) anchor=2026-09-01T06:30:00+07:00 (month=September) ok=false next=0001-01-01T00:00:00Z
PROBE Monthly(12,[30]) anchor=2026-08-31T23:30:00Z      (month=August   ) ok=true  next=2027-08-30T00:00:00Z
PROBE Monthly(12,[30]) anchor=2026-09-01T06:30:00+07:00 (month=September) ok=true  next=2026-09-30T00:00:00+07:00
PROBE Monthly(1,[31])  both locations ok=true
PROBE Daily(1)         both locations ok=true
PROBE MUTATION-3 DISCRIMINATES: Monthly(12,[31]) @2026-08-31T23:30Z  UTC ok=true  +07 ok=false  differ=true
```

**Verdict: the window is REACHABLE and the mutation discriminates.** The hedge is unnecessary —
but the plan gives the implementer no fixture, and only a narrow one works: `Monthly(12,[30])`
does **not** discriminate at this instant (ok=true in both zones), nor do `Monthly(1,[31])` or
`Daily(1)`. An implementer who guesses `Monthly(12,[30])` will observe a green mutation and — per
the plan's own instruction — **record a false finding that the claim is wrong**.

**Concrete fix.** Replace the hedge with the fixture: *"Use `Monthly(12,[31])` at
`2026-08-31T23:30:00Z` with `time.FixedZone("WIB", 7*3600)`. Measured: UTC → ok=true
(`next=2027-08-31`), +07 → ok=false, so dropping `.In(loc)` flips the verdict and the test goes
RED. ⚠ `Monthly(12,[30])` at the same instant does **not** discriminate (ok=true in both zones) —
do not substitute it. Use `time.FixedZone`, not `time.LoadLocation("Asia/Jakarta")`, so the test
does not depend on system tzdata (both were verified to give identical verdicts here)."*

---

## m4 (MINOR) — Plan Phase 4b's wait-reminder fixture needs an `ActorResolver` if it uses a UserTask

**Command.** `go test -count=1 -v -run 'TestZZZAuditMalformedExprStepFailure' ./runtime/`

**REAL output** (the incidental second case):

```
PROBE EveryExpr("0") Drive err=workflow-runtime: resolve candidates for task d9ukckh83g3hnviva0t0: no ActorResolver configured
```

**Why it matters.** Plan Phase 4b.2 requires a test at the **wait-reminder** site. Reached via
`armWaitReminder` (`step_nodes.go:689`), whose callers are `:100` (ReceiveTask-ish park), `:801`
(**UserTask**) and `:864` (intermediate catch). The UserTask route additionally requires an
`ActorResolver` on the driver or the step fails for an unrelated reason before the assertion is
reached — a trap that costs an implementation cycle.

**Concrete fix.** Add to Phase 4b.2: *"reach the wait-reminder site through the
`step_nodes.go:864` (intermediate-catch) or `:100` path, **not** the UserTask path at `:801` —
a UserTask fixture additionally needs an `ActorResolver` or the step fails with
`no ActorResolver configured` before the gate is reached."*

---

## Additional CONFIRMED claims (executed, survived)

| claim | verdict | evidence |
|---|---|---|
| §3.2 end-to-end: `EveryExpr("0")` really produces a zero `next_run` row from ordinary runtime data | **CONFIRMED end-to-end on SQLite** | `Drive err=<nil> status=running`; `ROW next_run=0001-01-01T00:00:00Z ZERO=true`; `Stats nextFireAt=0001-01-01… POISONED=true`. Same for `EveryExpr("-1")`, `Every(0)`, `Cron("bogus")`, `Monthly(12,[31])` at a February clock, and `Weekly(1,nil)` — **6 of 6** never-due shapes wrote a zero row; the `AfterDuration(1h)` control wrote `2026-02-10T10:00:00Z` |
| §3.5 a definition `model.Validate` rejects round-trips through `PutDefinition`/`GetDefinition` cleanly | **CONFIRMED** (and the loaded copy still fails `Validate`) | see m2 |
| §4.4 `stepCtx` already carries `at` | **CONFIRMED** — `engine/step_nodes.go:18` `type stepCtx struct`, `:29` `at time.Time` | |
| §4.4 `armBoundaries` and `armEventTriggeredSubprocesses` already take an `at time.Time` parameter | **CONFIRMED** — `step_boundaries.go:24`, `step_eventsubprocess.go:71` | so "no anchor threading is needed" holds |
| §4.3 / ADR Decision 3 all six gated sites already **propagate** the `ResolveTrigger` error through a per-site `fmt.Errorf` wrapper | **CONFIRMED at all six**, and **every caller propagates too** (`step_nodes.go:57/100/104/672/801/810/864`, `step_triggers.go:45`, `step_compensation.go:1166`) — no site swallows it | so "`Step` fails" and "no new failure semantics" hold *mechanically*; ⚠ but see C4 for what that failure *does* |
| ADR Decision 2 both predicates must return nil for `KindUnset` | **CONFIRMED as load-bearing** — `step_nodes.go:766` and `:825` call `ResolveTrigger` **unconditionally**, before their `IsZero()` check, so a predicate rejecting `KindUnset` would break them | |
| plan Phase 1.3 `engine/purity_test.go`'s AST scan is non-recursive and covers neither `definition/schedule` nor `internal/schedcalc` | **CONFIRMED** — `purity_test.go:16` iterates `[]string{".", "../definition"}`, `:17` `os.ReadDir(dir)`, `:23` `if e.IsDir() … continue` | only `definition/definition.go` is scanned under `../definition` |
| §4.1 `scheduler` and `definition/schedule` do not import each other | **CONFIRMED** — `go list -deps ./scheduler` in-repo deps are only `scheduler/internal/obs`, `scheduler/internal/gocron`; `./definition/schedule` has **no** in-repo deps (stdlib `time` only) | ⚠ the spec's "pulling in nothing" is loose — it imports `time` |
| plan Phase 4b.7 `engine` does not import `scheduler` | **CONFIRMED** — `go list -deps ./engine \| grep -c 'wrkflw/scheduler'` → `0` | |
| §4.4 the `StepOptions` additive-field pattern has precedent | **CONFIRMED** — `engine/step.go:24` `type StepOptions struct`, `:68` `CompensationStallAfter time.Duration`, consumed at `:95` | |

## Claims NOT verified by this lens

- The `migrations/*/0001_init.sql` line numbers cited in §1.1 (`mysql:95`, `postgres:103`,
  `sqlite:102`) were not opened; only the *behaviour* of each dialect was executed.
- Whether the rehydration loop in C4 actually re-fires after a reboot (labelled
  `ASSUMPTION (unverified)` there).
- The counting lens's remit: I re-derived the enumerations that were load-bearing for my probes
  (eight `ScheduleTimer{}` sites, six `ResolveTrigger` sites, five spec-bearing fields, one
  `model.Validate` caller, four `calendarNext` false branches) and all five were exact.

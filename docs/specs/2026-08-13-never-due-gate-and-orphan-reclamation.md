# A never-due gate at authoring time, and reclaiming the rows we already have (ADR-0181, ADR-0182)

**Status**: design — **audited (3 lenses, ~40 findings) and folded 2026-08-14**; ready to implement
**Date**: 2026-08-13 · folded 2026-08-14
**Bundle**: B — one delivery, two ADRs, one commit
**Base**: `main` at the ADR-0177/0178/0180 merge `a5b33e4c`

This bundle closes `HANDOVER.md` ▶ NEXT WORK item **2** — backlog **22** (the gates ADR-0176
deferred) and **23** (orphan rows are not reclaimed) — to the extent that closing them is
defensible. Two of backlog 22's pieces are **deliberately re-deferred**, with the measured reason
recorded (§5). That is the honest close, not a partial one.

---

## §0 — Reading guide

- Premise evidence: `docs/specs/2026-08-13-adr-0181-0182-premise-evidence.md`.
- Audit records (three lenses): `docs/specs/2026-08-13-adr-0181-0182-audit-lens-{a,b,c}.md`.
- **Audit adjudication — read this before the ADRs**:
  `docs/specs/2026-08-13-adr-0181-0182-audit-adjudication.md`. Every finding's disposition, plus the
  two controller probes (**P-A**: the corpus through the production chain; **P-B**: which sweep
  predicate actually reclaims) whose numbers this spec quotes.

⚠ Where the evidence record disagrees with `HANDOVER.md`, it wins — the handover's summary of
ADR-0176's deferrals is a compression of **two different lists** (§4).

⚠ **ADR-0176's measurements file must be read backwards**: §17 (`/security-review`) supersedes §16
(`/code-review`), which supersedes §15, which supersedes §13, which supersedes §1–§10. §14 holds
the arming instants.

---

## §1 — The constraint that caps this entire bundle

From ADR-0176's measurements §9, still standing:

> A static `Validate()` can only be **SOUND, never complete**:
> `Validate() != nil ⟹ ∀t: Next(t) is !ok`. **The converse is false.**

Measured again here (probe P-A, five anchors): `Monthly(12,[31])` is `!ok` at 2026-02-01,
2026-06-14 and 2028-02-29, and `ok` at 2026-08-01 (→ 2026-08-31) and 2026-01-31 (→ 2027-01-31).
Whether a recurring trigger is ever due can depend on **when you ask**.

Therefore: **a deploy-time gate can never replace the arm-time guard ADR-0176 shipped.** It is not
defence-in-depth for the anchor-dependent class; the arm guard is the only layer that catches it.
What a deploy-time gate buys is **early authoring feedback** for the anchor-*independent* class —
triggers that are never due at any anchor. That is a real but bounded benefit, and this bundle
claims nothing more.

⚠ This constraint is also why the cross-check test (§3) asserts only **one direction**. See §6.4.

---

## §2 — ADR-0181: reclaim orphan zero-`next_run` rows

### The measured problem

⚠ **The inherited figure "`PruneTimers` was measured deleting 1 of 5" is true but misleading, and
is replaced everywhere in this bundle.** Executed on SQLite (pure-Go, no container):

```
  PROBE before prune: 5 armed rows                     (4 orphans + 1 healthy expired one-shot)
  PROBE PruneTimers(cutoff=…) deleted=1
  PROBE ORPHANS (zero next_run) RECLAIMED: 0 of 4; still present: 4
```

The one row deleted was the deliberate **control** — a healthy expired one-shot, not an orphan.

The predicate is `next_run < cutoff AND trigger_kind IN (Unset, OneTime, Expr)`, and
`nonRecurringTriggerKinds` is exactly the complement of `TriggerSpec.Recurring()`. A zero
`next_run` **satisfies** the cutoff clause — the zero literal is not what excludes an orphan. The
`trigger_kind` clause is the whole exclusion, and **every never-due kind is recurring**. So:

> **`PruneTimers` reclaims 0 of 4 orphan rows. Its reachable set and the orphan set are disjoint by
> construction, not by cutoff choice.**

"1 of 5" invites "it partly works, tune the cutoff". It does not partly work.

⚠ There is **one** implementation (`internal/persistence/store/pruner.go`), not one per dialect —
ADR-0081 unified them behind `dialect.Rebind`.

### Decision

A **separate sweep with its own predicate** — `next_run < <Unix epoch> AND trigger_kind IN
(<the seven recurring kinds>)` — added to the store's `Pruner`, reached by consumers through a new
**optional-capability interface** `persistence.NeverDueTimerReclaimer` (§2.3).

⚠ **It must NOT be implemented by widening `PruneTimers`' `trigger_kind` IN-list.** That would
delete still-armed recurring rows, which is exactly the bug ADR-0134 fixed and the reason the
IN-list exists. A second, disjoint predicate — never a wider one.

The sweep is a **single-statement `DELETE`**. That is not incidental: a single statement
re-evaluates the predicate atomically, so a row concurrently re-armed by `upsertTimer` with a
valid `next_run` is safe. A batched `SELECT`-then-`DELETE`-by-PK variant would open a TOCTOU
window and destroy it.

### §2.1 — Why a threshold and not `next_run = <zero>` (audit A-F1 / B-F2)

The bundle originally specified equality on the Go zero time. **Measured (probe P-B, SQLite, eight
seeded rows):**

```
fixed-width zero = "0001-01-01T00:00:00.000000000Z"
trimmed     zero = "0001-01-01T00:00:00Z"
epoch sentinel   =  1970-01-01T00:00:00.000000000Z

equality(zero)     seeded=8 deleted=4 survivors=[armed-future-recurring armed-past-recurring
                                                 healthy-expired-oneshot orphan-legacy-trimmed]
threshold(epoch)   seeded=8 deleted=5 survivors=[armed-future-recurring armed-past-recurring
                                                 healthy-expired-oneshot]
```

SQLite stores `next_run` as **TEXT** and compares it lexicographically. ADR-0151 moved the encoding
to a fixed nine-digit fraction; `parseTimeText` still *reads* the older trimmed form, and its doc
comment asserts in-repo that such rows exist. An `=` predicate does not tolerate what a parser
does — and orphans are by construction **old** rows, i.e. the population most likely to carry the
old encoding. The sweep would have reported success while leaving them.

**The sentinel is the Unix epoch**, chosen over lens A's year-1000 suggestion because:

- it is inside MySQL's `DATETIME` range (which starts at 1000-01-01, so year-1000 sits exactly on
  the boundary), and it also sits above a non-strict MySQL's coerced `'0000-00-00'`;
- on SQLite the comparison is lexicographic and `"0001-…" < "1970-…"` holds for **both** encodings;
- no legitimately armed **recurring** row can precede it: a recurring `next_run` is computed by
  `Trigger.Next(after)` and is always strictly after the arming instant. The non-recurring kinds,
  where a caller-supplied past absolute time *is* possible, are excluded by the `trigger_kind`
  clause.

The measured control that settles the last point is **`armed-past-recurring`** — a recurring row
with a `next_run` in 2020, the exact population ADR-0134 protects. The threshold spares it.

### §2.2 — MySQL has no orphan population (audit A-F2 / B-F3 / C42)

⚠ **The original `ASSUMPTION (unverified — needs Docker)` that "the same 0-of-4 result holds on
Postgres and MySQL" was not merely unverified — its MySQL half is REFUTED by a measurement already
in this repo.** ADR-0176's measurements §4 measured `UpsertJob` with a zero `next_run`:

```
postgres  accepted
sqlite    accepted
mysql     REJECTED — Error 1292 (22007): Incorrect datetime value: '0000-00-00' for column 'next_run'
```

`next_run` is declared `DATETIME(6) NOT NULL` (`migrations/mysql/0001_init.sql:95`) and MySQL's
`DATETIME` range starts at 1000-01-01, so the Go zero time cannot round-trip as year 1.

Consequences the bundle originally missed:

1. On MySQL the orphan population is **provably empty** and the sweep is a structural no-op — 0 of
   **0**, not an unverified 0 of 4.
2. The plan's prescribed Docker checklist item was **unrunnable** on MySQL: the seeding step itself
   raises 1292.
3. This is a *population* difference, not the *implementation-uniformity* question ADR-0181 framed
   it as.

⚠ A MySQL deployment **without** `STRICT_TRANS_TABLES` / `NO_ZERO_DATE` stores `'0000-00-00
00:00:00'` rather than erroring. That value is not `0001-01-01`, so an equality on the Go zero time
would miss it — a third independent reason for §2.1's threshold, which does match it.

✅ **The Postgres half is now MEASURED, not assumed.** This paragraph originally read
`ASSUMPTION (unverified — needs Docker): the SQLite reclamation result generalises to Postgres`.
`/code-review` measured it instead: Postgres accepts a zero `next_run` (`TIMESTAMPTZ`, no `CHECK`,
range back to 4713 BC), and the sweep reclaims exactly the recurring orphan while the sub-epoch
one-shot survives. `TestPrunerReclaimNeverDueTimersPostgres` now pins that against a real container
and is mutation-verified to discriminate. ⚠ The gate also found the test file asserting the
opposite — that SQLite was "the only backend that can hold the fixture at all", which contradicted
**this section** and argued against covering the primary production backend. Corrected.

⚠ The MySQL no-op is conditional on **default strict mode**; under a non-strict `sql_mode` the
coerced `'0000-00-00 00:00:00'` rows *are* sub-epoch and *are* reclaimed. Both the public and
internal doc comments now say so — "no-op on MySQL", read unconditionally, would let an operator
skip reviewing a destructive call.

### §2.3 — Reachability: the sweep must not ship as dead code (audit B-F5)

All three public constructors — `persistence.NewPruner` (Postgres), `NewMySQLPruner`,
`NewSQLitePruner` — return the **`Pruner` interface**, not the concrete type, and
`internal/persistence/store` cannot be imported by a consumer. Adding a method only to the concrete
store type therefore leaves **no consumer wiring that can reach it at all**. The original ADR's
"a consumer wiring only the public interface cannot reach it" understated this, and its Positive
consequence ("an operator can reclaim rows that today require manual SQL") would have been false.

**Owner's decision**: expose it as a documented **optional-capability interface** in the public
`persistence` package —

```go
// NeverDueTimerReclaimer is an optional capability of a Pruner …
type NeverDueTimerReclaimer interface {
    ReclaimNeverDueTimers(ctx context.Context) (int64, error)
}

// usage:
if r, ok := pruner.(persistence.NeverDueTimerReclaimer); ok {
    n, err := r.ReclaimNeverDueTimers(ctx)
}
```

`persistence.Pruner` itself is **not** widened — that would be source-breaking for any consumer
implementing it. A compile-time assertion pins that `*store.Pruner` satisfies the capability, so
the assertion above can never silently start failing.

### Consequences

`Stats.NextFireAt`, measured pinned at `0001-01-01` by an orphan heading the keyset index, is
freed — and §6.1 now has a test that observes it, because an untested promise is the ADR-0162
zombie-scope shape.

⚠ **Reclaiming the row does not unpark the instance.** The orphan is the artefact of an instance
that armed a never-due timer and is parked forever; deleting the row removes the timer-side
diagnostic while the instance stays stuck. Operators wanting the identities should read `ListArmed`
/ `Stats` **before** sweeping. The sweep itself reports only a count: capturing per-row identities
would need either a pre-`SELECT` (reintroducing the TOCTOU the single-statement `DELETE` avoids) or
`DELETE … RETURNING`, which MySQL does not have.

Deleting a row is irreversible. The predicate is deliberately narrow — a sub-epoch `next_run` on a
recurring kind is unambiguously an orphan, because ADR-0176 now refuses to write one.

---

## §3 — ADR-0182: reject never-due triggers at definition validation

### What is true today

Executed (`definition/model`, probe D): **nothing rejects a never-due timer at `model.Validate`**,
root or nested — `Monthly(12,[31])`, `Cron("0 0 30 2 *")` and `Every(0)` all validate clean.

Three structural facts, all executed:

- **`model → event` is a real import cycle** (`go build` EXIT=1). So `model` cannot see
  `event.*.Timer` directly, and no accessor exposes it. ⚠ `model → scheduler` is **not** a cycle.
- **The wire form avoids it**: the trigger fields on `NodeWire` are `model` types, and a nested
  `event`-package node's timer appears in `model`-driven output with no `event` import.
- **A rule outside `validateStructure` exempts every nested sub-process.** Demonstrated with the
  one rule that actually sits in `Validate`: root `Version=0` errors, nested `Version=0` returns
  `nil`. A rule inside `validateStructure` **does** reach nested sub-processes, wrapped with the
  host node id.

### Decision

A **standalone predicate over `schedule.TriggerSpec`**, evaluated inside `validateStructure`,
rejecting the **anchor-independent** never-due classes enumerated in §3.1.

⚠ The wire fields are `*model.TriggerWire`, **not** `schedule.TriggerSpec` (audit C39). The decode
is `model.ReadTrigger(w, flatExpr, recurringFlat)`; the gate reads two of the node's three
trigger-carrying fields:

| field | decode | in the gate? |
|---|---|---|
| `TimerTrigger` / `TimerDuration` | `ReadTrigger(w.TimerTrigger, w.TimerDuration, false)` | **yes** |
| `WaitTrigger` / `WaitEvery` | **`WaitActionOf(n)`** | **yes** (§3.3) |
| `DeadlineTrigger` / `DeadlineDuration` | `ReadTrigger(w.DeadlineTrigger, w.DeadlineDuration, false)` | no — §3.4 |

⚠ **Corrected during implementation**: the in-wait row originally read
`ReadTrigger(w.WaitTrigger, w.WaitEvery, true)`. That is the same decode — `NodeWire.Wait()` is
literally that expression — but `WaitActionOf` is what `engine.armWaitReminder` itself reads
(`engine/step_nodes.go:690`), so the gate and the production arm path cannot disagree about which
spec is being judged. Measured populated on wire-reconstructed nodes.

**Why a standalone predicate rather than asking `scheduler.Trigger.Next`.** The authoritative route
would need `convertTrigger` — which lives in `runtime`, and `model → runtime` is a measured cycle —
extracted into a package `model` can import. That works, and it was the recon pass's
recommendation. It is rejected on an architectural ground the recon pass did not have:
**ADR-0134 left spinning `scheduler` out into an independent module as an explicitly out-of-scope
follow-up** (ADR-0134 "the spin-out is a `go.mod` split away"; "Follow-ups … not in scope: module
spin-out checklist"), and locked its preconditions with `scheduler/selfcontainment_guard_test.go`.
This decision assumes that follow-up is still intended. Making `definition/model` depend on
`scheduler` would mean that, after the split, the definition layer depends on an **external module**
to validate a definition. The duplication is the cheaper coupling.

⚠ Note the guard's direction (audit C33): `TestSchedulerTreeIsSelfContained` is **outbound-only** —
it forbids `scheduler/...` importing `wrkflw/*`. That is what makes a shared calendar-math package
impossible (§4.4). It says nothing about `definition/model → scheduler`, and must not be cited as
if it did.

⚠ **The cost of that choice is a second source of truth that can drift** — precisely the class
ADR-0176 spent a whole delivery eliminating between `Trigger.Next` and the live scheduler. It is
mitigated, not eliminated, by the cross-check of §6.4.

### §3.1 — The rejection list, as executed branches

Prose enumerations of a code path's branches rot; the audit found this one wrong in **six** places
at once. The list below is the closed set of `!ok` branches reachable from a `schedule.TriggerSpec`
through `convertTrigger`, each verified by probe P-A at five anchors.

**REJECT** (never due at every anchor):

| class | exact predicate | source branch |
|---|---|---|
| non-positive `Every` | `dur <= 0` | `trigger.go` `case triggerEvery` |
| `EveryRandom` bounds | `min <= 0 \|\| min >= max` | `case triggerEveryRandom` |
| zero calendar interval | `interval == 0` for `Daily`, `Weekly` **and `Monthly`** | `calendarNext` |
| out-of-range at-time | `Hour > 23 \|\| Minute > 59 \|\| Second > 59` (all three calendar kinds) | `clockTimesSchedulable` |
| out-of-range day-of-month | `d == 0 \|\| d > 31 \|\| d < -31`, for **any** day in the set | `monthDaysSchedulable` |
| all-negative weekday set | `len(weekdays) > 0 && every w < 0` | `weeklyNext` |

**MUST NOT REJECT** — each measured due:

| spec | why |
|---|---|
| `Monthly(12,[31])` | **anchor-dependent** — due at 2 of 5 probed anchors. §1. |
| `Monthly(1,[-1])`, `Monthly(1,[-31])` | a **negative** day counts back from month end and is legal |
| `Weekly(1,[Weekday(7)])`, `Weekly(1,[Weekday(9)])` | a weekday above Saturday stays a **raw day offset** and always matches on the first pass |
| `Weekly(1,[Weekday(-1), Monday])` | a **mixed** set is due — hence `len>0 && ALL negative`, never `ANY negative` |
| `Weekly(1,nil)`, `Monthly(1,nil)` | an empty set is substituted (Sunday / the 1st) |

⚠ Two of these — the out-of-range weekday and the negative day-of-month — were in the pre-audit
reject list. Rejecting them is the **ADR-0165 inverted-predicate shape verbatim**: a gate refusing
definitions that fire.

### §3.2 — Cron is OUT OF SCOPE (owner decision)

Measured: `Cron("not a cron")`, `Cron("")` **and** `Cron("0 0 30 2 *")` are never due at every
probed anchor; `Cron("0 9 * * 1-5")` is due at every one.

There are two distinct cron never-due causes — a parse error, and a parseable expression matching
no instant (robfig searches five years, then returns the zero time with no error). Catching
**either** requires `definition/model` to import `github.com/robfig/cron/v3`, which it does not
today (verified: `go list -deps ./definition/...`). The owner declined that dependency, so the gate
covers **no cron class at all**.

⚠ Consequently `Cron("0 0 30 2 *")` — the delivery's original headline example — is **no longer
cited as the motivation for this gate**. It is a measured, documented incompleteness. A never-due
cron reaches ADR-0176's arm guard and nothing earlier. The motivating examples are now `Every(0)`,
`Daily(0)` and `Monthly(0, …)`.

### §3.3 — `WaitEvery` is in scope (audit C41)

A node carries **three** trigger-bearing wire field pairs, not one. `WaitTrigger`/`WaitEvery` is a
real durable-arm path: `armWaitReminder` (`engine/step_nodes.go:689`) emits
`ScheduleTimer{Kind: TimerInWait}` from it — the identical path the gate exists to guard — so
`WaitEvery: Every(0)` reaches the arm guard today and nothing earlier. The owner's decision is to
apply the same predicate to it. This widens the authoring blast radius by one field.

⚠ **The legacy flat string forms cannot be judged** (audit C40). `ReadTrigger` decodes a nil wire
with a non-empty flat string as `AfterExpr` (timer/deadline) or `EveryExpr` (wait) — **expression
kinds the engine resolves at run time**. They are not statically judgeable by construction, and the
arm guard remains their only layer. Stated here so it is a known limit, not a silent gap.

### §3.4 — Deadline triggers stay out, and here is the executed reason

The recurring-`DeadlineTimer` half is already rejected at `validate.go:656`. ⚠ That check sits
**inside `validateStructure`**, not in `Build()` as ADR-0176 words it — same conclusion, better
located fact.

The non-recurring half needs an executed reason, because an auditor re-derived it, filed it as a
finding, and then retracted their own finding (lens A F9). `schedule.At(time.Time{})` looks
never-due — `scheduler.At(zero).Next(…)` reports `ok=false` — but that is **not a path the model
can reach**: `convertTrigger`'s `KindOneTime` branch guards on `AbsTime()`, which returns
`ok=false` for a zero instant, so it converts to `scheduler.After(0)`, which is **due**. Enumerating
the non-recurring kinds: `KindUnset` is skipped by the existing rule; `KindOneTime` converts to
`At(non-zero)` or `After(d)`, both due; `KindExpr` is engine-resolved and not statically judgeable.
**There is no never-due one-shot deadline reachable through the model.**

### §3.5 — What the gate does not reach

`validateStructure` recurses into nested sub-processes (including event sub-processes, which are
also `KindSubProcess`) at exactly one site. It does **not** recurse into a `KindCallActivity`'s
referenced definition — only `DefRef != ""` is checked. That definition is validated when it is
itself built, so this is a reach limit, not a hole; recorded so nobody mistakes the gate for
transitive.

### Consequences

**Breaking for authoring — and "authoring" includes every boot that re-parses YAML** (audit B-F1).
The original "breaking for authoring, not for loading" was a false dichotomy. Precisely:

- `Validate` has exactly **one** production caller, `definitionCore.build()`
  (`definition/model/builder.go:133`), reached by both `definition.NewBuilder(...).Build()` **and**
  `definition.NewLoader(yaml).Build()` — and `model.ParseYAML` returns a `DefinitionLoader`.
- Definitions already stored in `wrkflw_definitions` are `json.Unmarshal`ed with no validation
  (`store/definitions.go` `PutDefinition`/`GetDefinition`/`Lookup`), so **no stored definition stops
  loading**.
- ⚠ A consumer that keeps YAML definitions on disk and parses them at process start — the natural
  library deployment — gets the new rejection **at boot**, not in CI. That needs a release note.

The gate is **sound but incomplete by construction**, and says so. It is not defence-in-depth for
the anchor-dependent class, nor for cron, nor for the flat expression forms; ADR-0176's arm guard
remains the only layer that catches those.

---

## §4 — Where the inherited framing was wrong

1. ⚠ **"The four gates ADR-0176 deferred" is a merged enumeration.** ADR-0176's own *Deferred*
   bullets are: the `model.Validate` gate, the step-time engine gate, `StepOptions.SchedulingLocation`,
   and **moving the calendar math to a shared package**. Orphan migration is not among them — it is
   under **Costs accepted**. ADR-0176's spec §6.3 lists **six**.
   ⚠ **Correction (audit C17)**: the earlier draft of this section said "the count *four* appears in
   no single source document". That is **false** — ADR-0176's Deferred list *is* four bullets. What
   is unique to `HANDOVER.md` is the **membership**: it swaps calendar-math for orphan migration.
   A recap sentence over-generalising the reasoning above it — the exact defect Premise Discipline
   names.
2. ⚠ **"1 of 5"** — replaced by "0 of 4 orphans, disjoint by construction" (§2).
3. ⚠ **`neverDueNextRun`'s own doc comment enumerates THREE guard sites** and omits the
   post-commit re-check added at ADR-0176's `/code-review`. There are **four**, confirmed by
   counting the refusal-counter increments (the only exact method: two sites call
   `neverDueNextRun`, two use the equivalent `sj.NextRun().IsZero()`). Doc-only defect in shipped
   code, corrected in this bundle (P3).
4. **Already closed, do not re-scope**: moving calendar math to a shared package is *forbidden* by
   the self-containment guard and unnecessary now the guard lives in `runtime`; `EveryRandom(min>max)`
   was closed at ADR-0176's `/code-review` (handover backlog 25).
   ⚠ **Correction (audit C43)**: this section previously said "the `ErrTriggerNeverDue` sentinel
   exists". **No such symbol exists** — `grep` returns zero hits. ADR-0176's spec §6.3 said it
   exists "**in substance**", meaning the refusal at `scheduler/scheduler.go:580` wrapping
   `ErrUnsupportedTrigger` with the text *"trigger can never fire"*. The hedge was stripped on
   restatement. Creating a real sentinel stays out of scope.
   ⚠ **Correction (audit C44/C45)**: the inherited citation `scheduler/scheduler.go:575-578` has
   rotted — the refusal is at **578–581**, returning at 580. And `EveryRandom`'s never-due predicate
   was spelled three different ways across this bundle (`min>max`, `min >= max`, `min>=max`); the
   code is `min <= 0 || min >= max`.

---

## §5 — Re-deferred, with the measured reason

**The step-time engine gate** and **`StepOptions.SchedulingLocation`** ship together or not at all,
and this bundle ships neither.

- The step-time gate was **measured to wedge a running instance**: the step rolls back with the
  already-fired timer row restored at a past `next_run` and fails identically forever — worse than
  the inert row it replaces. Nothing found in this delivery's recon weakens that finding.
- `StepOptions.SchedulingLocation` has **no standalone justification**. It is additive and
  compile-safe (all 868 construction sites are keyed; exactly **one** is non-test). But its zero
  value `nil` means UTC, so a consumer calling `engine.Step` directly — the library-first path —
  gets the exact false rejection the field exists to prevent; and `runtime` does not need it,
  already asking the scheduler via `schedulingLocation()`. It exists solely to serve a gate that is
  itself deferred as harmful.

Re-deferring is a decision, recorded here so the next reader does not re-litigate it from scratch.

---

## §6 — Test plan, with what makes each test fail today

**6.1 — orphan reclamation.** Seeded **directly** via raw upsert (⚠ not through an arm path: the
trigger's schedulability is irrelevant to this predicate, and the pre-audit fixture named
`Weekly(1,nil)` as "never-due" when it is measurably **due** at every anchor — audit B-F10/C30).
Seed orphans in **both** encodings — the current fixed-width layout and the pre-ADR-0151 trimmed
layout — plus **four** controls: a recurring row with a **future** `next_run`, a recurring row with
a **past** `next_run`, a healthy expired one-shot, and a **sub-epoch one-shot**. Assert the sweep
reclaims every orphan and leaves all four controls.

**Fails today**: the method does not exist (compile error = valid RED); measured 0 of 4 for
`PruneTimers`.

⚠ **Corrected during implementation — the fixture as first prescribed could not test half the
predicate.** This section named the past-recurring row as "the ADR-0134 regression guard … what
fails if anyone implements this by widening the IN-list". Measured, twice and independently: it is
not. At 2020 the row is not sub-epoch, so the **threshold** clause rejects it before `trigger_kind`
is consulted; widening the IN-list to all ten kinds leaves it untouched. As prescribed, **no seeded
row observed the `trigger_kind` clause at all** — the load-bearing half of the predicate would have
shipped untested behind a test named for guarding it. This is the "the test that really covers this
was itself a test that could not fail" pattern, in the fixture rather than in the assertion.

Each control pins a different clause:

| control | clause it guards | dies when |
|---|---|---|
| past-recurring (2020, recurring) | the **epoch threshold** | the threshold becomes `time.Now()` |
| **sub-epoch one-shot** (`KindOneTime`) | the **`trigger_kind` clause** | the IN-list is widened — the ADR-0134 hazard |
| trimmed-encoding orphan | **threshold vs equality** | never — it *survives* the equality design (4 of 5) |
| future-recurring, expired one-shot | the sweep's overall narrowness | the predicate loses either clause |

None is optional.

**6.2 — `Stats.NextFireAt` is freed** (audit B-F7). Seed one orphan + one healthy future row;
assert `Stats().NextFireAt` is `0001-01-01` **before** the sweep and the future instant **after**.
**Fails today**: the sweep does not exist; and it fails again if the predicate reverts to equality.

**6.3 — the gate rejects the §3.1 classes**, root **and nested**, for `TimerTrigger` **and**
`WaitEvery`. **Fails today**: measured, all validate clean. ⚠ The nested case is the one that fails
if the rule is placed in `Validate` instead of `validateStructure` — and it is the reason placement
is a decision at all.

**6.4 — the soundness guard and the cross-check.** ⚠ These are **one corpus**, not two (audit A-F4:
as originally written, the reject corpus and the cross-check corpus were mutually unsatisfiable on
the weekday row, and the bundle did not say which won).

- Every §3.1 **MUST NOT REJECT** row is asserted not rejected. **Fails** if the predicate is
  "improved" into unsoundness — which is exactly what the pre-audit list had done, twice.
- The cross-check asserts the **one direction §1 permits**:
  `predicate(spec) ⟹ Next is !ok at every probed anchor`. Completeness is deliberately **not**
  asserted — the converse is false by §1, so a two-way "verdict == verdict" assertion is
  unsatisfiable, not merely strict. What this catches is **unsoundness**, which is the failure mode
  that actually occurred.
- ⚠ It lives in **`package runtime`** (internal test), not `package model_test` (audit
  A-F8/A-F10/B-F4). Only there are the real unexported `convertTrigger` **and** `Trigger.Next` both
  in scope, so the chain under test is exactly the production chain; a `model_test` version would
  have to hand-roll the conversion — a **third** copy, blind to conversion drift by construction.
  It also means the test survives the scheduler spin-out (`runtime` will simply import the external
  module), where "it must move with the scheduler" named a home that would exist in neither repo.
- Beyond the fixed corpus, a **deterministic generated sweep** (fixed field grids × fixed anchors)
  asserts the same one-directional property, because a fixed corpus only ever re-checks inputs
  already agreed on. `go test -fuzz` in CI is rejected: non-deterministic gate, no extra reach.
  Shipped: 1344 generated specs × 5 anchors = 6720 probes, alongside the 41-spec fixed corpus.
- ⚠ **Corrected during implementation — the sweep is not uniformly the stronger half.** This section
  framed the fixed corpus as a mere "regression floor". For the calendar kinds that is right (the
  sweep caught 240 and 270 violations respectively where the fixed corpus caught 1 and 2). But the
  sweep **generates no cron specs** — cron is out of scope for the gate, and generating cron strings
  would be arbitrary — so the fixed corpus is the **sole** detector for the cron scope decision
  being "improved" into unsoundness. Measured: mutating `NeverDue()` to return `true` for
  `KindCron` is caught only by the fixed corpus row `Cron("0 9 * * 1-5")`.
- ⚠ **`convertTrigger` returns an error, and three kinds never convert at all.** `KindUnset`,
  `KindExpr` and `KindEveryExpr` hit its `default:` branch and return a wrapped
  `scheduler.ErrUnsupportedTrigger` — they never become a `scheduler.Trigger`, so the implication is
  vacuous for them by a **second, different mechanism** than `NeverDue() == false`. The refusal must
  be asserted (`require.ErrorIs`) and the row skipped with its reason stated, **not** swallowed by a
  nil-check. An implementer following "drive the corpus through the production chain" literally
  would have swallowed it.

**Mutation duty** as in bundle A: break the production line, observe RED, restore from a `cp`
backup (⚠ never `git checkout <path>`), `diff` to confirm.

---

## §7 — Verification notes

`internal/persistence/store` is **not** container-free — but `RunTestSQLite` is pure-Go and starts
no container, which is how both the 0-of-4 measurement and probe P-B were taken without Docker.
⚠ The new sweep's test must use `dbtest.RunTestSQLite` **directly**, not `forEachDialect`, which
unconditionally boots Postgres and MySQL (audit B-F11/C52) — and whose MySQL arm cannot seed the
fixture at all (§2.2).

The Postgres generalisation needs the owner's Docker-backed run (§2.2). The MySQL arm is a
**documented no-op**, not a pending verification.

# Audit adjudication — ADR-0181 / ADR-0182 (bundle B)

**Date**: 2026-08-14 · **Adjudicated by**: controller session, against the three lens records
`2026-08-13-adr-0181-0182-audit-lens-{a,b,c}.md` (commit `0bf19033`).

Rule #9 requires the findings be *adjudicated*, not auto-applied. This file is that record: every
finding, its disposition, and — where the disposition rests on a fact — the measurement that
settles it. The accepted fixes are folded into the spec, both ADRs and the plan in the same commit.

## §0 — The controller's own measurements

Two throwaway probes were run before adjudicating, because several dispositions turn on facts no
lens had isolated. Both are deleted; the numbers are kept here and in the premise-evidence record.

### P-A — the candidate corpus through the PRODUCTION chain

`runtime/zz_probe_neverdue_corpus_test.go`, `package runtime` (internal, so the real unexported
`convertTrigger` is in scope), driving `schedule.TriggerSpec → convertTrigger → scheduler.Trigger.Next`
at five anchors: 2026-02-01, 2026-08-01, 2026-01-31, 2026-06-14 (a Sunday), 2028-02-29 (a leap day).
EXIT=0.

This matters because both lens A and lens B measured `scheduler.Trigger.…` constructors **directly**,
which is not the path a definition reaches — lens A retracted its own F9 for exactly that reason.

**Never due at every anchor** (the reject set):

```
Every(0)  Every(-1s)
EveryRandom(5s,5s)  EveryRandom(10s,5s)  EveryRandom(0,5s)  EveryRandom(-1s,5s)
Daily(0)  Weekly(0,[Mon])  Monthly(0,[15])
Daily(1,Hour=24)  Daily(1,Minute=60)  Daily(1,Second=60)
Weekly(1,[Mon],Hour=25)  Monthly(1,[15],Hour=25)
Monthly(1,[0])  Monthly(1,[32])  Monthly(1,[-32])  Monthly(1,[15,32])
Weekly(1,[-1])  Weekly(1,[-1,-3])  Weekly(4,[-1])
```

**Due somewhere** (must NOT be rejected):

```
Monthly(12,[31])      never 2026-08-31 2027-01-31 never never   <- ANCHOR-DEPENDENT
Monthly(1,[31])       due at all five
Monthly(1,[-1])       2026-02-28 2026-08-31 2026-02-28 2026-06-30 2028-03-31
Monthly(1,[-31])      2026-03-01 2026-10-01 2026-03-01 2026-07-01 2028-03-01
Monthly(1,nil)        due at all five
Weekly(1,[Weekday(7)])  due at all five
Weekly(1,[Weekday(9)])  due at all five
Weekly(1,[-1,Mon])      due at all five   <- a MIXED weekday set is due
Weekly(1,nil)           due at all five
Weekly(1,[Mon]) Daily(1,09:00) Daily(1,noAtTimes) Every(1h) EveryRandom(5s,10s)
```

**Cron** (scoped OUT by owner decision, measured for the record):
`Cron("not a cron")`, `Cron("")` and `Cron("0 0 30 2 *")` are never due at every anchor;
`Cron("0 9 * * 1-5")` is due at every anchor.

### P-B — which orphan-sweep predicate actually reclaims

`internal/persistence/store/zz_probe_orphan_threshold_test.go`, `package store_test`, SQLite via
`dbtest.RunTestSQLite` (pure-Go, no container). Eight rows seeded by raw `INSERT` so the *encoding*
is controlled: four orphans in the current fixed-width layout, one orphan in the pre-ADR-0151
**trimmed** layout, and three controls. EXIT=0.

```
fixed-width zero = "0001-01-01T00:00:00.000000000Z"
trimmed     zero = "0001-01-01T00:00:00Z"
epoch sentinel   =  1970-01-01T00:00:00.000000000Z

equality(zero)     seeded=8 deleted=4 survivors=[armed-future-recurring armed-past-recurring
                                                 healthy-expired-oneshot orphan-legacy-trimmed]
threshold(epoch)   seeded=8 deleted=5 survivors=[armed-future-recurring armed-past-recurring
                                                 healthy-expired-oneshot]
```

The control lens A did not have is **`armed-past-recurring`** — a recurring row with a *past*
`next_run` (2020), which is precisely the population ADR-0134 exists to protect. The epoch
threshold spares it. That is the evidence that the threshold form is not a widening of
`PruneTimers`' hazard in disguise.

## §1 — Dispositions: ADR-0181 (orphan reclamation)

| finding | lens | disposition | note |
|---|---|---|---|
| equality misses the trimmed encoding | A-F1, B-F2 | **ACCEPTED** | Predicate becomes `next_run < <epoch> AND trigger_kind IN (<recurring>)`. Measured P-B: equality 4/5, threshold 5/5, all three controls spared. Sentinel is the **Unix epoch**, not lens A's year-1000: it is inside MySQL's `DATETIME` range, lexicographically correct on SQLite under both encodings, and no legitimately armed recurring row can precede it. |
| MySQL cannot hold a zero `next_run` | A-F2, B-F3, C42 | **ACCEPTED** | Replaces the ASSUMPTION with ADR-0176's measured `Error 1292`. MySQL's orphan population is provably **empty**; the sweep is a structural no-op there, not an unverified generalisation. Non-strict `sql_mode` caveat (`'0000-00-00'`) recorded — the threshold form catches that too, an equality never could. Remaining assumption narrowed to **Postgres only**. |
| the sweep is unreachable dead code | B-F5 | **ACCEPTED** — owner's option (b) | `persistence.NeverDueTimerReclaimer` optional-capability interface + a documented type assertion. All three public constructors return the `Pruner` **interface** (verified), so without this there is no consumer wiring that can reach the method at all — the ADR's "a consumer wiring only the public interface cannot reach it" understated it. |
| irreversible delete with no record | B-F6 | **PARTIALLY ACCEPTED** | Accepted: the ADR now states the sweep does **not** unpark the instance, and points operators at `ListArmed`/`Stats` before sweeping. **Rejected — per-row identity logging**: capturing identities needs either a pre-`SELECT` (which reintroduces exactly the TOCTOU B-F13 warns about) or `DELETE … RETURNING` (absent on MySQL). The sweep stays a single-statement `DELETE` returning a count. |
| `Stats.NextFireAt` promised, never tested | B-F7 | **ACCEPTED** | Rule #11's zombie-scope shape. P1 gains a before/after `Stats()` assertion. |
| P1's fixture names a DUE trigger | B-F10, C30 | **ACCEPTED** | Measured P-A: `Weekly(1,nil)` is due at all five anchors (empty weekday set is substituted with Sunday). P1 seeds `next_run` **directly**; trigger kinds are chosen only for their `trigger_kind` column value, and the plan now says the trigger's schedulability is irrelevant to P1. |
| the Verify command needs Docker | B-F11, C52 | **ACCEPTED** | `-run '^TestPrune'` matches `TestPruner`, which runs under `forEachDialect` (Postgres + MySQL, no skip guard). Replaced with an exactly-named SQLite-only test plus `-v`, per pitfall #5. |
| concurrency undefined | B-F13 | **ACCEPTED** | ADR states single-statement `DELETE`, and why (atomic predicate re-evaluation against a concurrent `upsertTimer`). |
| C1, C2, C3, C22, C49, C50, C53, C54 | C | **CONFIRMED, no action** | |

## §2 — Dispositions: ADR-0182 (the authoring gate)

| finding | lens | disposition | note |
|---|---|---|---|
| "out-of-range weekday" is UNSOUND | A-F3, B-F8(a), C29 | **ACCEPTED** | Measured P-A: `Weekly(1,[Weekday(7)])` and `[Weekday(9)]` are due at **all five** anchors — a weekday above Saturday stays a raw day offset. Replaced by "a **non-empty, all-negative** weekday set"; `[-1,Mon]` is due, so the quantifier is load-bearing. |
| four never-due classes missing | A-F5, B-F9, C25–C27 | **ACCEPTED** | List restated as the **closed set of executed `Trigger.Next` branches**, not prose. Adds non-positive `Every`, `EveryRandom(min<=0)`, and `Monthly(0,…)`. |
| negative day-of-month is LEGAL | A-F6, B-F9, C31 | **ACCEPTED** | Measured P-A: `Monthly(1,[-1])` and `[-31]` are due. Rule is exactly `d == 0 \|\| d > 31 \|\| d < -31`, matching `monthDaysSchedulable`. |
| parseable-but-impossible cron | B-F8(b), C28 | **OWNER DECISION — cron OUT OF SCOPE** | Both cron never-due causes leave the gate. Catching either needs `definition/model` to import `robfig/cron/v3` (verified: it does not today), which the owner declined. `Cron("0 0 30 2 *")` **stops being cited as the motivation**; it is recorded as a measured, known incompleteness. |
| the two prescribed corpora contradict | A-F4 | **ACCEPTED, reshaped** | Not resolved by picking a winner. The cross-check assertion is **one-directional**: `predicate(spec) ⟹ !ok at every probed anchor`. Completeness is *not* asserted — that is the sound-never-complete cap of §1, so the contradiction dissolves. One corpus, two assertions per row. |
| cross-check can't survive the split; can't test the real chain | A-F8, A-F10, B-F4 | **ACCEPTED — one fix for all three** | The cross-check moves to **`package runtime`** (internal test), where the real unexported `convertTrigger` **and** `Next` are in scope and `model`'s exported predicate is importable. It then exercises the exact production chain, and it survives the scheduler spin-out because `runtime` will import the external module. No new public API — lens A's proposed `runtime.ConvertTrigger` export is **rejected as unnecessary**. |
| a never-due one-shot deadline | A-F9 | **RETRACTED by its author; replacement MINOR ACCEPTED** | `schedule.At(time.Time{})` has `AbsTime() ok=false`, so `convertTrigger` maps it to `After(0)`, which is DUE. The executed reason is now recorded in the spec so the next reader does not re-open it. |
| `WaitEvery` is an uncovered third trigger field | C41 | **ACCEPTED** — owner | Verified: `armWaitReminder` (`engine/step_nodes.go:689`) emits `ScheduleTimer{Kind: TimerInWait}` from it — the identical durable-arm path. The gate reads `TimerTrigger` **and** `WaitTrigger`/`WaitEvery`. |
| `toWire(n).TimerTrigger` is not a `TriggerSpec` | C39 | **ACCEPTED** | It is `*model.TriggerWire`; the decode is `model.ReadTrigger(w, flat, recurringFlat)`. Named explicitly so an implementer does not hit a compile error. |
| the legacy flat string form | C40 | **ACCEPTED as a stated limit** | Verified `ReadTrigger`: a nil wire with a non-empty flat string decodes to `AfterExpr` / `EveryExpr` — **expression kinds, resolved by the engine at run time and not statically judgeable**. The gate cannot judge them by construction; the arm guard remains their only layer. Stated, not silently skipped. |
| `KindCallActivity`'s referenced definition is not recursed | C12 | **NOTED, out of scope** | The referenced definition is validated when it is itself built. Recorded as a reach limit of the gate. |
| the spin-out hedge was stripped | B-F12 | **ACCEPTED** | ADR-0134 left the spin-out an explicitly out-of-scope **follow-up**. The hedge is restored with the citation. This mattered: it is the claim carrying the bundle's most consequential decision. |
| a fixed corpus cannot detect drift | B-F14 | **ACCEPTED in the sound direction** | Fixed corpus stays as the regression floor; a **deterministic generated sweep** (fixed field grids × fixed anchors) is added for the one-directional property. `go test -fuzz` in CI is **rejected** — non-deterministic gate, and the property it could check is the unsound direction the generated sweep already covers. |
| `ErrTriggerNeverDue` does not exist | C43 | **ACCEPTED** | Verified: `grep` EXIT=1, zero hits. ADR-0176's spec said it exists "**in substance**" — the refusal at `scheduler/scheduler.go:580` wrapping `ErrUnsupportedTrigger` with "trigger can never fire". The hedge was stripped on restatement. Claim corrected; creating the sentinel stays out of scope. |
| stale citation `scheduler.go:575-578` | C44 | **ACCEPTED** | Now 578–581; the return is at **580**. |
| `EveryRandom` spelled three ways | C45 | **ACCEPTED** | Actual predicate is `min <= 0 \|\| min >= max`; every occurrence normalised. |
| "the count four appears in no single source document" | C17 | **ACCEPTED** | False as written — ADR-0176's Deferred list *is* four bullets. What is unique to `HANDOVER.md` is the **membership** (it swaps calendar-math for orphan migration). A recap sentence over-generalising what it compressed — the exact defect class Premise Discipline names. |
| plan says 2 packages / 2 phases | C46, C47 | **ACCEPTED** | It was 3 and 3 then; after this fold it is **5 packages** (`internal/persistence/store`, `persistence`, `definition/model`, `runtime`, `scheduler`) and **5 phases**. |
| P3 misses two more stale comments | C48 | **ACCEPTED** | `runtime/jobstore.go:48-49` ("`Pruner.PruneTimers` does not delete it") becomes half-false once a second sweep exists. |
| `scheduler/trigger.go` `EveryRandom` doc rot | A-F7 | **ACCEPTED** | Three statements (lines 111, 200, 231–232) claim the bounds are unvalidated; the code returns `!ok` for `min <= 0 \|\| min >= max`. Load-bearing here: ADR-0182 cited them. Folded into P3. |
| self-containment guard direction | C33 | **ACCEPTED (wording)** | The guard is **outbound-only** (`scheduler → wrkflw/*`). It forbids moving calendar math out; it does **not** "protect" against `definition/model → scheduler`. |
| "breaking for authoring, not loading" | B-F1 | **ACCEPTED** | `Validate` has exactly one production caller, `definitionCore.build()`, reached by both `NewBuilder().Build()` and `NewLoader(yaml).Build()` — and `ParseYAML` returns a `DefinitionLoader`. A consumer parsing YAML at startup fails **at startup**, not in CI. Blast radius restated precisely, with a migration note. |

## §3 — What the audit changed about the delivery

1. **The gate's rejection list was wrong in both directions at once** — unsound on two classes it
   named (weekday, negative day-of-month) and silent on four it did not (`Monthly(0,…)`,
   non-positive `Every`, `EveryRandom(min<=0)`, and — until the owner scoped it out — its own
   motivating cron). A prose enumeration of a code path's branches rots; the list is now a table of
   executed verdicts.
2. **Both ADRs shipped a promise nobody was going to build** — 0181's `Stats.NextFireAt` fix had no
   test, and 0181's whole operator story had no reachable API. Rule #11's zombie-scope shape, twice
   in one bundle.
3. **The one mitigation for the design's largest accepted risk could not do its job.** The
   cross-check test was specified where it could reach neither the real conversion nor a home that
   survives the split it was scoped around. Moving it to `runtime` fixes all three findings at once.
4. **A measurement in an adjacent in-repo document refuted an ASSUMPTION nobody re-read.** MySQL's
   `Error 1292` was measured during ADR-0176 and sat in `docs/specs/`; the bundle labelled the same
   claim "unverified — needs Docker" and prescribed a Docker run that cannot execute.

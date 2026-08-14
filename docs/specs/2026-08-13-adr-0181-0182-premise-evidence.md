# Recon D — ADR-0176's four deferred gates + orphan-row reclamation

Worktree: `/Users/zakyalvan/Documents/RND/wrkflw/.claude/worktrees/agent-acf7c87ef72b5fa81`
Date: 2026-08-13. Docker NOT used, no containers attempted.

## STEP 0 — worktree verification (PASS)

```
$ git log --oneline -3
12c9d7e3 docs: codify that docs/specs holds a delivery's evidence records too
02430c5c docs: record the ADR-0176 audit branches as deleted
bf348974 docs: hand over a clean main after ADR-0176 shipped
$ git status --porcelain
(empty)
$ ls docs/adr/0176-*.md docs/specs/2026-08-13-*.md
docs/adr/0176-reject-never-due-timer-triggers.md
docs/specs/2026-08-13-adr-0176-audit-lens-a.md
docs/specs/2026-08-13-adr-0176-audit-lens-b.md
docs/specs/2026-08-13-adr-0176-audit-lens-c.md
docs/specs/2026-08-13-adr-0176-measurements.md
docs/specs/2026-08-13-never-due-timer-triggers.md
```
Bundle present, incl. THREE audit-lens records the brief did not name.

---

## §5 (done first) — RE-COUNT of the arm sites and the guard

**Answer: exactly FOUR guard sites, and they cover ALL THREE scheduler-entry call sites in
`runtime` production code. No unguarded arm path found.**

`neverDueNextRun` — `runtime/timerops.go:103`:
```go
func neverDueNextRun(next time.Time, ok bool) bool {
	return !ok || next.IsZero()
}
```

Guard sites (grep-derived, non-test):

| # | site | file:line | form |
|---|---|---|---|
| 1 | `timerJobsFor` | `runtime/timerops.go:178` | `if neverDueNextRun(next, ok)` |
| 2 | `scheduleStartTimerJob` | `runtime/timerops.go:505` | `if next, ok := strig.Next(...); neverDueNextRun(next, ok)` |
| 3 | `jobStore.Load` | `runtime/jobstore.go:93` | `if sj.NextRun().IsZero()` — equivalent form, NOT a call |
| 4 | post-commit pre-`Activate` re-check | `runtime/processdriver.go:780` | `if sj.NextRun().IsZero()` — equivalent form, NOT a call |

⚠ Only **two** of the four literally call `neverDueNextRun`. `grep neverDueNextRun` alone
finds 2 production sites + 1 comment; the counting must be done over the
**refusal-counter increments**, which is exact:

```
$ grep -rn "timerArmsRefused" --include="*.go" . | grep -v _test.go
runtime/jobstore.go:110:			j.driver.obs.timerArmsRefused.Add(ctx, 1)
runtime/timerops.go:188:				driver.obs.timerArmsRefused.Add(ctx, 1)
runtime/timerops.go:506:		driver.obs.timerArmsRefused.Add(ctx, 1)
runtime/observability.go:28:	timerArmsRefused  metric.Int64Counter
runtime/observability.go:56:		... "wrkflw_timer_arms_refused_total" ...
runtime/processdriver.go:794:				driver.obs.timerArmsRefused.Add(ctx, 1)
```
4 increments. Confirms measurements §16.3's "all four".

**Scheduler-entry sites in `runtime` production code — counted, not assumed:**
```
$ grep -rn "\.Schedule(\|\.Activate(" --include="*.go" . | grep -v _test.go | grep -v /scheduler/ | grep -v runtimetest
runtime/timerops.go:385:		if aerr := driver.sched.Activate(ctx, j); aerr != nil {      # RehydrateTimers
runtime/timerops.go:517:	return driver.sched.Schedule(ctx, job)                            # scheduleStartTimerJob
runtime/processdriver.go:801:			if aerr := driver.sched.Activate(ctx, sj); aerr != nil {  # post-commit
scheduler/scheduler.go:597:	if err := s.Activate(ctx, sj); err != nil {                       # inside Schedule, not runtime
```
Three entries in `runtime`; each is preceded by a guard:
- `timerops.go:385` consumes `NewJobStore(driver).Load(ctx)` → guard #3 already filtered it.
- `timerops.go:517` is guarded by #2 twelve lines above.
- `processdriver.go:801` is guarded by #4 immediately above, in the same loop.

**Handover claim "four sites" — SURVIVES.** ADR Decision 2's enumeration is correct.

⚠ **DOC DIVERGENCE found (minor, in shipped code).** `neverDueNextRun`'s own doc comment
(`runtime/timerops.go:86-91`) enumerates **THREE** sites and does not mention the
post-commit re-check:

> "It is applied at the two arm sites that compute a next run from a trigger:
> timerJobsFor and scheduleStartTimerJob. The third, jobStore.Load, applies the same
> condition in its own form…"

The re-check (`processdriver.go:780`) was added at `/code-review` (§16.2) and the predicate's
comment was not updated. It is an undercount inside a comment, not a behavioural defect — but
it is exactly the "enumerations rot" class, and it is the sentence a future reader would
inherit.

---

## §1 — What the SOURCE DOCUMENTS actually say (read backwards, per brief)

### The measurements file supersession chain
`§15 supersedes §13, which supersedes §1–§10`, `§14` holds the arming instants — and the brief
MISSED a later layer: **§16 (`/code-review`) supersedes §15**, and **§17 (`/security-review`)**
adds a further verified set. Correct reading order: **§17 → §16 → §15 → §14 → §13 → §1–§12.**

### ADR-0176, verbatim on the deferrals (`docs/adr/0176-…:191-207`)

> **Deferred, each for a measured reason:**
> - **A deploy-time `model.Validate` gate** — its prescribed mechanism was an **import cycle**
>   (`definition/event` imports `definition/model`). A mechanism exists (the wire form, via
>   `toWire`) and must live in `validateStructure` or every nested sub-process is exempt. Also
>   `Build()` already rejects every recurring `DeadlineTimer`, making part of that gate dead code.
> - **A step-time engine gate** — measured to **wedge a running instance**: the step rolls back
>   with the already-fired timer row restored at a past `next_run` and fails identically forever,
>   which is worse than the inert row it replaces.
> - **`StepOptions.SchedulingLocation`** — only a step-time gate needs it; it does not eliminate
>   both error directions (nil→UTC gives a direct-`engine.Step` consumer the very false
>   rejection it exists to prevent), and cron-under-`FixedZone` is unfixable by any location
>   (ADR-0136).
> - **Moving the calendar math to a shared package** — unnecessary once the guard lives in
>   `runtime`, and **forbidden** by `scheduler/selfcontainment_guard_test.go` …

⚠ **The handover's enumeration is subtly wrong.** `HANDOVER.md` lists deferral #4 as
"migrating existing orphan zero-`next_run` rows". The **ADR's** fourth bullet is
"**Moving the calendar math to a shared package**". The migration deferral is real but lives in
a *different* list — the ADR's **Costs accepted** (`:187-189`):

> - Existing zero-`next_run` rows are not migrated. The rehydration guard stops them wedging
>   boot; nothing deletes them, and `PruneTimers` provably cannot. Manual remediation is
>   documented.

The **spec** (§6.3) lists SIX deferrals and puts migration among them, alongside two the ADR
folded elsewhere (the `ErrTriggerNeverDue` sentinel — already exists; the shared calendar
package). So "the four gates ADR-0176 deferred" is a compression merging two lists.
**Not a defect, but the count is not four in any single source document**: ADR = 4 deferred +
1 accepted cost; spec §6.3 = 6 deferred.

### On the `model.Validate` gate — the spec is more specific than the ADR (§6.3)

> Its prescribed mechanism was an **import cycle** — `definition/event` imports
> `definition/model`, so `model` cannot see `event.*.Timer`, and **no accessor exposes them**.
> A mechanism exists (the wire form, `NodeWire.TimerTrigger` via `toWire`), and it must live in
> `validateStructure`, not `Validate`, or every nested sub-process is exempt. Also: `Build()`
> **already** rejects every recurring `DeadlineTimer` (`ErrDeadlineTriggerRecurring`), and
> `Recurring()` is true for `KindEveryExpr`, which made one prescribed test unconstructable and
> that gate partly dead code.

### The killer constraint on any static gate — measurements §9 (still standing)

> A static `Validate()` can only be SOUND, never complete:
> `Validate() != nil ⟹ ∀t: Next(t) is !ok`. **The CONVERSE IS FALSE.**
> … ⚠ The arm-time guard is **NOT defence-in-depth**: it is the ONLY layer that can catch the
> anchor-dependent class.

Measured there: `Monthly(12,[31])` is `ok=false` at a February anchor and `ok=true` at an August
one. **A deploy-time gate can therefore never replace the shipped arm guard — only add early
feedback.** The single most important fact for scoping the deferred work.

---

## §2 — The import-cycle claim, established by BUILDING

### Probe A — `definition/model` imports `definition/event` (the ADR's claim)
```go
// definition/model/zzz_probe_cycle.go
package model
import _ "github.com/kartaladev/wrkflw/definition/event"
```
```
$ go build ./... ; echo "EXIT=$?"
package github.com/kartaladev/wrkflw/casbinauthz
	imports github.com/kartaladev/wrkflw/service from policyadmin.go
	imports github.com/kartaladev/wrkflw/definition/model from instance.go
	imports github.com/kartaladev/wrkflw/definition/event from zzz_probe_cycle.go
	imports github.com/kartaladev/wrkflw/definition/model from event.go: import cycle not allowed
EXIT=1
```
**CYCLE CONFIRMED.** `definition/event/event.go:9-13` imports `model`, `model/validate`,
`definition/schedule`. Only *test* files under `definition/model/` import `event`, and they are
all `package model_test` (verified: `head -15 definition/model/validate_test.go` →
`package model_test`). The cycle is real for production code and invisible to the tests.

### Probe B — `definition/model` imports `scheduler`
```go
package model
import _ "github.com/kartaladev/wrkflw/scheduler"
```
```
$ go build ./... ; echo "EXIT=$?"
EXIT=0
```
**NO CYCLE.** ⚠ REFINES the ADR: the schedulability *predicate* (`scheduler.Trigger.Next`) IS
reachable from `model`. Only the *node-typed timer data* (`event.StartEvent.Timer`) is not.

### Probe C — `definition/model` imports `runtime` (where `convertTrigger` lives)
```
package github.com/kartaladev/wrkflw/casbinauthz
	… imports github.com/kartaladev/wrkflw/runtime from zzz_probe_cycle.go
	imports github.com/kartaladev/wrkflw/definition/activity from definition_registry.go
	imports github.com/kartaladev/wrkflw/definition/model from activity.go: import cycle not allowed
EXIT=1
```
**CYCLE.** `convertTrigger` (`runtime/timerops.go:44`, unexported, the only
`schedule.TriggerSpec → scheduler.Trigger` mapper in the repo) is **unreachable from `model`**.
A `model.Validate` gate needs that mapping and must either duplicate it or have it extracted to
a package importing only `definition/schedule` + `scheduler` — both of which `model` can already
reach (probe B; and `model` already imports `definition/schedule` in `accessors.go`, `node.go`,
`trigger_wire.go`).

**Probe files deleted; `go build ./...` EXIT=0 and `go vet ./definition/...` EXIT=0 afterwards.**

### Does the `toWire` route genuinely avoid the cycle? YES
`toWire` (`definition/model/node_wire.go:88`) is IN package `model` and flattens any `Node`
through its registered kind spec into `NodeWire`, whose `TimerTrigger *TriggerWire`
(`node_wire.go:42`) is also a `model` type. Reading the timer through the wire form needs **no
import of `event`** — demonstrated by probe D, where a nested `event`-package node's timer
appears in `model`-driven output.

**Handover claim on the import cycle — SURVIVES, with one correction: the cycle is
`model → event`, not `model → scheduler` (which builds clean).**

---

## §3 — `validateStructure` and nested sub-processes

### The structure (quoted)
`Validate` — `definition/model/validate.go:260-276` — is thin:
```go
func Validate(d *ProcessDefinition) error {
	var errs []error
	if d.Version < 1 {
		errs = append(errs, fmt.Errorf("%w: got %d", ErrInvalidVersion, d.Version))
	}
	if err := validateStructure(d, make(map[*ProcessDefinition]bool)); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}
```
`validateStructure` spans **lines 277–766** (`grep -n "^func " definition/model/validate.go` →
next function is `isEventTriggeredSubprocess` at 767) and recurses at **line 550**:
```go
	for _, n := range d.Nodes {
		if n.Kind() != KindSubProcess { continue }
		sub := toWire(n).Subprocess
		if sub == nil { … ErrMissingSubprocess … ; continue }
		if nestedErr := validateStructure(sub, seen); nestedErr != nil {
			errs = append(errs, fmt.Errorf("subprocess %q: %w", n.ID(), nestedErr))
		}
	}
```
**The only rule outside the recursion is the `Version >= 1` root check** — deliberately, per its
own doc comment ("applies only to the root definition").

### Probe D — executed (`definition/model/zzz_probe_nested_test.go`, deleted after)
```
$ go test -count=1 -v -run '^TestZZZProbeNestedExemption$' ./definition/model/ ; echo EXIT=$?
=== RUN   TestZZZProbeNestedExemption
  PROBE A1 root Version=0            -> workflow-definition: definition version must be >= 1
                                        (0 reserved as latest sentinel): got 0
  PROBE A2 nested Version=0          -> <nil>
  PROBE B  nested dangling flow      -> subprocess "sp": workflow-definition: flow references
                                        unknown node: flow "nf3" target "NOPE"
  PROBE C1 nested Monthly(12,[31])   -> <nil>
  PROBE C2 root-level Monthly(12,[31]) -> <nil>
  PROBE C3 root-level Cron(30 Feb)   -> <nil>
  PROBE C4 root-level Every(0)       -> <nil>
  PROBE D  marshal err=<nil>
  PROBE D  wire contains nested timer_trigger: true
  PROBE D  wire = {"id":"root","version":1,"nodes":[{"id":"s","kind":"startEvent"},
    {"id":"sp","kind":"subProcess","subprocess":{"id":"sub","version":1,"nodes":[
      {"id":"ns","kind":"startEvent"},
      {"id":"nc","kind":"intermediateCatchEvent",
       "timer_trigger":{"kind":"monthly","interval":12,"days_of_month":[31]}},
      {"id":"ne","kind":"endEvent"}], …}}, …]}
--- PASS
EXIT=0
```

**Four separate facts, all executed:**
- **A1 vs A2** — a rule placed in `Validate` (the `Version` check) **IS exempt inside a nested
  sub-process**. ADR claim CONFIRMED, demonstrated with the one rule that actually sits there.
- **B** — a rule inside `validateStructure` **DOES** reach nested sub-processes, wrapped with
  the host node id. The prescribed placement works.
- **C1–C4** — ⚠ **NOTHING today rejects a never-due timer at `model.Validate`**, root or
  nested: `Monthly(12,[31])`, `Cron("0 0 30 2 *")`, `Every(0)` all validate clean. Re-confirms
  spec §5's "a legal definition" premise on the CURRENT (post-0176) code.
- **D** — the nested node's timer IS reachable through the wire form from inside `model`, with
  no `event` import. The prescribed `toWire` mechanism is real.

**Handover claim "must live in `validateStructure` or every nested sub-process is exempt" —
SURVIVES, executed.**

⚠ **One correction to the ADR's supporting clause.** It says `Build()` already rejects every
recurring `DeadlineTimer`, "making part of that gate dead code". Source-verified:
`definition/model/validate.go:656-661` does exactly that — but at line **656, i.e. INSIDE
`validateStructure` (277–766)**, so it already recurses into sub-processes. The ADR describes it
as a `Build()` behaviour; it is really a `validateStructure` behaviour that `Build()` reaches.
Same conclusion (that half of the gate is redundant), better-located fact.

---

## §4 — `PruneTimers`: RE-COUNTED and **EXECUTED** (no Docker needed)

⚠ **The brief predicted this needed Postgres/MySQL via testcontainers. It does not.**
`internal/dbtest/sqlite.go:22` `RunTestSQLite` is pure-Go (`modernc.org/sqlite`), the
`internal/persistence/store` package has **no `TestMain`** (verified by grep), and a
`-run`-filtered invocation compiles the package but runs only the named test. So the claim was
re-derived directly rather than assumed. **No container was started.**

### (a) How many implementations? ONE, not one-per-dialect
```
$ grep -rn "PruneTimers" --include="*.go" . | grep -v _test.go
internal/persistence/store/pruner.go:192:func (p *Pruner) PruneTimers(ctx …) (int64, error)   ← the only impl
persistence/pruner.go:48:	PruneTimers(ctx context.Context, cutoff time.Time) (int64, error)  ← public interface
runtime/jobstore.go:49, runtime/timerops.go:237  ← comments only
```
ADR-0081 unified the dialects: one implementation, three dialects via `p.dialect.Rebind`.
⚠ The brief's "every implementation across dialects" presumes a plurality that no longer exists.

### (b) The predicate, verbatim (`internal/persistence/store/pruner.go:192-214`)
```go
	res, err := q.Exec(ctx,
		p.dialect.Rebind(
			`DELETE FROM wrkflw_timers
			  WHERE next_run < ?
			    AND trigger_kind IN (?, ?, ?)`),
		timeArg(p.dialect, cutoff.UTC()),
		int16(nonRecurringTriggerKinds[0]), int16(nonRecurringTriggerKinds[1]), int16(nonRecurringTriggerKinds[2]),
	)
```
```go
// pruner.go:224-228
var nonRecurringTriggerKinds = [3]schedule.Kind{
	schedule.KindUnset, schedule.KindOneTime, schedule.KindExpr,
}
```
And `definition/schedule/trigger.go:101-108`:
```go
func (s TriggerSpec) Recurring() bool {
	switch s.kind {
	case KindUnset, KindOneTime, KindExpr:
		return false
	default:
		return true
	}
}
```
**`nonRecurringTriggerKinds` is exactly the complement of `Recurring()`.** So the predicate
reads: *delete expired rows that are non-recurring.*

### Which rows it can and cannot reach — by reading the predicate
- `next_run < cutoff` is **satisfied** by a zero `next_run` (`0001-01-01` < any sane cutoff).
  The zero literal is NOT what excludes an orphan.
- `trigger_kind IN (Unset, OneTime, Expr)` is the whole exclusion. Enumerating
  `schedule.Kind` (`trigger.go:13-22`): `KindUnset 0`, `KindOneTime 1`, `KindDuration 2`,
  `KindDurationRand 3`, `KindCron 4`, `KindDaily 5`, `KindWeekly 6`, `KindMonthly 7`,
  `KindExpr 8`, `KindEveryExpr 9`.
- **Every never-due kind is recurring** → kinds 2,3,4,5,6,7,9, all outside the IN-list.
  Kinds 0/1/8 are the reachable ones, and none of them can be never-due: `After(d)`/`At(t)` are
  always `ok` with a non-zero instant, and `KindExpr` is engine-resolved before arming.
- ⇒ **The reachable set and the orphan set are DISJOINT.** `PruneTimers` cannot delete a single
  orphan, at any cutoff, on any dialect.

### (c) EXECUTED (`internal/persistence/store/zzz_probe_prune_test.go`, SQLite, deleted after)
```
$ go test -count=1 -v -run '^TestZZZProbePruneTimersOrphans$' ./internal/persistence/store/ ; echo EXIT=$?
=== RUN   TestZZZProbePruneTimersOrphans
  PROBE before prune: 5 armed rows
  PROBE   timer=zz-cronbogus       kind=4 recurring=true  next_run=0001-01-01T00:00:00Z
  PROBE   timer=zz-every0          kind=2 recurring=true  next_run=0001-01-01T00:00:00Z
  PROBE   timer=zz-monthly12-31    kind=7 recurring=true  next_run=0001-01-01T00:00:00Z
  PROBE   timer=zz-weekly1-nil     kind=6 recurring=true  next_run=0001-01-01T00:00:00Z
  PROBE   timer=zz-oneshot-past    kind=1 recurring=false next_run=2026-08-09T09:00:00Z
  PROBE PruneTimers(cutoff=2026-08-11T09:00:00Z) deleted=1
  PROBE after prune: 4 armed rows remain
  PROBE   remains timer=zz-cronbogus    kind=4 next_run=0001-01-01T00:00:00Z
  PROBE   remains timer=zz-every0       kind=2 next_run=0001-01-01T00:00:00Z
  PROBE   remains timer=zz-monthly12-31 kind=7 next_run=0001-01-01T00:00:00Z
  PROBE   remains timer=zz-weekly1-nil  kind=6 next_run=0001-01-01T00:00:00Z
  PROBE ORPHANS (zero next_run) RECLAIMED: 0 of 4; still present: 4
  PROBE Stats={Armed:4 NextFireAt:0001-01-01 00:00:00 +0000 UTC}
  PROBE kind=2 recurring=true (prune-eligible=false)   ← Every(0)
  PROBE kind=3 recurring=true (prune-eligible=false)   ← EveryRandom(2h,1h)
  PROBE kind=4 recurring=true (prune-eligible=false)   ← Cron(30 Feb)
  PROBE kind=5 recurring=true (prune-eligible=false)   ← Daily(0)
  PROBE kind=6 recurring=true (prune-eligible=false)   ← Weekly(1,nil)
  PROBE kind=7 recurring=true (prune-eligible=false)   ← Monthly(12,[31])
  PROBE kind=9 recurring=true (prune-eligible=false)   ← EveryExpr
--- PASS
EXIT=0
```
Byte-for-byte reproduction of audit lens C's numbers (`docs/specs/…-audit-lens-c.md:205-220`),
independently, on a clean tree. **Probe deleted; `go vet ./internal/persistence/store/` EXIT=0.**

### Verdict on the load-bearing claim
**"`PruneTimers` was measured deleting 1 of 5" — TRUE but MISLEADING, and the sharper form
should replace it everywhere.** The 1 it deleted was `zz-oneshot-past`, the deliberate
**non-recurring control** — a perfectly healthy expired one-shot, *not an orphan*. Of the four
actual orphans it deleted **ZERO**. The correct statement is:

> **`PruneTimers` reclaims 0 of 4 orphan rows — its predicate and the orphan set are disjoint by
> construction, not by cutoff choice.**

"1 of 5" invites the reading "it partly works, tune the cutoff". It does not partly work.
`Stats.NextFireAt` also stays pinned at `0001-01-01` — confirmed above.

⚠ **NOT verified on Postgres/MySQL** (SQLite only). The SQL is dialect-neutral and only
`Rebind`/`timeArg` differ, so I record the cross-dialect generalisation as:
`ASSUMPTION (unverified — needs Docker): the same 0-of-4 result holds on Postgres and MySQL.`
Owner verification command:
```bash
docker info >/dev/null 2>&1 && \
go test -count=1 -v -run '^TestPruner' ./internal/persistence/store/ ; echo "EXIT=$?"
```
(or re-add the probe above and run it unfiltered against the Postgres/MySQL conformance harness
in `internal/persistence/store/conformance_test.go:52`).

---

## §6 — `StepOptions`

`engine/step.go:23-70`. **Public module-root API** (package `engine`), a plain exported struct
consumed by value. Fields, complete:

| field | type | note |
|---|---|---|
| `Mode` | `StepMode` | Macro (default) / Micro |
| `DefaultRetryPolicy` | `*model.RetryPolicy` | fallback |
| `OverrideRetryPolicy` | `*model.RetryPolicy` | runtime's per-action seam |
| `Evaluator` | `ConditionEvaluator` | nil ⇒ pure package-global |
| `IDGenerator` | `IDGenerator` | nil ⇒ deterministic counter |
| `CompensationStallAfter` | `time.Duration` | ADR-0175; zero disables |

**Who constructs it:** exactly **ONE** non-test site in the whole repo —
`runtime/processdriver.go:615`, `engine.Step(stepCtx, def, st, t, engine.StepOptions{…})`.
Nothing in `examples/` constructs it (grep: no hits). 867 occurrences of `StepOptions{` in
`*_test.go`.

**What adding `SchedulingLocation` would break:**
- *Compilation*: nothing. It is an **additive field on a keyed struct literal**; all 868 known
  construction sites are keyed. Go only breaks on unkeyed composite literals, and `go vet`'s
  `composites` check already forbids those cross-package.
- *Semantics*: this is where the ADR's objection bites, and it is the real cost. The zero value
  of `*time.Location` is `nil`, and `nil` means UTC by convention
  (`runtime/timerops.go:30-37`, `schedulingLocation()` falls back to `time.UTC`). So a consumer
  calling `engine.Step` **directly** — the library-first path CLAUDE.md protects — gets `nil` →
  UTC silently, and if their scheduler runs in another zone the gate computes schedulability at
  the wrong anchor. That is the "false rejection it exists to prevent", verbatim from the ADR.
- *Architecturally*: the engine core has no scheduler to ask. `runtime` does not need this field
  at all — it already gets the answer from the scheduler itself via
  `schedulingLocation()`/`locatedScheduler`. So the field would exist **solely** to serve a
  step-time gate, which is itself deferred as harmful.

⇒ **`StepOptions.SchedulingLocation` has no standalone justification. It is strictly downstream
of the step-time gate and must not ship without it.**

---

## §7 — Scope judgement, independence, and dependency ordering

### Public-API blast radius
| piece | package | public? | breaking? |
|---|---|---|---|
| deploy-time `model.Validate` gate | `definition/model` | **PUBLIC** (`model.Validate`, and `Build()` calls it) | **YES, behaviourally** — definitions that build today start failing |
| step-time engine gate | `engine` | **PUBLIC** (`engine.Step`) | YES — new error from `Step` |
| `StepOptions.SchedulingLocation` | `engine` | **PUBLIC** | additive; compile-safe |
| orphan-row reclamation | `internal/persistence/store` (+ maybe `persistence`) | internal impl; **public if a new `Pruner` method is added** | additive |
| extracting `convertTrigger` | new pkg + `runtime` | new public pkg if module-root | additive |

⚠ The `model.Validate` gate is the **only genuinely breaking** one, and its blast radius is
larger than it looks: probe §7 of the measurements shows stored definitions are NOT re-validated
on read (`PutDefinition`/`GetDefinition` never call `Validate`), so it breaks **authoring**, not
loading. That is the good news the ADR already banked.

### Independence
- **Orphan reclamation (backlog 23) is FULLY INDEPENDENT.** It touches only the store's prune
  predicate (or adds a new targeted sweep). It needs no `Next`, no `convertTrigger`, no engine
  change, and — crucially — it is **not blocked by the anchor-dependence problem**, because it
  deletes rows by an *observed* zero `next_run`, not by predicting schedulability. **Ship it
  first, alone.** ⚠ It must NOT be implemented by widening `PruneTimers`' `trigger_kind` list:
  that would delete still-armed recurring rows, which is exactly the bug ADR-0134 fixed and the
  reason the IN-list exists. It needs its own predicate (`next_run = zero AND recurring`), and
  a decision on whether that is a new `Pruner` method or an opt-in admin sweep.
- **The `model.Validate` gate is independent of the engine/`StepOptions` pair**, but has a
  prerequisite of its own: a cycle-free `schedule.TriggerSpec → scheduler.Trigger` mapping
  (probe C). Its value is *early authoring feedback only* — it can never be complete (§9).
- **Step-time gate and `StepOptions.SchedulingLocation` MUST ship together or not at all.** The
  field exists only to serve the gate; the gate without the field computes at the wrong anchor.
  Both are currently deferred for a *measured harm*, and nothing in this recon weakens that.

### Recommended dependency ordering
1. **Orphan reclamation (backlog 23)** — independent, internal, no cycle, no anchor problem,
   and it closes the only item with a *measured* zero-mitigation (`0 of 4`). Highest value per
   unit of risk.
2. **Extract the trigger conversion** (`convertTrigger` → a package importing only
   `definition/schedule` + `scheduler`) — pure refactor, unblocks #3, and is provably
   cycle-free (probe B). ⚠ Check `scheduler/selfcontainment_guard_test.go` first: it bans
   `wrkflw/*` imports *from scheduler production code*, which this does not do (the new package
   imports scheduler, not vice-versa) — but the guard has already surprised two audit lenses,
   so verify by mutation before designing on it.
3. **Deploy-time `model.Validate` gate**, placed in **`validateStructure`** (line 277–766), fed
   by `toWire(n).TimerTrigger`, restricted to the **anchor-independent** never-due classes only
   (`Every(0)`, `Daily(0)`, `Weekly(0,…)`, `EveryRandom(min>=max)`, malformed cron, out-of-range
   day/weekday/clock). It must **NOT** attempt `Monthly(12,[31])` — that is anchor-dependent and
   a static rejection would be unsound. Skip the recurring-`DeadlineTimer` half: already covered
   at `validate.go:656`.
   ⚠ **SUPERSEDED 2026-08-14 by the audit.** This recommendation's reject list is **unsound in two
   places and silent in four**: an out-of-range *weekday* and a *negative* day-of-month are both
   measurably DUE and must not be rejected, while `Monthly(0,…)`, non-positive `Every`,
   `EveryRandom(min<=0)` and both cron classes are never-due and unlisted. The corrected closed set
   is spec §3.1; the measurements are in `…-audit-adjudication.md` §0 probe P-A. Do not implement
   from this paragraph.
4. **Step-time gate + `StepOptions.SchedulingLocation`** — jointly, and only if someone first
   refutes the measured "wedges a running instance" finding. **Recommend leaving deferred.**

### What is already CLOSED and should not be re-scoped
- ⚠ **CORRECTED 2026-08-14 (audit C43/C44).** This line originally read "The `ErrTriggerNeverDue`
  sentinel (exists: `scheduler/scheduler.go:575-578`)". **No such symbol exists** — `grep -rn
  ErrTriggerNeverDue --include="*.go" .` returns EXIT=1, zero hits. ADR-0176's spec said it exists
  "**in substance**"; the hedge was stripped on restatement. What exists is the refusal at
  `scheduler/scheduler.go:580` (the cited range has also rotted; it is now 578–581) wrapping
  `ErrUnsupportedTrigger` with the text *"trigger can never fire"*.
- Moving calendar math to a shared package (forbidden by the self-containment guard; unnecessary
  now the guard lives in `runtime`).
- `EveryRandom(min>max)` — closed at `/code-review` (§16.1), was handover backlog item 25.


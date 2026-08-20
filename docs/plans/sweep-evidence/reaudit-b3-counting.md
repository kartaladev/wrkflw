# Re-audit B3 (REVISION) — the counting lens

Bundle commit `dd76a17b`. **Step 0 PASSED**: all five bundle files present in the
worktree (spec, evidence, ADR-0185, ADR-0186, plan).

Predecessor: `docs/plans/sweep-evidence/audit-b3-counting.md` (bundle `3f317b63`,
28 findings, 4 Critical). This report re-derives the REVISION's counts from source
**at `dd76a17b`**, and specifically tests the revision's claim that it re-derived
rather than inherited.

Method: for every count, ask (a) what does the grep's **net** fail to match, and
(b) what **commit** was it measured against. Findings written one at a time.

---

## R-1 — CONFIRMED — the 29 pin count, AND the net is now provably closed

**Claim**: evidence §7 row 1 — *"**29** — httpcore 11, gin 7, fiber 5, stdlib 5, parity 1"*.

**Re-derived at `dd76a17b`**:
```
$ grep -rnE 'httpcore\.Actor\{|"actor"|"by"' transport/ --include='*_test.go' | wc -l
29
per-package: httpcore 11, gin 7, fiber 5, stdlib 5, parity 1   (sum 29 ✓)
files: 9 ✓   packages: 5 ✓
```

**Net closure re-derived** (the thing the previous audit's C-1 caught): `Actor`-typed
fields in `transport/http/httpcore/dto.go` are **exactly three** —
`ClaimInput.Actor` (`:44`, `json:"actor"`), `CompleteInput.Actor` (`:50`,
`json:"actor"`), `ReassignInput.By` (`:66`, `json:"by"`). `grep -n 'Actor ' dto.go`
returns those three and nothing else. So the two-key net **provably** covers the
whole body-actor surface; it is no longer a sample.

**Deliberate exclusion checked**: six further `Actor: authz.Actor{…}` lines exist in
transport tests (`endpoints_test.go:458,521,550`, `stdlib/errors_test.go:174`,
`stdlib/coverage_test.go:82,114`). All six are `svc.ClaimTask(...)` **service-level**
setup, not HTTP bodies — correctly outside the pin set.

**Verdict: CONFIRMED.** The revision fixed the net and the net is now closed by
construction, not by sampling.

## R-2 — MINOR — the `expreval.New(` command returns **5** lines, not the 4 the table reports

**Claim as written**: evidence §7 row 6 —
*"`grep -rn "expreval\.New(" --include='*.go' . | grep -v _test` → **4** —
`authz/authz.go:23`, `internal/authz/casbin/authorizer.go:30`,
`engine/conditions.go:43`, `runtime/processdriver_options.go:200`"*.
Restated in spec §4.3 / ADR-0185 D4 as "four instances".

**Re-derived at `dd76a17b`** — the command as written returns **five** lines:
```
runtime/processdriver_options.go:200   driver.conditionEval = expreval.New(expreval.WithTimeout(d))
internal/authz/casbin/authorizer.go:30 attrEval: expreval.New()
authz/authz.go:23                      var attrEval = expreval.New()
engine/conditions.go:43                var conditions = expreval.New(expreval.WithTimeout(0))
engine/step.go:41                      // timeout-capable evaluator (e.g. expreval.New(expreval.WithTimeout(d)),
```
The fifth is a **godoc comment** in `engine/step.go:41`, not a constructor call.

**Verdict: claim CONFIRMED (4 real instances), evidence-as-written WRONG (5 lines).**

**Damage if acted on**: an implementer told "four `expreval.New(` instances" runs the
recorded command, sees five, and either patches a comment or concludes the table
rotted. Small, but this is the exact "the command does not produce the number"
failure this lens exists for.

**Proposed replacement**: *"**four** constructor calls —
`authz/authz.go:23`, `internal/authz/casbin/authorizer.go:30`,
`engine/conditions.go:43`, `runtime/processdriver_options.go:200`. (`grep -rn
"expreval\.New(" --include='*.go' . | grep -v _test` returns five lines; the fifth,
`engine/step.go:41`, is a godoc example inside a comment.)"*

---

## R-3 — MINOR — "274 `NewUserTask(` **sites**" is 273 call sites + 1 declaration

**Claim as written**: evidence §7 row 8 — *"`NewUserTask(` sites | `grep -rn
"NewUserTask(" --include='*.go' . | wc -l` | **274**"*. Inherited verbatim from
`audit-b3-counting.md` C-20, which called them *"274 `NewUserTask(` **call sites**"*.

**Re-derived at `dd76a17b`**:
```
$ grep -rn "NewUserTask(" --include='*.go' . | wc -l
274
$ grep -rn "func NewUserTask(" --include='*.go' .
definition/activity/activity.go:161:func NewUserTask(id string, opts ...UserTaskOption) model.Node {
⇒ 273 call sites (252 in _test.go, 21 non-test, + the 1 declaration)
```

**Verdict: WRONG by one.** The population is **273** call sites; 274 counts the
`func NewUserTask` declaration.

**Damage if acted on**: trivial arithmetically, but it is an **inherited** number
restated without re-deriving the denominator — exactly what the evidence file's own
preamble promises it did not do (*"nothing here is inherited"*). If the 128 figure
was derived against the same net, it inherits the same off-by-one class.

**Proposed replacement**: *"**273** `NewUserTask` call sites (the 274th match is the
declaration at `definition/activity/activity.go:161`)."*

---

## R-4 — CONFIRMED — spec §6.1's NEW per-file split is exact

**Claim**: spec §6.1 table (spec:717-723) — `httpcore` 11 (`endpoints_test.go` 6,
`dto_test.go` 5); `gin` 7 (`gin_test.go` 4, `gin_coverage_test.go` 3); `fiber` 5;
`stdlib` 5 (`coverage_test.go` 2, `errors_test.go` 2, `stdlib_test.go` 1); `parity` 1.

**Re-derived at `dd76a17b`**:
```
$ grep -rcE 'httpcore\.Actor\{|"actor"|"by"' transport/ --include='*_test.go' | grep -v ':0$'
fiber/fiber_test.go:5   gin/gin_coverage_test.go:3   gin/gin_test.go:4
httpcore/dto_test.go:5  httpcore/endpoints_test.go:6 parity/parity_test.go:1
stdlib/coverage_test.go:2  stdlib/errors_test.go:2  stdlib/stdlib_test.go:1
```
Every cell exact; sum 29. This per-file split is **new in the revision** (the audit
gave only per-package) and it is right.

**Verdict: CONFIRMED.**

---

## R-5 — MAJOR — "the four `*_wiring` mains … carry `UserTask`s all the same" is FALSE for three of the four

**Claim as written**: spec §6.2 (spec:740-742) — *"The four outside `scenarios/` are
`cache_wiring`, `mysql_wiring`, `production_wiring`, `sqlite_wiring` — excluded by
the draft's `scenarios/`-scoped sentence but **carrying `UserTask`s all the same**."*

**Re-derived at `dd76a17b`**:
```
$ for f in cache mysql production sqlite; do grep -c 'NewUserTask(' examples/${f}_wiring/main.go; done
cache_wiring      1
mysql_wiring      0
production_wiring 0
sqlite_wiring     0
```
Cross-checked with `grep -rniE "usertask|user_task|WithEligible"` over each directory
(each is a single `main.go`): `mysql_wiring`, `production_wiring` and `sqlite_wiring`
contain **no UserTask node of any kind**. Their only human-task contact is *wiring* —
`humantask.NewMemTaskStore()` + `runtime.WithHumanTasks(resolver, taskStore, az)` —
which is why they appear in the 16.

And the one that does have a UserTask, `cache_wiring/main.go:136`, declares
`activity.WithEligibleRoles("reviewer")` — so it is **already** eligibility-bearing
and is unaffected by D3's authoring gate.

**Verdict: WRONG.** Zero of the four wiring mains need a D3 `open:`/eligibility
migration; the sentence asserts all four do.

**Damage if acted on**: an examples-phase agent budgets four extra mains for D3
migration and hunts for UserTask nodes that do not exist in three of them. Worse, it
**conflates two disjoint reasons** the wiring mains matter: three of them matter for
**D1** (they call `stdlib.Mount`, §6.2's own next sentence), and none of them matters
for **D3**. An agent that merges the two lists will either edit the wrong files or
conclude the enumeration rotted and re-derive it — the failure mode the revision was
written to end.

**Proposed replacement**:
> The four outside `scenarios/` — `cache_wiring`, `mysql_wiring`,
> `production_wiring`, `sqlite_wiring` — call `runtime.WithHumanTasks` as **wiring
> only**. Only `cache_wiring` declares a UserTask node (`main.go:136`), and it already
> carries `WithEligibleRoles("reviewer")`, so **none of the four needs a D3
> migration**. Three of them matter for a different reason: they mount the task routes
> via `stdlib.Mount` (`production_wiring:264`, `sqlite_wiring:278`,
> `mysql_wiring:262`) and so are affected by **D1**.

---

## R-6 — CONFIRMED — the revision is RIGHT and the previous audit was WRONG on the element bounds (adjudicated)

**The dispute**: ADR-0186:218-220 and spec:435-436 assert *"the audit's suggested
replacements — '5 000 elements ≈ 40 ms, 10 000 ≈ 150 ms' — are wrong by roughly 15×:
re-derived from the same ladder, 5 000 ≈ 610 ms and 10 000 ≈ 2.4 s."*

**Re-derived independently** from the measured ladder (25 / 98 / 391 / 1563 ms at
n = 1 000 / 2 000 / 4 000 / 8 000). Fit k = 1.563 s / 8 000² = **2.4422e-8 s·n⁻²**:

| n | k·n² | bundle says | audit said |
|---|---|---|---|
| 2 000 | 0.0977 s | ~100 ms ✓ | — |
| 5 000 | **0.6106 s** | ~610 ms ✓ | 40 ms ✗ (**15.3× low**) |
| 10 000 | **2.442 s** | ~2.4 s ✓ | 150 ms ✗ (**16.3× low**) |
| 43 000 | 45.15 s | ~45 s ✓ | ~45 s ✓ |
| 50 000 | 61.05 s | ~61 s ✓ | ~61 s ✓ |

**The audit contradicted itself internally**: `audit-b3-failure-modes.md:268-269`
extrapolates n = 43 000 and n = 50 000 **correctly** with the very formula
`(n/8000)² × 1.563 s`, then sixteen lines later (`:285`) proposes 40 ms / 150 ms,
which that same formula refutes. The bundle caught it.

**Verdict: the revision is CONFIRMED and the previous audit is WRONG.** "Roughly 15×"
is fair (15.3× and 16.3×). Every cell of ADR-0186:208-213 and spec:425-428 checks out.

Nit (cosmetic): the n = 2 000 row is labelled *"extrapolated"* but 2 000 is a
**measured** point in the ladder (98 ms). The extrapolation agrees with it, which is
good corroboration — say so instead of relabelling a measurement.

---

## R-7 — MAJOR — "the 256 KiB **and** 10 000 numbers are now derived" — 256 KiB is derived nowhere, and the ADR itself refutes its only rationale

**Claim as written**: ADR-0186:347-350 (Consequences / Negative) — *"1 MiB is still a
**judgement call**, explicitly `ASSUMPTION (unverified)`; **the 256 KiB and 10 000
numbers are now derived**, and the derivation is written down so the next reader can
check it rather than inherit it."*

**Re-derived** — every occurrence of `256 KiB` in the whole bundle:
```
$ grep -rn "256 KiB" docs/specs/2026-08-20-*.md docs/adr/0186-*.md docs/plans/2026-08-20-*.md docs/specs/2026-08-21-*.md
adr-0186:141  "- `service.WithMaxVariableBytes`, default **256 KiB** …"          ← asserted
adr-0186:212  "| 43 000 | ~45 s | what 256 KiB of JSON integers admits |"        ← REFUTES it
adr-0186:216  "The draft called 256 KiB the CPU mitigation; its own table falsifies that"
adr-0186:217  "256 KiB admits ~40–50 k elements ⇒ ~45–60 s of unpreemptible CPU"
adr-0186:348  "the 256 KiB and 10 000 numbers are now derived"                   ← the claim
spec:432,678  same two roles
plan:434,436  default asserted; framing corrected
```
There is **no derivation of 256 KiB anywhere in the bundle** — no target byte budget,
no storage-cost model, no measurement. Its only quantitative appearance is the ADR's
own demonstration that it admits 40–50 k elements ⇒ 45–60 s of CPU, i.e. the sentence
that **disqualifies** the number's original rationale without supplying a new one.

**Cross-document disagreement**: spec §4.9 (spec:678) says 256 KiB is *"now documented
as a **payload/storage**"* bound — a re-**labelling**, which is true — while the ADR
upgrades that to *"derived"*. The spec's weaker, correct wording did not propagate.

**Verdict: WRONG** for 256 KiB (CONFIRMED for 10 000's *arithmetic*, but see below).

**Damage if acted on**: 1 MiB carries `ASSUMPTION (unverified)` and will therefore be
attacked by the next reviewer; 256 KiB is declared derived and will not be. A number
with no rationale is thereby immunised against review by a false status label — the
precise mechanism by which the draft's 44-character and 23-pin figures survived.

**Secondary (same sentence)**: 10 000's *arithmetic* is derived (R-6) but its
*choice* is not. The table maps n → time correctly and then names 10 000 "the
default" with **no stated acceptance target**; the only criterion offered anywhere is
the n = 2 000 row's note *"a tight bound for latency-sensitive deployments"*. Picking
10 000 (~2.4 s of unpreemptible CPU) over 2 000 (~100 ms) is a judgement call resting
on an unstated budget. The audit's F6 asked for exactly this — *"state the target
explicitly ('no single evaluation exceeds ~100 ms of CPU at the measured curve') so
the number is derivable rather than asserted"* — and the revision adopted the
arithmetic while dropping the target.

**Proposed replacement** (ADR-0186 Consequences):
> 1 MiB and **256 KiB** are both judgement calls, explicitly
> `ASSUMPTION (unverified)`: 256 KiB is a payload/storage bound with no derivation —
> its original CPU rationale is refuted above. The **10 000** element bound's
> time-cost is derived from the measured ladder (~2.4 s); the *choice* of 2.4 s as the
> acceptable per-evaluation CPU ceiling is a judgement call and is stated here as one.
> A deployment wanting ~100 ms sets `WithMaxEvalElements(2000)`.

---

## R-8 — ⛔ CRITICAL — "only **5** reach `model.Validate`" is derived from a net that can only see ONE of the repo's THREE authoring forms

**Claim as written**, in four documents:
- ADR-0185:278-283 — *"Re-derived: **274** `NewUserTask` call sites repo-wide; **128**
  carry no eligibility dimension; **only 5** reach `model.Validate`, whose single
  non-test caller is `definition/model/builder.go` … the **authoring** gate reaches
  ~5 sites, the **runtime** rule reaches all 128"*
- spec:576-580 — same 274 / 128 / 5.
- plan:782 — table row *"`NewUserTask` sites / no eligibility / reach `Validate` |
  uncounted, 'every definition' | **274 / 128 / 5**"*.
- plan:385 — *"`runtime/manual_task_test.go`, one of the 5 sites that reach
  `model.Validate`"*.
- evidence §7 row 8 supplies the 274.
- ⚠ plan:336-339 — *"Most `engine` tests build `&model.ProcessDefinition{...}`
  **struct literals**, which **never** reach `model.Validate`"*.

**Re-derived at `dd76a17b`.** The number comes from `grep -rn "NewUserTask("`. That
net sees **one** of the three ways a UserTask is authored in this repo. CLAUDE.md
itself names two of them (*"YAML and direct Go code are the authoring forms"*), and
`definition/build` adds a third:

| authoring form | net sees it? | no-eligibility sites reaching `model.Validate` |
|---|---|---|
| `activity.NewUserTask(...)` | ✅ | the claimed 5, **plus 2 more** (below) |
| `build.Builder.AddUserTask(...)` (`definition/build/build.go:117`) | ❌ **invisible** | **1** |
| YAML `kind: userTask` (`model.ParseYAML` → `Loader.Build`) | ❌ **invisible** | **5** |

**The eight missed nodes**, each on a path that calls `model.Validate` and asserts success:

1. **`engine/step_signal_fanout_test.go:51,52`** — `activity.NewUserTask("taskA")`,
   `("taskB")`, no eligibility, inside `twoInterruptingBoundariesDef()`. That def is
   passed to `require.NoError(t, model.Validate(def))` at **`:84, :167, :227, :275,
   :345, :404`** — six assertions. (A third, `:213 "taskH"`, is in a sibling fixture.)
   ⇒ **This alone falsifies plan:336-339's "never".**
2. **`runtime/definition_registry_test.go:531`** — `definition.NewBuilder("multi",1)
   … .AddUserTask("a") … .Build()` with `require.NoError(t, err)`. `Build()` →
   `builder.go:133 Validate(&def)`. Invisible to `grep NewUserTask(`.
3. **`definition/model/yaml_test.go:229, 262, 300, 441`** — four `kind: userTask`
   nodes with no `eligible_*` key, each parsed by `model.ParseYAML` and built with
   `loader.Build()` under `t.Fatalf("Build: %v", err)`.
4. **`definition/model/strict_decoding_test.go:384`** — `{id: sign, kind: userTask,
   manual: true, manual_immediate: true}`, no eligibility, `ld.Build()`.

Verification of the YAML path reaching the gate:
```
$ grep -n "func (l \*definitionLoader) Build" definition/model/builder.go
227:func (l *definitionLoader) Build() (*ProcessDefinition, error) { return l.build() }
$ grep -n "Validate(&def)" definition/model/builder.go
133:    if err := Validate(&def); err != nil {
```
and the YAML eligibility keys that would have to be present are
`eligible_roles` / `eligible_privileges` / `eligible_expr`
(`definition/model/yaml.go:25-27`) — absent in all five.

**Verdict: WRONG.** "Only 5" is at least **13** nodes across **6** files, and the
three highest-value ones live in packages and authoring forms the plan says are out
of the authoring gate's reach.

**Damage if acted on** — this is the delivery-stopping kind:
- A phase-3 agent implements the `model.Validate` rejection, runs `go test
  ./definition/...` (the plan's own phase-3 verification, plan:341 for engine), and is
  hit by **`definition/model/yaml_test.go` and `strict_decoding_test.go` failing** —
  files the bundle never named and the plan gives no phase.
- A phase-4d agent is told `engine` fixtures *"never reach `model.Validate`"* and so
  breaks on the **runtime** rule only. It will instead find six `require.NoError(t,
  model.Validate(def))` assertions failing in `step_signal_fanout_test.go`, diagnose
  it as a regression it introduced, and may "fix" it by weakening the gate.
- The `AddUserTask` builder wrapper is a **public API** (`definition/build`). If the
  net cannot see it, neither the migration note nor the CHANGELOG will mention that
  `Builder.AddUserTask(id)` with no options now fails `Build()`.

**Proposed replacement** (ADR-0185 Consequences, spec §4.2, plan §5):
> A UserTask is authorable **three** ways, and the blast radius must be counted over
> all three: `activity.NewUserTask` (273 call sites), `build.Builder.AddUserTask`
> (`definition/build/build.go:117`, 3 call sites), and YAML `kind: userTask`
> (`model.ParseYAML` → `Loader.Build`, 8 occurrences in 2 files). Re-derived at
> `dd76a17b`, the definitions carrying a no-eligibility UserTask **that reach
> `model.Validate`** are:
> `examples/scenarios/manual_task/main.go` (2) · `runtime/manual_task_test.go` (3) ·
> `engine/step_signal_fanout_test.go` (2 nodes, 6 `require.NoError(model.Validate)`
> assertions) · `runtime/definition_registry_test.go:531` (via `AddUserTask`) ·
> `definition/model/yaml_test.go` (4 YAML nodes) ·
> `definition/model/strict_decoding_test.go:384` (1 YAML node).
> ⚠ Strike plan:336-339's *"engine … struct literals … **never** reach
> `model.Validate`"* — `engine/step_signal_fanout_test.go` and
> `engine/step_fuzz_test.go:48` both call `model.Validate` on struct-literal defs.
> Phase 3's verification must be `go test ./definition/... ./engine/... ./runtime/...`,
> not `./definition/...`.

---

## R-9 — MAJOR — "**128** carry no eligibility dimension" is 121–127, and the residue is unresolvable-by-construction

**Claim**: ADR-0185:278, :466 (*"128 no-eligibility …"*), spec:576, plan:87
(*"`service` — **128** sites (§5)"*), plan:782.

**Re-derived at `dd76a17b`** with a `go/ast` scan (`parser.ParseFile` over every
`.go` in the tree; every `CallExpr` whose callee `Sel.Name`/`Ident` is `NewUserTask`;
an option counts as eligibility if it is a `CallExpr` named `WithEligibleRoles` /
`WithEligiblePrivileges` / `WithEligibleExpr`):

```
=== TOTAL=273  ELIG=146  NOELIG(literal)=121  PASSTHRU/unresolvable=6
```

The 6 unresolvable are calls whose options are a forwarded `opts...` or a variable,
so eligibility cannot be decided statically:
`definition/build/build.go:118` (the `AddUserTask` wrapper — **not a definition site
at all**), `definition/activity/options_test.go:247`,
`definition/model/validate_test.go:1890`, `:2206`,
`engine/step_human_outcome_test.go:27`, `runtime/processdriver_action_test.go:81`.

So the true figure is **121 definite + up to 6 undecidable = 121–127**, never 128.
128 = 121 + 6 + **the `func NewUserTask` declaration** — i.e. the same off-by-one as
R-3, carried through, plus six pass-throughs counted as definition sites.

**Verdict: WRONG.** And note what it means that `definition/build/build.go:118` is in
the tally: the bundle's own no-eligibility count **includes the public wrapper whose
call sites it cannot see** (R-8).

**Damage if acted on**: plan:87 hands "**128** sites" to the phases as a work
estimate and as the completion criterion for the fixture migration. An agent that
migrates 121 and finds 7 unaccounted will either hunt for phantom sites or assume the
enumeration rotted.

**Proposed replacement**: *"**121** `NewUserTask` call sites declare no eligibility
option; a further **6** forward `opts...` and cannot be decided statically (listed
below) — the working figure is 121, with 6 to inspect by hand. ⚠ This counts only the
`NewUserTask` authoring form; see the three-form enumeration in R-8."*

---

## R-10 — CONFIRMED — batch: 16 counts and citations that hold exactly at `dd76a17b`

| claim | where | re-derived | ✓ |
|---|---|---|---|
| 10 `service` options | spec §6.6 | `grep -c '^func With' service/options.go` → 10 | ✓ |
| 6 `DurableProvider` methods | spec §6.6 | `service/durable.go` — `InstanceStore, Definitions, Lister, TaskStore, TimerStore, CallLinkStore`; no `Authorizer()` | ✓ |
| 39 = 13×3 decode sites | spec §6.6, adr-0186 | `stdlib/groups.go` `json.NewDecoder` 13 · `gin/groups.go` `ShouldBindJSON` 13 · `fiber/groups.go` `Bind().JSON` 13 | ✓ |
| 26 routes = 9+15+2 | spec §6.5 | `grep -cE '\bhandle\(' stdlib/groups.go` → 27, minus the `func handle(` decl | ✓ |
| 5 echoing 4xx arms + 1 blanking | spec §6.6 | `httpcore/errors.go` `:31,33,35,50,56` echo `err.Error()`; `:58` (500) blanks. Closed set — no other `return http.Status…` in the file | ✓ |
| 6 `wrkflw_journal` columns | spec §6.6 | sqlite `0001_init.sql:36-42` **and** postgres `:26-32` — `instance_id, seq, kind, trigger, occurred_at, applied_at` in **both** dialects | ✓ |
| 3 `SECURITY:` sites, admin-only | spec §6.4 | `stdlib/groups.go:189`, `gin/groups.go:204`, `fiber/groups.go:209`; identical text | ✓ |
| 3 `stdlib.Mount` callers | spec §6.2 | `mysql_wiring:262`, `sqlite_wiring:278`, `production_wiring:264`; no `gin.Mount`/`fiber.Mount` outside tests | ✓ |
| 4 `Authorize` sites + 1 delegation | spec §6.3 | `runtime/task/service.go:199,234,255,306`; `casbinauthz:163` delegates | ✓ |
| 6 `CustomizeConfig` fields | (inherited from audit C-23) | `httpcore/seam.go` — `BasePath, Wrap, InstanceMapper, Logger, TracerProvider, MeterProvider`; no identity seam | ✓ |
| **2** ABAC evaluation sites | spec §2.11, adr-0185 D4 | `grep "spec.Attribute"` + `EvalBool` non-test → exactly `authz/authz.go:136` and `internal/authz/casbin/authorizer.go:68`. **Closed set verified independently, not inherited** | ✓ |
| **2** "engine gate is open" godocs | spec:581, plan:722 | `activity.go:159`, `options.go:221` — but see **R-11** | ✓ (phrase) |
| ADR-0117 **Decisions 1 and 3** | adr-0185:26,75,288,488; plan:716,781; spec:151,581,871 | 0117's Decision body is a numbered list 1–4, so both resolve; D1 = *"with none set, the engine gate is open"*, D3 = *"Any combination (including none) is valid"*. Consistent in **all six** places | ✓ |
| 80-character predicate | spec:419, adr-0186:46 | `printf … | wc -c` → 80. The draft's 44 is corrected everywhere | ✓ |
| title = 12 items **+ parked 102** | spec:1 | matches §3 Scope (10 in-scope + 100/101 posture-only + parked 102). Audit C-27 fixed | ✓ |
| `handleHumanCompleted` `:849`, write `:941`, 0 claim comparisons | evidence §7, spec §2.5, plan §5 | `grep -n "^func handleHuman"` → `:849`; `Completion = &humantask.Completion{` → `:941`; `awk '/^func handleHumanCompleted/,/^}/' | grep -c "Candidates\|Eligibility\|Claim"` → **0**; body `:849-983`, next func `:984` | ✓ |

Line citations spot-checked and exact at `dd76a17b`: `engine/step_nodes.go:723`
(`spec := authz.AuthzSpec{`), `internal/persistence/store/store_core.go:81,174,231`
(`json.Marshal` / `json.Unmarshal` / `json.Marshal`),
`internal/expreval/expreval.go:135` (the `%q`),
`httpcore/endpoints.go:119,132,150` (the three `authz.Actor{ID, Roles}` projections),
`httpcore/dto.go:12,44,50,66`, `service/service.go:317` / `:323`.
**The anchor problem from the previous audit (C-12/C-13) is fixed**: the bundle now
cites `dd76a17b` consistently and every line I checked resolves there.

---

## R-11 — MAJOR — "**Both** godocs stating the open default as fact" is a phrase-sample presented as the class; `authz/authz.go` carries three more and none is prescribed for correction

**Claim as written**: plan:722-724 — *"⚠ **Both** godocs stating the open default as
fact — `definition/activity/activity.go:159` … and `definition/activity/options.go:221`.
`grep -rn "engine gate is open" --include='*.go' .` finds exactly these two; the draft
named one."* Same in spec:581 and adr-0185:291-293 (*"the **two** godocs that state
the open default as fact"*).

**Re-derived at `dd76a17b`.** The grep is honest about *itself* — the phrase does
occur exactly twice. But the sentence generalises the phrase-match to the **class**
("both godocs stating the open default as fact"), and the class is wider. Searching
the property rather than the phrase
(`grep -rniE "defers to the .*transport|gate is open|empty spec (means |allows)|open access|NOT evaluated"`,
non-test):

| # | godoc | verbatim | falsified by | prescribed? |
|---|---|---|---|---|
| 1 | `definition/activity/activity.go:159` | *"with none set, the engine gate is open"* | D3 | ✅ plan:722 |
| 2 | `definition/activity/options.go:220-221` | *"With no eligibility set, the engine gate is open"* | D3 | ✅ plan:722 |
| 3 | **`authz/authz.go:79-81`** (`AuthzSpec`) | *"**An empty spec means allow-all.**"* | D3 — `RoleAuthorizer` now denies it | ❌ **nowhere** |
| 4 | **`authz/authz.go:111-113`** (`RoleAuthorizer`) | *"1. spec.Roles is empty (**open access**), OR …"* | D3 | ❌ **nowhere** |
| 5 | **`authz/authz.go:119-120`** | *"[AuthzSpec].Privileges is reserved … and is **NOT evaluated** by RoleAuthorizer"* | D3 — any non-empty `Privileges` now returns `ErrUnevaluatableSpec` | ❌ **nowhere** |
| 6 | `internal/authz/casbin/authorizer.go:33-34` | *"An empty spec allows."* | D3 | ✅ plan:581 (phase 10) |
| 7 | `examples/scenarios/manual_task/main.go:9` | *"Both nodes carry no eligibility (authorization is deferred to …)"* | D3 | ~ implicitly, phase 14 |

Phase 2 (`authz`, plan:203-246) lists six symbols and seven tests and **never
mentions a godoc**. So the three godocs on the **very type being changed** — the ones
ADR-0185's Context finding 3 quotes as its *evidence* (spec:128 quotes `:79-86`
verbatim) — are never corrected.

**Verdict: WRONG** — sample-vs-class, the exact error the revision fixed for the
deny-list predicates (spec:151 now correctly says "unbounded; five were sampled") and
then reintroduced here.

**Damage if acted on**: `authz` ships with its own package documentation asserting the
opposite of its behaviour — *"An empty spec means allow-all"* on the exported
`AuthzSpec` a consumer reads on pkg.go.dev, and *"Privileges … is NOT evaluated"* on
the exported `RoleAuthorizer` that now returns `ErrUnevaluatableSpec` for exactly that
input. This is the ADR-0162 zombie-doc failure, one package over.

**Proposed replacement** (plan phase 2, new bullet; and re-word plan:722):
> **Godocs falsified by D3 — a closed list, derived from the property not a phrase:**
> `authz/authz.go` `AuthzSpec` (*"An empty spec means allow-all"*), `RoleAuthorizer`
> items 1 and the `Privileges` note; `definition/activity/activity.go:159`;
> `definition/activity/options.go:220-221`;
> `internal/authz/casbin/authorizer.go:33-34`; `examples/scenarios/manual_task/main.go:9`.
> The `grep "engine gate is open"` two are a **phrase** subset, not the class.

---

## R-12 — MINOR — "**both** in-repo ones" — there are FIVE in-repo `Authorizer` implementations, and the excluded one is what the load-bearing test wires

**Claim as written**: plan:353-355 — *"This is what makes ADR-0185's 'closed end to
end' true for a consumer-supplied `Authorizer` as well as for **both in-repo ones**."*

**Re-derived at `dd76a17b`** — non-`_test.go` types with an `Authorize` method
matching `authz.Authorizer`:
```
authz/authz.go:106                    AllowAll.Authorize
authz/authz.go:124                    RoleAuthorizer.Authorize
internal/authz/casbin/authorizer.go:43 casbin.Authorizer.Authorize
casbinauthz/casbinauthz.go:162        casbinauthz.Authorizer.Authorize   (public wrapper, delegates)
processtest/spyauthz.go:44            processtest.SpyAuthorizer.Authorize (public test harness)
```
**Five**, not two.

**Verdict: WRONG.** Damage is small — the hoisted `CheckSpecStated` gate covers all
five by construction, which is *stronger* than the sentence claims — but the omitted
`authz.AllowAll` is precisely the authorizer plan:369's load-bearing test wires
(`TestClaimDeniesUnstatedSpecEvenWithAllowAllAuthorizer`), so "both in-repo ones"
reads as if `AllowAll` were out of scope of its own test.

**Proposed replacement**: *"…true for **every** `Authorizer`, in-repo or
consumer-supplied. The in-repo implementations are `authz.AllowAll`,
`authz.RoleAuthorizer`, `internal/authz/casbin.Authorizer`, its public wrapper
`casbinauthz.Authorizer`, and `processtest.SpyAuthorizer` — the gate sits above all
of them because it is hoisted into `runtime/task`, not into any authorizer."*

---

## R-13 — MINOR — phase 14's prescribed enumeration is a FILE-level filter answering a CALL-SITE question (right answer today, by luck)

**Claim as written**: plan:690-692 — *"**Enumerate mechanically** with
`grep -rLn "WithEligible" $(grep -rln "NewUserTask" examples/scenarios/)` — do not
guess, and do not reuse the draft's list."*

**Re-derived at `dd76a17b`** — the command as written:
```
$ grep -rLn "WithEligible" $(grep -rln "NewUserTask" examples/scenarios/)
examples/scenarios/manual_task/main.go
```
which **is** the right answer today. But `grep -L` lists files containing **no**
match, so a scenario file with one eligible and one open UserTask is silently
excluded. Ground truth per file (calls vs `WithEligible` occurrences):
```
attribute_authz 2/6 · reverse_rollback 2/2 · manual_task 2/0 · the other nine 1/1
```
No file currently mixes the two, so the file-level filter coincides with the
call-site truth. It is correct **by luck**, not by construction. (`-n` is also inert
with `-L`.)

**Verdict: UNFALSIFIABLE-AS-WRITTEN** — the command cannot distinguish "no
no-eligibility call" from "at least one eligibility call somewhere in the file".

**Damage if acted on**: none today; a silent miss the first time a scenario declares
two UserTasks with different eligibility — and the plan explicitly hands this command
over as the authority ("do not guess").

**Proposed replacement**: *"Enumerate per **call site**, not per file — the file
filter `grep -L` hides a mixed file. Use an AST check, or inspect each of the 14
`activity.NewUserTask(` calls under `examples/scenarios/` individually
(`grep -rn 'NewUserTask(' examples/scenarios/`). Today exactly two lack a dimension,
both in `examples/scenarios/manual_task/main.go`."*

---

## R-14 — MAJOR — the depth-1 justification is an INHERITED citation stretched past its scope: the godoc covers `vars`, the rule covers `vars` ∪ `actor`

**Claim as written**: ADR-0185:345-349 — *"Extraction is **depth-1**:
`vars.order.total` yields `vars.order`. That is not a limitation to apologise for —
`humantask.HumanTask.Vars`' own godoc already states the snapshot is a shallow
`maps.Clone` and that *"eligibility predicates should rely on top-level scalar
variables only"*. **Depth-1 is precisely the documented supported surface.**"*
Restated at evidence §3 item 1 (*"Depth-1 is exactly the documented supported
surface"*).

**Re-derived at `dd76a17b`.** The quotation is verbatim and correctly located —
`humantask/humantask.go:112-118`, the godoc on the **`Vars` field**:
```
// Note: the snapshot is a shallow copy (maps.Clone) — top-level keys are copied
// defensively, but nested maps/slices remain shared with the instance variables;
// eligibility predicates should rely on top-level scalar variables only.
Vars map[string]any
```
But the **extractor's domain is wider than `Vars`**. The evidence file states its own
scope at §3: *"collecting `MemberNode`s whose base is the **`vars` / `actor`**
identifier"*, and its table's row 8 is `actor.attributes.clearance > 3 →
actor.attributes {member} depth-1 only`. Nothing in `humantask.HumanTask.Vars`'
godoc says anything about `actor`. The `actor` side is `authz.Actor`
(`authz/authz.go:35-39`), a **struct** with an `Attributes map[string]any` field —
a different type, a different package, a different shallowness rule
(`Actor.Clone`'s godoc: *"Attributes are cloned one level deep"*), and **not cited
anywhere in the bundle**.

Worse, the bundle's own probe shows `actor.attributes` does not even evaluate:
spec:350-351 records `"vars.internalApprovalLimit > actor.attributes.tier": cannot
fetch attributes from authz.Actor (1:36)` — expr resolves Go field names, so the
lower-case `attributes` is a hard fetch error, not a nil. So the extractor's
`actor.attributes` row describes a **static** extraction of a reference whose
**runtime** behaviour is an error, and the ADR's closed-set table (D4, four rows:
depth-1 member / nested chain / dynamic key / zero references) has **no row
distinguishing `vars`-rooted from `actor`-rooted references at all**.

**Verdict: WRONG** — an inherited citation restated with its scope silently widened
(*"top-level scalar **variables**"* → *"the documented supported surface"*), which is
Premise Discipline's named failure: restating strips the hedge.

**Damage if acted on**: a phase-1 implementer builds `WithStrictReferences` over both
roots on the strength of "depth-1 is the documented supported surface", and there is
no decision on record for what an `actor.X` reference means — is a missing
`Attributes` key a deny? is `actor.attributes` (which errors) a deny or an error? The
ADR's table says the set is closed and it is not.

**Proposed replacement** (ADR-0185 D4):
> Extraction is depth-1 over **two** roots, and they are not symmetric.
> - `vars` is a `map[string]any` whose snapshot godoc
>   (`humantask/humantask.go:112-118`) already restricts predicates to *"top-level
>   scalar variables only"* — depth-1 matches the documented surface **for `vars`**.
> - `actor` is the `authz.Actor` **struct**. Its fields (`ID`, `Roles`, `Attributes`)
>   always resolve; only `actor.Attributes[k]` can be absent, and expr resolves Go
>   field names — executed: `actor.attributes.tier` is a **fetch error**
>   (`cannot fetch attributes from authz.Actor`), not a nil, so it is already
>   fail-closed by a different mechanism. The strict-reference rule therefore applies
>   to **`vars`-rooted references only**; `actor`-rooted references are out of its
>   scope, and that is stated rather than implied.

---

## R-15 — MINOR — `seam.go:30-31` cited for `TracerProvider`/`MeterProvider`; they are at `:31-32` and `:30` is `Logger`

**Claim as written**: ADR-0186 D5 — *"`CustomizeConfig` already carries
`TracerProvider`/`MeterProvider` (`seam.go:30-31`), so no new dependency"*.

**Re-derived at `dd76a17b`**:
```
$ grep -n "Logger \|TracerProvider\|MeterProvider" transport/http/httpcore/seam.go
30:	Logger         *slog.Logger
31:	TracerProvider trace.TracerProvider
32:	MeterProvider  metric.MeterProvider
```
The two named fields are `:31-32`. `:30` is `Logger` — which the **same decision**
separately requires editing (*"`CustomizeConfig.Logger`'s godoc … must be corrected
in place"*), so the off-by-one lands on a field that is also in play.

**Verdict: WRONG (off-by-one).** This is a *new* citation introduced by the revision,
in a file the revision also modifies — the same class as the previous audit's C-9/C-12
Criticals, caught early enough to be cheap.

**Proposed replacement**: *"`CustomizeConfig` already carries `TracerProvider` and
`MeterProvider` (`httpcore/seam.go:31-32`)"* — or drop the line numbers and name the
struct, since the revision edits this struct in phase 9.

---

## R-16 — MAJOR — evidence §2 is FALSE AS LABELLED: its rows were run with EMPTY vars, under the env the section declares row 1 returns `true`

**Claim as written**: evidence file §1 preamble (`:28-30`) —
*"Compiled exactly as `internal/expreval` does … with **`vars = {"tier": "gold"}`**."*
§2 (`:51-60`) follows immediately under that env with no new env statement (§4, by
contrast, explicitly re-declares *"`vars` is **empty** in every row"*), and records:
```
(vars.tier ?? "none") == "gold"      out=false  err=<nil>
(vars.blocked ?? false) == true      out=false  err=<nil>
(vars.status ?? "") != "blocked"     out=true   err=<nil>
```

**Re-derived at `dd76a17b`** — throwaway module `probeexpr` requiring
`github.com/expr-lang/expr v1.17.8` (the `go.mod` pin), compiled with
`expr.Compile(code, expr.AllowUndefinedVariables())` + `expr.Run`, exactly as §1
prescribes:
```
ENV A = the env §1/§2 DECLARES: vars={tier:gold}
  (vars.tier ?? "none") == "gold"    out=true     <-- evidence says false
  (vars.blocked ?? false) == true    out=false
  (vars.status ?? "") != "blocked"   out=true

ENV B = empty vars
  (vars.tier ?? "none") == "gold"    out=false    <-- matches the record
  (vars.blocked ?? false) == true    out=false
  (vars.status ?? "") != "blocked"   out=true
```
All three recorded rows match **ENV B**, not the declared ENV A. Under the declared
env, row 1 is `true` — the opposite of what is written.

**Verdict: WRONG as labelled.** The *conclusion* §2 draws (the unparenthesised `??`
form is a **compile error** — reproduced verbatim, including the caret column
`(1:21)`) is CONFIRMED and unaffected. What is wrong is the env attribution of the
three evaluating rows.

**Damage if acted on**: §2's own instruction is *"Any documentation that offers `??`
must show the parentheses"*, so this table is destined to be copied into consumer
documentation and into a test. An implementer who writes
`assert (vars.tier ?? "none") == "gold" → false` with the section's stated fixture
`vars = {"tier":"gold"}` gets a **failing test** and will conclude the `??` guard is
broken — when it works correctly. This is the repo's *"a measured rate is a claim
about the MODE it was measured in"* lesson, in the file that is the single source for
the other four documents.

**Proposed replacement**: split the env declaration —
> §2 rows are evaluated with **`vars = {}` (empty)**, because the question is what a
> guard does on an *absent* key. For contrast, with `vars = {"tier":"gold"}` row 1
> returns `true`. The compile failures are env-independent.

---

## R-17 — CONFIRMED — evidence §1 and §4 reproduce EXACTLY, and §1 is corroborated by the vendor's builtin table

**§1 (guard forms)** — all nine rows reproduced character-for-character at
expr v1.17.8, including the run-time error text and caret:
```
has(vars, "tier")   out=<nil>  err=invalid operation: cannot call nil (1:1)
 | has(vars, "tier")
 | ^
```
**Independent vendor corroboration** (worth citing — it is a five-second check where
the probe is a five-minute one): `$GOMODCACHE/github.com/expr-lang/expr@v1.17.8/builtin/builtin.go`
declares the full builtin set — `abs all any bitnot ceil concat count date duration
filter find findIndex findLast findLastIndex first flatten float floor fromJSON
fromPairs **get** groupBy **hasPrefix hasSuffix** indexOf int join keys last
lastIndexOf len lower map max mean median min none one reduce repeat replace reverse
round sort sortBy split splitAfter string sum take timezone toJSON toPairs trim
trimPrefix trimSuffix type uniq upper values`. **`has` is absent**; only `hasPrefix`
and `hasSuffix` exist, and `get` is present. So *"`has` is not a builtin"* is true by
the vendor's own table, not only by probe. ✓

**§4 (guard dominance)** — the newest and most design-changing claim in the bundle,
reproduced exactly with `vars = {}`:
```
"tier" in vars and vars.tier == "gold"      out=false
"tier" in vars or  vars.tier != "blocked"   out=TRUE   <-- unsound if tree-wide
not ("tier" in vars) or vars.tier == "x"    out=TRUE   <-- unsound if tree-wide
```
Rows 2 and 3 do allow on an absent key. **The dominance requirement is real and the
falsifying table is correct.** ✓

**Inherited-citation check**: evidence §Why-this-file-exists says *"The audit proposed
**four** 'working replacements'"*. Re-derived from the audit itself —
`audit-b3-adjudication.md:21-22` and `audit-b3-execution.md:87` both list exactly
`"k" in vars`, `vars?.k`, `vars.k ?? default`, `get(vars,"k")`. **Four. ✓** The
revision's attribution to its own auditor is accurate, and its refutation of two of
the four (§2 parenthesisation, §3 `get()` zero-reference bypass) is a genuine
re-derivation rather than a restatement.

---

## R-18 — MAJOR — "**Every** number below was re-derived … not inherited" / "**nothing** here is inherited" — the 274/128/5 triple is inherited verbatim from the audit, and all three numbers are wrong

**Claims as written** (two blanket quantifiers, one per document):
- plan:777-779 — *"## 5. Enumerations, re-derived at this bundle's commit. **Every
  number below was re-derived for the revision, not inherited.**"*
- evidence:3-5 — *"Every row below was run on this machine against the repo at the
  revision commit; **nothing here is inherited** from the 2026-08-20 draft or from its
  audit reports."*
- spec:700 — *"## 6. Enumerations (**re-derived at the revision commit**)"*.

**Re-derived.** The previous audit's C-20 table reads:
```
| `NewUserTask(` call sites, repo-wide incl. tests            | **274** |
| …carrying **no** eligibility dimension                       | **128** |
| …of those, in files that reach `model.Validate`              | **5** (manual_task ×2, runtime/manual_task_test ×3) |
```
The revision's plan §5, ADR-0185:278-283, spec:576-580 and evidence §7 carry
**274 / 128 / 5** — the same three integers, in the same order, with the same
parenthetical file list. Re-derived independently at `dd76a17b` (R-3, R-8, R-9):

| number | inherited | re-derived | wrong how |
|---|---|---|---|
| 274 | 274 | **273** call sites | counts the `func NewUserTask` declaration |
| 128 | 128 | **121** definite (+6 undecidable) | the +1 declaration, plus 6 `opts...` pass-throughs |
| 5 | 5 | **≥13 nodes in 6 files** | the net sees one of three authoring forms |

**Verdict: WRONG** — three blanket "every/nothing is inherited" quantifiers, each
falsified by the same triple, which is the single most load-bearing enumeration in
the bundle (it sizes the migration and decides which packages get a phase).

Note this is *not* a claim that the revision inherited carelessly across the board:
the pin count (R-1), the `:849`/`:941` anchor, the log-level `:323`, the godoc pair,
the `expreval.New` set and the ADR-0117 decisions were all genuinely re-derived, and
the revision even caught its own auditor being wrong twice (R-6, R-17). The defect is
narrower and therefore more dangerous: **one triple slipped through under a blanket
"all of it was re-derived" claim**, which is exactly the mechanism that immunises a
number against the next reviewer.

**Proposed replacement**: state which numbers were re-derived and which were carried,
per row — e.g. a `source` column with `re-derived @dd76a17b` vs
`inherited from audit-b3-counting.md C-N`. A blanket quantifier over an enumeration
is the construct this repo has been burned by most; per CLAUDE.md's Premise
Discipline, *prefer naming a closed set over counting it*.

---

# Summary table

| # | claim | where | re-derived | verdict | severity |
|---|---|---|---|---|---|
| R-8 | "only **5** reach `model.Validate`"; engine struct literals "**never** reach" it | adr-0185:278-283, spec:576-580, plan:336-339, plan:385, plan:782 | ≥**13** no-eligibility UserTask nodes in **6** files reach it; the net sees 1 of 3 authoring forms (`AddUserTask` and YAML `kind: userTask` invisible); `engine/step_signal_fanout_test.go` calls `model.Validate` on a struct literal **6×** | **WRONG** | ⛔ Critical |
| R-18 | "**Every** number … not inherited" / "**nothing** here is inherited" | plan:777-779, evidence:3-5, spec:700 | the 274/128/5 triple is verbatim from audit C-20 and all three are wrong | **WRONG** | Major |
| R-9 | "**128** carry no eligibility dimension" | adr-0185:278,466, spec:576, plan:87, plan:782 | AST scan: **121** definite + **6** undecidable pass-throughs; 128 = 121+6+the declaration | **WRONG** | Major |
| R-16 | evidence §2's three `??` rows under the declared `vars={"tier":"gold"}` | evidence:51-60 | rows match **empty** vars; under the declared env row 1 is `true`, not `false` | **WRONG as labelled** | Major |
| R-11 | "**Both** godocs stating the open default as fact" | plan:722-724, spec:581, adr-0185:291-293 | phrase-sample presented as the class; `authz/authz.go` `:79-81`, `:111-113`, `:119-120` are falsified by D3 and prescribed **nowhere** | **WRONG** | Major |
| R-7 | "the **256 KiB** and 10 000 numbers are now **derived**" | adr-0186:347-350 | 256 KiB is derived nowhere; the ADR's own table refutes its only rationale. spec:678 says only "re-labelled" — the weaker correct wording did not propagate | **WRONG** | Major |
| R-14 | "Depth-1 is **precisely the documented supported surface**" | adr-0185:345-349, evidence §3.1 | the cited godoc governs `vars`; the extractor's domain is `vars` ∪ `actor`, and `actor.attributes` is a **fetch error**, not a nil | **WRONG** (scope-widened inherited citation) | Major |
| R-5 | the four `*_wiring` mains "**carry `UserTask`s all the same**" | spec:740-742, plan:680-682 | 3 of 4 contain **zero** UserTask nodes; the 4th (`cache_wiring:136`) already has `WithEligibleRoles` ⇒ none needs a D3 migration | **WRONG** | Major |
| R-3 | "**274** `NewUserTask` sites" | evidence §7 row 8, adr-0185:278, spec:576, plan:782 | **273** call sites + 1 declaration | **WRONG** (off by one) | Minor |
| R-2 | "`expreval.New(` instances → **4**" with the command | evidence §7 row 6 | command returns **5**; the 5th (`engine/step.go:41`) is a godoc comment. 4 real instances ✓ | evidence-as-written **WRONG** | Minor |
| R-12 | "…as well as for **both in-repo ones**" | plan:353-355 | **five** in-repo `Authorizer` impls; the omitted `AllowAll` is what plan:369's test wires | **WRONG** | Minor |
| R-15 | `TracerProvider`/`MeterProvider` at "`seam.go:30-31`" | adr-0186 D5 | they are `:31-32`; `:30` is `Logger`, which the same decision also edits | **WRONG** (off by one) | Minor |
| R-13 | `grep -rLn "WithEligible" $(...)` as the mechanical enumeration | plan:690-692 | file-level filter for a call-site question; right today **by luck**, silently drops a mixed file; `-n` inert with `-L` | **UNFALSIFIABLE-AS-WRITTEN** | Minor |
| R-6 | "the audit's 5 000 ≈ 40 ms / 10 000 ≈ 150 ms are wrong by ~15×" | adr-0186:218-220, spec:435-436 | **the revision is right**: k=2.4422e-8 ⇒ 610 ms / 2.442 s; the audit contradicted its own formula 16 lines earlier | CONFIRMED (adjudicated **for** the bundle) | — |
| R-1 | 29 pins, httpcore 11 / gin 7 / fiber 5 / stdlib 5 / parity 1 | evidence §7, spec §6.1 | exact; and the net is now **closed by construction** (`dto.go` declares exactly 3 `Actor` fields: `"actor"`,`"actor"`,`"by"`) | CONFIRMED | — |
| R-4 | spec §6.1's new **per-file** split | spec:717-723 | every cell exact | CONFIRMED | — |
| R-10 | 16 counts/citations (10 options, 6 methods, 39=13×3, 26=9+15+2, 5 arms, 6 journal cols ×2 dialects, 3 SECURITY, 3 Mount, 4 Authorize, 6 `CustomizeConfig` fields, 2 ABAC sites, 2 godocs, ADR-0117 D1+D3, 80 chars, title, `:849`/`:941`/0) | throughout | all exact at `dd76a17b`; the previous audit's anchor defect (C-12/C-13) is **fixed** | CONFIRMED | — |
| R-17 | evidence §1 and §4 | evidence:26-43, 106-123 | reproduce character-for-character incl. error text and caret column; `has` absent from the vendor builtin table; the "four replacements" attribution to the audit is accurate | CONFIRMED | — |

# Ranking by damage if acted on

1. **R-8 (Critical)** — the only finding that stops the delivery. Phase 3 ships the
   `model.Validate` gate and is hit by `definition/model/yaml_test.go` and
   `strict_decoding_test.go` (never named, no phase); phase 4d is told `engine`
   fixtures *"never"* reach `model.Validate` and will meet six
   `require.NoError(t, model.Validate(def))` failures it was told were impossible —
   the shape in which an implementer "fixes" the guard instead of the fixture. And
   `build.Builder.AddUserTask` is **public API** whose breakage no document mentions.
2. **R-18 + R-9 + R-3 (Major)** — the blanket "nothing is inherited" claim is what
   let the one bad triple through; 128 is the migration's completion criterion.
3. **R-16 (Major)** — a wrong result in the file every other document cites, in the
   exact table the bundle says must be copied into consumer documentation.
4. **R-11 (Major)** — `authz` ships pkg.go.dev documentation asserting the opposite
   of its new behaviour, on the very type being changed. ADR-0162 zombie-doc shape.
5. **R-7, R-14 (Major)** — a number immunised from review by a false "derived" label;
   an undecided `actor`-root semantics hidden behind a closed-set table.
6. **R-5 (Major)** — sends the examples phase hunting for UserTasks that do not exist
   and conflates D1's file set with D3's.
7. **R-2, R-12, R-15, R-13 (Minor)** — citation/command hygiene; cheapest to fix now.

# Lens note

**The arithmetic was right again, and the nets were wrong again.** Every sum,
extrapolation and per-package split in this revision checks out — the O(n²) ladder is
correct to three significant figures (R-6), the pin table sums to 29 with an exact
per-file split (R-1, R-4), and 16 further counts are exact (R-10). The revision also
fixed **both** of the previous audit's structural defects: the anchor (every citation
now resolves at `dd76a17b`) and the pin net (now closed by `dto.go`'s three `Actor`
fields rather than sampled).

What survived is the **third** net failure, one layer up: `grep NewUserTask(` is a
net over **one of three authoring forms**, and it produced the one enumeration the
revision carried over verbatim while asserting nothing was carried over (R-8, R-18).
Two of the remaining Majors are the same shape at smaller scale — a phrase-grep
standing in for a class (R-11) and a `vars`-scoped godoc standing in for a
`vars ∪ actor` rule (R-14).

⚠ **For the next round**: the two nets that are provably closed (R-1's `dto.go`
field list, R-10's `ClassifyError` switch) are closed because someone enumerated a
**declaration** rather than grepping usage. That is the difference, and it is
mechanisable — the standing fix is to derive each enumeration from the type or
switch that defines it, not from a text pattern over call sites.

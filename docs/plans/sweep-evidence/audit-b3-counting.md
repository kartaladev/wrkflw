# Audit B3 — the counting lens (enumerations, quantifiers, inherited citations)

Bundle commit `3f317b63`. Worktree verified present (step 0 PASSED: all four
bundle files exist).

Method: every count, enumeration, quantifier and citation in the four bundle
documents re-derived from source with `grep`/`sed`/`go doc`. Findings written
one at a time, before the next count is checked.

---

## C-1 — CRITICAL — the 51 pin count is **29**, not 23; the grep's net misses the reassign `by` bodies

**Claim as written**
- spec §6.1 heading: *"⚠ the backlog says 21; it is **23**"*, with
  `grep -rn 'httpcore\.Actor{\|"actor"' transport/ | grep '_test.go'`
  → *"23 lines across 9 files in 5 packages"*, and a 9-row table.
- ADR-0185 Decision 1 + Consequences: *"23 test pins across 9 files in 5 packages"* (×2).
- plan §Phase 9: *"spec §6.1: `stdlib` 3 sites across 3 files; `gin` 5 across 2;
  `fiber` 4 in 1"*.

**Where**: spec:663-686, adr-0185:122, adr-0185:245, plan:466.

**Re-derived**: the spec's own grep does return exactly 23 / 9 files / 5 packages
— that arithmetic is right. **The grep is the wrong net.** `ReassignInput.By`
carries the json tag `"by"`, not `"actor"`
(`transport/http/httpcore/dto.go:66`), so every reassign request body that
supplies an actor is invisible to the pattern:

```
transport/http/httpcore/dto_test.go:79     `{"from":"alice","to":"bob","by":{"id":"mgr","roles":["manager"]}}`
transport/http/gin/gin_test.go:453         "by": {"id":"alice","roles":["manager"]}
transport/http/gin/gin_coverage_test.go:244  "by": {"id":"alice"}
transport/http/fiber/fiber_test.go:624     "by": {"id":"alice","roles":["manager"]}
transport/http/stdlib/coverage_test.go:126 "by": {"id":"alice","roles":["manager"]}
transport/http/stdlib/errors_test.go:187   "by": {"id":"bob","roles":["viewer"]}
```

Six further lines. (`endpoints_test.go:531,560` use the Go literal
`httpcore.Actor{}` and were already caught.) **Total 29 pin lines**; files stay
9 and packages stay 5, because all six land in files already listed — which is
exactly why the miss is invisible in the table's shape.

These are load-bearing, not cosmetic:
- `gin_test.go:453` asserts **200** for a reassign whose authority comes only
  from the body. Under D1 it becomes a zero actor ⇒ 403. It **breaks**.
- `stdlib/errors_test.go:187` asserts **403** for a body-supplied `bob/viewer`.
  Under D1 it still returns 403 — for an entirely different reason (zero actor).
  It **passes while no longer testing what it names**: a vacuous survivor, the
  worst outcome, and nothing in the plan tells the agent to look at it.

**Per-package re-derivation** (the numbers phase 9 hands to three parallel agents):

| package | plan says | re-derived | delta |
|---|---|---|---|
| `stdlib` | 3 across 3 files | **5** across 3 files | +2 (`errors_test:187`, `coverage_test:126`) |
| `gin` | 5 across 2 files | **7** across 2 files | +2 (`gin_test:453`, `gin_coverage_test:244`) |
| `fiber` | 4 in 1 file | **5** in 1 file | +1 (`fiber_test:624`) |
| `httpcore` | (not given) | **11** across 2 files | dto_test:79 unlisted |
| `parity` | (not given) | 1 | — |

**Verdict: WRONG.** Damage: a phase-9 agent told "3 sites in stdlib" migrates 3,
leaves 2, and one of the two survives green while asserting nothing.

**Proposed replacement wording** (name the closed set, do not count a grep):

> The body-derived actor reaches the transport through **two** json keys —
> `"actor"` on `ClaimInput`/`CompleteInput` and `"by"` on `ReassignInput`
> (`httpcore/dto.go:44,50,66`). Re-derived at `70a631e9` with
> `grep -rnE 'httpcore\.Actor\{|"actor"|"by"' transport/ --include='*_test.go'`:
> **29 pin lines across 9 files in 5 packages** — `httpcore` 11, `gin` 7,
> `stdlib` 5, `fiber` 5, `parity` 1. ⚠ `stdlib/errors_test.go:187` and
> `gin_coverage_test.go:244` assert a **403/denial**; after D1 they still return
> 403 from the zero actor, so they must be rewritten rather than merely
> compiled — otherwise they survive green and assert nothing.

---
## C-2 — CONFIRMED — "three `examples/` mains mount `TaskRoutes` via `stdlib.Mount`"

**Where**: spec §6.2 (spec:687-711), adr-0185:123-124, adr-0185:245, plan:491-497.

**Re-derived**: `grep -rn 'stdlib\.Mount\|gin\.Mount\|fiber\.Mount' --include='*.go' .`
(non-test) → exactly three `Mount` calls, all `stdlib`:
`examples/production_wiring/main.go:264`, `examples/sqlite_wiring/main.go:278`,
`examples/mysql_wiring/main.go:262`. No `gin.Mount`/`fiber.Mount` caller exists
anywhere outside tests. `stdlib/mount.go:17-21` quoted verbatim-correct, and
`TaskRoutes{Svc: svc}.Customize` is at `:19` as shown.

**Verdict: CONFIRMED.** Exactly three, and the "it is not zero" correction the
spec makes to the triage is itself correct.

Nit: the spec quotes `stdlib.Mount(mux, svc, …)` for `production_wiring`; the
real call passes
`httpcore.WithMeterProvider[*http.ServeMux](meterProvider)`. The elision is
honest.

---

## C-3 — CONFIRMED — 13 / 13 / 13 / 0 decode sites, and `BodyParser` is genuinely dead

**Where**: spec §2.8 table (spec:262-273), adr-0186:25-33.

**Re-derived**, non-test, `--include='*.go'`:

| package | idiom | count | all in |
|---|---|---|---|
| `transport/http/stdlib` | `json.NewDecoder` | **13** | `groups.go` |
| `transport/http/gin` | `ShouldBindJSON` | **13** | `groups.go` |
| `transport/http/fiber` | `Bind().JSON` | **13** | `groups.go` |
| `transport/http/httpcore` | any of `json.NewDecoder`/`json.Unmarshal`/`ShouldBind`/`Bind()` | **0** | — |

`grep -rn 'BodyParser' --include='*.go' .` → **0 repo-wide** (not merely
non-test). 13×3 = **39**; the sum in ADR-0186:27 is right.

**Verdict: CONFIRMED**, including the `BodyParser` → `c.Bind().JSON` correction
and the arithmetic.

---

## C-4 — MINOR — three "→ 0" greps are recorded in a form that returns 0 for **any** repo

**Where**: spec:272 (`grep -rn "MaxBytesReader|BodyLimit" transport/`),
spec:319-320 (`grep -rn "CheckRedirect|expreval" action/httpcall/`),
spec:327 (`grep -rniE "encrypt|redact" …` — this one is fine, it has `-E`).

Two of the three are written **without `-E`**, so `|` is a literal character and
the pattern can never match. A reader re-running the command as written gets `0`
regardless of the code — the command does not falsify the claim it is offered as
evidence for.

**Re-derived with the corrected `-E` form** (and once repo-wide, once including
tests, to be sure):
- `grep -rnE "MaxBytesReader|BodyLimit" transport/ --include='*.go'` → **0**
  (0 including tests; 0 repo-wide).
- `grep -rnE "CheckRedirect|expreval" action/httpcall/ --include='*.go'` → **0**
  (0 including tests).
- `grep -rniE "encrypt|redact" persistence/ internal/persistence/ engine/`
  (non-test) → **0**.

**Verdict: claims CONFIRMED, evidence-as-written UNFALSIFIABLE.** No damage to
the design; the fix is to record the command that actually tests the claim.

**Proposed replacement**: write `grep -rnE` in all three places (and in ADR-0186
Context §1, which reproduces the `MaxBytesReader|BodyLimit` form).

---
## C-5 — CONFIRMED — "26 routes … 9 non-admin, 15 admin, 2 health", and no definition-deploy route

**Where**: spec §1.1 (spec:51-52), spec §6.5 (spec:724-730), adr-0186:91-94.

**Re-derived** from `transport/http/stdlib/groups.go`: routes register through the
local `handle(...)` helper. `grep -cE '\bhandle\('` → **27**, minus the `func handle(`
declaration at `:16` ⇒ **26 registrations**. Full enumeration:

- **non-admin, 9**: `:40` POST /instances · `:54` GET /instances/{id} ·
  `:64` …/snapshot · `:74` …/actionable · `:84` POST …/signals ·
  `:112` POST /messages · `:139` POST /tasks/{token}/claim ·
  `:154` …/complete · `:169` …/reassign.
- **admin, 15**: `:212, :233, :247, :265, :281, :297, :318, :328, :343, :358,
  :368, :383, :404, :420, :445`.
- **health, 2**: `:472` /healthz · `:478` /readyz.

9 + 15 + 2 = **26**. ✓ The spec's parenthetical list of the nine non-admin routes
is exactly right, and none of the 26 accepts a process definition.

**Verdict: CONFIRMED** — table, prose and sum all agree.

---

## C-6 — CONFIRMED — "five 4xx classes echo `err.Error()`", with correct line numbers

**Where**: spec §2.7 (spec:237-240), adr-0186:71-73, adr-0186 D5 table.

**Re-derived** by reading `transport/http/httpcore/errors.go` in full (60 lines).
`ClassifyError`'s switch has exactly **six** arms:

| status | line | Message |
|---|---|---|
| 404 not_found | `:31` | `err.Error()` |
| 403 forbidden | `:33` | `err.Error()` |
| 409 conflict | `:35` | `err.Error()` |
| 400 bad_request | `:50` | `err.Error()` |
| 422 conflict_state | `:56` | `err.Error()` |
| 500 internal_error | `:58` | **blank** |

Five echo, one blanks, the set is closed (no other `return http.Status…` exists
in the file). Every cited line number lands on the exact arm claimed.

**Verdict: CONFIRMED.**

Cross-doc nit (MINOR): ADR-0186 D5's table and plan phase 7 both add a **413**
arm; spec §4.7 Option B's enumeration (*"400 / 409 / 422 / 404: message kept"*)
never mentions 413, so the spec's per-class policy is one class short of the two
documents that implement it. Add the 413 row to spec §4.7.

---

## C-7 — WRONG (Minor) — "the caret line reprints it a third time" is refuted by the document's own pasted output

**Where**: spec §2.7 (spec:252-256); adr-0186:75-77 (*"plus a caret line rendering
it again"*).

**Re-derived** by counting occurrences in the spec's own verbatim probe `[3]`
output (spec:244-248). The string
`vars.internalApprovalLimit > actor.attributes.tier` appears **exactly twice**:
once inside `\"…\"` (the `%q` at `internal/expreval/expreval.go:135` — citation
CONFIRMED, the `%q` verb is there) and once after `\n | ` (expr's own snippet).
The third line is `| ...................................^` — dots and a caret. It
does **not** reprint the source in any form.

**Verdict: WRONG** in the recap sentence; the count "twice" that precedes it is
right. Textbook over-generalising recap appended to correct detail.

**Proposed replacement**: *"The predicate source appears **twice** — once from
`%q` … and once inside expr's own snippet; a third line renders a caret ruler
under it (dots and `^`, no source text)."*

---

## C-8 — CONFIRMED — `service` counts: 10 options, 6 `DurableProvider` methods, no standalone authorizer setter

**Where**: spec §2.2 (spec:89-99), adr-0185:36-43.

- `grep -n '^func With' service/options.go` → **10**: `WithProcessDriver:39`,
  `WithInstanceStore:48`, `WithDefinitions:57`, `WithLister:67`,
  `WithHumanTasks:77`, `WithActorResolver:99`, `WithClock:109`,
  `WithIDGenerator:120`, `WithoutEmbeddedDefinition:146`, `WithDurableStore:169`.
  ✓ And **none** is a standalone authorizer setter — `WithHumanTasks(taskStore, az)`
  at `:77` is the only `authz.Authorizer` entry point. ✓
- `DurableProvider` (`service/durable.go:17-24`) has exactly **six** methods
  (`InstanceStore`, `Definitions`, `Lister`, `TaskStore`, `TimerStore`,
  `CallLinkStore`) and no `Authorizer()`. ✓ Citation `:17-24` exact.
- `service/service.go:199-200` is verbatim
  `if c.authz == nil {` / `c.authz = authz.AllowAll{}`. ✓
- `service/options.go:169-181` is exactly `WithDurableStore`'s body. ✓

**Verdict: CONFIRMED** (all five).

---

## C-9 — WRONG (Major) — the `slog.LevelDebug` citation `service/service.go:315-317` points at the *label*, not the *level*

**Where**: spec:90-91, adr-0185:37-38, plan:286, plan:306.

**Re-derived**: `grep -n 'LevelDebug' service/service.go` → **one hit, `:323`**.
Lines `:315-317` are
`authzLabel := "custom"` / `if _, ok := c.authz.(authz.AllowAll); ok {` /
`authzLabel = "allow-all"` — the label *computation*. The level lives at
`:323`, `slog.Default().LogAttrs(context.Background(), slog.LevelDebug, …)`.

This is the dangerous kind of rotted citation: it lands on plausible, related,
**wrong** code. An implementer told *"logs allow-all at WARN
(`service/service.go:315-317`)"* edits the label block and never finds the level.

**Second-order design gap this exposes** (worth the controller's attention):
`:323` emits **one** record covering store, definitions, taskStore, authz and a
hint. "Log allow-all at WARN" cannot be done by changing that constant without
promoting the entire unrelated summary to WARN. ADR-0185 D2 and plan phase 5 do
not say which is intended — a separate WARN record, or the whole summary moved.

**Verdict: WRONG.**

**Proposed replacement**: *"the summary's level is `slog.LevelDebug` at
`service/service.go:323`; the allow-all **label** is computed at `:315-317`.
Emitting allow-all at WARN requires a **separate** record — the existing
`LogAttrs` call carries four unrelated attributes that must stay at DEBUG."*

---

## C-10 — WRONG (Major) — the `WithDurableStore`/`WithHumanTasks` "ordering trap" does not exist in the form the spec prescribes, and phase 5's test cannot fail

**Claim**: spec §2.2 (spec:98-102) — *"the durable wiring must be written
`WithDurableStore(p)` **then** `WithHumanTasks(nil, az)`; the reverse order loses
one or the other depending on option order. **This ordering trap is the real
ergonomics defect**"*; adr-0185:39-41; plan phase 5 test 3
`TestDurableStoreOptionOrderIsIrrelevant`, *"**Fails today:** `WithDurableStore`
(`options.go:169-181`) overwrites `c.taskStore`, so the two orders differ."*

**Re-derived** from the two option bodies:

```go
func WithHumanTasks(taskStore humantask.TaskStore, az authz.Authorizer) Option {  // options.go:77
    if taskStore != nil { c.taskStore = taskStore }
    if az != nil        { c.authz = az }
}
func WithDurableStore(p DurableProvider) Option {                                 // options.go:169
    ...; c.taskStore = p.TaskStore(); ...   // never touches c.authz
}
```

`WithDurableStore` **does not write `c.authz` at all**. So for the exact wiring
the spec names — `WithHumanTasks(nil, az)` — a **nil** task store is ignored and
the two orders are already **identical**:

| order | c.taskStore | c.authz |
|---|---|---|
| `WithDurableStore(p)`, `WithHumanTasks(nil, az)` | provider's | `az` |
| `WithHumanTasks(nil, az)`, `WithDurableStore(p)` | provider's | `az` |

The trap is real only in the **narrower** case the documents never state: a
consumer passing a **non-nil** task store *before* `WithDurableStore`, whose
store is then silently overwritten by the provider's.

Damage: plan phase 5 test 3 is prescribed with the spec's own `(nil, az)` wiring
as its premise. Written that way the fixture **cannot fail today** — both orders
already agree — so it is another test that goes green without ever having been
red. This is the repo's recurring vacuous-fixture failure, prescribed in the plan.

**Verdict: WRONG.**

**Proposed replacement** (spec §2.2 and plan phase 5 test 3):

> `WithDurableStore` assigns `c.taskStore = p.TaskStore()` unconditionally and
> never touches `c.authz`, so `WithHumanTasks(nil, az)` is order-**independent**.
> The trap is narrower: `WithHumanTasks(myStore, az)` written **before**
> `WithDurableStore(p)` has `myStore` silently replaced by the provider's.
> ⚠ **Fixture check:** `TestDurableStoreOptionOrderIsIrrelevant` must pass a
> **non-nil** task store to `WithHumanTasks`, or it cannot fail today.

---

## C-11 — MAJOR — `WithActorResolver` is an **already-taken** name in three packages, meaning something else

**Where**: adr-0185:112, plan:362, plan:453 (`cfg.ActorResolver(...)`),
spec §4.1 Option B/C (spec:374, spec:388).

**Re-derived**: `grep -rn 'ActorResolver' --include='*.go' .` → **180 hits**. The
established concept is `humantask.ActorResolver` — *"expands an eligibility spec
together with process variables into concrete actors"*
(`humantask/humantask.go:170-176`), i.e. **candidate expansion**, not caller
authentication. Three packages already export an option of exactly the proposed
name:

- `service.WithActorResolver(r humantask.ActorResolver)` — `service/options.go:99`
- `task.WithActorResolver(r humantask.ActorResolver)` — `runtime/task/service.go:113`
- `processtest.WithActorResolver(r humantask.ActorResolver)` — `processtest/harness.go:104`

plus `task.ErrNoActorResolver`, `humantask.StaticActorResolver`,
`runtime.WithHumanTasks(resolver humantask.ActorResolver, …)`.

The bundle adds a **fourth** `WithActorResolver`, in `httpcore`, whose type is
`func(context.Context) (authz.Actor, error)` and whose meaning is *"who is the
caller"* — the opposite direction of data flow. A reader who knows the repo will
read `httpcore.WithActorResolver` as candidate expansion.

**Verdict: WRONG (naming collision), not a count error but an inherited-symbol
failure this lens is asked to catch.**

**Proposed replacement**: name the new seam for what it does — e.g.
`httpcore.WithActorFromContext(fn)` / `CustomizeConfig.ActorFromContext`, or
`WithPrincipalResolver` / `CustomizeConfig.PrincipalResolver`. Whatever is
chosen, ADR-0185 must state explicitly that it is **not**
`humantask.ActorResolver` and say why the names differ.

Pre-existing nit (out of bundle scope, but the bundle edits this godoc region):
`authz/authz.go:34` links `[ActorResolver]` from package `authz`, where no such
symbol exists — the target is `humantask.ActorResolver`. Broken godoc link today.

---
## C-12 — CRITICAL — every `engine/step_triggers.go` citation is 10 lines stale **at the commit the bundle lives on**, and `:839` now lands in a different function

**Claims**: spec:196 (*"`handleHumanCompleted` (declared `:839`)"*), spec:199-201
(*"`Actor: t.Actor, // :932`"*), spec:206 (the `awk 'NR>=839 && NR<=960'` probe),
adr-0185:73 (*"`engine/step_triggers.go:839`, write at `:931-936`"*),
plan:216 and plan:226-228 (both reprint `:839` and the awk window).

**Re-derived at the bundle commit `3f317b63`:**

| symbol | bundle says | at `3f317b63` | at declared base `70a631e9` |
|---|---|---|---|
| `func handleHumanCompleted` | `:839` | **`:849`** | `:839` ✓ |
| `task.Completion = &humantask.Completion{` | `:931-936` | **`:941-946`** | `:931` ✓ |
| `Actor: t.Actor` | `:932` | **`:942`** | `:932` ✓ |

The citations were correct against the declared base and are **wrong at the
commit that carries the documents**: `3f317b63` itself modifies
`engine/step_triggers.go` (`git diff --stat 70a631e9 3f317b63` → `+11 −1`).

Why this is Critical rather than cosmetic: `:839` at HEAD is **inside
`applyOutcomeExposure`** (`:830-848`) — a different function. This is exactly
the "rotted line number that lands on plausible but unrelated code" case. A
phase-3b agent navigating to `:839` edits the wrong handler.

**The measurement window is wrong at both ends.** The true body is `:849-983`
(next `func` at `:984`). The prescribed window `NR>=839 && NR<=960`:
- starts **10 lines early**, so its single reported hit (relative line 10 ⇒
  absolute **848**) is in the function's **godoc**, *outside* the body;
- ends **23 lines short**, never examining `:961-983`.

Re-run over the true body (`awk 'NR>=849 && NR<984' | grep -n
"Candidates\|Eligibility\|Claim"`) → **ZERO hits**.

So the claim *"one hit, and it is a comment"* is a window artifact. The
**conclusion** ("no comparison exists") is CONFIRMED — and in fact stronger than
stated. Only the number and the window are wrong.

**Verdict: WRONG** (citations + measurement window); conclusion CONFIRMED.

**Proposed replacement**: cite **symbols, not lines** (per
[[audited-bundle-decays-when-base-moves]]):

> `handleHumanCompleted` in `engine/step_triggers.go` writes
> `task.Completion = &humantask.Completion{Actor: t.Actor, …}` with no reference
> to the claim. Re-derived over the whole function body (from `func
> handleHumanCompleted` to the next top-level `func`):
> `grep -n "Candidates\|Eligibility\|Claim"` → **zero hits**. ⚠ Do not quote line
> numbers for this file: the bundle commit itself moves them.

---

## C-13 — MAJOR — the bundle declares ONE base but cites TWO, and says so in a sentence that is false as a quantifier

**Claim**: spec:12 *"Base: `main` @ `70a631e9`"*; spec:64-66 *"**Everything** in
this section was executed … **or read from source at `70a631e9`**"*; spec:824-825
*"Source-verified without execution (greps and reads at `70a631e9`): **every**
line/symbol citation in §2 and §6."*

**Re-derived**: §2.10's citations do **not** exist at `70a631e9`.
`git show 70a631e9:internal/authz/casbin/db.go | grep -n newPolicyReloadCallback`
→ **no output**; the symbol, the
`wrkflw_authz_policy_reload_failures_total` counter and the deferral comment are
introduced by `3f317b63`. §2.10 says so honestly in prose (*"already fixed on
the **working tree**"*) — but that directly contradicts the two blanket
quantifiers above it (*"Everything … at `70a631e9`"*, *"**every** line/symbol
citation"*).

Combined with C-12, the bundle's citations are split across two revisions with
no marker saying which is which. A reader cannot tell whether a given line
number is base-relative or HEAD-relative.

**Verdict: WRONG** (the quantifier), and it is the root cause of C-12.

**Proposed replacement**: re-anchor the whole bundle on `3f317b63` (the commit it
ships in) and re-derive every line citation there; drop "Base: `70a631e9`" or
demote it to "branched from". Then the two `every`-sentences become true.

---

## C-14 — ⛔ CRITICAL — there are **TWO** ABAC predicate evaluation sites; the bundle names one, and D3 actively steers consumers onto the one left fail-open

**Claim**: adr-0185 Decision 4 (adr-0185:176-183) — *"This is scoped to the
**ABAC path only**, and the scoping is **structural rather than conventional**:
`authz` already owns a separate evaluator instance (`authz/authz.go:23`,
`expreval.New()`) from the engine's (`engine/conditions.go:43` …). Gateway
evaluation … is untouched **because it never enters the strict path**."*
Same two-element enumeration at spec §4.3 Option A (spec:458-462).

**Re-derived** — `grep -rn '\.EvalBool(' --include='*.go' . | grep -v _test`
and `grep -rn 'spec\.Attribute' … `:

| # | site | evaluator | fixed by D4? |
|---|---|---|---|
| 1 | `authz/authz.go:136` (`RoleAuthorizer.Authorize`) | `attrEval = expreval.New()` (`authz/authz.go:23`) | ✅ yes |
| 2 | **`internal/authz/casbin/authorizer.go:68`** (`Authorizer.Authorize`) | **its own** `attrEval: expreval.New()` (`:30`) | ❌ **NO** |

Both read `spec.Attribute`; both call `EvalBool`; both therefore allow a
deny-list predicate over an absent variable. The public `casbinauthz` wrapper
delegates straight into it (`casbinauthz/casbinauthz.go:163`,
`a.inner.Authorize(...)`).

**Why this is the Critical of the sweep:**
- CLAUDE.md makes **casbin the baseline authorizer** ("pluggable; **casbin** as
  the baseline").
- ADR-0185 **Decision 3** (adr-0185:154-157) tells consumers, in the same ADR:
  *"a consumer who wants privileges evaluated **wires the casbin authorizer**."*
- So the bundle closes the hole on `RoleAuthorizer` and simultaneously pushes
  the security-conscious consumer onto the authorizer where the hole remains —
  and the ADR calls the scoping *"structural rather than conventional"*, i.e.
  claims a guarantee it does not have.

The `expreval.New(` instance count is also wrong as a premise: **four** non-test
instances exist (`authz/authz.go:23`, `internal/authz/casbin/authorizer.go:30`,
`engine/conditions.go:43`, `runtime/processdriver_options.go:200`), not the two
the "structural" argument reasons over.

Note: spec §4.3 mentions casbin's path exactly once — as a limitation of
**Option B**, which was then *demoted to a non-fatal warning*. Option A (the
shipped decision) and ADR-0185 D4 never mention it. The one place it was written
down is the option that did not ship.

**Verdict: WRONG — a missed site, with a false "by construction" guarantee on top.**

**Proposed replacement** (ADR-0185 D4):

> The strict-reference rule applies to **both** ABAC evaluation sites, which are
> the only two in the repo today: `authz.RoleAuthorizer.Authorize`
> (`authz/authz.go`) and `casbin.Authorizer.Authorize`
> (`internal/authz/casbin/authorizer.go`). Each constructs its own
> `expreval.New()`; both must become `expreval.New(expreval.WithStrictReferences())`.
> Gateway evaluation is untouched because it uses a **different** evaluator
> (`engine/conditions.go`, `expreval.WithTimeout(0)`) and never reads
> `spec.Attribute`. ⚠ Do **not** describe the scoping as "structural": four
> `expreval.New(` instances exist; the property holds because the strict option
> is opt-in per instance, not because of package boundaries.

⚠ **Plan impact**: phase 8's brief is `casbinauthz` + `internal/authz/casbin`
for the *stale-policy* work only. It must additionally carry the strict-reference
swap and a `TestCasbinAuthorizerDeniesDenyListPredicateOverAbsentVariable`
mirroring phase 2 test 3 — otherwise the delivery ships a fix that the baseline
authorizer does not have.

---

## C-15 — CONFIRMED (with a caveat) — the casbin stale-policy citations

`internal/authz/casbin/db.go` at `3f317b63`: `newPolicyReloadCallback` spans
exactly **`:76-99`** ✓ (spec §2.10); the returned closure is **`:87-98`** ✓
(plan phase 8); the metric `wrkflw_authz_policy_reload_failures_total` is at
`:83` ✓; the ERROR log is `tel.Logger.ErrorContext` at `:90` ✓; the deferral
comment (*"See that function for why the reload failure is not turned into a
fail-closed enforcement decision here"*) is at `:159-161` ✓.

**Caveat**: none of this exists at the declared base `70a631e9` — see C-13.

**Verdict: CONFIRMED at HEAD.**

---

## C-16 — MAJOR — the "engine gate is open" godoc exists at **two** sites; phase 12 lists one

**Claim**: plan:524 — *"`definition/activity/options.go:220-222`'s godoc, which
currently states the open default as fact."* (singular, and the only godoc named).

**Re-derived**: `grep -rn 'engine gate is open' --include='*.go' .` → **2 hits**:

1. `definition/activity/options.go:221` — on `WithEligibleRoles`
   (*"With no eligibility set, the engine gate is open and authorization defers
   to the consumer's transport layer (e.g. HTTP security middleware). See
   ADR-0117."*) — cited ✓, range `:220-222` exact.
2. **`definition/activity/activity.go:159`** — on **`NewUserTask`** itself
   (*"Eligibility is optional … with none set, the engine gate is open
   (authorization defers to the transport layer). See ADR-0117."*) — **not cited
   anywhere in the bundle.**

Site 2 is the more prominent of the two: it is the godoc on the constructor
every consumer calls. Ship phase 12 as written and `NewUserTask`'s own
documentation still tells consumers the reversed decision is the truth.

**Verdict: WRONG (count 1 → 2).**

**Proposed replacement**: *"the two godocs stating the open default as fact —
`definition/activity/activity.go`'s `NewUserTask` and
`definition/activity/options.go`'s `WithEligibleRoles` — must both be corrected
(`grep -rn 'engine gate is open' --include='*.go' .` finds exactly these two)."*

---

## C-17 — MAJOR — ADR-0117 **Decision 1** resolves, but **Decision 3** is amended too and nobody says so

**Claim**: adr-0185:12, :53, :163, :270 and plan:522 all name **ADR-0117
Decision 1** as the single amended decision (*"Only the meaning of *none of
them* changes"*, *"its Decision 1 must be annotated in place"*).

**Re-derived** by reading `docs/adr/0117-optional-usertask-eligibility.md`:
headings are only `## Context` / `## Decision` / `## Consequences`, **but** the
Decision body is a numbered list 1–4, so *"Decision 1"* **does** resolve — the
`§`-citation hazard does not bite here. Decision 1 contains verbatim: *"with
none set, the engine gate is open and authorization defers to the transport
layer."* ✓

**However, Decision 3 states the same property independently and more
explicitly**: *"**All three dimensions are co-equal and independently optional.**
Any combination (**including none**) is valid."*

ADR-0185 D3 makes "none" **invalid** without `Open`. That reverses Decision 3's
parenthetical directly. Annotating only Decision 1 leaves ADR-0117 Decision 3
asserting, as live ADR text, the exact proposition ADR-0185 overturns.

This also undercuts adr-0185:163-166 (*"its authoring API (co-equal,
independently optional dimensions …) stands unchanged"*) — Decision 3 **is** that
sentence, and its "including none" clause does not stand unchanged.

**Verdict: WRONG (1 amended decision → 2).**

**Proposed replacement**: *"Amends ADR-0117 **Decisions 1 and 3** — Decision 1's
'with none set, the engine gate is open' and Decision 3's 'any combination
(including none) is valid'. Both must be annotated in place."*

---

## C-18 — MAJOR — "the **13** `examples/scenarios/*` mains" is **12**

**Claim**: plan:499 — *"The 13 `examples/scenarios/*` mains that call
`runtime.WithHumanTasks(...)`"*.

**Re-derived**: `grep -rln 'runtime\.WithHumanTasks' examples/scenarios/` →
**12 files** (15 call sites): `attribute_authz`, `boundary_action`,
`completion_action`, `input_validation`, `instance_cancellation`,
`inwait_reminder`, `manual_task`, `message_boundary`, `reverse_rollback`,
`terminate_end`, `usertask_approval`, `usertask_deadline`.

Repo-wide in `examples/` the figure is **16** files — the extra four are
`cache_wiring`, `mysql_wiring`, `production_wiring`, `sqlite_wiring`, which the
plan's sentence excludes by the `scenarios/` path but which phase 11 must touch
anyway (three of them are the `stdlib.Mount` trio of C-2).

**Verdict: WRONG (13 → 12).** Damage limited because the plan does say
*"Enumerate them mechanically; do not guess"* — but the number is the thing an
agent budgets against.

**Proposed replacement**: *"the **12** `examples/scenarios/*` mains that call
`runtime.WithHumanTasks` (15 call sites) — enumerate with
`grep -rln 'runtime\.WithHumanTasks' examples/scenarios/`; note four further
`examples/*_wiring` mains also call it."*

---

## C-19 — MINOR — the probe-[7] predicate is **80** characters, not 44 (restated three times)

**Claim**: spec:291 (*"**44 characters**, far under 1e4 AST nodes"*), spec:805
(*"predicate is 44 chars"*), adr-0186:46 (*"a **44-character** predicate"*).

**Re-derived**:
`printf '%s' 'count(vars.rows, {let x = #; count(vars.rows, {# == x}) == 1}) == len(vars.rows)' | wc -c`
→ **80**.

The *argument* (80 chars is still far under a 10 000-node budget) is unaffected,
which is why nobody checked it — and it was then restated in two more places.

**Verdict: WRONG (cosmetic).** Replace all three with **80**, or drop the
character count and state the AST node count, which is what the argument
actually needs.

---

## C-20 — CRITICAL — D3's blast radius is never counted, and the one quantifier offered for it is false

**Claim**: adr-0185:246-249 — *"**Every** existing definition with no eligibility
becomes invalid until it declares `open: true` or a dimension."* No number
anywhere in the bundle. (Contrast D1, whose breakage is counted to the line.)

**Re-derived** (AST-aware scan of all 274 `NewUserTask(` call sites, matching the
option list against `WithEligibleRoles|WithEligiblePrivileges|WithEligibleExpr`):

| population | count |
|---|---|
| `NewUserTask(` call sites, repo-wide incl. tests | **274** |
| …carrying **no** eligibility dimension | **128** |
| …of those, in files that reach `model.Validate` (via `NewBuilder`/`Loader`) | **5** (`examples/scenarios/manual_task/main.go` ×2, `runtime/manual_task_test.go` ×3) |

`model.Validate` has exactly **one** non-test caller:
`definition/model/builder.go:133`, inside `definitionCore.build()` (reached from
`DefinitionBuilder.Build` `:198` and `DefinitionLoader.Build` `:227`).

Two consequences the bundle misses:

1. **The quantifier is false.** Definitions built as `&model.ProcessDefinition{…}`
   struct literals never reach `model.Validate` and stay valid. That is the
   dominant idiom in `engine`'s tests — I sampled
   `engine/step_compensation_walk_error_test.go:43`, which builds the literal
   directly. So "every existing definition … becomes invalid" is wrong: the
   **authoring gate** touches ~5 sites.
2. **The two halves of D3 have different blast radii and the bundle conflates
   them.** The *authoring* gate (`model.Validate`) hits ~5 sites; the *runtime*
   rule (`RoleAuthorizer` denies an unstated spec) hits all **128**
   no-eligibility UserTasks the moment they are authorized — across `engine`,
   `runtime`, `processtest`, `service` and `examples`, i.e. packages that have
   **no phase in the plan** for this fallout (phase 4's verification is
   `go test ./definition/...` only).

The affected sites are also concentrated on **manual tasks**, which are
no-eligibility *by design* under ADR-0117 — so the migration is not mechanical
`open: true` sprinkling; it is a semantic decision per task.

**Verdict: WRONG (false quantifier) + a missing enumeration.**

**Proposed replacement** (ADR-0185 Consequences, and a new plan phase):

> Re-derived at `3f317b63`: **274** `NewUserTask` call sites, of which **128**
> declare no eligibility dimension. The **authoring** gate reaches only those
> that pass through `model.Validate` (one caller:
> `definition/model/builder.go`) — **5 sites in 2 files**, both manual-task
> flows. The **runtime** rule reaches all **128** when authorized. Definitions
> built as `model.ProcessDefinition` struct literals are never validated and are
> unaffected by the authoring gate. The plan must carry a fixture-migration task
> for `engine`, `runtime`, `processtest` and `service` — not only `definition`.

---

## C-21 — CONFIRMED — `DefaultMaxNodes = 1e4`, and the inversion, verified in the vendor as well as by probe

**Where**: spec §2.8 (spec:277-285), spec §9 `[5]`, adr-0186:42-48, plan:145-147.

**Re-derived** in `$GOMODCACHE/github.com/expr-lang/expr@v1.17.8` (go.mod pins
`v1.17.8` ✓):
- `conf/config.go:18` — `DefaultMaxNodes uint = 1e4` ✓
- `conf/config.go:51` — `CreateNew()` seeds `MaxNodes: DefaultMaxNodes` ✓
- `parser/parser.go:109` — `if p.config.MaxNodes > 0 && p.nodeCount > p.config.MaxNodes` ⇒ **0 disables** ✓
- `expr.go:221` — the vendor's own godoc states it outright: *"If MaxNodes is set
  to 0, the node budget check is disabled."*

**Verdict: CONFIRMED** on every leg. The "do NOT call `expr.MaxNodes`"
instruction is correct.

Nit: the bundle presents this as discovered by execution. It is also stated
verbatim in the vendor godoc — worth citing, since a reader can check that in
five seconds and the probe in five minutes.

---

## C-22 — MAJOR — "the class … is **five predicate forms wide**" mis-describes the class as closed

**Claim**: spec:148 — *"the class it never named is real and is **five predicate
forms wide**"*; adr-0185:58-62 lists the same five; plan phase 1 test 2 and
phase 2 test 3 both table *"the five forms"*.

**Re-derived by reasoning over the mechanism the bundle itself established**
(spec §2.4.1, vendor `vm/runtime/runtime.go:58-70`): a missing map key yields
`nil` **unconditionally**. Therefore the failing class is *every predicate whose
truth value is `true` when a referenced key is `nil`* — which is unbounded, not
five. Trivial further members not in the list: `not vars.blocked`,
`vars.owner != actor.ID`, `vars.deleted == nil`, `vars.state not in ["banned"]`,
`vars.a == vars.b` (both absent ⇒ `nil == nil` ⇒ true).

Five forms were **sampled and executed** — that is good evidence and the spec's
probe table is honest. The defect is the recap sentence promoting a sample to a
closed set, and the plan then prescribing tests "over the five forms" as if
exhaustive.

Damage is bounded because the chosen mechanism (strict *reference* checking) is
key-based, not form-based, so it closes the whole class regardless. But a
reviewer reading "five forms wide" may accept a form-matching implementation as
sufficient.

**Verdict: WRONG (quantifier).**

**Proposed replacement**: *"the class is every predicate that evaluates `true`
when a referenced key resolves to `nil`; it is unbounded. **Five** members were
executed (§9 `[2]`) and are the table the tests use — they are a sample, not the
class."*

---

## C-23 — CONFIRMED — citation batch (all resolve exactly at `3f317b63`)

| citation | claim | verdict |
|---|---|---|
| `httpcore/endpoints.go:119,132,150` | the **three** `authz.Actor{…}` constructions | ✓ exact, and the **only** three in `transport/` non-test |
| — | all three project `{ID, Roles}` only, dropping `Attributes` | ✓ |
| `httpcore/seam.go:19-33` | `CustomizeConfig` declares exactly `BasePath, Wrap, InstanceMapper, Logger, TracerProvider, MeterProvider` — **6** fields, no identity seam | ✓ exact |
| `Logger` godoc *"receives 5xx raw error details (never sent to clients)"* | quoted by adr-0186 D5 | ✓ verbatim at `:29` |
| `httpcore/dto.go:12,43,49,63` | `Actor`, `ClaimInput`, `CompleteInput`, `ReassignInput` declarations | ✓ all four exact |
| `httpcore/view.go:31` | `Variables: st.Variables` (alias, not copy) | ✓ exact |
| `authz/authz.go:23` | `var attrEval = expreval.New()` | ✓ |
| `authz/authz.go:38` | `Actor.Attributes` | ✓ |
| `authz/authz.go:79-86` | `AuthzSpec` godoc *"An empty spec means allow-all."* | ✓ verbatim at `:81` |
| `authz/authz.go:119-120` | `Privileges` documented reserved / NOT evaluated | ✓ verbatim |
| `authz/authz.go:124` | `RoleAuthorizer.Authorize` declaration | ✓ |
| `authz/authz.go:136-141` | eval error wrapped with `ErrNotAuthorized` | ✓ |
| `internal/expreval/expreval.go:135` | the `%q` that echoes the predicate source | ✓ exact, `%q` present |
| `runtime/task/service.go:199,234,255,306` | the **four** `Authorize` sites, all passing `task.Eligibility` | ✓ exact (repo-wide there are 5 `.Authorize(` non-test; the 5th, `casbinauthz:163`, is a delegation) |
| `engine/conditions.go:20-27` | `ConditionEvaluator`, **three** methods, none taking ctx | ✓ exact |
| `engine/conditions.go:43` | `expreval.New(expreval.WithTimeout(0))` | ✓ exact |
| `engine/conditions.go:29-42` | the `conditions` var godoc phase 3a must update | ✓ exact |
| `engine/step.go:152` | `func Step(ctx context.Context, …)` | ✓ exact |
| `engine/step_nodes.go:723` | `spec := authz.AuthzSpec{` | ✓ exact (**and** unchanged between base and HEAD) |
| `engine/state.go:248-262` | `NodeVisit`, **no actor field** | ✓ exact; fields are `NodeID, TokenID, EnteredAt, LeftAt, TaskID, CloseKind` |
| `…/migrations/sqlite/0001_init.sql:25,40` | `snapshot TEXT NOT NULL`, `trigger TEXT NOT NULL` | ✓ both exact |
| `wrkflw_journal` is **6** columns | `instance_id, seq, kind, trigger, occurred_at, applied_at` | ✓ exactly six, no hash/prev-hash/signature |
| `definition/activity/options.go:220-222` | the open-default godoc | ✓ exact (**but** see C-16 — there is a second) |
| `transport/http/{stdlib:189,gin:204,fiber:209}/groups.go` | the **three** `SECURITY:` sites | see C-24 |
| `internal/authz/casbin/authorizer.go:70` | "the casbin authorizer's own predicate path" | ✓ close enough — `:70` is the error-wrap; the `EvalBool` is `:68`, the block spans `:66-74` |
| ADR-0186 §Decision 6 (cited from spec:57) | resolves | ✓ `### 6. At rest…` |
| ADR-0145 | "the actor lives on the task record" | ✓ `0145-nodevisit-audit-linkage-and-token-state-rename.md`; `NodeVisit.TaskID` godoc says exactly that |
| ADR-0182 | "never-due authoring gate precedent" | ✓ `0182-reject-never-due-triggers-at-definition-validation.md` |
| ADR-0183 | "the claim invariant" | ✓ `0183-a-human-tasks-claim-invariant-is-enforced-on-write.md` |
| ADR-0185 / ADR-0186 numbers | genuinely free | ✓ highest existing is 0184; no duplicate `NNNN` prefixes in `docs/adr/` |

---
## C-24 — CONFIRMED — the three `SECURITY:` sites, all admin-only

`grep -rn 'SECURITY:' --include='*.go' . | grep -v _test` → exactly **3**:
`transport/http/stdlib/groups.go:189`, `gin/groups.go:204`,
`fiber/groups.go:209`. All three carry the identical text *"these routes have NO
built-in authentication. Mount AdminRoutes only …"*, i.e. all three are on the
**admin** group; the instance and task groups carry none. Line numbers exact.

**Verdict: CONFIRMED** (spec §2.6, spec §6.4, adr-0186:66-69).

---

## C-25 — CONFIRMED — fiber's 4 MiB is the framework's, not ours

`github.com/gofiber/fiber/v3 v3.4.0` (go.mod:14). Vendor:
`app.go:585` — `DefaultBodyLimit = 4 * 1024 * 1024` (4 MiB); `app.go:710` —
`app.config.BodyLimit = DefaultBodyLimit` applied in `New()`.

So ADR-0186 Context §1's *"Fiber's 4 MiB rejection is `fiber.DefaultBodyLimit`,
i.e. the framework's, not ours"* is **CONFIRMED**, and so is the arithmetic
*"two thirds of the surface has no cap"* — 26 of 39 sites (stdlib 13 + gin 13)
are uncapped, fiber's 13 are capped by the framework. 26/39 = two thirds exactly.

This also **corroborates** ADR-0186 Decision 1's reasoning that `BodyLimit` is a
`fiber.Config` field set on `fiber.New` — `app.go:710` is inside `New`. The
mechanism question (`len(c.Body())` pre-check) remains
`ASSUMPTION (unverified)` as the plan says; the *premise* is now source-verified.

---

## C-26 — MINOR — "53's **two** duplicate prose restatements" would delete a backlog line if followed literally

**Claim**: plan:528 — *"Delete 53's two duplicate prose restatements
(`HANDOVER.md` blocker-1 tail and the NEXT WORK bullet) — it is **one** item."*

**Re-derived** in `docs/plans/HANDOVER.md`:
- `:190` — *"1. ✅ CLOSED by ADR-0167. ⚠ Its tail (fail-open `AuthzSpec`) is
  **backlog 53**, still open → B3."* — a genuine cross-reference restatement. ✓
- `:114` — *"**B3 authz/security** — 51, 52, 53, 54, 65, 98, 99, 100, 101, 103,
  104, 124 (+ parked 102)."* — this is **not** a restatement of 53; it is the
  work-item line enumerating all twelve. Deleting it removes the B3 backlog
  entry itself.

So there is **one** prose restatement and **one** list membership. The claim
"it is one item" (53 is a single backlog item, not two) is **CONFIRMED**; the
instruction to delete two things is not.

**Verdict: WRONG (instruction), CONFIRMED (the underlying "one item" claim).**

**Proposed replacement**: *"Remove `HANDOVER.md`'s blocker-1 tail cross-reference
(`:190`) once 53 closes, and strike 53 from the B3 item list at `:114` — do not
delete the `:114` line, which enumerates the other eleven items."*

---

## C-27 — MINOR — the spec title enumerates 12 items; the bundle's scope is 13

spec:1 titles the bundle *"(backlog 51, 52, 53, 54, 65, 98, 99, 100, 101, 103,
104, 124)"* — **12**, matching §1's table (12 distinct items) and
`HANDOVER.md:114`. But §3 Scope, §5's ADR-split table, ADR-0185's `Backlog:`
line and plan phase 12 all additionally carry **the parked half of 102**.

`102` is the one item in scope that the title omits. Since phase 12's close-list
is the actionable enumeration and *does* include it, damage is nil — but the
title is the first thing a reader counts.

**Verdict: WRONG (cosmetic).** Add `+ parked 102` to the title, as
`HANDOVER.md:114` already does.

---

## C-28 — CONFIRMED — the `Privileges` leg of 53, by source

`RoleAuthorizer.Authorize` (`authz/authz.go:124-145`) reads **only**
`spec.Roles` (skipped when empty) and `spec.Attribute` (skipped when `""`), then
`return nil`. `spec.Privileges` is never read anywhere in the function — and
`grep -rn 'spec\.Privileges'` finds no reader in `authz` at all. So a
`Privileges`-only spec is non-empty yet unevaluated ⇒ admits the zero actor.

**Verdict: CONFIRMED** (spec §2.3, adr-0185 finding 3). The "extra leg found in
triage and recorded nowhere else" is real.

---

# Summary table

| # | claim as written | where | re-derived truth | verdict | severity |
|---|---|---|---|---|---|
| C-14 | ABAC strict-reference scoping is "structural"; one evaluator in `authz` vs the engine's | adr-0185:176-183, spec:458-462 | **two** ABAC sites (`authz/authz.go:136`, `internal/authz/casbin/authorizer.go:68`); **four** `expreval.New(` instances; casbin's stays fail-open, and D3 steers consumers to it | **WRONG** | ⛔ Critical |
| C-20 | "**Every** existing definition with no eligibility becomes invalid" (no count) | adr-0185:246-249 | 274 `NewUserTask` sites; **128** without eligibility; only **5** reach `model.Validate`; struct-literal defs unaffected; runtime rule hits all 128 across 4 unphased packages | **WRONG** | ⛔ Critical |
| C-1 | "**23** test pins / 9 files / 5 packages"; phase 9 "stdlib 3, gin 5, fiber 4" | spec:663-686, adr-0185:122,245, plan:466 | **29** lines — the grep misses `ReassignInput`'s `"by"` key; stdlib **5**, gin **7**, fiber **5**, httpcore **11**; two of the missed pins go green-but-vacuous | **WRONG** | ⛔ Critical |
| C-12 | `step_triggers.go:839` / `:931-936` / `:932`; `awk NR>=839 && NR<=960` → "one hit, a comment" | spec:196-206, adr-0185:73, plan:216-228 | `:849` / `:941-946` / `:942` at the bundle commit; `:839` is inside `applyOutcomeExposure`; body is `:849-983`; true hit count **zero** | **WRONG** | ⛔ Critical |
| C-13 | "**Everything** … read from source at `70a631e9`"; "**every** citation" | spec:64-66, 824-825 | §2.10's symbols do not exist at `70a631e9`; bundle cites two revisions with no marker | **WRONG** | Major |
| C-9 | allow-all logged at DEBUG "(`service/service.go:315-317`)" | spec:90-91, adr-0185:37, plan:286,306 | level is at **`:323`**; `:315-317` is the label; one record carries 4 unrelated attrs | **WRONG** | Major |
| C-10 | `WithDurableStore`/`WithHumanTasks` "ordering trap"; phase-5 test 3 | spec:98-102, plan:308-312 | `WithDurableStore` never touches `c.authz`; with `(nil, az)` both orders **already agree** ⇒ the prescribed test cannot fail | **WRONG** | Major |
| C-11 | new `httpcore.WithActorResolver` | adr-0185:112, plan:362,453 | `ActorResolver` already means candidate expansion; 3 packages export `WithActorResolver` already (180 hits) | **WRONG** | Major |
| C-16 | phase 12 fixes "`options.go:220-222`'s godoc" | plan:524 | **two** godocs state the open default; `definition/activity/activity.go:159` (on `NewUserTask`) is uncited | **WRONG** | Major |
| C-17 | amends "ADR-0117 **Decision 1**" | adr-0185:12,53,163,270, plan:522 | Decision 1 resolves ✓, but **Decision 3** ("any combination, including none, is valid") is reversed too and is never annotated | **WRONG** | Major |
| C-18 | "the **13** `examples/scenarios/*` mains" | plan:499 | **12** files, 15 call sites (+4 `*_wiring` mains outside `scenarios/`) | **WRONG** | Major |
| C-22 | "the class … is **five predicate forms wide**" | spec:148, adr-0185:58-62, plan:123,180 | the class is unbounded (any predicate true on a `nil` key); five were **sampled** | **WRONG** | Major |
| C-7 | "the caret line reprints it a third time" | spec:252-256, adr-0186:75-77 | the caret line is dots + `^`, no source text; source appears exactly **twice** | **WRONG** | Minor |
| C-19 | "**44**-character predicate" (×3) | spec:291,805, adr-0186:46 | **80** characters | **WRONG** | Minor |
| C-26 | delete 53's "**two** duplicate prose restatements" | plan:528 | one restatement (`:190`); `:114` is the B3 item list and must not be deleted | **WRONG** | Minor |
| C-27 | title enumerates 12 backlog items | spec:1 | scope is 12 **+ parked 102** | **WRONG** | Minor |
| C-4 | three "→ 0" greps | spec:272,319-320 | claims true, but two commands lack `-E` so they return 0 for any repo | evidence **UNFALSIFIABLE** | Minor |
| C-6b | spec §4.7 per-class policy | spec:582-594 | omits the **413** arm that adr-0186 D5 and plan phase 7 both add | **WRONG** | Minor |
| C-2 | "**three** `examples/` mains mount `TaskRoutes`" | spec §6.2, adr-0185:123 | exactly three, all `stdlib.Mount` | CONFIRMED | — |
| C-3 | 13 / 13 / 13 / 0 decode sites = 39; `BodyParser` dead | spec §2.8, adr-0186:25-33 | exact; `BodyParser` 0 repo-wide | CONFIRMED | — |
| C-5 | 26 routes = 9 + 15 + 2; no deploy route | spec §1.1, §6.5, adr-0186:91 | exact | CONFIRMED | — |
| C-6 | five 4xx arms echo, 500 blanks | spec §2.7, adr-0186:71-73 | exact, set closed, all six line numbers land | CONFIRMED | — |
| C-8 | 10 options, 6 `DurableProvider` methods, no authorizer setter | spec §2.2 | exact | CONFIRMED | — |
| C-15 | casbin `db.go:76-99`, `:87-98`, `:159-161` | spec §2.10, plan:422,434 | exact **at HEAD only** (see C-13) | CONFIRMED | — |
| C-21 | `DefaultMaxNodes = 1e4`; `MaxNodes(0)` disables | spec §2.8, adr-0186:42-48 | exact; vendor godoc `expr.go:221` says so outright | CONFIRMED | — |
| C-23 | 28 further line/symbol/ADR citations | throughout | all resolve exactly | CONFIRMED | — |
| C-24 | three `SECURITY:` sites, admin-only | spec §2.6, §6.4 | exact | CONFIRMED | — |
| C-25 | fiber's 4 MiB is `fiber.DefaultBodyLimit`; "two thirds uncapped" | adr-0186:29-31 | `app.go:585`, v3.4.0; 26/39 exactly two thirds | CONFIRMED | — |
| C-28 | a `Privileges`-only spec is unevaluated ⇒ allow-all | spec §2.3, adr-0185:45-52 | `spec.Privileges` has no reader in `authz` | CONFIRMED | — |

# Ranking by damage if acted on

1. **C-14 (Critical)** — ships a security fix the **baseline** authorizer does
   not get, while the same ADR tells consumers to wire that authorizer. A
   missed site, dressed as a structural guarantee. This is the ADR-0175-shaped
   finding.
2. **C-20 (Critical)** — the D3 migration is unsized and its only quantifier is
   false. 128 runtime-affected call sites live in `engine`, `runtime`,
   `processtest` and `service`, none of which has a phase. Phase 4's
   verification (`./definition/...`) cannot see the fallout.
3. **C-1 (Critical)** — three phase-9 agents get under-counts; two missed pins
   are *denial* assertions that stay green while asserting nothing.
4. **C-12 (Critical)** — `:839` lands in the wrong function at the commit the
   plan ships on; the prescribed falsifier measures the wrong 122 lines.
5. **C-10 (Major)** — a prescribed test that cannot fail today, in the repo's
   most-repeated failure mode.
6. **C-13, C-9, C-16, C-17, C-18, C-11, C-22 (Major)** — each sends an
   implementer or a doc phase to the wrong place, or leaves live text asserting
   the reversed decision.
7. **C-7, C-19, C-26, C-27, C-4, C-6b (Minor)** — cosmetic or evidence-hygiene.

# Lens note

The four Criticals divide cleanly: **two** are missed sites (C-14, C-20) that
only a re-count could find, and **two** are decayed citations (C-1, C-12) caused
by the bundle citing a base its own commit has already moved past. The bundle's
*arithmetic* is almost always right — 26 = 9+15+2, 39 = 13×3, 26/39 = two
thirds, 10 options, 6 methods, 6 columns, 5 arms, 3 mounts, 3 SECURITY sites,
4 Authorize sites all check out. What fails is the **net** (C-1's grep pattern,
C-14's two-element enumeration) and the **anchor** (C-12/C-13's base commit).

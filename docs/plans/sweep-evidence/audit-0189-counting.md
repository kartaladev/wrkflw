# ADR-0189 — COUNTING lens audit

Worktree: `wt-counting`, detached at `7fa756d0`.
Bundle present (step 0 PASS): spec 29602 B, ADR 14685 B, plan 48049 B.

⚠ NOTE ON BASE: the bundle documents all say **"Executed at `9789ebcc`"** and "Base: `main` at
`9789ebcc`". The audit worktree is at `7fa756d0`. Checked below (F0) whether the two trees
differ in any file the bundle counts over.

---
### F0 — base drift: NONE. The bundle's `9789ebcc` claims re-derive unchanged at `7fa756d0` — [INFO]
`git diff --stat 9789ebcc 7fa756d0` = the three bundle documents only, 1874 insertions, zero
code files. Every "executed at `9789ebcc`" claim is therefore directly checkable at HEAD.
No decay finding available here; all counting findings below are real, not base drift.

### F1 — the "5 packages / 9 files" scope MISSES `service/instance_test.go`, where TWO comments assert exactly the thing this bundle falsifies — [MAJOR]
**Bundle text attacked:** spec §2.6 table — *"total | **29** | **9** | **5**"*; and the ADR
Negative — *"**29 pin sites across 9 files in 5 packages** change"*. Plan Task 12 Step 3's
stale-reference sweep: `grep -rn '"actor"' README.md docs/ examples/`.

**The net the author used:** (a) compile ablation `go test -gcflags=-e -run '^$' ./...` after
deleting the fields; (b) grep for `"actor"`/`"by"` JSON keys in request bodies.
**What both structurally cannot see:** a **comment**. It neither breaks the compile nor
contains a JSON body key.

**The net that finds more:** `grep -rn "httpcore\.Actor" --include='*.go' .`

**Observed:**
```
transport/http/httpcore/dto_test.go:47        (counted — compile pin)
transport/http/httpcore/endpoints_test.go:405,422,466,485,531,560   (counted — compile pins)
service/instance_test.go:1090:// purpose: httpcore.Actor is {id, roles} only, so claim.actor / completion.actor
service/instance_test.go:1128:// reproducible; the transport is deliberately bypassed because httpcore.Actor
```
Read in full, the two uncounted sites say:

- `:1090` — *"The fixture is built through the Go API rather than the HTTP transport on
  purpose: **httpcore.Actor is {id, roles} only, so claim.actor / completion.actor could never
  carry the attributes the sample shows** (ADR-0147 amendment #5)."*
- `:1128` — *"the transport is **deliberately bypassed because httpcore.Actor cannot carry
  actor attributes** (ADR-0147 amendment #5)."*

**Correct value:** the blast radius touches a **6th package (`service`)** and a **10th file**.
These are not cosmetic: both comments state, as the documented *reason* for a test's design,
a limitation that ADR-0189 Decision 1 **deletes** (`httpcore.Actor` ceases to exist) and that
spec §3.2 explicitly reverses (*"the whole `authz.Actor` now flows, so `Attributes` reach the
authorizer"*). Shipping this bundle leaves two committed comments citing a deleted type and
asserting a false capability claim — precisely the "false claims in committed comments" the
CLAUDE.md Delivery Gate item 2 says are cheapest to kill at this gate.

**Concrete fix:**
1. spec §2.6 and the ADR Negative: restate as **29 behavioural pins in 9 files in 5 packages,
   plus 2 comment-rot sites in a 6th (`service/instance_test.go:1090,1128`)** — or widen the
   headline count to 31/10/6. Either is honest; the current number is not.
2. Plan Task 12 Step 3: replace the sweep with one whose net can see this class —
   `grep -rn 'httpcore\.Actor\|"actor"\|"by"' --include='*.go' --include='*.md' --include='*.json' .`
   over the **whole repo**, not `README.md docs/ examples/`. As written it searches neither
   `service/`, nor `transport/`, nor root-level `SECURITY.md`.
3. Add a Task-12 step rewriting both comments: the transport bypass is now a *historical*
   reason, and post-0189 the honest sentence is that the fixture is built through the Go API
   for determinism, not because the transport cannot carry attributes.

### F2 — CONFIRMED: three actor sites, eight `CustomizeConfig` fields, three `WithActorResolver`, nine adapter sites — [INFO / no defect]
Every one of these re-derives exactly, including line anchors. Recorded so the adjudicator
knows they were attacked and survived, not skipped.

| claim | net run | observed | verdict |
|---|---|---|---|
| "the only three `authz.Actor` constructions in `transport/`, non-test" | `grep -rn "authz\.Actor" transport/ \| grep -v _test.go` — **dropped the `{`** so a `var`/conversion/return would show | same 3 lines, `endpoints.go:119,132,150` | ✅ exact |
| `CustomizeConfig` declares **eight** fields, enumerated | `sed -n '/type CustomizeConfig/,/^}/p' httpcore/seam.go` | `BasePath, Wrap, InstanceMapper, MaxBodyBytes, BodyReadTimeout, Logger, TracerProvider, MeterProvider` = **8**, same order | ✅ exact (and the ⚠ correcting ADR-0185's inherited "six" is right) |
| `WithActorResolver` exported **three** times at `service/options.go:99`, `runtime/task/service.go:113`, `processtest/harness.go:104` | `grep -rn "^func WithActorResolver" --include='*.go' .` | exactly those 3 files **and exactly those 3 line numbers** | ✅ exact, anchors live |
| "`authz`'s own godoc links `[ActorResolver]` meaning that one" | `grep -rn ActorResolver authz/` | `authz/authz.go:34` — one hit, and `type ActorResolver` is declared only in `humantask/humantask.go:183` | ✅ exact |
| no existing `WithRequestActor` / `RequestActorFunc` / `RequestActor` symbol to collide with | `grep -rn "WithRequestActor\|RequestActorFunc\|RequestActor" --include='*.go' .` | **zero** Go hits outside the bundle's own docs | ✅ no collision |
| "nine adapter call sites", `fiber/groups.go:151,168,185 · gin:172,192,212 · stdlib:140,154,168` | `grep -rn "ClaimTask\|CompleteTask\|ReassignTask" transport/ \| grep -v _test.go \| grep -v httpcore/` | exactly 9, **exactly those line numbers** | ✅ exact, anchors live |
| "all nine pass `cfg.InstanceMapper` as the last argument" | same output | all 9 end `, cfg.InstanceMapper)` | ✅ exact |

### F3 — "the struct literal where `MaxBodyBytes` and `BodyReadTimeout` live" is a FALSE contrast: `Wrap`, `InstanceMapper` and `Logger` live there TOO — [MINOR]
**Bundle text attacked:** spec §3.5 — *"**The default is applied in `ResolveConfig`'s post-loop
nil-guard block**, alongside `Wrap`, `InstanceMapper` and `Logger` — *not* in the struct literal
where `MaxBodyBytes` and `BodyReadTimeout` live."* Same sentence in ADR Decision 2 and in the
plan's Task 3 Step 3 code comment (which will be COMMITTED as godoc).

**The net the author used:** read the post-loop guard block, which does contain exactly
`Wrap`/`InstanceMapper`/`Logger` — that half is right.
**The net that finds more:** read the *literal* too — `sed -n '/^func ResolveConfig/,/^}/p'`.

**Observed:**
```go
cfg := CustomizeConfig[R]{
    Wrap:           func(r R) R { return r },      // ← also in the literal
    InstanceMapper: func(st engine.InstanceState) any { return NewInstanceView(st) },
    Logger:         slog.Default(),                //  ← also in the literal
    MaxBodyBytes:    defaultMaxBodyBytes,
    BodyReadTimeout: defaultBodyReadTimeout,
}
```
**Correct value:** the existing convention is not *literal XOR post-loop guard*. It is
**both-for-the-nilable-three, literal-only-for-the-two-scalars**. The three fields the bundle
names as its model are seeded in the literal *and* guarded after the loop.

**Why it is not purely cosmetic:** the plan (Task 3 Step 3) adds the guard **only**. Options
run against the literal, so an option that *wraps* the previous value — the natural way to
chain resolvers, e.g. `prev := c.RequestActor; c.RequestActor = fallback(prev)` — sees a
non-nil `Wrap`/`InstanceMapper`/`Logger` but a **nil** `RequestActor`. That asymmetry is
invisible in the bundle because the bundle's stated model of the convention is wrong.

**Concrete fix:** correct the sentence in all three documents to *"guarded after the loop like
`Wrap`, `InstanceMapper` and `Logger` — and, like them, also seeded in the literal, unlike
`MaxBodyBytes`/`BodyReadTimeout` whose scalar types make the post-loop guard impossible"*, and
have Task 3 seed `RequestActor: defaultRequestActor` in the literal as well as guarding it.

## F4 — ⛔ CRITICAL — the compile ablation modelled only HALF the change. The "exhaustive" 11 is really 14, and the headline **29 / 9 / 5** is really **32 / 9 / 5**
**Bundle text attacked:**
- spec §2.6 table: *"**Compile-breaking** | **11** (17 errors) | 2 | **1** — `httpcore`"*, total *"**29** | **9** | **5**"*.
- spec §2.6: *"Compile-breaking, **exhaustive**: `httpcore/dto_test.go` lines 47, 62, 73, 84,
  153; `httpcore/endpoints_test.go` lines 405, 422, 466, 485, 531, 560."*
- ADR Negative: *"**29 pin sites across 9 files in 5 packages** change — derived by compile
  ablation in a detached worktree, not by grep. ⚠ Only **11** of the 29 break the build."*
- plan Task 5 Files: *"`transport/http/httpcore/endpoints_test.go` (lines 405, 422, 466, 485,
  531, 560) — the **11** compile-breaking pins"*.
- plan Self-review: *"§2.6 the 29 pins | 5 (11 compile) · 7/8/9 (17) · 10 (1)"*.

**The net the author used** — stated verbatim in §2.6: *"the three fields and the
`httpcore.Actor` type were deleted … the three `endpoints.go` sites stubbed to
`authz.Actor{}`, and every package including test packages compiled"*. That ablation models
**Decision 1** (remove the DTO fields). It does **not** model **Decision 2** — *"The three task
endpoints take the resolver as a parameter"* — which the same bundle also ships.

**The net that finds more:** the same ablation, plus the sixth parameter. Both run here.

**Observed — ablation A, the author's (fields+type only):**
```
$ go test -count=1 -gcflags=-e -run '^$' ./...   # EXIT=1
11 distinct lines / 17 errors / 2 files / 1 package   ← reproduces §2.6 EXACTLY
```
✅ The author's arithmetic is perfect. The number is faithful to the experiment run.

**Observed — ablation B, the change the bundle actually makes (fields+type removed AND
`actor RequestActorFunc` added as the 6th parameter to all three endpoints):**
```
$ go test -count=1 -gcflags=-e -run '^$' ./...   # EXIT=1
... all 17 errors from ablation A, PLUS:
transport/http/httpcore/endpoints_test.go:436:76: not enough arguments in call to httpcore.ClaimTask
transport/http/httpcore/endpoints_test.go:499:79: not enough arguments in call to httpcore.CompleteTask
transport/http/httpcore/endpoints_test.go:575:79: not enough arguments in call to httpcore.ReassignTask
... plus the nine groups.go production sites (counted separately by the bundle, fairly).
```

**Correct value:**

| | claimed | re-derived (full change) |
|---|---|---|
| compile-breaking distinct lines | **11** (17 errors) | **14** (20 errors) |
| total pins | **29** | **32** |
| files / packages | 9 / 5 | 9 / 5 (unchanged — same two httpcore files) |
| plan self-review split | 11 + 17 + 1 = 29 | **14** + 17 + 1 = **32** |

The three missed lines are `httpcore.ClaimTask(t.Context(), svc, token, tc.in, nil)` at
`endpoints_test.go:436`, `CompleteTask` at `:499`, `ReassignTask` at `:575` — i.e. **the call
sites of the three functions whose signature this bundle changes**. They are the single most
load-bearing test lines in the file, and the word attached to the list that omits them is
"**exhaustive**".

**Why the ablation structurally could not see them:** an ablation shows you the blast radius
of *the mutation you applied*. Applying half a change measures half a radius. The author
stubbed `endpoints.go`'s bodies but left its **signatures** intact, so every caller still
compiled. This is the counting-lens failure mode in its purest form — not arithmetic, but
**scope**: a number true of *field removal* asserted as the number for *this delivery*.

**It is self-correcting at build time but not at review time.** An implementer following plan
Task 5's file list edits the six named `endpoints_test.go` lines, gets a still-red build, and
fixes 436/499/575 on the spot. So the code ships fine. What does **not** self-correct is the
ADR, which publishes **"29 pin sites"** as the documented magnitude of a **BREAKING** change
for library consumers — and CLAUDE.md's Delivery Gate item 2 requires the documents to
describe what shipped.

**Concrete fix:**
1. spec §2.6: re-run the ablation **with the signature change applied** and restate the table
   as **14 (20 errors) / 18 / 32**. Add the three lines to the "exhaustive" enumeration.
2. Add one sentence to §2.6 recording the methodological limit, since the bundle's own §6.5
   asks the counting lens *"what does a compile ablation structurally fail to see?"* — the
   answer it did not anticipate is **the half of its own change it did not apply**.
3. ADR Negative: `29` → `32`, `11` → `14`.
4. plan Task 5 Files: add `endpoints_test.go` 436, 499, 575; `11` → `14`.
5. plan Self-review table: `5 (11 compile)` → `5 (14 compile)`; `29` → `32`.

## F5 — ⛔⛔ CRITICAL — the "29" the bundle claims to have RE-DERIVED is a DIFFERENT SET of 29 from the "29" it inherited. The totals match by COINCIDENCE; five members disagree
**Bundle text attacked:** spec §2.6 — *"⇒ per package: **httpcore 11**, gin 7, fiber 5, stdlib
5, parity 1. **The `29 / 9 / 5` filed in ADR-0185 is exact at today's base — re-derived, not
restated.**"* And spec §1: *"⚠ Every factual claim below was executed at `9789ebcc`. Where the
prior bundle's number survived re-derivation that is stated as a re-derivation, not as an
inheritance."*

**The net the author used:** compile ablation (fields+type deleted).
**The net ADR-0185 used** — recorded verbatim in `docs/specs/2026-08-21-adr-0185-0186-premise-evidence.md:183`:
`grep -rnE 'httpcore\.Actor\{|"actor"|"by"' transport/ --include='*_test.go'`.

**The net that finds more: run BOTH and diff the member sets.** Nobody did.

**Observed:**
```
$ grep -rnE 'httpcore\.Actor\{|"actor"|"by"' transport/ --include='*_test.go' | wc -l
29
```
Per file the inherited net gives `dto_test.go` **5**, `endpoints_test.go` **6** ⇒ httpcore 11;
gin 7, fiber 5, stdlib 5, parity 1. **The totals are identical to the ablation's.** The
members are not:

| `httpcore/dto_test.go` line | inherited GREP net | ADR-0189 ABLATION net | what is actually on that line |
|---|---|---|---|
| 47 | — | ✅ | `var got httpcore.Actor` |
| **57** | ✅ | — | ``const in = `{"actor":{"id":"u1","roles":["reviewer"]}}` `` |
| 62 | — | ✅ | `if got.Actor.ID != "u1" …` |
| **68** | ✅ | — | ``const in = `{"actor":{"id":"u1","roles":[]},"output":…}` `` |
| 73 | — | ✅ | `if got.Actor.ID != "u1" …` |
| **79** | ✅ | — | ``const in = `{"from":"alice","to":"bob","by":{…}}` `` |
| 84 | — | ✅ | `if … got.By.ID != "mgr"` |
| **151** | ✅ | — | ``body: `{"actor":{"id":"u-jane"},"outcome":…}` `` |
| 153 | — | ✅ | `require.Equal(t, "u-jane", in.Actor.ID)` |
| **161** | ✅ | — | ``body: `{"actor":{"id":"u-jane"},"output":{}}` `` |

The two nets agree on 24 of 29 members (the six `endpoints_test.go` struct literals and all 18
runtime pins) and are **disjoint on the other five**. One net sees the JSON **body strings**;
the other sees the Go **assertions on the decoded struct**. Each file contributes 5, so both
totals land on 11 — and on 29.

**Correct value:** the union — the set of distinct lines that must actually be edited — is

| file | lines | count |
|---|---|---|
| `httpcore/dto_test.go` | 47, 57, 62, 68, 73, 79, 84, 151, 153, 161 | **10** |
| `httpcore/endpoints_test.go` | 405, 422, **436**, 466, 485, **499**, 531, 560, **575** | **9** (incl. F4's three) |
| gin / fiber / stdlib / parity | unchanged | 18 |
| **TOTAL** | | **37 distinct lines**, 9 files, 5 packages |

**Why this is the worst kind of counting error.** The bundle's stated defence against inherited
numbers — *"re-derived, not restated"* — was satisfied by a **matching total**, not by a
matching set. A coincidence of sums was read as corroboration, and the corroboration was then
promoted into the ADR as a **BREAKING**-change magnitude. This is precisely the CLAUDE.md
Premise-Discipline hazard (*"Re-verify claims you inherit before restating them. Restating
strips the hedge"*) surviving in a document that quotes the rule at itself.

**Concrete fix:**
1. spec §2.6: state **both** nets, their member sets, and the union of **37**. Replace *"the
   `29 / 9 / 5` filed in ADR-0185 is exact at today's base"* with *"the inherited 29 and this
   ablation's 29 have the same total and differ on five `dto_test.go` members; the union is 37"*.
2. ADR Negative: **29 → 37**, **11 → 14**.
3. Add the rule this cost: *a re-derivation is confirmed by the MEMBER SET, never by the total.*

## F6 — MAJOR — the plan's dto_test.go instruction, driven by the wrong line set, leaves THREE tests that cannot fail and mis-describes a fourth
**Bundle text attacked:** plan Task 5 Step 1 — *"In `dto_test.go`, **delete the
`httpcore.Actor` decode assertions at lines 47/62/73/84/153** and replace them with one case
pinning the new contract."*

**Observed** (`sed -n '40,90p' transport/http/httpcore/dto_test.go`):
```go
func TestActorJSONTags(t *testing.T) {           // line 45
	const in = `{"id":"u1","roles":["admin","user"]}`
	var got httpcore.Actor                        // line 47  ← the cited "assertion"
	...
}
func TestClaimInputJSONTags(t *testing.T) {      // line 56
	const in = `{"actor":{"id":"u1","roles":["reviewer"]}}`   // 57  ← NOT in the plan's list
	var got httpcore.ClaimInput
	if err := json.Unmarshal([]byte(in), &got); err != nil { t.Fatal(err) }
	if got.Actor.ID != "u1" || len(got.Actor.Roles) != 1 {    // 62  ← in the plan's list
		t.Fatalf("wire tags mismatch: %+v", got)
	}
}
```
Two concrete consequences of following the instruction literally:

1. **Line 47 is not an assertion, it is a declaration** — and its enclosing function
   `TestActorJSONTags` (lines 45–54) exists *solely* to test the `httpcore.Actor` type this
   bundle **deletes**. The whole function must go; deleting "line 47" is not a coherent edit.
2. **Deleting only 62 / 73 / 84 / 153 leaves 57 / 68 / 79 / 151 / 161 standing.** An unused
   `const` is legal Go, so the tree still compiles and `TestClaimInputJSONTags`,
   `TestCompleteInputJSONTags` and `TestReassignInputJSONTags` survive as functions that
   unmarshal a body and **assert nothing** — three more tests that cannot fail, in the delivery
   whose own plan (Global Constraints) makes mutation-proof a standing requirement. This repo
   has shipped that exact defect repeatedly; the plan's line set walks straight into it.

**Concrete fix:** rewrite the instruction as *"delete `TestActorJSONTags`,
`TestClaimInputJSONTags`, `TestCompleteInputJSONTags` and `TestReassignInputJSONTags` **whole**
(lines 45–89), and drop the `"actor"` key from the two `body:` fixtures at 151 and 161 while
keeping their outcome/note/output assertions"* — naming **functions**, not line numbers. Add a
verification step: `grep -n '"actor"\|"by"' transport/http/httpcore/dto_test.go` must return
only the one new stale-body case.

## F7 — MAJOR — "the **four** `runtime/task` verbs as reached over HTTP" is a wrong count in the RESIDUAL section. Only **three** are HTTP-reachable
**Bundle text attacked:** ADR Negative — *"This covers the **four** `runtime/task` verbs as
reached over HTTP."* Same sentence in spec §4.1: *"This design covers the four `runtime/task`
verbs as reached over HTTP."*

**The net the author used:** apparently counting `TaskService`'s exported verbs — which really
is four.
**The net that finds more:** ask which of them the HTTP transport actually routes.

**Observed:**
```
$ grep -n "^func (s \*TaskService)" runtime/task/service.go
194: Claim   219: Reassign   250: Complete   294: RefreshCandidates        ← four verbs
$ grep -rn "RefreshTaskCandidates\|RefreshCandidates" transport/ --include='*.go' | grep -v _test.go
(no matches)                                                              ← exit 1
$ grep -n '"/tasks' transport/http/stdlib/groups.go
134: "/tasks/{token}/claim"   148: "/tasks/{token}/complete"   162: "/tasks/{token}/reassign"
```
`service.RefreshTaskCandidatesRequest` (`service/request.go:91-97`) carries
`By authz.Actor` — *"the principal requesting the refresh… the same policy
`ReassignTaskRequest.By` is held to"* — so it is an actor-bearing, authorization-checked verb.
It has **no HTTP route at all**, in any of the three adapters.

**Correct value:** **three** of the four. The fourth is unreachable over HTTP.

**Why it matters more than the arithmetic:** §4.1 is the sentence that *bounds the fix*.
Overstating the covered set makes the residual look smaller than it is, and it plants a
forward-looking trap: the next person who adds a `POST /tasks/{token}/refresh-candidates`
route will read "the four verbs are covered", find the seam already in place for the other
three, and have nothing in this bundle stopping them from building `By` out of a request body
exactly as `endpoints.go:150` does today. Nothing in the bundle adds a guard against a **fourth**
body-derived actor site.

**Concrete fix:** change both sentences to *"the **three** `runtime/task` verbs the HTTP
transport routes (claim, complete, reassign). The fourth, `RefreshCandidates`, has **no HTTP
route**; if one is ever added it must take `cfg.RequestActor` — it is not covered by anything
here."* Optionally add the machine check the bundle otherwise lacks: a test asserting
`grep`-equivalent that no `authz.Actor` is constructed from a DTO field in `transport/`.

## F8 — MAJOR — "a stale body is IGNORED, not rejected — **all three adapters**" is true of THIS REPO, not of the deployed system. A gin consumer can flip it to 400
**Bundle text attacked:** spec §2.3 heading — *"A stale `"actor"` body is IGNORED, not rejected
— **all three adapters**"*; and its body — *"`gin` uses `gc.ShouldBindJSON`, tolerant unless the
global `EnableDecoderDisallowUnknownFields` is set, **which nothing sets**."* ADR Decision 1
restates it with the hedge stripped: *"**A body still carrying `"actor"` or `"by"` is IGNORED,
not rejected.** Executed for all three adapters"*.

**The net the author used:** `grep -rn "DisallowUnknownFields|EnableDecoderDisallowUnknownFields" transport/ internal/`
→ no matches. That is correct **for this repo** — I re-ran it, exit 1, zero hits.
**The net that finds more:** ask *who else can set it*. This is a **library**; the consumer owns
`main`, and the switch is a mutable package-level global in a dependency.

**Observed** (gin **v1.12.0**, the version `go.mod:10` actually pins — I checked, not the stale
v1.10.0 in the module cache):
```go
// binding/json.go:25
var EnableDecoderDisallowUnknownFields = false      // ← exported, mutable, package-global
// binding/json.go:49
if EnableDecoderDisallowUnknownFields { decoder.DisallowUnknownFields() }
```
A consumer who has hardened their app with `binding.EnableDecoderDisallowUnknownFields = true`
— an entirely reasonable thing to do, and invisible to this library — gets **400** on every
in-flight request still carrying `"actor"`, for the whole rollout window. That is exactly the
outcome Decision 1's rationale says it is avoiding (*"would break consumers' rollout windows"*).

**Correct value:** ignored-not-rejected holds for **stdlib** and **fiber** unconditionally, and
for **gin** only while the consumer leaves `binding.EnableDecoderDisallowUnknownFields` at its
default. "All three adapters" is a repo-scoped fact restated as a contract.

**Concrete fix:** hedge the ADR sentence — *"stdlib and fiber ignore it unconditionally; gin
ignores it unless the consumer has set `binding.EnableDecoderDisallowUnknownFields`, a global
this library does not control. A gin consumer who has hardened that switch should expect 400s
during the rollout window and should drop the `actor`/`by` keys client-side first."* Add it to
`SECURITY.md` alongside the migration note in Task 12.

## F9 — MINOR — §2.4's option-alias enumeration mis-states fiber's set, in the very paragraph warning not to infer it from one file
**Bundle text attacked:** spec §2.4 — *"⚠ The adapter option-alias sets are **already uneven**:
`stdlib` and `gin` export `WithBasePath`/`WithMaxBodyBytes`/`WithBodyReadTimeout` (**gin adds
`WithMiddleware`**), while **`fiber` has no `WithBodyReadTimeout`**. … **Do not infer the new
alias set from any one adapter's file.**"* Restated in plan Task 6.

**The net that finds more:** enumerate all three, don't describe two and subtract.
```
$ for p in stdlib gin fiber; do grep -n "^func With" transport/http/$p/options.go; done
stdlib: WithBasePath:13  WithMaxBodyBytes:28  WithBodyReadTimeout:47                    (3)
gin:    WithBasePath:28  WithMaxBodyBytes:41  WithBodyReadTimeout:59  WithMiddleware:69 (4)
fiber:  WithBasePath:23  WithMaxBodyBytes:41  WithMiddleware:49                          (3)
```
**Correct value:** `WithMiddleware` is exported by **gin AND fiber**, not by gin alone —
`transport/http/fiber/options.go:49`. The true asymmetries are two, not one: **fiber lacks
`WithBodyReadTimeout`**, and **stdlib lacks `WithMiddleware`**. The bundle's sentence implies
fiber's set is `{BasePath, MaxBodyBytes}`; it is `{BasePath, MaxBodyBytes, Middleware}`.

Harmless to Task 6 (which adds `WithRequestActor` to all three regardless), but it is
enumeration rot inside the sentence whose entire job is to warn about enumeration rot.

**Concrete fix:** replace the prose with the three-row table above.

## F10 — MINOR — the inherited ADR-0185 Criticals breakdown does not sum, and the bundle restates half of it
**Bundle text attacked:** spec §1 — *"**Nineteen of its 22 raw Criticals were D3's** …; D1 had
two."* ADR Context — *"**Nineteen of the last round's 22 raw Criticals were D3's**; D1 had two."*

**Observed**, `docs/plans/sweep-evidence/audit-0185core-adjudication.md`:
```
:182  **Nineteen of the 22 raw Criticals are D3's.** D1 and D2 have two each.
:156  **21 of 22 raw Criticals are accepted**; the 22nd is the counting-F8 …
:11   **58 findings raw across four lenses; 22 raw Criticals.**
```
19 (D3) + 2 (D1) + 2 (D2) = **23**, against a stated total of **22**. The source's own
breakdown over-counts by one, and the bundle inherits and restates it without noticing —
the CLAUDE.md hazard *"Re-verify claims you inherit before restating them; restating strips
the hedge"* landing on a number used to justify shipping D1 alone.

✅ The **round totals** are right: adjudication `:15-16` — *"58 raw here against 58 and 38 in
the two prior rounds"* — matches the ADR's *"(58 / 38 / 58 findings)"* in that order.

The argument survives (D1 carried 2 of ~22 Criticals either way), but the number should be
stated as it can be defended: *"D1 carried two of the last round's 22 raw Criticals; the
overwhelming majority were D3's."*

## F11 — ⛔ CRITICAL — "`actor.Attributes.*` predicates fail closed **vacuously**" is FALSE for the deny-list class, which is the exact class §4.2 is about. Measured: they fail **OPEN** today
**Bundle text attacked:**
- spec §4.2 — *"Today all three endpoints drop `Actor.Attributes`, so `actor.Attributes.*`
  predicates **fail closed vacuously**. Once the actor arrives whole (§3.2) they go live, with
  nothing bounding them until 103 ships."*
- ADR Context — *"Attribute-based predicates over *actor* attributes cannot be satisfied over
  HTTP at all — **they fail closed vacuously**."*
- ADR Negative — *"Today all three endpoints drop `Actor.Attributes`, so `actor.Attributes.*`
  predicates **fail closed vacuously**; once the actor arrives whole they go live with nothing
  bounding them."*

**The net the author used:** reasoned from backlog 103's *sibling* measurement — the ADR-0185
bundle's `vars = map[string]any{}` probe over **`vars.*`** predicates — and generalised it to
**`actor.Attributes.*`**. §4.2 says so in as many words: *"executed in the ADR-0185 bundle with
`vars = map[string]any{}`"*. That is an **inherited** measurement in a **different env root**,
restated about a class it was not taken over. It was never run for `actor`.

**The net that finds more:** run it for `actor.Attributes`, in both states.
```go
// throwaway authz/zzprobe_test.go, deleted after the run
err := authz.RoleAuthorizer{}.Authorize(t.Context(),
    authz.AuthzSpec{Attribute: pred}, actor, map[string]any{})
```
**Observed** (`go test -count=1 -v -run TestZZProbe... ./authz/...`, EXIT=0):
```
PROBE actor.Attributes.dept == "finance"     | attrs DROPPED (today over HTTP) => DENY
PROBE actor.Attributes.dept == "finance"     | attrs WHOLE (after ADR-0189)   => ALLOW
PROBE actor.Attributes.status != "blocked"   | attrs DROPPED (today over HTTP) => ALLOW   ← FAILS OPEN
PROBE actor.Attributes.status != "blocked"   | attrs WHOLE (after ADR-0189)   => DENY
PROBE actor.Attributes.blocked != true       | attrs DROPPED (today over HTTP) => ALLOW   ← FAILS OPEN
PROBE actor.Attributes.blocked != true       | attrs WHOLE (after ADR-0189)   => ALLOW
```

**Correct value.** The universal is false, and it is false in the unsafe direction:

| predicate shape | attrs dropped (TODAY, over HTTP) | attrs whole (AFTER 0189) | what 0189 does |
|---|---|---|---|
| allow-list `== "finance"` | DENY | ALLOW | opens (intended: the feature) |
| deny-list `!= "blocked"` | **ALLOW — fails OPEN** | **DENY** | **TIGHTENS** |
| deny-list `!= true` on an absent key | **ALLOW — fails OPEN** | ALLOW | unchanged — this alone is backlog 103 |

**Two things the bundle gets backwards:**
1. **"Fail closed vacuously" is true only of the allow-list shape.** For deny-list predicates
   the current transport already fails **OPEN** — the very fail-open posture ADR-0189 exists to
   end, present *today*, undocumented, in the ADR that catalogues this area's exposures.
2. **§4.2's direction of travel is wrong for one of its own two example predicates.** It cites
   `vars.status != "blocked"` and `vars.blocked != true` as the 103 shapes and says shipping
   0189 makes them *"go live with nothing bounding them"*. Measured on `actor`, `status !=
   "blocked"` goes **ALLOW → DENY**: shipping 0189 **fixes** that one. Only the third shape
   (`!= true` against an absent key) is genuinely unbounded in both states, and it is unbounded
   **now** as well as after.

**Why this is Critical, not Minor.** §4.2 and the ADR's Negative section are the record of what
shipping this costs, written specifically to be honest about a deferred defect. The sentence
that does that work asserts today's state is *safe-but-vacuous* when for half the predicate
class it is *already fail-open*. It is also a textbook instance of the repo's own standing
lesson — a measurement taken in the `vars` root, inherited, and restated about the `actor` root
without re-derivation, with the hedge stripped on the way.

**Concrete fix:**
1. Replace the quantifier in all three places with the measured table above.
2. Restate §4.2's residual accurately: *"Today, deny-list predicates over `actor.Attributes`
   already ALLOW, because the attribute is always absent — the transport is fail-open for that
   class right now. Shipping this record makes `!=`-against-a-present-value **deny** correctly
   (a tightening) and leaves `!=`-against-an-absent-key allowing, which is backlog 103 and is
   unchanged by this record."*
3. Add the measurement to spec §2 as an executed premise — it is currently the only load-bearing
   §4 claim taken from a sibling context rather than run.

## F12 — MAJOR — three of spec §5's eleven test rows are not delivered by the task that claims them; row 8, the bundle's highest-value assertion, observes the WRONG OBJECT
**Bundle text attacked:** plan Self-review — *"| §5 test table rows 1–11 | 1, 2, 4, 5, 7, 8, 9,
10 |"* and *"**Gaps found and closed during review:** §5's row 8 (Attributes reach
`service.ClaimTaskRequest`) had no task — **folded into Task 4 Step 1's final case and Task 5's
endpoint tests**."*

**The net the author used:** a row→task mapping table, checked for *existence* of a task.
**The net that finds more:** for each row, open the task's prescribed test and ask *what object
does the assertion actually observe, and how many assertions does the row call for?*

**Observed, row by row:**

| §5 row | what the row demands | what the cited task prescribes | verdict |
|---|---|---|---|
| 1, 2 | context round-trip, clone, bare-ctx `ok==false` | Task 1, both tests written out | ✅ |
| 3, 7 | `ClaimTask` no-actor / nil-resolver ⇒ 401 | Task 5 case *"unauthenticated → 401"*, Task 4 case *"nil resolver fails CLOSED"* | ✅ |
| 4, 5, 6 | 503 + empty body, empty-ID 401, `ErrNotAuthorized`⇒503 | Tasks 2 and 4, all written out | ✅ |
| **8** | *"actor `Attributes` … reach **`service.ClaimTaskRequest.Actor.Attributes`**"* | Task 4's final case asserts `got.Attributes["dept"]` on **`resolveRequestActor`'s return value**. Task 5's endpoint-test sample asserts only `status`/`err`. | ❌ **wrong object** |
| **9** | *"**per adapter × 3 verbs**"* — 3 × 3 = **9** assertions | Task 7 one claim-verb test; Task 8 one; Task 9 one ⇒ **3** | ❌ **3 of 9** |
| **10** | *"per adapter: middleware → context → **200**"* | Task 7's seam test asserts **403**; Task 8's snippet uses a `viewer`; Task 9's uses a `viewer`; each task's second test asserts **401** | ❌ **no 200 anywhere per-adapter** |
| 11 | fiber `c.Locals` ⇒ 401 | Task 9 Step 3, written out | ✅ |

**Why row 8 is the serious one.** The seam it names is precisely
`resolveRequestActor` → `svc.ClaimTask(service.ClaimTaskRequest{TaskID: token, Actor: a})`.
The failure mode is an implementer reconstructing the actor field-by-field on the way through —
which is **literally what all three sites do today** (`authz.Actor{ID: in.Actor.ID, Roles:
in.Actor.Roles}`, `endpoints.go:119,132,150`, dropping `Attributes`). Task 4's assertion sits
*upstream* of that seam and would stay green through exactly that regression. The bundle's own
self-review declares this gap "closed"; it is not. This is the repo's standing lesson —
**a CITED test is not a COVERING test** — recurring.

**Why row 10 compounds it.** With no per-adapter 200, every adapter seam assertion in the
bundle is a **refusal** (401) or a **denial** (403). There is no positive control at the adapter
layer proving a resolved manager actually gets through. Combined with row 8, nothing in the
plan pins that the **right actor, whole**, reaches the service through an adapter.

**Concrete fix:**
1. Task 4 or Task 5: add a case using a **service spy** that captures the
   `service.ClaimTaskRequest` and asserts `req.Actor.Attributes["dept"] == "finance"` **and**
   `req.Actor.ID`/`Roles`. State its falsifier: *"fails if `endpoints.go` rebuilds the actor as
   `authz.Actor{ID: a.ID, Roles: a.Roles}` — today's shape."*
2. Tasks 7/8/9: either deliver row 9's ×3 verbs (9 assertions) or **amend §5 row 9** to say one
   verb per adapter and give the reason. Do not leave the spec demanding 9 and the plan
   delivering 3.
3. Tasks 7/8/9: add the missing **200** case (manager via middleware) alongside the 403/401.

## F13 — MINOR — "Between Task 5 and Task 9 the adapter test suites are RED" leaves `parity` out of its own range
**Bundle text attacked:** plan, Task-order section — *"⚠ **Between Task 5 and Task 9 the adapter
test suites are RED, by design.** **18** JSON-body pins in
`gin`/`fiber`/`stdlib`/**`parity`** still send `"actor"`/`"by"` and will now get 401."*

**Observed:** the 18 decompose as gin 7 + fiber 5 + stdlib 5 + **parity 1**. Tasks 7/8/9 fix
gin/fiber/stdlib (17). `parity` is **Task 10**. So the red window closes at Task **10**, not
Task 9, and a controller reading this line will treat a red `parity` after Task 9 as a
regression rather than the planned red.

✅ The rest of the plan's pin arithmetic is internally consistent: Task 5 (11) + Task 7 (5) +
Task 8 (7) + Task 9 (5) + Task 10 (1) = **29**, matching the Self-review's
*"5 (11 compile) · 7/8/9 (17) · 10 (1)"*. **Every pin in spec §2.6 is assigned to exactly one
task — no orphans, no double-assignments.** The plan is faithful to a count that is itself
wrong (F4, F5); the fixes for those must be pushed through all five per-task numbers.

**Concrete fix:** *"Between Task 5 and Task **10** …"*.

---

# Verdict

**3 CRITICAL · 5 MAJOR · 4 MINOR** (plus 2 informational). The arithmetic in this bundle is,
as in every prior round, **flawless** — I reproduced the author's ablation and got their exact
11/17/2/1. Every failure is **net, anchor, or scope**, and the anchors are the best I have seen
in this repo: **every single `file.go:NNN` in all three documents resolves and says what the
bundle says it says.** Not one rotted.

The bundle should **not** proceed to implementation until F4, F5 and F11 are folded in.

### The three Criticals, in one line each
- **F4** — the compile ablation applied **Decision 1 without Decision 2**: it deleted the DTO
  fields but never added the endpoint parameter, so it measured half the blast radius.
  "Exhaustive: 11" is really 14.
- **F5** — the "29" claimed as **re-derived** is a **different set of 29** from the inherited
  one; the two nets are disjoint on five `dto_test.go` members and agree on the total by
  coincidence. Union = **37**. A matching total was read as corroboration.
- **F11** — *"`actor.Attributes.*` predicates fail closed vacuously"* is **false in the
  fail-open direction** for the deny-list class, measured. The claim was inherited from a
  `vars`-root probe and restated about the `actor` root without re-derivation.

### Every enumeration in the bundle

| # | claimed | re-derived | verdict |
|---|---|---|---|
| 1 | 3 `authz.Actor` constructions in `transport/` non-test, `endpoints.go:119,132,150` | 3, same lines, even on a `{`-less net | ✅ |
| 2 | `dto.go` declares exactly 3 Actor-bearing fields | 3 — `:44`, `:50`, `:66` | ✅ |
| 3 | `CustomizeConfig` declares **8** fields, enumerated | 8, same names, same order | ✅ |
| 4 | ADR-0185's inherited "six fields" was wrong | correct — it is 8 | ✅ |
| 5 | `WithActorResolver` exported **3×** at `service/options.go:99`, `runtime/task/service.go:113`, `processtest/harness.go:104` | 3, all three anchors exact | ✅ |
| 6 | no existing `WithRequestActor`/`RequestActorFunc` to collide with | 0 Go hits | ✅ |
| 7 | **9** adapter call sites, 9 named line numbers | 9, all 9 anchors exact | ✅ |
| 8 | all 9 pass `cfg.InstanceMapper` last | all 9 | ✅ |
| 9 | **11** compile-breaking lines (17 errors), "exhaustive" | **14** (20 errors) once the 6th parameter is modelled | ❌ **F4** |
| 10 | **18** runtime-only pins, per package gin 7 / fiber 5 / stdlib 5 / parity 1 | 18, every line number exact | ✅ |
| 11 | **29** total / 9 files / 5 packages | **37** distinct lines once both nets are unioned | ❌ **F4+F5** |
| 12 | "the 29 filed in ADR-0185 is exact at today's base — re-derived" | inherited net also gives 29, but **5 members differ** | ❌ **F5** |
| 13 | 1 deliberate exclusion (`validate_test.go`) | correct — survives field removal | ✅ |
| 14 | blast radius = 5 packages | **6** — `service/instance_test.go:1090,1128` cite `httpcore.Actor` in comments | ❌ **F1** |
| 15 | both vacuous 403 pins in `stdlib`, `errors_test.go:158` + `:190`; gin has **none** | exact — gin has **zero** `StatusForbidden`/`403` in any test file | ✅ |
| 16 | `gin_coverage_test.go:244` asserts 404 not 403 | correct (assertion at `:246`) | ✅ |
| 17 | `TestTaskRoutes_Complete_ServiceError` / `_Reassign_ServiceError` exist (the `-run` filters) | both exist, `errors_test.go:143` and `:164` | ✅ |
| 18 | all httpcore test files are `httpcore_test`; the new one is the only in-package file | all **11** existing files are `package httpcore_test` | ✅ |
| 19 | the 404 arm is "currently first" in `ClassifyError` | first arm is `kernel.ErrInstanceNotFound` ⇒ 404 | ✅ |
| 20 | `errors.go` carries the standing co-match invariant | `errors.go:51-52`, quoted accurately | ✅ |
| 21 | no `DisallowUnknownFields` in `transport/` or `internal/` | 0 hits (it exists elsewhere — `runtime/kernel`, `definition/model` — but the claim is scoped and correct) | ✅ |
| 22 | "a stale body is IGNORED — all three adapters" | true for stdlib/fiber; **gin depends on a consumer-settable global** | ❌ **F8** |
| 23 | stdlib+gin export BasePath/MaxBody/BodyReadTimeout, "gin adds `WithMiddleware`", fiber lacks BodyReadTimeout | fiber **also** exports `WithMiddleware` (`options.go:49`) | ❌ **F9** |
| 24 | the 3 wiring mains never claim a task; `stdlib.Mount` at `:264`/`:278`/`:262` | exact, all three anchors; only comments/log strings match | ✅ |
| 25 | `production_wiring` already passes `WithMeterProvider` | exact, `main.go:264` | ✅ |
| 26 | ADR-0147 `:44` "guarantees nothing beyond `id`"; `:66` Caveat is about roles/attributes not `id` | both exact, and the Caveat reading is correct | ✅ |
| 27 | `runtime/processdriver.go:548` "bypasses authorization entirely" | exact | ✅ |
| 28 | `stdlib/body.go:143` plain `json.NewDecoder` | exact | ✅ |
| 29 | "the **four** `runtime/task` verbs as reached over HTTP" | 4 verbs exist; only **3** are HTTP-routed | ❌ **F7** |
| 30 | ADR-0185 failed 3×, "58 / 38 / 58" | exact per the adjudication | ✅ |
| 31 | "19 of 22 raw Criticals were D3's; D1 had two" | inherited breakdown sums to **23 ≠ 22** | ⚠ **F10** |
| 32 | `actor.Attributes.*` predicates "fail closed vacuously" today | **ALLOW** (fail open) for the deny-list class — measured | ❌ **F11** |
| 33 | per-task pin sums: 11+5+7+5+1 = 29; every pin in exactly one task | sums correctly; no orphans, no doubles | ✅ (faithful to a wrong total) |
| 34 | spec §5 has 11 test rows, all mapped to tasks | rows **8, 9, 10** not delivered as specified | ❌ **F12** |
| 35 | red window "between Task 5 and Task 9" covers gin/fiber/stdlib/parity | parity is **Task 10** | ⚠ **F13** |
| 36 | go 1.25.7, gin v1.12.0, fiber v3.4.0 | exact per `go.mod:3,10,14` | ✅ |
| 37 | `httpcore.Actor` "has no other referent once they go" | none **in compiled code**; two in comments (F1) | ⚠ **F1** |

### Note for the adjudicator
F4 and F5 are the same root cause seen from two sides: **a count is only re-derived when its
MEMBER SET is re-derived.** F4 is a net that modelled half the change; F5 is a net whose total
matched for an unrelated reason. Both were presented as machine-checked, and the machine really
did run — on the wrong thing. If one process rule comes out of this round, make it:
*paste the member list, not the total.*

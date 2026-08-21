# Audit round 7 (post-strip) — COUNTING lens, ADR-0186 (request body caps)

- Worktree detached at `27ff5841`. Step 0: all five bundle files present. ✅
- Method: independent nets only. Every count below re-derived by AST walk, `grep` with a stated
  positive control, or reading the cited file. No count is inherited from the bundle.

---

## ✅ Re-derived and CORRECT (recorded so the next round does not pay for them again)

| bundle claim | my independent net | verdict |
|---|---|---|
| 39 decode sites / 36 propagating / 3 discarding, all in `groups.go` | `go/ast` walk over **every non-test `.go` file in the repo** matching `json.NewDecoder().Decode`, gin's six `*Bind*JSON*` methods, and `c.Bind().JSON` → **39 total, 13+13+13, 3 discarded** (`stdlib/groups.go:238`, `gin/groups.go:265`, `fiber/groups.go:255`), **zero hits outside the three `groups.go` files** | ✅ EXACT |
| `httpcore` has 0 decode sites; no fourth package reads a request body | same walk; plus `grep -E '\.Body\|BodyRaw\|BodyParser\|ParseForm\|FormValue\|io\.ReadAll\|MultipartForm' transport/` non-test → **13 hits, all `stdlib/groups.go` `req.Body`** | ✅ EXACT |
| `ClassifyError` = 6 ordered arms at `:28 :32 :34 :36-50 :51 :57` | read `errors.go` — case lines are exactly 28, 32, 34, 36 (return 50), 51, default 57 | ✅ ALL SIX ANCHORS EXACT |
| 26 routes = 9 non-admin + 15 admin + 2 health | `handle(` call sites in `stdlib/groups.go`: 5 instance + 1 message + 3 task = **9**; admin 212…445 = **15**; health 472,478 = **2**; total **26** | ✅ EXACT |
| `ResolveConfig` call sites = 15, exactly 5 per adapter | `grep -rn ResolveConfig`, minus the 2 comment lines and 1 definition in `seam.go` → fiber 5, stdlib 5, gin 5 | ✅ EXACT |
| 15 `Customize` methods, 6 `Mount`/`MountHealth`, all returning nothing | `grep -E '^func \(…\) Customize\('` → 15; `grep -E '^func Mount'` → `MountGroups` + 3×`Mount` + 3×`MountHealth`; every signature has no result list | ✅ EXACT |
| `Instrumentation`'s fields are unexported; adapters import no otel | `observability.go:23-28` — `tracer/counter/histogram/propagator` all lowercase; `grep 'go.opentelemetry.io' transport/http/{stdlib,gin,fiber}` **exits 1** (positive control: the same pattern hits 5 lines in `httpcore`) | ✅ CORRECT |
| `ResolveConfig` defaults live in the struct literal; post-loop guards cover only nil-able fields | `seam.go:40-44` literal (`Wrap`, `InstanceMapper`, `Logger`); guards `:50-58` on the same three | ✅ CORRECT — a plain `int64` default does survive an explicit `0` |
| `action/httpcall.go:186,191,194,214` | `:186` `WithMaxResponseSize`, `:191` `if max <= 0`, `:194` `io.ReadAll(io.LimitReader(r, max+1))`, `:214` `maxResponseSize: defaultMaxResponseSize` in `NewHTTPCall` | ✅ ALL FOUR EXACT |
| `writeErr` logs only at `status >= 500` | `stdlib/write.go:32` `if status >= 500` | ✅ CORRECT |
| all three `examples/` set `ReadHeaderTimeout` but not `ReadTimeout` | `grep -rn 'http\.Server\{\|ReadHeaderTimeout\|ReadTimeout' examples/` → 3 servers, 3 `ReadHeaderTimeout`, **0** `ReadTimeout`. Also **exactly three** of the 11 example dirs build an `http.Server` | ✅ EXACT |
| no `CustomizeConfig` composite literal bypasses `ResolveConfig` | `grep -rn 'CustomizeConfig\['` repo-wide → 5 hits, all parameter/receiver types, **zero composite literals** | ✅ CORRECT (a real risk, correctly absent) |
| `grep -rnE 'MaxBytesReader\|BodyLimit\|MaxBytesHandler\|LimitReader' transport/` exits 1 | reproduced with **bare `|`** under `-E`; exits 1. Positive control: the same pattern repo-wide returns exactly one hit, `action/httpcall.go:194` | ✅ CORRECT, and the command as written in plan §4 is the corrected (bare-pipe) form |

---

## FINDINGS

### C1 — `SECURITY.md` does not contain EITHER of the two residuals the ADR discharges onto it, and one of those sentences is tagged "Verified from source"

**Severity: CRITICAL**

**Bundle says (quote + location):**
- ADR `0186-…md:184-185`, §"The bound is on SIZE, not on TIME":
  > "**The consumer owns `ReadTimeout`.** `SECURITY.md` says so plainly, and notes that all three
  > `examples/` set `ReadHeaderTimeout` but **not** `ReadTimeout`."
- ADR `:215-218`, Negative, on `MountGroups`:
  > "⚠ Its own godoc already names the escape … **and `SECURITY.md` repeats it. Verified from source.**"
- spec `:144`, §4 coupling row 8, discharge ✅: "Recorded in ADR Negative and **repeated in `SECURITY.md`**."

**Bundle's net:** none given for either; both are asserted as present-tense facts about the file.

**My INDEPENDENT net:**
```
$ grep -n -E 'ReadTimeout|ReadHeaderTimeout|body|Body|MountGroups|Customize' SECURITY.md
EXIT=1                       # zero matches
$ grep -c 'the' SECURITY.md  # POSITIVE CONTROL on the same file
14
$ grep -rn 'ReadTimeout' --include='*.md' .
# every hit is inside this bundle or its own audit records. No other .md mentions it.
```
`SECURITY.md` is **44 lines**. Its only embedder section, "Scope notes for embedders" (`:32-44`),
has exactly **three** bullets: authorization of the admin routes, TLS, and untrusted definitions.

**Observed:** the file says nothing about `ReadTimeout`, `ReadHeaderTimeout`, request bodies,
`MountGroups`, `Customize`, base paths, or middleware.

**Verdict: CONFIRMED-DEFECT, two false claims, both load-bearing.** Both sentences discharge a
residual the delivery declines to fix onto a document that does not carry it — and one carries the
words *"Verified from source."* This is precisely the failure the spec's own §4 preamble says it
rebuilt the table to prevent (*"one row of the previous table discharged onto ADR text that did not
exist … Every ✅ below names the file and section that carries the resolution, so that failure is
checkable"*). It recurred, one document over. ⚠ The godoc half of the `MountGroups` claim **is**
true (`seam.go:106-107`); only the `SECURITY.md` half is false — the shape that survives review.

**Fix:** delete "SECURITY.md says so plainly" and "and `SECURITY.md` repeats it. Verified from
source." Rewrite as future tense pointing at plan phase 4, which is where both sentences actually
become true. Then re-grade spec §4 coupling row 8 from ✅ to ⚠ "resolved BY THIS DELIVERY'S phase 4",
because today nothing carries it.

---

### C2 — `WithMaxBodyBytes` is a PER-ROUTE-GROUP setting, and `Mount` reaches only 6 of 13 decode sites per adapter — so the delivery's stated migration lever leaves 21 of the 39 sites capped

**Severity: CRITICAL**

**Bundle says:** the opt-out lever is `WithMaxBodyBytes(0)`, stated as a whole-delivery escape in
**five** places (ADR `:164`, ADR `:214`, spec `:109`, spec `:195`, plan `:264`). ADR Positive `:195`:
*"The unbounded-body surface closes on all **39** sites."* The only mount-path caveat anywhere in the
bundle is `MountGroups` (spec §4 coupling 8, ADR Negative `:215`).

**Bundle's net:** `MountGroups` only.

**My INDEPENDENT net** — decode sites mapped to their owning `Customize` method by line range:
```
group           stdlib decode lines            gin                          fiber                      count
InstanceRoutes  42, 87                          33, 82                       33, 77                      2
MessageRoutes   114                             114                          104                         1
TaskRoutes      142, 157, 172                   151, 167, 183                136, 150, 164               3
AdminRoutes     238*,253,300,331,346,371,386    265*,281,326,354,368,391,405 255*,271,316,345,359,383,397 7
HealthRoutes    —                               —                            —                           0
                                                                             (* = the discarding site)
```
```
$ cat transport/http/stdlib/mount.go        # identical in gin/mount.go and fiber/mount.go
func Mount(mux, svc, opts...) {
    InstanceRoutes{Svc: svc}.Customize(mux, opts...)
    TaskRoutes{Svc: svc}.Customize(mux, opts...)
    MessageRoutes{Svc: svc}.Customize(mux, opts...)
}                                            // Admin and Health are NOT mounted
func MountHealth(mux, checks...) { HealthRoutes{Checks: checks}.Customize(mux) }   // no opts at all
```

**Observed:** `Mount(..., WithMaxBodyBytes(n))` configures **6 of 13** decode sites per adapter
(**18 of 39** repo-wide). The other **7 per adapter / 21 repo-wide — a majority — live in
`AdminRoutes`**, which the consumer mounts with a **separate** `Customize` call carrying **whatever
options they pass there**, and `MountHealth` accepts no options at all. **All three discarding sites
are in that admin group.**

**Verdict: CONFIRMED-DEFECT.** Three consequences the bundle states nowhere:
1. **The opt-out does not opt out.** A consumer disabling the cap the documented way —
   `Mount(mux, svc, WithMaxBodyBytes(0))` — still gets the **1 MiB default on 21 of 39 sites**,
   because `AdminRoutes.Customize(mux)` resolves its own config. The "migration lever" named five
   times is a partial lever, and the partition is 46 % / 54 % against it.
2. **A raised cap is equally partial**, and the asymmetry is invisible: the admin surface silently
   keeps 1 MiB while the core surface gets `n`.
3. **The bundle derived the `MountGroups` case and never derived the `Mount` case** — and `Mount` is
   the primary documented entrypoint, used by all three `examples/` and by the parity harness
   (`parity_test.go:123,138,174`). This is round 6's shape exactly: a boundary asserted (*"the cap
   applies to the delivery"*) and never derived (*"the cap applies per `ResolveConfig` invocation,
   of which there are five per adapter"*). ⚠ Note the bundle **already knows** `ResolveConfig` runs
   per group (spec §5 row 1) and never connected that to the semantics of the option.
4. ⚠ **Plan phase 2 test 6 cannot catch it.** `TestDisabledCapDoesNotInstallTheReader` is specified
   as *"`WithMaxBodyBytes(0)`, a 3 MiB body succeeds"* with no route named — an author will reach for
   `POST /instances`, which is in `InstanceRoutes` and passes. The narrow-fixture bias, again.

**Fix:** (a) state in the ADR Decision that `MaxBodyBytes` is resolved **per route group**, and that a
consumer must pass the option to every `Customize`/`Mount` call they make — enumerate the five groups;
(b) name the split (6 via `Mount`, 7 via `AdminRoutes`, 0 in Health) in ADR Negative beside the
`MountGroups` row; (c) re-word the lever from *"the lever is `WithMaxBodyBytes(0)`"* to *"pass
`WithMaxBodyBytes(0)` to **every** group you mount"*; (d) pin plan phase 2 test 6 to **an admin route
mounted without the option** as well as a core route mounted with it, so the asymmetry is asserted
rather than assumed away.

---

### C3 — "the parity suite structurally cannot see the admin routes" is FALSE; the suite already covers an admin route, in the file phase 3 edits

**Severity: CRITICAL**

**Bundle says**, in four places:
- ADR `:152-154`: "These are on an **admin** route that ADR-0095 keeps out of `Mount`, so the parity
  suite **structurally cannot see them** — the per-adapter test must name the route."
- spec `:143` §4 coupling 7 ✅: "The per-adapter test **names the admin route**; parity is explicitly
  not the net."
- plan `:74` (§0 item 5): "⚠ ADR-0095 keeps admin routes out of `Mount`, so **parity cannot be the net**."
- plan `:225` and `:252`: "⚠ Parity **cannot** be the net (ADR-0095)." / "⚠ Parity **cannot** cover the
  optional-body admin route — phase 2 test 4's job."

**Bundle's net:** ADR-0095 (admin ∉ `Mount`). No inspection of `parity_test.go` is cited anywhere.

**My INDEPENDENT net** — read `transport/http/parity/parity_test.go`:
```
$ grep -n -E 'Mount|Customize|AdminRoutes' transport/http/parity/parity_test.go
123: stdlib.Mount(mux, svc)
138: ginadapter.Mount(r, svc)
174: fiberadapter.Mount(app, svc)
190: // admin-route tests that mount a group by hand rather than through Mount.
663: stdlib.AdminRoutes{Svc: svcS}.Customize(mux)
670: ginadapter.AdminRoutes{Svc: svcG}.Customize(ginRouter)
677: fiberadapter.AdminRoutes{Svc: svcF}.Customize(fiberApp)
```
`TestParity_PostResolveCompensationStall_400` (`:645-700`) mounts `AdminRoutes` **by hand on all
three adapters** and asserts a cross-adapter **400** on
`POST /admin/instances/{id}/compensation/resolve-stall` — an admin route, in the parity suite, today.
Its own comment (`:658-660`) states the pattern: *"Admin routes are deliberately NOT part of Mount —
… Each adapter is therefore mounted by hand here rather than through hit{Stdlib,Gin,Fiber}."* And the
package doc at `:190` already names the category: *"admin-route tests that mount a group by hand
rather than through Mount."*

**Observed:** parity **can** see admin routes and **already does**; the existing case is 55 lines long
and sits ~110 lines below the `TestParity_ErrorEnvelopes` the plan's phase 3 explicitly re-checks.

**Verdict: CONFIRMED-DEFECT.** The bundle inferred a *test-reachability* boundary from a *mounting*
fact. ADR-0095 says admin routes are absent from `Mount`; it says nothing about what a test may mount.
Consequences:
1. Plan phase 3 **instructs the phase-3 agent not to write** the one case that would prove all three
   adapters agree on 413 at the three discarding sites — the cheapest and strongest net available,
   with a working in-repo template.
2. The coverage is instead scattered across three separately-briefed parallel phase-2 agents
   (plan `:190`, "THREE PARALLEL AGENTS"), which is where three independently-invented fixtures come
   from — the failure mode round 6 flagged for the instrumentation.
3. spec §4 coupling 7's ✅ discharges onto a premise that is false, joining C1 as the second ✅ in the
   rebuilt table resting on something not in the repo.

**Fix:** delete "structurally cannot see them" / "parity cannot be the net" from all four locations.
Replace with: *"admin routes are absent from `Mount`, so a parity case must mount `AdminRoutes` by
hand — the pattern `TestParity_PostResolveCompensationStall_400` (`parity_test.go:645`) already
uses."* Add a phase-3 parity case over `POST /admin/instances/{id}/incidents/{incidentID}/resolve`
asserting all three adapters return **413** for an over-cap body and **2xx** for an absent one,
modelled on that test. Keep the per-adapter phase-2 tests as the unit-level net.

---

### M1 — the strip's headline justification ("the four multi-lens findings were all ancillary, not the cap") is false in BOTH directions

**Severity: MAJOR**

**Bundle says:** ADR banner `:16-18`:
> "**The four findings that three or four lenses reached independently were all about ancillary
> mechanisms, not the cap.** They are **deleted**, not redesigned"

followed by a 4-row table: mount-time construction error · instrumentation · fiber mount WARN ·
`*int64` tri-state. Spec banner `:9-11` restates it verbatim: *"The four findings reached by 3–4
lenses each were all **ancillary mechanisms** — … **All four are deleted, not redesigned.**"*

**Bundle's net:** inherited from `audit4-0186-adjudication.md`'s "Required next steps" item 3.

**My INDEPENDENT net** — `audit4-0186-adjudication.md`'s OWN table, `:44-70`, headed
*"⭐⭐ Four findings reached by THREE OR MORE lenses independently"*:

| # | finding | lenses | is it the cap? |
|---|---|---|---|
| 1 | negative cap has no return channel | E1+C1+F6+I1 (4) | ancillary ✓ |
| 2 | histogram/counter have no home | E7+C5+F7+I3 (4) | ancillary ✓ |
| 3 | **"unmarshal from the resulting buffer" is unspecified; the readings disagree on under-cap trailing bytes** | E2+C4+F3 (3) | ⛔ **the cap's CENTRAL mechanism** |
| 4 | **"the read's own error distinguishes absent/EOF from oversize" is FALSE** | E4+F9+F2 (3) | ⛔ **the cap's CENTRAL mechanism** |

```
$ grep -n -iE 'fiber.*(WARN|warn)' docs/plans/sweep-evidence/audit4-0186-adjudication.md
86: … ⚠ And the WARN as specified          # one hit, NO lens attribution
$ grep -n -iE 'tri-state|\*int64' docs/plans/sweep-evidence/audit4-0186-adjudication.md
81,116,141,167                              # four hits, NONE a multi-lens attribution
```

**Observed:** the set "findings reached by 3–4 lenses" and the set "mechanisms deleted by the strip"
**overlap in two members, not four**. Two of the four multi-lens findings are about the cap itself
and were **redesigned, not deleted** — as the ADR's *very next paragraph* (`:26-31`, *"And the central
mechanism changed"*) concedes without reconciling. Conversely, the fiber WARN and the tri-state have
**no** multi-lens attribution anywhere in audit4; restating audit4's loose "four-lens" prose as
"three or four lenses reached [them] independently" **adds** a precision the source does not carry.

**Verdict: CONFIRMED-DEFECT.** This is the celebratory-recap shape the lineage keeps producing, and
the inherited-claim rule applies: audit4's own step-3 sentence contradicts audit4's own table, and the
bundle restated the sentence rather than the table. ⚠ Note the bundle got this **right** in spec §2,
where the per-row lens counts (`4 lenses`, `4 lenses`, and none for the WARN/tri-state rows) are all
accurate — only the banners are wrong. The strip may still be the correct owner decision; its stated
evidentiary basis is not.

**Fix:** replace both banner sentences with: *"Of the four findings 3+ lenses reached, **two** were
ancillary mechanisms (the construction error, the instrumentation) and are deleted; the other two
were about the cap's own mechanism (what 'unmarshal from the buffer' means; how oversize is
discriminated) and are **redesigned** — see the next paragraph. The fiber WARN and the tri-state were
single-lens findings, deleted on the owner's scope call, not on lens consensus."*

---

### M2 — "`ResolveConfig` runs 5× per adapter" is labelled **Executed** but is a static grep count, and no mount path ever invokes it five times

**Severity: MAJOR**

**Bundle says:** ADR `:165-166`: "**No mount-time WARN on fiber.** **Executed:** `ResolveConfig` runs
**5× per adapter**, so a WARN in `Customize` fires 3–4× per documented mount". spec §2 row `:96`
repeats it with the same **Executed:** label. spec §4 coupling 9 `:145`: "⭐ **A per-mount diagnostic
would fire 5× per adapter.** `ResolveConfig` has **15** call sites, 5 per adapter."

**Bundle's net:** spec §5 row 1 and evidence §8.3 both derive the 15/5 by **`grep`, non-test** — a
*call-site* count, explicitly not an execution.

**My INDEPENDENT net:** the 15 call sites are correct (verified above, 5 per adapter). But per mount
entrypoint:
```
Mount(...)                        → InstanceRoutes + TaskRoutes + MessageRoutes = 3 invocations
Mount(...) + AdminRoutes.Customize → 4
MountHealth(...)                  → 1 (and it forwards NO opts)
```
`Mount` is 3 lines of delegation in each of `stdlib/mount.go:17-21`, `gin/mount.go:15-19`,
`fiber/mount.go:15-19`. The bundle's own inherited measurement agrees: audit4 `:85-86` recorded
*"`fiber.Mount` → 3 `ResolveConfig` invocations; `Mount` + `AdminRoutes.Customize` → 4."*

**Observed:** **5 is never observed for any mount path.** The sentence is also internally
inconsistent — *"runs 5× per adapter, **so** a WARN … fires 3–4× per mount"* — 5 does not imply 3–4;
the 3–4 comes from a different (correct) derivation.

**Verdict: CONFIRMED-DEFECT (label + quantifier).** An "Executed:" tag on a `grep` is exactly the
claim class Premise Discipline exists for, and it is attached to the number that justifies a deletion.
The **deletion is still right** (a WARN in `Customize` does fire 3–4× per mount, and never for the
admin group under `Mount` — I confirmed `Mount` excludes `AdminRoutes`). Only the premise is wrong.

**Fix:** drop the "Executed:" tag; write *"`ResolveConfig` has **15 call sites**, 5 per adapter
(`grep`, non-test); a documented mount invokes **3** of them (`Mount`) or **4** (`Mount` +
`AdminRoutes.Customize`), measured in round 6 — so a WARN in `Customize` fires 3–4× per mount and
never for the admin group."*

---

### M3 — the boundary that justifies deleting instrumentation ("it means a new exported `httpcore` API") does not distinguish the deleted mechanism from the kept ones

**Severity: MAJOR**

**Bundle says:** ADR `:160-163`: "**No instrumentation.** … `Instrumentation`'s fields are unexported
and only `httpcore` builds instruments from `cfg.MeterProvider`; **wiring one from three parallel
adapters means a new exported `httpcore` API.**" spec §6 Non-goals `:193-194`: "No new exported
interface and no new cross-package contract. ⚠ **This is load-bearing**: it is why the mount-time
construction error and the instrumentation are deleted rather than built."

**My INDEPENDENT net:**
- `observability.go:23-28` — all four `Instrumentation` fields unexported ✅; `NewInstrumentation`
  (`:40`) is the sole builder ✅; `grep 'go.opentelemetry.io' transport/http/{stdlib,gin,fiber}`
  exits 1 ✅. The **facts** are right.
- But `NewInstrumentation` is **exported and already called by all three adapters**
  (`stdlib/groups.go:37`, and the equivalent in gin/fiber), and `inst *httpcore.Instrumentation` is a
  declared parameter of each adapter's `handle` (`stdlib/groups.go:18`) and a live local in every
  `Customize` — so it is **in lexical scope at all 39 decode sites**.
- Phase 1 of this very delivery adds **two new exported `httpcore` symbols**: `WithMaxBodyBytes` and
  `ErrRequestBodyTooLarge` (`grep` confirms neither name exists anywhere in the repo today).

**Observed:** "requires a new exported `httpcore` API" is true of the histogram **and equally true of
the two things the delivery ships**. The stated boundary therefore does not discriminate; the real
reason is a scope call.

**Verdict: CONFIRMED-DEFECT (justification, not mechanism).** The deletion is defensible; its stated
rationale is not the thing doing the work, which matters because ADR §Neutral `:231-232` defers the
follow-up on the same false ground (*"once a way to build an instrument outside `httpcore` exists"* —
one already exists; what is missing is a *method on `Instrumentation`*, in a package phase 1 already
opens).

**Fix:** restate as *"An instrument would need a new **method on `httpcore.Instrumentation`** plus a
recording call at 39 sites across three packages. `inst` is already in scope at each site, so this is
a scope decision, not a structural impossibility. Deferred."* Correct the Neutral follow-up the same
way.

---

### M4 — spec §4 coupling 6 marks `cursorcodec.go` ✅ "adopted rather than re-invented"; the delivery in fact DIVERGES from it, and the divergence is unstated

**Severity: MAJOR**

**Bundle says:** spec `:142` §4 coupling 6: "⭐ **`runtime/kernel/cursorcodec.go` already handles
trailing data after a JSON value** (ADR-0160). | ✅ Cited in ADR §Context and used as the reason the
cap must bound the **read**, not the parse." ADR banner `:33-35`: "Two mechanisms in this repo already
solved parts of this and were missed." ADR Positive `:201-202`: "**One convention for bounding a body
across the library.**"

**My INDEPENDENT net** — `runtime/kernel/cursorcodec.go:27-33`, the doc comment ADR-0160 added:
> "a trailing-data check, because [json.Decoder.Decode] reads only the FIRST JSON value and silently
> ignores whatever follows. **The plain [json.Unmarshal] this supersedes rejects trailing bytes, so
> without this the "hardened" decoder would be strictly weaker than the code it replaced**, and an
> attacker-supplied cursor could carry a second payload past review."

**Observed:** ADR-0160's decision on trailing data after a JSON value is **reject it**. ADR-0186's
decision is **keep accepting it** — ADR `:126-127` and `:198-199`: *"under-cap behaviour is
byte-for-byte unchanged, **including trailing bytes**"*, and evidence §8.1's `undercap-trailing`
row is `TODAY=parsed/<nil> MINIMAL=parsed/<nil>`. After this delivery the library holds **two
opposite policies for the same construct**: a second JSON payload smuggled after a cursor is a hard
rejection with a named guard; the identical construct in a request body on `POST /instances` is
silently accepted, as long as it is under 1 MiB.

**Verdict: CONFIRMED-DEFECT (mislabelled coupling).** What was borrowed from `cursorcodec.go` is the
**observation** (`Decode` stops at the first value); what was *not* adopted is its **policy**. Marking
it ✅ "adopted rather than re-invented" and pairing it with "one convention across the library"
conceals a deliberate divergence. ⚠ The divergence is probably **correct** — ADR-0186 is protecting
wire compatibility on a public HTTP surface where ADR-0160 was hardening an opaque internal token —
but it must be *stated*, because the ADR's own standing-lesson paragraph (`:146-149`) exists to make
exactly this kind of cross-delivery consequence outlive the bundle.

**Fix:** re-grade coupling 6 to ✅/⚠ split: *"✅ its **observation** is adopted (the cap must bound the
read); ⚠ its **policy** is deliberately NOT — ADR-0160 rejects trailing data, this delivery preserves
lenient acceptance under the cap to avoid a wire break. The library will hold two policies; the
reason is the difference between an opaque internal cursor and a public request body."* Add the same
sentence to ADR §Context beside the `cursorcodec` citation.

---

### M5 — the carry-forward record's two provenance commits are unreachable from every ref

**Severity: MAJOR**

**Bundle says:** `2026-08-21-untrusted-input-deferred-slices.md:6-8`: "It **quotes** the state of five
decisions as they stood on `design/authz-security-b3` (**three at commit `1e527347`, two at
`6cddb7b1`**)". The file's whole purpose is `:14` *"a holding record so that splitting ADR-0186 loses
nothing"* / *"**Nothing was dropped.**"*

**My INDEPENDENT net:**
```
$ git cat-file -t 1e527347 → commit      $ git cat-file -t 6cddb7b1 → commit
$ git merge-base --is-ancestor 1e527347 HEAD → false
$ git merge-base --is-ancestor 6cddb7b1 HEAD → false
$ git log --all --oneline | grep -c '^1e52734' → 0
$ git log --all --oneline | grep -c '^6cddb7b' → 0
$ git branch -a → design/authz-security-b3, docs/architecture-audit, main, + origin/* (main+dependabot only)
```

**Observed:** both commits are **dangling objects**, reachable from no branch, tag or remote. They
survive only in this clone's object store until the next `git gc`, and the branch has never been
pushed (`origin` carries `main` + dependabot only). The fold-don't-stack `--amend` discipline orphaned
them — the known failure mode.

**Verdict: CONFIRMED-DEFECT.** The one document whose job is "nothing was dropped" anchors its
provenance on two references that cannot be resolved by anyone else, on any other machine, after any
`gc`. A future §SSRF or §BOUND author cannot recover the quoted state.

**Fix:** delete the SHA citations and replace with a self-contained statement — the carry-forward
already reproduces the decision text, the refutations and the open questions inline, so it does not
*need* the commits; it just needs to stop implying it does. If the pre-strip state is genuinely worth
keeping, tag it (`git tag adr-0186-prestrip <sha>`) before it is collected, and cite the tag.

---

### m1 — `cursorcodec.go:50-58` is the wrong anchor for the comment the bundle quotes (it is at `:27-28`)

**Severity: MINOR** · appears **5×**: ADR `:34`, ADR `:68-70`, spec §5 row 6, plan `:106`, evidence §7.1.

**Bundle says:** "`runtime/kernel/cursorcodec.go:50-58` carries a trailing-data guard **whose comment
says** `Decode` *'reads only the FIRST JSON value and silently ignores whatever follows'*".

**My net:** `grep -rn 'silently ignores whatever follows' --include='*.go' .` → **one hit,
`cursorcodec.go:28`**. Lines `:50-57` are a *different* comment ("Decode consumed exactly one JSON
value; anything left that is not whitespace…") and `:58` is the `dec.Token()` guard. The quoted text
lives in `decodeCursorInto`'s **doc comment**, `:27-28`.

**Verdict: CONFIRMED (anchor).** The guard *is* at `:50-62`; the quoted sentence is not.
**Fix:** cite `cursorcodec.go:27-33` for the quote and `:50-62` for the guard.

---

### m2 — the carry-forward's cross-slice dependency still says "slice 4" where its own table says slice 6. Round 6 found this; it was NOT fixed.

**Severity: MINOR (but a REGRESSION — a known finding survived a revision)**

**Bundle says** (`…deferred-slices.md:63-72`): "**D2 mints `service.ErrVariablesTooLarge`** … **slice 4
adds the second sentinel to the existing arm**. The dependency runs **slice 1 → slice 4** and never
back. … ⚠ **Slice 4 re-opens it**".

**My net:** the same file's slice table (`:44-51`): slice **4** = *"the instance read path aliases and
discloses"* (§READ-PATH, ADR-0189); slice **6** = *"variable-map admission bound"* (§BOUND, ADR-0191).
`ErrVariablesTooLarge` is §BOUND's sentinel — `:248` and `:266` both place it there. Three occurrences
of "slice 4" in a section whose subject is slice **6**.

**Verdict: CONFIRMED.** Spec §4 coupling 3 gets it right (*"the carry-forward's §BOUND owns the
message question"*), so only the carry-forward is wrong. **Fix:** s/slice 4/slice 6/ ×3.

---

### m3 — `seam.go:108` cited for a call that is at `:110`

**Severity: MINOR** · ADR `:216`, spec §4 coupling 8, evidence §8.3.

"`MountGroups(r, groups...)` calls `Customize(r)` with **no options** (`seam.go:108`)" — `:108` is the
**func signature**; the `g.Customize(r)` call is `:110`. spec §5 row 3's `seam.go:105-111` also clips
the godoc, which starts at `:104`. **Fix:** `seam.go:108-111` for the function, `:104-107` for the
godoc that names the escape.

---

### m4 — "documented in six places" (`action/httpcall`'s cap convention) matches neither of my two defensible nets

**Severity: MINOR** · evidence §8.2 `:698`; inherited verbatim from audit4 `:81`.

My nets over `action/httpcall/httpcall.go`: doc comments that state **"a non-positive value disables"**
→ `:36, :88, :112, :183, :189` = **5**. Doc comments that mention the cap **at all** → those five plus
`:206` and `:224-225` = **7**. Neither is six. **Verdict: CONFIRMED (unverifiable inherited count).**
**Fix:** say *"stated in five doc comments (`:36, :88, :112, :183, :189`)"*, or drop the count.

---

### m5 — the package set is 5, not 4: phase 1's prescribed test introduces a new `httpcore` → `action/httpcall` import edge

**Severity: MINOR**

Plan §4: "packages this delivery touches | **4** — `httpcore`, `stdlib`, `gin`, `fiber` (+`parity` for
tests, +docs)". Plan §2 row 5 prescribes "`httpcall.ErrBodyTooLarge` still classifies 500 (test)" in
phase 1 / `httpcore`.

My net: `grep -rn 'action/httpcall' transport/` **exits 1** — no package under `transport/` imports it
today. The test creates the edge. No cycle (`action/httpcall` imports only `github.com/kartaladev/wrkflw/action`),
so it is **safe** — but the enumeration is short by one, and plan §2's own rule is that every
prescribed thing gets a package. **Fix:** "+`action/httpcall` (test-only import, no cycle: it imports
only `action`)".

---

### m6 — `cursorcodec.go:44` cited for `DisallowUnknownFields()`, which is at `:45`

**Severity: MINOR** · evidence §6.2 `:397`. `:44` is `dec := json.NewDecoder(...)`. (This row belongs
to the deferred 4XX slice; logged so it does not travel.)

---

### m7 — the standing arm-ordering invariant is a Decision sentence with no plan row and no home

**Severity: MINOR**

ADR `:146-149` decides: "⚠ **`ClassifyError`'s arms are order-dependent by construction.** Any future
arm … must state its position and carry a test asserting an error matching two arms resolves to the
intended one. **This sentence exists so the lesson outlives the bundle that learned it.**"

Plan §2's rule is *"Every sentence of the ADR's Decision section has a row. A row with no phase is a
defect."* The only related row is "413 arm **before** the 400 arm + ordering comment" (phase 1), which
discharges *this* arm's placement, not the standing rule. A sentence whose stated purpose is to
outlive the bundle needs a durable home — a comment in `errors.go` above the switch, and/or the
`CONTRIBUTING`/`STABILITY` note. **Fix:** add a phase-1 row: *"a comment above `ClassifyError`'s
switch recording that arms are order-dependent and what a new arm owes."*

---

## Summary

**15 findings — 3 Critical · 5 Major · 7 Minor.**

⚠ **The arithmetic was right for the seventh consecutive round.** Every count I re-derived
independently — 39/36/3, 13/13/13, 6 `ClassifyError` arms and all six anchors, 26 = 9+15+2, 15
`ResolveConfig` call sites at 5 per adapter, 15 `Customize`, 6 `Mount`/`MountHealth`, all four
`httpcall.go` anchors, all three fiber vendor anchors, all six lineage finding-totals (58/12, 38/~13,
63/33, 56/28, 65/20, 61/24, and audit4's per-lens table sums to both 61 and 24) — was **exact**.

⚠⚠ **All three Criticals are again SCOPE, and two of them are the same shape as round 6's:** a
boundary derived correctly at one level (`ResolveConfig` runs per group; admin routes are absent from
`Mount`) and then asserted one level up without re-derivation (*"the option configures the delivery"*;
*"parity cannot see admin routes"*). The third (C1) is the **celebratory sentence** shape: a residual
discharged onto a document that does not carry it, tagged *"Verified from source."*

⭐ **The reverse error was live and I checked for it.** Every contradiction above carries the command
and a positive control (`grep -c 'the' SECURITY.md` = 14; the otel grep hitting 5 lines in `httpcore`
while exiting 1 in the adapters; the bare-`|` ERE reproduced against a known single hit).

---

## Addendum to C2 — the repo's own reference wiring already demonstrates the split

```
$ grep -n -E 'stdlib\.(Mount|MountHealth|AdminRoutes)|httpcore\.With' examples/*/main.go
sqlite_wiring/main.go:278      stdlib.Mount(mux, svc)                       # NO options
sqlite_wiring/main.go:279      stdlib.MountHealth(mux, readyChecks...)
mysql_wiring/main.go:262       stdlib.Mount(mux, svc)                       # NO options
mysql_wiring/main.go:263       stdlib.MountHealth(mux, readyChecks...)
production_wiring/main.go:264  stdlib.Mount(mux, svc, httpcore.WithMeterProvider[...](mp))
production_wiring/main.go:265  stdlib.MountHealth(mux, readyChecks...)
production_wiring/main.go:274  stdlib.AdminRoutes{Svc: svc}.Customize(adminMux,
                                   httpcore.WithMeterProvider[...](mp))     # option REPEATED, on a
                                                                            # DIFFERENT mux
```

`examples/production_wiring` is the repo's own proof that (a) the admin group is configured by a
**separate** call, (b) every option must be **repeated** there by hand, and (c) it is commonly mounted
on a **different router entirely** (`adminMux`) — so "pass the same opts to both" is not even a
copy-paste for a consumer who separates the surfaces, which ADR-0095 and `SECURITY.md:37-39` tell
them to do.

⇒ On the shipped examples: `sqlite_wiring` and `mysql_wiring` mount **no admin routes at all** (0 of
their 6 decode sites are admin), while `production_wiring` mounts all 13 across two muxes and would
need `WithMaxBodyBytes` written **twice** to get one policy. **Phase 4 must add the second call site
to `production_wiring` if the ADR wants "one policy" to be true of its own reference wiring**, and
plan §2's map has no row for `examples/` at all — it only schedules `go build ./examples/...`, which
cannot fail on a missing option.

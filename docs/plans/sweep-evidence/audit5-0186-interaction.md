# Audit round 7 (post-strip) — ADR-0186 — INTERACTION lens

Worktree detached at `27ff5841`. Step 0: all five bundle files present (ADR, spec, plan,
`…-adr-0186-premise-evidence.md`, `…-deferred-slices.md`) — verified, proceeding.

Question: **what does this decision assume someone else will hand it, and who agreed to that?**

---

### I1 — The documented migration lever `WithMaxBodyBytes(0)` DOES NOT COMPILE, and the repo's remedy for that has no row, no phase and no package

- **Severity: CRITICAL**
- **This bundle says-or-assumes.** The escape hatch is written **six times, always bare and
  un-instantiated**: ADR `:164` *"The migration lever is `WithMaxBodyBytes(0)`, not a metric"*;
  ADR `:214` *"The lever is `WithMaxBodyBytes(0)`"*; spec `:109`, `:180`, `:195`; plan `:67`,
  `:78`, `:227` (`TestDisabledCapDoesNotInstallTheReader` — `WithMaxBodyBytes(0)`), and plan
  `:264` prescribes writing exactly that string into `SECURITY.md`. The declared signature is
  plan `:163`: `WithMaxBodyBytes(n int64) CustomizeOption[R]`.
- **The other side.** `httpcore.CustomizeOption[R]` is generic in the **router type**, and `R`
  appears **only in the result type** of every `With…` constructor (`seam.go:63-97`). Go cannot
  infer it. The repo already worked around this in two different ways, and the bundle adopted
  neither: (a) explicit instantiation at the call site — `httpcore.WithMeterProvider[int](mp)`,
  `httpcore.WithLogger[int](logger)` (`httpcore/observability_test.go:29,77,141,175,195`); (b) a
  **non-generic per-adapter alias** in each adapter's `options.go` —
  `stdlib.WithBasePath` (`stdlib/options.go:12-14`), `gin.WithBasePath` (`gin/options.go:26-28`),
  `fiber.WithBasePath` (`fiber/options.go:22-25`), each a one-line wrapper whose godoc says
  *"so callers can use stdlib.WithBasePath without importing httpcore directly."*
- **The collision.** Inference fails **even in argument position against a concrete parameter
  type** — so the form the bundle documents is not merely unidiomatic, it is a compile error. The
  usable forms are `httpcore.WithMaxBodyBytes[*http.ServeMux](0)` /
  `[ginlib.IRouter]` / `[fiberlib.Router]`, or three new alias symbols. The plan's
  Decision→phase→package map (`§2`) has **no row** for adapter-level option aliases; phase 2's
  brief is decode-site edits only; phase 1 is `httpcore` only. This is round 6's own failure shape
  — *"four mechanisms whose home package could not host them"* — recurring as **an API the
  documented escape hatch requires that no phase builds**. It also lands in three packages that
  phase 2 fans out **in parallel**, so it cannot be retrofitted by one agent.
- **Evidence (executed).** Probe `transport/http/stdlib/zz_probe_infer_test.go` (deleted):

  ```go
  func take(opts ...httpcore.CustomizeOption[*http.ServeMux]) { _ = opts }
  func TestProbeInference(t *testing.T) { take(httpcore.WithBasePath("/x")) }
  ```
  ```
  vet: transport/http/stdlib/zz_probe_infer_test.go:14:7: in call to httpcore.WithBasePath,
       cannot infer R (declared at transport/http/httpcore/seam.go:63:1)
  ```
  A second form — `httpcore.ResolveConfig(httpcore.WithBasePath("/x"))` — fails identically.
- **Fix.** Decide explicitly: either (a) write every occurrence as
  `httpcore.WithMaxBodyBytes[R](0)` with a concrete `R` in ADR/spec/plan/`SECURITY.md`, or
  (b) add `WithMaxBodyBytes` aliases to `stdlib/options.go`, `gin/options.go`,
  `fiber/options.go` — in which case add a Decision→phase→package row (`phase 2`, all three
  adapter packages), name the three symbols in each phase-2 agent's brief, and note that three
  parallel agents each add one. (b) matches the repo's convention for consumer-facing options;
  (a) matches its convention for provider/observability options. Either way the string in
  `SECURITY.md` must be the one that compiles.

---

### I2 — The prescribed test that "pins" `httpcall.ErrBodyTooLarge` at 500 passes today with zero production change; it cannot fail against anything this delivery could get wrong

- **Severity: MAJOR**
- **This bundle says-or-assumes.** Spec §4 coupling **4** (*"`action/httpcall.ErrBodyTooLarge`
  already exists and means 500"*) is marked **✅**, discharged as *"Renamed
  `ErrRequestBodyTooLarge`; **a test pins that `httpcall`'s still classifies 500**. ADR §Decision."*
  ADR `:131-133` repeats *"A test pins that it still is."* Plan phase 1 test 1 `:177`:
  *"⚠ **Plus a row asserting `httpcall.ErrBodyTooLarge` still classifies 500.**"*
- **The other side.** `ClassifyError` (`httpcore/errors.go:26-59`) dispatches purely on
  `errors.Is`, i.e. on **sentinel identity**. `httpcall.ErrBodyTooLarge` and
  `httpcore.ErrRequestBodyTooLarge` are two distinct `errors.New` values; no naming choice can make
  one match the other's arm. The risk the rename addresses is **human confusion**, which no
  `errors.Is` assertion can observe.
- **The collision.** A ✅ discharged onto a test that **is green today, before any code is
  written**, and whose only RED mutation (adding an explicit `httpcall.ErrBodyTooLarge` arm) is
  something nobody proposes. This is the same class the lineage keeps shipping — a cited test that
  is not a covering test — and the plan violates its own rule for it: every other phase-1/phase-2
  test states a falsifier, this row states none, and Premise Discipline requires one.
- **Evidence (executed).** Throwaway `transport/http/httpcore/zz_probe_500_test.go` (deleted),
  run against the **unmodified** tree:
  ```
  PROBE bare:      status=500 body={Error:internal_error Message:}
  PROBE wrapped:   status=500 body={Error:internal_error Message:}
  --- PASS: TestProbeHttpcallStill500 (0.00s)
  ```
  (It also proves the incidental worry is nil: `httpcore`'s test importing `action/httpcall`
  compiles — `grep -rn 'transport/http' action/httpcall/` exits 1, so there is no cycle.)
- **Fix.** Either drop the row and downgrade coupling 4's ✅ to *"resolved by naming; nothing to
  test"*, or replace it with a test that can fail: a `go/parser`- or `grep`-based guard asserting
  no `httpcore` sentinel is named `ErrBodyTooLarge`, plus a doc-comment cross-reference on both
  sentinels. State the falsifier either way.

---

### I3 — Spec §4 row 3 and the carry-forward both rest on a STATIC 413 message that this bundle never specifies; every existing arm renders `err.Error()`

- **Severity: CRITICAL**
- **This bundle says-or-assumes.** Spec §4 coupling **3**: *"⚠ **This delivery ships no 413 message
  text, so there is nothing here for that delivery to contradict.**"* The carry-forward
  (`…-deferred-slices.md:68-74`) builds on it: *"⭐ **The split resolves a Critical for free.**
  Re-audit finding I-3/F3 was that a static `"request too large"` on the 413 arm is false when the
  arm is reached by a variable-count refusal … With D2 out of slice 1, the only producer of 413 is
  a genuinely oversize body, so **the static message is true as written**."*
- **The other side.** `httpcore/errors.go` — **all five** non-default arms render the error text:
  `:31` `ErrorBody{Error:"not_found", Message: err.Error()}`, `:33` `"forbidden"`, `:35`
  `"conflict"`, `:50` `"bad_request"`, `:56` `"conflict_state"`; only the 500 default (`:58`)
  blanks. A new arm written to house style therefore ships **`Message: err.Error()`**, i.e. the
  sentinel's own text — which *is* 413 message text, minted here.
- **The collision.** Two sentences, mutually exclusive, one paragraph apart across two bundle
  files: the spec says the delivery ships **no** 413 message text; the carry-forward says it ships
  a **static** one that is *"true as written"*. Neither is checkable, because **the ADR, the spec
  and the plan never state the 413 arm's `ErrorBody` at all** — not the `Error` code string
  (`"payload_too_large"`? `"too_large"`?), not whether `Message` is populated. That code string is
  a **wire contract** on a delivery whose own Negative section is headed *"BREAKING (wire)"*, and
  phase 3 asserts byte-for-byte envelope parity over it (`parity_test.go:556-620`
  `TestParity_ErrorEnvelopes`). And the resolution of the forward dependency inverts depending on
  which sentence is true: if the arm renders `err.Error()`, `ErrVariablesTooLarge` joining it is
  **already** per-sentinel and the deferred delivery inherits nothing; if it is static, the
  deferred delivery inherits an open Critical that the carry-forward has declared closed.
- **Evidence.** Source-read of `httpcore/errors.go:26-59` (quoted above); grep of all three
  authored bundle files for a 413 body shape returns only the status number, never an `ErrorBody`.
  Reasoned — not executed (the arm does not exist yet).
- **Fix.** State the 413 arm's full `ErrorBody` in the ADR Decision (code string + whether
  `Message` carries `err.Error()`), add it to the Decision→phase→package map, and add a phase-1
  assertion on the body, not only the status. Then correct **whichever** of spec §4 row 3 /
  carry-forward "resolved for free" is falsified by that choice — they cannot both survive.

---

### I4 — The carry-forward states the one cross-slice dependency against the WRONG SLICE, three times

- **Severity: CRITICAL**
- **This bundle says-or-assumes.** `…-deferred-slices.md:61-74`, the section titled *"The one
  cross-slice dependency, stated so it is not rediscovered"*: *"**D2 mints
  `service.ErrVariablesTooLarge` and D5 routes it to 413.** Slice 1 ships the 413 arm with **one**
  sentinel in it … **slice 4 adds the second sentinel to the existing arm**. The dependency runs
  **slice 1 → slice 4** and never back."* And `:72`: *"⚠ **Slice 4 re-opens it**: when
  `ErrVariablesTooLarge` joins the arm…"*
- **The other side.** The same file's own slice table (`:44-51`) assigns
  **slice 4 = "the instance read path aliases and discloses" (§READ-PATH, ADR-0189 reserved)** and
  **slice 6 = "variable-map admission bound" (§BOUND, ADR-0191 reserved)**. `ErrVariablesTooLarge`
  is named nowhere in §READ-PATH; it is §BOUND's sentinel (`:248` *"refused with
  `service.ErrVariablesTooLarge` → 413"*). This bundle's own spec §4 row 3 points correctly at
  *"the carry-forward's §BOUND"*.
- **The collision.** The single forward dependency this delivery creates — an ordering constraint
  on the 413 arm that outlives the bundle — is recorded, in the document written to preserve it,
  **pointing at a delivery that has nothing to do with it**. A future session reading the
  carry-forward will schedule the arm-ordering work into §READ-PATH and find nothing to do, while
  §BOUND (which the file's own ordering paragraph schedules **last**) inherits an unrecorded
  dependency. `D2` / `D5` are additionally bare labels from a superseded six-decision revision with
  no gloss anywhere in the file (CLAUDE.md rule 13).
- **Evidence.** `grep -n 'slice 4' docs/specs/2026-08-21-untrusted-input-deferred-slices.md` →
  `:66`, `:72`; slice table at `:44-51`; `ErrVariablesTooLarge` occurrences → `:63`, `:72`, `:248`
  (all §BOUND). Source-read, not executed.
- **Fix.** Replace "slice 4" with "**slice 6 (§BOUND, the variable-map admission bound)**" at both
  sites; expand `D2`/`D5` to their §-names on first use; and re-check the *ordering* paragraph
  (`:56-59`), which currently schedules §BOUND last **without** mentioning that it also carries
  slice 1's inherited 413-arm obligation.

---

### I5 — "The one cross-slice dependency" is a false quantifier; the same file states two more

- **Severity: MAJOR**
- **This bundle says-or-assumes.** `…-deferred-slices.md:61` — heading: *"**The one** cross-slice
  dependency, stated so it is not rediscovered"*.
- **The other side.** Same file, `:465` (§4XX): *"⇒ **This decision cannot deliver its headline
  claim without the read-path delivery (§READ-PATH).** State the dependency or withdraw the
  claim."* And `:296` (§BOUND item 3): *"⚠ Answer this **after** §READ-PATH, which may move the
  library's admission tier."* And the ordering paragraph `:58-59` states the same §READ-PATH →
  §BOUND edge a third time.
- **The collision.** A closed-set claim ("the one") in a boundary document, contradicted twice
  within the document itself, is exactly the recap-sentence defect the lineage keeps shipping — and
  here it is load-bearing, because the section exists precisely so a future session does not have
  to re-derive the cross-slice graph. As written, a reader who trusts the heading will believe
  §READ-PATH, §4XX and §BOUND are independently schedulable. They are not.
- **Evidence.** Source-read of the four cited lines. Not executed.
- **Fix.** Retitle to *"Cross-slice dependencies"* and list all three edges in one place:
  slice 1 → §BOUND (413 arm), §READ-PATH → §4XX (the 403 headline claim), §READ-PATH → §BOUND
  (the admission tier). Prefer naming the closed set over counting it.

---

### I6 — §4 row 8's ✅ discharges onto godoc that exists but does not say what the row claims; `MountGroups` consumers have no route to the escape hatch

- **Severity: MAJOR**
- **This bundle says-or-assumes.** Spec §4 coupling **8** — *"`MountGroups` consumers cannot pass
  options … so the 1 MiB default applies to the documented consumer extension seam"* — is marked
  **✅ "Not a gap — the existing godoc already names the escape: *'Groups needing distinct base
  paths or middleware call Customize directly with the relevant options.'*"* Repeated in ADR
  Negative `:215-218` and evidence §8.3 `:712`, and prescribed for `SECURITY.md` (plan `:271`).
- **The other side.** `seam.go:104-111`, read verbatim: the sentence enumerates **"distinct base
  paths or middleware"**. It is a routing/composition note predating this delivery; it says nothing
  about a security default, a body cap, or an opt-out. `MountGroups` (`:108`) calls
  `g.Customize(r)` with no opts, and its signature `MountGroups[R any](r R, groups ...RouteCustomizer[R])`
  admits none.
- **The collision.** The previous revision's version of this table had a ✅ discharged onto ADR text
  that **did not exist** (`grep` exited 1); §4's preamble says every ✅ now *"names the file and
  section that carries the resolution, so that failure is checkable"*. This ✅ names a real file and
  a real sentence — and the sentence does not carry the resolution. The failure mode moved from
  *absent text* to *present text that does not cover the claim*, which is harder to catch, not
  easier. Concretely: a consumer who mounts through `MountGroups` and today accepts 2 MiB bodies
  gets a **wire break with no in-place lever** — the "escape" is a restructure of their mount code
  into per-group `Customize` calls. Cross-check with **I1**: even after restructuring, the option
  they must write is the one that does not compile as documented.
- **Evidence.** `seam.go:104-111` quoted above; `grep -rn 'CustomizeConfig\[' --include='*.go' .`
  shows the struct is never literal-constructed outside `ResolveConfig`, so `MountGroups` really is
  option-free. Source-read, not executed.
- **Fix.** Downgrade row 8 to **⚠ stated, NOT resolved** (making it six such rows, which is
  honest), and say plainly in ADR Negative and `SECURITY.md`: *"a `MountGroups` consumer must
  switch to per-group `Customize` calls to change or disable the cap."* Alternatively add a
  `MountGroupsWith(r, opts, groups...)` — but that is a new exported symbol the spec's Non-goals
  forbid, so the honest downgrade is the cheaper answer.

---

### I7 — On fiber the cap bounds NOTHING but the status code: fasthttp has already buffered the whole body before `len(c.BodyRaw())` runs, so the stated "peak memory = cap × in-flight" is false for one of the three adapters

- **Severity: CRITICAL**
- **This bundle says-or-assumes.** The residual is stated **once, uniformly, for the delivery**:
  ADR `:188` *"⚠ **Peak memory is `MaxBodyBytes × in-flight requests`, and nothing here bounds
  concurrency.**"*; ADR Negative `:222`; spec §4 coupling **13** *"Peak memory couples the cap to
  CONCURRENCY (`cap × in-flight`)"*; plan `:268` prescribes that exact sentence for `SECURITY.md`.
  The mechanism for fiber is *"a `len(c.BodyRaw())` pre-check before `c.Bind().JSON`, **which is
  already before the parse**"* (ADR `:119`).
- **The other side.** Before the parse, yes — but **after the read**. fasthttp reads the entire
  request body into `Request.body` before the handler is invoked; that is why `BodyRaw()` returns
  `[]byte` rather than a reader (`fiber/v3@v3.4.0/req.go:92-96`). The real ceiling is
  `app.config.BodyLimit`, pushed into `app.server.MaxRequestBodySize` at
  `fiber/v3@v3.4.0/app.go:1516`, defaulted to `DefaultBodyLimit = 4 * 1024 * 1024` at `app.go:585`.
- **The collision.** The cap's *whole purpose* is memory: the spec's problem table calls the
  unbounded body a memory-exhaustion primitive, the ADR refuses to pass `0` to `MaxBytesReader`
  because *"an unbounded `io.ReadAll` is itself a memory-exhaustion primitive"* (`:118`), and the
  buffering-cost row (spec `:92`) reframes the true cost as *"peak memory = cap × in-flight"*. On
  fiber that number is **`fiber.Config.BodyLimit` × in-flight — 4× the stated figure at the
  default**, and setting `MaxBodyBytes` **does not move it at all**: the memory is spent before our
  code runs. So the delivery closes the memory hole on stdlib and gin and delivers, on fiber,
  **only a status code**. The bundle documents the fiber divergence as living *above* `BodyLimit`
  (a wire-format difference); it never says that *below* `BodyLimit` the security property itself is
  absent. This is a boundary handed to us by fasthttp's read model, and nobody agreed to it.
- **Evidence (executed).** Throwaway `transport/http/fiber/zz_probe_bodylimit_test.go` (deleted),
  `fiberlib.New(fiberlib.Config{BodyLimit: 1<<20})` with a handler that records whether it ran:
  ```
  PROBE n=100      status=200 ct="text/plain; charset=utf-8" body="len=100" handlerRan=true
  PROBE n=2097152  ERR=body size exceeds the given limit                    handlerRan=false
  ```
  The handler does not run above the limit ⇒ the body is read and bounded by fasthttp *before*
  dispatch ⇒ `len(c.BodyRaw())` can only observe bytes already resident.
- **Fix.** Say it in the ADR and `SECURITY.md`: **"peak memory is `MaxBodyBytes × in-flight` on
  stdlib and gin; on fiber it is `fiber.Config.BodyLimit × in-flight`, and `MaxBodyBytes` does not
  reduce it — a fiber consumer who wants a memory bound must set `fiber.Config.BodyLimit`."**
  Add it to spec §4 row 13 (splitting the coupling per adapter) and to the ADR's Negative. This is
  a documentation fix, not a mechanism change — but leaving it unstated makes the delivery's
  headline property untrue for a third of its surface.

---

### I8 — Phase 3's prescribed "fiber-only divergence case" cannot be written in the parity harness: `hitFiber` t.Fatalf's on exactly that condition

- **Severity: MAJOR**
- **This bundle says-or-assumes.** Plan phase 3 `:246-248`: *"⚠ **Pin the fixture below 4 MiB and
  say why in a comment** … Add a separate, **explicitly-labelled fiber-only case** documenting that
  divergence."*
- **The other side.** `transport/http/parity/parity_test.go:171-187`, `hitFiber`, drives fiber via
  `app.Test(req)` and does `t.Fatalf("hitFiber: app.Test: %v", err)` on error. Above `BodyLimit`,
  `app.Test` returns **an error and no response at all** — measured: `ERR=body size exceeds the
  given limit`. There is no status and no body to assert on; the prescribed case fails the suite
  instead of documenting anything.
- **The collision.** The plan assumes the existing harness will hand it an `(status, body)` pair
  for the over-`BodyLimit` case. It hands back a `t.Fatalf`. Writing the case therefore requires
  changing `hitFiber` (or adding a fiber-local variant that tolerates the error), which is work in
  the `parity` package that phase 3's one agent is not briefed for — and the ADR's own text about
  *"fasthttp's `text/plain` `Request Entity Too Large`"* is an **unexecuted** claim about a wire
  shape the harness cannot observe.
- **Evidence (executed).** The `TestProbeFiberBodyLimit` output in **I7**, plus
  `parity_test.go:171-187` read verbatim.
- **Fix.** Either drop the fiber-only parity case and keep the divergence as prose in
  `SECURITY.md` (cheapest, consistent with "documented only"), or brief phase 3 to add a
  `hitFiberExpectingTransportError` helper and assert the **error**, not a status — and correct the
  ADR's `text/plain` sentence to say it is what a *real socket* sees, labelled
  `ASSUMPTION (unverified)` until someone runs it over `net.Listen`.

---

### I9 — Phase 1 test 2 asserts something `httpcore` cannot observe: "whether a wrapper would be installed" has no subject in the phase-1 package

- **Severity: MAJOR**
- **This bundle says-or-assumes.** Plan phase 1 test 2 `:178-184`,
  `TestMaxBodyBytesDefaultAndDisable`: *"⚠ **Assert on the resolved config value AND on whether a
  wrapper would be installed** — asserting only the number cannot distinguish '0 stored' from
  '0 honoured'."* Its stated falsifier is *"the `0` row fails against an implementation that passes
  the configured value to `MaxBytesReader`"*.
- **The other side.** Phase 1's package is `transport/http/httpcore`, which by the bundle's own
  enumeration has **0 decode sites** (spec §2, plan §4) and installs no reader. The
  install/skip decision lives in the **adapters** (plan §2 map: *"`n <= 0` ⇒ do not install the
  wrapper" → phase 2 → `stdlib` | `gin`*). `httpcore` after phase 1 contains a config field, an
  option and a sentinel — nothing that can be asked "would you install a wrapper?".
- **The collision.** The assertion the plan says is the one that matters is **not writable in the
  phase it is assigned to**, and its falsifier is likewise unreachable there: an implementation
  that "passes the configured value to `MaxBytesReader`" is an *adapter* implementation. Either the
  assertion silently degrades to "0 stored" — the exact weakness the plan warns against, and the
  test becomes one that cannot fail — or someone invents an `httpcore` helper to give it a subject,
  which is the *"new cross-package contract"* the spec's Non-goals `:193-194` call **load-bearing**
  and forbid. This is the round-6 shape again: a mechanism whose home package cannot host it.
- **Evidence.** `transport/http/httpcore/*.go` contains no `http.Request` body read
  (`grep -rn 'json.NewDecoder\|MaxBytesReader' transport/http/httpcore/` over non-test files
  returns nothing); the 39 decode sites are all in the three adapters' `groups.go`. Source-read.
- **Fix.** Move the "0 honoured, not merely stored" assertion into **phase 2** (it is already there
  as test 6, `TestDisabledCapDoesNotInstallTheReader`), and reduce phase 1 test 2 to what
  `httpcore` can actually assert — the resolved value for unset / `0` / negative / `n` — saying so
  explicitly, with the honest falsifier (*"the unset row fails against a default placed in a
  post-loop guard"*). Then delete the now-redundant warning, or re-point it at phase 2 test 6.

---

### I10 — The four deletions were adjudicated one at a time; jointly they leave the new security control with no log, no dedicated metric and no mount-time signal — and the ADR's follow-up item mis-describes what is missing

- **Severity: MAJOR**
- **This bundle says-or-assumes.** Four mechanisms deleted in one revision (ADR banner table
  `:20-24`): the mount-time construction error, the histogram + rejection counter, the fiber mount
  WARN, the `*int64` tri-state. Each is justified **individually** ("no return channel",
  "no home", "fires 5× per adapter", "a plain `int64` suffices"). The stated joint consequence is a
  single sentence: ADR `:162-164` *"a consumer cannot measure their body-size distribution before
  the cap bites"*, plus ADR `:174` *"a 413 carries no correlation id and writes no log record"*.
  The follow-up (`:231-232`) is *"body-size observability, once a way to build an instrument
  outside `httpcore` exists"*.
- **The other side.** `writeErr` logs only at `status >= 500` (`stdlib/write.go:30-36`, and the gin
  and fiber twins), so a 413 is invisible in logs — correct as stated. But `httpcore` **already
  records every response**: `Instrumentation.Observe` (`observability.go:80-106`) emits
  `wrkflw_rest_requests_total` and `wrkflw_rest_request_duration_seconds` labelled with
  **`http.status_code`** (`:102`), and every route is wrapped (`stdlib/groups.go:14-25`,
  `handle(...) → observe(...)`). So rejections **are** countable as
  `wrkflw_rest_requests_total{http.status_code="413"}` — through an instrument `httpcore` already
  builds, from providers `CustomizeConfig` already carries.
- **The collision.** Two errors in opposite directions, and they matter to different readers. (a)
  The **Negative** as worded (*"a consumer cannot measure before the cap bites"*) reads, in a
  section listing costs, as "you get no telemetry" — an operator will not discover the existing
  counter from this document. (b) The **follow-up item** is scoped to *"a way to build an
  instrument outside `httpcore`"* — but the missing thing (a **request-size histogram**) would be
  built **inside** `httpcore`, exactly where instruments already are; what is missing is a *call
  site*, not a capability. Written as it is, the follow-up preserves the round-6 misdiagnosis
  (arguing from *observation* sites to a *declaration* boundary) into the backlog. Meanwhile the
  genuine joint residual — **a default-on control that silently changes production status codes
  with no log line, no correlation id, no warning and no per-request signal beyond a status
  counter** — is never stated as one thing.
- **Evidence.** `observability.go:99-106` and `stdlib/write.go:32` read verbatim; every route
  passes through `handle`→`observe` (`stdlib/groups.go:23-25`). Source-read, not executed
  (recording a metric end-to-end needs a meter provider; the attribute list is unambiguous).
- **Fix.** Reword ADR Negative to: *"a consumer cannot measure the body-size **distribution**
  before the cap bites; rejections after it bites are visible as
  `wrkflw_rest_requests_total{http.status_code="413"}` via the existing instrumentation, but with
  no log line and no correlation id."* Re-scope the follow-up to *"a request-body-size histogram
  recorded from the adapters — needs a way for adapter code to reach the `httpcore`-owned
  `Instrumentation`"*, which is the actual boundary.

---

### I11 — "One convention for bounding a body across the library" is asserted as a Positive with no owner and no invariant, and a deferred delivery is already positioned to break it

- **Severity: MINOR**
- **This bundle says-or-assumes.** ADR Positive `:201-202`: *"**One convention for bounding a body
  across the library**, matching `action/httpcall` rather than inventing a second."* Spec §4
  coupling **5** ✅. ADR `:102-103`: *"matching `action/httpcall.WithMaxResponseSize` **exactly**"*.
- **The other side.** Verified in source (`action/httpcall/httpcall.go:186,190-201,207-214`): plain
  `int64`, `max <= 0` disables, default in the constructor — the *config* convention matches
  exactly. The *mechanism* and *status* do not: `httpcall` uses
  `io.ReadAll(io.LimitReader(r, max+1))` and returns its own `ErrBodyTooLarge`, classified **500**
  and **non-retryable** (`:279`, `:348`); `httpcore` will use `http.MaxBytesReader` and return
  **413**. And `action/httpcall` is the subject of a **deferred** delivery (§SSRF, ADR-0190
  reserved), whose open questions include *"the refusal's error shape"* — i.e. that package's error
  surface is explicitly in play. §BOUND likewise proposes `service.WithMaxVariableBytes` /
  `WithMaxVariableElements` and **never says whether `0` disables there**.
- **The collision.** A Positive that spans package boundaries, guarded by nothing. Contrast the
  arm-ordering coupling (spec §4 row 1), where the ADR deliberately minted a standing invariant
  *"so the lesson outlives the bundle that learned it"*. The `<= 0` convention got no such
  sentence, so the next delivery that bounds something has no reason to know the claim exists.
- **Evidence.** `httpcall.go` lines above read verbatim; `…-deferred-slices.md:222` (§SSRF item 4)
  and `:248`, `:294` (§BOUND). Source-read.
- **Fix.** Narrow the Positive to what is true — *"one **configuration** convention: a plain
  `int64` where a non-positive value disables"* — and add a standing sentence beside the
  arm-ordering invariant: *"any future bound in this library uses the same `int64` / non-positive-
  disables shape; state it when you add one."* Add the same line to §BOUND's decision list so the
  deferred delivery inherits it explicitly.

---

### I12 — The carry-forward assigns `keywordLocation` ownership to **slice 1**, which forbids touching it

- **Severity: MAJOR**
- **This bundle says-or-assumes.** `…-deferred-slices.md:304` heading *"What HELD across both
  audits — do not re-derive it in any of the **three** bundles"*, first bullet `:308`:
  *"**`keywordLocation` is value-free across fifteen schema shapes.** ⚠ Word it
  **'author-derived'**, not 'schema-derived'. (**Slice 1 owns this.**)"*
- **The other side.** Slice 1 **is** ADR-0186, this bundle (carry-forward slice table `:45`).
  `keywordLocation` is a jsonschema validation-message concern; the **same file** discusses it
  under §4XX (slice 3, *"what a 4xx body may say"*) at `:483` and `:485`. This bundle's spec §6
  Non-goals `:190-191` says
  *"**No change to what any 4xx message says.** That is the deferred 4xx delivery, and touching it
  here re-creates the bundle that just failed."* The string `keywordLocation` appears **nowhere** in
  the ADR, the spec or the plan.
- **The collision.** The boundary document hands this delivery an obligation this delivery's own
  Non-goals refuse. A session working from the carry-forward would look for a `keywordLocation`
  wording fix in ADR-0186 and find none — and might add one, which is precisely the scope creep the
  re-cut exists to prevent. The same bullet's heading also says *"the **three** bundles"*, stale
  from the three-decision cut: the file now describes **six** slices.
- **Evidence.** `grep -rn 'keywordLocation'` over the ADR, spec and plan exits 1; carry-forward
  `:305` and `:45`; spec `:190-191`. Source-read.
- **Fix.** Reassign the bullet to **slice 3 (§4XX)** and correct *"three bundles"* to name the
  closed set of remaining slices (§AT-REST, §4XX, §READ-PATH, §SSRF, §BOUND) rather than counting
  them.

---

### I13 — Backlog bookkeeping: the ADR's "Relates to" and the plan's phase 4 agree, but the ADR's Neutral section re-opens two items it also lists as deferred

- **Severity: MINOR**
- **This bundle says-or-assumes.** ADR header `:44-45`: *"Backlog: **98** only. ⚠ 104, 100, 101,
  54, 65, 99 belong to the five deferred deliveries."* Plan phase 4 `:279`: *"Close backlog **98**.
  ⚠ **Do NOT close 104, 100, 101, 54, 65 or 99.**"* ADR Neutral `:237` repeats the six.
  **These three agree exactly** — checked item by item: {98} closed, {104, 100, 101, 54, 65, 99}
  left open, six items, five deliveries (100 and 101 share §AT-REST).
- **The other side.** ADR Neutral `:230-235` additionally **opens three new items** — body-size
  observability, a mount-time diagnostic channel, the fiber decompressed-size bound. None of them
  has a number, and the plan's phase 4 `:280-281` lists the same three under **"Open:"** with no
  instruction to allocate numbers or to record them anywhere.
- **The collision.** Minor but real: this lineage's backlog is the only durable record of the
  deletions' consequences, and three consequences are being handed to it as prose. The
  instrumentation item is additionally mis-scoped (see **I10**). Without numbers they will not
  appear in any future sweep, and the ADR's own claim that the deletions are *"removed rather than
  deferred silently"* (`:158`) rests on them landing somewhere.
- **Evidence.** The three cited passages read verbatim; item sets compared element-wise.
- **Fix.** Give the three follow-ups backlog numbers in phase 4 and name them in the ADR's Neutral
  section, or state explicitly where else they are recorded. Fold **I10**'s re-scoping into the
  instrumentation one before it is numbered.

---

### I14 — The missing couplings: three things THIS delivery's claims depend on that no row asks about

- **Severity: MAJOR**
- **This bundle says-or-assumes.** Spec §4's preamble `:126-129` diagnoses the previous table's
  blind spot as *"it asked 'what does the removed slice hand D1?' and never 'what did D1's claims
  depend on that left with the removal?'"*, and says the table is rebuilt to fix it. The rebuilt 14
  rows still ask outward: 12 of the 14 name an *external* fact (an ADR, another package, a
  framework) that constrains this delivery.
- **The other side / the collision.** Three dependencies of this delivery's own claims have no row:
  1. **The generic-option seam** (`CustomizeOption[R]`, `seam.go:36`) is a dependency of the
     migration lever, and it does not support the form the lever is written in — **I1**.
  2. **`ClassifyError`'s house style of rendering `err.Error()`** is a dependency of the claim
     *"this delivery ships no 413 message text"* — and it refutes it — **I3**.
  3. **fasthttp's eager body read** is a dependency of the claim *"peak memory is cap ×
     in-flight"* — and it refutes it for fiber — **I7**.
  Each is a *"someone else hands us X"* of exactly the kind the table is for, and each is invisible
  to the outward-facing question. A fourth near-miss: the **parity harness** (`hitFiber`) is a
  dependency of phase 3's prescribed divergence case — **I8**.
- **Evidence.** As recorded under I1, I3, I7, I8 (two executed, two source-read).
- **Fix.** Add the four rows, each marked honestly. More usefully, add the *inward* question to the
  table's preamble so the next rebuild asks it by construction: **"for each load-bearing claim in
  the ADR, name the external mechanism it depends on and say who guarantees it."** Rows 1–14
  answer *"what constrains us"*; nothing yet answers *"what are we standing on"*.

---

### I16 — The delivery introduces the read-to-EOF hang, then applies "the consumer owns `ReadTimeout`" to three servers WE own

- **Severity: MAJOR**
- **This bundle says-or-assumes.** ADR §*"The bound is on SIZE, not on TIME"* `:184-187`:
  *"**The consumer owns `ReadTimeout`.** `SECURITY.md` says so plainly, and notes that all three
  `examples/` set `ReadHeaderTimeout` but **not** `ReadTimeout`. We do not set it for them: the
  `http.Server` belongs to the consumer, and this library is mounted into it."* Plan phase 4
  `:266-267` prescribes the same sentence for `SECURITY.md`.
- **The other side.** CLAUDE.md defines `examples/` as *"**reference wiring** showing how a
  consumer embeds the engine and mounts its transports"* — code **we** write and ship. The three
  servers are ours: `examples/production_wiring/main.go:280`, `examples/sqlite_wiring/main.go:284`,
  `examples/mysql_wiring/main.go:268`, each an `http.Server` literal with
  `ReadHeaderTimeout: 5 * time.Second` and **no `ReadTimeout`**. All three import
  `transport/http`, so all three get the default cap and therefore the new read-to-EOF behaviour.
- **The collision.** The justification *"the `http.Server` belongs to the consumer"* is true of the
  library and **false of `examples/`**, and the ADR uses those same three files as *evidence* for
  the hazard while declining to fix them. The delivery would therefore ship, in the same commit,
  (i) a new slowloris exposure it created, (ii) documentation telling consumers to set
  `ReadTimeout`, and (iii) three reference wirings demonstrating the configuration the
  documentation warns against. The plan's Decision→phase→package map has **no `examples/` row**;
  `examples` appears only as a `go build` target (plan `:115`, `:330`) and in an audit prompt
  (`:68`).
- **Evidence (executed).** `grep -rn 'ReadHeaderTimeout\|ReadTimeout' examples/` →
  three `ReadHeaderTimeout` hits, zero `ReadTimeout`; `grep -rln 'transport/http' examples/` →
  the same three files.
- **Fix.** Add a phase-4 (or phase-2) row: set `ReadTimeout` in all three `examples/` servers with
  a one-line comment pointing at ADR-0186's size-not-time section, and change the ADR sentence to
  *"the consumer owns `ReadTimeout` for their own server; our three reference wirings set it, and
  `SECURITY.md` explains why yours must too."* Cost: three lines. Leaving it undone makes the
  residual's disclosure self-contradicting.

---

### I15 — Five "stated, NOT resolved" rows: honest, with one exception

- **Severity: INFO (adjudication of the plan's explicit question)**
- Rows **2** (no correlation id / no log on a 413), **3** (a future `ErrVariablesTooLarge` joins
  the arm), **11** (bounds a request, not an instance), **12** (read-to-EOF couples the cap to
  time) and **13** (peak memory couples the cap to concurrency) are marked *"stated, NOT
  resolved"*. Judged against the test *"does leaving this open make the delivery net-negative?"*:
  - **2, 11, 12** are honest and correctly scoped. 12 is the one that comes closest to
    net-negative — the delivery **introduces** the read-to-EOF hang that did not exist before, and
    mitigates it only by telling the consumer to set `ReadTimeout` — but the ADR states it in three
    places including its own Decision section, and `examples/` not setting `ReadTimeout` is called
    out. That is disclosure, not evasion.
  - **13 is not honest as written** — it is false for fiber; see **I7**.
  - **3 rests on an unspecified artifact**; see **I3**.
  - Separately, row **8** is marked ✅ and should be a sixth *"stated, NOT resolved"*; see **I6**.
- **Fix.** Fix 13 and 3 as described; downgrade 8. Then the "five deliberately open rows" claim
  becomes *"six"*, which is the number that is true.

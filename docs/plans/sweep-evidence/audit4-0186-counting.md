# Audit round 6 — ADR-0186 bundle — COUNTING lens

Worktree: wt2/counting @ 85d6bb68
Started: 2026-08-21

## Verified BUNDLE-CORRECT (recorded so they are not re-derived a seventh time)

- **39 decode sites = 13 stdlib (`json.NewDecoder`) + 13 gin (`ShouldBindJSON`) + 13 fiber
  (`c.Bind().JSON`), `httpcore` 0.** Re-derived by an independent `go/parser` walk over all
  non-test files under `transport/`. Exact match.
- **3 discarding sites, at the exact anchors claimed** — `stdlib/groups.go:238`,
  `gin/groups.go:265`, `fiber/groups.go:255`, detected structurally (`AssignStmt` with all-blank
  LHS), not by grep. 36 propagating. Exact match.
- **`ClassifyError` has 6 ordered arms and every line anchor resolves**: 404 `:28`, 403 `:32`,
  409 `:34`, 400 `:36-50`, 422 `:51`, default 500 `:57`. Exact match.
- **26 routes = 9 non-admin + 15 admin + 2 health.** Counted per adapter: gin 26, fiber 26.
- **The corrected `-E` grep works and the broken one is still broken.**
  `grep -rnE 'MaxBytesReader|BodyLimit|MaxBytesHandler|LimitReader' transport/` → EXIT=1 (0 caps);
  positive control repo-wide → `action/httpcall/httpcall.go:194 io.LimitReader` EXIT=0.
  The literal-pipe form `-E 'MaxBytesReader\|BodyLimit'` → EXIT=1 as the ADR warns. No regression.
- **PACKAGE BOUNDARY HOLDS.** Repo-wide AST walk for request-body reads (`json.NewDecoder`,
  `*Bind*`, `io.ReadAll`, `ParseForm`, `FormValue`, `BodyParser`, `c.Body*`, …) plus a repo-wide
  `\.Body\b` grep: the only request-body readers are the three adapters' `groups.go`.
  `action/httpcall` reads an outbound **response** body; `definition/model` decodes definition
  source, not a request. No fourth package. (This was the exactly-analogous step that missed
  `engine` last round; here it is clean.)

---

### C1 — "negative → a construction error, surfaced at mount time": the BOUNDARY of what can carry an error. No such mechanism exists, and three bundle claims deny it will be built.
**Severity:** Critical
**Bundle says:**
- ADR Decision: "**negative → a construction error**, surfaced at mount time rather than per request."
- ADR Consequences/Positive: "The delivery is **one decision in four packages** … with no new
  cross-package mechanism and **no new exported interface**."
- Spec §6 Non-goals: "**No new exported interface and no new cross-package contract.**"
- Plan §2 map: | negative `MaxBodyBytes` is a construction error at mount | **1** | **`httpcore`** |
- Plan phase 1 test 3: `TestNegativeMaxBodyBytesIsRefusedAtMount` — "the construction error is reachable."
- Plan phase 4: "⚠ `ClassifyError`'s signature is **not** changed." (two breaks enumerated, both wire)

**Bundle's net (if given):** none — the mechanism is asserted, never derived.

**My INDEPENDENT net:** read every signature on the config/mount path.
```
$ grep -rn --include='*.go' 'ResolveConfig' . | grep -v _test        # 15 non-test call sites
$ grep -rn --include='*.go' 'func .*Customize(' . | grep -v _test    # 15 method decls
```
**Observed** (`transport/http/httpcore/seam.go`, `*/mount.go`) — verbatim signatures:
```
seam.go:36   type CustomizeOption[R any] func(*CustomizeConfig[R])            // no error
seam.go:39   func ResolveConfig[R any](opts ...CustomizeOption[R]) CustomizeConfig[R]   // no error
seam.go:100  type RouteCustomizer[R any] interface {
seam.go:101      Customize(r R, opts ...CustomizeOption[R])                   // no error  <-- EXPORTED SEAM
seam.go:108  func MountGroups[R any](r R, groups ...RouteCustomizer[R])       // no error, NO opts
stdlib/mount.go:17  func Mount(mux *http.ServeMux, svc service.Service, opts ...) // no error
gin/mount.go:14     func Mount(r ginlib.IRouter, svc service.Service, opts ...)   // no error
fiber/mount.go:15   func Mount(r fiberlib.Router, svc service.Service, opts ...)  // no error
```
Every link in the chain option → `ResolveConfig` → `Customize` → `Mount` is error-free, and
`RouteCustomizer[R]` is documented at `seam.go:105-107` as **the consumer extension seam**
("any RouteCustomizer[R] — **including a consumer's own**").

**Verdict:** CONFIRMED-DEFECT. Four distinct defects in one decision:

1. **No reachable mechanism.** Surfacing a construction error requires changing
   `ResolveConfig` to `(CustomizeConfig[R], error)`, which forces `Customize` to return an
   error, which **changes the exported `RouteCustomizer[R]` interface** and breaks every
   consumer implementation of it. The only alternatives the current shape allows are **panic**
   (never mentioned anywhere in the bundle, and a new failure mode on a library mount path) or
   silently ignoring the value (which is not "a construction error").
2. **It contradicts the bundle's own "no new exported interface" claim** — stated twice, in the
   ADR's Positive consequences and the spec's Non-goals. Both are false if this decision ships.
3. **Wrong package and wrong phase.** Plan §2 assigns the row to phase 1 / `httpcore`, but
   `Mount` exists only in the three adapters. `httpcore`'s own `MountGroups` takes **no opts at
   all**, so `MaxBodyBytes` cannot even reach it. Phase 1's
   `TestNegativeMaxBodyBytesIsRefusedAtMount` is prescribed in a package that has no mount
   function capable of carrying the option — the test as named cannot be written there.
4. **The break enumeration is short.** Plan phase 4 tells the CHANGELOG/STABILITY author there
   are exactly **two** breaks, both *wire* (new 413; trailing-byte requests start failing), and
   reassures that `ClassifyError`'s signature is unchanged — naming the one signature that does
   **not** move while omitting the ones that must. A `ResolveConfig`/`Customize` signature change
   is a **source-level break of the module-root public API**, which for a library-first product
   (CLAUDE.md: "the product is the module-root public API") is the most severe class there is.
   `STABILITY.md:29-31` permits it pre-1.0 but requires the CHANGELOG "call them out explicitly".

**Fix:** Pick one and write it into the ADR *and* §2's map:
- **(a) Preferred, keeps every claim true:** negative is **not** a construction error — clamp it
  to the default and emit a **WARN at mount**, exactly like the fiber `DefaultBodyLimit` WARN this
  bundle already ships (same package, same mechanism, no signature change). Delete the "no new
  exported interface" contradiction; retitle the test
  `TestNegativeMaxBodyBytesWarnsAndUsesDefault`, sited per-adapter in phase 2.
- **(b) If refusal is genuinely wanted:** state the signature change explicitly, move the row to
  phase 2 (`stdlib`|`gin`|`fiber`), delete both "no new exported interface" sentences, and add the
  **third break** — "`ResolveConfig`, `RouteCustomizer.Customize` and the three `Mount` functions
  gain an `error` return" — to the phase-4 CHANGELOG/STABILITY row.
- Either way `WithMaxBodyBytes` needs a stated signature; the bundle never gives one, though every
  other `CustomizeConfig` field has a `With*` option (`seam.go:62-97`).

---

### C2 — The prescribed falsifier for phase 1 test 2's `nil` row is VACUOUS: the post-loop idiom it names as the mutation is CORRECT once the field is `*int64`
**Severity:** Major
**Bundle says:**
- Plan §3 phase 1: "⚠ **`ResolveConfig` must apply the 1 MiB default only when the pointer is
  `nil`.** The existing post-loop defaulting idiom (copied from `Logger`) **cannot distinguish an
  explicit `0` from an unset field** — that is the pre-condition for the whole defect."
- Plan §3 phase 1 test 2: "⚠ **Falsifier for the `nil` row:** *it fails against a `ResolveConfig`
  that defaults with the post-loop idiom*, which would overwrite an explicit `0`."
- ADR Context: "`ResolveConfig`'s post-loop defaulting idiom (copied from `Logger`) cannot
  distinguish 'the consumer wrote 0' from 'the consumer wrote nothing'…"

**Bundle's net:** none. Asserted, never executed against the *chosen* design.

**My INDEPENDENT net:** reproduce `ResolveConfig`'s exact post-loop shape
(`seam.go:56-58`, `if cfg.Logger == nil { cfg.Logger = slog.Default() }`) over a `*int64` field.

**Observed:**
```
no option (nil)                    -> *MaxBodyBytes=1048576
explicit 0 (the opt-out)           -> *MaxBodyBytes=0
explicit 4096                      -> *MaxBodyBytes=4096
explicit negative -1               -> *MaxBodyBytes=-1
```
**Verdict:** CONFIRMED-DEFECT.

The claim is true of the **rejected non-pointer `int64`** design and **false of the `*int64`
design the ADR actually chose**. `if cfg.MaxBodyBytes == nil` distinguishes an explicit `0`
(non-nil pointer to zero) from unset perfectly — it is precisely *why* the pointer works.

This is round 5's own signature failure repeating: **a sentence that was true when written, then
falsified by a sibling fix in the same revision**, restated in the plan without the hedge. The
ADR's Context is defensible (it explains the pre-condition of the defect); the **plan converts it
into a live implementation constraint and a falsifier**, and both are wrong.

Two concrete harms:
1. **The falsifier is vacuous** — CLAUDE.md forbids exactly this ("Prescribed tests must be
   falsifiable… this repo has shipped six tests that could not fail"). An implementer told to
   demonstrate RED by mutating to "the post-loop idiom" will observe **GREEN**, because the
   post-loop idiom over `*int64` passes both the `nil` row and the `0` row. The mutation cannot
   discriminate, and per the repo's own rule a mutation that cannot discriminate is evidence the
   *claim* is wrong, not the test.
2. **It pushes the implementer off the house idiom.** Told the existing pattern "cannot" work,
   they will invent a bespoke defaulting path in the one function whose other three fields
   (`Wrap`, `InstanceMapper`, `Logger`, `seam.go:50-58`) all use it — a gratuitous divergence in a
   function this delivery is already touching.

**Fix:** In plan §3 phase 1 replace the warning with: *"`ResolveConfig` defaults with the existing
post-loop idiom (`if cfg.MaxBodyBytes == nil`), which is correct **because** the field is a
pointer — an explicit `0` is a non-nil pointer and survives. Executed."* Replace the `nil`-row
falsifier with a real one: ***"the `nil` row fails against an implementation that leaves
`MaxBodyBytes` nil and treats nil as unbounded"*** (i.e. fail-open), which is the mutation that
actually matters and is the one the ADR's "zero value fails CLOSED" claim rests on.

---

### C3 — "the four body shapes" vs "3": the plan's own decision→phase map contradicts §4, the ADR, and phase 2 test 2
**Severity:** Major
**Bundle says:**
- Plan §2 map row: "| parity: all three agree on 413 for **the four body shapes** | 3 | `parity` |"
- Plan §4: "| body shapes that must all yield 413 | **3** — well-formed oversize,
  oversize-with-syntax-error, complete-value-plus-trailing-bytes |"
- Plan §3 phase 2 test 2 table: **3 rows.**
- Plan §3 phase 3: "All three adapters agree on **413** for the body shapes in phase 2 test 2"
- ADR Context table: **3 rows.** Spec §2 [R5] row: **3** shapes.

**My INDEPENDENT net:** counted the rows of every body-shape table in all five files.
**Observed:** every enumeration in the bundle is **3**. The string "four body shapes" appears
**once**, in plan §2 — the table the plan itself calls the authority ("Every sentence of the ADR's
Decision section has a row. **A row with no phase is a defect**").

**Verdict:** CONFIRMED-DEFECT (enumeration rot, sixth consecutive round).

Not cosmetic. §2 is the row an implementer builds phase 3 from, and phase 3 additionally
prescribes **two more** parity cases (a compressed-body case; an explicitly-labelled fiber-only
above-`DefaultBodyLimit` case) plus the unbounded case from test 4 — so an implementer reconciling
"four" against phase 3's prose has **three** different candidate fourth shapes to guess between.
The number is unfalsifiable as written.

**Fix:** Change the §2 row to "**the three body shapes** of phase 2 test 2, plus the unbounded
case (test 4) and a compressed-body case". State the parity case count explicitly in phase 3 so
the two sections cannot drift again.

---

### C4 — ⭐⭐⭐ "3 body shapes" is the wrong SCOPE. A FOURTH shape exists — the UNDER-CAP trailing-bytes body — and on it this delivery makes stdlib and gin DISAGREE, falsifying the ADR's headline
**Severity:** Critical
**Bundle says:**
- Plan §4: "| body shapes that must all yield 413 | **3** — well-formed oversize,
  oversize-with-syntax-error, complete-value-plus-trailing-bytes |"
- ADR Consequences/Positive: "The unbounded-body surface closes on all **39** sites with **one**
  policy and **one** status — and, unlike the previous revision, that sentence is true for
  **malformed** and **trailing-byte** bodies too, not only well-formed ones."
- ADR Decision: "`stdlib` and `gin`: read the body through `http.MaxBytesReader` to completion
  (`io.ReadAll`), then **unmarshal from the resulting buffer**."
- Evidence §7.1 Result 3: "**the gin buffer-and-reset works.** Reading through `MaxBytesReader`
  into a buffer and reassigning `r.Body = io.NopCloser(bytes.NewReader(buf))` decodes cleanly, so
  gin's `ShouldBindJSON` and its validation are preserved rather than bypassed."
- ADR Consequences/Negative enumerates exactly **two** breaks; plan phase 4 repeats "**two
  breaks**".

**Bundle's net:** every body-shape table in the bundle is keyed on **oversize** bodies only. The
under-cap version of the trailing-bytes shape is enumerated nowhere in the five files.

**My INDEPENDENT net:** the bundle's own three shapes vary one axis (size). I varied the *other*
axis — a trailing-bytes body **under** the cap — and ran it through all four idioms: today's
stdlib decoder, the ADR's prescribed `json.Unmarshal(buffer)`, the evidence file's prescribed gin
buffer-and-reset, and fiber's `c.Bind().JSON`. Real `httpcore.StartInput`, real gin, real fiber.

**Observed** (`go test -count=1 -run '^TestZZProbeTrailingUnderCap$' ./transport/http/httpcore/`,
body = `{"def_ref":"kyc:1"} TRAILING-GARBAGE`):
```
=== UNDER-CAP body: {"def_ref":"kyc:1"} TRAILING-GARBAGE
TODAY  stdlib json.Decoder.Decode      err=<nil>
NEW    stdlib json.Unmarshal(buffer)   err=invalid character 'T' after top-level value
NEW    gin buffer-reset ShouldBindJSON err=<nil>
TODAY/NEW fiber c.Bind().JSON          err=bind from body: invalid character 'T' after top-level value
--- PASS
```
**Verdict:** CONFIRMED-DEFECT. Three distinct defects, all invisible to a 3-shape enumeration:

1. **⭐ The ADR and the evidence file prescribe DIFFERENT gin implementations, and they produce
   DIFFERENT STATUSES.** The ADR says "unmarshal from the resulting buffer" (→ `json.Unmarshal`,
   **strict** on trailing data → 400). The evidence file validates buffer-and-**reset** into
   `ShouldBindJSON` (→ `json.Decoder`, **lenient** → 2xx). On this body they measurably disagree:
   400 vs 2xx. The bundle contains no sentence choosing between them, and phase 2's brief cites
   both. Whichever the implementer picks, one bundle document is false.
2. **⭐⭐ If gin follows the evidence file, "one policy, one status" is FALSE after the delivery** —
   stdlib returns 400 and gin returns 2xx for the same body. That is the exact class of divergence
   this whole round was cut to eliminate, re-introduced by the fix, and phase 3's parity suite
   will not catch it because §4 scopes parity to the three **oversize** shapes.
3. **⭐ A THIRD wire break, unenumerated.** If stdlib (and gin) unmarshal from the buffer, requests
   that succeed today with under-cap trailing bytes begin returning **400**. The ADR's break list
   says trailing-byte requests "begin failing **with 413**" — true only above the cap. Below the
   cap they fail with **400**, and that break is caused by the decoder swap, not the cap, so it
   fires **even when `MaxBodyBytes` is explicitly `0` (unbounded)**. The ADR states the opposite:
   "The trailing-byte gap remains in the explicitly-unbounded configuration."

**Bonus, same probe: fiber ALREADY returns an error on this body today** (`bind from body: invalid
character 'T'`) while stdlib and gin return 2xx. So the three adapters diverge on the under-cap
trailing body **today** — a pre-existing parity break that six audit rounds and the existing
parity suite have never seen.

**Fix:**
- Add a **fourth row** to plan §4's body-shape table and to phase 2 test 2: *under-cap complete
  value + trailing bytes*, with the expected status stated explicitly and identically for all
  three adapters.
- **Choose the gin implementation in the ADR** and delete the losing prescription. Recommend
  `json.Unmarshal` from the buffer for stdlib *and* gin (matches fiber's existing strict
  behaviour, makes all three agree, and aligns with ADR-0160's trailing-data posture). Note that
  the evidence file's stated reason for buffer-and-reset — "gin's validation is preserved" — is
  itself unverified and **false for these DTOs**: `grep 'binding:' transport/` returns no hits on
  any DTO, and validation runs via `httpcore.Validate` inside the endpoints
  (`endpoints.go:26,83,101`), not via gin's binder.
- Add the **third break** to the ADR's Negative consequences and to phase 4's CHANGELOG row:
  under-cap trailing-byte requests change from 2xx to 400 **on stdlib**, independently of the cap
  and **regardless of the unbounded opt-out**.
- Add a parity case for the under-cap trailing body, and correct the ADR sentence "the
  trailing-byte gap remains in the explicitly-unbounded configuration" — it remains only for
  bodies the *reader* never truncates, not for the *parse* semantics, which change unconditionally.

---

### C5 — ⭐⭐ BOUNDARY: "the histogram is recorded in each adapter, NOT in `httpcore`" — `httpcore` OWNS every REST instrument, and its fields are unexported. The instrumentation row has no `httpcore` phase and cannot be built without one.
**Severity:** Critical
**Bundle says:**
- ADR Decision/Instrumentation: "`wrkflw_rest_request_body_bytes` is recorded **in each adapter**,
  at the body read. ⚠ **Not in `httpcore`** — that package has **0** decode sites and never sees a
  body." + "A **rejection counter** records how often the cap bites."
- Plan §2 map: "| `wrkflw_rest_request_body_bytes` histogram + rejection counter | **2** |
  `stdlib` \| `gin` \| `fiber` |" — **no `httpcore` row**.
- Plan §2's own rule: "**Every sentence of the ADR's Decision section has a row. A row with no
  phase is a defect** — six of round 3's fifteen Criticals were that one omission, and round 5
  found a whole package (`engine`) in no list at all."
- Plan phase 1 "Symbols:" lists only `CustomizeConfig.MaxBodyBytes` and `ErrRequestBodyTooLarge`.

**Bundle's net:** none. The exclusion of `httpcore` is asserted from decode-site count alone.

**My INDEPENDENT net:** ask where the *instruments* live, not where the *bodies* are read.
```
$ grep -rn --include='*.go' 'wrkflw_rest' . | grep -v _test.go
```
**Observed** — every REST instrument is declared and constructed in `httpcore`:
```
transport/http/httpcore/observability.go:57   "wrkflw_rest_requests_total",
transport/http/httpcore/observability.go:61   "wrkflw_rest_request_duration_seconds",
```
and the holder is closed:
```
observability.go:23  type Instrumentation struct {
observability.go:24      tracer     trace.Tracer
observability.go:25      counter    metric.Int64Counter        // unexported
observability.go:26      histogram  metric.Float64Histogram    // unexported
observability.go:27      propagator propagation.TextMapPropagator
observability.go:40  func NewInstrumentation[R any](cfg CustomizeConfig[R]) *Instrumentation
```
Zero hits for `wrkflw_rest` anywhere under `stdlib/`, `gin/` or `fiber/` — the adapters receive a
`*httpcore.Instrumentation` and call `Observe`; they never construct an instrument.

**Verdict:** CONFIRMED-DEFECT. This is the round-5 shape exactly — a package set derived from the
wrong property.

1. **The exclusion's stated reason is a category error.** "0 decode sites, never sees a body"
   argues about *observation* sites; instrument *ownership* is a different axis. By the same
   argument `httpcore` should not own `wrkflw_rest_request_duration_seconds` — it serves no
   requests either — yet it does, and `Observe` (`observability.go:80-106`) is the shared seam
   built for precisely this.
2. **As scoped, the row is unimplementable.** `Instrumentation`'s fields are **unexported**. No
   adapter can add or reach an instrument. Shipping the histogram + rejection counter requires
   **new fields on `httpcore.Instrumentation` and new construction in
   `httpcore.NewInstrumentation`** — `httpcore` work, in a delivery whose only `httpcore` phase
   (phase 1) lists neither symbol.
3. **The alternative is worse, and phase 2's fan-out guarantees it.** If each adapter mints its own
   instrument, three packages independently call `observability.New(instrumentationScope, …)` and
   register **the same metric name three times**. Phase 2 dispatches **"3 agents in parallel"**,
   one per package, with no shared definition of the metric's name, unit, buckets or attribute
   set — three agents inventing one metric in isolation is a guaranteed divergence, and the plan's
   own fan-out rule ("fan out by Go package") is what makes them unable to coordinate.
4. **No attribute set is specified.** The existing instruments carry `http.method`, `http.route`,
   `http.status_code` (`observability.go:99-103`). The bundle says nothing about what the
   body-bytes histogram or the rejection counter carry, so the three agents will differ there too.
   Note also that `Observe` records **after** `run` returns, while the body read happens *inside*
   `run` — so the new observation cannot piggyback on the existing call and needs its own.

**Fix:** Move instrument **ownership** into phase 1 / `httpcore` and keep the **recording call** in
phase 2 / adapters, matching the existing split:
- Phase 1 `httpcore`: add `bodyBytes metric.Int64Histogram` and `bodyRejections metric.Int64Counter`
  to `Instrumentation`, construct them in `NewInstrumentation`, and add an exported method
  (e.g. `(*Instrumentation).ObserveBodyRead(ctx, method, route string, n int64, rejected bool)`)
  so the adapters have one call and one attribute set. Add both to phase 1's "Symbols:" line.
- Add the `httpcore` row to §2's map, and state the metric's unit (bytes), buckets and attributes
  in the ADR so the three parallel agents cannot diverge.
- Correct the ADR sentence: it is the **observation** that is per-adapter, not the instrument.

---

### C6 — The carry-forward record's cross-slice dependency names the WRONG SLICE, twice; and its consolidated "do not re-derive" block is scoped to THREE bundles when there are FIVE
**Severity:** Major
**Bundle says** (`docs/specs/2026-08-21-untrusted-input-deferred-slices.md`):
- Slice table, line 51: "| **6** | variable-map admission bound | ADR-0191 | **this file, §BOUND** |"
  and line 49: "| **4** | the instance read path aliases and discloses | ADR-0189 | §READ-PATH |"
- §"The one cross-slice dependency, stated so it is not rediscovered", lines 63-66: "**D2 mints
  `service.ErrVariablesTooLarge`** … **slice 4 adds the second sentinel to the existing arm**. The
  dependency runs **slice 1 → slice 4** and never back."
- Line 72: "⚠ **Slice 4 re-opens it**: when `ErrVariablesTooLarge` joins the arm…"
- Line 304: "## What HELD across both audits — **do not re-derive it in any of the three bundles**"
- Line 306: "Consolidated … so **three future deliveries** do not each pay for it"
- Line 308: "**`keywordLocation` is value-free across fifteen schema shapes.** … **(Slice 1 owns
  this.)**"

**My INDEPENDENT net:** `grep -niE 'slice [0-9]'` over the file, each hit resolved against the
slice table at lines 44-51; section anchors verified to exist.

**Observed:** all five section anchors exist (§READ-PATH :78, §SSRF :156, §BOUND :228,
§AT-REST :324, §4XX :386); all three cited commits resolve (`1e527347`, `6cddb7b1`, `85d6bb68`);
next free ADR is genuinely **0187** (186 ADR files, `0001`–`0186`, no gaps at the top).
**Three label defects remain:**

**Verdict:** CONFIRMED-DEFECT ×3 — all anchor rot from the earlier four-slice / three-decision cuts.

1. **"slice 4" should be "slice 6"** (three occurrences: lines 65, 66, 72). The variable-map
   admission bound is **slice 6** (`ADR-0191`, §BOUND) in this file's own table. **Slice 4 is the
   read path**, which does not mint `ErrVariablesTooLarge` and has nothing to do with the 413 arm.
   This is the *one* cross-slice dependency the section exists to preserve, and it points at the
   wrong delivery — so the slice that must inherit ADR-0186's arm-ordering invariant and the
   per-sentinel-message obligation is the one **not** told about it. Stale from the first cut,
   when there were four slices.
2. **"three bundles" / "three future deliveries" should be FIVE** (lines 304, 306). There are five
   deferred slices (2–6). The block is explicitly the list five future bundles are told not to
   re-derive; as written it addresses three of them, and nothing says which three. This is the
   documented recap failure — a summary sentence over-generalising what it compressed, left
   un-renumbered when the cut went 3 → 5.
3. **"(Slice 1 owns this.)" is wrong** (line 308). `keywordLocation`'s value-freedom is the
   **4xx-rendering** premise — §4XX, which is **slice 3**. Slice 1 is body caps and explicitly
   disclaims it: spec §6 Non-goals, "**No change to what any 4xx message says**", and ADR-0186
   mints no rendering. Slice 1 owns nothing here.

**Fix:** Renumber `slice 4` → `slice 6` at lines 65, 66, 72; `three bundles`/`three future
deliveries` → `five` at 304, 306; `(Slice 1 owns this.)` → `(Slice 3 / §4XX owns this.)` at 308.
Then add a one-line invariant to the file: *"every `slice N` reference must resolve against the
table at the top"* — or better, drop ordinal references entirely and cite the **§ANCHOR** names,
which are stable across re-cuts while the ordinals have now rotted through three of them.

---

### C7 — The `cursorcodec.go:50-58` citation's ANCHOR is wrong for the text it quotes — and the comment it should cite REFUTES the ADR's unbounded-configuration claim
**Severity:** Minor (anchor) / reinforces C4 (Critical)
**Bundle says:**
- ADR Context: "`runtime/kernel/cursorcodec.go:50-58` carries a trailing-data guard **with a comment
  explaining that `Decode` "reads only the FIRST JSON value and silently ignores whatever
  follows""** — added by ADR-0160 for exactly this reason, one package over."
- Evidence §7.1 repeats it verbatim: "carries a trailing-data guard **whose comment says** `Decode`
  *"reads only the FIRST JSON value and silently ignores whatever follows"*".

**My INDEPENDENT net:**
```
$ grep -rn --include='*.go' -iE 'FIRST JSON value|silently ignores' .
```
**Observed:**
```
runtime/kernel/cursorcodec.go:28://     JSON value and silently ignores whatever follows. The plain
```
The quoted sentence is in the **doc comment at `:27-33`**. The comment actually sitting at
`:50-58` is a different one ("Decode consumed exactly one JSON value; anything left that is not
whitespace…"). The *guard* is at `:58-63`.

**Verdict:** CONFIRMED-DEFECT (anchor), restated identically in two documents — the documented
"citation's ANCHOR" failure mode. The substance is true: the guard exists and ADR-0160 added it.

⭐ **But the anchor error hid the load-bearing half of that comment.** `cursorcodec.go:29-33`
reads:
```
//     JSON value and silently ignores whatever follows. The plain
//     [json.Unmarshal] this supersedes REJECTS TRAILING BYTES, so without this
//     the "hardened" decoder would be strictly weaker than the code it
//     replaced … Trailing WHITESPACE is legal JSON framing and stays
//     accepted; only a further value or garbage is rejected.
```
This is the repo stating, in the very prior art the ADR cites, that **`json.Unmarshal` rejects
trailing bytes while `json.Decoder.Decode` does not** — independently confirming C4's measurement.
The bundle borrows ADR-0160's *cap* shape and never notices it is also importing ADR-0160's
*strictness* change, which is why the ADR can assert "**The trailing-byte gap remains in the
explicitly-unbounded configuration**" while the prescribed implementation closes it
unconditionally, at 400 rather than 413.

**Fix:** Re-anchor both citations to `runtime/kernel/cursorcodec.go:27-33` (the doc comment) and
`:58-63` (the guard). Then fold C4's correction: the ADR's unbounded-configuration sentence is
false once stdlib unmarshals from a buffer, and ADR-0160's comment is the in-repo proof.

---

### C8 — The carry-forward record cites the executed evidence file ZERO times, though that file holds the probes four of the five deferred decisions rest on
**Severity:** Minor
**Bundle says:**
- Carry-forward header: "It **quotes** the state of five decisions … and adds what their audits say
  each must still answer." / "**Nothing was dropped.**"
- ADR + spec + plan all list `docs/specs/2026-08-21-adr-0186-premise-evidence.md` as a bundle file.

**My INDEPENDENT net:**
```
$ grep -nE 'premise-evidence|premise evidence|§6|6\.1|6\.3|6\.6' \
      docs/specs/2026-08-21-untrusted-input-deferred-slices.md
EXIT=1
```
**Observed:** no hits. Zero cross-references.

**Verdict:** CONFIRMED-DEFECT (handoff gap, not a lost fact).

The evidence file's §1, §2, §3, §4.2, §4.3, §4.6, §4.7, §6.1–§6.6 are executed premises for the
**deferred** decisions (D4 read path, D5 4xx rendering, D6 at-rest, D2 variable bound) — not for
D1. They ship in **this** bundle's commit and are pointed at by **this** bundle's three documents,
while the record that is supposed to carry the deferred work forward never names them. §AT-REST
does restate §6.1's conclusions (9 tables, the MySQL `trigger_` divergence), so nothing is lost
today; but the *derivations* — the probe commands and raw output a future bundle needs in order not
to re-derive them a fifth time — are reachable only by knowing they exist.

Related, same file: §AT-REST lists the rot chain as "2 → 'at least six' → 12 → **'48 columns'** →
and the audit refuted that too", yet evidence **§6.1 is not marked superseded** the way §4.4
prominently is ("⛔ SUPERSEDED BY §6.1. THIS SECTION IS WRONG."). §6.1's own Result 3 does
self-qualify ("a raw count of payload-typed columns is the wrong deliverable"), so this is a
labelling asymmetry rather than a false number — but a future §AT-REST author reading §6.1 alone
sees 48 presented as a live machine-derived result.

**Fix:** Add to each carry-forward section a one-line "**Executed premises:**" pointer naming the
evidence sections it depends on (§AT-REST → §6.1; §4XX → §1, §2, §6.2, §6.3, §6.4, §6.6;
§READ-PATH → §3, §4.2, §4.3; §BOUND → §4.6). Add a supersession banner to §6.1 mirroring §4.4's,
stating that the 48 figure is superseded by the audit's 79-column schema-wide count.

---

## SUMMARY — counting lens, round 6

**8 findings: 3 Critical, 3 Major, 2 Minor.**

| # | severity | one line |
|---|---|---|
| C1 | **Critical** | "negative `MaxBodyBytes` → a construction error at mount" has **no mechanism**: `ResolveConfig`, `CustomizeOption`, `RouteCustomizer.Customize` and all three `Mount`s are error-free; building it breaks the **exported consumer seam**, contradicting the bundle's twice-stated "no new exported interface" and adding an unenumerated **third, source-level** break. Row is also in the wrong package/phase (`httpcore` has no mount). |
| C2 | Major | Phase 1 test 2's `nil`-row **falsifier is vacuous** — executed: the post-loop idiom it names as the mutation is *correct* over `*int64` (nil→1 MiB, explicit `0` preserved). A sentence true of the rejected non-pointer design, restated as a live constraint on the chosen one. |
| C3 | Major | Plan §2's map says "**four** body shapes"; §4, the ADR, and phase 2 test 2 all say **three**. Sixth consecutive round with a rotted enumeration. |
| C4 | **Critical** | ⭐⭐⭐ **"3 body shapes" is the wrong SCOPE.** A fourth exists — the **under-cap** trailing-bytes body — and on it the ADR ("unmarshal from the buffer") and the evidence file ("gin buffer-and-reset") prescribe **different implementations with different statuses**. Executed: stdlib **400**, gin **2xx**, fiber **400**. Falsifies "one policy, one status", adds an unenumerated third wire break that fires **even when unbounded**, and reveals a pre-existing three-way parity split no round has seen. |
| C5 | **Critical** | ⭐⭐ **BOUNDARY:** "the histogram is recorded in each adapter, **not** in `httpcore`" derives the package set from decode-site count. Every REST instrument is **declared in `httpcore`** with **unexported** fields; the row has **no `httpcore` phase**, so it is unimplementable as scoped — and phase 2's three parallel agents would each mint the same metric independently. |
| C6 | Major | Carry-forward: the one cross-slice dependency names **slice 4** three times where its own table says **slice 6**; the consolidated "do not re-derive" block is scoped to **three** bundles when there are **five**; "(Slice 1 owns this.)" attributes a 4xx premise to the body-cap slice. |
| C7 | Minor | `cursorcodec.go:50-58` is the wrong anchor for the comment it quotes (`:27-33`) — and the real comment states `json.Unmarshal` rejects trailing bytes, independently confirming C4. |
| C8 | Minor | The carry-forward record cites the executed evidence file **zero** times; §6.1 lacks the supersession banner §4.4 carries. |

**Verified BUNDLE-CORRECT** (do not re-derive): 39 decode sites = 13+13+13, `httpcore` 0, all in
`groups.go` (AST walk); 36/3 split at the exact anchors, all three on
`POST /admin/instances/{id}/incidents/{incidentID}/resolve`; `ClassifyError`'s **6** ordered arms
and **all six** line anchors; **26 routes = 9 + 15 + 2** on all three adapters; the corrected
`-E` grep works and the literal-pipe form is still broken (positive control run); **no fourth
package reads a request body** (repo-wide AST walk + `\.Body\b` sweep); zero symbol collisions for
all four minted names; next free ADR is genuinely **0187** (186 files, no gaps); all five
carry-forward section anchors and all three cited commits resolve; every fiber vendor citation
(`app.go:585`, `:710`, `req.go:146`, `:92-96`) exact; `ActionableTask` has 6 fields and no `Vars`;
the inherited "12 of 20 Criticals" and "9 survivor×removed pairs, 1 derived" both check out;
lineage numbers 58/12, 38/~13, 63/33, 56/28, 65/20 all match their adjudication records.

⭐ **The pattern held for a sixth round: the arithmetic was right every time.** Every one of the
three Criticals is a **SCOPE** failure, not a count — a boundary the bundle drew and never
re-derived (what can carry an error; which axis body shapes vary on; which package owns an
instrument).

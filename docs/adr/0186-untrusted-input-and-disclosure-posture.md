# 186. Request bodies are capped by default — the read is bounded, the parse is untouched

> ## ▶ ROUND 7 FOLDED 2026-08-21. Owner decision: IMPLEMENT under TDD; code corrects the design.
>
> ⚠⚠ **Seven audits, 56–65 findings every time, scope down twelve-fold.** Round 7 (57 findings,
> 14 Critical) landed the trend as its own finding: **the finding rate is a property of the
> PROCESS, not the bundle.** Splitting was exhausted at round 6, stripping at round 7, and the last
> two rounds' Criticals were facts about `bind.go`, `seam.go`, `parity_test.go` and
> `observability.go` that **only running the code surfaces**.
> ⇒ **Owner decision: stop designing on paper.** Round 7's fixes are folded below; implementation
> proceeds strictly RED-first with the 57 findings as the test list, per CLAUDE.md rule #11
> (*"expect implementation to correct the design, and amend the ADR in the same bundle with the
> measurement"*). `docs/plans/sweep-evidence/audit5-0186-adjudication.md`.
>
> **What round 7 changed here** — each was executed, none was found by reading:
> 1. ⛔⛔ **fiber's cap was BYPASSED by `Content-Encoding: gzip`.** `bind.go:309` parses
>    `ctx.Body()` — the **decompressed** body — so a `BodyRaw()`-only check bounds the wrong thing.
>    Measured: a **3,121-byte** request parsed **3,145,761 bytes** and returned **200**.
>    ⇒ **fiber bounds BOTH.** ⚠ The exact inverse of the defect four rounds were spent on.
> 2. **`WithMaxBodyBytes(0)` did not compile** — `CustomizeOption[R]` cannot infer `R`. ⇒ three
>    **non-generic per-adapter aliases**, the repo's own existing pattern (`WithBasePath`).
> 3. **`Mount` reaches only 6 of 13 decode sites per adapter** — 21 of 39 sit behind `AdminRoutes`,
>    including all three discarding sites, and `MountHealth` forwards no options.
> 4. **"The parity suite structurally cannot see admin routes" was FALSE** (asserted in 4 places,
>    inherited, never checked) — `parity_test.go:663` mounts them by hand today.
> 5. **The 413's `ErrorBody` was never specified**, and two bundle files contradicted each other.
> 6. **"A consumer cannot measure" was FALSE** — `wrkflw_rest_requests_total{http.status_code}`
>    already counts every 413. ⚠ **Third time this lineage claimed a gap the repo had filled.**
> 7. **The slowloris residual used the one fixture where old and new behave identically.**
>
> ⚠ **Previously: STRIPPED after SIX failed audits.**
>
> Six decisions → two failed audits → three decisions → a third → **one decision → a fourth
> (61 findings, 24 Critical)**. `docs/plans/sweep-evidence/audit4-0186-adjudication.md`.
>
> ⚠⚠ **Round 6 is the control experiment: it failed at a bundle size of ONE decision, so bundle
> size is no longer the variable and splitting is exhausted.** All three lenses that commented
> agreed **every Critical was a SCOPE-BOUNDARY failure** — a boundary the bundle asserted and never
> derived. And the execution lens named the bias:
>
> > **"The bundle's probes are narrow in a consistent direction: toward the fixture that
> > demonstrates the fix."**
>
> **Owner decision: strip to the minimum cap.** The four findings that three or four lenses reached
> independently were all about **ancillary mechanisms, not the cap**. They are **deleted**, not
> redesigned:
> | deleted | why |
> |---|---|
> | mount-time construction error for a negative cap | no return channel exists — `ResolveConfig`, all 15 `Customize` methods and all 6 `Mount`s return nothing (4 lenses) |
> | `wrkflw_rest_request_body_bytes` histogram + rejection counter | `Instrumentation`'s fields are unexported and only `httpcore` builds instruments; the bundle excluded `httpcore` **by name** (4 lenses) |
> | fiber mount-time WARN | fires 3–4× per mount, and never for the group holding the discarding sites (ADR-0095) |
> | the `*int64` tri-state | `action/httpcall` **already ships the convention** — plain `int64`, non-positive disables, default in the constructor |
>
> **And the central mechanism changed, which deletes three more findings by construction:**
> ⭐⭐ **Cap the READ; leave each adapter's PARSE exactly as it is.** The previous revision
> prescribed "unmarshal from the buffer", which silently chose `json.Unmarshal` (strict) over the
> adapters' `json.Decoder` (lenient) and made stdlib and gin **disagree on under-cap trailing
> bytes** (3 lenses, executed). Feeding the buffer to *today's* decoder caps the whole request and
> changes nothing under the cap. **Executed** (Evidence §8).
>
> ⚠ Two mechanisms in this repo already solved parts of this and were missed:
> `runtime/kernel/cursorcodec.go:27-28` (trailing data) and `action/httpcall.go:186-194` (the cap
> itself). **"Search the repo for an existing convention" is now a step in the plan.**
>
> ⚠ **This bundle has been audited SEVEN times and never passed.** It proceeds to implementation by
> explicit owner decision under rule #11, **not** because it passed. Implementation is expected to
> correct it further, and every correction amends this record with the measurement.

- Status: **Accepted for implementation** (7 audits, never passed; owner decision 2026-08-21 to let code correct the design — rule #11)
- Date: 2026-08-20, stripped 2026-08-21
- Relates to: **ADR-0095** (admin routes are absent from `Mount`, so a consumer must pass the cap
  to `AdminRoutes` too — ⚠ but **NOT** "the parity suite cannot see them": `parity_test.go:663`
  mounts `AdminRoutes` by hand on all three adapters today, and that false premise was asserted in
  four places before round 7 refuted it), **ADR-0160** (the trailing-data guard whose lesson this
  borrows — `runtime/kernel/cursorcodec.go:27-28`).
- Backlog: **98** only. ⚠ 104, 100, 101, 54, 65, 99 belong to the five deferred deliveries in
  `docs/specs/2026-08-21-untrusted-input-deferred-slices.md`.

## Context

### No body cap anywhere — and three sites cannot report one

Re-derived by **AST walk** (not grep), and confirmed unchanged across four audit rounds:
`stdlib` **13** `json.NewDecoder`, `gin` **13** `ShouldBindJSON`, `fiber` **13** `c.Bind().JSON`,
`httpcore` **0** — **39 sites**, all in each package's `groups.go`.
`grep -rnE "MaxBytesReader|BodyLimit" transport/` exits 1.

⚠ **36 propagate the decode error; 3 discard it.** `stdlib/groups.go:238`, `gin/groups.go:265`,
`fiber/groups.go:255` are `_ = <decode>(&in)`, all on the optional-body route
`POST /admin/instances/{id}/incidents/{incidentID}/resolve`. A cap installed there fails, the
failure is assigned to `_`, and the handler returns **2xx** — a security control silently
unenforced.

### ⚠⚠ Capping DURING the parse does not cap the request

**Executed.** `json.Decoder.Decode` reads only the **first JSON value**; everything after it is
never read, so `MaxBytesReader` never trips. At a 1 MiB cap, a **complete JSON value followed by
3 MiB of trailing bytes** returns `err == nil` and **2xx** on stdlib and gin.

⚠ This repo already knew: `runtime/kernel/cursorcodec.go:27-28` carries a trailing-data guard whose
comment says `Decode` *"reads only the FIRST JSON value and silently ignores whatever follows"* —
added by ADR-0160, one package over.

### ⚠ What the previous revision got wrong trying to fix it

**Executed, three lenses.** Prescribing *"unmarshal from the resulting buffer"* silently chose
`json.Unmarshal` (**strict** — rejects trailing data) over the adapters' `json.Decoder`
(**lenient** — ignores it). Body `{"def_ref":"a:1"} zz` at a 64 B cap:

| | today | under "unmarshal from the buffer" |
|---|---|---|
| stdlib | 200 | **400** |
| gin (`ShouldBindJSON` is a Decoder) | 200 | **200** |

So the headline *"one policy, one status"* was false **after** the change, for every under-cap
trailing-byte body — and the stdlib 200→400 break was unlisted.

### The existing in-repo convention for exactly this problem

`action/httpcall` already caps a body: `io.ReadAll(io.LimitReader(r, max+1))` (`httpcall.go:194`),
a plain **`int64`**, **`max <= 0` disables** (`:191`), default applied in the constructor (`:214`),
documented in six places. ⚠ And `httpcore.ResolveConfig` (`seam.go:39-58`) sets its defaults **in
the struct literal before applying opts**, with post-loop guards only for nil-able fields — so a
plain `int64` default survives an explicit `0`. **No tri-state is needed.**

## Decision

### Request bodies are capped by default; the READ is bounded and the PARSE is untouched

**Configuration — the repo's existing convention, not a new one.**

- `httpcore.CustomizeConfig.MaxBodyBytes int64`, set by `httpcore.WithMaxBodyBytes[R any](n int64)`.
- ⚠⚠ **Plus a NON-GENERIC alias in each adapter**, because the generic form **does not compile at a
  call site**: `R` appears only in `CustomizeOption[R]`'s result type, so Go cannot infer it
  (`cannot infer R`). This is the repo's existing pattern, not a new one — `httpcore.WithBasePath`
  is generic and `stdlib/options.go:12`, `gin/options.go:26`, `fiber/options.go:23` each ship a
  concrete alias. ⇒ **three new symbols**: `stdlib.WithMaxBodyBytes`, `gin.WithMaxBodyBytes`,
  `fiber.WithMaxBodyBytes`. Every consumer-facing example uses the adapter form.
- Default **1 MiB**, applied in `ResolveConfig`'s struct literal alongside `Logger` and `Wrap`.
- **`n <= 0` disables the cap** — matching `action/httpcall.WithMaxResponseSize` exactly, so the
  library has one convention for "bound a body" rather than two.
- ⚠ **No pointer, no tri-state, and no mount-time validation.** A negative value means the same as
  zero: disabled. There is no channel to report a construction error (`ResolveConfig`, all 15
  `Customize` methods and all 6 `Mount`/`MountHealth` functions return nothing), and inventing one
  would be a new exported contract this delivery explicitly refuses.

**Enforcement — bound the read, then hand the bytes to today's decoder.**

- `stdlib` / `gin`, **when capped**: `io.ReadAll(http.MaxBytesReader(w, body, n))`, then run
  **the site's existing decode idiom over the buffer** — `json.NewDecoder(bytes.NewReader(buf))`
  for stdlib, and for gin reassign `gc.Request.Body` to the buffer before `ShouldBindJSON` so its
  binding and validation are unchanged.
  ⚠⚠ **Do NOT substitute `json.Unmarshal`.** It is strict about trailing data where the adapters
  are lenient, and swapping it in is a wire break on every under-cap trailing-byte body.
- `stdlib` / `gin`, **when disabled** (`n <= 0`): do not install the wrapper; decode straight from
  the wire, exactly as today. An unbounded `io.ReadAll` is itself a memory-exhaustion primitive.
- `fiber`: **two** pre-checks before `c.Bind().JSON`, and **both** are required:
  - `len(c.BodyRaw())` — the **wire** body (`req.go:92-96`), which bounds the transfer;
  - `len(c.Body())` — the **decompressed** body (`req.go:146`), which is what
    `bind.go:309` actually parses.
  ⚠⚠ **Checking only `BodyRaw()` leaves the cap BYPASSED by `Content-Encoding: gzip`.** Measured on
  a real fiber app at a 1 MiB cap: a **3,121-byte** request parsed **3,145,761 bytes** into a
  variables map and returned **200**. ⚠ And checking only `c.Body()` fails the amplification case
  the other way — a 63.7 KiB gzip expanding past fiber's own limit returns `len == 33` because
  fiber's bounded gunzip wrote an error string. **Neither check alone is sufficient; that is why
  there are two.**
  ⚠ `c.Body()` costs nothing extra: `Bind().JSON` calls it regardless.

**⭐ What this buys, executed** (Evidence §8): over-cap bodies are rejected **whatever their
shape** — well-formed, malformed, or a complete value plus trailing bytes — and **under-cap
behaviour is byte-for-byte unchanged**, including trailing bytes. The strict/lenient question does
not arise, because no decoder is replaced.

**Oversize is a 413, and the discriminator is kept.**

- a new **`httpcore.ErrRequestBodyTooLarge`**. ⚠ **Not `ErrBodyTooLarge`** —
  `action/httpcall.ErrBodyTooLarge` exists, means an *outbound response* exceeded 10 MiB, and is
  correctly a **500**. A test pins that it still is.
- ⚠⚠ **Oversize is identified by `errors.As(err, new(*http.MaxBytesError))`, NOT by "the read
  returned an error".** Executed: an over-declared `Content-Length` yields `unexpected EOF` with
  `errors.As` **false**, so treating any read error as oversize would ship **every aborted or
  truncated upload as a 413**. Both stdlib and gin surface the **bare** `*http.MaxBytesError`, so
  the check is cheap. Fiber's pre-check produces the sentinel directly.
- the adapter returns the **bare** sentinel; `ClassifyError` maps it → **413**.
- ⚠⚠ **The 413's `ErrorBody` is specified, because every other arm renders `err.Error()` and this
  one must not inherit that by accident**: `ErrorBody{Error: "request_too_large", Message: "request
  body exceeds the configured limit"}`. Static, no `err.Error()`, and it deliberately **does not
  name the limit** — the limit is deployment configuration, and echoing it tells an attacker
  exactly what to stay under. ⚠ An earlier revision claimed this delivery *"ships no 413 message
  text"*; that was false, because `ClassifyError` sets `Message` on every arm.

⚠⚠ **The oversize error must NOT carry `ErrBadInput`, or it ships as 400.** Executed: an error
wrapping both classifies **400**, because the switch is ordered — 404 `:28`, 403 `:32`, 409 `:34`,
**400 `:36-50`**, 422 `:51`, default 500 `:57`. So the **413 arm is placed before the 400 arm**,
with a comment saying why.

⚠ **The 413 arm sits immediately before the 400 arm, so 404/403/409 still win over it.**
**Amended during implementation (rule #11).** The design said only *"before the 400 arm"* and left
the position relative to the earlier arms unstated. The implemented order is deliberate: an oversize
body is detected at the decode site, **before** any service call, so it cannot in practice co-match
`ErrInstanceNotFound`, `ErrNotAuthorized` or `ErrConcurrentUpdate`. The residual is theoretical
today and is recorded rather than left to be rediscovered.

⚠ **`ClassifyError`'s arms are order-dependent by construction.** Any future arm — the deferred 4xx
delivery's, the deferred variable bound's `ErrVariablesTooLarge`, ADR-0185's 401/503 — must state
its position and carry a test asserting an error matching two arms resolves to the intended one.
This sentence exists so the lesson outlives the bundle that learned it.

⚠⚠ **The cap is PER-ROUTE-GROUP, and `Mount` does not cover the whole surface.** Mapping all 39
decode sites to their owning `Customize` method: **6 reachable via `Mount`** per adapter,
**7 via `AdminRoutes`** — **21 of 39 repo-wide, including all three discarding sites** — and
`MountHealth` forwards no options at all. So `Mount(mux, svc, WithMaxBodyBytes(0))` leaves 21 of 39
sites at the default. **A consumer must pass the option to every group they mount**, exactly as
`examples/production_wiring:264,274` already repeats an option across two muxes. `SECURITY.md`
states this and shows both calls; it is a documented property of the existing seam, not a new one.

⚠ **The three discarding sites gain an oversize path**, using the same `errors.As` check; every
other decode error stays ignored, because the body there is genuinely optional. These are on an
**admin** route that ADR-0095 keeps out of `Mount`, so the parity suite structurally cannot see
them — the per-adapter test must name the route.

### What this delivery deliberately does NOT do

Each of these was in a previous revision and is removed rather than deferred silently:

- **No instrumentation.** No histogram, no rejection counter. `Instrumentation`'s fields are
  unexported and only `httpcore` builds instruments from `cfg.MeterProvider`.
  ⚠⚠ **Consequence, CORRECTED — the earlier statement was false.** A consumer **can** already count
  rejections: `wrkflw_rest_requests_total{http.status_code}` exists (`observability.go:36-57`) and
  all three `observe` wrappers already feed it the handler status, so **every 413 is counted with
  no new code**. What is genuinely absent is the **body-size distribution**, i.e. the ability to
  choose a cap from data rather than by judgement. ⚠ *Third* time this lineage asserted a gap the
  repo had already filled.
- **No mount-time WARN on fiber.** Executed: `ResolveConfig` runs **5× per adapter**, so a WARN in
  `Customize` fires 3–4× per documented mount, and one in `Mount` never fires for the admin group
  at all. ⚠ **The divergence is real and is documented instead**: above `fiber.Config.BodyLimit`
  (default 4 MiB) the route group is never reached and the client gets fasthttp's `text/plain`
  `Request Entity Too Large` with no `ErrorBody`. ⚠ `(*fiber.App).Config()` **is** exported
  (`app.go:1233`) — an earlier `ASSUMPTION (unverified)` that it was unreachable is **refuted** —
  but reaching it requires a `*fiber.App`, which a mounted `*fiber.Group` is not.
- **No mount-time validation of the cap value.** See above.
- **No change to any 4xx message**, no correlation id, no logging change. Those are the deferred
  4xx delivery. ⚠ **Consequence, stated: a 413 carries no correlation id and writes no log record**
  — `writeErr` logs only at `status >= 500` today and this delivery does not change it.

### The bound is on SIZE, not on TIME

⚠⚠ **Stated because a previous revision missed it entirely.** Reading to EOF under a cap replaces
*return-on-first-value* with *wait-for-EOF*. **Executed against a real `http.Server`: a chunked
request with no terminating chunk holds the handler indefinitely.** `MaxBytesReader` bounds bytes;
it does not bound time.

- **The consumer owns `ReadTimeout`.** ⚠ Phase 4 **adds** this to `SECURITY.md`; it is not there
  today. ⚠⚠ **And the discriminating fixture matters, because an earlier statement of this residual
  used one that could not discriminate.** *Chunked with no terminator* hangs **today as well**
  (net/http drains post-handler). The case this delivery **creates** is: `Content-Length: 400000`
  (above net/http's 256 KiB drain tolerance, below the cap) + a complete JSON value + a slow
  dribble — **today: 0 s, 50/50 handlers return. Under read-to-EOF: never returns, 0/50,
  goroutines +150.** Measured. `ReadTimeout` fixes it (1.001 s) and **none** of the three
  `examples/` sets it — phase 4 fixes those too, since we own them.
- We do not set it for them: the `http.Server` belongs to the consumer, and this library is mounted
  into it. ⚠ This is a *documented residual*, not a fix.
- ⚠ **Peak memory is NOT `MaxBodyBytes × in-flight`; that formula was wrong on every adapter.**
  Measured: **stdlib/gin ≈ 2.12 × the cap** per request (`io.ReadAll` growth doubling), **including
  for a request that is ultimately rejected**; **fiber ≈ `fiber.Config.BodyLimit`** (4 MiB by
  default), in which `MaxBodyBytes` does not appear at all, because fasthttp has already read and
  limited the body before the handler runs. Nothing bounds concurrency. Documented per adapter.

## Implementation notes (rule #11 — what building it corrected)

**Phase 1 (`httpcore`) landed 2026-08-21.** Three things the design under-specified, reported by the
implementer rather than silently fixed:

1. ⭐ **The `action/httpcall.ErrBodyTooLarge` → 500 row is a PIN, not evidence.** It **passed in the
   RED run** and under both mutations, because nothing in phase 1 touches the default arm. Its real
   falsifier is the *naming collision* — had the new sentinel been called `ErrBodyTooLarge`, or been
   added to the 413 arm's `errors.Is` list. Kept and **labelled in its comment as a
   near-miss-neighbour pin**, because CLAUDE.md treats an unfalsifiable test as a defect and the
   honest response is to say which class it belongs to.
2. ⚠ **A new import edge now exists**: `transport/http/httpcore` **(test)** → `action/httpcall`.
   It did not exist at HEAD (verified). Consequence: **mutating `action/httpcall` recompiles
   `httpcore`'s test binary**, so the deferred SSRF delivery (§SSRF) must not run a mutation
   ablation concurrently with any re-verification of this test.
3. **The 413 arm's position relative to 404/403/409** was unstated — see the Decision section above.

⚠ **Two test rows were added beyond the brief, and both failed under real mutation**: the
wrapped-oversize disclosure row (the only row that catches `err.Error()` leaking the configured
limit — measured leak: `"…body capped at 1048576 bytes…"`), and a last-option-wins row for the
shared-default-list-overridden-per-mount shape.

**Phase 2 (the three adapters) landed 2026-08-21.** Six further corrections, each **measured** by
the implementer and none found by reading:

4. ⭐⭐ **The stated reason for returning the BARE sentinel is REFUTED.** The design said wrapping it
   in `ErrBadInput` "would classify it 400". Executed as a mutation: it still returns **413**, with
   every test green — because **phase 1 hoisted the 413 arm above the 400 arm**, which is exactly
   the fix that falsified the justification. ⚠⚠ *A claim true when written, falsified by a sibling
   fix in the same delivery* — the shape this lineage's fourth audit named, reappearing during
   implementation where no document review could reach it.
   ⇒ **The bare sentinel is still correct**, for a better reason: it makes classification
   independent of arm ordering rather than load-bearing on it. ⚠ **Consequence: no adapter-level
   test can distinguish wrapped from bare.** The rule is unpinned in all three adapters; only
   `httpcore`'s arm-ordering test pins it. Recorded in `gin/bodycap.go`'s doc comment.
5. ⭐⭐ **The fiber falsifier prescribed for a `Body()`-only check does NOT falsify it.** With no
   `Content-Encoding`, `Body()` returns bytes identical to `BodyRaw()` (`req.go:160-163`), so a
   plain oversize body passes under both. **Measured** under the decompressed-only mutation: the row
   still passed. The row that actually discriminates is a **gzip body whose wire exceeds the cap
   AND whose decompression exceeds fiber's own 4 MiB `BodyLimit`**, so `Body()` degrades to the
   33-byte error string — measured `BodyRaw()=1,543,869`, `Body()=33`.
   ⚠ **Without it the `BodyRaw()` check is dead weight nobody can justify.**
6. ⚠ **"`c.Body()` costs nothing extra" is FALSE on the compressed path.** `Body()` restores the
   raw body after each decode (`req.go:181-184`), so it **re-decompresses on every call** — the
   pre-check and the binder decompress **twice**. Bounded (the second only runs for a body already
   proven within the cap) but not free. Documented rather than assumed away.
7. ⭐⭐ **The prescribed tests left 11 of 13 sites unverified** and dropped fiber's coverage to
   **81.59 %**, below the 85 % floor. *"All 12 propagating sites are bounded"* was **prose only** —
   this lineage's recurring enumeration failure, caught by an implementer rather than an auditor.
   ⇒ `TestEveryDecodeSiteIsBounded`, a table over all **13** routes with a `require.Len(cases, 13)`
   count assertion. Coverage **86.6 %**.
8. ⚠⚠ **The aborted-upload premise does not reproduce the cheap way, and the cheap way is
   VACUOUS.** The `unexpected EOF` / `errors.As == false` fact holds only against a **real
   `net/http` server**. With `httptest.NewRequest` + a hand-set `ContentLength`, `io.ReadAll`
   returns **`err = nil`** — and a test written that way **passes against the broken implementation
   too**. Both stdlib and gin use `httptest.NewServer` + raw `net.Dial` + `CloseWrite`; verified by
   the controller across both packages.
9. ⚠ **The over-cap trailing-bytes falsifier depends on the FIXTURE, not only the implementation.**
   *"Fails against any implementation that caps during the parse"* holds only while the **leading
   complete value fits inside the cap** — `json.Decoder` scans its buffer for a complete value
   before consulting the stored read error. Pinned as `testCap = 64` against a 44-byte leading
   value, and documented in the test.
10. **A third read-error case was unstated**: a read error that is *not* `*http.MaxBytesError`.
    Resolved as *drop it and decode the buffered prefix*, which reproduces today byte-for-byte —
    `json.Decoder` converts a truncated stream's `io.EOF` to `unexpected EOF` itself, so the 400
    message is identical, and a complete value that arrived before the abort still decodes.
11. **Residual:** on the optional-body admin route with a **truncated** body, `gc.Request.ContentLength`
    stays at the declared value while the buffer holds fewer bytes. Nothing in the repo reads it
    (grep-verified); consumer middleware running after the handler would see the inconsistency.
12. **`stdlib/groups.go` no longer imports `encoding/json`** — a future "find the decode sites" grep
    keyed on that import returns nothing for that package.

**Phase 3 (`parity`) landed 2026-08-21** — 13 cases, **all GREEN on first run** because every one
is a *pin* over phase 1/2 behaviour, each discharged by a distinct mutation (8 mutations, all
discriminating). Two more corrections:

13. ⭐⭐ **The three adapters were ALREADY divergent on under-cap trailing bytes, before this
    delivery, and nothing pinned it.** Measured: `{…}` + 64 trailing `x` under a 4096-byte cap →
    **stdlib 201, gin 201, fiber 400**. stdlib and gin decode with `json.Decoder`, which stops at
    the first complete value; **fiber's `Bind().JSON` calls `json.Unmarshal`, which rejects trailing
    content**. ⚠ The cap neither causes nor changes this.
    ⇒ **This sharpens the "do not substitute `json.Unmarshal`" rule**: it protects stdlib and gin,
    and fiber was **already** strict. The delivery's *"under-cap behaviour is byte-for-byte
    unchanged"* holds **per adapter**, which is the claim that matters — but the design discussion
    framed the three as uniformly lenient, and they never were. Now pinned by a parity case, so it
    cannot be discovered in production and misdiagnosed as a cap bug. **Whether fiber's stricter
    behaviour is the intended one is a question this delivery does not answer** — opened as a
    backlog item.
14. ⭐ **Raising `fiber.Config.BodyLimit` above the adapter cap restores FULL parity** — measured:
    fiber then answers **413 with the identical envelope**. The known divergence is therefore a
    *configuration* property, not an inherent one, which is a stronger and more useful statement
    than "fiber diverges".

⚠ **Two pre-existing documentation defects were found and fixed in passing**, both Premise-Discipline
shapes: the parity package doc claimed *"**All** cases compare both the HTTP status and the
normalised body"* when four kinds of exception already existed (corrected to "Most", with the
exceptions enumerated); and *"byte-for-byte identical"* is really about the **decoded document** —
stdlib writes via `json.NewEncoder`, which appends `"\n"`, so every JSON response has always
differed from gin's and fiber's by exactly that newline. The guarantee holds as implemented; the
phrase was imprecise.

**The Delivery Gate's `/code-review` found 4 issues (2 MEDIUM, 2 LOW), all executed. All fixed.**
Two were genuine regressions this delivery introduced, and both had been *documented* rather than
*mitigated* — which the review correctly refused to accept:

15. ⭐⭐ **The cap turned a fast-returning request into an indefinite handler hold, ON BY DEFAULT.**
    Reading to completion before parsing replaced `json.Decoder`'s stop-at-first-value.
    **Measured**: `Content-Length: 400000`, a complete 41-byte value, then a stall — **201 in 0 s
    with the cap off, no response at all with it on.** ⚠⚠ **`SECURITY.md` described this residual
    and nothing mitigated it. A security feature that trades bounded memory for unbounded goroutine
    holds is not something to merely disclose.**
    ⇒ **`BodyReadTimeout`, default 30 s**, armed only when the cap is active, `d <= 0` disables,
    with `stdlib`/`gin` aliases and no `fiber` one. ⭐ **30 s because `action/httpcall.go:209`
    already uses it** — the "search the repo before inventing" step applied *before* the fact for
    once, and the citation was verified rather than trusted. All three `examples/` now set
    `ReadTimeout` too.
    ⚠ **Interaction found while implementing, not in review**: arming `now+d` **overwrites** the
    whole-request deadline `net/http` derives from `Server.ReadTimeout` (`server.go:1041`), so a
    consumer with a *shorter* `ReadTimeout` has it silently extended for the body read. Documented
    on the field, both options, both helpers and the examples.
    ⚠⚠ **And a test written for this was VACUOUS and was DELETED rather than shipped.** The brief
    asked for the deadline to be cleared so it "does not bleed into the handler"; the bleed test
    passed against a no-op clear. Escalated: a deadline left **expired an hour ago** *still* let the
    next keep-alive request answer 201, because `net/http`'s serve loop re-arms unconditionally
    before every request (`server.go:2093-2110`). The clear is kept as cheap hygiene and the comment
    now says exactly that, with the measurement. **Seventh test-that-cannot-fail avoided by an
    implementer noticing it themselves.**
16. ⭐ **`gin` answered 400 where `stdlib` answered 201, and it was a regression against gin's own
    pre-change behaviour.** For a body truncated *after* a complete JSON value, `io.ReadAll` returns
    `io.ErrUnexpectedEOF`; stdlib drops it and decodes the prefix, gin wrapped it in `ErrBadInput`.
    **Measured on real sockets:** stdlib **201**, gin+cap **400**, gin with the cap disabled **201**.
    ⚠ This directly contradicted this record's own CHANGELOG line — *"no request that fits the cap
    changes status or body"* — which is **now true**. ⚠ Both packages' existing tests missed it
    because they truncate **mid-value**, where the adapters agree.
17. ⭐⭐ **`fiber` was overwriting its own framework's correct 413 with a 400**, and the review's
    stated *mechanism* was wrong. `Body()` **already calls `SendStatus(413)`** on the
    decompression-limit path (`req.go:191`) — the fact the finding missed, and the one that made a
    clean fix possible without importing `fasthttp` or matching error strings. ⚠ The finding blamed
    `SetBodyRaw` restoring the compressed body; that write-back is guarded by
    `if i > 0 && decodesRealized > 0` (`req.go:132`) and **never runs for a single
    `Content-Encoding`** — measured, `BodyRaw()` is still gzip after `Body()` returns. **The
    conclusion held; the reason did not.** The fix reads a **status delta**, so it fires only when
    `c.Body()` itself moved the response to 413; corrupt, truncated, `compress` and unknown-encoding
    bodies all keep their existing 400. **Measured: 32.17 MiB → 16.15 MiB allocated.**
18. **A test comment asserted a "real production shape" that no production path produces.** Re-derived
    during the fix: **15** sites pass the sentinel bare, **zero** wrap it. The test is valuable — it
    pins the arm-ordering invariant — but the justification was false and is reworded.
    ⚠ The implementer's own first draft of the corrected count said "thirteen" and they caught it
    before it landed.

⚠ **Residual, unfixed and stated:** every compressed body *under* the cap is still decompressed
twice on `fiber` (once by the size pre-check, once by the binder). Removing it means feeding the
decoded bytes to the binder across all 13 sites and would change bind error text. **Backlog item.**

⭐ **Both prescribed falsifiers were observed, not asserted.** Moving the default into
`ResolveConfig`'s post-loop guard: the default row **passes** and the explicit-`0` row **fails** —
exactly the asymmetry that makes the `0` row load-bearing. Moving the 413 arm below the 400 arm:
the both-sentinels row returns 400 and the wrapped row leaks the limit again, while **the
bare-sentinel row stays green** — which is precisely why the both-sentinels row had to exist.

## Consequences

### Positive

- The unbounded-body surface closes on all **39** sites — and, unlike every previous revision, the
  claim survives its own fixtures: **over-cap bodies are rejected whatever their shape**, including
  the complete-value-plus-trailing-bytes case that returns **2xx** today.
- **Under-cap behaviour is byte-for-byte unchanged**, because no decoder is replaced. The
  strict/lenient divergence that failed the last round cannot arise.
- **The three sites that could not report a violation are named and given an error path.**
- **One convention for bounding a body across the library**, matching `action/httpcall` rather than
  inventing a second.
- **Aborted and truncated uploads keep today's behaviour**, because oversize is identified by type
  rather than by "an error happened".
- The delivery is **one decision, four packages, no new exported interface, no new metric, no new
  mount-time behaviour**.

### Negative / costs

- **BREAKING (wire)**: a **new 413** on routes that previously returned 400, 500 or a spurious
  **2xx**; and requests that succeed today via the trailing-byte gap now fail. Clients with an
  exhaustive status switch break.
- **A consumer cannot measure before the cap bites.** No histogram ships. The lever is
  `WithMaxBodyBytes(0)`.
- **`MountGroups` consumers get the default cap.** `MountGroups(r, groups...)` calls
  `Customize(r)` with **no options** (`seam.go:108`), so the 1 MiB default applies. ⚠ Its own godoc
  already names the escape — *"Groups needing distinct base paths or middleware call `Customize`
  directly with the relevant options"* — and `SECURITY.md` repeats it. Verified from source.
- **A 413 carries no correlation id and produces no log record.** Real gap, created by the split.
- **Fiber diverges above `fiber.Config.BodyLimit`**: framework plain-text 413, no `ErrorBody`, no
  log, and no WARN to tell the consumer. Documented only.
- **Slowloris is not addressed**; the consumer owns `ReadTimeout`. Peak memory is cap × in-flight.
- **1 MiB remains a judgement call, explicitly `ASSUMPTION (unverified)`.**
- ⚠ **This bounds a REQUEST, not an instance.** Per-instance variable accumulation is unbounded and
  is the deferred variable-bound delivery — five individually-compliant signal deliveries reach
  789 KiB in that delivery's own evidence. Stated so the Positive above is not read as more than it
  says.

### Neutral / follow-ups opened

- **New item: body-size observability**, once a way to build an instrument outside `httpcore`
  exists (or `Instrumentation` grows an exported constructor).
- **New item: a mount-time diagnostic channel** — several desirable warnings (fiber's body limit,
  a nonsensical cap) have nowhere to go because no mount function returns anything.
- **New item: the fiber decompressed-size bound**, which needs `fiber.Config.BodyLimit` and a
  `*fiber.App` a mounted group does not have.
- Backlog **104, 100, 101, 54, 65, 99** stay open in the five deferred deliveries.

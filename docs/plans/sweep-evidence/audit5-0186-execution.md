# Audit 5 (round 7) — ADR-0186 stripped bundle — EXECUTION lens

Worktree detached at bundle commit `27ff5841`. Step 0: all five bundle files present. ✅

Findings appended per finding, before the next probe.

## Summary — 14 findings

| # | severity | one line |
|---|---|---|
| E1 | **CRITICAL** | *"aborted and truncated uploads keep today's behaviour"* is false — an over-declared `Content-Length` goes **200 → 400**. A **third** wire break; the prescribed test asserts only `!= 413` and passes anyway. |
| E2 | bundle-correct | gin's `ShouldBindJSON` through a reassigned `gc.Request.Body` is byte-for-byte identical across 14 fixtures. The one live `ASSUMPTION (unverified)` is **discharged** — by this probe, not by the prescribed test. |
| E3 | **CRITICAL** | the test prescribed to discharge it **cannot be written**: **zero `binding:` tags** exist in the repo, so gin's validator is never engaged; written against `httpcore.Validate` instead, it **cannot fail**. |
| E4 | **CRITICAL** | ⭐⭐⭐ **the cap is fully bypassed on fiber by `Content-Encoding: gzip`.** `Bind().JSON` reads `c.Body()` (decompressed): a **3121-byte** request parses **3 145 761 bytes** and returns **200**. *"over-cap bodies are rejected whatever their shape"* and *"peak memory is `MaxBodyBytes × in-flight`"* are both false on 13 of the 39 sites. |
| E5 | MAJOR | the `len == 33` claim is numerically right and **causally wrong** — `Body()` returns 33 only when decompression exceeds `BodyLimit`, and **writes a 413 response from inside the getter**; live at all 13 fiber sites today. |
| E6 | **CRITICAL** | plan test 8's falsifier is **inverted, in the sentence warning about the inversion** — measured, **row 1** discriminates and row 2 does not; and spec §2's rule names an **unconstructable** fixture (gzip cannot be wire-large/decompressed-small). |
| E7 | **CRITICAL** | the read-to-EOF hang is **created by this delivery**, not "not addressed" — today the same unterminated chunked request returns in **0 s**; under the cap it blocks **indefinitely**, and **no** `examples/` sets `ReadTimeout`. |
| E8 | bundle-correct | 400-wins-when-both-match confirmed in **both** wrap orders; `httpcall.ErrBodyTooLarge` stays 500. Unstated: a bare untranslated `*http.MaxBytesError` classifies **500**. |
| E9 | bundle-correct | `writeErr` logs only at `status >= 500` in all three adapters — a 413 produces no log line. |
| E10 | bundle-correct | the `int64` / `<= 0` convention works **end to end** through a real patched mount; zero `CustomizeConfig` literals exist to change meaning; `./transport/...` stays green. |
| E11 | MAJOR | discarding sites behave correctly, but `TestBodyAbsentOnTheOptionalRouteStillSucceeds` **is not constructible** (no test anywhere creates an instance in an incident state); and a *read* error is discarded there while it 400s at the 36 wrapping sites — undesigned. |
| E12 | MAJOR | *"this delivery ships no 413 message text"* is false — it necessarily ships `{"error":"…","message":"…"}`, and the **`error` code string is specified nowhere** in the bundle. |
| E13 | MAJOR | the deletions compose: `MountGroups` takes **no opts**, so those consumers get the default cap, **cannot pass the only migration lever**, and have no metric. Spec §4 marks this ✅ *"Not a gap"*. |
| E14 | MAJOR | the ADR never says what `w` is on gin; `gc.Writer` **keeps the connection alive** after a 413 where stdlib tears it down. `Unwrap()` fixes it — measured. |

⚠ **Method note.** Every CRITICAL above came from asking of a probe the bundle already ran and
passed: *"did this probe exercise the thing it claims about?"* — not from finding a false premise.
E4, E6 and E7 were all invisible to Evidence §8.1 because **not one of its fixtures sets a
`Content-Encoding` header, uses chunked framing, or mis-declares `Content-Length`**. The bias the
bundle named in its own banner is still fully present in §8.1.

⚠ **Probe hygiene.** All probes were throwaway and are deleted; `git status` shows only this file.
Baseline re-verified after restore: `go build ./...` OK, `go test -count=1 ./transport/...` all five
packages `ok`.


### E1 — "Aborted and truncated uploads keep today's behaviour" is FALSE: an over-declared `Content-Length` goes 200 → 400. A THIRD wire break, unlisted.

**Severity: CRITICAL**

**Bundle says.** ADR §Consequences/Positive: *"**Aborted and truncated uploads keep today's
behaviour**, because oversize is identified by type rather than by 'an error happened'."*
Plan §4 (phase 4 docs) and ADR Negative both enumerate exactly **two** wire breaks:
*"(i) a new **413** on routes that previously returned 400, 500 or a spurious **2xx**; (ii) requests
succeeding today via the trailing-byte gap now fail."*
Plan phase 2 test 3: `TestAbortedUploadIsNotA413` — *"`Content-Length` over-declared, connection
ends early. **Falsifier:** it fails against an implementation that treats any read error as
oversize."*

**I ran.** `transport/http/httpcore/zz_e1_test.go` (throwaway, deleted). A real `httptest.Server`,
raw `net.Dial` wire control, real `httpcore.StartInput`, real `http.MaxBytesReader`, 64-byte cap
standing in for 1 MiB. `TODAY` = `json.NewDecoder(req.Body).Decode(&in)` — the exact stdlib idiom.
`MINIMAL` = `io.ReadAll(http.MaxBytesReader(w, req.Body, n))` → `errors.As(*http.MaxBytesError)` →
413, else 400; then `json.NewDecoder(bytes.NewReader(buf))`. Fixture: `Content-Length: 500`,
17 bytes of complete JSON actually sent, then `CloseWrite`.

**Observed (verbatim).**
```
CL-over-declared-500-actual-17     TODAY=200 def_ref="a:1"     MINIMAL=400 read=unexpected EOF
```

**Verdict: CONFIRMED-DEFECT.**

Today, `Decode` returns `nil` — it got its complete first JSON value before the stream died and
never looks further, so the handler returns **200**. Read-to-EOF *must* see the truncation, so the
same request becomes **400**. The `errors.As` discriminator does its job (it is not a 413) — that is
the only thing the bundle checked, and it is the only thing the prescribed test asserts.

So the delivery has **three** wire breaks, not two. The third is invisible to every fixture in the
bundle because §8.1's four rows all use well-formed, correctly-framed requests, and phase 2 test 3
asserts a **negative** (`IsNotA413`) that is satisfied by both 400 and 200.

⚠ This is the round-6 bias exactly: the fixture is aimed at the thing the fix does well.
The claim *"aborted and truncated uploads keep today's behaviour"* is the **celebratory sentence**
— it was written to describe the `errors.As` win and over-generalised into a statement about
behaviour preservation that the same probe refutes.

**Fix.**
1. **Delete the ADR Positive bullet** *"Aborted and truncated uploads keep today's behaviour"*.
   Replace with the measured truth: *"An aborted or truncated upload is **not** a 413 — the
   `errors.As` discriminator holds — but it does change status: a client that over-declares
   `Content-Length` and disconnects after a complete JSON value gets **400** where it gets **200**
   today (executed: `TODAY=200`, `MINIMAL=400 read=unexpected EOF`)."*
2. **Add it as wire break (iii)** in ADR Negative, the plan's phase-4 `CHANGELOG`/`STABILITY` row,
   and spec §2.
3. **Rewrite plan phase 2 test 3** to assert the **status**, not the absence of one:
   `TestAbortedUploadIs400NotA413`, with the falsifier restated as *"row fails against any
   implementation treating a read error as oversize (would be 413) **and** against any
   implementation claiming today's 200 is preserved."* A test whose assertion is `!= 413` passes
   for 200, 400, 500 and 503 alike.

### E2 — the gin `ASSUMPTION (unverified)` is DISCHARGED: the buffer swap is behaviourally identical across 14 fixtures

**Severity: BUNDLE-CORRECT (recorded so nobody re-derives it)**

**Bundle says.** Spec §5, the one live assumption: *"`ASSUMPTION (unverified)`: **gin's
`ShouldBindJSON` behaves identically when `gc.Request.Body` is reassigned to the buffer.**
Evidence §7.1 executed the *decoder* half; the **binder + validator** half is what phase 2's gin
test must discharge."*

**I ran.** `transport/http/gin/zz_e2_test.go` (throwaway, deleted). Real `gin.New()` engine, real
`httptest.Server`, real `httpcore.StartInput`, real `httpcore.Validate`. `/today` binds straight
from the wire; `/swap` does `io.ReadAll(http.MaxBytesReader(gc.Writer, gc.Request.Body, 64))` then
`gc.Request.Body = io.NopCloser(bytes.NewReader(buf))` then `ShouldBindJSON` unchanged. 14 fixtures.

**Observed (verbatim, abridged to the discriminating rows).**
```
clean                        TODAY=200 def_ref="a:1"          SWAP=200 def_ref="a:1"
exactly-at-cap-64            TODAY=200 def_ref="a:1"          SWAP=200 def_ref="a:1"
cap-plus-1-65                TODAY=200 def_ref="a:1"          SWAP=413 read=http: request body too large
empty                        TODAY=400 bind=EOF               SWAP=400 bind=EOF
whitespace-only              TODAY=400 bind=EOF               SWAP=400 bind=EOF
truncated                    TODAY=400 bind=unexpected EOF    SWAP=400 bind=unexpected EOF
two-values-ws-sep            TODAY=200 def_ref="a:1"          SWAP=200 def_ref="a:1"
undercap-trailing-garbage    TODAY=200 def_ref="a:1"          SWAP=200 def_ref="a:1"
overcap-trailing-garbage     TODAY=200 def_ref="a:1"          SWAP=413 read=http: request body too large
```
Every non-oversize row is **byte-for-byte identical**, error text included. `errors.As` against
`*http.MaxBytesError` holds through the gin handler.

**Verdict: BUNDLE-CORRECT.** Spec §5 may move this line to its *"Discharged — do not re-derive"*
list, citing this file. ⚠ But see **E3**: the *reason* the bundle gives for needing the swap, and
the test it prescribes to discharge this, are both wrong.

---

### E3 — the test prescribed to discharge that assumption CANNOT BE WRITTEN: gin's validator is not engaged on any DTO in this repo (zero `binding:` tags)

**Severity: CRITICAL**

**Bundle says.** Plan §3 phase 2, test 7: *"**gin only:** ⭐⭐
`TestBinderAndValidatorSurviveTheBufferSwap` — **a body that must fail validation (not decoding)**
still produces the same error through the reassigned body. ⚠ **This discharges spec §5's one live
`ASSUMPTION (unverified)`.** Do not infer it from stdlib."*
ADR §Decision: *"for gin reassign `gc.Request.Body` to the buffer before `ShouldBindJSON`
**so its binding and validation are unchanged**."*

**I ran.**
```
$ grep -rn 'binding:' --include='*.go' transport/ service/ engine/ | grep -v _test.go
(no output; exit 1)
```
and the control arm of `zz_e2_test.go`: a synthetic `e2Tagged struct{ DefRef string
\`json:"def_ref" binding:"required"\` }` alongside the real `httpcore.StartInput`.

**Observed (verbatim).**
```
MISSING-def_ref-{}           TODAY=400 validate=workflow-httpcore: bad input: Key: 'StartInput.def_ref' Error:Field validation for 'def_ref' failed on the 'required' tag   SWAP=(identical)
unknown-field-only           TODAY=400 validate=workflow-httpcore: bad input: Key: 'StartInput.def_ref' …                                                                    SWAP=(identical)
--- CONTROL: DTO WITH a gin `binding:"required"` tag ---
tagged-MISSING-def_ref-{}    TODAY=400 bind=Key: 'e2Tagged.DefRef' Error:Field validation for 'DefRef' failed on the 'required' tag                                          SWAP=(identical)
```

**Verdict: CONFIRMED-DEFECT.**

`gc.ShouldBindJSON` validates through **go-playground/validator with tag name `binding`**. This repo
has **zero `binding:` tags** in any non-test Go file. `httpcore.StartInput` carries
`validate:"required"` (`dto.go:20`), which is read by `httpcore.Validate`
(`transport/http/httpcore/validate.go:32`) — a **separate function, called after binding, from
inside `httpcore.StartInstance`, taking a struct value, not a reader.**

Consequences, both fatal to the prescription:

1. **There is no body that fails gin's validation on any route this delivery touches.** For
   `{}` the binder returns **nil** — the 400 comes from `httpcore.Validate` further down. So test 7
   as specified (*"a body that must fail validation, not decoding"*) has no fixture. Writing it with
   a synthetic tagged DTO — as my control arm did — tests **gin**, not this delivery, and would be a
   test on a type no route uses.
2. **If test 7 is instead written against `httpcore.Validate`, it CANNOT FAIL.** `Validate(in)`
   receives an already-decoded struct value; whether those bytes arrived from `req.Body` or from a
   `bytes.Reader` is unobservable to it. There is no mutation of the buffer-swap line that turns
   that assertion red. This is the repo's recurring *"prescribed test that could not fail"* failure
   (ADR-0183, ADR-0162/0163's six) reappearing in the one test the bundle marks ⭐⭐.
3. **The ADR's stated rationale is a false premise.** *"…so its binding and **validation** are
   unchanged"* names a validation step gin never performs here. The swap is needed for the plain
   reason that `ShouldBindJSON` reads from `gc.Request.Body`; no validator is at stake.

**Fix.**
- **Replace plan phase 2 test 7** with what actually discharges the assumption and *can* fail:
  `TestGinBindThroughReassignedBodyMatchesTheWire` — a table over the fixtures in E2 asserting the
  **verbatim error text and status** of `ShouldBindJSON` are identical with and without the swap,
  including `empty`→`EOF`, `truncated`→`unexpected EOF`, `two-values-ws-sep`→200 and
  `undercap-trailing-garbage`→200.
  **Falsifier, stated:** *the `undercap-trailing-garbage` and `two-values-ws-sep` rows fail against
  an implementation that substitutes `json.Unmarshal` for the reassignment; the `empty` row fails
  against one that reassigns `http.NoBody` or leaves `gc.Request.Body` nil.*
- **Correct the ADR sentence** to *"…so its binder is unchanged"*, and record in spec §5 that
  **gin's `binding:` validator is not engaged anywhere in this repo — `validate:` tags are consumed
  by `httpcore.Validate` after binding** (executed). That fact is a boundary the bundle asserted
  (§5 lists eight derived boundaries; this is a ninth it did not think to list).
- Move spec §5's gin assumption to *Discharged*, citing **E2**, not test 7.

### E4 — ⭐⭐⭐ THE CAP IS FULLY BYPASSED ON FIBER BY `Content-Encoding: gzip`. "Over-cap bodies are rejected whatever their shape" is FALSE on 13 of the 39 sites.

**Severity: CRITICAL**

**Bundle says.**
ADR §Consequences/Positive: *"The unbounded-body surface closes on all **39** sites — and, unlike
every previous revision, the claim survives its own fixtures: **over-cap bodies are rejected
whatever their shape**."*
ADR §Decision: *"`fiber`: a `len(c.BodyRaw())` pre-check before `c.Bind().JSON`, which is already
before the parse."*
ADR §Negative: *"⚠ **Peak memory is `MaxBodyBytes × in-flight requests`**, and nothing here bounds
concurrency."*

**I ran.** `transport/http/fiber/zz_e5_test.go` (throwaway, deleted). A real `fiber.New()` app whose
route implements **exactly** the bundle's prescription — `len(c.BodyRaw())` pre-check at a **1 MiB**
cap, then `c.Bind().JSON(&in)` into the real `httpcore.StartInput` — with fiber's default
`BodyLimit` (4 MiB). Fixture: `{"def_ref":"a:1","vars":{"k":"vvv…"}}` with a 3 MiB `k`, gzipped.

**Observed (verbatim).**
```
1 plain wire=2097185   status=413 413 workflow-httpcore: request body too large (wire=2097185)
2 gzip  wire=3121      dec=3145761   status=200 200 def_ref="a:1" wire=3121 PARSED-BYTES=3145761 varlen=3145728
```

**Verdict: CONFIRMED-DEFECT.**

`c.Bind().JSON(out)` is `bind.Bind(b.ctx.Body(), out)` (`fiber/v3@v3.4.0/bind.go:309`) — it reads
**`c.Body()`, the DECOMPRESSED body**, not `BodyRaw()`. So the prescribed pre-check bounds the
**wire** bytes while the parse that follows bounds nothing: a **3121-byte** request is expanded to
**3 145 761 bytes**, fully materialized, and parsed into a process-variable map. **200 OK.**
Amplification here is **1008×**, and it is capped only by `app.config.BodyLimit`.

Three bundle claims fall:

1. **"over-cap bodies are rejected whatever their shape"** — refuted. On fiber's **13** of the 39
   sites, `Content-Encoding: gzip` is a shape that is **accepted with 200**. This is the exact
   over-reaching quantifier Premise Discipline names, in the ADR's celebratory Positive bullet, and
   no fixture in Evidence §8.1 sets a `Content-Encoding` header at all.
2. **"Peak memory is `MaxBodyBytes × in-flight requests`"** — false on fiber. Peak is
   `app.config.BodyLimit × in-flight` = **4 MiB × in-flight** by default, i.e. **4× the documented
   bound**, and `MaxBodyBytes` **does not appear in fiber's bound at all**. Setting
   `WithMaxBodyBytes(64<<10)` does not lower fiber's peak memory by one byte.
3. **The delivery is not adapter-neutral in the way the ADR asserts.** stdlib and gin do not
   decompress, so `MaxBytesReader` genuinely bounds what is materialized there. Fiber does. The
   bundle treats `BodyRaw()` as *the* fix (⚠⚠ **NOT `c.Body()`**) when in fact `BodyRaw()` is the
   right thing to *measure* and the wrong thing to *assume bounds the parse*.

⚠ The ADR does list *"a decompressed-size bound for fiber"* under follow-ups, framed as *"the
ceiling is `app.config.BodyLimit`, which a mounted route group does not own"*. That framing says
**"we cannot lower the 4 MiB ceiling"**. The measured truth is **"the configured cap is not enforced
at all for compressed bodies on this adapter"** — a different and much larger statement, and the one
a consumer needs before they believe `WithMaxBodyBytes` means what it says.

**Fix** (design increment; this cannot be a doc-only change).
- **Preferred:** on fiber, apply the cap to **both** — keep `len(c.BodyRaw())` for the wire bound
  **and** add `len(c.Body())` after decompression, before `Bind().JSON`. `c.Body()` is already
  called by the binder, so the second check costs nothing new: it reuses the same materialized
  buffer. Wire-bound rejects the plain oversize case with no decompression; decompressed-bound
  rejects the amplification case. Both return `ErrRequestBodyTooLarge` → 413.
  ⚠ Note this is the *opposite* of the ADR's ⚠⚠ **"NOT `c.Body()`"** warning — that warning is
  correct about which value is the *wire* size and wrong to conclude `Body()` has no role.
- **Minimum acceptable if the increment is refused:** delete the *"whatever their shape"* quantifier
  and the *"all 39 sites"* claim; restate the fiber peak-memory residual as
  `max(MaxBodyBytes, fiber.Config.BodyLimit) × in-flight`; and state plainly in `SECURITY.md` that
  **`MaxBodyBytes` does not bound a compressed request body on the fiber adapter.**
- Add a fiber test `TestCompressedBodyIsCappedByDecompressedSize` with the row above.
  **Falsifier:** *it fails against any implementation that checks only `len(c.BodyRaw())`* —
  executed, that implementation returns **200** with 3 145 761 bytes parsed.

---

### E5 — the ADR's `len == 33` claim is numerically right and CAUSALLY WRONG, and it hides a response side effect that is already live at all 13 fiber sites

**Severity: MAJOR**

**Bundle says.** ADR §Decision: *"⚠⚠ **NOT `c.Body()`** — it **decompresses**
(`fiber/v3@v3.4.0/req.go:146`), **so** a 63.7 KiB gzip expanding to 64 MiB returns `len == 33`.
`c.BodyRaw()` (`req.go:92-96`) is the wire body **with no response side effect**."*
Spec §2 repeats it: *"`c.Body()` decompresses; a 63.7 KiB gzip expanding to 64 MiB yields
`len == 33`."*

**I ran.** Same probe, plus a bare getter route logging `len(BodyRaw())`, `len(Body())`, the first
40 bytes of `Body()`, and `c.Response().StatusCode()` **after** calling the getter.

**Observed (verbatim).**
```
4a dec=3MiB  status=200 BodyRaw=3121 Body=3145761 bodyText="{\"def_ref\":\"a:1\",\"vars\":{\"k\":\"vvvvvvvvvv" status-set-by-getter=200
4b dec=8MiB  status=413 BodyRaw=8213 Body=33      bodyText="body size exceeds the given limit"              status-set-by-getter=413
3 gzip wire=8213 dec=8388641  status=413 400 bind=bind from body: invalid character 'b' looking for beginning of value (wire=8213 parsed=33)
```

**Verdict: CONFIRMED-DEFECT** (the causal claim, not the number).

The **33** is real, and it is `len("body size exceeds the given limit")` — `fasthttp.ErrBodyTooLarge`
rendered as text. Reading `req.go:150-205`: `Body()` decompresses via
`request.BodyGunzipWithLimit(app.config.BodyLimit)`; **on failure it returns `[]byte(err.Error())`**.
So `Body()` yields 33 **only when decompression blows past `BodyLimit`** — not because it
decompresses. When decompression *succeeds* (4a, 3 MiB < 4 MiB) `Body()` returns **3 145 761**.

The ADR's rule as written — *"`Body()` decompresses, so a bomb reads small"* — is backwards. The
truth is: **`Body()` reads LARGE for every bomb under `BodyLimit` (which is the dangerous range, see
E4) and reads 33 only above it.** A reader who trusts the ADR's sentence will conclude `Body()` is
useless for size checks; it is in fact the only value that sees the amplification.

Two further facts the bundle does not record:

- **`c.Body()` writes the response.** The `errors.Is(err, fasthttp.ErrBodyTooLarge)` arm calls
  `r.c.DefaultRes.SendStatus(StatusRequestEntityTooLarge)` — measured: `status-set-by-getter=413`.
  The ADR notes `BodyRaw()` has *"no response side effect"*, which implies awareness, but never
  states that **`Body()` does, that `Bind().JSON` calls `Body()`, and therefore that the side effect
  is already live at all 13 fiber decode sites today.**
- **Row 3 is the visible damage, and it is pre-existing:** status **413** on the wire with a
  **400-shaped body** the handler wrote afterwards. Once this delivery ships a *real* 413, a fiber
  consumer cannot tell "our cap rejected you" (413 + `ErrorBody`) from "fiber's decompression limit
  rejected you" (413 + a `bind from body: invalid character 'b'` message). The bundle's
  ⚠ *"Fiber diverges above `fiber.Config.BodyLimit`: framework plain-text 413, no `ErrorBody`"* is
  therefore **incomplete** — the divergence also reaches the **compressed** path, where the body is
  not fasthttp's plain text but the adapter's own 400 envelope under a 413 status line.

**Fix.**
- Replace the ADR/spec sentence with the measured mechanism: *"`c.Body()` returns the
  **decompressed** length — 3 145 761 for a 3121-byte gzip — and returns the 33-byte string
  `\"body size exceeds the given limit\"` only when decompression exceeds `fiber.Config.BodyLimit`,
  in which case it also **sends a 413 response from inside the getter** (`req.go:190-200`)."*
- State that `Bind().JSON` → `c.Body()` (`bind.go:309`), so this side effect is live at all 13
  existing fiber sites and is not introduced by this delivery.
- Extend the ADR's fiber-divergence bullet to name the **compressed** case: status 413, body =
  the adapter's own 400 envelope.

---

### E6 — plan phase 2 test 8's falsifier is INVERTED — in the sentence that warns about the inversion. Row 2 cannot discriminate; row 2's shape is unconstructable.

**Severity: CRITICAL**

**Bundle says.** Plan §3 phase 2, test 8: *"**fiber only:** `TestWireBytesNotDecompressedBytes` —
gzip **wire 2 KiB / decompressed 2 MiB** at a 1 MiB cap must **not** 413; gzip **wire 2 MiB** must
413. ⚠⚠ **Falsifier: row 2 fails against a `len(c.Body())` pre-check.** ⚠ **Row 2 is the
discriminating one; an earlier revision had this backwards.**"*
Spec §2 states the general rule: *"The discriminating fixture is **wire-large, decompressed-small**."*

**I ran.** Both fixtures through **both** implementations, real fiber, 1 MiB cap:
RIGHT = `len(c.BodyRaw())` pre-check; WRONG = `len(c.Body())` pre-check.
Row-1 fixture: gzip wire **3121**, decompressed **3 145 761**.
Row-2 fixture: **genuinely incompressible** (`math/rand` letters, not a period-26 pattern) — gzip
wire **1 258 907**, decompressed **2 097 185**.

**Observed (verbatim).**
```
RIGHT  row1 (wire 3121 / dec 3 145 761)        status=200
RIGHT  row2 (wire 1 258 907 / dec 2 097 185)   status=413
6a WRONG impl, plan row1 (wire small/dec 3MiB):        status=413 413 (decompressed=3145761)
6b WRONG impl, plan row2 (wire 2MiB incompressible):   status=413 413 (decompressed=2097185)
```

**Verdict: CONFIRMED-DEFECT.**

| plan row | assertion | RIGHT | WRONG | discriminates? |
|---|---|---|---|---|
| 1 — wire small / dec large | must **not** 413 | 200 ✅ | **413** ❌ | **YES** |
| 2 — wire 2 MiB | must 413 | 413 ✅ | 413 ✅ | **NO** |

**Row 1 is the discriminating one. Row 2 passes under the wrong implementation.** The plan asserts
the exact opposite, in the same sentence that says *"an earlier revision had this backwards"* — the
correction re-introduced the inversion it was correcting. This is the ADR-0165 shape (an inverted
predicate surviving a design audit because nobody ran it) reappearing in a **falsifier**, which is
the one artefact whose entire job is to be runnable.

⚠ **And spec §2's general rule is worse than inverted — it names an unconstructable fixture.**
*"wire-large, decompressed-small"* cannot exist for gzip: DEFLATE stored blocks add ~5 bytes per
65 535, so wire ≥ decompressed − ε. There is **no** gzip body that is over a 1 MiB cap on the wire
and under it decompressed. A test written to spec §2's rule has no fixture at all.

**Fix.**
- Plan test 8: **swap the falsifier.** *"⚠⚠ **Falsifier: ROW 1 fails against a `len(c.Body())`
  pre-check** — executed, that implementation returns 413 for a 3121-byte request. Row 2 passes
  under both implementations and is a control, not a falsifier."*
- Spec §2: replace *"The discriminating fixture is wire-large, decompressed-small"* with
  *"The discriminating fixture is **wire-small, decompressed-large** — the bomb. It is the only
  orientation gzip can produce."*
- ⚠ Require the row-2 fixture to be built from `math/rand` bytes and to **assert its own wire size
  exceeds the cap**. A `strings.Repeat`-style filler gzips to ~0.2 % and silently becomes a second
  copy of row 1: my first attempt used a period-26 pattern and produced wire **5178**, not 2 MiB —
  a fixture that would have made the row vacuous in the opposite direction.
- ⚠ Per **E4**, once the decompressed-size check is added, row 1's expected result changes from
  200 to **413** (decompressed 3 145 761 > 1 MiB). The two findings interact: fix E4 first, then
  restate test 8 as *wire-bound and decompressed-bound are separate checks with separate fixtures*.

### E7 — the read-to-EOF hang is CREATED by this delivery, not merely "not addressed". Today the same request returns immediately.

**Severity: CRITICAL**

**Bundle says.** ADR §"The bound is on SIZE, not on TIME": *"Reading to EOF under a cap replaces
*return-on-first-value* with *wait-for-EOF*. **Executed against a real `http.Server`: a chunked
request with no terminating chunk holds the handler indefinitely.**"*
ADR §Negative: *"**Slowloris is not addressed**; the consumer owns `ReadTimeout`."*
Spec §4 coupling 12: *"⚠ **Stated, NOT resolved.**"*

**I ran.** `transport/http/httpcore/zz_e7_test.go` (throwaway, deleted). Three real `httptest`
servers, raw `net.Dial`, a chunked request with **no terminating chunk**, connection held open.
(a) new mechanism, no `ReadTimeout`; (b) new mechanism, `ReadTimeout = 1s`; (c) **today's**
`json.NewDecoder(r.Body).Decode(&v)` with a **complete JSON value in the first chunk**, no terminator.

**Observed (verbatim).**
```
UNTERMINATED-CHUNKED: handler STILL BLOCKED after 4s  ⇒ HANG CONFIRMED (no ReadTimeout set)
WITH ReadTimeout=1s: handler returned after 1.001s err=read tcp 127.0.0.1:58532->127.0.0.1:58533: i/o timeout  ⇒ MITIGATION WORKS
TODAY, complete value then no terminator: handler returned after 0s err=<nil>  ⇒ RETURNS IMMEDIATELY
```
Plus, from source: **all three** `http.Server` literals in `examples/` — `production_wiring/main.go:277`,
`sqlite_wiring/main.go:281`, `mysql_wiring/main.go:265` — set `ReadHeaderTimeout: 5 * time.Second`
and **no `ReadTimeout`**. (Bundle-correct on that fact.)

**Verdict: CONFIRMED-DEFECT** (the framing, not the mechanism).

The hang is real and the stated mitigation genuinely works. But the third row is the one the bundle
never ran, and it changes what the residual *is*:

**Today that request returns in 0 s with `err=nil` and a 200.** The decoder stops at the first
complete JSON value and never waits for the terminator. Under read-to-EOF the *same* request pins a
goroutine, a connection and a read buffer **indefinitely**.

So *"Slowloris is not addressed"* is the wrong sentence. Slowloris against a decode site **does not
work today and starts working when this ships**. "Not addressed" describes an inherited problem;
this is a **newly created** one, and it is created in the delivery whose purpose is to reduce
resource exhaustion. The ADR's own §"The bound is on SIZE" paragraph is accurate about the
mechanism and never says *"this is new"* — a reader who checks the Negative list sees an
acknowledged pre-existing weakness rather than an introduced regression.

⚠ Plan item 7 asks whether stating a residual is defensible *"or whether one of them makes the
delivery net-negative"*. For a consumer with no `ReadTimeout` — **which is every consumer following
the shipped `examples/`** — this delivery trades a *bounded* memory risk (1 MiB × in-flight, and
`Content-Length` already bounded the non-chunked case) for an *unbounded* goroutine/connection risk.
That is arguably net-negative for exactly the population the default cap is meant to protect.

**Fix.** One of:
- **(preferred, small)** When the cap is installed, also bound the wait: wrap the read so an
  unterminated body cannot outlive the request — e.g. reject `Transfer-Encoding: chunked` bodies
  that have not reached EOF within a configurable budget, or simply document that
  `WithMaxBodyBytes` **requires** the consumer to set `ReadTimeout` and make the three `examples/`
  set it (`ReadTimeout: 30 * time.Second`) **in this bundle**. Changing three example files is
  cheap, in scope (`examples/` is already a phase-4 target via `go build ./examples/...`), and
  removes the "our own reference wiring is vulnerable" embarrassment.
- **(minimum)** Rewrite the ADR Negative to say what was measured: *"**This delivery introduces a
  slowloris vector that does not exist today.** Executed: an unterminated chunked request carrying a
  complete JSON value returns in 0 s today and blocks indefinitely under the cap. Mitigation:
  `http.Server.ReadTimeout`, which **none** of the three `examples/` sets. Executed:
  `ReadTimeout = 1s` releases the handler in 1.001 s."*
  Add it to `SECURITY.md` residual 1 with the same "new, not inherited" wording, and to the
  `CHANGELOG` as an operational-requirement note.
- Add a plan phase-2 test `TestUnterminatedChunkedBodyDoesNotOutliveReadTimeout`.
  **Falsifier:** *it fails against today's code, which returns 200 in 0 s* — executed above.

---

### E8 — the ordering premise and the `httpcall` sentinel are correct; a bare, untranslated `*http.MaxBytesError` classifies **500**

**Severity: BUNDLE-CORRECT, with one unstated failure mode (MINOR)**

**Bundle says.** ADR §Decision: *"an error wrapping both classifies **400**, because the switch is
ordered… So the **413 arm is placed before the 400 arm**"*; *"`action/httpcall.ErrBodyTooLarge`
exists, means an *outbound response* exceeded 10 MiB, and is correctly a **500**. A test pins that
it still is."*

**I ran.** `TestE7ClassifyOrderingPremise` — real `httpcore.ClassifyError`, real
`action/httpcall.ErrBodyTooLarge`.

**Observed (verbatim).**
```
bare new sentinel (no arm yet)                               => 500 {Error:internal_error Message:}
wrapping BOTH ErrBadInput and the new sentinel               => 400 {Error:bad_request Message:workflow-httpcore: bad input: workflow-httpcore: request body too large}
wrapping the new sentinel then ErrBadInput                   => 400 {Error:bad_request Message:workflow-httpcore: request body too large: workflow-httpcore: bad input}
action/httpcall.ErrBodyTooLarge (must stay 500)              => 500 {Error:internal_error Message:}
httpcall.ErrBodyTooLarge wrapped in ErrBadInput              => 400 {Error:bad_request Message:workflow-httpcore: bad input: workflow-httpcall: body exceeds max size}
a bare *http.MaxBytesError (if an adapter forgot to translate) => 500 {Error:internal_error Message:}
ErrBadInput wrapping *http.MaxBytesError                     => 400 {Error:bad_request Message:workflow-httpcore: bad input: http: request body too large}
```

**Verdict: BUNDLE-CORRECT.** The 400-wins-when-both-match premise is confirmed **in both wrap
orders** (the bundle only ever showed one), so the 413-before-400 placement is necessary and
sufficient. `httpcall.ErrBodyTooLarge` is 500 and stays 500 (it appears in no arm).

**Unstated failure mode worth a plan row.** A **bare, untranslated `*http.MaxBytesError`** — the
shape produced if any of the 39 sites is converted to read-through-`MaxBytesReader` but its
`errors.As` translation is forgotten — classifies **500 `internal_error` with an empty message**,
which is silent: no log distinguishes it (`writeErr` logs at `>= 500`, so it *is* logged, but as
`"rest: internal error"`). Add a `ClassifyError` row asserting `500` for a bare `*http.MaxBytesError`
so the intent ("this type must never reach `ClassifyError`") is pinned, or — better — add a 413 arm
for `*http.MaxBytesError` itself as a belt-and-braces second discriminator.

---

### E9 — `writeErr` logs only at `status >= 500`: confirmed in all three adapters

**Severity: BUNDLE-CORRECT**

**Bundle says.** ADR §Negative and spec §4 coupling 2: *"Verified: `writeErr` logs only at
`status >= 500` and this delivery does not change it."*

**I ran.** Read all three at the anchor: `transport/http/stdlib/write.go:30-36`,
`transport/http/gin/write.go:11-17`, `transport/http/fiber/write.go:11-17`. Each is
`status, body := httpcore.ClassifyError(err)` then `if status >= 500 { cfg.Logger.ErrorContext(...) }`.

**Verdict: BUNDLE-CORRECT.** A 413 will produce **no log line** on any adapter.

### E10 — the `int64` / `<= 0` convention works END TO END through a real mount (Evidence §8.2's unexecuted half, discharged)

**Severity: BUNDLE-CORRECT**

**Bundle says.** Evidence §8.2: *"**Read from source**… ⚠ **Not executed:** the end-to-end path from
`WithMaxBodyBytes(0)` through a real adapter to a decode site. Phase 2's opt-out test is what
discharges it."* Spec §5 lists it as a live `ASSUMPTION (unverified)`.

**I ran.** I implemented the bundle's phase-1 + one phase-2 site **in this throwaway worktree**
(`cp` backups, restored after; `git status` clean): `CustomizeConfig.MaxBodyBytes int64` with
`MaxBodyBytes: 1 << 20` in `ResolveConfig`'s struct literal, `WithMaxBodyBytes[R]`,
`ErrRequestBodyTooLarge`, a 413 arm **before** the 400 arm, and the read-then-decode conversion of
`stdlib/groups.go`'s `POST /instances` site. Then drove a real `httptest.Server` over
`stdlib.InstanceRoutes` with the real `internal/transporttest` harness.

**Observed (verbatim).**
```
ResolveConfig()                        MaxBodyBytes=1048576
ResolveConfig(WithMaxBodyBytes(0))     MaxBodyBytes=0
ResolveConfig(WithMaxBodyBytes(-1))    MaxBodyBytes=-1
ResolveConfig(WithMaxBodyBytes(4096))  MaxBodyBytes=4096
ResolveConfig(WithLogger(nil), WithMaxBodyBytes(0)) MaxBodyBytes=0

default (1 MiB)                  3MiB=413 {"error":"request_too_large",...}  17B=404 (reaches the service)
WithMaxBodyBytes(0) — opt-out    3MiB=404 (reaches the service)              17B=404
WithMaxBodyBytes(-1) — opt-out   3MiB=404 (reaches the service)              17B=404
WithMaxBodyBytes(64)             3MiB=413 {"error":"request_too_large",...}  17B=404
MountGroups (no opts)            3MiB=413 {"error":"request_too_large",...}  17B=404
```
And `go test -count=1 ./transport/...` with the patch applied: **EXIT=0**, all five packages `ok`
(`httpcore`, `stdlib`, `gin`, `fiber`, `parity`).

**Verdict: BUNDLE-CORRECT.** The struct-literal default survives an explicit `0`; `0` and `-1` both
reach the decode site as *"no wrapper installed"* (a 3 MiB body succeeds); the default reaches
`MountGroups`. **No existing test changes meaning at a 1 MiB default** — there are also **zero**
`CustomizeConfig[...]{…}` composite literals anywhere in the repo outside `ResolveConfig` itself
(`grep`), so the new field cannot silently mean "disabled" for any existing construction site.

Spec §5 may move this assumption to *Discharged*, citing this file. ⚠ But see **E12** — the ⭐ mark
on plan phase-1 test 2 (*"Assert on the resolved config value AND on whether a wrapper would be
installed"*) is the right instruction and must survive, because asserting only the number would have
passed for an implementation that stores `0` and still calls `MaxBytesReader(w, body, 0)` — which
rejects **every** non-empty body.

---

### E11 — the three discarding sites behave correctly; but plan phase 2 test 5 as named is not constructible

**Severity: MAJOR**

**Bundle says.** ADR §Decision: *"⚠ **The three discarding sites gain an oversize path**, using the
same `errors.As` check; **every other decode error stays ignored**, because the body there is
genuinely optional."*
Plan phase 2 test 4/5: *"`TestOversizedBodyReturns413OnTheOptionalBodyAdminRoute` — **names the
resolve-incident route**"* and *"`TestBodyAbsentOnTheOptionalRouteStillSucceeds` — the control for
test 4."*

**I ran.** Patched `stdlib/groups.go:238` (the `_ = json.NewDecoder(req.Body).Decode(&in)` site) to
the prescribed shape, mounted **`stdlib.AdminRoutes`** (⚠ not `InstanceRoutes` — the route lives in
the admin group, which is why ADR-0095 keeps it out of `Mount`), and drove eight body shapes raw.

**Observed (verbatim).**
```
absent (no Content-Length)   => 404 {"error":"not_found","message":"workflow-service: resolve incident: workflow-runtime: instance not found"}
empty                        => 404 (identical)
valid                        => 404 (identical)
truncated                    => 404 (identical)
garbage-under-cap            => 404 (identical)
oversize-wellformed          => 413 {"error":"request_too_large","message":"workflow-httpcore: request body too large"}
oversize-and-malformed       => 413 (identical)
CL-over-declared             => 404 (identical)
```

**Verdict: BUNDLE-CORRECT on the mechanism** — every non-oversize shape preserves today's
behaviour (falls through to the service), and both oversize shapes 413.

**Two defects in the surrounding text.**

1. ⚠ **`TestBodyAbsentOnTheOptionalRouteStillSucceeds` cannot be written as named.** Measured, an
   absent body on this route yields **404 instance-not-found**, not a success. To assert *"succeeds"*
   (200) the test needs a live instance **in an incident state**, and **no existing test in any
   adapter does this** — `stdlib/coverage_test.go:309`, `gin/gin_admin_test.go:65` and
   `fiber/fiber_test.go:1027` all use `no-such-id`/`no-such` and assert the 404. The plan budgets no
   harness work for it. **Fix:** rename to
   `TestBodyAbsentOnTheOptionalRouteStillReachesTheService` and assert **404 `not_found`**, i.e. the
   request got past the decode site unchanged. **Falsifier:** *it fails against an implementation
   that returns 400 or 413 for an absent body* — which is exactly what a naive
   `if rerr != nil { 400 }` would do.
2. ⚠ **"every other *decode* error stays ignored" understates it — a *read* error is ignored too**,
   and that is a **divergence from the wrapping sites**. Measured: `CL-over-declared` is **404 here**
   (ignored) and **400 at a wrapping site** (E1). Both are defensible, but the ADR sentence describes
   only decode errors, so the read-error case at the discarding sites is undesigned. State it:
   *"at the three discarding sites, a non-oversize **read** error is discarded like a decode error;
   at the 36 wrapping sites it becomes a 400."*

---

### E12 — spec §4 coupling 3's *"this delivery ships NO 413 message text"* is false, and the 413's `error` code string is specified nowhere

**Severity: MAJOR**

**Bundle says.** Spec §4, coupling 3: *"⚠ **Stated, NOT resolved — one-way, out of this delivery.**
… ⚠ **This delivery ships no 413 message text, so there is nothing here for that delivery to
contradict.**"* Spec §6 Non-goals: *"No change to what any 4xx message *says*."*

**I ran.** The E10 patch, implementing the ADR's own prescription (*"the adapter returns the **bare**
sentinel; `ClassifyError` maps it → 413"*). `ClassifyError` has no arm that can return a body
without an `Error` code — every non-default arm is
`ErrorBody{Error: "<code>", Message: err.Error()}` — and
`transport/http/parity/parity_test.go:560` (`TestParity_ErrorEnvelopes`) asserts *"The response must
have an 'error' field"* and byte-for-byte envelope equality across adapters.

**Observed (verbatim).** The wire body the delivery necessarily ships:
```
413 {"error":"request_too_large","message":"workflow-httpcore: request body too large"}
```

**Verdict: CONFIRMED-DEFECT.**

The delivery **does** ship 413 message text — an `error` **code string** and a `message` — and both
are wire contract. The bundle:

- **never names the code string.** It is in no ADR sentence, no plan decision→phase row, and no
  prescribed test. `"request_too_large"` above is **my invention**; an implementer could equally
  write `"payload_too_large"`, `"too_large"` or `"bad_request"`. For a delivery whose sole output is
  a wire contract, leaving the machine-readable discriminator unspecified is the single most likely
  thing to be got wrong twice (once per adapter pair) and then frozen by a parity test.
- **cannot omit it.** An `ErrorBody{}` with an empty `Error` would fail
  `TestParity_ErrorEnvelopes`'s existing *"want 'error' field in body"* assertion — so "ship no
  message text" is not an available option.
- The `message` value is fixed by the ADR's own *"return the **bare** sentinel"* rule to
  `ErrRequestBodyTooLarge.Error()`, which is identical across all three adapters — **good**, and it
  is why parity holds. That should be stated as the reason, not left implicit.

**Fix.**
- Delete *"This delivery ships no 413 message text"* from spec §4 coupling 3. Replace with:
  *"This delivery ships exactly two wire strings — `error: \"<code>\"` and
  `message: \"workflow-httpcore: request body too large\"`, the latter fixed by returning the bare
  sentinel, which is why `TestParity_ErrorEnvelopes` still holds byte-for-byte. The deferred 4xx
  delivery owns any change to them."*
- **Add a row to the plan's decision→phase map**: *"the 413 `ErrorBody.Error` code string is
  `\"request_too_large\"`" → phase 1 → `httpcore`*, and extend phase-1 test 1 to assert the exact
  envelope, not only the status.
- Add to phase 3: *"`TestParity_ErrorEnvelopes` gains an oversize case; it passes because all three
  adapters return the bare sentinel."*

---

### E13 — the deletions leave `MountGroups` consumers with neither a measurement nor a usable lever

**Severity: MAJOR**

**Bundle says.** ADR §"deliberately does NOT do": *"**No instrumentation.** … ⚠ **Consequence,
stated: a consumer cannot measure their body-size distribution before the cap bites.** The
migration lever is `WithMaxBodyBytes(0)`, not a metric."*
ADR §Negative: *"**`MountGroups` consumers get the default cap.** … ⚠ Its own godoc already names
the escape … ✅ **Not a gap.**"*
Plan item 6 asks exactly this: *"is `WithMaxBodyBytes(0)` really a sufficient migration lever with
no metric at all?"*

**I ran.** Read the signatures at the anchor:
```
transport/http/httpcore/seam.go:116  func MountGroups[R any](r R, groups ...RouteCustomizer[R])
transport/http/stdlib/mount.go:17    func Mount(mux *http.ServeMux, svc service.Service, opts ...httpcore.CustomizeOption[*http.ServeMux])
transport/http/gin/mount.go:14       func Mount(r ginlib.IRouter, svc service.Service, opts ...httpcore.CustomizeOption[ginlib.IRouter])
transport/http/fiber/mount.go:15     func Mount(r fiberlib.Router, svc service.Service, opts ...httpcore.CustomizeOption[fiberlib.Router])
```
and executed the `MountGroups` path in E10: **3 MiB → 413** with the default cap applied.

**Verdict: CONFIRMED-DEFECT** (the *"Not a gap"* adjudication, not the fact).

`MountGroups` takes **no `opts` variadic at all**. So for a consumer who mounts through it:

- the default 1 MiB cap **is applied** (executed),
- `WithMaxBodyBytes(0)` — the **only** migration lever the ADR offers — **cannot be passed**,
- and with instrumentation deleted there is **no way to discover** their body-size distribution
  before the cap starts rejecting traffic.

The ADR resolves this with *"its own godoc already names the escape — call `Customize` directly"*.
That is true, and it is **not** an escape hatch, it is a **rewiring requirement**: the consumer must
replace one `MountGroups(r, groups...)` call with N per-group `Customize(r, opts...)` calls, having
first enumerated which groups they mount. The three deletions compose into a worse position than any
of them alone — which is precisely the interaction shape CLAUDE.md rule 9's interaction clause
exists to catch, and coupling rows 8 and 10 in spec §4 are each marked ✅ **in isolation**.

⚠ Note this is the *same* structure as the finding the previous round accepted about the fiber WARN
(*"never fires for the group holding the discarding sites"*): a mechanism whose reach stops exactly
where the affected consumers are.

**Fix.** Cheapest option that closes it without a new exported interface (the bundle's load-bearing
Non-goal):
- **Add the variadic to `MountGroups`**:
  `func MountGroups[R any](r R, opts []CustomizeOption[R], groups ...RouteCustomizer[R])` is a source
  break; instead add a **sibling** `MountGroupsWith[R any](r R, opts []CustomizeOption[R], groups ...RouteCustomizer[R])`.
  This is a new exported *function*, not a new interface or cross-package contract, so it does not
  violate spec §6.
- **Or**, if that is refused: change spec §4 coupling 8 from ✅ to ⚠ **stated, NOT resolved**, and
  say in `SECURITY.md` and the `CHANGELOG` in one sentence:
  *"Consumers mounting via `MountGroups` receive the 1 MiB default and **cannot opt out without
  replacing the call with per-group `Customize(r, httpcore.WithMaxBodyBytes(0))`**."*
  Silence here is the failure mode — an adjudication of ✅ *"Not a gap"* on a row that is a gap is
  worse than not having the row.

### E14 — the ADR never says WHAT `w` is on gin, and the obvious choice (`gc.Writer`) silently keeps the connection alive after a 413 where stdlib tears it down

**Severity: MAJOR**

**Bundle says.** ADR §Decision, one bullet covering both adapters: *"`stdlib` / `gin`, **when
capped**: `io.ReadAll(http.MaxBytesReader(w, body, n))`, then run the site's existing decode idiom
over the buffer."* Plan phase 2 repeats it verbatim. Neither document names what `w` is at a gin
decode site — a gin handler has `gc`, not `w`.

**I ran.** `transport/http/gin/zz_e15_test.go` and a stdlib control (both throwaway, deleted).
Raw `net.Dial`, an oversize body over a **keep-alive** connection, then a **second request on the
same connection**, at a 64-byte cap.

**Observed (verbatim).**
```
STDLIB (real http.ResponseWriter)   resp1=200 close=true  errorsAs=true  | REUSE-FAILED: unexpected EOF
gin gc.Writer                       resp1=200 close=false errorsAs=true  | REUSED resp2=200
gin Unwrap()ed writer               resp1=200 close=true  errorsAs=true  | CONNECTION-CLOSED: unexpected EOF
```

**Verdict: CONFIRMED-DEFECT.**

`net/http`'s `maxBytesReader` calls `w.(interface{ requestTooLarge() })` when the limit trips, which
sets `closeAfterReply` on the connection. `gc.Writer` is a `gin.ResponseWriter` wrapping the real
writer, so the assertion **fails silently** — no error, no log, `errors.As` still true, the 413 still
correct. The only difference is invisible from the handler: **stdlib destroys the connection after
an oversize request; gin reuses it.**

Why that matters for a delivery whose purpose is resource exhaustion:

- On gin an attacker can pipeline **unbounded oversize attempts down a single connection**, each
  costing a full `MaxBodyBytes` read, paying **one** connection instead of one per attempt.
- `net/http` also drains the unread remainder before reuse (bounded by
  `maxPostHandlerReadBytes = 256 KiB`), so gin reads **more** bytes per rejected request than the cap
  the consumer configured — another place `MaxBodyBytes × in-flight` under-describes peak cost.
- It is a **silent** adapter divergence: the parity suite asserts status and envelope, not
  connection lifecycle, so nothing in the plan's phase 3 can see it.

⚠ **Honesty note on my own probe, in the §6.3a spirit.** My first attempt type-asserted the writer
against a locally-declared `interface{ requestTooLarge() }`. That assertion returns **false for the
real `*http.response` too** — an unexported method name is qualified by the *declaring* package, so
the check can never succeed from outside `net/http`. That row of output was **uninformative and
looked like evidence**. The finding rests entirely on the **behavioural** measurement above
(`close=` and connection reuse), not on the type assertion.

**Fix — measured to work, and it is three lines.**
- Plan phase 2, gin bullet: *"`w` is **not** `gc.Writer`. Obtain the underlying writer:
  `w := http.ResponseWriter(gc.Writer); if u, ok := any(gc.Writer).(interface{ Unwrap() http.ResponseWriter }); ok { w = u.Unwrap() }`.
  `*gin.responseWriter.Unwrap()` is exported (`gin@v1.12.0/response_writer.go:57`) though it is not
  part of the `gin.ResponseWriter` interface (`:23-47`), so the assertion is the supported route."*
  **Executed:** with the unwrap, gin matches stdlib — `close=true`, connection closed.
- Add the ADR sentence naming `w` per adapter, since the single "stdlib / gin" bullet is what hid it.
- Add gin test `TestOversizeRequestClosesTheConnection`.
  **Falsifier:** *it fails against an implementation passing `gc.Writer` directly* — executed, that
  implementation reuses the connection (`REUSED resp2=200`).
- ⚠ If the unwrap is rejected, this must appear in ADR Negative as a stated divergence, not be
  left unmentioned — it currently is neither.

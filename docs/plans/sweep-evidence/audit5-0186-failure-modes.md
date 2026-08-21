# Audit round 7 — ADR-0186 (stripped, one decision) — FAILURE-MODES lens

Worktree detached at `27ff5841`. Step 0: all **five** bundle files present — verified.
Every probe below was **executed** in this worktree unless the finding says
"reasoned — not executed". Probe package `zzfm/` created, run, and deleted.

---

### F1 — The cap turns a slowloris-IMMUNE deployment into a slowloris-DoS-able one, and the residual that "states" this describes a different, milder case
**Severity: CRITICAL**

**Bundle says** (ADR §"The bound is on SIZE, not on TIME"):
> "Reading to EOF under a cap replaces *return-on-first-value* with *wait-for-EOF*. **Executed
> against a real `http.Server`: a chunked request with no terminating chunk holds the handler
> indefinitely.**" … "**The consumer owns `ReadTimeout`.**" … "⚠ This is a *documented residual*,
> not a fix."
Spec §4 row 12 and plan §3 phase 4 residual 1 repeat exactly the **chunked-no-terminator** framing.

**The case it misses.** The chunked-no-terminator fixture is the *wrong* fixture: it does not
discriminate, because **today's server hangs on it too** (net/http drains the unread body
post-handler). The bundle picked the one time-related fixture where old and new behave the same,
and concluded from it that the residual is merely "we do not set `ReadTimeout` for you".

The discriminating fixture is **`Content-Length` declared above net/http's 256 KiB post-handler
drain tolerance but below the cap, a COMPLETE JSON value in the first bytes, then a dribble**:
`Content-Length: 400000`, body `{"def_ref":"a:1"}`, then one space every 200 ms, cap 1 MiB.

**Evidence.** `zzfm/p2_test.go`, real `http.Server`, no `ReadTimeout` (i.e. all three `examples/`):

```
TODAY  resp="HTTP/1.1 200 OK…"  after=0s      handlers entered=1 returned=1
NEW    resp="<NO RESPONSE>"     after=4.001s  handlers entered=1 returned=0

50 concurrent dribblers:
TODAY  goroutines 3 -> 53  (delta  50)  handlers entered=50 returned=50
NEW    goroutines 4 -> 154 (delta 150)  handlers entered=50 returned=0
```

TODAY the handler returns **immediately** and every connection is released — `json.Decoder` stops at
the first value and net/http declines to drain because the remaining declared length exceeds
`maxPostHandlerReadBytes` (256 KiB). NEW, **zero of fifty** handlers return; each attacker
connection costs a conn goroutine + a handler goroutine + a growing buffer, at ~5 bytes/second of
attacker bandwidth.

**Consequence.** The delivery **creates** an amplifier that does not exist today, it is **on by
default**, and the library's own three reference wirings set `ReadHeaderTimeout: 5s` and **not**
`ReadTimeout`, so they are all vulnerable the day this ships. "The consumer owns `ReadTimeout`" is
true and is also not the point: the consumer owned it yesterday too, when it did not matter.
The one lever the delivery offers — `WithMaxBodyBytes(0)` — **restores unbounded bodies**. So the
security control and the new DoS exposure are the *same switch*, and neither position is safe
without a knob this delivery does not touch.

**Fix (pick one, all cheap):**
1. Set `ReadTimeout` in all three `examples/` **in this bundle** (phase 4 already edits docs; this is
   3 lines) and make `SECURITY.md` say *"the cap makes an unset `ReadTimeout` exploitable where it
   previously was not"*, not *"the consumer owns `ReadTimeout`"*.
2. Bound the read in time as well as size: wrap the body in a reader that resets a deadline via
   `http.NewResponseController(w).SetReadDeadline(...)` — `statusRecorder` already implements
   `Unwrap()`, so the controller reaches the real writer. ~6 lines in a shared helper.
3. At minimum, **re-execute the residual with the discriminating fixture and restate it**: the
   current sentence is evidence-backed by a fixture that proves nothing.

---

### F2 — A THIRD wire break, unlisted: a truncated/over-declared body containing a complete JSON value goes 200 → 400
**Severity: MAJOR**

**Bundle says** (ADR §Consequences/Negative, plan phase 4 `CHANGELOG`): exactly **two** wire breaks —
> "(i) a new **413** on routes that previously returned 400, 500 or a spurious **2xx**; (ii) requests
> succeeding today via the trailing-byte gap now fail."

and plan phase 2 test 3 `TestAbortedUploadIsNotA413` only pins that the aborted upload is **not a
413**.

**The case it misses.** A request whose declared `Content-Length` exceeds what is delivered but whose
delivered prefix is a **complete JSON value**: `Content-Length: 60`, body `{"def_ref":"a:1"}`
(17 bytes), client half-closes. There are no trailing bytes and nothing is over the cap, so it is
neither break (i) nor break (ii).

**Evidence.** `zzfm/p1_test.go`, real `http.Server`, 64-byte cap:

```
TODAY: HTTP/1.1 200 OK          … TODAY-200 def_ref="a:1"
NEW  : HTTP/1.1 400 Bad Request … NEW-400 err=unexpected EOF
```

**Consequence.** Real producers hit this: a proxy that pads `Content-Length`, an HTTP/1.1 client that
resets after the payload, any half-closing client. They get 200 today and 400 tomorrow, and the
`CHANGELOG` will not have warned them. Worse, plan test 3 **encodes this break as the expected
result** without recording today's status — the narrow-fixture failure mode the spec's own §0 warns
about, reproduced in a prescribed test.

**Fix.** (a) Add break (iii) to the ADR Negative and the `CHANGELOG` row. (b) Amend plan test 3 to
**record today's status first** (200) and assert the new one (400) explicitly, per the same
"record today's behaviour first" instruction the plan already gives test 2. (c) Decide whether a
complete-value-then-read-error should be *accepted* (decode the buffer anyway when
`errors.Is(err, io.ErrUnexpectedEOF)`) — that would make the break disappear; state the choice.

---

### F3 — Residual 2 ("peak memory = `MaxBodyBytes` × in-flight") is numerically false on ALL THREE adapters, in two different directions
**Severity: MAJOR**

**Bundle says** (ADR §"The bound is on SIZE, not on TIME", ADR Negative, spec §4 row 13, plan phase 4
residual 2): "⚠ **Peak memory is `MaxBodyBytes × in-flight requests`, and nothing here bounds
concurrency.**"

**The case it misses — stdlib/gin.** `io.ReadAll` grows by doubling from a 512-byte seed. Executed
(`zzfm/p3_test.go`, `MaxBytesReader` at a 1 MiB cap):

```
body=1048576  -> len=1048576 cap=1048576 err=<nil>                        TotalAlloc delta=2228096 (2.12x cap)
body=1048577  -> len=1048576 cap=1048576 err=http: request body too large TotalAlloc delta=2228144 (2.12x cap)
body=3145728  -> len=1048576 cap=1048576 err=http: request body too large TotalAlloc delta=2228112 (2.12x cap)
```

Per accepted request the **cumulative** allocation is **2.12× the cap** and the **transient live**
peak is ~1.5× (new 1 MiB buffer alive alongside the 512 KiB predecessor during the copy). And note
row 3: **a rejected 3 MiB body costs the same 2.12 MiB** — the rejection path is not free, so the
attack the ADR celebrates catching now costs the server a full cap-sized allocation per attempt,
where today's streaming decode of a value-plus-trailing-bytes body allocated only the value.

**The case it misses — fiber.** Fiber's `len(c.BodyRaw())` pre-check runs **after fasthttp has
already materialised the whole body**. The memory bound on fiber is not `MaxBodyBytes` at all, it is
`fiber.Config.BodyLimit`, whose default is **4 MiB** (`fiber/v3@v3.4.0/app.go:585`
`DefaultBodyLimit = 4 * 1024 * 1024`, applied at `:709-710`, pushed to
`server.MaxRequestBodySize` at `:1516`). A fiber consumer on defaults gets **4 MiB × in-flight**,
i.e. **4× the stated figure**, and setting `WithMaxBodyBytes` lower does not reduce it by one byte.

**Consequence.** An operator sizing a container from the stated formula under-provisions by ~2× on
stdlib/gin and ~4× on fiber. The number is the only capacity guidance the delivery ships (there is
no metric — see F4), so it is load-bearing and it is wrong.

**Fix.** Restate as: *"stdlib/gin: ~2.1× `MaxBodyBytes` allocated per in-flight request (io.ReadAll
doubling), including per REJECTED request; fiber: `fiber.Config.BodyLimit` (default 4 MiB) per
in-flight request regardless of `MaxBodyBytes`, because fasthttp buffers before the handler runs."*
Optionally pre-size the buffer from `req.ContentLength` (clamped to the cap) to remove the doubling
— that is a `bytes.Buffer` + `Grow` instead of `io.ReadAll`, ~3 lines, and it also removes the
2.12× cost on the rejection path.

---

### F4 — "A consumer cannot measure before the cap bites" is false in the half that matters: rejections are ALREADY counted by the shipped `wrkflw_rest_requests_total`
**Severity: MAJOR**

**Bundle says** (ADR §"What this delivery deliberately does NOT do"; spec §3, §4 row 10; ADR
Negative): "**No instrumentation.** No histogram, no rejection counter. … ⚠ **Consequence, stated: a
consumer cannot measure their body-size distribution before the cap bites.** The migration lever is
`WithMaxBodyBytes(0)`, not a metric." Plan phase 4 will put this in `SECURITY.md`.

**The case it misses.** `httpcore.Instrumentation.Observe` (`observability.go`) already records
`wrkflw_rest_requests_total` **labelled `http.status_code`**, per static route, and all three
adapters already feed it the handler's final status — stdlib via `statusRecorder.code`
(`stdlib/observe.go:19-23`), gin via `gc.Writer.Status()` (`gin/observe.go:24-31`), fiber via
`c.Response().StatusCode()` (`fiber/observe.go:44`). Every 413 this delivery emits therefore appears
as `wrkflw_rest_requests_total{http.status_code="413", http.route="…"}` **with no new code**.

**Consequence.** The **deleted "rejection counter" was redundant, not homeless** — the deletion is
right and its stated reason ("only `httpcore` can build an instrument") is right, but the stated
*consequence* is wrong and is wrong in the direction that hurts consumers: a consumer told "you
cannot measure this" will not write the one-line alert
(`rate(wrkflw_rest_requests_total{http_status_code="413"}[5m]) > 0`) that they can write today.
Phase 4 is scheduled to copy this false sentence into `SECURITY.md`, where it will outlive the ADR.

**Fix.** Split the claim. Keep *"no body-size **distribution** ships, so you cannot see how close
you are to the cap before it bites"* — that is true and is the real gap. Delete *"not a metric"* and
replace with *"**rejections are already observable** as
`wrkflw_rest_requests_total{http.status_code=\"413\"}`; alert on it before and after rollout."*
This also gives the migration story (F7) the observability it currently claims not to have.

---

### F5 — `http.MaxBytesReader(w, …)` is handed the adapters' WRAPPED ResponseWriter, so its server-side signal never fires — and on gin it CANNOT be made to fire
**Severity: MAJOR**

**Bundle says** (ADR §Decision, plan phase 2): "`stdlib` / `gin`, **when capped**:
`io.ReadAll(http.MaxBytesReader(w, body, n))`". Spec §5's "boundaries DERIVED from source" table has
**eight** rows and **none** of them is *which `w`*.

**The case it misses.** `net/http`'s `maxBytesReader.Read` signals the server through an
**unexported-method** interface (`$GOROOT/src/net/http/request.go`):

```go
type requestTooLarger interface{ requestTooLarge() }
if res, ok := l.w.(requestTooLarger); ok { res.requestTooLarge() }
```

There is **no `Unwrap` traversal**. But the `w` a wrkflw handler receives is never the raw
`*http.response`: stdlib's `observe` hands the handler `&statusRecorder{ResponseWriter: w}`, and gin
hands `gc.Writer`, a `gin.ResponseWriter`. Neither satisfies the assertion — and because
`requestTooLarge` is an **unexported method name declared in `net/http`**, no type outside `net/http`
can ever implement it. `statusRecorder` has an `Unwrap()` so stdlib can recover the raw writer;
**gin's `ResponseWriter` interface exposes no way to reach the underlying `http.ResponseWriter`**, so
on gin the signal is unreachable, full stop.

**Evidence.** `zzfm/p3_test.go`, real `http.Server`, 64-byte cap, 4 KiB body:

```
RAW ResponseWriter                             Connection:close present=true
WRAPPED (statusRecorder, as the adapter does)  Connection:close present=false
```

**Consequence.** `requestTooLarge()` sets `closeAfterReply`. Without it the 413 goes out **without
`Connection: close`**, so (a) the client believes the connection is reusable and may pipeline the
next oversize request onto it, (b) net/http instead falls back to its post-handler drain, reading up
to 256 KiB of the rejected body off the wire *after* rejecting it, and (c) for a body with more than
256 KiB remaining the connection is dropped **abruptly with no `Connection: close`**, which several
HTTP clients treat as a retryable transport error — turning one rejected upload into a retry storm.
None of this is in the bundle, and stdlib and gin will behave differently from each other.

**Fix.** In stdlib, unwrap before capping:
`if u, ok := w.(interface{ Unwrap() http.ResponseWriter }); ok { w = u.Unwrap() }` — `statusRecorder`
already provides `Unwrap()` for exactly this reason. On gin, accept that the signal is unreachable
and **set `Connection: close` explicitly on the 413 response**; then say so in the ADR and add a
parity note, because it is a per-adapter divergence the delivery introduces.

---

### F6 — The `n <= 0` disable sentinel is specified for stdlib and gin and NOT for fiber; the naive fiber implementation INVERTS it
**Severity: CRITICAL**

**Bundle says.** ADR §Decision: "`stdlib` / `gin`, **when disabled** (`n <= 0`): do not install the
wrapper…" then, as a separate bullet with **no disabled clause**: "`fiber`: a `len(c.BodyRaw())`
pre-check before `c.Bind().JSON`". Plan §2's decision→phase map is explicit:

| ADR sentence | phase | package |
|---|---|---|
| `n <= 0` ⇒ **do not install the wrapper**; decode from the wire as today | 2 | `stdlib` \| `gin` |

**`fiber` is absent from that row.** `grep -in 'disab\|n <= 0' … | grep fiber` over the ADR and the
plan exits **1** — verified in this worktree.

**The case it misses.** The plan's own §2 rule is *"Every sentence of the ADR's Decision section has a
row. **A row with no phase is a defect**"*. Here the inverse happened: a phase row exists that
covers two of the three packages that need it. A fiber agent handed
`len(c.BodyRaw()) > cfg.MaxBodyBytes` and no disabled clause writes the obvious thing, and with
`MaxBodyBytes = 0` **every body of one byte or more returns 413** — the cap's *disable* switch
becomes a *reject-everything* switch. This is precisely the `MaxBytesReader(w, body, 0)` inversion
the bundle executed, found, and fixed **for the other two adapters only**.

**And no prescribed test catches it.** Plan phase 2 test 6 is
`TestDisabledCapDoesNotInstallTheReader` — its name and its falsifier (*"fails against an
implementation that passes `0` to `MaxBytesReader`"*) are both written about a mechanism fiber does
not have. A fiber agent will either skip it or rewrite it against whatever they built.

**Consequence.** `WithMaxBodyBytes(0)` is the delivery's **only** migration lever (ADR Negative) and
its only escape from the new slowloris exposure (F1). On fiber it would take the service from
"bodies capped at 1 MiB" to "**every request with a body returns 413**" — a total outage, reached by
following the documented remedy.

**Fix.** Add the missing sentence to the ADR Decision and the missing `fiber` cell to plan §2:
*"fiber, when disabled (`n <= 0`): skip the pre-check entirely."* Rename plan test 6 to
`TestDisabledCapAcceptsAnOversizeBody` (mechanism-neutral) and give the fiber variant its own
falsifier: *"fails against a pre-check that compares against a non-positive cap."*

---

### F7 — The disabled path is a second, untested code path, and the prior art the ADR tells implementers to copy contains the exact hazard the ADR forbids
**Severity: MAJOR**

**Bundle says.** ADR §Decision: "`stdlib` / `gin`, **when disabled** (`n <= 0`): do not install the
wrapper; decode straight from the wire, exactly as today. **An unbounded `io.ReadAll` is itself a
memory-exhaustion primitive.**" And ADR §Context / spec §4 row 5 / plan §1: adopt
`action/httpcall`'s convention, citing `httpcall.go:186-194,214`.

**The case it misses.** The cited prior art is `readAllCapped`
(`action/httpcall/httpcall.go:190-202`), and its disabled branch is:

```go
func readAllCapped(r io.Reader, max int64) ([]byte, error) {
	if max <= 0 {
		return io.ReadAll(r)          // ← unbounded
	}
	...
}
```

An implementer told to "adopt the existing convention exactly", pointed at this function, will copy
its shape — and the disabled branch is the unbounded `io.ReadAll` the ADR names as a
memory-exhaustion primitive two sections earlier. **The only prescribed test for the disabled path
(phase 2 test 6) passes against the wrong implementation**: it asserts a 3 MiB body *succeeds* with
`WithMaxBodyBytes(0)`, and an unbounded `io.ReadAll` succeeds too. Its stated falsifier only
discriminates against `MaxBytesReader(…, 0)`.

**Consequence.** The disabled configuration — which the ADR names as the migration lever and which
F1 makes the only slowloris escape — can silently ship as *worse* than today (a whole-body buffer
with no limit at all instead of a streaming decode), and no test in the plan can tell.

**Fix.** Add a falsifier that discriminates the *mechanism*, not the outcome: with
`WithMaxBodyBytes(0)`, assert the handler responds **before** the body finishes arriving (the
streaming decode returns at the first JSON value; an `io.ReadAll` does not) — the F1 dribble fixture
inverted. And add a one-line warning at the `httpcall.go` citation in the ADR/plan: *"copy the
sentinel convention, NOT `readAllCapped`'s disabled branch."*

---

### F8 — No `Content-Length` pre-check: every oversize request is read and allocated in full before it is rejected, and the bundle never considers the header
**Severity: MAJOR**

**Bundle says.** Nothing. `grep -in 'content-length'` over the three bundle documents returns
**six** hits, and **all six** are about the *over-declared* `Content-Length` producing
`unexpected EOF` (the `errors.As` discriminator argument). `Content-Length` as a **cheap early
rejection** is never mentioned, and it is not in spec §5's derived-boundaries table.

**The case it misses.** `http.MaxBytesReader` ignores `ContentLength` entirely — verified from
`$GOROOT/src/net/http/request.go`, the struct is `{w, r, i, n, err}` and `Read` never consults the
request. So a request declaring `Content-Length: 1073741824` at a 1 MiB cap is read for a full
1 MiB, allocated at 2.12 MiB (F3), and only then rejected. A one-line
`if n > 0 && req.ContentLength > n { → 413 }` rejects it at **zero** wire and zero heap cost,
and it is what every hardened net/http service does.

**Consequence.** Under a flood of oversize requests the server does ~1 MiB of socket reads and
~2.1 MiB of allocation per rejection instead of ~0. Combined with F4's "you cannot measure", the
operator sees GC pressure and no signal. On fiber the equivalent is already handled by fasthttp
(`MaxRequestBodySize` checks the declared length), so this also makes the three adapters diverge in
cost by orders of magnitude for the same attack.

**Fix.** Add to the ADR Decision, for stdlib and gin: *"when capped, reject before reading if
`req.ContentLength > n`; `MaxBytesReader` remains the authority for chunked and for a lying
`Content-Length`."* Prescribe a test that a request with an over-declared oversize `Content-Length`
and a **short** body still returns 413 (proving the pre-check fired) and one with `ContentLength ==
-1` (chunked) still returns 413 (proving the reader is still installed).

---

### F9 — Three claims about what the 413 carries are wrong or incomplete: it DOES ship a message, the message leaks an internal package prefix, and it never tells the client the limit
**Severity: MAJOR**

**Bundle says.** Spec §4 coupling row 3: "⚠ This delivery ships **no** 413 message text, so there is
nothing here for that delivery to contradict." ADR Negative: "**A 413 carries no correlation id and
produces no log record.**"

**The case it misses.** `ClassifyError`'s 4xx arms all set `Message: err.Error()`
(`httpcore/errors.go:31,33,35,50,56`). The 413 arm will be no different, and the adapter returns the
**bare** sentinel — so the wire body is
`{"error":"…","message":"workflow-httpcore: request body too large"}`. That **is** 413 message text,
authored by this delivery, and it carries the internal `workflow-httpcore:` package prefix to
untrusted clients. Coupling row 3's "nothing here for that delivery to contradict" is therefore
false: the deferred 4xx delivery will have to change a string this delivery shipped.

Second: `*http.MaxBytesError` carries `Limit`, and the design deliberately discards it by returning a
bare sentinel. The client is told it sent too much and **not how much is allowed**.

Third, and the operational one: combine the three stated/derived facts — no correlation id, **no log
record** (`writeErr` logs only at `status >= 500`, `stdlib/write.go:32`, and this delivery does not
change it), and no limit in the message. When a consumer's client reports "we started getting 413s",
the server side has **one** artifact: a counter increment (F4). There is no per-request record
anywhere that says which request, from whom, or how big. The ADR states the pieces; it never states
the composite, which is the thing an on-call engineer actually hits.

**Fix.** (a) Correct coupling row 3 — say what message text ships and hand the string to the
deferred 4xx delivery as a known break. (b) Put the limit in the message
(`"request body exceeds N bytes"`) — it is client-safe, it is the client's own constraint, and it
removes the support round-trip; if that is judged 4xx-delivery scope, say so explicitly rather than
leaving it unmentioned. (c) Log the 413 at `Warn` with route and declared `Content-Length`; the
delivery already carries `cfg.Logger` at every `writeErr` call site and "no logging change" is a
self-imposed rule, not a constraint.

---

### F10 — The cap is per-`Customize`-call, and the repo's own godoc steers consumers into mounting admin routes separately — so the three discarding sites get a DIFFERENT cap than the rest of the API
**Severity: MINOR**

**Bundle says.** ADR Negative and spec §4 row 8 discuss only `MountGroups`: "**`MountGroups`
consumers get the default cap** … the escape is `Customize` directly."

**The case it misses.** The much more common path. `stdlib.Mount`'s own godoc
(`stdlib/mount.go:14-16`) says: *"Admin and health routes are intentionally excluded so consumers can
choose whether and where to mount them — typically on a separate, access-controlled mux. Use
[AdminRoutes.Customize] and [MountHealth]."* A consumer following that advice makes **two** calls,
and the cap must be repeated in both. `examples/production_wiring/main.go:264,274` is the shape:
`stdlib.Mount(mux, svc, WithMeterProvider(...))` and
`stdlib.AdminRoutes{Svc: svc}.Customize(adminMux, WithMeterProvider(...))` — two independent option
lists that today happen to match and that nothing keeps in sync.

**Consequence.** A consumer who raises the cap on the core API and forgets the admin call leaves the
admin group — **which is where all three discarding sites live** — on 1 MiB. The failure is silent
(no mount-time validation was deleted precisely because no channel exists) and shows up as a 413 on
an admin route only.

**Fix.** One sentence in `SECURITY.md`'s residual 4: *"`MaxBodyBytes` is per-`Customize` call. If you
mount admin or health routes separately (as `Mount`'s godoc recommends), pass the same
`WithMaxBodyBytes` to every call — nothing reconciles them."* Zero code.
---

### F6 (addendum) — the fiber disable inversion is EXECUTED, not reasoned
**Severity: CRITICAL (evidence upgrade for F6)**

`zzfm/p4_test.go`, a real `fiber.New()` app with default config, the pre-check written the obvious
way (`if int64(len(c.BodyRaw())) > ourCap { 413 }`):

```
ourCap=8388608  body=1048593 -> status=200  "OK wire=1048593 cap=8388608"
ourCap=0        body=1048593 -> status=413  "OUR-413 wire=1048593 cap=0"      ← DISABLE = REJECT-ALL
```

With `WithMaxBodyBytes(0)` — the delivery's documented migration lever and its only slowloris escape
(F1) — a perfectly ordinary 1 MiB request returns **413 on fiber**. F6 is not a hypothetical
implementer error; it is what the ADR's own prescription produces when read literally.

---

### F11 — `fiber.Config.BodyLimit` is the REAL ceiling on fiber: a `MaxBodyBytes` above it is silently ignored, and `WithMaxBodyBytes(0)` does NOT disable the cap on fiber at all
**Severity: MAJOR**

**Bundle says** (ADR §"What this delivery deliberately does NOT do", spec §4 row 14, plan phase 4
residual 5): "**Fiber diverges above `fiber.Config.BodyLimit`**: framework plain-text 413, no
`ErrorBody`, no log, and no WARN to tell the consumer. Documented only."

**The case it misses.** The residual describes the *shape* of the divergence and stops there. It
never states the two consequences that matter:

1. **A cap larger than fiber's own is not honoured.** `fiber.Config.BodyLimit` defaults to **4 MiB**
   (`app.go:585`, applied at `:709-710`). `WithMaxBodyBytes(8<<20)` therefore yields an effective
   cap of 4 MiB on fiber and 8 MiB on stdlib/gin — the consumer's explicit configuration is silently
   overridden on one of three adapters. The mount-time WARN that would have surfaced this is
   **deleted by this same delivery**, so nothing tells them.
2. **`WithMaxBodyBytes(0)` does not disable anything on fiber.** The escape hatch the ADR names as
   the migration lever, and F1 makes the only slowloris remedy, leaves fiber capped at 4 MiB.

**Evidence.** `zzfm/p4_test.go`, real `fiber.New()`, default config:

```
ourCap=8388608  body=5242897  app.Test err="body size exceeds the given limit"   (never reaches the handler)
ourCap=0        body=5242897  app.Test err="body size exceeds the given limit"   (disabled — still rejected)
```

**Consequence.** "Set `WithMaxBodyBytes(0)` to restore the old behaviour" is stated as an unqualified
migration instruction in the ADR Negative, spec §3, and (scheduled) `SECURITY.md`. **It is false on
fiber for any body above 4 MiB.** A consumer whose 6 MiB payloads worked before this delivery cannot
restore them with the documented lever; they must also raise `fiber.Config.BodyLimit`, which the ADR
correctly notes a mounted `*fiber.Group` cannot reach — so they must change their `fiber.New()` call,
which the migration note never tells them.

**Fix.** Rewrite residual 5 as an **effective-cap** statement, not a divergence statement:
*"On fiber the effective cap is `min(MaxBodyBytes, fiber.Config.BodyLimit)`, and `MaxBodyBytes <= 0`
leaves `fiber.Config.BodyLimit` (default 4 MiB) in force. To raise or remove the cap on fiber you
must also set `fiber.Config.BodyLimit` on your `fiber.New()`."* Same sentence in `SECURITY.md` and
in the `CHANGELOG` migration note. Zero code.

---

### F12 — No prescribed test pins the cap BOUNDARY (exactly `n`, `n+1`), so fiber's `>` vs `>=` can diverge from stdlib/gin by one byte and parity will not see it
**Severity: MINOR**

**Bundle says.** Plan §0 item 1 lists *"exactly at the cap; cap+1"* among fixtures **the audit**
should add. Plan §3 phase 2's actual test list (tests 1–8) contains **no** boundary case, and phase 3
parity asserts only that the three agree "on **413** for every **over-cap** shape, and are unchanged
**under** the cap" — the boundary itself is in neither set.

**The case it misses.** `http.MaxBytesReader(w, body, n)` accepts a body of **exactly** `n` and
rejects `n+1` — executed, `zzfm/p3_test.go`: `body=1048576` at a 1 MiB cap → `err=<nil>`,
`body=1048577` → `http: request body too large`. Fiber's pre-check must therefore be
`len(c.BodyRaw()) > n`. Written as `>=` — an ordinary off-by-one, and the natural reading of "reject
at the limit" — fiber rejects a body stdlib and gin accept, at exactly the default 1 MiB.

**Consequence.** A silent one-byte parity break at precisely the size an integration test would use
to probe the limit, invisible to every prescribed test.

**Fix.** Add a row to phase 2 test 1 (or a small table): `n-1` → accepted, `n` → accepted, `n+1` →
413, **on all three adapters**, and add the same three sizes to phase 3 parity. Falsifier, stated:
*the `n` row fails against a `>=` comparison.*

---

## Verdict on the five stated residuals

The instruction was to judge whether **stating** them is defensible. Per residual:

| # | residual | true? | complete? | verdict |
|---|---|---|---|---|
| 1 | consumer owns `ReadTimeout` | true | **no** | **F1 — not defensible as written.** The delivery *creates* the exposure; the fixture the ADR executed (chunked, no terminator) is the one where old and new behave the same, so the residual's evidence does not support its claim. Net-negative on this axis until `examples/` set `ReadTimeout`. |
| 2 | peak memory = cap × in-flight | **no** | no | **F3 — restate.** ~2.1× on stdlib/gin (including per rejection), `BodyLimit` (4 MiB) on fiber. |
| 3 | 413 has no correlation id, no log | true | **no** | **F9 — incomplete.** It also ships a message it claims not to ship, leaks `workflow-httpcore:`, omits the limit, and — composed with F4's mistaken "no metric" — leaves the operator with no per-request artifact at all. |
| 4 | `MountGroups` gets the default | true | **no** | **F10 — the common case is `Mount` + a separate `AdminRoutes.Customize`**, which is what the repo's own godoc and `examples/production_wiring` do, and it is where the three discarding sites live. |
| 5 | fiber diverges above `BodyLimit` | true | **no** | **F11 — not defensible as written.** The real statement is that `BodyLimit` is the effective ceiling, so a larger cap is ignored and `WithMaxBodyBytes(0)` does not disable on fiber. This falsifies the migration instruction. |

**Two of five residuals are false as stated (2, 5); three of five are true but omit the consequence
that makes them matter (1, 3, 4).** Stating a residual is only defensible when the statement is the
*whole* residual — otherwise it converts an unknown risk into an accepted-and-mis-sized one, which is
worse, because nobody re-derives an accepted risk.

## Cross-cutting: the delivery has exactly ONE lever and it is broken on two axes

`WithMaxBodyBytes(0)` is named as the migration lever (ADR Negative, spec §3, §6), and F1 makes it
the only slowloris remedy. It is: **the reject-everything switch on fiber** (F6, executed); **a
no-op above 4 MiB on fiber** (F11, executed); **an all-or-nothing revert** that also restores the
trailing-byte 2xx the delivery exists to close; and it is at risk of shipping as an unbounded
`io.ReadAll` (F7). Any one of these is survivable. Together they mean the delivery ships a
default-on breaking change whose documented escape hatch does not work on one of three adapters.

# Audit round 6 (ADR-0186, one decision) — EXECUTION lens

**Worktree:** detached at `85d6bb68`. **Step 0:** all five bundle files PRESENT (256 / 137 / 331 / 637 / 499 lines).
**Method:** for every probe the bundle already ran and passed, ask *"did it exercise the thing it claims about?"* and widen the fixtures.

Deferred decisions (4xx message policy, at-rest posture, read-path disclosure, `httpcall` SSRF,
variable-map bound) and ADR-0185 identity material are **out of scope** and are not audited here.

---
### E1 — "negative → a construction error, surfaced at mount time" has NO CHANNEL to surface on

**Severity:** Critical
**Bundle says:**
- ADR §Decision: *"**negative → a construction error**, surfaced at mount time rather than per request."*
- Plan §2 decision→phase map row: *"negative `MaxBodyBytes` is a construction error at mount | 1 | `httpcore`"*
- Plan §3 phase 1 test 3: *"`TestNegativeMaxBodyBytesIsRefusedAtMount` — the construction error is reachable."*
- Spec §6 Non-goals + ADR Consequences: *"**No new exported interface and no new cross-package contract**"* / *"no new exported interface"*.

**I ran:**
```
go doc ./transport/http/httpcore RouteCustomizer
go doc ./transport/http/httpcore ResolveConfig
go doc ./transport/http/stdlib Mount
grep -rn 'func .*Customize(' transport/http/ | grep -v _test
grep -rn 'func Mount' transport/http/ | grep -v _test
```
**Observed:**
```
type RouteCustomizer[R any] interface {
	Customize(r R, opts ...CustomizeOption[R])          <-- no error return
}
func ResolveConfig[R any](opts ...CustomizeOption[R]) CustomizeConfig[R]   <-- no error return
func Mount(mux *http.ServeMux, svc service.Service, opts ...httpcore.CustomizeOption[*http.ServeMux])  <-- no error return
```
All **15** `Customize` methods (5 route groups x 3 adapters) and all **6** `Mount`/`MountHealth`
functions return **nothing**. `CustomizeOption[R]` is `func(*CustomizeConfig[R])` — also no error.

**Verdict:** CONFIRMED-DEFECT.

There are exactly three ways to satisfy the ADR sentence and the bundle forbids or omits all three:
1. change `RouteCustomizer.Customize` / `ResolveConfig` / `Mount` to return an `error` — a **new
   exported contract** and a **breaking API change** for every consumer implementing
   `RouteCustomizer[R]` (the seam doc comment explicitly invites consumers to implement it:
   *"any RouteCustomizer[R] — including a consumer's own"*). Spec §6 forbids exactly this.
2. **panic** at mount — never named anywhere in the bundle, and a panic is not "a construction error".
3. silently clamp/ignore — which is what the plan's test 3 rationale says must NOT happen
   (*"Without this, a negative value silently reaches `MaxBytesReader` and behaves like `0`"*).

The plan's `TestNegativeMaxBodyBytesIsRefusedAtMount` is therefore **unwritable as specified**: there
is no value for the test to assert on. This is the same shape as round 3's F19 (a classifier written
before the sentinel it routes) — a prescribed test with no surface.

**Fix:** pick one and write it into the ADR *and* the spec's non-goals:
(a) the honest minimum — `ResolveConfig` **clamps a negative to the `nil` default (1 MiB) and logs a
WARN through `cfg.Logger`**, and the test asserts the WARN + the effective 1 MiB cap (fail-closed,
no API change, testable); or
(b) accept the breaking change: add `ResolveConfigE` returning `(CustomizeConfig[R], error)` and have
each adapter's `Customize` fall back to defaults + WARN — but then the spec's *"no new exported
interface"* claim is false and must be deleted. Option (a) is consistent with the rest of the bundle;
option (b) contradicts a stated non-goal.

---
### E2 — read-before-parse creates a NEW stdlib↔gin divergence on UNDER-CAP bodies, and it is invisible to every prescribed test

**Severity:** Critical
**Bundle says:**
- ADR §Decision: *"`stdlib` and `gin`: read the body through `http.MaxBytesReader` to completion
  (`io.ReadAll`), **then unmarshal from the resulting buffer**."*
- ADR §Consequences Positive: *"closes on all **39** sites with **one** policy and **one** status —
  and, unlike the previous revision, that sentence is true for **malformed** and **trailing-byte**
  bodies too."*
- Evidence §7.1 Result 3: *"the gin buffer-and-reset works … so gin's `ShouldBindJSON` and its
  validation are preserved rather than bypassed."*

**I ran:** `transport/http/parity/zz_exec_probe_test.go` (throwaway, deleted). Real
`httpcore.StartInput`, real `http.MaxBytesReader`, real `gc.ShouldBindJSON`, real `c.Bind().JSON`,
`gin.ReleaseMode`, cap = 64 bytes. 16 fixtures × {stdlib TODAY, stdlib PROPOSED, stdlib
PROPOSED-unbounded, gin TODAY, gin PROPOSED, fiber TODAY, fiber PROPOSED}.
`go test -count=1 -run '^TestProbeMatrix$' -v ./transport/http/parity/` → **EXIT=0**.

**Observed** (the rows the bundle never fixtured — all **under** the cap, so the cap is irrelevant):

```
=== 05 value+trailing-UNDER-cap  `{"def_ref":"a:1"} zz`   (len=20, cap=64)
  stdlib TODAY      : 200 | 200 def_ref="a:1"
  stdlib PROPOSED   : 400 | 400 invalid character 'z' after top-level value     <-- CHANGED 200→400
  gin    TODAY      : 200 | 200 def_ref="a:1"
  gin    PROPOSED   : 200 | 200 def_ref="a:1"                                   <-- UNCHANGED
  fiber  TODAY      : 400 | 400 bind from body: invalid character 'z' after top-level value

=== 11 multiple-json-values-under-cap  `{"def_ref":"a:1"} {"def_ref":"b:2"}` (len=35, cap=64)
  stdlib TODAY      : 200 | 200 def_ref="a:1"
  stdlib PROPOSED   : 400 | 400 invalid character '{' after top-level value     <-- CHANGED 200→400
  gin    TODAY      : 200 | 200 def_ref="a:1"
  gin    PROPOSED   : 200 | 200 def_ref="a:1"                                   <-- UNCHANGED
  fiber  TODAY      : 400 | 400 bind from body: invalid character '{' after top-level value
```

**Verdict:** CONFIRMED-DEFECT. Three separate problems in one row.

1. **`json.Unmarshal` and `ShouldBindJSON` are not the same parser.** `json.Unmarshal` **rejects
   trailing data**; gin's `ShouldBindJSON` calls `json.NewDecoder(r.Body).Decode(obj)` internally, so
   after the buffer-and-reset it **still ignores trailing data**. The ADR prescribes "unmarshal from
   the buffer" for stdlib and "buffer + reset" for gin and treats them as the same fix. They are not.
   After the change stdlib says 400 and gin says 200 for the **same** body. The delivery's headline
   sentence — *"one policy, one status … true for trailing-byte bodies too"* — is **false for every
   trailing-byte body under the cap**, which is the common case, not the exotic one.
2. **The stdlib 200→400 change is an unlisted BREAKING change.** ADR §Consequences Negative lists two
   breaks and the second is *"requests that succeed today via the trailing-byte gap begin failing
   with **413**"*. Executed: under the cap they fail with **400**, not 413, and only on stdlib.
   A consumer posting `{...}\n` with a stray byte gets a new 400 on stdlib and nothing on gin.
3. **Nothing in the bundle can catch it.** Plan phase 2 test 2's three fixtures are all **oversize**;
   phase 3's parity fixtures are the same three. I read `TestParity_ErrorEnvelopes` — its
   *"400 empty JSON body"* case posts `map[string]any{}`, i.e. the **valid** body `{}`, which never
   reaches the decode error path at all. **No existing or prescribed test fixtures an under-cap
   trailing-byte body.**

**Fix (pick one, and say which in the ADR):**
- **(a) Make the parse uniform.** Both stdlib and gin decode the buffer with
  `d := json.NewDecoder(bytes.NewReader(buf)); d.Decode(&in); then require d.Token() == io.EOF` —
  the trailing-data guard `runtime/kernel/cursorcodec.go:50-58` already implements (ADR-0160), which
  the bundle cites as prior art but then does not actually reuse. gin must stop using
  `ShouldBindJSON` on the capped path, or the guard must be applied to the buffer *before*
  `ShouldBindJSON`. Then all three agree at 400 and the ADR's sentence becomes true.
- **(b) Keep `Decode` semantics on both** (`json.NewDecoder(bytes.NewReader(buf)).Decode`) — stdlib
  stays 200, gin stays 200, fiber stays 400, i.e. the pre-existing fiber divergence survives and the
  ADR must delete *"true for … trailing-byte bodies too"*.
- Either way: **add an under-cap trailing-byte fixture to phase 2 test 2 and to phase 3 parity**, and
  correct the Consequences bullet from "413" to the status the chosen option actually produces.

---
### E3 — decode-ERROR TEXT diverges stdlib↔gin after the change, on ordinary malformed bodies

**Severity:** Major
**Bundle says:** Plan §3 phase 3: *"Check that `TestParity_ErrorEnvelopes`'s existing byte-for-byte
guarantee still holds and say so — this delivery adds no correlation id."* ADR §Consequences:
*"`ClassifyError`'s signature is not changed"* and no message change is contemplated
(spec §6 Non-goals: *"No change to what any 4xx message says"*).

**I ran:** same probe.
**Observed** (all under the cap):
```
=== 09 empty-body-zero-bytes
  stdlib TODAY    : 400 | 400 EOF
  stdlib PROPOSED : 400 | 400 unexpected end of JSON input     <-- message CHANGED
  gin    PROPOSED : 400 | 400 EOF                              <-- message UNCHANGED  => DIVERGE
=== 14 truncated-body  `{"def_ref":"a:1`
  stdlib TODAY    : 400 | 400 unexpected EOF
  stdlib PROPOSED : 400 | 400 unexpected end of JSON input     <-- message CHANGED
  gin    PROPOSED : 400 | 400 unexpected EOF                   <-- DIVERGE
=== 15 whitespace-only
  stdlib PROPOSED : 400 | 400 unexpected end of JSON input   vs  gin PROPOSED : 400 | EOF   => DIVERGE
```
**Verdict:** CONFIRMED-DEFECT. Switching stdlib from `Decode` to `Unmarshal` changes the 400
**message text** on three ordinary body shapes — which is exactly what spec §6 declares a non-goal —
and it does so **only on stdlib**, so the byte-for-byte envelope parity the plan asks to "check still
holds" is broken by the change. It does not fail today's parity suite only because no case fixtures
an empty/truncated body (the *"400 empty JSON body"* case posts `{}`). So this ships silently.
**Fix:** covered by E2 fix (a)/(b) — use a `json.Decoder` over the buffer on stdlib so the messages
are unchanged, and **add empty-body and truncated-body cases to `TestParity_ErrorEnvelopes`** so the
guarantee the plan asks about is actually asserted rather than asserted-about.

---
### E4 — ⭐⭐⭐ the plan DELETES the only discriminator: a non-nil read error is NOT proof of oversize

**Severity:** Critical
**Bundle says:**
- Plan §3 phase 2: *"**3 discarding sites** … : the read's own error now distinguishes *absent/EOF*
  (keep ignoring) from *oversize* (bare `ErrRequestBodyTooLarge` → 413). ⚠ **This replaces the
  previous revision's `errors.As(err, new(*http.MaxBytesError))` shape**, which diverged per adapter."*
- ADR §Decision: *"Under the read-before-parse rule this is simply the read's own error, **which
  removes** the previous revision's `errors.As(err, new(*http.MaxBytesError))` shape."*

**I ran:** `transport/http/parity/zz_exec_probe2_test.go` (throwaway, deleted) —
`TestProbeReadErrorDiscrimination`: a real `httptest.NewServer`, raw HTTP/1.1 over `net.Dial`,
handler = `io.ReadAll(http.MaxBytesReader(w, r.Body, 64))`.
`go test -count=1 -run '^TestProbeReadErrorDiscrimination$' -v ./transport/http/parity/` → **EXIT=0**.

**Observed:**
```
CL-too-large(40 vs 10)   read n=10 err=unexpected EOF                 errors.As(*MaxBytesError)=FALSE  errors.Is(io.ErrUnexpectedEOF)=true
CL-too-small(5 vs 20)    read n=5  err=<nil>                          errors.As(*MaxBytesError)=false
chunked-oversize-100     read n=64 err=http: request body too large   errors.As(*MaxBytesError)=TRUE
chunked-under-cap-10     read n=10 err=<nil>                          errors.As(*MaxBytesError)=false
```

**Verdict:** CONFIRMED-DEFECT. `io.ReadAll` over a `MaxBytesReader` returns a **non-nil error that is
not a `*http.MaxBytesError`** whenever the peer aborts mid-body or over-declares `Content-Length` —
an everyday event on a public endpoint (mobile client drops, proxy timeout, cancelled upload).
Two concrete breakages follow:

1. **A truncated upload is reported as `413 Payload Too Large`.** The ADR's rule *"the read's own
   error ⇒ `ErrRequestBodyTooLarge` ⇒ 413"* has no case for `unexpected EOF`. The correct status for
   a client-aborted body is 400 (the caller can retry), not 413 (the caller must shrink). This is the
   same class of defect as the trailing-byte 2xx the delivery exists to fix — a status that lies.
2. **The three discarding sites lose the very distinction the ADR promises them.** The plan's rule is
   *"absent/EOF ⇒ keep ignoring"*. Executed: an **absent or empty** body yields `err == nil, n == 0`
   (probe 1 fixtures 09/10), so "EOF" is not an error there at all; but a **truncated** body yields
   `err = unexpected EOF`, which the rule classifies as oversize ⇒ the optional-body admin route
   `POST /admin/instances/{id}/incidents/{incidentID}/resolve` starts returning **413** for a
   truncated body it ignores today. The plan's phase-2 test 5
   (`TestBodyAbsentOnTheOptionalRouteStillSucceeds`) uses an **absent** body and therefore **cannot
   fail against this bug** — it is a vacuous control for the case that actually breaks.

The stated justification for deleting `errors.As` — *"which diverged per adapter"* — does not hold:
the divergence was that **fiber** has no `MaxBytesReader` at all (it uses `len(c.BodyRaw())`), which
is unchanged by read-before-parse. stdlib and gin still both need `errors.As`, and probe 1 confirms
both produce the identical bare `*http.MaxBytesError`.

**Fix:** restore the discriminator, and say why in the ADR:
```go
buf, err := io.ReadAll(http.MaxBytesReader(w, r.Body, n))
switch {
case err == nil:                                    // fall through to parse
case errors.As(err, new(*http.MaxBytesError)):      // -> bare ErrRequestBodyTooLarge -> 413
default:                                            // -> ErrBadInput -> 400 (aborted/truncated read)
}
```
and at the three discarding sites: `err == nil` ⇒ ignore (covers absent, empty, zero-byte);
`*http.MaxBytesError` ⇒ 413; **any other read error ⇒ keep ignoring** (it is the optional-body route)
— but state which, because the two are not the same and the bundle currently states neither.
Add a phase-2 fixture **"body truncated mid-read (Content-Length over-declared)"** to tests 3 and 5;
today neither can fail against this.

---
### E5 — `http.MaxBytesReader` DOES have a response side effect: it forces `Connection: close`

**Severity:** Major
**Bundle says:** ADR §Decision: *"`c.BodyRaw()` (`req.go:92-96`) is the un-decompressed wire body
**with no response side effect**"* — stated for fiber, and the 413 mechanics rely on the stdlib/gin
reader not having written a status before `writeErr` runs. No cost bullet mentions connection churn.

**I ran:** `TestProbeMaxBytesResponseSideEffect` — real `httptest.NewServer`, cap 4, 50-byte body,
handler inspects `w.Header()` immediately after the read, then writes its own 413.
**Observed:**
```
after-read err=http: request body too large headers=map[Connection:[close]]
client saw: status=413 close=true conn-hdr="" body="{\"error\":\"request_too_large\"}"
```
**Verdict:** BUNDLE-CORRECT on the load-bearing half, CONFIRMED-DEFECT on completeness.
*Correct:* `MaxBytesReader` writes **no status** and no body, so the adapter's own 413 + `ErrorBody`
is delivered intact — the ADR's implicit premise holds, and I confirmed it rather than assuming it.
*Missing:* it **does** mutate the response — `Connection: close` is set on trip, and the client
observes `resp.Close == true`. Every 413 therefore **tears down the keep-alive connection**. On a
route being probed with oversize bodies this converts a cheap rejection into a TCP/TLS handshake per
request — i.e. the new control has a modest **amplification** property of its own. It is not a reason
to change the design (`MaxBytesReader` is still right), but it is an unlisted operational cost, and
it interacts with the rejection counter: the counter will correlate 1:1 with connection churn.
**Fix:** add a Consequences/Negative bullet — *"a 413 from the stdlib and gin adapters closes the
connection (`http.MaxBytesReader` sets `Connection: close`); fiber's `BodyRaw()` pre-check does not,
so the three adapters diverge in connection lifetime even though they agree on status."* ⚠ Note that
last clause: it is a **fourth** stdlib/gin-vs-fiber divergence introduced by this delivery, and the
parity suite compares status and body only, so nothing will catch it.

---
### E6 — the cap's INCLUSIVITY is never stated, and stdlib/gin vs fiber use different operators

**Severity:** Minor
**Bundle says:** ADR: *"`n > 0` → **cap at n wire bytes**"*; fiber uses `len(c.BodyRaw())`, stdlib/gin
use `http.MaxBytesReader(..., n)`. Neither the ADR, the spec, nor any prescribed test says whether a
body of **exactly** n bytes is accepted.
**I ran:** `TestProbeMaxBytesEdges` (limits -1/0/1/5 × body lengths 0/1/5/6) and probe 1 fixtures
06 (exactly-at-cap, 64 B), 07 (cap+1, 65 B), 08 (cap-1, 63 B).
**Observed:**
```
limit=5 bodylen=5 -> read="abcde" err=<nil>                       (exactly n is ACCEPTED)
limit=5 bodylen=6 -> read="abcde" err=http: request body too large (n+1 REJECTED)
06 exactly-at-cap : stdlib PROPOSED 200 | gin PROPOSED 200 | fiber PROPOSED 200
07 cap-plus-1     : stdlib PROPOSED 413 | gin PROPOSED 413 | fiber PROPOSED 413
limit=0  bodylen=0 -> err=<nil>   /  limit=-1 bodylen=0 -> err=<nil>   (an EMPTY body passes even at 0/-1)
```
**Verdict:** BUNDLE-CORRECT but under-specified. `MaxBytesReader(n)` allows exactly n; my fiber
stand-in used `> n` and therefore matched. An implementer writing `>= *cap` in the fiber adapter —
an equally natural reading of *"cap at n wire bytes"* — produces a **one-byte parity divergence**
that no prescribed test detects (phase 2 test 2's fixtures are 3× the cap; phase 3 reuses them).
*Also confirmed:* the ADR's *"`MaxBytesReader(…, 0)` rejects **every non-empty body**"* is exactly
right — an empty body passes at limit 0 and at limit −1. The hedge "non-empty" is load-bearing and
correctly placed; the spec §2 row restates it as *"`→ err=http: request body too large`; `-1`
identical"* **without the hedge**, which is the Premise-Discipline recap failure this repo keeps
hitting. Correct the spec row.
**Fix:** (i) state in the ADR that the cap is **inclusive** — a body of exactly `MaxBodyBytes` is
accepted, `MaxBodyBytes+1` is not; (ii) add `at-cap` and `cap+1` rows to phase 2 test 2 **and** to
phase 3 parity, so the operator is pinned on all three adapters; (iii) re-add the "non-empty" hedge
to spec §2's `MaxBytesReader` row.

---
### E7 — ⭐⭐ the instrumentation has NO phase: `httpcore` is excluded on a false premise, and the adapters cannot record from where they stand

**Severity:** Critical
**Bundle says:**
- ADR §Decision: *"`wrkflw_rest_request_body_bytes` is recorded **in each adapter**, at the body read.
  ⚠ **Not in `httpcore`** — that package has **0** decode sites and never sees a body."*
- Plan §2 map: *"`wrkflw_rest_request_body_bytes` histogram + rejection counter | **2** | `stdlib` \|
  `gin` \| `fiber`"* — and phase 2 is *"**3 agents in parallel**"*, one per package.
- Plan §2 preamble: *"**A row with no phase is a defect** — six of round 3's fifteen Criticals were
  that one omission."*

**I ran:**
```
go doc ./transport/http/httpcore Instrumentation
grep -rn 'otel' transport/http/{stdlib,gin,fiber}/*.go | grep -v _test
sed -n '1,80p' transport/http/httpcore/observability.go
```
**Observed:**
```
type Instrumentation struct { // Has unexported fields.  }
func NewInstrumentation[R any](cfg CustomizeConfig[R]) *Instrumentation
func (i *Instrumentation) Observe(ctx, method, routeTemplate string, hdr http.Header, ...)
      <-- Observe is the ONLY exported method; tracer/counter/histogram/propagator are all unexported

grep 'otel' over stdlib/gin/fiber non-test sources -> (no output; the three adapters import NO otel package)
```
`NewInstrumentation` is the single place `cfg.MeterProvider` is turned into instruments, it lives in
**`httpcore`**, and it builds exactly **two**: `wrkflw_rest_requests_total` and
`wrkflw_rest_request_duration_seconds`.

**Verdict:** CONFIRMED-DEFECT. The ADR's exclusion rule is derived from the **wrong property**. Two
different things are being located: *where the value is observed* (the adapter, correct) and *where
the instrument is constructed and bound to `cfg.MeterProvider`* (**`httpcore`**, the only package
that does this today). "0 decode sites" is an argument about the first and is applied to the second.

Consequences as the plan currently stands:
- Adding `Instrumentation.RecordBodyBytes(...)` / `.RecordBodyRejected(...)` is **`httpcore` work =
  phase 1**, and there is **no phase-1 row for it**. By the plan's own rule that is a defect.
- If the ADR's *"not in `httpcore`"* is taken literally, each of the **three parallel phase-2 agents**
  must independently introduce an otel import, a `metric.Meter`, and a `Float64Histogram` +
  `Int64Counter` registration into a package that has none today — three separate registrations of
  the same instrument name under three different (or accidentally identical) scopes, written by three
  agents who by construction cannot see each other's diff. That is the precise divergence class this
  delivery exists to eliminate, re-created in its own instrumentation.
- Nothing in the bundle names the instrument's unit, buckets, or attribute set, so the three agents
  would also each choose those independently.

**Fix:** move the instruments to `httpcore` and add the missing **phase 1** rows:
`Instrumentation` gains `RecordRequestBodyBytes(ctx, method, route string, n int64)` and
`RecordRequestBodyRejected(ctx, method, route string)`; `NewInstrumentation` registers
`wrkflw_rest_request_body_bytes` (unit `By`) and `wrkflw_rest_request_body_rejected_total` alongside
the two existing instruments. The adapters then only **call** them at the body read — which is what
the ADR's *"recorded in each adapter, at the body read"* actually wants, and it becomes true. Replace
the ADR's *"⚠ Not in `httpcore` — that package has 0 decode sites"* with *"the recording **call site**
is in each adapter; the **instrument** is registered in `httpcore.NewInstrumentation`, the only place
bound to `cfg.MeterProvider`."*

---
### E8 — `MaxBodyBytes` would be the ONLY `CustomizeConfig` field with no `With…` option — and it is the only pointer one

**Severity:** Major
**Bundle says:** Plan §3 phase 1 **Symbols:** *"`CustomizeConfig.MaxBodyBytes *int64`;
`ErrRequestBodyTooLarge`."* No option constructor is named in the ADR, the spec, or the plan.
CLAUDE.md: *"Every feature must be reachable and ergonomic through that API."*

**I ran:** `go doc -all ./transport/http/httpcore | grep '^func With'` and read `seam.go`.
**Observed:**
```
func WithBasePath[R any](p string) CustomizeOption[R]
func WithInstanceMapper[R any](fn func(engine.InstanceState) any) CustomizeOption[R]
func WithLogger[R any](l *slog.Logger) CustomizeOption[R]
func WithMeterProvider[R any](mp metric.MeterProvider) CustomizeOption[R]
func WithRouterFunc[R any](fn func(R) R) CustomizeOption[R]
func WithTracerProvider[R any](tp trace.TracerProvider) CustomizeOption[R]
```
**6 fields on `CustomizeConfig`, 6 `With…` options — a 1:1 correspondence with no exception.**
**Verdict:** CONFIRMED-DEFECT. Without `WithMaxBodyBytes`, a consumer opting out must write
```go
stdlib.Mount(mux, svc, func(c *httpcore.CustomizeConfig[*http.ServeMux]) { n := int64(0); c.MaxBodyBytes = &n })
```
— hand-rolling an `int64` pointer inside a generic closure, for the one setting whose *whole point*
is that `0` and unset mean different things. This is also where E1's negative-value validation would
naturally live, and where the ADR's migration procedure (*"run with `MaxBodyBytes` explicitly `0`,
observe the distribution, then choose a cap"*) is actually exercised: the procedure the ADR
prescribes has **no documented API to perform it with**.
**Fix:** add to phase 1 Symbols: `WithMaxBodyBytes[R any](n int64) CustomizeOption[R]` (takes a plain
`int64`, stores `&n` — so `WithMaxBodyBytes(0)` is the readable opt-out and the pointer never appears
in consumer code) and, per E1, either reject or clamp+WARN a negative **inside that option**, which is
the only place a "construction error" has a caller to report to. Add a `WithMaxBodyBytes(0)` line to
`SECURITY.md`'s opt-out paragraph in phase 4.

---
### E9 — the `*int64` tri-state through a faithful `ResolveConfig` replica: HOLDS (with one caveat)

**Severity:** Minor
**Bundle says:** Evidence §7.2: *"**Not probed here:** the `*int64` tri-state through the real
`ResolveConfig`. The `MaxBytesReader` half is executed …; the **defaulting** half is a
**prescription**, and phase 1 test 2 is what discharges it."* Plan §0.2 asks the audit to attack it.

**I ran:** `transport/http/httpcore/zz_exec_probe3_test.go` (throwaway, deleted) —
`resolveProposed` is a **line-for-line replica of `httpcore.ResolveConfig`** (same struct-literal
pre-defaults, same `for … if o != nil` loop, same post-loop `if cfg.X == nil` idiom) with the ADR's
`if cfg.MaxBodyBytes == nil { d := 1<<20; cfg.MaxBodyBytes = &d }` appended.
`go test -count=1 -run '^TestProbeTriState$' -v ./transport/http/httpcore/` → **EXIT=0**.
**Observed:**
```
A no option at all (zero value nil)      resolved MaxBodyBytes=1048576  effect=cap at 1048576
B explicit 0 (the opt-out)               resolved MaxBodyBytes=0        effect=unbounded (reader NOT installed)
C explicit 2048                          resolved MaxBodyBytes=2048     effect=cap at 2048
D explicit -1                            resolved MaxBodyBytes=-1       effect=!! negative — no error channel
E option sets the pointer back to nil    resolved MaxBodyBytes=1048576  effect=cap at 1048576
F two options, 0 then 4096               resolved MaxBodyBytes=4096     effect=cap at 4096
```
**Verdict:** BUNDLE-CORRECT. The post-loop idiom **does not** clobber an explicit `0` once the field
is a pointer — row A and row B are genuinely different outcomes, and the ADR's *"the zero value
(`nil`) is the restrictive branch"* is true as written. Row E is the reassuring one: an option that
*clears* the field re-defaults to the cap rather than to unbounded, i.e. the safe state is reached by
every accidental route I could construct. §7.2's `ASSUMPTION` is discharged.
**Caveat, and it is E1:** row D shows a negative surviving `ResolveConfig` intact with nowhere to go.
**Fix:** none for the tri-state itself. Move §7.2's bullet from "not established" to "executed", quote
rows A/B/E, and keep phase 1 test 2 — it is now a regression pin rather than a discharge.

---
### E10 — the 413/400 ordering and the `httpcall` sentinel: BOTH premises re-executed and CORRECT

**Severity:** Minor (confirmation)
**Bundle says:** ADR: *"Executed: `ClassifyError` on an error wrapping both returns `400
{bad_request}`"*; *"a test asserts `httpcall.ErrBodyTooLarge` still classifies **500**"*;
plan §4: `ClassifyError` arms = **6**, ordered 404 `:28`, 403 `:32`, 409 `:34`, 400 `:36-50`,
422 `:51`, default 500 `:57`.
**I ran:** `TestProbeClassifyOrdering` against the real `httpcore.ClassifyError`, plus
`cat -n transport/http/httpcore/errors.go`.
**Observed:**
```
bare new sentinel (unknown to ClassifyError)      -> 500 {internal_error}
new sentinel WRAPPED WITH ErrBadInput             -> 400 {bad_request  ...bad input: ...request body too large}
bare ErrBadInput                                  -> 400 {bad_request}
httpcall.ErrBodyTooLarge (must stay 500)          -> 500 {internal_error}
raw *http.MaxBytesError                           -> 500 {internal_error}
*http.MaxBytesError wrapped in ErrBadInput        -> 400 {bad_request  ...bad input: http: request body too large}
httpcall.ErrBodyTooLarge text = "workflow-httpcall: body exceeds max size"
```
and the line numbers in `errors.go` are exactly 28 / 32 / 34 / 36–50 / 51 / 57 as the plan states.
**Verdict:** BUNDLE-CORRECT on all three claims. The double-wrap really does classify 400, so the
"return the **bare** sentinel + place the 413 arm before the 400 arm" instruction is necessary, and
`httpcall.ErrBodyTooLarge` really is a 500 today.
**One addition worth prescribing:** a **bare `*http.MaxBytesError` classifies 500**, not 400 — so an
adapter that forgets the conversion regresses a request error into an internal error with an **empty
message**, which is strictly worse than today's 400 and is silent. Add a row to phase 1 test 1
asserting `*http.MaxBytesError` **unconverted** is a 500, with a comment saying that is why every
adapter must convert; it gives the reviewer a named consequence for the omission.

---
### E11 — ⭐⭐ the "the bomb cannot discriminate" claim is FALSE for the bomb the plan itself prescribes

**Severity:** Major
**Bundle says:** ADR §Decision: *"`c.Body()` **decompresses** …, so a 63.7 KiB gzip expanding to
64 MiB makes `len(c.Body())` return **33** … ⚠⚠ **The falsifier for this is the REVERSE of what a
previous revision prescribed.** That revision's fixture was the gzip bomb … but with the bomb,
`len(c.Body())` sees 33, which is *under* the cap, so **the wrong implementation returns 400 exactly
like the right one** and the test cannot discriminate."*
Plan §3 phase 2 test 6 row 1: *"gzip, **wire 2 KiB**, decompressed **2 MiB**, cap 1 MiB | **not
413**"*, with the note *"**Row 2 is the discriminating one**"*.

**I ran:** `transport/http/fiber/zz_exec_probe5_test.go` + `zz_exec_probe4_test.go` (throwaway, both
deleted). A real `fiber.App`, `Content-Encoding: gzip`, sweeping the **decompressed** size.
`go test -count=1 -run '^TestProbeFiberBombBodyLen$' -v ./transport/http/fiber/` → **EXIT=0**.

**Observed:**
```
decompressed=1  MiB wire=1056    BodyRaw=1056    Body=1048576
decompressed=2  MiB wire=2072    BodyRaw=2072    Body=2097152      <-- the PLAN's row-1 fixture
decompressed=3  MiB wire=3088    BodyRaw=3088    Body=3145728
decompressed=4  MiB wire=4103    BodyRaw=4103    Body=4194304
decompressed=5  MiB wire=5132    BodyRaw=5132    Body=33   bodyText="body size exceeds the given limit"
decompressed=8  MiB wire=8180    BodyRaw=8180    Body=33
decompressed=64 MiB wire=65253   BodyRaw=65253   Body=33   <-- the ADR's fixture
fiber.DefaultBodyLimit = 4194304
```
**Verdict:** CONFIRMED-DEFECT (a false claim, not a broken design).

`len(c.Body()) == 33` happens **only when the decompressed size exceeds `fiber.Config.BodyLimit`
(4 MiB)** — the threshold sits between 4 MiB (`Body=4194304`) and 5 MiB (`Body=33`). The ADR
generalises from its 64 MiB fixture to *"with the bomb … the test cannot discriminate"*, and the
**plan's own row 1 uses a 2 MiB bomb**, which is on the other side of that threshold:

| impl, cap 1 MiB | plan row 1 (wire 2072, decompressed 2 MiB) | plan row 2 (wire 2097191, decompressed 869567) |
|---|---|---|
| `len(c.BodyRaw())` (right) | 2072 ≤ cap → **not 413** ✅ | 2097191 > cap → **413** ✅ |
| `len(c.Body())` (wrong) | 2097152 > cap → **413** ✗ | 869567 ≤ cap → falls through → **400** ✗ |

**Both rows discriminate.** The ADR's sentence *"the wrong implementation returns 400 exactly like the
right one"* is true of a **>4 MiB** bomb and false of the **2 MiB** bomb the plan fixtures. This is
the *"a claim TRUE when written, falsified by a sibling fix in the same commit"* shape — the ADR was
corrected to invert the falsifier, and the plan was corrected to a 2 MiB fixture, and neither was
re-derived against the other.

**Fix:** in the ADR, replace *"with the bomb, `len(c.Body())` sees 33"* with the executed
threshold — *"`c.Body()` returns the real decompressed length while it is ≤ `fiber.Config.BodyLimit`
(4 MiB), and the 33-byte string `\"body size exceeds the given limit\"` above it; executed at 1/2/3/4
MiB → real length, 5/8/64 MiB → 33"* — and correct *"Row 2 is the discriminating one"* to *"**both**
rows discriminate at a 2 MiB decompressed size; row 2 additionally discriminates for a bomb above
4 MiB, where `c.Body()` collapses to 33."* Keep both rows; the design is right, the narrative is not.

---
### E12 — ⭐⭐ `fiber.Config.BodyLimit` IS reachable from the router in the common case — the spec's ASSUMPTION is refuted

**Severity:** Major
**Bundle says:** Spec §5: *"`ASSUMPTION (unverified)`: that `fiber.Config.BodyLimit` is unreachable
from a mounted `fiber.Router`. Believed from the API shape; the WARN's fallback to the package
constant depends on it, so **the implementation must confirm it or use the real value**."*
Evidence §7.2 repeats it. ADR: *"it must compare against the app's real limit **where that is
reachable** rather than against the package constant."*

**I ran:** `TestProbeFiberConfigReachability` + `go doc github.com/gofiber/fiber/v3.App` and `.Router`.
**Observed:**
```
app passed directly          dynamic type=*fiber.App    -> Config().BodyLimit = 4194304   REACHABLE
app with BodyLimit 16MiB     dynamic type=*fiber.App    -> Config().BodyLimit = 16777216  REACHABLE
app.Group("/api")            dynamic type=*fiber.Group  -> no Config() accessor           UNREACHABLE
fiber.DefaultBodyLimit = 4194304
```
`func (app *App) Config() Config` is exported; `*fiber.Group` has no equivalent (its app handle is
unexported) and the `fiber.Router` **interface** declares only routing methods.

**Verdict:** CONFIRMED-DEFECT — the assumption is **refuted for the common case**, which is the case
every fiber test in this repo and every `examples/*_wiring/main.go` uses (the `*fiber.App` is passed
straight to `Customize`/`Mount`). The WARN can and must read the real limit:
```go
limit := int64(fiberlib.DefaultBodyLimit)
if app, ok := r.(*fiberlib.App); ok { limit = int64(app.Config().BodyLimit) }
```
⚠ **And the assertion must be on `r`, not on `rt := cfg.Wrap(r)`** — every fiber `Customize` shadows
the router through `cfg.Wrap`, and a consumer using `WithRouterFunc(func(r) r.Group("/api"))` turns
an `*App` into a `*Group`, i.e. into the unreachable branch. Nothing in the bundle says which of the
two to assert on, and the wrong choice silently degrades to the constant for exactly the consumers
who customised their limit.
**Fix:** delete the `ASSUMPTION` from spec §5 and evidence §7.2 and replace it with the executed
result above; state the type assertion, state that it happens on `r` **before** `cfg.Wrap`, and state
the `*fiber.Group` fallback to `DefaultBodyLimit` as a **known** partial case rather than the design.
Add a row to phase 2 test 7: *"an app constructed with `fiber.Config{BodyLimit: 16<<20}` and
`MaxBodyBytes = 8 MiB` must NOT warn"* — which fails against any implementation comparing to the
package constant, and which the current test text cannot produce.

---
### E13 — the fiber above-`BodyLimit` divergence, verified over a real socket (BUNDLE-CORRECT)

**Severity:** Minor (confirmation, with one correction)
**Bundle says:** ADR: *"Executed: at 8 MiB the route group is **never reached** — the client receives
fasthttp's `text/plain` `Request Entity Too Large`, with no `ErrorBody` and no log line."*
**I ran:** `TestProbeFiberAboveLimitWireResponse` — real `net.Listen` + `app.Listener`, raw HTTP/1.1,
reading the actual response bytes; plus `TestProbeFiberAboveDefaultBodyLimit` for the boundary.
**Observed:**
```
under DefaultBodyLimit (n=1048576)
    HTTP/1.1 200 OK / Content-Type: application/json; charset=utf-8 / BODY={"ok":"yes"}
above DefaultBodyLimit (n=8388608)
    HTTP/1.1 413 Request Entity Too Large
    Content-Type: text/plain; charset=utf-8
    Connection: close
    BODY=Request Entity Too Large

wire=1048576  handlerReached=1     wire=4194303  handlerReached=1
wire=4194304  handlerReached=1     wire=8388608  handlerReached=0
```
**Verdict:** BUNDLE-CORRECT, verified on the wire rather than through `app.Test`. Two additions worth
carrying into `SECURITY.md` (phase 4): the framework rejection is a **413**, i.e. the *same status*
this delivery mints — so a consumer cannot distinguish "wrkflw rejected it" from "fiber rejected it"
by status alone, only by `Content-Type` (`application/json` + `ErrorBody` vs `text/plain`); and the
boundary is **inclusive** — a body of exactly `DefaultBodyLimit` (4194304) **does** reach the handler,
so the WARN condition `MaxBodyBytes > fiber.DefaultBodyLimit` is the correct comparison.
**Fix:** none to the design. Add the `Content-Type` discriminator sentence to the `SECURITY.md` and
ADR text — *"both rejections are 413; only the body distinguishes them"* — because the plan's phase-3
*"explicitly-labelled fiber-only case"* will otherwise assert on a status that matches by accident.

---
### E14 — "the function the documented mount path actually calls" is FIVE functions, and `Mount` is not one of them for admin

**Severity:** Critical
**Bundle says:** ADR: *"**A WARN is logged when `MaxBodyBytes > fiber.DefaultBodyLimit`.** ⚠ **It must
be logged from the function the documented mount path actually calls** … a previous revision put it
in neither."* Plan phase 2 test 7: *"`TestMountWarnsAboveDefaultBodyLimit` — asserted against a `slog`
handler capturing records, **through the documented mount entry point**."* Both speak of **the**
function, singular.

**I ran:** `transport/http/fiber/zz_exec_probe6_test.go` (throwaway, deleted). `cfg.Wrap(r)` is called
exactly once per `Customize`, so a counting `WithRouterFunc` counts `ResolveConfig` invocations.
`go test -count=1 -run '^TestProbeMountEntryPointMultiplicity$' -v ./transport/http/fiber/` → **EXIT=0**.
**Observed:**
```
fiber.Mount(app, svc, opts...)                        ResolveConfig/Customize invocations = 3
AdminRoutes{}.Customize(app, opts...)                 ResolveConfig/Customize invocations = 1
InstanceRoutes{}.Customize(app, opts...)              ResolveConfig/Customize invocations = 1
MountGroups(app, Instance, Task, Message, Admin)      ResolveConfig/Customize invocations = 0   (see E15)
Mount + AdminRoutes.Customize (documented admin path) ResolveConfig/Customize invocations = 4
```
**Verdict:** CONFIRMED-DEFECT. There is no single "documented mount path". `grep 'func .*Customize('`
finds **five** entry points per adapter (`InstanceRoutes`, `MessageRoutes`, `TaskRoutes`,
`AdminRoutes`, `HealthRoutes`), each calling `ResolveConfig` independently; `Mount` is a convenience
wrapper over **three** of them and — per ADR-0095, which this very bundle cites — **deliberately
excludes `AdminRoutes`**. So:
- **WARN inside `Mount`** ⇒ it **never fires for `AdminRoutes.Customize`**, i.e. never for the route
  group containing the three discarding sites this delivery exists to fix. That is the *same* class
  of miss as round 5's finding, relocated rather than repaired.
- **WARN inside each `Customize`** ⇒ a single `fiber.Mount(...)` emits the identical WARN **3 times**,
  and the documented full mount (`Mount` + `AdminRoutes.Customize`) emits it **4 times**. Nothing in
  the bundle says this is intended, and a 4× duplicated startup WARN reads as a bug to an operator.
- **WARN inside `httpcore.ResolveConfig`** is worse still: `ResolveConfig` is the *shared generic*
  used by stdlib and gin too, where `fiber.DefaultBodyLimit` is meaningless — and `httpcore` cannot
  import `fiber` without inverting the dependency direction the whole package layout rests on.

**Fix:** name the placement explicitly and make it idempotent. Log from **each fiber `Customize`**
(the only place with both `cfg` and the router), guarded by a `sync.Once` **per resolved config**, or
— simpler and testable — emit it from a single unexported `warnIfAboveBodyLimit(r fiberlib.Router,
cfg …)` called at the top of all **five** `Customize` methods, and make phase 2 test 7 a table with a
row per entry point: `Mount`, `AdminRoutes.Customize`, `MountHealth`, asserting **exactly one** record
per `Customize` call and that `AdminRoutes.Customize` alone still warns. The current single-row test
passes against a `Mount`-only implementation, which is the defect.

---
### E15 — `httpcore.MountGroups` passes NO options, so a consumer mounting through the documented seam CANNOT set or opt out of the cap

**Severity:** Critical
**Bundle says:** Nothing. `MountGroups` appears nowhere in the ADR, the spec or the plan. ADR
migration procedure: *"**run with `MaxBodyBytes` explicitly `0`, observe the distribution, then choose
a cap**"*. Plan §4 boundary warning: *"a **config sentinel** nobody enumerated … **Assume one
boundary here is still wrong.**"*

**I ran:** the same probe (`MountGroups` row) plus `sed -n '104,112p' transport/http/httpcore/seam.go`.
**Observed:**
```
MountGroups(app, Instance, Task, Message, Admin)   ResolveConfig/Customize invocations = 0
```
(zero, because the counting option is never delivered) and the source:
```go
// MountGroups mounts each group onto r at its current position (no extra opts).
// It is also the consumer extension seam: any RouteCustomizer[R] — including a
// consumer's own — can be passed. …
func MountGroups[R any](r R, groups ...RouteCustomizer[R]) {
	for _, g := range groups {
		g.Customize(r)          // <-- no opts, by construction: MountGroups takes none
	}
}
```
**Verdict:** CONFIRMED-DEFECT. `MountGroups` is exported, is documented as *"the consumer extension
seam"*, and its signature has **no `CustomizeOption` parameter at all**. Every group it mounts
therefore resolves the **default** config. After this delivery that means:
- every `MountGroups` consumer silently acquires the **1 MiB cap** with **no way to raise it** —
  a behaviour change on a path the bundle never considered, and the fail-closed direction makes it
  *worse*, not better, because it is unfixable rather than merely surprising;
- the ADR's migration procedure (**explicitly `0` first, then choose a cap**) is **impossible to
  perform** for such a consumer, so the histogram-then-cap story the ADR builds its instrumentation
  around does not apply to them;
- it is a **sixth** mount entry point for E14's WARN, and it would warn zero times.

**Fix:** decide and record it. Either (i) `MountGroups` gains a trailing
`opts ...CustomizeOption[R]` — a **source-compatible** variadic addition, since no caller passes one
today (grep: the only in-repo callers are in `seam_test.go`), which is the cheapest correct answer and
does *not* break the "no new exported interface" non-goal; or (ii) the ADR states that `MountGroups`
consumers get the default and must switch to per-group `Customize` to configure it, and `SECURITY.md`
+ `CHANGELOG.md` say so as a **third** break. Option (i) is strongly preferred. Either way,
`MountGroups` must appear in the plan's decision→phase map — by the plan's own rule, a mount path
with no row is a defect.

---
### E16 — the memory/latency "cost" of buffering is measured, and it is a BENEFIT, not a cost

**Severity:** Minor (an ASSUMPTION discharged, and a Consequences bullet that is now wrong)
**Bundle says:**
- ADR §Decision: *"⚠ **Cost, stated:** under a cap, stdlib and gin now hold the whole body in memory
  before unmarshalling, bounded by `MaxBodyBytes`."*
- ADR §Consequences Negative: *"Under a cap, stdlib and gin buffer the whole body before
  unmarshalling. Bounded by the cap; **previously they streamed**."*
- Spec §5 / evidence §7.2: *"`ASSUMPTION (unverified)`: that buffering the body under a cap has no
  material latency cost at 1 MiB … **not measured**."*

**I ran:** `transport/http/parity/zz_exec_probe7_test.go` (throwaway, deleted). Same handler shape,
same 1 MiB `httpcore.StartInput` payload, `httptest` round trip.
`go test -count=3 -run '^$' -bench '^Benchmark(Streaming1MiB|Buffered1MiB|OversizeStreaming|OversizeBuffered)$' ./transport/http/parity/` → **EXIT=0**.
**Observed** (Apple M4 Pro, darwin/arm64, 3 runs each — spread < 1.5 %):
```
BenchmarkStreaming1MiB-14      3347027 / 3330736 / 3354634 ns/op   5254398 B/op   44 allocs/op
BenchmarkBuffered1MiB-14       3265303 / 3253816 / 3296555 ns/op   3298840 B/op   58 allocs/op
BenchmarkOversizeStreaming-14  2015764 / 2007524 / 2010122 ns/op   4227072 B/op   34 allocs/op
BenchmarkOversizeBuffered-14     90209 /   93040 /   93109 ns/op   2235077 B/op   41 allocs/op
```
**Verdict:** BUNDLE-CORRECT in direction of safety, but the **Consequences bullet is factually wrong**
and should not ship as written.
- **Happy path, 1 MiB:** buffered is **~2 % FASTER** (3.27 ms vs 3.34 ms) and allocates **37 % FEWER
  bytes** (3.30 MB vs 5.25 MB), at the cost of 14 more allocations. `json.NewDecoder` over an
  `io.Reader` maintains its own growing scratch buffer, so "streaming" was never allocation-free —
  it was allocating *more* than a single `io.ReadAll` slice.
- **The attack path (8 MiB body at a 1 MiB cap):** buffered is **~22× faster** (0.092 ms vs 2.01 ms)
  and allocates **47 % fewer bytes**. `io.ReadAll` stops one byte past the cap; the decoder keeps
  pulling and re-growing until it happens to trip. This is the case an operator cares about, and
  read-before-parse improves it by more than an order of magnitude.
**Fix:** discharge the `ASSUMPTION` in spec §5 and evidence §7.2 with these numbers, and **rewrite the
Negative bullet**: it currently sells a regression that does not exist and omits a 22× improvement on
the DoS path — which is a materially better argument for the decision than the one the ADR makes.
Suggested replacement: *"Under a cap, stdlib and gin buffer the body before unmarshalling, bounded by
`MaxBodyBytes`. Measured at 1 MiB this is a wash on latency (−2 %) and a 37 % reduction in allocated
bytes; on an 8 MiB body at a 1 MiB cap it is ~22× faster than capping during the parse. The residual
cost is peak resident bytes per in-flight request, still bounded by the cap."* ⚠ The remaining honest
concern the bundle *should* state and does not: peak memory is now `MaxBodyBytes × concurrent
in-flight requests` with no global ceiling — at 1 MiB and 1 000 concurrent uploads that is 1 GiB.
Neither this bundle nor any deferred one owns a global bound; say so.

---
### E17 — gin's `ShouldBindJSON` DOES survive the buffer-and-reset, but the evidence's "and its validation" clause is VACUOUS in this repo

**Severity:** Minor
**Bundle says:** Evidence §7.1 Result 3: *"**the gin buffer-and-reset works.** Reading through
`MaxBytesReader` into a buffer and reassigning `r.Body = io.NopCloser(bytes.NewReader(buf))` decodes
cleanly, so gin's `ShouldBindJSON` **and its validation** are preserved rather than bypassed."*
⚠ The probe that produced this logged `GIN-RESET decode err=<nil>` from a test in **`httpcore`** —
i.e. it ran `json.NewDecoder` after the reset, **not** `gc.ShouldBindJSON`, and not gin's validator.
This is the §6.3a shape the plan tells the audit to look for.

**I ran:** two things.
(1) `TestProbeGinValidatorThroughReset` — real `gin.Engine`, real `gc.ShouldBindJSON`, a DTO carrying
real `binding:"required"` / `binding:"gte=18"` tags, with and without the reset.
(2) `grep -rn 'binding:"' --include='*.go' . | grep -v _test | wc -l`.
**Observed:**
```
reset=false body={"name":"ok","age":30}  -> 200 | {Name:ok Age:30}
reset=true  body={"name":"ok","age":30}  -> 200 | {Name:ok Age:30}
reset=false body={"age":30}              -> 400 | Key: 'tagged.Name' … failed on the 'required' tag
reset=true  body={"age":30}              -> 400 | Key: 'tagged.Name' … failed on the 'required' tag
reset=false body={"name":"ok","age":10}  -> 400 | Key: 'tagged.Age' … failed on the 'gte' tag
reset=true  body={"name":"ok","age":10}  -> 400 | Key: 'tagged.Age' … failed on the 'gte' tag

binding tags in the repo (non-test): 0
validate tags under transport/:      4   (StartInput.DefRef, SignalInput.Signal, MessageInput.Name, …)
httpcore.Validate call sites:        endpoints.go:26, :83, :101
```
**Verdict:** BUNDLE-CORRECT on the mechanism, INCONCLUSIVE-as-stated on the claim.
The reset genuinely preserves gin's binder **and** its validator — every row is identical with and
without the reset, including both tag kinds. I confirmed the load-bearing half by running the actual
binder rather than a stand-in decoder.
But *"and its validation"* describes something that **does not exist here**: there are **zero**
`binding:` tags anywhere in the repo. Validation is done by **`httpcore.Validate`** over `validate:`
tags, at three call sites *after* the decode, on the transport-neutral side — so it is untouched by
this change on **all three** adapters, gin included. The evidence sentence is true-but-empty and
invites a reader to believe a gin-specific risk was retired when there was none.
**Fix:** replace §7.1 Result 3 with the executed version: *"executed against `gc.ShouldBindJSON` with
a `binding:`-tagged DTO — decode and validator behaviour are byte-identical with and without the
reset. ⚠ No DTO in this repo carries a `binding:` tag (grep → 0); validation is `httpcore.Validate`
over `validate:` tags at `endpoints.go:26/83/101`, which runs after the decode on every adapter and
is unaffected."* And per E2, note that `ShouldBindJSON` **ignores trailing data**, which is the
property that actually matters and which §7.1 did not test.

---
### E18 — the 413's `ErrorBody.Error` code string and its `Message` policy are NEVER named

**Severity:** Major
**Bundle says:** ADR: *"`ClassifyError` maps it → **413**"*, and the arm goes before the 400 arm.
Spec §4 (D1 × the removed variable-map bound) refers in passing to *"the **static** `\"request too
large\"`"* — the only hint that the message is static, and it appears in a row about a **deferred**
delivery, not in the Decision.
**I ran:** `cat -n transport/http/httpcore/errors.go`; `TestProbeClassifyOrdering`; `cat -n
transport/http/{stdlib,gin}/write.go`.
**Observed:** every existing arm returns `ErrorBody{Error: "<code>", Message: err.Error()}` with codes
`not_found` / `forbidden` / `conflict` / `bad_request` / `conflict_state`, and 5xx returns
`{Error:"internal_error"}` with **no** message. `writeErr` logs only when `status >= 500`
(`stdlib/write.go:32`, `gin/write.go:13`), so — as the ADR states — a 413 writes **no log record**;
that part is confirmed. Nothing in the bundle names the 413's code string, and
`errProbeRequestTooLarge.Error()` would render as `"workflow-httpcore: request body too large"` if
the arm follows the 400 pattern.
**Verdict:** CONFIRMED-DEFECT (an omission with wire consequences).
`ErrorBody.Error` is a **stable wire value** — phase 4 updates `CHANGELOG.md` and `STABILITY.md` for
the new status but there is no name for the new code. Left unspecified, phase 1's single agent
invents one (`payload_too_large`? `request_too_large`? `too_large`?) and it becomes contract by
accident. And if the arm copies the 400 pattern (`Message: err.Error()`), the client-visible message
becomes `"workflow-httpcore: request body too large"` — leaking an internal package prefix into a
public body, on the one route class most likely to be hit by an untrusted client.
**Fix:** name both in the ADR Decision: `ErrorBody{Error: "request_too_large", Message: "request body
exceeds the configured limit"}` — a **static** message with **no** limit value in it (disclosing the
exact cap tells an attacker precisely how to sit under it), and add an assertion for both strings to
phase 1 test 1 and to phase 3's parity envelope comparison. Also state explicitly that a 413 produces
**no log record** and why (`writeErr` logs only ≥ 500) — the ADR asserts the consequence without the
mechanism, and an implementer adding a log line there would be within the letter of the ADR.

---
### E19 — enumerations re-derived: 39 / 36 / 3 and the three line numbers all HOLD

**Severity:** Minor (confirmation)
**Bundle says:** plan §4: *"decode sites **39** — `stdlib` 13, `gin` 13, `fiber` 13, `httpcore` **0**
… propagating **36** … discarding **3** — `stdlib:238`, `gin:265`, `fiber:255`"*; *"packages this
delivery touches **4**"*.
**I ran:**
```
grep -rn 'json.NewDecoder' transport/http/stdlib/ | grep -v _test | wc -l          -> 13
grep -rn 'ShouldBindJSON'  transport/http/gin/    | grep -v _test | wc -l          -> 13
grep -rn 'Bind()\.JSON'    transport/http/fiber/  | grep -v _test | wc -l          -> 13
grep -rn 'json.NewDecoder\|json.Unmarshal' transport/http/httpcore/ | grep -v _test | wc -l -> 0
grep -rn '_ = json.NewDecoder\|_ = gc.ShouldBindJSON\|_ = c.Bind()' transport/http/ | grep -v _test
grep -rln 'ShouldBindJSON\|json.NewDecoder(r\.Body)\|json.NewDecoder(req\.Body)\|Bind()\.JSON' --include='*.go' . | grep -v _test
```
**Observed:**
```
13 / 13 / 13 / 0
transport/http/stdlib/groups.go:238:  _ = json.NewDecoder(req.Body).Decode(&in) // body is optional
transport/http/fiber/groups.go:255:   _ = c.Bind().JSON(&in)
transport/http/gin/groups.go:265:     _ = gc.ShouldBindJSON(&in)
packages decoding an HTTP body, repo-wide: transport/http/{stdlib,gin,fiber}/groups.go  (exactly 3 files)
```
Also verified independently in `errors.go`: the `ClassifyError` arms sit at exactly 404 `:28`,
403 `:32`, 409 `:34`, 400 `:36–50`, 422 `:51`, default `:57` — the plan's line citations are current
at this commit.
**Verdict:** BUNDLE-CORRECT. 39 = 13+13+13, 3 discarders at the stated lines, `httpcore` really is 0,
and the repo-wide scope really is those three files — the boundary the plan warns may be wrong is, on
this axis, right. (The boundary that *is* wrong is the **mount-path** one — see E14/E15 — and the
**instrument-registration** one — see E7.)

---
## Summary — EXECUTION lens, ADR-0186 (one decision), round 6

**19 findings: 6 Critical, 6 Major, 7 Minor.** 12 CONFIRMED-DEFECT, 7 confirmations (4 bundle claims
re-executed and correct, 3 correct-in-mechanism but mis-stated in the document).

| id | one line | sev | verdict |
|---|---|---|---|
| E1 | *"negative → a construction error at mount"* has **no error-returning function** to surface on — all 15 `Customize` and 6 `Mount` return nothing, and adding one contradicts the *"no new exported interface"* non-goal | Critical | defect |
| E2 | read-before-parse makes stdlib (`json.Unmarshal`, rejects trailing data) and gin (`ShouldBindJSON`, ignores it) **disagree on under-cap trailing-byte bodies** — 400 vs 200; no prescribed or existing test fixtures one | Critical | defect |
| E3 | stdlib's 400 **message text** changes (`EOF` → `unexpected end of JSON input`) while gin's does not — breaks `TestParity_ErrorEnvelopes`'s byte-for-byte guarantee, silently (no case fixtures an empty/truncated body) | Major | defect |
| E4 | the plan **deletes `errors.As(*http.MaxBytesError)`**, but a non-nil read error is not proof of oversize — an aborted/over-declared body yields `unexpected EOF` and would ship as **413**; phase-2 test 5 uses an *absent* body and cannot fail against it | Critical | defect |
| E5 | `http.MaxBytesReader` **does** mutate the response: `Connection: close` on trip (it writes no status — that half of the premise holds). Every 413 tears down keep-alive; fiber's `BodyRaw()` does not → a 4th adapter divergence | Major | defect |
| E6 | cap **inclusivity** never stated; `MaxBytesReader(n)` accepts exactly n, so a fiber `>=` would diverge by one byte with no test to catch it. Spec §2 also drops the load-bearing *"non-empty"* hedge | Minor | correct/under-specified |
| E7 | the histogram + rejection counter have **no phase**: `httpcore` is excluded for having *"0 decode sites"*, but it is the only package that builds instruments from `cfg.MeterProvider`; as written, 3 parallel agents each invent the same instrument | Critical | defect |
| E8 | `MaxBodyBytes` would be the **only** `CustomizeConfig` field with no `With…` option (6 fields ↔ 6 options today) and the only pointer one — the ADR's own migration procedure has no API to perform it | Major | defect |
| E9 | the `*int64` tri-state through a line-for-line `ResolveConfig` replica: **holds** (nil→1 MiB, 0→unbounded, n→n, re-nil→1 MiB). §7.2's ASSUMPTION discharged | Minor | correct |
| E10 | 413/400 ordering + `httpcall.ErrBodyTooLarge`=500 + the `errors.go` line numbers: all re-executed, all correct. Addition: a **bare** `*http.MaxBytesError` is a **500**, worth pinning | Minor | correct |
| E11 | *"with the bomb the test cannot discriminate"* is **false for the 2 MiB bomb the plan itself fixtures** — `c.Body()` returns 33 only above `fiber.Config.BodyLimit` (4 MiB); executed threshold 4→real, 5→33. **Both** rows discriminate | Major | defect |
| E12 | `fiber.Config.BodyLimit` **is reachable**: `(*fiber.App).Config()` is exported (`*fiber.Group` is not). Spec §5's ASSUMPTION refuted for the common case; the assertion must be on `r`, **before** `cfg.Wrap` | Major | defect |
| E13 | above `DefaultBodyLimit`, verified on a real socket: `413 Request Entity Too Large`, `text/plain`, `Connection: close`, handler not reached; boundary **inclusive** (4194304 reaches). Same **status** as ours — only `Content-Type` distinguishes | Minor | correct |
| E14 | *"the function the documented mount path actually calls"* is **five** functions; a WARN in `Mount` never fires for `AdminRoutes` (where the 3 discarding sites live), a WARN in `Customize` fires **3–4×** per documented mount | Critical | defect |
| E15 | `httpcore.MountGroups` — the documented *"consumer extension seam"* — takes **no options at all**, so such consumers get the 1 MiB cap **with no way to raise it or opt out**, and the ADR's migration procedure is impossible for them | Critical | defect |
| E16 | the *"buffering is a cost"* Consequences bullet is **wrong**: measured, buffered is 2 % faster and allocates 37 % fewer bytes at 1 MiB, and is **~22× faster** on an 8 MiB oversize body. Unstated real cost: peak = cap × in-flight | Minor | correct-but-misstated |
| E17 | the gin reset **does** preserve `ShouldBindJSON` + its validator (executed with real `binding:` tags) — but the repo has **0** `binding:` tags, so §7.1's *"and its validation"* is vacuous; validation is `httpcore.Validate` over `validate:` tags, unaffected on all three adapters | Minor | correct-but-vacuous |
| E18 | the 413's `ErrorBody.Error` **code string** and its `Message` policy are never named — a wire contract invented by accident, and copying the 400 pattern leaks `"workflow-httpcore: …"` to untrusted clients. (Confirmed: `writeErr` logs only ≥ 500, so no log record — as the ADR says) | Major | defect |
| E19 | 39 = 13+13+13, `httpcore` 0, discarders at `stdlib:238` / `gin:265` / `fiber:255`, `ClassifyError` arms at `:28/:32/:34/:36-50/:51/:57`, repo-wide decoding scope = exactly 3 files — **all re-derived and all correct** | Minor | correct |

### The three that decide whether this bundle is implementable

1. **E2 + E4** — read-before-parse, the delivery's one idea, is under-specified in the two places it
   is load-bearing: *which parser runs over the buffer* (stdlib and gin do not agree, and the ADR
   treats them as one fix) and *what a read error means* (the plan deletes the only discriminator).
   Both are new in round 6 and neither is reachable by any prescribed test.
2. **E14 + E15** — the **mount boundary** was never enumerated. Five `Customize` entry points, a
   `Mount` that excludes admin by design (ADR-0095, which the bundle cites for a *different* reason),
   and an exported `MountGroups` seam that cannot take options at all. This is the *"the failure was
   the grep's NET, generalised to SCOPES"* lesson recurring on the axis the bundle did not re-derive.
3. **E1 + E7** — two Decision sentences with **no implementable landing site**: a construction error
   with no error channel, and an instrument excluded from the only package able to register it. Both
   are the plan's own *"a row with no phase is a defect"* rule, violated.

### Method note

Every Critical came from **widening a fixture the bundle had already run and passed** — under-cap
instead of oversize (E2, E3), a real socket instead of `httptest` (E4, E13), the real binder instead
of a stand-in decoder (E17), the *other* mount entry points instead of one (E14, E15), a swept
decompressed size instead of one bomb (E11). The bundle's probes are not wrong; they are **narrow**,
and the narrowness is always in the same direction — the fixture that demonstrates the fix.

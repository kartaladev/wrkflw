# Audit round 6 (ADR-0186 one-decision re-cut) — FAILURE-MODES lens

Bundle commit `85d6bb68`, worktree `wt2/failure-modes`. Step 0: all five bundle files present. ✅

Scope note: the five **deferred** decisions (4xx message policy, at-rest posture, read-path
disclosure, `httpcall` SSRF, variable-map bound) and ADR-0185's identity material are **out of
scope** and are not judged on their merits here. Where a finding touches the boundary, it attacks
what *this* bundle assumes about them, which the brief permits.

---

### F1 — Read-before-parse turns a return-on-first-value decode into a wait-for-EOF read: a NEW connection-hold / slowloris primitive that the ADR's cost paragraph does not mention
**Severity:** Critical
**Bundle says:** ADR §Decision — *"`stdlib` and `gin`: read the body through `http.MaxBytesReader`
to completion (`io.ReadAll`), then unmarshal from the resulting buffer."* and, as the only stated
cost, *"⚠ **Cost, stated:** under a cap, stdlib and gin now hold the whole body in memory before
unmarshalling, **bounded by `MaxBodyBytes`**. … The bound is the cap."* Spec §5 records the only
`ASSUMPTION (unverified)` in this area as *"buffering the body under a cap has no material latency
cost at 1 MiB"*. Every statement bounds **space**. **Nothing in the bundle bounds TIME.**

**The case it misses:** `json.Decoder.Decode` returns the instant the first complete JSON value has
been read — it does **not** wait for EOF. That is precisely the property the ADR spends a whole
Context section calling a defect. Removing it has a second consequence the bundle never derives:
`io.ReadAll` **must** read to EOF. A client that sends `Content-Length: 1048576`, writes a valid
18-byte JSON object, and then simply stops writing (or opens a chunked request and never sends the
terminating chunk) now holds:

- one server goroutine,
- the connection,
- the growing `io.ReadAll` buffer (up to the cap),

for as long as the *consumer's* `http.Server.ReadTimeout` allows. **`wrkflw` does not own the
`http.Server`** — it is a library that hands the consumer route groups (`Mount`,
`RouteCustomizer.Customize`), and `http.Server`'s zero value has **no** `ReadTimeout`. Today that
same client gets a 2xx and the goroutine is released immediately; after this change it is held
indefinitely. N concurrent such clients = N goroutines × up to N MiB, unbounded in both dimensions.

**Evidence:** executed, `/tmp/probe1` (deleted), `go test -v` EXIT=0:
```
today (streaming decode): DECODE returned err=<nil> v={AddAttempts:1} -> returned WITHOUT EOF
proposed (read-before-parse): *** BLOCKED for 2s — holds the goroutine + buffer until EOF/timeout ***
```
(The reader returns one complete JSON value and then blocks forever, mimicking a client that stops
writing without closing.)

**Consequence:** the delivery closes a memory-amplification hole (uncapped body) and opens a
*connection*-amplification hole in the same edit. A consumer who mounts the groups on a bare
`&http.Server{Handler: mux}` — the shape every `examples/` file uses — is newly vulnerable to a
trivial slowloris that costs the attacker one socket and 18 bytes per held goroutine. Worse, the
attack is *cheaper* under the cap than it would have been without it, because the attacker never
has to send the megabyte.

**Fix:** three parts, all cheap.
1. Add a stated cost to the ADR's Negative list: *"read-before-parse waits for EOF where the
   streaming decode returned at the end of the first JSON value; a client that stalls mid-body now
   holds a goroutine and up to `MaxBodyBytes` for the duration of the consumer's
   `http.Server.ReadTimeout`."*
2. `SECURITY.md` (phase 4) must tell consumers to set `ReadTimeout`/`ReadHeaderTimeout` on the
   `http.Server` they own, and `examples/` wiring must actually set them — otherwise the library's
   own reference wiring demonstrates the vulnerable shape.
3. Add a plan phase-2 test with a body reader that stalls after a complete value and a
   `context`/`ReadTimeout`-bounded assertion that the handler does not hang forever. **Falsifier:**
   it fails against any implementation that calls `io.ReadAll` without a deadline. (fiber is
   unaffected: fasthttp has already read the whole body before the handler runs.)

---

### F2 — *"the read's own error distinguishes absent/EOF from oversize"* is FALSE: `io.ReadAll` returns `err == nil` for an absent, empty, whitespace-only AND truncated body
**Severity:** Critical
**Bundle says:** ADR §Decision — *"⚠ **The three discarding sites get the opposite instruction.** …
They must **gain** a path distinguishing *body absent / EOF* (keep ignoring — genuinely optional)
from *body present but oversize*. **Under the read-before-parse rule this is simply the read's own
error**, which removes the previous revision's `errors.As(err, new(*http.MaxBytesError))` shape."
Plan phase 2 repeats it verbatim: *"the read's own error now distinguishes *absent/EOF* (keep
ignoring) from *oversize*"*.

**The case it misses:** under read-before-parse the read produces **no error at all** for an absent
body. The `io.EOF` that today's `_ = json.NewDecoder(req.Body).Decode(&in)` relies on is generated
by the **decoder**, not by the read; `io.ReadAll` treats EOF as success and returns
`([]byte{}, nil)`. The signal moves to the *unmarshal*, and there it changes shape: `json.Unmarshal`
on an empty buffer returns a `*json.SyntaxError` reading `"unexpected end of JSON input"`, which is
**not** `io.EOF` and does **not** satisfy `errors.Is(err, io.EOF)`.

**Evidence:** executed, EXIT=0:
```
body=""                     readAll_err=<nil> len=0  unmarshal_err=unexpected end of JSON input  isEOF(unmarshal)=false || decoder_err=EOF            isEOF(decoder)=true
body="   "                  readAll_err=<nil> len=3  unmarshal_err=unexpected end of JSON input  isEOF(unmarshal)=false || decoder_err=EOF            isEOF(decoder)=true
body="{\"add_attempts\":1"  readAll_err=<nil> len=17 unmarshal_err=unexpected end of JSON input  isEOF(unmarshal)=false || decoder_err=unexpected EOF isEOF(decoder)=false
req.Body == http.NoBody? true; readAll len=0 err=<nil>
```

**Consequence:** the sentence that tells three parallel phase-2 agents *how* to implement the most
delicate part of the delivery is wrong about the mechanism. An agent that follows it literally —
"switch on the read's error" — writes a guard that can never see the absent-body case, and the
correct discriminator (`len(buf) == 0`, or keeping the `_ =` on the unmarshal) is stated nowhere in
the bundle. The failure mode is silent: the route still works, and the test that would have caught a
*wrong* guard (phase 2 test 5, `TestBodyAbsentOnTheOptionalRouteStillSucceeds`) is described with
the wrong falsifier — its stated falsifier is *"an implementer who 413s an absent body"*, while the
real failure an implementer following the ADR will produce is a 400 from
`"unexpected end of JSON input"`.

**Fix:** replace the sentence in the ADR and the plan with the executed mechanism:
> the read yields `(buf, nil)` for an absent, empty or truncated body and
> `(partial, *http.MaxBytesError)` only for oversize. *Oversize* is therefore the read's error;
> *absent* is `len(buf) == 0` — **not** an `io.EOF` from the unmarshal, which no longer occurs.

and extend plan phase-2 test 5 to a table with rows `absent`, `empty string`, `whitespace-only`,
`truncated` — all expected 2xx — plus `oversize` → 413.

---

### F3 — The ADR does not say *how* to unmarshal from the buffer, and the two obvious readings differ on the wire for **under-cap** trailing bytes (400 vs 2xx). No prescribed test discriminates them.
**Severity:** Critical
**Bundle says:** ADR §Decision — *"then **unmarshal from the resulting buffer**"*. Plan phase 2 —
*"`io.ReadAll(http.MaxBytesReader(w, body, n))`, then unmarshal from the buffer"*. Plan §2's
decision→phase map row is *"read-before-parse at the **36 propagating** sites"*.

**The case it misses:** `json.Unmarshal(buf, &v)` and `json.NewDecoder(bytes.NewReader(buf)).Decode(&v)`
are both "unmarshal from the buffer" and they disagree on exactly the input class this ADR exists
to fix — a complete JSON value followed by trailing bytes — **whenever the whole thing fits under
the cap**. `json.Unmarshal` rejects it; the decoder-over-buffer keeps today's silent acceptance.

**Evidence:** executed against the real `httpcore.StartInput` DTO, EXIT=0:
```
body="{\"def_ref\":\"a:b:1\"} trailing-garbage"
   TODAY  Decode:    err=workflow-model: invalid qualifier: bad version in "a:b:1": …
   PROP-A Unmarshal: err=invalid character 't' after top-level value
body="{\"def_ref\":\"a:b:1\"}{\"def_ref\":\"c:d:1\"}"
   TODAY  Decode:    err=… (first value only, trailing object silently dropped)
   PROP-A Unmarshal: err=invalid character '{' after top-level value
```

**Consequence:** a wire-visible behaviour change that the ADR's BREAKING list does **not** contain.
The list says *"requests that succeed today by virtue of the trailing-byte gap begin failing with
**413**"* — but a 200-byte request with trailing bytes was never oversize, and under the
`json.Unmarshal` reading it begins failing with **400**, on the default configuration, for every
consumer. HTTP request smuggling / duplicate-JSON-object payloads that today reach the engine as
"first value wins" also change class. And because the discrimination is invisible to every
prescribed test (plan phase 2 test 2 row 3 pins only the **3 MiB** trailing case, which the read
rejects regardless of unmarshal choice), three parallel agents can pick differently and the suite
stays green — a per-adapter divergence in the delivery whose headline is *"one policy, one status"*.

**Fix:** name the call. Recommend `json.Unmarshal` (it closes the trailing-byte gap for **all**
sizes, which is what the ADR's own Context argues for, and matches fiber's existing
`c.Bind().JSON`), then (i) add the under-cap trailing-bytes row to plan phase-2 test 2 with
expected **400**, (ii) add the under-cap case to the phase-3 parity suite, and (iii) add a third
BREAKING bullet to the ADR: *"a body with trailing bytes below the cap changes from 2xx to 400"*.

---
### F4 — The prescribed migration procedure cannot produce the measurement it exists to produce: in the unbounded mode it mandates, stdlib and gin perform **no body read**, and the histogram is recorded **at the body read**
**Severity:** Critical
**Bundle says:** three sentences that cannot all hold. ADR §Decision, Instrumentation:
1. *"`wrkflw_rest_request_body_bytes` is recorded **in each adapter**, at the body read."*
2. *"⚠ **And not at `json.Decoder`**, which measures what it *consumed*, not what arrived."*
3. *"⚠⚠ **The histogram is truncated at the cap** … The procedure is therefore: **run with
   `MaxBodyBytes` explicitly `0`, observe the distribution, then choose a cap.**"*

and, from the same section: *"⚠ **When unbounded, stdlib and gin keep streaming into the decoder**
rather than buffering."*

**The case it misses:** the unbounded path has **no body read**. That is its defining property — the
ADR removes it deliberately, and correctly, because an unbounded `io.ReadAll` is a
memory-exhaustion primitive. So on stdlib and gin, the one instrument the migration depends on has
no site to be recorded at, in the one mode the migration requires. The only remaining place to count
bytes is a counting reader feeding the decoder — which counts exactly *what the decoder consumed*,
the measurement sentence 2 forbids by name (and which, per the ADR's own Context, stops at the end
of the first JSON value and misses every trailing byte).

**Evidence:** reasoned from the bundle's own three sentences — not executed, and it does not need to
be: the ADR states that the unbounded path does not read the body, and states that the histogram is
recorded at the body read. Corroborating source fact, read at the anchor: `httpcore.Instrumentation`
(`transport/http/httpcore/observability.go`) exposes only `Observe`, which is called from
`stdlib/observe.go`, `gin/observe.go`, `fiber/observe.go` **around** the handler — there is no
existing per-request hook inside the handler that sees the body.

**Consequence:** a consumer follows the documented upgrade path — set `MaxBodyBytes = 0`, run for a
week, read `wrkflw_rest_request_body_bytes`, pick a cap — and the histogram is **empty** on stdlib
and gin. They then either pick 1 MiB blind (the outcome the procedure exists to avoid) or, worse,
conclude the metric is broken and turn the cap on with no data. The single stated migration story
for a **default-on breaking change** does not work.

**Fix:** pick one and write it down.
- **(a)** Record the histogram in the unbounded path from `req.ContentLength` when it is `>= 0`, and
  from a counting reader otherwise, with the ADR stating plainly that the unbounded measurement is
  *declared* size (and is absent for chunked requests). Cheap, honest, and enough to choose a cap.
- **(b)** Record from a counting `io.TeeReader` around `req.Body` and accept that it under-counts
  trailing bytes — but then sentence 2 must be softened to *"not at `json.Decoder` **when capped**"*.
- **(c)** Drop the unbounded-run migration procedure and replace it with an explicit
  `ASSUMPTION (unverified)`-labelled recommendation to start at a generous cap (e.g. 8 MiB) and
  tighten using the rejection counter, which **does** work in the capped mode.

Whichever is chosen, the plan needs a phase-2 test `TestBodyBytesHistogramRecordsWhenUnbounded`.
**Falsifier:** it fails against any implementation that records the histogram only on the capped
branch — which is every implementation the current plan text describes.

---

### F5 — *"`0` → unbounded"* is FALSE on fiber: the framework still rejects at `fiber.Config.BodyLimit` (4 MiB by default), with plain text and no `ErrorBody`. The opt-out that the migration procedure depends on does not opt out on one of the three adapters.
**Severity:** Critical
**Bundle says:** ADR §Decision — *"**`0` → unbounded**, the explicit opt-out"*; §Consequences,
Positive — *"**The opt-out actually opts out.** `MaxBodyBytes` set to `0` disables the cap"*; and
§Decision, on fiber — *"Executed: at 8 MiB the route group is **never reached** — the client
receives fasthttp's `text/plain` `Request Entity Too Large`, with no `ErrorBody` and no log line.
**`MaxBodyBytes > 4 MiB` is therefore silently ineffective on fiber**."*

**The case it misses:** the bundle applies its own executed finding to `MaxBodyBytes > 4 MiB` and
never applies it to `MaxBodyBytes == 0`. Unbounded is `> 4 MiB` in every sense that matters. On
fiber, `MaxBodyBytes = 0` does not remove the ceiling — it hands the ceiling to
`fiber.Config.BodyLimit`, which `fiber.New()` defaults to `DefaultBodyLimit = 4 MiB` and which
`fiber.New` copies to `app.server.MaxRequestBodySize`.

**Evidence:** source, at the pinned version:
```
fiber/v3@v3.4.0/app.go:585   DefaultBodyLimit = 4 * 1024 * 1024
fiber/v3@v3.4.0/app.go:709   if app.config.BodyLimit <= 0 {
fiber/v3@v3.4.0/app.go:710       app.config.BodyLimit = DefaultBodyLimit
fiber/v3@v3.4.0/app.go:1516  app.server.MaxRequestBodySize = app.config.BodyLimit
```
plus the bundle's own executed result at 8 MiB, which is the same mechanism.

**Consequence:** two compounding failures.
1. **The migration observation window is itself breaking on fiber.** A consumer told to "run with
   `MaxBodyBytes = 0` and observe" gets framework plain-text 413s for every request above 4 MiB
   during the observation window — wire-visible, unlogged, un-enveloped, and attributed to nothing.
2. **The distribution they are told to observe is truncated at 4 MiB on fiber anyway** — the exact
   defect ("the histogram is truncated at the cap") that motivated the unbounded run, reappearing in
   the mode that was supposed to cure it.

Combined with F4 this means the migration procedure yields: no data on stdlib, no data on gin, and
data truncated at 4 MiB on fiber.

**Fix:** the ADR must say, in the same paragraph that defines the tri-state, that **`0` means
"wrkflw imposes no cap", not "the request is unbounded"** — on fiber the effective ceiling is
`fiber.Config.BodyLimit`, and on stdlib/gin it is whatever the consumer's `http.Server` and
reverse proxy impose. Extend the fiber mount-time WARN to fire for `MaxBodyBytes == 0` as well as
for `> DefaultBodyLimit` (an unbounded configuration on fiber is *more* surprising, not less), and
add a phase-3 parity note that the unbounded parity case must be pinned below 4 MiB for the same
reason the 413 case is.

---

### F6 — *"negative → a construction error, surfaced at mount time"* has **no return channel**: every mount entry point on all three adapters returns nothing, and the only two ways to implement it are a panic or a break to the public `RouteCustomizer[R]` extension seam
**Severity:** Critical
**Bundle says:** ADR §Decision — *"**negative → a construction error**, surfaced at mount time rather
than per request."* Plan §2 decision→phase map — *"negative `MaxBodyBytes` is a construction error at
mount | **1** | **`httpcore`**"*. Plan phase 1 test 3 —
*"`TestNegativeMaxBodyBytesIsRefusedAtMount` — the construction error is reachable."* Spec §6
Non-goals — *"**No new exported interface and no new cross-package contract.**"*

**The case it misses:** there is nowhere for an error to go. Source, at the anchor:
```go
// transport/http/httpcore/seam.go
func ResolveConfig[R any](opts ...CustomizeOption[R]) CustomizeConfig[R]   // no error
type CustomizeOption[R any] func(*CustomizeConfig[R])                      // no error
type RouteCustomizer[R any] interface { Customize(r R, opts ...CustomizeOption[R]) } // no error
func MountGroups[R any](r R, groups ...RouteCustomizer[R])                 // no error
// transport/http/stdlib/mount.go
func Mount(mux *http.ServeMux, svc service.Service, opts ...httpcore.CustomizeOption[*http.ServeMux]) // no error
// transport/http/fiber/mount.go
func Mount(r fiberlib.Router, svc service.Service, opts ...httpcore.CustomizeOption[fiberlib.Router]) // no error
```
`RouteCustomizer[R]` is not an internal detail — `seam.go` documents it as *"the consumer extension
seam: any `RouteCustomizer[R]` — **including a consumer's own** — can be passed."* Adding an `error`
return breaks every consumer implementation of it, which is precisely the "new cross-package
contract" the spec forbids. The only alternative is `panic`, which the bundle never names, and which
for a library is a design decision requiring its own justification.

Worse, an option-level guard cannot close it either: `CustomizeConfig` is **exported specifically so
consumers may author their own `CustomizeOption[R]`** (`seam.go`, struct doc comment), so a consumer
can assign a negative `*int64` to the field directly, bypassing any validation inside a
`WithMaxBodyBytes` constructor.

**Evidence:** source read at the anchor (quoted above); `grep -rn "ResolveConfig" --include="*.go"`
returns 15 non-test call sites, all of the form `cfg := httpcore.ResolveConfig(opts...)` inside a
`Customize` method whose signature is fixed by the interface. No call site can propagate an error.

**Consequence:** three parallel phase-2 agents, and a phase-1 agent, are asked to implement a
mechanism that does not exist. The likely outcomes are (i) a silent `panic` inside `Mount`, which on
`stdlib.Mount` — three sequential `Customize` calls — leaves a **partially populated `*http.ServeMux`**
that a consumer's `recover()` will happily keep serving, or (ii) the check quietly degrades to
"clamp negative to the default and log", which is behaviour the ADR does not describe and no test
pins. Either way `TestNegativeMaxBodyBytesIsRefusedAtMount` is written against whatever the
implementer chose, i.e. it certifies nothing.

**Fix:** decide it in the ADR, not in implementation. The lowest-cost option that keeps the seam
intact: **treat a negative value exactly as `nil`** (fall back to the 1 MiB default) and log a WARN
naming the field and the value — a negative cap has no coherent meaning, the fail-closed direction
is unambiguous, and it needs no signature change. Whatever is chosen, the plan's decision→phase map
row must move off `httpcore` (which has no mount function) onto the adapters, and the test must be
renamed to what it actually asserts.

---

### F7 — Nothing in the bundle owns the `wrkflw_rest_request_body_bytes` **instrument**, only its recording; `httpcore.Instrumentation`'s fields are unexported, so three parallel agents must each mint their own
**Severity:** Major
**Bundle says:** ADR §Decision — *"`wrkflw_rest_request_body_bytes` is recorded **in each adapter**,
at the body read. ⚠ **Not in `httpcore`** — that package has **0** decode sites and never sees a
body."* Plan §2 map — *"`wrkflw_rest_request_body_bytes` histogram + rejection counter | **2** |
`stdlib` \| `gin` \| `fiber`"*, with **no `httpcore` row**. Plan phase 1 §Symbols lists only
`CustomizeConfig.MaxBodyBytes` and `ErrRequestBodyTooLarge`.

**The case it misses:** the sentence conflates the **recording site** with the **instrument-declaration
site**. Recording happens in the adapter; the `metric.Float64Histogram` and `metric.Int64Counter`
have to be *created* somewhere, from the consumer's `MeterProvider`, once per mount. Today that is
`httpcore.NewInstrumentation(cfg)`, and its fields are unexported:
```go
type Instrumentation struct {
    tracer     trace.Tracer
    counter    metric.Int64Counter
    histogram  metric.Float64Histogram
    propagator propagation.TextMapPropagator
}
```
There is no exported accessor, no exported `Record…` method, and no way for `stdlib` to add a
histogram to it. So the phase-2 agents' only in-plan option is for each adapter to call
`observability.New(instrumentationScope, …)` itself — three independent constructions of the same
two instrument names, with independently authored descriptions and units, in three worktrees that
by the plan's own fan-out rule **cannot see each other's code**.

**Evidence:** source read at the anchor — `transport/http/httpcore/observability.go` (struct above,
`Observe` the only method); `grep -n "NewInstrumentation" transport/http/*/groups.go` shows it called
once per `Customize` in all three adapters (5 groups × 3 adapters = 15 sites).

**Consequence:** either (i) a phase-1 `httpcore` change lands that no row of the decision→phase map
authorises — the plan's own stated defect criterion, *"a row with no phase is a defect"*, inverted —
or (ii) three divergent instrument registrations. Under the OTel SDK, two instruments with the same
name but different descriptions or units on the same meter produce a **duplicate-instrument
conflict**: the consumer gets a logged warning and two exported streams, and the metric the
migration procedure depends on is unusable for cross-adapter comparison.

**Fix:** add an explicit phase-1 `httpcore` row and symbol: extend `Instrumentation` with the two
instruments and one exported method — e.g.
`func (i *Instrumentation) RecordRequestBody(ctx context.Context, method, routeTemplate string, n int64, rejected bool)`
— created once in `NewInstrumentation` from the same `Telemetry`. That keeps the "never sees a body"
property intact (the adapter still supplies the number), removes the duplicate-registration hazard,
and gives phase 3 one name to assert. The ADR sentence should be corrected to
*"recorded **from** each adapter, on the instrument `httpcore` owns"*.

---

### F8 — Replacing gin's `ShouldBindJSON` with a raw unmarshal silently drops two consumer-settable gin decoder behaviours, one of which changes how **process variables** are typed
**Severity:** Major
**Bundle says:** ADR §Decision — *"`stdlib` and `gin`: read the body through `http.MaxBytesReader`
to completion (`io.ReadAll`), then **unmarshal from the resulting buffer**."* Plan phase 2 says the
same. Neither document names gin's binder, and the plan's per-package test list contains nothing
about decoder options.

**The case it misses:** gin's `ShouldBindJSON` does not call `json.Unmarshal` — it calls
`binding.decodeJSON`, which honours two package-level globals a consumer may have set:
```go
// gin@v1.12.0/binding/json.go
var EnableDecoderUseNumber = false
var EnableDecoderDisallowUnknownFields = false
func decodeJSON(r io.Reader, obj any) error {
    decoder := json.API.NewDecoder(r)
    if EnableDecoderUseNumber            { decoder.UseNumber() }
    if EnableDecoderDisallowUnknownFields { decoder.DisallowUnknownFields() }
    if err := decoder.Decode(obj); err != nil { return err }
    return validate(obj)
}
```
`EnableDecoderUseNumber` is not cosmetic **in this codebase**: `StartInput.Vars map[string]any`,
`SignalInput.Payload`, `MessageInput.Payload` and `CompleteInput.Output` are all
`map[string]any` that become **process-instance variables**, are evaluated by `expr-lang` in gateway
conditions, and are persisted. A consumer who set `EnableDecoderUseNumber` to keep int64 identifiers
from becoming lossy `float64` loses that silently — on gin only, so their stdlib deployment and
their gin deployment now disagree about the *value* of a variable, not merely a status code.
`validate(obj)` (a consumer-installable `binding.Validator`) is dropped too.

**Evidence:** source at the pinned version `gin@v1.12.0/binding/json.go:16-55`, quoted verbatim
above. Confirmed that the repo's own DTOs use `validate:` tags handled by `httpcore/validate.go`,
**not** gin `binding:` tags — so the *default* validator path is a genuine no-op, but the globals and
a custom `binding.Validator` are not.

**Consequence:** a silent, gin-only, data-level regression, invisible to every test in the plan
(none of which sets the globals) and invisible to the parity suite (which does not set them either).

**Fix:** on gin, "unmarshal from the buffer" must be **`binding.JSON.BindBody(buf, &in)`** — gin's
own buffer-entry point, which routes through the identical `decodeJSON` and preserves both globals
and the validator. Name it in the ADR and the plan. ⚠ **Interacts with F3:** `BindBody` uses a
*decoder* over the buffer, so it keeps trailing-byte tolerance; if stdlib uses `json.Unmarshal`,
under-cap trailing bytes become 400 on stdlib and 2xx on gin — a new cross-adapter divergence in the
delivery whose headline is "one policy, one status". F3 and F8 must be resolved **together**.

---
### F9 — *"the read's own error"* is a **taxonomy**, not a signal: a truncated or aborted request produces `unexpected EOF`, not `*http.MaxBytesError`, and the prescribed rule turns today's 400 into a 413
**Severity:** Critical
**Bundle says:** ADR §Decision — *"Under the read-before-parse rule this is **simply the read's own
error**, which removes the previous revision's `errors.As(err, new(*http.MaxBytesError))` shape and
the per-adapter divergence that came with it."* Plan phase 2 repeats it and explicitly presents the
removal of the `errors.As` check as a *simplification*.

**The case it misses:** `io.ReadAll(http.MaxBytesReader(...))` returns a non-nil error for at least
four distinct causes, only one of which is oversize:

| cause | error | is `*http.MaxBytesError`? | correct status |
|---|---|---|---|
| body exceeds the cap | `http: request body too large` | **yes** | 413 |
| `Content-Length` declares more than the client sent, client aborts | `unexpected EOF` | **no** | 400 |
| client resets the connection mid-body | network error | **no** | 400 / no response |
| request context cancelled, or the body is read after the handler's deadline | `context.Canceled` / `http: invalid Read on closed Body` | **no** | 499 / 500 |

The ADR's rule maps *all* of them to `ErrRequestBodyTooLarge` → **413**. The `errors.As` check it
proudly deletes is the only thing that distinguishes them, and it was deleted for the wrong reason:
it was per-adapter *divergent* under capping-during-parse, but under read-before-parse it is
per-adapter *identical* — both stdlib and gin surface the bare `*http.MaxBytesError` (spec §2 records
this as already-executed: *"stdlib and gin both surface the **bare** `*http.MaxBytesError`. Two
shapes, not three."*). The bundle establishes the fact and then discards the mechanism that uses it.

**Evidence:** executed against a **real `http.Server`** on a real socket, `/tmp/probe3` (deleted),
`go test -count=1 -v` EXIT=0:
```
CL larger than body (abort):       ContentLength=5000        read=7 err=unexpected EOF isMaxBytesError=false
honest small body:                 ContentLength=7           read=7 err=<nil>          isMaxBytesError=false
CL declares 10 GiB:                ContentLength=10737418240 read=7 err=unexpected EOF isMaxBytesError=false
```

**Consequence:** any client that aborts a POST mid-body — a mobile client losing signal, a load
balancer timing out, a `curl` interrupted with Ctrl-C, an HTTP/1.1 client with a wrong
`Content-Length` — now receives **413 Request Entity Too Large** for a request that was not too
large. That is a wrong, actionable-looking status: the operator's runbook says "the body exceeded the
cap", the consumer raises `MaxBodyBytes`, and nothing changes. With no log record and no correlation
id (the ADR's own stated gap), there is nothing to contradict the wrong status.

**Fix:** keep `errors.As(err, new(*http.MaxBytesError))` on stdlib and gin — it is now *uniform*
across both, which is exactly what the ADR wanted. Only that error becomes
`ErrRequestBodyTooLarge`; every other read error keeps today's
`fmt.Errorf("%w: %w", httpcore.ErrBadInput, err)` → 400. Add a plan phase-2 test row:
*"`Content-Length: 5000`, 7 bytes sent, client aborts → **400**, not 413."*
**Falsifier:** it fails against any implementation that classifies the read's error by non-nil-ness,
which is what the ADR's current sentence prescribes.

---

### F10 — `Content-Length` is never consulted, so every oversize request costs the server a full cap's worth of ingress before it is rejected — a cheap early rejection the design leaves on the table
**Severity:** Major
**Bundle says:** nothing. The words `Content-Length` do not appear in the ADR, the spec, or the plan.
The design keys entirely on the read: ADR §Decision — *"The oversize condition is decided by the
**read**, not by what a decoder chose to consume."*

**The case it misses:** an attacker sending `Content-Length: 10737418240` is rejected — but only
after the server has accepted, buffered and discarded `MaxBodyBytes + 1` bytes. At the 1 MiB default
and a modest 1 000 rps, that is ~1 GB/s of ingress the process must read, allocate (`io.ReadAll`
grows its buffer by repeated `append`, so ~2× the cap transiently) and throw away, per second, to
serve zero requests. Reading `r.ContentLength` first costs one integer comparison and rejects at
**0 bytes**.

**Evidence:** executed (same probe as F9): the handler is entered with
`ContentLength=10737418240` **before any body byte is read** — the declared length is available for
free at handler entry.
```
CL declares 10 GiB:  ContentLength=10737418240 read=7 err=unexpected EOF isMaxBytesError=false
```
`io.ReadAll`'s growth behaviour is stdlib-documented (`append` with capacity doubling from a 512-byte
seed), so the transient allocation for a 1 MiB body is up to ~2 MiB — reasoned, not measured.

**Consequence:** the delivery's headline is closing a resource-exhaustion hole, and it leaves the
cheapest half of the mitigation unimplemented and undiscussed. It also interacts with F1: a
`Content-Length` pre-check would reject the stalling-client attack at header time, before the
hold-open even begins, for the subset of attackers who declare an oversize length.

**Fix:** add to the ADR: *"when `r.ContentLength >= 0 && r.ContentLength > n`, reject with
`ErrRequestBodyTooLarge` **before reading any body byte**; the `MaxBytesReader` remains as the
authoritative check for chunked requests and for clients that under-declare."* Add a plan phase-2
test asserting the handler returns 413 without consuming the body (assert via a body reader that
records whether `Read` was ever called). **Falsifier:** it fails against any implementation that
reads first — i.e. every implementation the current bundle describes. ⚠ State explicitly that the
header check may **not** be the only check, or an under-declaring client bypasses the cap entirely.

---

### F11 — Fiber above `fiber.DefaultBodyLimit` swallows **all** wrkflw telemetry — span, request counter, duration histogram, body histogram, rejection counter — not just "no `ErrorBody` and no log line"
**Severity:** Major
**Bundle says:** ADR §Decision — *"Executed: at 8 MiB the route group is **never reached** — the
client receives fasthttp's `text/plain` `Request Entity Too Large`, **with no `ErrorBody` and no log
line**."* §Consequences repeats: *"Fiber diverges above `fiber.DefaultBodyLimit`: framework
plain-text 413, **no `ErrorBody`, no log**."* Plan phase 3 repeats the same two-item list.

**The case it misses:** "the route group is never reached" is a much larger claim than the two items
the bundle draws from it. `observed()` — the wrapper that calls `Instrumentation.Observe` and
therefore produces the span, `wrkflw_rest_requests_total` and
`wrkflw_rest_request_duration_seconds` — **is registered as part of the route handler chain**. If the
handler is never reached, none of it runs. So on fiber, above `BodyLimit`, wrkflw emits *no signal
of any kind*.

**Evidence:** source at the anchor. `transport/http/fiber/groups.go` registers every route as
`rt.Post(path, observed(inst, "POST", path, func(c fiberlib.Ctx) error {…}))`; `observed` in
`transport/http/fiber/observe.go` is the only caller of `inst.Observe` in the package, and it is
*inside* the handler. fasthttp rejects at `app.server.MaxRequestBodySize`
(`fiber/v3@v3.4.0/app.go:1516`), i.e. before routing. Reasoned from that source — not executed, and
the bundle's own executed result ("the route group is never reached") is the premise.

**Consequence:** the ADR's stated gap is *"a consumer debugging a 413 today has the status and
nothing else"*. On fiber above `BodyLimit` they have **less than that**: the 413 does not appear in
`wrkflw_rest_requests_total{http.status_code="413"}`, so the operator's dashboard shows a *drop in
traffic*, not a rise in rejections. That is the worst diagnostic shape — a failure that looks like an
absence. And it silently applies to the **unbounded** configuration too (see F5), which is the mode
the migration procedure mandates.

**Fix:** correct the two enumerations in the ADR and the plan to *"no `ErrorBody`, no log record, and
**no wrkflw metric or span at all** — the rejection is invisible to every wrkflw signal."* Then
`SECURITY.md` (phase 4) must tell the fiber consumer the only place the rejection is observable is
their own fasthttp/proxy access log, and the phase-3 fiber-only divergence case should assert the
absence of the metric, not only the absence of the envelope.

---

### F12 — The stated gap *"a consumer debugging a 413 has the status and nothing else"* is **pessimistic and therefore unactionable**: three signals already exist, and phase 4 points the operator at none of them
**Severity:** Major
**Bundle says:** ADR §Consequences — *"**A 413 body carries no correlation id and produces no log
record**, because that machinery belongs to the deferred 4xx delivery. A consumer debugging a 413
today has the status and nothing else. ⚠ Stated rather than implied."* Spec §3 repeats it. Plan
phase 4's `SECURITY.md` bullet list does not mention diagnosis at all.

**The case it misses:** "nothing else" is false in the consumer's favour, and the honesty of the
sentence is not the problem — its *uselessness* is. On stdlib and gin (and on fiber below
`BodyLimit`) a 413 produced by this delivery is already visible in three places, none of which the
bundle names:
1. **`wrkflw_rest_requests_total{http.method, http.route, http.status_code="413"}`** — per-route, so
   a 413 storm is attributable to a route without any new machinery;
2. **`wrkflw_rest_request_duration_seconds`** with the same labels;
3. an **OTel span** `wrkflw.rest POST /instances` carrying `http.route` and `http.status_code`,
   with the incoming trace context already extracted from the request headers — i.e. a consumer
   running distributed tracing *does* get correlation, just not a wrkflw-minted correlation id.

Plus the two instruments this delivery adds. The real gap is narrower and sharper than stated: **no
per-request record naming the offending route *and* the observed size *and* the configured cap**.

**Evidence:** source at the anchor. `httpcore.Instrumentation.Observe`
(`transport/http/httpcore/observability.go`) records `wrkflw_rest_requests_total` and
`wrkflw_rest_request_duration_seconds` labelled with `http.status_code = strconv.Itoa(status)`, and
starts a span with `http.route`. All three adapters route every handler through it and report the
written status: `stdlib/observe.go` via `statusRecorder.code`, `gin/observe.go` via
`gc.Writer.Status()`, `fiber/observe.go` via `c.Response().StatusCode()`. Reasoned from that source;
the status path for a 413 is the same `writeErr` → `writeJSON`/`c.Status(...).JSON(...)` path every
existing 4xx already takes.

**Consequence:** the ADR discharges an operational hazard by *declaring* it, and phase 4 writes
nothing that would let an operator act. A consumer hitting a 413 storm after upgrading has a
five-minute answer (query `wrkflw_rest_requests_total` by route and status) that no document tells
them about — so they will instead reach for the thing the ADR told them they do not have.

**Fix:** replace "and nothing else" with the accurate list, and give phase 4's `SECURITY.md` bullet
a concrete diagnosis paragraph: the PromQL/metric names to query, the span name, and the two new
instruments — plus the explicit statement of what is genuinely missing (no per-request log line, no
wrkflw correlation id, deferred to the 4xx delivery) and the two places it is missing *entirely*
(fiber above `BodyLimit`, per F11). ⚠ Also add the **rejection counter's label set** to the ADR — it
is currently specified as "a rejection counter" with no attributes, and an unlabelled counter cannot
answer "which route".

---
### F13 — The fiber WARN is wired to the wrong comparand and therefore **cannot fire in the one case that matters**: `fiber.Config.BodyLimit` **lower** than `MaxBodyBytes`. And the "unreachable" assumption it is built on is FALSE.
**Severity:** Critical
**Bundle says:** ADR §Decision — *"**A WARN is logged when `MaxBodyBytes > fiber.DefaultBodyLimit`.**
⚠ It must be logged from the function the documented mount path actually calls, and it must compare
against the app's real limit **where that is reachable** rather than against the package constant."*
Spec §5 — *"`ASSUMPTION (unverified)`: that `fiber.Config.BodyLimit` is **unreachable** from a mounted
`fiber.Router`. Believed from the API shape; **the WARN's fallback to the package constant depends on
it**."*

**The case it misses:** two failures, one of them silent.

1. **False negative on the default configuration.** A consumer running
   `fiber.New(fiber.Config{BodyLimit: 256 << 10})` — a perfectly ordinary hardening choice — with
   `MaxBodyBytes` left at its default (`nil` → 1 MiB) has a **cap that never bites**: fiber rejects
   at 256 KiB first, with plain text and no telemetry (F11). The condition
   `MaxBodyBytes > DefaultBodyLimit` is `1 MiB > 4 MiB` = **false**, so no WARN is emitted. The
   divergence the WARN exists to announce is precisely the case it is blind to, and it is blind to it
   *in the zero-configuration default*.
2. **Un-silenceable false positive.** A consumer running `fiber.New(fiber.Config{BodyLimit: 64 << 20})`
   with `MaxBodyBytes = 8 MiB` has a **correct** configuration and gets a WARN on every mount, with
   no option to suppress it. A library that logs warnings for correct configurations trains operators
   to filter wrkflw's warnings out — after which the true positives are invisible too.

**Evidence:** executed, `transport/http/fiber/zz_probe_test.go` (created, run, deleted),
`go test -count=1 -v` EXIT=0:
```
configured=0         MOUNT-TIME via r.(*fiber.App).Config().BodyLimit = 4194304
configured=0         MOUNT-TIME on app.Group(): assertable to *fiber.App = false
configured=0         REQUEST-TIME via c.App().Config().BodyLimit = 4194304 ; len(BodyRaw)=7
configured=262144    MOUNT-TIME via r.(*fiber.App).Config().BodyLimit = 262144
configured=262144    REQUEST-TIME via c.App().Config().BodyLimit = 262144 ; len(BodyRaw)=7
configured=67108864  MOUNT-TIME via r.(*fiber.App).Config().BodyLimit = 67108864
configured=67108864  REQUEST-TIME via c.App().Config().BodyLimit = 67108864 ; len(BodyRaw)=7
```
So spec §5's assumption is **refuted in the terms that matter**: the real limit is reachable
(a) at mount time by `r.(*fiberlib.App)` whenever the router *is* the app — which is what
`fiber.Mount`'s own doc comment describes and what every example does — and (b) **always** at request
time via `c.App().Config().BodyLimit` (`fiber/v3@v3.4.0/ctx_interface_gen.go:20 App() *App`,
`app.go:1233 func (app *App) Config() Config`). Note also that `fiber.New` normalises
`BodyLimit <= 0` to `DefaultBodyLimit` (`app.go:709-710`), so `Config().BodyLimit` is *always* the
effective value and the package-constant fallback is never needed for a `*App`.

**Consequence:** the delivery ships a mitigation that is a no-op for the dangerous case and noise for
the safe one, built on an assumption the bundle itself flagged as unverified and then designed
around instead of verifying. Plan phase-2 test 7 (`TestMountWarnsAboveDefaultBodyLimit`) pins the
false-positive direction only, so the false negative is not merely unfixed — it is untested.

**Fix:** change the predicate from `MaxBodyBytes > fiber.DefaultBodyLimit` to
**`effectiveLimit < MaxBodyBytes` (or `MaxBodyBytes == 0`), where `effectiveLimit` is
`app.Config().BodyLimit`** obtained by type-asserting the router to `*fiberlib.App`, falling back to
`fiber.DefaultBodyLimit` only for the `*Group` case (which the probe shows is genuinely not
assertable). Delete the `ASSUMPTION (unverified)` from spec §5 and replace it with the executed
result above. Rename the test `TestMountWarnsWhenFiberBodyLimitIsBelowMaxBodyBytes` and give it a
table: `BodyLimit=256 KiB / MaxBodyBytes=1 MiB → WARN`, `BodyLimit=64 MiB / MaxBodyBytes=8 MiB → no
WARN`, `MaxBodyBytes=0 → WARN`. **Falsifier:** row 1 fails against any implementation comparing to
the package constant — which is the implementation the ADR currently prescribes.

---

### F14 — The WARN's placement is specified as "the documented mount path", but fiber has **six** mount entry points; putting it in `Mount` misses admin-only mounts, and putting it in `Customize` fires it 3–5 times per process
**Severity:** Major
**Bundle says:** ADR §Decision — *"⚠ It must be logged from **the function the documented mount path
actually calls**"* (singular). Plan phase-2 test 7 — *"asserted against a `slog` handler capturing
records, **through the documented mount entry point**"* (singular). Plan §0 item 4 — *"find the real
mount entry point"* (singular).

**The case it misses:** there is no single mount entry point. Source at the anchor,
`transport/http/fiber/`:
```
mount.go:15  func Mount(r fiberlib.Router, svc service.Service, opts ...)      // instance+task+message
mount.go:23  func MountHealth(r fiberlib.Router, checks ...)
groups.go:25   func (g InstanceRoutes) Customize(r fiberlib.Router, opts ...)
groups.go:96   func (g MessageRoutes) Customize(r fiberlib.Router, opts ...)
groups.go:127  func (g TaskRoutes) Customize(r fiberlib.Router, opts ...)
groups.go:222  func (g AdminRoutes) Customize(r fiberlib.Router, opts ...)
groups.go:468  func (g HealthRoutes) Customize(r fiberlib.Router, opts ...)
```
`Mount` is a wrapper over three of the five `Customize` methods, and **`AdminRoutes` is reachable
only by calling `Customize` directly** — ADR-0095 keeps admin routes out of `Mount`, which the
bundle itself cites as the reason the parity suite cannot see the three discarding sites. The repo's
own reference wiring does exactly this: `examples/production_wiring/main.go:274` is
`stdlib.AdminRoutes{Svc: svc}.Customize(adminMux, …)` on a *separate* mux.

So: put the WARN in `Mount` and an admin-only fiber mount never sees it — the mount that carries the
three discarding sites the ADR spends a whole section on. Put it in `Customize` and
`fiber.Mount(app, svc, …)` emits it **three** times, plus once more for admin and once for health —
five identical WARNs per process start.

**Evidence:** source listing above, read at the anchor; `examples/production_wiring/main.go:264,274`
read at the anchor. Reasoned from that structure — not executed.

**Consequence:** whichever placement the single phase-2 fiber agent picks, it is defensible and the
prescribed test passes, because the test names one entry point and there are six. Round 5 already
found this WARN in the wrong function once; the fix ("the documented mount path") re-states the same
singular assumption that produced the original error.

**Fix:** name all five `Customize` methods as the placement (they are the only functions that see
`cfg`), and add `sync.Once`-per-`*App` (or a simple "log once per distinct
`(router, MaxBodyBytes)`") so repeated mounts do not multiply the record. State the expected
**count** in plan phase-2 test 7 — `assert exactly one WARN after fiber.Mount(...)` — not merely
"a WARN is captured". **Falsifier:** the count assertion fails against a per-`Customize` WARN with no
de-duplication, which is the natural implementation.

---

### F15 — This repo already ships a first-party body cap with the **opposite** zero-value convention, and the bundle's grid discusses only the default *value*, not the *sentinel*
**Severity:** Major
**Bundle says:** spec §4, the `D1 × httpcall SSRF` row — *"`action/httpcall.ErrBodyTooLarge` already
exists, means an **outbound response** exceeded 10 MiB, and is a **500** … ⚠ And the two first-party
defaults sit **10× apart** in opposite directions. … ⚠ The 10× divergence is documented, not
reconciled — they bound different things."* Plan §4 enumeration — *"…already capped by us | **0** —
`grep -rnE 'MaxBytesReader|BodyLimit|MaxBytesHandler|LimitReader' **transport/**' exits 1"*.

**The case it misses:** the boundary is `transport/`, and the prior art sits one step outside it.
`action/httpcall` does not merely own a colliding *name* — it owns a working implementation of
**exactly this mechanism** (`io.ReadAll(io.LimitReader(r, max+1))`, i.e. read-to-cap-then-decide),
with a **plain `int64`** and the documented convention *"a non-positive value disables the bound"*,
repeated in six places:
```
action/httpcall/httpcall.go:36   // Override the cap with [WithMaxResponseSize]; a non-positive value disables it.
action/httpcall/httpcall.go:88   // Overridable via [WithMaxResponseSize]; a non-positive value disables the bound.
action/httpcall/httpcall.go:112  // A non-positive value disables the bound. See [WithMaxResponseSize].
action/httpcall/httpcall.go:183  // A non-positive n disables the bound. The default is 10 MiB.
action/httpcall/httpcall.go:189  // A non-positive max disables the bound.
action/README.md:146             | `WithMaxResponseSize(n int64)` | … `n <= 0` disables …
```
ADR-0186 mints, in the same module, a second body cap where a plain `int64` zero would mean *the
default* (hence the pointer), `0` means disabled, and **negative is a construction error** — the one
value `httpcall` documents as "disables the bound".

**Evidence:** `grep -n "MaxResponseSize" -B3 -A10 action/httpcall/httpcall.go` and
`sed -n '188,203p' action/httpcall/httpcall.go`, both read at the anchor; quoted verbatim above.
Executed grep, not merely reasoned.

**Consequence:** a consumer who has already tuned `WithMaxResponseSize(-1)` to disable the outbound
cap, and who reads that convention as the library's, writes `MaxBodyBytes: ptr(int64(-1))` and gets a
construction error (or a panic — see F6) instead of "disabled". Two first-party caps in one module
with three incompatible sentinel meanings for the same number is a support hazard of exactly the
kind the ADR's §4 grid flags for the *name* collision, applied to the *semantics* and missed.

**Fix:** either (a) adopt `httpcall`'s convention — plain `int64`, `n <= 0` disables — and solve the
"nil vs explicit 0" defaulting problem the way the tri-state was invented for, which then requires an
explicit `WithoutBodyLimit()` option rather than a magic number; or (b) keep the tri-state, make
negative behave as `nil` (per F6), and add a row to spec §4's grid recording the divergence and why
it is accepted. Either way the ADR must cite `httpcall.readAllCapped` as **prior art for the
mechanism**, in the same way it cites ADR-0160 as prior art for the trailing-data guard — the
sentence *"⚠ This is prior art in the same repo that two audits and four revisions walked past"*
applies a second time, to a second piece of prior art, in the same bundle.

---

### F16 — The prescribed test row *"`httpcall.ErrBodyTooLarge` still classifies 500"* **cannot fail**, and no falsifier is stated for it
**Severity:** Major
**Bundle says:** ADR §Decision — *"a test asserts `httpcall.ErrBodyTooLarge` still classifies
**500**."* Plan §2 map gives it its own row. Plan phase 1 test 1 —
*"⚠ **Plus a row asserting `httpcall.ErrBodyTooLarge` still classifies 500.**"*, under a
**Fails today:** line that reads *"the sentinel does not exist → compile error"* — which is the
falsifier for the *new* sentinel's rows, not for this one.

**The case it misses:** `httpcall.ErrBodyTooLarge` is a distinct `errors.New` value
(`action/httpcall/httpcall.go:94`) and is referenced **nowhere outside `action/httpcall`**. It
matches no arm of `ClassifyError` and therefore falls to the `default: 500`. After the change it
still matches no arm, because the new 413 arm tests `errors.Is(err, ErrRequestBodyTooLarge)` — a
different value. There is no implementation an agent could plausibly write that makes this row red.
Per this repo's own Premise Discipline rule (*"When a plan prescribes a test, it must also state what
makes that test fail today"*), it is a vacuous test — and this lineage has already shipped six of
those in one delivery.

**Evidence:** executed grep at the anchor:
```
$ grep -rn "ErrBodyTooLarge" --include="*.go" . | grep -v action/
(no output — exit 1)
```
i.e. the sentinel has zero call sites outside its own package, so it never reaches `ClassifyError`
from any production path.

**Consequence:** a green row that certifies nothing occupies a slot in the plan's most load-bearing
test, and the *real* risk it was meant to insure against — that an implementer, seeing "body too
large", adds `errors.Is(err, httpcall.ErrBodyTooLarge)` to the 413 arm — is not what the row asserts.
It also crowds out the row that *would* be load-bearing.

**Fix:** replace the row with the one that can fail: **an error that wraps both
`ErrRequestBodyTooLarge` and `ErrBadInput` must classify 413** (already prescribed, keep it) **and an
error wrapping both `ErrRequestBodyTooLarge` and `validation.ErrInvalidInput` must classify 413**,
plus a `go/parser`-style or plain-grep assertion in the same test file that the 413 arm's `errors.Is`
list does **not** name `httpcall.ErrBodyTooLarge`. If the 500 row is kept, restate its falsifier
honestly: *"this row is a regression pin with no failing implementation today; it exists to make a
future edit that folds the two sentinels together turn red."*

---

### F17 — The migration procedure requires a metrics stack the library never configures, and **two of the three reference wirings do not have one**
**Severity:** Major
**Bundle says:** ADR §Decision — *"The procedure is therefore: **run with `MaxBodyBytes` explicitly
`0`, observe the distribution, then choose a cap.**"* §Consequences — *"The histogram cannot be read
while the cap is on; the migration procedure requires a deliberate unbounded run first."* Neither the
ADR nor the plan states any non-metrics fallback.

**The case it misses:** the histogram only exists if the consumer supplied a `MeterProvider`.
`httpcore.NewInstrumentation` passes `cfg.MeterProvider` into `observability.New`, and
`observability.New` falls back to `otel.GetMeterProvider()` when it is nil — which is OTel's global
**noop** provider unless the consumer has called `otel.SetMeterProvider`. So for any consumer who has
not wired OTel metrics, the prescribed migration produces a histogram that records into a noop and a
rejection counter that counts into a noop.

**Evidence:** source at the anchor —
`internal/observability/observability.go:83-85` (`if cfg.mp == nil { cfg.mp = otel.GetMeterProvider() }`,
with the package doc stating *"defaults to … the OTel global providers, with a noop fallback"*), and
executed grep over the repo's own reference wiring:
```
$ grep -rn "SetMeterProvider\|MeterProvider" examples/
examples/production_wiring/main.go:123  meterProvider := sdkmetric.NewMeterProvider()
examples/production_wiring/main.go:220  runtime.WithMeterProvider(meterProvider),
examples/production_wiring/main.go:264  stdlib.Mount(mux, svc, httpcore.WithMeterProvider[*http.ServeMux](meterProvider))
examples/production_wiring/main.go:274  stdlib.AdminRoutes{Svc: svc}.Customize(adminMux, httpcore.WithMeterProvider[*http.ServeMux](meterProvider))
```
`examples/sqlite_wiring/main.go:278` and `examples/mysql_wiring/main.go:262` are bare
`stdlib.Mount(mux, svc)` with no options and no `otel.SetMeterProvider` anywhere in the file.

**Consequence:** the only documented upgrade path for a **default-on breaking change** is unavailable
to the majority of consumers, and demonstrably unavailable in 2 of the library's own 3 reference
wirings. Combined with F4 (no observation on stdlib/gin when unbounded) and F5 (fiber truncates at
4 MiB even when unbounded), the procedure has no configuration in which it works end-to-end.

**Fix:** phase 4's `SECURITY.md` must carry a metrics-free procedure as the primary path — e.g.
*"start at a deliberately generous cap (8 MiB), watch your existing access-log/proxy 413 rate or
`wrkflw_rest_requests_total{http.status_code="413"}` if you have metrics, and tighten"* — with the
histogram-based procedure as the refinement for consumers who do have a `MeterProvider`. The ADR's
`Negative` list should state plainly that the histogram and the rejection counter are **no-ops
without a consumer-supplied `MeterProvider`**.

---
### F18 — On fiber the cap bounds the **compressed** size, so it does not bound what reaches the engine — and the prescribed compressed-body **parity** case cannot be written as described, because the three adapters do not agree on compressed requests at all
**Severity:** Critical
**Bundle says:** ADR §Decision — *"**`MaxBodyBytes` means WIRE bytes, in all three adapters.**"*, and
*"A second, separately-named check on the *decompressed* size is deliberately **out of scope**."*
Plan phase 3 — *"⚠ **A compressed-body parity case** asserting all three read **wire** bytes."*
§Consequences, Positive — *"The unbounded-body surface closes on all **39** sites with **one** policy
and **one** status."*

**The case it misses:** two things, and the second invalidates a prescribed test.

1. **Wire bytes are the wrong unit for the hazard on fiber.** `net/http` does not decompress request
   bodies, so on stdlib and gin wire bytes *are* the bytes the handler unmarshals and the engine
   stores. On fiber, `c.Bind().JSON` decompresses, so a 1 MiB **wire** cap admits a JSON document up
   to `fiber.Config.BodyLimit` (4 MiB by default) after expansion. Backlog 98 is "no request body
   cap" — a memory bound — and on one of the three adapters the shipped cap does not provide it. The
   ADR's out-of-scope note covers the *check*; it never states this *consequence*, and
   §Consequences' "one policy" sentence reads as though it does.
2. **The three adapters disagree on the outcome of a compressed request, so there is no parity to
   assert.** fiber accepts `Content-Encoding: gzip` and returns 2xx; stdlib and gin see opaque gzip
   bytes and return 400. A parity case written as the plan describes fails on **status**, before any
   byte-count assertion is reached.

**Evidence:** executed, `transport/http/fiber/zz_probe2_test.go` (created, run, deleted),
`go test -count=1 -v` EXIT=0. Same request (`{"a":7}` gzipped, 31 wire bytes,
`Content-Encoding: gzip`) against both:
```
raw=7 bytes, gzip wire=31 bytes
FIBER  status=200 body={"bindErr":"<nil>","in":{"a":7},"lenBody":7,"lenBodyRaw":31}
STDLIB read=31 unmarshalErr=invalid character '\x1f' looking for beginning of value in={A:0}
```
`lenBodyRaw=31` (wire) vs `lenBody=7` (decompressed) also confirms the ADR's `BodyRaw()` mechanism —
that part is right. What is wrong is what the bundle concludes from it.

**Consequence:** the phase-3 agent is handed a test that cannot pass, will discover the divergence
during implementation, and will either delete the case (losing the check entirely) or silently mark
it `noBodyParity` / fiber-only — a decision about the library's compressed-request contract taken by
an implementation agent with no design record. Meanwhile the "one policy" claim in the ADR's Positive
list is false for the property backlog 98 actually asks about, on the adapter where compression is
accepted.

**Fix:**
- Restate the wire-bytes decision honestly: *"the cap bounds wire bytes; on fiber, which accepts
  `Content-Encoding: gzip`, the decompressed size the engine sees is bounded only by
  `fiber.Config.BodyLimit`. On stdlib and gin the two coincide because `net/http` does not
  decompress."* Move the follow-up item from "the fiber decompressed-size bound" (a nice-to-have) to
  a named gap in §Consequences/Negative.
- Replace the parity case with **two** cases: a `noBodyParity`-style status-divergence case
  documenting `gzip → 2xx on fiber, 400 on stdlib/gin` (pre-existing, now written down), and a
  **fiber-only** case asserting the cap is measured on `BodyRaw()` — i.e. wire 2 MiB /
  decompressed-small → 413 at a 1 MiB cap, which is plan phase-2 test 6 row 2 and belongs there, not
  in parity.

---

### F19 — The 413's `ErrorBody.Error` code — the field clients actually switch on — is **never named**, and the message carries neither the limit nor the observed size
**Severity:** Major
**Bundle says:** ADR §Decision — *"a new **`httpcore.ErrRequestBodyTooLarge`** … `ClassifyError` maps
it → **413**"*, and *"the adapter returns the **bare** sentinel on the oversize path, and the **413
arm is placed before the 400 arm**"*. §Consequences, BREAKING — *"a **new 413 status** appears on
routes that previously returned 400, 500, or a spurious **2xx**. Clients with an exhaustive status
switch break."* Spec §4 refers in passing to *"the static `"request too large"`"*.

**The case it misses:** `ClassifyError` does not return a status — it returns
`(int, ErrorBody{Error string, Message string})`, and **`Error` is the machine-readable code**. Every
existing arm names it explicitly in source: `"not_found"`, `"forbidden"`, `"conflict"`,
`"bad_request"`, `"conflict_state"`, `"internal_error"`. The new arm needs a seventh, it is a
permanent wire contract, and **no bundle document names it**. Nor does any document give the sentinel's
`.Error()` text, which — following every other 4xx arm's `Message: err.Error()` — becomes the
client-visible `message`.

Separately: a 413 that says only "too large" gives the client no way to self-correct. It carries
neither the configured limit nor the observed size, and — per the ADR's own stated gap — no
correlation id and no log line. The observed size is *available for free* under read-before-parse
(the read knows it; spec §2 says so: *"Under read-before-parse the read itself knows the size"*), and
the limit is `cfg.MaxBodyBytes`.

**Evidence:** source at the anchor, `transport/http/httpcore/errors.go` — six arms, each with a
literal `Error:` string, and `ErrorBody` declared as
`{Error string \`json:"error"\`; Message string \`json:"message,omitempty"\`}`. Executed grep over the
bundle: neither `payload_too_large`, `request_entity_too_large`, nor any candidate code string
appears in any of the five files.

**Consequence:** the phase-1 agent invents the code string. It is then frozen into the wire contract
of every consumer, and `CHANGELOG`/`STABILITY` (phase 4) document a status change without documenting
the new code — so a client that switches on `body.error` rather than on the HTTP status (the shape
the envelope is designed to encourage) sees an unannounced new value.

**Fix:** name it in the ADR — recommend `Error: "request_too_large"` (snake_case, consistent with the
six existing codes) and
`var ErrRequestBodyTooLarge = errors.New("workflow-httpcore: request body too large")` (matching the
repo's `workflow-<pkg>: …` sentinel convention). Add the code string to the phase-4
`CHANGELOG`/`STABILITY` bullet as a **third** breaking item alongside the status and the trailing-byte
change. Then decide explicitly whether the message includes the limit — if the answer is "not until
the deferred 4xx delivery", say so in §Consequences, because "the read knows the size" is stated in
the spec and a reader will assume it is used.

---

### F20 — Phase 3 asserts the three adapters *"agree on **413**"* — a **status** claim — while the delivery's real cross-adapter risk is the **envelope**, which `assertParity` is the only thing that checks
**Severity:** Minor
**Bundle says:** Plan phase 3 — *"All three adapters agree on **413** for the body shapes in phase 2
test 2, and on the **unbounded** behaviour in test 4."* and separately *"⚠ **Check that
`TestParity_ErrorEnvelopes`'s existing byte-for-byte guarantee still holds and say so** — this
delivery adds no correlation id."*

**The case it misses:** the two sentences treat the byte-for-byte guarantee as something to *preserve*
rather than something to *extend to the new status*. `assertParity` compares the normalised JSON
body across adapters only when `bodyParity` is set, and the existing suite already carries one case
marked `noBodyParity: true` because *"fiber adds `bind from body: ` prefix"*. The 413 path is where
that hazard recurs: if any adapter wraps the sentinel (`fmt.Errorf("%w: …", ErrRequestBodyTooLarge)`)
instead of returning it bare, the status stays 413 in all three and only the `message` diverges — and
a phase-3 case that asserts "agree on 413" passes.

**Evidence:** source at the anchor, `transport/http/parity/parity_test.go`: `assertParity` compares
`s.status/g.status/f.status` unconditionally and `normJSON(...)` only `if bodyParity`;
`TestParity_ErrorEnvelopes` case *"400 missing def_ref"* carries `noBodyParity: true` with the comment
*"which each adapter wraps differently (fiber adds "bind from body: " prefix)"*. Reasoned from that
source — not executed.

**Consequence:** the ADR's instruction *"the adapter returns the **bare** sentinel"* — the single
sentence protecting both the 413 classification (a wrapped `ErrBadInput` would ship 400) and the
envelope parity — has no test that fails when one of three parallel agents wraps it.

**Fix:** add the 413 case **into `TestParity_ErrorEnvelopes`** with `noBodyParity` left false, so the
envelope is compared byte-for-byte across the three adapters. **Falsifier:** it fails against any
adapter that wraps `ErrRequestBodyTooLarge` before classification.

---

## Checked and clear (stated so the next round does not re-derive them)

- **No existing test in `transport/http/{httpcore,stdlib,gin,fiber,parity}` posts a body anywhere
  near 1 MiB.** Executed: `grep -rn "Repeat|1 <<|<< 20|<< 10" transport/http/*/[a-z]*_test.go`
  returns nothing. The 1 MiB default breaks no existing test.
- **No `examples/` file constructs a `httpcore.CustomizeConfig` struct literal** — all three use
  option functions (`stdlib.Mount(mux, svc)` ×2, `httpcore.WithMeterProvider[…]` ×2). Adding a field
  to the struct requires no `examples/` edit. (Plan §0 item 2 asks this; the answer is no.)
- **`c.BodyRaw()` really is the wire body and `c.Body()` really is decompressed**, and `Body()`
  restores the original via `request.SetBodyRaw(originalBody)` (`fiber/v3@v3.4.0/req.go:180-181`), so
  a middleware calling `Body()` ahead of the route group does **not** poison a later `BodyRaw()`.
  Executed: `lenBodyRaw=31, lenBody=7` for a gzipped `{"a":7}`. The ADR's mechanism claim holds.
- **`ClassifyError`'s arms are genuinely order-dependent and 400 genuinely precedes 422**, as the ADR
  states. Read at the anchor, `transport/http/httpcore/errors.go`.
- **All three adapters route every handler through `Instrumentation.Observe` and report the written
  status**, so a 413 *does* land in `wrkflw_rest_requests_total{http.status_code="413"}` on the
  in-handler path. (This is what makes F12's "nothing else" wrong, and what F11 shows is lost above
  fiber's `BodyLimit`.)

---

## Summary

**20 findings — 9 Critical, 10 Major, 1 Minor** (plus 5 checked-and-clear items).
Counts re-derived mechanically, not from memory:
`grep -c "^### F" …` → 20; `grep -A1 "^### F" … | grep Severity | sort | uniq -c` →
9 Critical / 10 Major / 1 Minor.

The shape of this round matches the brief's warning exactly: **the reasoning inside each declared
boundary is sound, and almost every Critical sits one step outside a boundary the bundle drew.**
- "the body read" is a site that exists only on the capped path → **F4**;
- "unbounded" is a claim about wrkflw's cap, not about the request → **F5**;
- "at mount" is a moment with no error channel → **F6**;
- "not in httpcore" conflates the recording site with the instrument-declaration site → **F7**;
- "`transport/`" excluded `action/httpcall`, which already implements this cap with the opposite
  sentinel convention → **F15**;
- "a mounted `fiber.Router`" excluded `Ctx.App()`, where the real limit is → **F13**;
- "wire bytes" is the right unit on two adapters and the wrong one on the third → **F18**.

And one shape the brief did not predict: **the three sentences that specify the mechanism at the
three discarding sites (F2), the read-error taxonomy (F9) and the unmarshal call (F3) are each a
compression of a correct analysis into a summary that is false.** All three were caught by execution,
none by reading.

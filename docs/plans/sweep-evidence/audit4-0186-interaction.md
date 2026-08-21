# Audit 4 — ADR-0186 (request body caps) — INTERACTION lens

- Bundle commit: `85d6bb68` (detached worktree). Step 0: all **five** bundle files present. ✅
- Lens question: *what does this decision assume someone else will hand it, and who agreed to that?*
- Grid under attack: spec §4, the **survivor × removed** table (1 survivor × 5 removals = 5 pairs).
- ⚠ Note before the findings: the bundle commit's own message reads *"re-cut ADR-0186 into four
  deliveries — this one is **three decisions**"*, while all five files say **one** decision and six
  deliveries. The commit message is stale by one re-cut. (Bookkeeping; folded into I13.)

---

### I1 — "negative `MaxBodyBytes` is a construction error at mount" has NO return channel, and the four deferred deliveries inherit whatever signature it invents
**Severity:** Critical

**This bundle says / assumes:** ADR Decision, *"**negative → a construction error**, surfaced at mount
time rather than per request."* Plan §2 maps that sentence to **phase 1 / `httpcore`**. Plan §3
phase 1 test 3: `TestNegativeMaxBodyBytesIsRefusedAtMount` — *"the construction error is
reachable."* ADR Consequences/Positive: *"no new cross-package mechanism and **no new exported
interface**."*

**The other side says / will do:** the code has no error channel at mount, executed:
```
transport/http/stdlib/mount.go:17: func Mount(mux *http.ServeMux, svc service.Service, opts ...httpcore.CustomizeOption[*http.ServeMux]) {
transport/http/gin/mount.go:14:    func Mount(r ginlib.IRouter, svc service.Service, opts ...httpcore.CustomizeOption[ginlib.IRouter]) {
transport/http/fiber/mount.go:15:  func Mount(r fiberlib.Router, svc service.Service, opts ...httpcore.CustomizeOption[fiberlib.Router]) {
```
All three return **nothing**. Neither does the exported seam interface:
```
transport/http/httpcore/seam.go:100  type RouteCustomizer[R any] interface { Customize(r R, opts ...CustomizeOption[R]) }
transport/http/httpcore/seam.go:105  // It is also the consumer extension seam: any RouteCustomizer[R] — including a consumer's own
transport/http/httpcore/seam.go:39   func ResolveConfig[R any](opts ...CustomizeOption[R]) CustomizeConfig[R]   // no error
```

**The collision:** the sentence is unimplementable as written without changing one of three exported
signatures, and each choice hands an unagreed constraint to somebody:
- change `ResolveConfig` → `(CustomizeConfig[R], error)`: breaks **15 non-test call sites** (5 per
  adapter — `ResolveConfig` is called once per *route group*, not once per mount) and is inherited by
  §READ-PATH, §4XX and §BOUND, all three of which add fields to the same struct;
- change `Mount` → `error`: source-breaking for every consumer;
- change `RouteCustomizer.Customize` → `error`: source-breaking for every **consumer-authored route
  group**, which `seam.go:105` documents as a supported extension point;
- or panic / log-and-default — i.e. *not* a construction error, and the ADR sentence is false.

Nobody agreed to any of these. The ADR asserts the opposite (*"no new exported interface"*), and
plan phase 4's CHANGELOG entry enumerates **two** breaks, both **wire**-level (a new 413; requests
that succeed today via the trailing-byte gap). A **source**-level break of the library's public
mount API is absent from that enumeration — and for a library-first product (CLAUDE.md) a source
break is the more expensive one.

**Evidence:** greps above, executed at the bundle commit. The signature-choice consequences are
**reasoned — not executed** (no implementation exists), except I2 below.

**Fix:** the ADR must **name the mechanism**, not the outcome. Recommended, because it needs no
signature change and no consumer break: **treat a negative value as a config error surfaced through
the already-present `cfg.Logger` at `ERROR` and fail closed to the 1 MiB default** — the same
fail-closed rule the tri-state already uses for `nil`. If the owner instead wants a hard construction
error, that is a **separate decision** with its own break entry, and the plan must move the row out of
phase 1 (`httpcore` holds no mount function) into phase 2 and add the CHANGELOG/STABILITY break.

---

### I2 — the plan's only guard for that break, `go build ./examples/...`, provably CANNOT fail for it
**Severity:** Major

**This bundle says / assumes:** plan §1, *"⚠ `go build ./examples/...` runs at the end of phase 2,
where the adapters change"*; plan §3 phase 2, *"**Then, once all three land:** `go build
./examples/...`"*; checklist §5, *"`go build ./examples/...` — at the end of phase 2 and again here."*

**The other side says / will do:** Go permits discarding a call's return values in statement
position. All three example call sites are statements:
```
examples/production_wiring/main.go:264  stdlib.Mount(mux, svc, httpcore.WithMeterProvider[*http.ServeMux](meterProvider))
examples/sqlite_wiring/main.go:278      stdlib.Mount(mux, svc)
examples/mysql_wiring/main.go:262       stdlib.Mount(mux, svc)
```

**The collision:** phase 2's gate is blind to exactly the change phase 1 would make. The break
surfaces only at the **repo-wide `golangci-lint` at the very end of the checklist**, after all four
phases — and never at all for a downstream consumer, who is the actual victim.

**Evidence — EXECUTED** (mutation in this worktree; `cp` backup, restored, `git status --porcelain`
clean afterwards). Added an `error` return to `stdlib.Mount`:
```
$ go build ./examples/...
EXIT=0                                  ← the prescribed guard passes
$ golangci-lint run ./examples/...
EXIT=1
examples/mysql_wiring/main.go:262:14: Error return value of `stdlib.Mount` is not checked (errcheck)
examples/production_wiring/main.go:264:14: ... (errcheck)
examples/sqlite_wiring/main.go:278:14: ... (errcheck)
3 issues: * errcheck: 3
```
(`errcheck` is live — `.golangci.yml:10` `default: standard`.)

**Fix:** if I1 resolves toward any signature change, replace the phase-2 gate with
`golangci-lint run ./examples/... ./transport/...` and state the falsifier: *"it fails against a
signature change whose return value the examples discard."* State plainly in the plan that
`go build` cannot detect a widened return.

---

### I3 — the new histogram and rejection counter have NO home: `httpcore` is forbidden by the ADR and owns the only instrument constructor
**Severity:** Critical

**This bundle says / assumes:** ADR Decision, *"`wrkflw_rest_request_body_bytes` is recorded **in each
adapter**, at the body read. ⚠ **Not in `httpcore`** — that package has **0** decode sites and never
sees a body."* Plan §2 decision→phase map: *"`wrkflw_rest_request_body_bytes` histogram + rejection
counter | **2** | `stdlib` \| `gin` \| `fiber`"* — **no `httpcore` row**.

**The other side says / will do:** `transport/http/httpcore/observability.go`:
```go
type Instrumentation struct {            // :23 — ALL FOUR FIELDS UNEXPORTED
    tracer trace.Tracer; counter metric.Int64Counter
    histogram metric.Float64Histogram; propagator propagation.TextMapPropagator
}
func NewInstrumentation[R any](cfg CustomizeConfig[R]) *Instrumentation   // :40 — the ONLY constructor
func (i *Instrumentation) Observe(ctx, method, routeTemplate string, hdr http.Header,
        run func(context.Context) (status int))                          // :80 — callback returns ONLY status
```
`NewInstrumentation` has **15 non-test call sites**, five in each adapter's `groups.go`.

**The collision:** the ADR conflates *where a value is observed* with *where the instrument is
declared*. The adapters cannot record anything: the fields are unexported, the constructor is in
`httpcore`, and `Observe`'s callback signature carries **no channel for a body size** back out. Every
implementation therefore requires a **new exported `httpcore` API** — a `RecordBodyBytes` method, or a
widened `Observe` callback (which would touch every route in all three adapters) — and phase 1, the
only `httpcore` phase, is not told to create it. Phase 2's **three parallel agents** would each
discover they need the same new `httpcore` symbol; per the plan's own fan-out rule they may not all
edit `httpcore`, and per the phase table `httpcore` is already done.

This is round 5's *"an entire package that produced four sentinels and appeared in no list"*
repeating, one round later, with the package the plan **explicitly excluded by name**. It is also a
straight violation of the plan's own §2 preamble: *"A row with no phase is a defect."* Here the row
has a phase and the **wrong package**, which the preamble does not check for.

The only alternative — each adapter calling `observability.New(...)` itself — forks the
`instrumentationScope` constant (`observability.go:18`) three ways and breaks the documented promise
that *"consumers migrating between the two transports see identical telemetry"*.

**Evidence:** source above + `grep -c` of `NewInstrumentation` call sites, executed at the bundle
commit. That no exported recorder exists is read from the file, not executed — the type has one
exported method and it is `Observe`.

**Fix:** add an explicit **phase 1** row: *"`httpcore.Instrumentation` gains
`wrkflw_rest_request_body_bytes` (Float64Histogram/Int64Histogram) and
`wrkflw_rest_request_body_rejected_total`, plus an exported `RecordBody(ctx, route string, n int64,
rejected bool)`."* Then rewrite the ADR sentence to *"the value is **observed** in each adapter at the
body read and **recorded** through `httpcore.Instrumentation`, which owns the meter"* — the current
sentence is what created the gap. Add the new symbols to phase 1's test list.

---

### I4 — spec §4's D1×BOUND row cites the ADR for two records the ADR does not contain
**Severity:** Critical

**This bundle says / assumes:** spec §4, row *D1 × the variable-map bound*, resolution column:
> ⚠ **Stated, not resolved here** — one-way, out of this delivery. **The ADR records** that the
> message must become **per-sentinel** when the arm gains a second producer, and that **the two
> notions of "bytes" must not be conflated**.

**The other side says / will do:** the ADR records neither.
```
$ grep -n "per-sentinel\|notion\|decoded-map\|conflat" docs/adr/0186-untrusted-input-and-disclosure-posture.md
(no output)   exit=1
```
The ADR's only forward-looking sentence on the arm is about **ordering**:
> *"Any future arm — the deferred 4xx delivery's, the deferred variable bound's
> `ErrVariablesTooLarge`, ADR-0185's 401 and 503 — must state its position and carry a test asserting
> that an error matching two arms resolves to the intended one."*

Ordering ≠ message text, and ordering ≠ wire-bytes vs decoded-map-bytes.

**The collision:** the row is marked *"stated, not resolved"*, which the brief asks me to judge as
honest or evasive. It is **neither — it is inaccurate**. The honesty claim rests on the record living
in the ADR (the durable artifact that ships in the commit and outlives the bundle). It lives only in
the carry-forward record, which self-describes as *"**not a bundle**"* and carries
*"⚠ **Do not implement anything in this file.**"* — i.e. the one place a future implementer is told
not to act on. This is precisely the round-4 shape the brief names: a claim that is true about the
*intent* and false about the *artifact*, in a celebratory-adjacent sentence ("stated, not resolved"
being the bundle's own proof of diligence).

**Evidence — EXECUTED:** grep above, exit 1.

**Fix:** add both sentences to the ADR body, adjacent to the existing ordering paragraph:
> ⚠ **The 413 arm's message is static only while the arm has one producer.** When the deferred
> variable-map bound adds `service.ErrVariablesTooLarge`, the message must become per-sentinel — a
> 109 KiB body refused over an *element* bound is not "request too large".
> ⚠ **`MaxBodyBytes` counts WIRE bytes. The deferred bound counts a DECODED MAP.** They are not the
> same quantity and must never be described by one option, one metric, or one message.

Then downgrade §4's cell from *"The ADR records…"* to a citation of the ADR line that now says it.

---

### I5 — the single stated cross-slice dependency names the WRONG SLICE, in the one paragraph written to stop it being rediscovered
**Severity:** Critical

**This bundle says / assumes:** carry-forward record, the section titled *"The one cross-slice
dependency, stated so it is not rediscovered"* (`…-deferred-slices.md:61-66`):
> **D2 mints `service.ErrVariablesTooLarge` and D5 routes it to 413.** Slice 1 ships the 413 arm with
> **one** sentinel … ; **slice 4 adds the second sentinel to the existing arm**. The dependency runs
> **slice 1 → slice 4** and never back.

**The other side says / will do:** the same file's own slice table, 15 lines above (`:44-51`):

| slice | decision | record |
|---|---|---|
| 4 | the instance read path aliases and discloses | ADR-0189 |
| **6** | **variable-map admission bound** | **ADR-0191** |

The variable-map bound is **slice 6**. **Slice 4 is §READ-PATH, which mints no sentinel and touches
no arm.**

**The collision:** spec §4's D1×BOUND row — the row I4 already found to be citing a non-existent ADR
passage — delegates its "one-way, out of this delivery" claim to this paragraph. A future author
following the pointer lands on §READ-PATH, finds nothing about 413, and concludes the dependency was
already discharged. The paragraph exists *specifically* so the dependency is not lost, and it
misroutes the reader. Compounding it, `D2` and `D5` are **dead labels from the six-decision era**
with no gloss — CLAUDE.md rule 13 — and in the *current* numbering `D2`/`D5` mean nothing at all.

**Evidence — EXECUTED:** `grep -n "slice 4\|D2 mints\|D5 routes"` and the slice table, both quoted
above from the same file.

**Fix:** rewrite as: *"**§BOUND (slice 6, reserved ADR-0191) mints `service.ErrVariablesTooLarge` and
must route it to the 413 arm this slice creates.** The dependency runs **slice 1 → slice 6** and never
back."* Delete `D2`/`D5`. Then add the corresponding item to §BOUND's *"What §BOUND's own bundle must
decide"* list, which currently has five items and mentions the 413 arm in none of them.

---

### I6 — MISSING PAIR: D1 × §BOUND has a SECOND coupling, and the ADR's headline Positive is false because of it
**Severity:** Critical

**This bundle says / assumes:** ADR Consequences/Positive, first bullet — the bundle's headline
celebratory sentence:
> *"**The unbounded-body surface closes** on all **39** sites with **one** policy and **one** status."*

Spec §4 derives **one** coupling for D1 × the variable bound (the sentinel and the arm).

**The other side says / will do:** the removed §BOUND slice, *executed* and quoted in the
carry-forward record (`:256-258`):
> *"**Per-request is not per-caller** (I-10 + F7, two lenses, executed). **5 admitted signal
> deliveries reach 49,995 elements / 789 KiB**, ≈61 s per evaluation, with no wall-clock backstop on
> the gateway path."*

**The collision:** every one of those five deliveries is **individually under the 1 MiB cap** — 789
KiB total across five requests. A per-request **wire-byte** cap does not bound **per-instance
accumulation**, and the delivery that did bound it was removed from this bundle. So the surface D1
closes is *"the per-request decode surface"*, not *"the unbounded-body surface"*. The quantifier is a
scope over-reach of exactly the kind the spec's own §0 lesson 2 warns about — *"the failure was the
grep's NET … generalises from enumerations to SCOPES"* — committed in the bundle's own victory
sentence, about a scope the bundle itself deferred 30 lines earlier in spec §3 (*"Out … 99
(variable-map admission bound)"*).

This is the pair the grid's own construction should have produced: it derived what §BOUND **hands
D1** (a second sentinel) and never asked what §BOUND's **removal takes away from D1** (the only bound
on accumulation). The at-rest row asks exactly that question — *"a reader composes 'capped' with 'at
rest' into 'protected'"* — and the bound row does not.

**Evidence:** the 789 KiB / 49,995-element measurement is **inherited** from the previous round's
audit as recorded in the carry-forward record; I did **not** re-execute it (it needs an
`expr` benchmark harness). The arithmetic that 789 KiB / 5 ≈ 158 KiB < 1 MiB is mine and trivially
checkable. **Reasoned — not executed.**

**Fix:** (a) narrow the Positive bullet to *"**the per-request decode surface** closes on all 39
sites…"*; (b) add a Negative bullet: *"⚠ **A per-request cap does not bound per-instance
accumulation.** Executed by §BOUND: five individually-compliant requests reach 789 KiB / 49,995
elements in one instance. Bounding that is the deferred variable-map admission delivery (backlog 99);
until it lands, `MaxBodyBytes` bounds a single request and nothing else."*; (c) add the pair as a
second coupling in §4's D1×BOUND row.

---

### I7 — MISSING PAIR: D1 mints three NEW 4xx-producing sites, and the delivery that owns "what a 4xx body may say" was removed — including a two-way rule for a three-way space
**Severity:** Critical

**This bundle says / assumes:** ADR Decision, on the three discarding sites:
> *"They must **gain** a path distinguishing *body absent / EOF* (keep ignoring — genuinely optional)
> from *body present but oversize*. Under the read-before-parse rule this is simply the read's own
> error."*

And spec §3 *Out*: *"The correlation id, per-class 4xx logging, and **any change to what a 4xx message
says**. All belong to the deferred 4xx delivery."*

**The other side says / will do:** the removed §4XX slice's carried-forward **core insight**, which
the record marks *"⭐⭐ sound and executed — carry it forward"*:
> **"Value-freedom is a property of the PRODUCING SITE and the types it renders — never of the
> sentinel."**

**The collision, two parts.**

**(a) Three new producing sites, none analysed.** D1 creates a new error-producing site in each
adapter (the body read) and a new sentinel, and performs none of the producer-side value-freedom
analysis §4XX establishes as mandatory. The bundle reasons only about the *sentinel* (*"the adapter
returns the **bare** sentinel"*) — the exact fallacy §4XX retired.

**(b) The instruction is a two-way split of a three-way space.** `io.ReadAll` on a request body
returns more than {EOF, `*http.MaxBytesError`}: a truncated body, malformed chunked encoding, a
client reset, or a read deadline all produce a third class. Today all three sites discard **every**
decode error (`_ = json.NewDecoder(req.Body).Decode(&in) // body is optional`,
`stdlib/groups.go:238`; `gin:265`; `fiber:255`) and return 2xx. Under the new rule the implementer
must choose, and the ADR does not:
- *"anything not EOF → error"* ⇒ a transient read failure now returns 4xx/5xx on a route that returns
  2xx today. That is a **third behaviour break**, absent from phase 4's CHANGELOG (which lists two),
  and its message is rendered by `writeErr` → `ClassifyError` → `err.Error()` — a `*net.OpError`
  string carrying **local and remote IP:port**, on a 400/500 path, which is precisely backlog 104's
  harm and precisely the delivery that was removed;
- *"anything not `*http.MaxBytesError` → ignore"* ⇒ silently correct, but then the ADR's
  *"distinguishing absent/EOF from oversize"* wording is wrong and an implementer reading it
  literally picks the first branch.

**Evidence — EXECUTED** for the current discard sites (repo-wide grep of the three idioms, non-test:
13 `json.NewDecoder` / 13 `ShouldBindJSON` / 13 `c.Bind().JSON` = 39, all in `groups.go`; the three
`_ =` sites at the stated lines). **EXECUTED** for the 5xx-only logging: `stdlib/write.go:30-35` is
```go
status, body := httpcore.ClassifyError(err)
if status >= 500 { cfg.Logger.ErrorContext(r.Context(), "rest: internal error", "err", err) }
```
so the ADR's *"a 413 … produces no log record"* is **correct** — but so is *"a spurious 400 from a
read error produces no log record either"*. The `*net.OpError` disclosure path is **reasoned — not
executed**.

**Fix:** make the rule three-way and explicit in the ADR:
> *"At the three optional-body sites the read's error is classified three ways: **`io.EOF` /
> `ErrUnexpectedEOF` on an empty body → ignore** (genuinely optional); **`*http.MaxBytesError` →
> bare `ErrRequestBodyTooLarge` → 413**; **any other read error → ignore, exactly as today**, because
> changing it would break a route that returns 2xx and would render a transport error string the
> deferred 4xx delivery has not yet made safe."*

Add a phase-2 test row per adapter: *a read that fails for a non-oversize reason still returns 2xx on
the optional-body admin route.* Falsifier: *it fails against an implementation that treats "not EOF"
as an error.* And add to §4XX's *"must decide"* list: *"the three adapter body-read sites are new 4xx
producers; classify their value-freedom."*

---

### I8 — spec §4 row 1(iii)'s ✅ answers a different question than the coupling it states, and its stated inheritance channel does not exist
**Severity:** Major

**This bundle says / assumes:** spec §4, D1 × the 4xx policy, coupling (iii) and its verdict:
> (iii) *"When the 4xx delivery lands, its **deny-by-default rendering must not blank** the 413's
> static message."* → **"✅ (iii) the 4xx delivery inherits the arm-ordering invariant."**

**The other side says / will do:** the coupling is about **blanking** (which messages render); the
resolution cites **ordering** (which arm matches first). They are independent properties of
`ClassifyError` — an arm can be first in the switch and still render `ErrorBody{Message: ""}`. The
cited invariant cannot discharge the stated risk.

And the channel is empty. §4XX's *"What §4XX's own bundle must decide"* list has **seven** items
(packages producing 4xx sentinels; 404/409/422 deny-by-default; how a producer vouches; precedence;
rendering from typed fields; the §READ-PATH dependency; the logging policy). **None mentions 413,
`ErrRequestBodyTooLarge`, or preserving a static message.**
```
$ grep -n "413\|RequestBodyTooLarge" docs/specs/2026-08-21-untrusted-input-deferred-slices.md
63,64,69,71  ← all four hits are in the cross-slice-dependency paragraph about the BOUND slice
248          ← inside §BOUND
(zero hits inside §4XX)
```

**The collision:** a ✅ whose fix appears in no phase and no successor document — the shape the brief
names as *"six of one round's fifteen Criticals"*. When §4XX lands and flips 4xx rendering to
deny-by-default, `ErrRequestBodyTooLarge` is a sentinel §4XX has never heard of, and the default
denies. The 413 goes out with an empty message and this bundle's parity test
(`TestOversizedBodyReturns413`, and `TestParity_ErrorEnvelopes` at `parity_test.go:560`) is the only
thing that would catch it — in a delivery whose plan does not know to run it.

**Evidence — EXECUTED:** grep above; §4XX's seven-item list read in full.

**Fix:** add an eighth item to §4XX's *"must decide"* list: *"**`httpcore.ErrRequestBodyTooLarge` →
413 is an existing arm with a deliberately static, value-free message (ADR-0186). Deny-by-default
must vouch it explicitly, and the vouch must be pinned by a test in this delivery.**"* Change §4's
(iii) verdict from ✅ to ⚠ until that item exists.

---

### I9 — CELEBRATORY SENTENCE: the migration procedure requires a measurement that this revision's OWN sibling fix made unobtainable on two of three adapters
**Severity:** Major

**This bundle says / assumes:** ADR Decision, *Instrumentation, and the honest migration story*:
> *"⚠⚠ **The histogram is truncated at the cap** … The procedure is therefore: **run with
> `MaxBodyBytes` explicitly `0`, observe the distribution, then choose a cap.** ⚠ **That is only safe
> because the opt-out is now a real opt-out**; under the previous design this instruction bricked
> every route."*

**The other side says / will do:** two sentences earlier in the *same Decision section*, from a
*different* fix in the same revision:
> *"⚠ **When unbounded, stdlib and gin keep streaming into the decoder** rather than buffering — an
> unbounded `io.ReadAll` is itself a memory-exhaustion primitive."*

and, three bullets down:
> *"`wrkflw_rest_request_body_bytes` is recorded in each adapter, **at the body read**. … ⚠ **And not
> at `json.Decoder`**, which measures what it *consumed*, not what arrived."*

**The collision:** in the **`MaxBodyBytes = 0` configuration — the exact one the migration procedure
mandates — stdlib and gin perform no buffering read.** There is no "body read" to observe. The only
remaining measurement point is `json.Decoder`, which the ADR forbids by name for being the same
defect one layer over. Fiber is unaffected (`len(c.BodyRaw())` works in both configurations, since
fasthttp always buffers), so the prescribed procedure yields a distribution on **one adapter out of
three** — a fourth adapter divergence, in the delivery whose headline is *"one policy"*.

This is the round-4 shape verbatim: a sentence **true when written** against the previous design,
falsified by a **sibling fix in the same commit**, and phrased as a celebration (*"that is only safe
because the opt-out is now a real opt-out"*). Execution cannot catch it — a probe of the capped path
passes, and a probe of the unbounded path passes; only composing the two with the migration procedure
fails.

Corroborating gap: the plan's §2 map gives the histogram one row (phase 2, all three adapters) and
its phase-2 test list has **no** histogram test at all — capped or unbounded — so nothing would
detect the hole.

**Evidence:** the three ADR passages, quoted at the bundle commit. That `io.ReadAll` is skipped when
unbounded is the ADR's own prescription, not an inference. **Reasoned — not executed** (no
implementation exists to probe).

**Fix:** pick one and say it in the ADR.
- **Preferred:** measure from `Content-Length` when present and fall back to a counting reader
  (`io.TeeReader` into a byte counter, or a 20-line counting `io.Reader` wrapper) in the unbounded
  path. A counting wrapper streams — it is not a buffering primitive — so it costs nothing the
  Negative bullet already accepts, and it makes the procedure work on all three adapters.
- **Or:** delete the migration procedure and state honestly that the distribution is observable only
  on fiber, and that operators must size the cap from their own edge proxy's metrics.

Either way add a phase-2 test: *the body-bytes histogram records a sample when `MaxBodyBytes` is
explicitly `0`.* Falsifier: *it fails against an implementation that observes only inside the
`MaxBytesReader` branch* — which is every implementation the current ADR text describes.

---

### I10 — MISSING PAIR: D1 × §AT-REST — the removed slice carried the "discover, never hardcode a scope" lesson, and D1's scope is hardcoded
**Severity:** Major

**This bundle says / assumes:** spec §4, D1 × at-rest posture: *"⚠ **Nothing to resolve in this
delivery** — with the at-rest posture removed, this bundle makes **no** at-rest claim at all."* The
row treats the removal as content-only.

**The other side says / will do:** §AT-REST's refuted item 1 (`…-deferred-slices.md:353-359`):
> *"**The enumeration walks three migration files; there is a FOURTH** … ⇒ **Discover migrations
> (`**/migrations/*.sql`); never hardcode a directory list.**"*

and the bundle's own plan §4 closing warning:
> *"⚠⚠⚠ **Round 5 extended it to SCOPES** … **Assume one boundary here is still wrong.**"*

**The collision:** the removal took the *content* and left the *lesson* stranded. D1's entire
enumeration is scoped to a hardcoded set — three packages × one filename — and nothing in the bundle
applies the discovery rule to it. The plan warns that a boundary is probably wrong and then prescribes
no mechanism that could find out; the mechanism the lineage already agreed on (*"a `go/parser` walk;
pattern `engine/terminal_sites_test.go`"*, cited twice in the carry-forward record at `:141` and in
memory) is prescribed for **§READ-PATH** and for **§BOUND** but **not for D1**, whose count is the one
being shipped now.

I re-derived the boundary and **it currently holds** — repo-wide, non-test, the three idioms produce
exactly 13 + 13 + 13 = 39 hits and every one is in a `groups.go`. The finding is not that the number
is wrong; it is that **nothing keeps it right**, in the one delivery whose correctness is "all N sites
are capped", and the plan itself predicts the boundary will rot.

**Evidence — EXECUTED:**
```
$ grep -rn --include='*.go' -E "json\.NewDecoder|ShouldBindJSON|c\.Bind\(\)\.JSON" . | grep -v _test
transport/http/fiber/groups.go   × 13   (:255 is the `_ =` discarder)
transport/http/stdlib/groups.go  × 13   (:238 is the `_ =` discarder)
transport/http/gin/groups.go     × 13   (:265 is the `_ =` discarder)
runtime/kernel/cursorcodec.go, definition/model/node_wire.go   ← not request bodies
```

**Fix:** add a phase-1 (or phase-3) invariant test, `TestEveryRequestDecodeSiteIsCapped`, as a
`go/parser` walk over `transport/http/{stdlib,gin,fiber}` **discovering** decode calls by AST rather
than by filename, asserting each is preceded by the cap. Falsifier: *it fails the moment a 40th decode
site is added, or an existing one moves out of `groups.go`.* This is the same machine-checked
invariant memory already records as the fix for eleven rotted enumerations — *"a prose warning cannot
make a prose number reliable"* — and this bundle currently ships the prose warning without it.

---

### I11 — the mount-time WARN and any mount-time refusal fire once per ROUTE GROUP, not once per mount; and `MountHealth` cannot receive the option at all
**Severity:** Major

**This bundle says / assumes:** ADR: *"**A WARN is logged when `MaxBodyBytes > fiber.DefaultBodyLimit`.**
⚠ **It must be logged from the function the documented mount path actually calls**"*; plan phase 2 test
7: *"`TestMountWarnsAboveDefaultBodyLimit` — asserted against a `slog` handler capturing records,
**through the documented mount entry point**."*

**The other side says / will do:** the documented mount path resolves config **per route group**:
```
transport/http/fiber/groups.go:26,97,128,223,469   cfg := httpcore.ResolveConfig(opts...)   (5 sites)
transport/http/stdlib/groups.go:36,108,135,206,466  (5)
transport/http/gin/groups.go:25,106,142,231,479     (5)
transport/http/fiber/mount.go:15-19  Mount → InstanceRoutes.Customize + TaskRoutes.Customize + MessageRoutes.Customize
transport/http/fiber/mount.go:23-25  MountHealth(r, checks ...httpcore.HealthCheck)   ← NO CustomizeOption parameter
```

**The collision:** three consequences the bundle does not state.
1. `Mount` emits the WARN **three times**; a consumer mounting admin routes too emits it **four**. The
   test's cardinality is unspecified — an assertion of exactly one record fails, an assertion of
   "at least one" passes vacuously against a three-times-per-mount log spam regression.
2. Same for I1's construction refusal: whatever mechanism is chosen fires 3–5× per mount.
3. **`MountHealth` accepts no `CustomizeOption`**, so `/healthz` and `/readyz` resolve
   `ResolveConfig()` with defaults and take the **1 MiB cap unconditionally** — a consumer who sets
   `MaxBodyBytes = 0` cannot opt those routes out. Harmless today (health routes hold none of the 39
   decode sites) but it is a config surface that silently ignores the new field, and the "26 routes =
   9 + 15 + **2 health**" enumeration in plan §4 is the very place it should have been noticed.

**Evidence — EXECUTED:** greps above at the bundle commit.

**Fix:** state in the ADR that the WARN is emitted **once per route group** and is therefore
idempotent-but-repeated; make plan test 7 assert `>= 1` **and** add a comment naming the expected
count for `Mount` (3). Add a sentence to phase 4's `SECURITY.md` item: *"`MountHealth` takes no
options; health routes are unaffected by `MaxBodyBytes` and decode no bodies."*

---

### I12 — the `httpcall.ErrBodyTooLarge` guard test is unfalsifiable and creates the bundle's first `transport/` → `action/` import edge
**Severity:** Major

**This bundle says / assumes:** ADR: *"a test asserts `httpcall.ErrBodyTooLarge` still classifies
**500**."* Plan §2 gives it a row (phase 1, `httpcore`); plan phase 1 test 1 lists it as
*"⚠ **Plus a row asserting `httpcall.ErrBodyTooLarge` still classifies 500.**"* — with **no
falsifier**, in a plan whose §5 checklist requires *"Every prescribed **falsifier** demonstrated by
mutation"* and whose §3 states one for every other test.

**The other side says / will do:** `action/httpcall.ErrBodyTooLarge` is `errors.New(...)` matched by
**no arm** in `ClassifyError` (`httpcore/errors.go:26-59`, six arms, verified). It reaches the
`default:` 500. It will still reach `default:` after a 413 arm keyed on
`errors.Is(err, ErrRequestBodyTooLarge)` is inserted, because the two sentinels are unrelated values.
**No implementation of this ADR can make that test fail.** It is a test that cannot fail — this
repo's signature defect (six in one delivery, three more caught in one audit).

And the edge is new:
```
$ grep -rn --include='*.go' "action/httpcall" transport/
(no output)   exit=1
```
`transport/` imports `action/httpcall` **nowhere**, test or not. The prescribed test creates the first
one — against the ADR's *"no new cross-package mechanism"* and the spec §0 claim *"no new
cross-package contract"*.

**The collision (my lens):** the test is offered as the resolution of §4's D1 × §SSRF row (*"✅
Renamed `ErrRequestBodyTooLarge`; a test asserts `httpcall.ErrBodyTooLarge` still classifies 500"*).
A ✅ discharged by a test that cannot fail is not a resolution. The actual risk the row names — *"minting
a same-named sentinel in one commit across two packages is a support hazard"* — is discharged by the
**rename**, which is real, and not by the test, which is theatre.

**Evidence — EXECUTED:** the import grep (exit 1) and `errors.go`'s six arms, read in full at the
bundle commit. That the test cannot fail is **reasoned from the source** — `ClassifyError` matches by
`errors.Is` on named sentinels only, so an unrelated sentinel always falls through.

**Fix:** either (a) drop the test, keep the rename, and change §4's row-3 ✅ to cite the rename alone —
the naming hazard is a human one and a test cannot address it; or (b) keep it and state a real
falsifier: *"it fails against a 413 arm implemented as `errors.As(err, new(*http.MaxBytesError)) ||
strings.Contains(err.Error(), "too large")`"* — a message-substring or type-shaped arm is the only
implementation it discriminates, and the plan should say so. If (b), add the import edge to the ADR's
Consequences instead of claiming there is none.

---

### I13 — MISSING PAIR: D1 × §SSRF — the two first-party byte defaults are called "different things", but they compose into one ingress path
**Severity:** Major

**This bundle says / assumes:** spec §4, D1 × `httpcall` SSRF: *"⚠ And the two first-party defaults sit
**10× apart** in opposite directions."* → *"⚠ The 10× divergence is documented, **not reconciled —
they bound different things**."* The ADR repeats it as a Negative: *"1 MiB remains a judgement call …
One datapoint sits in the other direction: first-party `action/httpcall` caps an outbound *response*
at 10 MiB."*

**The other side says / will do:** the removed §READ-PATH slice's verified finding
(`…-deferred-slices.md:101-103`): the instance read surface exposes *"**Five disclosure-bearing
fields** … `variables`, `tokens[].payload`, `incidents[].error`, `tasks[]`, and the whole embedded
`definition`"* — `variables` being where an `httpcall` response lands.

**The collision:** they do not bound different things once composed. A definition containing an
`httpcall` node ingests up to **10 MiB** of attacker-influenced bytes into process variables per
call, entirely bypassing the 1 MiB request cap — and those bytes are then served back on a
**non-admin** instance read route. The "10× apart in opposite directions" framing treats the two caps
as unrelated axes; they are the **same axis** (bytes entering an instance) measured at two doors, and
this delivery locks the small door while a first-party action holds the large one open. Both
deliveries that would have addressed it (§SSRF for the outbound leg, §BOUND for the admission leg)
were removed.

This is the same structural miss as I6 — the grid asks *what the removed slice hands D1* and never
*what D1's claim depends on that the removed slice was going to supply*.

**Evidence:** `action/httpcall/httpcall.go:94` `ErrBodyTooLarge` and its 10 MiB cap, and the five
disclosure-bearing fields, both read at the bundle commit. The composed path
(httpcall response → variables → instance view) is **reasoned — not executed**.

**Fix:** replace *"they bound different things"* with the honest version in both the ADR Negative and
§4's row: *"⚠ **They bound the same axis at two doors.** `MaxBodyBytes` (1 MiB) bounds bytes entering
through the HTTP request; `httpcall`'s 10 MiB bounds bytes entering through an outbound action
response, which land in process variables and are served back on a non-admin read route. A consumer
must not read the request cap as a bound on what an instance can hold. Reconciling the two defaults is
the deferred SSRF and variable-bound deliveries' work (backlog 65, 99)."*

---

### I14 — bookkeeping: the bundle commit message describes the previous re-cut
**Severity:** Minor

**This bundle says / assumes:** all five files say **one** decision, six deliveries.

**The other side says / will do:** `git log --oneline -1` at the bundle commit:
```
85d6bb68 docs(security): re-cut ADR-0186 into four deliveries — this one is three decisions
```

**The collision:** the commit message is stale by one re-cut (four deliveries / three decisions was
the *previous* cut; this is six deliveries / one decision). Since the bundle is amended in place per
the fold-don't-stack rule, `git log` is the only chronology a fresh session sees before opening the
files, and it currently contradicts them. Backlog closure is otherwise **correct**: the ADR closes
**98** only and names 104/100/101/54/65/99 as not closed; plan phase 4 agrees verbatim; the
carry-forward record's slice table covers all six. No item is closed whose mechanism was deferred.

**Evidence — EXECUTED:** `git log --oneline -1`; cross-read of ADR `Backlog:` line, plan phase 4, and
the carry-forward slice table.

**Fix:** amend the commit message to `docs(security): re-cut ADR-0186 to ONE decision — request body
caps; five slices deferred`.

---

## Summary

**14 findings — 6 Critical, 7 Major, 1 Minor.**

| # | severity | one line |
|---|---|---|
| I1 | Critical | "negative → construction error at mount" has no return channel: `Mount` ×3 and the exported `RouteCustomizer.Customize` all return nothing |
| I2 | Major | its only guard, `go build ./examples/...`, provably exits 0 for that break (executed mutation) |
| I3 | Critical | the body-bytes histogram/counter has no home — `httpcore` owns the only instrument constructor and the ADR forbids it by name |
| I4 | Critical | spec §4's "the ADR records…" cites two ADR passages that do not exist (grep exit 1) |
| I5 | Critical | the one cross-slice dependency paragraph names **slice 4** (read path) for what is **slice 6** (the bound) |
| I6 | Critical | MISSING PAIR — a per-request cap does not bound per-instance accumulation; the Positive's "the unbounded-body surface closes" is false |
| I7 | Critical | MISSING PAIR — three new 4xx producers with no value-freedom analysis, and a two-way rule for a three-way error space |
| I8 | Major | row 1(iii)'s ✅ cites *ordering* to discharge a *blanking* risk, and §4XX's must-decide list never mentions 413 |
| I9 | Major | CELEBRATORY — the migration procedure needs a measurement the sibling "keep streaming when unbounded" fix removed on stdlib+gin |
| I10 | Major | MISSING PAIR — §AT-REST's "discover, never hardcode a scope" lesson left with the slice; D1's scope is hardcoded and unenforced |
| I11 | Major | the WARN/refusal fire per route group (3–5× per mount), and `MountHealth` takes no options at all |
| I12 | Major | the `httpcall.ErrBodyTooLarge` guard test cannot fail, and creates the first `transport/`→`action/` import edge |
| I13 | Major | MISSING PAIR — the 1 MiB / 10 MiB defaults bound the *same* axis at two doors, not "different things" |
| I14 | Minor | the bundle commit message describes the previous re-cut |

**Verdict on §4 as constructed:** the table is **right to exist and wrong to be closed**. It derives
five pairs and asserts completeness by construction (*"one survivor plus five removals, so the grid is
1 × 5"*). That framing is itself the round-5 error in a new costume: it counts **removals**, not
**couplings**, and three of its five cells hold more than one coupling (the 4xx row admits this by
listing three). I found **four** couplings it omits — I6, I7, I10, I13 — all of them instances of the
same blind spot: the grid consistently asks *"what does the removed slice hand D1?"* and never
*"what did D1's claims depend on that left with the removal?"*

**On the two "stated, not resolved" cells:** the D1 × 4xx (ii) cell (no correlation id, no log) is
**honest** — I verified the mechanism (`write.go` logs only at `status >= 500`), the ADR states it as a
Negative, and nothing in the bundle references correlation ids or verbose logging. The D1 × BOUND cell
is **not honest** — not evasive by intent, but factually wrong: it discharges itself onto an ADR
passage that does not exist (I4) and a cross-slice pointer that names the wrong slice (I5).

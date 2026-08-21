# ADR-0186 re-audit — FAILURE-MODES / GAPS / MISSING-DECISIONS lens

Bundle commit: `677760d5` (`docs(security): fold ADR-0186's audit — bound at ADMISSION, not at evaluation`)
Worktree: `.../scratchpad/a0186-fail`
Date: 2026-08-21
Lens: failure modes, gaps, missing decisions, decision→phase→package map integrity,
"what must the guard STILL DO", wedging, migration completeness, sentinel routing,
vacuous prescribed tests.

Findings appended as confirmed. Nothing here is batched.

---

## F1 — CRITICAL — Seven of nine phase references in the ADR and spec point at the WRONG phase, and two point at phases that DO NOT EXIST

**Claims attacked (verbatim, with location):**

- `docs/adr/0186-…-posture.md:543` — *"**Phase 4 asserts the count as a machine-checked invariant**, because this enumeration has now rotted twice."*
- `docs/adr/0186-…-posture.md:741` — *"⇒ **Phase 9 derives the list from `internal/persistence/store/migrations/{postgres,mysql,sqlite}` at implementation time** …"*
- `docs/specs/2026-08-21-untrusted-input-and-disclosure.md:234` — *"**Phase 5's correlation-id tests** must cover **both** the span path and the random-hex fallback."*
- spec `:182` — *"the correlation test moves to **phase 5**"*
- spec `:187` — *"**phase 8** is **not** the net"*
- spec `:178` — *"One sentence in D3 and in **phase 9**"*
- spec `:180` — *"**phase 9** must not write them as one posture"*
- spec `:169` — *"D1 minted a second sentinel with that name, in a phase running parallel to **phase 6**"*

**Evidence (executed).** The plan's phase table (`docs/plans/2026-08-21-…md:176-184`) has **seven** phases:

```
| # | package(s)                                              |
| 1 | service                                                 |
| 2 | runtime/validation + definition/model/validate/expr     |
| 3 | transport/http/httpcore (+1 comment in persistence)      |
| 4 | transport/http/stdlib | gin | fiber   (3 parallel agents)|
| 5 | action/httpcall                                         |
| 6 | transport/http/parity                                   |
| 7 | docs + the SECURITY: caveats                            |
```

Grep of every phase reference in the two other bundle documents:

```
$ grep -nE "[Pp]hase [0-9]+" docs/adr/0186-untrusted-input-and-disclosure-posture.md
543:  **Phase 4 asserts the count as a machine-checked invariant**, …
683:  ⚠ Phase 2 must **not** also re-route that package through `expreval` …
741:⇒ **Phase 9 derives the list from `internal/persistence/store/migrations/{postgres,mysql,sqlite}`
$ grep -nE "[Pp]hase [0-9]+" docs/specs/2026-08-21-untrusted-input-and-disclosure.md
169: … in a phase running parallel to phase 6.
178: … One sentence in D3 and in phase 9.
180: … phase 9 must not write them as one posture …
182: … the correlation test moves to phase 5.
187: … phase 8 is **not** the net.
234:  … Phase 5's correlation-id tests must cover both the span path and the random-hex fallback.
```

Cross-checked against plan §2's map, which is the only correct source:

| citation | says | plan §2 map actually assigns | verdict |
|---|---|---|---|
| ADR:543 count invariant | phase 4 (`stdlib`\|`gin`\|`fiber`) | **phase 3**, `httpcore` (§2 row *"D4 redaction helper on all 11 paths + count invariant \| 3"*, phase 3 test 8) | **wrong, and phase 4 EXISTS** |
| ADR:741 at-rest enumeration | phase 9 | **phase 7** (`docs` + `internal/persistence/store`) | **phase 9 does not exist** |
| spec:234 + :182 correlation id | phase 5 (`action/httpcall`) | **phase 4**, the three adapters' `writeErr` (§2 row *"D5 correlation id minted in `writeErr` \| 4"*, phase 4 test 3) | **wrong, and phase 5 EXISTS** |
| spec:187 parity net | phase 8 | **phase 6** (`transport/http/parity`) | **phase 8 does not exist** |
| spec:178/:180 `SECURITY.md` | phase 9 | **phase 7** | **does not exist** |
| spec:169 `httpcall` sentinel | phase 6 | `httpcall` is **phase 5**; parity is phase 6 | wrong |
| ADR:683 no-reroute of the validator | phase 2 | phase 2 (`runtime/validation` + `validate/expr`) | ✅ the one correct reference |

**Verdict — CONFIRMED, and it is a recurrence of the exact defect this revision was rebuilt to close.** The plan deleted two phases (`internal/expreval`, `runtime`) and moved `service` from last to first; the ADR and spec were **not** renumbered. Two of the mis-references name a phase that **exists and is a different, real work unit**, which is the dangerous shape:

- ADR:543 tells a **phase-4 adapter agent** to assert a count invariant over `NewInstanceView`/`mapInstance` call sites. Executed: those call sites do **not** exist in `stdlib`/`gin`/`fiber` — `grep -rn "NewInstanceView(" transport/http/ | grep -v _test` returns hits only under `httpcore/`. The phase-4 agent cannot do it; the phase-3 agent was told (by the ADR) that phase 4 owns it. **The invariant the bundle calls "machine-checked, because this enumeration has rotted twice" can fall between the two phases and ship as prose.**
- spec:234 tells a **phase-5 `action/httpcall` agent** to write the correlation-id tests. `action/httpcall` has no `writeErr`, no `ErrorBody` and no HTTP server. Phase 4's agent, briefed from the spec's assumption list, may treat the both-legs requirement as somebody else's.

The bundle's own audit-#1 root cause was *"a decision stated in the ADR whose realisation lands in a package no phase assigns it to."* This is the same failure with the pointer inverted: the realisation is correctly placed in the plan, and the ADR/spec point elsewhere. A reader who trusts the ADR (the document of record, and the one that survives after the plan is archived) is misdirected in **six** of seven cases.

**Fix (concrete):**
1. Renumber all seven stale references: ADR:543 `Phase 4` → `Phase 3`; ADR:741 `Phase 9` → `Phase 7`; spec:169 `phase 6` → `phase 5`; spec:178/:180 `phase 9` → `phase 7`; spec:182/:234 `phase 5` → `phase 4`; spec:187 `phase 8` → `phase 6`.
2. **Better: stop naming phase numbers in the ADR and spec at all.** Phase numbers are plan-local and have now been renumbered twice; they are guaranteed to rot again. Replace each with the *package* (`the httpcore phase`, `the docs phase`, `the adapter phase`), which does not renumber, and let plan §2's map be the single place numbers appear.
3. Add a plan §2 note: *"No phase number appears outside this table."* — and a delivery-gate sweep item for it.

## F2 — CRITICAL — The body-size histogram is DECLARED in a package that cannot observe a body, RECORDED by no phase, and structurally cannot measure the distribution it exists to reveal

**Claims attacked (verbatim):**

- ADR `:304-308` — *"**Migration and discoverability.** `MaxBodyBytes = 0` is the opt-out, and a `wrkflw_rest_request_body_bytes` histogram joins the existing transport instrumentation **so a consumer can measure their real distribution *before* the cap bites**. A separate observe-only soft-limit option was considered and is **not** shipped — `0` plus the histogram covers the same need at a fraction of the surface."*
- ADR `:816-817` (Negative consequences) — *"Default-on caps will reject payloads that work today. `MaxBodyBytes = 0` and the element/byte bounds' `0` are the opt-outs, and **the body-size histogram lets a consumer measure first**."*
- Plan §2 map `:141` — *"D1 `wrkflw_rest_request_body_bytes` histogram | **3** | `transport/http/httpcore` (`observability.go`)"*

This is the ADR's **entire** migration answer for a breaking, default-ON control, and it is the stated reason a soft-limit option was **not** shipped. Three independent defects.

### (a) `httpcore` never sees a request body — the mapped package does not contain the seam

Executed. `transport/http/httpcore/observability.go:80-85`:

```go
func (i *Instrumentation) Observe(
	ctx context.Context,
	method, routeTemplate string,
	hdr http.Header,
	run func(context.Context) (status int),
)
```

`Observe` receives **headers and a callback**, never a body or a byte count; it records `wrkflw_rest_requests_total` and `wrkflw_rest_request_duration_seconds` from `time.Since(start)` and `status` only. And every `httpcore` endpoint function takes an **already-decoded DTO** (`StartInput`, `SignalInput`, …) — `grep -rn "NewDecoder\|Bind()\|ShouldBind" transport/http/httpcore/ | grep -v _test` returns nothing (evidence §4.1 records `httpcore` **0** decode sites). The bytes exist only in `stdlib`/`gin`/`fiber` `groups.go`, i.e. **phase 4**.

Phase 3 can therefore declare the instrument and a `RecordBodyBytes(...)` method; it cannot record a single observation.

### (b) No phase records it

```
$ grep -n "body_bytes\|histogram\|Histogram" docs/plans/2026-08-21-untrusted-input-and-disclosure.md
141:| D1 `wrkflw_rest_request_body_bytes` histogram | 3 | `transport/http/httpcore` (`observability.go`) |
330:- `wrkflw_rest_request_body_bytes` histogram alongside the existing instrumentation.
```

Both hits are **phase 3**. Plan §3 phase 4 (`:431-500`) prescribes the caps, the per-adapter mechanism, `writeErr`'s correlation id and the per-class logging — and **never mentions the histogram**; none of phase 4's five tests observes it. Phase 6 (parity) does not either. ⇒ the instrument ships registered and permanently empty: a consumer scraping `wrkflw_rest_request_body_bytes` sees **no series at all**, which is indistinguishable from "no traffic".

This is audit #1's root cause verbatim — *"a decision stated in the ADR whose realisation lands in a package no phase assigns it to"* — surviving the very rebuild that was supposed to eliminate it.

### (c) Even correctly wired, it CANNOT measure the distribution, because the default cap truncates the measurement

Executed (`go run`, `net/http` + `httptest`, 4 MiB body against a 1 MiB `MaxBytesReader`):

```
client sent = 4194304 bytes
bytes observable after MaxBytesReader = 1048576
err = http: request body too large ; errors.As(*http.MaxBytesError) = true
MaxBytesError.Limit = 1048576  (does NOT carry the actual size)
```

`http.MaxBytesReader` stops the read at the limit and `*http.MaxBytesError` carries **only `Limit`** — never the true size. So on `stdlib` and `gin` (the two adapters D1 caps with `MaxBytesReader`) the histogram's maximum observable value **is the cap**. A consumer whose real p99 is 6 MiB records 1 MiB and concludes they are safe — right up until the 413s start. "Measure their real distribution *before* the cap bites" is false by construction on 2 of 3 adapters: you must first set `MaxBodyBytes = 0`, and the only signal telling you to do that is the outage the histogram was supposed to prevent.

⚠ Third divergence, unstated: fiber's `len(c.BodyRaw())` pre-check runs **after** the body is fully buffered, so fiber *can* record the true size (up to `fiber.DefaultBodyLimit`) while stdlib and gin cannot. The three adapters would report **different distributions for identical traffic** on the same metric name.

**Verdict — CONFIRMED, three ways.** (a) and (b) are the mapped-row-is-false / unassigned-realisation defects the map was built to catch; (c) refutes the ADR's stated migration rationale and, with it, the justification for not shipping the soft-limit option.

**Fix (concrete):**
1. Split the row: **phase 3** declares `Instrumentation.RecordRequestBodyBytes(ctx, method, route string, n int64)` plus the `wrkflw_rest_request_body_bytes` `Int64Histogram`; **phase 4** calls it from each adapter's decode site, with a per-adapter test asserting one observation with the expected attributes. Add both rows to §2's map.
2. Fix (c) by observing a size the cap cannot truncate. Two options, pick one and say which:
   - **preferred** — record `Content-Length` when present (it is the client's declared size and is not clipped by `MaxBytesReader`), attributed with `capped=true|false`; document that chunked requests fall back to the read count.
   - or ship the observe-only soft limit the ADR rejected: `MaxBodyBytes` enforced at `n` but *measured* against a higher ceiling. The ADR rejected it on the strength of a discoverability story that (c) shows does not work; that rejection must be re-derived, not inherited.
3. Correct ADR `:304-308` and `:816-817`: with `MaxBytesReader` in force the histogram is **capped at the limit**; the honest migration instruction is *"deploy once with `MaxBodyBytes = 0`, observe, then set the cap"* — say that, or fix the measurement per (2).
4. Add a phase-4 test whose falsifier is stated: *it fails against an implementation that declares the instrument and never records* — e.g. assert a non-zero observation count through a test `MeterProvider`.

## F3 — CRITICAL — D5's static 413 DESTROYS the message D2 deliberately built, and merges two unrelated remediations into one useless string

**Claims attacked (verbatim, both in ADR-0186):**

- D2, ADR `:370-371` — *"Both are refused with a single new `service.ErrVariablesTooLarge`, **whose message names which bound tripped**. **Decision 5 routes it → 413.**"*
- D5, ADR `:605` (the class table) — *"| **413** (new) | static `"request too large"`, via `ErrRequestBodyTooLarge` **and** `service.ErrVariablesTooLarge` (Decisions 1 and 2) |"*
- Plan `:203` — *"`service.ErrVariablesTooLarge` — one sentinel; **its message names which bound tripped**."*

**Evidence.** Read together, D2 mandates a message and D5 throws it away. Nothing in the bundle reconciles them; spec §5's `D2 × D5` row only checks that the sentinel is *routed*, and its `D1 × D5` row only checks the "no HTTP caller" case:

> `D2 × D5` … ✅ The sentinel is now `service.ErrVariablesTooLarge` and D5 routes it **by name** to 413

The information destroyed is **provably value-free** by the ADR's own standard. `"workflow-service: variable payload exceeds the element bound (10000)"` contains a *library-configured limit* and no caller data — the same category as `httpcore.Validate`'s `Key: 'DTO.name' … failed on the 'max' tag`, which the ADR spends Evidence §1 proving is safe to keep. The ADR's own words at `:213-214`:

> *"Blanking it wholesale would be pure information loss with zero disclosure benefit, and would silently retire three ADRs."*

That reasoning was applied exhaustively to the 400 arm and **not at all** to the new 413 arm, which this bundle mints.

**Worse than information loss — the merged message is actively misleading.** One static string now covers two unrelated failures with two opposite remediations:

| trip | caller must | static message says |
|---|---|---|
| `ErrRequestBodyTooLarge` (body > 1 MiB wire) | send a smaller/compressed body | "request too large" |
| `ErrVariablesTooLarge` / bytes (vars > 256 KiB) | send fewer/smaller variables | "request too large" |
| `ErrVariablesTooLarge` / elements (> 10 000) | send **fewer keys** — the body may be tiny | "request too large" |

The element case is the one that breaks a caller: a **109 KiB** body (the plan's own phase-1 test 4 fixture — 20 000 small integers, well under both the 1 MiB body cap and the 256 KiB byte bound) is refused with *"request too large"*. A caller who compresses, splits or shrinks the body — the only action that message suggests — fails again, indefinitely, with no signal that the *element count* is the constraint. There is no route that reports the limits either.

**Verdict — CONFIRMED.** This is audit-#1 finding F4 (*"the static-400 default destroyed the actionable messages three prior ADRs deliberately added"*) reproduced one status code over, on an arm this very bundle creates. It is the third instance of the repo's standing ADR-0165 shape: the design reasons exhaustively about what the guard must **not** say and never asks what it must **still** say.

**Fix (concrete):** apply D5's own exception-list method to the 413 arm rather than blanket-blanking it.

| 413 source | rendering |
|---|---|
| `httpcore.ErrRequestBodyTooLarge` | static `"request body too large"` + the configured limit (`MaxBodyBytes`) — the limit is ours, not the caller's |
| `service.ErrVariablesTooLarge` | `err.Error()` — pinned by a test asserting it names **which** bound tripped and its configured value, and contains **no** variable key or value |

Add a row to D5's per-class table and to the phase-3 pin test (plan phase 3 test 3) so the 413 arm inherits the same machine-checked invariant as the 400 arm — otherwise the next sentinel added to 413 silently inherits `static` for the same reason this one did. Also add a phase-1 test asserting `ErrVariablesTooLarge`'s message distinguishes the byte trip from the element trip (falsifier: *it fails against an implementation returning one undifferentiated sentinel message*).

---

## F4 — CRITICAL — Redacting `GetInstanceSnapshot` from `httpcore` RE-EMBEDS the definition for consumers who opted out — the fix defeats the only existing lever against the disclosure D4 itself names

**Claims attacked (verbatim):**

- ADR D4 `:539-542` — *"⚠⚠ **The covered set is the ELEVEN paths named in Context §4**, applied in a helper each one calls"* — the eleven include `GetInstanceSnapshot`.
- Plan §2 map `:153` — *"D4 redaction helper on **all 11** paths + count invariant | **3** | `transport/http/httpcore`"*.
- ADR D4 `:580-582` — *"Four of these live inside `service.instanceJSON`, which is **unexported** with a custom `MarshalJSON` — so covering them is work in **`service`**, not a `httpcore` helper"*.
- ADR D4 `:578-580` — the embedded `definition` … *"`service`'s existing **`WithoutEmbeddedDefinition` is the only lever today**"*.

**Evidence (executed).** `GetInstanceSnapshot` does not build a view — it returns the `service.ProcessInstance` **itself** as the response body (`transport/http/httpcore/endpoints.go:60-66`):

```go
func GetInstanceSnapshot(ctx context.Context, svc service.Service, id string) (int, any, error) {
	pi, err := svc.GetInstance(ctx, id)
	if err != nil { return 0, nil, err }
	return http.StatusOK, pi, nil          // <- the interface value IS the body
}
```

`variables` is produced by `processInstance.MarshalJSON` → `newInstanceJSON(p.def, p.st, p.omitDefinition)` (`service/instance.go:98-99`). Both `p.st` and `p.omitDefinition` are **unexported fields of an unexported struct**; `newProcessInstance` (the only constructor carrying the flag) is unexported (`service/instance.go:57`). The one exported fabrication path is:

```go
// service/instance.go:49-51
func NewProcessInstance(def *model.ProcessDefinition, st engine.InstanceState) ProcessInstance {
	return newProcessInstance(def, st, false)      // <- omitDefinition hard-coded false
}
```

and its godoc says so outright: *"The marshalled document embeds a non-nil definition (ADR-0144); the engine-level opt-out [WithoutEmbeddedDefinition] applies to instances the ProcessEngine hands out, **not to one fabricated here**."*

Executed probe (`service/zzprobe_audit_test.go`, external test package, deleted after) doing exactly what a phase-3 redaction helper must do — rebuild the instance with a redacted state:

```
=== RUN   TestProbeSnapshotRebuildReEmbedsDefinition
    rebuilt snapshot contains "definition" key = true
    rebuilt snapshot contains raw ssn          = false
    marshalled = {"instance_id":"i-1","def_id":"kyc","def_version":3,"status":"running",
      "variables":{"ssn":"[REDACTED]","tier":"gold"},"started_at":"0001-01-01T00:00:00Z",
      "definition":{"id":"kyc","version":3,"nodes":[],"flows":null}}
--- PASS
```

**Verdict — CONFIRMED, and it is a net disclosure REGRESSION on that path.** For a consumer running `service.WithoutEmbeddedDefinition()` (pinned today by `service/instance_test.go:1289` *"WithoutEmbeddedDefinition drops the embed"*), phase 3's helper as mapped would:

- redact `variables` (the intended gain), and
- **re-embed the whole process template** — every gateway and sequence-flow expression source — on a **non-admin** `GET …/snapshot` route, for a consumer who had explicitly turned it off.

D4 names the embedded definition as one of the five disclosure-bearing snapshot fields it deliberately does **not** cover, and names `WithoutEmbeddedDefinition` as *"the only lever today"*. The prescribed fix silently breaks that lever. And D4's own argument for deferring the other four fields — *"they live inside `service.instanceJSON`, which is unexported … so covering them is work in `service`, not a `httpcore` helper"* — **applies verbatim to `variables` on this path**, which the ADR did not notice because it reasoned about the field list rather than the path list.

⚠ Related, same root: `GetActionableView` (`endpoints.go:72`) returns `view.NewActionableView(pi.State(), pi.Definition())`, and the bundle concedes it carries **no variables at all** (evidence §4.3). So of "**all eleven** paths", **one cannot be implemented in `httpcore` at all** and **one has nothing to redact** — the "eleven" is a count of *endpoints touched*, not of paths on which redaction is achievable as mapped. The count invariant prescribed in phase 3 test 8 (`NewInstanceView`/`mapInstance` call sites) does not detect either problem, because `GetInstanceSnapshot` calls **neither** function.

**Fix (concrete) — choose one and record it:**
1. **Preferred: move the snapshot path's redaction into `service`.** Add `service.ProcessInstance`-side support — e.g. a `WithVariableRedactor(func(ctx, scope, vars) map[string]any) Option` on `NewProcessEngine`, applied inside `newInstanceJSON`, so `omitDefinition` and every other marshalling policy is preserved by construction. Add a plan row: *D4 snapshot-path redaction | new phase | `service`* — and note it makes phase 3 depend on phase 1's package, which the current dependency order already allows (3 depends on 1).
2. Or export the missing seam: `service.NewProcessInstanceWithPolicy(def, st, opts...)` carrying `omitDefinition`, plus a way to **read** the flag off an existing `ProcessInstance` (an accessor or an `Options()` method) — without the reader, `httpcore` cannot know which value to pass.
3. Or **remove `GetInstanceSnapshot` from the covered set** and say so explicitly, downgrading the claim from "eleven paths" to "ten, plus a named exception" — but then a non-admin route still serves unredacted `variables`, which contradicts D4's headline and backlog 54's closure for `variables`.
4. Whichever is chosen, add the falsifiable test: *with `WithoutEmbeddedDefinition()` configured AND a `RedactVariables` hook configured, `GET …/snapshot` must contain no `definition` key and no redacted value.* **Falsifier: it fails against a helper implemented with `service.NewProcessInstance`** — i.e. against the only implementation reachable from the package the map names.

## F5 — CRITICAL — A proxied deployment VOIDS the IP deny-list entirely, and the ADR states the opposite as the reason for choosing `Dialer.Control`

**Claim attacked (verbatim, ADR D3 `:469-472`):**

> *"**IP deny-list, in `net.Dialer.Control`.** ⚠ Executed: `Control` receives only the resolved `network, address` … That makes it the right place for an **IP** rule (**it sees every resolved address, so DNS rebinding cannot bypass it**) and an impossible place for a **host** rule."*

and plan phase 5 `:513-515`, which repeats it as the mechanism.

**Evidence (executed).** `Dialer.Control` sees the address the transport **dials**, which — whenever `http.Transport.Proxy` returns a URL — is the **proxy**, never the target. Probe (`net/http` + `httptest` stand-in proxy, a `Control` hook that records every address, target = the canonical SSRF victim):

```
target requested             = http://169.254.169.254/latest/meta-data/
addresses Dialer.Control saw = [tcp4 127.0.0.1:64944]
status = 200 body = "PROXY FETCHED http://169.254.169.254/latest/meta-data/"

DefaultTransport.Proxy is ProxyFromEnvironment? true
```

`169.254.169.254` **never reached the hook**. The request to link-local cloud metadata succeeded end-to-end while the deny-list was installed and enabled.

**Why this is reachable, not theoretical.** Three compounding facts:

1. `http.DefaultTransport.Proxy` is `http.ProxyFromEnvironment` (**executed above: non-nil**). Any implementer building the restricted transport by cloning `http.DefaultTransport` — the idiomatic way to keep `MaxIdleConns`, `TLSHandshakeTimeout`, `ForceAttemptHTTP2` etc. — inherits it.
2. `HTTP_PROXY` / `HTTPS_PROXY` being set is the **norm in egress-controlled corporate and Kubernetes environments** — i.e. precisely the deployments that adopt an SSRF default.
3. The failure is **silent and open**: the option is configured, the tests (which use `httptest` and no proxy) pass, and the control does nothing in production.

The bundle never mentions `Proxy` — `grep -in "proxy" docs/adr/0186-*.md docs/plans/2026-08-21-*.md docs/specs/2026-08-21-untrusted*.md` returns **nothing**. Plan phase 5's test list has no proxy row, so no prescribed test can fail against this.

**Verdict — CONFIRMED. This is a missing decision, and both plausible defaults are wrong in a different direction**, which is exactly why it must be decided rather than left to the implementer:

- `Proxy: nil` — the control holds, and **every consumer who requires a proxy for egress loses `httpcall` entirely** (their traffic cannot leave). A "guard refuses the useful case" of the first order.
- `Proxy: ProxyFromEnvironment` — consumers work, and the control is a no-op.

**Fix (concrete):** add a decision to D3 and a row to plan §2's map (phase 5, `action/httpcall`):

1. **Default `Proxy: nil` on the restricted transport**, and say why: the IP rule is enforced at the dial, and a proxy relocates the dial.
2. Add `WithProxy(func(*http.Request) (*url.URL, error))` as the **explicit** opt-in for proxied egress, whose godoc states in one sentence that *configuring a proxy moves destination control to the proxy — the IP deny-list can no longer see the target, and the host allow-list becomes the only remaining gate.*
3. Make `WithAllowedHosts` **mandatory** when a proxy is configured (refuse the combination otherwise, the same "compose or refuse, never overwrite" rule D3 already applies to `WithHTTPClient`) — with a proxy the host allow-list is the *only* control that still functions, and it runs on the request URL before the dial.
4. Prescribed test with a stated falsifier: `TestProxiedTransportStillRefusesADeniedTarget` — a `Control` hook plus a stand-in proxy, asserting the request to a link-local target is refused. **Falsifier: it fails against an implementation whose restricted transport inherits `ProxyFromEnvironment`.**
5. Correct the ADR sentence: `Dialer.Control` sees every address the transport **dials**, which equals every resolved target address **only when no proxy is configured**. The DNS-rebinding immunity claim carries the same condition.

---

## F6 — MAJOR — D3 refuses `WithURLExpr` + `WithHTTPClient`, and the only escape is disabling the protection — refusing the library's OWN documented use of that option

**Claims attacked (verbatim, ADR D3 `:505-515`):**

> *"⚠ **`WithHTTPClient` collides, and the collision is refused, not resolved silently.** … And "wrap their transport" is not generally possible — `otelhttp.NewTransport(...)` is an opaque `RoundTripper`, not an `*http.Transport` whose `DialContext` can be reached. Therefore: setting **both** `WithURLExpr` and `WithHTTPClient` **without** `WithUnrestrictedTransport()` is a construction error…"*

**Evidence.** The option the ADR refuses is documented in this repo for exactly this purpose (`action/httpcall/httpcall.go:152-153`):

```go
// WithHTTPClient injects the http.Client (e.g. an otel-instrumented one).
// Default: a client with a 30s timeout.
func WithHTTPClient(c *http.Client) Option { return func(h *httpCall) { h.client = c } }
```

So the deployment D3 makes inexpressible is *"I trace my outbound calls"* — against a project whose CLAUDE.md Architecture section lists **"Observability — expose process metrics, enable traces"** as a load-bearing property. A consumer who must have tracing (essentially every production deployment) and has a variable-derived URL is left with one lever: `WithUnrestrictedTransport()` — **turning the entire SSRF protection off**. The default therefore ships disabled for the most mature consumers, which is the worst possible selection bias for a security default.

Same applies to every other ordinary reason to inject a client: mTLS/custom `TLSClientConfig`, a corporate proxy (see F5), tuned connection pooling, a retrying `RoundTripper`.

**The ADR's stated justification is half true and does not support the conclusion.** *We* cannot reach inside *their* opaque `RoundTripper` — correct. But the composition works in the other direction, and the vendor the ADR names supports it. Executed (module cache, the exact version this repo resolves, `go.mod:142` `otelhttp v0.68.0`):

```
$ grep -n "func NewTransport" -A 6 .../otelhttp@v0.68.0/transport.go
49:func NewTransport(base http.RoundTripper, opts ...Option) *Transport {
50-	if base == nil {
51-		base = http.DefaultTransport
52-	}
```

`otelhttp.NewTransport` takes a **base** `RoundTripper`. If the library hands the consumer its restricted transport, `otelhttp.NewTransport(restricted)` composes cleanly and the `DialContext` control survives underneath. The same is true of every instrumenting/retrying `RoundTripper` in common use — they all wrap a base.

**Verdict — CONFIRMED.** This is the third instance of the repo's ADR-0165 shape (*the design gets the "must not" side exhaustively right and refuses the useful case*), and it is the same shape the prompt's brief flagged from the previous round: last round `WithAllowedHosts` was the unimplementable escape hatch; this round `WithAllowedCIDRs` fixes the *internal-address* case but **nothing fixes the custom-client case**. `WithAllowedCIDRs` cannot help here — the consumer's problem is not *which addresses* but *whose transport*.

**Fix (concrete):** keep the refusal for the genuinely ambiguous case, but add the composition seam so the useful case is expressible:

1. Add `httpcall.WithClientTransportMiddleware(func(base http.RoundTripper) http.RoundTripper)`. The library builds the restricted `*http.Transport` (Control dialer + `CheckRedirect` on the client), passes it in as `base`, and installs the returned `RoundTripper` — so `WithClientTransportMiddleware(func(rt http.RoundTripper) http.RoundTripper { return otelhttp.NewTransport(rt) })` is instrumented **and** restricted.
2. Keep `WithHTTPClient` + `WithURLExpr` a construction error, and make its message name **three** levers, in order: `WithClientTransportMiddleware` (compose), `WithAllowedCIDRs`/`WithAllowedHosts` (widen), `WithUnrestrictedTransport` (last resort). Today's prescription names only the last.
3. ⚠ `CheckRedirect` lives on `*http.Client`, not on the transport — state explicitly that middleware composes the **transport** only and the library retains ownership of `CheckRedirect` and `Timeout`.
4. Test with a stated falsifier: `TestTransportMiddlewareStillRefusesNonGlobalUnicast` — a middleware that wraps and counts, asserting both that the wrapper ran **and** that a link-local target is refused. **Falsifier: it fails against an implementation that installs the middleware's transport in place of, rather than on top of, the restricted one.**
5. Add both rows to plan §2's map (phase 5, `action/httpcall`).

## F7 — CRITICAL — The incoming-only bound is defeated by REPETITION on the caller axis, and the wedge D2 traded the stronger property away to avoid is still reachable — as an UNRECOVERABLE persist failure instead of a recoverable 413

**Claims attacked (verbatim, ADR D2):**

- `:382-383` — *"**Bounding the incoming map cannot wedge anything**: the refusal happens before persist, with the caller present, and the caller can retry with less."*
- `:425-426` — *"The untrusted axis this record exists to close is **caller-supplied** input; action output is author-configured."*
- spec §5 `D2 × itself` row — *"✅ The bound is on the **incoming caller-supplied map**, not the merged result. **It cannot wedge** (refusal is pre-persist, caller present, retry with less)."*

**Evidence (executed source, exact citations).**

1. `mergeVars` is a **top-level key-wise copy**, so distinct keys **accumulate** rather than overwrite (`engine/step_state.go:312-321`):

```go
func mergeVars(s *InstanceState, in map[string]any) {
	if len(in) == 0 { return }
	if s.Variables == nil { s.Variables = make(map[string]any, len(in)) }
	maps.Copy(s.Variables, in)
}
```

2. Of the eight `mergeVars` call sites (see F8), **five carry caller-supplied request payloads** and are individually admitted under D2's bound:

| site | function | source | admitted field |
|---|---|---|---|
| `step_triggers.go:45` | `handleStartInstance` | `t.Vars` | `StartInstanceRequest.Vars` |
| `:936` | `handleHumanCompleted` | `t.Output` | `CompleteTaskRequest.Output` |
| `:1028` | `handleSignalReceived` | `t.Payload` | `DeliverSignalRequest.Payload` |
| `:1312`, `:1349` | `handleMessageReceived` | `t.Payload` | `DeliverMessageRequest.Payload` |

3. Signal and message deliveries are **repeatable against one instance**. `handleSignalReceived`'s `markMatched` merges the payload on the first matching dispatch point of *each delivery* (`:1022-1028`); nothing caps the number of deliveries. Any definition where a signal or message can be caught more than once — a loop-back flow, or a **non-interrupting boundary signal event** (which `:1009-1015` documents as a supported, ordinary construct) — accepts an unbounded sequence of them.

**The attack.** Each request is individually admitted (≤ 256 KiB, ≤ 10 000 elements), each merge uses fresh top-level keys, and the persisted map grows without limit:

- ~**256 requests** at the 256 KiB default ⇒ a ~64 MiB `wrkflw_instances.snapshot`.
- `ASSUMPTION (unverified — needs Docker, out of scope for this lens)`: MySQL 8's default `max_allowed_packet` is 64 MiB, so at that point **every persist of that instance fails**. The column is `JSON NOT NULL` (`internal/persistence/store/migrations/mysql/0001_init.sql:18`; `JSONB` in postgres `:15`, `TEXT` in sqlite `:25`), so the ceiling is the driver/server packet limit, not the DDL. **The magnitude is verified; the exact threshold is not.**
- Well before any hard limit, `copyVars`/`maps.Clone` and `json.Marshal` of the snapshot run on **every read and every persist** of that instance, so throughput collapses first.

**Why this refutes the trade, not just a sentence.** D2 chose the weaker property *specifically* to avoid a wedge:

> *"Bounding the **merged** result … converts the unbounded runtime growth below into an **unrecoverable wedge**: once a service action's output has pushed the map past 256 KiB, every subsequent `CompleteTask` would be refused **413 forever**."*

That reasoning trades a **recoverable, caller-visible, pre-persist 413** for an **unrecoverable, silent, post-persist failure** — the instance stops advancing, the failure surfaces as a store error (500 / incident) with no diagnosis pointing at variable size, and the only documented exit (`POST /admin/instances/{id}/cancel`) must itself persist the same oversized snapshot. The wedge was not removed; it was moved to a worse place and made reachable from the untrusted axis instead of the author-configured one.

And `:425-426`'s framing is false as written: the untrusted, caller-supplied axis **is** the one that accumulates. The ADR attributes aggregate growth entirely to author-configured action output; three of the five accumulating sites are caller-supplied request payloads.

**Verdict — CONFIRMED.** The per-request bound is a real improvement against a *single* hostile request and gives no protection against a hostile *sequence*, which is the cheaper attack (no oversized payload needed — 256 ordinary ones).

**Fix (concrete) — the cheap half is available without inventing an incident-disposition design:**

1. **Add an aggregate ceiling that is enforced where a caller is present and can be told.** At the same admission seam, check `len(existing) + len(incoming)` (or the persisted snapshot's last-known byte size, already computable at the store) against a **separate, much larger** `service.WithMaxInstanceVariableBytes(n)` — default e.g. 4 MiB, well above any legitimate map and well below any store limit. Refuse the *incoming request* with a distinct sentinel. This wedges nothing that is not already wedged: at 4 MiB the instance is already failing, and unlike the merged-map bound it does **not** fire on a small `CompleteTask` against a normally-sized instance.
2. If (1) is judged out of scope, then **the residual must be stated honestly** and the two false sentences corrected: replace *"cannot wedge anything"* with *"cannot wedge on the refusal path; the aggregate map remains unbounded and an instance can be driven past the store's row/packet limit by repeated admitted requests, which is an unrecoverable state with no repair verb"*, and correct *"the untrusted axis … is caller-supplied input"* to name the five accumulating sites. Open it as a **named backlog item with the attack written down**, not as the current generic *"bound runtime variable growth"* item, which describes only the action-output half.
3. Add a `wrkflw_instance_variable_bytes` gauge/histogram at persist so the growth is observable before it wedges — the same discoverability gap F2 identifies for bodies applies here with **no** instrument at all.
4. Prescribed test with a falsifier: `TestRepeatedAdmittedSignalsAccumulateWithoutLimit` — N deliveries each under both bounds, asserting the aggregate exceeds them. **Falsifier today: it PASSES (documenting the hole); after fix (1) it must fail at the ceiling.** State which of the two the bundle intends.

---

## F8 — MAJOR — The `mergeVars` enumeration is wrong in COUNT, in CLASSIFICATION and in IDENTITY: 8 sites exist, 3 are named, one named site is a REQUEST source, and another is misidentified

**Claim attacked (verbatim, ADR D2 `:417-421`):**

> *"⚠⚠ **Runtime growth.** The variable map is also grown by `mergeVars` from **three non-request sources** — a service action's output (`engine/step_triggers.go:161`), human-task completion output (`:936`) and **the message/callback mirror** (`:1208`) — plus the engine's own `_errorMessage`/`_errorAttempts` writes."*

Repeated in the Consequences at `:844-845` (*"New item: bound runtime variable growth (`mergeVars` from action/task/message output)"*) and in spec §3 (*"Runtime variable growth via `mergeVars` from action/task/message output"*).

**Evidence (executed).**

```
$ grep -rn "mergeVars(" --include='*.go' . | grep -v _test.go
engine/step_triggers.go:45      engine/step_triggers.go:161    engine/step_triggers.go:841
engine/step_triggers.go:936     engine/step_triggers.go:1028   engine/step_triggers.go:1208
engine/step_triggers.go:1312    engine/step_triggers.go:1349
engine/step_state.go:314        (the definition)
```

Enclosing function per site (derived with `awk`, not read off):

| site | enclosing function | merged value | ADR's account |
|---|---|---|---|
| `:45` | `handleStartInstance` | `t.Vars` | **not named** — and it is a *request* source |
| `:161` | `handleActionCompleted` | `t.Output` (service action output) | ✅ named, correctly classified |
| `:841` | `applyOutcomeExposure` | `{name: outcome}` — one pair | **not named** (benign, but it is a site) |
| `:936` | `handleHumanCompleted` | `t.Output` | named — **but classified as a "non-request source"; it IS `CompleteTaskRequest.Output`, one of D2's own four admitted fields** |
| `:1028` | `handleSignalReceived` | `t.Payload` (`DeliverSignalRequest.Payload`) | **not named** — a request source |
| `:1208` | `handleSubInstanceCompleted` | `t.Output` — a **child instance's** output (call-link) | named as *"the message/callback mirror"* — **misidentified** |
| `:1312`, `:1349` | `handleMessageReceived` | `t.Payload` (`DeliverMessageRequest.Payload`) | **neither named** — these are the actual message merges |

**Verdict — CONFIRMED, three distinct errors in one sentence, and each changes a conclusion:**

- **Count.** Eight sites, three named. The plan's own §4 warning — *"assume one more is wrong"* — applies, and this enumeration is not in §4's table at all, so no re-derivation covered it.
- **Classification.** Calling `:936` a *non-request* source is what makes D2's residual look like it is only about author-configured action output. Together with the two unnamed message sites and the unnamed signal site, this is the mis-framing F7 turns into an attack: **five of the eight sites are caller-supplied**, and the ADR's residual names none of them as such.
- **Identity.** `:1208` is `handleSubInstanceCompleted` — a **call-link child instance's output map**, not a message mirror. This is a *separate, genuinely unbounded, non-caller source* the bundle therefore never considered, and it composes: a child instance whose map grew at runtime dumps its `Output` into the **parent**, so growth is transitive across the call-link tree. `wrkflw_call_links.output` is one of D6's twelve plaintext columns, which is the only place in the bundle this path appears at all.

**Fix (concrete):**
1. Re-derive the enumeration and put it in plan §4's table as a row (*"`mergeVars` call sites | **8** — `step_triggers.go:{45,161,841,936,1028,1208,1312,1349}`; **5 caller-supplied**, 2 unbounded non-caller (`:161` action output, `:1208` child-instance output), 1 single-pair (`:841`)"*), citing **function names**, not line numbers — spec §0 already commits to symbol anchoring and this sentence uses raw line numbers for a file that will be edited.
2. Correct D2's residual paragraph: separate **caller-supplied accumulation** (F7) from **author-configured growth**, and name the call-link path explicitly.
3. Fix the Consequences backlog item text — *"`mergeVars` from action/task/message output"* omits signal, start and sub-instance output, i.e. it under-specifies the very item that is supposed to carry this residual forward.
4. Since this enumeration is load-bearing for a residual that will outlive the bundle, make it machine-checked like the other two: a test asserting the count of `mergeVars` call sites, so the next merge site added forces a decision rather than silently joining an unbounded class.

## F9 — CRITICAL — Two prescribed phase-5 tests CANNOT PASS as written: `httptest` binds loopback, which the IP deny-list refuses — the plan diagnoses this defect in test 2 and then repeats it in tests 3 and 5

**Claims attacked (verbatim, plan §3 phase 5 `:552-559`):**

> *"3. `TestAllowedHostsUsesTheHOSTNAME` — **allow-list `localhost` against a loopback `httptest` server**, and assert an allow-listed hostname resolving to a **denied** IP is still refused."*
>
> *"5. `TestURLExprFollowsSameHostRedirectByDefault` — ⚠ **the control.** **Falsifier:** *it fails against a `CheckRedirect` that refuses when the allow-list is empty.*"*

**Evidence.** The bundle establishes all three premises itself, and they are jointly contradictory:

1. `httptest.NewServer` binds **loopback**. Executed in this audit's proxy probe: the stand-in `httptest` server was dialled at `127.0.0.1:64944`.
2. The IP deny-list refuses loopback — ADR D3 `:474-476`: *"refuse any resolved address that is not global unicast — `IsLoopback()`, …"*.
3. The host allow-list **does not override it** — ADR D3 `:486-487`: *"⚠ **It does not override the IP deny-list** — an allow-listed host that resolves to `169.254.169.254` is still refused, or the option becomes a rebinding bypass."*
4. Both tests set `WithURLExpr`, so the restricted client **is** installed (D3: restriction applies when `urlExprProg != nil`).

⇒ **Test 3's first leg** — *"allow-list `localhost` against a loopback `httptest` server"* — must produce a **refusal**, not the success it is written to demonstrate. `localhost` resolves to `127.0.0.1`; `IsLoopback()` is true; the dial is denied regardless of the host allow-list. The leg that proves *"the host gate accepts a hostname"* cannot be written this way at all.

⇒ **Test 5** — billed *"⚠ **the control**"*, the one test protecting every existing user from an empty-allow-list `CheckRedirect` that refuses all redirects — never reaches a redirect: the **first hop** to the `httptest` server is denied at dial time. It fails for a reason unrelated to its falsifier, and an implementer will "fix" it by adding `WithAllowedCIDRs("127.0.0.0/8")`, at which point it is no longer testing the **default** configuration its name and falsifier claim.

**This is not a subtle miss — the plan diagnoses the identical defect two bullets earlier**, for test 2:

> *"⚠ The previous revision's `httptest`-based version was refused at the **first hop** (httptest binds `127.0.0.1`) and never reached `CheckRedirect` at all — green against no `CheckRedirect` whatsoever."*

The lesson was applied to exactly one test (test 2, rewritten to assert `client.CheckRedirect(req, via)` as a unit) and **not propagated to its two siblings that still need a real network hop**. Same failure shape as the repo's standing lesson on `mergeVars`/timer-arm enumerations: a fix applied at the site it was found and not at the class.

**Verdict — CONFIRMED.** Test 5's unreachability is the more serious of the two: it is the **only** control standing between this delivery and "the SSRF default silently broke redirect-following for every existing `WithURLExpr` user", and it cannot execute. Its falsifier (*fails against a `CheckRedirect` that refuses when the allow-list is empty*) is unreachable, so a wrong `CheckRedirect` would ship green.

**Fix (concrete):**
1. **Test 5** — assert the same way test 2 does: call `client.CheckRedirect(req, via)` **directly as a unit** with an empty allow-list and a non-empty `via`, asserting `nil` (follow). Add a second unit row with a configured allow-list and an off-list hop asserting refusal. No network, no loopback, and the falsifier becomes reachable. ⚠ Also add a row for `len(via) >= 10` so the stdlib's own redirect cap is not accidentally replaced.
2. **Test 3** — split it. (a) A **unit** assertion on the host gate: the hostname predicate accepts `localhost` and rejects `evil.example.com`, with no dial. (b) An **end-to-end** leg that needs a hop must pair `WithAllowedHosts("localhost")` **with** `WithAllowedCIDRs("127.0.0.0/8")` — and the plan must say so, because without it the test is unimplementable. (c) The "allow-listed host resolving to a denied IP is still refused" leg must then choose a denied IP **outside** the exempted CIDR (e.g. an allow-listed host whose resolution is stubbed to `169.254.169.254`), or the exemption masks exactly what it asserts.
3. **Add a standing note to phase 5's brief**: *"every `httptest` server in this package binds loopback, which the deny-list refuses; any test needing a real hop must either exempt `127.0.0.0/8` explicitly or assert the gate as a unit."* Test 4 (`TestAllowedCIDRsOptsANetworkBackIn`) and test 6 (`TestBaseURLIsUnrestricted`) survive only because they happen to be the exemption test and the unrestricted-path test respectively — that is luck, not design, and the note is what makes it design.

---

## F10 — MAJOR — An SSRF refusal mints no sentinel, is treated as RETRYABLE, and lands the blocked URL in `incidents[].error` — the one disclosure field D4 explicitly declines to cover

**Claims attacked.** ADR D3 (`:469-503`) specifies *what* is refused and never specifies **how the refusal is surfaced**. Plan §2's map has five D3 rows, none of them an error contract; plan phase 5's eight tests assert refusals and never assert an error's type, sentinel or retryability.

**Evidence (executed source).** A `net.Dialer.Control` refusal propagates out of `h.client.Do` and is handled here (`action/httpcall/httpcall.go:339-343`):

```go
	resp, err := h.client.Do(req)
	if err != nil {
		// Transport/timeout error — retryable (plain error).
		return nil, fmt.Errorf("workflow-httpcall: request failed: %w", err)
	}
```

Contrast every *policy* failure in the same file, which is explicitly permanent — `:232`, `:237`, `:241`, `:246`, `:280`, `:296`, `:310`, `:317`, `:349`, `:364` all wrap in `action.NonRetryable`, and the package's own contract says so (`:64` *"a plain error is treated as retryable"*).

**Three consequences, none decided in the bundle:**

1. **A deterministic policy denial is retried.** The dial to `169.254.169.254` will be refused identically every time. The node burns its entire retry budget with backoff on an outcome that cannot change, then raises an incident. Every other permanent condition in `httpcall` is marked `NonRetryable`; this one — added by *this* delivery — would not be.
2. **No sentinel.** D1 mints two exported sentinels (`ErrRequestBodyTooLarge`, `ErrVariablesTooLarge`) and carefully renames one to avoid a collision; D3 mints **none**. A consumer cannot distinguish *"blocked by the library's SSRF policy"* from *"the host is down"* — neither in code (`errors.Is`) nor in an operator's incident view. That is the difference between "our config is too strict, add a CIDR" and "the upstream is down", and it is the first question anyone will ask when this default starts refusing things.
3. ⚠ **It composes into a disclosure the bundle believes it left uncovered.** The wrapped error text is `workflow-httpcall: request failed: Get "http://<the evaluated URL>": dial tcp <ip>: …` — i.e. **the full expression-derived URL, including any process-variable value interpolated into it**. That string lands in `incidents[].error`, which ADR D4 `:575-577` names as explicitly **NOT covered** by redaction:

   > *"**NOT covered** … `incidents[].error` (the raw `err.Error()` of a failed action — **for an `httpcall` node, the target URL and query string**, i.e. the same value `ClassifyError` blanks at 5xx)"*

   and which is served on the **non-admin** `GET …/snapshot` route. So D3's protection, implemented the obvious way, **increases** traffic through the uncovered channel: every blocked call now writes an attacker-chosen URL into a publicly-readable incident. D3's scope statement (`:518-524`) covers the opposite direction only — the unredacted map going *out* through `httpcall` — and never the blocked URL coming *back* through the incident.

**Verdict — CONFIRMED.** A missing decision with a security consequence, sitting exactly in the D3 × D4 seam that spec §5's `D3 × D4` row marked *"✅ Not a defect"*.

**Fix (concrete):**
1. Mint `action/httpcall.ErrDestinationNotAllowed` (exported, `workflow-httpcall:` prefixed per the repo convention) and return it wrapped in `action.NonRetryable` for **both** gates — the IP refusal (surfaced from `Dialer.Control` and unwrapped from `*url.Error`/`*net.OpError` after `client.Do`, since `Control`'s error is not returned verbatim) and the host/redirect refusal. Add a plan §2 row and a phase-5 test asserting `errors.Is(err, ErrDestinationNotAllowed)` **and** `action.IsNonRetryable(err)` (falsifier: *it fails against an implementation that lets the dial error propagate as a plain retryable error*, which is the default behaviour today).
2. Make the error message **value-free about the target**: name the *policy* that refused and the *category* (`loopback`, `link-local`, `private`, `host not allow-listed`), not the URL. Then the incident text carries no caller value and item 3 above dissolves without needing D4 to grow.
3. Add a sentence to D3's scope statement: *a refusal must not echo the evaluated URL, because `incidents[].error` is not redacted.* Add the corresponding row to spec §5's `D3 × D4`, replacing *"✅ Not a defect"*.

## F11 — MAJOR — The plan's "no agent needs Docker" is FALSE and its package list is short: D6's invariant test lands in `internal/persistence/store`, a container-only package

**Claims attacked (verbatim):**

- Plan §1 `:112-115` — *"**Docker:** the standing carve-out covers the Verification runs only. **Every package in this delivery is container-free**: `service`, `runtime/validation`, `definition/model/validate/*`, `transport/http/*`, `action/httpcall`, `persistence` (one comment). **No agent needs Docker**; say so explicitly in each brief so nobody asks."*
- Plan §2 map `:170` — *"D6 `SECURITY.md` derived-enumeration + invariant test | 7 | **docs + `internal/persistence/store`**"*
- ADR D6 `:741-746` — *"⇒ **Phase 9 derives the list from `internal/persistence/store/migrations/{postgres,mysql,sqlite}` at implementation time** … **and the invariant is a test**: any new column in those tables is either listed or explicitly justified."*

**Evidence (executed).** The two sentences contradict each other: §2 names `internal/persistence/store` as a package this delivery edits; §1's container-free list does **not** contain it (it lists `persistence`, the public root package holding the one godoc comment — a different package).

```
$ grep -rln "dbtest\.\|testcontainers" internal/persistence/store/*_test.go | head -5
internal/persistence/store/call_links_errors_test.go
internal/persistence/store/conformance_test.go
internal/persistence/store/clock_injection_test.go
internal/persistence/store/conflict_mapping_test.go
internal/persistence/store/migration_parity_test.go
$ ls internal/persistence/store/*_test.go | wc -l
      38
```

And the closest existing analogue — the test the D6 invariant would naturally sit beside — is itself container-bound (`internal/persistence/store/migration_parity_test.go:1-17`):

```go
package store_test

import (
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "modernc.org/sqlite"
	"github.com/kartaladev/wrkflw/internal/dbtest"
	…
)
```

⇒ the phase-7 agent's own verification (`go test ./internal/persistence/store/...`) requires a running Docker daemon. This matches the repo's standing note that `internal/persistence/store` is **not** in the container-free set.

**Verdict — CONFIRMED, and it is a schedulability defect, not a typo.** Phase 7 is the **controller** phase, briefed (per §1) to say *"no Docker needed"*. An agent told that, then asked to add a test to a 38-file container-bound package, either (a) cannot verify its own work, (b) silently writes a test it never runs, or (c) stalls asking for a Docker decision the brief pre-emptively refused. Option (b) is the likely one and produces exactly what D6 warns against: *"an incomplete list presented as exhaustive is strictly worse than the silence D6 rejects."*

⚠ Compounding: **phase 7 has no `Verify:` line at all.** Phases 1–6 each end with a `go test -race -count=1 ./<pkg>/...`; phase 7 (`:595-634`) ends with the `HANDOVER.md` bullet and no verification command. So even the instruction to run the invariant is missing.

**Fix (concrete):**
1. Add `internal/persistence/store` to §1's package list and **correct the blanket claim**: *"all packages are container-free **except `internal/persistence/store`**, whose package-level test run needs Docker."* Per the repo's Docker rule the phase-7 brief must then state the requirement explicitly rather than deny it.
2. Write the invariant as a **pure file-parsing test over `migrations/{postgres,mysql,sqlite}/*.sql`** that needs no database — then the only Docker dependency is the *package's other tests*, and the brief can prescribe the narrow verification `go test -race -count=1 -run '^TestPlaintextColumnsAreEnumerated$' ./internal/persistence/store/...`. ⚠ Per the repo's standing rule, a `-run` filter on a name that does not exist exits 0, so the brief must require `-v` and confirm the test actually **ran**.
3. Or place the invariant in a **new container-free package** (e.g. `internal/persistence/store/migrations` with its own `*_test.go` reading the embedded FS), which removes the Docker question entirely. State which.
4. Give phase 7 a `Verify:` line, like every other phase.

---

## F12 — MAJOR — D5 blanks the `callback` strategy's message, destroying a message the CONSUMER wrote and gave no way to opt back in — the sixth actionable 400, and the same mistake D5 spends a section avoiding for `ErrBadInput`

**Claims attacked (verbatim, ADR D5):**

- `:686` — *"`avro` and `callback` render static text."*
- `:200-202` — *"`validation.ErrInvalidInput` wraps **four** strategies … `callback` **emits whatever a consumer's validator writes**."*
- Plan phase 2 `:270-272` — *"The rendering is an **allow-list keyed on strategy KIND** … `jsonschema` → … **everything else → static `"invalid input"`**."*

**Evidence (executed source).** The `callback` strategy is a **consumer-authored Go function**, and nothing else (`definition/model/validate/callback/callback.go:1-25`):

```go
// Package callback is a code-only validation adapter wrapping a Go func. …
func New(fn func(ctx context.Context, input map[string]any) error) validate.ValidationStrategy {
	return strategy{fn: fn}
}
func (s strategy) Validate(ctx context.Context, input map[string]any) error { return s.fn(ctx, input) }
```

This is the one validation source whose message is written by the **consumer of this library**, deliberately, for their own callers — *"amount exceeds the approved credit limit"*, *"delivery date must be a working day"*. D5 replaces every one of them with `"invalid input"`, and provides **no mechanism to opt back in**: the allow-list keys on *kind*, and `callback` is a single kind covering every consumer function.

**Verdict — CONFIRMED. This is the third "guard refuses the useful case", and the bundle's own reasoning refutes it.** D5 spends a full section (ADR `:204-214`, Evidence §1) establishing that `ErrBadInput` keeps `err.Error()` because it was **executed and shown value-free** rather than assumed leaky, and states the principle:

> *"Blanking it wholesale would be pure information loss with zero disclosure benefit."*

For `callback`, the library cannot execute the message — but the **consumer can**, and they are the only party who knows. Deny-by-default is the right posture for an unknown function; *deny with no opt-in* converts a documented extension point into one that cannot produce a client-visible message at all. Note the asymmetry the bundle creates: a consumer who writes their validation as a **jsonschema** strategy gets a rendered `keywordLocation`; the same consumer writing it as Go gets `"invalid input"`.

⚠ And the stated reason is not the real one. `avro` is blanked because it was **executed and shown to leak** (Evidence, enum path echoes the submitted value). `callback` is blanked because it is *unknown*. Two different verdicts share one table row and one rationale sentence, which is how the opt-in went missing.

**Fix (concrete):** keep deny-by-default; add the one seam that makes it honest.

1. Add an explicit client-safe marker in `runtime/validation`, e.g.
   ```go
   // ClientSafe marks err's message as safe to return to an API caller.
   // The library never inspects the message; the wrapper is the consumer's assertion.
   func ClientSafe(err error) error
   ```
   The gate renders `err.Error()` when `errors.Is`/a marker interface matches, and static text otherwise. Unmarked consumer errors stay blanked — the default is unchanged.
2. Document it on `callback.New`'s godoc **and** in `SECURITY.md`'s phase-7 bullet (*"which strings a non-admin caller can read"*), since a consumer marking a leaky message is now the consumer's own disclosure decision — which is the correct place for it.
3. Add plan §2 rows (phase 2, `runtime/validation`) and a phase-2 test with a stated falsifier: `TestClientSafeCallbackMessageSurvives` — **falsifier: it fails against an allow-list keyed only on strategy kind**, which is what phase 2 currently prescribes. Keep the existing `TestNonStructuredStrategiesRenderStatically` row for the **unmarked** callback so both directions are pinned.
4. If the marker is rejected, say so as a decision with the cost written down — *"consumer-authored validation messages are not returnable to API callers in this release"* — rather than leaving it as an unremarked consequence of a table row.

## F13 — MAJOR — The "six breaks, not one" migration list is itself short by ~6, and EVERY omission is outside `transport/http` — including two default-on breaks that hit consumers with no HTTP transport at all

**Claim attacked (verbatim, plan phase 7 `:617-623`):**

> *"**`CHANGELOG.md` + `STABILITY.md`** — ⚠ **six breaks, not one.** (i) the 403 message becomes static; (ii) the 400 message changes shape; (iii) a correlation-id **field** appears, breaking `DisallowUnknownFields` decoders; (iv) a **new 413 status** appears on routes that previously returned 400 or 500, breaking exhaustive status switches; (v) the **eight exported endpoint functions** gain the response-policy parameter — a **source** break; (vi) `Logger` starts receiving 4xx records, changing log volume."*

Mirrored in ADR Consequences `:804-815`.

**Evidence.** All six items are `transport/http` items. Walking the ADR's own Decision section for consumer-visible behaviour changes outside that tree yields at least five more, none of them listed in the CHANGELOG deliverable:

| # | break | decision | who it hits | listed? |
|---|---|---|---|---|
| vii | **`MaxBodyBytes` defaults to 1 MiB** — requests that succeed today return 413 | D1 | every HTTP consumer | ADR *Negative* only, **not** in the six |
| viii | **`WithMaxVariableBytes` (256 KiB) / `WithMaxVariableElements` (10 000) default ON** — `StartInstance`/`DeliverSignal`/`DeliverMessage`/`CompleteTask` start returning `ErrVariablesTooLarge` | D2 | ⚠ **every `service` consumer, including one with no HTTP transport mounted at all** | **no** |
| ix | **expression-derived `httpcall` URLs to internal addresses are refused** — working automation stops | D3 | `action/httpcall` consumers | ADR *Negative* mentions the `10.x` case; **not** in the six |
| x | **`WithURLExpr` + `WithHTTPClient` becomes a construction error** — code that compiles and runs today fails at `Do` | D3 | see F6 — the documented otel case | **no** |
| xi | **`runtime/validation`'s error message text changes** (jsonschema → `keywordLocation`; `expr`/`avro`/`callback` → static) | D5 | consumers who log or match validation text **without** HTTP | **no** |
| xii | **`engine.ErrInvalidOutcome`'s message is reshaped** (`"%w: node %q outcome %q"` → `node %q: outcome not declared`) | D5 | embedded `engine` consumers, not only HTTP | folded into (ii), which is described as an *HTTP* break |

**Verdict — CONFIRMED, and the omission has a single shape.** The list was derived by walking the **transport diff**; every miss is a decision whose consumer-visible effect lands in `service`, `action/httpcall` or `runtime/validation`. That is the plan's own diagnosed failure mode — *"the failure was the grep's **NET**"* (§4 `:665-667`) — applied to the breaking-change enumeration, which §4's re-derivation table does **not** cover.

Two of the misses matter disproportionately for a **library-first** product: (viii) and (xi) break a consumer who imports `service`/`runtime` and mounts none of our HTTP adapters — the primary audience per CLAUDE.md — and neither the CHANGELOG list nor `STABILITY.md` would tell them.

⚠ Note the asymmetry the ADR creates: `MaxBodyBytes` gets an opt-out **and** a (broken, see F2) discoverability instrument; the variable bounds get an opt-out and **no instrument at all**, so a consumer cannot measure their variable-map distribution before the default bites. The ADR's Negative sentence — *"`MaxBodyBytes = 0` and the element/byte bounds' `0` are the opt-outs, and the body-size histogram lets a consumer measure first"* — reads as if the histogram covers all three; it covers one.

**Fix (concrete):**
1. Extend the phase-7 CHANGELOG/STABILITY list to twelve items, grouped by **package** rather than by decision, and re-derive it by walking each of the six decisions' *consumer-visible surface* rather than the transport diff. Add it as a row to plan §4's enumeration table so it is subject to the same "assume one more is wrong" discipline as the others.
2. Mark (vii)–(x) explicitly as **behavioural** breaks (source-compatible, runtime-breaking) — they will not be caught by any consumer's compiler, which makes them the most dangerous class and the one a CHANGELOG exists for.
3. Add a variable-size instrument (`wrkflw_service_variable_bytes` / `_elements` histograms at the admission seam, recorded **before** the bound is applied so it is not truncated — cf. F2(c)), or state in `SECURITY.md` and the CHANGELOG that the variable bounds ship with no measurement path and the only safe upgrade is `0` first.

---

## F14 — CRITICAL — How `WithMaxVariableBytes` MEASURES bytes is never decided; the only implementable mechanism costs 948 µs and a 265 KB allocation per request — ~50× its co-located twin, with no early exit, and it contradicts D1's carefully-established "wire bytes"

**Claim attacked (verbatim, ADR D2 `:364-375`):**

> *"`service.WithMaxVariableBytes(n int64)`, default **256 KiB**, and `service.WithMaxVariableElements(n int)`, default **10 000**, are enforced **together, at the same seam, at the same moment** … The element count walks the map with an **early exit at `n+1`**, so it is `O(min(elements, n))` and **can never cost more than the bound it enforces**."*

**Evidence.** The bundle specifies the element mechanism precisely and the byte mechanism **not at all**:

```
$ grep -n "MaxVariableBytes\|byte bound\|Marshal\|encoded\|serial" docs/adr/0186-…-posture.md
364:- `service.WithMaxVariableBytes(n int64)`, default **256 KiB**, and
397:⇒ **What the co-location closes is the WINDOW** …
412:`vars["httpBody"]` … **40×** the variable byte bound …
823:  … should size `WithMaxVariableBytes` accordingly …
$ grep -n "MaxVariableBytes\|byte bound\|Marshal" docs/plans/2026-08-21-…md
142: | D2 `WithMaxVariableBytes` / `WithMaxVariableElements` | 1 | service |
201: - `service.WithMaxVariableBytes(n int64) Option` — default 256 KiB, `0` = unbounded.
```

No sentence anywhere says what a "byte" is. **This is decisive because `service` receives a decoded `map[string]any`, not bytes** — `service/request.go:19,30,44,72` are all `map[string]any` (evidence §4.6 confirms the four fields and their type). The wire bytes are gone by the time D2's seam runs; they existed only in the adapter, two layers up. So the bound must **re-serialize** the map to measure it.

Measured, both mechanisms on the same fixture (Apple M4 Pro, plain mode, `b.Loop()`, `-benchtime=200x`):

```
n=10000  marshalled bytes = 48901 (47.8 KiB)
n=45540  marshalled bytes = 262141 (256.0 KiB)      <- corroborates the bundle's 45 540 figure exactly

BenchmarkByteBound_JSONMarshal-14        200    948523 ns/op   265098 B/op   4 allocs/op
BenchmarkElementBound_WalkEarlyExit-14   200     19000 ns/op        0 B/op   0 allocs/op
```

**Verdict — CONFIRMED, four ways:**

1. **Missing decision on the central new control.** Byte measurement is unspecified, so the implementer picks — and the two natural picks differ: `json.Marshal` + `len` (accurate, expensive) versus a recursive size estimate (cheap, **not a bound** — it can under-count and admit an oversized payload, failing open).
2. **The co-location argument is asymmetric.** D2's headline property is that the two bounds run *"at the same seam, at the same moment"* and that the element walk *"can never cost more than the bound it enforces."* The byte bound has **no such property**: `json.Marshal` cannot early-exit, is `O(payload)` unconditionally, and allocates a **full second copy** (265 KB at the bound) — **~50× the walk, and 4 allocations against 0**. The sentence that reassures the reader about cost is true of exactly one of the two bounds, and the ADR does not say which.
3. **It re-commits D2's own refuted analytical error, inverted.** D2 §3 rejects the `ctx` mechanism after measuring it at **866 ns/op, +6 allocs**, and calls the counting alternative *"~12× cheaper."* The byte bound as measured is **~1 100× more expensive than the mechanism the ADR rejected on cost grounds**, and was never measured because it was never specified. ⚠ Fair framing: this is the *worst case* (a payload at the bound); a typical 3-scalar map marshals in hundreds of ns. But the worst case is what an attacker sends, and it is free for them — a 255 KiB payload is **admitted** and still pays the full 948 µs + 265 KB, on every request, for the check alone. That is an amplification the control introduces.
4. **Two incompatible notions of "bytes" ship in one release.** D1 goes to considerable length (executed `c.BodyRaw()` vs `c.Body()`, a folded correction, a stated residual) to make `MaxBodyBytes` mean **wire bytes uniformly across three adapters**. `MaxVariableBytes` would mean **re-marshalled bytes** — a different number for the same request (whitespace, key ordering, number formatting, `json.Number` vs `float64` round-tripping). A caller who sends 250 KiB of variables and is told "256 KiB" cannot predict which side of the line they land on, and the two knobs' names imply they are commensurable.

**Fix (concrete):**
1. **Decide and state the mechanism**, in D2 and as a plan §2 row. Recommended: measure with a **counting writer** — `json.NewEncoder(&countingWriter{limit: n})` that aborts at `n+1` — giving the byte bound the same *"can never cost more than the bound it enforces"* property the element walk has, and roughly the same constant. Then the ADR's cost sentence becomes true of both.
2. **Define the unit explicitly** in the godoc: *"bytes of the map's canonical JSON encoding, which may differ from the request's wire bytes; `MaxBodyBytes` bounds the wire, `MaxVariableBytes` bounds the encoded map."* Say it in `SECURITY.md` too — a consumer sizing the two knobs against each other (which ADR `:823` explicitly tells them to do for `httpcall`'s 10 MiB) needs to know they are not the same unit.
3. **Add a benchmark to phase 1** and a stated falsifier for the cost property: `BenchmarkVariableBoundsAtTheSeam` — assert the check over a map **100× the bound** costs no more than one **at** the bound, *for both knobs*. Plan phase 1 test 5 (`TestElementCountExitsEarly`) prescribes exactly this **for the element bound only**; extend it, with the falsifier *"it fails against a byte bound implemented as `json.Marshal` + `len`."*
4. Record the measured numbers in the evidence file, replacing the current silence — this is the bundle's most-used new code path and the only one whose cost is unmeasured.

## F15 — MAJOR — The prescribed "machine-checked count invariant" is structurally blind to 2 of the 11 paths, and counts a population that is neither 11 nor a superset of it

**Claim attacked (verbatim, plan phase 3 test 8 `:416-418`):**

> *"⚠ **Plus the count invariant:** assert that the number of `NewInstanceView`/`mapInstance` call sites routed through the helper **equals the number that exist**. This enumeration has rotted **twice**; a number in prose will rot again."*

and ADR D4 `:543` — *"Phase 4 asserts the count as a machine-checked invariant, because this enumeration has now rotted twice."* (whose phase number is separately wrong — F1).

**Evidence (executed).**

```
$ grep -rn "NewInstanceView(" --include='*.go' transport/http/httpcore/ | grep -v _test.go | wc -l
       7
$ grep -rn "mapInstance("    --include='*.go' transport/http/httpcore/ | grep -v _test.go | wc -l
       7
```

Broken down (from evidence §4.2, re-run in this audit):

| population | count | are these the "eleven"? |
|---|---|---|
| raw `NewInstanceView(` occurrences | **7** — `seam.go:42,54`, `endpoints.go:17`, `view.go:23` (the **declaration**), `admin_endpoints.go:111,121,514` | no — 4 are the declaration, the `mapInstance` default and the two `InstanceMapper` defaults |
| raw `mapInstance(` occurrences | **7** — `endpoints.go:15` (declaration) + 6 call sites | no |
| genuine response paths reachable by this grep | **9** = 6 `mapInstance` + 3 direct admin `NewInstanceView` | short by 2 |
| the covered set D4 claims | **11** | — |

**The two missing paths are the ones the grep cannot see at all:**

- `GetInstanceSnapshot` (`endpoints.go:60-66`) returns `pi` — the `service.ProcessInstance` — and calls **neither** function.
- `GetActionableView` (`endpoints.go:72`) calls `view.NewActionableView`, not either function.

⇒ an invariant defined over `NewInstanceView`/`mapInstance` call sites **can never equal 11**, and can never detect a regression on either of those two paths. It also cannot detect the next occurrence of the actual failure mode — *a new endpoint that projects instance state by some third route*, which is exactly how the enumeration rotted the last two times (6 → 6+2 when the two mapper-less endpoints were found; 6+2 → 6+2+3 when the three direct-`NewInstanceView` admin endpoints were found).

**Verdict — CONFIRMED.** The invariant protects the part of the enumeration that is already known-correct and is blind to the part most likely to rot. As written it will also require the implementer to hand-exclude the four non-call-site occurrences of `NewInstanceView(` and the one declaration of `mapInstance(` — a hand-tuned constant inside a test billed as *"machine-checked, because a number in prose will rot again."* A hand-tuned constant **is** a number in prose.

**Fix (concrete):** invert the invariant so it keys on the property that actually matters — *"every exported endpoint that returns instance-derived state applies the response policy"* — rather than on two function names.

1. Make the response policy a **required parameter** of every exported endpoint that projects state (D4 already threads it into eight of them — extend to all, including `GetInstanceSnapshot` and `GetActionableView`). Then the **compiler** is the invariant: a new endpoint cannot be written without it. This is strictly stronger than any grep and costs nothing extra, since the parameter thread is already a listed breaking change.
2. Keep a **belt-and-braces reflection/AST test** if desired, but define it over *exported functions in `httpcore` whose return type is instance-derived*, not over two callee names; assert each one's body reaches the policy helper.
3. Fix the phase attribution (F1) and state the number **9 / 11 / 2** explicitly in the test's comment so the next reader knows which population the assertion covers and which two paths it structurally cannot.

---

# Ranked index

| # | severity | one-line |
|---|---|---|
| **F5** | CRITICAL | A proxied deployment voids the IP deny-list entirely; `Dialer.Control` never sees the target (executed: `169.254.169.254` fetched, 200 OK, hook saw only `127.0.0.1`). `http.DefaultTransport.Proxy` is non-nil. `Proxy` appears **0 times** in the bundle. Missing decision; both defaults wrong in different directions. |
| **F7** | CRITICAL | The incoming-only variable bound is defeated by **repetition** — `mergeVars` is `maps.Copy`, 5 of 8 sites are caller-supplied, signals/messages are repeatable. ~256 admitted requests wedge an instance at the store's packet limit: the wedge D2 traded the stronger property away to avoid, now unrecoverable instead of a recoverable 413. |
| **F14** | CRITICAL | How `WithMaxVariableBytes` measures bytes is never decided. `service` holds a decoded map, so the only accurate mechanism is `json.Marshal`: measured **948 523 ns/op, 265 098 B/op, 4 allocs** vs the element walk's **19 000 ns/op, 0 allocs** — ~50×, no early exit, ~1 100× the mechanism D2 rejected on cost. Also a second, incompatible notion of "bytes" against D1's wire bytes. |
| **F4** | CRITICAL | Redacting `GetInstanceSnapshot` from `httpcore` **re-embeds the definition** for `WithoutEmbeddedDefinition` consumers (executed). `omitDefinition` is unexported; `service.NewProcessInstance` hard-codes `false`. The fix defeats the only existing lever against a disclosure D4 itself names as uncovered. |
| **F1** | CRITICAL | **Seven of nine** phase references in the ADR and spec are wrong; two name phases that do not exist (8, 9); two name real, *different* phases (ADR's count invariant → phase 4 = the adapters, which contain no such call site; spec's correlation-id tests → phase 5 = `action/httpcall`, which has no `writeErr`). Audit #1's root cause with the pointer inverted. |
| **F9** | CRITICAL | Two prescribed phase-5 tests cannot pass: `httptest` binds loopback, which the IP rule refuses and the host allow-list explicitly does not override. Test 5 — billed *"the control"*, the only guard against the SSRF default breaking redirects for every existing user — never reaches a redirect. The plan diagnoses this exact defect for test 2 and repeats it in 3 and 5. |
| **F2** | CRITICAL | The body-size histogram is mapped to `httpcore`, which never sees a body (`Observe` takes headers + a callback); **no phase records it**; and with `MaxBytesReader` in force it is truncated at the cap (executed: 4 MiB sent, 1 048 576 observable, `MaxBytesError` carries only `Limit`). The ADR's whole migration story, and its reason for not shipping a soft limit. |
| **F3** | CRITICAL | D5's static 413 destroys the message D2 mandates ("names which bound tripped") and merges three remediations into one. A 109 KiB body over the *element* bound is refused with *"request too large"*. Audit #1's F4, one status code over, on an arm this bundle mints. |
| **F6** | MAJOR | `WithURLExpr` + `WithHTTPClient` refused; the only escape is `WithUnrestrictedTransport()`. Refuses the option's **own documented use** (*"e.g. an otel-instrumented one"*) against a project requiring traces. The ADR's justification is half true: `otelhttp.NewTransport(base …)` (executed, v0.68.0) composes in the other direction. |
| **F13** | MAJOR | "Six breaks, not one" is short by ~6, and every omission is outside `transport/http` — including two default-on breaks (variable bounds; validation message text) that hit consumers with **no HTTP transport mounted**. The list was derived from the transport diff — the plan's own diagnosed "grep's NET" failure. |
| **F10** | MAJOR | An SSRF refusal mints no sentinel and is **retryable** (`httpcall.go:339-343` returns a plain error, comment: *"retryable"*), so a deterministic denial burns the retry budget, then writes the blocked URL into `incidents[].error` — the field D4 explicitly declines to redact, on a non-admin route. Sits in the D3 × D4 seam spec §5 marked ✅. |
| **F8** | MAJOR | The `mergeVars` enumeration is wrong in count (8 sites, 3 named), classification (`:936` is `CompleteTaskRequest.Output`, called a "non-request source") and identity (`:1208` is `handleSubInstanceCompleted` — a child instance's output, not "the message/callback mirror"). The real message sites `:1312`/`:1349` are unnamed. |
| **F12** | MAJOR | D5 blanks `callback` — a **consumer-authored** validation message — with no opt-in, while spending a section proving `ErrBadInput` should keep its message. A consumer writing validation as jsonschema gets a rendering; the same consumer writing it in Go gets `"invalid input"`. |
| **F15** | MAJOR | The "machine-checked count invariant" is defined over `NewInstanceView`/`mapInstance` call sites (7 + 7 raw occurrences, 9 genuine) and is **structurally blind to 2 of the 11 paths** — `GetInstanceSnapshot` and `GetActionableView` call neither. It protects the known-correct part and cannot detect the failure mode that rotted the enumeration twice. |
| **F11** | MAJOR | Plan §1's *"every package is container-free … no agent needs Docker"* is false: §2 maps D6's invariant test to `internal/persistence/store` (38 test files, ≥5 using `dbtest`/testcontainers; the analogous `migration_parity_test.go` imports pgx + `dbtest`). Phase 7 also has **no `Verify:` line at all**. |

**Totals: 15 findings — 8 Critical, 7 Major, 0 Minor.**

Root-cause clustering (4 clusters, not 15 independent defects):

1. **The map is right and the other two documents point elsewhere** — F1, and F2/F4/F11/F15 are rows that are *present but false* (wrong package, unrecordable, unimplementable, container-bound, or blind). The mechanical check audit #1 prescribed was built and then **not run against the ADR and spec**.
2. **The guard refuses the useful case, three more times** — F3 (413 blanks D2's own message), F6 (instrumented client), F12 (consumer's own validation message). All three are the ADR-0165 shape: the "must not" side is exhaustively right.
3. **Per-request bounds do not compose over a sequence** — F7 and F14: the bound's *scope* and the bound's *cost* were both reasoned about one request at a time.
4. **`action/httpcall` was designed as a dial-time control and nothing else** — F5 (proxy), F9 (loopback tests), F10 (no sentinel, retryable, leaks). D3 has five map rows and no error contract, no proxy decision, and no runnable end-to-end test.

---

# What HELD — do not re-litigate

Executed or source-verified in this audit and found **correct**:

- **`ClassifyError`'s shape.** Exactly six arms, ordered 404 `:28` / 403 `:32` / 409 `:34` / 400 `:36-50` / 422 `:51` / default 500 `:57`; the 400 arm carries exactly **eight** sentinels across five `errors.Is` groups; only 500 blanks. Read verbatim from `transport/http/httpcore/errors.go`. The in-code ADR-0146/0152/0183 rationale is present at `:38-49` as quoted.
- **`ErrBadInput` is the highest-volume 400 and the three ADR rationales are real** — the comments are in the switch being edited.
- **The 45 540 figure.** Executed independently here: 45 540 JSON integers marshal to **262 141 bytes = 256.0 KiB exactly**. The bundle's `ASSUMPTION (unverified)` label on the ~6 bytes/element conversion is now dischargeable for this element shape.
- **`mergeVars` is `maps.Copy` over a lazily-allocated destination** (`engine/step_state.go:312-321`) and **`copyVars` is `maps.Clone`** (`:322`) — the shallow-clone premise under D4 is correct.
- **The four `service` admission fields are the only `map[string]any` in `service`'s request types** (`grep` over `service/*.go` non-test returns `request.go:19,30,44,72` plus two *projection* fields in `instance.go`). Evidence §4.6 holds. ⚠ Caveat, not a refutation: `authz.Actor.Attributes map[string]any` (`authz/authz.go:38`) is a **second unbounded map** that reaches the ABAC env (`authz/authz.go:132-135`: `env := {"actor": actor, "vars": vars}`), so the ADR's Positive claim that *"both ABAC evaluators"* inherit the bound holds for the `vars` root of their env and not the `actor` root. Not exploitable over HTTP today — `httpcore.Actor` (`dto.go:12-15`) carries **`ID` and `Roles` only, no attributes** — so I am recording it here rather than as a finding.
- **The transport routes through `service`.** All three adapters and `httpcore` import `github.com/kartaladev/wrkflw/service`; no adapter reaches `runtime`/`engine` for a write path. D2's bound is therefore genuinely on the HTTP untrusted path.
- **The chain path does not wedge.** `runtime/chain/chainer.go:216` calls `c.starter.Drive(ctx, dec.Def, id, dec.Vars)` — a driver-level seam, **not** `service.StartInstance` — so a predecessor whose map grew at runtime cannot be refused at admission when it chains. Plan §0 item 2 raised this; it is clean.
- **Error routing is complete for the two sentinels the bundle names.** `ErrRequestBodyTooLarge` and `service.ErrVariablesTooLarge` are both routed to the new 413 arm by name; `httpcore/errors.go:12` already imports `service`, so there is no cycle and phase 3 can see phase 1's sentinel in the stated order. ⚠ The **unrouted** error is D3's, which mints none at all — that is F10, not a routing-table gap.
- **`GetActionableView` carries no task variables.** `runtime/view.ActionableTask` has six fields and no `Vars` (evidence §4.3 re-confirmed by grep). Deleting `TestActionableViewRedactsTaskVars` is correct.
- **Discharged items I did not re-derive, per brief:** the O(n²) ladder and n = 10 000; the ctx-path benchmark; `gate.go:45`'s `%s` flattening; the 413-before-400 ordering; the eight-sentinel count; `expr.MaxNodes` inverted; fiber's `BodyLimit` unreachable from a mounted group. **I found no reason to dispute any of them.**


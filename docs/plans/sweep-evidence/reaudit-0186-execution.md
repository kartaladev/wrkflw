# Re-audit of ADR-0186 bundle — EXECUTION lens

Worktree: `/private/tmp/claude-501/-Users-zakyalvan-Documents-RND-wrkflw/fa4d4e94-df9a-493c-8448-72c3e28bcba7/scratchpad/a0186-exec`
Bundle commit: `677760d5`
Date: 2026-08-21
Step 0: all four bundle files PRESENT — verified.

Findings appended below as confirmed.

---
## E1 — CRITICAL — D2's admission seam is NOT the closed set of four `service` request fields: `runtime.ProcessDriver` exports four more caller-supplied variable-map entry points, and `BroadcastSignal` has no `service` equivalent at all

**Claims attacked (verbatim):**

- Evidence `docs/specs/2026-08-21-adr-0186-premise-evidence.md` §4.6:
  > "**Four, and only four.** No other `service` request type carries a `map[string]any`."
- ADR `docs/adr/0186-…md` D2:
  > "are enforced **together, at the same seam, at the same moment**: where a caller-supplied variable map is admitted. That seam is the closed set of four request fields (Evidence §4.6)"
- ADR Consequences/Positive:
  > "⭐ **The bound acts on the MAP, not on an evaluator, so every expression surface that reads process variables inherits it for the caller-supplied contribution** — both ABAC evaluators, the engine's gateway path, `action/httpcall`'s URL expression and `action/transform`."

**Probe** — `go doc` on the module-root public package CLAUDE.md names as the product
(`runtime/`), plus a repo-wide non-test grep for `BroadcastSignal`:

```
$ go doc ./runtime ProcessDriver
package runtime // import "github.com/kartaladev/wrkflw/runtime"

type ProcessDriver struct {
	// Has unexported fields.
}
    ProcessDriver is the reference single-process driver loop.

func NewProcessDriver(opts ...Option) (*ProcessDriver, error)
func (driver *ProcessDriver) ApplyTrigger(ctx context.Context, def *model.ProcessDefinition, instanceID string, ...) (engine.InstanceState, error)
func (driver *ProcessDriver) BroadcastSignal(ctx context.Context, name string, payload map[string]any) error
func (driver *ProcessDriver) DeliverMessage(ctx context.Context, name, correlationKey string, payload map[string]any) error
func (driver *ProcessDriver) Drive(ctx context.Context, def *model.ProcessDefinition, instanceID string, ...) (engine.InstanceState, error)
...

$ grep -rn "func (driver \*ProcessDriver) Drive" runtime/processdriver.go
447:func (driver *ProcessDriver) Drive(ctx context.Context, def *model.ProcessDefinition, instanceID string, vars map[string]any) (engine.InstanceState, error)

$ grep -rn "BroadcastSignal" --include='*.go' service/ transport/ | grep -v _test
(no output — exit 1)

$ go doc ./service | grep -iE "broadcast|signal"
type DeliverSignalRequest struct{ ... }

$ grep -rn "Payload" engine/step_triggers.go
1028:			mergeVars(s, t.Payload)
1312:		func() { mergeVars(s, t.Payload) },
1349:	mergeVars(s, t.Payload)
```

**Observed:** `runtime.ProcessDriver` — an **exported module-root type**, which CLAUDE.md
declares *is* the product ("the exported packages at the repo root — e.g. `engine/`,
`definition/`, `runtime/` … that a consumer imports and embeds in *their* application") —
exposes **four** caller-supplied variable-map entry points that never pass through `service`:
`Drive(…, vars map[string]any)`, `BroadcastSignal(…, payload map[string]any)`,
`DeliverMessage(…, payload map[string]any)` and `ApplyTrigger(…, trg engine.Trigger)` (every
payload-bearing trigger constructor, e.g. `engine.NewStartInstance(at, vars)` at
`engine/trigger.go:201`). And `BroadcastSignal` has **no `service` equivalent whatsoever** —
`grep` over `service/` and `transport/` is empty, while `examples/scenarios/signal_broadcast/main.go:108`
calls `driver.BroadcastSignal(ctx, "market-open", map[string]any{"at": "09:30"})` as the
documented consumer API. Its payload reaches `mergeVars(s, t.Payload)` in the engine.

**Verdict: CONFIRMED.** Evidence §4.6's grep net (`grep -n "map\[string\]any" service/request.go`)
answers a *narrower* question than the sentence it supports. The sentence "no other `service`
request type carries a `map[string]any`" is true of `service/request.go`; the ADR's
load-bearing sentence — "that seam is the closed set of … where a caller-supplied variable
map is admitted" — is **false for the library**, which is the product. A consumer who embeds
`runtime.ProcessDriver` directly (the primary documented shape) gets **zero** bound, and even a
`service`-using consumer must drop to the driver for broadcast.

This is the exact failure the plan's §0 item 1 asked for ("A fifth path into `State.Variables`
that a caller controls would be a hole straight through Decision 2") — there are four, and the
grep's *net* is why they were missed, which is also the lineage's stated recurring defect.

**Proposed fix (concrete):** Do **not** answer this by moving the bound into `engine` (that
reopens everything D2 deleted). Either:
1. **Restate D2's scope honestly and narrowly** — "the bound is a `service`-facade admission
   control; a consumer driving `runtime.ProcessDriver` directly owns their own input bounds" —
   and delete the Positive-consequences sentence about every expression surface inheriting it,
   or requalify it as *"for maps admitted through `service`"*; **and** `SECURITY.md` must name
   the four driver entry points as unbounded, beside the runtime-growth residual it already names; **or**
2. add the same `service`-owned bound helper as an **exported** `service.BoundVariables(vars, …) error`
   (or a `runtime` option) so the driver path can opt in, and have the plan assign it a phase.

Either way the ADR sentence *"the closed set of four request fields"* must gain the qualifier
**"admitted through the `service` facade"**, and Evidence §4.6 must record the driver
enumeration so the next reader does not re-derive it from the same blind grep.

---
## E2 — CRITICAL — `authz.Actor.Attributes` is a second caller-supplied `map[string]any` in the ABAC env, carried by FOUR `service` request types, and D2 does not bound it — while the ADR claims both ABAC evaluators inherit the bound

**Claims attacked (verbatim):**

- Evidence §4.6:
  > "**Four, and only four.** No other `service` request type carries a `map[string]any`."
- ADR D2, Consequences/Positive:
  > "⭐ **The bound acts on the MAP, not on an evaluator, so every expression surface that reads process variables inherits it for the caller-supplied contribution** — **both ABAC evaluators**, the engine's gateway path…"

**Probe A — the type.** `authz/authz.go:33-39` and the ABAC env at `authz/authz.go:130-134`:

```
type Actor struct {
	ID         string         `json:"id"`
	Roles      []string       `json:"roles,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
}
...
		env := map[string]any{
			"actor": actor,
			"vars":  vars,
		}
		ok, err := attrEval.EvalBool(spec.Attribute, env)
```

`authz.Actor` is a field of **four** `service` request types — `ClaimTaskRequest.Actor`
(`service/request.go:52`), `CompleteTaskRequest.Actor` (`:59`), `ReassignTaskRequest.By` (`:84`),
`RefreshTaskCandidatesRequest.By` (`:94`). Each therefore transitively carries a
`map[string]any`.

**Probe B — is it the same cost axis?** Throwaway `authz/zzprobe_exec_test.go` (deleted after),
running the **repo's own** `authz.RoleAuthorizer.Authorize` — not `expr` directly — with the
identical non-short-circuiting quadratic predicate over each axis:

```
=== RUN   TestProbeActorAttributesUnmetered
    ACTOR-ATTR axis n=1000 elapsed=20ms err=<nil>
    VARS       axis n=1000 elapsed=19ms err=<nil>
    ACTOR-ATTR axis n=2000 elapsed=77ms err=<nil>
    VARS       axis n=2000 elapsed=76ms err=<nil>
    ACTOR-ATTR axis n=4000 elapsed=304ms err=<nil>
    VARS       axis n=4000 elapsed=304ms err=<nil>
--- PASS: TestProbeActorAttributesUnmetered (0.80s)
```

**Observed:** the two axes are cost-identical to within measurement noise, and reproduce the
ADR's own O(n²) ladder (ADR Context §2 quotes 25/98/391 ms at n = 1000/2000/4000 for `vars`;
this run gives 20/77/304 ms for **both** `vars` and `actor.Attributes` on the same machine).
The predicate had to be made non-short-circuiting (`# == -1`, never matches) — a first attempt
using a matching literal returned in 0 ms at n = 4000 because `any()` short-circuits, which is
worth recording since the ADR does not state its predicate.

**Verdict: CONFIRMED.** `actor.Attributes` is an unmetered caller-supplied map on exactly the
axis D2 exists to close, and it is **inside the env of the one evaluator the ADR names as
newly covered**. D2 bounds `Vars`/`Payload`/`Payload`/`Output` only, so the Positive-consequences
sentence "both ABAC evaluators … inherit it for the caller-supplied contribution" is **false for
the actor half of that evaluator's env**. Evidence §4.6's sentence is likewise false as written
(four `service` request types *do* carry a `map[string]any`, via an embedded struct) — its grep
matched declared field types in one file and was blind to composition, the same *net* failure as E1.

**Out-of-scope note:** the HTTP DTO (`transport/http/httpcore/dto.go:12-15`) declares
`Actor{ID, Roles}` with **no** `Attributes`, so the HTTP surface is closed today. Who populates
the actor is ADR-0185's question and I am not folding it in. The library surface — which is the
product — is open regardless of ADR-0185.

**Proposed fix (concrete):** add `Actor.Attributes` to D2's admission bound at the same four
sites (it is the same helper, one extra call per request), **or** state explicitly in D2 and in
`SECURITY.md` that actor attributes are *consumer-resolved, not caller-supplied* and therefore
out of the untrusted axis — and in that case **delete the "both ABAC evaluators" quantifier from
the Positive consequences**, because half of that evaluator's env stays unbounded. Also correct
Evidence §4.6 to say "four *directly declared* `map[string]any` request fields; a fifth reaches
the ABAC env through `authz.Actor.Attributes`".

---
## E3 — CRITICAL — D5 keeps `err.Error()` for `ErrBadInput` on a probe of the WRONG LAYER: the 36 *decode* wrap sites echo caller values verbatim, including the whole `def_ref` twice

**Claims attacked (verbatim):**

- ADR D5 table row:
  > "| `httpcore.ErrBadInput` | `err.Error()` | **executed**: `httpcore.Validate` renders `Key: 'DTO.name' Error:Field validation for 'name' failed on the 'max' tag` — field + tag, no value, **not even a length**. This is the highest-volume 400 on all 26 routes |"
- ADR Context §5:
  > "Executed: `httpcore.Validate`'s message is `Key: 'DTO.name' Error:Field validation for 'name' failed on the 'max' tag` — **value-free, and value-free even for a length constraint**."
- Evidence §1:
  > "⇒ D5 keeps `err.Error()` for `ErrBadInput`. The two `ErrBadInput` wrap sites that DO embed a caller value are named in §4 and are edited instead."
- ADR Consequences/Positive:
  > "**Five of the eight sentinels — including `ErrBadInput`, every DTO on all 26 routes — keep their message, because it was executed and shown value-free rather than assumed leaky.**"

**Why this is the repo's signature failure.** Evidence §1 executed `httpcore.Validate` — the
`go-playground/validator` **tag** layer. But `ErrBadInput` has **two** producers, and the
larger one is the **decode** layer: 36 sites of the form
`fmt.Errorf("%w: %w", httpcore.ErrBadInput, err)` where `err` is the *decoder's* error, not
`Validate`'s. `stdlib/groups.go:41-44`:

```go
var in httpcore.StartInput
if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
    writeErr(cfg, w, req, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
```

The claim was evidenced one layer away from where the decision acts.

**Probe** — throwaway `transport/http/httpcore/zzprobe_exec_test.go` (deleted after), decoding
into the **repo's own** exported DTOs exactly as `groups.go` does, then applying the identical
`%w: %w` wrap:

```
=== RUN   TestProbeDecodeErrorEchoesCallerValue
    qualifier bad version        -> workflow-httpcore: bad input: workflow-model: invalid qualifier: bad version in "kyc:SECRET-4111-1111-1111-1111": strconv.Atoi: parsing "SECRET-4111-1111-1111-1111": invalid syntax
    qualifier empty id           -> workflow-httpcore: bad input: workflow-model: invalid qualifier: empty id in ":123"
    qualifier version 0          -> workflow-httpcore: bad input: workflow-model: invalid qualifier: version must be >= 1 in "acct-4111111111111111:0"
    number overflow into int64   -> workflow-httpcore: bad input: json: cannot unmarshal number 99999999999999999999999 into Go struct field RedriveInput.ids of type int64
    wrong type for int           -> workflow-httpcore: bad input: json: cannot unmarshal string into Go struct field ResolveIncidentInput.add_attempts of type int
    syntax error                 -> workflow-httpcore: bad input: invalid character '-' after object key:value pair
    wrong type for map           -> workflow-httpcore: bad input: json: cannot unmarshal string into Go struct field SignalInput.payload of type map[string]interface {}
--- PASS
```

**Observed:** **four of seven shapes echo the caller's submitted value verbatim.** The
`def_ref` case echoes the caller's entire string **twice** — once from
`definition/model/qualifier.go:52` (`fmt.Errorf("%w: bad version in %q: %w", ErrInvalidQualifier, s, err)`)
and again from the wrapped `strconv.Atoi` error. `qualifier.go:45` and `:55` are two more `%q`
echoes of the raw caller string. `encoding/json`'s `UnmarshalTypeError` echoes an out-of-range
**number literal** verbatim. All of these arrive at `ClassifyError`'s 400 arm wrapped in
`ErrBadInput` and are rendered by `Message: err.Error()` — which D5 explicitly **preserves**.

**Verdict: CONFIRMED.** The D5 row is not merely incomplete — it is the arm the ADR calls "the
highest-volume 400 by a wide margin", kept verbatim on an executed claim about a *different*
producer. The evidence-file sentence "The two `ErrBadInput` wrap sites that DO embed a caller
value are named in §4" is false: `admin_endpoints.go:30` and `dto.go:174` are the two sites that
embed a caller value *in their own format string*; the 36 decode sites embed it **through the
wrapped error**, which `%w` carries into `err.Error()` unchanged.

**Proposed fix (concrete).** D5 cannot keep a blanket `err.Error()` for `ErrBadInput`. Split the
arm, because the two producers have opposite properties:

1. **`Validate`-produced `ErrBadInput` keeps `err.Error()`** — Evidence §1's probe stands for
   this producer, and it is where the three prior ADRs' actionable value lives. Give
   `httpcore.Validate` its own sentinel (e.g. `ErrValidation`, wrapping `ErrBadInput` so the
   status stays 400) so the classifier can tell the two apart by type rather than by hope.
2. **Decode-produced `ErrBadInput` renders static text** — `"malformed request body"` plus the
   correlation id — with the raw decoder error going to the log under the same
   `WithVerboseErrorLogging` gate D5 already defines. Nothing actionable is lost: a decoder
   error names a Go struct field, not the JSON path, and the field name is already in the
   `Validate` message for every required field.
3. Separately, `definition/model/qualifier.go:45,52,55` echo the caller's raw `def_ref` in
   `%q` on a path reachable from an anonymous POST body. Either stop echoing `s`, or route
   `def_ref` parsing through a `Validate` tag. Add this to the plan's phase table — today no
   phase touches `definition/model`, which is exactly the "a decision whose realisation lands in
   a package no phase assigns it to" defect audit #1 named.
4. The plan's pin test must therefore assert **rendering per producer**, not per sentinel — a
   test that only enumerates sentinels cannot fail on this defect, since `ErrBadInput` is in the
   list either way.

---
## E4 — CRITICAL — D3's IP rule as written FAILS OPEN for every IPv6 address (`::1`, `fe80::1`, `fc00::1` all admitted), and the stated "property" and its enumerated helper list deny DIFFERENT SETS in both directions

**Claim attacked (verbatim), ADR D3, bullet "IP deny-list, in `net.Dialer.Control`":**

> "The rule is stated as a **property**, not a list of five categories: refuse any resolved
> address that is not global unicast — `IsLoopback()`, `IsLinkLocalUnicast()`,
> `IsLinkLocalMulticast()`, `IsInterfaceLocalMulticast()`, `IsUnspecified()`, `IsPrivate()`
> (RFC1918 + ULA `fc00::/7`) — plus the ranges Go's helpers do not cover: `100.64.0.0/10`
> (CGNAT), `192.0.0.0/24`, `198.18.0.0/15`. Evaluated **after `ip.To4()`**, so IPv4-mapped IPv6
> (`::ffff:127.0.0.1`) is normalised."

**Probe** — throwaway Go program (`/tmp/ipprobe`, deleted after) implementing (a) the literal
prescription "evaluated after `ip.To4()`", (b) the same enumeration without the `To4()` step,
(c) the three extra prefixes, and (d) Go's `IsGlobalUnicast` (the *stated property*), over the
addresses the plan's §0 item 5 names plus the ones it does not:

```
addr                       To4?     denyAfter  denyNoTo4  extra    GlobUni
127.0.0.1                  yes      true       true       false    false
0.0.0.0                    yes      true       true       false    false
::ffff:127.0.0.1           yes      true       true       false    false
100.64.0.1                 yes      false      false      true     true
192.0.0.1                  yes      false      false      true     true
198.18.0.1                 yes      false      false      true     true
169.254.169.254            yes      true       true       false    false
10.0.0.5                   yes      true       true       false    true
172.16.0.1                 yes      true       true       false    true
192.168.1.1                yes      true       true       false    true
::1                        nil      false      true       false    false
fe80::1                    nil      false      true       false    false
fd00::1                    nil      false      true       false    true
fc00::1                    nil      false      true       false    true
::127.0.0.1                nil      false      false      false    true
64:ff9b::7f00:1            nil      false      false      false    true
2001:db8::1                nil      false      false      false    true
239.255.255.250            yes      false      false      false    false
224.0.1.1                  yes      false      false      false    false
255.255.255.255            yes      false      false      false    false
240.0.0.1                  yes      false      false      false    true
192.88.99.1                yes      false      false      false    true
8.8.8.8                    yes      false      false      false    true

nil net.IP: IsLoopback=false IsUnspecified=false IsPrivate=false IsLinkLocalUnicast=false IsGlobalUnicast=false
```

**Observed — three distinct defects:**

**(a) FAILS OPEN on IPv6.** Column `denyAfter` implements the sentence literally. `net.IP.To4()`
returns **nil** for a genuine IPv6 address, and every `net.IP` predicate on a nil receiver
returns `false` (bottom line of the transcript). So `::1` → **admitted**, `fe80::1` →
**admitted**, `fc00::1`/`fd00::1` → **admitted**. `http://[::1]:8080/` is the single most obvious
SSRF target on a dual-stack host, and the security control's own normalisation step is what lets
it through. This is the ADR-0165 inverted-predicate shape: the guard refuses the harmless case
(`denyAfter` is correct for IPv4) and admits the harmful one.

**(b) The stated PROPERTY and the enumerated LIST are different sets, in both directions.**
- "not global unicast" **admits** every RFC1918 address: `10.0.0.5`, `172.16.0.1`,
  `192.168.1.1`, `fc00::1`, `fd00::1` all have `IsGlobalUnicast() == true`. An implementer who
  takes the ADR at its word ("the rule is stated as a property") and writes
  `if ip.IsGlobalUnicast() { allow }` ships an SSRF filter that permits the entire private
  internet — the *exact* thing D3 exists to stop.
- The enumeration **admits** things the property denies: `239.255.255.250` (SSDP
  administratively-scoped multicast), `224.0.1.1`, `255.255.255.255` (broadcast) are all
  `denyNoTo4 == false` and `IsGlobalUnicast() == false`. The list has `IsLinkLocalMulticast` and
  `IsInterfaceLocalMulticast` but not `IsMulticast()`, and no broadcast check.

**(c) Reachable-and-internal addresses that ARE global unicast and are covered by nothing:**
`::127.0.0.1` (IPv4-compatible IPv6, `To4()` = nil, `IsLoopback()` = false — the plan's own §0
item 5 asked for exactly this), `64:ff9b::7f00:1` (RFC 6052 NAT64 well-known prefix, which
translates to `127.0.0.1` on any NAT64 network), `240.0.0.1` (reserved 240/4), `192.88.99.1`
(6to4 relay anycast).

**Verdict: CONFIRMED, three ways.** The one claim that HELD: `IsPrivate()` does cover
`fc00::/7` (`fc00::1`/`fd00::1` → `denyNoTo4 == true`) as the ADR says, and
`169.254.169.254` needs no separate rule — `IsLinkLocalUnicast()` catches it. Those two
sub-claims are correct.

**Proposed fix (concrete):**
1. **Delete the `To4()` sentence.** Do not normalise by `To4()`. Normalise with
   `netip.Addr` and `.Unmap()`, which maps `::ffff:127.0.0.1` → `127.0.0.1` and leaves a real
   IPv6 address intact:
   ```go
   a, ok := netip.AddrFromSlice(ip)
   if !ok { return errRefused }        // fail CLOSED on an unparseable address
   a = a.Unmap()
   ```
2. **Stop calling it "the property".** Write the rule as one deny predicate and say the
   enumeration *is* the rule:
   ```go
   if a.IsLoopback() || a.IsUnspecified() || a.IsMulticast() ||
      a.IsLinkLocalUnicast() || a.IsPrivate() || !a.IsValid() { deny }
   ```
   `IsMulticast()` subsumes both multicast helpers and closes SSDP/broadcast;
   `netip.Addr.IsPrivate()` keeps RFC1918 + ULA.
3. **Extend the explicit prefix list** to `100.64.0.0/10`, `192.0.0.0/24`, `198.18.0.0/15`,
   **`240.0.0.0/4`**, **`255.255.255.255/32`**, **`192.88.99.0/24`**, **`::/96`** (IPv4-compatible)
   and **`64:ff9b::/96`** (NAT64), all as `netip.Prefix` and all checked after `Unmap()`.
4. **Prescribe the tests as a table over this exact transcript**, and state the falsifier: a
   test that only feeds IPv4 literals cannot fail on defect (a) — which is precisely why the
   draft's "four prescribed tests" passed an implementation that let `0.0.0.0`,
   `::ffff:127.0.0.1` and `100.64.0.1` through. The pin must include `::1`, `fe80::1`,
   `fd00::1`, `::127.0.0.1` and `64:ff9b::7f00:1`.

---
## E5 — MAJOR — `c.BodyRaw()` HOLDS as wire bytes, but D1's cap is therefore ~4× weaker on fiber than on stdlib/gin, and that divergence is not the one the ADR records

**Claim attacked (verbatim), ADR D1:**

> "`c.BodyRaw()` (`req.go:92-96`) is the un-decompressed wire body with no response side effect,
> and it is what makes the three adapters agree: `net/http` does not auto-decompress request
> bodies, so stdlib and gin already see wire bytes."

**Probe** — throwaway `transport/http/fiber/zzprobe_exec_test.go` (deleted after), a real
`fiberv3.New()` with `app.Group("/api")` — a **mounted route group**, the shape ADR-0095 requires
— driven through `app.Test`:

```
=== RUN   TestProbeFiberBodyRawWireBytes
    plain payload=65544 bytes, gzip=111 bytes
       BodyRaw=111  then Body=65544
    gzip: BodyRaw() FIRST              -> status=200
       Body=65544   then BodyRaw=111
    gzip: Body() FIRST then BodyRaw()  -> status=200
       Body=65544   then BodyRaw=111
    gzip+identity: Body() FIRST        -> status=200
       BodyRaw=65544
    plain: BodyRaw()                   -> status=200
--- PASS
```

**What HELD (do not re-litigate):** `BodyRaw()` returns wire bytes (111) where `Body()` returns
65 544; it is reachable from a mounted `app.Group`; it has **no response side effect** (status
200 in every leg); and — a case the ADR does not claim and I checked anyway — it is
**order-independent**, still returning 111 after `c.Body()` has already decompressed, and even
with a two-encoding header (`gzip, identity`) which is the branch where fiber's
`tryDecodeBodyInOrder` calls `request.SetBodyRaw(body)` (`req.go:135`). Source chain confirmed:
`BodyRaw()` → `getBody()` (`req.go:1255`) → `fasthttp.Request.Body()`, no decode step.

**What does NOT hold: the conclusion drawn from it.** "It is what makes the three adapters
agree" is true about the *measured quantity* and false about the *protection*:

| adapter | mechanism | bytes the JSON decoder can see under `MaxBodyBytes = 1 MiB` |
|---|---|---|
| stdlib | `http.MaxBytesReader` on the read | ≤ 1 MiB — the reader itself fails |
| gin | same wrapper before `ShouldBindJSON` | ≤ 1 MiB |
| **fiber** | `len(c.BodyRaw())` pre-check, then `c.Bind().JSON` decompresses | **up to `fiber.Config.BodyLimit` = 4 MiB** |

This run measured a **590×** amplification ratio (111 → 65 544) on an ordinary gzip of repetitive
JSON, so reaching fiber's 4 MiB decompression ceiling from a ~7 KiB wire body is trivial. The
1 MiB cap therefore bounds *the wire* on all three but bounds *the decoded working set* on only
two.

**Verdict: PARTLY REFUTED** — the mechanism claim is sound and I confirm it; the "makes the three
adapters agree" claim over-reaches. The ADR does record a fiber divergence, but it records the
**wrong one**: the residual it names is bodies *above* `DefaultBodyLimit` (which fasthttp rejects
before the group), not bodies *between* `MaxBodyBytes` and `DefaultBodyLimit` (which the pre-check
admits and the decoder then expands). The bullet
*"A **second**, separately-named check on the decompressed size is deliberately **out of scope**"*
states the omission but attributes it to a ceiling that is 4× the default cap, without saying
that the gap between them is exactly the unprotected window.

**Proposed fix (concrete):** keep `BodyRaw()`. Add one sentence to D1's residual and one to
`SECURITY.md`: *"on fiber, `MaxBodyBytes` bounds wire bytes only; a compressed body may expand to
`fiber.Config.BodyLimit` (default 4 MiB) before decoding, so the decoded working set on fiber is
bounded by fiber's config, not by ours."* Optionally make the fiber mount WARN — which D1 already
prescribes for `MaxBodyBytes > DefaultBodyLimit` — also fire when `MaxBodyBytes < DefaultBodyLimit`,
since that is the configuration in which the gap exists (and it is the **default** one: 1 MiB < 4 MiB).
Add a per-adapter test with the falsifier stated: *a gzip body whose wire size is under the cap and
whose decompressed size is over it must be rejected on stdlib and gin, and is expected to be
**accepted** on fiber* — today no prescribed test distinguishes the three.

---
## E6 — CRITICAL — D4's default (shallow copy, no hook) does NOT fix the aliasing defect for nested values, and the bundle's own Evidence §3 says so one page earlier

**Claims attacked (verbatim), ADR D4:**

> "⚠ **The deep copy is taken ONLY when a hook is configured.** The default path — no
> `RedactVariables` — takes the shallow copy, **which is all the aliasing defect needs.**"

> "The **default** (no hook configured) is therefore the shallow copy, **which fixes the
> aliasing defect at `view.go:31`** whether or not a consumer redacts anything and restores the
> repo's clone-on-escape convention."

> "A shallow copy **was sufficient for the *aliasing* defect** and is insufficient for the *hook*."

**Why it is load-bearing.** It is the sentence that lets the hot read path keep its cheap copy.
It is also the plan's own §0 item 13, bullet 2: *"Is the shallow default still sufficient for
the aliasing defect in every case — including a nested value a consumer mutates without a hook?"*

**Probe** — throwaway `transport/http/httpcore/zzprobe_exec_test.go` (deleted after), running the
**repo's own** `engine.InstanceState.Clone()` (the read path's existing clone) plus D4's proposed
default shallow copy at the view boundary, then mutating the response view the way a consumer
would:

```
=== RUN   TestProbeShallowDefaultInsufficient
    TOP-LEVEL   delete -> live still has tags? true
    NESTED      delete -> live still has applicant.ssn? false
    NESTED      write  -> live applicant.name = "REDACTED"
    live now: map[string]interface {}{"applicant":map[string]interface {}{"name":"REDACTED"}, "tags":[]interface {}{"a"}}
--- PASS
```

**Observed:** with **two** shallow copies stacked (the existing `State.Clone()` *and* D4's new
one), a nested `delete` still removes the key from the live state, and a nested **write** still
overwrites it. The default path leaves the aliasing defect fully intact one level down —
identical to the split the bundle's own `docs/specs/2026-08-21-adr-0186-premise-evidence.md` §3
recorded (*"after nested delete on the CLONE, SOURCE applicant = map[…]{\"name\":\"ada\"}"*).

**Verdict: CONFIRMED — the ADR contradicts its own evidence file.** Evidence §3 established that
`copyVars = maps.Clone` is shallow and that nested mutation propagates; the ADR then asserts
three times that a shallow copy "is all the aliasing defect needs". Those cannot both be true.
The defect at `view.go:31` is *aliasing of the caller's map*, and it exists at every depth; the
hook is only the most *likely* mutator, not the only one. A consumer's own `InstanceMapper`, a
middleware that scrubs a field, or a test helper reaches the same nested map with no
`RedactVariables` configured at all.

There is a second, independent problem: **the default shallow copy is very nearly a no-op.** The
value reaching `NewInstanceView` has already been through `State.Clone()` on the read path
(`persistence/caching_instance_store.go:73`, `cloneInstanceEntry`), so D4's new top-level clone
duplicates work already done and adds protection only against a mutation that the upstream clone
already prevented. The cost argument for keeping it shallow therefore buys almost nothing.

**Proposed fix (concrete) — pick one and say which:**
1. **Make the deep copy unconditional and measure it.** The ADR's stated reason for the
   conditional is *"a recursive copy of every response's variable map is a real cost that nothing
   in this bundle has measured"* — that is an `ASSUMPTION (unverified)` presented as a design
   constraint. Measure it (a benchmark over a realistic variable map belongs in the plan's
   phase 3), then decide. If it is cheap, the conditional disappears and with it the whole
   interaction.
2. **Or keep the conditional and restate the guarantee honestly**: *"without a hook, the response
   map aliases nested values of the live instance state; the deep copy is taken only when
   `RedactVariables` is configured."* Then `SECURITY.md` must say a consumer must not mutate the
   returned view, and D4 must stop claiming the aliasing defect is fixed by default — backlog 54
   would then close for *redaction* but not for *aliasing*.
3. Either way, the prescribed test must assert the **nested** case with a fixture that actually
   contains a nested map. A test whose fixture is `map[string]any{"a": 1}` cannot fail on this
   defect — and that is the fixture shape the plan's test text implies today. State the falsifier
   explicitly: *"delete a key from `view.Variables["applicant"]` and assert the source still has
   it"* fails RED against a shallow copy and passes against a deep one.

---
## E7 — REFUTED (the claim HOLDS) — `keywordLocation` is value-free across eleven further schema shapes, including the four the plan asked for

**Claim attacked (verbatim), ADR D5 / Evidence §2:**

> "| `keywordLocation` | **value-free in every shape tried** — `/properties/ssn/pattern`,
> `/additionalProperties/type`, `/propertyNames/maxLength`, `/properties/items/items/type`. It is
> a JSON pointer into the **schema**, which is author-supplied |"

Plan §0 item 4 asked for a fifth leaking shape: *"`patternProperties`, `$ref`, `$dynamicRef`,
`unevaluatedProperties`, a schema whose own text embeds caller-supplied content."*

**Probe** — throwaway `definition/model/validate/jsonschema/zzprobe_exec_test.go` (deleted
after), through the **repo's own** `vjs.New(schema).NewValidator()` → `Validate` →
`errors.As(*jsonschema.ValidationError)` → `BasicOutput().Errors`:

```
### patternProperties      keywordLocation="/patternProperties/%5Esec_/type" instanceLocation="/sec_4111111111111111"
### $defs+$ref             keywordLocation="/properties/a/$ref/type"        instanceLocation="/a"
### unevaluatedProperties  keywordLocation="/unevaluatedProperties"          instanceLocation="/4111-1111-1111-1111"
### dependentSchemas       keywordLocation="/dependentSchemas/card/required" instanceLocation=""
### oneOf branches         keywordLocation="/oneOf" , "/oneOf/0/type" , "/oneOf/1/type"
### contains               keywordLocation="/properties/xs/contains" , "/properties/xs/contains/const"  instanceLocation="/xs/0"
### prefixItems            keywordLocation="/properties/xs/prefixItems/1/type" instanceLocation="/xs/1"
### additionalProperties   keywordLocation="/additionalProperties/maxLength"  instanceLocation="/4111-1111-1111-1111"
### enum                   keywordLocation="/properties/k/enum"               instanceLocation="/k"
### schema embeds a name   keywordLocation="/properties/ssn-of-4111111111111111/type"
### $id external-looking   keywordLocation="/properties/a/type"
```

**Verdict: the claim HOLDS.** In all eleven shapes `keywordLocation` is a pointer into the
author's schema and carries no submitted value; `instanceLocation` leaks in three of them
(`/sec_4111111111111111`, `/4111-1111-1111-1111` twice), reconfirming the ADR's reason for
dropping that column. Note that in the "schema embeds a name" shape `keywordLocation` echoes the
**author's** property name verbatim (`/properties/ssn-of-4111111111111111/type`) — that is
consistent with D4's stated position that author-supplied process metadata is disclosable, but
the ADR should say "**author**-derived by construction" rather than "value-free by
construction", because those are different properties and only the first is what was proven.

**Do not re-litigate this.** Fifteen schema shapes have now been executed against it.

---

## E8 — MAJOR — D5's stated ergonomics cost is wrong by a wide margin: the `keywordLocation`-only rendering is NON-ACTIONABLE for the single most common validation failure

**Claim attacked (verbatim), ADR D5:**

> "⚠ **Ergonomics cost, measured not asserted.** For a closed-`properties` schema
> `keywordLocation` still names the field *and* the constraint. For an **array** it loses the
> index: `/properties/items/items/type` does not say *which* item failed… **Accepted, and
> recorded as the price of value-freedom by construction.**"

**Probe** — throwaway `definition/model/validate/jsonschema/zzprobe2_exec_test.go` (deleted
after), an ordinary closed-`properties` object schema with `required`, rendered exactly as D5
prescribes (`keywordLocation` and nothing else):

```
=== RUN   TestProbeKeywordLocationActionability
### two required missing   -- D5 would render ONLY the keywordLocation column:
    D5 renders: /required                            (dropped: instanceLocation="" error="missing properties 'orderId', 'amount'")
### one required missing   -- D5 would render ONLY the keywordLocation column:
    D5 renders: /required                            (dropped: instanceLocation="" error="missing property 'orderId'")
### minimum violated   -- D5 would render ONLY the keywordLocation column:
    D5 renders: /properties/amount/minimum           (dropped: instanceLocation="/amount" error="minimum: got 0, want 1")
    D5 renders: /additionalProperties                (dropped: instanceLocation="" error="additional properties 'orderId' not allowed")
### unknown property   -- D5 would render ONLY the keywordLocation column:
    D5 renders: /additionalProperties                (dropped: instanceLocation="" error="additional properties 'orderId', 'ssn' not allowed")
--- PASS
```

**Observed:** for a **missing required property** — the most common 400 a schema produces —
D5's entire response body is the string `/required`. It does not say which property, and it is
identical whether one or five are missing. For an unknown property it is `/additionalProperties`,
again naming nothing. From E7's run, `/unevaluatedProperties`, `/oneOf` and
`/properties/xs/contains` are equally empty, and `/patternProperties/%5Esec_/type` is
percent-encoded and names the pattern rather than the offending key. So the cost is not "loses
the array index" — for at least six common keywords it **loses the field name entirely**.

**Verdict: CONFIRMED (the stated cost is a material understatement).** This matters beyond
ergonomics because the *same decision* spends two sections arguing that blanking 400 messages
would "silently retire three ADRs" whose whole point is an actionable 4xx. D5 then adopts a
rendering that is less actionable than static text for the commonest case: `"invalid input"` and
`/required` convey the same amount of information, and the first is honest about it.

**Proposed fix (concrete):** render `keywordLocation` **plus the property name(s) the keyword
names in the SCHEMA**, which are author-supplied and therefore admissible under the very
definition D5 uses:
- for `required` / `dependentRequired`, the missing names come from the *schema's* `required`
  array intersected with the absent instance keys — the names are author-declared, the fact of
  absence carries no submitted value. Render `missing required property: orderId`.
- for `additionalProperties` / `unevaluatedProperties`, the **rejected key is caller-supplied**
  and must stay out; render `unknown property (see schema additionalProperties)` and log the key.
- Keep everything derived from the submitted **value** (lengths, counts, allowed-enum listings,
  the vendor's text) out, as D5 already says.

And state the falsifier for the prescribed test: a fixture whose schema declares `required` and
whose input omits one property must produce a message naming that property; a fixture that only
exercises `type` on a declared property cannot fail on this defect — and that is the fixture
shape Evidence §2's four probes used.

---
## E9 — MAJOR — D5's "no caller value" claim for the two cursor sentinels is false: a forged cursor's JSON field name is echoed verbatim at 400

**Claim attacked (verbatim), ADR D5 exception table, row 1:**

> "| `kernel.ErrBadCursor`, `kernel.ErrBadArmedTimerCursor` | `err.Error()` | messages are
> `": not an instance cursor"` / `": cursor carries no start time"` — **no caller value** |"

**Probe** — throwaway `runtime/kernel/zzprobe_exec_test.go` (deleted after), feeding forged
cursors through the repo's own `kernel.DecodeCursor` / `kernel.DecodeArmedTimerCursor` and then
through the repo's own `httpcore.ClassifyError` — i.e. the exact 400 body a client receives:

```
=== RUN   TestProbeBadCursorEchoesCallerValue
    unknown field    -> 400 bad_request | workflow-runtime: malformed instance cursor: json: unknown field "leak_4111-1111-1111-1111"
    syntax error     -> 400 bad_request | workflow-runtime: malformed instance cursor: invalid character '-' after object key:value pair
    trailing data    -> 400 bad_request | workflow-runtime: malformed instance cursor: trailing data after cursor payload
    not base64       -> 400 bad_request | workflow-runtime: malformed instance cursor: illegal base64 data at input byte 0
    wrong type       -> 400 bad_request | workflow-runtime: malformed instance cursor: json: cannot unmarshal object into Go struct field cursorPayload.kind of type string
    [timer] unknown field -> 400 bad_request | workflow-runtime: malformed armed-timer cursor: json: unknown field "leak_4111-1111-1111-1111"
    ... (identical shape for all five on the armed-timer family)
--- PASS
```

**Observed:** the caller's own forged field name is reflected verbatim into the response body,
on **both** sentinels. The mechanism is `runtime/kernel/cursorcodec.go:37-47` — `decodeCursorInto`
returns the raw `json.Decoder` error (with `DisallowUnknownFields`, which is what puts the field
name in the message), and `lister.go:66` / `armed_timer_paging.go:89` wrap it with `%w: %w`.

**Verdict: CONFIRMED, and it is also a counting failure.** The D5 row enumerates **two** messages
for these sentinels; there are **seven** producing sites (`lister.go:66,69,77,90` and
`armed_timer_paging.go:89,92,99`), and the two the row names are the two that happen to be
value-free. The row's evidence was drawn from the wrap sites with literal format strings and
missed the `%w: %w` pass-through — the same *net* failure as E3.

**Severity note (honest):** the reflected string is the caller's own input, so this is not a
cross-tenant disclosure. It matters for two reasons: (1) D5's rule is *"renders `err.Error()`
only for sources whose message is **provably value-free by construction**"* and this source is
not, so a pin test written from the ADR's table would enshrine a false premise; (2) reflected
caller content in a JSON error body is an XSS vector for any consumer that renders
`body.message` into HTML — the library cannot know that they do not.

**Proposed fix (concrete):** keep the two sentinels in the exception list but render them from
the **wrap-site literal only**, not from the pass-through. Concretely, have `decodeCursorInto`
return a bare `kernel.ErrMalformedCursorPayload` (value-free) and log/keep the underlying
decoder error via `errors.Join` or a private wrapper that `Error()` does not print — or, simpler,
have `DecodeCursor`/`DecodeArmedTimerCursor` at `lister.go:66` and `armed_timer_paging.go:89`
render `fmt.Errorf("%w: malformed cursor payload", ErrBadCursor)` and drop `%w: %w`. The
underlying error goes to the operator log under D5's existing 400 logging row. Assign this to a
phase — today the plan's phase table has no row touching `runtime/kernel`.

State the falsifier for the pin test: *a cursor of `base64('{"unexpected":1}')` must not produce
a 400 body containing the string `unexpected`*. A pin that only feeds `"not-base64"` cannot fail
on this defect, and `"not-base64"` is the fixture the current tests use.

---
## E10 — MAJOR — the plan's two "machine-checked invariants" are not machine-checkable as prescribed, and the repo already contains the pattern that would make them so

**Claims attacked (verbatim), plan phase 3:**

> "3. ⭐ `TestFourHundredArmRenderingIsPinned` — one row per sentinel in the arm, asserting the
> exact rendering, **plus a machine-checked invariant**: the set of sentinels matching the 400
> arm equals the enumerated set. … ⚠ **The invariant is the point.** A new sentinel added to the
> arm without a row must **fail the test**"

> "8. ⭐ `TestRedactionCoversAllElevenReadPaths` … ⚠ **Plus the count invariant:** assert that the
> number of `NewInstanceView`/`mapInstance` call sites routed through the helper equals the
> number that exist."

And ADR D5: *"⚠ **The pin test is a machine-checked invariant, not a list in prose.**"*

**Why both fail as written.** Neither quantity is observable at run time:

- **"the set of sentinels matching the 400 arm"** — `errors.Is` answers *"does this error match?"*,
  never *"which sentinels would match?"*. There is no reflective enumeration of a `switch`'s
  `case` list. A test that ranges over the ADR's own table and asserts each row's status is
  asserting the **enumeration against itself**: adding a ninth sentinel to `errors.go` changes
  neither side, so it passes. The prescription "prove it by adding one in a mutation" proves the
  *mutation*, not the invariant — and a mutation is not shipped, so nothing guards the arm
  afterwards. This is the same class as the six tests that could not fail in the ADR-0162/0163
  delivery.
- **"the number that exist"** — a runtime test cannot count `NewInstanceView(` call sites. If the
  number is a literal on both sides, adding a twelfth path changes neither and the test passes;
  the enumeration rots for the **third** time, in the very artifact introduced to stop it rotting.

**Probe / precedent** — the repo already solves exactly this, twice, and neither the ADR nor the
plan cites it:

```
$ grep -rln "go/ast\|go/parser\|packages.Load" --include='*_test.go' .
runtime/monitor/internal_leak_test.go
scheduler/selfcontainment_guard_test.go
engine/purity_test.go
engine/terminal_sites_test.go
engine/state_recent_compensation_cmd_ids_test.go
```

`engine/terminal_sites_test.go` was written for **this exact failure** and says so in its header:

> "ADR-0164 established that every terminal transition routes through
> `InstanceState.endInstance`, and recorded the fact as a **COUNT**: 'all eight sites route
> through it'. The count was 8 when written and **is 10 today** … A number in prose rots. The
> invariant does not, so this file pins the invariant instead… ⚠ **HONEST FRAMING: this is a
> PIN, not a red-green fix.** It passes the moment it is written… Mutation-verified."

**Verdict: CONFIRMED.** Both prescriptions are labelled "machine-checked" and, as specified,
are prose assertions in Go syntax.

**Proposed fix (concrete):** write both as `go/parser` source invariants, modelled on
`engine/terminal_sites_test.go`, and re-state them as **properties, not counts** — which is that
file's whole lesson:

1. **400 arm:** parse `transport/http/httpcore/errors.go`, walk the `ClassifyError` switch, collect
   every `errors.Is(err, X)` selector in the 400 `case`, and assert that set equals the rendering
   table's key set. A new `case` operand with no table row fails at parse time. (Bonus: the same
   walk pins D5's *arm ordering* claim — assert the 413 case precedes the 400 case — which the
   plan currently prescribes only as a **comment**, row "D5 arm-ordering invariant comment".)
2. **Read paths:** parse `transport/http/httpcore/*.go` (non-test), collect every call to
   `NewInstanceView` and `mapInstance`, and assert each occurs **inside** the redaction helper or
   passes the policy parameter — i.e. pin the *property* "no projection of `InstanceState`
   reaches a response without going through the policy", not the *number* 11. Then the twelfth
   path fails the test the day it is added, which the number never will.
3. Add the honest-framing note both tests need: **they pass the moment they are written**, so each
   must be **mutation-verified** (plant a ninth sentinel / a twelfth call site, observe RED,
   restore, `diff`) and the observed RED recorded in the delivery's evidence file. Without that
   step these are two more tests that cannot fail.

---

## E11 — MAJOR — `TestNoHookConfiguredTakesTheShallowCopy` has an unachievable falsifier; the only way to write it is to PIN the defect E6 identifies

**Claim attacked (verbatim), plan phase 3, test 6's control:**

> "⚠ **Plus the control for the conditional copy:** `TestNoHookConfiguredTakesTheShallowCopy` —
> with `RedactVariables` nil, the response still does not alias `st.Variables` (test 5) and **no
> recursive copy is taken**. **Falsifier:** *it fails against an implementation that deep-copies
> unconditionally* — which is the hot-path regression ADR D4 avoids."

**Attack.** Take the two stated assertions in turn:

- *"the response still does not alias `st.Variables`"* — an unconditional deep copy satisfies
  this too. Cannot discriminate.
- *"no recursive copy is taken"* — there is no black-box observation of "a recursive copy was not
  taken" **except** observing that a nested value still aliases: mutate `view.Variables["applicant"]["ssn"]`
  and assert the source **changed**. That is a test whose green condition is the presence of the
  nested-aliasing defect.

So the stated falsifier (*"it fails against an implementation that deep-copies unconditionally"*)
is reachable **only** by asserting the defect. Executed in E6 above: with the default shallow
copy, a nested delete removes the key from the live state and a nested write overwrites it
(`live applicant.name = "REDACTED"`). A test that goes green on that is pinning a data-corruption
path into the suite as a requirement.

The alternative reading — assert it by benchmark/allocation count (`testing.AllocsPerRun`) — is
not what the plan says, would be flaky against map iteration, and still would not fail for the
reason the plan names.

**Verdict: CONFIRMED.** As written the test is either vacuous (if it asserts only non-aliasing) or
harmful (if it asserts nested aliasing). It is also the plan's own §0 item 13 bullet 2, marked
"resolved in the documents" — it is not.

**Proposed fix (concrete):** delete this control and resolve E6 first. If D4 keeps the conditional
copy, the honest control is a **benchmark**, not a test: `BenchmarkInstanceViewNoHook` with an
allocation budget, plus an explicit comment that the no-hook path deliberately aliases nested
values and that `SECURITY.md` says so. If D4 makes the deep copy unconditional (E6 fix 1), the
control disappears entirely and test 5 and test 6 collapse into one table with a nested fixture —
which is simpler and removes the interaction.

---
## E12 — CRITICAL — D3 never mentions `Transport.Proxy`, and the omission cuts both ways: the restricted transport as described silently drops proxy support for every existing user, and the escape hatch that restores it disables the whole SSRF protection

**Claim attacked (verbatim), ADR D3:**

> "- **When a URL is expression-derived** (`urlExprProg != nil`, decidable at construction), the
>   action **installs a restricted client**. Two checks, each where its data exists:
>   - **IP deny-list, in `net.Dialer.Control`.** ⚠ Executed: `Control` receives only the resolved
>     `network, address` … That makes it the right place for an **IP** rule (it sees every
>     resolved address, so DNS rebinding cannot bypass it)"

And ADR Context §3, on today's behaviour:

> "The default client is `&http.Client{Timeout: 30s}` with no `CheckRedirect` and no allowlist"

**Why load-bearing.** "It sees every resolved address" is the sentence that makes
`net.Dialer.Control` the correct home for the rule. A forward proxy breaks the identity between
*the address dialled* and *the address requested*, and D3 does not mention proxies anywhere.

**Probe** — throwaway Go program (`/tmp/proxyprobe`, deleted after) building the restricted client
exactly two ways: (a) `http.DefaultTransport.Clone()` + our `Control` hook — the shape a consumer
gets today, since `&http.Client{Timeout: 30s}` uses `http.DefaultTransport`; and (b) a bare
`&http.Transport{DialContext: …}` — the shape "install a restricted client" most naturally
produces. `HTTP_PROXY` points at a stand-in proxy, and the target is the AWS IMDS address:

```
http.DefaultTransport.Proxy == nil? false

--- WITH HTTP_PROXY set (a restricted transport cloned from DefaultTransport) ---
  request error: Get "http://169.254.169.254/latest/api/token": proxyconnect tcp: dial tcp 127.0.0.1:50140: refused by IP deny-list: 127.0.0.1:50140
  Control hook saw: [tcp4 127.0.0.1:50140]

--- WITHOUT a proxy (Transport{} with no Proxy field) ---
  request error: Get "http://169.254.169.254/latest/api/token": dial tcp 169.254.169.254:80: refused by IP deny-list: 169.254.169.254:80
  Control hook saw: [tcp4 169.254.169.254:80]
```

**Observed — three consequences, and the good news first:**

1. **There is NO proxy bypass.** With a proxy configured, `Control` fired on the *proxy's*
   address, not the target's — so the guard does not silently wave the request through. That is
   the one thing that could have been catastrophic and it is not. Recorded so nobody re-derives it.
2. ⚠ **The guard refuses the useful case.** `Control saw: tcp4 127.0.0.1:50140` and refused it.
   Corporate forward proxies live on loopback (a sidecar) or on RFC1918 addresses — both are in
   D3's deny set. So in **any** deployment with `HTTP_PROXY`/`HTTPS_PROXY` set, **every**
   `httpcall` node with `WithURLExpr` fails at the dial, for every destination, including
   perfectly legitimate public ones. The failure message names the proxy, not the target, so the
   operator's first hypothesis will be wrong.
3. ⚠⚠ **And the escape hatch turns the protection off completely.** The only lever D3 offers for
   (2) is `WithAllowedCIDRs` covering the proxy's address. Exempting the proxy from the IP gate
   means the guard now approves a connection to a host that will fetch **any** URL on the
   operator's behalf — including `http://169.254.169.254/`. The IP deny-list becomes decorative:
   `Control` only ever sees the proxy, which is exempt. `WithAllowedHosts` does not rescue this
   either, since it filters the request hostname and the ADR states plainly that CIDRs exempt
   "from the **IP** gate only".
   ⇒ In one configuration the consumer *"reasonably expects access and gets none"* **and**
   *"expects a block and gets access"* — both halves of the plan's own §0 item 13 bullet 3, which
   the plan marks "resolved in the documents".
4. ⚠ **Silent behaviour change.** `http.DefaultTransport.Proxy == nil? false` — today's client
   honours `HTTP_PROXY`. A restricted transport written as `&http.Transport{DialContext: …}`
   (leg b) has `Proxy == nil` and **stops honouring it**, so an existing consumer's egress
   suddenly bypasses their proxy and hits the network directly. That is a connectivity and
   compliance regression introduced by a security fix, and no sentence in D3, its Consequences,
   or the plan mentions `Proxy`.

**Verdict: CONFIRMED (bypass REFUTED, usability + escape-hatch defect CONFIRMED).**

**Proposed fix (concrete):**
1. **State the proxy posture in D3.** Build the restricted transport with
   `http.DefaultTransport.Clone()` so `Proxy: ProxyFromEnvironment` is preserved — never a bare
   `&http.Transport{}`. Add it to the plan's phase row for `action/httpcall`.
2. **Detect the proxy case and refuse to guess.** At construction, resolve the proxy for a
   representative URL (`tr.Proxy(req)`); if a proxy is configured **and** `WithURLExpr` is set
   **and** neither `WithAllowedHosts` nor `WithUnrestrictedTransport()` is present, return the
   same non-retryable construction error D3 already mints for the `WithHTTPClient` collision,
   naming the proxy and the two options. Failing loudly at construction beats failing at every
   dial with a message that names the wrong host.
3. **Make `WithAllowedHosts` the required control in the proxied case, and say why**: when a proxy
   is in play the IP gate is structurally blind, so the *host* gate is the only real filter.
   `WithAllowedCIDRs` covering a proxy must be documented as **equivalent to
   `WithUnrestrictedTransport()`** — because it is.
4. Add the falsifier to the prescribed tests: *with `HTTP_PROXY` set, a request to an
   allow-listed public host must succeed, and a request to `169.254.169.254` must be refused.*
   Every test in D3's current prescription runs without a proxy and therefore cannot fail on any
   of this.

---
## E13 — MINOR — the four remaining 400 sentinels render as D5 says (claim HOLDS), but `ErrEmptyReassignTarget` has TWO producers with different format strings and the row names only one

**Claim attacked (verbatim), ADR D5 exception table:**

> "| `engine.ErrEmptyTriggerKey`, `engine.ErrEmptyReassignTarget` | `err.Error()` | `"%w: %T.%s"` — names the **field the caller must fill**. ADR-0152/0183 rationale, in the switch being edited |"
> "| `engine.ErrInvalidOutcome` | **reshaped**: `node %q: outcome not declared` | it is the one non-validation sentinel echoing a caller value (`"%w: node %q outcome %q"`) |"
> "| `engine.ErrOutcomeRequired` | `err.Error()` | `"%w: node %q"` — a definition node id, ADR-0146 rationale |"

**Probe** — throwaway `transport/http/httpcore/zzprobe3_exec_test.go` (deleted after),
reproducing each producer's literal format string with hostile inputs and passing the result
through the repo's own `httpcore.ClassifyError`:

```
=== RUN   TestProbeRemainingFourHundredSentinels
    engine/trigger_validate.go:177 ErrEmptyTriggerKey          -> 400 bad_request  | workflow-engine: trigger identity key is empty: engine.HumanReassigned.TaskID
    engine/step.go:163 ErrEmptyReassignTarget                  -> 400 bad_request  | workflow-engine: reassignment target is empty: engine.HumanReassigned.To
    runtime/task/service.go:221 ErrEmptyReassignTarget         -> 400 bad_request  | workflow-engine: reassignment target is empty: TaskService.Reassign to
    engine/step_triggers.go:932 ErrOutcomeRequired             -> 400 bad_request  | workflow-engine: user task requires a completion outcome: node "approve-4111-1111-1111-1111"
    engine/step_triggers.go:934 ErrInvalidOutcome              -> 400 bad_request  | workflow-engine: completion outcome is not declared by the user task: node "approve" outcome "123-45-6789"
--- PASS
```

**Verdict: the security claims HOLD; one enumeration is short.**
- `ErrEmptyTriggerKey` and both `ErrEmptyReassignTarget` producers are **value-free** — they emit
  a Go type name and a struct field name, never a submitted value. Confirmed.
- `ErrOutcomeRequired` echoes only the **definition** node id (author-supplied). Confirmed, and
  the probe deliberately used a node id shaped like a card number to show that whatever leaks
  there is the author's own string.
- `ErrInvalidOutcome` echoes the caller's outcome verbatim (`outcome "123-45-6789"`). Confirmed —
  D5's reshape is correct and necessary.

**The short enumeration:** the row's format string `"%w: %T.%s"` covers
`engine/trigger_validate.go:177` and `engine/step.go:163` (which is actually `"%w: %T.To"`, a
third literal), but **not** `runtime/task/service.go:221`, whose message is
`"%w: TaskService.Reassign to"`. Three literals, one quoted. No disclosure consequence, but the
plan's pin test asserts "the exact rendering" per sentinel — with two producers emitting
different text for `ErrEmptyReassignTarget`, a single-row assertion will pin whichever the
fixture happens to hit and will not notice the other changing.

**Proposed fix:** in D5's table, replace the single format string with "three producers, all
value-free: `engine/trigger_validate.go:177`, `engine/step.go:163`, `runtime/task/service.go:221`",
and make the pin test's row for `ErrEmptyReassignTarget` a two-row sub-table naming both
producers.

---

# Ranked index

| ID | Sev | One-line | Verdict |
|---|---|---|---|
| **E1** | Critical | D2's admission seam is not closed: `runtime.ProcessDriver` exports four more caller-supplied variable-map entry points (`Drive`, `BroadcastSignal`, `DeliverMessage`, `ApplyTrigger`), and `BroadcastSignal` has **no** `service` equivalent — a consumer embedding the driver (the product's primary shape) gets zero bound | CONFIRMED |
| **E4** | Critical | D3's IP rule **fails open for every IPv6 address** as written (`::1`, `fe80::1`, `fc00::1` all admitted, because `To4()` returns nil and nil-`net.IP` predicates are all false); the stated "property" (`not global unicast`) and its enumerated helper list deny **different sets in both directions** — the property alone admits all of RFC1918; and `::127.0.0.1`, `64:ff9b::7f00:1` (NAT64), `240.0.0.1`, `192.88.99.1` are covered by nothing | CONFIRMED |
| **E3** | Critical | D5 keeps `err.Error()` for `ErrBadInput` on a probe of `httpcore.Validate`, but the **36 decode wrap sites** are a different producer and echo caller values verbatim — the whole `def_ref` **twice**, and an out-of-range number literal | CONFIRMED |
| **E6** | Critical | D4's default (shallow copy, no hook) does **not** fix the aliasing defect for nested values — executed: a nested delete *and* a nested write both reach the live state through two stacked shallow copies. The ADR asserts the opposite three times, contradicting its own Evidence §3 | CONFIRMED |
| **E12** | Critical | D3 never mentions `Transport.Proxy`. No bypass (good), but with `HTTP_PROXY` set the guard refuses **every** `httpcall` at the proxy's own address, and the only escape hatch (`WithAllowedCIDRs` on the proxy) makes the IP deny-list decorative — both halves of the plan's own §0 item 13 bullet 3, in one configuration. Plus a silent proxy-support regression if the transport is built bare | CONFIRMED (bypass REFUTED) |
| **E2** | Critical | `authz.Actor.Attributes` is a second caller-supplied `map[string]any` carried by **four** `service` request types, sits in the ABAC env next to `vars`, is cost-identical on the O(n²) axis (measured 20/77/304 ms vs `vars`' 20/76/304 ms), and D2 does not bound it — while the ADR claims "both ABAC evaluators" inherit the bound | CONFIRMED |
| **E9** | Major | D5's "no caller value" claim for `kernel.ErrBadCursor`/`ErrBadArmedTimerCursor` is false: a forged cursor's JSON field name is reflected verbatim at 400 (`json: unknown field "leak_4111-1111-1111-1111"`). The row enumerates 2 of **7** producing sites | CONFIRMED |
| **E10** | Major | Both of the plan's "machine-checked invariants" (the 400-arm sentinel pin, the eleven-read-path count) are **not machine-checkable as prescribed** — neither quantity is observable at run time. The repo already contains the `go/parser` pattern that solves it (`engine/terminal_sites_test.go`, written for this exact failure) | CONFIRMED |
| **E11** | Major | `TestNoHookConfiguredTakesTheShallowCopy` has an **unachievable falsifier**: the only way to fail against an unconditional deep copy is to assert that nested aliasing *still happens* — i.e. to pin the E6 defect as a requirement | CONFIRMED |
| **E8** | Major | D5's stated ergonomics cost ("loses the array index") understates it badly: for a **missing required property** — the commonest 400 a schema produces — the entire rendering is the string `/required`, naming nothing; same for `/additionalProperties`, `/unevaluatedProperties`, `/oneOf`, `/properties/xs/contains` | CONFIRMED |
| **E5** | Major | `c.BodyRaw()` HOLDS as wire bytes, but the conclusion "it is what makes the three adapters agree" over-reaches: under `MaxBodyBytes = 1 MiB` the decoder sees ≤ 1 MiB on stdlib/gin and up to **4 MiB** on fiber. Measured amplification 111 → 65 544 bytes (590×). The ADR records the *wrong* fiber divergence | PARTLY REFUTED |
| **E13** | Minor | The four remaining 400 sentinels render exactly as D5 says (security claims HOLD), but `ErrEmptyReassignTarget` has **three** producing literals across two packages and the row quotes one | CONFIRMED (minor) |
| **E7** | — | `keywordLocation` is value-free across **eleven further** schema shapes including all four the plan asked for. The claim HOLDS; it should be worded "**author**-derived by construction" | REFUTED (claim holds) |

**Severity roll-up: 6 Critical, 5 Major, 1 Minor, 1 refutation-in-the-bundle's-favour.**

---

# What HELD — do not re-litigate

Executed in this pass and confirmed. Re-deriving these is wasted budget.

1. **`keywordLocation` is value-free** (E7). Fifteen schema shapes total have now been executed
   against it — the bundle's four plus eleven more (`patternProperties`, `$defs`+`$ref`,
   `unevaluatedProperties`, `dependentSchemas`, `oneOf`, `contains`, `prefixItems`,
   `additionalProperties`-as-schema, `enum`, a schema whose own text embeds a card-shaped name, and
   an external-looking `$id`). It never carried a submitted value. `instanceLocation` leaked in
   three of the eleven, reconfirming the ADR's reason for dropping that column.
2. **`c.BodyRaw()` is the un-decompressed wire body, has no response side effect, and is reachable
   from a mounted `app.Group`** (E5). Measured 111 (wire) vs 65 544 (decompressed) on the same
   request; status 200 in every leg. Additionally — a case the ADR does not claim — it is
   **order-independent**: still 111 after `c.Body()` has already run, and still 111 with a
   two-encoding header (`gzip, identity`), the branch where fiber calls `request.SetBodyRaw`.
3. **`*http.MaxBytesError` is surfaced BARE by both stdlib and gin**, exactly as D1 says.
   Probe (`transport/http/gin/zzprobe_exec_test.go`, deleted after):
   ```
   stdlib: err=http: request body too large | errors.As(*MaxBytesError)=true | type=*http.MaxBytesError
   stdlib: ClassifyError(bare)=500 internal_error ""
   gin:    err=http: request body too large | errors.As(*MaxBytesError)=true | type=*http.MaxBytesError
   ```
   The "two shapes, not three" claim and the "falls to the 500 default today" claim both hold.
4. **`net.Dialer.Control` is NOT bypassed by a configured proxy** (E12 point 1) — it fires on the
   proxy's own address. The catastrophic reading of E12 is refuted; what remains is a usability
   and escape-hatch defect.
5. **`IsPrivate()` does cover `fc00::/7`** and `169.254.169.254` needs no separate rule
   (`IsLinkLocalUnicast()` catches it) — both sub-claims of D3 confirmed (E4).
6. **`ErrEmptyTriggerKey`, both `ErrEmptyReassignTarget` producers, and `ErrOutcomeRequired` are
   value-free; `ErrInvalidOutcome` is not** — exactly as D5's table states (E13). D5's reshape of
   `ErrInvalidOutcome` is correct and necessary.
7. **The ABAC env is `{"actor": actor, "vars": vars}`** (`authz/authz.go:130-134`) and
   `RoleAuthorizer` is the surface — confirming the ADR's Context §2 claim that `authz`'s
   evaluator is `expreval.New()` with `DefaultTimeout` enabled.

⚠ **A note on the ADR's O(n²) ladder.** I reproduced it incidentally at 20/77/304 ms for
n = 1000/2000/4000 (ADR quotes 25/98/391 ms) — same shape, same machine class. But the predicate
**must be non-short-circuiting**: my first attempt used a predicate whose inner `any()` matched
early and returned in **0 ms at n = 4000**. The ADR does not state its predicate. If the plan
prescribes a regression test around this cost, it must pin the predicate text, or the test will
silently measure nothing.

# Unexecuted / boundary

- `ASSUMPTION (unverified)`: everything about `service.ErrVariablesTooLarge`, `MaxBodyBytes` and
  `ErrRequestBodyTooLarge` reaching a **413** — these symbols do not exist yet, so the arm
  ordering could only be reasoned about, not run. E13's and E9's findings about the 400 arm are
  executed; the 413 arm is not.
- `ASSUMPTION (unverified)`: the fiber `> DefaultBodyLimit` behaviour (spec §7 lists it as
  discharged; I did not re-derive it, per the brief).
- **Out of scope, stated rather than folded in:** who populates `authz.Actor` is ADR-0185's
  question. E2 stands on the library surface regardless of ADR-0185, and I have not used any
  symbol ADR-0185 introduces.

*End of EXECUTION lens report. All probe files deleted; worktree verified clean
(`git status --porcelain` empty).*

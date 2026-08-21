# ADR-0186 re-audit — INTERACTION lens

Bundle commit: `677760d5` (worktree `.../scratchpad/a0186-inter`)
Date: 2026-08-21
Scope: **ADR-0186 only** (untrusted input & disclosure). ADR-0185 identity work is a separate delivery — out of scope.

Method: all 15 D×D pairs derived; each decision against already-shipped behaviour; each
decision against itself; spec §5's 21-row pairwise table attacked row by row; plan §2 phase
parallelism checked for file/symbol/type collisions; new symbol names checked against existing ones.

Findings appended one at a time as confirmed.

---

## I-1 — CRITICAL — **D4 × itself (the conditional deep copy) × the shipped `persistence` cache**

**The pair.** D4's two bullets, written as though independent: (a) *"the deep copy is taken ONLY
when a hook is configured"* and (b) *"the default … takes the shallow copy, **which is all the
aliasing defect needs**"*. Cross this with the shipped read path: `persistence`'s
`cloneInstanceEntry` (`persistence/caching_instance_store.go:72`) is `e.State.Clone()`, and
`State.Clone()` → `cloneState` → `copyVars` = `maps.Clone` (**shallow**, the bundle's own
Evidence §3).

**The interaction.** The bundle treats "the aliasing defect" as a *one-level* property of
`view.go:31`. It is not. With the hook absent, the response map's **nested** values are still the
**live objects inside the cached `instanceEntry`** — the shallow copy D4 adds at `view.go` is a
*second* top-level clone of a map that `State.Clone()` had already isolated at the top level, and
it changes nothing below level 1. So the default path ships:

1. the exact hazard Evidence §3 documents (a nested mutation reaching the source), for **every
   consumer that configures no hook** — including every `InstanceMapper` consumer, the seam
   CLAUDE.md lists as a product feature, whose whole job is to walk and reshape the state; and
2. a **concurrency** dimension no document in the bundle mentions: `CachingInstanceStore` serves
   the same entry to concurrent readers, so a consumer mutating a nested response value is
   racing the cache, not merely corrupting it.

D4 justifies the conditional on hot-path cost — a real concern — but the cost argument was
applied to the wrong question. It decides *when to pay for a recursive copy*; it does not
establish that the unpaid path is **safe**, and D4 asserts safety in the same bullet
("all the aliasing defect needs") without deriving it.

**Evidence (executed, worktree `a0186-inter` @ `677760d5`, `go test -count=1 -v ./zzprobe_inter/...`).**
The probe reproduces the shipped chain — cached entry → `State.Clone()` → `httpcore.NewInstanceView`
→ **D4's prescribed default shallow copy (`maps.Clone`)** → consumer mutates a nested value:

```
=== RUN   TestD4ShallowDefaultLeavesNestedAliasToTheCACHEDentry
CACHED entry after consumer mutation of the RESPONSE map: map[string]interface {}{"name":"MUTATED"}
top-level delete isolated? cached still has 'tags' = true
--- PASS
```

The consumer deleted `applicant.ssn` and rewrote `applicant.name` **through the response map with
no hook configured**, and the **cached entry** now reads `{"name":"MUTATED"}` — `ssn` gone, `name`
overwritten. The top-level control confirms the split is exactly nested-vs-top-level, i.e. the
extra shallow copy bought nothing that `State.Clone()` did not already provide.

**Verdict on spec §5.** Row **D4 × the read hot path** is marked ✅ on the strength of *"the
default path keeps the shallow copy, which is all the aliasing defect needs"*. That clause is
**false**, so the row is **NOT resolved** — it resolves the *cost* question and silently inherits
the *correctness* question. Row **D4 × itself** (✅, "the map handed to the hook is a JSON-shaped
deep copy") is resolved **only for the hook path** and its ✅ now over-reads: the same defect
survives one bullet away. The pair the table still omits is **D4 × `persistence`/the cache**: the
only mention of `persistence` anywhere in the bundle is a godoc fix.

**Proposed fix.** Pick one and state it:
- **(a) Preferred — make the default deep, and measure it.** The bundle has *no* measurement
  behind the hot-path claim ("an unmeasured cost", D4's own words). Phase 3 gets a benchmark of
  the recursive copy over a representative variable map **before** the conditional is designed
  in; if it is cheap (it is a JSON-shaped walk over a map already bounded to 256 KiB **on the
  caller axis**), the conditional is unjustified complexity guarding a correctness hole.
- **(b) If the conditional stays**, D4 must say plainly that **the no-hook response aliases
  nested cached state**, `InstanceView.Variables`' godoc must carry a "do not mutate" contract,
  and `SECURITY.md`/`STABILITY.md` must record it — because today's `cloneInstanceEntry` godoc
  promising a deep copy is the *only* place a consumer would look, and this bundle is already
  fixing that comment for being false.
- Either way, add the missing test: **`TestNoHookConfiguredDoesNotAliasNestedState`**. Plan phase
  3 test 6's control (`TestNoHookConfiguredTakesTheShallowCopy`) asserts the *opposite* — that no
  recursive copy is taken — so as written the plan **pins the defect in place**.

---

## I-2 — CRITICAL — **D5 × D5 (the gate's two mandates) × plan phase 2 test 5**

**The pair.** Two D5 bullets written as though independent, plus the plan test that pins one of
them:
- (a) *"The gate must **preserve the strategy's error (`%w`)**"* — ADR D5, and plan phase 2 test 5
  `TestStructuredErrorSurvivesTheGate`, asserted via `errors.As`, billed as *"the falsifier for
  the whole phase"*.
- (b) *"…**and render the client-safe message itself**"*, which `ClassifyError`'s 400 arm then
  emits — the D5 table's `validation.ErrInvalidInput` row reads *"what `runtime/validation`
  rendered"*, and `errors.go:50` renders `Message: err.Error()`.

**The interaction.** `fmt.Errorf` — the only mechanism either document names — **cannot satisfy
both**. `%w` on the typed error puts the vendor's text into `Error()`; dropping the vendor's text
from the format string drops the `%w` with it. So the two bullets are in direct contradiction, and
the contradiction resolves *against the delivery's purpose*: an implementer who follows the plan
literally makes test 5 pass and **re-ships the exact leak D5 exists to close**, with a green suite.

**Evidence (executed, `go test -count=1 -v -run TestGateCannot ./zzprobe_inter/...`).** Real
in-repo strategy `vjs.New(schema).NewValidator()`, real `httpcore.ClassifyError`:

```
FORM A  errors.As=true  status=400
FORM A  ClassifyError Message = "workflow-validation: invalid input: workflow-validation/jsonschema: jsonschema validation failed with 'mem://schema.json#'
- at '/ssn': '123-45-6789' does not match pattern '^[0-9]{3}$'"
FORM B  errors.As=false  status=400
FORM B  ClassifyError Message = "workflow-validation: invalid input: invalid input: /properties/ssn/pattern"
```

FORM A is `fmt.Errorf("%w: %w", ErrInvalidInput, typed)` — it **satisfies phase 2 test 5**
(`errors.As=true`) and the 400 body carries **`'123-45-6789'` verbatim**. FORM B is
`fmt.Errorf("%w: %s", ErrInvalidInput, safeRendering)` — value-free, and **phase 2 test 5 fails**
(`errors.As=false`). There is no third `fmt.Errorf` form.

⚠ Note FORM B's doubled prefix (`invalid input: invalid input:`) — a second, cosmetic sign that
the sentinel/rendering layering was never derived end to end.

**Verdict on spec §5.** No row covers this. **D4 × D5** (✅) and **D5 × D6** (✅) both treat D5's
rendering as a settled mechanism; the mechanism does not exist. The omitted pair is
**D5 × itself** — and D5 is the one decision the table never crosses with itself, despite the
revision having changed *both* its rendering and its routing. Spec §2's row *"the value-free 400
rendering happens in `ClassifyError`" → NOT IMPLEMENTABLE there* correctly moved the rendering to
`runtime/validation` and then stopped: it never asked how the rendered text **gets back** to a
transport that reads `err.Error()`.

**Proposed fix.** Name the missing symbol in the ADR and the plan, and drop the mandate that
forces the leak:
- `runtime/validation` gains an unexported error type, e.g.
  ```go
  type invalidInputError struct{ safe string; typed error }
  func (e *invalidInputError) Error() string  { return e.safe }          // value-free by construction
  func (e *invalidInputError) Unwrap() []error { return []error{ErrInvalidInput, e.typed} }
  ```
  so `errors.Is(err, ErrInvalidInput)` **and** `errors.As(err, **jsonschema.ValidationError)` both
  hold while `Error()` — the only thing `ClassifyError` reads — carries `keywordLocation` alone.
- **Then re-justify test 5.** With the rendering done in the gate, *nothing in this delivery needs
  `errors.As` after the gate*; the assertion's only remaining effect is to guarantee a
  value-bearing error is reachable by any consumer's error handling and by anything that formats
  the chain. Either state that as a deliberate, documented channel (and check it against D6's sink
  enumeration), or replace test 5 with the assertion that actually falsifies the phase:
  **`TestGateRenderingIsValueFreeEvenThoughTheTypedErrorIsStillWrapped`** — assert
  `errors.As` is true **and** `err.Error()` does not contain the submitted value. As written,
  test 5 is green against the leaking implementation.

---

## I-3 — CRITICAL — **D2 × D5 (the 413 body) × D5's own 4xx logging table**

**The pair.** D2 mints `service.ErrVariablesTooLarge` *"whose message **names which bound
tripped**"* (ADR D2). D5 routes it to 413 with the message **`static "request too large"`** (D5's
status table, ADR:605). D5 also widens 4xx logging *per class* — and its table has rows for
**403, 400, 400/403-verbose and 5xx**, and **no row for 413** (ADR:707-712). Phase 4's
instruction repeats the same four classes.

**The interaction.** D2's only diagnostic is destroyed at **both** sinks in the same decision that
promises a join between them:

| sink | what a 413 carries | consequence |
|---|---|---|
| response body | `"request too large"` + a correlation id | the caller cannot tell **body cap** from **variable cap**, nor **bytes** from **elements** |
| `cfg.Logger` | **nothing** — `writeErr`'s widened guard covers 403 and 400 only; 413 stays under the untouched `status >= 500` branch | the operator cannot tell either, and the correlation id in the body **joins to no record** |

Both D1 and D2 land on 413 with the **identical static string**, and they collide on the same
requests: `POST /instances` carries a body (D1's axis) *and* `StartInstanceRequest.Vars` (D2's
axis). A caller who shrinks their body when the real refusal was 10 001 elements gets a second
identical 413 and no signal at all. D2's whole justification for bounding the *incoming* rather
than the *merged* map is that *"the refusal happens … with the caller present, and **the caller
can retry with less**"* — a remedy that requires knowing **less of what**.

**This also contradicts D5's own reasoning one table row away.** D5 spends a full section arguing
that blanket blanking of 400 *"destroys the actionable messages three prior ADRs deliberately
added"*, and builds an exception list for sources *"provably value-free by construction"*. A
count of the caller's **own** submitted bytes/elements is value-free by construction in the
strongest possible sense — the caller already has the payload. So the static 413 is **pure
information loss with zero disclosure benefit**, which is the exact charge D5 levels at the design
it replaced. The rule was derived for one arm and not carried to the arm the same revision added.

**Evidence.** Document-internal contradiction, verified against the shipped code that makes it
bite (`grep -n "413" docs/{adr,specs,plans}/…` — 413 never appears in the ADR's logging table or
in phase 4's logging bullet). The untouched guard is `status >= 500` in all three adapters,
verbatim today:

```
transport/http/stdlib/write.go:33:  if status >= 500 {
transport/http/gin/write.go:14:     if status >= 500 {
transport/http/fiber/write.go:14:   if status >= 500 {
```

Phase 4 widens this *"per class: 403 logs the raw error …; 400 logs the rendered message …; 5xx
unchanged"*. 413 matches none of the three, so it falls through to `status >= 500` = false.

**Verdict on spec §5.** Row **D2 × D5** is marked ✅ for the *routing* question only — *"the
sentinel is now `service.ErrVariablesTooLarge` and D5 routes it by name to 413"*. It never asks
what the 413 **says**, and the table has **no D1 × D2 row for the shared status** (the existing
D1 × D2 row is about the byte/element window at admission, a different question). Row
**D5 × D6** (✅, "D6 names the sink") is also affected: D6's sink enumeration inherits a logging
table that omits a class.

**Proposed fix.**
1. **413 renders `err.Error()`**, added to D5's 400-style exception list as a value-free-by-
   construction source, with both sentinels' messages fixed by contract: e.g.
   `"workflow-service: variables too large: 12874 elements exceeds limit 10000"` and
   `"workflow-httpcore: request body exceeds 1048576 bytes"`. If the ADR prefers static bodies on
   principle, then D1 and D2 must at minimum be **distinguishable** — two `ErrorBody.Error` codes
   (`request_too_large` vs `variables_too_large`), not one string.
2. **Add a 413 row to the logging table** at `WarnContext` with the raw error (it contains no
   caller value), so the correlation id has something to join to. Without this, 413 is the only
   4xx in the system with an id and no record.
3. **Plan:** phase 3 test 2 (`TestOversizedVariablesClassifyAs413`) asserts only the status —
   extend it to assert the two refusals are **distinguishable**; phase 4 test 3
   (`TestCorrelationIDInBodyMatchesTheLogRecord`) must include a **413 row**, which is precisely
   the row that fails today against the prescribed guard.

---

## I-4 — CRITICAL — **D3 × D4 (the return path), and D3 × D6**

**The pair.** D3's scope statement says the two controls are orthogonal in **one** direction:
*"`httpcall.Do` and `transform.Do` receive the **unredacted** variable map … so a definition
author can write `WithURLExpr('https://reports.example.com/?q=' + vars.ssn)` and, to an allowed
host, this decision permits it."* D4 lists `incidents[].error` — *"the raw `err.Error()` of a
failed action — for an `httpcall` node, the target URL and query string"* — as **NOT covered**, a
new backlog item.

**The interaction, in the direction neither decision looks.** The value D3 lets *out* comes back
*in*, through the field D4 declined to cover, onto a **non-admin 200 route**. Two compounding
effects:

1. **D3 makes the channel wider, not narrower.** A `Dialer.Control` refusal is wrapped by
   `net/http` into a `*url.Error` and then by `httpcall.go:342`
   (`fmt.Errorf("workflow-httpcall: request failed: %w", err)`). The resulting string carries the
   **full request URL including its query string** *and* the **resolved internal IP address** —
   i.e. D3's security control **manufactures a network-topology disclosure** into an uncovered
   field. Nothing in D3 specifies what its refusal error may say; every other decision in the
   bundle specifies its error text carefully.
2. **The composition is a live path today**, before any refusal exists: `httpcall.go:360` is
   `fmt.Errorf("workflow-httpcall: %s %s -> %d", method, requestURL, resp.StatusCode)` — the URL
   verbatim on any non-2xx.

**Evidence (executed, `go test -count=1 -v -run TestD3Refusal ./zzprobe_inter/...`).** D3's exact
mechanism (`net.Dialer.Control`, `To4()`, not-global-unicast + `IsPrivate`), wrapped as
`httpcall.go:342` wraps it, with D3's **own example URL**:

```
incidents[].error would read:
  workflow-httpcall: request failed: Get "http://10.1.2.3:8080/reports?q=123-45-6789": dial tcp 10.1.2.3:8080: workflow-httpcall: refused 10.1.2.3:8080: not a permitted destination
```

`123-45-6789` — the value D4 exists to redact — and `10.1.2.3` **twice**. Verified reachable:
`instanceJSON.Incidents []incidentJSON` with `Error string \`json:"error"\`` (`service/instance.go:218`,
assigned `:314`), served by `GetInstanceSnapshot`, registered in the **instance** (non-admin)
group at `transport/http/stdlib/groups.go:64`.

**And D3 × D6.** D6 enumerates twelve plaintext at-rest columns and commits `SECURITY.md` to that
list. `wrkflw_instances.snapshot` holds the incident array, so D3's new refusal strings become
**at-rest** internal-network topology — a category D6's table describes as "the whole instance
state, incl. every process variable" and never as network destinations. The list stays correct by
column and becomes incomplete **by content**, which is precisely the failure mode D6 says is worse
than silence (*"a consumer who reads `SECURITY.md` … has been harmed by our documentation"*).

**Verdict on spec §5.** Row **D3 × D4** is ✅ on a one-directional reading — *"Redaction is a
display control, the allowlist a destination control … One sentence in D3 and in phase 9"*. The
return path is **not** in the row, so the ✅ is over-read. Row **D3 × D6** is ✅ as *"benign — phase
9 must not write them as one posture"*; it is not benign. Both rows need re-derivation.

**Proposed fix.**
1. **D3 specifies its refusal error text.** The refusal must name the **rule**, not the
   destination: `"workflow-httpcall: destination refused by URL restriction policy"`, with the
   host/IP available to the operator's log only. This is the same "static to the caller, raw to
   the operator" split D5 just built for 403 — reuse it rather than inventing a third policy.
2. **Sever the URL from the wrapped `*url.Error` on the refusal path**, since `%w` of a
   `*url.Error` re-embeds the whole URL no matter what the control hook says. Use
   `action.NonRetryable(errors.New(staticText))` for policy refusals and keep the detailed error
   out of the wrap.
3. **Move `incidents[].error` from "backlog" to "in scope for the refusal path at least."** D4 may
   legitimately defer general incident redaction, but it must not defer a channel **this bundle
   creates**. At minimum, `SECURITY.md` (phase 7) must state that `incidents[].error` can carry
   the target URL, its query string and resolved internal addresses, **on a non-admin route** —
   the plan's phase-7 bullet currently promises only *"which strings a non-admin caller can
   read: `allowed_actions[].condition` and the embedded `definition`"*, which is now short by
   one.
4. **Plan phase 5:** add `TestRefusalErrorDoesNotNameTheDestination`. **Falsifier:** *it fails
   against the natural implementation that returns the `*url.Error` from `client.Do`* — which is
   what every other error path in `httpcall.go` does, so the natural implementation is the
   leaking one.

---

## I-5 — MAJOR — **D4 (the breaking signature thread) × plan §2's phase ordering and §1's fan-out rules**

**The pair.** D4 requires the response policy to be threaded into **eight exported `httpcore`
endpoint functions** — *"Threaded in **one** edit as a single parameter"* (ADR Consequences),
scheduled in **phase 3**. All eight are *called* from the three adapters' `groups.go`, which are
**phase 4**. Plan §1 then hoists a verification: *"`go build ./examples/...` is attached to
**phase 3**, not only to the final checklist: phase 3 is where the public `httpcore` surface
changes, and `examples/` is the only consumer-compile check in the repo."*

**The interaction.** Phase 3's own prescribed verification **cannot pass at the end of phase 3**.
The signature change breaks every caller, and every caller is in phase 4. `examples/` imports the
`stdlib` adapter (`examples/{production,sqlite,mysql}_wiring/main.go`), so the consumer-compile
check is downstream of the very edits that have not happened yet. Consequences that follow:

- **Phase 4's three parallel agents each start from a repo that does not build.** Their first
  `go test ./transport/http/<pkg>/...` fails for reasons unrelated to their own RED state, which
  is exactly the condition CLAUDE.md's TDD discipline says must stay legible in the transcript
  ("a compile error like `undefined: NewThing` is a valid red state" — here it is *someone else's*
  red state, in all three packages at once).
- **Phase 6 (`transport/http/parity`) cannot compile until all three phase-4 agents finish**, not
  just "depends on 4" as a scheduling arrow — a straggler blocks it hard.
- Plan §1's fan-out rule (*"concurrent agents in one package break each other's `go test`
  compile"*) is stated as a **within-package** hazard. This is the **cross-package** version of
  the same hazard, and the rule as written does not catch it.

**Evidence (executed mutation in the audit worktree; `cp` backup, restored and re-verified).**
One of the eight threads applied, phase 4 not yet done:

```
=== BASELINE ===
baseline EXIT=0
=== AFTER phase-3's D4 signature thread (one of eight), phase 4 NOT yet done ===
# github.com/kartaladev/wrkflw/transport/http/stdlib
transport/http/stdlib/groups.go:66:75: not enough arguments in call to httpcore.GetInstanceSnapshot
	have (context.Context, service.Service, string)
	want (context.Context, service.Service, string, httpcore.ResponsePolicy)
=== RESTORED ===
restored EXIT=0
```

Call sites confirmed in all three adapters, e.g. `transport/http/stdlib/groups.go:46,56,66,76,91,118,161`.

**Verdict on spec §5.** Row **D4 × D5 (breaking surface)** is ✅ for *"D4's parameter is threaded
in **one** edit and listed as breaking"* — it settles *how many* edits and *whether it is
documented*, and never asks *when the repo builds again*. The omitted pair is **D4 × the plan's
phase graph**. This is the same class as audit #1's headline finding (*"a decision stated in the
ADR whose realisation lands in a package no phase assigns it to"*) — here the realisation is
assigned to two phases and the **verification** to the earlier one.

**Proposed fix.** Choose one and write it into §2:
- **(a) Preferred — make the signature thread a serial, controller-inline change spanning
  `httpcore` + all three adapters**, exactly as CLAUDE.md rule #11 prescribes for *"a serial,
  compile-breaking, repo-wide change that every other phase blocks on (e.g. a shared type
  change)"*. Phase 2 is already inline for a weaker reason (an error's type discipline); this one
  is the textbook case and is not inline.
- **(b) If it stays split**, move `go build ./examples/...` from phase 3 to **the end of phase 4**,
  and say in phase 4's brief that the repo does not build at its start and which error each agent
  should expect to see and fix.
- Add to §1's fan-out rules: *"a phase that changes an exported signature owns every call site of
  it, in whatever package they live"* — the by-package rule is necessary and not sufficient.

---

## I-6 — CRITICAL — **D5 × itself: the "machine-checked" pin invariant cannot fail on the case it exists for**

**The pair.** Two D5 bullets that only work if the other is true:
- *"400 is deny-by-default over an **OPEN set**, with an enumerated exception list… Every other
  source — **including any sentinel added to the arm in future** — renders static text"*;
- *"⚠ **The pin test is a machine-checked invariant, not a list in prose.** It asserts that the set
  of sentinels matching the 400 arm **equals** the set enumerated above — **a new sentinel added
  without a row fails the test** rather than silently inheriting `err.Error()`."*
  (Plan phase 3 test 3 repeats it: *"⚠ **The invariant is the point.**"*)

**The interaction.** The second bullet is the *entire* safety mechanism for the first, and it is
not constructible against `ClassifyError` as the bundle leaves it. There is no registry of
sentinels in this repo; "the set of sentinels matching the 400 arm" can only be computed by
running a **hand-listed corpus** through `ClassifyError`. A sentinel added to the arm and not
added to the corpus is invisible to the test — which is exactly and only the scenario the
invariant is written for. The property is **self-referentially vacuous**.

Compounding it: the arm is a closed `errors.Is` chain, so *"anything else in the arm renders
static `invalid input`"* describes **unreachable code** — nothing can match the 400 arm except an
enumerated sentinel. D5 conflates two different opennesses: the genuinely runtime-open set is the
**validation strategy** set (`validate.Register` is exported, plan phase 2 test 4 covers it
correctly); the 400 **sentinel** set is closed at every compile and only "open over time". The
deny-by-default language was carried from the first to the second, where it buys nothing.

**Evidence (executed mutation; `cp` backup, restored, `go build ./...` EXIT=0 after).** The pin
test written exactly as specified, then a future sentinel added to the arm with no test row:

```
=== BASELINE pin test ===
PIN PASSES: all 7 enumerated sentinels classify 400          --- PASS

=== MUTATION: a new sentinel joins the 400 arm, no test row added ===
PIN PASSES: all 7 enumerated sentinels classify 400          --- PASS     <-- did not fail

--- what the new sentinel now renders ---
status=400 message="workflow-httpcore: rejected value 4111-1111-1111-1111"
```

The mutation is the literal future the ADR describes; the invariant passed, and the new sentinel
silently inherited `err.Error()` and shipped a card number in a 400 body.

**Verdict on spec §5.** No row covers the pin test at all — it is the mechanism three ✅ rows lean
on (**D4 × D5**, **D5 × D6**, **D5 × the deferred ADR-0185**). The last of these is the sharpest:
it is marked ✅ because *"D5 records the ordering rule as a standing invariant for any future
arm"*, i.e. the bundle's defence against ADR-0185's 401/503 arms is **a comment plus this test**.
The comment survives; the test does not do what it claims. The omitted pair is **D5 × itself**.

**Proposed fix.** Make the arm's membership **data**, so it can be enumerated:
```go
// httpcore/errors.go
var badRequestRenderers = []struct {
    sentinel error
    render   func(error) string   // nil ⇒ static "invalid input"
}{ {kernel.ErrBadCursor, verbatim}, {engine.ErrInvalidOutcome, reshapeOutcome}, … }
```
`ClassifyError`'s 400 arm iterates it; the pin test then asserts over `badRequestRenderers`
itself — length, key set **and** each rendering — and a new entry without a test row genuinely
fails. Restate the mutation proof in the plan's checklist as *"add an entry to
`badRequestRenderers` and observe RED"*, which is falsifiable; the current instruction (*"Prove it
by adding one in a mutation"*) is unfalsifiable against a hand-listed corpus and would have been
recorded as satisfied.
⚠ Also delete *"anything else in the arm renders static"* or re-word it: as written it prescribes
unreachable code, and an implementer will write a `default` branch nothing reaches and a test that
cannot cover it — a coverage hole on the very hot arm CLAUDE.md's Verification section says must
be covered first.

---

## I-7 — MAJOR — **D1 × itself (wire bytes vs. the histogram) × plan §2's decision→phase→package map**

**The pair.** D1 says two things that need each other:
- *"**`MaxBodyBytes` means WIRE bytes, in all three adapters**"* — the folded correction the whole
  fiber `BodyRaw()` mechanism turns on;
- *"a `wrkflw_rest_request_body_bytes` histogram joins the existing transport instrumentation so a
  consumer can measure their real distribution **before** the cap bites"* — the migration story
  that makes a default-on cap acceptable.

Plan §2 assigns the histogram to **phase 3 / `transport/http/httpcore` (`observability.go`)**.

**The interaction.** The quantity is produced in phase 4 and the recorder is in phase 3. The
`httpcore` seam has no access to a wire-byte count:

```go
// transport/http/httpcore/observability.go
func (i *Instrumentation) Observe(
    ctx context.Context,
    method, routeTemplate string,
    hdr http.Header,
    run func(context.Context) (status int),
)
```

`Observe` receives headers and a status-returning closure — **never the body**. The wire-byte
count exists only where `http.MaxBytesReader` wraps `Body` (stdlib/gin) or where
`len(c.BodyRaw())` is taken (fiber), i.e. in the three adapters' `groups.go` — **phase 4**. The
only phase-3-reachable proxy is `hdr.Get("Content-Length")`, which is:
(i) **absent for chunked requests**, (ii) **attacker-declared**, not measured, and (iii) a
*different quantity* from the one `MaxBodyBytes` is defined over — so a consumer sizing their cap
from this histogram would be sizing it against a number the cap does not use. That is the same
class of defect as the `c.Body()`/`c.BodyRaw()` correction this revision just made, one decision
over.

This is precisely the failure audit #1 named as the bundle's root cause — *"a decision stated in
the ADR whose realisation lands in a package no phase assigns it to"* — **recurring inside the
table built to prevent it**. §2's own header says *"A row with no phase is a defect"*; the rule it
needs is stronger: *a row whose phase cannot reach the data is also a defect.*

**Second-order: an unlisted breaking change.** Recording body bytes through the existing seam
requires changing `Instrumentation.Observe` — an **exported method on an exported type**, called
from all three adapters (`transport/http/{stdlib,gin,fiber}/observe.go:20/19/39`) and therefore by
any consumer who assembled their own route group. The Consequences section lists **six** breaks
and this is not among them; the ADR is explicit that `ClassifyError`'s signature is *"deliberately
not"* changed, so the omission reads as a claim of completeness.

**Evidence.** `sed -n '25,120p' transport/http/httpcore/observability.go` (signature above,
verbatim); `grep -rn "\.Observe(" transport/ | grep -v _test` → the three adapter call sites
listed above; existing metric names `wrkflw_rest_requests_total` /
`wrkflw_rest_request_duration_seconds` at `observability.go:57,61`.

**Verdict on spec §5.** No row. The pairs the table omits are **D1 × itself** (wire-bytes mandate
vs. histogram placement) and **D1 × the observability seam**. Note the table has *no* D1 × D4 row
either, and both decisions change the same exported `httpcore` surface in the same phase.

**Proposed fix.**
1. Move the histogram row to **phase 4**, one recording per adapter at the point the cap is
   applied, and add a `RecordRequestBodyBytes(ctx, method, route string, n int64)` method to
   `Instrumentation` in phase 3 — **additive**, no signature break, and it keeps the metric
   definition in `httpcore` where the other two live.
2. State in D1 that the histogram records the **same** quantity the cap enforces (wire bytes), and
   that a request rejected by the cap is recorded at the cap value, not at `Content-Length` —
   otherwise the pre-cap distribution a consumer measures is systematically wrong at the tail,
   which is the only part of the distribution they are measuring it for.
3. Add the new `Instrumentation` method to the Consequences break list, or state explicitly that
   it is additive and therefore not a break — silence here is the same shape as the omission this
   finding reports.

---

## I-8 — MAJOR — **D4's widened covered set (11 paths) × D4's unwidened breaking-change list (8 functions)**

**The pair.** Two D4 enumerations, both changed by this revision, written against different
premises:
- *"⚠⚠ **The covered set is the ELEVEN paths named in Context §4**… The three direct-`NewInstanceView`
  admin endpoints (`ResolveIncident`, `CancelInstance`, `ResolveCompensationStall`) **are in the
  set**"* — the fix folded from audit #1;
- *"**BREAKING (source)**: the **eight** exported `httpcore` endpoint functions that project
  instance state gain the response-policy parameter — `GetInstanceSnapshot`, `GetActionableView`,
  and the six taking `mapper func(engine.InstanceState) any`"* — plus plan §2's row *"D4 endpoint
  signature thread (**8 functions**)"* and phase 3's *"Thread the response policy into the
  **eight** exported endpoint functions in **one** edit."*

**The interaction.** The path count was widened from 8 → 11 and the **function** count was not.
The three added paths live in three exported functions that are *not* among the eight, and they
take neither a mapper nor a config — so the redaction helper inside them has **nothing to call**
unless they too gain the parameter. The correct figure is **eleven functions**, and the source
break is three wider than documented.

**Evidence (executed, `grep … | grep -v _test`).** The dimensions reconcile exactly, and the
mismatch is arithmetic, not interpretation:

```
$ grep -rn "mapper func(engine.InstanceState) any" transport/http/httpcore/ | grep -v _test
endpoints.go:15  mapInstance (the helper, not an endpoint)
endpoints.go:25  StartInstance      endpoints.go:47  GetInstance
endpoints.go:82  DeliverSignal      endpoints.go:116 ClaimTask
endpoints.go:129 CompleteTask       endpoints.go:145 ReassignTask     -> SIX, as the ADR says

$ grep -n "mapInstance(" transport/http/httpcore/endpoints.go
42, 52, 94, 124, 140, 155                                             -> SIX call sites, one per function

$ grep -rn "NewInstanceView(" transport/http/httpcore/ | grep -v _test
admin_endpoints.go:111  ResolveIncident            }
admin_endpoints.go:121  CancelInstance             } THREE exported funcs, no mapper, no cfg
admin_endpoints.go:514  ResolveCompensationStall   }
```

6 (mapper-taking) + 2 (`GetInstanceSnapshot`, `GetActionableView`) = **8 threaded**;
6 + 2 + **3** = **11 paths**. The three admin functions are in the covered set and outside the
thread.

**Verdict on spec §5.** Row **D4 × D5 (breaking surface)** is ✅ on *"D4's parameter is threaded in
**one** edit and **listed as breaking**"* — the ✅ certifies the listing, and the listing is short
by three. Row **D4 × the response-customization feature** is ✅ partly on *"above the three
mapper-less admin endpoints"*, i.e. that row **knows** about the three while the breaking list
does not. Two ✅ rows disagree with each other about the same enumeration.

**Proposed fix.** Change **8 → 11** in three places (ADR Consequences "BREAKING (source)", plan §2's
map row, plan phase 3's thread bullet) and name the three admin functions explicitly, so the
`CHANGELOG.md`/`STABILITY.md` entry phase 7 writes is right the first time. Then re-check phase 3
test 8's count invariant against the **function** dimension as well as the call-site dimension —
as prescribed it counts `NewInstanceView`/`mapInstance` **call sites**, which would have been
green with the three admin functions un-threaded had the helper been reachable another way.
⚠ This is the enumeration plan §4 warns about (*"assume one more is wrong"*) — but the rot here is
not in the count of paths, which is right; it is in a **second** count that the path fix was never
propagated to.

---

## I-9 — MAJOR — **D3 × D3: `WithAllowedCIDRs` (the new escape hatch) × `WithAllowedHosts` being unconfigured by default**

*(This is the brief's explicit question 3: "is there a configuration where a consumer reasonably
expects access and gets none, or **expects a block and gets access**?" — the second half.)*

**The pair.** Two D3 bullets, each correct alone:
- *"`WithAllowedCIDRs([]string)` is the exemption that **makes the escape hatch usable**: it opts
  specific **networks** out of the IP deny-list, so the consumer whose `httpcall` node
  legitimately targets **one internal `10.x` service** has an answer short of turning the whole
  protection off."*
- *"**Host allow-list**… `WithAllowedHosts([]string)` is a *positive* filter… ⚠ **The two gates
  are independent and BOTH must pass.**"* — and, critically, the host gate applies only *"when
  configured"*, i.e. **the default is no host restriction at all**.

**The interaction.** The escape hatch is sold as *per-service* and is implemented as
*per-network*, and the gate that could have scoped it back down is **off by default**. A consumer
following D3's own sentence — one internal service, so add its CIDR — re-opens the entire
exempted range to **any attacker-chosen URL expression**, including a hostname that resolves into
it. D3 refuses to let `AllowedHosts` override the IP gate with the explicit reason *"or the option
becomes a rebinding bypass"* — and then adds an option that **is** a rebinding bypass into the
exempted range, without saying so. `10.0.0.0/8` is 16.7 M addresses; on a typical Kubernetes
deployment it contains the API server and the whole service network.

**Evidence (executed, `go test -count=1 -v -run TestD3EscapeHatchScope ./zzprobe_inter/...`).**
Both gates implemented exactly as D3 specifies (`To4()` normalisation, not-global-unicast +
`IsPrivate`/`IsLoopback`/`IsLinkLocalUnicast`, CIDR exemption applied to the IP gate only, host
gate permissive when unconfigured). Config = the consumer's stated intent:
`WithAllowedCIDRs(["10.0.0.0/8"])`, no host list.

```
the ONE service the consumer meant       host=reports.internal   ip=10.20.0.9       *** DIAL PERMITTED ***
attacker-chosen host, rebinding …        host=evil.example.com   ip=10.0.0.1        *** DIAL PERMITTED ***
attacker-chosen literal inside range     host=10.99.99.99        ip=10.99.99.99     *** DIAL PERMITTED ***
kubernetes API on the cluster network    host=kubernetes.default ip=10.96.0.1       *** DIAL PERMITTED ***
cloud metadata (link-local, NOT exempted) host=metadata.internal ip=169.254.169.254 REFUSED
```

Row 1 is the intent. Rows 2–4 are the cost. Row 5 is the control confirming the exemption is
scoped to the named range and the rest of the deny-list still holds — i.e. the finding is about
the **granularity** of the hatch, not a break in the property.

**The symmetric half of the question (expects access, gets none) is genuinely resolved** and I
could not break it: an allow-listed public host resolving to a public address passes both gates;
`WithBaseURL` is untouched; the empty-allow-list `CheckRedirect` follows redirects, so http→https
upgrades survive. That correction from audit #1 holds.

**Verdict on spec §5.** There is **no D3 × D3 row**, and D3 is one of the six decisions this
revision changed — the corollary in CLAUDE.md rule #9 asks for the pairwise consequences of each
changed decision, and `WithAllowedCIDRs` is a *new option* whose consequences against D3's *other*
new mechanism were not derived. The nearest row, **D2 × D3**, is about the admission bound
reaching `httpcall` transitively, a different question.

**Proposed fix.** Any one of these closes it; (a) is cheapest and matches the ADR's own reasoning:
- **(a) Make the CIDR exemption conditional on a configured host allow-list.** If
  `WithAllowedCIDRs` is set and `WithAllowedHosts` is empty, that is a **construction error** —
  exactly the "compose or refuse, never overwrite" rule D3 already applies to
  `WithHTTPClient` + `WithURLExpr`. The consumer who means "one internal service" can name it.
- **(b) Scope the exemption to a (host, CIDR) pair** — `WithAllowedDestination(host, cidr)` — so
  the option expresses the sentence D3 uses to justify it.
- **(c) If it ships as-is**, D3 must state the cost in the same bullet, `SECURITY.md` (phase 7)
  must say *"a CIDR exemption re-admits **every** address in that range from **any**
  expression-derived URL, including a hostname that resolves into it"*, and the Consequences'
  *"the escape hatch works"* claim must be hedged.
- **Plan phase 5:** test 4 (`TestAllowedCIDRsOptsANetworkBackIn`) asserts only that the hatch is
  reachable — it is green against every variant above. Add
  **`TestAllowedCIDRsDoesNotAdmitAnUnrelatedHostInTheSameRange`**. **Falsifier:** *it fails against
  the design as currently written*, which is the point.

---

## I-10 — CRITICAL — **D2 × itself: the per-request bound does not bound the caller, because the caller controls the request COUNT**

*(This is the brief's explicit question (a): "the aggregate is NOT bounded — is the weaker property
still worth the surface, and **does it leave an attack open?**" It does.)*

**The pair.** D2's two halves, each defensible alone:
- the scope statement — *"⇒ **What this bound guarantees is per-request on the caller axis.** It
  does **not** bound the aggregate map"*, and *"The untrusted axis this record exists to close is
  **caller-supplied** input; action output is author-configured"*;
- the wedge argument that chose it — *"Bounding the *merged* result … converts the unbounded
  runtime growth below into an **unrecoverable wedge**: once a service action's output has pushed
  the map past 256 KiB, every subsequent `CompleteTask` would be refused **413 forever**."*

**The interaction.** "Per-request on the caller axis" and "the caller-supplied axis is closed" are
treated as the same statement. They are not, because **a caller can send more than one request**.
`mergeVars` is a top-level `maps.Copy`, so payloads with distinct keys accumulate, and
`DeliverSignal` / `DeliverMessage` can be replayed by the same caller against the same instance
with no engine or author involvement. The measured hazard the whole delivery is built around
(spec §1: *"expression evaluation unbounded in **caller-supplied input size** — O(n²) measured,
n = 10 000 → **2.458 s**"*) is therefore **rate-limited, not closed**: the bound caps `n` per HTTP
request, and the attacker's `n` is `10 000 × requests`.

**The wedge argument does not cover the paths where the attack lives.** It is an argument about
`CompleteTask` — a task that must remain completable. Refusing a *signal* or *message* delivery
wedges nothing: the instance stays in its wait state and the caller can retry with less, exactly
the property D2 claims for admission. So a merged-map bound was available on precisely the two
paths that carry the attack, and D2 never considers applying it selectively — the wedge reasoning
was derived for one of the four admission fields and generalised to all four.

**Evidence (executed, `go test -count=1 -v -run TestD2Aggregate ./zzprobe_inter/...`).** D2's
admission check as specified (256 KiB **and** 10 000 elements, together, on the incoming map) plus
`mergeVars`' body verbatim from `engine/step_state.go:314-322`. Five `DeliverSignalRequest.Payload`
requests, each with 9 999 distinct top-level keys:

```
request 1: payload 9999 elems / 157765 bytes -> ADMITTED | aggregate now  9999 elems / 157765 bytes
request 2: payload 9999 elems / 157765 bytes -> ADMITTED | aggregate now 19998 elems / 315529 bytes
request 3: payload 9999 elems / 157765 bytes -> ADMITTED | aggregate now 29997 elems / 473293 bytes
request 4: payload 9999 elems / 157765 bytes -> ADMITTED | aggregate now 39996 elems / 631057 bytes
request 5: payload 9999 elems / 157765 bytes -> ADMITTED | aggregate now 49995 elems / 788821 bytes

after 5 admitted requests the evaluation env is 49995 elements
```

**Five requests.** The ADR's own ladder puts n = 50 000 at **≈61 s per evaluation** — and every
subsequent gateway evaluation on the engine's path pays it, where ADR-0056's deliberate trade
means there is **no wall-clock backstop**. The instance is also now durably ~789 KiB of variables
in `wrkflw_instances.snapshot`, i.e. **3×** the byte bound D2 advertises, reached entirely through
admitted requests.

**Second defect, same root.** D2 enumerates the `mergeVars` sources as **three** — *"a service
action's output (`engine/step_triggers.go:161`), human-task completion output (`:936`) and the
message/callback mirror (`:1208`)"*. Executed:

```
$ grep -rn "mergeVars(" engine/ | grep -v _test
engine/step_triggers.go:45   :161   :841   :936   :1028   :1208   :1312   :1349
engine/step_state.go:314  (the definition)
```

**Eight call sites, not three** — and the three the enumeration omits that matter most are
**`:1028`, `:1312` and `:1349`, all `mergeVars(s, t.Payload)`**, i.e. the *caller-supplied*
signal/message payload merges. D2's list contains only author/engine sources, which is exactly why
the caller-axis aggregation was never derived: the enumeration that would have surfaced it had the
relevant rows missing. (`:45` is `StartInstance` vars, `:841` an engine-authored outcome name.)

**Verdict on spec §5.** Row **D2 × itself** is marked ✅ — *"It cannot wedge (refusal is
pre-persist, caller present, retry with less). ⚠ The aggregate map is therefore **NOT** bounded"*.
The row states the weakness and then marks it resolved, on the ground that the *alternative* was
worse. That is an adjudication, not a resolution: the ✅ should be a **⚠ accepted residual with a
named attack**, and the attack is not named anywhere in the bundle. Row **D1 × D2** (✅, "the
window is closed") and the Positive consequence *"the unbounded-input surface closes on both axes
that were measured"* both inherit the over-read — the second axis (variable payload) does **not**
close, it becomes 10 000-per-request.

**Proposed fix.** In descending order of preference:
- **(a) Bound the merged map on `DeliverSignal` and `DeliverMessage` only**, keeping the incoming
  bound on `StartInstance.Vars` and `CompleteTask.Output` where the wedge argument genuinely
  applies. This is a two-line change to the seam D2 already builds and it closes the attack. The
  ADR must then state the asymmetry and why (the wedge argument is per-field, not per-decision).
- **(b) If the aggregate stays unbounded**, D2 must carry the executed numbers above as a named
  residual — *"a caller may reach n elements in ⌈n/10 000⌉ signal deliveries; at n = 50 000 the
  measured gateway evaluation is ≈61 s and there is no wall-clock backstop (ADR-0056)"* — and
  `SECURITY.md` (phase 7) must tell a consumer to rate-limit signal/message delivery, which is
  currently not mentioned. Silence here is the over-claim D2's own text says it is trying to avoid.
- **(c) Correct the `mergeVars` enumeration to eight sites**, and separate them into
  *caller-supplied* (`:45`, `:1028`, `:1312`, `:1349`) and *author/engine* (`:161`, `:841`,
  `:936`, `:1208`). The current three-source list is what made the attack invisible.
- **Plan phase 1:** test 6 (`TestRuntimeVariableGrowthIsNotRefused`) and test 7
  (`TestCompleteTaskIsNotRefusedBecausePriorStateIsLarge`) both pin the *permissive* direction and
  neither can fail against an implementation with this hole. Add
  **`TestRepeatedSignalDeliveryCannotGrowTheEnvPastTheBound`**. **Falsifier under fix (a):** *it
  fails against the design as currently written* — five admitted requests reach 49 995 elements.

---

## I-11 — MAJOR — **D5 × D6: the new default-on 403 log sink is missing from D6's sink enumeration**

**The pair.** D5 widens 4xx logging *per class*, and its table logs **403's raw error by default**:
*"403 | the **raw** error + correlation id … it belongs in the operator's log | `WarnContext`"*.
D6 commits `SECURITY.md` to naming what this bundle changes: *"⚠ `SECURITY.md` must also record
the **two sinks this bundle itself creates or widens**: with `WithVerboseErrorLogging(true)`,
rejected request payloads reach the configured `slog.Logger`; and the caps bound, but do not
protect, what is already at rest."*

**The interaction.** D6's enumeration names only the **opt-in** sink. The **default-on** one — 403's
raw error, and 400's rendered message — is absent, and D6's deliverable *is* the enumeration
(*"an incomplete list presented as exhaustive is strictly worse than the silence D6 rejects"*).
Three things make the omission bite rather than being a wording nit:

1. **The default destination is `slog.Default()`.** `CustomizeConfig.Logger` is defaulted in two
   places — `transport/http/httpcore/seam.go:43` (`Logger: slog.Default()`) and `:56-57`
   (`if cfg.Logger == nil { cfg.Logger = slog.Default() }`). A consumer who never called
   `WithLogger` gets process-default logging of every 403 predicate source, starting at upgrade.
   D5's phrase *"it belongs in the operator's log"* presumes a configured operator log that, by
   the shipped default, does not exist.
2. **It is the same string class D6 itself flags as sensitive at rest.** D6's twelve-column table
   lists `wrkflw_human_task.eligibility` = *"the attribute-**predicate source**"*. D5 now writes
   that same class of string to logs by default while D6's list describes only columns.
3. **The reasoning that protected 400 was not carried one row up.** D5 argues, correctly and at
   length, that widening the sink unconditionally *"would move the submitted value … **off the
   wire and onto `slog.Default()`** — a sink `RedactVariables` cannot reach, that **Decision 6's
   at-rest enumeration would then be wrong about**, and that in a typical deployment has longer
   retention and a wider audience than the API response ever had."* Every clause of that sentence
   applies to the 403 row directly above it; the flag was applied to one row and not the other.

**Evidence (read at the anchor).**
```
transport/http/httpcore/seam.go:29   // Logger receives 5xx raw error details (never sent to clients).
transport/http/httpcore/seam.go:30   Logger         *slog.Logger
transport/http/httpcore/seam.go:43           Logger:         slog.Default(),
transport/http/httpcore/seam.go:56-57        if cfg.Logger == nil { cfg.Logger = slog.Default() }
transport/http/httpcore/seam.go:84   // WithLogger sets the logger used for 5xx raw-error logging.
```
`grep -rn "slog\.\|Logger\." transport/http/httpcore/ | grep -v _test` returns only these lines —
confirming the ADR's *"`httpcore` never logs at all"* and, with it, that every new 4xx record is a
sink this bundle creates.

**Verdict on spec §5.** Row **D5 × D6** is ✅ on *"Logging is widened **per class** … D6 names the
sink"* — singular. There is one sink named and two created. The row's own resolution text
describes the mechanism correctly and then certifies a documentation obligation that the ADR
discharges incompletely.

**Proposed fix.**
1. D6's sink sentence becomes **three** sinks: (i) 403 raw error → `Logger` (**default on**,
   carrying policy predicate source, i.e. process-variable and actor-attribute *names*);
   (ii) 400 rendered message + sentinel class → `Logger` (**default on**, value-free by
   construction per D5); (iii) raw 4xx error → `Logger` only under `WithVerboseErrorLogging`.
   Add the 413 row from **I-3** and it is four.
2. Say explicitly that `Logger` defaults to `slog.Default()`, so the default-on records land in the
   consumer's process logger whether or not they configured one — and fix
   `WithLogger`'s godoc (`seam.go:84`, *"used for 5xx raw-error logging"*) alongside
   `CustomizeConfig.Logger`'s, which D5 already schedules. The plan's §2 map has a row for the
   `Logger` godoc and **none for `WithLogger`'s**.
3. **Consider defaulting the 403 raw log behind `WithVerboseErrorLogging` too**, and logging only
   the correlation id + `"forbidden"` by default. That makes the flag mean one coherent thing —
   *"put raw 4xx detail in my logs"* — instead of covering 400 but not 403, which is the shape a
   consumer will mis-read.

---

## I-12 — MAJOR — **D2 × D4: the deep copy D4 conditionalises for cost is unbounded in size, because D2 declined to bound the aggregate**

**The pair.** D4 makes the deep copy conditional *because* of cost: *"the read path is a hot path
and a recursive copy of every response's variable map is a real cost that **nothing in this bundle
has measured**."* D2 decides the aggregate map is **not** bounded: *"⚠⚠ **Runtime growth** … None
of these is bounded by this decision, and that is deliberate"*, and — per **I-10** — the
caller-supplied aggregate is not bounded either.

**The interaction.** For a consumer who *does* configure `RedactVariables` — i.e. every consumer
who takes up the security feature D4 exists to add — **every instance read performs a recursive
copy of a structure with no upper bound**, and the size of that structure is under the control of
whoever can deliver signals to the instance (I-10) or configure an `httpcall` node. The ADR
already records the second half of this by accident, in a Consequence written for a different
purpose: *"⚠ **Two first-party defaults disagree**: `action/httpcall` writes up to **10 MiB** into
`vars["httpBody"]`, **40×** the 256 KiB variable bound."* A 10 MiB JSON-shaped recursive copy, per
read, per response, is not a footnote about default sizing — it is the hot-path cost D4 built its
conditional to avoid, arriving through the door D2 left open.

The composition is a **caller-amplifiable** cost: an attacker grows the map cheaply (5 requests →
~789 KiB, I-10), and the victim pays a recursive walk of it on **every** subsequent read by any
client. Neither decision can see this alone — D4 reasons about "a variable map" as if bounded, D2
reasons about evaluation cost and persistence and never about the response path.

**Evidence.** Document-internal, over I-10's executed aggregate (5 admitted requests → 49 995
elements / 788 821 bytes) and the ADR's own 10 MiB `httpcall` datapoint. No further execution
needed: the two decisions' texts are sufficient, and the cost claim D4 rests on is explicitly
labelled unmeasured by D4 itself.

**Verdict on spec §5.** Row **D2 × D4** reads *"The bound counts the map on the way in; redaction
transforms it on the way out. | ✅ **none.** (The nested-copy hazard is D4-internal.)"* — the only
row in the table that asserts a **negative**. It is wrong in both clauses: the hazard is not
D4-internal (it is D4 × the cache, **I-1**), and the pair is not "none" (it is D4's cost premise ×
D2's scope decision).

**Proposed fix.** Whichever way **I-1** is resolved, D4 must bound what it copies:
- If the deep copy becomes unconditional (I-1 fix (a)), it needs a **size or depth guard** and a
  documented behaviour when exceeded — refuse, truncate, or fall back to the shallow copy with the
  aliasing contract stated.
- If the conditional stays, D4 must state that a hook-configured deployment pays an **unbounded**
  per-read recursive copy, and `SECURITY.md` must pair that with I-10's rate-limiting advice —
  otherwise enabling redaction converts an availability property from "bounded" to
  "caller-controlled".
- **Plan phase 3:** the missing measurement is one benchmark. Add
  **`BenchmarkRedactionCopyByVariableMapSize`** over 1 KiB / 256 KiB / 1 MiB / 10 MiB maps, and
  put the numbers in the ADR. D4's central design choice currently rests on an explicitly
  unmeasured premise, which is the one thing CLAUDE.md's Premise Discipline forbids a decision to
  rest on.

---

## I-13 — MEDIUM — **D4 × D5: `keywordLocation` discloses the NAME of a key D4 was configured to redact**

**The pair.** D4 lets a consumer redact named variables from responses (*"redact `ssn` only for
definition `kyc-v3`"* is D4's own motivating example for widening the hook signature). D5 renders
400s as *"one line per leaf … carrying **`keywordLocation` and nothing else**"*, and the evidence
file's own executed sample of that column is **`/properties/ssn/pattern`**.

**The interaction.** `keywordLocation` is value-free — that claim survives my attack and the
evidence for it is sound. But it is **not name-free**: it is a JSON pointer into the schema, and
for a closed-`properties` schema it names the field. So a deployment that configures
`RedactVariables` to strip `ssn` from every 200 response still discloses, on any 400, that a field
called `ssn` exists, is constrained by a pattern, and was submitted wrongly. D5 explicitly counts
this as a *feature* — *"For a closed-`properties` schema `keywordLocation` still names the field
**and** the constraint, so nothing is lost"* — which is true for ergonomics and is the exact
property D4's consumer is paying to remove.

This is not a leak of caller data and I am not arguing it should be blanked. It is an **unstated
boundary between two controls that ship together**, and the bundle's own D3 scope statement sets
the precedent for stating exactly this kind of thing (*"a reader will otherwise compose the two
into a guarantee neither makes"*).

**Evidence.** The bundle's own executed probe, Evidence §2:
`keywordLocation="/properties/ssn/pattern"`, `"/propertyNames/maxLength"`,
`"/additionalProperties/type"`. Field name present in the first; schema-structure only in the
others — so the disclosure is real and shape-dependent, not universal.

**Verdict on spec §5.** Row **D4 × D5** is ✅ on *"D5 renders `keywordLocation` only — nothing
derived from the value, no lengths. **D4 states that redaction does not extend to validation
errors and the namespaces are independent by design.**" The first clause is correct. The second
clause is the resolution — and **it is not in D4**. Grepping the ADR, D4 contains no sentence about
validation errors or independent namespaces; the row certifies a document change that was never
made. That is the row's actual defect, and it is a small instance of the shape this lens keeps
finding: *a decision assuming a channel another decision owns and does not provide* — here, a spec
row assuming an ADR sentence that does not exist.

**Proposed fix.** Add the sentence the §5 row already promises, to **D4**, not only to the table:
*"Redaction is a control over response **values**. It does not extend to validation errors: a 400
renders a schema location that may name a redacted field. The two namespaces are independent by
design."* Mirror it in `SECURITY.md` (phase 7), whose bullet *"what a 400/403/413 body does and
does not contain"* is the right home. Zero code cost; it closes a row that is currently ✅ against
nothing.

---

## I-14 — MEDIUM — **D2 × D5: D2's "`runtime` is untouched" quantifier is falsified by D5's own phase, in the same revision**

**The pair.** D2's headline guarantee — repeated four times across the bundle, and the reason it
claims ADR-0003/0049/0056 need no amendment:

```
docs/adr/…0186….md:438   `runtime` and `engine` are **not touched at all**.
docs/adr/…0186….md:783   - `internal/expreval`, `runtime` and `engine` are **untouched**.
docs/specs/…disclosure.md:9     … left `internal/expreval`, `runtime` and `engine` untouched
docs/specs/…disclosure.md:143   **`internal/expreval`, `runtime` and `engine` are not touched.**
```

D5's realisation, in the same bundle:

```
docs/plans/…disclosure.md:161  | D5 gate preserves the typed error (`%w`)          | 2 | `runtime/validation` |
docs/plans/…disclosure.md:162  | D5 `keywordLocation`-only rendering               | 2 | `runtime/validation` |
docs/plans/…disclosure.md:164  | D5 `avro`/`callback`/unknown-kind → static        | 2 | `runtime/validation` |
docs/plans/…disclosure.md:179  | 2 | `runtime/validation` + `definition/model/validate/expr` | … |
docs/plans/…disclosure.md:320  **Verify:** go test -race -count=1 ./runtime/validation/... …
```

`runtime/validation` is a package **under `runtime/`** (`runtime/validation/gate.go`, package
`validation`). The bundle therefore says `runtime` is untouched and schedules three edits to it.

**The interaction, and why it is not a nit.** This is the textbook product of a two-decision
revision: D2 moved the bound *out* of `runtime` and minted an absolute quantifier to celebrate it;
D5 moved the 400 rendering *into* `runtime/validation` in the same fold. Each change is correct;
the sentence describing one of them was written against premises the other changed. CLAUDE.md's
Premise Discipline names this exact class — *"Verify every all, none, only, every, never, always …
as if it stood alone"* — and it has now produced an operational hazard, not just a false sentence:

**Plan phase 1's brief instructs an escalation on it.** *"⚠ **Do NOT touch `internal/expreval`,
`runtime` or `engine`.** … If implementation suggests the bound 'really belongs' at the evaluator,
**stop and escalate** — that is the design audit #1 refuted, not a discovery."* A phase-1 agent
that notices phase 2 editing `runtime/validation` has been told, in its own brief, that this is a
stop-and-escalate condition. The controller then burns a round-trip adjudicating a contradiction
the documents created.

**Evidence.** Executed at the anchor: `ls -d runtime/validation` → `runtime/validation`;
`head -1 runtime/validation/gate.go` → `// Package validation is the executor-side of
external-input validation:`. Both greps above are verbatim output.

**Verdict on spec §5.** Rows **D2 × ADR-0049 replay** and **D2 × the shipped `runtime` options**
are both ✅ and both rest on the untouched claim. Their *substance* survives — nothing in phase 2
goes near `ConditionEvaluator`, `driver.conditionEval`, the compile cache or replay, so the
**invariants** those rows protect are genuinely safe. It is the **quantifier** that is false, and
it is false in the one place a reader checks for scope.

**Proposed fix.** Replace the four occurrences with the precise claim, which is stronger anyway
because it is checkable: *"`internal/expreval` and `engine` are untouched, and within `runtime`
only `runtime/validation` changes — the process driver, `ConditionEvaluator`, the shared compile
cache and the replay path are not touched, so ADR-0003/0049/0056 need no amendment."* Then delete
`runtime` from phase 1's do-not-touch list (leaving `internal/expreval` and `engine`, which are
the ones the escalation clause is actually about), or qualify it as *"`runtime` **except**
`runtime/validation`, which is phase 2's."*

---

# The complete D×D matrix — all 15 pairs + all 6 self-cells

Legend: **finding** = a defect recorded above · **HOLDS** = I attacked the spec §5 resolution and
could not break it · **NOTE** = no defect, but a consequence the documents should carry.

Decision glosses, once, for a reader who does not have the ADR open: **D1** request bodies capped
at 1 MiB wire bytes → 413 · **D2** caller-supplied variable maps bounded at admission in bytes and
elements · **D3** expression-derived URLs restricted by an IP deny-list + host allow-list ·
**D4** redaction hook at the `ProcessInstance` → response boundary on 11 paths · **D5** 403 static,
400 value-free, correlation id + 4xx logging · **D6** at-rest posture documented, mechanism deferred.

| pair | verdict |
|---|---|
| **D1 × D2** | **I-3 (CRITICAL)** — both refuse with 413 and one static string, so body-cap and variable-cap are indistinguishable to caller and operator alike. Spec's own D1×D2 row (the byte/element admission window) **HOLDS** — co-location does close that window. But the Positive consequence *"the unbounded-input surface closes on **both** axes"* is over-read once **I-10** lands: axis 2 becomes 10 000-per-request, not closed. |
| **D1 × D3** | **HOLDS.** The `ErrBodyTooLarge` → `ErrRequestBodyTooLarge` rename is correct and `action/httpcall.ErrBodyTooLarge` is confirmed exported at `httpcall.go:94` meaning an *outbound* 10 MiB response → 500. **NOTE:** phase 3's prescribed test (*"asserting `httpcall.ErrBodyTooLarge` still classifies 500"*) creates a **new import edge** `transport/http/httpcore` (test) → `action/httpcall`; `grep -rn "kartaladev/wrkflw/action" transport/http/` returns **nothing** today. Phase 5 mutates that package **in parallel** with phase 3, so phase 3's verification compiles a package another agent is mid-edit in. Cheap fix: move that one assertion to phase 6, or make phase 5 a predecessor of phase 3. |
| **D1 × D4** | **HOLDS**, with a **NOTE**: `ResolveIncident` is simultaneously D1's discarding decode site (phase 4, adapters) and one of D4's three direct-`NewInstanceView` admin endpoints (phase 3, `httpcore`). Different packages, correctly ordered — not a collision, but it is the single most-edited endpoint in the delivery and neither phase brief mentions the other. See also **I-8**, which makes it one of the three functions the signature thread must gain. |
| **D1 × D5** | **I-3 (CRITICAL)**. The ordering fix (413 arm before 400, bare sentinel, no `ErrBadInput` wrap) is executed and **HOLDS**; the fiber-above-`DefaultBodyLimit` exception to *"every error body"* is correctly stated and **HOLDS**. |
| **D1 × D6** | **HOLDS** (benign — caps reduce plaintext volume; D6 states caps bound but do not protect). **NOTE:** weakened by **I-10** — 5 admitted requests durably persist ~789 KiB of variables, 3× the byte bound, into `wrkflw_instances.snapshot`. |
| **D2 × D3** | **HOLDS.** The admission move genuinely reaches `httpcall`'s URL expression and `action/transform` transitively, and the withdrawal of the `expreval` routing (which would have replaced a non-string **rejection** with a **coercion**) is correct. **NOTE:** the map `httpcall` reads is the **aggregate**, so per **I-10** the inherited bound is per-request, not absolute — the Positive consequence already hedges this (*"for the caller-supplied contribution"*) and should also name the repetition axis. |
| **D2 × D4** | **I-12 (MAJOR)** — the deep copy D4 conditionalises on cost is unbounded in size precisely because D2 declined to bound the aggregate. Spec §5's *"✅ none"* is the table's only asserted negative and it is wrong twice. |
| **D2 × D5** | **I-3 (CRITICAL)**. The *routing* fix (`service.ErrVariablesTooLarge` named in the 413 arm, general rule that an unrouted sentinel becomes a 500) is correct and **HOLDS**. **I-14 (MEDIUM)** — D2's `runtime`-untouched quantifier is falsified by D5's phase 2. |
| **D2 × D6** | **HOLDS** — the bound never touches persistence, no re-check of stored instances, replay untouched. |
| **D3 × D4** | **I-4 (CRITICAL)** — the return path. D3's refusal errors and `httpcall`'s existing non-2xx error carry the full URL, its query string and resolved internal IPs into `incidents[].error`, which D4 explicitly leaves uncovered, on a **non-admin** route. |
| **D3 × D5** | **HOLDS.** `WithURLExpr` keeps its own `expr.Compile`; phase 2 is explicitly told not to re-route `definition/model/validate/expr`; `httpcall` errors become incidents rather than reaching `ClassifyError`, so no arm interacts. |
| **D3 × D6** | **I-4 (CRITICAL, second half)** — D3's new refusal strings become at-rest content inside `wrkflw_instances.snapshot`, a *content* category D6's column-wise enumeration does not describe. The *"do not write D3 and D6 as one posture"* instruction **HOLDS** and is good. |
| **D4 × D5** | **I-8 (MAJOR)** — the covered set widened 8→11 paths, the breaking-change list did not (11 functions, not 8). **I-13 (MEDIUM)** — the §5 row's resolution cites a D4 sentence about independent namespaces that **does not exist in D4**. The `keywordLocation`-only rendering itself is value-free and **HOLDS**. |
| **D4 × D6** | **HOLDS** (display vs. at-rest, no shared claim). **NOTE:** via **I-1**, a consumer's nested mutation through the un-hooked response map corrupts the cached entry and therefore what is subsequently persisted — the one thread that does connect D4 to D6, and it runs through a defect rather than a design. |
| **D5 × D6** | **I-11 (MAJOR)** — D6 enumerates the opt-in log sink and omits the two default-on ones; `Logger` defaults to `slog.Default()` (`seam.go:43,56-57`). |
| **D1 × itself** | **I-7 (MAJOR)** — wire-bytes mandate vs. a histogram assigned to a package that cannot see wire bytes. |
| **D2 × itself** | **I-10 (CRITICAL)** — per-request ≠ per-caller; 5 admitted requests → 49 995 elements. The author's own D2×D2 finding (incoming vs. merged) is correct as far as it goes and stops one step short. |
| **D3 × itself** | **I-9 (MAJOR)** — `WithAllowedCIDRs` is per-network while the sentence justifying it is per-service, and the host gate that could scope it is off by default. |
| **D4 × itself** | **I-1 (CRITICAL)** — the conditional deep copy leaves the no-hook path aliasing nested cached state. |
| **D5 × itself** | **I-2 (CRITICAL)** — the gate cannot both preserve the typed error and hide its text with `fmt.Errorf`. **I-6 (CRITICAL)** — the "machine-checked" pin invariant cannot fail on the case it exists for. |
| **D6 × itself** | **HOLDS**, with a **NOTE**: the ADR asserts **12 columns / 7 tables** as fact while phase 7 instructs *"derive the column list from the migrations at implementation time — do not copy it from the ADR."* Both are right; nothing says which wins if they differ. One sentence — *"the ADR's list is indicative; the derived list is normative and the invariant test pins it"* — removes the ambiguity before an implementer has to guess. |

## Out-of-grid: each decision against already-shipped behaviour

| pair | verdict |
|---|---|
| **D2 × ADR-0003 / 0049 / 0056** (evaluator purity, deterministic replay, opt-in timeout) | **HOLDS in substance, fails on the quantifier — I-14.** Nothing in the bundle touches `ConditionEvaluator`, `driver.conditionEval`, the shared compile cache, the `slog.Bool("conditionEval")` diagnostic, or the replay path; no already-persisted instance is re-checked. The admission move genuinely dissolves the upgrade-stranding shape audit #1 found. Only the word *"`runtime`"* is wrong. |
| **any × ADR-0095** (admin routes absent from `Mount`) | **HOLDS.** D1's per-adapter test names `POST /admin/instances/{id}/incidents/{incidentID}/resolve` and phase 6 states plainly that parity **cannot** be the net. **NOTE:** the same reasoning applies to D4 — three of its eleven paths are admin endpoints invisible to parity — and phase 3 test 8 covers them at the `httpcore` unit level, which is the right answer; it is simply never said in the D4/ADR-0095 terms D1 uses. |
| **D4 × ADR-0144** (embedded definition) | **HOLDS.** The whole template — every gateway and flow-condition expression source — is correctly identified as disclosed on the non-admin snapshot route, correctly named as **not covered**, and `service.WithoutEmbeddedDefinition` is correctly described as the only existing lever. The *position* (author-supplied process metadata a caller acting on the process may see) is stated rather than assumed, which is what this row needed. |
| **D4/D6 × ADR-0145 / 0147** (audit model) | **HOLDS.** D6 defers the integrity chain citing ADR-0145's decision that `engine.NodeVisit` carries no actor; D4 correctly notes `tasks[]` carries actor id/roles/attributes verbatim per ADR-0147 and leaves it uncovered by name. |
| **D5 × ADR-0146 / 0152 / 0183** (actionable-400 rationale) | **HOLDS on the decision, at risk on the mechanism.** The exception list preserves all three ADRs' messages, and I verified the in-code rationale it must not destroy is exactly where the bundle says it is (`transport/http/httpcore/errors.go:38-41`, `:43-46`, `:47-49`). The risk is **I-6**: the invariant meant to keep them protected as the arm evolves cannot detect the evolution. |
| **D4 × the `InstanceMapper` product feature** | **HOLDS as designed** (redaction runs *above* the mapper), and is the bundle's best single fix. **NOTE:** it is also the population most exposed by **I-1** — a mapper's job is to walk and reshape `engine.InstanceState`, and the no-hook path hands it nested cache-aliased maps. |
| **ADR-0185 leakage check** | ✅ **CLEAN — confirmed by targeted grep over all four bundle files.** `ActorFromContext`, `WithActor`, `ErrNoAuthorizer`, `EligibilitySpec`, `reassign privilege`, `Open codec`, and backlog 51/52/53 all return **0** in every file. The four `401`/`503` mentions are forward-references correctly labelled as ADR-0185's and explicitly severed (spec §0: *"They are removed"*); the two `backlog 103`/`124` mentions are in the spec's own out-of-scope delivery map. No ADR-0185 **symbol** appears. |

## Phase parallelism (plan §2) and new-symbol collisions

- **Collision found: I-5 (MAJOR).** Phase 3 changes eight (per **I-8**, eleven) exported
  signatures; every call site is in phase 4. Phase 3's own hoisted verification
  `go build ./examples/...` cannot pass, and phase 4's three parallel agents start from a
  non-building repo. Executed mutation recorded under I-5.
- **Second, softer collision: D1 × D3 row above** — phase 3's `httpcall.ErrBodyTooLarge` assertion
  compiles a package phase 5 is mutating in parallel.
- **Phases 1, 2 and 5 are genuinely independent** — `service`, `runtime/validation` +
  `definition/model/validate/expr`, and `action/httpcall` share no package and no symbol. Phase 2
  being controller-inline is correct for the reason given (it changes an error's type discipline
  phase 3 depends on) — and per **I-2** that dependency is deeper than stated.
- **Phase 4's three agents** are correctly one-per-package; `writeErr` is genuinely three separate
  functions (`stdlib/write.go:30`, `gin/write.go:11`, `fiber/write.go:11`), so *"each adapter's
  `writeErr`"* is accurate and there is no shared-file hazard.
- **New symbols: no collisions.** Executed over all non-test Go files —
  `ErrRequestBodyTooLarge`, `ErrVariablesTooLarge`, `RedactVariables`, `RedactionScope`,
  `WithMaxVariableBytes`, `WithMaxVariableElements`, `WithAllowedHosts`, `WithAllowedCIDRs`,
  `WithUnrestrictedTransport`, `WithVerboseErrorLogging`, `MaxBodyBytes` all return **0**
  occurrences. `service.Option` exists (`service/options.go:16`, `type Option func(*engineConfig)`)
  so D2's two options fit the shipped idiom. The metric name `wrkflw_rest_request_body_bytes` is
  consistent with `wrkflw_rest_requests_total` / `wrkflw_rest_request_duration_seconds`
  (`observability.go:57,61`) — but see **I-7** for where it can be recorded.

---

# Summary — the recurring shape

**Fourteen findings: 6 Critical, 5 Major, 3 Medium.** The lens's question — *"what does this
decision assume someone else will hand it, and who agreed to?"* — again earns its keep: **nine of
the fourteen** are a decision assuming a channel, a count, a package or a sentence that another
decision (or another bullet of the same decision) owns and does not provide.

But this round has a sharper, more specific shape, and it is the one to take away:

> ⭐ **The revision minted absolute claims to celebrate its own fixes, and the claims were written
> against premises the revision's *other* fixes had already changed.**

Every Critical is an instance:

| the claim the fix minted | what falsifies it | finding |
|---|---|---|
| *"the shallow copy is **all the aliasing defect needs**"* | the shipped cache clone is shallow too, so nested values alias live cached state | **I-1** |
| *"the gate **preserves** the typed error **and** renders client-safe text"* | `fmt.Errorf` cannot do both; the form that satisfies the prescribed test re-ships the leak | **I-2** |
| *"the sentinel's message **names which bound tripped**"* | D5 blanks the 413 body and its logging table has no 413 row | **I-3** |
| *"redaction is a **display** control, the allowlist a **destination** control"* | the destination control's *error* is a display, on a non-admin route | **I-4** |
| *"a new sentinel added without a row **fails the test**"* | executed: it does not, and the sentinel silently inherits `err.Error()` | **I-6** |
| *"the bound guarantees **per-request on the caller axis**"* | the caller also controls the request count: 5 requests → 49 995 elements | **I-10** |

Three second-order observations for the adjudicator:

1. **Spec §5's ✅ column is the highest-risk artifact in the bundle.** Of the 21 rows, I broke or
   qualified **nine**, and the failures cluster in a specific way: rows that resolve a *mechanism*
   question and then certify a *different* question in the same cell — routing vs. rendering (D2×D5),
   cost vs. correctness (D4 × the hot path), listing vs. counting (D4×D5 breaking), and one row
   (**I-13**) that certifies a document sentence that was never written. The table's discipline is
   good; its ✅ is doing two jobs.
2. **Two findings would have been caught by re-deriving one enumeration each** — the `mergeVars`
   sites (three named, **eight** exist, and the three omitted are the caller-supplied ones that
   carry **I-10**) and the endpoint-function count (8 named, **11** required, **I-8**). Both are
   enumerations the *revision itself* changed a neighbouring count of and did not propagate. Plan
   §4's instruction to *"assume one more is wrong"* is right; the miss is that the rot is now in the
   **second** count a fix should have touched, not in the first.
3. **The single cheapest structural fix is I-5's**: make the exported-signature thread a serial,
   controller-inline change spanning `httpcore` **and** the three adapters, exactly as CLAUDE.md
   rule #11 prescribes for a compile-breaking repo-wide change. It removes the non-building
   window, removes the phase-3/phase-4 verification contradiction, and makes **I-8**'s 8→11
   correction a one-edit change instead of a discovery in three parallel agents.

**Verdict: the bundle is not implementation-ready.** I-1, I-2, I-6 and I-10 each ship a control
that is green in its own prescribed test and does not do what the ADR says it does; I-3 and I-4
ship disclosure the delivery exists to close. None requires a new design *increment* — I-10 is the
only one where the fix (bound the merged map on the two signal/message fields) changes a decision
rather than a mechanism or a sentence.


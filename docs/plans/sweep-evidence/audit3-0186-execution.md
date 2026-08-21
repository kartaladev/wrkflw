# ADR-0186 audit round 3 — EXECUTION lens

Worktree: detached at `6cddb7b1`. Step 0: all five bundle files PRESENT.

## Summary — 23 entries: 16 findings (6 Critical, 5 Major, 5 Minor) + 7 executed confirmations

| # | severity | one line |
|---|---|---|
| E1 | Major | "outermost `ClientSafeMessage` wins" is false — `errors.As` is preorder DFS; a deep vouch in branch 1 beats a shallow one in branch 2 |
| E2 | **Critical** | the `callback` consumer opt-in is unreachable — the gate's own `ClientSafeMessage` shadows the consumer's |
| E3 | Minor | `SafeMessage("")` renders an empty 4xx `Message`; the anti-vacuity control does not cover it |
| E4 | ✅ | deny-by-default genuinely holds for the 400 render path |
| E5 | ✅ | `errors.As` survives `fmt.Errorf("%w: %w", …)` for both the interface and the json concrete types |
| E6 | Major | both "does not import transport" tests CANNOT fail — the import is an **import cycle**, executed |
| E7 | ✅ | Evidence §6.6's `ASSUMPTION (unverified)` DISCHARGED: `errors.As` reaches both json types through gin **and** fiber |
| E8 | Major | the vouch snippet vouches `err.Error()` of the OUTER error → fiber's `bind "x" from body:` prefix makes the 400 body adapter-dependent |
| E9 | **Critical** | `*json.UnmarshalTypeError` is NOT value-free — an out-of-range/fractional number echoes the caller's literal, inside the **vouched** set, live on 2 routes |
| E10 | Minor | "12 `*Input` types" is **11**; reflection walk otherwise confirms exactly one reachable custom unmarshaller |
| E11 | ✅ | all four 413 mechanics reproduce exactly (bare `*MaxBytesError` in stdlib+gin; both-sentinels→400; arm order; `Body()`=33 vs `BodyRaw()`=65253) |
| E12 | **Critical** | plan phase 4 test 6's stated FALSIFIER is inverted — with the bundle's own gzip-bomb fixture, right and wrong implementations both return 400 |
| E13 | Minor | fiber stamps 413 during decompression; unrecorded anywhere in the bundle |
| E14 | ✅ | `keywordLocation` value-free across **nine** adversarial schema shapes (bundle probed three) |
| E15 | **Critical** | `keywordLocation`-only renders `required` as `at '/required'` — destroys the ADR-0183 message the ADR claims survives |
| E16 | Minor | `BasicOutput()` renders `"validation failed"` behind a `$ref` |
| E17 | ✅ | `LocalizedString(nil)` panics; root `KeywordPath()` empty in all nine shapes |
| E18 | Minor | 403 is blanked as "policy source" while 400 publishes schema regexes; the distinction is never stated |
| E19 | Major | the 400-arm parser invariant works (3 of 4 mutations RED) but is **blind to a non-`errors.Is` arm predicate** |
| E20 | Major | "48 free-form columns" is postgres-only — sqlite has **67** (19 timestamp columns are `TEXT`) |
| E21 | **Critical** | no second column-NAME divergence exists (swept), but a systematic **TYPE** divergence does, and the prescribed invariant is names-only |
| E22 | **Critical** | the residual is NOT bounded — 404 echoes the caller's `def_ref` verbatim on `POST /instances`; 422 echoes caller ids and an arbitrary inner error |
| E23 | ✅ | 403 double-echo, bare-deny cleanliness, `Validate` value-freedom, the "0 caps" grep and the 3 discard sites all reproduce exactly |

**The three that must change the design:** E22 (residual unbounded, and its justification is the sentinel-keying the re-cut deleted), E9 (the vouched set admits caller digits), E15 (the rendering destroys the `required` message the ADR promises to keep).

⚠ **Method note for the next round.** Every one of E9, E12, E15, E20, E22 came from asking *"did this probe exercise the thing it claims about?"* of a probe the bundle had **already run and passed** — Evidence §6.3a's own lesson, applied outward. The bundle's probes are not wrong; they are **narrow**. Widening the fixture set (nine schema shapes not three; three gzip shapes not one; numeric DTO fields not string ones; all three dialects not postgres) is what produced every Critical here.


### E1 — "the outermost `ClientSafeMessage` wins" is FALSE; `errors.As` is preorder DFS, not outermost-first
**Severity:** Major
**Bundle says:** plan §3 phase 1 test 3 `TestTwoVouchedMessagesInOneChainIsDeterministic`: *"assert which one wins (the **outermost**, i.e. the first `errors.As` match)"*. Those are glossed as the same thing. They are not.
**I ran:** `zzprobe/main.go` (throwaway, deleted) transcribing the ADR's `SafeMessage`/`ClientSafeMessage` verbatim, then a chain where a **deep** implementor sits in `fmt.Errorf("%w: %w", …)`'s FIRST branch and a **shallow** one in the SECOND.
**Observed:**
```
B1 nested single-wrap          = "OUTER"                    (outermost — agrees)
B2 deep-first vs shallow-2nd   = "DEEP-IN-FIRST-BRANCH"     <-- the DEEPER one won
B4 both branches vouch         = "SENTINEL-SIDE"            (arg1 of %w: %w, not the producer)
```
`errors.As` walks `Unwrap() []error` **depth-first, preorder**: it exhausts branch 1's whole subtree before touching branch 2. So the winner is "first in preorder", which for a multi-error tree is NOT "outermost" — a 3-levels-deep vouch in branch 1 beats a top-level vouch in branch 2.
**Verdict:** CONFIRMED-DEFECT
**Fix:** (a) State the rule as **"the first match in `errors.As` preorder — branch order first, depth second"**, not "outermost", in both the ADR D2 text and `SafeMessage`'s godoc. (b) The prescribed test must include the B2 row (deep-in-first vs shallow-in-second); as worded, a test built only from B1's nested shape pins a rule the implementation does not have, and passes either way. (c) See E2 — this is not academic; phase 2's callback path hits it.

### E2 — the `callback` consumer opt-in is UNREACHABLE if the gate's own error type also implements the interface
**Severity:** Critical
**Bundle says:** ADR D2: *"a consumer whose validator returns an error implementing `ClientSafeMessage` gets that message rendered"*; plan phase 2 bullet: *"`callback` → static **unless the consumer's own error implements `ClientSafeMessage`**, in which case that message is used"*, and separately *"The gate preserves the strategy's error (`%w`) **and attaches a client-safe message** by implementing `ClientSafeMessage() string` on its own error type."*
**I ran:** the same probe, B1/B4 rows — the gate's wrapper is by construction **outer** to the consumer's error.
**Observed:** `B1 nested single-wrap = "OUTER"` — when the gate's own type implements the interface and wraps the consumer's error, `errors.As` stops at the gate's wrapper. The consumer's `ClientSafeMessage()` is never called. `B4 = "SENTINEL-SIDE"` shows the same for the multi-error shape.
**Verdict:** CONFIRMED-DEFECT — the two prescriptions are mutually exclusive as written. The gate cannot both "attach a client-safe message on its own error type" for every strategy AND let a `callback` consumer's inner vouch win, because its own wrapper shadows the inner one.
**Fix:** The gate must **not** blanket-implement the interface. It must compute the message and *delegate explicitly*: for `callback`, run `errors.As(strategyErr, &cs)` **itself** and use the consumer's message if found, else static; only then wrap. I.e. the gate's `ClientSafeMessage()` returns *the message it decided*, and the decision includes an inner lookup. Say this in the ADR — the plan's phase 2 test 5 (`TestCallbackConsumerCanVouchForItsOwnMessage`) is exactly the test that will go red against the design as written, which is good, but the design should not be shipped knowing it is red.

### E3 — nothing forbids an EMPTY vouch, and the anti-vacuity control does not cover it
**Severity:** Minor
**Bundle says:** plan phase 1 test 2: *"⚠ **Anti-vacuity control:** a row asserting the static message is **not empty** and is **not** the raw error's text."* — the control is on the **static default**, not on a vouched message.
**I ran:** same probe.
**Observed:** `D2 EMPTY vouch = ""` — `SafeMessage("", err)` renders an empty `Message`, which the wire cannot distinguish from a 5xx blank body, and which satisfies every "must not contain X" assertion in phases 1/2/4.
**Verdict:** CONFIRMED-DEFECT (minor)
**Fix:** `SafeMessage` rejects/normalises an empty message (fall back to the class default), and the invariant/test asserts a rendered 4xx `Message` is never empty. One line in `SafeMessage`.

### E4 — deny-by-default genuinely holds for the 400 render path
**Severity:** —
**I ran:** same probe, `D1`.
**Observed:** `D1 no implementor = "invalid input"` — an error wrapping only `ErrBadInput` and a raw error renders the static text, no fall-through to `err.Error()`.
**Verdict:** BUNDLE-CORRECT (for the mechanism in isolation; see E10 for the arm that still falls through).

### E5 — `errors.As` through the double wrap reaches both the interface and the `encoding/json` concrete types
**Severity:** —
**I ran:** same probe, `A1`–`A3`, `E1`, `F1`.
**Observed:**
```
A1 double-wrap render = "SAFE-A"   A2 errors.Is(ErrInvalidInput) = true   A3 errors.Is(inner) = true
E1 errors.As(*UnmarshalTypeError) through SafeMessage+double wrap = true
F1 errors.As(*MaxBytesError) = true
```
**Verdict:** BUNDLE-CORRECT — `fmt.Errorf("%w: %w", …)` preserves `errors.Is`/`errors.As` in both directions, and `SafeMessage`'s single `Unwrap` does not break the concrete-type lookup phase 4 depends on.

### E6 — the two prescribed "does not import transport" tests CANNOT fail; the compiler already refuses the import
**Severity:** Major
**Bundle says:** plan phase 2 test 7 `TestValidationDoesNotImportTransport` — *"**Falsifier:** it fails the moment someone adds the convenience import, which is the one edit that silently inverts the layering"*; plan phase 3 test 3 repeats it for `runtime/kernel`; plan §2 note *"If an implementer finds they need the import, **stop and escalate**"*.
**I ran:** actually added the import and built.
```
$ # gate.go += import "…/transport/http/httpcore"; var _ = httpcore.ErrBadInput
$ go build ./runtime/validation/ ; echo EXIT=$?
```
**Observed:**
```
EXIT=1
package github.com/kartaladev/wrkflw/runtime/validation
	imports github.com/kartaladev/wrkflw/transport/http/httpcore from gate.go
	imports github.com/kartaladev/wrkflw/runtime/validation from errors.go: import cycle not allowed
```
Plus, per `go list -deps`, **4 of the 5 `transport/...` packages** (`httpcore`, `stdlib`, `gin`, `fiber`) transitively import BOTH `runtime/validation` and `runtime/kernel`; only `transport/http/parity` does not.
**Verdict:** CONFIRMED-DEFECT — the import is **not silent**, it does not compile, and therefore the test's stated falsifier is false: the build breaks before the test can run. This is the third "machine-checked invariant that cannot fail" in this lineage, and the plan's own §0 item 4 warns about exactly that.
**Fix:** Either (a) delete both tests and replace the prose with *"the import is structurally impossible — `httpcore` imports `runtime/validation` and `runtime/kernel`, so the reverse edge is an import cycle; executed"*, which is a **stronger** guarantee than a test and should be stated as the reason the structural-satisfaction argument holds; or (b) keep them but restate the falsifier honestly ("guards only the `transport/http/parity` edge, which is not a cycle") and drop the "stop and escalate" line, since an implementer cannot get that far. Option (a) is right; the ADR should carry the executed cycle proof as the layering argument.

### E7 — Evidence §6.6's `ASSUMPTION (unverified)` is DISCHARGED: `errors.As` reaches both `encoding/json` types through gin's and fiber's binders
**Severity:** — (assumption discharged; bundle should be updated to say so)
**Bundle says:** Evidence §6.6: *"⚠ `ASSUMPTION (unverified)`: that gin's `ShouldBindJSON` and fiber's `c.Bind().JSON` preserve these concrete types through `errors.As`."*; plan phase 4 test 7 is prescribed to discharge it.
**I ran:** `zzprobe` driving the **real** `httpcore.StartInput` through all three real idioms — `json.NewDecoder`, a real `gin.Context.ShouldBindJSON` via `gin.CreateTestContext`, and a real `fiber.App` route calling `c.Bind().JSON` via `app.Test`.
**Observed (gin and fiber rows only; stdlib identical):**
```
gin   {"def_ref": 4111111111111111}   UTE=true  SE=false concrete=*json.UnmarshalTypeError
gin   {"def_ref": "ok" xx             UTE=false SE=true  concrete=*json.SyntaxError
gin   {"def_ref": "kyc:ssn-…"}        UTE=false SE=false concrete=*fmt.wrapErrors
fiber {"def_ref": 4111111111111111}   UTE=true  SE=false concrete=*fiber.BindError
      msg="bind \"def_ref\" from body: json: cannot unmarshal number into Go struct field StartInput.def_ref of type string"
fiber {"def_ref": "ok" xx             UTE=false SE=true  concrete=*fiber.BindError
fiber {"def_ref": "kyc:ssn-…"}        UTE=false SE=false concrete=*fiber.BindError
      msg="bind from body: workflow-model: invalid qualifier: bad version in \"kyc:ssn-123-45-6789\": strconv.Atoi: parsing \"ssn-123-45-6789\": invalid syntax"
```
**Verdict:** BUNDLE-CORRECT. gin returns the bare concrete types; fiber wraps in `*fiber.BindError` but `errors.As` sees through it. The three-way split holds in all three adapters.
**Fix:** Replace §6.6's `ASSUMPTION (unverified)` with this executed result (it is no longer unverified), and note the **new fact it exposes**: fiber's outer error text differs (`bind "def_ref" from body: …` / `bind from body: …`), so a parity test asserting an exact 400 message across adapters will fail — see E8.

### E8 — the phase-4 vouch snippet vouches for `err.Error()` of the OUTER error, which differs per adapter; parity on the 400 message body is impossible as written
**Severity:** Major
**Bundle says:** plan phase 4: `err = httpcore.SafeMessage(err.Error(), err)   // vouched: field path + Go types only`. Plan phase 5: parity cases assert all three adapters agree; ADR D2 table row 400: *"the `ClientSafeMessage` if any error in the chain provides one"*.
**I ran:** same probe (E7 output).
**Observed:** for the identical body `{"def_ref": 4111111111111111}` the vouched text would be
- stdlib/gin: `json: cannot unmarshal number into Go struct field StartInput.def_ref of type string`
- fiber: `bind "def_ref" from body: json: cannot unmarshal number into Go struct field StartInput.def_ref of type string`

and for a syntax error fiber prefixes `bind from body: `.
**Verdict:** CONFIRMED-DEFECT (a divergence the bundle nowhere states). Two consequences: (i) phase 5's parity suite cannot assert a common 400 message for the vouched decode path, and the plan does not carve it out the way it carves out the fiber `DefaultBodyLimit` case; (ii) `bind "def_ref" from body` re-emits the **JSON field name**, which is DTO-author-supplied, not caller-supplied, so it is not a disclosure — but it *is* an undocumented wire difference.
**Fix:** Vouch for the **typed error's** text, not the outer's: `errors.As(err,&ute)` then `SafeMessage(ute.Error(), err)` (and likewise `se.Error()`). That makes the three adapters byte-identical and removes fiber's prefix from the wire. Add a parity row asserting the three 400 bodies are **equal** for `{"def_ref": 4111111111111111}` — currently the plan asserts nothing about it.

### E9 — ⛔ `*json.UnmarshalTypeError` is NOT value-free: an out-of-range or fractional number echoes the caller's LITERAL verbatim, and it is inside the VOUCHED set
**Severity:** Critical
**Bundle says:** ADR-0186 Decision 2, the conditional-vouch table:
> | wrong JSON type for the field | `*json.UnmarshalTypeError` | **vouch** — renders the field path and Go types only |

and *"`*json.UnmarshalTypeError` names only the field path and Go type names and **has no such caveat**"* (Evidence §6.6); plan phase 4 ships `errors.As(err,&ute) → SafeMessage(err.Error(), err) // vouched: field path + Go types only`.
**I ran:** real `httpcore` DTOs with numeric fields (`ResolveIncidentInput.AddAttempts int`, `RedriveInput.IDs []int64`) through real `json.NewDecoder`:
**Observed:**
```
{"add_attempts": 99999999999999999999}    vouched=true  msg="json: cannot unmarshal number 99999999999999999999 into Go struct field ResolveIncidentInput.add_attempts of type int"
{"add_attempts": 4111111111111111.5}      vouched=true  msg="json: cannot unmarshal number 4111111111111111.5 into Go struct field ResolveIncidentInput.add_attempts of type int"
{"ids": [1, 4111111111111111111111]}      vouched=true  msg="json: cannot unmarshal number 4111111111111111111111 into Go struct field RedriveInput.ids of type int64"
{"ids": [123456789012345678901234567890]} vouched=true  msg="json: cannot unmarshal number 123456789012345678901234567890 into Go struct field RedriveInput.ids of type int64"
{"actor": {"id": 4111111111111111}}       vouched=true  msg="json: cannot unmarshal number into Go struct field Actor.actor.id of type string"   <-- this one IS value-free
```
`encoding/json` sets `UnmarshalTypeError.Value = "number " + <the input literal>` when a number does not fit the target numeric type (range or fractional); for a plain kind mismatch it sets just `"number"`. So the vouched set **admits caller digits verbatim, today, on two live routes** — `POST /admin/instances/{id}/incidents/{id}/resolve` and `POST /admin/dead-letters/redrive`.
**Verdict:** CONFIRMED-DEFECT — this is precisely the disclosure class the whole delivery exists to close, admitted by the delivery's own allow rule. It is also the exact shape Evidence §6.3's own warning describes (*"adding one `time.Time` field silently converts 36 value-free 400s into value-echoing ones"*) — except it does not need a future edit: it is live now, and the bundle asserts the opposite with an "no such caveat" absolute.
**Fix (concrete, and it is small):** do **not** vouch for `err.Error()`. Vouch for a message the transport **constructs** from the typed error's structured fields, which are individually safe:
```go
var ute *json.UnmarshalTypeError
if errors.As(err, &ute) {
    // ute.Value is caller-derived when the number is out of range — never render it.
    msg := fmt.Sprintf("invalid value for field %q: expected %s", ute.Field, ute.Type)
    err = httpcore.SafeMessage(msg, err)
}
```
`ute.Field` and `ute.Type` are DTO-author-derived; `ute.Value` and `ute.Offset` are not. This also fixes E8 (adapter-divergent text) for free, because the message is constructed rather than inherited. Add a plan phase-4 test row: `{"add_attempts": 99999999999999999999}` → the 400 body must NOT contain `99999999999999999999`. **That row is the falsifier for this fix and fails against every version of the design as currently written.**

### E10 — the plan's "12 `*Input` types" is 11; the reflection walk otherwise confirms exactly one reachable custom unmarshaller
**Severity:** Minor
**Bundle says:** plan §0 item 2 *"There are 12 `*Input` types"*; §4 table *"custom `UnmarshalJSON` reachable from a decode target | **1 of 3 in the repo** — `model.Qualifier` (via `StartInput.DefRef`). `ProcessDefinition` and `NodeKind` are not reachable from any of the 12 `*Input` types"*.
**I ran:** (a) a `go/parser`-free machine walk pairing each of stdlib's 13 `json.NewDecoder` sites with its nearest `var in <Type>`; (b) a **reflection** walk (depth 8, cycle-guarded) over every decode target looking for `json.Unmarshaler` / `encoding.TextUnmarshaler` / `time.Time` / `json.Number`.
**Observed:**
```
stdlib decode sites: 13     distinct target types: 11
  StartInput, SignalInput, MessageInput, ClaimInput, CompleteInput, ReassignInput,
  ResolveIncidentInput, ResolveCompensationStallInput, RedriveInput,
  PolicyRuleInput x2, RoleBindingInput x2
decode target types: 11
== StartInput
   !! json.Unmarshaler at StartInput.DefRef  (model.Qualifier)
(no other hit; no time.Time / json.Number anywhere)
```
**Verdict:** CONFIRMED-DEFECT on the count (**11**, not 12 — `dto.go` declares 11 `*Input` types; the 13 sites are 11 distinct types with `PolicyRuleInput` and `RoleBindingInput` each decoded twice). BUNDLE-CORRECT on the substance: exactly one custom unmarshaller is reachable, and no DTO carries `time.Time` or `json.Number`.
**Fix:** say **11** in plan §0 item 2 and §4, and add the fact the walk supplies and the greps did not: *the count of decode **sites** (13) exceeds the count of decode **types** (11) because `PolicyRuleInput` and `RoleBindingInput` are each decoded on two routes*. Also record the walk itself — a reflection walk is the reliable net here and a `grep` for `UnmarshalJSON` is not, since reachability is what matters.

### E11 — the 413 mechanics are all BUNDLE-CORRECT (four separate claims executed)
**Severity:** —
**Bundle says:** ADR D1 — stdlib and gin surface the **bare `*http.MaxBytesError`** ("two shapes, not three"); an error wrapping both `ErrBadInput` and the new sentinel classifies **400**; the switch is **ordered**; `c.Body()` sees **33** on a gzip bomb where `c.BodyRaw()` sees the wire bytes.
**I ran:** real `http.MaxBytesReader` through `json.NewDecoder` and a real `gin.Context.ShouldBindJSON`; real `httpcore.ClassifyError`; a real `fiber.App` with a 64 MiB→gzip body.
**Observed:**
```
stdlib  errors.As(*MaxBytesError)=true concrete=*http.MaxBytesError msg="http: request body too large"
gin     errors.As(*MaxBytesError)=true concrete=*http.MaxBytesError msg="http: request body too large"
classify(ErrBadInput + newSentinel)          = 400 {bad_request ...}
classify(bare newSentinel)                   = 500 {internal_error }
classify(ErrConcurrentUpdate + ErrBadInput)  = 409     } arm order decides,
classify(ErrBadInput + ErrConcurrentUpdate)  = 409     } NOT wrap order
fiber   gzip wire bytes = 65253 (expands to 67108864)
fiber   len(Body())=33 len(BodyRaw())=65253 Body()="body size exceeds the given limit"
```
**Verdict:** BUNDLE-CORRECT on all four. The 33 is exact, the ordering claim is exact, and the "bare `*http.MaxBytesError` in both stdlib and gin" claim is exact.

### E12 — ⛔ plan phase 4 test 6's stated FALSIFIER is INVERTED, and the fixture the bundle narrates makes the test pass against the implementation it exists to reject
**Severity:** Critical
**Bundle says:** plan §3 phase 4 test 6: *"`TestCompressedBodyOverTheCapReturns413` — a gzip body whose **wire** size is under the cap must **not** 413 (wire bytes is the contract) … ⚠ **Falsifier:** *the first row fails against a `len(c.Body())` pre-check, which sees 33.*"* The "sees 33" fixture is the 63.7 KiB→64 MiB bomb, narrated in ADR D1, plan §3 phase 4 and Evidence.
**I ran:** a real `fiber.App` with the **exact real adapter shape** (pre-check → `c.Bind().JSON` → `writeErr` = `c.Status(s).JSON(b)`), two routes differing only in `len(c.BodyRaw())` vs `len(c.Body())`, cap 1 MiB.
**Observed:**
```
fixture                                        impl   wire      status
BOMB gzip 64MiB (bundle's narrated fixture)    /raw   65287     400   <-- CORRECT impl
BOMB gzip 64MiB (bundle's narrated fixture)    /dec   65287     400   <-- WRONG impl: IDENTICAL
MODEST gzip 2MiB (wire<cap, decompressed>cap)  /raw   2106      200   <-- CORRECT impl
MODEST gzip 2MiB (wire<cap, decompressed>cap)  /dec   2106      413   <-- WRONG impl: DIFFERS
```
**Verdict:** CONFIRMED-DEFECT. With the bomb, `len(c.Body())` **is 33, which is UNDER the cap**, so the wrong implementation does **not** 413 — row 1 ("must not 413") is **green against the wrong implementation**. The falsifier says the opposite. The fixture that actually discriminates is the reverse shape: **wire under the cap, decompressed OVER the cap** (2 MiB of repeated bytes → 2,106 wire bytes → `Body()` = 2,097,152).
**Fix:** rewrite plan phase 4 test 6 with **three** rows and name the fixtures:
1. wire ≈ 2 KiB, decompressed 2 MiB → must be **200/400, not 413**. *Falsifier: fails against `len(c.Body())`.* ← this is the row that does the work.
2. wire > 1 MiB (use **incompressible** data; keep it under `fiber.DefaultBodyLimit` 4 MiB) → must **413**.
3. the 64 MiB bomb → document that **both** implementations return 400 and that this fixture is **non-discriminating**; keep it only as a residual-behaviour pin, and say so, or the next reader re-derives the wrong falsifier.
Also correct ADR D1's narrative: the "33" observation motivates `BodyRaw()` correctly, but it is **not** the case that a `c.Body()` pre-check turns the bomb into a 400 *instead of* a 413 — executed, the correct implementation also returns 400 on the bomb. Both are 400; the bomb is simply not stopped by either, which the ADR already concedes as a residual. The sentence *"a `c.Body()` pre-check passes it through to a **400**, not a 413, on precisely the amplification case it exists for"* implies `BodyRaw()` fixes that case. It does not.

### E13 — fiber stamps 413 on the response itself during decompression; a handler that does not set a status inherits it
**Severity:** Minor
**Bundle says:** nothing — this behaviour is unrecorded anywhere in the bundle.
**I ran:** the same fiber app with a handler that calls `c.Body()` and returns `c.SendString(...)` **without** setting a status.
**Observed:** `BOMB … /bodyraw wire=65253 status=413 200 raw=65253 body=33` — the handler's own 200-path body is returned under HTTP **413**, because fiber's bounded gunzip set the status before the handler ran. The real adapters happen to mask this because `writeErr` always calls `c.Status(...)` explicitly, and the success path calls `c.Status(status).JSON(body)`.
Separately: a gzip whose **wire** size exceeds `fiber.DefaultBodyLimit` never yields an HTTP response at all — `app.Test` returns the transport error `body size exceeds the given limit`.
**Verdict:** CONFIRMED-DEFECT (documentation gap, low severity — no live path is wrong).
**Fix:** one sentence in ADR D1's fiber paragraph: *"fiber sets 413 on the response during decompression; every `wrkflw` fiber handler sets a status explicitly, so this is masked — but a phase-4 test that asserts on a handler which does not set one will read 413 regardless of the cap."* This is what makes E12's test rows readable to whoever writes them.

### E14 — `keywordLocation` survives an adversarial sweep: value-free in all NINE schema shapes probed
**Severity:** —
**Bundle says:** ADR D2 / Evidence §2 — `keywordLocation` is value-free **by construction**; only three shapes were probed (`pattern`+`maxLength`, `propertyNames`, array item).
**I ran:** the **real** in-repo `vjs.New(schema).NewValidator()` against nine shapes chosen to break it: `patternProperties`, `$ref`→`$defs`, `dependentSchemas`, `unevaluatedProperties`, `propertyNames`, `anyOf`, `enum`, `additionalProperties:false`, `required` — every one fed a card number or an SSN.
**Observed (keywordLocation column only):**
```
/patternProperties/%5Essn_%5B0-9%5D%7B3%7D-%5B0-9%5D%7B2%7D$/type
/properties/card/$ref/maximum
/dependentSchemas/pan/required
/unevaluatedProperties
/additionalProperties/type   /propertyNames/propertyNames   /propertyNames/maxLength
/properties/v/anyOf   /properties/v/anyOf/0/type   /properties/v/anyOf/1/type
/properties/k/enum
/additionalProperties
/required
```
No submitted value appears in any of them — including the two shapes where the caller's key **does** appear in `instanceLocation` (`/4111-1111-1111-1111`) and in the vendor's `err` text (`additional properties '4111-1111-1111-1111' not allowed`).
**Verdict:** BUNDLE-CORRECT, and now on a much wider basis than Evidence §2's three shapes. **Fold this table into Evidence §2** — it converts "value-free by construction" from an argument into a measurement over the shapes an attacker would reach for.

### E15 — ⛔ `keywordLocation`-only DESTROYS the `required` message — the exact class ADR-0183/0152 added and this bundle promises to preserve
**Severity:** Critical
**Bundle says:** ADR-0186 Consequences/Negative lists exactly one ergonomics cost: *"400 loses the **array index** in validation messages (the author-derived schema location does not carry it)."* And Positive claims *"**The actionable 400 messages ADR-0146, ADR-0152 and ADR-0183 added survive**"*. ADR-0183's in-code rationale (`errors.go:47-49`) is *"a required field the caller omitted and can supply"*.
**I ran:** the same nine-shape sweep; the rows that matter are `required`, `additionalProperties:false`, `unevaluatedProperties`, `dependentSchemas`.
**Observed:**
```
required            kwLoc="/required"                  err="missing property 'a'"
addlProps:false     kwLoc="/additionalProperties"      err="additional properties '4111-1111-1111-1111' not allowed"
unevaluatedProps    kwLoc="/unevaluatedProperties"     err="false schema"
dependentSchemas    kwLoc="/dependentSchemas/pan/required"  err="missing property 'cvv'"
```
Under the prescribed rendering the caller receives **`at '/required'`** and nothing else. It does not name which property is missing. The missing property's name (`'a'`, `'cvv'`) is **author-derived — it is declared in the schema, not submitted by the caller — and is therefore perfectly safe to render**, yet the `keywordLocation`-only rule discards it.
**Verdict:** CONFIRMED-DEFECT, and it is a bigger loss than the one cost the ADR concedes. A missing-required-field 400 is the single most common validation failure on any API; rendering it as `at '/required'` is not an actionable message, and it silently undoes ADR-0183's stated purpose on the jsonschema path while the ADR claims the opposite in its Positive section.
**Fix:** the rule is one clause too broad. Render **`keywordLocation` + the author-derived operand of the failing keyword**, where the operand is safe by kind:
- `required` / `dependentSchemas` → the **missing property name** (schema-declared);
- `enum` / `const` → the **allowed set** (schema-declared — executed: `err="value must be one of 'a', 'b'"` is already value-free);
- `additionalProperties` / `unevaluatedProperties` / `propertyNames` → **keywordLocation only**, because their operand *is* the caller's key;
- `pattern` / `maxLength` / `minimum` / `type` → **keywordLocation only** (their `err` carries the submitted value or its length).
That is a four-line switch on `e.Error` / `ErrorKind`'s concrete type in `runtime/validation`, and it keeps deny-by-default: unknown kind → keywordLocation only. Add a plan phase-2 test row: *a schema with `required:["a"]` fed `{}` must produce a message naming `a`, and must not contain any submitted value.* **That row fails against the design exactly as written**, which is the point.

### E16 — `BasicOutput()` drops the leaf detail behind a `$ref`, rendering `"validation failed"`
**Severity:** Minor
**Bundle says:** ADR D2: *"⚠ Use `BasicOutput()`, **not the root error** … The usable leaves are in `.Causes`, recursively."*
**I ran:** the sweep, `$ref/$defs` row.
**Observed:**
```
$ref/$defs   ROOT msg = "- at '/card': maximum: got 4.111111111111111×10¹⁵, want 10"
             kwLoc="/properties/card/$ref/maximum"   err="validation failed"
```
The leaf `err` behind a `$ref` is the placeholder `"validation failed"`; the real kind (`maximum`) survives only in `keywordLocation`. (The root message, correctly rejected by the ADR, echoes the caller's value in scientific notation with a Unicode `×`.)
**Verdict:** BUNDLE-CORRECT on "do not use the root error"; a gap the bundle does not mention. Under the fix proposed in E15 the `$ref` case degrades to keywordLocation-only, which is the safe default — so this is informational, but it should be a **test row** so nobody later "fixes" it by falling back to `Error.String()`.
**Fix:** add a `$ref` fixture to plan phase 2 test 1 asserting the rendering is non-empty and contains `maximum` (from the keyword location) and **not** `4111111111111111`.

### E17 — `ErrorKind.LocalizedString(nil)` panics: BUNDLE-CORRECT, reproduced
**Severity:** —
**I ran:** real validator, `errors.As` → `*jsonschema.ValidationError`, then `ve.ErrorKind.LocalizedString(nil)`.
**Observed:** `PANIC: runtime error: invalid memory address or nil pointer dereference`
**Verdict:** BUNDLE-CORRECT. Also confirmed in the same run: the **root** `ValidationError.ErrorKind.KeywordPath()` is `[]` (empty) in **all nine** shapes, so the ADR's warning about a literal root rendering producing `at '/': violates ` is correct and general, not shape-specific.

### E18 — the 403 rationale and the 400 vouch disagree about author-supplied source, and the bundle does not say so
**Severity:** Minor
**Bundle says:** D2 blanks 403 because *"the leaked string is the deployment's own policy source"*; D2 vouches `keywordLocation` because it is *"author-derived"*.
**I ran:** the `patternProperties` row.
**Observed:** `kwLoc="/patternProperties/%5Essn_%5B0-9%5D%7B3%7D-%5B0-9%5D%7B2%7D$/type"` — the definition's **regex, percent-encoded, on the wire to an anonymous caller**. Likewise `/dependentSchemas/pan/required` puts a schema property name on the wire, and `$defs` names would too.
**Verdict:** CONFIRMED-DEFECT (documentation/consistency, not a new leak). Both are author/deployment-supplied structure; one is blanked as a policy leak, the other is published as safe. The distinction is defensible (a validation schema is part of the API contract; an authz predicate is not) but it is **nowhere stated**, and D2's own justification for 403 reads as a general principle that D2's 400 rule then violates.
**Fix:** one sentence in D2: *"`keywordLocation` publishes the definition's schema structure — property names, `patternProperties` regexes (percent-encoded), `$defs` names — to the caller. That is deliberate: a request schema is part of the API contract a caller must satisfy. An authz predicate is not, which is why 403 is blanked and 400 is not."*

### E19 — the 400-arm `go/parser` invariant IS implementable and goes RED on three mutations — but it is BLIND to a non-`errors.Is` arm predicate
**Severity:** Major
**Bundle says:** ADR D2 / plan phase 1 test 5: *"The test therefore **parses `httpcore/errors.go`** and asserts that the set of sentinels named in each 4xx arm equals the set with a row in the policy table"*, motivated by *"a new sentinel joined the 400 arm, the pin passed, and it shipped `\"rejected value 4111-1111-1111-1111\"`"*.
**I ran:** I wrote the invariant (walk `ClassifyError`'s `*ast.CaseClause`s, map each arm's returned status constant → the set of `errors.Is(err, X)` second arguments), ran it against the real `transport/http/httpcore/errors.go`, then against four textual mutations of it.
**Observed:**
```
== extracted from the REAL file ==
   http.StatusBadRequest          [ErrBadInput engine.ErrEmptyReassignTarget engine.ErrEmptyTriggerKey
                                   engine.ErrInvalidOutcome engine.ErrOutcomeRequired
                                   kernel.ErrBadArmedTimerCursor kernel.ErrBadCursor validation.ErrInvalidInput]  (8)
   http.StatusConflict            [kernel.ErrConcurrentUpdate]
   http.StatusForbidden           [authz.ErrNotAuthorized]
   http.StatusNotFound            [humantask.ErrTaskNotFound kernel.ErrDefinitionNotFound kernel.ErrInstanceNotFound]
   http.StatusUnprocessableEntity [engine.ErrInvalidTransition humantask.ErrInvalidTask service.ErrConflict]
   http.StatusInternalServerError []

MUT1 new sentinel in 400 arm       FAIL   <- RED (the exact rot the pin exists to catch)
MUT2 new 413 arm, no policy row    FAIL   <- RED
MUT3 sentinel moved 422 -> 409     FAIL   <- RED
MUT4 sentinel behind a HELPER      (output IDENTICAL to baseline)  <- ⛔ NOT RED
```
MUT4 replaced `errors.Is(err, engine.ErrEmptyReassignTarget)` with `errors.Is(err, engine.ErrEmptyReassignTarget), isNewLeak(err)` — i.e. a new arm predicate that is not a literal `errors.Is` call. The walk sees no new sentinel and passes.
**Verdict:** BUNDLE-CORRECT that the invariant is buildable and non-vacuous (three of four mutations red — and it confirms the **8 sentinels across the 400 arm**, matching the ADR). CONFIRMED-DEFECT on completeness: the shape the previous pin died of was "a form the checker did not model", and this checker has exactly one such blind spot.
**Fix, three clauses the plan must state, because none is implied by "extracts the sentinel identifiers named in each arm":**
1. **Assert the arm predicate SHAPE**: every expression in every `case` list must be an `errors.Is(err, <ident|selector>)` call; anything else — a helper call, a type assertion, an `errors.As` — is a **failure**, not a skip. Without this, MUT4 ships.
2. **The equality must be BIDIRECTIONAL**: an arm present in source with no policy row must fail (this is what caught MUT2, the future 413/401/503 arms) *and* a policy row with no arm must fail. The plan says "each arm … equals the policy table's", which reads one-directional.
3. **Whitelist the `default` arm explicitly** — it legitimately has zero sentinels; my run flagged it every time, which is the noise that makes a reviewer weaken the check.
Also: the self-test fixture prescribed for this test should be the **MUT4 shape**, not the MUT1 shape. MUT1 is caught by the obvious implementation; MUT4 is the one that distinguishes a real invariant from the previous one.

### E20 — "48 free-form columns" is a POSTGRES-ONLY figure; by the same rule SQLite has 67. The one-dialect blind spot the re-cut says it fixed is still in the number it fixed it with
**Severity:** Major
**Bundle says:** ADR-0186 Context §3 *"**48 columns** carry a free-form type (`TEXT`/`JSON`/`JSONB`)"*; plan §4 *"at-rest: free-form columns | ⭐ **48**"*; Evidence §6.1 *"TOTAL payload-typed columns: 48 across 9 tables"* with the note *"(postgres types shown)"* — and the very next paragraph explains the previous rot as *"every round enumerated columns from one dialect and assumed the other two matched."*
**I ran:** my own SQL walk over all three `0001_init.sql` files, applying the identical free-form rule (`TEXT`/`JSON`/`JSONB`/`VARCHAR`/`CHAR`/`CLOB`/`BLOB`/`LONGTEXT`/`MEDIUMTEXT`) **per dialect** rather than reading postgres and assuming.
**Observed:**
```
TOTAL free-form (postgres): 48
TOTAL free-form (mysql):    48
TOTAL free-form (sqlite):   67
columns free-form in SQLITE but not in postgres: 19
  wrkflw_call_links.{created_at,notified_at,claimed_at}   pg=TIMESTAMPTZ  my=DATETIME(6)  sq=TEXT
  wrkflw_chain_links.created_at, wrkflw_definitions.created_at
  wrkflw_human_task.{claimed_at,completed_at,created_at,due_at}
  wrkflw_instances.{started_at,ended_at,updated_at}
  wrkflw_journal.{occurred_at,applied_at}
  wrkflw_outbox.{created_at,published_at,next_attempt_at}
  wrkflw_processed_message.processed_at, wrkflw_timers.next_run
```
**Verdict:** CONFIRMED-DEFECT. SQLite has no native timestamp type, so all 19 timestamp columns are `TEXT` there. "48" is true of postgres and of mysql, and false of the schema. This is the **fourth** occurrence of this enumeration being wrong (2 → "at least six" → 12 → 48), and the fourth is the same mistake in a new dimension: derived from one dialect, asserted of the schema, one paragraph after diagnosing exactly that.
**Fix:** state it as *"**48** in postgres and mysql; **67** in sqlite, because SQLite stores its 19 timestamp columns as `TEXT`"* — or better, drop the number entirely from prose (the ADR already argues a raw count is the wrong deliverable) and let the generator emit it per dialect. Amend ADR Context §3, plan §4, and Evidence §6.1.

### E21 — the SECOND per-dialect divergence the bundle asks for EXISTS, is systematic, and the prescribed invariant is names-only so it will NOT catch it
**Severity:** Critical
**Bundle says:** ADR Consequences: *"**New item: a second per-dialect schema-name divergence, if one exists.** Decision 3's invariant catches it going forward; nothing has swept the existing schema for siblings of `trigger_`."*; plan §0 item 5 *"look for a **second** per-dialect divergence … assume the class is not exhausted"*; plan phase 6 test 2 is `TestDialectsAgreeOnColumn**Names**`; ADR D3 *"It also asserts the three dialects agree on **table and column names**"*.
**I ran:** the same three-dialect walk, comparing (a) table sets, (b) full column-name lists per table, (c) declared **types** per column.
**Observed:**
```
TABLES per dialect: {postgres: 9, mysql: 9, sqlite: 9}   table set identical: True
column-NAME divergences: 1   (wrkflw_journal.trigger / trigger_ — the known one; NO second name divergence)
column-TYPE divergences: systematic and large, e.g.
  wrkflw_human_task.claim_actor   pg=JSONB   my=JSON        sq=TEXT
  wrkflw_human_task.state         pg=TEXT    my=VARCHAR(64) sq=TEXT
  wrkflw_instances.snapshot       pg=JSONB   my=JSON        sq=TEXT
  wrkflw_instances.status         pg=SMALLINT my=SMALLINT   sq=INTEGER
  wrkflw_outbox.next_attempt_at   pg=TIMESTAMPTZ my=DATETIME(6) sq=TEXT
  wrkflw_outbox.id                pg=BIGSERIAL my=BIGINT    sq=INTEGER
```
**Verdict:** CONFIRMED-DEFECT, two parts. (i) I can report a **negative** the bundle wanted: there is **no second column-NAME divergence** — I swept all 9 tables in all 3 dialects and `trigger_` is the only one. That closes the ADR's open follow-up item with a measurement instead of a guess. (ii) But the divergence class **is** unexhausted in the **type** dimension, and it is exactly the dimension a consumer acting on D3's list needs: the column-level-encryption `ALTER` a consumer writes depends on the declared type, and `JSONB` / `JSON` / `TEXT` are three different statements. The prescribed invariant is names-only and will pass over every one of these.
**Fix:** (a) record the swept negative in the ADR — *"executed: `trigger_` is the only column-name divergence across all 9 tables and 3 dialects"* — and change the open follow-up from "if one exists" to a closed answer. (b) Widen plan phase 6 test 2 from `TestDialectsAgreeOnColumnNames` to also record the **per-dialect type triple** for every column, with the systematic families (`JSONB/JSON/TEXT`, `TIMESTAMPTZ/DATETIME(6)/TEXT`, `TEXT/VARCHAR(n)/TEXT`, `BIGSERIAL/BIGINT/INTEGER`) in a stated allow-list, so that a **new** unexplained type divergence fails. (c) `SECURITY.md`'s generated list must carry the type **per dialect**, not one column of types — otherwise it repeats E20's error in the document the consumer actually acts on.

### E22 — ⛔⛔ THE RESIDUAL IS NOT BOUNDED: 404 echoes the caller's `def_ref` VERBATIM on `POST /instances`, and 422 echoes caller ids and an arbitrary inner error. The residual's justification repeats the exact error the re-cut exists to fix.
**Severity:** Critical
**Bundle says:** ADR D2: *"⚠ **The bounded residual, stated rather than implied.** 404, 409 and 422 also render `err.Error()` and were **not** proven to leak. They keep it here, for a reason that is checkable rather than hopeful: each matches a **small closed set of engine sentinels with no open extension point**, whereas the 400 arm matches eight sentinels …"*; Consequences: *"404, 409 and 422 keep `err.Error()` in this delivery. **Bounded** by the parser invariant …"*. Plan §0 item 3 asks the audit to attack exactly this.
**I ran:** (1) enumerated every non-test producer of each residual sentinel by grep; (2) drove the real producers and the real `httpcore.ClassifyError`, including the real `POST /instances` decode path (`json.Decode` into `httpcore.StartInput` → `registry.Lookup(in.DefRef)`).
**Observed:**
```
404 MemDefinitionRegistry.Lookup  -> 404 {"error":"not_found","message":"workflow-runtime: definition not found in registry: \"kyc-ssn-123-45-6789:7\""}
404 MapDefinitionRegistry.Lookup  -> 404 {"error":"not_found","message":"workflow-runtime: definition not found in registry: \"4111-1111-1111-1111\""}
422 humantask.Validate            -> 422 {"error":"conflict_state","message":"workflow-humantask: invalid task: task \"tok-4111-1111-1111-1111\": unknown state 99"}
422 ErrConflict %q instance id    -> 422 {"error":"conflict_state","message":"workflow-service: conflicting state: instance \"inst-123-45-6789\" is in a terminal state"}
422 ErrConflict %w: %w passthrough-> 422 {"error":"conflict_state","message":"workflow-service: conflicting state: json: unknown field \"ssn-123-45-6789\""}
```
Producing sites, enumerated (this is the "count them again" net the bundle asks for):
- **404 / `kernel.ErrDefinitionNotFound` — 4 wrap forms, ALL of them format the caller's qualifier:** `runtime/kernel/definition_registry.go:56` `%w: %q` on `q`; `runtime/kernel/mem_definition_registry.go:112` `%w: %q` on `q`; `internal/persistence/store/definitions.go:150` `%w: %s:%d` on `defID, version`; `:186` `%w: %s` on `q`. `q` is `StartInput.DefRef` — **the caller's request body**.
- **404 / `ErrInstanceNotFound`, `ErrTaskNotFound`** — no value-bearing wrap found (`engine/step_triggers.go:888` formats `t.TaskID`, server-side).
- **409 / `ErrConcurrentUpdate`** — bare `errors.New`, no wrap site. **Genuinely clean.**
- **422 / `humantask.ErrInvalidTask` — 3 forms, all `%q` on `t.TaskID`** (`humantask/validate.go:47,51,53`).
- **422 / `service.ErrConflict` — 6 forms**: `service/service.go:377,540,593,600` `%q` on `req.InstanceID` / `taskID` (caller-supplied path parameters); `:549` `%w: cancel instance %q: %w`; **`:605` `fmt.Errorf("%w: %w", ErrConflict, err)` — the identical `%w: %w`-over-an-arbitrary-inner-error shape that made `ErrBadCursor` leak.**
- **422 / `engine.ErrInvalidTransition`** — 9 package-level `fmt.Errorf` constants, all static. **Genuinely clean.**
**Verdict:** CONFIRMED-DEFECT, and it is the most important finding in this audit. Three things are wrong:
1. **The factual claim is false.** "404, 409 and 422 … were **not** proven to leak" — they leak, executed above, on live routes.
2. **The justification commits the precise error the re-cut was made to fix.** Evidence §6.4's whole increment is *"value-freedom is a property of the PRODUCING SITE and the TYPES it renders — never of the sentinel."* The residual's argument is *"a small closed set of **sentinels** with no open extension point"* — keyed on the sentinel, over a set of producers **nobody enumerated**. That is the sentinel-keyed list, reintroduced one section after being deleted, for three arms instead of one.
3. **It defeats Decision 2's headline outcome on the highest-traffic route.** After this ships, `POST /instances` with `{"def_ref":"kyc:ssn-123-45-6789"}` returns a static 400 — but `{"def_ref":"4111-1111-1111-1111"}`, which **parses** (no colon ⇒ `Latest`) and merely is not registered, returns **404 with the value echoed verbatim**. An attacker who wants their string reflected only has to make it well-formed. The delivery closes the parse-failure channel and leaves the lookup-failure channel on the same field of the same request.
**Fix — the residual cannot stay as written. Options, in preference order:**
(a) **Move 404 and 422 to deny-by-default in THIS delivery.** The mechanism already exists (`ClientSafeMessage`), the arms are three sentinels each, and the two genuinely-clean producers (`ErrConcurrentUpdate`, `engine.ErrInvalidTransition`'s nine static constants) opt in trivially. Cost: `service.ErrConflict`'s and `ErrDefinitionNotFound`'s messages become static until someone vouches — which is exactly the migration cost D2 already accepts for 400.
(b) If the residual must stay for scope reasons, then **delete the "not proven to leak" and "small closed set of sentinels" justification and replace it with the executed table above**, state plainly that *404 and 422 are known to echo caller-supplied ids and, at `service.go:605`, an arbitrary inner error*, and record it as an **open disclosure**, not a bounded residual. A stated gap the reader can price is honest; "not proven to leak" is false.
In either case the `SECURITY.md` text must not claim 4xx bodies are value-free, and plan phase 1 test 6 (`TestFourOhFourNineTwentyTwoResidualIsPinned`) must pin the **producing sites**, not the sentinel sets — pinning the sentinel set is what let this through.

### E23 — the 403 double-echo, the bare-deny cleanliness, `httpcore.Validate`'s value-freedom, the "0 existing caps" grep and the 3 discard sites all reproduce EXACTLY
**Severity:** —
**Bundle says:** ADR Context §2 (403 returns the predicate source **twice**, bare deny leaks nothing); Evidence §1 (`httpcore.Validate` value-free even for a length constraint); ADR Context §1 (`grep -rnE "MaxBytesReader|BodyLimit" transport/` exits 1; three `_ =` discard sites at `stdlib:238`, `gin:265`, `fiber:255`).
**I ran:** real `httpcore.Validate` on a DTO with `max=3` fed 12 chars and `numeric` fed `123-45-6789`; the real `internal/expreval` evaluator on an ABAC predicate that errors; `httpcore.ClassifyError` on both; the greps with a **bare** `|` under `-E`.
**Observed:**
```
400 httpcore.Validate -> 400 {"message":"workflow-httpcore: bad input: Key: 'probeDTO.name' Error:Field
   validation for 'name' failed on the 'max' tag\nKey: 'probeDTO.ssn' Error:Field validation for 'ssn'
   failed on the 'numeric' tag"}                       <- neither the value nor its length
403 eval-error arm    -> 403 {"message":"workflow-authz: not authorized: workflow-expreval: run
   \"actor.attributes.internalApprovalLimit > vars.amount\": invalid operation: string > int (1:40)\n
   | actor.attributes.internalApprovalLimit > vars.amount\n | ......^"}   <- source TWICE
403 BARE deny         -> 403 {"message":"workflow-authz: not authorized"}  <- clean
$ grep -rnE "MaxBytesReader|BodyLimit" transport/   ; EXIT=1
$ grep -rnE "_ = (gc\.ShouldBindJSON|c\.Bind\(\)\.JSON|json\.NewDecoder)" transport/http/*/groups.go
   fiber/groups.go:255  gin/groups.go:265  stdlib/groups.go:238           <- exactly 3, exact lines
```
**Verdict:** BUNDLE-CORRECT on all five. `expreval.go:135` is confirmed as `fmt.Errorf("workflow-expreval: run %q: %w", code, err)`, and expr's own error carries the snippet a second time.
**One rider (Minor):** vouching for `httpcore.Validate`'s message publishes the **Go struct type name** on the wire (`Key: 'probeDTO.name'` → in production `Key: 'StartInput.def_ref'`). Author-derived, not caller-derived, so not a disclosure under this record's definition — but the ADR should say so explicitly next to the "opts in" decision, because it is the same "author-derived structure on the wire" judgement E18 asks it to state for `keywordLocation`.

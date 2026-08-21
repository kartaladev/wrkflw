# Audit round 5 (post re-cut) — INTERACTION lens over ADR-0186's three-decision bundle

- Bundle commit: `6cddb7b1`, worktree detached.
- Step 0: **all five bundle files present** (ADR, spec, plan, evidence, deferred-slices record).
- Lens question: *take the changed decisions pairwise and derive what each does to the other's
  premises*; and — because this re-cut's change is a **REMOVAL** — *what did the survivors assume
  the removed decisions would provide?*
- Changed set given to this lens: **D1** (body caps), **D2** (what a 4xx body may say), **D3**
  (at-rest posture); **three decisions REMOVED** to the deferred-slices record (read-path
  disclosure, `httpcall` SSRF, variable-map admission bound).

Findings appended as derived.

---

### I1 — D2's own per-class logging table contradicts itself about 403, and BOTH readings break a celebrated claim
**Severity:** Critical

**Decision A says (ADR-0186 D2, per-class logging table, row 1, `0186…md:409`):**
> | 403 | the **raw** error + correlation id — the leaked string is the deployment's own policy predicate source, and it belongs in the operator's log | `WarnContext` |

**Decision A also says (same table, row 3, `:411`):**
> | 400/403 raw error | only under `httpcore.WithVerboseErrorLogging(true)`, default **off** | `WarnContext` |

Row 1 says 403's raw error is logged **by default**. Row 3 says 403's raw error is logged **only
under an option that is off by default**. Same table, two rows, opposite defaults for the same class.

**The collision — this is a D2×D3 collision, and both readings falsify something the bundle
celebrates:**

- **If row 1 governers (403 raw logged by default):** then D3's prescribed `SECURITY.md` sentence is
  wrong. ADR `:451-453` says *"`SECURITY.md` must also record the sink this bundle itself widens:
  **with `WithVerboseErrorLogging(true)`**, rejected request payloads reach the configured
  `slog.Logger`"* — conditioning the sink on the option. Under row 1 the deployment's own policy
  predicate source (`internal/expreval/expreval.go:135`, `%q`, carrying process-variable and
  actor-attribute names) reaches `slog` **unconditionally**, and the security document this bundle
  ships says it does not. That is exactly the **relocation** spec §5's D2×D3 row claims is resolved:
  > "✅ Logging is widened **per class** … ✅ D3's `SECURITY.md` **names that sink explicitly**."
- **If row 3 governs (403 raw only under verbose):** then ADR Consequences `:483-484` is false —
  > "**403 stops leaking the deployment's own policy source**, and the fix does **not** relocate it:
  > **the raw error goes to the operator's log**"
  — it does not, by default. The operator gets a correlation id joining to a record that omits the
  only diagnostic content, which is the *"a join nobody built"* defect (`:402-404`) re-created one
  layer up. The entire justification for blanking 403 rests on this sentence.

**It propagates into the implementation brief.** Plan phase 4 `:448-450` carries the same
contradiction verbatim: *"403 raw at `WarnContext`; … the raw 4xx error only under
`WithVerboseErrorLogging`"*. An implementer has no answer, and **no prescribed test resolves it**:
phase 4 test 4 is `TestRejected400PayloadIsNotWrittenToTheDefaultLogger` — **400 only**. There is no
403 logging test in any phase. The ambiguity ships untested in either direction.

**Evidence:** reasoned from the documents — not executed; the contradiction is textual and internal.
Confirmed the current sink is absent: all three `writeErr`s guard on `status >= 500` (spec §2, ADR
`:402`), so today no 4xx log record exists and neither reading is the status quo.

**Fix:** delete row 3 and state the default explicitly, per class, in one place. Proposed: **403 logs
the raw error by default** (it is the operator's own policy, and it is the only justification for
blanking the wire), **400 logs the rendered message only** by default, and
`WithVerboseErrorLogging(true)` widens **400** to the raw error — not 403, which is already raw.
Then correct ADR `:451-453` so `SECURITY.md` states *both* sinks: 403's raw predicate source
**always**, and 400's raw submitted values **under the option**. Add a phase 4 test
`TestForbiddenRawErrorIsLoggedButNotReturned` asserting the 403 raw string is in the log record and
absent from the body — otherwise the 403 half of the "join" is the same unbuilt join as before.

---

### I2 — the Decision→phase→package map has NO row for the conditional decode vouch, which is the re-cut's newest mechanism
**Severity:** Critical

**Decision A says (plan §2, `:138`):**
> "**Every sentence of ADR-0186's Decision section has a row. A row with no phase is a defect.** Six
> of round 3's fifteen Criticals were this one omission."

**Decision B says (ADR-0186 D2, `:363-375`):**
> "**The 36 decode wraps opt in CONDITIONALLY, on the concrete error TYPE.**" — with a three-row
> table (`*json.UnmarshalTypeError` → vouch, `*json.SyntaxError` → vouch, neither → static) and an
> explicit caveat "stated for the audit to attack".

**The collision:** I read the map's 29 rows. The D2 rows are: `ClientSafeMessage`+`SafeMessage`(1),
403 static(1), 400 deny-by-default + parser invariant(1), 404/409/422 residual(1), two wrap sites
de-valued(1), `httpcore.Validate` opts in(1), `WithVerboseErrorLogging`(1), `Logger` godoc(1),
arm-ordering comment(1), gate `%w`+safe message(2), `keywordLocation` via `BasicOutput()`(2),
avro/callback(2), expr(2), cursor forms(3), correlation id(4), 4xx logging(4).
**There is no row for the conditional decode vouch.** The D1 row that mentions the decode sites —
*"D1 body cap at the **36 propagating** decode sites | 4"* — is the **cap**, not the vouch.

This is the mechanism the plan's own §0 item 2 calls *"the newest and least-reviewed piece"*, it is
the only thing standing between `POST /instances` and `bad version in "kyc:ssn-123-45-6789"`, and it
is the one D2 sentence carrying an explicit "if the audit disagrees, the fix is one line". It exists
in the phase-4 prose and in phase 4 test 7 — but the map is the artifact that certifies coverage,
and by the map's own stated rule its absence is a defect.

**Evidence:** enumerated the map rows by hand against the ADR Decision section, both read in full.
Reasoned — not executed (a map has no runtime).

**Fix:** add
`| D2 the 36 decode wraps vouch CONDITIONALLY on `*json.UnmarshalTypeError` / `*json.SyntaxError` | 4 | `stdlib` \| `gin` \| `fiber` |`
immediately after the D1 decode-site row. Then re-walk the Decision section once more with the map
open — the omission is in the one D2 sentence that lives in phase 4 rather than phase 1/2, which is
how it fell out of a map grouped by decision.

---

### I3 — ADR says "the FOUR static cursor forms opt in" and lists FIVE; the plan says five. An implementer following the ADR silently blanks one shipped diagnostic
**Severity:** Major

**Decision A says (ADR-0186 D2, `:322-324`):**
> "The **four** static forms (`lister.go:69,77,90`, `armed_timer_paging.go:92,99`) **do** opt in"

That sentence names **five** locations and calls them four. It is restated twice more as a bare
count with no locations, in the two celebratory positions:
- ADR Consequences `:480-482`: "because `httpcore.Validate` and **the four static cursor forms** opt in"
- spec §5, D2×ADR-0146/0152/0183 row `:155`: "`httpcore.Validate` and **the four static cursor
  forms** opt in, so those messages survive"

**Decision B says (plan phase 3, `:357-360` and §4 enumerations `:586`):**
> "`lister.go:69,77,90` and `armed_timer_paging.go:92,99` — **the five static forms** opt in"
> "**7 total across two files — 2 that ECHO caller content, 5 static.**"

**The collision:** the ADR and the plan disagree by one on the size of the set D2's
ADR-0146/0152/0183-survival claim rests on. The plan's number is the right one:

**Evidence — executed:**
```
$ grep -n "ErrBadCursor\|ErrBadArmedTimerCursor" runtime/kernel/*.go | grep -v _test
runtime/kernel/lister.go:66:   fmt.Errorf("%w: %w", ErrBadCursor, err)                       <- ECHO
runtime/kernel/lister.go:69:   fmt.Errorf("%w: not an instance cursor", ErrBadCursor)         <- static
runtime/kernel/lister.go:77:   fmt.Errorf("%w: cursor carries no instance identity", …)       <- static
runtime/kernel/lister.go:90:   fmt.Errorf("%w: cursor carries no start time", …)              <- static
runtime/kernel/armed_timer_paging.go:89: fmt.Errorf("%w: %w", ErrBadArmedTimerCursor, err)    <- ECHO
runtime/kernel/armed_timer_paging.go:92: fmt.Errorf("%w: not an armed-timer cursor", …)        <- static
runtime/kernel/armed_timer_paging.go:99: fmt.Errorf("%w: cursor carries no timer identity", …) <- static
```
**2 echoing + 5 static = 7.** The plan is right; the ADR's word "four" is wrong in all three places.

**Why it is not merely a typo:** phase 3 test 2 is `TestStaticCursorFormsStayActionable`, prescribed
as *"positive assertions that the five static forms still render their message"*, and the plan warns
*"⚠ Without this, blanking all seven passes test 1 and destroys the diagnostics ADR-0160 added."*
An implementer who works from the ADR's count writes four rows, leaves one form unvouched, and it
renders static — a silently blanked ADR-0160 diagnostic that test 2 does not cover. This is the
same shape as the defect that killed the sentinel-keyed design: **the previous round quoted two of
four wrap forms and generalised.** The re-cut fixed the echo count and left an off-by-one in the
static count.

**Fix:** ADR `:322-324`, `:481` and spec `:155` → **five**. And state the total (**seven**) beside
it in the ADR, since the ADR is the only one of the two documents that names the sites.

---

### I4 — the fiber `BodyRaw()` fix moved fiber's cap BEFORE parsing while stdlib/gin cap DURING parsing. "Oversize has ONE status" is false, and on stdlib/gin an oversize body can return 2xx
**Severity:** Critical

**Decision A says (ADR-0186 D1 heading, `:196`, and Consequences `:473-475`):**
> "**Oversize is a 413**, and the mapping is named rather than assumed."
> "The unbounded-body surface closes: **39 sites, one policy, one status**"

and (D1, `:180-189`) that fiber uses **`len(c.BodyRaw())` as a pre-check** — adopted in this re-cut
because `c.Body()` decompresses — while stdlib and gin install `http.MaxBytesReader` and let the
oversize surface **as a decode error**:
> "`http.MaxBytesReader` does not produce a status — it makes the next `Read` fail, surfacing inside
> the decoder as an error the 400 arm would classify."

**Decision B says / assumes (plan phase 5, `:492-497`):**
> "Parity cases asserting all three adapters agree on **413** for an oversize body"

and spec §5 D1×D2 row (ii) marks the ordering coupling **✅ resolved** by "the adapter returns the
**bare** sentinel and the 413 arm precedes 400".

**The collision:** the two mechanisms do not merely produce the same status by different routes —
**they run at different points in the request lifecycle**, and this re-cut is what moved fiber's to
the earlier point. A pre-check is a property of the *wire body*; a `MaxBytesReader` is a property of
*what the decoder chose to read*. `json.Decoder.Decode` stops at the end of the first JSON value and
returns whatever error it hits first. So the two mechanisms disagree on any body where a decode
outcome is reached before the limit is.

**Evidence — EXECUTED.** Throwaway `main.go` reproducing the exact stdlib idiom
(`json.NewDecoder(req.Body).Decode(&in)`, verified at `transport/http/stdlib/groups.go:42,87,114,
142,157,172,253,300,331` — a bare single `Decode`, no trailing-data check), cap 1 MiB:

```
wellformed-oversize             wire=3145742  MaxBytes=true  Syntax=false  concrete=*http.MaxBytesError
malformed-at-byte3-oversize     wire=3145739  MaxBytes=false Syntax=true   concrete=*json.SyntaxError
                                              err=invalid character 'x' after object key
typemismatch-early-oversize     wire=3145758  MaxBytes=true  Syntax=false  concrete=*http.MaxBytesError
complete-then-garbage-oversize  wire=3145744  MaxBytes=false Syntax=false  concrete=<nil>   err=<nil>
```

Row-by-row, for a **3 MiB wire body against a 1 MiB cap**:

| body | stdlib / gin | fiber (`len(c.BodyRaw())` pre-check) |
|---|---|---|
| well-formed, oversize | `*http.MaxBytesError` → **413** | **413** |
| syntax error at byte 3, oversize | `*json.SyntaxError` → **400** (and, under D2's conditional vouch, the message is **vouched**) | **413** |
| complete JSON value then 3 MiB of trailing garbage | **`err == nil` → 2xx** | **413** |

**Two separate defects fall out:**

1. **The adapters diverge on status for the same request.** Phase 5's parity case passes or fails on
   the shape of its fixture, and the plan pins only the *size* (`⚠ Pin the body fixture below 4 MiB`),
   never the shape. A well-formed fixture is green and the divergence ships.
2. ⚠⚠ **On stdlib and gin an oversize body can return 2xx** — `Decode` returns `nil` after the first
   complete value and never reads far enough to trip `MaxBytesReader`. This is the *same outcome*
   D1 calls "the worst outcome for a security control — silently unenforced" (`:74-76`) when
   describing the three `_ = decode(&in)` sites — except it is at the **12 propagating** sites, which
   the bundle treats as the solved case. A consumer who sets `MaxBodyBytes = 1<<20` believing
   requests above 1 MiB are refused is wrong on two of three adapters.

**Why the bundle could not see it:** the `BodyRaw()` correction and the `MaxBytesReader` mechanism
were each verified **alone** (spec §6 "Discharged": *"the fiber `BodyRaw()` mechanism; the bare
`*http.MaxBytesError` through both stdlib and gin"*). Nothing re-derived what moving one of them
earlier in the lifecycle does to the *cross-adapter* claim the other half of the bundle asserts.

**Fix (three parts):**
1. **State the contract honestly in D1.** Either (a) accept the divergence and say so — *"stdlib/gin
   enforce the cap on bytes the decoder reads; fiber enforces it on the wire body; they agree on a
   well-formed oversize body and may differ otherwise"* — and add an explicitly-labelled parity
   divergence case beside the existing fiber-only one; or (b) make stdlib/gin pre-check too
   (`req.ContentLength > cap` is a cheap first gate, and `MaxBytesReader` remains the backstop for a
   chunked/lying `Content-Length`). **(b) is preferable** and closes both defects: it makes the
   mechanism identical in all three adapters, which is what "one policy, one status" claims.
2. Whichever is chosen, **phase 5's parity fixture set must carry all three body shapes above**, not
   just the well-formed one, with the expected status stated per adapter.
3. Add a phase 4 falsifier row: *"a complete JSON value followed by `cap`-exceeding trailing bytes
   must not return 2xx"* — it fails today and it fails against a `MaxBytesReader`-only implementation.

---

### I5 — "403 stops leaking the deployment's own policy source" is FALSE: the identical predicate ships verbatim as `definition.nodes[].eligible_expr` on a non-admin read route — and the read path is exactly what this re-cut REMOVED
**Severity:** Critical

**Decision A says (ADR-0186 Consequences → Positive, `:483-484`):**
> "⭐ **403 stops leaking the deployment's own policy source**, and the fix does **not** relocate it:
> the raw error goes to the operator's log"

restated in D2's per-class table `:299`:
> | 403 | static `"not authorized"` — **no opt-in**. The leaked string is the deployment's own policy
> source; **it belongs in the operator's log, never on the wire** |

and pinned by plan phase 1 test 4, `TestClassifyErrorDoesNotEchoPredicateSource`:
> "build a real 403 from an erroring attribute predicate; assert the body omits the identifier.
> ⚠ **Mandatory control:** `require.Contains(t, err.Error(), "internalApprovalLimit")`"

**Decision B says / assumes — the REMOVED read-path decision (deferred-slices record §D4, `:82-84`):**
> "**Five disclosure-bearing fields**, not one: `variables`, `tokens[].payload`, `incidents[].error`,
> `tasks[]`, and **the whole embedded `definition` (ADR-0144) — i.e. every gateway and
> flow-condition expression source, on a non-admin route**."

**The collision:** D2's 403 blanking is stated as a property of the *string* ("it belongs in the
operator's log, **never on the wire**"). It is only a property of *that response*. The ABAC predicate
D2 refuses to render at 403 is a **marshalled field of the definition wire form**, and the definition
is embedded in every instance view **by default**. Removing D4 removed the only decision that would
have closed the other channel — and nothing re-derived what that does to D2's absolute claim.

**Evidence — EXECUTED** (throwaway `definition/model/zz_probe_disclose_test.go`, run then deleted;
a definition with one `UserTask` carrying the same identifier phase 1 test 4 pins):

```
$ go test -count=1 -run '^TestZZProbeDefinitionEmbedCarriesPredicateSource$' -v ./definition/model/
    definition JSON = {"id":"kyc","version":1,"nodes":[
      {"id":"s","kind":"startEvent"},
      {"id":"t","kind":"userTask",
       "eligible_expr":"actor.attributes.internalApprovalLimit > vars.amount"},
      {"id":"e","kind":"endEvent"}],"flows":[…]}
--- PASS
```
Decoded back: `'actor.attributes.internalApprovalLimit > vars.amount'`. The identifier
`internalApprovalLimit` is present in the wire bytes.

⚠ **My own first containment check said `false` and was a stand-in** — `json.Marshal` HTML-escapes
`>` to `>`, so `strings.Contains(s, predicate)` missed a string that is plainly there. Recording
it because §6.3a asks exactly this question of every probe, and because **phase 1 test 4 and phase 2
test 1 are `Contains`/`NotContains` assertions over JSON bodies** — any of them written against a
predicate or value containing `<`, `>` or `&` will pass vacuously. That is a second, independent
finding (see I6).

Source chain, verified: `definition/model/node_wire.go:29`
`EligibleExpr string \`json:"eligible_expr,omitempty"\`` → `service/instance.go:143`
`Definition *model.ProcessDefinition \`json:"definition,omitempty"\`` → suppressed **only** by
`service.WithoutEmbeddedDefinition` (`service/options.go:128`), which is **off by default**.
The same applies to validation predicates: `validate.ValidationDescriptor.Schema`
(`definition/model/validate/validate.go:38`, *"schema text / predicate list"*) is marshalled at
`node_wire.go:84` as `"validation"` — so **phase 2 blanks `expr.go:64,68`'s `%q` on `v.source[i]`
while the identical source ships in the same response's `definition` embed.**

**Fix (documentation-only; the mechanism is correctly deferred):**
1. **Withdraw the absolute.** ADR `:483-484` → *"403 stops leaking the policy source **through the
   error body**. ⚠ The same predicate is still returned as `definition.nodes[].eligible_expr` in the
   embedded definition on `GET /instances/{id}` (ADR-0144, default-on, suppressible only with
   `service.WithoutEmbeddedDefinition`). Closing that channel is deferred with backlog 54."*
   Same hedge on D2's *"never on the wire"* at `:299` and on the `expr` bullet at `:344`.
2. **Add the row to spec §5.** This is a **D2 × (removed D4)** pair and the table has no removed-decision
   column at all. The re-cut's safety argument — *"this table is complete at three"* (spec `:141`) —
   is only true for pairs among the **survivors**; a removal has pairwise consequences too, and this
   is one.
3. **Phase 7 `SECURITY.md` must say it.** The bullet *"what a 400/403/413 body does and does not
   contain"* will otherwise tell a consumer the predicate is not disclosed. See I7.

---

### I6 — "the OUTERMOST `ClientSafeMessage` wins" (phase 1 test 3) makes the callback opt-in (the F12 fix, phase 2 test 5) UNREACHABLE. Two fixes in the same decision, each correct alone
**Severity:** Critical

**Decision A says (plan phase 1, test 3, `:244-249`):**
> `TestTwoVouchedMessagesInOneChainIsDeterministic` — "Build a chain where two errors implement the
> interface; assert which one wins (**the outermost**, i.e. the first `errors.As` match) and that
> the rule is documented on `SafeMessage`."

**Decision B says (plan phase 2, `:283-285` and `:292-294`; ADR `:326-328`, `:349-355`):**
> "The gate preserves the strategy's error (`%w`) **and attaches a client-safe message** by
> implementing `ClientSafeMessage() string` **on its own error type**."
> "`callback` → static **unless the consumer's own error implements `ClientSafeMessage`**, in which
> case **that message is used**. ⭐ This is the F12 fix: the previous design blanked a
> consumer-authored message with no way to keep it."

**The collision:** the gate's own error type is the **outermost** wrapper — it is what `Gate.Validate`
returns. The consumer's callback error is **inside** it (`%w`). If the gate attaches a
`ClientSafeMessage` on its own type, the outermost-wins rule guarantees the gate's message is the one
`ClassifyError` renders, **always**, and the consumer's message is never reachable. The F12 fix is
dead on arrival.

Both rules are new in this re-cut. Neither document says whether the gate attaches its message
**always** or **only when the strategy has no vouched inner message**, and the two prescribed tests
live in different packages so neither can observe the conflict:
- phase 1 test 3 is `httpcore`-local, over `SafeMessage` chains it builds itself;
- phase 2 test 5 is `runtime/validation`-local and can assert *"the returned error's
  `ClientSafeMessage()` is the consumer's"* — which is only true under the resolution the plan does
  not state.

There is **no cross-package test** for the callback path. Phase 5's one cross-package case is the
**jsonschema** rendering (plan `:495-497`), not callback.

**Evidence:** reasoned from the two documents plus the real gate; `runtime/validation/gate.go:38-47`
confirms `Gate.Validate` is the outermost wrapper (`return fmt.Errorf("%w: %s", ErrInvalidInput,
err.Error())` today — the site phase 2 changes to `%w` + attach).

**Fix:** state the delegation rule explicitly in the ADR, in D2's callback bullet:
> *"The gate's error type implements `ClientSafeMessage()` by **delegating**: if any error in the
> strategy's own chain implements it, the gate returns **that** message; otherwise it returns the
> gate's per-kind rendering, else static. The gate does not shadow a producer beneath it."*

and change **phase 1 test 3's** rule from "outermost wins" to "outermost wins **among peers**", then
add the case that actually discriminates: a gate-wrapped callback error where the inner message must
win. Best of all, promote phase 2 test 5 to a **phase 5 parity case** — it is the only mechanism in
this bundle with a consumer-authored producer and no end-to-end pin.

---

### I7 — the rendering is prescribed as "keyed on strategy KIND", and `callback` — the one strategy whose handling D2 celebrates — HAS NO KIND
**Severity:** Major

**Decision A says (plan phase 2, `:286-295`):**
> "The rendering is **keyed on strategy kind** (the set is open — `validate.Register` is exported):
> `jsonschema` → … · `avro` → static · `callback` → static unless … · unknown kind → static."

**Decision B says / assumes — the real code:**

**Evidence — read from source, quoted verbatim:**
```
definition/model/validate/callback/callback.go:1-3
  // Package callback is a code-only validation adapter wrapping a Go func. It is NOT
  // declarative: it has NO DESCRIPTOR and cannot be serialized.

runtime/validation/gate.go:53-59  (validator)
  ds, ok := s.(validate.DescribableStrategy)
  if !ok { return s.NewValidator() }     // <- callback takes this branch
  d := ds.Descriptor()                    // <- the ONLY source of .Kind
```
A kind exists **only** for `DescribableStrategy`. `callback` is explicitly not one — the gate's own
comment says so (`gate.go:50-52`: *"non-describable strategies (e.g. callback) have no stable key"*).
So a kind-keyed switch has **no `callback` case to write**; the callback path is reached by the
*absence* of a descriptor, not by a kind value.

**A second collision on the same line.** `definition/model/validate/registry.go:41` —
> "// Register maps kind -> factory. **A later registration for the same kind wins.**"

so a consumer may re-register kind `"jsonschema"` with their own strategy. A kind-keyed renderer then
runs the jsonschema branch — `errors.As(err, **jsonschema.ValidationError)` plus `BasicOutput()` —
against a foreign error type. The plan does not say what happens (static? panic on a failed
assertion? empty message?).

⚠⚠ **And this is the anti-pattern D2's own rationale condemns, re-created one layer down.** ADR
`:266-269`: *"A list keyed on a sentinel asserts a property the sentinel does not own, over a set of
producers nobody enumerated — and it cannot be kept true."* Keying the rendering on a **kind string**
over a registry whose *"later registration wins"* is the same defect with a different label.

**Fix:** key the rendering on the **concrete error type**, not the kind — which is what the
jsonschema branch already does (`errors.As(err, **jsonschema.ValidationError)`) and what makes the
whole opt-in coherent:
> *"the gate renders `keywordLocation` when `errors.As` finds `*jsonschema.ValidationError`;
> otherwise it delegates to any `ClientSafeMessage` in the strategy's chain; otherwise static.
> **No kind string is consulted.**"*
This also disposes of `avro` and `unknown kind` without enumerating them, and it is robust to
`Register`'s override semantics. Then rewrite plan phase 2 test 6 (`TestUnknownStrategyKindRenders
Statically`) to register a throwaway strategy that returns an error carrying the submitted value, and
assert static — it currently reads as a test of the kind switch it should no longer have.

---

### I8 — phase 7's `SECURITY.md` will state what a 403 body does not contain, while omitting the read-path disclosure the SAME lineage verified and this re-cut deferred
**Severity:** Major

**Decision A says (plan phase 7, `:548-558`) — the `SECURITY.md` bullet list:**
> "what a 400/403/413 body does and does not contain, **and which sinks receive what**"
> "the at-rest posture, over the **generated** column list (D3)"
> "that the body cap **bounds, but does not protect,** what is already at rest"

**Decision B says / assumes — the removed read-path decision (deferred record §D4, `:76-84`):**
> "**eleven** paths … **Five disclosure-bearing fields**, not one: `variables`, `tokens[].payload`,
> `incidents[].error`, `tasks[]`, and the whole embedded `definition`" — on **non-admin** routes.

**The collision:** `SECURITY.md` is this bundle's disclosure-posture document and the only artifact a
consumer reads. Its prescribed contents enumerate two disclosure surfaces — the **error body** and
the **database at rest** — and are silent on the third, **the read path**, which this lineage
verified, executed, and then removed from scope. A consumer who reads the finished document
reasonably concludes: error bodies are sanitised, storage is plaintext-and-my-problem, therefore the
API surface is covered. It is not: `GET /instances/{id}` returns process variables, token payloads,
incident errors and the full definition — including, per I5, the ABAC predicate D2 blanked at 403.

Deferring the **mechanism** is the right call. Deferring the **sentence** is not — the ADR's own
framing for D3 is exactly this argument (`:466-467`): *"Recording 'we do not do this, and here is why
doing it badly is worse' is a decision a consumer can act on. **Silence is not.**"* D3 applies that
standard to encryption at rest and this bundle does not apply it to the read path.

**Evidence:** reasoned from the documents; the underlying disclosure is executed in I5 and in the
deferred record's own evidence (`§D4`, evidence file §3 and §4.2).

**Fix:** add one bullet to plan phase 7's `SECURITY.md` list and one Consequence to the ADR:
> *"⚠ **The read path is not covered by this record.** Instance-read routes return process
> variables, token payloads, incident errors, human-task rows and the embedded definition
> (ADR-0144) to any caller the consumer's own middleware admits — including expression sources this
> record blanks from 4xx bodies. `service.WithoutEmbeddedDefinition` suppresses only the definition
> embed. Redaction is designed and deferred (backlog 54)."*
Same sentence in ADR Consequences → Negative, beside *"100 and 101 stay open"*.

---

### I9 — D3 "the document and the test cannot disagree" is unearned: no phase builds the generator, and no test compares `SECURITY.md` to it. The file is hand-written prose that must survive generation
**Severity:** Major

**Decision A says (ADR-0186 D3, `:445-446`):**
> "The `SECURITY.md` list is **generated from that classification**, so **the document and the test
> cannot disagree**."
restated as a Positive `:485-487`: *"the at-rest posture becomes a generated statement with a test
behind it, so the one enumeration in this repo that has rotted three times **cannot rot a fourth**."*

**Decision B says / assumes (plan phase 6 `:511-538` and phase 7 `:548-551`; map `:172-173`):**
| D3 migration parser invariant + dialect-name agreement | **6** | `internal/persistence/store` |
| D3 `SECURITY.md` generated from the classification | **7** | docs |

**The collision — three concrete gaps, all in the seam between the two phases:**

1. **No phase builds the generator.** Phase 6's deliverables are two *tests*
   (`TestEveryAtRestColumnIsClassified`, `TestDialectsAgreeOnColumnNames`); phase 7 is the
   **controller writing documents by hand**, with the instruction *"⚠ Do not hand-write it"* for the
   one bullet it cannot then produce. The generator has no phase, no package and no test.
2. **Nothing compares the file to the classification.** Both phase 6 tests assert properties of the
   *migrations*; neither reads `SECURITY.md`. The moment the two phases finish, the file and the
   classification are two independent artifacts, and "cannot disagree" is a hope. The rot this
   decision exists to stop is *the document being wrong* — which is precisely the assertion nobody
   makes.
3. **`SECURITY.md` already exists and is hand-written prose**, not a generated list:
   ```
   $ ls -la SECURITY.md
   -rw-r--r--  2040  SECURITY.md
   ```
   It carries `## Supported versions`, `## Reporting a vulnerability`, `## What to expect` and a
   `## Scope notes for embedders` section with three hand-authored bullets (authorization of admin
   routes / TLS / untrusted definitions). D3 gives no mechanism for a generator to write into it —
   no marker region, no `go:generate` directive, no "generated section between these comments".
   And phase 7 adds **six further hand-written bullets** to the same file, several of them D2's, so
   the generated and hand-written content interleave with no stated boundary.

⚠ This is a **D2 × D3** collision the spec's D2×D3 row does not see: that row is only about the log
sink. The sharper coupling is that **D2's hand-written sentences and D3's generated list share one
file**, and D3's celebrated property ("cannot disagree") is a property of a file D2 also writes.

**Evidence — executed:** `ls -la SECURITY.md` (above) and reading its contents in full; the file is
a vulnerability-reporting policy, not a data inventory.

**Fix:**
1. Give the generator a phase and a name. Simplest shape that keeps the property: **the classification
   is a Go table in `internal/persistence/store`; a test in phase 6 renders it to markdown and asserts
   the rendered block equals the region of `SECURITY.md` between
   `<!-- BEGIN generated: at-rest columns -->` / `<!-- END … -->`, failing with the diff.** That is
   the only shape under which "cannot disagree" is true, and it is a test, not a build step.
2. Add the map rows: the generator, and the `SECURITY.md`-matches-classification assertion.
3. Phase 7's ordering must then be: phase 6 writes the generated region; phase 7 edits **only**
   outside the markers. Say so, because the file already has hand-written content that must survive.

---

### I10 — `ClientSafeMessage` is a cross-package contract with THREE independent implementations and ZERO compile-time or cross-package enforcement. A rename silently blanks every vouched message
**Severity:** Critical

**Decision A says (ADR-0186 D2, `:284-288`):**
> "⚠ **The interface is satisfied structurally, so a lower layer never imports the transport.**
> `runtime/validation` implements the method on its own error type and does **not** import
> `transport/http/httpcore` — which is what makes this legal under the layering CLAUDE.md locks.
> `httpcore` **declares** the interface for documentation."

**Decision B says / assumes (spec §5, D2 × the layering rule, `:159`):**
> "✅ `ClientSafeMessage` is satisfied **structurally** … ⚠ **A phase-2 test must assert the absence
> of that import**, or the design is one careless `goimports` away from inverting."

and the plan's prescribed guards: phase 2 test 7 `TestValidationDoesNotImportTransport`, phase 3
test 3 `TestKernelDoesNotImportTransport`.

**The collision:** both prescribed tests guard the **wrong direction**. They prevent the layering
from *inverting*; nothing prevents the contract from *silently breaking*. Structural satisfaction has
no compiler check by construction — the usual guard, `var _ httpcore.ClientSafeMessage = (*myErr)(nil)`,
requires exactly the import the design forbids. So:

- if `httpcore` renames the method (`ClientSafeMessage` → `SafeClientMessage`), or changes the
  signature (`() string` → `() (string, bool)`),
- **nothing fails to compile**, both `go/parser` import tests stay green, both packages' own unit
  tests stay green (they assert their own method), and
- every vouched message **silently falls back to static text** — including the four/five static cursor
  forms whose diagnostics ADR-0160 added and which plan phase 3 test 2 exists to protect.

**By my count there will be three independent implementations** of the same structural interface,
none checked against the declaration:
1. `httpcore.SafeMessage`'s returned type (phase 1),
2. `runtime/validation`'s gate error type (phase 2) — it cannot use `httpcore.SafeMessage`,
3. `runtime/kernel`'s cursor wrapper type (phase 3) — same reason; today those sites are bare
   `fmt.Errorf` (`lister.go:69,77,90`, `armed_timer_paging.go:92,99`), so a new type is required.

**What the existing tests can and cannot see.** Phase 5's parity suite has exactly one cross-package
case — *"a jsonschema validation failure returns a body containing the `keywordLocation`"*
(plan `:495-497`) — which does pin implementation (2) end-to-end. **Implementation (3) has no
cross-package pin at all**: phase 3 test 2 asserts the kernel error's own method, phase 5 does not
mention cursors, and phase 1's tests use `httpcore`'s own `SafeMessage`. So the cursor diagnostics
can be blanked by an `httpcore` edit with a fully green suite.

**Evidence:** reasoned from the documents plus the current code
(`runtime/kernel/lister.go:66,69,77,90`, `runtime/kernel/armed_timer_paging.go:89,92,99` are bare
`fmt.Errorf` — verified by grep, quoted in I3). Not executed — the symbols do not exist yet.

**Fix:** put the compile-time assertion in a place that may legally import both — an **external test
package**. `httpcore`'s own `package httpcore_test` may import `runtime/kernel` and
`runtime/validation` without inverting any layering, because a test binary is not the library.
Add to phase 1 (or better, phase 5):
```go
// interface_contract_test.go — package httpcore_test
var (
    _ httpcore.ClientSafeMessage = kernelVouchedError()      // runtime/kernel
    _ httpcore.ClientSafeMessage = validationVouchedError()  // runtime/validation
)
```
This is the *only* thing that turns a rename into a compile error. Add a map row and a plan test with
its falsifier stated: *"rename `ClientSafeMessage` in `httpcore` and this file must fail to compile;
both import tests and both package suites stay green."* Also add a cursor case to phase 5's parity
suite, so implementation (3) has an end-to-end pin like implementation (2) has.

---

### I11 — phase 4's conditional-vouch snippet has no `*http.MaxBytesError` branch, so copying it re-creates the 400-instead-of-413 defect D1 exists to prevent
**Severity:** Major

**Decision A says (plan phase 4, `:390-393`):**
> "**12 propagating sites per adapter** — install the cap and return the **bare**
> `httpcore.ErrRequestBodyTooLarge` on the oversize path. Keep
> `fmt.Errorf("%w: %w", httpcore.ErrBadInput, err)` for **decode** failures only."

**Decision B says (plan phase 4, the very next bullet, `:394-405`) — the code an implementer copies:**
```go
var ute *json.UnmarshalTypeError
var se  *json.SyntaxError
if errors.As(err, &ute) || errors.As(err, &se) {
    err = httpcore.SafeMessage(err.Error(), err)   // vouched
}
writeErr(cfg, …, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
```

**The collision:** on stdlib and gin the oversize signal **is** a decode error — it is the value
`Decode` returns. `*http.MaxBytesError` is neither of the two vouched types, so it takes the
`else` path and is wrapped in `ErrBadInput` on the last line. Executed (I4, row 1): that value is
`*http.MaxBytesError`, and ADR `:211-212` records the consequence — *"Executed: `ClassifyError` on an
error wrapping both sentinels returns **400** `{bad_request}`"*. The snippet therefore ships a 400
with `"invalid input"` for every oversize body.

The requirement is stated correctly one bullet above; the **code that replaces it is what gets
copied**, and the conditional vouch is a *new* piece of this re-cut written against the decode path
as it looked before the 413 sentinel existed. Classic pairwise miss: D2's newest mechanism, written
against a premise D1's fix changed.

Mitigating: phase 4 test 1's stated falsifier — *"it also fails against an implementation that keeps
the `ErrBadInput` wrapper"* — does catch it at RED. Hence Major, not Critical.

**Evidence:** executed in I4 (the concrete type of an oversize `Decode` is `*http.MaxBytesError`);
the snippet is quoted verbatim from the plan.

**Fix:** the oversize branch must come **first** in the snippet and `return`:
```go
var mbe *http.MaxBytesError
if errors.As(err, &mbe) {
    writeErr(cfg, …, httpcore.ErrRequestBodyTooLarge)   // BARE -> 413
    return
}
var ute *json.UnmarshalTypeError
var se  *json.SyntaxError
…
```
Show the whole handler shape once, not two bullets that must be mentally merged. Same edit in
ADR `:217-218`, which states the rule but shows no code for the propagating sites (only for the
three discarding ones).

---

### I12 — the deferred variable bound's own stated escape route ("measure bytes at the transport") lands on `MaxBodyBytes`, which D1 makes consumer-disableable. The one-way dependency is not one-way
**Severity:** Major

**Decision A says (ADR-0186 D1, `:177-178`):**
> "`httpcore.CustomizeConfig.MaxBodyBytes`, default **1 MiB**, **`0` = unbounded (the explicit
> opt-out)**"

**Decision B says / assumes (deferred-slices record §D2, `:241-246`) — the removed variable bound:**
> "⛔ **2. The byte bound has no affordable mechanism** … It is also a **second, incompatible notion
> of 'bytes' against D1's wire bytes**.
> ⇒ **Either drop the byte bound and keep elements only, or measure bytes where bytes exist
> (the transport).**"

**The collision:** spec §0 and §5 state the cross-slice dependency as *"one-way … the deferred bound
mints `service.ErrVariablesTooLarge`, which the deferred slice adds to **this delivery's** 413 arm.
**Direction is one-way; nothing in this delivery depends on it.**"* That is true of the *sentinel*.
It is not true of the **byte bound**, whose surviving option B is to reuse D1's transport-side byte
measurement — and D1 has just given that measurement two properties the deferred slice was not
written against:
1. **it is opt-out-able** (`MaxBodyBytes = 0`), so a security control layered on it inherits a
   consumer-facing off switch that D1 documents as a legitimate migration step;
2. **it counts the whole request body**, not the variable map — a 1 MiB body is ~4× the 256 KiB the
   deferred bound proposed, and per that record's own measurements (`§D2`, 25 ms → 1.563 s at
   n = 1 000 → 8 000, clean O(n²)) the compute admitted at 1 MiB is roughly **16×** the compute at
   256 KiB. **The body cap is not a bound on expression cost, and nothing in this bundle says so.**

Neither point is a defect *in this delivery*; both are premises the deferred slice will inherit
silently, and the boundary paragraph currently tells its author the opposite ("nothing in this
delivery depends on it" reads as "and nothing depends on this delivery either").

**Evidence:** reasoned across the two records; the O(n²) numbers are quoted from the deferred record's
own executed measurements, not re-derived here.

**Fix:** two sentences, both in the deferred-slices record's *"one cross-slice dependency"* section
(and one in ADR-0186's Consequences → Neutral):
> *"⚠ **The dependency is one-way for the sentinel only.** Slice 4's surviving byte-bound option
> ('measure bytes where bytes exist') resolves to `MaxBodyBytes`, which slice 1 ships as
> **consumer-disableable (`0` = unbounded)** and which counts the **whole request body**, not the
> variable map. A byte bound built on it inherits an off switch and a ~4× looser magnitude. Slice 4
> must decide whether that is acceptable or whether the byte bound needs its own measurement point."*
> *"⚠ **The 1 MiB body cap is not a bound on expression evaluation cost.** At the O(n²) rate slice 4
> measured, a 1 MiB body admits materially more compute than the 256 KiB the deferred bound
> proposed. The cap bounds bytes, not work."*

---

### I13 — the ✅ on "D2 × ADR-0185" conflates the MEMBERSHIP invariant with the ORDERING invariant. Only membership is machine-checked; ordering gets a prose warning, in the document whose lesson is that prose warnings do not hold
**Severity:** Major

**Decision A says (spec §5 cross-cutting, `:158`):**
> | **D2 × ADR-0185 (deferred identity)** | ADR-0185 adds **401** and **503** arms to the same ordered
> switch, 401 next to the 403 arm D2 rewrites. | ✅ D2 records the ordering rule as a **standing
> invariant for any future arm**, and **the parser walk enforces** that a new sentinel cannot join an
> arm without a policy row. |

**Decision B says (ADR-0186 D2, `:388-391`, and plan phase 1 test 5, `:255-264`) — what the walk
actually asserts:**
> "The test therefore **parses `httpcore/errors.go`** and asserts that **the set of sentinels named
> in each 4xx arm equals the set with a row in the policy table**."

**The collision:** set equality per arm. **Order is not parsed, not asserted, not mentioned.** The
ordering rule is carried by ADR `:313-316`:
> "⚠ **`ClassifyError`'s arms are order-dependent by construction.** Any future arm … **must state
> its position** … and carry a test asserting that an error matching two arms resolves to the
> intended one. **This sentence exists so the lesson outlives the bundle that learned it.**"

— which is a **prose warning plus a manually-remembered test**. The ✅ presents the walk as covering
both, and it does not.

⚠⚠ **This is the same document that says, about the other enumeration:** *"⚠⚠⚠ The deliverable is a
generator plus an invariant — NOT a number and NOT a hand-written list … **A prose warning cannot
make a prose number reliable**"* (`:433-436`). ADR-0186 applies that lesson to D3's column list and
declines to apply it to D2's arm order — in the same record, and the arm order is the thing that
produced **this bundle's own headline defect** (an oversize body classifying 400) **and** round 3's
finding F19 (a classifier scheduled before the sentinel it routes).

**Evidence:** reasoned from the three documents; the walk's specification is quoted in full and
contains no ordering clause.

**Fix:** extend the same `go/parser` walk to emit the arms **in source order** and assert that order
against the policy table's declared order — the walk already has the AST, so the ordered list is free.
Then the ADR-0185 401/503 arms and the deferred `ErrVariablesTooLarge` are covered by a machine, not
by a sentence. State the falsifier: *"swap the 413 and 400 arms in `errors.go`; the walk must go red
on order while set equality still holds."* That falsifier is the one that distinguishes the two
invariants, and it does not exist today.

---

### I14 — closing backlog 104 ("4xx bodies echo internals") while three of the seven arms still render `err.Error()` closes an item this bundle simultaneously reopens
**Severity:** Minor

**Decision A says (plan phase 7, `:564`):** "Close backlog **98** and **104**"; ADR `:51-52` lists
**104** (4xx bodies echo internals) among what this record closes.

**Decision B says (ADR-0186 D2 residual, `:305-311`, and Consequences `:510-511`):**
> "404, 409 and 422 keep `err.Error()` in this delivery. Bounded by the parser invariant and opened
> as a backlog item, but **it is a stated gap, not closure**."
and plan phase 7 `:566`: "**Open the new backlog items:** 404/409/422 adopt the opt-in".

**The collision:** 104's label in `docs/plans/HANDOVER.md:79` is *"104 4xx bodies echoing internals"* —
scoped to the class, not to two of its arms. Closing it and opening a successor in the same phase is
bookkeeping that reads, six months later, as "the 4xx echo problem was solved" when three arms of the
seven-arm switch still render `err.Error()`.

**Evidence — executed:** `grep -n "104" docs/plans/HANDOVER.md` → `:79` *"104 4xx bodies echoing
internals, + posture for 100/101"*; `:289` lists 104 under "Still open — Design tier". The full
item text lives in `AUDIT.md` on the unmerged local `docs/architecture-audit` branch and is **not
present in this worktree**, so the scope reading is from the label — labelled as such.

**Fix:** close 104 **partially**, the way 54 is handled in the same lineage ("54 *partially*",
`HANDOVER.md:247`): *"104 — closed for the **403 and 400** arms; the 404/409/422 arms are the
successor item."* Or keep 104 open and let the successor be its remaining scope. Either is fine;
what is not fine is a closure line and a reopening line in the same phase with no relationship
stated between them.

---

### I15 — the stated reason for running `go build ./examples/...` at the end of phase 4 rather than phase 1 is false; phase 1 is source-additive
**Severity:** Minor

**Decision A says (plan §1 fan-out rules, `:131-133`):**
> "⚠ **`go build ./examples/...` runs at the end of phase 4**, not phase 1. **The public `httpcore`
> surface changes in phase 1, but its *call sites* are in the adapters; hoisting the check earlier
> makes it unpassable.** (The previous revision hoisted it and finding I-5 caught it.)"

**Decision B says (ADR-0186 D2, `:396-399` and Consequences `:498`):**
> "⚠ `ClassifyError` **keeps its signature** and its pure-function discipline."
> "⚠ `ClassifyError`'s signature is deliberately **not** changed."

**The collision:** every phase-1 deliverable is **source-additive** — a new config field
(`MaxBodyBytes`), a new sentinel, a new interface, a new option (`WithVerboseErrorLogging`), a new
constructor (`SafeMessage`), plus *behavioural* changes inside `ClassifyError` and two error message
edits. None of it removes or re-types an exported symbol. So `go build ./examples/...` after phase 1
**passes**, and the stated justification is wrong.

**Evidence — executed:**
```
$ grep -rn "transport/http" examples/ --include="*.go" | sort -u
examples/{mysql_wiring,production_wiring,sqlite_wiring}/main.go -> transport/http/httpcore, transport/http/stdlib
$ grep -rn "httpcore\.\|stdlib\." examples/production_wiring/main.go
:139  readyChecks []httpcore.HealthCheck
:264  stdlib.Mount(mux, svc, httpcore.WithMeterProvider[*http.ServeMux](meterProvider))
:265  stdlib.MountHealth(mux, readyChecks...)
:274  stdlib.AdminRoutes{Svc: svc}.Customize(adminMux, httpcore.WithMeterProvider[…](…))
$ grep -rn "ClassifyError\|CustomizeConfig\|writeErr" examples/ --include="*.go"
(no matches)
```
The examples touch `HealthCheck`, `WithMeterProvider`, `Mount`, `MountHealth`, `AdminRoutes.Customize`
— none of which phase 1 changes.

**Why it matters (a little):** the *placement* is harmless, but the plan states it as a derived
constraint carried over from a previous audit finding, and the next revision will inherit it as a
fact about the phase graph. Round-3's I-5 was about a **different** bundle shape, before
`ClassifyError`'s signature was frozen. This is the "re-verify claims you inherit" case: restating
stripped the context that made it true.

**Fix:** replace with the honest reason — *"`go build ./examples/...` runs at the end of phase 4
because that is when the adapters stop moving; phase 1 is source-additive and would also pass, so
running it earlier is optional rather than forbidden."*

---

### I16 — `Contains`/`NotContains` assertions over JSON bodies are silently defeated by `encoding/json`'s HTML escaping
**Severity:** Minor

**The interaction:** phase 1 test 4, phase 2 test 1, phase 3 test 1 and phase 4 test 7 are all
"the body must NOT contain <string>" assertions, and every response body in this repo is written by
`json.Marshal`/`json.Encoder`, which escape `<`, `>` and `&` to `<`, `>`, `&` by
default.

**Evidence — EXECUTED, and it caught me first.** In I5's probe I asserted
`strings.Contains(marshalledDefinition, "actor.attributes.internalApprovalLimit > vars.amount")`
and got **`false`** — while the wire bytes plainly read
`"eligible_expr":"actor.attributes.internalApprovalLimit > vars.amount"`. Decoding the same
bytes returns the predicate verbatim.

**Why it is only Minor here:** I checked every prescribed fixture. None of the strings the tests
assert about contains `<`, `>` or `&` — `123-45-6789`, `4111-1111-1111-1111`, `ssn-123-45-6789`,
`kyc:ssn-123-45-6789`, `internalApprovalLimit`. So no prescribed assertion is vacuous **today**.
It becomes live the moment anyone adds a predicate- or expression-shaped fixture, which is exactly
what phase 1 test 4 is about (`amount > 100` is the natural fixture for an attribute predicate).

**Fix:** one line in plan §5's verification checklist, beside the existing anti-vacuity controls:
*"⚠ Assertions about response bodies must decode the JSON and assert over the decoded value, or use
a fixture free of `<`, `>` and `&` — `encoding/json` escapes those and a raw-bytes `NotContains`
passes over a leak. Observed in this audit."*

---

### I17 — D1×D3: the one sentence denies the wrong composition. The cap is a TRANSPORT control; the product is the library, and the library API is uncapped
**Severity:** Major

**Decision A says (spec §5, D1 × D3 row, `:147`):**
> | **D1 × D3** | Both touch what a consumer must know about data volume: the cap bounds what
> *arrives*, D3 documents what is *stored*. A reader will compose them into "capped therefore
> protected". | ✅ D3 states in one sentence that the cap **bounds, but does not protect,** what is
> already at rest. |

**Decision B says / assumes — CLAUDE.md's first load-bearing property:**
> "**Library-first, always**. The product is the **module-root public API** … a consumer imports and
> embeds in *their* application. … **Transports are library-provided, not shipped binaries.**"

**The collision:** the author identified the composition risk as *protected vs bounded* and answered
it. The **prior** composition is the one that fails: a reader composes "request bodies are capped by
default" into "**data entering the engine** is bounded". It is not. `MaxBodyBytes` is a field on
`httpcore.CustomizeConfig`, honoured at 39 decode sites in three HTTP adapters. A consumer who
embeds the engine — the primary shape CLAUDE.md describes — reaches the same state through the
module-root API with **no cap at any size**.

**Evidence — executed:**
```
$ grep -rn "^func (driver \*ProcessDriver)" runtime/*.go | grep -E "vars|payload"
runtime/processdriver.go:447         Drive(ctx, def, instanceID, vars map[string]any)
runtime/processdriver_signal.go:32   BroadcastSignal(ctx, name string, payload map[string]any)
runtime/processdriver_message.go:48  DeliverMessage(ctx, name, correlationKey string, payload map[string]any)
```
All exported, all module-root, all taking a caller-supplied map. The deferred record makes the same
observation for its own purposes (`§D2`, item 3: *"**`BroadcastSignal` has no `service` equivalent at
all** and is called directly by `examples/scenarios/signal_broadcast/main.go:108`. A library-first
bound placed only in `service` is bypassed by the library's own documented API."*) — but nothing
carries that observation into **D1**, whose bound is placed one layer further out still.

This bears directly on D3's deliverable: the at-rest columns D3 enumerates (`wrkflw_instances.snapshot`,
`wrkflw_human_task.vars`, `wrkflw_journal.trigger`) are written from data that arrives through
**both** doors, and the generated `SECURITY.md` will sit next to a hand-written sentence about a cap
that only governs one of them.

**Fix:** replace the D1×D3 sentence with one that denies the right composition, and put it in D1's
Consequences → Negative as well as `SECURITY.md`:
> *"⚠ **`MaxBodyBytes` is a transport control, not an engine one.** It bounds HTTP request bodies at
> the 39 decode sites in `transport/http/{stdlib,gin,fiber}`. A consumer embedding the engine and
> calling `runtime.ProcessDriver.Drive` / `BroadcastSignal` / `DeliverMessage` directly — the
> library-first shape — passes unbounded maps, and nothing in this record changes that. The cap
> bounds one door; it does not bound what is at rest, and it does not bound the library API.
> (Bounding the admission tier is backlog 99, deferred.)"*

---

### I18 — spec §5's claim that the pairwise grid is COMPLETE at three is the re-cut's stated main safety property, and it is false: a REMOVAL has pairwise consequences the grid has no column for
**Severity:** Critical (meta — it is the finding that generates I5, I8 and I12)

**Decision A says (spec §5 header, `:140-142`):**
> "⚠ The six-decision bundle needed **15** D×D pairs and got five of them wrong; **this table is
> complete at three**, and **that is the re-cut's main safety property rather than a happy
> accident**."

restated in spec §0 `:39-49` as the arithmetic argument (6 decisions ⇒ 15 pairs; 3 ⇒ 3) and in the
plan's brief for this very lens (`:87-90`):
> "⭐ **The changed decisions are D1, D2 and D3 — all three, so the grid is 3 pairs and it is
> COMPLETE in spec §5.**"

**Decision B says / assumes — CLAUDE.md rule #9's interaction clause:**
> "**take the changed decisions pairwise and derive what each does to the other's premises**. Give it
> the explicit list of **what changed**."

**The collision:** the change set of this revision is not `{D1, D2, D3}`. It is
`{D1, D2, D3} ∪ {−D4, −D3ssrf, −D2vars}` — **six changes, not three**, because *removing* a decision
changes the premises of the survivors exactly as *editing* one does. The correct grid is
3 survivor×survivor pairs **plus 3×3 = 9 survivor×removed pairs**. The bundle derives **one** of the
nine (D2 × the removed variable bound, spec `:157`), and the arithmetic argument that justifies the
re-cut — *"cutting the bundle is the only lever that acts on the cause"* (spec `:49`) — quietly
assumes removal is free.

**It is not free, and this audit found three of the nine:**
- **I5** — D2 × removed read path: *"403 stops leaking the deployment's own policy source"* is false;
  the same predicate ships as `definition.nodes[].eligible_expr` on a non-admin route (executed).
- **I8** — D3 × removed read path: `SECURITY.md` will enumerate two disclosure surfaces and omit the
  third, the one this lineage verified.
- **I12** — D1 × removed variable bound: the "one-way" dependency is two-way for the byte bound,
  whose surviving option resolves onto `MaxBodyBytes` — an opt-out-able, ~4× looser measurement.

⚠⚠ **And this is the exact shape the lens was briefed to hunt, in the exact predicted location.**
The claim is a **celebratory sentence** — *"this table is complete at three, and that is the re-cut's
main safety property"* — minted to praise the re-cut, and written against a premise (that the change
set equals the surviving decision set) that the re-cut's own action falsified. Execution cannot catch
it: there is nothing to run.

**Fix:**
1. Withdraw the completeness claim as worded. Replace with: *"the survivor×survivor grid is complete
   at three; the survivor×removed grid is **3 × 3 = 9** and is derived below."*
2. Add the survivor×removed table to spec §5. The nine cells, with the three known-live ones from
   this audit filled in and the remaining six explicitly derived or marked *"no interaction, because
   …"* — a blank cell is a defect, per the same rule the Decision→phase map already states.
3. Correct the plan's audit brief (`:87-90`) so the next round's interaction lens is not told the
   grid is complete before it starts. That instruction, had it been followed literally, would have
   suppressed the three findings above.

---

### I19 — the 413 logging row (the author's OWN interaction finding, added by this re-cut) prescribes "the observed size", which does not exist on stdlib or gin
**Severity:** Major

**Decision A says (ADR-0186 D2, per-class logging table, `:412`), added by this re-cut:**
> | 413 | **the observed size** + the cap + correlation id | `WarnContext` |

restated in spec §5's D1×D2 row (iii) `:146` — *"✅ (iii) 413 has its own logging row (**observed
size** + cap + id). ⚠ **(iii) was missing from the six-decision bundle** and is an author interaction
finding of this re-cut"* — in plan phase 4 `:449` and as plan phase 4 test 5
`TestOversizeIsLoggedWithSizeAndCap`. The plan's §0 item 13 asks this audit specifically:
*"Added. **Is the row's content right?**"*

**Decision B says / assumes — the mechanism D1 chose for stdlib and gin:**
> "`http.MaxBytesReader` for stdlib; the same wrapper applied to `c.Request.Body` … for gin"

**The collision: `MaxBytesReader` never learns the body's size.** It stops reading at the limit and
returns an error carrying only the limit.

**Evidence — EXECUTED:**
```
$ go doc net/http.MaxBytesError
type MaxBytesError struct {
    Limit int64
}
    MaxBytesError is returned by MaxBytesReader when its read limit is exceeded.
```
One field. No size. The remaining candidates on stdlib/gin are both unusable as *"the observed
size"*:
- `req.ContentLength` — *"The value **-1** indicates that the length is unknown"* (`go doc
  net/http.Request.ContentLength`), which is what a chunked request gives; and when present it is a
  **caller-supplied header**, i.e. an attacker-chosen number in the operator's log.
- bytes actually read — bounded above by the cap by construction, so it is a constant
  (`= MaxBodyBytes`) on every 413 and carries no information.

Only **fiber** can satisfy the row, because its mechanism is `len(c.BodyRaw())` — a real measurement
taken before any limit applies. So the row is satisfiable on **one of three** adapters, and phase 4
test 5 is prescribed for **all three** with no per-adapter caveat.

⚠ This is the row the author added *as* their interaction pass and flagged for the audit. It is
correct that the row was missing; its content was written against fiber's mechanism and not
re-derived against the other two — which is the same half-right pairwise pattern the deferred record
records for its own author pass (`§D2` item 1: *"the author's own interaction pass identified the
wedge correctly and then applied the weaker rule to all four fields"*).

**Fix:** make the row honest and per-adapter:
> | 413 | **the cap**, the correlation id, and — where the adapter can measure it — the observed
> wire size. **stdlib/gin log `content_length` when `req.ContentLength >= 0`, labelled as
> client-asserted, and omit it otherwise; fiber logs the true `len(c.BodyRaw())`.** |
Then split phase 4 test 5: assert cap + id in all three, and the size only in fiber. If the
uniform-3-adapter assertion is kept, prefer the alternative fix in **I4** — a `req.ContentLength`
pre-check on stdlib/gin — which makes all three adapters measure before they read and makes this row
true everywhere.

---

### I20 — the histogram is prescribed at the decode sites, where `json.Decoder` measures what it CONSUMED, not the body. The corrected migration story ("read it with the cap off") does not fix that
**Severity:** Major

**Decision A says (ADR-0186 D1, `:245-251`, one of this re-cut's five headline corrections):**
> "**The histogram cannot live in `httpcore`** — that package has **0** decode sites and never sees a
> body. It is recorded in each **adapter**, where the body is read."
> "**It must be read with the cap OFF** … **set `MaxBodyBytes = 0`, observe the distribution, then
> choose a cap**"

**Decision B says / assumes — what "where the body is read" actually is:**
`transport/http/stdlib/groups.go:42,87,114,142,157,172,238,253,300,331` are all
`json.NewDecoder(req.Body).Decode(&in)`. `json.Decoder.Decode` **stops at the end of the first JSON
value** and reports no byte count.

**The collision:** to record a body-size histogram at that site you must wrap `req.Body` in a
counting reader — and a counting reader around a `json.Decoder` measures **bytes the decoder chose to
consume**, not the wire body. Executed in I4, row 4: a complete JSON object followed by 3 MiB of
trailing bytes returns `err == nil` having read a few hundred bytes. That request would be recorded
in the histogram as **small**.

The correction that moved the histogram into the adapters and the correction that turned the cap off
for the observation window are each right; together they still do not produce the measurement the
migration story depends on. *"Observe the distribution, then choose a cap"* is only sound if the
observation is of the same quantity the cap is enforced against — **wire bytes** (D1 `:182`,
emphasised). With the cap off there is no `MaxBytesReader` truncation, so the histogram is no longer
clipped; it is instead **under-reporting by an unbounded amount** on exactly the requests whose tails
the operator is trying to see.

**Evidence — EXECUTED (I4's probe, row 4):**
```
complete-then-garbage-oversize  wire=3145744  MaxBytes=false Syntax=false  concrete=<nil>  err=<nil>
```
3 MiB on the wire, decode returns nil, no error, nothing forces a read past the first value.
And `go doc` confirms `json.Decoder` exposes no consumed-byte count (only `InputOffset`, which is the
offset *within the decoded value*, not the body length).

**Fix:** measure the wire body, not the decode. Two options, both cheap:
1. **Record `req.ContentLength` when `>= 0`** (fiber: `len(c.BodyRaw())`), and a separate
   `unknown_length` counter for chunked requests. Honest, one line, no reader wrapping.
2. **Wrap `req.Body` in a counting reader and drain the remainder** after `Decode` returns
   (`io.Copy(io.Discard, req.Body)`), then observe. Accurate, but it deliberately reads bytes the
   handler does not need — state the cost if chosen.
Whichever is picked, say in the ADR **what the histogram measures**, since the whole migration
procedure is built on it, and add the falsifier to phase 4: *"a request with a complete JSON value
followed by 1 MiB of trailing bytes must be observed as ~1 MiB, not as ~20 bytes."* It fails against
any decoder-side measurement.

---

## Summary — 20 findings (7 Critical, 10 Major, 3 Minor)

| # | one line | sev | pair |
|---|---|---|---|
| I1 | D2's per-class logging table has two rows giving 403 opposite defaults; both readings falsify a celebrated claim | **Critical** | D2 × D3 |
| I2 | no Decision→phase map row for the conditional decode vouch — the re-cut's newest mechanism | **Critical** | map completeness |
| I3 | ADR says "the **four** static cursor forms" and lists **five**; the plan says five. ADR-0160 diagnostic silently blanked | Major | ADR × plan |
| I4 | fiber caps **before** parsing, stdlib/gin **during** — oversize returns 400, or even **2xx**, on stdlib/gin. Executed | **Critical** | D1 × D1-mechanism × parity |
| I5 | "403 stops leaking the policy source" is FALSE — same predicate ships as `definition.nodes[].eligible_expr` on a non-admin route. Executed | **Critical** | D2 × removed D4 |
| I6 | "outermost `ClientSafeMessage` wins" makes the callback opt-in (the F12 fix) unreachable | **Critical** | D2 × D2 |
| I7 | rendering keyed on strategy **kind**, but `callback` has **no descriptor and no kind**; `Register` lets a later kind win | Major | D2 internal |
| I8 | `SECURITY.md` will name two disclosure surfaces and omit the read path this lineage verified | Major | D3 × removed D4 |
| I9 | "the document and the test cannot disagree" — no phase builds the generator, no test compares the file; `SECURITY.md` is hand-written prose | Major | D2 × D3 |
| I10 | `ClientSafeMessage` = 3 implementations, 0 compile-time checks; a rename blanks every vouched message with a green suite | **Critical** | D2 × layering |
| I11 | phase 4's vouch snippet has no `*http.MaxBytesError` branch → ships 400 for oversize | Major | D1 × D2 |
| I12 | deferred byte bound's surviving option lands on `MaxBodyBytes` — opt-out-able and ~4× looser. The "one-way" dependency is not | Major | D1 × removed D2vars |
| I13 | the ✅ conflates the membership invariant with the **ordering** invariant; only membership is machine-checked | Major | D2 × ADR-0185 |
| I14 | closing backlog 104 while 404/409/422 still render `err.Error()` and a successor item opens in the same phase | Minor | bookkeeping |
| I15 | the stated reason for deferring `go build ./examples/...` to phase 4 is false — phase 1 is source-additive. Executed | Minor | phase graph |
| I16 | `NotContains` over JSON bodies is defeated by HTML escaping; it defeated my own probe first | Minor | test vacuity |
| I17 | D1×D3's sentence denies the wrong composition: the cap is transport-only, the **library API is uncapped**. Executed | Major | D1 × D3 × library-first |
| I18 | spec §5's "complete at three" — the re-cut's **stated main safety property** — omits the 3×3 survivor×removed grid. Generates I5, I8, I12 | **Critical** | meta |
| I19 | the 413 logging row prescribes "the observed size", which `http.MaxBytesError` does not carry. Executed | Major | D1 × D2 (author's own) |
| I20 | the histogram sits at `json.Decoder`, which measures what it **consumed**, not the body. Executed | Major | D1 internal |

**The shape the lens was briefed to hunt, found four times.** A celebratory sentence written against a
premise another fix — or the **removal** — had already changed: **I18** (*"this table is complete at
three, and that is the re-cut's main safety property"*), **I5** (*"403 stops leaking the deployment's
own policy source"*), **I9** (*"the document and the test cannot disagree"* / *"cannot rot a
fourth"*), **I13** (*"the parser walk enforces …"*). None is reachable by execution: I18 and I13 have
nothing to run, and I5's and I9's probes pass at the moment they run.

**The single highest-leverage correction:** I18. Add the **survivor × removed** grid to spec §5.
Three of its nine cells are live findings in this report, and the plan's own brief for this lens told
the auditor the grid was already complete — which, followed literally, suppresses all three.

⚠ **Probes created and deleted:** `scratchpad/probe1/main.go` (I4, I11),
`definition/model/zz_probe_disclose_test.go` (I5, I16). Working tree carries only this file.

# Carry-forward — the five decisions cut out of ADR-0186 by the 2026-08-21 re-cuts

- Date: 2026-08-21
- Status: **not a bundle.** This is a holding record so that splitting ADR-0186 loses nothing.
  Each section below becomes its own delivery — spec + ADR + plan + **its own** rule-#9 audit.
- Supersedes nothing. It **quotes** the state of five decisions as they stood on
  `design/authz-security-b3` (three at commit `1e527347`, two at `6cddb7b1`), and adds what their
  audits say each must still answer.

> ⚠ **Do not implement anything in this file.** Every section carries an unresolved design
> increment. A section is ready when it has been written up as its own bundle and that bundle has
> survived its own audit.

## Why this file exists

ADR-0186 was drafted as **six** decisions. It failed two audits as a six-decision bundle — the
second (56 findings, 28 Critical, four Opus lenses) concluding that **six of the six decisions**
needed a change rather than a sentence, and that *"bundle size is the multiplier"*: three of the
four failures in this lineage were **interaction** failures, where a fix to one decision falsified
a premise another decision was written against.

**Owner decision, 2026-08-21 (first cut): four single-decision deliveries.** The first slice was
written as **three** decisions (D1 + at-rest + 4xx) and **failed its own audit — 65 findings, 20
Critical**, with **12 of the 20 in the 4xx policy alone**.
**Owner decision, 2026-08-21 (second cut): split all three — genuinely one decision each.**
`docs/plans/sweep-evidence/audit3-0186-adjudication.md`.

⭐⭐⭐ **What that audit established, and why it changed the cut:** splitting worked on the axis it
was chosen for — the survivor×survivor interaction grid got small and **held** (one finding of that
shape, against five the round before). It did **not** protect against three other things, and none
of them is cured by a smaller bundle:
1. ⭐⭐ **A REMOVAL is a change, and it generates its own interaction grid.** Cutting three decisions
   out created **9 survivor×removed pairs**; the bundle derived **one**, wrote *"this table is
   complete at three"*, and shipped that false quantifier into its own audit brief. One of the
   missing eight was a live Critical.
2. ⭐⭐ **Scope-boundary failures are not interaction failures.** Three of one lens's four Criticals
   sat **one step outside a boundary the bundle drew and never re-derived** — a directory glob, a
   package set, a config sentinel. Reasoning *inside* each boundary was sound.
   ⇒ **"The failure was the grep's NET" generalises from enumerations to SCOPES.**
3. ⭐⭐ **A new mechanism carries risk unrelated to bundle size.** The 4xx producer opt-in was one
   round old and collected 12 of 20 Criticals. The design it replaced was *refuted*; it was itself
   never *validated* — reasoned into existence in the session that shipped it.

| slice | decision | record | state |
|---|---|---|---|
| 1 | **D1** request body caps | **ADR-0186** | in flight — folding its audit |
| 2 | **at-rest posture** | ADR-0187 (unwritten) | **this file, §AT-REST** |
| 3 | **what a 4xx body may say** | ADR-0188 (unwritten) | **this file, §4XX** — needs the most design |
| 4 | the instance read path aliases and discloses | ADR-0189 (unwritten) | **this file, §READ-PATH** |
| 5 | `httpcall` is an SSRF primitive | ADR-0190 (unwritten) | **this file, §SSRF** |
| 6 | variable-map admission bound | ADR-0191 (unwritten) | **this file, §BOUND** |

⚠ **The ADR numbers above are reservations, not records.** Next free ADR remains **0187** until
one is actually written. Do not create stub ADRs.

⚠ **Ordering is not arbitrary.** The at-rest posture goes next because its two defects are pure
scope corrections with stated fixes. The 4xx policy follows because it needs the most new design.
The read path precedes the bound, because the bound's seam question (§BOUND, item 3) may be changed
by whatever the read path decides about `runtime` vs `service` as the library's admission tier.

## The one cross-slice dependency, stated so it is not rediscovered

**D2 mints `service.ErrVariablesTooLarge` and D5 routes it to 413.** Slice 1 ships the 413 arm
with **one** sentinel in it (`httpcore.ErrRequestBodyTooLarge`) and the standing arm-ordering
invariant; **slice 4 adds the second sentinel to the existing arm**. The dependency runs
slice 1 → slice 4 and never back.

⭐ **The split resolves a Critical for free.** Re-audit finding I-3/F3 was that a static
`"request too large"` on the 413 arm is *false* when the arm is reached by a variable-count
refusal (a 109 KiB body over the *element* bound is not too large). With D2 out of slice 1, the
only producer of 413 is a genuinely oversize body, so the static message is true as written.
⚠ **Slice 4 re-opens it**: when `ErrVariablesTooLarge` joins the arm, the message must become
per-sentinel or the finding returns. Written here because that is exactly the kind of
cross-delivery consequence this lineage keeps losing.

---

# §READ-PATH — the instance read path aliases, and discloses (backlog 54, partially)

## The finding, as verified

`transport/http/httpcore/view.go:31` assigns `Variables: st.Variables` — an alias of the caller's
map, not a copy.

**Executed** (evidence file §3): `engine/step_state.go:325` is `copyVars = maps.Clone`, and
`State.Clone()` → `cloneState` → `copyVars` is the whole chain, so the clone is **shallow**.
Deleting a *nested* key from the clone deletes it from the source:

```
after nested delete on the CLONE, SOURCE applicant = map[string]interface{}{"name":"ada"}
top-level delete isolation: source still has 'tags' = true
```

`persistence/caching_instance_store.go:72`'s godoc — *"cloneInstanceEntry **deep-copies** an
entry"* — is a **false comment in shipped code**.

**The read surface, re-derived:** **eleven** paths — six `mapInstance` call sites
(`endpoints.go:42,52,94,124,140,155`), **three admin endpoints calling `NewInstanceView` directly
and taking no mapper** (`admin_endpoints.go:111` `ResolveIncident`, `:121` `CancelInstance`,
`:514` `ResolveCompensationStall`), and two mapper-less non-admin endpoints
(`GetInstanceSnapshot` `endpoints.go:60`, `GetActionableView` `:72`). `AdminListInstances` is
genuinely clean.

**Five disclosure-bearing fields**, not one: `variables`, `tokens[].payload`, `incidents[].error`,
`tasks[]`, and the whole embedded `definition` (ADR-0144) — i.e. every gateway and flow-condition
expression source, on a **non-admin** route.

⚠ **`GetActionableView` carries no task variables.** Executed: `runtime/view.ActionableTask`
declares six fields — `TaskID, NodeID, State, Claim, Candidates, AllowedActions` — and **no
`Vars`**. What it *does* disclose is `allowed_actions[].condition` (sequence-flow expression
source, verbatim) and `candidates[]`. The prescribed `TestActionableViewRedactsTaskVars`
**cannot be written** and was deleted.

## The decision as it stood — and the four things its re-audit refuted

The shape was: `httpcore.CustomizeConfig.RedactVariables func(ctx, RedactionScope, map[string]any)
map[string]any`, running **above** `InstanceMapper` (which would otherwise bypass it wholesale),
applied on all eleven paths, with the copy **deep only when a hook is configured**.

⛔ **1. The conditional copy does not fix the defect** (I-1 + E6, two lenses). Executed with **no
hook configured**: a consumer mutating a nested response value rewrote the **live cached entry**
(`{"name":"MUTATED"}`, `ssn` deleted). The ADR asserted the opposite three times, **contradicting
its own evidence file §3**.
⇒ **The copy must be deep UNCONDITIONALLY**, and the cost stated honestly rather than avoided by a
conditional that does not work.
⚠ This was an *author* interaction finding — "the deep copy would tax the read hot path, so take
it only when a hook is set" — that was correct about the cost and wrong about the correctness. The
hot-path cost is real and still needs a measured answer; it just cannot be paid for this way.

⛔ **2. The three direct-`NewInstanceView` admin endpoints have no channel to receive the policy**
(C8 + I-8). The covered set is 11 paths but the prescribed signature change threads **8**
functions. Three paths were in the set with no mechanism to reach them.

⛔ **3. Redacting `GetInstanceSnapshot` from `httpcore` re-embeds the definition** (F4, executed).
`service.instanceJSON`'s `omitDefinition` is **unexported** and `service.NewProcessInstance`
hard-codes `false`, so the `httpcore`-level fix **defeats `WithoutEmbeddedDefinition`** — the only
existing lever against a disclosure D4 itself names as uncovered.

⛔ **4. The prescribed count invariant is blind to both mapper-less endpoints** (C3 + F15) —
precisely the two the *last* rot added. It must be a `go/parser` walk over the real call sites
(pattern: `engine/terminal_sites_test.go`), not a runtime count.

## What §READ-PATH's own bundle must decide

1. **What the unconditional deep copy costs on the read hot path**, measured, and whether it is
   paid always or avoided structurally (e.g. copy-on-write, or redacting during projection rather
   than after).
2. **How the three mapper-less admin endpoints receive the policy** — a parameter, a config read,
   or a restructure that removes the direct `NewInstanceView` calls.
3. **Whether `variables` redaction belongs in `httpcore` at all**, given item 3 above puts the
   snapshot's other four fields inside unexported `service` machinery.
4. The covered-set boundary: `variables` only, with the other five as new backlog items, or wider.

---

# §SSRF — `httpcall` is an SSRF primitive reachable from process variables (backlog 65)

## The finding, as verified

`WithURLExpr` (`action/httpcall/httpcall.go:125-134`) calls raw `expr.Compile`. The default client
is `&http.Client{Timeout: 30s}` with no `CheckRedirect` and no allowlist;
`grep -rnE "CheckRedirect|expreval" action/httpcall/` exits 1. The hazard **is** documented in
`WithURLExpr`'s godoc, which makes this a posture question rather than an oversight.

Two load-bearing facts: `WithHTTPClient` (`httpcall.go:153`) assigns the **same** `h.client` field
a restricted transport would, and `NewHTTPCall` applies options in registration order — so a
restriction written as an option is order-dependent and one ordering silently drops it. And
`action/httpcall.ErrBodyTooLarge` (`httpcall.go:94`) **already exists**, bounds the *outbound
response* body at 10 MiB, and correctly classifies **500**.

## The decision as it stood — and the five things its re-audit refuted

The shape was: for expression-derived URLs only, an **IP deny-list in `net.Dialer.Control`** stated
as the property *"refuse any resolved address that is not global unicast"*, a **host allow-list**
on the URL and each redirect hop, `WithAllowedCIDRs` as the escape hatch, `WithUnrestrictedTransport`
as the opt-out, and `WithURLExpr` + `WithHTTPClient` together **refused**.

⛔ **1. There is no proxy decision — the word "proxy" appears ZERO times in the bundle** (F5 + E12,
two lenses). `http.DefaultTransport.Proxy` is `ProxyFromEnvironment`, **non-nil by default**, and
`Dialer.Control` sees **the proxy's address, never the target**. Executed: `169.254.169.254`
fetched **200 OK** while `Control` observed only `127.0.0.1`.
⚠ The two lenses' apparent disagreement resolves cleanly — **one mechanism, two configurations**: a
loopback proxy makes D3 refuse *every* `httpcall`; a reachable proxy makes the deny-list blind to
the real target. **Both defaults are wrong, in opposite directions.**

⛔ **2. The IP rule fails OPEN for every IPv6 address** (E4). The rule was specified as evaluated
*after* `ip.To4()`; `To4()` returns **nil** for real IPv6, and every predicate on a nil `net.IP` is
false — so `::1`, `fe80::1` and `fc00::1` are **admitted**.

⛔ **3. The stated property and the enumerated helper list deny different sets, in BOTH
directions** (E4). The ADR wrote *"not global unicast"* and then a list of `Is*` helpers plus three
CIDRs, as one sentence, assuming they agreed. Neither covers `::127.0.0.1`, `64:ff9b::7f00:1`
(NAT64 → 127.0.0.1), `240.0.0.1`, or `192.88.99.1`.

⛔ **4. The escape hatch is per-network; the justification was per-service** (I-9, executed).
`WithAllowedCIDRs(["10.0.0.0/8"])` with the default-empty host gate admits
`evil.example.com → 10.0.0.1` **and** `kubernetes.default → 10.96.0.1`.

⛔ **5. The refusal's return path leaks, and is retryable** (I-4 + F10, executed with D3's own
example URL). `incidents[].error` would carry the redacted value **and** internal IPs, on a
**non-admin** route, in the field §READ-PATH declines to cover. The refusal mints no sentinel, so it is
**retryable** — the action retries a destination that will never be allowed.

⚠ **6. Two prescribed tests cannot pass** (F9): `httptest` binds loopback, which the IP rule
refuses and the host allow-list explicitly does not override. The plan **diagnosed this exact
defect for test 2 and then repeated it in tests 3 and 5** — including the test billed as *"the
control"*.

⚠ **7. Refusing `WithURLExpr` + `WithHTTPClient` refuses the option's own documented use** (F6) —
*"e.g. an otel-instrumented one"* — and the stated justification is half false:
`otelhttp.NewTransport(base, …)` **composes the other way** (verified v0.68.0), so a consumer's
instrumented transport *can* wrap a restricted base.

## What §SSRF's own bundle must decide

1. **The proxy posture** — refuse to install a restricted client when a proxy is configured, resolve
   and check the target independently of the dial, or require `WithUnrestrictedTransport` under a
   proxy. This is the decision the whole mechanism turns on and it does not exist yet.
2. **One address rule, stated once, that covers IPv4 and IPv6** — derived from a table of addresses
   with expected verdicts, not from a property sentence plus a helper list.
3. **What granularity the escape hatch has** (host×CIDR pair, not CIDR alone).
4. **The refusal's error shape** — a non-retryable sentinel whose message names no resolved address.
5. **A test strategy that does not depend on dialing loopback**, since loopback is the thing being
   refused.

---

# §BOUND — expression cost is unbounded in its input (backlog 99)

## The finding, as verified

⚠ **The original `MaxNodes` fix is INVERTED and this was executed** — `expr@v1.17.8/expr.go:221`:
*"If MaxNodes is set to 0, the node budget check is disabled"*. `expr.MaxNodes(0)` is what
*disables* it; never calling it leaves `DefaultMaxNodes = 1e4` **active**. **Do not implement it.**

The unmetered axis is **caller-supplied array size**. Measured with an 80-byte predicate against
`vars.items` of *n* JSON integers: 25 ms → 98 ms → 391 ms → 1.563 s at n = 1 000/2 000/4 000/8 000.
Clean O(n²). Two audit lenses independently measured n = 10 000 directly: **2.458 s** (predicted
2.442 s, 0.65 % error) and **1.92 s** on a faster run. Both plain-mode.

⚠ **Two evaluator surfaces, not one**, and **neither has an options seam** — `authz/authz.go:23` is
a package-level global and `internal/authz/casbin/authorizer.go:30` is hard-coded in a constructor.

## The decision as it stood — and the four things its re-audit refuted

The shape was: `service.WithMaxVariableBytes` (256 KiB) and `service.WithMaxVariableElements`
(10 000), enforced **together at admission** over the closed set of four caller-supplied request
fields, refused with `service.ErrVariablesTooLarge` → 413, leaving `internal/expreval`, `runtime`
and `engine` untouched.

⭐ **The admission move itself is sound and should survive** — it closed seven findings in the
previous round, and it reaches three expression surfaces (both ABAC evaluators, `httpcall`'s URL
expression, `transform`) that an evaluator-side bound could not reach at all.

⛔ **1. Per-request is not per-caller** (I-10 + F7, two lenses, executed). **5 admitted signal
deliveries reach 49,995 elements / 789 KiB**, ≈61 s per evaluation, with no wall-clock backstop on
the gateway path. And the wedge argument that chose *incoming over merged* **applies to
`CompleteTask` alone** — refusing a signal or a message wedges nothing.
⇒ **Bound the merged map on `DeliverSignal`/`DeliverMessage`; keep incoming-only on `CompleteTask`.**
⚠ The author's own interaction pass identified the wedge correctly and then applied the weaker rule
to **all four** fields. Getting the pairwise consequence half right is the characteristic failure.

⛔ **2. The byte bound has no affordable mechanism** (F14). `service` holds a **decoded map**, so
the only accurate measure is `json.Marshal`: **948,523 ns/op, 265,098 B/op** against the element
walk's **19,000 ns/op, 0 allocs** — ~1,100× the per-evaluation cost D2 **rejected on cost grounds**.
It is also a second, incompatible notion of "bytes" against D1's wire bytes.
⇒ **Either drop the byte bound and keep elements only, or measure bytes where bytes exist (the
transport).** Elements alone is the cheaper and more honest control; the byte bound was never
derived — it is an `ASSUMPTION (unverified)` twice over.

⛔ **3. The admission seam is NOT closed** (E1). `runtime.ProcessDriver` — the module-root package
CLAUDE.md calls the product — exports `Drive(…, vars)`, `BroadcastSignal(…, payload)`,
`DeliverMessage(…, payload)` and `ApplyTrigger`. **`BroadcastSignal` has no `service` equivalent at
all** and is called directly by `examples/scenarios/signal_broadcast/main.go:108`. A library-first
bound placed only in `service` is bypassed by the library's own documented API.

⛔ **4. ⭐ The "three non-request `mergeVars` sources" enumeration is wrong, and one named site is
inside D2's own closed set** (C12, independently F8). There are **eight** `mergeVars` sources, and
`engine/step_triggers.go:936` — named as a *non-request* source — **is admission site #4**
(`CompleteTaskRequest.Output`). The plan's phase-1 **test 1 and test 6 therefore assert opposite
outcomes on the same line**, and the plan's escalation clause points at deleting the bound.
⚠ **The counting lens found this, for the fourth consecutive bundle.**

⚠ **5. `authz.Actor.Attributes` is a second unbounded caller-supplied map** (C7/E2), in the ABAC env
beside `vars`, cost-identical on the O(n²) axis. Adjudicated **MAJOR**: `httpcore.Actor` carries
`ID`/`Roles` only, so it is not exploitable over the shipped HTTP surface — **but it is exploitable
through the library API, which is the product.**

## What §BOUND's own bundle must decide

1. **Per-field admission semantics** — merged for signal/message, incoming for `CompleteTask`,
   with the `step_triggers.go:936` overlap resolved explicitly rather than by two contradicting
   tests.
2. **Whether a byte bound exists at all**, and if so at which layer.
3. **Which tier owns the bound** — `service`, or `runtime.ProcessDriver` where every caller passes.
   ⚠ Answer this *after* §READ-PATH, which may move the library's admission tier.
4. **Whether `Actor.Attributes` is bounded or documented.**
5. Runtime growth via `mergeVars` remains **out of scope by decision** — bounding it means refusing
   a persist *after* the side effect fired, which wedges the instance with no repair verb. That
   needs an incident-disposition design in `engine` and is its own backlog item.

---

## What HELD across both audits — do not re-derive it in any of the three bundles

Consolidated from `reaudit-0186-adjudication.md` so three future deliveries do not each pay for it:

- **`keywordLocation` is value-free across fifteen schema shapes.** ⚠ Word it **"author-derived"**,
  not "schema-derived". (Slice 1 owns this.)
- **39 decode sites, 36/3 split, no fourth idiom.** **11 read paths, checked via four independent
  nets — there is no 12th.** **8 sentinels / 5 groups.** **26 routes = 9 + 15 + 2.**
  **`ActionableTask` has 6 fields and no `Vars`.** **4 `expr` importers / 3 violators.**
  **3 `SECURITY:` sites.** ~30 line anchors including every vendor citation — all exact.
- **`BodyRaw()`** is the wire body, has no response side effect, is reachable from a mounted group,
  and is order-independent. **Bare `*http.MaxBytesError`** through both stdlib and gin.
- **45,540 elements = 262,141 bytes = 256.0 KiB exactly** — independently executed.
- `mergeVars`/`copyVars` shallow-clone premises; the chain path genuinely does not wedge; routing
  complete for both named sentinels with no import cycle; `ClassifyError`'s six ordered arms.
- **ADR-0185 leakage: CLEAN.** Zero hits for every identity symbol across all four files.
- **No symbol collisions**: all 11 minted names return 0.

---

# §AT-REST — nothing is protected at rest, and nothing is tamper-evident (backlog 100/101, posture only)

> **Was ADR-0186 D3/D6. Cut out 2026-08-21 after slice 1's audit.** Two Criticals, **both pure
> scope corrections with stated fixes** — this is the readiest of the five and should go first.

## The finding, as verified

`grep -rniE "encrypt|redact"` over `persistence/`, `internal/persistence/` and `engine/`
(non-test) exits 1. `wrkflw_journal` is **6** columns — no hash, no prev-hash, no signature.
`engine.NodeVisit` carries no actor field, by ADR-0145 design.

⚠⚠ **The enumeration of what sits in the clear has rotted FOUR times** — 2 → "at least six" → 12 →
"48 columns" → and the audit refuted that too. The third rot happened **inside a paragraph warning
that it rots**, in a sentence that **counted its own markdown rows**. This matters more than any
other enumeration in the lineage, because **this decision's deliverable IS the enumeration**: a
consumer who encrypts the columns we name and leaves the rest in the clear has been harmed by our
documentation.

**Machine-derived, and then corrected by the audit:**
- The schema is **9 tables** and the **table set is identical across postgres, mysql and sqlite**.
  Counting lens, independently: **79 columns** schema-wide, **nullability agrees on all 79**.
- ⭐ **`wrkflw_journal.trigger` is named `trigger_` in MySQL only** — `TRIGGER` is a MySQL reserved
  word (`mysql/0001_init.sql:31` vs `postgres/0001_init.sql:30`). No audit lens found this; the
  machine walk did.
- ✅ **The open question "is there a second name divergence?" is now CLOSED by measurement** (E21):
  swept all 9 tables × 3 dialects — **there is exactly one**.

## What the audit refuted

⛔ **1. The enumeration walks three migration files; there is a FOURTH** (F5).
`internal/authz/casbin/migrations/0001_casbin_rule.sql` creates a **tenth table** with **seven
free-form `TEXT` columns holding the deployment's casbin policy**, applied by the module-root
public `casbinauthz.MigrateCasbin`.
⚠⚠ **The bundle blanks the 403 body BECAUSE policy source is sensitive, and then omits the table
that stores that same policy at rest** — the precise harm this decision's own opening paragraph
forbids. ⇒ **Discover migrations (`**/migrations/*.sql`); never hardcode a directory list.**

⛔ **2. "48 free-form columns" is a POSTGRES number** (E20 + C4, two lenses). SQLite has **67**.
⚠ **The single-dialect blind spot this decision claims to fix, reappearing in the very number that
fixes it.** ⇒ The classification must be per-dialect, or explicitly justified as dialect-invariant.

⚠ **3. The prescribed invariant is names-only** (E21). A systematic **type** divergence exists
across dialects (`JSONB` / `JSON` / `TEXT`). Decide whether type divergence matters for a consumer
applying column-level encryption — it may, since some engines cannot index or encrypt some types.

⚠ **4. `SECURITY.md` "cannot disagree" with the classification, but no generator exists** (F13) —
no command, no drift check, nothing in any phase. Either build it or drop the claim.

## What §AT-REST's own bundle must decide

1. **Migration discovery** — the glob, and what happens when a consumer adds their own migrations.
2. **Per-dialect classification**, and whether type/nullability divergence is in scope.
3. **The generator**: what emits `SECURITY.md`'s list, when it runs, and what fails on drift.
4. **The classification itself**, column by column — a judgement the invariant forces to be
   *stated*, not one it can compute.
5. The deferral of the mechanisms stands and is the decision, not an omission: a
   `persistence.VariableCodec` without a key-rotation/key-loss story is worse than none, and a
   hash-chained journal whose head lives in the database the attacker already writes to is theatre
   (ADR-0145 explicitly rules `NodeVisit` out as the place for it).

---

# §4XX — what a 4xx body may say (backlog 104)

> **Was ADR-0186 D5/D2. Cut out 2026-08-21 after slice 1's audit, where it collected 12 of the 20
> Criticals.** ⚠⚠ **This is the largest of the five and the least settled. It needs real design,
> not a fold.**

## The finding, as verified — this part is SOUND and should be carried forward intact

`transport/http/httpcore/errors.go` renders `err.Error()` at 404 (`:31`), 403 (`:33`), 409 (`:35`),
400 (`:50`), 422 (`:56`); 500 (`:58`) correctly blanks. Six arms, **ordered**, closed set.

- **403 leaks the deployment's own policy source.** Executed: a 403 from an ABAC *evaluation error*
  returns the predicate source **twice** — `%q` in `internal/expreval/expreval.go:135` plus expr's
  own snippet. A **bare deny** returns only `"workflow-authz: not authorized"` and leaks nothing.
- **400 leaks the submitted value.** Executed against the repo's jsonschema strategy:
  `- at '/ssn': '123-45-6789' does not match pattern '^[0-9]{3}$'`. A `maxLength` leaf reports
  `got 11, want 3`, disclosing a length.
- ⚠ **The typed error does not reach the transport.** `runtime/validation/gate.go:45` is
  `fmt.Errorf("%w: %s", ErrInvalidInput, err.Error())` — `%s`, so `errors.As` is **true before the
  gate and false after** (two lenses). Any rendering needing the type must live in
  `runtime/validation`.
- The 400 arm matches **8 sentinels across 5 `errors.Is` groups**; `validation.ErrInvalidInput`
  wraps **four** strategies.

⭐⭐ **And the core insight is sound and executed — carry it forward:**

> **Value-freedom is a property of the PRODUCING SITE and the types it renders — never of the
> sentinel.**

| sentinel | producer | discloses? |
|---|---|---|
| `ErrBadInput` | `httpcore.Validate` | **no** (executed) |
| `ErrBadInput` | the decode wraps, via `Qualifier.UnmarshalJSON` | **YES** — echoes `def_ref` twice |
| `ErrBadCursor` | its static wrap forms | **no** |
| `ErrBadCursor` | `lister.go:66`, `%w: %w` over caller bytes | **YES** — two channels |

**Two sentinels, four producers, and within each sentinel the producers disagree.** A one-row-per-
sentinel allow-list cannot express that table. That is not a defect in how the list was populated;
it is a defect in **what the list is keyed on**.

## What the audit refuted — the proposed MECHANISM, not the insight

⛔ **1. ⭐ The decisive one: an enumeration error causing a design error** (C9, confirmed by the
controller). The ADR called `httpcore.Validate` *"the DTO validator, every POST/PUT on all 26
routes"*. **It runs on 3** — `endpoints.go:26,83,101`, and **3 of 11 DTOs carry a `validate:` tag**.
That false count is the entire basis for believing the opt-in protects ADR-0146/0152/0183.
Executed: `errors.Is(err, ErrBadInput)` is **false** for all four `engine.*` sentinels, so
deny-by-default renders `"user task requires a completion outcome"` → `"invalid input"` — the
outcome the ADR itself flags **in bold as unacceptable** and marks ✅ resolved.
⚠ **`engine` appears in no package list, no phase and no fan-out plan** (F4). The prescribed guard
test asserts only `Validate`'s message and **cannot fail when all four are blanked**.

⛔ **2. The 404/409/422 "bounded residual" is not bounded** (E22 + C2, two lenses, executed). Of the
**7** sentinels in those arms, **6 echo caller-controlled bytes**; only `ErrConcurrentUpdate`
survives. `ErrDefinitionNotFound` has **4** wrap forms all formatting the caller's qualifier;
`service.ErrConflict` has **6**, including `service.go:549` and `:605`
`fmt.Errorf("%w: %w", ErrConflict, err)` — *literally the `lister.go:66` shape the ADR calls "the
finding that killed the sentinel-keyed design"*. **A `def_ref` of `"kyc:ssn-123-45-6789"` is blanked
at 400 and echoed at 404, on the same route.**
⚠⚠ And the residual's justification — *"a small closed set of **sentinels**"* — **is itself
sentinel-keyed**, the identical fallacy retired two paragraphs earlier in the same document.

⛔ **3. The type-keyed decode vouch re-commits the same structural mistake** (E9 + F2, two lenses).
`*json.UnmarshalTypeError` was placed in the vouched set and is **not** value-free:
`{"add_attempts": 99999999999999999999}` → `json: cannot unmarshal number 99999999999999999999
into Go struct field …`. `encoding/json` sets `Value = "number " + <caller literal>` when a number
does not fit. Live on two routes. ⇒ Construct the message from `ute.Field`/`ute.Type`; never vouch
`err.Error()`. (Also fixes E8: fiber's `bind "x" from body:` prefix makes the body adapter-dependent.)

⛔ **4. The mechanism does not describe the code** (C6). `callback` has **no `Kind`** and is
deliberately not a `DescribableStrategy` (an existing test asserts it), so a rendering "keyed on
strategy kind" has nothing to key on — **3** registered kinds, not 4. And the kind literal is
**`"json-schema"`**, not `"jsonschema"` as written throughout: `case "jsonschema":` falls through
to static text **silently** while the prescribed tests still pass.

⛔ **5. "403 stops leaking the policy source" is FALSE** (I5, executed). The predicate is a
marshalled field, `definition.nodes[].eligible_expr` (`definition/model/node_wire.go:29`), inside
the definition embedded in every instance view by default (ADR-0144), shipped verbatim on a
**non-admin** read route. ⇒ **This decision cannot deliver its headline claim without the
read-path delivery (§READ-PATH).** State the dependency or withdraw the claim.

⛔ **6. Two fixes in one decision, each correct alone** (I6 + E2). The "outermost
`ClientSafeMessage` wins" rule makes the `callback` consumer opt-in — the celebrated fix for the
consumer-authored-message finding — **unreachable**, because the gate's own message shadows it.

⛔ **7. The cross-package contract has no enforcement** (E6 + F7 + I10, three lenses). Both
prescribed "does not import transport" tests **cannot fail** — the import would be a compile-time
cycle (executed). And structural satisfaction means three implementations agree only by
method-name coincidence: **an `httpcore` rename silently blanks every vouched message with a fully
green suite.**

⚠ **8. The per-class logging table contradicts itself on 403** (F6): one row logs the raw error by
default, another gates it behind default-off verbose logging. One reading performs the relocation
onto `slog.Default()` the design claims to prevent; the other deletes the operator's only channel.

⚠ **9. Widened 4xx logging has no off switch** (F11) — attacker-driven log volume, 0→N.

⚠ **10. `keywordLocation`-only discards safe information** (E15): a missing required field renders
as `at '/required'`, dropping a property name that is **author-derived and safe**.
✅ But `keywordLocation`'s value-freedom itself **held across nine adversarial schema shapes** (E14),
three times the bundle's own coverage, and the `LocalizedString` nil-printer panic and empty root
`KeywordPath` are confirmed (E17).

## What §4XX's own bundle must decide

1. **Which packages produce 4xx sentinels** — derived by discovery, not by list. `engine` was
   missing entirely. This is the scope-boundary lesson applied.
2. **404/409/422 move to deny-by-default too**, or a justification that is not sentinel-keyed.
3. **How a producer vouches, with compile-time enforcement** — the structural interface has none.
   Consider a shared low-level package both sides import, so a rename is a build failure.
4. **Precedence when several errors in one chain vouch**, without shadowing the consumer's.
5. **A rendering constructed from typed fields**, never from `err.Error()`.
6. **The dependency on §READ-PATH** for the headline 403 claim.
7. **The logging policy**, stated once and consistently, with an off switch.

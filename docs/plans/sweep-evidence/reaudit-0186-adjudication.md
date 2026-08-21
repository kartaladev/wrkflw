# ADR-0186 re-audit (round 2 of the standalone delivery) — adjudication

**Date:** 2026-08-21 · **Bundle audited:** `677760d5` — the fold of audit #1, on `design/authz-security-b3`
(spec + ADR-0186 + plan + `docs/specs/2026-08-21-adr-0186-premise-evidence.md`).
**Lenses:** execution · failure-modes · counting · interaction — four Opus agents, four detached
worktrees at the bundle commit. **Step-0 presence check passed in all four** (worktrees were created
*at* the bundle commit this time, so the check confirmed rather than rescued).
**Reports:** `reaudit-0186-{execution,failure-modes,counting,interaction}.md` (3,717 lines).

⚠ All four were killed mid-run by an API session limit and **resumed from transcript, not replaced**
(rule #11). 2,418 of the eventual 3,717 lines were already on disk at the kill, because every brief
required appending **per finding**. Second outing for that rule; second time it paid.

## ⛔ VERDICT: FAILS — and this time the failure is NOT confined to the plan

**56 findings, 28 Critical.** Audit #1's verdict was *"a plan defect, not a mechanism defect —
nothing needs a design increment"*. **That is no longer true.** Six decisions need a change, not a
sentence:

| # | what must change | who found it |
|---|---|---|
| 1 | **D2's bound must cover the merged map on the signal/message fields.** Per-request ≠ per-caller. | I-10 + F7 (2 lenses) |
| 2 | **D2's byte bound has no affordable mechanism** in `service`. | F14 |
| 3 | **D2's admission seam is not closed** — `runtime.ProcessDriver` exports four more entry points, one with no `service` equivalent. | E1 |
| 4 | **D5's 400 allow-list rests on a false premise** — the "value-free by construction" sentinels are not. | C5 + E9 + E3 (3 lenses) |
| 5 | **D3 has no proxy decision**, and `Transport.Proxy` is non-nil by default. | F5 + E12 (2 lenses) |
| 6 | **D4's copy must be deep unconditionally**, or the aliasing defect is not fixed at all. | I-1 + E6 (2 lenses) |

⭐ **Five of the six were found by two or more lenses independently.** That is the strongest signal
this process produces, and it is the reason four lenses are dispatched rather than one.

## ⭐⭐ The diagnosis, in the interaction lens's own words

> **"The revision minted absolute claims to celebrate its own fixes, and wrote them against premises
> its *other* fixes had already changed."**

All six of that lens's Criticals are instances. This is a sharper statement than *"some claims were
wrong"*, and it is **actionable**: the reliable defect site is the **celebratory sentence** — the one
that says a problem is now closed, a set is now complete, a cost is now avoided. Every such sentence
in the next revision must be re-derived against the *post-fix* premises, not the pre-fix ones.

⚠ Note how this differs from the previous rounds' lessons and does not replace them: audit #1's shape
was *"a claim about a NAME, never executed"*; this round's is *"a claim that was TRUE when written and
was falsified by a sibling fix in the same commit."* Execution alone does not catch the second — the
probe passes at the moment it is run.

## ⭐⭐⭐ The counting lens found the decisive Critical for the FOURTH consecutive bundle

**C12** — and it stops phase 1 dead. D2 names *"three non-request sources"* of `mergeVars`; there are
**eight**, and one of the three named — `engine/step_triggers.go:936` — **is admission site #4 of D2's
own closed set** (`CompleteTaskRequest.Output`). So the plan's phase-1 **test 1 and test 6 assert
opposite outcomes on the same line**: test 1 says bound it, test 6 says runtime growth must not be
refused and cites that very site. An implementer hits the contradiction on day one, and the plan's
escalation clause points at *deleting the bound*.

⚠ **F8 found the same enumeration wrong independently**, with the same misclassification. Two lenses,
one line of source.

And the enumeration this bundle was **loudest** about is the one it got wrong again:

**C1 + C2 — the at-rest plaintext set is not "12 columns across 7 tables".** Re-derived and
confirmed against `sqlite/0001_init.sql` by the controller during adjudication:
- The ADR's own markdown table lists **15 columns across 8 tables** — three rows brace-collapse
  multiple columns (`outbox.{payload,last_error}`, `human_task.{…six…}`, `call_links.{output,error}`)
  and `chain_links` was omitted from the table count. **The sentence counted its own markdown ROWS.**
- It names the actor **remainder** (`claim_actor`, `completion_actor` — roles + attributes) and omits
  the actor **identifier**: `claimed_by` / `completed_by` hold the ID, split out for indexing
  (`htActorRemainder`, `humantask_store.go:552`). Plus `outcome`. **True total: 18 columns, 8 tables.**
- Wrong in **nine places** across the bundle.

⭐ **This is the third consecutive rot of this exact enumeration** (2 → "at least six" → 12 → 18), and
it happened **inside the document that says "this rotted twice, assume it is still short"**. D6's
*deliverable is the enumeration*; a consumer who follows it encrypts what we list and leaves the rest
in the clear. **A number in prose cannot be made reliable by warning the reader about it.**

## Accepted Criticals, grouped by what must change

### A. D2 — the admission model is wrong in three independent ways (I-10, F7, F14, E1, C12, C7/E2)

- **I-10 / F7 — per-request is not per-caller.** Executed: **5 admitted signal deliveries → 49,995
  elements / 789 KiB**, ≈61 s per evaluation on the bundle's own ladder, no wall-clock backstop on the
  gateway path. And the wedge argument that chose *incoming over merged* **applies only to
  `CompleteTask`** — refusing a signal or a message wedges nothing. ⇒ **Bound the merged map on
  `DeliverSignal`/`DeliverMessage`; keep incoming-only on `CompleteTask`, where the wedge is real.**
  ⚠ The controller's own interaction pass got this half right and stopped one step short: it
  identified the wedge and applied the weaker rule to *all four* fields.
- **F14 — the byte bound has no affordable mechanism.** `service` holds a decoded map, so the only
  accurate measure is `json.Marshal`: **948,523 ns/op, 265,098 B/op** vs the element walk's
  **19,000 ns/op, 0 allocs** — and ~1,100× the per-evaluation cost D2 *rejected on cost grounds*.
  Plus it is a second, incompatible notion of "bytes" against D1's wire bytes. ⇒ **Either drop the
  byte bound and keep elements only, or measure bytes where bytes exist (the transport).** Elements
  alone is the cheaper, more honest control; the byte bound was never derived (it is an
  `ASSUMPTION (unverified)` twice over).
- **E1 — the seam is not closed.** `runtime.ProcessDriver` — the module-root package CLAUDE.md calls
  the product — exports `Drive(…, vars)`, `BroadcastSignal(…, payload)`, `DeliverMessage(…, payload)`
  and `ApplyTrigger`. **`BroadcastSignal` has no `service` equivalent at all** and is called directly
  by `examples/scenarios/signal_broadcast/main.go:108`. A library-first bound placed only in `service`
  is bypassed by the library's own documented API. ⇒ **The bound belongs where every caller passes,
  or D2 must state that it is a `service`-tier control and `runtime` consumers own it.**
- **C12** — above.
- **C7 / E2 — `authz.Actor.Attributes` is a second unbounded caller-supplied map**, in the ABAC env
  beside `vars`, cost-identical on the O(n²) axis. ⚠ **Adjudicated MAJOR, not Critical**: the
  failure-modes lens checked reachability and `httpcore.Actor` carries `ID`/`Roles` only, so it is not
  exploitable over the shipped HTTP surface today. It is exploitable through the **library** API,
  which is the product. ⇒ Fix the ADR's *"both ABAC evaluators inherit the bound"* claim, and bound
  or document `Attributes`.

### B. D5 — the 400 allow-list's premise is false (C5, E9, E3, I-2, I-3, I-6, F3, F12)

- **C5 + E9 + E3 — "value-free by construction" is not.** Executed: `ErrBadCursor` and
  `ErrBadArmedTimerCursor` reflect caller strings verbatim
  (`json: unknown field "ssn-4111111111111111"`); the **36 `ErrBadInput` decode wrap sites** echo the
  whole `def_ref` **twice**. ⚠⚠ **The controller's Evidence §1 probed `httpcore.Validate` and
  generalised to a different producer** — the stand-in failure the same evidence file documents as
  this repo's signature defect, committed one layer from where it was documented. And C5 notes the
  evidence quotes two messages that come from the **same** sentinel, covering 2 of 7 sites.
  ⇒ **The allow-list cannot be keyed on a sentinel.** Key it on a *rendering function* the producing
  site opts into, or blank by default and let sites opt in explicitly.
- **I-2 — two prescribed tests are mutually exclusive.** Executed both forms: the gate cannot both
  `%w`-preserve the typed error and hide its text; FORM A satisfies test 5 and leaks
  `'123-45-6789'`, FORM B is value-free and fails test 5. **There is no third `fmt.Errorf` form.** A
  custom error type is required and is named nowhere.
- **I-6 — the pin invariant cannot fail on its own scenario.** Executed mutation: a new sentinel
  joined the 400 arm, the pin **PASSED**, and it shipped `"rejected value 4111-1111-1111-1111"`.
- **I-3 / F3 — the static 413 destroys the message D2 mandates.** A 109 KiB body over the *element*
  bound returns *"request too large"*, which is false; and 413 has **no row** in the widened logging
  table, so the correlation id joins to nothing. Audit #1's "guard refuses the useful case", one
  status code over, on an arm this bundle minted while fixing that shape.
- **F12** — `callback` is a **consumer-authored** message blanked with no opt-in, while the same
  consumer's jsonschema gets a rendering.

### C. D3 — designed as a dial-time control only (F5, E12, E4, I-9, I-4, F9, F10, F6)

- **F5 + E12 — no proxy decision, and the word "proxy" appears zero times in the bundle.**
  `http.DefaultTransport.Proxy` is `ProxyFromEnvironment`, non-nil by default. `Dialer.Control` sees
  **the proxy's address, never the target**. Executed: `169.254.169.254` fetched **200 OK** while
  Control saw only `127.0.0.1`. ⚠ The two lenses' apparent contradiction resolves cleanly — **one
  mechanism, two configurations**: a loopback proxy makes D3 refuse *every* `httpcall`; a reachable
  proxy makes the deny-list blind to the real target. Both defaults are wrong, in opposite directions.
- **E4 — the IP rule fails OPEN for every IPv6 address.** `To4()` is nil for real IPv6 and every
  nil-`net.IP` predicate is false, so `::1`, `fe80::1`, `fc00::1` are **admitted**. And the stated
  *property* (`not global unicast`) and the enumerated *helper list* deny different sets **in both
  directions** — the controller wrote both into one sentence assuming they agreed.
  `::127.0.0.1`, `64:ff9b::7f00:1` (NAT64→127.0.0.1), `240.0.0.1`, `192.88.99.1` are covered by neither.
- **I-9 — the escape hatch is per-network, the justification per-service.** Executed:
  `WithAllowedCIDRs(["10.0.0.0/8"])` with the default-empty host gate admits `evil.example.com →
  10.0.0.1` and `kubernetes.default → 10.96.0.1`.
- **I-4 — the refusal's return path leaks.** Executed with D3's own example URL: `incidents[].error`
  would carry the redacted value **and** internal IPs, on a **non-admin** route, in the field D4
  declines to cover. **F10** adds that the refusal mints no sentinel and is **retryable**.
- **F9 — two phase-5 tests cannot pass**: `httptest` binds loopback, which the IP rule refuses and the
  host allow-list explicitly does not override. The plan **diagnoses this exact defect for test 2 and
  then repeats it in tests 3 and 5** — including the test billed as *"the control"*.
- **F6** — refusing `WithURLExpr` + `WithHTTPClient` refuses the option's **own documented use**
  (*"e.g. an otel-instrumented one"*), and the ADR's justification is half false:
  `otelhttp.NewTransport(base, …)` composes the other way (verified v0.68.0).

### D. D4 — the conditional copy does not fix the defect (I-1, E6, E11, C8, I-8, F4, C3, F15)

- **I-1 + E6 — executed, with NO hook configured**: a consumer mutating a nested response value
  rewrote the **live cached entry** (`{"name":"MUTATED"}`, `ssn` deleted). The ADR asserts the
  opposite three times, **contradicting its own Evidence §3**. ⇒ **The copy must be deep
  unconditionally**, and the cost stated honestly rather than avoided by a conditional that does not
  work. **E11**: the prescribed control test's falsifier is unachievable, and **I-1**: plan phase 3's
  control test *pins the defect in place*.
- **C8 + I-8 — covered set is 11 paths, the break list threads 8 functions.** The three direct
  `NewInstanceView` admin endpoints have **no channel** to receive the policy.
- **F4 — redacting `GetInstanceSnapshot` from `httpcore` re-embeds the definition** for
  `WithoutEmbeddedDefinition` consumers (executed): `omitDefinition` is unexported and
  `service.NewProcessInstance` hard-codes `false`. The fix **defeats the only existing lever** against
  a disclosure D4 names as uncovered.
- **C3 + F15 — the invariant prescribed to stop the read-path count rotting a third time is blind to
  both mapper-less endpoints** — precisely the two the *last* rot added.

### E. The plan's own pointers (F1, C14, C4, I-5, F2, I-7, F11, C13)

- **F1 + C14 — 7 of 9 phase references are wrong; three name phases 8 and 9, which do not exist**
  after two phases were deleted, including D6's entire deliverable instruction. **Audit #1's root
  cause with the pointer inverted**: the map was built to guarantee every decision has a phase, and
  the prose now points at the wrong ones.
- **I-5 — phase 3's hoisted `go build ./examples/...` cannot pass** (every call site is in phase 4);
  three parallel agents would start from a non-building repo. Executed by mutation.
- **F2 + I-7 — the body-size histogram is mapped to `httpcore`, which never sees a body**, no phase
  records it, and with `MaxBytesReader` installed it is truncated at the cap (4 MiB sent → 1,048,576
  observed). That histogram is the ADR's **entire migration story** and its stated reason for not
  shipping a soft limit.
- **F11 — *"no agent needs Docker"* is false**: D6's invariant test is mapped to
  `internal/persistence/store` (≥5 container-bound test files). Phase 7 has **no `Verify:` line**.
- **C13 — spec §5 claims "all 15 D×D pairs"; it is 13**, missing **D1×D4** and **D4×D6**.

## What HELD — do not re-litigate

Consolidated across four lenses so the next round does not re-derive it:

- **`keywordLocation` is value-free across fifteen schema shapes**, including all four the plan asked
  for and eleven more. ⚠ Word it **"author-derived"**, not "schema-derived". (**E7**, REFUTED in the
  bundle's favour.)
- **39 decode sites, 36/3 split, no fourth idiom.** **11 read paths, checked via four independent
  nets — there is no 12th.** **8 sentinels / 5 groups.** **26 routes = 9 + 15 + 2** exactly.
  **`ActionableTask` has 6 fields and no `Vars`.** **4 expr importers / 3 violators.** **3 `SECURITY:`
  sites.** ~30 line anchors including every vendor citation — all exact.
- **`BodyRaw()`** is the wire body, has no response side effect, is reachable from a mounted group,
  and is order-independent. **Bare `*http.MaxBytesError`** through both stdlib and gin.
- **45,540 elements = 262,141 bytes = 256.0 KiB exactly** — independently executed; the bundle's
  `ASSUMPTION (unverified)` on the bytes→elements conversion **can be discharged**.
- `mergeVars`/`copyVars` shallow-clone premises; the chain path (`chainer.go` → `starter.Drive`)
  genuinely does not wedge; routing complete for both named sentinels with no import cycle;
  ADR-0146/0152/0183 in-code rationale; `ClassifyError`'s six ordered arms.
- **ADR-0185 leakage: CLEAN.** Zero hits for every identity symbol across all four files.
- **No symbol collisions**: all 11 minted names return 0.
- **12 of the 21 spec §5 rows survived attack**, including D1×D3 (the rename), D1×D5 (arm ordering),
  D2×D3 (transitivity), D3×D5, D4×ADR-0144, ADR-0095 handling, and the "expects access, gets none"
  half of D3's two gates.
- **No lens disputed any of the seven items marked discharged** from audit #1.

## ⚠ What the controller got wrong, recorded so it is not repeated

1. **The stand-in probe, again — in the evidence file that documents it as the repo's signature
   defect.** `httpcore.Validate` was executed and the result generalised to the 36 decode wrap sites,
   a different producer (C5/E3/E9). **Probing one producer of a sentinel says nothing about the
   others.**
2. **A self-contradiction across two files of one commit.** Evidence §3 recorded that nested deletes
   propagate; D4 then asserted three times that the shallow default fixes the aliasing defect (I-1/E6).
3. **The third consecutive rot of the at-rest enumeration, inside the paragraph warning about it**
   (C1/C2) — and the sentence counted its own markdown rows rather than columns.
4. **An interaction pass that stopped one step short.** The wedge was correctly identified and the
   weaker rule then applied to all four admission fields, when it was only needed for one (I-10/F7).
5. **Two prescribed tests that are mutually exclusive** (I-2), **one whose falsifier is unachievable**
   (E11), **one that pins the defect** (I-1), and **two that cannot pass** (F9) — in a plan whose §5
   demands a stated falsifier for every test.
6. **Two "machine-checked invariants" that are not machine-checkable**, one of which was executed and
   **passed over the exact rot it exists to catch** (I-6, C3, E10, F15). ⚠ The repo already has the
   working pattern — `engine/terminal_sites_test.go`, a `go/parser` walk, written for this exact
   class.
7. **Deleting two phases without re-deriving the pointers to them** (F1/C14).

## Required next steps, in order

1. **Decide the six decision-level items in section A–D before touching prose.** Four of them
   (D2's merged-map split, D2's byte bound, D3's proxy posture, D5's allow-list basis) are genuine
   design increments — this bundle no longer qualifies as "one-line fixes".
2. **Replace both prose invariants with `go/parser` walks** modelled on `engine/terminal_sites_test.go`,
   and re-derive the at-rest enumeration **from the migration files by machine**, not by hand.
3. **Fix the phase pointers last**, after the phase set is final — they rotted because they were
   written before the phases stopped moving.
4. **Re-audit.** ⚠ A bundle whose decisions changed has not been audited, and this round changed six.

⚠⚠ **Consider the scope question honestly before revising.** This is the delivery's **second**
failure and the lineage's **fourth**. Audit #1 said the mechanisms were sound; this round says four of
six decisions need increments. The B3 lesson — **bundle size is the multiplier** — applies to ADR-0186
itself: six decisions across six packages is the same shape that failed twice as twelve items. D1
(body caps) and D6 (at-rest posture) are separable and nearly ready; D2, D3, D4 and D5 each now carry a
design increment. **Splitting is on the table and is the owner's call.**

**Do not implement. Nothing here has been folded.**

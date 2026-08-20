# Spec — untrusted input and disclosure posture (backlog 54, 65, 98, 99, 104; posture for 100, 101)

> ## ⛔ AUDIT FAILED — 2026-08-21. NOT an input to implementation.
>
> Four lenses (execution / failure-modes / counting / **interaction**): **63 findings, 33
> Critical**. ⚠ **But the failure is different in kind from B3's**: three of the four lenses
> independently concluded the *decisions* are largely sound and the **plan** is where this
> breaks — *"six Criticals share one root cause: a decision stated in the ADR whose
> realisation lands in a package no phase assigns it to"*. Nothing here needs a design
> increment, unlike the deferred backlog-103/124 work.
>
> ⭐ **One change closes ~7 findings**: move the element bound from **evaluation** to
> **admission**. And D2's "count once per env" mandate is both unimplementable **and
> unnecessary** — the cost figure that forced it compared a worst case against a typical
> case; measured like-for-like, counting is ~12–13× *cheaper* than the `ctx` D2 refused.
>
> See `docs/plans/sweep-evidence/audit-0186-adjudication.md`.

- Date: 2026-08-21
- **Anchor:** this bundle's own commit on `design/authz-security-b3`. Every citation
  below was re-derived there. Where a file is volatile, the citation is a **symbol**.
- Bundle: this spec + `docs/adr/0186-untrusted-input-and-disclosure-posture.md`
  + `docs/plans/2026-08-21-untrusted-input-and-disclosure.md`
- Inherited evidence (executed, survived two audits):
  `docs/specs/2026-08-21-adr-0185-0186-premise-evidence.md` — ⚠ **§5 and §7 of that
  file are known-defective**, see §6 below. §1–4 and §6 hold.

## 0. Why this is its own delivery

This was half of the **B3 bundle** — a twelve-item authorization-and-security design
that **failed two rule-#9 audits**. The first (58 findings, 12 Critical) killed
individual decisions; the revision fixed all of them and the second (38 findings,
~13 Critical) killed the **interactions** between the four decisions that revision
rewrote. Owner decision, 2026-08-21: re-cut into three deliveries.

This is the first, and it is genuinely separable — the B3 spec's own §5 said
*"0186's decisions hold whether or not 0185 ships, and vice versa"*, and that
survived both audits unchallenged. ⚠ **One real dependency existed and has been
severed**: the draft added **401** (nothing authenticated the caller) and **503**
(the identity resolver errored) arms to the error classifier, both of which belong to
the identity delivery. They are removed from this record. Nothing here now depends on
a symbol ADR-0185 introduces.

The other two deliveries, for a reader who needs the map:
- **ADR-0185-core** — the actor travels in `context.Context` rather than the request
  body; constructing an engine without an authorizer is an error; an eligibility spec
  that states nothing denies. Backlog 51/52/53.
- **Deferred** — backlog 103 (deny-list attribute predicates allow when the process
  variable is missing) and backlog 124 (task completion never checks who claimed).
  Each needs a design increment that does not exist yet.

## 1. Problem

`wrkflw` is a library a consumer mounts into their own HTTP server. On the way **in**
it accepts unbounded input; on the way **out** it discloses more than it should.
Five verified defects plus a posture question:

| | today | item |
|---|---|---|
| request bodies | **no cap** at any of the 39 decode sites we own | **98** |
| expression evaluation | unbounded in **caller-supplied input size** — O(n²) measured | **99** |
| `httpcall` with an expression-derived URL | an **SSRF primitive**: no allowlist, no `CheckRedirect` | **65** |
| the instance read path | variables **aliased not copied**, and **no redaction hook** anywhere | **54** |
| 4xx error bodies | 403 echoes the **predicate source**; 400 echoes the **submitted value** | **104** |
| at rest | plaintext snapshot + plaintext journal, no integrity chain | **100/101** — posture only |

These do **not** compose into a chain the way the identity defects do; each is
independently exploitable and independently fixable. That is precisely why this
delivery can go first.

## 2. Verified current behaviour

Everything here was **executed**, or read from source at the anchor commit. What
could not be executed is labelled `ASSUMPTION (unverified)`.

The six findings are stated in full in **ADR-0186 §Context** and are not duplicated
here. What this section adds is the **corrections history** — what earlier drafts got
wrong, so a reader does not re-derive a refuted claim:

| claim | status |
|---|---|
| *"`expr.MaxNodes` should be set to bound cost"* | ⚠ **INVERTED.** `MaxNodes(0)` *disables* the check; never calling it leaves `DefaultMaxNodes = 1e4` **active**. Executed, and stated outright in the vendor godoc (`expr@v1.17.8/expr.go:221`). **Do not implement it.** |
| *"the ABAC and gateway evaluators are one surface"* | ⚠ **TWO surfaces.** `authz`'s is `expreval.New()` (5 s `DefaultTimeout` **enabled**); only the engine's gateway evaluator (`engine/conditions.go:43`, `WithTimeout(0)`) is wall-clock unbounded, deliberately, per ADR-0003/0049/0056. |
| *"fiber decodes with `BodyParser`"* | ⚠ **Name rotted.** Zero hits repo-wide; the live idiom is `c.Bind().JSON`. The count of 13 was right. |
| *"the caret line reprints the predicate source a third time"* | ⚠ **FALSE.** It is dots and a `^`. The source appears exactly **twice**. |
| *"the probe predicate is 44 characters"* | ⚠ **80** (`wc -c`). The argument (far under a 1e4-node budget) is unaffected — which is why nobody checked it. |
| *"mutating the returned view mutates instance state"* | ⚠ **WITHDRAWN, unverified in both directions.** The read path clones (`caching_instance_store.go:73-76` → `State.Clone()`; `cloneState` → `copyVars`). What remains is a **convention violation** — every other escape boundary in this repo clones — which is enough to fix it. |
| *"a `ctx` on `ConditionEvaluator` bounds evaluation cost"* | ⚠ **DROPPED.** Justified against the *import* rule `engine/purity_test.go` enforces, not the **deterministic-replay** invariant `engine/conditions.go:29-43` actually locks. Honouring it forces the engine default onto the goroutine path: **99.43 → 965.2 ns/op, 3 → 9 allocs**, reproduced independently by an auditor (97.62 → 976.7). |
| *"256 KiB of variables is the CPU mitigation"* | ⚠ **REFUTED by this bundle's own table** — 256 KiB admits ~40–50 k elements ⇒ ~45–60 s of CPU. It is a **payload** bound; the CPU bound is element cardinality. |
| *"5 000 elements ≈ 40 ms, 10 000 ≈ 150 ms"* (proposed by audit #1) | ⚠ **WRONG by ~15×.** Re-derived from the same ladder: k = 1.563/8000² ⇒ **5 000 ≈ 610 ms, 10 000 ≈ 2.442 s**. Formally adjudicated by the counting lens of audit #2, which noted audit #1 contradicted its own formula 16 lines after using it correctly. |
| *"the value-free 400 rendering happens in `ClassifyError`"* | ⚠ **NOT IMPLEMENTABLE there.** `runtime/validation/gate.go:45` is `fmt.Errorf("%w: %s", …)` — `%s`, so the typed error is a **string** by the time the transport sees it. The probe that "proved" it feasible called the vendor directly and never went through `Gate`. |
| *"oversize bodies return 413"* | ⚠ **They would return 400.** All 39 decode sites already wrap in `httpcore.ErrBadInput`, and `ClassifyError` is an **ordered** switch with the 400 arm at `:36-50`. |
| *"redaction in `mapInstance` cannot be bypassed"* | ⚠ **True of the mapper, false of the endpoints.** `GetInstanceSnapshot` (`endpoints.go:60`) and `GetActionableView` (`:72`) take **no mapper** and never reach `mapInstance`. Both non-admin. |

## 3. Scope

**In:** 54, 65, 98, 99, 104, and the documented posture for 100/101.

**Out (named so the audit can check the boundary):** everything identity —
51 (self-asserted principal), 52 (allow-all default), 53 (unstated spec allows),
103 (missing-variable predicates), 124 (claimant not checked), and the parked
102 (stale casbin policy). Also 32 (snapshot versioning), 60/91 (outbox envelopes),
96 (transport parity suite), 106 (readiness aggregate).

## 4. Decisions

Stated in full in **ADR-0186**. In one line each:

1. **Bodies and variables are capped by default** — 1 MiB body, 256 KiB variable map;
   oversize is a **413** via a bare `httpcore.ErrBodyTooLarge` sentinel, with the 413
   arm placed **before** the 400 arm.
2. **Evaluation input is bounded; `ConditionEvaluator` does NOT gain a `ctx`** —
   `expreval.WithMaxEnvElements(n)`, plumbed by `runtime.WithMaxEvalElements(n)`,
   default 10 000 (~2.4 s at the measured curve), **counted once per env**.
3. **Expression-derived URLs are restricted by default; author-typed URLs are not.**
4. **Redaction runs at the `ProcessInstance` → response boundary**, and the view
   copies rather than aliases.
5. **403 says nothing; 400 is an allow-list** — structured rendering for the one
   validation strategy that yields structured leaves, static text for the rest.
6. **At rest: the posture is documented, the mechanism deferred** — deliberately.

## 5. ⚠ Pairwise interactions between these decisions

Required by CLAUDE.md rule #9's interaction clause, and written by the author before
the audit because **an unwritten interaction is the cheapest possible Critical**.
This bundle's second audit failed on exactly this.

| pair | interaction | resolved? |
|---|---|---|
| **D1 × D5** | D1 introduces the oversize path; D5 owns the classifier that must map it. The 413 arm and the `ErrBadInput` wrap collide — this is where audit #2 found the 400/413 defect. | ✅ D1 mandates a **bare** sentinel; D5 orders 413 before 400. |
| **D2 × D1** | Both bound "size", in different units (elements vs bytes) and different packages. The draft called them "the same knob" and built two disconnected halves. | ✅ Two knobs, two stated jobs: elements bound **evaluation**, bytes bound **payload**. D1 explicitly disclaims the CPU rationale. |
| **D2 × the engine hot path** | The bound must be cheaper than the cost it replaces, or D2 is self-defeating (866 ns saved, ~19 µs spent at the default). | ✅ Counted **once per env**, not per evaluation. ⚠ The implementation must prove this — it is a prescribed test. |
| **D2 × `runtime`'s existing options** | `WithExpressionTimeout` and `WithConditionEvaluator` already both assign `driver.conditionEval`. A third writer is silent last-writer-wins. | ⚠ **OPEN** — must compose or refuse, not overwrite. Plan carries both alternatives; the audit should pick. |
| **D4 × D5** | Redaction changes what reaches the response; the classifier changes what an *error* response says. Disjoint paths (success vs error) — no interaction found. | ✅ none |
| **D4 × the response-customization feature** | `CustomizeConfig.InstanceMapper` is a documented product feature that replaces the default view wholesale. | ✅ Redaction runs above it, and above the two mapper-less endpoints. |
| **D3 × D2** | `WithURLExpr` is routed through `internal/expreval`, which D2 also changes. Does the new env bound apply to `httpcall`'s URL evaluation? | ⚠ **OPEN** — the env there is process variables, so the bound *should* apply, but neither decision says. The audit must settle it. |
| **D5 × D3/D6** | none — different surfaces. | ✅ none |

## 6. ⚠ Known defects in the inherited evidence file

`docs/specs/2026-08-21-adr-0185-0186-premise-evidence.md` was written for the B3
bundle and **audit #2 found three defects in it**. Two are inherited by this
delivery and are corrected here; a reader must not cite the file for them:

1. **§6 (jsonschema)** — the probe called the vendor directly. It proves the *leak*
   and it proves a value-free rendering is *constructible*, but **not** that it is
   constructible at `ClassifyError`. It is not. See §2's table.
2. **§2 (the `??` guard form)** — the rows were run with an **empty** `vars` map while
   sitting under a section declaring `vars = {"tier":"gold"}`. The finding it supports
   (that `??` does not parse unparenthesised) is **correct and independently
   confirmed**; the transcript is mislabelled. Not load-bearing for this delivery.
3. **§5 and §7** — the tri-state `Open` codec evidence and the `274/128/5`
   enumeration. Both belong to the **identity** delivery and are **defective**; §7's
   triple was inherited verbatim from audit #1 under a caption claiming nothing was
   inherited, and all three numbers are wrong. **Not used here.**

§1 (expr builtin behaviour), §3 (reference extraction shapes) and §4 (guard
dominance) hold and were re-confirmed; they belong to the deferred backlog-103
delivery, not this one.

## 7. What is still NOT executed

Labelled so the audit attacks the boundary rather than re-deriving it:

- `ASSUMPTION (unverified)`: the **fiber body-cap mechanism** — a `len(c.Body())`
  pre-check, reasoned from source (`BodyLimit` is a `fiber.Config` field set on
  `fiber.New`, `fiber/v3@v3.4.0/app.go:710`, which a mounted route group does not
  own). ⚠ Conceded in ADR-0186 D1: it is a **rejection, not a prevention** — the body
  is already buffered when it runs.
- `ASSUMPTION (unverified)`: the **1 MiB body default** and the **256 KiB variable
  default**. Both are judgement calls with nothing behind them.
- `ASSUMPTION (unverified)`: the element-bound extrapolations beyond n = 8 000 are
  arithmetic on the measured ladder, not fresh measurements. **The audit should
  re-measure at n = 10 000.**
- **Never executed:** the claim that stdlib and gin currently return 201 for a
  256 MiB body, and its heap figures. Inherited from a 2026-08-20 run, never
  re-derived. The *absence* of any cap is verified (`grep -rnE` → 0); the response
  behaviour is not.

## 8. Non-goals

- No key management. The library will not hold, rotate or derive encryption keys —
  which is why 100 (at-rest codec) is a posture, not a mechanism.
- No identity work. That is ADR-0185's delivery.
- No new transport, no gRPC, no BPMN2 XML.

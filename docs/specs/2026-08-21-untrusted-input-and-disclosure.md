# Spec — request bodies are capped by default, before they are parsed (backlog 98)

> ## ⚠ STRIPPED 2026-08-21 after SIX failed audits. NOT YET RE-AUDITED.
>
> Six decisions → two failed audits → three → a third → **one decision → a FOURTH (61 findings,
> 24 Critical)**. `docs/plans/sweep-evidence/audit4-0186-adjudication.md`.
> ⚠⚠ **Round 6 failed at a bundle size of ONE decision — the control experiment. Splitting is
> exhausted; every Critical was a SCOPE-BOUNDARY failure.**
> **Owner decision: strip to the minimum cap.** The four findings reached by 3–4 lenses each were
> all **ancillary mechanisms** — the mount-time construction error, the instrumentation, the fiber
> WARN, and the `*int64` tri-state. **All four are deleted, not redesigned.**
> The five decisions cut out are held in
> **`docs/specs/2026-08-21-untrusted-input-deferred-slices.md`** with every finding their audits
> established. **Nothing was dropped.**
> ⚠ **A bundle whose decisions changed has not been audited.** Not an input to implementation.

- Date: 2026-08-21
- **Anchor:** this bundle's own commit on `design/authz-security-b3`. ⚠ Citations name a **symbol**;
  line numbers appear only where exact ordering is load-bearing (`httpcore/errors.go`, the three
  discarding decode sites).
- Bundle: this spec + `docs/adr/0186-untrusted-input-and-disclosure-posture.md`
  + `docs/plans/2026-08-21-untrusted-input-and-disclosure.md`
  + `docs/specs/2026-08-21-adr-0186-premise-evidence.md`
  + `docs/specs/2026-08-21-untrusted-input-deferred-slices.md`

## 0. Why this is one decision

The lineage's five rounds, and what each taught:

| round | bundle | verdict | the lesson |
|---|---|---|---|
| 1 | B3, 12 items | 58 findings, 12 Critical | individual decisions were wrong |
| 2 | B3 revised | 38, ~13 | **the INTERACTIONS between the fixes** were wrong |
| 3 | ADR-0186, 6 decisions | 63, 33 | the **PLAN** was wrong |
| 4 | ADR-0186 folded | 56, 28 | **six of six DECISIONS** needed increments |
| 5 | ADR-0186, 3 decisions | 65, 20 | splitting fixed the interaction grid **and nothing else** |
| 6 | ADR-0186, **1 decision** | **61, 24** | ⭐⭐ **size is no longer the variable — every Critical was a SCOPE boundary** |

⭐⭐⭐ **Round 6 is the control experiment.** With one decision there are zero decision×decision
pairs, and the bundle still failed with 24 Criticals. **Bundle size is no longer the variable.**
All three lenses that commented agreed every Critical was a **boundary the bundle asserted and
never derived**, and the execution lens named the bias:

> **"The bundle's probes are narrow in a consistent direction: toward the fixture that demonstrates
> the fix."**

⚠⚠ **This is what execution does NOT protect against.** Execution catches a false premise; it
cannot catch a **narrow fixture**, because the probe passes. ⇒ **§5 of this spec now lists the
boundaries this delivery DERIVED from source**, and the plan requires searching the repo for an
existing convention before inventing one.

Round 5's three lessons still stand and are why the bundle is one decision:

1. **A REMOVAL is a change and generates its own grid.** Cutting three decisions created **9
   survivor×removed pairs**. The bundle derived **one**, wrote *"this table is complete at three"*,
   and shipped that false quantifier into its own audit brief. ⇒ **§4 of this spec derives the
   survivor×removed grid, which is now 1×5 = 5 pairs, and it is the section to attack.**
2. **Scope-boundary failures are not interaction failures.** Three Criticals sat one step outside a
   boundary the bundle drew and never re-derived — a directory glob, a package set, a config
   sentinel. ⇒ **"the failure was the grep's NET" generalises from enumerations to SCOPES.**
3. **A new mechanism carries risk unrelated to bundle size.** The 4xx opt-in was one round old and
   took 12 of 20 Criticals — refuted predecessor, unvalidated successor.

**This delivery introduces no new mechanism, no new exported interface and no new cross-package
contract.** It is a cap, a sentinel, and a status, in four packages.

## 1. Problem

| | today | item |
|---|---|---|
| request bodies | **no cap** at any of the 39 decode sites we own — and **3 of the 39 cannot report one**, because they discard the decode error | **98** |
| oversize requests | not merely uncapped: **executed, stdlib and gin return 2xx** for a 3 MiB body at a 1 MiB cap when the excess is trailing bytes | **98** |

## 2. Verified current behaviour

Everything here was **executed**, or read from source at the anchor. Unexecutable claims are
labelled `ASSUMPTION (unverified)` in §5.

| claim | status |
|---|---|
| 39 decode sites / 36 propagating / 3 discarding | ✅ **HELD across two audits**, latest by an **AST walk** rather than a grep. `stdlib:238`, `gin:265`, `fiber:255` are the discarders, all on the optional-body admin `ResolveIncident` route. |
| *"all 39 already wrap in `ErrBadInput`"* | ⚠ **FALSE — 3 discard to `_`.** A cap installed there fails, the failure is assigned to `_`, and the handler returns **2xx**. |
| *"a `len(c.Body())` pre-check caps fiber"* | ⚠ **Returns 400 on the amplification case.** `c.Body()` decompresses; a 63.7 KiB gzip expanding to 64 MiB yields `len == 33`. Use **`c.BodyRaw()`**. |
| *"`ErrBodyTooLarge` is a new sentinel"* | ⚠ **`action/httpcall.ErrBodyTooLarge` already exists**, means an outbound **response** exceeded 10 MiB, and is a **500**. Renamed `httpcore.ErrRequestBodyTooLarge`. |
| *"gin wraps the `MaxBytesError` again"* | ⚠ **FALSE.** stdlib and gin both surface the **bare** `*http.MaxBytesError`. Two shapes, not three. |
| *"oversize bodies return 413"* | ⚠ **They would return 400.** An error wrapping both `ErrBadInput` and the new sentinel classifies **400** — the switch is ordered with 400 at `:36-50`. |
| **[R5]** *"39 sites, one policy, one status"* | ⛔ **FALSE, executed.** Capping *during* the parse caps only the prefix the decoder consumed. At a 1 MiB cap: well-formed 3 MiB → 413 everywhere; 3 MiB with a syntax error at byte 3 → **400** on stdlib/gin, 413 on fiber; **a complete JSON value + 3 MiB of trailing bytes → `err == nil`, 2xx** on stdlib/gin. ⇒ **cap BEFORE parsing.** |
| **[R5]** *"`MaxBodyBytes = 0` is the unbounded opt-out"* | ⛔ **FALSE as implemented, executed.** `http.MaxBytesReader(w, body, 0)` → `err=http: request body too large`; `-1` identical. ⇒ **`n <= 0` must mean "do not install the wrapper"**, never "pass 0 through". |
| **[R6]** *"…so `MaxBodyBytes` must be a `*int64` tri-state"* | ⛔ **UNNECESSARY, and it invented a second convention.** `ResolveConfig` sets defaults **in the struct literal** before applying opts, with post-loop guards only for **nil-able** fields (`seam.go:39-58`) — so a plain `int64` default survives an explicit `0`. And `action/httpcall` **already ships this exact convention**: plain `int64`, `max <= 0` disables, default in the constructor, `io.ReadAll(io.LimitReader(r, max+1))`. ⇒ **plain `int64`.** Evidence §8.2. |
| **[R6]** *"unmarshal from the resulting buffer"* | ⛔ **AMBIGUOUS, and the readings disagree — 3 lenses, executed.** `json.Unmarshal` is **strict** about trailing data; the adapters' `json.Decoder` is **lenient**. `{"def_ref":"a:1"} zz` at a 64 B cap → stdlib 200→**400**, gin 200→**200**. ⇒ **feed the buffer to the site's EXISTING decoder.** Under-cap behaviour then changes for nobody. Evidence §8.1. |
| **[R6]** *"the read's own error distinguishes absent/EOF from oversize"* | ⛔ **FALSE, executed.** An over-declared `Content-Length` yields `unexpected EOF` with `errors.As(*MaxBytesError)` **false**, so every aborted upload would ship as **413**; and `io.ReadAll` returns `err == nil` for absent, empty, whitespace-only **and** truncated bodies. ⇒ **keep `errors.As`** as the discriminator. |
| **[R6]** *"buffering is a cost"* | ⛔ **REVERSED by measurement.** Buffered is ~**2 % faster** and allocates ~**37 % fewer** bytes at 1 MiB, and ~**22× faster** on an 8 MiB body at a 1 MiB cap. ⚠ The real unstated cost is **peak memory = cap × in-flight**, which nothing bounds. |
| **[R6]** *the bound is on size* | ⚠ **It is not a bound on TIME.** Executed: read-to-EOF means a chunked request with no terminating chunk **holds the handler indefinitely**. `MaxBytesReader` bounds bytes only. ⇒ documented residual; the consumer owns `ReadTimeout`. |
| **[R5]** *the gzip fixture as the falsifier for wire-bytes* | ⛔ **INVERTED.** With the bomb, `len(c.Body())` sees 33 — *under* the cap — so the wrong implementation returns 400 exactly like the right one. The discriminating fixture is **wire-large, decompressed-small**. |
| **[R6]** *the histogram and rejection counter* | ⛔ **NO HOME AND NO PHASE — 4 lenses.** `Instrumentation`'s fields are **unexported** and only `httpcore` builds instruments from `cfg.MeterProvider`; the ADR excluded `httpcore` **by name** ("0 decode sites"), arguing from *observation* sites about a *declaration* boundary. ⇒ **DELETED from this delivery.** |
| **[R6]** *the fiber mount WARN* | ⛔ **DELETED.** Executed: `ResolveConfig` runs **5× per adapter**, so a WARN in `Customize` fires 3–4× per documented mount, and one in `Mount` never fires for the admin group (ADR-0095). ⚠ And `(*fiber.App).Config()` **IS** exported (`app.go:1233`) — the earlier "unreachable" assumption is **refuted** — but a mounted `*fiber.Group` is not an `*App`. |
| **[R6]** *"negative → a construction error at mount"* | ⛔ **NO RETURN CHANNEL — 4 lenses.** `ResolveConfig`, `CustomizeOption`, all 15 `Customize` methods and all 6 `Mount`/`MountHealth` return nothing; adding one is the *"new exported interface"* this bundle's own Non-goals forbid. ⇒ **DELETED**; `n <= 0` simply disables. |

## 3. Scope

**In:** backlog **98** only.

**Out — deferred to their own deliveries, not dropped** (see the carry-forward record): 104 (what a
4xx body may say), 100/101 (at-rest posture), 54 (read-path disclosure), 65 (`httpcall` SSRF), 99
(variable-map admission bound).

**Deleted by the 2026-08-21 strip — NOT deferred, not silently dropped:**
- **All instrumentation** (histogram + rejection counter). ⚠ Consequence: a consumer **cannot
  measure their body-size distribution before the cap bites**. The lever is `WithMaxBodyBytes(0)`.
- **The fiber mount-time WARN.** The divergence above `fiber.Config.BodyLimit` is documented instead.
- **Mount-time validation of the cap value.** No channel exists to report it.
- **The `*int64` tri-state**, replaced by the repo's existing `int64` / `<= 0` disables convention.

**Also out, named so the audit can check the boundary:**
- **The correlation id, per-class 4xx logging, and any change to what a 4xx message says.** All
  belong to the deferred 4xx delivery. ⚠ **This delivery adds a 413 that carries no correlation id
  and writes no log record.** Stated as a gap, not hidden.
- **A decompressed-size bound for fiber** — the ceiling is `app.config.BodyLimit`, which a mounted
  route group does not own.
- **The trailing-byte gap in the explicitly-unbounded configuration** — closing it needs unbounded
  buffering, which is itself a memory-exhaustion primitive.

## 4. ⚠⚠ Interactions — COUPLINGS, not removals

Rule #9's interaction clause. ⚠⚠ **The previous revision's version of this table was judged
*"right to exist, wrong to be closed — it counts REMOVALS, not COUPLINGS"***: three of its five
cells held more than one coupling and four couplings were omitted, all with the same blind spot —
it asked *"what does the removed slice hand D1?"* and never *"what did D1's claims depend on that
left with the removal?"* **This table is rebuilt as one row per coupling.**

⚠ And one row of the previous table **discharged onto ADR text that did not exist** — `grep -E
'per-sentinel|notion|decoded-map|conflat'` over the ADR exited **1**. Every ✅ below names the file
and section that carries the resolution, so that failure is checkable.

| # | coupling | resolved? |
|---|---|---|
| 1 | **The 413 arm's ORDER.** A bare sentinel still classifies 400 unless the 413 arm precedes the ordered 400 arm. | ✅ ADR §Decision, "Oversize is a 413" — arm before 400, with a comment, plus the standing ordering invariant for every future arm. |
| 2 | **The 413's MESSAGE and correlation id left with the 4xx delivery.** | ⚠ **Stated, NOT resolved.** ADR Negative: *"a 413 carries no correlation id and produces no log record"*. Verified: `writeErr` logs only at `status >= 500` and this delivery does not change it. **Honest gap.** |
| 3 | **A future `ErrVariablesTooLarge` joins THIS arm**, at which point one static 413 message serves two causes. | ⚠ **Stated, NOT resolved — one-way, out of this delivery.** ADR §Decision records the ordering invariant *"any future arm must state its position and carry a test"*, and the carry-forward's §BOUND owns the message question. ⚠ This delivery ships **no** 413 message text, so there is nothing here for that delivery to contradict. |
| 4 | **`action/httpcall.ErrBodyTooLarge` already exists and means 500.** | ✅ Renamed `ErrRequestBodyTooLarge`; a test pins that `httpcall`'s still classifies 500. ADR §Decision. |
| 5 | ⭐ **`action/httpcall` also already ships the CAP CONVENTION** — `int64`, `<= 0` disables, constructor default, `io.ReadAll(io.LimitReader(...))`. | ✅ **Adopted rather than re-invented.** ADR §Context, "The existing in-repo convention"; Evidence §8.2. ⚠ This coupling was invisible to five audit rounds. |
| 6 | ⭐ **`runtime/kernel/cursorcodec.go` already handles trailing data after a JSON value** (ADR-0160). | ✅ Cited in ADR §Context and used as the reason the cap must bound the **read**, not the parse. |
| 7 | **ADR-0095 keeps admin routes out of `Mount`**, so the parity suite cannot see the three discarding sites. | ✅ The per-adapter test **names the admin route**; parity is explicitly not the net. Confirmed by mutation in round 5. |
| 8 | ⭐ **`MountGroups` consumers cannot pass options.** `MountGroups(r, groups...)` calls `Customize(r)` with none (`seam.go:108`), so the 1 MiB default applies to the documented consumer extension seam. | ✅ **Not a gap — the existing godoc already names the escape**: *"Groups needing distinct base paths or middleware call Customize directly with the relevant options."* Recorded in ADR Negative and repeated in `SECURITY.md`. Derived from source, Evidence §8.3. |
| 9 | ⭐ **A per-mount diagnostic would fire 5× per adapter.** `ResolveConfig` has **15** call sites, 5 per adapter. | ✅ Which is **why the fiber WARN is deleted**. ADR §"What this delivery deliberately does NOT do". |
| 10 | **Only `httpcore` can build an instrument** (unexported `Instrumentation` fields; adapters import no otel). | ✅ Which is **why instrumentation is deleted**. The consequence — no pre-cap measurement — is stated in ADR Negative rather than papered over. |
| 11 | ⚠ **The cap bounds a REQUEST; it does not bound an INSTANCE.** Per-instance accumulation via repeated compliant requests is unbounded. | ⚠ **Stated, NOT resolved** — it is the deferred variable-bound delivery, whose own evidence has five individually-compliant signal deliveries reaching 789 KiB. ADR Negative says so, so the Positive is not read as more than it claims. |
| 12 | **Reading to EOF couples the cap to TIME**, which nothing in this delivery bounds. | ⚠ **Stated, NOT resolved.** ADR §"The bound is on SIZE, not on TIME": the consumer owns `ReadTimeout`, and all three `examples/` set `ReadHeaderTimeout` but not `ReadTimeout`. |
| 13 | **Peak memory couples the cap to CONCURRENCY** (`cap × in-flight`). | ⚠ **Stated, NOT resolved.** ADR Negative. |
| 14 | **Fiber's own `BodyLimit` sits above ours** and rejects before the group is reached. | ✅ Documented divergence, ADR §"deliberately does NOT do". ⚠ `(*fiber.App).Config()` is exported but a mounted `*fiber.Group` is not an `*App`. |

⚠ **Five rows are marked "stated, not resolved" on purpose.** Each is a real gap this delivery
chooses not to close, named so it cannot be mistaken for closure — the failure mode that produced
the previous table's ✅ pointing at absent text.

## 5. Boundaries DERIVED from source, and what is still NOT executed

⚠⚠ **This section is new, and it exists because every Critical in round 6 was a boundary the bundle
ASSERTED and never DERIVED.** Each row below was read out of the code before the design was written.

| boundary | derived value | where |
|---|---|---|
| `ResolveConfig` call sites | **15 — exactly 5 per adapter** | `grep`, non-test |
| what can carry an error out of mounting | **nothing** — `ResolveConfig`, `CustomizeOption`, `RouteCustomizer.Customize`, `MountGroups`, all `Mount`/`MountHealth` | `seam.go`, three `groups.go` |
| how a `MountGroups` consumer configures a group | it cannot; **its godoc already names the escape** (call `Customize` directly) | `seam.go:105-111` |
| who can build an instrument | **only `httpcore`** — unexported `Instrumentation` fields; adapters import no otel | `observability.go` |
| existing convention for bounding a body | **`action/httpcall`** — `int64`, `<= 0` disables, constructor default | `httpcall.go:186,191,194,214` |
| existing handling of trailing data after a JSON value | **`cursorcodec.go:50-58`** (ADR-0160) | `runtime/kernel` |
| is `fiber.Config.BodyLimit` reachable? | `(*fiber.App).Config()` **is exported**; a mounted `*fiber.Group` is not an `*App` | `fiber/v3@v3.4.0/app.go:1233` |
| does `ResolveConfig` clobber an explicit `0`? | **No** — defaults are in the struct literal; post-loop guards cover only nil-able fields | `seam.go:39-58` |

**Still NOT executed — labelled so the audit attacks the boundary rather than re-deriving it:**

- `ASSUMPTION (unverified)`: **gin's `ShouldBindJSON` behaves identically when `gc.Request.Body` is
  reassigned to the buffer.** Evidence §7.1 executed the *decoder* half; the **binder + validator**
  half is what phase 2's gin test must discharge. ⚠ **Do not infer it from the decoder result** —
  that inference is the §6.3a mistake in miniature.
- `ASSUMPTION (unverified)`: the **1 MiB default**. A judgement call. One datapoint runs the other
  way: `action/httpcall` caps an outbound *response* at 10 MiB.
- `ASSUMPTION (unverified)`: the end-to-end path from `WithMaxBodyBytes(0)` through a real adapter
  to a decode site. Phase 2's opt-out test discharges it.
- **Discharged — do not re-derive:** the 39/36/3 split (AST walk, four rounds); the bare
  `*http.MaxBytesError` through stdlib and gin; `BodyRaw()` as the wire body; the 413/400 ordering;
  `MaxBytesReader(…, 0)` rejecting everything; the trailing-byte 2xx; `ClassifyError`'s six arms
  and all six anchors; 26 routes; **no fourth package reads a request body**; buffering being
  faster, not slower; the read-to-EOF hang; `errors.As` false on an over-declared `Content-Length`.

## 6. Non-goals

- No change to what any 4xx message *says*. That is the deferred 4xx delivery, and touching it here
  re-creates the bundle that just failed.
- No correlation id, no logging changes, no redaction, no SSRF posture, no at-rest work.
- No new exported interface and no new cross-package contract. ⚠ **This is load-bearing**: it is
  why the mount-time construction error and the instrumentation are deleted rather than built.
- **No metric.** A consumer cannot measure before the cap bites; the lever is `WithMaxBodyBytes(0)`.
- **No mount-time diagnostics.** No channel exists for them.

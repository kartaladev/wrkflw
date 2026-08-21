# ADR-0186 (stripped: request body caps) — audit adjudication, round 7

**Date:** 2026-08-21 · **Bundle audited:** `27ff5841` on `design/authz-security-b3` — the stripped
one-decision bundle. Five files.
**Lenses:** execution · failure-modes · counting · interaction — four Opus agents, four detached
worktrees. **Step-0 presence check passed in all four.**
**Reports:** `audit5-0186-{execution,failure-modes,counting,interaction}.md` (2,305 lines).

## ⛔ VERDICT: FAILS. 57 findings, 14 Critical.

| lens | findings | Critical |
|---|---|---|
| execution | 14 | 5 |
| interaction | 16 | 4 |
| counting | 15 | 3 |
| failure-modes | 12 | 2 |
| **total** | **57** | **14** |

## ⭐⭐⭐ THE TREND IS NOW THE FINDING

| round | scope | findings | Critical |
|---|---|---|---|
| 1 | B3, 12 items | 58 | 12 |
| 2 | B3 revised | 38 | ~13 |
| 3 | ADR-0186, 6 decisions | 63 | 33 |
| 4 | folded | 56 | 28 |
| 5 | 3 decisions | 65 | 20 |
| 6 | **1 decision** | 61 | 24 |
| 7 | **1 decision, stripped to a minimum** | **57** | **14** |

**Scope has shrunk roughly twelve-fold. The finding count has not moved** — every round lands
between 56 and 65. Criticals are falling (33 → 14) but are not converging toward zero.

⇒ **The finding rate is a property of the PROCESS, not of the bundle.** Splitting was tried and
exhausted (round 6 was the control). Stripping was tried and the count held (round 7). There is no
smaller scope left: the delivery is now one option, one sentinel, one status.

## ⭐⭐⭐ And the reason is now precisely characterised, by two lenses independently

- **counting:** *"All three Criticals are SCOPE, and two are round 6's exact shape: **a boundary
  derived correctly at one level, then asserted one level up without re-derivation.**"*
- **execution:** *"Evidence §8.1's four fixtures contain **no `Content-Encoding` header, no chunked
  framing, and no mis-declared `Content-Length`** — **the bias the bundle diagnoses in its own
  banner is still fully present in the section it calls 'the whole design'.**"*

⚠⚠ **That second sentence is the whole problem.** Round 6 named the bias ("probes narrow toward the
fixture that demonstrates the fix"), round 7's bundle *quoted that diagnosis in its banner*, and
then **reproduced it in its central evidence section**. Naming a bias does not remove it, because
the author still chooses the fixtures.

## The Criticals

### A. Needs a DESIGN INCREMENT — not a document fix

- ⛔⛔ **E4 — the cap is completely BYPASSED on fiber by `Content-Encoding: gzip`.** Verified at
  source by the controller: `fiber/v3@v3.4.0/bind.go:309` calls `b.ctx.Body()` — the
  **decompressed** body. So the prescribed `len(c.BodyRaw())` pre-check bounds **wire** bytes while
  the parse consumes **decompressed** bytes. Measured on a real fiber app at a 1 MiB cap: a
  **3,121-byte** request parsed **3,145,761 bytes** into a variables map and returned **200**.
  ⇒ Kills *"over-cap bodies are rejected whatever their shape"* (false on **13 of 39** sites) and
  the memory claim (fiber's real bound is `fiber.Config.BodyLimit`, in which `MaxBodyBytes` does not
  appear).
  ⚠ **This is the exact inverse of the defect four rounds were spent on.** `BodyRaw()` was adopted
  so the *check* sees wire bytes; nobody asked what the *parse* reads. **Fix: on fiber check BOTH**
  `len(c.BodyRaw())` (transfer) **and** `len(c.Body())` (what will be parsed) — the latter costs
  nothing extra because `Bind().JSON` calls it anyway.

### B. Boundaries derived at one level, asserted at the next

- ⛔ **C2 — `Mount` reaches only 6 of 13 decode sites per adapter.** Mapping all 39 sites to their
  owning `Customize` method: **6 via `Mount`**, **7 via `AdminRoutes`** (21 of 39 repo-wide,
  including **all three discarding sites**), 0 health, and `MountHealth` forwards no options.
  So `Mount(mux, svc, WithMaxBodyBytes(0))` — the migration lever named in five places — leaves
  **21 of 39 sites at the 1 MiB default**. The bundle derived the `MountGroups` case and never
  derived the `Mount` case.
- ⛔ **I1 — `WithMaxBodyBytes(0)` does not compile as written.** `CustomizeOption[R]` is generic with
  `R` only in the result type, so Go cannot infer it. The repo's own remedy is a **non-generic
  per-adapter alias** (`stdlib/options.go:12`, `gin:26`, `fiber:23` all ship `WithBasePath`) —
  **three new symbols in three packages**, with no map row, no phase and no brief. Verified by the
  controller. The uncompilable string is prescribed verbatim for `SECURITY.md`.
- ⛔ **C1 — `SECURITY.md` contains NEITHER residual the ADR discharges onto it**, and one of those
  sentences is tagged *"Verified from source."* Verified: the grep exits 1 with a valid positive
  control. ⚠ **Spec §4's rebuilt coupling table — whose stated purpose was to stop ✅s discharging
  onto absent text — does it again one document over.**
- ⛔ **C3 — "the parity suite structurally cannot see the admin routes" is FALSE**, refuted by the
  file phase 3 edits. Verified: `parity_test.go:663,670,677` mounts `AdminRoutes` by hand on all
  three adapters **today**. Asserted in **four** places, inherited from ADR-0095, never checked.
  ⇒ The plan **forbids the cheapest correct net** and scatters it into three parallel agents.
- ⛔ **F6/F11 — `WithMaxBodyBytes(0)` is broken on fiber twice.** The ADR's *"`n <= 0` ⇒ do not
  install the wrapper"* names **stdlib and gin only**; plan §2's map row omits `fiber`. The obvious
  fiber pre-check with cap 0 returns **413 on an ordinary 1 MiB body** (executed). And fiber's own
  `BodyLimit` remains the real ceiling — the mount-time WARN that would have surfaced this was
  deleted by this same delivery.

### C. Residuals that are false, incomplete, or self-contradicting

- ⛔ **E7 + F1 — the slowloris hang is CREATED by this delivery, and the residual that "states" it
  used the one fixture where old and new behave identically.** The bundle executed
  *chunked-with-no-terminator*; today's server hangs on that too. The discriminating fixture is
  `Content-Length: 400000` (above net/http's 256 KiB drain tolerance, below the cap) + a complete
  JSON value + a slow dribble: **today 0 s and 50/50 handlers return; new code never returns, 0/50,
  goroutines +150.** ⚠ `ReadTimeout` fixes it (measured 1.001 s) and **none** of the three
  `examples/` sets it.
- ⛔ **I7 + F4 — "peak memory is `MaxBodyBytes × in-flight`" is false for a third of the surface.**
  On fiber it is `fiber.Config.BodyLimit × in-flight` (4× larger by default) and `MaxBodyBytes` does
  not enter it. On stdlib/gin it is **2.12× the cap** from `io.ReadAll` doubling — **including for
  every rejected request**.
- ⛔ **I3 + F9 — the bundle never specifies the 413's `ErrorBody`, and its two documents contradict
  each other.** Spec §4 row 3 says *"ships no 413 message text"*; the carry-forward says a static
  one that is *"true as written"*. Verified: `ClassifyError` sets `Message: err.Error()` on **every**
  arm, so a 413 **does** ship text — leaking `workflow-httpcore:` and never naming the limit.
- ⛔ **F3 — "a consumer cannot measure" is FALSE in the half that matters.**
  `wrkflw_rest_requests_total{http.status_code}` already exists (`observability.go:36-57`) and all
  three `observe` wrappers already feed it the handler status, so **every 413 is already counted
  with no new code**. ⚠ **Third time this lineage asserted a gap the repo had already filled.**

### D. Prescribed tests that cannot do their job

- ⛔ **E6 — plan test 8's falsifier is inverted IN THE VERY SENTENCE that says "an earlier revision
  had this backwards."** Measured: row 1 discriminates `BodyRaw` from `Body`; row 2 passes under
  both. And spec §2's rule *"wire-large, decompressed-small"* names a fixture **gzip cannot
  produce**.
- ⛔ **E3 — plan test 7, the designated discharge for the bundle's one live
  `ASSUMPTION (unverified)`, cannot be written.** The repo has **zero `binding:` tags**, so gin's
  validator is never engaged; aimed at `httpcore.Validate` instead, it **cannot fail**.
- **I2** — the *"`httpcall.ErrBodyTooLarge` still classifies 500"* test **passes today unchanged**;
  `errors.Is` compares identity, so no naming choice can make it fail.
- **I8** — phase 3's prescribed fiber-divergence case is unwritable: `hitFiber` `t.Fatalf`s on
  exactly that condition.

### E. The strip's own summary was wrong

- **M1 — the headline *"the four 3–4-lens findings were all ancillary, not the cap"* is false both
  ways.** Two of round 6's four **are** the cap's core mechanism and were *redesigned*; the fiber
  WARN and the tri-state have **no** multi-lens attribution in round 6 at all. ⚠ The
  celebratory-sentence failure, in the sentence celebrating the strip.
- **M5** — the carry-forward's provenance commits `1e527347` / `6cddb7b1` are **unreachable from
  every ref** (fold-don't-stack orphaning), in the one file whose job is *"nothing was dropped"*.
  Verified. ⚠ A repo lesson already recorded and hit anyway.
- **Minor, and mine:** the *"slice 4"* vs *"slice 6"* error **round 6 found is still not fixed**
  (3 occurrences). A fix recorded as made and not made.

## ⭐ What HELD — verified, do not re-derive

- **The gin buffer-swap assumption is DISCHARGED** — 14 fixtures, byte-for-byte identical. (Just
  not by the test the plan names.)
- **The `int64` / `<= 0` convention works END TO END** — a lens implemented phase 1 plus one phase-2
  site in its worktree and `./transport/...` stayed green.
- **413-before-400 ordering confirmed in BOTH wrap orders.** `writeErr` really does log only at
  `>= 500`.
- **The arithmetic was right for the SEVENTH consecutive round** — 39/36/3, 13/13/13, six
  `ClassifyError` arms and all six anchors, 26 = 9+15+2, 15 `ResolveConfig` sites, 15 `Customize`,
  6 `Mount`/`MountHealth`, all four `httpcall.go` anchors, all three fiber vendor anchors, and all
  six prior lineage totals — every one exact.

## Required next steps

1. ⛔ **Do not implement as written.** E4 alone means the fiber mechanism does not work.
2. ⚠⚠ **Do not respond by splitting or stripping again.** Round 6 exhausted splitting; round 7
   exhausted stripping. The scope is one option, one sentinel, one status, and the count held.
3. **E4 has a concrete fix**: on fiber, bound **both** `len(c.BodyRaw())` and `len(c.Body())`.
4. **Adopt what the audit already built**: a lens implemented phase 1 + a phase-2 site and it went
   green. That is the cheapest available evidence and it is code, not prose.
5. ⚠⚠⚠ **The recommendation this round earns: stop designing this on paper.** Seven rounds, ~57
   findings each, scope down twelve-fold, and the last two rounds' Criticals are facts about
   `bind.go`, `seam.go`, `parity_test.go` and `observability.go` that **only reading and running the
   code surfaces**. CLAUDE.md rule #11 already budgets for this — *"expect implementation to correct
   the design"*. The audit findings are now a better test list than the plan is.

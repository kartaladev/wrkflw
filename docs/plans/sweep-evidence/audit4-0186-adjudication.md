# ADR-0186 (ONE decision: request body caps) — audit adjudication

**Date:** 2026-08-21 · **Bundle audited:** `85d6bb68` on `design/authz-security-b3` — the
one-decision re-cut. Five files.
**Lenses:** execution · failure-modes · counting · interaction — four Opus agents, four detached
worktrees at the bundle commit. **Step-0 presence check passed in all four.**
**Reports:** `audit4-0186-{execution,failure-modes,counting,interaction}.md` (2,956 lines, rescued
into the repo on completion).

## ⛔ VERDICT: FAILS. 61 findings, 24 Critical.

| lens | findings | Critical | Major | Minor |
|---|---|---|---|---|
| execution | 19 | 6 | 6 | 7 |
| failure-modes | 20 | 9 | 10 | 1 |
| interaction | 14 | 6 | 7 | 1 |
| counting | 8 | 3 | 3 | 2 |
| **total** | **61** | **24** | **26** | **11** |

⚠⚠ **This is the SIXTH consecutive failure, and the FIRST at a bundle size of one decision.**
That is the finding. Splitting is now exhausted as a remedy: **there is nothing left to split.**

## ⭐⭐⭐ The diagnosis has CHANGED, and this is the round's real output

Rounds 1–4 failed on **decision content** and **interactions**. Round 5 proved splitting fixes the
interaction grid **and nothing else**. Round 6 removed the last interaction pair — and produced
**24 Criticals**, of which, by all three lenses that commented:

> **Every single one is a SCOPE-BOUNDARY failure.**

- *counting*: "All three Criticals are **scope** failures — a boundary the bundle drew and never
  re-derived (what can carry an error; which axis body shapes vary on; which package owns an
  instrument)."
- *failure-modes*: "reasoning inside each declared boundary is sound; **almost every Critical sits
  one step outside a boundary the bundle drew and never re-derived**" — listing seven such
  boundaries by name.
- *execution*: "**every Critical came from widening a fixture the bundle had already run and
  passed**."

⭐⭐⭐ **And the execution lens named the bias precisely. This is the most valuable sentence the
lineage has produced:**

> **"The bundle's probes are narrow in a consistent direction: toward the fixture that
> demonstrates the fix."**

That is not carelessness and it is not fixable by splitting, re-reading, or auditing harder. It is
a *systematic* bias in how an author chooses evidence: you reach for the case that proves your
change works, and that case is by construction the one that cannot embarrass it.

⚠⚠ **Corollary, and it is the actionable one:** *execution* was supposed to be this repo's
protection against false premises, and it is — but only against premises. It does **not** protect
against a **narrow fixture**, because the probe passes. Round 5 already found one instance (the
author's §6.3a). Round 6 found that essentially *every* Critical is one.

## ⭐⭐ Four findings reached by THREE OR MORE lenses independently

| # | finding | lenses |
|---|---|---|
| 1 | **"negative `MaxBodyBytes` → a construction error at mount" has NO RETURN CHANNEL** — `ResolveConfig`, `CustomizeOption`, all 15 `Customize` methods and all 6 `Mount`/`MountHealth` return nothing. Adding one contradicts the bundle's own *"no new exported interface"*, stated in **both** the ADR's Positive and the spec's Non-goals | **E1 + C1 + F6 + I1** (4) |
| 2 | **The histogram and rejection counter have no home and no phase.** `httpcore.Instrumentation`'s fields are all **unexported**, it is the only package that builds instruments from `cfg.MeterProvider`, and the three adapters import no otel. The ADR excludes `httpcore` **by name** ("0 decode sites") — arguing from *observation* sites about a *declaration* boundary. As written, phase 2's **three parallel agents each invent the same instrument** | **E7 + C5 + F7 + I3** (4) |
| 3 | **"Unmarshal from the resulting buffer" is unspecified, and the two readings disagree on UNDER-CAP trailing bytes.** Executed: `json.Unmarshal` **rejects** trailing data; gin's `ShouldBindJSON` (a `Decoder`) **ignores** it. `{"def_ref":"a:1"} zz` at a 64 B cap → stdlib 200→**400**, gin 200→**200**. ⚠ The bundle's own evidence §7.1 validated the *lenient* form while the ADR prescribes the *strict* one | **E2 + C4 + F3** (3) |
| 4 | **"The read's own error distinguishes absent/EOF from oversize" is FALSE.** Executed over a real socket: an over-declared `Content-Length` yields `unexpected EOF` with `errors.As(*MaxBytesError) = false`, so **every aborted/truncated upload ships as 413**; and `io.ReadAll` returns `err == nil` for absent, empty, whitespace-only **and** truncated bodies. The plan **deletes the only discriminator** (`errors.As`) that the bundle's own evidence shows is easy to keep | **E4 + F9 + F2** (3) |

## The other Criticals, grouped

### A. Boundaries the bundle asserted and never derived

- ⭐ **E14/E15 — the mount boundary is FIVE functions per adapter, not one.** Executed with a
  counting `WithRouterFunc`: `fiber.Mount` → 3 `ResolveConfig` invocations; `Mount` +
  `AdminRoutes.Customize` → 4. And **`AdminRoutes` is excluded from `Mount` by design** (ADR-0095),
  so a WARN in `Mount` never fires for the route group holding the three discarding sites, while a
  WARN in `Customize` fires 3–4× per documented mount.
- ⭐⭐ **E15 — `httpcore.MountGroups` has NO `CustomizeOption` parameter at all.** Verified by the
  controller: `func MountGroups[R any](r R, groups ...RouteCustomizer[R])` (`seam.go:108`).
  `seam.go` documents this as **the consumer extension seam**. Those consumers get the 1 MiB cap
  with **no way to raise it or opt out** — which makes the ADR's entire migration procedure
  (*"run with `MaxBodyBytes` explicitly `0` first"*) **impossible** for them.
- **F15 — `action/httpcall` ALREADY SHIPS THIS EXACT MECHANISM, with an incompatible convention.**
  Verified by the controller: `io.ReadAll(io.LimitReader(r, max+1))` (`httpcall.go:194`), a plain
  **`int64`** (not a pointer), **`max <= 0` disables** (`:191`), and the default applied **in the
  constructor** (`:214`) — documented in six places. ⚠⚠ **That convention solves the tri-state
  problem without a pointer**, because "unset" is never observed. The bundle invented a second,
  inconsistent convention one package over.
- **F13 + E12 — spec §5's `ASSUMPTION (unverified)` that `fiber.Config.BodyLimit` is unreachable is
  REFUTED.** Verified by the controller: `func (app *App) Config() Config` is exported
  (`fiber/v3@v3.4.0/app.go:1233`). Only `*fiber.Group` is unreachable. ⚠ And the WARN as specified
  **cannot fire** for `BodyLimit < MaxBodyBytes` — it is blind in the zero-config default.

### B. The mechanism's own unhandled cases

- ⭐⭐ **F1 — read-before-parse introduces an unbounded WAIT.** It replaces *return-on-first-value*
  with *wait-for-EOF*. Executed against a real `http.Server`: a chunked request with no terminating
  chunk **holds the handler indefinitely**. The ADR bounds **space** ("bounded by the cap") and
  never **time**, and all three `examples/` set `ReadHeaderTimeout` but not `ReadTimeout`.
  ⇒ **The fix introduces a slowloris primitive the design does not mention.**
- ⭐⭐ **F4 + F5 + F17 — the migration procedure cannot produce its own measurement.** The ADR
  records the histogram *"at the body read"*, forbids *"at `json.Decoder`"*, then prescribes
  running with `MaxBodyBytes = 0` — but **the unbounded path has no body read**; it streams, by
  design. The histogram is empty in exactly the mode the upgrade path mandates. Compounded: `0` is
  not unbounded on fiber either (fasthttp still rejects at `fiber.Config.BodyLimit`), and 2 of 3
  `examples/` have no `MeterProvider`. ⇒ **The only documented migration for a default-on breaking
  change works in NO configuration.**
- **F18 — the prescribed compressed-body PARITY case cannot pass.** Executed: a gzip request is
  **2xx on fiber, 400 on stdlib/gin**. And on fiber the cap does not bound what the engine sees.

### C. Claims refuted on their own terms

- **E16 — the "buffering is a cost" Consequence is WRONG, and measured.** 3 runs, <1.5 % spread:
  buffered is **2 % faster** and allocates **37 % fewer bytes** at 1 MiB, and **~22× faster** on an
  8 MiB body at a 1 MiB cap. ⚠ The real cost the bundle never states is **peak memory = cap ×
  in-flight requests**, which nothing bounds.
- **I6 — the Positive's *"the unbounded-body surface closes on all 39 sites"* is false**: a
  per-request wire cap does not bound per-instance accumulation. §BOUND's own executed evidence has
  five *individually compliant* signal deliveries reaching 789 KiB / 49,995 elements.
- **C2 — phase 1 test 2's `nil`-row falsifier is VACUOUS.** Executed: the post-loop idiom named as
  the mutation is *correct* over `*int64`. Another prescribed test that cannot fail.
  ⚠ Moot if F15's existing convention is adopted.
- **I4/I5 + C6 — spec §4 discharges onto text that does not exist.** Verified by the controller:
  `grep -E 'per-sentinel|notion|decoded-map|conflat'` over the ADR **exits 1**. And the
  carry-forward's cross-slice paragraph says *"slice 4"* three times where its own table says
  **slice 6**; "three bundles" should be five.

### D. The interaction lens's verdict on spec §4 — the grid the author added in response to round 5

> **"Right to exist, wrong to be closed. It counts REMOVALS, not COUPLINGS."**

Three of its five cells already hold more than one coupling, and four couplings are omitted
(I6, I7, I10, I13) — all the same blind spot: the grid asks *"what does the removed slice hand
D1?"* and never *"what did D1's claims depend on that left with the removal?"*
✅ Of the two cells marked *"stated, not resolved"*, the no-correlation-id/no-log one is **honest**
(verified: `write.go` logs only at `status >= 500`); the variable-bound one is **not**.

## ⭐ What HELD — verified, do not re-derive

- **39 decode sites = 13+13+13, `httpcore` 0, all in `groups.go`** — independent `go/parser` walk.
- **The 36/3 split at exactly `stdlib:238` / `gin:265` / `fiber:255`**, all on the same admin route.
- **`ClassifyError`'s 6 ordered arms with ALL SIX line anchors exact** (`:28/:32/:34/:36-50/:51/:57`).
- **26 routes = 9 + 15 + 2** on all three adapters. **No fourth package reads a request body** —
  repo-wide walk; ⭐ the boundary step that missed `engine` last round is **clean here**.
- The double-wrap → 400 and `httpcall.ErrBodyTooLarge` → 500 premises.
- **The `*int64` tri-state works** through a line-for-line `ResolveConfig` replica (E9), discharging
  evidence §7.2's assumption — ⚠ but F15 shows a simpler in-repo convention already exists.
- The corrected `-E` grep works, with a positive control proving the literal-pipe form still broken.
- Next free ADR is genuinely **0187**; zero symbol collisions; all fiber vendor citations exact;
  **all five prior lineage finding-counts match their adjudication records**.

## ⚠⚠ What the controller got wrong, recorded so it is not repeated

1. **Two mechanisms invented where the repo already had one.** `cursorcodec.go`'s trailing-data
   guard (cited as prior art in the ADR, then not used) and `httpcall`'s
   `int64`/non-positive-disables/constructor-default cap (not cited at all). ⭐ **"Search the repo
   for an existing convention" is not in any checklist and should be.**
2. **An evidence section that used two different decode idioms and reported them as one result.**
   §7.1's `BEFORE` rows used strict `json.Unmarshal`; its `GIN-RESET` row used a lenient
   `json.Decoder`. The inconsistency is visible in the transcript I pasted.
3. **A spec row discharging onto ADR text I had deleted in the same rewrite.**
4. **An `ASSUMPTION (unverified)` that was one `go doc` away from being resolved** (`(*fiber.App).Config()`).
5. **A stated cost that measurement reversed** ("buffering is a cost" — it is faster and allocates
   less), while the real cost (peak memory × in-flight) went unstated.

## Required next steps

1. ⛔ **Do not implement.** Nothing has been folded.
2. ⚠⚠ **Do not respond by splitting.** There is nothing left to split; round 6 is the control
   experiment that proves size is no longer the variable.
3. **The four-lens Criticals are all ANCILLARY mechanisms, not the cap:** the mount-time
   construction error, the instrumentation, the mount WARN, and the tri-state. **Deleting them
   removes those findings by construction rather than by design.** What remains — read the body
   under a cap, one sentinel, 413, three adapters, three discarding sites — is the actual security
   control.
4. **Adopt `action/httpcall`'s existing convention** rather than inventing one: plain `int64`,
   non-positive disables, default applied in the constructor.
5. **Decide the trailing-byte policy explicitly** (strict vs lenient) and make it identical across
   adapters, with an under-cap fixture in the tests. This is the one genuine design question left.
6. **Bound the WAIT, not only the size** — a read deadline, or state plainly that the consumer owns
   `ReadTimeout`.
7. **Before the next revision, derive the boundaries from code:** what can carry an error, which
   package owns an instrument, how many mount entry points exist, and what conventions already
   exist for this problem. Every Critical in this round was one of those.

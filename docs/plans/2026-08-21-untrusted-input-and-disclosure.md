# Plan — request bodies are capped by default, before they are parsed (ADR-0186)

> ## ⚠ STRIPPED 2026-08-21 after SIX failed audits. NOT YET RE-AUDITED.
>
> Round 6 failed at a bundle size of **ONE decision** (61 findings, 24 Critical) — the control
> experiment. **Splitting is exhausted; every Critical was a SCOPE-BOUNDARY failure.**
> **Owner decision: strip to the minimum cap.** Four mechanisms are **DELETED**: the mount-time
> construction error, the instrumentation, the fiber WARN, and the `*int64` tri-state.
> `docs/plans/sweep-evidence/audit4-0186-adjudication.md`.
> ▶ **ROUND 7 FOLDED. Owner decision 2026-08-21: IMPLEMENT under TDD.** Seven audits, 56–65
> findings every round, scope down twelve-fold — the finding rate is a property of the process, not
> the bundle. Round 7's Criticals were facts about `bind.go`, `seam.go`, `parity_test.go` and
> `observability.go` that only running the code surfaces. **The 57 findings are the test list.**
> `docs/plans/sweep-evidence/audit5-0186-adjudication.md`.

## ▶ Progress

- **Branch:** `design/authz-security-b3` (docs-only). ⚠ Do not quote its SHA — amended each revision.
- **State:** ⭐ **IMPLEMENTED 2026-08-21 — phases 1–4 complete, all gates green.** Awaiting the
  **Delivery Gate**, which is owner-invoked (`/code-review`, `/security-review`).
- **Verification (run 2026-08-21):** `go test -race ./...` **EXIT=0** · `golangci-lint run ./...`
  **0 issues** (v2.12.2, repo-wide) · `go vet ./...` **EXIT=0** · `go build ./examples/...`
  **EXIT=0** · Docker probed and available, so the coverage run is the full one.
  **Touched-package coverage: `httpcore` 94.7 %, `stdlib` 94.8 %, `gin` 87.8 %, `fiber` 86.6 %** —
  all above the 85 % floor. ⚠ The **repo-wide** total is **75.1 %**, which is **pre-existing**:
  `main` measures 75.1 % too (verified by checking out `main` and re-running). Not a regression.
- **`/code-review` (Delivery Gate) — 4 findings, 2 MEDIUM + 2 LOW, ALL FIXED.** Two were genuine
  regressions this delivery introduced and had *documented* rather than *mitigated*: the slowloris
  hold (now `BodyReadTimeout`, 30 s) and a gin/stdlib 400-vs-201 divergence. One review finding's
  stated **mechanism was refuted** while its conclusion held. ⚠ **`/security-review` is still
  OWNER-INVOKED and has not run.**
- **New backlog item:** on `fiber`, a compressed body under the cap is decompressed **twice**.
- **Implementation corrected the design 18 times** — all folded into ADR-0186's
  "Implementation notes" section per rule #11, not left in the transcript.
- **Lineage — SIX failed audits.** ⚠ The adjudication records are authoritative; ADR banners are a
  chronology, not a spec.
  1. B3 (12 items) → 58 findings, 12 Critical, on *individual decisions*. `audit-b3-adjudication.md`
  2. Revised → 38, ~13, on the **interactions**. `reaudit-b3-adjudication.md`
  3. ADR-0186, 6 decisions → 63, 33, on the **PLAN**. `audit-0186-adjudication.md`
  4. Folded → 56, 28, on **six DECISIONS**. `reaudit-0186-adjudication.md`
  5. **Re-cut to 3 decisions → 65, 20**, with **12 of 20 in one decision**.
     `audit3-0186-adjudication.md` — splitting fixed the interaction grid and nothing else.
  6. **One decision → 61, 24.** `audit4-0186-adjudication.md` — ⭐⭐ **read this one: it failed at a
     bundle size of ONE, so size is no longer the variable, and every Critical was a boundary the
     bundle asserted and never derived.**
- **What the STRIP changed** (from round 6's 24 Criticals):
  1. ⭐⭐ **Cap the READ, leave each adapter's PARSE untouched.** "Unmarshal from the buffer" was
     ambiguous and the two readings disagreed on **under-cap** trailing bytes (3 lenses, executed:
     stdlib 200→400, gin 200→200). Feeding the buffer to *today's* decoder caps the whole request
     and changes nothing under the cap. Evidence §8.1.
  2. ⭐⭐ **`MaxBodyBytes` is a plain `int64`, `<= 0` disables** — `ResolveConfig` defaults in the
     struct literal so an explicit `0` survives, and `action/httpcall` **already ships this exact
     convention**. Evidence §8.2.
  3. **Keep `errors.As(*MaxBytesError)`** as the oversize discriminator — "any read error" would
     ship every aborted upload as 413 (executed).
  4. **DELETED**: mount-time construction error (no return channel, 4 lenses), instrumentation (no
     home, 4 lenses), fiber mount WARN (fires 5× per adapter; never for the admin group).
  5. **Stated, not built**: the consumer owns `ReadTimeout` (read-to-EOF hangs on an unterminated
     chunked body); peak memory is cap × in-flight; a 413 has no correlation id.
  ⚠ **Two mechanisms already existed in this repo and were missed for five rounds** —
  `cursorcodec.go` (trailing data) and `httpcall.go` (the cap convention).
- **Evidence:** `docs/specs/2026-08-21-adr-0186-premise-evidence.md` (§6 is the re-cut's own;
  **§4.4 is marked superseded**).

---

## 0. What the audit must attack

⚠⚠ **Round 6's verdict shapes this list.** Every one of its 24 Criticals was a **boundary the
bundle asserted and never derived**, and the execution lens named the bias:
*"the bundle's probes are narrow in a consistent direction — toward the fixture that demonstrates
the fix."* **Execution catches a false premise; it does not catch a narrow fixture, because the
probe passes.** Attack the fixtures, not only the claims.

1. ⭐⭐ **The central mechanism — cap the READ, keep the PARSE — is one round old.** Evidence §8.1
   shows over-cap rejected in every shape and under-cap unchanged. **Widen it.** Fixtures to add:
   exactly at the cap; cap+1; empty; absent; whitespace-only; truncated; `Content-Length` declared
   wrong in both directions; chunked with no `Content-Length`; two JSON values with whitespace
   between; valid JSON that is not an object. On **stdlib and gin**.
2. ⭐⭐ **The one `ASSUMPTION (unverified)` that matters: gin's `ShouldBindJSON` + validator through
   a reassigned `gc.Request.Body`.** §8.1 executed only the *decoder* half and says so.
   **Discharge it or refute it** — and note that inferring the binder from the decoder is the
   §6.3a mistake in miniature.
3. ⭐ **The `int64` / `<= 0` convention.** Is `ResolveConfig`'s struct-literal defaulting really
   safe for an explicit `0` (Evidence §8.2 reads it from source but does not run it end to end)?
   Does `WithMaxBodyBytes(0)` reach a decode site as "no wrapper"? Does any existing
   `CustomizeConfig` literal in `examples/`, tests or the parity suite change meaning?
4. ⭐ **`errors.As(*MaxBytesError)` as the discriminator.** Does it hold through gin's binder and
   fiber's pre-check? Is there an oversize path that does **not** produce it? Is there a
   **non**-oversize path that **does**?
5. ⭐ **The three discarding sites** (`stdlib:238`, `gin:265`, `fiber:255`, the optional-body admin
   `ResolveIncident` route). Absent / empty / truncated / oversize / oversize-and-malformed.
   ⚠ ADR-0095 keeps admin routes out of `Mount`, so parity cannot be the net.
6. ⭐⭐ **The DELETIONS are decisions and must be attacked as such.** No instrumentation, no
   mount-time WARN, no mount-time validation. For each: **is the stated consequence honest, and is
   it the whole consequence?** Particularly *"a consumer cannot measure before the cap bites"* —
   is `WithMaxBodyBytes(0)` really a sufficient migration lever with no metric at all?
7. ⭐⭐ **The residuals — time, memory, and the missing 413 metadata.** Read-to-EOF hangs on an
   unterminated chunked body; peak memory is cap × in-flight; a 413 has no correlation id and no
   log. All three are *stated, not fixed*. **Attack whether stating them is defensible** or whether
   one of them makes the delivery net-negative.
8. ⚠ **One lens must be the COUNTING lens** (rule #9). Six rounds, and **the arithmetic was right
   every time** — the failure was the grep's **NET**, the citation's **ANCHOR**, and now the
   **SCOPE**. ⚠⚠ **Spec §5 lists the boundaries this delivery derived from source. Re-derive every
   one of them independently**, and look for a boundary it did not think to list.
9. ⚠ **One lens must do the INTERACTION pass** (rule #9), over **spec §4, now rebuilt as 14
   COUPLINGS rather than 5 removals** — the previous table was judged *"right to exist, wrong to be
   closed; it counts removals, not couplings"*. ⚠ **Five rows are marked "stated, NOT resolved" on
   purpose. Attack whether that is honest or evasive**, and check that every ✅ names a file and
   section that actually contains the resolution — the previous version had one discharging onto
   ADR text that did not exist.
10. ⚠ **Attack the evidence file.** §6 and §8 are the author's own — **inputs**, not conclusions.
    ⚠⚠ **Read §6.3a FIRST**: a probe that PASSED and was WRONG.
11. ⚠ **Every auditor gets the step-0 worktree check**: verify the bundle is present, STOP if not.
    Worktrees **detached at the bundle commit**. The bundle is **five** files.
12. ⚠ **Do not audit the five DEFERRED deliveries** on their merits. **Do** audit the boundary.

---

## 1. Fan-out rules

- **Fan out by Go package.** Concurrent agents in one package break each other's compile.
- ⭐⭐ **BEFORE writing any new symbol, search the repo for an existing convention.** Round 6 found
  **two** mechanisms already in this repo that five rounds of design missed —
  `runtime/kernel/cursorcodec.go:50-58` (trailing data after a JSON value, ADR-0160) and
  `action/httpcall.go:186-194,214` (the body cap itself: `int64`, `<= 0` disables, constructor
  default). **This step is now mandatory and is why the config shape changed.**
- **No Docker.** Every package here is container-free:
  `transport/http/{httpcore,stdlib,gin,fiber,parity}`.
- **`golangci-lint`:** probe `command -v golangci-lint` and run it; if absent, say so and offer
  install-or-skip. **Never substitute `go vet`.** ⚠ Round 6 showed `go build ./examples/...`
  **exits 0** on an unchecked error return — only errcheck catches it.
- ⚠ **A mutation ablation gets its own `git worktree`.**
- ⚠ **`go build ./examples/...` runs at the end of phase 2**, not phase 1.

---

## 2. ⚠ Decision → phase → package map

**Every sentence of the ADR's Decision section has a row. A row with no phase is a defect** — six of
one round's fifteen Criticals were that omission, and round 6 found four mechanisms whose home
package could not host them at all.

| ADR sentence | phase | package |
|---|---|---|
| `MaxBodyBytes int64` + `WithMaxBodyBytes`, default 1 MiB in the `ResolveConfig` literal | 1 | `httpcore` |
| `n <= 0` disables (matching `action/httpcall`) | 1 | `httpcore` |
| `ErrRequestBodyTooLarge` sentinel | 1 | `httpcore` |
| ⭐ the 413 arm's **`ErrorBody`** — `{request_too_large, "request body exceeds the configured limit"}`, static, does not name the limit | 1 | `httpcore` |
| 413 arm **before** the 400 arm + ordering comment | 1 | `httpcore` |
| `httpcall.ErrBodyTooLarge` still classifies 500 (test) | 1 | `httpcore` |
| ⭐ **non-generic `WithMaxBodyBytes` alias per adapter** (the generic form cannot infer `R`) | 2 | `stdlib` \| `gin` \| `fiber` |
| cap the **read**, then run the site's **existing** decoder over the buffer (36 sites) | 2 | `stdlib` \| `gin` \| `fiber` |
| `n <= 0` ⇒ **do not install the wrapper**; decode from the wire as today | 2 | `stdlib` \| `gin` |
| oversize identified by `errors.As(*MaxBytesError)`, **not** by "a read error" | 2 | `stdlib` \| `gin` |
| oversize path at the **3 discarding** sites | 2 | `stdlib` \| `gin` \| `fiber` |
| fiber pre-checks **both** `len(c.BodyRaw())` and `len(c.Body())` | 2 | `fiber` |
| parity: all three agree on 413 for every over-cap shape, and are unchanged under the cap | 3 | `parity` |
| `SECURITY.md` residuals (ReadTimeout, peak memory, no 413 metadata, `MountGroups`, fiber divergence) | 4 | docs |
| `CHANGELOG` / `STABILITY` — two wire breaks | 4 | docs |

⚠ **Nothing in this delivery lands in `engine`, `runtime`, `service`, `persistence` or
`internal/`.** Round 6's analogue of this row was wrong — a package produced four sentinels and
appeared in no list — so it is stated explicitly and the audit should check it.

**Phase table:**

| # | package(s) | depends on | fan-out |
|---|---|---|---|
| 1 | `transport/http/httpcore` | — | 1 agent |
| 2 | `transport/http/stdlib` \| `gin` \| `fiber` | 1 | **3 agents in parallel** |
| 3 | `transport/http/parity` | 2 | 1 agent |
| 4 | docs | 3 | controller |

⚠ **Phase 1 must precede phase 2** — the adapters cannot reference a sentinel or config field that
does not exist.

---

## 3. Phases

### Phase 1 — `transport/http/httpcore`: the config, the sentinel, the status

**Symbols:** `CustomizeConfig.MaxBodyBytes int64`; `WithMaxBodyBytes(n int64) CustomizeOption[R]`;
`ErrRequestBodyTooLarge`.

⚠ **The default (1 MiB) goes in `ResolveConfig`'s struct literal**, beside `Logger` and `Wrap` —
**not** in a post-loop guard. An `int64` has no nil, so nothing clobbers an explicit `0`.
⚠ **`n <= 0` disables.** This mirrors `action/httpcall.WithMaxResponseSize` deliberately; do not
invent a different sentinel. ⚠ **No pointer, no mount-time validation.**

**Tests, and what makes each fail today:**

1. `TestOversizedBodyClassifiesAs413NotBadRequest`.
   **Fails today:** the sentinel does not exist → compile error.
   ⚠ **Must include a row where the error wraps BOTH `ErrBadInput` and the new sentinel, asserting
   413** — executed, that combination classifies **400** today. Without it the test passes against
   the ordering bug. ⚠ **Plus a row asserting `httpcall.ErrBodyTooLarge` still classifies 500.**
2. ⭐ `TestMaxBodyBytesDefaultAndDisable` — a table: **unset → 1 MiB**, **`0` → disabled**,
   **negative → disabled**, **`n` → n**.
   **Fails today:** the field does not exist.
   **Falsifier, stated:** *the `0` row fails against an implementation that passes the configured
   value to `MaxBytesReader`* — executed, `MaxBytesReader(w, body, 0)` rejects every non-empty body.
   ⚠ **Assert on the resolved config value AND on whether a wrapper would be installed** — asserting
   only the number cannot distinguish "0 stored" from "0 honoured".

**Verify:** `go test -race -count=1 ./transport/http/httpcore/...`

---

### Phase 2 — `transport/http/{stdlib,gin,fiber}`  ⚠ THREE PARALLEL AGENTS

One agent per package. **Never two agents in one package.**

- `stdlib` / `gin`, **capped**: `io.ReadAll(http.MaxBytesReader(w, body, n))`, then run **the site's
  existing decode idiom over the buffer** — `json.NewDecoder(bytes.NewReader(buf))` for stdlib;
  for gin reassign `gc.Request.Body` to the buffer, then `ShouldBindJSON` unchanged.
  ⚠⚠ **Do NOT substitute `json.Unmarshal`** — it is strict about trailing data where the adapters
  are lenient, and swapping it is a wire break on every under-cap trailing-byte body (3 lenses).
- `stdlib` / `gin`, **`n <= 0`**: do not install the wrapper; decode from the wire exactly as today.
- `fiber`: **TWO** pre-checks before `c.Bind().JSON`, and **both** are required —
  `len(c.BodyRaw())` (wire) **and** `len(c.Body())` (decompressed, which is what `bind.go:309`
  actually parses).
  ⚠⚠ **`BodyRaw()` alone leaves the cap BYPASSED by `Content-Encoding: gzip`** — measured, a
  3,121-byte request parsed 3,145,761 bytes and returned **200**. ⚠ And `Body()` alone fails the
  amplification case the other way (`len == 33` when fiber's own gunzip limit wrote an error
  string). **Neither alone is sufficient.**
  ⚠⚠ **`n <= 0` disables on fiber too** — an earlier revision named only stdlib and gin, and the
  obvious fiber pre-check with cap 0 returns **413 on an ordinary 1 MiB body** (executed).
- ⚠⚠ **Oversize is `errors.As(err, new(*http.MaxBytesError))`, never "the read returned an error".**
  Executed: an over-declared `Content-Length` yields `unexpected EOF` with `errors.As` **false**.
- **3 discarding sites**: same `errors.As` check → bare `ErrRequestBodyTooLarge` → 413; every other
  decode error stays ignored.
- ⚠ **No histogram, no counter, no mount WARN.** Deleted by decision — do not add them back.

**Tests per package:**

1. ⭐⭐ `TestOverCapIsRejectedInEveryBodyShape` — well-formed oversize; oversize with a syntax error;
   **a complete JSON value followed by over-cap trailing bytes**.
   **Fails today:** executed — the third returns `err == nil` and **2xx** on stdlib/gin.
   ⚠ **Falsifier:** *row 3 fails against any implementation that caps during the parse.*
2. ⭐⭐ `TestUnderCapBehaviourIsUnchanged` — **the control that decides the whole mechanism**:
   a clean body, and **a complete JSON value plus UNDER-cap trailing bytes**, must behave exactly as
   they do today on this adapter.
   ⚠ **Falsifier:** *the trailing-byte row fails against an implementation that substitutes
   `json.Unmarshal`* — executed, stdlib would go 200→400 and gin would not, which is the
   divergence three lenses found. ⚠ **Record today's behaviour first and assert against it**, so
   the test cannot be written to match whatever the new code does.
3. ⭐ `TestAbortedUploadIsNotA413` — `Content-Length` over-declared, connection ends early.
   **Falsifier:** *it fails against an implementation that treats any read error as oversize.*
4. ⭐ `TestOversizedBodyReturns413OnTheOptionalBodyAdminRoute` — **names the resolve-incident route.**
   **Falsifier:** *it fails against an implementation that only edits the 12 wrapping sites.*
   ⚠⚠ **CORRECTED (round 7, C3):** the old claim *"parity structurally cannot see admin routes"*
   is **FALSE** — `parity_test.go:663,670,677` mounts `AdminRoutes` by hand on all three adapters
   today. **Phase 3 SHOULD carry a three-adapter parity case over this route**; it is the cheapest
   correct net and the plan previously forbade it on a false premise.
5. `TestBodyAbsentOnTheOptionalRouteStillSucceeds` — the control for test 4.
6. ⭐ `TestDisabledCapDoesNotInstallTheReader` — `WithMaxBodyBytes(0)`, a 3 MiB body succeeds.
   **Falsifier:** *it fails against an implementation that passes `0` to `MaxBytesReader`.*
7. **gin only:** ⭐ `TestBinderSurvivesTheBufferSwap` — a table of body shapes (valid, malformed,
   type-mismatch, trailing bytes, empty) producing **byte-identical** errors through the reassigned
   body vs today's path.
   ⚠⚠ **CORRECTED (round 7, E3):** the previous version asserted gin's **validator**, which
   **cannot be written** — the repo has **zero `binding:` tags**, so gin's validator is never
   engaged and the test could not fail. ⚠ A lens has already discharged the binder half across
   **14 fixtures, byte-for-byte identical** — this test pins it rather than discovering it.
8. **fiber only:** ⭐⭐ `TestBothWireAndDecompressedAreBounded` — **the test that decides the fiber
   mechanism.**
   | fixture, 1 MiB cap | expected | what it falsifies |
   |---|---|---|
   | gzip, wire ~3 KiB, **decompressed 3 MiB** | **413** | *a `BodyRaw()`-only check* — measured: parses 3,145,761 bytes, returns **200** |
   | plain, wire 2 MiB | **413** | a `Body()`-only check that mis-reads fiber's gunzip error string |
   | plain, wire 100 B | 200 | over-refusal |
   ⚠⚠ **CORRECTED (round 7, E6):** the previous version's falsifier was **inverted in the very
   sentence claiming an earlier revision had it backwards**, and its stated rule *"wire-large,
   decompressed-small"* names a fixture **gzip cannot produce**.

**Verify (per agent):** `go test -race -count=1 ./transport/http/<pkg>/...`
**Then, once all three land:** `go build ./examples/...`

---

### Phase 3 — `transport/http/parity`

All three adapters agree on **413** for every over-cap shape, and are **unchanged** under the cap.

⚠ **Pin the fixture below 4 MiB and say why in a comment:** above `fiber.Config.BodyLimit` the
fiber route group is never reached and the client gets fasthttp's `text/plain` with no `ErrorBody`.
Add a separate, **explicitly-labelled fiber-only case** documenting that divergence.
⚠ **Do NOT add a compressed-body parity case asserting identical status** — executed in round 6, a
gzip request is **2xx on fiber, 400 on stdlib/gin** *today*, so such a case cannot pass and is not
this delivery's to fix. Assert wire-byte *capping* per adapter instead.
⚠⚠ **Parity CAN cover the optional-body admin route** — `parity_test.go:663,670,677` already mounts
`AdminRoutes` by hand on all three adapters. **Add that case here** (round 7 C3 refuted the
"structurally cannot" premise asserted in four places).
⚠ **Check `TestParity_ErrorEnvelopes`'s byte-for-byte guarantee still holds and say so** — this
delivery adds no correlation id, but the deferred 4xx delivery will break it.

**Verify:** `go test -race -count=1 ./transport/http/parity/...`, then `./transport/http/...`

---

### Phase 4 — documents (controller)

⚠ **Write phase pointers LAST.** One round found 7 of 9 wrong.

- **`SECURITY.md`** — the cap, its default, and `WithMaxBodyBytes(0)` to disable. Plus **five
  residuals, each stated plainly**:
  1. **The consumer owns `ReadTimeout`** — reading to EOF means an unterminated chunked body holds
     the handler indefinitely; all three `examples/` set `ReadHeaderTimeout` but not `ReadTimeout`.
  2. **Peak memory is `MaxBodyBytes × in-flight requests`**; nothing here bounds concurrency.
  3. **A 413 carries no correlation id and writes no log record.**
  4. **`MountGroups` consumers get the default** — and the escape is `Customize` directly with
     options, which `seam.go`'s own godoc already documents.
  5. **Fiber above `fiber.Config.BodyLimit`** rejects before the group is reached: framework
     plain-text 413, no `ErrorBody`, no log, no warning.
  ⚠ **Do NOT write an at-rest paragraph** — that delivery is deferred.
- **`CHANGELOG.md` + `STABILITY.md`** — **two wire breaks**: (i) a new **413** on routes that
  previously returned 400, 500 or a spurious **2xx**; (ii) requests succeeding today via the
  trailing-byte gap now fail. ⚠ **No source break**: `ClassifyError` keeps its signature and no
  exported function changes shape.
- Close backlog **98**. ⚠ **Do NOT close 104, 100, 101, 54, 65 or 99.**
- **Open:** body-size observability once an instrument can be built outside `httpcore`; a mount-time
  diagnostic channel; the fiber decompressed-size bound.
- `docs/plans/HANDOVER.md` + this plan's `▶ Progress`, per rule #10.

---

## 4. Enumerations, re-derived at the anchor

⚠ **Bare `|` under `-E`** — `\|` in ERE is a *literal* pipe. An earlier revision's "0 existing caps"
evidence was a command returning 0 for **any** repository, and ⚠⚠ **a later revision reintroduced
the same broken pattern in the row below the warning about it.** The corrected form is below.

| what | value |
|---|---|
| decode sites | **39** — `stdlib` 13 `json.NewDecoder`, `gin` 13 `ShouldBindJSON`, `fiber` 13 `c.Bind().JSON`, `httpcore` **0**; all in each package's `groups.go`. ⚠ Re-derived by **AST walk**, not grep |
| …**propagating** the decode error | **36** |
| …**discarding** it to `_` | **3** — `stdlib:238`, `gin:265`, `fiber:255`, the optional-body `ResolveIncident` route |
| …already capped by us | **0** — `grep -rnE 'MaxBytesReader|BodyLimit|MaxBytesHandler|LimitReader' transport/` exits 1 |
| `ClassifyError` arms | **6**, ordered: 404 `:28`, 403 `:32`, 409 `:34`, 400 `:36-50`, 422 `:51`, default 500 `:57` — becoming **7** with 413 inserted before 400 |
| body shapes that must all yield 413 | **3** — well-formed oversize, oversize-with-syntax-error, complete-value-plus-trailing-bytes. ⚠ Today they yield 413 / **400** / **2xx** on stdlib+gin |
| `MaxBytesReader` limits that reject everything | **2** — `0` and negative. Executed |
| packages this delivery touches | **4** — `httpcore`, `stdlib`, `gin`, `fiber` (+`parity` for tests, +docs) |
| routes | **26** = 9 non-admin + 15 admin + 2 health |

⚠⚠ **Six rounds, and the arithmetic was right every time** — the failure was the grep's **NET**,
the citation's **ANCHOR**, and then the **SCOPE**. ⚠⚠⚠ **Round 6 was entirely scope**: a package
that could not host an instrument, a function set that could not carry an error, a config sentinel,
and two existing conventions nobody looked for. **The bottom five rows above are boundaries derived
from source for exactly that reason. Assume one is still wrong.**

---

## 5. Verification checklist

- [ ] **Rule-#9 audit** — lenses including a **counting** lens and an **interaction** lens, detached
      worktrees at the bundle commit, step-0 presence check over **five** files, **append per
      finding**. **Nothing below starts until this is checked.**
- [ ] Every phase's tests observed **RED before GREEN**, in the transcript.
- [ ] Every prescribed **falsifier** demonstrated by mutation — break the production line, observe
      RED, restore from a `cp` backup (⚠ **never `git checkout <path>`**), `diff`.
      The ones that matter most: phase 2 test 2 (**under-cap behaviour unchanged** — the control
      that decides the mechanism), phase 2 test 1 row 3 (over-cap trailing bytes), phase 2 test 3
      (aborted upload is not a 413), phase 2 test 6 (the `0` opt-out), phase 2 test 8 row 2 (wire
      vs decompressed).
- [ ] `go test -race -coverprofile=cover.out ./... && scripts/coverage.sh cover.out` ≥ 85 % over
      hand-written code, hot paths and failure branches first. Probe `docker info`; if down, say so
      and label any container-free subset as the partial result it is.
- [ ] `go test ./...` from the repo root — no regressions.
- [ ] `golangci-lint run ./...` **repo-wide** clean.
- [ ] `go vet ./...`
- [ ] `go build ./examples/...` — at the end of phase 2 and again here.
- [ ] Documents describe what shipped; per rule #11 expect implementation to correct the design and
      **amend the ADR in the same bundle**, with the measurement.
- [ ] Sweep the diff's comments for unexecuted claims and over-reaching quantifiers.
- [ ] `/code-review` — all findings fixed, folded via `--amend`.
- [ ] `/security-review` — all findings fixed, folded via `--amend`.
- [ ] `HANDOVER.md` rewritten in place; `▶ Progress` updated; memory topic file written and pointing
      at `HANDOVER.md`.

## 6. Commit shape

One feature bundle, one commit, amended (never stacked):

```
feat(transport): cap request bodies before parsing them
```

carrying implementation, tests, the spec, ADR-0186, this plan, the evidence file, the carry-forward
record and the phase-4 docs.

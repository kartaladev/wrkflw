# Spec — untrusted input and disclosure posture (backlog 54 partially, 65, 98, 99, 104; posture for 100, 101)

> ## ⚠ REVISED 2026-08-21 after its first standalone audit. NOT YET RE-AUDITED.
>
> Audit #1 (execution / failure-modes / counting / **interaction**): **63 findings, 33
> Critical**, of which three lenses independently judged the *decisions* largely sound and
> the **plan** the point of failure. This revision folds it.
> ⭐ **The central change: the element bound moved from EVALUATION to ADMISSION**, which
> closed seven findings at once and left `internal/expreval`, `runtime` and `engine`
> untouched by this delivery.
> Adjudication: `docs/plans/sweep-evidence/audit-0186-adjudication.md`.
> ⚠ **A bundle whose decisions changed has not been audited.** Not an input to implementation.

- Date: 2026-08-21
- **Anchor:** this bundle's own commit on `design/authz-security-b3`.
  ⚠ Citations into `transport/http/**`, `runtime/**`, `service/**` and `action/**` name a
  **symbol**. Line numbers appear only where exact ordering is load-bearing —
  `httpcore/errors.go` (the arms are order-dependent), the three decode sites that discard,
  and the SQL migrations. The previous revision claimed *"every citation was re-derived
  here"* and **four were off by one**, one of them truncating the sentence it was cited for;
  that quantifier is withdrawn rather than re-asserted.
- Bundle: this spec + `docs/adr/0186-untrusted-input-and-disclosure-posture.md`
  + `docs/plans/2026-08-21-untrusted-input-and-disclosure.md`
  + **`docs/specs/2026-08-21-adr-0186-premise-evidence.md`** — this revision's own executed
    evidence, written before the re-audit.
- Inherited evidence: `docs/specs/2026-08-21-adr-0185-0186-premise-evidence.md`.
  ⚠ **Four of its eight sections are known-defective — §2, §5, §6, §7** — see §6 below.
  §1, §3 and §4 hold and were re-confirmed, and they serve the **deferred backlog-103
  delivery**, not this one. §8 is its assumption list, superseded by §7 here.
  ⚠⚠ **§6 in particular must not be cited for Decision 5**: its probe bypassed
  `runtime/validation.Gate`, which is the whole reason D5's rendering moved into that package.
  **Nothing in this delivery depends on that file.**

## 0. Why this is its own delivery

This was half of the **B3 bundle** — a twelve-item authorization-and-security design that
**failed two rule-#9 audits**. The first (58 findings, 12 Critical) killed individual
decisions; the revision fixed all of them and the second (38 findings, ~13 Critical) killed
the **interactions** between the four decisions that revision rewrote. Owner decision,
2026-08-21: re-cut into three deliveries.

This is the first, and it is genuinely separable — the B3 spec's own §5 said *"0186's
decisions hold whether or not 0185 ships, and vice versa"*, and an audit lens confirmed by
targeted grep that **no ADR-0185 symbol appears anywhere in this bundle**. ⚠ **One real
dependency existed and has been severed**: the draft added **401** (nothing authenticated the
caller) and **503** (the identity resolver errored) arms to the error classifier, both of
which belong to the identity delivery. They are removed.

The other two deliveries, for a reader who needs the map:
- **ADR-0185-core** — the actor travels in `context.Context` rather than the request body;
  constructing an engine without an authorizer is an error; an eligibility spec that states
  nothing denies. Backlog 51/52/53.
- **Deferred** — backlog 103 (deny-list attribute predicates allow when the process variable
  is missing) and backlog 124 (task completion never checks who claimed). Each needs a design
  increment that does not exist yet.

## 1. Problem

`wrkflw` is a library a consumer mounts into their own HTTP server. On the way **in** it
accepts unbounded input; on the way **out** it discloses more than it should.

| | today | item |
|---|---|---|
| request bodies | **no cap** at any of the 39 decode sites we own — and **3 of the 39 cannot even report one**, because they discard the decode error | **98** |
| variable payloads | no bound in bytes **or** element count at any admission point | **99** |
| expression evaluation | unbounded in **caller-supplied input size** — O(n²) measured, n = 10 000 → **2.458 s** | **99** |
| `httpcall` with an expression-derived URL | an **SSRF primitive**: no allowlist, no `CheckRedirect` | **65** |
| the instance read path | variables **aliased not copied** on **11** paths, and **no redaction hook** anywhere | **54** (partially — see §4) |
| 4xx error bodies | 403 echoes the **predicate source**; 400 echoes the **submitted value** | **104** |
| at rest | **12 plaintext columns across 7 tables**, no integrity chain | **100/101** — posture only |

These do **not** compose into a chain the way the identity defects do; each is independently
exploitable and independently fixable. That is precisely why this delivery can go first.

## 2. Verified current behaviour

Everything here was **executed**, or read from source at the anchor. What could not be
executed is labelled `ASSUMPTION (unverified)` in §7.

The six findings are stated in full in **ADR-0186 §Context** and are not duplicated here. What
this section adds is the **corrections history** — what earlier drafts got wrong, so a reader
does not re-derive a refuted claim. ⚠ Rows marked **[A1]** were refuted by audit #1 and are
this revision's own corrections; the rest predate it.

| claim | status |
|---|---|
| *"`expr.MaxNodes` should be set to bound cost"* | ⚠ **INVERTED.** `MaxNodes(0)` *disables* the check; never calling it leaves `DefaultMaxNodes = 1e4` **active**. Executed, and stated outright in the vendor godoc. **Do not implement it.** |
| *"the ABAC and gateway evaluators are one surface"* | ⚠ **TWO surfaces.** `authz`'s is `expreval.New()` (5 s `DefaultTimeout` enabled); only the engine's gateway evaluator is wall-clock unbounded, deliberately, per ADR-0003/0049/0056. |
| **[A1]** *"…and a `runtime` option can bound them"* | ⚠ **It bounds ONE.** Neither ABAC evaluator has an options seam — `authz/authz.go:23` is a package global, `internal/authz/casbin/authorizer.go:30` is hard-coded in a constructor. A decisive input to the admission move. |
| *"fiber decodes with `BodyParser`"* | ⚠ **Name rotted.** Zero hits repo-wide; the live idiom is `c.Bind().JSON`. The count of 13 was right. |
| *"the caret line reprints the predicate source a third time"* | ⚠ **FALSE.** It is dots and a `^`. The source appears exactly **twice**. |
| *"the probe predicate is 44 characters"* | ⚠ **80 bytes** — and ⚠ **[A1] the predicate text itself was never recorded**, which makes *"it is 80 characters"* unfalsifiable as written and made the plan's "re-measure the ladder" item unexecutable as briefed. Two audit lenses re-measured the ladder anyway and it reproduces; the predicate must be quoted in the implementation's benchmark. |
| *"mutating the returned view mutates instance state"* | ⚠ **[A1] WITHDRAWN, and the withdrawal was HALF WRONG.** Executed: `copyVars` is `maps.Clone`, so the claim is **false for top-level values and TRUE for nested ones** — deleting `vars["applicant"]["ssn"]` from the clone deletes it from the source. `caching_instance_store.go:72`'s *"deep-copies"* godoc is a false comment in shipped code. Evidence §3. |
| *"a `ctx` on `ConditionEvaluator` bounds evaluation cost"* | ⚠ **DROPPED.** Justified against the *import* rule `purity_test.go` enforces, not the **deterministic-replay** invariant `engine/conditions.go:29-43` locks. Honouring it forces the engine default onto the goroutine path: **99.43 → 965.2 ns/op**, reproduced independently three times (97.62 → 976.7; 101.9 → 932.6; 97.50 → 917.1). |
| *"256 KiB of variables is the CPU mitigation"* | ⚠ **REFUTED by this bundle's own table** — 256 KiB admits ~40–50 k elements (measured exactly: **45 540** at the defaults) ⇒ ~45–60 s of CPU. It is a **payload** bound; the CPU bound is element cardinality. |
| *"5 000 elements ≈ 40 ms, 10 000 ≈ 150 ms"* (proposed by audit #1 of B3) | ⚠ **WRONG by ~15×.** Re-derived: **5 000 ≈ 610 ms, 10 000 ≈ 2.442 s** — and now **measured**: 2.458 s (0.65 % error) and 1.92 s on a faster run. |
| *"the value-free 400 rendering happens in `ClassifyError`"* | ⚠ **NOT IMPLEMENTABLE there.** `gate.go:45` is `%w: %s`, so the typed error is a **string** by the time the transport sees it — `errors.As` `true` before the gate, `false` after (reproduced twice). |
| **[A1]** *"…and rendering `InstanceLocation` + the keyword is value-free"* | ⚠ **FALSE — the replacement leaked too.** `InstanceLocation` is *instance*-derived: a card number submitted as an object **key** renders as `at '/4111-1111-1111-1111': violates type`. Executed twice. The vendor's `Error.String()` leaks both the value **and** lengths. **`keywordLocation` is the value-free column**, executed against the real in-repo strategy. Evidence §2. |
| **[A1]** *"blanking the 400 arm is safe"* | ⚠ **It destroys three ADRs.** `errors.go:38-49` carries in-code ADR-0146/0152/0183 rationale demanding those messages stay actionable; four of seven non-validation sentinels echo **no** caller value; and `ErrBadInput` — every DTO on all 26 routes — is **executed value-free**, not even leaking a length. Evidence §1. |
| *"oversize bodies return 413"* | ⚠ **They would return 400.** Executed: an error wrapping both `ErrBadInput` and the new sentinel classifies **400**, because `ClassifyError` is an ordered switch with the 400 arm at `:36-50`. |
| **[A1]** *"all 39 decode sites already wrap in `ErrBadInput`"* | ⚠ **36 do; 3 DISCARD the error to `_`** (`stdlib:238`, `gin:265`, `fiber:255` — the optional-body admin resolve-incident route). At those three the cap ships **silently unenforced**, returning 2xx. The three need the *opposite* instruction: add an error path where none exists. Evidence §4.1. |
| **[A1]** *"a `len(c.Body())` pre-check caps fiber"* | ⚠ **It returns 400, not 413, on the amplification case.** `c.Body()` **decompresses**; a 63.7 KiB gzip expanding to 64 MiB yields `len == 33`. Use **`c.BodyRaw()`**, and `MaxBodyBytes` means **wire bytes** in all three adapters. |
| **[A1]** *"`ErrBodyTooLarge` is a new sentinel"* | ⚠ **`action/httpcall.ErrBodyTooLarge` already exists** (`httpcall.go:94`), means an outbound **response** exceeded 10 MiB, and is a **500**. Renamed to `httpcore.ErrRequestBodyTooLarge`. |
| **[A1]** *"gin wraps the `MaxBytesError` again — three adapters, three shapes"* | ⚠ **FALSE.** Executed: stdlib and gin both surface the **bare `*http.MaxBytesError`**. **Two shapes, not three.** |
| *"redaction in `mapInstance` cannot be bypassed"* | ⚠ **True of the mapper, false of the endpoints.** |
| **[A1]** *"…so the covered set is the 6 + 2 read paths"* | ⚠ **It is 11.** Three admin endpoints call `NewInstanceView` **directly** and take no mapper: `ResolveIncident`, `CancelInstance`, `ResolveCompensationStall`. `AdminListInstances` is clean — and it is the one admin endpoint of four that was checked before generalising. Evidence §4.2. |
| **[A1]** *"`GetActionableView` renders `HumanTask.Vars`"* | ⚠ **FALSE.** `ActionableTask` declares six fields and **no `Vars`**; `NewActionableView` never reads `t.Vars` and already clones. The prescribed `TestActionableViewRedactsTaskVars` **cannot be written** — it was billed as one of two "controls that decide D4's placement". What the route *does* disclose is `allowed_actions[].condition` (expression source) and `candidates[]`. Evidence §4.3. |
| **[A1]** *"the snapshot carries variables"* | ⚠ **Five disclosure-bearing fields, not one**: `variables`, `tokens[].payload`, `incidents[].error`, `tasks[]`, and the **whole embedded definition** (ADR-0144) — i.e. every condition expression source. A `func(map[string]any) map[string]any` hook reaches exactly the first. |
| **[A1]** *"the two plaintext columns"* | ⚠ **Twelve columns across seven tables, in three dialects.** An audit lens raised it to "at least six" and was itself short by three tables. Evidence §4.4. D6's deliverable *is* this enumeration. |
| **[A1]** *"three options writing one field is last-writer-wins, silently"* | ⚠ **NOT silent** — both existing godocs state it verbatim (`processdriver_options.go:196-197`, `:215-216`). Moot now: the admission move creates **no** third writer. |
| **[A1]** *"the bound is computed once per env"* | ⚠ **Unimplementable AND unnecessary.** No carrier exists at `ConditionEvaluator`; map-identity memoisation fails **open** (200 000 maps → 82 473 addresses, 59 % collided); and like-for-like the count is **~12–13× cheaper** than the ctx it refused. Moot now: admission counts once per request. |

## 3. Scope

**In:** 65, 98, 99, 104, the documented posture for 100/101, and **54 for `variables` only**
(aliasing + a redaction hook + the eleven paths). ⚠ The other four disclosure-bearing snapshot
fields and `ActionableView`'s two become **new backlog items** — see ADR-0186 D4.

**Out (named so the audit can check the boundary):** everything identity — 51, 52, 53, 103,
124, and the parked 102. Also 32 (snapshot versioning), 60/91 (outbox envelopes), 96
(transport parity suite), 106 (readiness aggregate).

⚠ **Newly named as out**, because audit #1 showed they were invisible rather than excluded:
- **`action/transform`** — a second unbounded expression surface over process variables
  (`transform.Do` receives `copyVars(s.Variables)` and runs `expr.Run` with no wrapper). The
  admission bound covers its *input*; the vendor-wrapper question is a backlog item.
- **Runtime variable growth** via `mergeVars` from action/task/message output. Deliberately
  unbounded here; it needs an incident-disposition design in `engine`.
- **A decompressed-size bound for fiber.** The ceiling is `app.config.BodyLimit`, which a
  mounted route group does not own.

## 4. Decisions

Stated in full in **ADR-0186**. In one line each:

1. **Request bodies are capped by default** — 1 MiB of **wire** bytes (`c.BodyRaw()` on
   fiber), `0` = unbounded; oversize is a **413** via a bare `httpcore.ErrRequestBodyTooLarge`,
   with the 413 arm placed **before** the 400 arm; and the **three discarding sites gain an
   error path** rather than a converted one.
2. ⭐ **Variable maps are bounded at ADMISSION, in bytes AND elements** —
   `service.WithMaxVariableBytes` (256 KiB) and `service.WithMaxVariableElements` (10 000),
   enforced together at the four caller-supplied request fields, refused with
   `service.ErrVariablesTooLarge` → 413. **`internal/expreval`, `runtime` and `engine` are not
   touched.**
3. **Expression-derived URLs are restricted by default; author-typed URLs are not** — an IP
   deny-list stated as *not global unicast* in `Dialer.Control`, a host allow-list on the URL
   and each redirect hop, `WithAllowedCIDRs` as the working escape hatch, and `WithHTTPClient`
   + `WithURLExpr` **refused** rather than silently resolved.
4. **Redaction runs at the `ProcessInstance` → response boundary** on **eleven** paths, with a
   **deep** copy, a scope-carrying hook signature, and a **named covered set**.
5. **403 says nothing; 400 renders a value-free SCHEMA location** — deny-by-default over the
   open set with an executed exception list that preserves ADR-0146/0152/0183; the correlation
   id is minted in `writeErr`; 4xx logging is widened **per class** so the fix does not
   relocate the leak to `slog.Default()`.
6. **At rest: the posture is documented over a DERIVED enumeration, the mechanism deferred.**

## 5. ⚠ Pairwise interactions between these decisions

Required by CLAUDE.md rule #9's interaction clause. ⚠⚠ **The previous revision's table was
wrong in five of its eight rows and omitted two pairs entirely** — the interaction lens
derived all 15 D×D pairs plus 8 cross-cutting ones. This table is rebuilt from that
derivation, and every row states the *resolution now in the ADR*, not a hope.

| pair | interaction | resolved? |
|---|---|---|
| **D1 × D2** | The byte cap admits **45 540** elements against an element cap of 10 000 — **4.55×** — so the window 10 001…45 540 was persisted then permanently unevaluable, with **no repair verb** (no route mutates variables; only `POST /admin/…/cancel` exits). "Two knobs, two jobs" was a relabelling that left the **looser gate first**. | ✅ **Both enforced at the same seam, at the same moment** — the window is closed and the two defaults no longer need to be mutually consistent to be safe. ⚠ **Scoped, not closure**: see the D2 × D2 row. |
| **D2 × itself** *(author's own interaction finding, this fold)* | Bounding the **merged** map would give "nothing persisted exceeds the bound" — and would convert the deliberately-unbounded runtime growth into an **unrecoverable wedge**: once an action's output pushes the map past 256 KiB, every later `CompleteTask` is refused **413 forever** and the task can never be completed. | ✅ The bound is on the **incoming caller-supplied map**, not the merged result. It cannot wedge (refusal is pre-persist, caller present, retry with less). ⚠ **The aggregate map is therefore NOT bounded**, and the earlier draft of this fold's *"nothing is ever persisted that cannot be evaluated"* is **withdrawn**. |
| **D4 × the read hot path** *(author's own interaction finding, this fold)* | D4's fix for the nested-mutation hazard is a recursive deep copy, on a path every instance read takes — an unmeasured cost introduced by a fix. | ✅ The deep copy is taken **only when a hook is configured**; the default path keeps the shallow copy, which is all the *aliasing* defect needs. |
| **D1 × D3** | *(absent from the previous table.)* `action/httpcall.ErrBodyTooLarge` already exists and means **500**; D1 minted a second sentinel with that name, in a phase running parallel to phase 6. | ✅ Renamed `ErrRequestBodyTooLarge`; a test asserts `httpcall.ErrBodyTooLarge` still classifies **500**. |
| **D1 × D5** | *(two Criticals.)* Three decode sites **discard** the error, so the 413 chain never fires and the adapters **diverge** — on an admin route ADR-0095 keeps out of the parity suite. And the variable cap can fire with **no HTTP caller** (`mergeVars`), where a static `"request too large"` is simply false. | ✅ The three sites get an explicit oversize path and a per-adapter test that **names the route**. ✅ Runtime growth is **out of scope by decision**, so the 413 always has a caller. |
| **D1 × D6** | Both caps reduce plaintext volume reaching `wrkflw_instances.snapshot`. | ✅ benign — D6 states the caps bound, but do not protect, data at rest. |
| **D2 × D3** | Does the input bound reach `httpcall`'s URL evaluation? Under the *evaluator* design: **no, and unwireable** — an `Action` receives `(ctx, in)` and holds no reference to the driver. | ✅ **Reframed by the admission move: yes, transitively** — `httpcall` reads the admitted variable map. No `httpcall`-side knob is needed. ⚠ And D3 **no longer routes `WithURLExpr` through `expreval`**, which would have replaced a non-string *rejection* with a *coercion*. |
| **D2 × D4** | The bound counts the map on the way in; redaction transforms it on the way out. | ✅ none. (The nested-copy hazard is D4-internal.) |
| **D2 × D5** | *(absent from the previous table.)* `ErrEnvTooLarge` was minted by D2 and routed by nobody — `ClassifyError`'s `default` would have made a caller-actionable refusal a **blank 500**. | ✅ The sentinel is now `service.ErrVariablesTooLarge` and D5 routes it **by name** to 413, with the general rule stated: any sentinel this bundle mints and does not route becomes a 500. |
| **D2 × D6** | The bound never touches persistence. | ✅ none. |
| **D2 × ADR-0049 replay** | Under the *evaluator* design, default-on **stranded already-persisted instances** above 10 000 elements — replaying that history would produce a different outcome, with no repair verb. The same upgrade-stranding shape that killed ADR-0185's D4. | ✅ **Dissolved by the admission move.** No rehydrated instance is re-checked; replay is untouched. |
| **D2 × the shipped `runtime` options** | Under the *evaluator* design, `WithExpressionTimeout` unconditionally reassigned `driver.conditionEval`, silently dropping the bound **for the untrusted-definition consumer** — the only one who needs it. | ✅ **Dissolved.** No third writer exists; both godocs stay true; the shared compile cache and the `slog` startup diagnostic keep their meanings. |
| **D3 × D4** | *(absent from the previous table.)* Redaction is a **display** control, the allowlist a **destination** control. `httpcall`/`transform` receive the **unredacted** map, so `WithURLExpr('…?q=' + vars.ssn)` to an allowed host is permitted. | ✅ Not a defect — but the two ship in one `SECURITY.md` and a reader would compose them into a guarantee neither makes. One sentence in D3 and in phase 9. |
| **D3 × D5** | D3's stated rationale was the single-vendor-wrapper rule; by import line there are **3** violators and D3 fixed **one** — while D5 *edits* one of the survivors. The "only wrapper" Consequence would be false on the day it shipped. | ✅ The `expreval` routing is **withdrawn** and the Consequence with it; phase 2 is explicitly told **not** to re-route the validator. `action/transform` is named in §3 and opened as a backlog item. |
| **D3 × D6** | Both land in `SECURITY.md`. | ✅ benign — phase 9 must not write them as one posture: one is about data we store, the other about connections we make. |
| **D4 × D5** | *(the previous table said "disjoint, no interaction".)* The 400 leaf **names the redacted key's namespace**; the draft instructed covering `maxLength`, whose leaf discloses a **length**; and the two controls sit on opposite sides of a layer boundary with no shared config. | ✅ D5 renders **`keywordLocation` only** — nothing derived from the value, no lengths. D4 states that redaction does not extend to validation errors and the namespaces are independent by design. |
| **D4 × D5 (breaking surface)** | Both force **unlisted** breaking changes to exported `httpcore` signatures — 8 endpoint functions for D4, `ClassifyError` for D5 — and one prescribed test landed in the wrong package as a result. | ✅ D4's parameter is threaded in **one** edit and listed as breaking; **`ClassifyError` keeps its signature** and the id is minted in `writeErr`; the correlation test moves to phase 5. |
| **D4 × the response-customization feature** | `CustomizeConfig.InstanceMapper` replaces the default view wholesale. | ✅ Redaction runs above it, and above the three mapper-less admin endpoints and the two mapper-less non-admin ones. |
| **D4 × itself** | The prescribed **shallow** copy plus a **mutation hook**: deleting a nested key from the clone deletes it from the **source**. Both natural hook implementations are wrong, in opposite directions. | ✅ The map handed to the hook is a **JSON-shaped deep copy**, and `cloneInstanceEntry`'s false *"deep-copies"* godoc is fixed in this bundle. |
| **D5 × D6** | *(the previous table said "none — different surfaces".)* Widening the logger to 400/403 moves submitted values **onto `slog.Default()` by default** — a sink D4 cannot redact and D6's enumeration excludes. The headline outcome would be a **relocation**, not a removal. | ✅ Logging is widened **per class**: 403 logs the raw error (it is the deployment's own policy source); 400 logs the **rendered** message; the raw 400 error only under `WithVerboseErrorLogging`, default off. D6 names the sink. |
| **D5 × the deferred ADR-0185** | ADR-0185 will add **401** and **503** arms to the same ordered switch, 401 next to the 403 arm D5 rewrites — and the order-dependence lesson would die with this bundle. | ✅ D5 records the ordering rule as a standing invariant for any future arm. ⚠ Leakage check **clean**: no ADR-0185 symbol appears in this bundle. |
| **any × ADR-0095** | Admin routes are default-absent from `Mount`, which is why the parity suite structurally cannot see the three discarding sites. | ✅ Folded into D1 — the per-adapter test names the admin route; phase 8 is **not** the net. |
| **any × ADR-0145/0147** | Only D6 touches the audit model, and it explicitly defers, citing ADR-0145. | ✅ none. |

## 6. ⚠ Known defects in the inherited evidence file

`docs/specs/2026-08-21-adr-0185-0186-premise-evidence.md` was written for the **B3** bundle.
⚠⚠ **The previous revision's header and this section contradicted each other** — the header
said §2 and §6 *hold*, this section said both were *defective*. This section was the correct
half, and the header is now corrected. **Four sections are defective:**

1. **§2 (the `??` guard form)** — the rows were run with an **empty** `vars` map while sitting
   under a section declaring `vars = {"tier":"gold"}`. Under that env
   `(vars.tier ?? "none") == "gold"` must be **true**; the transcript records `false`. The
   *finding* it supports is correct and independently confirmed; the **transcript is
   mislabelled**. Not load-bearing here.
2. **§5** — the tri-state `Open` codec evidence. Belongs to the **identity** delivery. Not used
   here.
3. **§6 (jsonschema)** — the probe called the **vendor** directly. It proves the *leak* and
   that a value-free rendering is *constructible*, but **not** that it is constructible at
   `ClassifyError`. It is not. ⚠ **Do not cite §6 for Decision 5** — this delivery's own
   evidence file §2 replaces it, executed through the real in-repo strategy.
4. **§7** — a re-derived enumeration table for **ADR-0185**. ⚠ The previous revision said
   *"§7's `274/128/5` triple … all three numbers are wrong"*; that is an over-statement of its
   own source. §7 holds **one** of the three (`274`), and that number **reproduces exactly** —
   only the noun was wrong (274 = 273 call sites + the declaration). The `128` and `5` figures
   live in **ADR-0185 and the B3 spec**, not in §7. Separately, §7's `expreval.New(` row
   records **4** for a command that outputs **5** (the fifth match is inside a godoc).
   **None of this material is used here.**

§1 (expr builtin behaviour), §3 (reference-extraction shapes) and §4 (guard dominance) hold and
were re-confirmed; they belong to the deferred backlog-103 delivery.

## 7. What is still NOT executed

Labelled so the audit attacks the boundary rather than re-deriving it. ⚠ Audit #1 found
**three unlabelled assumptions sitting inside Decision text as measurements**, and **one orphan
entry** referring to a claim no document made. Both classes are fixed here.

- `ASSUMPTION (unverified)`: the **1 MiB body default** and the **256 KiB variable-byte
  default**. Judgement calls with nothing behind them. One datapoint argues 256 KiB is *low*:
  first-party `action/httpcall` writes up to **10 MiB** into `vars["httpBody"]`.
- `ASSUMPTION (unverified)`: **256 KiB ⇒ ~40–50 k JSON-integer elements** (~6 bytes/element
  with separators). Never executed as a general conversion; it supplies the `n` for D2's two
  largest table rows. An auditor measured the exact figure at the defaults as **45 540** for one
  element shape.
- `ASSUMPTION (unverified)`: that a **recording OTel span exists** at the transport seam. It
  requires consumer-installed tracing middleware; `CustomizeConfig.TracerProvider` being present
  does not imply it. Phase 5's correlation-id tests must cover **both** the span path and the
  random-hex fallback.
- `ASSUMPTION (unverified)`: the **mysql and postgres** plaintext-column claim is read from the
  DDL, not exercised. Sufficient for a `SECURITY.md` statement; not a round-trip test.
- **Discharged since the previous revision, do not re-derive:** the n = 10 000 extrapolation
  (measured 2.458 s and 1.92 s, two lenses, two machines); the fiber `len(c.Body())` mechanism
  (viable **only** for uncompressed bodies ≤ `fiber.DefaultBodyLimit` — which is why D1 uses
  `BodyRaw()`); the ctx-path benchmark (three reproductions); the `gate.go:45` flattening (two);
  the 413/400 ordering defect; and the eight-sentinel count.
- ⚠ **Deleted as an orphan:** the previous revision's *"never executed: stdlib and gin return
  201 for a 256 MiB body, and its heap figures"*. **No document in this bundle makes that
  claim** — it was residue from the excised B3 record, and it was the one §7 entry an auditor
  would have spent time on.

## 8. Non-goals

- No key management. The library will not hold, rotate or derive encryption keys — which is why
  100 (at-rest codec) is a posture, not a mechanism.
- No identity work. That is ADR-0185's delivery.
- No new transport, no gRPC, no BPMN2 XML.
- No vendor-wrapper consolidation. Three files import `expr-lang/expr` directly; this delivery
  routes none of them and opens the question as a backlog item.

# Plan — untrusted input and disclosure posture (ADR-0186)

> ## ⚠ REVISED 2026-08-21 after its first standalone audit. NOT YET RE-AUDITED.
>
> Audit #1: **63 findings, 33 Critical**, and **the plan was the point of failure** —
> *"six Criticals share one root cause: a decision stated in the ADR whose realisation lands
> in a package no phase assigns it to"*. §2 below is rebuilt around the mechanical check that
> lens proposed: **every sentence of the Decision section is mapped to a phase and a package,
> and any that has none is rejected.**
> ⭐ Two phases were **deleted** by the admission move (`internal/expreval`, `runtime`), and
> the phase that was last is now first (the classifier cannot route a sentinel that does not
> exist yet).
> Adjudication: `docs/plans/sweep-evidence/audit-0186-adjudication.md`.
> ⚠ **A bundle whose decisions changed has not been audited.** Nothing below starts until the
> re-audit passes.

## ▶ Progress

- **Branch:** `design/authz-security-b3` (docs-only). ⚠ Do not quote its SHA — it is amended
  on every revision.
- **State:** re-cut 2026-08-21 from the failed B3 bundle; **audit #1 failed (63 findings, 33
  Critical); this revision folds it. Re-audit PENDING. Zero phases executed.**
- **Lineage:** ADR-0186 was half of B3, which failed two audits — the first on individual
  decisions (58 findings), the second on the interactions between the four decisions the
  revision rewrote (38 findings). Then this record's own first standalone audit (63 findings,
  four lenses, 4,020 lines). Records: `docs/plans/sweep-evidence/{audit,reaudit}-b3-adjudication.md`
  then `audit-0186-adjudication.md` with its four lens reports.
- **What this revision changed:** the element bound moved **evaluation → admission** (closing
  I-2, I-3, I-9, I-14, E1, E2, F14 together); D2's once-per-env mandate deleted; D5's 400
  rendering changed to a value-free **schema** location and its blanket blanking replaced by an
  executed exception list; D3's `WithAllowedHosts` mechanism replaced and `WithAllowedCIDRs`
  added; D4's copy made deep and its covered set widened to eleven paths and named; D6's
  enumeration re-derived (**12** columns, 7 tables, 3 dialects); four counts corrected.
- **This delivery's own executed evidence:** `docs/specs/2026-08-21-adr-0186-premise-evidence.md`.

---

## 0. What the re-audit must attack

The author's own list of where this is most likely still wrong. Give it to the auditors; do not
make them re-derive it. ⚠ **Items 1–7 of the previous list are gone — they were answered by
audit #1** (the fiber mechanism, the D2×D3 open question, the option collision, once-per-env,
the strategy set, the ladder at n = 10 000). Re-deriving them is wasted budget; see spec §7's
"discharged" list.

1. ⭐ **The admission move is the single largest change, and it is only as good as its seam.**
   Spec §4.6 says the caller-supplied variable maps are exactly four request fields. **Is that
   set closed?** A fifth path into `State.Variables` that a caller controls would be a hole
   straight through Decision 2.
2. **Runtime growth is out of scope BY DECISION** (ADR D2). Attack that decision, not its
   absence: is there a caller-reachable path that grows the map without passing an admission
   point — a signal payload merged after admission, a callback mirror, a chained instance's
   `start_vars`?
3. **The 400 exception list is deny-by-default over an OPEN set.** Five sentinels keep
   `err.Error()` on the strength of an executed claim about *today's* message text. Attack the
   claim (does any of the five have a caller-value-bearing wrap site the author missed?) **and**
   the invariant (does the pin test actually fail when a new sentinel joins the arm?).
4. **`keywordLocation` is claimed value-free BY CONSTRUCTION.** Executed for four schema shapes.
   **Find a fifth** — `patternProperties`, `$ref`, `$dynamicRef`, `unevaluatedProperties`, a
   schema whose own text embeds caller-supplied content.
5. **The IP deny-list is stated as a property (`not global unicast`) plus three explicit
   ranges.** Attack the property: is there a reachable internal address that IS global unicast?
   And attack the ordering — does the host allow-list run where the ADR says, before the dial?
6. **The eleven read paths.** This enumeration has now rotted **twice** (6 → 6+2 → 6+2+3).
   Assume it is still short. The counting lens's own proposal — a machine-checked invariant over
   `NewInstanceView(` call sites — is prescribed in phase 3; check that it can fail.
7. **The twelve plaintext columns.** Rotted twice as well (2 → "at least six" → 12), and the
   audit's own count was short by three tables. This is D6's **deliverable**, not a footnote.
8. ⚠ **One lens must be the counting lens** (rule #9). The B3 lineage rotted enumerations in
   **all three** rounds, and in every case the arithmetic was right — the failure was the
   grep's **net** and the citation's **anchor**. Brief it that way. §4 below re-derives every
   count this delivery uses; assume there is one more.
9. ⚠ **One lens must do the interaction pass** (rule #9). It found **8 Criticals on its first
   use** that no other lens reported in the same terms. Spec §5 is now a 21-row table rebuilt
   from that lens's own derivation — **attack the rows it marks ✅, and find the pairs it still
   omits.** The changed decisions, for the pairwise brief: **D1, D2, D3, D4, D5 and D6** — that
   is all six, so the interaction lens has the full grid to re-derive.
10. ⚠ **Every auditor gets the step-0 worktree check**: verify the bundle is present, STOP if
    not. Create worktrees **detached at the bundle commit**. ⚠ The bundle is now **four** files
    — spec, ADR, plan, **and `docs/specs/2026-08-21-adr-0186-premise-evidence.md`**.
11. ⚠ **Attack the evidence file too.** Audit #2 of B3 found three of its findings inside the
    bundle's own evidence file — the one written to stop unexecuted claims. This delivery has a
    new one, written by the author, and it is an **input** to the audit, not a conclusion of it.
12. ⚠ **Do not re-audit the identity material.** ADR-0185 is a separate, later delivery. If a
    finding depends on a symbol ADR-0185 introduces, it is out of scope — say so rather than
    folding it in.
13. ⭐ **Three interactions the AUTHOR found in this fold, before dispatch** (rule #9's corollary:
    *"if a revision touches more than one decision, write down the pairwise consequences yourself
    and mark the ones you could not resolve"*). All three were holes this revision's own fixes
    opened in each other. They are **resolved in the documents** — attack the resolutions:
    - **D2 × itself.** Bounding the *merged* map gives the stronger guarantee and **wedges**
      instances that grew at runtime. Resolved by bounding the *incoming* map — which means the
      aggregate is **not** bounded, and an earlier draft's *"nothing is ever persisted that
      cannot be evaluated"* is withdrawn. **Is the weaker property still worth the surface?**
    - **D4 × the read hot path.** The deep copy fixing the nested-mutation hazard is taken only
      when a hook is configured. **Is the shallow default still sufficient for the aliasing
      defect in every case** — including a nested value a *consumer* mutates without a hook?
    - **D3's two gates.** `AllowedHosts` and `AllowedCIDRs` are opt-in lists over two different
      gates, both of which must pass, with CIDRs exempting only the IP gate. **Is there a
      configuration in which a consumer reasonably expects access and gets none, or expects a
      block and gets access?**

---

## 1. Fan-out rules

- **Fan out by Go package.** Concurrent agents in one package break each other's `go test`
  compile even on disjoint files.
- **Phase 2 stays INLINE in the controller** — it spans `runtime/validation` **and**
  `definition/model/validate/expr`, and it changes an error's *type* discipline that phase 3
  depends on.
- **Docker:** the standing carve-out covers the Verification runs only. Every package in this
  delivery is **container-free**: `service`, `runtime/validation`,
  `definition/model/validate/*`, `transport/http/*`, `action/httpcall`, `persistence` (one
  comment). **No agent needs Docker**; say so explicitly in each brief so nobody asks.
- **`golangci-lint`:** probe `command -v golangci-lint` and run it; if absent, say so and offer
  install-or-skip. Never substitute `go vet`.
- ⚠ **A mutation ablation gets its own `git worktree`** — a live ablation in a shared tree once
  cost another agent ~40 minutes as a phantom hang.
- ⚠ **`go build ./examples/...` is attached to phase 3**, not only to the final checklist:
  phase 3 is where the public `httpcore` surface changes, and `examples/` is the only
  consumer-compile check in the repo.

---

## 2. ⚠ Decision → phase → package map (the mechanical check audit #1 prescribed)

**Every sentence of ADR-0186's Decision section has a row. A row with no phase is a defect.**
Six of audit #1's fifteen Criticals were this one omission.

| ADR sentence | phase | package |
|---|---|---|
| D1 `MaxBodyBytes` config field + `0` = unbounded | 3 | `transport/http/httpcore` |
| D1 `ErrRequestBodyTooLarge` sentinel | 3 | `transport/http/httpcore` |
| D1 413 arm **before** 400 arm | 3 | `transport/http/httpcore` |
| D1 `httpcall.ErrBodyTooLarge` still classifies 500 (test) | 3 | `transport/http/httpcore` |
| D1 body cap at the **36 propagating** decode sites | 4 | `stdlib` \| `gin` \| `fiber` |
| D1 **explicit oversize path at the 3 discarding sites** | 4 | `stdlib` \| `gin` \| `fiber` |
| D1 fiber `BodyRaw()` (wire bytes) | 4 | `fiber` |
| D1 fiber mount **WARN** above `DefaultBodyLimit` | 4 | `fiber` |
| D1 `wrkflw_rest_request_body_bytes` histogram | 3 | `transport/http/httpcore` (`observability.go`) |
| D2 `WithMaxVariableBytes` / `WithMaxVariableElements` | **1** | `service` |
| D2 `ErrVariablesTooLarge` sentinel | **1** | `service` |
| D2 admission checks at the **four** request fields | **1** | `service` |
| D2 element count with **early exit at n+1** | **1** | `service` |
| D2 → 413 (routing the `service` sentinel) | 3 | `transport/http/httpcore` |
| D3 IP deny-list in `Dialer.Control` | 5 | `action/httpcall` |
| D3 host allow-list on URL + each redirect hop | 5 | `action/httpcall` |
| D3 `WithAllowedHosts` / `WithAllowedCIDRs` / `WithUnrestrictedTransport` | 5 | `action/httpcall` |
| D3 `WithHTTPClient` + `WithURLExpr` **refused** | 5 | `action/httpcall` |
| D3 `WithBaseURL` unchanged (control test) | 5 | `action/httpcall` |
| D4 `RedactVariables` + `RedactionScope` on `CustomizeConfig` | 3 | `transport/http/httpcore` |
| D4 redaction helper on **all 11** paths + count invariant | 3 | `transport/http/httpcore` |
| D4 **deep** copy for the hook; `view.go` copies | 3 | `transport/http/httpcore` |
| D4 endpoint signature thread (8 functions) | 3 | `transport/http/httpcore` |
| D4 `cloneInstanceEntry` **false godoc** fixed | 3 | `persistence` (one comment — see brief) |
| D4 `SECURITY:` caveat on instance + task groups | 7 | `stdlib` \| `gin` \| `fiber` (controller, serialised after 4/6) |
| D5 403 static | 3 | `transport/http/httpcore` |
| D5 400 exception list + **pin invariant** | 3 | `transport/http/httpcore` |
| D5 two `ErrBadInput` wrap sites de-valued | 3 | `transport/http/httpcore` |
| D5 gate preserves the typed error (`%w`) | **2** | `runtime/validation` |
| D5 `keywordLocation`-only rendering via `BasicOutput()` | **2** | `runtime/validation` |
| D5 `expr` stops `%q`-ing `v.source[i]` | **2** | `definition/model/validate/expr` |
| D5 `avro`/`callback`/unknown-kind → static | **2** | `runtime/validation` |
| D5 correlation id **minted in `writeErr`** | 4 | `stdlib` \| `gin` \| `fiber` |
| D5 4xx logging widened **per class** | 4 | `stdlib` \| `gin` \| `fiber` |
| D5 `WithVerboseErrorLogging` option | 3 | `transport/http/httpcore` |
| D5 `Logger` godoc corrected | 3 | `transport/http/httpcore` |
| D5 arm-ordering invariant comment | 3 | `transport/http/httpcore` |
| D6 `SECURITY.md` derived-enumeration + invariant test | 7 | docs + `internal/persistence/store` |
| D6 log-sink + caps-do-not-protect sentences | 7 | docs |
| Consequences: 5 wire breaks + 1 source break | 7 | `CHANGELOG.md` / `STABILITY.md` |

**Phase table:**

| # | package(s) | ADR decision | depends on | fan-out |
|---|---|---|---|---|
| 1 | `service` | D2, D1(vars) | — | 1 agent |
| 2 | `runtime/validation` + `definition/model/validate/expr` | D5 (rendering) | — | **controller, inline** |
| 3 | `transport/http/httpcore` (+1 comment in `persistence`) | D1, D2(routing), D4, D5 | 1, 2 | 1 agent |
| 4 | `transport/http/stdlib` \| `gin` \| `fiber` | D1, D5(id + logging) | 3 | **3 agents in parallel** |
| 5 | `action/httpcall` | D3 | — | 1 agent (‖ 1, 2, 3, 4) |
| 6 | `transport/http/parity` | test fallout | 4 | 1 agent |
| 7 | docs + the `SECURITY:` caveats | all | 6 | controller |

⚠ **Phase 1 moved from last to first.** Audit finding F19: the classifier arm for `service`'s
sentinel was **unschedulable** as the table was drawn — phase 7 (`service`) depended on nothing
and ran in parallel with phase 4 (`httpcore`), so the sentinel did not exist when the classifier
was written and the classifier was done when the sentinel appeared. It would have shipped as an
empty-bodied **500**.

⚠ **Phase 5 is fully independent** and may start immediately.

---

## 3. Phases

### Phase 1 — `service`: variable maps are bounded at admission

**Symbols:**
- `service.WithMaxVariableBytes(n int64) Option` — default **256 KiB**, `0` = unbounded.
- `service.WithMaxVariableElements(n int) Option` — default **10 000**, `0` = unbounded.
- `service.ErrVariablesTooLarge` — one sentinel; its message names **which** bound tripped.
- Enforced at the four caller-supplied request fields and **nowhere else**:
  `StartInstanceRequest.Vars`, `DeliverSignalRequest.Payload`, `DeliverMessageRequest.Payload`,
  `CompleteTaskRequest.Output`.
- The element count walks with an **early exit at `n+1`** — `O(min(elements, n))`, so it can
  never cost more than the bound it enforces.

⚠ **Do NOT touch `internal/expreval`, `runtime` or `engine`.** The draft's evaluator-side bound
is withdrawn; ADR-0186 D2 explains why in four executed steps. If implementation suggests the
bound "really belongs" at the evaluator, **stop and escalate** — that is the design audit #1
refuted, not a discovery.

**Tests, and what makes each fail today:**

1. `TestStartInstanceRefusesOversizedVariablesByBytes` and
   `…ByElementCount` — table over the **four** admission fields.
   **Fails today:** the options do not exist → compile error.
   ⚠ **Fixture check:** the element case must be **nested** (one key holding a 20 000-element
   slice), not 20 000 top-level keys — a top-level fixture cannot fail for the reason the test
   names.
2. `TestVariableBoundsDefaultsApply` — a service built with **no** options still refuses.
   **Fails today:** no bound exists at all. Without this row, an implementer who wires the
   options but not the defaults passes test 1.
3. `TestVariableBoundZeroIsUnbounded` — the control, for both knobs. Without it, an implementer
   who treats `0` as "reject everything" bricks every existing consumer and tests 1–2 still pass.
4. ⭐ `TestByteAndElementBoundsAreEnforcedAtTheSameSeam` — **the control that decides the
   admission move.** A request carrying **20 000** small integers (~109 KiB — under the 256 KiB
   byte bound, over the 10 000 element bound) is refused **at admission**, in the same call, and
   **nothing is persisted**.
   **Falsifier, stated:** *it fails against an implementation that enforces the two bounds at
   different points* — which is the design audit #1 rejected, where the byte bound admitted
   **45 540** elements against a cap of 10 000 and the element bound refused them afterwards,
   forever.
   ⚠ **Do not name this test `…AreAlwaysEvaluable`.** The bound is on the **incoming** map, not
   the merged result (ADR D2), so the aggregate map can still exceed it via runtime growth. A
   test named for the stronger property would be asserting something the design does not claim.
5. `TestElementCountExitsEarly` — a benchmark or a counter assertion showing the walk over an
   env **100× the bound** costs no more than one over an env **at** the bound.
   ⚠ This replaces the previous revision's phase-1 test 4, which mandated *"the bound adds no
   per-evaluation walk"* — an assertion that **could not be satisfied by any correct
   implementation** and would have stalled the phase at its own escalation clause.
6. `TestRuntimeVariableGrowthIsNotRefused` — ⚠ **the scope control.** An action output merged via
   `mergeVars` that carries the map past the bound must **not** be refused, because refusing a
   persist after the side effect has happened wedges the instance with no repair verb.
   **Falsifier:** *it fails against an implementation that checks at the persist boundary
   instead of at admission.*
7. `TestCompleteTaskIsNotRefusedBecausePriorStateIsLarge` — ⚠ **the second scope control, and
   the one that keeps test 6 honest.** With existing variables already past the bound (grown at
   runtime), a `CompleteTask` carrying a *small* `Output` must still succeed.
   **Falsifier:** *it fails against an implementation that bounds the MERGED map rather than the
   incoming one* — which would refuse that task 413 forever and is the wedge ADR D2 rejects.

**Verify:** `go test -race -count=1 ./service/...`

---

### Phase 2 — the structured validation error survives, and renders value-free  ⚠ CONTROLLER, INLINE

⚠ **This phase exists because the fix cannot live at the transport.**
`runtime/validation/gate.go:45` is `fmt.Errorf("%w: %s", ErrInvalidInput, err.Error())` — `%s`
flattens the strategy's typed error to a string, so `errors.As` is `true` before the gate and
`false` after. Executed twice.

- The gate preserves the strategy's error (`%w`) and **renders the client-safe message itself**,
  where the type is still available.
- The rendering is an **allow-list keyed on strategy KIND** (the set is open —
  `validate.Register` is exported and `callback` takes an arbitrary function):
  - `jsonschema` → one line per leaf of `ValidationError.BasicOutput().Errors`, carrying
    **`keywordLocation` and nothing else**.
  - everything else → static `"invalid input"`.
- ⚠ **Do NOT render `instanceLocation`** — it is *instance*-derived and a caller-chosen object
  key renders verbatim. ⚠ **Do NOT render the vendor's `Error.String()`** — it carries the value
  *and* lengths. ⚠ **Do NOT call `ErrorKind.LocalizedString`** — it panics on a nil printer
  (turning a malformed request into a server panic **inside the 400 path**) and a real printer
  promotes `golang.org/x/text` from indirect to a new direct dependency.
- ⚠ **Do NOT apply the prescription to the root error.** The object `errors.As` yields is the
  **root** `*jsonschema.ValidationError`, whose `KeywordPath()` is **empty**; the usable leaves
  are in `.Causes`, recursively. `BasicOutput()` flattens them.
- `definition/model/validate/expr/expr.go:64,68` stops echoing `v.source[i]` on the
  runtime-validation path. ⚠ **Do NOT also re-route that package through `internal/expreval`** —
  that is a separate question (ADR D3 withdrew it) and changing validation semantics mid-phase
  is out of scope.

**Tests, and what makes each fail today:**

1. `TestJSONSchemaRenderingIsValueFree` — a table, **and the table is the point**:
   | fixture | must NOT appear |
   |---|---|
   | `{"ssn":"123-45-6789"}` vs a `pattern` schema | `123-45-6789` |
   | an 11-char value vs `maxLength: 3` | `got 11`, `11`, any length |
   | ⭐ a card number submitted **as an object key**, against `propertyNames` + `additionalProperties` | `4111-1111-1111-1111` |
   | an array whose item 1 is the wrong type | (positive: the message is non-empty and names a keyword) |
   **Fails today:** executed — the messages are
   `at '/ssn': '123-45-6789' does not match pattern …`, `maxLength: got 9, want 3`, and
   `at '/4111-1111-1111-1111': …`.
   ⚠ **Row 3 is the falsifier for the FIX, not the bug**: *it fails against an implementation
   that renders `instanceLocation`* — which is what the previous revision prescribed, and its
   closed-`properties` fixture was **green against the leak**.
   ⚠ **Row 4 is the anti-vacuity control**: a rendering that emits `at '/': violates ` satisfies
   every "does not contain" assertion. Assert the message is non-empty **and** names a keyword.
2. `TestExprValidationErrorDoesNotEchoPredicateSource`.
   **Fails today:** `expr.go:64,68` `%q` the source.
   ⚠ **This proves the allow-list, not the special case.** Without it a fix confined to
   `jsonschema` is green.
3. `TestNonStructuredStrategiesRenderStatically` — rows for **`avro`** and `callback`.
   **Fails today:** the message is passed through verbatim — and for avro that message
   **contains the submitted value** on the enum path (`"4111-1111-1111-1111"`) and a length on
   the fixed path (`11 != 4`). ⚠ The previous revision's test was `callback`-only and avro had
   no test at all, while the ADR table named it.
4. `TestUnknownStrategyKindRendersStatically` — register a throwaway kind via
   `validate.Register`. **Fails today:** compile/behaviour — there is no kind-keyed allow-list.
   ⚠ This is what makes deny-by-default true over an **open** set.
5. `TestStructuredErrorSurvivesTheGate` — `errors.As` finds the strategy's typed error **after**
   the gate.
   **Fails today:** `gate.go:45` stringifies it. ⚠ Assert on `errors.As`, not on message text —
   this is the falsifier for the whole phase.

**Verify:** `go test -race -count=1 ./runtime/validation/... ./definition/model/validate/...`

---

### Phase 3 — `transport/http/httpcore`: caps, classification, redaction

**D1:**
- `CustomizeConfig.MaxBodyBytes` (default **1 MiB**, `0` = unbounded) and
  `ErrRequestBodyTooLarge`. ⚠ **Not `ErrBodyTooLarge`** — `action/httpcall.ErrBodyTooLarge`
  already exists and means **500**.
- `wrkflw_rest_request_body_bytes` histogram alongside the existing instrumentation.

**D2 routing / D5 classification:**
- `ClassifyError`: **413 arm placed BEFORE the 400 arm**, matching **both**
  `ErrRequestBodyTooLarge` **and** `service.ErrVariablesTooLarge`, with a comment naming the
  order-dependence and stating it as a **standing invariant for any future arm** (ADR-0185 will
  add 401 and 503).
- 403 → static `"not authorized"`.
- 400 → the **exception list** in ADR-0186 D5: `err.Error()` for `ErrBadCursor`,
  `ErrBadArmedTimerCursor`, `ErrEmptyTriggerKey`, `ErrEmptyReassignTarget`, `ErrBadInput` and
  `ErrOutcomeRequired`; **reshaped** for `ErrInvalidOutcome`; phase 2's rendering for
  `ErrInvalidInput`; **static for anything else**.
- The two `ErrBadInput` wrap sites that embed a caller value —
  `admin_endpoints.go:30` (`unknown status %q`) and `dto.go:174` (`got %q`) — name the allowed
  set instead of the rejected input.
- `WithVerboseErrorLogging(bool)`, default **off**.
- Correct `CustomizeConfig.Logger`'s godoc — it says *"receives 5xx raw error details"* and now
  also receives 400's rendered message and 403's raw error.
- ⚠ **`ClassifyError` keeps its signature.** The correlation id is minted in phase 4's
  `writeErr`, which already holds a `ctx`. Changing `ClassifyError(err error)` would break an
  exported function `doc.go:66` advertises as a consumer seam.

**D4:**
- `CustomizeConfig.RedactVariables func(ctx, RedactionScope, map[string]any) map[string]any`
  and the `RedactionScope` type (`InstanceID`, `DefinitionRef`, `Kind`).
- A redaction helper applied on **all eleven** paths: the 6 `mapInstance` sites, the 3 direct
  `NewInstanceView` admin sites (`admin_endpoints.go:111/121/514`), and the 2 mapper-less
  endpoints (`GetInstanceSnapshot`, `GetActionableView`).
- Thread the response policy into the **eight** exported endpoint functions in **one** edit.
- `view.go:31` copies rather than aliases. ⚠ The map handed to the hook is a **JSON-shaped deep
  copy — taken only when a hook is configured**; the default path keeps the shallow copy, which
  is all the aliasing defect needs. A recursive copy on every read of every instance is an
  unmeasured cost on a hot path, and charging it to consumers who configure no hook would be a
  regression introduced by a fix.
- ⚠ **One comment in another package:** `persistence/caching_instance_store.go:72` claims
  `cloneInstanceEntry` *"deep-copies"*. It does not (`copyVars` is `maps.Clone`). Fix it here —
  no other phase touches `persistence`, so there is no fan-out conflict. Verify with
  `go vet ./persistence/...`.

**Tests, and what makes each fail today:**

1. `TestOversizedBodyClassifiesAs413NotBadRequest`.
   **Fails today:** the sentinel does not exist → compile error.
   ⚠ **Must include a row where the error wraps BOTH `ErrBadInput` and the new sentinel,
   asserting 413** — executed, that combination classifies **400** today. Without the row the
   test passes against the ordering bug.
   ⚠ **Plus a row asserting `httpcall.ErrBodyTooLarge` still classifies 500.**
2. `TestOversizedVariablesClassifyAs413` — `service.ErrVariablesTooLarge`.
   **Falsifier:** *it fails against a 413 arm that names only `ErrRequestBodyTooLarge`* — which
   is what the previous phase table made the only schedulable outcome.
3. ⭐ `TestFourHundredArmRenderingIsPinned` — one row per sentinel in the arm, asserting the
   exact rendering, **plus a machine-checked invariant**: the set of sentinels matching the 400
   arm equals the enumerated set. **Fails today:** all eight render `err.Error()`.
   ⚠ **The invariant is the point.** A new sentinel added to the arm without a row must **fail
   the test**, not silently inherit `err.Error()`. Prove it by adding one in a mutation.
   ⚠ And the four sentinels that **keep** `err.Error()` need positive assertions — audit finding
   F4 is that the previous design destroyed messages ADR-0146/0152/0183 deliberately added, and
   only a positive assertion protects them.
4. `TestClassifyErrorDoesNotEchoPredicateSource` — build a real 403 from an erroring attribute
   predicate; assert the body omits the identifier.
   ⚠ **Mandatory control:** `require.Contains(t, err.Error(), "internalApprovalLimit")` *before*
   classifying. Both the deny path and the eval-error path produce 403 and only the latter
   leaks; without this a predicate that quietly returns `false` makes the assertion pass
   vacuously.
5. `TestInstanceViewCopiesVariables` — mutate the returned map, assert the source is unchanged.
   **Fails today:** `view.go:31` aliases.
   ⚠ Do **not** restate the withdrawn *"mutates instance state"* claim in the comment — it is
   false for top-level values (see test 6).
6. ⭐ `TestRedactionOfANestedKeyDoesNotMutateTheSource`.
   **Fails today:** executed — `delete(clone.Variables["applicant"].(map[string]any), "ssn")`
   deletes it from the source, because `copyVars` is `maps.Clone`.
   ⚠ **Falsifier:** *it fails against a `maps.Clone`-based copy.* ⚠⚠ **Check the fixture:** a
   fixture whose variables are all top-level scalars **cannot fail** for the reason this test
   names — top-level deletes are already isolated.
   ⚠ **Plus the control for the conditional copy:** `TestNoHookConfiguredTakesTheShallowCopy` —
   with `RedactVariables` nil, the response still does not alias `st.Variables` (test 5) and no
   recursive copy is taken. **Falsifier:** *it fails against an implementation that deep-copies
   unconditionally* — which is the hot-path regression ADR D4 avoids.
7. `TestRedactionAppliesUnderCustomInstanceMapper` — set both `InstanceMapper` and
   `RedactVariables`; assert the mapper never sees the redacted key.
   **Fails today:** compile error; and against a fix placed inside `NewInstanceView` this fails
   while a default-mapper test passes.
8. ⭐ `TestRedactionCoversAllElevenReadPaths` — a table with **one row per path**, including the
   **three direct-`NewInstanceView` admin endpoints** and `GetInstanceSnapshot`.
   **Fails today:** no redaction exists — and each admin row **fails against a fix confined to
   `mapInstance`**, which is the whole point.
   ⚠ **Plus the count invariant:** assert that the number of `NewInstanceView`/`mapInstance`
   call sites routed through the helper equals the number that exist. This enumeration has
   rotted **twice**; a number in prose will rot again.
   ⚠ **`TestActionableViewRedactsTaskVars` IS DELETED.** Executed: `ActionableTask` has **no
   `Vars` field** and `NewActionableView` never reads `t.Vars`, so the test could not fail — and
   the previous revision billed it as one of *"the controls that decide D4's placement"*.
   `GetActionableView` stays in the eleven as a **path** (it takes the policy parameter), but it
   carries no variables to redact.

**Verify:** `go test -race -count=1 ./transport/http/httpcore/...` then
`go vet ./persistence/...` then **`go build ./examples/...`** — this phase changes the public
`httpcore` surface and `examples/` is the only consumer-compile check.

---

### Phase 4 — `transport/http/{stdlib,gin,fiber}`  ⚠ THREE PARALLEL AGENTS

One agent per package. **Never two agents in one package.**

**Body caps at all 13 decode sites in each `groups.go` — but the sites are NOT uniform:**

- **12 propagating sites per adapter** — install the cap and return the **bare**
  `httpcore.ErrRequestBodyTooLarge` on the oversize path. Keep
  `fmt.Errorf("%w: %w", httpcore.ErrBadInput, err)` for **decode** failures only; an oversize
  error carrying `ErrBadInput` classifies **400**, because the arms are ordered.
- ⚠⚠ **1 DISCARDING site per adapter** — `stdlib/groups.go:238`, `gin/groups.go:265`,
  `fiber/groups.go:255`, all the optional-body
  `POST /admin/instances/{id}/incidents/{incidentID}/resolve`. These are `_ = decode(&in)`, so
  **there is no error to convert**. They must **gain** a path that distinguishes *body absent /
  EOF* (keep ignoring — the body is genuinely optional) from *body present but oversize*:
  ```go
  if err := decode(&in); err != nil && errors.As(err, new(*http.MaxBytesError)) {
      writeErr(cfg, …, httpcore.ErrRequestBodyTooLarge)   // bare → 413
      return
  }
  // any other decode error stays ignored: the body is optional
  ```
  Without this, the cap is installed and its violation is **silently swallowed into a 2xx** —
  the worst outcome for a security control.

**Per-adapter mechanism:**
- `stdlib` — `http.MaxBytesReader` before `json.NewDecoder`.
- `gin` — assign `gc.Request.Body = http.MaxBytesReader(...)` **before** `ShouldBindJSON`.
  ⚠ Executed: both stdlib and gin surface the **bare `*http.MaxBytesError`** — `errors.As`
  works through both decoders. *"gin wraps it again"* is false; **two shapes, not three.**
- `fiber` — a **`len(c.BodyRaw())`** pre-check before `c.Bind().JSON`.
  ⚠⚠ **NOT `c.Body()`.** Executed: `c.Body()` **decompresses**, so a 63.7 KiB gzip expanding to
  64 MiB returns `len == 33` (the string `"body size exceeds the given limit"`), the pre-check
  passes it through, and the client gets a **400 over fiber's own 413**. `BodyRaw()` is the
  un-decompressed wire body with no response side effect.
  ⚠ Also: `Mount` logs a **WARN** when `MaxBodyBytes > fiber.DefaultBodyLimit` (4 MiB), because
  above that the route group is never reached and the knob is silently ineffective.

**D5's log half — all three adapters:**
- `writeErr` mints the correlation id (the recording OTel span's id, else a random hex) and
  assigns it onto `ErrorBody`.
- The `status >= 500` guard widens **per class**: 403 logs the **raw** error at `WarnContext`;
  400 logs the **rendered** message + sentinel class at `WarnContext`; the raw 4xx error only
  under `WithVerboseErrorLogging`; 5xx unchanged at `ErrorContext`.

**Tests per package:**

1. `TestOversizedBodyReturns413` — on a normal required-body route.
   **Fails today:** no cap exists, so the body is read in full.
   ⚠ **Falsifier:** *it also fails against an implementation that keeps the `ErrBadInput`
   wrapper.*
2. ⭐ `TestOversizedBodyReturns413OnTheOptionalBodyAdminRoute` — **names the resolve-incident
   route**.
   ⚠ **Falsifier:** *it fails against an implementation that only edits the 12 wrapping sites
   and leaves `_ = decode(&in)`.* ⚠ Phase 6's parity suite **cannot** be the net here: ADR-0095
   keeps admin routes out of `Mount`.
3. `TestCorrelationIDInBodyMatchesTheLogRecord` — assert against a `slog` handler capturing
   records. ⚠ **Both legs:** with a recording span (the id matches it) and without one (a
   non-empty random hex). ⚠ This test was previously prescribed in phase 3, a package that
   **cannot emit a log record**.
4. ⭐ `TestRejected400PayloadIsNotWrittenToTheDefaultLogger`.
   **Falsifier:** *it fails against an implementation that widens the `status >= 500` guard
   unconditionally* — which would relocate the submitted value from the wire onto
   `slog.Default()`, a sink `RedactVariables` cannot reach.
5. **fiber only:** `TestCompressedBodyOverTheCapReturns413` — a gzip body whose **wire** size is
   under the cap and whose decompressed size is over it must **not** 413 (wire bytes is the
   contract), and one whose wire size is over must 413.
   ⚠ **Falsifier:** *the first row fails against a `len(c.Body())` pre-check, which sees 33.*

**Verify (per agent):** `go test -race -count=1 ./transport/http/<pkg>/...`

---

### Phase 5 — `action/httpcall`: SSRF posture  *(independent — may start immediately)*

- ⚠ **`WithURLExpr` keeps its own `expr.Compile`.** Do **not** route it through
  `internal/expreval`: executed, `expreval.EvalString` **coerces** a non-string result
  (`nil` → `"<nil>"`, `1+1` → `"2"`, `{"a":1}` → `"map[a:1]"`) where `httpcall.go:239-242`
  **rejects** it non-retryably — the refactor would weaken URL type discipline inside the
  decision whose purpose is to stop SSRF.
- Restricted client for **expression-derived** URLs only (`urlExprProg != nil`):
  - **IP deny-list in `net.Dialer.Control`** — refuse any resolved address that is not global
    unicast: `IsLoopback`, `IsLinkLocalUnicast`, `IsLinkLocalMulticast`,
    `IsInterfaceLocalMulticast`, `IsUnspecified`, `IsPrivate` — **plus** `100.64.0.0/10`,
    `192.0.0.0/24`, `198.18.0.0/15`, evaluated **after `ip.To4()`**.
    ⚠ `169.254.169.254` needs no separate rule: it is link-local. The previous list named "cloud
    metadata" as a fifth category (already inside a range it listed) and omitted `0.0.0.0/8`,
    CGNAT and IPv4-mapped IPv6.
  - **Host allow-list** on the request URL and re-checked in `CheckRedirect`.
    ⚠ It does **not** override the IP deny-list — otherwise it is a DNS-rebinding bypass.
  - `WithAllowedCIDRs([]string)` exempts specific **networks** from the IP deny-list — the
    working escape hatch. ⚠ `WithAllowedHosts` alone **cannot** serve that case: executed,
    `Dialer.Control` receives only `network, address` and `http://localhost:…` arrives as
    `127.0.0.1:…` — the hostname is gone.
  - `CheckRedirect` with **no** allow-list configured **follows** redirects, each hop subject to
    the IP deny-list. ⚠ The previous wording ("refuses a redirect whose host leaves the
    allowlist") means the empty default refuses **every** redirect, breaking http→https upgrades
    for every existing user.
- `WithUnrestrictedTransport()` makes today's behaviour explicit.
- ⚠ `WithURLExpr` + `WithHTTPClient` **without** `WithUnrestrictedTransport()` is refused —
  surfaced as a non-retryable error from `Do`, the existing `urlExprErr` pattern
  (`httpcall.go:128-130`), naming both options and the escape hatch. `NewHTTPCall` applies
  options in registration order over one `h.client` field, so either ordering silently drops
  something.
- `WithBaseURL` **unchanged**.

**Tests:**

1. `TestRestrictedDialerRefusesNonGlobalUnicast` — a table asserting on the **dialer control's
   refusal**, not a network result. ⚠ **Do not dial real link-local addresses in CI.**
   ⚠ **Rows that make it discriminating:** `169.254.169.254`, **`0.0.0.0`**,
   **`::ffff:127.0.0.1`**, **`100.64.0.1`**, `10.0.0.1`, `192.0.0.1`.
   **Falsifier:** *the last four fail against an implementation that blocks only
   `169.254.0.0/16` and `127.0.0.0/8`* — i.e. against every test the previous revision
   prescribed.
2. `TestCheckRedirectRefusesAHostOutsideTheAllowlist` — ⚠ assert `client.CheckRedirect(req, via)`
   **directly as a unit**, with a non-empty `via`.
   **Falsifier:** *it fails against a client whose `CheckRedirect` is nil.*
   ⚠ The previous revision's `httptest`-based version was refused at the **first hop** (httptest
   binds `127.0.0.1`) and never reached `CheckRedirect` at all — green against no
   `CheckRedirect` whatsoever.
3. `TestAllowedHostsUsesTheHOSTNAME` — allow-list `localhost` against a loopback `httptest`
   server, and assert an allow-listed hostname resolving to a **denied** IP is still refused.
   ⚠ The previous fixture used `"127.0.0.1"`, where the host string and the resolved IP are the
   **same token**, so it could not distinguish a host allow-list from an IP comparison — and
   therefore could not have revealed that `Dialer.Control` never sees a hostname.
4. `TestAllowedCIDRsOptsANetworkBackIn` — the escape hatch is reachable.
5. `TestURLExprFollowsSameHostRedirectByDefault` — ⚠ **the control.**
   **Falsifier:** *it fails against a `CheckRedirect` that refuses when the allow-list is empty.*
6. `TestBaseURLIsUnrestricted` — ⚠ **the ADR's load-bearing control.** A static `WithBaseURL`
   pointing at the `httptest` loopback server still works. Without it an implementer who
   over-applies the restriction breaks every existing user and the suite stays green.
7. `TestURLExprRejectsNonStringResult` — the type discipline that the withdrawn `expreval`
   routing would have relaxed.
   **Falsifier:** *it fails against an implementation returning `expreval.EvalString`'s coerced
   value.*
8. `TestURLExprWithCustomClientIsRefusedUnlessUnrestricted` — **both option orderings**.
   **Falsifier:** *it fails against an implementation that overwrites `h.client`, or that skips
   the restriction when a client was supplied.*

**Verify:** `go test -race -count=1 ./action/httpcall/...`

---

### Phase 6 — `transport/http/parity`

Parity cases asserting all three adapters agree on **413** for an oversize body and on the
blanked 403 and the correlation-id **shape**.

⚠ **Pin the fixture below 4 MiB and say why in a comment:** above `fiber.DefaultBodyLimit` the
fiber route group is never reached, so the client receives fasthttp's `text/plain`
`Request Entity Too Large` — no `ErrorBody`, no id, no log. Add a separate,
**explicitly-labelled fiber-only case** documenting that divergence, so it is a recorded
decision rather than a gap the fixture happens to avoid.
⚠ **A compressed-body parity case** asserting all three read **wire** bytes.
⚠ Parity **cannot** cover the optional-body admin route (ADR-0095 keeps admin routes out of
`Mount`) — that is phase 4 test 2's job, per adapter.

**Verify (two steps, so the agent can separate its own failures from phases 3–5's):**
`go test -race -count=1 ./transport/http/parity/...`, then
`go test -race -count=1 ./transport/http/...`

---

### Phase 7 — documents and the route-group caveats (controller)

- **`SECURITY.md`**:
  - the at-rest posture (D6). ⚠ **Derive the column list from
    `internal/persistence/store/migrations/{postgres,mysql,sqlite}` at implementation time** —
    do not copy it from the ADR. Evidence §4.4 has **12 columns across 7 tables**; the previous
    revision said two and an audit lens said six. Add the invariant test: any new column in
    those tables is either listed or explicitly justified.
  - the SSRF default and how to opt out (`WithAllowedCIDRs` first, `WithUnrestrictedTransport`
    last). ⚠ Do **not** write D3 and D6 as one posture — one is about data we store, the other
    about connections we make.
  - what a 400/403/413 body does and does not contain, and **which sinks receive what**: with
    `WithVerboseErrorLogging`, rejected request payloads reach the configured `slog.Logger`.
  - ⚠ **the covered set for redaction, and the five things it does not cover** — plus the
    sentence that `RedactVariables` is a *display* control: `action/httpcall` and
    `action/transform` receive the **unredacted** map.
  - ⚠ **which strings a non-admin caller can read**: `allowed_actions[].condition` and the
    embedded `definition` carry expression source, by decision.
  - ⚠ **runtime variable growth is not bounded**, and a consumer using `httpcall` with a large
    `WithMaxResponseSize` should size `WithMaxVariableBytes` accordingly.
  - ⚠ fiber above `DefaultBodyLimit`: rejections are emitted by the framework and are neither
    logged nor correlated by `wrkflw`.
- **`CHANGELOG.md` + `STABILITY.md`** — ⚠ **six breaks, not one.** (i) the 403 message becomes
  static; (ii) the 400 message changes shape; (iii) a correlation-id **field** appears, breaking
  `DisallowUnknownFields` decoders; (iv) a **new 413 status** appears on routes that previously
  returned 400 or 500, breaking exhaustive status switches; (v) the **eight exported endpoint
  functions** gain the response-policy parameter — a **source** break; (vi) `Logger` starts
  receiving 4xx records, changing log volume. ⚠ `ClassifyError`'s signature is deliberately
  **not** among them.
- Add the `SECURITY:` caveat to the **instance and task** route groups in all three adapters —
  today it exists at exactly three non-test sites, all on the **admin** group
  (`stdlib/groups.go:189`, `gin:204`, `fiber:209`), which implies the others are safe by
  omission.
- Close backlog **65, 98, 99, 104**, and **54 for `variables` only**; record **100/101** as
  posture-answered, mechanism-deferred.
- **Open the new backlog items** ADR-0186 Consequences names: the four uncovered snapshot fields
  + `ActionableView`'s two; runtime variable growth; the vendor-wrapper consolidation
  (`action/httpcall`, `action/transform`, `definition/model/validate/expr`).
- **Do not** close 51, 52, 53, 103, 124, 102, 32, 60, 91, 96, 106.
- `docs/plans/HANDOVER.md` + this plan's `▶ Progress`, per rule #10.

---

## 4. Enumerations, re-derived at the anchor

⚠ Every row was re-run in the working tree. **Bare `|` under `-E`** — `\|` in ERE is a *literal*
pipe, which is how the previous revision's "0 existing caps" evidence became a command that
returns 0 for **any** repository.

| what | value |
|---|---|
| decode sites | **39** — `stdlib` 13 `json.NewDecoder`, `gin` 13 `ShouldBindJSON`, `fiber` 13 `c.Bind().JSON`, `httpcore` 0; all in each package's `groups.go` |
| …**propagating** the decode error | **36** |
| …**discarding** it to `_` | **3** — `stdlib:238`, `gin:265`, `fiber:255`, all the optional-body `ResolveIncident` route |
| …already capped by us | **0** — `grep -rnE 'MaxBytesReader\|BodyLimit\|MaxBytesHandler\|LimitReader' transport/` exits 1. ⚠ After phase 4 this must return **26** (stdlib 13 + gin 13); fiber uses a `BodyRaw()` pre-check and will not match |
| `ClassifyError` arms | **6**, ordered: 404 `:28`, 403 `:32`, 409 `:34`, 400 `:36-50`, 422 `:51`, default 500 `:57` — becoming **7** with the 413 arm inserted before 400 |
| …echoing `err.Error()` today | **5** (all but 500) |
| sentinels in the 400 arm | **8**, across 5 `errors.Is` groups. ⚠ **Eight, not nine** — an earlier banner of the ADR said nine, inherited and restated without checking |
| …keeping `err.Error()` after D5 | **6** (4 provably value-free + `ErrOutcomeRequired` + `ErrInvalidInput`'s rendered message); **1** reshaped (`ErrInvalidOutcome`); the open remainder static |
| `ErrBadInput` wrap sites embedding a caller value | **2** — `admin_endpoints.go:30`, `dto.go:174` |
| validation strategies under `ErrInvalidInput` | **4** in-repo — `jsonschema`, `expr`, `avro`, `callback`; only `jsonschema` yields structured leaves. ⚠ The **class is open** (`validate.Register` is exported), so the allow-list keys on **kind** |
| response paths projecting `InstanceState.Variables` | **11** = 6 `mapInstance` + **3** direct `NewInstanceView` admin + 2 mapper-less. ⚠ Was stated as 8 |
| disclosure-bearing fields in the snapshot projection | **5** — `variables`, `tokens[].payload`, `incidents[].error`, `tasks[]`, `definition`. ⚠ Was stated as 1 |
| `ActionableTask` fields | **6**, and **none of them is `Vars`** |
| plaintext at-rest columns | **12 across 7 tables**, in **3** dialects. ⚠ Was stated as 2; an audit lens said "at least six" and was short by three tables |
| caller-supplied variable-map admission sites in `service` | **4** — `StartInstanceRequest.Vars`, `DeliverSignalRequest.Payload`, `DeliverMessageRequest.Payload`, `CompleteTaskRequest.Output` |
| direct `expr-lang/expr` importers (non-test, **by import line**) | **4** — `internal/expreval` (sanctioned) + **3** violators: `action/httpcall`, `action/transform`, `definition/model/validate/expr`. This delivery routes **none** of them |
| `SECURITY:` caveat sites | **3**, all admin-only |
| routes | **26** = 9 non-admin + 15 admin + 2 health; **no definition-deploy route** |

⚠⚠ **The B3 lineage rotted an enumeration in all THREE audit rounds, and in every case the
arithmetic was right — the failure was the grep's NET and the citation's ANCHOR.** Three of the
rows above (decode sites, read paths, plaintext columns) are corrections of counts that survived
a previous audit. **Assume one more is wrong**, and prefer the two machine-checked invariants
prescribed above (phase 3 test 8's call-site count, phase 3 test 3's sentinel-set pin) over any
number in this table.

---

## 5. Verification checklist

- [ ] **Rule-#9 re-audit** over this bundle — lenses including a **counting** lens and an
      **interaction** lens, detached worktrees at the bundle commit, step-0 presence check over
      **four** files. **Nothing below starts until this is checked.**
- [ ] Every phase's tests observed **RED before GREEN**, in the transcript.
- [ ] Every prescribed **falsifier** was demonstrated by mutation — break the production line,
      observe RED, restore from a `cp` backup (⚠ **never `git checkout <path>`**), `diff`.
      The falsifiers that matter most: phase 2 test 1 row 3 (`instanceLocation`), phase 3 test 6
      (shallow copy), phase 4 test 2 (`_ =` discard), phase 5 test 1 (two-range deny-list).
- [ ] `go test -race -coverprofile=cover.out ./... && scripts/coverage.sh cover.out`
      ≥ 85 % over hand-written code, hot paths and their failure branches first.
      Probe `docker info`; if the daemon is down, say so and label any container-free subset as
      the partial result it is.
- [ ] `go test ./...` from the repo root — no regressions.
- [ ] `golangci-lint run ./...` **repo-wide** (not package-scoped) clean.
- [ ] `go vet ./...`
- [ ] `go build ./examples/...` — ⚠ also run at the end of **phase 3**, not only here.
- [ ] Documents describe what shipped; per rule #11 expect implementation to correct the design
      and **amend the ADR in the same bundle**, with the measurement.
- [ ] Sweep the diff's comments for unexecuted claims and over-reaching quantifiers.
- [ ] `/code-review` — all findings fixed, folded via `--amend`.
- [ ] `/security-review` — all findings fixed, folded via `--amend`.
- [ ] `HANDOVER.md` rewritten in place; `▶ Progress` updated; memory topic file written and
      pointing at `HANDOVER.md`.

## 6. Commit shape

One feature bundle, one commit, amended (never stacked):

```
feat(service,transport,httpcall): bound untrusted input and stop leaking it back
```

carrying implementation, tests, the spec, ADR-0186, this plan, the evidence file and the
phase-7 docs.

# ADR-0186 standalone bundle — rule-#9 audit, FAILURE-MODES / GAPS / MISSING-DECISIONS lens

- **Date:** 2026-08-21
- **Bundle commit audited:** `32f4e3e55abc5898b50db5e00671cd8d86b2fac2` (detached worktree
  `…/scratchpad/a186-fail`). Step-0 presence check: **PASSED** — all three files present
  (`docs/specs/2026-08-21-untrusted-input-and-disclosure.md`,
  `docs/adr/0186-untrusted-input-and-disclosure-posture.md`,
  `docs/plans/2026-08-21-untrusted-input-and-disclosure.md`).
- **Scope:** ADR-0186 only. Identity material (ADR-0185, backlog 51/52/53/103/124) is
  explicitly out of scope and was not audited.
- **Prior rounds read first:** `reaudit-b3-adjudication.md`, then `audit-b3-adjudication.md`.
  Findings already folded by those rounds are not re-reported.
- **Constraint:** container-free only, no Docker. Every claim below was executed or read
  from source at the bundle commit; anything I could not execute is labelled
  `ASSUMPTION (unverified)` on my side.

Findings are appended in confirmation order. Ranked index is at the end.

---

## F1, CRITICAL — the correlation id cannot be produced by `ClassifyError`; its signature has no context and no config, and changing it is an unlisted breaking API change

**Claim attacked** (ADR-0186 D5, verbatim):
> "**Every error body gains a correlation id**, echoed in the log line, so an operator can
> join a blanked 403 to its cause. ⚠ The draft never said where the value comes from. It is
> the **OTel span id when a span is recording** — `CustomizeConfig` already carries
> `TracerProvider`/`MeterProvider` (`seam.go:30-31`), so no new dependency — falling back to
> a random hex id otherwise."

and Plan phase 4:
> "`ClassifyError`: … correlation id on every body (OTel span id when a span is recording,
> else a random hex id)."

**Evidence** (source, bundle commit):

- `transport/http/httpcore/errors.go:26` — `func ClassifyError(err error) (int, ErrorBody)`.
  **One parameter. No `context.Context`, no `CustomizeConfig[R]`.**
- The OTel span id is only reachable via `trace.SpanContextFromContext(ctx)`. It is **not**
  reachable from `cfg.TracerProvider`: a `TracerProvider` creates spans, it does not expose
  the span already on a request. So "CustomizeConfig already carries TracerProvider … so no
  new dependency" is a **non-sequitur** — the dependency needed is the request `ctx`, which
  `ClassifyError` does not receive.
- The span *is* on the request context in all three adapters
  (`stdlib/observe.go:21` `r.WithContext(ctx)`; `gin/observe.go:21`; `fiber/observe.go:42`
  `c.SetContext(enrichedCtx)`), so the id is reachable **at `writeErr`**, not at
  `ClassifyError`.
- `ClassifyError` is **exported module-root public API** and is advertised as a consumer seam
  in the module doc: `doc.go:66` — *"Shared logic lives in transport/http/httpcore: …
  ClassifyError (5xx redaction) …"*. Changing its signature to
  `ClassifyError(ctx, err)` or `ClassifyError(cfg, err)` breaks every consumer that calls it,
  which is precisely the audience `doc.go` points at it.
- The ADR's own breaking-change list (Consequences → Negative) names **only** the `ErrorBody`
  wire shape: *"`ErrorBody`'s message content and shape change for 400 and 403, and a
  correlation-id field is added. Consumers matching on `ErrorBody.Message` break."* The
  **source-level** break of `ClassifyError`'s signature is not listed anywhere in spec, ADR
  or plan.
- Minor collateral: the citation `seam.go:30-31` is off by one — line 30 is `Logger`, 31 is
  `TracerProvider`, 32 is `MeterProvider` (`seam.go:29-32`). The spec's header claims *"Every
  citation below was re-derived there."*

**Verdict: CONFIRMED.** The mechanism as specified is not constructible where the plan puts
it, and the API break it forces is unlisted.

**Proposed fix:**
1. Decide explicitly: either (a) `ClassifyError` keeps its signature and gains a sibling
   `ClassifyErrorContext(ctx context.Context, err error) (int, ErrorBody)` with
   `ClassifyError` delegating with `context.Background()` (no break, deprecation path), or
   (b) the correlation id is minted in each adapter's `writeErr` and assigned onto the
   returned `ErrorBody` — `ClassifyError` never sees it. (b) is the smaller change and keeps
   `httpcore`'s pure-function discipline.
2. Whichever is chosen, add it to the ADR's breaking-change list *and* to plan phase 9's
   CHANGELOG bullet, and say which of the two the implementer must build.
3. Delete or correct the "TracerProvider … so no new dependency" sentence — it does not
   support the conclusion.

---

## F2, CRITICAL — no phase builds the log half of the correlation-id join, and 400/403 are not logged at all today

**Claim attacked** (ADR-0186 D5, verbatim):

> | 403 | static `"not authorized"` — raw error logged at the transport seam |

> "**Every error body gains a correlation id**, echoed in the log line, so an operator can
> join a blanked 403 to its cause."

and Plan phase 4 test 6:
> "`TestCorrelationIDInBodyMatchesTheLogRecord` — the entire justification for blanking 403
> is that an operator can join the two. **Fails today:** no id exists."

**Evidence** (source, bundle commit):

- The **only** non-test callers of `ClassifyError` are the three adapters' `writeErr`:
  `transport/http/stdlib/write.go:31`, `transport/http/gin/write.go:12`,
  `transport/http/fiber/write.go:12`. (`grep -rn --include="*.go" ClassifyError . | grep -v _test.go`
  — the remaining hits are comments and the definition.)
- All three log **only for 5xx**:
  `stdlib/write.go:32-34` `if status >= 500 { cfg.Logger.ErrorContext(...) }`; identical
  guards at `gin/write.go:13` and `fiber/write.go:13`. **A 403 or 400 produces no log record
  at all today.**
- `grep -rn --include="*.go" "Logger" transport/http/httpcore/ | grep -v _test.go` → the
  package **never logs**: `Logger` appears only as a config field (`seam.go:29-30,43,56-57`),
  an option (`:84-86`) and a hand-off to `observability.WithLogger` (`observability.go:42-43`).
  **`httpcore` has no logging seam.**
- Plan phase 4 is scoped to `transport/http/httpcore` and prescribes
  `TestCorrelationIDInBodyMatchesTheLogRecord` **there** — a package that cannot produce a
  log record. Plan phase 5 (`stdlib`/`gin`/`fiber`) is scoped to *"caps the body at all 13
  decode sites"* and to `TestOversizedBodyReturns413`; it says nothing about `writeErr`,
  nothing about widening the `status >= 500` guard, and nothing about emitting the
  correlation id.

**Verdict: CONFIRMED — and it is zombie scope of exactly the ADR-0162 shape the bundle's own
plan (phase 3 note) says it is guarding against.** The ADR promises a join; no phase builds
the side of the join that lives in the log. The prescribed test is in the wrong package and,
as written, can only assert against a stubbed logger the phase does not have.

**Proposed fix:**
- Add an explicit bullet to **phase 5** (all three adapter agents): widen `writeErr`'s guard
  so 400 and 403 also emit `cfg.Logger.ErrorContext(ctx, "<adapter>: client error", "err",
  err, "correlation_id", body.CorrelationID)`, and state the falsifier
  (*it fails against an implementation that keeps `status >= 500`*).
- Move `TestCorrelationIDInBodyMatchesTheLogRecord` out of phase 4 into phase 5, once per
  adapter, asserting against a `slog` handler capturing records — or, if the id is minted in
  `httpcore`, split it into (a) a httpcore test that the id is present and non-empty and
  (b) a per-adapter test that the same id reaches the log.
- Decide and record the log **level**: `ErrorContext` for a routine 400 will flood an
  operator's error stream; `WarnContext`/`InfoContext` is the likelier choice and the ADR
  currently implies `Error`.

---
## F3, CRITICAL — the 400 allow-list's DENY half is specified nowhere and tested nowhere; only the two allow-list arms are built

**Claim attacked** (ADR-0186 D5, verbatim):

> | `avro`, `callback`, and the other seven sentinels | static `"invalid input"` + the correlation id; raw error to `CustomizeConfig.Logger` |

> "⚠ The draft's Consequences said *"the two 4xx arms proven to leak stop leaking"*. That was
> true for one strategy of four and one sentinel group of five. The allow-list is what makes
> it true, and the tests must cover the **uncovered** cases — a test over `jsonschema` alone
> has exactly the fix's own coverage and can never reveal the gap."

**Evidence:**

- `transport/http/httpcore/errors.go:36-50` renders **all eight** 400 sentinels through one
  shared expression: `return http.StatusBadRequest, ErrorBody{Error: "bad_request", Message: err.Error()}`.
  Making seven of them static therefore requires **splitting the 400 arm** inside
  `ClassifyError` — one arm for `validation.ErrInvalidInput` (pass through what phase 2
  rendered) and one for the rest (static). No document says this.
- Plan **phase 2** (`runtime/validation`) covers the two allow-list branches only:
  test 1 `jsonschema`, test 2 `expr`, test 3 `callback` (all under `ErrInvalidInput`),
  test 4 `errors.As` survival. **`avro` is named in the ADR table and has no prescribed
  test.**
- Plan **phase 4** (`httpcore`) bullet for the classifier reads, in full:
  *"`ClassifyError`: **413 arm placed BEFORE the 400 arm** …; 403 static; 400 renders what
  phase 2 gives it; correlation id on every body"*. "renders what phase 2 gives it" is the
  only instruction, and phase 2 never sees `kernel.ErrBadCursor`,
  `kernel.ErrBadArmedTimerCursor`, `httpcore.ErrBadInput`, `engine.ErrInvalidOutcome`,
  `engine.ErrOutcomeRequired`, `engine.ErrEmptyTriggerKey` or `engine.ErrEmptyReassignTarget`
  — none of those originate in `runtime/validation`.
- Plan phase 4's six prescribed tests: 413-vs-400, predicate-source, view-copies, redaction
  under mapper, snapshot/actionable redaction, correlation id. **Zero** assert the static
  rendering of any of the seven non-validation sentinels.

**Verdict: CONFIRMED — zombie scope.** The ADR states a policy ("deny-by-default text with an
allow-list") and the plan builds only the allow-list. An implementer following the plan
literally ships a 400 arm that still echoes `err.Error()` for the seven, and every prescribed
test is green. The ADR's own warning about a `jsonschema`-only test is reproduced one level
up: the *plan* has exactly the fix's own coverage.

**Proposed fix:**
- Give phase 4 an explicit instruction: split the 400 arm into
  `case errors.Is(err, validation.ErrInvalidInput): Message: err.Error()` (phase 2 already
  made it client-safe) and a second case listing the other seven with
  `Message: "invalid input"`. Name the ordering constraint (`ErrInvalidInput` first) and its
  reason, as D1 does for 413-before-400.
- Add a phase-4 table test with **one row per non-validation sentinel** (7 rows) asserting
  `body.Message == "invalid input"`, plus a row where an error wraps **both** `ErrBadInput`
  and `validation.ErrInvalidInput` (see F6) — falsifier: *it fails against an implementation
  that leaves the single shared `err.Error()` render in place.*
- Add the missing `avro` row to phase 2.

---

## F4, CRITICAL — the static-400 default refuses the useful case: it destroys the actionable messages three prior ADRs deliberately added, and four of the seven sentinels leak nothing at all

**Claim attacked** (ADR-0186 D5, verbatim):

> "Blanking 400 wholesale was rejected — a consumer needs to know *which field* failed *which
> constraint* to fix their request."

and the table row that then blanks seven of eight sentinels to `"invalid input"`.

**Evidence — what each of the seven actually renders today, read from source:**

| sentinel | message format | caller value echoed? |
|---|---|---|
| `kernel.ErrBadCursor` | `lister.go:66,69,77,90` — `"…: %w"` (base64 error), `": not an instance cursor"`, `": cursor carries no instance identity"`, `": cursor carries no start time"` | **no** |
| `kernel.ErrBadArmedTimerCursor` | `armed_timer_paging.go:89,92,99` — same shapes | **no** |
| `engine.ErrEmptyTriggerKey` | `trigger_validate.go:177` — `"%w: %T.%s"` — Go type + field name of the empty key | **no** (names the field the caller must fill) |
| `engine.ErrEmptyReassignTarget` | `step.go:163` — `"%w: %T.To"` | **no** |
| `engine.ErrOutcomeRequired` | `step_triggers.go:932` — `"%w: node %q"` | no (definition node id) |
| `engine.ErrInvalidOutcome` | `step_triggers.go:934` — `"%w: node %q outcome %q"` | **yes** — the submitted outcome |
| `httpcore.ErrBadInput` | `validate.go:34` wraps go-playground/validator errors (field name + failing tag, JSON names by `RegisterTagNameFunc`); plus 36 adapter decode wraps and `admin_endpoints.go:30`, `endpoints.go:33`, `dto.go:174` | field/tag names; `"unknown status %q"` echoes a caller value |

- **Four of the seven echo nothing a caller supplied.** Blanking them is pure information
  loss with zero disclosure benefit.
- **The in-code rationale for three of them is the opposite of this decision**, written in
  the very switch ADR-0186 edits:
  - `errors.go:38-41` (ADR-0146): *"Both outcome sentinels describe a completion payload the
    caller can correct … Without these arms they fall to the 500 default, **which hides an
    actionable 4xx behind an empty body**."*
  - `errors.go:43-46` (ADR-0152): *"An empty trigger identity key is **a malformed request the
    caller can fix by supplying the id**."*
  - `errors.go:47-49` (ADR-0183): *"a required field the caller omitted **and can supply**."*
  A static `"invalid input"` re-creates precisely the harm those comments name — the caller
  learns nothing but the status code. **ADR-0186 neither cites nor amends ADR-0146/0152/0183,
  and its Relates-to list does not include them.**
- **`ErrBadInput` is the highest-volume 400 by a wide margin** — 36 adapter decode sites, the
  whole `httpcore.Validate` DTO layer (`validate.go:32-36`) which every POST/PUT body passes
  through, and three ad-hoc wraps. `validate.go:11-14`'s godoc says the tag-name mapping
  exists *"so 400 messages describe the payload the client actually sent"*. D5 makes that
  entire investment unreachable: **every malformed DTO on all 26 routes becomes
  `"invalid input"`**. The ADR's table hides this behind the phrase "and the other seven
  sentinels" and never notices which sentinel it is.

**Verdict: CONFIRMED.** This is the ADR-0165 failure shape the brief names: the decision
reasons exhaustively about what the body must *not* say and never asks what it must still do.
The bundle's own justification sentence ("a consumer needs to know which field failed which
constraint") is falsified by its own table for the sentinel that carries field-and-constraint
information for every route.

**Proposed fix (pick one and record it):**
1. **Narrow the deny-list to what actually leaks.** Static text for `ErrInvalidOutcome` (the
   only non-validation sentinel echoing a caller value — and even then, render
   `"node %q: outcome not declared"` without the value) and for `ErrBadInput` wraps that
   embed a caller value (`admin_endpoints.go:30`'s `unknown status %q`). Keep `err.Error()`
   for cursors, empty-key, empty-reassign-target and validator-tag failures, whose messages
   are provably value-free. This preserves ADR-0146/0152/0183 and still closes the leak.
2. Or, if the blanket default is kept: **amend ADR-0146, ADR-0152 and ADR-0183** in this
   bundle (rule #11 — the ADR that promises behaviour nobody keeps is the zombie), add them
   to Relates-to, and state in Consequences → Negative that outcome/trigger-key/reassign
   diagnostics move from the response body to the operator's log.
3. Either way: state in the ADR that a `structured` rendering path is available for
   `ErrBadInput` (validator errors are already typed `validator.ValidationErrors` and carry
   `Field()`/`Tag()` with no value) so the highest-volume case is not collateral damage.

---

## F5, CRITICAL — "all 39 decode sites wrap in ErrBadInput" is false (36 do); the three that do not DISCARD the decode error, so an oversize body there is silently ignored, not 413

**Claims attacked** (verbatim):

- ADR-0186 D1: *"Every one of the 39 decode sites *already* double-wraps —
  `writeErr(cfg, gc, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))`
  (`transport/http/gin/groups.go:33-35`, and the stdlib/fiber equivalents)"*
- Spec §2 corrections table: *"All 39 decode sites already wrap in `httpcore.ErrBadInput`, and
  `ClassifyError` is an **ordered** switch"*
- Plan phase 5: *"Each caps the body at all **13** decode sites in its `groups.go`"* … *"Every
  decode site today wraps in `fmt.Errorf("%w: %w", httpcore.ErrBadInput, err)`"*

**Evidence (executed):**

`grep -n` for each adapter's decode idiom returns **13** sites; `grep -n "ErrBadInput"` in the
same file returns **12** wrap sites. The discrepancy is one site per adapter, and it is the
same route in all three:

- `transport/http/stdlib/groups.go:238` — `_ = json.NewDecoder(req.Body).Decode(&in) // body is optional`
- `transport/http/gin/groups.go:265` — `_ = gc.ShouldBindJSON(&in)`
- `transport/http/fiber/groups.go:255` — `_ = c.Bind().JSON(&in)`

The route is `POST /admin/instances/{id}/incidents/{incidentID}/resolve`
(`stdlib/groups.go:233-245`), whose body is deliberately optional
(`httpcore.ResolveIncidentInput`). So the true split is **36 wrapping / 3 discarding**, not
39/39.

**Why it is a failure mode, not just a count:**

Under D1 the adapter wraps the body in `http.MaxBytesReader` (stdlib/gin) or pre-checks
`len(c.Body())` (fiber). At this route the resulting oversize error is assigned to `_`. The
handler then proceeds with a **zero-value `ResolveIncidentInput`** and resolves the incident
with default disposition/notes, returning **2xx**. The cap is installed and its violation is
**silently swallowed** — the single worst outcome for a security control, and the one shape
the bundle's own D1 section is otherwise careful about ("an implementer who follows the
conversion instruction literally … ships 400"). Here they ship **200**.

Fiber's `len(c.Body())` pre-check variant is different but no better: since the plan tells the
fiber agent to pre-check "before `c.Bind().JSON`", the pre-check at this site must decide
whether to reject or to ignore — and the plan does not say.

`TestOversizedBodyReturns413` (phase 5) names no route. Run against any of the 36 wrapping
sites it is green while this route stays broken.

**Verdict: CONFIRMED.** The quantifier is false in all three documents, and the exception is
exactly the site where D1's mechanism fails open.

**Proposed fix:**
- Correct the count everywhere to **36 wrapping / 3 discarding** (spec §2, ADR D1, plan phase 5
  and plan §4's enumeration table).
- Give phase 5 an explicit instruction for the optional-body site: distinguish
  *"body absent / EOF"* (keep ignoring) from *"body present but oversize / malformed"*
  (return bare `ErrBodyTooLarge`). With `MaxBytesReader` this is
  `errors.As(err, new(*http.MaxBytesError))` before the `_ =` discard.
- Pin the phase-5 test to the **admin resolve-incident route** in addition to a normal one,
  and state the falsifier: *it fails against an implementation that wraps the body but leaves
  the `_ =` discard.*

---
## F6, CRITICAL — `TestActionableViewRedactsTaskVars` CANNOT FAIL: `ActionableView` emits no task vars at all, and the ADR premise it rests on is false (EXECUTED)

**Claims attacked** (verbatim):

- ADR-0186 D4: *"and `GetActionableView` (`endpoints.go:72`) renders open human tasks, whose
  `HumanTask.Vars` is the per-task variable snapshot."*
- Plan phase 4 test 5: *"`TestSnapshotEndpointRedactsVariables` and
  `TestActionableViewRedactsTaskVars` — ⚠ **the controls that decide D4's placement.** …
  `GetActionableView` (`:72`) renders task vars. … **Fails today:** no redaction exists — and
  each **fails against a fix confined to `mapInstance`**, which is the whole point."*

**Evidence — EXECUTED** (throwaway probe `runtime/view/probe_actionable_test.go`, since deleted):

```
ACTIONABLE JSON = {"instance_id":"i1","status":"running","open_tasks":[{"task_id":"tk",
  "node_id":"gw","state":"unclaimed","allowed_actions":[{"flow_id":"go-e","target":"e",
  "condition":"vars.internalApprovalLimit > 5000"}]}]}
```

The fixture set `HumanTask.Vars = {"ssn":"123-45-6789"}`. **The string `123-45-6789` does not
appear in the output.** Source confirms why: `runtime/view/instance_actionable.go:25-41`
declares `ActionableTask` with exactly six fields — `TaskID`, `NodeID`, `State`, `Claim`,
`Candidates`, `AllowedActions`. **There is no `Vars` field**, and `NewActionableView`
(`:62-`) never reads `t.Vars`.

**Verdict: CONFIRMED — this is the repo's own recurring failure, verbatim.** A test asserting
that a secret is absent from a projection that never carried it is green today, green after
the fix, and green against *no* fix. It is the `assert.Empty(state.Boundaries)`-against-a-
definition-with-no-boundary-node shape that CLAUDE.md's Premise Discipline calls out by name,
and the plan asserts a falsifier (*"Fails today"*, *"fails against a fix confined to
`mapInstance`"*) that is untrue for this row. It is also billed as one of *"the controls that
decide D4's placement"* — so a false control is load-bearing on a decision.

**Proposed fix:**
- Delete `TestActionableViewRedactsTaskVars`, and delete D4's `HumanTask.Vars` sentence from
  the ADR — it is a premise that was never executed.
- If task-variable disclosure is genuinely in scope, name the projection that *does* carry it:
  `service.ProcessInstance`'s `tasks[]` → `taskJSON` (`service/instance.go`), which carries
  `Candidates []authz.Actor` (actor **attribute maps**), `Claim` and `Completion` — reached
  by `GetInstanceSnapshot`, not by `GetActionableView`.
- Replace the deleted control with one that can fail: assert against
  `GetInstanceSnapshot`'s marshalled body, whose fixture must place the secret in a field the
  projection actually emits.

---

## F7, CRITICAL — the redaction hook covers ONE of at least six variable-bearing fields in the snapshot response; `func(map[string]any) map[string]any` cannot address the rest, and Consequences claims coverage it does not have

**Claim attacked** (ADR-0186 Consequences → Positive, verbatim):

> "Redaction is applied at the `ProcessInstance` → response boundary, so it covers the two
> mapper-less non-admin read endpoints (`GetInstanceSnapshot`, `GetActionableView`) as well as
> the six that go through `mapInstance`. ⚠ The draft covered only the latter, and its *"cannot
> be bypassed"* sentence was true of the mapper and false of the endpoints."

and D4's hook signature: `httpcore.CustomizeConfig.RedactVariables func(map[string]any) map[string]any`.

**Evidence — `service.ProcessInstance`'s wire projection, source-read at `service/instance.go`:**

`GetInstanceSnapshot` (`endpoints.go:60-66`, **non-admin**) returns `pi` directly. Its JSON is
produced by the **unexported** `instanceJSON` (`service/instance.go:157-183`, assigned at
`:330-360`), which carries:

| field | payload | covered by `RedactVariables`? |
|---|---|---|
| `variables` | `st.Variables` | **yes** — the only one |
| `tokens[].payload` | `map[string]any` per token (`tokenJSON`, `:146-154`) | **no** |
| `tasks[].candidates[]` | `[]authz.Actor` — carries `Attributes` (the ABAC attribute map) | **no** |
| `tasks[].claim` / `tasks[].completion` | `*humantask.Claim` / `*Completion` — each embeds a full `authz.Actor` plus a free-text `Note` (`humantask/humantask.go:70-79`) | **no** |
| `incidents[].error` | the raw error string of a failed service action (`incidentJSON`, `:198-221`) — for an `httpcall` node this is a transport error carrying the target URL and query string | **no** |
| `definition` | the **entire `model.ProcessDefinition`**, embedded verbatim (ADR-0144), including every sequence-flow `Condition` **expression source** and every validation schema | **no** |

Two structural consequences the bundle does not address:

1. **`instanceJSON` is unexported and built by an unexported function in `service`.** A
   `httpcore` helper "every read path calls" cannot reach into it; it can only redact
   `pi.State().Variables` *before* the marshal, which is not where the other five live. The
   ADR's phrase "a helper every read path calls" presupposes a single shared shape; there are
   **three** — `mapInstance(mapper, pi.State())` returns `any` from an
   `engine.InstanceState`, `GetInstanceSnapshot` returns `service.ProcessInstance`, and
   `GetActionableView` returns `view.ActionableView`. **No common type, no common boundary.**
2. **The hook's signature is instance-blind and kind-blind** (the brief's question 5, answered):
   `func(map[string]any) map[string]any` receives a bare map. It cannot tell *which instance*,
   *which definition*, *which route*, or *whether these are instance variables, a token
   payload, or a task snapshot*. A consumer who needs "redact `ssn` only for definition
   `kyc-v3`", or "redact task vars but not instance vars", cannot express it. Every other
   customisation seam in this package is either generic over the router or receives the full
   `engine.InstanceState` (`InstanceMapper func(engine.InstanceState) any`, `seam.go:28`) —
   this one is strictly weaker than the seam it is supposed to sit above.

**Verdict: CONFIRMED.** The Consequences sentence repeats the exact error it is written to
correct: the draft claimed closure over the mapper and was false about the endpoints; the
revision claims closure over the endpoints and is false about the fields.

**Proposed fix:**
1. **Widen the signature** to `RedactVariables func(ctx context.Context, st engine.InstanceState, vars map[string]any) map[string]any`, or at minimum
   `func(instanceID string, kind VariableScope, vars map[string]any) map[string]any` with
   `VariableScope ∈ {InstanceVars, TokenPayload, TaskVars}`. This is the only version that can
   express per-definition or per-scope policy, and it costs nothing to add now — after v0.1.0
   it is a breaking change.
2. **Name the covered set explicitly in Consequences instead of claiming closure** (the ADR
   itself demands this of D4 — *"The Consequences must name the covered set rather than claim
   closure"* — and then does not do it). State plainly: *covered — `variables` on all eight
   read endpoints; NOT covered — `tokens[].payload`, `incidents[].error`,
   `tasks[].candidates/claim/completion`, and the embedded `definition`.*
3. Decide whether `GetInstanceSnapshot` must redact the other five. If yes, the work lands in
   `service` (phase 7 already touches it) — add a phase task and stop describing it as a
   `httpcore` helper. If no, say so and record the residual disclosure as an accepted risk in
   `SECURITY.md` (phase 9).

---

## F8, MAJOR — a non-admin 200 route hands out the predicate source that D5 goes to lengths to remove from the 403 body (EXECUTED)

**Claim attacked** (ADR-0186 Context §5 / D5): the whole rationale that a predicate source is
sensitive — *"carrying whatever process-variable and actor-attribute names the deployment's
policy names"* — and the resulting decision to blank 403 and to stop `expr.go:64,68` echoing
`v.source[i]`.

**Evidence — EXECUTED** (same probe as F6):

```
"allowed_actions":[{"flow_id":"go-e","target":"e","condition":"vars.internalApprovalLimit > 5000"}]
```

`runtime/view/instance_actionable.go:12-21` declares `NextAction.Condition` with the godoc
*"Condition is the routing expression guarding this flow"*, and it is emitted on
`GET …/instances/{id}/actionable` — a **non-admin** route (`endpoints.go:72`). The identical
class of string is also embedded wholesale in `GetInstanceSnapshot`'s `definition` field.

**Verdict: CONFIRMED.** The ADR spends two sections establishing that expression source is a
disclosure worth a breaking wire change, then leaves two success-path routes publishing it by
design. This is not necessarily wrong — an actionable view arguably *needs* the condition —
but the bundle never notices the tension, so the reader cannot tell whether it is a decision
or an oversight. Under D5's own reasoning, blanking the 403 while `GET /actionable` serves the
same strings makes the 403 change close to cosmetic for an authenticated caller.

**Proposed fix:** add a paragraph to ADR-0186 D5 (or D4) stating the position explicitly —
either *"routing conditions and the embedded definition are author-supplied metadata a caller
is entitled to see; the 403 fix is about the ABAC policy surface, which is not in either
projection"*, or scope the flow condition behind the same `RedactVariables`/`SECURITY:` regime.
Whichever, it must be **written**, and `SECURITY.md` (phase 9) must say which strings a
non-admin caller can read.

---
## F9, CRITICAL — `WithAllowedHosts` is not implementable in the prescribed `net.Dialer.Control` hook: the hook never sees a hostname (EXECUTED). The fine-grained escape hatch does not work, leaving only the wholesale one

**Claim attacked** (ADR-0186 D3, verbatim):

> "the default transport refuses loopback, link-local (`169.254.0.0/16`, `fe80::/10`),
> RFC1918/ULA and cloud metadata addresses via a `net.Dialer.Control` hook, and
> `CheckRedirect` refuses a redirect whose host leaves the allowlist."
> "`WithAllowedHosts([]string)` opts specific hosts back in."

**Evidence — EXECUTED** (`/tmp/dialprobe`, Go 1.25, `net.Dialer{Control: …}` + `httptest`):

```
--- GET http://localhost:51179/
CONTROL network="tcp6" address="[::1]:51179"
CONTROL network="tcp4" address="127.0.0.1:51179"
status: 200
--- GET http://127.0.0.1:51179/
CONTROL network="tcp4" address="127.0.0.1:51179"
status: 200
```

`Dialer.Control` is invoked with the **resolved IP:port** and nothing else. The request to
`http://localhost:…` reaches the hook as `[::1]:…` / `127.0.0.1:…` — **the hostname
`localhost` is gone**. `Control`'s signature is `func(network, address string, c syscall.RawConn) error`;
there is no host parameter and no context.

Consequences the bundle does not address:

- A **host** allowlist cannot be evaluated where the ADR puts the check. Implementing
  `WithAllowedHosts` requires either `Dialer.ControlContext` plus a custom `DialContext`
  wrapper that stashes the intended host in the dial context, or resolving and comparing in a
  `DialContext` wrapper — neither of which the ADR or the plan describes.
- The consumer the ADR's own Consequences names — *"A consumer whose `httpcall` node
  legitimately targets an internal `10.x` address from a variable-derived URL must now say so
  explicitly"* — has, in practice, only `WithUnrestrictedTransport()`: turn the whole
  protection off. **That is exactly the brief's "guard refuses the useful case" shape**, and it
  is worse than the status quo, because a consumer who needs one internal host is pushed to
  disable SSRF protection for all URLs the action can produce.
- Even done correctly, a host allowlist and an IP denylist answer different questions. An
  allowlisted host that resolves to `169.254.169.254` (DNS rebinding) must still be refused;
  the ADR does not say which of the two wins.

**Verdict: CONFIRMED.**

**Proposed fix:**
1. State the real mechanism: a `DialContext` wrapper that (a) records the requested host,
   (b) resolves it, (c) applies the IP denylist to **every** resolved address, and (d) consults
   `AllowedHosts` on the *hostname*. Name `ControlContext` if that is the chosen shape.
2. Decide and record precedence: **the IP denylist is not overridable by `AllowedHosts`** (an
   allowlisted host that resolves internal is still refused) — otherwise `WithAllowedHosts`
   becomes a rebinding bypass. If the opposite is intended, say so and say why.
3. Add `WithAllowedCIDRs([]string)` (or make `WithAllowedHosts` accept `host` *and* CIDR
   forms) so the "one internal `10.x` service" case has an answer short of
   `WithUnrestrictedTransport()`.
4. Add a prescribed test that a *hostname* in `AllowedHosts` resolving to a denied IP is still
   refused — falsifier: *it fails against an `AllowedHosts` check placed in `Dialer.Control`,
   which cannot see the hostname at all.*

---

## F10, CRITICAL — D3 silently collides with the existing `WithHTTPClient` option; the bundle flags this exact collision class for `runtime` and misses it here

**Claim attacked** (ADR-0186 D3): *"When a URL is **expression-derived**, the default transport
refuses … via a `net.Dialer.Control` hook"* — and, by contrast, ADR-0186 D2's careful
treatment: *"⚠ `runtime.WithMaxEvalElements` collides with two existing options. … Three
options writing one field is last-writer-wins, silently. The option must **compose** … or
**refuse** … it must not quietly overwrite."*

**Evidence:**

- `action/httpcall/httpcall.go:151-153` — `WithHTTPClient(c *http.Client) Option` assigns
  `h.client`, documented as *"e.g. an otel-instrumented one"*. `httpCall.client` is a **single
  field** (`:97`).
- `NewHTTPCall` (`:207-220`) applies options **in registration order** over a default
  `&http.Client{Timeout: 30 * time.Second}`. Any "install a restricted transport when
  `urlExprProg != nil`" logic written as an option is therefore order-dependent:
  `NewHTTPCall(WithURLExpr(e), WithHTTPClient(c))` and `NewHTTPCall(WithHTTPClient(c), WithURLExpr(e))`
  give different results, and one of them silently drops the protection.
- If instead the restriction is applied after the option loop, it must either **replace** the
  consumer's client (losing their otel instrumentation, retries, proxy and TLS config) or
  **wrap** its `Transport` — which is not possible in general, because
  `otelhttp.NewTransport(...)` is an opaque `http.RoundTripper`, not an `*http.Transport`
  whose `DialContext` can be reached.
- Neither the ADR, the spec's §5 pairwise-interaction table, nor plan phase 6 mentions
  `WithHTTPClient` at all. The spec's table has no D3 × existing-options row.

**Verdict: CONFIRMED.** The bundle applies its own "must compose or refuse, never overwrite"
rule to `runtime` and not to `action/httpcall`, where the collision is identical in shape and
the consequence is a **security** control being silently disarmed rather than a knob being
silently ignored.

**Proposed fix:**
- Add a D3 paragraph deciding the combination. Recommended: `NewHTTPCall` **refuses at
  construction** (deferred to `Do` as a non-retryable error, matching `urlExprErr`'s existing
  pattern at `:128-130`) when `WithURLExpr` and `WithHTTPClient` are both set without
  `WithUnrestrictedTransport()` — with the error naming both options and the escape hatch. A
  consumer who wants their own client takes responsibility explicitly.
- Add the missing **D3 × `WithHTTPClient`** row to spec §5.
- Add plan phase-6 test `TestURLExprWithCustomClientIsRefusedUnlessUnrestricted`, falsifier:
  *it fails against an implementation that overwrites `h.client` or that skips the restriction
  when a client was supplied.*

---

## F11, MAJOR — the default `CheckRedirect` behaviour is undefined when no allowlist is configured (the default case), and no test covers a legitimate redirect

**Claim attacked** (ADR-0186 D3): *"`CheckRedirect` refuses a redirect whose host leaves the
allowlist."*

**Evidence:** `WithAllowedHosts` is opt-in, so the **default** allowlist is empty. Under the
sentence as written, *every* host "leaves" an empty allowlist ⇒ **all redirects are refused by
default**. The ADR does not say this, and it is a large behaviour change: `http.Client` follows
up to 10 redirects today (`httpcall.go:209` default client sets only `Timeout`), and the
commonest redirects on any real endpoint are `http`→`https` upgrades and trailing-slash
normalisation — both to the *same* host.

The alternative reading — an empty allowlist means "no host restriction, the dialer still
guards every hop" — is the safe and useful one, but it is a different rule from the one
written.

Plan phase 6 prescribes `TestURLExprRefusesRedirectToLoopback` and `TestAllowedHostsOptsBackIn`
and **no test that a legitimate same-host redirect still succeeds**. So whichever reading an
implementer picks, the suite is green.

**Verdict: CONFIRMED (missing decision).**

**Proposed fix:** state the rule as *"with no `AllowedHosts` configured, redirects are followed
and each hop is subject to the IP denylist at dial time; with `AllowedHosts` configured, a hop
to a host outside it is refused"* — or the opposite, explicitly. Add
`TestURLExprFollowsSameHostRedirect` as the control, falsifier: *it fails against a
`CheckRedirect` that refuses when the allowlist is empty.*

---

## F12, MAJOR — `ErrBodyTooLarge` already exists in the public API of `action/httpcall`; D1 introduces a second one with the same name in the same delivery

**Claim attacked** (ADR-0186 D1): *"a new `httpcore.ErrBodyTooLarge` sentinel"*.

**Evidence:** `action/httpcall/httpcall.go:92-94` —
`var ErrBodyTooLarge = errors.New("workflow-httpcall: body exceeds max size")`, exported, and
documented in the package doc at `:31-37` (*"A body exceeding the cap fails with a
non-retryable [ErrBodyTooLarge]"*). It bounds the **response** body and any buffered request
body, default 10 MiB (`:90`).

This delivery touches **both** packages (phase 4 `httpcore`, phase 6 `action/httpcall`) and
phase 9 writes a CHANGELOG entry and a `SECURITY.md` section that will name `ErrBodyTooLarge`
without a package qualifier. Two identically-named exported sentinels with different meanings
(request-in vs response-in), different owners, and no `errors.Is` relationship, introduced in
one commit, is a documentation and support hazard — and the bundle never mentions the
existing one, which also means D1's *"no cap anywhere"* framing is stated without noticing that
the repo already has a body cap with this exact name and a chosen default (10 MiB) that could
have informed D1's 1 MiB judgement call.

**Verdict: CONFIRMED.**

**Proposed fix:** rename the new sentinel `httpcore.ErrRequestBodyTooLarge` (or reuse a
distinct string, e.g. `"workflow-httpcore: request body exceeds max size"`), and add one line
to D1 acknowledging `httpcall`'s existing 10 MiB response cap as prior art for the default.

---

## F13, CRITICAL — two of phase 6's four prescribed tests cannot discriminate: the redirect test never reaches `CheckRedirect`, and the allowlist test cannot tell a host allowlist from an IP allowlist

**Claims attacked** (plan phase 6, verbatim):

> 2. `TestURLExprRefusesRedirectToLoopback` — an `httptest` server that 302s to `127.0.0.1`.
>    **Fails today:** `http.Client` follows by default.
> 4. `TestAllowedHostsOptsBackIn` — the escape hatch is reachable.

**Evidence:**

- **Test 2 is unreachable.** `httptest.NewServer` binds `127.0.0.1`. Under D3 the restricted
  dialer refuses **loopback**, so the client is refused at the *first* hop — connecting to the
  `httptest` server itself — and never issues the 302, never invokes `CheckRedirect`. The test
  goes green while asserting nothing about redirect handling. It would also be green against an
  implementation with **no `CheckRedirect` at all**, which is precisely the code path it exists
  to prove. (Its stated falsifier, *"`http.Client` follows by default"*, describes today's
  unrestricted client and stops being the reason once the dialer exists.)
- **Test 4 cannot discriminate.** Any `httptest`-based allowlist fixture will list either
  `"127.0.0.1"` or `"localhost"`. With `"127.0.0.1"` the host string and the resolved IP are
  the **same token**, so the test passes identically against a correct host allowlist and
  against the (unimplementable — see F9) IP-string comparison inside `Dialer.Control`. The
  fixture is structurally unable to reveal F9.

**Verdict: CONFIRMED.** Both are the fixture-level vacuity CLAUDE.md's Premise Discipline warns
about — *"Check the FIXTURE, not the line."*

**Proposed fix:**
- Test 2: the first hop must be **allowed**. Either run the redirecting server on a
  non-loopback local address, or — simpler and deterministic — assert `CheckRedirect` directly
  as a unit: build the restricted client, call `client.CheckRedirect(req, via)` with a request
  to a denied host and a non-empty `via`, and assert the error. State the falsifier: *it fails
  against a client whose `CheckRedirect` is nil.*
- Test 4: use a **hostname** that is not its own IP (e.g. `localhost`, allowlisted, against a
  loopback `httptest` server) so a host allowlist passes and an IP-string comparison fails.
  That single fixture change turns test 4 into the control that would have caught F9.

---
## F14, CRITICAL — "the count is supplied with the env, computed once per env" has NO CARRIER: `ConditionEvaluator` (which D2 refuses to change) passes a bare `map[string]any`. And the measurement that forces the requirement is an invalid comparison (EXECUTED)

**Claims attacked** (verbatim):

- ADR-0186 D2: *"**The bound is computed ONCE PER ENV, not per evaluation — or it costs more
  than it saves.** … Measured on this machine: **~84 ns/op** on a typical few-variable env and
  **~19 µs at the 10 000 default** … Counting per *evaluation* would therefore be **20–60×
  worse than the cost the decision refused**, which would make Decision 2 self-defeating."*
- ADR-0186 D2: *"It is computed when the variable map changes and carried alongside it;
  evaluation compares a number."*
- ADR-0186 Consequences: *"`ConditionEvaluator` keeps its signature."*
- Plan phase 1: *"The count is **supplied with the env, not computed per evaluation**."*

### (a) There is no carrier — the requirement is unbuildable as specified

- `engine/conditions.go:20-27` — `ConditionEvaluator` has exactly three methods, each
  `(code string, env map[string]any)`. **No count parameter, no env wrapper type.**
- `internal/expreval/expreval.go:118,160,212` — `EvalBool` / `EvalDuration` / `EvalString`
  have the same shape. The `Evaluator` receives only the map.
- The engine reaches the evaluator through the interface:
  `engine/step_gateways.go:41` and `:185` — `eval.EvalBool(f.Condition, s.Variables)`, where
  `eval` comes from `resolveEvaluator(opt)` (`conditions.go:49-54`) i.e. `StepOptions.Evaluator`
  — the exact seam `runtime.WithMaxEvalElements` uses.
- So a count computed in `runtime` or `engine` **cannot be handed to `expreval`** without
  changing `ConditionEvaluator`, which D2 forbids in the same section. The only remaining
  options are (i) walk per evaluation (what D2 says is self-defeating), or (ii) key a cache on
  the map's identity — which the bundle never mentions, requires `reflect`/`unsafe` on the map
  header, and is **incorrect** here because the engine mutates `s.Variables` in place during a
  step (an action's output is merged into the same map), so a cached count goes stale exactly
  when it matters.

### (b) The comparison that forces (a) is apples-to-oranges — measured

Measured on this machine (Apple M4 Pro, Go 1.25, `-benchtime=1s`, no `-race`), throwaway
package `internal/expreval/probe` (deleted after):

| benchmark | ns/op | allocs |
|---|---|---|
| `EvalBool("vars.amount > 100")`, `WithTimeout(0)` | **97.50** | 3 |
| `EvalBool("vars.amount > 100")`, `WithTimeout(5s)` (the ctx/goroutine path) | **917.1** | 9 |
| bounded env count, typical env (1 map + 3 scalars), early abort at 10 000 | **64.01** | **0** |
| bounded env count, env holding a 10 000-element slice, early abort | **17 379** (17.4 µs) | 0 |

The ADR's own two figures reproduce (99.43 → 965.20 in the ADR; 97.62 → 976.7 by a re-audit
lens; 97.50 → 917.1 here). The delta the ctx would have cost is **~820 ns/op**.

**The 17.4 µs and the 820 ns are not comparable.** 17.4 µs is the count on a
**10 000-element** env; 820 ns is the ctx cost on a **3-scalar** env. Compared like with like:

- typical env: counting **64 ns** vs ctx **820 ns** ⇒ counting is **~13× CHEAPER**, and
  allocation-free where the ctx path adds 6 allocs.
- 10 000-element env: counting 17.4 µs vs the evaluation it guards, **measured at 1.92 s** at
  n = 10 000 (see F16) ⇒ the count is **0.0009 %** of the work it refuses.

There is no env size at which per-evaluation counting is 20–60× worse than the cost the
decision refused. **The "self-defeating" premise is false, and it is the sole justification for
the once-per-env requirement that has no carrier.**

**Verdict: CONFIRMED (both legs).**

**Proposed fix:**
1. **Drop the once-per-env requirement.** Replace D2's paragraph with the measured comparison
   above: an **early-aborting** bounded count is O(min(elements, n)), costs ~64 ns on a typical
   env — cheaper than the ctx path it replaces — and is negligible against any env large enough
   to matter. Record the numbers, per Premise Discipline.
2. Specify **early abort** explicitly (stop at `n+1`); a naive full walk *is* O(total) and is
   the version that could be argued self-defeating.
3. Rewrite plan phase 1's symbol note (*"the count is supplied with the env"*) and
   `BenchmarkEvalBoolWithBoundEnabled` vs `…Disabled`: the benchmark's pass condition must
   become *"the bound adds less than the `WithTimeout(5s)` path costs on the same env"*, not
   *"adds no per-evaluation walk"* — the latter cannot be satisfied and would stall phase 1 at
   its own escalation clause (*"If it does, stop and escalate"*).
4. If the once-per-env design is nevertheless kept, the ADR must name the carrier and amend
   `ConditionEvaluator` accordingly — and then Consequences' *"`ConditionEvaluator` keeps its
   signature"* becomes false and must go.

---

## F15, CRITICAL — D2 bounds one of the two evaluator surfaces it itself enumerates; the ABAC surface has NO seam to bound and its 5 s timeout makes CPU exhaustion worse, not better

**Claim attacked** (ADR-0186 Context §2, verbatim):

> "⚠ Two evaluator **surfaces**, not one: `authz`'s is `expreval.New()`, i.e.
> `DefaultTimeout = 5 s` **is** enabled; only the engine's gateway evaluator
> (`engine/conditions.go:43`, `expreval.WithTimeout(0)`) is wall-clock unbounded, and that is a
> deliberate ADR-0003/0049/0056 trade, not an oversight."

and D2's scope: `runtime.WithMaxEvalElements(n)` → `driver.conditionEval` → `StepOptions.Evaluator`.

**Evidence:**

- `grep -rn --include="*.go" "expreval.New(" . | grep -v _test.go` → four non-test constructions:
  - `runtime/processdriver_options.go:200` — `driver.conditionEval` (the seam D2 uses)
  - `engine/conditions.go:43` — the engine default
  - **`authz/authz.go:23` — `var attrEval = expreval.New()`, a package-level GLOBAL**
  - **`internal/authz/casbin/authorizer.go:30` — `expreval.New()`, hard-coded in the constructor**
- Neither ABAC evaluator is reachable from any option. `authz.RoleAuthorizer.Authorize`
  (`authz/authz.go:126-145`) evaluates `spec.Attribute` against
  `map[string]any{"actor": actor, "vars": vars}` — **process variables, caller-influenced,
  exactly the axis D2 exists to bound** — using the package global. There is no
  `authz.WithEvaluator`, no option struct, nothing.
- The 5 s timeout does **not** substitute. `internal/expreval/expreval.go:27-32` says so in its
  own godoc: *"because Go cannot preempt a running goroutine, a pure-CPU expression keeps
  consuming a core until it finishes — the timeout bounds latency, not CPU."* Worse, the
  timeout path **abandons** the goroutine (`run`, `:74-100`: the select returns on the timer
  and the goroutine keeps running). So a caller who can trigger repeated ABAC evaluations over
  a large `vars` accumulates one core-burning goroutine every 5 s, indefinitely — the guard
  converts a bounded-latency CPU burn into an **unbounded goroutine leak**.

**Verdict: CONFIRMED.** D2's Context correctly enumerates two surfaces and its Decision bounds
one, without saying so. The unbounded one is the ABAC path — the surface ADR-0186's own
sibling delivery (ADR-0185) is about, and the one whose predicate is evaluated on every
claim/complete/reassign.

**Proposed fix:**
- State the scope honestly in D2: *"the bound applies to the driver's condition evaluator only.
  The two ABAC evaluators (`authz/authz.go:23`, `internal/authz/casbin/authorizer.go:30`)
  construct `expreval.New()` with no seam and are NOT bounded by this decision."*
- Then either (a) add `authz.WithAttributeEvaluator(ConditionEvaluator)` /
  `casbinauthz.WithEvaluator(...)` in this delivery and plumb the same `n` (a small phase — one
  option each, both packages are container-free), or (b) record it as an explicit follow-up
  backlog item with the goroutine-accumulation note above, and say in Consequences → Negative
  that the ABAC path remains unbounded in input size.
- Add the missing **D2 × `authz`** row to spec §5's pairwise table.

---

## F16, MINOR (measurement the plan asked for) — n = 10 000 re-measured: 1.92 s, not the extrapolated ~2.4 s; the ladder reproduces

**Claim attacked** (plan §0 item 7): *"**Re-measure the O(n²) ladder at n = 10 000.** The
~2.4 s figure behind the default is *extrapolated*, not measured."* — and ADR-0186 D2's table
row `| 10 000 | **~2.4 s** | the default |`.

**Evidence — EXECUTED** (same probe package; predicate 81 bytes, `WithTimeout(0)`, single run
per n, Apple M4 Pro, no `-race`):

```
n=1000  elapsed=19.82 ms
n=2000  elapsed=77.70 ms
n=4000  elapsed=308.53 ms
n=8000  elapsed=1.2298 s
n=10000 elapsed=1.9249 s
```

Clean O(n²) (ratios 3.92 / 3.97 / 3.99 per doubling; 10 000/8 000 = 1.565 vs the theoretical
1.5625). The bundle's ladder (25 / 98 / 391 ms / 1.563 s) reproduces in shape and is ~25 %
slower in absolute terms — a machine difference, not a defect.

**Verdict: PARTLY REFUTED (the number, not the decision).** The extrapolation is ~25 %
conservative on this machine. The 10 000 default remains defensible; the ADR's table should
carry the measured value with the machine noted, since Premise Discipline forbids leaving a
load-bearing number as an extrapolation once it has been run.

**Proposed fix:** change the D2 table row to `| 10 000 | **1.92 s measured** (Apple M4 Pro, no
-race; ~2.4 s extrapolated) | the default |` and note that the ladder was re-derived at the
0186 bundle commit. ⚠ Also note the mode: under `-race` this is several times slower, so the
default's real-world latency claim should say which mode it describes.

---

## F17, MAJOR (settles a plan-flagged OPEN) — the env bound does NOT reach `action/httpcall`, and cannot without a new option

**Claim attacked** (plan §0 item 2 / spec §5 D3×D2 row, both marked **OPEN**): *"Does the new
env-element bound apply to `action/httpcall`'s URL evaluation? … the env there is process
variables, so the bound *should* apply, but neither decision says."*

**Evidence — the answer is no, by construction:**

- D2's bound is carried by an `*expreval.Evaluator` **instance** built in
  `runtime/processdriver_options.go` and assigned to `driver.conditionEval`.
- `action/httpcall` is constructed by the **consumer**, independently, via
  `httpcall.NewHTTPCall(opts...)` (`httpcall.go:207`) and registered in the action catalog by
  name. It receives no evaluator and has no reference to the driver. Phase 6's instruction
  ("route `WithURLExpr` through `internal/expreval`") would have it construct its **own**
  `expreval.New(...)` — with whatever options phase 6 hard-codes, not the consumer's `n`.
- There is no plumbing path: `runtime` does not construct actions, and `action/httpcall` cannot
  import `runtime` (the action catalog is resolved by name at execution time).

**Verdict: CONFIRMED — the open interaction resolves against the bundle's guess.** The bound
does not apply, and cannot, without a new `httpcall.WithMaxEvalElements(n)` option that the
consumer sets on the action *and* keeps in sync with `runtime.WithMaxEvalElements`.

**Proposed fix:** decide and write it down. Recommended: phase 6 adds
`httpcall.WithMaxURLExprElements(n int)` defaulting to the same 10 000, documented as
*"independent of `runtime.WithMaxEvalElements`; set both if you tune either"*. Note in D2 that
the two knobs are separate and why. Remove the OPEN marker from spec §5.

---
## F18, CRITICAL — the default-ON caps can WEDGE a running instance permanently, and the repo's own default `httpcall` produces values 40× the variable cap

**Claims attacked** (verbatim):

- ADR-0186 D1: *"`service.WithMaxVariableBytes`, default **256 KiB** for an instance's variable
  map, refused before persist with a sentinel."* … *"Both caps default **on**."*
- Plan phase 7: *"`WithMaxVariableBytes(n int64) Option`, default **256 KiB**, refused before
  persist with a sentinel classified 413."*
- ADR-0186 D2: default **10 000** env elements.
- ADR-0186 Consequences → Negative: *"Default-on caps will reject payloads that work today."*

**Evidence:**

- `action/httpcall` — a **first-party, in-repo action** — writes the response body into a
  process variable: `httpcall.go:353-358`, `out := map[string]any{ h.statusKey: …,
  h.bodyOutKey: decodeBody(...), h.hdrOutKey: … }`, default key `"httpBody"` (`:212`). Its own
  default response cap is **10 MiB** (`defaultMaxResponseSize`, `:90`) — **40× the proposed
  256 KiB variable cap**. Two first-party defaults that contradict each other out of the box.
- `decodeBody` (`:372-383`) `json.Unmarshal`s a JSON response into `any`, so a 256 KiB JSON
  array becomes ~40–50 k elements — the ADR's own figure — which **exceeds D2's 10 000-element
  default** as well. A single successful, legitimate `httpcall` can therefore trip *both* caps.
- **There is no recovery.** `service.Service` (`service/service.go:115-121` →
  `InstanceStarter`/`InstanceReader`/`TaskManager`/`Messaging`/`InstanceOps`) exposes
  **no verb that edits or shrinks an instance's variables**. Once the map exceeds the cap:
  - every subsequent step's persist is refused ⇒ the instance cannot progress;
  - `CancelInstance` also writes state, so under a persist-boundary check the instance cannot
    even be **cancelled**;
  - under D2, every gateway condition returns `ErrEnvTooLarge` ⇒ the token cannot route.
  The instance is unrecoverable by any exposed operation.
- The bundle never asks **where** the refusal happens relative to the work already done. If the
  action already ran (an HTTP POST that charged a card), refusing the persist means the side
  effect happened and the state recording it cannot be written.

**Verdict: CONFIRMED.** This is the "guard refuses the useful case" failure at its most
expensive: not a rejected request, an unrecoverable instance — the same *upgrade-stranding*
shape the re-audit accepted as Critical F11 against ADR-0185's D4, reproduced here for a
different reason and not carried across when the bundle was re-cut.

**Proposed fix (all three needed):**
1. **Reconcile the defaults.** Either raise `MaxVariableBytes` above `httpcall`'s response cap,
   lower `httpcall`'s default, or state explicitly that a consumer using `httpcall` with a large
   `WithMaxResponseSize` must raise `WithMaxVariableBytes`. Put the sentence in D1 and in
   `SECURITY.md`.
2. **Decide the refusal point and say it.** Recommended: enforce at the **request boundary**
   (`StartInstance` vars, `DeliverSignal` payload, `CompleteTask` output) where the caller can
   be told 413 and nothing is wedged — *not* at persist. If persist must also be guarded, it
   must **fail the step into an incident**, not refuse the write, so the existing
   resolve-incident path applies.
3. **Provide an escape.** Either exempt terminal transitions (cancel/terminate always persist)
   or add an admin verb to truncate/drop a named variable. Without one, item 2 is the only
   thing standing between a consumer and a dead instance.
4. Add the missing **D1 × D2 × `action/httpcall`** row to spec §5 — the pairwise table has a
   D1×D2 row about *units*, and misses that both bounds are tripped by the same first-party
   action.

---

## F19, CRITICAL — phase 7's sentinel is "classified 413" by no phase, and the phase table makes the classifier arm impossible to write

**Claim attacked** (plan phase 7): *"`WithMaxVariableBytes(n int64) Option`, default **256 KiB**,
refused before persist with a sentinel **classified 413**."*

**Evidence:**

- The only 413 work anywhere in the bundle is phase 4's *"`ClassifyError`: **413 arm placed
  BEFORE the 400 arm**"*, and D1 defines that arm as mapping **`httpcore.ErrBodyTooLarge`** —
  a sentinel about request **bodies**. A `service` sentinel about **variable size** is a
  different error and matches no arm ⇒ it falls to `ClassifyError`'s `default:` and ships as
  **500 with an empty body** (`errors.go:57-58`).
- Phase 4 is scoped to `httpcore` and lists no `service` sentinel. Phase 7 is scoped to
  `service` and touches no classifier.
- The phase table (`plan §2`) says phase 7 **depends on `—`** and runs *"‖ 4, 5, 6"*. So the
  sentinel does not exist when phase 4's agent writes the classifier, and phase 4 has already
  finished when phase 7 creates it. **No ordering in the plan permits the arm to be written.**
  (`httpcore/errors.go:12` already imports `service`, so the arm is *possible* — just not
  schedulable as the plan is drawn.)
- Nothing in the verification checklist would catch it: no prescribed test asserts 413 for an
  oversized variable map.

**Verdict: CONFIRMED — zombie scope plus a phase-ordering defect.**

**Proposed fix:** move the variable cap to **phase 7a** (sentinel only, `service`), make phase 4
depend on it, add `service.ErrVariablesTooLarge` to the 413 arm alongside `ErrBodyTooLarge`, and
prescribe `TestOversizedVariablesClassifyAs413` in phase 4 with the falsifier *it fails against
a 413 arm that names only `ErrBodyTooLarge`*. Or drop the "classified 413" claim from phase 7
and say the sentinel is a `service`-level error the consumer maps themselves.

---

## F20, MAJOR — `runtime.WithMaxEvalElements` (adjudicating the plan's OPEN item 3): "silently" is false, "refuse at construction" is available, and the real hole is `WithExpressionTimeout` silently dropping the bound

**Claim attacked** (ADR-0186 D2): *"`runtime.WithExpressionTimeout` … and
`runtime.WithConditionEvaluator` … both assign `driver.conditionEval` — the same field. Three
options writing one field is last-writer-wins, **silently**."* Plan §0 item 3: *"Compose, or
refuse at construction? … **Pick one.**"*

**Evidence:**

- Not silent: **both** godocs document it. `runtime/processdriver_options.go:196-197` —
  *"WithExpressionTimeout and [WithConditionEvaluator] set the same field; the last option
  wins."*; `:215-216` — the mirror sentence. Last-writer-wins is this file's stated convention.
- "Refuse at construction" is expressible: `NewProcessDriver(opts ...Option) (*ProcessDriver, error)`
  (`runtime/processdriver.go:198`) already returns an error, and already fails that way for the
  default store (`:200-202`). No signature change needed.
- **The real hole the bundle misses:** `conditionEval` is **nil by default** (it is absent from
  the struct literal at `processdriver.go:207-218`), so a default 10 000 bound must be applied
  *after* the option loop, gated on `conditionEval == nil`. A consumer who sets
  `WithExpressionTimeout(d)` therefore gets `expreval.New(expreval.WithTimeout(d))` —
  **no element bound** — and D2's *"default 10 000"* silently does not apply to them. That is
  the most likely combination in practice: the consumer who enables the DoS guard is precisely
  the one who wanted input bounded.
- Cosmetic knock-on: `processdriver.go:440` logs `slog.Bool("conditionEval", driver.conditionEval != nil)`.
  A default-constructed evaluator flips that field to `true` for every existing consumer; the
  log line's meaning changes and no phase updates it.

**Verdict: PARTLY REFUTED (the "silently" premise) / CONFIRMED (a worse hole underneath).**

**Adjudication — pick this:** keep the file's convention (**last-writer-wins, documented**) for
`WithConditionEvaluator`, and make `WithExpressionTimeout` **compose**: it should build
`expreval.New(WithTimeout(d), WithMaxEnvElements(n))` using the driver's current `n`, which
means `n` must be stored on the driver as a plain field (`driver.maxEvalElements`) and the
evaluator constructed **after** the option loop, not inside the options. Then:
- `WithMaxEvalElements(n)` sets the field;
- `WithExpressionTimeout(d)` sets a timeout field;
- both are applied together post-loop;
- `WithConditionEvaluator(e)` overrides wholesale and **must** log/return a named error if
  `WithMaxEvalElements` was also set — a consumer-supplied evaluator cannot be bounded from
  outside, and silently dropping the bound is the failure this decision exists to prevent.
Prescribe `TestExpressionTimeoutKeepsTheElementBound` with the falsifier *it fails against an
implementation that constructs the evaluator inside `WithExpressionTimeout`* — i.e. against
today's code shape.

---

## F21, MAJOR — the migration story for two default-ON caps is one sentence, and there is no way for a consumer to discover they are about to be broken

**Claim attacked** (ADR-0186 D1): *"Both caps default **on**. Pre-v0.1.0 is the window in which
a fail-closed default is cheap; after it, it never gets one."* and Consequences → Negative:
*"Default-on caps will reject payloads that work today."*

**Evidence / what is missing:**

- No **observe-only mode**. A consumer upgrading cannot answer "would this cap have fired?"
  without turning it on in production. Every other risky default in this repo ships with a
  visible signal (the transport already has `wrkflw_rest_requests_total` labelled by status,
  `observability.go:56-63`) — but a 413 counter only tells you *after* you have started
  rejecting.
- No **metric or log for near-misses**. The natural mitigation — count bodies over, say, 50 %
  of the cap at `WarnContext`, and a `wrkflw_rest_body_bytes` histogram — is not in any phase.
- The **variable** cap is worse: it fires deep inside a step, not at a request boundary, so a
  consumer discovers it as a wedged instance (F18), not as a 413.
- **Detectability of the `ErrorBody` break is asserted, not designed.** Phase 9 says CHANGELOG
  + STABILITY. There is no deprecation window, no opt-out
  (`httpcore.WithLegacyErrorMessages()`), and no way for a consumer to run both shapes. For a
  library whose product *is* the API, a wire-format change with no transitional flag is a
  larger ask than the ADR's one bullet suggests.
- `ErrorBody`'s break shape is **under-enumerated** (the brief's question 7). Beyond the two the
  ADR lists, the actual surface is: (i) `403` message changes; (ii) `400` message changes for
  **all eight** sentinels, not just validation (F3/F4); (iii) a new field appears — which
  breaks any consumer decoding `ErrorBody` with `DisallowUnknownFields`; (iv) a **new status
  code (413)** appears on routes that previously returned 400/500 — clients with an exhaustive
  status switch break; (v) `ClassifyError`'s **Go signature** may change (F1); (vi) `Logger`
  starts receiving 400/403 records, changing log volume and possibly cost for anyone shipping
  logs by the gigabyte.

**Verdict: CONFIRMED.**

**Proposed fix:**
- Add a D1 paragraph: `MaxBodyBytes` and `MaxVariableBytes` accept `0` = unbounded and a
  documented **observe-only** value (e.g. a separate `WithBodySoftLimit(n)` that logs+counts
  and does not reject), so a consumer can measure before enforcing. Add
  `wrkflw_rest_request_body_bytes` to the existing instrumentation in the same phase.
- Replace the one-line CHANGELOG bullet in phase 9 with the **six-item** break list above, and
  decide explicitly whether a `WithLegacyErrorMessages()` opt-out ships for one minor version.
- State in Consequences that pre-v0.1.0 status is the justification — and check it: if any
  consumer is already on this API, "pre-v0.1.0" is a claim that needs a source
  (`STABILITY.md`), not an assumption.

---
## F22, CRITICAL (discharges the plan's OPEN item 1) — above fiber's 4 MiB app limit the adapter is never reached: the 413 body is fasthttp plain text, with no `ErrorBody`, no correlation id and no log record; and `MaxBodyBytes` > 4 MiB is silently ineffective on fiber

**Claims attacked** (verbatim):

- Spec §7 / plan §0 item 1: *"`ASSUMPTION (unverified)`: the **fiber body-cap mechanism** — a
  `len(c.Body())` pre-check, reasoned from source … **Execute it before phase 5 edits 13 call
  sites.**"*
- ADR-0186 D1: *"a `len(c.Body())` pre-check for fiber … ⚠ Conceded plainly: fiber's pre-check
  is a **rejection, not a prevention** … and `fiber.DefaultBodyLimit` is what actually prevents
  the amplification there."*
- ADR-0186 D5: *"**Every error body gains a correlation id**, echoed in the log line."*
- Plan phase 8: *"Add parity cases asserting all three adapters agree on **413** for an
  oversize body."*

**Evidence — EXECUTED**, two probes in a throwaway package `transport/http/fiber/probe`
(deleted after), fiber v3.4.0, `fiberlib.New()` — exactly the construction a consumer writes:

Probe A (`app.Test`, mounted handler logs what it observes):
```
HANDLER REACHED: len(c.Body())=1048576     size=1048576  status=200
HANDLER REACHED: len(c.Body())=2097152     size=2097152  status=200
size=5242880 TEST ERROR: body size exceeds the given limit   (handler NOT reached)
size=8388608 TEST ERROR: body size exceeds the given limit   (handler NOT reached)
```

Probe B (real socket, `app.Listener` + `http.Client`):
```
size=2097152  status=200 content-type="application/json; charset=utf-8" body="{\"seen\":2097152}"
size=8388608  status=413 content-type="text/plain; charset=utf-8"       body="Request Entity Too Large"
```

**What this establishes:**

1. **The pre-check mechanism is viable — but only in the 0…4 MiB band.** For bodies up to
   `fiber.DefaultBodyLimit` the handler is reached and `c.Body()` returns the complete body, so
   `len(c.Body())` can enforce a 1 MiB cap. The spec's `ASSUMPTION (unverified)` is
   **discharged for that band** (see "What HELD").
2. **Above 4 MiB the route group is bypassed entirely.** The handler is never invoked, so no
   `len(c.Body())` check runs, `writeErr` never runs, `ClassifyError` never runs,
   `cfg.Logger` is never called.
3. **D5's quantifier is false.** The 413 that a fiber consumer's client actually receives above
   4 MiB is fasthttp's own `text/plain` `Request Entity Too Large` — **not** `httpcore.ErrorBody`
   JSON, no `error` field, no `message`, **no correlation id**, and **no log line to join it
   to**. "Every error body gains a correlation id" is untrue for the single largest class of
   oversize request on one of the three shipped adapters.
4. **Phase 8's parity assertion is fixture-dependent.** A parity test whose body is < 4 MiB sees
   413 from all three and passes; the same test at 8 MiB sees three-way agreement on the
   *status* and disagreement on the *content type and body shape*. If phase 8 asserts on
   `ErrorBody` (as every other parity case must), it **fails on fiber** at any size above
   4 MiB — and the plan gives the fixture size nowhere.
5. **`MaxBodyBytes` above 4 MiB is silently ineffective on fiber.** A consumer who raises the
   cap to 8 MiB gets it honoured on stdlib and gin and overridden by fiber's app-level limit —
   a knob accepted and ignored, with no error and no warning. The ADR notes
   `fiber.DefaultBodyLimit` as the thing that "actually prevents the amplification" and never
   notices that it also **caps the consumer's own configuration from above**.

**Verdict: CONFIRMED** (and the author-flagged assumption is now executed, partly holding).

**Proposed fix:**
1. Record the two probe transcripts in the spec's evidence section; replace the
   `ASSUMPTION (unverified)` with *"viable for bodies ≤ `fiber.DefaultBodyLimit`; above it the
   handler is not reached (executed)."*
2. **Fiber's mount must reconcile the two limits.** Either (a) `Customize` refuses at mount
   time when `MaxBodyBytes > fiber.DefaultBodyLimit` unless the consumer also configured
   `fiber.Config.BodyLimit` (we cannot set it — it belongs to `fiber.New`), or (b) the fiber
   package documents loudly that `MaxBodyBytes` is capped from above by the app's `BodyLimit`
   and that the consumer must raise both. (a) is preferable: a silently-ignored security knob
   is worse than a refusal.
3. **Pin phase 8's parity fixture below 4 MiB and say why**, and add a separate,
   explicitly-labelled fiber-only case documenting the plain-text 413 above `DefaultBodyLimit`
   as known-divergent — so the divergence is a recorded decision rather than a gap the fixture
   happens to avoid.
4. Scope D5's correlation-id sentence: *"every error body **the library writes**"* — and state
   in `SECURITY.md` (phase 9) that a fiber consumer's >4 MiB rejections are emitted by the
   framework and are neither logged nor correlated by `wrkflw`.

---

## F23, MINOR — phase 8's verify command re-runs phases 4–7's packages but the plan calls it "test fallout"; phase 3's `./runtime` caveat is right and phase 8's is not stated

**Claim attacked** (plan phase 8): *"**Verify:** `go test -race -count=1 ./transport/http/...`"*,
and phase 3's *"**Verify:** `go test -race -count=1 ./runtime` ⚠ **not** `./runtime/...`, which
is not container-free."*

**Evidence:** phase 3's caveat is correct and well-placed. Phase 8's command is the only one in
the plan that spans **four** packages (`httpcore`, `stdlib`, `gin`, `fiber`, `parity`) — which
is the right scope for a parity phase, but it means phase 8's agent will see failures owned by
phases 4–7 and cannot tell them apart from its own. The plan labels phase 8 "test fallout"
without saying whose. Separately, the checklist's `go build ./examples/...` is the only
consumer-facing compile check and it is not attached to any phase that changes a public
signature (`ClassifyError`, `CustomizeConfig`) — so an `examples/` break surfaces only at the
very end.

**Verdict: CONFIRMED (Minor).**

**Proposed fix:** give phase 8 the two-step verify — `go test -race -count=1 ./transport/http/parity/...`
first (its own scope), then the repo-wide `./transport/http/...` as the regression sweep — and
add `go build ./examples/...` to phase 4's verify, since phase 4 is where the public
`httpcore` surface changes.

---

## What HELD — do not re-litigate

These were attacked and survived. Several are corrections the revision made against earlier
rounds; all reproduce.

1. **`gate.go:45` really is `%w: %s`.** `runtime/validation/gate.go:44-46` —
   `return fmt.Errorf("%w: %s", ErrInvalidInput, err.Error())`. The typed strategy error is
   flattened to a string before the transport, so `errors.As` at `ClassifyError` is false and
   the rendering genuinely must move into `runtime/validation`. The re-audit's finding 9 and
   this bundle's phase 2 are correct.
2. **`ClassifyError` is an ordered switch and the 413-before-400 ordering requirement is real.**
   `transport/http/httpcore/errors.go:26-59`: six arms — 404 `:28`, 403 `:32`, 409 `:34`,
   400 `:36-50`, 422 `:51`, default 500 `:57`. Five echo `err.Error()`; 500 blanks. An error
   wrapping both `ErrBadInput` and a new sentinel *would* match 400 first. D1's mandate for a
   **bare** sentinel plus arm ordering is the right fix.
3. **The 400 arm's sentinel count is 8 across 5 `errors.Is` groups** — re-counted from source
   (`:36` ×2, `:37` ×2, `:42` ×2, `:46` ×1, `:49` ×1). ADR D5's body, its "other seven" phrasing,
   Consequences' "seven non-validation sentinels" and plan §4's table are all **consistent at 8**.
   ⚠ Only the ADR's top banner says *"nine sentinels"* — one stale number inherited from the
   re-audit; correct it, but the decision text is right.
4. **`mapInstance` has exactly 6 call sites**, and two further read endpoints take no mapper.
   `endpoints.go:42, 52, 94, 124, 140, 155` = 6; `GetInstanceSnapshot` `:60-66` and
   `GetActionableView` `:72-78` bypass it. Plan §4's row is correct.
5. **`view.go:31` really aliases.** `NewInstanceView` assigns `Variables: st.Variables`
   verbatim. The convention-violation framing (rather than the withdrawn "mutates instance
   state" claim) is the right one, and the fix is one line.
6. **Two evaluator surfaces, correctly identified.** `engine/conditions.go:43` is
   `expreval.New(expreval.WithTimeout(0))`; `authz/authz.go:23` and
   `internal/authz/casbin/authorizer.go:30` are `expreval.New()` with `DefaultTimeout = 5s`.
   The Context §2 correction against the earlier draft is right. (What it then omits is F15.)
7. **`ConditionEvaluator` keeps its signature — and dropping the ctx is right.**
   `internal/expreval/expreval.go:74-76`: `run` is synchronous when `timeout <= 0`, so a ctx
   could not interrupt it. The ADR-0003/0049/0056 determinism argument at
   `engine/conditions.go:29-43` is quoted accurately.
8. **The ctx-path benchmark reproduces.** Measured here: 97.50 ns/op / 3 allocs →
   917.1 ns/op / 9 allocs (ADR: 99.43 → 965.20; re-audit lens: 97.62 → 976.7). Three
   independent reproductions.
9. **The O(n²) ladder reproduces** — 19.8 / 77.7 / 308.5 ms / 1.230 s at n = 1 000 / 2 000 /
   4 000 / 8 000, ratios 3.92 / 3.97 / 3.99. The predicate is 81 bytes (`len(code)`), matching
   the corrected "80" rather than the draft's "44".
10. **`expr.MaxNodes` is inverted and must not be implemented** — `expr@v1.17.8/expr.go:221`
    states it; the ADR's ⚠ is correct.
11. **`AdminListInstances` is clean.** `admin_endpoints.go:57-` projects no variables; the ADR's
    check holds.
12. **`WithBaseURL` must stay unrestricted, and `urlExpr` takes precedence.**
    `httpcall.go:229-244`: `requestURL := h.baseURL`, overwritten when `urlExprProg != nil`. So
    "expression-derived" is decidable at construction (`urlExprProg != nil`), which is what makes
    D3's per-client transport split coherent at all. Plan phase 6 test 3
    (`TestBaseURLIsUnrestricted`) is a genuine, non-vacuous control.
13. **The fiber `len(c.Body())` pre-check works below `DefaultBodyLimit`** — executed, handler
    reached with the full body at 1 MiB and 2 MiB. The mechanism is sound in the band it can
    reach (F22 is about the band it cannot).
14. **`NewProcessDriver` returns an error**, so "refuse at construction" is available without a
    signature change (`runtime/processdriver.go:198-202`).
15. **Every decision in scope is genuinely independent of ADR-0185.** No symbol ADR-0185
    introduces is referenced by any in-scope decision; the 401/503 arms really were removed.
    The re-cut is clean on that axis.
16. **The arithmetic is right everywhere, again.** As in both prior rounds, no computed value
    was wrong; the failures are premises, quantifiers, mechanisms and missing decisions.

---

## Ranked index — most severe first

| # | sev | what it actually claims |
|---|---|---|
| **F6** | **Critical** | **`TestActionableViewRedactsTaskVars` — a test the bundle's own author prescribes as "the control that decides D4's placement" — CANNOT FAIL. Executed: `ActionableView` has no `Vars` field (`instance_actionable.go:25-41`) and never reads `t.Vars`; a fixture setting `Vars={"ssn":"123-45-6789"}` produces JSON containing no such string. The ADR premise it rests on ("`GetActionableView` renders … `HumanTask.Vars`") is false.** |
| **F9** | **Critical** | **`WithAllowedHosts` is not implementable where D3 puts the check. Executed: `net.Dialer.Control` receives only the resolved `IP:port` — `http://localhost:…` arrives as `[::1]:…`/`127.0.0.1:…`, the hostname is gone. A host allowlist cannot be evaluated there, so the fine-grained escape hatch does not work and a consumer needing one internal host is left with `WithUnrestrictedTransport()` — turning SSRF protection off wholesale.** |
| F14 | Critical | "The count is supplied with the env, computed once per env" has no carrier: `ConditionEvaluator` (which D2 refuses to change) passes a bare `map[string]any`; the engine calls `eval.EvalBool(f.Condition, s.Variables)` through that interface. And the "20–60× worse, self-defeating" measurement that forces the requirement compares a 10 000-element count (17.4 µs measured) against a 3-scalar ctx cost (820 ns measured) — like-for-like, counting is ~13× *cheaper* (64 ns vs 820 ns) and is 0.0009 % of a 1.92 s evaluation. |
| F18 | Critical | The default-ON caps can wedge a running instance permanently. First-party `action/httpcall` writes the response body into `vars["httpBody"]` with a default 10 MiB cap — 40× the proposed 256 KiB variable cap — and a JSON array response blows D2's 10 000-element bound too. `service.Service` exposes no verb to shrink variables, and a persist-boundary refusal blocks even `CancelInstance`. |
| F3 | Critical | The 400 allow-list's deny half is built by no phase: `errors.go:36-50` renders all 8 sentinels through one `Message: err.Error()`, and no document tells anyone to split the arm. Phase 2 covers only the three `ErrInvalidInput` strategies; phase 4's six tests assert nothing about the seven non-validation sentinels; `avro` has no test at all. |
| F4 | Critical | The static-400 default refuses the useful case. Four of the seven blanked sentinels echo no caller value at all, and the in-code rationale for three of them — written in the very switch being edited (`errors.go:38-49`, ADR-0146/0152/0183) — says the message must stay actionable. `ErrBadInput`, the highest-volume 400 (36 decode sites + the whole `httpcore.Validate` DTO layer), becomes `"invalid input"` on all 26 routes. ADR-0186 neither cites nor amends the three ADRs it contradicts. |
| F22 | Critical | Above fiber's 4 MiB app limit the route group is never reached (executed): the client gets fasthttp's `text/plain` `Request Entity Too Large` — no `ErrorBody`, no correlation id, no log record — falsifying D5's "every error body gains a correlation id". `MaxBodyBytes` > 4 MiB is silently ignored on fiber, and phase 8's parity claim only holds if its fixture happens to be under 4 MiB. |
| F5 | Critical | "All 39 decode sites already wrap in `ErrBadInput`" is false in all three documents: 36 wrap, **3 discard** (`stdlib:238`, `gin:265`, `fiber:255` — the optional-body admin resolve-incident route). With a body cap installed, an oversize body there is silently swallowed and the incident resolves with zero-value input, returning 2xx instead of 413. |
| F1 | Critical | The correlation id cannot be produced where the plan puts it: `ClassifyError(err error)` takes no context and no config, and the OTel span id is reachable only from the request `ctx` (not from `cfg.TracerProvider`, as the ADR argues). Making it work changes the signature of an exported function `doc.go:66` advertises as a consumer seam — a source break absent from every breaking-change list. |
| F2 | Critical | No phase builds the log half of the join that justifies blanking 403. `ClassifyError`'s only non-test callers are the three adapters' `writeErr`, all gated on `status >= 500`, so 400/403 produce **no log record today**; `httpcore` never logs at all. `TestCorrelationIDInBodyMatchesTheLogRecord` is prescribed in the one package that cannot emit a log line. |
| F7 | Critical | The redaction hook covers `variables` and nothing else. `GetInstanceSnapshot`'s wire projection also carries `tokens[].payload`, `incidents[].error` (which embeds the httpcall target URL), `tasks[].candidates/claim/completion` (actor attribute maps) and the entire embedded `definition`. `instanceJSON` is unexported, there is no common type across the three read shapes, and `func(map[string]any) map[string]any` cannot express per-instance, per-definition or per-scope policy. |
| F10 | Critical | D3 collides with the existing `WithHTTPClient` option — one `h.client` field, options applied in registration order — so the restricted transport either silently loses to a consumer-supplied client or silently discards their otel-instrumented one. The bundle applies its "compose or refuse, never overwrite" rule to `runtime` and not here, where the casualty is a security control. |
| F13 | Critical | Two of phase 6's four tests cannot discriminate. `TestURLExprRefusesRedirectToLoopback` is refused at the *first* hop (httptest binds 127.0.0.1) and never reaches `CheckRedirect` — it is green against an implementation with no `CheckRedirect` at all. `TestAllowedHostsOptsBackIn` uses a fixture where host and IP are the same token, so it cannot reveal F9. |
| F15 | Critical | D2 bounds one of the two evaluator surfaces its own Context enumerates. `authz/authz.go:23` and `internal/authz/casbin/authorizer.go:30` construct `expreval.New()` as a package global / hard-coded field with **no options seam**, so `runtime.WithMaxEvalElements` cannot reach the ABAC path — the one evaluating caller-influenced `vars` on every claim/complete/reassign. Its 5 s timeout abandons the goroutine, converting a bounded CPU burn into unbounded goroutine accumulation. |
| F19 | Critical | Phase 7's *"sentinel classified 413"* is built by no phase: the only 413 arm maps `ErrBodyTooLarge`, so a `service` variable-size sentinel falls to `default:` and ships as an empty-bodied 500. The phase table makes it unschedulable — phase 7 depends on nothing and runs in parallel with phase 4, which writes the classifier. |
| F20 | Major | Adjudicates the plan's OPEN item 3. "Silently" is false — both godocs document last-writer-wins (`processdriver_options.go:196-197, 215-216`) — and "refuse at construction" is available since `NewProcessDriver` already returns an error. The real hole: `conditionEval` is nil by default, so a post-loop default means `WithExpressionTimeout(d)` yields an evaluator with **no element bound**, silently exempting exactly the consumer who asked for DoS protection. Recommended resolution stated. |
| F21 | Major | Two default-ON caps ship with no observe-only mode, no near-miss metric and no way to discover you are about to break; the variable cap surfaces as a wedged instance rather than a 413. `ErrorBody`'s break is under-enumerated — six distinct breaks, not two (new field vs `DisallowUnknownFields`, a new 413 status on routes that never returned it, the possible `ClassifyError` signature change, and a log-volume change). |
| F17 | Major | Settles the plan's OPEN item 2 against the bundle's guess: the env bound does **not** reach `action/httpcall`'s URL evaluation and cannot, because the action is consumer-constructed by name and holds no reference to the driver's evaluator. A separate `httpcall.WithMaxURLExprElements(n)` is required. |
| F8 | Major | A non-admin 200 route publishes the predicate source D5 spends two sections removing from the 403 body: executed, `GET …/actionable` emits `"condition":"vars.internalApprovalLimit > 5000"`, and `GET …/snapshot` embeds the whole definition. Not necessarily wrong — but undecided and unwritten, which makes the 403 change close to cosmetic for an authenticated caller. |
| F11 | Major | `CheckRedirect`'s default behaviour is undefined: with no `AllowedHosts` configured (the default) the rule as written refuses *every* redirect, breaking http→https and trailing-slash normalisation. No prescribed test covers a legitimate redirect, so either reading ships green. |
| F12 | Major | `action/httpcall.ErrBodyTooLarge` already exists (`httpcall.go:94`, 10 MiB response cap); D1 introduces a second exported sentinel with the same name in the same commit, and phase 9's CHANGELOG/`SECURITY.md` will name it unqualified. The existing 10 MiB default is also unacknowledged prior art for D1's 1 MiB judgement call. |
| F16 | Minor | Executes the plan's own item 7: n = 10 000 measured at **1.92 s**, not the extrapolated ~2.4 s (ladder reproduces cleanly). The default stands; Premise Discipline says the measured number replaces the extrapolation, with the machine and `-race` mode named. |
| F23 | Minor | Phase 8's verify command spans four packages it does not own, so its agent cannot separate its own failures from phases 4–7's; and `go build ./examples/...` — the only consumer-compile check — is attached to no phase, though phase 4 changes the public `httpcore` surface. |

**Totals: 23 findings — 15 Critical, 6 Major, 2 Minor.**

⚠ One cross-cutting observation for the adjudicator. Six of the fifteen Criticals (F1, F2, F3,
F19, F22, and the placement half of F7) are the same failure: **a decision stated in the ADR
whose realisation lands in a package no phase assigns it to**. The bundle guards hard against
zombie scope in the one place it already burned (D2's plumbing, flagged twice), and the pattern
recurs five more times unflagged. A mechanical check would catch all six: for every sentence in
the Decision section, name the phase and the package that builds it — and reject any that has
none.

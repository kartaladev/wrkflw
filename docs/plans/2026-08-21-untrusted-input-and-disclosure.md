# Plan — untrusted input and disclosure posture (ADR-0186)

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

## ▶ Progress

- **Branch:** `design/authz-security-b3` (docs-only). ⚠ Do not quote its SHA — it is
  amended on every revision.
- **State:** re-cut 2026-08-21 from the failed B3 bundle into its own delivery.
  Spec + ADR + this plan written. **Rule-#9 audit PENDING. Zero phases executed.**
- **Lineage:** ADR-0186 was half of B3, which failed two audits — the first on
  individual decisions (58 findings), the second on the interactions between the four
  decisions the revision rewrote (38 findings). Six of audit #2's findings apply here
  and are folded. Records: `docs/plans/sweep-evidence/{audit,reaudit}-b3-adjudication.md`.
- **Adjudicated findings folded into this delivery:** the 400 rendering moved out of
  `ClassifyError` into `runtime/validation`; 400 became an allow-list; 413 given a
  bare sentinel and arm ordering; redaction moved to the response boundary; the env
  bound made once-per-env; the 256 KiB default demoted to a payload bound.

---

## 0. What the audit must attack

The author's own list of where this is most likely still wrong. Give it to the
auditors; do not make them re-derive it.

1. **The fiber body-cap mechanism is `ASSUMPTION (unverified)`.** A `len(c.Body())`
   pre-check, reasoned from source only. It is conceded to be a *rejection, not a
   prevention*. **Execute it before phase 5 edits 13 call sites.**
2. **Does the new env-element bound apply to `action/httpcall`'s URL evaluation?**
   Spec §5 flags this as an **open interaction**: D3 routes `WithURLExpr` through
   `internal/expreval`, which D2 changes, and the env there is process variables. The
   bundle does not say. Settle it.
3. **`runtime.WithMaxEvalElements` collides with two existing options** that assign
   the same field (`WithExpressionTimeout`, `WithConditionEvaluator`). Compose, or
   refuse at construction? Spec §5 lists it **open**. Pick one.
4. **Is "count once per env" actually achievable** where the plan puts it? If the
   count cannot be attached to the variable map's lifetime, D2 costs more than the
   866 ns/op it saved and becomes self-defeating.
5. **The allow-list 400 rendering** — is `jsonschema` really the only strategy with
   structured leaves? Re-derive the strategy set and each one's error type.
6. **The 1 MiB and 256 KiB defaults** are judgement calls. Attack them against the
   repo's own fixtures.
7. **Re-measure the O(n²) ladder at n = 10 000.** The ~2.4 s figure behind the
   default is *extrapolated*, not measured.
8. ⚠ **One lens must be the counting lens** (rule #9). The B3 lineage rotted
   enumerations in both rounds; §5 below re-derives every count this delivery uses.
   Assume there is one more.
9. ⚠ **One lens must do the interaction pass** (rule #9). Audit #2 failed on
   interactions. Spec §5 is the author's own pairwise table — **attack it, and find
   the pairs it omits.**
10. ⚠ **Every auditor gets the step-0 worktree check**: verify the bundle is present,
    STOP if not. Create worktrees **detached at the bundle commit**.
11. ⚠ **Do not re-audit the identity material.** ADR-0185 is a separate, later
    delivery. If a finding depends on a symbol ADR-0185 introduces, it is out of
    scope here — say so rather than folding it in.

---

## 1. Fan-out rules

- **Fan out by Go package.** Concurrent agents in one package break each other's
  `go test` compile even on disjoint files.
- **Phases 1 and 2 stay INLINE in the controller** — `internal/expreval` and
  `runtime/validation` are both consumed by later phases, and phase 2 changes an
  error's *type* discipline that phase 4 depends on.
- **Docker:** the standing carve-out covers the Verification runs only. Every package
  in this delivery is **container-free**: `internal/expreval`, `runtime/validation`,
  `runtime`, `transport/http/*`, `action/httpcall`, `service`. **No agent needs
  Docker**; say so explicitly in each brief so nobody asks.
- **`golangci-lint`:** probe `command -v golangci-lint` and run it; if absent, say so
  and offer install-or-skip. Never substitute `go vet`.
- ⚠ **A mutation ablation gets its own `git worktree`** — a live ablation in a shared
  tree once cost another agent ~40 minutes as a phantom hang.

---

## 2. Phase table

| # | package(s) | ADR decision | depends on | fan-out |
|---|---|---|---|---|
| 1 | `internal/expreval` | D2 | — | **controller, inline** |
| 2 | `runtime/validation` | D5 | — | **controller, inline** |
| 3 | `runtime` | D2 (plumbing) | 1 | 1 agent |
| 4 | `transport/http/httpcore` | D1, D4, D5 | 1, 2 | 1 agent |
| 5 | `transport/http/stdlib` \| `gin` \| `fiber` | D1 | 4 | **3 agents in parallel** |
| 6 | `action/httpcall` | D3 | 1 | 1 agent (‖ 4, 5) |
| 7 | `service` | D1(b) | — | 1 agent (‖ 4, 5, 6) |
| 8 | `transport/http/parity` | test fallout | 5 | 1 agent |
| 9 | docs | all | 8 | controller |

Phases 4, 6 and 7 are disjoint and may run concurrently. Phase 5's three agents are
the only true fan-out.

---

## 3. Phases

### Phase 1 — `internal/expreval`: an input bound  ⚠ CONTROLLER, INLINE

⚠ **No `ctx` methods.** ADR-0186 D2 explicitly drops the `ctx` on
`ConditionEvaluator`. Do not add `EvalBoolContext` or its siblings; the existing three
methods keep their signatures, and `runtime`'s two exported options keep theirs.

**Symbols:**
- `func WithMaxEnvElements(n int) Option` — refuses an env whose bounded element count
  exceeds `n`, with a new `ErrEnvTooLarge`. `0` = unbounded (current behaviour).
- The count is **supplied with the env, not computed per evaluation** (see below).

**Tests, and what makes each fail today:**

1. `TestWithMaxEnvElementsRefusesOversizedEnv` — an env whose `vars` holds more than
   `n` elements returns `ErrEnvTooLarge`.
   **Fails today:** the option does not exist → compile error.
2. `TestWithMaxEnvElementsZeroIsUnbounded` — the control. Without it, an implementer
   who treats `0` as "reject everything" bricks every existing consumer and the other
   test still passes.
3. `TestMaxEnvElementsCountsNestedCollections` — a `vars` holding one key whose value
   is a 20 000-element slice must be refused at `n = 10 000`.
   **Fails today:** compile error. ⚠ **Fixture check:** the oversize must be *nested*,
   not top-level — a fixture with 20 000 top-level keys cannot fail for the reason
   this test names.
4. `BenchmarkEvalBoolWithBoundEnabled` vs `BenchmarkEvalBoolWithBoundDisabled` —
   ⚠ **this is the test that decides D2.** ADR-0186 D2 drops the `ctx` to avoid
   866 ns/op and is self-defeating if the replacement costs more. The measured cost of
   counting is **~84 ns/op** on a typical env and **~19 µs at n = 10 000**, so a
   per-evaluation count is 20–60× worse than what it replaced.
   **The benchmark must show the bound adds no per-evaluation walk.** If it does, stop
   and escalate — the decision is wrong, not the code.

**Verify:** `go test -race -count=1 ./internal/expreval/...` then
`go test -bench=. -run '^$' ./internal/expreval/...`

---

### Phase 2 — `runtime/validation`: the structured error survives  ⚠ CONTROLLER, INLINE

⚠ **This phase exists because the fix cannot live at the transport.**
`runtime/validation/gate.go:45` is `fmt.Errorf("%w: %s", ErrInvalidInput, err.Error())`
— `%s` flattens the strategy's typed error to a string, so
`errors.As(err, **jsonschema.ValidationError)` is **false** by the time
`ClassifyError` runs. Audit #2 executed this.

- The gate preserves the strategy's error (`%w`, or an explicit typed wrapper), and
  **renders the client-safe message itself**, where the type is still available.
- The rendering is an **allow-list**: structured leaves for `jsonschema`
  (`InstanceLocation` + `ErrorKind.KeywordPath()` ⇒ `at '/ssn': violates pattern`),
  static `"invalid input"` for everything else.
- ⚠ **`definition/model/validate/expr/expr.go:64,68` stops echoing `v.source[i]`** on
  the runtime-validation path. It `%q`s the predicate source into the 400 body — the
  same disclosure ADR-0186 Context §5 establishes for 403.

**Tests, and what makes each fail today:**

1. `TestJSONSchemaValidationErrorIsRenderedWithoutTheSubmittedValue` — input
   `{"ssn":"123-45-6789"}` against a `pattern`-constrained schema; the message
   contains `"ssn"` and `"pattern"` and **not** `"123-45-6789"`.
   **Fails today:** executed —
   `- at '/ssn': '123-45-6789' does not match pattern '^[0-9]{3}$'` is the message.
   ⚠ **Add a `maxLength` row**: its leaf discloses `got 11, want 3`, so a
   `pattern`-only fix passes a `pattern`-only test.
2. `TestExprValidationErrorDoesNotEchoPredicateSource`.
   **Fails today:** `expr.go:64,68` `%q` the source.
   ⚠ **This is the test that proves the allow-list, not the special case.** Without
   it, a fix confined to `jsonschema` is green.
3. `TestUnknownStrategyErrorIsRenderedStatically` — a `callback` strategy returning an
   arbitrary string yields `"invalid input"`.
   **Fails today:** the message is passed through verbatim.
4. `TestStructuredErrorSurvivesTheGate` — `errors.As` finds the strategy's typed error
   after the gate.
   **Fails today:** `gate.go:45` stringifies it. ⚠ This is the falsifier for the whole
   phase; assert on `errors.As`, not on message text.

**Verify:** `go test -race -count=1 ./runtime/validation/... ./definition/model/validate/...`

---

### Phase 3 — `runtime`: plumb the bound

- `runtime.WithMaxEvalElements(n int)`, default **10 000**, constructing the driver's
  evaluator and reaching the engine through the existing `StepOptions.Evaluator` seam
  (ADR-0056). ⚠ This is D2's plumbing; without it the bound is a zombie knob and the
  ADR-0162 failure repeats.
- ⚠ **It must not silently overwrite.** `WithExpressionTimeout`
  (`runtime/processdriver_options.go:198`) and `WithConditionEvaluator` (`:217`)
  already assign `driver.conditionEval`. **Spec §5 lists this OPEN** — compose (wrap a
  consumer-supplied evaluator) or refuse at construction with a named error. The audit
  picks; the implementer does not.

**Tests:**

1. `TestWithMaxEvalElementsBoundsTheDriverEvaluator`.
   **Fails today:** the option does not exist → compile error.
2. `TestMaxEvalElementsDefaultIsApplied` — a driver built with no options still
   refuses an oversize env. **Fails today:** no bound exists at all.
3. `TestOptionCollisionIsNotSilent` — whichever resolution the audit picks, this
   asserts it. ⚠ **Fails today** only after the decision is made; if the audit picks
   "refuse", assert the named error; if "compose", assert both behaviours survive.

**Verify:** `go test -race -count=1 ./runtime` ⚠ **not** `./runtime/...`, which is not
container-free.

---

### Phase 4 — `transport/http/httpcore`: caps, redaction, classification

- `CustomizeConfig.MaxBodyBytes` (default **1 MiB**) and a new `ErrBodyTooLarge`.
- `ClassifyError`: **413 arm placed BEFORE the 400 arm**, with a comment naming the
  order-dependence; 403 static; 400 renders what phase 2 gives it; correlation id on
  every body (OTel span id when a span is recording, else a random hex id).
- `RedactVariables` applied at the **`ProcessInstance` → response boundary**, in a
  helper every read path calls.
- `view.go:31` copies rather than aliases.
- Correct `CustomizeConfig.Logger`'s godoc — it says *"receives 5xx raw error
  details"* and now also receives 400's and 403's.

**Tests, and what makes each fail today:**

1. `TestOversizedBodyClassifiesAs413NotBadRequest`.
   **Fails today:** the sentinel does not exist → compile error. ⚠ **And it must fail
   against a bare arm-append**: add a case where the error wraps **both**
   `ErrBadInput` and `ErrBodyTooLarge` and assert **413**. Without that row the test
   passes against the ordering bug audit #2 found.
2. `TestClassifyErrorDoesNotEchoPredicateSource` — build a real 403 from an erroring
   attribute predicate; assert the body omits the identifier.
   ⚠ **Mandatory control:** `require.Contains(t, err.Error(), "internalApprovalLimit")`
   *before* classifying. Both the deny path and the eval-error path produce 403 and
   only the latter leaks; without this a predicate that quietly returns `false` makes
   the assertion pass vacuously.
3. `TestInstanceViewCopiesVariables` — mutate the returned map, assert the source is
   unchanged. **Fails today:** `view.go:31` aliases.
   ⚠ Do **not** restate the withdrawn *"mutates instance state"* claim in the comment.
4. `TestRedactionAppliesUnderCustomInstanceMapper` — set both `InstanceMapper` and
   `RedactVariables`; assert the mapper never sees the redacted key.
   **Fails today:** compile error; and against a fix placed inside `NewInstanceView`
   this fails while a default-mapper test passes.
5. `TestSnapshotEndpointRedactsVariables` and `TestActionableViewRedactsTaskVars` —
   ⚠ **the controls that decide D4's placement.** `GetInstanceSnapshot`
   (`endpoints.go:60`) returns the raw `service.ProcessInstance`, whose JSON carries
   `variables` (`service/instance.go:125`, assigned `:344`); `GetActionableView`
   (`:72`) renders task vars. Both take **no mapper**.
   **Fails today:** no redaction exists — and each **fails against a fix confined to
   `mapInstance`**, which is the whole point.
6. `TestCorrelationIDInBodyMatchesTheLogRecord` — the entire justification for
   blanking 403 is that an operator can join the two. **Fails today:** no id exists.

**Verify:** `go test -race -count=1 ./transport/http/httpcore/...`

---

### Phase 5 — `transport/http/{stdlib,gin,fiber}`  ⚠ THREE PARALLEL AGENTS

One agent per package. **Never two agents in one package.**

Each caps the body at all **13** decode sites in its `groups.go`:
- `stdlib` — `http.MaxBytesReader` before `json.NewDecoder`;
- `gin` — assign `gc.Request.Body = http.MaxBytesReader(...)` **before**
  `ShouldBindJSON`;
- `fiber` — a `len(c.Body())` pre-check before `c.Bind().JSON`.
  ⚠ **`ASSUMPTION (unverified)` — the fiber agent's FIRST task is to establish this
  mechanism by execution and report back before editing 13 sites.**

⚠ **The oversize path returns the BARE `httpcore.ErrBodyTooLarge`.** Every decode site
today wraps in `fmt.Errorf("%w: %w", httpcore.ErrBadInput, err)` — keep that for
**decode** failures only. An oversize error carrying `ErrBadInput` classifies as
**400**, because `ClassifyError`'s arms are ordered.

**Test per package:** `TestOversizedBodyReturns413`.
**Fails today:** no cap exists, so the body is read in full.
⚠ **Falsifier to state in each brief:** *it also fails against an implementation that
keeps the `ErrBadInput` wrapper.*

**Verify (per agent):** `go test -race -count=1 ./transport/http/<pkg>/...`

---

### Phase 6 — `action/httpcall`: SSRF posture  *(parallel with 4, 5, 7)*

- Route `WithURLExpr` through `internal/expreval`.
- Restricted transport for **expression-derived** URLs only: a `net.Dialer.Control`
  hook refusing loopback, link-local (`169.254.0.0/16`, `fe80::/10`), RFC1918/ULA and
  cloud metadata; `CheckRedirect` refusing a host outside the allowlist.
- `WithAllowedHosts([]string)`, `WithUnrestrictedTransport()`.
- `WithBaseURL` **unchanged**.
- ⚠ **Open (spec §5):** does phase 1's env bound apply here? The audit settles it; if
  yes, this phase wires it.

**Tests:**

1. `TestURLExprRefusesLinkLocalAddress` — `vars.url = "http://169.254.169.254/…"`.
   **Fails today:** `grep -rnE "CheckRedirect|expreval" action/httpcall/` → 0, so the
   request is attempted. ⚠ **Do not dial a real link-local address in CI** — assert on
   the dialer control's refusal, not a network result.
2. `TestURLExprRefusesRedirectToLoopback` — an `httptest` server that 302s to
   `127.0.0.1`. **Fails today:** `http.Client` follows by default.
3. `TestBaseURLIsUnrestricted` — ⚠ **the ADR's load-bearing control.** A static
   `WithBaseURL` pointing at the `httptest` loopback server still works. Without it an
   implementer who over-applies the restriction breaks every existing user and the
   suite stays green.
4. `TestAllowedHostsOptsBackIn` — the escape hatch is reachable.

**Verify:** `go test -race -count=1 ./action/httpcall/...`

---

### Phase 7 — `service`: the variable payload bound  *(parallel with 4, 5, 6)*

- `WithMaxVariableBytes(n int64) Option`, default **256 KiB**, refused before persist
  with a sentinel classified 413.
- ⚠ **Document it as a payload/storage bound.** ADR-0186 D2 states the CPU bound is
  `WithMaxEvalElements`; this bundle's own O(n²) table refutes the draft's framing of
  256 KiB as a CPU mitigation.

**Tests:**

1. `TestWithMaxVariableBytesRefusesOversizedVariables`.
   **Fails today:** the option does not exist → compile error.
2. `TestMaxVariableBytesDefaultApplies` — the control for the default.

**Verify:** `go test -race -count=1 ./service/...`

---

### Phase 8 — `transport/http/parity`

Add parity cases asserting all three adapters agree on **413** for an oversize body
and on the blanked 403.
**Fails today:** no adapter caps anything.

**Verify:** `go test -race -count=1 ./transport/http/...`

---

### Phase 9 — documents (controller)

- `SECURITY.md`: the at-rest posture (D6) naming `wrkflw_instances.snapshot` and
  `wrkflw_journal.trigger`; the SSRF default and how to opt out; what a 400/403 body
  does and does not contain.
- `CHANGELOG.md` + `STABILITY.md`: **`ErrorBody` is a breaking wire change** — the 403
  message becomes static, the 400 message changes shape, and a correlation-id field is
  added. Consumers matching on `ErrorBody.Message` break. ⚠ The B3 draft omitted this
  from its breaking-change list.
- Add the `SECURITY:` caveat to the **instance and task** route groups in all three
  adapters — today it exists at exactly three non-test sites, all on the **admin**
  group (`stdlib/groups.go:189`, `gin:204`, `fiber:209`), which implies the others are
  safe by omission.
- Close backlog **54, 65, 98, 99, 104**; record **100/101** as posture-answered,
  mechanism-deferred.
- **Do not** close 51, 52, 53, 103, 124, 102, 32, 60, 91, 96, 106.
- `docs/plans/HANDOVER.md` + this plan's `▶ Progress`, per rule #10.

---

## 4. Enumerations, re-derived at the anchor commit

| what | value |
|---|---|
| decode sites to cap | **39** — `stdlib` 13 `json.NewDecoder`, `gin` 13 `ShouldBindJSON`, `fiber` 13 `c.Bind().JSON`, `httpcore` 0; all in each package's `groups.go` |
| …already capped by us | **0** (`grep -rnE "MaxBytesReader\|BodyLimit" transport/`) |
| …capped by the framework | fiber's 13, via `fiber.DefaultBodyLimit` = 4 MiB — **not ours** |
| `ClassifyError` arms | **6**, ordered: 404 `:28`, 403 `:32`, 409 `:34`, 400 `:36-50`, 422 `:51`, default 500 `:57` |
| …echoing `err.Error()` | **5** (all but 500) |
| sentinels in the 400 arm | **8**, across 5 `errors.Is` groups |
| validation strategies under `ErrInvalidInput` | **4** — `jsonschema`, `expr`, `avro`, `callback`; only `jsonschema` yields structured leaves |
| `SECURITY:` caveat sites | **3**, all admin-only |
| `mapInstance` call sites | **6** — and **2** further read endpoints take no mapper at all |
| routes | **26** = 9 non-admin + 15 admin + 2 health; **no definition-deploy route** |

⚠ **The B3 lineage rotted an enumeration in both audit rounds, and in both cases the
arithmetic was right — the failure was the grep's NET and the citation's ANCHOR.**
Every number above is a closed set named from source, not a count copied forward.

---

## 5. Verification checklist

- [ ] **Rule-#9 audit** over this bundle — three lenses including a **counting** lens
      and an **interaction** lens, three detached worktrees, step-0 presence check.
      **Nothing below starts until this is checked.**
- [ ] Every phase's tests observed **RED before GREEN**, in the transcript.
- [ ] `go test -race -coverprofile=cover.out ./... && scripts/coverage.sh cover.out`
      ≥ 85 % over hand-written code, hot paths and their failure branches first.
      Probe `docker info`; if the daemon is down, say so and label any container-free
      subset as the partial result it is.
- [ ] `go test ./...` from the repo root — no regressions.
- [ ] `golangci-lint run ./...` repo-wide (not package-scoped) clean.
- [ ] `go vet ./...`
- [ ] `go build ./examples/...`
- [ ] **Phase 1's benchmark shows the env bound adds no per-evaluation walk.**
- [ ] Documents describe what shipped; per rule #11 expect implementation to correct
      the design and **amend the ADR in the same bundle**, with the measurement.
- [ ] Sweep the diff's comments for unexecuted claims and over-reaching quantifiers.
- [ ] `/code-review` — all findings fixed, folded via `--amend`.
- [ ] `/security-review` — all findings fixed, folded via `--amend`.
- [ ] `HANDOVER.md` rewritten in place; `▶ Progress` updated; memory topic file
      written and pointing at `HANDOVER.md`.

## 6. Commit shape

One feature bundle, one commit, amended (never stacked):

```
feat(transport,expreval,httpcall): bound untrusted input and stop leaking it back
```

carrying implementation, tests, the spec, ADR-0186, this plan and the phase-9 docs.

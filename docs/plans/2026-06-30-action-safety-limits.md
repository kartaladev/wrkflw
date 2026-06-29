# Action-execution Safety Limits + Linter Rollout — Implementation Plan

> **For agentic workers:** strict TDD (visible RED → GREEN per symbol). Steps use `- [ ]`.

**Goal:** Bound httpcall response/body reads, add a default-on action-execution timeout, and adopt
security linters (gosec/bodyclose/errorlint) with all findings triaged.

**Architecture:** Two additive functional options (`httpcall.WithMaxResponseSize`,
`runtime.WithActionTimeout`) plus a `.golangci.yml` linter expansion. Engine/model untouched.

**Tech Stack:** Go 1.25, stdlib (`io.LimitReader`, `context.WithTimeout`), golangci-lint v2, gosec.

## Global Constraints

- TDD strict: no production code before a failing test; visible RED via `go test ./<pkg>/...`.
- Black-box tests (`<package>_test`); table form when ≥2 cases share a call shape (assert-closure style).
- engine/ + model/ production code stays **zero-diff**.
- Error messages carry the `workflow-` prefix; assert with `errors.Is`.
- ≥85% coverage on touched packages; full `go test -race ./...` green; `golangci-lint run ./...` clean.
- ADRs: **0076** (safety limits), **0077** (security-linter adoption).

---

### Task 1: httpcall response & body size cap (P0-4, ADR-0076)

**Files:**
- Modify: `action/httpcall/httpcall.go` (struct field, `NewHTTPCall` default, new `WithMaxResponseSize`
  option, sentinel `ErrBodyTooLarge`, `readAllCapped` helper, two read sites ~L223 + ~L289)
- Test: `action/httpcall/httpcall_test.go` (or a new `httpcall_cap_test.go`)

**Produces:** `httpcall.WithMaxResponseSize(n int64) Option`, `httpcall.ErrBodyTooLarge`,
default cap `10 << 20`.

- [ ] **Step 1 (RED):** Table test `TestHTTPCall_ResponseSizeCap` with a stub `*http.Client` (or
  `httptest.Server`) returning bodies of varied sizes:
  - body ≤ cap → success, `httpBody` populated.
  - body > cap (set `WithMaxResponseSize(16)`, return 64 bytes) → `errors.Is(err, httpcall.ErrBodyTooLarge)`
    and `action.IsRetryable(err) == false` (NonRetryable).
  - `WithMaxResponseSize(0)` + large body → success (unlimited).
  - default (no option) honours 10 MiB (assert a >10 MiB body errors; use a cheap large reader).
- [ ] **Step 2:** `go test ./action/httpcall/...` → FAIL (`WithMaxResponseSize`/`ErrBodyTooLarge` undefined).
- [ ] **Step 3 (GREEN):**
  - Add `maxResponseSize int64` to `httpCall` struct.
  - In `NewHTTPCall` literal add `maxResponseSize: defaultMaxResponseSize` with
    `const defaultMaxResponseSize = 10 << 20`.
  - Add `var ErrBodyTooLarge = errors.New("workflow-httpcall: body exceeds max size")`.
  - Add `func WithMaxResponseSize(n int64) Option { return func(h *httpCall) { h.maxResponseSize = n } }`.
  - Add helper:
    ```go
    func readAllCapped(r io.Reader, max int64) ([]byte, error) {
        if max <= 0 {
            return io.ReadAll(r)
        }
        b, err := io.ReadAll(io.LimitReader(r, max+1))
        if err != nil {
            return nil, err
        }
        if int64(len(b)) > max {
            return nil, ErrBodyTooLarge
        }
        return b, nil
    }
    ```
  - Response site (~L289): `raw, err := readAllCapped(resp.Body, h.maxResponseSize)`; on
    `errors.Is(err, ErrBodyTooLarge)` return `action.NonRetryable(fmt.Errorf("workflow-httpcall: response body: %w", err))`.
  - Validator-buffer site (~L223): `raw, err := readAllCapped(r, h.maxResponseSize)`; same NonRetryable
    wrap with `"workflow-httpcall: request body: %w"`.
- [ ] **Step 4:** `go test ./action/httpcall/...` → PASS.
- [ ] **Step 5:** Commit `feat(action/httpcall): cap response/body reads (default 10 MiB)`.

### Task 2: action-execution timeout (P0-5, ADR-0076)

**Files:**
- Modify: `runtime/runner.go` (`Runner.actionTimeout` field, `NewRunner` default, `withActionTimeoutCtx`
  helper, two `safeActionDo` call sites L772 + L805)
- Create/Modify: `runtime/options.go` or wherever `Option` lives — add `WithActionTimeout`
- Test: `runtime/runner_action_timeout_test.go`

**Produces:** `runtime.WithActionTimeout(d time.Duration) Option`, default `30 * time.Second`,
`d <= 0` disables.

- [ ] **Step 1 (RED):** Table test `TestRunner_ActionTimeout` driving a Runner (MemStore + a catalog
  with a blocking action that respects ctx, e.g. `select { case <-ctx.Done(): return ctx.Err(); case <-time.After(big): }`):
  - `WithActionTimeout(20*time.Millisecond)` + action blocking 5s → instance reaches a **retryable**
    failed/incident state (assert via the produced `ActionFailed`/incident; action got `ctx.Err()`).
  - `WithActionTimeout(0)` + fast action → completes normally (no timeout applied).
  - fast action under default → completes.
- [ ] **Step 2:** `go test ./runtime/... -run TestRunner_ActionTimeout` → FAIL (`WithActionTimeout` undefined).
- [ ] **Step 3 (GREEN):**
  - Add `actionTimeout time.Duration` to `Runner`; set `actionTimeout: defaultActionTimeout` in `NewRunner`
    with `const defaultActionTimeout = 30 * time.Second`.
  - Add `func WithActionTimeout(d time.Duration) Option { return func(r *Runner) { r.actionTimeout = d } }`.
  - Add helper:
    ```go
    func (r *Runner) actionContext(parent context.Context) (context.Context, context.CancelFunc) {
        if r.actionTimeout <= 0 {
            return parent, func() {}
        }
        return context.WithTimeout(parent, r.actionTimeout)
    }
    ```
  - Wrap both `safeActionDo` sites:
    ```go
    actx2, cancel := r.actionContext(actx)
    out, err := safeActionDo(actx2, a, cmd.Input)
    cancel()
    ```
    (InvokeAction at L772 over `actx`; InvokeCancelAction at L805 over `ctx`.)
- [ ] **Step 4:** `go test ./runtime/...` → PASS.
- [ ] **Step 5:** Commit `feat(runtime): default-on 30s action-execution timeout (WithActionTimeout)`.

### Task 3: security-linter rollout + finding triage (P1-D, ADR-0077)

**Files:** `.golangci.yml`; per-finding source files; constructor validation in
`internal/persistence/mysql/{relay,call_links,lister}.go` (+ tests).

- [ ] **Step 1:** Add `gosec`, `bodyclose`, `errorlint` to `.golangci.yml` `linters` (keep generated
  `transport/grpc/workflowpb` excluded). Run `golangci-lint run ./...` → capture the full finding set
  (gosec ~36 + any bodyclose/errorlint).
- [ ] **Step 2 (RED, for behaviour-changing fixes only):** For the G201/G202 `LIMIT %d` sites, write a
  test asserting the mysql constructors reject a negative batch/limit/fetch (e.g.
  `TestNewMySQLRelay_RejectsNegativeBatch`). Run → FAIL.
- [ ] **Step 3 (GREEN):** Add non-negative validation in those constructors; then annotate the
  Sprintf/concat sites `//nolint:gosec // G20x: int-only LIMIT; placeholder impossible with FOR UPDATE; validated >= 0`.
- [ ] **Step 4:** Triage the remaining findings per the spec table — each disposition is either a real
  guarded conversion **with a test** (G115 where a real overflow is reachable) or a `//nolint:gosec`
  with a one-line reason (jitter G404; bounded page-limit G115; test-helper G101). Exclude generated
  `workflow.pb.go` (G103) via path config. Resolve every `bodyclose`/`errorlint` finding (close bodies;
  `errors.Is`/`As`; `%w`).
- [ ] **Step 5:** `golangci-lint run ./...` → 0 issues. `go test -race ./...` → green. Commit
  `chore(lint): enable gosec/bodyclose/errorlint and triage findings`.

### Task 4: ADRs + docs

- [ ] ADR `docs/adr/0076-action-execution-safety-limits.md` (Nygard) — the two options, default-on
  rationale, wall-clock mechanism, behaviour-change note.
- [ ] ADR `docs/adr/0077-security-linter-adoption.md` — the three linters + nolint-with-reason policy.
- [ ] CHANGELOG `Unreleased` entries: response cap; **default 30s action timeout (behaviour change)**;
  security linters. Update the production-readiness backlog statuses (P0-4/P0-5/P1-D ✅).
- [ ] Commit `docs(adr): 0076 safety limits + 0077 security linters`.

## Verification checklist

- [ ] `go test -race ./...` green (27 pkgs).
- [ ] `golangci-lint run ./...` 0 issues (with the three new linters).
- [ ] `gosec ./...` 0 unjustified findings.
- [ ] httpcall + runtime touched packages ≥ 85%.
- [ ] engine/ + model/ `git diff` empty.
- [ ] opus whole-branch review before merge.

## Self-review notes

- Spec coverage: P0-4 → Task 1; P0-5 → Task 2; P1-D → Task 3; ADRs/docs → Task 4. Complete.
- Type consistency: `WithMaxResponseSize`/`ErrBodyTooLarge`/`WithActionTimeout`/`actionContext` used
  consistently across tasks.
- Risk: Task 2's default-on timeout may affect existing runtime tests — Step 4 runs the full suite to catch it.

# Engine Simplification — Phase A (Safe Sweep) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to
> implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
> Spec: `docs/specs/2026-07-13-engine-simplification-program.md` (Phase A section).

**Goal:** Remove ~300-400 lines of pure duplication from the `engine` package, make dispatch and
state subsystems findable, and make the purity guarantee machine-checked — all without changing
one bit of observable behavior (except A0, which adds a test).

**Architecture:** Behavior-preserving extraction of repeated code into named helpers; mechanical
file splits by subsystem; one new activity interface to retire four parallel type switches. The
existing engine test suite (green baseline: 87.6% coverage, lint 0) is the oracle — every task
proves the suite green immediately before and immediately after its change.

**Tech Stack:** Go 1.25, standard library `slog`, `expr-lang/expr` (unchanged here). No new deps.

## Global Constraints

- Go 1.25; module `github.com/kartaladev/wrkflw`.
- **No regression is a hard requirement.** After every task: `go test ./engine/...` green,
  `go test ./...` green from repo root, `golangci-lint run ./engine/...` clean, `engine`
  coverage ≥85% (baseline 87.6% — must not drop).
- **Engine core stays pure:** no new import of transport, `internal/persistence`, watermill,
  gocron, clockwork, otel, or `time.Now`/`time.Since`. (A0 makes this machine-checked.)
- **Behavior-preserving tasks (A1-A10) add no new test** and edit no existing test except
  mechanical helper-name references. The proof is: suite green before, suite green after, and
  the diff contains only the extraction/move. **A0 is red-first** (new test must fail first).
- Follow Go skills: every implementer starts from `cc-skills-golang:golang-how-to` and loads the
  task-relevant skills; the project's `table-test`, `use-mockgen`, `use-testcontainers` skills
  override the corresponding `golang-testing` guidance.
- Prefer black-box tests (`engine_test` package) where tests are touched.
- One commit per task, Conventional Commits scoped `refactor(engine):` (or `test(engine):` for
  A0), ending with the `Co-Authored-By` trailer.
- Do NOT change any exported symbol's name, signature, or doc-visible behavior. `go doc ./engine`
  surface must be identical after each task (A8/A9/A10 are moves/renames of unexported code and
  test files only).

---

### Task A0: Machine-check the purity guarantee

**Files:**
- Modify: `engine/purity_test.go` (currently enforces only the OTel import ban)

**Interfaces:**
- Produces: nothing consumed by later tasks. Self-contained test hardening.

**Why red-first:** this task adds real assertions; the red state proves the new checks actually
catch a violation rather than vacuously passing.

- [ ] **Step 1: Read the current test.** Read `engine/purity_test.go` in full to learn its
  existing import-walking helper (it already lists non-test imports of `.` and `../definition`
  for the OTel grep). Reuse that walker.

- [ ] **Step 2: Write the failing assertions.** Extend the test with:
  (a) a denylist check — no non-test import path of the `engine` package may contain any of:
  `"/transport/"`, `"/internal/persistence"`, `"watermill"`, `"gocron"`, `"clockwork"`, or the
  existing `"go.opentelemetry.io"`; and
  (b) an AST check — walk every non-test `.go` file in the package with `go/parser` +
  `go/ast`, and fail if any `CallExpr` selects `time.Now`, `time.Since`, or `time.Tick`
  (i.e. a `SelectorExpr` whose `X` is the `time` package ident and whose `Sel` is one of those).
  To force a genuine red state first, temporarily add a `deny := append(denied, "definition")`
  style tightening OR point the AST check at a throwaway fixture containing `time.Now()` and
  assert it is detected. Prefer a self-contained fixture assertion so the red state is
  reproducible: add a sub-test `TestPurity_ASTDetectsWallClock` that runs the detector over a
  small in-test source string containing `time.Now()` and asserts it reports a violation.

- [ ] **Step 3: Run to verify red.**
  Run: `go test -run '^TestPurity' ./engine/... -v`
  Expected: FAIL — the new detector sub-test fails until the detector exists, or the denylist
  flags nothing yet (write the detector helper as returning `nil` first to see the fixture
  sub-test go red).

- [ ] **Step 4: Implement the detector helpers** so the fixture sub-test passes AND the real
  package scan passes (the package is pure today, so the denylist + AST scan over real files
  must report zero violations — that is the green state proving the checks run over real code).

- [ ] **Step 5: Run to verify green.**
  Run: `go test -run '^TestPurity' ./engine/... -v && go test ./engine/... 2>&1 | tail -1`
  Expected: PASS; full engine suite `ok`.

- [ ] **Step 6: Commit.**
  ```bash
  git add engine/purity_test.go
  git commit -m "test(engine): enforce full purity guarantee (denylist + wall-clock AST check)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
  ```

---

### Task A6: Drop dead per-strategy type-assertion guards

*(Ordered before A5/A9 because it is the smallest edit to the strategy bodies and lowers the
noise the later strategy tasks must read.)*

**Files:**
- Modify: `engine/step_nodes.go` (the `enter` methods) and possibly `engine/step.go` (`drive`)

**Interfaces:**
- Consumes: the `nodeStrategies` map (keyed by `model.NodeKind`) and `nodeStrategy.enter`.
- Produces: unchanged `nodeStrategy` interface and `enter` signature.

**Background:** ~11 strategies open with `if _, ok := node.(X); !ok { tok.State =
TokenWaitingCommand; return nil, false, nil }`. The registry key already guarantees the kind, so
the guard is unreachable for a well-formed node. The type assertion for the *value* (e.g.
`n := node.(activity.ServiceTask)`) is still needed; only the guard-with-early-return is dead.

- [ ] **Step 1: Confirm green baseline.** Run: `go test ./engine/... 2>&1 | tail -1` → `ok`.

- [ ] **Step 2: Locate all guard sites.**
  Run: `grep -n 'ok := node\.\|; !ok {' engine/step_nodes.go` and read each `enter` method.
  Baseline sites (verify, they drift): serviceTask, businessRuleTask, script/manual, receiveTask,
  subProcess, userTask, sendTask, eventBasedGateway, intermediateCatch/throw, compensationThrow.

- [ ] **Step 3: Replace guarded assertions with direct assertions.** For each site, change
  ```go
  n, ok := node.(activity.ServiceTask)
  if !ok { tok.State = TokenWaitingCommand; return nil, false, nil }
  ```
  to `n := node.(activity.ServiceTask)`. If a reviewer would want defensiveness retained, add a
  SINGLE guard in `drive()` (`step.go`) that parks a token whose `node.Kind()` has no registered
  strategy — but do NOT keep the per-strategy guards. Prefer the direct assertion; the map key
  is the invariant.

- [ ] **Step 4: Run to verify green (behavior unchanged).**
  Run: `go test ./engine/... 2>&1 | tail -1`  Expected: `ok`.
  Run: `golangci-lint run ./engine/... 2>&1 | tail -1`  Expected: `0 issues.`

- [ ] **Step 5: Commit.**
  ```bash
  git add engine/step_nodes.go engine/step.go
  git commit -m "refactor(engine): drop dead per-strategy type-assertion guards

The nodeStrategies map key already guarantees node.Kind(); the per-strategy
!ok early-return was unreachable for well-formed nodes.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
  ```

---

### Task A5: Collapse the two action-task strategies

**Files:**
- Modify: `engine/step_nodes.go` (`serviceTaskStrategy`, `businessRuleTaskStrategy`, registry)
- Modify: `engine/step_timers.go` (`reinvokeServiceAction`, ~200-229) to reuse the shared body

**Interfaces:**
- Produces: `emitActionInvoke(c *stepCtx, tok *Token, node model.Node) (cmds []Command, err error)`
  — the shared "resolve action name → `InvokeAction` (park token) → `armBoundaries` → append"
  body used by service-task, business-rule-task, and timer re-invocation.

**Background:** `serviceTaskStrategy.enter` (~83-105) and `businessRuleTaskStrategy.enter`
(~113-135) are byte-for-byte identical except the type assertion. The same InvokeAction+park+
armBoundaries sequence recurs in `reinvokeServiceAction`.

- [ ] **Step 1: Confirm green baseline.** `go test ./engine/... 2>&1 | tail -1` → `ok`.

- [ ] **Step 2: Read the three sites** and confirm the only difference between the two
  strategies is `activity.ServiceTask` vs `activity.BusinessRuleTask` (both expose the same
  action-name accessor — confirm the accessor name, e.g. `.Action`/`.ActionName()`).

- [ ] **Step 3: Introduce `emitActionInvoke`.** Extract the shared body into one unexported
  helper. Register a single `actionTaskStrategy` (or keep two zero-size strategy structs both
  delegating to `emitActionInvoke`) for both `KindServiceTask` and `KindBusinessRuleTask` in the
  `nodeStrategies` map. Update `reinvokeServiceAction` to call `emitActionInvoke`. Keep the
  action-name resolution (which differs per kind only in the concrete type) inside the helper via
  the existing `node_accessors.go` accessor or a small type switch — do NOT duplicate it.

- [ ] **Step 4: Run to verify green.**
  Run: `go test ./engine/... 2>&1 | tail -1` → `ok`;
  `go test -run 'ServiceTask|BusinessRule|Reinvoke|Timer' ./engine/... -count=1 2>&1 | tail -1`.
  `golangci-lint run ./engine/... 2>&1 | tail -1` → `0 issues.`

- [ ] **Step 5: Commit.**
  ```bash
  git add engine/step_nodes.go engine/step_timers.go
  git commit -m "refactor(engine): unify service/business-rule task strategies via emitActionInvoke

Both strategies and reinvokeServiceAction shared a byte-identical
InvokeAction+park+armBoundaries body; extracted to one helper.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
  ```

---

### Task A7: Retire the four parallel type switches in node_accessors.go

**Files:**
- Modify: `engine/node_accessors.go`
- Modify: `definition/activity/*.go` — add the interface methods to each activity type OR add a
  single interface the types already satisfy (see Step 3; prefer the zero-code-change option)

**Interfaces:**
- Produces: a small interface consumed via one type assertion in `node_accessors.go`, replacing
  `compensateActionOf` / `cancelActionOf` / `recoveryFlowOf` / `completionActionOf`'s 7-case
  switches. Keep the existing accessor function names and signatures (callers unchanged).

**Background:** the four functions each enumerate the same 7 activity types (`ServiceTask`,
`UserTask`, `ReceiveTask`, `SendTask`, `BusinessRuleTask`, `SubProcess`, `CallActivity`), all of
which already carry the same fields.

- [ ] **Step 1: Confirm green baseline.** `go test ./engine/... 2>&1 | tail -1` → `ok`.

- [ ] **Step 2: Inspect the activity types.** Read `definition/activity` and confirm all 7 types
  expose the four values. Decide the interface shape, e.g.:
  ```go
  // resilientActivity is any activity node carrying optional resiliency wiring.
  type resilientActivity interface {
      CompensateAction() string
      CancelAction() string
      RecoveryFlow() string
      CompletionAction() string
  }
  ```
  **Check for an import cycle:** `definition/activity` must not import `engine`. If the types
  already have exported getter methods, define the interface in `engine` (consumer side) — no
  change to `definition/activity` needed. If the values are plain fields with no getters, prefer
  adding getter methods in `definition/activity` (they are pure value getters, no cycle) OR keep
  the interface field-free by having `node_accessors.go` assert to each getter. Choose the option
  that adds the least surface; document the choice in the commit body.

- [ ] **Step 3: Rewrite the four accessors** to a single assertion form:
  ```go
  func compensateActionOf(n model.Node) string {
      if r, ok := n.(resilientActivity); ok { return r.CompensateAction() }
      return ""
  }
  ```
  (and the analogous three). Remove the 7-case switches.

- [ ] **Step 4: Run to verify green.**
  Run: `go test ./engine/... ./definition/... 2>&1 | tail -3` → all `ok`.
  `golangci-lint run ./engine/... ./definition/... 2>&1 | tail -1` → `0 issues.`

- [ ] **Step 5: Commit.**
  ```bash
  git add engine/node_accessors.go definition/activity/
  git commit -m "refactor(engine): retire 4 parallel activity type switches via one interface

compensate/cancel/recovery/completion accessors collapsed to a single
type assertion against a resilientActivity interface the 7 activity
types already satisfy.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
  ```

---

### Task A1: Extract `cancelTokenWaits`

**Files:**
- Create the helper in `engine/step_state.go` (or `engine/step_cancel.go` — a new small file is
  acceptable; choose per where the token-mutation helpers already live).
- Modify: `engine/step_compensation.go` (~151-177), `engine/step_errors.go` (~308-336),
  `engine/step_eventsubprocess.go` (~197-234), `engine/step_boundaries.go` (~145-163)

**Interfaces:**
- Produces:
  ```go
  // cancelTokenWaits cancels every wait attached to tok — deadline/reminder timers, the
  // token-keyed in-wait reminder, boundary arms on the token's node, and (for an event-based
  // gateway token, AwaitCommand prefixed "evtgw:") its armed events — and consumes the token.
  // Returns the CancelTimer commands produced by the sweep.
  func cancelTokenWaits(s *InstanceState, tok *Token, at time.Time) []Command
  ```

**Background:** the four call sites are near-verbatim, each with the same
`strings.HasPrefix(tok.AwaitCommand, "evtgw:")` special-case. The variant in `fireBoundaryArm`
(single host token) is a subset — fold it in if behavior is provably identical; otherwise leave
`fireBoundaryArm` alone and note why in the commit.

- [ ] **Step 1: Confirm green baseline.** `go test ./engine/... 2>&1 | tail -1` → `ok`.

- [ ] **Step 2: Diff the four sites** side by side. Confirm they are identical modulo the
  event-sub site also cancelling ESP arms. If the ESP site does strictly more, the shared helper
  covers the common sweep and the ESP site keeps its extra ESP-arm removal after calling it.

- [ ] **Step 3: Write `cancelTokenWaits`** capturing the exact common sweep (copy the current
  logic verbatim; do not "improve" it). Replace all four call sites with a call to it (the ESP
  site appends its extra removal; the boundary site adapts if folded).

- [ ] **Step 4: Run to verify green (behavior identical).**
  Run: `go test ./engine/... -count=1 2>&1 | tail -1` → `ok`.
  Run the compensation/error/boundary/eventsub suites explicitly:
  `go test -run 'Compensat|Error|Boundary|EventSub|Cancel' ./engine/... -count=1 2>&1 | tail -1`.
  `golangci-lint run ./engine/... 2>&1 | tail -1` → `0 issues.`

- [ ] **Step 5: Commit.**
  ```bash
  git add engine/
  git commit -m "refactor(engine): extract cancelTokenWaits (dedup 4x token-cancel sweep)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
  ```

---

### Task A2: Extract `emitFireOnceAction`

**Files:**
- Create helper alongside `cancelTokenWaits` (same file).
- Modify: `engine/step_errors.go` (~167-174, ~293-300), `engine/step_boundaries.go` (~130-137),
  and any deadline path emitting the same block.

**Interfaces:**
- Produces:
  ```go
  // emitFireOnceAction returns the fire-and-forget InvokeAction for a boundary/handler action
  // named actionName, with a defensive copy of the instance variables as input. Empty name -> nil.
  func emitFireOnceAction(s *InstanceState, actionName string) []Command
  ```

- [ ] **Step 1: Confirm green baseline.** `go test ./engine/... 2>&1 | tail -1` → `ok`.
- [ ] **Step 2: Locate every copy** of the `InvokeAction{FireAndForget: true, Input:
  copyVars(s.Variables)}` block: `grep -n 'FireAndForget' engine/*.go` (exclude `_test.go`).
- [ ] **Step 3: Extract and replace** all copies with `emitFireOnceAction`. Preserve the exact
  empty-name behavior at each site (some sites guard on non-empty name before emitting).
- [ ] **Step 4: Verify green.** `go test ./engine/... -count=1 2>&1 | tail -1` → `ok`;
  `golangci-lint run ./engine/... 2>&1 | tail -1` → `0 issues.`
- [ ] **Step 5: Commit.**
  ```bash
  git add engine/
  git commit -m "refactor(engine): extract emitFireOnceAction (dedup 4x fire-once action emit)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
  ```

---

### Task A3: Extract `resumeAndDrive`

**Files:**
- Create helper alongside the others.
- Modify: `engine/step_triggers.go` — `handleActionCompleted` (~107-117),
  `handleSubInstanceCompleted` (~698-712), `handleMessageReceived` step-4 (~805-838),
  `handleSignalReceived` step-4 (~664-681), `handleTimerFired` step-5 (~461-479).

**Interfaces:**
- Produces:
  ```go
  // resumeAndDrive resumes a parked token: clears its AwaitCommand, marks it TokenActive,
  // cancels its token-scoped timers (returning the CancelTimer commands), resolves the scope
  // definition, moves it along its single outgoing flow, and drives it. Returns the accumulated
  // commands and any drive error.
  func resumeAndDrive(def *definition.Definition, s *InstanceState, tok *Token, at time.Time, opt StepOptions) ([]Command, error)
  ```
  (Match the exact parameter types used by the current handlers — confirm whether they thread
  `mode`/`eval` separately or via `opt`; mirror it precisely.)

- [ ] **Step 1: Confirm green baseline.** `go test ./engine/... 2>&1 | tail -1` → `ok`.
- [ ] **Step 2: Read the five sites** and confirm the ritual is identical modulo the token and
  the source trigger. Note any site that does extra work before/after the ritual — that stays at
  the call site; only the common ritual is extracted.
- [ ] **Step 3: Extract `resumeAndDrive`** and replace the common ritual at all five sites.
- [ ] **Step 4: Verify green.**
  `go test ./engine/... -count=1 2>&1 | tail -1` → `ok`;
  `go test -run 'ActionCompleted|SubInstance|Message|Signal|Timer' ./engine/... -count=1 2>&1 | tail -1`;
  `golangci-lint run ./engine/... 2>&1 | tail -1` → `0 issues.`
- [ ] **Step 5: Commit.**
  ```bash
  git add engine/step_triggers.go engine/
  git commit -m "refactor(engine): extract resumeAndDrive (dedup 5x resume-parked-token ritual)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
  ```

---

### Task A4: Extract the 3-way arm-dispatch preamble

**Files:**
- Create helper alongside the others.
- Modify: `engine/step_triggers.go` — `handleTimerFired` (~407-432), `handleSignalReceived`
  (~611-648), `handleMessageReceived` (~766-796).

**Interfaces:**
- Produces: a helper that runs the shared gateway-arm → boundary-arm → event-sub-arm cascade
  given the three accessor lookups. Design the seam to fit all three handlers; signal's
  broadcast-to-all vs first-match tail must remain specialized (do NOT force signal into a
  first-match shape). Suggested form:
  ```go
  // dispatchArm resolves which armed construct (gateway / boundary / event-subprocess) a
  // trigger targets, using the supplied lookups, and returns the fire result. The signal
  // handler passes broadcast=true to fire all matches instead of the first.
  ```
  If a single signature cannot serve all three without contortion, extract only the gateway+
  boundary+event-sub *lookup ordering* as a helper returning a small found-arm sum, and let each
  handler fire per its own semantics. Prefer clarity over forcing one signature.

- [ ] **Step 1: Confirm green baseline.** `go test ./engine/... 2>&1 | tail -1` → `ok`.
- [ ] **Step 2: Read the three preambles** and identify the exact common cascade vs the
  per-trigger specialization (signal broadcast, message key correlation).
- [ ] **Step 3: Extract the common cascade.** Keep specialized tails at the call sites.
- [ ] **Step 4: Verify green.**
  `go test ./engine/... -count=1 2>&1 | tail -1` → `ok`;
  `go test -run 'Timer|Signal|Message|Boundary|EventBased|Gateway' ./engine/... -count=1 2>&1 | tail -1`;
  `golangci-lint run ./engine/... 2>&1 | tail -1` → `0 issues.`
- [ ] **Step 5: Commit.**
  ```bash
  git add engine/step_triggers.go engine/
  git commit -m "refactor(engine): extract shared arm-dispatch preamble for timer/signal/message

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
  ```

---

### Task A9: Signpost dispatch — split the registry into step_dispatch.go

**Files:**
- Create: `engine/step_dispatch.go`
- Modify: `engine/step_nodes.go`

**Interfaces:**
- Produces: no API change. Moves the `nodeStrategy` interface, the `nodeStrategies` map
  construction, and the `drive`/registry-lookup wiring into `step_dispatch.go`. The per-kind
  strategy structs + their `enter` methods stay in `step_nodes.go` (or move too if it reads
  better — but keep the move mechanical and behavior-identical).

- [ ] **Step 1: Confirm green baseline.** `go test ./engine/... 2>&1 | tail -1` → `ok`.
- [ ] **Step 2: Identify the registry surface** in `step_nodes.go`: the `nodeStrategy`
  interface + its doc comment, `var nodeStrategies = map[...]...{...}`, and any registry helper.
- [ ] **Step 3: Move** exactly those declarations to `engine/step_dispatch.go` (same package, no
  signature change). Leave strategies in `step_nodes.go`. `gofmt` both files.
- [ ] **Step 4: Verify green.** `go build ./engine/... && go test ./engine/... -count=1 2>&1 | tail -1`
  → `ok`; `golangci-lint run ./engine/... 2>&1 | tail -1` → `0 issues.`
- [ ] **Step 5: Commit.**
  ```bash
  git add engine/step_dispatch.go engine/step_nodes.go
  git commit -m "refactor(engine): move nodeStrategies registry to step_dispatch.go for findability

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
  ```

---

### Task A8: Split state.go by subsystem

**Files:**
- Create: `engine/state_timers.go`, `engine/state_arms.go`, `engine/state_compensation.go`,
  `engine/state_waiters.go`
- Modify: `engine/state.go`

**Interfaces:**
- Produces: no API change. Pure relocation of methods/types within the same package.

**Move map** (all methods on `*InstanceState`; verify names against current source):
- `state_timers.go`: `timerRecord` type, `timerByID`, `removeTimer`, `cancelTimersByTaskToken`,
  `cancelTimersForToken`, `cancelAllTimers`.
- `state_arms.go`: `armedEvent`, `boundaryArm`, `eventTriggeredSubprocessArm` types and all their
  `*ByTimer/BySignal/ByMessage` lookups + `removeArmedEventsForGateway`,
  `removeBoundaryArmsForHost`, `removeEventTriggeredSubprocessArmsForScope`,
  `removeAllEventTriggeredSubprocessArms`, `cancelAllArmsAndBoundaries`.
- `state_compensation.go`: `CompensationRecord`, `Scope`, `compensationCursor` types,
  `recordCompensation`, `openScope`, `tokensInScope`, `archiveCompensations`,
  `consolidateArchiveIntoRoot`, `closeScope`, `scopeByID`.
- `state_waiters.go`: the 6 waiter-enumeration methods (`MessageBoundaryWaiters`,
  `MessageArmedEventWaiters`, `MessageEventSubprocessWaiters`, `SignalEventSubprocessNames`,
  `MessageWaiters`, `SignalWaiters`) + `MessageWaiter` type.
- `state.go` keeps: `Status`, `TokenState`, `Token`, `Incident`, `NodeVisit`, `InstanceState`
  struct + `Clone`, `TaskByToken`, `cancelOpenTasks`.

- [ ] **Step 1: Confirm green baseline.** `go test ./engine/... 2>&1 | tail -1` → `ok`.
- [ ] **Step 2: Move declarations** per the map above — cut/paste only, no logic edits. While
  moving, strip multi-paragraph ADR-history prose from struct/method bodies down to a one-line
  godoc + the `(ADR-NNNN)` tag (this is the only allowed content change; it must not alter code).
- [ ] **Step 3: Verify build + green.** `go build ./engine/... && go test ./engine/... -count=1 2>&1 | tail -1`
  → `ok`; `go vet ./engine/...`; `golangci-lint run ./engine/... 2>&1 | tail -1` → `0 issues.`
- [ ] **Step 4: Confirm exported surface unchanged.**
  Run: `go doc ./engine > /tmp/after.txt` and diff against a pre-task capture; expect no diff.
- [ ] **Step 5: Commit.**
  ```bash
  git add engine/state.go engine/state_timers.go engine/state_arms.go engine/state_compensation.go engine/state_waiters.go
  git commit -m "refactor(engine): split state.go by subsystem (timers/arms/compensation/waiters)

Pure relocation; ADR-history prose trimmed to one-line godoc + (ADR-NNNN) tags.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
  ```

---

### Task A10: Fix test-file naming drift

**Files:**
- Rename (test files only): `engine/step_gateway_test.go` → `engine/step_gateways_test.go`,
  `engine/step_timer_test.go` → `engine/step_timers_test.go`. Audit the full list for other
  singular/plural or stem mismatches vs their impl file and rename to match the impl stem.

**Interfaces:**
- Produces: nothing. Test-file renames only; no code change.

- [ ] **Step 1: List mismatches.** For each `engine/*.go` impl file, check a same-stem
  `_test.go` exists; list the drifted names.
  Run: `ls engine/*.go | grep -v _test.go` and compare stems against `ls engine/*_test.go`.
- [ ] **Step 2: `git mv`** each drifted test file to its impl stem + `_test.go`. Do NOT rename
  ADR/task-probe test files that intentionally have no impl-file pair
  (e.g. `step_compensation_dedup_probe_test.go`) — those are allowed.
- [ ] **Step 3: Verify green.** `go test ./engine/... -count=1 2>&1 | tail -1` → `ok`.
- [ ] **Step 4: Commit.**
  ```bash
  git add -A engine/
  git commit -m "refactor(engine): align test-file names with impl stems (pairing convention)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
  ```

---

## Phase A Self-Review (control gate before Phase C)

After A0-A10 land on the branch, the controller runs the whole-branch verification:

- [ ] `go test ./... 2>&1 | tail -5` from repo root — all packages `ok` (no regression anywhere).
- [ ] `go test -race ./engine/... -count=1` — green (no data race introduced by extraction).
- [ ] `go test -coverprofile=cover.out ./engine/... && go tool cover -func=cover.out | tail -1`
  — ≥85% (baseline 87.6%; a small drop from removed dead guards is acceptable if still ≥85%).
- [ ] `golangci-lint run ./...` — clean.
- [ ] `gofmt -l engine/` — empty.
- [ ] `go doc ./engine` exported surface identical to baseline.
- [ ] `/code-review` (whole Phase A diff vs `main`) — all findings adjudicated and resolved.
- [ ] `/security-review` — clean.
- [ ] `--no-ff` merge to `main` + push.
```

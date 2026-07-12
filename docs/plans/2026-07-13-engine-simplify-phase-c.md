# Engine Simplification — Phase C (Decompose Monsters + Explicit Invariants + Folded Gaps) Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to
> implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.
> Spec: `docs/specs/2026-07-13-engine-simplification-program.md` (Phase C section).

**Goal:** Make the engine's highest-cognitive-load logic legible — decompose the two monster
functions (`endEventStrategy.enter` 256L, `propagateError` 310L) and the compensation trio,
make implicit invariants explicit — and close two verified completeness gaps
(`SubInstanceFailed` → parent error boundary; `closeScope` child-scope cascade), all without
regression.

**Architecture:** Behavior-preserving decomposition (existing tests are the oracle) for C1-C6;
red-first TDD for the behavioral tasks C7 (slog), C8 and C9 (the folded gaps). **Wire-format is
NOT touched in Phase C** — the persistence snapshot (`InstanceState` and its serialized
sub-structs including `Compensating`) keeps its exact shape; C4 therefore introduces the
walk-mode as a *computed method*, not a stored field. Arm-model unification and any wire change
are Phase B.

**Tech Stack:** Go 1.25, standard library `slog`, `expr-lang/expr`. No new deps.

## Global Constraints

- Go 1.25; module `github.com/kartaladev/wrkflw`.
- **No regression is a hard requirement.** After every task: `go test ./engine/...` green,
  `go test ./...` green from repo root, `golangci-lint run ./engine/...` clean, `engine`
  coverage ≥85% (baseline this branch: 89.7% — must not drop below 85, aim to hold/raise).
- **Engine core stays pure** — the hardened `purity_test.go` enforces it (no transport,
  persistence, watermill/gocron/clockwork/casbin, otel, or wall-clock reads). `slog` (stdlib)
  is allowed. Do NOT read `time.Now`; time enters via `Trigger.OccurredAt()`.
- **Wire-format frozen in Phase C.** Do NOT add, remove, or rename any serialized field on
  `InstanceState` or any struct it serializes (`Token`, `Scope`, `compensationCursor`,
  `CompensationRecord`, the arm structs, timer records, `Incident`, `NodeVisit`). If a task
  seems to need a new serialized field, STOP and escalate — it belongs in Phase B.
- **C1-C6 are behavior-preserving:** add no new test, edit no existing test except mechanical
  helper-name references; the suite must be green before AND after. **C7/C8/C9 are red-first:**
  a failing test precedes implementation; the red state is observable in the transcript.
- Follow Go skills: every implementer starts from `cc-skills-golang:golang-how-to` and loads
  task-relevant skills; the project's `table-test`, `use-mockgen`, `use-testcontainers` skills
  override the `golang-testing` equivalents. Prefer black-box `engine_test` for new tests.
- Error sentinels use the `workflow-engine:` prefix (project convention). New sentinels (if any)
  follow it and are tested for `errors.Is`.
- One commit per task, Conventional Commits scoped `refactor(engine):` / `feat(engine):` /
  `test(engine):` / `docs(adr):`, ending with the `Co-Authored-By: Claude Opus 4.8 (1M context)
  <noreply@anthropic.com>` trailer.
- Public API (`go doc ./engine`) must stay identical for C1-C7 (internal changes). C8 changes
  observable *behavior* (routing) but adds no exported symbol unless an ADR-approved sentinel is
  needed; C9 likewise. Confirm `go doc` deltas are intentional.

---

### Task C1: Decompose `endEventStrategy.enter` (256 lines)

**Files:** Modify `engine/step_nodes.go`.

**Interfaces:**
- Produces (all unexported, package-internal): `exitRootScope`, `exitRegularSubprocessScope`,
  `exitEventSubprocessScope` (handling root-level and nested), and a shared
  `resumeInParentScope(...)` helper. Exact signatures determined by reading the current
  function; each helper takes the `stepCtx`/token/node context the corresponding branch needs
  and returns the same `([]Command, bool /*halt*/, error)` contribution the inline branch
  produced.

**Oracle:** the existing end-event, terminate, error-end, sub-process, and event-subprocess
(ESP) tests: `step_errorend_drive_test.go`, `end_force_termination_test.go`,
`step_subprocess_test.go`, `step_eventsubprocess_test.go`, `state_esp_test.go`,
`step_subprocess_eventstart_test.go`, `step_subprocess_multistart_test.go`,
`step_eventsubprocess_multistart_test.go`, plus scenario tests.

- [ ] **Step 1: Confirm green baseline.** `go test ./engine/... -count=1 2>&1 | tail -1` → `ok`.
- [ ] **Step 2: Map the branches.** Read `endEventStrategy.enter` (~step_nodes.go, the
  `KindEndEvent` strategy). Identify its distinct exit paths: (a) normal/terminate/error end
  behavior via `EndBehavior`; (b) root scope vs sub-process scope; (c) ESP (root-level vs nested,
  interrupting vs non-interrupting) vs regular sub-process; each with scope-drain + resume. Note
  every `stopped`/`tok.State` outcome and every early-return with its `halt` value.
- [ ] **Step 3: Extract per-exit helpers** preserving each path's exact command output, token
  state mutation, `halt` value, and error. The ESP resume and regular-sub-process resume share a
  parent-scope resume — factor `resumeInParentScope(enclosingNodeID, parentScopeID)` used by
  both. Do NOT change control flow outcomes — this is a pure restructuring; the top-level
  `enter` becomes a thin dispatcher over the helpers.
- [ ] **Step 4: Verify green (byte-identical behavior).**
  `go test ./engine/... -count=1 2>&1 | tail -1` → `ok`;
  `go test -run 'End|Terminate|ErrorEnd|SubProcess|EventSub|ESP|Scope' ./engine/... -count=1`;
  `go test -race ./engine/... -count=1`. `golangci-lint run ./engine/...` → `0 issues.`
- [ ] **Step 5: Commit** `refactor(engine): decompose endEventStrategy.enter into per-scope-exit helpers`.

---

### Task C2: Decompose `propagateError` (310 lines)

**Files:** Modify `engine/step_errors.go`.

**Interfaces:**
- Produces: `findDirectBoundary(...) (matched *boundaryArm/def, targetScope, ok)` — the Step-1
  own-scope boundary lookup; `findEnclosingBoundary(...)` — the Step-2 scope→root walk; and a
  shared `routeToBoundary(...)` tail (fire-once action via `emitFireOnceAction` + outgoing-flow
  resolve + `placeToken` + `drive`). The no-handler fallback (incident vs compensation vs
  FailInstance) stays in `propagateError` or becomes `handleUnhandledError(...)`. Exact
  signatures from reading the current code. **Reuse the Phase-A `cancelTokenWaits` and
  `emitFireOnceAction` helpers** — do not reintroduce their inline copies.

**Oracle:** `step_errors_test.go`, `errors_test.go`, `boundary_error_matching_test.go`,
`step_boundaries_test.go`, `step_boundaries_action_test.go`,
`step_compensation_error_cancel_test.go`, `failing_action_test.go`, `retry_test.go`,
`step_fail_tasks_test.go`, scenario tests.

- [ ] **Step 1: Confirm green baseline.** `go test ./engine/... -count=1 2>&1 | tail -1` → `ok`.
- [ ] **Step 2: Map the two phases + fallback.** Read `propagateError` (step_errors.go:109-419).
  Phase 1 = direct-attachment boundary on the failing node (consume failing token by ID, route
  recovery token in same scope, drive). Phase 2 = enclosing-scope walk (cancel all scope tokens,
  close scope, route in parent, drive). Fallback = incident (retry-exhaustion, park
  `TokenIncident`) | compensation (`beginCompensation`) | immediate `StatusFailed` + sweep. Note
  the duplicated "is-error-boundary" marker filter (`!Timer.IsZero() || SignalName!="" ||
  MessageName!=""`) at both phases — factor it into one predicate.
- [ ] **Step 3: Extract `findDirectBoundary` / `findEnclosingBoundary` / `routeToBoundary`** and
  the shared marker predicate. Preserve: the consume-by-ID correctness (parallel/loop tokens),
  the partial-`cmds`-with-error return contract at each early return, and the exact fallback
  precedence. Pure restructuring.
- [ ] **Step 4: Verify green.**
  `go test ./engine/... -count=1 2>&1 | tail -1` → `ok`;
  `go test -run 'Error|Boundary|Retry|Fail|Compensat|Incident' ./engine/... -count=1`;
  `go test -race ./engine/... -count=1`. `golangci-lint run ./engine/...` → `0 issues.`
- [ ] **Step 5: Commit** `refactor(engine): decompose propagateError into find/route boundary helpers`.

---

### Task C3: Collapse compensation eligibility math

**Files:** Modify `engine/step_compensation.go`.

**Interfaces:**
- Produces: `eligibleRange(records []CompensationRecord, toNode string) (start, stopExclusive int)`
  (or the shape the current code needs) consumed by BOTH `beginCompensation` and
  `stepCompensationAdvance`, retiring the duplicated `toNodeIdx` scan and the two encodings of
  the stop rule (`toNodeIdx >= len-1`, `nextIdx <= toNodeIdx`). Also replace full-struct
  `compensationCursor{...}` re-lists with copy-and-mutate.

**Oracle:** `step_compensation_test.go`, `compensation_throw_test.go`,
`step_compensation_throw_test.go`, `step_compensation_parallel_throw_test.go`,
`step_compensation_dedup_probe_test.go`, `step_compensation_outcome_test.go`,
`step_compensation_finish_endedat_test.go`, `reverse_instance_test.go`.

- [ ] **Step 1: Confirm green baseline.** `go test -run 'Compensat|Reverse' ./engine/... -count=1 2>&1 | tail -1` → `ok`.
- [ ] **Step 2: Extract the range once.** Read `beginCompensation` (startIndex/toNode-elision
  logic, ~191-237) and `stepCompensationAdvance` (~279-333, the re-derived predicate ~302).
  Define one `eligibleRange` capturing the exact rule both encode. Replace both derivations with
  calls to it. Replace cursor re-lists with `cur := s.Compensating; cur.X = ...; s.Compensating = cur`.
- [ ] **Step 3: Verify green.** `go test ./engine/... -count=1 2>&1 | tail -1` → `ok`;
  `go test -race ./engine/... -count=1`; `golangci-lint run ./engine/...` → `0 issues.`
- [ ] **Step 4: Commit** `refactor(engine): unify compensation eligibility range (begin+advance)`.

---

### Task C4: Make `compensationCursor` walk-mode explicit (computed method, wire-safe)

**Files:** Modify `engine/state_compensation.go` and `engine/step_compensation.go`.

**Interfaces:**
- Produces: an unexported `walkMode` enum type + a `(*compensationCursor) walkMode()` **method**
  (or a package func `walkModeOf(cur)`), centralizing the current inference from which fields are
  non-zero (`ArchiveKey`/`ScopeID`/`ResumeNode`/`ReverseNode`/`RestoreTargetVars`/
  `StartRecordCount`). **Do NOT add a serialized field to `compensationCursor`** — the struct is
  part of the persistence snapshot; introducing storage is a Phase-B wire change. The mode is
  DERIVED, not stored.

- [ ] **Step 1: Confirm wire constraint.** Verify `compensationCursor` (as `s.Compensating`) is
  serialized in the persistence snapshot (grep the persistence/JSON round-trip). Confirm the plan
  to compute (not store) the mode. If it turns out NOT serialized and a stored field is strictly
  cleaner, a stored field is acceptable — but default to computed; note the finding.
- [ ] **Step 2: Confirm green baseline.** `go test -run 'Compensat|Reverse' ./engine/... -count=1 2>&1 | tail -1` → `ok`.
- [ ] **Step 3: Centralize the inference.** Enumerate the walk modes (e.g. `walkAdmin`,
  `walkThrowTargeted`, `walkThrowScopeWide`, `walkReverse`) and implement `walkMode()` returning
  the mode from the SAME conditions the code currently scatters. Replace each scattered
  "which-field-is-zero" branch with a `switch cur.walkMode()`. Behavior must be identical: the
  method returns exactly what the inline inference concluded at each site.
- [ ] **Step 4: Verify green.** `go test ./engine/... -count=1 2>&1 | tail -1` → `ok`;
  `go test -race ./engine/... -count=1`; `golangci-lint run ./engine/...` → `0 issues.`
  Also run the persistence round-trip: `go test ./persistence/... ./internal/persistence/... -count=1 2>&1 | tail -3` → `ok` (proves no wire change).
- [ ] **Step 5: Commit** `refactor(engine): derive compensation walk-mode via one method (wire-safe)`.

---

### Task C5: Replace positional bool/string arg-tails with named options

**Files:** Modify `engine/step_compensation.go`, `engine/step_errors.go`, `engine/step_triggers.go` (call sites).

**Interfaces:**
- Produces: an options struct (e.g. `compensationStart` with named fields) replacing
  `beginCompensation`'s ~11 positional args (the `"", false, false` tail at
  step_triggers.go:191 is the target), and a named type/bool-struct replacing `propagateError`'s
  trailing `raiseIncidentOnUnhandled bool`. Update every call site. Unexported — no public API
  change.

- [ ] **Step 1: Confirm green baseline.** `go test ./engine/... -count=1 2>&1 | tail -1` → `ok`.
- [ ] **Step 2: Introduce the options types** and rewrite `beginCompensation` /`propagateError`
  signatures + ALL call sites (grep them: `grep -n 'beginCompensation\|propagateError' engine/*.go`).
  Field-by-field the new struct must carry the identical values each positional arg carried — a
  pure signature refactor, zero behavior change.
- [ ] **Step 3: Verify green.** `go test ./engine/... -count=1 2>&1 | tail -1` → `ok`;
  `go test -race ./engine/... -count=1`; `golangci-lint run ./engine/...` → `0 issues.`
- [ ] **Step 4: Commit** `refactor(engine): replace positional arg-tails with named options (beginCompensation, propagateError)`.

---

### Task C6: Enforce `finishPlan` invariants in code

**Files:** Modify `engine/step_compensation.go`.

**Interfaces:**
- Produces: a constructor or checked assertion enforcing `finishPlan`'s documented
  mutual-exclusion invariants (e.g. `resetVars` xor `toNode`; terminate plans never
  `scopeWideThrow`). `finishPlan` is a transient local struct (NOT serialized) — safe to add a
  constructor. On invariant violation, panic with a clear message (an internal-invariant
  violation is a programming bug, not a runtime error path) OR return an error routed to an
  incident — choose per how the surrounding code treats internal invariants; default to a panic
  with `workflow-engine:`-prefixed message since a violated construction invariant is a bug.

- [ ] **Step 1: Confirm green baseline.** `go test -run 'Compensat|Reverse' ./engine/... -count=1 2>&1 | tail -1` → `ok`.
- [ ] **Step 2: Enumerate the invariants** from `finishPlan`'s current doc comments. Add a
  `newFinishPlan(...)`/validation that asserts them. Route all `finishPlan{...}` construction
  through it. Because the invariants hold today, the existing suite stays green — the assertion
  only fires on a future violation.
- [ ] **Step 3: Verify green.** `go test ./engine/... -count=1 2>&1 | tail -1` → `ok`;
  `go test -race ./engine/... -count=1`; `golangci-lint run ./engine/...` → `0 issues.`
- [ ] **Step 4: Commit** `refactor(engine): enforce finishPlan mutual-exclusion invariants at construction`.

---

### Task C7: Observability on silent no-op paths (red-first)

**Files:** Modify the files holding the no-op sites; add tests in a `_test.go` (black-box where
feasible, or white-box if the log call is unexported-path).

**Interfaces:**
- Produces: `slog` calls (stdlib `log/slog`, no vendor) at the deliberate silent no-op / swallowed
  paths, using the engine's existing logger seam if one exists — **first check how the engine
  currently obtains a `*slog.Logger`** (it may thread one via `StepOptions` or use
  `slog.Default()`; match the existing pattern; if none exists, use `slog.Default()` at
  Debug/Warn level and note it). Sites (from the audit; verify each still exists):
  1. late-timer / stale-token no-op (`drive` missing-node park; terminal-instance timer no-op),
  2. swallowed `ErrorExpr` eval error in boundary matching (`step_errors.go` ~147-150, ~271-274),
  3. missing-node park in `drive` (`step.go` ~128-131),
  4. cancel-path swallowed `defForScope` error (`handleCancelRequested`).

**Red-first is mandatory.** Use an `slog` test handler (`slog.NewTextHandler`/a capturing
handler) injected via the existing seam (or `slog.SetDefault` with a capturing handler in the
test) to assert a record is emitted with the expected level + key attributes.

- [ ] **Step 1: Discover the logger seam.** Grep the engine + runtime for how a `*slog.Logger`
  reaches engine code (`grep -rn 'slog' engine/ runtime/ | head`). Decide the injection point.
  If the engine has NO logger seam and adding one would change a public signature, STOP and
  escalate — a logger seam may need an ADR/`StepOptions` field (public API); do not invent one
  silently.
- [ ] **Step 2: Write the failing test** for ONE site first (e.g. terminal-instance timer no-op):
  drive an instance to a terminal state, fire a late timer, assert a Warn/Debug record with the
  instance id + reason is captured. Run: `go test -run 'TestSilentNoOpLogs' ./engine/... -v` →
  FAIL (no log emitted yet).
- [ ] **Step 3: Add the `slog` call** at that site; make the test pass. Repeat Steps 2-3 per site
  (one red→green cycle each; do not batch).
- [ ] **Step 4: Verify green + no-regression.** `go test ./engine/... -count=1 2>&1 | tail -1` →
  `ok`; `go test -race ./engine/...`; `golangci-lint run ./engine/...` → `0 issues.`; confirm the
  purity test still passes (slog is allowed; no wall-clock/vendor added).
- [ ] **Step 5: Commit** `feat(engine): observability (slog) on silent no-op paths`.

---

### Task C8: `SubInstanceFailed` routes to a parent error boundary (red-first, folded gap)

**Files:** Modify `engine/step_triggers.go` (`handleSubInstanceFailed`, ~717-745); reuse C2's
`findDirectBoundary`/`routeToBoundary`. Add ADR `docs/adr/NNNN-*.md` (next free number — check
`ls docs/adr | tail`). Tests in a `_test.go`.

**Interfaces:**
- Consumes: C2's `findDirectBoundary`/`routeToBoundary` (the call-activity node is the host; the
  child's error code is the error). Produces: no new exported symbol expected (unless a sentinel
  is needed — then `workflow-engine:`-prefixed + ADR-noted).

**Design (controller-decided):** A `SubInstanceFailed` is semantically an error thrown at the
call-activity node. When the parent's call-activity node carries a boundary error event matching
the child's error code, route to it (interrupting: cancel the call-activity token, fire the
boundary flow, drive) exactly as `propagateError` Phase-1 does for a direct boundary. When no
matching boundary exists, fall back to the CURRENT behavior (`FailInstance`). This makes a child
failure catchable by a parent error boundary.

- [ ] **Step 1: Write the ADR** (Nygard template: Status/Context/Decision/Consequences)
  recording the decision that `SubInstanceFailed` participates in boundary-error propagation on
  the call-activity node, with `FailInstance` as the no-boundary fallback. Commit is folded into
  this task's final commit or a preceding `docs(adr)` commit.
- [ ] **Step 2: Confirm green baseline.** `go test ./engine/... -count=1 2>&1 | tail -1` → `ok`.
- [ ] **Step 3: Write the failing regression test.** Build a definition: parent with a
  `CallActivity` node carrying an attached boundary **error** event whose ErrorCode matches; a
  child that fails with that code. Deliver `SubInstanceFailed` and assert the parent routes to
  the boundary flow (parent NOT Failed; a token proceeds down the boundary's outgoing flow).
  Also add/keep a test that with NO matching boundary the parent still `FailInstance`s.
  Run: `go test -run 'TestSubInstanceFailed' ./engine/... -v` → the boundary test FAILS (current
  code always FailInstances).
- [ ] **Step 4: Implement the routing.** In `handleSubInstanceFailed`, locate the parent's
  call-activity token + node, run the child's error code through `findDirectBoundary`; on match,
  `routeToBoundary` (cancel the call-activity token, fire boundary action, place token on the
  boundary outgoing flow, drive); else `FailInstance` as before. Make both tests pass.
- [ ] **Step 5: Verify green + no-regression.** `go test ./engine/... -count=1 2>&1 | tail -1` →
  `ok`; `go test -run 'SubInstance|CallActivity|Boundary|Error' ./engine/... -count=1`;
  `go test -race ./engine/...`; full repo `go test ./...` (call-link/chain runtime tests exercise
  sub-instances — must stay green); `golangci-lint run ./engine/...` → `0 issues.`
- [ ] **Step 6: Commit** `feat(engine): route SubInstanceFailed to a parent error boundary when attached (ADR-NNNN)`.

---

### Task C9: `closeScope` cascades to child scopes (red-first, folded gap)

**Files:** Modify `engine/state_compensation.go` (`closeScope`, ~1139 pre-split → now in
`state_compensation.go`). Tests in a `_test.go`. ADR: fold into C8's ADR or add a short note
(these are companion completeness gaps) — prefer ONE ADR covering both folded gaps; reference it.

**Interfaces:**
- Produces: `closeScope(scopeID)` now removes the scope AND its descendant scopes (and their
  arms/timers as the existing per-scope cleanup does), instead of requiring callers to pre-close
  children.

**Design (controller-decided):** `closeScope` walks the scope tree and closes the target scope
plus all scopes whose parent chain reaches it, applying the same per-scope removal the current
code applies to one scope. This closes the audit gap (the "Plan 8 will add cascading" comment).

- [ ] **Step 1: Audit callers.** `grep -n 'closeScope' engine/*.go`. Confirm NO caller relies on
  the current manual-cascade (i.e. no caller closes children first and would now double-close).
  If a caller pre-closes children, adjust it to not double-work (idempotent removal is fine).
  Report findings.
- [ ] **Step 2: Confirm green baseline.** `go test -run 'Scope|Compensat|SubProcess|EventSub' ./engine/... -count=1 2>&1 | tail -1` → `ok`.
- [ ] **Step 3: Write the failing test.** Construct state with a parent scope and nested child
  scope(s); call `closeScope(parent)`; assert the child scopes are also removed from `s.Scopes`
  (and their arms/timers cleaned). Run → FAIL (current `closeScope` leaves children orphaned).
- [ ] **Step 4: Implement the cascade.** Make `closeScope` remove descendants. Make the test
  pass. Ensure idempotency (closing an already-closed child is a no-op).
- [ ] **Step 5: Verify green + no-regression.** `go test ./engine/... -count=1 2>&1 | tail -1` →
  `ok`; `go test -race ./engine/...`; full repo `go test ./...`; `golangci-lint run ./engine/...`
  → `0 issues.`
- [ ] **Step 6: Commit** `feat(engine): closeScope cascades to child scopes (ADR-NNNN)`.

---

## Phase C Self-Review (control gate before Phase B)

- [ ] `go test ./... 2>&1 | tail -5` from repo root — all packages `ok` (no regression anywhere,
  incl. testcontainers persistence + runtime call-link/chain).
- [ ] `go test -race ./engine/... -count=1` — green.
- [ ] `go test -coverprofile=cover.out ./engine/... && go tool cover -func=cover.out | tail -1`
  — ≥85%.
- [ ] `golangci-lint run ./...` — clean. `gofmt -l engine/` — empty.
- [ ] Persistence round-trip green — proving Phase C introduced NO wire-format change.
- [ ] `go doc ./engine` — deltas only where intentional (C8/C9 add no exported symbol unless
  ADR-approved).
- [ ] ADR(s) for C8 (+C9) committed under `docs/adr/` (Nygard template).
- [ ] `/code-review` (whole Phase C diff vs `main`) — all findings adjudicated & resolved.
- [ ] `/security-review` — clean.
- [ ] `--no-ff` merge to `main` + push.
```

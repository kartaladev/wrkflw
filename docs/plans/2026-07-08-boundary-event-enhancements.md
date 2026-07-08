# Boundary-Event Enhancements Implementation Plan (ADR-0103/0104)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a fire-once boundary action (`WithBoundaryAction`), flexible boundary error matching (`WithBoundaryErrorCheck` closure + `WithBoundaryErrorExpr` expr, precedence Check→Expr→Code), split `WithDeadline` into `WithDeadlineFlow` + `WithDeadlineAction`, and add the `boundary_action` example + godoc Examples. Behavior of existing boundaries and deadlines is preserved except the deliberate breaking option-surface changes.

**Architecture:** Boundaries fire via `engine/step_boundaries.go:fireBoundaryArm` (timer/signal/message dispatch) and are error-matched lazily in `engine/step_errors.go:propagateError` (error boundaries are never *armed*). A fire-once action reuses the existing `InvokeAction{FireAndForget:true}` mechanism (identical to deadline-breach actions). Error matching gains a Go-closure tier (live error threaded via a non-persisted `ActionFailed.Cause`) and an expr tier (over instance vars + a freshly-injected `_error` code string). The engine is snapshot-based — `Step` runs exactly once per trigger and the resulting `InstanceState` is committed — so the non-persisted live error cannot cause replay divergence.

**Tech Stack:** Go 1.25; `expr-lang/expr` via the existing `ConditionEvaluator.EvalBool`; `schedule.TriggerSpec` (post-ADR-0102); no new dependency.

## Global Constraints

- Go 1.25, module `github.com/zakyalvan/krtlwrkflw`.
- **Strict TDD** (CLAUDE.md TDD Operational Discipline — observable RED before every new symbol). Black-box tests (`_test` packages); `table-test` closure form for 2+ cases; `t.Context()`.
- Type-safe options (Item 0 convention): a subset-applicable option returns a narrow anonymous interface; `activityOnlyOption`/`BoundaryOption` for genuinely-broad ones. `WithDeadlineFlow`/`WithDeadlineAction` stay `activityOnlyOption` (all activity kinds — do NOT narrow; preserves `WithDeadline`'s scope). The 3 new boundary options are plain `BoundaryOption`.
- Use `expr-lang` for the error-matching predicate (`ErrorExpr`) — never hand-roll (CLAUDE.md).
- **Do NOT touch** the fused-timer-write path or scheduler internals. **Do NOT split `event.WithCatchDeadline`** (activity-only per spec).
- Non-serializable Go escape hatches (`ErrorCheck` closure, like inline actions) MUST be excluded from wire both directions; `Action`/`ErrorExpr` DO serialize.
- Error sentinels use `workflow-<pkg>:` prefix.
- **Corrections vs the spec (from the architecture assessment — authoritative):**
  1. The "message-boundary-never-armed bug" is **already fixed** (message boundaries arm/expose/deliver/fire; proven by `runtime/message_boundary_e2e_test.go`, `engine/step_boundaries_test.go`). **No bug-fix work item.**
  2. `WithDeadline` uses `schedule.TriggerSpec`, not a string — split is `WithDeadlineFlow(t schedule.TriggerSpec, flowID string)`.
  3. `ErrorCheck`/`ErrorExpr` live on `event.BoundaryEvent` (read by `propagateError`), **not** on `boundaryArm` (error boundaries are never armed). Only `Action` goes on `boundaryArm`.
  4. `_error` does NOT pre-exist (only `_errorMessage`/`_errorAttempts`, catch-flow only). `WithBoundaryErrorExpr` injects `_error` (= error code string) fresh into its eval env only.
  5. Determinism proven safe: `deliverLoop` runs `engine.Step` once/trigger and commits the resulting `InstanceState`; no replay re-invokes `propagateError`.
- ADRs: **ADR-0103** (boundary action + `WithDeadline` split) + **ADR-0104** (flexible boundary error matching). Nygard template, `docs/adr/0001-record-architecture-decisions.md` format.
- `WithReminder`→`WithWaitReminder` (spec Feature 7) is already DONE — SKIP.

## File Structure

- `definition/event/event.go` — add `Action`/`ErrorExpr`/`ErrorCheck` to `BoundaryEvent` (~L95); extend boundary `FromWire`/`ToWire` (~L242-252) for `Action`↔`BoundaryAction`, `ErrorExpr`↔`BoundaryErrorExpr` (NOT `ErrorCheck`).
- `definition/event/options.go` — `WithBoundaryAction`, `WithBoundaryErrorExpr`, `WithBoundaryErrorCheck` (~after L155).
- `definition/model/node_wire.go` — `BoundaryAction`, `BoundaryErrorExpr` fields (~L42).
- `engine/state.go` — `boundaryArm.Action` (~L107).
- `engine/step_boundaries.go` — capture `Action` in `armBoundaries` (~L37-43); prepend `InvokeAction{FireAndForget}` in `fireBoundaryArm` (~L124).
- `engine/trigger.go` — `ActionFailed.Cause error json:"-"` + `WithCause` option.
- `runtime/processdriver_action.go` — thread `engine.WithCause(err)` (~L136).
- `engine/step_errors.go` — `propagateError` gains `cause error`; shared `boundaryErrorMatches` helper at both match sites (~L86, L189).
- `engine/step_triggers.go` — `handleActionFailed` passes `t.Cause` (~L301, L315).
- `definition/activity/options.go` — split `WithDeadline` → `WithDeadlineFlow` + `WithDeadlineAction` (~L149); migrate 19 call sites.
- `examples/scenarios/boundary_action/main.go` — new; `examples/scenarios/usertask_deadline/main.go` — package-doc rewrite.
- `docs/adr/0103-*.md`, `docs/adr/0104-*.md`.

---

## Task 1: Definition layer — boundary fields, options, wire round-trip

**Files:** `definition/event/event.go`, `definition/event/options.go`, `definition/model/node_wire.go`; test `definition/event/boundary_options_test.go` (+ wire round-trip in the existing model wire test).

**Interfaces — Produces:** `event.BoundaryEvent.Action/ErrorExpr string`, `.ErrorCheck func(map[string]any, error) bool`; `WithBoundaryAction(name string) BoundaryOption`; `WithBoundaryErrorExpr(expr string) BoundaryOption`; `WithBoundaryErrorCheck(fn func(map[string]any, error) bool) BoundaryOption`; `NodeWire.BoundaryAction/BoundaryErrorExpr string`.

- [ ] **Step 1: Failing option tests.** In a new `definition/event/boundary_options_test.go` (package `event_test`), assert each new option sets its field on a `NewBoundary(...)` node (type-assert to `event.BoundaryEvent`); and a wire round-trip test asserting `boundaryAction`/`boundaryErrorExpr` survive `ToWire`→JSON→`FromWire` while `ErrorCheck` is nil after round-trip. Run → FAIL (`undefined: event.WithBoundaryAction`, etc.).

- [ ] **Step 2: Add the fields.** In `definition/event/event.go`, add to `BoundaryEvent` (after `Timer`, ~L95):
```go
	// Action is the optional catalog action name run fire-once (FireAndForget)
	// when this boundary fires, for any trigger type. Empty = no action.
	Action string
	// ErrorExpr is an optional expr-lang predicate (over instance vars + the
	// injected _error code string) deciding whether an ERROR boundary catches.
	// Serialized. See the Check→Expr→Code precedence in propagateError.
	ErrorExpr string
	// ErrorCheck is an optional Go predicate (vars, thrown error) deciding
	// whether an ERROR boundary catches. Highest precedence. Non-serializable
	// (Go-authoring-only escape hatch, like inline actions) — absent from wire.
	ErrorCheck func(map[string]any, error) bool
```

- [ ] **Step 3: Add the options.** In `definition/event/options.go` (after `WithBoundaryNonInterrupting`, ~L155), using the existing `boundaryFuncOpt` adapter:
```go
// WithBoundaryAction attaches a fire-once catalog action run when the boundary
// fires (any trigger type). Result discarded; failure logs + continues routing.
func WithBoundaryAction(name string) BoundaryOption {
	return boundaryFuncOpt(func(n *BoundaryEvent) { n.Action = name })
}

// WithBoundaryErrorExpr sets an expr-lang predicate deciding whether an error
// boundary catches, evaluated over the instance variables plus _error (the
// thrown error code string). Truthy = catch. Serializable. Precedence: applied
// after WithBoundaryErrorCheck, before WithBoundaryErrorCode.
func WithBoundaryErrorExpr(expr string) BoundaryOption {
	return boundaryFuncOpt(func(n *BoundaryEvent) { n.ErrorExpr = expr })
}

// WithBoundaryErrorCheck sets a Go predicate (instance vars, thrown error)
// deciding whether an error boundary catches. Highest precedence; non-serializable
// (Go-authoring only). For action-thrown failures err is the ORIGINAL error
// (use errors.Is/As); for bare-code sources err.Error() == the code.
func WithBoundaryErrorCheck(fn func(map[string]any, error) bool) BoundaryOption {
	return boundaryFuncOpt(func(n *BoundaryEvent) { n.ErrorCheck = fn })
}
```

- [ ] **Step 4: Wire fields + projection.** In `definition/model/node_wire.go` (~L42, near the boundary fields) add:
```go
	BoundaryAction    string `json:"boundaryAction,omitempty"`
	BoundaryErrorExpr string `json:"boundaryErrorExpr,omitempty"`
```
In `event.go` boundary `ToWire` (~L247-252) add `w.BoundaryAction = v.Action` and `w.BoundaryErrorExpr = v.ErrorExpr`; in `FromWire` (~L242-246) add `Action: w.BoundaryAction, ErrorExpr: w.BoundaryErrorExpr`. Do NOT touch `ErrorCheck` in either direction (it must not serialize).

- [ ] **Step 5: Run** — `go test ./definition/... -count=1` → PASS (options set fields; wire round-trips Action/ErrorExpr; ErrorCheck nil after round-trip).

- [ ] **Step 6: Commit** — `feat(event): boundary Action/ErrorExpr/ErrorCheck fields + options + wire`

---

## Task 2: Engine — fire-once boundary action

**Files:** `engine/state.go`, `engine/step_boundaries.go`; test `engine/step_boundaries_action_test.go`.

**Interfaces — Consumes:** `event.BoundaryEvent.Action` (Task 1). **Produces:** `boundaryArm.Action`; `fireBoundaryArm` emits `InvokeAction{FireAndForget:true}` before routing.

- [ ] **Step 1: Failing test.** New `engine/step_boundaries_action_test.go`: table over {interrupting, non-interrupting} × {timer, signal, message}. Build a definition whose boundary carries `WithBoundaryAction("notify")`, drive the host to parked, fire the boundary (timer/signal/message trigger), and assert the returned commands include an `InvokeAction{Name:"notify", FireAndForget:true}` that PRECEDES the routing/drive commands. Plus: a case asserting a failing action still routes (the InvokeAction is FireAndForget so routing is unaffected at the engine layer). Run → FAIL (`boundaryArm` has no `Action`).

- [ ] **Step 2: Add `boundaryArm.Action`.** In `engine/state.go` (~L107, end of `boundaryArm`) add `Action string` (value type — safe for the shallow `cloneState`).

- [ ] **Step 3: Capture it.** In `engine/step_boundaries.go` `armBoundaries` (~L37-43), add `Action: n.Action` to the `boundaryArm{...}` literal.

- [ ] **Step 4: Emit before routing.** In `fireBoundaryArm` (~L124, right after `var cmds []Command`, before the `if !ba.NonInterrupting` branch):
```go
	if ba.Action != "" {
		cmds = append(cmds, InvokeAction{
			CommandID:     s.nextCommandID(),
			Name:          ba.Action,
			Input:         copyVars(s.Variables),
			FireAndForget: true,
		})
	}
```
This mirrors the deadline-breach path (`engine/step_timers.go` `handleDeadlineFired` ~L76-81) exactly, so the two stay in lockstep. Since routing/`drive` commands are appended AFTER, the action naturally precedes them for both interrupting and non-interrupting branches and all three dispatchers.

- [ ] **Step 5: Run** — `go test ./engine/... -run Boundary -count=1` → PASS.

- [ ] **Step 6: Commit** — `feat(engine): fire-once boundary action (FireAndForget InvokeAction before routing)`

---

## Task 3: Runtime/trigger — non-persisted ActionFailed cause carrier

**Files:** `engine/trigger.go`, `runtime/processdriver_action.go`; test in `engine/trigger_test.go` (or a new `engine/action_failed_cause_test.go`).

**Interfaces — Produces:** `engine.ActionFailed.Cause error` (`json:"-"`); `engine.WithCause(err error) ActionFailedOption`. Consumed by Task 4.

- [ ] **Step 1: Failing test.** Assert `NewActionFailed(..., WithCause(myErr))` sets `.Cause == myErr`, and that JSON-marshalling an `ActionFailed` OMITS the cause (round-trips clean, `Cause` nil after). Run → FAIL (`undefined: engine.WithCause`).

- [ ] **Step 2: Add field + option.** In `engine/trigger.go`: add `Cause error \`json:"-"\`` to the `ActionFailed` struct (~L37-47); add `func WithCause(err error) ActionFailedOption { return func(a *ActionFailed) { a.Cause = err } }` alongside `WithJitter` (~L64). Ensure the option type matches the existing `ActionFailedOption` shape.

- [ ] **Step 3: Thread the live error.** In `runtime/processdriver_action.go` (~L136), add `engine.WithCause(err)` to the `NewActionFailed(...)` call (the `err` is the live action error from `safeActionDo`). Leave the unknown-action `NewActionFailed` (~L114) without a cause (bare-code source).

- [ ] **Step 4: Run** — `go test ./engine/... ./runtime/... -run 'ActionFailed|Cause' -count=1` → PASS; `go build ./...`.

- [ ] **Step 5: Commit** — `feat(engine): non-persisted ActionFailed.Cause carrier (WithCause) for live-error boundary matching`

---

## Task 4: Engine — three-tier boundary error matching (Check→Expr→Code)

**Files:** `engine/step_errors.go`, `engine/step_triggers.go`; test `engine/boundary_error_matching_test.go`.

**Interfaces — Consumes:** `BoundaryEvent.ErrorCheck/ErrorExpr` (Task 1), `ActionFailed.Cause` (Task 3), `ConditionEvaluator.EvalBool` (`engine/conditions.go:22`). **Produces:** `propagateError` gains a `cause error` parameter; a shared `boundaryErrorMatches` helper used at both match sites.

- [ ] **Step 1: Failing tests.** New `engine/boundary_error_matching_test.go`: (a) a `WithBoundaryErrorCheck` that catches vs. propagates, including an `errors.As`-style typed-error match using the live cause; (b) a `WithBoundaryErrorExpr` over `vars + _error` (truthy catches, falsy propagates); (c) precedence: a boundary with all three set uses Check; with Expr+Code uses Expr; with only Code uses Code; (d) a non-matching Check/Expr lets the error propagate to the next handler / raise an incident; (e) a bare-code source (nil cause) still evaluates a Check against a synthesized `errors.New(errorCode)`. Run → FAIL (helper/param absent).

- [ ] **Step 2: Add the shared helper.** In `engine/step_errors.go`, add:
```go
// boundaryErrorMatches decides whether error boundary n catches a thrown error.
// Precedence: ErrorCheck (Go closure, highest) → ErrorExpr (expr-lang over vars
// + _error code) → ErrorCode (exact/catch-all). cause is the live thrown error
// (a synthesized errors.New(errorCode) for bare-code sources).
func boundaryErrorMatches(n event.BoundaryEvent, vars map[string]any, cause error, errorCode string, eval ConditionEvaluator) (bool, error) {
	if n.ErrorCheck != nil {
		return n.ErrorCheck(vars, cause), nil
	}
	if n.ErrorExpr != "" {
		env := make(map[string]any, len(vars)+1)
		for k, v := range vars {
			env[k] = v
		}
		env["_error"] = errorCode
		return eval.EvalBool(n.ErrorExpr, env)
	}
	return n.ErrorCode == "" || n.ErrorCode == errorCode, nil
}
```
(Extracting this de-duplicates the two hand-copied match sites and removes drift risk.)

- [ ] **Step 3: Wire the cause + helper.** Add a `cause error` parameter to `propagateError` (`step_errors.go:60`). Replace the bare `n.ErrorCode == "" || n.ErrorCode == errorCode` at **L86** and **L189** with a call to `boundaryErrorMatches(n, s.Variables, cause, errorCode, eval)` (handle its returned error — treat an expr evaluation error as no-match + surface per existing error conventions, or fail the step; match how gateway condition-eval errors are handled). When the caller passes `cause == nil`, synthesize `errors.New(errorCode)` for the closure — do this once at the top of `propagateError` (`if cause == nil { cause = errors.New(errorCode) }`).

- [ ] **Step 4: Pass the cause from callers.** In `engine/step_triggers.go` `handleActionFailed`, pass `t.Cause` at both `propagateError` call sites (~L301, L315). Any other `propagateError` caller (error-end-event/sub-instance) passes `nil`. Update the `propagateError` signature at all call sites.

- [ ] **Step 5: Run** — `go test ./engine/... -run 'BoundaryError|propagateError|Error' -count=1` → PASS; full `go test ./engine/... -count=1` green (no regression in existing error tests).

- [ ] **Step 6: Commit** — `feat(engine): flexible boundary error matching (Check→Expr→Code) with live-error cause`

---

## Task 5: Split `WithDeadline` → `WithDeadlineFlow` + `WithDeadlineAction`

**Files:** `definition/activity/options.go`; the 19 call sites listed below; parity test.

**Interfaces — Produces:** `WithDeadlineFlow(t schedule.TriggerSpec, flowID string) activityOnlyOption`, `WithDeadlineAction(action string) activityOnlyOption`. `WithDeadline` (3-arg) REMOVED.

- [ ] **Step 1: Failing tests.** In `definition/activity/activity_test.go` (or the accessors test), assert `WithDeadlineFlow(t, "overdue")` sets `DeadlineTimer`+`DeadlineFlow` (via `model.DeadlineOf`) and `WithDeadlineAction("notify")` sets `DeadlineAction`. Plus a **parity** assertion (engine test) that the deadline-breach path (`handleDeadlineFired`) and the boundary-fire path (Task 2) both emit `InvokeAction{FireAndForget:true}`. Run → FAIL (`undefined: WithDeadlineFlow`).

- [ ] **Step 2: Implement the split.** In `definition/activity/options.go` replace `WithDeadline` (~L149-151) with:
```go
// WithDeadlineFlow sets the deadline timer (schedule.TriggerSpec) and the flow
// taken when it breaches. Pair with WithDeadlineAction for a fire-once breach action.
func WithDeadlineFlow(t schedule.TriggerSpec, flowID string) activityOnlyOption {
	return withActivity(func(a *model.ActivityFields) { a.DeadlineTimer, a.DeadlineFlow = t, flowID })
}

// WithDeadlineAction sets the optional fire-once action run when the deadline
// breaches (FireAndForget — same semantics as event.WithBoundaryAction).
func WithDeadlineAction(action string) activityOnlyOption {
	return withActivity(func(a *model.ActivityFields) { a.DeadlineAction = action })
}
```

- [ ] **Step 3: Migrate all 19 call sites.** Replace `WithDeadline(t, flow, action)` → `WithDeadlineFlow(t, flow), WithDeadlineAction(action)`; the single empty-arg site `engine/step_subprocess_test.go:1032` (`WithDeadline(t, "", "")`) → `WithDeadlineFlow(t, "")` only (no action option). Sites:
  - `runtime/timer_example_test.go:90`; `runtime/deadline_fireforget_e2e_test.go:87`;
  - `internal/persistence/store/definitions_conformance_test.go:45`;
  - `definition/activity/activity_test.go:22,141`; `definition/model/definition_test.go:268,299`;
  - `definition/model/accessors_test.go:77,83,148,355`; `definition/model/node_test.go:88`;
  - `examples/scenarios/usertask_deadline/main.go:71`;
  - `engine/step_timer_test.go:170,361,673`; `engine/step_timers_fireforget_test.go:24,43`;
  - `engine/step_subprocess_test.go:1032 (empty-arg),1620`.
  Leave `event.WithCatchDeadline` UNTOUCHED.

- [ ] **Step 4: Run** — `go build ./... && go test ./... -count=1` (scoped run of touched packages first) → PASS; `grep -rn 'WithDeadline(' --include='*.go' .` returns only `WithDeadlineFlow`/`WithDeadlineAction` (no 3-arg `WithDeadline` remains).

- [ ] **Step 5: Commit** — `refactor(activity): split WithDeadline into WithDeadlineFlow + WithDeadlineAction (breaking)`

---

## Task 6: Examples, godoc Examples, ADRs

**Files:** `examples/scenarios/boundary_action/main.go` (new); `examples/scenarios/usertask_deadline/main.go` (package doc); godoc `Example`s in `definition/event`; `docs/adr/0103-*.md`, `docs/adr/0104-*.md`.

- [ ] **Step 1: `boundary_action` example.** Create `examples/scenarios/boundary_action/main.go`: an interrupting timer boundary on a UserTask that BOTH runs `WithBoundaryAction("notify-overdue")` (fire-once) AND routes to an escalation Service task, deterministic via `*clockwork.FakeClock` + `kernel.NewMemScheduler` (`clk.Advance` + `sched.Tick`). Print milestones; assert the notify action ran, the human task was cancelled, and the instance completed via the escalation path (exit non-zero on failure). Model wiring on `examples/scenarios/timer_boundary` / `message_boundary`. Contrast it with `usertask_deadline` in the package doc. `go run ./examples/scenarios/boundary_action` → success.

- [ ] **Step 2: `usertask_deadline` package doc.** Rewrite the package doc comment to frame it as a user-task deadline (`WithDeadlineFlow`+`WithDeadlineAction`) demo; drop any "boundary" wording. No behavior change (the call site was migrated in Task 5).

- [ ] **Step 3: godoc Examples.** Add testable `Example` functions in `definition/event` for `WithBoundaryAction`, `WithBoundaryErrorCheck`, `WithBoundaryErrorExpr` (compile + `// Output:` match). Run `go test ./definition/event/ -run Example -count=1`.

- [ ] **Step 4: ADR-0103 + ADR-0104** (Nygard, two-bullet Status/Date like ADR-0001):
  - **0103** — boundary fire-once action + `WithDeadline` split. Consequences: `WithBoundaryAction` and `WithDeadlineAction` share identical `InvokeAction{FireAndForget:true}` semantics (a deadline is a timer boundary with a bundled fire-once action); the 3-arg `WithDeadline` is removed (breaking, pre-v1.0).
  - **0104** — flexible boundary error matching. Context: exact-code-only matching was limiting. Decision: Check→Expr→Code precedence; `ErrorCheck` (non-serializable Go closure, live error via non-persisted `ActionFailed.Cause`), `ErrorExpr` (serializable expr over vars + injected `_error`). Consequences: determinism preserved (snapshot-based engine; `propagateError` runs once at failure time, never on replay — cite `deliverLoop` committing the resulting `InstanceState`); `ErrorCheck` closures should avoid external mutable state; `_error` is a boundary-expr-only var (does not pre-exist elsewhere).

- [ ] **Step 5: Run + Commit** — `go build ./... && go test ./definition/event/... -count=1`; `docs(adr): 0103 boundary action + deadline split; 0104 flexible boundary error matching; boundary_action example + godoc Examples`.

---

## Verification Checklist

- [ ] `go build ./...` clean; `go test -race ./... -count=1` 0 fail / 0 races (PG+MySQL+SQLite testcontainers); `golangci-lint run ./...` 0 issues; touched packages ≥85% coverage.
- [ ] `WithBoundaryAction` emits `FireAndForget` `InvokeAction` before routing (interrupting + non-interrupting, all trigger types); a failing action still routes.
- [ ] Error matching precedence Check→Expr→Code, error boundaries only; live cause threaded via non-persisted `ActionFailed.Cause`; bare-code sources get a synthesized error; `boundaryErrorExpr` serializes, `ErrorCheck` does NOT.
- [ ] `WithDeadline` split; 3-arg form removed; all 19 sites migrated; deadline & boundary actions asserted `FireAndForget`-equal; `event.WithCatchDeadline` untouched.
- [ ] `boundary_action` builds/runs to expected outcome; `usertask_deadline` package doc reframed; godoc Examples pass.
- [ ] ADR-0103 + ADR-0104 present (Nygard); determinism documented.
- [ ] `/code-review` run; Critical/Important fixed.
- [ ] NO message-boundary bug-fix work (already fixed — confirmed by the assessment).

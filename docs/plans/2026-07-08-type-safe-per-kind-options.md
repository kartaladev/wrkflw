# Type-Safe Per-Kind Activity Options (Phase 1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `activity.WithWaitReminder` a compile-time error on node kinds that never arm reminders, and remove the now-redundant runtime `definition.Lint` advisory whose sole rule this compile guarantee replaces.

**Architecture:** The option system already encodes per-kind validity through marker interfaces (`UserTaskOption interface{ applyUserTask(*UserTask) }`, etc.). The only genuinely mis-scoped option is `WithWaitReminder`, which returns the broad `activityOnlyOption` (all 7 activity kinds) but is honored only by UserTask and ReceiveTask. We narrow it to `interface { UserTaskOption; ReceiveTaskOption }` via a dedicated `reminderOpt` struct, then delete `definition.Lint` (its one `reminder-ignored` rule is now a compile error) and the driver's `lintDefinition` WARN hook.

**Tech Stack:** Go 1.25; the existing `definition/activity`, `definition`, and `runtime` packages. No new dependencies. `lestrrat-go/option` was evaluated and rejected (see spec — its technique is identical to what the codebase already does but adds runtime boxing).

## Global Constraints

- Go 1.25; module path `github.com/zakyalvan/krtlwrkflw`.
- Behavior-preserving: all existing tests stay green except the deliberately-deleted lint tests and the two reminder assertions removed from the ServiceTask test. No runtime behavior changes for UserTask/ReceiveTask reminders.
- No new dependency; keep the existing marker-interface + `applyXxx` pattern. Do NOT adopt `lestrrat-go/option`.
- TDD discipline (CLAUDE.md): every behavioral change is preceded by an observable red state in a Bash `go test`/`go build` run. This phase is largely a behavior-preserving refactor whose "test" is the type system — the observable red is the compile failure the narrowing induces in existing mis-scoped call sites, made green by fixing those sites.
- Error sentinel prefix convention `workflow-<pkg>:` — not relevant here (no new sentinels).
- Plans/specs never under a path containing `superpowers`.
- ADR: allocate **ADR-0113** (Nygard template: Status/Date, Context, Decision, Consequences).

---

### Task 1: Narrow `WithWaitReminder` to UserTask + ReceiveTask only

**Files:**
- Modify: `definition/activity/options.go:144-149` (replace the `WithWaitReminder` definition)
- Modify: `definition/activity/activity_test.go:15-45` (`TestServiceTaskOptions` — remove the reminder line + reminder assertion, now a compile error)

**Interfaces:**
- Consumes: `UserTaskOption`, `ReceiveTaskOption` (existing marker interfaces in `options.go`); `schedule.TriggerSpec`; `model.ActivityFields.ReminderEvery`/`ReminderAction` (embedded in `UserTask` and `ReceiveTask`).
- Produces: `WithWaitReminder(t schedule.TriggerSpec, action string) interface { UserTaskOption; ReceiveTaskOption }` — narrowed public signature; unchanged runtime effect on UserTask/ReceiveTask.

- [ ] **Step 1: Replace the `WithWaitReminder` definition in `options.go`**

Replace lines 144-149 (the current `activityOnlyOption`-returning form) with a dedicated narrow struct. Place the `reminderOpt` type + methods just above `WithWaitReminder`:

```go
// reminderOpt narrows WithWaitReminder to only the activity kinds whose engine
// strategy actually arms an in-wait reminder: UserTask and ReceiveTask.
// IntermediateCatchEvent uses the event-side event.WithCatchWaitReminder.
// Applying a reminder to any other activity kind (ServiceTask, SendTask,
// BusinessRuleTask, SubProcess, CallActivity) is a compile-time error.
type reminderOpt struct {
	every  schedule.TriggerSpec
	action string
}

func (o reminderOpt) applyUserTask(u *UserTask) {
	u.ReminderEvery, u.ReminderAction = o.every, o.action
}

func (o reminderOpt) applyReceiveTask(r *ReceiveTask) {
	r.ReminderEvery, r.ReminderAction = o.every, o.action
}

// WithWaitReminder sets the ReminderEvery (schedule.TriggerSpec) and ReminderAction
// on a UserTask or ReceiveTask — the only activity kinds whose engine strategy arms
// an in-wait reminder. Passing it to any other activity constructor is a compile
// error (see reminderOpt). Use schedule.Every, schedule.EveryExpr, or any other
// recurring TriggerSpec constructor. For IntermediateCatchEvent, use
// event.WithCatchWaitReminder instead.
func WithWaitReminder(t schedule.TriggerSpec, action string) interface {
	UserTaskOption
	ReceiveTaskOption
} {
	return reminderOpt{t, action}
}
```

- [ ] **Step 2: Run the build to observe the RED (compile failure at the mis-scoped call sites)**

Run: `go build ./... 2>&1 | head -30`
Expected: FAIL — compile errors where `WithWaitReminder` is applied to a non-UserTask/ReceiveTask kind. At least:
- `definition/activity/activity_test.go` — `WithWaitReminder(...)` passed to `NewServiceTask(...)` (cannot use as `ServiceTaskOption`).
- `runtime/processdriver_lint_test.go` — same, on a ServiceTask.
- `definition/lint_test.go` — same, on a ServiceTask.

This compile failure IS the evidence the narrowing took effect. (The lint test files are deleted in Task 3; fix only the ServiceTask production test here.)

- [ ] **Step 3: Fix `TestServiceTaskOptions` — remove the reminder line and its assertion**

In `definition/activity/activity_test.go`, delete the `activity.WithWaitReminder(schedule.EveryExpr(`"30m"`), "ping"),` line from the `NewServiceTask("charge", ...)` call, and delete the reminder assertion block:

```go
	re, ra := model.ReminderOf(n)
	if re.IsZero() || ra != "ping" {
		t.Errorf("ReminderOf = %v,%q", re, ra)
	}
```

Leave the rest of `TestServiceTaskOptions` (name/action/deadline/retry assertions) intact. If `schedule` or `model` imports become unused after this, remove them — but `schedule` is still used by `WithDeadline` and `model` by other assertions, so both stay.

- [ ] **Step 4: Verify the activity package compiles and its tests pass**

Run: `go build ./definition/... && go test ./definition/activity/... -count=1`
Expected: PASS (note: `go build ./...` still fails on the two lint test files — those are removed in Task 3).

- [ ] **Step 5: Commit**

```bash
git add definition/activity/options.go definition/activity/activity_test.go
git commit -m "refactor(definition): narrow WithWaitReminder to UserTask+ReceiveTask (compile-time scoping)"
```

---

### Task 2: Remove `definition.Lint` and the `Warning` type

**Files:**
- Delete: `definition/lint.go`
- Delete: `definition/lint_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: removes public `definition.Lint(def *model.ProcessDefinition) []Warning` and `definition.Warning` from the API. The one rule they enforced (`reminder-ignored`) is now a compile error (Task 1).

- [ ] **Step 1: Delete both files**

```bash
git rm definition/lint.go definition/lint_test.go
```

- [ ] **Step 2: Run the build to observe the RED (driver still references `definition.Lint`)**

Run: `go build ./... 2>&1 | head -20`
Expected: FAIL — `runtime/processdriver.go` references `definition.Lint` (undefined) and `runtime/processdriver_lint_test.go` references `definition.Lint`/`definition.Warning` (undefined). This confirms the removal surface; fixed in Task 3.

- [ ] **Step 3: Commit (staged deletion; build is red until Task 3 — commit together is acceptable, but keep the deletion isolated)**

Do NOT commit yet — the tree does not build. Proceed directly to Task 3, then commit Tasks 2+3 together. (This step is a marker: the deletion is staged.)

---

### Task 3: Remove the driver's `lintDefinition` hook and the lint test file

**Files:**
- Modify: `runtime/processdriver.go` — remove struct fields, constructor init, two call sites, the `lintDefinition` method, and now-unused imports (`strconv`, `definition`)
- Delete: `runtime/processdriver_lint_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `ProcessDriver` no longer runs any lint pass on `Drive`/`ApplyTrigger`; behavior otherwise unchanged.

- [ ] **Step 1: Remove the `lintMu`/`lintedDefs` struct fields**

In `runtime/processdriver.go`, delete this block (currently ~lines 87-92):

```go
	// lintMu guards lintedDefs.
	lintMu sync.Mutex
	// lintedDefs records the (id, version) of definitions already passed through
	// definition.Lint so each definition's advisory warnings are logged at most
	// once, not on every Drive/ApplyTrigger.
	lintedDefs map[string]struct{}
```

- [ ] **Step 2: Remove the constructor initialization**

Delete the `lintedDefs: make(map[string]struct{}),` line (currently ~line 147) from the `ProcessDriver` struct literal in the constructor.

- [ ] **Step 3: Remove the two `lintDefinition` call sites**

Delete `driver.lintDefinition(ctx, def)` from `Drive` (~line 298) and from `ApplyTrigger` (~line 366).

- [ ] **Step 4: Remove the `lintDefinition` method**

Delete the entire method (currently ~lines 318-344), from the `// lintDefinition runs definition.Lint ...` doc comment through the closing brace.

- [ ] **Step 5: Remove now-unused imports**

In the import block, remove `"strconv"` (only used by the deleted key builder) and `"github.com/zakyalvan/krtlwrkflw/definition"` (only used by the deleted method). Verify with:

Run: `grep -n 'strconv\.\|definition\.' runtime/processdriver.go`
Expected: no matches.

- [ ] **Step 6: Delete the lint test file**

```bash
git rm runtime/processdriver_lint_test.go
```

- [ ] **Step 7: Verify the whole tree builds and runtime + definition tests pass**

Run: `go build ./... && go test ./runtime/... ./definition/... -count=1`
Expected: PASS (GREEN — the compile failures from Task 1 Step 2 and Task 2 Step 2 are now all resolved).

- [ ] **Step 8: Commit Tasks 2+3 together**

```bash
git add -A
git commit -m "refactor(definition,runtime): remove definition.Lint + driver lintDefinition hook (rule now compile-enforced)"
```

---

### Task 4: Document the convention and write ADR-0113

**Files:**
- Modify: `definition/activity/options.go` — add a package/interface-level doc note on the subset-narrowing convention (if not already covered by the `reminderOpt` doc)
- Create: `docs/adr/0113-type-safe-per-kind-options.md`

**Interfaces:**
- Consumes: nothing.
- Produces: ADR-0113 recording the decision; documented convention on the option interfaces.

- [ ] **Step 1: Add the convention note above the option-interface block in `options.go`**

Add, just above `// --- option interfaces ---` (line 11):

```go
// Option-scoping convention: an option valid on only a subset of activity kinds
// MUST return a narrow anonymous interface embedding just those kinds' option
// interfaces (e.g. WithActionName returns interface{ServiceTaskOption;
// BusinessRuleOption}), so mis-applying it is a compile-time error. The broad
// activityOnlyOption type means "valid on EVERY activity kind" and is reserved
// for genuinely-universal options (WithRetryPolicy, WithCompensation,
// WithDeadline, WithRecoveryFlow, WithCancelHandler). There is no runtime lint
// pass; the type system is the guardrail.
```

- [ ] **Step 2: Write ADR-0113 (Nygard template)**

Create `docs/adr/0113-type-safe-per-kind-options.md` following the Nygard template used by `docs/adr/0001-record-architecture-decisions.md`:
- **Status:** Accepted — Date: 2026-07-08
- **Context:** The option system already encodes per-kind validity via marker interfaces; the one genuinely mis-scoped option (`WithWaitReminder`) was papered over by a runtime `definition.Lint` advisory (`reminder-ignored`). `lestrrat-go/option` was evaluated and rejected (its restriction technique is identical to the existing pattern but adds runtime boxing / no extra safety).
- **Decision:** Narrow `WithWaitReminder` to `interface { UserTaskOption; ReceiveTaskOption }`; keep `activityOnlyOption` for the broad-but-correct options; remove `definition.Lint`, the `Warning` type, and the driver's `lintDefinition` hook. Document the subset-narrowing convention on the option interfaces. Keep the marker-interface + `applyXxx` pattern; no new dependency.
- **Consequences:** Mis-applying `WithWaitReminder` is now a compile error, not a silently-ignored runtime warning. The public `definition.Lint`/`definition.Warning` API is removed (pre-1.0, acceptable). Future non-type-enforceable advisories need a fresh, purpose-built mechanism. Later phases (validation, completion-action) add options born-narrow using this convention.

- [ ] **Step 3: Verify build + lint clean, then commit**

Run: `go build ./... && golangci-lint run ./definition/... ./runtime/...`
Expected: clean.

```bash
git add definition/activity/options.go docs/adr/0113-type-safe-per-kind-options.md
git commit -m "docs(adr): ADR-0113 type-safe per-kind options; document subset-narrowing convention"
```

---

## Verification Checklist (run before declaring the phase done)

- [ ] `go build ./...` clean.
- [ ] `go test -race ./... -count=1` — 0 failures, 0 races (PG+MySQL+SQLite testcontainers require a running Docker daemon).
- [ ] `golangci-lint run ./...` — 0 issues.
- [ ] `grep -rn 'definition\.Lint\|definition\.Warning\|lintDefinition' --include='*.go' .` — no matches (all removed).
- [ ] `grep -rn 'WithWaitReminder' --include='*.go' .` — every remaining call site is on a `NewUserTask` or `NewReceiveTask` (or the `event.WithCatchWaitReminder` counterpart), never a ServiceTask/SendTask/BusinessRule/SubProcess/CallActivity.
- [ ] `engine/` and `definition/model/` production code (non-test) unchanged (this phase touches only `definition/activity/options.go`, `definition/lint.go` [deleted], and `runtime/processdriver.go`).
- [ ] ADR-0113 present and follows the Nygard template.
- [ ] `/code-review` run on the branch diff; findings triaged/resolved.

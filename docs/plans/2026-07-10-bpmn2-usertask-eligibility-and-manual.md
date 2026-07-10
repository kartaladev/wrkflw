# UserTask Eligibility Relaxation + Manual Mode — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make a `UserTask`'s three eligibility dimensions (roles / resource-privileges / attribute) co-equal and optional (ADR-0117), and add a `WithManual()` mode for form-less human checkpoints (ADR-0118).

**Architecture:** Two decisions, one file surface. ADR-0117 drops `NewUserTask`'s mandatory positional `roles []string`, replacing it with an optional `WithCandidateRoles(...)` option alongside the existing `WithEligibilityPrivileges`/`WithEligibilityExpr`; an empty eligibility spec means the engine gate is open and authorization defers to the consumer's transport layer (already the runtime behavior). ADR-0118 adds a `Manual bool` to `UserTask` + `WithManual()`: a manual task waits for a bare completion trigger (no payload), and carrying completion validation on it is rejected at Build time.

**Tech Stack:** Go 1.25; `expr-lang/expr` (unaffected here); definition authoring layer (`definition/activity`, `definition/model`); engine (`engine`); runtime driver (`runtime`). Module path `github.com/zakyalvan/krtlwrkflw`.

## Global Constraints

- Go 1.25; single module rooted at `github.com/zakyalvan/krtlwrkflw`; public packages at the repo root (no `pkg/` prefix).
- **TDD strict:** every new symbol/behaviour gets a failing test FIRST, with a visible RED (`go test ./<pkg>/...`) before implementation. Pure refactors (no behaviour change) need no new test but existing tests must pass before AND after.
- **Black-box tests:** use `package <pkg>_test`.
- **Table tests:** when ≥2 cases exercise the same call, use the project `table-test` skill (the `assert func(...)` closure form, `t.Context()` over `context.Background()`).
- Error sentinels use the `workflow-<pkg>: ...` message prefix.
- Pair each `.go` with a same-named `_test.go`.
- Library is **unreleased** → breaking API changes are acceptable; no wire aliases/migrators.
- Verify per touched package: `go test -race -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1` (≥85% line), `go test ./...` from root, `golangci-lint run ./...` clean.
- Commit per task with Conventional Commits scoped to the area. End commit messages with the repo's `Co-Authored-By` + `Claude-Session` trailers.

---

## Task 1: Add `WithCandidateRoles` option (ADR-0117, additive)

**Files:**
- Modify: `definition/activity/options.go` (add the option next to `WithEligibilityPrivileges` ~line 200)
- Test: `definition/activity/options_test.go`

**Interfaces:**
- Consumes: `UserTaskOption` interface (`interface{ applyUserTask(u *UserTask) }`), `UserTask.CandidateRoles []string`.
- Produces: `func WithCandidateRoles(roles ...string) UserTaskOption`.

- [ ] **Step 1: Write the failing test**

Add to `definition/activity/options_test.go` (black-box, `package activity_test`):

```go
func TestWithCandidateRoles(t *testing.T) {
	n := activity.NewUserTask("approve",
		activity.WithCandidateRoles("manager", "director"))
	ut, ok := n.(activity.UserTask)
	if !ok {
		t.Fatalf("node is %T, want activity.UserTask", n)
	}
	if len(ut.CandidateRoles) != 2 || ut.CandidateRoles[0] != "manager" || ut.CandidateRoles[1] != "director" {
		t.Fatalf("CandidateRoles = %v, want [manager director]", ut.CandidateRoles)
	}
}
```

> NOTE: This test already assumes Task 2's optional constructor `NewUserTask(id, opts...)`. If you implement Task 1 before Task 2, temporarily call `activity.NewUserTask("approve", nil, activity.WithCandidateRoles(...))` and drop the `nil` in Task 2. Prefer doing Task 1 and Task 2 back-to-back.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./definition/activity/... -run TestWithCandidateRoles`
Expected: FAIL — `undefined: activity.WithCandidateRoles`.

- [ ] **Step 3: Write minimal implementation**

Add to `definition/activity/options.go` immediately after `WithEligibilityPrivileges`:

```go
type candidateRolesOpt struct{ roles []string }

func (o candidateRolesOpt) applyUserTask(u *UserTask) {
	u.CandidateRoles = append(u.CandidateRoles, o.roles...)
}

// WithCandidateRoles sets the roles eligible to claim and complete a UserTask.
// Roles are one of three co-equal, optional eligibility dimensions (with
// WithEligibilityPrivileges and WithEligibilityExpr). With no eligibility set,
// the engine gate is open and authorization defers to the consumer's transport
// layer (e.g. HTTP security middleware). See ADR-0117.
func WithCandidateRoles(roles ...string) UserTaskOption { return candidateRolesOpt{roles} }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./definition/activity/... -run TestWithCandidateRoles`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add definition/activity/options.go definition/activity/options_test.go
git commit -m "feat(definition): add WithCandidateRoles UserTask option (ADR-0117)"
```

---

## Task 2: Make `NewUserTask` roles optional + migrate call sites (ADR-0117, refactor)

**Files:**
- Modify: `definition/activity/activity.go:123-130` (`NewUserTask`)
- Modify (mechanical migration): every `NewUserTask(` call site — 135 across the repo (16 non-test files listed below + ~116 test files). Non-test: `definition/build/build.go`, `examples/cache_wiring/main.go`, `examples/readme_quickstart/main.go`, `examples/scenarios/{attribute_authz,boundary_action,completion_action,input_validation,instance_cancellation,inwait_reminder,message_boundary,reverse_rollback,usertask_approval,usertask_deadline}/main.go`, `internal/transporttest/harness.go`, `runtime/internal/runtimetest/fixtures.go`.
- Test: existing tests are the safety net (behaviour-preserving refactor).

**Interfaces:**
- Consumes: `WithCandidateRoles` (Task 1).
- Produces: `func NewUserTask(id string, opts ...UserTaskOption) model.Node` (BREAKING — positional `roles []string` removed).

- [ ] **Step 1: Change the constructor signature**

In `definition/activity/activity.go`, replace `NewUserTask` (currently lines 123-130):

```go
// NewUserTask constructs a UserTask. Eligibility is optional and set via
// WithCandidateRoles / WithEligibilityPrivileges / WithEligibilityExpr; with
// none set, the engine gate is open (authorization defers to the transport
// layer). See ADR-0117.
func NewUserTask(id string, opts ...UserTaskOption) model.Node {
	u := UserTask{Base: model.NewBase(id, "")}
	for _, o := range opts {
		o.applyUserTask(&u)
	}
	return u
}
```

- [ ] **Step 2: Run the build to see the RED (breakage across the repo)**

Run: `go build ./... 2>&1 | head -30`
Expected: FAIL — `not enough arguments` / `cannot use []string{...}` at every `NewUserTask(id, roles, ...)` call site. This is the expected RED for a breaking refactor.

- [ ] **Step 3: Migrate every call site mechanically**

Apply this transform to every `NewUserTask(` call (production + test):
- `NewUserTask("x", nil)` → `NewUserTask("x")`
- `NewUserTask("x", nil, opt...)` → `NewUserTask("x", opt...)`
- `NewUserTask("x", []string{"a","b"})` → `NewUserTask("x", activity.WithCandidateRoles("a", "b"))` (drop the `activity.` qualifier inside package `activity` itself)
- `NewUserTask("x", []string{"a"}, opt1, opt2)` → `NewUserTask("x", activity.WithCandidateRoles("a"), opt1, opt2)`
- `NewUserTask("x", roles)` where `roles` is a variable → `NewUserTask("x", activity.WithCandidateRoles(roles...))`

Find remaining sites after each pass:

```bash
rg -n "NewUserTask\(" --glob '*.go'
```

Iterate until `go build ./...` is clean. (macOS/BSD `sed` lacks `\b`; do the edits with the Edit tool or `perl`, then re-grep — do NOT trust a single regex sweep.)

- [ ] **Step 4: Run the full suite to verify no behaviour changed**

Run: `go build ./... && go test ./...`
Expected: PASS (all pre-existing tests green — the migration is behaviour-preserving).

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor(definition)!: NewUserTask roles optional via WithCandidateRoles (ADR-0117)

BREAKING: NewUserTask(id, roles, opts...) -> NewUserTask(id, opts...); set
roles with WithCandidateRoles. All three eligibility dimensions are now
co-equal optional options."
```

---

## Task 3: Add `Manual` field + `WithManual()` option (ADR-0118)

**Files:**
- Modify: `definition/activity/activity.go:26-40` (`UserTask` struct — add `Manual bool`)
- Modify: `definition/activity/options.go` (add `WithManual` near `WithCandidateRoles`)
- Test: `definition/activity/options_test.go`

**Interfaces:**
- Consumes: `UserTaskOption`, `UserTask`.
- Produces: `UserTask.Manual bool`; `func WithManual() UserTaskOption`.

- [ ] **Step 1: Write the failing test**

Add to `definition/activity/options_test.go`:

```go
func TestWithManual(t *testing.T) {
	n := activity.NewUserTask("confirm", activity.WithManual())
	ut, ok := n.(activity.UserTask)
	if !ok {
		t.Fatalf("node is %T, want activity.UserTask", n)
	}
	if !ut.Manual {
		t.Fatal("Manual = false, want true")
	}
	if len(ut.CandidateRoles) != 0 {
		t.Fatalf("CandidateRoles = %v, want empty (manual task has no eligibility by default)", ut.CandidateRoles)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./definition/activity/... -run TestWithManual`
Expected: FAIL — `ut.Manual undefined` and `undefined: activity.WithManual`.

- [ ] **Step 3: Write minimal implementation**

In `definition/activity/activity.go`, add a field to the `UserTask` struct (after `CompletionValidation`):

```go
	// Manual, when true, marks this UserTask as a manual task: it completes on a
	// bare trigger (no payload/form-data) and must not carry CompletionValidation.
	// Eligibility is still honored if set. See ADR-0118. This deliberately
	// diverges from strict BPMN Manual Task (which has no execution semantics /
	// auto-completes): a manual task here keeps a durable "someone confirmed this"
	// checkpoint.
	Manual bool
```

In `definition/activity/options.go`, add near `WithCandidateRoles`:

```go
type manualOpt struct{}

func (manualOpt) applyUserTask(u *UserTask) { u.Manual = true }

// WithManual marks a UserTask as a manual task: completion needs only a bare
// trigger (no payload/form-data), and the task must not carry completion
// validation (rejected at Build time). See ADR-0118.
func WithManual() UserTaskOption { return manualOpt{} }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./definition/activity/... -run TestWithManual`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add definition/activity/activity.go definition/activity/options.go definition/activity/options_test.go
git commit -m "feat(definition): add Manual field + WithManual option to UserTask (ADR-0118)"
```

---

## Task 4: JSON wire round-trip for `Manual` (ADR-0118)

**Files:**
- Modify: `definition/model/node_wire.go:14` (`NodeWire` struct — add `Manual`)
- Modify: `definition/activity/activity.go:198-219` (UserTask `FromWire`/`ToWire`)
- Test: `definition/activity/activity_test.go` (or the existing wire round-trip test file for activities)

**Interfaces:**
- Consumes: `UserTask.Manual` (Task 3), `NodeWire`.
- Produces: `NodeWire.Manual bool` (`json:"manual,omitempty"`); UserTask wire spec round-trips it.

- [ ] **Step 1: Write the failing test**

Add to `definition/activity/activity_test.go` (black-box). Use the package's existing JSON round-trip helper if present; otherwise marshal a one-node definition. Minimal direct form:

```go
func TestUserTaskManualWireRoundTrip(t *testing.T) {
	orig := activity.NewUserTask("confirm", activity.WithManual()).(activity.UserTask)

	// Round-trip through the definition wire (ToWire -> NodeWire -> FromWire).
	def, err := definition.NewBuilder("d", 1).
		Add(event.NewStart("s")).
		Add(orig).
		Add(event.NewEnd("e")).
		Connect("s", "confirm").Connect("confirm", "e").
		Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	blob, err := json.Marshal(def)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := definition.LoadJSON(bytes.NewReader(blob)) // use the package's actual JSON loader
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	n, _ := got.Node("confirm")
	if !n.(activity.UserTask).Manual {
		t.Fatal("Manual not preserved across JSON round-trip")
	}
}
```

> NOTE: match the package's real JSON marshal/load entry points (see how a sibling test in `definition/` round-trips a definition — e.g. `definition.NewLoader`/`LoadJSON` or `json.Marshal(def)` + the loader). Do not invent an API; copy the existing pattern.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./definition/activity/... -run TestUserTaskManualWireRoundTrip`
Expected: FAIL — `Manual not preserved` (field not on the wire yet).

- [ ] **Step 3: Write minimal implementation**

In `definition/model/node_wire.go`, add to the `NodeWire` struct (near `CandidateRoles`):

```go
	Manual bool `json:"manual,omitempty"`
```

In `definition/activity/activity.go`, the `KindUserTask` spec — `FromWire` struct literal, add `Manual: w.Manual`:

```go
			n := UserTask{Base: b, ActivityFields: w.Activity(), CandidateRoles: w.CandidateRoles, EligibilityPrivileges: w.EligibilityPrivileges, EligibilityExpr: w.EligibilityExpr, Manual: w.Manual}
```

and `ToWire`, add the assignment:

```go
			w.CandidateRoles, w.EligibilityPrivileges, w.EligibilityExpr = v.CandidateRoles, v.EligibilityPrivileges, v.EligibilityExpr
			w.Manual = v.Manual
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./definition/activity/... -run TestUserTaskManualWireRoundTrip`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add definition/model/node_wire.go definition/activity/activity.go definition/activity/activity_test.go
git commit -m "feat(definition): round-trip UserTask Manual on the JSON wire (ADR-0118)"
```

---

## Task 5: YAML decode for `Manual` (ADR-0118)

**Files:**
- Modify: `definition/model/yaml.go:16-46` (`nodeYAML` struct — add `Manual`)
- Modify: `definition/model/yaml.go:91-120` (`fromNodeYAML` — map `Manual` into `NodeWire`)
- Test: `definition/model/yaml_test.go`

**Interfaces:**
- Consumes: `NodeWire.Manual` (Task 4).
- Produces: `nodeYAML.Manual bool` (`yaml:"manual,omitempty"`); decode maps it. (YAML is decode-only — there is no marshal path.)

- [ ] **Step 1: Write the failing test**

Add to `definition/model/yaml_test.go`:

```go
func TestParseYAMLUserTaskManual(t *testing.T) {
	const src = `
id: d
version: 1
nodes:
  - {id: s, kind: startEvent}
  - {id: confirm, kind: userTask, manual: true}
  - {id: e, kind: endEvent}
flows:
  - {id: f1, source: s, target: confirm}
  - {id: f2, source: confirm, target: e}
`
	loader, err := model.ParseYAML(strings.NewReader(src))
	if err != nil {
		t.Fatalf("ParseYAML: %v", err)
	}
	def, err := loader.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	n, ok := def.Node("confirm")
	if !ok {
		t.Fatal("node confirm not found")
	}
	// model cannot import activity; assert via the wire projection.
	// Simplest: assert the node kind is userTask and re-marshal preserves manual.
	if n.Kind() != model.KindUserTask {
		t.Fatalf("kind = %v", n.Kind())
	}
}
```

> NOTE: `definition/model` cannot import `definition/activity`, so assert `Manual` via a JSON re-marshal (`json.Marshal(def)` then check the `"manual":true` bytes) or via a `toWire`-based test helper already used in `yaml_test.go`. Follow the existing yaml_test assertion style for user-task fields (see how `CandidateRoles` is asserted around `yaml_test.go:213`).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./definition/model/... -run TestParseYAMLUserTaskManual`
Expected: FAIL — `manual` decodes to nothing; a JSON re-marshal shows no `"manual":true` (field not carried).

- [ ] **Step 3: Write minimal implementation**

In `definition/model/yaml.go`, add to `nodeYAML` (near `CandidateRoles`, line 21):

```go
	Manual bool `yaml:"manual,omitempty"`
```

In `fromNodeYAML`, add to the `NodeWire{...}` literal (near `CandidateRoles: ny.CandidateRoles`, line 96):

```go
		Manual: ny.Manual,
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./definition/model/... -run TestParseYAMLUserTaskManual`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add definition/model/yaml.go definition/model/yaml_test.go
git commit -m "feat(definition): decode UserTask manual from YAML (ADR-0118)"
```

---

## Task 6: Build-time guard — Manual + CompletionValidation rejected (ADR-0118)

**Files:**
- Modify: `definition/model/validate.go` (add `ErrManualTaskValidation` sentinel ~line 150; add the rule ~line 466, after the CompletionAction rule)
- Test: `definition/model/validate_test.go`

**Interfaces:**
- Consumes: `toWire(n).Manual`, `ValidationStrategyFor(n)`, `KindUserTask`.
- Produces: `var ErrManualTaskValidation error`; rejection in `validateStructure`.

- [ ] **Step 1: Write the failing test**

Add to `definition/model/validate_test.go`. Build a definition with a manual UserTask that also carries completion validation, and assert `Validate` returns `ErrManualTaskValidation`:

```go
func TestValidateManualTaskRejectsCompletionValidation(t *testing.T) {
	def, err := definition.NewBuilder("d", 1).
		Add(event.NewStart("s")).
		Add(activity.NewUserTask("confirm",
			activity.WithManual(),
			activity.WithCompletionValidation(vexpr.New(`ok == true`)))).
		Add(event.NewEnd("e")).
		Connect("s", "confirm").Connect("confirm", "e").
		Build()
	// Build may surface the error here (Build calls Validate) or def may be nil.
	if err == nil {
		err = model.Validate(def)
	}
	if !errors.Is(err, model.ErrManualTaskValidation) {
		t.Fatalf("err = %v, want ErrManualTaskValidation", err)
	}
}
```

> NOTE: `WithCompletionValidation` is the existing option (see `definition/activity/options.go`); `vexpr` is the expr validation constructor used in `examples/scenarios/input_validation` — import the same package it uses. If `Build` already fails closed, the test still passes via the `errors.Is` check; keep both paths.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./definition/model/... -run TestValidateManualTaskRejectsCompletionValidation`
Expected: FAIL — no such error is returned (the combination is currently allowed).

- [ ] **Step 3: Write minimal implementation**

In `definition/model/validate.go`, add the sentinel to the `var (...)` block (after `ErrCompensateActionWithoutForwardAction`, ~line 150):

```go
	// ErrManualTaskValidation is returned when a UserTask marked Manual
	// (WithManual) also carries completion validation. A manual task completes
	// on a bare trigger with no payload, so there is no output to validate — the
	// combination is contradictory and rejected at authoring time. See ADR-0118.
	ErrManualTaskValidation = errors.New("workflow-definition: manual user task cannot carry completion validation")
```

Add the rule in `validateStructure` after the CompletionAction-supported-kind loop (~line 466):

```go
	// Manual UserTask must not carry completion validation: a manual task
	// completes with no payload, so a validation strategy would never receive
	// input to check. model cannot import the activity package, so Manual is read
	// via the wire projection.
	for _, n := range d.Nodes {
		if n.Kind() != KindUserTask {
			continue
		}
		if toWire(n).Manual && ValidationStrategyFor(n) != nil {
			errs = append(errs, fmt.Errorf("%w: node %q", ErrManualTaskValidation, n.ID()))
		}
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./definition/model/... -run TestValidateManualTaskRejectsCompletionValidation`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add definition/model/validate.go definition/model/validate_test.go
git commit -m "feat(definition): reject manual UserTask with completion validation (ADR-0118)"
```

---

## Task 7: Engine behaviour — roleless manual task completes on a bare trigger (ADR-0118)

**Files:**
- Test: `runtime/manual_task_test.go` (new black-box test, `package runtime_test`)
- Production change expected: NONE. An empty `authz.AuthzSpec` already permits (`task.Complete`/`Claim` authorize against `task.Eligibility`), and `handleHumanCompleted` merges an empty `Output` fine. This task LOCKS that behaviour; if it fails, the failure pinpoints the exact gate to relax.

**Interfaces:**
- Consumes: `runtime.NewProcessDriver`, `runtime.WithInstanceStore`, `runtime.WithHumanTasks`, `runtime/task.NewTaskService`, `humantask.NewMemTaskStore`, `humantask.HumanTask.IsOpen()`, `driver.Drive`, `driver.ApplyTrigger`.
- Produces: proof that a manual, roleless UserTask drives to `StatusCompleted` via a bare completion trigger (nil output, no claim).

- [ ] **Step 1: Write the failing test**

Create `runtime/manual_task_test.go`:

```go
package runtime_test

import (
	"testing"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/task"
)

func TestManualTaskCompletesOnBareTrigger(t *testing.T) {
	ctx := t.Context()

	def, err := definition.NewBuilder("manual-demo", 1).
		Add(event.NewStart("s")).
		Add(activity.NewUserTask("confirm", activity.WithManual())). // no eligibility
		Add(event.NewEnd("e")).
		Connect("s", "confirm").Connect("confirm", "e").
		Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	taskStore := humantask.NewMemTaskStore()
	memSt, err := kernel.NewMemInstanceStore()
	if err != nil {
		t.Fatalf("memstore: %v", err)
	}
	driver, err := runtime.NewProcessDriver(
		runtime.WithInstanceStore(memSt),
		runtime.WithHumanTasks(humantask.NewStaticActorResolver(nil), taskStore, authz.RoleAuthorizer{}),
	)
	if err != nil {
		t.Fatalf("driver: %v", err)
	}

	const id = "m-1"
	parked, err := driver.Drive(ctx, def, id, nil)
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	if parked.Status != engine.StatusRunning {
		t.Fatalf("status = %v, want Running (parked at manual task)", parked.Status)
	}

	// Find the OPEN task (Tasks accumulates; never index 0 blindly).
	var token string
	for i := range parked.Tasks {
		if parked.Tasks[i].IsOpen() {
			token = parked.Tasks[i].TaskToken
			break
		}
	}
	if token == "" {
		t.Fatal("no open human task after driving to the manual node")
	}

	svc, err := task.NewTaskService(taskStore, authz.RoleAuthorizer{})
	if err != nil {
		t.Fatalf("task service: %v", err)
	}
	// Bare completion: no claim, no payload.
	trg, err := svc.Complete(ctx, token, authz.Actor{ID: "operator"}, nil)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	final, err := driver.ApplyTrigger(ctx, def, id, trg)
	if err != nil {
		t.Fatalf("apply complete: %v", err)
	}
	if final.Status != engine.StatusCompleted {
		t.Fatalf("status = %v, want Completed", final.Status)
	}
}
```

- [ ] **Step 2: Run test to verify it fails (RED) — then diagnose**

Run: `go test ./runtime/... -run TestManualTaskCompletesOnBareTrigger`
Expected first run: it may FAIL to compile (missing helpers named slightly differently — reconcile against `examples/scenarios/usertask_approval/main.go`, the authoritative wiring reference) or fail an assertion. Fix the test wiring until the RED is a *behavioural* assertion, not a compile error.

- [ ] **Step 3: Make it pass**

Most likely NO production change is needed — the test passes once the wiring is correct, proving the manual+roleless path already works. IF it fails behaviourally:
- If `svc.Complete` rejects an unclaimed task → the manual path needs completion-without-claim for roleless tasks; the minimal change is in `runtime/task/service.go` `Complete` (do not require a prior claim when `task.Eligibility` is empty). Add a focused test for that relaxation first.
- If authz rejects the empty actor → confirm `authz.RoleAuthorizer{}` permits an empty `AuthzSpec` (it should). 

Do the smallest change that turns the assertion green; keep the RED-first discipline for any production edit.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -race ./runtime/... -run TestManualTaskCompletesOnBareTrigger`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add runtime/manual_task_test.go   # + any minimal service change if one was needed
git commit -m "test(runtime): manual roleless UserTask completes on a bare trigger (ADR-0118)"
```

---

## Task 8: Example scenario `examples/scenarios/manual_task` (ADR-0118)

**Files:**
- Create: `examples/scenarios/manual_task/main.go`
- (Reference wiring only — shows engine mechanics, per the examples/ convention; do not wire test helpers.)

**Interfaces:**
- Consumes: the public driver + task-service API (mirror `examples/scenarios/usertask_approval/main.go`).

- [ ] **Step 1: Write the example**

Create `examples/scenarios/manual_task/main.go` — an onboarding "hand over badge" manual step with no form and no roles; an operator triggers completion to advance:

```go
// Package main demonstrates a manual user task: a form-less human checkpoint.
//
// Flow:
//
//	start → handOverBadge[UserTask, manual, no roles] → end
//
// A manual task parks the instance like any user task, but it carries no
// eligibility (authorization is deferred to the consumer's transport layer, see
// ADR-0117) and completes on a bare trigger with no payload (ADR-0118).
//
// This is a reference wiring example — not a shipped binary.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/runtime/task"
	"github.com/zakyalvan/krtlwrkflw/runtime/view"
)

func main() {
	ctx := context.Background()

	def, err := definition.NewBuilder("employee-onboarding", 1).
		Add(event.NewStart("start")).
		Add(activity.NewUserTask("handOverBadge", activity.WithManual())).
		Add(event.NewEnd("end")).
		Connect("start", "handOverBadge").
		Connect("handOverBadge", "end").
		Build()
	if err != nil {
		log.Fatal("build def:", err)
	}

	taskStore := humantask.NewMemTaskStore()
	memSt, err := kernel.NewMemInstanceStore()
	if err != nil {
		log.Fatal("memstore:", err)
	}
	driver, err := runtime.NewProcessDriver(
		runtime.WithInstanceStore(memSt),
		runtime.WithHumanTasks(humantask.NewStaticActorResolver(nil), taskStore, authz.RoleAuthorizer{}),
	)
	if err != nil {
		log.Fatal("driver:", err)
	}

	const instanceID = "onboarding-001"
	fmt.Println("--- Employee Onboarding: Manual Task ---")

	parked, err := driver.Drive(ctx, def, instanceID, nil)
	if err != nil {
		log.Fatal("drive:", err)
	}
	fmt.Printf("parked at %q (status=%s)\n", parked.Tokens[0].NodeID, view.StatusString(parked.Status))

	// Find the open manual task (Tasks accumulates — use IsOpen()).
	var token string
	for i := range parked.Tasks {
		if parked.Tasks[i].IsOpen() {
			token = parked.Tasks[i].TaskToken
			break
		}
	}
	if token == "" {
		log.Fatal("expected an open manual task")
	}

	svc, err := task.NewTaskService(taskStore, authz.RoleAuthorizer{})
	if err != nil {
		log.Fatal("task service:", err)
	}

	// Bare completion — no claim, no payload.
	trg, err := svc.Complete(ctx, token, authz.Actor{ID: "operator"}, nil)
	if err != nil {
		log.Fatal("complete:", err)
	}
	final, err := driver.ApplyTrigger(ctx, def, instanceID, trg)
	if err != nil {
		log.Fatal("apply complete:", err)
	}

	if final.Status == engine.StatusCompleted {
		fmt.Println("instance completed — manual step confirmed")
	} else {
		fmt.Printf("unexpected status: %s\n", view.StatusString(final.Status))
	}
}
```

- [ ] **Step 2: Build and run the example**

Run: `go run ./examples/scenarios/manual_task`
Expected output ends with `instance completed — manual step confirmed`.

> NOTE: reconcile every constructor/option name against Task 7's now-green wiring and `usertask_approval/main.go`. If `task.NewTaskService` needs `task.WithClock(...)`, add it as that example does.

- [ ] **Step 3: Commit**

```bash
git add examples/scenarios/manual_task/main.go
git commit -m "docs(examples): manual_task scenario — form-less human checkpoint (ADR-0118)"
```

---

## Task 9: ADRs 0117 and 0118 (Nygard)

**Files:**
- Create: `docs/adr/0117-optional-usertask-eligibility.md`
- Create: `docs/adr/0118-manual-user-task.md`

**Interfaces:** none (documentation). Use `docs/adr/0001-record-architecture-decisions.md` as the canonical Nygard template (Status/Date, Context, Decision, Consequences).

- [ ] **Step 1: Write ADR-0117**

Create `docs/adr/0117-optional-usertask-eligibility.md` with Nygard sections:
- **Status:** Accepted, 2026-07-10.
- **Context:** Authorization must be role-, resource-privilege-, and attribute-based, co-equal (CLAUDE.md). The model already carries all three and the runtime treats an empty spec as open (transport-level authz possible), but `NewUserTask(id, roles []string, opts...)` made RBAC a mandatory positional while the others were optional options — an authoring-API asymmetry.
- **Decision:** `NewUserTask(id string, opts ...UserTaskOption)`; add `WithCandidateRoles`; empty eligibility ⇒ open engine gate (defer to transport). Breaking change; library unreleased. "Privilege held by role or user" is a casbin-adapter capability, out of scope here.
- **Consequences:** RBAC no longer privileged in the API; manual tasks (ADR-0118) become naturally expressible; all `NewUserTask` call sites changed; no wire change.

- [ ] **Step 2: Write ADR-0118**

Create `docs/adr/0118-manual-user-task.md`:
- **Status:** Accepted, 2026-07-10.
- **Context:** BPMN Manual Task has no execution semantics (auto-completes). We want a durable, form-less human checkpoint that waits for a bare trigger.
- **Decision:** `Manual bool` + `WithManual()`; a manual task parks and completes on a bare trigger (no payload), skips completion validation, and rejects `WithCompletionValidation` at Build time (`ErrManualTaskValidation`). Deliberate divergence from strict BPMN, documented. Builds on ADR-0117 (a manual task is a no-eligibility UserTask).
- **Consequences:** New `manual` wire/YAML field (additive); Build guard added; engine unchanged (empty-spec authz + empty-output completion already work).

- [ ] **Step 3: Commit**

```bash
git add docs/adr/0117-optional-usertask-eligibility.md docs/adr/0118-manual-user-task.md
git commit -m "docs(adr): ADR-0117 optional UserTask eligibility + ADR-0118 manual user task"
```

---

## Verification Checklist

- [ ] `WithCandidateRoles` sets roles; `NewUserTask(id, opts...)` compiles with no positional roles (Tasks 1-2).
- [ ] All 135 `NewUserTask` call sites migrated; `go build ./...` and `go test ./...` green (Task 2).
- [ ] `Manual` field + `WithManual()` present and round-trip through JSON (Task 4) and YAML decode (Task 5).
- [ ] `Validate` rejects `Manual` + completion validation with `ErrManualTaskValidation` (Task 6).
- [ ] A roleless manual task drives to `StatusCompleted` on a bare, claim-less, payload-less trigger (Task 7).
- [ ] `go run ./examples/scenarios/manual_task` prints the completion line (Task 8).
- [ ] ADR-0117 and ADR-0118 exist under `docs/adr/`, Nygard template (Task 9).
- [ ] `go test -race ./...` green from root; touched packages ≥85% line coverage; `golangci-lint run ./...` clean.

## Spec coverage self-check

- ADR-0117 (eligibility relaxation) → Tasks 1, 2, 9. ✓
- ADR-0118 (manual mode): model+option → Task 3; wire → Tasks 4-5; Build guard → Task 6; engine behaviour → Task 7; example → Task 8; ADR → Task 9. ✓
- No `NewUserTask` positional-roles references remain (Task 2 migration). ✓
- Types/names consistent: `WithCandidateRoles`, `WithManual`, `UserTask.Manual`, `NodeWire.Manual`, `nodeYAML.Manual`, `ErrManualTaskValidation` used identically across tasks. ✓

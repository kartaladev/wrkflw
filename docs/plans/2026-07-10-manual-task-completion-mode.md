# Manual UserTask Completion Mode + Payload Enforcement — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give a manual `UserTask` two completion modes via `WithManual(immediate bool)` — wait-for-trigger (default, now rejecting a non-empty completion payload) and immediate auto-complete (records a completed task for audit) — and revise ADR-0118 in place.

**Architecture:** `WithManual` gains a bool `immediate`; `UserTask` gains `ManualImmediate bool`. Wait mode is unchanged parking plus a new engine enforcement (`ErrManualTaskPayload`) in `handleHumanCompleted`. Immediate mode is a new branch in `userTaskStrategy.enter` that records a completed task and advances without parking. Both flags round-trip on JSON/YAML.

**Tech Stack:** Go 1.25; `definition/activity`, `definition/model`; engine (`engine`); runtime driver (`runtime`). Module `github.com/kartaladev/wrkflw`.

## Global Constraints

- Go 1.25; single module `github.com/kartaladev/wrkflw`; public packages at repo root (no `pkg/` prefix).
- **TDD strict:** every new symbol/behaviour gets a failing test FIRST, with a visible RED (`go test ./<pkg>/...`) before implementation. Pure refactors need no new test but existing tests pass before AND after.
- **Black-box tests:** `package <pkg>_test`.
- **Table tests:** when ≥2 cases exercise the same call, use the project `table-test` skill (an `assert func(...)` closure form, `t.Context()` over `context.Background()`).
- Error sentinels use the `workflow-<pkg>: ...` message prefix (here `workflow-engine: ...`).
- Pair each `.go` with a same-named `_test.go`.
- Library **unreleased** → breaking API changes acceptable; no wire aliases/migrators.
- Verify per touched package: `go test -race -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1` (≥85%), `go test ./...` from root, `golangci-lint run ./...` clean.
- Commit per task with Conventional Commits + the repo's `Co-Authored-By` + `Claude-Session` trailers.
- **Revise ADR-0118 in place** — NO new ADR (user directive).

---

## Task 1: `WithManual(immediate bool)` + `ManualImmediate` field, migrate call sites (breaking)

**Files:**
- Modify: `definition/activity/options.go` (the `manualOpt` + `WithManual`)
- Modify: `definition/activity/activity.go` (`UserTask` struct — add `ManualImmediate`)
- Modify (migration): every `WithManual()` call site → `WithManual(false)`. Sites: `definition/activity/options_test.go`, `definition/activity/activity_test.go`, `definition/model/validate_test.go`, `definition/model/yaml_test.go` (if it uses WithManual — it uses YAML text, so likely not), `runtime/manual_task_test.go`, `examples/scenarios/manual_task/main.go`. Find all: `rg -n "WithManual\(" --glob '*.go'`.
- Test: `definition/activity/options_test.go`.

**Interfaces:**
- Consumes: `UserTaskOption`, `UserTask`.
- Produces: `func WithManual(immediate bool) UserTaskOption`; `UserTask.ManualImmediate bool`.

- [ ] **Step 1: Write the failing test**

Replace `TestWithManual` in `definition/activity/options_test.go` with a table (two cases now exercise `WithManual`, so use the project table-test form — `assert` closure):

```go
func TestWithManual(t *testing.T) {
	cases := []struct {
		name      string
		immediate bool
		assert    func(t *testing.T, ut activity.UserTask)
	}{
		{
			name:      "wait mode",
			immediate: false,
			assert: func(t *testing.T, ut activity.UserTask) {
				if !ut.Manual {
					t.Fatal("Manual = false, want true")
				}
				if ut.ManualImmediate {
					t.Fatal("ManualImmediate = true, want false (wait mode)")
				}
			},
		},
		{
			name:      "immediate mode",
			immediate: true,
			assert: func(t *testing.T, ut activity.UserTask) {
				if !ut.Manual {
					t.Fatal("Manual = false, want true")
				}
				if !ut.ManualImmediate {
					t.Fatal("ManualImmediate = false, want true (immediate mode)")
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := activity.NewUserTask("confirm", activity.WithManual(tc.immediate))
			ut, ok := n.(activity.UserTask)
			if !ok {
				t.Fatalf("node is %T, want activity.UserTask", n)
			}
			if len(ut.EligibleRoles) != 0 {
				t.Fatalf("EligibleRoles = %v, want empty", ut.EligibleRoles)
			}
			tc.assert(t, ut)
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./definition/activity/... -run TestWithManual`
Expected: FAIL — compile error `not enough arguments in call to activity.WithManual` / `ut.ManualImmediate undefined`.

- [ ] **Step 3: Write minimal implementation**

In `definition/activity/activity.go`, add to the `UserTask` struct after `Manual bool`:

```go
	// ManualImmediate, meaningful only when Manual is true, selects the manual
	// completion mode: false (default) waits for a bare completion trigger; true
	// auto-completes the task on entry (a documentation marker) — the engine
	// records a completed task for audit and advances without waiting. See ADR-0118.
	ManualImmediate bool
```

In `definition/activity/options.go`, replace `manualOpt`/`WithManual`:

```go
type manualOpt struct{ immediate bool }

func (o manualOpt) applyUserTask(u *UserTask) {
	u.Manual = true
	u.ManualImmediate = o.immediate
}

// WithManual marks a UserTask as a manual task: a form-less human checkpoint.
// immediate selects the completion mode:
//   - false: the task parks and completes on a bare trigger; a non-empty
//     completion payload is rejected (engine.ErrManualTaskPayload).
//   - true:  the task auto-completes on entry (a documentation marker); the
//     engine records a completed task for audit and advances without waiting.
// A manual task must not carry completion validation (rejected at Build time,
// ErrManualTaskValidation), regardless of mode. See ADR-0118.
func WithManual(immediate bool) UserTaskOption { return manualOpt{immediate} }
```

- [ ] **Step 4: Migrate every `WithManual()` call site to `WithManual(false)`**

`rg -n "WithManual\(\)" --glob '*.go'` and change each to `WithManual(false)` (Edit tool; re-grep until zero `WithManual()` remain). Then `go build ./... && go vet ./...` clean.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./definition/activity/... -run TestWithManual` → PASS. Then `go test ./definition/... ./runtime/...` green.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "feat(definition)!: WithManual(immediate bool) + ManualImmediate field (ADR-0118)"
```

BREAKING body: `WithManual()` → `WithManual(immediate bool)`; immediate selects auto-complete vs wait.

---

## Task 2: JSON + YAML round-trip for `ManualImmediate` (ADR-0118)

**Files:**
- Modify: `definition/model/node_wire.go` (add `ManualImmediate` near `Manual`)
- Modify: `definition/activity/activity.go` (UserTask `FromWire`/`ToWire`)
- Modify: `definition/model/yaml.go` (`nodeYAML` + `fromNodeYAML`)
- Test: `definition/activity/activity_test.go` (JSON), `definition/model/yaml_test.go` (YAML)

**Interfaces:**
- Consumes: `UserTask.ManualImmediate` (Task 1), `NodeWire.Manual` (existing).
- Produces: `NodeWire.ManualImmediate bool` (`json:"manualImmediate,omitempty"`); `nodeYAML.ManualImmediate bool` (`yaml:"manualImmediate,omitempty"`).

- [ ] **Step 1: Write the failing tests**

Add to `definition/activity/activity_test.go` (mirror `TestUserTaskManualWireRoundTrip`):

```go
func TestUserTaskManualImmediateWireRoundTrip(t *testing.T) {
	def := &model.ProcessDefinition{
		ID: "d", Version: 1,
		Nodes: []model.Node{activity.NewUserTask("confirm", activity.WithManual(true))},
	}
	data, err := json.Marshal(def)
	if err != nil {
		t.Fatal(err)
	}
	var got model.ProcessDefinition
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	ut := got.Nodes[0].(activity.UserTask)
	if !ut.Manual || !ut.ManualImmediate {
		t.Fatalf("Manual=%v ManualImmediate=%v, want both true", ut.Manual, ut.ManualImmediate)
	}
}
```

Add to `definition/model/yaml_test.go` (mirror `TestParseYAMLUserTaskManual`, assert via `activity.UserTask` cast):

```go
func TestParseYAMLUserTaskManualImmediate(t *testing.T) {
	const src = `
id: d
version: 1
nodes:
  - {id: s, kind: startEvent}
  - {id: confirm, kind: userTask, manual: true, manualImmediate: true}
  - {id: e, kind: endEvent}
flows:
  - {id: f1, source: s, target: confirm}
  - {id: f2, source: confirm, target: e}
`
	loader, err := model.ParseYAML(strings.NewReader(src))
	if err != nil {
		t.Fatalf("ParseYAML: %v", err)
	}
	parsed, err := loader.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	n, ok := parsed.Node("confirm")
	if !ok {
		t.Fatal("node confirm not found")
	}
	ut := n.(activity.UserTask)
	if !ut.Manual || !ut.ManualImmediate {
		t.Fatalf("Manual=%v ManualImmediate=%v, want both true", ut.Manual, ut.ManualImmediate)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./definition/activity/... -run TestUserTaskManualImmediateWireRoundTrip` and `go test ./definition/model/... -run TestParseYAMLUserTaskManualImmediate`
Expected: FAIL — `ManualImmediate` not carried.

- [ ] **Step 3: Write minimal implementation**

`definition/model/node_wire.go`, after the `Manual` field:

```go
	ManualImmediate bool `json:"manualImmediate,omitempty"`
```

`definition/activity/activity.go` KindUserTask spec — `FromWire` struct literal add `ManualImmediate: w.ManualImmediate`; `ToWire` add `w.ManualImmediate = v.ManualImmediate` next to `w.Manual = v.Manual`.

`definition/model/yaml.go` — `nodeYAML` after `Manual`:

```go
	ManualImmediate bool `yaml:"manualImmediate,omitempty"`
```

and in `fromNodeYAML`'s `NodeWire{...}` literal, next to `Manual: ny.Manual,` add `ManualImmediate: ny.ManualImmediate,`.

- [ ] **Step 4: Run tests to verify they pass**

Run both `-run` commands from Step 2 → PASS.

- [ ] **Step 5: Commit**

```bash
git add definition/model/node_wire.go definition/activity/activity.go definition/activity/activity_test.go definition/model/yaml.go definition/model/yaml_test.go
git commit -m "feat(definition): round-trip UserTask ManualImmediate on JSON + YAML (ADR-0118)"
```

---

## Task 3: Wait-mode payload enforcement — `ErrManualTaskPayload` (ADR-0118)

**Files:**
- Modify: `engine/step_triggers.go` (`handleHumanCompleted` ~line 476; add sentinel near the engine's other sentinels)
- Test: `runtime/manual_task_test.go` (extend — a wait-mode manual task rejects a non-empty completion payload)

**Interfaces:**
- Consumes: `activity.UserTask.Manual`/`ManualImmediate`, `HumanCompleted.Output`, `def`/`humanTdef`.
- Produces: `var engine.ErrManualTaskPayload error`; `handleHumanCompleted` returns it for a wait-mode manual node completed with non-empty output.

- [ ] **Step 1: Write the failing test**

Add to `runtime/manual_task_test.go` (black-box `runtime_test`), a case where `svc.Complete` carries a non-empty output and `ApplyTrigger` (or Complete) surfaces `engine.ErrManualTaskPayload`:

```go
func TestManualWaitTaskRejectsPayload(t *testing.T) {
	ctx := t.Context()
	def, err := definition.NewBuilder("manual-payload", 1).
		Add(event.NewStart("s")).
		Add(activity.NewUserTask("confirm", activity.WithManual(false))).
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
	const id = "mp-1"
	parked, err := driver.Drive(ctx, def, id, nil)
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	var token string
	for i := range parked.Tasks {
		if parked.Tasks[i].IsOpen() {
			token = parked.Tasks[i].TaskToken
			break
		}
	}
	svc, err := task.NewTaskService(taskStore, authz.RoleAuthorizer{})
	if err != nil {
		t.Fatalf("svc: %v", err)
	}
	trg, err := svc.Complete(ctx, token, authz.Actor{ID: "operator"}, map[string]any{"note": "oops"})
	if err != nil {
		t.Fatalf("complete build: %v", err)
	}
	_, err = driver.ApplyTrigger(ctx, def, id, trg)
	if !errors.Is(err, engine.ErrManualTaskPayload) {
		t.Fatalf("err = %v, want ErrManualTaskPayload", err)
	}
}
```

> NOTE: if `svc.Complete` itself is where the error should surface, adapt — but per the spec the engine is the enforcement point, so the error comes from `ApplyTrigger`. Import `errors` and `engine`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./runtime/... -run TestManualWaitTaskRejectsPayload`
Expected: FAIL — no error / instance completes (payload accepted today).

- [ ] **Step 3: Write minimal implementation**

Add the sentinel to `engine`'s error block (near other engine sentinels; grep `Err.*= errors.New("workflow-engine`):

```go
// ErrManualTaskPayload is returned when a wait-mode manual UserTask is completed
// with a non-empty output. A manual task is a form-less checkpoint; supplying a
// payload is a caller error. Immediate-mode manual tasks never take a trigger.
// See ADR-0118.
var ErrManualTaskPayload = errors.New("workflow-engine: manual user task cannot carry a completion payload")
```

In `handleHumanCompleted` (engine/step_triggers.go), AFTER `humanTdef` is resolved and BEFORE `mergeVars(s, t.Output)` (note: `mergeVars` is earlier — move the check before it; re-read the function to place the guard before any output is applied). Look up the node and reject:

```go
	if n, ok := humanTdef.Node(tok.NodeID); ok {
		if ut, ok := n.(activity.UserTask); ok && ut.Manual && !ut.ManualImmediate && len(t.Output) > 0 {
			return StepResult{}, fmt.Errorf("%w: node %q", ErrManualTaskPayload, tok.NodeID)
		}
	}
```

Place this guard immediately after `task := s.TaskByToken(...)` nil-check and BEFORE `mergeVars(s, t.Output)`. Confirm `engine` already imports `definition/activity` (step_nodes.go does); if step_triggers.go doesn't, add the import. Confirm `humanTdef.Node(id)` returns `(model.Node, bool)` (it's `*model.ProcessDefinition.Node`).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -race ./runtime/... -run 'TestManual'` → all PASS (existing bare-completion lock test still green; new payload-rejection green).

- [ ] **Step 5: Commit**

```bash
git add engine/step_triggers.go engine/errors.go runtime/manual_task_test.go
git commit -m "feat(engine): reject completion payload on wait-mode manual task (ADR-0118)"
```

(Adjust the sentinel's file to wherever engine sentinels live.)

---

## Task 4: Immediate-mode auto-complete in `userTaskStrategy.enter` (ADR-0118)

**Files:**
- Modify: `engine/step_nodes.go` (`userTaskStrategy.enter` ~line 528)
- Test: `runtime/manual_task_test.go` (immediate manual drives to Completed with no trigger + a completed task in history)

**Interfaces:**
- Consumes: `activity.UserTask.Manual`/`ManualImmediate`, `humantask.HumanTask`, `stepCtx`, token advance helpers (`c.s.moveAlongSingleFlow`, the enter return contract).
- Produces: engine behaviour — an immediate manual UserTask does not park; it records a `humantask.Completed` task and advances.

- [ ] **Step 1: Write the failing test**

Add to `runtime/manual_task_test.go`:

```go
func TestImmediateManualTaskAutoCompletes(t *testing.T) {
	ctx := t.Context()
	def, err := definition.NewBuilder("manual-immediate", 1).
		Add(event.NewStart("s")).
		Add(activity.NewUserTask("noted", activity.WithManual(true))).
		Add(event.NewEnd("e")).
		Connect("s", "noted").Connect("noted", "e").
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
	// No external trigger: driving alone must reach Completed.
	final, err := driver.Drive(ctx, def, "mi-1", nil)
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	if final.Status != engine.StatusCompleted {
		t.Fatalf("status = %v, want Completed (immediate manual auto-completes)", final.Status)
	}
	// Audit: a completed task for the manual node exists in history.
	var found bool
	for i := range final.Tasks {
		if final.Tasks[i].NodeID == "noted" && final.Tasks[i].State == humantask.Completed {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("no completed task recorded for the immediate manual node")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./runtime/... -run TestImmediateManualTaskAutoCompletes`
Expected: FAIL — the instance parks at the manual node (Status Running) instead of completing; no completed task.

- [ ] **Step 3: Write minimal implementation**

In `userTaskStrategy.enter`, immediately after `ut, ok := node.(activity.UserTask)` succeeds and the `taskToken`/`spec`/`ht` are built (before the deadline/reminder/AwaitHuman parking block), add an immediate-mode short-circuit:

```go
	if ut.Manual && ut.ManualImmediate {
		// Immediate manual task: record a completed task for audit and advance
		// without parking. No eligibility check (no actor acts), no payload.
		ht.State = humantask.Completed
		c.s.Tasks = append(c.s.Tasks, ht)
		c.s.moveAlongSingleFlow(c.tdef, tok, c.at)
		tok.State = TokenActive
		return []Command{UpdateTask{Task: ht}}, false, nil
	}
```

> IMPLEMENTER NOTE: The enter contract's second return value is the "stopped" bool. Verify from the existing parked path (returns `false` after parking) and an advancing path how `drive` continues after enter — the immediate branch must leave the token ACTIVE and positioned on the outgoing flow so the outer `drive` loop advances it to the end event. Study `drive()` and `moveAlongSingleFlow` before finalizing the return. If `moveAlongSingleFlow` + `TokenActive` + returning `false` does not cause `drive` to continue, adjust to match how a pass-through node (e.g. a gateway) hands control back. The behavioural test in Step 1 is the exact spec — make it green with the smallest correct mechanism. If the audit-task must also reach the external task store, emit the appropriate command (mirror how AwaitHuman persists, but mark completed) — but InstanceState.Tasks is the audit trail the test asserts. Escalate (BLOCKED) if the correct mechanism needs an engine command that does not exist and its addition is non-trivial.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -race ./runtime/... -run TestImmediateManualTaskAutoCompletes` → PASS. Then `go test -race ./engine/... ./runtime/...` green (no regression to normal user tasks).

- [ ] **Step 5: Commit**

```bash
git add engine/step_nodes.go runtime/manual_task_test.go
git commit -m "feat(engine): immediate manual UserTask auto-completes with audit record (ADR-0118)"
```

---

## Task 5: Example — demonstrate both manual modes (ADR-0118)

**Files:**
- Modify: `examples/scenarios/manual_task/main.go`

**Interfaces:**
- Consumes: the public driver + task-service API (already used in this example).

- [ ] **Step 1: Extend the example**

Update `examples/scenarios/manual_task/main.go` so `WithManual()` calls become `WithManual(false)` for the existing wait-mode "hand over badge" step, and add a short second demonstration: an immediate-mode manual step (`activity.NewUserTask("recordOrientation", activity.WithManual(true))`) that auto-completes with no trigger, printing that it was recorded for audit. Keep it a single coherent reference-wiring `main` (no test helpers). If a single flow is cleaner, chain: `start → handOverBadge[wait] → recordOrientation[immediate] → end`, drive to the wait task, complete it bare, then observe the instance run to completion through the immediate node.

- [ ] **Step 2: Build and run**

Run: `go run ./examples/scenarios/manual_task`
Expected: prints the wait-mode completion line AND ends with instance completed (the immediate node auto-completed). Show the output. Delete any stray root binary; do not commit it.

- [ ] **Step 3: Commit**

```bash
git add examples/scenarios/manual_task/main.go
git commit -m "docs(examples): manual_task shows wait + immediate completion modes (ADR-0118)"
```

---

## Task 6: Revise ADR-0118 in place (no new ADR)

**Files:**
- Modify: `docs/adr/0118-manual-user-task.md`

- [ ] **Step 1: Amend ADR-0118**

Update the Decision and Consequences of `docs/adr/0118-manual-user-task.md`:
- Decision: `WithManual` takes `immediate bool`; two modes (wait / immediate auto-complete). Wait-mode rejects a non-empty completion payload with `ErrManualTaskPayload`. Immediate mode records a completed task for audit and advances on entry. `ManualImmediate` added to the JSON/YAML wire.
- Consequences: SUPERSEDE the prior "the engine is unchanged / manual adds no engine-observable branch" statement — enforcement (`ErrManualTaskPayload`) and immediate-mode auto-completion ARE engine behaviour now. Keep the deliberate-divergence-from-BPMN framing. Note that the earlier characterization ("a plain roleless UserTask would also complete on a bare trigger") no longer fully holds: wait-mode manual now additionally rejects payloads.
- Add a dated amendment note referencing `docs/specs/2026-07-10-manual-task-completion-mode-design.md`.

- [ ] **Step 2: Commit**

```bash
git add docs/adr/0118-manual-user-task.md
git commit -m "docs(adr): revise ADR-0118 — manual completion modes + payload enforcement"
```

---

## Verification Checklist

- [ ] `WithManual(immediate bool)` sets `Manual` + `ManualImmediate`; all `WithManual()` sites migrated (Task 1).
- [ ] `ManualImmediate` round-trips on JSON (Task 2) and YAML decode (Task 2).
- [ ] Wait-mode manual completion with non-empty output → `engine.ErrManualTaskPayload` (Task 3); bare completion still drives to Completed.
- [ ] Immediate-mode manual auto-completes with no trigger to `StatusCompleted`, with a completed task recorded for the node (Task 4).
- [ ] `go run ./examples/scenarios/manual_task` shows both modes (Task 5).
- [ ] ADR-0118 revised in place; no new ADR (Task 6).
- [ ] `go test -race ./...` green from root; touched packages ≥85% coverage; `golangci-lint run ./...` clean.

## Spec coverage self-check

- `WithManual(immediate bool)` + `ManualImmediate` → Task 1. ✓
- Wire/YAML round-trip → Task 2. ✓
- Wait-mode payload enforcement (`ErrManualTaskPayload`, engine) → Task 3. ✓
- Immediate-mode record-then-auto-complete → Task 4. ✓
- Example both modes → Task 5. ✓
- ADR-0118 revised in place → Task 6. ✓
- Build guard unchanged (manual + completion validation still rejected) — covered by existing Task-6/9034787 tests; no new task needed. ✓

# ProcessDriver.ReverseInstance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a public `ProcessDriver.ReverseInstance` facade that rolls a running instance back (compensate-all→resume-at-start, or compensate-back-to-a-target-node) without terminating it.

**Architecture:** A thin runtime facade over `ApplyTrigger` (like `processdriver_cancel.go`) plus one engine enhancement: full compensation can, when the trigger requests it, resume at the start node with reset variables instead of terminating. `WithTargetNode` reuses the existing partial-rollback path unchanged; only `WithFullReverse` needs new engine behavior. The cancel/error terminate path stays byte-for-byte unchanged (the new behavior is gated entirely on new trigger/cursor fields being set).

**Tech Stack:** Go 1.25; engine token state machine; `stretchr/testify` (black-box `engine_test` / `runtime_test`).

## Global Constraints

- **TDD strict (CLAUDE.md):** every new exported symbol / behavioral change is preceded by a *failing* `go test ./<pkg>/...` whose red output is visible in the transcript. No impl before red.
- **Go 1.25**; no new dependencies.
- **Error sentinels** use the `workflow-<pkg>:` prefix (facade errors `workflow-runtime:`, engine errors `workflow-engine:`).
- **Test file naming:** each `foo.go` pairs with `foo_test.go`; prefer black-box `<pkg>_test`.
- **Table tests** follow the project `table-test` skill (assert-closure form, `t.Context()`) when a test has ≥2 cases over the same call.
- **Coverage** ≥ 85% line for touched packages; `go build ./...`, `go test ./...`, `golangci-lint run ./...` clean before done.
- **Reverse never terminates.** Termination stays `CancelInstance`'s job. The cancel/error full-compensation terminate path in `stepCompensationFinish` must remain behaviorally identical.
- **ADR-0109.** Do NOT commit until the user approves (Git Discipline); spec + plan commit together first.

## File Structure

- `engine/state.go` — `InstanceState.StartVariables` field; `compensationCursor.ReverseNode` + `ReverseResetVars` fields.
- `engine/step_triggers.go` — capture `StartVariables` in `handleStartInstance`.
- `engine/trigger.go` — `CompensateRequested.ReverseNode` + `ResetVars` fields; `NewReverseToStart(at, startNode)` constructor (keep `NewCompensateRequested` for back-compat).
- `engine/step_compensation.go` — thread reverse intent (`stepCompensateRequested` → `beginCompensation` → cursor); reverse-resume branch in `stepCompensationFinish`.
- `runtime/processdriver_reverse.go` — `ReverseInstance`, `ReverseOption`, `WithFullReverse`, `WithTargetNode`.
- `examples/scenarios/reverse_rollback/main.go` — UserTask reject/re-escalate loop, reversed both ways.
- `docs/adr/0109-reverse-instance.md` — ADR.

---

## Task 1: `InstanceState.StartVariables` — snapshot start vars

**Files:**
- Modify: `engine/state.go` (`InstanceState` struct ~L403)
- Modify: `engine/step_triggers.go` (`handleStartInstance` ~L15)
- Test: `engine/start_variables_test.go` (new, `engine_test`)

**Interfaces:**
- Produces: `InstanceState.StartVariables map[string]any` — immutable copy of the variables the instance began with, captured once on `StartInstance`. Serializes automatically (InstanceState uses default JSON, no custom marshaler).

- [ ] **Step 1: Write the failing test**

```go
// engine/start_variables_test.go (package engine_test)
func TestStartInstance_CapturesStartVariables(t *testing.T) {
	def := &model.ProcessDefinition{
		ID: "p", Version: 1,
		Nodes: []model.Node{event.NewStart("start"), event.NewEnd("end")},
		Flows: []flow.SequenceFlow{{ID: "f1", Source: "start", Target: "end"}},
	}
	t0 := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)
	r, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(t0, map[string]any{"amount": 100}), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"amount": 100}, r.State.StartVariables)
	// Mutating live Variables must not change the snapshot.
	r.State.Variables["amount"] = 999
	assert.Equal(t, 100, r.State.StartVariables["amount"], "StartVariables must be an independent copy")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./engine/... -run TestStartInstance_CapturesStartVariables`
Expected: FAIL — `StartVariables` undefined on `InstanceState`.

- [ ] **Step 3: Implement**

In `engine/state.go`, add to `InstanceState` (after `Variables`):
```go
	// StartVariables is an immutable copy of the variables the instance began with,
	// captured once on StartInstance. Used by a full ReverseInstance to restore a
	// fresh slate when resuming at the start node.
	StartVariables map[string]any
```
In `engine/step_triggers.go` `handleStartInstance`, right after `mergeVars(s, t.Vars)`:
```go
	s.StartVariables = copyVars(s.Variables)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./engine/... -run TestStartInstance_CapturesStartVariables`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add engine/state.go engine/step_triggers.go engine/start_variables_test.go
git commit -m "feat(engine): capture InstanceState.StartVariables on start"
```

---

## Task 2: `CompensateRequested` reverse-to-start fields + constructor

**Files:**
- Modify: `engine/trigger.go` (`CompensateRequested` ~L240, `NewCompensateRequested` ~L249)
- Test: `engine/trigger_test.go`

**Interfaces:**
- Produces: `CompensateRequested.ReverseNode string`, `CompensateRequested.ResetVars bool`; `engine.NewReverseToStart(at time.Time, startNode string) CompensateRequested`. `NewCompensateRequested(at, toNode)` is UNCHANGED (back-compat; leaves the new fields zero).

- [ ] **Step 1: Write the failing test**

```go
// engine/trigger_test.go (package engine_test) — add:
func TestNewReverseToStart_SetsReverseFields(t *testing.T) {
	t0 := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)
	trg := engine.NewReverseToStart(t0, "start")
	assert.Equal(t, "start", trg.ReverseNode)
	assert.True(t, trg.ResetVars)
	assert.Equal(t, "", trg.ToNode, "reverse-to-start is a full walk: ToNode empty")
	// Back-compat: NewCompensateRequested leaves the reverse fields zero.
	c := engine.NewCompensateRequested(t0, "X")
	assert.Equal(t, "", c.ReverseNode)
	assert.False(t, c.ResetVars)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./engine/... -run TestNewReverseToStart_SetsReverseFields`
Expected: FAIL — `ReverseNode`/`ResetVars`/`NewReverseToStart` undefined.

- [ ] **Step 3: Implement**

In `engine/trigger.go`, extend `CompensateRequested`:
```go
type CompensateRequested struct {
	baseTrigger
	// ToNode is the rollback target node ID (exclusive). Empty means full rollback.
	ToNode string
	// ReverseNode, when non-empty on a FULL walk (ToNode==""), makes the walk resume at
	// this node with StatusRunning instead of terminating (ReverseInstance full reverse).
	// Empty for cancel/error/admin walks, which terminate on a full walk.
	ReverseNode string
	// ResetVars, when true, resets Variables to StartVariables on a ReverseNode resume.
	ResetVars bool
}
```
Add the constructor (leave `NewCompensateRequested` unchanged):
```go
// NewReverseToStart builds a CompensateRequested that compensates ALL records and,
// on finish, resumes at startNode (StatusRunning) with variables reset to
// StartVariables — the full-reverse form of ReverseInstance (does NOT terminate).
func NewReverseToStart(at time.Time, startNode string) CompensateRequested {
	return CompensateRequested{baseTrigger: baseTrigger{at: at}, ReverseNode: startNode, ResetVars: true}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./engine/... -run TestNewReverseToStart_SetsReverseFields`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add engine/trigger.go engine/trigger_test.go
git commit -m "feat(engine): CompensateRequested reverse-to-start fields + NewReverseToStart"
```

---

## Task 3: engine reverse-to-start finish behavior (the core)

**Files:**
- Modify: `engine/state.go` (`compensationCursor` ~L357 — add `ReverseNode`, `ReverseResetVars`)
- Modify: `engine/step_compensation.go` (`stepCompensateRequested` ~L44, `beginCompensation` ~L55, `stepCompensationFinish` ~L268)
- Test: `engine/reverse_instance_test.go` (new, `engine_test`)

**Interfaces:**
- Consumes: Task 1 `StartVariables`, Task 2 `CompensateRequested.ReverseNode`/`ResetVars`.
- Produces: full-compensation walk that resumes at `ReverseNode` (StatusRunning, vars reset) instead of terminating, when the trigger set `ReverseNode`.

**Design:** thread the trigger's `ReverseNode`/`ResetVars` onto the cursor (new fields `ReverseNode`, `ReverseResetVars`, kept DISTINCT from the throw-walk's `ResumeNode`/`ResumeScope` so the throw branch is not triggered). In `stepCompensationFinish`, the full-rollback branch (`toNode==""` && `resumeNode==""`) checks the cursor's `ReverseNode` FIRST: if set, clear the scope's compensation records (as full rollback does), place a token at `ReverseNode`, set `StatusRunning`, reset `Variables` to `copyVars(StartVariables)` when `ReverseResetVars`, and drive — instead of terminating. Terminate path otherwise unchanged.

- [ ] **Step 1: Write the failing test**

```go
// engine/reverse_instance_test.go (package engine_test)
// start → svc(compensable) → end ; drive to completion, then reverse-to-start.
func reverseSvcDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-rev", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewServiceTask("svc", activity.WithTaskAction("do"), activity.WithCompensateAction("undo")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "svc"},
			{ID: "f2", Source: "svc", Target: "end"},
		},
	}
}

func TestReverseToStart_ResumesAtStartWithResetVars(t *testing.T) {
	def := reverseSvcDef()
	t0 := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)
	// Start with amount=100; drive the service action to completion (records compensation).
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(t0, map[string]any{"amount": 100}), engine.StepOptions{})
	require.NoError(t, err)
	// Find the svc InvokeAction command id, complete it (mutating a var along the way).
	var cmdID string
	for _, c := range r1.Commands {
		if ia, ok := c.(engine.InvokeAction); ok && ia.Name == "do" {
			cmdID = ia.CommandID
		}
	}
	require.NotEmpty(t, cmdID)
	r2, err := engine.Step(def, r1.State, engine.NewActionCompleted(t0, cmdID, map[string]any{"amount": 500}), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r2.State.RootCompensations, 1, "svc must have recorded a compensation")

	// Reverse to start: expect the "undo" compensation to fire, then resume at start with reset vars.
	r3, err := engine.Step(def, r2.State, engine.NewReverseToStart(t0, "start"), engine.StepOptions{})
	require.NoError(t, err)
	var undoID string
	for _, c := range r3.Commands {
		if ia, ok := c.(engine.InvokeAction); ok && ia.Name == "undo" {
			undoID = ia.CommandID
		}
	}
	require.NotEmpty(t, undoID, "reverse must invoke the compensate action")
	r4, err := engine.Step(def, r3.State, engine.NewActionCompleted(t0, undoID, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusRunning, r4.State.Status, "reverse resumes Running, NOT terminated")
	assert.Equal(t, 100, r4.State.Variables["amount"], "vars reset to StartVariables")
	require.Len(t, r4.State.Tokens, 1)
	assert.Equal(t, "start", r4.State.Tokens[0].NodeID, "token resumes at the start node")
	assert.Empty(t, r4.State.RootCompensations, "records cleared after full reverse")
}

// Regression: the cancel/error terminate path (full compensation with NO ReverseNode) still terminates.
func TestFullCompensation_WithoutReverse_StillTerminates(t *testing.T) {
	def := reverseSvcDef()
	t0 := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)
	r1, _ := engine.Step(def, engine.InstanceState{InstanceID: "i1"}, engine.NewStartInstance(t0, map[string]any{"amount": 100}), engine.StepOptions{})
	var cmdID string
	for _, c := range r1.Commands {
		if ia, ok := c.(engine.InvokeAction); ok && ia.Name == "do" { cmdID = ia.CommandID }
	}
	r2, _ := engine.Step(def, r1.State, engine.NewActionCompleted(t0, cmdID, nil), engine.StepOptions{})
	r3, err := engine.Step(def, r2.State, engine.NewCompensateRequested(t0, ""), engine.StepOptions{}) // full, no reverse
	require.NoError(t, err)
	var undoID string
	for _, c := range r3.Commands {
		if ia, ok := c.(engine.InvokeAction); ok && ia.Name == "undo" { undoID = ia.CommandID }
	}
	r4, _ := engine.Step(def, r3.State, engine.NewActionCompleted(t0, undoID, nil), engine.StepOptions{})
	assert.Equal(t, engine.StatusTerminated, r4.State.Status, "full compensation without ReverseNode still terminates")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./engine/... -run 'TestReverseToStart_ResumesAtStartWithResetVars|TestFullCompensation_WithoutReverse_StillTerminates'`
Expected: `TestReverseToStart...` FAILS (reverse-to-start currently terminates + doesn't reset vars); the regression test PASSES already (guards you don't break it).

- [ ] **Step 3: Implement**

In `engine/state.go` `compensationCursor`, add (distinct from the throw `ResumeNode`/`ResumeScope`):
```go
	// ReverseNode, when non-empty, makes the FULL-rollback finish resume at this node
	// (StatusRunning) instead of terminating — ReverseInstance full reverse. Kept
	// distinct from ResumeNode so the throw-walk branch is not triggered.
	ReverseNode string
	// ReverseResetVars resets Variables to StartVariables on the ReverseNode resume.
	ReverseResetVars bool
```
In `engine/step_compensation.go` `stepCompensateRequested`, thread the trigger fields into `beginCompensation`. Add two params to `beginCompensation` (`reverseNode string, reverseResetVars bool`); all OTHER callers (cancel/error terminal paths — grep `beginCompensation(`) pass `"", false`:
```go
	return beginCompensation(def, s, t.ToNode, 0, "", t.OccurredAt(), mode, eval, t.ReverseNode, t.ResetVars)
```
In `beginCompensation`, when it sets the `compensationCursor`, stamp `cur.ReverseNode = reverseNode; cur.ReverseResetVars = reverseResetVars`. **Also handle the empty-records / last-record early-finish paths**: those call `stepCompensationFinish` before a cursor is set — so for a reverse with zero eligible records, the finish must still resume at start. Simplest: set the cursor's reverse fields BEFORE the early-finish calls too (or pass them through). Ensure a reverse-to-start with no compensation records still resumes at start (add an assertion to the test if you want, but at minimum don't terminate).
In `stepCompensationFinish`, capture `reverseNode := s.Compensating.ReverseNode; reverseResetVars := s.Compensating.ReverseResetVars` alongside the other cursor reads (BEFORE `s.Compensating = compensationCursor{}`). Then, at the TOP of the full-rollback branch (`toNode != ""` returns above; this branch is `resumeNode == "" && toNode == ""`), before the terminate logic:
```go
	if reverseNode != "" {
		// Full reverse: records are all compensated; clear them (like full rollback),
		// then resume at reverseNode with StatusRunning instead of terminating.
		if scopeID == "" {
			s.RootCompensations = nil
		} else if sc := s.scopeByID(scopeID); sc != nil {
			sc.Compensations = nil
		}
		s.Status = StatusRunning
		if reverseResetVars {
			s.Variables = copyVars(s.StartVariables)
		}
		s.placeToken(reverseNode, at)
		driveCmds, err := drive(def, s, at, mode, eval)
		if err != nil {
			return StepResult{}, err
		}
		return StepResult{State: *s, Commands: driveCmds}, nil
	}
	// ...existing terminate logic unchanged...
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./engine/... -run 'TestReverseToStart_ResumesAtStartWithResetVars|TestFullCompensation_WithoutReverse_StillTerminates'`
Expected: BOTH PASS. Then run the whole engine package to confirm no compensation/cancel regression: `go test ./engine/...`.

- [ ] **Step 5: Commit**

```bash
git add engine/state.go engine/step_compensation.go engine/reverse_instance_test.go
git commit -m "feat(engine): full-compensation reverse-to-start (resume at start, reset vars, no terminate)"
```

---

## Task 4: cycle-regression + Item-4 completion-action reversibility tests

**Files:**
- Test: `engine/reverse_instance_test.go` (extend)

**Interfaces:** consumes Tasks 1–3. No production code (pure test coverage of existing behavior + the Item-4 interaction).

- [ ] **Step 1: Write the failing tests**

Two tests:
1. **Cycle LIFO** — a definition with an exclusive-gateway reject/re-escalate loop over a compensable ServiceTask, driven so the node completes 3× (3 compensation records), then `NewReverseToStart` — assert exactly 3 `undo` InvokeActions fire newest-first (drive each ActionCompleted and count/order them), ending StatusRunning at start. Also assert the same 3-record LIFO for a `WithTargetNode`/`NewCompensateRequested(t0, "X")` partial reverse.
2. **Item-4 completion-action reversibility** — a UserTask with BOTH `WithCompletionAction("record")` and `WithCompensateAction("unrecord")`: drive Start → HumanCompleted → (completion action parks) → ActionCompleted (records compensation), then `NewReverseToStart` — assert `unrecord` fires and the instance resumes at start. Proves the completion-action round-trip creates a reversible compensation record.

(Study `engine/reminder_*` / `step_cancel_handlers_test.go` for the loop/gateway fixture idiom; study `engine/completion_action_test.go` for the UserTask+completion drive sequence and how to obtain the task token.)

- [ ] **Step 2: Run to verify** — the cycle test drives existing behavior (should pass once Task 3 lands; if the LIFO ordering assertion is wrong it fails — that's the point of writing it); the Item-4 test proves the interaction. Run:
`go test ./engine/... -run 'TestReverse.*Cycle|TestReverse.*CompletionAction'`

- [ ] **Step 3:** No production code expected — if a test reveals a real ordering/record bug, fix it in the engine (that's a genuine find). Otherwise these lock in behavior.

- [ ] **Step 4: Run** — all green.

- [ ] **Step 5: Commit**

```bash
git add engine/reverse_instance_test.go
git commit -m "test(engine): reverse cycle LIFO + completion-action reversibility regression"
```

---

## Task 5: `ReverseInstance` runtime facade

**Files:**
- Create: `runtime/processdriver_reverse.go`
- Test: `runtime/processdriver_reverse_test.go` (new, `runtime_test`)

**Interfaces:**
- Consumes: `engine.NewReverseToStart(at, startNode)`, `engine.NewCompensateRequested(at, toNode)`, `driver.ApplyTrigger`, `driver.clk.Now()`, `driver.store.Load`, `def.StartNodes()`.
- Produces:
```go
func (d *ProcessDriver) ReverseInstance(ctx context.Context, def *model.ProcessDefinition, instanceID string, opts ...ReverseOption) (engine.InstanceState, error)
type ReverseOption // opaque
func WithFullReverse() ReverseOption
func WithTargetNode(nodeID string) ReverseOption
```

- [ ] **Step 1: Write the failing tests** (table where ≥2 cases share the call — use the project `table-test` closure form)

Cases:
- default (no opt) → full reverse: end-to-end on a compensable ServiceTask def → StatusRunning, token at start, vars reset. (Load the driver's store to assert.)
- `WithFullReverse()` → same as default.
- `WithTargetNode("svc")` where svc has a record → resumes at svc (or the engine's existing target semantics), current vars kept.
- `WithFullReverse()` + `WithTargetNode("x")` together → `workflow-runtime:` mutual-exclusion error, no state change.
- def with 0 or 2 start events + `WithFullReverse()` → `workflow-runtime:` start-resolution error.
- reverse of a terminal instance → `workflow-runtime:` terminal error.
- unknown `WithTargetNode` node → surfaced engine "not found in scope records" error.

Study `runtime/processdriver_cancel_test.go` (or the message/signal facade tests) for the driver-construction + store-load idiom.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./runtime/... -run TestReverseInstance`
Expected: FAIL — `ReverseInstance`/`WithFullReverse`/`WithTargetNode` undefined.

- [ ] **Step 3: Implement** (`runtime/processdriver_reverse.go`)

```go
package runtime

import (
	"context"
	"fmt"

	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
)

// ReverseOption configures a ReverseInstance call. Construct with WithFullReverse
// or WithTargetNode; they are mutually exclusive.
type ReverseOption func(*reverseConfig)

type reverseConfig struct {
	full     bool
	target   string
	targeted bool
}

// WithFullReverse compensates ALL work (LIFO), resets variables to the start
// slate, and resumes the instance at its start node (Running). This is the
// default when no option is given.
func WithFullReverse() ReverseOption { return func(c *reverseConfig) { c.full = true } }

// WithTargetNode compensates back to nodeID (exclusive, LIFO) and resumes the
// instance at nodeID (Running), keeping the current variables.
func WithTargetNode(nodeID string) ReverseOption {
	return func(c *reverseConfig) { c.targeted = true; c.target = nodeID }
}

// ReverseInstance rolls a running instance back without terminating it. With no
// option (or WithFullReverse) it compensates everything and resumes fresh at the
// start node; with WithTargetNode it compensates back to a node and resumes there.
// Termination remains CancelInstance's job. Returns the reversed InstanceState.
func (d *ProcessDriver) ReverseInstance(ctx context.Context, def *model.ProcessDefinition, instanceID string, opts ...ReverseOption) (engine.InstanceState, error) {
	var cfg reverseConfig
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.full && cfg.targeted {
		return engine.InstanceState{}, fmt.Errorf("workflow-runtime: ReverseInstance: WithFullReverse and WithTargetNode are mutually exclusive")
	}
	// Terminal-instance guard: load current state and reject if terminal.
	st, _, err := d.store.Load(ctx, instanceID)
	if err != nil {
		return engine.InstanceState{}, err
	}
	if st.Status != engine.StatusRunning && st.Status != engine.StatusCompensating {
		return engine.InstanceState{}, fmt.Errorf("workflow-runtime: ReverseInstance: cannot reverse a terminal instance %q (status %v)", instanceID, st.Status)
	}
	if cfg.targeted {
		return d.ApplyTrigger(ctx, def, instanceID, engine.NewCompensateRequested(d.clk.Now(), cfg.target))
	}
	// Default / full reverse: resolve the single start node.
	starts := def.StartNodes()
	if len(starts) != 1 {
		return engine.InstanceState{}, fmt.Errorf("workflow-runtime: ReverseInstance: expected exactly one start event, got %d", len(starts))
	}
	return d.ApplyTrigger(ctx, def, instanceID, engine.NewReverseToStart(d.clk.Now(), starts[0].ID()))
}
```
(Confirm the exact `d.store.Load` return arity and `d.clk` field name against `processdriver_cancel.go` / `processdriver.go`; adjust if the store signature differs. Confirm `engine.StatusCompensating`/`StatusRunning` are the correct non-terminal statuses to allow — align the terminal guard with what `CancelInstance` treats as terminal.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./runtime/... -run TestReverseInstance`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add runtime/processdriver_reverse.go runtime/processdriver_reverse_test.go
git commit -m "feat(runtime): ProcessDriver.ReverseInstance facade (full + target reverse)"
```

---

## Task 6: `reverse_rollback` example — UserTask approval loop

**Files:**
- Create: `examples/scenarios/reverse_rollback/main.go`

**Interfaces:** consumes `ReverseInstance`, `WithFullReverse`, `WithTargetNode`, catalog actions.

- [ ] **Step 1: Write the example.** A reject/re-escalate approval loop built from **UserTasks**, each carrying BOTH a completion action (forward: e.g. "record-decision") and a compensate action (undo: e.g. "revert-decision") — required by the Item-4 Build guard `ErrCompensateActionWithoutForwardAction`. Drive the loop a couple of iterations, then demonstrate `ReverseInstance` both ways (`WithTargetNode` to a mid-loop node, and `WithFullReverse` to reset). Register the actions in a catalog by name (no inline actions — removed in Item 4). Study `examples/scenarios/compensation_saga/main.go` and `examples/scenarios/usertask_approval/main.go` for driver wiring and the human-task completion sequence. Per `examples-dir-purpose`: show engine mechanics, do NOT import `processtest`/test helpers.

- [ ] **Step 2: Build** — `go build ./examples/scenarios/reverse_rollback/...` clean.
- [ ] **Step 3: Run** — `go run ./examples/scenarios/reverse_rollback` prints the loop, the compensations firing on reverse, and the resumed state (both modes).
- [ ] **Step 4: Lint** — `golangci-lint run ./examples/scenarios/reverse_rollback/...` clean.
- [ ] **Step 5: Commit**

```bash
git add examples/scenarios/reverse_rollback/
git commit -m "docs(examples): reverse_rollback UserTask approval-loop scenario"
```

---

## Task 7: ADR-0109

**Files:**
- Create: `docs/adr/0109-reverse-instance.md`

- [ ] **Step 1:** Write ADR-0109 in the Nygard template (Status: Accepted, 2026-07-09 / Context / Decision / Consequences). Cover: the `ReverseInstance` facade + functional-options decision (and that it INTRODUCES the option pattern to the facade layer — no sibling uses it); default = full reverse; mutual exclusion; terminal-instance = clean error (and how it aligns/deviates from `CancelInstance`); the engine enhancement (`StartVariables` snapshot + `CompensateRequested.ReverseNode`/`ResetVars` + the reverse-resume finish branch, gated so the cancel/error terminate path is unchanged); the operation trio (Cancel=terminate, FullReverse=fresh-at-start, TargetReverse=at-X); and the Item-4 interaction (UserTask/ReceiveTask compensation flows through the completion-action round-trip; example/tests must pair compensate with a completion action). Reference the spec `docs/specs/2026-07-09-reverse-instance-design.md`.

- [ ] **Step 2: Commit**

```bash
git add docs/adr/0109-reverse-instance.md
git commit -m "docs(adr): ADR-0109 ProcessDriver.ReverseInstance"
```

---

## Verification checklist

- [ ] Each new symbol (`StartVariables`, `NewReverseToStart`, `CompensateRequested.ReverseNode`/`ResetVars`, `compensationCursor.ReverseNode`/`ReverseResetVars`, `ReverseInstance`, `WithFullReverse`, `WithTargetNode`) has an observable red state before implementation.
- [ ] Full reverse: resumes at start, StatusRunning (NOT terminated), vars reset to StartVariables, records cleared.
- [ ] **Regression:** cancel/error full-compensation terminate path unchanged (StatusTerminated) — `TestFullCompensation_WithoutReverse_StillTerminates` + existing cancel/compensation tests green.
- [ ] Target reverse resumes at X with current vars kept (existing partial path, unchanged).
- [ ] Cycle: 3× loop → 3 LIFO compensations for both modes.
- [ ] Item-4: UserTask with completion+compensate action is reversible.
- [ ] Facade: default=full, mutual-exclusion error, start-resolution error, terminal error, unknown-target error.
- [ ] `StartVariables` survives instance-state serialization round-trip (persisted + reloaded).
- [ ] Example builds, runs (`go run`), lint clean.
- [ ] `go build ./...`, `go test ./...`, `go test -race` on touched pkgs, `golangci-lint run ./...` all clean; touched pkgs ≥ 85%.

## Self-Review Notes (author)

- **Spec coverage:** facade → Task 5; engine `StartVariables` → Task 1; `CompensateRequested` extension → Task 2; reverse-resume finish → Task 3; cycle + Item-4 tests → Task 4; example → Task 6; ADR → Task 7. All spec sections mapped.
- **Key design decision (locked here):** reverse intent uses NEW `compensationCursor.ReverseNode`/`ReverseResetVars` fields kept DISTINCT from the throw-walk's `ResumeNode`/`ResumeScope`, so the existing throw-resume branch is never mis-triggered and the cancel/error terminate path is gated off entirely. This is the safest way to add resume-at-start without perturbing the three existing finish branches.
- **Open confirmations at execution:** exact `d.store.Load` arity + `d.clk` field name (from `processdriver_cancel.go`); which statuses count as "terminal" for the guard (align with CancelInstance); the empty-records early-finish path in `beginCompensation` must also honor the reverse intent (Task 3 Step 3 calls this out).
- **beginCompensation signature change** ripples to cancel/error callers (pass `"", false`) — compiler-guarded.

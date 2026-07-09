# Option-Consolidation & Completion-Action Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a node completion-action and consolidate the `activity`/`event` option surface into a coherent `WithXxxAction` family, a split deadline/wait-action model, unified event message/signal setters, and catalog-only (no inline) actions.

**Architecture:** Six ordered phases on one program branch. Phase A is the only *new behaviour* (completion-action, reusing the existing `InvokeAction`→`ActionCompleted` round-trip — no new token state). Phases B–F are hard renames / removals the Go compiler + existing tests enforce. `main` gates stay green at every phase boundary.

**Tech Stack:** Go 1.25; `stretchr/testify` (existing engine tests are black-box `engine_test`); `expr-lang/expr` (unchanged); `schedule.TriggerSpec` for triggers.

## Global Constraints

- **TDD strict (CLAUDE.md):** every new exported symbol and behavioural change is preceded by a *failing* `go test ./<pkg>/...` run whose output is visible in the transcript. No impl before red.
- **Go 1.25**; no new dependencies.
- **Error sentinels** use the `workflow-<pkg>: ...` prefix (e.g. `workflow-model: deadline trigger must be one-shot`).
- **Test file naming:** each `foo.go` pairs with `foo_test.go`; prefer black-box `<pkg>_test`.
- **Table tests** follow the project `table-test` skill (assert-closure form, `t.Context()`), invoked when a test has ≥2 cases over the same call.
- **Coverage** ≥ 85% line for touched packages; `go build ./...`, `go test ./...`, `golangci-lint run ./...` clean before a phase is done.
- **Wire/YAML key renames are breaking** (pre-1.0, accepted). CHANGELOG + migration note required (Task G1).
- **ADR-0114** written in Phase A (Task A6), amended if later phases surface decisions.
- **Do NOT commit** until the user approves (Git Discipline); the spec + this plan commit together first.

## File Structure (created / modified)

- `definition/model/node.go` — `ActivityFields`/`WaitFields` field set (add `CompletionAction`; rename `CompensationAction`→`CompensateAction`, `CancelHandler`→`CancelAction`, `ReminderEvery`/`ReminderAction`→`WaitEvery`/`WaitAction`; remove `TaskAction.Inline`).
- `definition/model/node_wire.go` — `NodeWire` keys + `PutActivity`/`Activity`/`PutWait`/`Wait`.
- `definition/model/yaml.go` — `nodeYAML` keys + `fromNodeYAML` copies.
- `definition/model/builder.go` — remove inline-vs-name conflict validation; add nothing else.
- `definition/activity/options.go` — the activity option renames/removals/additions.
- `definition/activity/activity.go` — remove inline plumbing on `ServiceTask`/`BusinessRuleTask`.
- `definition/event/options.go` — event waiter/message/signal consolidation + deadline split + fire-once.
- `definition/build/build.go` — deadline fire-once Build check (if Build validation is centralized here) OR per-option in the leaf packages (decide in Task C2 by where existing Build checks live).
- `engine/node_accessors.go` — add `completionActionOf`; rename `compensationActionOf`→ reads `CompensateAction`, `cancelHandlerOf`→ reads `CancelAction`, reminder accessor → wait.
- `engine/step_triggers.go` — completion-action branches in `handleHumanCompleted` + `handleMessageReceived`.
- `engine/command.go` — remove `InvokeAction.Inline`.
- `engine/main_action.go` — drop inline note/precedence.
- `runtime/resolve_action.go` — drop inline precedence (`cmd.Inline`).
- `processtest/harness.go` — migrate inline usage to catalog registration.
- `examples/scenarios/completion_action/` — new example (Task A5).
- `CHANGELOG.md` — breaking-change + migration note (Task G1).
- `docs/adr/0114-option-consolidation-and-completion-action.md` — ADR (Task A6).

---

## Phase A — Completion-action (new behaviour, purely additive)

### Task A1: `CompletionAction` field + wire + YAML round-trip

**Files:**
- Modify: `definition/model/node.go` (`ActivityFields` ~L68)
- Modify: `definition/model/node_wire.go` (`NodeWire` ~L35, `PutActivity` ~L67, `Activity` ~L75)
- Modify: `definition/model/yaml.go` (`nodeYAML` + `fromNodeYAML`)
- Test: `definition/model/node_wire_test.go`

**Interfaces:**
- Produces: `ActivityFields.CompletionAction string`; wire key `json:"completionAction"`; YAML key `yaml:"completionAction"`.

- [ ] **Step 1: Write the failing test** (JSON round-trip of `CompletionAction`)

```go
// definition/model/node_wire_test.go  (package model, white-box — matches existing file)
func TestNodeWire_CompletionActionRoundTrip(t *testing.T) {
	w := NodeWire{ID: "u1", Kind: KindUserTask, CompletionAction: "recordApproval"}
	got := w.Activity()
	if got.CompletionAction != "recordApproval" {
		t.Fatalf("Activity() dropped CompletionAction: %q", got.CompletionAction)
	}
	var back NodeWire
	back.PutActivity(got)
	if back.CompletionAction != "recordApproval" {
		t.Fatalf("PutActivity() dropped CompletionAction: %q", back.CompletionAction)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./definition/model/... -run TestNodeWire_CompletionActionRoundTrip`
Expected: FAIL — `CompletionAction` undefined on `NodeWire`/`ActivityFields`.

- [ ] **Step 3: Implement**

In `node.go`, add to `ActivityFields` (after `CancelHandler`):
```go
	// CompletionAction is the optional action.Action invoked when the node's
	// completion is triggered (human completion / message receive), before the
	// token advances. Its returned vars merge into the instance variables.
	CompletionAction string
```
In `node_wire.go` `NodeWire`, add field:
```go
	CompletionAction   string             `json:"completionAction,omitempty"`
```
In `PutActivity`, extend the assignment line:
```go
	w.CompensationAction, w.CancelHandler, w.CompletionAction = a.CompensationAction, a.CancelHandler, a.CompletionAction
```
In `Activity()`, add the field to the returned struct literal:
```go
	return ActivityFields{WaitFields: w.Wait(), RetryPolicy: w.RetryPolicy, RecoveryFlow: w.RecoveryFlow, CompensationAction: w.CompensationAction, CancelHandler: w.CancelHandler, CompletionAction: w.CompletionAction}
```
In `yaml.go`, add to `nodeYAML`: `CompletionAction string \`yaml:"completionAction,omitempty"\`` and, in `fromNodeYAML`, add `CompletionAction: ny.CompletionAction,` to the `NodeWire` literal.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./definition/model/... -run TestNodeWire_CompletionActionRoundTrip`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add definition/model/node.go definition/model/node_wire.go definition/model/yaml.go definition/model/node_wire_test.go
git commit -m "feat(model): add CompletionAction activity field + wire/yaml round-trip"
```

### Task A2: `activity.WithCompletionAction` option (UserTask + ReceiveTask)

**Files:**
- Modify: `definition/activity/options.go`
- Test: `definition/activity/options_test.go`

**Interfaces:**
- Produces: `activity.WithCompletionAction(name string) interface { UserTaskOption; ReceiveTaskOption }`.

- [ ] **Step 1: Write the failing test**

```go
// definition/activity/options_test.go  (package activity_test)
func TestWithCompletionAction_SetsFieldOnUserAndReceive(t *testing.T) {
	u := activity.NewUserTask("u1", []string{"r"}, activity.WithCompletionAction("recordApproval")).(activity.UserTask)
	assert.Equal(t, "recordApproval", u.CompletionAction)

	r := activity.NewReceiveTask("r1", "m", activity.WithCompletionAction("ackOrder")).(activity.ReceiveTask)
	assert.Equal(t, "ackOrder", r.CompletionAction)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./definition/activity/... -run TestWithCompletionAction_SetsFieldOnUserAndReceive`
Expected: FAIL — `WithCompletionAction` undefined.

- [ ] **Step 3: Implement** (mirror `reminderOpt`, the existing dual-kind pattern at `options.go:159`)

```go
type completionActionOpt struct{ action string }

func (o completionActionOpt) applyUserTask(u *UserTask)       { u.CompletionAction = o.action }
func (o completionActionOpt) applyReceiveTask(r *ReceiveTask) { r.CompletionAction = o.action }

// WithCompletionAction attaches a catalog action run when a UserTask or
// ReceiveTask completion is triggered (human completion / message receive),
// after the completion input is merged. Its returned vars merge into the
// instance variables. Failure is governed by the node's WithRetryPolicy /
// error boundary (same machinery as a ServiceTask action). Distinct from
// WithCompletionValidation, which gates the completion input; this runs after it.
func WithCompletionAction(name string) interface {
	UserTaskOption
	ReceiveTaskOption
} {
	return completionActionOpt{name}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./definition/activity/... -run TestWithCompletionAction_SetsFieldOnUserAndReceive`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add definition/activity/options.go definition/activity/options_test.go
git commit -m "feat(activity): add WithCompletionAction option (UserTask + ReceiveTask)"
```

### Task A3: engine `completionActionOf` + `handleHumanCompleted` completion branch

**Files:**
- Modify: `engine/node_accessors.go` (add `completionActionOf`)
- Modify: `engine/step_triggers.go` (`handleHumanCompleted` ~L437)
- Test: `engine/completion_action_test.go` (new, black-box `engine_test`)

**Interfaces:**
- Consumes: `s.nextCommandID()`, `InvokeAction{CommandID, Name, Input}`, `TokenWaitingCommand`, `copyVars`, `mergeVars`, `handleActionCompleted` (existing).
- Produces: `completionActionOf(model.Node) string`.

- [ ] **Step 1: Write the failing test** (UserTask completion action parks + emits InvokeAction, then ActionCompleted advances to completion, merging the action output)

```go
// engine/completion_action_test.go  (package engine_test)
func userTaskCompletionDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-uc", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewUserTask("u1", []string{"r"}, activity.WithCompletionAction("recordApproval")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "u1"},
			{ID: "f2", Source: "u1", Target: "end"},
		},
	}
}

func TestUserTaskCompletionAction_ParksThenAdvancesOnActionCompleted(t *testing.T) {
	def := userTaskCompletionDef()
	t0 := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)

	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(t0, nil), engine.StepOptions{})
	require.NoError(t, err)
	tok := r1.State.Tokens[0]
	require.Equal(t, "u1", tok.NodeID)
	taskToken := r1.State.Tasks[0].Token // task record created alongside the parked UserTask token

	// Complete the human task: expect an InvokeAction for the completion action,
	// and the instance NOT yet complete (token parked on the action).
	r2, err := engine.Step(def, r1.State,
		engine.NewHumanCompleted(t0, taskToken, map[string]any{"approved": true}, authz.Actor{ID: "alice"}),
		engine.StepOptions{})
	require.NoError(t, err)
	var cmdID string
	for _, c := range r2.Commands {
		if ia, ok := c.(engine.InvokeAction); ok && ia.Name == "recordApproval" {
			cmdID = ia.CommandID
		}
	}
	require.NotEmpty(t, cmdID, "completion should emit InvokeAction for recordApproval")
	assert.NotEqual(t, engine.StatusCompleted, r2.State.Status, "must not complete before the action returns")

	// Action returns → token advances to end → instance completes, action output merged.
	r3, err := engine.Step(def, r2.State,
		engine.NewActionCompleted(t0, cmdID, map[string]any{"recorded": true}), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, r3.State.Status)
	assert.Equal(t, true, r3.State.Variables["recorded"])
	assert.Equal(t, true, r3.State.Variables["approved"])
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./engine/... -run TestUserTaskCompletionAction_ParksThenAdvancesOnActionCompleted`
Expected: FAIL — instance completes immediately (no completion branch); no `InvokeAction` for `recordApproval`.

- [ ] **Step 3: Implement**

In `node_accessors.go`, add (only the two completion-triggered kinds carry it):
```go
// completionActionOf returns the CompletionAction of a completion-triggered
// activity node (UserTask, ReceiveTask), or "".
func completionActionOf(n model.Node) string {
	switch v := n.(type) {
	case activity.UserTask:
		return v.CompletionAction
	case activity.ReceiveTask:
		return v.CompletionAction
	default:
		return ""
	}
}
```

In `handleHumanCompleted` (`step_triggers.go`), replace the tail from `s.moveAlongSingleFlow(humanTdef, tok, ...)` onward. Keep task completion + timer/boundary cancellation, but branch on the completion action **before** advancing:

```go
	task.State = humantask.Completed
	tok.State = TokenActive
	tok.AwaitCommand = ""
	humanTdef, humanTdefErr := defForScope(def, s, tok.ScopeID)
	if humanTdefErr != nil {
		return StepResult{}, humanTdefErr
	}
	cmds := []Command{UpdateTask{Task: *task}}
	for _, timerID := range s.cancelTimersByTaskToken(t.TaskToken, "") {
		cmds = append(cmds, CancelTimer{TimerID: timerID})
	}
	for _, timerID := range s.removeBoundaryArmsForHost(tok.ID) {
		cmds = append(cmds, CancelTimer{TimerID: timerID})
	}
	// Completion action: park on the action round-trip instead of advancing now.
	// handleActionCompleted resumes: it merges the action output and advances the
	// token along u1's single outgoing flow (the token is still AT u1).
	if node, ok := humanTdef.Node(tok.NodeID); ok {
		if ca := completionActionOf(node); ca != "" {
			cmdID := s.nextCommandID()
			cmds = append(cmds, InvokeAction{CommandID: cmdID, Name: ca, Input: copyVars(s.Variables)})
			tok.State = TokenWaitingCommand
			tok.AwaitCommand = cmdID
			return StepResult{State: *s, Commands: cmds}, nil
		}
	}
	// No completion action: advance + drive as before.
	s.moveAlongSingleFlow(humanTdef, tok, t.OccurredAt())
	driveCmds, err := drive(def, s, t.OccurredAt(), opt.Mode, resolveEvaluator(opt))
	if err != nil {
		return StepResult{}, err
	}
	cmds = append(cmds, driveCmds...)
	return StepResult{State: *s, Commands: cmds}, nil
```

> Note: `handleActionCompleted` records the node's `CompensateAction` (if any) at action-completed time and merges the action output — this is the intended reuse. Do not special-case it.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./engine/... -run TestUserTaskCompletionAction_ParksThenAdvancesOnActionCompleted`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add engine/node_accessors.go engine/step_triggers.go engine/completion_action_test.go
git commit -m "feat(engine): UserTask completion-action via action round-trip"
```

### Task A4: `handleMessageReceived` completion branch + failure-path test

**Files:**
- Modify: `engine/step_triggers.go` (`handleMessageReceived` standalone-token tail ~L711)
- Test: `engine/completion_action_test.go`

**Interfaces:**
- Consumes: `completionActionOf` (Task A3).

- [ ] **Step 1: Write the failing tests** (ReceiveTask completion action; and completion-action failure raises an incident when no retry/boundary)

```go
func receiveCompletionDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p-rc", Version: 1,
		Nodes: []model.Node{
			event.NewStart("start"),
			activity.NewReceiveTask("r1", "m", activity.WithCompletionAction("ackOrder")),
			event.NewEnd("end"),
		},
		Flows: []flow.SequenceFlow{
			{ID: "f1", Source: "start", Target: "r1"},
			{ID: "f2", Source: "r1", Target: "end"},
		},
	}
}

func TestReceiveTaskCompletionAction_ParksThenAdvances(t *testing.T) {
	def := receiveCompletionDef()
	t0 := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)
	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(t0, nil), engine.StepOptions{})
	require.NoError(t, err)
	r2, err := engine.Step(def, r1.State, engine.NewMessageReceived(t0, "m", "", map[string]any{"orderID": "o1"}), engine.StepOptions{})
	require.NoError(t, err)
	var cmdID string
	for _, c := range r2.Commands {
		if ia, ok := c.(engine.InvokeAction); ok && ia.Name == "ackOrder" {
			cmdID = ia.CommandID
		}
	}
	require.NotEmpty(t, cmdID)
	assert.NotEqual(t, engine.StatusCompleted, r2.State.Status)
	r3, err := engine.Step(def, r2.State, engine.NewActionCompleted(t0, cmdID, map[string]any{"acked": true}), engine.StepOptions{})
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, r3.State.Status)
	assert.Equal(t, true, r3.State.Variables["acked"])
}

func TestCompletionAction_FailureRaisesIncidentWhenNoRetryOrBoundary(t *testing.T) {
	def := receiveCompletionDef()
	t0 := time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)
	r1, _ := engine.Step(def, engine.InstanceState{InstanceID: "i1"}, engine.NewStartInstance(t0, nil), engine.StepOptions{})
	r2, _ := engine.Step(def, r1.State, engine.NewMessageReceived(t0, "m", "", nil), engine.StepOptions{})
	var cmdID string
	for _, c := range r2.Commands {
		if ia, ok := c.(engine.InvokeAction); ok && ia.Name == "ackOrder" {
			cmdID = ia.CommandID
		}
	}
	require.NotEmpty(t, cmdID)
	r3, err := engine.Step(def, r2.State,
		engine.NewActionFailed(t0, cmdID, "boom", false), engine.StepOptions{}) // non-retryable
	require.NoError(t, err)
	assert.Len(t, r3.State.Incidents, 1, "terminal completion-action failure raises an incident")
}
```

> If `engine.NewActionFailed`'s signature differs, adapt to the existing constructor (see `engine/trigger.go`); the assertion — one incident after a non-retryable failure — is the contract.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./engine/... -run 'TestReceiveTaskCompletionAction_ParksThenAdvances|TestCompletionAction_FailureRaisesIncidentWhenNoRetryOrBoundary'`
Expected: FAIL — ReceiveTask completes immediately; no `ackOrder` InvokeAction.

- [ ] **Step 3: Implement**

In `handleMessageReceived`, in the standalone-token branch (after `mergeVars(s, t.Payload)` and the `preCmds` timer/boundary cancellations, before `s.moveAlongSingleFlow(msgTdef, ...)`):
```go
	msgTdef, msgTdefErr := defForScope(def, s, tok.ScopeID)
	if msgTdefErr != nil {
		return StepResult{}, msgTdefErr
	}
	if node, ok := msgTdef.Node(tok.NodeID); ok {
		if ca := completionActionOf(node); ca != "" {
			cmdID := s.nextCommandID()
			preCmds = append(preCmds, InvokeAction{CommandID: cmdID, Name: ca, Input: copyVars(s.Variables)})
			tok.State = TokenWaitingCommand
			tok.AwaitCommand = cmdID
			return StepResult{State: *s, Commands: preCmds}, nil
		}
	}
	s.moveAlongSingleFlow(msgTdef, tok, t.OccurredAt())
```
(The failure-path test passes with no extra code: the parked token is an ordinary action-awaiting token, so the existing `ActionFailed` handler applies retry/incident/boundary precedence.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./engine/... -run 'TestReceiveTaskCompletionAction_ParksThenAdvances|TestCompletionAction_FailureRaisesIncidentWhenNoRetryOrBoundary'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add engine/step_triggers.go engine/completion_action_test.go
git commit -m "feat(engine): ReceiveTask completion-action + failure reuses incident/retry machinery"
```

### Task A5: `examples/scenarios/completion_action/` reference example

**Files:**
- Create: `examples/scenarios/completion_action/main.go`

**Interfaces:**
- Consumes: `activity.WithCompletionAction`, a registered catalog action.

- [ ] **Step 1: Write the example** (per `examples-dir-purpose` memory: illustrate the engine mechanic, not testing helpers). A UserTask whose completion action records a "domain record" from the completion input, using a named catalog action. Follow the structure of a sibling scenario (e.g. `examples/scenarios/usertask_approval/main.go`) for driver wiring.

- [ ] **Step 2: Build it**

Run: `go build ./examples/scenarios/completion_action/...`
Expected: builds clean.

- [ ] **Step 3: Run it (smoke)**

Run: `go run ./examples/scenarios/completion_action`
Expected: prints the completion-action side effect (domain record updated) and instance completion.

- [ ] **Step 4: Commit**

```bash
git add examples/scenarios/completion_action/
git commit -m "docs(examples): completion_action scenario"
```

### Task A6: ADR-0114

**Files:**
- Create: `docs/adr/0114-option-consolidation-and-completion-action.md`

- [ ] **Step 1:** Write ADR-0114 in the Nygard template (Status/Date, Context, Decision, Consequences) covering: the `WithXxxAction` family rule; deadline split + fire-once; wait-action generalization + field rename; event message/signal multi-kind consolidation; inline-action removal (full-serializability rationale); completion-action mechanics (reuse of the round-trip; failure via existing retry/incident/boundary). Reference the spec.

- [ ] **Step 2: Commit**

```bash
git add docs/adr/0114-option-consolidation-and-completion-action.md
git commit -m "docs(adr): ADR-0114 option consolidation + completion-action"
```

---

## Phase B — `WithXxxAction` family renames

Hard renames. The compiler + existing tests are the safety net: after each rename, the build breaks at every call site; fix them all, then green.

### Task B1: `WithCompensation`→`WithCompensateAction` (option + field + wire/yaml)

**Files:** `definition/activity/options.go`, `definition/model/node.go`, `definition/model/node_wire.go`, `definition/model/yaml.go`, `engine/node_accessors.go` (+ every call site).

- [ ] **Step 1: Red** — rename in a test first. Pick one existing test using `WithCompensation` (e.g. in `engine/` compensation tests) and change it to `WithCompensateAction`; run it.

Run: `go build ./... 2>&1 | head` → Expected: FAIL (`WithCompensateAction` undefined / `WithCompensation` still referenced elsewhere). This is the red state.

- [ ] **Step 2: Green** — apply the full rename:
  - `options.go`: `func WithCompensation` → `func WithCompensateAction`; body sets `a.CompensateAction`.
  - `node.go`: field `CompensationAction` → `CompensateAction` (update the doc comment).
  - `node_wire.go`: `NodeWire.CompensationAction` → `CompensateAction`, tag `json:"compensationAction"` → `json:"compensateAction"`; update `PutActivity`/`Activity`.
  - `yaml.go`: `nodeYAML.CompensationAction` → `CompensateAction`, tag → `compensateAction`; `fromNodeYAML` copy line.
  - `engine/node_accessors.go`: `compensationActionOf` reads `v.CompensateAction` (keep the func name or rename to `compensateActionOf` — rename for consistency and update its 2 call sites in `step_triggers.go`/`step_compensation.go`).
  - Fix all remaining call sites: `rg -l 'WithCompensation\b|CompensationAction'`.

- [ ] **Step 3: Verify green + no stragglers**

Run: `rg -n 'WithCompensation\b|CompensationAction|compensationAction' -g '!docs/**'` → Expected: no matches.
Run: `go build ./... && go test ./definition/... ./engine/...` → Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git commit -am "refactor(activity): rename WithCompensation→WithCompensateAction (field+wire+yaml)"
```

### Task B2: `WithCancelHandler`→`WithCancelAction` (option + field + wire/yaml)

**Files:** same set as B1, for the cancel field.

- [ ] **Step 1: Red** — rename `WithCancelHandler` in one existing cancel-handler test; run build → FAIL.
- [ ] **Step 2: Green** — rename:
  - `options.go`: `WithCancelHandler` → `WithCancelAction`; sets `a.CancelAction`.
  - `node.go`: `CancelHandler` → `CancelAction`.
  - `node_wire.go`: field + tag `cancelHandler` → `cancelAction`; `PutActivity`/`Activity`.
  - `yaml.go`: field + tag + copy.
  - `engine/node_accessors.go`: `cancelHandlerOf` reads `v.CancelAction` (rename func → `cancelActionOf`, update its call site in `step_triggers.go` `handleCancelRequested` ~L112).
- [ ] **Step 3: Verify** — `rg -n 'WithCancelHandler|CancelHandler|cancelHandler' -g '!docs/**'` → no matches; `go build ./... && go test ./definition/... ./engine/...` → PASS.
- [ ] **Step 4: Commit** — `git commit -am "refactor(activity): rename WithCancelHandler→WithCancelAction (field+wire+yaml)"`

### Task B3: `WithActionName`→`WithTaskAction`

**Files:** `definition/activity/options.go` (+ all call sites; examples/tests use it heavily).

- [ ] **Step 1: Red** — rename `WithActionName` in one existing test (e.g. `engine/receive_task_test.go:39` uses `WithActionName("esc")`); build → FAIL.
- [ ] **Step 2: Green** — `func WithActionName` → `func WithTaskAction` (body/return unchanged: sets `.Action`). Fix all call sites: `rg -l 'WithActionName\b'` (includes `examples/`, `processtest/`, tests).
- [ ] **Step 3: Verify** — `rg -n 'WithActionName\b' -g '!docs/**'` → no matches; `go build ./... && go test ./...` → PASS.
- [ ] **Step 4: Commit** — `git commit -am "refactor(activity): rename WithActionName→WithTaskAction"`

---

## Phase C — Deadline split + fire-once enforcement

### Task C1: split `WithDeadline`/`WithCatchDeadline` into `WithWaitDeadline` + `WithDeadlineAction`

**Files:** `definition/activity/options.go`, `definition/event/options.go` (+ call sites), tests in both packages.

**Interfaces:**
- Produces: `activity.WithWaitDeadline(t schedule.TriggerSpec, flow string) activityOnlyOption`; `activity.WithDeadlineAction(action string) activityOnlyOption`; `event.WithWaitDeadline(t, flow) CatchOption`; `event.WithDeadlineAction(action) CatchOption`.

- [ ] **Step 1: Red** — test the new split shape:

```go
// definition/activity/options_test.go
func TestWithWaitDeadline_And_WithDeadlineAction(t *testing.T) {
	st := activity.NewUserTask("u1", []string{"r"},
		activity.WithWaitDeadline(schedule.AfterDuration(72*time.Hour), "escalate"),
		activity.WithDeadlineAction("notify"),
	).(activity.UserTask)
	assert.Equal(t, "escalate", st.DeadlineFlow)
	assert.Equal(t, "notify", st.DeadlineAction)
	assert.False(t, st.DeadlineTimer.IsZero())
}
```

- [ ] **Step 2: Run → FAIL** (`WithWaitDeadline`/`WithDeadlineAction` undefined).

Run: `go test ./definition/activity/... -run TestWithWaitDeadline_And_WithDeadlineAction`

- [ ] **Step 3: Green** — in `activity/options.go` replace `WithDeadline`:
```go
// WithWaitDeadline sets the one-shot deadline trigger and the breach flow on an
// activity node. The trigger MUST be one-shot (schedule.AfterDuration/At/AfterExpr);
// a recurring trigger is a Build error (see build validation).
func WithWaitDeadline(t schedule.TriggerSpec, flow string) activityOnlyOption {
	return withActivity(func(a *model.ActivityFields) { a.DeadlineTimer, a.DeadlineFlow = t, flow })
}

// WithDeadlineAction sets the optional action run on deadline breach.
func WithDeadlineAction(action string) activityOnlyOption {
	return withActivity(func(a *model.ActivityFields) { a.DeadlineAction = action })
}
```
Mirror in `event/options.go` replacing `WithCatchDeadline` (set fields on `*IntermediateCatchEvent`). Fix all call sites: `rg -l 'WithDeadline\b|WithCatchDeadline\b'`.

- [ ] **Step 4: Run → PASS**; `go build ./... && go test ./definition/... ./engine/...`.
- [ ] **Step 5: Commit** — `git commit -am "refactor(options): split deadline into WithWaitDeadline + WithDeadlineAction (activity+event)"`

### Task C2: fire-once Build enforcement

**Files:** wherever activity/event Build validation lives (`definition/build/build.go` or `definition/model/builder.go` — check where existing per-node Build checks are; add there), tests.

**Interfaces:**
- Produces: sentinel `ErrDeadlineTriggerRecurring` (prefix `workflow-...: deadline trigger must be one-shot`).

- [ ] **Step 1: Red** — build a definition whose `WithWaitDeadline` uses `schedule.Every(...)` and assert Build errors:

```go
func TestBuild_RejectsRecurringDeadline(t *testing.T) {
	// Construct a definition with a UserTask carrying a recurring deadline trigger,
	// run it through the Builder/Loader Build path, assert errors.Is(err, ErrDeadlineTriggerRecurring).
}
```

- [ ] **Step 2: Run → FAIL** (no such validation / sentinel).
- [ ] **Step 3: Green** — add a validation pass over nodes with a non-zero `DeadlineTimer`: if `DeadlineTimer.Recurring()` return `ErrDeadlineTriggerRecurring`. Define the sentinel with the `workflow-<pkg>:` prefix.
- [ ] **Step 4: Run → PASS**; full build/test.
- [ ] **Step 5: Commit** — `git commit -am "feat(build): reject recurring WithWaitDeadline trigger (ErrDeadlineTriggerRecurring)"`

---

## Phase D — Wait-action rename + field rename

### Task D1: rename `ReminderEvery`/`ReminderAction` fields → `WaitEvery`/`WaitAction` (model + wire + engine)

**Files:** `definition/model/node.go` (`WaitFields`, `reminder()` carrier), `definition/model/node_wire.go` (`Wait`/`PutWait` + keys), `definition/model/yaml.go`, engine/runtime accessors + `armWaitReminder` and any `ReminderOf`/`.ReminderEvery`/`.ReminderAction` reader.

- [ ] **Step 1: Red** — rename the fields in `WaitFields` and run `go build ./...` → FAIL at every reader. The compile errors enumerate the call sites.
- [ ] **Step 2: Green** — apply:
  - `node.go`: `ReminderEvery`→`WaitEvery`, `ReminderAction`→`WaitAction`; method `reminder()`→`waitAction()`; update `ReminderOf` accessor name if present (→ `WaitActionOf`).
  - `node_wire.go`: `Wait`/`PutWait` field names + keys `reminderTrigger`→`waitTrigger`, `reminderAction`→`waitAction`; legacy flat `reminderEvery`→`waitEvery` (or drop the legacy flat form — **decision:** drop it, since wire is already breaking and the legacy flat form is a pre-nested-trigger BC shim; note the drop in the CHANGELOG).
  - `yaml.go`: keys.
  - engine/runtime: `armWaitReminder` and every `.ReminderEvery`/`.ReminderAction`/`ReminderOf` reader → the new names. `rg -l 'Reminder'` to enumerate.
- [ ] **Step 3: Verify** — `rg -n 'ReminderEvery|ReminderAction|reminderTrigger|reminderAction|ReminderOf' -g '!docs/**'` → no matches (WithWaitReminder option handled in D2); `go build ./... && go test ./definition/... ./engine/... ./runtime/...` → PASS.
- [ ] **Step 4: Commit** — `git commit -am "refactor(model): rename Reminder{Every,Action} fields → Wait{Every,Action} (+wire/yaml)"`

### Task D2: rename options `WithWaitReminder`/`WithCatchWaitReminder` → `WithWaitAction`

**Files:** `definition/activity/options.go`, `definition/event/options.go` (+ call sites).

- [ ] **Step 1: Red** — rename in one existing reminder test (e.g. `engine/reminder_receive_test.go`); build → FAIL.
- [ ] **Step 2: Green** — `activity.WithWaitReminder` → `activity.WithWaitAction` (keep dual-kind `interface { UserTaskOption; ReceiveTaskOption }`, set `WaitEvery`/`WaitAction`); `event.WithCatchWaitReminder` → `event.WithWaitAction` (`CatchOption`, set `WaitEvery`/`WaitAction`). Fix call sites: `rg -l 'WithWaitReminder|WithCatchWaitReminder'`.
- [ ] **Step 3: Verify** — `rg -n 'WithWaitReminder|WithCatchWaitReminder' -g '!docs/**'` → no matches; `go build ./... && go test ./...` → PASS.
- [ ] **Step 4: Commit** — `git commit -am "refactor(options): rename Wait-reminder options → WithWaitAction (activity+event)"`

---

## Phase E — Event message/signal consolidation

### Task E1: unify message setters → `WithMessageCorrelator` (multi-kind)

**Files:** `definition/event/options.go` (+ call sites), test.

**Interfaces:**
- Produces: `event.WithMessageCorrelator(msg, key string) interface { StartOption; CatchOption; BoundaryOption }`.

- [ ] **Step 1: Red** — test the multi-kind option sets fields on all three kinds:

```go
func TestWithMessageCorrelator_AllKinds(t *testing.T) {
	c := event.NewIntermediateCatch("c", event.WithMessageCorrelator("m", "k")).(event.IntermediateCatchEvent)
	assert.Equal(t, "m", c.MessageName); assert.Equal(t, "k", c.CorrelationKey)
	// Repeat for Start (EventSubProcess message-start) and Boundary using their constructors.
}
```
(Use the actual event constructors — check `event` package for `NewIntermediateCatch`/start/boundary constructor names.)

- [ ] **Step 2: Run → FAIL** (`WithMessageCorrelator` undefined).
- [ ] **Step 3: Green** — replace `WithStartMessage`/`WithCatchMessage`/`WithBoundaryMessage` with one multi-kind option (mirror the existing multi-kind `event.WithName` at `options.go:39`):
```go
type messageCorrelatorOpt struct{ msg, key string }

func (o messageCorrelatorOpt) applyStart(n *StartEvent)             { n.MessageName, n.CorrelationKey = o.msg, o.key }
func (o messageCorrelatorOpt) applyCatch(n *IntermediateCatchEvent) { n.MessageName, n.CorrelationKey = o.msg, o.key }
func (o messageCorrelatorOpt) applyBoundary(n *BoundaryEvent)       { n.MessageName, n.CorrelationKey = o.msg, o.key }

// WithMessageCorrelator sets the message name + correlation key on a start,
// catch, or boundary event.
func WithMessageCorrelator(msg, key string) interface {
	StartOption
	CatchOption
	BoundaryOption
} {
	return messageCorrelatorOpt{msg, key}
}
```
Fix call sites: `rg -l 'WithStartMessage|WithCatchMessage|WithBoundaryMessage'`.

- [ ] **Step 4: Run → PASS**; `go build ./... && go test ./...`.
- [ ] **Step 5: Commit** — `git commit -am "refactor(event): unify message setters into WithMessageCorrelator"`

### Task E2: unify signal setters → `WithSignalName` (listen) + `WithThrowSignalName` (emit)

**Files:** `definition/event/options.go` (+ call sites), test.

**Interfaces:**
- Produces: `event.WithSignalName(name string) interface { StartOption; CatchOption; BoundaryOption }`; `event.WithThrowSignalName(name string) ThrowOption`.

- [ ] **Step 1: Red** — test multi-kind `WithSignalName`:

```go
func TestWithSignalName_ListenKinds(t *testing.T) {
	c := event.NewIntermediateCatch("c", event.WithSignalName("s")).(event.IntermediateCatchEvent)
	assert.Equal(t, "s", c.SignalName)
	// Repeat for Start + Boundary.
}
```

- [ ] **Step 2: Run → FAIL**.
- [ ] **Step 3: Green** — replace `WithStartSignal`/`WithCatchSignal`/`WithBoundarySignal` with one multi-kind `WithSignalName` (same pattern as E1, setting `SignalName`); rename `WithThrowSignal`→`WithThrowSignalName` (unchanged `ThrowOption` body). Fix call sites: `rg -l 'WithStartSignal|WithCatchSignal|WithBoundarySignal|WithThrowSignal\b'`.
- [ ] **Step 4: Run → PASS**; `go build ./... && go test ./...`.
- [ ] **Step 5: Verify no stragglers** — `rg -n 'WithCatchMessage|WithCatchSignal|WithStartSignal|WithBoundarySignal|WithThrowSignal\b|WithStartMessage|WithBoundaryMessage' -g '!docs/**'` → no matches.
- [ ] **Step 6: Commit** — `git commit -am "refactor(event): unify signal setters into WithSignalName + WithThrowSignalName"`

---

## Phase F — Remove inline actions

### Task F1: delete the inline-action path; migrate consumers to catalog registration

**Files:** `definition/activity/options.go`, `definition/activity/activity.go`, `definition/model/node.go` (`TaskAction`), `definition/model/builder.go`, `engine/command.go`, `engine/main_action.go`, `runtime/resolve_action.go`, `processtest/harness.go` (+ tests/examples using `WithAction`/`WithActionFunc`).

- [ ] **Step 1: Red** — pick one test/harness inline usage (e.g. `processtest/options_test.go` or `runtime/subprocess_inline_action_test.go`) and rewrite it to register the action by name in a catalog + reference via `WithTaskAction`. Then delete `WithAction`/`WithActionFunc` and run build → FAIL at the remaining inline call sites (this enumerates every consumer to migrate).
- [ ] **Step 2: Green** — remove:
  - `activity/options.go`: `WithAction`, `WithActionFunc`, `inlineActionOpt`, `actionFunc` type.
  - `activity/activity.go`: any `Inline` field plumbing on `ServiceTask`/`BusinessRuleTask`.
  - `model/node.go`: `TaskAction.Inline` field; simplify `taskAction()` to `return t.Action, nil` **or** change its signature to return only the name — follow what `ActionOf`/`InlineActionOf` accessors need (adjust those accessors + their engine call sites).
  - `model/builder.go`: the inline-vs-name conflict validation.
  - `engine/command.go`: `InvokeAction.Inline` field + the doc lines referencing it.
  - `engine/main_action.go`: inline precedence note.
  - `runtime/resolve_action.go`: the `if cmd.Inline != nil { return cmd.Inline, true }` branch (resolve by name only).
  - Migrate every remaining consumer (`processtest/harness.go`, examples, tests) to catalog registration.
- [ ] **Step 3: Verify** — `rg -n 'WithAction\b|WithActionFunc\b|\.Inline\b|InlineActionOf' -g '!docs/**'` → no matches; `go build ./... && go test ./...` → PASS.
- [ ] **Step 4: Commit** — `git commit -am "refactor(action): remove inline actions; catalog-name resolution only"`

---

## Phase G — Cross-cutting closeout

### Task G1: CHANGELOG + migration note

**Files:** `CHANGELOG.md`.

- [ ] **Step 1:** Add an entry documenting the breaking option renames/removals and wire/YAML key changes (`compensationAction`→`compensateAction`, `cancelHandler`→`cancelAction`, `reminder*`→`wait*`, dropped legacy flat `reminderEvery`, new `completionAction`), plus the inline-action removal. Include a short "migrating persisted definitions" note.
- [ ] **Step 2: Commit** — `git commit -am "docs(changelog): option-consolidation breaking changes + migration note"`

### Task G2: full verification gate

- [ ] `go build ./...` → clean.
- [ ] `go test -race -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1` → all pass; touched packages ≥ 85%.
- [ ] `golangci-lint run ./...` → clean.
- [ ] Straggler sweep — `rg -n 'WithCompensation\b|WithCancelHandler|WithActionName|WithDeadline\b|WithWaitReminder|WithCatch(Deadline|WaitReminder|Message|Signal)|WithAction\b|WithActionFunc|Reminder(Every|Action)|CompensationAction|CancelHandler' -g '!docs/**' -g '!CHANGELOG.md'` → no matches.
- [ ] `superpowers:requesting-code-review` (or `/code-review high`) on the branch before merge.

---

## Self-Review Notes (author)

- **Spec coverage:** WS1→Phase A; WS2→Phase B + Task A2/A3 (completion) + C1 (deadline-action); WS3→Phase C; WS4→Phase D; WS5→Phase E; WS6→Phase F. Every spec workstream maps to ≥1 task.
- **Decisions locked here (from spec deferrals):** drop the legacy flat `reminderEvery` wire form (Task D1); fire-once enforcement lives with the existing Build-validation pass (Task C2 — confirm exact file at implementation).
- **Ordering:** Phase A additive-first (green throughout); renames B→F each compiler-guarded. A ships before B so completion-action lands on the pre-rename field names, then rides the renames like every other field — no rework.
- **Open confirm at execution:** exact event constructor names (`NewIntermediateCatch`, start/boundary) and `NewActionFailed` signature — resolved by reading the packages during the red step, not guessed.

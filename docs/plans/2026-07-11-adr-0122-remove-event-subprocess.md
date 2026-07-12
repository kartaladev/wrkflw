# ADR-0122 — Remove `EventSubProcess` (event-triggered `SubProcess`) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Delete the `EventSubProcess` node kind; model an event sub-process as an `activity.SubProcess` whose inner start event is event-triggered, preserving behavior exactly.

**Architecture:** ADR-0121 already added `eventTriggeredStart(def)` (selects a nested def's signal/timer/message start) and the arm/fire machinery already keys off it. This plan (a) moves the interrupting marker onto the `StartEvent` (BPMN-faithful), (b) introduces a single discriminator `eventSubprocessNested(node)` that recognizes an event-sub in *both* the legacy `event.EventSubProcess` form and the new `activity.SubProcess`-with-event-start form, (c) routes every arm/fire/drain/validation site through it, (d) proves parity with both forms green, then (e) deletes the legacy kind and the transitional branch. Internal `eventSubprocess*` identifiers are renamed to `eventTriggeredSubprocess*`.

**Tech Stack:** Go 1.25, `expr-lang/expr`, `go-co-op/gocron` v2.21.2, `jonboulle/clockwork` via `clock.Clock`. Tests: standard `testing`, project `table-test`/`use-mockgen`/`use-testcontainers` skills, black-box `_test` packages.

## Global Constraints

- **Library-first, unreleased.** Clean break — no wire aliases, no migrators (CLAUDE.md / ADR-0004).
- **No wire-format-version constant exists** — do NOT invent one. The only version is the business `ProcessDefinition.Version int` (`definition/model/node_wire.go:132`, `yaml.go:68`); it is per-definition data, not a schema version. (Corrects the handover's "bump def wire version" premise — verified against source.)
- **`definition/model` MUST NOT import `definition/event`** (import cycle). Model-space event-trigger detection goes through the wire projection `toWire(node)` and its `SignalName`/`MessageName`/`TimerTrigger`/`TimerDuration` fields.
- **TDD strict** (CLAUDE.md): a visible failing test (`go test ./<pkg>/...` showing RED) precedes every new/changed symbol. Never one Write for test + impl.
- **Tables use the project `table-test` skill**: `assert func(t, ...)` closure form (not `want`/`wantErr` fields), `t.Context()` not `context.Background()`.
- **Verification per touched package:** `go test -race ./...` green, ≥85% line coverage on touched packages, `golangci-lint run ./...` clean.
- **Commit per task** (Conventional Commits, scoped). End commit bodies with the `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>` trailer.
- **Every task must leave the tree green** (`go build ./... && go test ./...`). The legacy `event.EventSubProcess` kind stays alive and green through T1–T4; only T5 deletes it.

---

## File Structure

| File | Responsibility | Tasks |
|---|---|---|
| `definition/event/event.go` | `StartEvent.NonInterrupting` field + wire; DELETE `EventSubProcess` struct/kind/ctor/RegisterKind | T1, T5 |
| `definition/event/options.go` | `WithNonInterrupting()` StartOption; DELETE `EventSubProcessOption` + `WithEventSubProcessNonInterrupting` | T1, T5 |
| `definition/model/definition.go` | DELETE `KindEventSubProcess` enum constant | T5 |
| `definition/build/build.go` | DELETE `Builder.AddEventSubProcess` | T5 |
| `definition/model/validate.go` | reachability root + nested-def loop recognize event-triggered SubProcess; drop `KindEventSubProcess` | T3, T5 |
| `engine/step_eventsubprocess.go` | `eventSubprocessNested` discriminator; rename+generalize arm/fire | T2, T5 |
| `engine/state.go` | rename `eventSubprocessArm`→`eventTriggeredSubprocessArm`, `InstanceState.EventSubprocesses`→`EventTriggeredSubprocesses`, the `…By{Signal,Timer,Message}`/`remove…` helpers | T2 |
| `engine/step_nodes.go` | drain-detection site uses `eventSubprocessNested`; SubProcess entry unchanged (already arms via renamed fn) | T2 |
| `engine/step_state.go` | `defForScope`: DELETE the `case event.EventSubProcess` (SubProcess case already resolves it) | T5 |
| `engine/step_triggers.go`, `engine/step_errors.go`, `engine/step_compensation.go`, `engine/target_node.go` | call renamed arm/fire/remove helpers | T2 |
| `engine/step_subprocess_eventstart_test.go` (new) | parity tests authored in the `SubProcess`-with-event-start form | T4 |
| `engine/state_esp_test.go`, `step_eventsubprocess_multistart_test.go`, `step_subprocess_test.go`, `reverse_instance_test.go`, `step_nodes_test.go` | convert legacy ESP tests to SubProcess form / delete ESP-kind-specific ones | T5 |
| `definition/**/*_test.go`, `definition/testdata/golden_definition.json` | migrate ESP references; regenerate golden | T5 |
| `examples/scenarios/event_subprocess/` (new) | reference wiring | T6 |
| `docs/adr/0122-remove-event-subprocess.md` (new); `README.md`, `definition/README.md`, `engine/README.md`, `INTERACTIONS.md` | ADR + doc sweep | T7 |

---

## Task 1: `StartEvent.NonInterrupting` + `WithNonInterrupting()` + wire

**Files:**
- Modify: `definition/event/event.go` (StartEvent struct ~18-35; StartEvent `RegisterKind` block 283-307)
- Modify: `definition/event/options.go` (add `WithNonInterrupting`)
- Test: `definition/event/event_test.go`, `definition/model/nodekind_json_test.go` (wire round-trip)

**Interfaces:**
- Produces: `event.StartEvent.NonInterrupting bool`; `event.WithNonInterrupting() StartOption`. Default `false` = interrupting. Wire via existing `NodeWire.NonInterrupting`.

- [ ] **Step 1: Write the failing option+field test** in `definition/event/event_test.go`:

```go
func TestWithNonInterrupting(t *testing.T) {
	n := event.NewStart("s", event.WithMessageCorrelator("cancel", "orderId"), event.WithNonInterrupting())
	se, ok := n.(event.StartEvent)
	assert.True(t, ok)
	assert.True(t, se.NonInterrupting)
	assert.Equal(t, "cancel", se.MessageName)

	// default is interrupting
	d := event.NewStart("s2").(event.StartEvent)
	assert.False(t, d.NonInterrupting)
}
```

- [ ] **Step 2: Run — expect FAIL** (`undefined: event.WithNonInterrupting` / field missing)

Run: `go test ./definition/event/... -run TestWithNonInterrupting -v`
Expected: FAIL (compile: `WithNonInterrupting` undefined; `NonInterrupting` not a field of StartEvent).

- [ ] **Step 3: Add the field.** In `definition/event/event.go`, add to `StartEvent` struct (after `MessageStartSingleton`, before `Timer` or after `Timer` — group with trigger fields):

```go
	// NonInterrupting applies only when this start is the event-triggered inner
	// start of a SubProcess acting as an event sub-process: false (default) =
	// interrupting (the event sub-process cancels and replaces its enclosing
	// scope); true = non-interrupting (runs alongside). Meaningless on a root /
	// manual start (guarded by validation). Set via WithNonInterrupting.
	NonInterrupting bool
```

Update the StartEvent `RegisterKind` (line 283) `FromWire` to add `NonInterrupting: w.NonInterrupting,` and `ToWire` to add `w.NonInterrupting = v.NonInterrupting`.

- [ ] **Step 4: Add the option.** In `definition/event/options.go`, add a StartOption. Follow the existing `startFuncOpt`/`applyStart` pattern in that file:

```go
// WithNonInterrupting marks an event-triggered start as non-interrupting: when
// this start is the inner start of a SubProcess acting as an event sub-process,
// the sub-process runs alongside its enclosing scope instead of cancelling it.
// Default (unset) is interrupting. No effect on a root / manual start.
func WithNonInterrupting() StartOption {
	return startFuncOpt(func(n *StartEvent) { n.NonInterrupting = true })
}
```

(If the concrete StartOption adapter type/name differs, match the file's existing one, e.g. how `WithMessageStartSingleton` is written.)

- [ ] **Step 5: Run — expect PASS**

Run: `go test ./definition/event/... -run TestWithNonInterrupting -v`
Expected: PASS.

- [ ] **Step 6: Add a wire round-trip assertion** in `definition/model/nodekind_json_test.go` (or event_test.go): marshal a definition whose start has `WithNonInterrupting()`, unmarshal, assert `NonInterrupting` survives. Run the package tests:

Run: `go test ./definition/... -v`
Expected: PASS (all definition tests, no regressions).

- [ ] **Step 7: Commit**

```bash
git add definition/event/event.go definition/event/options.go definition/event/event_test.go definition/model/nodekind_json_test.go
git commit -m "feat(definition): StartEvent.NonInterrupting + WithNonInterrupting (ADR-0122)"
```

---

## Task 2: `eventSubprocessNested` discriminator + generalize & rename engine arm/fire/drain

**Files:**
- Modify: `engine/step_eventsubprocess.go` (add `eventSubprocessNested`; rename+rework `armEventSubprocesses`→`armEventTriggeredSubprocesses`, `fireEventSubprocessArm`→`fireEventTriggeredSubprocessArm`)
- Modify: `engine/state.go` (rename `eventSubprocessArm`→`eventTriggeredSubprocessArm`; `InstanceState.EventSubprocesses`→`EventTriggeredSubprocesses`; `eventSubprocessArmBy{Signal,Timer,Message}`→`eventTriggeredSubprocessArmBy…`; `remove{,All,…ForScope}EventSubprocessArm(s)`→`…EventTriggeredSubprocessArm(s)`)
- Modify: `engine/step_nodes.go` (drain-detection site 269-275; the caller of the renamed arm fn ~543-549)
- Modify: `engine/step_triggers.go` (`:41`, `:222`, `:418-419`, `:630-635`, `:731`, `:781-783`), `engine/step_errors.go` (`:334`, `:415`), `engine/step_compensation.go` (`:387-390`, `:672`, `:679-682`, `:722`), `engine/target_node.go` (`:74-75`) — call renamed helpers
- Test: existing engine ESP tests remain the oracle (must stay GREEN); no new test authored here (parity tests are T4)

**Interfaces:**
- Produces:
  ```go
  // eventSubprocessNested reports whether raw acts as an event sub-process and,
  // if so, returns its nested definition and non-interrupting flag. Two forms are
  // recognized during the ADR-0122 migration: (1) the legacy event.EventSubProcess
  // kind (flag on the node); (2) an activity.SubProcess whose inner start is
  // event-triggered (flag on that start event, per ADR-0122). A SubProcess with
  // only a manual/none start is NOT an event sub-process (ok=false) — it stays
  // token-driven inline. The legacy case is deleted in T5.
  func eventSubprocessNested(raw model.Node) (nested *model.ProcessDefinition, nonInterrupting bool, ok bool)
  ```
- Renamed symbols consumed by later tasks: `armEventTriggeredSubprocesses`, `fireEventTriggeredSubprocessArm`, `eventTriggeredSubprocessArm`, `InstanceState.EventTriggeredSubprocesses`, `removeEventTriggeredSubprocessArmsForScope`, `removeAllEventTriggeredSubprocessArms`, `removeEventTriggeredSubprocessArm`, `eventTriggeredSubprocessArmBy{Signal,Timer,Message}`.

- [ ] **Step 1: Confirm RED baseline is GREEN first.** Before changing anything, run the ESP tests to capture the passing oracle:

Run: `go test ./engine/... -run 'ESP|EventSubprocess|EventSubProcess|Subprocess' -v 2>&1 | tail -30`
Expected: PASS (this is the behavior to preserve).

- [ ] **Step 2: Add the discriminator.** In `engine/step_eventsubprocess.go`, add (import `activity` "github.com/kartaladev/wrkflw/definition/activity"):

```go
func eventSubprocessNested(raw model.Node) (*model.ProcessDefinition, bool, bool) {
	switch n := raw.(type) {
	case event.EventSubProcess: // legacy kind — removed in T5
		if n.Subprocess == nil {
			return nil, false, false
		}
		if _, ok := eventTriggeredStart(n.Subprocess); !ok {
			return nil, false, false
		}
		return n.Subprocess, n.NonInterrupting, true
	case activity.SubProcess:
		if n.Subprocess == nil {
			return nil, false, false
		}
		se, ok := eventTriggeredStart(n.Subprocess)
		if !ok {
			return nil, false, false
		}
		return n.Subprocess, se.NonInterrupting, true
	}
	return nil, false, false
}
```

- [ ] **Step 3: Route the arm scan through it.** In `armEventSubprocesses` (rename to `armEventTriggeredSubprocesses`), replace the `raw.(event.EventSubProcess)` type-switch (lines 48-62) with:

```go
	for _, raw := range def.Nodes {
		nested, nonInterrupting, ok := eventSubprocessNested(raw)
		if !ok {
			continue
		}
		se, _ := eventTriggeredStart(nested) // ok guaranteed by eventSubprocessNested
		arm := eventTriggeredSubprocessArm{
			EnclosingScopeID:    enclosingScopeID,
			EventSubprocessNode: raw.ID(),
			NonInterrupting:     nonInterrupting,
		}
		// ... existing signal/timer/message trigger encoding, unchanged, using se ...
		s.EventTriggeredSubprocesses = append(s.EventTriggeredSubprocesses, arm)
	}
```
Keep the trigger-encoding body (signal/timer/message) exactly as-is; only the node selection, the arm type name, the NonInterrupting source, and the append target change. (`eventSubprocessArm` struct field `EventSubprocessNode` name may stay — it's internal; rename optional, but the struct type renames to `eventTriggeredSubprocessArm`.)

- [ ] **Step 4: Route fire through it.** In `fireEventSubprocessArm` (rename to `fireEventTriggeredSubprocessArm`), replace the node resolution (lines 150-167) — instead of asserting `event.EventSubProcess`, call `eventSubprocessNested(espRaw)` to get `nested`; then `innerStart, ok := eventTriggeredStart(nested)`. Everything else (interrupting/non-interrupting branches, `openScope`, `placeTokenInScope`, drive) stays identical, with `s.removeEventSubprocessArmsForScope`→`s.removeEventTriggeredSubprocessArmsForScope` and `s.removeEventSubprocessArm`→`s.removeEventTriggeredSubprocessArm`.

- [ ] **Step 5: Route the drain detection through it.** In `engine/step_nodes.go:269-275`, replace:

```go
		isEventSubProcess := false
		parentDef, pErr := defForScope(c.def, c.s, parentScopeID)
		if pErr == nil {
			if espNode, ok2 := parentDef.Node(subNodeID); ok2 {
				_, _, isEventSubProcess = eventSubprocessNested(espNode)
			}
		}
```
Also rename the arm-cleanup calls in this region (`removeEventSubprocessArmsForScope` at `:311`, `:365`, `:418`, `:500`) to `removeEventTriggeredSubprocessArmsForScope`.

- [ ] **Step 6: Rename state.go symbols and all call sites.** In `engine/state.go` rename the struct, field, and helper methods per the Interfaces block. Then update every caller: the arm-fn call in `subProcessStrategy.enter` (`step_nodes.go` ~543-549) and the sites in `step_triggers.go`, `step_errors.go`, `step_compensation.go`, `target_node.go` listed under Files. Use a compiler-driven sweep:

Run: `go build ./... 2>&1 | head -40`
Fix each `undefined:` until the build is clean. (Deterministic: the old names no longer exist, so every stale reference surfaces.)

- [ ] **Step 7: Run the ESP oracle — expect STILL GREEN.** The legacy `event.EventSubProcess` form must behave identically (it still routes through `eventSubprocessNested`'s legacy case):

Run: `go test -race ./engine/... 2>&1 | tail -20`
Expected: PASS (no behavior change; pure generalization + rename).

- [ ] **Step 8: Full suite + lint**

Run: `go build ./... && go test ./... 2>&1 | tail -20 && golangci-lint run ./engine/...`
Expected: PASS, clean.

- [ ] **Step 9: Commit**

```bash
git add engine/
git commit -m "refactor(engine): eventSubprocessNested discriminator + rename to eventTriggeredSubprocess* (ADR-0122)"
```

> **Review gate:** T2 is engine-behaviour + concurrency (token cancellation, scope drain). Dispatch a full **opus** reviewer: verify the legacy form is behavior-identical, the discriminator correctly excludes none-start SubProcesses, and no rename missed a call site.

---

## Task 3: Validation recognizes event-triggered `SubProcess`

**Files:**
- Modify: `definition/model/validate.go` (reachability roots 346-352; keep nested-def loop 445-456 as-is until T5)
- Test: `definition/model/validate_test.go`

**Interfaces:**
- Produces: a model-space predicate
  ```go
  // isEventTriggeredSubprocess reports whether n is a KindSubProcess whose nested
  // definition has an event-triggered (signal/timer/message) start. Model-space
  // only — uses the wire projection because definition/model cannot import event.
  func isEventTriggeredSubprocess(n Node) bool
  ```

- [ ] **Step 1: Write the failing reachability test** in `definition/model/validate_test.go`: a definition whose only flow-reachable nodes are the none-start chain, plus a `SubProcess` with a message-triggered inner start and NO incoming flow, must validate WITHOUT `ErrUnreachableNode` (the event-sub SubProcess is a reachability root). Use the `table-test` assert-closure form.

```go
func TestValidate_EventTriggeredSubprocessIsReachabilityRoot(t *testing.T) {
	inner := model.NewDefinition("esc",
		event.NewStart("onCancel", event.WithMessageCorrelator("cancel", "orderId")),
		activity.NewServiceTask("notify"), event.NewEnd("ie"))
	d := model.NewDefinition("p",
		event.NewStart("s"), activity.NewServiceTask("work"), event.NewEnd("e"),
		activity.NewSubProcess("handleCancel", inner), // no incoming flow
		flow.New("s", "work"), flow.New("work", "e"))
	assert.NoError(t, model.Validate(d))
}
```
(Confirm the exact `model.Validate`/`validateStructure` entry name and `flow.New` signature against the file; adjust the constructor calls to match existing tests in that package.)

- [ ] **Step 2: Run — expect FAIL** (`ErrUnreachableNode: node "handleCancel"` — SubProcess-with-event-start not yet a reachability root)

Run: `go test ./definition/model/... -run TestValidate_EventTriggeredSubprocessIsReachabilityRoot -v`
Expected: FAIL.

- [ ] **Step 3: Add the predicate + widen the reachability seed.** In `validate.go`, add `isEventTriggeredSubprocess`:

```go
func isEventTriggeredSubprocess(n Node) bool {
	if n.Kind() != KindSubProcess {
		return false
	}
	sub := toWire(n).Subprocess
	if sub == nil {
		return false
	}
	for _, st := range sub.StartNodes() {
		w := toWire(st)
		if w.SignalName != "" || w.MessageName != "" || w.TimerTrigger != nil || w.TimerDuration != "" {
			return true
		}
	}
	return false
}
```
Change the reachability-root loop (346-352) to also seed from event-triggered SubProcesses (transitional — recognizes BOTH forms):

```go
	for _, n := range d.Nodes {
		if n.Kind() == KindEventSubProcess || isEventTriggeredSubprocess(n) {
			for id := range forwardReachable(d, n.ID()) {
				reached[id] = true
			}
		}
	}
```

- [ ] **Step 4: Run — expect PASS**

Run: `go test ./definition/model/... -run TestValidate_EventTriggeredSubprocessIsReachabilityRoot -v`
Expected: PASS.

- [ ] **Step 5: (Optional guard) reject ambiguous mixed SubProcess.** Add a test + rule: a `SubProcess` with an event-triggered start MUST have no incoming sequence flow (it is an event sub-process, not embedded). If `isEventTriggeredSubprocess(n) && len(d.Incoming(n.ID())) > 0` → a new sentinel `ErrEventSubprocessOnFlow`. Keep it minimal; this is beyond strict parity but prevents an un-modelable node. Write the test first (RED), then the rule (GREEN).

- [ ] **Step 6: Full definition suite + lint**

Run: `go test ./definition/... && golangci-lint run ./definition/...`
Expected: PASS, clean.

- [ ] **Step 7: Commit**

```bash
git add definition/model/validate.go definition/model/validate_test.go
git commit -m "feat(definition): validate event-triggered SubProcess as reachability root (ADR-0122)"
```

---

## Task 4: Parity tests in the `SubProcess`-with-event-start form

**Files:**
- Create: `engine/step_subprocess_eventstart_test.go`
- Reference (mirror the scenarios of): `engine/state_esp_test.go`, `engine/step_eventsubprocess_multistart_test.go`, and the ESP cases in `engine/step_subprocess_test.go` / `engine/reverse_instance_test.go`

**Interfaces:**
- Consumes: everything from T1–T3. Authors event-subs as `activity.NewSubProcess(id, innerDefWithEventStart)`; sets non-interrupting via `event.WithNonInterrupting()` on the inner start.

Purpose: with the engine now recognizing BOTH forms, the same scenarios must pass in the new form — demonstrating parity while the legacy ESP tests also stay green.

- [ ] **Step 1: Enumerate the scenarios to mirror.** For each of these existing ESP behaviors, write a test that authors the event-sub as a SubProcess-with-event-start:
  1. Root-level **interrupting** message event-sub cancels enclosing (root) tokens and completes.
  2. Root-level **non-interrupting** signal event-sub runs alongside; both drain to completion.
  3. **Nested** event-sub (declared inside a SubProcess scope) arms on scope open, fires, drains correctly.
  4. **Timer**-triggered event-sub emits `ScheduleTimer` on arm and fires on `TimerFired`.
  5. **Message**-triggered event-sub correlates by key.
  6. Scope-drain edge cases: interrupting event-sub with sibling non-interrupting children; root-ESP "Fix 1/Fix 2" cases.
  7. **Reverse-instance** over an armed event-sub (mirror `reverse_instance_test.go` ESP cases).

- [ ] **Step 2: Write one fully-worked test first (RED→GREEN pattern), then the rest.** Example (interrupting root message event-sub):

```go
func TestEventStartSubprocess_RootInterrupting(t *testing.T) {
	inner := model.NewDefinition("esc",
		event.NewStart("onCancel", event.WithMessageCorrelator("cancel", "orderId")),
		activity.NewServiceTask("notify"), event.NewEnd("ie"),
		flow.New("onCancel", "notify"), flow.New("notify", "ie"))
	def := model.NewDefinition("order",
		event.NewStart("s"),
		event.NewIntermediateCatch("wait", event.WithSignalName("never")), // parks a root token
		event.NewEnd("e"),
		activity.NewSubProcess("handleCancel", inner), // event-sub: no incoming flow
		flow.New("s", "wait"), flow.New("wait", "e"))

	// Drive to the parked state, deliver "cancel", assert:
	//  - the root "wait" token is cancelled,
	//  - the event-sub scope runs "notify",
	//  - the instance completes.
	// Mirror the harness setup used by the corresponding case in state_esp_test.go.
	// assert closures per table-test skill.
}
```
Author the remaining scenarios by taking each existing ESP test and swapping `event.NewEventSubProcess(id, inner, event.WithEventSubProcessNonInterrupting())` → `activity.NewSubProcess(id, inner)` with `event.WithNonInterrupting()` moved onto `inner`'s start. Keep every assertion identical.

- [ ] **Step 3: Run each new test — RED then GREEN.** For each test: run it, confirm it fails only if the engine were wrong (it should PASS now that T2/T3 landed — the parity proof is that the identical assertions hold). If any FAIL, that is a real parity gap → fix the engine under T2's rules (not the test).

Run: `go test -race ./engine/... -run EventStartSubprocess -v`
Expected: PASS (all scenarios) AND the legacy ESP tests still PASS:
Run: `go test -race ./engine/... 2>&1 | tail -10`
Expected: PASS (both forms green = parity demonstrated).

- [ ] **Step 4: Commit**

```bash
git add engine/step_subprocess_eventstart_test.go
git commit -m "test(engine): parity tests for event-triggered SubProcess (ADR-0122)"
```

---

## Task 5: Delete the `EventSubProcess` kind + transitional branch; convert legacy tests; regenerate golden

**Files:**
- Modify: `definition/event/event.go` (DELETE struct 157-168, ctor 270-278, RegisterKind 398-407, doc mentions 17/202/271)
- Modify: `definition/event/options.go` (DELETE `EventSubProcessOption` 30-31 + list membership 50; `espFuncOpt`/`WithEventSubProcessNonInterrupting` 266-275; the `applyEventSubProcess` on `nameOpt` 41)
- Modify: `definition/model/definition.go` (DELETE `KindEventSubProcess` 28)
- Modify: `definition/build/build.go` (DELETE `AddEventSubProcess` 92-93)
- Modify: `definition/model/validate.go` (drop `KindEventSubProcess` from reachability 347 and nested-def loop 446; `ErrMissingSubprocess` doc 74-77)
- Modify: `engine/step_eventsubprocess.go` (DELETE the `case event.EventSubProcess` branch in `eventSubprocessNested`; drop the now-unused `event` import if orphaned)
- Modify: `engine/step_state.go` (DELETE `defForScope`'s `case event.EventSubProcess` — the `activity.SubProcess` case already resolves it)
- Modify/convert: `engine/state_esp_test.go`, `engine/step_eventsubprocess_multistart_test.go`, `engine/step_subprocess_test.go`, `engine/reverse_instance_test.go`, `engine/step_nodes_test.go`, `definition/model/node_test.go`, `definition/model/definition_test.go`, `definition/model/accessors_test.go`, `definition/build/build_test.go`, `definition/event/event_test.go`, `definition/model/nodekind_json_test.go`, `definition/model/validate_test.go`
- Regenerate: `definition/testdata/golden_definition.json`

- [ ] **Step 1: Delete the model/API surface (compiler-driven).** Remove the struct, `Kind()`, ctor, options, `KindEventSubProcess`, `AddEventSubProcess`, and the `eventSubProcess` `RegisterKind`. Then:

Run: `go build ./... 2>&1 | head -60`
Expected: FAIL with `undefined: event.EventSubProcess` / `KindEventSubProcess` / `NewEventSubProcess` / `WithEventSubProcessNonInterrupting` at every legacy call site — this is the worklist.

- [ ] **Step 2: Convert every legacy test authoring an ESP** to the SubProcess-with-event-start form (same swap as T4 Step 2): `event.NewEventSubProcess(id, inner, event.WithEventSubProcessNonInterrupting())` → `activity.NewSubProcess(id, inner)` + `event.WithNonInterrupting()` on `inner`'s start. For ESP-kind-specific unit tests (e.g. `nodekind_json_test.go` asserting the `"eventSubProcess"` wire name; `node_test.go` asserting `KindEventSubProcess`), DELETE those specific assertions (the kind no longer exists). `state_esp_test.go` and `step_eventsubprocess_multistart_test.go` are ESP-kind-dedicated — fold any still-unique coverage into `step_subprocess_eventstart_test.go` (T4) and delete the files, or convert in place.

- [ ] **Step 3: Collapse the transitional branches.** In `eventSubprocessNested` delete the `case event.EventSubProcess`. In `validate.go` change `n.Kind() == KindEventSubProcess || isEventTriggeredSubprocess(n)` → `isEventTriggeredSubprocess(n)` and the nested-def loop guard `n.Kind() != KindSubProcess && n.Kind() != KindEventSubProcess` → `n.Kind() != KindSubProcess`. Delete `defForScope`'s ESP case.

- [ ] **Step 4: Build clean**

Run: `go build ./... 2>&1 | head -40`
Expected: clean (all references resolved). Remove any now-orphaned `event` imports flagged by the compiler.

- [ ] **Step 5: Regenerate the golden fixture.** `definition/testdata/golden_definition.json` pins one `eventSubProcess` node. Rebuild it in the SubProcess-with-event-start form. Find the generator:

Run: `grep -rn "golden_definition" --include='*.go' definition/`
Then either run the golden-update test flag (e.g. `go test ./definition/... -run Golden -update`) if one exists, or hand-edit the JSON: replace the `"kind":"eventSubProcess"` node with a `"kind":"subProcess"` node whose nested `startEvent` carries the trigger + `nonInterrupting`. Confirm by round-trip test.

- [ ] **Step 6: Full suite + race + coverage + lint**

Run: `go test -race -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1`
Expected: PASS, ≥85% on touched packages.
Run: `golangci-lint run ./...`
Expected: clean.

Run: `grep -rn "EventSubProcess\|eventSubProcess\|KindEventSubProcess" --include='*.go' .`
Expected: NO matches (all identifiers gone; only `eventTriggeredSubprocess*` remain).

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "feat(definition)!: remove EventSubProcess kind; event-sub is a SubProcess with event-triggered start (ADR-0122)"
```

> **Review gate:** T5 is deletion + the migrate/remove refactor. Dispatch a full **opus** reviewer: confirm no behavioral ESP coverage was silently dropped (every scenario in the deleted files exists in `step_subprocess_eventstart_test.go`), the golden regen is faithful, and the grep is truly clean.

---

## Task 6: Example `event_subprocess`

**Files:**
- Create: `examples/scenarios/event_subprocess/main.go` (+ any README the sibling examples use)
- Reference: an existing `examples/scenarios/*/main.go` for the wiring pattern; `examples/scenarios/subprocess_embedded` for the none-start inline contrast

- [ ] **Step 1: Write the example** — a `SubProcess` with a **message-triggered non-interrupting** inner start acting as an event sub-process (e.g. an order process where a `cancel` message spawns a non-interrupting "notify + log" event sub-process alongside the running order). Mirror the construction and driver-wiring style of a neighboring scenario. Per the `examples/` convention memory: examples show engine INTERNAL mechanics; do NOT wire in `processtest`/test helpers.

- [ ] **Step 2: Build + run**

Run: `go build ./examples/... && go run ./examples/scenarios/event_subprocess`
Expected: builds; runs to the intended output (event-sub fires on the delivered message).

- [ ] **Step 3: Commit**

```bash
git add examples/scenarios/event_subprocess
git commit -m "docs(examples): event_subprocess scenario (SubProcess with event-triggered start) (ADR-0122)"
```

---

## Task 7: ADR-0122 + documentation sweep

**Files:**
- Create: `docs/adr/0122-remove-event-subprocess.md` (Nygard: Status/Date, Context, Decision, Consequences)
- Modify: `README.md`, `definition/README.md`, `engine/README.md`, `INTERACTIONS.md` (ESP references → SubProcess-with-event-start)

- [ ] **Step 1: Write the ADR** following the Nygard template (see `docs/adr/0001-record-architecture-decisions.md`). Content from the spec `docs/specs/2026-07-10-adr-0122-remove-event-subprocess-design.md`: the redundancy context, D1 (marker on StartEvent), D2 (discriminator), D3 (rename), D4 (clean break, no version constant), D5 (validation), and the parity-first mitigation as Consequences.

- [ ] **Step 2: Sweep the READMEs.** Find and rewrite live ESP references (skip historical ADRs/plans/specs — those record past state and stay as-is):

Run: `grep -rn "EventSubProcess\|eventSubProcess" README.md definition/README.md engine/README.md INTERACTIONS.md`
Rewrite each: authoring is now `activity.NewSubProcess` with an event-triggered inner start; the interrupting marker is `WithNonInterrupting()` on that start. Ensure any code samples compile (0119 lesson: stale README samples leaked non-compiling code).

- [ ] **Step 3: Verify docs code samples** (if any README Go blocks are runnable, extract and `go build` them, or eyeball against the new API).

Run: `go build ./... && go test ./...`
Expected: PASS (no code touched, sanity check).

- [ ] **Step 4: Commit**

```bash
git add docs/adr/0122-remove-event-subprocess.md README.md definition/README.md engine/README.md INTERACTIONS.md
git commit -m "docs(adr): ADR-0122 remove EventSubProcess + README sweep"
```

---

## Whole-branch review + merge (post-plan)

After T7, before merging to main:
1. **Whole-branch `/code-review`** (high, multi-finder + opus composition) — point it at: interrupting vs non-interrupting **parity** vs the old ESP path, the scope-drain edge cases (root "Fix 1/Fix 2", sibling non-interrupting children), the discriminator correctly excluding none-start SubProcesses, and **doc-completeness** (grep `.md` too, not just `.go`). This is the load-bearing guard — it caught a Critical + systemic flaw on ADR-0121 that per-task reviews missed, because a change that lifts/retires an invariant ripples into untouched files.
2. Fix findings in TDD waves (RED reproduces the bug first), re-review.
3. `go test -race ./...` green, ≥85% coverage on touched packages, `golangci-lint run ./...` clean.
4. Merge `--no-ff` to main, push over SSH. Write a `[[adr-0121-shipped]]`-style shipped memory; update the BPMN2-alignment handover (all 6 features done).

## Self-review notes (author)

- **Spec coverage:** D1→T1; D2→T2; D3→T2; D4→T5 (+ constraint: no version constant); D5→T3/T5; parity-first mitigation→T2/T3/T4/T5 ordering; example→T6; ADR+docs→T7. All spec sections mapped.
- **Type consistency:** `eventSubprocessNested` signature identical in T2 (produce) and T5 (delete-a-branch); `armEventTriggeredSubprocesses`/`fireEventTriggeredSubprocessArm`/`eventTriggeredSubprocessArm`/`EventTriggeredSubprocesses` used consistently T2→T5; `isEventTriggeredSubprocess` model-space predicate T3→T5.
- **Green-every-task:** legacy kind alive T1–T4; deletion isolated to T5. No task leaves the tree red.
- **Known judgement calls flagged for implementers:** exact StartOption adapter name in T1 (match file); `model.Validate` entry name + `flow.New` signature in T3 (match package); golden regen mechanism in T5 (grep for `-update` flag).

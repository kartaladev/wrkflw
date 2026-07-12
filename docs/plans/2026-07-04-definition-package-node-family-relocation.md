# `definition` Package + Node-Family Relocation — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rename `model` → `definition` and relocate the 19 node kinds into
`event`/`gateway`/`activity` leaf packages via a driver-registration pattern, with
a `definition/build` fluent wrapper — reducing the add-a-kind tax and grouping the
node palette by BPMN family, with zero format/behavior change.

**Architecture:** `definition` (top package) owns `Node`, `ProcessDefinition`, the
builder, validation, serialization, and a per-kind **registry**; it imports no
leaf. Leaf packages `event`/`gateway`/`activity` define the concrete structs +
constructors + options and register their wire-factories in `init()`. `definition/
kinds` blank-imports the leaves so any deserialization path guarantees a populated
registry. `definition/build` supplies the fluent `AddX` chain.

**Tech Stack:** Go 1.25, `gopkg.in/yaml.v3`, `github.com/expr-lang/expr` (unchanged),
standard `encoding/json`. No new dependencies.

## Global Constraints

- Go 1.25; module `github.com/kartaladev/wrkflw`.
- `definition` and its leaves import **only the Go standard library +
  `action` + yaml.v3** (pure data + validation; no transport/storage/engine).
- Wire format is **frozen**: JSON/YAML discriminator strings and field names are
  byte-for-byte unchanged; stored JSONB must round-trip identically.
- Error sentinels use the `workflow-definition:` prefix (was `workflow-model:`).
- No backward-compat aliases — every `model.*` reference is rewritten.
- Import rule (enforced): `definition` imports no leaf; leaves import only
  `definition`; `kinds`/`build` import leaves. No cycles.
- TDD per CLAUDE.md: every **new** exported symbol (registry API, exported embed
  types, loud error, round-trip guards, leaf constructors/options, `build`
  methods) is preceded by a visible failing test. Pure moves/renames are
  behavior-preserving and gated by the **existing** test suite passing before and
  after (no silent red skipped).
- Gates after each phase: `go build ./...` green, touched-package tests green,
  no import cycles. Final: `go test ./...`, `-race`, coverage ≥ 85% per touched
  package, `golangci-lint run ./...` clean.

---

## Phase 0 — Baseline & branch

### Task 0: Confirm green baseline on a branch

**Files:** none (setup).

- [ ] **Step 1: Confirm clean main + green build/tests**

Run:
```bash
git status --porcelain            # expect empty
go build ./...                    # expect no output
go test ./model/... ./engine/... ./runtime/... 2>&1 | tail -20
```
Expected: build clean; listed packages `ok`.

- [ ] **Step 2: Create the working branch (if not already on it)**

Run:
```bash
git rev-parse --abbrev-ref HEAD   # if not refactor/definition-relocation:
git checkout -b refactor/definition-relocation
```

- [ ] **Step 3: Capture a golden serialization fixture (regression anchor)**

Create `model/testdata/golden_definition.json` by round-tripping a
representative definition (all option-bearing kinds) through the *current*
`model` marshaler, and commit it. This file is the byte-compatibility oracle used
in Task 9.

Run:
```bash
# write a tiny throwaway program or reuse an existing test to emit the JSON,
# then:
git add model/testdata/golden_definition.json
git commit -m "test(model): add golden serialization fixture for relocation"
```
Expected: fixture committed; `go test ./model/...` still green.

---

## Phase 1 — Rename `model` → `definition` (pure, mechanical)

No relocation yet: one package, new name. The compiler is the safety net.

### Task 1: Move the directory and rewrite the package clause + import paths

**Files:**
- Move: `model/` → `definition/` (all `.go`, `README.md`, `testdata/`).
- Modify: every file repo-wide importing `.../model` or referencing `model.`.

**Interfaces:**
- Produces: package `definition` at import path
  `github.com/kartaladev/wrkflw/definition`, with the **same exported symbols
  as `model`** (no renames yet). Later tasks consume `definition.Node`,
  `definition.ProcessDefinition`, `definition.NewDefinition`, `definition.Validate`,
  `definition.NewServiceTask`, … .

- [ ] **Step 1: Move the package directory**

Run:
```bash
git mv model definition
```

- [ ] **Step 2: Rewrite the package clause in the moved files**

In every `definition/*.go`, change `package model` → `package definition`
(and `package model_test` → `package definition_test`).
Run:
```bash
grep -rl '^package model' definition | xargs sed -i '' -E 's/^package model(_test)?$/package definition\1/'
grep -rn '^package model' definition   # expect empty
```

- [ ] **Step 3: Rewrite import paths repo-wide**

Run:
```bash
grep -rl 'wrkflw/model' --include='*.go' . \
  | xargs sed -i '' 's#wrkflw/model#wrkflw/definition#g'
grep -rn 'wrkflw/model"' --include='*.go' . | grep -v wrkflw/definition   # expect empty
```

- [ ] **Step 4: Rewrite qualified identifiers `model.` → `definition.`**

Only the package selector, not substrings. Run:
```bash
grep -rl --include='*.go' -E '\bmodel\.' . \
  | xargs sed -i '' -E 's/\bmodel\./definition./g'
```
Then hand-audit for false positives (a local variable literally named `model`):
```bash
grep -rn --include='*.go' -E '\bmodel\b' . | grep -v definition
```
Fix any variable/receiver named `model` that got mis-rewritten.

- [ ] **Step 5: Update the internal error-sentinel prefix**

Run:
```bash
grep -rl 'workflow-model:' definition | xargs sed -i '' 's/workflow-model:/workflow-definition:/g'
grep -rn 'workflow-model' definition   # expect empty
```
Update any test asserting the old prefix string accordingly.

- [ ] **Step 6: Build and test**

Run:
```bash
go build ./... 2>&1 | head
go test ./definition/... 2>&1 | tail -5
go test ./... 2>&1 | tail -30
```
Expected: everything compiles; all packages `ok`. The rename is behavior-neutral —
a red here means a missed identifier.

- [ ] **Step 7: Rename the package README and update its prose**

Run:
```bash
git mv definition/README.md definition/README.md   # already moved; edit contents
```
Replace `model` → `definition` and `Package \`model\`` headers in
`definition/README.md`. (Constructor renames land in Phase 5; leave `model.NewX`
prose as `definition.NewX` for now.)

- [ ] **Step 8: Commit**

Run:
```bash
git add -A
git commit -m "refactor(definition): rename model package to definition

Pure directory + identifier rename across the repo; no relocation or
behavior change. Compiler-verified green."
```

---

## Phase 2 — Internal cleanup inside `definition` (behavior-preserving)

Still one package. Introduce the registry and exported embed types, collapse the
switches, dedupe YAML. Existing tests are the gate; new symbols get red-first
tests.

### Task 2: Export the wire union and embed field-groups

**Files:**
- Modify: `definition/node_wire.go` (rename `nodeWire`→`NodeWire`,
  `applyActivityWire`→exported helper, `activity()`→`Activity()`).
- Modify: `definition/node.go` (`baseNode`→`Base`, `activityFields`→`ActivityFields`,
  add `WaitFields`).
- Test: `definition/node_wire_test.go`, `definition/accessors_test.go` (existing).

**Interfaces:**
- Produces:
  - `type Base struct { … }` with `func NewBase(id, name string) Base`,
    `(Base) ID() string`, `(Base) Name() string`, `(*Base) SetName(string)`.
  - `type WaitFields struct { DeadlineDuration, DeadlineFlow, DeadlineAction,
    ReminderEvery, ReminderAction string }` with
    `(WaitFields) deadline() (string,string,string)` and
    `(WaitFields) reminder() (string,string)`.
  - `type ActivityFields struct { WaitFields; RetryPolicy *RetryPolicy;
    RecoveryFlow, CompensationAction, CancelHandler string }` with
    `(ActivityFields) retry() *RetryPolicy` and
    `(ActivityFields) recoveryFlow() string`.
  - `type NodeWire struct { … }` (was `nodeWire`, all fields exported already were),
    `(NodeWire) Activity() ActivityFields`, `(*NodeWire) PutActivity(ActivityFields)`.

- [ ] **Step 1: Write failing tests for the embed accessor methods**

Add to `definition/node_test.go`:
```go
func TestActivityFieldsAccessors(t *testing.T) {
	a := ActivityFields{
		WaitFields:   WaitFields{DeadlineDuration: "2h", DeadlineFlow: "f", DeadlineAction: "act", ReminderEvery: "1h", ReminderAction: "r"},
		RetryPolicy:  &RetryPolicy{MaxAttempts: 5},
		RecoveryFlow: "rec",
	}
	d, f, act := a.deadline()
	assert(t, d == "2h" && f == "f" && act == "act", "deadline")
	e, r := a.reminder()
	assert(t, e == "1h" && r == "r", "reminder")
	assert(t, a.retry().MaxAttempts == 5, "retry")
	assert(t, a.recoveryFlow() == "rec", "recoveryFlow")
}
```
(Use the package's existing `assert` closure convention — see the `table-test` skill.)

- [ ] **Step 2: Run — expect FAIL (undefined types/methods)**

Run: `go test ./definition/ -run TestActivityFieldsAccessors`
Expected: compile error `undefined: WaitFields` / method set.

- [ ] **Step 3: Rename + add the embed types**

In `definition/node.go`: rename `baseNode`→`Base` (fields stay unexported; add
`NewBase`, `SetName`), rename `activityFields`→`ActivityFields`, extract the
deadline/reminder five into `WaitFields`, embed `WaitFields` into `ActivityFields`,
add the four methods. Update all embedders in `node.go`
(`ServiceTask{Base; ActivityFields; …}` etc.) and make
`IntermediateCatchEvent` embed `WaitFields` in place of its five inline fields.
Rename the type in `node_wire.go` `nodeWire`→`NodeWire`, `.activity()`→`.Activity()`,
add `PutActivity`.

- [ ] **Step 4: Run the new test + the full package**

Run:
```bash
go test ./definition/ -run TestActivityFieldsAccessors
go test ./definition/...
```
Expected: PASS; whole package green (existing tests still pass — the field
promotion keeps `ice.DeadlineDuration` working).

- [ ] **Step 5: Commit**

```bash
git add definition
git commit -m "refactor(definition): export Base/ActivityFields/NodeWire, add WaitFields

Extract deadline+reminder into an embeddable WaitFields (shared by activities
and IntermediateCatchEvent, removing its field duplication); add accessor
methods for the coming interface-based collapse."
```

### Task 3: Collapse the parallel accessor switches

**Files:**
- Modify: `definition/accessors.go`, `definition/validate.go` (`recoveryFlowOf`).
- Test: `definition/accessors_test.go` (existing, unchanged assertions).

**Interfaces:**
- Consumes: the `deadline()/reminder()/retry()/recoveryFlow()` methods from Task 2.
- Produces: `RetryPolicyOf`, `DeadlineOf`, `ReminderOf`, `recoveryFlowOf`,
  `ActionOf`, `InlineActionOf` with unchanged signatures & behavior.

- [ ] **Step 1: Run existing accessor tests (green before)**

Run: `go test ./definition/ -run 'Of$|Accessor'` — Expected: PASS.

- [ ] **Step 2: Rewrite the accessors as interface assertions**

```go
func RetryPolicyOf(n Node) *RetryPolicy {
	if a, ok := n.(interface{ retry() *RetryPolicy }); ok {
		return a.retry()
	}
	return nil
}
func DeadlineOf(n Node) (duration, flow, action string) {
	if w, ok := n.(interface{ deadline() (string, string, string) }); ok {
		return w.deadline()
	}
	return "", "", ""
}
func ReminderOf(n Node) (every, action string) {
	if w, ok := n.(interface{ reminder() (string, string) }); ok {
		return w.reminder()
	}
	return "", ""
}
```
In `validate.go`, `recoveryFlowOf(n)` → the `retry`-carrier assertion returning
`recoveryFlow()`. For `ActionOf`/`InlineActionOf`, introduce an embedded
`taskAction struct { Action string; inline action.ServiceAction }` on `ServiceTask`
+ `BusinessRuleTask` with `taskAction() (string, action.ServiceAction)` and assert
on `interface{ taskAction() (string, action.ServiceAction) }`.

- [ ] **Step 3: Run accessor + validate tests (green after)**

Run: `go test ./definition/...` — Expected: PASS unchanged.

- [ ] **Step 4: Commit**

```bash
git add definition
git commit -m "refactor(definition): collapse parallel accessor switches to interface asserts

RetryPolicyOf/DeadlineOf/ReminderOf/recoveryFlowOf/ActionOf/InlineActionOf now
dispatch on unexported carrier interfaces instead of enumerating activity kinds."
```

### Task 4: Introduce the per-kind registry (still switch-backed)

**Files:**
- Create: `definition/registry.go`.
- Modify: `definition/node_wire.go` (route `toWire`/`fromWire` through the registry),
  `definition/nodekind_json.go` (derive names from the registry).
- Test: `definition/registry_test.go`.

**Interfaces:**
- Produces:
  - `type NodeSpec struct { Name string; FromWire func(Base, NodeWire) Node;
    ToWire func(Node, *NodeWire) }`.
  - `func RegisterKind(k NodeKind, s NodeSpec)` — panics on duplicate/empty name
    (registration is init-time programmer error).
  - `func specFor(k NodeKind) (NodeSpec, bool)` (unexported lookup).
  - `ErrKindNotRegistered` sentinel + loud message used by `fromWire`.
- Consumes: `NodeWire`, `Base` from Task 2.

- [ ] **Step 1: Write failing registry tests**

`definition/registry_test.go`:
```go
func TestRegisterKindAndLookup(t *testing.T) {
	s, ok := specFor(KindServiceTask)
	assert(t, ok, "serviceTask registered")
	assert(t, s.Name == "serviceTask", "name")
}
func TestFromWireUnregisteredKindIsLoud(t *testing.T) {
	_, err := fromWire(NodeWire{ID: "x", Kind: NodeKind(9999)})
	assert(t, err != nil && errors.Is(err, ErrKindNotRegistered), "loud error")
}
```

- [ ] **Step 2: Run — expect FAIL**

Run: `go test ./definition/ -run 'Register|FromWireUnregistered'`
Expected: compile error `undefined: specFor/RegisterKind/ErrKindNotRegistered`.

- [ ] **Step 3: Implement the registry, register all kinds in `definition` for now**

In `registry.go`, define the types + `nodeRegistry map[NodeKind]NodeSpec` +
`RegisterKind` + `specFor` + `ErrKindNotRegistered`. Add an `init()` in
`definition` that registers **all 19 kinds** (temporarily — moved to leaves in
Phase 3) by wrapping the existing `toWire`/`fromWire` per-kind logic into
`NodeSpec` closures. Rewrite `toWire(n)` to `specFor(n.Kind()).ToWire(...)` and
`fromWire(w)` to look up `specFor(w.Kind)` and return `ErrKindNotRegistered` on
miss. Derive `nodeKindNames` from `nodeRegistry` (plus `KindUnspecified`).

- [ ] **Step 4: Run registry + serialization tests**

Run: `go test ./definition/...` — Expected: PASS (round-trip unchanged).

- [ ] **Step 5: Add the all-kinds round-trip guard**

`definition/registry_test.go`:
```go
func TestAllKindsRoundTrip(t *testing.T) {
	for k := KindStartEvent; k <= KindEventBasedGateway; k++ {
		_, ok := specFor(k)
		assert(t, ok, k.String()+" must be registered")
	}
}
```
Run: `go test ./definition/ -run TestAllKindsRoundTrip` — Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add definition
git commit -m "refactor(definition): route serialization through a per-kind registry

toWire/fromWire and NodeKind names now resolve via nodeRegistry (all kinds
registered in-package for now); unregistered kinds fail loudly. Prepares the
leaf relocation."
```

### Task 5: Delete the duplicate `nodeYAML` struct

**Files:**
- Modify: `definition/yaml.go` (decode through `NodeWire`; keep only the two
  divergent fields — `Kind` as string, nested `subprocess` — via a thin shim).
- Test: `definition/yaml_test.go` (existing).

- [ ] **Step 1: Run YAML tests (green before)** — `go test ./definition/ -run YAML`.

- [ ] **Step 2: Replace `nodeYAML`**

Give `NodeWire` yaml tags (matching the current `nodeYAML` tags) OR keep a minimal
`nodeYAML` holding only `Kind string` + `Subprocess *definitionYAML` + an embedded
`NodeWire` for the rest; convert to `NodeWire` without the 25-field manual copy,
then reuse `fromWire`. Delete `fromNodeYAML`'s field-by-field block.

- [ ] **Step 3: Run YAML tests (green after)** — `go test ./definition/...` — PASS.

- [ ] **Step 4: Hoist validate.go per-call maps**

Move `gatewayKinds` and `errorBoundaryHostKinds` to package-level `var`s beside
`activityKinds`. Run `go test ./definition/...` — PASS.

- [ ] **Step 5: Commit**

```bash
git add definition
git commit -m "refactor(definition): drop duplicate nodeYAML union; hoist validate maps"
```

---

## Phase 3 — Relocate structs into leaf packages

Move each kind's struct + `Kind()` + constructor + options + `init()` registration
into its family package. `definition` keeps `Node`, machinery, registry, embeds.

### Task 6: Create `definition/gateway` (simplest family first)

**Files:**
- Create: `definition/gateway/gateway.go`, `definition/gateway/gateway_test.go`.
- Modify: `definition/node.go`, `definition/node_constructors.go` (remove the four
  gateway structs/constructors), `definition/registry.go` (remove their in-package
  registration).

**Interfaces:**
- Produces: `gateway.ExclusiveGateway/ParallelGateway/InclusiveGateway/EventBasedGateway`
  (each embeds `definition.Base`, has `Kind()`), and
  `gateway.NewExclusive(id string, name ...string) definition.Node`, `NewParallel`,
  `NewInclusive`, `NewEventBased`. `init()` registers all four via
  `definition.RegisterKind`.
- Consumes: `definition.Base`, `definition.NewBase`, `definition.NodeWire`,
  `definition.RegisterKind`, `definition.KindExclusiveGateway`, ….

- [ ] **Step 1: Write failing black-box test**

`definition/gateway/gateway_test.go`:
```go
package gateway_test

func TestNewExclusive(t *testing.T) {
	n := gateway.NewExclusive("gw", "Choice")
	if n.Kind() != definition.KindExclusiveGateway || n.ID() != "gw" || n.Name() != "Choice" {
		t.Fatalf("got %+v", n)
	}
}
```

- [ ] **Step 2: Run — expect FAIL** (`go test ./definition/gateway/`) — undefined `gateway`.

- [ ] **Step 3: Implement `gateway.go`**

Define the four structs embedding `definition.Base`, their `Kind()` methods, the
four constructors (using `definition.NewBase(id, optName(name))`), and `init()`
registering each with a `NodeSpec` whose `FromWire`/`ToWire` only touch `Base`
(gateways carry no extra fields).

- [ ] **Step 4: Remove the gateway definitions from `definition`**

Delete the four structs/`Kind()` from `node.go`, the four `NewXGateway` from
`node_constructors.go`, and their in-package registry entries from Task 4.

- [ ] **Step 5: Build + test**

Run:
```bash
go build ./definition/... 2>&1 | head       # definition compiles without gateways
go test ./definition/gateway/
go test ./definition/...                     # in-package refs to gateways now gone;
                                             # any that remain are compile errors to fix
```
Expected: green. (The `definition` package's own tests that constructed gateways
move to Phase 5 rewrite or are updated to use `gateway.New*` here if they live in
`definition_test`.)

- [ ] **Step 6: Commit**

```bash
git add definition
git commit -m "refactor(definition): relocate gateways into definition/gateway"
```

### Task 7: Create `definition/activity`

**Files:**
- Create: `definition/activity/{servicetask,usertask,receivetask,sendtask,businessruletask,subprocess,callactivity}.go` + paired `_test.go` (per the test-file-naming convention).
- Create: `definition/activity/options.go` (the activity option system).
- Modify: `definition` — remove the seven activity structs, their constructors, and
  the activity option machinery.

**Interfaces:**
- Produces: `activity.ServiceTask/UserTask/ReceiveTask/SendTask/BusinessRuleTask/
  SubProcess/CallActivity` (embedding `definition.Base` + `definition.ActivityFields`),
  constructors `activity.NewServiceTask(id, ...ServiceTaskOption) definition.Node`,
  `NewUserTask(id, roles, ...UserTaskOption)`, `NewReceiveTask(id, msg, ...)`,
  `NewSendTask(id, msg, ...)`, `NewBusinessRuleTask(id, ...)`,
  `NewSubProcess(id, *definition.ProcessDefinition, ...)`,
  `NewCallActivity(id, defRef, ...)`; options `activity.WithName`, `WithActionName`,
  `WithAction`, `WithActionFunc`, `WithRetryPolicy`, `WithRecoveryFlow`,
  `WithCompensation`, `WithCancelHandler`, `WithDeadline`, `WithReminder`,
  `WithEligibilityExpr`, `WithEligibilityPrivileges`, `WithCorrelationKey`; and the
  exported option interface types (`ServiceTaskOption`, `UserTaskOption`,
  `ReceiveTaskOption`, `SendTaskOption`, `BusinessRuleOption`, `ActivityOption`).
  `init()` registers all seven kinds.
- Consumes: `definition.Base/ActivityFields/NodeWire/RegisterKind/Kind*`,
  `action.ServiceAction`.

- [ ] **Step 1: Write failing black-box tests** (one representative per kind + option coverage)

`definition/activity/servicetask_test.go`:
```go
func TestNewServiceTaskWithActionName(t *testing.T) {
	n := activity.NewServiceTask("charge", activity.WithActionName("charge-card"), activity.WithName("Charge"))
	if n.Kind() != definition.KindServiceTask || definition.ActionOf(n) != "charge-card" || n.Name() != "Charge" {
		t.Fatalf("got %+v", n)
	}
}
```
Add analogous failing tests for user/receive/send/businessrule/subprocess/callactivity
and for `WithDeadline`/`WithReminder`/`WithRetryPolicy` via the `definition.*Of`
accessors.

- [ ] **Step 2: Run — expect FAIL** (`go test ./definition/activity/`).

- [ ] **Step 3: Implement the structs, options, constructors, registration**

Port `node.go` activity structs (embedding the exported embeds), the option
interfaces/impls from `node_constructors.go` (now exported types, package
`activity`), the seven constructors, and `init()` registering each kind's
`NodeSpec` (FromWire/ToWire projecting `Action`, `CandidateRoles`, `MessageName`,
`Subprocess`, `DefRef`, etc. + `w.Activity()`/`PutActivity`).

- [ ] **Step 4: Remove activity definitions from `definition`** (structs, constructors, option machinery, in-package registration).

- [ ] **Step 5: Build + test**

Run: `go build ./definition/... && go test ./definition/activity/ ./definition/...`
Expected: green (fix any residual in-package references by moving those tests to
Phase 5 scope or updating to `activity.New*`).

- [ ] **Step 6: Commit** — `git commit -m "refactor(definition): relocate activities into definition/activity"`.

### Task 8: Create `definition/event`

**Files:**
- Create: `definition/event/{startevent,endevent,terminateend,errorend,catch,throw,boundary,eventsubprocess}.go` + paired `_test.go`.
- Create: `definition/event/options.go`.
- Modify: `definition` — remove the eight event structs, constructors, option machinery.

**Interfaces:**
- Produces: `event.StartEvent/EndEvent/TerminateEndEvent/ErrorEndEvent/
  IntermediateCatchEvent/IntermediateThrowEvent/BoundaryEvent/EventSubProcess`;
  constructors `event.NewStart(id, ...StartOption)`, `NewEnd(id, name...)`,
  `NewTerminateEnd(id, name...)`, `NewErrorEnd(id, code, name...)`,
  `NewCatch(id, ...CatchOption)`, `NewThrow(id, ...ThrowOption)`,
  `NewBoundary(id, host, ...BoundaryOption)`,
  `NewEventSubProcess(id, *definition.ProcessDefinition, ...EventSubProcessOption)`;
  options `event.WithName`, `WithStartSignal/Message/Timer`,
  `WithCatchTimer/Signal/Message/Deadline/Reminder`,
  `WithThrowSignal/CompensateRef/ThrowName`,
  `WithBoundaryTimer/Signal/Message/ErrorCode/NonInterrupting`,
  `WithEventSubProcessNonInterrupting`; exported option interface types. `init()`
  registers all eight kinds.
- Consumes: `definition.Base/WaitFields/NodeWire/RegisterKind/Kind*`.

- [ ] **Step 1: Write failing black-box tests** (representative per kind + the renamed options)

`definition/event/catch_test.go`:
```go
func TestNewCatchWithRenamedOptions(t *testing.T) {
	n := event.NewCatch("wait",
		event.WithCatchTimer("1h"),
		event.WithCatchDeadline("2h", "esc", "notify"),
		event.WithCatchReminder("30m", "ping"))
	d, f, a := definition.DeadlineOf(n)
	if n.Kind() != definition.KindIntermediateCatchEvent || d != "2h" || f != "esc" || a != "notify" {
		t.Fatalf("got %+v (%s,%s,%s)", n, d, f, a)
	}
}
func TestNewBoundaryNonInterrupting(t *testing.T) {
	n := event.NewBoundary("b", "task", event.WithBoundaryNonInterrupting()).(event.BoundaryEvent)
	if !n.NonInterrupting { t.Fatal("want non-interrupting") }
}
```

- [ ] **Step 2: Run — expect FAIL** (`go test ./definition/event/`).

- [ ] **Step 3: Implement structs, options (with the renames), constructors, registration.**

`IntermediateCatchEvent` embeds `definition.WaitFields`. Register all eight kinds'
`NodeSpec`s (Start signal/message/timer, error code, boundary fields, ESP
subprocess+nonInterrupting, ICE timer/signal/message + wait fields).

- [ ] **Step 4: Remove event definitions from `definition`.**

- [ ] **Step 5: Build + test** — `go build ./definition/... && go test ./definition/event/ ./definition/...` — green.

- [ ] **Step 6: Commit** — `git commit -m "refactor(definition): relocate events into definition/event (with option renames)"`.

### Task 9: `definition/kinds` bundle + serialization guarantee

**Files:**
- Create: `definition/kinds/kinds.go` (blank-imports the three leaves),
  `definition/kinds/roundtrip_test.go`.

**Interfaces:**
- Produces: importing `definition/kinds` populates the registry for all 19 kinds.

- [ ] **Step 1: Write the bundle**

```go
// Package kinds registers every node kind so definitions deserialize.
package kinds

import (
	_ "github.com/kartaladev/wrkflw/definition/activity"
	_ "github.com/kartaladev/wrkflw/definition/event"
	_ "github.com/kartaladev/wrkflw/definition/gateway"
)
```

- [ ] **Step 2: Write the golden round-trip + all-kinds test**

`definition/kinds/roundtrip_test.go` (black-box, imports `kinds` for its side
effects + the leaves to build a definition): unmarshal
`../../definition/testdata/golden_definition.json` (moved fixture), assert it
re-marshals byte-identically; and assert all 19 `NodeKind`s resolve `specFor`.

- [ ] **Step 3: Run — expect FAIL then implement/fix** until:

Run: `go test ./definition/kinds/` — Expected: PASS (golden bytes identical).

- [ ] **Step 4: Commit** — `git commit -m "feat(definition): add kinds bundle + golden round-trip guarantee"`.

---

## Phase 4 — `definition/build` fluent wrapper

### Task 10: Fluent builder package

**Files:**
- Create: `definition/build/build.go`, `definition/build/build_test.go`.
- Delete: the old fluent file now in `definition/builder_fluent.go` (+ its test),
  whose `AddX` methods can no longer live in `definition`.

**Interfaces:**
- Produces: `build.New(id string, version int) *Builder`; `Builder` wraps
  `definition.DefinitionBuilder`; one `AddX` per kind mirroring the leaf
  constructor (`AddStart`, `AddEnd`, `AddTerminateEnd`, `AddErrorEnd`,
  `AddExclusive`, `AddParallel`, `AddInclusive`, `AddEventBased`, `AddServiceTask`,
  `AddUserTask`, `AddReceiveTask`, `AddSendTask`, `AddBusinessRuleTask`,
  `AddSubProcess`, `AddCallActivity`, `AddCatch`, `AddThrow`, `AddBoundary`,
  `AddEventSubProcess`); plus pass-through `Connect`, `RegisterAction`,
  `RegisterActionFunc`, `CancelActions`, `Build`, `Loader`.
- Consumes: the leaf constructors + `definition.New`.

- [ ] **Step 1: Failing test**

```go
func TestBuildFluentChain(t *testing.T) {
	def, err := build.New("order", 1).
		AddStart("s").
		AddServiceTask("charge", activity.WithActionName("charge-card")).
		AddEnd("e").
		Connect("s", "charge").Connect("charge", "e").
		Build()
	if err != nil { t.Fatal(err) }
	if _, ok := def.Node("charge"); !ok { t.Fatal("missing node") }
}
```

- [ ] **Step 2: Run — expect FAIL** (`go test ./definition/build/`).

- [ ] **Step 3: Implement `Builder`** — each `AddX` is
  `func (b *Builder) AddServiceTask(id string, opts ...activity.ServiceTaskOption) *Builder { b.inner.Add(activity.NewServiceTask(id, opts...)); return b }`.

- [ ] **Step 4: Delete `definition/builder_fluent.go` + `builder_fluent_test.go`.**

- [ ] **Step 5: Run** — `go test ./definition/build/ ./definition/...` — PASS.

- [ ] **Step 6: Commit** — `git commit -m "feat(definition): add definition/build fluent wrapper; drop in-package AddX"`.

---

## Phase 5 — Repo-wide call-site migration

The compiler drives this: every unmigrated reference is a build error.

### Task 11: Rewrite constructor call sites to the family packages

**Files:** every non-`definition` `.go` referencing the old `definition.NewXEvent/
Gateway/Task` constructors and options (~1,800 sites, 158 files).

- [ ] **Step 1: Mechanical replacement of constructor selectors**

Apply the naming map (Spec §3.1/§3.2) with scripted, reviewed `sed`, one kind at a
time, adding the needed leaf import per file. E.g.:
```bash
grep -rl 'definition\.NewServiceTask' --include='*.go' . \
  | xargs sed -i '' 's/definition\.NewServiceTask/activity.NewServiceTask/g'
# then goimports adds the activity import:
goimports -w $(grep -rl 'activity\.New' --include='*.go' . )
```
Repeat for every constructor + option in the map (`NewStartEvent`→`event.NewStart`,
`WithICEDeadline`→`event.WithCatchDeadline`, …). Fluent `NewDefinition(...).AddX`
chains → `build.New(...).AddX`.

- [ ] **Step 2: Iterate build until green**

Run repeatedly, fixing missing imports / stragglers:
```bash
go build ./... 2>&1 | head -40
```
Expected end state: no output.

- [ ] **Step 3: Wire `definition/kinds` into deserialization paths**

Add a blank import of `definition/kinds` to the persistence store package and any
REST/gRPC decode entry point that unmarshals `ProcessDefinition` without importing
a leaf. Verify:
```bash
grep -rl 'UnmarshalJSON\|ParseYAML\|LoadYAML\|json.Unmarshal' --include='*.go' internal engine runtime | sort -u
```
Ensure each such package (transitively) imports `kinds`.

- [ ] **Step 4: Full test run**

Run:
```bash
go test ./... 2>&1 | tail -40
```
Expected: all `ok`. Fix any test that asserted old names/behavior (names only —
behavior is unchanged).

- [ ] **Step 5: Commit** — `git commit -m "refactor: migrate all call sites to definition family packages"`.

---

## Phase 6 — Examples, docs, ADR, final gates

### Task 12: Examples + READMEs + ADR-0090

**Files:**
- Modify: `examples/**` to the new API (showcase `build` + leaf packages).
- Modify: `definition/README.md`, root `README.md`.
- Create: `docs/adr/0090-definition-package-node-family-relocation.md` (Nygard).

- [ ] **Step 1: Migrate `examples/`** to `build.New(...)` + `event.`/`activity.`/
  `gateway.` constructors + `kinds` where they load from JSON/YAML. Run
  `go build ./examples/... && go vet ./examples/...`.

- [ ] **Step 2: Rewrite `definition/README.md`** — the node-family tables now point
  at `event`/`gateway`/`activity`; document the `build` fluent package, the `kinds`
  bundle + the deserialization import rule, and the option renames. Update root
  `README.md` snippets.

- [ ] **Step 3: Write ADR-0090** (Status/Date, Context, Decision, Consequences) —
  the add-a-kind tax + cognitive load (Context); the `definition` rename + family
  packages + driver-registration + no-compat (Decision); the breaking migration,
  the deserialization import rule, and the eliminated parallel-maintenance sites
  (Consequences). Commit.

### Task 13: Final verification

- [ ] **Step 1: Full gates**

Run:
```bash
go build ./...
go test -race -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1
golangci-lint run ./...
```
Expected: build clean; tests green under `-race`; each touched package ≥ 85%;
lint clean.

- [ ] **Step 2: Import-cycle / layering check**

Run:
```bash
go list -deps ./definition | grep -E 'definition/(event|gateway|activity|build|kinds)' && echo "CYCLE!" || echo "definition imports no leaf — OK"
```
Expected: `definition imports no leaf — OK`.

- [ ] **Step 3: Golden-format check** — `go test ./definition/kinds/ -run RoundTrip` —
  golden JSON byte-identical.

- [ ] **Step 4: Finish the branch** — via `superpowers:finishing-a-development-branch`
  (merge `refactor/definition-relocation` → `main`, `--no-ff`).

---

## Self-review notes

- **Spec coverage:** §2 decisions → Tasks 1 (rename), 6–8 (families), 4/9 (registry
  + guarantee), 10 (build), 11 (no-compat migration). §3 layout → Tasks 6–10. §3.1/
  §3.2 naming → Tasks 6–8, 11. §4 registration/cycle-break → Tasks 4, 6–9, 13-step2.
  §4.1 correctness → Tasks 4-step5, 9, 13-step3. §5 internal simplifications →
  Tasks 2, 3, 5. §6 builder → Task 10. §8 phases → Phases 0–6. §9 checklist →
  Task 13. All covered.
- **Placeholder scan:** no TBD/TODO; each code step shows real code or an exact
  command. The two bulk phases (1, 5) are explicitly scripted-with-gates because
  per-file steps for 158 files would be noise — the compiler is the stated oracle.
- **Type consistency:** `NodeSpec{Name,FromWire,ToWire}`, `Base`, `WaitFields`,
  `ActivityFields`, `NodeWire.Activity()/PutActivity`, `RegisterKind`, `specFor`,
  `ErrKindNotRegistered`, and the leaf constructor/option names are used
  consistently across Tasks 2, 4, 6–10.

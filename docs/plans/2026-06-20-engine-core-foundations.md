# Engine Core Foundations (Plan 1 of 5) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stand up the pure `wrkflw` engine core far enough to run a linear process (Start → ServiceTask → End) end-to-end through a reference runtime.

**Architecture:** A pure stepper (`engine.Step`) maps `(definition, state, Trigger) → (state, []Command)` with no I/O, no clock reads, no goroutines (ADR-0002). A thin `runtime` loop performs commands and feeds results back as triggers. Time enters only as `Trigger.OccurredAt`, produced from `clock.Clock` (ADR-0003). Public packages live at the module root (ADR-0004).

**Tech Stack:** Go 1.25, `github.com/stretchr/testify` for assertions. No DB, no transport, no clockwork in this plan (those arrive in later plans).

## Global Constraints

- Go **1.25** (hard requirement; `go.mod` declares `go 1.25`).
- Module path: **`github.com/kartaladev/wrkflw`** (ADR-0004); public packages at the module root, **no `pkg/` prefix**.
- The core (`engine/`, `model/`) imports **no** transport/storage/bus/time-vendor packages, and **never calls `time.Now()`** — time enters as `Trigger.OccurredAt` (ADR-0002/0003).
- `Step` is **deterministic**: identical `(state, trigger)` ⇒ identical `(state, commands)`. CommandID/Token IDs derive from in-state counters, never from randomness or the clock.
- `Step` is **pure**: it must not mutate its input `InstanceState`; it returns a new value.
- Tests are **black-box** (`package <pkg>_test`). Use table-driven tests with an **`assert` closure per case** (not `want`/`wantErr` fields) and `t.Context()` where a context is needed (project `table-test` skill).
- Conventional Commits scoped to the area; commit after each green step group.

---

## File Structure

```
go.mod                          # module github.com/kartaladev/wrkflw, go 1.25
.golangci.yml                   # baseline linter config
model/
  definition.go                 # ProcessDefinition, Node, NodeKind, SequenceFlow + lookups
  validate.go                   # Validate() + sentinel errors
  definition_test.go            # (black-box) lookups
  validate_test.go              # (black-box) malformed-graph table
clock/
  clock.go                      # Clock interface + System()
  clock_test.go
engine/
  trigger.go                    # Trigger sealed interface + StartInstance/ActionCompleted/ActionFailed
  command.go                    # Command sealed interface + InvokeAction/CompleteInstance/FailInstance
  state.go                      # InstanceState, Token, NodeVisit, Status, TokenState, counters
  step.go                       # Step() + internal drive loop
  trigger_test.go
  command_test.go
  step_test.go                  # (black-box) linear flow
action/
  action.go                     # ServiceAction + Catalog interfaces + MapCatalog
  action_test.go
runtime/
  ports.go                      # StateStore, Journal, OutboxWriter interfaces
  memory.go                     # in-memory implementations of the ports
  runner.go                     # Runner.Run loop
  example_test.go               # (black-box) end-to-end linear process
```

Each task below is self-contained and ends with a green test and a commit.

---

### Task 1: Module init + tooling

**Files:**
- Create: `go.mod`
- Create: `.golangci.yml`

- [ ] **Step 1: Initialize the module**

Run:
```bash
cd /Users/zakyalvan/Documents/RND/wrkflw
go mod init github.com/kartaladev/wrkflw
```
Expected: creates `go.mod` containing `module github.com/kartaladev/wrkflw` and a `go 1.25` line.

- [ ] **Step 2: Pin the Go version line**

Open `go.mod` and ensure it reads exactly:
```
module github.com/kartaladev/wrkflw

go 1.25
```

- [ ] **Step 3: Add a baseline linter config**

Create `.golangci.yml` (golangci-lint **v2** schema — the installed version is 2.x):
```yaml
version: "2"
run:
  timeout: 5m
linters:
  default: standard   # errcheck, govet, ineffassign, staticcheck, unused
```

- [ ] **Step 4: Verify the module builds**

Run:
```bash
go build ./...
```
Expected: no output, exit 0 (nothing to build yet).

- [ ] **Step 5: Commit**

```bash
git add go.mod .golangci.yml
git commit -m "chore(module): init go module and baseline linter config"
```

---

### Task 2: Definition model types

**Files:**
- Create: `model/definition.go`
- Test: `model/definition_test.go`

**Interfaces:**
- Produces: `model.NodeKind` (int enum with `KindStartEvent`, `KindEndEvent`, `KindServiceTask`, … full set), `model.Node{ID string; Kind NodeKind; Name string; Action string}`, `model.SequenceFlow{ID, Source, Target, Condition string; IsDefault bool}`, `model.ProcessDefinition{ID string; Version int; Nodes []Node; Flows []SequenceFlow}`, and methods `(*ProcessDefinition).Node(id string) (Node, bool)`, `.Outgoing(nodeID string) []SequenceFlow`, `.Incoming(nodeID string) []SequenceFlow`, `.StartNodes() []Node`.

- [ ] **Step 1: Write the failing test**

Create `model/definition_test.go`:
```go
package model_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/model"
)

func linearDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID:      "p1",
		Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "greet", Kind: model.KindServiceTask, Action: "greet"},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "greet"},
			{ID: "f2", Source: "greet", Target: "end"},
		},
	}
}

func TestProcessDefinitionLookups(t *testing.T) {
	d := linearDef()

	n, ok := d.Node("greet")
	require.True(t, ok)
	assert.Equal(t, model.KindServiceTask, n.Kind)

	_, ok = d.Node("missing")
	assert.False(t, ok)

	out := d.Outgoing("start")
	require.Len(t, out, 1)
	assert.Equal(t, "greet", out[0].Target)

	in := d.Incoming("end")
	require.Len(t, in, 1)
	assert.Equal(t, "greet", in[0].Source)

	starts := d.StartNodes()
	require.Len(t, starts, 1)
	assert.Equal(t, "start", starts[0].ID)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:
```bash
go test ./model/... -run TestProcessDefinitionLookups -v
```
Expected: FAIL — build error, `undefined: model.ProcessDefinition` (testify also needs fetching).

- [ ] **Step 3: Add the testify dependency**

Run:
```bash
go get github.com/stretchr/testify@v1.9.0
```
Expected: adds testify to `go.mod`/`go.sum`.

- [ ] **Step 4: Write minimal implementation**

Create `model/definition.go`:
```go
// Package model defines the in-memory BPMN-flavored process-definition types.
// It is pure data plus validation; it imports only the standard library.
package model

// NodeKind discriminates the kind of a Node.
type NodeKind int

const (
	KindUnspecified NodeKind = iota
	KindStartEvent
	KindEndEvent
	KindTerminateEndEvent
	KindErrorEndEvent
	KindServiceTask
	KindUserTask
	KindReceiveTask
	KindSendTask
	KindBusinessRuleTask
	KindSubProcess
	KindCallActivity
	KindEventSubProcess
	KindIntermediateCatchEvent
	KindIntermediateThrowEvent
	KindBoundaryEvent
	KindExclusiveGateway
	KindParallelGateway
	KindInclusiveGateway
	KindEventBasedGateway
)

// Node is a single point in a process: an event, activity, or gateway.
// Kind-specific fields beyond those below are added in later plans.
type Node struct {
	ID     string
	Kind   NodeKind
	Name   string
	Action string // service-action name, for KindServiceTask
}

// SequenceFlow is a directed edge between two nodes.
type SequenceFlow struct {
	ID        string
	Source    string
	Target    string
	Condition string // expr; empty means unconditional
	IsDefault bool
}

// ProcessDefinition is the reusable template a process instance executes.
type ProcessDefinition struct {
	ID      string
	Version int
	Nodes   []Node
	Flows   []SequenceFlow
}

// Node returns the node with the given id.
func (d *ProcessDefinition) Node(id string) (Node, bool) {
	for _, n := range d.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return Node{}, false
}

// Outgoing returns the sequence flows leaving nodeID.
func (d *ProcessDefinition) Outgoing(nodeID string) []SequenceFlow {
	var out []SequenceFlow
	for _, f := range d.Flows {
		if f.Source == nodeID {
			out = append(out, f)
		}
	}
	return out
}

// Incoming returns the sequence flows entering nodeID.
func (d *ProcessDefinition) Incoming(nodeID string) []SequenceFlow {
	var in []SequenceFlow
	for _, f := range d.Flows {
		if f.Target == nodeID {
			in = append(in, f)
		}
	}
	return in
}

// StartNodes returns all start-event nodes.
func (d *ProcessDefinition) StartNodes() []Node {
	var starts []Node
	for _, n := range d.Nodes {
		if n.Kind == KindStartEvent {
			starts = append(starts, n)
		}
	}
	return starts
}
```

- [ ] **Step 5: Run test to verify it passes**

Run:
```bash
go test ./model/... -run TestProcessDefinitionLookups -v
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum model/definition.go model/definition_test.go
git commit -m "feat(model): process-definition types and lookups"
```

---

### Task 3: Definition validation

**Files:**
- Create: `model/validate.go`
- Test: `model/validate_test.go`

**Interfaces:**
- Consumes: `model.ProcessDefinition` and its lookups (Task 2).
- Produces: `func model.Validate(d *ProcessDefinition) error`; sentinel errors `ErrNoStartEvent`, `ErrMultipleStartEvents`, `ErrDanglingFlow`, `ErrDeadEnd`, `ErrStartHasIncoming`, `ErrEndHasOutgoing`.

- [ ] **Step 1: Write the failing test**

Create `model/validate_test.go`:
```go
package model_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/model"
)

func TestValidate(t *testing.T) {
	tests := map[string]struct {
		def    *model.ProcessDefinition
		assert func(t *testing.T, err error)
	}{
		"valid linear": {
			def: linearDef(),
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
		"no start event": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{{ID: "end", Kind: model.KindEndEvent}},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrNoStartEvent)
			},
		},
		"multiple start events": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "s1", Kind: model.KindStartEvent},
					{ID: "s2", Kind: model.KindStartEvent},
					{ID: "end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "s1", Target: "end"},
					{ID: "f2", Source: "s2", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrMultipleStartEvents)
			},
		},
		"dangling flow target": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "start", Target: "ghost"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrDanglingFlow)
			},
		},
		"dead end non-end node": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "task", Kind: model.KindServiceTask, Action: "x"},
					{ID: "end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "start", Target: "task"},
					// task has no outgoing → dead end
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrDeadEnd)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			tc.assert(t, model.Validate(tc.def))
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:
```bash
go test ./model/... -run TestValidate -v
```
Expected: FAIL — `undefined: model.Validate`.

- [ ] **Step 3: Write minimal implementation**

Create `model/validate.go`:
```go
package model

import (
	"errors"
	"fmt"
)

var (
	ErrNoStartEvent        = errors.New("model: no start event")
	ErrMultipleStartEvents = errors.New("model: multiple start events")
	ErrDanglingFlow        = errors.New("model: flow references unknown node")
	ErrDeadEnd             = errors.New("model: non-end node has no outgoing flow")
	ErrStartHasIncoming    = errors.New("model: start event has incoming flow")
	ErrEndHasOutgoing      = errors.New("model: end event has outgoing flow")
)

// Validate checks structural well-formedness of a process definition. It
// returns a joined error covering every violation found.
func Validate(d *ProcessDefinition) error {
	var errs []error

	starts := d.StartNodes()
	switch {
	case len(starts) == 0:
		errs = append(errs, ErrNoStartEvent)
	case len(starts) > 1:
		errs = append(errs, fmt.Errorf("%w: %d found", ErrMultipleStartEvents, len(starts)))
	}

	for _, f := range d.Flows {
		if _, ok := d.Node(f.Source); !ok {
			errs = append(errs, fmt.Errorf("%w: flow %q source %q", ErrDanglingFlow, f.ID, f.Source))
		}
		if _, ok := d.Node(f.Target); !ok {
			errs = append(errs, fmt.Errorf("%w: flow %q target %q", ErrDanglingFlow, f.ID, f.Target))
		}
	}

	for _, n := range d.Nodes {
		isEnd := n.Kind == KindEndEvent || n.Kind == KindTerminateEndEvent || n.Kind == KindErrorEndEvent
		out := d.Outgoing(n.ID)
		in := d.Incoming(n.ID)

		if !isEnd && len(out) == 0 {
			errs = append(errs, fmt.Errorf("%w: node %q", ErrDeadEnd, n.ID))
		}
		if n.Kind == KindStartEvent && len(in) > 0 {
			errs = append(errs, fmt.Errorf("%w: node %q", ErrStartHasIncoming, n.ID))
		}
		if isEnd && len(out) > 0 {
			errs = append(errs, fmt.Errorf("%w: node %q", ErrEndHasOutgoing, n.ID))
		}
	}

	return errors.Join(errs...)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run:
```bash
go test ./model/... -v
```
Expected: PASS (all subtests).

- [ ] **Step 5: Commit**

```bash
git add model/validate.go model/validate_test.go
git commit -m "feat(model): structural validation with sentinel errors"
```

---

### Task 4: Clock port

**Files:**
- Create: `clock/clock.go`
- Test: `clock/clock_test.go`

**Interfaces:**
- Produces: `clock.Clock` interface (`Now() time.Time`) and `clock.System() Clock`.

- [ ] **Step 1: Write the failing test**

Create `clock/clock_test.go`:
```go
package clock_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/kartaladev/wrkflw/clock"
)

func TestSystemClockNow(t *testing.T) {
	c := clock.System()
	before := time.Now()
	got := c.Now()
	after := time.Now()

	assert.False(t, got.Before(before), "Now() should not precede the call")
	assert.False(t, got.After(after), "Now() should not exceed the call")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:
```bash
go test ./clock/... -v
```
Expected: FAIL — `undefined: clock.System`.

- [ ] **Step 3: Write minimal implementation**

Create `clock/clock.go`:
```go
// Package clock is the engine's sole time abstraction (ADR-0003). Stateful
// components depend on Clock rather than calling time.Now() or importing a time
// vendor; clockwork.Clock satisfies this interface structurally.
package clock

import "time"

// Clock reports the current time.
type Clock interface {
	Now() time.Time
}

// System returns a Clock backed by the standard library.
func System() Clock { return systemClock{} }

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }
```

- [ ] **Step 4: Run test to verify it passes**

Run:
```bash
go test ./clock/... -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add clock/clock.go clock/clock_test.go
git commit -m "feat(clock): Clock interface and System() real clock"
```

---

### Task 5: Engine triggers (sealed set)

**Files:**
- Create: `engine/trigger.go`
- Test: `engine/trigger_test.go`

**Interfaces:**
- Produces: `engine.Trigger` interface (`isTrigger()`, `OccurredAt() time.Time`); constructors `NewStartInstance(at time.Time, vars map[string]any) StartInstance`, `NewActionCompleted(at time.Time, commandID string, output map[string]any) ActionCompleted`, `NewActionFailed(at time.Time, commandID, errMsg string, retryable bool) ActionFailed`; the corresponding struct types with exported fields (`StartInstance.Vars`, `ActionCompleted.CommandID/Output`, `ActionFailed.CommandID/Err/Retryable`).

- [ ] **Step 1: Write the failing test**

Create `engine/trigger_test.go`:
```go
package engine_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/kartaladev/wrkflw/engine"
)

func TestTriggersCarryOccurredAt(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)

	var trs []engine.Trigger = []engine.Trigger{
		engine.NewStartInstance(at, map[string]any{"x": 1}),
		engine.NewActionCompleted(at, "c1", map[string]any{"ok": true}),
		engine.NewActionFailed(at, "c1", "boom", true),
	}
	for _, tr := range trs {
		assert.Equal(t, at, tr.OccurredAt())
	}

	ac := engine.NewActionCompleted(at, "c1", map[string]any{"ok": true})
	assert.Equal(t, "c1", ac.CommandID)
	assert.Equal(t, true, ac.Output["ok"])
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:
```bash
go test ./engine/... -run TestTriggersCarryOccurredAt -v
```
Expected: FAIL — `undefined: engine.NewStartInstance`.

- [ ] **Step 3: Write minimal implementation**

Create `engine/trigger.go`:
```go
// Package engine is the pure token state machine (ADR-0002). Step maps
// (definition, state, Trigger) -> (state, []Command) with no I/O, no clock
// reads, and no goroutines.
package engine

import "time"

// Trigger is the sealed set of things that drive the next step: initiating
// causes and returning results. The unexported marker keeps the set closed.
type Trigger interface {
	isTrigger()
	OccurredAt() time.Time
}

type baseTrigger struct{ at time.Time }

func (b baseTrigger) OccurredAt() time.Time { return b.at }
func (baseTrigger) isTrigger()              {}

// StartInstance begins a new process instance with initial variables.
type StartInstance struct {
	baseTrigger
	Vars map[string]any
}

// ActionCompleted reports that a ServiceAction finished successfully.
type ActionCompleted struct {
	baseTrigger
	CommandID string
	Output    map[string]any
}

// ActionFailed reports that a ServiceAction failed.
type ActionFailed struct {
	baseTrigger
	CommandID string
	Err       string
	Retryable bool
}

func NewStartInstance(at time.Time, vars map[string]any) StartInstance {
	return StartInstance{baseTrigger: baseTrigger{at: at}, Vars: vars}
}

func NewActionCompleted(at time.Time, commandID string, output map[string]any) ActionCompleted {
	return ActionCompleted{baseTrigger: baseTrigger{at: at}, CommandID: commandID, Output: output}
}

func NewActionFailed(at time.Time, commandID, errMsg string, retryable bool) ActionFailed {
	return ActionFailed{baseTrigger: baseTrigger{at: at}, CommandID: commandID, Err: errMsg, Retryable: retryable}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run:
```bash
go test ./engine/... -run TestTriggersCarryOccurredAt -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add engine/trigger.go engine/trigger_test.go
git commit -m "feat(engine): sealed Trigger set with OccurredAt"
```

---

### Task 6: Engine commands (sealed set)

**Files:**
- Create: `engine/command.go`
- Test: `engine/command_test.go`

**Interfaces:**
- Produces: `engine.Command` interface (`isCommand()`); types `InvokeAction{CommandID, Name string; Input map[string]any}`, `CompleteInstance{Result map[string]any}`, `FailInstance{Err string}`.

- [ ] **Step 1: Write the failing test**

Create `engine/command_test.go`:
```go
package engine_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kartaladev/wrkflw/engine"
)

func TestCommandsImplementInterface(t *testing.T) {
	var cmds []engine.Command = []engine.Command{
		engine.InvokeAction{CommandID: "c1", Name: "greet", Input: map[string]any{"a": 1}},
		engine.CompleteInstance{Result: map[string]any{"done": true}},
		engine.FailInstance{Err: "boom"},
	}
	assert.Len(t, cmds, 3)

	ia, ok := cmds[0].(engine.InvokeAction)
	assert.True(t, ok)
	assert.Equal(t, "greet", ia.Name)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:
```bash
go test ./engine/... -run TestCommandsImplementInterface -v
```
Expected: FAIL — `undefined: engine.InvokeAction`.

- [ ] **Step 3: Write minimal implementation**

Create `engine/command.go`:
```go
package engine

// Command is the sealed set of side effects the core asks the runtime to
// perform. The unexported marker keeps the set closed.
type Command interface {
	isCommand()
}

// InvokeAction asks the runtime to run a named ServiceAction. Its result
// returns as an ActionCompleted/ActionFailed trigger carrying the same CommandID.
type InvokeAction struct {
	CommandID string
	Name      string
	Input     map[string]any
}

// CompleteInstance marks the instance complete with a result.
type CompleteInstance struct {
	Result map[string]any
}

// FailInstance marks the instance failed.
type FailInstance struct {
	Err string
}

func (InvokeAction) isCommand()     {}
func (CompleteInstance) isCommand() {}
func (FailInstance) isCommand()     {}
```

- [ ] **Step 4: Run test to verify it passes**

Run:
```bash
go test ./engine/... -run TestCommandsImplementInterface -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add engine/command.go engine/command_test.go
git commit -m "feat(engine): sealed Command set"
```

---

### Task 7: Instance state types

**Files:**
- Create: `engine/state.go`
- Test: (covered by Task 8's `step_test.go`; no separate test — these are pure data types with no behavior yet)

**Interfaces:**
- Produces: `engine.Status` (`StatusRunning`, `StatusCompleted`, `StatusFailed`, `StatusCompensating`, `StatusTerminated`), `engine.TokenState` (`TokenActive`, `TokenWaitingCommand`, `TokenAtJoin`), `engine.Token`, `engine.NodeVisit`, `engine.InstanceState` (with `CmdSeq`, `TokenSeq` counters for deterministic ID generation). Scopes/Tasks fields are intentionally deferred to later plans.

- [ ] **Step 1: Write the implementation (pure data, no behavior — exercised by Task 8)**

> Note: this task is pure type declarations with no behavior of its own; its red/green cycle is Task 8, whose `Step` test fails to compile until these types exist. Create the file, then proceed directly to Task 8 Step 2 to observe the red state.

Create `engine/state.go`:
```go
package engine

import "time"

// Status is the lifecycle state of a process instance.
type Status int

const (
	StatusRunning Status = iota
	StatusCompleted
	StatusFailed
	StatusCompensating
	StatusTerminated
)

// TokenState is the execution state of a single token.
type TokenState int

const (
	TokenActive TokenState = iota
	TokenWaitingCommand
	TokenAtJoin
)

// Token marks where execution currently sits and what it is waiting on.
type Token struct {
	ID           string
	NodeID       string
	ScopeID      string
	State        TokenState
	AwaitCommand string // CommandID this token is parked on, if any
	Payload      map[string]any
	EnteredAt    time.Time
}

// NodeVisit is one traversal of one node by one token (audit/history).
type NodeVisit struct {
	NodeID    string
	TokenID   string
	EnteredAt time.Time
	LeftAt    *time.Time
	ActorID   *string // who completed a human-task visit (later plans)
}

// InstanceState is the authoritative snapshot of a running instance.
// Scopes and human-task records are added in later plans.
type InstanceState struct {
	InstanceID string
	DefID      string
	DefVersion int
	Status     Status
	Variables  map[string]any
	Tokens     []Token
	StartedAt  time.Time
	EndedAt    *time.Time
	History    []NodeVisit

	// Deterministic ID counters (never randomness or the clock).
	CmdSeq   int
	TokenSeq int
}
```

- [ ] **Step 2: Verify it compiles**

Run:
```bash
go build ./engine/...
```
Expected: no output, exit 0.

- [ ] **Step 3: Commit**

```bash
git add engine/state.go
git commit -m "feat(engine): instance state, token, and history types"
```

---

### Task 8: Step — linear flow (Start → ServiceTask → End)

**Files:**
- Create: `engine/step.go`
- Test: `engine/step_test.go`

**Interfaces:**
- Consumes: `model.ProcessDefinition` (Task 2/3), all `engine` types (Tasks 5–7).
- Produces: `engine.StepMode` (`Macro`, `Micro`), `engine.StepOptions{Mode StepMode}`, `engine.StepResult{State InstanceState; Commands []Command}`, and `func engine.Step(def *model.ProcessDefinition, st InstanceState, trg Trigger, opt StepOptions) (StepResult, error)`. Sentinel `engine.ErrUnknownTrigger`, `engine.ErrTokenNotFound`.
- Note: `Micro` mode is accepted but behaves as `Macro` in this plan; true single-node stepping arrives in Plan 5.

- [ ] **Step 1: Write the failing test**

Create `engine/step_test.go`:
```go
package engine_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/model"
)

func linearDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "p1", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "greet", Kind: model.KindServiceTask, Action: "greet"},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "greet"},
			{ID: "f2", Source: "greet", Target: "end"},
		},
	}
}

func TestStepStartInstanceReachesServiceTask(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	def := linearDef()

	res, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, map[string]any{"name": "Ada"}), engine.StepOptions{})
	require.NoError(t, err)

	// One InvokeAction emitted for the service task; one token parked on it.
	require.Len(t, res.Commands, 1)
	ia, ok := res.Commands[0].(engine.InvokeAction)
	require.True(t, ok)
	assert.Equal(t, "greet", ia.Name)
	assert.Equal(t, "Ada", ia.Input["name"])

	require.Len(t, res.State.Tokens, 1)
	tok := res.State.Tokens[0]
	assert.Equal(t, "greet", tok.NodeID)
	assert.Equal(t, engine.TokenWaitingCommand, tok.State)
	assert.Equal(t, ia.CommandID, tok.AwaitCommand)
	assert.Equal(t, engine.StatusRunning, res.State.Status)
	assert.Equal(t, at, res.State.StartedAt)
}

func TestStepActionCompletedReachesEnd(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	def := linearDef()

	r1, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, map[string]any{"name": "Ada"}), engine.StepOptions{})
	require.NoError(t, err)
	cmdID := r1.Commands[0].(engine.InvokeAction).CommandID

	r2, err := engine.Step(def, r1.State,
		engine.NewActionCompleted(at.Add(time.Second), cmdID, map[string]any{"greeting": "hi Ada"}),
		engine.StepOptions{})
	require.NoError(t, err)

	require.Len(t, r2.Commands, 1)
	_, ok := r2.Commands[0].(engine.CompleteInstance)
	require.True(t, ok)
	assert.Equal(t, engine.StatusCompleted, r2.State.Status)
	assert.Empty(t, r2.State.Tokens)
	assert.Equal(t, "hi Ada", r2.State.Variables["greeting"])
	require.NotNil(t, r2.State.EndedAt)
}

func TestStepIsDeterministic(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	def := linearDef()
	in := engine.InstanceState{InstanceID: "i1"}
	trg := engine.NewStartInstance(at, map[string]any{"name": "Ada"})

	a, err := engine.Step(def, in, trg, engine.StepOptions{})
	require.NoError(t, err)
	b, err := engine.Step(def, in, trg, engine.StepOptions{})
	require.NoError(t, err)

	assert.Equal(t, a.Commands, b.Commands)
	assert.Equal(t, a.State, b.State)
}

func TestStepDoesNotMutateInput(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	def := linearDef()
	in := engine.InstanceState{InstanceID: "i1", Variables: map[string]any{"name": "Ada"}}

	_, err := engine.Step(def, in, engine.NewStartInstance(at, map[string]any{"extra": 1}), engine.StepOptions{})
	require.NoError(t, err)

	// Caller's state is untouched.
	assert.Empty(t, in.Tokens)
	assert.Equal(t, map[string]any{"name": "Ada"}, in.Variables)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:
```bash
go test ./engine/... -run TestStep -v
```
Expected: FAIL — `undefined: engine.Step` (and `engine.StepOptions`).

- [ ] **Step 3: Write minimal implementation**

Create `engine/step.go`:
```go
package engine

import (
	"errors"
	"fmt"
	"time"

	"github.com/kartaladev/wrkflw/model"
)

var (
	ErrUnknownTrigger = errors.New("engine: unknown trigger")
	ErrTokenNotFound  = errors.New("engine: no token awaiting command")
)

// StepMode selects how far one Step advances. Micro behaves as Macro in this
// plan; true single-node stepping arrives in Plan 5.
type StepMode int

const (
	Macro StepMode = iota
	Micro
)

type StepOptions struct{ Mode StepMode }

type StepResult struct {
	State    InstanceState
	Commands []Command
}

// Step applies one trigger to the instance state and returns the new state plus
// the commands the runtime must perform. It is pure: it does not mutate st.
func Step(def *model.ProcessDefinition, st InstanceState, trg Trigger, opt StepOptions) (StepResult, error) {
	s := cloneState(st)
	var cmds []Command

	switch t := trg.(type) {
	case StartInstance:
		s.Status = StatusRunning
		s.StartedAt = t.OccurredAt()
		s.DefID = def.ID
		s.DefVersion = def.Version
		mergeVars(&s, t.Vars)
		starts := def.StartNodes()
		if len(starts) != 1 {
			return StepResult{}, fmt.Errorf("engine: expected exactly one start, got %d", len(starts))
		}
		s.placeToken(starts[0].ID, t.OccurredAt())

	case ActionCompleted:
		tok := s.tokenAwaiting(t.CommandID)
		if tok == nil {
			return StepResult{}, fmt.Errorf("%w: %q", ErrTokenNotFound, t.CommandID)
		}
		mergeVars(&s, t.Output)
		tok.State = TokenActive
		tok.AwaitCommand = ""

	case ActionFailed:
		tok := s.tokenAwaiting(t.CommandID)
		if tok == nil {
			return StepResult{}, fmt.Errorf("%w: %q", ErrTokenNotFound, t.CommandID)
		}
		s.Status = StatusFailed
		ended := t.OccurredAt()
		s.EndedAt = &ended
		return StepResult{State: s, Commands: []Command{FailInstance{Err: t.Err}}}, nil

	default:
		return StepResult{}, fmt.Errorf("%w: %T", ErrUnknownTrigger, trg)
	}

	cmds = drive(def, &s, trg.OccurredAt())
	return StepResult{State: s, Commands: cmds}, nil
}

// drive advances all active tokens until each is parked or consumed (Macro).
func drive(def *model.ProcessDefinition, s *InstanceState, at time.Time) []Command {
	var cmds []Command
	for {
		tok := s.firstActive()
		if tok == nil {
			break
		}
		node, ok := def.Node(tok.NodeID)
		if !ok {
			// Defensive: a token on a missing node cannot advance.
			tok.State = TokenWaitingCommand
			continue
		}

		switch node.Kind {
		case model.KindStartEvent:
			s.moveAlongSingleFlow(def, tok, at)

		case model.KindServiceTask:
			cmdID := s.nextCommandID()
			cmds = append(cmds, InvokeAction{
				CommandID: cmdID,
				Name:      node.Action,
				Input:     copyVars(s.Variables),
			})
			tok.State = TokenWaitingCommand
			tok.AwaitCommand = cmdID

		case model.KindEndEvent:
			s.consumeToken(tok, at)
			if len(s.Tokens) == 0 {
				s.Status = StatusCompleted
				ended := at
				s.EndedAt = &ended
				cmds = append(cmds, CompleteInstance{Result: copyVars(s.Variables)})
			}

		default:
			// Node kinds beyond linear flow arrive in later plans; park the
			// token so the loop terminates rather than spinning.
			tok.State = TokenWaitingCommand
		}
	}
	return cmds
}

// ---- InstanceState helpers (unexported) ----

func (s *InstanceState) placeToken(nodeID string, at time.Time) {
	s.TokenSeq++
	id := fmt.Sprintf("%s-t%d", s.InstanceID, s.TokenSeq)
	s.Tokens = append(s.Tokens, Token{ID: id, NodeID: nodeID, State: TokenActive, EnteredAt: at})
	s.openVisit(id, nodeID, at)
}

func (s *InstanceState) firstActive() *Token {
	for i := range s.Tokens {
		if s.Tokens[i].State == TokenActive {
			return &s.Tokens[i]
		}
	}
	return nil
}

func (s *InstanceState) tokenAwaiting(cmdID string) *Token {
	for i := range s.Tokens {
		if s.Tokens[i].AwaitCommand == cmdID {
			return &s.Tokens[i]
		}
	}
	return nil
}

func (s *InstanceState) nextCommandID() string {
	s.CmdSeq++
	return fmt.Sprintf("%s-c%d", s.InstanceID, s.CmdSeq)
}

func (s *InstanceState) moveAlongSingleFlow(def *model.ProcessDefinition, tok *Token, at time.Time) {
	out := def.Outgoing(tok.NodeID)
	s.closeVisit(tok.ID, tok.NodeID, at)
	if len(out) == 0 {
		tok.State = TokenWaitingCommand // defensive; Validate forbids this
		return
	}
	tok.NodeID = out[0].Target
	tok.EnteredAt = at
	s.openVisit(tok.ID, tok.NodeID, at)
}

func (s *InstanceState) consumeToken(tok *Token, at time.Time) {
	s.closeVisit(tok.ID, tok.NodeID, at)
	id := tok.ID
	out := s.Tokens[:0]
	for _, t := range s.Tokens {
		if t.ID != id {
			out = append(out, t)
		}
	}
	s.Tokens = out
}

func (s *InstanceState) openVisit(tokenID, nodeID string, at time.Time) {
	s.History = append(s.History, NodeVisit{NodeID: nodeID, TokenID: tokenID, EnteredAt: at})
}

func (s *InstanceState) closeVisit(tokenID, nodeID string, at time.Time) {
	for i := len(s.History) - 1; i >= 0; i-- {
		v := &s.History[i]
		if v.TokenID == tokenID && v.NodeID == nodeID && v.LeftAt == nil {
			left := at
			v.LeftAt = &left
			return
		}
	}
}

// ---- value helpers ----

func mergeVars(s *InstanceState, in map[string]any) {
	if len(in) == 0 {
		return
	}
	if s.Variables == nil {
		s.Variables = make(map[string]any, len(in))
	}
	for k, v := range in {
		s.Variables[k] = v
	}
}

func copyVars(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneState(st InstanceState) InstanceState {
	s := st
	s.Variables = copyVars(st.Variables)
	s.Tokens = append([]Token(nil), st.Tokens...)
	s.History = append([]NodeVisit(nil), st.History...)
	if st.EndedAt != nil {
		e := *st.EndedAt
		s.EndedAt = &e
	}
	return s
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:
```bash
go test ./engine/... -v
```
Expected: PASS (all `TestStep*` plus earlier engine tests).

- [ ] **Step 5: Commit**

```bash
git add engine/step.go engine/step_test.go
git commit -m "feat(engine): Step macro-drives linear Start->ServiceTask->End"
```

---

### Task 9: ServiceAction catalog

**Files:**
- Create: `action/action.go`
- Test: `action/action_test.go`

**Interfaces:**
- Produces: `action.ServiceAction` interface (`Do(ctx, in) (out, err)`), `action.Catalog` interface (`Resolve(name) (ServiceAction, bool)`), `action.MapCatalog` (a `map[string]ServiceAction`-backed Catalog) with `action.NewMapCatalog(m map[string]ServiceAction) MapCatalog`, and `action.Func` (a func adapter implementing ServiceAction).

- [ ] **Step 1: Write the failing test**

Create `action/action_test.go`:
```go
package action_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/action"
)

func TestMapCatalogResolveAndRun(t *testing.T) {
	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"greet": action.Func(func(_ context.Context, in map[string]any) (map[string]any, error) {
			return map[string]any{"greeting": "hi " + in["name"].(string)}, nil
		}),
	})

	a, ok := cat.Resolve("greet")
	require.True(t, ok)

	out, err := a.Do(t.Context(), map[string]any{"name": "Ada"})
	require.NoError(t, err)
	assert.Equal(t, "hi Ada", out["greeting"])

	_, ok = cat.Resolve("missing")
	assert.False(t, ok)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:
```bash
go test ./action/... -v
```
Expected: FAIL — `undefined: action.NewMapCatalog`.

- [ ] **Step 3: Write minimal implementation**

Create `action/action.go`:
```go
// Package action defines the service-action catalog: named, interface-based
// units of work referenced from definition nodes and resolved at execution time.
package action

import "context"

// ServiceAction performs a unit of work for a service task.
type ServiceAction interface {
	Do(ctx context.Context, in map[string]any) (out map[string]any, err error)
}

// Func adapts a plain function to ServiceAction.
type Func func(ctx context.Context, in map[string]any) (map[string]any, error)

func (f Func) Do(ctx context.Context, in map[string]any) (map[string]any, error) { return f(ctx, in) }

// Catalog resolves action names to implementations.
type Catalog interface {
	Resolve(name string) (ServiceAction, bool)
}

// MapCatalog is a map-backed Catalog.
type MapCatalog map[string]ServiceAction

func NewMapCatalog(m map[string]ServiceAction) MapCatalog { return MapCatalog(m) }

func (c MapCatalog) Resolve(name string) (ServiceAction, bool) {
	a, ok := c[name]
	return a, ok
}
```

- [ ] **Step 4: Run test to verify it passes**

Run:
```bash
go test ./action/... -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add action/action.go action/action_test.go
git commit -m "feat(action): ServiceAction interface and map catalog"
```

---

### Task 10: Runtime ports + in-memory implementations

**Files:**
- Create: `runtime/ports.go`
- Create: `runtime/memory.go`
- Test: (exercised by Task 11's `example_test.go`)

**Interfaces:**
- Consumes: `engine.InstanceState`, `engine.Trigger`.
- Produces: `runtime.StateStore` (`Load(id) (engine.InstanceState, bool); Save(engine.InstanceState)`), `runtime.Journal` (`Append(id string, trg engine.Trigger)`), `runtime.OutboxWriter` (`Write(topic string, payload map[string]any)`), and in-memory implementations `runtime.NewMemStateStore()`, `runtime.NewMemJournal()`, `runtime.NewMemOutbox()`. The mem-journal exposes `Entries(id string) []engine.Trigger` for test assertions.

- [ ] **Step 1: Write the implementation (exercised by Task 11)**

> Note: these ports + in-memory impls have no standalone behavior worth a separate test; their red/green cycle is Task 11's end-to-end example, which won't compile until they exist. Create the files, then go to Task 11 Step 2 for the red state.

Create `runtime/ports.go`:
```go
// Package runtime is the reference driver that performs engine Commands and
// feeds results back as Triggers. It is reference wiring, not the product;
// later sub-projects replace the in-memory ports with real implementations.
package runtime

import "github.com/kartaladev/wrkflw/engine"

// StateStore persists the authoritative instance snapshot.
type StateStore interface {
	Load(id string) (engine.InstanceState, bool)
	Save(st engine.InstanceState)
}

// Journal is the append-only audit ledger of applied triggers.
type Journal interface {
	Append(id string, trg engine.Trigger)
}

// OutboxWriter records domain events for later relay (no-op in memory here).
type OutboxWriter interface {
	Write(topic string, payload map[string]any)
}
```

Create `runtime/memory.go`:
```go
package runtime

import "github.com/kartaladev/wrkflw/engine"

type memStateStore struct{ m map[string]engine.InstanceState }

func NewMemStateStore() StateStore { return &memStateStore{m: map[string]engine.InstanceState{}} }

func (s *memStateStore) Load(id string) (engine.InstanceState, bool) {
	st, ok := s.m[id]
	return st, ok
}
func (s *memStateStore) Save(st engine.InstanceState) { s.m[st.InstanceID] = st }

type memJournal struct{ m map[string][]engine.Trigger }

func NewMemJournal() *memJournal { return &memJournal{m: map[string][]engine.Trigger{}} }

func (j *memJournal) Append(id string, trg engine.Trigger) { j.m[id] = append(j.m[id], trg) }
func (j *memJournal) Entries(id string) []engine.Trigger   { return j.m[id] }

type memOutbox struct{ events []struct {
	Topic   string
	Payload map[string]any
} }

func NewMemOutbox() *memOutbox { return &memOutbox{} }

func (o *memOutbox) Write(topic string, payload map[string]any) {
	o.events = append(o.events, struct {
		Topic   string
		Payload map[string]any
	}{topic, payload})
}
```

- [ ] **Step 2: Verify it compiles**

Run:
```bash
go build ./runtime/...
```
Expected: no output, exit 0.

- [ ] **Step 3: Commit**

```bash
git add runtime/ports.go runtime/memory.go
git commit -m "feat(runtime): persistence ports and in-memory implementations"
```

---

### Task 11: Runner loop + end-to-end linear example

**Files:**
- Create: `runtime/runner.go`
- Test: `runtime/example_test.go`

**Interfaces:**
- Consumes: `engine.Step`, `action.Catalog`, `clock.Clock`, the runtime ports (Task 10).
- Produces: `runtime.Runner` with `runtime.NewRunner(cat action.Catalog, clk clock.Clock, store StateStore, jnl Journal, out OutboxWriter) *Runner` and `(*Runner).Run(ctx context.Context, def *model.ProcessDefinition, instanceID string, vars map[string]any) (engine.InstanceState, error)`. Run drives the instance to a terminal state for linear processes.

- [ ] **Step 1: Write the failing test**

Create `runtime/example_test.go`:
```go
package runtime_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/clock"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/model"
	"github.com/kartaladev/wrkflw/runtime"
)

func linearDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "greeting", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "greet", Kind: model.KindServiceTask, Action: "greet"},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "greet"},
			{ID: "f2", Source: "greet", Target: "end"},
		},
	}
}

func TestRunnerExecutesLinearProcess(t *testing.T) {
	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"greet": action.Func(func(_ context.Context, in map[string]any) (map[string]any, error) {
			return map[string]any{"greeting": "hi " + in["name"].(string)}, nil
		}),
	})
	jnl := runtime.NewMemJournal()
	r := runtime.NewRunner(cat, clock.System(), runtime.NewMemStateStore(), jnl, runtime.NewMemOutbox())

	final, err := r.Run(t.Context(), linearDef(), "i1", map[string]any{"name": "Ada"})
	require.NoError(t, err)

	assert.Equal(t, engine.StatusCompleted, final.Status)
	assert.Equal(t, "hi Ada", final.Variables["greeting"])
	assert.Empty(t, final.Tokens)

	// Journal recorded StartInstance + ActionCompleted (audit trail).
	assert.Len(t, jnl.Entries("i1"), 2)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:
```bash
go test ./runtime/... -run TestRunnerExecutesLinearProcess -v
```
Expected: FAIL — `undefined: runtime.NewRunner`.

- [ ] **Step 3: Write minimal implementation**

Create `runtime/runner.go`:
```go
package runtime

import (
	"context"
	"fmt"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/clock"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/model"
)

// Runner is the reference single-process driver loop.
type Runner struct {
	cat   action.Catalog
	clk   clock.Clock
	store StateStore
	jnl   Journal
	out   OutboxWriter
}

func NewRunner(cat action.Catalog, clk clock.Clock, store StateStore, jnl Journal, out OutboxWriter) *Runner {
	return &Runner{cat: cat, clk: clk, store: store, jnl: jnl, out: out}
}

// Run starts an instance and drives it to a terminal state, performing each
// command and feeding results back as triggers. Linear processes only in Plan 1.
func (r *Runner) Run(ctx context.Context, def *model.ProcessDefinition, instanceID string, vars map[string]any) (engine.InstanceState, error) {
	queue := []engine.Trigger{engine.NewStartInstance(r.clk.Now(), vars)}
	st := engine.InstanceState{InstanceID: instanceID}

	for len(queue) > 0 {
		trg := queue[0]
		queue = queue[1:]

		r.jnl.Append(instanceID, trg)
		res, err := engine.Step(def, st, trg, engine.StepOptions{})
		if err != nil {
			return st, fmt.Errorf("runtime: step: %w", err)
		}
		st = res.State
		r.store.Save(st)

		for _, c := range res.Commands {
			next, err := r.perform(ctx, c)
			if err != nil {
				return st, err
			}
			if next != nil {
				queue = append(queue, next)
			}
		}
	}
	return st, nil
}

// perform executes one command and returns the resulting trigger, if any.
func (r *Runner) perform(ctx context.Context, c engine.Command) (engine.Trigger, error) {
	switch cmd := c.(type) {
	case engine.InvokeAction:
		a, ok := r.cat.Resolve(cmd.Name)
		if !ok {
			return engine.NewActionFailed(r.clk.Now(), cmd.CommandID, "unknown action: "+cmd.Name, false), nil
		}
		out, err := a.Do(ctx, cmd.Input)
		if err != nil {
			return engine.NewActionFailed(r.clk.Now(), cmd.CommandID, err.Error(), true), nil
		}
		return engine.NewActionCompleted(r.clk.Now(), cmd.CommandID, out), nil

	case engine.CompleteInstance:
		r.out.Write("instance.completed", cmd.Result)
		return nil, nil

	case engine.FailInstance:
		r.out.Write("instance.failed", map[string]any{"error": cmd.Err})
		return nil, nil

	default:
		return nil, fmt.Errorf("runtime: unsupported command %T", c)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run:
```bash
go test ./runtime/... -v
```
Expected: PASS.

- [ ] **Step 5: Run the full suite + lint + coverage**

Run:
```bash
go test -race -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1
golangci-lint run ./...
```
Expected: all tests pass; total coverage printed (≥ 85% on `model`/`engine`/`action`); lint clean. If `golangci-lint` is not installed, install per the `cc-skills-golang:golang-lint` skill, then re-run.

- [ ] **Step 6: Commit**

```bash
git add runtime/runner.go runtime/example_test.go cover.out
git rm --cached cover.out 2>/dev/null || true
git commit -m "feat(runtime): reference runner loop with end-to-end linear example"
```

---

## Verification Checklist (Plan 1)

- [ ] `go build ./...` and `go test ./...` pass from the repo root.
- [ ] `model.Validate` rejects: no start, multiple starts, dangling flow, dead end (covered by `TestValidate`).
- [ ] `engine.Step` drives Start → ServiceTask (emits `InvokeAction`, parks token) → on `ActionCompleted` → End → `CompleteInstance`, `StatusCompleted`.
- [ ] `Step` is deterministic (`TestStepIsDeterministic`) and does not mutate input (`TestStepDoesNotMutateInput`).
- [ ] Timestamps come only from `Trigger.OccurredAt`; grep confirms `engine`/`model` contain no `time.Now(` call: `! grep -rn "time.Now(" engine model`.
- [ ] `engine`/`model` import no transport/storage/bus/time-vendor packages: `go list -deps ./engine/... ./model/...` shows only stdlib + `model`.
- [ ] Reference `Runner` runs a linear process end-to-end; journal records both triggers.
- [ ] Coverage ≥ 85% on `model`, `engine`, `action`; `golangci-lint run ./...` clean.

## Self-Review Notes

- **Spec coverage (this plan's slice):** model + Validate (§3, partial — only the rules linear flow exercises; gateway/boundary/call-activity rules land in their plans), sealed `Trigger`/`Command` sets (§5, the StartInstance/ActionCompleted/ActionFailed/InvokeAction/CompleteInstance/FailInstance subset), `InstanceState`+timing/history (§4, subset; Scopes/Tasks deferred), `Step` macro (§6), `clock` (§8), `action` (§8), reference runtime + fakes (§9). Deferred to later plans (tracked in the roadmap at the top): expr wrapper, all gateways, human tasks/authz/humantask, timers/boundary/SLA, sub-processes/call activity, events, errors/compensation, micro-step.
- **Determinism:** CommandID (`<instanceID>-c<seq>`) and Token IDs (`<instanceID>-t<seq>`) derive from in-state counters — no randomness, no clock — satisfying the Global Constraint.
- **Purity:** `cloneState` copies `Variables`/`Tokens`/`History`/`EndedAt` so `Step` never mutates the caller's state (`TestStepDoesNotMutateInput`).
- **Type consistency:** `CommandID` naming is uniform across `InvokeAction`/`ActionCompleted`/`ActionFailed`; `engine.Step` signature matches the spec §6 and is consumed unchanged by `runtime.Runner`.
```

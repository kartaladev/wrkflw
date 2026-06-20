# Engine Core — Gateways (Plan 2 of 6) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `expr`-based conditions and the Exclusive (XOR) and Parallel (AND, fork + synchronizing join) gateways to the engine core, on top of the merged Plan 1 foundation.

**Architecture:** Routing decisions happen inside `engine.drive()` when a token sits on a gateway node. An in-repo `expreval` wrapper (the only importer of `expr-lang/expr`) evaluates sequence-flow conditions; the engine holds a package-level memoizing evaluator so `Step` keeps its signature and stays deterministic. Parallel fork creates one token per outgoing flow; the parallel join parks arriving tokens (`TokenAtJoin`) and fires when all incoming flows have arrived.

**Tech Stack:** Go 1.25, `github.com/expr-lang/expr`, `github.com/stretchr/testify`.

## Global Constraints

- Go **1.25**; module `github.com/zakyalvan/krtlwrkflw`; public packages at module root (no `pkg/`).
- The core (`engine`, `model`, `expreval`) imports only stdlib + `model` + `expreval` + `expr-lang/expr` (the last only inside `expreval`). **No** transport/storage/bus/time-vendor; the engine **never calls `time.Now()`** — time enters as `Trigger.OccurredAt`.
- `Step` stays **deterministic** (identical `(state,trigger)` ⇒ identical `(state,commands)`; IDs from in-state counters; flows evaluated in definition order) and **pure** (never mutates its input `InstanceState`).
- `Step`'s public signature is unchanged: `Step(def *model.ProcessDefinition, st InstanceState, trg Trigger, opt StepOptions) (StepResult, error)`.
- Tests are **black-box** (`package <pkg>_test`), table-driven with an **`assert` closure per case**, `t.Context()` where a context is needed.
- Coverage ≥ 85% on touched packages; `go test -race ./...` green; `golangci-lint run ./...` clean. Conventional Commits; commit after each green step group.

## Context from Plan 1 (already on `main`)

- `engine/step.go`: `Step` switches on the trigger; `drive(def, *InstanceState, at) []Command` loops `firstActive()` and switches on `node.Kind` (StartEvent → `moveAlongSingleFlow`; ServiceTask → emit `InvokeAction`, park; EndEvent → `consumeToken`, maybe `CompleteInstance`; `default` → park). Sentinels `ErrUnknownTrigger`, `ErrTokenNotFound`, `ErrMicroNotImplemented`.
- `engine/state.go`: `Token{ID,NodeID,ScopeID,State,AwaitCommand,Payload,EnteredAt}`; `TokenState` = `TokenActive | TokenWaitingCommand | TokenAtJoin`; `InstanceState` with `CmdSeq`/`TokenSeq` counters; `NodeVisit`.
- Helpers in `step.go`: `placeToken(nodeID, at)`, `firstActive()`, `tokenAwaiting(cmdID)`, `nextCommandID()`, `moveAlongSingleFlow(def, *Token, at)`, `consumeToken(*Token, at)`, `openVisit`/`closeVisit`, `mergeVars`/`copyVars`/`cloneState`.
- `model`: `NodeKind` already includes `KindExclusiveGateway`, `KindParallelGateway`, `KindInclusiveGateway`, `KindEventBasedGateway`. `SequenceFlow{ID,Source,Target,Condition,IsDefault}`. `Validate` covers start/flow-endpoint/dead-end/end rules.

> **Note on roadmap:** Inclusive (OR) gateway + OR-join moved to Plan 3 (reachable-incoming synchronization needs its own focused plan). Subsequent plans renumber: 3 = inclusive/event-based, 4 = human tasks & timers, 5 = events & sub-processes, 6 = errors/compensation/micro-step.

---

## File Structure

```
expreval/
  expreval.go            # Evaluator: memoized Compile + EvalBool
  expreval_test.go       # (black-box) bool eval, undefined vars, non-bool error
engine/
  conditions.go          # package-level default Evaluator used by drive()
  step.go                # MODIFY: drive returns ([]Command, error); add gateway cases + helpers; ErrNoMatchingFlow
  step_gateway_test.go   # (black-box) exclusive + parallel behavior
model/
  validate.go            # MODIFY: gateway condition/default rules + sentinels
  validate_test.go       # MODIFY: gateway-misuse cases
```

---

### Task 1: `expreval` wrapper over expr-lang/expr

**Files:**
- Create: `expreval/expreval.go`
- Test: `expreval/expreval_test.go`

**Interfaces:**
- Produces: `expreval.Evaluator` (memoizing), `expreval.New() *Evaluator`, `(*Evaluator).EvalBool(code string, env map[string]any) (bool, error)`. Undefined variables are allowed (treated as nil). A non-bool result is an error.

- [ ] **Step 1: Write the failing test**

Create `expreval/expreval_test.go`:
```go
package expreval_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/expreval"
)

func TestEvalBool(t *testing.T) {
	tests := map[string]struct {
		code   string
		env    map[string]any
		assert func(t *testing.T, got bool, err error)
	}{
		"true comparison": {
			code: "amount > 100", env: map[string]any{"amount": 150},
			assert: func(t *testing.T, got bool, err error) {
				require.NoError(t, err)
				assert.True(t, got)
			},
		},
		"false comparison": {
			code: "amount > 100", env: map[string]any{"amount": 50},
			assert: func(t *testing.T, got bool, err error) {
				require.NoError(t, err)
				assert.False(t, got)
			},
		},
		"undefined variable treated as nil (no error)": {
			code: "amount > 100", env: map[string]any{},
			assert: func(t *testing.T, got bool, err error) {
				require.NoError(t, err)
				assert.False(t, got)
			},
		},
		"non-bool result errors": {
			code: "amount + 1", env: map[string]any{"amount": 1},
			assert: func(t *testing.T, got bool, err error) {
				require.Error(t, err)
			},
		},
		"syntax error": {
			code: "amount >", env: map[string]any{"amount": 1},
			assert: func(t *testing.T, got bool, err error) {
				require.Error(t, err)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			e := expreval.New()
			got, err := e.EvalBool(tc.code, tc.env)
			tc.assert(t, got, err)
		})
	}
}

func TestEvalBoolMemoizes(t *testing.T) {
	e := expreval.New()
	// Same code evaluated twice with different envs must use the cached program
	// and still return per-env results.
	got1, err := e.EvalBool("x == 1", map[string]any{"x": 1})
	require.NoError(t, err)
	assert.True(t, got1)
	got2, err := e.EvalBool("x == 1", map[string]any{"x": 2})
	require.NoError(t, err)
	assert.False(t, got2)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:
```bash
go test ./expreval/... -v
```
Expected: FAIL — `package .../expreval is not in std` / `undefined: expreval.New` (expr dependency not yet added).

- [ ] **Step 3: Add the expr dependency**

Run:
```bash
go get github.com/expr-lang/expr@latest
```
Expected: adds `github.com/expr-lang/expr` to `go.mod`/`go.sum`.

- [ ] **Step 4: Write minimal implementation**

Create `expreval/expreval.go`:
```go
// Package expreval is the engine's only wrapper over expr-lang/expr. It is the
// single place that imports the expression vendor, so the dependency stays
// swappable. It memoizes compiled programs (compilation is deterministic, so the
// cache is referentially transparent) and is safe for concurrent use.
package expreval

import (
	"fmt"
	"sync"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
)

// Evaluator compiles and evaluates expression strings, caching compiled programs.
type Evaluator struct {
	mu    sync.Mutex
	cache map[string]*vm.Program
}

// New returns an empty Evaluator.
func New() *Evaluator { return &Evaluator{cache: make(map[string]*vm.Program)} }

func (e *Evaluator) compile(code string) (*vm.Program, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if p, ok := e.cache[code]; ok {
		return p, nil
	}
	p, err := expr.Compile(code, expr.AllowUndefinedVariables())
	if err != nil {
		return nil, fmt.Errorf("expreval: compile %q: %w", code, err)
	}
	e.cache[code] = p
	return p, nil
}

// EvalBool evaluates code against env and requires a boolean result. Undefined
// variables evaluate to nil rather than erroring.
func (e *Evaluator) EvalBool(code string, env map[string]any) (bool, error) {
	p, err := e.compile(code)
	if err != nil {
		return false, err
	}
	out, err := expr.Run(p, env)
	if err != nil {
		return false, fmt.Errorf("expreval: run %q: %w", code, err)
	}
	b, ok := out.(bool)
	if !ok {
		return false, fmt.Errorf("expreval: %q did not evaluate to bool (got %T)", code, out)
	}
	return b, nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run:
```bash
go test ./expreval/... -v
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum expreval/expreval.go expreval/expreval_test.go
git commit -m "feat(expreval): memoizing expr wrapper with EvalBool"
```

---

### Task 2: Gateway validation rules in `model.Validate`

**Files:**
- Modify: `model/validate.go`
- Modify: `model/validate_test.go`

**Interfaces:**
- Consumes: existing `model` types + `Validate`.
- Produces: new sentinels `ErrConditionNotAllowed`, `ErrDefaultNotAllowed`, `ErrMultipleDefaults`. Rule: a flow with a non-empty `Condition` or `IsDefault==true` may only leave an Exclusive or Inclusive gateway; a node may have at most one default outgoing flow.

- [ ] **Step 1: Write the failing test (extend the table)**

Add these cases inside the existing `tests` map in `model/validate_test.go` (before its closing brace). They reuse the table's `def`/`assert` shape:
```go
		"condition on parallel gateway outgoing": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "fork", Kind: model.KindParallelGateway},
					{ID: "a", Kind: model.KindServiceTask, Action: "a"},
					{ID: "b", Kind: model.KindServiceTask, Action: "b"},
					{ID: "end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "start", Target: "fork"},
					{ID: "f2", Source: "fork", Target: "a", Condition: "x > 1"}, // illegal
					{ID: "f3", Source: "fork", Target: "b"},
					{ID: "f4", Source: "a", Target: "end"},
					{ID: "f5", Source: "b", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrConditionNotAllowed)
			},
		},
		"default on parallel gateway outgoing": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "fork", Kind: model.KindParallelGateway},
					{ID: "a", Kind: model.KindServiceTask, Action: "a"},
					{ID: "b", Kind: model.KindServiceTask, Action: "b"},
					{ID: "end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "start", Target: "fork"},
					{ID: "f2", Source: "fork", Target: "a", IsDefault: true}, // illegal
					{ID: "f3", Source: "fork", Target: "b"},
					{ID: "f4", Source: "a", Target: "end"},
					{ID: "f5", Source: "b", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrDefaultNotAllowed)
			},
		},
		"multiple defaults on exclusive gateway": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "xor", Kind: model.KindExclusiveGateway},
					{ID: "a", Kind: model.KindServiceTask, Action: "a"},
					{ID: "b", Kind: model.KindServiceTask, Action: "b"},
					{ID: "end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "start", Target: "xor"},
					{ID: "f2", Source: "xor", Target: "a", IsDefault: true},
					{ID: "f3", Source: "xor", Target: "b", IsDefault: true}, // illegal: two defaults
					{ID: "f4", Source: "a", Target: "end"},
					{ID: "f5", Source: "b", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.ErrorIs(t, err, model.ErrMultipleDefaults)
			},
		},
		"valid exclusive gateway with condition and default": {
			def: &model.ProcessDefinition{
				ID: "p", Version: 1,
				Nodes: []model.Node{
					{ID: "start", Kind: model.KindStartEvent},
					{ID: "xor", Kind: model.KindExclusiveGateway},
					{ID: "a", Kind: model.KindServiceTask, Action: "a"},
					{ID: "b", Kind: model.KindServiceTask, Action: "b"},
					{ID: "end", Kind: model.KindEndEvent},
				},
				Flows: []model.SequenceFlow{
					{ID: "f1", Source: "start", Target: "xor"},
					{ID: "f2", Source: "xor", Target: "a", Condition: "x > 1"},
					{ID: "f3", Source: "xor", Target: "b", IsDefault: true},
					{ID: "f4", Source: "a", Target: "end"},
					{ID: "f5", Source: "b", Target: "end"},
				},
			},
			assert: func(t *testing.T, err error) {
				require.NoError(t, err)
			},
		},
```

- [ ] **Step 2: Run test to verify it fails**

Run:
```bash
go test ./model/... -run TestValidate -v
```
Expected: FAIL — `undefined: model.ErrConditionNotAllowed` (and the new cases fail).

- [ ] **Step 3: Write minimal implementation**

In `model/validate.go`, add the new sentinels to the existing `var (...)` block:
```go
	ErrConditionNotAllowed = errors.New("model: condition on flow from a non-conditional gateway")
	ErrDefaultNotAllowed   = errors.New("model: default flow from a non-conditional gateway")
	ErrMultipleDefaults    = errors.New("model: node has more than one default flow")
```

Then, inside `Validate`, after the existing per-node loop, add a per-node gateway-flow check. Insert this block just before `return errors.Join(errs...)`:
```go
	for _, n := range d.Nodes {
		conditional := n.Kind == KindExclusiveGateway || n.Kind == KindInclusiveGateway
		defaults := 0
		for _, f := range d.Outgoing(n.ID) {
			if f.Condition != "" && !conditional {
				errs = append(errs, fmt.Errorf("%w: flow %q from node %q", ErrConditionNotAllowed, f.ID, n.ID))
			}
			if f.IsDefault {
				if !conditional {
					errs = append(errs, fmt.Errorf("%w: flow %q from node %q", ErrDefaultNotAllowed, f.ID, n.ID))
				}
				defaults++
			}
		}
		if defaults > 1 {
			errs = append(errs, fmt.Errorf("%w: node %q has %d", ErrMultipleDefaults, n.ID, defaults))
		}
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run:
```bash
go test ./model/... -v
```
Expected: PASS (all prior + new cases).

- [ ] **Step 5: Commit**

```bash
git add model/validate.go model/validate_test.go
git commit -m "feat(model): validate gateway condition/default flow rules"
```

---

### Task 3: Exclusive (XOR) gateway routing

**Files:**
- Create: `engine/conditions.go`
- Modify: `engine/step.go`
- Test: `engine/step_gateway_test.go`

**Interfaces:**
- Consumes: `expreval` (Task 1), the existing `drive`/helpers.
- Produces: package-level `engine` evaluator (`conditions`); `drive` now returns `([]Command, error)`; new sentinel `engine.ErrNoMatchingFlow`; helpers `selectExclusiveTarget`, `(*InstanceState).moveTokenToTarget`. A token on an Exclusive gateway takes the first outgoing flow whose condition is empty or true (in definition order), else the default flow, else `ErrNoMatchingFlow`.

- [ ] **Step 1: Write the failing test**

Create `engine/step_gateway_test.go`:
```go
package engine_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/model"
)

// exclusiveDef: start -> xor -{amount > 100}-> big ; -default-> small ; both -> end
func exclusiveDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "xor", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "xor", Kind: model.KindExclusiveGateway},
			{ID: "big", Kind: model.KindServiceTask, Action: "big"},
			{ID: "small", Kind: model.KindServiceTask, Action: "small"},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "xor"},
			{ID: "f2", Source: "xor", Target: "big", Condition: "amount > 100"},
			{ID: "f3", Source: "xor", Target: "small", IsDefault: true},
			{ID: "f4", Source: "big", Target: "end"},
			{ID: "f5", Source: "small", Target: "end"},
		},
	}
}

func TestExclusiveGatewayTakesConditionalBranch(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	res, err := engine.Step(exclusiveDef(), engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, map[string]any{"amount": 150}), engine.StepOptions{})
	require.NoError(t, err)

	require.Len(t, res.Commands, 1)
	ia := res.Commands[0].(engine.InvokeAction)
	assert.Equal(t, "big", ia.Name)
	require.Len(t, res.State.Tokens, 1)
	assert.Equal(t, "big", res.State.Tokens[0].NodeID)
}

func TestExclusiveGatewayTakesDefaultBranch(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	res, err := engine.Step(exclusiveDef(), engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, map[string]any{"amount": 5}), engine.StepOptions{})
	require.NoError(t, err)

	require.Len(t, res.Commands, 1)
	assert.Equal(t, "small", res.Commands[0].(engine.InvokeAction).Name)
}

func TestExclusiveGatewayNoMatchNoDefaultErrors(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	def := &model.ProcessDefinition{
		ID: "xor", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "xor", Kind: model.KindExclusiveGateway},
			{ID: "big", Kind: model.KindServiceTask, Action: "big"},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "xor"},
			{ID: "f2", Source: "xor", Target: "big", Condition: "amount > 100"},
			{ID: "f3", Source: "big", Target: "end"},
		},
	}
	_, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, map[string]any{"amount": 5}), engine.StepOptions{})
	require.ErrorIs(t, err, engine.ErrNoMatchingFlow)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:
```bash
go test ./engine/... -run TestExclusiveGateway -v
```
Expected: FAIL — `undefined: engine.ErrNoMatchingFlow`, and the gateway token gets parked (no `InvokeAction`) because `drive`'s `default` case handles `KindExclusiveGateway` today.

- [ ] **Step 3: Write the package-level evaluator**

Create `engine/conditions.go`:
```go
package engine

import "github.com/zakyalvan/krtlwrkflw/expreval"

// conditions is the engine's shared, memoizing expression evaluator. Compilation
// is deterministic and the cache is referentially transparent, so using a shared
// instance does not affect Step's determinism.
var conditions = expreval.New()
```

- [ ] **Step 4: Modify `engine/step.go` — drive returns error, add exclusive case + helpers**

1. Add the sentinel to the existing `var (...)` block:
```go
	ErrNoMatchingFlow = errors.New("engine: no matching outgoing flow")
```

2. Change the `drive` call in `Step` from:
```go
	cmds := drive(def, &s, trg.OccurredAt())
	return StepResult{State: s, Commands: cmds}, nil
```
to:
```go
	cmds, err := drive(def, &s, trg.OccurredAt())
	if err != nil {
		return StepResult{}, err
	}
	return StepResult{State: s, Commands: cmds}, nil
```

3. Change `drive`'s signature and its return statements:
```go
func drive(def *model.ProcessDefinition, s *InstanceState, at time.Time) ([]Command, error) {
```
The early `break` stays; change the final `return cmds` to `return cmds, nil`.

4. Add the exclusive case to the `switch node.Kind` in `drive`, immediately before the `default:` case:
```go
		case model.KindExclusiveGateway:
			target, err := selectExclusiveTarget(def, s, node)
			if err != nil {
				return cmds, err
			}
			s.moveTokenToTarget(tok, target, at)
```

5. Add the two new helpers (next to the other `InstanceState` helpers):
```go
// moveTokenToTarget moves a token to targetID, closing the old visit and opening
// a new one, leaving the token Active.
func (s *InstanceState) moveTokenToTarget(tok *Token, target string, at time.Time) {
	s.closeVisit(tok.ID, tok.NodeID, at)
	tok.NodeID = target
	tok.EnteredAt = at
	tok.State = TokenActive
	s.openVisit(tok.ID, target, at)
}

// selectExclusiveTarget picks the target of an exclusive gateway: the first
// outgoing flow (in definition order) with an empty or true condition, else the
// default flow, else ErrNoMatchingFlow.
func selectExclusiveTarget(def *model.ProcessDefinition, s *InstanceState, node model.Node) (string, error) {
	var def_ *model.SequenceFlow
	for _, f := range def.Outgoing(node.ID) {
		if f.IsDefault {
			ff := f
			def_ = &ff
			continue
		}
		if f.Condition == "" {
			return f.Target, nil
		}
		ok, err := conditions.EvalBool(f.Condition, s.Variables)
		if err != nil {
			return "", fmt.Errorf("engine: gateway %q flow %q: %w", node.ID, f.ID, err)
		}
		if ok {
			return f.Target, nil
		}
	}
	if def_ != nil {
		return def_.Target, nil
	}
	return "", fmt.Errorf("%w: gateway %q", ErrNoMatchingFlow, node.ID)
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run:
```bash
go test ./engine/... -v
```
Expected: PASS (the three exclusive tests + all Plan-1 engine tests).

- [ ] **Step 6: Commit**

```bash
git add engine/conditions.go engine/step.go engine/step_gateway_test.go
git commit -m "feat(engine): exclusive gateway routing via expr conditions"
```

---

### Task 4: Parallel (AND) gateway — fork (diverging)

**Files:**
- Modify: `engine/step.go`
- Modify: `engine/step_gateway_test.go`

**Interfaces:**
- Produces: parallel-gateway handling in `drive` and helper `(*InstanceState).forkParallel`. A parallel gateway with ≤1 incoming flow forks: it consumes the arriving token and creates one Active token at each outgoing flow's target (in definition order).

- [ ] **Step 1: Write the failing test (add to `engine/step_gateway_test.go`)**

```go
// parallelForkDef: start -> fork => a, b (service tasks) -> end (each)
func parallelForkDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "par", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "fork", Kind: model.KindParallelGateway},
			{ID: "a", Kind: model.KindServiceTask, Action: "a"},
			{ID: "b", Kind: model.KindServiceTask, Action: "b"},
			{ID: "enda", Kind: model.KindEndEvent},
			{ID: "endb", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "fork"},
			{ID: "f2", Source: "fork", Target: "a"},
			{ID: "f3", Source: "fork", Target: "b"},
			{ID: "f4", Source: "a", Target: "enda"},
			{ID: "f5", Source: "b", Target: "endb"},
		},
	}
}

func TestParallelGatewayForksAllBranches(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	res, err := engine.Step(parallelForkDef(), engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)

	// Both branches fire their service action in one macro step.
	require.Len(t, res.Commands, 2)
	names := []string{
		res.Commands[0].(engine.InvokeAction).Name,
		res.Commands[1].(engine.InvokeAction).Name,
	}
	assert.ElementsMatch(t, []string{"a", "b"}, names)

	// Two tokens, one parked on each service task; the fork token is gone.
	require.Len(t, res.State.Tokens, 2)
	nodes := []string{res.State.Tokens[0].NodeID, res.State.Tokens[1].NodeID}
	assert.ElementsMatch(t, []string{"a", "b"}, nodes)
	for _, tk := range res.State.Tokens {
		assert.Equal(t, engine.TokenWaitingCommand, tk.State)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:
```bash
go test ./engine/... -run TestParallelGatewayForks -v
```
Expected: FAIL — only the fork token is parked by `default`; no `InvokeAction` commands (`len(res.Commands)` is 0, not 2).

- [ ] **Step 3: Modify `engine/step.go` — add the parallel case + forkParallel**

1. Add the parallel case to the `switch node.Kind` in `drive`, before `default:`:
```go
		case model.KindParallelGateway:
			s.forkParallel(def, tok, node, at)
```

2. Add the helper next to the other `InstanceState` helpers:
```go
// forkParallel consumes the incoming token and creates one Active token at each
// outgoing flow target (definition order). Used for a diverging parallel gateway.
func (s *InstanceState) forkParallel(def *model.ProcessDefinition, tok *Token, node model.Node, at time.Time) {
	outs := def.Outgoing(node.ID)
	s.consumeToken(tok, at)
	for _, f := range outs {
		s.placeToken(f.Target, at)
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:
```bash
go test ./engine/... -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add engine/step.go engine/step_gateway_test.go
git commit -m "feat(engine): parallel gateway fork (diverging)"
```

---

### Task 5: Parallel (AND) gateway — synchronizing join (converging) + full diamond

**Files:**
- Modify: `engine/step.go`
- Modify: `engine/step_gateway_test.go`
- Test: `runtime/example_test.go` (add an end-to-end parallel diamond)

**Interfaces:**
- Produces: join handling in the parallel case. A parallel gateway with >1 incoming flow is a join: each arriving token parks as `TokenAtJoin`; when the count of `TokenAtJoin` tokens on the node equals the number of incoming flows, all are consumed and the join forks to its outgoing flows (one Active token per outgoing target). Helper `(*InstanceState).tryParallelJoin`.

- [ ] **Step 1: Write the failing tests**

Add to `engine/step_gateway_test.go`:
```go
// diamondDef: start -> fork => a,b -> join -> end. Join waits for both a and b.
func diamondDef() *model.ProcessDefinition {
	return &model.ProcessDefinition{
		ID: "diamond", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "fork", Kind: model.KindParallelGateway},
			{ID: "a", Kind: model.KindServiceTask, Action: "a"},
			{ID: "b", Kind: model.KindServiceTask, Action: "b"},
			{ID: "join", Kind: model.KindParallelGateway},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "fork"},
			{ID: "f2", Source: "fork", Target: "a"},
			{ID: "f3", Source: "fork", Target: "b"},
			{ID: "f4", Source: "a", Target: "join"},
			{ID: "f5", Source: "b", Target: "join"},
			{ID: "f6", Source: "join", Target: "end"},
		},
	}
}

func TestParallelJoinWaitsForAllBranches(t *testing.T) {
	at := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	def := diamondDef()

	r0, err := engine.Step(def, engine.InstanceState{InstanceID: "i1"},
		engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r0.Commands, 2) // a and b invoked
	cmdA := r0.Commands[0].(engine.InvokeAction)
	cmdB := r0.Commands[1].(engine.InvokeAction)

	// Complete the first branch: token parks at the join, instance not done.
	r1, err := engine.Step(def, r0.State,
		engine.NewActionCompleted(at.Add(time.Second), cmdA.CommandID, nil), engine.StepOptions{})
	require.NoError(t, err)
	assert.Empty(t, r1.Commands)
	assert.Equal(t, engine.StatusRunning, r1.State.Status)
	require.Len(t, r1.State.Tokens, 2)

	// Complete the second branch: join fires, reaches end, instance completes.
	r2, err := engine.Step(def, r1.State,
		engine.NewActionCompleted(at.Add(2*time.Second), cmdB.CommandID, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Len(t, r2.Commands, 1)
	_, ok := r2.Commands[0].(engine.CompleteInstance)
	require.True(t, ok)
	assert.Equal(t, engine.StatusCompleted, r2.State.Status)
	assert.Empty(t, r2.State.Tokens)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:
```bash
go test ./engine/... -run TestParallelJoin -v
```
Expected: FAIL — the join is a parallel gateway with 2 incoming, but the current code calls `forkParallel` unconditionally, so the first arriving token forks straight to `end` and the instance completes too early (or token bookkeeping is wrong).

- [ ] **Step 3: Modify `engine/step.go` — split fork vs join, add tryParallelJoin**

1. Replace the parallel case body in `drive` with a fork/join split:
```go
		case model.KindParallelGateway:
			if len(def.Incoming(node.ID)) > 1 {
				s.tryParallelJoin(def, tok, node, at)
			} else {
				s.forkParallel(def, tok, node, at)
			}
```

2. Add the helper next to the other `InstanceState` helpers:
```go
// tryParallelJoin parks the arriving token at a converging parallel gateway and,
// once a token has arrived on every incoming flow, consumes them all and forks to
// the gateway's outgoing flows. Until then the token waits as TokenAtJoin.
func (s *InstanceState) tryParallelJoin(def *model.ProcessDefinition, tok *Token, node model.Node, at time.Time) {
	tok.State = TokenAtJoin

	arrived := 0
	for i := range s.Tokens {
		if s.Tokens[i].NodeID == node.ID && s.Tokens[i].State == TokenAtJoin {
			arrived++
		}
	}
	if arrived < len(def.Incoming(node.ID)) {
		return // still waiting on other branches
	}

	// Fire: remove all tokens parked at this join (closing their visits), then
	// create one Active token per outgoing flow.
	kept := make([]Token, 0, len(s.Tokens))
	for _, t := range s.Tokens {
		if t.NodeID == node.ID && t.State == TokenAtJoin {
			s.closeVisit(t.ID, t.NodeID, at)
			continue
		}
		kept = append(kept, t)
	}
	s.Tokens = kept
	for _, f := range def.Outgoing(node.ID) {
		s.placeToken(f.Target, at)
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:
```bash
go test ./engine/... -v
```
Expected: PASS (join test + all earlier engine tests).

- [ ] **Step 5: Add an end-to-end parallel diamond through the runtime**

Add to `runtime/example_test.go`:
```go
func TestRunnerExecutesParallelDiamond(t *testing.T) {
	def := &model.ProcessDefinition{
		ID: "diamond", Version: 1,
		Nodes: []model.Node{
			{ID: "start", Kind: model.KindStartEvent},
			{ID: "fork", Kind: model.KindParallelGateway},
			{ID: "a", Kind: model.KindServiceTask, Action: "a"},
			{ID: "b", Kind: model.KindServiceTask, Action: "b"},
			{ID: "join", Kind: model.KindParallelGateway},
			{ID: "end", Kind: model.KindEndEvent},
		},
		Flows: []model.SequenceFlow{
			{ID: "f1", Source: "start", Target: "fork"},
			{ID: "f2", Source: "fork", Target: "a"},
			{ID: "f3", Source: "fork", Target: "b"},
			{ID: "f4", Source: "a", Target: "join"},
			{ID: "f5", Source: "b", Target: "join"},
			{ID: "f6", Source: "join", Target: "end"},
		},
	}
	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"a": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return map[string]any{"a": true}, nil
		}),
		"b": action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
			return map[string]any{"b": true}, nil
		}),
	})
	r := runtime.NewRunner(cat, clock.System(), runtime.NewMemStateStore(), runtime.NewMemJournal(), runtime.NewMemOutbox())

	final, err := r.Run(t.Context(), def, "i1", nil)
	require.NoError(t, err)
	assert.Equal(t, engine.StatusCompleted, final.Status)
	assert.Empty(t, final.Tokens)
	assert.Equal(t, true, final.Variables["a"])
	assert.Equal(t, true, final.Variables["b"])
}
```

- [ ] **Step 6: Run the full suite + coverage + lint**

Run:
```bash
go test -race ./...
go test -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1
golangci-lint run ./...
```
Expected: all pass; total coverage ≥ 90%; lint clean. (`cover.out` is gitignored — do not commit it.)

- [ ] **Step 7: Commit**

```bash
git add engine/step.go engine/step_gateway_test.go runtime/example_test.go
git commit -m "feat(engine): parallel gateway synchronizing join + e2e diamond"
```

---

## Verification Checklist (Plan 2)

- [ ] `expreval.EvalBool` handles true/false/undefined-var/non-bool/syntax-error; memoizes compiled programs.
- [ ] `model.Validate` rejects conditions/defaults on non-conditional gateways and multiple defaults; accepts a valid exclusive gateway with a condition + a default.
- [ ] Exclusive gateway: takes the matching conditional branch, falls back to default, and errors (`ErrNoMatchingFlow`) when neither matches.
- [ ] Parallel fork creates one token per outgoing branch and both branches' actions fire in one macro step.
- [ ] Parallel join waits for every incoming branch, then proceeds once; the diamond completes exactly once with both branches' outputs merged.
- [ ] `Step` remains deterministic and pure (the Plan-1 `TestStepIsDeterministic`/`TestStepDoesNotMutateInput` still pass; flows are evaluated in definition order).
- [ ] `engine`/`model`/`expreval` import no transport/storage/bus/time-vendor packages; engine still never calls `time.Now()`.
- [ ] `go test -race ./...` green; coverage ≥ 85% on touched packages; `golangci-lint run ./...` clean.

## Self-Review Notes

- **Spec coverage (this slice):** §3 gateway validation (exclusive/parallel), §6 gateway routing semantics (exclusive conditional + default; parallel fork; parallel synchronizing join), §7 expr wrapper. Inclusive/OR-join (§3/§6) and event-based gateway deferred to Plan 3.
- **Determinism:** `def.Outgoing`/`Incoming` preserve definition order; forked/merged tokens get counter-derived IDs via `placeToken`; the shared `conditions` evaluator only memoizes (referentially transparent). No map iteration affects command/token order.
- **Purity:** all gateway helpers operate on the cloned `*InstanceState` inside `Step`; `consumeToken` already allocates a fresh slice (Plan-1 fix), so forking/joining never aliases the caller's tokens.
- **Join scope (acknowledged limitation):** `tryParallelJoin` counts `TokenAtJoin` tokens on the node against the incoming-flow count — correct for a single (non-nested, acyclic) parallel diamond, which is Plan 2's scope. Nested parallel regions and loops that re-enter a join are covered when scopes/loops land in a later plan.
- **Type consistency:** `drive` returns `([]Command, error)`; `Step`'s single call site updated; public `Step` signature unchanged. New sentinels live beside the existing engine/model sentinel blocks.

# Definition-Scoped Action Catalog, Optional Names & Inline Actions — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add per-definition action catalogs (with global fallback), optional action names that default to the node id, and node-local inline actions, applied to both `ServiceTask` and `BusinessRuleTask`.

**Architecture:** A single resolution chain — node-local inline → definition-scoped catalog → global catalog — governs every action reference at runtime. Inline funcs live on the node; the scoped catalog lives on `ProcessDefinition`; neither is serialized (execution uses the caller-supplied in-memory `*ProcessDefinition`, so funcs survive rehydration). `BusinessRuleTask`, currently unexecuted, gains an engine strategy mirroring `ServiceTask`.

**Tech Stack:** Go 1.25, `expr-lang/expr` (unaffected), existing `action`/`model`/`engine`/`runtime` root packages. Tests are black-box (`_test` packages), table-driven per the `table-test` skill where ≥2 cases share a call.

## Global Constraints

- Go 1.25; no new third-party dependencies.
- Public packages at module root, no `pkg/` prefix (ADR-0004).
- Error sentinel messages use the `workflow-<package>:` prefix (e.g. `workflow-model:`).
- Never import watermill/casbin/gocron/clockwork from engine/workflow code.
- TDD strict: every new symbol gets a failing test (visible red `go test`) before implementation.
- Per-package verification on completion: `go test -race -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1` (≥85% on touched packages), `go test ./...` green, `golangci-lint run ./...` clean.
- Black-box tests (`package <pkg>_test`); pair each `foo.go` with a same-named `foo_test.go`.
- Resolution precedence (the invariant every task serves): **inline → scoped → global**; main-task lookup key is the explicit name, or the **node id** when the name is empty; secondary actions (compensation, SLA, reminder, cancel handler, `CancelActions`) use **scoped → global** only.

---

### Task 1: `action.Resolve` — pure scoped→global lookup helper

**Files:**
- Create: `action/resolve.go`
- Test: `action/resolve_test.go`

**Interfaces:**
- Consumes: existing `action.Catalog` (`Resolve(name string) (ServiceAction, bool)`), `action.ServiceAction`, `action.MapCatalog`, `action.Func`.
- Produces: `func Resolve(scoped, global Catalog, name string) (ServiceAction, bool)` — used by the runtime in Task 4.

- [ ] **Step 1: Write the failing test**

Create `action/resolve_test.go`:

```go
package action_test

import (
	"context"
	"testing"

	"github.com/kartaladev/wrkflw/action"
)

func act(tag string) action.ServiceAction {
	return action.Func(func(_ context.Context, in map[string]any) (map[string]any, error) {
		return map[string]any{"tag": tag}, nil
	})
}

func TestResolve(t *testing.T) {
	scoped := action.NewMapCatalog(map[string]action.ServiceAction{"a": act("scoped")})
	global := action.NewMapCatalog(map[string]action.ServiceAction{"a": act("global"), "b": act("global-b")})

	tests := map[string]struct {
		scoped, global action.Catalog
		name           string
		assert         func(t *testing.T, got action.ServiceAction, ok bool)
	}{
		"scoped wins over global": {scoped, global, "a", func(t *testing.T, got action.ServiceAction, ok bool) {
			if !ok {
				t.Fatal("want ok")
			}
			out, _ := got.Do(context.Background(), nil)
			if out["tag"] != "scoped" {
				t.Fatalf("want scoped, got %v", out["tag"])
			}
		}},
		"falls back to global": {scoped, global, "b", func(t *testing.T, got action.ServiceAction, ok bool) {
			if !ok {
				t.Fatal("want ok")
			}
			out, _ := got.Do(context.Background(), nil)
			if out["tag"] != "global-b" {
				t.Fatalf("want global-b, got %v", out["tag"])
			}
		}},
		"nil scoped uses global": {nil, global, "a", func(t *testing.T, _ action.ServiceAction, ok bool) {
			if !ok {
				t.Fatal("want ok from global")
			}
		}},
		"nil global, scoped only": {scoped, nil, "a", func(t *testing.T, _ action.ServiceAction, ok bool) {
			if !ok {
				t.Fatal("want ok from scoped")
			}
		}},
		"both nil": {nil, nil, "a", func(t *testing.T, _ action.ServiceAction, ok bool) {
			if ok {
				t.Fatal("want miss")
			}
		}},
		"miss everywhere": {scoped, global, "zzz", func(t *testing.T, _ action.ServiceAction, ok bool) {
			if ok {
				t.Fatal("want miss")
			}
		}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, ok := action.Resolve(tc.scoped, tc.global, tc.name)
			tc.assert(t, got, ok)
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./action/...`
Expected: FAIL — `undefined: action.Resolve`.

- [ ] **Step 3: Write minimal implementation**

Create `action/resolve.go`:

```go
package action

// Resolve looks name up in the scoped catalog first, then the global catalog.
// Either catalog may be nil. It returns the first match and true, or nil and
// false when neither resolves the name. This is the scoped→global tier shared
// by every action reference at execution time; node-local inline actions take
// precedence over both and are handled by the caller.
func Resolve(scoped, global Catalog, name string) (ServiceAction, bool) {
	if scoped != nil {
		if a, ok := scoped.Resolve(name); ok {
			return a, true
		}
	}
	if global != nil {
		return global.Resolve(name)
	}
	return nil, false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./action/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add action/resolve.go action/resolve_test.go
git commit -m "feat(action): add scoped→global Resolve helper

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: model — inline actions, scoped catalog, option-based constructors, builder registration

This task is one commit because changing the `NewServiceTask`/`NewBusinessRuleTask` signatures breaks every call site; the build is only green again once the sweep (Steps 8–10) is done.

**Files:**
- Modify: `model/node.go` (add `inline` field to `ServiceTask` and `BusinessRuleTask`)
- Modify: `model/accessors.go` (add `InlineActionOf`)
- Modify: `model/definition.go` (add `scoped` field + `ScopedCatalog`; update package doc)
- Modify: `model/builder.go` (add `actions` accumulator, `RegisterAction`/`RegisterActionFunc`, wire scoped catalog + conflict/duplicate validation in `Build`)
- Modify: `model/node_constructors.go` (new option types, `WithActionName`/`WithAction`/`WithActionFunc`, `applyServiceTask`/`applyBusinessRule` on shared options, new constructor signatures)
- Create: `model/action_options_test.go` (new behavior)
- Modify: every caller of `NewServiceTask`/`NewBusinessRuleTask` (sweep — see Step 8)

**Interfaces:**
- Consumes: `action.ServiceAction`, `action.Func`, `action.MapCatalog`, `action.Catalog`.
- Produces:
  - `func NewServiceTask(id string, opts ...serviceTaskOption) Node`
  - `func NewBusinessRuleTask(id string, opts ...businessRuleOption) Node`
  - `func WithActionName(name string) interface{ serviceTaskOption; businessRuleOption }`
  - `func WithAction(a action.ServiceAction) interface{ serviceTaskOption; businessRuleOption }`
  - `func WithActionFunc(fn func(context.Context, map[string]any) (map[string]any, error)) interface{ serviceTaskOption; businessRuleOption }`
  - `func InlineActionOf(n Node) action.ServiceAction`
  - `func (d *ProcessDefinition) ScopedCatalog() action.Catalog`
  - `func (b *DefinitionBuilder) RegisterAction(name string, a action.ServiceAction) *DefinitionBuilder`
  - `func (b *DefinitionBuilder) RegisterActionFunc(name string, fn func(context.Context, map[string]any) (map[string]any, error)) *DefinitionBuilder`
  - `var ErrActionInlineAndNameConflict error`, `var ErrDuplicateScopedAction error`

- [ ] **Step 1: Write the failing test**

Create `model/action_options_test.go`:

```go
package model_test

import (
	"context"
	"errors"
	"testing"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/model"
)

func noopFn(_ context.Context, in map[string]any) (map[string]any, error) { return in, nil }

func TestServiceTaskActionOptions(t *testing.T) {
	tests := map[string]struct {
		node       model.Node
		wantName   string // model.ActionOf
		wantInline bool   // model.InlineActionOf != nil
	}{
		"named action":     {model.NewServiceTask("st", model.WithActionName("pay")), "pay", false},
		"default by id":    {model.NewServiceTask("st"), "", true === false && false || "" == "" && false}, // see note
		"inline action":    {model.NewServiceTask("st", model.WithAction(action.Func(noopFn))), "", true},
		"inline func":      {model.NewServiceTask("st", model.WithActionFunc(noopFn)), "", true},
		"businessrule name": {model.NewBusinessRuleTask("br", model.WithActionName("rule")), "rule", false},
		"businessrule inline": {model.NewBusinessRuleTask("br", model.WithAction(action.Func(noopFn))), "", true},
	}
	_ = tests // replaced below — the map literal above is illustrative only
}
```

> NOTE TO IMPLEMENTER: the `"default by id"` row above contains a deliberately
> broken expression so this file does not accidentally compile before the real
> symbols exist. Replace the entire `TestServiceTaskActionOptions` body with the
> clean version below before running Step 2. (Kept separate to make the red
> state unambiguous.)

Clean version to use:

```go
func TestServiceTaskActionOptions(t *testing.T) {
	tests := map[string]struct {
		node       model.Node
		wantName   string
		wantInline bool
	}{
		"named action":        {model.NewServiceTask("st", model.WithActionName("pay")), "pay", false},
		"empty default":       {model.NewServiceTask("st"), "", false},
		"inline action":       {model.NewServiceTask("st", model.WithAction(action.Func(noopFn))), "", true},
		"inline func":         {model.NewServiceTask("st", model.WithActionFunc(noopFn)), "", true},
		"businessrule name":   {model.NewBusinessRuleTask("br", model.WithActionName("rule")), "rule", false},
		"businessrule inline": {model.NewBusinessRuleTask("br", model.WithAction(action.Func(noopFn))), "", true},
		"with name + retry":   {model.NewServiceTask("st", model.WithActionName("pay"), model.WithName("Pay")), "pay", false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := model.ActionOf(tc.node); got != tc.wantName {
				t.Fatalf("ActionOf = %q, want %q", got, tc.wantName)
			}
			if got := model.InlineActionOf(tc.node) != nil; got != tc.wantInline {
				t.Fatalf("InlineActionOf present = %v, want %v", got, tc.wantInline)
			}
		})
	}
}

func TestRegisterActionScopedCatalog(t *testing.T) {
	def, err := model.NewDefinition("d", 1).
		RegisterAction("score", action.Func(noopFn)).
		RegisterActionFunc("notify", noopFn).
		Add(model.NewServiceTask("s", model.WithActionName("score"))).
		Add(model.NewEndEvent("e")).
		Connect("s", "e").
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	cat := def.ScopedCatalog()
	if cat == nil {
		t.Fatal("ScopedCatalog nil, want non-nil")
	}
	if _, ok := cat.Resolve("score"); !ok {
		t.Fatal("scoped catalog missing 'score'")
	}
	if _, ok := cat.Resolve("notify"); !ok {
		t.Fatal("scoped catalog missing 'notify'")
	}
}

func TestBuildRejectsInlineAndNameConflict(t *testing.T) {
	_, err := model.NewDefinition("d", 1).
		Add(model.NewServiceTask("s", model.WithActionName("x"), model.WithAction(action.Func(noopFn)))).
		Add(model.NewEndEvent("e")).
		Connect("s", "e").
		Build()
	if !errors.Is(err, model.ErrActionInlineAndNameConflict) {
		t.Fatalf("err = %v, want ErrActionInlineAndNameConflict", err)
	}
}

func TestBuildRejectsDuplicateScopedAction(t *testing.T) {
	_, err := model.NewDefinition("d", 1).
		RegisterAction("x", action.Func(noopFn)).
		RegisterAction("x", action.Func(noopFn)).
		Add(model.NewServiceTask("s", model.WithActionName("x"))).
		Add(model.NewEndEvent("e")).
		Connect("s", "e").
		Build()
	if !errors.Is(err, model.ErrDuplicateScopedAction) {
		t.Fatalf("err = %v, want ErrDuplicateScopedAction", err)
	}
}

func TestNoScopedActionsLeavesCatalogNil(t *testing.T) {
	def, err := model.NewDefinition("d", 1).
		Add(model.NewServiceTask("s", model.WithActionName("x"))).
		Add(model.NewEndEvent("e")).
		Connect("s", "e").
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if def.ScopedCatalog() != nil {
		t.Fatal("ScopedCatalog should be nil when nothing registered")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./model/...`
Expected: FAIL to compile — `undefined: WithActionName`, `WithAction`, `WithActionFunc`, `InlineActionOf`, `RegisterAction`, `RegisterActionFunc`, `ScopedCatalog`, `ErrActionInlineAndNameConflict`, `ErrDuplicateScopedAction`, and the new constructor arity.

- [ ] **Step 3: Add inline fields + InlineActionOf**

In `model/node.go`, add the unexported field to both task structs (keep existing fields):

```go
type ServiceTask struct {
	baseNode
	activityFields
	// Action is the service-action name; empty means default to the node id.
	Action string
	// inline is a node-local ServiceAction taking precedence over name lookup.
	// It is never serialized (re-attached in code on rehydration).
	inline action.ServiceAction
}
```

```go
// BusinessRuleTask executes a business rule action (by name or inline).
type BusinessRuleTask struct {
	baseNode
	activityFields
	Action string
	inline action.ServiceAction
}
```

Add the `action` import to `model/node.go`. In `model/accessors.go`, add below `ActionOf`:

```go
// InlineActionOf returns the node-local inline ServiceAction of a ServiceTask or
// BusinessRuleTask, or nil when the node has none (or is another kind). Inline
// actions are never serialized; a node decoded from JSONB always returns nil.
func InlineActionOf(n Node) action.ServiceAction {
	switch v := n.(type) {
	case ServiceTask:
		return v.inline
	case BusinessRuleTask:
		return v.inline
	default:
		return nil
	}
}
```

Add the `action` import to `model/accessors.go`.

- [ ] **Step 4: Add scoped catalog to ProcessDefinition**

In `model/definition.go`: update the package doc line (it currently claims "imports only the standard library") to "imports only the standard library and the in-repo `action` package (a pure leaf)". Add the field + accessor:

```go
type ProcessDefinition struct {
	ID      string
	Version int
	Nodes   []Node
	Flows   []SequenceFlow
	CancelActions []string
	// scoped is the optional definition-scoped action catalog. nil means none.
	// It is never serialized; resolution falls back to the global catalog on a
	// miss (see action.Resolve).
	scoped action.Catalog
}

// ScopedCatalog returns the definition-scoped action catalog, or nil when the
// definition registered no scoped actions.
func (d *ProcessDefinition) ScopedCatalog() action.Catalog { return d.scoped }
```

Add the `action` import to `model/definition.go`.

- [ ] **Step 5: Add option types + With* functions + shared-option dispatch**

In `model/node_constructors.go`, add the `context` and `action` imports, then add the new option interfaces and options:

```go
// serviceTaskOption configures a ServiceTask.
type serviceTaskOption interface{ applyServiceTask(s *ServiceTask) }

// businessRuleOption configures a BusinessRuleTask.
type businessRuleOption interface{ applyBusinessRule(b *BusinessRuleTask) }

// actionNameOpt sets the action name on a ServiceTask or BusinessRuleTask.
type actionNameOpt struct{ name string }

func (o actionNameOpt) applyServiceTask(s *ServiceTask)       { s.Action = o.name }
func (o actionNameOpt) applyBusinessRule(b *BusinessRuleTask) { b.Action = o.name }

// WithActionName sets the catalog action name. Resolved scoped→global at runtime.
// Mutually exclusive with WithAction/WithActionFunc (Build reports a conflict).
func WithActionName(name string) interface {
	serviceTaskOption
	businessRuleOption
} {
	return actionNameOpt{name}
}

// inlineActionOpt sets a node-local inline action.
type inlineActionOpt struct{ a action.ServiceAction }

func (o inlineActionOpt) applyServiceTask(s *ServiceTask)       { s.inline = o.a }
func (o inlineActionOpt) applyBusinessRule(b *BusinessRuleTask) { b.inline = o.a }

// WithAction attaches a node-local inline ServiceAction available to this node
// only. Mutually exclusive with WithActionName (Build reports a conflict).
func WithAction(a action.ServiceAction) interface {
	serviceTaskOption
	businessRuleOption
} {
	return inlineActionOpt{a}
}

// WithActionFunc is WithAction sugar wrapping a plain function as action.Func.
func WithActionFunc(fn func(context.Context, map[string]any) (map[string]any, error)) interface {
	serviceTaskOption
	businessRuleOption
} {
	return inlineActionOpt{action.Func(fn)}
}
```

Make the shared activity/name options satisfy the new interfaces. Add to `activityOnlyOption` (near its other apply* methods):

```go
func (o activityOnlyOption) applyServiceTask(s *ServiceTask)       { o.fn(&s.activityFields) }
func (o activityOnlyOption) applyBusinessRule(b *BusinessRuleTask) { o.fn(&b.activityFields) }
```

Add to `nameOpt`:

```go
func (o nameOpt) applyServiceTask(s *ServiceTask)       { s.name = o.name }
func (o nameOpt) applyBusinessRule(b *BusinessRuleTask) { b.name = o.name }
```

- [ ] **Step 6: Change the constructors to option-based signatures**

Replace `NewServiceTask` and `NewBusinessRuleTask` in `model/node_constructors.go`:

```go
// NewServiceTask constructs a ServiceTask. Set the action with WithActionName
// (catalog reference) or WithAction/WithActionFunc (node-local inline); with
// neither, the action name defaults to the node id at execution time. Other
// behaviour (retry, SLA, name, etc.) is configured via the shared activity options.
func NewServiceTask(id string, opts ...serviceTaskOption) Node {
	s := ServiceTask{baseNode: baseNode{id: id}}
	for _, o := range opts {
		o.applyServiceTask(&s)
	}
	return s
}

// NewBusinessRuleTask constructs a BusinessRuleTask. Action configuration mirrors
// NewServiceTask (WithActionName / WithAction / WithActionFunc / default-by-id).
func NewBusinessRuleTask(id string, opts ...businessRuleOption) Node {
	b := BusinessRuleTask{baseNode: baseNode{id: id}}
	for _, o := range opts {
		o.applyBusinessRule(&b)
	}
	return b
}
```

- [ ] **Step 7: Add builder registration + Build validation**

In `model/builder.go`: add the accumulator field and an import block for `context` + `errors` + `action`.

```go
type DefinitionBuilder struct {
	id            string
	version       int
	nodes         []Node
	flows         []SequenceFlow
	cancelActions []string
	actions       map[string]action.ServiceAction // scoped catalog accumulator; nil until first register
	dupAction     string                          // first duplicate-registered name, "" if none
}
```

Add sentinels (top of file, after imports):

```go
// ErrActionInlineAndNameConflict is returned by Build when a node carries both
// an inline action (WithAction/WithActionFunc) and an action name (WithActionName).
var ErrActionInlineAndNameConflict = errors.New("workflow-model: node has both an inline action and an action name")

// ErrDuplicateScopedAction is returned by Build when RegisterAction registered
// the same name twice.
var ErrDuplicateScopedAction = errors.New("workflow-model: duplicate scoped action name")
```

Registration methods:

```go
// RegisterAction adds a definition-scoped action under name, visible only to
// this definition (global catalog is the fallback). Returns the builder.
func (b *DefinitionBuilder) RegisterAction(name string, a action.ServiceAction) *DefinitionBuilder {
	if b.actions == nil {
		b.actions = make(map[string]action.ServiceAction)
	}
	if _, exists := b.actions[name]; exists && b.dupAction == "" {
		b.dupAction = name
	}
	b.actions[name] = a
	return b
}

// RegisterActionFunc is RegisterAction sugar wrapping a plain function.
func (b *DefinitionBuilder) RegisterActionFunc(name string, fn func(context.Context, map[string]any) (map[string]any, error)) *DefinitionBuilder {
	return b.RegisterAction(name, action.Func(fn))
}
```

Update `Build` to validate and wire the scoped catalog:

```go
func (b *DefinitionBuilder) Build() (*ProcessDefinition, error) {
	if b.dupAction != "" {
		return nil, fmt.Errorf("%w: %q", ErrDuplicateScopedAction, b.dupAction)
	}
	for _, n := range b.nodes {
		if ActionOf(n) != "" && InlineActionOf(n) != nil {
			return nil, fmt.Errorf("%w: node %q", ErrActionInlineAndNameConflict, n.ID())
		}
	}
	def := ProcessDefinition{
		ID:            b.id,
		Version:       b.version,
		Nodes:         b.nodes,
		Flows:         b.flows,
		CancelActions: b.cancelActions,
	}
	if b.actions != nil {
		def.scoped = action.NewMapCatalog(b.actions)
	}
	if err := Validate(&def); err != nil {
		return nil, err
	}
	return &def, nil
}
```

Add `"fmt"` to `model/builder.go` imports (used by the wrapped errors).

- [ ] **Step 8: Sweep all `NewServiceTask`/`NewBusinessRuleTask` call sites**

The signature change breaks every caller. Find them:

```bash
grep -rln "NewServiceTask\|NewBusinessRuleTask" --include="*.go" . | grep -v "model/node_constructors.go"
```

For each call `NewServiceTask("id", "actname", <opts...>)` rewrite to `NewServiceTask("id", model.WithActionName("actname"), <opts...>)` (drop `model.` inside `package model` tests). Same for `NewBusinessRuleTask`. Callers that were passing a literal `""` action become `NewServiceTask("id", <opts...>)` (default-by-id). This spans `examples/` (9 mains) and test files across `model`, `engine`, `runtime`, `internal`.

- [ ] **Step 9: Run the model package tests**

Run: `go test ./model/...`
Expected: PASS (the new tests plus the swept existing model tests).

- [ ] **Step 10: Build and test the whole module to confirm the sweep is complete**

Run: `go build ./... && go test ./...`
Expected: PASS. Any remaining compile error points to an un-swept call site — fix it.

- [ ] **Step 11: Commit**

```bash
git add -A
git commit -m "feat(model): inline actions, scoped catalog, option-based task constructors

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: engine — NodeID on InvokeAction, main-action default-by-id, BusinessRuleTask execution

**Files:**
- Modify: `engine/command.go` (add `NodeID` to `InvokeAction`)
- Create: `engine/main_action.go` (helper `mainActionName`)
- Create: `engine/main_action_test.go`
- Modify: `engine/step_nodes.go` (`serviceTaskStrategy.enter` sets NodeID + default; add `businessRuleTaskStrategy`; register it; update the "not in map" comment)
- Modify: `engine/step_timers.go` (`reinvokeServiceAction` sets NodeID + default)
- Modify: `engine/step.go` (drop `KindBusinessRuleTask` from the unhandled-kinds comment)
- Test: `engine/step_nodes_test.go` (add business-rule + NodeID assertions)

**Interfaces:**
- Consumes: `model.ActionOf`, `model.Node`, `serviceActionInput`, `armBoundaries`, `nodeStrategies` map.
- Produces: `InvokeAction{CommandID, NodeID, Name, Input}`; `func mainActionName(n model.Node) string`; `businessRuleTaskStrategy` registered under `model.KindBusinessRuleTask`.

- [ ] **Step 1: Write the failing test (helper + strategy behavior)**

Create `engine/main_action_test.go`:

```go
package engine

import (
	"testing"

	"github.com/kartaladev/wrkflw/model"
)

func TestMainActionName(t *testing.T) {
	tests := map[string]struct {
		node model.Node
		want string
	}{
		"explicit name":      {model.NewServiceTask("s", model.WithActionName("pay")), "pay"},
		"default to node id": {model.NewServiceTask("s"), "s"},
		"businessrule name":  {model.NewBusinessRuleTask("b", model.WithActionName("rule")), "rule"},
		"businessrule id":    {model.NewBusinessRuleTask("b"), "b"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := mainActionName(tc.node); got != tc.want {
				t.Fatalf("mainActionName = %q, want %q", got, tc.want)
			}
		})
	}
}
```

> This is a white-box test (`package engine`) because `mainActionName` is unexported. Keep it in `main_action_test.go`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./engine/ -run TestMainActionName`
Expected: FAIL — `undefined: mainActionName`.

- [ ] **Step 3: Implement the helper**

Create `engine/main_action.go`:

```go
package engine

import "github.com/kartaladev/wrkflw/model"

// mainActionName returns the lookup key for a task's primary action: the
// explicit action name, or the node id when no name was set (default-by-id).
// Inline actions take precedence at resolution time and are unaffected by this.
func mainActionName(n model.Node) string {
	if name := model.ActionOf(n); name != "" {
		return name
	}
	return n.ID()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./engine/ -run TestMainActionName`
Expected: PASS.

- [ ] **Step 5: Add NodeID to InvokeAction**

In `engine/command.go`, update the struct (keep the doc comment, extend it):

```go
// InvokeAction asks the runtime to run a ServiceAction. NodeID identifies the
// emitting node so the runtime can honour a node-local inline action; it is
// empty for secondary invocations (compensation, SLA, reminder) that resolve by
// name only. Its result returns as ActionCompleted/ActionFailed with the same CommandID.
type InvokeAction struct {
	CommandID string
	NodeID    string
	Name      string
	Input     map[string]any
}
```

(Existing keyed literals for compensation/SLA/reminder need no change — `NodeID` defaults to `""`.)

- [ ] **Step 6: Write the failing test for the two main-action emission paths**

Add to `engine/step_nodes_test.go` (black-box `engine_test`) a test that drives a one-node service task and a one-node business-rule task and asserts the emitted `InvokeAction` carries `NodeID` and the defaulted `Name`. Use the existing test harness in that file as the template for building a definition and calling `Step`/`drive`. Concretely assert:

```go
func TestServiceTaskEmitsInvokeActionWithNodeID(t *testing.T) {
	def := mustDef(t, // helper already used in this test file
		model.NewStartEvent("start"),
		model.NewServiceTask("work"), // no name → default-by-id
		model.NewEndEvent("end"),
		flow("start", "work"), flow("work", "end"),
	)
	cmds := driveFromStart(t, def) // helper pattern from existing tests
	ia := firstInvokeAction(t, cmds)
	if ia.NodeID != "work" {
		t.Fatalf("NodeID = %q, want work", ia.NodeID)
	}
	if ia.Name != "work" {
		t.Fatalf("Name = %q, want work (default-by-id)", ia.Name)
	}
}

func TestBusinessRuleTaskExecutes(t *testing.T) {
	def := mustDef(t,
		model.NewStartEvent("start"),
		model.NewBusinessRuleTask("rule", model.WithActionName("decide")),
		model.NewEndEvent("end"),
		flow("start", "rule"), flow("rule", "end"),
	)
	cmds := driveFromStart(t, def)
	ia := firstInvokeAction(t, cmds)
	if ia.NodeID != "rule" || ia.Name != "decide" {
		t.Fatalf("got NodeID=%q Name=%q, want rule/decide", ia.NodeID, ia.Name)
	}
}
```

> IMPLEMENTER: adapt `mustDef`, `flow`, `driveFromStart`, and `firstInvokeAction`
> to the actual helpers/idioms already present in `engine/step_nodes_test.go`
> (read the file first). If no `firstInvokeAction` helper exists, write a small
> local one that type-asserts the first `InvokeAction` from the `[]Command`. The
> business-rule test is the red proof that `KindBusinessRuleTask` is now executed
> (today it parks and emits nothing — this test FAILS before Step 7).

- [ ] **Step 7: Run test to verify it fails**

Run: `go test ./engine/ -run 'TestServiceTaskEmitsInvokeActionWithNodeID|TestBusinessRuleTaskExecutes'`
Expected: FAIL — service-task test fails on `NodeID`/`Name` (NodeID not set yet); business-rule test fails because no `InvokeAction` is emitted.

- [ ] **Step 8: Implement the strategy changes**

In `engine/step_nodes.go`, update `serviceTaskStrategy.enter` to set `NodeID` and the defaulted name:

```go
	cmds = append(cmds, InvokeAction{
		CommandID: cmdID,
		NodeID:    node.ID(),
		Name:      mainActionName(node),
		Input:     serviceActionInput(c.s, node),
	})
```

Add `businessRuleTaskStrategy` mirroring `serviceTaskStrategy` (same file):

```go
// businessRuleTaskStrategy handles KindBusinessRuleTask node entry. It mirrors
// serviceTaskStrategy: emit the primary InvokeAction (with default-by-id name +
// NodeID for inline resolution), park the token, and arm boundary events.
type businessRuleTaskStrategy struct{}

func (businessRuleTaskStrategy) enter(c *stepCtx, tok *Token, node model.Node) ([]Command, bool, error) {
	if _, ok := node.(model.BusinessRuleTask); !ok {
		tok.State = TokenWaitingCommand
		return nil, false, nil
	}
	var cmds []Command
	cmdID := c.s.nextCommandID()
	cmds = append(cmds, InvokeAction{
		CommandID: cmdID,
		NodeID:    node.ID(),
		Name:      mainActionName(node),
		Input:     serviceActionInput(c.s, node),
	})
	tok.State = TokenWaitingCommand
	tok.AwaitCommand = cmdID
	bndCmds, err := armBoundaries(c.tdef, c.s, tok.ID, node.ID(), c.at, c.eval)
	if err != nil {
		return cmds, false, err
	}
	cmds = append(cmds, bndCmds...)
	return cmds, false, nil
}
```

Register it in the `nodeStrategies` map (the one initialised near `step_nodes.go:60`):

```go
	model.KindServiceTask:      serviceTaskStrategy{},
	model.KindBusinessRuleTask: businessRuleTaskStrategy{},
```

Update the "Kinds NOT in this map" comment at `step_nodes.go:56` to remove `KindBusinessRuleTask`, and the same comment in `step.go:165`.

In `engine/step_timers.go`, update `reinvokeServiceAction`'s emission to set NodeID + default name:

```go
	cmds := []Command{InvokeAction{
		CommandID: cmdID,
		NodeID:    node.ID(),
		Name:      mainActionName(node),
		Input:     serviceActionInput(s, node),
	}}
```

- [ ] **Step 9: Run tests to verify they pass**

Run: `go test ./engine/...`
Expected: PASS (new tests + existing engine tests).

- [ ] **Step 10: Commit**

```bash
git add engine/
git commit -m "feat(engine): carry NodeID on InvokeAction, default action to node id, execute BusinessRuleTask

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: runtime — inline→scoped→global resolvers wired into perform()

**Files:**
- Create: `runtime/resolve_action.go` (`resolveActionFor`, `resolveActionName`)
- Create: `runtime/resolve_action_test.go`
- Modify: `runtime/runner.go` (rewire the `InvokeAction` and `InvokeCancelAction` cases in `perform`)

**Interfaces:**
- Consumes: `action.Resolve` (Task 1), `model.InlineActionOf`, `(*model.ProcessDefinition).Node`, `(*model.ProcessDefinition).ScopedCatalog`, `r.cat`.
- Produces:
  - `func (r *Runner) resolveActionFor(def *model.ProcessDefinition, nodeID, name string) (action.ServiceAction, bool)`
  - `func (r *Runner) resolveActionName(def *model.ProcessDefinition, name string) (action.ServiceAction, bool)`

- [ ] **Step 1: Write the failing test**

Create `runtime/resolve_action_test.go` (black-box `runtime_test`). Build a `Runner` with a global catalog via the exported constructor and definitions via `model.NewDefinition(...).RegisterAction(...)`. Assert precedence:

```go
package runtime_test

import (
	"context"
	"testing"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/model"
	"github.com/kartaladev/wrkflw/runtime"
)

func tag(name string) action.ServiceAction {
	return action.Func(func(_ context.Context, _ map[string]any) (map[string]any, error) {
		return map[string]any{"tag": name}, nil
	})
}

func tagOf(t *testing.T, a action.ServiceAction) string {
	t.Helper()
	out, err := a.Do(context.Background(), nil)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	return out["tag"].(string)
}

func TestRunnerResolveActionPrecedence(t *testing.T) {
	global := action.NewMapCatalog(map[string]action.ServiceAction{"x": tag("global"), "comp": tag("global-comp")})
	r := runtime.NewRunner(global, nil, nil) // clk/store nil: resolvers touch neither

	def, err := model.NewDefinition("d", 1).
		RegisterAction("x", tag("scoped")).
		Add(model.NewServiceTask("inlineNode", model.WithAction(tag("inline")))).
		Add(model.NewServiceTask("namedNode", model.WithActionName("x"))).
		Add(model.NewServiceTask("idNode")). // default-by-id, no catalog entry "idNode"
		Add(model.NewEndEvent("e")).
		Connect("inlineNode", "namedNode").Connect("namedNode", "idNode").Connect("idNode", "e").
		Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	tests := map[string]struct {
		nodeID, name string
		wantOK       bool
		wantTag      string
	}{
		"inline beats scoped+global": {"inlineNode", "x", true, "inline"},
		"scoped beats global":        {"namedNode", "x", true, "scoped"},
		"name-only (no nodeID)":      {"", "comp", true, "global-comp"},
		"miss":                       {"idNode", "idNode", false, ""},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, ok := r.ResolveActionForTest(def, tc.nodeID, tc.name)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && tagOf(t, got) != tc.wantTag {
				t.Fatalf("tag = %q, want %q", tagOf(t, got), tc.wantTag)
			}
		})
	}
}
```

> The resolvers are unexported. Either (a) make this an internal white-box test
> in `package runtime` (file `resolve_action_internal_test.go`) calling
> `r.resolveActionFor`/`r.resolveActionName` directly — PREFERRED, no test-only
> export — or (b) add a tiny exported `ResolveActionForTest` shim. Use (a): put
> the test in `package runtime` and call `r.resolveActionFor(def, tc.nodeID, tc.name)`
> directly (drop the `runtime.` qualifier and the `ResolveActionForTest` shim).
> Verify `runtime.NewRunner(global, nil, nil)` is the real signature
> (`NewRunner(cat action.Catalog, clk clock.Clock, store Store, opts ...Option)`);
> pass real `nil` for clk/store since the resolvers never dereference them.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./runtime/ -run TestRunnerResolveActionPrecedence`
Expected: FAIL — `r.resolveActionFor` undefined.

- [ ] **Step 3: Implement the resolvers**

Create `runtime/resolve_action.go`:

```go
package runtime

import (
	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/model"
)

// resolveActionName resolves name against the definition-scoped catalog first,
// then the runner's global catalog (action.Resolve). Used for every secondary
// action reference (compensation, SLA, reminder, cancel handler, CancelActions).
func (r *Runner) resolveActionName(def *model.ProcessDefinition, name string) (action.ServiceAction, bool) {
	var scoped action.Catalog
	if def != nil {
		scoped = def.ScopedCatalog()
	}
	return action.Resolve(scoped, r.cat, name)
}

// resolveActionFor resolves a node's primary action: a node-local inline action
// (highest precedence) when nodeID names a task carrying one, else the
// scoped→global name chain. nodeID may be empty for non-node invocations.
func (r *Runner) resolveActionFor(def *model.ProcessDefinition, nodeID, name string) (action.ServiceAction, bool) {
	if def != nil && nodeID != "" {
		if n, ok := def.Node(nodeID); ok {
			if inline := model.InlineActionOf(n); inline != nil {
				return inline, true
			}
		}
	}
	return r.resolveActionName(def, name)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./runtime/ -run TestRunnerResolveActionPrecedence`
Expected: PASS.

- [ ] **Step 5: Rewire `perform()`**

In `runtime/runner.go`, replace the `InvokeAction` resolution block (the `if r.cat == nil { … }` guard plus `a, ok := r.cat.Resolve(cmd.Name)`) with the new resolver — no special-casing nil catalog:

```go
		a, ok := r.resolveActionFor(def, cmd.NodeID, cmd.Name)
		if !ok {
			err := errors.New("unknown action: " + cmd.Name)
			aspan.RecordError(err)
			aspan.SetStatus(codes.Error, err.Error())
			return engine.NewActionFailed(r.clk.Now(), cmd.CommandID, "unknown action: "+cmd.Name, false), nil
		}
```

In the `InvokeCancelAction` case, replace the `if r.cat == nil { … }` guard plus `a, ok := r.cat.Resolve(cmd.Name)` with:

```go
		a, ok := r.resolveActionName(def, cmd.Name)
		if !ok {
			r.obs.tel.Logger.LogAttrs(ctx, slog.LevelWarn, "runtime: cancel action not found",
				slog.String("action", cmd.Name))
			return nil, nil
		}
```

(The "no catalog" warn/error branches are gone — a nil global catalog is now just one more miss, correctly handled when the scoped catalog also misses.)

- [ ] **Step 6: Run the runtime tests**

Run: `go test ./runtime/...`
Expected: PASS. If a pre-existing test asserted the literal `"no action catalog: …"` message, update it to the unified `"unknown action: …"` path (note the change in the commit body).

- [ ] **Step 7: Commit**

```bash
git add runtime/
git commit -m "feat(runtime): resolve actions inline→scoped→global in perform

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: docs — runnable Example, README snippet, ADR-0063, HANDOVER + memory

**Files:**
- Create: `runtime/example_scoped_action_test.go` (godoc `Example`)
- Modify: `README.md` (add a short "definition-scoped & inline actions" snippet, if a node-authoring section exists)
- Create: `docs/adr/0063-definition-scoped-action-catalog.md`
- Modify: `docs/plans/HANDOVER.md` (record completion + any follow-ups)

**Interfaces:**
- Consumes: the full public API produced by Tasks 1–4.

- [ ] **Step 1: Write the failing Example test**

Create `runtime/example_scoped_action_test.go`:

```go
package runtime_test

import (
	"context"
	"fmt"

	"github.com/kartaladev/wrkflw/action"
	"github.com/kartaladev/wrkflw/model"
)

// ExampleDefinitionBuilder_RegisterAction shows the three ways to bind an action
// to a task: a definition-scoped catalog entry referenced by name, a node-local
// inline function, and default-by-id (no name → the node id is the lookup key).
func ExampleDefinitionBuilder_RegisterAction() {
	score := action.Func(func(_ context.Context, in map[string]any) (map[string]any, error) {
		return map[string]any{"score": 42}, nil
	})
	def, err := model.NewDefinition("loan", 1).
		RegisterAction("score", score). // def-scoped, by name
		Add(model.NewStartEvent("start")).
		Add(model.NewServiceTask("risk", model.WithActionName("score"))).             // scoped→global
		Add(model.NewServiceTask("notify", model.WithActionFunc(func(_ context.Context, in map[string]any) (map[string]any, error) {
			return in, nil // node-local inline
		}))).
		Add(model.NewServiceTask("archive")). // default-by-id → looks up "archive"
		Add(model.NewEndEvent("end")).
		Connect("start", "risk").Connect("risk", "notify").
		Connect("notify", "archive").Connect("archive", "end").
		Build()
	if err != nil {
		fmt.Println("build error:", err)
		return
	}
	fmt.Println(def.ScopedCatalog() != nil)
	// Output: true
}
```

- [ ] **Step 2: Run it to verify it fails (then passes)**

Run: `go test ./runtime/ -run ExampleDefinitionBuilder_RegisterAction`
Expected: initially FAIL only if the API is incomplete; with Tasks 1–4 done it PASSES. (This Example is also compiled by `go test ./...`.)

- [ ] **Step 3: Write ADR-0063**

Create `docs/adr/0063-definition-scoped-action-catalog.md` in the Nygard template (Status/Date, Context, Decision, Consequences). Content to capture:

- **Context:** single global catalog; rigidity (no per-def isolation, mandatory names, no inline); JSONB persistence vs non-serializable funcs; execution uses the in-memory `*ProcessDefinition`.
- **Decision:** resolution precedence inline → scoped → global for all action references; main-task name defaults to node id; `WithAction`/`WithActionName`/`WithActionFunc` (mutually exclusive); `DefinitionBuilder.RegisterAction(Func)`; Option-A in-memory re-attach round-trip (funcs never serialized); `model → action` dependency accepted (action is a pure leaf); `BusinessRuleTask` now executed via its own strategy.
- **Consequences:** breaking constructor signatures (acceptable pre-consumer, library still in development); model is no longer stdlib-only; nil global catalog is no longer a special error; a rehydrated-from-JSONB definition has nil scoped/inline until the consumer re-registers it in code.

- [ ] **Step 4: Update README + HANDOVER**

Add the snippet from Step 1 to the README's node-authoring section if one exists (skip if it would be redundant; note the decision). Append a completion entry to `docs/plans/HANDOVER.md` summarizing the feature, ADR-0063, and any follow-ups (e.g. "scoped catalog is not surfaced in instance DTOs/gRPC snapshots — out of scope").

- [ ] **Step 5: Final full verification**

Run: `go test -race -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1`
Run: `golangci-lint run ./...`
Expected: all green; ≥85% coverage on `action`, `model`, `engine`, `runtime`.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "docs(action): ADR-0063 + example for definition-scoped action catalog

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review

**Spec coverage:**
- Definition-scoped catalog + global fallback → Tasks 1 (`Resolve`), 2 (`RegisterAction`/`ScopedCatalog`), 4 (wired into `perform`). ✓
- Scoped actions declared at build time → Task 2 (`RegisterAction`/`RegisterActionFunc`). ✓
- Optional name → default-by-id → Task 2 (constructors) + Task 3 (`mainActionName`). ✓
- Inline `WithAction` vs `WithActionName`, node-local → Task 2 (options + `inline` field + `InlineActionOf`) + Task 4 (precedence). ✓
- Applies to all action references → Task 4 (`resolveActionName` used by InvokeCancelAction; secondary InvokeAction emissions carry empty NodeID and fall through to scoped→global). ✓
- Both ServiceTask + BusinessRuleTask, incl. BusinessRuleTask execution → Task 2 (model symmetry) + Task 3 (`businessRuleTaskStrategy`). ✓
- Persistence Option A (no func serialization; in-memory re-attach) → Task 2 (`scoped`/`inline` unexported, not in wire form) — verified against `node_wire.go` (struct literals omit them). ✓
- ADR → Task 5. ✓

**Placeholder scan:** The only intentional "broken" code is the illustrative map in Task 2 Step 1, explicitly flagged and replaced by the clean version before running — every other step contains real code. No TBD/TODO. ✓

**Type consistency:** `Resolve(scoped, global, name)`, `resolveActionFor(def, nodeID, name)`, `resolveActionName(def, name)`, `mainActionName(n)`, `InlineActionOf(n)`, `ScopedCatalog()`, `RegisterAction(name, a)`, `WithActionName`/`WithAction`/`WithActionFunc`, `InvokeAction{CommandID, NodeID, Name, Input}` — names match across tasks. `InvokeAction.CommandID` is `string` (verified in `command.go`). ✓

## Notes & Risks

- **Test-harness adaptation (Task 3 Step 6):** the engine test helpers (`mustDef`, `flow`, drive helpers) must be matched to whatever already exists in `engine/step_nodes_test.go` — read it first.
- **Unexported-resolver testing (Task 4 Step 1):** prefer a white-box `package runtime` test file over a test-only export.
- **Pre-existing message assertions (Task 4 Step 6):** the removed "no action catalog" branch may break a runtime test asserting that string — update it and disclose in the commit.
- **Out of scope (queue as follow-ups):** surfacing the scoped catalog in instance DTOs / gRPC snapshots; YAML/BPMN authoring of inline actions (impossible to serialize — names only).

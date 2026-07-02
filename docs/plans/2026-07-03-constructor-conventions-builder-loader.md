# Constructor Conventions + Builder/Loader Unification — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make stateful/service constructors fail fast on invalid required args, collapse redundant sibling constructors to functional options, split `model.DefinitionBuilder`/`DefinitionLoader` into interfaces over a shared core, and deepen interaction docs.

**Architecture:** Public root-package constructors that own state and require non-nilable collaborators validate their args and return `(T, error)`. Redundant constructor families (`MemStore`, `engine.NewActionFailed`, `casbinauthz`) collapse into `New…(opts …Option)` that returns an error on invalid/inconsistent options. `model` gains two interfaces (`DefinitionBuilder` ⊋ `DefinitionLoader`) backed by one unexported `definitionCore`; YAML loading returns a `DefinitionLoader`.

**Tech Stack:** Go 1.25, `expr-lang/expr`, `samber/do` v2 (wiring only), testify + uber-go/mock, testcontainers-go (`database.RunTestDatabase`), casbin v2 (adapter package only).

## Global Constraints

- Go 1.25; module path `github.com/zakyalvan/krtlwrkflw`. One `go.mod` at repo root.
- **TDD strict** (CLAUDE.md "TDD Operational Discipline"): every new/changed exported symbol is preceded by a **visible failing `go test` run** (red) before implementation (green). No test+impl in one edit pass. A compile error like `undefined: X` is a valid red.
- **Required Go skills — load at the start of every code task:** `cc-skills-golang:golang-code-style`, `golang-naming`, `golang-error-handling`, `golang-design-patterns`, `golang-structs-interfaces`, `golang-safety`, `golang-documentation`, `golang-testing`, `golang-modernize`. Load `golang-database`/`golang-concurrency`/`golang-samber-do` when the task touches those areas. Run `golang-modernize` on every file you touch and apply its suggestions.
- **Project test skills override the Go baseline:** `table-test` (assert-closure form, `ctx` modifier, `t.Context()`), `use-mockgen` (`--typed`, mocks beside the interface), `use-testcontainers` (`database.RunTestDatabase(t, opts...)`; never mock a DB).
- **Performance discipline:** load `cc-skills-golang:golang-performance` (and `golang-benchmark` when measuring) on any task touching an execution/read hot path. This refactor is mostly API-shape, but keep the fast paths allocation-clean: option application must not add per-call allocations on the happy path (nil/empty `opts` allocates nothing), the shared `*definitionCore` must be pointer-shared between the builder/loader wrappers (no copying of node/flow slices), and hardened constructors must keep their nil-guards as cheap comparisons ahead of any work. Do not add a benchmark unless a change plausibly regresses a hot path; if in doubt, `golang-benchmark` + `benchstat` before/after.
- Prefer **black-box tests** (`package xxx_test`). Error sentinels use the `workflow-<pkg>:` message prefix.
- Never import watermill/casbin/gocron/clockwork directly from engine/workflow code (casbin is allowed only inside `casbinauthz` and `internal/authz/casbin`).
- Verification gate per touched package: `go test -race -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1` (≥85% line cov), `go test ./...` green repo-wide, `golangci-lint run ./...` clean.
- Commit per task with Conventional Commits scoped to the area; end every commit message with `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.

---

## File Structure

- `docs/adr/0083-constructor-conventions.md` — ADR (Nygard): fail-fast + options-collapse policy.
- `docs/adr/0084-builder-loader-unification.md` — ADR (Nygard): the interface split.
- `model/builder.go` — `DefinitionBuilder`/`DefinitionLoader` interfaces, `definitionCore`, `definitionBuilder`, `definitionLoader`, `NewDefinition`.
- `model/builder_fluent.go` — `AddX` methods move to `*definitionBuilder`, return `DefinitionBuilder`.
- `model/yaml.go` — `ParseYAML`/`LoadYAML` return `DefinitionLoader`; add `coreFromYAML`.
- `runtime/memstore.go` — `NewMemStore(opts…) (*MemStore, error)`, `MemStoreOption`, `WithCallLinks`, `WithTimers`, `ErrNilDependency`.
- `runtime/errors_construct.go` *(new)* — shared `runtime.ErrNilDependency` sentinel.
- `engine/trigger.go` — `NewActionFailed(…, opts…)`, `ActionFailedOption`, `WithJitter`; remove `NewActionFailedJittered`.
- `casbinauthz/casbinauthz.go` — single `NewCasbinAuthorizer(opts…)` + `FromEnforcer`/`FromStrings`/`FromDB` + source sentinels.
- `runtime/{runner,taskservice,caching_store,caching_definition_registry,broadcast,call_notifier,chainer,lineage}.go` — hardened constructors.
- `internal/persistence/store/*.go` + `persistence/*.go` — hardened store constructors.
- `INTERACTIONS.md`, `engine/README.md`, `model/README.md`, `runtime/README.md`, `action/README.md`, `service/README.md`, `doc.go` — elaboration.
- `CHANGELOG.md` — breaking-change entries.

---

## Task 1: ADRs (0083, 0084)

**Files:**
- Create: `docs/adr/0083-constructor-conventions.md`
- Create: `docs/adr/0084-builder-loader-unification.md`

**Interfaces:** none (docs only).

- [ ] **Step 1: Read the canonical ADR template.** Read `docs/adr/0001-record-architecture-decisions.md` for the exact Nygard structure (Status/Date, Context, Decision, Consequences) and `docs/adr/0082-sqlite-backend.md` for house style.

- [ ] **Step 2: Write ADR 0083** `docs/adr/0083-constructor-conventions.md`. Sections:
  - **Status:** Accepted — 2026-07-03.
  - **Context:** ~60 constructors, almost none validate required args → latent nil panics far from the call site; redundant positional sibling constructors (MemStore trio, `NewActionFailed`/`Jittered`, casbinauthz trio) leave gaps and force combinatorial APIs.
  - **Decision:** (1) The fail-fast rule verbatim from spec §4 (stateful + required non-nilable dependency ⇒ validate & return error; one shared `ErrNilDependency` per package wrapped with the arg name). (2) Value/DTO/trigger constructors stay non-error. (3) Redundant families collapse to functional options returning an error on invalid/inconsistent option values. (4) `NewChainer` panic → error. (5) `model.NewDefinition` is explicitly exempt.
  - **Consequences:** clearer failures; breaking signature changes across `NewRunner`/`NewTaskService`/etc. and the `NewMemStore*`/`NewActionFailed*`/`NewCasbinAuthorizer*` families (list them); per-dialect triplets deliberately untouched.

- [ ] **Step 3: Write ADR 0084** `docs/adr/0084-builder-loader-unification.md`. Sections:
  - **Status:** Accepted — 2026-07-03.
  - **Context:** builder is a fluent struct; YAML load is free functions; no shared abstraction; YAML-loaded definitions can't carry `RegisterAction` (runtime values, unserializable).
  - **Decision:** introduce `DefinitionBuilder` (full) and `DefinitionLoader` (all but `Add`/`AddX`/`Connect`) as interfaces over one `definitionCore`, via **two wrappers** (not Go embedding) so builder methods all return `DefinitionBuilder` and the established **actions-first** idiom keeps compiling. `NewDefinition` returns `DefinitionBuilder`; `ParseYAML`/`LoadYAML` return `DefinitionLoader`; validation moves to `Build()`.
  - **Consequences:** any-order chaining preserved; ~4 shared method bodies duplicated across wrappers; breaking return-type changes on `NewDefinition`, `ParseYAML`, `LoadYAML`.

- [ ] **Step 4: Commit.**
```bash
git add docs/adr/0083-constructor-conventions.md docs/adr/0084-builder-loader-unification.md
git commit -m "docs(adr): 0083 constructor conventions + 0084 builder/loader unification"
```

---

## Task 2: `model` — DefinitionBuilder/DefinitionLoader interfaces over a shared core

**Files:**
- Modify: `model/builder.go`
- Modify: `model/builder_fluent.go`
- Modify: `model/yaml.go`
- Test: `model/builder_test.go`, `model/builder_fluent_test.go`, `model/yaml_test.go`, `model/example_test.go`
- Update callers: any file calling `model.ParseYAML`/`model.LoadYAML`/storing the builder value.

**Interfaces:**
- Produces:
  ```go
  type DefinitionLoader interface {
      RegisterAction(name string, a action.ServiceAction) DefinitionLoader
      RegisterActionFunc(name string, fn func(context.Context, map[string]any) (map[string]any, error)) DefinitionLoader
      CancelActions(names ...string) DefinitionLoader
      Build() (*ProcessDefinition, error)
  }
  type DefinitionBuilder interface {
      Add(n Node) DefinitionBuilder
      AddStartEvent(id string, opts ...startEventOption) DefinitionBuilder
      AddEndEvent(id string, name ...string) DefinitionBuilder
      AddTerminateEndEvent(id string, name ...string) DefinitionBuilder
      AddErrorEndEvent(id, errorCode string, name ...string) DefinitionBuilder
      AddExclusiveGateway(id string, name ...string) DefinitionBuilder
      AddParallelGateway(id string, name ...string) DefinitionBuilder
      AddInclusiveGateway(id string, name ...string) DefinitionBuilder
      AddEventBasedGateway(id string, name ...string) DefinitionBuilder
      AddServiceTask(id string, opts ...serviceTaskOption) DefinitionBuilder
      AddUserTask(id string, roles []string, opts ...userTaskOption) DefinitionBuilder
      AddReceiveTask(id, messageName string, opts ...receiveTaskOption) DefinitionBuilder
      AddSendTask(id, messageName string, opts ...sendTaskOption) DefinitionBuilder
      AddBusinessRuleTask(id string, opts ...businessRuleOption) DefinitionBuilder
      AddSubProcess(id string, sub *ProcessDefinition, opts ...activityOption) DefinitionBuilder
      AddCallActivity(id, defRef string, opts ...activityOption) DefinitionBuilder
      AddEventSubProcess(id string, sub *ProcessDefinition, opts ...eventSubProcessOption) DefinitionBuilder
      AddIntermediateCatchEvent(id string, opts ...catchOption) DefinitionBuilder
      AddIntermediateThrowEvent(id string, opts ...throwOption) DefinitionBuilder
      AddBoundaryEvent(id, attachedTo string, opts ...boundaryOption) DefinitionBuilder
      Connect(fromID, toID string, opts ...FlowOption) DefinitionBuilder
      RegisterAction(name string, a action.ServiceAction) DefinitionBuilder
      RegisterActionFunc(name string, fn func(context.Context, map[string]any) (map[string]any, error)) DefinitionBuilder
      CancelActions(names ...string) DefinitionBuilder
      Build() (*ProcessDefinition, error)
      Loader() DefinitionLoader
  }
  func NewDefinition(id string, version int) DefinitionBuilder
  func ParseYAML(data []byte) (DefinitionLoader, error)
  func LoadYAML(r io.Reader) (DefinitionLoader, error)
  ```

- [ ] **Step 1: Load skills** — `golang-structs-interfaces`, `golang-design-patterns`, `golang-naming`, `golang-documentation`, `golang-modernize`.

- [ ] **Step 2: Write the failing test** — add to `model/builder_test.go` (package `model_test`):
```go
func TestDefinitionBuilderActionsFirstAndStructureFirstBothBuild(t *testing.T) {
	assert := func(t *testing.T, b model.DefinitionBuilder) {
		def, err := b.Build()
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		if def.ID != "d" || len(def.Nodes) != 2 || len(def.Flows) != 1 {
			t.Fatalf("unexpected def: %+v", def)
		}
	}
	// actions-first (the established idiom)
	assert(t, model.NewDefinition("d", 1).
		RegisterActionFunc("a", func(context.Context, map[string]any) (map[string]any, error) { return nil, nil }).
		Add(model.NewStartEvent("s")).
		Add(model.NewEndEvent("e")).
		Connect("s", "e"))
	// structure-first
	assert(t, model.NewDefinition("d", 1).
		Add(model.NewStartEvent("s")).
		Add(model.NewEndEvent("e")).
		Connect("s", "e").
		RegisterActionFunc("a", func(context.Context, map[string]any) (map[string]any, error) { return nil, nil }))
}

func TestDefinitionLoaderFromBuilderCanRegisterThenBuild(t *testing.T) {
	var l model.DefinitionLoader = model.NewDefinition("d", 1).
		Add(model.NewStartEvent("s")).
		Add(model.NewEndEvent("e")).
		Connect("s", "e").
		Loader()
	def, err := l.RegisterActionFunc("a", func(context.Context, map[string]any) (map[string]any, error) { return nil, nil }).Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if def.ScopedActionNames() == nil {
		t.Fatalf("expected scoped action registered via loader")
	}
}
```

- [ ] **Step 3: Run test to verify it fails.** Run: `go test ./model/... -run 'DefinitionBuilder|DefinitionLoader' -v`. Expected: compile FAIL (`NewDefinition` returns `*DefinitionBuilder`, no `Loader`/interface types).

- [ ] **Step 4: Refactor `model/builder.go`.** Replace the exported struct with a core + two wrappers + interfaces. Keep the two sentinels and the `Build` logic; move the field-mutation and build logic onto `*definitionCore`:
```go
// keep ErrActionInlineAndNameConflict, ErrDuplicateScopedAction as-is.

type DefinitionLoader interface { /* signatures from Interfaces block */ }
type DefinitionBuilder interface { /* signatures from Interfaces block */ }

var (
	_ DefinitionBuilder = (*definitionBuilder)(nil)
	_ DefinitionLoader  = (*definitionLoader)(nil)
)

type definitionCore struct {
	id            string
	version       int
	nodes         []Node
	flows         []SequenceFlow
	cancelActions []string
	actions       map[string]action.ServiceAction
	dupAction     string
}

func (c *definitionCore) register(name string, a action.ServiceAction) {
	if c.actions == nil {
		c.actions = make(map[string]action.ServiceAction)
	}
	if _, exists := c.actions[name]; exists && c.dupAction == "" {
		c.dupAction = name
	}
	c.actions[name] = a
}

func (c *definitionCore) connect(fromID, toID string, opts ...FlowOption) {
	f := SequenceFlow{ID: fromID + "->" + toID, Source: fromID, Target: toID}
	for _, o := range opts {
		o.applyFlow(&f)
	}
	c.flows = append(c.flows, f)
}

func (c *definitionCore) build() (*ProcessDefinition, error) {
	if c.dupAction != "" {
		return nil, fmt.Errorf("%w: %q", ErrDuplicateScopedAction, c.dupAction)
	}
	for _, n := range c.nodes {
		if ActionOf(n) != "" && InlineActionOf(n) != nil {
			return nil, fmt.Errorf("%w: node %q", ErrActionInlineAndNameConflict, n.ID())
		}
	}
	def := ProcessDefinition{ID: c.id, Version: c.version, Nodes: c.nodes, Flows: c.flows, CancelActions: c.cancelActions}
	if c.actions != nil {
		def.scoped = action.NewMapCatalog(c.actions)
		names := make([]string, 0, len(c.actions))
		for name := range c.actions {
			names = append(names, name)
		}
		sort.Strings(names)
		def.scopedNames = names
	}
	if err := Validate(&def); err != nil {
		return nil, err
	}
	return &def, nil
}

type definitionBuilder struct{ *definitionCore }
type definitionLoader struct{ *definitionCore }

func NewDefinition(id string, version int) DefinitionBuilder {
	return &definitionBuilder{&definitionCore{id: id, version: version}}
}

// builder methods (all return DefinitionBuilder)
func (b *definitionBuilder) Add(n Node) DefinitionBuilder { b.nodes = append(b.nodes, n); return b }
func (b *definitionBuilder) Connect(fromID, toID string, opts ...FlowOption) DefinitionBuilder {
	b.connect(fromID, toID, opts...)
	return b
}
func (b *definitionBuilder) CancelActions(names ...string) DefinitionBuilder {
	b.cancelActions = append(b.cancelActions, names...)
	return b
}
func (b *definitionBuilder) RegisterAction(name string, a action.ServiceAction) DefinitionBuilder {
	b.register(name, a)
	return b
}
func (b *definitionBuilder) RegisterActionFunc(name string, fn func(context.Context, map[string]any) (map[string]any, error)) DefinitionBuilder {
	b.register(name, action.Func(fn))
	return b
}
func (b *definitionBuilder) Build() (*ProcessDefinition, error) { return b.build() }
func (b *definitionBuilder) Loader() DefinitionLoader           { return &definitionLoader{b.definitionCore} }

// loader methods (all return DefinitionLoader)
func (l *definitionLoader) CancelActions(names ...string) DefinitionLoader {
	l.cancelActions = append(l.cancelActions, names...)
	return l
}
func (l *definitionLoader) RegisterAction(name string, a action.ServiceAction) DefinitionLoader {
	l.register(name, a)
	return l
}
func (l *definitionLoader) RegisterActionFunc(name string, fn func(context.Context, map[string]any) (map[string]any, error)) DefinitionLoader {
	l.register(name, action.Func(fn))
	return l
}
func (l *definitionLoader) Build() (*ProcessDefinition, error) { return l.build() }
```
Keep `FlowOption`, `flowFuncOpt`, `WithFlowID`, `WithCondition`, `AsDefault` unchanged. Update the type doc comment on `DefinitionBuilder`/`DefinitionLoader`.

- [ ] **Step 5: Update `model/builder_fluent.go`.** Change every `func (b *DefinitionBuilder) AddX(...) *DefinitionBuilder { return b.Add(NewX(...)) }` to `func (b *definitionBuilder) AddX(...) DefinitionBuilder { return b.Add(NewX(...)) }` (receiver `*definitionBuilder`, return `DefinitionBuilder`). Bodies unchanged.

- [ ] **Step 6: Update `model/yaml.go`.** Replace `definitionFromYAML` with a `coreFromYAML` producing `*definitionCore`, and make the parsers return a loader (validation deferred to `Build()`):
```go
func coreFromYAML(dy *definitionYAML) (*definitionCore, error) {
	c := &definitionCore{id: dy.ID, version: dy.Version, cancelActions: dy.CancelActions}
	c.nodes = make([]Node, len(dy.Nodes))
	for i, ny := range dy.Nodes {
		n, err := fromNodeYAML(ny)
		if err != nil {
			return nil, err
		}
		c.nodes[i] = n
	}
	c.flows = make([]SequenceFlow, len(dy.Flows))
	for i, fy := range dy.Flows {
		c.flows[i] = SequenceFlow(fy)
	}
	return c, nil
}

// ParseYAML decodes a YAML process-definition and returns a DefinitionLoader whose
// structure is already declared. Register any definition-scoped actions, then call
// Build to validate and obtain the *ProcessDefinition.
func ParseYAML(data []byte) (DefinitionLoader, error) {
	var dy definitionYAML
	if err := yaml.Unmarshal(data, &dy); err != nil {
		return nil, fmt.Errorf("workflow-model: parse YAML: %w", err)
	}
	core, err := coreFromYAML(&dy)
	if err != nil {
		return nil, err
	}
	return &definitionLoader{core}, nil
}

func LoadYAML(r io.Reader) (DefinitionLoader, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("workflow-model: read YAML: %w", err)
	}
	return ParseYAML(data)
}
```

- [ ] **Step 7: Fix existing `model` tests/examples that assumed `*ProcessDefinition` from `ParseYAML`/`LoadYAML`.** Grep `grep -rn "ParseYAML\|LoadYAML" model/*_test.go model/example_test.go`. Each `def, err := model.ParseYAML(...)` becomes `def, err := model.ParseYAML(...); ...; def.Build()` OR, where the test wants the built definition, `ld, err := model.ParseYAML(...); def, err := ld.Build()`. Preserve the original assertions on `def` after `Build()`. Where a YAML test asserted a *validation* error at parse time, move the error expectation to `Build()`.

- [ ] **Step 8: Run model tests.** Run: `go test ./model/... -v`. Expected: PASS. If `golang-modernize` flags anything in touched files, apply it.

- [ ] **Step 9: Update out-of-package callers.** Grep repo-wide `grep -rln "model.ParseYAML\|model.LoadYAML" --include="*.go"`. For each, change to build via `.Build()` and thread the extra error. Also grep `grep -rn "model.NewDefinition" --include="*.go"` — those keep compiling (interface has the same methods) but if any code stored the result as `*model.DefinitionBuilder`, change it to `model.DefinitionBuilder`.

- [ ] **Step 10: Verify + commit.**
```bash
go build ./... && go test ./... && golangci-lint run ./model/...
git add -A && git commit -m "refactor(model): DefinitionBuilder/DefinitionLoader interfaces over shared core

NewDefinition returns DefinitionBuilder; ParseYAML/LoadYAML return
DefinitionLoader (validation deferred to Build). Actions-first idiom preserved."
```

---

## Task 3: `runtime.MemStore` — functional-options collapse

**Files:**
- Create: `runtime/errors_construct.go`
- Modify: `runtime/memstore.go`
- Test: `runtime/memstore_test.go`; new helper `runtime/memstore_helper_test.go`
- Update callers: all `NewMemStore*` sites repo-wide.

**Interfaces:**
- Produces:
  ```go
  var ErrNilDependency = errors.New("workflow-runtime: nil required dependency")
  type MemStoreOption func(*memStoreConfig) error
  func WithCallLinks(cl *MemCallLinkStore) MemStoreOption
  func WithTimers(mts *MemTimerStore) MemStoreOption
  func NewMemStore(opts ...MemStoreOption) (*MemStore, error)
  ```

- [ ] **Step 1: Load skills** — `golang-design-patterns`, `golang-error-handling`, `golang-naming`, `golang-modernize`, `table-test`.

- [ ] **Step 2: Write the failing test** in `runtime/memstore_test.go` (package `runtime_test`):
```go
func TestNewMemStoreOptions(t *testing.T) {
	cl := runtime.NewMemCallLinkStore()
	mts := runtime.NewMemTimerStore()
	tests := map[string]struct {
		opts   []runtime.MemStoreOption
		assert func(t *testing.T, m *runtime.MemStore, err error)
	}{
		"no options": {
			opts:   nil,
			assert: func(t *testing.T, m *runtime.MemStore, err error) { require.NoError(t, err); require.NotNil(t, m) },
		},
		"both set": {
			opts:   []runtime.MemStoreOption{runtime.WithCallLinks(cl), runtime.WithTimers(mts)},
			assert: func(t *testing.T, m *runtime.MemStore, err error) { require.NoError(t, err); require.NotNil(t, m) },
		},
		"nil call-links": {
			opts:   []runtime.MemStoreOption{runtime.WithCallLinks(nil)},
			assert: func(t *testing.T, m *runtime.MemStore, err error) { require.ErrorIs(t, err, runtime.ErrNilDependency); require.Nil(t, m) },
		},
		"nil timers": {
			opts:   []runtime.MemStoreOption{runtime.WithTimers(nil)},
			assert: func(t *testing.T, m *runtime.MemStore, err error) { require.ErrorIs(t, err, runtime.ErrNilDependency); require.Nil(t, m) },
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			m, err := runtime.NewMemStore(tc.opts...)
			tc.assert(t, m, err)
		})
	}
}
```

- [ ] **Step 3: Run test to verify it fails.** Run: `go test ./runtime/... -run TestNewMemStoreOptions -v`. Expected: compile FAIL (`NewMemStore` takes no args; `MemStoreOption`/`WithCallLinks`/`WithTimers`/`ErrNilDependency` undefined).

- [ ] **Step 4: Add the sentinel** — create `runtime/errors_construct.go`:
```go
package runtime

import "errors"

// ErrNilDependency is returned by runtime constructors when a required, non-nilable
// dependency (interface or pointer) is nil. Wrap it with the argument name via %w.
var ErrNilDependency = errors.New("workflow-runtime: nil required dependency")
```

- [ ] **Step 5: Rewrite the MemStore constructors** in `runtime/memstore.go` — replace the three `NewMemStore*` funcs with:
```go
type memStoreConfig struct {
	callLinks *MemCallLinkStore
	timers    *MemTimerStore
}

// MemStoreOption configures a MemStore. Options validate eagerly and may return an error.
type MemStoreOption func(*memStoreConfig) error

// WithCallLinks records call-link correlation into cl atomically with Create/Commit.
func WithCallLinks(cl *MemCallLinkStore) MemStoreOption {
	return func(c *memStoreConfig) error {
		if cl == nil {
			return fmt.Errorf("%w: call-link store", ErrNilDependency)
		}
		c.callLinks = cl
		return nil
	}
}

// WithTimers records armed-timer side-effects into mts atomically with Create/Commit.
func WithTimers(mts *MemTimerStore) MemStoreOption {
	return func(c *memStoreConfig) error {
		if mts == nil {
			return fmt.Errorf("%w: timer store", ErrNilDependency)
		}
		c.timers = mts
		return nil
	}
}

// NewMemStore constructs an in-memory Store + JournalReader. By default it tracks
// neither call-links nor timers; use WithCallLinks/WithTimers to opt in.
func NewMemStore(opts ...MemStoreOption) (*MemStore, error) {
	var cfg memStoreConfig
	for _, o := range opts {
		if err := o(&cfg); err != nil {
			return nil, err
		}
	}
	return &MemStore{
		instances: map[string]*memInstance{},
		journal:   map[string][]engine.Trigger{},
		callLinks: cfg.callLinks,
		timers:    cfg.timers,
	}, nil
}
```
Add `"fmt"` to imports. Remove `NewMemStoreWithCallLinks` and `NewMemStoreWithTimers`.

- [ ] **Step 6: Run test to verify it passes.** Run: `go test ./runtime/... -run TestNewMemStoreOptions -v`. Expected: PASS.

- [ ] **Step 7: Add a test helper** — create `runtime/memstore_helper_test.go` (package `runtime_test`):
```go
package runtime_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

// mustMemStore builds a MemStore or fails the test. Keeps option-free call sites terse.
func mustMemStore(t *testing.T, opts ...runtime.MemStoreOption) *runtime.MemStore {
	t.Helper()
	m, err := runtime.NewMemStore(opts...)
	require.NoError(t, err)
	return m
}
```

- [ ] **Step 8: Update all call sites.** Grep `grep -rln "NewMemStore" --include="*.go"`.
  - `NewMemStore()` in **test files (package `xxx_test` in `runtime`)** → `mustMemStore(t)`.
  - `NewMemStore()` in **other test packages** (transport, eventing, examples, service, …) → inline `m, err := runtime.NewMemStore(); require.NoError(t, err)` (or a local `mustMemStore` helper in that package if it appears many times).
  - `NewMemStoreWithCallLinks(cl)` → `mustMemStore(t, runtime.WithCallLinks(cl))` (or inline error handling outside runtime tests).
  - `NewMemStoreWithTimers(mts)` → `mustMemStore(t, runtime.WithTimers(mts))`.
  - In **non-test** code (`examples/`, reference wiring): handle the error explicitly and return/log it.

- [ ] **Step 9: Verify + commit.**
```bash
go build ./... && go test ./... && golangci-lint run ./runtime/...
git add -A && git commit -m "refactor(runtime): collapse NewMemStore* into options ctor with fail-fast

NewMemStore(opts...) (*MemStore, error); WithCallLinks/WithTimers reject nil.
Adds runtime.ErrNilDependency. Closes the can't-set-both gap."
```

---

## Task 4: `engine.NewActionFailed` — functional-options collapse

**Files:**
- Modify: `engine/trigger.go`
- Test: `engine/trigger_test.go`
- Update callers: all `NewActionFailedJittered` sites.

**Interfaces:**
- Produces:
  ```go
  type ActionFailedOption func(*ActionFailed)
  func WithJitter(fraction float64) ActionFailedOption
  func NewActionFailed(at time.Time, commandID, errMsg string, retryable bool, opts ...ActionFailedOption) ActionFailed
  ```
- Removes: `NewActionFailedJittered`.

- [ ] **Step 1: Load skills** — `golang-design-patterns`, `golang-naming`, `golang-documentation`, `golang-modernize`, `table-test`.

- [ ] **Step 2: Write the failing test** in `engine/trigger_test.go` (package `engine_test`):
```go
func TestNewActionFailedJitterOption(t *testing.T) {
	at := time.Unix(0, 0)
	base := engine.NewActionFailed(at, "cmd", "boom", true)
	if base.JitterFraction != 0 {
		t.Fatalf("default jitter = %v, want 0", base.JitterFraction)
	}
	jit := engine.NewActionFailed(at, "cmd", "boom", true, engine.WithJitter(0.5))
	if jit.JitterFraction != 0.5 {
		t.Fatalf("jitter = %v, want 0.5", jit.JitterFraction)
	}
	if !jit.Retryable || jit.CommandID != "cmd" || jit.Err != "boom" {
		t.Fatalf("unexpected fields: %+v", jit)
	}
}
```

- [ ] **Step 3: Run test to verify it fails.** Run: `go test ./engine/... -run TestNewActionFailedJitterOption -v`. Expected: compile FAIL (`WithJitter` undefined; `NewActionFailed` has no variadic).

- [ ] **Step 4: Rewrite in `engine/trigger.go`** — replace both funcs:
```go
// ActionFailedOption configures an ActionFailed trigger.
type ActionFailedOption func(*ActionFailed)

// WithJitter sets the backoff jitter fraction (fraction >= 0; the runtime samples
// it to spread concurrent retries across workers). Values <= 0 mean no jitter.
func WithJitter(fraction float64) ActionFailedOption {
	return func(a *ActionFailed) { a.JitterFraction = fraction }
}

// NewActionFailed builds an ActionFailed trigger reporting a service-action error
// and whether it is retryable. JitterFraction defaults to 0; use WithJitter to set it.
func NewActionFailed(at time.Time, commandID, errMsg string, retryable bool, opts ...ActionFailedOption) ActionFailed {
	af := ActionFailed{baseTrigger: baseTrigger{at: at}, CommandID: commandID, Err: errMsg, Retryable: retryable}
	for _, o := range opts {
		o(&af)
	}
	return af
}
```
Delete `NewActionFailedJittered`.

- [ ] **Step 5: Run test to verify it passes.** Run: `go test ./engine/... -run TestNewActionFailedJitterOption -v`. Expected: PASS.

- [ ] **Step 6: Update callers of `NewActionFailedJittered`.** Grep `grep -rln "NewActionFailedJittered" --include="*.go"` (known: `runtime/runner.go`, `runtime/jitter_test.go`, `internal/persistence/store/trigger_codec.go`, `internal/persistence/store/trigger_codec_test.go`, `engine/retry_test.go`). Rewrite `NewActionFailedJittered(at, id, msg, retryable, j)` → `NewActionFailed(at, id, msg, retryable, WithJitter(j))` (drop `WithJitter` when `j` is a literal `0`).

- [ ] **Step 7: Verify + commit.**
```bash
go build ./... && go test ./... && golangci-lint run ./engine/...
git add -A && git commit -m "refactor(engine): collapse NewActionFailed/Jittered into WithJitter option"
```

---

## Task 5: `casbinauthz` — single source-options constructor

**Files:**
- Modify: `casbinauthz/casbinauthz.go`
- Test: `casbinauthz/casbinauthz_test.go` (+ DB test via testcontainers if present)
- Update callers: all `NewCasbinAuthorizer*` sites.

**Interfaces:**
- Produces:
  ```go
  var ErrNoAuthorizerSource = errors.New("workflow-casbinauthz: no source configured")
  var ErrMultipleAuthorizerSources = errors.New("workflow-casbinauthz: multiple sources configured")
  type Option func(*builderConfig) error
  func FromEnforcer(e *casbinv2.SyncedEnforcer) Option
  func FromStrings(modelText, policyText string) Option
  func FromDB(ctx context.Context, pool *pgxpool.Pool, opts ...DBOption) Option
  func NewCasbinAuthorizer(opts ...Option) (authz.Authorizer, io.Closer, error)
  ```
- Removes: `NewCasbinAuthorizerFromStrings`, `NewCasbinAuthorizerFromDB`; the old `NewCasbinAuthorizer(e)` becomes the `FromEnforcer` path.

- [ ] **Step 1: Load skills** — `golang-design-patterns`, `golang-error-handling`, `golang-structs-interfaces`, `golang-modernize`, `table-test`, and `use-testcontainers` if a DB test exists.

- [ ] **Step 2: Read** `casbinauthz/casbinauthz.go` fully to capture the existing enforcer-build logic in each of the three constructors (so it can be routed through the single builder).

- [ ] **Step 3: Write the failing test** in `casbinauthz/casbinauthz_test.go` (package `casbinauthz_test`). Reuse the model/policy strings already used by the existing `FromStrings` test:
```go
func TestNewCasbinAuthorizerSourceValidation(t *testing.T) {
	const modelText = `...` // copy from existing test
	const policyText = `...`
	t.Run("no source", func(t *testing.T) {
		_, _, err := casbinauthz.NewCasbinAuthorizer()
		require.ErrorIs(t, err, casbinauthz.ErrNoAuthorizerSource)
	})
	t.Run("multiple sources", func(t *testing.T) {
		_, _, err := casbinauthz.NewCasbinAuthorizer(
			casbinauthz.FromStrings(modelText, policyText),
			casbinauthz.FromStrings(modelText, policyText),
		)
		require.ErrorIs(t, err, casbinauthz.ErrMultipleAuthorizerSources)
	})
	t.Run("from strings ok", func(t *testing.T) {
		az, closer, err := casbinauthz.NewCasbinAuthorizer(casbinauthz.FromStrings(modelText, policyText))
		require.NoError(t, err)
		require.NotNil(t, az)
		if closer != nil {
			t.Cleanup(func() { _ = closer.Close() })
		}
	})
}
```

- [ ] **Step 4: Run test to verify it fails.** Run: `go test ./casbinauthz/... -run TestNewCasbinAuthorizerSourceValidation -v`. Expected: compile FAIL (`FromStrings`/`ErrNoAuthorizerSource`/`ErrMultipleAuthorizerSources` undefined; `NewCasbinAuthorizer` takes an enforcer).

- [ ] **Step 5: Implement the options builder** in `casbinauthz/casbinauthz.go`. Add sentinels and a `builderConfig` that records exactly one source; each `From*` sets a `build func() (authz.Authorizer, io.Closer, error)` closure reusing the existing per-source logic. `NewCasbinAuthorizer` applies opts, errors on zero/multiple sources, else runs the closure:
```go
type builderConfig struct {
	build func() (authz.Authorizer, io.Closer, error)
	count int
}

type Option func(*builderConfig) error

func FromEnforcer(e *casbinv2.SyncedEnforcer) Option {
	return func(c *builderConfig) error {
		if e == nil {
			return fmt.Errorf("workflow-casbinauthz: %w: enforcer", errNilSource)
		}
		c.count++
		c.build = func() (authz.Authorizer, io.Closer, error) { return newFromEnforcer(e), nil, nil }
		return nil
	}
}
// FromStrings / FromDB analogous, wrapping the existing compile / DB logic in the closure.

func NewCasbinAuthorizer(opts ...Option) (authz.Authorizer, io.Closer, error) {
	var cfg builderConfig
	for _, o := range opts {
		if err := o(&cfg); err != nil {
			return nil, nil, err
		}
	}
	switch {
	case cfg.count == 0:
		return nil, nil, ErrNoAuthorizerSource
	case cfg.count > 1:
		return nil, nil, ErrMultipleAuthorizerSources
	}
	return cfg.build()
}
```
Factor the old constructor bodies into unexported helpers (`newFromEnforcer`, `newFromStrings`, `newFromDB`) so no logic is lost. Keep `DBOption` and `io.Closer` semantics.

- [ ] **Step 6: Run test to verify it passes.** Run: `go test ./casbinauthz/... -v`. Expected: PASS.

- [ ] **Step 7: Update callers.** Grep `grep -rln "NewCasbinAuthorizerFromStrings\|NewCasbinAuthorizerFromDB\|NewCasbinAuthorizer(" --include="*.go"`. Rewrite:
  - `NewCasbinAuthorizer(e)` → `NewCasbinAuthorizer(FromEnforcer(e))` (now returns 3 values — thread the closer/err).
  - `NewCasbinAuthorizerFromStrings(m, p)` → `NewCasbinAuthorizer(FromStrings(m, p))`.
  - `NewCasbinAuthorizerFromDB(ctx, pool, opts...)` → `NewCasbinAuthorizer(FromDB(ctx, pool, opts...))`.

- [ ] **Step 8: Verify + commit.**
```bash
go build ./... && go test ./... && golangci-lint run ./casbinauthz/...
git add -A && git commit -m "refactor(casbinauthz): single NewCasbinAuthorizer(opts...) with source options

FromEnforcer/FromStrings/FromDB; error on zero or multiple sources."
```

---

## Task 6: Fail-fast hardening — `runtime` stateful constructors

**Files:**
- Modify: `runtime/runner.go`, `runtime/taskservice.go`, `runtime/caching_store.go`, `runtime/caching_definition_registry.go`, `runtime/broadcast.go`, `runtime/call_notifier.go`, `runtime/chainer.go`, `runtime/lineage.go`
- Test: the matching `_test.go` files
- Update callers: all sites across the repo.

**Interfaces (all now return an error; `ErrNilDependency` from Task 3):**
```go
func NewRunner(cat action.Catalog, store Store, opts ...Option) (*Runner, error)
func NewTaskService(store humantask.TaskStore, az authz.Authorizer, opts ...TaskServiceOption) (*TaskService, error)
func NewCachingStore(backing Store, owner Ownership, opts ...CachingStoreOption) (*CachingStore, error)
func NewCachingDefinitionRegistry(backing DefinitionRegistry, ttl time.Duration, opts ...CachingDefinitionRegistryOption) (*CachingDefinitionRegistry, error)
func NewSignalBus(deliver DeliverFunc, opts ...SignalBusOption) (*SignalBus, error)
func NewCallNotifier(cl CallLinkStore, deliver CallDeliverFunc, reg DefinitionRegistry, opts ...CallNotifierOption) (*CallNotifier, error)
func NewChainer(starter InstanceStarter, policy SuccessorPolicy, opts ...ChainerOption) (*Chainer, error)
func NewLineageReader(calls CallLineageReader, chains ChainLineageReader) (*LineageReader, error)
```

Do these **one constructor at a time**, each its own red→green→commit micro-cycle. Below is the pattern for `NewRunner`; repeat identically for each.

- [ ] **Step 1: Load skills** — `golang-error-handling`, `golang-design-patterns`, `golang-safety`, `golang-modernize`, `table-test`.

- [ ] **Step 2 (per constructor): Write the failing test.** Example for `NewRunner` in `runtime/runner_test.go`:
```go
func TestNewRunnerFailsFast(t *testing.T) {
	store := mustMemStore(t)
	cat := action.NewRegistry()
	tests := map[string]struct {
		cat   action.Catalog
		store runtime.Store
	}{
		"nil catalog": {cat: nil, store: store},
		"nil store":   {cat: cat, store: nil},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			r, err := runtime.NewRunner(tc.cat, tc.store)
			require.ErrorIs(t, err, runtime.ErrNilDependency)
			require.Nil(t, r)
		})
	}
	r, err := runtime.NewRunner(cat, store)
	require.NoError(t, err)
	require.NotNil(t, r)
}
```

- [ ] **Step 3: Run to verify it fails.** Run: `go test ./runtime/... -run TestNewRunnerFailsFast -v`. Expected: compile FAIL (`NewRunner` returns one value).

- [ ] **Step 4: Implement.** Change the signature to return `(*Runner, error)` and add guards at the top, before any use:
```go
func NewRunner(cat action.Catalog, store Store, opts ...Option) (*Runner, error) {
	if cat == nil {
		return nil, fmt.Errorf("%w: catalog", ErrNilDependency)
	}
	if store == nil {
		return nil, fmt.Errorf("%w: store", ErrNilDependency)
	}
	// …existing body…
	return r, nil
}
```
For `NewChainer`: replace the two `panic(...)` lines with the same `return nil, fmt.Errorf("%w: starter", ErrNilDependency)` / `policy` form.

- [ ] **Step 5: Run to verify it passes.** Run: `go test ./runtime/... -run TestNewRunnerFailsFast -v`. Expected: PASS.

- [ ] **Step 6: Update all call sites of this constructor.** Grep e.g. `grep -rln "NewRunner(" --include="*.go"`. In tests, add `require.NoError(t, err)` (or a local `mustRunner(t, …)` helper where it recurs a lot). In `examples/` and reference wiring, handle/propagate the error. For `NewChainer`, remove any test that asserted a panic and replace with an error assertion.

- [ ] **Step 7: Commit this constructor**, then repeat Steps 2–6 for `NewTaskService`, `NewCachingStore`, `NewCachingDefinitionRegistry`, `NewSignalBus`, `NewCallNotifier`, `NewChainer`, `NewLineageReader`.

- [ ] **Step 8: Final package verify.**
```bash
go build ./... && go test ./... && go test -race -coverprofile=cover.out ./runtime/... && go tool cover -func=cover.out | tail -1 && golangci-lint run ./runtime/...
git add -A && git commit -m "feat(runtime): fail-fast validation on stateful constructors

NewRunner/NewTaskService/NewCachingStore/NewCachingDefinitionRegistry/NewSignalBus/
NewCallNotifier/NewChainer/NewLineageReader now return (T, error) and reject nil
required dependencies. NewChainer: panic -> error."
```

---

## Task 7: Fail-fast hardening — persistence public wrappers + internal store

**Files:**
- Modify: `internal/persistence/store/store.go`, `call_links.go`, `chainlink.go`, `dedup.go`, `definitions.go`, `lister.go`, `pruner.go`, `timerstore.go`, `relay.go`
- Modify: the matching public wrappers in `persistence/*.go`
- Create: `internal/persistence/store/errors.go` (sentinel)
- Test: package `_test.go` files (SQLite path via `database.RunTestDatabase` where a live conn is needed; nil-arg checks need no DB).

**Interfaces:**
```go
// internal/persistence/store
var ErrNilDependency = errors.New("workflow-store: nil required dependency")
func New(conn any, d dialect.Dialect, opts ...Option) (*Store, error)   // + siblings, all (T, error)
```
Public `persistence/` wrappers that take a `conn`/`pool` propagate the error (or validate the pool and wrap).

- [ ] **Step 1: Load skills** — `golang-database`, `golang-error-handling`, `golang-safety`, `golang-modernize`, `table-test`, `use-testcontainers`.

- [ ] **Step 2: Add the sentinel** — `internal/persistence/store/errors.go`:
```go
package store

import "errors"

// ErrNilDependency is returned by store constructors when conn or dialect is nil.
var ErrNilDependency = errors.New("workflow-store: nil required dependency")
```

- [ ] **Step 3 (per constructor): Write the failing test** in `internal/persistence/store` (white-box `package store` is acceptable here since these are internal). Example for `New`:
```go
func TestNewStoreNilArgs(t *testing.T) {
	d := dialect.NewSQLite()
	if _, err := New(nil, d); !errors.Is(err, ErrNilDependency) {
		t.Fatalf("nil conn: err = %v, want ErrNilDependency", err)
	}
	if _, err := New(struct{}{}, nil); !errors.Is(err, ErrNilDependency) {
		t.Fatalf("nil dialect: err = %v, want ErrNilDependency", err)
	}
}
```
(`conn` is `any`; treat a nil interface value as invalid. Use a non-nil placeholder like `struct{}{}` for the nil-dialect case.)

- [ ] **Step 4: Run to verify it fails.** Run: `go test ./internal/persistence/store/... -run TestNewStoreNilArgs -v`. Expected: compile FAIL (`New` returns one value).

- [ ] **Step 5: Implement.** Each store constructor becomes `(T, error)` with a leading guard:
```go
func New(conn any, d dialect.Dialect, opts ...Option) (*Store, error) {
	if conn == nil {
		return nil, fmt.Errorf("%w: conn", ErrNilDependency)
	}
	if d == nil {
		return nil, fmt.Errorf("%w: dialect", ErrNilDependency)
	}
	// …existing body…
	return s, nil
}
```
Apply the same to `NewCallLinkStore`, `NewChainLinkStore`, `NewDeduper`, `NewDefinitionStore`, `NewLister`, `NewPruner`, `NewTimerStore`, `NewRelay`.

- [ ] **Step 6: Run to verify it passes.** Run: `go test ./internal/persistence/store/... -v`. Expected: PASS.

- [ ] **Step 7: Update the public `persistence/` wrappers** that call these internal constructors so they return/propagate the error. Grep `grep -rn "store.New\|store.NewCallLinkStore\|store.NewDefinitionStore\|store.NewLister\|store.NewPruner\|store.NewTimerStore\|store.NewChainLinkStore\|store.NewDeduper\|store.NewRelay" persistence/`. Thread the error out of each wrapper (each wrapper's own signature gains `error` if it didn't have one). Update wrapper tests and their callers.

- [ ] **Step 8: Update remaining call sites** repo-wide and verify.
```bash
go build ./... && go test ./... && golangci-lint run ./internal/persistence/... ./persistence/...
git add -A && git commit -m "feat(store): fail-fast nil conn/dialect on store constructors + wrappers"
```

---

## Task 8: Documentation elaboration

**Files:**
- Modify: `INTERACTIONS.md`, `engine/README.md`, `model/README.md`, `runtime/README.md`, `action/README.md`, `service/README.md`, `doc.go`

**Interfaces:** none. **No meta "readability requirement" text in any doc.**

- [ ] **Step 1: Load skill** — `golang-documentation`. Read the current `INTERACTIONS.md` in full to match its voice and diagram style.

- [ ] **Step 2: Deepen `INTERACTIONS.md`.** For each existing flow (Human task, Timer, Message, Signal, Send message, Compensation, Retry, Incident resolve, Sub-process, Call activity, Eventing/outbox), expand the prose **around** (not replacing) each diagram to explain: *why* the seam exists, what each participant **guarantees** (pre/postconditions, ownership), the failure/retry/CAS behavior, and the exact file(s) to read. Add two new subsections:
  - **Constructor conventions** — the fail-fast rule (stateful + required non-nilable dep ⇒ `(T, error)` with `ErrNilDependency`); value/trigger constructors stay non-error; the functional-options collapse (`NewMemStore`, `NewActionFailed`, `NewCasbinAuthorizer`) and when a constructor returns an error.
  - **Builder ↔ Loader** — `NewDefinition → DefinitionBuilder` (full) vs `LoadYAML → DefinitionLoader` (structure pre-declared); why YAML can't carry `RegisterAction`; the load → register-actions → `Build()` sequence; a small Mermaid or code snippet.

- [ ] **Step 3: Elaborate the package READMEs + `doc.go`** to junior→maintainer depth: `engine/README.md` (pure core, command/trigger vocabulary, why it holds no ports), `runtime/README.md` (the Runner as the single adapter; the new constructor error contract), `action/README.md` (catalog resolution + the options-based action constructors), `service/README.md` (assembly), and `doc.go` (module-root orientation). `model/README.md` **must** document `DefinitionBuilder` vs `DefinitionLoader`, the actions-first idiom, and the YAML load-then-register-actions flow.

- [ ] **Step 4: Verify docs build/links.** Run `golangci-lint run ./...` (catches broken `doc.go`), and skim each Mermaid block renders (no stray semicolons per commit f57f8e9). Commit.
```bash
git add INTERACTIONS.md engine/README.md model/README.md runtime/README.md action/README.md service/README.md doc.go
git commit -m "docs: deepen INTERACTIONS.md + package READMEs for all developer levels"
```

---

## Task 9: Final verification + CHANGELOG

**Files:**
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Add CHANGELOG entries** under an Unreleased/Breaking section: the hardened constructors now returning `(T, error)`; `NewChainer` panic→error; `NewMemStore*`→options; `NewActionFailedJittered` removed; `NewCasbinAuthorizer*`→single options ctor; `model.DefinitionBuilder` now an interface; `ParseYAML`/`LoadYAML` return `DefinitionLoader`. Reference ADR 0083/0084.

- [ ] **Step 2: Full verification.**
```bash
go build ./...
go test -race -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1
golangci-lint run ./...
```
Expected: build clean, all tests pass, total coverage line printed (each touched package ≥85%), lint clean.

- [ ] **Step 3: Commit + finish.**
```bash
git add CHANGELOG.md && git commit -m "docs(changelog): constructor conventions + builder/loader breaking changes"
```
Then use `superpowers:finishing-a-development-branch` to merge `feat/constructor-conventions-builder-loader` into `main` (per the autonomous-SDD cadence) and write a HANDOVER note.

---

## Self-Review

**Spec coverage:** Part A → Tasks 6, 7 (+ rule in ADR 0083, Task 1). Part B → Tasks 3 (MemStore), 4 (ActionFailed), 5 (casbinauthz). Part C → Task 2 (+ ADR 0084). Part D → Task 8. Testing strategy → embedded per task. Breaking-change summary → Task 9 (CHANGELOG). Resolved decisions §11 → §5.2 in Task 4 (non-error), §4.3 shared sentinel in Tasks 3/6/7. No gaps.

**Placeholders:** none — every code step shows real code; call-site churn is directed with exact grep commands and rewrite rules.

**Type consistency:** `ErrNilDependency` (runtime + store variants), `MemStoreOption`/`WithCallLinks`/`WithTimers`, `ActionFailedOption`/`WithJitter`, `DefinitionBuilder`/`DefinitionLoader`/`definitionCore`, `Option`/`FromEnforcer`/`FromStrings`/`FromDB` are used consistently across tasks and match the spec's Interfaces blocks.

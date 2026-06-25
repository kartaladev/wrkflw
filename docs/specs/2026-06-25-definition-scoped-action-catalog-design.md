# Definition-Scoped Action Catalog, Optional Names & Inline Actions — Design

- **Status:** Proposed
- **Date:** 2026-06-25
- **Related ADR:** 0063 (to be written)

## Problem

Today a `ServiceTask` / `BusinessRuleTask` references its action by a **mandatory
name string**, and that name is resolved at runtime against a **single global
`action.Catalog`** injected once into `NewRunner(cat, …)` and shared by every
definition and instance (`runtime/runner.go → perform()`,
`r.cat.Resolve(cmd.Name)`).

This is rigid in three ways the consumer wants relaxed:

1. **No per-definition isolation.** Every action a definition needs must be in
   the one global catalog; definitions cannot carry their own private actions,
   and there is no fallback story (definition-private overrides + shared
   defaults).
2. **Names are mandatory and verbose.** A task must be given an explicit action
   name even when the node id would do.
3. **No inline actions.** A consumer cannot attach a Go function directly to the
   node it belongs to; everything must be pre-registered by name in the global
   catalog, away from the definition that uses it.

## Goals

- A **definition-scoped catalog**: actions registered on a definition are
  visible only to that definition; on a miss the engine **falls back to the
  global catalog**.
- Scoped actions are declared **at definition-build time**.
- The action **name is optional** on `ServiceTask`/`BusinessRuleTask`; when
  omitted it **defaults to the node id**.
- **Inline actions** attachable per node: `WithAction(fn)` (inline,
  node-local) vs `WithActionName(name)` (catalog reference). An inline action is
  available to **that node only**.
- The scoped→global fallback applies to **all action references** — the main
  task action *and* compensation, SLA-breach, reminder, cancel-handler, and
  definition-level `CancelActions`.

## Non-Goals

- Serializing Go functions. Inline actions and scoped Go-func catalogs are
  in-memory only (see "Persistence" below). JSONB stores names only.
- Build-time validation that a name is resolvable. Catalogs are a runtime
  concern; an unresolved name remains a runtime error (existing behaviour, with
  a clearer message).
- Changing the `ServiceAction` interface or its `map[string]any` I/O contract.

## Key Constraint: Persistence / Rehydration (decided: Option A)

Definitions persist as JSONB (`model/node_wire.go`), and Go funcs cannot be
serialized. This is reconciled by the fact that **execution always operates on
the caller-supplied in-memory `*model.ProcessDefinition`**, never on a value
re-deserialized from JSONB:

- `Run`, `Deliver`, `DeliverMessage`, `ResolveIncident`, `CancelInstance` all
  take `def *model.ProcessDefinition` as a parameter.
- Call-activities and timer rehydration resolve defs via
  `DefinitionRegistry.Lookup` → `MapDefinitionRegistry` holds in-memory
  `*ProcessDefinition` values (`runner.go:695, 916, 1107`).

**Decision (Option A): in-memory, re-attached in code.** Inline funcs live
node-local on the task; the scoped catalog lives on `ProcessDefinition`. Neither
is serialized. JSONB persists only the action name (empty when the node uses an
inline action or relies on default-by-id). On restart the consumer rebuilds and
re-registers the same definition objects in Go (the code that authored them),
restoring the funcs. This honours the durable-first engine without any new
persistence machinery and is the only option that keeps an inline action
*strictly node-local* (it is physically stored on the node, not in a shared
keyspace).

## Resolution Model (the heart of the design)

A single precedence chain governs every action reference at runtime:

1. **Node-local inline** — only for the *main* action of a
   `ServiceTask`/`BusinessRuleTask`, set via `WithAction`. Highest precedence.
2. **Definition-scoped catalog** — `def.ScopedCatalog().Resolve(name)`.
3. **Global catalog** — `r.cat.Resolve(name)`. Fallback.

The lookup **key** (`name`):

- **Main task action:** the explicit name from `WithActionName` if set; otherwise
  **the node id** (default-by-id).
- **Secondary actions** (compensation, SLA, reminder, cancel-handler, definition
  `CancelActions`): always explicit names. They use tiers 2–3 only (no inline,
  no default-by-id).

If no tier resolves, return a clear `unknown action: <name>` failure (the
existing `ActionFailed` path). The current early error when `r.cat == nil` is
relaxed: a definition may supply all its actions via the scoped catalog with a
nil global catalog; only a genuine miss across all tiers fails.

## API Changes

### `action` package (pure leaf — no new deps)

Add a pure helper for tiers 2–3 (reused by the runner, independently testable):

```go
// Resolve looks up name in the scoped catalog first, then the global catalog.
// Either may be nil.
func Resolve(scoped, global Catalog, name string) (ServiceAction, bool)
```

### `model` package (gains a `model → action` import; `action` is a pure leaf, no cycle)

`ServiceTask` and `BusinessRuleTask` each gain a node-local inline action; the
`Action` field becomes optional:

```go
type ServiceTask struct {
    baseNode
    activityFields
    Action string                 // optional name; "" → default to node id
    inline action.ServiceAction   // node-local; nil if none; NEVER serialized
}
func (s ServiceTask) Inline() action.ServiceAction { return s.inline }
// (identical addition on BusinessRuleTask)
```

`ProcessDefinition` gains a definition-scoped catalog:

```go
type ProcessDefinition struct {
    ID, Version int
    Nodes []Node; Flows []SequenceFlow; CancelActions []string
    scoped action.Catalog          // def-scoped; nil if none; NEVER serialized
}
func (d *ProcessDefinition) ScopedCatalog() action.Catalog { return d.scoped }
```

**Constructors (breaking change — positional action dropped):**

```go
func NewServiceTask(id string, opts ...serviceTaskOption) Node
func NewBusinessRuleTask(id string, opts ...businessRuleOption) Node
```

**Node options (mutually exclusive on a node):**

```go
func WithActionName(name string) // sets Action (catalog reference)
func WithAction(a action.ServiceAction) // sets node-local inline
func WithActionFunc(fn func(ctx context.Context, in map[string]any) (map[string]any, error)) // convenience: wraps action.Func
```

`Build()` returns an error if a node carries **both** an inline action and an
action name (mutually exclusive). New sentinel:
`ErrActionInlineAndNameConflict` (message prefixed `workflow-model: …`).

**`DefinitionBuilder`:**

```go
func (b *DefinitionBuilder) RegisterAction(name string, a action.ServiceAction) *DefinitionBuilder
func (b *DefinitionBuilder) RegisterActionFunc(name string, fn func(context.Context, map[string]any) (map[string]any, error)) *DefinitionBuilder
```

Accumulates into a map; `Build()` wires an `action.MapCatalog` onto
`ProcessDefinition.scoped` (nil when none registered). Registering the same
name twice is a `Build()` error.

**Wire format (`model/node_wire.go`):** unchanged shape. `inline` and `scoped`
are not serialized. `Action` round-trips as-is (empty for inline / default-by-id
nodes). A rehydrated-from-JSONB definition has nil inline/scoped — by design,
the consumer re-attaches in code (Option A).

### `engine` package

> **Superseded — see ADR-0063.** This section's `InvokeAction.NodeID` + runtime
> `def.Node(nodeID)` lookup design was changed during implementation: a flat
> top-level node lookup could not resolve inline/scoped actions for nodes inside
> sub-processes. The shipped design instead has the engine carry the resolved
> inline action and scope-effective scoped catalog on the command
> (`InvokeAction.Inline` + `InvokeAction.Scoped`; `NodeID` removed). The rest of
> this doc is retained as the point-in-time design record; ADR-0063 is authoritative.

`InvokeAction` gains a node id so the runner can find the node for inline lookup:

```go
type InvokeAction struct {
    CommandID CommandID
    NodeID    string   // NEW
    Name      string
    Input     map[string]any
}
```

`serviceTaskStrategy.enter` (and the BusinessRuleTask equivalent) default the
name and stamp the node id:

```go
name := task.Action
if name == "" { name = node.ID() }
cmds = append(cmds, InvokeAction{CommandID: cmdID, NodeID: node.ID(), Name: name, Input: …})
```

The engine does **not** touch inline actions or catalogs — it only defaults the
name and passes the node id; all resolution stays in the runtime. Secondary
action commands (cancel, SLA, reminder, compensation) are unchanged in shape;
their names already flow through.

### `runtime` package

Two resolver methods on `Runner`:

```go
// full chain (inline → scoped → global); used for the main InvokeAction
func (r *Runner) resolveActionFor(def *model.ProcessDefinition, nodeID, name string) (action.ServiceAction, bool)

// name-only chain (scoped → global); used for every secondary action reference
func (r *Runner) resolveActionName(def *model.ProcessDefinition, name string) (action.ServiceAction, bool)
```

`resolveActionFor` checks `def.Node(nodeID)` → if it is a `ServiceTask`/
`BusinessRuleTask` with a non-nil `Inline()`, return it; otherwise delegate to
`resolveActionName`. `resolveActionName` calls `action.Resolve(def.ScopedCatalog(), r.cat, name)`.

`perform()` is updated: the `InvokeAction` case uses `resolveActionFor(def,
cmd.NodeID, cmd.Name)`; `InvokeCancelAction` and every other call site that
currently does `r.cat.Resolve(name)` uses `resolveActionName(def, name)`. The
nil-catalog early-return is replaced by "attempt all tiers, fail only on a true
miss".

## Authoring Example (godoc-style, for library consumers)

```go
def, _ := model.NewDefinition("loan-approval", 1).
    RegisterAction("score", scoreAction).                                  // def-scoped, by name
    Add(model.NewServiceTask("risk", model.WithActionName("score"))).      // scoped→global by name
    Add(model.NewServiceTask("notify", model.WithAction(notifyFn))).       // node-local inline
    Add(model.NewServiceTask("archive")).                                  // no name → resolves by id "archive"
    Connect("risk", "notify").Connect("notify", "archive").
    Build()
```

## Components & Boundaries

| Unit | Responsibility | Depends on |
|---|---|---|
| `action.Resolve` | pure scoped→global name lookup | `action.Catalog` only |
| `model` options/builder | declare inline + scoped actions, default-by-id, conflict validation | `action` (pure leaf) |
| `engine` InvokeAction defaulting | compute lookup key + carry node id | `model` |
| `runtime` resolvers | full + name-only chains, wired into `perform()` | `action`, `model` |

## Error Handling

- `Build()` → `ErrActionInlineAndNameConflict` when a node has both inline + name.
- `Build()` → duplicate-scoped-name error on `RegisterAction` collision.
- Runtime miss across all tiers → `ActionFailed` with `unknown action: <name>`
  (retryable=false), matching today's semantics; `r.cat == nil` is no longer a
  special-cased early failure.

## Testing Strategy (TDD strict, black-box `_test` packages)

- **`action`**: table test for `Resolve` precedence and nil scoped/global.
- **`model`**: `WithActionName`/`WithAction`/`WithActionFunc` storage;
  default-by-id (empty name); `RegisterAction` → `ScopedCatalog`; `Build`
  conflict + duplicate errors; wire round-trip drops inline, preserves `Action`.
- **`engine`**: `serviceTaskStrategy` emits `InvokeAction` with `NodeID` and the
  defaulted name (empty `Action` ⇒ `Name == node.ID()`); same for
  BusinessRuleTask.
- **`runtime`**: `resolveActionFor` precedence (inline > scoped > global);
  `resolveActionName` (scoped > global); scoped-only with nil global; unknown →
  error; every secondary call site routes through `resolveActionName`.
- **Example test**: a `func Example…` exercising the authoring snippet above.

Coverage target ≥ 85% on touched packages; `go test ./...` green;
`golangci-lint run ./...` clean.

## Migration / Impacted Call Sites

Breaking constructor change ripples to:

- All existing `NewServiceTask(id, action, …)` / `NewBusinessRuleTask(id, action, …)`
  call sites → `New…Task(id, model.WithActionName(action), …)`.
- `examples/` reference wiring.
- YAML / BPMN authoring loaders that call these constructors (the JSONB wire
  path uses struct literals, not constructors, so it is unaffected — but
  authoring loaders must be checked and updated).

The plan must enumerate and update every call site; `go build ./...` is the
backstop.

## ADR

Record **ADR-0063** (Nygard template): the resolution precedence, the
`model → action` dependency, optional-name/default-by-id, and the Option-A
in-memory re-attach round-trip model.

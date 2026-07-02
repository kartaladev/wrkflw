# 0084. Builder/Loader unification: interfaces over a shared core

Status: **Accepted — 2026-07-03.**
Spec: `docs/specs/2026-07-03-constructor-conventions-and-builder-loader.md`.

## Context

`model.DefinitionBuilder` is a fluent concrete struct. YAML loading is provided by two free
functions (`ParseYAML`/`LoadYAML`) that return `*ProcessDefinition` directly. There is no
shared abstraction between the two entry points.

This creates a critical gap: a definition loaded from YAML **cannot** carry definition-scoped
actions (`RegisterAction`), because `action.ServiceAction` values are runtime Go values that
are not serializable. A consumer who loads a definition from YAML has no ergonomic,
type-guided way to attach those actions before building. The only workaround is to call
`ParseYAML`, discard the result, and re-declare the structure imperatively — which defeats
the purpose of YAML authoring.

Additionally, because `DefinitionBuilder` is a struct (not an interface), consumers cannot
accept or return a "thing that can be built" without coupling to the concrete type, and
mock/test doubles are impossible without embedding the struct.

## Decision

We introduce two interfaces over a single unexported shared core (`*definitionCore`), using
two thin wrapper types — **not Go embedding of one interface into the other** — so that every
builder method returns `DefinitionBuilder` (preserving any-order chaining) and every loader
method returns `DefinitionLoader`.

### 1. The two interfaces

```go
// DefinitionLoader is the reduced surface for a definition whose structure
// (nodes + sequence flows) is already declared — e.g. loaded from YAML.
// It can still attach definition-scoped actions and build, but cannot add
// nodes or flows.
type DefinitionLoader interface {
    RegisterAction(name string, a action.ServiceAction) DefinitionLoader
    RegisterActionFunc(name string, fn func(context.Context, map[string]any) (map[string]any, error)) DefinitionLoader
    CancelActions(names ...string) DefinitionLoader
    Build() (*ProcessDefinition, error)
}

// DefinitionBuilder is the full authoring surface: everything a DefinitionLoader
// offers, plus structural declaration (Add/AddX/Connect).
type DefinitionBuilder interface {
    Add(n Node) DefinitionBuilder
    AddStartEvent(id string, opts ...startEventOption) DefinitionBuilder
    // …all existing AddX methods, each returning DefinitionBuilder…
    Connect(fromID, toID string, opts ...FlowOption) DefinitionBuilder

    RegisterAction(name string, a action.ServiceAction) DefinitionBuilder
    RegisterActionFunc(name string, fn func(context.Context, map[string]any) (map[string]any, error)) DefinitionBuilder
    CancelActions(names ...string) DefinitionBuilder
    Build() (*ProcessDefinition, error)

    // Loader returns the reduced DefinitionLoader view over the same state.
    Loader() DefinitionLoader
}
```

`DefinitionBuilder` does **not** Go-`embed` `DefinitionLoader`. If it did, the shared methods
(`RegisterAction`, `RegisterActionFunc`, `CancelActions`, `Build`) would be required to return
`DefinitionLoader` to satisfy the embedded interface, which breaks any-order chaining on a
`DefinitionBuilder`. The superset relationship is expressed by convention: `DefinitionBuilder`'s
method set is a strict superset of `DefinitionLoader`'s.

### 2. Concrete implementation

```go
// Unexported shared state — single source of truth.
type definitionCore struct { /* id, version, nodes, flows, cancelActions, actions, dupAction */ }

// Two thin wrappers; each delegates shared mutations/build to *definitionCore.
type definitionBuilder struct{ *definitionCore } // implements DefinitionBuilder
type definitionLoader  struct{ *definitionCore } // implements DefinitionLoader
```

- `NewDefinition(id, version string) DefinitionBuilder` returns `&definitionBuilder{core}`.
- `(*definitionBuilder).Loader() DefinitionLoader` returns `&definitionLoader{b.definitionCore}`,
  sharing the same state pointer.
- The ~4 shared methods (`RegisterAction`, `RegisterActionFunc`, `CancelActions`, `Build`) are
  implemented on both wrappers as one-line delegations to `*definitionCore`. The delegation is
  intentional duplication — it keeps each wrapper's method set independent and avoids the
  return-type conflict described above.

### 3. Validation moves to `Build()`

`ParseYAML` and `LoadYAML` previously ran structural validation at load time. Under this
decision, parse/syntax errors still surface at load time, but semantic validation (e.g.
unreachable nodes, missing start/end events) moves to `Build()`. This aligns YAML-loaded
definitions with builder-constructed definitions — both validate on `Build()` — and means
`ParseYAML`/`LoadYAML` return a well-formed-but-unvalidated definition.

### 4. YAML loading entry points

```go
func ParseYAML(data []byte) (DefinitionLoader, error)
func LoadYAML(r io.Reader) (DefinitionLoader, error)
```

Return type changes from `(*ProcessDefinition, error)` to `(DefinitionLoader, error)`.

Typical flow after this change:

```go
l, err := model.LoadYAML(r)
if err != nil {
    return nil, err
}
def, err := l.RegisterAction("score", scoreAction).Build()
```

### 5. `NewDefinition` return type

`NewDefinition(id, version string) DefinitionBuilder` — return type changes from
`*DefinitionBuilder` (concrete struct pointer) to the `DefinitionBuilder` interface. The
established **actions-first** idiom — `NewDefinition(...).RegisterAction(...).Add(...).Build()`
— compiles unchanged because builder methods all return `DefinitionBuilder`.

## Consequences

- **YAML-loaded definitions can register actions.** The critical gap is closed: a consumer
  loads a definition from YAML, calls `RegisterAction` on the returned `DefinitionLoader`, and
  then calls `Build()`. No imperative redeclaration is required.
- **Any-order chaining preserved.** Because builder methods return `DefinitionBuilder` (not
  `DefinitionLoader`), the actions-first idiom (`RegisterAction` before `Add`) and the
  structure-first idiom (`Add` before `RegisterAction`) both compile and chain correctly.
- **`~4 shared method bodies duplicated across wrappers.`** Each of `RegisterAction`,
  `RegisterActionFunc`, `CancelActions`, and `Build` is implemented on both
  `definitionBuilder` and `definitionLoader` as a one-line delegation to `*definitionCore`.
  This is deliberate: the return-type constraint of each wrapper makes a single shared
  implementation impossible without generics or code generation.
- **Breaking: `ParseYAML`/`LoadYAML` return type.** Existing callers that assign the result
  to `*ProcessDefinition` will fail to compile. The migration is mechanical: add `.Build()`
  (or `.RegisterAction(...).Build()`) where the `*ProcessDefinition` was previously used.
- **Breaking: `NewDefinition` return type.** Existing callers that store the result in a
  variable typed as `*DefinitionBuilder` or `*model.DefinitionBuilder` will fail to compile.
  Callers that chain fluently (`NewDefinition(...).Add(...).Build()`) are unaffected.
- **Validation timing shift.** Structural validation now runs at `Build()` rather than at
  `ParseYAML`/`LoadYAML` call time. Code that relied on immediate validation errors from the
  YAML load functions must move its error check to `Build()`.
- **`DefinitionLoader` is a testable seam.** Functions that accept or return a
  `DefinitionLoader` (e.g. a registration helper) can be tested with a stub that implements
  the four-method interface, without importing the model package's concrete types.

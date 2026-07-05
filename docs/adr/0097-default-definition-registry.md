# 0097. Default `DefinitionRegistry` and `runtime.RegisterDefinition`

Status: **Accepted — 2026-07-05.**
Spec: `docs/specs/2026-07-05-default-definition-registry-design.md`.
Follows: [ADR-0096](0096-sensible-default-driver-construction.md) (sensible-default `ProcessDriver` construction).

## Context

### Asymmetry between actions and definitions

ADR-0096 introduced `action.DefaultCatalog()` + `action.Register` so that a
zero-config `runtime.NewProcessDriver()` driver can resolve service-task actions
without any explicit wiring. The definition side had no equivalent: a consumer who
uses a `KindCallActivity` node — referencing a sub-definition by `DefRef` string —
must always call `runtime.WithDefinitions(reg)` explicitly. If they forget, the
driver has `defsReg == nil` and the `StartSubInstance` path fails immediately with
"no definition registry configured".

This creates a friction asymmetry: actions and definitions are both named, looked-up
collaborators, yet one has a process-global default and the other does not.

### Where the API must live — layering constraint

The obvious candidate package for a `RegisterDefinition` API is `definition` — it is
already the authoring root. However, `definition` is the pure authoring/model layer.
It imports only `definition/{activity,build,model,flow,kinds,event,gateway}` and
never imports `runtime/kernel`. Placing a `kernel.DefinitionRegistry` there would
invert the layering: authoring would depend on execution. That direction is
irreversible and would contaminate the `definition` package's clean compile graph.

A mutable registry type belongs in `runtime/kernel`, which already owns the
`DefinitionRegistry` interface and the immutable `MapDefinitionRegistry`. The
ergonomic process-global API belongs in `runtime`, which already imports `kernel` +
`definition/model` and is where consumers build the driver — mirroring the placement
of `action.Register` / `action.DefaultCatalog` beside their consumer code.

### Existing `MapDefinitionRegistry` is immutable

`kernel.MapDefinitionRegistry` is constructed from a complete `map[string]*model.ProcessDefinition`
and is read-only after construction. It is not suited for incremental, `init`-time
registration. A concurrency-safe mutable sibling is needed.

## Decision

### D1 — `kernel.MemDefinitionRegistry` (mutable, concurrency-safe)

We add a new type to `runtime/kernel`, the mutable sibling of the immutable
`MapDefinitionRegistry`:

```go
// MemDefinitionRegistry is a concurrency-safe, register-after-construction
// in-memory DefinitionRegistry. It is the mutable sibling of the immutable
// MapDefinitionRegistry; use it when definitions are registered incrementally
// (e.g. the process-global default populated at init).
// Never copy a MemDefinitionRegistry (it holds an RWMutex).
type MemDefinitionRegistry struct { ... } // sync.RWMutex + map[string]*model.ProcessDefinition

func NewMemDefinitionRegistry() *MemDefinitionRegistry
```

`Register` indexes `def` under BOTH `"<ID>"` (bare) and `"<ID>:<Version>"` (versioned)
so a `KindCallActivity` `DefRef` in either form resolves. The bare `<ID>` key is
overwritten to point at the most recently registered version — consumers who register
multiple versions of the same definition get the latest by default when they omit the
version in `DefRef`. The exact `<ID>:<Version>` key is first-registration-wins;
re-registering the same version returns `ErrDefinitionExists` (wrapped with the key).

New sentinel errors in `runtime/kernel`:
- `ErrNilDefinition` — `Register` called with `nil` definition.
- `ErrEmptyDefinitionID` — definition has an empty ID.
- `ErrDefinitionExists` — `"<ID>:<Version>"` already registered (wrapped with key).

`MustRegister` panics on error (init-time wiring). `Lookup` implements
`DefinitionRegistry`, returning `ErrDefinitionNotFound` on miss.

### D2 — `runtime` process-global default + API

We add a package-level default in `runtime`, mirroring the `action` package:

```go
// defaultDefinitionRegistry is the process-global registry a ProcessDriver uses
// when WithDefinitions is not supplied. Populate it via RegisterDefinition.
var defaultDefinitionRegistry = kernel.NewMemDefinitionRegistry()

func DefaultDefinitionRegistry() *kernel.MemDefinitionRegistry { return defaultDefinitionRegistry }
func RegisterDefinition(def *model.ProcessDefinition) error     // → defaultDefinitionRegistry.Register
func MustRegisterDefinition(def *model.ProcessDefinition)       // → .MustRegister
```

The test-isolation caveat applies (same as `action.DefaultCatalog`): the global is
process-wide and rejects duplicate `"<ID>:<Version>"` registrations; tests needing
isolation must pass `WithDefinitions(kernel.NewMemDefinitionRegistry())` rather than
relying on the global.

### D3 — Driver default + `WithDefinitions` nil-guard

We adjust `runtime.NewProcessDriver`:

- **Before** the option loop, `r.defsReg` is set to `defaultDefinitionRegistry` (non-nil).
  The empty-registry default resolves no definitions — it only removes the nil fast-fail
  and makes `defsReg` always non-nil.
- **`WithDefinitions(nil)`** becomes a no-op (nil-ignored), matching the behaviour of
  `WithActionCatalog` and `WithInstanceStore`. A nil argument never clobbers the default.
- **`WithDefinitions(custom)`** overrides the default with a consumer-supplied registry,
  as before.

### D4 — Ripple from a now-always-non-nil `defsReg`

Three sites in the driver branch on `r.defsReg == nil`. All degrade gracefully because
the empty default registry's `Lookup` returns `ErrDefinitionNotFound`, which each
path already handles:

- **`processdriver_action.go` `StartSubInstance`:** the `defsReg == nil` fast-fail is
  now unreachable. A Lookup miss yields a descriptive error pointing consumers at
  `runtime.RegisterDefinition` / `WithDefinitions`. The nil-guard may be retained as
  defensive dead-safe code (belt-and-suspenders) or removed — `WithDefinitions(nil)`
  can never nil it, so it is purely defensive either way.
- **`processdriver_cancel.go` `propagateCancel` gate `callLinks != nil && defsReg != nil`:**
  the `defsReg != nil` disjunct now always evaluates true. The logic collapses to
  `callLinks != nil`. If no definitions are registered in the (empty) default, each
  child's `Lookup` returns not-found and propagation for that child is skipped (the
  behaviour is already logged + `continue`). End-state behaviour is unchanged.
- **`timerops.go` `RehydrateTimers` gate `sched==nil || timerStore==nil || defsReg==nil`:**
  the `defsReg==nil` disjunct never fires. Unresolved timers are already
  skipped-and-counted; the error message mentioning `WithDefinitions` remains
  relevant to the `sched`/`timerStore` disjuncts.

### D5 — DEBUG construction summary

The `definitions` attribute in the one-shot DEBUG log emitted by `NewProcessDriver`
changes from `on/off` to `default-global` / `custom`:

- `definitions=default-global` when `r.defsReg == DefaultDefinitionRegistry()`.
- `definitions=custom` when a consumer has passed `WithDefinitions(reg)` and `reg`
  is a different registry instance.

This mirrors the `store=in-memory(non-durable)|custom` and `catalog=default-global|custom`
rendering introduced by ADR-0096.

## Consequences

- **Zero-config call activities.** A `KindCallActivity` node no longer requires explicit
  `WithDefinitions` wiring. Consumers can register sub-definitions via
  `runtime.RegisterDefinition(def)` (or `runtime.MustRegisterDefinition(def)`) at
  program start, and the driver picks them up automatically.

- **`defsReg` is always non-nil.** The three `r.defsReg == nil` branches in the driver
  are now permanently `false` at runtime. Each degrades gracefully: a `Lookup` miss
  produces a clear, actionable error (or a logged skip) rather than an upfront nil panic.
  This is a safe behavioural change — the nil-guard was a hard fail; the new path is a
  softer, descriptive failure at the point of use.

- **`WithDefinitions` nil-guard prevents accidental clobbering.** Passing a nil registry
  is now a no-op, consistent with `WithActionCatalog`/`WithInstanceStore`. A consumer
  who dynamically decides whether to supply a registry can safely pass nil when they
  don't, without clearing the sensible default.

- **Test-isolation obligation.** Tests that register definitions via
  `runtime.RegisterDefinition` must use unique IDs (or unique `<ID>:<Version>` keys)
  to avoid `ErrDefinitionExists` collisions from parallel test runs. Tests that need
  full isolation — for example, to verify that an empty registry produces a descriptive
  error — must pass `WithDefinitions(kernel.NewMemDefinitionRegistry())` to obtain a
  fresh, empty registry scoped to that test.

- **`kernel` package grows by one type and three sentinels.** `MemDefinitionRegistry`
  and its sentinels (`ErrNilDefinition`, `ErrEmptyDefinitionID`, `ErrDefinitionExists`)
  are new exported API surface. They are consistent with the existing `MapDefinitionRegistry`
  and `ErrDefinitionNotFound` and carry the `workflow-runtime:` error prefix.

- **`definition` package layering preserved.** The `definition` package remains free of
  `runtime/kernel` imports. The ergonomic API lives exclusively in `runtime`, where it
  is appropriate for a consumer who already imports `runtime` to wire a driver.

- **`runtime.RegisterDefinition` and `action.Register` are peers.** Both populate a
  process-global default that a zero-config driver uses automatically; both follow the
  fail-on-error / panic-variant pattern; both have the same test-isolation caveat. Future
  maintainers can reason about them by analogy.

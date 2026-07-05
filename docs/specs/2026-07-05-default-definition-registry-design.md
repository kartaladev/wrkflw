# Default DefinitionRegistry + `runtime.RegisterDefinition`

- **Date:** 2026-07-05
- **Status:** Approved (design), pending implementation
- **Related:** follows the sensible-default-driver work (ADR-0096); new ADR-0097
- **Scope:** `runtime/kernel`, `runtime`, docs

## Problem

`action.DefaultCatalog()` + `action.Register` gave a zero-config driver a default
action catalog. The definition side has no equivalent: unless a consumer calls
`runtime.WithDefinitions(reg)`, a `KindCallActivity` node fails with "no definition
registry configured". We want the same sensible-default ergonomics — a
process-global default definition registry the driver uses automatically, which
consumers populate with `runtime.RegisterDefinition(def)`.

## Placement decision

- **NOT the `definition` package.** It is the pure authoring/model layer and does
  not import `runtime/kernel`; owning a `kernel.DefinitionRegistry` there would
  invert the layering (authoring → execution). Confirmed: `definition` imports
  only `definition/{activity,build,model}`, never `runtime/kernel`.
- **Mutable registry type → `runtime/kernel`**, which already owns the
  `DefinitionRegistry` interface and the immutable `MapDefinitionRegistry`.
- **Ergonomic API → `runtime`**, which already imports `kernel` + `definition/model`
  and is where consumers build the driver. Mirrors `action.Register` /
  `action.DefaultCatalog` living beside their consumers.

## Decisions

### D1 — `kernel.MemDefinitionRegistry` (mutable, concurrency-safe)

New type in `runtime/kernel`, the mutable sibling of the immutable
`MapDefinitionRegistry`:

```go
// MemDefinitionRegistry is a concurrency-safe, register-after-construction
// in-memory DefinitionRegistry. It is the mutable sibling of the
// immutable MapDefinitionRegistry; use it when definitions are registered
// incrementally (e.g. the process-global default populated at init).
// Never copy a MemDefinitionRegistry (it holds an RWMutex).
type MemDefinitionRegistry struct { ... } // sync.RWMutex + map[string]*model.ProcessDefinition

func NewMemDefinitionRegistry() *MemDefinitionRegistry

// Register indexes def under BOTH "<ID>" and "<ID>:<Version>" so a
// KindCallActivity DefRef in either form resolves. Returns:
//   - ErrNilDefinition if def is nil
//   - ErrEmptyDefinitionID if def.ID == ""
//   - ErrDefinitionExists (wrapped, with the key) if "<ID>:<Version>" already registered
func (r *MemDefinitionRegistry) Register(def *model.ProcessDefinition) error

// MustRegister panics on error (init-time wiring).
func (r *MemDefinitionRegistry) MustRegister(def *model.ProcessDefinition)

// Lookup implements DefinitionRegistry (returns ErrDefinitionNotFound on miss).
func (r *MemDefinitionRegistry) Lookup(ctx context.Context, defRef string) (*model.ProcessDefinition, error)
```

New sentinels in kernel: `ErrNilDefinition`, `ErrEmptyDefinitionID`,
`ErrDefinitionExists` (all `workflow-runtime:`-prefixed). `Register` is
first-registration-wins on the exact `<ID>:<Version>` key; the bare `<ID>` key is
overwritten to point at the most recently registered version (documented) so a
DefRef without a version resolves the latest registered.

### D2 — `runtime` process-global default + API

```go
// defaultDefinitionRegistry is the process-global registry a ProcessDriver uses
// when WithDefinitions is not supplied. Populate it via RegisterDefinition.
var defaultDefinitionRegistry = kernel.NewMemDefinitionRegistry()

func DefaultDefinitionRegistry() *kernel.MemDefinitionRegistry { return defaultDefinitionRegistry }
func RegisterDefinition(def *model.ProcessDefinition) error     // → defaultDefinitionRegistry.Register
func MustRegisterDefinition(def *model.ProcessDefinition)        // → .MustRegister
```

Test-isolation caveat (same as `action.DefaultCatalog`): the global is process-wide
and rejects duplicate `<ID>:<Version>`; tests needing isolation pass
`WithDefinitions(kernel.NewMemDefinitionRegistry())`.

### D3 — Driver default + `WithDefinitions` nil-guard

- `NewProcessDriver` sets `r.defsReg = defaultDefinitionRegistry` (non-nil) before
  the option loop.
- `WithDefinitions(nil)` becomes a no-op (nil-ignored), matching
  `WithActionCatalog`/`WithInstanceStore`, so a nil arg never clobbers the default.

### D4 — Ripple from a now-always-non-nil `defsReg`

Three sites branch on `r.defsReg == nil`; all degrade gracefully because the empty
default registry's `Lookup` returns `ErrDefinitionNotFound`, which each path
already handles:

- `processdriver_action.go` StartSubInstance: the `defsReg == nil` fast-fail is now
  unreachable (registry always present). Lookup miss yields a descriptive error;
  update its wording to point at `runtime.RegisterDefinition` / `WithDefinitions`.
  Keep the nil-guard as defensive dead-safe code (belt-and-suspenders) OR drop it —
  implementer's choice, but the WithDefinitions nil-guard (D3) means it cannot be
  nil anyway.
- `processdriver_cancel.go` propagateCancel gate `callLinks != nil && defsReg != nil`:
  now effectively `callLinks != nil`. If no defs registered, `propagateCancel`
  lists children and skips each on `Lookup` not-found (already logged + `continue`).
  Behaviour end-state unchanged (children not resolved → not propagated), only an
  extra warn-log per child. Acceptable.
- `timerops.go` RehydrateTimers gate `sched==nil || timerStore==nil || defsReg==nil`:
  the `defsReg==nil` disjunct never fires now; still guarded by sched/timerStore.
  Unresolved timers are already skipped-and-counted. Keep the error message
  mentioning WithDefinitions (still relevant to sched/timerStore wiring).

### D5 — DEBUG construction summary

Change the `definitions` attribute from `on/off` to `default-global` / `custom`
(same rendering as `store` and `catalog`): `default-global` when
`r.defsReg == DefaultDefinitionRegistry()`, else `custom`.

## Testing (strict TDD)

1. `kernel.MemDefinitionRegistry`: register + Lookup by `<ID>` and `<ID>:<Version>`;
   nil/empty-ID guards; duplicate `<ID>:<Version>` → `ErrDefinitionExists`; bare-ID
   resolves latest; concurrent-safe (race). Black-box `kernel_test`.
2. `runtime.RegisterDefinition` / `DefaultDefinitionRegistry` / `MustRegisterDefinition`:
   registration into the global; identity of `DefaultDefinitionRegistry()`.
3. Driver default: a zero-config `NewProcessDriver()` runs a parent definition with a
   `KindCallActivity` whose DefRef resolves a sub-definition registered via
   `runtime.RegisterDefinition` (use unique IDs to avoid global-state collisions;
   `sync.Once` per the action-default test pattern).
4. `WithDefinitions(nil)` ignored (default stands); `WithDefinitions(custom)` overrides.
5. Graceful degradation: with `callLinks` wired but the default registry empty, cancel
   propagation does not error (children skipped); StartSubInstance for an unregistered
   DefRef returns a clear, RegisterDefinition-pointing error.
6. DEBUG summary shows `definitions=default-global` for zero-config and
   `definitions=custom` under `WithDefinitions(custom)`.

## Verification checklist

- [ ] `kernel.MemDefinitionRegistry` + sentinels, TDD, race-clean.
- [ ] `runtime.RegisterDefinition`/`MustRegisterDefinition`/`DefaultDefinitionRegistry`, TDD.
- [ ] Driver defaults `defsReg`; `WithDefinitions` nil-guarded; StartSubInstance error reworded.
- [ ] DEBUG `definitions=default-global|custom`.
- [ ] Graceful-degradation tests (cancel + StartSubInstance) green; existing runtime tests green.
- [ ] ADR-0097 (Nygard).
- [ ] `go build`, `go test -race`, coverage ≥85% touched (kernel, runtime), `golangci-lint` clean.
- [ ] Docs: README + INTERACTIONS + CHANGELOG updated.

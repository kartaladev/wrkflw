# Sensible-default `ProcessDriver` construction

- **Date:** 2026-07-05
- **Status:** Implemented (branch feat/sensible-default-driver; ADR-0096)
- **Related ADRs:** amends ADR-0083 (fail-fast constructors); new ADR-0096 to record this decision
- **Scope:** `runtime`, `runtime/kernel`, `action`, `persistence` (façade), `internal/persistence/store` (rename touch-through), `examples/**`, docs

## Problem

Constructing a `runtime.ProcessDriver` today requires two positional dependencies
that most first-run / experimentation / test scenarios do not care about:

```go
func NewProcessDriver(cat action.Catalog, store kernel.Store, opts ...Option) (*ProcessDriver, error)
```

Both are nil-guarded and return `kernel.ErrNilDependency`, so a consumer must
hand-build an `action.NewMapCatalog(nil)` and a `kernel.NewMemStore()` even to run
a definition that has no service tasks against in-memory state. This is friction
for the "just let me run a process" path and does not follow the project's
"sensible default until explicitly configured" principle.

Separately, the port name `kernel.Store` is too generic — it does not say *what*
it stores until you read its methods, and it is the only `*Store` in the kernel
not named after its domain (cf. `DefinitionStore`, `TimerStore`, `CallLinkStore`,
`ChainLinkStore`, `humantask.TaskStore`).

## Goals

1. `runtime.NewProcessDriver()` with **zero arguments** produces a working,
   in-memory, non-durable driver ready to `Run` a definition.
2. Both the action catalog and the instance store are **optional**, filled by
   sensible in-memory defaults, overridable via functional options.
3. A package-level `action.Register` (+ friends) lets consumers populate a shared
   default catalog (`action.DefaultCatalog()`) that zero-config drivers see.
4. A **DEBUG** log at construction tells the consumer what got wired, which
   optional features are on/off, and how to move to a production-ready runtime.
5. Replace generic / misleading exported names with self-explaining ones — chiefly
   the `Store` family → `InstanceStore`, plus `Token`→`Version`,
   `Outcome`→`ChainOutcome`, and lower-value polish (see D4).

Non-goals (explicitly deferred this session):

- `runtime.WithDurableStore(db)` implicit durable wiring. Dropped — it would pull
  `pgx` + `modernc/sqlite` + `go-sql-driver/mysql` into every `runtime` consumer
  and cannot disambiguate dialect from a bare `*sql.DB`. Revisit later, likely as
  a `persistence`-package helper returning a `runtime.Option`.
- Auto-defaulting an in-memory `SignalBus` / `DefinitionRegistry`. These stay
  explicit opt-in; auto-wiring behaviour a consumer did not ask for is surprising.
  The DEBUG summary makes each discoverable instead.

## Decisions

### D1 — All-optional constructor (clean break)

```go
func NewProcessDriver(opts ...Option) (*ProcessDriver, error)
```

- The two positionals and their `ErrNilDependency` guards are removed.
- Defaults are established **before** the option loop, so any option overrides them:
  - instance store → `kernel.NewMemInstanceStore()` (in-memory, non-durable)
  - action catalog → `action.DefaultCatalog()` (the package-global registry, see D3)
- Return type stays `(*ProcessDriver, error)`: `NewMemStore()` can technically
  return an error (defensively surfaced), and it leaves room for future
  option-level errors.
- This is a clean break — no deprecated shim. `NewProcessDriver` keeps its
  descriptive name (chosen over `runtime.New`, which is less self-describing in a
  package that has other constructors).

This narrows ADR-0083's fail-fast-on-nil specifically for the driver's
`cat`/`store`; the rest of ADR-0083 (other constructors return `(T, error)` +
`ErrNilDependency`) stands.

### D2 — New options

```go
// WithActionCatalog sets the service-action catalog. A nil cat is ignored, so
// the package-global action.DefaultCatalog() registry remains in effect.
func WithActionCatalog(cat action.Catalog) Option

// WithInstanceStore sets the transactional instance store. A nil store is
// ignored, so the default in-memory MemInstanceStore remains in effect.
func WithInstanceStore(store kernel.InstanceStore) Option
```

Nil-ignore matches the existing `WithClock` / `WithConditionEvaluator` convention
(a nil value never clobbers a good default).

### D3 — `action` package: package-global default registry

New file `action/default.go`. Reuses the existing concurrency-safe `*Registry`
(no new type):

```go
// defaultCatalog is the process-global action catalog used by a ProcessDriver
// constructed without WithActionCatalog. Concurrency-safe (Registry's RWMutex).
var defaultCatalog = NewRegistry()

// DefaultCatalog returns the process-global action registry.
func DefaultCatalog() *Registry { return defaultCatalog }

// Register adds a to the global registry under name (see Registry.Register).
func Register(name string, a Action) error

// RegisterFunc wraps fn and registers it in the global registry.
func RegisterFunc(name string, fn func(context.Context, map[string]any) (map[string]any, error)) error

// MustRegister / MustRegisterFunc panic on error (init-time wiring).
func MustRegister(name string, a Action)
func MustRegisterFunc(name string, fn func(context.Context, map[string]any) (map[string]any, error))
```

- The `action` package only *initialises* the global; consumers populate it from
  their own code / `init()`.
- **Documented gotcha:** the global is shared across all zero-config drivers and
  (like any `Registry`) rejects duplicate names. Tests needing isolation must use
  `WithActionCatalog(action.NewRegistry())` rather than the global, to avoid
  cross-test registration collisions.

### D4 — Self-explaining renames (naming audit)

A scan of exported types across `runtime`, `runtime/kernel`, `action`, and
`persistence` found `Store` was the head of a family of generic names plus a few
outright misleading ones. All of the following are **behaviour-preserving, pure
identifier renames** (no signature-shape or logic changes). The full set, approved
this session:

**Store family** — named after *what it stores* (an instance's execution state),
symmetric with `DefinitionStore` / `TimerStore` / `CallLinkStore` and the
`InstanceState` / `InstanceLister` / `ErrInstance*` vocabulary:

| Current | New |
|---|---|
| `kernel.Store` | `kernel.InstanceStore` |
| `kernel.MemStore` / `NewMemStore` / `MemStoreOption` | `kernel.MemInstanceStore` / `NewMemInstanceStore` / `MemInstanceStoreOption` |
| `kernel.CachingStore` / `NewCachingStore` / `CachingStoreOption` | `kernel.CachingInstanceStore` / `NewCachingInstanceStore` / `CachingInstanceStoreOption` |
| `persistence.Store` | `persistence.InstanceStore` (composite of `kernel.InstanceStore` + `kernel.JournalReader`; `Open*` now return `InstanceStore`) |

**Misleading — semantic fixes:**

| Current | New | Why |
|---|---|---|
| `kernel.Token` (`int64`) | `kernel.Version` | It is an optimistic-concurrency version (`// Postgres: a bigint version`), NOT a BPMN *execution token*. Naming it `Token` collides with the project's headline "token-based execution" concept. Threads through `InstanceStore.Load`/`Commit`, `ProcessDriver` internals, `MemStore`/`CachingStore`, and the persistence store impl. |
| `kernel.Outcome` (`string`, `chainlink.go`) | `kernel.ChainOutcome` | A bare `Outcome string`; matches `ChainLink` / `ChainLinkStore` / `ChainLinkRef` and its `CallOutcome` sibling. |

**Lower-value polish:**

| Current | New | Notes |
|---|---|---|
| `kernel.Ownership` | `kernel.InstanceOwnership` | Per-instance write ownership for the caching store. `AlwaysOwn` keeps its name (behaviourally self-explaining) but its doc + assertion update to the new interface name. `persistence.New*AdvisoryLockOwnership` return types and `_ kernel.Ownership = ...` assertions update. |
| `kernel.Publisher` | `kernel.OutboxPublisher` | It publishes *outbox* events. The `persistence.Publisher = kernel.Publisher` alias becomes `persistence.OutboxPublisher = kernel.OutboxPublisher`; `NewRelay`/`NewMySQLRelay`/`NewSQLiteRelay` param types update. |
| `action.Retryabler` (`error` + `Retryable() bool`) | `action.RetryableError` | The bare `Retryable` from the audit preview would make the type name equal its own method name (awkward); `RetryableError` is self-explaining and collision-free. |

**Rename ripple (all mechanical):** interface/type decls + doc comments in
`runtime/kernel/*.go`; compile-time assertions; `runtime/processdriver*.go`
fields, params, and docs; the `internal/persistence/store` impl (asserts against
`kernel.InstanceStore` / uses `Version`); the `persistence` façade types,
constructors, and godoc; every `store kernel.Store` / `kernel.Token` /
`kernel.Ownership` / `kernel.Publisher` reference across `runtime`, `internal/**`,
`examples/**`; and `docs/**` references. `go build ./...` and `go test ./...` must
stay green (no behaviour change).

### D5 — DEBUG construction summary

After `r.obs` is built, emit exactly one structured DEBUG record via
`r.obs.tel.Logger`:

```
DEBUG "ProcessDriver constructed"
    store=in-memory(non-durable)|custom
    catalog=default-global|custom
    humanTasks=on|off scheduler=on|off signalBus=on|off
    definitions=on|off callLinks=on|off timerStore=on|off
    actionTimeout=<dur> retryDefault=on|off exprTimeout=on|off
    hint="in-memory store is not durable; for production wire
          persistence.OpenPostgres/OpenMySQL/OpenSQLite + runtime.WithInstanceStore,
          and enable WithScheduler/WithTimerStore/WithCallLinkStore as needed"
```

- Level DEBUG: silent by default, discoverable when the consumer turns on debug
  logging.
- `store`/`catalog` report `custom` when overridden via options; the on/off flags
  reflect whichever optional collaborators were wired.
- Field derivation is from the already-populated `ProcessDriver` struct (a nil
  collaborator ⇒ `off`); no new state is added for logging.

## Component boundaries

- `action` (value/stateless helpers + one process-global registry var): owns the
  default catalog and its registration surface. No dependency on `runtime`.
- `runtime/kernel`: owns the renamed `InstanceStore` port and its in-memory
  reference impl (`MemStore`), unchanged in behaviour.
- `runtime`: owns the defaulting constructor, the two new options, and the DEBUG
  summary. Depends on `action` and `runtime/kernel` interfaces only — no new
  vendor/persistence import (that is why `WithDurableStore` is out of scope).
- `persistence`: unchanged except its public `Store` composite now embeds
  `kernel.InstanceStore`.

## Testing (strict TDD, red → green per symbol)

1. `action.Register` / `RegisterFunc` / `MustRegister` / `MustRegisterFunc` /
   `Default` — registration into the global, duplicate rejection, nil/empty
   guards (delegated to `Registry`), `DefaultCatalog()` identity.
2. `NewProcessDriver()` zero-arg — returns a usable driver; the default store is a
   fresh `MemInstanceStore`; the default catalog is `action.DefaultCatalog()` (assert a service
   task resolves an action registered via `action.Register`).
3. `WithActionCatalog` / `WithInstanceStore` — override the defaults; nil is
   ignored (default stands).
4. DEBUG summary — capture via an `slog` handler and assert the record + key
   attributes (store/catalog/feature flags) for a zero-config driver and a
   fully-wired driver.
5. Renames — pure identifier changes; existing `kernel`/`runtime`/`persistence`/
   `action` tests must stay green after each rename. No new behaviour, so no new
   test beyond compile. Do the renames as a distinct, self-contained step (ideally
   before the constructor/default work) so the diff stays reviewable.

## Migration

Clean break. Every `NewProcessDriver(cat, store, ...)` call site migrates to
`WithActionCatalog(cat)` + `WithInstanceStore(store)`:

- `examples/**` reference wiring.
- Non-`runtime` tests and the `processtest` harness.
- `runtime` internal callers.

## Verification checklist

- [ ] `action.Register`/`Default` + globals implemented TDD, tests green.
- [ ] `NewProcessDriver(opts...)` defaults (MemInstanceStore + `action.DefaultCatalog()`) TDD.
- [ ] `WithActionCatalog` / `WithInstanceStore` (incl. nil-ignore) TDD.
- [ ] DEBUG summary emitted and asserted via captured `slog` handler.
- [ ] All D4 renames complete — Store family (`InstanceStore`/`MemInstanceStore`/
      `CachingInstanceStore` + ctors/options, `persistence.InstanceStore`),
      `Token`→`Version`, `Outcome`→`ChainOutcome`, `Ownership`→`InstanceOwnership`,
      `Publisher`→`OutboxPublisher`, `Retryabler`→`RetryableError` — across kernel,
      runtime, internal/persistence/store, persistence façade, examples, docs;
      `go build ./...` clean.
- [ ] All call sites migrated to options; no positional `NewProcessDriver` remains.
- [ ] ADR-0096 written (Nygard), amends ADR-0083 for driver `cat`/`store` + records
      the D4 renames.
- [ ] `go test ./...` green; touched packages ≥ 85% line coverage.
- [ ] `golangci-lint run ./...` clean.

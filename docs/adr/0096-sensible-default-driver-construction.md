# 0096. Sensible-default `ProcessDriver` construction and naming audit

Status: **Accepted — 2026-07-05.**
Spec: `docs/specs/2026-07-05-sensible-default-driver-construction-design.md`.
Amends: [ADR-0083](0083-constructor-conventions.md) (fail-fast constructors) — narrows the
fail-fast rule for `runtime.NewProcessDriver`'s `cat`/`store` parameters only; the rest
of ADR-0083 stands.

## Context

### Two-positional construction friction

Constructing a `runtime.ProcessDriver` currently requires two positional dependencies:

```go
func NewProcessDriver(cat action.Catalog, store kernel.Store, opts ...Option) (*ProcessDriver, error)
```

Both are nil-guarded per ADR-0083 and return `kernel.ErrNilDependency` when absent. A
consumer must therefore hand-build an `action.NewMapCatalog(nil)` and a
`kernel.NewMemStore()` even for a definition with no service tasks running against
in-memory state. This is unnecessary friction for zero-config experimentation, testing,
and first-run scenarios, and it conflicts with the project's "sensible default until
explicitly configured" principle.

### Generic and misleading exported names

A naming audit across `runtime`, `runtime/kernel`, `action`, and `persistence` found a
cluster of exported identifiers that are either too generic or semantically misleading:

- **`kernel.Store`** does not say *what* it stores. Every other store in the project
  is named after its domain object (`DefinitionStore`, `TimerStore`, `CallLinkStore`,
  `ChainLinkStore`, `humantask.TaskStore`); the instance-execution store should follow
  the same convention.
- **`kernel.Token` (`int64`)** is the most problematic: it is an optimistic-concurrency
  version counter (conceptually a Postgres `bigint` row version), yet "token" is also
  the project's headline execution concept — the BPMN-inspired token that carries
  process-instance variables across nodes (exposed as `engine.Token`). The name
  collision causes genuine confusion when reading `InstanceStore.Load`/`Commit`
  signatures.
- **`kernel.Outcome` (`string`)** stands alone; its siblings are all prefixed —
  `ChainLink`, `ChainLinkStore`, `ChainLinkRef`, `CallOutcome` — making `Outcome`
  harder to grep and easier to confuse with unrelated result types.
- **`kernel.Ownership`**, **`kernel.Publisher`**, and **`action.Retryabler`** are
  functional but underspecified: they do not name their domain (`InstanceOwnership`,
  `OutboxPublisher`, `RetryableError` respectively).

### ADR-0083 fail-fast rule

ADR-0083 established that stateful, long-lived collaborators with required non-nilable
dependencies must be hardened: validate arguments and return `(T, error)`. It explicitly
does not address defaults — it guards against *nil* arguments but does not provide a
mechanism for *optional* arguments that fall back to sensible in-memory values.

## Decision

### D1 — All-optional `NewProcessDriver` constructor

We change the signature of `runtime.NewProcessDriver` to:

```go
func NewProcessDriver(opts ...Option) (*ProcessDriver, error)
```

The two positional parameters (`cat`, `store`) and their `ErrNilDependency` guards are
removed. Defaults are established **before** the option loop so any option overrides them:

- instance store → `kernel.NewMemInstanceStore()` (in-memory, non-durable)
- action catalog → `action.DefaultCatalog()` (the package-global registry; see D3)

The return type stays `(*ProcessDriver, error)` — `NewMemInstanceStore()` is
defensively error-returning, and the signature leaves room for future option-level
errors. `NewProcessDriver` keeps its descriptive name (over `runtime.New`, which would
be ambiguous in a package that has other constructors).

This is a **clean break** — no deprecated compatibility shim.

**Narrowing of ADR-0083:** For `runtime.NewProcessDriver` specifically, the fail-fast
nil-guard on `cat` and `store` is replaced by in-memory defaults. Every other hardened
constructor listed in ADR-0083 is unaffected.

### D2 — Two new functional options

```go
// WithActionCatalog sets the service-action catalog used by this driver.
// A nil cat is ignored; action.DefaultCatalog() remains in effect.
func WithActionCatalog(cat action.Catalog) Option

// WithInstanceStore sets the transactional instance store used by this driver.
// A nil store is ignored; the default in-memory MemInstanceStore remains in effect.
func WithInstanceStore(store kernel.InstanceStore) Option
```

The nil-ignore convention matches the existing `WithClock` / `WithConditionEvaluator`
pattern: a nil value never clobbers a good default.

### D3 — `action` package: process-global default registry

A new `action/default.go` file exposes a package-global `*Registry` (no new type —
reuses the existing concurrency-safe `Registry` with its `sync.RWMutex`):

```go
// DefaultCatalog returns the process-global action registry.
// Zero-config ProcessDrivers resolve service-task actions from this registry.
func DefaultCatalog() *Registry

// Register adds a to the global registry under name.
func Register(name string, a Action) error

// RegisterFunc wraps fn and registers it in the global registry.
func RegisterFunc(name string, fn func(context.Context, map[string]any) (map[string]any, error)) error

// MustRegister panics on error (intended for init-time wiring).
func MustRegister(name string, a Action)

// MustRegisterFunc panics on error (intended for init-time wiring).
func MustRegisterFunc(name string, fn func(context.Context, map[string]any) (map[string]any, error))
```

The `action` package only initialises the global; consumers populate it from their own
code or `init()` functions. The global is shared across all zero-config drivers in a
process and (like any `Registry`) rejects duplicate names. Tests that need action-catalog
isolation must pass `WithActionCatalog(action.NewRegistry())` rather than relying on the
global, to prevent cross-test registration collisions.

### D4 — Self-explaining renames (naming audit)

All renames are **behaviour-preserving pure identifier renames** — no signature-shape or
logic changes. Every affected call site (kernel, runtime, internal/persistence/store,
the persistence façade, examples, and docs) is updated mechanically; `go build ./...` and
`go test ./...` must remain green throughout.

**Store family** — named after what it stores (an instance's execution state), symmetric
with `DefinitionStore` / `TimerStore` / `CallLinkStore` and the `InstanceState` /
`InstanceLister` / `ErrInstance*` vocabulary:

| Current | Renamed to |
|---|---|
| `kernel.Store` | `kernel.InstanceStore` |
| `kernel.MemStore` | `kernel.MemInstanceStore` |
| `kernel.NewMemStore` | `kernel.NewMemInstanceStore` |
| `kernel.MemStoreOption` | `kernel.MemInstanceStoreOption` |
| `kernel.CachingStore` | `kernel.CachingInstanceStore` |
| `kernel.NewCachingStore` | `kernel.NewCachingInstanceStore` |
| `kernel.CachingStoreOption` | `kernel.CachingInstanceStoreOption` |
| `persistence.Store` | `persistence.InstanceStore` (composite of `kernel.InstanceStore` + `kernel.JournalReader`; `Open*` constructors now return `InstanceStore`) |

**Misleading — semantic fixes:**

| Current | Renamed to | Reason |
|---|---|---|
| `kernel.Token` (`int64`) | `kernel.Version` | It is an optimistic-concurrency row-version counter, not a BPMN execution token. The name `Token` collides with the project's headline "token-based execution" concept and with `engine.Token`. Threads through `InstanceStore.Load`/`Commit`, `ProcessDriver` internals, `MemInstanceStore`/`CachingInstanceStore`, and the persistence store implementation. |
| `kernel.Outcome` (`string`, `chainlink.go`) | `kernel.ChainOutcome` | Matches its siblings: `ChainLink`, `ChainLinkStore`, `ChainLinkRef`, and `CallOutcome`. |

**Lower-value polish:**

| Current | Renamed to | Notes |
|---|---|---|
| `kernel.Ownership` | `kernel.InstanceOwnership` | Per-instance write ownership used by the caching store. `AlwaysOwn` keeps its name (behaviourally self-explaining) but its doc and compile-time assertion update to the new interface name. `persistence.New*AdvisoryLockOwnership` return types and `_ kernel.Ownership = ...` assertions update accordingly. |
| `kernel.Publisher` | `kernel.OutboxPublisher` | It publishes outbox events, not arbitrary events. The `persistence.Publisher = kernel.Publisher` type alias becomes `persistence.OutboxPublisher = kernel.OutboxPublisher`; `NewRelay`/`NewMySQLRelay`/`NewSQLiteRelay` parameter types update. |
| `action.Retryabler` | `action.RetryableError` | The bare name `Retryabler` is awkward (method name equals type name). `RetryableError` is self-explaining and collision-free. |

### D5 — DEBUG construction summary

`NewProcessDriver` emits exactly one structured log record at `slog.LevelDebug` via the
driver's configured logger after the option loop completes:

```
DEBUG "ProcessDriver constructed"
    store=in-memory(non-durable)|custom
    catalog=default-global|custom
    humanTasks=on|off  scheduler=on|off  signalBus=on|off
    definitions=on|off callLinks=on|off  timerStore=on|off
    actionTimeout=<dur>  retryDefault=on|off  exprTimeout=on|off
    hint="in-memory store is not durable; for production wire
          persistence.OpenPostgres/OpenMySQL/OpenSQLite + runtime.WithInstanceStore,
          and enable WithScheduler/WithTimerStore/WithCallLinkStore as needed"
```

- **Level DEBUG**: silent by default; discoverable when the consumer enables debug
  logging. It is never emitted in production unless the consumer opts in.
- `store` reports `custom` when `WithInstanceStore` was used; `catalog` reports `custom`
  when `WithActionCatalog` was used.
- Feature on/off flags are derived from the already-populated `ProcessDriver` struct
  fields (a nil collaborator → `off`); no new state is added.

### Deferred — `WithDurableStore`

A convenience option `runtime.WithDurableStore(db *sql.DB)` (or dialect-aware equivalent)
was considered and explicitly **deferred**. Wiring it inside the `runtime` package would
pull `pgx`, `modernc.org/sqlite`, and `go-sql-driver/mysql` into every `runtime`
consumer's binary — violating the library's principle that `runtime` stays free of
persistence and DB-driver imports. Additionally, a bare `*sql.DB` carries no dialect
signal, making it impossible for `runtime` to select the correct store implementation.
This concern will be revisited as a helper in the `persistence` package that returns a
`runtime.Option`.

## Consequences

- **Zero-config path.** `runtime.NewProcessDriver()` is sufficient for experimentation,
  unit tests, and simple embedded scenarios. Consumers no longer need to construct
  throwaway catalogs and stores.
- **Clearer, collision-free names.** The `InstanceStore` / `Version` / `ChainOutcome` /
  `InstanceOwnership` / `OutboxPublisher` / `RetryableError` identifiers communicate
  their domain and do not collide with established project vocabulary. The `Token` →
  `Version` rename, in particular, eliminates the most misleading name in the codebase.
- **BREAKING — constructor signature.** All `NewProcessDriver(cat, store, ...)` call
  sites must migrate to `NewProcessDriver(WithActionCatalog(cat), WithInstanceStore(store))`.
  No compatibility shim is provided; the compiler flags every affected call site.
- **BREAKING — renamed identifiers.** Every symbol listed in D4 is renamed; affected
  packages (`runtime`, `runtime/kernel`, `action`, `persistence`, `internal/**`) and
  all consumer code (examples, tests) must update their references. The renames are
  mechanical and exhaustive — `go build ./...` enforces completion.
- **Test-isolation obligation for the global catalog.** Tests that register actions must
  use `WithActionCatalog(action.NewRegistry())` to avoid cross-test registration
  collisions in the process-global `DefaultCatalog`. Tests that do not register actions
  may use the zero-config path safely.
- **`runtime` stays free of persistence/DB-driver imports.** The deferred
  `WithDurableStore` means consumers who want a durable store continue to wire it via
  `persistence.Open*` + `runtime.WithInstanceStore`. This is slightly more verbose than
  a single helper call, but keeps the `runtime` module's import graph clean.
- **DEBUG discoverability.** The construction summary gives consumers a cheap path to
  understanding what is and is not wired without reading source code. It is a no-op in
  production (DEBUG suppressed by default).
- **ADR-0083 narrowing.** The fail-fast nil-guard on `NewProcessDriver`'s `cat`/`store`
  is replaced by defaults; all other hardened constructors in ADR-0083 are unaffected.
  Future constructors with required non-nilable dependencies continue to follow ADR-0083
  in full.

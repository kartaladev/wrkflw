# 0083. Constructor conventions: fail-fast hardening and redundant-constructor collapse

Status: **Accepted — 2026-07-03.**
Spec: `docs/specs/2026-07-03-constructor-conventions-and-builder-loader.md`.

## Context

Across roughly 60 constructors in the public root packages, almost none validate their
required arguments. A nil required collaborator — e.g. a nil `Store` passed to
`runtime.NewRunner` — is accepted silently and surfaces later as an opaque nil-pointer panic
far from the call site. The one constructor that does guard (`runtime.NewChainer`) panics
rather than returning an error. Error-returning constructors today exist only where
construction performs real I/O (database sessions) or compiles an expression.

Several types also expose multiple positional sibling constructors that differ only in which
optional collaborator they wire. The canonical case is `runtime.MemStore`:
`NewMemStore` / `NewMemStoreWithCallLinks` / `NewMemStoreWithTimers` — three constructors with
no way to obtain a `MemStore` that has *both* call-links and timers. Similar redundancy exists
in `engine.NewActionFailed`/`NewActionFailedJittered` and the casbinauthz authorizer trio
(`NewCasbinAuthorizer` / `NewCasbinAuthorizerFromStrings` / `NewCasbinAuthorizerFromDB`).

Two categories of constructor are explicitly excluded from this decision:

- **Pure value / DTO / trigger constructors** (`model.NewX` node constructors, `engine.New*`
  trigger constructors, `runtime.NewActionableView`, `runtime.NewInstanceSnapshot`). Their
  structural validity is enforced downstream at `Build()`/`Validate()`, and they appear inside
  fluent chains and expression contexts where an `(T, error)` return would break the API.
- **`model.NewDefinition`** is explicitly exempt from fail-fast changes: it remains
  `NewDefinition(id, version) DefinitionBuilder` with validation deferred to `Build()`.
- **Per-dialect constructor triplets** (`OpenPostgres` / `OpenMySQL` / `OpenSQLite`). Those are
  deliberate parallel backends, not accidental redundancy; they are left untouched.

## Decision

### 1. The fail-fast rule

> A constructor is **hardened** — validate required args, return `(T, error)` — iff it
> produces a **stateful, long-lived collaborator** *and* takes a **required non-nilable
> dependency** (interface or pointer) whose nil value would cause a latent panic later.

Options-only constructors with no required dependency are left as-is.

### 2. Hardened constructors — public root packages

The following constructors gain a leading nil/empty check on required arguments and change
their return type to `(T, error)` (a breaking signature change — see Consequences):

| Constructor | Required args validated |
|---|---|
| `runtime.NewRunner` | `cat action.Catalog`, `store Store` non-nil |
| `runtime.NewTaskService` | `store humantask.TaskStore`, `az authz.Authorizer` non-nil |
| `runtime.NewCachingStore` | `backing Store` non-nil |
| `runtime.NewCachingDefinitionRegistry` | `backing DefinitionRegistry` non-nil |
| `runtime.NewSignalBus` | `deliver DeliverFunc` non-nil |
| `runtime.NewCallNotifier` | `cl`, `deliver`, `reg` non-nil |
| `runtime.NewChainer` | `starter`, `policy` non-nil — **panic → error** |
| `runtime.NewLineageReader` | `calls`, `chains` non-nil |
| `persistence.*` wrappers taking a `conn`/`pool` | handle non-nil (delegates to internal store) |

### 3. Hardened constructors — `internal/`

The public `persistence/` wrappers are thin pass-throughs to the neutral store; the real
nil-panic risk lives one layer down. The following are hardened to validate `conn != nil` and
`dialect != nil`:

- `internal/persistence/store.New` and its siblings: `NewCallLinkStore`, `NewDefinitionStore`,
  `NewLister`, `NewPruner`, `NewTimerStore`, `NewChainLinkStore`, `NewDeduper`, `NewRelay`.

These currently return a value only; they become `(T, error)`.

### 4. Sentinel and wrapping conventions

We introduce one exported sentinel per package for "required argument missing", e.g.
`runtime.ErrNilDependency`. Sentinel messages follow the repo-wide prefix convention:
`errors.New("workflow-runtime: nil required dependency")`. All nil-arg errors are wrapped with the
offending argument name for context:

```go
fmt.Errorf("%w: store", ErrNilDependency)
```

This produces readable messages (`workflow-runtime: nil required dependency: store`) while remaining
targetable with `errors.Is`.

### 5. Redundant-constructor collapse — functional options

All collapsed constructor families adopt functional options and return an error when the
consumer supplies an **invalid or inconsistent** option value.

**`runtime.MemStore`:**

```go
func NewMemStore(opts ...MemStoreOption) (*MemStore, error)

func WithCallLinks(cl *MemCallLinkStore) MemStoreOption  // error if cl == nil
func WithTimers(mts *MemTimerStore) MemStoreOption       // error if mts == nil
```

Replaces `NewMemStore` / `NewMemStoreWithCallLinks` / `NewMemStoreWithTimers`. Closes the
"can't set both" gap: `NewMemStore(WithCallLinks(cl), WithTimers(mts))`. A test helper
`mustMemStore(t, opts...) *MemStore` keeps test sites readable.

**`engine.NewActionFailed`:**

```go
func NewActionFailed(at time.Time, commandID, errMsg string, retryable bool, opts ...ActionFailedOption) ActionFailed
func WithJitter(fraction float64) ActionFailedOption
```

Replaces `NewActionFailed` + `NewActionFailedJittered`. `ActionFailed` is a pure engine
trigger value type; per the value/DTO exclusion it remains **non-error**. `WithJitter`
documents its valid range (`fraction >= 0`); out-of-range is a documented precondition, not
an error return.

**`casbinauthz` source trio:**

```go
func NewCasbinAuthorizer(opts ...Option) (authz.Authorizer, io.Closer, error)

func FromEnforcer(e *casbin.SyncedEnforcer) Option
func FromStrings(modelText, policyText string) Option
func FromDB(ctx context.Context, pool *pgxpool.Pool, dbOpts ...DBOption) Option
```

Replaces `NewCasbinAuthorizer` / `NewCasbinAuthorizerFromStrings` / `NewCasbinAuthorizerFromDB`.
Exactly one source must be supplied. Zero sources → `ErrNoAuthorizerSource`; two or more
sources → `ErrMultipleAuthorizerSources`. An invalid source (nil enforcer, non-compilable
model/policy, DB error) → wrapped error. The `io.Closer` is nil/no-op for non-DB sources,
preserving the existing resource-lifecycle contract.

## Consequences

- **Clearer failures.** Required-argument mistakes now surface at construction with a message
  that names the offending argument, rather than as a nil-pointer panic hundreds of frames
  away from the call site.
- **Exhaustive MemStore wiring.** The `WithCallLinks`+`WithTimers` combination — previously
  impossible — is now the canonical call site.
- **Breaking signature changes.** All hardened constructors change from `T` to `(T, error)`
  return. Affected public constructors include `NewRunner`, `NewTaskService`, `NewCachingStore`,
  `NewCachingDefinitionRegistry`, `NewSignalBus`, `NewCallNotifier`, `NewChainer`,
  `NewLineageReader`, and the `persistence/` wrappers. The `NewMemStore*` trio collapses to
  `NewMemStore(opts...)`. `NewActionFailedJittered` is removed; `NewActionFailed` gains a
  variadic options argument. The `NewCasbinAuthorizer*` trio collapses to a single
  `NewCasbinAuthorizer(opts...)`.
- **Per-dialect triplets untouched.** `OpenPostgres`/`OpenMySQL`/`OpenSQLite` are deliberate
  parallel backends and are not affected.
- **`model.NewDefinition` untouched.** Validation remains deferred to `Build()`; the
  constructor signature is unchanged.
- **Consistent error handling obligation.** All callers of hardened constructors must
  now handle the returned error; the compiler enforces this. Existing callers that
  assign to a single variable will fail to compile, making the migration mechanical.
- **`ErrNilDependency` per package.** Each affected package exposes one sentinel; consumers
  who need to distinguish nil-dependency errors from other construction errors can use
  `errors.Is`. This is a small addition to each package's exported error surface.

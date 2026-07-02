# Constructor Conventions, Redundant-Constructor Collapse, and Builder/Loader Unification

Status: Draft (awaiting user review)
Date: 2026-07-03
Owners: engine maintainers
Related ADRs (to be written): 0083 (constructor conventions), 0084 (builder/loader unification)

## 1. Problem

Three related API-hygiene gaps in the public root packages:

1. **No construction-time validation.** Across ~60 constructors, almost none validate
   their required arguments. A nil required collaborator (e.g. a nil `Store` passed to
   `runtime.NewRunner`) is accepted silently and surfaces later as an opaque nil-pointer
   panic far from the call site. Only `runtime.NewChainer` guards (and it *panics*), plus a
   couple of config-conflict checks in the scheduler. Error-returning constructors today
   exist *only* where construction does real I/O (DB sessions) or compiles an expression.

2. **Redundant sibling constructors.** Several types expose multiple positional
   constructors that differ only in which optional collaborator they wire, forcing a
   combinatorial explosion and leaving gaps. The canonical case is `runtime.MemStore`:
   `NewMemStore` / `NewMemStoreWithCallLinks` / `NewMemStoreWithTimers` — three constructors,
   with **no** way to obtain a MemStore that has *both* call-links and timers.

3. **Builder and loader are unrelated API shapes.** `model.DefinitionBuilder` is a fluent
   struct; YAML loading is two free functions (`ParseYAML`/`LoadYAML`) returning
   `*ProcessDefinition`. There is no shared abstraction, and — critically — a definition
   loaded from YAML **cannot** carry definition-scoped actions (`RegisterAction`), because
   `action.ServiceAction` values are runtime Go values, not serializable. A consumer who
   loads a definition from YAML has no ergonomic, type-guided way to attach those actions
   before building.

Additionally, project documentation (`INTERACTIONS.md`, package READMEs, `doc.go`) explains
the *what* of component collaboration but is thin on the *why* and the *how-it-connects* at
a depth that serves developers from junior through maintainer.

## 2. Goals

- Make required-argument mistakes **fail fast at construction** with a clear, wrapped error,
  rather than as a delayed nil panic.
- Collapse redundant sibling constructors into the **functional-options** pattern, returning
  an error when a consumer supplies an **invalid or inconsistent** option value.
- Introduce a **unified interface view**: `DefinitionBuilder` (full surface) and
  `DefinitionLoader` (everything except the structural `Add`/`AddX`/`Connect`), so a
  YAML-loaded definition can still register actions and build through a type-guided API.
- **Deepen documentation** so interactions are legible to all developer levels — without
  writing any "make this readable" meta-instruction into the docs themselves.

## 3. Non-Goals

- Changing pure **value/DTO constructors** (`model.NewX` node constructors, `engine.New*`
  trigger constructors, `runtime.NewActionableView`/`NewInstanceSnapshot`). Their structural
  validity is already enforced downstream at `Build()`/`Validate()`, and they are used inside
  fluent `.Add(...)` chains and expression contexts where an `(T, error)` return would break
  the API. (One exception is discussed in §5.2.)
- Collapsing the **per-dialect** Postgres/MySQL/SQLite constructor triplets. Those are
  deliberate parallel backends, not accidental redundancy.
- Adding BPMN/XML loading (still out of scope for the model package).
- `model.NewDefinition` is **explicitly excluded** from the fail-fast change (per direction):
  it remains `NewDefinition(id, version) DefinitionBuilder` with validation deferred to
  `Build()`.

## 4. The fail-fast rule (Part A)

> A constructor is **hardened** — validate required args, return `error` — iff it produces a
> **stateful, long-lived collaborator** *and* takes a **required non-nilable dependency**
> (interface or pointer) whose nil value would cause a latent panic later.

Pure value/DTO/snapshot constructors and options-only constructors with no required
dependency are left as-is.

### 4.1 Hardened constructors (public root packages)

Each gains a leading nil/empty check on its required args and returns `(T, error)` (those
that already return a value only will change signature — a breaking change, see §7):

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

### 4.2 Hardened constructors (`internal/`)

The public `persistence/` wrappers are thin pass-throughs to the neutral store, so the real
nil-panic risk lives one layer down. Hardened:

- `internal/persistence/store.New(conn, dialect, …)` and its siblings (`NewCallLinkStore`,
  `NewDefinitionStore`, `NewLister`, `NewPruner`, `NewTimerStore`, `NewChainLinkStore`,
  `NewDeduper`, `NewRelay`): validate `conn != nil` and `dialect != nil`.

These constructors currently return a value only; they become `(T, error)`.

### 4.3 Sentinel & wrapping conventions

- New sentinels use the repo prefix `workflow-<pkg>:` (e.g.
  `errors.New("workflow-runtime: nil store")`), matching existing convention.
- Prefer a single exported sentinel per package for "required argument missing", e.g.
  `runtime.ErrNilDependency`, wrapped with `%w` and the argument name for context:
  `fmt.Errorf("%w: store", ErrNilDependency)`. (Exact naming decided per package during
  implementation; must read as `workflow-runtime: …`.)

## 5. Redundant-constructor collapse (Part B)

All collapsed constructors adopt functional options and **return an error when a consumer
supplies an invalid or inconsistent option value**.

### 5.1 `runtime.MemStore`

```go
func NewMemStore(opts ...MemStoreOption) (*MemStore, error)

func WithCallLinks(cl *MemCallLinkStore) MemStoreOption // error if cl == nil
func WithTimers(mts *MemTimerStore) MemStoreOption      // error if mts == nil
```

- Replaces `NewMemStore` / `NewMemStoreWithCallLinks` / `NewMemStoreWithTimers`.
- Closes the "can't set both" gap: `NewMemStore(WithCallLinks(cl), WithTimers(mts))`.
- Invalid option (nil dependency) → `NewMemStore` returns a wrapped error.
- **Call-site churn:** 118 `NewMemStore(` + 24 `NewMemStoreWith*` sites (mostly tests). A
  test helper `mustMemStore(t, opts...) *MemStore` will keep those readable; production/example
  sites handle the error explicitly.

### 5.2 `engine.NewActionFailed`

```go
func NewActionFailed(at time.Time, commandID, errMsg string, retryable bool, opts ...ActionFailedOption) ActionFailed
func WithJitter(fraction float64) ActionFailedOption
```

- Replaces `NewActionFailed` + `NewActionFailedJittered`.
- **Resolved:** `ActionFailed` is a pure engine **trigger value** type; per §3 such
  constructors stay non-error. `NewActionFailed` remains **non-error**; `WithJitter` documents
  its valid range (`fraction >= 0`), treating out-of-range as a documented precondition.

### 5.3 `casbinauthz` source trio

```go
func NewCasbinAuthorizer(opts ...Option) (authz.Authorizer, io.Closer, error)

func FromEnforcer(e *casbin.SyncedEnforcer) Option
func FromStrings(modelText, policyText string) Option
func FromDB(ctx context.Context, pool *pgxpool.Pool, dbOpts ...DBOption) Option
```

- Replaces `NewCasbinAuthorizer` / `NewCasbinAuthorizerFromStrings` / `NewCasbinAuthorizerFromDB`.
- Exactly **one** source must be supplied. Zero sources or two+ sources is **inconsistent** →
  return `ErrNoAuthorizerSource` / `ErrMultipleAuthorizerSources`. An invalid source (nil
  enforcer, un-compilable model/policy, DB error) → wrapped error.
- `io.Closer` is returned for the DB-backed source (nil/no-op for the others), preserving the
  existing resource-lifecycle contract.

## 6. Builder/Loader unification (Part C) — two-wrapper over shared core

### 6.1 Interfaces

```go
// DefinitionLoader is the reduced surface for a definition whose structure
// (nodes + sequence flows) is already declared — e.g. loaded from YAML. It can
// still attach definition-scoped actions and build, but cannot add nodes/flows.
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

`DefinitionBuilder` does **not** Go-`embed` `DefinitionLoader` (that would force its shared
methods to return `DefinitionLoader` and break any-order chaining). The superset relationship
is expressed by-convention: `DefinitionBuilder`'s method set is a strict superset of
`DefinitionLoader`'s.

### 6.2 Concrete implementation

```go
type definitionCore struct { /* id, version, nodes, flows, cancelActions, actions, dupAction */ }
// unexported mutators + build() live on *definitionCore (single source of truth).

type definitionBuilder struct{ *definitionCore } // implements DefinitionBuilder; methods return the builder
type definitionLoader struct{ *definitionCore }  // implements DefinitionLoader; methods return the loader
```

- `NewDefinition(id, version) DefinitionBuilder` returns `&definitionBuilder{core}`.
- `(*definitionBuilder).Loader() DefinitionLoader` returns `&definitionLoader{b.definitionCore}`
  (shares state).
- The ~4 shared methods (`RegisterAction`, `RegisterActionFunc`, `CancelActions`, `Build`) are
  implemented on both wrappers as one-line delegations to `*definitionCore`.

### 6.3 YAML loading

```go
func ParseYAML(data []byte) (DefinitionLoader, error) // structure declared; caller registers actions, then Build()
func LoadYAML(r io.Reader) (DefinitionLoader, error)
```

- Returns a `DefinitionLoader` backed by a `*definitionCore` pre-populated with the YAML nodes
  and flows. **Validation moves from load-time to `Build()`** — the loader carries a
  well-formed-but-unvalidated definition; `Build()` runs `Validate`. Parse/syntax errors still
  surface at load time.
- Typical flow: `l, err := model.LoadYAML(r); def, err := l.RegisterAction("score", a).Build()`.

### 6.4 Backward compatibility

- **No consumer stores `*DefinitionBuilder`** as a variable (verified repo-wide); call sites
  chain fluently, so changing the return type from `*DefinitionBuilder` to the interface
  `DefinitionBuilder` is source-compatible for those chains.
- The established **actions-first** idiom (`NewDefinition().RegisterAction(...).Add(...)…`),
  used in tests and the public `example_scoped_action_test.go`, **compiles unchanged** because
  builder methods all return `DefinitionBuilder`.
- **Breaking:** `ParseYAML`/`LoadYAML` return type changes from `(*ProcessDefinition, error)` to
  `(DefinitionLoader, error)`; existing callers add a `.Build()` (or `.RegisterAction(...).Build()`).

## 7. Breaking changes summary

- Hardened constructors that previously returned a value only now return `(T, error)`:
  `NewRunner`, `NewTaskService`, `NewCachingStore`, `NewCachingDefinitionRegistry`,
  `NewSignalBus`, `NewCallNotifier`, `NewLineageReader`, the affected `persistence/` wrappers,
  and the internal store constructors. `NewChainer` changes from panic to `(*Chainer, error)`.
- `NewMemStore*` trio → `NewMemStore(opts...) (*MemStore, error)`.
- `NewActionFailedJittered` removed; `NewActionFailed` gains `opts ...ActionFailedOption`.
- `NewCasbinAuthorizer*` trio → single `NewCasbinAuthorizer(opts...) (authz.Authorizer, io.Closer, error)`.
- `model.DefinitionBuilder` becomes an interface (was a struct); `NewDefinition` returns it.
- `ParseYAML`/`LoadYAML` return `DefinitionLoader`.

All are acceptable per the project's breaking-change-with-advisory posture; each is called out
in `CHANGELOG.md` and package docs.

## 8. Documentation elaboration (Part D)

Deepen — without adding any meta "readability" note to the docs:

- **`INTERACTIONS.md`:** for each existing flow, expand the prose around the diagram to explain
  *why* the seam exists, what each participant **guarantees**, the failure/retry/CAS behavior,
  and where to look in code. Add a short **constructor conventions** subsection (fail-fast +
  functional options + when a constructor returns an error) and a **builder ↔ loader**
  interaction subsection (YAML → `DefinitionLoader` → register actions → `Build`). Keep all
  existing diagrams.
- **Package READMEs** (`engine`, `model`, `runtime`, `action`, `service`) and root `doc.go`:
  expand component-interaction explanations to junior→maintainer depth. `model/README.md`
  documents `DefinitionBuilder` vs `DefinitionLoader` and the load-then-register-actions flow.

## 9. Testing strategy (TDD strict)

Per project discipline, every new/changed exported symbol is preceded by a visible failing
test (red), then implementation (green), per package:

- **Fail-fast:** table test per hardened constructor asserting `(nil, ErrNilDependency)`-style
  errors on each nil/empty required arg, and success on valid args. Uses the project
  `table-test` closure form.
- **MemStore options:** tests for `WithCallLinks(nil)`/`WithTimers(nil)` → error; both-set path;
  no-option path. `mustMemStore` helper introduced test-first.
- **casbinauthz options:** zero-source, multi-source, invalid-source → sentinel errors;
  each valid source → working authorizer. DB source via `testcontainers` (`RunTestDatabase`).
- **Builder/Loader:** tests that (a) actions-first and structure-first chains both build;
  (b) `LoadYAML` returns a loader that can `RegisterAction(...).Build()`; (c) the loader view
  does not expose `Add`/`Connect` (compile-time — asserted via an `example`/doc and interface
  satisfaction checks); (d) builder↔loader share state.
- Coverage ≥ 85% per touched package; `go test ./...` green; `golangci-lint run ./...` clean.

## 10. Rollout / plan shape

Multi-task plan (separate `docs/plans/` doc):

1. ADR 0083 (constructor conventions) + ADR 0084 (builder/loader unification).
2. Builder/Loader interfaces + shared core + YAML return-type change (model).
3. MemStore options collapse + `mustMemStore`.
4. engine `NewActionFailed` options collapse.
5. casbinauthz source-options collapse.
6. Fail-fast hardening: runtime constructors.
7. Fail-fast hardening: persistence public wrappers + internal store.
8. Documentation elaboration (INTERACTIONS.md, READMEs, doc.go).
9. Final verification (coverage, lint, CHANGELOG).

## 11. Resolved decisions

1. **§5.2** — `engine.NewActionFailed` stays **non-error** (consistent with the value/trigger
   constructor policy; `WithJitter` documents its valid range `fraction >= 0`). Resolved
   2026-07-03.
2. **§4.3** — **one shared `ErrNilDependency` sentinel per package**, wrapped with the
   offending argument name via `%w` (e.g. `fmt.Errorf("%w: store", ErrNilDependency)`).
   Resolved 2026-07-03.

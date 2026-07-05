# Service package refactor — coherent-graph constructor, interface segregation, `ProcessInstance` return type, vendor-free durable provider

- **Status:** Draft (design approved via brainstorming, 2026-07-06). Not yet implemented.
- **Target ADR:** 0098 (next free).
- **Scope:** the module-root `service` package public API, plus a new durable
  `humantask.TaskStore` in `persistence` / `internal/persistence` (D6). No `engine`/`model`
  changes expected.
- **Breaking:** yes, throughout. Acceptable — pre-v0.1.0, clean break, no deprecated shims.

## Context

`service` is the hand-wired facade consumers embed to drive process instances. Today
(`service/service.go`):

- `New(runner *runtime.ProcessDriver, tasks *task.TaskService, reg kernel.DefinitionRegistry,
  store kernel.InstanceStore, lister kernel.InstanceLister, taskStore humantask.TaskStore,
  opts ...EngineOption) *Engine` — **6 required positional deps, no validation, no error
  return**, one option (`WithEngineClock`).
- The `Engine` struct holds a `*runtime.ProcessDriver` **and** its own `store`/`reg`, which the
  driver *also* owns — a redundancy that can silently drift.
- Almost every method returns raw `engine.InstanceState`. Only `GetInstanceWithDefinition`
  returns state **and** `*model.ProcessDefinition`, as a separate tuple.
- The def+state→JSON fusion (`runtime/view.InstanceSnapshot`, `ActionableView`) is built
  **transport-side**, so every HTTP adapter re-implements the transformation.
- The 5 admin ports (`DeadLetterAdmin`, `LineageAdmin`, `RelayStatsAdmin`, `TimerAdmin`,
  `PolicyAdmin`) are already segregated into separate optional interfaces — good precedent.
- `service`'s dependency graph is currently **DB-vendor-free** (`go list -deps ./service` pulls
  in no pgx / go-sql-driver / modernc-sqlite / `database/sql` / `persistence`). This must be
  preserved.

## Goals

1. `New*` constructor **validates arguments and fails fast** (returns an error).
2. **Segregate** `Service` into smaller role interfaces so a consumer references only the
   functionality it needs.
3. Single-instance methods return a new **`ProcessInstance`** type that carries the process
   definition + instance state and **serializes directly to JSON** with no transformation.
4. Constructor configured with **sensible defaults** (in-memory), mirroring
   `runtime.NewProcessDriver` — including a DEBUG construction summary.
5. Introduce the previously-deferred **`WithDurableStore`** option, here in `service`, without
   leaking DB drivers into the `service` compile graph.
6. Introduce a **durable SQL-backed `humantask.TaskStore`** so the durable graph has a persistent
   task store (not just an in-memory one).

## Decisions

### D1 — `NewEngine` owns one coherent component graph

```go
func NewEngine(opts ...Option) (*Engine, error)
```

The service **builds the leaves first, then builds the `ProcessDriver` from those same leaves**,
so the driver and the service can never point at different stores/registries. There are **no
separate `store`/`reg` constructor params** — they are derived from the shared components.

Default graph (all in-memory, mutually coherent; applied before options):

```
store     = kernel.NewMemInstanceStore()        // also satisfies kernel.InstanceLister
reg       = runtime.DefaultDefinitionRegistry() // the process-global registry (matches runtime)
taskStore = humantask.NewMemTaskStore()
authz     = <default allow-all authorizer>      // D5
tasks     = task.NewTaskService(taskStore, authz)
driver    = runtime.NewProcessDriver(
                runtime.WithInstanceStore(store),
                runtime.WithDefinitions(reg),
            )
lister    = store                               // MemInstanceStore.List
clk       = clock.System()
```

**Fail-fast validation** (after the option loop): every resolved dependency must be non-nil, else
return a wrapped `service.ErrNilDependency` (new sentinel, mirroring
`persistence.ErrNilDependency` / ADR-0083). Because defaults fill everything, validation is the
backstop that catches (a) explicitly-passed nils, (b) a `DurableProvider` that returns a nil
leaf, (c) a failed default sub-construction (e.g. `NewProcessDriver` error, which is wrapped and
returned). Options that receive nil are **ignored** (runtime convention), never stored.

**DEBUG construction summary** (mirrors `ProcessDriver.logConstructionSummary`): one `slog`
record — `store=in-memory(non-durable)|durable`, `definitions=default-global|custom`,
`taskStore`, `authz=allow-all|custom`, plus a durability hint.

The old 6-positional `New` is **removed**.

### D2 — Role-grouped interface segregation

```go
type InstanceStarter interface {
    StartInstance(ctx context.Context, req StartInstanceRequest) (ProcessInstance, error)
}
type InstanceReader interface {
    GetInstance(ctx context.Context, instanceID string) (ProcessInstance, error)
    ListInstances(ctx context.Context, filter kernel.InstanceFilter) (kernel.InstancePage, error)
}
type TaskManager interface {
    ClaimTask(ctx context.Context, req ClaimTaskRequest) (ProcessInstance, error)
    CompleteTask(ctx context.Context, req CompleteTaskRequest) (ProcessInstance, error)
    ReassignTask(ctx context.Context, req ReassignTaskRequest) (ProcessInstance, error)
}
type Messaging interface {
    DeliverSignal(ctx context.Context, req DeliverSignalRequest) (ProcessInstance, error)
    DeliverMessage(ctx context.Context, req DeliverMessageRequest) error
}
type InstanceOps interface {
    ResolveIncident(ctx context.Context, req ResolveIncidentRequest) (ProcessInstance, error)
    CancelInstance(ctx context.Context, req CancelInstanceRequest) (ProcessInstance, error)
}

type Service interface {
    InstanceStarter
    InstanceReader
    TaskManager
    Messaging
    InstanceOps
}
```

Deliberate exceptions to the `ProcessInstance` return:

- **`ListInstances`** stays `kernel.InstancePage` — a summary page; resolving a definition per row
  is too heavy.
- **`DeliverMessage`** stays `error`-only — a message may correlate to zero or many instances.

**`GetInstanceWithDefinition` is removed** — folded into `GetInstance`, which now carries the
definition inside the returned `ProcessInstance`.

The 5 admin ports remain separate optional interfaces, unchanged.

### D3 — `ProcessInstance` is a clean interface that serializes itself

`ProcessInstance` is a **clean interface with only the required methods**, and it satisfies
`json.Marshaler`. The concrete serialized DTO shape is an **unexported type built inside
`MarshalJSON`** — never exposed on the public API.

```go
// ProcessInstance is the read-only, fused view of a running instance: its definition
// and state. It serializes directly to a stable, frontend-ready JSON document via
// MarshalJSON; the serialized shape is an internal detail (no exported DTO fields), so
// a consumer can embed it in its own domain/DTO type and marshal with no transformation.
type ProcessInstance interface {
    Definition() *model.ProcessDefinition // raw template (nil if unresolved)
    State() engine.InstanceState          // raw running state
    json.Marshaler                        // MarshalJSON() ([]byte, error)
}

// unexported impl — the only value NewProcessInstance returns
type processInstance struct {
    def *model.ProcessDefinition
    st  engine.InstanceState
}

func (p processInstance) Definition() *model.ProcessDefinition { return p.def }
func (p processInstance) State() engine.InstanceState          { return p.st }
func (p processInstance) MarshalJSON() ([]byte, error) {
    return json.Marshal(newInstanceJSON(p.def, p.st)) // unexported DTO builder
}

// exported constructor so consumers/tests can fabricate one
func NewProcessInstance(def *model.ProcessDefinition, st engine.InstanceState) ProcessInstance
```

Rationale / consequences:

- **Direct serialization:** `json.Marshal(pi)` yields the frontend-ready JSON with zero
  transformation. Raw `engine.InstanceState` (unexported bookkeeping) and the 19-kind
  interface-typed `model.ProcessDefinition` graph are **not** dumped; the JSON is a **projection**
  (state fields + definition-*derived* fields: `scoped_actions`, `action_bindings`, etc.).
- **Hidden serialized type:** the DTO struct (`instanceJSON`) is unexported and lives only inside
  `MarshalJSON`, satisfying "the serialized type must be hidden."
- **Embeddable:** a consumer places `ProcessInstance` as a field in its own domain/DTO type; it
  marshals correctly via the promoted `MarshalJSON`.
- **Raw programmatic access** via `State()` / `Definition()`. `InstanceID()` / `Status()` are
  intentionally **omitted** to keep the interface minimal (both are `pi.State().InstanceID` /
  `.Status`).
- The projection logic currently in `runtime/view.NewInstanceSnapshot` **moves into `service`** as
  the unexported `newInstanceJSON`. Transports return `service.ProcessInstance` and simply
  `json.Encode` it — no transport-side view construction for the full snapshot.
- **`ActionableView`** (the lightweight open-tasks/allowed-actions projection) is a *different*
  JSON shape and stays a separate transport/view concern — **out of scope** here.

### D4 — `WithDurableStore(DurableProvider)`, DB-vendor-free

The single durable switch takes an **interface**, so `service` imports no DB driver:

```go
type Option func(*engineConfig)

// graph-leaf overrides (all nil-ignored):
func WithProcessDriver(d *runtime.ProcessDriver) Option
func WithInstanceStore(s kernel.InstanceStore) Option
func WithDefinitions(reg kernel.DefinitionRegistry) Option
func WithLister(l kernel.InstanceLister) Option
func WithHumanTasks(taskStore humantask.TaskStore, az authz.Authorizer) Option
func WithClock(clk clock.Clock) Option

// one-call durable switch (interface only — no drivers in service's compile graph):
func WithDurableStore(p DurableProvider) Option

type DurableProvider interface {
    InstanceStore() kernel.InstanceStore
    Definitions()   kernel.DefinitionRegistry
    Lister()        kernel.InstanceLister
    TaskStore()     humantask.TaskStore   // durable SQL-backed store (D6)
    TimerStore()    kernel.TimerStore      // driver leaves; nil → in-mem
    CallLinkStore() kernel.CallLinkStore
}
```

`WithDurableStore` sets every resolved leaf from the provider **and** rebuilds the driver from
those leaves, so the whole graph is durable-coherent in one call.

The driver-backed implementation lives in **`persistence`** (where DB drivers are allowed):
`persistence.NewDurableProvider(pool)` (Postgres — dialect known by the pool type), plus MySQL /
SQLite `*sql.DB` variants. The **dialect ambiguity** that deferred the runtime version does not
apply: the consumer picks the dialect by choosing the constructor, and hands `service` an already
dialect-bound provider.

**Precedence:** `WithDurableStore` applies first; finer overrides later in the option list may
still replace an individual leaf (documented, last-writer-wins in option order).

### D5 — Default authorizer

- **Default authorizer:** allow-all, flagged in the DEBUG construction summary as a non-durable
  dev default (consistent with the in-memory "just works, not for production" framing).

### D6 — Durable `humantask.TaskStore` (in scope)

A SQL-backed `humantask.TaskStore` is implemented as part of this refactor so
`DurableProvider.TaskStore()` returns a **real durable store** (never nil in the durable path).
It follows the neutral-store + dialect pattern (ADR-0081): one implementation over
`database.Querier`, parametrized by `internal/persistence/dialect`, working on
Postgres / MySQL / SQLite.

**Interface to satisfy** (`humantask/humantask.go:111`):

```go
Upsert(ctx, t HumanTask) error
Get(ctx, taskToken string) (HumanTask, error)          // miss → humantask.ErrTaskNotFound
AssignedTo(ctx, actorID string) ([]HumanTask, error)   // sorted by TaskToken
ClaimableBy(ctx, actor authz.Actor) ([]HumanTask, error)
```

**Table** `wrkflw_human_task` (added to the neutral schema + per-dialect DDL, covered by the
existing cross-dialect schema-parity guardrail test):

| column | type | notes |
|---|---|---|
| `task_token` | text PK | matches the engine token |
| `instance_id` | text, indexed | parent process instance |
| `node_id` | text | source BPMN node |
| `state` | text, indexed | `TaskState.String()` — index supports the Unclaimed scan |
| `claimed_by` | text | empty when unclaimed; index supports `AssignedTo` |
| `eligibility` | json/jsonb/text | serialized `authz.AuthzSpec` |
| `candidates` | json/jsonb/text | resolved actor IDs |
| `vars` | json/jsonb/text | variable snapshot |
| `created_at` | timestamptz | store as UTC (ADR-0080) |
| `due_at` | timestamptz null | optional deadline |

**Query strategy (portable):** `Upsert` is an idempotent insert-or-replace keyed on `task_token`
(dialect `UpsertClause`). `Get` selects by PK. `AssignedTo` filters `claimed_by = ?` in SQL,
ordered by `task_token`. `ClaimableBy` selects `state = 'unclaimed'` in SQL, then applies the
same eligibility matching the `MemTaskStore` uses (actor ID ∈ candidates **OR** actor role ∩
`Eligibility.Roles`) **in Go** — keeping the predicate dialect-agnostic; JSON columns are
decoded per row. If `ClaimableBy` becomes a hot path, front it with a cache per the project's
hot-path guidance (ADR-0073) — deferred until measured.

**Placement:** implementation in `internal/persistence/store`; public constructors in
`persistence`: `persistence.NewTaskStore(pool)` (Postgres) + MySQL / SQLite `*sql.DB` variants,
each returning `humantask.TaskStore`. `persistence.NewDurableProvider` wires this into
`TaskStore()`. A `use-mockgen` mock is unnecessary (the in-mem `MemTaskStore` and the SQL store
both serve tests; conformance is exercised via `use-testcontainers` on all three dialects).

## Invariant — `service` stays DB-vendor-free (tested)

`go list -deps ./service` must contain **no** DB driver (`pgx`, `go-sql-driver/mysql`,
`modernc.org/sqlite`), **no** `database/sql`, and **no** `…/persistence` or
`…/internal/persistence` package. Enforced by a `service` black-box test (modeled on
`scripts/check-extraction.sh`, which uses `go list -deps`). The refactor fails CI if
`WithDurableStore` — or anything else — ever pulls a driver into the service compile graph. This
is the property that makes the `DurableProvider`-interface approach safe: the driver-backed impl
is in `persistence`, imported only by a consumer that *chooses* durability.

## Migration / impacted call sites

- **`service.New` → `service.NewEngine` (+ error):**
  `internal/transporttest/harness.go`, `examples/{production,sqlite,mysql}_wiring/main.go`, and
  service tests (`service_test.go`, `cancel_instance_test.go`, `resolve_incident_test.go`,
  `errors_test.go`, `coverage_gaps_test.go`).
- **`GetInstanceWithDefinition` removed → `GetInstance`:**
  `transport/http/httpcore/endpoints.go`, `transport/http/gin/gin_coverage_test.go`,
  `service/service.go`, `service/coverage_gaps_test.go`.
- **Single-instance return type → `ProcessInstance`:** all callers reading `resp.Status` /
  `resp` as `engine.InstanceState` update to `pi.State().Status` / `pi.State()`.
- **Snapshot construction moves into `service`:** the HTTP adapters
  (`transport/http/{httpcore,fiber,stdlib,gin}`) currently call `view.NewInstanceSnapshot` — they
  instead return / `json.Encode` the `service.ProcessInstance`. `runtime/view.InstanceSnapshot`
  and its builder are retired for the full-snapshot path (kept only if `ActionableView` still
  needs shared view types). Confirm during planning whether `runtime/view` can be trimmed or must
  stay for `ActionableView`.

## Testing strategy (TDD strict — red before green for every new symbol)

- `NewEngine` nil-guards → table test asserting `ErrNilDependency` for each explicitly-nil leaf
  and for a `DurableProvider` returning a nil leaf. Uses the project `assert`-closure table form.
- Default-graph coherence: zero-config `NewEngine()` → `StartInstance` → `GetInstance`
  round-trips in-memory; the same store instance is observed by both driver and reader.
- Each role interface is satisfied by `*Engine` (compile-time `var _ InstanceReader = (*Engine)(nil)`
  etc.) and by the composed `Service`.
- `ProcessInstance`: `json.Marshal` produces the expected projection; the serialized DTO type is
  not exported (assert via API — no exported snapshot fields); nil-definition marshals safely;
  `State()`/`Definition()` return the raw inputs; embedding in a consumer struct marshals.
- `WithDurableStore`: a fake `DurableProvider` wires all leaves; driver rebuilt from them;
  precedence with a later `WithInstanceStore` override.
- Vendor-free invariant test (`go list -deps ./service`).
- Durable `humantask.TaskStore` (D6): 3-dialect conformance via `use-testcontainers` —
  Upsert/Get round-trip, `Get` miss → `ErrTaskNotFound`, `AssignedTo` filters by `claimed_by` and
  sorts, `ClaimableBy` matches by candidate ID and by shared role and excludes claimed tasks;
  JSON columns round-trip `Eligibility`/`Candidates`/`Vars`; behavioural parity with `MemTaskStore`.
- Coverage ≥ 85% for `service`; `go test ./...` green; `golangci-lint` clean.

## Open items / follow-ups (out of scope)

1. Whether `ActionableView` should also become a `service`-owned self-serializing type.
2. Whether `runtime/view` can be deleted after the full-snapshot path moves into `service`.
3. Optional hot-path cache in front of the durable `TaskStore.ClaimableBy` (defer until measured,
   ADR-0073).

## Verification checklist

- [ ] `NewEngine(opts...) (*Engine, error)` with in-mem coherent defaults + fail-fast validation.
- [ ] `service.ErrNilDependency` sentinel added.
- [ ] DEBUG construction summary emitted.
- [ ] 5 role interfaces + composed `Service`; `*Engine` satisfies all (compile asserts).
- [ ] `ProcessInstance` interface + `json.Marshaler` + unexported DTO + `NewProcessInstance`.
- [ ] `GetInstanceWithDefinition` removed; single-instance methods return `ProcessInstance`.
- [ ] `WithDurableStore(DurableProvider)` + graph-leaf override options.
- [ ] Durable `humantask.TaskStore` (D6): `wrkflw_human_task` table added to neutral schema +
      per-dialect DDL; schema-parity guardrail test updated; `internal/persistence/store` impl;
      `persistence.NewTaskStore` (PG/MySQL/SQLite); 3-dialect conformance via testcontainers.
- [ ] `persistence.NewDurableProvider` (PG/MySQL/SQLite) implemented, wiring the durable TaskStore.
- [ ] Vendor-free `go list -deps ./service` test passes.
- [ ] All impacted call sites migrated (transports, examples, transporttest, tests).
- [ ] ADR-0098 written (Nygard template).
- [ ] `go test ./...` green, coverage ≥ 85%, `golangci-lint` clean.

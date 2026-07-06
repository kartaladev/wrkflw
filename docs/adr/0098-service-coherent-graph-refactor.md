# 0098. `service` coherent-graph refactor: `NewEngine`, interface segregation, self-serializing `ProcessInstance`, vendor-free durable provider

Status: **Accepted — 2026-07-06.**
Spec: `docs/specs/service-coherent-graph-refactor.md`.
Plan: `docs/plans/2026-07-06-service-coherent-graph-refactor.md`.
Follows: [ADR-0096](0096-sensible-default-driver-construction.md) (sensible-default `ProcessDriver` construction) and [ADR-0097](0097-default-definition-registry.md) (default `DefinitionRegistry`). Reuses the neutral-store + dialect pattern of [ADR-0081](0081-store-unification-dialect.md).

## Context

`service` is the hand-wired facade a consumer embeds to drive process instances. Before this change (`service/service.go`):

- `New(runner *runtime.ProcessDriver, tasks *task.TaskService, reg kernel.DefinitionRegistry, store kernel.InstanceStore, lister kernel.InstanceLister, taskStore humantask.TaskStore, opts ...EngineOption) *Engine` — **six required positional dependencies, no validation, no error return**, one option (`WithEngineClock`).
- The `Engine` held a `*runtime.ProcessDriver` **and** its own `store`/`reg`, which the driver *also* owned — a redundancy that could silently drift.
- Almost every method returned raw `engine.InstanceState`. `GetInstanceWithDefinition` returned state **and** `*model.ProcessDefinition` as a separate tuple.
- The def+state→JSON fusion (`runtime/view.NewInstanceSnapshot`) was built **transport-side**, so every HTTP adapter re-implemented the transformation.
- `service`'s dependency graph was **DB-vendor-free** (no pgx / go-sql-driver / modernc-sqlite / `database/sql` / `persistence`). ADR-0096 deferred a `WithDurableStore` option partly because a bare `*sql.DB` cannot disambiguate its dialect and because wiring a durable graph risked leaking DB drivers into consumers of the pure API.

Two collaborators also had no durable form: the human-task store existed only as an in-memory `humantask.MemTaskStore`, so a durable graph had no persistent task store.

## Decision

Rewrite the `service` public API as a coherent-graph, fail-fast, self-serializing facade, and add a durable SQL-backed `humantask.TaskStore` plus a vendor-free `DurableProvider`. Breaking changes are acceptable (pre-v0.1.0, no shims).

### D1 — `NewEngine(opts ...Option) (*Engine, error)` owns one coherent graph

The service **builds the in-memory leaves first, then builds the `ProcessDriver` from those same leaves**, so the driver and the service can never point at different stores/registries. There are no separate `store`/`reg` constructor params. Default graph (all in-memory, applied before options): `kernel.NewMemInstanceStore()` (also the lister), `runtime.DefaultDefinitionRegistry()`, `humantask.NewMemTaskStore()`, an allow-all authorizer, an internal `task.NewTaskService`, and a driver built via `runtime.WithInstanceStore`/`WithDefinitions`. **Fail-fast validation** runs before collaborators are built: every required leaf (instance store, definitions, lister, task store) must be non-nil, else a wrapped `service.ErrNilDependency` (new sentinel, mirroring `persistence.ErrNilDependency`/ADR-0083) is returned — placing validation ahead of `task.NewTaskService` so a nil leaf surfaces as `service.ErrNilDependency` rather than a downstream `kernel.ErrNilDependency`. A DEBUG `slog` construction summary mirrors `ProcessDriver.logConstructionSummary`. The old 6-positional `New` is removed.

### D2 — Role-grouped interface segregation

`Service` is composed from five role interfaces so a consumer references only what it needs: `InstanceStarter`, `InstanceReader`, `TaskManager`, `Messaging`, `InstanceOps`. The five admin ports (`DeadLetterAdmin`/`LineageAdmin`/`RelayStatsAdmin`/`TimerAdmin`/`PolicyAdmin`) were already separate and are unchanged.

### D3 — `ProcessInstance` is a clean interface that serializes itself

Single-instance methods return a `ProcessInstance` interface exposing only `Definition() *model.ProcessDefinition`, `State() engine.InstanceState`, and `json.Marshaler`. Its `MarshalJSON` emits a frontend-ready projection (state fields + definition-derived `scoped_actions`/`action_bindings`) via an **unexported** `instanceJSON` DTO — the serialized shape stays hidden, so a consumer can embed `ProcessInstance` in its own type and marshal with no transformation. `NewProcessInstance(def, st)` is exported for tests/consumers. The projection logic moved out of `runtime/view.NewInstanceSnapshot` into `service` as the unexported `newInstanceJSON`; transports now `json.Encode` the returned `ProcessInstance` directly. `GetInstance` is lenient — it returns the instance with a nil `Definition()` (no error) when the definition is unresolved — folding in the removed `GetInstanceWithDefinition`.

Deliberate return-type exceptions: `ListInstances` → `kernel.InstancePage` (a summary page; resolving a definition per row is too heavy); `DeliverMessage` → error only (a message may correlate to zero or many instances).

### D4 — `WithDurableStore(DurableProvider)`, DB-vendor-free

A single option flips the whole graph durable. It takes a `service.DurableProvider` **interface** (`InstanceStore`/`Definitions`/`Lister`/`TaskStore`/`TimerStore`/`CallLinkStore`), so `service` imports no DB driver. The driver-backed implementation lives in `persistence`: `persistence.NewDurableProvider(pool)` (Postgres) plus MySQL/SQLite `*sql.DB` variants — dialect is fixed by which constructor you call, dissolving the bare-`*sql.DB` ambiguity that deferred the runtime version. Precedence is last-writer-wins in option order; the lister auto-derive from the store is gated to the non-durable path so a nil provider lister surfaces via validation. An enforced invariant test (`go list -deps ./service`) fails CI if any driver ever enters the service compile graph.

### D5 — Default authorizer

The default authorizer is allow-all (`authz.AllowAll{}`), flagged in the DEBUG construction summary as a non-production dev default, consistent with the in-memory "just works, not for production" framing.

### D6 — Durable SQL-backed `humantask.TaskStore`

A neutral, dialect-parametrised SQL store (`internal/persistence/store.HumanTaskStore`) implements `humantask.TaskStore` over a new `wrkflw_human_task` table on Postgres/MySQL/SQLite (ADR-0081 pattern). `Upsert` is an idempotent insert-or-replace via a new `dialect.UpsertTask()` conflict clause; `Get` maps `sql.ErrNoRows` → `humantask.ErrTaskNotFound`; `AssignedTo` filters `claimed_by` in SQL; `ClaimableBy` selects `state='unclaimed'` in SQL then applies the exact `MemTaskStore` eligibility rule (actor ID ∈ candidates OR actor role ∩ eligibility roles) in Go, keeping the predicate dialect-agnostic. Timestamps route through the dialect codec (UTC; text on SQLite). Public constructors `persistence.NewTaskStore`/`NewMySQLTaskStore`/`NewSQLiteTaskStore` return `humantask.TaskStore` and back `DurableProvider.TaskStore()`. Conformance is exercised on all three dialects via testcontainers; the cross-dialect schema-parity guardrail covers the new table.

## Consequences

- **Breaking.** `service.New` and `GetInstanceWithDefinition` are removed; single-instance methods return `ProcessInstance`; `EngineOption`/`WithEngineClock` are replaced by `Option`/`WithClock`. All call sites migrated: the four HTTP adapters (via `httpcore`), the three `examples/*_wiring` mains, `internal/transporttest`, and the service/transport test fakes.
- **Coherence by construction.** Driver-vs-service store/registry drift is impossible; the round-trip is proven by a default-graph test.
- **Transports simplify.** The snapshot endpoint returns the self-serializing `ProcessInstance`; the `mapper func(engine.InstanceState) any` customization seam is preserved by feeding it `pi.State()`.
- **`runtime/view` is trimmed, not deleted.** `InstanceSnapshot`/`NewInstanceSnapshot` and the nested view types are retired (the projection now lives in `service`); `ActionableView` (a different shape) and `StatusString` remain.
- **Vendor-free is now enforced**, not merely observed. Durability is opt-in and isolated to consumers that import `persistence`.
- **Durable graphs gain a persistent task store**, closing the last in-memory-only leaf. A hot-path cache in front of `TaskStore.ClaimableBy` is deferred until measured (ADR-0073).
- Whether `ActionableView` should also become a service-owned self-serializing type, and whether `runtime/view` can eventually be deleted, remain open follow-ups.

Next free ADR: 0099.

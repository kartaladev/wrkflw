# service

Package `service` is the single **application-layer seam** between the HTTP transport
adapters (`transport/http/{stdlib,gin,fiber}`) and the workflow engine. Every operation is
transport-neutral — request and result types carry no HTTP concerns — so the HTTP
handlers are thin translators over this one interface.

The package plays three roles:
1. **Operation façade** — `Service` exposes the full consumer-facing operation
   surface in one typed interface, making it easy to mock or test transports
   without a real engine.
2. **Error normalization** — domain errors from `runtime`, `humantask`, `authz`,
   and `engine` are propagated as-is; `service.ErrConflict` is the only locally
   defined sentinel. Transport layers classify them to HTTP status codes (via
   `httpcore.ClassifyError`) without needing to import every sub-package.
3. **Admin port composition** — optional administrative capabilities (dead-letter
   management, timer inspection, lineage queries, policy management) are wired
   separately as fields on the adapter's `AdminRoutes` struct so a minimal deployment
   omits the overhead.

Import path: `github.com/kartaladev/wrkflw/service`

## Contents

1. [The `Service` interface](#the-service-interface)
2. [Constructing the engine (`New`)](#constructing-the-engine-new)
3. [Request types](#request-types)
4. [Errors](#errors)
5. [Admin ports](#admin-ports)

---

## The `Service` interface

`Service` is implemented by `*ProcessEngine`. Every method takes `ctx context.Context`
first. Domain errors (`runtime.ErrInstanceNotFound`, `runtime.ErrDefinitionNotFound`,
`authz.ErrNotAuthorized`, `runtime.ErrConcurrentUpdate`, `humantask.ErrTaskNotFound`)
are propagated **as-is** so the transport layer can classify them.

| Method | Argument | Returns | Purpose |
|---|---|---|---|
| `StartInstance` | `StartInstanceRequest` | `(ProcessInstance, error)` | Resolve the definition by `DefRef`, start a new instance, return the resulting instance. |
| `GetInstance` | `instanceID string` | `(ProcessInstance, error)` | Load the current state of an existing instance. |
| `DeliverSignal` | `DeliverSignalRequest` | `(ProcessInstance, error)` | Deliver a `SignalReceived` trigger to a parked instance. `ErrConflict` if terminal. |
| `DeliverMessage` | `DeliverMessageRequest` | `error` | Route a message to the waiting instance via the driver's waiter table. |
| `ClaimTask` | `ClaimTaskRequest` | `(ProcessInstance, error)` | Authorize + claim a human task, deliver the trigger, return the instance. |
| `CompleteTask` | `CompleteTaskRequest` | `(ProcessInstance, error)` | Authorize + complete a human task, deliver the trigger, return the instance. |
| `ReassignTask` | `ReassignTaskRequest` | `(ProcessInstance, error)` | Authorize + reassign a human task, deliver the trigger, return the instance. |
| `RefreshTaskCandidates` | `RefreshTaskCandidatesRequest` | `(ProcessInstance, error)` | Re-resolve an open task's candidate actors through the `ActorResolver` and replace the stored list (ADR-0150). Requires `WithActorResolver`. |
| `ListInstances` | `kernel.InstanceFilter` | `(kernel.InstancePage, error)` | Keyset-paginated list of instance summaries matching the filter. |
| `ResolveIncident` | `ResolveIncidentRequest` | `(ProcessInstance, error)` | Clear an open incident, grant `AddAttempts` (≤ 0 → 1), and re-drive the instance. |
| `CancelInstance` | `CancelInstanceRequest` | `(ProcessInstance, error)` | Terminate a running instance (runs cancel actions best-effort). `ErrConflict` if terminal. |

`ProcessInstance` is the self-serializing instance view (ADR-0098/ADR-0144); a
definition is no longer returned alongside it — the snapshot and actionable views are
built from the instance itself.

---

## Constructing the engine (`NewProcessEngine`)

```go
func NewProcessEngine(opts ...Option) (*ProcessEngine, error)
```

`NewProcessEngine` builds the facade from **functional options over a coherent
in-memory default graph** (ADR-0096/0097/0098; renamed from `NewEngine` in
ADR-0141). Called with no options it wires a fully-functional, non-durable engine:
an in-memory instance store, the process-global definition registry, an in-memory
human-task store, an allow-all authorizer, a real clock
(`clockwork.NewRealClock()`), `idgen.XID()`, and a driver built over those same
leaves (so the store the driver writes is the store the reader loads from). It
returns `ErrNilDependency` when a required leaf resolves to nil (e.g. a
`DurableProvider` that yields a nil store).

```go
// Zero-config, in-memory engine (tests / embedded single-node):
svc, err := service.NewProcessEngine()
```

Options override individual leaves; an option that receives nil is ignored (the
default is kept), except the leaves set together by `WithDurableStore`.

| Option | Effect |
|---|---|
| `WithProcessDriver(*runtime.ProcessDriver)` | Supply a pre-built driver (escape hatch for advanced wiring — e.g. one built with `runtime.WithHumanTasks` / `runtime.WithScheduler`). When set, the engine neither builds a driver from the leaves nor starts/stops it. |
| `WithInstanceStore(kernel.InstanceStore)` | Override the in-memory instance store. |
| `WithDefinitions(kernel.DefinitionRegistry)` | Override the default process-global definition registry. |
| `WithLister(kernel.InstanceLister)` | Override the instance lister (defaults to the instance store when it satisfies `kernel.InstanceLister`). |
| `WithHumanTasks(humantask.TaskStore, authz.Authorizer)` | Override the human-task store and authorizer used to build the internal task service. |
| `WithActorResolver(humantask.ActorResolver)` | Resolver the internal task service uses to re-expand a task's eligibility spec into actors. Required only by `RefreshTaskCandidates`, which returns `task.ErrNoActorResolver` without it. No default — pass the same resolver the driver got via `runtime.WithHumanTasks`. |
| `WithClock(clockwork.Clock)` | Override the time source used by the engine, the internal task service, and the default driver. Default `clockwork.NewRealClock()`; a nil clock is ignored. |
| `WithIDGenerator(idgen.Generator)` | Strategy used to mint every new process-instance ID. Default `idgen.XID()`. |
| `WithDurableStore(DurableProvider)` | Flip the whole graph durable in one call, setting every leaf from the provider and rebuilding the driver durable-coherent. A durable graph that uses human tasks or timers must also pass a fully-wired driver via `WithProcessDriver` — see the option's doc comment. |

> **Registry key contract:** the `DefinitionRegistry` must be keyed by
> `"DefID:DefVersion"` so an existing instance can be resolved by its state. Short
> aliases (e.g. the bare definition ID) may also be registered for `StartInstance`.

**Explicit wiring (in-memory store + human tasks):**

```go
store, _  := kernel.NewMemInstanceStore()
taskStore := humantask.NewMemTaskStore()
reg       := kernel.NewMapDefinitionRegistry(def) // def ...*model.ProcessDefinition (variadic)
resolver  := humantask.NewStaticActorResolver(map[string][]authz.Actor{})
az        := authz.RoleAuthorizer{}

driver, _ := runtime.NewProcessDriver(
    runtime.WithActionCatalog(cat),
    runtime.WithInstanceStore(store),
    runtime.WithHumanTasks(resolver, taskStore, az),
)

svc, err := service.NewProcessEngine(
    service.WithProcessDriver(driver),
    service.WithInstanceStore(store),
    service.WithDefinitions(reg),
    service.WithHumanTasks(taskStore, az),
)
```

For durable (Postgres / MySQL / SQLite) wiring, the runnable `examples/production_wiring`,
`examples/mysql_wiring`, and `examples/sqlite_wiring` open a SQL store
(`persistence.OpenPostgres` / `OpenMySQL` / `OpenSQLite`) and pass its leaves via
the granular per-leaf options (`WithInstanceStore`, `WithDefinitions`, …). The
one-call coherent-graph shortcut — `WithDurableStore(persistence.NewDurableProvider(...))`
— is illustrated (commented) in `examples/cache_wiring`.

Assemble this once at startup and inject `svc` into the transport adapters. The
service layer holds no goroutines and no persistent connections of its own — those
belong to the collaborators.

---

## Request types

Transport-neutral input DTOs (`service/request.go`):

**`StartInstanceRequest`**

| Field | Type |
|---|---|
| `DefRef` | `string` |
| `InstanceID` | `string` |
| `Vars` | `map[string]any` |

**`DeliverSignalRequest`**

| Field | Type |
|---|---|
| `InstanceID` | `string` |
| `Signal` | `string` |
| `Payload` | `map[string]any` |

**`DeliverMessageRequest`**

| Field | Type |
|---|---|
| `DefRef` | `string` |
| `Name` | `string` |
| `CorrelationKey` | `string` |
| `Payload` | `map[string]any` |

**`ClaimTaskRequest`** / **`CompleteTaskRequest`**

| Field | Type | Notes |
|---|---|---|
| `TaskID` | `string` | |
| `Actor` | `authz.Actor` | |
| `Outcome` | `string` | `CompleteTaskRequest` only. Business outcome the actor chose (e.g. `"approve"`); recorded on the completion audit and validated against the node's declared outcome set when it declares one (`engine.ErrInvalidOutcome`). Empty means none. |
| `Note` | `string` | `CompleteTaskRequest` only. Actor's free-text remark; optional. |
| `Output` | `map[string]any` | `CompleteTaskRequest` only. Output variables merged into the process variables. |

**`ReassignTaskRequest`**

| Field | Type |
|---|---|
| `TaskID` | `string` |
| `From` | `string` |
| `To` | `string` |
| `By` | `authz.Actor` |

**`RefreshTaskCandidatesRequest`**

| Field | Type | Notes |
|---|---|---|
| `TaskID` | `string` | |
| `By` | `authz.Actor` | Must satisfy the task's eligibility spec — same policy as `ReassignTaskRequest.By`. |

**`CancelInstanceRequest`**

| Field | Type |
|---|---|
| `InstanceID` | `string` |

**`ResolveIncidentRequest`**

| Field | Type | Notes |
|---|---|---|
| `InstanceID` | `string` | |
| `IncidentID` | `string` | |
| `AddAttempts` | `int` | Values ≤ 0 are coerced to 1. |

---

## Errors

This package defines exactly one sentinel; all other domain errors are propagated
from their owning packages so the transport layer classifies them uniformly.

| Sentinel | Meaning | Returned when |
|---|---|---|
| `ErrConflict` | Wrong-state operation against an instance/task. Transports map it to HTTP 422. The cause is wrapped, so `errors.Is(err, ErrConflict)` holds. | `DeliverSignal`/`CancelInstance` on a terminal instance; a task that is not open or whose instance is terminal; an `engine.ErrInvalidTransition` from a task trigger. |

Propagated (defined elsewhere, classified by `httpcore.ClassifyError`): `runtime.ErrInstanceNotFound`
(→ HTTP 404), `runtime.ErrDefinitionNotFound`, `authz.ErrNotAuthorized`
(→ HTTP 403), `runtime.ErrConcurrentUpdate` (→ HTTP 409),
`humantask.ErrTaskNotFound`.

---

## Admin ports

Optional, single-method-ish interfaces the transports mount **separately** from
`Service`. Each is supplied as a **field on the adapter's `AdminRoutes` struct**
(`transport/http/{stdlib,gin,fiber}`); a field left nil simply does not register its
routes. Admin routes are **default-absent by composition** (ADR-0095) — they exist only
when you mount `AdminRoutes` on a router group your own auth middleware already protects.
A consumer wires only the ports its infrastructure supports.

### `DeadLetterAdmin` (`deadletter.go`)

| Method | Signature | Purpose |
|---|---|---|
| `ListDeadLettered` | `(ctx, limit int) ([]runtime.DeadLetter, error)` | Up to `limit` dead-lettered outbox rows, oldest first. |
| `Redrive` | `(ctx, ids ...int64) (int, error)` | Reset the given dead rows to pending; returns the count re-queued (no ids → `(0, nil)`). |

Satisfied by the outbox **relay** (`persistence.Relay`, whose methods are a superset).
Wired via the `AdminRoutes.DeadLetters` field.

### `RelayStatsAdmin` (`opsadmin.go`)

| Method | Signature | Purpose |
|---|---|---|
| `OutboxStats` | `(ctx) (runtime.OutboxStats, error)` | Outbox health snapshot (pending count, dead count, oldest-pending age). |

Satisfied by the relay. Wired via the `AdminRoutes.RelayStats` field.

### `TimerAdmin` (`opsadmin.go`)

| Method | Signature | Purpose |
|---|---|---|
| `Stats` | `(ctx) (runtime.TimerStats, error)` | Armed-timer aggregate (count + next fire-at). |
| `ListArmed` | `(ctx) ([]runtime.ArmedTimer, error)` | All armed timers, in `(FireAt, InstanceID, TimerID)` order. |

Satisfied by the persistence `TimerStore` (Postgres/MySQL/SQLite). `runtime.MemTimerStore`
implements only `ListArmed`, so it is **not** a full `TimerAdmin`. Wired via the `AdminRoutes.Timers` field.

### `LineageAdmin` (`lineage.go`)

| Method | Signature | Purpose |
|---|---|---|
| `Lineage` | `(ctx, instanceID string) (runtime.InstanceLineage, error)` | Single-hop lineage: call parent (nil when root), call children, chain predecessor, chain successors. |

Satisfied by `*runtime.LineageReader`. Wired via the `AdminRoutes.Lineage` field.

### `PolicyAdmin` (`policyadmin.go`)

Runtime authorization-policy management without a restart. `ctx` is accepted for
interface consistency; the casbin implementation runs synchronously and ignores it.

`PolicyRule` (one casbin `p` line): `Subject`, `Object`, `Action` (all `string`).
`RoleBinding` (one casbin `g` line): `User`, `Role` (both `string`).

| Method | Signature | Purpose |
|---|---|---|
| `AddPolicy` | `(ctx, rule PolicyRule) (bool, error)` | Add a permission rule; `(false, nil)` if it already exists. |
| `RemovePolicy` | `(ctx, rule PolicyRule) (bool, error)` | Remove a permission rule; `(false, nil)` if absent. |
| `ListPolicies` | `(ctx) ([]PolicyRule, error)` | All permission rules in effect. |
| `AddRole` | `(ctx, binding RoleBinding) (bool, error)` | Add a role-inheritance rule (user → role); `(false, nil)` if already set. |
| `RemoveRole` | `(ctx, binding RoleBinding) (bool, error)` | Remove a role-inheritance rule; `(false, nil)` if not found. |
| `ListRoles` | `(ctx) ([]RoleBinding, error)` | All role-inheritance rules in effect. |

Satisfied by the casbin policy admin, obtained via
`casbinauthz.PolicyAdminFor(authz.Authorizer) (service.PolicyAdmin, bool)`. Wired via the `AdminRoutes.Policies` field.

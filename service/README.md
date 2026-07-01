# service

Package `service` is the single **application-layer seam** between the transport
adapters (REST, gRPC) and the workflow engine. Every operation is
transport-neutral — request and result types carry no HTTP/gRPC concerns — so the
REST and gRPC handlers are thin translators over this one interface.

Import path: `github.com/zakyalvan/krtlwrkflw/service`

## Contents

1. [The `Service` interface](#the-service-interface)
2. [Constructing the engine (`New`)](#constructing-the-engine-new)
3. [Request types](#request-types)
4. [Errors](#errors)
5. [Admin ports](#admin-ports)

---

## The `Service` interface

`Service` is implemented by `*Engine`. Every method takes `ctx context.Context`
first. Domain errors (`runtime.ErrInstanceNotFound`, `runtime.ErrDefinitionNotFound`,
`authz.ErrNotAuthorized`, `runtime.ErrConcurrentUpdate`, `humantask.ErrTaskNotFound`)
are propagated **as-is** so the transport layer can classify them.

| Method | Argument | Returns | Purpose |
|---|---|---|---|
| `StartInstance` | `StartInstanceRequest` | `(engine.InstanceState, error)` | Resolve the definition by `DefRef`, start a new instance, return the resulting state. |
| `GetInstance` | `instanceID string` | `(engine.InstanceState, error)` | Load the current state of an existing instance. |
| `GetInstanceWithDefinition` | `instanceID string` | `(engine.InstanceState, *model.ProcessDefinition, error)` | Load state **and** resolve its definition (for building a snapshot / actionable view). |
| `DeliverSignal` | `DeliverSignalRequest` | `(engine.InstanceState, error)` | Deliver a `SignalReceived` trigger to a parked instance. `ErrConflict` if terminal. |
| `DeliverMessage` | `DeliverMessageRequest` | `error` | Route a message to the waiting instance via the runner's waiter table. |
| `ClaimTask` | `ClaimTaskRequest` | `(engine.InstanceState, error)` | Authorize + claim a human task, deliver the trigger, return state. |
| `CompleteTask` | `CompleteTaskRequest` | `(engine.InstanceState, error)` | Authorize + complete a human task, deliver the trigger, return state. |
| `ReassignTask` | `ReassignTaskRequest` | `(engine.InstanceState, error)` | Authorize + reassign a human task, deliver the trigger, return state. |
| `ListInstances` | `runtime.InstanceFilter` | `(runtime.InstancePage, error)` | Keyset-paginated list of instance summaries matching the filter. |
| `ResolveIncident` | `ResolveIncidentRequest` | `(engine.InstanceState, error)` | Clear an open incident, grant `AddAttempts` (≤ 0 → 1), and re-drive the instance. |
| `CancelInstance` | `CancelInstanceRequest` | `(engine.InstanceState, error)` | Terminate a running instance (runs cancel actions best-effort). `ErrConflict` if terminal. |

---

## Constructing the engine (`New`)

```go
func New(
    runner    *runtime.Runner,           // 1. required
    tasks     *runtime.TaskService,       // 2. required
    reg       runtime.DefinitionRegistry, // 3. required
    store     runtime.Store,              // 4. required
    lister    runtime.InstanceLister,     // 5. required
    taskStore humantask.TaskStore,        // 6. required
    opts      ...EngineOption,            //    optional
) *Engine
```

The six required collaborators must be wired by hand (no DI container is imposed):

| # | Parameter | Type | Role |
|---|---|---|---|
| 1 | `runner` | `*runtime.Runner` | Drives execution — `Run` / `Deliver` / `DeliverMessage` / `ResolveIncident` / `CancelInstance`. |
| 2 | `tasks` | `*runtime.TaskService` | Authorizes human-task ops and returns the resulting engine trigger (`Claim`/`Complete`/`Reassign`). |
| 3 | `reg` | `runtime.DefinitionRegistry` | Resolves `DefRef` strings to `*model.ProcessDefinition`. |
| 4 | `store` | `runtime.Store` | Loads instance state for `GetInstance` and definition resolution. |
| 5 | `lister` | `runtime.InstanceLister` | Enumerates instance summaries for `ListInstances`. |
| 6 | `taskStore` | `humantask.TaskStore` | Resolves the owning instance ID from a task token in task-lifecycle ops. |

> **Registry key contract:** the `DefinitionRegistry` must be keyed by
> `"DefID:DefVersion"` so an existing instance can be resolved by its state. Short
> aliases (e.g. the bare definition ID) may also be registered for `StartInstance`.

**Options** (`type EngineOption func(*Engine)`):

| Option | Effect |
|---|---|
| `WithEngineClock(clk clock.Clock)` | Overrides the time source used to stamp signal triggers. Default `clock.System()`; a nil clock is ignored. |

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
| `TaskToken` | `string` | |
| `Actor` | `authz.Actor` | |
| `Output` | `map[string]any` | `CompleteTaskRequest` only |

**`ReassignTaskRequest`**

| Field | Type |
|---|---|
| `TaskToken` | `string` |
| `From` | `string` |
| `To` | `string` |
| `By` | `authz.Actor` |

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
| `ErrConflict` | Wrong-state operation against an instance/task. Transports map it to HTTP 422 / gRPC `FailedPrecondition`. The cause is wrapped, so `errors.Is(err, ErrConflict)` holds. | `DeliverSignal`/`CancelInstance` on a terminal instance; a task that is not open or whose instance is terminal; an `engine.ErrInvalidTransition` from a task trigger. |

Propagated (defined elsewhere, classified by transports): `runtime.ErrInstanceNotFound`
(→ 404 / `NotFound`), `runtime.ErrDefinitionNotFound`, `authz.ErrNotAuthorized`
(→ 403 / `PermissionDenied`), `runtime.ErrConcurrentUpdate` (→ 409 / `Aborted`),
`humantask.ErrTaskNotFound`.

---

## Admin ports

Optional, single-method-ish interfaces the transports mount **separately** from
`Service` (each behind its own `With*Admin` option and the transport's default-deny
gate). A consumer wires only the ports its infrastructure supports.

### `DeadLetterAdmin` (`deadletter.go`)

| Method | Signature | Purpose |
|---|---|---|
| `ListDeadLettered` | `(ctx, limit int) ([]runtime.DeadLetter, error)` | Up to `limit` dead-lettered outbox rows, oldest first. |
| `Redrive` | `(ctx, ids ...int64) (int, error)` | Reset the given dead rows to pending; returns the count re-queued (no ids → `(0, nil)`). |

Satisfied by the outbox **relay** (`persistence.Relay`, whose methods are a superset).
Wired via `transport/{rest,grpc}.WithDeadLetterAdmin`.

### `RelayStatsAdmin` (`opsadmin.go`)

| Method | Signature | Purpose |
|---|---|---|
| `OutboxStats` | `(ctx) (runtime.OutboxStats, error)` | Outbox health snapshot (pending count, dead count, oldest-pending age). |

Satisfied by the relay. Wired via `WithRelayStatsAdmin`.

### `TimerAdmin` (`opsadmin.go`)

| Method | Signature | Purpose |
|---|---|---|
| `Stats` | `(ctx) (runtime.TimerStats, error)` | Armed-timer aggregate (count + next fire-at). |
| `ListArmed` | `(ctx) ([]runtime.ArmedTimer, error)` | All armed timers, in `(FireAt, InstanceID, TimerID)` order. |

Satisfied by the persistence `TimerStore` (Postgres/MySQL). `runtime.MemTimerStore`
implements only `ListArmed`, so it is **not** a full `TimerAdmin`. Wired via `WithTimerAdmin`.

### `LineageAdmin` (`lineage.go`)

| Method | Signature | Purpose |
|---|---|---|
| `Lineage` | `(ctx, instanceID string) (runtime.InstanceLineage, error)` | Single-hop lineage: call parent (nil when root), call children, chain predecessor, chain successors. |

Satisfied by `*runtime.LineageReader`. Wired via `WithLineageAdmin`.

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
`casbinauthz.PolicyAdminFor(authz.Authorizer) (service.PolicyAdmin, bool)`. Wired via `WithPolicyAdmin`.

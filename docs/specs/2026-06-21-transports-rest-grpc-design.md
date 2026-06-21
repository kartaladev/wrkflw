# Transports (REST + gRPC) — design spec

- Status: Accepted
- Date: 2026-06-21
- Related: ADR-0004 (flat root-package layout), ADR-0008 (consumer façade over
  internal impl), ADR-0010 (casbin-backed authorizer), ADR-0011 (REST + gRPC
  transports)
- Plan: `docs/plans/2026-06-21-transports-rest-grpc.md`

## 1. Goal & scope

Ship **library-provided, consumer-mounted** REST and gRPC transports that expose
the already-implemented engine operations, plus **`ProcessInstance` response
customization** and **admin/superuser monitoring**. The deliverable is a set of
importable module-root packages a consumer embeds in *their* application — we
never ship or own a server, a `main`, or a daemon (ADR-0004).

Concretely, the consumer:

- mounts our REST `http.Handler` under any prefix of their choosing
  (`http.StripPrefix("/api/wf", restHandler)`), and
- registers our gRPC service on *their* `*grpc.Server` via a
  `grpc.ServiceRegistrar`.

Both transports call a single **transport-agnostic `Service` façade**. Neither
transport touches the engine core (`engine`, `runtime`, `model`, `humantask`)
directly, and the engine core never imports `net/http` server types, `grpc`, or
`protobuf`. This mirrors the vendor-isolation rule the project already enforces
for watermill, casbin, gocron, and clockwork.

In scope (v1):

- A `service` package: the transport-neutral `Service` interface + a concrete
  impl wiring `runtime.Runner` + `runtime.TaskService` + `runtime.DefinitionRegistry`
  + `runtime.Store` (+ a new instance lister).
- A `transport/rest` package: `NewHandler(svc, ...opts) http.Handler`
  (a `*http.ServeMux`), stdlib-only routing, JSON encode/decode, error mapping,
  response-view customization, and admin monitoring.
- A `transport/grpc` package + a committed generated `transport/grpc/workflowpb`
  package: a `.proto`, buf-generated code, a `WorkflowServiceServer` impl
  delegating to `Service`, and `RegisterWorkflowServiceServer(reg, svc)`.
- A new **instance-enumeration query** on the persistence side (`runtime` port +
  `MemStore` + Postgres impl + façade) so admin "list all" works. The engine
  core stays untouched.

Explicitly **out of scope** (see §9): auth/observability middleware *examples*
(consumers wrap the returned handler), an OpenAPI/Swagger document,
grpc-gateway / REST-from-proto generation, streaming / watch endpoints, and
richer admin filters (by definition, actor, time range).

## 2. Why a Service façade (and not transports-on-the-engine)

The operations to expose are spread across `runtime`:

- `runtime.Runner.Run(ctx, def, instanceID, vars) (engine.InstanceState, error)` — start.
- `runtime.Runner.Deliver(ctx, def, instanceID, trg)` — deliver a signal/trigger.
- `runtime.Runner.DeliverMessage(ctx, def, name, correlationKey, payload)` — deliver a message.
- `runtime.TaskService.Claim / Reassign / Complete(...)` — human-task interactions.
- `runtime.Store.Load(ctx, id)` — read one instance.
- `runtime.DefinitionRegistry.Lookup(defRef)` — resolve a definition.

Two awkward facts make a thin façade the right seam rather than calling these
from each transport:

1. **`Run`, `Deliver`, and `DeliverMessage` all require a `*model.ProcessDefinition`**,
   but the transport caller only has an instance ID (or a definition reference).
   The façade is the single place that resolves the definition from the
   `DefinitionRegistry` by the instance's `DefID:DefVersion` (read off the loaded
   `engine.InstanceState`), so neither transport re-implements that lookup.
2. **There is no instance-enumeration query today.** Admin "list all" needs one;
   the façade is where `ListInstances` lives, backed by a new persistence port.

A single façade means REST and gRPC share *exactly* one behavioural contract,
one definition-resolution rule, and one error-classification table — the two
transports differ only in wire encoding.

## 3. Package layout (module-root, no `pkg/` — ADR-0004)

```
service/                      — transport-agnostic Service interface + impl
  service.go / service_test.go
  request.go / request.go     — transport-neutral request & result value types
transport/
  rest/                       — NewHandler(...) http.Handler (*http.ServeMux)
    handler.go / handler_test.go
    options.go / options_test.go
    errors.go / errors_test.go     — mapToHTTPError
    view.go / view_test.go         — InstanceView default DTO
    admin.go / admin_test.go       — GET /admin/instances, keyset pagination
  grpc/
    proto/workflow.proto           — service + message definitions
    buf.gen.yaml                   — buf codegen config (dev-tooling)
    workflowpb/                    — COMMITTED generated *.pb.go + *_grpc.pb.go
    server.go / server_test.go     — WorkflowServiceServer impl + RegisterWorkflowServiceServer
    errors.go / errors_test.go     — mapToGRPCStatus
```

`service`, `transport/rest`, and `transport/grpc` are all module-root packages,
consistent with ADR-0004 (no `pkg/` prefix; public packages live at the root).
`transport/` is a grouping directory, not a vendor-isolation boundary —
`internal/` is reserved for non-exported concrete impls, and the transports are
deliberately public (the consumer must import them to mount them).

## 4. The Service façade (`service`)

```go
// Service is the transport-agnostic entry point. Both the REST handler and the
// gRPC server depend only on this interface.
type Service interface {
    StartInstance(ctx context.Context, req StartInstanceRequest) (engine.InstanceState, error)
    GetInstance(ctx context.Context, instanceID string) (engine.InstanceState, error)
    DeliverSignal(ctx context.Context, req DeliverSignalRequest) (engine.InstanceState, error)
    DeliverMessage(ctx context.Context, req DeliverMessageRequest) error
    ClaimTask(ctx context.Context, req ClaimTaskRequest) (engine.InstanceState, error)
    CompleteTask(ctx context.Context, req CompleteTaskRequest) (engine.InstanceState, error)
    ReassignTask(ctx context.Context, req ReassignTaskRequest) (engine.InstanceState, error)
    ListInstances(ctx context.Context, filter runtime.InstanceFilter) (runtime.InstancePage, error)
}
```

- `StartInstanceRequest` carries `DefRef string`, `InstanceID string`, and
  `Vars map[string]any`. The impl resolves the definition via the registry, then
  calls `Runner.Run`.
- `DeliverSignalRequest` carries `InstanceID` + `Signal` + `Payload`; the impl
  loads the instance, resolves its definition by `DefID:DefVersion`, builds the
  signal trigger (`engine.NewSignalReceived`), and calls `Runner.Deliver`.
- `DeliverMessageRequest` carries `Name`, `CorrelationKey`, `Payload`; delegates
  to `Runner.DeliverMessage`. The definition is resolved by the runner's own
  internal waiter table, so the façade need not resolve it here (no instance ID
  is known at the call site).
- `ClaimTaskRequest` / `CompleteTaskRequest` / `ReassignTaskRequest` carry the
  task token, the `authz.Actor`, and (for complete) the output vars / (for
  reassign) `from`/`to`. The impl calls `TaskService.Claim/Complete/Reassign`,
  then immediately delivers the returned trigger via `Runner.Deliver` (resolving
  the definition the same way `DeliverSignal` does) and returns the new state.
- `GetInstance` is a straight `Store.Load` + return state.
- `ListInstances` delegates to the new `runtime.InstanceLister` (§7).

The concrete `*service.Engine` (name chosen so `service.Service` reads as the
port and `service.Engine` as the impl) is constructed with plain constructor
parameters — never a DI container:

```go
func New(
    runner *runtime.Runner,
    tasks  *runtime.TaskService,
    reg    runtime.DefinitionRegistry,
    store  runtime.Store,
    lister runtime.InstanceLister,
) *Engine
```

All request/result types are transport-neutral value structs defined in
`service` — no JSON tags here (REST owns its DTOs), no proto coupling.

## 5. REST transport (`transport/rest`) — stdlib only

**Zero router dependencies.** Go 1.22+ `http.ServeMux` method+path patterns carry
the entire routing surface:

```go
mux := http.NewServeMux()
mux.Handle("POST /instances",              startHandler)
mux.Handle("GET  /instances/{id}",         getHandler)
mux.Handle("POST /instances/{id}/signals", signalHandler)
mux.Handle("POST /messages",               messageHandler)
mux.Handle("POST /tasks/{token}/claim",    claimHandler)
mux.Handle("POST /tasks/{token}/complete", completeHandler)
mux.Handle("POST /tasks/{token}/reassign", reassignHandler)
// admin routes mounted under the admin middleware — see §6:
mux.Handle("GET /admin/instances",         adminMW(listHandler))
```

Note: there is no `POST /instances/{id}/cancel` route and no unauthenticated
`GET /instances` list — the only list endpoint is the admin-gated
`GET /admin/instances`. `CancelInstance` is not implemented in v1; it is a
deferred follow-up.

Path variables are read with `r.PathValue("id")` / `r.PathValue("token")`. The
routes are **root-relative**; `NewHandler` returns the `*http.ServeMux` and the
consumer mounts it under any prefix with `http.StripPrefix`. (We return the
concrete `*http.ServeMux`-as-`http.Handler` so the consumer can also pull
individual patterns if they want; the signature is `http.Handler` to keep the
surface minimal.)

Request/response bodies are JSON via `encoding/json`. Every handler:

1. decodes the JSON body (415/400 on malformed body),
2. builds the corresponding `service` request value,
3. calls the façade,
4. on error, `mapToHTTPError(err)` writes the status + error body,
5. on success, serializes the result (an instance view for state-returning
   operations; `{}`/204 for `DeliverMessage`).

**No middleware framework.** Consumers inject auth, logging, and tracing by
*wrapping* the returned `http.Handler` themselves (`loggingMW(authMW(restHandler))`).
We do not build an interceptor chain for the non-admin routes — that is the
consumer's composition concern, and a framework here would be exactly the
"server convenience over library ergonomics" trade ADR-0004 forbids.

### 5.1 Response customization (`WithInstanceMapper`)

The v1 engine already exists with a `ProcessInstance` shape; consumers must be
able to keep their response contract with minimal migration. We expose a lambda
option rather than a typed View interface:

```go
func WithInstanceMapper(fn func(engine.InstanceState) any) Option
```

When set, REST serializes `fn(state)`'s output. When unset, REST serializes a
stable built-in `InstanceView` DTO (a struct with JSON tags covering instance
ID, def ID/version, status string, variables, started/ended timestamps, and a
compact token + task projection). The default `InstanceView` is the migration
anchor: a v1 consumer who wants the old shape supplies a one-line mapper; a new
consumer gets a sensible default for free. A lambda (not an interface) keeps the
option a single closure with no type to implement.

## 6. Admin / superuser monitoring (REST)

The requirement asks for "a way for admins to monitor all processes, likely
middleware and/or a set of HTTP handlers". We provide both:

```
GET /admin/instances?status=&limit=&cursor=
→ 200 {"items": [...InstanceView...], "next_cursor": "<opaque>", "has_more": true}
```

- **Keyset (cursor) pagination, not offset.** Offset pagination degrades under a
  growing instance table and double-counts under concurrent inserts. The cursor
  is an **opaque** `base64(json{started_at, instance_id})` token; clients treat
  it as a blob and pass it back verbatim. `limit` defaults to 50, max 200
  (over-max is clamped, not rejected).
- The query orders by `(started_at DESC, instance_id DESC)` and selects
  `limit+1` rows to compute `has_more` without a second count query (see §7).
- **`status` filter** maps a string (e.g. `running`, `completed`, `failed`) to
  `engine.Status`; an unknown value is a 400.

The admin routes are gated by a consumer-supplied middleware:

```go
func WithAdminMiddleware(mw func(http.Handler) http.Handler) Option
```

The library mounts the admin routes *under* `mw`; the consumer supplies the auth
logic (superuser check). This is the "middleware or http handlers" the
requirement names — we provide the handlers and the mount point, the consumer
provides the policy.

**Default-deny when no admin middleware is configured.** If the consumer never
calls `WithAdminMiddleware`, admin routes are mounted behind a built-in
deny-all middleware that returns `403` — admin monitoring is **opt-in**, so an
unconfigured deployment never accidentally exposes "list all instances"
unauthenticated. This is documented on `NewHandler` and on the option.

## 7. Instance-enumeration query (persistence)

The engine core has no enumeration API by design (the runner tracks message
waiters internally, see `DeliverMessage`). Admin listing needs one, added on the
**persistence** side without touching the engine core:

```go
// runtime ports (new)

type InstanceFilter struct {
    Status *engine.Status // nil = any status
    Limit  int            // <=0 → default 50; >200 → clamped to 200
    Cursor string         // opaque keyset cursor (empty = first page)
}

type InstanceSummary struct {
    InstanceID string
    DefID      string
    DefVersion int
    Status     engine.Status
    StartedAt  time.Time
    EndedAt    *time.Time
}

type InstancePage struct {
    Items      []InstanceSummary
    NextCursor string // empty when HasMore is false
    HasMore    bool
}

type InstanceLister interface {
    List(ctx context.Context, filter InstanceFilter) (InstancePage, error)
}
```

- **`MemStore`** implements `InstanceLister` by snapshotting its instances,
  filtering by status, sorting by `(StartedAt DESC, InstanceID DESC)`, decoding
  the cursor, slicing `limit+1`, and computing `NextCursor`/`HasMore`.
- **The Postgres store** implements it with a keyset query over the projected
  columns:

  ```sql
  SELECT instance_id, def_id, def_version, status, started_at, ended_at
  FROM   instances
  WHERE  ($1::int IS NULL OR status = $1)
    AND  ($2::timestamptz IS NULL OR (started_at, instance_id) < ($2, $3))
  ORDER BY started_at DESC, instance_id DESC
  LIMIT  $4;             -- $4 = limit+1
  ```

  The `(started_at, instance_id)` row-value comparison is the keyset seek;
  selecting `limit+1` lets the impl set `HasMore` and emit the next cursor from
  the last *returned* row without a `COUNT(*)`. Requires an index on
  `(started_at DESC, instance_id DESC)` (and a partial / leading-column form for
  the status filter).
- The persistence **façade** exposes the lister the same way it exposes the
  Store, so a consumer gets it from the same constructor.

Both impls are TDD'd: `MemStore` as a unit test, the Postgres impl via
`database.RunTestDatabase` (testcontainers). The cursor codec
(`encodeCursor`/`decodeCursor`) is unit-tested for round-trip and
malformed-input rejection.

## 8. gRPC transport (`transport/grpc`) — standard grpc-go + buf

We use **standard grpc-go + buf**, not connect-go. grpc-go is the lingua franca
of Go gRPC consumers and registers on a plain `*grpc.Server`; connect-go would
impose its own server/handler model on the consumer.

- A committed `.proto` at `transport/grpc/proto/workflow.proto` defines a
  `WorkflowService` with one RPC per façade operation (`StartInstance`,
  `GetInstance`, `DeliverSignal`, `DeliverMessage`, `ClaimTask`, `CompleteTask`,
  `ReassignTask`, `ListInstances`) and their request/response messages.
  `option go_package = ".../transport/grpc/workflowpb;workflowpb";`.
- `buf.gen.yaml` drives `protoc-gen-go` + `protoc-gen-go-grpc` to produce
  `transport/grpc/workflowpb/*.pb.go` and `*_grpc.pb.go`. **The generated code is
  committed to the repo** so a `go get` consumer never runs a code generator —
  buf/protoc is dev-tooling, not a `go.mod` dependency.
- `server.go` holds a struct embedding
  `workflowpb.UnimplementedWorkflowServiceServer` and delegating each RPC to the
  `service.Service`, translating proto messages ↔ façade request/result values
  and mapping domain errors via `mapToGRPCStatus`.
- The consumer mounts it with:

  ```go
  func RegisterWorkflowServiceServer(reg grpc.ServiceRegistrar, svc service.Service)
  ```

  `grpc.ServiceRegistrar` (satisfied by `*grpc.Server`) keeps us off any concrete
  server type — the consumer owns the `*grpc.Server` lifecycle.

The generated `workflowpb` messages are **not** TDD'd (generated code is exempt
from the cycle and the coverage bar). The **service-impl is** TDD'd via
`google.golang.org/grpc/test/bufconn` — an in-memory listener that exercises the
real wire path (marshalling, status codes) without a TCP port.

## 9. Error mapping

Domain sentinels are classified **in the transport layer only** — the engine
core never knows an HTTP status or a gRPC code. A single table drives both
transports' mappers:

| domain condition | HTTP | gRPC code |
|---|---|---|
| `runtime.ErrInstanceNotFound` / `runtime.ErrDefinitionNotFound` / `humantask.ErrTaskNotFound` | 404 | `NotFound` |
| `authz.ErrNotAuthorized` | 403 | `PermissionDenied` |
| `runtime.ErrConcurrentUpdate` | 409 | `Aborted` |
| validation / malformed request (`model.Validate`, bad JSON, bad status filter) | 400 | `InvalidArgument` |
| wrong-state (e.g. claiming an already-completed task) | 422 | `FailedPrecondition` |
| anything else | 500 | `Internal` |

REST error body is a simple shape for v1:

```json
{"error": "instance_not_found", "message": "runtime: instance not found: inst-42"}
```

`error` is a stable machine code; `message` is the human-readable detail.
Central `mapToHTTPError(w, err)` and `mapToGRPCStatus(err) error` own the table;
classification uses `errors.Is` against the sentinels. A wrong-state sentinel may
not exist yet for every case — where it doesn't, the mapper falls through to 500
and a follow-up adds the sentinel (tracked in §10).

## 10. Dependencies

- **REST: zero new dependencies.** stdlib `net/http`, `encoding/json`,
  `encoding/base64`, `errors`.
- **gRPC:** `google.golang.org/grpc` + `google.golang.org/protobuf` (latest
  stable). buf + `protoc-gen-go` + `protoc-gen-go-grpc` are **dev-tooling**,
  installed by the implementer (`go install` or `protoc`), never a `go.mod`
  dependency of the module.
- **Never** import `net/http` server types, `grpc`, or `protobuf` from
  `engine` / `runtime` / `model` / `humantask` — only `service` and `transport/*`
  may. A verification step asserts this with an import grep.

## 11. Deferred follow-ups

- Worked auth/observability middleware **examples** under `examples/` (a slog
  request logger, an OTel span wrapper, a bearer-token admin middleware).
- An OpenAPI/Swagger document and/or grpc-gateway to serve REST from the proto.
- Streaming / watch endpoints (server-stream instance state changes; subscribe
  to outbox events).
- Richer admin filters (by `def_id`, by actor, by time range) and a total count.
- A typed wrong-state sentinel set so 422 / `FailedPrecondition` fires precisely
  rather than falling through to 500.
- A REST↔gRPC parity conformance test asserting both transports classify every
  sentinel identically.

# 11. REST + gRPC transports mounted by the consumer over a Service façade

- Status: Accepted
- Date: 2026-06-21

## Context

The requirements ask for a "rest or grpc API", for the ability to "customize the
response of `ProcessInstance`" while minimizing migration from the existing v1
engine, and for "admin/super user [to] monitor all process — middleware or http
handlers". CLAUDE.md and ADR-0004 constrain how that API lands:

- **Library-first, consumer-mounted.** We ship an importable module, not a
  daemon. Transports must be things a consumer *mounts* in their own server — an
  `http.Handler` they hang under any prefix, and a gRPC service they register on
  their own `*grpc.Server`. We own no `main`; binaries are reference wiring only.
- **No transport leakage into the core.** `engine`, `runtime`, `model`, and
  `humantask` must never import `net/http` server types, `grpc`, or `protobuf`.
  This is the same vendor-isolation rule the project already enforces for
  watermill, casbin, gocron, and clockwork.
- **No `pkg/` prefix (ADR-0004).** Public packages live at the module root.

The operations to expose already exist, spread across `runtime`:
`Runner.Run` (start), `Runner.Deliver` (signal), `Runner.DeliverMessage`,
`TaskService.Claim/Reassign/Complete`, `Store.Load` (get one), and
`DefinitionRegistry.Lookup`. Three facts shape the design:

1. **`Run`/`Deliver`/`DeliverMessage` need a `*model.ProcessDefinition`**, but a
   transport caller has only an instance ID. Something must resolve the
   definition from the `DefinitionRegistry` by the loaded instance's
   `DefID:DefVersion`. Duplicating that across two transports would fork the
   resolution rule.
2. **There is no instance-enumeration query.** Admin "list all" requires one, and
   the engine core deliberately has none (the runner tracks message waiters
   internally rather than enumerating the store).
3. **The v1 engine already has a `ProcessInstance` response shape.** Consumers
   must be able to keep their contract without rewriting their clients.

## Decision

Add **REST and gRPC transports as module-root packages, mounted by the consumer,
both calling a single transport-agnostic `Service` façade**, plus a persistence
instance-enumeration query for admin monitoring.

- **`service/` (module root)** defines the `Service` interface and a concrete
  impl wiring `Runner` + `TaskService` + `DefinitionRegistry` + `Store` + a new
  `InstanceLister`. Operations: `StartInstance`, `GetInstance`, `DeliverSignal`,
  `DeliverMessage`, `ClaimTask`, `CompleteTask`, `ReassignTask`, `ListInstances`.
  The façade is the **only** place that resolves `*model.ProcessDefinition` from
  the registry by the instance's `DefID:DefVersion`, so both transports share one
  resolution rule, one behavioural contract, and one error classification. The
  impl is built with **plain constructors and interface parameters** — never a DI
  container, honouring the library-DI rule. Request/result types are
  transport-neutral value structs with no JSON tags and no proto coupling.

- **`transport/rest/` (module root)** exposes
  `NewHandler(svc service.Service, opts ...Option) http.Handler` returning a
  `*http.ServeMux`. Routing is **stdlib-only** (Go 1.22+ method+path patterns,
  `r.PathValue`) with **zero router dependencies**. Routes are root-relative so
  the consumer mounts them under any prefix via `http.StripPrefix`. Bodies are
  JSON via `encoding/json`. We build **no middleware framework** for the
  non-admin routes — consumers inject auth/logging/tracing by *wrapping* the
  returned handler.

- **Response customization is a lambda option**, not a typed View interface:
  `WithInstanceMapper(func(engine.InstanceState) any) Option`. When set, REST
  serializes the mapper's output; when unset, it serializes a stable built-in
  `InstanceView` DTO. The default `InstanceView` is the migration anchor (a v1
  consumer supplies a one-line mapper to keep the old shape), and a closure keeps
  the option free of any type to implement.

- **Admin monitoring** is `GET /admin/instances?status=&limit=&cursor=` returning
  `{items, next_cursor, has_more}`, using **keyset (cursor) pagination, not
  offset** — the cursor is an opaque `base64(json{started_at, instance_id})`,
  `limit` defaults to 50 / clamps at 200. Admin routes are gated by
  `WithAdminMiddleware(func(http.Handler) http.Handler) Option`: the library
  mounts the routes *under* the consumer-supplied middleware. **When no admin
  middleware is configured the routes are default-deny (403)** — admin monitoring
  is opt-in so an unconfigured deployment never exposes "list all" unauthenticated.

- **Instance enumeration is added on the persistence side, not the core.** New
  `runtime` ports — `InstanceFilter`, `InstanceSummary`, `InstancePage`, and an
  `InstanceLister` interface — are implemented on `MemStore` (filter + sort +
  cursor-slice) and on the Postgres store (a keyset query:
  `WHERE (started_at, instance_id) < cursor ORDER BY started_at DESC,
  instance_id DESC LIMIT limit+1` over projected columns) and surfaced through
  the persistence façade. The engine core is untouched.

- **`transport/grpc/` (module root) uses standard grpc-go + buf, not
  connect-go.** A committed `.proto` (`transport/grpc/proto/workflow.proto`) and
  `buf.gen.yaml` generate `transport/grpc/workflowpb/*.pb.go` + `*_grpc.pb.go`,
  which are **committed to the repo** (`option go_package = ".../workflowpb;workflowpb"`)
  so a `go get` consumer never runs a generator — buf/protoc is dev-tooling, not a
  `go.mod` dependency. A server struct embeds
  `UnimplementedWorkflowServiceServer` and delegates to `service.Service`;
  `RegisterWorkflowServiceServer(reg grpc.ServiceRegistrar, svc service.Service)`
  registers it on the consumer's own `*grpc.Server`. gRPC adds
  `google.golang.org/grpc` + `google.golang.org/protobuf`.

- **Error mapping lives in each transport, keyed off domain sentinels** — never
  in the core. One table drives both mappers (`mapToHTTPError` /
  `mapToGRPCStatus`): not-found → 404/`NotFound`; `authz.ErrNotAuthorized` →
  403/`PermissionDenied`; `runtime.ErrConcurrentUpdate` → 409/`Aborted`;
  validation/bad-request → 400/`InvalidArgument`; wrong-state → 422/
  `FailedPrecondition`; else → 500/`Internal`. REST error body is
  `{"error", "message"}` for v1. Classification uses `errors.Is`.

- **`service` and `transport/*` are the only packages allowed to import
  `net/http` server types, `grpc`, or `protobuf`.** A verification step asserts
  this with an import grep over `engine`/`runtime`/`model`/`humantask`.

- **TDD applies everywhere except generated code.** The façade, the REST
  handlers/options/view/admin, the gRPC service-impl (tested via `bufconn`), the
  error mappers, the cursor codec, and both `InstanceLister` impls are written
  red→green. The generated `workflowpb` messages are exempt from the cycle and
  the coverage bar; their consumer (the service-impl) carries the tests.

## Consequences

**Easier:** a consumer embeds the engine and mounts a REST `http.Handler` under
any prefix and/or registers a gRPC service on their own server, with zero router
dependencies on the REST side and the engine core never seeing `net/http`,
`grpc`, or `protobuf`. The single `Service` façade means REST and gRPC share one
behavioural contract, one definition-resolution rule, and one error table — they
differ only in wire encoding, so parity is structural rather than maintained by
hand. The `WithInstanceMapper` lambda lets v1 consumers keep their
`ProcessInstance` shape with one line while new consumers get a sensible default
`InstanceView`. Committing the generated `workflowpb` keeps `go get` consumers
free of any code generator. Keyset pagination keeps admin listing stable and
cheap as the instance table grows. Adding enumeration on the persistence side
leaves the engine core untouched, preserving its purity.

**Harder / trade-offs:** the codebase gains a generated package
(`workflowpb`) that must be regenerated and re-committed when the `.proto`
changes — contributors need buf/`protoc-gen-go`/`protoc-gen-go-grpc` installed,
and a verification step must catch a stale checkout, but `go get` consumers do
not. The new `InstanceLister` and its keyset query require a supporting Postgres
index on `(started_at DESC, instance_id DESC)` and an opaque-cursor codec the
client must treat as a blob; offset-style "jump to page N" is intentionally not
offered. Standard grpc-go over connect-go means no built-in HTTP/JSON fallback
for the gRPC surface (REST is the JSON path); grpc-gateway is a deferred
follow-up. Default-deny admin routes mean a consumer who forgets
`WithAdminMiddleware` sees 403s rather than an open endpoint — the safe failure,
but it must be documented so the 403 is not mistaken for a bug. Until a typed
wrong-state sentinel set lands, some 422/`FailedPrecondition` cases fall through
to 500 — a tracked follow-up.

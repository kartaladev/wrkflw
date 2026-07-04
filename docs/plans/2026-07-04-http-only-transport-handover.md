# Handover — HTTP-only transport refactor (remove gRPC + gin/fiber Mount adapters)

**Status:** NOT STARTED. Design analysis complete (below); brainstorming was
interrupted before a spec was written. A fresh session can start here cold.
**Base:** `origin/main == main == 97e18c1` (2026-07-04), working tree clean.
**Next free ADR:** 0094.

## Goal (from the maintainer)

1. **Completely remove gRPC support** — HTTP-based transport only.
2. **Support gin and fiber v3** beside the standard `net/http` adapter.
3. Each handler exposes a framework-specific mount method so consumers embed it in
   their existing routes:
   - stdlib: `Mount(mux *http.ServeMux)`
   - gin: `Mount(rg *gin.RouterGroup)`
   - fiber v3: `Mount(router fiber.Router)`
   Flexibility to include the handler in an existing consumer route is the point.

Go has no method overloading, so a single `Mount` name taking three types is
impossible on one type → use **per-framework adapter subpackages**, each importing
ONLY its framework, so a stdlib-only consumer never pulls gin/fiber deps.

## Part A — gRPC deletion inventory (CLEAN CUT)

gRPC is fully confined to `transport/grpc/` (pkg `grpctransport`). Nothing in
`examples/`, `runtime/`, `engine/`, `persistence/` imports it. `grpc` and `rest`
are independent siblings, both thin translators over the shared `service.Service`
façade (no shared grpc-only application logic). Deleting the tree is clean; the
shared `service/`, `runtime/*`, `authz`, `engine`, `humantask`,
`internal/observability` packages STAY (REST uses them identically).

**Deletion checklist:**
1. `rm -rf transport/grpc/` (server.go, secure_server.go, options.go,
   method_auth_interceptor.go, errors.go, observability.go, proto/workflow.proto,
   buf.gen.yaml, workflowpb/*.pb.go, and all `*_test.go`).
2. `go.mod`: drop `google.golang.org/grpc`, `google.golang.org/protobuf`,
   `google.golang.org/genproto/googleapis/rpc` (all grpc-ONLY), then `go mod tidy`.
3. `.github/dependabot.yml`: remove the `grpc-protobuf` update group.
4. `.golangci.yml`: remove the `transport/grpc/workflowpb` lint/format exclusions.
5. **`internal/authz/casbin/confinement_test.go:30`**: remove the
   `"./transport/grpc/..."` `go list` target — otherwise the confinement test
   fails on a missing package.
6. `doc.go:62`: edit the prose mention.
7. `README.md`: remove the gRPC usage block (~269-283), the `transport/grpc/`
   layout line (~494), and inline mentions (~8, 19-20, 235, 1278).
8. `CHANGELOG.md`: gRPC mentions (~109, 134).
9. Supersede ADRs **0011** (rest+grpc transports), **0051** (grpc fail-closed
   `NewSecureServer`), **0058** (grpc validation + per-method auth), **0062** (grpc
   error-details + `NewMethodAuthInterceptor`), **0029** (adds a grpc RPC — but the
   `DeadLetterAdmin` SEAM stays for REST). Touch incidental parity mentions in
   docs/runbooks/other ADRs as needed.

The grpc-only auth glue (`NewSecureServer`, `NewMethodAuthInterceptor`) is pure
transport wiring over the shared `authz` primitives — no auth *decision* logic is
lost. REST keeps its own default-deny admin middleware seam.

## Part B — current REST architecture (what the adapters build on)

**Great news: REST is already pure stdlib `net/http`** — `http.NewServeMux()` with
Go 1.22 method+pattern routing (`"GET /instances/{id}"`) and path params via
`r.PathValue(name)` ONLY (no chi/gorilla/gin anywhere). This is ideal for the
multi-framework design.

**Endpoints** (`transport/rest/handler.go:89-156`, `NewHandler(svc service.Service,
opts ...Option) http.Handler`):
- `POST /instances`, `GET /instances/{id}`, `GET /instances/{id}/snapshot`,
  `GET /instances/{id}/actionable`, `POST /instances/{id}/signals`,
  `POST /messages`, `POST /tasks/{token}/{claim,complete,reassign}`.
- Admin (deny-all default via `cfg.adminMiddleware`; opt-in
  `WithAdminMiddleware(func(http.Handler) http.Handler)`): `GET /admin/instances`,
  `POST /admin/instances/{id}/cancel`,
  `POST /admin/instances/{id}/incidents/{incidentID}/resolve` (TWO params),
  and conditional `/admin/{dead-letters,policies,role-bindings,relay-stats,timers}`
  + `GET /admin/instances/{id}/lineage`.
- Health is a SEPARATE `NewHealthHandler(checks ...HealthCheck) http.Handler`
  (`/healthz`, `/readyz`; no path params).

**Service seam:** handlers are methods on an unexported `handler` struct holding a
`service.Service` (transport-neutral, `service/service.go:24-81`) + 5 optional
admin interfaces (`DeadLetterAdmin`, `PolicyAdmin`, `RelayStatsAdmin`, `TimerAdmin`,
`LineageAdmin`) — all `ctx + plain args`, port to any framework unchanged.

**Response customization:** `WithInstanceMapper(func(engine.InstanceState) any)`
(default `NewInstanceView`) — the CLAUDE.md ProcessInstance-shape requirement.

**Tests:** pure `httptest` against `NewHandler`; shared `newTestHarness` wires an
in-memory stack. Tests hit full concrete paths so the mux populates `PathValue`.

## Part C — design constraints & intended approach

**Frictions to resolve (all in `transport/rest/`):**
1. **No decomposable route seam.** `NewHandler` bakes `http.NewServeMux()` +
   `traceMiddleware` and returns an opaque `http.Handler`; handlers are methods on
   an unexported struct. Need to expose endpoints as a **route-descriptor list**
   (`{Method, Pattern, Handler http.HandlerFunc, RouteTemplate string}`) or a
   `RegisterRoutes(mux)` seam so adapters can register onto gin/fiber routers.
2. **`traceMiddleware` reads `r.Pattern`** (`handler.go:228`) for the metric route
   label — populated by the stdlib mux, NOT gin/fiber. Fix: carry the route
   template on each route descriptor and label metrics from that, not `r.Pattern`.
3. **`err.Error()` leaks into 500 bodies** via `WriteHTTPError` / `classifyError`
   default branch (`transport/rest/errors.go:28,47-48`). Centralized → fix once:
   render a generic message for the 500 default, log detail server-side. (Also the
   queued P1-B hardening item.)

**Intended design (to confirm in brainstorming):**
- **Core package** (`transport/rest` or a renamed `transport/http`) exposes each
  endpoint as a stdlib `http.HandlerFunc` using `r.PathValue`, plus a
  route-descriptor list and the stdlib `Mount(*http.ServeMux)`. No framework deps.
- **Adapter subpackages** — e.g. `transport/rest/gin`, `transport/rest/fiber` —
  each imports ONLY its framework and provides `Mount(rg *gin.RouterGroup)` /
  `Mount(router fiber.Router)`. Each translates the stdlib route pattern
  (`{id}` → `:id`) and, in a wrapper, copies the framework's path params into the
  request via `req.SetPathValue(name, val)` before delegating to the core
  `http.HandlerFunc` (gin `gin.WrapH` / fiber `adaptor.HTTPHandler`). This keeps
  the core as plain net/http and the param-extraction uniform (`r.PathValue`).
- Preserve `WithAdminMiddleware`, `WithInstanceMapper`, and OTel metrics/tracing
  across all three adapters.

**Open questions for brainstorming (unresolved):**
1. Package naming: keep `transport/rest` or rename to `transport/http`? Adapter
   subpackage names (`transport/rest/gin` vs `transportgin`)?
2. Mount granularity: one `Mount` per handler group (core + health), or per
   endpoint? The maintainer said "each handler must have a `Mount`" — decide
   whether that means one handler object with `Mount`, or per-route mounting.
3. How much P1-B hardening to fold in now (panic-recovery, request-id, rate-limit,
   CORS, gRPC-parity health) vs. later.
4. Fiber v3 specifics: v3 is the target (not v2) — confirm the `fiber.Router` type
   and `adaptor` package APIs against fiber v3.

## How to resume

1. `superpowers:brainstorming` → resolve the open questions above → write the spec
   to `docs/specs/`. (This handover replaces the "explore context" step.)
2. `superpowers:writing-plans` → plan.
3. Implement with strict TDD; ADR-0094 (supersedes 0011/0051/0058/0062 for the
   gRPC removal + records the framework-adapter design).
4. Full analysis also captured in the session memory `http-only-transport-analysis`.

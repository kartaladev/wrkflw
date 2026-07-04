# HTTP-only Transport Refactor — Design Spec

**Date:** 2026-07-04
**Status:** Approved (brainstorming) — pending implementation plan
**Branch:** `refactor/http-only-transport`
**ADRs:** 0094 (HTTP-only: remove gRPC), 0095 (multi-framework composable `RouteCustomizer` adapters)

## Problem

The library ships two transport surfaces — `transport/rest` (stdlib `net/http`)
and `transport/grpc`. Two goals drive this refactor:

1. **Drop gRPC entirely.** It doubles the transport maintenance surface, pulls
   heavy `google.golang.org/{grpc,protobuf,genproto}` dependencies into the module
   graph, and is not required by the target consumers. HTTP is the sole transport.
2. **Let consumers compose the endpoints into their own routers, on their
   framework of choice, at paths they choose.** Today `NewHandler` returns an
   opaque `http.Handler` with the mux + trace middleware baked in — no route seam,
   one fixed shape. Consumers must be able to mount **individual, conceptually
   grouped route sets** (instance / task / message / admin / health) onto a stdlib
   `*http.ServeMux`, a `gin` router, or a `fiber` router — each group at whatever
   base path they want (e.g. admin + health under `/api/v1/workflow`, tasks under
   `/tasks`), using their own native middleware and auth.

Go has no function overloading, so a single `Mount` cannot accept three router
types. Each framework gets its own adapter **subpackage**, so the framework
dependency is isolated to the leaf a consumer opts into — a stdlib-only consumer
never pulls gin or fiber.

## Non-goals

- No change to the `service.Service` port or the admin seams (`DeadLetterAdmin`,
  `PolicyAdmin`, `RelayStatsAdmin`, `TimerAdmin`, `LineageAdmin`).
- No change to the JSON wire shape of request/response bodies, nor to the
  instance-response customization seam (`WithInstanceMapper` / `NewInstanceView`).
- No new endpoints. This restructures how existing endpoints are exposed, plus two
  centralized bug fixes (below).

## Architecture

Three native sibling implementations over one shared, transport-neutral root. No
`net/http` bridging/adaptor — each framework's handlers are native and idiomatic,
but stay **thin**: they bind the request natively, call a shared pure per-endpoint
function, and write the response natively. The business logic, wire DTOs, error
classification, and view mapping live **once** in the root.

```
transport/http/           package httpcore — SHARED ROOT (zero non-stdlib deps)
    customizer.go   RouteCustomizer[R] generic interface + Customize[R] driver
    handlers.go     pure per-endpoint funcs: StartInstance(ctx, svc, in) (int, any, error), …
    dto.go          shared request/response DTO structs (identical wire shapes)
    errors.go       ClassifyError(err) (status int, code string) ; ErrBadInput
    view.go         instance view mapper (NewInstanceView) + mapper type
    health.go       HealthCheck, HealthCheckFunc, pure readiness-probe logic
    observability.go shared OTel meters/tracer + RecordRequest(ctx, method, route, status, dur)
transport/http/stdlib/    native net/http ; groups implement RouteCustomizer[*http.ServeMux]
    instance.go task.go message.go admin.go health.go mount.go
transport/http/gin/       native gin      ; groups implement RouteCustomizer[gin.IRouter]
transport/http/fiber/     native fiber v3 ; groups implement RouteCustomizer[fiber.Router]
```

- `transport/grpc` is **deleted**; `transport/rest` is **removed** and its behavior
  re-expressed across `transport/http/*`.
- Package name `httpcore` avoids the `package http` clash with `net/http`. (Dir is
  `transport/http`; the shared package name is `httpcore`. Open to bikeshed in
  review; `golang-naming` mildly disfavors "core"-style names.)
- **Dependency isolation:** `transport/http/stdlib` (and `httpcore`) pull no
  third-party transport dep. `transport/http/gin` pulls gin (+ httpcore);
  `transport/http/fiber` pulls fiber v3 (+ httpcore). Neither pulls the other.
- Package `gin` importing the gin library is legal (a package's own name is not an
  in-scope identifier); `gin.IRouter` reads as the library type. Same for `fiber`.

## The `RouteCustomizer[R]` seam

One generic interface in the root, implemented per group in each subpackage. The
`Customize` method takes a **generic variadic `CustomizeOption[R]`** so a group can
be mounted with per-call configuration. Two needs drive this:

- **Base path** — the *only* way to sub-path a group under stdlib's `*http.ServeMux`
  (it has no native router group). Framework-neutral (a string prefix), so defined
  generically.
- **Group-specific middleware** — genuinely framework-typed (`gin.HandlerFunc` vs
  `fiber.Handler` vs `func(http.Handler) http.Handler`). A shared option type can't
  carry all three type-safely, so the **option is generic over `R`** and each
  framework contributes its own native middleware option that yields a
  `CustomizeOption[R]` for its router type.

```go
// httpcore — the exported config IS the extension surface: consumers may write
// their own CustomizeOption[R] (it's just func(*CustomizeConfig[R])) to set any field.
type CustomizeConfig[R any] struct {
    BasePath       string                          // pattern prefix (stdlib) or native group path (gin/fiber)
    Wrap           func(R) R                        // router transform: middleware / subrouter (default identity)
    InstanceMapper func(engine.InstanceState) any   // response-shape override (nil → NewInstanceView)
    // grows over time: ErrorMapper, per-group metric toggles, decoders, …
}
type CustomizeOption[R any] func(*CustomizeConfig[R])

// ResolveConfig applies opts over defaults (Wrap = identity, InstanceMapper = default view).
func ResolveConfig[R any](opts ...CustomizeOption[R]) CustomizeConfig[R]

// Built-in, framework-neutral options (any R). Each subpackage re-exports non-generic
// aliases (e.g. gin.WithBasePath) so callers never write the type arg.
func WithBasePath[R any](p string) CustomizeOption[R]
func WithInstanceMapper[R any](fn func(engine.InstanceState) any) CustomizeOption[R]
func WithRouterFunc[R any](fn func(R) R) CustomizeOption[R]   // general router escape hatch (compose middleware, subrouters, …)

type RouteCustomizer[R any] interface {
    Customize(r R, opts ...CustomizeOption[R])
}

// MountGroups is a batch helper (and the consumer extension seam): any type
// implementing RouteCustomizer[R] — including a consumer's own — can be mounted
// together at the router's current position. Groups needing distinct base paths or
// middleware call Customize directly with the relevant options.
func MountGroups[R any](r R, groups ...RouteCustomizer[R]) {
    for _, g := range groups {
        g.Customize(r)
    }
}
```

`Wrap` is how middleware is applied without a shared middleware type: a framework's
`WithMiddleware` composes onto `Wrap` a function that, for gin/fiber, derives a
native sub-router carrying the middleware (`r.Group("", mw...)`); the group registers
its routes onto `cfg.Wrap(router)`. Per-framework option constructors:

```go
// gin
func WithBasePath(p string) httpcore.CustomizeOption[gin.IRouter]        // alias of httpcore.WithBasePath[gin.IRouter]
func WithMiddleware(mw ...gin.HandlerFunc) httpcore.CustomizeOption[gin.IRouter]
// fiber
func WithBasePath(p string) httpcore.CustomizeOption[fiber.Router]
func WithMiddleware(mw ...fiber.Handler) httpcore.CustomizeOption[fiber.Router]
// stdlib
func WithBasePath(p string) httpcore.CustomizeOption[*http.ServeMux]
// (stdlib offers base path only; native-middleware is gin/fiber territory — a
//  stdlib consumer wraps the mux or an individual group's handlers themselves.)
```

So callers only touch their framework package for options — no explicit type args
(`gin.WithMiddleware` is just sugar composing onto `Wrap` via `WithRouterFunc`):

```go
gin.InstanceRoutes{Svc: svc}.Customize(g,
    gin.WithBasePath("/v1"),
    gin.WithMiddleware(authMw, rateLimitMw),
    httpcore.WithInstanceMapper[gin.IRouter](myMapper),
)
```

**Maximizing consumer flexibility.** `CustomizeOption[R]` is the single, open-ended
per-group seam. Because `CustomizeConfig[R]` is exported and options are plain
`func(*CustomizeConfig[R])`, a consumer can write their own option to set any
current-or-future config field, and `WithRouterFunc` is a total escape hatch to
transform the native router however the framework allows. Anything the built-in
options don't cover, the consumer reaches via `WithRouterFunc` or by implementing
`RouteCustomizer[R]` for a fully custom group — no library change required.

Each subpackage defines one **group type per conceptual cluster**, as an exported
config-carrying struct (idiomatic composable unit, à la `http.Server{}`):

Group structs carry **dependencies only** (what the group *is*); all mount-time
customization — base path, middleware, response mapping, … — flows through the
`Customize` options (how you mount it *here*).

```go
// transport/http/gin  (native gin; same shape in stdlib & fiber with their router type)
type InstanceRoutes struct { Svc service.Service }
func (c InstanceRoutes) Customize(g gin.IRouter, opts ...httpcore.CustomizeOption[gin.IRouter]) {
    cfg := httpcore.ResolveConfig(opts...)      // BasePath, Wrap, InstanceMapper, …
    r := cfg.Wrap(g)                            // apply middleware/subrouter (identity by default)
    r.POST(cfg.BasePath+"/instances", func(gc *gin.Context) {
        var in httpcore.StartInput
        if err := gc.ShouldBindJSON(&in); err != nil { writeErr(gc, httpcore.ErrBadInput); return }
        status, body, err := httpcore.StartInstance(gc.Request.Context(), c.Svc, in, cfg.InstanceMapper)
        if err != nil { writeErr(gc, err); return }
        gc.JSON(status, body)
    })
    // GET /instances/{id}[/snapshot|/actionable], POST /instances/{id}/signals …
}

type TaskRoutes struct    { Svc service.Service }               // /tasks/{token}/{claim,complete,reassign}
type MessageRoutes struct { Svc service.Service }               // POST /messages
type AdminRoutes struct   { Svc service.Service; DeadLetters service.DeadLetterAdmin; Policies service.PolicyAdmin; RelayStats service.RelayStatsAdmin; Timers service.TimerAdmin; Lineage service.LineageAdmin } // optional deps → route present only when non-nil
type HealthRoutes struct  { Checks []httpcore.HealthCheck }     // /healthz, /readyz
```

**All route patterns are relative** — no group hardcodes a base prefix. Where the
consumer calls `Customize`, that becomes the mount point. `writeErr` is a small
per-framework helper that runs `httpcore.ClassifyError(err)` and writes the JSON
error body natively.

### Base-path idiom (stdlib vs gin/fiber)

`http.ServeMux` has **no child/sub-router**, so under stdlib the *only* way to
sub-path a group is `WithBasePath`. gin and fiber **do** support sub-routers
(`Group(...)`), so their idiomatic form is a native group; `WithBasePath` still
works on them (applied as a prefix) but is redundant with a native group — pick one.

```go
// stdlib — no sub-router; base path via option
mux := http.NewServeMux()
stdlib.AdminRoutes{Svc: svc}.Customize(mux, httpcore.WithBasePath("/api/v1/workflow"))
stdlib.TaskRoutes{Svc: svc}.Customize(mux, httpcore.WithBasePath("/tasks"))

// gin — native sub-router
base  := g.Group("/api/v1/workflow")
tasks := g.Group("/tasks")
gin.AdminRoutes{Svc: svc}.Customize(base)
gin.TaskRoutes{Svc: svc}.Customize(tasks)
```

### Public entry points (per framework)

```go
// convenience: mount the standard operational set at one base
func Mount(r <RouterType>, svc service.Service, opts ...httpcore.CustomizeOption[<RouterType>])  // instance + task + message

// health convenience
func MountHealth(r <RouterType>, checks ...httpcore.HealthCheck)

// full flexibility: the exported group structs + their Customize method + httpcore.MountGroups
```

Common case:

```go
mux := http.NewServeMux()
stdlib.Mount(mux, svc)
stdlib.MountHealth(mux, dbCheck)
```

Flexible case (the motivating scenario):

```go
base  := g.Group("/api/v1/workflow")
tasks := g.Group("/tasks")
gin.AdminRoutes{Svc: svc, DeadLetters: dlq}.Customize(base)
gin.HealthRoutes{Checks: checks}.Customize(base)
gin.TaskRoutes{Svc: svc}.Customize(tasks)
gin.InstanceRoutes{Svc: svc}.Customize(base)
```

`NewHandler` and `NewHealthHandler` are **removed**.

## Admin authorization: default-absent by composition

Native handlers per framework break today's `WithAdminMiddleware(func(http.Handler)
http.Handler)` option (it is `net/http`-shaped and cannot wrap a gin/fiber handler).
Replacement, aligned with the composable model:

- `AdminRoutes` is a **separate, opt-in group** with **prefix-relative** patterns
  (`/instances/{id}/cancel`, `/instances`, `/dead-letters`, `/policies`, …).
- The consumer mounts it onto a router group **they have already secured** with
  their native auth middleware.
- **Default-deny becomes default-absent:** admin endpoints do not exist unless the
  consumer explicitly mounts `AdminRoutes` on a secured group — safer than today's
  403-returning built-in middleware, and idiomatic per framework.

```go
admin := g.Group("/api/v1/workflow", myAuthMiddleware) // consumer's native auth
gin.AdminRoutes{Svc: svc, DeadLetters: dlq, Policies: pol}.Customize(admin)
```

Conditional admin routes (dead-letters/policies/role-bindings/relay-stats/timers/
lineage) register only when their corresponding dependency field is non-nil,
mirroring today's conditional registration.

## Observability (unified, no `r.Pattern` dependency)

Today `traceMiddleware` reads `r.Pattern` post-routing to label spans/metrics — a
value only the stdlib `ServeMux` populates. Replaced by:

- The root owns the OTel meters + tracer and exposes
  `RecordRequest(ctx, method, route, status, dur)` plus span start/annotate helpers,
  keyed on the **static** route template known at registration time.
- Each framework wraps its registered routes with a thin native middleware (or
  per-route wrapper) that times the request, captures the status, and calls the
  shared helper with the static template.
- Result: identical spans (`wrkflw.rest <METHOD> <template>`) and metrics
  (`wrkflw_rest_requests_total`, `wrkflw_rest_request_duration_seconds` with
  `http.route`) across all three frameworks — no `"unmatched"` fallback, no reliance
  on any router-populated request field. Trace-context extraction reads `r.Header`
  (every framework populates it).

## Error-body 500 leak fixed

Today `WriteHTTPError` writes `err.Error()` into every response body, including the
5xx default branch — leaking internal error text. In `httpcore`: `ClassifyError`
returns `(status, code)`; the shared write path emits, for **5xx**, only the
sentinel code (`{"error":"internal_error"}`) and logs the raw error via the injected
telemetry logger; **4xx** keep their descriptive `message` (client-correctable).
Each framework's `writeErr` uses this path. Closes the queued P1-B item.

## gRPC removal (clean cut)

Per the pre-design inventory, gRPC is fully confined to `transport/grpc/`:

- `rm -rf transport/grpc/` (includes `proto/`, `workflowpb/`, `buf.gen.yaml`,
  `//go:generate`).
- Drop `google.golang.org/grpc`, `google.golang.org/protobuf`,
  `google.golang.org/genproto/googleapis/rpc` from `go.mod`; `go mod tidy`.
- Remove the `transport/grpc/workflowpb` lint/format exclusions in `.golangci.yml`.
- Remove the grpc-protobuf group from `.github/dependabot.yml`.
- Remove the `"./transport/grpc/..."` confinement target in
  `internal/authz/casbin/confinement_test.go`.
- Fix `doc.go` prose and README/CHANGELOG references to gRPC and `transport/rest`.

**Supersede** ADR-0011 (rest+grpc transports), 0051 (grpc fail-closed
`NewSecureServer`), 0058 (grpc validation + per-method auth), 0062 (grpc
error-details + `NewMethodAuthInterceptor`), and the grpc-`ResolveIncident`-RPC
portion of 0029. The admin/incident/dead-letter **seams** survive (transport-neutral).

## Dependencies (new)

```
github.com/gin-gonic/gin v1.x
github.com/gofiber/fiber/v3 v3.x  (+ github.com/gofiber/fiber/v3/middleware/... as needed)
```

Isolated to their adapter subpackages; `go mod tidy` after the gRPC removal nets out
the grpc/protobuf/genproto trees.

## Testing

- **TDD strict** (CLAUDE.md rule #6): a visible failing test precedes every new
  exported symbol — `RouteCustomizer`/`Customize`, each group type's `Customize`,
  each `Mount`/`MountHealth`, `httpcore` per-endpoint funcs, `ClassifyError`,
  `RecordRequest`. The `err.Error()` leak fix lands as a regression test first.
- `httpcore` per-endpoint functions are unit-tested directly (no HTTP) — the bulk of
  business-logic coverage lives here, tested once.
- **Adapter parity tests** are the key new coverage: for each framework, spin a real
  router (`http.ServeMux`, `gin.Engine`, `fiber.App`), mount the groups, hit the
  endpoints, and assert **parity** — same status codes, same JSON bodies, path
  params resolved, mounting at a custom base path works, admin absent until mounted
  on a secured group, conditional admin routes absent (404) when their dep is nil.
  Table-driven per the project `table-test` skill (assert-closure form).
- Observability tests assert the static route template on spans and on the
  `http.route` metric label for all three frameworks.

## Verification gates

- `go test -race -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1`
  — ≥85% line coverage on every touched package.
- `go test ./...` from the repo root — no regressions.
- `golangci-lint run ./...` — clean, including the new gin/fiber packages.
- `go build ./...` — including `examples/` reference wiring, migrated to the new API.

## Open follow-ups (out of scope here)

- gin/fiber reference examples under `examples/` (small; decide during planning).
- `httpcore` package-name bikeshed (`httpcore` vs `httpapi` vs putting the generic
  interface literally at `transport/http` with a name/dir mismatch).

# 0095 — Composable multi-framework HTTP mount adapters

- **Status:** Accepted
- **Date:** 2026-07-05

## Context

`transport/rest` (the package removed as part of ADR-0094) exposed a single
entry point: `NewHandler(svc, opts...) http.Handler`. It returned an opaque
`*http.ServeMux` with the mux, trace middleware, and all routes baked in. This
created four friction points:

1. **No route seam.** A consumer could not mount individual groups of endpoints
   (instance / task / message / admin / health) at different base paths or under
   different middleware chains without wrapping the whole handler. The admin
   group, for instance, was protected by a single `WithAdminMiddleware` option
   that had to apply equally to every admin endpoint.

2. **Framework lock-in.** The handler was `net/http`-shaped. Consumers who
   already used gin or fiber had to bridge into `net/http` (via adaptor
   packages) to mount the workflow routes, accepting a performance and ergonomic
   penalty and pulling an extra dependency. There was no native gin or fiber
   adapter.

3. **`r.Pattern` observability dependency.** `traceMiddleware` read
   `r.Pattern` post-routing to label spans and metrics with the route template.
   This field is only populated by `*http.ServeMux` (Go 1.22+); it is never
   populated for wrapped/nested handlers or on any non-stdlib router. Spans from
   nested mounts were labelled `"unmatched"`.

4. **5xx error leak.** `WriteHTTPError` wrote `err.Error()` into every
   response body, including the 5xx default branch. Internal error text was
   visible to any client that reached a 5xx.

5. **No request validation library.** Validation was hand-rolled
   (`if field == ""`) per-handler. Different handlers had inconsistent rules.
   There was no guarantee that adding a new framework adapter would validate
   identically.

The concurrent gRPC removal (ADR-0094) created an opportunity to redesign the
HTTP transport from scratch.

### Rejected alternatives

**Option A — Opaque `NewHandler` with a route-seam overlay.** Keep `NewHandler`
and add a `Routes()` map or registration callback so consumers can intercept the
registration. Rejected: the consumer still cannot use native framework types
(gin.HandlerFunc, fiber.Handler) as middleware; the opaque mux pattern resists
composition; adding gin/fiber would still require a bridging adapter.

**Option B — One adapter using `net/http` bridge/adaptor packages** (e.g.
`negroni`, `chi`, `adaptor`). Rejected: bridge packages convert between handler
types at the cost of performance, correctness (some adaptor packages do not
preserve context, trailer handling, or protocol-specific features), and an extra
dependency. They also require consumers to bring the bridging package into their
own module.

**Option C — Dual binding+validate tags** (bind via gin's built-in validator,
then validate again with shared rules). Rejected: duplicated rules in two forms
would diverge under maintenance. Gin's built-in binding validator is
`go-playground/validator/v10`; using a shared `Validate` call inside the pure
endpoint functions ensures all three frameworks apply identical rules from one
source.

## Decision

We ship three native sibling adapter subpackages over a shared pure root, with a
generic composable seam.

### Package layout

```
transport/http/            package httpcore — shared root (no third-party deps)
    seam.go          RouteCustomizer[R] interface + MountGroups[R]
    dto.go           shared request/response DTO structs (identical wire shapes)
    endpoints.go     pure per-endpoint funcs (StartInstance, GetInstance, …)
    admin_endpoints.go  pure admin-endpoint funcs (ListInstances, CancelInstance, …)
    errors.go        ClassifyError(err) → (status int, code string); ErrBadInput
    validate.go      Validate(v any) — go-playground/validator/v10; wraps as ErrBadInput
    view.go          NewInstanceView + InstanceMapper type
    health.go        HealthCheck, HealthCheckFunc, EvaluateReady, EvaluateLive
    observability.go Instrumentation.Observe + RecordRequest (static route template)
transport/http/stdlib/     native net/http; groups implement RouteCustomizer[*http.ServeMux]
transport/http/gin/        native gin; groups implement RouteCustomizer[gin.IRouter]
transport/http/fiber/      native fiber v3; groups implement RouteCustomizer[fiber.Router]
```

Package name `httpcore` avoids the `package http` clash with `net/http`. The
directory is `transport/http`; the shared package is `httpcore`.

**Dependency isolation:** `httpcore` and `transport/http/stdlib` pull no
third-party transport dependency. `transport/http/gin` pulls gin (+ httpcore);
`transport/http/fiber` pulls fiber v3 (+ httpcore). Neither gin nor fiber pulls
the other. A stdlib-only consumer's dependency graph is unchanged from today.

### The `RouteCustomizer[R]` seam

One generic interface in `httpcore`, implemented per group in each subpackage:

```go
type RouteCustomizer[R any] interface {
    Customize(r R, opts ...CustomizeOption[R])
}

type CustomizeConfig[R any] struct {
    BasePath       string
    Wrap           func(R) R
    InstanceMapper func(engine.InstanceState) any
    Logger         *slog.Logger
    TracerProvider trace.TracerProvider
    MeterProvider  metric.MeterProvider
}
type CustomizeOption[R any] func(*CustomizeConfig[R])

func ResolveConfig[R any](opts ...CustomizeOption[R]) CustomizeConfig[R]

// Framework-neutral built-ins (any R):
func WithBasePath[R any](p string) CustomizeOption[R]
func WithInstanceMapper[R any](fn func(engine.InstanceState) any) CustomizeOption[R]
func WithRouterFunc[R any](fn func(R) R) CustomizeOption[R]
func WithLogger[R any](l *slog.Logger) CustomizeOption[R]
func WithTracerProvider[R any](tp trace.TracerProvider) CustomizeOption[R]
func WithMeterProvider[R any](mp metric.MeterProvider) CustomizeOption[R]

func MountGroups[R any](r R, groups ...RouteCustomizer[R])
```

`Wrap` is how middleware is applied without a shared middleware type. For
gin/fiber, a framework's `WithMiddleware` option composes onto `Wrap` a function
that derives a native sub-router carrying the middleware. The group registers its
routes onto `cfg.Wrap(router)`.

Each subpackage re-exports non-generic `WithBasePath` aliases so callers never
write the type argument. gin and fiber additionally export `WithMiddleware` for
their native handler types.

### Group structs

Each subpackage defines one exported struct per conceptual group. Structs carry
**dependencies only**; all mount-time customisation flows through the `Customize`
options:

```go
// transport/http/gin (same shape in stdlib & fiber with their router type)
type InstanceRoutes struct { Svc service.Service }
type TaskRoutes    struct { Svc service.Service }
type MessageRoutes struct { Svc service.Service }
type AdminRoutes   struct {
    Svc         service.Service
    DeadLetters service.DeadLetterAdmin   // optional; route absent when nil
    Policies    service.PolicyAdmin       // optional; route absent when nil
    RelayStats  service.RelayStatsAdmin   // optional; route absent when nil
    Timers      service.TimerAdmin        // optional; route absent when nil
    Lineage     service.LineageAdmin      // optional; route absent when nil
}
type HealthRoutes struct { Checks []httpcore.HealthCheck }
```

### Admin-by-composition (default-absent)

`AdminRoutes` is a separate, opt-in group. The consumer mounts it onto a router
group they have already secured with their native auth middleware.
**Default-absent** replaces the old default-deny (403): admin endpoints simply do
not exist in a deployment that does not mount `AdminRoutes`. This is safer than a
built-in default-deny gate and idiomatic per framework.

Conditional admin sub-routes (dead-letters, policies, relay-stats, timers,
lineage) register only when the corresponding struct field is non-nil, mirroring
today's conditional registration.

### Public entry points

```go
// Convenience: mount the standard operational set (instance + task + message).
func Mount(r <RouterType>, svc service.Service, opts ...httpcore.CustomizeOption[<RouterType>])
// Health convenience.
func MountHealth(r <RouterType>, checks ...httpcore.HealthCheck)
```

`NewHandler` and `NewHealthHandler` are **removed**.

### Observability fix (static route template)

`traceMiddleware`'s `r.Pattern` dependency is removed. The root owns the OTel
meters and tracer and exposes `Instrumentation.Observe(ctx, method, route,
status, dur)` keyed on the **static** route template known at registration time.
Each framework wraps its registered routes with a thin native per-route wrapper
that times the request, captures the status, and calls the shared helper with
the static template. Result: identical spans (`wrkflw.rest <METHOD> <template>`)
and metrics (`wrkflw_rest_requests_total`, `wrkflw_rest_request_duration_seconds`
with `http.route`) across all three frameworks — no `"unmatched"` label, no
router-populated request field.

### 5xx error-body redaction fix

`ClassifyError(err) (status int, code string)` classifies errors by
`errors.Is`. For **5xx** the response body contains only the sentinel code
(`{"error":"internal_error"}`) and the raw error is logged via the injected
logger. **4xx** retain their descriptive `message` (client-correctable). This
closes the queued P1-B item (internal error text visible in 500 responses).

### Request validation (`go-playground/validator/v10`)

`httpcore.Validate(v any) error` wraps `go-playground/validator/v10`. Validation
rules live as `validate:"..."` struct tags on the shared `httpcore` DTO structs.
`Validate` is called inside the pure endpoint functions so all three frameworks
enforce identical rules and return byte-identical 400 bodies — the parity
requirement. This is the same library gin embeds; using one shared rule source
avoids duplicated `binding:` tags and keeps the frameworks in lockstep.

`go-playground/validator/v10` is recorded in `go.mod` as a direct dependency of
`transport/http/httpcore`. (Its transitive use inside gin was already an
indirect dependency of `transport/http/gin`; this makes it direct and version-
pinned from the root of the shared package.)

### New dependencies

| Package | Version | Scope |
|---|---|---|
| `github.com/gin-gonic/gin` | v1.12.0 | `transport/http/gin` only |
| `github.com/gofiber/fiber/v3` | v3.4.0 | `transport/http/fiber` only |
| `github.com/go-playground/validator/v10` | v10.x | `transport/http/httpcore` |

## Consequences

**Positive**

- A consumer can mount individual route groups at different paths and under
  different native middleware with no bridging adapters — the composable model
  (`Customize` + `MountGroups`) matches how each framework is used natively.
- Stdlib-only consumers retain a zero-framework-dependency wiring path.
- Admin endpoints are absent (not 403) unless explicitly mounted on a secured
  group, which is safer and clearer.
- Observability labels are always accurate (static template); spans are never
  labelled `"unmatched"`.
- The 5xx error-text leak is closed at the shared `ClassifyError`/`writeErr`
  layer — the fix applies to all three frameworks without repetition.
- Request validation rules are declared once in `httpcore` DTO struct tags and
  enforced identically across all three adapters.

**Negative / trade-offs**

- Three adapter packages replace one: more code surface to maintain. Mitigated
  by the shared `httpcore` root carrying all business logic — adapters are thin
  (bind + call pure func + write response).
- `NewHandler`/`NewHealthHandler` are removed; existing consumers must migrate
  to `Mount`/`MountHealth` or the group-struct `Customize` calls.
- gin and fiber bring new transitive dependencies into the module. They are
  isolated to their leaf subpackages; a stdlib consumer never pulls them.
- `go-playground/validator/v10` is now a direct dependency of `httpcore`. It is
  already an indirect dependency of any gin consumer, so the net addition for
  them is zero; for stdlib/fiber consumers it is a small new dep.

## References

- Spec: `docs/specs/2026-07-04-http-only-transport-refactor.md`
- Supersedes the HTTP transport portions of ADR-0011 (REST transport + `NewHandler` design).
- Accompanies ADR-0094 (gRPC removal).
- Builds on ADR-0011 (`service.Service` façade, which survives unchanged),
  ADR-0019 (observability runtime boundary), ADR-0029 (`DeadLetterAdmin` seam,
  which survives unchanged).

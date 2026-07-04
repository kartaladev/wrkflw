# HTTP-only Transport Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove gRPC entirely and re-express the HTTP transport as three native sibling adapters (stdlib `net/http`, gin, fiber v3) over one shared, transport-neutral `httpcore` root, with a generic `RouteCustomizer[R]` seam and an open-ended `CustomizeOption[R]` per-group flexibility mechanism.

**Architecture:** `transport/http/httpcore` holds all transport-neutral logic — pure per-endpoint functions (extracted from today's `transport/rest` handlers), DTOs, error classification, the instance view mapper, health-probe evaluation, observability recording, and the generic seam (`RouteCustomizer[R]`, `CustomizeConfig[R]`, `CustomizeOption[R]`, `MountGroups[R]`). Each framework subpackage (`stdlib`, `gin`, `fiber`) declares exported group structs (`InstanceRoutes`, `TaskRoutes`, `MessageRoutes`, `AdminRoutes`, `HealthRoutes`) that carry dependencies only and implement `Customize(r R, opts ...httpcore.CustomizeOption[R])` by natively binding the request, calling the shared `httpcore` function, and natively writing the response. `transport/grpc` and `transport/rest` are deleted.

**Tech Stack:** Go 1.25, `net/http`, `github.com/gin-gonic/gin` (v1.x), `github.com/gofiber/fiber/v3` (v3.x) + `github.com/gofiber/fiber/v3/middleware/adaptor`, `expr-lang/expr` (unchanged), OpenTelemetry (`go.opentelemetry.io/otel`), `log/slog`.

## Global Constraints

- Go 1.25; module path `github.com/zakyalvan/krtlwrkflw` (root packages, no `pkg/` prefix — ADR-0004).
- **TDD strict** (CLAUDE.md rule #6): every new exported symbol and behavioural change is preceded by a **visible failing test** run via `go test`. No implementation before red. No batching test+impl in one edit.
- Never import gin/fiber outside their own adapter subpackage; `httpcore` and `stdlib` pull **zero** third-party transport deps.
- Never import watermill/casbin/gocron/clockwork from transport code — go through existing abstractions.
- Prefer **black-box tests** (`package <name>_test`). Table-driven tests use the project `table-test` skill (assert-closure form, `ctx` modifier, `t.Context()`). Mocks via `use-mockgen`.
- Coverage ≥85% line on every touched package. `golangci-lint run ./...` clean. `go test ./...` green. `go build ./...` green (incl. `examples/`).
- Conventional Commits scoped `transport`/`grpc`/`docs`. Every commit ends with the `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>` trailer. Commit per logical change.
- Error sentinel messages use the `workflow-` prefix convention (e.g. `workflow-httpcore: ...`).
- Branch: `refactor/http-only-transport` (already created).

## File Structure

**Deleted:**
- `transport/grpc/**` (entire tree: `proto/`, `workflowpb/`, `buf.gen.yaml`, all `.go`).
- `transport/rest/**` (logic relocates into `transport/http/**`).

**Created — shared root `transport/http/httpcore/` (package `httpcore`, stdlib-only):**
- `seam.go` — `RouteCustomizer[R]`, `CustomizeConfig[R]`, `CustomizeOption[R]`, `ResolveConfig`, `WithBasePath`, `WithInstanceMapper`, `WithRouterFunc`, `WithLogger`, `WithTracerProvider`, `WithMeterProvider`, `MountGroups`.
- `dto.go` — request DTOs (`StartInput`, `SignalInput`, `MessageInput`, `Actor`, `ClaimInput`, `CompleteInput`, `ReassignInput`, admin query/body DTOs).
- `endpoints.go` — pure per-endpoint funcs (service-facing).
- `admin_endpoints.go` — pure admin per-endpoint funcs.
- `errors.go` — `ErrBadInput`, `ErrorBody`, `ClassifyError`.
- `view.go` — `NewInstanceView` + instance view type (moved from `transport/rest/view.go`).
- `health.go` — `HealthCheck`, `HealthCheckFunc`, `EvaluateHealth`, `EvaluateReady`.
- `observability.go` — `Instrumentation`, `RecordRequest`, span helpers.

**Created — three adapter subpackages (package `stdlib` / `gin` / `fiber`):**
- `transport/http/stdlib/{groups.go,mount.go,options.go,write.go,observe.go}`
- `transport/http/gin/{groups.go,mount.go,options.go,write.go,observe.go}`
- `transport/http/fiber/{groups.go,mount.go,options.go,write.go,observe.go}`
- plus matching `_test.go` per file (black-box).

**Modified:**
- `go.mod` / `go.sum` — drop grpc/protobuf/genproto; add gin, fiber/v3.
- `.golangci.yml` — drop `transport/grpc/workflowpb` exclusions.
- `.github/dependabot.yml` — drop grpc-protobuf group.
- `internal/authz/casbin/confinement_test.go` — drop the `"./transport/grpc/..."` target; add `"./transport/http/..."` if the confinement test enumerates transport packages.
- `doc.go` — prose (remove gRPC; rename rest→http).
- `README.md`, `CHANGELOG.md` — gRPC + `transport/rest` references.
- `examples/**` — any reference wiring using `rest.NewHandler` / grpc.
- `docs/adr/0094-*.md`, `docs/adr/0095-*.md` — new ADRs; mark 0011/0051/0058/0062/0029(grpc-parts) superseded.

---

## Phase 0 — gRPC removal (independent; run first to free the dep graph)

### Task 1: Delete gRPC transport and its dependencies

**Files:**
- Delete: `transport/grpc/**`
- Modify: `go.mod`, `.golangci.yml`, `.github/dependabot.yml`, `internal/authz/casbin/confinement_test.go`, `doc.go`

**Interfaces:**
- Consumes: nothing.
- Produces: a module with no gRPC packages/deps. No exported symbols added.

- [ ] **Step 1: Capture the confinement test as the red signal.** Open `internal/authz/casbin/confinement_test.go`; find the target list (~line 30) containing `"./transport/grpc/..."`. Remove that entry. Run:

```bash
go test ./internal/authz/casbin/... 2>&1 | head -30
```

Expected: still passes (it no longer references the soon-deleted package). If the test enumerates ALL transport packages positively, note it — it will be updated in Phase 2 to include `./transport/http/...`.

- [ ] **Step 2: Delete the tree and prune deps.**

```bash
rm -rf transport/grpc
# remove the three grpc-only requires from go.mod
go mod edit -droprequire=google.golang.org/grpc \
            -droprequire=google.golang.org/protobuf \
            -droprequire=google.golang.org/genproto/googleapis/rpc
go mod tidy
```

- [ ] **Step 3: Remove grpc references from tooling + docs.** In `.golangci.yml` delete any `transport/grpc/workflowpb` `issues.exclude-rules` / formatter-exclusion entries. In `.github/dependabot.yml` delete the grpc-protobuf group. In `doc.go` (~line 62) remove the gRPC sentence and rename `transport/rest` mentions to `transport/http` (the http packages land in Phase 1–2; wording may say "HTTP transport adapters under transport/http").

- [ ] **Step 4: Verify build + vet clean of grpc.**

```bash
go build ./... && go vet ./... 2>&1 | head -20
grep -rn "google.golang.org/grpc\|transport/grpc\|workflowpb" --include=*.go . ; echo "exit=$?"
```

Expected: build OK; grep prints nothing (exit=1).

- [ ] **Step 5: Commit.**

```bash
git add -A
git commit -m "refactor(grpc): remove gRPC transport, proto codegen, and deps

$(printf 'Deletes transport/grpc/** and drops google.golang.org/{grpc,protobuf,\ngenproto/googleapis/rpc}. HTTP is the sole transport. Supersedes ADR-0011/\n0051/0058/0062 and the grpc ResolveIncident RPC of 0029 (recorded in Phase 3).')

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

> README/CHANGELOG prose + the ADR supersede records land in Phase 3 (Task 18) once the new package names are final.

---

## Phase 1 — `httpcore` shared root (sequential within the package)

> These tasks all land in `transport/http/httpcore` (package `httpcore`). They share files' package, so run them **in order** (not parallel) to avoid edit conflicts. Each is an independent TDD unit.

### Task 2: The generic seam — `CustomizeConfig`, options, `RouteCustomizer`, `MountGroups`

**Files:**
- Create: `transport/http/httpcore/seam.go`
- Test: `transport/http/httpcore/seam_test.go`

**Interfaces:**
- Consumes: `github.com/zakyalvan/krtlwrkflw/engine` (for `engine.InstanceState`), `log/slog`, `go.opentelemetry.io/otel/trace`, `go.opentelemetry.io/otel/metric`.
- Produces:
  ```go
  type CustomizeConfig[R any] struct {
      BasePath       string
      Wrap           func(R) R
      InstanceMapper func(engine.InstanceState) any
      Logger         *slog.Logger
      TracerProvider trace.TracerProvider
      MeterProvider  metric.MeterProvider
  }
  type CustomizeOption[R any] func(*CustomizeConfig[R])
  func ResolveConfig[R any](opts ...CustomizeOption[R]) CustomizeConfig[R] // Wrap defaults to identity; InstanceMapper defaults to NewInstanceView; Logger defaults to slog.Default()
  func WithBasePath[R any](p string) CustomizeOption[R]
  func WithInstanceMapper[R any](fn func(engine.InstanceState) any) CustomizeOption[R]
  func WithRouterFunc[R any](fn func(R) R) CustomizeOption[R]        // composes onto Wrap (outer wraps inner)
  func WithLogger[R any](l *slog.Logger) CustomizeOption[R]
  func WithTracerProvider[R any](tp trace.TracerProvider) CustomizeOption[R]
  func WithMeterProvider[R any](mp metric.MeterProvider) CustomizeOption[R]
  type RouteCustomizer[R any] interface { Customize(r R, opts ...CustomizeOption[R]) }
  func MountGroups[R any](r R, groups ...RouteCustomizer[R])
  ```

- [ ] **Step 1: Write the failing test.**

```go
package httpcore_test

import (
	"testing"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/transport/http/httpcore"
)

func TestResolveConfigDefaults(t *testing.T) {
	cfg := httpcore.ResolveConfig[int]() // R=int is a stand-in router type for the seam test
	if cfg.Wrap == nil {
		t.Fatal("Wrap must default to a non-nil identity")
	}
	if got := cfg.Wrap(7); got != 7 {
		t.Fatalf("default Wrap must be identity, got %d", got)
	}
	if cfg.InstanceMapper == nil {
		t.Fatal("InstanceMapper must default to non-nil")
	}
	if cfg.Logger == nil {
		t.Fatal("Logger must default to slog.Default()")
	}
}

func TestOptionsCompose(t *testing.T) {
	inner := func(x int) int { return x + 1 }
	outer := func(x int) int { return x * 2 }
	cfg := httpcore.ResolveConfig(
		httpcore.WithBasePath[int]("/api"),
		httpcore.WithRouterFunc(inner),
		httpcore.WithRouterFunc(outer), // composes: later wraps earlier
	)
	if cfg.BasePath != "/api" {
		t.Fatalf("BasePath=%q", cfg.BasePath)
	}
	// outer(inner(3)) or inner(outer(3)); assert deterministic composition order.
	if got := cfg.Wrap(3); got != outer(inner(3)) {
		t.Fatalf("Wrap composition = %d, want %d", got, outer(inner(3)))
	}
}

type recordCustomizer struct{ hits *int }

func (c recordCustomizer) Customize(r int, _ ...httpcore.CustomizeOption[int]) { *c.hits++ }

func TestMountGroupsInvokesEach(t *testing.T) {
	hits := 0
	httpcore.MountGroups(0, recordCustomizer{&hits}, recordCustomizer{&hits})
	if hits != 2 {
		t.Fatalf("MountGroups invoked %d customizers, want 2", hits)
	}
}

func TestWithInstanceMapperOverrides(t *testing.T) {
	cfg := httpcore.ResolveConfig(httpcore.WithInstanceMapper[int](func(engine.InstanceState) any { return "x" }))
	if cfg.InstanceMapper(engine.InstanceState{}) != "x" {
		t.Fatal("WithInstanceMapper not applied")
	}
}
```

- [ ] **Step 2: Run — expect FAIL (package/symbols undefined).**

```bash
go test ./transport/http/httpcore/... 2>&1 | head -20
```

Expected: build failure `package .../httpcore is not in std` / `undefined: httpcore.ResolveConfig`.

- [ ] **Step 3: Implement `seam.go`.**

```go
// Package httpcore holds the transport-neutral core shared by the stdlib, gin,
// and fiber HTTP adapter subpackages: pure per-endpoint logic, DTOs, error
// classification, the instance view, health-probe evaluation, observability
// recording, and the generic RouteCustomizer seam.
package httpcore

import (
	"log/slog"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/zakyalvan/krtlwrkflw/engine"
)

// CustomizeConfig carries per-mount configuration for a route group. R is the
// framework router type (*http.ServeMux, gin.IRouter, fiber.Router). The struct
// is exported so consumers may author their own CustomizeOption[R].
type CustomizeConfig[R any] struct {
	// BasePath prefixes every route the group registers. Under stdlib it is the
	// only way to sub-path a group; gin/fiber may use native groups instead.
	BasePath string
	// Wrap transforms the router before the group registers onto it — the vehicle
	// for framework-native middleware/subrouters. Defaults to identity.
	Wrap func(R) R
	// InstanceMapper customises the process-instance response shape. nil-safe:
	// ResolveConfig defaults it to NewInstanceView.
	InstanceMapper func(engine.InstanceState) any
	// Logger receives 5xx raw error details (never sent to clients).
	Logger         *slog.Logger
	TracerProvider trace.TracerProvider
	MeterProvider  metric.MeterProvider
}

// CustomizeOption mutates a CustomizeConfig[R].
type CustomizeOption[R any] func(*CustomizeConfig[R])

// ResolveConfig applies opts over safe defaults.
func ResolveConfig[R any](opts ...CustomizeOption[R]) CustomizeConfig[R] {
	cfg := CustomizeConfig[R]{
		Wrap:           func(r R) R { return r },
		InstanceMapper: func(st engine.InstanceState) any { return NewInstanceView(st) },
		Logger:         slog.Default(),
	}
	for _, o := range opts {
		if o != nil {
			o(&cfg)
		}
	}
	if cfg.Wrap == nil {
		cfg.Wrap = func(r R) R { return r }
	}
	if cfg.InstanceMapper == nil {
		cfg.InstanceMapper = func(st engine.InstanceState) any { return NewInstanceView(st) }
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return cfg
}

// WithBasePath prefixes every route the group registers (e.g. "/api/v1/workflow").
func WithBasePath[R any](p string) CustomizeOption[R] {
	return func(c *CustomizeConfig[R]) { c.BasePath = p }
}

// WithInstanceMapper overrides the process-instance response shape.
func WithInstanceMapper[R any](fn func(engine.InstanceState) any) CustomizeOption[R] {
	return func(c *CustomizeConfig[R]) { c.InstanceMapper = fn }
}

// WithRouterFunc composes fn onto Wrap; fn runs outermost (fn(previous(r))).
func WithRouterFunc[R any](fn func(R) R) CustomizeOption[R] {
	return func(c *CustomizeConfig[R]) {
		prev := c.Wrap
		if prev == nil {
			c.Wrap = fn
			return
		}
		c.Wrap = func(r R) R { return fn(prev(r)) }
	}
}

// WithLogger sets the logger used for 5xx raw-error logging.
func WithLogger[R any](l *slog.Logger) CustomizeOption[R] {
	return func(c *CustomizeConfig[R]) { c.Logger = l }
}

// WithTracerProvider sets the OTel tracer provider for per-route spans.
func WithTracerProvider[R any](tp trace.TracerProvider) CustomizeOption[R] {
	return func(c *CustomizeConfig[R]) { c.TracerProvider = tp }
}

// WithMeterProvider sets the OTel meter provider for per-route metrics.
func WithMeterProvider[R any](mp metric.MeterProvider) CustomizeOption[R] {
	return func(c *CustomizeConfig[R]) { c.MeterProvider = mp }
}

// RouteCustomizer is a mountable route group for router type R.
type RouteCustomizer[R any] interface {
	Customize(r R, opts ...CustomizeOption[R])
}

// MountGroups mounts each group onto r at its current position (no extra opts).
// It is also the consumer extension seam: any RouteCustomizer[R] — including a
// consumer's own — can be passed. Groups needing distinct base paths or
// middleware call Customize directly with the relevant options.
func MountGroups[R any](r R, groups ...RouteCustomizer[R]) {
	for _, g := range groups {
		g.Customize(r)
	}
}
```

> `NewInstanceView` is defined in Task 6; this task will not compile until Task 6 lands. If executing strictly per-task, add a temporary `func NewInstanceView(engine.InstanceState) any { return st }` stub in `view.go` now and flesh it out in Task 6 — the seam test does not exercise the mapper's shape.

- [ ] **Step 4: Run — expect PASS.**

```bash
go test ./transport/http/httpcore/... -run 'TestResolveConfig|TestOptions|TestMountGroups|TestWithInstanceMapper' -v 2>&1 | tail -20
```

- [ ] **Step 5: Commit.**

```bash
git add transport/http/httpcore/seam.go transport/http/httpcore/seam_test.go transport/http/httpcore/view.go
git commit -m "feat(transport): httpcore generic RouteCustomizer seam + CustomizeOption

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task 3: Error classification + 500-leak fix

**Files:**
- Create: `transport/http/httpcore/errors.go`, `transport/http/httpcore/errors_test.go`

**Interfaces:**
- Consumes: existing sentinels — `kernel.ErrInstanceNotFound`, `kernel.ErrDefinitionNotFound`, `humantask.ErrTaskNotFound`, `authz.ErrNotAuthorized`, `kernel.ErrConcurrentUpdate`, `kernel.ErrBadCursor`, `service.ErrConflict`, `engine.ErrInvalidTransition` (see `transport/rest/errors.go` for the exact set).
- Produces:
  ```go
  var ErrBadInput error // "workflow-httpcore: bad input"
  type ErrorBody struct { Error string `json:"error"`; Message string `json:"message,omitempty"` }
  // ClassifyError returns the HTTP status and the CLIENT-SAFE body. For status>=500
  // the returned ErrorBody.Message is empty (raw text is caller-logged, never sent).
  func ClassifyError(err error) (status int, body ErrorBody)
  ```

- [ ] **Step 1: Write the failing test** (table-driven, assert-closure form per `table-test` skill).

```go
package httpcore_test

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/transport/http/httpcore"
)

func TestClassifyError(t *testing.T) {
	tests := map[string]struct {
		err    error
		assert func(t *testing.T, status int, body httpcore.ErrorBody)
	}{
		"not found": {
			err: fmt.Errorf("wrap: %w", kernel.ErrInstanceNotFound),
			assert: func(t *testing.T, status int, body httpcore.ErrorBody) {
				if status != http.StatusNotFound || body.Error != "not_found" {
					t.Fatalf("got %d/%q", status, body.Error)
				}
			},
		},
		"forbidden": {
			err: authz.ErrNotAuthorized,
			assert: func(t *testing.T, status int, body httpcore.ErrorBody) {
				if status != http.StatusForbidden || body.Error != "forbidden" {
					t.Fatalf("got %d/%q", status, body.Error)
				}
			},
		},
		"bad input keeps message": {
			err: fmt.Errorf("%w: def_ref required", httpcore.ErrBadInput),
			assert: func(t *testing.T, status int, body httpcore.ErrorBody) {
				if status != http.StatusBadRequest || body.Message == "" {
					t.Fatalf("4xx must keep message; got %d/%q", status, body.Message)
				}
			},
		},
		"internal hides message": {
			err: errors.New("pgx: connection refused at 10.0.0.5:5432"),
			assert: func(t *testing.T, status int, body httpcore.ErrorBody) {
				if status != http.StatusInternalServerError {
					t.Fatalf("status=%d", status)
				}
				if body.Error != "internal_error" || body.Message != "" {
					t.Fatalf("5xx must not leak: error=%q message=%q", body.Error, body.Message)
				}
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			status, body := httpcore.ClassifyError(tc.err)
			tc.assert(t, status, body)
		})
	}
}
```

- [ ] **Step 2: Run — expect FAIL** (`undefined: httpcore.ClassifyError`).

```bash
go test ./transport/http/httpcore/... -run TestClassifyError 2>&1 | head -15
```

- [ ] **Step 3: Implement `errors.go`** — port `classifyError` from `transport/rest/errors.go`, but split the message: keep a descriptive `Message` for 4xx sentinels, and for the `default` (5xx) branch return `ErrorBody{Error: "internal_error"}` with **empty** Message.

```go
package httpcore

import (
	"errors"
	"net/http"

	"github.com/zakyalvan/krtlwrkflw/authz"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/humantask"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
	"github.com/zakyalvan/krtlwrkflw/service"
)

// ErrBadInput is the sentinel for 400-class decode/validation errors.
var ErrBadInput = errors.New("workflow-httpcore: bad input")

// ErrorBody is the JSON error envelope. Message is omitted for 5xx responses.
type ErrorBody struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

// ClassifyError maps err to an HTTP status and a CLIENT-SAFE body. For 5xx the
// Message is empty; callers log the raw error instead of exposing it.
func ClassifyError(err error) (int, ErrorBody) {
	switch {
	case errors.Is(err, kernel.ErrInstanceNotFound),
		errors.Is(err, kernel.ErrDefinitionNotFound),
		errors.Is(err, humantask.ErrTaskNotFound):
		return http.StatusNotFound, ErrorBody{Error: "not_found", Message: err.Error()}
	case errors.Is(err, authz.ErrNotAuthorized):
		return http.StatusForbidden, ErrorBody{Error: "forbidden", Message: err.Error()}
	case errors.Is(err, kernel.ErrConcurrentUpdate):
		return http.StatusConflict, ErrorBody{Error: "conflict", Message: err.Error()}
	case errors.Is(err, kernel.ErrBadCursor), errors.Is(err, ErrBadInput):
		return http.StatusBadRequest, ErrorBody{Error: "bad_request", Message: err.Error()}
	case errors.Is(err, service.ErrConflict), errors.Is(err, engine.ErrInvalidTransition):
		return http.StatusUnprocessableEntity, ErrorBody{Error: "conflict_state", Message: err.Error()}
	default:
		return http.StatusInternalServerError, ErrorBody{Error: "internal_error"}
	}
}
```

- [ ] **Step 4: Run — expect PASS.**

```bash
go test ./transport/http/httpcore/... -run TestClassifyError -v 2>&1 | tail -15
```

- [ ] **Step 5: Commit.**

```bash
git add transport/http/httpcore/errors.go transport/http/httpcore/errors_test.go
git commit -m "feat(transport): httpcore ClassifyError with 5xx message redaction

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task 4: Request DTOs

**Files:**
- Create: `transport/http/httpcore/dto.go`, `transport/http/httpcore/dto_test.go`

**Interfaces:**
- Produces (JSON tags must match today's `transport/rest` bodies exactly — see `handler.go` reqBody structs):
  ```go
  type Actor struct { ID string `json:"id"`; Roles []string `json:"roles"` }
  type StartInput struct { DefRef string `json:"def_ref"`; InstanceID string `json:"instance_id"`; Vars map[string]any `json:"vars"` }
  type SignalInput struct { Signal string `json:"signal"`; Payload map[string]any `json:"payload"` }
  type MessageInput struct { DefRef string `json:"def_ref"`; Name string `json:"name"`; CorrelationKey string `json:"correlation_key"`; Payload map[string]any `json:"payload"` }
  type ClaimInput struct { Actor Actor `json:"actor"` }
  type CompleteInput struct { Actor Actor `json:"actor"`; Output map[string]any `json:"output"` }
  type ReassignInput struct { From string `json:"from"`; To string `json:"to"`; By Actor `json:"by"` }
  // Admin DTOs (mirror transport/rest/admin.go bodies): PolicyRuleInput, RoleBindingInput, RedriveInput, ListInstancesQuery (cursor/limit/status filters), etc.
  ```

- [ ] **Step 1: Write the failing test** — round-trip JSON for one representative DTO to lock the wire tags.

```go
package httpcore_test

import (
	"encoding/json"
	"testing"

	"github.com/zakyalvan/krtlwrkflw/transport/http/httpcore"
)

func TestStartInputJSONTags(t *testing.T) {
	const in = `{"def_ref":"order","instance_id":"o-1","vars":{"amount":42}}`
	var got httpcore.StartInput
	if err := json.Unmarshal([]byte(in), &got); err != nil {
		t.Fatal(err)
	}
	if got.DefRef != "order" || got.InstanceID != "o-1" || got.Vars["amount"].(float64) != 42 {
		t.Fatalf("wire tags mismatch: %+v", got)
	}
}
```

- [ ] **Step 2: Run — expect FAIL.**

```bash
go test ./transport/http/httpcore/... -run TestStartInputJSONTags 2>&1 | head -10
```

- [ ] **Step 3: Implement `dto.go`** with the structs above (copy the exact reqBody shapes + admin body shapes from `transport/rest/handler.go` and `transport/rest/admin.go`).

- [ ] **Step 4: Run — expect PASS.** `go test ./transport/http/httpcore/... -run TestStartInputJSONTags -v`

- [ ] **Step 5: Commit.**

```bash
git add transport/http/httpcore/dto.go transport/http/httpcore/dto_test.go
git commit -m "feat(transport): httpcore request DTOs (wire-compatible)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task 5: Pure service endpoint functions

**Files:**
- Create: `transport/http/httpcore/endpoints.go`, `transport/http/httpcore/endpoints_test.go`

**Interfaces:**
- Consumes: `service.Service`, DTOs (Task 4), `NewInstanceView` (Task 6), `ErrBadInput` (Task 3).
- Produces (each returns `(status int, body any, err error)`; on `err != nil` status/body are zero — the caller writes the classified error; `body == nil` means "status only, no JSON"; `mapper` may be nil → funcs fall back to `NewInstanceView`):
  ```go
  func StartInstance(ctx context.Context, svc service.Service, in StartInput, mapper func(engine.InstanceState) any) (int, any, error)
  func GetInstance(ctx context.Context, svc service.Service, id string, mapper func(engine.InstanceState) any) (int, any, error)
  func GetInstanceSnapshot(ctx context.Context, svc service.Service, id string) (int, any, error)
  func GetActionableView(ctx context.Context, svc service.Service, id string) (int, any, error)
  func DeliverSignal(ctx context.Context, svc service.Service, id string, in SignalInput, mapper func(engine.InstanceState) any) (int, any, error)
  func DeliverMessage(ctx context.Context, svc service.Service, in MessageInput) (int, any, error) // (202, nil, nil) on success
  func ClaimTask(ctx context.Context, svc service.Service, token string, in ClaimInput, mapper func(engine.InstanceState) any) (int, any, error)
  func CompleteTask(ctx context.Context, svc service.Service, token string, in CompleteInput, mapper func(engine.InstanceState) any) (int, any, error)
  func ReassignTask(ctx context.Context, svc service.Service, token string, in ReassignInput, mapper func(engine.InstanceState) any) (int, any, error)
  ```

**Source of truth:** the bodies of `transport/rest/handler.go` `handle*` methods. Extract the service-call + mapping (drop the decode/write, which move to adapters). Validation that today returns `ErrBadInput` (e.g. "def_ref and instance_id are required") moves INTO these funcs so every framework enforces it identically.

- [ ] **Step 1: Write the failing test** — table-driven against a mock `service.Service` (generate via `use-mockgen` if not present: `mockgen` the `service.Service` interface into `service/mock_service_test.go`... place per `use-mockgen`). Assert-closure form.

```go
package httpcore_test

import (
	"net/http"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/service"
	svcmock "github.com/zakyalvan/krtlwrkflw/service/mock" // per use-mockgen placement
	"github.com/zakyalvan/krtlwrkflw/transport/http/httpcore"
)

func TestStartInstance(t *testing.T) {
	tests := map[string]struct {
		in     httpcore.StartInput
		setup  func(m *svcmock.MockService)
		assert func(t *testing.T, status int, body any, err error)
	}{
		"missing fields → ErrBadInput, no service call": {
			in:    httpcore.StartInput{DefRef: ""},
			setup: func(m *svcmock.MockService) {}, // no EXPECT: must not call svc
			assert: func(t *testing.T, status int, body any, err error) {
				if err == nil {
					t.Fatal("want ErrBadInput")
				}
			},
		},
		"success → 201 mapped body": {
			in: httpcore.StartInput{DefRef: "order", InstanceID: "o-1"},
			setup: func(m *svcmock.MockService) {
				m.EXPECT().StartInstance(gomock.Any(), gomock.Any()).Return(engine.InstanceState{ /* minimal */ }, nil)
			},
			assert: func(t *testing.T, status int, body any, err error) {
				if err != nil || status != http.StatusCreated || body == nil {
					t.Fatalf("got %d body=%v err=%v", status, body, err)
				}
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			m := svcmock.NewMockService(ctrl)
			tc.setup(m)
			status, body, err := httpcore.StartInstance(t.Context(), m, tc.in, nil)
			tc.assert(t, status, body, err)
		})
	}
}
```

- [ ] **Step 2: Run — expect FAIL** (`undefined: httpcore.StartInstance`).

```bash
go test ./transport/http/httpcore/... -run TestStartInstance 2>&1 | head -20
```

- [ ] **Step 3: Implement `endpoints.go`.** Representative:

```go
func StartInstance(ctx context.Context, svc service.Service, in StartInput, mapper func(engine.InstanceState) any) (int, any, error) {
	if in.DefRef == "" || in.InstanceID == "" {
		return 0, nil, fmt.Errorf("%w: def_ref and instance_id are required", ErrBadInput)
	}
	st, err := svc.StartInstance(ctx, service.StartInstanceRequest{DefRef: in.DefRef, InstanceID: in.InstanceID, Vars: in.Vars})
	if err != nil {
		return 0, nil, err
	}
	return http.StatusCreated, mapInstance(mapper, st), nil
}

// mapInstance applies mapper, defaulting to NewInstanceView.
func mapInstance(mapper func(engine.InstanceState) any, st engine.InstanceState) any {
	if mapper == nil {
		return NewInstanceView(st)
	}
	return mapper(st)
}
```

Implement the remaining funcs by relocating the corresponding `transport/rest/handler.go` handler bodies (GetInstance→200, DeliverSignal→200, DeliverMessage→202/nil, ClaimTask/CompleteTask/ReassignTask→200, GetInstanceSnapshot/GetActionableView→200 via the `transport/rest/snapshot.go` logic). Each returns `(0, nil, err)` on service error.

- [ ] **Step 4: Run — expect PASS** (extend the test table to cover every func before moving on; target ≥85% of `endpoints.go`).

```bash
go test ./transport/http/httpcore/... -run 'TestStartInstance|TestGetInstance|TestDeliver|TestClaim|TestComplete|TestReassign|TestSnapshot|TestActionable' -v 2>&1 | tail -25
```

- [ ] **Step 5: Commit.**

```bash
git add transport/http/httpcore/endpoints.go transport/http/httpcore/endpoints_test.go service/mock*
git commit -m "feat(transport): httpcore pure service endpoint functions

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task 6: Instance view mapper

**Files:**
- Move: `transport/rest/view.go` → `transport/http/httpcore/view.go`; `transport/rest/view_test.go` → `transport/http/httpcore/view_test.go`

**Interfaces:**
- Produces: `func NewInstanceView(st engine.InstanceState) any` (+ the view struct) — identical shape/JSON tags to today's `transport/rest` default view. (If Task 2 added a stub, replace it here.)

- [ ] **Step 1:** Copy `view_test.go` into the package as `package httpcore_test`; adjust imports to `httpcore`. Run — expect FAIL (undefined `NewInstanceView` or stub returns wrong shape).
- [ ] **Step 2:** Move `view.go` content into `httpcore/view.go` (package `httpcore`), keeping the exported `NewInstanceView` + view type verbatim.
- [ ] **Step 3:** Run — expect PASS. `go test ./transport/http/httpcore/... -run TestInstanceView -v`
- [ ] **Step 4: Commit.**

```bash
git add transport/http/httpcore/view.go transport/http/httpcore/view_test.go
git commit -m "refactor(transport): relocate instance view mapper into httpcore

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task 7: Admin endpoint functions + admin DTOs

**Files:**
- Create: `transport/http/httpcore/admin_endpoints.go`, `transport/http/httpcore/admin_endpoints_test.go`

**Interfaces:**
- Consumes: `service.Service` + the optional admin sub-interfaces `service.DeadLetterAdmin`, `service.PolicyAdmin`, `service.RelayStatsAdmin`, `service.TimerAdmin`, `service.LineageAdmin` (unchanged), admin DTOs (Task 4).
- Produces pure funcs mirroring `transport/rest/admin.go` handlers, each `(status int, body any, err error)`:
  ```go
  func AdminListInstances(ctx context.Context, svc service.Service, q ListInstancesQuery) (int, any, error)
  func ResolveIncident(ctx context.Context, svc service.Service, instanceID, incidentID string) (int, any, error)
  func CancelInstance(ctx context.Context, svc service.Service, instanceID string) (int, any, error)
  func ListDeadLetters(ctx context.Context, a service.DeadLetterAdmin, q DeadLetterQuery) (int, any, error)
  func RedriveDeadLetters(ctx context.Context, a service.DeadLetterAdmin, in RedriveInput) (int, any, error)
  func ListPolicies(ctx context.Context, a service.PolicyAdmin) (int, any, error)
  func AddPolicy(ctx context.Context, a service.PolicyAdmin, in PolicyRuleInput) (int, any, error)
  func RemovePolicy(ctx context.Context, a service.PolicyAdmin, in PolicyRuleInput) (int, any, error)
  func ListRoleBindings(ctx context.Context, a service.PolicyAdmin) (int, any, error)
  func AddRoleBinding(ctx context.Context, a service.PolicyAdmin, in RoleBindingInput) (int, any, error)
  func RemoveRoleBinding(ctx context.Context, a service.PolicyAdmin, in RoleBindingInput) (int, any, error)
  func AdminRelayStats(ctx context.Context, a service.RelayStatsAdmin) (int, any, error)
  func AdminTimers(ctx context.Context, a service.TimerAdmin) (int, any, error)
  func AdminInstanceLineage(ctx context.Context, a service.LineageAdmin, instanceID string) (int, any, error)
  ```

- [ ] **Step 1: Write the failing test** — table-driven against mocks for `service.Service` + one admin sub-interface (e.g. `PolicyAdmin`). Cover: AddPolicy success (200/201), ResolveIncident success, CancelInstance success. Assert-closure form, `t.Context()`.
- [ ] **Step 2: Run — expect FAIL.** `go test ./transport/http/httpcore/... -run TestAdmin 2>&1 | head -20`
- [ ] **Step 3: Implement** by relocating `transport/rest/admin.go` handler bodies (drop decode/write). Keyset pagination cursor parsing that today lives in the handler moves into `ListInstancesQuery` construction at the adapter (adapter parses query params → `ListInstancesQuery`; func consumes it).
- [ ] **Step 4: Run — expect PASS** (extend table to all funcs; ≥85%).
- [ ] **Step 5: Commit.**

```bash
git add transport/http/httpcore/admin_endpoints.go transport/http/httpcore/admin_endpoints_test.go transport/http/httpcore/dto.go
git commit -m "feat(transport): httpcore admin endpoint functions

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task 8: Health-probe evaluation

**Files:**
- Create: `transport/http/httpcore/health.go`, `transport/http/httpcore/health_test.go` (port `HealthCheck`/`HealthCheckFunc` from `transport/rest/health.go`).

**Interfaces:**
- Produces:
  ```go
  type HealthCheck interface { Name() string; Check(ctx context.Context) error }
  func HealthCheckFunc(name string, fn func(ctx context.Context) error) HealthCheck
  // EvaluateReady runs all checks; returns 200 + {"status":"ok","checks":{...}} when all pass,
  // else 503 + {"status":"unavailable","checks":{name:error}}. EvaluateLive returns a static 200.
  func EvaluateReady(ctx context.Context, checks []HealthCheck) (int, any)
  func EvaluateLive(ctx context.Context) (int, any)
  ```

- [ ] **Step 1: Write the failing test** — one passing check → (200, status ok); one failing check → (503, includes the check name). Table-driven.
- [ ] **Step 2: Run — expect FAIL.**
- [ ] **Step 3: Implement** — relocate the `HealthCheck`/`HealthCheckFunc` types verbatim; extract the probe loop from `transport/rest/health.go` `NewHealthHandler` into `EvaluateReady`; `EvaluateLive` returns `(200, map[string]string{"status":"ok"})` (the `/healthz` liveness shape).
- [ ] **Step 4: Run — expect PASS.**
- [ ] **Step 5: Commit.**

```bash
git add transport/http/httpcore/health.go transport/http/httpcore/health_test.go
git commit -m "feat(transport): httpcore health-probe evaluation

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task 9: Observability recording

**Files:**
- Create: `transport/http/httpcore/observability.go`, `transport/http/httpcore/observability_test.go`

**Interfaces:**
- Consumes: `internal/observability` (existing `observability.New`), OTel providers from `CustomizeConfig`.
- Produces:
  ```go
  type Instrumentation struct { /* holds tracer, counter, histogram, propagator */ }
  // NewInstrumentation builds meters/tracer from the config providers (nil → OTel globals).
  func NewInstrumentation[R any](cfg CustomizeConfig[R]) *Instrumentation
  // Observe wraps one request: extracts trace context from header carrier, starts the span
  // "wrkflw.rest <METHOD> <routeTemplate>", records duration+count with http.route=routeTemplate,
  // http.method, http.status_code. routeTemplate is STATIC (no r.Pattern dependency).
  func (i *Instrumentation) Observe(ctx context.Context, method, routeTemplate string, hdr http.Header, run func(context.Context) (status int))
  ```

- [ ] **Step 1: Write the failing test** — with a manual `metric.Reader` (`sdkmetric.NewManualReader`) + `WithMeterProvider`, call `Observe` for one request and assert `wrkflw_rest_requests_total` recorded with `http.route` = the static template passed in (never `"unmatched"`).
- [ ] **Step 2: Run — expect FAIL.**
- [ ] **Step 3: Implement** — reuse `internal/observability` for meter/tracer construction; port the metric names + span naming from `transport/rest/handler.go` `traceMiddleware`, but take `routeTemplate` as a parameter instead of reading `r.Pattern`. `run` returns the status the middleware records.
- [ ] **Step 4: Run — expect PASS.**
- [ ] **Step 5: Commit.**

```bash
git add transport/http/httpcore/observability.go transport/http/httpcore/observability_test.go
git commit -m "feat(transport): httpcore per-route observability (static template, no r.Pattern)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task 10: httpcore package gate

- [ ] **Step 1:** `go test -race -coverprofile=cover.out ./transport/http/httpcore/... && go tool cover -func=cover.out | tail -1` — expect ≥85%.
- [ ] **Step 2:** `golangci-lint run ./transport/http/httpcore/...` — expect clean.
- [ ] **Step 3:** No commit (verification only); if coverage <85%, add tests for the uncovered funcs and commit those.

---

## Phase 2 — framework adapters (run the three adapters in PARALLEL)

> Tasks 11 (stdlib), 12 (gin), 13 (fiber) touch **disjoint** package directories and all depend only on the finished `httpcore` (Phase 1). Dispatch them concurrently. Each adapter is one task with its own TDD cycle and ends independently testable. Shared route table (relative patterns) every adapter must register:
>
> | Group | Method | Relative Pattern | httpcore func | Decode |
> |---|---|---|---|---|
> | Instance | POST | `/instances` | `StartInstance` | body→`StartInput` |
> | Instance | GET | `/instances/{id}` | `GetInstance` | path `id` |
> | Instance | GET | `/instances/{id}/snapshot` | `GetInstanceSnapshot` | path `id` |
> | Instance | GET | `/instances/{id}/actionable` | `GetActionableView` | path `id` |
> | Instance | POST | `/instances/{id}/signals` | `DeliverSignal` | path `id` + body→`SignalInput` |
> | Message | POST | `/messages` | `DeliverMessage` | body→`MessageInput` |
> | Task | POST | `/tasks/{token}/claim` | `ClaimTask` | path `token` + body→`ClaimInput` |
> | Task | POST | `/tasks/{token}/complete` | `CompleteTask` | path `token` + body→`CompleteInput` |
> | Task | POST | `/tasks/{token}/reassign` | `ReassignTask` | path `token` + body→`ReassignInput` |
> | Admin | GET | `/admin/instances` | `AdminListInstances` | query→`ListInstancesQuery` |
> | Admin | POST | `/admin/instances/{id}/incidents/{incidentID}/resolve` | `ResolveIncident` | path `id`,`incidentID` |
> | Admin | POST | `/admin/instances/{id}/cancel` | `CancelInstance` | path `id` |
> | Admin (DeadLetters≠nil) | GET | `/admin/dead-letters` | `ListDeadLetters` | query |
> | Admin (DeadLetters≠nil) | POST | `/admin/dead-letters/redrive` | `RedriveDeadLetters` | body→`RedriveInput` |
> | Admin (Policies≠nil) | GET/POST/DELETE | `/admin/policies` | `ListPolicies`/`AddPolicy`/`RemovePolicy` | body→`PolicyRuleInput` |
> | Admin (Policies≠nil) | GET/POST/DELETE | `/admin/role-bindings` | `ListRoleBindings`/`AddRoleBinding`/`RemoveRoleBinding` | body→`RoleBindingInput` |
> | Admin (RelayStats≠nil) | GET | `/admin/relay-stats` | `AdminRelayStats` | — |
> | Admin (Timers≠nil) | GET | `/admin/timers` | `AdminTimers` | — |
> | Admin (Lineage≠nil) | GET | `/admin/instances/{id}/lineage` | `AdminInstanceLineage` | path `id` |
> | Health | GET | `/healthz` | `EvaluateLive` | — |
> | Health | GET | `/readyz` | `EvaluateReady` | — |
>
> Note the admin patterns above are prefixed `/admin/...` for parity with today. Because admin is default-ABSENT by composition, keep the `/admin` inside the relative pattern so `AdminRoutes{}.Customize(secureGroup)` yields `<secureGroup>/admin/...`. (A consumer wanting bare `/...` under their own `/admin` group can strip via `WithBasePath`.)

### Task 11: stdlib adapter (`transport/http/stdlib`)

**Files:**
- Create: `transport/http/stdlib/{write.go,observe.go,groups.go,options.go,mount.go}` + `*_test.go`.

**Interfaces:**
- Consumes: `httpcore.*` (all of Phase 1), `service.Service`.
- Produces:
  ```go
  // R = *http.ServeMux
  func WithBasePath(p string) httpcore.CustomizeOption[*http.ServeMux]     // = httpcore.WithBasePath[*http.ServeMux]
  type InstanceRoutes struct{ Svc service.Service }
  type TaskRoutes struct{ Svc service.Service }
  type MessageRoutes struct{ Svc service.Service }
  type AdminRoutes struct{ Svc service.Service; DeadLetters service.DeadLetterAdmin; Policies service.PolicyAdmin; RelayStats service.RelayStatsAdmin; Timers service.TimerAdmin; Lineage service.LineageAdmin }
  type HealthRoutes struct{ Checks []httpcore.HealthCheck }
  // all implement Customize(mux *http.ServeMux, opts ...httpcore.CustomizeOption[*http.ServeMux])
  func Mount(mux *http.ServeMux, svc service.Service, opts ...httpcore.CustomizeOption[*http.ServeMux]) // Instance+Task+Message
  func MountHealth(mux *http.ServeMux, checks ...httpcore.HealthCheck)
  ```

- [ ] **Step 1: Write the failing test** (black-box, `package stdlib_test`, `httptest`). Cover, table-driven: POST `/instances` 201; missing fields → 400 with message; GET `/instances/{id}` path param resolved; unknown id → 404; `WithBasePath("/api/v1/workflow")` shifts routes; admin absent until `AdminRoutes{}.Customize(mux)`; conditional admin route 404 when its dep is nil; `/readyz` 200/503; **5xx body carries no raw message**.

```go
func TestStdlibStartInstance(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc := svcmock.NewMockService(ctrl)
	svc.EXPECT().StartInstance(gomock.Any(), gomock.Any()).Return(engine.InstanceState{ /* … */ }, nil)

	mux := http.NewServeMux()
	stdlib.Mount(mux, svc)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/instances", strings.NewReader(`{"def_ref":"o","instance_id":"o-1"}`))
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body)
	}
}
```

- [ ] **Step 2: Run — expect FAIL** (`undefined: stdlib.Mount`). `go test ./transport/http/stdlib/... 2>&1 | head -20`
- [ ] **Step 3: Implement.** `write.go`: `writeJSON(w, status, v)` + `writeErr(cfg, w, r, err)` (classify; if status≥500 `cfg.Logger.ErrorContext(r.Context(), "rest: internal error", "err", err)`; write body). `observe.go`: wrap each registered handler with `httpcore.Instrumentation.Observe` passing the static template. `groups.go`: each group's `Customize` resolves cfg, registers its routes via a helper `handle(mux, cfg, method, pattern, tmpl, h)` that does `mux.HandleFunc(method+" "+cfg.BasePath+pattern, observed(h))`; handlers decode via `json.NewDecoder`/`r.PathValue`/`r.URL.Query()`, call the httpcore func, then `writeJSON`/`writeErr`. Representative:

```go
func (c InstanceRoutes) Customize(mux *http.ServeMux, opts ...httpcore.CustomizeOption[*http.ServeMux]) {
	cfg := httpcore.ResolveConfig(opts...)
	inst := httpcore.NewInstrumentation(cfg)
	base := cfg.BasePath
	mux.HandleFunc("POST "+base+"/instances", observe(inst, http.MethodPost, base+"/instances", func(w http.ResponseWriter, r *http.Request) {
		var in httpcore.StartInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeErr(cfg, w, r, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
			return
		}
		status, body, err := httpcore.StartInstance(r.Context(), c.Svc, in, cfg.InstanceMapper)
		if err != nil {
			writeErr(cfg, w, r, err)
			return
		}
		writeJSON(w, status, body)
	}))
	// … GET {id}, {id}/snapshot, {id}/actionable, POST {id}/signals per the route table …
}
```

Register admin conditional routes only when the corresponding `AdminRoutes` field is non-nil. `Mount` = `InstanceRoutes{svc}.Customize(mux,opts...)`, `TaskRoutes{svc}.Customize(...)`, `MessageRoutes{svc}.Customize(...)`. `MountHealth` = `HealthRoutes{checks}.Customize(mux)`. `stdlib.WithBasePath` aliases `httpcore.WithBasePath[*http.ServeMux]`.

- [ ] **Step 4: Run — expect PASS**; extend table to full parity set; `go test -race ./transport/http/stdlib/...`; coverage ≥85%.
- [ ] **Step 5: Commit.**

```bash
git add transport/http/stdlib/
git commit -m "feat(transport): stdlib net/http adapter (composable route groups)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task 12: gin adapter (`transport/http/gin`)

**Files:**
- Create: `transport/http/gin/{write.go,observe.go,groups.go,options.go,mount.go}` + `*_test.go`.
- Modify: `go.mod` (add `github.com/gin-gonic/gin`).

**Interfaces:**
- Consumes: `httpcore.*`, `service.Service`, `github.com/gin-gonic/gin`.
- Produces (R = `gin.IRouter`):
  ```go
  func WithBasePath(p string) httpcore.CustomizeOption[gin.IRouter]
  func WithMiddleware(mw ...gin.HandlerFunc) httpcore.CustomizeOption[gin.IRouter] // composes onto Wrap via r.Group("", mw...)
  type InstanceRoutes struct{ Svc service.Service } // + Task/Message/Admin/Health, same fields as stdlib
  func Mount(r gin.IRouter, svc service.Service, opts ...httpcore.CustomizeOption[gin.IRouter])
  func MountHealth(r gin.IRouter, checks ...httpcore.HealthCheck)
  ```

- [ ] **Step 1:** `go get github.com/gin-gonic/gin@latest`.
- [ ] **Step 2: Write the failing test** (black-box, `package gin_test`; spin `gin.New()`, `httptest`). Mirror the stdlib parity table: start 201, 400 on bad input, path params via `c.Param`, `WithBasePath` and native `Group` both work, `WithMiddleware(mw)` runs mw before the handler, admin absent-by-default, conditional admin 404, `/readyz`, 5xx no-leak.
- [ ] **Step 3: Run — expect FAIL.** `go test ./transport/http/gin/... 2>&1 | head -20`
- [ ] **Step 4: Implement.** `WithMiddleware`:

```go
func WithMiddleware(mw ...gin.HandlerFunc) httpcore.CustomizeOption[gin.IRouter] {
	return httpcore.WithRouterFunc(func(r gin.IRouter) gin.IRouter { return r.Group("", mw...) })
}
```

`groups.go` representative handler (native gin bind/write; translate `{id}`→`:id` in patterns via a `toColon` helper; path params via `c.Param`):

```go
func (c InstanceRoutes) Customize(r gin.IRouter, opts ...httpcore.CustomizeOption[gin.IRouter]) {
	cfg := httpcore.ResolveConfig(opts...)
	inst := httpcore.NewInstrumentation(cfg)
	rt := cfg.Wrap(r) // applies middleware/subrouter
	rt.POST(cfg.BasePath+"/instances", observe(inst, http.MethodPost, cfg.BasePath+"/instances", func(gc *gin.Context) {
		var in httpcore.StartInput
		if err := gc.ShouldBindJSON(&in); err != nil {
			writeErr(cfg, gc, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
			return
		}
		status, body, err := httpcore.StartInstance(gc.Request.Context(), c.Svc, in, cfg.InstanceMapper)
		if err != nil {
			writeErr(cfg, gc, err)
			return
		}
		gc.JSON(status, body)
	}))
	// … remaining Instance routes; {id} → :id …
}
```

`write.go`: `writeErr(cfg, gc, err)` classifies, logs 5xx via `cfg.Logger`, `gc.JSON(status, body)`. `observe.go`: gin middleware/closure wrapping the handler with `httpcore.Instrumentation.Observe`, status from `gc.Writer.Status()`. `Mount`/`MountHealth`/`WithBasePath` analogous to stdlib.

- [ ] **Step 5: Run — expect PASS**; `go test -race ./transport/http/gin/...`; ≥85%.
- [ ] **Step 6: Commit.**

```bash
git add transport/http/gin/ go.mod go.sum
git commit -m "feat(transport): gin adapter (composable route groups + WithMiddleware)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task 13: fiber v3 adapter (`transport/http/fiber`)

**Files:**
- Create: `transport/http/fiber/{write.go,observe.go,groups.go,options.go,mount.go}` + `*_test.go`.
- Modify: `go.mod` (add `github.com/gofiber/fiber/v3`).

**Interfaces:**
- Consumes: `httpcore.*`, `service.Service`, `github.com/gofiber/fiber/v3` (+ `.../middleware/adaptor` only if needed for header extraction).
- Produces (R = `fiber.Router`):
  ```go
  func WithBasePath(p string) httpcore.CustomizeOption[fiber.Router]
  func WithMiddleware(mw ...fiber.Handler) httpcore.CustomizeOption[fiber.Router] // composes onto Wrap via r.Group("", mw...)
  type InstanceRoutes struct{ Svc service.Service } // + Task/Message/Admin/Health
  func Mount(r fiber.Router, svc service.Service, opts ...httpcore.CustomizeOption[fiber.Router])
  func MountHealth(r fiber.Router, checks ...httpcore.HealthCheck)
  ```

- [ ] **Step 1:** `go get github.com/gofiber/fiber/v3@latest`.
- [ ] **Step 2: Write the failing test** (black-box, `package fiber_test`; `fiber.New()`, drive via `app.Test(httptest.NewRequest(...))`). Mirror the parity table.
- [ ] **Step 3: Run — expect FAIL.** `go test ./transport/http/fiber/... 2>&1 | head -20`
- [ ] **Step 4: Implement.** Native fiber handlers: bind via `c.Bind().JSON(&in)` (fiber v3), path params via `c.Params("id")`, write via `c.Status(status).JSON(body)`. `{id}`→`:id` translation. Body building calls the shared `httpcore` funcs. For trace-context extraction, build an `http.Header` from `c.GetReqHeaders()` and pass to `Observe`. `WithMiddleware`:

```go
func WithMiddleware(mw ...fiber.Handler) httpcore.CustomizeOption[fiber.Router] {
	return httpcore.WithRouterFunc(func(r fiber.Router) fiber.Router { return r.Group("", mw...) })
}
```

Representative handler:

```go
func (c InstanceRoutes) Customize(r fiber.Router, opts ...httpcore.CustomizeOption[fiber.Router]) {
	cfg := httpcore.ResolveConfig(opts...)
	inst := httpcore.NewInstrumentation(cfg)
	rt := cfg.Wrap(r)
	rt.Post(cfg.BasePath+"/instances", func(fc fiber.Ctx) error {
		var in httpcore.StartInput
		if err := fc.Bind().JSON(&in); err != nil {
			return writeErr(cfg, fc, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))
		}
		status, body, err := httpcore.StartInstance(fc.Context(), c.Svc, in, cfg.InstanceMapper)
		if err != nil {
			return writeErr(cfg, fc, err)
		}
		return fc.Status(status).JSON(body)
	})
	// … remaining Instance routes; {id} → :id …
}
```

`write.go`: `writeErr(cfg, fc, err) error` classifies, logs 5xx, returns `fc.Status(status).JSON(body)`. `observe.go`: wrap handlers timing + status (`fc.Response().StatusCode()`), call `httpcore.Instrumentation.Observe`.

- [ ] **Step 5: Run — expect PASS**; `go test -race ./transport/http/fiber/...`; ≥85%.
- [ ] **Step 6: Commit.**

```bash
git add transport/http/fiber/ go.mod go.sum
git commit -m "feat(transport): fiber v3 adapter (composable route groups + WithMiddleware)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Phase 3 — integration, docs, ADRs, final gate (sequential)

### Task 14: Cross-adapter parity test

**Files:**
- Create: `transport/http/parity_test.go` (package `parity_test`, imports all three adapters + a shared in-mem `service.Service`).

**Interfaces:**
- Consumes: `stdlib`, `gin`, `fiber` adapters; the `processtest`/in-mem stack or a mock `service.Service`.

- [ ] **Step 1: Write the failing test** — a single table of `(method, path, body)` cases run against all three adapters; assert identical status + identical JSON body across frameworks for each case (start, get, 400, 404, signal, message 202, task claim, readyz). Fails initially only if a divergence exists.

```go
func TestAdapterParity(t *testing.T) {
	cases := []struct{ method, path, body string; wantStatus int }{
		{http.MethodPost, "/instances", `{"def_ref":"o","instance_id":"o-1"}`, 201},
		{http.MethodPost, "/instances", `{}`, 400},
		{http.MethodGet, "/instances/missing", ``, 404},
		// …
	}
	for _, tc := range cases {
		stdlibResp := hitStdlib(t, tc)   // helpers construct a fresh mux/engine per adapter
		ginResp := hitGin(t, tc)
		fiberResp := hitFiber(t, tc)
		if stdlibResp.status != tc.wantStatus || ginResp.status != tc.wantStatus || fiberResp.status != tc.wantStatus {
			t.Fatalf("%s %s status: stdlib=%d gin=%d fiber=%d want=%d", tc.method, tc.path, stdlibResp.status, ginResp.status, fiberResp.status, tc.wantStatus)
		}
		if stdlibResp.body != ginResp.body || stdlibResp.body != fiberResp.body {
			t.Fatalf("%s %s body divergence:\n stdlib=%s\n gin=%s\n fiber=%s", tc.method, tc.path, stdlibResp.body, ginResp.body, fiberResp.body)
		}
	}
}
```

- [ ] **Step 2: Run.** `go test ./transport/http/ -run TestAdapterParity -v` — fix any real divergence found in the adapters (re-run their package tests after).
- [ ] **Step 3: Commit.**

```bash
git add transport/http/parity_test.go
git commit -m "test(transport): cross-adapter parity (stdlib/gin/fiber)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task 15: Delete `transport/rest`

- [ ] **Step 1:** Confirm nothing imports it: `grep -rn "transport/rest" --include=*.go . ; echo exit=$?` — must be only `examples/**` (fixed in Task 16) or empty.
- [ ] **Step 2:** `rm -rf transport/rest`.
- [ ] **Step 3:** `go build ./... 2>&1 | head` — expect failures only in `examples/**` (next task) or clean.
- [ ] **Step 4: Commit** (bundle with Task 16 if examples break the build; otherwise commit now).

```bash
git rm -r transport/rest
git commit -m "refactor(transport): remove transport/rest (superseded by transport/http)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task 16: Migrate `examples/` + confinement test

**Files:**
- Modify: any `examples/**` using `rest.NewHandler`/`NewHealthHandler` or grpc; `internal/authz/casbin/confinement_test.go` (add `./transport/http/...` if it positively enumerates transport packages).

- [ ] **Step 1:** `grep -rln "transport/rest\|transport/grpc\|NewHandler\|NewHealthHandler" examples/` — list files.
- [ ] **Step 2:** For each, replace with `mux := http.NewServeMux(); stdlib.Mount(mux, svc); stdlib.MountHealth(mux, checks...)`. Run the example's build/test.
- [ ] **Step 3:** `go build ./... && go test ./examples/... ./internal/authz/casbin/... 2>&1 | tail`.
- [ ] **Step 4: Commit.**

```bash
git add examples/ internal/authz/casbin/confinement_test.go
git commit -m "refactor(examples): migrate reference wiring to transport/http/stdlib

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task 17: ADR-0094 + ADR-0095

**Files:**
- Create: `docs/adr/0094-http-only-remove-grpc.md`, `docs/adr/0095-multiframework-mount-adapters.md`
- Modify: `docs/adr/0011-*.md`, `0051-*.md`, `0058-*.md`, `0062-*.md`, `0029-*.md` (add `> **Superseded by ADR-0094**` note to Status; for 0029 scope the note to the gRPC ResolveIncident RPC only).

- [ ] **Step 1:** Write both ADRs in the Nygard template (Status/Date, Context, Decision, Consequences). 0094 = HTTP-only + gRPC removal; 0095 = generic `RouteCustomizer[R]` + `CustomizeOption[R]` + three native adapters + admin-by-composition + observability/500-leak changes. Reference the spec.
- [ ] **Step 2:** Add supersede notes to the five prior ADRs.
- [ ] **Step 3: Commit.**

```bash
git add docs/adr/
git commit -m "docs(adr): 0094 HTTP-only, 0095 multi-framework adapters; supersede grpc ADRs

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task 18: README + CHANGELOG + doc.go

**Files:** `README.md`, `CHANGELOG.md`, `doc.go`.

- [ ] **Step 1:** README — remove the gRPC block (~269–283), the layout line (~494), and mentions (~8/19–20/235/1278); replace the transport section with the three-adapter `Mount`/`Customize` usage (stdlib/gin/fiber examples + the flexible base-path scenario from the spec). CHANGELOG — add a BREAKING entry (removed gRPC, removed `transport/rest`/`NewHandler`; new `transport/http/{httpcore,stdlib,gin,fiber}`). `doc.go` — finalize the transport prose.
- [ ] **Step 2:** `go build ./... && go test ./... 2>&1 | tail -5` (doc examples, if any, compile).
- [ ] **Step 3: Commit.**

```bash
git add README.md CHANGELOG.md doc.go
git commit -m "docs: HTTP-only transport (three adapters), remove gRPC references

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task 19: Final verification gate

- [ ] **Step 1:** `go build ./...` — clean.
- [ ] **Step 2:** `go test -race -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1` — all green; touched packages ≥85%.
- [ ] **Step 3:** `golangci-lint run ./...` — clean.
- [ ] **Step 4:** `grep -rn "transport/rest\|transport/grpc\|google.golang.org/grpc\|workflowpb\|r.Pattern" --include=*.go . ; echo exit=$?` — expect no matches (exit=1).
- [ ] **Step 5:** No commit (gate). If anything fails, fix under TDD and commit the fix.

---

## Self-Review

**Spec coverage:**
- gRPC removal → Task 1 (+ Task 17 supersede, Task 18 docs). ✓
- `httpcore` root (seam/DTOs/endpoints/errors/view/health/observability) → Tasks 2–10. ✓
- Generic `RouteCustomizer[R]` + `CustomizeOption[R]` + `MountGroups` + escape hatch → Task 2. ✓
- Three native adapters + `Mount`/`MountHealth`/`WithBasePath`/`WithMiddleware` → Tasks 11–13. ✓
- Composable groups, relative patterns, mount-at-any-base → Tasks 11–13 (+ base-path tests). ✓
- Admin default-absent by composition → Tasks 11–13 (admin conditional + absent-by-default tests). ✓
- Observability without `r.Pattern` → Task 9 + adapter `observe.go`. ✓
- 500-leak fix → Task 3 + adapter `writeErr` + no-leak tests. ✓
- `NewHandler`/`NewHealthHandler` removed → Task 15. ✓
- Parity → Task 14. Examples migrated → Task 16. ADRs → Task 17. Verification → Task 19. ✓

**Placeholder scan:** Route tables and interface blocks give exact patterns/signatures; representative full code shown per framework with the remaining endpoints enumerated by the route table (mechanical, not hand-wavy). No "TBD"/"add error handling"/"similar to Task N".

**Type consistency:** `CustomizeOption[R]`, `ResolveConfig`, `httpcore.StartInput`, endpoint func signatures `(int, any, error)`, group struct field names (`Svc`, `DeadLetters`, `Policies`, `RelayStats`, `Timers`, `Lineage`, `Checks`), and `Mount`/`MountHealth`/`WithBasePath`/`WithMiddleware` are used identically across Tasks 2–16.

**Known cross-task compile note:** Task 2's `seam.go` references `NewInstanceView` (Task 6). Mitigation documented in Task 2 (temporary stub). Under parallel execution the httpcore phase is sequential, so Task 6 lands before adapters consume the mapper.

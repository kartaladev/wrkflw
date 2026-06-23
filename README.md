# wrkflw

**An embeddable Go workflow engine — library, not a daemon.**

`wrkflw` is a single importable Go module (`github.com/zakyalvan/krtlwrkflw`) that a
consumer embeds in their own application. There is no owned binary, no daemon, and no
container you run. Process execution, human tasks, timers, compensation, and
authorization all live in the library's root packages. The REST and gRPC transport
adapters are mountable handlers a consumer registers in their own server.

---

## What it is

- **Library-first.** The product is the module's public root packages — `engine/`,
  `model/`, `runtime/`, etc. A consumer imports them and embeds the engine in their app.
  Every feature must be reachable through the public API; no feature lives exclusively
  in a binary or example.
- **Mountable transports, no owned main.** REST (`http.Handler`) and gRPC
  (`grpc.ServiceRegistrar`) entry points are constructors the consumer calls and mounts
  in their own server. The `examples/` directory shows reference wiring but is not a
  shipped product.
- **Token-based execution.** Transitions between nodes are modeled as tokens. Each
  token carries process-instance variables that downstream nodes (e.g. exclusive
  gateways) read to make routing decisions.
- **BPMN-inspired, not BPMN-compatible.** The domain vocabulary — gateways, sequence
  flows, boundary events, compensation, error codes — is inspired by BPMN, but this
  is **not** a BPMN-compatible implementation and does not aim to load or round-trip
  arbitrary BPMN2 documents. Definitions are authored in Go or YAML; BPMN2 XML can be
  loaded but is not the preferred form.
- **Expression evaluation** via [`expr-lang/expr`](https://github.com/expr-lang/expr):
  gateway conditions, attribute predicates, timer durations.

---

## Requirements

| Requirement | Version |
|---|---|
| Go | 1.25 |
| PostgreSQL | 17 |
| Docker | any recent version — only needed to run the testcontainers-based integration tests |

---

## Install

```bash
go get github.com/zakyalvan/krtlwrkflw
```

---

## Quickstart

### Define a process (Go builder)

```go
package main

import (
	"fmt"
	"log"

	"github.com/zakyalvan/krtlwrkflw/model"
)

func main() {
	def, err := model.NewDefinition("order-fulfillment", 1).
		Add(model.NewStartEvent("start")).
		Add(model.NewServiceTask("charge", "charge-card",
			model.WithCompensation("refund-card"),
		)).
		Add(model.NewUserTask("approve", []string{"manager"})).
		Add(model.NewEndEvent("end")).
		Connect("start", "charge").
		Connect("charge", "approve").
		Connect("approve", "end").
		Build()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("defined %q v%d with %d nodes\n", def.ID, def.Version, len(def.Nodes))
}
```

### Author in YAML

```yaml
# order.yaml
id: order
version: 1
nodes:
  - id: s
    kind: startEvent
  - id: charge
    kind: serviceTask
    action: charge-card
    compensationAction: refund-card
  - id: e
    kind: endEvent
flows:
  - { id: f1, source: s, target: charge }
  - { id: f2, source: charge, target: e }
```

```go
data, _ := os.ReadFile("order.yaml")
def, err := model.ParseYAML(data)
```

### Run it

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/clock"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/model"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

func main() {
	ctx := context.Background()

	def, _ := model.NewDefinition("order", 1).
		Add(model.NewStartEvent("s")).
		Add(model.NewServiceTask("charge", "charge-card")).
		Add(model.NewEndEvent("e")).
		Connect("s", "charge").
		Connect("charge", "e").
		Build()

	cat := action.NewMapCatalog(map[string]action.ServiceAction{
		"charge-card": action.Func(func(_ context.Context, vars map[string]any) (map[string]any, error) {
			return map[string]any{"charged": true}, nil
		}),
	})

	r := runtime.NewRunner(cat, clock.System(), runtime.NewMemStore())

	state, err := r.Run(ctx, def, "order-001", map[string]any{"amount": 99.0})
	if err != nil {
		log.Fatal(err)
	}
	if state.Status == engine.StatusCompleted {
		fmt.Println("order completed:", state.Variables["charged"])
	}
}
```

For signal/message delivery use `r.Deliver(ctx, def, instanceID, trigger)`. See
`runtime/caching_store_example_test.go` for a park-then-resume pattern.

---

## Authoring forms

| Form | Function | Notes |
|---|---|---|
| Go builder | `model.NewDefinition(...).Add(...).Connect(...).Build()` | Preferred; compile-time safe |
| YAML | `model.ParseYAML(data)` / `model.LoadYAML(r)` | Human-readable; lowerCamelCase kind discriminator |
| BPMN2 XML | loadable | Not the preferred form; see `model` package for details |

---

## Package map

All packages live directly at the module root — no `pkg/` prefix.

| Package | Role |
|---|---|
| `model` | Process-definition types: nodes, gateways, sequence flows, `ProcessDefinition`. Pure data + validation; no I/O. |
| `engine` | Core token state machine. Pure of transport, storage, and event-bus specifics — depends on interfaces only. |
| `runtime` | Reference driver: wires the engine to persistence, scheduling, and actions; provides `Runner`, `CachingStore`, `MemStore`, snapshot DTOs. |
| `action` | Service-action catalog (`Catalog`, `ServiceAction`, `MapCatalog`, `Func` adapter). |
| `humantask` | Human-task model and ports that drive human work (claim, complete, reassign). |
| `authz` | Pluggable `Authorizer` abstraction: role, resource-privilege, and attribute-based rules. |
| `casbinauthz` | Casbin-backed `Authorizer` (baseline implementation). Wraps `*casbin.SyncedEnforcer`. |
| `transport` | REST `http.Handler` factory (`transport/rest.NewHandler`) and gRPC `ServiceRegistrar` registration (`transport/grpc.RegisterWorkflowServiceServer`). |
| `persistence` | Persistence façade over the SQL/PostgreSQL store. |
| `eventing` | Eventing façade for publishing domain events via the transactional outbox. |
| `scheduling` | Façade over the timer/SLA scheduler (gocron behind the abstraction). |
| `observability` | Metrics, traces, and `slog` wiring at the runtime boundary. |
| `clock` | `clock.Clock` time abstraction. Supply `clock.System()` in production; inject a fake in tests. |
| `service` | Application-layer `Service` façade consumed by transport adapters. |

Implementation details a consumer must not import live under `internal/`.

---

## Mounting transports

### REST

```go
import (
	"net/http"

	"github.com/zakyalvan/krtlwrkflw/service"
	"github.com/zakyalvan/krtlwrkflw/transport/rest"
)

// svc is a service.Service (constructed via service.New or wired manually).
var svc service.Service

mux := http.NewServeMux()
mux.Handle("/workflow/", http.StripPrefix("/workflow", rest.NewHandler(svc)))
```

`rest.NewHandler` accepts functional options: `rest.WithAdminMiddleware(mw)` to protect
admin routes, `rest.WithDeadLetterAdmin(dla)`, `rest.WithPolicyAdmin(pa)`.

### gRPC

```go
import (
	"google.golang.org/grpc"

	"github.com/zakyalvan/krtlwrkflw/service"
	grpctransport "github.com/zakyalvan/krtlwrkflw/transport/grpc"
)

// srv is a *grpc.Server; svc is a service.Service.
var srv *grpc.Server
var svc service.Service

grpctransport.RegisterWorkflowServiceServer(srv, svc)
```

---

## Reading instance state

The `runtime` package provides two JSON-serializable projections built from an
`engine.InstanceState`:

| Type | Constructor | Purpose |
|---|---|---|
| `runtime.InstanceSnapshot` | `runtime.NewInstanceSnapshot(state, def)` | Full snapshot: tokens, variables, history, tasks, incidents. Omits engine bookkeeping. |
| `runtime.ActionableView` | `runtime.NewActionableView(state, def)` | Curated view: only open human tasks + allowed next actions (outgoing flows). |

The REST handler exposes these via:

- `GET /instances/{id}/snapshot` — returns `InstanceSnapshot` JSON.
- `GET /instances/{id}/actionable` — returns `ActionableView` JSON.

Front-ends can use the snapshot to render process history and the actionable view to
drive human-task completion UIs.

---

## Authorization

`authz.Authorizer` is the single interface the engine evaluates at human-task nodes.
Implement it to integrate any authorization backend.

```go
type Authorizer interface {
    Authorize(ctx context.Context, actor Actor, spec AuthzSpec, vars map[string]any) error
}
```

`AuthzSpec` supports:
- **Role-based:** any-of required roles.
- **Resource-privilege-based:** any-of resource:privilege pairs.
- **Attribute-based:** an `expr` expression evaluated over `{actor, vars}`.

The baseline implementation is in `casbinauthz`:

```go
// From a pre-built enforcer:
a := casbinauthz.NewCasbinAuthorizer(syncedEnforcer)

// Or from model + policy strings (testing / simple cases):
a, err := casbinauthz.NewCasbinAuthorizerFromStrings(modelText, policyText)

// Or from a live PostgreSQL pool (production):
a, closer, err := casbinauthz.NewCasbinAuthorizerFromDB(ctx, pool)
defer closer.Close()
```

Pass the authorizer to `runtime.NewRunner` via `runtime.WithAuthorizer(a)`.

---

## Scheduling and waits

Timers and SLA deadlines are driven by gocron (behind the `scheduling` abstraction).

- **Intermediate timer events** — pause execution for an ISO-8601 duration before continuing.
- **SLA deadlines** — if a human task (or any wait node) is not resolved within the SLA
  duration, the engine takes an alternative sequence flow and/or runs a recovery action.
- **In-wait reminder actions** — service actions executed on a repeating interval _during_
  a wait period (e.g. send a reminder email every 24 h while waiting for approval).

Configure timers on nodes:

```go
// UserTask with a 3-day SLA and daily reminders:
model.NewUserTask("approve", []string{"manager"},
    model.WithSLA("P3D", "escalate-flow", "notify-manager"),
    model.WithReminder("P1D", "send-reminder"),
)
```

---

## Compensation, resilience, and observability

### Compensation

Attach an optional compensation action to any activity. When the engine rolls back the
process (e.g. after a downstream failure), it runs compensation actions in reverse order.

```go
model.NewServiceTask("charge", "charge-card",
    model.WithCompensation("refund-card"),
)
```

Cancellation actions (run best-effort when the whole instance is cancelled) are set on
the definition:

```go
model.NewDefinition("order", 1).CancelActions("send-cancellation-email")
```

### Resilience

- **Retry:** configure `model.WithRetryPolicy(p)` on any activity node.
- **Recovery flow:** `model.WithRecoveryFlow(flowID)` routes the token to an alternative
  path on repeated failure.
- **Incidents and DLQ:** failed tokens that exhaust retries become incidents. Admins
  resolve them via `POST /admin/instances/{id}/incidents/{incidentID}/resolve` or
  `service.Service.ResolveIncident`. Dead-lettered outbox rows are surfaced at
  `GET /admin/dead-letters` and redriven via `POST /admin/dead-letters/redrive`.

### Observability

The `runtime.Runner` emits OpenTelemetry spans, metrics, and `slog`-structured logs
around every engine step and service-action invocation:

```go
r := runtime.NewRunner(
    cat, clock.System(), store,
    runtime.WithTracerProvider(tp),
    runtime.WithMeterProvider(mp),
    runtime.WithLogger(slog.Default()),
)
```

When a `With*` option is omitted, the runner defaults to the OTel global provider
(or noop) and `slog.Default()`.

---

## Testing

```bash
# Run the full test suite (requires Docker for testcontainers-based tests):
go test ./...

# Race detector + coverage:
go test -race -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1

# Lint:
golangci-lint run ./...
```

Tests that touch PostgreSQL, MinIO, or SNS use
[testcontainers-go](https://github.com/testcontainers/testcontainers-go) — they spin up
real containers and require a running Docker daemon. A shared
`internal/database.RunTestDatabase(t, opts...)` helper is available for database tests.

Pure unit tests (engine core, model validation, in-memory runner) need no Docker.

---

## For maintainers

### Repository layout

```
.                   # Single go.mod; root packages are the public library API
model/              # Process-definition types (public)
engine/             # Token state machine (public)
runtime/            # Reference driver + DTOs (public)
action/             # Service-action catalog (public)
humantask/          # Human-task model (public)
authz/              # Authorizer abstraction (public)
casbinauthz/        # Casbin-backed authorizer (public)
transport/rest/     # REST http.Handler factory (public)
transport/grpc/     # gRPC ServiceRegistrar registration (public)
persistence/        # Persistence façade (public)
eventing/           # Eventing façade (public)
scheduling/         # Scheduling façade (public)
observability/      # OTel + slog wiring (public)
clock/              # Clock abstraction (public)
service/            # Application-layer Service façade (public)
internal/           # Non-exported implementation details (consumers must not import)
examples/           # Reference wiring (illustrative main packages, not a product)
docs/adr/           # Architecture Decision Records (Nygard template)
docs/specs/         # Design docs / feature specs
docs/plans/         # Implementation plans
```

### Architecture Decision Records

ADRs live in `docs/adr/NNNN-<slug>.md` following the Nygard template
(Status/Date, Context, Decision, Consequences). See
`docs/adr/0001-record-architecture-decisions.md` as the canonical example.

### TDD discipline

New exported symbols require a failing test before any implementation is written
(red → green → refactor). See the TDD Operational Discipline section in `CLAUDE.md`.

### Locked tech stack

Changes to the tech stack (Go version, database, expression evaluator, event bus,
scheduler, DI container) require an ADR. The currently locked choices are documented
in `CLAUDE.md`.

---

## License

License: TBD by the project owner. No `LICENSE` file is present in this repository.

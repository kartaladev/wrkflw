# wrkflw

**An embeddable Go workflow engine — library, not a daemon.**

`wrkflw` is a single importable Go module (`github.com/zakyalvan/krtlwrkflw`) that a
consumer embeds in their own application. There is no owned binary, no daemon, and no
container you run. Process execution, human tasks, timers, compensation, and
authorization all live in the library's root packages. The HTTP transport
adapters are mountable route groups a consumer registers in their own server.

---

## What it is

- **Library-first.** The product is the module's public root packages — `engine/`,
  `definition/`, `runtime/`, etc. A consumer imports them and embeds the engine in their app.
  Every feature must be reachable through the public API; no feature lives exclusively
  in a binary or example.
- **Mountable transports, no owned main.** HTTP entry points are constructors the consumer
  calls and mounts in their own server — stdlib `*http.ServeMux`, gin, or fiber v3, each
  with native group structs the consumer positions wherever they choose. The `examples/`
  directory shows reference wiring but is not a shipped product.
- **Token-based execution.** Transitions between nodes are modeled as tokens. Each
  token carries process-instance variables that downstream nodes (e.g. exclusive
  gateways) read to make routing decisions.
- **BPMN-inspired, not BPMN-compatible.** The domain vocabulary — gateways, sequence
  flows, boundary events, compensation, error codes — is inspired by BPMN, but this
  is **not** a BPMN-compatible implementation and does not aim to load or round-trip
  arbitrary BPMN2 documents. Definitions are authored in Go or YAML only; there is no
  BPMN2 XML loader.
- **Expression evaluation** via [`expr-lang/expr`](https://github.com/expr-lang/expr):
  gateway conditions, attribute predicates, timer durations.

---

## Requirements

| Requirement | Version |
|---|---|
| Go | 1.25 |
| Database | PostgreSQL 17, MySQL 8.0+, or SQLite (`modernc.org/sqlite`, in-process; single-node / test / embedded) |
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

	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
)

func main() {
	// Node kinds live in BPMN-family packages (event, gateway, activity). The
	// fluent definition.NewBuilder(...) chain offers one terse AddX per kind; the generic
	// definition.NewBuilder(...).Add(node) form also works for dynamic nodes.
	def, err := definition.NewBuilder("order-fulfillment", 1).
		AddStartEvent("start").
		AddServiceTask("charge",
			activity.WithActionName("charge-card"),
			activity.WithCompensation("refund-card"),
		).
		AddUserTask("approve", []string{"manager"}).
		AddEndEvent("end").
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

### Definition-scoped & inline actions

Actions can be bound to a definition or a node in three ways:

| Style | How | Scope |
|---|---|---|
| Named catalog reference | `WithActionName("name")` | Resolves scoped → global |
| Node-local inline | `WithActionFunc(fn)` / `WithAction(a)` | That node only; never serialized |
| Default-by-id | omit name | Node id is the lookup key |

```go
score := action.ActionFunc(func(_ context.Context, in map[string]any) (map[string]any, error) {
    return map[string]any{"score": 42}, nil
})

def, _ := definition.NewBuilder("loan", 1).
    RegisterAction("score", score).                   // def-scoped, by name
    Add(event.NewStart("start")).
    Add(activity.NewServiceTask("risk",
        activity.WithActionName("score"),             // resolves scoped → global
    )).
    Add(activity.NewServiceTask("notify",
        activity.WithActionFunc(func(_ context.Context, in map[string]any) (map[string]any, error) {
            return in, nil                            // node-local inline
        }),
    )).
    Add(activity.NewServiceTask("archive")).          // default-by-id → looks up "archive"
    Add(event.NewEnd("end")).
    Connect("start", "risk").Connect("risk", "notify").
    Connect("notify", "archive").Connect("archive", "end").
    Build()
```

`WithActionName` and `WithAction`/`WithActionFunc` are mutually exclusive on a node; `Build`
returns `model.ErrActionInlineAndNameConflict` if both are set. See
`runtime.ExampleDefinitionBuilder_RegisterAction` for a runnable version.

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
f, _ := os.Open("order.yaml")
defer f.Close()
ld, err := definition.NewLoader(f) // NewLoader reads YAML from any io.Reader
if err != nil { log.Fatal(err) }
def, err := ld.Build()
```

### Run it

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
)

func main() {
	ctx := context.Background()

	def, _ := definition.NewBuilder("order", 1).
		Add(event.NewStart("s")).
		Add(activity.NewServiceTask("charge", activity.WithActionName("charge-card"))).
		Add(event.NewEnd("e")).
		Connect("s", "charge").
		Connect("charge", "e").
		Build()

	// Zero-argument: in-memory driver with action.DefaultCatalog() + kernel.NewMemInstanceStore().
	// For a process with service tasks, populate the default action catalog via action.Register /
	// action.MustRegister. For a process with call activities, populate the default definition
	// registry via runtime.RegisterDefinition / runtime.MustRegisterDefinition.
	action.MustRegister("charge-card", action.ActionFunc(func(_ context.Context, vars map[string]any) (map[string]any, error) {
		return map[string]any{"charged": true}, nil
	}))
	driver, err := runtime.NewProcessDriver()
	if err != nil { log.Fatal(err) }

	state, err := driver.Drive(ctx, def, "order-001", map[string]any{"amount": 99.0})
	if err != nil {
		log.Fatal(err)
	}
	if state.Status == engine.StatusCompleted {
		fmt.Println("order completed:", state.Variables["charged"])
	}
}
```

For signal/message delivery use `driver.Deliver(ctx, def, instanceID, trigger)`. See
`runtime/kernel/caching_store_example_test.go` for a park-then-resume pattern.

---

## Authoring forms

| Form | Function | Notes |
|---|---|---|
| Go builder | `definition.NewBuilder(...).AddServiceTask(...).Connect(...).Build()` | Preferred; compile-time safe. Node kinds live in `definition/{event,gateway,activity}`; the fluent `build` package has one `Add<Kind>` per kind, or use `definition.NewBuilder(...).Add(node)` for dynamic nodes. |
| YAML | `definition.NewLoader(r)` (any io.Reader) | Human-readable; lowerCamelCase kind discriminator; returns `DefinitionLoader` — call `.Build()` (optionally after `.RegisterAction(...)`) to obtain `*ProcessDefinition` |

---

## Package map

All packages live directly at the module root — no `pkg/` prefix.

| Package | Role |
|---|---|
| `definition` | Authoring entry: `NewBuilder` (Go) and `NewLoader` (YAML) only. Types/validation/serialization live in `definition/model`; sequence flows in `definition/flow`; node constructors in `definition/{event,gateway,activity}`; fluent builder in `definition/build`; deserialization bundle `definition/kinds`. Pure data + validation; no I/O. |
| `engine` | Core token state machine. Pure of transport, storage, and event-bus specifics — depends on interfaces only. |
| `runtime` | Reference driver `ProcessDriver` that wires the engine to persistence, scheduling, and actions, plus lifecycle helpers (`ShutdownGroup`). Supporting pieces live in sub-packages: `runtime/kernel` (in-memory `MemInstanceStore`/`CachingInstanceStore`, schedulers, definition registry, ownership), `runtime/view` (snapshot DTOs), `runtime/chain` (instance chaining), `runtime/task` (human-task service), plus `runtime/signal`, `runtime/calllink`, `runtime/monitor`. |
| `action` | Service-action catalog (`Catalog`, `Action`, `MapCatalog`, `ActionFunc` adapter). |
| `humantask` | Human-task model and ports that drive human work (claim, complete, reassign). |
| `authz` | Pluggable `Authorizer` abstraction: role, resource-privilege, and attribute-based rules. |
| `casbinauthz` | Casbin-backed `Authorizer` (baseline implementation). Wraps `*casbin.SyncedEnforcer`. |
| `transport` | HTTP transport adapters: `transport/http/httpcore` (shared pure-endpoint funcs, DTOs, validation, error classification, observability), `transport/http/stdlib` (net/http), `transport/http/gin`, `transport/http/fiber`. |
| `persistence` | Persistence façade over the neutral SQL store: `OpenPostgres`, `OpenMySQL`, and `OpenSQLite`, plus migrations and relay/lister/store constructors (ADR-0081/0082). |
| `eventing` | Eventing façade for publishing domain events via the transactional outbox. |
| `scheduling` | Façade over the timer/deadline scheduler (gocron behind the abstraction). |
| `observability` | Metrics, traces, and `slog` wiring at the runtime boundary. |
| `clock` | `clock.Clock` time abstraction. Supply `clock.System()` in production; inject a fake in tests. |
| `service` | Application-layer `Service` façade consumed by transport adapters. |

Implementation details a consumer must not import live under `internal/`.

---

## Mounting transports

The library provides three native HTTP adapter subpackages over a shared root:

| Subpackage | Router type | Dep added |
|---|---|---|
| `transport/http/stdlib` | `*http.ServeMux` | none |
| `transport/http/gin` | `gin.IRouter` | `github.com/gin-gonic/gin` |
| `transport/http/fiber` | `fiber.Router` | `github.com/gofiber/fiber/v3` |

Import only the subpackage you use — a stdlib consumer never pulls gin or fiber.

### stdlib (net/http)

```go
import (
    "net/http"

    "github.com/zakyalvan/krtlwrkflw/service"
    "github.com/zakyalvan/krtlwrkflw/transport/http/stdlib"
)

// svc is a service.Service (constructed via service.New or wired manually).
var svc service.Service

mux := http.NewServeMux()
stdlib.Mount(mux, svc)           // instance + task + message routes
stdlib.MountHealth(mux, dbCheck) // /healthz + /readyz
http.ListenAndServe(":8080", mux)
```

### gin

```go
import (
    "github.com/gin-gonic/gin"

    "github.com/zakyalvan/krtlwrkflw/transport/http/gin" // package alias: gintransport
    "github.com/zakyalvan/krtlwrkflw/service"
)

g := gin.Default()
gintransport.Mount(g, svc)
gintransport.MountHealth(g, dbCheck)
g.Run(":8080")
```

### fiber

```go
import (
    "github.com/gofiber/fiber/v3"

    "github.com/zakyalvan/krtlwrkflw/transport/http/fiber" // package alias: fibertransport
    "github.com/zakyalvan/krtlwrkflw/service"
)

app := fiber.New()
fibertransport.Mount(app, svc)
fibertransport.MountHealth(app, dbCheck)
app.Listen(":8080")
```

### Flexible base-path and admin-by-composition

All route groups are mounted at **relative** paths — no group hardcodes a
prefix. Use `WithBasePath` (stdlib) or native sub-routers (gin/fiber) to place
groups where you need them. Admin routes are **default-absent**: they do not
exist unless you explicitly mount `AdminRoutes` on a consumer-secured router
group — which is safer than a built-in default-deny gate.

```go
// stdlib — no sub-router; use WithBasePath
import (
    "github.com/zakyalvan/krtlwrkflw/transport/http/httpcore"
    "github.com/zakyalvan/krtlwrkflw/transport/http/stdlib"
)

mux := http.NewServeMux()
stdlib.InstanceRoutes{Svc: svc}.Customize(mux, httpcore.WithBasePath[*http.ServeMux]("/api/v1/workflow"))
stdlib.TaskRoutes{Svc: svc}.Customize(mux, httpcore.WithBasePath[*http.ServeMux]("/tasks"))
// Admin mounted separately — no route is registered until this call:
stdlib.AdminRoutes{Svc: svc, DeadLetters: dlq}.Customize(mux, httpcore.WithBasePath[*http.ServeMux]("/admin/workflow"))
stdlib.MountHealth(mux)
```

```go
// gin — native sub-router; WithBasePath still works but sub-routers are idiomatic
import (
    gintransport "github.com/zakyalvan/krtlwrkflw/transport/http/gin"
)

base  := g.Group("/api/v1/workflow")
tasks := g.Group("/tasks")
gintransport.InstanceRoutes{Svc: svc}.Customize(base)
gintransport.TaskRoutes{Svc: svc}.Customize(tasks)

// Admin secured by the consumer's native auth middleware:
admin := g.Group("/api/v1/workflow", myAuthMiddleware)
gintransport.AdminRoutes{Svc: svc, DeadLetters: dlq, Policies: pol}.Customize(admin)
gintransport.HealthRoutes{Checks: checks}.Customize(base)
```

```go
// fiber — same pattern as gin
import (
    fibertransport "github.com/zakyalvan/krtlwrkflw/transport/http/fiber"
)

base  := app.Group("/api/v1/workflow")
admin := app.Group("/api/v1/workflow", myAuthHandler)
fibertransport.InstanceRoutes{Svc: svc}.Customize(base)
fibertransport.AdminRoutes{Svc: svc, DeadLetters: dlq}.Customize(admin)
fibertransport.MountHealth(base)
```

For custom middleware on a single group use `WithMiddleware` (gin/fiber) or
`WithRouterFunc` (any framework) — see `transport/http/httpcore` godoc for the
full `CustomizeConfig[R]` and `CustomizeOption[R]` seam.

---

## Reading instance state

The `runtime/view` package provides two JSON-serializable projections built from an
`engine.InstanceState`:

| Type | Constructor | Purpose |
|---|---|---|
| `view.InstanceSnapshot` | `view.NewInstanceSnapshot(state, def)` | Full snapshot: tokens, variables, history, tasks, incidents. Omits engine bookkeeping. |
| `view.ActionableView` | `view.NewActionableView(state, def)` | Curated view: only open human tasks + allowed next actions (outgoing flows). |

The HTTP transport exposes these via:

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
    Authorize(ctx context.Context, spec AuthzSpec, actor Actor, vars map[string]any) error
}
```

`AuthzSpec` supports:
- **Role-based:** any-of required roles.
- **Resource-privilege-based:** any-of resource:privilege pairs.
- **Attribute-based:** an `expr` expression evaluated over `{actor, vars}`.

The baseline implementation is in `casbinauthz`:

```go
// From a pre-built enforcer:
a, _, err := casbinauthz.NewCasbinAuthorizer(casbinauthz.FromEnforcer(syncedEnforcer))

// Or from model + policy strings (testing / simple cases):
a, _, err := casbinauthz.NewCasbinAuthorizer(casbinauthz.FromStrings(modelText, policyText))

// Or from a live PostgreSQL pool (production):
a, closer, err := casbinauthz.NewCasbinAuthorizer(casbinauthz.FromDB(ctx, pool))
defer closer.Close()
```

Pass the authorizer to `runtime.NewProcessDriver` via `runtime.WithHumanTasks(resolver, taskStore, a)`
(the authorizer is the third argument).

---

## Scheduling and waits

Timers and deadlines are driven by gocron (behind the `scheduling` abstraction).

- **Intermediate timer events** — pause execution for a Go-duration interval (e.g. `"1h"`) before continuing.
- **Deadlines** — if a human task (or any wait node) is not resolved within the deadline
  duration, the engine takes an alternative sequence flow and/or runs a recovery action.
- **In-wait reminder actions** — service actions executed on a repeating interval _during_
  a wait period (e.g. send a reminder email every 24 h while waiting for approval).

Configure timers on nodes:

```go
// UserTask with a 3-day deadline and daily reminders. Durations are expr
// expressions evaluated to a Go duration via time.ParseDuration, so they are
// backtick-wrapped quoted duration literals ("72h", "24h" — not ISO-8601).
activity.NewUserTask("approve", []string{"manager"},
    activity.WithDeadline(`"72h"`, "escalate-flow", "notify-manager"),
    activity.WithWaitReminder(`"24h"`, "send-reminder"),
)
```

---

## Compensation, resilience, and observability

### Compensation

Attach an optional compensation action to any activity. When the engine rolls back the
process (e.g. after a downstream failure), it runs compensation actions in reverse order.

```go
activity.NewServiceTask("charge",
    activity.WithActionName("charge-card"),
    activity.WithCompensation("refund-card"),
)
```

Cancellation actions (run best-effort when the whole instance is cancelled) are set on
the definition:

```go
definition.NewBuilder("order", 1).CancelActions("send-cancellation-email")
```

### Resilience

- **Retry:** configure `activity.WithRetryPolicy(p)` on any activity node.
- **Recovery flow:** `activity.WithRecoveryFlow(flowID)` routes the token to an alternative
  path on repeated failure.
- **Incidents and DLQ:** failed tokens that exhaust retries become incidents. Admins
  resolve them via `POST /admin/instances/{id}/incidents/{incidentID}/resolve` or
  `service.Service.ResolveIncident`. Dead-lettered outbox rows are surfaced at
  `GET /admin/dead-letters` and redriven via `POST /admin/dead-letters/redrive`.

### Observability

The `runtime.ProcessDriver` emits OpenTelemetry spans, metrics, and `slog`-structured logs
around every engine step and service-action invocation:

```go
driver, err := runtime.NewProcessDriver(
    runtime.WithActionCatalog(cat),
    runtime.WithInstanceStore(store),
    runtime.WithTracerProvider(tp),
    runtime.WithMeterProvider(mp),
    runtime.WithLogger(slog.Default()),
)
if err != nil { log.Fatal(err) }
```

When a `With*` option is omitted, the runner defaults to the OTel global provider
(or noop) and `slog.Default()`.

`NewProcessDriver` also emits exactly one `DEBUG`-level log record after the option loop
completes, summarising which collaborators are wired:

```
DEBUG "ProcessDriver constructed"
    store=in-memory(non-durable)|custom
    catalog=default-global|custom
    definitions=default-global|custom
    humanTasks=on|off  scheduler=on|off  ...
```

`definitions=default-global` means the driver is using `runtime.DefaultDefinitionRegistry()`;
`definitions=custom` means `runtime.WithDefinitions(reg)` was called with a non-nil registry.
This log is suppressed in production unless the consumer enables debug logging.

---

## Process-instance chaining

Automatically start a new, **independent** top-level instance when another reaches a
terminal state (completed, failed, or terminated) — e.g. when an `approval` process
completes, start a `fulfillment` process seeded with its result. The predecessor fully
ends and releases its resources; the successor is a fresh root instance that outlives it.
This is sequential chaining of independent instances, driven off the durable terminal
outbox events — **not** the parent→child nesting of a call activity.

A `SuccessorPolicy` callback decides the successor for each terminal outcome; the
`chain.Chainer` records the lineage hop and starts the successor with a deterministic
id, so a redelivered event is a clean no-op (exactly-once effect under at-least-once
delivery).

```go
policy := func(_ context.Context, ev chain.ChainEvent) (chain.SuccessorDecision, bool) {
    if ev.Outcome != kernel.OutcomeCompleted {
        return chain.SuccessorDecision{}, false // end the chain
    }
    return chain.SuccessorDecision{Def: fulfillmentDef, Vars: ev.Result}, true
}
chainer, err := chain.NewChainer(runner, policy, chain.WithChainLinks(links))
if err != nil {
    log.Fatal(err) // NewChainer rejects a nil starter/policy with ErrNilDependency
}

// Drive it from the broker: mount eventing.NewChainHandler(chainer) on your own
// message.Router, or run the turnkey wrapper that subscribes the terminal topics:
go eventing.NewChainerRunner(chainer).Run(ctx, subscriber)
```

Terminal outbox events are **status-accurate**: completed → `instance.completed`, failed →
`instance.failed`, terminated → `instance.terminated`. Chaining lineage is durable and
queryable (`kernel.ChainLinkStore`, in-memory or Postgres via
`persistence.NewChainLinkStore`), and a policy can route on the predecessor definition
(`ChainEvent.PredecessorDefinitionRef`). See
[`runtime/README.md`](runtime/README.md#process-instance-chaining) for the full API.

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
definition/              # Process-definition types (public; leaves: event/gateway/activity)
engine/             # Token state machine (public)
runtime/            # Reference driver + DTOs (public)
action/             # Service-action catalog (public)
humantask/          # Human-task model (public)
authz/              # Authorizer abstraction (public)
casbinauthz/        # Casbin-backed authorizer (public)
transport/http/         # HTTP transport adapters (public)
transport/http/httpcore/    # shared pure-endpoint funcs, DTOs, validation, observability
transport/http/stdlib/      # net/http adapter
transport/http/gin/         # gin adapter
transport/http/fiber/       # fiber v3 adapter
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

## Node types

A process definition is a graph of **nodes** connected by **sequence flows**. Every
node is a value built with a constructor from `definition/{event,gateway,activity}`
and implements the `model.Node` interface (`Kind() NodeKind`, `ID() string`,
`Name() string`). Never construct the struct types directly — use the constructors.
There are 19 node kinds, grouped below.

All examples in this section are excerpts; the full, compiling programs they are
drawn from live under [`examples/scenarios/`](examples/scenarios).

### Shared activity options

Every **activity** constructor (`NewServiceTask`, `NewUserTask`, `NewReceiveTask`,
`NewSendTask`, `NewBusinessRuleTask`, `NewSubProcess`, `NewCallActivity`) accepts the
same set of functional options:

| Option | Configures |
|---|---|
| `model.WithName(string)` | Human-readable display name. |
| `activity.WithRetryPolicy(*model.RetryPolicy)` | Per-node retry policy (see below). |
| `activity.WithRecoveryFlow(flowID string)` | Sequence flow taken when retries are exhausted. |
| `activity.WithCompensation(actionName string)` | Service action invoked on rollback (reverse order). |
| `activity.WithCancelHandler(actionName string)` | Service action run when the node is interrupted. |
| `activity.WithDeadline(duration, flowID, actionName string)` | On deadline breach: take `flowID` and/or run `actionName`. |
| `activity.WithWaitReminder(every, actionName string)` | Run `actionName` repeatedly *during* the wait. |

Two options are **compile-enforced** to a single constructor:

- `activity.WithEligibilityExpr(expr string)` — **`NewUserTask` only**. Attribute-based
  eligibility predicate (evaluated by authz over `vars[...]`). Passing it to any other
  constructor is a compile error.
- `activity.WithCorrelationKey(key string)` — **`NewReceiveTask` only**. Correlation-key
  expression. Passing it elsewhere is a compile error.

> **Durations are expr-lang expressions parsed by Go's `time.ParseDuration`.** Write
> them as **quoted Go-duration strings** — `` `"1h"` ``, `` `"30m"` ``, `` `"45s"` `` —
> not ISO-8601. This applies to `WithBoundaryTimer`, `WithCatchTimer`, `WithDeadline`,
> `WithWaitReminder`, `WithStartTimer`, and `WithCatchDeadline`/`WithCatchWaitReminder`.

`RetryPolicy` fields:

```go
model.RetryPolicy{
    MaxAttempts:        5,                 // includes the first attempt; 0 = unlimited
    InitialInterval:    1 * time.Second,
    BackoffCoef:        2.0,               // exponential multiplier
    MaxInterval:        30 * time.Second,
    MaxElapsed:         5 * time.Minute,
    NonRetryableErrors: []string{"validation"},
}
```

### Events

| Node | What it does | Constructor |
|---|---|---|
| **StartEvent** | Entry point of a process (or the trigger of an EventSubProcess). | `event.NewStart(id string, opts ...) Node` |
| **EndEvent** | Normal completion of one branch. | `event.NewEnd(id string, name ...string) Node` |
| **TerminateEndEvent** | Terminates the whole instance, including parallel branches. | `event.NewTerminateEnd(id string, name ...string) Node` |
| **ErrorEndEvent** | Throws an error code, caught by a boundary error event. | `event.NewErrorEnd(id, errorCode string, name ...string) Node` |

`NewStartEvent` options (only meaningful when the start is an EventSubProcess trigger):
`WithName(string)`, `WithStartSignal(name)`, `WithStartMessage(msg, key)`,
`WithStartTimer(dur)`. An empty `errorCode` on `NewErrorEndEvent` throws an anonymous
(catch-all) error.

```go
event.NewStart("start")
event.NewEnd("end", "Order complete")
event.NewTerminateEnd("kill-all")
event.NewErrorEnd("insufficient-funds", "FUNDS_ERROR")
```

### Activities

| Node | What it does | Constructor |
|---|---|---|
| **ServiceTask** | Runs a named service action. | `activity.NewServiceTask(id string, opts ...) Node` |
| **UserTask** | Waits for a human to complete a work item. | `activity.NewUserTask(id string, roles []string, opts ...) Node` |
| **ReceiveTask** | Waits for an inbound correlated message. | `activity.NewReceiveTask(id, messageName string, opts ...) Node` |
| **SendTask** | Sends an outbound message. | `activity.NewSendTask(id, messageName string, opts ...) Node` |
| **BusinessRuleTask** | Runs a named business-rule action. | `activity.NewBusinessRuleTask(id string, opts ...) Node` |
| **SubProcess** | Runs an *embedded* nested definition as a scope. | `activity.NewSubProcess(id string, sub *model.ProcessDefinition, opts ...) Node` |
| **CallActivity** | Calls a *separate* top-level definition by name. | `activity.NewCallActivity(id, defRef string, opts ...) Node` |
| **EventSubProcess** | Event-triggered subprocess rooted at an event start. | `event.NewEventSubProcess(id string, sub *model.ProcessDefinition, opts ...) Node` |

All activity constructors take the shared activity options above. `NewUserTask` also
takes `WithEligibilityExpr`; `NewReceiveTask` also takes `WithCorrelationKey`.
`NewEventSubProcess` takes `WithName(string)` and `WithEventSubProcessNonInterrupting()` (default
is interrupting) — its nested start event carries the trigger.

```go
activity.NewServiceTask("charge",
    activity.WithActionName("charge-card"),
    activity.WithCompensation("refund-card"),
    activity.WithRetryPolicy(&retry),
)
activity.NewUserTask("approve", []string{"manager"},
    activity.WithDeadline(`"3h"`, "escalate-flow", "notify-manager"),
    activity.WithEligibilityExpr(`vars["region"] == "EU"`),
)
activity.NewReceiveTask("await-payment", "payment-received",
    activity.WithCorrelationKey("orderId"),
)
activity.NewSubProcess("reserve-hotel", hotelDef)
activity.NewCallActivity("credit-check", "credit-check")        // resolved via a DefinitionRegistry
event.NewEventSubProcess("on-cancel", cancelHandlerDef, event.WithEventSubProcessNonInterrupting())
```

### SendTask delivery (transactional outbox)

A BPMN `SendTask` emits its outbound message as a `message.<MessageName>` event written
into the same `wrkflw_outbox` (and the same transaction) as the state commit, then relayed
at-least-once by the outbox relay — no separate message-sink wiring, no stranding window (ADR-0067).

The event payload is `{"messageName", "correlationKey", "variables"}`, with `instance_id`
and `definition_ref` as message metadata. Consume it like any other outbox topic. To deliver
a message intra-engine (resume a parked `ReceiveTask`), mount `eventing.NewMessageHandler`
on your message router and route to `ProcessDriver.DeliverMessage`:

```go
handler := eventing.NewMessageHandler(func(ctx context.Context, name, key string, vars map[string]any) error {
    return runner.DeliverMessage(ctx, receiverDef, name, key, vars)
})
```

`DeliverMessage`'s waiter index is in-memory per `ProcessDriver`, so intra-engine correlation works
within one process; for cross-process correlation, subscribe `message.*` in your own consumer.

### Intermediate and boundary events

| Node | What it does | Constructor |
|---|---|---|
| **IntermediateCatchEvent** | Pauses until a timer, signal, or message arrives. | `event.NewCatch(id string, opts ...) Node` |
| **IntermediateThrowEvent** | Throws a signal or triggers compensation. | `event.NewThrow(id string, opts ...) Node` |
| **BoundaryEvent** | Event attached to an activity; fires on timer/signal/error. | `event.NewBoundary(id, attachedTo string, opts ...) Node` |

`NewIntermediateCatchEvent` options: `WithCatchTimer(dur)`, `WithCatchSignal(name)`,
`WithCatchMessage(msg, key)`, `WithCatchDeadline(dur, flow, action)`,
`WithCatchWaitReminder(every, action)`, `WithName(string)`.
`NewIntermediateThrowEvent` options: `WithThrowSignal(name)`,
`WithCompensateRef(nodeID)` (empty = scope-wide compensation), `WithThrowName(name)`.
`NewBoundaryEvent` options: `WithBoundaryTimer(dur)`, `WithBoundarySignal(name)`,
`WithBoundaryMessage(msg, key)`, `WithBoundaryErrorCode(code)` (empty = catch-all),
`WithBoundaryNonInterrupting()` (default interrupting), `WithName(string)`.

> **Boundary events:** timer, signal, error, and message boundaries are all armed and
> fired by the engine (message boundaries since ADR-0053).

```go
event.NewCatch("wait-1h", event.WithCatchTimer(`"1h"`))
event.NewThrow("compensate", event.WithCompensateRef("reserve-hotel"))
event.NewBoundary("review-timeout", "review", event.WithBoundaryTimer(`"1h"`))
```

### Gateways

Gateways take no options beyond an optional name — their behaviour is determined by
the number of incoming and outgoing flows and (for conditional gateways) the flow
conditions.

| Node | What it does | Constructor |
|---|---|---|
| **ExclusiveGateway** | XOR. Split: first matching flow (or the default). Merge: pass-through. | `gateway.NewExclusive(id string, name ...string) Node` |
| **ParallelGateway** | AND. Split: activate all outgoing. Join: wait for all incoming. | `gateway.NewParallel(id string, name ...string) Node` |
| **InclusiveGateway** | OR. Split: every matching flow. Join: wait for the active matching branches. | `gateway.NewInclusive(id string, name ...string) Node` |
| **EventBasedGateway** | Race: routes to whichever following catch event fires first. | `gateway.NewEventBased(id string, name ...string) Node` |

```go
gateway.NewExclusive("route")
gateway.NewParallel("fork")
gateway.NewInclusive("split")
gateway.NewEventBased("await")
```

### DefinitionBuilder

Assemble nodes and flows with the fluent builder, then `Build()` (which validates):

```go
def, err := definition.NewBuilder("order-fulfillment", 1).
    Add(event.NewStart("start")).
    Add(gateway.NewExclusive("route")).
    Add(activity.NewServiceTask("manual-review", activity.WithActionName("manual-review"))).
    Add(activity.NewServiceTask("auto-approve", activity.WithActionName("auto-approve"))).
    Add(activity.NewServiceTask("reject", activity.WithActionName("reject"))).
    Add(event.NewEnd("end")).
    Connect("start", "route").
    Connect("route", "manual-review", flow.WithCondition("amount > 50000")).
    Connect("route", "auto-approve", flow.WithCondition("amount <= 50000")).
    Connect("route", "reject", flow.AsDefault()).
    Connect("manual-review", "end").
    Connect("auto-approve", "end").
    Connect("reject", "end").
    CancelActions("send-cancellation-email"). // best-effort on instance cancel
    Build()
```

| Builder method | Purpose |
|---|---|
| `definition.NewBuilder(id, version)` | Start a builder. |
| `.Add(node)` | Append a node. |
| `.Connect(fromID, toID, opts...)` | Add a sequence flow (ID auto = `"from->to"`). |
| `.CancelActions(names...)` | Best-effort actions run when the instance is cancelled. |
| `.Build()` | Assemble + validate; returns `(*model.ProcessDefinition, error)`. |

Flow options for `.Connect`: `flow.WithFlowID(id)`, `flow.WithCondition(expr)`,
`flow.AsDefault()`.

> **Flow conditions use bare variable keys.** A condition is evaluated by expr-lang
> directly against the process-variable map, so write `flow.WithCondition("amount > 100")`
> — **not** `vars.amount`. (Only `WithEligibilityExpr` on a UserTask uses the
> `vars[...]` form, because it is evaluated by authz.)

### YAML authoring

Definitions can also be authored in YAML and loaded with `definition.NewLoader(r)` (any io.Reader);
Each node carries a `kind` discriminator (lowerCamelCase):

```yaml
id: order
version: 1
nodes:
  - id: start
    kind: startEvent
  - id: charge
    kind: serviceTask
    action: charge-card
    compensationAction: refund-card
    retryPolicy: { maxAttempts: 5, initialInterval: 1s, backoffCoef: 2.0 }
  - id: approve
    kind: userTask
    candidateRoles: [manager]
    deadlineDuration: "3h"
    deadlineFlow: escalate
    deadlineAction: notify-manager
  - id: end
    kind: endEvent
flows:
  - { id: f1, source: start,  target: charge }
  - { id: f2, source: charge, target: approve }
  - { id: f3, source: approve, target: end }
```

Valid `kind` values: `startEvent`, `endEvent`, `terminateEndEvent`, `errorEndEvent`,
`serviceTask`, `userTask`, `receiveTask`, `sendTask`, `businessRuleTask`, `subProcess`,
`callActivity`, `eventSubProcess`, `intermediateCatchEvent`, `intermediateThrowEvent`,
`boundaryEvent`, `exclusiveGateway`, `parallelGateway`, `inclusiveGateway`,
`eventBasedGateway`.

---

## Complex scenarios

The scenarios below are realistic multi-node processes drawn from order-fulfillment
and loan-origination domains. **Each is a complete, compiling, runnable program** under
[`examples/scenarios/`](examples/scenarios) — run any of them with
`go run ./examples/scenarios/<name>/`. The snippets here are excerpts; the linked
program is the source of truth.

### 1. Parallel fork & join

A `ParallelGateway` split fans a single token into N branches that all run; a matching
`ParallelGateway` join waits for **every** branch before the process continues.

```
start → fork[Parallel] → pick-items[Service]  ┐
                       → charge-card[Service]  ┤→ join[Parallel] → ship[Service] → end
```

```go
def, _ := definition.NewBuilder("order-fulfillment", 1).
    Add(event.NewStart("start")).
    Add(gateway.NewParallel("fork")).
    Add(activity.NewServiceTask("pick-items", activity.WithActionName("pick-items"))).
    Add(activity.NewServiceTask("charge-card", activity.WithActionName("charge-card"))).
    Add(gateway.NewParallel("join")).
    Add(activity.NewServiceTask("ship", activity.WithActionName("ship"))).
    Add(event.NewEnd("end")).
    Connect("start", "fork").
    Connect("fork", "pick-items").
    Connect("fork", "charge-card").
    Connect("pick-items", "join").
    Connect("charge-card", "join").
    Connect("join", "ship").
    Connect("ship", "end").
    Build()
```

**At runtime:** `driver.Drive` drives `pick-items` and `charge-card`, then the join releases a
single token to `ship` only after both arrive. Both branches' output variables are
merged into the instance.
→ [`examples/scenarios/parallel_fork_join`](examples/scenarios/parallel_fork_join)

### 2. Exclusive routing with conditions and a default

An `ExclusiveGateway` split takes the **first** outgoing flow whose condition is true,
falling back to the flow marked `AsDefault()` when none match.

```
start → check-credit[Service] → route[Exclusive]
            amount > 50000   → manual-review[Service] → end
            amount <= 50000  → auto-approve[Service]  → end
            (default)        → reject[Service]        → end
```

```go
Connect("route", "manual-review", flow.WithCondition("amount > 50000")).
Connect("route", "auto-approve",  flow.WithCondition("amount <= 50000")).
Connect("route", "reject",        flow.AsDefault()).
```

**At runtime:** the example runs the same definition twice — `amount=75000` routes to
`manual-review`, `amount=20000` routes to `auto-approve`. Conditions reference the bare
variable key `amount`, not `vars.amount`.
→ [`examples/scenarios/exclusive_routing`](examples/scenarios/exclusive_routing)

### 3. Activity deadline / timeout escalation

A **`WithDeadline`** option attached to an activity arms a deadline timer; if the
activity is still in progress when the deadline elapses, the engine runs the fire-once
breach action (the third argument), cancels the in-progress task, and routes the token
down a named deadline flow to an escalation path.

```
start → review[UserTask, deadline "1h" → flow "review-overdue", action "notify-overdue"] ──→ approved-end
            │ (deadline breach)
            ↓
        escalate[Service "reassign"] → escalated-end
```

```go
Add(activity.NewUserTask("review", []string{"reviewer"},
    activity.WithDeadline(`"1h"`, "review-overdue", "notify-overdue"))). // fire-once breach action
Add(activity.NewServiceTask("escalate", activity.WithActionName("reassign"))).
// ...
Connect("review", "approved-end").                                  // normal path
Connect("review", "escalate", flow.WithFlowID("review-overdue")).  // deadline flow
Connect("escalate", "escalated-end").
```

**At runtime:** the reviewer never claims the task; advancing a `*clockwork.FakeClock`
past the deadline and calling `sched.Tick(ctx)` fires the deadline timer, runs the
fire-once `notify-overdue` breach action, cancels the host user task, routes to
`escalate`, and completes via `escalated-end`. The breach action is fire-and-forget
(its result is not fed back). The example wires `WithHumanTasks` (so the user task
parks) and `WithScheduler` (so the timer arms).
(For edge-attached `BoundaryEvent` timers/signals/errors/messages, see the
`WithBoundary*` options in the node reference above.)
→ [`examples/scenarios/usertask_deadline`](examples/scenarios/usertask_deadline)

### 4. Compensation / saga rollback

Activities carrying `WithCompensation(...)` record an undo action when they complete. On
rollback the engine invokes those undo actions in **reverse completion order**.

```
start → book[Service, comp:cancel-booking]
      → pay [Service, comp:refund]
      → ship[Service] -- fails ──(boundary error)──→ end-fail
      → end

then: deliver CompensateRequested("") → refund, then cancel-booking → terminated
```

```go
Add(activity.NewServiceTask("book", activity.WithActionName("book"), activity.WithCompensation("cancel-booking"))).
Add(activity.NewServiceTask("pay", activity.WithActionName("pay"), activity.WithCompensation("refund"))).
Add(activity.NewServiceTask("ship", activity.WithActionName("ship"))).
Add(event.NewBoundary("ship-err", "ship", event.WithBoundaryErrorCode(""))).
// ... after the forward run completes via the boundary path:
trg := engine.NewCompensateRequested(clk.Now(), "") // "" = full rollback
final, _ := driver.Deliver(ctx, def, instanceID, trg)
```

**At runtime:** `book` and `pay` succeed, `ship` fails. A catch-all boundary error
routes to `end-fail` so the instance reaches `StatusCompleted` **without** triggering
the engine's automatic unhandled-error compensation — leaving the recorded
compensations intact. An operator then delivers `CompensateRequested` with an empty
`ToNode`; the engine runs `refund` then `cancel-booking` (reverse order) and the
instance ends `StatusTerminated`. Observed invocation order:
`[book pay ship refund cancel-booking]`.
→ [`examples/scenarios/compensation_saga`](examples/scenarios/compensation_saga)

### 5. Human-task approval

A `UserTask` parks the instance until a human claims and completes it. The lifecycle is
driven through the `humantask` ports and `task.TaskService`.

```
start → approve[UserTask, roles: manager] → end
```

```go
// This process has no service tasks; zero-arg driver uses the default in-memory catalog and store.
driver, err := runtime.NewProcessDriver(
    runtime.WithClock(clk),
    runtime.WithHumanTasks(resolver, taskStore, authz.RoleAuthorizer{}))
if err != nil {
    log.Fatal(err)
}

parked, _ := driver.Drive(ctx, def, instanceID, map[string]any{"amount": 4200}) // parks at "approve"

claimable, _ := taskStore.ClaimableBy(ctx, manager)        // discover tasks
svc, _ := task.NewTaskService(taskStore, az, runtime.WithClock(clk))

claimTrg, _ := svc.Claim(ctx, claimable[0].TaskToken, manager)
driver.Deliver(ctx, def, instanceID, claimTrg)                  // → Claimed

completeTrg, _ := svc.Complete(ctx, claimable[0].TaskToken, manager, map[string]any{"approved": true})
final, _ := driver.Deliver(ctx, def, instanceID, completeTrg)   // → Completed
```

**At runtime:** `Run` returns with `StatusRunning` (parked). `ClaimableBy` lists the
task for the manager actor; `Claim` then `Complete` (each followed by `driver.Deliver`) drive
the instance to `StatusCompleted`, merging the completion output (`approved`) into the
variables. See `runtime/human_example_test.go` for the authoritative end-to-end test
(including attribute-based eligibility and deadline escalation).
→ [`examples/scenarios/usertask_approval`](examples/scenarios/usertask_approval)

### 6. Sub-process and call activity

A `SubProcess` **embeds** a nested definition inline; a `CallActivity` **references** a
separate, reusable top-level definition by name through a `DefinitionRegistry`. Both run
the nested definition as a scope and merge its output back into the parent.

```
SubProcess:   start → reserve-hotel[SubProcess: hotel-start → book-room → hotel-end]
                    → send-confirmation[Service] → end

CallActivity: parent-start → call[CallActivity → "credit-check"] → parent-end
              "credit-check" = child-start → score[Service] → child-end  (registered)
```

```go
// Embedded sub-process:
hotel, _ := definition.NewBuilder("hotel-reservation", 1). /* ... */ .Build()
def, _  := definition.NewBuilder("travel-booking", 1).
    Add(activity.NewSubProcess("reserve-hotel", hotel)).
    /* ... */ .Build()

// Call activity (separate definition resolved by name).
//
// Option A — process-global default registry (zero-config, no WithDefinitions needed):
runtime.MustRegisterDefinition(child) // registers under "credit-check" and "credit-check:1"
driver, _ := runtime.NewProcessDriver(
    runtime.WithActionCatalog(cat),
    // driver uses runtime.DefaultDefinitionRegistry() automatically
)

// Option B — explicit per-driver registry (test isolation or multiple driver instances):
reg := kernel.NewMapDefinitionRegistry(map[string]*model.ProcessDefinition{"credit-check": child})
driver, _ = runtime.NewProcessDriver(
    runtime.WithActionCatalog(cat),
    runtime.WithDefinitions(reg), // nil is ignored; pass a non-nil registry to override
)
```

**At runtime:** the SubProcess example books a room inside the nested scope and merges
`confirmation` back into the parent; the CallActivity example resolves `"credit-check"`
from the registry, runs it to completion, and merges `credit_score` into the parent.
Use a SubProcess for a scope private to one definition; use a CallActivity to reuse a
standalone, independently-versioned definition.
→ [`examples/scenarios/subprocess_embedded`](examples/scenarios/subprocess_embedded),
[`examples/scenarios/call_activity`](examples/scenarios/call_activity)

### 7. Inclusive (OR) gateway split & join

An `InclusiveGateway` split activates **every** outgoing flow whose condition is true
(zero, one, or many); the matching join waits for exactly the branches that were
activated.

```
start → assess[Service] → split[Inclusive]
          score < 600     → notify-risk[Service]    ┐
          amount > 10000  → senior-review[Service]  ┤→ join[Inclusive] → end
          flagged == true → fraud-check[Service]    ┘
```

```go
Connect("split", "notify-risk",   flow.WithCondition("score < 600")).
Connect("split", "senior-review", flow.WithCondition("amount > 10000")).
Connect("split", "fraud-check",   flow.WithCondition("flagged == true")).
```

**At runtime:** with `score=580`, `amount=25000`, `flagged=false`, the split activates
`notify-risk` and `senior-review` but skips `fraud-check`; the join waits for those two
branches only, then continues to `end`. Contrast with the exclusive gateway (exactly
one branch) and the parallel gateway (all branches unconditionally).
→ [`examples/scenarios/inclusive_gateway`](examples/scenarios/inclusive_gateway)

### 8. Instance cancellation & cleanup

`ProcessDriver.CancelInstance` terminates a running instance mid-flight. It runs the
definition's `CancelActions` (best-effort, in order — a failing one is logged and
skipped), clears all tokens to `StatusTerminated`, and reconciles the human-task
projection so a parked task is marked `Cancelled` rather than orphaned in an inbox
query (ADR-0088).

```
start → fulfil[UserTask] → end   ── cancel ──▶ [release-inventory, notify-customer] → terminated
```

```go
def, _ := definition.NewBuilder("order-fulfilment", 1).
    Add(event.NewStart("start")).
    Add(activity.NewUserTask("fulfil", []string{"fulfiller"})).
    Add(event.NewEnd("end")).
    Connect("start", "fulfil").Connect("fulfil", "end").
    CancelActions("release-inventory", "notify-customer").
    Build()

parked, _ := driver.Drive(ctx, def, "order-9001", nil)      // parks at "fulfil" (StatusRunning)
final, _ := driver.CancelInstance(ctx, def, "order-9001") // → StatusTerminated
```

**At runtime:** `CancelInstance` runs `release-inventory` then `notify-customer` (the
latter may fail without aborting the cancel), clears the token, and the parked task
transitions to `Cancelled` — it no longer appears in `taskStore.ClaimableBy`.
→ [`examples/scenarios/instance_cancellation`](examples/scenarios/instance_cancellation)

### 9. Message correlation

A `ReceiveTask` parks until a named message is delivered with a matching correlation key
(an `expr` expression over the instance variables). Delivery is **point-to-point** —
`DeliverMessage` resumes only the instance whose key matches.

```
start → await-payment[ReceiveTask "PaymentReceived", key = orderID] → ship → end
```

```go
def, _ := definition.NewBuilder("order-shipping", 1).
    Add(event.NewStart("start")).
    Add(activity.NewReceiveTask("await-payment", "PaymentReceived",
        activity.WithCorrelationKey("orderID"))).
    Add(activity.NewServiceTask("ship", activity.WithActionName("ship-order"))).
    Add(event.NewEnd("end")).
    Connect("start", "await-payment").
    Connect("await-payment", "ship").Connect("ship", "end").
    Build()

driver.Drive(ctx, def, "order-1", map[string]any{"orderID": "order-1"}) // parks on "PaymentReceived"
driver.DeliverMessage(ctx, def, "PaymentReceived", "order-1", nil)    // resumes order-1 only
```

**At runtime:** two orders park on the same message name; delivering key `"order-1"`
advances only order 1 through `ship → end`, leaving order 2 waiting for its own key.
→ [`examples/scenarios/message_correlation`](examples/scenarios/message_correlation)

### 10. Signal broadcast

An `IntermediateCatchEvent` with a signal name parks until the signal is published to a
`SignalBus`. Delivery is **fan-out** — one `bus.Publish` resumes every instance awaiting
that name. The bus needs a deliver callback into the driver and the driver needs the bus,
so a forward-reference wires the cycle.

```
start → await["market-open" catch] → trade[Service] → end        (× N instances)
```

```go
var driver *runtime.ProcessDriver
bus, _ := signal.NewSignalBus(func(ctx context.Context, id string, trg engine.Trigger) error {
    _, err := driver.Deliver(ctx, def, id, trg)
    return err
})
driver, _ = runtime.NewProcessDriver(cat, store, runtime.WithSignalBus(bus))

driver.Drive(ctx, def, "desk-A", nil)       // parks awaiting "market-open"
driver.Drive(ctx, def, "desk-B", nil)
bus.Publish(ctx, "market-open", nil) // resumes BOTH desks
```

**At runtime:** each parked catcher is auto-subscribed to the bus after it parks; a single
`Publish` fans the signal out to all of them and each runs its service task to completion.
→ [`examples/scenarios/signal_broadcast`](examples/scenarios/signal_broadcast)

### 11. Retry with recovery

`WithRetryPolicy` turns a retryable action failure into a scheduled retry instead of an
incident. Retries are **not** automatic: a failed attempt parks the instance on a backoff
timer, so a scheduler tick (driven here by a fake clock) fires the next attempt.

```
start → charge[Service "charge-card", retry ≤5, backoff ×2] → end
```

```go
Add(activity.NewServiceTask("charge", activity.WithActionName("charge-card"),
    activity.WithRetryPolicy(&model.RetryPolicy{
        MaxAttempts: 5, InitialInterval: time.Second, BackoffCoef: 2.0,
    })))
// driver wired with WithClock(fc), WithScheduler(sched), WithJitterSource(...)
driver.Drive(ctx, def, "pay-1", nil)             // attempt 1 fails → parks on retry timer
for attempts < 3 {                        // advance + tick until it recovers
    fc.Advance(time.Minute)
    sched.Tick(ctx)
}
```

**At runtime:** the action fails twice then succeeds on attempt 3; advancing the clock and
ticking the scheduler between attempts drives the exponential backoff, and the instance
reaches `StatusCompleted` with no incident raised.
→ [`examples/scenarios/retry_recovery`](examples/scenarios/retry_recovery)

### 12. In-wait reminders

`WithWaitReminder(every, action)` schedules a recurring in-wait action that fires once per
interval **while** a task is open, re-arming itself each time. It stops automatically once
the task is completed, cancelled, or breached — distinct from the one-shot `WithDeadline`
escalation in scenario 3.

```
start → review[UserTask, reminder every "30m" → "nudge-reviewer"] → end
```

```go
Add(activity.NewUserTask("review", []string{"reviewer"},
    activity.WithWaitReminder(`"30m"`, "nudge-reviewer")))
// driver wired with WithClock(fc), WithScheduler(sched), WithHumanTasks(...)
driver.Drive(ctx, def, "review-77", nil)                            // parks; first reminder armed
for range 3 { fc.Advance(30 * time.Minute); sched.Tick(ctx) } // 3 nudges fire
// reviewer then claims + completes → further ticks fire nothing
```

**At runtime:** three ticks fire three `nudge-reviewer` reminders while the reviewer sits
on the task; once the task completes the recurring timer goes stale and no further
reminders run.
→ [`examples/scenarios/inwait_reminder`](examples/scenarios/inwait_reminder)

### 13. Event-based gateway (race)

`gateway.NewEventBased` fans out to several following catch events and takes the branch of
whichever fires **first** — the losing arms are cancelled. It models "wait for A **or** B",
e.g. await a payment confirmation (signal) or a payment-window timeout (timer).

```
start → gw[event-based] ─┬─ await-payment[catch signal "payment-confirmed"] → ship   → shipped-end
                         └─ payment-window[catch timer "24h"]                → cancel → cancelled-end
```

```go
Add(gateway.NewEventBased("gw")).
Add(event.NewCatch("await-payment", event.WithCatchSignal("payment-confirmed"))).
Add(event.NewCatch("payment-window", event.WithCatchTimer(schedule.AfterDuration(24*time.Hour)))).
// ... gw → both arms; each arm → its own service task + end
driver.Drive(ctx, def, "order-fast", nil) // parks at gw with both arms live
bus.Publish(ctx, "payment-confirmed", nil) // signal wins → ship branch; timer arm cancelled
```

**At runtime:** one instance receives the signal first (ships; the 24h timer is cancelled);
another gets no payment and, once the fake clock advances past the window, the timer fires
(cancels the order; the signal arm is cancelled). Same definition, opposite outcome —
decided by whichever event happened first.
→ [`examples/scenarios/event_based_gateway`](examples/scenarios/event_based_gateway)

### 14. Catch-event in-wait reminder

`WithCatchWaitReminder(every, action)` attaches a recurring in-wait reminder to an
**intermediate catch event** — the same reminder mechanism as scenario 12, now generalized
beyond `UserTask`. The reminder fires once per interval **while** the instance is parked
awaiting the catch, re-arming itself each time, and is cancelled automatically the moment the
catch resolves.

```
start → await[catch signal "approved", reminder every "30m" → "nudge"] → end
```

```go
Add(event.NewCatch("await",
    event.WithCatchSignal("approved"),
    event.WithCatchWaitReminder(schedule.Every(30*time.Minute), "nudge"))).
// driver wired with WithClock(fc), WithScheduler(sched), WithSignalBus(bus)
driver.Drive(ctx, def, "approval-001", nil)                  // parks; reminder armed
for range 3 { fc.Advance(30 * time.Minute) }                 // 3 nudges fire
bus.Publish(ctx, "approved", nil)                            // resumes → reminder cancelled
fc.Advance(30 * time.Minute)                                 // no further nudge
```

**At runtime:** three intervals fire three `nudge` reminders while the instance sits at the
catch; publishing `"approved"` resumes it to completion and cancels the recurring timer, so a
final clock advance fires nothing — proof the catch-event reminder arms, fires, and cancels.
→ [`examples/scenarios/catch_event_reminder`](examples/scenarios/catch_event_reminder)

> The tour above is a curated subset. Other runnable scenarios under
> [`examples/scenarios/`](examples/scenarios) include `attribute_authz` (ABAC + Casbin
> eligibility) and `admin_monitoring` (instance listing, incident resolve, dead-letter
> redrive).

---

## Persistence backends

> **Production checklist:** see [`docs/production-checklist.md`](docs/production-checklist.md)
> for connection pool sizing, statement-timeout guidance per dialect, and the opt-in-but-unsafe
> MUST-DOs (call-link lease, `WithHistoryCap`, pruning cron) — each with its concrete failure
> mode. Use `persistence.WarnUnsafeConfig` to get a startup-time reminder in your logs.

The engine supports three SQL backends, all using the same neutral store and migration set:

| Backend | Facade constructors | Notes |
|---|---|---|
| **PostgreSQL 17** | `persistence.OpenPostgres` / `persistence.MigratePostgres` | Production default; LISTEN/NOTIFY relay, advisory-lock ownership. |
| **MySQL 8.0+** | `persistence.OpenMySQL` / `persistence.MigrateMySQL` | Production-grade; poll-only relay (no LISTEN/NOTIFY), advisory-lock ownership. |
| **SQLite** | `persistence.OpenSQLite` / `persistence.MigrateSQLite` | Single-node / test / embedded; WAL mode, single-writer. No distributed advisory lock (`persistence.NewSQLiteAdvisoryLockOwnership` is fail-loud — every acquire returns `dialect.ErrUnsupported`) and no LISTEN/NOTIFY (relay is poll-only). Use Postgres or MySQL for multi-replica. |

### SQLite quickstart

```go
import _ "modernc.org/sqlite"

db, _ := sql.Open("sqlite", "file:app.db?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
db.SetMaxOpenConns(1) // single-writer serialisation (required)
persistence.MigrateSQLite(ctx, db)
store, _ := persistence.OpenSQLite(ctx, db)
runner, _ := runtime.NewProcessDriver(
    runtime.WithActionCatalog(cat),
    runtime.WithInstanceStore(store),
)
```

See [`examples/sqlite_wiring/`](examples/sqlite_wiring/) for the complete reference wiring.

---

## Production wiring: health, readiness & graceful shutdown

The library is lifecycle-neutral — the consumer owns the process. Three pieces make a
clean production embedding ergonomic (ADR-0054):

- **Health & readiness handlers.** `stdlib.MountHealth(mux, checks...)` (or the gin/fiber
  equivalent `MountHealth`) mounts `GET /healthz` (liveness — always `200`) and
  `GET /readyz` (readiness — runs every registered `httpcore.HealthCheck` and returns
  `200`, or `503` with a per-check JSON body naming the failure) alongside the workflow
  routes. Wire readiness to Postgres with the ready-made `persistence.NewPingCheck(pool)`
  (a `pool.Ping` probe), or register an inline check with `httpcore.HealthCheckFunc(name, fn)`.

- **One-call graceful shutdown.** `runtime.ShutdownGroup` aggregates your resource
  holders — the `scheduling.Scheduler` (`io.Closer`), the advisory-lock ownership
  closer, the eventing closer, the `pgxpool.Pool` — and `Shutdown(ctx)` closes them in
  reverse registration order, running every one even if an earlier fails and joining the
  errors with `errors.Join`. The background `Run(ctx)` workers (relay, call notifier,
  chainer) keep their idiomatic stop story: you start their goroutines and stop them by
  cancelling the context you passed.

- **Single-replica caching guard.** Pairing `kernel.NewCachingInstanceStore` with
  `kernel.AlwaysOwn` is single-writer / single-replica **only** — across replicas it is
  a stale-read footgun. `NewCachingInstanceStore` now logs a one-time warning when
  constructed with `AlwaysOwn`; for multi-replica deployments use
  `persistence.NewAdvisoryLockOwnership` so only the owning replica caches an instance.

The full assembly — engine + scheduler + relay + mounted HTTP and health routes +
`signal.NotifyContext` → cancel workers → `http.Server.Shutdown` → `ShutdownGroup.Shutdown`
— is in [`examples/production_wiring`](examples/production_wiring).

### Database pool (pgxpool) tuning

The Postgres-backed stores, relay, advisory-lock ownership, and timer store all take a
`*pgxpool.Pool` that **you** construct and own. The library never creates or tunes the
pool — configure it before passing it in, and close it last (via `ShutdownGroup`):

```go
cfg, _ := pgxpool.ParseConfig(dsn)
cfg.MaxConns = 20             // cap to your Postgres max_connections budget across replicas
cfg.MinConns = 2             // keep a warm floor so the relay/hot paths don't cold-start
cfg.MaxConnLifetime = time.Hour
cfg.MaxConnIdleTime = 30 * time.Minute
cfg.HealthCheckPeriod = time.Minute
pool, _ := pgxpool.NewWithConfig(ctx, cfg)
```

Sizing guidance:

- **`MaxConns`** is the dominant knob. Size it so `MaxConns × replicas + headroom` stays
  under the server's `max_connections`. The relay's listen/poll loop, the timer
  scheduler, and request handlers all draw from this one pool — undersizing it serializes
  the hot path; oversizing it can exhaust Postgres. Start around 10–25 per replica and
  tune from the `pgxpool` stats and your DB connection metrics.
- **`MinConns`** keeps a warm floor so latency-sensitive paths (relay drain, instance
  reads) don't pay connection setup on the first request after idle.
- **`MaxConnLifetime` / `MaxConnIdleTime`** bound how long a connection lives, which plays
  well with rolling Postgres failovers and PgBouncer in front of the pool.

If you front Postgres with **PgBouncer in transaction-pooling mode**, the engine's use of
`LISTEN/NOTIFY` (the relay's low-latency wake path) needs a session-pooled connection or a
direct connection — transaction pooling drops `LISTEN`. The relay degrades gracefully to
its poll-interval fallback if notifications are lost, but you lose the low-latency wake.

### Single-tenant scope

**The engine is single-tenant. It has no built-in tenant isolation.** All instances,
definitions, tasks, timers, and outbox events share one logical namespace; there is no
tenant column, no per-tenant authorization scoping, and no row-level isolation. A
multi-tenant consumer **must enforce isolation itself** — e.g. one engine instance (and
one schema/database) per tenant, or a tenant guard in front of every API call that filters
by a tenant id the consumer threads through `authz.Actor` attributes and its own queries.
Do not assume the authorization layer partitions data by tenant; it evaluates
role/resource/attribute rules but does not invent a tenant boundary.

### Secrets and sensitive variables

Process-instance **variables** and **human-task variables** (`HumanTask.Vars`) frequently
carry sensitive data (PII, tokens, payment details). Treat them as secrets:

- **The engine does not log variables in cleartext.** The core state machine reads the
  wall clock and emits no logs; the `runtime.ProcessDriver`'s `slog` records identify instances,
  nodes, actions, and outcomes — they do **not** dump the variable map. This is a
  deliberate invariant; keep it that way if you extend the logging.
- **Redact in your own resolvers and actions.** When you write an `action.Action`,
  a human-task resolver, or a `SuccessorPolicy`, do not `slog`/print the raw input/output
  maps. Log only the keys you need, or a redacted view — never the whole map. The same
  applies to error messages: don't interpolate a variable value into an error string that
  flows back through the HTTP transport.
- **Encrypt at rest if required.** Variables persist to Postgres as JSONB. If your
  compliance posture requires encryption-at-rest for these fields, encrypt the values in
  your action layer before they enter the engine (the engine treats them as opaque
  `any`), or rely on database/disk-level encryption.

See [`STABILITY.md`](STABILITY.md) for the module's versioning and stability policy.

---

## License

License: TBD by the project owner. No `LICENSE` file is present in this repository.

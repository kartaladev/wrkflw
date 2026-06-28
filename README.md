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
	// Fluent per-node-type builder methods (AddStartEvent, AddServiceTask, …)
	// are the preferred form; each mirrors its New<Kind> constructor and appends
	// the node. The generic .Add(node) still works for dynamically-built nodes.
	def, err := model.NewDefinition("order-fulfillment", 1).
		AddStartEvent("start").
		AddServiceTask("charge",
			model.WithActionName("charge-card"),
			model.WithCompensation("refund-card"),
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
score := action.Func(func(_ context.Context, in map[string]any) (map[string]any, error) {
    return map[string]any{"score": 42}, nil
})

def, _ := model.NewDefinition("loan", 1).
    RegisterAction("score", score).                   // def-scoped, by name
    Add(model.NewStartEvent("start")).
    Add(model.NewServiceTask("risk",
        model.WithActionName("score"),                // resolves scoped → global
    )).
    Add(model.NewServiceTask("notify",
        model.WithActionFunc(func(_ context.Context, in map[string]any) (map[string]any, error) {
            return in, nil                            // node-local inline
        }),
    )).
    Add(model.NewServiceTask("archive")).             // default-by-id → looks up "archive"
    Add(model.NewEndEvent("end")).
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
		Add(model.NewServiceTask("charge", model.WithActionName("charge-card"))).
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
| Go builder | `model.NewDefinition(...).AddServiceTask(...).Connect(...).Build()` | Preferred; compile-time safe. Fluent `Add<Kind>` methods (one per node kind) mirror the `New<Kind>` constructors; the generic `.Add(node)` remains for dynamic nodes. |
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
| `scheduling` | Façade over the timer/deadline scheduler (gocron behind the abstraction). |
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

Timers and deadlines are driven by gocron (behind the `scheduling` abstraction).

- **Intermediate timer events** — pause execution for an ISO-8601 duration before continuing.
- **Deadlines** — if a human task (or any wait node) is not resolved within the deadline
  duration, the engine takes an alternative sequence flow and/or runs a recovery action.
- **In-wait reminder actions** — service actions executed on a repeating interval _during_
  a wait period (e.g. send a reminder email every 24 h while waiting for approval).

Configure timers on nodes:

```go
// UserTask with a 3-day deadline and daily reminders:
model.NewUserTask("approve", []string{"manager"},
    model.WithDeadline("P3D", "escalate-flow", "notify-manager"),
    model.WithReminder("P1D", "send-reminder"),
)
```

---

## Compensation, resilience, and observability

### Compensation

Attach an optional compensation action to any activity. When the engine rolls back the
process (e.g. after a downstream failure), it runs compensation actions in reverse order.

```go
model.NewServiceTask("charge",
    model.WithActionName("charge-card"),
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

## Process-instance chaining

Automatically start a new, **independent** top-level instance when another reaches a
terminal state (completed, failed, or terminated) — e.g. when an `approval` process
completes, start a `fulfillment` process seeded with its result. The predecessor fully
ends and releases its resources; the successor is a fresh root instance that outlives it.
This is sequential chaining of independent instances, driven off the durable terminal
outbox events — **not** the parent→child nesting of a call activity.

A `SuccessorPolicy` callback decides the successor for each terminal outcome; the
`runtime.Chainer` records the lineage hop and starts the successor with a deterministic
id, so a redelivered event is a clean no-op (exactly-once effect under at-least-once
delivery).

```go
policy := func(_ context.Context, ev runtime.ChainEvent) (runtime.SuccessorDecision, bool) {
    if ev.Outcome != runtime.OutcomeCompleted {
        return runtime.SuccessorDecision{}, false // end the chain
    }
    return runtime.SuccessorDecision{Def: fulfillmentDef, Vars: ev.Result}, true
}
chainer := runtime.NewChainer(runner, policy, runtime.WithChainLinks(links))

// Drive it from the broker: mount eventing.NewChainHandler(chainer) on your own
// message.Router, or run the turnkey wrapper that subscribes the terminal topics:
go eventing.NewChainerRunner(chainer).Run(ctx, subscriber)
```

Terminal outbox events are **status-accurate**: completed → `instance.completed`, failed →
`instance.failed`, terminated → `instance.terminated`. Chaining lineage is durable and
queryable (`runtime.ChainLinkStore`, in-memory or Postgres via
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

## Node types

A process definition is a graph of **nodes** connected by **sequence flows**. Every
node is a value built with a `model.New*` constructor and implements the `model.Node`
interface (`Kind() NodeKind`, `ID() string`, `Name() string`). Never construct the
struct types directly — use the constructors. There are 19 node kinds, grouped below.

All examples in this section are excerpts; the full, compiling programs they are
drawn from live under [`examples/scenarios/`](examples/scenarios).

### Shared activity options

Every **activity** constructor (`NewServiceTask`, `NewUserTask`, `NewReceiveTask`,
`NewSendTask`, `NewBusinessRuleTask`, `NewSubProcess`, `NewCallActivity`) accepts the
same set of functional options:

| Option | Configures |
|---|---|
| `model.WithName(string)` | Human-readable display name. |
| `model.WithRetryPolicy(*model.RetryPolicy)` | Per-node retry policy (see below). |
| `model.WithRecoveryFlow(flowID string)` | Sequence flow taken when retries are exhausted. |
| `model.WithCompensation(actionName string)` | Service action invoked on rollback (reverse order). |
| `model.WithCancelHandler(actionName string)` | Service action run when the node is interrupted. |
| `model.WithDeadline(duration, flowID, actionName string)` | On deadline breach: take `flowID` and/or run `actionName`. |
| `model.WithReminder(every, actionName string)` | Run `actionName` repeatedly *during* the wait. |

Two options are **compile-enforced** to a single constructor:

- `model.WithEligibilityExpr(expr string)` — **`NewUserTask` only**. Attribute-based
  eligibility predicate (evaluated by authz over `vars[...]`). Passing it to any other
  constructor is a compile error.
- `model.WithCorrelationKey(key string)` — **`NewReceiveTask` only**. Correlation-key
  expression. Passing it elsewhere is a compile error.

> **Durations are expr-lang expressions parsed by Go's `time.ParseDuration`.** Write
> them as **quoted Go-duration strings** — `` `"1h"` ``, `` `"30m"` ``, `` `"45s"` `` —
> not ISO-8601. This applies to `WithBoundaryTimer`, `WithTimerDuration`, `WithDeadline`,
> `WithReminder`, `WithStartTimer`, and `WithICEDeadline`/`WithICEReminder`.

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
| **StartEvent** | Entry point of a process (or the trigger of an EventSubProcess). | `model.NewStartEvent(id string, opts ...) Node` |
| **EndEvent** | Normal completion of one branch. | `model.NewEndEvent(id string, name ...string) Node` |
| **TerminateEndEvent** | Terminates the whole instance, including parallel branches. | `model.NewTerminateEndEvent(id string, name ...string) Node` |
| **ErrorEndEvent** | Throws an error code, caught by a boundary error event. | `model.NewErrorEndEvent(id, errorCode string, name ...string) Node` |

`NewStartEvent` options (only meaningful when the start is an EventSubProcess trigger):
`WithName(string)`, `WithStartSignal(name)`, `WithStartMessage(msg, key)`,
`WithStartTimer(dur)`. An empty `errorCode` on `NewErrorEndEvent` throws an anonymous
(catch-all) error.

```go
model.NewStartEvent("start")
model.NewEndEvent("end", "Order complete")
model.NewTerminateEndEvent("kill-all")
model.NewErrorEndEvent("insufficient-funds", "FUNDS_ERROR")
```

### Activities

| Node | What it does | Constructor |
|---|---|---|
| **ServiceTask** | Runs a named service action. | `model.NewServiceTask(id string, opts ...) Node` |
| **UserTask** | Waits for a human to complete a work item. | `model.NewUserTask(id string, roles []string, opts ...) Node` |
| **ReceiveTask** | Waits for an inbound correlated message. | `model.NewReceiveTask(id, messageName string, opts ...) Node` |
| **SendTask** | Sends an outbound message. | `model.NewSendTask(id, messageName string, opts ...) Node` |
| **BusinessRuleTask** | Runs a named business-rule action. | `model.NewBusinessRuleTask(id string, opts ...) Node` |
| **SubProcess** | Runs an *embedded* nested definition as a scope. | `model.NewSubProcess(id string, sub *model.ProcessDefinition, opts ...) Node` |
| **CallActivity** | Calls a *separate* top-level definition by name. | `model.NewCallActivity(id, defRef string, opts ...) Node` |
| **EventSubProcess** | Event-triggered subprocess rooted at an event start. | `model.NewEventSubProcess(id string, sub *model.ProcessDefinition, opts ...) Node` |

All activity constructors take the shared activity options above. `NewUserTask` also
takes `WithEligibilityExpr`; `NewReceiveTask` also takes `WithCorrelationKey`.
`NewEventSubProcess` takes `WithName(string)` and `WithESPNonInterrupting()` (default
is interrupting) — its nested start event carries the trigger.

```go
model.NewServiceTask("charge",
    model.WithActionName("charge-card"),
    model.WithCompensation("refund-card"),
    model.WithRetryPolicy(&retry),
)
model.NewUserTask("approve", []string{"manager"},
    model.WithDeadline(`"3h"`, "escalate-flow", "notify-manager"),
    model.WithEligibilityExpr(`vars["region"] == "EU"`),
)
model.NewReceiveTask("await-payment", "payment-received",
    model.WithCorrelationKey("orderId"),
)
model.NewSubProcess("reserve-hotel", hotelDef)
model.NewCallActivity("credit-check", "credit-check")        // resolved via a DefinitionRegistry
model.NewEventSubProcess("on-cancel", cancelHandlerDef, model.WithESPNonInterrupting())
```

### SendTask delivery (transactional outbox)

A BPMN `SendTask` emits its outbound message as a `message.<MessageName>` event written
into the same `wrkflw_outbox` (and the same transaction) as the state commit, then relayed
at-least-once by the outbox relay — no `MessageSink` wiring, no stranding window (ADR-0067).

The event payload is `{"messageName", "correlationKey", "variables"}`, with `instance_id`
and `definition_ref` as message metadata. Consume it like any other outbox topic. To deliver
a message intra-engine (resume a parked `ReceiveTask`), mount `eventing.NewMessageHandler`
on your message router and route to `Runner.DeliverMessage`:

```go
handler := eventing.NewMessageHandler(func(ctx context.Context, name, key string, vars map[string]any) error {
    return runner.DeliverMessage(ctx, receiverDef, name, key, vars)
})
```

`DeliverMessage`'s waiter index is in-memory per `Runner`, so intra-engine correlation works
within one process; for cross-process correlation, subscribe `message.*` in your own consumer.

### Intermediate and boundary events

| Node | What it does | Constructor |
|---|---|---|
| **IntermediateCatchEvent** | Pauses until a timer, signal, or message arrives. | `model.NewIntermediateCatchEvent(id string, opts ...) Node` |
| **IntermediateThrowEvent** | Throws a signal or triggers compensation. | `model.NewIntermediateThrowEvent(id string, opts ...) Node` |
| **BoundaryEvent** | Event attached to an activity; fires on timer/signal/error. | `model.NewBoundaryEvent(id, attachedTo string, opts ...) Node` |

`NewIntermediateCatchEvent` options: `WithTimerDuration(dur)`, `WithSignalName(name)`,
`WithMessageNameAndKey(msg, key)`, `WithICEDeadline(dur, flow, action)`,
`WithICEReminder(every, action)`, `WithName(string)`.
`NewIntermediateThrowEvent` options: `WithThrowSignal(name)`,
`WithCompensateRef(nodeID)` (empty = scope-wide compensation), `WithThrowName(name)`.
`NewBoundaryEvent` options: `WithBoundaryTimer(dur)`, `WithBoundarySignal(name)`,
`WithBoundaryMessage(msg, key)`, `WithBoundaryErrorCode(code)` (empty = catch-all),
`BoundaryNonInterrupting()` (default interrupting), `WithName(string)`.

> **Boundary events:** timer, signal, error, and message boundaries are all armed and
> fired by the engine (message boundaries since ADR-0053).

```go
model.NewIntermediateCatchEvent("wait-1h", model.WithTimerDuration(`"1h"`))
model.NewIntermediateThrowEvent("compensate", model.WithCompensateRef("reserve-hotel"))
model.NewBoundaryEvent("review-timeout", "review", model.WithBoundaryTimer(`"1h"`))
```

### Gateways

Gateways take no options beyond an optional name — their behaviour is determined by
the number of incoming and outgoing flows and (for conditional gateways) the flow
conditions.

| Node | What it does | Constructor |
|---|---|---|
| **ExclusiveGateway** | XOR. Split: first matching flow (or the default). Merge: pass-through. | `model.NewExclusiveGateway(id string, name ...string) Node` |
| **ParallelGateway** | AND. Split: activate all outgoing. Join: wait for all incoming. | `model.NewParallelGateway(id string, name ...string) Node` |
| **InclusiveGateway** | OR. Split: every matching flow. Join: wait for the active matching branches. | `model.NewInclusiveGateway(id string, name ...string) Node` |
| **EventBasedGateway** | Race: routes to whichever following catch event fires first. | `model.NewEventBasedGateway(id string, name ...string) Node` |

```go
model.NewExclusiveGateway("route")
model.NewParallelGateway("fork")
model.NewInclusiveGateway("split")
model.NewEventBasedGateway("await")
```

### DefinitionBuilder

Assemble nodes and flows with the fluent builder, then `Build()` (which validates):

```go
def, err := model.NewDefinition("order-fulfillment", 1).
    Add(model.NewStartEvent("start")).
    Add(model.NewExclusiveGateway("route")).
    Add(model.NewServiceTask("manual-review", model.WithActionName("manual-review"))).
    Add(model.NewServiceTask("auto-approve", model.WithActionName("auto-approve"))).
    Add(model.NewServiceTask("reject", model.WithActionName("reject"))).
    Add(model.NewEndEvent("end")).
    Connect("start", "route").
    Connect("route", "manual-review", model.WithCondition("amount > 50000")).
    Connect("route", "auto-approve", model.WithCondition("amount <= 50000")).
    Connect("route", "reject", model.AsDefault()).
    Connect("manual-review", "end").
    Connect("auto-approve", "end").
    Connect("reject", "end").
    CancelActions("send-cancellation-email"). // best-effort on instance cancel
    Build()
```

| Builder method | Purpose |
|---|---|
| `model.NewDefinition(id, version)` | Start a builder. |
| `.Add(node)` | Append a node. |
| `.Connect(fromID, toID, opts...)` | Add a sequence flow (ID auto = `"from->to"`). |
| `.CancelActions(names...)` | Best-effort actions run when the instance is cancelled. |
| `.Build()` | Assemble + validate; returns `(*model.ProcessDefinition, error)`. |

Flow options for `.Connect`: `model.WithFlowID(id)`, `model.WithCondition(expr)`,
`model.AsDefault()`.

> **Flow conditions use bare variable keys.** A condition is evaluated by expr-lang
> directly against the process-variable map, so write `model.WithCondition("amount > 100")`
> — **not** `vars.amount`. (Only `WithEligibilityExpr` on a UserTask uses the
> `vars[...]` form, because it is evaluated by authz.)

### YAML authoring

Definitions can also be authored in YAML and loaded with `model.ParseYAML(data)` or
`model.LoadYAML(r)`. Each node carries a `kind` discriminator (lowerCamelCase):

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
def, _ := model.NewDefinition("order-fulfillment", 1).
    Add(model.NewStartEvent("start")).
    Add(model.NewParallelGateway("fork")).
    Add(model.NewServiceTask("pick-items", model.WithActionName("pick-items"))).
    Add(model.NewServiceTask("charge-card", model.WithActionName("charge-card"))).
    Add(model.NewParallelGateway("join")).
    Add(model.NewServiceTask("ship", model.WithActionName("ship"))).
    Add(model.NewEndEvent("end")).
    Connect("start", "fork").
    Connect("fork", "pick-items").
    Connect("fork", "charge-card").
    Connect("pick-items", "join").
    Connect("charge-card", "join").
    Connect("join", "ship").
    Connect("ship", "end").
    Build()
```

**At runtime:** `r.Run` drives `pick-items` and `charge-card`, then the join releases a
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
Connect("route", "manual-review", model.WithCondition("amount > 50000")).
Connect("route", "auto-approve",  model.WithCondition("amount <= 50000")).
Connect("route", "reject",        model.AsDefault()).
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
Add(model.NewUserTask("review", []string{"reviewer"},
    model.WithDeadline(`"1h"`, "review-overdue", "notify-overdue"))). // fire-once breach action
Add(model.NewServiceTask("escalate", model.WithActionName("reassign"))).
// ...
Connect("review", "approved-end").                                  // normal path
Connect("review", "escalate", model.WithFlowID("review-overdue")).  // deadline flow
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
→ [`examples/scenarios/boundary_timer`](examples/scenarios/boundary_timer)

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
Add(model.NewServiceTask("book", model.WithActionName("book"), model.WithCompensation("cancel-booking"))).
Add(model.NewServiceTask("pay", model.WithActionName("pay"), model.WithCompensation("refund"))).
Add(model.NewServiceTask("ship", model.WithActionName("ship"))).
Add(model.NewBoundaryEvent("ship-err", "ship", model.WithBoundaryErrorCode(""))).
// ... after the forward run completes via the boundary path:
trg := engine.NewCompensateRequested(clk.Now(), "") // "" = full rollback
final, _ := r.Deliver(ctx, def, instanceID, trg)
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
driven through the `humantask` ports and `runtime.TaskService`.

```
start → approve[UserTask, roles: manager] → end
```

```go
r := runtime.NewRunner(nil, clk, runtime.NewMemStore(),
    runtime.WithHumanTasks(resolver, taskStore, authz.RoleAuthorizer{}))

parked, _ := r.Run(ctx, def, instanceID, map[string]any{"amount": 4200}) // parks at "approve"

claimable, _ := taskStore.ClaimableBy(ctx, manager)        // discover tasks
svc := runtime.NewTaskService(taskStore, az, clk)

claimTrg, _ := svc.Claim(ctx, claimable[0].TaskToken, manager)
r.Deliver(ctx, def, instanceID, claimTrg)                  // → Claimed

completeTrg, _ := svc.Complete(ctx, claimable[0].TaskToken, manager, map[string]any{"approved": true})
final, _ := r.Deliver(ctx, def, instanceID, completeTrg)   // → Completed
```

**At runtime:** `Run` returns with `StatusRunning` (parked). `ClaimableBy` lists the
task for the manager actor; `Claim` then `Complete` (each followed by `r.Deliver`) drive
the instance to `StatusCompleted`, merging the completion output (`approved`) into the
variables. See `runtime/human_example_test.go` for the authoritative end-to-end test
(including attribute-based eligibility and deadline escalation).
→ [`examples/scenarios/human_task_approval`](examples/scenarios/human_task_approval)

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
hotel, _ := model.NewDefinition("hotel-reservation", 1). /* ... */ .Build()
def, _  := model.NewDefinition("travel-booking", 1).
    Add(model.NewSubProcess("reserve-hotel", hotel)).
    /* ... */ .Build()

// Call activity (separate definition resolved by name):
reg := runtime.NewMapDefinitionRegistry(map[string]*model.ProcessDefinition{"credit-check": child})
r   := runtime.NewRunner(cat, clock.System(), runtime.NewMemStore(), runtime.WithDefinitions(reg))
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
Connect("split", "notify-risk",   model.WithCondition("score < 600")).
Connect("split", "senior-review", model.WithCondition("amount > 10000")).
Connect("split", "fraud-check",   model.WithCondition("flagged == true")).
```

**At runtime:** with `score=580`, `amount=25000`, `flagged=false`, the split activates
`notify-risk` and `senior-review` but skips `fraud-check`; the join waits for those two
branches only, then continues to `end`. Contrast with the exclusive gateway (exactly
one branch) and the parallel gateway (all branches unconditionally).
→ [`examples/scenarios/inclusive_gateway`](examples/scenarios/inclusive_gateway)

---

## Production wiring: health, readiness & graceful shutdown

The library is lifecycle-neutral — the consumer owns the process. Three pieces make a
clean production embedding ergonomic (ADR-0054):

- **Health & readiness handlers.** `rest.NewHealthHandler(checks...)` returns an
  `http.Handler` you mount alongside the workflow routes. It exposes `GET /healthz`
  (liveness — always `200`, runs no checks) and `GET /readyz` (readiness — runs every
  registered `rest.HealthCheck` and returns `200`, or `503` with a per-check JSON body
  naming the failure). Wire readiness to Postgres with the ready-made
  `persistence.NewPingCheck(pool)` (a `pool.Ping` probe), or register an inline check
  with `rest.HealthCheckFunc(name, fn)`.

- **One-call graceful shutdown.** `runtime.ShutdownGroup` aggregates your resource
  holders — the `scheduling.Scheduler` (`io.Closer`), the advisory-lock ownership
  closer, the eventing closer, the `pgxpool.Pool` — and `Shutdown(ctx)` closes them in
  reverse registration order, running every one even if an earlier fails and joining the
  errors with `errors.Join`. The background `Run(ctx)` workers (relay, call notifier,
  chainer) keep their idiomatic stop story: you start their goroutines and stop them by
  cancelling the context you passed.

- **Single-replica caching guard.** Pairing `runtime.NewCachingStore` with
  `runtime.AlwaysOwn` is single-writer / single-replica **only** — across replicas it is
  a stale-read footgun. `NewCachingStore` now logs a one-time warning when constructed
  with `AlwaysOwn`; for multi-replica deployments use
  `persistence.NewAdvisoryLockOwnership` so only the owning replica caches an instance.

The full assembly — engine + scheduler + relay + mounted REST and health routes +
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
  wall clock and emits no logs; the `runtime.Runner`'s `slog` records identify instances,
  nodes, actions, and outcomes — they do **not** dump the variable map. This is a
  deliberate invariant; keep it that way if you extend the logging.
- **Redact in your own resolvers and actions.** When you write a `service.ServiceAction`,
  a human-task resolver, or a `SuccessorPolicy`, do not `slog`/print the raw input/output
  maps. Log only the keys you need, or a redacted view — never the whole map. The same
  applies to error messages: don't interpolate a variable value into an error string that
  flows back through the REST/gRPC transports.
- **Encrypt at rest if required.** Variables persist to Postgres as JSONB. If your
  compliance posture requires encryption-at-rest for these fields, encrypt the values in
  your action layer before they enter the engine (the engine treats them as opaque
  `any`), or rely on database/disk-level encryption.

See [`STABILITY.md`](STABILITY.md) for the module's versioning and stability policy.

---

## License

License: TBD by the project owner. No `LICENSE` file is present in this repository.

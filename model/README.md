# model

Package `model` is the **process-definition authoring layer** for
`github.com/zakyalvan/krtlwrkflw`. It is pure data plus validation — it imports
only the Go standard library. Consumers use it to describe the shape of a
workflow; the `runtime` and `engine` packages execute it.

## Contents

1. [Overview](#overview)
2. [The Node interface and concrete types](#the-node-interface-and-concrete-types)
3. [Constructors and options](#constructors-and-options)
4. [DefinitionBuilder](#definitionbuilder)
5. [RetryPolicy](#retrypolicy)
6. [Validation](#validation)
7. [Serialization / YAML](#serialization--yaml)
8. [Authoring forms](#authoring-forms)

---

## Overview

`model` holds the in-memory representation of a **process definition** — the
reusable template from which process instances are created. Concepts mirror BPMN
(tasks, gateways, events, sequence flows) but this package is **not BPMN-compatible**
and makes no attempt to load or round-trip arbitrary BPMN2 XML documents.

Key design properties:

- **Pure data + validation.** `ProcessDefinition` and every `Node` type are plain
  Go values. No I/O, no goroutines, no external dependencies.
- **One concrete type per node kind.** `Node` is an interface; each kind has its
  own struct (`ServiceTask`, `UserTask`, `ExclusiveGateway`, …). Construct them
  with the `New*` constructors — never construct the structs directly.
- **Three authoring forms.** Go constructors + `DefinitionBuilder` (preferred),
  YAML, or JSON. All three paths call `Validate` before returning a definition.

---

## The Node interface and concrete types

```go
type Node interface {
    Kind() NodeKind
    ID()   string
    Name() string
}
```

Every node satisfies this interface. The 19 concrete kinds are grouped below.

### Events

| Kind constant | Concrete type | Constructor |
|---|---|---|
| `KindStartEvent` | `StartEvent` | `NewStartEvent(id, opts...)` |
| `KindEndEvent` | `EndEvent` | `NewEndEvent(id, name...)` |
| `KindTerminateEndEvent` | `TerminateEndEvent` | `NewTerminateEndEvent(id, name...)` |
| `KindErrorEndEvent` | `ErrorEndEvent` | `NewErrorEndEvent(id, errorCode, name...)` |

### Activities

| Kind constant | Concrete type | Constructor |
|---|---|---|
| `KindServiceTask` | `ServiceTask` | `NewServiceTask(id, action, opts...)` |
| `KindUserTask` | `UserTask` | `NewUserTask(id, roles, opts...)` |
| `KindReceiveTask` | `ReceiveTask` | `NewReceiveTask(id, messageName, opts...)` |
| `KindSendTask` | `SendTask` | `NewSendTask(id, messageName, opts...)` |
| `KindBusinessRuleTask` | `BusinessRuleTask` | `NewBusinessRuleTask(id, action, opts...)` |
| `KindSubProcess` | `SubProcess` | `NewSubProcess(id, *ProcessDefinition, opts...)` |
| `KindCallActivity` | `CallActivity` | `NewCallActivity(id, defRef, opts...)` |
| `KindEventSubProcess` | `EventSubProcess` | `NewEventSubProcess(id, *ProcessDefinition, opts...)` |

### Intermediate and boundary events

| Kind constant | Concrete type | Constructor |
|---|---|---|
| `KindIntermediateCatchEvent` | `IntermediateCatchEvent` | `NewIntermediateCatchEvent(id, opts...)` |
| `KindIntermediateThrowEvent` | `IntermediateThrowEvent` | `NewIntermediateThrowEvent(id, opts...)` |
| `KindBoundaryEvent` | `BoundaryEvent` | `NewBoundaryEvent(id, attachedTo, opts...)` |

### Gateways

Gateways carry no options beyond an optional name; their routing behaviour
emerges entirely from the number and conditions of their incoming/outgoing flows.

| Kind constant | Concrete type | Constructor | Routing rule |
|---|---|---|---|
| `KindExclusiveGateway` | `ExclusiveGateway` | `NewExclusiveGateway(id, name...)` | XOR — first matching condition (or default) |
| `KindParallelGateway` | `ParallelGateway` | `NewParallelGateway(id, name...)` | AND — activate all / wait for all |
| `KindInclusiveGateway` | `InclusiveGateway` | `NewInclusiveGateway(id, name...)` | OR — all matching conditions; join waits for active branches |
| `KindEventBasedGateway` | `EventBasedGateway` | `NewEventBasedGateway(id, name...)` | Race — routes to whichever `IntermediateCatchEvent` fires first |

---

## Constructors and options

### Shared activity options

The following options work on **all** activity constructors (`NewServiceTask`,
`NewUserTask`, `NewReceiveTask`, `NewSendTask`, `NewBusinessRuleTask`,
`NewSubProcess`, `NewCallActivity`):

| Option | Effect |
|---|---|
| `WithName(name string)` | Sets the display name |
| `WithRetryPolicy(p *RetryPolicy)` | Per-node retry policy (nil = use runtime default) |
| `WithRecoveryFlow(flowID string)` | Flow to take when retries are exhausted |
| `WithCompensation(actionName string)` | Service-action name invoked during rollback |
| `WithCancelHandler(actionName string)` | Service-action invoked when the node is interrupted |
| `WithSLA(dur, flowID, actionName string)` | SLA deadline (ISO-8601 duration), escape flow, and/or action on breach |
| `WithReminder(every, actionName string)` | Periodic reminder action (ISO-8601 interval) fired while the node is active |

### Kind-specific options (compile-enforced)

These options are accepted **only** by the constructor whose option type they
satisfy. Passing them to a different constructor is a compile-time error.

**`NewUserTask` only:**

```go
WithEligibilityExpr(expr string) // fine-grained eligibility predicate (authz)
```

**`NewReceiveTask` only:**

```go
WithCorrelationKey(key string) // expr evaluated at runtime to derive the correlation key
```

**`NewStartEvent` only (for EventSubProcess triggers):**

```go
WithStartSignal(name string)
WithStartMessage(msg, key string)
WithStartTimer(durISO8601 string)
```

**`NewEventSubProcess` only:**

```go
WithESPNonInterrupting() // run alongside enclosing scope; default is interrupting
```

**`NewIntermediateCatchEvent`:**

```go
WithTimerDuration(dur string)
WithSignalName(name string)
WithMessageNameAndKey(msg, key string)
WithICESLA(dur, flowID, action string)
WithICEReminder(every, action string)
WithName(name string)
```

**`NewIntermediateThrowEvent`:**

```go
WithThrowSignal(name string)
WithCompensateRef(nodeID string) // empty = scope-wide compensation
WithThrowName(name string)
```

**`NewBoundaryEvent`:**

```go
WithBoundaryTimer(dur string)
WithBoundarySignal(name string)
WithBoundaryMessage(msg, key string)
WithBoundaryErrorCode(code string) // empty = catch-all
BoundaryNonInterrupting()          // default is interrupting
WithName(name string)
```

> **Note:** Message boundary events are not yet armed by the engine. Timer,
> signal, and error boundary events work. Message is accepted by the model but
> has no runtime effect in the current release.

### Example — service task with compensation and SLA

```go
task := model.NewServiceTask("charge", "charge-card",
    model.WithName("Charge Card"),
    model.WithCompensation("refund-card"),
    model.WithSLA("PT2H", "sla-breach-flow", "notify-ops"),
    model.WithRetryPolicy(&model.RetryPolicy{
        MaxAttempts:     5,
        InitialInterval: 2 * time.Second,
        BackoffCoef:     2.0,
    }),
)
```

### Example — user task with eligibility expression

```go
task := model.NewUserTask("approve", []string{"manager"},
    model.WithName("Approve Order"),
    model.WithEligibilityExpr(`vars["region"] == actor.Region`),
    model.WithReminder("PT24H", "send-reminder-email"),
)
```

---

## DefinitionBuilder

`DefinitionBuilder` wires nodes and flows into a `ProcessDefinition` and calls
`Validate` on `Build`.

```go
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
```

**Method summary:**

| Method | Description |
|---|---|
| `NewDefinition(id string, version int) *DefinitionBuilder` | Start a new builder |
| `.Add(n Node) *DefinitionBuilder` | Append a node |
| `.Connect(fromID, toID string, opts ...FlowOption) *DefinitionBuilder` | Add a directed sequence flow |
| `.CancelActions(names ...string) *DefinitionBuilder` | Best-effort actions on instance cancel |
| `.Build() (*ProcessDefinition, error)` | Assemble and validate |

**`FlowOption` values:**

| Option | Effect |
|---|---|
| `WithFlowID(id string)` | Override the auto-generated flow ID (default: `"fromID->toID"`) |
| `WithCondition(expr string)` | Routing expression (exclusive/inclusive gateways) |
| `AsDefault()` | Mark as the exclusive-gateway default flow |

### Flow condition expressions

Conditions are evaluated by [`expr-lang/expr`](https://github.com/expr-lang/expr)
against the process-instance variable map using **bare variable keys**:

```go
// variables: {"amount": 150}
model.WithCondition("amount > 100")  // correct
// NOT: "vars.amount > 100"
```

The `vars[...]` form applies only to `WithEligibilityExpr`, which is evaluated
by the authz layer.

### Exclusive gateway example

```go
model.NewDefinition("loan", 1).
    Add(model.NewStartEvent("start")).
    Add(model.NewExclusiveGateway("gw")).
    Add(model.NewServiceTask("approve", "approve-loan")).
    Add(model.NewServiceTask("reject", "reject-loan")).
    Add(model.NewEndEvent("end-ok")).
    Add(model.NewEndEvent("end-ko")).
    Connect("start", "gw").
    Connect("gw", "approve", model.WithCondition("score >= 700")).
    Connect("gw", "reject", model.AsDefault()).
    Connect("approve", "end-ok").
    Connect("reject", "end-ko").
    Build()
```

---

## RetryPolicy

```go
type RetryPolicy struct {
    MaxAttempts        int           // total attempts including first; 0 = unlimited; default 3
    InitialInterval    time.Duration // delay before first retry; default 1s
    BackoffCoef        float64       // exponential multiplier per attempt; default 2.0
    MaxInterval        time.Duration // per-attempt cap; 0 = no cap; default 100s
    MaxElapsed         time.Duration // total time budget; 0 = no cap
    NonRetryableErrors []string      // error-message substrings that abort retrying immediately
}
```

- `MaxAttempts` includes the original attempt. Set to `0` for unlimited retries.
- `BackoffCoef` must be ≥ 1.0 when `InitialInterval > 0`; a value below 1.0
  would shrink delays and is rejected by `Validate`.
- `model.DefaultRetryPolicy()` returns safe defaults (MaxAttempts=3,
  InitialInterval=1s, BackoffCoef=2.0, MaxInterval=100s).
- `RetryPolicy.Normalize()` fills zero fields from the defaults (preserves
  `MaxAttempts == 0` as unlimited).
- Attach to any activity with `model.WithRetryPolicy(&p)`.
- A runtime-wide default is set with `runtime.WithDefaultRetryPolicy(p)`.

---

## Validation

`model.Validate(*ProcessDefinition)` is called automatically by `Build` and by
the YAML/JSON loaders. It runs a comprehensive structural check and returns a
joined error listing every violation found.

**Rules enforced:**

| Sentinel error | Rule |
|---|---|
| `ErrNoStartEvent` | Definition must have exactly one start event |
| `ErrMultipleStartEvents` | Only one start event permitted |
| `ErrDanglingFlow` | Flow source and target must reference existing nodes |
| `ErrDeadEnd` | Every non-end node must have at least one outgoing flow |
| `ErrStartHasIncoming` | Start event must have no incoming flows |
| `ErrEndHasOutgoing` | End events must have no outgoing flows |
| `ErrConditionNotAllowed` | Flow conditions are only allowed on outgoing flows of exclusive/inclusive gateways |
| `ErrDefaultNotAllowed` | Default flows are only allowed from exclusive/inclusive gateways |
| `ErrMultipleDefaults` | A node may have at most one default outgoing flow |
| `ErrEventGatewayTarget` | Every outgoing flow of an `EventBasedGateway` must target an `IntermediateCatchEvent` |
| `ErrMixedGateway` | A gateway with >1 incoming and >1 outgoing flows is ambiguous (ADR-0014) |
| `ErrUnreachableNode` | Every node must be reachable from the start event (via flows, boundary events, or event-sub-processes) |
| `ErrUnpairedJoin` | A `ParallelGateway` join without a matching split that can deliver concurrent tokens would deadlock |
| `ErrBoundaryAttachment` | `BoundaryEvent.AttachedTo` must reference an existing activity node |
| `ErrBoundaryErrorHost` | Boundary error events may only attach to `ServiceTask`, `SubProcess`, or `CallActivity` |
| `ErrMissingSubprocess` | `SubProcess` and `EventSubProcess` must carry a non-nil nested definition |
| `ErrMissingDefRef` | `CallActivity.DefRef` must be non-empty |
| `ErrInvalidRetryPolicy` | `MaxAttempts ≥ 0`, intervals `≥ 0`, `BackoffCoef ≥ 1.0` when `InitialInterval > 0` |
| `ErrInvalidRecoveryFlow` | `RecoveryFlow` must name an existing outgoing flow of that node |
| `ErrEmptyCancelAction` | `CancelActions` entries must be non-empty strings |
| `ErrCompensateRefNotFound` | A non-empty `CompensateRef` on `IntermediateThrowEvent` must reference an existing node |

Validation recurses into nested `SubProcess` and `EventSubProcess` definitions.
Errors from nested definitions are wrapped with the host node ID.

---

## Serialization / YAML

`ProcessDefinition` round-trips through a flat, backward-compatible wire form via
standard `encoding/json`. For YAML authoring there are two entry points:

```go
// Parse a YAML byte slice.
def, err := model.ParseYAML(data []byte) (*ProcessDefinition, error)

// Parse from any io.Reader.
def, err := model.LoadYAML(r io.Reader) (*ProcessDefinition, error)
```

Both functions validate before returning.

### Kind discriminator

The `kind` field uses **lowerCamelCase** strings:

`startEvent`, `endEvent`, `terminateEndEvent`, `errorEndEvent`, `serviceTask`,
`userTask`, `receiveTask`, `sendTask`, `businessRuleTask`, `subProcess`,
`callActivity`, `eventSubProcess`, `intermediateCatchEvent`,
`intermediateThrowEvent`, `boundaryEvent`, `exclusiveGateway`,
`parallelGateway`, `inclusiveGateway`, `eventBasedGateway`

### YAML example

```yaml
id: order-fulfillment
version: 1
nodes:
  - id: start
    kind: startEvent

  - id: charge
    kind: serviceTask
    name: Charge Card
    action: charge-card
    compensationAction: refund-card
    retryPolicy:
      maxAttempts: 5
      initialInterval: 2s
      backoffCoef: 2.0

  - id: approve
    kind: userTask
    name: Approve Order
    candidateRoles: [manager]
    slaDuration: PT48H
    slaFlow: sla-breach->notify
    slaAction: notify-ops

  - id: gw
    kind: exclusiveGateway

  - id: fulfill
    kind: serviceTask
    action: fulfill-order

  - id: cancel
    kind: serviceTask
    action: cancel-order

  - id: end
    kind: endEvent

flows:
  - { id: f1, source: start,   target: charge }
  - { id: f2, source: charge,  target: approve }
  - { id: f3, source: approve, target: gw }
  - { id: f4, source: gw,      target: fulfill, condition: "approved == true" }
  - { id: f5, source: gw,      target: cancel,  isDefault: true }
  - { id: f6, source: fulfill, target: end }
  - { id: f7, source: cancel,  target: end }
```

### YAML node fields reference

| Field | Applies to |
|---|---|
| `id`, `kind`, `name` | all nodes |
| `action` | `serviceTask`, `businessRuleTask` |
| `candidateRoles` | `userTask` |
| `eligibilityExpr` | `userTask` |
| `messageName`, `correlationKey` | `receiveTask`, `sendTask`, `intermediateCatchEvent`, `boundaryEvent` |
| `timerDuration` | `startEvent` (ESP), `intermediateCatchEvent`, `boundaryEvent` |
| `signalName` | `startEvent` (ESP), `intermediateCatchEvent`, `intermediateThrowEvent`, `boundaryEvent` |
| `errorCode` | `errorEndEvent`, `boundaryEvent` |
| `attachedTo` | `boundaryEvent` |
| `nonInterrupting` | `boundaryEvent`, `eventSubProcess` |
| `defRef` | `callActivity` |
| `subprocess` | `subProcess`, `eventSubProcess` (nested definition object) |
| `compensateRef` | `intermediateThrowEvent` |
| `retryPolicy` | all activity kinds |
| `recoveryFlow` | all activity kinds |
| `compensationAction` | all activity kinds |
| `cancelHandler` | all activity kinds |
| `slaDuration`, `slaFlow`, `slaAction` | all activity kinds, `intermediateCatchEvent` |
| `reminderEvery`, `reminderAction` | all activity kinds, `intermediateCatchEvent` |

---

## Authoring forms

| Form | Entry point | When to use |
|---|---|---|
| **Go constructors + DefinitionBuilder** | `model.NewDefinition(...).Add(...).Connect(...).Build()` | Preferred. Type-safe, IDE-navigable, no external files. |
| **YAML** | `model.ParseYAML(data)` / `model.LoadYAML(r)` | Configuration-driven pipelines; definitions stored outside the binary. |
| **JSON** | `json.Unmarshal` into `ProcessDefinition` then `model.Validate` | Programmatic interchange, REST payloads, persistence round-trips. |

In all cases `Validate` runs before the definition is returned to the caller.

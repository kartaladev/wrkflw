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
8. [DefinitionBuilder vs DefinitionLoader](#definitionbuilder-vs-definitionloader)
9. [Authoring forms](#authoring-forms)

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
  YAML, or JSON. The builder and YAML paths call `Validate` automatically; the
  JSON path (`json.Unmarshal`) does **not** — call `model.Validate` yourself after
  decoding.

### Container types

`ProcessDefinition` is the top-level template:

| Field | Type | Description |
|---|---|---|
| `ID` | `string` | Definition identifier. |
| `Version` | `int` | Definition version — a definition is keyed by `ID:Version`. |
| `Nodes` | `[]Node` | The process nodes (built via the `New*` constructors / builder). |
| `Flows` | `[]SequenceFlow` | Directed sequence flows connecting the nodes. |
| `CancelActions` | `[]string` | Best-effort service-action names run when an instance is cancelled. |

(Two unexported fields, `scoped`/`scopedNames`, hold the definition-scoped action
catalog; they are populated by `Build()` from `RegisterAction` and read via
`ScopedActionNames()`.)

`SequenceFlow` is a directed edge between two nodes:

| Field | Type | Description |
|---|---|---|
| `ID` | `string` | Flow identifier (defaults to `"fromID->toID"`). |
| `Source` | `string` | Source node ID. |
| `Target` | `string` | Target node ID. |
| `Condition` | `string` | expr-lang routing expression; empty = unconditional. Only valid on the outgoing flows of an exclusive/inclusive gateway. |
| `IsDefault` | `bool` | Marks the exclusive/inclusive-gateway default flow. |

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
| `KindServiceTask` | `ServiceTask` | `NewServiceTask(id, opts...)` |
| `KindUserTask` | `UserTask` | `NewUserTask(id, roles, opts...)` |
| `KindReceiveTask` | `ReceiveTask` | `NewReceiveTask(id, messageName, opts...)` |
| `KindSendTask` | `SendTask` | `NewSendTask(id, messageName, opts...)` |
| `KindBusinessRuleTask` | `BusinessRuleTask` | `NewBusinessRuleTask(id, opts...)` |
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
| `WithDeadline(dur, flowID, actionName string)` | Deadline (Go-duration string, e.g. `72h`), escape flow, and/or action on breach |
| `WithReminder(every, actionName string)` | Periodic reminder action (Go-duration interval, e.g. `24h`) fired while the node is active |

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
WithStartTimer(dur string) // Go-duration string, e.g. "1h"
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
WithICEDeadline(dur, flowID, action string)
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

> **Note:** Timer, signal, error, and message boundary events are all armed and
> fired by the engine (message boundaries since ADR-0053). The one exception is a
> message boundary attached to a `ReceiveTask` host, which is not yet armed.

### Example — service task with compensation and deadline

```go
task := model.NewServiceTask("charge",
    model.WithActionName("charge-card"),
    model.WithName("Charge Card"),
    model.WithCompensation("refund-card"),
    model.WithDeadline("2h", "sla-breach-flow", "notify-ops"),
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
    model.WithEligibilityExpr(`actor.Attributes["region"] == "EU"`),
    model.WithReminder("24h", "send-reminder-email"),
)
```

---

## DefinitionBuilder

`DefinitionBuilder` wires nodes and flows into a `ProcessDefinition` and calls
`Validate` on `Build`.

The **fluent `AddX` methods** are the preferred way to add nodes: there is one
`Add<Kind>` per node kind, each mirroring its `New<Kind>` constructor exactly and
appending the node — so you write `AddServiceTask("charge", …)` instead of
`Add(NewServiceTask("charge", …))`. They make the node palette discoverable via
autocomplete and keep the chain terse:

```go
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
```

The generic `Add(n Node)` is retained for programmatically- or dynamically-built
nodes (and is what YAML loading uses internally); the two forms are equivalent —
`AddServiceTask(id, opts...)` is exactly `Add(NewServiceTask(id, opts...))`.

**Method summary:**

| Method | Description |
|---|---|
| `NewDefinition(id string, version int) DefinitionBuilder` | Start a new builder |
| `.Add<Kind>(…) DefinitionBuilder` | **Fluent per-kind node adder** — one per node kind (see table below); mirrors `New<Kind>` and appends the node |
| `.Add(n Node) DefinitionBuilder` | Append a pre-built node (programmatic / dynamic; YAML uses this) |
| `.Connect(fromID, toID string, opts ...FlowOption) DefinitionBuilder` | Add a directed sequence flow |
| `.CancelActions(names ...string) DefinitionBuilder` | Best-effort actions on instance cancel |
| `.RegisterAction(name string, a action.ServiceAction) DefinitionBuilder` | Register a definition-scoped action |
| `.RegisterActionFunc(name string, fn …) DefinitionBuilder` | Register a scoped action from a plain func |
| `.Build() (*ProcessDefinition, error)` | Assemble and validate |

**Fluent node adders** (each takes the same arguments as its `New<Kind>` constructor and returns `DefinitionBuilder`):

| Group | Methods |
|---|---|
| Events | `AddStartEvent(id, opts...)`, `AddEndEvent(id, name...)`, `AddTerminateEndEvent(id, name...)`, `AddErrorEndEvent(id, errorCode, name...)` |
| Gateways | `AddExclusiveGateway(id, name...)`, `AddParallelGateway(id, name...)`, `AddInclusiveGateway(id, name...)`, `AddEventBasedGateway(id, name...)` |
| Activities | `AddServiceTask(id, opts...)`, `AddUserTask(id, roles, opts...)`, `AddReceiveTask(id, messageName, opts...)`, `AddSendTask(id, messageName, opts...)`, `AddBusinessRuleTask(id, opts...)`, `AddSubProcess(id, sub, opts...)`, `AddCallActivity(id, defRef, opts...)` |
| Subprocess / intermediate / boundary | `AddEventSubProcess(id, sub, opts...)`, `AddIntermediateCatchEvent(id, opts...)`, `AddIntermediateThrowEvent(id, opts...)`, `AddBoundaryEvent(id, attachedTo, opts...)` |

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
    AddStartEvent("start").
    AddExclusiveGateway("gw").
    AddServiceTask("approve", model.WithActionName("approve-loan")).
    AddServiceTask("reject", model.WithActionName("reject-loan")).
    AddEndEvent("end-ok").
    AddEndEvent("end-ko").
    Connect("start", "gw").
    Connect("gw", "approve", model.WithCondition("score >= 700")).
    Connect("gw", "reject", model.AsDefault()).
    Connect("approve", "end-ok").
    Connect("reject", "end-ko").
    Build()
```

---

## RetryPolicy

| Field | Type | Default | Meaning |
|---|---|---|---|
| `MaxAttempts` | `int` | `3` | Total attempts including the first; `0` = unlimited. |
| `InitialInterval` | `time.Duration` | `1s` | Delay before the first retry. |
| `BackoffCoef` | `float64` | `2.0` | Exponential multiplier per attempt. Must be ≥ 1.0 when `InitialInterval > 0` (a value below 1.0 is rejected by `Validate`). |
| `MaxInterval` | `time.Duration` | `100s` | Per-attempt delay cap; `0` = no cap. |
| `MaxElapsed` | `time.Duration` | `0` (no cap) | Total time budget across all attempts; `0` = no cap. Enforced by the engine retry executor (anchored at the token's first retry). |
| `NonRetryableErrors` | `[]string` | `nil` | Error-message substrings that abort retrying immediately. |

- `model.DefaultRetryPolicy()` returns the defaults above.
- `RetryPolicy.Normalize()` fills zero fields from the defaults (preserving
  `MaxAttempts == 0` as unlimited).
- Attach to any activity with `model.WithRetryPolicy(&p)`; set a runtime-wide
  fallback with `runtime.WithDefaultRetryPolicy(p)`.

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
// Parse a YAML byte slice — returns a DefinitionLoader, not the definition directly.
ld, err := model.ParseYAML(data)
if err != nil { log.Fatal(err) }
// Optionally register definition-scoped actions before building:
ld.RegisterAction("my-action", myAction)
def, err := ld.Build()

// Parse from any io.Reader (delegates to ParseYAML internally).
ld, err := model.LoadYAML(r)
if err != nil { log.Fatal(err) }
def, err := ld.Build()
```

Both functions validate the YAML structure before returning. Validation of the
assembled definition runs inside `Build()`. YAML cannot carry `RegisterAction`
calls — those require Go code — so the load → register-actions → `Build()`
sequence is the correct idiom for YAML-loaded definitions that need scoped actions.

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
    deadlineDuration: 48h
    deadlineFlow: sla-breach->notify
    deadlineAction: notify-ops

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
| `deadlineDuration`, `deadlineFlow`, `deadlineAction` | all activity kinds, `intermediateCatchEvent` |
| `reminderEvery`, `reminderAction` | all activity kinds, `intermediateCatchEvent` |

---

## DefinitionBuilder vs DefinitionLoader

`model` exposes two interface types for assembling a `*ProcessDefinition`. They
share one underlying `definitionCore` and differ only in their method set.

### `DefinitionBuilder` — full authoring surface

Returned by `NewDefinition(id, version)`. Lets you add nodes, connect flows, and
register definition-scoped actions in any order (both idioms below compile):

```go
// Actions-first idiom (useful when actions are wired before nodes are known):
b := model.NewDefinition("order", 1).
    RegisterAction("charge-card", myAction)
def, err := b.AddServiceTask("charge", model.WithActionName("charge-card")).
    AddEndEvent("end").
    Connect("charge", "end").
    Build()
if err != nil {
    log.Fatal(err) // Build validates the assembled definition
}
_ = def

// Structure-first idiom (most common):
def, err := model.NewDefinition("order", 1).
    AddStartEvent("start").
    AddServiceTask("charge", model.WithActionName("charge-card"),
        model.WithCompensation("refund-card")).
    AddEndEvent("end").
    Connect("start", "charge").
    Connect("charge", "end").
    RegisterAction("charge-card", myChargeAction).
    RegisterAction("refund-card", myRefundAction).
    Build()
```

`Build()` validates the assembled definition, compiles the scoped action catalog,
and returns a `*ProcessDefinition`. It returns an error if validation fails (e.g.
missing start event, dangling flow) or if the same action name was registered twice
(`model.ErrDuplicateScopedAction`).

`DefinitionBuilder` also exposes `.Loader()` which returns a `DefinitionLoader`
backed by the same core — useful when handing off a builder to a function that
only needs to register actions.

### `DefinitionLoader` — post-parse, actions only

Returned by `ParseYAML` and `LoadYAML`. Exposes only:
- `RegisterAction(name, a)` / `RegisterActionFunc(name, fn)` — attach Go function
  values that the YAML cannot serialize.
- `CancelActions(names...)` — override or supplement the cancel-action list.
- `Build()` — assemble, validate, and return the `*ProcessDefinition`.

The structural elements (nodes, flows, kind, options) are already decoded from
the YAML byte stream. The `DefinitionLoader` has no `Add*` or `Connect` methods
because the structure is pre-declared.

### Why YAML can't carry `RegisterAction`

A YAML node can name an action (`action: charge-card`) but it cannot embed a Go
function. Any `serviceTask` or `businessRuleTask` that needs a definition-scoped
action must be registered in Go code after loading:

```go
ld, err := model.ParseYAML(yamlBytes)
if err != nil {
    return err
}
ld.RegisterAction("charge-card", myChargeAction)
ld.RegisterAction("refund-card", myRefundAction)
def, err := ld.Build()
```

If the definition uses only global-catalog actions (not scoped), calling
`Build()` without any `RegisterAction` is correct — the global catalog is
resolved at execution time, not at definition time.

### Duplicate scoped-action detection

Registering the same name twice overwrites the first value in the accumulator but
sets an internal flag; `Build()` returns `model.ErrDuplicateScopedAction` naming
the duplicate. This catches accidental re-registration at wiring time rather than
producing a silent override.

---

## Authoring forms

| Form | Entry point | When to use |
|---|---|---|
| **Go constructors + DefinitionBuilder** | `model.NewDefinition(...).Add(...).Connect(...).Build()` | Preferred. Type-safe, IDE-navigable, no external files. |
| **YAML** | `model.ParseYAML(data)` / `model.LoadYAML(r)` → returns `DefinitionLoader`; call `.RegisterAction(...)` then `.Build()` | Configuration-driven pipelines; definitions stored outside the binary. |
| **JSON** | `json.Unmarshal` into `ProcessDefinition` then `model.Validate` | Programmatic interchange, REST payloads, persistence round-trips. |

In all cases `Validate` runs before the definition is returned to the caller.

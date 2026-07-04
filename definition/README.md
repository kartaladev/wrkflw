# definition

Package `definition` is the **process-definition authoring layer** for
`github.com/zakyalvan/krtlwrkflw`. It is pure data plus validation — it imports
only the Go standard library (plus the `action` interface). Consumers use it to
describe the shape of a workflow; the `runtime` and `engine` packages execute it.

## Contents

1. [Overview](#overview)
2. [Packages](#packages)
3. [The Node interface and node-family packages](#the-node-interface-and-node-family-packages)
4. [Constructors and options](#constructors-and-options)
5. [Building a definition](#building-a-definition)
6. [The kinds bundle (deserialization)](#the-kinds-bundle-deserialization)
7. [RetryPolicy](#retrypolicy)
8. [Validation](#validation)
9. [Serialization / YAML](#serialization--yaml)
10. [Authoring forms](#authoring-forms)

---

## Overview

`definition` holds the in-memory representation of a **process definition** — the
reusable template from which process instances are created. Concepts mirror BPMN
(tasks, gateways, events, sequence flows) but this package is **not
BPMN-compatible** and makes no attempt to round-trip arbitrary BPMN2 XML.

Key design properties:

- **Pure data + validation.** `ProcessDefinition` and every `Node` type are plain
  Go values. No I/O, no goroutines, no heavy dependencies.
- **One concrete type per node kind, grouped by BPMN family.** `Node` is an
  interface; each kind is a struct that lives in one of three leaf packages —
  `definition/event`, `definition/gateway`, `definition/activity`. Construct nodes
  with the family `New*` constructors — never construct the structs directly.
- **Registry-driven (de)serialization.** Each leaf registers its kinds with
  `definition` at import time (the `image/png`/`database/sql` driver idiom), so
  `definition` serializes and validates without importing the leaves. This is why
  deserialization callers must import [`definition/kinds`](#the-kinds-bundle-deserialization).
- **Three authoring forms.** Go constructors (`definition/build` fluent chain or
  `definition.NewDefinition(...).Add(...)`), YAML, or JSON. The builder and YAML
  paths call `Validate` automatically; the JSON path (`json.Unmarshal`) does
  **not** — call `definition.Validate` yourself after decoding.

### Container types

`ProcessDefinition` is the top-level template: `ID string`, `Version int`,
`Nodes []Node`, `Flows []SequenceFlow`, `CancelActions []string` (best-effort
service-action names run on cancel), plus unexported scoped-action state populated
by `Build()`.

`SequenceFlow` is a directed edge: `ID`, `Source`, `Target`, `Condition`
(expr-lang routing expression; only on exclusive/inclusive-gateway outgoing
flows), `IsDefault`.

---

## Packages

| Package | Import path | Holds |
|---|---|---|
| core | `.../definition` | `Node`, `NodeKind`, `ProcessDefinition`, `SequenceFlow`, `RetryPolicy`, `Validate`, `NewDefinition`, JSON/YAML (de)serialization, the kind registry, shared embeds (`Base`, `ActivityFields`, `WaitFields`, `TaskAction`), sentinel errors. Imports no leaf. |
| events | `.../definition/event` | `NewStart`, `NewEnd`, `NewTerminateEnd`, `NewErrorEnd`, `NewCatch`, `NewThrow`, `NewBoundary`, `NewEventSubProcess` + their options |
| gateways | `.../definition/gateway` | `NewExclusive`, `NewParallel`, `NewInclusive`, `NewEventBased` |
| activities | `.../definition/activity` | `NewServiceTask`, `NewUserTask`, `NewReceiveTask`, `NewSendTask`, `NewBusinessRuleTask`, `NewSubProcess`, `NewCallActivity` + their options |
| fluent builder | `.../definition/build` | `build.New(...)` with terse `AddX` methods (imports the leaves) |
| kinds bundle | `.../definition/kinds` | blank-imports all leaves so deserialization has every kind registered |

The leaves import `definition`; `definition` imports none of them (no cycle).

---

## The Node interface and node-family packages

```go
type Node interface {
    Kind() NodeKind
    ID()   string
    Name() string
}
```

The 19 concrete kinds live in the leaf packages. Constructors return
`definition.Node`; you rarely name the concrete type (accessors below read
kind-specific data generically).

### Events — `definition/event`

| Kind constant | Constructor |
|---|---|
| `KindStartEvent` | `event.NewStart(id, opts...)` |
| `KindEndEvent` | `event.NewEnd(id, name...)` |
| `KindTerminateEndEvent` | `event.NewTerminateEnd(id, name...)` |
| `KindErrorEndEvent` | `event.NewErrorEnd(id, errorCode, name...)` |
| `KindIntermediateCatchEvent` | `event.NewCatch(id, opts...)` |
| `KindIntermediateThrowEvent` | `event.NewThrow(id, opts...)` |
| `KindBoundaryEvent` | `event.NewBoundary(id, attachedTo, opts...)` |
| `KindEventSubProcess` | `event.NewEventSubProcess(id, *ProcessDefinition, opts...)` |

### Gateways — `definition/gateway`

| Kind constant | Constructor | Routing rule |
|---|---|---|
| `KindExclusiveGateway` | `gateway.NewExclusive(id, name...)` | XOR — first matching condition (or default) |
| `KindParallelGateway` | `gateway.NewParallel(id, name...)` | AND — activate all / wait for all |
| `KindInclusiveGateway` | `gateway.NewInclusive(id, name...)` | OR — all matching; join waits for active branches |
| `KindEventBasedGateway` | `gateway.NewEventBased(id, name...)` | Race — routes to whichever catch event fires first |

### Activities — `definition/activity`

| Kind constant | Constructor |
|---|---|
| `KindServiceTask` | `activity.NewServiceTask(id, opts...)` |
| `KindUserTask` | `activity.NewUserTask(id, roles, opts...)` |
| `KindReceiveTask` | `activity.NewReceiveTask(id, messageName, opts...)` |
| `KindSendTask` | `activity.NewSendTask(id, messageName, opts...)` |
| `KindBusinessRuleTask` | `activity.NewBusinessRuleTask(id, opts...)` |
| `KindSubProcess` | `activity.NewSubProcess(id, *ProcessDefinition, opts...)` |
| `KindCallActivity` | `activity.NewCallActivity(id, defRef, opts...)` |

---

## Constructors and options

### Shared activity options (`definition/activity`)

Work on all activity constructors: `WithName`, `WithRetryPolicy(*RetryPolicy)`,
`WithRecoveryFlow(flowID)`, `WithCompensation(actionName)`,
`WithCancelHandler(actionName)`, `WithDeadline(dur, flowID, actionName)`,
`WithReminder(every, actionName)`.

Kind-specific: `WithActionName` / `WithAction` / `WithActionFunc` (service &
business-rule tasks), `WithEligibilityExpr` / `WithEligibilityPrivileges` (user
task), `WithCorrelationKey` (receive & send tasks).

```go
task := activity.NewServiceTask("charge",
    activity.WithActionName("charge-card"),
    activity.WithName("Charge Card"),
    activity.WithCompensation("refund-card"),
    activity.WithDeadline("2h", "sla-breach-flow", "notify-ops"),
    activity.WithRetryPolicy(&definition.RetryPolicy{
        MaxAttempts: 5, InitialInterval: 2 * time.Second, BackoffCoef: 2.0,
    }),
)
```

### Event options (`definition/event`)

`WithName` (start/catch/boundary/event-sub-process); start triggers
`WithStartSignal` / `WithStartMessage` / `WithStartTimer`; catch
`WithCatchTimer` / `WithCatchSignal` / `WithCatchMessage` / `WithCatchDeadline` /
`WithCatchReminder`; throw `WithThrowSignal` / `WithCompensateRef` /
`WithThrowName`; boundary `WithBoundaryTimer` / `WithBoundarySignal` /
`WithBoundaryMessage` / `WithBoundaryErrorCode` / `WithBoundaryNonInterrupting`;
event-sub-process `WithEventSubProcessNonInterrupting`.

> The start / catch / boundary trigger options are symmetric
> (`WithStartTimer` / `WithCatchTimer` / `WithBoundaryTimer`, etc.).

Gateways take only an optional name (trailing variadic); they have no options.

---

## Building a definition

Two Go paths. The fluent `definition/build` package is the terse one:

```go
import (
    "github.com/zakyalvan/krtlwrkflw/definition/activity"
    "github.com/zakyalvan/krtlwrkflw/definition/build"
)

def, err := build.New("order-fulfillment", 1).
    AddStart("start").
    AddServiceTask("charge",
        activity.WithActionName("charge-card"),
        activity.WithCompensation("refund-card")).
    AddUserTask("approve", []string{"manager"}).
    AddEnd("end").
    Connect("start", "charge").
    Connect("charge", "approve").
    Connect("approve", "end").
    Build()
```

The core `definition.NewDefinition(...)` builder takes pre-built nodes via the
generic `.Add(node)` — useful for programmatic/dynamic construction:

```go
def, err := definition.NewDefinition("loan", 1).
    Add(event.NewStart("start")).
    Add(gateway.NewExclusive("gw")).
    Add(activity.NewServiceTask("approve", activity.WithActionName("approve-loan"))).
    Add(event.NewEnd("end-ok")).
    Connect("start", "gw").
    Connect("gw", "approve", definition.WithCondition("score >= 700")).
    Connect("gw", "end-ok", definition.AsDefault()).
    Connect("approve", "end-ok").
    Build()
```

Both `Build()` calls run `Validate` and compile the definition-scoped action
catalog. `FlowOption` values: `definition.WithFlowID(id)`,
`definition.WithCondition(expr)`, `definition.AsDefault()`.

**`DefinitionLoader`** (returned by `ParseYAML`/`LoadYAML`) exposes only
`RegisterAction`/`RegisterActionFunc`/`CancelActions`/`Build` — the structure is
already declared by the parsed YAML.

---

## The kinds bundle (deserialization)

Because each node kind registers itself from its leaf package's `init`, code that
**deserializes** a definition (JSON/JSONB from a store, a transport payload) must
ensure the leaves are imported. Blank-import the bundle:

```go
import _ "github.com/zakyalvan/krtlwrkflw/definition/kinds"
```

Code that **constructs** definitions in Go already imports the specific leaf
packages it uses and needs no extra import. If a kind is not registered,
`ProcessDefinition.UnmarshalJSON` (and the YAML loader) fail with a loud
`definition.ErrKindNotRegistered` naming the missing kind — never a silent zero
value. The persistence store already imports the bundle.

---

## RetryPolicy

| Field | Type | Default | Meaning |
|---|---|---|---|
| `MaxAttempts` | `int` | `3` | Total attempts including the first; `0` = unlimited. |
| `InitialInterval` | `time.Duration` | `1s` | Delay before the first retry. |
| `BackoffCoef` | `float64` | `2.0` | Exponential multiplier; must be ≥ 1.0 when `InitialInterval > 0`. |
| `MaxInterval` | `time.Duration` | `100s` | Per-attempt delay cap; `0` = no cap. |
| `MaxElapsed` | `time.Duration` | `0` | Total time budget; `0` = no cap. |
| `NonRetryableErrors` | `[]string` | `nil` | Error-message substrings that abort retrying. |

`definition.DefaultRetryPolicy()` returns the defaults; `RetryPolicy.Normalize()`
fills zero fields (preserving `MaxAttempts == 0`). Attach with
`activity.WithRetryPolicy(&p)`; set a runtime-wide fallback with
`runtime.WithDefaultRetryPolicy(p)`.

---

## Validation

`definition.Validate(*ProcessDefinition)` is called automatically by `Build` and
the YAML/JSON loaders. It runs a comprehensive structural check and returns a
joined error. Sentinel errors include: `ErrNoStartEvent`,
`ErrMultipleStartEvents`, `ErrDanglingFlow`, `ErrDeadEnd`, `ErrStartHasIncoming`,
`ErrEndHasOutgoing`, `ErrConditionNotAllowed`, `ErrDefaultNotAllowed`,
`ErrMultipleDefaults`, `ErrEventGatewayTarget`, `ErrMixedGateway`,
`ErrUnreachableNode`, `ErrUnpairedJoin`, `ErrBoundaryAttachment`,
`ErrBoundaryErrorHost`, `ErrMissingSubprocess`, `ErrMissingDefRef`,
`ErrInvalidRetryPolicy`, `ErrInvalidRecoveryFlow`, `ErrEmptyCancelAction`,
`ErrCompensateRefNotFound`. Validation recurses into nested `SubProcess` and
`EventSubProcess` definitions.

### Kind-agnostic accessors

`definition.RetryPolicyOf(n)`, `DeadlineOf(n)`, `ReminderOf(n)`, `ActionOf(n)`,
`InlineActionOf(n)` read kind-specific fields off any `Node` (returning zero
values for kinds that don't carry them), so callers never type-switch on concrete
leaf types.

---

## Serialization / YAML

`ProcessDefinition` round-trips through a flat, backward-compatible wire form via
standard `encoding/json`. YAML entry points return a `DefinitionLoader`:

```go
ld, err := definition.ParseYAML(data)   // or definition.LoadYAML(r)
ld.RegisterAction("my-action", myAction) // YAML can't carry Go funcs
def, err := ld.Build()
```

The `kind` discriminator uses lowerCamelCase strings, unchanged by the
relocation: `startEvent`, `endEvent`, `serviceTask`, `userTask`,
`exclusiveGateway`, `intermediateCatchEvent`, `boundaryEvent`, … . (When
deserializing, import `definition/kinds` — see above.)

---

## Authoring forms

| Form | Entry point | When to use |
|---|---|---|
| **Fluent Go** | `build.New(...).AddX(...).Connect(...).Build()` | Preferred; terse, IDE-navigable. |
| **Core builder** | `definition.NewDefinition(...).Add(node).Connect(...).Build()` | Programmatic / dynamic node lists. |
| **YAML** | `definition.ParseYAML` / `LoadYAML` → `DefinitionLoader` | Config-driven pipelines; import `definition/kinds`. |
| **JSON** | `json.Unmarshal` into `ProcessDefinition` then `definition.Validate` | Interchange / persistence; import `definition/kinds`. |

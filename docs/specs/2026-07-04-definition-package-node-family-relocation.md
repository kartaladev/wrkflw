# Spec — `definition` package + node-family relocation

- **Date:** 2026-07-04
- **Status:** Approved (brainstorming)
- **Related ADR:** 0090 (to be written)
- **Supersedes package:** `model/` (renamed to `definition/`)

## 1. Problem

The `model` package (the process-definition authoring layer, the product's public
API) has grown a high **maintenance and reading cost**:

- **The "add-a-kind tax."** Adding one node kind requires coordinated edits across
  ~7 files with no compile-time coupling forcing them to agree: the `NodeKind`
  iota (`definition.go`), the struct + `Kind()` method (`node.go`), the JSON name
  (`nodekind_json.go`), the `toWire`/`fromWire` arms (`node_wire.go`), the
  constructor + option plumbing (`node_constructors.go`), the fluent `AddX`
  (`builder_fluent.go`), and — for activities — participation in five parallel
  accessor switches (`accessors.go` + `validate.go`).
- **Parallel switch duplication.** `RetryPolicyOf`, `DeadlineOf`, `ReminderOf`,
  and `recoveryFlowOf` each re-enumerate the same seven activity kinds, all
  returning fields of the embedded `activityFields`.
- **Serialization struct duplication.** `yaml.go` defines `nodeYAML`, a second
  copy of the 25-field `nodeWire` union, plus a 25-field manual copy in
  `fromNodeYAML`.
- **Flat namespace.** All 19 node kinds, their constructors, and ~30 options sit
  in one package. A reader looking for "how do I make a boundary event" wades
  through the entire node palette. There is no grouping by BPMN family.
- **Abbreviations leak into the public API.** `WithICEDeadline`,
  `WithICEReminder` (ICE = IntermediateCatchEvent), `WithESPNonInterrupting`
  (ESP = EventSubProcess), and the prefix-inconsistent `BoundaryNonInterrupting`
  (missing `With`).

The goal is a **deliberate reduction of cognitive load for readers and
maintainers**, with **functional correctness as a hard requirement**. Backward
compatibility is explicitly **not** required (pre-v0.1.0; the whole repo is
migrated in the same change).

## 2. Decision summary

1. **Rename `model` → `definition`.** The package that describes a process
   *definition* is named for what it holds.
2. **Group node kinds into BPMN-family subpackages** a consumer imports directly:
   - `definition/event` — start, end, terminate-end, error-end, intermediate
     catch, intermediate throw, boundary, **event sub-process**.
   - `definition/gateway` — exclusive, parallel, inclusive, event-based.
   - `definition/activity` — service, user, receive, send, business-rule task;
     sub-process; call-activity.
3. **True relocation** — the concrete structs, constructors, and options physically
   live in the leaves, not in `definition`.
4. **A driver-registration pattern** breaks the import cycle and makes adding a
   kind a single-site change.
5. **Fluent authoring stays**, in a dedicated `definition/build` package that may
   import the leaves.
6. **No backward-compatibility shim.** Every call site in the repo is migrated.

## 3. Target package layout

```
definition/                 core: container + machinery. Imports NO leaf.
  Node, NodeKind, ProcessDefinition, SequenceFlow, RetryPolicy
  DefinitionBuilder / New(), Validate(), JSON + YAML (de)serialization
  the per-kind registry + wire projection (registry starts empty)
  exported embeddable field-groups: Base, ActivityFields, WaitFields
  sentinel errors
definition/event/           NewStart, NewEnd, NewTerminateEnd, NewErrorEnd,
                            NewCatch, NewThrow, NewBoundary, NewEventSubProcess
definition/gateway/         NewExclusive, NewParallel, NewInclusive, NewEventBased
definition/activity/        NewServiceTask, NewUserTask, NewReceiveTask, NewSendTask,
                            NewBusinessRuleTask, NewSubProcess, NewCallActivity
definition/kinds/           one-line bundle: blank-imports the three leaves
definition/build/           fluent DefinitionBuilder wrapper (AddStart, AddServiceTask, …);
                            imports the leaves + definition
```

### 3.1 Naming map (public API)

| Old (`model`) | New |
|---|---|
| `model.ProcessDefinition`, `model.SequenceFlow`, `model.Node`, `model.NodeKind`, `model.RetryPolicy`, `model.Validate`, `model.NewDefinition` | `definition.*` (same names, new package) |
| `model.NewStartEvent(id, opts…)` | `event.NewStart(id, opts…)` |
| `model.NewEndEvent(id, name…)` | `event.NewEnd(id, name…)` |
| `model.NewTerminateEndEvent` | `event.NewTerminateEnd` |
| `model.NewErrorEndEvent(id, code, name…)` | `event.NewErrorEnd(id, code, name…)` |
| `model.NewIntermediateCatchEvent(id, opts…)` | `event.NewCatch(id, opts…)` |
| `model.NewIntermediateThrowEvent(id, opts…)` | `event.NewThrow(id, opts…)` |
| `model.NewBoundaryEvent(id, host, opts…)` | `event.NewBoundary(id, host, opts…)` |
| `model.NewEventSubProcess(id, sub, opts…)` | `event.NewEventSubProcess(id, sub, opts…)` |
| `model.NewExclusiveGateway(id, name…)` | `gateway.NewExclusive(id, name…)` |
| `model.NewParallelGateway` | `gateway.NewParallel` |
| `model.NewInclusiveGateway` | `gateway.NewInclusive` |
| `model.NewEventBasedGateway` | `gateway.NewEventBased` |
| `model.NewServiceTask` / `NewUserTask` / `NewReceiveTask` / `NewSendTask` / `NewBusinessRuleTask` / `NewSubProcess` / `NewCallActivity` | `activity.New*` (same suffix) |

The concrete **types** move with their constructors (e.g. `activity.ServiceTask`,
`event.BoundaryEvent`, `gateway.ExclusiveGateway`). Consumers rarely name them —
constructors return `definition.Node`, and field access goes through accessors.

### 3.2 Option renames (land natively in the leaves; no aliases)

| Old | New |
|---|---|
| `WithICEDeadline` | `event.WithCatchDeadline` |
| `WithICEReminder` | `event.WithCatchReminder` |
| `WithTimerDuration` | `event.WithCatchTimer` |
| `WithSignalName` | `event.WithCatchSignal` |
| `WithMessageNameAndKey` | `event.WithCatchMessage` |
| `WithESPNonInterrupting` | `event.WithEventSubProcessNonInterrupting` |
| `BoundaryNonInterrupting` | `event.WithBoundaryNonInterrupting` |
| `WithBoundaryTimer/Signal/Message/ErrorCode` | `event.WithBoundary*` (unchanged names, new pkg) |
| `WithStartSignal/Message/Timer` | `event.WithStart*` (unchanged names, new pkg) |
| `WithThrowSignal/CompensateRef/ThrowName` | `event.WithThrow*` (unchanged names, new pkg) |
| activity options (`WithName`, `WithActionName`, `WithAction`, `WithActionFunc`, `WithRetryPolicy`, `WithRecoveryFlow`, `WithCompensation`, `WithCancelHandler`, `WithDeadline`, `WithReminder`, `WithEligibilityExpr`, `WithEligibilityPrivileges`, `WithCorrelationKey`) | `activity.With*` (unchanged names, new pkg) |

The start / catch / boundary event-option families become symmetric
(`WithStartTimer` / `WithCatchTimer` / `WithBoundaryTimer`, etc.).

`WithName` is duplicated per leaf (`activity.WithName`, `event.WithName`) — each
sets `Base` name on that family's kinds. Gateways take an optional name as a
trailing variadic (no options), unchanged.

## 4. The registration / cycle-break design

The import cycle in a naive split is: `definition` needs concrete types (to
serialize/validate) while the leaves need `definition` (for `Node`,
`ProcessDefinition`, embeds). We break it with a **driver-registration** pattern
(the `image/png`, `database/sql` idiom):

- `definition` owns a registry keyed by `NodeKind`:

  ```go
  // definition/registry.go
  type NodeSpec struct {
      Name     string                          // stable JSON discriminator, e.g. "serviceTask"
      FromWire func(Base, NodeWire) Node        // reconstruct concrete node
      ToWire   func(Node, *NodeWire)            // project concrete node into the wire union
  }
  func RegisterKind(k NodeKind, s NodeSpec)     // called by leaves in init()
  ```

- `NodeWire` (formerly the unexported `nodeWire`) is **exported** so leaves can
  build/project it. `Base`, `ActivityFields`, `WaitFields` are exported embeddable
  structs with the setters the options need (e.g. `Base.SetName`).
- Each leaf registers its kinds in `init()`:

  ```go
  // definition/activity/servicetask.go
  func init() {
      definition.RegisterKind(definition.KindServiceTask, definition.NodeSpec{
          Name:     "serviceTask",
          FromWire: func(b definition.Base, w definition.NodeWire) definition.Node {
              return ServiceTask{Base: b, ActivityFields: w.Activity(), Action: w.Action}
          },
          ToWire: func(n definition.Node, w *definition.NodeWire) {
              st := n.(ServiceTask); w.Action = st.Action; w.PutActivity(st.ActivityFields)
          },
      })
  }
  ```

- `definition` imports **no** leaf; the leaves import `definition`. **No cycle.**
- `ProcessDefinition.MarshalJSON` iterates nodes and calls the registered
  `ToWire`; `UnmarshalJSON`/YAML look up `FromWire` by kind. `NodeKind.String`/
  JSON name come from the same registry (the `nodeKindNames` map is derived from
  registered specs; `KindUnspecified` registered by `definition` itself).

### 4.1 Correctness guarantee

Deserializing needs the kinds registered. `definition/kinds` blank-imports the
three leaves; **all deserialization paths (persistence, REST/gRPC decode) import
`definition/kinds`.** If a kind is not registered, `FromWire` lookup returns a
**loud, actionable error** — `workflow-definition: node kind %q not registered
(blank-import github.com/kartaladev/wrkflw/definition/kinds)` — never a silent
zero-value. A package-level test asserts all 19 kinds round-trip, guarding against
a leaf forgetting to register.

## 5. Internal simplifications (behavior-preserving)

Applied inside `definition` as the machinery is reshaped:

- **`WaitFields`** = `{DeadlineDuration, DeadlineFlow, DeadlineAction,
  ReminderEvery, ReminderAction}` with methods `deadline()`/`reminder()`.
  Embedded by `ActivityFields` **and** by `event.IntermediateCatchEvent`
  (removing that type's field duplication).
- **`ActivityFields`** embeds `WaitFields` + `{RetryPolicy, RecoveryFlow,
  CompensationAction, CancelHandler}` with `retry()`/`recoveryFlow()`.
- Accessors collapse to interface assertions:
  `func DeadlineOf(n Node) (d,f,a string) { if w, ok := n.(interface{ deadline() (string,string,string) }); ok { return w.deadline() }; return "","","" }`
  — likewise `ReminderOf`, `RetryPolicyOf`, `recoveryFlowOf`. `ActionOf` /
  `InlineActionOf` use a shared `taskAction` embed on `ServiceTask` +
  `BusinessRuleTask`.
- **YAML** decodes through the exported wire type (or a thin shim that differs
  only in the two divergent fields: `Kind string` and nested `subprocess`),
  deleting the 25-field `nodeYAML` duplicate and its manual copy.
- **`validate.go`** per-call local maps (`gatewayKinds`, `errorBoundaryHostKinds`)
  hoisted to package vars, joining the existing `activityKinds`.

## 6. Builder & fluent authoring

- `definition` keeps the base builder: `definition.New(id, version)` →
  `DefinitionBuilder` with `.Add(Node)`, `.Connect(from, to, …FlowOption)`,
  `.RegisterAction`, `.RegisterActionFunc`, `.CancelActions`, `.Build`, `.Loader`.
  This is cycle-free (it only knows `Node`).
- `definition/build` provides the terse fluent surface, importing the leaves:

  ```go
  import (
      "github.com/kartaladev/wrkflw/definition/activity"
      "github.com/kartaladev/wrkflw/definition/build"
  )
  def, err := build.New("order", 1).
      AddStart("s").
      AddServiceTask("charge", activity.WithActionName("charge-card")).
      AddEnd("e").
      Connect("s", "charge").Connect("charge", "e").
      Build()
  ```

  `build.Builder` wraps `definition.DefinitionBuilder`; each `AddX` is
  `return b.Add(<leaf>.New…(args…))`. Options are the leaf option types (users
  import the relevant leaf for options), keeping `build` a thin, mechanical mirror.
- The plain `definition.New(...).Add(event.NewStart(...))...` path remains for
  programmatic/dynamic construction and is what YAML loading uses internally.

## 7. Non-goals

- No change to wire/JSON/YAML **format** — stored JSONB definitions round-trip
  byte-compatibly (the discriminator strings and field names are unchanged).
- No change to validation **rules**, retry math, or engine/runtime behavior.
- No new node kinds, options semantics, or authoring forms.
- Not adopting code generation — the registry makes per-kind wiring data-driven
  without a generator.

## 8. Migration (repo-wide, breaking)

~1,800 call sites across 158 files. Executed in phases, each ending green
(`go build ./...`, touched-package tests, no import cycles):

0. **Baseline + branch.** Confirm green; branch `refactor/definition-relocation`.
1. **Rename `model` → `definition`.** Directory move, package clause, all import
   paths + qualified identifiers repo-wide. Pure rename, no relocation.
2. **Internal cleanup** inside `definition` (registry, exported `NodeWire`/`Base`/
   `ActivityFields`/`WaitFields`, accessor collapse, YAML dedup, validate hoist) —
   still one package, behavior-preserving, existing tests green.
3. **Relocate structs into leaves** `event`/`gateway`/`activity` + `kinds` bundle;
   `definition` keeps interface + machinery + registry; leaves register in
   `init()`. Move each kind's tests with it.
4. **`definition/build`** fluent package; move `AddX` there; delete the old
   builder-fluent file.
5. **Rewrite call sites** repo-wide to family packages + fluent `build`; apply
   option renames; wire `definition/kinds` into deserialization paths.
6. **Examples, READMEs, ADR-0090, docs**; final gates (race, coverage ≥85% per
   touched package, lint clean, `go test ./...`, import-cycle check).

## 9. Verification checklist

- [ ] `go build ./...` green after every phase.
- [ ] `go test ./...` green at the end (no regressions).
- [ ] Per touched package: `-race` clean, line coverage ≥ 85%.
- [ ] `golangci-lint run ./...` clean.
- [ ] No import cycles: `definition` imports no leaf; leaves import only
      `definition`; `kinds`/`build` import leaves.
- [ ] Round-trip test: all 19 kinds marshal→unmarshal identically (JSON + YAML).
- [ ] Unregistered-kind path returns the loud error, tested.
- [ ] Stored-format compatibility: a golden JSON definition from before the change
      still decodes and re-encodes byte-identically.
- [ ] `examples/` build and run against the new API.
- [ ] READMEs (`definition/README.md`, root `README.md`) updated to the new
      packages and names.
- [ ] ADR-0090 recorded (Nygard template).

## 10. Risks & mitigations

- **Serialization regression** (highest risk). Mitigated by the golden round-trip
  test, the all-kinds registry test, and keeping the wire format byte-identical.
- **Missed call site** in the 1,800-site rewrite. Mitigated by `go build ./...`
  as a hard compiler gate — an unmigrated reference to `model.*` fails to compile.
- **Registry not populated** on a deserialization path. Mitigated by
  `definition/kinds` + the loud error + wiring the bundle into persistence/transport.
- **`core`-like generic naming.** Avoided: the machinery stays in the top
  `definition` package (no separate `core`); only construction moves to leaves.

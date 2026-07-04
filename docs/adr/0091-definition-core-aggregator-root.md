# 0091 — `definition` becomes an aggregator so the fluent builder starts from root

- **Status:** Accepted
- **Date:** 2026-07-04
- **Refines:** [0090](0090-definition-package-node-family-relocation.md)

## Context

ADR-0090 relocated the node kinds into `definition/{event,gateway,activity}` leaf
packages and put the fluent per-kind builder in `definition/build`, entered via
`build.New(...)`. The maintainer wants the fluent builder to start from the root
package for ergonomics — `definition.NewBuilder(id, version)` returning the fluent
builder, with `Build()` returning `*definition.ProcessDefinition` — plus:

- fluent methods named for the full node type (`AddStartEvent`,
  `AddExclusiveGateway`, `AddServiceTask`, …) for clarity, and
- `SequenceFlow` (and the flow options) moved into their own `flow` package.

The blocker is an import cycle. For `definition.NewBuilder` to return
`build.Builder`, root `definition` must import `build`; `build` imports the leaves
(to call their constructors); the leaves import `definition` (for `Node`,
`ProcessDefinition`, `Base`, `RegisterKind`, …):

```
definition → build → activity → definition   ❌
```

A registry/interface indirection does not help: the fluent `AddServiceTask`
signature references `activity.ServiceTaskOption`, so `definition` would still
import a leaf.

## Decision

Adopt the **aggregator + core-leaf** topology. The core types move out of the
root package; the root package becomes a thin aggregator.

- **`definition/model`** — holds what was in the root package: `Node`, `NodeKind`,
  `ProcessDefinition`, `RetryPolicy`, `Validate`, JSON/YAML (de)serialization, the
  kind registry (`NodeSpec`/`RegisterKind`), the shared embeds (`Base`,
  `ActivityFields`, `WaitFields`, `TaskAction`, `NodeWire`), the core builder
  (`definitionBuilder`/`DefinitionBuilder`/`DefinitionLoader` with generic `Add`),
  and the sentinel errors. Imports `flow`; imports no leaf.
- **`flow`** — `SequenceFlow` + `Option` (`WithFlowID`, `WithCondition`,
  `AsDefault`). A leaf; imports nothing internal.
- **`definition/event` / `gateway` / `activity`** — unchanged in spirit; now import
  `model` (and `flow`) instead of the root package, and register with `model`.
- **`definition/build`** — imports `model` + the leaves + `flow`. Defines
  `Builder` with the full-name fluent methods (`AddStartEvent`, …); `New(...)` and
  `Build()` return `*model.ProcessDefinition`.
- **`definition`** (root) — the **aggregator**: imports `model`, `build`, `flow`.
  Re-exports the public surface as aliases (`type Node = model.Node`,
  `type ProcessDefinition = model.ProcessDefinition`, `type SequenceFlow =
  flow.SequenceFlow`, `var Validate = model.Validate`, the `KindX` constants, the
  accessors, …) and defines
  `func NewBuilder(id, version int) *build.Builder { return build.New(id, version) }`.
  The `ErrX` validation/builder sentinels are **not** re-exported — check them from
  `definition/model` (e.g. `errors.Is(err, model.ErrNoStartEvent)`) to keep the
  root surface minimal.

Dependency graph (acyclic; nothing imports the root aggregator):

```
definition (root) → build, model, flow
build             → model, event, gateway, activity, flow
event/gateway/activity → model, flow
model             → flow
flow              → (stdlib only)
```

`definition.NewBuilder(...)` now returns the fluent `*build.Builder`; its
`AddStartEvent(...)`/`AddServiceTask(...)`/… mirror the leaf constructors; and
`Build()` yields `*definition.ProcessDefinition`. Because the root package
transitively imports the leaves (via `build`), importing `definition` also
populates the kind registry — the `definition/kinds` bundle remains for
deserialization paths that import only `model`.

## Consequences

- **The maintainer's ergonomics are met**: authoring starts from
  `definition.NewBuilder(...)` with a fully fluent, clearly-named chain, and
  yields `*definition.ProcessDefinition`.
- **Most call sites are unaffected** — `definition.Node`, `definition.Validate`,
  `definition.ProcessDefinition`, `definition.KindX`, the accessors, etc. keep
  working through the aggregator's re-exports. `definition.NewBuilder(...)` returns
  a `*build.Builder`, which still offers `Add`/`Connect`/`Build`, so existing
  `.Add(...)` chains compile unchanged.
- **`SequenceFlow` is now `flow.SequenceFlow`** (aliased as `definition.SequenceFlow`).
- **Cost**: re-restructures the core merged in ADR-0090; a sizeable but mechanical
  aggregator shim; the core `.go` files move into `definition/model`; the leaves
  and `build` re-point their imports.
- **`model` is a public sub-package** but consumers rarely name it directly — they
  use the root aliases. It is the one piece of new surface.
- **Wire format and behaviour remain frozen** (unchanged from ADR-0090); the
  golden round-trip and all-kinds tests continue to guard it.

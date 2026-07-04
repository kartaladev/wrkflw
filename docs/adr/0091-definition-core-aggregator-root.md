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
- **`definition`** (root) — imports `model`, `build`, `flow`, and holds **only the
  two authoring constructors** (the one place that can import `build` without a
  cycle): `func NewBuilder(id, version int) *build.Builder` (Go, fluent) and
  `func NewLoader(r io.Reader) (model.DefinitionLoader, error)` (YAML). It does
  **not** re-export the rest of the surface — every other symbol is used directly
  from its source package (`model.Node`, `model.ProcessDefinition`,
  `model.Validate`, `model.KindX`, the accessors and `ErrX` sentinels;
  `flow.SequenceFlow`). One canonical home per symbol, no duplicate names.

Dependency graph (acyclic; nothing imports the root aggregator):

```
definition (root) → build, model, flow
build             → model, event, gateway, activity, flow
event/gateway/activity → model, flow
model             → flow
flow              → (stdlib only)
```

`definition.NewBuilder(...)` returns the fluent `*build.Builder`; its
`AddStartEvent(...)`/`AddServiceTask(...)`/… mirror the leaf constructors, and
`Build()` yields `*model.ProcessDefinition`. Because the root package
transitively imports the leaves (via `build`), importing `definition` also
populates the kind registry — the `definition/kinds` bundle remains for
deserialization paths that import only `model`.

## Consequences

- **The maintainer's ergonomics are met**: authoring starts from a single,
  well-named root — `definition.NewBuilder(...)` (Go) or `definition.NewLoader(r)`
  (YAML) — with a fully fluent, clearly-named chain.
- **One canonical home per symbol.** The root package exposes *only* the two
  constructors; the rest is used from its source package (`model.Node`,
  `model.ProcessDefinition`, `model.Validate`, `model.KindX`, the accessors and
  `ErrX` sentinels; `flow.SequenceFlow`). No duplicate names, no aliases.
- **Call sites were rewritten** — this is the tradeoff. ~1,600 `definition.X`
  references across ~134 files became `model.X` / `flow.SequenceFlow`. Consumers
  now import `definition` for the entry, `model` for the types, `flow` for flows,
  and the leaf packages for constructors. (An earlier iteration re-exported
  everything from the root as aliases; that facade was dropped to keep a single,
  unambiguous home per symbol.)
- **Cost**: re-restructures the core merged in ADR-0090; the core `.go` files move
  into `definition/model`; the leaves and `build` re-point their imports; the
  repo-wide call-site rewrite above.
- **`model` is the de-facto types package** consumers import most; `definition` is
  a thin two-function entry. `model`/`flow` are new public sub-packages.
- **Wire format and behaviour remain frozen** (unchanged from ADR-0090); the
  golden round-trip and all-kinds tests continue to guard it.

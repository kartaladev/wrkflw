# Spec — Surface definition-scoped action metadata in snapshots + gRPC snapshot RPCs

Date: 2026-06-27
Status: Accepted (autonomous backlog program, track T1)
Relates to: ADR-0063 (definition-scoped action catalog), ADR-0038 (admin DTOs)

## Problem

ADR-0063 introduced definition-scoped and inline service actions, but none of that
metadata is surfaced to consumers. `runtime.NewInstanceSnapshot` even accepts a
`*model.ProcessDefinition` it ignores (the `_` parameter). On the gRPC side there is
no snapshot RPC at all — only `GetInstance` returning the basic `Instance` message
(tokens/history/tasks/incidents are gRPC-invisible). The REST transport already exposes
`GET /instances/{id}/snapshot` and `.../actionable`.

Backlog item (HANDOVER): "Surface the def-scoped catalog in InstanceSnapshot/ActionableView
DTOs + gRPC snapshots" + "deferred gRPC GetInstanceSnapshot RPC mirroring the REST task".

## Goals

1. Surface, per process instance, which service/business-rule nodes bind to which action,
   and whether that binding is inline (node-local) or by name — derived purely from the
   process definition.
2. Surface the definition's scoped-action names.
3. Add gRPC `GetInstanceSnapshot` and `GetActionableView` RPCs mirroring the REST endpoints,
   returning the full snapshot/actionable projections as proto messages.

## Non-goals (YAGNI)

- Resolving the *tier* an action ultimately resolves to (inline > scoped > global) in the
  snapshot. That requires the runner's global catalog at mapping time and is not needed to
  understand a definition's action wiring. Inline-vs-named + scoped-name list is sufficient.
- Surfacing inline actions on `ActionableView`: that DTO is human-task-routing focused
  (`NextAction` = outgoing sequence flows). Service-action bindings are not relevant there;
  it stays unchanged (documented). The gRPC `GetActionableView` simply mirrors the existing
  REST projection so gRPC reaches parity.
- Serializing Go functions / YAML authoring of inline actions (impossible by design).

## Design

### 1. `model.ProcessDefinition.ScopedActionNames() []string`

`action.Catalog` is intentionally minimal (`Resolve(name) (ServiceAction, bool)`) and not
enumerable — do **not** widen it. The `DefinitionBuilder` already accumulates scoped actions
in `b.actions map[string]action.ServiceAction`. At `Build()`, store a sorted `[]string` of
those names on the definition and expose:

```go
// ScopedActionNames returns the sorted names registered in the definition-scoped
// action catalog, or nil when none were registered.
func (d *ProcessDefinition) ScopedActionNames() []string
```

Additive, deterministic (sorted), nil when empty. This is the only `model` change.

### 2. Runtime DTO enrichment (`runtime/instance_snapshot.go`)

New view type + two new `InstanceSnapshot` fields, populated by `NewInstanceSnapshot` only
when `def != nil` (the previously-ignored `def` parameter is now used):

```go
type ActionBindingView struct {
    NodeID   string `json:"node_id"`
    NodeKind string `json:"node_kind"`        // "serviceTask" | "businessRuleTask"
    Action   string `json:"action,omitempty"` // explicit name; empty => default-by-id (key == NodeID)
    Inline   bool   `json:"inline"`           // node carries an inline action.ServiceAction
}

// added to InstanceSnapshot:
ScopedActions  []string            `json:"scoped_actions,omitempty"`
ActionBindings []ActionBindingView `json:"action_bindings,omitempty"`
```

Population: iterate `def` nodes (a definition node accessor that yields all nodes — confirm
the iteration API: `def.StartNodes()` + traversal is wrong; use the node map. If no public
"all nodes" accessor exists, add a minimal `Nodes() []Node` accessor to model, additive).
For each `model.ServiceTask` / `model.BusinessRuleTask`: emit a binding with
`Action = model.ActionOf(n)` and `Inline = model.InlineActionOf(n) != nil`. `ScopedActions`
= `def.ScopedActionNames()`. Bindings sorted by NodeID for determinism.

REST automatically surfaces the new fields (handlers already pass `def`). Add a REST test
asserting they appear.

### 3. gRPC proto + RPCs (`transport/grpc`)

Add to `proto/workflow.proto`:
- messages mirroring the full DTOs: `TokenView`, `NodeVisitView`, `TaskView`, `IncidentView`,
  `ActionBindingView`, `InstanceSnapshot`; `NextAction`, `ActionableTask`, `ActionableView`.
- `rpc GetInstanceSnapshot(GetInstanceRequest) returns (InstanceSnapshotResponse);`
- `rpc GetActionableView(GetInstanceRequest) returns (ActionableViewResponse);`

Reuse the existing `GetInstanceRequest`. Wrap each in a `*Response` message for forward-compat
(matches the existing `InstanceResponse` convention).

Regeneration: `buf` is not installed; use the documented raw-protoc fallback —
`go install google.golang.org/protobuf/cmd/protoc-gen-go@latest` +
`google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest`, then the `protoc` invocation in
`buf.gen.yaml`'s comment. Commit regenerated `workflowpb/*.pb.go`.

Server (`server.go`): implement both RPCs via `svc.GetInstanceWithDefinition(ctx, id)` (same
service call the REST handlers use), mapping `runtime.NewInstanceSnapshot` /
`runtime.NewActionableView` to proto. Reuse existing helpers (`structToMap`/`structFromMap`,
timestamp mapping). Test with `bufconn` (existing gRPC test pattern).

## Testing

- `model`: `ScopedActionNames` returns sorted names / nil when empty (table test).
- `runtime`: `NewInstanceSnapshot` populates `ScopedActions` + `ActionBindings` for a def with
  inline, named, and default-by-id service/business-rule tasks; nil `def` => empty.
- `transport/rest`: snapshot response JSON includes the new fields.
- `transport/grpc`: `GetInstanceSnapshot`/`GetActionableView` over bufconn return the mapped
  projections incl. action metadata; not-found => `codes.NotFound`.

Gate: touched pkgs ≥85% line coverage, `go test -race ./...` green, lint 0, gofmt clean,
engine import-purity unaffected (engine untouched).

## Risks

- `model` gains a tiny additive accessor (and possibly `Nodes()`); justified by the feature,
  no behavior change. Documented in ADR-0068.
- Proto regeneration tooling must be installed; deterministic and documented.

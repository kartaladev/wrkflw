# Fluent per-node-type builder methods — design

Date: 2026-06-29
Status: **Approved** (maintainer chose AddUserTask naming + all-19-kinds coverage, 2026-06-29).
Relates to: the existing `model.DefinitionBuilder` (`model/builder.go`), node constructors
(`model/node_constructors.go` / `model/node.go`), ADR-0041 (`model.Node` is an interface).

## Goal

Add ergonomic, discoverable **fluent per-node-type methods** to `model.DefinitionBuilder` so a
consumer authoring a definition in Go writes `b.AddServiceTask("t", model.WithActionName("do"))`
instead of `b.Add(model.NewServiceTask("t", model.WithActionName("do")))`. The whole value is
IDE/autocomplete discoverability of the node palette and less `model.New…` boilerplate.

## Decisions

1. **Thin sugar, 1:1 with constructors.** Each `AddX` method mirrors the corresponding `NewX`
   constructor's signature exactly and delegates to the existing `Add(NewX(...))`. No new option
   types, no behavioral logic — pure forwarding. (Rejected: a per-type sub-builder like
   `b.ServiceTask("t").WithAction(...).Done()` — it duplicates the established functional-options
   idiom, adds two ways to set the same options, and is more surface for no real gain. YAGNI.)
2. **Naming mirrors the constructors/kinds** (`AddUserTask`, not `AddHumanTask`) so the fluent method,
   the `New*` constructor, the `Kind*` constant, and the YAML discriminator (`userTask`) all agree.
3. **All 19 node kinds** get a method — complete palette, no "which ones have sugar?" ambiguity.
4. **Additive, non-breaking.** The generic `Add(n Node)` stays (still needed for YAML reconstruction,
   programmatically-built nodes, and any future kind). Existing call sites are untouched.
5. **No new error path.** Like `Add`, the `AddX` methods return `*DefinitionBuilder` and never an
   error; all validation stays deferred to `Build()` (duplicate IDs, nil sub-process, structure, etc.).
   Constructor-level invariants behave exactly as they do via `Add(NewX(...))` today.

## The methods (signatures mirror the constructors exactly)

Each returns `*DefinitionBuilder`. Option parameter types are the same (unexported) option types the
constructors already use — consumers pass the exported `model.With*` option functions, identical to
calling `New*` today.

Events:
- `AddStartEvent(id string, opts ...startEventOption)`
- `AddEndEvent(id string, name ...string)`
- `AddTerminateEndEvent(id string, name ...string)`
- `AddErrorEndEvent(id, errorCode string, name ...string)`

Gateways:
- `AddExclusiveGateway(id string, name ...string)`
- `AddParallelGateway(id string, name ...string)`
- `AddInclusiveGateway(id string, name ...string)`
- `AddEventBasedGateway(id string, name ...string)`

Activities:
- `AddServiceTask(id string, opts ...serviceTaskOption)`
- `AddUserTask(id string, roles []string, opts ...userTaskOption)`
- `AddReceiveTask(id, messageName string, opts ...receiveTaskOption)`
- `AddSendTask(id, messageName string, opts ...sendTaskOption)`
- `AddBusinessRuleTask(id string, opts ...businessRuleOption)`
- `AddSubProcess(id string, sub *ProcessDefinition, opts ...activityOption)`
- `AddCallActivity(id, defRef string, opts ...activityOption)`

Subprocess / intermediate / boundary:
- `AddEventSubProcess(id string, sub *ProcessDefinition, opts ...eventSubProcessOption)`
- `AddIntermediateCatchEvent(id string, opts ...catchOption)`
- `AddIntermediateThrowEvent(id string, opts ...throwOption)`
- `AddBoundaryEvent(id, attachedTo string, opts ...boundaryOption)`

Each body is a one-liner: `return b.Add(NewX(<args>))`.

Example after refactor:
```go
def, err := model.NewDefinition("loan", 1).
    RegisterAction("score", score).
    AddStartEvent("start").
    AddServiceTask("risk", model.WithActionName("score")).
    AddUserTask("approve", []string{"manager"}, model.WithEligibilityExpr("amount < 1e6")).
    AddExclusiveGateway("gw").
    AddEndEvent("end").
    Connect("start", "risk").Connect("risk", "approve").
    Connect("approve", "gw").Connect("gw", "end").
    Build()
```

## File structure

- `model/builder_fluent.go` — the 19 `AddX` methods (keeps `builder.go` focused on core build/validate).
- `model/builder_fluent_test.go` — black-box (`model_test`) tests.

## Testing

- For each `AddX`: assert the builder records a node whose `Kind()`/`ID()` match the expected kind/id,
  and that an option threads through where applicable (e.g. `AddServiceTask` + `WithActionName` →
  `ActionOf(node) == name`; `AddUserTask` roles present).
- **Equivalence test:** a definition built with `AddX` methods is deep-equal (or structurally equal on
  nodes) to the same definition built with `Add(NewX(...))` — proves pure forwarding.
- One end-to-end fluent `Build()` that validates green, mirroring `builder_test.go` style.
- Table-driven where ≥2 cases share the call shape (project `table-test` skill, `assert`-closure form).

## Non-goals

- No change to `Add`, `Connect`, `Build`, validation, YAML, or any node constructor.
- No typed/guided sub-builders; no fluent `Connect` sugar (separate idea if ever wanted).
- No deprecation of `Add` (it remains first-class).

## Consequences

- Library ergonomics improve (the load-bearing priority): the node palette is autocomplete-discoverable
  and authoring is terser, with zero migration cost (purely additive).
- ~19 small methods to keep in sync if a constructor signature changes; the equivalence test + the fact
  that each method forwards to the constructor keep drift visible (a constructor signature change breaks
  the forwarding method at compile time).

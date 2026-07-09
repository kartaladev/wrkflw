# 115. Validation engine-decides seam: `engine.TargetNode` + `definition/model/validate` / `runtime/validation` split

- Status: Accepted
- Date: 2026-07-09

## Context

ADR-0110 introduced node-level input validation at three runtime boundaries: `ProcessDriver.Drive`
(start vars), `ProcessDriver.DeliverMessage` (message payload), `TaskService.Complete` (completion
output). Each boundary independently resolved "which node does this trigger target" using either
the flat `(*ProcessDefinition).Node(id)` lookup or a boundary-local variant built on it
(`MessageTargetNode`). That lookup only scans a definition's top-level `Nodes` slice — a node
nested inside a sub-process is invisible to it, so validation for that node was silently skipped
rather than rejecting: the opposite of the fail-closed guarantee the feature is supposed to
provide (see ADR-0110's Revision section for the full defect writeup).

Scope-correct node resolution — "given a live instance and a trigger, which node in which nested
scope does it target" — already exists, but only inside the engine: `defForScope` and the
scope/token/arm bookkeeping `handleMessageReceived` reads to dispatch a `MessageReceived` trigger
(`engine/step_triggers.go`) are unexported, precisely because they encode `Step`'s own dispatch
semantics. Reimplementing an equivalent resolution in the runtime (as the boundary-local variants
tried) means either exporting all of that internal bookkeeping — a leak — or accepting a resolver
that can silently drift from what `Step` actually dispatches to, which is worse than not
validating at all: it can validate the *wrong* node, or none.

A second constraint comes from the engine's own effect model: `Step` commits state changes and
only *afterward* emits commands/events for the runtime to perform (commit-before-perform,
ADR-0002/0044). A hypothetical `ValidateInput` command emitted by `Step` could not gate the very
commit that already happened — validation-as-a-command is a non-starter under this effect model.
Whatever runs validation must decide *before* `Step`/commit, not be dispatched by it.

Separately, validator *execution* is deliberately impure and, for the `validate/callback` adapter,
runs arbitrary Go closures — including ones that make external calls (a lookup, a remote check).
Running that inside `Step` would break the engine core's purity contract (`purity_test.go`) and
determinism: `Step` must produce identical output for identical input on replay, which an
arbitrary closure cannot guarantee.

## Decision

We split "decide which node, and what strategy" (pure) from "run the strategy" (impure) across
two packages, with the engine owning only the pure half:

**`engine.TargetNode(def *model.ProcessDefinition, st InstanceState, trg Trigger) (model.Node,
bool)`** (`engine/target_node.go`) is a pure query, validation-agnostic — it has no notion of
`ValidationStrategy` or any validation library. It mirrors `Step`'s own trigger dispatch
tier-for-tier, reading the same unexported scope/token/arm helpers `handleMessageReceived` reads,
so the two can never disagree on which node wins:

- `StartInstance` → the definition's sole start node (`def.StartNodes()`, `len == 1` required).
- `MessageReceived` → the same 4-tier priority order as `handleMessageReceived`: event-based-
  gateway arm, then message-boundary arm, then event-subprocess arm, then a standalone parked
  token — each tier resolved to a `(nodeID, scopeID)` pair, then `nodeInScope` resolves `nodeID`
  against the `ProcessDefinition` that actually governs `scopeID` (`defForScope`: the top-level
  def for the root scope, a sub-process's own nested definition otherwise).
- `HumanCompleted` → the parked task token's node, resolved in that token's own `ScopeID` the same
  way.
- Any other trigger kind → `(nil, false)`, harmless if called.

Because `nodeInScope` always resolves through `defForScope` rather than a flat `def.Node` lookup,
a node nested arbitrarily deep in a sub-process is now found correctly — this is the concrete fix
for the fail-open defect.

**`model.ValidationStrategyFor(n model.Node) validate.ValidationStrategy`**
(`definition/model/validation_wire.go`) extracts the strategy a node carries, via the node kind's
registered `NodeSpec.ValidationGet` function rather than a type switch over concrete node types —
consistent with how `model` already avoids importing the leaf node packages
(`definition/activity`, `definition/event`) for anything else. Returns `nil` for a node kind with
no validation slot, or one whose slot is unset.

**The runtime composes both and owns the impure `Gate`.** `ProcessDriver.validateInput`
(`runtime/processdriver.go`) calls `engine.TargetNode`, then `model.ValidationStrategyFor`, then
(if a strategy exists) `driver.gate.Validate` — wired into `deliverLoop` *before* the per-trigger
`Step` call, so a rejection (`runtime/validation.ErrInvalidInput`) returns before the store
`Create`/`Commit` runs. No validator ever executes inside `Step`; `Step` stays pure and
deterministic, and a validated trigger, once accepted, is applied exactly once — never replayed
through validation again.

**Package layout — `definition/model/validate` (authoring) + `runtime/validation` (execution):**

```
definition/model/validate/        package validate     — the declarative port + registry (authoring)
  validate.go     ValidationStrategy, Validator, DescribableStrategy, ValidationDescriptor
  registry.go     Register, DefaultRegistry, Registry.Strategy (reconstruct-by-kind)
  expr/ callback/ jsonschema/ avro/   adapters, each self-registering via init()

runtime/validation/               package validation   — the executor (execution)
  gate.go         Gate (compile-once memoizing cache), ErrInvalidInput
```

`definition/model/validate` imports nothing from `runtime` or `engine` — it is a standalone,
declarative vocabulary. `model` (and the leaf node packages that define per-kind slots) import it
to give nodes a validation slot; this is a normal parent→child dependency, no cycle.
`runtime/validation` imports `definition/model/validate` to build and run a `Validator` from a
node's `ValidationStrategy` — a normal runtime→definition dependency. The engine depends on the
validation *port* only transitively, through `model.Node`'s validation-carrying types — `engine`
itself never imports `definition/model/validate` or `runtime/validation`, and never holds a
`Gate`. The two package clauses (`validate` vs `validation`) are deliberately distinct so a caller
importing both — the runtime driver — needs no import alias.

`ValidationStrategy` and `Validator` are a mutually-referential pair
(`ValidationStrategy.NewValidator() (Validator, error)`), so they are colocated together in
`definition/model/validate` rather than split by "declares" vs "executes" — splitting them across
packages would force a dependency cycle (the executor package would need to import the
declaration package for `ValidationStrategy`, and the declaration package would need to import the
executor package for `Validator`'s type in `NewValidator`'s signature). Colocating both in the
lower layer preserves the intended runtime→definition dependency direction; only `Gate` and
`ErrInvalidInput` — the pieces that are specifically about *running* a `Validator` from the
runtime's perspective, not about defining what one is — live in `runtime/validation`.

## Consequences

- Nested-subprocess validation now works and fails closed: a `UserTask`/`ReceiveTask`/
  `IntermediateCatchEvent` nested inside a sub-process is resolved via `defForScope` like every
  other node, not silently skipped.
- One resolver (`engine.TargetNode`) is shared by all three input-bearing boundaries and is
  guaranteed to agree with `Step`'s own dispatch, because it reads the same unexported helpers —
  eliminating the drift risk a runtime-side reimplementation would carry.
- `Step` stays pure: no `Gate`, no validator, no I/O in the engine core; determinism and replay
  safety are unaffected by this feature.
- The Gate caches compiled validators keyed by the strategy's **descriptor** (`kind` + `schema`),
  not by node location: `runtime/validation.Gate.Validate(ctx, strategy, input)` derives the key
  itself. This is what actually determines a compiled validator, so it is correct across scopes (a
  node nested in a sub-process can share an id with a top-level node yet carry a different schema —
  keying by node id would collide and validate against the wrong schema), bounded (finite distinct
  schemas — no per-instance cache growth), and shares one compiled validator across every node that
  declares the same schema. Non-describable (`callback`) strategies have no descriptor and are built
  fresh each call (their `NewValidator` is a trivial identity wrap). This replaces the three
  independent hand-rolled boundary keys.
- `validate/callback` (arbitrary Go closures) remains usable, but strictly in-memory: it has no
  `Descriptor()` and cannot round-trip through wire/YAML (`ProcessDefinition.MarshalJSON` fails
  closed with `ErrUnserializableValidation` if one reaches serialization) — this is unchanged from
  ADR-0110, restated here because the engine-decides split does not relax it.
- Trade-off: the runtime (`ProcessDriver`) now loads and owns the `Gate`, a piece of executor
  state the engine has no visibility into; a consumer embedding only `engine` (no `runtime`) gets
  no validation at all, which is intended — validation is a runtime-execution concern, not a core
  one.
- Trade-off: completion validation now surfaces from `ApplyTrigger` (via `deliverLoop`), not from
  `TaskService.Complete` directly — a caller inspecting only `Complete`'s return value no longer
  sees a validation rejection; it appears as the error `ApplyTrigger` returns once the
  `HumanCompleted` trigger reaches the driver. `TaskService.Complete` itself now does authorization
  and trigger emission only.
- See ADR-0110's Revision section for the defect history and full revert list this decision
  supersedes, and `docs/specs/2026-07-09-input-validation-redesign.md` for the implementation-
  level design record (including per-revert commit references).

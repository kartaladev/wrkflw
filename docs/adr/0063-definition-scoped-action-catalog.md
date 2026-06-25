# 63. Definition-scoped action catalog, optional action names, and node-local inline actions

- Status: Accepted
- Date: 2026-06-25

## Context

Until this change every `ServiceTask` and `BusinessRuleTask` resolved its action against a
**single global `action.Catalog`** supplied once to `runtime.NewRunner`. This created three
rigidities:

1. **No per-definition isolation.** All actions a definition needs must live in the one
   global catalog; definitions cannot carry their own private actions, and there is no
   scoped-override story.
2. **Mandatory, verbose names.** Every task requires an explicit action name even when the
   node id is the only natural key.
3. **No inline (node-local) actions.** A consumer must pre-register every function in the
   global catalog, away from the definition that uses it, preventing co-location of a node
   and its behaviour.

Additionally, `BusinessRuleTask` had no execution strategy — it was modelled but never
driven by the engine.

A related constraint shapes the design: process definitions are persisted as JSONB
(`model/node_wire.go`) and Go functions cannot be serialized. However, **execution always
operates on the caller-supplied in-memory `*model.ProcessDefinition`** (every `Runner`
entry-point — `Run`, `Deliver`, `ResolveIncident`, `CancelInstance` — takes a
`*model.ProcessDefinition`, and timer/call-activity rehydration resolves definitions via
`DefinitionRegistry` which holds in-memory values). This means inline funcs and scoped
catalogs are in-memory concerns; they never need to survive a database round-trip.

## Decision

We introduce a three-tier action resolution precedence for every action reference at runtime:

1. **Node-local inline** (highest priority) — for the *main* action of a
   `ServiceTask`/`BusinessRuleTask` only; set via `WithAction(a)` or `WithActionFunc(fn)`.
2. **Definition-scoped catalog** — actions registered on the definition via
   `DefinitionBuilder.RegisterAction(name, a)` or `RegisterActionFunc(name, fn)`, stored in
   `ProcessDefinition.scoped` (`action.Catalog`); visible only to that definition.
3. **Global catalog** — the `action.Catalog` supplied to `NewRunner`; the fallback (may be
   nil if all actions are supplied through tiers 1–2).

For **secondary action references** (compensation, SLA-breach, reminder, cancel-handler,
definition `CancelActions`) there is no inline tier; they use tiers 2–3 only.

**Default-by-id:** the `Action` name field on `ServiceTask`/`BusinessRuleTask` is now
optional. When empty, the lookup key defaults to the node id at engine execution time.

**Mutually exclusive node options:** `WithActionName`, `WithAction`, and `WithActionFunc`
are mutually exclusive on a single node. `Build()` returns `ErrActionInlineAndNameConflict`
when a node carries both an inline action and a name.

**`DefinitionBuilder` API additions:**

```go
func (b *DefinitionBuilder) RegisterAction(name string, a action.ServiceAction) *DefinitionBuilder
func (b *DefinitionBuilder) RegisterActionFunc(name string, fn func(context.Context, map[string]any) (map[string]any, error)) *DefinitionBuilder
```

`Build()` returns `ErrDuplicateScopedAction` on a duplicate registration. When at least one
action is registered, `ProcessDefinition.ScopedCatalog()` returns a non-nil `action.Catalog`;
otherwise it returns nil.

**`model → action` dependency accepted.** The `model` package now imports the `action`
package. `action` is a pure leaf (no dependencies on other in-repo packages), so there is
no import cycle.

**Persistence (Option A — in-memory re-attach).** Inline actions and the scoped catalog are
never serialized. JSONB persists only the action name string (empty for inline/default-by-id
nodes). On restart the consumer rebuilds and re-registers the same definition objects in Go
code, restoring the funcs. No new persistence machinery is required.

**`action.Resolve` helper.** A package-level function centralizes tiers 2–3:

```go
func Resolve(scoped, global Catalog, name string) (ServiceAction, bool)
```

This is used by `runtime.Runner` internally and is independently testable.

**`engine.InvokeAction` gains `NodeID string`.** The engine stamps the node id on every
`InvokeAction` command so the runner can locate the node for inline-action lookup without
an additional query.

**`BusinessRuleTask` execution.** A dedicated `businessRuleTaskStrategy` is added alongside
`serviceTaskStrategy`. Both strategies share the same default-by-id name defaulting and
node-id stamping logic.

## Consequences

- **Breaking constructor signatures.** `NewServiceTask(id, action, opts...)` and
  `NewBusinessRuleTask(id, action, opts...)` are replaced by `New…Task(id, opts...)` with
  `WithActionName` carrying the name. All existing call sites (engine tests, runtime tests,
  examples) are updated. This is acceptable: the library has no known external consumers
  yet and is explicitly pre-consumer.
- **`model` is no longer stdlib-only.** The `model → action` import means `model` now
  has a non-stdlib dependency. The `action` package is a pure leaf, so this is the only new
  edge.
- **Nil global catalog is no longer a special error.** The previous early-return on
  `r.cat == nil` is removed. A definition may supply all its actions via its scoped catalog
  with a nil global catalog. A genuine miss across all tiers still fails via `ActionFailed`
  with an `unknown action: <name>` message.
- **Rehydrated-from-JSONB definitions have nil scoped/inline.** A `*ProcessDefinition`
  round-tripped through JSONB will have neither a scoped catalog nor inline actions until the
  consumer explicitly re-registers them in Go. This is by design (Option A); consumers must
  treat definition objects as code-owned, not purely data-driven.
- **`BusinessRuleTask` is now executed.** Processes that previously stalled on a
  `BusinessRuleTask` node will now proceed. This is intentional; any such stalled instances
  represent an engine gap that is now corrected.
- **Follow-up obligations:**
  - The scoped catalog is not surfaced in `InstanceSnapshot`/`ActionableView` DTOs or gRPC
    snapshots — out of scope for this change.
  - YAML/BPMN authoring of inline actions is impossible (funcs are not serializable); only
    action names can be specified in YAML/BPMN.
  - The `retry_test.go` `hasInvokeActionForNode` helper casts to `.(ServiceTask).Action`
    and would break if a retry test used default-by-id; noted as a minor follow-up (M-1).

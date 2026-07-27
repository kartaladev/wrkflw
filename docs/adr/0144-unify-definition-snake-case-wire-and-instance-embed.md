# 144. Unify the definition on one snake_case wire; embed it in the instance view

Status: Accepted — 2026-07-27. Spec:
[docs/specs/2026-07-27-processinstance-audit-view-and-idgen.md](../specs/2026-07-27-processinstance-audit-view-and-idgen.md).
First of the ADR-0144…0149 delivery. Supersedes the definition-JSON shape of
[ADR-0098](0098-service-coherent-graph-refactor.md); builds on the wire form in
[node_wire.go].

## Context

`service.ProcessInstance.MarshalJSON` (`service/instance.go`) emits a thin
projection — `def_id`/`def_version` summaries plus derived `action_bindings` /
`scoped_actions` — but **not the definition template itself**. Consumers embedding
the engine need the full definition in the marshalled instance so the document is
self-contained.

`model.ProcessDefinition` already has a canonical `MarshalJSON`/`UnmarshalJSON`
(`node_wire.go:148/169`) producing **camelCase** `{id, version, nodes, flows,
cancelActions}`, and **this same JSON is the persisted JSONB** in
`wrkflw_definitions.definition` (`internal/persistence/store/definitions.go:93/
129/165`) — the only (de)serialization vector for definitions. The instance
envelope, by contrast, is **snake_case** (`instance_id`, `node_id`). Embedding the
camelCase definition verbatim would mix casings; and `flow.SequenceFlow` has **no
json tags** at all, so flows marshal as PascalCase (`ID`/`Source`/`IsDefault`).

Go's `encoding/json` folds keys case-insensitively **only within one token**
(`strings.EqualFold`): an inserted underscore is not a fold. Verified — stored
`"IsDefault"` decodes into `json:"is_default"` as `false`; `"eligibleRoles"` into
`json:"eligible_roles"` as `[]`. So renaming the *persisted* wire to snake_case
would silently corrupt already-stored definitions. **But the library is
pre-release with no deployed data** — there is nothing to corrupt.

## Decision

Adopt **one snake_case canonical definition wire**, shared by persisted JSONB, the
instance-view embed, and YAML authoring. There is no separate view-only
projection.

1. Retag to snake_case, together, updating every fixture and golden test in the
   same change: `definitionWire`, `NodeWire`, `TriggerWire`, the
   `validate.ValidationDescriptor`, `flow.SequenceFlow` (which **gains** json tags
   `id/source/target/condition,omitempty/is_default,omitempty`), and the YAML
   authoring structs `nodeYAML`/`definitionYAML`/`sequenceFlowYAML`. `kind`
   *values* stay lowerCamelCase (`startEvent`) — they are enum values, not keys.
2. Add an additive `scoped_actions []string` field to the definition wire, written
   from `def.ScopedActionNames()` (the scoped catalog itself stays unserialized).
3. `service.ProcessInstance.MarshalJSON`: **remove** `def_id`, `def_version`,
   `action_bindings`; **embed** `definition` via the canonical `MarshalJSON`
   (omitted when the definition is nil). Keep `instance_id`, `status`, `variables`,
   `tokens`, `history`, `tasks`, `started_at`, `ended_at`.
   ⚠ **Superseded in part — see Implementation amendment 1 below: `def_id` and
   `def_version` are RETAINED. Only `action_bindings` was removed.**
4. Rename the tasks JSON key `task_token` → `task_id` (key only; the engine
   "task token" concept is unchanged).

This is a deliberate **breaking** change to the persisted definition format and
the YAML authoring keys, taken now, before release, in exchange for one
consistent shape and zero field-mapping duplication.

## Consequences

- **Positive:** storage, view embed, and YAML share one shape; no projection to
  keep in sync as definition fields evolve; the marshalled instance is
  self-contained; casing is uniform (snake_case keys everywhere).
- **Positive:** `flow.SequenceFlow` gets real json tags; empty `condition` /
  `is_default` are omitted.
- **Negative / breaking:** any definition JSONB or YAML written before this change
  is incompatible. There is **no migration or dual-read** — pre-release only. A
  golden round-trip test pins the snake_case shape; all wire/YAML fixtures are
  rewritten. Post-release, a change like this would require a migration.
- **Negative:** the transport `httpcore.NewInstanceView` DTO (`view.go`) keeps its
  own `def_id`/`def_version` and is unaffected; the `def_id`/`action_bindings`
  removal is a contract change only for consumers marshalling
  `service.ProcessInstance` directly. `service/instance_test.go:51` **does** assert
  `action_bindings` is absent for a nil def — that assertion is updated (the key is
  now always absent). `scoped_actions` is a **derived, marshal-only** field: written
  from `ScopedActionNames()` but dropped on unmarshal (a catalog cannot be rebuilt
  from names), so round-trip tests compare **marshal-side only**, never
  `JSON→struct→JSON`.
- Node-kind reconciliation on decode (`reconcileNodeValidationLenient`) is
  unaffected — only tag casing changes, not the decode structure.

## Implementation amendments (2026-07-27, Phase 1)

Recorded during implementation; each is a refinement of the decision above, not a
reversal.

- **`sequenceFlowYAML` deleted, not retagged.** Once `flow.SequenceFlow` carries
  both json AND yaml snake_case tags, the YAML mirror struct is pure duplication —
  the exact "projection to keep in sync" this ADR exists to remove. `definitionYAML.Flows`
  is now `[]flow.SequenceFlow` and `coreFromYAML` clones it directly.
- **Retag set (final):** `NodeWire`, `TriggerWire`, `definitionWire`,
  `flow.SequenceFlow`, `model.RetryPolicy`, `schedule.ClockTime`, and the
  `nodeYAML`/`definitionYAML` authoring structs. `validate.ValidationDescriptor`
  needed no change (`kind`/`schema` are already single tokens). Enum VALUES
  (`startEvent`, `everyExpr`, …) stay lowerCamelCase as decided.
- **`task_token` → `task_id` goes all the way down.** The JSON key rename is not
  cosmetic-only: the Go identifiers (`HumanTask.TaskID`, `NodeVisit.TaskID`,
  `HumanCompleted.TaskID`, `TaskByID`, `nextTaskID`, `cancelTimersByTaskID`, …)
  and the `wrkflw_human_task.task_id` COLUMN are renamed with it, so one name
  reads end to end. Pre-release, single consolidated migration (ADR-0132) ⇒ the
  column rename is an edit to `0001_init.sql`, not a new migration file.

## Implementation amendments (2026-07-28, code review)

### 1. `def_id` / `def_version` are retained

Decision point 3 above called for removing `def_id`, `def_version`, **and**
`action_bindings` on the grounds that all three are readable from the embedded
`definition`. Code review found that reasoning holds for `action_bindings` but
**not** for the definition identity, because the embed is `omitempty`:

```go
Definition *model.ProcessDefinition `json:"definition,omitempty"`
```

`service.NewProcessInstance(def, st)` is exported specifically "so consumers and
tests can fabricate one" and accepts a nil `def`; `resolveDefinition` can also
yield nil. In those cases the marshalled document carried **no definition identity
at all** — no `definition`, no `def_id`, no `def_version` — even though
`engine.InstanceState.DefID` / `.DefVersion` were populated the whole time. A
consumer that previously routed or grouped instances on `def_id` had nothing left
to read, and the information was available but deliberately dropped.

**Amendment:** `instanceJSON` carries `def_id` and `def_version` **unconditionally**
(no `omitempty`), sourced from the instance state rather than from a resolved
definition. The `definition` embed is unchanged and stays `omitempty`.

The two now play different roles, which is the point: the identity is cheap,
always available, and is the stable key a consumer routes on; the embed is the
best-effort full template that may legitimately be absent. Only `action_bindings`
and the top-level `scoped_actions` mirror were genuinely removed — both really are
derivable from the embed, and neither is a routing key.

`docs/specs/assets/process-instance-sample.json` gained the two keys accordingly,
and `transport/http/httpcore`'s independent `def_id`/`def_version` view fields
(noted under Consequences) are now consistent with the envelope rather than
diverging from it.

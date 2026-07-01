# 42. Node-as-interface process-definition model

- Status: Accepted
- Date: 2026-06-23

## Context

A design follow-up observed that `model.Node` was a flat ~35-field god-struct
where most fields are meaningful for only one `NodeKind` (e.g. `DefRef` only on
a call activity, `AttachedTo` only on a boundary event). This invites invalid
states (any field settable on any kind) and obscures each kind's real shape.

The spec (`docs/specs/2026-06-23-followups-resolution-design.md`) chose the
**full interface** redesign over a lower-cost "typed authoring layer that lowers
to a flat runtime node" — for this library the model *is* the product, so the
runtime model should be segregated, not just the authoring sugar. That decision
was made "eyes open" about an engine migration the spec estimated at ~108
field-read sites in one file.

During implementation the true blast radius proved larger: while production
node-field reads were indeed engine-only (concentrated in the `engine/step_*.go`
files after the ADR-0044 decomposition), **~995 node struct literals across ~50
test files repo-wide** had to be rewritten as constructor calls once the flat
struct was deleted. The owner re-confirmed full migration with this number known.

## Decision

`model.Node` becomes an **interface** (`Kind() NodeKind`, `ID() string`,
`Name() string`) with **one concrete value type per kind** (19 types:
`StartEvent`, `EndEvent`, `TerminateEndEvent`, `ErrorEndEvent`, `ServiceTask`,
`UserTask`, `ReceiveTask`, `SendTask`, `BusinessRuleTask`, `SubProcess`,
`CallActivity`, `EventSubProcess`, `IntermediateCatchEvent`,
`IntermediateThrowEvent`, `BoundaryEvent`, `ExclusiveGateway`,
`ParallelGateway`, `InclusiveGateway`, `EventBasedGateway`). Each embeds a
shared `baseNode` (id/name); activity kinds embed a shared `activityFields`
(retry, recovery, compensation, cancel, SLA, reminder). Each kind carries only
its valid fields.

- **Constructors + functional options.** One `New<Kind>` per kind, with an
  interface-based option system (`activityOption`, `catchOption`,
  `boundaryOption`, `startEventOption`, `throwOption`); `WithName` is a shared
  option satisfying several families. Kind-specific fields are settable only on
  their own kind, preserving the segregation.
- **Exported accessors** for fields the engine reads across multiple activity
  kinds without narrowing: `RetryPolicyOf`, `SLAOf`, `ReminderOf`, `ActionOf`.
- **`ProcessDefinition.Nodes` is `[]Node`**; `Node(id)`/`StartNodes()` return
  `Node`.
- **Backward-compatible JSONB.** Because `encoding/json` cannot decode into an
  interface, an unexported flat `nodeWire` (identical field set + JSON tags to
  the old struct) is the single serialization shape. `ProcessDefinition`'s
  custom `MarshalJSON`/`UnmarshalJSON` round-trip through it, with a `kind`
  discriminator reconstructing the concrete type. **Previously stored JSONB
  definitions decode unchanged** — verified by a backward-compat test and the
  Postgres store round-trip tests.
- **`DefinitionBuilder`** — a fluent `NewDefinition(id, version).Add(node).
  Connect(from, to, opts...).Build()` that validates on build.
- **YAML loader** — `ParseYAML`/`LoadYAML` decode through the same flat wire form
  and `kind` discriminator, then `Validate`. One validation path, three
  authoring front-ends (Go constructors/builder, YAML, existing JSON/XML).
  Adopts `gopkg.in/yaml.v3` (v3.0.1) — the de-facto standard, recorded here per
  the locked-tech-stack rule because YAML authoring is an explicit project
  goal.
- Per-kind validation replaces the old single field-switch in `model/validate.go`
  with type-asserts; every existing sentinel error and message is preserved.

## Consequences

- Each kind's state is segregated; invalid cross-kind field assignments are no
  longer expressible. The authoring API is ergonomic and discoverable.
- **Ongoing tax:** every future node kind needs a concrete type, a `fromWire`/
  `toWire` arm, a constructor, a validation method, and (implicitly) a YAML/JSON
  mapping. The completeness of these is guarded by tests.
- Stored definitions remain readable; the wire contract is unchanged, so the
  Postgres `DefinitionStore` and all transports keep working without migration.
- The engine's node access is confined to `engine/step_*.go`; the migration was
  behavior-preserving (the full pre-existing test suite passes with **no
  assertion changes** anywhere).
- **Field-map corrections discovered during migration:**
  - `EventSubProcess.NonInterrupting` was initially dropped from the new type but
    is load-bearing (the ESP arm branches interrupting vs non-interrupting). It
    was restored to the concrete type, its constructor option, and the wire form.
  - `StartEvent` carries `SignalName`/`MessageName`/`CorrelationKey`/
    `TimerDuration` so event-sub-process trigger encoding round-trips.
  - Activities no longer carry `ErrorCode`. The old flat struct allowed it on any
    node and the engine read `node.ErrorCode` in the retry-exhaustion catch-flow
    to set `s.Variables["_error"]`; in practice no activity ever set `ErrorCode`
    (it is meaningful only on `ErrorEndEvent`/`BoundaryEvent`), so that branch was
    dead. Dropping it is behavior-identical for all real definitions and is the
    intended consequence of per-kind segregation; `_errorMessage`/`_errorAttempts`
    are still set.
- The repo-wide literal migration (~995 sites) was a one-time cost; new tests use
  the constructors directly.

## Sequencing note

This ADR is numbered 0042 but executed after ADR-0044 (the `engine/step.go`
strategy-registry decomposition), which was deliberately sequenced first so this
migration edited small per-kind strategy files instead of the 3251-line
monolith. ADR numbers are chronological IDs, not an execution order.

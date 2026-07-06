# 0101. Definition Qualifier: typed DefRef

Status: **Accepted — 2026-07-07.**
Follows: [ADR-0100](0100-server-generated-instance-id.md) (server-generated instance ID).
Mirrors the injectable-seam pattern from [ADR-0003](0003-clockwork-as-time-source.md).

## Context

Before this ADR every component that needed to reference a process definition carried a raw
`string` in the `"id"` (latest) or `"id:version"` (pinned) form. The convention was
implicit: about nine production sites scattered `fmt.Sprintf`, `strings.Cut`, and
`strconv.Atoi` calls across `definition/`, `runtime/kernel/`, `runtime/chain/`,
`engine/`, `service/`, `internal/persistence/store/`, and the HTTP transport.

Specific pains:

1. **Registries double-indexed.** `MapDefinitionRegistry` and `MemDefinitionRegistry` each
   maintained two `map[string]*ProcessDefinition` entries — one for the pinned key
   (`"id:version"`) and one for the latest key (`"id"`). The contract for which key won the
   latest slot was repeated, not centralised.
2. **Parse scattered.** `DefinitionRegistry.Lookup(ctx, string)` required every call site
   that held a typed pair `(id, version)` to re-join with `fmt.Sprintf` before calling, and
   every implementation to re-split with `strings.Cut`/`strconv.Atoi`.
3. **No stable map key.** Using a `string` as a map key worked but required callers to
   normalise the form; using a struct key would be safer and allow the Go compiler to enforce
   completeness.
4. **Latest-vs-pinned implicit.** There was no single canonical predicate; each site
   independently checked `version == 0` or `!strings.Contains(ref, ":")`.

## Decision

### D1 — New type `definition/model.Qualifier{ID string; Version int}` with helpers

`model.Qualifier` is a small value struct. `Version == 0` is the reserved sentinel meaning
"latest registered version". Constructor helpers and methods:

```go
func Latest(id string) Qualifier            // {id, 0}
func Version(id string, v int) Qualifier    // {id, v}
func (q Qualifier) IsLatest() bool          // Version == 0
func (q Qualifier) String() string          // "id" or "id:version"
func ParseQualifier(s string) (Qualifier, error)
var ErrInvalidQualifier = errors.New("workflow-model: invalid qualifier")
```

`ParseQualifier` rejects: empty id; non-numeric, negative, or zero explicit version (`:0`
is explicitly rejected — zero is the reserved latest sentinel; express latest as bare `"id"`).
JSON and YAML marshalers emit and parse the `String()` form, keeping the wire byte-identical
to the previous string encoding.

`definition.Qualifier`, `definition.Latest`, `definition.Version`, and
`definition.ParseQualifier` are re-exported from the `definition` root package as type
aliases / forwarding functions so consumers importing `definition` see a uniform surface.

`(*model.ProcessDefinition).Qualifier()` returns the pinned `{ID, Version}` for the
definition itself, used to index the definition under its exact version key.

### D2 — String wire form via JSON/YAML marshalers (no schema change)

`Qualifier` implements `json.Marshaler` / `json.Unmarshaler` and the YAML equivalent,
emitting and parsing the `String()` representation. All HTTP DTO fields typed `model.Qualifier`
(`StartInput.DefRef`, `DeliverMessageInput.DefRef`) serialise identically to their former
`string` counterparts. The HTTP `def_ref` JSON key is unchanged.

HTTP DTOs add an explicit `DefRef.ID == ""` guard returning HTTP 400 because
`validate:"required"` cannot detect a zero-value struct.

The node-wire (`node_wire.go`) and YAML loader (`yaml.go`) structs keep a bare `string`
field; `CallActivity.ToWire()` calls `DefRef.String()` and `FromWire()` calls the
best-effort `parseOrZero` wrapper around `ParseQualifier` (the zero `Qualifier` on a
bad/empty ref is caught by definition validation).

### D3 — TEXT column storage via `q.String()` (no migration)

The `definition_ref` TEXT columns in `wrkflw_chainlinks` and the `definition_ref` metadata
field in `wrkflw_outbox` continue to store the joined `"id:version"` or `"id"` string.
Write paths call `q.String()`; read paths call `parseDefRef(s)` — a thin wrapper around
`ParseQualifier` that maps an empty string to the zero `Qualifier` (preserving compatibility
with pre-ADR-0047 rows where the column is empty) and wraps any other parse error as a
`"workflow-store:"` error.

No schema migration is required.

### D4 — `DefinitionRegistry.Lookup(ctx, model.Qualifier)` on both interface declarations and all four implementations

The `DefinitionRegistry` interface in `runtime/kernel` and the matching interface in
`internal/persistence/store` both change their `Lookup` signature from `(ctx, string)` to
`(ctx, model.Qualifier)`.

All four implementations are updated:

| Implementation | Package | Latest behaviour |
|---|---|---|
| `MapDefinitionRegistry` | `runtime/kernel` | map keyed `map[model.Qualifier]*ProcessDefinition`; latest slot = highest-version-seen |
| `MemDefinitionRegistry` | `runtime/kernel` | same map type; latest slot = last-registered |
| `CachingDefinitionRegistry` | `runtime/kernel` | keys `q.String()` for the internal TTL cache and singleflight group |
| `DefinitionStore` | `internal/persistence/store` | `q.IsLatest()` branches to `ORDER BY version DESC LIMIT 1`; otherwise exact `(id, version)` lookup |

`NewMapDefinitionRegistry` becomes variadic (`...*ProcessDefinition`) — callers that passed
a single definition previously must add `...` or wrap in a slice at each call site.

### D5 — Typed fields, params, and producers; names kept; boundary-only conversion

All def-ref carrying fields and constructor parameters are retyped to `model.Qualifier`;
field names are preserved to minimise blast radius:

| Symbol | Package |
|---|---|
| `service.StartInstanceRequest.DefRef` | `service` |
| `service.DeliverMessageRequest.DefRef` | `service` |
| `engine.StartSubInstance.DefRef` | `engine` |
| `activity.CallActivity.DefRef` | `definition/activity` |
| `kernel.OutboxEvent.DefinitionRef` | `runtime/kernel` |
| `kernel.ChainLink.{Predecessor,Successor}DefinitionRef` | `runtime/kernel` |
| `kernel.ChainLinkRef.DefinitionRef` | `runtime/kernel` |
| `chain.ChainEvent.PredecessorDefinitionRef` | `runtime/chain` |

Constructors `activity.NewCallActivity(id, model.Qualifier, …)` and
`build.(*Builder).AddCallActivity(id, model.Qualifier, …)` take the typed value directly.

`fmt.Sprintf("id:version")`/`strings.Cut`/`strconv.Atoi` producers are eliminated from all
internal sites. The only remaining string conversion points are the wire/storage boundaries
(D2, D3) and the `CachingDefinitionRegistry` singleflight key (D4).

## Consequences

**Positive**

- **Typed end-to-end.** The compiler enforces that every def-ref site provides a valid
  `Qualifier`; passing a raw string is no longer possible inside the module.
- **Single latest-vs-pinned locus.** `q.IsLatest()` and `q.String()` are the only places
  that encode the latest-vs-pinned convention. All registries and the store branch on
  `IsLatest()`.
- **No scattered fmt.Sprintf/Cut/Atoi.** The nine former parse/join sites are eliminated;
  only boundary adapters (HTTP DTOs, YAML loader, node-wire, store scan) perform string
  conversion.
- **Wire and schema unchanged.** `def_ref` HTTP keys and TEXT columns store the same
  `"id"` / `"id:version"` strings; no consumer migration and no database migration are
  required.
- **Stable map key.** Registries now use `map[model.Qualifier]*ProcessDefinition`, a
  comparable struct key, instead of two separate `map[string]` maps; the latest-slot
  keying (`model.Latest(id)`) is explicit and uniform.
- **Consistent with prior seam pattern.** The approach mirrors ADR-0047's typed
  `ChainLink.{Predecessor,Successor}DefinitionRef` fields, which already used string-typed
  refs before this ADR; those are now `model.Qualifier` as well.

**Negative / risks**

- **Breaking Go API.** `DefinitionRegistry.Lookup` signature changes; all `DefRef`/
  `DefinitionRef` fields and the constructor/parameter types change. Pre-v0.1.0 — no
  stability promise applies; all call sites inside the module are updated, and the
  wire+DB formats are unchanged so consumers using only the HTTP transport are unaffected.
- **Version-0-as-latest sentinel.** `ParseQualifier` explicitly rejects `":0"` — callers
  cannot express "latest" via an explicit zero version; they must use the bare `"id"` form.
  This is intentional: `Version == 0` is reserved so `Qualifier` can be used as a map key
  without a separate boolean.
- **~41 test files swept.** The typed change propagated broadly. All test literals were
  updated in a single sweep commit; no behavioural changes were introduced.

# Definition Qualifier: replace string `DefRef` with a typed `Qualifier{ID, Version}`

**Status:** Draft — 2026-07-07. Target ADR: **0101**.

## Context

A process-definition is referenced throughout the engine by a **string** `DefRef`
whose format is `"id"` (resolve the latest version) or `"id:version"` (pinned).
That string is parsed and re-joined in many places: registries double-index a map
under both the bare id and the `"id:version"` key, the SQL store parses it with
`strings.Cut`/`strconv.Atoi`, and nine `fmt.Sprintf("%s:%d", …)` sites re-build it
from a definition's `ID`+`Version`. The convention (`0`/no-colon = latest, `>=1` =
pinned) is implicit and re-implemented per call site, which is error-prone and
untyped.

We replace the string with a typed value, **`Qualifier{ID string; Version int}`**
(`Version == 0` means "latest"), used **everywhere** a definition is referenced —
not merely at the API boundary. The change is **behavior-preserving**: latest/pinned
resolution semantics are identical, the wire stays byte-identical, and no schema
migration is required.

### Verified facts (recon 2026-07-07, current tree @ `94d2808`)

- Real definition versions are always `>= 1` (64 test constructions use version 1;
  the only `DefVersion: 0` is an empty/unset `CallLinkRef` fixture). So `Version == 0`
  is a **safe** "latest" sentinel — no real definition collides with it.
- `definition` package does **not** already export `Latest`, `Version`, `Qualifier`,
  or `ParseQualifier` — no name collision.
- `kernel.CallLink` (`runtime/kernel/calllink.go:18-19`) and `kernel.CallLinkRef`
  (`runtime/kernel/opsstats.go:56-57`) **already store split** `DefID`/`DefVersion`
  columns — they become `Qualifier` trivially, no migration.

## Decision

### D0 — The `Qualifier` type

Lives in `definition/model` (low in the import graph — kernel/engine/activity/store/
transport all depend on it), re-exported as `definition.Qualifier` alongside the
existing `definition.NewBuilder` aggregator.

```go
// Qualifier references a process definition: a specific version when Version >= 1,
// or the latest registered version when Version == 0.
type Qualifier struct {
    ID      string
    Version int // 0 == latest; >= 1 == pinned
}

func Latest(id string) Qualifier         { return Qualifier{ID: id} }
func Version(id string, v int) Qualifier { return Qualifier{ID: id, Version: v} }

func (q Qualifier) IsLatest() bool { return q.Version == 0 }
func (q Qualifier) String() string // "order" if latest, else "order:3"

func ParseQualifier(s string) (Qualifier, error) // inverse of String()

func (q Qualifier) MarshalJSON() ([]byte, error)   // emits String()
func (q *Qualifier) UnmarshalJSON(b []byte) error  // via ParseQualifier
func (q Qualifier) MarshalYAML() (any, error)      // emits String()
func (q *Qualifier) UnmarshalYAML(unmarshal func(any) error) error // via ParseQualifier
```

Re-exports in the `definition` aggregator: `type Qualifier = model.Qualifier`,
`var Latest = model.Latest`, `var Version = model.Version`,
`var ParseQualifier = model.ParseQualifier` (or thin forwarding funcs, matching the
existing `NewBuilder` forwarding style).

`ParseQualifier` rules (inverse of `String`, total on all inputs):

| Input        | Result            |
|--------------|-------------------|
| `"order"`    | `{order, 0}`      |
| `"order:3"`  | `{order, 3}`      |
| `""`         | error             |
| `":3"`       | error (empty id)  |
| `"order:"`   | error             |
| `"order:x"`  | error (non-numeric) |
| `"order:-1"` | error (negative)  |
| `"order:0"`  | error (0 is reserved for the latest sentinel; callers express latest as `"order"`) |

Error sentinel prefixed `workflow-model:` (project convention).

Also add a helper on the existing definition type:

```go
// Qualifier returns a pinned Qualifier for this definition's exact ID+Version.
func (d *ProcessDefinition) Qualifier() Qualifier { return Qualifier{d.ID, d.Version} }
```

This replaces the `fmt.Sprintf("%s:%d", def.ID, def.Version)` producers.

### D1 — Wire/YAML form: string via Marshal/Unmarshal (chosen)

The HTTP JSON tag `def_ref`, the CallActivity node-wire tag `defRef`, and the YAML
tag `defRef` all keep their **string** form on the wire. `Qualifier`'s
`MarshalJSON`/`UnmarshalJSON` and `MarshalYAML`/`UnmarshalYAML` (de)serialize to the
same `"id"`/`"id:version"` string. The wire is **byte-identical to today** — no HTTP
request body, YAML fixture, or wire-round-trip test needs to change. Only the
internal representation becomes typed; the string↔struct boundary is the new code.

Rejected: nested object `{id, version}` (breaks every wire body + fixture);
hybrid accept-both (most code, fuzziest contract).

### D2 — Storage: keep TEXT columns via `Qualifier.String()` (chosen)

The persisted `definition_ref` TEXT columns (`wrkflw_outbox.definition_ref`,
`wrkflw_chain_link.{predecessor,successor}_definition_ref`) and the watermill
`definition_ref` metadata keep the joined `"id:version"` string. The store boundary
writes `q.String()` and reads `ParseQualifier()`. **No schema migration**; existing
rows stay valid; the outbox/event row shape is unchanged.

Rejected: split `def_id`/`def_version` columns (requires a 3-dialect migration +
backfill for marginal gain; `CallLink` is already split and unaffected either way).

### D3 — Registry `Lookup` becomes typed

Both `Lookup(ctx, defRef string)` interface declarations change to
`Lookup(ctx, q Qualifier)`:

- `runtime/kernel/definition_registry.go:22`
- the `persistence` port at `persistence/persistence.go:61`

(These are two independent declarations today; both change together. Compile-time
`var _ DefinitionRegistry = …` asserts at `internal/persistence/store/definitions.go:45`,
`runtime/kernel/mem_definition_registry.go:113`, and
`runtime/kernel/caching_definition_registry.go:16` follow.)

All four implementations:

- **`MapDefinitionRegistry`** (`runtime/kernel/definition_registry.go:52`) and
  **`MemDefinitionRegistry`** (`runtime/kernel/mem_definition_registry.go:101`):
  drop the double-map indexing (bare-id key for latest + `"id:version"` key). Key the
  map by `Qualifier` directly (it is comparable). `Register` stores under the pinned
  `def.Qualifier()`; `Lookup` on an `IsLatest()` qualifier resolves to the
  newest-registered version for that ID (mirrors today's "bare-id key overwritten to
  latest-registered" semantics — implementation switches to tracking the max/last
  version per ID instead of a bare-id map entry).
- **`CachingDefinitionRegistry`** (`runtime/kernel/caching_definition_registry.go:85`):
  cache and singleflight keys become `Qualifier` (comparable — safe as a map key).
  Delegates to the backing registry's typed `Lookup`.
- **`DefinitionStore`** (SQL, `internal/persistence/store/definitions.go:144`):
  replace `strings.Cut`/`Atoi` with a branch on `q.IsLatest()` → `ORDER BY version
  DESC LIMIT 1`, else `WHERE def_id=? AND version=?` (the existing
  `GetDefinition(ctx, id, version)` path).

Callers of `.Lookup` that pass a joined string (`runtime/calllink/notifier.go:190`,
`runtime/processdriver_action.go:273`, `runtime/processdriver_cancel.go:64`,
`runtime/timerops.go:98`, `service/service.go:272,296,326,423`) pass a `Qualifier`
instead — sourced from a split field (`def.Qualifier()`,
`Qualifier{ParentDefID, ParentDefVersion}`, `Version(defID, defVersion)`), removing
the `fmt.Sprintf` join.

### D4 — Field / parameter / producer swaps

All string def-refs become `Qualifier`. **Field names are kept unchanged**
(type-only change) to minimize churn:

Request / DTO / node fields (string → `Qualifier`):

| Field | Location |
|---|---|
| `service.StartInstanceRequest.DefRef` | `service/request.go:14` |
| `service.DeliverMessageRequest.DefRef` | `service/request.go:37` |
| `engine.StartSubInstance.DefRef` | `engine/command.go:156` |
| `activity.CallActivity.DefRef` | `definition/activity/activity.go:96` |

Persisted domain fields (string → `Qualifier`; **names kept**, store boundary converts
via `String()`/`ParseQualifier()`):

| Field | Location |
|---|---|
| `kernel.OutboxEvent.DefinitionRef` | `runtime/kernel/ports.go:43` |
| `kernel.ChainLink.PredecessorDefinitionRef` | `runtime/kernel/chainlink.go:34` |
| `kernel.ChainLink.SuccessorDefinitionRef` | `runtime/kernel/chainlink.go:37` |
| `kernel.ChainLinkRef.DefinitionRef` | `runtime/kernel/opsstats.go:66` |
| `chain.ChainEvent.PredecessorDefinitionRef` | `runtime/chain/chainer.go:33` |

Constructors / builder (string param → `Qualifier`):

| Signature | Location |
|---|---|
| `activity.NewCallActivity(id string, ref Qualifier, …)` | `definition/activity/activity.go:160` |
| `build.(*Builder).AddCallActivity(id string, ref Qualifier, …)` | `definition/build/build.go:116` |

Nine `fmt.Sprintf("%s:%d", …)` producers → typed equivalents
(`def.Qualifier()` / `Version(id, v)` / `Qualifier{ParentDefID, ParentDefVersion}`):
`runtime/kernel/mem_definition_registry.go:71` (removed — map keyed by Qualifier),
`runtime/outbox.go:14`, `runtime/chain/chainer.go:228`,
`runtime/calllink/notifier.go:189`, `runtime/processdriver_cancel.go:63`,
`runtime/timerops.go:97`, `service/service.go:296`, `service/service.go:422`,
`internal/transporttest/harness.go:86` (test helper).

### D5 — Boundaries convert; nothing else does

- **Wire (HTTP `def_ref`, node `defRef`, YAML `defRef`)** — tags unchanged; the
  string form is produced by `Qualifier`'s Marshal/Unmarshal. `endpoints.go:29,98`
  map the (now-typed) DTO field straight through. `validate.go` `def_ref`-required
  checks are unchanged (a zero `Qualifier` marshals to `""`, which still fails the
  required check on an empty id — verify during implementation).
- **SQL store** — the write sites for `OutboxEvent.DefinitionRef` / `ChainLink.*`
  (`runtime/outbox.go`, `runtime/chain/chainer.go`, `internal/persistence/store/*`)
  call `q.String()`; read/hydration sites call `ParseQualifier()`. No migration.
- **Watermill `definition_ref` metadata** (`internal/eventing/watermill/publisher.go:106`,
  `eventing/chaining.go:61`) stays a string via `String()`.
- **Admin lineage projection** (`transport/http/httpcore/admin_endpoints.go:372,414,422`)
  exposes `definition_ref` as a string — sourced via `String()`.

## Consequences

**Positive**
- Def-refs are typed end-to-end; the latest-vs-pinned convention lives in one place
  (`Qualifier.IsLatest()`), not re-implemented per call site.
- No more `strings.Cut`/`Atoi`/`fmt.Sprintf("%s:%d")` scattered across nine sites —
  replaced by `def.Qualifier()` / constructors.
- `Qualifier` matches the already-split on-disk `CallLink` shape, so the
  `notifier.go` join disappears.
- Wire byte-identical (D1) and no schema migration (D2) → low blast-radius on
  consumers and operators.

**Negative / risks**
- Breaking Go API: field/param/interface types change (`Lookup` signature, four
  request/node fields, five persisted fields, two constructors). Acceptable
  pre-v0.1.0. Old string call sites won't compile — a compile-driven sweep catches
  every one.
- `Version:0`-as-latest is a sentinel convention: a definition genuinely versioned 0
  would be unreachable-by-pin. Mitigated — versions are `>= 1` by convention and
  nothing uses 0; `ParseQualifier` rejects `"id:0"` so the sentinel can't be
  smuggled in via the wire.
- ~41 test files reference `def_ref`/`DefRef`; construction-site string literals
  (`"order:1"`) become `definition.Version("order", 1)`, while wire-body strings stay
  as-is. Mechanical but broad.

## Testing

- **Unit `Qualifier`** (`definition/model`): constructors; `IsLatest`; `String`;
  `ParseQualifier` round-trip and every error case in the D0 table; JSON and YAML
  marshal/unmarshal round-trips (incl. latest form `"order"` and the empty/zero
  qualifier). Table-driven per project `table-test` skill.
- **Registry `Lookup`** per impl (`Map`/`Mem`/`Caching`/`DefinitionStore`): latest
  resolves to newest registered; pinned resolves exactly; unknown → not-found. The
  SQL `DefinitionStore` test runs against the 3 dialects via `RunTestDatabase`.
- **Behavior preservation**: existing registry/chain/outbox/service/transport suites
  must pass unchanged in intent — the wire round-trips and the SQL round-trips assert
  the string form is preserved across the typed boundary.
- Coverage ≥ 85% on every touched package; `golangci-lint` clean; `go test -race ./...`
  green.

## Execution

Own branch `feat/definition-qualifier` (off `main @ 94d2808`), executed via
subagent-driven-development. Task decomposition roughly follows the sections, in
dependency order:

1. `Qualifier` type + constructors + `String`/`ParseQualifier` + JSON/YAML marshal +
   `ProcessDefinition.Qualifier()` — with full unit tests (TDD). Foundation; nothing
   else compiles against it yet.
2. `DefinitionRegistry.Lookup(ctx, Qualifier)` — both interface decls + all four impls
   + their tests (drop double-indexing / string-parse).
3. Domain field + constructor swaps (request/DTO/node/persisted fields, `NewCallActivity`,
   `AddCallActivity`) + the `fmt.Sprintf` producer replacements — compile-driven sweep
   of call sites.
4. Boundary conversions (wire Marshal already in task 1; store `String()`/`ParseQualifier`
   write/read sites; watermill metadata; admin projection) + the ~41-file test sweep.
5. ADR-0101 (Nygard) + CHANGELOG (Breaking: `Lookup` signature + def-ref field/param
   types now `Qualifier`; Added: `definition.Qualifier`/`Latest`/`Version`/`ParseQualifier`).
6. Final verification (race/coverage/lint) + whole-branch review.

## Self-Review

- **Placeholders:** none. All file:line references are from the 2026-07-07 recon;
  implementation re-greps exact lines before editing (idgen renumbered some).
- **Consistency:** `Version:0 == latest` is applied uniformly (type doc, `IsLatest`,
  `ParseQualifier` rejecting `":0"`, registry branching, the version-floor rationale).
  D1 (wire string) and D2 (TEXT storage) are mutually reinforcing — both rely on
  `String()`/`ParseQualifier` as the single conversion seam.
- **Scope:** one coherent behavior-preserving refactor; no unrelated cleanup (the two
  duplicate `Lookup` interface declarations are kept, only re-typed).
- **Ambiguity:** the field-rename question is resolved (keep `…DefinitionRef` names,
  type-only change). `ParseQualifier` totality and error cases are enumerated.

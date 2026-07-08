# 0111. JSON Schema adapter libraries: santhosh-tekuri/jsonschema/v6 + invopop/jsonschema

- Status: Accepted
- Date: 2026-07-08

## Context

ADR-0110 establishes the validation architecture: a neutral `validation` port
(`Validator`, `ValidationStrategy`, `DescribableStrategy`, `Registry`, `Gate`) with concrete
schema/predicate libraries confined to opt-in adapter subpackages, so the `definition`/engine
core never imports a third-party validation library directly.

One of those adapters must validate a process instance's external input map against a **JSON
Schema**. The consumer needs two authoring paths for that schema:

1. **Declarative** — JSON Schema text (or an equivalent Go `map[string]any`) written directly,
   serialized verbatim on the definition node so it round-trips through the wire/YAML form
   (`DescribableStrategy.Descriptor()`).
2. **Programmatic** — deriving the schema from an existing Go struct (the same struct a
   consumer might use to decode a `StartInstance` payload), via `encoding/json` struct tags
   plus a `jsonschema:"..."` tag for constraints, avoiding hand-duplicating the schema.

Both paths need a validator that can compile a JSON Schema document (draft 2020-12, the
current JSON Schema Core version) and check an arbitrary `map[string]any` instance against it,
returning descriptive per-property errors. The reflection path needs a struct→schema generator
that supports common constraint tags (`minimum`, `required`, etc.) and can produce a
self-contained (non-`$ref`-fragmented) document, because the input to `Validate` is a flat
`map[string]any`, not a struct instance that could resolve schema references through Go's type
graph.

## Decision

We adopt two libraries, both confined to `validation/jsonschema` (this package) — the
`definition`/engine core imports neither:

- **`github.com/santhosh-tekuri/jsonschema/v6`** as the validator. It compiles JSON Schema
  documents (2020-12 draft, with fallback support for older drafts) from an in-memory resource
  and validates arbitrary `any` values (including `map[string]any`) against the compiled
  schema, returning a structured `*ValidationError` with per-property location detail. The
  package-level surface used is:
  `jsonschema.UnmarshalJSON(io.Reader) (any, error)` to parse schema text into the value graph
  the compiler expects, `jsonschema.NewCompiler()` / `(*Compiler).AddResource(url string, doc
  any) error` / `(*Compiler).Compile(url string) (*Schema, error)` to compile it, and
  `(*Schema).Validate(v any) error` to check an instance. `Validate` accepts a plain
  `map[string]any` with native Go numeric types (`float64`) directly — no
  `json.Number`-normalization round-trip is required for the *instance*, only the *schema
  document* goes through `UnmarshalJSON`.
- **`github.com/invopop/jsonschema`** for struct-reflection schema generation
  (`NewFromStruct`). `(&jsonschema.Reflector{DoNotReference: true}).Reflect(v any) *Schema`
  walks a Go type via `reflect`, honoring `json` tags for property names and `jsonschema:"..."`
  tags for constraints (e.g. `minimum=0`). `DoNotReference: true` is load-bearing: without it,
  the reflector emits `$ref`/`$defs` schema fragments, which is fine for struct-typed instances
  resolved through the same reflector but is unnecessary complexity here since the adapter's
  instance is always a flat `map[string]any` — inlining keeps the derived schema
  self-contained and identical in shape to a hand-written one.

Both dependencies are pulled with `go get ... @latest` and pinned by `go.sum`; no version pin
is asserted beyond what `go.mod` records (unlike the hard-pinned entries in the Tech Stack
table — these adapters are an isolated, swappable seam per ADR-0110, not a project-wide
committed dependency).

The serialized `ValidationDescriptor.Schema` is always canonical JSON text — whether it came
from `New(text)` (verbatim), `NewFromValue(map)` (marshaled), or `NewFromStruct(v)` (reflected
then marshaled) — so `Factory(schema)` can rebuild an equivalent strategy regardless of which
constructor produced it originally.

## Consequences

- Two new third-party dependencies enter `go.mod`, but both are isolated behind
  `validation/jsonschema`; swapping either (e.g. a different JSON Schema draft implementation)
  touches only this package, not `definition`/engine core or other adapters
  (`validation/expr`, `validation/callback`).
- JSON Schema draft 2020-12 semantics are supported (santhosh-tekuri/jsonschema/v6's default
  draft), giving access to modern keywords (`prefixItems`, `unevaluatedProperties`, etc.) if a
  consumer's schema needs them, at the cost of not supporting the oldest Draft-4-only
  ecosystems without an explicit `DefaultDraft` override.
- Struct-derived schemas (`NewFromStruct`) let a consumer avoid hand-duplicating validation
  rules already expressed in a decode-target Go struct's `jsonschema:"..."` tags. invopop's
  reflector panics internally on unsupported field types (e.g. channels, functions);
  `NewFromStruct` recovers that panic via a deferred `recover()` and converts it into a
  wrapped `workflow-validation/jsonschema: ...` error, so the panic never escapes to the
  caller and the function's `(validation.DescribableStrategy, error)` contract holds even for
  callers passing arbitrary `any` rather than a well-formed DTO struct.
- Every strategy's `Descriptor()` returns canonical JSON, so a compiled definition round-trips
  through storage/wire (YAML or DB) without re-deriving from the original Go struct; the
  reflection step only ever runs once, at authoring time.

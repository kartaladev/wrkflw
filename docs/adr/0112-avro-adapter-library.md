# 0112. Avro adapter library: linkedin/goavro/v2

- Status: Accepted
- Date: 2026-07-08

## Context

ADR-0110 establishes the validation architecture: a neutral `validation` port (`Validator`,
`ValidationStrategy`, `DescribableStrategy`, `Registry`, `Gate`) with concrete schema/predicate
libraries confined to opt-in adapter subpackages, so the `definition`/engine core never imports
a third-party validation library directly. ADR-0111 landed the sibling
`definition/model/validate/jsonschema` adapter following that pattern.

A second declarative adapter must validate a process instance's external input map — a flat
`map[string]any` — against an **Avro record schema** (`.avsc` text), for consumers who already
express their event/message contracts in Avro (e.g. schema-registry-backed pipelines) and want
the same shape enforced at `StartInstance`/task-completion boundaries. The adapter needs to:

1. Parse `.avsc` record schema text into a reusable, compiled form.
2. Check an arbitrary `map[string]any` instance against that schema, catching both missing
   required fields and fields of the wrong native Go type, without hand-rolling Avro's type
   and field-presence rules.
3. Serialize back to the same `.avsc` text so `DescribableStrategy.Descriptor()` round-trips
   through the wire/YAML definition form, matching the `definition/model/validate/jsonschema` and
   `definition/model/validate/expr` adapters' convention.

## Decision

We adopt **`github.com/linkedin/goavro/v2`**, confined entirely to
`definition/model/validate/avro` (this package) — the `definition`/engine core imports neither
goavro nor any other Avro library.

The package-level surface used is:

- `goavro.NewCodec(schema string) (*Codec, error)` — parses `.avsc` record schema text into a
  compiled `*Codec`. A parse error (malformed JSON, or JSON that isn't a well-formed Avro
  schema) is wrapped `workflow-validation/avro: parse schema: %w` and returned from
  `NewValidator()`, matching the sibling adapters' failure-wrapping convention.
- `(*Codec).BinaryFromNative(buf []byte, native any) ([]byte, error)` — encodes a native Go
  value (here, `map[string]any`) against the schema. We discard the returned bytes (`buf` is
  always `nil`) and use **only the error** as the conformance signal: goavro's binary encoder
  is strict about native representation — an Avro `record` requires every non-defaulted field
  to be present in the map, and each field's native Go type must match what the schema's
  primitive type expects (e.g. `double` requires a Go numeric, `string` requires a Go `string`).
  A missing field or a type mismatch surfaces as a non-nil `error`, which is exactly the
  conformance check this adapter needs — no separate schema-validation API call is required.

Confirmed empirically against the pulled version (`v2.15.0`) before committing to this approach:
a conforming map (`{"amount": 10.0, "decision": "approve"}` against `record{double amount,
string decision}`) encodes with a nil error; a map missing `decision` errors
(`... field "decision": schema does not specify default value and no value provided`); and a
map with `amount` as a string errors (`... cannot encode binary double: expected: Go numeric;
received: string`). All three hold as written, so the brief's original design needed no
adaptation.

The Avro-conformance error itself is also wrapped `workflow-validation/avro: does not conform to
avro schema: %w`, consistent with `definition/model/validate/jsonschema`'s `Validate` wrapping
its schema compiler's error the same way.

The dependency is pulled with `go get ... @latest` and pinned by `go.sum`; no version pin is
asserted beyond what `go.mod` records (unlike the hard-pinned entries in the Tech Stack table —
this adapter is an isolated, swappable seam per ADR-0110, not a project-wide committed
dependency).

## Consequences

- One new third-party dependency (`github.com/linkedin/goavro/v2`, pulling in
  `github.com/linkedin/goavro` and `github.com/golang/snappy` transitively) enters `go.mod`,
  but it is isolated behind `definition/model/validate/avro`; swapping it for a different Avro
  library touches only this package, not `definition`/engine core or the other adapters
  (`definition/model/validate/expr`, `definition/model/validate/jsonschema`,
  `definition/model/validate/callback`).
- The conformance check is **encode-based**: it validates by attempting to serialize the input
  to Avro binary rather than calling a dedicated "validate against schema" API (goavro exposes
  none for a raw `map[string]any`). This means the accepted error surface is whatever
  `BinaryFromNative` reports — sufficient for missing-field and wrong-type detection (this
  adapter's stated scope), but not a substitute for a schema-registry-grade Avro validator if a
  consumer later needs richer diagnostics (e.g. structured per-field error paths, as
  `definition/model/validate/jsonschema` gets from its compiler).
- Only Avro `record` schemas are supported by this adapter's intended usage (a top-level
  `map[string]any` instance); non-record top-level Avro types (e.g. a bare `union` or
  primitive) are out of scope and were not exercised.
- The serialized `ValidationDescriptor.Schema` is always the raw `.avsc` text passed to `New`,
  so `Factory(schema)` rebuilds an equivalent strategy byte-for-byte regardless of formatting,
  matching the round-trip guarantee the other declarative adapters provide.

> **Note (2026-07-09).** This package relocated from `validation/avro` to
> `definition/model/validate/avro` per ADR-0115 (the engine-decides / runtime-executes package
> segregation). The library choice recorded above is unchanged; only the import path moved. The
> adapter's error-string prefix (`workflow-validation/avro:`) is unchanged.

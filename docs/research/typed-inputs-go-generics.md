# Typed inputs for wrkflw: what Go 1.26/1.27 offer, and how far the Go type can go

Research note, 2026-09-07. Question: can wrkflw type two input surfaces in Go —
(a) human-task *part* inputs and (b) service-task action arguments — instead of
`map[string]any` validated at runtime against a JSON Schema descriptor, and what
does Go 1.27 add over 1.26 towards that?

Every claim below carries its source. Codebase citations are `path:line` relative
to the wrkflw worktree root. Go claims were verified against the raw release-notes
HTML fetched on 2026-09-07 and against local toolchains (see §0), not against
summaries. Where something could not be verified it is marked **unverified**.

---

## 0. Environment facts

| Fact | Value | Source |
|---|---|---|
| `go version` in this environment | `go1.26.8 darwin/arm64` | `go version` |
| `go env GOTOOLCHAIN` | `auto` | `go env` |
| Module language version | `go 1.26.8` (no `toolchain` line) | `go.mod:3` |
| CI Go pin | `setup-go@v7` with `go-version-file: go.mod` (three jobs) | `.github/workflows/ci.yml:22-24,82-84,144-146`; `codeql.yml:30-32` |
| Toolchains cached in `GOMODCACHE` | go1.25.0, go1.25.12, go1.25.13, go1.26.8, **go1.27.0** | `ls $(go env GOROOT)/..` |
| `GOTOOLCHAIN=go1.27.0 go version` | `go1.27.0 darwin/arm64` | run in `/tmp/gm` |
| `github.com/invopop/jsonschema` | v0.14.0 (latest release, published 2026-04-23), `go 1.24` | `go.mod:16`; module `go.mod`; `gh api repos/invopop/jsonschema/releases/latest` |
| `github.com/santhosh-tekuri/jsonschema/v6` | v6.0.3 (latest release, 2026-08-06), `go 1.21` | `go.mod:26`; module `go.mod`; `gh api …/releases/latest` |
| `github.com/expr-lang/expr` | v1.17.8 | `go.mod:9` |

A 1.27 toolchain is therefore available locally for experiments, but the module
and CI are pinned to 1.26.8. Everything in §3 was compiled under
`GOTOOLCHAIN=go1.26.8` with a `go 1.26` go.mod; everything marked "1.27" was
compiled under `GOTOOLCHAIN=go1.27.0` with a `go 1.27` go.mod.

---

## 1. Go 1.27 versus Go 1.26 (verified)

### 1.1 Language changes

**Go 1.26 (released February 2026)** — "Changes to the language" section of
https://go.dev/doc/go1.26 lists exactly two items:

1. `new(expr)`: "The built-in `new` function … now allows its operand to be an
   expression, specifying the initial value of the variable." The release notes
   motivate it with `encoding/json` optional pointer fields (`Age: new(yearsSince(born))`).
2. Self-referential constraints: "The restriction that a generic type may not refer
   to itself in its type parameter list has been lifted" (`type Adder[A Adder[A]] interface{…}`).

**Go 1.27 (release notes say "arrives in August 2026")** — "Changes to the language"
section of https://go.dev/doc/go1.27 lists exactly three items, quoted verbatim:

1. **Generic methods.** "Go 1.27 now supports generic methods: a method declaration
   may declare its own type parameters. … Note that methods of interfaces may not
   declare type parameters nor can interface methods be implemented by generic methods."
   Example given: `math/rand/v2` gains `(*Rand) N[Int intType](Int) Int`.
2. **Struct-literal selector keys.** "A key in a struct literal may now be any valid
   field selector for the struct type, not just a (top-level) field name of the struct."
3. **Generalised function type inference.** "Function type inference has been
   generalized to apply in all contexts where a generic function is assigned to a
   variable of (or converted to) a matching function type."

Spec confirmation (https://go.dev/ref/spec, header "Language version go1.27"):
`MethodDecl = "func" Receiver MethodName [ TypeParameters ] Signature [ FunctionBody ] .`

Proposal trail for generic methods:

- https://github.com/golang/go/issues/49085 "proposal: spec: allow type parameters in
  methods" — **closed 2026-04-08 as duplicate**; last comment by ianlancetaylor:
  "Closing as a dup of #77273, which has been accepted." (`gh api repos/golang/go/issues/49085`, `…/comments`)
- https://github.com/golang/go/issues/77273 "spec: generic methods for Go" (griesemer,
  opened 2026-01-22) — labels `Proposal-Accepted`, `release-blocker`; milestone
  **Go1.27**; closed 2026-05-26 `completed`. (`gh api repos/golang/go/issues/77273`)

Local proof of the language gate (`/tmp/gm26a`, a type with `func (b Box) As[T any]()`):

```
go.mod "go 1.26", toolchain go1.27.0 → vet: ./main.go:7:17: generic method requires go1.27 or later
go.mod "go 1.26", toolchain go1.26.8 → vet: ./main.go:7:16: method must have no type parameters
go.mod "go 1.27", toolchain go1.27.0 → compiles; prints "generic method: 3 true"
```

So generic methods need **both** a 1.27 toolchain and `go 1.27` in `go.mod`.

**Generic type aliases** are not new in either release: https://go.dev/doc/go1.24
"Go 1.24 now fully supports generic type aliases"; proposal
https://github.com/golang/go/issues/46477 closed `completed`, milestone Go1.24.

**Range-over-func / `iter`**: no language or `iter` package changes appear in the
1.26 or 1.27 notes (heading scan of both pages; `iter`, `slices`, `maps` are not
"Minor changes" headings in 1.27). `iter` itself is Go 1.23 (proposal
https://github.com/golang/go/issues/61897, closed 2024-06-14).

### 1.2 `encoding/json/v2`

| Release | Status | Source |
|---|---|---|
| Go 1.25 | "new, experimental JSON implementation, which can be enabled by setting the environment variable `GOEXPERIMENT=jsonv2` at build time" | https://go.dev/doc/go1.25 |
| Go 1.26 | **Not mentioned** anywhere in the release notes (full-text search for "json" hits only the `new(expr)` example). Still `GOEXPERIMENT=jsonv2`. | https://go.dev/doc/go1.26; local probe below |
| Go 1.27 | **GA.** "Two new packages are now available: `encoding/json/v2` … `encoding/json/jsontext`." "The `encoding/json` package is now backed by the v2 implementation. Marshaling and unmarshaling behavior is preserved, but the exact text of error messages may differ." Opt-out: "`GOEXPERIMENT=nojsonv2` … expected to be removed in a future release." | https://go.dev/doc/go1.27 |

Proposal: https://github.com/golang/go/issues/71497 "encoding/json/v2: new API for
encoding/json" — `Proposal-Accepted`, `release-blocker`, milestone **Go1.27**, closed
2026-06-09 `completed`.

The 1.27 notes also record API changes made during the experiment: removal of the
`format` tag option (#79071), the `unknown` tag option and `DiscardUnknownMembers`
(#77271), `SkipFunc` (#74324); `inline` renamed to `embed` (#79985); changed
behaviour of the `string` option (#79065) and `MatchCaseInsensitiveNames` (CL 792780).

Local probes (`/tmp/gm26b` imports `encoding/json/v2`):

```
go.mod 1.26, toolchain 1.26.8, no GOEXPERIMENT → build constraints exclude all Go files in …/src/encoding/json/v2
go.mod 1.26, toolchain 1.26.8, GOEXPERIMENT=jsonv2 → runs
go.mod 1.26, toolchain 1.27.0 → BUILDS AND RUNS, but `go vet`: json.Unmarshal requires go1.27 or later (module is go1.26)
go.mod 1.27, toolchain 1.27.0 → runs, vet clean
```

The vet finding matters because the 1.27 notes say "`go test` now invokes the
`stdversion` vet check by default" — so under a 1.27 toolchain, wrkflw tests that
import `json/v2` would fail until `go.mod` says `go 1.27`.

v2 semantics relevant to erasure (from `go doc encoding/json/v2` on go1.27.0 and
the probe in `/tmp/gm/main.go`):

- "By default, unmarshaling uses case-sensitive matching" (package doc); probe:
  `{"N":1}` leaves `N int \`json:"n"\`` at 0. v1 matches case-insensitively
  (`go doc encoding/json.Unmarshal`: "ignoring case"); probe under 1.26: `{"N":1}` sets `n` to 1.
- Unknown members "are ignored by default or rejected if `RejectUnknownMembers` is
  specified" (package doc). v1 equivalent is `Decoder.DisallowUnknownFields` only.
- Integer from fractional number: "fails with a SemanticError if the JSON number has
  a fractional or exponent component" (`go doc encoding/json/v2.Unmarshal`); v1 also
  errors (`cannot unmarshal number 1.5 into Go struct field In.n of type int`).
- `null`: "A JSON null may be decoded into every supported Go value where it is
  equivalent to storing the zero value" — same as v1 for non-pointer fields (probe:
  both leave `n=0 tags=nil`, no error).
- Into `any`: both v1 and v2 produce `float64` for numbers (probe:
  `9007199254740993` → `9.007199254740992e+15` in a `map[string]any`, but exact when
  decoded straight into `int`).
- v2 marshals nil slices as `[]` (v1 doc "Migrating to v2": "v2 marshals a nil Go
  slice or Go map as an empty JSON array or JSON object"); probe: `"tags":[]`.

### 1.3 `reflect`, `go/types`, struct tags

- **Go 1.26 `reflect`**: "The new methods `Type.Fields`, `Type.Methods`, `Type.Ins`
  and `Type.Outs` return iterators for a type's fields …, methods, inputs and
  outputs parameters … Similarly, the new methods `Value.Fields` and `Value.Methods`
  return iterators" (https://go.dev/doc/go1.26). This is the only reflection
  addition relevant here; it is a convenience for walking a struct at registration
  time, nothing more.
- **Go 1.27 `reflect`**: no `reflect` heading in "Minor changes to the library".
- **`go/types`**: 1.26 announces and 1.27 executes the permanent removal of the
  `gotypesalias` GODEBUG ("`go/types` now always produces an `Alias` type node for
  alias declarations"); 1.27 adds `go/types.Hasher`. Not relevant to runtime typing.
- **Struct tags / schema**: neither release changes struct-tag syntax. The only
  tag-related items are the `json/v2` tag options listed in §1.2. There is no
  standard-library JSON Schema facility in either release (heading scan of both pages).

### 1.4 Summary table

| Feature | 1.26 | 1.27 | Proposal |
|---|---|---|---|
| Generic methods | no | **yes** (not on interfaces) | #77273 accepted; #49085 closed dup |
| Generic type aliases | yes (since 1.24) | yes | #46477 |
| Function type inference in assignment/conversion contexts | partial | generalised | (release note) |
| Struct-literal selector keys | no | yes | (release note) |
| `new(expr)` | yes | yes | (release note) |
| Self-referential constraints | yes | yes | (release note) |
| `encoding/json/v2` | `GOEXPERIMENT=jsonv2` only | **GA**, `encoding/json` backed by v2 | #71497 |
| `reflect.Type.Fields` iterators | yes | yes | (release note) |
| Std JSON Schema / schema-from-struct | no | no | none found |

---

## 2. The two input surfaces as they exist in wrkflw today

### 2.1 Service-task actions

- The port is untyped: `type Action interface { Do(ctx context.Context, in map[string]any) (out map[string]any, err error) }` (`action/action.go:12-14`), with `ActionFunc` adapting a plain func (`action/action.go:17-21`).
- Read side `Catalog { Resolve(name string) (Action, bool) }` (`action/catalog.go:11-13`); write side `Registrar { Register(name, Action) error; RegisterFunc(name, func(ctx, map[string]any) (map[string]any, error)) error }` (`action/catalog.go:90-98`); `Registry` implements both (`action/catalog.go:108-203`); `MapCatalog` is the read-only map form (`action/catalog.go:18-45`).
- Resiliency is layered through non-embedded wrapper structs plus capability interfaces discovered by type assertion (`TimedAction`, `RetriableAction`, `RecoverableAction`) and an `Unwrap` chain (`action/wrap.go:9-127`). `Wrap` rebuilds layers in canonical order (`action/wrap.go:136-156`). **Any typed wrapper must sit innermost (as the bare action), so it is not stripped by `Unwrap`.**
- Engine input: `serviceActionInput` copies the *whole* variables map and injects `_idempotencyKey` (`engine/step_state.go:354-361`); the command carries it as `InvokeAction.Input` (`engine/step_nodes.go:49-54`). The runtime resolves the name, unwraps to the bare action, and calls `invokeActionDo(tctx, bare, cmd.Input, recoverPanics)` (`runtime/processdriver_action.go:143-161, 392-395`). The returned map goes back as `ActionCompleted` (`runtime/processdriver_action.go:425`) and is merged into variables by the engine.
- Consequence: action inputs today are "all process variables plus one reserved key", not a declared argument set. A typed `In` struct decoded from that map necessarily sees unrelated variables, so strict unknown-field rejection is *not* usable at this surface unless the definition first projects an argument map (see §5 and §8).

### 2.2 Human-task completion input (the surface "parts" would extend)

- No "part" concept exists in the repository yet: `grep -rni '\bparts?\b'` over `docs`, `examples`, `definition`, `humantask` finds only an unrelated comment (`examples/scenarios/parallel_fork_join/main.go:15`). Everything below about parts is design, not code.
- Today a `UserTask` has one validation slot, `CompletionValidation validate.ValidationStrategy` (`definition/activity/activity.go:36-39`, set via `WithCompletionValidation`, `options.go:304-312`), serialised as `NodeWire.Validation *validate.ValidationDescriptor` (`definition/model/node_wire.go:84-90`).
- The submitted payload is `engine.CompletionInput{Outcome, Note, Output map[string]any}` flattened onto the `HumanCompleted` trigger (`engine/trigger.go:266-300`). The runtime validates `inputOf(trg)` — `t.Output` for `HumanCompleted`, `t.Vars` for `StartInstance`, `t.Payload` for `MessageReceived` — through `Gate.Validate` **before** `Step` (`runtime/processdriver.go:922-948`, `runtime/validation/gate.go:39-48`). The engine then `mergeVars(s, t.Output)` (`engine/step_triggers.go:930`).
- Persistence: the whole `InstanceState` (including `Variables map[string]any` and `Tasks []humantask.HumanTask`, `engine/state.go:302-323`) is JSON-marshalled by `marshalSnapshot` (`internal/persistence/store/history_cap.go:9-26`) into `wrkflw_instances.snapshot JSONB` (`internal/persistence/store/migrations/postgres/0001_init.sql:10-19`). Every trigger, including every `HumanCompleted` with its `Output` map, is journaled as a `triggerEnvelope` (`Output map[string]any \`json:"output,omitempty"\``, `internal/persistence/store/trigger_codec.go:53-60`) into `wrkflw_journal.trigger JSONB` (`0001_init.sql:26-34`, kind `human_completed`, `trigger_codec.go:17`).

### 2.3 The validation port and the JSON Schema adapter

- Port: `Validator.Validate(ctx, input map[string]any) error`; `ValidationStrategy.NewValidator()`; `DescribableStrategy.Descriptor() ValidationDescriptor{Kind, Schema string}` (`definition/model/validate/validate.go:17-39`). Kind → factory registry with process-global `DefaultRegistry` and `init()` self-registration by adapters (`validate/registry.go:9-58`, `validate/jsonschema/register.go:8`).
- Round trip: a wire `validation` descriptor becomes a `pendingStrategy` on decode and is resolved to a live strategy via the registry at Build; unresolved strategies fail closed at runtime (`definition/model/validation_wire.go:24-50, 95-122`). Non-describable (callback) strategies cannot be persisted (`ErrUnserializableValidation`, `validation_wire.go:17`, `node_wire.go:MarshalJSON`).
- `jsonschema` adapter: `New(schemaJSON)`, `NewFromValue(map)`, and **`NewFromStruct(v any)`** which runs `invopop.Reflector{DoNotReference: true}.Reflect(v)` and stores the canonical JSON text — the descriptor always carries text, never a Go type (`definition/model/validate/jsonschema/jsonschema.go:23-55`). Validation compiles with `santhosh-tekuri/jsonschema/v6` and validates the `map[string]any` directly (`jsonschema.go:66-90`).
- `expr` adapter: predicates compiled with `expr.Compile(p, expr.AsBool())` and run with the raw map as environment (`definition/model/validate/expr/expr.go:43-72`).
- `Gate` caches compiled validators by `kind + "\x00" + schema` (`runtime/validation/gate.go:54-79`), so the schema text is already the identity of a validator.

**Conclusion of §2:** the persisted contract on both surfaces is (descriptor text, `map[string]any`). The `jsonschema` adapter already supports deriving that text from a Go type. Nothing in the engine, wire format, or stores depends on a Go type, and that is the property to preserve.

---

## 3. Coexistence: schema derived from a Go type, validated regardless of the Go type

### 3.1 What `invopop/jsonschema` v0.14.0 emits

Probe (`/tmp/inv/main.go`, `Reflector{DoNotReference: true}` — the same configuration `NewFromStruct` uses) on a struct with common field types. Observed output:

| Go field | Emitted schema |
|---|---|
| `float64 \`jsonschema:"minimum=0"\`` | `{"type":"number","minimum":0}` |
| `int` | `{"type":"integer"}` |
| `string \`json:",omitempty" jsonschema:"maxLength=200"\`` | `{"type":"string","maxLength":200}`, **not** in `required` |
| `bool` | `{"type":"boolean"}` |
| `time.Time` | `{"type":"string","format":"date-time"}` |
| `*int \`json:",omitempty"\`` | `{"type":"integer"}` — pointer does **not** produce `null` in the type; omitted from `required` only because of `omitempty` |
| `[]string` | `{"type":"array","items":{"type":"string"}}` |
| `map[string]string` | `{"type":"object","additionalProperties":{"type":"string"}}` |
| nested struct / `*struct` | inline object with its own `required` and `"additionalProperties": false` |
| `any`, `json.RawMessage` | `true` (accept anything) |
| `string \`jsonschema:"enum=low,enum=high"\`` | `{"type":"string","enum":["low","high"]}` |
| unexported field, `json:"-"` | omitted |
| top level | `"$schema":"https://json-schema.org/draft/2020-12/schema"`, `"additionalProperties": false`, `required` = every field without `omitempty` |

Source of these behaviours in the module: `reflect.go:341-346` (`integer`/`number` kinds), `:451-453` (`time.Time` → `date-time`), `:338` (interface → true), `:236` (`json.RawMessage`), `:97-107` (`AllowAdditionalProperties`, `RequiredFromJSONSchemaTags` knobs), `:216` (`$schema` version). With `AllowAdditionalProperties: true, RequiredFromJSONSchemaTags: true` the same `Address` struct emitted no `additionalProperties:false` and no `required` list (probe, second output line).

Two defaults deserve a decision because they define the *contract*, not a convenience:

1. **`additionalProperties: false` by default.** For human-task parts this is desirable (closed form). For service-task actions it is *incompatible* with today's input (`serviceActionInput` passes all variables plus `_idempotencyKey`, `engine/step_state.go:355-359`) — a schema derived with defaults would reject every real invocation.
2. **Required = not-`omitempty`.** Go's zero value and JSON Schema's "required" are different concepts; a `count int` field is `required` in the schema even though a missing key decodes cleanly to 0. Authors must decide per field via `omitempty`/pointer.

### 3.2 Compatibility

- invopop v0.14.0 declares `go 1.24` and depends on `pb33f/ordered-map/v2` (module `go.mod`). It builds and runs on go1.26.8 (probe). It imports `encoding/json` (v1). Under a 1.27 toolchain `encoding/json` "is now backed by the v2 implementation. Marshaling and unmarshaling behavior is preserved" (https://go.dev/doc/go1.27), so the emitted schema text is expected to be unchanged; **unverified** — the probe was not re-run under 1.27.
- santhosh-tekuri v6.0.3 declares `go 1.21`. `Schema.Validate` dispatches on the Go dynamic type of the instance: `map[string]any`, `[]any`, `string`, and `json.Number, float32, float64, int, …, uint64` are all accepted as numbers (`validator.go:167`); comparisons go through `big.Rat` (`util.go:322-327`, `validator.go:518-521`). So a variables map holding Go `int` values (e.g. produced in-process by an action) validates the same as one holding `float64` from JSON. It also ships `UnmarshalJSON(io.Reader)` using `Decoder.UseNumber()` (`loader.go:253-257`) for precision-preserving loads.

### 3.3 Verdict on coexistence

Deriving the persisted descriptor from a Go type at authoring/registration time and
validating at runtime purely from the text is exactly what the current adapter does
(`NewFromStruct` → text → `NewValidator` from text). The Go type is then a
consumer-side convenience and the schema text is the contract:

- The definition stored in `wrkflw_definitions.definition JSONB` (`0001_init.sql:58-63`) carries the text; a reload never needs the Go type (`validation_wire.go:95-109`).
- A consumer that registers a typed action or a typed part must accept that **the text is authoritative once persisted**: changing the Go type does not change the stored definition's schema (definitions are versioned by `(def_id, version)`, `0001_init.sql:58-63`; `definitions.go:26-28`). Drift detection therefore has to be an explicit check (§5.2), not an emergent property.

---

## 4. Registration-and-erasure pattern

### 4.1 Typed service-task actions on Go 1.26 (compiles today)

The minimal wrapper, mirrored against `action.Action` and compiled and run under
`GOTOOLCHAIN=go1.26.8`, `go 1.26` (`/tmp/typed/main.go`):

```go
// package action (proposed)

type typedAction[In, Out any] struct {
    fn     func(context.Context, In) (Out, error)
    strict bool // DisallowUnknownFields on the In decode
}

// Typed erases fn behind the untyped Action port: In is decoded from the
// variables map via a JSON round-trip; Out is encoded back to map[string]any.
func Typed[In, Out any](fn func(context.Context, In) (Out, error), opts ...TypedOption) Action

func (a typedAction[In, Out]) Do(ctx context.Context, in map[string]any) (map[string]any, error) {
    var typed In
    if err := decodeInto(in, &typed, a.strict); err != nil {
        return nil, fmt.Errorf("%w: %v", ErrDecodeInput, err) // non-retryable by construction
    }
    out, err := a.fn(ctx, typed)
    if err != nil { return nil, err }
    var m map[string]any
    if err := decodeInto(out, &m, false); err != nil { return nil, fmt.Errorf("…encode typed output: %w", err) }
    return m, nil
}

func decodeInto(src, dst any, strict bool) error {
    b, err := json.Marshal(src); if err != nil { return err }
    dec := json.NewDecoder(bytes.NewReader(b))
    if strict { dec.DisallowUnknownFields() }
    return dec.Decode(dst)
}
```

Observed behaviour of the probe:

```
lenient:                     map[approved:true ref:ana-3] <nil>
strict with unrelated var:   workflow-action: decode typed input: json: unknown field "unrelated"
fractional into int:         … cannot unmarshal number 2.5 into Go struct field ApproveIn.count of type int (is ErrDecodeInput=true)
```

Design notes grounded in the existing package:

- Registration stays on the existing port: `reg.Register("approve", action.Typed(approve))` — no change to `Catalog`/`Registrar` (`action/catalog.go:11-13, 90-98`). `Wrap(action.Typed(fn), WithExecTimeout(…))` works unchanged because the typed action is the bare action and `Unwrap` stops at it (`action/wrap.go:85-93`).
- `Out` must be a struct or `map[string]any`; the round-trip yields the map the engine merges. A scalar `Out` would not decode into `map[string]any` and must be rejected at construction (e.g. `reflect.TypeFor[Out]().Kind()` check) — mirroring Temporal's `isValidResultType` (§6.1).
- Optional capability interface for drift detection, in the style of `TimedAction` etc. (`action/wrap.go:95-127`):
  `type SchemaDescriber interface { InputSchema() string }`; `typedAction` implements it by calling `jsonschema.NewFromStruct(*new(In))` lazily. The definition validator (`definition/model/validate.go`, where node/action references are checked) can then compare a ServiceTask's declared descriptor against `InputSchema()` at Build time (§5.2). Because `ResolvePolicy` walks the `Unwrap` chain, the same walk can find this capability under resiliency layers.
- Decode failure must be **non-retryable**: it is deterministic for a given snapshot. The runtime already consults `action.IsRetryable(err)` (`runtime/processdriver_action.go:401-405`); `ErrDecodeInput` should be classified accordingly.
- Reserved key: `_idempotencyKey` (`engine/step_state.go:359`) is present on every invocation, so `strict` must either whitelist it or the engine must move it out of the argument map (see §8, "what NOT to do").

### 4.2 Typed parts for human tasks (design; the part model does not exist yet)

Keep the engine model untyped and add two consumer-side generics:

```go
// authoring side (definition/activity or definition/model/validate/jsonschema)
func PartSchema[T any](opts ...jsonschema.Option) (validate.DescribableStrategy, error) // NewFromStruct(*new(T))

// consumer side (humantask or a new humantask/typed package)
func DecodePart[T any](parts map[string]any, name string) (T, error)   // reads a part from the persisted map
func EncodePart[T any](v T) (map[string]any, error)                    // builds the submission map
```

Persisted shape stays `parts: []{name, validation: {kind, schema}}` on the node wire
(next to `NodeWire.Validation`, `node_wire.go:84-90`), submissions stay
`map[string]any` in the trigger `Output` and the journal envelope
(`trigger_codec.go:56-57`), and per-part validation goes through the existing `Gate`
keyed by descriptor (`gate.go:54-79`), which already dedupes identical schemas across
parts and nodes. `T` never reaches `engine`, `runtime`, or `internal/persistence`.

### 4.3 What Go 1.27 would change for this pattern

- **Generic methods do not help the port.** The release note is explicit: "methods of interfaces may not declare type parameters nor can interface methods be implemented by generic methods" (https://go.dev/doc/go1.27). `Registrar.RegisterTyped[In, Out](…)` is therefore impossible on the interface; a generic method on the concrete `*Registry` would fragment the port (`MapCatalog` could not have it). The package-level `action.Typed[In, Out]` shape is the right one on both versions.
- **Generalised function type inference** (1.27) would let `action.Typed(approve)` infer `In, Out` in more assignment/conversion contexts; the probe shows inference already works for the direct call form on 1.26.
- **`encoding/json/v2` (1.27, GA)** is the concrete win: `RejectUnknownMembers(true)` as an option instead of a `Decoder`, exact case-sensitive member matching (v1's case-insensitive matching means the schema and the decoder disagree about which key is which — §5.4), `omitzero`, and faster unmarshal. It requires `go 1.27` in `go.mod` for `go vet`/`go test` to pass (§1.2 probe).
- **`reflect.Type.Fields` (1.26)** is enough for any registration-time struct walk; nothing in 1.27 adds to it.
- **`new(expr)` (1.26)** is a small ergonomic gain for optional pointer fields in `Out` structs.

---

## 5. Failure modes and what stays runtime-validated

### 5.1 Go type changes under a running instance

The snapshot (`wrkflw_instances.snapshot`) and journal hold the old map shape; the
definition version that instance runs is pinned (`def_id, def_version` on the instance
row, `0001_init.sql:10-13`). A redeploy with a changed `In`:

- Added required field → `In` decodes with a zero value silently (v1 and v2 both treat missing as zero: probes in §1.2). The **schema** in the pinned definition version still describes the old shape, so the gate passes. Only a schema/type drift check at registration (§5.2) can surface this.
- Removed field → strict decode rejects old payloads (`unknown field`); lenient decode drops the value silently.
- Renamed field → same as removed+added.
- Type change (e.g. `int` → `string`) → decode error, deterministic, non-retryable; instance stalls on the action with an `ActionFailed` unless the failure is routed to an incident. This is the failure class River avoids by identifying jobs by `Kind()` string rather than type and by explicitly documenting the multi-deploy rename protocol (§6.2).

Mitigations that keep the contract schema-first: version the action *name* (or the
definition version) when `In` changes incompatibly; never mutate a persisted
definition's schema in place; treat `ErrDecodeInput` as a definition/deploy defect,
not a transient.

### 5.2 Schema and Go type drift — which wins and how to detect

- **The persisted schema wins at runtime.** The gate runs from the descriptor text (`runtime/processdriver.go:927-933`, `gate.go:39-48`); the typed wrapper never sees the schema.
- **Detection point:** the only moment both are in hand is definition Build/validate with a catalog available. Proposal: when a ServiceTask (or part) declares a `json-schema` descriptor and the resolved action implements `SchemaDescriber`, compare the two documents. A byte-wise comparison is too brittle (key order, `$schema`); a structural comparison of `properties`/`required`/`type` after parsing both with `santhosh-tekuri`'s `UnmarshalJSON` is sufficient and dependency-free. Report as a definition validation error or warning; do not auto-rewrite the persisted text.
- If the definition carries **no** descriptor but the action is typed, the derived schema can be *offered* (`NewFromStruct`) as the descriptor at authoring time — never injected at reload, because reload has no catalog guarantee (`ScopedActions` are marshal-only, `node_wire.go:165-171`).

### 5.3 Unknown fields

- Human-task parts: reject. Both the schema (`additionalProperties: false`, the invopop default) and the decoder (`DisallowUnknownFields` / `RejectUnknownMembers`) agree.
- Service-task actions: cannot reject today, because the input is the whole variables map plus `_idempotencyKey` (`engine/step_state.go:354-361`). Either derive the action schema with `AllowAdditionalProperties: true` and decode leniently, or introduce an explicit argument projection on the ServiceTask node (an `args` mapping evaluated by the engine). The second is a definition-model change outside this note's scope; without it, "typed" at this surface means "typed view of a subset of variables".

### 5.4 Numbers through JSON

- Any value that enters through a JSON transport into `map[string]any` is already `float64` (`go doc encoding/json.Unmarshal`: "float64, for JSON numbers"; identical for v2 in the probe). `9007199254740993` becomes `9.007199254740992e+15` *before* the typed wrapper runs; the wrapper cannot recover it. Only decoding the transport body with `UseNumber` (v1) or into `json.Number`/`int64` directly avoids it. The JSON Schema validator is precision-safe by itself (`big.Rat`, `validator.go:518-521`) — so the schema can pass a value the Go type has already lost.
- Fractional into `int`: both v1 and v2 error (probes). A schema `"type":"integer"` rejects `2.5` too, so the gate and the decoder agree.
- Values produced in-process (an action returning `map[string]any{"count": 3}`) stay Go `int` in the variables map until the next snapshot round-trip turns them into `float64`. The wrapper's marshal→unmarshal path normalises both cases; santhosh accepts both (`validator.go:167`).

### 5.5 nil vs missing

- Non-pointer struct fields cannot distinguish `null`, missing, and zero (both v1 and v2 probes: `{"n":null}` → `n=0`, no error). Only pointer fields (`*int`, and now `new(expr)` for authoring them) or `omitzero` preserve the distinction.
- JSON Schema distinguishes *missing* (`required`) from *present null* (`"type":"integer"` rejects `null`; invopop does not emit nullable types for pointers — §3.1). So a `*int` field that is `required` in the schema (no `omitempty`) rejects `null` at the gate while Go would accept it. Authors must pick one policy per field via tags; the derived schema documents it.

### 5.6 Case sensitivity (v1 only)

Under Go 1.26 `encoding/json`, `{"Amount": 1}` decodes into `Amount float64 \`json:"amount"\`` (case-insensitive, probe), whereas JSON Schema `properties` are case-sensitive, so with `additionalProperties:false` the gate rejects the same payload and with `additionalProperties:true` the gate ignores the key that Go then binds. Under `json/v2` (Go 1.27) matching is exact and the two agree. On 1.26 the mismatch is only reachable if the gate is bypassed, since the gate runs first (`processdriver.go:922-933`).

### 5.7 What must remain runtime-validated even with typed wrappers

1. **Every external-input trigger** (`StartInstance.Vars`, `MessageReceived.Payload`, `HumanCompleted.Output`, `processdriver.go:939-948`) — the payload comes from a transport, not from Go, and the definition (not the binary) says what is acceptable.
2. **Cross-field and value constraints** (`minimum`, `enum`, `maxLength`, `expr` predicates) — Go types express structure, not ranges; invopop only emits these from `jsonschema:"…"` tags the author writes.
3. **Reloaded definitions** — the Go type may not be linked into the process that reloads a stored definition (`reconcileNodeValidationLenient`, `validation_wire.go:111-122`, exists precisely for that case).
4. **Journal replay** — `JournalReader.Entries` returns `engine.Trigger` values whose maps are decoded from `wrkflw_journal.trigger` (`runtime/kernel/ports.go:24-26`, `trigger_codec.go`); replay must not depend on a Go type that may have changed since the entry was written.
5. **Outcome validation** for user tasks (`ErrOutcomeRequired`/`ErrInvalidOutcome`, `engine/step_triggers.go:918-927`) — already engine-side and name-based; unchanged.

---

## 6. Precedents

### 6.1 Temporal Go SDK (v1.48.0, 2026-08-18)

- **Registration:** `RegisterActivityWithOptions(af any, …)` accepts a func (or a struct pointer whose methods become activities) and validates the signature by reflection: `validateFnFormat` requires a `reflect.Func`, forbids `workflow.Context` in activity args, and requires returns of `(<result>, error)` or `(error)`; `isValidResultType` rejects `Func`, `Chan`, `UnsafePointer` results (`internal/internal_worker.go:811-870`, `1088-1145`, `2751-2760`). The registered **name** is the function name derived with `runtime.FuncForPC(reflect.ValueOf(i).Pointer()).Name()` (trimmed `-fm` suffix for method values) unless an alias is given (`2766-2790`).
- **Erasure:** `workflow.ExecuteActivity(ctx Context, activity any, args ...any) Future` (`internal/workflow.go:1029`); results are recovered by `Future.Get(ctx Context, valuePtr any) error` (`internal/workflow.go:377`), which calls `dataConverter.FromPayloads(payloads, valuePtr)` (`internal/internal_workflow.go:1778-1797`).
- **Payload/versioning:** `DataConverter{ToPayload(value any) (*Payload, error); FromPayload(payload, valuePtr any) error; …}` (`converter/data_converter.go:18-39`); the default `JSONPayloadConverter` is plain `json.Marshal`/`json.Unmarshal` (`converter/json_payload_converter.go:20-35`) and stamps `metadata["encoding"]="json/plain"` so a `CompositeDataConverter` can route by encoding (`composite_data_converter.go:28-29, 162-168`). Typing is compile-time only on the *activity implementation*; the call site is `any`, and there is no schema. Lesson for wrkflw: reflection-derived names are fragile across renames; wrkflw's explicit string names are already the safer choice.

### 6.2 River (v0.47.0, 2026-08-31)

- **Registration is generic and name-based:** `type JobArgs interface { Kind() string }` (`job.go:21-37`); `type Worker[T JobArgs] interface { … Work(ctx, *Job[T]) error … }` (`worker.go:39`); `AddWorker[T JobArgs](workers *Workers, worker Worker[T])` instantiates `var jobArgs T` and registers under `jobArgs.Kind()` (`worker.go:108-135, 177`); `WorkFunc[T JobArgs](f func(ctx, *Job[T]) error) Worker[T]` uses `(*new(T)).Kind()` (`worker.go:222`).
- **Erasure:** `wrapperWorkUnit[T].UnmarshalJob` does `json.Unmarshal(w.jobRow.EncodedArgs, &w.job.Args)` (`work_unit_wrapper.go:44-50`); args are stored as encoded JSON in the job row.
- **Versioning:** the `JobArgs` doc is explicit — "Jobs are identified by a string instead of being based on type names so that previously inserted jobs can be worked across deploys even if job/worker types are renamed"; renaming a kind is a three-deploy protocol via `JobArgsWithKindAliases` (`job.go:21-70`). No schema; the Go type is the only contract, and the docs accept that consequence.
- Lesson: River is the closest shape to `action.Typed` — package-level generic function, string identity, JSON erasure — and it compiles on Go 1.18+ generics; nothing in it needs 1.27.

### 6.3 Restate Go SDK (v1.0.4, 2026-08-21)

- **Two registration styles.** Generic: `type ServiceHandlerFn[I any, O any] func(ctx Context, input I) (O, error)`; `NewServiceHandler[I, O](fn, opts...)`; `Call(ctx, bytes []byte) ([]byte, error)` does `var input I; encoding.Unmarshal(codec, bytes, &input)`, calls `fn`, then `encoding.Marshal(codec, output)` (`handler.go:24-75`). Reflective: `restate.Reflect(rcvr any)` walks exported methods, accepts `(ctx, I) (O, error)` and seven other shapes, extracts `I` = `mtype.In(2)` and `O` = `mtype.Out(0)`, and panics on anything else (`reflect.go:36-60, 140-200`); its `Call` uses `reflect.New(h.input)` + `encoding.Unmarshal` + `h.fn.Call(args)` (`reflect.go:265-310`).
- **Erasure/codec:** `Codec{Marshal(v any) ([]byte, error); Unmarshal(data []byte, v any) error}`; the default `jsonCodec` is `json.Marshal`/`json.Unmarshal` (`encoding/encoding.go:105-108, 246-258`).
- **Schema coexistence — the most relevant precedent.** The JSON codec optionally implements `CodecMetadata{ContentType() string; JsonSchema(v any) any}` (`encoding.go:112-117`), and `InputPayloadFor`/`OutputPayloadFor` attach a JSON Schema for the handler's `I`/`O` to service discovery (`encoding.go:67-90`). The generator imports `github.com/invopop/jsonschema` (`encoding.go:10`) and falls back to an empty schema on panic (`generateJsonSchema`, `encoding.go:358-372`). Decode failure is a terminal `400` (`handler.go:59-61`). Same library, same "type → schema at registration, JSON at the boundary" split this note recommends.

### 6.4 Dapr / durabletask-go (v0.14.1, 2026-08-26)

- `type Activity func(ctx ActivityContext) (any, error)`; input is pulled by the implementation with `ActivityContext.GetInput(resultPtr any) error` → `unmarshalData(actx.rawInput, v)` (`task/activity.go:133-170`), where `unmarshalData` is `protojson.Unmarshal` for `proto.Message` else `json.Unmarshal`, and `marshalData` the mirror (`task/executor.go:206-226`). Registration is `AddActivity(a)` with the name from `helpers.GetTaskFunctionName` (reflection) or `AddActivityN(name, a)` (`task/registry.go:91-107`). Entirely untyped at the boundary; no schema. Least relevant of the four.

### 6.5 What the precedents agree on

| | Typed registration | Identity | Erasure | Schema |
|---|---|---|---|---|
| Temporal | reflection over func signature | function name (reflection) or alias | DataConverter, JSON default | none |
| River | `Worker[T]`, `AddWorker[T]` | `Kind()` string | `json.Unmarshal` into `T` | none |
| Restate | `NewServiceHandler[I,O]` or `Reflect` | method/service name | Codec, JSON default | invopop-derived, advertised |
| Dapr | none (`GetInput(&v)`) | function name or explicit | JSON/protojson | none |

None of them requires anything newer than Go 1.18 generics. None makes the persisted
payload depend on the Go type; all of them accept JSON's number/null semantics as-is.

---

## 7. Answers to the scoped questions

1. **What 1.27 adds vs 1.26:** generic methods (not on interfaces), struct-literal selector keys, broader function type inference; `encoding/json/v2` GA with `encoding/json` reimplemented on it; `go/types.Hasher`; `stdversion` vet in `go test`. Generic type aliases are 1.24. `reflect` field iterators are 1.26. No struct-tag or schema facility in either. (§1)
2. **Coexistence:** already the architecture — `NewFromStruct` derives text, the text is persisted, the runtime validates from text. Decide the two invopop defaults (`additionalProperties`, `required`) per surface. (§3)
3. **Pattern:** package-level `action.Typed[In, Out]` returning `action.Action`, compiled on 1.26 (§4.1); `PartSchema[T]`/`DecodePart[T]` for parts (§4.2). 1.27 improves the decoder (`json/v2`), not the registration shape (§4.3).
4. **Precedents:** §6; River and Restate are the templates.
5. **Failure modes:** §5; the schema wins at runtime, drift is detected only at Build with a catalog present, and numbers/null/unknown-field semantics are set by the transport before any Go type sees them.

---

## 8. Recommendation

**Which surfaces can be typed at the consumer boundary**

- **Service-task actions: yes, now.** Add `action.Typed[In, Out any](fn func(context.Context, In) (Out, error), opts ...TypedOption) Action` in package `action`, registered through the unchanged `Registrar`/`Catalog` port and placed innermost so `Wrap`/`Unwrap` keep working. Decode `In` from the variables map and encode `Out` back with a JSON round-trip. Default to lenient unknown-field handling because the engine passes the whole variables map plus `_idempotencyKey` (`engine/step_state.go:354-361`); offer `WithStrictInput()` for consumers who project arguments themselves. Classify `ErrDecodeInput` as non-retryable.
- **Human-task parts: yes, at authoring and consumption, once parts exist.** Author each part's descriptor with `jsonschema.NewFromStruct`/`PartSchema[T]`, keep the persisted part list as `{name, validation: {kind, schema}}`, keep submissions as `map[string]any` in the trigger, snapshot and journal, and give consumers `DecodePart[T]`/`EncodePart[T]` helpers. Reject unknown fields at both the schema (`additionalProperties:false`) and the decoder for this surface.
- **Start variables and message payloads:** same mechanism is available (`inputOf` already gates them) but out of scope here.

**Exact mechanism**

1. Go type → schema text at authoring/registration via invopop (`NewFromStruct`, already present); the text goes into the descriptor and is what `wrkflw_definitions` stores.
2. Runtime validation stays exactly where it is: `Gate.Validate` from the descriptor text, before `Step`.
3. Erasure at the boundary: JSON round-trip `map[string]any` ↔ `In`/`Out` (Go 1.26: `encoding/json` + `Decoder.DisallowUnknownFields`; Go 1.27: `encoding/json/v2` with `RejectUnknownMembers` and exact-case matching).
4. Drift detection at definition Build when a catalog is present: an optional `SchemaDescriber { InputSchema() string }` capability on typed actions, compared structurally against the node's descriptor; report, never rewrite.

**Go version floor: 1.26 (current).** Everything above compiles and runs on go1.26.8 with `go 1.26` (probe §4.1). Move to `go 1.27` only for the `json/v2` decoder benefits (strict membership as an option, case-sensitive matching that agrees with JSON Schema, `omitzero`, faster unmarshal); note that importing `json/v2` under a 1.27 toolchain with a `go 1.26` module builds but fails `go vet`/`go test` (`stdversion`), so it is an all-or-nothing module bump. Generic methods (1.27) do not improve this design because the port is an interface and interface methods cannot be generic.

**What NOT to do**

- Do not make `engine`, `runtime`, `internal/persistence`, or the wire/JSONB formats reference Go types, type names, or reflection-derived identities. Identity stays the action name / part name string (River's `Kind()` argument, §6.2).
- Do not let the Go type override a persisted definition's schema at reload, or auto-rewrite stored descriptors when a type changes; a type change that alters the contract is a new definition version or a new action name.
- Do not derive action schemas with invopop's default `additionalProperties:false` while the engine still passes the whole variables map; it would reject every invocation.
- Do not enable strict unknown-field rejection on actions without first removing or whitelisting `_idempotencyKey`.
- Do not rely on the typed wrapper for numeric precision or null/missing distinctions; those are decided at the transport decode (`float64` for JSON numbers) and by pointer/`omitempty` choices that the derived schema must reflect.
- Do not add generic methods to `Registry` on 1.27 as a "typed registrar"; it would split the `Registrar` port between `Registry` and `MapCatalog`.

---

## Sources

Primary Go sources

- Go 1.27 release notes — https://go.dev/doc/go1.27 (raw HTML fetched 2026-09-07; sections "Changes to the language", "New encoding/json/v2 and encoding/json/jsontext packages", "Minor changes to the library" › go/types, math/rand/v2, "Tools")
- Go 1.26 release notes — https://go.dev/doc/go1.26 ("Changes to the language", "Minor changes" › reflect, go/types)
- Go 1.25 release notes — https://go.dev/doc/go1.25 ("New experimental encoding/json/v2 package")
- Go 1.24 release notes — https://go.dev/doc/go1.24 ("Changes to the language": generic type aliases)
- The Go Programming Language Specification, "Language version go1.27", § Method declarations — https://go.dev/ref/spec
- golang/go#77273 "spec: generic methods for Go" — https://github.com/golang/go/issues/77273
- golang/go#49085 "proposal: spec: allow type parameters in methods" — https://github.com/golang/go/issues/49085
- golang/go#71497 "encoding/json/v2: new API for encoding/json" — https://github.com/golang/go/issues/71497
- golang/go#46477 "spec: generics: permit type parameters on aliases" — https://github.com/golang/go/issues/46477
- golang/go#61897 "iter: new package for iterators" — https://github.com/golang/go/issues/61897
- `go doc encoding/json/v2`, `go doc encoding/json/v2.Unmarshal`, `…RejectUnknownMembers`, `…MatchCaseInsensitiveNames` (toolchain go1.27.0); `go doc encoding/json` "Migrating to v2" (go1.27.0); `go doc encoding/json.Unmarshal` (go1.26.8)

Local probes (scratch, not committed)

- `/tmp/gm` (go 1.27): generic method + json/v2 semantics; `/tmp/gm26a`, `/tmp/gm26b`: language/toolchain gating; `/tmp/v1` (go 1.26): encoding/json v1 semantics; `/tmp/inv` (go 1.26): invopop emission; `/tmp/typed` (go 1.26): the `Typed[In,Out]` wrapper

Libraries (module cache paths under `$(go env GOMODCACHE)`)

- `github.com/invopop/jsonschema@v0.14.0/go.mod`, `reflect.go:97-144, 216, 236, 338-346, 451-453`
- `github.com/santhosh-tekuri/jsonschema/v6@v6.0.3/go.mod`, `validator.go:167, 518-521`, `util.go:322-327`, `loader.go:253-257`

Precedents (GitHub default branches, fetched 2026-09-07)

- Temporal Go SDK — `internal/internal_worker.go` (811-870, 1088-1145, 2751-2790), `internal/workflow.go` (377, 1029), `internal/internal_workflow.go` (1778-1797), `converter/data_converter.go` (18-39), `converter/json_payload_converter.go` (20-35), `converter/composite_data_converter.go` (28-29, 162-168) — https://github.com/temporalio/sdk-go
- River — `job.go` (21-70), `worker.go` (39, 81, 108-135, 177, 198-222), `work_unit_wrapper.go` (13-50) — https://github.com/riverqueue/river
- Restate Go SDK — `handler.go` (24-90), `reflect.go` (36-60, 140-200, 265-310), `encoding/encoding.go` (10, 67-90, 105-117, 246-258, 358-372) — https://github.com/restatedev/sdk-go
- Dapr durabletask-go — `task/activity.go` (133-170), `task/registry.go` (91-107), `task/executor.go` (206-226) — https://github.com/dapr/durabletask-go

wrkflw worktree files cited

- `go.mod`; `.github/workflows/ci.yml`, `codeql.yml`
- `action/action.go`, `action/catalog.go`, `action/wrap.go`
- `definition/model/validate/validate.go`, `registry.go`; `validate/jsonschema/jsonschema.go`, `register.go`; `validate/expr/expr.go`
- `definition/model/validation_wire.go`, `node_wire.go`; `definition/activity/activity.go`, `options.go`
- `engine/state.go`, `trigger.go`, `step_state.go`, `step_nodes.go`, `step_triggers.go`
- `runtime/processdriver.go`, `processdriver_action.go`, `runtime/validation/gate.go`, `runtime/kernel/ports.go`
- `humantask/humantask.go`
- `internal/persistence/store/trigger_codec.go`, `history_cap.go`, `definitions.go`, `migrations/postgres/0001_init.sql`

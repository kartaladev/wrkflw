# Design

## Context

See proposal.md, *Why*. The current state that shapes the approach:

- `ProcessDefinition.UnmarshalJSON` (`definition/model/node_wire.go`) decodes into `definitionWire` through a `json.Decoder` with `DisallowUnknownFields`. Nodes decode as `nodeWireIn` (a `NodeWire` plus the retired `label`). Flows decode as `flow.SequenceFlow`. A node's nested objects decode as `*RetryPolicy`, `*TriggerWire` (whose `at_times` entries are `schedule.ClockTime`) and `*validate.ValidationDescriptor`. `subprocess` is a `*ProcessDefinition`, so encoding/json re-enters `UnmarshalJSON` for every nested level. `rule` goes through `RuleSpec.UnmarshalJSON`, and an object there is kept as an opaque inline document.
- encoding/json binds a key to a field case-insensitively, and `DisallowUnknownFields` uses the same match. `action/typed.go` already records the consequence for typed input: the match also folds U+017F onto `s` and U+212A onto `k`.
- Measured on main with go1.26.9, using a scratch probe outside the repo:
  - every row of the new spec scenario *A JSON key that case-folds onto a defined key is refused at every level* decodes with a nil error;
  - `{"id":"d","ID":"z"}` decodes to the ID `z`;
  - YAML refuses `ID`, `Name` and `Target` with `field <key> not found in type ...`.
- The definition store only ever writes `json.Marshal(def)` output (`PublishDefinition`, `internal/persistence/store/definitions.go`). It decodes stored bytes with `json.Unmarshal` in `GetDefinition`, in `Lookup`, and in `normaliseDefinition` when a republish compares content.

## Goals / Non-Goals

**Goals:**
- Refuse every fold-equal-but-not-exact JSON key at every wire-format position with `ErrCaseVariantKey`. Leave every other decode outcome unchanged: plain unknown keys keep encoding/json's error, and the label, kind-key, vocabulary and rule gates are unchanged.
- One check in one package, derived from the decode types rather than from a hand-written key table.
- A linear cost on the definition-load hot path, measured.

**Non-Goals:**
- Changing YAML decoding.
- Exact-duplicate key detection.
- A nesting path in the error (kartaladev/wrkflw#217).
- Typed action input: `harden-typed-action-input` (kartaladev/wrkflw#142).

## Decisions

### D1. An exact-key scan of the raw JSON, driven by a schema derived from the decode types

Before `dec.Decode`, `UnmarshalJSON` walks the input once with `json.Decoder.Token`. It tracks the wire position it is in: definition, node, flow, retry policy, trigger, clock time or validation descriptor. Each object key is compared with the exact keys allowed at that position. A key that is not exact but `strings.EqualFold`s an allowed key returns `ErrCaseVariantKey` at once. A key that matches nothing is left for `DisallowUnknownFields`, which keeps every existing unknown-key message and test unchanged.

How the walk treats the two special values:
- It skips the value of `rule`. An inline rule document's keys are the rule engine's, not the wire format's.
- It skips the value of `subprocess`, because the nested `UnmarshalJSON` call scans its own bytes. Walking it here as well would make the cost quadratic in nesting depth.

Each position's allowed-key set is built once, behind `sync.Once`, by reflecting over the `json` tags of the type that decodes it: `definitionWire`, `nodeWireIn` with promoted `NodeWire` fields, `flow.SequenceFlow`, `RetryPolicy`, `TriggerWire`, `schedule.ClockTime` and `validate.ValidationDescriptor`. This follows the precedent in `node_wire_keys.go`, which derives read sets instead of writing them down because a written table drifts. Adding a field with a tag then extends the check with no edit.

**Fold relation.** encoding/json's matcher folds ASCII case plus U+017F and U+212A. Every defined key is ASCII lower case, digits and `_`. For such a target, `strings.EqualFold` accepts exactly the ASCII case variants and those two runes, since they are the only non-ASCII runes whose simple-fold orbit contains an ASCII letter. This was measured on go1.26.9 by walking `unicode.SimpleFold` over every rune. The same probe showed encoding/json binding U+212A to `k` and U+017F to `s`, but not binding U+0130 or U+0131 to `i`. The spec rows for U+212A and U+017F pin this, and so do the near-miss rows (`nàme`, whitespace), which must stay unknown keys.

**Alternatives rejected:**
- *A custom `UnmarshalJSON` on each nested type* (`SequenceFlow`, `RetryPolicy`, `TriggerWire`, `ClockTime`, `ValidationDescriptor`). It spreads the rule across four packages. It also changes how those exported types decode outside a definition, which is behavior the spec does not cover.
- *Decoding each level into `map[string]json.RawMessage` first.* It allocates a map and a copy per object on the read hot path, where a token walk allocates neither.
- *Hand-written key lists per position.* They drift, for the reason `node_wire_keys.go` records.

### D2. Not `encoding/json/v2`

Its exact-case matching would close this outright. Measured on go1.26.9, `go doc encoding/json/v2` reports "no buildable Go source files" unless `GOEXPERIMENT=jsonv2` is set. Depending on it would force every consumer's build to set an experiment flag. That is a change to consumer build configuration, the same class of cost as a go.mod change, which `openspec/config.yaml` says must always be called out. kartaladev/wrkflw#142 also records the move as blocked on the module decision in #116. A third-party decoder is rejected for the same go.mod reason.

### D3. Persisted definitions: no lenient mode, an operator repair recipe instead

**Needs human confirmation (Readiness: human).**

The library cannot have written an affected row, because `PublishDefinition` stores only `MarshalJSON` output and that writes exact tags. So an affected row was written out of band. After upgrade it fails `GetDefinition` and `Lookup` with an error wrapping `ErrCaseVariantKey` that names the key, so it is loud, not silent. A republish over it gets the store's existing non-disclosing error "cannot decode the stored definition".

**Options rejected:**
- *A lenient decode option.* `UnmarshalJSON` takes no options, and `json.Unmarshal` cannot pass any. A switch would have to be package-global mutable state, or a store option that swaps decoders, which forks the decode path the parity contract keeps single. It would also re-admit exactly the silence this change removes.
- *Fold-and-normalise on load.* This is today's behavior with extra steps. With a shadowing pair, it must pick a winner silently.

**Evidence that this is the established stance.** Two earlier tightenings made out-of-band or old rows unloadable with no compatibility mode, and both are pinned by tests: the snake_case wire rename (commit 8179c0b0, `TestDefinitionStorePathIsStrict` "a legacy camelCase blob no longer loads") and the retired `label` key (`ErrRetiredLabelKey`).

**Repair recipe, for the PR body and release notes.** Before upgrading, while still on the previous release, decode each row in `wrkflw_definitions` with `json.Unmarshal` and re-encode it with `json.Marshal`. Update the row wherever the canonical bytes differ in their key set. The previous release folds keys and resolves a shadowing pair last-wins, so the re-encoded row means exactly what running instances were already executing. The repair preserves behavior, not just loadability. After upgrading, any row missed surfaces by name on first load.

### D4. Cost on the load path

`GetDefinition` and `Lookup` decode on every call, so the scan adds one token pass per definition level. Building the key sets is a one-time cost. The walk keeps no per-key allocation beyond what `Token` returns for a string. The existing `BenchmarkGoldenDecode` is run before and after with `-count=10`, and the benchstat output goes in the PR. A regression is a review question; this change sets no numeric ceiling.

### D5. Error shape

The error is `fmt.Errorf("%w: key %q matches defined key %q", ErrCaseVariantKey, authored, defined)`, raised on the first variant in document order. Raising on the first keeps the error bounded however many variants a document holds. The authored key is itself bounded: to fold onto a defined key it has the same rune count, and at most 3 bytes per rune. So naming it cannot become a log-flooding vector, the risk `TestParseYAMLBoundsUnknownFieldErrorSize` guards on the YAML side.

## Risks / Trade-offs

- **[An out-of-band stored row strands its in-flight instances after upgrade]** → Release note with the D3 repair recipe; the failure names the key.
- **[The schema walk diverges from what encoding/json actually decodes]** (a new nested object type is added without registering its position) → Derive positions from the decode types' field types, not from a list. Add a test that every struct-typed field reachable from `definitionWire` has a position, excluding `rule` and `subprocess` by name.
- **[The fold relation is wider or narrower than encoding/json's]** → The U+212A, U+017F and near-miss spec rows; plus a property test that, for every defined key and every ASCII case permutation of its first three letters, the scan and encoding/json agree on "binds".
- **[A double scan of nested subprocesses]** → The walk skips `subprocess` (D1). A depth-two scenario row exercises the recursion.

## Migration Plan

1. Ship with **BREAKING** and the D3 repair recipe stated in the PR body. The repository keeps no CHANGELOG file.
2. Rollback is reverting the change. No schema or stored data changed, so a rolled-back library reads every row it could read before.

## Open Questions

- Should the library ship the D3 repair as a helper, for example a store method or an example program, rather than as a release-note recipe? This does not change the spec or the approach.
- Should YAML's refusal of a case-variant key also wrap `ErrCaseVariantKey`, so both formats share one sentinel? Today it is yaml.v3's unknown-field error. Doing it would be an ADDED YAML scenario in a later change; this change keeps YAML unchanged.
- Exact duplicate keys (`{"id":"a","id":"b"}`) decode last-wins in JSON and are refused in YAML. This was measured during planning. It is a format asymmetry no change owns, and needs intake.

## Coupling

- `definition/model/node_wire.go` is also the home of the decode error path that kartaladev/wrkflw#217 (pending migration) would restructure. Whichever lands second rebases. No in-flight change touches the file.
- `ErrCaseVariantKey` is a new public identifier in `definition/model`, owned by this change.
- No migration files, store code or shared public types change.

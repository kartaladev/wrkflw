# Design

## Context

See proposal.md (Why) for the motivation and the re-measured premises. The current shape that constrains the approach:

- Both decoders funnel each node through one seam: `fromWire` runs the kind-key gate (`checkNodeKeys`) and then the termination-outcome gate. `checkNodeKeys` returns on the first refused field, in `NodeWire` field order.
- JSON nesting goes through `encoding/json`. `NodeWire.Subprocess` is a `*ProcessDefinition`, so the outer `Decode` calls the nested `UnmarshalJSON`, and that runs the nested gates *before* any outer node reaches `fromWire`. A nested refusal aborts the outer `Decode`, and `encoding/json` returns it unwrapped. Measured: no path at depth 1 or 2.
- YAML nesting is explicit. `fromNodeYAML` calls `coreFromYAML` and then `build()` (which validates) on the inline subprocess before the outer node's gate. It wraps errors as `subprocess %q: ...`, which gives YAML one level of path today.
- `ProcessDefinition.MarshalJSON` recurses through a fresh `json.Marshal` per level, so `encoding/json`'s own cycle detector never fires (#204, reproduced). `Validate` walks with a global `seen` set: it terminates on a cycle but does not refuse one.
- `yaml.go` already bounds a YAML field-error list at `maxReportedFieldErrors = 20`. The rationale is measured log flooding: a 1.9 MB input gave a 4.7 MB error.

## Goals / Non-Goals

**Goals:**
- One reporting path for node decode refusals. It gives location, completeness and the same order in both formats.
- No process abort on any definition a caller can build in Go.
- No refusal whose existence depends on the Go toolchain's `encoding/json`.
- Guards over the two struct-parity hazards (#208, #218), whose failure direction is open.

**Non-Goals:**
- Unknown-key completeness in JSON. `encoding/json` stops at the first unknown key, and replacing that decoder is out of proportion (see `refuse-case-variant-json-keys` design D2, which rejected moving to `encoding/json/v2`).
- When structural validation runs. YAML validates inline subprocesses at parse time and JSON never validates at decode. That stays; only the order relative to decode refusals changes.

## Decisions

**D1. Aggregate node decode refusals with `errors.Join`, capped at 20.** Each refusal is its own error and keeps its sentinel, wrapped with its path. The joined error lists them in document order (pre-order: a node's own refusals in field order, then its inline subprocess, then the next node). After 20 it adds one line, "and N more". Rejected:
- Unbounded join. It is the log-flood vector `maxReportedFieldErrors` was measured against.
- A custom multi-error type. `errors.Join` already gives `errors.Is` for every member, which is what the spec promises.
- Stopping at the first refusal. That is C9.

The cap reuses the existing constant, so YAML field errors and gate refusals agree on one number.

**D2. JSON decodes the inline subprocess itself, after the outer node's gates.** The JSON node wire reads `subprocess` as raw bytes. The outer `UnmarshalJSON` gates every node at its level first, then decodes each raw subprocess recursively with the path extended. Rejected: wrapping the error that the nested `UnmarshalJSON` returns. That would add a path, but measured, a nested refusal aborts the outer `Decode` before any outer node is gated. The outer refusals could then never join the report (spec scenario "subProcess 'sub' carrying action whose endEvent 'e' carries action"). The raw form decodes the nested bytes once, so there is no second parse of the same bytes. The benchmark task checks that.

**D3. YAML runs every gate in the tree before it builds any inline subprocess.** `coreFromYAML` gains a first pass that gates all nodes recursively and collects refusals with paths. `build()` runs on inline subprocesses only when that pass is clean. This is what makes C8's measured divergence disappear. JSON never validates at decode, so YAML must not report a structural defect while a decode refusal exists. Rejected: making JSON validate nested subprocesses at decode. That widens JSON decode and changes what `GetDefinition` refuses on every dialect.

**D4. A single top-level refusal keeps its exact message.** The path prefix appears only for a nested refusal, and the join only when there are two or more refusals. Every existing scenario that pins a top-level message stays true. That includes this baseline's `A key the node kind never reads is refused` and the messages `refuse-unrecognized-wire-values` adds.

**D5. One access path for the gate and its probe (#218).** The gate reads a field with `FieldByIndex(f.Index)`, exactly as `probeWire` writes it. The field enumeration refuses an anonymous (embedded) field outright. Measured: today every index has length 1, because only top-level fields are walked, so the two access paths agree. The hazard is an embedded struct. With an unexported embedded type, `TestEveryNodeWireFieldHasAWireKey` skips the field (it is not exported), the enumeration skips it (no tag), and `encoding/json` still promotes its keys. Those keys would then be ungated, which is fail-open. That chain is reasoned and stays a hypothesis until the guard test (task 2.1) shows it on a test-only struct. Either way the fix is cheap and fails closed. Rejected: documenting the asymmetry, because the #218 acceptance criteria require fixing a fail-open limit, not documenting it.

**D6. Cycle refusal uses an ancestor set, not a visited set (pending OQ1).** A subprocess definition reached twice without a cycle must encode, as in the spec's negative control. A global visited set, like `Validate`'s `seen`, would refuse it. Encoding passes the ancestor chain down through an internal recursive encoder that builds nested definitions directly, instead of re-entering `json.Marshal` per level. Rejected:
- A pre-walk inside each `MarshalJSON` call. Every nested call would re-walk its subtree, which is quadratic in depth.
- Refusing at construction (#204 option 2). The reproduction builds the cycle by assigning the exported `Nodes` field, so no constructor runs.
- Documenting the limit (#204 option 3). The issue rejects it: an abort is not fail-closed.

**D7. `RuleSpec` walks its own decoded document (pending OQ2).** Before encoding, the YAML arm walks the `yaml.Node` tree and refuses any mapping key below the top level whose resolved tag is not `!!str`. That removes the dependency on `encoding/json` refusing `map[any]any`. Measured on 1.27.1: int keys are accepted, bool, float and null keys are refused, and the messages change (`jsontext: object member name must be a string`). The top-level coercion of `1:` happens in yaml.v3's decode into `map[string]any` and is untouched.

**D8. The `def_ref` refusal is produced by the library (pending OQ3).** Measured cause: `Qualifier.UnmarshalJSON` unmarshals into a bare `string`. Go 1.26 decorates the inner `*json.UnmarshalTypeError` with `StartInput.def_ref`; Go 1.27 does not. Gin is not involved: gin v1.12.0's default codec is `encoding/json`. Recommended mechanism: `StartInput` decodes `def_ref` as raw bytes and reports `def_ref: <reason>`, wrapping `ErrInvalidQualifier`. `Qualifier.UnmarshalJSON` refuses a non-string token itself with `ErrInvalidQualifier` naming the JSON type. All three adapters decode `StartInput` through `encoding/json` and inherit the change. A parity row per adapter pins it.

**D9. #208 guard is an internal test with reflection.** `nodeYAML` is unexported. The precedent is `node_wire_keys_test.go`, an internal test that already reflects over `NodeWire` with `jsonKey`. Three assertions:
- the `Eligible*` set on `NodeWire` is exactly three fields with their JSON tags;
- the same set on `nodeYAML`, with its YAML tags;
- every `NodeWire` JSON key has a `nodeYAML` YAML key and the reverse, except an explicit JSON-only list of `timer_trigger`, `deadline_trigger` and `wait_trigger`, which matches `kindGateCase.yamlSkip`.

The third assertion is the issue's own point: the right assertion over too small a domain reproduces the blind spot.

**D10. #225 reuses `validate.go`'s wording.** The test comment cites `ErrDuplicateFlowID`'s godoc reason: blank IDs are tolerated by construction, because the engine mints a position-derived edge identity. It does not compose a new sentence (the issue's criterion 1).

## Cross-change coupling

- **`refuse-unrecognized-wire-values`: Depends on (landing order).** It adds `ErrInvalidEndBehavior`, `ErrInvalidTriggerKind` and `ErrKeyNotOnEndBehavior` gates in `fromWire`, beside `checkNodeKeys`. D1 must aggregate those too. Landing after it means aggregating gates that exist, not ones that are planned. This also follows the order ruled in the #217 thread: the #219 class, then #217 and #218, then #204 and #208. Both changes ADD requirements to `definition-wire-format` under different names, so only the archive sync rebases.
- **`refuse-case-variant-json-keys`: shared file, no dependency.** It adds an exact-key check in the JSON decode path of `node_wire.go`, which D2 also reshapes. Its refusal is per object, before the node seam, so it is not a node decode refusal. It still aborts the decode on its own, and its message is unchanged by D4. Whichever change lands second rebases `UnmarshalJSON`. If D2 lands first, the case-variant check must also cover the raw `subprocess` bytes. Its spec already requires "a nested subprocess, at any depth", and its tests catch a miss.
- **`export-refusal-sentinels`: shared file, no dependency.** It marks the unknown-key and unknown-kind refusals with sentinels and keeps their text. The `errors.Join` and wrapping in D1 preserve `errors.Is`, so its scenarios hold in either order.
- **`make-definition-registration-idempotent`: named coupling, deliberately not Blocks.** Its design (Risks) and task 2.4 record #204 as a risk. A separately built copy of a cyclic graph would abort inside the shared normalisation, where today it gets `ErrDefinitionExists`. After this change, `Validate` refuses the cycle (D6, spec *A cyclic subprocess graph is refused*) before registration compares anything, and encoding returns `ErrCyclicSubprocess` instead of aborting. The risk then disappears, and its "unencodable stays refused" rule covers any residue. That change is correct without this one, so it is not blocked. Whichever lands second updates two comments that would otherwise go stale: the `Register` godoc note and the `MarshalJSON` comment "Tracked as a follow-up".
- **`bound-untrusted-input`: shared file, no dependency.** It bounds the inline rule and definition document size (#184) and touches `rulespec.go` and `definition-wire-format`. D7's walk is linear in the document it already decoded, so the two compose in either order.

## Risks / Trade-offs

- [Callers that match the text of a nested or multi-refusal decode error break] → D4 keeps the single top-level message. `errors.Is` is the supported contract, and the release note names the change.
- [D2 adds a raw-bytes hop on the definition-store load path (`GetDefinition`, `Lookup`)] → `node_wire_keys_bench_test.go` is extended with a nested case, and the task fails if a clean decode allocates more than before.
- [A cap of 20 hides refusals] → the tail states the count, so the author knows to look again.
- [OQ1 to OQ3 resolve differently from the recommendation] → those requirements are marked pending. The #217, #218, #208 and #225 work does not depend on them and can proceed (task groups 1, 2, 6 and 7).

## Migration Plan

No data migration. Stored rows the library wrote decode identically. A row written out of band that was already refused is now refused with every refusal listed. Rollback is a plain revert: no format, column or dependency changes.

## Open Questions

These need a maintainer decision. The specs carry the recommended option, marked "Pending".

- **OQ1 (#204): where is a cyclic subprocess graph refused?**
  - (a) `MarshalJSON` only, with a new sentinel.
  - (b) `Validate` and `MarshalJSON`, with one sentinel `ErrCyclicSubprocess`. **Recommended.** The authoring gate stops clearing a graph that the next step dies on, and direct `json.Marshal` callers are covered too.
  - (c) Refuse at construction. Rejected in D6 with evidence.

  Sub-question: reuse an existing sentinel instead of adding one. None fits: `ErrForeignNodeType` and `ErrUnserializableValidation` name other defects.
- **OQ2 (#235 half 1): what does the YAML inline rule accept for a nested non-string key?**
  - (a) Refuse every nested non-string key in `RuleSpec` itself. **Recommended.** It keeps today's refused set on go1.26.9, removes the stdlib dependency, and fails closed.
  - (b) Accept 1.27's coercion and pin it. This widens what the library accepts, needs a release note, and still leaves bool, float and null keys refused only because `encoding/json` refuses them.

  Sub-question: should the top-level `1:` coercion, documented as lossy, also become a refusal? Recommended no, because it would be a separate BREAKING tightening.
- **OQ3 (#235 half 2): what should the 400 for a refused `def_ref` say?**
  - (a) Name `def_ref` in a library-owned message wrapping `ErrInvalidQualifier`, the same on every adapter and toolchain. **Recommended.** It also fixes the empty-string case, which already lacks the field name.
  - (b) A library-owned message without the field name.
  - (c) Change nothing in the library, and make the gin test assert status, `ErrBadInput` and the prefix only. Under (c) the requirement is dropped, and only the test task remains.

# Proposal

## Why

The definition wire gates make the right decision and report it badly, and the encode side of the same boundary can kill the process. A misplaced key inside a JSON subprocess does not say which subprocess. The same defective definition fails with different first errors in YAML and JSON. Three misplaced keys cost three fix-and-retry cycles. `model.Validate` accepts a cyclic subprocess graph that `ProcessDefinition.MarshalJSON` then overflows the stack on, and `PublishDefinition` runs those two steps one after the other. Two refusals also depend on `encoding/json` behavior that Go 1.27 changes. Once go.mod moves to 1.27, one rule refusal goes silent and one 400 loses its field name.

Every premise below was re-measured on `9c6dd2c1` with go1.26.9 (and go1.27.1 where stated), using scratch tests compiled in through `-overlay`. Nothing was written to the tree.

## What Changes

- **Decode refusals carry their location, all of them, the same in both formats** (kartaladev/wrkflw#217, absorbed). Measured:
  - C7: JSON names only the node (`node "e" declares kind "endEvent" ... "action"`) at subprocess depth 1 and 2. YAML already names one enclosing subprocess (`subprocess "sub": ...`), so the premise is **half stale**: it holds for JSON only.
  - C8: an outer node with a misplaced key whose inline subprocess has no start event. JSON reports `ErrKeyNotOnKind` for the outer node. YAML reports `subprocess "sub": no start event`, because `ParseYAML` builds and validates the inline subprocess before the outer node's gate runs. The issue's mechanism holds. When both levels carry a misplaced key, both formats agree today, because both descend first.
  - C9: holds. Both formats stop at the first misplaced key, across keys and across nodes.
  - After this change, every node decode refusal in a definition is reported in one error, up to a cap of 20 plus a count of the rest, in document order. Each refusal names its enclosing subprocess path and still matches its sentinel with `errors.Is`. Decode refusals are reported before, and instead of, a nested subprocess's structural validation, in both formats. A single refusal at the top level keeps its exact message today. **BREAKING** only for callers that match the text of a nested or multi-key refusal.
- **The gate and its probe reach a field the same way** (kartaladev/wrkflw#218, absorbed, no spec). The probe sets a field with `FieldByIndex(f.Index)`. The gate reads it with `Field(f.Index[0])`. They agree today only because field enumeration walks top-level fields, so every index has length 1. An embedded struct would break that, and the failure direction is open: its promoted keys would never be gated. That direction is reasoned, not demonstrated, so it stays a **hypothesis** until the guard test shows it. One access path, plus a guard test.
- **A cyclic subprocess graph is refused, never encoded to a stack overflow** (kartaladev/wrkflw#204, absorbed; **pending OQ1**). Reproduced: start → subProcess(itself) → end passes `model.Validate` with nil, and `json.Marshal` then dies with `fatal error: stack overflow`. `PublishDefinition` validates at `internal/persistence/store/definitions.go:198` and marshals at `:202`. Recommended: `MarshalJSON` and `Validate` both refuse with a new `model.ErrCyclicSubprocess`. A subprocess definition shared, without a cycle, by two nodes still encodes. **BREAKING** for `Validate`, which now refuses a graph it accepted. Refusing at construction (the issue's option 2) is rejected with evidence: the reproduction builds the cycle by assigning the exported `Nodes` field, and no constructor sees that.
- **The inline rule codec stops depending on encoding/json to refuse non-string keys** (kartaladev/wrkflw#235 half 1, absorbed; **pending OQ2**). Measured: on go1.26.9 a YAML inline rule with a nested mapping key that is an int, bool, float or null is refused with `ErrInvalidRule`. On go1.27.1 the int key, at any depth and inside a list, is accepted and coerced to `"1"`; bool, float and null keys stay refused, with a different message. Recommended: `RuleSpec` refuses every nested non-string key itself, so both toolchains refuse the same set. A top-level `1:` key keeps yaml.v3's coercion to `"1"`, which is documented as lossy.
- **A non-string `def_ref` names the field on every toolchain and adapter** (kartaladev/wrkflw#235 half 2, absorbed; **pending OQ3**). The cause is now established, and the issue's premise is **stale**. It is not gin's decoder: gin v1.12.0 decodes with `encoding/json` by default (the issue says "gin v3"). The cause is `model.Qualifier.UnmarshalJSON`, which unmarshals into a bare `string`. Go 1.26 adds the struct-field context to the inner error; Go 1.27 does not. `json.Unmarshal` into `httpcore.StartInput` loses `StartInput.def_ref` on 1.27 with no adapter involved. An empty `def_ref` already omits the field name on both toolchains. Recommended: the 400 for any refused `def_ref` names `def_ref`, matches `model.ErrInvalidQualifier`, and reads the same on stdlib, gin and fiber.
- **Eligibility keys are pinned on both wire structs** (kartaladev/wrkflw#208, absorbed, no spec). Holds. The guard died with `internal/atrest` (`TestDefinitionEligibilityFieldsAreTheDeclaredSet`), and nothing in `definition/model` reflects over the `Eligible*` set or compares the JSON and YAML node structs. The guard is re-homed in `definition/model` over both structs, with a JSON↔YAML key correspondence check. Keys that exist only in JSON (the nested trigger objects) are listed explicitly.
- **The blank-flow-ID test states the true reason** (kartaladev/wrkflw#225, absorbed, no spec). Holds. `definition/model/validate_test.go:977` still says "nothing looks a flow up by ID on the execution path". The `ErrDuplicateFlowID` godoc in `validate.go` was corrected, and its wording is reused. The issue's engine citations drifted: `step_timers.go` now resolves through `flowRefByID`, and `step_triggers.go:571` matches on the ID alone. The `-E` sweep finds only this one instance.
- **Persisted-format impact (flagged).** Stored bytes do not change and no migration ships. Every definition-store load (`GetDefinition`, `Lookup`, the republish comparison) decodes through the reporting path, on Postgres, MySQL and SQLite alike. A refused row now reports all of its refusals, not just the first. A cyclic graph never reached storage, because publishing it aborted the process.
- **Default security posture:** tightened. A process abort becomes a named error. The aggregated report is capped, so a hostile document cannot grow the error past 20 entries. **go.mod:** no change.

**Out of scope**
- Reporting every unknown key in JSON. `encoding/json` stops at the first unknown key, while YAML lists them up to 20. Recorded as a finding; no change owns it.
- The new value gates (`ErrInvalidEndBehavior`, `ErrInvalidTriggerKind`, `ErrKeyNotOnEndBehavior`) belong to `refuse-unrecognized-wire-values`. This change reports them through the same aggregation once they exist.
- Case-variant JSON keys belong to `refuse-case-variant-json-keys`. Sentinels for unknown keys and unknown kinds belong to `export-refusal-sentinels`.
- Size, depth and key-count ceilings on a definition or inline rule document belong to `bound-untrusted-input`.
- Identical in-memory re-registration belongs to `make-definition-registration-idempotent`. The coupling is named in design, Cross-change coupling.
- Running CI on the next Go toolchain (CI guard work). This change makes the suite pass on 1.27 but adds no job.
- The other doc-rot items belong to `fix-doc-rot` and `align-godoc-with-behavior`.

**Decisions this change cannot be fully specified without** (design, Open Questions; Readiness: human)
- OQ1 (#204): where a cycle is refused, and under which sentinel. The specs carry the recommended option, marked pending.
- OQ2 (#235 half 1): refuse nested non-string rule keys (recommended), or accept 1.27's coercion and pin it. The specs carry the recommended option, marked pending.
- OQ3 (#235 half 2): does a refused `def_ref` name the field (recommended), use a library-owned message without the field, or only stop the test pinning stdlib text? The specs carry the recommended option, marked pending.

**Coordination**
- Size: L
- Depends on: refuse-unrecognized-wire-values
- Blocks: `back-business-rule-task-with-rlng`
- Readiness: human

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `definition-wire-format`: ADDED *A decode refusal names its enclosing subprocess path*, *Every node decode refusal is reported in one pass*, *JSON and YAML report the same decode refusals*, *Encoding refuses a cyclic subprocess graph* (pending OQ1), and *An inline rule document refuses non-string keys below its top level* (pending OQ2).
- `definition-validation`: ADDED *A cyclic subprocess graph is refused* (pending OQ1).
- `http-transport`: ADDED *A refused def_ref names the field on every adapter* (pending OQ3).

## Impact

- **Code:**
  - `definition/model`: the reporting path in `node_wire.go` (`fromWire`, `UnmarshalJSON`), `node_wire_keys.go` (`checkNodeKeys`, `refusalsFor`), and `yaml.go` (`fromNodeYAML`, `coreFromYAML`, with gates run before the inline subprocess is built).
  - `definition/model`: `MarshalJSON` and `validate.go` get the cycle refusal and a new sentinel. `rulespec.go` gets the key walk.
  - `model.Qualifier.UnmarshalJSON` and/or `httpcore.StartInput` for `def_ref`.
- **Tests:**
  - `rulespec_test.go` and `transport/http/gin/gin_bodycap_test.go` stop pinning stdlib text.
  - New guard tests for #208 and #218.
  - The comment fix in `validate_test.go`.
- **Consumers:** a caller matching the text of a nested or multi-key decode refusal sees the new text. `errors.Is` keeps working for every sentinel. A caller that validates a cyclic graph gets an error where it got nil. Nobody could encode one before, so no stored data depends on it.

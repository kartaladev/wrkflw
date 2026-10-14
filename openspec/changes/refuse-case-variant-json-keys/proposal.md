# Proposal

## Why

A JSON definition may carry a key that differs from a defined key only in letter case, and it decodes without error. `"ID"`, `"Version"`, `"Name"`, `"KIND"`, `"Action"`, `"Target"` and `"Max_Attempts"` all bind to the defined key. So do `"Kind"` (KELVIN SIGN) and `"ſignal_name"` (LATIN SMALL LETTER LONG S), which do not look like case variants at all. The same document authored in YAML is refused. A definition's meaning therefore depends on the format it was written in. The project rule is that an out-of-vocabulary value is refused with a named error and never decays silently, and this breaks it.

The cause is encoding/json. It matches object keys to struct fields case-insensitively, and `DisallowUnknownFields` uses the same folded comparison. Neither can see a fold-equal key. The worst case is shadowing: `{"id":"d","ID":"z"}` decodes to the ID `z`, and nothing reports the duplicate.

The baseline found this (archived change `establish-baseline-specs`, design D7 group 1) and recorded it as a defect. It was re-measured on main with go1.26.9: every key above decodes with a nil error at the definition, node, flow and nested-subprocess levels, and inside `retry_policy`, `timer_trigger` and `validation`. No open issue covers it.

## What Changes

- **BREAKING** Decoding a JSON definition with `ProcessDefinition.UnmarshalJSON`, and so with `json.Unmarshal` into a `ProcessDefinition`, refuses any object key that is not byte-for-byte a key the wire format defines at that position but that encoding/json would bind to one. The refusal uses a new exported sentinel, `model.ErrCaseVariantKey`, and the error names the authored key and the defined key it folds onto. This covers every level of the wire format:
  - the definition, its nodes and its flows;
  - a nested subprocess, at any depth;
  - the nested objects a node carries (`retry_policy`, the three trigger objects and their `at_times` entries, and `validation`).
- JSON and YAML then agree: both refuse any key that is not exactly a defined key. YAML is unchanged and keeps its current error.
- The definition-wire-format requirement *Unknown keys are refused at decode* is MODIFIED. Its JSON clause stops excluding case variants.
- An inline rule document (`rule` given as an object) stays opaque. Its keys belong to the rule engine, not to the wire format, and are not checked.
- **Persisted-format impact (flagged).** The stored bytes do not change, and no migration or schema change ships. The reader gets stricter, though, and every definition-store load decodes through it: `GetDefinition`, `Lookup`, and the content comparison on a republish. Rows the library wrote are unaffected, because `MarshalJSON` writes only exact keys. A row written out of band (direct SQL, a data migration, a hand edit) that holds a case-variant key will stop loading after the upgrade. Its in-flight instances are then stranded until the row is repaired. Design D3 gives the compatibility answer: no lenient mode, plus an operator repair recipe.
- **Default security posture:** tightened, not loosened. A shadowing key can no longer silently override the exact key. **go.mod:** no change for consumers. The fix uses only encoding/json.

**Out of scope**
- An exact duplicate key (`{"id":"a","id":"b"}`). JSON accepts it, last value wins, while YAML refuses it. This was measured during planning and is reported as a finding; no change or issue owns it yet.
- Recursive exact-key checking for typed action input under `WithStrictInput`. The change `harden-typed-action-input` owns it (kartaladev/wrkflw#142).
- Diagnostic quality of decode refusals: the nesting path, error order across formats, and reporting one key at a time. kartaladev/wrkflw#217 owns it (pending migration). This change does not add a nesting path.
- Exporting a sentinel for YAML's unknown-key refusal, or for JSON's plain unknown-key refusal.
- Moving to `encoding/json/v2`, which is rejected in design D2.

**Decisions taken before specs**
- The refusal is a new exported sentinel, `ErrCaseVariantKey`, not a reuse of encoding/json's unknown-field error. A caller can then tell "you mis-cased a defined key" apart from "this key does not exist".
- No lenient or compatibility decode option ships. Design D3 records why and gives the operator repair recipe; a human confirms it before work starts (Readiness).
- YAML keeps its current refusal and does not wrap the new sentinel. This is listed as an open question in design.

**Coordination**
- Size: M.
- Depends on: `establish-baseline-specs`.
- Blocks: nothing.
- Readiness: human.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `definition-wire-format`: the requirement *Unknown keys are refused at decode* changes. A JSON key that only case-folds onto a defined key is refused with `ErrCaseVariantKey` at every level, where until now it was explicitly excluded.

## Impact

- **Code:** `definition/model`, meaning the JSON decode path in `node_wire.go` and a new exact-key check beside the existing decode gates; plus the new `ErrCaseVariantKey` sentinel. `definition/flow`, `definition/schedule` and `definition/model/validate` are read for their JSON tags but need no change.
- **Persistence:** `internal/persistence/store/definitions.go` is unchanged, but the stricter decode now applies to its loads (see the persisted-format flag above). This holds identically for Postgres, MySQL and SQLite, because all three decode stored bytes through the same `json.Unmarshal`.
- **Performance:** every `GetDefinition` and `Lookup` decodes a definition, so the check sits on a read hot path. Design D4 bounds its cost, and a task benchmarks it.
- **Consumers:** any hand-authored or generated JSON definition that uses a non-canonical key casing now fails to decode and must be corrected. Library-encoded JSON and every YAML definition are unaffected.

# Proposal

## Why

`action.Typed` still has four loose ends, all filed from the review of PR #135. Each was re-measured on `main` (9c6dd2c1, go1.26.9) with a scratch probe that imports the library:

| Issue | Premise | Re-measured on main |
|---|---|---|
| kartaladev/wrkflw#142 | Under `WithStrictInput`, a fold-equal key inside a nested object is not refused. | **Holds, and is wider than filed.** A case variant (`NAME`), a U+017F long-s variant (`ſKey` onto `sKey`) and a shadowing twin all bind silently. That holds in a nested struct, behind a pointer, in a slice element and in a map value. A twin that sorts after the exact key wins (pinned by the "documented limit" row in `action/typed_test.go`). This is a silent outcome, which the project rule forbids. |
| kartaladev/wrkflw#144 | `rejectUnknownKeys` names every unknown key. "41 bytes to roughly 460 KB for 20,000 unknown keys." | **Unbounded count holds. The amplification figure is stale.** With 20,000 keys `k0`…`k19999`, the message is 188,976 bytes against a 208,891-byte JSON input, so it is smaller than its input. The code comment's closed form still holds: about 85 bytes plus `len(strconv.Quote(key))+2` per key, so at most about 4x the key bytes when every byte needs escaping. The 41-byte input could not be reproduced and stays a hypothesis. The message does reach durable sinks: `ActionFailed.Err`, `_errorMessage`, `Incident.Error`, and the `instance.failed` outbox payload through `terminalEventErr`. |
| kartaladev/wrkflw#143 | Strict requires the Go-cased key (`Untagged`) for an untagged field, and this is undocumented. | **Holds.** Strict refuses `untagged` with `ErrDecodeInput`, while lenient binds it. Neither the `WithStrictInput` godoc nor the spec says so. A test pins only the accepting row. |
| kartaladev/wrkflw#137 | In lenient mode `In` is unvalidated. A bad `In` "fails closed … on every invocation". | **The first half holds. The fail-closed half is stale.** `Typed` accepts an `In` of `string`, `int`, `[]any`, `chan int` or `**T`. For a non-empty input, the scalar, slice and channel shapes fail with `ErrDecodeInput`. A **nil** input marshals to `null` and decodes to the zero `In` with no error, so the function runs on a zero value: that is fail-open. The engine passes a nil map wherever it clones nil instance variables (`copyVars` is `maps.Clone`). Whether such an instance reaches a typed action is unmeasured and stays a hypothesis. |

## What Changes

- **Strict nested exact keys (kartaladev/wrkflw#142, absorbed; pending Q1).** Under `WithStrictInput`, a key at any depth that is not byte-for-byte a JSON name the destination struct declares is refused with `action.ErrDecodeInput` (non-retryable). Today that check covers the top-level object only. It applies wherever encoding/json would decode a struct: a struct field, a pointer, a slice or array element, and a map value. Positions that own their own decoding stay opaque: `any`, a map of `any`, `json.RawMessage`, and a type with its own `UnmarshalJSON`. **BREAKING** for a strict consumer whose payload relies on a nested key binding by case-folding.
- **Cap the named keys (kartaladev/wrkflw#144, absorbed with its figure corrected; pending Q3).** A strict refusal names at most ten offending keys, the first ten in byte order, and then states how many more there are. The text stays deterministic for the same input.
- **Document and pin the Go-cased rule (kartaladev/wrkflw#143, absorbed).** The `WithStrictInput` godoc states that an untagged field's JSON name is its Go field name, matched exactly. The spec gains the refusing row and the accepting row.
- **Constrain lenient `In` at construction (kartaladev/wrkflw#137, absorbed with its fail-closed premise found stale; pending Q2).** `action.Typed` panics when `In` cannot hold a JSON object: anything other than a struct, a map or an interface with no methods, optionally behind a single pointer. **BREAKING** for code that registers such a shape. Code that registers `**T`, which decodes today, also breaks.
- **Persisted formats:** no schema, column, snapshot or outbox shape change. The *text* recorded for new strict refusals changes (capped, with nested keys). Rows already written keep their old text. No promise covers the message text.
- **Default security posture:** tightened. The default is lenient and is unchanged in what it decodes. Strict closes the nested shadowing twin. The construction check removes a fail-open path on a nil input.
- **Consumer go.mod:** no change. `encoding/json/v2` is not adopted (Q1). On go1.26.9 it builds only with `GOEXPERIMENT=jsonv2`, re-measured with `go doc encoding/json/v2`.

**Decisions this change cannot be specified without (Open Questions in design; none is recorded in any issue thread, and every thread has zero comments):**
- **Q1 (kartaladev/wrkflw#142).** Implement recursive exact-key checking now, or wait for `encoding/json/v2`? Recommended: now. The specs carry the recommended option.
- **Q2 (kartaladev/wrkflw#137).** Should lenient constrain `In` at all? If so, at construction or at invocation, and which shapes? Recommended: at construction, mirroring `Out`'s one-pointer rule. The specs carry the recommended option.
- **Q3 (kartaladev/wrkflw#144).** Cap or not, and the cap's value. Recommended: ten. The specs carry the recommended option.

The requirements that depend on Q1 to Q3 are marked *pending* in design. The gate task rewrites them if the maintainer chooses otherwise.

**Out of scope**
- Making the persisted decode text value-free, with the raw error kept in `Cause`: kartaladev/wrkflw#138, owned by `bound-untrusted-input`.
- Size, key-count and depth ceilings on process variables, and so on the length of any single key named here: kartaladev/wrkflw#140, owned by `bound-untrusted-input`.
- Fold-variant engine-stamped keys reaching cancel, completion and compensation action input: kartaladev/wrkflw#187, owned by `reserve-engine-variable-keys`.
- Case-variant keys in definition JSON: the active change `refuse-case-variant-json-keys`. Its proposal names kartaladev/wrkflw#142 as the owner of typed input; that pointer now resolves to this change.
- Lenient mode's fold binding of a top-level or nested key. It stays as documented; strict is the opt-in.

**Coordination**
- Size: M.
- Depends on: `establish-baseline-specs`.
- Blocks: nothing.
- Readiness: human.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `typed-action-input`: the top-level strict requirement gains the untagged-field rows and defers naming to the cap. The construction requirement gains lenient `In` shapes. New requirements cover nested exact keys and the named-key cap.

## Impact

- **Code:** `action/typed.go` gets a schema built at construction, a strict key check at every depth, the cap, the lenient `In` shape check, and godoc for `WithStrictInput` and `Typed`. `action/typed_test.go`: the "documented limit" row is inverted.
- **API:** no new exported identifier. Refusals stay `action.ErrDecodeInput`, and construction refusals stay panics.
- **Performance:** a strict invocation now walks the whole input against the schema. A benchmark task bounds the cost.
- **Coupling:** `bound-untrusted-input` (kartaladev/wrkflw#138) edits the same refusal message's path to persistence. `reserve-engine-variable-keys` edits the `Typed` godoc paragraph on unfiltered action input. Neither has to land first. See design, Coupling.

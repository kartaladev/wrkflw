# Proposal

## Why

Four inputs a caller controls have no ceiling, and one error path records the caller's values in durable rows. Each issue's premise was re-measured on `main` (`9c6dd2c1`, go1.26.9) with a scratch probe that imports the library:

| Issue | Premise | Re-measured on main |
|---|---|---|
| kartaladev/wrkflw#184 | The inline rule document has no size, depth or key-count ceiling: 64 MB of input allocates about 320 MB. The inline rule is "new in kind", because it is the first arbitrary-shape sub-document. | **The figure holds. The "new in kind" premise is stale.** A 64 MiB inline rule allocates 320.0 MiB during JSON decode (5.00x), and so does a 64 MiB node *name*: 320.0 MiB, 5.00x. The ratio is the same at 1 and 16 MiB. YAML is worse: a 16 MiB inline rule through `ParseYAML` allocates 212.5 MiB (13.3x), and `ParseYAML` reads its whole reader with `io.ReadAll` (`definition/model/yaml.go:226`). JSON nesting is already capped by encoding/json at depth 10000; depth 9000 decodes. So the missing ceiling is on the whole definition document, not on the rule key. No library route accepts a definition from an HTTP caller, so the exposed party is a consumer that decodes definitions from untrusted bytes. |
| kartaladev/wrkflw#140 | Process variables have no size, key-count or depth validation (`engine/step_state.go:331-339`). | **Holds, and the line has drifted.** `mergeVars` is now `engine/step_state.go:347-356`, an unconditional `maps.Copy`. `engine.Step` accepted a start payload of 200,002 keys, nesting depth 5000 and 36.8 MB encoded with no error. The HTTP adapters do cap one request body at 1 MiB by default (`http-transport` spec), but that bounds one request, not the instance: two signals of just under 1 MiB each left 2.1 MB of encoded variables. Measured at the codec only: a map nested 20,000 deep encodes with `json.Marshal` and fails `json.Unmarshal` ("exceeded max depth"). The SQL stores decode snapshots with `json.Unmarshal`, so an instance carrying such a variable, supplied through the Go API, would commit and then fail to load. That end-to-end claim is a **hypothesis** until a store test reproduces it (task 4.1). |
| kartaladev/wrkflw#139 | `MaxAttempts == 0` means unlimited, and `Normalize` does not fill it (`definition/model/retry.go:88-95`). It is reachable from any partly filled literal. | **Holds, and the lines have drifted.** The comment is at `definition/model/retry.go:87-88` and the guard at `:92-93`. The engine treats zero as "no attempt bound" (`engine/step_triggers.go:465`, `engine/step_compensation.go:602`). The value is also reachable from the wire: `max_attempts` is `omitempty`, so a `retry_policy` that omits it, in JSON or YAML, decodes to zero and retries without limit. `action.RetrySpecs` and `WithCompensationRetryPolicy` document the same convention. |
| kartaladev/wrkflw#138 | Decode-error text reaches four durable sinks, an outbox topic and the public read model. String values never reach it; numeric literals do in narrow cases; key names do by design. | **Holds.** Re-measured over `action.Typed`: a string, bool, array or object value never appears. Numeric literals do: `1.5` and `123456789.25` into an `int`, `300` into an `int8`, `-7` into a `uint`, `1e+300` into a `float32`, and `9.5` in a nested field. The text also carries the consumer's Go type names (`In.amount`, and a struct literal with its tags). The sinks are confirmed: `ActionFailed.Err` (`runtime/processdriver_action.go:413`, persisted by the trigger journal), `Incident.Error` (snapshot, and `service` read model `error`), the `_errorMessage` variable, and the `instance.failed` outbox payload. `ActionFailed.Cause` already exists and is in memory only (`json:"-"`). |

## What Changes

- **Value-free decode text (kartaladev/wrkflw#138, absorbed).** When `action.Typed` cannot decode its input, the error text names the offending key but contains no input value: no numeric literal and no consumer Go type name. That text is what the runtime records as the action failure and what reaches every durable sink. The decoder's own error stays reachable in memory with `errors.As` on the failure's cause, so an error boundary's Go check still sees it. The error still matches `action.ErrDecodeInput` and stays non-retryable. Decision-independent; not pending.
- **Bounded retry attempts (kartaladev/wrkflw#139, absorbed; pending Q1).** Recommended: `RetryPolicy.Normalize` fills a zero `MaxAttempts` with the default of 3, and unlimited attempts must be spelled explicitly as a new `model.UnlimitedAttempts` value (`max_attempts: -1` on the wire). Every tier normalizes, so the node, action, runtime-default and compensation policies all change together. **BREAKING.**
- **Ceilings on process variables (kartaladev/wrkflw#140, absorbed; pending Q2).** Recommended: the `ProcessDriver` refuses a caller-supplied variables map that is over a configurable encoded-size or nesting-depth limit, and refuses a trigger whose merge would grow the instance's variables past a configurable total. This covers start variables, signal and message payloads, and human-task completion output. The refusal leaves the instance unchanged, and the defaults are finite, so a consumer is safe without configuring anything. **BREAKING** for a caller that sends payloads above the defaults.
- **Ceiling on a definition document (kartaladev/wrkflw#184, absorbed with its "new in kind" premise found stale; pending Q3).** Recommended: a byte ceiling on the whole definition document, enforced where a definition is authored and decoded (`ParseYAML` and the JSON decode of a `ProcessDefinition`) and refused before the decode allocates. It is not a ceiling on the rule key alone. Stored definitions reloaded by a definition store are not re-checked, so no stored row can be stranded. **BREAKING** for a definition above the default.
- **Persisted formats (flagged).** No schema, column or migration changes. Three changes are semantic:
  - #138: the *text* recorded for a new decode failure changes. Rows already written keep their old text. A definition that branches on the old `_errorMessage` wording of a decode failure would stop matching.
  - #139 under the recommended option: a stored definition whose `retry_policy` omits `max_attempts` changes meaning on upgrade, from unlimited attempts to 3. A token already past its third attempt fails into an incident at its next failure. Option C in design avoids this cost.
  - #140: an instance whose stored variables already exceed the new total is not refused on load. Only a trigger that would grow it further is refused.
- **Default security posture (flagged): tightened.** Today an unconfigured deployment retries without limit on an omitted `max_attempts`, accepts unbounded variables through the Go API, and lets numeric input values reach outbox subscribers and the read model. After this change all three are bounded by default.
- **Consumer go.mod:** no change. No new dependency.

**Decisions this change cannot be specified without (Open Questions in design; no issue thread records a maintainer decision, and all four threads have zero comments):**
- **Q1 (kartaladev/wrkflw#139).** Should a zero `MaxAttempts` keep meaning unlimited? Recommended: no. Normalize fills it, and unlimited is spelled explicitly. The specs carry the recommended option.
- **Q2 (kartaladev/wrkflw#140).** Where does the bound live, which dimensions does it cover, and what are the defaults? Recommended: in the `ProcessDriver` at ingress, as encoded size and depth per payload plus a total, with defaults 1 MiB, depth 64 and 8 MiB. Over-limit HTTP requests answer 413. The specs carry the recommended option and do not state the default numbers.
- **Q3 (kartaladev/wrkflw#184).** Does the bound apply per field, per document or at the decode boundary, and is it fixed or configurable? Recommended: per document, at the authoring decode boundary, with a 4 MiB default that `ParseYAML` can configure. The specs carry the recommended option and do not state the default number.

The requirements that depend on Q1 to Q3 are marked *pending* in design. Gate task 1.1 rewrites them if the maintainer chooses otherwise.

**Out of scope**
- Capping how many unknown keys a strict refusal names (kartaladev/wrkflw#144), and every other strict-decode change: owned by `harden-typed-action-input`. This change governs what the refusal text may contain. That change governs how many keys it names.
- A caller writing or overwriting engine-stamped variables such as `_errorMessage` through a payload (kartaladev/wrkflw#187): owned by `reserve-engine-variable-keys`. This change bounds payload size and depth, not key names.
- A ceiling on action *output*. A consumer's own action is trusted code, and `action/httpcall` already caps response bodies (`ErrBodyTooLarge`). The total-variables ceiling of Q2 still counts output toward the total; see design D3.
- The text of an action error other than `ErrDecodeInput`. A consumer's own error message is the consumer's to redact.
- Definition key casing and decode diagnostics: `refuse-case-variant-json-keys` and `improve-wire-gate-diagnostics` (kartaladev/wrkflw#217).
- Size and storability of the definition body per SQL dialect (kartaladev/wrkflw#154): owned by `harden-definition-store-parity`.
- The fiber adapter's own 4 MiB `BodyLimit` divergence. It is already specified in `http-transport` and does not change.

**Coordination**
- Size: L.
- Depends on: `establish-baseline-specs`.
- Blocks: nothing.
- Readiness: human.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `typed-action-input`: a new requirement makes the recorded decode-failure text value-free while keeping the decoder's error reachable in memory (#138).
- `retry-policy`: *The default retry policy carries Temporal-style values* and *Validate refuses a retry policy outside its documented ranges* change for an explicit unlimited value and a filled zero. A new requirement pins that a policy which omits `MaxAttempts` is bounded (#139; pending Q1).
- `process-variables`: new requirements set per-payload and total ceilings on caller-supplied variables (#140; pending Q2).
- `http-transport`: a new requirement maps the variables-ceiling refusal to 413 identically on all three adapters (#140; pending Q2).
- `definition-wire-format`: a new requirement sets a whole-document byte ceiling at the authoring decode boundary (#184; pending Q3).

## Impact

- **Code:** `action/typed.go` (decode error construction); `definition/model/retry.go`, `definition/model/validate.go` and `action/policy.go` (godoc), plus the engine's attempt test in `engine/step_triggers.go` and `engine/step_compensation.go`; `runtime` ingress for start, signal, message and task completion, plus new driver options; `definition/model/yaml.go` and `definition/model/node_wire.go` (document ceiling); `transport/http/httpcore` error classification.
- **API (recommended options):** new sentinels `runtime.ErrVariablesTooLarge`, `runtime.ErrVariablesTooDeep` and `model.ErrDefinitionTooLarge`. New constant `model.UnlimitedAttempts`. New driver options for the variables limits, and a `LoaderOption` for the definition limit. No exported identifier is removed.
- **Performance:** a size and depth walk on each ingress payload, and an encoded-size check of the merged variables. A benchmark task bounds both.
- **Coupling:** `harden-typed-action-input` edits the same refusal message in `action/typed.go`. `reserve-engine-variable-keys` edits the ingress paths this change guards. `refuse-case-variant-json-keys` edits `ProcessDefinition.UnmarshalJSON`. None has to land first. See design, Coupling.

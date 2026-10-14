# Design

## Context

See proposal.md, Why, for the four premises and how each re-measured. The constraints that shape the approach:

- The trust posture decided on kartaladev/wrkflw#21 and applied by #43 is that wrkflw authenticates nobody. Any caller who reaches the instance and message routes can start, signal and message any instance. A ceiling therefore has to be safe without configuration.
- wrkflw is a library. Consumers reach the engine through `service`, through the `ProcessDriver`, and directly through `engine.Step`. Only the HTTP adapters bound a request today, through `WithMaxBodyBytes` (1 MiB by default).
- `engine.Step` is pure, and its tests drive it without a runtime. It holds no configuration for limits. Every variables merge goes through `mergeVars` (`engine/step_state.go:347`).
- The SQL stores decode snapshots with `json.Unmarshal` (`internal/persistence/store/store_core.go:175`). Its nesting limit is 10000. `json.Marshal` has none: a 20,000-deep map encodes, then fails to decode (measured).
- `ActionFailed.Cause` is already in memory only (`json:"-"`, `engine/trigger.go:176`), and an error boundary's `ErrorCheck` closure receives it (`engine/step_errors.go:42`).

## Goals / Non-Goals

**Goals:**
- Every input a caller controls is bounded by a default, and each refusal has a named sentinel.
- No refusal can strand data that is already stored.
- The four sinks of an action failure's text never carry an input value.

**Non-Goals:**
- Bounding `engine.Step` itself. Direct callers of the pure engine own their inputs, and the engine stays configuration-free. Its godoc says so.
- Redacting a consumer action's own error text.
- A key-count dimension. A byte ceiling already bounds key count, because each key costs at least 4 encoded bytes (`"":`).

## Decisions

### D1. Value-free decode text, in the action package (kartaladev/wrkflw#138; not pending)

`action.Typed` returns an unexported error type. Its `Error()` is built from the decoder's error with the value and the Go type removed: the JSON key path, the JSON kind received (`number`, `string`, ...) and the Go kind expected (`int`). Its `Unwrap() []error` returns `ErrDecodeInput` and the decoder's raw error. The runtime is unchanged. It already records `err.Error()` as `ActionFailed.Err` and keeps `err` as `Cause` (`runtime/processdriver_action.go:413`), so the persisted text is value-free and the raw error stays reachable in memory through `errors.As`.

- **Rejected: split in the runtime**, for example a second `ActionFailed` text field. The runtime cannot know which part of an arbitrary action error is a value. Only the decoder knows its own error's structure, and its fields (`*json.UnmarshalTypeError.Value`, `.Field`, `.Type`) are typed.
- **Rejected: redacting with a regular expression over the text.** The probe showed four message shapes (number with literal, number without, string, nested struct with an anonymous type literal), so a pattern would drift with the Go version.
- **Evidence:** the probe in proposal.md, Why. Only `*json.UnmarshalTypeError` carries a value (`Value: "number 1.5"`). The strict unknown-key text already names keys only, quoted with `strconv.Quote`, and keeps them.
- `harden-typed-action-input` changes how *many* keys the strict text names. This decision governs *what* any decode text may contain, so the two compose.

### D2. Zero MaxAttempts is filled by Normalize; unlimited is explicit (kartaladev/wrkflw#139; pending Q1)

`model.UnlimitedAttempts = -1`. `Normalize` maps 0, and every negative value except -1, to the default of 3. `Validate` refuses values below -1, where today it refuses everything below 0. The engine's terminal test changes from `MaxAttempts != 0` to `MaxAttempts > 0` (`engine/step_triggers.go:465`, `engine/step_compensation.go:602`). Every tier passes through `Normalize` (`effectiveRetryPolicy`, `engine/step_state.go:325-342`), so the node, action, runtime-default and compensation policies change together. `action.RetrySpecs` godoc points at the constant.

- **Option A, rejected: keep it and document it.** The footgun stays reachable from a partial Go literal and from any `retry_policy` that omits `max_attempts`, and a zero value decaying to "no limit" is a fail-open default.
- **Option C, the strong alternative:** keep 0 as unlimited on the wire, but refuse a policy with neither bound (MaxAttempts 0 and MaxElapsed 0) at `Validate`, at `WithDefaultRetryPolicy` and at `WithCompensationRetryPolicy`. It changes no stored definition's meaning. Its cost is that the action tier (`RetrySpecs`, including `RetriableAction`, which is resolved per failure) has no construction point where it can refuse, so that path would need a runtime refusal with its own outcome. Only Option C avoids the persisted-semantics flag.
- **Evidence:** `max_attempts` is `omitempty` (`definition/model/retry.go:17`), so absent and zero are indistinguishable on the wire. Store loads do not run `Validate` (`internal/persistence/store/definitions.go` has no call), so under B a stored definition is reinterpreted, never refused.

### D3. Variables ceilings live in the ProcessDriver at ingress (kartaladev/wrkflw#140; pending Q2)

The driver checks before it calls `engine.Step`:
- **Per payload:** the JSON-encoded length and the nesting depth of the caller's map, on `StartInstance`, signal and message delivery, and human-task completion. A refusal returns `runtime.ErrVariablesTooLarge` or `runtime.ErrVariablesTooDeep`, and nothing is stepped or committed.
- **Total:** after the step, inside the same load-step-commit attempt, the encoded length of the resulting variables is compared with the total limit. When the trigger is caller-supplied, the attempt is abandoned with `ErrVariablesTooLarge`. When it is an action completion, the driver re-steps with a non-retryable `ActionFailed` whose cause matches the sentinel, so the token follows the ordinary failure path (recovery flow or incident) and the output is never silently dropped. The check runs on the state being committed, so the store's optimistic concurrency serializes two concurrent deliveries and the second one sees the first one's merge.
- The total check refuses growth only. An instance stored above a lowered total still loads and advances when a trigger does not grow it.
- **Recommended defaults:** 1 MiB per payload, which matches the HTTP body cap, so an HTTP caller cannot pass it while a Go caller can; depth 64; total 8 MiB. Zero or less disables a dimension, the same convention as `WithMaxBodyBytes`. Task 4.6 measures snapshot commit cost at 8 MiB on each dialect before the number is fixed.
- **HTTP:** both sentinels classify as 413, code `variables_too_large`, with a fixed message, in `httpcore` (one classifier, so the three adapters agree).
- **Rejected: in the engine.** The engine has no options for limits, and it would also bound trusted direct callers.
- **Rejected: in the stores.** Column limits differ by dialect, which breaks the parity contract, and a refusal at commit arrives after the step has already run.
- **Rejected: HTTP only (status quo).** Measured: two signals each under 1 MiB accumulated 2.1 MB, and the Go API is unbounded.

### D4. A whole-document byte ceiling at the authoring decode boundary (kartaladev/wrkflw#184; pending Q3)

`ParseYAML` reads through `io.LimitReader(r, limit+1)` and refuses with `model.ErrDefinitionTooLarge` when it receives more than `limit` bytes. A new `LoaderOption` configures the limit. `ProcessDefinition.UnmarshalJSON` refuses `len(data) > limit` before decoding and uses the default, because `json.Unmarshal` gives it no options. The definition stores decode stored rows through an internal path that skips the ceiling, so a stored row is never stranded. Recommended default: 4 MiB.

- **Rejected: per field (the rule key only).** A 64 MiB node name allocates the same 320 MiB as a 64 MiB inline rule (5.00x each, measured), so bounding the rule alone leaves the cheaper attack open. The issue warned about exactly this for #140.
- **Rejected: depth or key-count dimensions for definitions.** The allocation is linear in bytes (5.0x for JSON, 13.3x for YAML, measured at 1, 16 and 64 MiB). JSON depth is already capped at 10000 by encoding/json. YAML depth was not measured and stays a hypothesis.
- **Rejected: documentation only** ("bound your reader"). No library HTTP route accepts a definition, so the exposure is limited to consumers who build one. That argues for a generous default, not for none: `ParseYAML` takes an `io.Reader` and reads it to the end.

## Coupling

- **`harden-typed-action-input`** owns the strict refusal's content and the cap on named keys (kartaladev/wrkflw#144) in `action/typed.go`. This change owns the value-free text (D1) in the same file. There is no landing order; whichever lands second rebases its message assertions. Its Q3 option (c), "no cap, leaving the bound to #140", would rely on D3's per-payload ceiling.
- **`reserve-engine-variable-keys`** (kartaladev/wrkflw#187) edits the same ingress paths (start, signal, message, completion) to reserve engine-stamped keys. Both changes add checks before `engine.Step`. If both land, the order of the checks is fixed in whichever lands second: the reservation first, then the size and depth checks.
- **`refuse-case-variant-json-keys`** edits `ProcessDefinition.UnmarshalJSON` (`definition/model/node_wire.go:283`). D4 adds a length check at the top of the same method. This is a textual merge only.
- **`harden-definition-store-parity`** (kartaladev/wrkflw#154) owns the storable domain of the definition body per dialect. D4's store-load exemption must not be read as a promise about dialect limits.
- **`align-godoc-with-behavior`** rewrites the `MaxInterval` godoc in `definition/model/retry.go` and `action/policy.go`. D2 rewrites the neighbouring `MaxAttempts` godoc in both files. This is a textual merge only.

## Risks / Trade-offs

- [D2 under Option B reinterprets stored definitions] → Flagged in the proposal. The release note names the recipe: publish a new version with `max_attempts: -1` where unlimited was intended. A token past its third attempt fails into an incident, which an operator can resolve with `add_attempts`.
- [D3's total check costs one encode per committing step] → The step already encodes the snapshot. Task 4.6 benchmarks the check and reuses the encoded bytes if the cost is visible.
- [D3's defaults refuse a legitimate large payload] → Loud and named. Each dimension is configurable or can be disabled.
- [D1 makes the persisted text less specific for debugging] → The key path and the kinds stay. The full error is on the in-memory cause, in the action span (`aspan.RecordError` receives the same error, whose text is now value-free), and reachable with `errors.As` in a consumer's `ErrorCheck`.
- [D4's JSON limit is not configurable] → A consumer that needs a larger JSON definition can decode with `ParseYAML`'s configured limit, because YAML is a superset of JSON. If that proves awkward, a `DecodeJSON` helper with options is additive.

## Migration Plan

No schema or data migration. Persisted rows keep the text they were written with. Under D2 (Option B), consumers that relied on an omitted `max_attempts` meaning unlimited publish a new version with `max_attempts: -1`. Under D3 and D4, a consumer above a default configures the limit. Rollback is a revert. Nothing written under the new code is unreadable by the old code: the new code writes no new value except `max_attempts: -1`, and the old code reads that as "treated as unset" (3) and its `Validate` refuses it on re-registration. That rollback hazard is noted in the release note.

## Open Questions

None of these is answered in an issue thread: kartaladev/wrkflw#138, #139, #140 and #184 have no comments. The specs carry the recommended option for each, marked pending here. Gate task 1.1 rewrites the affected requirements if the maintainer chooses otherwise.

- **Q1 (kartaladev/wrkflw#139). Should a zero `MaxAttempts` keep meaning unlimited?**
  - Options: (A) keep it and document it; (B) Normalize fills it, and unlimited is `model.UnlimitedAttempts` (D2); (C) keep the wire meaning and refuse an unbounded policy at construction and validation.
  - Recommendation: (B), with (C) as the alternative if the maintainer will not accept a stored definition changing meaning.
  - Pending: the modified *The default retry policy carries Temporal-style values* and *Validate refuses a retry policy outside its documented ranges*, and the added *A retry policy that does not set MaxAttempts is bounded*.
- **Q2 (kartaladev/wrkflw#140). Where does the variables bound live, over which dimensions, with which defaults, and with which HTTP status?**
  - Options: (a) the driver at ingress, per payload and total (D3); (b) per payload only; (c) the stores; (d) HTTP only, as today.
  - Recommendation: (a), with 1 MiB, depth 64 and 8 MiB, and HTTP 413 `variables_too_large`.
  - Pending: all three added `process-variables` requirements and the added `http-transport` requirement. The specs state no default number, so the numbers can change without a spec edit.
- **Q3 (kartaladev/wrkflw#184). Per field, per document or at the decode boundary; fixed or configurable?**
  - Options: (a) per document at the authoring decode boundary, configurable on `ParseYAML`, with the store load exempt (D4); (b) per field (rule only); (c) documentation only.
  - Recommendation: (a), with a 4 MiB default.
  - Pending: the added *An authored definition document is refused above the byte limit*.

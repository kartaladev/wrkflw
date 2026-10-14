# Design

## Context

See proposal.md, Why, for the four re-measured premises. The current state that shapes the approach (`action/typed.go` on 9c6dd2c1):

- Strict runs two guards. The first is an exact-key check over the top-level map against a name set that `jsonNames` builds once at construction. The second is `DisallowUnknownFields` on the decode, which folds case.
- The decode marshals the input map to JSON and then decodes that JSON. Every nested value goes through `json.Marshal` first, whatever its Go type: `map[string]any`, `map[string]string`, a consumer struct, or a `json.Marshaler`.
- The refusal text becomes `ActionFailed.Err` (`runtime/processdriver_action.go`, `err.Error()`). From there it reaches `_errorMessage`, `Incident.Error`, `FailInstance.Err`, and the `instance.failed` outbox payload (`runtime/outbox.go` `terminalEventErr`).
- Measurement probe: a scratch module outside the repo that imports the library through a `replace` directive, run on go1.26.9. Its output is quoted in the proposal table.

## Goals / Non-Goals

**Goals:**
- A strict typed action never lets a non-exact key bind at any depth.
- The number of keys a refusal names is bounded.
- A lenient `In` that cannot hold an object is refused before any input arrives.

**Non-Goals:**
- Changing lenient decoding of any key.
- Bounding the length of a single key name. That is `bound-untrusted-input` (kartaladev/wrkflw#140).
- Path syntax as a promise. Nested offenders may be shown with a path, but the specs promise only that the key is named.

## Decisions

### D1. The nested check walks the marshalled JSON, not the Go map (pending Q1)

At construction, strict builds a schema tree from `In`:
- a struct node holds its exact JSON names, each mapped to a child node;
- a slice, array or map-value node holds an element node;
- a pointer node is followed through;
- an opaque leaf stands for `any`, a map with `any` values, `json.RawMessage`, and any type implementing `json.Unmarshaler` or `encoding.TextUnmarshaler`.

Recursive types are handled by memoising each node per `reflect.Type`, so the tree is a finite graph. At invocation, the bytes `decodeInto` already produces are scanned with `json.Decoder.Token` against that graph. Every object key at a struct node is collected that is not an exact name. Opaque subtrees are skipped. The existing top-level map check is subsumed.

- *Rejected: walking the `map[string]any` directly.* A nested value need not be a `map[string]any`. The `map[string]string` row of the spec, a consumer struct, or a `json.Marshaler` would each need its own branch, and a missed branch fails open. The marshalled bytes are the exact input encoding/json then binds, so checking them cannot disagree with the decode.
- *Rejected: a custom `UnmarshalJSON` per `In`.* That cannot be added to a consumer's type.
- *Rejected: `encoding/json/v2` exact matching.* See Q1.
- `DisallowUnknownFields` stays as the backstop for the embedded-name conflict that `jsonNames` over-accepts (this is documented on `decodeInto`).

**Evidence:** the probe shows nested binding through a pointer, a slice element and a map value, as well as through a direct struct field. Each is a different Go shape at the input, and all of them are uniform in the marshalled bytes.

### D2. One collection, one cap, byte order (pending Q3)

Top-level and nested offenders go into one list. The list is sorted byte-wise by the key as named, and the first ten are quoted with `strconv.Quote`, as today. A remainder suffix such as `and 19990 more` is appended when more were refused. A nested offender is rendered with its JSON path (for example `order.orderid`, `items[1].NAME`) so it can be found; the path is diagnostic text, not a spec promise. Sorting by the rendered path keeps the text deterministic.

- *Rejected: a byte budget instead of a count.* The issue proposes a count. A byte budget would cut a key mid-name. Single-key length belongs to kartaladev/wrkflw#140.
- *Rejected: naming only the first key, as `DisallowUnknownFields` did before PR #135.* The review that motivated naming every key wanted several keys named so a consumer can fix them in one pass.

**Evidence:** the measured message grows by `len(Quote(k))+2` per key without bound (proposal table). With ten keys it is bounded by ten key lengths plus a constant.

### D3. Lenient `In` shape at construction (pending Q2)

`Typed` panics unless `In`, behind at most one pointer, is a struct, a map, or an interface with no methods. This mirrors `validOutType` and the existing strict rule, and it keeps construction panics as the single place shape errors surface.

- *Rejected: refuse at invocation.* The input is already fixed, so refusing then only moves a programming error to run time. It would also still need a nil-input special case.
- *Rejected: document only.* The measured nil-input path is fail-open, and the project rule forbids silent decay to a zero value.
- *Rejected: allowing any pointer depth.* `**T` decodes today, but no other `Typed` rule allows it. Consistency with `Out` is cheaper than a special case. This is BREAKING for `**T` users, and Q2 lets the maintainer reverse it.

**Evidence:** the probe's `#137` rows. A nil input reaches `string`, `int`, `[]any` and `chan int` with no error. A non-empty input fails with `ErrDecodeInput`.

### D4. Documentation of the Go-cased rule (kartaladev/wrkflw#143)

The `WithStrictInput` godoc gains one sentence: an untagged exported field's JSON name is its Go field name, matched exactly, so `Untagged` is accepted and `untagged` is refused, while lenient binds both. The godoc's "known, documented limit" paragraph on nested fold keys is replaced by the D1 behavior. The `Typed` godoc's line "In carries NO constraint in lenient mode" is replaced by the D3 rule. These comments name `ErrDecodeInput` and the `typed-action-input` capability spec.

## Coupling

- **`bound-untrusted-input`** (kartaladev/wrkflw#138, #140) owns how this text is persisted (#138: a value-free persisted string, with the raw error kept in `Cause`) and the ingress bound on variable keys (#140). Both touch the refusal message's path from `action/typed.go` to `runtime/processdriver_action.go`. There is no required order. Whichever lands second rebases its message assertions. This change owns the message content and the cap.
- **`reserve-engine-variable-keys`** (kartaladev/wrkflw#187) owns fold-variant engine-stamped keys on cancel, completion and compensation input. It edits the `Typed` godoc paragraph "The reservation covers the PRIMARY service task's input only". This change edits neighbouring paragraphs of the same comment, so there is a textual merge only.
- **`refuse-case-variant-json-keys`** (active) solves the same encoding/json fold for definition JSON in `definition/model`. The packages differ, and neither imports the other, so no code is shared and there is no landing order. Its proposal's pointer "kartaladev/wrkflw#142 owns it" now resolves to this change. Its D2 measurement on `encoding/json/v2` is the one Q1 relies on, re-measured here.

## Risks / Trade-offs

- [Strict invocations now scan the whole input] → The scan runs over bytes already produced. A benchmark (task 2.5) compares strict before and after on a 1,000-key nested input, and the result is recorded in the PR.
- [A consumer's strict payload relied on nested fold binding] → BREAKING, called out in the proposal. The refusal is loud and names the key.
- [The cap hides `_idempotencyKey` when ten or more offenders sort before `_` (uppercase or digit-leading keys)] → The diagnostic in "Strict without the declared stamp fails the service task by name" degrades only for such inputs. The remainder count still signals truncation. Accepted. Revisit if Q3 chooses to always name engine-stamped keys first.
- [The schema misses a position encoding/json decodes into] → A test enumerates every field kind of a fixture `In` and asserts that each is either walked or opaque by name (task 2.2).

## Migration Plan

No data migration. Consumers who use strict with non-exact nested keys, or who register a lenient `In` of a shape that cannot hold an object, must fix their types or payloads. Rollback is a revert. Persisted rows keep whatever text they were written with.

## Open Questions

None of these is answered in any issue thread: kartaladev/wrkflw#137, #142, #143 and #144 have no comments. The specs carry the recommended option for each, marked pending here. Task 0.1 rewrites the affected requirements if the maintainer chooses otherwise.

- **Q1 (kartaladev/wrkflw#142). Recursive exact-key checking now, or wait for `encoding/json/v2`?**
  - Options: (a) implement D1 now; (b) wait for v2 and keep the documented limit.
  - Recommendation: (a). On go1.26.9, `go doc encoding/json/v2` reports "no buildable Go source files" without `GOEXPERIMENT=jsonv2`, so (b) has no date and would impose a build flag on consumers. Meanwhile the nested twin is a silent outcome.
  - Pending under (a): the requirement *Strict input rejects a nested key that is not exactly a declared JSON name*. Under (b) it is removed and replaced by a requirement pinning the limit.
- **Q2 (kartaladev/wrkflw#137). Does lenient constrain `In`, and how?**
  - Options: (a) construction panic per D3; (b) the same but allowing any pointer depth; (c) document the nil-input fail-open only.
  - Recommendation: (a).
  - Pending: the modified *Typed refuses an unusable action shape at construction* requirement and its two new scenarios.
- **Q3 (kartaladev/wrkflw#144). Cap, and at what count?**
  - Options: (a) ten; (b) another count; (c) no cap, leaving the bound to kartaladev/wrkflw#140.
  - Recommendation: (a). It is enough to fix a typical payload in one pass, and the text stays small.
  - Pending: the requirement *A strict refusal names at most ten offending keys and counts the rest*, and the words "within the bound on named keys" in the modified top-level requirement.

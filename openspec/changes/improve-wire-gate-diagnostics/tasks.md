# Tasks

Commands: `T` stands for `GOTOOLCHAIN=go1.26.9 go test -race`. Dialect rows need Docker. Groups 1, 2, 6 and 7 do not depend on OQ1 to OQ3 and can start once `refuse-unrecognized-wire-values` is archived. Groups 3, 4 and 5 start only after a maintainer has resolved their open question; the "Pending" markers are removed from the spec deltas then.

## 0. Preconditions

- [ ] 0.1 Confirm `refuse-unrecognized-wire-values` is archived, and re-derive every citation in proposal.md against `main`: `checkNodeKeys`, `fromWire`, `fromNodeYAML`, `MarshalJSON`, `definitions.go:198/:202`, `validate_test.go:977` and `rulespec_test.go`. Record any drift in the PR body. Verify: each citation opens at the cited symbol.
- [ ] 0.2 Record the OQ1 to OQ3 decisions in design.md, and update or remove the "Pending" requirements to match. Verify: `openspec validate improve-wire-gate-diagnostics --strict` passes.

## 1. Decode refusals: path, completeness, order (kartaladev/wrkflw#217)

- [ ] 1.1 Red: in `definition/model`, add table tests for the scenarios *The refusal names every enclosing subprocess*, *Several refusals are reported together*, *Refusals from different gates are reported together*, *The report is capped* and *A decode refusal outranks a nested structural defect*, in JSON and YAML. Verify: `T ./definition/model/ -run 'Path|OnePass|SameRefusals'` fails on the JSON path rows, the multi-refusal rows and the YAML C8 row, for the reasons measured in proposal.md.
- [ ] 1.2 Green (design D1, D4): collect node decode refusals with `errors.Join`, wrapped with their subprocess path and capped at 20 plus a count. A single top-level refusal keeps its exact text. Verify: 1.1's rows pass, *A top-level refusal keeps its message* passes, and every existing test in `node_wire_keys_test.go`, `node_wire_kindgate_test.go` and `strict_decoding_test.go` stays green.
- [ ] 1.3 Green (design D2): JSON decodes `subprocess` from raw bytes after the outer level's gates, extending the path. Verify: the JSON rows of 1.1 pass, *Negative control - a legal key at depth still decodes* passes, and `T ./definition/model/...` is green.
- [ ] 1.4 Green (design D3): `ParseYAML` gates the whole tree before it builds any inline subprocess. Verify: the YAML rows of 1.1 pass, and *Negative control - a nested structural defect is still reported in YAML* passes.
- [ ] 1.5 Add *The report is deterministic*: 50 decodes, 10 of them concurrent under `-race`. Verify: `T ./definition/model/ -run Deterministic`.
- [ ] 1.6 Add the store rows of *A stored definition reports the path on load*, on Postgres, MySQL and SQLite, through `GetDefinition`, using the existing dialect helpers. Verify: `T ./internal/persistence/store/ -run StoredPath` (Docker).
- [ ] 1.7 Extend `node_wire_keys_bench_test.go` with a three-level nested clean definition. Verify: `GOTOOLCHAIN=go1.26.9 go test -run '^$' -bench NodeKeys -benchmem ./definition/model/` shows no more allocs/op on the clean path than before 1.2, compared with benchstat.
- [ ] 1.8 Document: update the godoc of `fromWire`, `checkNodeKeys` and `ParseYAML` (order, path, cap), citing `openspec/specs/definition-wire-format`. Verify: `scripts/check-doc-refs.sh` and `scripts/lint.sh ./...` pass.

## 2. Gate and probe share one access path (kartaladev/wrkflw#218)

- [ ] 2.1 Red: an internal test showing that the gate's top-level index read and `probeWire`'s `FieldByIndex` disagree for a field reached through an embedded struct, using a test-only struct run through the same enumeration and read helpers. Verify: `T ./definition/model/ -run AccessPath` fails, or, if the hypothesis does not reproduce, record that in design D5 and keep the test as a guard.
- [ ] 2.2 Green (design D5): the gate reads with `FieldByIndex`, and the field enumeration refuses an anonymous field. Verify: 2.1 passes, and *Negative control - legal keys on every node decode* and the full `T ./definition/model/...` are green.

## 3. Cyclic subprocess graphs (kartaladev/wrkflw#204, after OQ1)

- [ ] 3.1 Red: tests for *A cycle is refused at any length* and *Validation refuses the cycle*. Each encode case runs in a subprocess (`exec` of the test binary with an env guard), because the defect aborts the process. Verify: `T ./definition/model/ -run Cyclic` fails, and the child process exits with `fatal error: stack overflow`.
- [ ] 3.2 Green (design D6): add `model.ErrCyclicSubprocess`; give the encoder an ancestor-set recursion and `Validate` the refusal. Verify: 3.1 passes in-process, *Negative control - a shared subprocess definition encodes* and *Negative control - a shared acyclic subprocess definition validates* pass, and the golden round-trip test stays byte-identical.
- [ ] 3.3 Add *Registration refuses the cycle and claims no key* (`runtime/kernel`, `runtime`) and *Publishing a cyclic graph does not abort the process* on three dialects. Verify: `T ./runtime/... ./internal/persistence/store/ -run Cyclic` (Docker).
- [ ] 3.4 Document: replace the `MarshalJSON` comment "Tracked as a follow-up, not fixed here" with the constraint, naming `ErrCyclicSubprocess`. If `make-definition-registration-idempotent` has landed, update its `Register` godoc risk note. Verify: `scripts/check-doc-refs.sh` passes, and `git grep -n "Tracked as a follow-up" definition/model` is empty.

## 4. Inline rule keys independent of the toolchain (kartaladev/wrkflw#235 half 1, after OQ2)

- [ ] 4.1 Red: add the rows of *A nested non-string key is refused* to `TestRuleSpecCodecRejectsMalformedInput`, asserting `ErrInvalidRule` and a zero rule, not stdlib text. Verify: `GOTOOLCHAIN=go1.27.1 go test ./definition/model/ -run RuleSpecCodec` fails on the int rows (`stages: {1: review}` and `stages: [{1: review}]`), and `T` with go1.26.9 passes.
- [ ] 4.2 Green (design D7): `RuleSpec`'s YAML arm walks the node tree and refuses a nested non-`!!str` key, naming its path. Verify: 4.1 passes on both go1.26.9 and go1.27.1, and *Negative control - string keys are accepted*, *A top-level integer key keeps its documented coercion* and *A JSON inline rule is unaffected* pass on both.
- [ ] 4.3 Document: update the `UnmarshalYAML` comment in `rulespec.go` (which says a nested int-keyed mapping "still lands in json.Marshal") and the `rulespec_test.go:403-405` comment. Verify: `scripts/lint.sh ./...` passes.

## 5. def_ref refusal names the field (kartaladev/wrkflw#235 half 2, after OQ3)

- [ ] 5.1 Red: a parity test over stdlib, gin and fiber for *A malformed def_ref is refused naming the field*. Verify: under go1.27.1 every adapter fails the `def_ref` assertion on the non-string rows; under go1.26.9 the `""` rows fail on every adapter, because today's message omits the field.
- [ ] 5.2 Green (design D8): `StartInput` decodes `def_ref` as raw bytes and wraps `ErrInvalidQualifier` naming `def_ref`, and `Qualifier.UnmarshalJSON` refuses a non-string itself. Verify: 5.1 passes on go1.26.9 and go1.27.1, and *Negative control - a valid def_ref starts an instance* passes on all three adapters.
- [ ] 5.3 Rewrite the `type mismatch` row of `TestBinderSurvivesTheBufferSwap`, and the baseline note at the top of `gin_bodycap_test.go`, to the library-owned message. Verify: `GOTOOLCHAIN=go1.27.1 go test ./transport/http/gin/` and `T ./transport/http/...` both pass.

## 6. Eligibility keys pinned on both wire structs (kartaladev/wrkflw#208)

- [ ] 6.1 Add the internal reflection test from design D9 (closed `Eligible*` set on `NodeWire`, the same on `nodeYAML`, and full key correspondence with the explicit JSON-only list). Its rationale is that the eligibility keys are published wire contract. Verify: `T ./definition/model/ -run WireTwin` passes. Then temporarily add an `EligibleGroups` field to `NodeWire` only, watch it go red, and revert.
- [ ] 6.2 Confirm nothing is re-added under `internal/atrest`. Verify: `git grep -n Eligible internal/atrest` is empty.

## 7. Blank-flow-ID test comment (kartaladev/wrkflw#225)

- [ ] 7.1 Replace the justification at `validate_test.go:977` with the reason given in `ErrDuplicateFlowID`'s godoc (design D10). Verify: `git grep -n -E -i 'looks a flow up by ID|nothing looks' -- '*.go'` is empty (note `-E`), and `T ./definition/model/ -run Validate` is green.

## 8. Integration

- [ ] 8.1 Run the full suite on the declared toolchain and on the next one. Verify: the per-module loop from CONTRIBUTING.md passes with `GOTOOLCHAIN=go1.26.9` (Docker), and `GOTOOLCHAIN=go1.27.1 go test ./definition/... ./transport/...` passes.
- [ ] 8.2 Verify: `openspec validate improve-wire-gate-diagnostics --strict`, `scripts/check-openspec.sh`, `scripts/check-doc-refs.sh` and `scripts/check-test-timeout.sh` all pass.

## Workflow follow-up

- The PR body names this change. Its last commit runs `openspec archive improve-wire-gate-diagnostics`.
- After merge, comment on and close kartaladev/wrkflw#217, #218, #204, #235, #208 and #225, naming this change.

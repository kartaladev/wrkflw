# Tasks

Before writing any Go, start with the `cc-skills-golang:golang-how-to` skill. Tables follow the project `table-test` skill: `assert` closures and `t.Context()`. Run every command from the repo root.

## 1. Reproduce the defect (red)

- [ ] 1.1 Export `ErrCaseVariantKey` in `definition/model`, with only enough body for the tests to compile. Its godoc states the constraint and cites `openspec/specs/definition-wire-format/spec.md`. Add a table test `TestUnmarshalJSONRefusesCaseVariantKeys` in `definition/model/strict_decoding_test.go`. It has one row per Examples row of scenario *A JSON key that case-folds onto a defined key is refused at every level*, and one per row of *A case variant cannot shadow the defined key*. Each row decodes through `json.Unmarshal` and asserts `errors.Is(err, model.ErrCaseVariantKey)` and that both spellings are named. Watch every row go red, showing nil errors: `GOTOOLCHAIN=go1.26.9 go test ./definition/model/ -run 'TestUnmarshalJSONRefusesCaseVariantKeys' -count=1`.
- [ ] 1.2 Add the negative controls as `TestUnmarshalJSONCaseVariantNeighbours` in the same file, in the same table form. These are the rows of *A key that does not fold onto a defined key stays an unknown key*, which assert an error naming the key and `!errors.Is(err, ErrCaseVariantKey)`, and of *Exact keys and case-insensitive values are still accepted*. Confirm the controls are green before the fix and the 1.1 rows are red: `GOTOOLCHAIN=go1.26.9 go test ./definition/model/ -run 'TestUnmarshalJSON(RefusesCaseVariantKeys|CaseVariantNeighbours)' -count=1 -v`.

## 2. Refuse case-variant keys (green)

- [ ] 2.1 Implement the exact-key scan from design D1:
  - one token walk at the top of `ProcessDefinition.UnmarshalJSON`, before `dec.Decode`;
  - per-position key sets derived once by reflection from the decode types' `json` tags;
  - `rule` and `subprocess` values skipped;
  - the first fold-equal non-exact key in document order is refused with the D5 error shape.

  This makes scenarios *A JSON key that case-folds onto a defined key is refused at every level*, *A case variant cannot shadow the defined key*, *A key that does not fold onto a defined key stays an unknown key*, and *Exact keys and case-insensitive values are still accepted* pass. Verify with: `GOTOOLCHAIN=go1.26.9 go test ./definition/model/ -run 'TestUnmarshalJSON' -count=1`.
- [ ] 2.2 Add a test that every struct-typed field reachable from the definition decode type has a scan position, with `rule` and `subprocess` excluded by name (design Risks, schema drift). Add a test that, for every defined key and each ASCII case permutation of its first three letters, plus the U+212A and U+017F substitutions, the scan's verdict matches whether encoding/json binds the key (design Risks, fold relation). Verify with: `GOTOOLCHAIN=go1.26.9 go test ./definition/model/ -run 'TestCaseVariantScan' -count=1`.
- [ ] 2.3 Add a test for scenario *Many case variants yield a bounded error*: 500 case-variant keys, `ErrCaseVariantKey`, the first key named, and the error shorter than the input. Verify with: `GOTOOLCHAIN=go1.26.9 go test ./definition/model/ -run 'TestUnmarshalJSONCaseVariantErrorIsBounded' -count=1`.
- [ ] 2.4 Add tests that pin YAML parity and library output. For scenario *YAML refuses the same case variants*, the YAML rows go through `ParseYAML` and assert an error naming the authored key; YAML code is unchanged. For scenario *Library-encoded definitions are never refused as case variants*, the golden file and the all-fields YAML fixture are encoded then decoded with no `ErrCaseVariantKey`. Verify with: `GOTOOLCHAIN=go1.26.9 go test ./definition/model/ -run 'TestCaseVariant(YAMLParity|LibraryOutput)' -count=1`.
- [ ] 2.5 Refactor: consider `/simplify` on the scan, keep `node_wire.go`'s decode comments accurate, and re-run the whole package plus the doc-ref guard: `GOTOOLCHAIN=go1.26.9 go test ./definition/... -count=1 && scripts/check-doc-refs.sh`.

## 3. Definition store surfaces the refusal (all dialects)

- [ ] 3.1 In `internal/persistence/store/definitions_conformance_test.go`, add a `forEachDialect` test that inserts an out-of-band row with raw SQL. The row's JSON carries `"ID"` in place of `"id"`. Assert `errors.Is(err, model.ErrCaseVariantKey)` from both `GetDefinition` and `Lookup` at latest (scenario *A stored row carrying a case variant fails to load on every dialect*). Then publish a valid definition at the same key and version, and assert the error says the stored definition cannot be decoded, does not wrap `ErrCaseVariantKey`, and does not contain `ID` (scenario *Republishing over such a row does not disclose the stored key*). No store code is expected to change; if it must, stop and revisit design. Verify, with Docker running: `GOTOOLCHAIN=go1.26.9 go test ./internal/persistence/store/ -run 'TestDefinitionStoreCaseVariantRow' -count=1`.

## 4. Cost and integration

- [ ] 4.1 Run `BenchmarkGoldenDecode` on main and on the branch, `GOTOOLCHAIN=go1.26.9 go test ./definition/model/ -run '^$' -bench 'BenchmarkGoldenDecode' -benchmem -count=10`, and put the `benchstat` comparison in the PR body (design D4).
- [ ] 4.2 Run the full suite per module with Docker, using the CONTRIBUTING.md form, and the OpenSpec guard: `mods="$(scripts/modules.sh)"; for pat in $(printf '%s\n' "${mods}"); do GOTOOLCHAIN=go1.26.9 go test -race "${pat}"; done && scripts/check-openspec.sh`.
- [ ] 4.3 Run the scenario-to-test map check: every scenario in `specs/definition-wire-format/spec.md` names the test that covers it in the PR body, and each named test ran green under 4.2.

## Workflow follow-up

- Before 1.1, a human confirms design D3: no lenient mode, and the operator repair recipe. Readiness then flips to agent.
- The PR body names this change, marks it **BREAKING**, flags the persisted-format impact, and carries the D3 repair recipe.
- The PR's last commit runs `openspec archive refuse-case-variant-json-keys`.

# Tasks

Gate: Q1 to Q3 in design.md are answered by the maintainer before group 1 starts. Start every Go task with `cc-skills-golang:golang-how-to`. Write table tests in the `table-test` form. Run commands from the repository root on the toolchain `go.mod` declares. No task needs Docker: the change touches no store.

## 0. Gate

- [ ] 0.1 Record the maintainer's answers to Q1, Q2 and Q3 under design.md Open Questions. For each answer that differs from the recommendation, rewrite the pending requirement it names in `specs/typed-action-input/spec.md` and the matching task group below. Verify: `openspec validate harden-typed-action-input --strict` passes, and design.md lists no unanswered question.

## 1. Pin the Go-cased rule for untagged fields (kartaladev/wrkflw#143)

- [ ] 1.1 Add rows to the strict table in `action/typed_test.go`:
  - `untagged`, `UNTAGGED` and ` Untagged` are refused with `ErrDecodeInput` naming the key;
  - `Untagged` is accepted;
  - lenient binds `untagged`.

  These are characterization rows and are expected green. A red row means the premise moved, so stop and report. Scenarios: "Strict refuses keys that are not exact declared names" (the three Untagged rows), "Strict accepts exact declared names" (the Untagged row), and "Lenient binds the lower-cased name of an untagged field". Verify: `GOTOOLCHAIN=go1.26.9 go test -race ./action/ -run 'TestTypedDo|TestBaselineTypedActionInput' -count=1`.
- [ ] 1.2 Add the D4 sentence to the `WithStrictInput` godoc, naming the `typed-action-input` capability spec. Verify: `bash scripts/check-doc-refs.sh` and `scripts/lint.sh ./action/...` pass.

## 2. Exact keys at every depth under strict (kartaladev/wrkflw#142, pending Q1)

- [ ] 2.1 Red. Add a nested-strict table test covering every row of the following scenarios:
  - "Strict refuses a non-exact key at every nested struct position";
  - "Strict refuses a non-exact key two levels down";
  - "A shadowing nested twin can no longer win";
  - "Negative control - exact nested keys are accepted at every position";
  - "Positions that decode arbitrary keys stay open";
  - "A self-referential In terminates at any input depth".

  In `action/typed_test.go`, invert the row "strict does NOT reject a nested case-variant (documented limit)" to expect `ErrDecodeInput`. Each refusing row asserts `ErrDecodeInput`, `IsRetryable` false, the key in the error text, and that the function was not invoked. Verify: `GOTOOLCHAIN=go1.26.9 go test ./action/ -run 'Typed' -count=1` fails on the refusing rows, and the negative-control and opaque rows pass.
- [ ] 2.2 Green. Implement D1: the schema graph is built once in `Typed` under strict, memoised per `reflect.Type`, and the check is a token scan of the marshalled input. Replace the top-level-only check, and keep `DisallowUnknownFields`. Add the schema-coverage test from design Risks. It uses a fixture `In` with one field of every kind and asserts that each is walked or opaque. Verify: the 2.1 command passes, and `GOTOOLCHAIN=go1.26.9 go test -race ./action/... ./runtime/ -run 'Typed' -count=1` passes, `runtime/action_typed_e2e_test.go` included.
- [ ] 2.3 Add a concurrency test that invokes one strict action from 64 goroutines with exact and variant inputs. Scenario: "Concurrent invocations of one strict action are independent". Verify: `GOTOOLCHAIN=go1.26.9 go test -race ./action/ -run 'Concurrent' -count=5`.
- [ ] 2.4 Replace the godoc on `WithStrictInput`, `decodeInto` and the `Typed` lenient paragraph that describes the nested limit with the D1 behavior. Name `ErrDecodeInput` and the capability spec. Verify: `bash scripts/check-doc-refs.sh` and `scripts/lint.sh ./action/...` pass.
- [ ] 2.5 Add `BenchmarkTypedStrict` with a flat 10-key input and a nested 1,000-key input. Run it with `-count=10` on the parent commit and on this one, and record the `benchstat` comparison in the PR body. Verify: `GOTOOLCHAIN=go1.26.9 go test ./action/ -run '^$' -bench 'TypedStrict' -benchmem -count=10`.
- [ ] 2.6 Refactor. Consider `/simplify` on `action/typed.go`. Verify: the 2.2 command passes.

## 3. Cap the named keys (kartaladev/wrkflw#144, pending Q3)

- [ ] 3.1 Red. Add a table test with these rows:
  - "Named keys by offending-key count" (1, 9, 10, 11 and 20000);
  - "The named keys are the first in byte order";
  - "The refusal text is identical across invocations";
  - "The cap covers nested and top-level offenders together".

  Each row counts the quoted keys in the error text and checks the remainder suffix. Verify: `GOTOOLCHAIN=go1.26.9 go test ./action/ -run 'Typed' -count=1` fails on the 11 and 20000 rows and on the byte-order and combined rows.
- [ ] 3.2 Green. Implement D2: one sorted list of offenders, the first ten quoted, and the remainder suffix. Rewrite the `rejectUnknownKeys` blast-radius comment for the bounded message. Keep its escaping and injection notes, and name the `typed-action-input` capability spec. Verify: the 3.1 command passes, `GOTOOLCHAIN=go1.26.9 go test -race ./action/... ./runtime/ -count=1` passes, and `bash scripts/check-doc-refs.sh` passes.
- [ ] 3.3 Confirm that the strict service-task row in `runtime/action_typed_e2e_test.go` still names `_idempotencyKey`. Scenario: "Strict without the declared stamp fails the service task by name". Verify: `GOTOOLCHAIN=go1.26.9 go test -race ./runtime/ -run 'Typed' -count=1`.

## 4. Constrain lenient In at construction (kartaladev/wrkflw#137, pending Q2)

- [ ] 4.1 Red. Add the rows of "Lenient construction refuses an In that cannot hold a JSON object" to `TestTypedConstruction`. Add the test for "A lenient In that cannot hold an object no longer runs on a nil input". Before the fix it shows a `string` In invoked with a nil input, returning no error, with the function called. Verify: `GOTOOLCHAIN=go1.26.9 go test ./action/ -run 'TestTypedConstruction|Lenient' -count=1` fails on the panics rows.
- [ ] 4.2 Green. Implement D3 beside `validOutType`. Replace "In carries NO constraint in lenient mode" in the `Typed` godoc with the rule. Scenarios: "Construction outcome by type shape" (unchanged rows stay green), plus the two 4.1 scenarios. Verify: the 4.1 command passes, `GOTOOLCHAIN=go1.26.9 go test -race ./action/... ./runtime/... ./engine/... -count=1` passes, and `bash scripts/check-doc-refs.sh` passes.

## 5. Integration

- [ ] 5.1 Run the full gate. Verify:
  - `openspec validate harden-typed-action-input --strict` passes;
  - `bash scripts/check-openspec.sh` passes;
  - the full per-module race suite from CONTRIBUTING.md passes;
  - `scripts/lint.sh ./...` passes in each module.

## Workflow follow-up

- The PR body names `harden-typed-action-input` and carries the 2.5 benchstat table.
- The last commit runs `openspec archive harden-typed-action-input --yes`.

# Tasks

Every Go task starts with the `cc-skills-golang:golang-how-to` skill, then runs red, green, refactor. Waits and deadlines follow `docs/agents/eventually-waits.md` and `docs/agents/test-deadlines.md`. `GOTOOLCHAIN=go1.26.9` is set on every command. Store and parity rows need Docker.

## 1. Gate

- [ ] 1.1 Record the maintainer's answers to Q1, Q2 and Q3 (design, Open Questions) in design.md. For any question answered differently from the recommendation, rewrite the pending requirements with `/opsx:update` before starting the group that depends on it. Verify with `openspec validate bound-untrusted-input --strict` and `scripts/check-openspec.sh`. Group 2 depends on no question and may start before the gate.

## 2. Value-free decode text (kartaladev/wrkflw#138, D1)

- [ ] 2.1 Write the failing table test in `action/typed_test.go` for *The decode-failure text omits the input value*, covering every Examples row. Watch the numeric-literal rows (`1.5`, `123456789.25`, `300`, `-7`, `1e+300`, nested `9.5`) and the Go-type-name assertion go red. Verify with `go test -run TestTyped ./action/...`, red before 2.2.
- [ ] 2.2 Return the unexported decode error type from `action.Typed`, as D1 describes: value-free `Error()`, and `Unwrap() []error` yielding `ErrDecodeInput` and the decoder's error. Make 2.1 green. Add *The decoder's own error stays reachable in memory* and both negative-control scenarios. Verify with `go test -race ./action/...`.
- [ ] 2.3 Add the runtime test for *The recorded action failure and every sink derived from it are value-free*. It drives a service task with a recovery flow and asserts the recorded failure text, `_errorMessage`, and an `ErrorCheck` closure reaching `*json.UnmarshalTypeError` through the cause. Verify with `go test -race -run TestTypedDecodeText ./runtime/...`.
- [ ] 2.4 Rewrite the `ErrDecodeInput` and `rejectUnknownKeys` godoc in `action/typed.go`. Persisted text carries keys and kinds, never values; cite `openspec/specs/typed-action-input`. Verify with `scripts/check-doc-refs.sh` and `go doc ./action ErrDecodeInput`.

## 3. Bounded retry attempts (kartaladev/wrkflw#139, D2; pending Q1)

- [ ] 3.1 Write the failing tests for *A retry policy that does not set MaxAttempts is bounded*. Cover the node-tier "MaxAttempts omitted, 3rd failure" row, the action-tier and runtime-default rows, and the compensation scenario. Each currently arms a timer forever. Verify with `go test -run 'TestRetry|TestCompensationRetry' ./engine/... ./runtime/...`, red before 3.2.
- [ ] 3.2 Add `model.UnlimitedAttempts`. Change `Normalize` and `Validate`, and the attempt tests at `engine/step_triggers.go:465` and `engine/step_compensation.go:602`. Make 3.1 green, and add the new Examples rows of *Normalize fills unset fields and keeps set ones* and *Retry policy validation by field values*. Verify with `go test -race ./definition/... ./engine/... ./runtime/...`.
- [ ] 3.3 Add the JSON and YAML rows of *The unlimited spelling survives the wire in both formats* to the definition round-trip tests. Verify with `go test -race ./definition/model/...`.
- [ ] 3.4 Rewrite the `MaxAttempts` godoc on `model.RetryPolicy`, `RetryPolicy.Normalize`, `action.RetrySpecs` and `runtime.WithCompensationRetryPolicy`, so that none says "0 means unlimited". Verify with `grep -rn "0 means unlimited\|== 0 means UNLIMITED" --include='*.go' .`, which prints nothing.
- [ ] 3.5 Update the examples that rely on an unset `MaxAttempts`, if any. Verify by building and testing the examples module with the `scripts/modules.sh` loop in CONTRIBUTING.md.

## 4. Variables ceilings (kartaladev/wrkflw#140, D3; pending Q2)

- [ ] 4.1 Reproduce the hypothesis first. In the store conformance suite, write a test that starts an instance with a variables map nested 20,000 deep through the ProcessDriver, then reloads it. Record whether it commits and then fails to load on in-memory, Postgres, MySQL and SQLite. Watch it fail, or record in design.md that the hypothesis did not reproduce. Verify with `go test -race -run TestDeepVariables ./internal/persistence/... ./runtime/...` (Docker).
- [ ] 4.2 Write the failing table tests for *Per-payload size boundary on every ingress*, *Multi-byte and escaped content is measured as encoded*, *The default limit applies without configuration* and *A disabled limit admits a large payload*. Verify with `go test -run TestVariablesLimit ./runtime/...`, red before 4.3.
- [ ] 4.3 Add the driver options, `runtime.ErrVariablesTooLarge` and `runtime.ErrVariablesTooDeep`, and the per-payload size and depth checks on start, signal, message and task completion. Make 4.2 and *Depth boundary* green. Make *A variable deeper than the store codec can read is never committed* green on all four stores. Make 4.1 green. Verify with `go test -race ./runtime/... ./internal/persistence/...` (Docker).
- [ ] 4.4 Write and then pass the tests for the total limit: *Accumulation across payloads that are each within the per-payload limit*, *A payload that overwrites keys is measured after the merge*, *An action output past the total fails the action non-retryably*, *Two concurrent deliveries cannot jointly exceed the total* (run under `-race`, on a SQL store and in memory) and *An instance already above a lowered total still loads and advances without growing*. Verify with `go test -race -run TestVariablesTotal ./runtime/... ./internal/persistence/...` (Docker).
- [ ] 4.5 Make *A refused payload leaves no trace* pass on in-memory, Postgres, MySQL and SQLite. Verify with `go test -race -run TestVariablesLimitNoTrace ./internal/persistence/... ./runtime/...` (Docker).
- [ ] 4.6 Benchmark the total check at the recommended 8 MiB, and the per-payload walk at 1 MiB, on each dialect. Record the result in design.md D3 and fix the defaults. Verify with `go test -run '^$' -bench BenchmarkVariablesLimit ./runtime/...` and benchstat output in the PR.
- [ ] 4.7 Classify both sentinels as 413 `variables_too_large` in `httpcore`. Make *Variables-limit refusal per route and adapter*, *The refusal envelope discloses neither the limit nor the payload* and its negative control pass across stdlib, gin and fiber. Verify with `go test -race ./transport/http/...`.
- [ ] 4.8 Godoc the options and sentinels, and state on `engine.Step` that the pure engine applies no limit. Cite `openspec/specs/process-variables`. Verify with `scripts/check-doc-refs.sh`.

## 5. Definition document ceiling (kartaladev/wrkflw#184, D4; pending Q3)

- [ ] 5.1 Write the failing table test for *Byte-limit boundary in both formats*, including the node-name and 50,000-node rows that show the rule key is not special. Verify with `go test -run TestDefinitionTooLarge ./definition/model/...`, red before 5.2.
- [ ] 5.2 Add `model.ErrDefinitionTooLarge`, the default, the `ParseYAML` `LoaderOption`, the limited reader, and the length check at the top of `ProcessDefinition.UnmarshalJSON`. Make 5.1, *The limit counts bytes, not characters*, *An oversize document is refused without decoding its content* and the round-trip negative control green. Verify with `go test -race ./definition/...`.
- [ ] 5.3 Route the definition stores' load path around the ceiling, then make *A definition already stored is not re-checked on load* pass on Postgres, MySQL and SQLite. Seed the oversize row by direct SQL. Verify with `go test -race -run TestDefinitionLoadIgnoresLimit ./internal/persistence/...` (Docker).
- [ ] 5.4 Add an allocation benchmark that compares a refused oversize document with today's 5x and 13x figures, and record it in the PR. Godoc the sentinel, the option and the `RuleSpec.Inline` note, citing `openspec/specs/definition-wire-format`. Verify with `go test -run '^$' -bench BenchmarkDefinitionTooLarge -benchmem ./definition/model/...` and `scripts/check-doc-refs.sh`.

## 6. Integration

- [ ] 6.1 Run the full suite and the repo checks across both modules: the `scripts/modules.sh` build, test and lint loops in CONTRIBUTING.md, `scripts/check-doc-refs.sh`, `scripts/check-openspec.sh` and `openspec validate bound-untrusted-input --strict`. All exit 0 (Docker).

## Workflow follow-up

- The PR body names `bound-untrusted-input`. Its last commit runs `openspec archive bound-untrusted-input`.
- After merge, comment on and close whichever of kartaladev/wrkflw#138, #139, #140 and #184 are still open. Normally the planning PR's conversion has closed them already.

# Tasks

All Go commands run on the toolchain `go.mod` declares (go1.26.8 when the baseline was measured; go1.26.9 after kartaladev/wrkflw#243; go1.27 fails tests that open kartaladev/wrkflw#235 covers). Dialect rows need Docker.

## 1. Re-run the evidence map (D5, D6)

- [x] 1.1 Run every test named in `evidence.md` (per package, `go test ./<pkg> -run '<names>'`, Docker up for the Postgres, MySQL and SQLite rows). Verify: each named test passes; for a row whose test fails, remove that row or requirement from its spec, mark it in `evidence.md`, and record it in design D7.

## 2. Characterization tests for the `NO TEST` rows (D4)

Each test is written, run and expected green. A red test proves a defect: remove the row from the spec, keep the test for its follow-up change (design D7), and fix nothing here. Each task covers the `NO TEST` rows of the named scenarios in `evidence.md`, and ends with those rows updated to the new test's name.

- [x] 2.1 `definition-wire-format`: *An unknown or retired kind name is refused* (the error names the value), *YAML refuses an unknown kind name*. Verify: new tests in `definition/model` pass.
- [x] 2.2 `definition-validation`: *A builder definition with no start event is refused* and *A YAML definition with no start event is refused at Build* (assert `errors.Is(err, ErrNoStartEvent)`), *Each structural defect is refused by its sentinel* (the two-blank-ID row), *A json-schema strategy round-trips through its descriptor*. Verify: new and tightened tests pass.
- [x] 2.3 `definition-store`: *Republishing identical content is a no-op on every backend* (read-back on the scoped-actions rows), *A duplicate version with different content is refused* (in-memory registry and `RegisterDefinition`). Verify: new tests pass on all three dialects.
- [x] 2.4 `operator-incidents`: *Each stuck shape raises a definition-defect incident* (sub-process row names the token), *Non-action incidents are refused and kept* (the `IncidentDefinitionDefect` row). Verify: new tests in `engine` pass.
- [x] 2.5 `typed-action-input`: *Strict refuses keys that are not exact declared names* (the U+017F and U+212A rows, and `IsRetryable == false` on the Nested, Skipped and secret rows), *Strict accepts exact declared names* (the `sKey`, `kKey` row), *An absent envelope decodes to the zero In* (the `{}` row). Verify: new rows in `action` pass.
- [x] 2.6 `process-variables`: *A dropped payload is not merged* (completed and terminated rows), *A lenient typed action sees the engine's stamp, not a caller's variant*, *An empty start-of-visit snapshot leaves the variables untouched*. Verify: new tests in `engine` and `runtime` pass.
- [x] 2.7 `expression-evaluation`: *Invalid conditions are errors*, *Duration by result form*, *Correlation key by result form*, *Validation outcome by input* (each error names its expression), *A runaway expression is abandoned under the opt-in timeout* (timer duration and correlation key through the driver). Verify: new tests pass.
- [x] 2.8 `retry-policy`: *The first retry parks the token and counts the attempt*, *Terminal conditions end retrying* (a plain `action.NonRetryable` error under a policy, and the `MaxElapsed` row), *The legal neighbours of each terminal condition are retried*, *Substring matching of NonRetryableErrors* (the empty-substring row), *Normalize fills unset fields and keeps set ones*, *Retry policy validation by field values* (the seven rows besides `BackoffCoef -1`). Verify: new rows in `engine` and `definition/model` pass.
- [x] 2.9 `http-transport`: *Admin routes registered without authentication* (the body lists the instance), *Status, code and message rendering per error class* (the 409 `conflict` code). Verify: new assertions pass on stdlib, gin and fiber.
- [x] 2.10 `outbound-http-calls`: *The boundary is hostname equality, not Go's subdomain rule* (the `sub.api.internal` row), *Retryability by response* (408 and unreachable target), *Validator rejects the body*, *An over-cap request body is never sent*. Verify: new rows in `action/httpcall` pass.
- [x] 2.11 `human-task-authorization`: *A refused operation changes nothing*, *The forged-actor refusal carries the forbidden code* (stdlib, gin), *The pre-empting 401 carries the unauthenticated code* (gin, fiber), *Unauthenticated claim is refused identically on every adapter*, *Unevaluable predicates fail closed as errors*. Verify: new tests pass.
- [x] 2.12 `human-task-lifecycle`: *Closed task on a running instance* (a mutation check that can fail), *Terminal instance* (completed row), *Claim invariant on write* (memory-store upserts). Verify: new tests pass.
- [x] 2.13 `instance-disclosure`: *Categories are independent* (`{actors}` restores candidate actor IDs). Verify: new test in `runtime/view` passes.
- [x] 2.14 `event-delivery`: *Calls after Close* (Subscribe and Start return `ErrBusClosed`), *A failed row is not claimed before its backoff elapses*, and the per-dialect relay rows driven through the `persistence` package's public relays (*A drain publishes every pending row on every dialect*, *Poison isolation and quarantine on every dialect*, *A publish failure is absorbed, not propagated*, *Run absorbs failures and exits on cancel on every dialect*, *Listing and redriving on every dialect*, *Stats on every dialect*, *Postgres drains on notification before the poll interval*, *MySQL and SQLite still drain by polling*). Verify: new tests pass on all three dialects.
- [x] 2.15 `instance-chaining`: *Handle outcomes* (the transient row and `starts=1`). Verify: tightened test in `runtime/chain` passes.
- [x] 2.16 `instance-store`: *Duplicate and fresh IDs* (the fresh-ID rows on every dialect), *Capping closed visits* (through a store configured with a history cap), *Second migration is a no-op* (SQLite). Verify: new tests pass.
- [x] 2.17 `restart-recovery`: *RehydrateTimers re-arms resolvable timers and reports the rest*, *Unarmable rows are dropped, healthy rows survive* (the healthy row's next run is non-zero). Verify: new tests in `runtime` pass.
- [x] 2.18 `instance-id-generation`: *XID yields distinct hyphen-free identifiers* (second call's error and hyphen-freeness), *Func adapts a plain function* (the error row's empty value). Verify: tightened tests in `runtime/idgen` pass.

## 3. Reconcile and validate

- [x] 3.1 Apply every removal from tasks 1.1 and 2.x to the specs and `evidence.md`; confirm no requirement is left without a scenario and no `NO TEST` row remains. Verify: `grep -c 'NO TEST' evidence.md` prints 0, `openspec validate establish-baseline-specs --strict` passes, and `scripts/check-openspec.sh` exits 0.
- [x] 3.2 Confirm every scenario header is `#### Scenario:` and carries exactly one gherkin block. Verify: `grep -rn '^#### Scenario Outline' openspec/changes/establish-baseline-specs/specs` prints nothing, and per capability the count of `#### Scenario:` headers equals the count of opening gherkin fences.

## Workflow follow-up

- Commit, push, and open the PR naming this change (after confirmation). It is stacked on PR #242.
- Last commit of the PR: `openspec archive establish-baseline-specs --yes`, which creates the 21 specs under `openspec/specs/`.
- After merge: comment on kartaladev/wrkflw#20 pointing at `openspec/specs/` and close it.
- After merge: create one change per finding group in design D7 with `openspec new change`, each citing its findings and carrying a Coordination block (`Depends on: establish-baseline-specs`), with full delta specs that MODIFY the archived baseline; where an open GitHub issue already covers a finding, add it to that issue's migration instead. This waits for archive because a proposal-only change fails `openspec validate --strict` and MODIFIED deltas need the baseline specs to exist.
- Next: the migration of the 85 open issues into changes, wave 1 first.
